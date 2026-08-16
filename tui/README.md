# keelson

*Renamed from `agent-tui`, 2026-08-16 (agent-tui#38's overnight brief) --
`agent-tui` named the rendering technology (Go + Bubble Tea, "tui"), not
the product. A keelson is the timber running along the inside of a ship's
keel, binding all the frames into one structure — the layer above
orchestration, which is what this actually is. The GitHub repo stays
`jonhill90/agent-tui` for tonight; only the binary, module path and
`cmd/` directory moved. See this PR's handoff for what's left.*

A terminal application for the agent estate: reads [`agent-supervisor`](https://github.com/jonhill90/agent-supervisor)'s
lane and session state over MCP and renders it behind a persistent left
rail, with the task board, cost panel and glyph gallery reachable as panes
in the same process. Go + [Bubble Tea](https://github.com/charmbracelet/bubbletea).

It consumes the supervisor; it does not import supervisor internals, and the
supervisor has no opinion about how a human sees it. Either side is
removable. Full technical design: `docs/SPEC.md`. What the product is for:
`docs/PRD.md`. Arrival policy for an agent working in this repo: `AGENTS.md`.

**This section describes what is actually on this branch, checked against
`lane/38-app-keelson`.** Where intent and code diverge, that is said
explicitly — see "What this is not, yet" below, and `docs/SPEC.md`'s "Gap
between intent and code" for the full accounting (not yet re-verified
against this branch; treat its specifics as pre-shell).

## What it is today

**One `tea.NewProgram`, one process.** `internal/shell.Model` owns a
persistent left rail (every tmux session, live lane state) plus a content
pane that holds the task board, cost panel or glyph gallery — reached with
a keypress, never a relaunch (agent-tui#38). `-board`/`-cost`/`-gallery`
still choose which pane the app *opens* on; they stop being the only way to
reach it.

```
go build -o keelson ./cmd/keelson

# opens on the rail with the home pane -- [f2] board, [f3] cost, [f4] gallery,
# [tab] moves focus between the rail and the content pane
AGENT_SUPERVISOR_REPO=/path/to/agent-supervisor ./keelson

# same app, opens on the task board pane -- needs a ledger COPY
AGENT_SUPERVISOR_REPO=/path/to/agent-supervisor ./keelson -board \
  -ledger /path/to/a/COPY/of/ledger.sqlite3

# same app, opens on the cost pane
AGENT_SUPERVISOR_REPO=/path/to/agent-supervisor ./keelson -cost

# same app, opens on the glyph gallery pane
AGENT_SUPERVISOR_REPO=/path/to/agent-supervisor ./keelson -gallery
```

The rail is always on screen now, so every invocation needs a supervisor
connection (`-supervisor-repo`/`$AGENT_SUPERVISOR_REPO`/`-mcp-cmd`) — even
one that opens on the cost or gallery pane, which is a change from the
four-separate-programs era, when `-cost`/`-gallery` needed no connection at
all because there was no rail beside them to feed.

Run `./keelson -h` for every flag; `cmd/keelson/main.go`'s flag help
strings are the authoritative, current documentation for each one — this
README does not restate them.

### The rail (default screen)

A left-anchored navigation rail (~28 columns, `rail.RailWidth`), driven
entirely by the supervisor's `sessions` and `lanes` MCP tools. No second
reader of tmux, no ledger access beyond the optional per-lane task text.
Every state in `internal/lane/states.go`'s `AllStates` (counted, not
hardcoded here — 14 as of `d5e4dab`; `internal/lane/variants.go`'s
`init()` guard checks the same list, so a state added later can't fall out
of sync with this doc) animates distinctly: a spinner on `busy`, a settled
dot on `free` (`dead`, `service`, `supervisor`, and `unknown` are also
`MotionStill`), glitch motion on `hung`/`broken`, pulse on `stale` and the
blocked states — so a wrong state reads as wrong at a glance rather than
requiring the word to be read. Not verified against a live rendered
session — this is read from `signalSet`'s `Motion` field per state, not
watched as animated frames. Sessions are grouped,
`director` styled distinctly (`★`, gold accent), with an interim
supervised/unsupervised marker per session.

**Anchor-feature status:** `[n]ew` and `[x]remove` are wired and tested
through `Model.Update`. `[a]ttach` and `[d]etach` are **not currently
bound to any key** — they were removed in `3137206` because MCP's stdio
transport gives the supervisor no client identity to attach/detach
correctly, so the old bindings silently acted on an arbitrary tmux client
while reporting success. `session.Interface` still declares both methods;
`agent-supervisor#189` tracks the fix this needs before they can come back
honestly. See `AGENTS.md` and `docs/SPEC.md` for the detail.

Live pickers, all driven by real keys against real state (verified through
`Model.Update` tests, not merely rendered):

- **`[1-2]`** cycles the glyph set — `signal` (default) or `nerd` (Font
  Awesome glyphs via a Nerd Font's Private Use Area, flagged `[NF]` in the
  gallery). `ascii`, `blocks`, and `emoji` were judged live against a
  running rail and deleted outright, not merely deprioritised —
  `internal/lane/variants.go`.
- **`[g]`** cycles session grouping (flat-with-headers / indented tree).
- **`[w]`** cycles the rail's content reading between work-centric and
  status-centric (`internal/rail/readings.go`).
- **`[t]`** cycles the active theme in memory, live — never persists; see
  "Themes and glyphs are data" below.

### The task board (`-board`)

A second screen: five columns (Backlog, In progress, In review, Blocked,
Done), grouped by repo, across every repo the estate touches. A card's
column is recomputed fresh on every fetch from three real sources —
`gh issue|pr list` (intent), the ledger's `tasks`/`source_tasks` tables
(opened `sqlite3 -readonly`, never the live file), and the live `lanes` MCP
payload (blocked detection) — never stored as a fourth store. Six layout
variants (`[1-6]`), picked live (not verified against a live session for
this doc — read from the `Layouts` literal in `internal/board/layout.go`,
not watched rendering): boxed columns vs. thin rules vs.
whitespace-only, single-line vs. multi-line cards, restrained vs. vivid
colour, by-column vs. by-repo swimlanes. Project selection (`[a]`, `[b]`,
...) filters the already-fetched snapshot without a new read. `-ledger`
must point at a copy — `-board` refuses to start otherwise, and the read is
always `sqlite3 -readonly` regardless.

### The cost panel (`-cost`)

Per-harness spend and quota pressure from `ccusage`, with an explicit
"unknown" instead of a fabricated zero wherever `ccusage` cannot see a
figure (`cost.Figure.Known`) — including quota buckets `ccusage` genuinely
has no local way to compute (verified against `ccusage codex --help` /
`ccusage pi --help`: no `blocks`/token-limit concept for those harnesses).
Needs no supervisor connection. A compact form of this panel already
renders inside the rail's default view (`cost.RenderCompact`) — the one
place today where two screens are actually composed together.

### The glyph gallery (`-gallery`)

Every state in `AllStates` (14 as of `d5e4dab`) against every candidate glyph,
flagged `[NF]` where a Nerd Font is required to render as intended. Needs
no supervisor connection; reads only compiled-in glyph data
(`lane.Variants`, `lane.Candidates`).

## Themes and glyphs are data, not code

Every look-and-feel literal in the render path — colour, border character,
chrome padding, the director's `★` mark — lives in `internal/theme` as a
`theme.Theme` value keyed by semantic `Role` (`RoleError`, `RoleDirector`,
`RoleSelectedBG`, ...). `internal/board`, `internal/rail`, `internal/cost`
and `internal/gallery` ask for a role at render time; none names a hex
value. Two themes ship: `signal-dark` (default) and `mono-contrast` (a
high-contrast theme that exists to prove the routing works, not for daily
use). Changing the whole look is a one-line edit to
`$AGENT_TUI_THEME_CONFIG` (or `$XDG_CONFIG_HOME/agent-tui/theme.json`):

```json
{"theme": "mono-contrast"}
```

or an optional per-role colour override layered on top:

```json
{"theme": "signal-dark", "colors": {"error": "#ff0000"}}
```

A missing config renders exactly as before this existed. A malformed config
or unknown theme name renders the default theme and **says so visibly** —
never silently — and, as of `d5e4dab`, a single bad colour entry drops only
that entry (with a notice naming the role) rather than discarding the whole
file. `[t]` cycles themes at runtime in every screen, live against whatever
is already on screen; it never calls `theme.Save`, so a user's own config
file is untouched until they edit it themselves.

Glyph sets follow the identical pattern one level down, governing which
rune animates which lane state rather than the chrome around it — see
`internal/lane/variants.go`.

## What this is not, yet

- **No chat screen.** Built and tested standalone
  (`internal/chat`, thread list + transcript and multi-pane-tail layouts,
  an ACP-informed design) on unmerged `origin/lane/20-chat-threads`. Its
  own commit message says it stopped short of wiring in specifically to
  avoid adding a fifth flag-selected screen — it needs the shell above.
- **No knowledge/memory viewer, no AgentBox sandboxes.** No code exists for
  either as of this SHA.
- **The anchor feature is missing two of its four verbs** — see "The rail"
  above.

`docs/PRD.md` states what the product is for, including these; this
section exists so the distance between the two is never silently implied
to be zero.

## Design constraints, from measurement not taste

- **It renders in its own window. It never injects panes into live ones.**
  Measured on a real fixture: injecting a fixed-width pane into every
  window changed two lanes' classification, because the supervisor reads
  panes. A sidebar built that way corrupts the state it displays.
- **Acceptance for any renderer: `lanes.sh` output is byte-identical with
  the app running and not running.**
  `scripts/verify-lanes-unaffected.sh <agent-supervisor-repo> <keelson-binary>`
  is the checked proof — it spins up an isolated tmux server, snapshots
  `lanes.sh --json`, runs keelson in its own window of the same session,
  snapshots again, and diffs.
- **Every state the supervisor emits must be nameable.** A state with no
  glyph must not silently read as idle. `internal/lane/variants.go`'s
  `init()` refuses to start if a shipped glyph set doesn't cover every
  state in `internal/lane/states.go`'s `AllStates`.
- **Single static binary.** Mac and Linux today, Windows eventually, no
  runtime to install.

It spawns `python3 scripts/supervisor/mcp_server.py` from the given
`agent-supervisor` checkout as a child process and speaks MCP JSON-RPC over
its stdio — the same protocol Claude Code/Codex/Copilot use, not a private
wire format (`internal/mcp`). `-mcp-cmd` overrides the launch command
entirely, e.g. an SSH hop to a remote supervisor.

## Building and testing

```
go build ./...
go vet ./...
go test ./...
```

All three verified green at `d5e4dab`, 2026-08-16T01:21:59Z. See
`AGENTS.md` for what CI runs beyond this (a supervisor-checkout-gated
cross-check of the lane-state table) and the adapter discipline that keeps
every package's tests running against fakes rather than real subprocesses.

## Repository split

This repo exists so the boundary between UI and orchestration is real from
the first commit rather than extracted later — `agent-supervisor` was split
out of `agent-dotfiles` only after it reached 25,000 lines, and a coupling
missed by that inventory (a default tmux session name) survived a full day
past the split.
