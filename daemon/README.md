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

Two bugs were found by RUNNING it, and both are recorded in source comments
rather than quietly fixed:

1. `tasks.lane REFERENCES lanes(lane)` — the first real dispatch died on a
   foreign key. `EnsureLane` fixes it.
2. `UNIQUE(tasks.lane)` — a lane holds at most one task, so concurrent jobs
   need distinct lanes. "One agent per lane" is the real model.

The #488 write-once guard is mutation-checked in both directions: remove it and
`TestFinishRefusesToRestampTerminal` goes red; restore it and the suite passes.

## Not done yet

- codex/copilot adapters (the interface is there; only claude is implemented)
- per-agent spend caps
- blocked-on-permission-prompt as a first-class state
- the review-independence gate
