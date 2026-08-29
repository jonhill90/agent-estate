---
type: Decision
description: Full detail behind AGENTS.md's "Merging PRs you did not author" section for tui/ -- the shared-login self-review problem, the Verdict/Review-Lane/Reviewed-SHA comment convention, and the blank-Review-Lane self-approval bypass cmd/mergepr had to close.
generated:
  at: 2026-08-29T00:00:00-04:00
---

# Merging a `tui/` PR you did not author

`AGENTS.md`'s "Merging PRs you did not author" section states the convention
and the command; this doc is the incident history behind it.

Every agent lane working this repository pushes through the same shared
GitHub login. `gh pr review --approve` is refused as self-review regardless
of who is actually asking, so a real cross-lane review has to be recorded
another way: a reviewing lane posts a plain PR comment, not a GitHub review
object, carrying `Verdict:`/`Review-Lane:`/`Reviewed-SHA:`, and the PR's own
body states `Author-Lane:`.

**`cmd/mergepr` exists because a tool nobody is told to use is exactly how
`agent-tui#107` happened** — a comment-verdict gate merged by its own
author, unreviewed, within minutes, the second confirmed instance of that
anti-pattern after `jonhill90/skills#255`'s own. `agent-tui#109` is the
issue that recorded the gap: `cmd/prverdict` existed as a manual pre-check
but nothing forced a caller to run it before merging by hand. `cmd/mergepr`
closes that gap by chaining the CI gate and the comment-verdict gate itself
and refusing to call `gh pr merge` unless both pass — modelled directly on
`agent-supervisor`'s own working pattern (`scripts/supervisor/merge-pr.sh` +
`ci_gate.py`).

**`390c99a` (`agent-tui#113`) fixed a blank-`Review-Lane:`-trailer
self-approval bypass** in `internal/prverdict`'s gate: a same-lane author
posting a comment with an empty `Review-Lane:` value and a real head SHA on
the next line was previously resolved as `approved`, because the
post-colon regex's greedy whitespace consumed the newline and captured the
next line's text instead of an empty string. It now resolves to `unknown`
with an explicit "no `Review-Lane:` trailer" reason — see
`internal/prverdict`'s own `BlankReviewLaneSelfApprovalBypass` regression
test.

`internal/prverdict` is a Go port of `jonhill90/skills#255`'s
`pr_verdict.py`, itself ported from `jonhill90/agent-supervisor`'s
`verdict.py`/`verdict-independence.sh` — Go rather than a second-language
copy of `skills#255`'s Python, per this repo's "Go, not shell, for new
code" convention.

**Not wired into CI, deliberately.** This repository's CI
(`.github/workflows/ci.yml`) builds, vets and tests every push and PR; it
never merges one — merging is always a separate command an operator or
agent lane runs directly, outside any workflow. There is no merge-time CI
job to attach this gate to without inventing one that does not otherwise
exist. Nothing on GitHub's side stops a caller from running `gh pr merge`
directly instead and skipping both gates entirely — the same residual
`merge-pr.sh`'s own doc comment states for `agent-supervisor`.
