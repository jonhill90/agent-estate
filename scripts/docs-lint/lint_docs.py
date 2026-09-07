#!/usr/bin/env python3
"""Enforce the docs/ classification standard (P12 phase 2, run/p12-execution-plan.md).

Jon's own framing for why this exists, verbatim: "Classification is
judgment; enforcement is a gate. Third cleanup = last manual cleanup." P12
phase 1 did the judgment (run/p12-estate-disposition.md, cross-lane
reviewed); this script is the gate, so a fourth unreviewed drift back into
loose root files or leaked state never needs a fourth manual sweep.

Language: Python, not Go. This is TOOLING, not the app -- the estate's own
Go-only rule (docs/orientation/go-only.md) binds src/estate, not scripts/,
and this repo already has exactly this shape of precedent:
scripts/knowledge/vault/validate_index.py, a read-only, exit-code-driven
repo-hygiene checker with its own unittest-based test file, run directly
via python3, not wired into any Go build. This script matches that
convention rather than inventing a second scripting language for the same
job.

THREE RULES, each independently checkable and independently mutation-tested
(see test_lint_docs.py):

  1. No unclassified root file. A file sitting directly in docs/ (not in
     any subdirectory) is unclassified unless it is either a recognized
     TOMBSTONE (see TOMBSTONE_MARKER below) or named in ROOT_ALLOWLIST with
     a stated reason. Today's one allowlisted entry is
     docs/knowledge-workflow.md -- Phase 4 (P12's live-repo-pointers phase)
     owns that file and it stays at docs/ root deliberately, not because
     nobody classified it.

  2. No state file (.json/.jsonl) anywhere under docs/, recursively --
     docs/ is documentation, not a place a running process writes to.
     Today's two exceptions, EXPLICIT AND NAMED, not a wildcard:
     docs/tick-log.jsonl and docs/tick-escalations.jsonl are live state
     written by the Director cron loop RIGHT NOW via
     internal/tick.Path()/EscalationPath(), resolved against the process's
     own cwd, pinned by src/estate/tick_check_discloses_path_test.go. P12
     phase 1's disposition table judged relocating them in the SAME PR as
     this lint too risky to do safely -- moving them requires repointing
     TWO independent subsystems (internal/tick AND src/tui/cmd/estate's own
     separate hardcoded default) while a live process may be writing to the
     old path during the sync window. That is deferred, deliberate work,
     not an oversight; this lint would otherwise fail on it forever, which
     is exactly why the exemption is explicit and cited here rather than a
     silent gap.

  3. Zero full-text duplicates under docs/, by sha256 checksum of every
     .md file's exact bytes. Two files with identical content is either an
     accidental copy-paste or an abandoned draft sitting beside its own
     replacement -- either way, a human should look, not the gate silently
     tolerate it.

Exit 0 = all three rules hold. Exit 1 = at least one violation, printed
with enough detail (paths, both files of a duplicate pair) to act on
without re-deriving what failed.
"""
import hashlib
import sys
from pathlib import Path

# Rule 1 exemption: files that legitimately sit at docs/ root, with the
# reason a human would need to know before "fixing" this list.
ROOT_ALLOWLIST = {
    "knowledge-workflow.md": (
        "Phase 4 (P12 live-repo-pointers) owns this file; it is real, "
        "current content deliberately kept at docs/ root, not an "
        "unclassified leftover -- see run/p12-execution-plan.md"
    ),
}

# Rule 1: a tombstone's first line names exactly where its content moved.
# Matched literally, not by regex -- a tombstone is produced by this same
# migration, in this exact shape, and a file merely mentioning "Moved to"
# in unrelated prose is not a valid escape from classification.
TOMBSTONE_PREFIX = "Moved to `docs/canonical/"
TOMBSTONE_PREFIX_HISTORICAL = "Moved to `docs/historical/"

# Rule 2 exemption: named, not wildcarded. Adding a path here must be a
# deliberate, documented decision (see the module docstring), never a
# quick fix for a lint failure.
STATE_FILE_EXEMPTIONS = {
    "docs/tick-log.jsonl": (
        "live state, internal/tick.DefaultPath, written by the Director "
        "cron loop right now -- P12 phase 1 deferred relocating it, see "
        "run/p12-estate-disposition.md's own row for the full cost"
    ),
    "docs/tick-escalations.jsonl": (
        "live state, internal/tick.DefaultEscalationPath, same deferral "
        "as docs/tick-log.jsonl"
    ),
}

STATE_FILE_SUFFIXES = (".json", ".jsonl")


def is_tombstone(path):
    try:
        with open(path, "r", encoding="utf-8") as f:
            first_line = f.readline()
    except (OSError, UnicodeDecodeError):
        return False
    return first_line.startswith(TOMBSTONE_PREFIX) or first_line.startswith(
        TOMBSTONE_PREFIX_HISTORICAL
    )


def check_unclassified_root_files(docs_dir):
    violations = []
    for entry in sorted(docs_dir.iterdir()):
        if entry.is_dir():
            continue
        if entry.suffix in STATE_FILE_SUFFIXES:
            # Rule 2's job entirely, not rule 1's -- a state file is never
            # "classified" in the canonical/historical/research sense, so
            # it would otherwise need a second, redundant exemption here on
            # top of STATE_FILE_EXEMPTIONS. Whether it belongs at docs/
            # root at all is what rule 2 decides.
            continue
        if entry.name in ROOT_ALLOWLIST:
            continue
        if is_tombstone(entry):
            continue
        violations.append(
            f"unclassified root file: docs/{entry.name} -- not a "
            f"recognized tombstone and not in ROOT_ALLOWLIST "
            f"(lint_docs.py). Move it into a classified subdirectory "
            f"(docs/canonical/, docs/historical/, docs/research/, ...) "
            f"or add it to ROOT_ALLOWLIST with a stated reason."
        )
    return violations


def check_no_state_files(docs_dir, repo_root):
    violations = []
    for path in sorted(docs_dir.rglob("*")):
        if path.is_dir():
            continue
        if path.suffix not in STATE_FILE_SUFFIXES:
            continue
        rel = path.relative_to(repo_root).as_posix()
        if rel in STATE_FILE_EXEMPTIONS:
            continue
        violations.append(
            f"state file in docs/: {rel} -- {path.suffix} is a state "
            f"format, not documentation, and is not in "
            f"STATE_FILE_EXEMPTIONS (lint_docs.py). Relocate it out of "
            f"docs/ entirely, or if this is a deliberate, reviewed "
            f"exception, name it explicitly in STATE_FILE_EXEMPTIONS "
            f"with a cited reason."
        )
    return violations


def check_no_duplicates(docs_dir):
    by_hash = {}
    for path in sorted(docs_dir.rglob("*.md")):
        if path.is_dir():
            continue
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        by_hash.setdefault(digest, []).append(path)
    violations = []
    for digest, paths in sorted(by_hash.items()):
        if len(paths) > 1:
            names = ", ".join(str(p) for p in paths)
            violations.append(
                f"full-text duplicate (sha256 {digest[:12]}): {names} -- "
                f"byte-identical content under two paths; a human should "
                f"decide which is canonical, this gate does not."
            )
    return violations


def main():
    repo_root = Path(__file__).resolve().parents[2]
    docs_dir = repo_root / "docs"
    if not docs_dir.is_dir():
        print(f"docs-lint: {docs_dir} is not a directory -- nothing to check", file=sys.stderr)
        return 1

    violations = []
    violations += check_unclassified_root_files(docs_dir)
    violations += check_no_state_files(docs_dir, repo_root)
    violations += check_no_duplicates(docs_dir)

    if violations:
        print(f"docs-lint: {len(violations)} violation(s):\n")
        for v in violations:
            print(f"  - {v}")
        return 1

    print("docs-lint: clean -- no unclassified root files, no state files in docs/, "
          "zero full-text duplicates.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
