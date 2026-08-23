---
type: Spec
description: The technical design of what is actually built in agent-tui today.
generated:
  at: 2026-08-23T13:43:05-04:00
---

# SPEC — agent-tui, technical design as it exists

**What this document is:** the technical design of what is ACTUALLY on
`main` today, not the intended end state (that is `docs/PRD.md`). Every
claim below is checked against `origin/main` `b00db9b`, **verified
2026-08-16** — `go build ./...`, `go vet ./...`, and `go test ./...` all
pass at that SHA (output: 9 tested packages, all `ok`; `cmd/estate` has no
test files). The product is named the Estate, binary `estate` (decided
2026-08-23, superseding `steading`, agent-tui#42's prose-only name, and
`keelson`, the module/binary's own earlier name before this document's own
distinction between the two collapsed). See `AGENTS.md`'s naming note for
the full history.

**Partially re-verified against `390c99a`, 2026-08-23
(estate-loop/b-docs-stale, docs-stale-sweep worktree).** The `b00db9b`
stamp above is stale for several sections below — corrected in place where
found false, each correction dated and cited with its own evidence rather
than a blanket restamp. Sections not called out as corrected below were not
re-walked in this pass and should still be treated as `b00db9b`-era until
someone does.

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

## Today's shape: one process, one shell, five panes

**Stale, corrected 2026-08-23 against `390c99a`.** The paragraph below
describes the pre-`docs/SPEC-shell.md` shape: a persistent `internal/rail`
left column with a handful of panes behind it. That is no longer the
architecture. `SPEC-shell.md`'s S1-S3 replaced the rail with
`internal/nav.Model` as the fixed left column (a sidebar modelled 1:1 on
the hill90 web nav); `internal/rail` is now reached as a routed pane
(`PaneLanes`, behind the sidebar's "Lanes" route) rather than always being
on screen — its own render/key logic is unchanged, only its screen
position moved (`scripts/verify-lanes-unaffected.sh`). There are now 20
`Pane*` constants in `internal/shell/model.go` (`PaneHome` through
`PaneSecrets`, counted directly from the `const ( ... )` block), most
reached via the nav sidebar rather than an f-key.

**Stale, re-measured 2026-08-23 (estate-loop/b-docs-stale sweep, pass 2,
against `56513a2`):** the `const ( ... )` block now has **24** `Pane*`
constants, not 20 (counted directly, same method as before). At least
three of the four-constant growth is `agent-tui#115`/`#122`'s
`PaneLaneChatLanePrimary`, `PaneLaneChatRoomPrimary`, and
`PaneLaneChatUnifiedList` (the combined Lanes+Chat surface variants),
added since this count was last taken; not re-deriving whether the
original 20 was itself exact at the time it was written, only that 24 is
the live count now. `[f1]`-`[f6]` still map
to exactly the original six — Home/Board/Cost/Gallery/Flow/Chat
(`internal/shell/model.go`'s key-handling `switch`, `case "f1"` through
`case "f6"`) — everything added since (Agents, Skills, MCP Servers,
Connectors, Admin, Dashboard, Library, Monitor, Workflows, Knowledge, API
Docs, Platform Docs, Secrets, plus the Lanes/Stub panes) is nav-sidebar-only,
no f-key of its own. The paragraph originally here is kept below for
historical shape (four-pane, rail-fixed) but is no longer accurate; treat
the correction above as current.

`cmd/estate/main.go` constructs exactly **one** `tea.NewProgram` call site,
running `internal/shell.Model` (agent-tui#38, landed on `main` in PR #43).
`internal/shell.Model` owns a persistent left rail (`internal/rail`, always
visible) and a content area that holds one of four panes — board, cost,
gallery, flow, chat (`internal/flow`, agent-tui#64; `internal/chat`, agent-tui#20) — switched with `[f1]`–`[f6]`,
never a relaunch. `[tab]` toggles keyboard focus between the rail and
whichever pane is active.

| flag | pane it opens on | needs a supervisor connection? |
|---|---|---|
| (none — default) | `PaneHome` (rail + a placeholder content view) | yes |
| `-board` | `PaneBoard` | yes (rail's own `sessions`/`lanes` fetch, plus the board's `gh`/ledger reads) |
| `-cost` | `PaneCost` | yes — the rail beside it still needs a connection, even though the cost pane itself reads only `ccusage` |
| `-gallery` | `PaneGallery` | yes — same reasoning; the gallery pane itself reads only compiled-in glyph data |
| `-flow` | `PaneFlow` | yes, plus the same `-ledger`/board data `-board` needs — flow reads board's own `Snapshot()` (`shell.Model` pushes it in after every `board.Update`), never a second gh/ledger read; refuses to start under the same `boardOK == false` rule `-board` does |

This is a real change from the pre-#38 shape (four mutually exclusive
`tea.NewProgram` sites selected by boolean flags, each its own process):
every flag above now only chooses which pane the ONE process opens on
(`shell.Model.WithStart`, `cmd/estate/main.go`'s `start` switch). Because
the rail is now always on screen, every launch needs a supervisor
connection — including a bare `-cost`/`-gallery` start, which needed none
before #38 because there was no rail beside it to feed. This is the
mechanism behind agent-tui#49's first defect: bare launch demands a
connection it cannot silently do without, and currently fails closed
(`os.Exit(1)`) instead of degrading — see "Known defects" below.

### `internal/shell.Model`: how panes compose

`internal/shell/model.go` — 4 files, 690 lines including tests
(`model.go`, `model_test.go`, `model_teatest_test.go`). **Stale, re-measured
2026-08-23 against `390c99a`** (`wc -l internal/shell/*.go`): the package is
now 7 files, 2,926 lines total — `model.go` (1,349), `model_test.go` (120),
`model_teatest_test.go` (352), `mouse.go` (272), `mouse_test.go` (214),
`nav_teatest_test.go` (492), `theme_test.go` (127). `mouse.go`/`mouse_test.go`
and the two `_teatest_test.go` files did not exist at `b00db9b`:

**Stale again, re-measured 2026-08-23 (estate-loop/b-docs-stale sweep, pass
2, against `56513a2`):** same 7 files, now **3,022 lines total** —
`model.go` grew from 1,349 to **1,445** (the `agent-tui#114`/`#115`/`#122`
chat-room and lanechat-variant wiring landed since the count above), every
other file in the package unchanged.

- **`resize`** sizes the rail first, to its fixed `rail.RailWidth` (28
  columns); the rail's own *rendered* width (border/padding included) is
  then measured with `lipgloss.Width` rather than recomputed by hand.
  Whatever width is left over — never the raw terminal width — is what
  every content pane is sized to, whether visible or not.
- **`routeAll`** forwards every non-key, non-resize `tea.Msg` (ticks, fetch
  results) to all five sub-models unconditionally, so each keeps
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
  /tmp/estate-check ./cmd/estate`, run with `-board` and no `-ledger`, and
  the flag-parse refusal fires before the program starts at all (see
  "Known defects").

Not verified against a live rendered session for its animation/motion
claims — the render-path assertions in this document are read from source
(`Motion` fields, `View()` methods) and driven through `Model.Update` with
synthetic `tea.Msg` values in tests, not watched as animated terminal
frames.

## Adapter seams

Every package that reaches outside the process is behind a narrow,
function- or interface-typed seam supplied by `cmd/estate`. This is the
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

### `cost.Fetcher` (built in `cmd/estate/cost.go`, consumed by `internal/cost`)

`internal/cost` does no I/O itself; `cmd/estate` shells `ccusage` and
hands the package only its already-parsed JSON. `cost.Figure{Known, Value}`
is the pattern every "may be absent" value in this repo follows — `Known`
is the only field a caller may branch on; a caller must never read a zero
`Value` as "zero spent" when `Known` is false. **Not wired to
`scripts/supervisor/quota.sh`** — confirmed by `grep -rn "quota.sh"
--include='*.go' .` returning zero matches — see "Known defects." **Fixed,
stale as of `390c99a`:** `internal/cost/quota.go` now shells `quota.sh` out
via `QuotaRunner`/`ExecQuotaRunner`, wired from `cmd/estate/main.go`'s
`resolvedQuotaBin`; `grep -rn "quota.sh" --include='*.go' .` now returns
matches throughout `internal/cost` and `cmd/estate` (re-run 2026-08-23,
non-empty). See "Known defects" below, also corrected.

### `board.Fetcher`-shaped functions (`cmd/estate/board.go`)

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

### `chat.Source` (agent-tui#20)

`internal/chat` is wired into `internal/shell` as `PaneChat` (`[f6]` -- `[f5]` was already `PaneFlow`, agent-tui#64, by the time this rebased onto it).
`chat.Source` is the fixture-backed read seam (`chat.FixtureSource` is the
only implementation shipped — no lane in this estate runs on a structured
transport yet, see `internal/chat/fixture.go`'s doc comment), with two
`Layouts` (`internal/chat/layouts.go`). **Stale, corrected 2026-08-23
against `390c99a`:** `chat.FixtureSource` is no longer the only
implementation. `agent-tui#99` (commit `5997399`) shipped
`internal/chat/claudecode.go`'s `ClaudeCodeSource`, which reads real Claude
Code CLI session transcripts from the local project directory, and
`internal/chat/fallback.go`'s `FallbackSource`, which `cmd/estate` actually
wires in: try `ClaudeCodeSource` first, and only fall back to the fixture
when the real source reports itself genuinely unconfigured
(`ErrNoProjectDir`), never on a real read error or real emptiness.
`FixtureSource` is now the last-resort fallback, not the only source. The
thread list and the "big" transcript are `bubbles/viewport`-backed rather
than a plain string clipped to a height, with an always-reserved indicator
row so overflowing content is both flagged and reachable (`pgup`/`pgdn`,
`home`/`end`, or `[f]` to focus a grid tile) — see `docs/PRD.md`'s chat
section for status.

## MCP transport

`internal/mcp.Client` (`internal/mcp/client.go`) is a minimal JSON-RPC 2.0
client, newline-delimited JSON framed over a child process's stdio. It
speaks exactly the subset `agent-supervisor`'s `scripts/supervisor/mcp_server.py`
implements — `initialize` and `tools/call` — and has no knowledge of lanes,
tmux, or any other supervisor internal; it only knows MCP's wire shape.
`callTimeout` (10s, a `var` so tests can shrink it) bounds every
request/response round trip so a non-responding supervisor subprocess
surfaces as a visible error rather than an indefinite hang.

`cmd/estate/main.go`'s `connect()` chooses how to start the child:
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

**All three closed, `6942926`/`390c99a`, 2026-08-23.** `agent-tui#49` is
closed; the three defects below are historical, kept for record with their
fixes noted rather than deleted. See `AGENTS.md`'s own "Known defects"
section for the fix evidence (`cmd/estate/main.go`'s `supervisorRepoResolved`
handling for #1, `resolveLedgerSource`/`defaultLedgerLivePath`/
`newLedgerCopier` for #2, `internal/cost/quota.go`'s `QuotaRunner`/
`ExecQuotaRunner` wiring for #3) — the same evidence, not re-derived here.

agent-tui#49 (open) records three; all three were confirmed here by
running the built binary, not by reading source:

1. **Bare launch exits 1.** `go build -o /tmp/estate-check ./cmd/estate
   && /tmp/estate-check` with no flags and no `$AGENT_SUPERVISOR_REPO`
   prints `no supervisor to connect to: set -supervisor-repo,
   $AGENT_SUPERVISOR_REPO, or -mcp-cmd` and exits 1 (`connect()`'s default
   case, `cmd/estate/main.go`) instead of opening in a degraded state.
2. **The board pane's unavailable message.** Reaching `PaneBoard` via
   `[f2]` with no `-ledger` renders `shell.Model.unavailableView`'s `"!
   board unavailable"` plus `main.go`'s `boardUnavailable` string
   (`"no -ledger (or $AGENT_TUI_LEDGER) configured -- point it at a COPY of
   the ledger to use the board"`) — confirmed present in
   `cmd/estate/main.go` and `internal/shell/model.go`.
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
   needs `agent-supervisor#189` (client-identity plumbing) first. Still
   true as of `390c99a` — see `AGENTS.md`'s "What NOT to do here", which
   re-confirms zero `.Attach(`/`.Detach(` callers outside test files.
   **Partially stale, corrected 2026-08-23 (estate-loop/b-docs-stale
   sweep, pass 2):** `agent-supervisor#189` closed 2026-08-16, fixed by
   `agent-supervisor#202` — the client-identity prerequisite this item
   names is resolved. Zero `.Attach(`/`.Detach(` callers still holds as of
   `56513a2` (re-grepped), so the keys are still not restored — just no
   longer blocked on the supervisor side, only on someone doing the
   agent-tui-side work.
2. **Chat has no live transport.** `internal/chat` is wired into the shell
   (agent-tui#20, `[f6]`) and renders correctly, but the only `chat.Source`
   shipped is `chat.FixtureSource` — no lane in this estate runs on a
   structured transport (`acp`/`pi-rpc`) yet, so there is nothing live to
   read. **False as of `390c99a`, corrected 2026-08-23** — same correction
   as the `chat.Source` section above: `ClaudeCodeSource` (agent-tui#99,
   commit `5997399`) reads real Claude Code CLI transcripts, and
   `FallbackSource` is what `cmd/estate` actually wires in.
   `chat.Sender` also now has a real implementation over `session_send`
   (agent-tui#104, commit `6942926`) — see the S7 correction in
   `docs/SPEC-shell.md`.
3. **The hill90 1:1 comparison is currently unfalsifiable** — no estate
   access to the web harness to compare against, and no measurement has
   ever been attempted.
4. **Knowledge/memory viewer and AgentBox sandboxes have no code or branch**
   as of this SHA. **The Knowledge-viewer half is false as of `390c99a`,
   corrected 2026-08-23:** `internal/knowledge` exists and is wired as
   `PaneKnowledge` (agent-tui#87, commit `922400b`). The AgentBox half still
   holds — no container driver code exists (`docs/SPEC-shell.md` S12's own
   2026-08-22 update: only the `ExecutionMode` interface and the Agents
   view's MODE column shipped, never a container driver).
5. **The three defects in "Known defects" above** are gaps between the
   shell composing correctly and the panes it composes behaving well when
   navigated to rather than launched into directly. **All three closed as
   of `6942926`** — see the "Known defects" section's own correction above.

None of items 1–4 is a defect in what shipped; each named item works as
built and is tested at the `Model.Update` level with synthetic key
messages against fakes (see `AGENTS.md`'s "Running the tests"). The gap is
between what has shipped and the single-application intent in
`docs/PRD.md`.
