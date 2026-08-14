# agent-tui

**A terminal application for the agent estate.** Left-rail session navigation,
live lane state, and — over time — knowledge, memory and sandbox management.

It **consumes** [`agent-supervisor`](https://github.com/jonhill90/agent-supervisor)
over MCP and the RPC transport. It does not import supervisor internals, and the
supervisor has no opinion about how a human sees it. Either side is removable.

Go + [Bubble Tea](https://github.com/charmbracelet/bubbletea).

## Status

**5a shipped: the left rail.** `cmd/agent-tui` renders a left-anchored
navigation rail (~28 columns) driven entirely by the supervisor's `lanes` MCP
tool — no second reader of tmux, no ledger access. Every state `lanes.sh`
emits animates: a spinner on `busy`, a settled dot on `free`, glitch/pulse
motion on `hung`/`dead`/`stale`/`broken` so they read as wrong at a glance
instead of just printing the word. See `internal/lane/glyph.go` for the full
state→glyph map and `internal/rail/model.go` for the Bubble Tea program.

```
go build -o agent-tui ./cmd/agent-tui
AGENT_SUPERVISOR_REPO=/path/to/agent-supervisor ./agent-tui -session <tmux-session>
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
