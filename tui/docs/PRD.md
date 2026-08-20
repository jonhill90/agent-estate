# PRD — agent-tui

**What this document is:** what the product is FOR, in Jon's own framing.
Intent, not status — for what has actually shipped, read `README.md`; for
the gap between the two, read `docs/SPEC.md`'s "Gap between intent and
code" section. Every dated claim below is checked against `origin/main`
`b00db9b`, **verified 2026-08-16.** The product is named `steading`
(agent-tui#42, decided 2026-08-20) — see `AGENTS.md`'s naming note for the
reasoning and evidence.

## The one-line framing

A real terminal application — like Claude Code, Codex CLI, or `pi` — for
managing and watching the agent estate. **Not** a renderer, **not** four
screens behind launch flags. Source: `NOTEBOOK-jon-directives.md`, "What he
is building — the TUI."

## Why this exists

The estate (`agent-supervisor`) already runs and reasons about lanes,
sessions, cost, and tasks. Nothing renders it as a single coherent
application a human can sit in front of. `steading` is that surface: it
consumes the supervisor over MCP, adds no orchestration logic of its own,
and is meant to be as removable as it is addable — see `AGENTS.md`'s
adapter discipline for how that boundary is kept real in code, not just
in intent.

## What it is for, feature by feature

### Anchor feature — the vertical session bar

A persistent left rail that manages tmux sessions: **attach, detach, add,
remove.** Jon has asked for this specific set of four verbs, by his own
count, four separate times. It is the anchor — every other feature is
secondary to this one working completely.

**Status as of `b00db9b`:** half-shipped. `add` and `remove` exist and are
wired (`[n]ew`/`[x]remove`, `internal/session.Ops`), now reachable from
inside the one persistent shell rather than a rail-only program. `attach`
and `detach` were removed in commit `3137206` for a reproduced reason, not
neglect: MCP's stdio transport gives the supervisor no client identity to
target, so an attach/detach call silently acts on an arbitrary attached
tmux client while reporting success — worse than no control at all. The fix
is filed as `agent-supervisor#189` and requires a supervisor-side change
(`TmuxTransport.switch_client`/`detach_client` needs to receive a client
identity) before the agent-tui side can be restored honestly. See
`AGENTS.md`'s "What NOT to do here."

### Kanban board

"A fancy GUI that looks like a kanban board. I am really going for some eye
candy and speed." Five columns (Backlog, In progress, In review, Blocked,
Done), across every repo the estate touches, with six selectable layout
variants trading boxes-vs-rules, density, and grouping. A card's column is
always recomputed fresh from GitHub + the ledger, never stored — this board
is a projection, not a fourth data store.

**Status:** shipped as a pane inside the one persistent shell (`[f2]`,
agent-tui#38/#43), composed with the rail rather than a separate program.
Navigating to it with no `-ledger` configured currently renders an
"unavailable" message rather than degrading further or explaining the fix
in-pane — agent-tui#49, see `docs/SPEC.md`'s "Known defects."

### Cost panel

Per-harness spend and quota pressure, sourced from `ccusage`, with an
explicit "unknown" rendering rather than a fabricated zero when a figure
can't be determined (`internal/cost.Figure.Known`) — the panel has been
bitten by silent-zero blindness before and is built not to repeat it.

**Status:** shipped as a pane inside the one persistent shell (`[f3]`),
with its compact form also composed inside the rail (`cost.RenderCompact`).
Its "unknown" quota fallback is honest, but it does not yet read
`scripts/supervisor/quota.sh` — agent-tui#49, see `docs/SPEC.md`'s "Known
defects."

### Glyph gallery, with Nerd Font support

Every lane state against every candidate glyph, flagged where it needs a
Nerd Font's Private Use Area glyphs to render as intended (as opposed to
tofu). **Glyph sets 1 (Signal) and 5 (Nerd Font) are the keepers** — Jon
judged all five original candidates live against a running rail, not the
gallery in isolation, and the other three were deleted outright rather than
merely deprioritised.

**Status:** shipped as a pane inside the one persistent shell (`[f4]`); the
two kept glyph sets are live in the rail today.

### Chat with threads, live

"See the agents talking" — rendered as live sessions side by side, not a
literal agent-to-agent channel: research against the Agent Client Protocol
(ACP) confirmed it is client↔agent only, with no agent-to-agent channel of
any kind. So "watch the agents talk" can only honestly mean every live
session's transcript rendered together, which requires an application shell
capable of showing more than one thing at once.

**Status:** built and tested standalone (`internal/chat` on
`origin/lane/20-chat-threads`, unmerged) — `chat.Source`, a fixture
implementation, and two layouts (thread list + transcript; multi-pane tail
with focus). Explicitly not wired into `cmd/keelson`'s shell; its own commit
message says adding a fifth flag-selected screen "would have repeated that
a fourth time," which was the same defect the shell (agent-tui#38) was
built to fix — chat still needs to be composed into it as a pane the same
way board/cost/gallery now are. Not on `main` as of `b00db9b`.

### Knowledge / memory viewer

Read-first, layered over the estate's existing memory vault rather than
owning a second store. **Status:** not started. No package or branch exists
for this as of `b00db9b`.

### AgentBox sandboxes

Spinning up isolated environments for harnesses. **Status:** not started.

### As close to 1:1 with the hill90 web harness as possible

Stated as a north star for how the pieces above should ultimately compose
and feel. **Status: currently unfalsifiable.** There is no access to the
web harness from the estate to compare against, and this repo has never
claimed to measure the comparison. This is not a claim the product currently
meets or fails — it is simply not checkable today. (Grounding review,
2026-08-16.)

## Non-negotiable constraints, from measurement not taste

These come from `README.md`'s existing "Design constraints" section and are
carried forward here because they bound every feature above:

- **It renders in its own window; it never injects panes into live ones.**
  Measured on a real fixture: injecting a fixed-width pane into a tmux
  window changed two lanes' classification, because the supervisor reads
  panes to determine state. A sidebar built that way corrupts the state it
  is trying to display.
- **Acceptance for any renderer: `lanes.sh` output is byte-identical with
  the app running and not running.** `scripts/verify-lanes-unaffected.sh`
  is the checked proof of this.
- **Every state the supervisor emits must be nameable** — a lane state with
  no glyph must not silently read as idle.
- **Single static binary**, Mac and Linux today, Windows eventually, no
  runtime to install.
- **Everything behind adapters**, so a piece can be enabled, disabled, or
  removed without touching the others. See `AGENTS.md`'s adapter table.

## What "done" looks like for this product

One process. A persistent rail always visible. The other views — board,
cost, gallery, chat, and whatever comes after — as panes inside that one
process, not quit-and-relaunch screens behind flags. The anchor feature
carrying all four of its verbs, safely. **The first half of this — one
process, a persistent rail, board/cost/gallery as panes — shipped**
(agent-tui#38, PR #43, `b00db9b`). What remains: attach/detach, chat, and
the three defects agent-tui#49 found by driving the shipped shell (bare
launch fails closed instead of degrading; the board and cost panes fail
closed rather than helpfully when navigated to without their own
prerequisites) — see `docs/SPEC.md` for exactly how far the code is from
the rest of this intent today. This is intent, dated 2026-08-16.
