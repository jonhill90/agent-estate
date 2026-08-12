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
- Architecture notifications contain event IDs and result paths—not tmux
  scrollback or broad repository snapshots—and are marked notified only after
  the architecture harness is genuinely active.
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
  --harness codex --repo /Users/jon/source/repos/Personal/Hill90

hill90-supervisor register --lane infra-claude --target %8 \
  --harness claude --repo /Users/jon/source/repos/Personal/Hill90

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
in the input. `DISPATCH_SETTLE` (default 2s) is the pause that gives the
harness time to finish repainting.

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

### Three tools, and why only three

| tool | source | answers |
| --- | --- | --- |
| `lanes` | `lanes.sh --json` | lane states, optionally for a named session |
| `digest` | `digest.sh --json` | watchdog, poller, lane counts, open PRs, merges |
| `ledger` | `cli.py status` | registered lanes, availability, outstanding tasks |

Tool definitions are always-on context in every consuming session, which cuts
directly against the estate's token aim — so the surface is deliberately small.
`mcp_server.py --print-tools` emits the exact `tools/list` payload, so the cost
is measurable rather than argued about.

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

### Reads only

No write tool is exposed, and `WRITE_SOURCES` is empty. `tool_definitions`
refuses to publish any source whose `mutates` is true, so the absence is
enforced rather than remembered. What a write tool needs first, none of which
exists: a caller identity (`cli.py`'s `_verify_caller` authenticates a lane by a
pane nonce an off-machine client cannot have), a blast-radius bound (a tool any
agent may call on its own initiative is a third dispatcher, and #184/#209 are
both about two), and an audit trail that survives the client. The seam is
there; the authorisation story is not, so the tools are not.

Nonces never leave. `LedgerSource` projects rows through explicit
`LANE_FIELDS`/`TASK_FIELDS` allowlists — a column added to the schema later is
omitted by default and must be named to appear.

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

## Verification

```bash
python3 -m unittest discover -s tests -v
python3 -m py_compile scripts/supervisor/*.py
```

The first command is agent-dotfiles' repository-wide test command, run from
the repository root; it discovers this core's tests under `tests/supervisor/`
along with the rest of the suite — including the stub-driven bash suites for
`lanes.sh`, `watchdog.sh`, `claim.sh`, `worktree.sh` and `dispatch.sh`, which
`test_shell_suites.py` runs as
subtests. Until that shim existed the sentence above was false for them: they
were in no workflow and no test shelled out to them, so a regression in
`lanes.sh` would have reached `main` green.

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
