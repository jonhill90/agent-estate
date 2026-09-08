# agent-estate

One person runs a fleet of coding agents on one Mac. This repo is the machinery
that makes that survivable: a supervisor that decides whether there is room to
start an agent, starts it, and records what happened — and a terminal
application that shows the operator the live state of the estate.

**The app is Go.** Shell and Python are not an implementation option for it,
at any size, for any reason. That rule covers the app; *tooling* — a lint, a
migration script, the vault validator — may be shell or Python (Jon,
2026-09-07). `scripts/` is where that tooling lives.

```
src/estate      supervisor: pressure gate, append-only ledger, dispatch,
                knowledge index, corpus projection, distilled rules
src/tui         the terminal UI
src/notify      sends a message to the operator's Telegram
src/issuemine   distils closed issues into rules worth carrying forward
src/progress    progress reporting
scripts/        tooling, not app: docs-lint (a CI gate), evidence, knowledge
reference/      the deleted shell and Python supervisor, read-only
docs/plan/      the plan, and PLAN.md which says which plan governs
docs/canonical/ living specs; docs/historical/ what they superseded
docs/product/   PRD (parameters) and SPEC (what is actually built)
docs/tui/       TUI design, with a verification banner on unchecked claims
```

Start at **[`docs/plan/PLAN.md`](docs/plan/PLAN.md)**. It names what governs,
what is a child of it, and what is superseded — and a brief handed to a lane
does not override it.

## The supervisor

```
estate pressure                       can this host take more work?
estate dispatch <issue> <brief-file>  run one agent turn, gated and recorded
estate tasks                          latest state of every task
estate inflight                       tasks still occupying a slot
```

An agent turn is a **subprocess** — `claude -p --output-format json`, brief on
stdin. Delivery is a process exit and a parsed result; nothing is concluded
from what a pane appears to show. The turn is still watchable: its output is
teed to a transcript a tmux window tails.

Three limits gate dispatch — load per core, free memory, lanes in flight — and
**every one fails closed**: a limit that cannot be measured refuses. A turn
that timed out or produced unparseable output is `unknown`, which is not
terminal and not failed; it keeps its slot until something establishes
otherwise.

See [docs/orientation/daemon.md](docs/orientation/daemon.md) for the mirror,
the switches, and how each harness differs.

## Knowledge

The estate remembers what the operator has decided, and an agent can ask it:

```
estate knowledge query "should I merge my own PR"
```

Three layers — the corpus is evidence, the vault's facts layer is what binds,
the compiled index is a regenerable view. See
[docs/canonical/knowledge.md](docs/canonical/knowledge.md).

## The TUI

`src/tui`, entrypoints under `src/tui/cmd/`. See `docs/tui/SPEC.md` — note its
banner: the paths there are current, the behavioural claims predate the move
into `src/tui` and are unverified.

## History

A shell and Python supervisor was deleted on 2026-08-30 and kept under
`reference/` as material to read when recovering a rule it encoded — read-only,
never run. `src/issuemine` mines closed issues for the durable rules worth
reimplementing.

## Not built yet

No lane view. No relation proposer — the corpus `links` table has zero rows,
so nothing is recorded as superseding or contradicting anything. The rules
layer covers a fraction of the corpus.
