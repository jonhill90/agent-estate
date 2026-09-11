# 2026-09-11 — measuring a defect class and deciding not to build the check

**Status:** decided, twice tonight, independently. Recorded so "why isn't
there a mechanism for this" has an answer other than "nobody thought of it."

## The pattern

Two separate investigations tonight measured a recurring defect shape,
built and mutation-tested the obvious static check for it, and concluded
the check should not ship — not because building it was too hard, but
because the measurement itself argued against it.

**agent-estate#1380** — five closed/open issues (#1210, #1248, #1194,
#1348, #1095) all described as "a mechanism that is correct, tested, and
called by nothing." The naive check — "every exported writer needs a
non-test caller" — was built for both Go and Python, mutation-validated
against a synthetic dead function, then run for real:

- 4 of the 5 named instances turned out to be **operational
  invocation-cadence gaps** (a cron that never runs, a process that never
  restarts, a gate whose precondition became unsatisfiable) — invisible to
  any static analysis of source, however sophisticated, because the code
  itself is fully reachable; nothing in the *environment* invokes it.
- The naive check, run unscoped, was ~100% noise (false "dead" hits from
  values-as-defaults and dispatch-table entries the AST pass can't see;
  false "live" hits from doc-comment self-mentions). Correctly scoped
  (only files git history confirms are actually maintained), the same
  check's real hit rate dropped to a handful of genuine findings — but
  getting that scoping right required a human reading `git log` and
  tracing an import graph, not something the check could determine about
  itself.
- Retro-run at the one case that *is* this shape (#1095's `link_items`),
  a correctly-built version would have caught it on day one. That is the
  entire positive result: one real class, caught reliably, at real
  engineering cost to scope correctly, against four other cases it could
  never have touched.

**agent-estate#1095** — `links`/`conflicts_with`/`supersedes`/`depends_on`,
zero rows since the schema was created a month ago. Investigated whether
to build the missing manual entry point (the schema's own comment: "the
views ARE the deliverable... Jon asked for these five by name," with
`conflicts` the one view actually reading `links`). Decision, differentiated
per relation rather than one verdict for the whole table:
`conflicts_with`'s use case is already served by a different, shipped
mechanism (`internal/knowledge/contradiction.go`'s query-time detector,
recalibrated the same night — see it own record if one exists);
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
