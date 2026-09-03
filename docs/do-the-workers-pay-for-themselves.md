# Do the workers pay for themselves?

`Answered 2026-09-03 from the dispatch ledger. Regenerate the numbers with the
queries at the bottom; do not trust these once the ledger has moved.`

Jon asked this on **2026-08-12** and it went unanswered for three weeks:

> Are the workers providing value or are they token waste? Does the loop
> exist because an AI was determined to make it work?

The second question is the sharper one. This answers both from one night's
data, and the answer to the second is not flattering.

## What ran

52 dispatched turns. 48 completed, 4 still in flight at the time of writing.
**2.8 hours of agent time.** Median turn 195 seconds; the longest 1,098.

**48 of 52 were reviewers. 4 were workers.** That is the first finding and it
was not the intended design: `chain_of_command=director_then_supervisor_then_workers`
(`it-7d6902ac9a42ab55`, hard) describes a Director that dispatches
implementation. Tonight the Director wrote the code itself and dispatched
almost nothing but critics.

## Did they earn it

Of 44 reviews that reached a readable verdict:

| verdict | count |
|---|---|
| REQUEST CHANGES | 28 |
| APPROVE | 11 |
| COULD NOT DETERMINE | 1 |
| unreadable | 4 |

**64% found something.** Not style notes — every one of these was reproduced
before it was acted on:

- `estate authored` let any caller forge authorship: name a decoy, pass your
  own lane as reviewer, get "may merge". Three seats found it independently.
- The merge gate never read what the review *said*; a REQUEST CHANGES
  satisfied it exactly like an approval.
- The gate judged an issue the **caller** named, never the pull request, so a
  clean author/reviewer pair on unrelated work could authorise any merge.
- Teardown destroyed work a turn had **committed** — the tidy agent lost more
  than the messy one.
- Removing the function keys orphaned three panes, reachable by neither
  keyboard nor mouse.
- The path resolver could adopt an **unrelated repository's** tick log and
  present it as the Director's record.

Five of those are in the merge gate alone, found across five rounds. Each
round's fix introduced or exposed the next. **No round found nothing.**

## The uncomfortable half

**Every one of those defects was mine.** The reviewers did not catch a
worker's bad code; they caught the Director's. On the artifact-validation
rule alone, five successive designs were defeated in turn — `"null"`, then
`"working on it"`, then `"read/write path unclear"`, then `"AGENTS.md"`, then
`touch go.work`. Every defeat came from a seat. None came from the loop
inspecting itself.

That is exactly what the loops research predicted:

> loop quality tracks the VERIFIER, not the loop

So: the workers pay for themselves, and it is not close. 2.8 hours of agent
time against defects that would each have shipped a gate that could be talked
past. But the honest framing is not "the workers are productive" — it is
**"the author has a high defect rate and the reviewers are the only thing
catching it."**

## And the second question

*Does the loop exist because an AI was determined to make it work?*

Partly, yes, and the ledger says so. The 13 review seats on one pull request
are not a healthy signal — they are one change being iterated because it was
in front of me. The stop condition fired on this loop tonight for exactly
that: three consecutive ticks naming the same pull request as their artifact.
A loop that produced no artifact would have looked stalled sooner; this one
produced *revisions*, which is harder to see and was caught by a rule a
reviewer made me write.

The loop is worth keeping. The evidence that it is worth keeping is that it
stopped itself.

## Regenerate these numbers

```
python3 - <<'EOF'
import json, collections, datetime, re
rows=[json.loads(l) for l in open('~/.local/state/estate/ledger.jsonl'.replace('~','/Users/jon'))]
# turns: group by id, pair dispatched with its terminal record
# verdicts: regex '(?m)^\s*VERDICT:\s*(APPROVE|REQUEST CHANGES|COULD NOT DETERMINE)'
EOF
```

Every figure above is a count over `~/.local/state/estate/ledger.jsonl`, which
is append-only and local. It is not authenticated: it records what the estate
believes happened, which is why the gate that reads it refuses to treat it as
proof of anything.
