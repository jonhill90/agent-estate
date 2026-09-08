# Director handoff — knowledge-architecture run, 2026-09-06 16:03 EDT
# (corrected 16:4x: manager is director:1)

**You (the Claude director, `director:1`) manage this run.** Jon authorized
it; Astra returns ~19:15 EDT and takes over planning for whatever is left.
Ping Fable (Jon's interactive Claude session) via Jon when a judgement call
is needed.

Codex (`director:2`) was briefed for CONTEXT only — it holds the plan-review
history and may advise or take a bounded review on request, but it does not
manage lanes or merge: OpenAI quota is ~10% for the next ~3 hours, and one
run has one manager. An earlier version of this file addressed it as
manager; that was Fable's error, since corrected with both parties.

While this run is active, hold NEW estate-CLI dispatches (e.g. the
retrieval-reliability brief, #1262's post-merge review) unless Jon says
otherwise — capacity belongs to the three lanes; queue such items in
`run/next-plan-inputs.md` for the 19:15 plan instead. Your scheduled
backstop/watcher ticks continue as normal.

## The run

- Master plan: `run/execution-plan.md` (this directory). Approved source
  plan: `/tmp/knowledge-plan-review.hybPxD/knowledge-architecture-plan.md`;
  review trail: `/tmp/knowledge-plan-review.hybPxD/review-findings.md`.
- tmux session `agent-estate`, windows `lane-a`, `lane-b`, `lane-c` — three
  Claude Sonnet workers (`cdsp --model sonnet --effort medium`), one
  worktree each under `~/source/repos/Personal/agent-estate-lanes/`.
- Briefs: `run/brief-lane-{a,b,c}.md`. Lane ids are `agent-estate:<window index>`.

## Your jobs, in order of importance

1. **Merge protocol** (the scoped exception Jon approved — one push only):
   merge a lane PR with `gh pr merge --squash` ONLY when (a) a cross-lane
   review comment `Verdict: APPROVE` + `Review-Lane` + `Reviewed-SHA` exists
   for the EXACT current head SHA, (b) required checks are green at that
   SHA, (c) reviewer lane ≠ author lane. Review cycle: A reviews C, B
   reviews A, C reviews B. One fix pass per PR; second failed review →
   close and file what remains. Never let a worker merge. No fabricated
   dispatch records.
2. **Sequence**: A's navigation/contract PR merges first, then B, then C,
   then A's final guide/skill PR. Each later branch rebases onto merged
   work; a changed head needs a renewed review at the new SHA.
3. **Integration (step 4)**: after C merges — integrate B's staged views
   from `run/views-staging/` into the vault (you are the vault writer at
   that point; lanes A and B must have stopped writing), run the integrated
   fixture, one real publication, refresh, and the demonstration in
   `run/demo-task.md` (its wording is pinned; do not re-word it after the
   real publication). Then C's evidence PR merges last.
4. **Health**: check lanes periodically (capture-pane; is it progressing or
   waiting?). Unstick with short, targeted messages. If host pressure
   degrades (`go run ./src/estate pressure` from a main checkout), pause
   dispatching new work and tell Jon.
5. **Checkpoint**: before ~19:15 EDT make sure each lane wrote
   `run/handoff-<lane>.md`; summarize run state (merged / open / failed /
   unmeasured) for Astra's next plan.

## Escalate to Fable/Jon (don't decide alone)

- Any conflict between two lanes' changes to the same surface.
- Any temptation to bypass the merge protocol or a failing guard.
- The demonstration failing its criteria.
- Anything requiring vault deletion, Second Brain contact, or scheduler /
  model / capacity changes (all out of scope this run).
