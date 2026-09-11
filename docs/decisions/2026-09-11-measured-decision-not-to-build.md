# 2026-09-11 — measuring a defect class and deciding not to build the check

**Status:** decided, twice tonight, independently. Recorded so "why isn't
there a mechanism for this" has an answer other than "nobody thought of it."

## The pattern

Two separate investigations tonight measured a recurring defect shape,
built and mutation-tested the obvious static check for it, and concluded
the check should not ship — not because building it was too hard, but
because the measurement itself argued against it.

### agent-estate#1380 — a naive dead-code check, measured against five known instances

Five issues (#1210, #1248, #1194, #1348, #1095) all "a mechanism that is
correct, tested, and called by nothing." A naive check — every exported
writer needs a non-test caller — was built for Go and Python and run for
real, mutation-validated first against a synthetic dead function.

4 of 5 were **operational invocation-cadence gaps**: a cron that never
runs, a process that never restarts, an unsatisfiable precondition —
invisible to static analysis, since the code is reachable and nothing in
the *environment* invokes it. Unscoped, the check was ~100% noise; scoped
to actually-maintained files, hits dropped to a handful, but that scoping
needed a human. Retro-run at #1095's own `link_items` (the one genuinely
static case), a correct version would have caught it day one.

### agent-estate#1095 — one empty table, three relations, three different answers

`links`/`conflicts_with`/`supersedes`/`depends_on`, zero rows since the
schema was created a month ago. Investigated whether to build the missing
manual entry point (the schema's own comment: "the views ARE the
deliverable... Jon asked for these five by name," with `conflicts` the
one view actually reading `links`). Decision, differentiated per relation
rather than one verdict for the whole table: `conflicts_with`'s use case
is already served by a different, shipped mechanism
(`internal/knowledge/contradiction.go`'s query-time detector,
recalibrated the same night — see its own record if one exists);
`supersedes` overlaps a vault-layer mechanism that covers a narrower case;
`depends_on` has no consumer at any layer and is the strongest candidate
to eventually retire, not build for.

## Why this is worth a record and not just two comments

Both are exactly the shape Jon's own standing complaint about agents
warns against, run in reverse: the failure mode here isn't inventing a
decision and presenting it as settled, it's the opposite risk — a real,
recurring pattern with two independent measurements behind it disappearing
back into "nobody built a check for this" the next time someone notices
the same five issues and reaches for a linter as the obvious fix. The
measurement already happened. Re-running it from scratch would cost real
time to re-arrive at the same, non-obvious answer: **most of this defect
class is not a code problem a check can see, and the fraction that is
costs more to scope correctly than it catches.**

## What this does not decide

Neither investigation forecloses a differently-scoped check later — #1380
names the exact conditions (Go, `internal/`-only, a real committed hash
history to scope against) under which the one genuinely-static case stays
catchable, and #1095 leaves `depends_on`'s narrowing as a real, separately
scoped option if anyone wants to act on it. This record is "here is the
evidence that made building nothing the right call tonight," not "never
build this."

## References

- agent-estate#1380 — the naive-check measurement, both languages, retro-run
  against #1095's own introducing commit
- agent-estate#1095 — the `links` table investigation, differentiated
  recommendation per relation
- agent-estate#1210, agent-estate#1248, agent-estate#1194, agent-estate#1348
  — the other four "correct, tested, called by nothing" instances #1380
  measured against
- agent-estate#1368 — a related but structurally different case (a
  calibration bug with one clearly-correct fix, not a build-vs-don't-build
  question) considered for this record and left out; see this task's own
  landing PR for why
