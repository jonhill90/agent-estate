# Phase plan — the meta-harness

`Written 2026-09-02 by the Director. Awaiting operator approval — do not execute.`

Scope is the meta-harness itself. It is **not** the 40 open issues and **not** a
backlog mined from `reference/`. Every phase ends in something a human can look
at; a phase with no artifact is not done, however much code moved.

**The measurement rule that governs this whole plan:** `src/tui` is what the
operator can see. `src/estate` is what he cannot. Phases 0, 1 and 3 are almost
entirely `src/estate` and will show him nothing on their own — that is why
Phase 2 is scheduled third and not eighth.

---

## Phase 0 — Make the loop able to report failure

**Why first.** §3 of the brief defines a mechanical stop condition over
`docs/tick-log.jsonl`. That file does not exist, nothing writes it, and every
tick is a fresh context. A stop condition that depends on an agent remembering
is a sentence, not a guard — and this loop's whole purpose is to be unable to
run for days producing defensible nothing.

- `estate tick record` appends the §3 line; `estate tick check` exits non-zero
  when the last three entries share `phase_item` + `src_head` with
  `artifact: null`.
- The loop prompt calls `check` before it does anything else.
- Interval adapts: 3 min in flight, suspended while blocked on review.

**Done when:** a synthetic three-stalled-tick log makes `estate tick check`
exit non-zero, and removing the guard makes it exit zero. **Artifact:**
`docs/tick-log.jsonl` with real entries.

## Phase 1 — Make dispatch safe to run unattended

**Why second.** `estate dispatch` runs `claude -p --dangerously-skip-permissions`
with no worktree isolation and no cwd restriction, on a 3-minute loop. He has
already had a machine damaged by orphaned agent work. Everything after this
phase runs *through* dispatch; if this is wrong, the blast radius is every later
phase.

- One git worktree per dispatch; cwd confined to it; the worktree is the only
  writable surface.
- A dispatch that cannot be isolated **refuses** — blindness is not capacity.
- Settle the worker-model question in the same pass: `model_tier=cheapest_
  sufficient` (`it-e44fb5f85459f7ba`, hard) vs the question-sourced
  `worker_model=sonnet` (`it-5d5dba1f971402ef`). Measure a real task at each
  tier rather than assert one.

**Done when:** a brief instructed to write outside its worktree fails, and the
same brief succeeds inside it. **Artifact:** that test, plus a cost-per-task
reading at two tiers.

## Phase 2 — Make the estate visible

**Why here and not later.** After Phases 0–1 the estate is safer and he still
cannot see it. `src/tui` is 347 files of nav and largely stubs. One screen that
answers *what is the estate doing right now* — pressure, in-flight dispatches,
the tick log, the current phase item — is the first thing this plan gives him
that is not a claim.

**Done when:** `estate` (the TUI) shows live state from the real ledger and a
real `pressure` reading with no fixture in the path. **Artifact:** a captured
frame. Stub panes stay honestly labelled stubs; this replaces one, not twenty.

## Phase 3 — Cross-harness dispatch

Dispatch today is Claude-only, so this is a harness, not a meta-harness. One
dispatch interface, N adapters behind it (`claude -p`, `codex`, `pi --mode
rpc`, ACP). The chain of command he stated — director → supervisor → workers
(`it-7d6902ac9a42ab55`, hard) — is what the adapter boundary has to preserve.

**Done when:** the same brief runs through two different harnesses and both
land in the ledger with the same task shape. **Artifact:** the two ledger
entries, side by side.

## Phase 4 — Intent as a live system, not a frozen file

The corpus is the only record of what he wants, and it is frozen: capture is a
hook local to *this project*, so his prompts in hill90-app, Hill90 and
agent-dotfiles — which supplied most of the 873 live hard constraints — are not
captured at all. Its last genuine prompt is 2026-08-30.

- Capture across the projects he actually works in.
- A parameter cited in a plan, brief or PR must join to its source prompt or be
  refused at the point of citation, not audited afterwards.
- `corpus-audit` output stays out of the repo (§10) — the join is the product,
  not the text.

**Done when:** a brief citing an unjoinable parameter is rejected by the tool
that builds it. **Artifact:** a dispatched brief whose every citation resolves.

## Phase 5 — Council on demand

Retire the standing council seat (§6) in favour of members assembled per
question by progressive disclosure — genuinely different evidence per seat, not
personas. His rule: never let an agent answer from its weights; test, measure,
work with at least one other agent with a different lens.

**Done when:** a real decision this estate faces is put to an assembled council
and the seats disagree on evidence, not on tone. **Artifact:** that transcript.

## Phase 6 — Loops and graphs, retiring the 3-minute tick

The tick is a stopgap the brief names as one. Replacing it with real loop and
graph engineering — triggers, dependencies, terminal states, a loop that can
say it is finished — is the product, not scaffolding for it. Crons become
things he manages from the UI, which is what he said he was waiting for.

**Done when:** the Director runs on a loop it did not have to be told the
interval of, and the 3-minute cron is cancelled without the estate stalling.

---

## What this plan deliberately does not do

- **Does not touch the 40 open issues.** They were dropped on purpose.
- **Does not fix `AGENTS.md` as its own phase.** It is stale and it is what a
  cold agent reads, so it gets corrected inside whichever phase proves a claim
  in it wrong — not as a documentation project.
- **Does not mass-close anything.** Bookkeeping is mine; a hundred issues is a
  decision.

## The risk I would name if asked for one

Phases 0, 1, 3 and 4 all live in `src/estate`. `src/progress` counts that as
"app", so a week spent entirely there scores clean and shows him nothing. Every
tick report will name which of `src/tui` / `src/estate` moved, and a run of
`src/estate`-only weeks is a signal to reorder, not to keep scoring.
