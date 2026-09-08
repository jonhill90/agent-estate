Verdict: REQUEST CHANGES
Review-Lane: agent-estate:2

# Cross-lane review — run/p12-dotfiles-disposition.md (lane-a, 23,537 bytes, 15:01)

Reviewer: agent-estate:2 (lane-b). Read `run/p12-execution-plan.md` first,
per instruction. **No destructive git command was run anywhere in
`agent-dotfiles` during this review** — `git status`, `git log`, `git
ls-tree`, `git show`, `git merge-base`, `git check-ignore`, `git ls-files`,
`git branch --contains` only. The frozen tree (`a3f6e09` unpushed, dirty
`hooks/ledger-write-guard.sh`/`apm.lock.yaml`, untracked
`docs/moc-map-of-maps.md`) was read exactly as found and left exactly as
found — confirmed identical at the end of this review (see item 5).

## (1) Completeness — measured, both counts pasted

```
$ find docs -type f | wc -l
45
$ git ls-files docs | wc -l
44
```

The one-file gap is `docs/moc-map-of-maps.md` (untracked in this local
checkout — see item 3 for why that framing itself needs correcting). The
table used `find` (45), states so explicitly in its own opening section
("The checkout is not clean... `docs/moc-map-of-maps.md` is untracked —
see its own row below"), and its "Totals" section states "45 files under
`docs/` (44 real files + `docs/.gitkeep`)" — the right instrument, used,
and the choice stated, not left implicit. Diffed the table's own listed
paths against `find`'s output directly:

```
$ diff <(table's 45 paths) <(find docs -type f | sort)
(empty — exact match)
```

Zero rows missing, zero extra, zero duplicated. **Item (1) passes cleanly.**

## (2) Consumer checks re-run myself — 9 rows, across every disposition class present

`relocate-out-of-docs` has zero rows in this table (noted explicitly:
"nothing found that belongs somewhere else entirely") — there is nothing
in that class to spot-check; I confirmed this is a real claim, not an
omission, by re-running the full consumer sweep myself (item 1's methodology)
and finding no `docs/`-tree file in agent-dotfiles that reads as
misplaced state, unlike agent-estate's `tick-log.jsonl` case.

Re-ran independently (not copied from the table):

- **`docs/.gitkeep`** (DELETE-CANDIDATE) — `git grep`, cross-repo, vault: zero
  real hits, confirmed. The one basename collision the table flags
  (`hooks/.gitkeep`, a different file) is real and correctly distinguished.
- **`docs/corpus/b2-corpus-refresh-2026-08-23.md`** (corpus) — zero hits
  everywhere, confirmed exactly.
- **`docs/inmpara-and-coleam-study-2026-08-23.md`** (research) — zero
  inbound within agent-dotfiles, confirmed exactly.
- **`docs/migration-audit.md`** (historical) — confirmed real: `README.md`,
  `PRD.md`, `provenance-manifest.md` (×2), `docs-layout-council-138.md`
  (multiple), plus the full research-batch citations the table names.
  Count and substance both check out.
- **`docs/PRD.md`** (canonical) — confirmed the "0 real hits, same-basename
  collision" claim: `docs/PRD.md` as a qualified string in agent-estate
  resolves only to agent-estate's OWN `docs/tui/PRD.md`/`docs/tui/SPEC.md`
  cross-referencing EACH OTHER via shorthand (I independently found and
  documented this exact shorthand convention in my own estate-side
  disposition table) — not a reference to agent-dotfiles' file at all.
  Correct.
- **`docs/hierarchy-naming-57.md`** (canonical) — the qualitative claim
  (real cross-repo dependency, code comments in `reference/` + stale
  `.claude/worktrees/` copies) is **confirmed true**. The stated count (6)
  undercounts what I measured (8: 2 real files in `reference/scripts/
  supervisor/` × the 3 stale worktree copies plus... precisely,
  `director-route.sh`+`digest.sh` in `reference/` = 2, and the same pair
  repeated across 3 separate `.claude/worktrees/*/` dirs = 6 more = 8
  total). **Non-blocking** — the conclusion Phase 2 needs (a real
  cross-repo dependency exists, must be handled if this file ever moves)
  is correct either way; only the headline number is off.
- **`docs/supervisor-disposition.md`** (canonical) — the qualitative claim
  is **confirmed true and important**: real code comments cite specific
  section numbers (§1.3, §5, line 359) in agent-estate. But the table's
  SPECIFIC citations (`scripts/supervisor/cli.py:850`,
  `scripts/supervisor/watchdog.sh:96`) **only exist inside stale
  `.claude/worktrees/` snapshots** — the current, real `reference/` tree
  has since renamed these to `cli_dispatch_record.py:97` and
  `watchdog-harness.sh:32`. Someone using the table's citations to find
  and repoint these comments in the CURRENT tree would search for files
  that no longer exist there. **Non-blocking** on its own (the dependency
  itself is real and the table's core warning holds), but worth a
  same-pass fix since it's exactly the kind of stale-citation trap this
  whole phase exists to prevent.
- **`docs/loop-engineering.md`** (canonical) — confirmed the 2-hit
  functional claim (`reference/scripts/estate-loop/check.sh` + its
  worktree copy). Found 3 additional, lower-stakes hits the table didn't
  mention: `AGENTS.md` (real + 2 worktree copies) names "loop-engineering"
  in a generic prose list of what `agent-dotfiles/docs/` carries — not a
  path link, not evidence the table's risk assessment is wrong, just an
  incomplete count. **Non-blocking.**

**No disprovable "zero consumers" claim survived scrutiny** — every
zero-hit row I checked (`.gitkeep`, `b2-corpus-refresh`,
`inmpara-and-coleam-study`) held up exactly. The inaccuracies found are all
undercounts on rows the table already correctly flagged as having real
consumers, not false negatives. Item (2)'s sharpest concern — a row
claiming safety that isn't there — did not reproduce on any of the 9 rows
checked, **except item (3) below, which is a different and more serious
class of claim than a consumer-count miss.**

## (3) The two required rows

### `docs/moc-map-of-maps.md` — **BLOCKING**

The row correctly avoids both failure modes the Director named: it is
**not** filed as DELETE-CANDIDATE, and it is **not** silently classified as
though it were ordinary tracked content. It is marked QUESTION FOR JON,
plainly, at the top of the disposition column. That much is right.

**But the row's stated reason — "Untracked, unowned — git does not know
this file exists" — is factually wrong, and the correction changes what
the actual question for Jon should be.** I checked this directly rather
than trusting the table's premise:

```
$ git log --all --oneline -- docs/moc-map-of-maps.md
9648535 docs: organize canonical knowledge surfaces (#345)
5a653a6 docs: organize canonical knowledge surfaces
28460fc docs: fix absolute machine-local path in moc-map-of-maps.md (#327 CI fix) (#328)
001fa51 docs: drop absolute machine-local path from moc-map-of-maps.md
7db6a6a docs: commit moc-map-of-maps.md, complete and untracked (#321)

$ git ls-tree origin/main -- docs/moc-map-of-maps.md
100644 blob 0f03ae697017f6ccacdfa1054a8a5a19fbc5cfb3	docs/moc-map-of-maps.md

$ git log --oneline -3 origin/main
5894803 fix(instructions): repair agent/ prose after vault A2-COMPLETION (#347)
bf74e8a fix(ledger-write-guard): protect corpus.sqlite3 (agent-estate#P6 rename) (#346)
9648535 docs: organize canonical knowledge surfaces (#345)

$ git merge-base --is-ancestor 7db6a6a HEAD && echo ancestor || echo not-ancestor
not-ancestor
```

**Git knows this file extremely well.** It was added and tracked by PR
#321 ("docs: commit moc-map-of-maps.md, complete and untracked"), fixed by
#328, and most recently edited YESTERDAY by #345 ("docs: organize canonical
knowledge surfaces"), all on `origin/main`, which sits **13 commits ahead**
of this local checkout's own `main` (`git status`'s own "have 1 and 13
different commits each" line — the same divergence the table's opening
section already measures for the OTHER frozen-tree facts, just not applied
here). The commit that first tracked this file (`7db6a6a`) is **not an
ancestor of local HEAD at all** — local `main` simply never merged the lane
that ever tracked it. The file "looks untracked" purely because this one
stale local clone hasn't caught up, not because nobody ever committed it.

It gets more consequential than a stale-clone technicality. I diffed the
local untracked file against what origin/main actually has tracked at this
exact path today:

```
$ diff <(git show origin/main:docs/moc-map-of-maps.md) docs/moc-map-of-maps.md
```

Origin/main's tracked version (5 lines) reads: `# moc-map-of-maps` /
`Status: superseded routing location. Canonical content moved, not
copied.` / `Continue to [moc-map-of-maps](canonical/moc-map-of-maps.md).`
— **this exact file has already been superseded and its real content
relocated to `docs/canonical/moc-map-of-maps.md` on origin/main**, via a
real, merged PR, as of yesterday. The local untracked file is a much older,
full 36-line draft ("Map of maps — where the estate's stores live," the
six-store pointer table the table's row describes) that predates that
supersession.

**Why this is blocking, not a nitpick:** the row's premise — that nobody
has ever decided this file's fate and Jon must originate a judgment from
nothing — is not what's actually true. The fate is already decided and
already executed upstream; this checkout just hasn't synced to it. If
Phase 2 executes this row mechanically as written, the honest reading is
"leave it alone, awaiting Jon's original decision" — but the real, useful
question for Jon is completely different and much narrower: *"origin/main
already superseded this path and moved its content to
`docs/canonical/moc-map-of-maps.md`; this local checkout's untracked draft
predates that and may hold content the supersession didn't carry forward
(compare the two directly) — is there anything worth preserving before this
checkout syncs and either the tracked tombstone silently shadows the
untracked draft, or vice versa?"* That is answerable in one comparison, not
an open-ended ownership question, and Phase 2 asking Jon the wrong question
here is exactly the kind of "wedging the executor" the Director's own bar
warns against.

**The disposition category chosen (QUESTION FOR JON, not DELETE-CANDIDATE,
not silent-normal) is still the right SHAPE of answer** — this is not a
call for the table to have decided anything on its own. It's a call to
correct the row's stated reasoning so Jon is asked the real, narrower,
already-mostly-answered question rather than a broader one that makes the
situation look less resolved than it is.

### `hooks/ledger-write-guard.sh` uncommitted work

Recorded correctly and clearly, in the table's opening section (not buried
in a row, since it isn't a `docs/` file): "work in flight... Phase 2's own
execution against this repo must not `git checkout`/`git restore`/`git
clean` or otherwise assume a clean working tree going in." `apm.lock.yaml`
is named alongside it with the same caveat. **This requirement is
satisfied as written.**

## (4) DELETE-CANDIDATE audit

One row: `docs/.gitkeep`, correctly "listed for Jon; not removed by this
pass or any pass that follows without his say-so" (Totals section,
verified verbatim). No execution occurred (confirmed — see item 5).

**Reverse-error check** (something that should be a DELETE-CANDIDATE filed
as historical instead, to dodge the conversation): checked the strongest
candidate for this failure mode directly —
`docs/research/docs-layout-council-138/claude-adversarial.txt`, which the
table itself quotes as saying it "should be deleted [after Jon acts on
this document's conclusions]." The table disposes it `research`, not
DELETE-CANDIDATE, with the explicit, checkable reason that its own stated
deletion criterion ("if it is still here at the next spec iteration's exit
with nothing left citing it") has not yet been met — `docs-layout-
council-138.md` still cites it today, confirmed in my own row 2/4/6/9
re-checks above. This is not evasion; it is the criterion stated and
correctly judged not-yet-met. I did not find any other row that reads as a
plausible DELETE-CANDIDATE mislabeled to avoid the conversation — the
`corpus/` genre (three dated pass reports, zero consumers each) is a
deliberate archival category the execution plan itself protects
("deliberate history, not staleness"), not a dodge.

## (5) Zero moves, zero edits in agent-dotfiles

```
$ git status
On branch main
Your branch and 'origin/main' have diverged,
and have 1 and 13 different commits each, respectively.

Changes not staged for commit:
	modified:   apm.lock.yaml
	modified:   hooks/ledger-write-guard.sh

Untracked files:
	.worktrees/
	docs/moc-map-of-maps.md
```

Confirmed at the start of this review and again at the end — byte-for-byte
the same four pre-existing entries the Director's own message described,
nothing new. **No STOP-and-report condition triggered.**

## (6) AGENTS.md / CLAUDE.md / knowledge-workflow.md

```
$ ls -la AGENTS.md CLAUDE.md
-rw-r--r--  AGENTS.md
lrwxr-xr-x  CLAUDE.md -> AGENTS.md
$ git status --porcelain AGENTS.md CLAUDE.md docs/knowledge-workflow.md
(empty)
```

Symlink pair present and untouched (zero diff, zero status entries).
`docs/knowledge-workflow.md` does not exist in agent-dotfiles at all
(confirmed against the full 45-file `find` listing above) — not applicable
to this repo, correctly absent from the table rather than invented.

---

## Summary

Items (1), (2) [substantively], (4), (5), (6) all pass, several with real
independent re-verification, not a rubber stamp — this table's consumer
work is careful and its zero-hit claims are trustworthy everywhere I tested
them. Item (3)'s `hooks/ledger-write-guard.sh` half is satisfied. Item (3)'s
`docs/moc-map-of-maps.md` half is not: the row's premise
("unowned/untracked, nobody has decided") is disprovable, and I disproved
it — origin/main tracked, edited, and superseded this exact path yesterday;
this checkout is 13 commits behind. The safe, non-judgmental SHAPE of the
row's disposition (QUESTION FOR JON) is correct and should be kept; its
stated REASONING needs to be corrected to the actual, narrower situation
before Phase 2 treats this row as executable without re-deciding anything.

Two non-blocking accuracy items worth fixing in the same pass: the
`hierarchy-naming-57.md` hit count (6 stated, 8 measured) and
`docs/supervisor-disposition.md`'s specific file:line citations (drawn from
stale `.claude/worktrees/` snapshots — the current `reference/` tree has
since renamed both files).

---

# Re-review — 2026-09-07, corrected table

Scope: the corrected `docs/moc-map-of-maps.md` row, the two non-blocking
accuracy items above, and the required checkout-divergence warning. Per
instruction, the rest of the table (already passed) was not re-reviewed.

`agent-dotfiles` re-checked, read-only, before touching anything:

```
$ git status
On branch main
Your branch and 'origin/main' have diverged,
and have 1 and 13 different commits each, respectively.

Changes not staged for commit:
	modified:   apm.lock.yaml
	modified:   hooks/ledger-write-guard.sh

Untracked files:
	.worktrees/
	docs/moc-map-of-maps.md
```

Exactly the four pre-existing entries, nothing new. No git command that
mutates state was run anywhere in `agent-dotfiles` during this re-review —
`git status`/`git ls-tree`/`git rev-list`/`git show`/`git log`/`diff`
against `git show origin/main:<path>` only.

## (1) The row's reasoning — corrected, not just re-shaped

Independently re-ran every claim the corrected row makes, not trusting the
pasted output:

```
$ git ls-tree origin/main -- docs/moc-map-of-maps.md
100644 blob 0f03ae697017f6ccacdfa1054a8a5a19fbc5cfb3	docs/moc-map-of-maps.md
$ git rev-list --left-right --count HEAD...origin/main
1	13
$ git ls-tree origin/main -- docs/canonical/moc-map-of-maps.md
100644 blob 0f977921c9348089a4fb737d6813def3b7d23b6f	docs/canonical/moc-map-of-maps.md
$ git show origin/main:docs/moc-map-of-maps.md
# moc-map-of-maps

Status: superseded routing location. Canonical content moved, not copied.

Continue to [moc-map-of-maps](canonical/moc-map-of-maps.md).
```

Both blob hashes and the `1	13` divergence match the row exactly, and
match the Director's own independently-stated verification. The row now
states the narrow, nearly-answered question — origin/main already
superseded this path and relocated the content; the local untracked draft
predates that; does it hold anything worth preserving before the checkout
syncs — and no longer poses the open-ended "nobody has decided this."

Also re-ran the characterizing diff the row cites, myself:

```
$ diff <(git show origin/main:docs/canonical/moc-map-of-maps.md) docs/moc-map-of-maps.md
1,6d0
< ---
< type: Document
< knowledge_status: canonical
< updated: 2026-09-06T23:27:40+00:00
< ---
< 
24c18
< | vault | ... | bundle-root `index.md` in the Obsidian vault at `$AGENT_MEMORY_VAULT` (`agent/` retired in full, agent-estate#1275) |
---
> | vault | ... | [`agent/index.md`](/Users/jon/Library/Mobile%20Documents/...) in the Obsidian vault at `$AGENT_MEMORY_VAULT` |
```

Matches the row's characterization exactly — frontmatter present only in
the canonical copy, one row's wording differing (the pre-#328
absolute-machine-local-path vault reference, versus the canonical copy's
already-fixed relative one). Nothing else in the diff. **Confirmed
accurate.**

The disposition SHAPE is unchanged — still `**QUESTION FOR JON**`, in the
row itself, in the Totals section, and in the prior-art framing. Neither
DELETE-CANDIDATE nor a silent-normal reclassification occurred. Correct,
and consistent with what I already said was the right shape.

## (2) Phase 2 is not instructed to execute the comparison

The row's own words, verified present verbatim: "running and interpreting
that comparison as a decision is Phase 2's or Jon's, not this table's —
this row states what the diff shows, not what should be done about it."
The characterizing diff above is explicitly framed as already-run, for
description only, not as an instruction. **Satisfied.**

## (3) The two non-blocking accuracy items

Both fixed, and I re-verified both independently rather than trusting the
correction:

- **`hierarchy-naming-57.md`**: now states 8 hits (was 6), with the same
  breakdown I measured in round one. Re-ran: `grep -rn hierarchy-naming-57
  agent-estate | wc -l` → **8**, matches.
- **`docs/supervisor-disposition.md`**: now correctly distinguishes the
  citations that are real in the CURRENT `reference/` tree
  (`adapter.py:16,83`, `watchdog-harness.sh:32`) from the two that only
  existed in stale `.claude/worktrees/` snapshots
  (`cli.py:850`→`cli_dispatch_record.py:97`,
  `watchdog.sh:96`→`watchdog-harness.sh:32`). Re-ran `grep -rn
  supervisor-disposition agent-estate/reference/` myself: confirms exactly
  those four current-tree hits at those paths/lines, nothing else.
  **Both satisfied.**

## (4) The required checkout-divergence warning

Present, in the opening section, stated plainly and not scoped to the one
row:

> **This checkout is 1 commit ahead of `origin/main` and 13 commits
> BEHIND it**... **Any claim in this table derived from this local
> working tree or its own `git log`/`git status` may be stale relative to
> `origin/main`.** Phase 2 must verify every fact this table states
> against `origin/main` directly... before executing anything against
> it — this is the general form of the specific error corrected in the
> `docs/moc-map-of-maps.md` row below, and it is not scoped to that one
> row.

Includes the same `git rev-list --left-right --count HEAD...origin/main`
→ `1	13` command and output I independently re-ran above. An executor
reading only the opening section (before reaching any individual row)
cannot miss this. **Satisfied.**

## (5) `agent-dotfiles` still untouched

Confirmed at the top of this re-review and unchanged from the first
review's own baseline capture — same four entries, nothing added, nothing
removed. No STOP-and-report condition triggered.

---

## Verdict

All four items requested plus the required addition are satisfied, each
independently re-verified against the real repo state rather than the
table's own prose. The blocking finding from round one is closed
correctly — not just given a safer-sounding shape, but factually
corrected — and the general-form warning (item 4) means the same class of
stale-checkout error is now guarded against for every other row, not only
the one that surfaced it.

Verdict: APPROVE
Review-Lane: agent-estate:2
