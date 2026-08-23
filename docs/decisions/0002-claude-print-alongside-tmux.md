---
type: Decision
description: Decision record: `claude-print` dispatch exists alongside the standing tmux lanes rather than replacing them.
generated:
  at: 2026-08-20T07:40:40-04:00
---

# 0002 — `claude-print` dispatch exists alongside tmux lanes, not instead of them

`2026-08-16` (agent-supervisor#171/#215/#274), `Verified 2026-08-20`.

## Decision

A fresh, single-issue, non-review, non-`--pr`-scoped dispatch to `claude`
now defaults to a new `claude-print` lane — dispatch-and-collect over
`claude -p --output-format json`, no tmux pane, no window to watch — via
`dispatch-claude-print.sh`. `--live-pane` opts back into the original
behavior. No standing tmux `claude` lane is ever converted or replaced;
`claude-print` only ever creates a *new* lane, opt-in per dispatch.

## Why

`dispatch-claude-print.sh`'s own header states the measurement directly:
`pi --mode rpc` — the transport agent-supervisor#161 had claimed a lane
onto — was not actually exercisable with the credentials available to
this estate. `pi --list-models` on the host lists only `github-copilot`
and `openai-codex` as configured providers; there is no `ANTHROPIC_API_KEY`
and `pi`'s own `--provider` default (`google`) is also unconfigured. Both
providers `pi` *could* reach map onto harnesses agent-supervisor#171's own
brief had already measured as exhausted — Codex at 100% of its weekly
budget, Copilot at 97.1%. Routing a lane through `pi` under those
conditions would not be a real dispatch; it would fail against an
exhausted quota and look like a transport defect rather than what it
actually was. `claude -p` was the one non-keystroke surface the estate
could actually exercise, because it draws on Claude's own capacity, not a
wrapper's.

## Why not replace the standing tmux lanes

`cli.py`'s comment on `adapter_for_harness` is explicit, and this decision
does not override it: tmux stays the default transport for every existing
lane (`codex`, `claude`) because Jon requires the persistent, watchable
terminals it gives him. `claude-print` is additive — the same posture
`dispatch-pi-rpc.sh` already took for `pi --mode rpc` (agent-supervisor#58
/#160): a new lane, a new `transport` value in the ledger
(`docs/product/SPEC.md` §1), never a conversion of a lane a human is
already watching.

## Why a separate script rather than a branch in `dispatch.sh`

`ClaudePrintAdapter.observe_lane` is a permanent no-op — there is no pane
to poll, no window to rename, no input box that could ever hold unsent
text. `lanes.sh` has nothing to classify for a `claude-print` lane, so the
dispatch path for it does not share `dispatch.sh`'s pane-shape assumptions.
Reuses `claim.sh` and `worktree.sh` unchanged.

## Verified

`2026-08-20`: `dispatch-claude-print.sh`'s header still states the
provider/quota measurement above; `core.py`'s `lanes.transport` CHECK
constraint still includes `claude-print` alongside `send-keys`, `acp`,
`pi-rpc`.
