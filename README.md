# agent-estate

One person runs a fleet of coding agents on one Mac. This repo is the machinery
that makes that survivable: a supervisor that decides whether there is room to
start an agent, starts it, and records what happened — and a terminal
application that shows the operator the live state of the estate.

**Everything here is Go.** Shell and Python are not an implementation option.
`src/langguard` enforces that in CI.

```
src/estate      supervisor: pressure gate, append-only ledger, dispatch
src/tui         the terminal UI
src/langguard   fails the build on shell or Python outside reference/
src/notify      sends a message to the operator's Telegram
src/issuemine   distils closed issues into rules worth carrying forward
reference/      the deleted shell and Python supervisor, read-only
docs/product/   PRD (parameters) and SPEC (what is actually built)
docs/tui/       TUI design, with a verification banner on unchecked claims
```

## The supervisor

```
estate pressure                       can this host take more work?
estate dispatch <issue> <brief-file>  run one agent turn, gated and recorded
estate tasks                          latest state of every task
estate inflight                       tasks still occupying a slot
```

An agent turn is a **subprocess** — `claude -p --output-format json` with the
brief on stdin. Delivery is a process exit and a parsed result. Nothing is ever
concluded from what a terminal pane appears to show.

Three limits gate dispatch and all must pass: load per core, free memory, and
lanes in flight. **Every one fails closed** — a limit that cannot be measured
refuses. A turn that timed out or produced unparseable output is recorded
`unknown`, which is *not* terminal: it keeps its slot until something
establishes otherwise. Unknown is not failed.

## The TUI

`src/tui`, entrypoints under `src/tui/cmd/`. See `docs/tui/SPEC.md` — note its
banner: the paths there are current, the behavioural claims predate the move
into `src/tui` and are unverified.

## History

This repo previously carried a supervisor written in shell and Python. It was
deleted on 2026-08-30 and restored under `reference/` as material to read when
recovering a rule it encoded. Recovering a rule means reimplementing it in Go —
nothing under `reference/` is maintained, run, or fixed.

`src/issuemine` scans the closed issue history and finds the ones carrying
durable rules — fail-closed guards, instruments that lie, orderings that
matter. That is the specification input for what still needs building.

## Not built yet

Named so this reads honestly: no merge gate, no reviewer-vs-author independence
check, no worktree lifecycle, no lane view.
