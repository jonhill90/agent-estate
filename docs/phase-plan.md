# Phase plan — the meta-harness

`Revised 2026-09-02 against Loops-Research 15-19 (3,562 lines, provenance-graded).`
`Supersedes the version merged in #907. What was dropped is listed at the end.`

Scope is the meta-harness. Still **not** the 40 open issues and **not** a
backlog mined from `reference/`. Every phase ends in something a human can
look at.

## The finding that reorders the plan

**Loop quality tracks the VERIFIER, not the loop.** Reflexion scores 91% on
HumanEval, where its self-written verifier is wrong 1.4% of the time — and
*underperforms its own baseline* on MBPP, where the same mechanism is wrong
16.3% of the time. Huang et al. go further: the self-correction gains in the
prior literature *"result from using oracle labels… and the improvements
vanish when oracle labels are not available"*; without them self-correction
*"consistently results in a decrease in performance."*

`docs/tick-log.jsonl`'s artifact field is **self-reported**. That is the
gameable seam, and this estate has already demonstrated it four times: four
successive artifact rules (non-empty → placeholder list → looks-like-a-pointer
→ must-resolve) were each defeated, and **every one was caught by an
independent dispatched reviewer, never by the loop inspecting itself.** The
verifier is doing the work. Building more loop was not the answer any of the
four times.

**Second finding, equally decision-relevant: length is a failure signal.**
Failure trajectories run *"12–82% longer than successful ones"*; failing
attempts consume *"up to 4× the resources of successful ones."* A long tick is
evidence of trouble, not of effort. Velocity is not progress and neither is
duration.

**Third: there is no prior art for half of what we said we are building.**
Across five comparable systems, *"nobody… orchestrates across models as a
first-class concern"*, none does cross-harness budget accounting, and **none
has a verification stage** — *"every one of them ends a run on 'process
exited' plus at best a status enum."* Those three are the differentiated part.
Nothing can be copied for them.

---

## Phase 0 — The verifier, not the loop `NOW`

Was "make the loop able to report failure". Re-aimed: the stop condition is a
self-report and must be replaced by an external check.

- Tick artifacts verified **outside** the agent that wrote them: a token must
  post-date the previous tick (shipped), and an independent check must confirm
  the phase item moved.
- **Record tick cost and duration.** Length is a failure signal, so it has to
  be measured before it can be one.
- `estate verify-branch` (shipped) is the shape: check where the accumulated
  state does not exist.

**Done when:** a tick's claim is confirmed by something that did not write it.

## Phase 1 — Dispatch isolation `SHIPPED, one gap`

Worktree per dispatch, refuses when it cannot isolate, teardown refuses to
delete uncollected work. Verified end-to-end through two harnesses.

Remaining: the `--ignored` teardown rule over-refuses on build detritus
(`__pycache__`), leaking a worktree per dispatch. Fix is a known-detritus
list. Not a phase; a defect.

## Phase 2 — Make the estate visible `PARTIAL`

Home shows the live ledger and tick log. Still owes a real `pressure` reading.

New, from the research: **a parent must not see a child's intermediate
steps** — OpenClaw, Hermes and hve-core reached that independently, so treat
it as settled. The viewer shows a turn's *result*, never its transcript.

## Phase 3 — Cross-**model** orchestration `RE-AIMED`

Was "cross-harness dispatch", and that part shipped (claude + codex, one
brief, both recorded). The research says the harness axis is the easy half and
the differentiated half is models: routing a workload to a model on grounds of
cost, capability or **independence**, and comparing results across them.

- **Copy Paperclip's `AdapterSessionCodec`** — per-harness
  `serialize`/`deserialize`/`getDisplayId` turning a foreign session handle
  into one durable column. *"The single most transferable idea in the whole
  survey."* Our `harness.Harness` has no session concept at all, so no
  dispatch can be resumed.
- **Copy OpenClaw's durable retry budget with a tombstone state.** Without it
  crash-resume is a crash-loop.
- Independence has a use we already rely on: two models disagreeing is a
  verifier. Codex and claude reached different verdicts on #913 for a real
  reason (codex's sandbox has no network), and that disagreement was
  informative.

**Done when:** the same brief runs on two models and the estate records which
was cheaper, which was right, and how they differed.

## Phase 4 — Cross-harness budget accounting `NEW`

Nobody does this. `estate pressure` reads one weekly figure; there is no spend
ledger across heterogeneous harnesses, so "cheapest sufficient model"
(`it-e44fb5f85459f7ba`, hard) cannot be obeyed with evidence. Includes settling
the worker-model question by measurement rather than by the question-sourced
`worker_model=sonnet`.

## Phase 5 — Intent as a live system

Unchanged in substance. Capture beyond this project; a parameter cited in a
plan or brief must join to its source or be refused at the point of citation.
This is the oracle-label problem applied to the corpus: a parameter that cannot
be traced is a self-report too.

## Phase 6 — Survival, and naming the reasoning architecture

- **The Director is ring 3 and dies with its session.** Rings 0–3 do not
  survive; only 4–5 do. The operator forbids scheduling outside the Claude
  Code ecosystem (`it-7d6902ac9a42ab55`, hard, verbatim). **These conflict.**
  A durable loop needs an escalation path that is not launchd — most likely
  the estate scheduling itself from inside a surviving process. Named here as
  an intent question, not decided.
- **No reasoning architecture has been chosen.** Dispatch runs `claude -p` and
  accepts whatever it does — ReAct by default, unnamed and unmeasured.
- Director→workers is **Magentic/Handoff**, a named pattern with prior art and
  a published caveat: *"it is untested how well the Magentic orchestration will
  perform outside of the original Magentic-One design."*

---

## What I dropped, and why

- **"Council on demand" as its own phase.** A council is a verifier; it
  belongs in Phase 0 rather than sitting six phases away from the finding that
  makes it matter.
- **"Retire the 3-minute tick" as a goal.** Cadence was never the problem. The
  loop's honesty was. Survival (ring 4) is the real content and moved to
  Phase 6.
- **The tick-log stop condition as the loop's primary guard.** It stays, but
  demoted: it is a self-report, and the four defeats say a self-report is worth
  less than one external check.
- **Copying prior art for the three differentiated areas.** There is none.
  Planning to borrow was a plan to be surprised late.

## One correction to what I was told

The claim that Anthropic deletes *~80%* of the Claude Code system prompt each
model release is not what the source says. Cherny says *"every time that a new
model comes out, we delete a bunch of the system prompt"*, and the ablation
(`CLAUDE_CODE_SIMPLE=1`) deletes **all** of them, finding the model *"a little
bit more intelligent without these prompts."* No percentage is given. The
direction stands and it indicts our own documents: `docs/director-brief.md` is
302 lines and `AGENTS.md` is 42KB describing a deleted supervisor. Cherny's
instruction is blunt — *"every six months, delete your CLAUDE.md, delete your
skills, delete your hooks."* Shrinking them is Phase 0 work, because a brief
nobody can hold is another unverified claim.

## The risk that has not changed

Phases 0, 3, 4 and 6 live in `src/estate`, which `src/progress` scores as
"app" while showing the operator nothing. Every tick report names which of
`src/tui` / `src/estate` moved. A run of `src/estate`-only weeks is a signal to
reorder, not to keep scoring.
