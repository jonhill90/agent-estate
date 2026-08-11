# Supervisor tick

One iteration of the architecture supervisor loop. If your context was lost,
read `brief.md` in this directory first — it is the full standing context.

## What to do

Sweep GitHub across the four repos — `agent-dotfiles`, `skills`,
`skills-private`, `agent-evals` — for **actionable** work:

- open issues **not** gated on a Jon decision,
- PRs needing independent review or a merge,
- failing CI.

**If there is actionable work:** dispatch it to a free lane with a fresh
bounded brief and a **backgrounded** `tmux wait-for` waiter, so lanes run
concurrently rather than serially. Review, then merge what passes. You have
full autonomy to merge and close in these four repos.

`/clear` a lane before reusing it for an independent review — an author
reviewing their own PR is not an independent reviewer.

## If everything left is gated on Jon

Currently gated: **#24** herdr build-vs-adopt · **#20** apm pin bump ·
**skills#129** naming · **#28** duplicate identities · the **Hill90 strip PR**
(propose-only, never merge in Hill90).

Then:

- Do **not** invent work.
- Do **not** re-verify what is already done and recorded.
- Do **not** open cosmetic or documentation-churn PRs to look busy.
- Say in **one line** that you are blocked and on what, then schedule the
  **longest** wakeup available (3600s).

**Never stop the loop.** Do not call `ScheduleWakeup` with `stop: true`, and do
not treat "everything is gated on Jon" as a terminating condition. A stopped
loop cannot notice that Jon unblocked something — it requires him to come back
and re-arm it by hand, which is the thing this loop exists to avoid. Blocked is
a *state to sleep through*, not a reason to exit. Only Jon ends the loop.

This is a correction to an earlier version of this file, which said "schedule a
long wakeup" without forbidding `stop`. On 2026-08-10 the supervisor read that
as permission to stop, reasoning that further ticks could not make progress. The
reasoning was sound and the action was wrong: it ended the loop, and Jon's next
observation was "its not looping."

A blocked tick must be **cheap**, which is what makes an hourly sweep
affordable: list open issues and PRs across the four repos, compare against the
gated list, and if nothing has changed, sleep. No re-reading the codebase, no
re-running suites, no fresh analysis of already-recorded decisions. Cost per
blocked tick should be a few tool calls, not a work session.

Waking with nothing to do and *doing something anyway* is the v1 failure mode —
it queued a prompt every five minutes into a usage-blocked session and delivered
119 identical prompts. The defect there was the redundant work, not the wakeup.
An honest "blocked on Jon" followed by a long sleep is the correct output of a
tick, not a failure of one.

## Boundaries

- Never touch `Hill90`, `hill90-app`, or `hill90-docs` beyond propose-only.
- Still requires Jon: repo creation/deletion/visibility, force-push or history
  rewrite, deleting retained data, anything touching production or the real
  `$HOME` (`sync.py apply`, `install.sh`, any `apm` mutation).
- Commits: Jon Hill \<jonhill90@live.com\> sole author, no co-author trailers.

## Before you finish the tick

Keep `brief.md` current as state changes — it is what a cold session resumes
from. Durable preferences and decisions go to the Obsidian vault under
`agent/facts/` with an index line, not into this file.

## Lane names must say what is happening right now

Jon must be able to read the tmux window list and know what the estate is
doing, without asking an agent and without opening a pane. On 2026-08-11 he
said: *"i dont like the lanes being called worker- i cannot tell what is going
on."* He was right, and the history matters — the lanes were originally named
after the task they last ran (`docs-loop-acp`, `findings-acd`), which went
stale the moment a lane was cleared and reused, so they were renamed to
`worker-N`. That traded a misleading label for no label.

**Rename the window on every dispatch and on every completion.** It is one
extra tmux call per dispatch and it costs nothing:

```bash
tmux rename-window -t agent-dotfiles:<n> 'skills139-verify-instrument'   # on dispatch
tmux rename-window -t agent-dotfiles:<n> 'free-<n>'                      # on completion
```

Rules:

- **Dispatching:** name it `<repo><issue>-<short-slug>`, e.g. `skills140-loop-contract`.
  Short enough to read in the status bar; the issue number makes it traceable.
- **Finished and available:** rename back to `free-<n>`. A lane called `free-3`
  is unambiguous; a lane still carrying a task name is still working on it.
- **Never leave a completed lane named after its finished task.** That is the
  stale-label failure this rule exists to prevent.

The window list then answers "what is going on" at a glance: anything named
`free-N` is available, anything else is in flight and says what it is doing.
