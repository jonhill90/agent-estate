# AGENTS.md — keelson

Repository policy for an agent arriving here. `CLAUDE.md` is a committed
symlink to this file — one source, two harness-visible names, no sync step.
Edit this file; never edit `CLAUDE.md` directly (it will edit the same bytes,
but say so in the commit message if you do, to avoid a reviewer thinking two
files drifted).

**Verified against `lane/38-app-keelson`, 2026-08-16 (agent-tui#38's overnight
brief).** Confirm the branch/SHA in `git log -1` still matches before trusting
counts below; they are measured, not estimated. The Go module, `cmd/`
directory and binary are named `keelson` as of this branch; the GitHub repo
itself is still `jonhill90/agent-tui` (renaming it is a separate, more
disruptive step left for later — issue references below keep the
`agent-tui#NN` form because that is the repo they still point at).

## What this repo is

`keelson` (module `github.com/jonhill90/keelson`, formerly `agent-tui` --
that name described the rendering technology, Go + [Bubble Tea](https://github.com/charmbracelet/bubbletea),
not the product) is one terminal application: a persistent left rail over
`agent-supervisor`'s lane/session state, with the task board, cost panel and
glyph gallery reachable as panes in the same process (`internal/shell`,
agent-tui#38). It is a **viewer with one write path** (session
attach/detach/add/remove, see below) — same discipline as
`agent-supervisor`'s own `scripts/supervisor/laneview/`. It never shells out
to `tmux` directly, never reads or writes the ledger except through the
adapters listed below, and never reimplements `ccusage`'s or `lanes.sh`'s
parsing.

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
cmd/keelson/         one tea.NewProgram entry point, running internal/shell.Model (see docs/SPEC.md)
internal/board/      task board projection — GitHub issues/PRs + ledger tasks + live lanes
internal/cost/       per-harness spend/quota projection from ccusage
internal/gallery/    glyph gallery — every lane state × every candidate glyph set
internal/lane/       lane/session decode, glyph sets (data, not code), state table
internal/mcp/        minimal MCP JSON-RPC client over a child process's stdio
internal/rail/       the left-anchored navigation rail — the one shipped anchor feature, now always visible
internal/session/    write path: attach/detach/add/remove, all via MCP, no os/exec
internal/shell/      the application shell -- owns the rail + board/cost/gallery as panes (agent-tui#38)
internal/theme/      look-and-feel as data — Role-keyed colours, persisted per-user config
scripts/             verify-lanes-unaffected.sh — the rail's non-interference proof
```

`internal/chat` (an unwired chat adapter) exists only on an unmerged branch
(`lane/20-chat-threads`) — see `docs/SPEC.md`'s "Gap between intent and code"
section before assuming it is on this branch; it was stopped deliberately to
avoid becoming a fifth flag-selected screen ahead of the shell landing.

## Adapter discipline

Every package that touches the outside world is behind a function-typed or
interface-typed seam, supplied by `cmd/keelson/main.go`:

| seam | package | what it hides |
|---|---|---|
| `rail.Fetcher`, `rail.SessionsFetcher` | `internal/rail` | the MCP `lanes`/`sessions` tool calls |
| `session.Interface` | `internal/session` | attach/detach/add/remove, each one `mcp.Client.CallTool` |
| `cost.Fetcher` (built in `cmd/keelson/cost.go`) | `internal/cost` | shelling out to `ccusage` |
| `board.Fetcher`-shaped functions (`cmd/keelson/board.go`) | `internal/board` | `gh` CLI calls and a read-only `sqlite3` ledger open |
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

All three verified green on `lane/38-app-keelson` (9 packages with tests,
`cmd/keelson` has none). CI (`.github/workflows/*.yml`) runs the same three
commands on `ubuntu-latest`, Go 1.26, plus a fourth check gated on a live
`agent-supervisor` checkout: `internal/lane/states_lanessh_test.go`
cross-checks `lane.AllStates` against `lanes.sh`'s own `state=` assignments
when `$AGENT_SUPERVISOR_REPO` is set, and skips otherwise — this repo must
still build and test standalone with no supervisor checkout present.

To run the app against a real supervisor:

```
go build -o keelson ./cmd/keelson
AGENT_SUPERVISOR_REPO=/path/to/agent-supervisor ./keelson
```

The board, cost and gallery screens are panes reached with `[f2]`/`[f3]`/
`[f4]` inside the one running process (`internal/shell`, agent-tui#38);
`-board`/`-cost`/`-gallery` now only choose which pane the app opens on.

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

- Do not add a new `tea.NewProgram` call site. `internal/shell.Model` is the
  one program now (agent-tui#38); a new view is a pane added to the shell,
  never a second program selected by a launch flag — the mistake a fifth
  flag would have repeated, which `lane/20-chat-threads` explicitly declined
  to make.
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
