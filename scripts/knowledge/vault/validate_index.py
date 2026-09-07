#!/usr/bin/env python3
"""Validate agent/index.md against INDEX-CONTRACT.md.

Four checks across legacy facts and migrated INMAPS notes:
  1. every index.md bullet resolves to an existing facts/*.md file
  2. every facts/*.md file has required frontmatter: type, created, source
  3. no facts/*.md file is orphaned (unreferenced by index.md or by any
     other fact's [[wikilink]]/markdown link)
  4. no index.md bullet is missing a source link entirely

Reports violations. Does not repair anything. Exit 0 = contract holds
(warnings may still be printed). Exit 1 = at least one hard violation.

Lives at 99 - Meta/tools/ (moved from agent/tools/ under the agent/
dissolution, run/inmaps-spec.md §7b) but still validates agent/index.md
and agent/facts/ -- vault_root() below computes the vault root from this
file's OWN location, then explicitly targets agent/, rather than assuming
(as the pre-move version did) that its own parent directory IS agent/.

Run from 99 - Meta/:  python3 tools/validate_index.py
"""
import os
import re
import sys
from pathlib import Path
from urllib.parse import unquote

BULLET_RE = re.compile(r"^- (.*)$")
MD_LINK_RE = re.compile(r"\[([^\]]+)\]\(([^)]+\.md)\)")
WIKILINK_RE = re.compile(r"\[\[([a-zA-Z0-9_-]+)\]\]")
FRONTMATTER_RE = re.compile(r"^---\n(.*?)\n---\n", re.S)
REQUIRED_FRONTMATTER = ("type", "created", "source")
RECOMMENDED_FRONTMATTER = ("title", "description")


def vault_root():
    here = os.path.dirname(os.path.abspath(__file__))  # .../99 - Meta/tools
    meta_dir = os.path.dirname(here)                    # .../99 - Meta
    vault = os.path.dirname(meta_dir)                   # vault root
    return os.path.join(vault, "agent")                 # agent/ -- unmoved this batch


def load_frontmatter_keys(path):
    text = Path(path).read_text(encoding="utf-8")
    m = FRONTMATTER_RE.match(text)
    if not m:
        return None
    return set(re.findall(r"^([a-zA-Z_]+):", m.group(1), re.M))


def links_in_text(text):
    """Return (md_targets, wikilink_targets) found anywhere in text."""
    md_targets = {unquote(p) for _, p in MD_LINK_RE.findall(text)
                  if unquote(p).startswith("facts/") or
                  re.fullmatch(r"\.\./01 - Notes/(?:01p - Parameters/)?(?:\d{12}|index)\.md", unquote(p))}
    wiki_targets = set(WIKILINK_RE.findall(text))
    return md_targets, wiki_targets


def main():
    root = vault_root()
    facts_dir = os.path.join(root, "facts")
    index_path = os.path.join(root, "index.md")

    paths = list(Path(facts_dir).glob("*.md"))
    paths += [p for p in (Path(root).parent / "01 - Notes").rglob("*.md")
              if re.fullmatch(r"\d{12}", p.stem) or p.parent.name == "01p - Parameters" and p.name == "index.md"]
    fact_files = {os.path.relpath(p, root): p for p in paths}
    aliases = {}
    for key, path in fact_files.items():
        text = path.read_text(encoding="utf-8")
        fm = FRONTMATTER_RE.match(text)
        names = [path.stem]
        if fm:
            match = re.search(r"^aliases: *\[([^\]]*)\]", fm.group(1), re.M)
            if match:
                names += [v.strip().strip("\"'") for v in match.group(1).split(",") if v.strip()]
            block = re.search(r"^aliases: *\n((?:[ \t]*- .*(?:\n|$))+)", fm.group(1), re.M)
            if block:
                names += [v.strip().strip("\"'") for v in re.findall(r"^[ \t]*- (.*)$", block.group(1), re.M)]
        for name in names:
            aliases.setdefault(name, set()).add(key)

    def resolve(md, wiki):
        found = set(md)
        for name in wiki:
            targets = aliases.get(name, set())
            if len(targets) != 1:
                found.add("unresolved-or-ambiguous-wikilink:" + name)
            else:
                found.update(targets)
        return found
    index_text = Path(index_path).read_text(encoding="utf-8")

    hard_violations = []
    warnings = []

    # --- check 1 + 4: walk index.md bullets ---
    referenced_by_index = set()
    for lineno, line in enumerate(index_text.splitlines(), start=1):
        m = BULLET_RE.match(line)
        if not m:
            continue
        body = m.group(1)
        md_targets, wiki_targets = links_in_text(body)
        if not md_targets and not wiki_targets:
            hard_violations.append(
                f"index.md:{lineno}: bullet has no source link — {body[:70]!r}"
            )
            continue
        for target in resolve(md_targets, wiki_targets):
            referenced_by_index.add(target)
            if target not in fact_files:
                hard_violations.append(
                    f"index.md:{lineno}: links to {target}, which does not exist"
                )

    # --- check 2: frontmatter on every fact file ---
    referenced_by_facts = set()
    for fn in sorted(fact_files):
        path = fact_files[fn]
        keys = load_frontmatter_keys(path)
        if keys is None:
            hard_violations.append(f"{fn}: no frontmatter block found")
        else:
            missing_req = [k for k in REQUIRED_FRONTMATTER if k not in keys]
            if missing_req:
                hard_violations.append(
                    f"{fn}: missing required frontmatter key(s): {', '.join(missing_req)}"
                )
            missing_rec = [k for k in RECOMMENDED_FRONTMATTER if k not in keys]
            if missing_rec:
                warnings.append(
                    f"{fn}: missing recommended frontmatter key(s): {', '.join(missing_rec)}"
                )
        # collect sibling wikilinks/links for the orphan check, whether or
        # not this file itself is a real fact/target — a broken link inside
        # a fact body is not this contract's concern, only what it points at
        text = Path(path).read_text(encoding="utf-8")
        md_targets, wiki_targets = links_in_text(text)
        referenced_by_facts |= resolve(md_targets, wiki_targets)

    # --- check 3: orphaned facts ---
    referenced = referenced_by_index | referenced_by_facts
    orphaned = sorted(set(fact_files) - referenced)
    for fn in orphaned:
        hard_violations.append(
            f"{fn}: orphaned — not referenced by index.md or by any other fact"
        )

    print(f"Checked {len(fact_files)} fact file(s) against {index_path}")
    print()

    if warnings:
        print(f"Warnings ({len(warnings)}, non-blocking):")
        for w in warnings:
            print(f"  - {w}")
        print()

    if hard_violations:
        print(f"VIOLATIONS ({len(hard_violations)}):")
        for v in hard_violations:
            print(f"  - {v}")
        print()
        print("Contract does NOT hold. Fix the above; this tool does not repair them.")
        return 1

    print("Contract holds: no hard violations.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
