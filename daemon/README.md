# supervisord

The supervisor as a single Go binary. Replaces the shell/tmux control plane.

## Why

Every expensive failure on 2026-08-21/22 came from one decision: driving
agents by typing into tmux panes and screen-scraping the result.

- `/clear did not blank the screen -- #494 was NOT dispatched` (killed 4 review attempts)
- lanes claimed and renamed but never given their brief
- a brief typed into a prompt box, never submitted (#414)
- a task stamped `complete` while its process still ran (#488)
- panes wedged so hard that `C-u` would not clear them

Those are not bugs in the guards. They are what happens when delivery is
INFERRED from pixels instead of OBSERVED from a process.

## What changed

| | shell supervisor | supervisord |
|---|---|---|
| transport | `tmux send-keys` + screen scrape | `claude -p --output-format json` subprocess |
| delivery proof | pane text matched a regex | process exit + parsed JSON result |
| terminal stamp | any of 4 components could write one | only the code that observed the exit |
| timeout | stamped `failed` | left NON-terminal — unknown is not failed |
| memory | agent might read the vault | vault index is PREPENDED to every brief |
| concurrency | one transition per tick | bounded pool, cores-2, capped 8 |
| language | 44,794 lines of bash | ~700 lines of Go |

[Could not measure 2026-08-23: this row's scope (which files each side
counted, and on which date) isn't stated, so the original 44,794/~700
figures can't be directly reproduced. Measured today instead, for
context, not as a replacement: `find . -name '*.sh' -not -path
'./.git/*' | xargs wc -l` over the whole repo totals 52,121 lines;
`scripts/supervisor/*.sh` alone totals 21,400. `find daemon -name '*.go'
! -name '*_test.go' | xargs wc -l` totals 3,021 lines — well past "~700",
consistent with the daemon having grown substantially since this table
was written (codex adapter, budget, ciflake, sendmsg all landed after).]

## Use

```
supervisord status                                  # ledger counts, read-only
supervisord run   -task ID -brief FILE [-cwd DIR]   # one task, proven outcome
supervisord batch -file jobs.jsonl [-workers N]     # many, concurrently
```

`jobs.jsonl` is one JSON object per line: `{"task":"t1","brief":"/path/b.md","cwd":"/repo"}`.
Omit `lane` and it derives a unique one — the schema enforces one task per lane.

`$AGENT_MEMORY_VAULT` must be set. A missing vault is a hard error: starting
blind has to be loud.

## Verified, not asserted

```
$ supervisord run -task proof-b69335 ...
elapsed: 4.181s
OUTCOME: delivered and complete (session=eafe2738-7c85-47b3-bfba-4275f287ce23 turns=1)

$ supervisord batch -workers 4
batch: 4 ok, 0 failed, wall 2.826s     # sum of parts 10.6s => genuinely parallel
```

[Corrected 2026-08-23: this transcript predates the spend-cap feature
(#499). `daemon/cmd/supervisord/batch.go`'s current format string is
`"batch: %d ok, %d failed, wall %s, spend $%.4f of $%.2f cap\n"` — a
real run today prints spend/cap figures this pasted line does not show.
Not re-run here (a real `supervisord batch` invocation shells out to
`claude`/`codex`, which this pass avoided); the format-string mismatch
is confirmed from source, not by re-running the command.]

Two bugs were found by RUNNING it, and both are recorded in source comments
rather than quietly fixed:

1. `tasks.lane REFERENCES lanes(lane)` — the first real dispatch died on a
   foreign key. `EnsureLane` fixes it.
2. `UNIQUE(tasks.lane)` — a lane holds at most one task, so concurrent jobs
   need distinct lanes. "One agent per lane" is the real model.
   [Corrected 2026-08-23: the actual constraint, in both `core.py` and
   `ledger_test.go`'s schema mirror, is `CREATE UNIQUE INDEX
   one_open_task_per_lane` — a partial index over OPEN tasks, not a blanket
   `UNIQUE(tasks.lane)` (a lane accumulates many tasks over its life; only
   one may be open at once). The behavioral claim above ("a lane holds at
   most one task [at a time]") is what's true; the literal SQL name is not.]

The #488 write-once guard is mutation-checked in both directions: remove it and
`TestFinishRefusesToRestampTerminal` goes red; restore it and the suite passes.

## `cmd/ciflake` — is a CI verdict from this repo trustworthy?

Not part of the daemon; it lives here because this is the repository's Go
tree. It answers the question agent-supervisor#461 asks, with a number:

```
go run ./cmd/ciflake -runs 120            # markdown, ready to paste into an issue
go run ./cmd/ciflake -runs 40 -logs=false # skip per-failure log reads
```

It reports two things that are easy to conflate:

- **failure rate per shard** — how often a shard job went red. High is not
  the same as flaky; a branch with a real defect fails its shard on every
  attempt, and that is CI working.
- **ambiguity rate** — of the (commit, shard) cells executed more than
  once, how many returned *disagreeing* verdicts for the same unmodified
  tree. That is the figure #461 is about, because it is what makes a
  regression and a runner artifact indistinguishable.

Measured over 120 runs (2026-08-21 → 2026-08-22), shard 3 had the highest
failure rate and **zero** ambiguity — every one of its failures reproduced
on rerun. Reporting only the first table would have called it the worst
shard; it was the clearest signal in the set.

The jobs endpoint is queried with `filter=all` on purpose: its default
returns only the latest attempt, which drops every rerun — and a rerun is
the only place a disagreeing verdict can be seen. A test pins that, because
getting it wrong reports a repository as stable precisely when it is flaky
enough to be re-run.

## Not done yet

- ~~codex/copilot adapters (the interface is there; only claude is
  implemented)~~ [Corrected 2026-08-23: `daemon/internal/agent/codex.go`
  (233 lines) is a real, verified-against-the-shipped-CLI adapter, wired via
  `-harness codex` in `daemon/cmd/supervisord/main.go` (#497/#499, landed
  2026-08-22). Copilot still has no adapter — `grep -rn copilot
  daemon --include='*.go'` outside comments returns nothing.]
- per-agent spend caps [Corrected 2026-08-23: `daemon/internal/budget`
  landed (#499) and `supervisord batch` wires a `budget.Tracker` through
  `dispatch.Gates` — but it is one shared tracker for the whole batch
  (`-budget-usd`/`-budget-window` flags), not a cap scoped per individual
  agent, so this item is still open in the sense originally meant.]
- blocked-on-permission-prompt as a first-class state
- the review-independence gate
