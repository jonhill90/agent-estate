# agent-tui

**A terminal application for the agent estate.** Left-rail session navigation,
live lane state, and — over time — knowledge, memory and sandbox management.

It **consumes** [`agent-supervisor`](https://github.com/jonhill90/agent-supervisor)
over MCP and the RPC transport. It does not import supervisor internals, and the
supervisor has no opinion about how a human sees it. Either side is removable.

Go + [Bubble Tea](https://github.com/charmbracelet/bubbletea).

## Status

Nothing here yet. This repo exists so the boundary is real from the first
commit rather than extracted later — `agent-supervisor` was split out of
`agent-dotfiles` only after it reached 25,000 lines, and a coupling missed by
that inventory (a default tmux session name) survived a full day past the split.

## What it is for

- **A left rail for managing tmux sessions.** The anchor feature, and the one
  asked for four times.
- **State you can read at a glance** — glyphs and motion rather than words,
  without losing the fidelity of the supervisor's eleven lane states.
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
