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
one block per in-flight task, blocks separated by a blank line (or `---`),
each block four `key: value` lines:

```text
## Live lanes and armed channels

channel: infra-claude-tick
lane: infra-claude
brief: ~/.local/state/agent-dotfiles-supervisor/brief.md
description: implementing #47 recycle decision function

channel: skills-tick
lane: skills
brief: ~/.local/state/agent-dotfiles-supervisor/brief.md
description: rostering the loop-memory skill
```

This is the field a successor re-arms `tmux wait-for` channels from after a
recycle, so tmux's queued-signal behavior (a signal sent to a channel with
no waiter is delivered to the next waiter, not lost) still covers in-flight
lanes across the handover. A successor can read it directly with grep,
without `recycle.py`:

```bash
grep -A3 '^channel:' ~/.local/state/agent-dotfiles-supervisor/brief.md
```

A heading present with no blocks under it means "no lanes in flight" and
parses to an empty list. A heading that is entirely absent is not the same
fact -- `parse_armed_channels` raises `ChannelsSectionMissing`, and
`decide_recycle` turns that into a refusal, not an empty list silently
treated as "nothing running".

## Verification

```bash
python3 -m unittest discover -s tests -v
python3 -m py_compile scripts/supervisor/*.py
```

The first command is agent-dotfiles' repository-wide test command, run from
the repository root; it discovers this core's tests under `tests/supervisor/`
along with the rest of the suite.

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
