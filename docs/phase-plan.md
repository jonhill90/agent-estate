# Phase plan — the meta-harness

`Revised 2026-09-02 against Loops-Research 15-19 (3,562 lines, provenance-graded).`
`Supersedes the version merged in #907. What was dropped is listed at the end.`

Scope is the meta-harness. Still **not** the 40 open issues and **not** a
backlog mined from `reference/`. Every phase ends in something a human can
look at.

> **Nothing below is on `main` except what `git log main` shows.** Phase
> statuses name the pull request the work sits in. An earlier draft of this
> file said "SHIPPED" for work that was open and unmerged — the sixth time a
> reviewer has caught this author citing a branch as though it were the tree.
> `main` currently has the CI retirement (#913) and the previous plan (#907);
> everything else is a PR.

## The finding that reorders the plan

**Loop quality tracks the VERIFIER, not the loop.** Reflexion scores 91% on
HumanEval, where its self-written verifier is wrong 1.4% of the time — and
*underperforms its own baseline* on MBPP, where the same mechanism is wrong
16.3% of the time. Huang et al. go further: the self-correction gains in the
prior literature *"result from using oracle labels… and the improvements
vanish when oracle labels are not available"*; without them self-correction
*"consistently results in a decrease in performance."*

`docs/tick-log.jsonl`'s artifact field is **self-reported**. That is the
gameable seam, and this estate has demonstrated it five times: five successive
artifact rules — non-empty, a placeholder list, looks-like-a-pointer,
must-resolve, must-be-recent — were each defeated, by `"null"`, `"working on
it"`, `"still going, read/write path unclear"`, `"AGENTS.md"` and `touch
go.work`. **Every one was caught by an independent dispatched reviewer, never
by the loop inspecting itself.** The verifier is doing the work. Building more
loop was not the answer any of the five times, and a sixth rule is not being
attempted.

**Second finding: length is a failure signal — graded `[S]`, secondhand.**
Failure trajectories run *"12–82% longer than successful ones"*; failing
attempts consume *"up to 4× the resources of successful ones."* The research
read these in another paper's related-work section, not in the primaries, and
grades them `[S]` accordingly. So this is a lead worth measuring against, not
a result to reorganise around — which is why it buys one line in Phase 0
(measure tick cost) rather than a phase of its own. A long tick is a reason to
look, not proof of failure.

**Third: there is no prior art for half of what we said we are building.**
Across five comparable systems, *"nobody… orchestrates across models as a
first-class concern"*, none does cross-harness budget accounting, and **none
has a verification stage** — *"every one of them ends a run on 'process
exited' plus at best a status enum."* Those three are the differentiated part.
Nothing can be copied for them.

---

## Phase 0 — The verifier, not the loop `IN #914, NOT ON MAIN`

Was "make the loop able to report failure". Re-aimed: the stop condition is a
self-report and must be replaced by an external check.

- Tick artifacts verified **outside** the agent that wrote them: a token must
  post-date the previous tick (built, in #914), and an independent check must confirm
  the phase item moved.
- **Record tick cost and duration.** Length is a failure signal, so it has to
  be measured before it can be one.
- `estate verify-branch` (built, in #914) is the shape: check where the accumulated
  state does not exist.

**Done when:** a tick's claim is confirmed by something that did not write it.

## Phase 1 — Dispatch isolation `IN #909, NOT ON MAIN`

Worktree per dispatch, refuses when it cannot isolate, teardown refuses to
delete uncollected work. Verified end-to-end through two harnesses.

Remaining: the `--ignored` teardown rule over-refuses on build detritus
(`__pycache__`), leaking a worktree per dispatch. Fix is a known-detritus
list. Not a phase; a defect.

## Phase 2 — Make the estate visible `IN #910, NOT ON MAIN`

Home shows the live ledger and tick log. Still owes a real `pressure` reading.

New, from the research: **a parent must not see a child's intermediate
steps** — OpenClaw, Hermes and hve-core reached that independently, so treat
it as settled. The viewer shows a turn's *result*, never its transcript.

## Phase 3 — Cross-**model** orchestration `HARNESS HALF IN #911, NOT ON MAIN`

Was "cross-harness dispatch". That part is built and verified but sits in
#911, unmerged (claude + codex, one brief, both recorded). The research says the harness axis is the easy half and
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
  demoted: it is a self-report, and five defeats say a self-report is worth
  less than one external check.
(A fourth item — "copying prior art for the three differentiated areas" —
was listed here and removed: those three areas are framing this revision
introduced, so there was nothing in the #907 plan to drop. Padding a drop
list overstates how much was retracted.)

## One correction to what I was told

The claim that Anthropic deletes *~80%* of the Claude Code system prompt each
model release is not what the source says. Cherny says *"every time that a new
model comes out, we delete a bunch of the system prompt"*, and the ablation
(`CLAUDE_CODE_SIMPLE=1`) deletes **all** of them, finding the model *"a little
bit more intelligent without these prompts."* No percentage is given. The
direction stands and it indicts our own documents: at `43b93b5`,
`docs/director-brief.md` is **310** lines and `AGENTS.md` is **44,722 bytes**
describing a deleted supervisor. Cherny's instruction is blunt — *"every six
months, delete your [CLAUDE.md], delete your skills, delete your hooks."* The
bracket is the research's own correction: the transcript reads *"quantum D"*,
and `17-practitioners.md` flags the substitution as a reading rather than his
word. Shrinking our documents is Phase 0 work, because a brief nobody can hold
is another unverified claim.

**And this file is not exempt.** At `33e7e34` it was **174 lines / 1,439
words**, against **129** in the version it supersedes — 35% longer while
arguing for shorter documents, and roughly two and a half pages rather than
the "two pages, 160 lines" an earlier draft of this PR claimed. Both of those
figures were wrong in the flattering direction. This paragraph made it longer
again; measure it yourself with `wc -l` rather than trusting a number written
inside the file it describes, which is stale the moment it is saved. Cutting
it is Phase 0 work, and the next revision should be shorter or say why not.

## The risk that has not changed

Phases 0, 3, 4 and 6 live in `src/estate`, which `src/progress` scores as
"app" while showing the operator nothing. Every tick report names which of
`src/tui` / `src/estate` moved. A run of `src/estate`-only weeks is a signal to
reorder, not to keep scoring.
