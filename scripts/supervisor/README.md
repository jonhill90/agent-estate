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
```

`dispatch.sh` calls `new` and hands the printed path to the lane, rather than
a brief telling the lane to create its own worktree — a step in a brief is a
step that can be skipped. `done` and `guard` both refuse when the target has
uncommitted changes, matching `safe-deletion`: a worktree with uncommitted
changes is someone's unfinished work, not garbage. `guard` is for the
Director's own use of the shared checkout, which caused the same class of bug
this tool exists to prevent.

## Dispatch

`dispatch.sh` is the caller. It performs one dispatch end to end — pick a free
lane, claim the issue, create the worktree, rename the window, send the brief:

```bash
dispatch.sh <issue> <slug> <brief-file> [repo] [repo-path]
```

A lane is a candidate only if `lanes.sh` calls it `free` **and** its window is
named `free-N`. Both are needed: `lanes.sh` reads pane content, so a lane that
finished without being renamed, and one paused on an approval prompt, look
identical to an unowned one — the name is the only signal that separates them,
and `/clear`ing the wrong lane destroys whatever it had not posted yet (#89).
The lane cannot be chosen from the environment; there is no override.

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
