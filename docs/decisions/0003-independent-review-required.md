---
type: Decision
description: Decision record -- `merge-pr.sh` refuses to merge a PR whose author and reviewing lane resolve to the same identity or whose verdict can't be read, enforcing independent review at the merge gate itself, not only at dispatch.
generated:
  at: 2026-08-24T21:18:37-04:00
---

# 0003 — Independent review is enforced at merge, not just at dispatch

`2026-08-15` (agent-supervisor#179/#184/#196/#198), `Verified 2026-08-20`.

## Decision

`merge-pr.sh` — the only path meant to merge a PR in this repo — refuses
to merge a PR whose reviewing lane and authoring lane resolve to the same
lane identity, or whose verdict cannot be read. This check lives at the
merge gate itself, not only on the dispatch path that assigns reviews.

## Why

Before #179, every guard this estate had against an author reviewing or
merging their own work — `dispatch.sh`'s `--reviews-pr` author exclusion,
the one-review-per-PR convention — sat entirely on the *dispatch* path: it
governed who got *assigned* a review, not what could actually merge. Free
text typed directly into a lane's pane walks around every dispatch-time
guard and reaches `gh pr merge` (via `merge-pr.sh`) directly, because
nothing downstream of dispatch was checking who was asking.

`verdict-independence.sh`'s own header names the incident: a prompt
reading "merge the PR" was found sitting, unsubmitted, in the input box of
the lane that had *authored* PR #168, while that PR's verdict was still
`none`. It did not submit only because of an unrelated defect (#178,
`Enter` not submitting text a previous `send-keys` call had left in the
box) — that is luck, not a guard, and #178's own fix removed the thing
that had accidentally saved this estate once. So the check moved to
`merge-pr.sh` itself — the one place in the system that "cannot be
skipped by habit" (its own header) — rather than staying only where a
prompt could bypass it.

## Why a shared library (`verdict-independence.sh`), not inline logic

The author-lane / lane-relation / verdict computation already existed
once, inside `digest.sh`, built only for *reporting* independence in the
estate's status digest — not for *enforcing* it. Re-deriving the same
computation a second time for enforcement is exactly the shape
agent-supervisor#108 had already cost a day on: two implementations of "is
this the same lane" that silently stopped agreeing the moment a session
was renamed. `verdict-independence.sh` is that one computation, shared by
`digest.sh` (reporting) and `merge-pr.sh` (enforcement); `digest.sh`'s own
copy was deleted rather than kept alongside a duplicate.

## Fail closed

`ci_gate.py` and `verdict-independence.sh` both refuse when they *cannot
evaluate*, not only when they evaluate false: an unreadable verdict, an
unresolved authorship, or a lane-identity comparison that cannot be
determined all refuse the merge, the same instrument `dispatch.sh`'s own
ledger-readability guard uses one step earlier in the lifecycle
(`docs/product/PRD.md`, "fail closed"). This is also why lane identity is
compared with `resolve_lane_relation` rather than bare string equality —
see `AGENTS.md` invariant 9 on why a same-window, renamed-session pairing
still counts as a self-review.

## Verified

`2026-08-20`: `merge-pr.sh`'s header still names #13/#179 and chains
`ci_gate.py` then `verdict-independence.sh`; `verdict-independence.sh`'s
header still cites PR #168 and defect #178.
