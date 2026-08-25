---
type: Measurement
description: Replicates agent-tui#136's OKF index measurement on agent-supervisor -- does docs/index.md hold up as a complete, accurate map of docs/? Redone through the dispatch path as agent-supervisor#531's ledger-less predecessor.
generated:
  at: 2026-08-25T00:55:10Z
---

# OKF index measurement: does `docs/index.md` hold up as a map of `docs/`?

**This redoes `agent-supervisor#531`, not reviews it.** #531
(`docs: replicate agent-tui#136's OKF index measurement on a second repo`,
branch `docs/okf-bundle-replication`, head `2cb7082`) has no ledger record of
its author -- it was written by a direct pane write, not through
`dispatch.sh`, so `dispatch.sh --reviews-pr 531` cannot prove a reviewer
would be independent and fails closed. Per the precedent at
`agent-supervisor#607` (three agent-tui PRs hit the same defect; decision was
redo, not launder with `mark-pr-external.sh`), this is a fresh piece of work
dispatched through the normal path, on a fresh branch from current
`origin/main`. None of the figures below are copied from #531's branch or
its comments; #531's branch was read for structure only (its report's
shape, not its numbers).

**Which tree each figure is measured against, stated explicitly.** This
report is measured in two passes against two different trees, and the
numbers differ because of it, not because of an arithmetic or method error:

- **Step 1's corpus** (`git ls-files 'docs/*.md' ...`) was captured
  *before this report file itself existed* -- at
  `f21a09e35aa0a40ca6043bbcfbce8788974c8702`, the pre-report snapshot of
  `origin/main`. That step's figures (18 total under `docs/`, 17 excluding
  the index, 26 tracked `.md` repo-wide) describe that earlier tree only.
- **Every figure from "Final committed tree" onward** (added below, and in
  the "What CI does and does not check" / index-completeness re-check) is
  measured against the tree this PR actually ships -- the one a merge
  produces, which includes this report file as a tracked `docs/*.md` path.
  A merge produces the second tree, never the first, so those are the
  figures that describe reality after this lands.

An independent review at head `a70f9480` (`gh pr view 620 --comments`)
caught that the first pass's "zero missing entries" claim did not hold
against the second tree -- see "Self-check: did this measurement commit the
defect it measures?" below.

**Scope note, stated up front.** #531's own report scoped its OKF-bundle
replication (frontmatter additions, byte-cost comparisons) to a 10-file
subset of `docs/` chosen to match `agent-tui#136`'s corpus size. This
redo's brief asks a narrower, more mechanical question instead: is
`docs/index.md` -- **today, as it actually ships on `main`** -- a complete
and accurate index of everything under `docs/`? That mechanical check is
what follows.

## Positive control, before any filtering

`find` on this machine is `bfs`, which (unlike GNU find) can silently
reject an unsupported predicate to stderr while a piped `wc -l` still reads
a truncated stream as a low or zero count. Printing the unfiltered totals
first means a zero further down is distinguishable from a filter that
matched nothing:

```
$ git ls-files '*.md' | wc -l
26
$ find docs -name '*.md' | wc -l
18
```

Both commands agree on the docs/-scoped figure (18, confirmed independently
below via `git ls-files`), so no `bfs`-vs-GNU divergence was actually hit on
this tree -- tried `-regextype posix-extended`, GNU-style `-printf`, and the
deprecated BSD `-perm +644` form against `bfs 4.1.1` here and all three ran
without error or discrepancy. Named because the brief calls out the risk
explicitly, not because this measurement reproduced it; `git ls-files` is
used as the source of truth throughout regardless, since it needs no
predicate compatibility at all.

## Step 1 — the corpus

```
$ git ls-files '*.md' | wc -l
26
$ git ls-files -s '*.md' | awk '$1==120000' 
120000 47dc3e3d863cfb5727b87d785d09abf9743c0a72 0	CLAUDE.md
$ git ls-files -s '*.md' | awk '$1==120000' | wc -l
1
```

**26 tracked `.md` paths, 1 of them a symlink** (`CLAUDE.md` -> `AGENTS.md`,
per this repo's own header: "`AGENTS.md` and `CLAUDE.md` are the same file --
one is a symlink, so there is no second copy to drift"). 25 distinct files
by content.

```
$ git ls-files 'docs/*.md' 'docs/**/*.md' | wc -l
18
$ git ls-files 'docs/*.md' 'docs/**/*.md' | grep -v '^docs/index.md$' | wc -l
17
```

**18 `.md` files live under `docs/` including the index itself; 17
excluding it** -- these are the files `index.md` is responsible for
covering:

```
docs/archive/agent-tui/README.md
docs/decisions/0001-sqlite-ledger.md
docs/decisions/0002-claude-print-alongside-tmux.md
docs/decisions/0003-independent-review-required.md
docs/decisions/0004-restore-refuses-never-invents.md
docs/decisions/0005-codex-session-resume.md
docs/decisions/0006-agent-tui-merges-into-agent-supervisor.md
docs/decisions/0008-estate-lane-pr-authorship-evidence.md
docs/decisions/0010-estate-authorship-three-failures.md
docs/decisions/0011-dispatch-sh-standing-rule-and-backlog-path.md
docs/diagrams/dispatch-path.md
docs/merge-impact-inventory-agent-estate.md
docs/product/PRD.md
docs/product/SPEC.md
docs/runbooks/agent-estate-migration.md
docs/runbooks/restore-after-tmux-loss.md
docs/runbooks/send-keys-retirement-284.md
```

(Note the numbering gap: `0007` and `0009` do not exist as files in this
tree -- not investigated further here, out of scope for an index
measurement, named so it is not mistaken for something this report missed.)

## Step 2 — what `docs/index.md` looked like on `main` before this PR

`main`'s `docs/index.md` (`f21a09e`, before this branch's edit) was **26
lines / 2,004 bytes** and carried **11 entries**:

```
$ wc -lc docs/index.md      # before this PR's edit
26 2004 docs/index.md
$ grep -cE '^\* \[' docs/index.md
11
```

No documented size cap exists in the file (no `<!-- cap -->` comment or
equivalent) and no CI workflow references it:

```
$ grep -rl 'index.md' .github/workflows/
# (no output -- confirmed by exit code, not silence alone)
```

**Checked against the four properties, before this PR's fix:**

- **Duplicate link targets:** none (`sort | uniq -d` on the 11 targets was
  empty).
- **Dangling entries** (target does not exist), verified with
  `git cat-file -e` for each of the 11, not by eye: none. All 11 existing
  entries pointed at real files.
- **Every file under `docs/` has exactly one index entry:** **false.** Six
  of the 17 non-index files under `docs/` had **zero** entries:

  ```
  $ comm -23 \
      <(git ls-files 'docs/*.md' 'docs/**/*.md' | grep -v '^docs/index.md$' | sed 's#^docs/##' | sort) \
      <(grep -oE '\]\(([^)]+)\)' docs/index.md | sed -E 's/^\]\(//; s/\)$//' | sort)
  archive/agent-tui/README.md
  decisions/0008-estate-lane-pr-authorship-evidence.md
  decisions/0010-estate-authorship-three-failures.md
  decisions/0011-dispatch-sh-standing-rule-and-backlog-path.md
  merge-impact-inventory-agent-estate.md
  runbooks/agent-estate-migration.md
  ```

  These six all postdate `main`'s last edit to `index.md` (`0006` landed and
  was indexed; `0008`/`0010`/`0011`, the archive README, the merge-impact
  inventory, and the agent-estate migration runbook did not, each landing
  in a PR that updated `docs/` without also updating the index). This is
  the "not automatically enforced" gap the brief asks to be named
  explicitly, made concrete: it isn't hypothetical here, it is the actual
  state of `main` at dispatch time.

**This is the measurement's headline finding: `docs/index.md`, as shipped
on `main`, was not a complete index** -- 6 of 17 indexable files (35%) had
no entry. Zero duplicates and zero dangling links, so where it was wrong,
it was wrong by omission, not by pointing at the wrong thing.

## Step 3 — the fix, and the same four checks re-run against it

This PR adds one entry per missing file (`0008`, `0010`, `0011` to
`# Decisions`; the migration runbook to `# Runbooks`; a new `# Archive`
section for the archive README, matching the `Archive` `type` value that
document's own frontmatter already declares; a new `# Other` section for
the merge-impact inventory, which has no `type` frontmatter key at all to
group it by). Descriptions are taken from each file's own frontmatter
`description` where one exists (five of the six), and written directly for
the one file with no `description` key
(`merge-impact-inventory-agent-estate.md`) -- the same situation #531's
predecessor report already found for `merge-impact-inventory-agent-estate.md`
specifically, reconfirmed here rather than assumed:

```
$ grep -L '^description:' docs/archive/agent-tui/README.md \
    docs/decisions/0008-estate-lane-pr-authorship-evidence.md \
    docs/decisions/0010-estate-authorship-three-failures.md \
    docs/decisions/0011-dispatch-sh-standing-rule-and-backlog-path.md \
    docs/merge-impact-inventory-agent-estate.md \
    docs/runbooks/agent-estate-migration.md
docs/merge-impact-inventory-agent-estate.md
```

After adding those six entries (this section's original state, before the
self-check below), measured against the pre-report-file tree:

```
$ wc -lc docs/index.md
38 3831 docs/index.md
$ grep -cE '^\* \[' docs/index.md
17
```

That pass re-ran the same four checks and found zero duplicates, zero
dangling links, and zero missing entries -- but "zero missing" was true
only against the 17-file corpus Step 1 measured, which was captured before
this report file (`docs/okf-index-measurement-2026-08-24.md`) existed. See
the self-check immediately below for what that snapshot ordering hid, and
"Final committed tree" for the numbers that actually describe what ships.

## Self-check: did this measurement commit the defect it measures?

**Yes.** An independent review of this PR at head `a70f9480`
(`gh pr view 620 --comments`) found that `docs/index.md`, as this PR
originally left it, did not have an entry for the one file the PR itself
adds -- this report. That is the exact defect class Step 2's headline
finding is about (a `.md` file landing under `docs/` with no index entry),
reproduced in miniature inside the same PR that exists to document and fix
it:

```
$ comm -23 \
    <(git ls-files 'docs/*.md' 'docs/**/*.md' | grep -v '^docs/index.md$' | sed 's#^docs/##' | sort) \
    <(grep -oE '\]\(([^)]+)\)' docs/index.md | sed -E 's/^\]\(//; s/\)$//' | sort)
okf-index-measurement-2026-08-24.md
```

This happened because Step 1's corpus was captured *before* this file was
written, and the "17 files, all indexed" claim was never re-checked against
the tree the commit actually adds this file to. The fix (a new
`# Measurements` entry in `docs/index.md`) is applied below, and every
figure from here on is re-measured against the tree this PR ships, not the
pre-report snapshot. This paragraph is left in place rather than quietly
edited away, because the fact that a measurement of index-completeness
initially failed to index itself is more informative than a clean report
would have been.

## Final committed tree — every figure re-measured against what this PR ships

```
$ git ls-files '*.md' | wc -l
27
$ git ls-files 'docs/*.md' 'docs/**/*.md' | wc -l
19
$ git ls-files 'docs/*.md' 'docs/**/*.md' | grep -v '^docs/index.md$' | wc -l
18
```

**27 tracked `.md` paths repo-wide** (26 in Step 1's pre-report snapshot,
plus this report file itself). **19 `.md` files under `docs/` including the
index** (18 in Step 1's snapshot, plus this file). **18 excluding the
index** -- the corpus `index.md` is now responsible for covering, one more
than Step 1's 17.

With the `# Measurements` entry added for this report file:

```
$ wc -lc docs/index.md
42 4050 docs/index.md
$ grep -cE '^\* \[' docs/index.md
18
$ grep -oE '\]\(([^)]+)\)' docs/index.md | sed -E 's/^\]\(//; s/\)$//' | sort | uniq -d
# (empty -- no duplicate link targets)
$ for t in $(grep -oE '\]\(([^)]+)\)' docs/index.md | sed -E 's/^\]\(//; s/\)$//'); do
    git cat-file -e HEAD:docs/$t 2>/dev/null || echo "MISSING docs/$t"
  done
# (no output -- all 18 targets resolve with git cat-file -e)
$ comm -23 \
    <(git ls-files 'docs/*.md' 'docs/**/*.md' | grep -v '^docs/index.md$' | sed 's#^docs/##' | sort) \
    <(grep -oE '\]\(([^)]+)\)' docs/index.md | sed -E 's/^\]\(//; s/\)$//' | sort)
# (empty -- no file under docs/, including this report, is missing an entry)
$ grep -oE '\]\(([^)]+)\)' docs/index.md | sed -E 's/^\]\(//; s/\)$//' | sort | uniq -c | awk '$1!=1'
# (empty -- every linked file has exactly one entry)
```

`docs/index.md` is now **42 lines / 4,050 bytes**, indexing all 18
non-index `.md` files under `docs/` in the tree this PR ships -- including
this report -- with zero duplicates, zero dangling links, and zero missing
entries. This is the figure that describes reality after merge; the 17/38-line/
3,831-byte figures above describe a tree that will not exist once this PR
lands.

## What CI does and does not check

**CI does not check index-to-file correspondence.** No workflow under
`.github/workflows/` references `docs/index.md`
(`grep -rl 'index.md' .github/workflows/` returns nothing), so nothing
enforced the six-file gap found in Step 2 and nothing will catch the next
one. A green CI run on any PR that adds a file under `docs/` without
touching `index.md` is not evidence the index stayed accurate -- it is
evidence of an unrelated fact. All four checks above were run by hand for
this snapshot; none is wired into a gate.

## Relationship to `agent-supervisor#531` and its prior fix-pass

The one prior attempt on this issue
(`as531-as531-fixpass`, 2026-08-24 17:47) was a fix-pass **on #531's own
branch**, not a redo -- it corrected seven numeric/prose defects an
independent adversarial review had found in #531's OKF-bundle-replication
report (stale headline byte counts, a mis-stated frontmatter-match ratio,
an "eight documents" miscount, a missing supersession annotation, a
date-fence inconsistency, and an undercounted type vocabulary), and pushed
directly to `docs/okf-bundle-replication` because that PR was already open
and already reviewed once.

**Nothing in that fix-pass is confirmed or contradicted here**, because it
answers a different, broader question -- #531's OKF-bundle frontmatter
replication across a 10-file corpus sized to match `agent-tui#136` -- than
this redo's narrower brief, which is specifically whether `docs/index.md`
indexes `docs/` completely and accurately. The fix-pass's own numbers
(`docs/index.md` at 4,578 B / 50 lines with 18 entries on its branch) are
**not reproduced here and are not this measurement's baseline**: they were
computed against `docs/okf-bundle-replication`'s branch state, which never
merged, so `main`'s `docs/index.md` never had those 18 branch-local entries
-- it had the 11 shown in Step 2 above. Both figures are real; they
describe two different git refs. What that fix-pass's PR did establish
that *is* directly relevant -- **do not launder #531's unrecorded
authorship via `mark-pr-external.sh`, redo from a real dispatch instead** --
is the reason this task exists at all, and is honored here: this is a
fresh branch, from `dispatch.sh`, with its own ledger row.

## What this measurement does not claim

- Whether `docs/index.md`'s 60-line-ish informal size is "too big" is not
  assessed -- no cap is declared in the file to measure against, and this
  report does not invent one.
- Whether the byte-cost-of-reading-without-a-map result from `agent-tui#136`
  replicates on this repo (the "map beats reading" ratios) is out of scope
  for this redo's brief, which asks only about index completeness and
  accuracy -- not measured or claimed here in either direction.
- Whether the six previously-missing files were omitted deliberately or by
  oversight was not investigated; each was added to `docs/index.md` by a
  PR that did not also touch `index.md` itself, which is consistent with
  oversight but not proof of it.

Supersedes `agent-supervisor#531`, which is unmergeable through the normal
review path for the ledger-authorship reason above (agent-supervisor#607
precedent). Comment posted on #531 pointing here so it can be closed.
