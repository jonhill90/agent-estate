---
type: Decision
description: Extended evidence for AGENTS.md's Invariants 4, 9, and 10 -- the incidents that produced each rule, moved out of the index so AGENTS.md states the rule and points here for the "why".
generated:
  at: 2026-08-29T00:00:00-04:00
---

# Invariant evidence

`AGENTS.md`'s "Invariants" section states each rule in one to three lines and
links here for the incident that produced it. This doc is the detail, not a
second copy of the rule -- read the rule in `AGENTS.md` first.

## Invariant 4 — never address the default tmux socket in a test

`kill-server`, `kill-session`, `kill-window` and `respawn-*` must be scoped
with `TMUX_TMPDIR` and gated by `assert_isolated_tmux` (`tmux-isolation.sh`).
A bare `tmux kill-server` from a lane destroyed the entire live estate three
times in one day, including unrelated sessions belonging to the operator.
The rule is not "never call it" — one test harness calls it sixteen times
safely.

`Verified 2026-08-15` (#185): the guard was extended to session *creation*
too, not just the destructive verbs above — an isolated test that creates a
session without `TMUX_TMPDIR` set was the same class of leak with a
different verb.

## Invariant 9 — lane identity is the string `<session>:<index>`

Not the task, not the worktree, not the window name. A lane that finishes
one task and is dispatched a second, in a different worktree, is still the
*same lane*. This was used to justify treating a review as independent on
2026-08-15 and was wrong: same `<session>:<index>` across a rename is a
self-review, and `verdict-independence.sh`'s `lane_relation` check exists
specifically to catch it — see its own comment on "the same window, renamed
session" (#184/#192/#196/#198). Compare lane ids, never task ids or window
names, when deciding whether two pieces of work were done by the same agent.

## Invariant 10 — a lane identifies itself by matching `worktree_path` to its own `cwd`

Not by asking tmux who it is. `tmux display-message` **without an explicit
`-t`** answers for the session's *currently active* window, not the caller's
own — a background or non-focused pane gets someone else's answer. That
produced six mis-stamped `Review-Lane:` trailers in one day (#187) before the
merge gate's independence check caught them as suspicious self-reviews (see
Invariant 9) rather than silently trusting them.

A brief should never spell this out as a raw command — name `lane-whoami.sh`
(#685), which already picks the right branch (pane vs. pane-less) and never
calls `display-message` with no `-t`. The self-lookup it wraps for the
pane-less branch is `cli.py worktree-lane --path "$(pwd)" --include-reviews`
(`Ledger.get_task_for_worktree(..., include_reviews=True)`). The
`--include-reviews` flag matters: `worktree-lane` defaults to `False`
because its real caller, `dispatch.sh --reviews-pr`, is asking a DIFFERENT
question — "who could plausibly have AUTHORED this PR?" — and a review task
can never be its own PR's author (#76), so the default filters
review-shaped tasks out. A REVIEWING lane's own worktree is legitimately
parked on a task that looks like a review, so that same filter answers
`known:false` for a row the ledger actually has if the flag is left off —
measured directly (#212), from the exact situation #187 was about, before
this flag existed:

```
$ cli.py worktree-lane --path "$(pwd)"
{"known":false,"lane":null,"path":".../ad-211-rev212-14268","task":null}
$ cli.py worktree-lane --path "$(pwd)" --include-reviews
{"known":true,"lane":"agent-supervisor:6","path":"...","task":"as211-rev212"}
```

Do not remove the default filter to "fix" this — that reintroduces #76 (a
review task answering as a PR's author). The flag exists so the two
questions share one lookup without sharing one answer.
