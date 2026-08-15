# agent-tui

**A terminal application for the agent estate.** Left-rail session navigation,
live lane state, and — over time — knowledge, memory and sandbox management.

It **consumes** [`agent-supervisor`](https://github.com/jonhill90/agent-supervisor)
over MCP and the RPC transport. It does not import supervisor internals, and the
supervisor has no opinion about how a human sees it. Either side is removable.

Go + [Bubble Tea](https://github.com/charmbracelet/bubbletea).

## Status

**27 shipped: aesthetics are data, not code.** Every look-and-feel literal
in the render path -- colour, border character, chrome padding, the
director's "★" mark -- now lives in `internal/theme` as one of two
`theme.Theme` values (`signal-dark`, today's appearance unchanged, and
`mono-contrast`, a high-contrast verification theme that exists to prove
the routing, not to be used day to day). `internal/board`, `internal/rail`,
`internal/cost` and `internal/gallery` ask for a `theme.Role`
(`RoleError`, `RoleDirector`, `RoleSelectedBG`, ...) at render time; none of
them names a hex value any more. Switching the whole look is a one-line
edit to a user's config (`{"theme": "mono-contrast"}` at
`$AGENT_TUI_THEME_CONFIG`, or `$XDG_CONFIG_HOME/agent-tui/theme.json`), not
a change to any of those four packages -- see
`internal/rail/theme_test.go`'s `TestThemeSwitchChangesEverySurface` for
the driven, mutation-checked proof, and the matching per-package tests in
`internal/board`, `internal/cost` and `internal/gallery`. A missing config
renders exactly as before this existed; a malformed config or an unknown
theme name renders the default theme and says so visibly, never silently
(`internal/theme.Load`). Glyph sets (agent-tui#11/#16, Jon's picks from
#24: Signal and set 5) were already data before this shipped and keep their
own home in `internal/lane` -- this issue governs the chrome around them,
not the glyphs themselves. The hill90 palette Jon actually wants is a
follow-up now that the seam exists, not part of this drop (agent-tui#27:
"do not invent a palette to ship with").

**6 shipped: the task board, as a projection.** `agent-tui -board` renders
a second screen -- five columns (Backlog, In progress, In review, Blocked,
Done), grouped by repo, across every repo the estate touches. **A card's
column is computed fresh on every fetch, never stored** -- there is no
write path from this screen into GitHub or the ledger, and no fourth store:
Backlog/In progress/Done come from GitHub issues and PRs (`gh issue|pr
list`), In progress/lane linkage and cycle time come from the ledger's
`tasks`/`source_tasks` tables (opened `sqlite3 -readonly`, never the live
file), and Blocked additionally checks the same live `lanes` MCP payload
the rail already fetches. See `internal/board/card.go`'s `Derive` for the
whole rule table, and `agent-supervisor#127`/PR#130 for the reconciliation
sweep this board depends on (`source_tasks` was write-once before that
landed -- confirm `select source_state, status, count(*) from source_tasks
group by 1,2` shows a distribution, not one row, before trusting a board
built on a ledger the sweep hasn't touched).

**10 shipped: the board LOOKS like a board.** Real bordered columns with
cards, not a flat list -- six layout variants ship (`internal/board/
layout.go`), picked live with **1-6**, same picker convention as the
rail's glyph sets. Each varies column style (boxed / thin rules /
whitespace-only), density, card shape (single-line / multi-line with
metadata), colour theme (restrained / vivid), and grouping (by-column
across every repo, or by-repo swimlanes -- the evolution of #6's old
`by-repo` view):

| # | id | what it's for |
|---|----|----------------|
| 1 (default) | `kanban-column` | the eye-candy default -- bordered columns, vivid per-column colour, one card per box |
| 2 | `kanban-repo` | same look, swimlaned per repo |
| 3 | `compact-column` | thin rules not boxes, one line per card, muted colour -- speed, or a small terminal |
| 4 | `compact-repo` | compact's grouping, swimlaned |
| 5 | `kanban-recent` | kanban, plus Done cards completed in the last 24h |
| 6 | `whitespace-all` | no border characters at all, every Done card ever -- lightest to render |

Closed items (`Done` cards) default to hidden -- shown only by picking a
layout that says so (5 or 6 above), never a separate prompt.

**Project selection** toggles which repos' cards show, letter keys beside
each repo's name in the on-screen legend (`[a]`, `[b]`, ...), `[0]` to show
every repo again. Selection is a pure filter over the already-fetched
snapshot -- toggling a repo never triggers a new `gh`/ledger read.

WIP is shown **per tmux session** (the part of a lane name before `:`),
not globally -- two workers per session is the estate's real capacity, and
a session running three is flagged `OVER` right on the board. A card that
has sat in its current column two hours or more (`as#95`'s own case: it
sat CONFLICTING for two hours before a human noticed) is marked `!` and
colored -- in every layout, restrained theme included.

```
go build -o agent-tui ./cmd/agent-tui
AGENT_SUPERVISOR_REPO=/path/to/agent-supervisor ./agent-tui -board \
  -ledger /path/to/a/COPY/of/ledger.sqlite3
```

`-ledger` is always opened `sqlite3 -readonly` (a second line of defense
behind pointing it at a copy in the first place), defaults to
`$AGENT_TUI_LEDGER`, then `$AGENT_SUPERVISOR_STATE_DIR/ledger.sqlite3`.
`-repositories` accepts the exact `SUPERVISOR_REPOSITORIES` shape
agent-supervisor's own `.env.example` documents; unset, the board unions
agent-supervisor's own `DEFAULT_REPOSITORIES` with every repo it discovers
in the ledger's own `source_tasks.source_url` column, so a repo dispatched
under an env override that was never persisted (agent-tui itself, measured
while building this) still shows up without editing a flag.

**5a shipped: the left rail.** `cmd/agent-tui` renders a left-anchored
navigation rail (~28 columns) driven entirely by the supervisor's MCP
surface — no second reader of tmux, no ledger access. Every state `lanes.sh`
emits animates: a spinner on `busy`, a settled dot on `free`, glitch/pulse
motion on `hung`/`dead`/`stale`/`broken` so they read as wrong at a glance
instead of just printing the word. See `internal/lane/glyph.go` for the full
state→glyph map and `internal/rail/model.go` for the Bubble Tea program.

**13 shipped: every tmux session, not one.** The rail regressed to a
single-session list before anyone diffed it against the retired Python
prototype — see [agent-tui#13](https://github.com/jonhill90/agent-tui/issues/13).
It now reads the supervisor's `sessions` tool (`internal/rail.NewMultiSession`)
and renders every tmux session grouped, `director` included and styled
distinctly (`★`, gold accent — "something to make it special"), with an
interim `supervised`/`unsupervised` marker per session (real ledger
evidence, not agent-supervisor#153's own marker, which had not landed when
this shipped — see `sessions.sh` and `lane.Session`'s doc comments for
exactly what it proves and doesn't). Selection (`j`/`k`) spans every
session's lanes as one list; `g` cycles the grouping style
(flat-with-headers / indented tree — collapsible is a named gap, not
shipped). `-board` is unaffected: it still reads `lanes` for one session.

```
go build -o agent-tui ./cmd/agent-tui
AGENT_SUPERVISOR_REPO=/path/to/agent-supervisor ./agent-tui
```

It spawns `python3 scripts/supervisor/mcp_server.py` from the given
`agent-supervisor` checkout as a child process and speaks MCP JSON-RPC over
its stdio — the same protocol Claude Code/Codex/Copilot use, not a private
wire format. `-mcp-cmd` overrides the launch command entirely (e.g. an SSH
hop to a remote supervisor).

### The glyph picker

Press **1-4** while it's running to switch the state glyph set live, against
whatever lanes are actually up — never a mock. This is the one open design
question this drop answers by letting Jon cycle it himself rather than
asking in prose (agent-supervisor#107's addendum). Rail width, theme and
density all keep their defaults this round; the glyph set is the only
variable.

| # | id | what it looks like |
|---|----|---------------------|
| 1 (default) | `signal` | braille spinner, glitch on `hung`/`broken`, pulse on waiting |
| 2 | `ascii` | plain ASCII only — safe over any terminal, font or SSH hop |
| 3 | `blocks` | unicode block elements — solid reads filled, hollow reads empty |
| 4 | `emoji` | color emoji, the only set where `menu-blocked` and `text-blocked` get visually distinct glyphs, not just distinct labels |

Every variant renders all thirteen `lanes.sh` states, `stale`/`menu-blocked`/
`unsent`/`scrolled` included — a variant that can't is not a candidate, and
`internal/lane/variants.go`'s `init()` refuses to start if one is
incomplete. `internal/lane/states.go`'s `AllStates` is the count, and
`internal/lane/states_lanessh_test.go` cross-checks it against `lanes.sh`'s
own `state=` assignments rather than letting it drift unnoticed the way it
did before [agent-tui#3](https://github.com/jonhill90/agent-tui/issues/3).
**The set itself is data, not code:** `internal/lane/variants.go`
is a `[]GlyphSet` literal and nothing reads a variant's ID anywhere else, so
adding a fifth candidate or deleting one Jon dislikes is a one-line change
to that slice — see `internal/lane/glyph.go` for the shape (`Motion`,
`Style`, `GlyphSet`) and `internal/lane/variants.go` for the four shipped
today.

Run in a narrow tmux pane (~24-32 cols) beside a terminal pane — the rail is
a layout region, not a full-screen list, and it never creates that split
itself (see "own window" below).

`scripts/verify-lanes-unaffected.sh <agent-supervisor-repo> <agent-tui-binary>`
is the acceptance proof for "lanes.sh output is byte-identical with the app
running and not running": it spins up an isolated tmux server, snapshots
`lanes.sh --json` for a few decoy windows, runs agent-tui in its own new
window of the same session, snapshots again, and diffs.

This repo exists so the boundary is real from the first commit rather than
extracted later — `agent-supervisor` was split out of `agent-dotfiles` only
after it reached 25,000 lines, and a coupling missed by that inventory (a
default tmux session name) survived a full day past the split.

## What it is for

- **A left rail for managing tmux sessions.** The anchor feature, and the one
  asked for four times.
- **State you can read at a glance** — glyphs and motion rather than words,
  without losing the fidelity of the supervisor's thirteen lane states.
- **Knowledge and memory**, read-first, layered over an existing vault rather
  than owning a second store.
- **Sandboxes** — spinning up AgentBoxes to give harnesses isolated
  environments.
- Treat this list as **open**. Build for extension.

## Design constraints, from measurement not taste

- **It renders in its own window. It never injects panes into live ones.**
  Measured on a real fixture: injecting a fixed-width pane into every window
  changed two lanes' classification, because the supervisor reads panes. A
  sidebar built that way corrupts the state it displays.
- **Acceptance for any renderer:** `lanes.sh` output is byte-identical with the
  app running and not running.
- **Every state the supervisor emits must be nameable.** A state with no glyph
  must not silently read as idle.
- **Single static binary.** Mac, Linux and eventually Windows, with no runtime
  to install.
