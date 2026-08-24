# Portable supervisor core

This directory holds the durable coordination core for tmux agent lanes,
originally built for and still used by Hill90. tmux persists interactive
Claude and Codex terminals; it is not the task database or normal result
transport.

This core is harness- and repo-agnostic; it moved here from
`jonhill90/Hill90` (`scripts/supervisor/` on branch
`feat/portable-supervisor-ledger`) so it is not blocked on that repository.
The launchd adapter that drives it for Hill90 specifically —
`com.hill90.supervisor.plist`, `service.sh`, `install.sh`, and the
`hill90-supervisor` entry-point shim — stayed behind in Hill90 and is not
part of this directory.

## The poller is a service now, not a tmux window (agent-supervisor#154)

`inbox-poll.sh` is the portable core; how it runs follows the same split as
`watchdog.sh` above — a LaunchAgent (or systemd user unit) belongs to the
machine-specific adapter repository, not here. Nearly every poller incident
measured for #154 traced to hosting it in a tmux window instead: four
pollers acking the same Telegram offset from duplicate **windows**, a
restart loop tied to a **pane** relaunch, a dead **pane** leaving the
channel silently down, and a cleanup that killed windows **by index** and
took the poller with it. `watchdog.sh`'s own LaunchAgent is the
counter-example already in this repo: it survives `tmux kill-server` because
there is no window for the server's death to reach.

`inbox-poll.sh` itself makes no tmux call at all — grep it. It now also
refuses to become a second running instance (a self-held, reclaimable lock,
`INBOX_POLL_LOCK`, default `$STATE/.inbox-poll.lock`), so installing a
LaunchAgent while an old tmux-hosted poller is still running does not double
the number of live consumers: the new instance finds the lock held and
refuses to start, exactly like starting it twice by hand. **Migration is by
attrition, not cutover** — the tmux window keeps carrying Jon's messages
until it is retired by hand (or dies and nothing respawns it); only then
does the service's own restart policy pick the lock up. At every moment
there is at most one lock holder and so at most one consumer of the
Telegram offset — the correctness property `inbox.sh`'s own offset lock
(see its header) already gives independently, this is belt-and-suspenders
so a raced start fails loudly instead of relying on that deeper net.

`tests/supervisor/test_inbox_poll_service.sh` drives the real script through
a real, throwaway LaunchAgent (same posture as
`test_watchdog_launchd_relaunch.sh`, #75) and proves: a second instance is
refused while the lock is held; `tmux kill-server` on an isolated socket
leaves the poller acking, same pid, heartbeat still advancing (the mutation
check that is the entire justification for #154); and a `kill -9`'d poller
is relaunched by `KeepAlive` with a new pid, having routed every message
exactly once across the crash — no double-ack, nothing dropped.

**What becomes dead code once every poller is service-hosted and no window
runs it, in the 16 files that currently reference `inbox-poll` (not removed
in this PR — `lanes.sh` is under active change elsewhere, see its own
header — but named here so the cleanup is a known follow-up, not a
discovery):**

- `poller-window.sh` — the whole file is a tmux window lookup. Dead.
- `poller-recover.sh` — exists entirely to recreate/respawn a window. Dead,
  once nothing depends on the window path during attrition.
- `lanes.sh` — the `service` state, `SERVICE_RE`, and the
  `is_poller_window_name` branch that promotes a dead pane to `service`.
  Dead, but **not removed here**: #149 adds `never-busy` to this same file
  and agent-tui#19 covers a guard that cannot see dynamic assignments —
  removing a state now would collide with both.
- `advance-live.sh` — `find_poller_pane` and `prompt_poller_relaunch`
  (queuing `poller-recover.sh`'s tmux respawn). Dead. The sha-comparison and
  restart-flag write around them **stay** — #154 fixed those to no longer
  require a window, which is what lets a service-hosted poller keep getting
  version-triggered restarts at all.
- `digest.sh` — the `poller_alive` liveness check is scoped to the pane pid
  of a window (#96). This one does not just go dead, it goes **wrong**: once
  nothing runs a window, this reads "not alive" forever, a permanent false
  alarm layered on top of the 278 measured for #154. Flagged here as a
  correctness follow-up, not a cosmetic one; `inbox-poll.status`'s own
  `state`/`checked` fields (read separately, a few lines below) already
  give a host-agnostic answer and do not need this fix to keep working.
- `loop-tick.md` — the "window 11" prose reference. Stale once true; a doc
  fix, not a behavior change.

**What stays, unchanged, because it is already host-agnostic:** `inbox.sh`,
`inbox-route.sh`, `director-route.sh`, `director-inbox.sh` (message
plumbing, no tmux dependency of its own), `watchdog.sh`'s and
`watchdog_notify.py`'s inbox-poll **heartbeat staleness** check (#163) —
reads `inbox-poll.status` directly, which keeps meaning the same thing
whoever launched the process — and the handful of comments in `core.py` and
`dispatch.sh` that merely cite `inbox-poll.sh` as signal-handling precedent.

## Contract

- GitHub Issues and pull requests are the canonical task ID, source, status,
  and evidence records. SQLite under `~/.local/state/hill90-supervisor` is a
  reconstructable delivery spool (mode `0700`), never the only completion
  record.
- A logical lane is bound to a physical tmux pane incarnation with a random
  nonce, tmux server/session identity, harness, command, and repository path.
- A lane has at most one nonterminal task. The prompt contains the stable task
  ID and the commands that accept and complete that task.
- Delivery is ambiguous the instant a send is attempted, not just when it
  fails: `assign` persists `delivery_pending` before the physical tmux send,
  and that task id cannot be automatically resent while it stays in that
  state, whether the send raised or the ledger's own post-send write failed.
  Nothing infers delivery from echoed pane text. A human resolves the
  ambiguity with `hill90-supervisor reconcile --task <id> --outcome
  {delivered,failed}` after inspecting the pane directly; `reconcile` is
  deliberately not caller-verified because the pane it is reconciling may be
  the very thing that is stuck.
- Completion results are immutable, limited to 64 KiB, hashed, and published
  with a deterministic `completion:<task-id>` event in the same database
  transaction as the terminal task transition.
- `assign` requires a reconstructed, open GitHub source record for the task
  id; a task with no such record, or whose source is closed or already past
  `created`, is refused before anything is sent to a pane.
- `complete` requires the task's own recorded `pane_nonce`; a lane
  incarnation cannot complete a task it does not own. Re-registering a lane
  is refused while it has an outstanding task in any status other than
  `delivery_pending` — that status alone has its own reconciliation path
  keyed off the task's own `pane_nonce`, independent of the lane's current
  one.
- An outstanding delivered task observed idle, blocked, awaiting approval, or
  in an unrecognized pane state produces a persistent `attention:<task-id>`
  (idle) or `attention:<task-id>:<reason>` (blocked/approval/unknown) event.
  It cannot be acknowledged until the task is completed, failed, or
  cancelled, and notified events retry after their deadline.
- Supervisor notifications contain event IDs and result paths—not tmux
  scrollback or broad repository snapshots—and are marked notified only after
  the supervisor harness is genuinely active.
- Codex and Claude use different terminal classifiers but the same ledger
  schema and lifecycle.
- GitHub is the canonical external sensor. A GitHub timeout or failure leaves
  its previous baseline intact, is reported in tick output, and gates lane
  observations and notifications until a successful collection reconciles it.
- Each Git, GitHub, and tmux subprocess has a bounded timeout. The LaunchAgent
  sets an explicit Homebrew-aware `PATH`, rather than relying on an interactive
  shell environment.

## Commands

The examples below use the `hill90-supervisor` entry-point shim, which is
Hill90's adapter and lives in that repository, not here. From this
directory, invoke the same subcommands directly against `cli.py`, e.g.
`python3 scripts/supervisor/cli.py register --lane ...`.

```bash
hill90-supervisor register --lane architecture --target %19 \
  --harness codex --repo ~/source/repos/Personal/Hill90

hill90-supervisor register --lane infra-claude --target %8 \
  --harness claude --repo ~/source/repos/Personal/Hill90

hill90-supervisor reconstruct \
  --source-url https://github.com/jonhill90/Hill90/issues/42 \
  --source-ref 0123456789abcdef0123456789abcdef01234567

hill90-supervisor assign --lane infra-worker --task gh.jonhill90.Hill90.issue.42 \
  --summary 'Review Issue 42 at its reconstructed immutable source ref.'

hill90-supervisor tick
hill90-supervisor status

# Only after inspecting a task stuck in delivery_pending directly:
hill90-supervisor reconcile --task gh.jonhill90.Hill90.issue.42 --outcome delivered
```

Workers run the task-bound `accept` and `complete` commands included in their
brief. Architecture acknowledges accepted events explicitly. The periodic tick
only observes registered lanes and delivers due ledger events.

## GitHub task records

An Issue or PR body declares exactly one source-bound task marker. It uses the
canonical filesystem-safe ID `gh.<owner>.<repo>.issue.<number>` or
`gh.<owner>.<repo>.pull.<number>`. Both the command and marker require the
canonical GitHub URL and a full immutable commit SHA; pull requests must still
point at that exact head SHA when read. Status changes are append-only GitHub
comments with a deterministic marker; terminal statuses require at least one
evidence value. The marker is compact, sorted JSON inside an HTML comment:

```text
<!-- hill90-supervisor:v1 {"kind":"task","source_ref":"<sha>","source_url":"<url>","task_id":"gh.owner.repo.issue.42"} -->
<!-- hill90-supervisor:v1 {"evidence":["<evidence-url>"],"kind":"status","source_ref":"<sha>","source_url":"<url>","status":"complete","task_id":"gh.owner.repo.issue.42"} -->
```

`reconstruct` reads these records through `gh` and idempotently restores the
local `source_tasks` spool, even into an empty state directory. It does not
invent a tmux pane or dispatch work.

## Claims

`claim.sh` makes "has this work already been taken" answerable from GitHub.
The contract above says Issues are the canonical task, **claim**, and status
record; task and status were true, claim was not, and on 2026-08-11 issue #28
was dispatched to two lanes independently and fixed twice (#68 merged, #69
closed). The claim is the GitHub assignee — a first-class field, visible in
the UI, and surviving the loss of the local spool, which the contract requires.

```bash
claim.sh list  <repo>                 # open issues with no claim -- what dispatch reads
claim.sh take  <n> <repo> <lane>      # exits non-zero if someone got there first
claim.sh check <n> <repo>
claim.sh release <n> <repo>
claim.sh stale <repo>                 # claims whose lane is gone
```

`loop-tick.md` requires the dispatch step to call it. Claims expire with the
lane, not on a clock: a task here runs for hours, so a useful timeout would
steal live work. `stale` reports a claim when no live lane window names the
issue — `dead` from `lanes.sh` is the case it catches — and no open PR says it
fixes it. It reports only; releasing stays a decision.

`--add-assignee` is add-to-a-set, not compare-and-swap, and every lane
authenticates as the same GitHub user, so two dispatchers reading "unassigned"
within the same second still both win. That is a sub-second window replacing a
multi-minute one; GitHub offers no CAS on issues.

## Worktree isolation

`worktree.sh` gives every dispatch its own git worktree, so lanes and the
Director stop sharing one working tree (agent-dotfiles#73). On 2026-08-11 a
lane working #28 had its branch switched out from under it mid-task by
another lane in the shared checkout: its uncommitted edits to four files were
discarded, and its staged deletion of a file was swept into an unrelated
lane's commit, which shipped without that deletion ever mentioned in the
commit message.

```bash
worktree.sh new <slug> [repo] [base]   # create a worktree, print its path
worktree.sh done <path>                # remove a worktree; refuses if dirty
worktree.sh guard <repo>               # exit 1 if <repo> itself is dirty
worktree.sh gc [--dry-run] [repo] [base]  # remove clean worktrees whose branch
                                          # content is already on [base]
```

`dispatch.sh` calls `new` and hands the printed path to the lane, rather than
a brief telling the lane to create its own worktree — a step in a brief is a
step that can be skipped. `done` and `guard` both refuse when the target has
uncommitted changes, matching `safe-deletion`: a worktree with uncommitted
changes is someone's unfinished work, not garbage. `guard` is for the
Director's own use of the shared checkout, which caused the same class of bug
this tool exists to prevent.

Nothing calls `new`'s counterpart: `lane-done.sh` renames the tmux window and
closes the ledger task but never removes the worktree it was handed
(agent-dotfiles#165). `gc` is the intended cleanup, but it is not
"`lane-done.sh` calls `done`" — a just-finished lane's branch is usually
pushed and in review, so removing its tree at completion time is early, and
`done` correctly refuses a dirty tree, which would make a lane that finished
with uncommitted work leak silently. `gc` instead sweeps every worktree in
`[repo]` and removes one only when `[base]` already contains the branch's
content *and* its tree is clean, reusing the same dirty/detached-HEAD guard
as `done`.

That predicate was tip-ancestry until agent-dotfiles#169. This repository
squash-merges, and a squash merge writes a new commit with no parent link to
the branch, so a fully merged branch is never an ancestor of `origin/main` —
`gc` refused those trees permanently, and one more with every merge. It now
asks the content question instead, in `loop-tick.md`'s formulation: a two-dot
`base..branch` diff scoped to the paths the branch touched (see "Which diff
answers which question"), with ancestry kept as the cheap yes.

Measured on the live checkout, 2026-08-11: 70 worktree-held branches, 26
ancestors (what `gc` reached before), and 12 more whose content is already on
`origin/main` — 38 reachable. The 32 it still refuses include 17 with a
MERGED PR whose files `main` has since changed again: merged, then superseded.
No local check can tell that apart from unmerged work, so `gc` leaves them.
Converging is not emptying.

`--dry-run` runs every check and prints what a real run would remove, without
removing anything — the way to confirm the list against the live estate before
trusting it. It is idempotent and safe to run repeatedly, but it is not wired
into `dispatch.sh`, `lane-done.sh`, or the Director tick — that is a separate
decision left to whoever owns the tick, not bundled into landing the tool
(see dispatch.sh's header on tools nothing calls).

## Advancing the live worktree

The watchdog LaunchAgent runs `watchdog.sh` from one pinned worktree,
`~/.local/state/agent-dotfiles-supervisor/live` — a git worktree of this
repository, sharing its object store and refs with every other worktree on
the machine, detached at whatever commit it was last checked out to. Nothing
in this repository advanced it (#99). `watchdog.status`'s `code:` line
(#100) reports how far behind that copy is; `advance-live.sh` is the step
that acts on the report:

```bash
advance-live.sh [live-worktree-path]   # default: $SUPERVISOR_STATE/live
```

Four design constraints — the first argued out on #99 and revised by #130,
the second on #130, the last two on #99 from measured toil (five
hand-advances in one day, each prompted only by a human noticing the
`code:` line). They are recorded here, not reargued:

- **The watchdog calls it, and so does the loop. Not a merge webhook, not a
  plain timer.** A merge-triggered deploy puts the decision in the same
  system that produced the change, which is what makes "merged does not mean
  running" a safety property here; a plain timer supplies no gate at all.
  `loop-tick.md` calls this as its first step, once per supervisor tick, and
  `watchdog.sh` calls it on the way out of every tick in which the copy
  running is the pinned live one — invoked rather than merely documented
  (the `acp_transport.py`/`worktree.sh`/`lane-done.sh` shape: a tool nothing
  calls is a documentation rule with a binary attached).

  The watchdog was originally excluded on the grounds that a broken watchdog
  auto-installing itself every 180s leaves nothing to notice the break.
  #130 reversed that: the objection is answered by the run-gate below — a
  candidate that cannot run is never installed — while leaving the loop as
  the only caller meant the live copy stopped advancing whenever the loop
  went down, including during a deliberate escalation, when it is down by
  design. The guard ran its stalest code during exactly the incidents it
  exists for.
- **During an escalation the code is frozen, deliberately.** Escalation is
  the one state where a human has already been paged, so staleness is
  bounded by someone who is coming and who can run this by hand. Against
  that bound, advancing costs more than it saves: the sha in the status file
  a human was paged with must still be the sha they find, a recent merge is
  a leading suspect for whatever made the loop die three times in an hour,
  and freezing is reversible by hand while a redeploy mid-diagnosis is not.
  `watchdog.status` says `advance: held` when this happens. The full
  argument, including the case for the opposite choice, is in `watchdog.sh`
  beside the code.
- **The candidate must demonstrably run before the pin moves.** CI green is
  a property of the merge commit, not proof this machine's copy works.
  Before switching `live`, `advance-live.sh` checks the candidate commit out
  into a throwaway worktree and runs its own `watchdog.sh` once, pointed at
  scratch state with a `SUPERVISOR_PANE` that cannot exist, and requires a
  well-formed status file back. That exercises the real entry point without
  any possibility of a tmux send-keys reaching the live loop.
- **Advance only in the window right after a live tick, never blind.**
  `watchdog.sh` writes `watchdog.status` on every tick, including a
  `checked:` timestamp. `advance-live.sh` reads that file — it does not
  touch or lock `watchdog.sh` itself — and refuses to advance once too much
  of the cadence between ticks has elapsed, so a checkout never lands mid-
  tick. Outside that window it exits 0 having done nothing; that is
  correct, not a failure, and the next supervisor tick tries again.

The pre-advance sha is written to `$SUPERVISOR_STATE/.live-rollback-sha`
before anything is mutated — it is only knowable then; after
`checkout --detach` you are guessing from reflog. Any refusal (unreadable
`origin/main`, a failed smoke test, a post-checkout HEAD that does not match
the target) exits non-zero with `live` left exactly where it was — no
silent revert, no half-state.

When the watchdog is the caller it adds one line to `watchdog.status` saying
what this tick did about the drift the `code:` line reports — `advanced`,
`current`, `not this tick` (a gate declined; ordinary), `refused` (with the
reason), or `held` (an escalation). The `code:` line is unchanged by any of
this and still describes the code the tick that wrote it actually ran.
A refusal never fails the tick: the watchdog still writes its status, still
restarts or pages, and still exits 0. Turning "the code is one commit stale"
into "the watchdog is down" would be a strictly worse outcome than the one
being reported.

### The watchdog's own absence must be loud (#24)

On 2026-08-13 the watchdog LaunchAgent itself was unloaded for 91 minutes
and nothing noticed — `watchdog.status`'s `checked:` line went stale and no
code ever compared it to the clock. It was found only because a human
happened to `cat` the file by hand. `advance-live.sh` already reads
`checked:` on every invocation for the race gate above, and it already runs
on every supervisor tick (`loop-tick.md`'s first step), so that is where the
mechanical check lives rather than a new script the tick has to remember to
call: if `checked:` is older than `ADVANCE_WATCHDOG_STALE_MULTIPLE` (default
3) times the tick interval, `advance-live.sh` exits non-zero naming the
staleness and the last-checked time, before it ever looks at whether there
is anything to advance — a dead watchdog with an already-current live copy
would otherwise sail through reporting nothing wrong.

## Dispatch

`dispatch.sh` is the caller. It performs one dispatch end to end — pick a free
lane, claim the issue, create the worktree, rename the window, send the brief:

```bash
dispatch.sh <issue> <slug> <brief-file> [repo] [repo-path]
```

### Two identities per window, and they are not interchangeable (#241)

A lane has a **lane id** and a **tmux target**, and the split runs through
`lanes.sh`, `dispatch.sh` and `lane-done.sh`:

| | example | what it is | who takes it |
|---|---|---|---|
| lane | `agent-dotfiles:5` | a SLOT number — the identity the ledger keys on, the one every recovery command in `dispatch.sh`'s refusal names, and the one Jon reads off the tmux window list | `cli.py lane-free --lane`, `record-dispatch --lane`, `cancel-open-task --lane` |
| target | `agent-dotfiles:@12` | tmux's own handle for the window, stable for its lifetime and never reused | every `send-keys`, `rename-window`, `capture-pane`, `display-message` |

This server runs `renumber-windows on`, so closing any window shifts every
higher **index** down by one. A target resolved before a close and used after
it addresses a different pane — measured in agent-dotfiles#241, and observed
on 2026-08-12 as three briefs landing in windows other than the ones
`dispatch.sh` reported. A window **id** cannot move.

The lane id deliberately stays index-based: it names a slot that must survive
a window being closed and recreated, which is exactly what destroys a window
id. So `lanes.sh --free` emits both, tab separated, and `cli.py lane_free`'s
existing `--lane`/`--target` split is where they part company.

Empty targets are refused, not defaulted: `send-keys -t session:` does not
error, it hits the ACTIVE window (usually the supervisor), and `session:@` is
empty the same way. `dispatch.sh` checks the target's shape positively and
skips any candidate it cannot place, so a `lanes.sh` that stopped emitting the
column dispatches nothing rather than guessing.

A lane is a candidate only if `lanes.sh` calls it `free` **and** the LEDGER
says it is unowned (agent-dotfiles#174). `lanes.sh` still answers "is an agent
there and not mid-turn" from pane content — that has not changed. "Is this
lane UNOWNED" used to be answered by the window name (`free-N`); a lane that
finished without being renamed, and one paused on an approval prompt, look
identical to an unowned one from pane content alone, and the name used to be
the only signal that survived that. Now the ledger is: `dispatch.sh` asks
`cli.py lane-free`, which answers from the `lanes`/`tasks` tables, not the
name. The window name still matters for exactly one thing — MIGRATION: a lane
the ledger has never heard of is registered as free the first time it is seen
named `free-N`, and never consulted by name again after that. A lane the
ledger already knows about, free or occupied, answers from the ledger alone
regardless of what it is currently named — a hand-renamed occupied lane is
still not offered, which is the inversion #174 exists to prove. An unreadable
ledger refuses every candidate rather than assume any of them are free. The
lane cannot be chosen from the environment; there is no override.

Reading a lane free is not the same as taking it (agent-dotfiles#184).
`lane-free` is a QUERY, and the gap between it and the first `send-keys` spans
the issue claim, the worktree build and the send — long enough for a second
dispatcher to walk straight into it. So a candidate that reads free is
immediately followed by `cli.py claim-lane`, an atomic write-then-verify: it
inserts a placeholder task under the `one_open_task_per_lane` unique index and
re-reads to confirm the row occupying the lane is its own. Two dispatchers
racing the same candidate are serialized by the ledger's `flock` plus
`BEGIN IMMEDIATE`; the loser is refused and moves to the next candidate.

**And a claim outlives the process that took it, so both halves of its
cleanup have to exist** (agent-dotfiles#209). `lane_available` counts that
placeholder as occupying the lane, which is the point — but it means a
dispatcher that dies holding one leaves the lane reading occupied with nothing
working it, which is agent-dotfiles#102's failure shape (capacity silently
falling to zero while lanes sit idle) arriving through the mechanism built to
prevent it. Two mechanisms cover it, and neither is sufficient alone:

- **A trap**, on `EXIT`/`TERM`/`INT`, releasing the claim on every exit the
  shell can observe — the same guard `advance-live.sh`, `would-revert.sh`,
  `watchdog.sh` and `inbox-poll.sh` already put on their own held resources.
- **A reap**, run by `dispatch.sh` itself before it picks a lane
  (`cli.py reap-lane-claims`), covering what no shell can trap: SIGKILL, an
  OOM kill, a host crash. It is **not a TTL**. A TTL short enough to be
  useful can expire on a slow-but-live dispatch and reopen the race above;
  elapsed time cannot tell a slow dispatcher from a dead one. Each claim
  records the owning process instead, and a claim is cleared only when that
  pid is provably gone **on this host**. Every ambiguity — a live pid, a
  recycled pid, another host, no recorded owner — leaves the claim alone, so
  this can only ever clear a claim nobody owns, never grant a lane
  (agent-dotfiles#124/#126's one-way ratchet).

**Both stop at the same line, and the line is the send.** A claim is
`created` while it is only a reservation, and `cli.py commit-lane-claim` moves
it to `delivered` — which `dispatch.sh` calls immediately *before* the
`send-keys Enter` that submits the brief. Neither the trap nor the reap will
touch a claim past that point, because from the instant a brief is in front of
a worker, freeing the lane is agent-dotfiles#102 caused by the cleanup rather
than prevented by it. It is a ledger fact rather than a flag in the dispatcher
precisely because the SIGKILL case is one where the dispatcher stops existing:
at that moment the placeholder is the only record the lane is occupied at all,
since `record-dispatch` has not run yet. A dead owner does not distinguish
"claim taken, nothing sent" from "claim taken, brief live in the pane"; this
status does.

That ordering is deliberately fail-closed and it has a price. Every failure
after the commit — a send that errors, or the agent-dotfiles#141 confirmation
concluding the brief never left the input box — now leaves the lane **held**
where it used to be freed. A lane wrongly held costs capacity and is recovered
by one command; a lane wrongly freed costs a running lane's work and is
recovered by nothing. The abort says so and prints the command, and `lanes.sh`
still reports such a lane `unsent`.

If a lane is still held after all that, `dispatch.sh`'s `no free lane` refusal
names the manual recoveries rather than leaving them to be reconstructed, and
**which one applies depends on the claim's status**, readable straight out of
`cli.py status` (whose task id is `ledger-claim:<lane>:<token>`):

- `created` — a reservation that never sent anything. Clear it with
  `cli.py release-lane-claim --lane <lane> --token <token>`.
- `delivered` — a claim with a live brief behind it. `release-lane-claim`
  deliberately will not touch this one; that is the guard working. If the pane
  really is idle, `cli.py cancel-open-task --lane <lane>`.
- a `ledger-hold:` row instead of a claim — a failed `record-dispatch`
  (agent-dotfiles#188) awaiting reconciliation, which the automatic reap also
  deliberately will not touch. Same `cancel-open-task --lane <lane>`.

Check the pane before running any of them: all of them make the lane
dispatchable again.

It exists because `worktree.sh` shipped (#79) with no automated caller
(agent-dotfiles#81): `grep -rn worktree.sh` found three code fences in
`loop-tick.md` and the section above, and nothing else. The tool fails closed
when it is called; nothing made it get called, so enforcement was still "the
dispatcher reads the file and runs the command" — the mechanism whose failure
produced #73. That is the third instance of one shape in this repository:
`acp_transport.py` (302 lines, tested, zero importers, #56), `claim.sh` (wired
into the dispatch step by #74, the one that got it right), `worktree.sh`
(#81). **A tool that fails closed when called, and that nothing calls, is a
documentation rule with a binary attached.**

Every step is a refusal point, and every refusal aborts the whole dispatch and
undoes what it already did — the claim is released, a created worktree is
removed. A failed `worktree.sh new` in particular is fatal rather than
degraded: a lane with no worktree works in the shared checkout, which is #73.
The window name follows the `<prefix><issue>-<slug>` convention `loop-tick.md`
requires, with the prefix derived from the repo name (`agent-dotfiles` → `ad`,
`skills` → `skills`).

**It checks that the brief landed before submitting it.** Running the first
version against a live tmux server, the lane's prompt came back reading
`/var/…/brief.md and do exactly what it says` — the leading `Read ` swallowed
while the harness repainted after `/clear`. A mangled brief is worse than a
missing one: the lane acts on it anyway. So the brief is typed, the pane is
read back, and only a pane showing both the head of the message and the
worktree path gets an `Enter`. One retype is attempted; if it still has not
landed, the dispatch aborts and rolls back rather than submitting whatever is
in the input. `DISPATCH_SETTLE` (**default 1s, not 2s as this line
previously said** — `dispatch.sh`'s own comment states "`--settle` defaults
to `$DISPATCH_SETTLE` itself (1s in production...)", and every call site
grepped (`grep -n 'DISPATCH_SETTLE' scripts/supervisor/dispatch.sh`) falls
back to `:-1`) is the pause that gives the harness time to finish
repainting.

### It records what it dispatched (#140), and now reads it back (#174)

After the brief is submitted, `dispatch.sh` writes the dispatch to the ledger
(`cli.py record-dispatch`), and `lane-done.sh` marks it complete
(`cli.py record-completion`) after it renames the lane back to `free-N`. The
task id is the window name, the identifier `lanes.sh` and `claim.sh stale`
already key on. `AGENT_SUPERVISOR_STATE_DIR` aims both at a state directory;
unset, it is the default under `~/.local/state/`, which `Ledger.__init__`
`chmod`s to `0700` on first write.

**`dispatch.sh`'s own lane-selection step reads these records now** (#174):
`lanes.sh` still classifies panes exactly as it did, but "is this lane
unowned" is answered from the ledger, not the window name. That write was
made non-fatal-on-failure by #140 specifically because nothing read it back
yet; #140's own docstring named that as a risk to revisit once something did.
A ledger record this dispatch failed to write simply makes that ONE lane read
`unknown` (never offered) until it is reconciled by hand or overwritten by a
later dispatch — see `cli.py`'s `lane_free` docstring and `dispatch.sh` step
6's comment for the full argument on why the write still does not abort an
already-live dispatch.

Two consequences of that, both deliberate:

- **A ledger failure never aborts a dispatch or a completion.** Everywhere
  else in `dispatch.sh` a failure aborts and unwinds, which is right for
  claims and worktrees — real resources. A bookkeeping write with no reader is
  not one: a broken ledger that stopped the estate dispatching would trade the
  estate for a record nobody consumes. Failures are loud on stderr and the run
  stands. `tests/supervisor/test_dispatch.sh` mutation-checks this by making
  the write fatal and asserting the suite goes red.
- **The write is last, after every abort path.** A record asserting work is in
  flight, left by a dispatch that then aborted, is worse than no record — the
  ledger's value is that it can be believed. Ordering guarantees it; there is
  no cleanup step to get wrong.

Neither `register` nor `assign` is used for this, and the difference matters:
`TmuxAdapter.assign_task` *sends a prompt* to the pane, so calling it here
would type a second, competing task at a lane that has just been given its
brief. `record_dispatch` in `cli.py` documents that and the rest of the
routing.

### Retiring a lane administratively, without killing its window (#564)

`lane-done.sh` frees a lane only once its worker signals completion. There was
no tool for the other case — an operator who decides mid-task that a lane
should come out of rotation right now — until retiring three dispatched lanes
by hand (`tmux kill-window`) destroyed the underlying tmux windows, 2026-08-23.
Two of those windows (`build-2`, `build-3`) predated the dispatch that had
claimed and renamed them: `dispatch.sh` respawns whatever idle pane the ledger
says is free (lane identity is `<session>:<index>`, never the window's name —
CLAUDE.md invariant 9), so a window a human had put unrelated, non-lane work
into is exactly as claimable as a genuine pool window. The estate went from
five windows to two, silently — nothing announced it, and only luck (every
lane idle, every branch already pushed) kept the destruction from taking live
work with it.

```bash
lane-retire.sh <window> [session]
```

`lane-retire.sh` unregisters the lane from the ledger (the same
`record-completion` call `lane-done.sh` makes) and renames the window back to
`free-N` — the exact rename-back `lane-done.sh` performs on ordinary
completion. It **never** runs `kill-window` or any other verb that can
destroy a pane. Before doing either, it refuses outright when the target's
worktree has uncommitted changes, or commits that exist nowhere but that
worktree (no upstream, and no same-named branch already on `origin`) — the
same class of check `worktree.sh`'s `safe_remove`/`gc` already apply before
discarding a worktree, for the identical reason: reclaiming a lane's window is
never license to discard whatever work is sitting in it.

## Scheduled session recycling

`recycle.py` decides when a long-lived supervisor session should checkpoint
and hand over to a fresh one, on a wall-clock schedule rather than on
exhaustion (agent-dotfiles#47). Per the transport-adapter boundary above,
`decide_recycle` is pure and tmux-free -- it takes a brief path, a "session
started at" reading, a max session age, and a max brief staleness, and
returns a `RecycleDecision(allowed, reason, channels)`. `respawn_supervisor`
is the thin actuator that replaces the pane's session and seeds it with the
tick prompt; it is never exercised against a live pane in tests.

Every refusal fails closed: a missing brief, a stale brief (not written
within the staleness window), or a brief with no `## Live lanes and armed
channels` section all refuse recycling rather than treating absence as
"nothing to check". A session younger than the max age is a normal negative,
not a refusal.

### The channels section

The brief carries a `## Live lanes and armed channels` heading followed by
a markdown table, one row per in-flight lane:

```text
## Live lanes and armed channels

| Channel | Lane | Working from | Task |
|---|---|---|---|
| `recycling` | `agent-dotfiles:worker-1` | `recycle-brief.md` | #47 session recycling |
```

This is the field a successor re-arms `tmux wait-for` channels from after a
recycle, so tmux's queued-signal behavior (a signal sent to a channel with
no waiter is delivered to the next waiter, not lost) still covers in-flight
lanes across the handover. A successor can read it directly with grep,
without `recycle.py`:

```bash
grep -A5 '^## Live lanes and armed channels' ~/.local/state/agent-dotfiles-supervisor/brief.md
```

An idle supervisor must say so explicitly. The section's body must be
either the table above, or the literal marker `_No lanes armed._` -- nothing
else parses. A heading followed by the marker means "no lanes in flight" and
parses to an empty list. A heading followed by anything else that is neither
a readable table nor that marker -- free text, a stale format, a typo in the
table header -- raises `ChannelsSectionUnparseable`, and `decide_recycle`
turns that into a refusal, not an empty list silently treated as "nothing
running". A heading that is entirely absent is a third, distinct fact --
`parse_armed_channels` raises `ChannelsSectionMissing` for that case.
"Zero channels", "I could not read the channels", and "there is no channels
section at all" are three different values; none of them collapses into
either of the others.

## Lane views (#178)

`lanes.sh` answers "what is every lane doing" for a human at a terminal.
`laneview.sh` is the same answer rendered somewhere a human is already
looking:

```bash
bash scripts/supervisor/laneview.sh text                  # stdout, one line per lane
bash scripts/supervisor/laneview.sh text hill90            # ...for another session
bash scripts/supervisor/laneview.sh opensessions           # a tmux sidebar, via its daemon
bash scripts/supervisor/laneview.sh tui                    # a curses screen, selectable
```

`<impl>` is the basename of a script under `laneview/`; run it with a name
that does not exist and it lists the ones that do. Every renderer is handed
`lanes.sh --json` and reads nothing else — no tmux, no ledger, no second
classifier — so a viewer can never disagree with the supervisor about a
lane, and adding or deleting one changes no other file under `scripts/`.
`laneview/README.md` is the contract: read, never write; degrade to absence
rather than staleness; cost nothing when unused; name every state.

Nothing headless calls this, by rule and now by check
(`tests/supervisor/test_laneview_isolation.sh`). It exists for the human
half — which is why it is documented here rather than only in its own
directory: until #4, a human-invoked tool that no document told a human to
invoke was, from outside, indistinguishable from one nobody invokes.

## MCP server (#198)

A worker has always had to be on a machine where these scripts exist at the
right paths. Claude Code, Codex and Copilot all speak MCP, so a tool surface is
the one interface all three consume without the estate growing a client per
harness. `mcp_server.py` is that surface.

### The seam

Two files, one direction:

- `supervisor_view.py` — the read interface. A `ReadSource` base, one class per
  question, a `READ_SOURCES` registry, and `SupervisorView.read`. Same shape as
  `verdict.py`'s `VerdictSource`/`SOURCES` and `harness/*.sh`: adding a source
  is a class plus an entry, removing one is a deletion, and no caller names any
  of them. It knows nothing about MCP.
- `mcp_server.py` — JSON-RPC 2.0 over stdio, and nothing else. It knows nothing
  about `lanes.sh`, `digest.sh` or the ledger.

Delete `mcp_server.py` and every script in this directory works exactly as it
does now. `test_supervisor_view.py`'s `SeamTest` asserts that by parsing each
Python file's imports, so the claim is checked rather than stated.

Each source WRAPS an existing entry point (`lanes.sh --json`, `digest.sh
--json`, `cli.py status`) rather than reimplementing it. There is no second
behaviour to drift.

### Eleven tools today, not four

**This table originally said "Four tools, and why only four" (`lanes`,
`digest`, `ledger`, `events`) and the "Reads only" section below it said
`WRITE_SOURCES` was empty. Both are false as of this pass (re-checked
2026-08-23 with `python3 scripts/supervisor/mcp_server.py --print-tools`,
piped through `jq '.tools | length'` and `jq '.tools[].name'`): the surface
is now eleven tools, six reads and five writes.**

| tool | source | answers |
| --- | --- | --- |
| `lanes` | `lanes.sh --json` | lane states, optionally for a named session |
| `sessions` | (see `supervisor_view.py`'s `SessionsSource`) | session-level view alongside `lanes` |
| `digest` | `digest.sh --json` | watchdog, poller, director inbox pending/age, lane counts, open PRs (CI + verdict), merges |
| `ledger` | `cli.py status` | registered lanes (harness, repo, **transport**), availability, outstanding tasks per lane |
| `events` | `cli.py events` | the ledger's event log (completion, attention), cursor-resumable |
| `session_remove_check` | (read half of the remove guard) | what `session_remove` would evaluate, without removing anything |
| `session_attach` | write | agent-tui#14, see `supervisor_view.WRITE_SOURCES`'s own docstring |
| `session_detach` | write | agent-tui#14 |
| `session_add` | write | agent-tui#14 |
| `session_remove` | write | agent-tui#14; refuses unless the guard says safe, logs to the ledger before killing anything |
| `session_send` | write | agent-supervisor#508: send an ad-hoc message to an EXISTING session (`--resume`, via `supervisord send`), never raw `tmux send-keys`; reports delivered/failed/unknown, never launders a timeout into delivered |

Tool definitions are always-on context in every consuming session, which cuts
directly against the estate's token aim — so the surface is deliberately kept
to what earns its keep, not literally minimal. `mcp_server.py --print-tools`
emits the exact `tools/list` payload, so the cost is measurable rather than
argued about. (`mcp_server.py`'s own module docstring still says "Ten tools"
as of this pass — also stale by the same `session_send` addition; fix
belongs with that file, not repeated here.)

`ledger` now projects `transport` (`send-keys` / `acp` / `pi-rpc`) alongside
each lane (agent-supervisor#87). It was already a ledger column — every lane
records which transport drives it (agent-supervisor#58) — but the MCP
allowlist omitted it, which meant "how is this lane driven" was the one
acceptance question in #87 that the read surface could not yet answer.

### The event stream (agent-supervisor#87)

The obligation from #198/PHASES.md §4c is narrow: a consumer must be able to
learn that a lane changed state **without polling `lanes` in a loop**. `events`
answers that by wrapping `cli.py events` (`Ledger.list_events`, already ordered
`ORDER BY created_at, key`) with a cursor:

1. Call `events` with no `after_key` to read the whole log once.
2. Store the response's `next_cursor` (an event `key`).
3. Poll `events` again passing that value as `after_key`; the response holds
   only events recorded since, plus a new `next_cursor`.

The cursor is a `key`, not a timestamp — `created_at` has one-second
resolution and two events can land in the same second, so time alone cannot
order them exactly. An unrecognised `after_key` is refused (`ValueError`,
surfaced as an MCP protocol error) rather than silently treated as "start from
the beginning" — a stale cursor from a restarted consumer must be visible as a
gap, not hidden as a full replay.

**What this is not:** a push channel. MCP's stdio transport here is
request/response only (no server-initiated notifications), so "without
polling" means *without polling the expensive, whole-estate `lanes` command in
a loop* — a consumer instead polls the cheap, incremental `events` log, or
polls `digest`/`ledger` at whatever cadence it needs and reconciles against
the cursor it already holds. A server-push stream is a transport-level
change (SSE, WebSocket) that `mcp_server.py`'s stdio framing does not carry,
and is out of scope for a read-only fill of the existing surface — noted here
as a rejected design, not a deferred one, so it is not re-derived from
scratch later.

**Rejected: a dedicated "lane state changed" event type.** `lanes.sh`'s states
(free/busy/hung/blocked/…) are derived by classifying a pane right now, not
recorded facts — writing one to the `events` table on every transition would
make the ledger the author of a UI-shaped concept (`lane_changed`) it does not
otherwise need, for a table whose whole job today is delivery bookkeeping
(`completion`/`attention`). A consumer that wants lane-state transitions polls
`lanes`/`digest` at its own cadence; `events` is for the two event kinds the
ledger already produces.

### It fails loud, never empty

Every source raises `SupervisorUnavailable` when it cannot see its backing
store — a non-zero exit, a zero-byte stdout, or output that is not JSON — and
`_tools_call` turns that into an MCP result with `isError: true` and the reason
attached. A tool that answers `[]` when the session is gone, jq is missing or
the ledger will not open is indistinguishable from a quiet estate, which is
this estate's most-repeated defect. `digest.sh`'s own header states the rule for
itself ("an empty `prs` list and an unreachable GitHub must not look the same");
this carries it across the process boundary.

`digest.sh`'s PARTIAL digest is passed through intact rather than raised on: it
exits 0 with the failures named in `errors` and `ok: false`, and throwing away
the readable half would lose more than it protects.

### Reads and writes

**This section used to say "No write tool is exposed, and `WRITE_SOURCES` is
empty" — true when written, false now.** `WRITE_SOURCES` currently holds
five sources (`session_attach`, `session_detach`, `session_add`,
`session_remove`, `session_send` — see the table above), confirmed by
reading `supervisor_view.py`'s `WRITE_SOURCES` dict directly. `mcp_server.py`
still enforces the read/write split structurally — `SupervisorView.__init__`
asserts every `READ_SOURCES` source has `mutates is False` and every
`WRITE_SOURCES` source has `mutates is True`, refusing to construct
otherwise — the assertion just no longer means "so there are no writes"; it
means "so a source can't land in the wrong registry." `session_attach`,
`session_detach`, `session_add` and `session_remove` were added for
agent-tui#14; `session_send` (agent-supervisor#508) is the newest and a
different risk shape — the first write here that runs a live agent turn
rather than a tmux control-plane operation — see `SessionSendSource`'s own
docstring in `supervisor_view.py` for why it is still bounded the same way
(one exact, caller-supplied `session_id`, no lane-claim race, delivery
observed rather than inferred).

Nonces never leave. `LedgerSource` projects rows through explicit
`LANE_FIELDS`/`TASK_FIELDS` allowlists — a column added to the schema later is
omitted by default and must be named to appear. `EventsSource` follows the
same discipline with `EVENT_FIELDS`, even though the `events` table carries no
credential today.

### Wiring it into a harness

Declared once in `settings/mcp/servers.json` and projected by `sync.py` into
all three harnesses: `~/.claude.json` (`merge_mcp`),
`~/.copilot/mcp-config.json` (`merge_copilot_mcp`) and `~/.codex/config.toml`
(`merge_codex_mcp`). #198 also taught the Codex renderer to render `command` /
`args`; before that a stdio server became a bare `[mcp_servers.<name>]` table
Codex could not launch.

Run it by hand against any MCP client:

```bash
python3 scripts/supervisor/mcp_server.py            # stdio server
python3 scripts/supervisor/mcp_server.py --print-tools
npx @modelcontextprotocol/inspector --cli python3 \
  scripts/supervisor/mcp_server.py --method tools/list
```

## Capped state (agent-supervisor#248)

`digest.sh` answers "what is the state of the estate" for a human reading a
terminal. `state.sh` answers the same question for a prompt: a small,
**hard-capped** document meant to be reinjected as *current truth* on every
supervisor turn, replacing conversation-history replay rather than adding to
it (`sfw/loom`'s `task_state.py` is the prior art this follows, capped at
roughly 500–1500 tokens).

```bash
scripts/supervisor/state.sh          # human-readable, capped
scripts/supervisor/state.sh --json   # the same facts, uncapped, for tooling
```

It is a projection, not a second implementation: `digest.sh --json` supplies
every live measurement (watchdog/poller health, lane table, PR verdicts, the
delivered-vs-pane reconciliation that already covers a #235-shaped stale
row), `cli.py status` supplies which lane has which task open, and
`loop-tick.md`'s own `## Boundaries` section is parsed for the standing
constraints rather than copied by hand — editing that section changes
`state.sh`'s output without touching the script.

**The cap is enforced, not aspirational.** The token estimate (chars/4,
named as the crude instrument it is) is always printed. When the full
document would exceed `STATE_TOKEN_CAP` (default 1500), a fixed reduction
ladder drops or summarises sections — trimming the dispatched/PR lists,
then collapsing the constraints list to a pointer — never silently, and a
row is never dropped for being *unknown*, only for not fitting after every
reduction. If even the most reduced document still exceeds the cap, `state.sh`
exits 2 and says `CAP EXCEEDED` rather than emit an over-budget document or
widen the ceiling.

**This line used to say `quota` reads `unknown` unconditionally because no
usage/rate-limit tracker exists anywhere in this estate — that is now
false, and `state.sh`'s own comment says so:** `quota.sh`, `quota-watch.sh`
and `quota-watch-recover.sh` all exist, `quota-watch.sh` runs under
launchd, and `state.sh` reads quota-watch's *persisted* verdict (not a live
`quota.sh` call, deliberately — `quota.sh` can take 45s+ and this document
is re-read every turn) from `$SUPERVISOR_STATE/.quota-watch.state`. It
still falls back to `unknown` when that file is missing, unreadable, or
stale (>1800s old, i.e. more than six quota-watch intervals), and the
`state` field it reports is explicitly the *reported* value, not
`confirmed` — see `state.sh`'s own comment block above `QUOTA_STATE=` for
why that distinction is load-bearing (a blind meter and a healthy one must
not render identically).

**Every shell-out is bounded (agent-supervisor#251).** `state.sh`'s calls to
`digest.sh --json` and `cli.py status`, and `digest.sh`'s own `gh api`/
`gh run list` calls, all run through the same self-contained `kill -0` poll
loop quota.sh (#267) and advance-live.sh (#51) use — not a `timeout`/
`gtimeout` wrapper, since production PATH is pinned to `/usr/bin:/bin` and
neither ships on macOS. A timeout degrades the same way any other read
failure does: `gate: FAIL` with the reason named, never a hang. And every
section that reads `[]`/empty on failure says so explicitly — `dispatched`
and `constraints` read `unknown -- <reason>` rather than `none`/empty on a
read failure, so an unreadable ledger or a missing `loop-tick.md` is never
indistinguishable from a genuinely idle one.

## Verification

```bash
python3 -m unittest discover -s tests -v
python3 -m py_compile scripts/supervisor/*.py
```

The first command is this repository's own test command, run locally from
the repository root; it discovers this core's tests under
`tests/supervisor/` along with the rest of the suite — including the
stub-driven bash suites for `lanes.sh`, `watchdog.sh`, `claim.sh`,
`worktree.sh` and `dispatch.sh`, which `test_shell_suites.py` runs as
subtests. Until that shim existed the sentence above was false for them: they
were in no workflow and no test shelled out to them, so a regression in
`lanes.sh` would have reached `main` green.

`.github/workflows/validate.yml` runs the same tests on every `pull_request`,
but not as that one command (agent-supervisor#440: the 89 bash suites,
executed serially inside `test_shell_suites_pass`, owned ~99% of a 22-minute
run — that count is #440's own historical measurement at the time of that
PR, not a claim about today; re-counted for this pass with `find
tests/supervisor -name 'test_*.sh' | wc -l` and confirmed against
`plan_shell_shards.py`'s own discovery: **106** suites now, not 89 — the
suite has grown since #440, this is not a retraction of #440's number). The
Python tests run in their own `unit-tests` job
(`SHELL_SUITE_SKIP=1` so this job does not also run all 89 bash suites);
`plan-shell-shards` bin-packs the currently-discovered `test_*.sh` files by
measured wall time (`scripts/ci/plan_shell_shards.py`,
`tests/supervisor/shell_suite_timings.json`) into 5 balanced `shell-suites`
matrix shards, each invoking `test_shell_suites.py` directly with
`SHELL_SUITE_ONLY` set to its assigned subset. All three jobs must be green
for `ci_gate.py` to allow a merge — see `merge-pr.sh`.

`test_bootstrap_session.sh`, `test_lane_done.sh` and `test_restore.sh` also
gate real-tmux checks behind `command -v tmux`, skipping loudly
(`SKIP real-tmux checks: tmux not installed …`) rather than passing silently
when tmux is absent. The workflow installs tmux via `apt-get` so CI exercises
those checks for real instead of skipping them.

The v4-cutover, rollback, and launchd-adapter behavior described below is
Hill90-specific and lives in Hill90's `service.sh` and `install.sh`, not in
this directory:

Do not run this service alongside the retired v4 supervisor. `service.sh`
checks both v4 LaunchAgents (`com.hill90.codex-supervisor` and its `-awake`
companion) plus v4's enabled marker before starting v5. Use its explicit
`cutover` command to stop v4 first. Rollback refuses to restart v4 until v5 has
no open tasks or unacknowledged events, then verifies v4 can read canonical
state and archives v4's old delivery cursor so it takes a fresh snapshot.

`install.sh` installs the reviewed files without starting them.

## Retention

The ledger is an audit and recovery record, not a cache: results, event
payloads, and snapshots have no automatic deletion policy. Back up the state
directory before any explicit archival or deletion decision. Launchd stdout and
stderr logs are operational diagnostics and should be rotated by the host's
normal log-retention policy; never use log deletion as ledger cleanup.
