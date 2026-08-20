# SPEC — agent-tui, technical design as it exists

**What this document is:** the technical design of what is ACTUALLY on
`main` today, not the intended end state (that is `docs/PRD.md`). Every
claim below is checked against `origin/main` `b00db9b`, **verified
2026-08-16** — `go build ./...`, `go vet ./...`, and `go test ./...` all
pass at that SHA (output: 9 tested packages, all `ok`; `cmd/keelson` has no
test files). The product is named `steading` (agent-tui#42, decided
2026-08-20); this document refers to the binary/module by their current,
factual name (`keelson`) only where the code itself uses it, never as a
claim that the product is named that. See `AGENTS.md`'s naming note.

## Stack

- **Go 1.25** (`go.mod`; CI pins the toolchain to 1.26 in
  `.github/workflows/*.yml`).
- **[Bubble Tea](https://github.com/charmbracelet/bubbletea) v1.3.10** —
  the Elm-architecture TUI framework (`Model`/`Update`/`View`) every screen
  in this repo is built on.
- **[Lip Gloss](https://github.com/charmbracelet/lipgloss) v1.1.0** —
  styling; `internal/theme` is the seam between this and every render path.
- No database, no server. The only network/subprocess I/O is: the MCP child
  process (`internal/mcp`), `gh` CLI invocations (`internal/board`), a
  read-only `sqlite3` open (`internal/board`), and `ccusage` (`internal/cost`).

## Today's shape: one process, one shell, four panes

`cmd/keelson/main.go` constructs exactly **one** `tea.NewProgram` call site,
running `internal/shell.Model` (agent-tui#38, landed on `main` in PR #43).
`internal/shell.Model` owns a persistent left rail (`internal/rail`, always
visible) and a content area that holds one of three panes — board, cost,
gallery — switched with `[f1]`–`[f4]`, never a relaunch. `[tab]` toggles
keyboard focus between the rail and whichever pane is active.

| flag | pane it opens on | needs a supervisor connection? |
|---|---|---|
| (none — default) | `PaneHome` (rail + a placeholder content view) | yes |
| `-board` | `PaneBoard` | yes (rail's own `sessions`/`lanes` fetch, plus the board's `gh`/ledger reads) |
| `-cost` | `PaneCost` | yes — the rail beside it still needs a connection, even though the cost pane itself reads only `ccusage` |
| `-gallery` | `PaneGallery` | yes — same reasoning; the gallery pane itself reads only compiled-in glyph data |

This is a real change from the pre-#38 shape (four mutually exclusive
`tea.NewProgram` sites selected by boolean flags, each its own process):
every flag above now only chooses which pane the ONE process opens on
(`shell.Model.WithStart`, `cmd/keelson/main.go`'s `start` switch). Because
the rail is now always on screen, every launch needs a supervisor
connection — including a bare `-cost`/`-gallery` start, which needed none
before #38 because there was no rail beside it to feed. This is the
mechanism behind agent-tui#49's first defect: bare launch demands a
connection it cannot silently do without, and currently fails closed
(`os.Exit(1)`) instead of degrading — see "Known defects" below.

### `internal/shell.Model`: how panes compose

`internal/shell/model.go` — 4 files, 690 lines including tests
(`model.go`, `model_test.go`, `model_teatest_test.go`):

- **`resize`** sizes the rail first, to its fixed `rail.RailWidth` (28
  columns); the rail's own *rendered* width (border/padding included) is
  then measured with `lipgloss.Width` rather than recomputed by hand.
  Whatever width is left over — never the raw terminal width — is what
  every content pane is sized to, whether visible or not.
- **`routeAll`** forwards every non-key, non-resize `tea.Msg` (ticks, fetch
  results) to all four sub-models unconditionally, so each keeps
  refreshing in the background even while a different pane is on screen —
  switching to a pane shows already-fresh data, not a blank first frame.
- **`routeKey`** sends a `KeyMsg` to exactly one region: the rail if
  `focus == focusRail`, otherwise whichever pane is `active`. `ctrl+c` is
  intercepted at the top of `Update` before routing, as a universal escape
  hatch — the agent-tui#22 trap (a mode that swallowed every key, quit
  included) must never be able to recur just because a future pane adds a
  new key-swallowing state. Plain `q` is deliberately *not* caught the same
  way, so the rail's own session-name-entry mode can still accept a literal
  `q` as a character.
- **`boardOK`** mirrors `-board`'s own launch-time refusal (no ledger, no
  start) but for *navigation*: reaching the board pane by `[f2]` when no
  `-ledger` was configured renders `shell.Model.unavailableView` — `"!
  board unavailable"` plus the reason string — instead of running the
  board's fetch loop against an empty path. Verified live: `go build -o
  /tmp/keelson-check ./cmd/keelson`, run with `-board` and no `-ledger`, and
  the flag-parse refusal fires before the program starts at all (see
  "Known defects").

Not verified against a live rendered session for its animation/motion
claims — the render-path assertions in this document are read from source
(`Motion` fields, `View()` methods) and driven through `Model.Update` with
synthetic `tea.Msg` values in tests, not watched as animated terminal
frames.

## Adapter seams

Every package that reaches outside the process is behind a narrow,
function- or interface-typed seam supplied by `cmd/keelson`. This is the
mechanism that makes "enable, disable, or remove a piece" mean something —
today it means "the shell composes a different set of already-typed
`Model`s and `Fetcher`s," never a raw `os/exec.Command` inside `internal/`.

### `rail.Fetcher` / `rail.SessionsFetcher` (`internal/rail/model.go`)

```go
type Fetcher func() ([]lane.Lane, error)
type SessionsFetcher func() ([]lane.Session, error)
```

Wraps the supervisor's `lanes` (one session) and `sessions` (every tmux
session, agent-tui#13) MCP tools. `rail.NewMultiSession` takes both: if
`sessions` isn't available on an unpatched supervisor, it degrades to the
single-session `lanes` read with a visible note rather than rendering
nothing.

### `session.Interface` (`internal/session/ops.go`)

```go
type Interface interface {
    Attach(session string) error
    Detach() error
    Add(session string, lanes int, agent, cwd string) (AddResult, error)
    RemoveCheck(session string) (RemoveCheck, error)
    Remove(session string, confirm bool) (RemoveResult, error)
}
```

The write path. `session.Ops` is the only production implementation, over
`mcp.Client.CallTool`. **`Attach` and `Detach` are declared and implemented
but have no caller in `internal/rail` or `internal/shell`** — confirmed by
`grep -rn "\.Attach(\|\.Detach("` across the repo outside test files: zero
matches. They were wired to keys once, then `3137206` removed the key
bindings (not the interface methods) because MCP's stdio transport carries
no client identity for `switch-client`/`detach-client` to target correctly.
`session.RemoveCheck`/`Remove` back `[x]remove`'s two-step confirm;
`session.Add` backs `[n]ew`.

### `cost.Fetcher` (built in `cmd/keelson/cost.go`, consumed by `internal/cost`)

`internal/cost` does no I/O itself; `cmd/keelson` shells `ccusage` and
hands the package only its already-parsed JSON. `cost.Figure{Known, Value}`
is the pattern every "may be absent" value in this repo follows — `Known`
is the only field a caller may branch on; a caller must never read a zero
`Value` as "zero spent" when `Known` is false. **Not wired to
`scripts/supervisor/quota.sh`** — confirmed by `grep -rn "quota.sh"
--include='*.go' .` returning zero matches — see "Known defects."

### `board.Fetcher`-shaped functions (`cmd/keelson/board.go`)

Composes `gh issue|pr list` (intent), the ledger's `tasks`/`source_tasks`
tables (`sqlite3 PRAGMA query_only=1`, never `-readonly` — see
`internal/board/ledger.go`'s doc comment for why that pragma matters for a
WAL-mode file), and the same `lanes` fetch the rail already uses (blocked
detection), into one `board.Snapshot` — a projection, never a fourth data
store. `buildTaskFetch` returns `nil` when `-ledger`/`$AGENT_TUI_LEDGER` is
empty, which `rail.Model.WithTasks(nil)` treats as a no-op so the rail's
own per-lane task text keeps working with no ledger configured.

### `theme.Theme` / `theme.Load` (`internal/theme`)

```go
type Theme struct {
    ID, Name string
    // one lipgloss-resolvable colour per theme.Role
}
```

`AllRoles` (`internal/theme/theme.go`) is the only vocabulary a render path
file may use — no package under `internal/{board,rail,cost,gallery,shell}`
names a concrete colour. `theme.Load(theme.ConfigPath())` resolves, in
order: a per-role colour override map layered over a named base theme
(`signal-dark` default, `mono-contrast` a high-contrast verification theme),
read from `$AGENT_TUI_THEME_CONFIG` or
`$XDG_CONFIG_HOME/agent-tui/theme.json`. Three outcomes, always one of:
missing config → `theme.Default`, empty notice; malformed config or unknown
theme name → `theme.Default` plus a notice every screen renders visibly
(never silent); valid config → that theme. A single bad colour entry drops
only that entry (with a notice naming the role), not the whole file — fixed
in commit `d5e4dab` (agent-tui#34 fix pass) after a review found a JSON
number or nested object as a colour value previously failed
`json.Unmarshal` on the entire config. `[t]` cycles the active theme at
runtime in every pane, live; it never calls `theme.Save`, so a user's own
config file is untouched until they edit it themselves.

### `chat.Source` — **not on `main`**

`internal/chat` exists only on `origin/lane/20-chat-threads` (unmerged).
There, `chat.Source` is the fixture-backed read seam (`chat.FixtureSource`
is the only implementation shipped), with two `Layouts`. It is not part of
the `b00db9b` tree; do not document it as a current seam without saying so.
See `docs/PRD.md`'s chat section for status.

## MCP transport

`internal/mcp.Client` (`internal/mcp/client.go`) is a minimal JSON-RPC 2.0
client, newline-delimited JSON framed over a child process's stdio. It
speaks exactly the subset `agent-supervisor`'s `scripts/supervisor/mcp_server.py`
implements — `initialize` and `tools/call` — and has no knowledge of lanes,
tmux, or any other supervisor internal; it only knows MCP's wire shape.
`callTimeout` (10s, a `var` so tests can shrink it) bounds every
request/response round trip so a non-responding supervisor subprocess
surfaces as a visible error rather than an indefinite hang.

`cmd/keelson/main.go`'s `connect()` chooses how to start the child:
`-mcp-cmd` (full override, e.g. an SSH hop to a remote supervisor) takes
precedence over `-supervisor-repo` (spawns
`python3 <repo>/scripts/supervisor/mcp_server.py`). With neither set and
`$AGENT_SUPERVISOR_REPO` unset, `connect()` returns an error and `main()`
exits 1 before `tea.NewProgram` is ever constructed — agent-tui#49's first
defect, see below.

## Theme and glyph data model

Two data models, kept deliberately separate:

- **`internal/theme`** governs chrome: colour by `Role`, border characters,
  chrome padding, the director's `★` accent. A theme is a struct literal in
  `internal/theme/registry.go`; adding one is a data change, not a new
  render code path.
- **`internal/lane`** governs which glyph animates which of the
  supervisor's lane states, as enumerated by `states.go`'s `AllStates` (14
  as of `d5e4dab`, counted rather than hardcoded here — `variants.go`'s
  `[]GlyphSet` literal is the single source; nothing reads a variant's ID
  anywhere else). Two sets ship: `signal` (default, braille spinner,
  glitch/pulse motion) and `nerd` (Font Awesome glyphs via a patched Nerd
  Font's Private Use Area, flagged `[NF]` in the gallery). `ascii`,
  `blocks`, and `emoji` were judged live against a running rail and deleted
  outright (`5fc880c`), not merely deprioritised — `variants.go`'s `init()`
  refuses to start if any shipped set doesn't cover every state in
  `AllStates`, so a partial set cannot silently ship.

## Known defects, verified live against `b00db9b`

agent-tui#49 (open) records three; all three were confirmed here by
running the built binary, not by reading source:

1. **Bare launch exits 1.** `go build -o /tmp/keelson-check ./cmd/keelson
   && /tmp/keelson-check` with no flags and no `$AGENT_SUPERVISOR_REPO`
   prints `no supervisor to connect to: set -supervisor-repo,
   $AGENT_SUPERVISOR_REPO, or -mcp-cmd` and exits 1 (`connect()`'s default
   case, `cmd/keelson/main.go`) instead of opening in a degraded state.
2. **The board pane's unavailable message.** Reaching `PaneBoard` via
   `[f2]` with no `-ledger` renders `shell.Model.unavailableView`'s `"!
   board unavailable"` plus `main.go`'s `boardUnavailable` string
   (`"no -ledger (or $AGENT_TUI_LEDGER) configured -- point it at a COPY of
   the ledger to use the board"`) — confirmed present in
   `cmd/keelson/main.go` and `internal/shell/model.go`.
3. **The cost panel's quota line has no live quota source.** It renders
   `"unknown (no quota source)"` (`internal/cost/view.go`) for any harness
   `ccusage` cannot compute a blocks/limit figure for. `scripts/supervisor/quota.sh`
   is not referenced anywhere in this module's Go source (`grep -rn
   "quota.sh" --include='*.go' .` — zero matches), so it is not the source
   this panel actually reads, contrary to what its presence in
   `agent-supervisor` might suggest.

None of the three is a defect in what shipped for #38 — the shell composes
correctly; these are pre-existing gaps in the individual panes it now
exposes by navigation as well as by launch flag. Fixing or re-documenting
any of them is in scope for future work on this repo; presenting only the
happy path is not.

## Gap between intent and code, stated plainly

All items below verified 2026-08-16 against `b00db9b` unless noted:

1. **`[a]ttach`/`[d]etach` are gone from the rail's keybindings.** The
   interface methods remain (`session.Interface`), uncalled. Restoring them
   needs `agent-supervisor#189` (client-identity plumbing) first.
2. **Chat does not exist on `main`.** Built and tested standalone on
   unmerged `origin/lane/20-chat-threads`; its own commit message states it
   stopped specifically to avoid adding a fifth flag-selected screen ahead
   of the shell landing.
3. **The hill90 1:1 comparison is currently unfalsifiable** — no estate
   access to the web harness to compare against, and no measurement has
   ever been attempted.
4. **Knowledge/memory viewer and AgentBox sandboxes have no code or branch**
   as of this SHA.
5. **The three defects in "Known defects" above** are gaps between the
   shell composing correctly and the panes it composes behaving well when
   navigated to rather than launched into directly.

None of items 1–4 is a defect in what shipped; each named item works as
built and is tested at the `Model.Update` level with synthetic key
messages against fakes (see `AGENTS.md`'s "Running the tests"). The gap is
between what has shipped and the single-application intent in
`docs/PRD.md`.
