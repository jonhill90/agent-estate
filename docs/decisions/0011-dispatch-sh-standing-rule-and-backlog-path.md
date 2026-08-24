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
