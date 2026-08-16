# AGENTS.md — agent-tui

Repository policy for an agent arriving here. `CLAUDE.md` is a committed
symlink to this file — one source, two harness-visible names, no sync step.
Edit this file; never edit `CLAUDE.md` directly (it will edit the same bytes,
but say so in the commit message if you do, to avoid a reviewer thinking two
files drifted).

**Verified 2026-08-16T01:21:59Z against `origin/main` `d5e4dab`.** Confirm the
SHA in `git log -1` still matches before trusting counts below; they are
measured, not estimated.

## What this repo is

`agent-tui` is a terminal UI, Go + [Bubble Tea](https://github.com/charmbracelet/bubbletea),
that reads `agent-supervisor`'s state over MCP and renders it for a human. It
is a **viewer with one write path** (session attach/detach/add/remove, see
below) — same discipline as `agent-supervisor`'s own
`scripts/supervisor/laneview/`. It never shells out to `tmux` directly, never
reads or writes the ledger except through the adapters listed below, and
never reimplements `ccusage`'s or `lanes.sh`'s parsing.

Read `README.md` for what has shipped, `docs/PRD.md` for what the product is
for, and `docs/SPEC.md` for the technical design. This file is arrival
policy only.

## What belongs here vs. `agent-supervisor`

- **Here:** rendering, layout, glyph/theme data, keybindings, anything that
  turns supervisor state into pixels a human reads. The one exception is the
  session write path (`internal/session`), which is a thin MCP call wrapper
  with zero tmux knowledge of its own.
- **In `agent-supervisor`:** tmux orchestration, the ledger, dispatch, the
  MCP server itself (`scripts/supervisor/mcp_server.py`), and any logic that
  decides whether an operation is *safe* (e.g. `session_remove_check`'s
  refusal rules). If a change requires knowing tmux client identity, session
  guard logic, or ledger schema, it is a supervisor change with an agent-tui
  caller added after, not the reverse.
- **Never here:** a second reader of tmux, a second ledger, a fabricated
  metric (a cost or quota figure invented because the real source returned
  nothing — see `internal/cost.Figure`'s `Known` field for the pattern this
  repo uses everywhere data may be absent).

## Layout

```
cmd/agent-tui/       four tea.NewProgram entry points, selected by flag (see docs/SPEC.md)
internal/board/      task board projection — GitHub issues/PRs + ledger tasks + live lanes
internal/cost/       per-harness spend/quota projection from ccusage
internal/gallery/    glyph gallery — every lane state × every candidate glyph set
internal/lane/       lane/session decode, glyph sets (data, not code), state table
internal/mcp/        minimal MCP JSON-RPC client over a child process's stdio
internal/rail/       the left-anchored navigation rail — the one shipped anchor feature
internal/session/    write path: attach/detach/add/remove, all via MCP, no os/exec
internal/theme/      look-and-feel as data — Role-keyed colours, persisted per-user config
scripts/             verify-lanes-unaffected.sh — the rail's non-interference proof
```

`internal/shell` (an unwired application shell, `internal/chat` (an unwired
chat adapter) exist only on unmerged branches — see `docs/SPEC.md`'s "Gap
between intent and code" section before assuming either is on `main`.

## Adapter discipline

Every package that touches the outside world is behind a function-typed or
interface-typed seam, supplied by `cmd/agent-tui/main.go`:

| seam | package | what it hides |
|---|---|---|
| `rail.Fetcher`, `rail.SessionsFetcher` | `internal/rail` | the MCP `lanes`/`sessions` tool calls |
| `session.Interface` | `internal/session` | attach/detach/add/remove, each one `mcp.Client.CallTool` |
| `cost.Fetcher` (built in `cmd/agent-tui/cost.go`) | `internal/cost` | shelling out to `ccusage` |
| `board.Fetcher`-shaped functions (`cmd/agent-tui/board.go`) | `internal/board` | `gh` CLI calls and a read-only `sqlite3` ledger open |
| `theme.Theme` / `theme.Load` | `internal/theme` | every colour, border and chrome literal |

**Why this matters practically:** every package's tests construct a fake
implementing the seam, not a real subprocess. If you add a feature that needs
new external data, add it as a new field on an existing seam or a new
function-typed seam — never an `os/exec.Command` inside `internal/*` directly.
`internal/mcp` is the only package that knows it is talking to a subprocess;
everything above it knows only Go types.

## Running the tests

```
go build ./...
go vet ./...
go test ./...
```

All three verified green at `d5e4dab`, 2026-08-16T01:21:59Z (8 packages with
tests, `cmd/agent-tui` has none). CI (`.github/workflows/*.yml`) runs the
same three commands on `ubuntu-latest`, Go 1.26, plus a fourth check gated on
a live `agent-supervisor` checkout: `internal/lane/states_lanessh_test.go`
cross-checks `lane.AllStates` against `lanes.sh`'s own `state=` assignments
when `$AGENT_SUPERVISOR_REPO` is set, and skips otherwise — this repo must
still build and test standalone with no supervisor checkout present.

To run the rail against a real supervisor:

```
go build -o agent-tui ./cmd/agent-tui
AGENT_SUPERVISOR_REPO=/path/to/agent-supervisor ./agent-tui
```

The board and cost/gallery screens are separate binaries-by-flag today
(`-board`, `-cost`, `-gallery`) — see `docs/SPEC.md` for why that is a known
gap, not the design.

**A binary that builds is not a feature that works.** `go test` exercises
`Model.Update` with synthetic key messages against fakes; it does not press a
key against a live tmux session. Before documenting a control as working,
either cite the test that drives it through `Update` (name it) or say
"not verified against a live session."

## Conventions

- **Code comments cite functions and behaviours, never line numbers.** A
  comment naming a caller by line number is wrong the moment the file is
  next edited. Existing comments in this repo already follow this — match
  it.
- **Every seam is a `func` type or a small interface, not a concrete
  dependency.** See "Adapter discipline" above.
- **Absence is a typed value, never a bare zero.** `cost.Figure.Known`,
  `theme.Load`'s notice string, `session.Worktree.Clean *bool` (nil is a
  third state, not false) are the pattern: a caller must be able to tell "we
  looked and it's zero" from "we could not look." Follow it for any new data
  that might be unavailable rather than absent.
- **Dated claims.** Any doc comment or README line asserting something is
  true today, not merely intended, should be checkable against a commit SHA
  or a test name. This file and its siblings under `docs/` carry a `Verified
  <UTC>` stamp at the top; update it when you re-check the claims below it,
  don't just edit the prose.
- **Glyph sets and themes are data, not code** (`internal/lane/variants.go`,
  `internal/theme/registry.go`) — a new visual variant is a struct literal
  addition, never a new code path in a render function.

## What NOT to do here

- Do not add a fifth `tea.NewProgram` flag-selected screen. The known gap is
  the missing application shell (see `docs/SPEC.md`); a fifth flag repeats
  the defect `lane/20-chat-threads` explicitly declined to repeat.
- Do not call `os/exec` for tmux from any package under `internal/`. Every
  tmux-adjacent operation is a supervisor MCP tool call.
- Do not restore `[a]ttach`/`[d]etach` in the rail without the client-identity
  fix tracked at `agent-supervisor#189`. They were removed in `3137206`
  because MCP's stdio transport gives the supervisor no way to know which
  tmux client is asking, so `switch-client`/`detach-client` acts on an
  arbitrary attached client while reporting success. `session.Interface`
  still declares both methods; nothing in `internal/rail` calls them as of
  `d5e4dab`.
- Do not point `-ledger` at the live supervisor's `ledger.sqlite3`. It is
  always opened read-only, but the flag help and `internal/board/ledger.go`
  both document why a copy is still required.
