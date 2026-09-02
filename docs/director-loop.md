# The Director's loop

`Verified 2026-09-02.`

The Director runs on a cron inside the Claude Code ecosystem — never `launchd`,
never `crontab`, never a shell script on a timer (brief §10, and
`it-7d6902ac9a42ab55`, hard). Today that is a `CronCreate` job, which is
**session-only: it dies when the session does.** This file exists so the loop
can be restarted from a cold session without reconstructing it from memory.

## The prompt

Recreate with `CronCreate`, `*/3 * * * *`, recurring:

```
Director tick.

1. Run `go run ./src/estate tick check` FIRST. Exit 1 means the loop is
   stalled: stop ticking, escalate per brief §6, and do nothing else this
   tick. Exit 2 means the record is unreadable — that is not clean, treat it
   as a stall. Exit 0 means continue.
2. Read docs/director-brief.md §3 and docs/phase-plan.md.
3. Advance exactly one phase item. Never work a menu. "I did not advance it,
   and why" is a legitimate result.
4. Record it: `go run ./src/estate tick record <phase-item> [artifact]`.
   Omit the artifact when there was none — do not invent one, and do not
   record "" to dodge the stop condition.
5. End by stating what a human can now do that they could not before, naming
   which of src/tui or src/estate moved.
```

## Why step 1 is step 1

`estate tick check` is a gate that fails closed, and until this file it had no
caller. This repo has a name for that: *a tool that fails closed and that
nothing calls is a documentation rule with a binary attached.* The stop
condition only binds if the loop consults it **before** deciding it has work,
not after — a tick that has already talked itself into a task will not stop.

## Interval

Adapt it to state, per brief §3:

| state | interval |
|---|---|
| advancing a phase item | 3 minutes |
| blocked on operator review, nothing else unblocked | widen; a wakeup with nothing to react to manufactures make-work |
| stalled per `tick check` | stop, escalate |
| budget at ~10% remaining | cancel the cron; schedule one that fires when usage returns (`it-fb0cfce397677cb3`, hard) |

Widening is a judgement the Director makes and announces, not a thing it waits
to be told. Restoring 3 minutes when work unblocks is the same judgement.

## What is not verified here

That the cron survives a session restart — it does not. Replacing this stopgap
with real loop and graph engineering is Phase 6 of `docs/phase-plan.md`, and
until then a dead session means a dead loop with no external alarm.
