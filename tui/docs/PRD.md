---
type: PRD
description: What agent-tui is for, in Jon's own framing -- not the technical design.
generated:
  at: 2026-08-23T13:43:05-04:00
---

# PRD — agent-tui

**What this document is:** what the product is FOR, in Jon's own framing.
Intent, not status — for what has actually shipped, read `README.md`; for
the gap between the two, read `docs/SPEC.md`'s "Gap between intent and
code" section. Every dated claim below is checked against `origin/main`
`b00db9b`, **verified 2026-08-16.** The product is named the Estate
(binary `estate`, decided 2026-08-23, superseding `steading` — see
`AGENTS.md`'s naming note for the full history and reasoning).

**First full re-verification, 2026-08-23 (estate-loop/b-docs-stale sweep,
pass 2, against `56513a2`) — never previously touched by a truth pass.**
This file is ~40 commits and a week behind its `b00db9b` stamp; every
per-feature "Status" block below was checked against the current tree, not
against another doc. Corrections are inline, dated, next to what they
replace, per this repo's own "Dated claims" convention — the paragraph
above is kept as the original baseline.

## The one-line framing

A real terminal application — like Claude Code, Codex CLI, or `pi` — for
managing and watching the agent estate. **Not** a renderer, **not** four
screens behind launch flags. Source: `NOTEBOOK-jon-directives.md`, "What he
is building — the TUI."

## Why this exists

The estate (`agent-supervisor`) already runs and reasons about lanes,
sessions, cost, and tasks. Nothing renders it as a single coherent
application a human can sit in front of. The Estate is that surface: it
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

**Status update, 2026-08-23 (pass 2):** `agent-supervisor#189` is closed
(fixed by `agent-supervisor#202`, merged 2026-08-16) — the supervisor-side
prerequisite this paragraph names now exists. `attach`/`detach` are still
not restored on the agent-tui side (`grep -rn "\.Attach(\|\.Detach("
--include='*.go' .` outside test files: zero matches, re-checked against
`56513a2`) — still half-shipped, but the remaining half is agent-tui-side
work now, not a wait on the supervisor.

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

**False as of `56513a2`, corrected 2026-08-23 (pass 2).** agent-tui#49 is
closed; this defect is fixed. `resolveLedgerSource`
(`cmd/estate/board.go`) now auto-discovers and stages a copy of the live
ledger when `-ledger`/`$AGENT_TUI_LEDGER` is unset — the "unavailable"
render now only fires when discovery genuinely finds nothing, not merely
because the flag was omitted. See `AGENTS.md`'s "Known defects" for the
fix evidence.

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

**False as of `56513a2`, corrected 2026-08-23 (pass 2).** agent-tui#49 is
closed; this defect is fixed. `internal/cost/quota.go` now shells
`quota.sh` out via `QuotaRunner`/`ExecQuotaRunner`, wired from
`cmd/estate/main.go`'s `resolvedQuotaBin` — `grep -rn "quota.sh"
--include='*.go' .` now returns matches throughout `internal/cost` and
`cmd/estate` (zero matches when this status was written).

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

**Status:** shipped as a pane inside the shell (`[f6]`, agent-tui#20) —
`chat.Source`, a fixture implementation (`chat.FixtureSource`), and two
layouts (thread list + transcript, with a synthetic unified-feed thread;
multi-pane tail with focus). Scrollable, not clipped: the thread list and
the selected/focused transcript are `bubbles/viewport`-backed with an
always-visible "hidden content" indicator, the fix agent-tui#29 already
paid for once. What remains: a live `chat.Source` — no lane in this estate
runs on a structured transport (`acp`/`pi-rpc`) yet, so every thread shown
today is visibly synthetic fixture data, not a real transcript.

**False as of `56513a2`, corrected 2026-08-23 (pass 2).** The "what
remains" line is no longer accurate. `chat.ClaudeCodeSource`
(agent-tui#99, commit `5997399`) reads real Claude Code CLI session
transcripts and is what `cmd/estate` wires in via `FallbackSource`,
falling back to `FixtureSource` only when genuinely unconfigured — threads
shown today are real, not synthetic, whenever a Claude Code project
directory resolves. Sending is also built (agent-tui#104, commit
`6942926`): `chat.Sender` is implemented over `session_send`
(`agent-supervisor#509`). Chat is further a multi-participant room with
`@`-mention addressing (agent-tui#114, commit `a0ad626`), not just
one-thread-per-lane. What remains now: nothing structural — see
`docs/SPEC-shell.md`'s S7 for the fuller history.

### Knowledge / memory viewer

Read-first, layered over the estate's existing memory vault rather than
owning a second store. **Status:** not started. No package or branch exists
for this as of `b00db9b`.

**False as of `56513a2`, corrected 2026-08-23 (pass 2).** `internal/knowledge`
exists and is wired as `PaneKnowledge` (agent-tui#87, commit `922400b`) —
reads `$AGENT_MEMORY_VAULT`'s `agent/index.md` + `agent/facts/<slug>.md`,
progressive disclosure. Read-first, no write path, matching the "layered
over" framing above. Status is now shipped, not not-started.

### AgentBox sandboxes

Spinning up isolated environments for harnesses. **Status:** not started.

**Partially stale as of `56513a2`, corrected 2026-08-23 (pass 2).** The
container driver itself is still not started — no code exists anywhere in
this repo, `agent-supervisor`, or `AgentBox` that runs a supervised agent
inside a container (`docs/SPEC-agentbox-execution-mode.md` is the design
brief for that, not an implementation). What has shipped since this line
was written: the *interface* a driver would plug into —
`internal/session/execution_mode.go`'s `ExecutionMode`
(`local`/`container`) and `AddWithMode` — plus the Agents view's MODE
column (`internal/agents.modeFor`), both from `docs/SPEC-shell.md`'s S12.
`AddWithMode(..., ExecutionContainer)` returns `ErrContainerNotImplemented`
rather than silently faking a local subprocess, so "not started" is still
the honest read for the actual sandbox capability — only the seam it will
eventually plug into now exists.

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
carrying all four of its verbs, safely. **One process, a persistent rail,
board/cost/gallery/chat all as panes — shipped** (agent-tui#38 PR #43,
agent-tui#20). What remains: attach/detach, a live chat transport, and
the three defects agent-tui#49 found by driving the shipped shell (bare
launch fails closed instead of degrading; the board and cost panes fail
closed rather than helpfully when navigated to without their own
prerequisites) — see `docs/SPEC.md` for exactly how far the code is from
the rest of this intent today. This is intent, dated 2026-08-16.

**Stale, corrected 2026-08-23 (pass 2).** "One process, a persistent rail"
is itself now the outdated architecture — the fixed left column is
`internal/nav`'s sidebar (`docs/SPEC-shell.md` S1-S3), with the rail
reached as a routed pane. Of the "what remains" list: all three of
agent-tui#49's defects are closed (see the Kanban board / cost panel
corrections above and `AGENTS.md`'s "Known defects"), and chat has a live
transport, sending, and multi-participant rooms (see the Chat correction
above). Only attach/detach genuinely remains, and only on the agent-tui
side — the supervisor-side blocker (`agent-supervisor#189`) closed
2026-08-16. What "done" looks like for the anchor feature specifically has
not changed; everything else in this list has been reached.
