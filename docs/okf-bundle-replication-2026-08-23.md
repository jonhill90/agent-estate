---
type: Measurement
description: Replicates agent-tui#136's index-versus-reading measurement on a second repo -- a replication, not an adoption.
generated:
  at: 2026-08-23T15:56:59-04:00
---

# Replication: does agent-tui#136's map-beats-reading result hold on agent-supervisor?

**This is a replication, not an adoption.** `agent-tui#136` measured one
repo and got 7.5x and 20.4x. One repo's number could be a fluke. This
costs one lane to check whether the result survives on a second, different
repo. Nothing here proposes rolling this out anywhere, and no doc's
content was rewritten.

## Step 1 — the brief's numbers, verified rather than trusted

The brief stated: 18 md files, 246 KB, 0 stale over 14 days. Measured
directly on `origin/main` at `35988b5`:

**Read every corpus figure below as of that commit.** `main` has moved since,
to a single consistent count rather than two conventions read as if they
disagreed: 27 tracked `.md` files at this branch's tip, of which 19 sit under
`docs/` (including `index.md` itself) and 18 are the entries `index.md` indexes
(excluding itself). The measurement is dated on purpose and is not restated as
current.

```
$ git ls-files '*.md' | wc -l
18
```

**18 tracked `.md` files — confirmed, with one correction the brief's
count hides.** `CLAUDE.md` is a *symlink* to `AGENTS.md`
(`git ls-files -s '*.md' | grep 120000` returns exactly one row,
`CLAUDE.md`), so 18 tracked paths are **17 distinct files**. This matters
mechanically, not pedantically: adding frontmatter to `AGENTS.md` would
change `CLAUDE.md` too, and writing to both would be writing to one file
twice.

**246 KB — confirmed, and the brief's figure is already the deduplicated
one.** Summing all 18 tracked paths double-counts the symlink:

```
all 18 paths:            262,579 bytes
minus CLAUDE.md (16,697): 245,882 bytes = 245.9 KB (decimal) / 240.1 KiB
```

245.9 KB rounds to the brief's 246 KB, so the brief was counting 17
distinct files. Stated so the two numbers in the same sentence ("18 files,
246 KB") are not read as coming from the same set — they don't.

**0 stale over 14 days — confirmed.** Today is 2026-08-23, so the window
opens 2026-08-09. `git log -1 --format=%cd --date=short -- <file>` for all
18 paths: the oldest last-touch date in the entire corpus is **2026-08-13**
(`CLAUDE.md`, i.e. the symlink's creation, and
`scripts/supervisor/laneview-plugin-tmux/README.md`). Nothing is older
than 10 days. Zero stale, exactly as stated.

## Step 2 — inclusion criteria, matched to #136 rather than to the brief

**In scope: the 10 `.md` files under `docs/`.** Not the 18.

`agent-tui#136`'s stated criterion was "every `.md` under `docs/`" — it
excluded that repo's root `AGENTS.md`, `CLAUDE.md`, `README.md`, and its
`testdata/vhs/*.md`. A replication has to apply the *same criterion*, not
the same headline count. Applied here, `git ls-files 'docs/*.md'
'docs/**/*.md'` returns 10 files, which is also — usefully for a
comparison — the same corpus size agent-tui had (10 files, 109,821 bytes
there; 10 files, 107,664 bytes here).

**The brief asked for 18. This is a deliberate divergence, stated rather
than quietly taken**, for one reason: widening the corpus widens the
without-map baseline, and the without-map baseline is the numerator of
every ratio in this report. Including `scripts/supervisor/README.md`
(53,032 bytes) and `scripts/supervisor/loop-tick.md` (40,202 bytes) would
roughly double the baseline and roughly double every ratio, without the
index doing any more work. That is inflating a result by choosing a
denominator, which is precisely the move a replication exists to not make.

**The cost of this choice is real and is reported, not hidden.** Eight
tracked `.md` files sit outside `docs/` — including the two largest
documents in the repo and `AGENTS.md`, the file agents are actually told
to read first. On this repo, unlike agent-tui, a meaningful share of the
written-down knowledge lives outside `docs/`. Q3 below is a direct
casualty of that, and it is the most interesting result in this report.

The 10 in-scope files, as they ship in this PR:

| File | Bytes | `type` |
|---|---:|---|
| `docs/decisions/0001-sqlite-ledger.md` | 3,760 | Decision |
| `docs/decisions/0002-claude-print-alongside-tmux.md` | 2,945 | Decision |
| `docs/decisions/0003-independent-review-required.md` | 3,373 | Decision |
| `docs/decisions/0004-restore-refuses-never-invents.md` | 3,999 | Decision |
| `docs/decisions/0005-codex-session-resume.md` | 8,459 | Decision |
| `docs/diagrams/dispatch-path.md` | 31,407 | Diagram |
| `docs/product/PRD.md` | 7,064 | PRD |
| `docs/product/SPEC.md` | 18,730 | Spec |
| `docs/runbooks/restore-after-tmux-loss.md` | 16,332 | Runbook |
| `docs/runbooks/send-keys-retirement-284.md` | 11,595 | Measurement |
| **total** | **107,664** | |

### Frontmatter shape, and the `date` question

Each file got `type` (the only OKF-required key), a one-line
`description`, and `generated.at` — matching #136's shape exactly,
including its key name. #136 used `generated.at`, not `date`; the brief
said `date`. Matching #136 was the point, so `generated.at` it is.

**The convention: `git log -1 --format='%aI' -- <file>`** — the author
timestamp of the file's most recent commit. Same command #136 used. This
is a *last-modified* convention, not first-added. Every one of the 10
files returned a non-empty timestamp, so **no date was invented and no
key was omitted**; the brief's escape hatch for messy history was not
needed. Values were taken from the commit that was `HEAD` before this
branch touched anything.

**No `verified` key anywhere**, for #136's reason, which applies
unchanged here: git log establishes when a file last *changed*, never
whether its content was re-checked against reality. Several of these
docs carry an explicit `Verified <date>` line in their own prose (ADRs
0001–0004 all do). Lifting those into frontmatter would mean parsing free
text to decide which files "count," and getting a different answer per
file — a uniform, honest gap beats an inconsistently-filled field.

**Type vocabulary — reused where #136 established one, extended where it
had nothing.** #136's values were `PRD`, `Spec`, `Research`,
`UI Variants`, `Test Evidence`, `Measurement`. `PRD`, `Spec` and
`Measurement` are reused verbatim. agent-supervisor has kinds agent-tui
had none of, so four values are new: `Decision` (the ADRs), `Runbook`,
`Diagram`, and `Archive` (added when `docs/archive/agent-tui/README.md`
landed in the rebase). #136's vocabulary is descriptive and open, not a
declared closed set, so extending it is not a conflict — but it is an
extension, named here so nobody later reads seven values as the whole
vocabulary.

**One judgement call worth naming.**
`docs/runbooks/send-keys-retirement-284.md` lives under `runbooks/` but
its content is an enumeration with a `## Method` section and before/after
evidence. It is typed `Measurement`, describing its content rather than
its directory. Reasonable people could type it `Runbook`.

**Content was not touched.** `git diff --stat` on the 10 files shows
insertions only, zero deletions — every change is a prepended 6-line
frontmatter block.

## Step 3 — `docs/index.md` against the four map-before-search properties

The contract is stated in agent-dotfiles'
`docs/memory-per-agent-map-contract.md` (from agent-dotfiles#315), which
gives three numbered clauses; the brief splits the first into two
(enumerable, and separate from content). Against the shipped file:

1. **Enumerable — a reader can see everything that exists.** One line per
   `.md` under `docs/`, no exceptions, mechanically verified below: 18
   files, 18 links, 0 missing, 0 dangling. The index also carries a
   closing **"Not covered by this index"** section naming the eight
   tracked `.md` files outside `docs/`. **This is a divergence from
   #136**, whose index was silent about what it excluded. It is here
   because on this repo the excluded set contains the two largest docs and
   `AGENTS.md`; an index that silently omits them is not enumerable, it is
   misleading about its own edges. It also materially changes the Q3
   result below, which is why it is flagged rather than slipped in.
2. **Separate from content.** The index holds links and one-line
   descriptions only. It states no fact about the system that isn't in the
   doc it points at, so it cannot be a source anyone cites, and it cannot
   drift on its own — the failure mode it could have would be pointing
   wrongly, not saying something false.

   **"Copied verbatim from frontmatter" does not hold for every entry, and
   is corrected here rather than restated.** Checked all 18 entries against
   their file's own `description` frontmatter directly
   (`git ls-files 'docs/*.md' 'docs/**/*.md' | grep -v index.md`, then a
   description-for-description diff): **11 of 18 match verbatim; 7 do
   not.** Six are a deliberate departure, not drift — `0006`, `0008`,
   `0010`, `0011`, the agent-estate migration runbook, and the archive
   README all carry an index description that states the file's outcome or
   status rather than the question its frontmatter poses, because
   frontmatter states what a document is *about* and not always what it
   *decided* (see `0008`'s own correction, below). The seventh,
   `merge-impact-inventory-agent-estate.md`, has no `description`
   frontmatter key at all, so its index line cannot be "copied" from
   anything — it was written directly. The property this clause actually
   holds is narrower than first stated: descriptions are *sourced from* the
   document, never invented independently of it, not that they are a
   mechanical copy in every case.
3. **Loadable before search.** 4,578 bytes / 50 lines — cheap enough to
   read in full before deciding anything, which is the whole point of
   loading it first. It answers "what exists here" without a query, which
   is clause 2 of the contract's own wording: browsable, not only
   searchable.
4. **Bounded, with detection.** The index declares its own ceiling in a
   comment on line 5: **60 lines / 6 KB**. Today it sits at **50/60 lines
   (83.3%) and 4,578/6,144 bytes (74.5%)** -- larger than the
   40/2,745 this section first recorded, because the rebase onto `main` had
   to index seven more documents that landed after the measurement commit
   (11 entries -> 18 is a seven-document delta). The bound is meaningful rather
   than decorative because the index grows one line per doc: 60 lines caps
   this corpus at roughly 45 documents before the ceiling forces a
   decision about grouping.

   **The honest limit: the cap is declared, not enforced.** agent-dotfiles
   has `scripts/memory_lint.py` running an `index-cap` check with review
   and decide thresholds in CI. Nothing in agent-supervisor checks this
   index's cap, its completeness, or its link integrity today. The check
   below was run by hand for this snapshot. Clause 3 asks for "some bound
   and some detection for approaching it" — this ships the bound and a
   re-runnable check, not the detection. Same gap #136 left, named the
   same way rather than quietly improved upon.

Mechanically verified for this snapshot (a script comparing
`docs/**/*.md` against `index.md`'s own link targets — the same set-diff a
CI guard would automate, not eyeballed):

```
$ git ls-files 'docs/*.md' 'docs/**/*.md' | grep -v '^docs/index.md$' | wc -l
18
$ wc -lc docs/index.md
50  4578 docs/index.md
```

```
files under docs/ (excl. index.md): 18
linked from index.md: 18
missing from index (file exists, no entry): []
dangling index links (points at nothing): []
cap: 50/60 lines (83.3%), 4578/6144 bytes (74.5%)
```

(18, not 10 — Step 2's in-scope set is 10 files; the other 8 under `docs/`
are this report plus the seven documents that landed in the rebase and the
completeness fix that followed it.)

## Step 4 — three real questions, measured three ways

Questions were fixed **before** any measurement was taken, and before the
index was written, so they could not be chosen to flatter the result. All
three are things someone actually working on this repo needs:

1. The tmux server died and the lanes are gone — what do I run, and what
   happens to a lane it can't bring back?
2. Why can't a lane merge a PR it reviewed itself, and where is that
   actually enforced?
3. How does an agent visually inspect a pane — what is `look.py` for and
   when is a captured frame required?

**Method, matching #136 exactly:**

- **Without the map** = the byte count of all 10 in-scope files as they
  ship (`wc -c`, post-frontmatter) — **107,664 bytes**. This is the
  ceiling for "an agent with no index and no prior knowledge of this
  repo's structure": it cannot know which file answers an arbitrary
  question without reading all of them or already knowing what to search
  for. #136's baseline was computed the same way, against post-frontmatter
  files — verified directly rather than assumed, by re-summing agent-tui's
  10 shipped docs at `origin/main` and reproducing its stated 109,821
  exactly.
- **With the map** = `docs/index.md` (4,578 B) plus only the file(s) the
  index actually points at for that question.
- **Grep** = an actual `grep -rli` run against `docs/`, with the byte
  count of every file it returns, because a file grep names is a file the
  agent then has to read.
- Bytes via `wc -c`. Exact and reproducible; nobody has to agree on a
  tokenizer.

### Q1 — restore after a tmux server loss

Fully answered by `docs/runbooks/restore-after-tmux-loss.md` alone: Step 3
gives the command, Step 4 is titled "what `UNRECOVERABLE` means and what
to do about it."

```
$ git cat-file -s HEAD:docs/index.md HEAD:docs/runbooks/restore-after-tmux-loss.md
4578
16332
$ python3 -c "print(107664/(4578+16332))"
5.148923959827833
```

```
without map: 107,664 B   -- all 10 docs
with map:     20,910 B   -- index.md (4,578) + restore-after-tmux-loss.md (16,332)
ratio: 5.15x fewer bytes with the map
```

**Grep: the map beats grep here, unlike in #136.** The obvious term finds
the right file but drowns it. Re-run against the current `docs/` corpus, not
the 10 this report first measured against — `docs/` gained seven documents in
the rebase, so the grep result sets moved too. Scoped to the 17 files under
`docs/` excluding `index.md` **and this report**, for the same
self-referential reason Step 2 excludes the report from the in-scope
baseline: a grep sweep that counts this document's own bytes moves every
time this document is edited, including by this fix:

```
grep -rli 'restore' docs/      -> 8 of 17 files, 102,638 B  (4.91x worse than the map)
grep -rli 'server loss' docs/  -> 3 files,        28,790 B  (1.38x worse than the map)
```

Even the sharper two-word phrase leaves three candidate files. This is a
genuine divergence from #136, where Q1's grep (13,972 B) nearly tied its
map (14,721 B). The reason is structural, not luck: agent-supervisor's
corpus is *about* restore in a large share of its documents — one ADR, the
runbook, plus passing mentions across the PRD, the SPEC, ADR-0005 and the
newer decision/runbook docs. A keyword common to the corpus's central
subject cannot discriminate within it.

### Q2 — why a lane can't merge its own reviewed PR

Fully answered by `docs/decisions/0003-independent-review-required.md`
alone — its title is the decision, and its `## Fail closed` section covers
the enforcement point.

```
$ git cat-file -s HEAD:docs/index.md HEAD:docs/decisions/0003-independent-review-required.md
4578
3373
$ python3 -c "print(107664/(4578+3373))"
13.540938246761414
```

```
without map: 107,664 B   -- all 10 docs
with map:      7,951 B   -- index.md (4,578) + 0003-independent-review-required.md (3,373)
ratio: 13.54x fewer bytes with the map
```

**Grep: the map beats grep, by a lot — and grew wider on the current corpus.**
Re-run against the same 17-file `docs/` scope as Q1 (excluding `index.md` and
this report):

```
grep -rli 'review' docs/       -> 13 of 17 files, 196,980 B  (24.77x worse than the map)
grep -rli 'self-review' docs/  ->  2 files,        22,103 B  ( 2.78x worse than the map)
```

`review` is close to useless here — independent review is one of this
system's core invariants and most of the corpus mentions it. `self-review`
is much sharper, but it is a term the question doesn't contain; an asker who
hasn't already internalised the vocabulary won't reach for it, and it still
costs 2.78x the map.

### Q3 — how does an agent visually inspect a pane? **Could not measure.**

**Not documented anywhere under `docs/`.** Grepped the in-scope corpus for
every natural term:

```
grep -rli 'look\.py'    docs/  -> (nothing)
grep -rli 'screenshot'  docs/  -> (nothing)
grep -rli 'ui-evidence' docs/  -> (nothing)
grep -rli 'png'         docs/  -> (nothing)
```

Recording this as **could not measure**, not as a 1.0x ratio and not as a
zero — a ratio implies both paths reached an answer, and neither did.

**This is where grep misleads, and it is a real result, not a
formality.** One near-miss term does return files, confidently and
wrongly:

```
grep -rli 'capture' docs/ -> docs/runbooks/restore-after-tmux-loss.md (16,332 B)
                             docs/product/SPEC.md                     (18,730 B)
                             35,062 B combined
```

Both hits are irrelevant, verified by reading the actual matching lines:
the runbook's is `"whose actual launch $PWD is captured"` (about
worktrees), and SPEC.md's is `"the file isn't open during a pane
capture"` (about `lsof`, in a section on resolving session ids). Neither
has anything to do with visual inspection. That is 35,062 bytes — a third
of the whole corpus — spent to arrive at nothing, with two plausible-
looking hits that an agent could easily stop on. Same failure shape #136
found on its Q2: keyword search does not fail loudly, it succeeds
confidently on the wrong file.

**Where the answer actually lives, and why that indicts the scope
criterion rather than the corpus.** Unlike #136's Q3 — where the subject
was undocumented *anywhere* in the repo — this subject **is** documented
here, just outside the measured set:

```
grep -rli 'look\.py' --include='*.md' .  -> AGENTS.md   (and CLAUDE.md, its symlink)
grep -rli 'ui-evidence' --include='*.md' . -> AGENTS.md
```

`AGENTS.md` is the file every agent is instructed to read first, and it is
the *only* file in the repo mentioning `look.py`. So Q3's could-not-measure
is caused by the scope criterion inherited from #136, not by a hole in
agent-supervisor's knowledge. That is a finding about the method: **"every
`.md` under `docs/`" is a criterion that transplants cleanly onto a repo
that keeps its docs in `docs/`, and mis-measures one that doesn't.**

**One asymmetry, reported without a number attached.** The shipped index
does let a reader establish in 4,578 bytes that `docs/` has nothing on
this, *and* points at the out-of-`docs/` set where it lives — an
authoritative negative for a fraction of what the misleading grep cost. That
is a real advantage over both baselines, but it is **not** counted as a
ratio, for two reasons: neither path produced an answer to the question
asked, and the property comes entirely from the "Not covered by this
index" section, which is this replication's own divergence from #136 and
not part of the thing being replicated.

### Results together

| Question | Without map | With map | Ratio | Grep verdict |
|---|---:|---:|---:|---|
| Q1 restore after tmux loss | 107,664 B | 20,910 B | **5.15x** | map beats grep (grep 28,790–102,638 B) |
| Q2 self-review at merge | 107,664 B | 7,951 B | **13.54x** | map beats grep (grep 22,103–196,980 B) |
| Q3 pane inspection / `look.py` | — | — | **could not measure** | grep **misleads** — 35,062 B of confident, irrelevant hits |

Per-question ratios are the numbers of record, reported separately, as
#136 reported its two. For completeness and not as a headline:

```
$ python3 -c "print((107664*2)/(20910+7951), (107664/20910 + 107664/7951)/2)"
7.460864141921625 9.344931103294623
```

across the two measurable questions the aggregate is 215,328 / 28,861 =
**7.46x**, and the mean of the two ratios is **9.34x**. Neither is "the"
number.

## Step 5 — arithmetic reproduced against the committed file

This is the specific defect #136's review caught: its write-up used
`index.md` at 1,766 bytes while the file that actually shipped was 1,955,
because a self-referential entry landed after the measurement and the
arithmetic was never re-run. This report shipped the same defect once
already, at a larger magnitude: an independent adversarial review (see the
issue thread, `2026-08-24T09:09:52Z`) found `index.md` had grown to
4,578 bytes across a rebase that indexed seven more documents, while this
section still quoted the abandoned pre-rebase blob's 2,745 bytes, and every
ratio downstream of it was stale as a result. Re-run in full against the file as it actually ships, not the one number a
prior pass happened to notice:

```
$ git cat-file -s 6607623:docs/index.md
4578

$ wc -lc docs/index.md
50  4578 docs/index.md

$ python3 -c "print(107664/(4578+16332), 107664/(4578+3373))"
5.148923959827833 13.540938246761414
```

**4,578 bytes at the committed head = 4,578 bytes in every calculation
above.** 5.15x and 13.54x reproduce exactly from the committed artifact —
not the 5.6x / 17.6x this section quoted before, which were computed
against a blob (`5d79513`) this branch abandoned in its rebase.

## Does #136's result hold? Yes in direction, weaker in magnitude

| | agent-tui#136 | agent-supervisor (this) |
|---|---:|---:|
| In-scope docs | 10 files, 109,821 B | 10 files, 107,664 B |
| `index.md` | 1,955 B | 4,578 B |
| Ratio, question A | 7.5x | **5.15x** |
| Ratio, question B | 20.4x | **13.54x** |
| Could not measure | 1 of 3 | 1 of 3 |

**The pilot's finding replicates.** Two measurable questions, both large
wins, one could-not-measure — the same shape, on a repo with a
near-identical corpus size but entirely different content.

```
$ python3 -c "
q1_old, q1_new = 7.5, 107664/20910
q2_old, q2_new = 20.4, 107664/7951
print((q1_new-q1_old)/q1_old*100, (q2_new-q2_old)/q2_old*100)
"
-31.347680535628893 -33.622851731561695
```

**Both ratios come in 31–34% lower** (7.5x → 5.15x is −31.3%; 20.4x →
13.54x is −33.6%). The cause is diagnosable and unflattering to the
method, not to this repo: agent-supervisor's index is **134% larger**
(4,578 vs 1,955 B) over the same number of in-scope documents, because the
rebase had to fold in seven more documents' worth of entries as well as
carrying longer descriptions and the extra "Not covered" section. The
index is the fixed cost paid on *every* question, so a bigger index lowers
every ratio. **This is the first evidence that #136's numbers are
index-size-sensitive and should not be quoted as a constant.** A third
repo would be worth measuring before anyone treats "roughly 10x" as a
property of the technique.

**The grep comparison did not replicate, and diverged in the map's
favour.** #136's most careful finding was that grep nearly ties the map
when the answer's vocabulary matches the question's, so the map's real
advantage is narrower than the headline. On agent-supervisor that
qualifier did not hold: the map beat grep on *both* measurable questions,
by 1.38x–24.77x. The reason is a corpus property #136 had no way to see from
one repo — agent-supervisor's documents are densely cross-referential, and
its most natural search terms (`restore`, `review`) are the names of its
central invariants, so they appear in most documents and discriminate
between none. **A keyword search degrades as a corpus becomes more
internally coherent; an index does not.** That is a stronger claim for the
map than #136 made, and it comes from the repo that was supposed to be the
skeptical second data point.

## What this does not settle

- **Whether to do this anywhere else.** Two repos is two data points, and
  they disagree by 31–34% on magnitude and disagree qualitatively on
  whether grep is competitive. Sequencing is the director's; approval is
  Jon's.
- **The index-freshness guard.** Both #136 and this replication ship a
  declared bound and a hand-run check, and neither wires it into CI. The
  objection that a stale map misdirects with authority is unclosed here
  too.
- **Whether `docs/`-only is the right scope for this repo.** Q3 says it is
  not, but fixing that would have meant not replicating #136. Named as a
  result, not repaired.
