---
type: Measurement
description: Measures whether an OKF-style docs/ bundle is worth doing across four repos -- a pilot on agent-tui, not an adoption.
generated:
  at: 2026-08-23T14:32:56-04:00
---

# Pilot: is agent-tui's `docs/` already project memory, just without a map?

**This is a measurement, not an adoption.** The deliverable is a number
that tells us whether the idea is worth doing across the other three
repos. Scope: `agent-tui` only. Repo visibility, the other three repos,
and any estate-wide rollout are untouched and not proposed here.

## What was built — the smallest honest wrapper

Every `.md` under `docs/` (10 files, confirmed by `git ls-files
'docs/**/*.md' 'docs/*.md'`) got frontmatter: `type` (the only OKF-
required key), a one-line `description`, and `generated.at` — the date
of the file's last commit, from `git log -1 --format='%aI' -- <file>`.

**`verified` was deliberately left empty on every file.** Established
from git log alone, per the brief's own constraint — and git log cannot
honestly answer "was this content re-checked against reality," only "when
did it last change." One file's own prose (`SPEC-agentbox-execution-
mode.md`) states an explicit verification event in its opening
paragraph ("Verified against the sibling AgentBox repo... 2026-08-22, by
reading its own docs/architecture/overview.md... directly"), but encoding
that into an OKF `verified` field would mean parsing free text to decide
which files "count," inconsistently, file by file — worse than a
uniform, honest gap. Stated here so the gap is visible rather than
silently filled for one file and not the other nine.

`docs/index.md`: one line per doc, link plus its one-line description,
grouped by kind (Design / Research / UI variants and spikes / Test
evidence). No frontmatter beyond `okf_version` (spec §8's own exception
for a root index). Content unchanged — this is a wrapper, not a truth
pass; no doc's prose was rewritten.

## The measurement

Three real questions a new agent would need this repo's docs to answer,
picked before measuring either path (so the questions weren't chosen to
flatter the result):

1. Which nav destinations are stubs, and why?
2. How do I regenerate the rail+board UI variant screenshots?
3. What is the tape-guard for?

**Correction, made honestly rather than silently:** an earlier draft of
this section measured `docs/index.md` at 1,766 bytes and used that
figure below. That was the file's size *before* this report itself was
added to it as an 11th, self-referential entry (the "Measurement"
heading at the top of `index.md`, 189 bytes) — the arithmetic was never
re-run after the self-entry landed. `wc -c docs/index.md` against what
actually ships in this PR reads **1,955 bytes**, and every number below
is computed against that real, shipped file, not the earlier snapshot.
The underlying finding survives the correction with almost no change
(7.6x → 7.5x, 21.2x → 20.4x) — restated here as a demonstration of the
method's own point: re-running this doc's stated method against its own
shipped artifact has to reproduce the numbers in the doc, and until this
correction it didn't.

**Method, stated precisely enough to re-run:**

- **Without the map** = the byte count of every file under `docs/`
  (`cat` all 10 files, `wc -c`) — the honest ceiling for "an agent with no
  index and no prior knowledge of this repo's structure," since it
  cannot know in advance which file(s) answer an arbitrary question
  without either reading all of them or already knowing what to search
  for (see the grep caveat below for why "just grep it" isn't a free
  alternative).
- **With the map** = `docs/index.md`'s byte count plus only the file(s)
  the index actually points at for that question.
- Bytes are counted with `wc -c` — exact and reproducible. A token
  figure (bytes ÷ 4, the standard rough English-text approximation) is
  given alongside for scale, but bytes are the number of record; nobody
  needs to agree on a tokenizer to reproduce a byte count.

**Q1 — nav destinations, stubs and why.** Fully answered by
`docs/SPEC-shell.md` alone (its own intro states S1–S11 built, S12's
interface shipped with the container driver unbuilt; §S5 states the
stub mechanism — "every remaining destination renders a named
placeholder... not built yet").

```
without: 109,821 bytes (~27,455 tokens)  -- all 10 docs
with:      14,721 bytes (~3,680 tokens)  -- index.md (1,955) + SPEC-shell.md (12,766)
ratio: 7.5x fewer bytes with the map
```

**Q2 — regenerating the rail+board UI variant screenshots.** Fully
answered by `docs/uivariants/README.md` alone — its own "## Regenerating"
section gives the exact command (`./scripts/render-uivariants.sh`) and
its two dependencies.

```
without: 109,821 bytes (~27,455 tokens)  -- all 10 docs
with:       5,373 bytes (~1,343 tokens)  -- index.md (1,955) + uivariants/README.md (3,418)
ratio: 20.4x fewer bytes with the map
```

**Q3 — what is the tape-guard for? Could not measure a ratio — honestly, not by omission.**
`internal/vhscheck` is the tape-build guard (agent-tui#132, `go doc`
confirms: *"Package vhscheck is the durable guard agent-tui#132
asked for..."*), but its purpose is **not documented anywhere under
`docs/`** — grepped the whole corpus for `vhscheck`, `tape.guard`,
`guard.*tape`, zero hits. The map cannot point at content that was never
written down; this is a real gap in the docs corpus itself, not a defect
in the index. Recording this as **could not measure**, not as a 1.0x
ratio or a zero — a ratio implies both paths reached an answer, and
neither did.

**Aggregate, the two measurable questions:** 219,642 bytes without vs.
20,094 bytes with → **10.9x fewer bytes**, averaging the two ratios
gives 13.9x. Both numbers reported; neither is "the" number — see the
caveat below for why this likely overstates the map's real advantage.

## The caveat this number needs, found while measuring, not added after

**"Without the map" here means "read the entire `docs/` corpus blind" —
that is a ceiling, not what a competent agent actually does.** A
grep-capable agent doesn't read everything; it searches first. Tested
this directly rather than asserting it changes the picture:

- **Q1, grep helps almost as much as the index.** `grep -rl stub docs/`
  hits exactly two files (`SPEC-shell.md`, `navwalk-docs/README.md`,
  13,972 bytes combined) — close to the map's own 14,721. For this
  question, a grep-first agent does nearly as well without the index as
  with it.
- **Q2, grep genuinely misleads — a real, useful finding, not a
  hypothetical.** Grepping for the brief's own literal example word,
  `screenshot`, hits exactly one file: `lanechat-variant-comparison.md`
  — a different, real variant gallery, plausible-looking, and **wrong**
  for this question. `uivariants/README.md` (the actual answer) never
  says "screenshot," it says "images." A keyword search doesn't fail
  loudly here; it succeeds confidently on the wrong file, which is worse
  than reading everything, because a satisfied agent stops. The index
  doesn't have this failure mode — its description is a human-written
  summary, not a literal-string index, so an agent using it reads
  `uivariants/README.md`'s actual one-line description and recognizes
  the match regardless of which word the question happened to use.

So the honest range is: **grep does almost as well as the map for
questions whose answer uses the question's own vocabulary (Q1), and the
map has a real, demonstrated advantage specifically when it doesn't
(Q2)** — not "the map is always an 11x win." Both are real findings from
this pilot; reporting only the aggregate ratio would overstate the case.

**A third alternative, named but not tested.** A filename-only baseline —
`ls docs/`, then open whichever name looks most relevant — is cheaper
than either grep or the map for the byte cost of the "search" step
itself (no `grep` output to scan, no index file to read). Not measured
here: it depends on how well a filename predicts its content, which is a
judgment call about ten specific names, not a byte count, and doing it
honestly would mean simulating a blind guess rather than using the
hindsight this pilot already has about which file answers which
question. Naming it as an untested alternative rather than leaving it
implied — a real gap in this measurement, not a result.

## `devils-advocate`, required by the brief — run and answered, not waved at

**The objection:** an index is one more thing that goes stale, and a
stale map is worse than no map, because it misdirects with authority
instead of merely being absent. What makes `docs/index.md` go stale, and
is there a mechanical check that would catch it — or is this a promise
to be disciplined, which this estate has repeatedly shown does not hold?

**Answered specifically, not generically:**

`docs/index.md` goes stale in exactly two mechanical shapes, both
independently checkable:

1. **A new `.md` lands under `docs/` with no corresponding index
   entry.** Checkable: diff the set of `git ls-files 'docs/**/*.md'
   'docs/*.md'` (minus `index.md` itself) against the set of relative
   paths `index.md` links to. Zero today (verified below); no such CI
   guard exists in this repo yet.
2. **An index entry's link target is deleted or renamed, and the entry
   isn't.** Checkable: resolve every relative link in `index.md` against
   the filesystem. Zero broken today (verified below); also no guard.

**Neither check exists in agent-tui today.** `agent-dotfiles`'s
`memory_lint.py` has both — `0/76 facts have no corresponding index.md
link` and `0 index.md links point at a fact that no longer exists` — for
the shared memory vault specifically, not for this repo's `docs/`. This
pilot did **not** port that tool here; a real adoption decision would
need to, and until it does, this index's own freshness genuinely is a
discipline promise, not a mechanically enforced one — the objection is
correct as stated, and this pilot does not close it.

Run mechanically for this snapshot (a short Python script comparing
`docs/**/*.md` against `index.md`'s own link targets — the same check a
CI guard would automate, not eyeballed):

```
files under docs/ (excl. index.md): 11
linked from index.md: 11
missing from index (file exists, no index entry): []
dangling index links (index points at nothing): []
```

(11, not 10 — this pilot report itself is the 11th file, indexed above
under "Measurement.")

## What this does not settle

- Whether this is worth doing across the other three repos — that
  decision follows the number and is the director's to sequence, Jon's
  to approve.
- Whether the index-freshness guard should be built — a real requirement
  this pilot surfaced, not a recommendation to build it now.
- Repository visibility — untouched, Jon's call.
