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
- An outstanding delivered task observed idle produces the persistent
  `attention:<task-id>` event. It cannot be acknowledged until the task is
  completed, failed, or cancelled, and notified events retry after their
  deadline.
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
