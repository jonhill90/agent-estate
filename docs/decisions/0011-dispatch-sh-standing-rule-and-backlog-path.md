---
type: Decision
description: dispatch.sh works end to end -- the standing rule for all future dispatch, what retires, and the established (not assumed) legitimate path for the fifteen already-open PRs.
generated:
  at: 2026-08-24T00:55:00-04:00
---

# 0011 — `dispatch.sh` proven; the standing rule, and the backlog's real path, established not assumed

`2026-08-24`. Decided by the director loop
(`estate-loop/director-*.md` — the dispatch.sh end-to-end experiment).
Not implemented here except where noted.

## What was proven, independently re-verified before recording

`agent-supervisor#560` merged at `2026-08-24T00:44:19Z`
(`e5b4f379226115bb8eaadbeca3164aded5f4c7cd`), confirmed by `gh pr view
560 --json state,mergedAt,mergeCommit` directly — not taken on the
report alone. Dispatched through `dispatch.sh` against a real issue
(`#558`, closed automatically on merge), reviewed independently
(`estate:2`), merged through the unmodified sanctioned path with no
`mark-pr-external`, no hand-written ledger row, no pane-write substituting
for dispatch, no bypass. `merge-pr.sh`'s own output: `gate passed`, then
`independence confirmed -- independent -- author lane
as558-readme-test-count-558, reviewer lane estate:2`.

**This settles what three authorship-mechanism attempts
(`#539` twice, `#556`) were aimed at: the gate is not broken.** The
fifteen PRs currently stuck on "author lane unresolved" are a backlog
created by dispatching outside the path the gate understands, not
evidence the gate itself needs a fourth redesign.

## Decision 1 — the standing rule

**All future dispatch, in every repo this estate operates in, goes
through `dispatch.sh` against a real issue. Writing a brief directly into
an already-existing pane is retired as a dispatch mechanism, effective
now.** This is the estate-loop's own habit that created tonight's backlog
— every one of the fifteen stuck PRs traces to exactly that pattern, and
`dispatch.sh` resolving cleanly for `#560`, `agent-dotfiles#320`, and
`skills#278` while every pane-written dispatch sits unresolved is not a
coincidence to explain away, it is the actual, now-measured cause.

**What does NOT retire — the distinction stated explicitly, not left
implicit:** pane-writing remains legitimate for *operational* messages to
a lane already dispatched by other means — a freeze notice, a correction
to an in-flight brief, a wind-down instruction, a standing-rule
announcement like this one. Those are not new dispatches; they manage
work `dispatch.sh` (or a prior dispatch) already started. The line is:
**does this message start a new piece of work with no issue behind it, or
does it manage work already underway?** The first is retired; the second
is not, and pretending otherwise would make it impossible to ever
communicate a freeze, a wind-down, or this rule itself.

**Where this is recorded so a lane actually reads it:** this document,
plus delivered directly to every live `estate:N` pane the same way
`director-freeze-agent-supervisor.md` was — confirmed landed, not just
filed.

## This document broke its own rule, said plainly rather than left for someone else to find

**This PR — `#561` itself, the document recording "always dispatch through
`dispatch.sh`" — was not dispatched through `dispatch.sh`.** Branch
`docs/estate-dispatch-sh-standing-rule`, no `Author-Lane:` trailer,
authored the same way every director decision doc has been all session:
a worktree created directly, committed directly, a PR opened directly.
Confirmed by `gh pr view 561 --json body`, not assumed clean because I
wrote it. **This is the strongest evidence for the rule, not a
contradiction of it** — the habit is ingrained enough to have captured the
document meant to end it.

**Why, honestly, not excused:** the rule as first stated addressed *lane*
dispatch — `dispatch.sh` finds a free `estate:N` pane, claims an issue,
builds a worktree, hands over a brief. **The director has no
self-dispatch mode in that tool, and no `lanes` row to dispatch as
(`agent-supervisor#532`).** Writing this document was never a case of
"the director chose the pane-write habit over `dispatch.sh`" — there was
no `dispatch.sh` invocation shape that fit "the director does its own
analysis and writes its own finding." That is a real, distinct gap from
the one this document's Decision 1 closes, not the same gap wearing the
director's name. Conflating them would let this document claim a fix it
doesn't provide.

**The actual fix, available now, without waiting on `#532`:** the
director stops authoring decision-doc PRs directly. Where a decision
needs to be written up as a document (not just a comment recording an
already-made call), **the director files the issue and dispatches the
writing itself through `dispatch.sh` to a free lane**, the same as any
other task — the lane gets a real `Author-Lane:` trailer, a real
worktree, a real ledger row, because it went through the mechanism that
is now proven to work. The director's role becomes deciding and briefing,
not typing the commit. This document does not retroactively fix itself
under this rule; the next one should not need this section.

## Decision 2 — the fifteen do not retroactively unblock; the real path, established not assumed

**`#560` does not retroactively resolve any of the fifteen.** Its own
mechanism (`dispatch.sh` against a fresh issue) only records authorship
for dispatches made through it; it has no retroactive effect on PRs
already open with no such record.

**Whether `dispatch.sh` can *legitimately re-dispatch* an existing PR —
checked in the code, not assumed:**

`core.py`'s `get_contributor_tasks_for_pr` (`Ledger`, resolution path 5)
reads every `source_kind='pull'` task ever recorded against a PR number —
written exactly when a lane is dispatched with `dispatch.sh ... --pr <N>`
— and **explicitly excludes anything that looks like a review**
(`_task_looks_like_review`, checked against the task's own summary). A
`--reviews-pr <N>` dispatch does **not** populate this table, by design —
its own docstring: "a fix-pass or review dispatched with `--pr <N>` /
`--reviews-pr <N>` writes `source_kind='pull'`... [but review tasks are
filtered]." **A genuine, non-review `--pr <N>` dispatch — a real fix-pass
or follow-up — does populate it, and does resolve `author_lane_for` to
that lane once it exists.** This is not a new or borrowed mechanism; it
is the same one `#560` just proved end to end, used the way it is
documented to be used.

**So: yes, `dispatch.sh` can legitimately unblock a PR in the backlog —
condition is real, dispatcher-recorded work, not a bare attach.** For
each of the fifteen:

- **Where genuine follow-up work exists or can honestly be asked for**
  (and for PRs reviewed hours ago against a head that has since moved,
  "re-verify this PR still holds against current `origin/main`, fix
  anything found stale, report back" is always real work in this repo's
  own convention — never a pretext) — **dispatch it via `dispatch.sh
  <issue> <slug> <brief> --pr <N>`, framed and executed as genuine
  work, never as a review** (a review dispatch does not populate the
  contributor table and would not resolve anything). This is the
  preferred path over `#555`'s fallback wherever it applies, because it
  uses the proven mechanism rather than a human-authorized exception.
- **Where a PR is genuinely complete and there is nothing honest to
  dispatch** (manufacturing busywork to populate a ledger row is exactly
  the gaming-the-gate shape this estate has refused three separate
  times tonight) — `agent-supervisor#555`'s bounded, per-PR,
  human-authorized exception remains the path, unchanged.

**Per-PR judgment, not a blanket rule** — whoever executes this should
assess each of the fifteen individually and say, for each, which path
applies and why, rather than defaulting all fifteen to either option.

## The enforcement point — a statement alone is not enough, measured directly

**Two more PRs joined the backlog after `#560` proved the path**:
`#557` and `#559` (`estate:3`'s `#550` work — one of the two named freeze
exceptions, and it still ended up pane-write dispatched), both APPROVE at
head, both refusing identically. The rule as a sentence in a document does
not stop new backlog from forming; it needs a real enforcement point, per
repo, per cause, not a restated intention:

1. **For lane pane-writes** (what closed `#557`/`#559`'s gap, and every
   `estate:N` PR before them): the correct enforcement point is the
   send mechanism itself refusing to deliver a **new-work** brief (per
   this document's own new-work/operational-message distinction) unless
   it already names a `dispatch.sh`-created issue and worktree. **Named,
   not built here** — the director does not have full visibility into
   every path that delivers briefs to lanes in this estate (stated
   plainly in `agent-supervisor#553`/`0009` already; still true). What
   the director *can* enforce unilaterally, starting now: **every future
   brief the director itself sends that starts new work is preceded by a
   real `dispatch.sh` call** — the director becomes the enforcement point
   for its own dispatches, a behavioral commitment, checkable after the
   fact by whether the resulting PR carries a real `Author-Lane:` trailer.
2. **For director self-authorship** (what `#561` itself is an instance
   of): the fix stated above — the director stops opening decision-doc
   PRs directly and dispatches the writing through `dispatch.sh` instead.
   Same enforcement shape: checkable after the fact, per PR, by whether
   an `Author-Lane:` trailer exists.
3. **Accepted cost, stated rather than hidden**: neither of the above is
   a tooling-level refusal yet — both are the director's own discipline,
   which this document already showed, of itself, is not automatically
   reliable (`#561` broke the rule it was busy writing). A real tooling
   refusal — a CI check at PR-open time, not just `merge-pr.sh`'s
   merge-time catch — is filed as `agent-supervisor#562`, scoped, not
   assigned yet, not implemented here. Until it lands, **the honest state
   is: this is enforced by the director remembering, which has already
   failed once tonight, and the backup is `#555`'s human-authorized
   exception catching what the discipline misses — not a closed loop.**

## Companion rule for existing work — second proof landed, `#543`, and a mistake caught before it shipped

`agent-supervisor#543` merged the same way `#560` did: a genuine fix-pass
dispatched through `dispatch.sh` against a real issue (`#542`), applied
backward to a PR that already existed. The first version of this section
said the companion rule was "an existing issue if one applies, a freshly
filed one if not" — **that was wrong, and reversed before any lane acted
on it.**

**Verified, not assumed:** `#544`, `#551`, `#559` each reference a real
issue (`#540`, `#548`, `#550`) whose `createdAt` genuinely predates the
PR's own `createdAt` — confirmed via `gh issue view`/`gh pr view
--json createdAt`, not inferred from the number appearing in a title.
Those three route through a genuine `dispatch.sh --pr <N>` fix-pass, same
as `#543`.

**The other eleven — `#561`, `#557`, `#553`, `#547`, `#541`, `#537`,
`#536`, `#535`, `#534`, `#533`, `#531` — do not, and the losing argument
is answered, not just noted:**

The case for allowing a freshly-filed issue: the fix-pass lane would do
genuine, checkable verification work, and that work's authorship is real
regardless of when the issue was filed — which is exactly what made
`#543`'s merge trustworthy in the first place, so why should the issue's
birthdate matter more than the work's honesty?

**It loses, on the property that actually makes the existing pattern
sound, not on a technicality:** `dispatch.sh`'s issue-first ordering is
trustworthy specifically because the dispatcher commits to a scope of
work *before* anyone knows what the result will look like — the issue
existing first is what proves the work wasn't shaped to fit a
predetermined, already-known-good outcome. An already-open,
already-independently-APPROVE'd PR is a known-good outcome. Filing an
issue afterward and dispatching a "verification" against it is not blind
in the way a real dispatch is — the fix-pass is checking work everyone
involved already believes is correct, which is a structurally weaker
form of scrutiny than the blind case even when the individual lane
verifies honestly and adversarially. That is the same shape — an
after-the-fact link constructed to satisfy the guard — as the three
mechanisms this estate has already rejected tonight, not a new one;
the fact that the *specific* link this time is a real GitHub issue rather
than a self-run script doesn't change which property is missing.

**`agent-supervisor#555`'s human-authorized exception is the only route
for those eleven.** Stated plainly rather than left implied: this makes
`#555` the answer for most of the current backlog, not a narrow backstop
— eleven of fourteen open PRs, as measured, have no `dispatch.sh` route
at all without reversing the property that makes the tool trustworthy.

## What this does not decide

- Which specific lane dispatches each fix-pass, or the exact brief
  content for each of the fifteen — real scoping work for whoever
  executes this, not fixed here.
- Whether `agent-supervisor#557`'s redesigned dispatch-time mechanism
  is still worth building. Given `dispatch.sh` is proven to work for the
  shape of dispatch this estate actually needs, `#557`'s redesign is
  downgraded from urgent to unnecessary for the authorship problem
  specifically — stated plainly here rather than left to continue by
  momentum, matching the branch of `director-dispatch-test-decision-drafted.md`
  written before this result landed. `#546`, `#553`'s retroactive-evidence
  question, and `#557` itself can close as superseded by this result,
  a lane's own call to make on each issue, not mandated here.
