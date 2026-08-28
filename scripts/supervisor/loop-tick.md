# Supervisor tick

One iteration of the supervisor loop. If your context was lost,
read `brief.md` in this directory first — it is the full standing context.

**Corrections, `Verified 2026-08-16`, added by the docs sweep rather than
rewriting what they correct:**

- **"In this directory" is wrong.** `brief.md` is not tracked in this repo
  (`git ls-files scripts/supervisor/brief.md` — nothing) and is not read from
  beside this file. It is a state-directory artifact,
  `~/.local/state/agent-dotfiles-supervisor/brief.md`, as `README.md`'s own
  `recycle.py` section confirms by grepping that exact path. Read it
  from there.
- **This file predates the 2026-08-13 extraction into `agent-supervisor`**
  (its own last content edit is `#114`, before the split) **and was not
  updated for it.** Every worked `dispatch.sh`/`claim.sh`/`worktree.sh`
  example below points at `~/source/repos/Personal/agent-dotfiles` and
  `jonhill90/agent-dotfiles` as the repo being operated on — accurate as one
  of the five swept repos, but the scripts those examples invoke now live in
  *this* repo (`agent-estate`, renamed from `agent-supervisor` — #728/#729),
  not in `agent-dotfiles`. Substitute this repo's own path when dispatching
  work in this repo; do not copy an example's repo argument verbatim without
  checking which repo the work is actually in.
- Neither correction changes what to *do* — only where to look and which repo
  an example's literal argument names. Per this project's "correct rather
  than delete" rule, the examples below are left as written.

## Before anything else: `cd` into the pinned tooling clone, not your own cwd

`agent-supervisor#654`. Every `scripts/supervisor/...` command in this file
is written as a path relative to your own shell. If your shell's cwd is the
shared, interactively-used checkout (`~/source/repos/Personal/agent-estate`),
that is exactly the gap #654 measured directly: `dispatch.sh` merged a fix
(#650) that a real `--reviews-pr` dispatch from that checkout never saw,
because a merge changes `origin/main`, not a working tree nobody advanced.
Three separate merged fixes were inert against the running loop the same
day, discovered only by a human diffing the running file against
`origin/main` by hand.

Run this once, at the start of every tick, before any other command below:

```bash
cd ~/.local/state/agent-dotfiles-supervisor/live
```

(`$SUPERVISOR_LIVE`, if set, overrides that default — same variable
`advance-live.sh` itself reads.) This is the SAME pinned worktree #99/#130
already keep current for the watchdog LaunchAgent — nothing but
`advance-live.sh`'s own update step is supposed to write to it, and every
example below now resolves against it once you have `cd`'d in, with no
further changes needed to the commands themselves. Never run
`scripts/supervisor/*` from `~/source/repos/Personal/agent-estate`
directly — that checkout is explicitly for a human or a lane to work in
interactively (`#73`), and its state is exactly what this loop's own tooling
must never depend on.

If the `cd` fails, the clone is missing or broken — run
`scripts/supervisor/advance-live.sh` once, from a known-good checkout, to
(re)create it (its own "live/ was deleted" recovery path, #367), then retry
this tick. Do not fall back to running tooling from your own cwd; that
silently reintroduces the exact gap this section exists to close.

Sanity-check it is actually current before trusting it, especially after a
long gap since the last tick:

```bash
scripts/supervisor/tooling-drift.sh
```

Everything reporting `in sync` means the clone matches `origin/main` for
every file it holds under `scripts/supervisor/`. Anything else — `behind by
N commit(s)` or `diverged` — means `advance-live.sh`'s own update step is
not doing its job; report that rather than proceeding on tooling you cannot
trust matches what was actually reviewed and merged.

## Before the quota gate: hold the supervisor lease, or stop entirely

agent-dotfiles#238. Nothing below this line establishes WHO the supervisor
is — it was never a recorded fact, only an inference from which tmux window
a process happened to occupy, and on 2026-08-12 a second, fully legitimate
instance resumed elsewhere, inherited this whole file's context, and
dispatched the same five issues a first instance had claimed seconds
earlier. `claim.sh`'s per-issue claim did not catch it (both instances
share one GitHub identity), and nothing checked the ROLE itself.

**agent-supervisor#671: not `$$`.** `$$` was believed to be "this process's
own pid, stable for the life of this conversation" — measured false. Every
`Bash` tool call in this harness runs in its own short-lived subprocess: two
consecutive calls in the same turn print two different pids, and the first is
already gone (`ps -p` exits 1, checked directly, never through a pipe) by the
time the second one runs. A lease taken with `$$` is therefore stale before
the tick that took it even finishes — not because the loop crashed or exited,
but because the pid recorded was never anything but one tool call's own
process. That is what produced agent-dotfiles#238's stale-lease symptom every
tick: the NEXT tick's `take-supervisor-lease` finds the previous tick's owner
provably dead, reaps it, and re-takes it under a pid that will itself be dead
one tool call later.

Use `$TMUX_PANE`'s own process instead — the same anchor
`register-lane-self.sh` uses for the identical reason (invariant 10: a fact
read from the pane's own environment, never inferred or guessed), and a pid
that lives for the life of the pane, not one tool call:

```bash
OWNER_PID="$(tmux display-message -p -t "$TMUX_PANE" '#{pane_pid}' 2>/dev/null)"
python3 scripts/supervisor/cli.py take-supervisor-lease --owner-pid "${OWNER_PID:-$$}"
```

`$TMUX_PANE` is exported by tmux into every process it starts in a pane, so it
is set here without asking tmux which window is focused (the exact mistake
#187 made). The `${OWNER_PID:-$$}` fallback is only for a tick run outside
tmux entirely (a test, a hand-invocation) — expect it to behave exactly like
the old, broken default in that case, not as a fix for it.

- **`"leased":true`** — you hold it (freshly, or still, if this is a later
  tick re-affirming the same pid). Proceed to the quota gate.
- **`"leased":false`** — you are NOT the supervisor. `"holder"` names who is.
  Before standing down, check whether that holder is actually dead — a
  legitimate restart must still work, and a lease that outlives a crashed
  supervisor with no way to reclaim it would be strictly worse than two
  dispatchers:

  ```bash
  python3 scripts/supervisor/cli.py reap-supervisor-lease
  ```

  If this reports a reaped record (the holder's pid was provably gone on
  this host), retry `take-supervisor-lease` once — it should now succeed.
  If `reap-supervisor-lease` reports nothing reaped (`"reaped":null`), the
  holder is alive: **stop.** Do not run the quota gate, do not read the
  Director's inbox, do not dispatch anything. Notify Jon (`notify.sh`) that
  a second instance detected a live supervisor and is standing down, the
  same way agent-dotfiles#238's own incident was handled, and end this tick.

Every `dispatch.sh` call also re-checks this lease against `$PPID` (its own
invoking process) before dispatching anything — see that script's step 0.2 —
so a lease taken here is enforced again at the point of dispatch, not just
trusted for the rest of the tick.

## Every tick begins with the quota gate

```bash
bash scripts/supervisor/quota.sh check
```

Before the live worktree, before the Director's inbox, before anything else
in this file — nothing downstream is worth reading if the estate cannot see
its own quota state. Four exit codes, and only these four are ever read as
anything but UNKNOWN:

- `0` **SAFE** — proceed with the rest of the tick.
- `1` **WIND DOWN** — stop dispatching, tell every in-flight lane to push and
  release, go quiet. This is a *legitimate stop*, not a failure — see "Exit 1
  is covered, not blocked" immediately below.
- `2` **UNAVAILABLE** — quota could not be read. Treat as unknown, never as
  safe.
- `3` **MISSING** — `codexbar` is not installed. Same treatment as 2.

Any other exit code is UNKNOWN and fails closed exactly like 2 and 3 —
**including 127**, bash's own "No such file or directory" for a gate that is
missing or not executable. Do not write `if [ rc -eq 1 ]` or any check that
treats "not 1" as "proceed": that reads a missing gate as permission to
spend, which is what actually happened before agent-supervisor#227 got
`quota.sh` committed to `main` and deployed into `live/` — the gate was
untracked, a tick following this instruction literally got exit 127, and
nothing at the time said that code was anything other than safe. Enumerate
the codes you accept; refuse everything else.

### Exit 1 is covered, not blocked — do not re-arm past it

`scripts/supervisor/quota-watch.sh` runs OUTSIDE this estate (its own
detached process, started with `nohup`) and watches for the session window's
quota state to flip back to SAFE. The moment it does, it sends exactly one
resume message. A quota wind-down is therefore not a silent stop — something
external is already holding the reason to restart it, which is exactly what
"Never stop the loop" below is about *not* having. Stop dispatching, tell
every in-flight lane to push and release, then go quiet **without**
scheduling a wakeup of your own; `quota-watch.sh` is the wakeup. Re-arming
into an exhausted window anyway is what burned $80 of usage credits down to
$8 — the whole reason this gate exists.

This is a different case from "blocked on everything else is gated on Jon"
below, which has no external watcher and must always re-arm itself. Which
mechanism applies depends on WHY the tick is stopping: quota exhaustion is
watched from outside; a Jon-gated backlog is not.

**agent-supervisor#260: the SAFE → WINDDOWN half is watched from outside
too, now.** Until this, the wake-up (WINDDOWN → SAFE, above) was automatic
but the stand-down was not — `quota-watch.sh` logged the SAFE → WINDDOWN
transition and did nothing else, so it still depended on a human, or on a
tick happening to run this gate at the right moment. If a tick was slow,
crashed, or mid-task when the window closed, nothing stopped the spend.
`quota-watch.sh` now sends exactly one stand-down message on that
transition — finish the step in progress, commit, push, post one comment
saying what is done and the next action, then stop — the same thing this
section already told a tick to do by hand. You do not need to race it: if
your own `quota.sh check` returns 1 first, follow the instructions above as
before; if `quota-watch.sh`'s message arrives first, that already covers it.
Either way, dispatch.sh's own quota gate (#227) is what actually stops new
work — it re-checks quota on every call and refuses independent of anything
`quota-watch.sh` does.

## Advance the live worktree, before anything else

```bash
scripts/supervisor/advance-live.sh
```

The watchdog LaunchAgent runs from a pinned worktree
(`~/.local/state/agent-dotfiles-supervisor/live`) that nothing else updates
(#99). `watchdog.status`'s `code:` line reports how far behind it is; this
command is what acts on that report. It only advances when the candidate
commit's own `watchdog.sh` runs and writes a well-formed status from a
throwaway worktree, and only in the window right after the live watchdog's
last tick — outside that gate it exits 0 having done nothing, which is
correct, not a failure. Never run it against any worktree other than the
default live one, and never touch `~/.local/state/agent-dotfiles-supervisor/live`
by hand outside this command.

`watchdog.sh` now runs the same command itself on the way out of every tick
(#130), so this step is usually a no-op that reports `current`. Keep it: the
watchdog only advances when it is running from the pinned live worktree, and
this is the path that still works when it is not. If it reports anything
other than `current` or `advanced`, read `watchdog.status`'s `advance:` line
before doing anything else — the guard is running code you did not merge.

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

Sweep GitHub across the five repos — `agent-dotfiles`, `agent-supervisor`,
`skills`, `skills-private`, `agent-evals` — for **actionable** work:

- open issues **not** gated on a Jon decision,
- PRs needing independent review or a merge,
- failing CI.

**If there is actionable work:** claim it (see "Claim the issue before you
dispatch it"), then dispatch it to a free lane with a fresh bounded brief and a
**backgrounded** `tmux wait-for` waiter, so lanes run concurrently rather than
serially. Review, then merge what passes. You have full autonomy to merge and
close in these five repos.

Branch protection and rulesets are unavailable on these private repos without
GitHub Pro (agent-supervisor#13, measured: both 403 "Upgrade to GitHub Pro or
make this repository public"), so `gh pr merge` alone enforces nothing about
the PR's checks. Merge through `merge-pr.sh <repo> <number> [gh pr merge
args...]`, never `gh pr merge` directly -- it runs `ci_gate.py`, which
re-fetches the PR's head SHA live and refuses unless every check for that
exact SHA is green (an absent check refuses too, it is not treated as
pending). This is enforcement in the actor, not the platform: it is still
convention that a lane or the loop uses this script rather than calling `gh
pr merge` by hand.

`/clear` a lane before reusing it for an independent review — an author
reviewing their own PR is not an independent reviewer. Pass `--reviews-pr
<PR>` to `dispatch.sh` for a review dispatch (agent-dotfiles#212): it refuses
to send that review to the lane whose ledger record shows it authored the
PR's branch, and fails closed if authorship cannot be determined at all —
never rely on remembering which lane wrote what by hand. Still pass it
explicitly: agent-supervisor#70 added a best-effort fallback that infers the
flag from a "review PR #<N>" line in the issue title or brief when it is
forgotten, but that is a safety net, not a substitute for saying it.

That inference reads prose, so an ordinary dispatch whose brief merely talks
about a PR ("rebase it so PR #93 can be reviewed") is read as a review of it
and can be refused on authorship grounds. Pass `--not-a-review`
(agent-supervisor#101) for that dispatch — say it at the dispatch, never by
rewording the brief until the tool stops matching it. The two flags are
mutually exclusive; passing both is refused.

### A review or fix-pass brief must tell the lane to post through `post-verdict.sh`

agent-supervisor#187/#412: the false refusals #187 measured were never a
committed script calling `gh pr comment` directly — they were a reviewing
agent hand-typing the `Review-Lane:`/`Verdict:` trailers exactly as its own
brief told it to, via raw `gh pr comment <N> --body "..."` or `-F
body=@<file>`. `post-verdict.sh` (agent-supervisor#188) validates that
trailer pair against the ledger and refuses the two shapes #187 measured
before anything is posted, but it only helps if brief text actually tells
the lane to run it. When a brief's dispatch is a review (`--reviews-pr`) or
a fix pass replying with a verdict, its closing instruction must read:

```bash
printf '%s\n' "$BODY" | scripts/supervisor/post-verdict.sh <repo> <N>
```

never a raw `gh pr comment`/`gh issue comment` invocation. `gh-comment-gate.sh`
only greps this repo's own committed `*.sh` files for the raw form — by its
own docstring it cannot reach brief text, which is generated per-dispatch,
not committed here. This is the one place in this repo's own docs that
generates that text, so it is the one place drifting back to raw `gh pr
comment` would matter.

### A review or fix-pass brief never asks the lane to derive `Review-Lane:` — dispatch already states it

agent-supervisor#685 first fixed this by pointing every brief at
`lane-whoami.sh` instead of a bare `tmux display-message`. That closed the
`claude-print` gap but not the whole defect: `skills#289` and `skills#291`
still stamped the supervisor's own window AFTER #685 landed, because a hand-
written brief outside this repo's own generated text told the lane to
derive its id, and asking is exactly the step that fails — a lane in a
non-active tmux window asking `display-message` (bare, or even the `-t
"$TMUX_PANE"` form typed by hand instead of run through `lane-whoami.sh`)
can still get this wrong. agent-estate#793: `dispatch.sh`,
`dispatch-claude-print.sh` and `dispatch-pi-rpc.sh` already resolved `$LANE`
before ever writing the brief, so the deliverable contract they append now
STATES it —

```
**Your lane id is `<session>:<index>`.** Use this exact value for any
`Review-Lane:` or `Lane:` trailer this brief asks you to write.
```

— and a brief's own closing instruction should say the same: point at the
stated value in the deliverable contract, not at a command to run. A lane
that is *told* its id cannot mis-derive one. `lane-whoami.sh` is not
removed — it is the fallback for a brief written by hand, outside
`dispatch.sh`'s own contract, or predating it — but it is no longer the
first thing a generated brief's closing instruction should name. As with
the `post-verdict.sh` rule above, this is the one place in this repo's own
docs that generates that text, so it is the one place drifting back to
"go derive it yourself" would matter.

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

**Do not stop the loop for this reason.** Do not call `ScheduleWakeup` with
`stop: true` because everything is gated on Jon, and do not treat "everything
is gated on Jon" as a terminating condition. Nothing outside this loop is
watching for Jon to unblock something — unlike the quota gate's exit 1
(above), which `quota-watch.sh` watches for from outside. A tick stopped here
requires Jon to come back and re-arm it by hand, which is the thing this
loop exists to avoid. Blocked-on-Jon is a *state to sleep through*, not a
reason to exit.

This is a correction to an earlier version of this file, which said "schedule a
long wakeup" without forbidding `stop`. On 2026-08-10 the supervisor read that
as permission to stop, reasoning that further ticks could not make progress. The
reasoning was sound and the action was wrong: it ended the loop, and Jon's next
observation was "its not looping."

**Second correction, 2026-08-16 (agent-supervisor#227/#229).** The line above
used to read "**Never stop the loop... Only Jon ends the loop**," stated as an
absolute with no exception. It was wrong the moment the quota gate landed:
that rule, read literally, forbids the wind-down `quota.sh check`'s exit 1
requires, and a supervisor that "helpfully" re-armed past it anyway is what
burned $80 of usage credits down to $8. The rule now has exactly one carve-out,
and it is the only one that gets to exist, because it is the only stop with an
external party already holding the resume: quota exhaustion, covered by
`quota-watch.sh` (see "Every tick begins with the quota gate" at the top of
this file). Every other reason to stop — including "everything is gated on
Jon," and including a blocked tick — still has nothing watching for it and
must still re-arm itself. Both halves of this have now actually happened: the
2026-08-10 silent stop above, and separately a supervisor that re-armed into
an already-exhausted window on 2026-08-15/16 and spent through it. Neither
rule was wrong on its own; the estate needed both, scoped to the case each
one actually covers.

A blocked tick must be **cheap**, which is what makes an hourly sweep
affordable: list open issues and PRs across the five repos, compare against the
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
- Commits: Jon Hill \<jonhill90@users.noreply.github.com\> sole author, no co-author trailers.

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

It picks a free lane — idle **and** unowned per the ledger, both required
(agent-dotfiles#174) — claims the issue, **creates the worktree**, renames the
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

The table prints window **indices**, which is what you read off the tmux
window list, and that has not changed. `--free` prints two tab-separated
columns per lane: the lane (`agent-dotfiles:5`, the identity the ledger and
every recovery command key on) and its tmux target (`agent-dotfiles:@12`, the
window id anything addressing a pane must use). See #241 for why the two are
not interchangeable.

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
- `service` — a supervisor service that lives in this session and is not a
  lane, such as the Telegram poller (`inbox-poll.sh`) when it is still
  tmux-hosted (see README.md — as of #154 the poller is normally a
  launchd/systemd-managed service outside tmux entirely, and this window
  shape only appears for a legacy poller not yet migrated). Its command
  is a shell because the service is a shell script, so it read `dead` until
  #154 and the restart instruction above pointed straight at it — restarting it
  replaces the poller with an agent and Jon's replies stop arriving silently.
  **Leave it alone.** It is never offered by `--free`.
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

The claim is the GitHub **assignee**. In these five repos an assignee means
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

`dispatch.sh` resolves its lane from `lanes.sh --free` **intersected with what
the ledger says is unowned** (agent-dotfiles#174, superseding the `free-N`
naming rule below as the authority — the name still matters for a lane the
ledger has never registered, see that section), and refuses an empty target,
so a dispatch made through it cannot hit the supervisor. It takes no lane from
the environment: `DISPATCH_LANE` was an override that skipped every one of
those checks, and `DISPATCH_LANE=t:1` reproduced this same incident at exit 0,
so it was removed (#89). To aim a dispatch at a specific lane the ledger has
never seen, rename that lane `free-N` first; a lane the ledger already knows
is occupied has to be released in the ledger (its worker finishing normally
through `lane-done.sh`, or `cli.py record-completion` by hand) — renaming it
no longer has any effect on whether it is offered.
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

scripts/supervisor/lane-done.sh <window-id> ad102-lane-rename-on-completion ad102-done
# run this with the Bash tool's run_in_background:true so the tick is not
# blocked while it waits
```

**Pass the window ID (`@12`), not the index (#241.)** `dispatch.sh` prints it
on success as `target:`, and `lanes.sh --free` emits it as its second column.
This server runs `renumber-windows on`, so closing any window shifts every
higher index down by one — and this waiter holds its target for as long as the
work takes, which is the longest such hold anywhere in the estate. An index
given here can name a different window by the time the channel fires; the
name-match guard then correctly refuses the stranger, and a lane that
genuinely finished is never renamed and never released. That is #102's shape
reached through the mechanism built to prevent it. An index still works and is
fine to type by hand off the window list for an immediate one-off; a script
should never pass one.

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
`free-N` is USUALLY available, anything else is USUALLY in flight and says
what it is doing. It is a human-readable label now, not the authority (agent-
dotfiles#174) — `dispatch.sh` decides from the ledger, so a stale `free-N`
left by a hand-rename or a lost completion signal is a cosmetic bug rather
than a lane the estate mistakenly hands work to.
