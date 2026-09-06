# 2026-09-06 — scoped one-push merge exception for the knowledge architecture run

**Status:** decided, scoped, one-push only. Not a standing change to
`docs/orientation/conventions.md`'s merge protocol — see that file for the
rule this exception temporarily departs from, and read this doc as the
record of the departure, not a replacement for it.

## What this run does differently, and why it's allowed to

The normal path (`docs/orientation/conventions.md`): a branch is
`dispatch/<id>`, created by the estate; one independent review per PR; the
gate (`estate merge`) joins authorship through the dispatch ledger and
refuses any hand-named branch structurally, because authorship for a
hand-named branch cannot be established at all today (`agent-estate#940`).

This run's three lanes work from `knowledge/lane-a`, `knowledge/lane-b`,
`knowledge/lane-c` — hand-named branches, not `dispatch/<id>` — because the
three lanes are long-running `cdsp` worker sessions in tmux, not individual
`estate dispatch` turns, and the coordinating Director (a separate
supervising session, not the estate's own dispatch gate) is doing the
merge decision by hand for this run only. Jon authorized this explicitly
on 2026-09-06 (approval of the reconciled plan v2,
`/tmp/knowledge-plan-review.hybPxD/knowledge-architecture-plan.md`,
including this scoped one-push merge exception) — see
`run/execution-plan.md`'s own header line for the citation trail.

## What still holds, unchanged, inside the exception

- **Workers never merge.** The Director merges via `gh pr merge --squash`
  only when an independent cross-lane `APPROVE` exists for the exact
  current head SHA and required checks are green at that SHA — the same
  substantive bar `estate merge` enforces structurally, applied by hand
  instead.
- **No self-review.** Cross-lane reviews are fixed for this run: A reviews
  C, B reviews A, C reviews B — never a lane reviewing its own PR.
- **One fix pass per PR.** A second failed review closes the PR and files
  what remains, same as the normal convention.
- **A review verdict is still a structured, attributable comment:**
  `Verdict: APPROVE` or `REQUEST CHANGES` + specifics, `Review-Lane: <lane
  id>`, `Reviewed-SHA: <exact head sha>` — the hand-merge substitutes for
  the ledger-joined verdict cross-check `internal/gate` performs
  automatically, not for the verdict's own shape.
- **PR body carries `Author-Lane: <lane id>`** (lane id =
  `agent-estate:<window index>`) — the same authorship-attribution goal
  `dispatch/<id>` branch naming exists to serve, restated in the PR body
  since the branch name can't carry it this run.

## Why this is scoped to one push, not adopted going forward

The exception exists because this run's cast (three long-lived worker
lanes plus a human-supervised Director) doesn't fit the dispatch-per-turn
shape `estate merge` was built to gate. It does not argue that hand-named
branches or hand-run merges are safe in general, and it does not change
`agent-estate#940`'s refusal for any PR outside this run. The next time
this shape of run happens, re-decide it explicitly rather than citing this
doc as standing precedent — if the Director/lane pattern recurs often
enough to be worth a permanent path, that's a design change to
`docs/orientation/conventions.md` and `internal/gate` itself, not an
extension of this exception.

## References

- `run/execution-plan.md` — the plan this exception is drawn from
  (Cast, Review and merge protocol sections)
- `docs/orientation/conventions.md` — the standing merge protocol this
  exception departs from, for exactly this run
- `agent-estate#940`, `agent-estate#980` — why the gate refuses a
  hand-named branch, and why `estate merge` decides rather than merges
