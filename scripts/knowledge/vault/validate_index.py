#!/usr/bin/env python3
"""Validate the bundle-root index.md against 99 - Meta/index-contract.md.

Four checks:
  1. every index.md bullet resolves to an existing 01 - Notes/**/*.md file
  2. every such fact file has required frontmatter: type, created, source
  3. no fact file is orphaned (unreferenced by index.md, by any
     02 - MOCs/*.md hub, or by any other fact's [[wikilink]]/markdown link)
  4. no index.md bullet is missing a source link entirely

Reports violations. Does not repair anything. Exit 0 = contract holds
(warnings may still be printed). Exit 1 = at least one hard violation.

Lives at 99 - Meta/tools/ (moved from agent/tools/ under the agent/
dissolution, run/inmaps-spec.md §7b). A2-COMPLETION (run/iteration-queue.md,
the last item on §7b's disposition map) retired agent/ entirely:
agent/index.md's role -- the file actually loaded at session start, the
sole okf_version carrier, the subject of this validator -- moved to a new
index.md at the vault root. agent/INDEX-CONTRACT.md became
99 - Meta/index-contract.md, content otherwise unchanged. A first A2 fix
pass briefly moved the carrier onto Start Here.md; a Director review
corrected that against OKF §12/§8's own spec text, which names the
bundle-root index.md FILENAME specifically -- index.md is the subject
here, Start Here.md stays the vault's separate human-entry-point page,
outside this validator's scope entirely (it has no capped bullet list of
its own to check). There is no agent/ left; vault_dir() below computes the
vault root directly from this file's own location, no longer detouring
through a retired agent/ subdirectory.

Index-link subdir support (P9, run/iteration-queue.md): the link regex
below is built from 99 - Meta/note-subdirs.md's own registry table rather
than hardcoding each subdir name a second time -- adding a row there is
enough for a link into that subdir to validate; nothing here needs a
matching edit. Accepts both a root-relative link (as used inside index.md
itself, e.g. "01 - Notes/01f - Facts/<id>.md") and a one-level-down link
(as used inside a 02 - MOCs/*.md hub or another fact under 01 - Notes/,
e.g. "../01 - Notes/01f - Facts/<id>.md") -- the same regex validates
links written from either depth.

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
SUBDIR_ROW_RE = re.compile(r"^\| `(\d{2}[a-z])` \| `01 - Notes/([^`/]+)/` \|", re.M)

# Named, not a blanket "any dot-prefixed directory is scratch" -- that shape
# already burned this same check once (below): trusting a category by
# default instead of naming what's actually in it. `.obsidian` and `.trash`
# are Obsidian's own; `.p15-fix-backup`, the numbered `.source-*-backup-*`
# and `.inmaps-backup-*` directories are migration/tool-generated snapshots
# actually present in the real vault (enumerated with os.walk, not just the
# vault root -- the root-only pass this list started from missed 56
# `.inmaps-backup-*` dirs nested under 99 - Meta/, agent-estate#1296 fix
# pass). Each currently holds zero *.md files (checked directly), so this
# list names them for what they are rather than leaving them an accident of
# "rglob('*.md') never happened to look there yet." A real vault directory
# someone later dot-prefixes is not silently exempted by this list the way
# it would be by `startswith(".")`.
SCRATCH_DIR_RE = re.compile(
    r"^\.(?:obsidian|trash|p15-fix-backup"
    r"|source-(?:hub|index|view)-backup-\d+"
    r"|inmaps-backup-\d+)$"
)


def vault_dir():
    """Vault root: an explicit path via argv[1], or -- with no argument --
    two directories up from this file's own location (.../99 - Meta/tools/
    validate_index.py -> vault root), which is what the vault's own
    deployed copy relies on and must keep working unargued.

    Director finding, second fix pass on PR #1296: this repo-tracked copy
    is meant to be runnable against any vault by path, not only from its
    own deployed location inside one -- that is the entire point of
    tracking it here separately. Before this fix argv was read nowhere in
    this file at all: a path argument was silently ignored, the derived
    path was used regardless, and a caller outside the vault either got a
    confusing FileNotFoundError against the wrong tree or -- worse, had
    that wrong tree happened to contain its own index.md -- a clean
    contract report about entirely the wrong vault. A given path is
    validated as a real vault root (index.md present) and any mismatch
    exits loudly here, before main() does anything with it -- never a
    silent fall-back to the derived path instead."""
    if len(sys.argv) > 1:
        given = sys.argv[1]
        if not os.path.isdir(given):
            sys.exit(f"error: not a directory: {given!r}")
        if not os.path.isfile(os.path.join(given, "index.md")):
            sys.exit(
                f"error: {given!r} is not a vault root -- no index.md found "
                "there. Refusing to fall back to this file's own location."
            )
        return given
    here = os.path.dirname(os.path.abspath(__file__))  # .../99 - Meta/tools
    meta_dir = os.path.dirname(here)                    # .../99 - Meta
    return os.path.dirname(meta_dir)                    # vault root


def registered_note_subdirs(vault_dir_path):
    """Read 99 - Meta/note-subdirs.md's own table and return the set of
    registered subdirectory names (e.g. "01f - Facts", "01p - Parameters").
    Missing or unparseable registry is treated as zero registered subdirs
    -- a link into an unregistered subdir then correctly fails to
    validate, same as before this subdir existed, rather than silently
    accepting anything."""
    registry = Path(vault_dir_path) / "99 - Meta" / "note-subdirs.md"
    if not registry.is_file():
        return set()
    rows = SUBDIR_ROW_RE.findall(registry.read_text(encoding="utf-8"))
    return {name for _prefix, name in rows}


def build_link_re(subdirs):
    """A link target is (optionally "../")01 - Notes/<optionally one
    registered subdir>/(<12- or 14-digit-id>|index).md -- built from the
    registry, never a second hardcoded list. The leading "../" is
    optional so the same regex validates links written from index.md
    itself (vault root, no "../" needed) and links written one level down
    (02 - MOCs/*.md hubs, other 01 - Notes/**/*.md facts)."""
    if subdirs:
        alt = "|".join(re.escape(s) for s in sorted(subdirs))
        subdir_part = f"(?:(?:{alt})/)?"
    else:
        subdir_part = ""
    return re.compile(r"(?:\.\./)?01 - Notes/" + subdir_part + r"(?:\d{12}|\d{14}|index)\.md")


def load_frontmatter_keys(path):
    text = Path(path).read_text(encoding="utf-8")
    m = FRONTMATTER_RE.match(text)
    if not m:
        return None
    return set(re.findall(r"^([a-zA-Z_]+):", m.group(1), re.M))


def links_in_text(text, notes_link_re):
    """Return (md_targets, wikilink_targets) found anywhere in text.

    md_targets is normalized to a vault-root-relative path (leading
    "../" stripped) regardless of which depth the link was written from
    -- fact_files below is keyed the same way, since index.md itself sits
    at the vault root. Without this, a link written one level down (a
    02 - MOCs/*.md hub, another fact under 01 - Notes/) would carry its
    own "../" into md_targets and never match a fact_files key that was
    never given one."""
    md_targets = set()
    for _, p in MD_LINK_RE.findall(text):
        target = unquote(p)
        if notes_link_re.fullmatch(target):
            md_targets.add(target.removeprefix("../"))
    wiki_targets = set(WIKILINK_RE.findall(text))
    return md_targets, wiki_targets


def main():
    vault = vault_dir()
    index_path = os.path.join(vault, "index.md")

    subdirs = registered_note_subdirs(vault)
    notes_link_re = build_link_re(subdirs)

    # Fact files: every 01 - Notes/**/<12- or 14-digit-id>.md, keyed relative to
    # the vault root (root-relative, matching how index.md's own bullets
    # link them -- unlike the pre-A2 scheme, which kept facts/ under
    # agent/ and keyed relative to that).
    paths = [p for p in (Path(vault) / "01 - Notes").rglob("*.md")
              if re.fullmatch(r"\d{12}|\d{14}", p.stem)]
    fact_files = {os.path.relpath(p, vault): p for p in paths}
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
        md_targets, wiki_targets = links_in_text(body, notes_link_re)
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
        md_targets, wiki_targets = links_in_text(text, notes_link_re)
        referenced_by_facts |= resolve(md_targets, wiki_targets)

    # --- generated hubs (P10, run/iteration-queue.md): 02 - MOCs/*.md
    # each bulk-list one area's notes (e.g. Parameters.md lists every
    # 01p - Parameters/*.md note) -- a note reachable only through its own
    # generated hub, not through index.md directly, is not orphaned; scan
    # every hub the same way index.md's own bullets are scanned.
    referenced_by_hubs = set()
    mocs_dir = Path(vault) / "02 - MOCs"
    if mocs_dir.is_dir():
        for hub in sorted(mocs_dir.glob("*.md")):
            text = hub.read_text(encoding="utf-8")
            md_targets, wiki_targets = links_in_text(text, notes_link_re)
            referenced_by_hubs |= resolve(md_targets, wiki_targets)

    # --- check 3a: every Markdown link resolves ---
    #
    # This check exists because on 2026-09-07 the vault carried 63 links to
    # files that did not exist -- hubs pointing at iCloud conflict copies
    # ("20260805165028 2.md") that were later deleted, and log entries still
    # naming the retired agent/facts/ layout -- and this tool reported
    # "Contract holds" throughout. Checks 1-3 only ask whether every NOTE is
    # reachable; nothing asked whether every LINK arrives somewhere. A reader
    # following a dead link and a reader finding nothing look identical, which
    # is the failure this vault's own rules name (it-d43a08d739bf32a8).
    #
    # A target containing "<" is a documented placeholder in a contract or a
    # spec ("01 - Notes/<subdir>/<id>.md"), not a link anyone can follow.
    for path in sorted(Path(vault).rglob("*.md")):
        # Named exemptions only (SCRATCH_DIR_RE above) -- see its comment for
        # why this isn't a blanket "any dot-prefixed directory" skip.
        if any(SCRATCH_DIR_RE.match(part) for part in path.parts):
            continue
        try:
            text = path.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError) as e:
            # A read that fails and a file with nothing wrong in it must not
            # look the same (it-d43a08d739bf32a8) -- fail closed, matching
            # how every other read_text() in this file behaves (raise and
            # halt), rather than silently skipping whatever link check this
            # file would have failed.
            hard_violations.append(
                f"{path.relative_to(vault)}: could not read for link check -> {e}"
            )
            continue
        for m in re.finditer(r"\]\(([^)]+\.md)\)", text):
            target = unquote(m.group(1))
            if target.startswith(("http://", "https://")) or "<" in target:
                continue
            if not (path.parent / target).exists():
                hard_violations.append(
                    f"{path.relative_to(vault)}: link does not resolve -> {target}"
                )

    # --- check 3: orphaned facts ---
    referenced = referenced_by_index | referenced_by_facts | referenced_by_hubs
    orphaned = sorted(set(fact_files) - referenced)
    for fn in orphaned:
        hard_violations.append(
            f"{fn}: orphaned — not referenced by index.md, a hub, or any other fact"
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
