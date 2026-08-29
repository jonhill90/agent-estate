---
type: Runbook
description: A merged fix that never reaches the process running it looks identical to an unfixed defect -- check what checkout a "still broken" report was actually measured against before re-diagnosing it as a code defect.
generated:
  at: 2026-08-29T00:00:00-04:00
---

# A merged fix can look identical to an unfixed defect

`AGENTS.md`'s failure-modes section states this lesson in one line and
points here for the incident that produced it.

`agent-supervisor#308` was reported broken three times (#331, #333, then
this same report a third time) with the same live `bash -x` transcript:
`cli.py pr-task` reported as an "invalid choice". Every repair attempt found
`pr-task` already implemented and working on `origin/main` — because it was:
added by #321/`a2d8a80`, before either later PR. The transcripts were real;
the repo was never the thing they were measuring.

The Director runs `scripts/supervisor/` out of the shared checkout at
`/Users/jon/source/repos/Personal/agent-supervisor`, which had fallen 13
commits behind `origin/main` (stuck at `876edb1`, predating #321 entirely)
and was carrying its own uncommitted staged/unstaged changes. A code-level
drift guard — #333's `test_resolve_pr_contributors_subcommands.sh` — can
only check that the tree it runs in is internally consistent; it has no way
to see that a *different* checkout, never pulled forward, is the one
actually executing production traffic.

Before re-diagnosing a "still broken" report against `origin/main` as a code
defect, check what checkout the report was actually measured against and
how stale it is — a green guard and a red transcript can both be telling
the truth at once.
