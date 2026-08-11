# Supervisor tick

One iteration of the architecture supervisor loop. If your context was lost,
read `brief.md` in this directory first — it is the full standing context.

## Read the Director's inbox first, before anything else

```bash
scripts/supervisor/director-inbox.sh drain
```

Constraints and corrections from the Director are meant to arrive here, not
as messages typed into your pane. Act on anything it returns before doing the
rest of the tick — it is usually a correction that changes what the rest of
the tick should do, and acting on it late means work gets done wrong first.

Why it works this way, and it matters: a dynamic `/loop` stays alive by
scheduling its own wakeup at the end of each turn. **A plain message sent to
your pane replaces the loop prompt**, so the next turn is an ordinary turn and
nothing re-arms. The loop ends silently, and the watchdog cannot tell that
from a crash — both look like "idle pane, agent alive, no pending wakeup".

Measured on 2026-08-11 (#85): 27 `/loop` messages since 09:00 produced zero
`ScheduleWakeup` calls. The watchdog restarted three times; each restart did
real work and was then ended by the Director sending a constraint; the third
tripped the escalation cap and paged Jon at 09:34:49Z — for a condition the
Director itself kept re-creating.

The pane is convention-single-writer: nobody but the watchdog's `/loop` is
supposed to write into it. Nothing enforces that yet — it depends on every
caller reading this file and using `director-inbox.sh post` instead of a raw
`send-keys`. See #81/#88.

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

## Before you merge, check what the merge will CLOSE

```bash
gh pr view <n> --json closingIssuesReferences --jq '.closingIssuesReferences[].number'
```

If that list does not match what you intend to close, fix the PR body before
merging. It is the only way to see this — the PR body reads as prose and the
linkage is invisible in it.

**GitHub's closing-keyword parser is not negation-aware.** A PR body saying

> It does not explain or close #NNN

links issue NNN as a closing reference and auto-closes it on merge. The sentence
promising not to close the issue is what closes it. Caught on #98 by review,
after the PR had been open for a full tick and read past twice.

Two things that follow, both learned the hard way on that PR:

- The **commit message** counts too, not just the body. A squash merge folds
  commit messages into the merge commit, so amend the branch as well.
- Writing the explanation reintroduces the bug. The note added to #98's body
  describing this trap repeated the same keyword-then-number pattern three more
  times and re-linked the issue. **On a PR that should close nothing**, break
  every `close|closes|closed|fix|fixes|resolve|resolves` followed by
  `#<number>` — including inside quotes and explanations — then re-check and
  confirm the list is empty.

**The target is "matches intent", never "empty".** A PR that genuinely resolves
an issue SHOULD carry `Fixes #N`, and stripping it breaks something real:
`claim.sh stale` finds in-flight work by grepping PR bodies for
`(fixes|closes|resolves) #N`, and that is the only signal it has. A PR with no
reference is invisible to every duplicate-work check in this estate — which is
how a lane was dispatched to #99 while #100 was already open, #100 having
omitted the keyword because it implements only half of that issue.

So there are two failure directions, not one:

- a keyword that should not be there closes an issue nothing has solved
- a missing keyword hides live work and gets a second lane dispatched onto it

Both are silent. Check the list against what you intend; do not reach for
either extreme by reflex.

An issue closed by accident is a louder false signal than almost anything else
this loop produces: the tracker then says a problem is solved, and the next
session believes it.

## "This branch would revert X" — check with three-dot, not two-dot

`git diff main..branch` compares the two **tips**. Every commit on `main` the
branch does not have appears as a **deletion**, because the branch's tree does
not contain it. That is a property of comparing tips. It is not a prediction
about merging.

A merge — including a squash merge — applies the **three-dot** diff: the
branch's changes since the merge base. Everything else on `main` is left alone.

Reproduced from scratch on 2026-08-11, after a PR was held on the two-dot
reading:

```
$ git diff --stat master..feature     # two-dot
  feature.txt  | 1 +
  mainfile.txt | 1 -                  <- looks exactly like a revert

$ git diff --stat master...feature    # three-dot -- what a merge applies
  feature.txt | 1 +

$ git merge feature        -> mainfile.txt PRESENT   (not reverted)
$ git merge --squash ...   -> mainfile.txt PRESENT
```

Applied as written, the two-dot reading holds **every** branch that is behind
`main` with a revert warning that is not real.

*Reasoning, not measurement, and flagged as such:* the cost of a
usually-wrong warning is that readers learn to skim past it. That is an argument,
not something counted here. The frequency claim beside it originally read "in
this repo tonight, most of them" — measured afterwards and **false**: of four
open PRs, one was behind `main`. Stated wrong first, corrected by counting.

**Also not a revert: "the squash moved the merge base."** It does not. A squash
merge writes one new commit on `main` whose parent is `main`'s previous tip; the
branch's own merge base is unaffected, and a later branch forked before that
squash still merges its own changes and nothing else. This was part of the
original false alarm's reasoning and the tip-versus-merge-base correction above
does not address it on its own.

Being behind is not sufficient to revert anything. It takes a real content
conflict — the branch editing lines a newer commit changed, a rebase onto an
older base, or a delete-versus-modify. All of those appear in a three-dot diff or
as a merge conflict.

When it matters, do the decisive thing instead of reading a diff: merge into a
scratch worktree and look. That is what found the genuine #96/#97 semantic
conflict, which git reported as a clean merge.

Do not do this by hand, and do not rely on having read this section:
`scripts/supervisor/would-revert.sh <branch> [base]` does it for you and
reports deletions, conflicts, and other changes as three distinct things.
Run it before writing a revert hold; exit 0 means the merge deletes nothing
(agent-dotfiles#114 -- the same two-dot misreading held PR #111 an hour
after this section was written, because a rule in a document only works if
the reader happens to reach it first).

### Which diff answers which question

For "would merging this branch delete something", use **three-dot**
(`main...branch`) — see above. For "does `main` already contain this branch's
work", **neither plain form is correct**:

- `main...branch` — after a squash merge, `main` holds that content under a
  different commit, so this still lists it. Reports finished work as outstanding.
- `main..branch` — once `main` drifts, this lists `main`'s own newer files as
  "deletions". Reports unrelated work as the branch's.

What works is two-dot **scoped to the paths the branch touched**, with the
pathspec passed through `xargs -0` so filenames cannot word-split:

```bash
mb=$(git merge-base main "$b")
git diff --name-only -z "$mb" "$b" | xargs -0 git diff --stat main.."$b" --
# empty => the branch's work is on main
```

**Do not write that as `-- $paths`.** Unquoted, it is broken in both shells and
both failures say "already merged" — the direction that gets unmerged work
deleted. In bash a path containing a space splits into two pathspecs that match
nothing; in zsh, which does not word-split, a multi-file branch becomes one
newline-joined pathspec that matches nothing. Verified in both shells.

Verified by construction: superseded branch with `main` drifted → empty; branch
that only deletes a file → still reported; rename, add-on-both-sides, binary,
and a path containing a space → all correct.

This question got four wrong answers in one night, three of them written by
whoever was also writing this section. Check it rather than adopt it.

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

**Rename the window on every dispatch and on every completion.**

- **Dispatching:** `dispatch.sh` does this itself — `<repo><issue>-<short-slug>`,
  e.g. `skills140-loop-contract`. Nothing to remember here.
- **Finished and available:** rename back to `free-<n>`. A lane called `free-3`
  is unambiguous; a lane still carrying a task name is still working on it.
  **Never leave a completed lane named after its finished task.**

### Completion is `lane-done.sh`, not a thing to remember (#102)

Renaming on dispatch was mechanical from the start (`dispatch.sh`); renaming
on completion was not — it stayed a sentence in this file for anyone to
forget, and everyone did. `lanes.sh --free` returning nothing then reads as
"the estate is at capacity" when every lane sitting idle has actually
finished and is simply still wearing its task name. Observed twice in one
evening on 2026-08-11: five lanes idle and unrenamed, then two more half an
hour later, `dispatch.sh` refusing #99 the whole time.

The tempting fix — rename any lane `lanes.sh` reports idle — is wrong and
dangerous: idle also means "between tool calls" and "blocked on an approval
prompt holding an unposted verdict", and those look identical to `lanes.sh`.
Reclaiming on idle alone was tried against a live lane the same night and
nearly destroyed a verdict.

So the rename is tied to the one signal that cannot fire early: the worker's
own `tmux wait-for -S <channel>`, its brief's literal last action (§14.1).
Immediately after a successful `dispatch.sh`, background a waiter for the
same channel named in that brief:

```bash
scripts/supervisor/dispatch.sh 102 lane-rename-on-completion \
  ~/.local/state/agent-dotfiles-supervisor/ad102-brief.md \
  jonhill90/agent-dotfiles ~/source/repos/Personal/agent-dotfiles
# brief.md ends with: Final shell action: tmux wait-for -S ad102-done

scripts/supervisor/lane-done.sh <window-index> ad102-lane-rename-on-completion ad102-done
# run this with the Bash tool's run_in_background:true so the tick is not
# blocked while it waits
```

`lane-done.sh` blocks on bare `wait-for` — the counterpart of the worker's
`-S`, and **not** `wait-for -L`, which is the unrelated lock primitive and
returns immediately on a channel nobody has locked (#108) — and renames to
`free-<n>` only when it returns, and only if the window still carries the
exact name it was dispatched with. If it doesn't, someone already handled it,
or the lane was redispatched while the waiter was still up, and renaming now
would steal the new name. Needs no pane inspection, so it cannot mistake an
approval prompt for completion. It has the same open limit `wait-for` itself
has (SPEC §14.2 L3 — no timeout): a worker that crashes or wedges before its
final action leaves the waiter blocked forever, same as today.

Channel names are a flat global namespace on the tmux server with no enforced
uniqueness — any `-S` on the same string releases the waiter, whoever sent it.
Tie the channel to the issue number, as every example here does.

The window list then answers "what is going on" at a glance: anything named
`free-N` is available, anything else is in flight and says what it is doing.
