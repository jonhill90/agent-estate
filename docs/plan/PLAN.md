# The plan — what governs, what is a child of it, what is finished

Read this file first. It is the spine: it says which document governs, which
are its children, and which are superseded. Nothing else in `docs/plan/`
claims authority over anything.

## Why this file exists

On 2026-09-07 there were **ten plan-shaped documents** describing this work,
in a directory that was not a git repository. Two of them were real plans and
**neither referenced the other** — `master-execution-plan.md` (the
architecture) and `astra-recovery-20260907/EXECUTION-PLAN.md` (a repair plan
written fifteen hours later). Grepping all seven files of the recovery packet
for `master-execution-plan` returned zero hits.

The consequence was not confusion about the plan. It was that agents never
read it: every brief handed to a lane was nearer than the architecture, so
work followed briefs and relayed messages instead. A weekend went into
carefully executed work whose connection to the recorded goal nobody checked.

This file is the fix, and the rule it establishes is: **an agent routes here
before it reads a brief.**

## What governs

**[`specs/master-execution-plan.md`](specs/master-execution-plan.md)** — the
architecture. Vocabulary, the knowledge pillars, the three layers, the push
sequence, the discipline tracks, the product arc. Where any other document
disagrees with it, this one wins, or the other document is wrong and should be
corrected rather than followed.

Its own words: *"Supersedes nothing; sequences everything."*

## Binding specs — children, not competitors

| Document | Governs |
|---|---|
| [`specs/inmaps-spec.md`](specs/inmaps-spec.md) | Vault layout, note ids, frontmatter, tag standard |
| [`specs/contract.md`](specs/contract.md) | The vault contract the validator enforces |
| [`specs/knowledge-structure-research.md`](specs/knowledge-structure-research.md) | Why the structure is what it is — cite it, do not re-derive it |
| [`specs/source-api.md`](specs/source-api.md) | Source registration shape |

**Known defect in `inmaps-spec.md` §8, left in place deliberately:** line 44
says note ids are `YYYYMMDDHHMM(SS)`; line 175 says `YYYYMMDD` + a 4-digit
sequence. Both generators implemented the second, which produced ids whose
trailing digits ran past 59 and were not times. The code now emits real
timestamps. The spec still contradicts itself and should be corrected to the
timestamp form — recorded here so the next reader does not implement the wrong
line again.

## Pushes — the sequence, in order

Each is a child of the master plan. A push does not start until the previous
push's gate has been **reported**, pass or fail, honestly.

| Push | Plan | State |
|---|---|---|
| 2 | [`pushes/astra-execution-plan.md`](pushes/astra-execution-plan.md) | Done |
| 3 | [`pushes/astra-push3-plan.md`](pushes/astra-push3-plan.md) | Done — gate part A passed, part B recorded as failed |
| 4 | [`pushes/astra-push4-plan.md`](pushes/astra-push4-plan.md) | Done |
| 4.5 | [`pushes/astra-push45-plan.md`](pushes/astra-push45-plan.md) | C1, C2 done · C3 not started · C4 gate ran and returned an honest negative |
| P12 | [`pushes/p12-execution-plan.md`](pushes/p12-execution-plan.md) | Phases 1–2 done in all three repos · Phase 3 and 4 not started |
| Recovery | [`pushes/astra-recovery-20260907/`](pushes/astra-recovery-20260907/) | Slices 1–2 in flight · 3–4 cut from this run |

**Superseded:** [`pushes/execution-plan.md`](pushes/execution-plan.md) — the
push-1 crew plan. Historical only. Do not execute it.

**How the recovery packet relates to the master plan:** it is repair work
*inside* the sequence, not a parallel track. Its slice 1 (readable
projections) and slice 2 (reachable knowledge) are the unfinished half of the
memory foundation Push 3 began. Its slices 3 and 4 were cut on 2026-09-07 as
not delivering either operator-visible outcome.

## The two outcomes everything is measured against

Recorded by Jon, and the only test that matters:

1. **Governed, organised Agent Memory** a fresh agent can actually use.
2. **Repo progressive disclosure** — `AGENTS.md` is a short index, with no
   duplicated stale summaries.

A push that does not move one of these has not delivered, however well it ran.

## Known state, measured 2026-09-07 — not claims

- Corpus: 10,371 prompts · 7,890 items · **0 rows in `links`** — nothing has
  ever been merged or superseded.
- Vault: 3,189 notes — **3,070 parameters against 119 facts**. The evidence
  layer grew 25x; the distilled layer did not. That ratio is the clearest
  statement of what is missing.
- Distillation: `internal/distill` groups restated rules deterministically and
  covers **3%**. Lexical similarity cannot see a rule restated in different
  words; the remaining recall needs the model step.
- Two MOC hubs hold **1,752 and 1,748 links**, because `corpus` and `vault`
  were treated as topic tags when they are the subject of nearly every note.
  Routing through them is unusable until they are re-tagged.

## Working records — read for history, not for orders

[`iteration-queue.md`](iteration-queue.md) is the running log of what was
dispatched, merged and found. [`next-plan-inputs.md`](next-plan-inputs.md) is
the queue of things noticed but not yet scheduled. Both are long and neither
governs anything.

`reports/` holds evidence from completed work. `briefs/` and
`briefs-dispatched/` hold what was handed to lanes. A brief is an instruction
to one worker at one moment — **it never overrides this file or the master
plan**, which is the error that produced the weekend this file exists to end.

## What is deliberately not here

Vault backups and fixtures stayed out of git: they are large, they are
regenerable, and durable copies live under
`~/.local/state/agent-estate/recovery/`. Nothing in `docs/plan/` should be a
backup.
