# Brief — Recovery slice 1: repair the projection producer and prove it

**Owner (implementation):** worker lane in tmux window `agent-estate:2` (lane-b).
**Independent reviewer:** worker lane in `agent-estate:3` (lane-c). Not you.
**Issued by:** Director (run manager), 2026-09-07.
**Base:** `origin/main` = `1b581de4b01a507bb860475d9eab6905c8743988`. Branch from it.
**Timebox:** pilot delivered within 45–60 minutes of receipt. Deliver the
verified part and name the remainder rather than overrunning silently.

Your session is fresh and shares no memory with earlier work. This brief is
self-contained. Do not infer context from your window name.

## Read first, in order

1. `run/astra-recovery-20260907/SPEC.md` §2 — the contract you implement.
2. `run/astra-recovery-20260907/EXECUTION-PLAN.md` §1 — the slice definition.
3. `run/astra-recovery-20260907/PRD.md` — why this matters.

Where this brief and SPEC §2 conflict, **SPEC §2 wins**; tell me about the
conflict.

## The problem

`internal/vaultview` generates 2,638 managed projections into
`01 - Notes/01p - Parameters/`. They are currently unreadable in three ways:

- **Boilerplate descriptions and a repetitive footer** on every note.
- **Type-plus-ID titles** — e.g. `Directive it-b29425780b4cd06c` — which name
  nothing a human or an agent can act on.
- **An old one-time task directive presented as apparent standing law**,
  because nothing distinguishes a spent instruction from a live rule.

The third is the dangerous one: it lets an agent act confidently on an
expired order.

## Scope you own

`internal/vaultview`, plus whatever publication-metadata support is genuinely
needed in `internal/candidates` / `internal/notemeta`, plus focused tests.

**No layout changes. No note-ID renames. No new sidecar store.**

## THE HARD CONSTRAINT — read this before you write a single file

Recorded as **agent-estate#1286**, verified today.

The one declared standing-law member lives at
`01 - Notes/01f - Facts/202609060005.md`. `internal/corpus/standinglaw.go`
refuses resolution if that file's sha256 stops matching the pinned prefix
`ecf40670309d`, and `main.go` turns that refusal into
`estate: refusing to dispatch` + `os.Exit(1)` — for **every dispatch on the
estate**, not just yours.

Today's tagging pass wrote to 112 of the 119 files in `01f - Facts`. That one
was spared only because it carries a `candidate_id` and `TagNotes` refuses
receipt-bearing notes — an unrelated guard that happened to point the same
way. We avoided halting the estate by luck, not design.

So: **any bulk write must either exclude the StandingLawSet member file(s),
or re-pin `HashPrefix` in the same PR showing the before/after hash.** State
which you did in the PR body. Your slice targets `01p - Parameters` and this
file is in `01f - Facts`, so exclusion should be natural — but prove it, do
not assume it. Verify the hash still matches the pin when you finish:

```
shasum -a 256 "$AGENT_MEMORY_VAULT/01 - Notes/01f - Facts/202609060005.md"
# must still start with ecf40670309d
```

## Red first — reproduce before you fix

Using **synthetic fixtures in the real nested layout** (`01 - Notes/01p -
Parameters/`, not a flat temp dir), reproduce all three defects: boilerplate
description/footer, type-plus-ID title, and a spent task directive rendered
as apparent standing law. Include two harder cases the plan names explicitly:

- an **ambiguous fragment** (a note whose text does not determine a subject), and
- a **previously edited / tagged note** (must survive regeneration intact).

These tests must fail before your change and pass after.

## Green — implement SPEC §2

- Titles name the subject; descriptions convey the useful statement.
- A deterministic excerpt is acceptable **only** where it stays accurate and
  readable.
- Provenance retained **once**, in structured metadata — drop the repetitive
  footer and the generic description template.
- Preserve `corpus_item → note ID`, source links, body meaning, lifecycle,
  associative tags, and `## Relations`.
- Correct misleading authority presentation with **explicit scope**. Do not
  change `StandingLawSet` and do not silently weaken any binding corpus rule.
- A fragment needing context gets a **reviewed editorial proposal**, never an
  invented expansion. Unresolved fragments are explicitly labelled and
  excluded from authority-bearing use.
- Accepted editorial title/description must **survive regeneration**.

## Pilot — 12 real notes, chosen BEFORE you edit anything

Select and record the 12 up front, so the sample cannot be chosen to flatter
the result: **four** fallback titles, **four** fragments/short commands,
**four** already-useful notes, including overlap with associative tags and
`## Relations`, and inactive/draft states where available.

Read the **whole before/after file** for each. Put both in the PR or an
evidence file. The reviewer checks meaning against the cleaned source and
context — **not a length ratio**.

**Do not force an ambiguous note into an invented fact to finish the batch.**
An honest "unresolved, needs editorial review" is the correct output for a
note whose subject genuinely is not determinable. I would rather see four
unresolved than four confabulations.

## Explicitly NOT in this slice

Do **not** run the mechanical repair over all 2,638 projections. That waits
for pilot approval, and then runs in checksummed batches with a validator per
batch and a second unchanged run that must write **zero** files. Mechanical
completion and editorial completion get reported separately.

Do not touch the shared knowledge index at
`~/.local/state/agent-estate/knowledge/knowledge/index.json` — build a private
one under `ESTATE_KNOWLEDGE_INDEX` if you need retrieval. Do not reorganize
docs (#1285 owns that, merged). Do not touch `docs/knowledge-workflow.md`.

## Vault-writer discipline

You are the single vault writer for this slice. Before any vault write,
snapshot: `find "$AGENT_MEMORY_VAULT" -mmin -10 -name '*.md'` — if that shows
another writer, stop and tell me. Checksummed backup before writing; the
vault validator (`99 - Meta/tools/validate_index.py`) after; restore that
batch and halt on any failure.

## Deliverable

One PR against `origin/main` from `1b581de`. Body carries: what it does, real
command output as evidence, the 12 before/afters (or a pointer to the
evidence file), what it deliberately did not do, what the reviewer should
attack first, and the #1286 statement.

**Push once, then HOLD.** A head that moves under a reviewer cost this run
three review rounds on #1282. Reply `DONE` with the PR number and SHA.
Lane-c reviews; the Director merges. You never self-merge.
