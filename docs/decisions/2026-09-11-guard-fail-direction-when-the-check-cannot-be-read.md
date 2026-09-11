# 2026-09-11 — a guard's fail direction when its own check is unreadable: two opposite decisions, both correct, no shared rule

**Status:** decided, twice, in opposite directions. Not a standing rule — the
next guard-author still has to make this judgment call; this record exists so
they know it *is* a judgment call and what the last two turned on.

## The collision

Two guards shipped hours apart, both facing the identical shape of failure —
the underlying observation the guard depends on cannot be read at all (not
"the observed thing is bad," but "nothing was observed") — and picked
opposite defaults:

- **agent-estate#1383** (`internal/gate/mainstatus.go`, merged `93546789`):
  when `gh run list` itself errors while checking whether `main`'s CI is
  green, the merge guard **permits**, labeled `UNKNOWN`, never silently
  as a plain pass.
- **agent-estate#1388** (`internal/quota/quota.go`, head `e2894674`, open,
  not yet merged): when `codexbar usage` itself errors or hangs while
  checking remaining quota, the dispatch guard **refuses**, bounded to 60s
  so the refusal itself is not also a hang.

Read naively, side by side, this looks like the estate arguing with itself.
It isn't — both were argued with real evidence, and the evidence points in
different directions because the two checks are not the same kind of thing.

## What actually distinguishes them

**1. Is the failing check evidence about the action being gated, or an
advisory about something else?**

`estate merge`'s other four conditions (checks green, author ≠ reviewer, an
independent review completed, that review approved) already establish
*this PR's own* safety before condition 5 (main's status) is ever reached.
An unreadable main-status check says nothing about whether those four
conditions hold — it's an out-of-band signal about a *different* branch,
layered on top of a PR that already cleared its own bar. Confirmed by
tracing `gate.Evaluate`'s own control flow: a genuine total-outage-shaped
failure is already caught earlier, by the pre-existing, unchanged,
fail-closed PR-fetch path — condition 5's own fail-open branch is reached
only when everything else about this specific PR already checked out.

Quota has no such fallback layer. The check *is* the resource the
dispatch is about to consume — there is no other condition establishing
"this dispatch is safe to start" that quota's own unreadability leaves
intact.

**2. Which failure direction actually costs more, evidenced, not assumed?**

For the red-main guard: the guarded event (main is genuinely red) measured
2.8% over 8.25 days (7 of 250 runs), zero of them transient. But the same
night this guard was built, the check meant to *detect* that condition hit
two independent TLS handshake timeouts and a four-hour GitHub rate-limit
window — real, not projected. Failing closed on an unreadable check would
have made the guard's own availability a bigger source of blocked merges
than the red-main events it exists to catch.

For the quota guard: a needless refusal costs a few minutes (retry once
`codexbar` recovers). A needless permit can destroy an entire turn's
in-progress work by dispatching into an already-exhausted window — not
hypothetical: agent-estate#264's own cited incident is a cached-quota
guard that read stale-safe and spent a window from $80 to $8. The
asymmetry runs the opposite way from the red-main case.

## Whether this generalizes

**Both of the above, read together, are as close to a test as this record
can honestly offer**: ask whether the unreadable check is advisory about
something the gated action didn't already establish for itself, and ask
which failure direction has real, dated evidence of being worse. When both
answers point the same way, as they did in each of these two cases, the
direction follows. Neither PR states this as a rule, and this record does
not promote it to one — a third case could plausibly fail both tests
ambiguously, and whoever hits that should re-argue it from evidence, not
cite this doc as a tiebreaker.

## References

- agent-estate#1383, agent-estate#1379 — the red-main guard and its own
  incident
- agent-estate#1388, agent-estate#1163, agent-estate#474, agent-estate#436,
  agent-estate#264 — the quota-timeout guard, its incident, and the
  precedent it argues from (agent-estate#474 does not, on inspection,
  contain the line once attributed to it — agent-estate#436/#264's own
  cached-verdict incident is the real, closer precedent; corrected during
  #1388's own review)
- agent-estate#1348 — the still-open branch-identity gap that makes
  `estate merge`'s other four conditions unreachable for most of tonight's
  own PRs; unrelated to this record's own question, but the reason
  `estate main-status` had to exist as a standalone command in the first
  place
