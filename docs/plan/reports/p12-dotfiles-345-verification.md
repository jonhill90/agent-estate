# Did #345 satisfy the docs standard? A verification, not a re-sort

Author: agent-estate:3 (lane-c). Scope: P12 item 2 for `agent-dotfiles`.
Everything below was checked directly against a fresh clone of
`jonhill90/agent-dotfiles` at `origin/main` (`5894803`), never the frozen
local checkout at `~/source/repos/Personal/agent-dotfiles`, which was not
touched.

## Background

`run/p12-dotfiles-disposition.md` (lane-a) and its review
(`run/p12-dotfiles-disposition-review.md`, lane-b, two rounds) were both
built against a local checkout 13 commits behind `origin/main`. Verifying
every row against `origin/main` before executing anything (per instruction)
found that a separate, already-merged PR — **#345, "docs: organize
canonical knowledge surfaces"** — had already executed nearly the entire
sort the table was written to plan, inside the 13-commit gap neither lane-a
nor lane-b's checkouts had synced to. The Director's own retarget message
confirmed this and redirected the remaining work to the lint (shipped in
the accompanying PR) plus this verification.

## Does the current tree pass all three docs-lint rules on its own merits?

**Yes — verified by running the actual lint against the actual tree,
before any change of mine, from a fresh clone:**

```
$ python3 scripts/docs_lint.py
ok   -- no unclassified root files under docs/
ok   -- no state files under docs/
ok   -- zero full-text duplicates by checksum

docs-lint: all three rules hold.
```

Not a vacuous pass: every one of the 20 flat root tombstones (`docs/PRD.md`,
`docs/memory.md`, `docs/hierarchy-naming-57.md`, etc.) was individually
checked to confirm its forwarding link actually resolves to a real file
under `docs/canonical/` or `docs/historical/` — all 20 do (verified with
the lint's own tombstone-resolution logic run standalone, not just via the
aggregate pass/fail).

## Things the table never knew about, found by this verification

1. **`docs/canonical/repository-policy.md` and `docs/canonical/agent-roster.md`**
   — both added by #345, in neither lane-a's table nor its review (both were
   built before #345 existed on either author's checkout). Both are real,
   substantial content (365 and 26 lines respectively), both correctly
   linked from `docs/index.md` and cross-referenced from `AGENTS.md`. No
   defect found in either — they are simply new canonical documents this
   phase's own tables have no row for, because they didn't exist when
   those tables were written.
2. **`docs/inmpara-and-coleam-study-2026-08-23.md`** (a `research`-disposed
   row in lane-a's table) no longer exists anywhere in the tree. Traced via
   `git log --oneline --all -- <path>`: deleted by **#329/#330** ("delete
   three documents preserved in skills-private#6"), a separate,
   already-merged, presumably-reviewed deletion that predates #345 by
   several days and has nothing to do with this phase. Not a gap in #345's
   work — the file was already gone before #345 ran.
3. **`docs/.gitkeep`** — still present, untouched, exactly matching lane-a's
   table (`DELETE-CANDIDATE`, listed for Jon, never executed). #345 did not
   touch it either way. Still awaiting Jon's decision, nothing has changed
   about its status.

## What #345 left that a human should look at

**The root `README.md` (and only the README — `AGENTS.md` has zero hits)
still links directly to the OLD flat tombstone paths, not the new
`docs/canonical/`/`docs/historical/` locations:**

```
$ grep -n "docs/PRD.md\|docs/SPEC.md\|docs/provenance-manifest.md\|docs/migration-audit.md" README.md
README.md:8:Product requirements: [docs/PRD.md](docs/PRD.md); technical design:
README.md:9:[docs/SPEC.md](docs/SPEC.md).
README.md:27:new-machine criterion in [docs/PRD.md](docs/PRD.md) is measured against:
README.md:370:issue #45) — see [docs/provenance-manifest.md](docs/provenance-manifest.md).
README.md:384:[docs/SPEC.md](docs/SPEC.md) §4). Each kept tool skill has acceptance
README.md:426:See [docs/provenance-manifest.md](docs/provenance-manifest.md) for every
README.md:428:[docs/migration-audit.md](docs/migration-audit.md), for the original skill
```

Seven links. Every one now lands a reader on a 5-line tombstone
("Continue to...") instead of the real content directly — not broken (the
tombstones all resolve, confirmed above), but a real, avoidable extra hop
on the repository's own front door, for content #345 itself already knows
the real location of. This is squarely **Phase 3's job** ("README.md +
AGENTS.md refreshed... routing-index style, current-truth only") and I have
not touched `README.md` here, per instruction — flagging it as a concrete,
specific, ready-to-fix item for whoever runs that phase, rather than a
vague "check the README" note.

Everything else checked (`settings/README.md`, `tests/test_instruction_globs.py`,
`scripts/memory_lint.py` — the other files matching old-path filenames) are
prose mentions or test-fixture strings, not real path dependencies; none
read or open the real file at its old location, so none are affected by
the tombstone shape.

## Bottom line

`origin/main`'s current tree satisfies all three docs-lint rules on its
own merits, verified independently, not asserted. #345's sort is complete
and correct for the files it touched. The one loose end — `README.md`'s
seven stale-but-not-broken links — is real, specific, and scoped to Phase
3, not something this pass fixes. `docs/.gitkeep` remains an open
DELETE-CANDIDATE for Jon, unchanged.
