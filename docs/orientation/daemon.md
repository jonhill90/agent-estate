# The daemon

*Relocated verbatim from the repo root `AGENTS.md` by the progressive-disclosure split (only heading levels and relative link paths were adjusted for the new location). `AGENTS.md` is the index that routes here; this file is the detail.*

The daemon is Go, in `src/estate`: one binary, one append-only ledger, and one
narrowly-scoped tmux caller (`internal/mirror`, added by #1003 — it opens a
read-only viewer window on a turn's transcript, and tmux is never the
transport). Run `go run ./src/estate` with no arguments for the current
subcommand list — it grows, and a list written in prose goes stale between
commits.

**The shell and Python supervisor this section used to index is deleted.**
`git ls-files scripts/supervisor tests/supervisor` returns **0**. Those files
are kept, unmaintained, under `reference/` so a rule can be read in the form
it was once encoded, and git history has the rest. Nothing there is run,
tested, or fixed; recovering a rule from it means reimplementing that rule in
Go. The rules retired along with the CI workflows that ran them, and the
status of each, are in [`docs/historical/ci-rules-retired.md`](../historical/ci-rules-retired.md)
— do not cite any of them as enforced.

## The guards that actually run

Two, both in Go, both failing closed.

| Guard | Refuses | Implemented in |
|---|---|---|
| Host pressure | a new dispatch when the host cannot safely take one — and equally when it cannot measure the host at all, because blindness is not capacity. The token-budget floor is one of its limits, not a separate gate: `internal/quota` refuses at roughly 10% remaining, and a reading older than 20 minutes counts as no reading | `estate pressure`, `src/estate/internal/pressure`, `src/estate/internal/quota` |
| Merge gate | to *allow* a merge unless the PR is open with every required check green at its live head SHA, a dispatched turn is joined to it by BOTH its `dispatch/<id>` head ref and a head SHA the estate itself recorded, the reviewer is a different dispatch from the author, and a parsable APPROVE was actually posted. It decides and prints; the merge itself is still someone else's `gh pr merge` — see the Conventions section and **#980** | `estate merge`, `src/estate/internal/gate` |

`estate dispatch` refuses to start a turn at all when the named harness is
unknown or its binary is not on PATH, when the operator's hard parameters
cannot be read from the corpus, when host pressure refuses, or when the turn
cannot be given a git worktree of its own (`src/estate/internal/isolate` — a
worktree, not a sandbox; read its doc comment for what that does and does not
bound).

Read `internal/gate`'s own package comment before relying on any summary of
it, including this one: it states what the authorship join does and does not
establish, at more length than a table can.

**Nothing else refuses anything.** The collision check, the supervisor lease,
the completion gate, the fix-pass evidence gate and the UI evidence gate were
all mechanisms of the deleted supervisor, and none was reimplemented. What each
of those five rules actually *said* is recorded in
[`docs/historical/ci-rules-retired.md`](../historical/ci-rules-retired.md), along with
`gh-comment-gate.sh` and `mark-pr-external.sh` — read it there rather than
recovering a rule from `reference/`'s source.
