# The Director's loop

`Verified 2026-09-03.`

The Director runs on a cron inside the Claude Code ecosystem — never `launchd`,
never `crontab`, never a shell script on a timer (brief §10, and
`it-7d6902ac9a42ab55`, hard). Today that is a `CronCreate` job, which is
**session-only: it dies when the session does.** This file exists so the loop
can be restarted from a cold session without reconstructing it from memory.

## The prompt

Recreate with `CronCreate`, `*/3 * * * *`, recurring:

```
Director tick.

1. Run `go build -o /tmp/estate-bin ./src/estate && /tmp/estate-bin tick check`
   FIRST -- NOT `go run`. `go run` reports a child's exit code as text on
   stderr (`exit status 3`) and exits 1 itself, so exit 3 (STALLED,
   escalated) arrives indistinguishable from exit 1 (STALLED, unacknowledged)
   -- see agent-estate#968. Believe the exit code of `/tmp/estate-bin`, never
   of a `go run` wrapper around it.
   - Exit 0: continue.
   - Exit 1 (STALLED, unacknowledged): escalate per brief §6, then run
     `go build -o /tmp/estate-bin ./src/estate && /tmp/estate-bin tick
     escalate <phase-item> <where>` naming the phase item and where you told
     the human, and stop -- do nothing else this tick.
   - Exit 3 (STALLED, escalated): this phase item/src head is still stuck
     and a human has already been told. Do not re-escalate the same stall
     every tick -- work something ELSE (a different phase item, or nothing,
     stated plainly) instead of repeating the escalation for its own sake.
   - Exit 2: the record is unreadable -- that is not clean, treat it as a
     stall (escalate, same as exit 1).
2. Read docs/director-brief.md §3 and docs/phase-plan.md.
3. Advance exactly one phase item. Never work a menu. "I did not advance it,
   and why" is a legitimate result.
4. Record it: `go build -o /tmp/estate-bin ./src/estate && /tmp/estate-bin
   tick record <phase-item> [artifact]`.
   Omit the artifact when there was none — do not invent one, and do not
   record "" to dodge the stop condition. **Never put an escalation's
   detail here** (a Telegram link, "told Jon") -- that goes through
   `tick escalate`'s own log, never the artifact field; see
   `internal/tick.RecordEscalation`'s doc comment for why.
5. End by stating what a human can now do that they could not before, naming
   which of src/tui or src/estate moved.
```

**The prompt must not restate the stop condition.** An earlier version ended
with "if the last three entries share phase_item and src_head with artifact
null, stop and escalate." That is the rule `estate tick check` owns, and a
prompt carrying its own copy is how the two drift — the copy was already
wrong within a day, describing a rule an independent review had shown does
not catch the loop it exists to catch. The prompt calls the command and
believes its exit code. Nothing else.

## What the stop condition actually is

Three consecutive ticks that produced no artifact. **Only an artifact clears
it.**

This departs from brief §3's literal wording ("the same `phase_item` and the
same `src_head` with `artifact: null`") deliberately, because that wording
does not catch what §3 says it is for. Both of its equality tests were escape
hatches:

- a loop bouncing `phase-0, phase-1, phase-0` forever, producing nothing,
  never has three consecutive entries sharing `phase_item`;
- `src_head` is `git log -1 -- src/`, the whole tree, so an unrelated commit
  anywhere under `src/` cleared the stall for a phase item that had not
  moved.

Both are the same mistake: treating a signal that merely *changed* as evidence
that *this* work advanced. `phase_item` and `src_head` are still recorded and
still named in the reason — they say what was stuck and where — but they no
longer excuse a stall. The rule is strictly stronger: every log the old form
flagged, this one flags.

## The stall that could not record itself (agent-estate#923)

A stalled tick correctly writes nothing to `docs/tick-log.jsonl` — it
produced no artifact, so nothing else was true to write. But that means the
window never changes, so `tick check` reports `STALLED` on the *next* tick
too, and every tick after that, forever. There was no way to record "I
noticed, told a human, and am waiting" — the state brief §3 itself names
("the clock does not run while you are blocked on operator review") but that
the record had no field for. The only thing that ever broke the loop was a
human overriding the rule in the open, which is the exact judgement call the
rule exists to remove.

`estate tick escalate <phase-item> <where>` fixes this by writing to a
**second, separate** log (`docs/tick-escalations.jsonl`) that `tick check`
reads alongside the tick log. It never counts as an artifact — there is no
artifact field on an escalation entry, and it cannot be smuggled through
`tick record`'s artifact argument either (see step 4 above and
`internal/tick.RecordEscalation`'s doc comment for the exact `://`-shaped
dodge that would otherwise open). It only tells `tick check` that THIS
stall — same `phase_item`, same `src_head` as the most recent tick — has
already been told to a human. `tick check`'s exit code then splits three
ways instead of two: still moving (0), stalled and nobody told (1), stalled
and a human already knows (3). Nothing here lets Stalled itself go back to
false — the window still only moves the way it always has, on a real
artifact recorded since the last tick. Escalating the same stall over and
over is counted, not hidden: `tick check`'s output says how many times, so a
loop escalating every tick reads differently from one that escalated once
and moved to other work.

## Why step 1 builds the binary instead of `go run`ning it (agent-estate#968)

`go run ./src/estate tick check` does not preserve the child's exit code: Go
reports it as text on stderr (`exit status 3`) and `go run` itself always
exits `1` when the child exits non-zero. Through that wrapper, exit 3
(STALLED, escalated) and exit 1 (STALLED, unacknowledged) both arrive as `1`
-- the one distinction the escalation mechanism exists to make disappears at
the exact layer that was supposed to read it. #965's own review was asked to
check every caller of `tick check` including this file, and approved anyway,
because it checked the binary directly rather than the `go run` invocation
the loop actually issues -- the same failure mode this repo names most
often, aimed at itself.

The fix changes the invocation, not `tick check`: build once
(`go build -o /tmp/estate-bin ./src/estate`), then run the compiled binary
and believe **its** exit code. `go build` is incremental -- with nothing
under `src/estate` changed since the last tick it costs a few hundred
milliseconds, not a full rebuild -- so doing it every tick (and before each
of `tick check`/`tick escalate`/`tick record`, since all three lose the same
fidelity through `go run`) is not a meaningful tax on the 3-minute cadence.

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
| stalled per `tick check`, unacknowledged (exit 1) | stop, escalate |
| stalled per `tick check`, already escalated (exit 3) | move to other work; do not repeat the same escalation every tick |
| budget at ~10% remaining | cancel the cron; schedule one that fires when usage returns (`it-fb0cfce397677cb3`, hard) |

Widening is a judgement the Director makes and announces, not a thing it waits
to be told. Restoring 3 minutes when work unblocks is the same judgement.

## What is not verified here

That the cron survives a session restart — it does not. Replacing this stopgap
with real loop and graph engineering is Phase 6 of `docs/phase-plan.md`, and
until then a dead session means a dead loop with no external alarm.
