# Supervisor tick

One iteration of the architecture supervisor loop. If your context was lost,
read `brief.md` in this directory first — it is the full standing context.

## What to do

Sweep GitHub across the four repos — `agent-dotfiles`, `skills`,
`skills-private`, `agent-evals` — for **actionable** work:

- open issues **not** gated on a Jon decision,
- PRs needing independent review or a merge,
- failing CI.

**If there is actionable work:** claim it (see "Claim the issue before you
dispatch it"), then dispatch it to a free lane with a fresh bounded brief and a
**backgrounded** `tmux wait-for` waiter, so lanes run concurrently rather than
serially. Review, then merge what passes. You have full autonomy to merge and
close in these four repos.

`/clear` a lane before reusing it for an independent review — an author
reviewing their own PR is not an independent reviewer.

## "Blocked on Jon" is often true of an issue and false of a piece of it

Before recording an issue as gated, ask what specifically is gated. The gate is
usually on one verb, and the rest of the issue is ordinary work.

Worked example, 2026-08-11. `agent-evals#3` reads as fully blocked: every
redesign it proposes changes what a scenario measures, so each needs fresh
baselines, and baselines are billed agent sessions the owner must authorise.
True — and it hid a deliverable. The issue's own text named a fixture as
"specified and not written". **Writing a fixture costs nothing. Running one is
what costs money.** The fixture was built that night, unrun, and is now waiting
for whenever the baselines are authorised.

The same split recurs:

- **run vs write** — the run is billed, the artifact is free
- **decide vs prepare** — the decision is Jon's, the options paper is not
- **deploy vs build** — `$HOME` mutation is gated, the PR is not
- **measure vs explain** — a billed measurement is gated, reading the code to
  work out *why* is free and often worth more. `#44` produced a better answer
  under a no-billed-runs constraint than the measurement would have.

Two failure modes, and the second is the common one:

- Doing the gated part anyway. Obvious, and the constraints catch it.
- **Recording the whole issue as blocked and moving on.** That looks like
  discipline and is how work quietly stops. A tick that reports "all gated on
  Jon" without having asked *which verb* is gated has not finished its sweep.

Say in the report which part was gated and which part was done, so the
distinction survives into the next tick.

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

## Dispatch with `dispatch.sh` — it is the whole dispatch, in one command

Write the brief to a file, then:

```bash
scripts/supervisor/dispatch.sh 81 dispatch-worktree \
  ~/.local/state/agent-dotfiles-supervisor/ad81-brief.md \
  jonhill90/agent-dotfiles ~/source/repos/Personal/agent-dotfiles
```

It picks a free lane — idle **and** named `free-N`, both required — claims the
issue, **creates the worktree**, renames the
window, and sends the brief — the five steps the rest of this file describes,
performed rather than recited. Any refusal aborts the whole dispatch and undoes
what it already did, so a non-zero exit means nothing happened and the issue is
still available. Exit 0 means a lane has the work.

The sections below are why each step exists and what its refusals mean. Read
them to interpret a refusal; do not re-perform the steps by hand. Running the
pieces individually is how the worktree step got skipped all night, which is
`#81`: `worktree.sh` shipped with no caller, so "give the lane a worktree"
stayed a sentence someone had to remember, which is exactly the failure mode
that produced `#73`.

## Check lane health before dispatching — do not trust "idle"

Run this first, every time you are about to dispatch:

```bash
scripts/supervisor/lanes.sh          # full table
scripts/supervisor/lanes.sh --free   # only lanes safe to dispatch to
```

**Dispatch only to lanes it reports `free`.** The supervisor's own window is
reported `supervisor` and withheld from `--free` — sending a worker brief there
`/clear`s the loop and replaces it with someone else's task. That happened twice
on 2026-08-11, by two different mechanisms with the same outcome, which is why
the rule now lives in the tool rather than in someone's memory. `capture-pane` alone cannot tell
these apart, and all of them were misread as "nothing to do" on 2026-08-11:

- `free` — an agent is running and waiting. Dispatch here.
- `busy` — mid-turn. Leave alone.
- `hung` — looks busy but has stopped advancing. A dispatch queues forever.
- `dead` — no agent, just a shell. A dispatch lands in `zsh`, which replies
  `no such file or directory: /clear`, and the work is silently lost. Restart
  the agent with `claude --dangerously-skip-permissions` before using the lane.
- `unknown` — a non-Claude harness. There is no probe for it; do not guess.

This tool exists because a dispatch was sent into a dead lane and vanished, and
because a lane wedged for 40 minutes was read as busy and left alone. Reading
the table costs one command; both of those cost a whole tick.

## Claim the issue before you dispatch it

`lanes.sh` answers *is this lane safe to dispatch to*. This is the orthogonal
question: *has this work already been taken*. A perfectly healthy free lane is
exactly where duplicate work lands.

Select work with `claim.sh list`, never a bare `gh issue list`:

```bash
scripts/supervisor/claim.sh list jonhill90/agent-dotfiles   # open AND unclaimed
```

`dispatch.sh` then takes the claim itself, before it builds anything and long
before the `send-keys`. It prints the holder and **exits non-zero** if someone
got there first. Treat that as "pick different work", not as an error to retry.

The claim is the GitHub **assignee**. In these four repos an assignee means
*claimed by a lane* — do not hand-assign an issue you are not dispatching. The
claim is released when the PR closes the issue; release it by hand with
`claim.sh release <n> <repo>` if the lane abandons the work.

This exists because on 2026-08-11 issue #28 was dispatched to two lanes
independently — once by the Director, once by the supervisor — and both
produced complete, near-identical fixes (#68 merged, #69 closed). About an hour
of lane work was spent twice. Neither dispatcher was wrong: `gh issue list`
shows an open issue whether or not another lane took it ninety seconds ago.

**Claims expire with the lane, not on a clock.** A task here can legitimately
run for hours, so a timeout short enough to be useful would steal live work.
Before dispatching, check for claims whose lane is gone:

```bash
scripts/supervisor/claim.sh stale jonhill90/agent-dotfiles
```

It reports a claim as stale when no live lane window names that issue and no
open PR says it fixes it — a `dead` lane from `lanes.sh` is exactly the case it
catches. It **reports only**: releasing is your call, because a bare issue
number cannot tell `agent-dotfiles#70` from `skills#70`, and leaving a claim in
place costs a tick while dropping a live one costs an hour.

## Give the lane its own worktree — never brief work into the shared checkout

Every lane, the Director, and the supervisor share one working tree,
`~/source/repos/Personal/agent-dotfiles`. Two lanes editing it at once is not
a hypothetical: on 2026-08-11 a lane working #28 had its branch switched out
from under it mid-task by another lane. Its uncommitted edits to four files
were discarded, and its staged deletion of a file was swept into an unrelated
lane's commit, which shipped in a PR whose own message never mentions the
deletion. That is the shared checkout destroying one agent's work and
silently corrupting another's commit (#73).

`dispatch.sh` creates the worktree and hands the lane a ready path in the
message it sends, rather than telling the lane to create one — a step in a
brief is a step that can be skipped, and for one night this one was: the tool
existed and no dispatch called it (`#81`).

If `worktree.sh new` fails, `dispatch.sh` sends nothing and exits non-zero. A
lane with no worktree works in the shared checkout, which is the bug, not a
degraded mode — so a failed worktree is a failed dispatch. Fix the cause and
dispatch again.

On completion — PR opened, or the lane abandoning the task — remove it:

```bash
scripts/supervisor/worktree.sh done <path>
```

`done` refuses and prints `git status` if the worktree is dirty. Treat that
refusal as "someone's work is still here", not as an error to force past —
matching the same rule `safe-deletion` applies to any other directory.

**This applies to the Director too.** It has been branching directly in the
shared checkout all night — the same bug, not a different one. Before
branching or checking out anything there, confirm it is actually clean:

```bash
scripts/supervisor/worktree.sh guard ~/source/repos/Personal/agent-dotfiles
```

A non-zero exit means the shared checkout is dirty. That is someone's live
work, not a base to branch on — leave it alone and use a worktree instead of
proceeding.

## An empty tmux target hits the ACTIVE window — always resolve it first

`tmux send-keys -t agent-dotfiles:` with an empty index does **not** error. It
targets whatever window is currently active, which is usually the supervisor.

This happened on 2026-08-11. A dispatch computed its window index from
`lanes.sh --free`, which returned nothing because that script did not exist on
the branch that was checked out. The index was empty, the target collapsed to
the session's active window, and the supervisor was `/clear`ed and handed a
worker's brief — losing its loop context and spending a turn duplicating a
review another lane was already running.

`dispatch.sh` resolves its lane from `lanes.sh --free` **intersected with the
`free-N` naming rule below**, and refuses an empty target, so a dispatch made
through it cannot hit the supervisor. It takes no lane from the environment:
`DISPATCH_LANE` was an override that skipped every one of those checks, and
`DISPATCH_LANE=t:1` reproduced this same incident at exit 0, so it was removed
(#89). To aim a dispatch at a specific lane, rename that lane `free-N` first.
For any other
`send-keys` or `rename-window`, resolve the index yourself and refuse to
proceed if it is empty:

```bash
IDX=$(tmux list-windows -t agent-dotfiles -F '#{window_index} #{window_name}' \
      | awk -v n="$LANE" '$2==n{print $1}')
[ -n "$IDX" ] || { echo "no window named $LANE"; exit 1; }
```

The same rule covers the empty-variable family generally: a target built from
a command's output is only as safe as that command's success, and `tmux`
treats "no index" as "the one the human is looking at".

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
