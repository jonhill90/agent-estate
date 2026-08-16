# SPEC — agent-tui, technical design as it exists

**What this document is:** the technical design of what is ACTUALLY on
`main` today, not the intended end state (that is `docs/PRD.md`). Every
claim below is checked against `origin/main` `d5e4dab`, **verified
2026-08-16T01:21:59Z** — `go build ./...`, `go vet ./...`, and
`go test ./...` all pass at that SHA (output: 8 tested packages, all `ok`,
`cmd/agent-tui` has no test files).

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

## Today's shape: four programs behind flags, not an application

`cmd/agent-tui/main.go` has **four mutually exclusive `tea.NewProgram` call
sites**, selected by boolean flags parsed at the top of `main()`:

| flag | package | needs a supervisor connection? |
|---|---|---|
| (none — default) | `internal/rail` (`rail.NewMultiSession`) | yes |
| `-board` | `internal/board` | yes (reads `lanes` via the same fetch closure) |
| `-cost` | `internal/cost` | **no** — reads only `ccusage` |
| `-gallery` | `internal/gallery` | **no** — reads only compiled-in glyph data |

`-cost` and `-gallery` both `return` before `connect()` is called, i.e.
before any MCP subprocess spawns. There is no in-app navigation between
these four: switching from the rail to the board means quitting and
relaunching with a different flag. The flag help text says so of itself —
`-board`'s help calls it "a separate screen, never a rail restyle."

**Why this is the state, not a temporary oversight:** composition already
works in exactly one place — the cost line renders *inside* the rail via
`cost.RenderCompact` (see `rail.NewMultiSession`'s use of `costFetch`). Its
absence everywhere else is therefore a choice already made once, not a
capability that doesn't exist.

### The deeper cause: `rail.Model.View` clamps width

`internal/rail/model.go:26-28` (cited by name and constant, not line
number, per this repo's own comment convention — the number is given once
here as a locator for this doc's own verification, not to be relied on
after the file next changes):

```go
// RailWidth is the target column count for the rail region.
const RailWidth = 28
```

`Model.View()` clamps to it unconditionally:

```go
width := m.width
if width <= 0 || width > RailWidth+8 {
    width = RailWidth
}
```

So even under `tea.WithAltScreen()` on a wide terminal, the rail renders
only its own ~28–36 column bordered strip and nothing else — there is no
content region left over for a second view to occupy beside it. This is the
mechanism behind "four screens behind flags": the flags are the symptom,
this clamp is the cause. Confirmed present at `d5e4dab`,
2026-08-16T01:21:59Z; `internal/rail/sessions_test.go:43` and `:220` and
`internal/rail/ops_test.go:87` all set `m.width = RailWidth + 8` specifically
to probe "the widest `View()` honors without clamping back."

### What exists to fix this, and isn't wired

`origin/lane/38-app-shell` (unmerged, one commit `2034b2d`, WIP, **zero
tests**) adds `internal/shell.Model` — described in its own commit message
as "the one Bubble Tea model intended to replace `cmd/agent-tui`'s four
mutually exclusive `tea.NewProgram` call sites." It composes `rail.Model`
(always visible, left-docked) with `board`/`cost`/`gallery.Model` as
content-pane candidates, with `[tab]` focus routing and `[f1-f4]` pane
selection, and includes a `WindowSizeMsg` sizing fix the rail-only path
doesn't need. Not constructed by `main.go`. `origin/lane/38-app-shell` is
the only shell/router-related work on the remote — `git ls-remote --heads`
against `origin` on 2026-08-16T01:21:59Z shows no `lane/38-view-router` ref
at all; it was a local-only branch, never pushed, standing in for a
duplicate task that was stood down. Do not assume a locally-visible branch
name has a remote counterpart — `git ls-remote` asks the remote directly,
while `git branch -a` and `origin/<name>` read a local cache that can be
stale or simply invented.

## Adapter seams

Every package that reaches outside the process is behind a narrow,
function- or interface-typed seam supplied by `cmd/agent-tui`. This is the
mechanism that makes "enable, disable, or remove a piece" mean something —
today it means "pass a different flag and construct a different `Model`"
rather than true runtime composition, which is exactly the gap above.

### `rail.Fetcher` / `rail.SessionsFetcher` (`internal/rail/model.go`)

```go
type Fetcher func() ([]lane.Lane, error)
type SessionsFetcher func() ([]lane.Session, error)
```

Wraps the supervisor's `lanes` (one session) and `sessions` (every tmux
session, `agent-tui#13`) MCP tools. `rail.NewMultiSession` takes both: if
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
but have no caller in `internal/rail` as of `d5e4dab`** — confirmed by
`grep -rn "\.Attach(\|\.Detach("` across the repo outside test files,
2026-08-16T01:21:59Z: zero matches. They were wired to keys once, then
`3137206` removed the key bindings (not the interface methods) because MCP's
stdio transport carries no client identity for `switch-client`/
`detach-client` to target correctly. `session.RemoveCheck`/`Remove` back
`[x]remove`'s two-step confirm; `session.Add` backs `[n]ew`.

### `cost.Fetcher` (built in `cmd/agent-tui/cost.go`, consumed by `internal/cost`)

`internal/cost` does no I/O itself; `cmd/agent-tui` shells `ccusage` and
hands the package only its already-parsed JSON. `cost.Figure{Known, Value}`
is the pattern every "may be absent" value in this repo follows — `Known`
is the only field a caller may branch on; a caller must never read a zero
`Value` as "zero spent" when `Known` is false.

### `theme.Theme` / `theme.Load` (`internal/theme`)

```go
type Theme struct {
    ID, Name string
    // one lipgloss-resolvable colour per theme.Role
}
```

Thirteen `Role`s (`RoleError`, `RoleDirector`, `RoleSelectedBG`, ...,
`AllRoles` in `internal/theme/theme.go`) are the only vocabulary a render
path file may use — no package under `internal/{board,rail,cost,gallery}`
names a concrete colour. `theme.Load(theme.ConfigPath())` resolves, in
order: a per-role colour override map layered over a named base theme
(`signal-dark` default, `mono-contrast` a high-contrast verification theme),
read from `$AGENT_TUI_THEME_CONFIG` or
`$XDG_CONFIG_HOME/agent-tui/theme.json`. Three outcomes, always one of:
missing config → `theme.Default`, empty notice; malformed config or unknown
theme name → `theme.Default` plus a notice every screen renders visibly
(never silent); valid config → that theme. A single bad colour entry drops
only that entry (with a notice naming the role), not the whole file — fixed
in commit `d5e4dab` itself (`agent-tui#34` fix pass) after a review found a
JSON number or nested object as a colour value previously failed
`json.Unmarshal` on the entire config.

### `chat.Source` — **not on `main`**

`internal/chat` exists only on `origin/lane/20-chat-threads` (unmerged).
There, `chat.Source` is the fixture-backed read seam (`chat.FixtureSource`
is the only implementation shipped), with two `Layouts`. It is not part of
the `d5e4dab` tree; do not document it as a current seam without saying so.
See `docs/PRD.md`'s chat section for status.

## MCP transport

`internal/mcp.Client` (`internal/mcp/client.go`) is a minimal JSON-RPC 2.0
client, newline-delimited JSON framed over a child process's stdio. It
speaks exactly the subset `agent-supervisor`'s `scripts/supervisor/mcp_server.py`
implements — `initialize` and `tools/call` — and has no knowledge of lanes,
tmux, or any other supervisor internal; it only knows MCP's wire shape.
`callTimeout` (10s, a `var` so tests can shrink it) bounds every
request/response round trip so a non-responding supervisor subprocess
surfaces as a visible error rather than an indefinite hang — added at the
`#22` review after the prior implementation could only unblock if the child
crashed and closed its pipes.

`cmd/agent-tui/main.go`'s `connect()` chooses how to start the child:
`-mcp-cmd` (full override, e.g. an SSH hop to a remote supervisor) takes
precedence over `-supervisor-repo` (spawns
`python3 <repo>/scripts/supervisor/mcp_server.py`).

## Theme and glyph data model

Two data models, kept deliberately separate:

- **`internal/theme`** governs chrome: colour by `Role`, border characters,
  chrome padding, the director's `★` accent. A theme is a struct literal in
  `internal/theme/registry.go`; adding one is a data change, not a new
  render code path. `theme.Cycle` (registry.go) backs the `t` runtime
  picker in every screen — deliberately non-persisting; it never calls
  `theme.Save`, so cycling never rewrites a user's `theme.json` for them.
- **`internal/lane`** governs which glyph animates which of the
  supervisor's lane states, as enumerated by `states.go`'s `AllStates` (14
  as of `d5e4dab`, counted rather than hardcoded here) — `variants.go`, a
  `[]GlyphSet` literal,
  is the single source; nothing reads a variant's ID anywhere else. Two
  sets ship as of `d5e4dab`: `signal` (default, braille spinner, glitch/
  pulse motion) and `nerd` (Font Awesome glyphs via a patched Nerd Font's
  Private Use Area, flagged `[NF]` in the gallery). `ascii`, `blocks`, and
  `emoji` were judged live against a running rail and deleted outright
  (`5fc880c`), not merely deprioritised — `variants.go`'s `init()` refuses
  to start if any shipped set doesn't cover every state in `AllStates`, so
  a partial set cannot silently ship, and this count cannot drift from the
  code the way a hardcoded number in this prose could.

## Gap between intent and code, stated plainly

This section exists because a README describing the intended application
as though it already exists is the specific defect this documentation sweep
was commissioned to fix (`DOCS-GROUNDING-SWEEP.md`, `GROUNDING-REVIEW.md`
Finding 1). All items below verified 2026-08-16T01:21:59Z against `d5e4dab`
unless noted:

1. **No application shell on `main`.** Four flag-selected programs, as
   described above. `internal/shell.Model` exists, unwired, zero tests, on
   unmerged `origin/lane/38-app-shell`.
2. **`[a]ttach`/`[d]etach` are gone from the rail's keybindings.** The
   interface methods remain (`session.Interface`), uncalled. Restoring them
   needs `agent-supervisor#189` (client-identity plumbing) first.
3. **Chat does not exist on `main`.** Built and tested standalone on
   unmerged `origin/lane/20-chat-threads`; its own commit message states it
   stopped specifically to avoid adding a fifth flag-selected screen.
4. **The hill90 1:1 comparison is currently unfalsifiable** — no estate
   access to the web harness to compare against, and no measurement has
   ever been attempted.
5. **Knowledge/memory viewer and AgentBox sandboxes have no code or branch**
   as of this SHA.

None of the above is a defect in what shipped; each named item works as
built and is tested at the `Model.Update` level with synthetic key
messages against fakes (see `AGENTS.md`'s "Running the tests"). The gap is
between what has shipped and the single-application intent in
`docs/PRD.md` — closing it is `agent-tui#38`/the successor to
`lane/38-app-shell`, not a bug in any of the four existing screens.
