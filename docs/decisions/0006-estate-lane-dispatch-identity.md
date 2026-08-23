# 0006 — An estate-lane dispatch records its own identity retroactively, through `cli.py record-dispatch`, not through `dispatch.sh`

`2026-08-23` (agent-supervisor#538).

## Decision

An estate-loop lane — one handed a brief file rather than dispatched by
`dispatch.sh` against a GitHub issue — records its own authorship after it
opens a PR, by running `register-pr-dispatch-self.sh --pr <N> --repo
<owner/name>` from its own pane. That script (1) confirms/refreshes the
lane's own identity via `register-lane-self.sh`, unchanged, then (2) calls
`cli.py record-dispatch --pr <N> ... ` with no `--issue` — newly allowed,
see below — producing the same `source_tasks` row shape
(`source_kind='pull'`) a `dispatch.sh --pr` caller already produces.
`author_lane_for` (`verdict-independence.sh`) already reads that shape
(Path 4, `contributor-pr-lanes`); nothing there changed.

`cli.py record-dispatch --issue` is no longer required at the argparse
layer when `--pr` is given — it stays required in every other shape (a
plain issue-scoped dispatch cannot record itself with no source at all;
enforced in the free function, not argparse, so the refusal names what is
actually missing). Every existing `dispatch.sh` caller still passes both
flags and is unaffected.

## Why not just call `dispatch.sh`

Read `dispatch.sh`'s own header before assuming this is a shortcut around
it. It picks a FREE lane from `lanes.sh`'s classification, claims a GitHub
issue with `claim.sh take`, and builds the worktree itself (`worktree.sh
new`) — every step assumes work starts from an open issue in a repo
`dispatch.sh` is driving, before the worktree exists. An estate-loop brief
is a local file (`~/.local/state/estate-loop/*.md`), the lane's worktree
already exists by the time it has anything to record, and — measured on
this decision's own two symptom PRs, agent-supervisor#531 and this PR
itself — the resulting PR frequently closes no GitHub issue at all. Routing
these through `dispatch.sh` would mean either (a) minting a synthetic
GitHub issue purely so `claim.sh` has something to point at — a fact
nobody has, recorded as if it were one, which is exactly the self-attested-
record hazard `register-lane-self.sh`'s own header warns against for lane
identity — or (b) reimplementing `dispatch.sh`'s claim-and-worktree
machinery for a precondition (issue exists, worktree does not yet) that
does not hold. Neither is "simply calling dispatch.sh"; both are worse than
recording, retroactively, the dispatch that already happened — which is
literally what `cli.py record_dispatch`'s own docstring says it is for
("Record a dispatch that ALREADY happened. Writes; never sends.").

## What this deliberately preserves

- **`register-lane-self.sh` still takes no lane id.** The wrapper calls it
  unmodified; every field but `--pr` and `--repo` (the two facts a pane
  cannot observe about itself) is measured off `$TMUX_PANE`, exactly as
  before. A lane still cannot fabricate an identity it does not have.
- **`Author-Lane:` stays refuse-only.** This decision adds a SECOND,
  independent, ledger-backed way to resolve authorship; it does not touch
  `claimed_author_conflict()` or widen what the self-attested trailer may
  do. A PR this mechanism resolves needs the trailer for nothing; a PR it
  does not resolve is exactly as before — the trailer can still convict its
  own author, never clear one.
- **Fail-closed is unchanged for everything that does not opt in.** A PR
  nobody ever ran `register-pr-dispatch-self.sh` against still reads
  "independence unknown -- PR author lane unresolved", byte for byte
  (proven by `test_merge_pr_estate_lane_identity.sh` case 3, run
  immediately after a case that DOES resolve, so the refusal is shown to be
  live, not a leftover default).
- **`verdict.py`, the `Reviewed-SHA` freshness check, and
  `mark-pr-external.sh` are untouched.** This is not a laundering path: it
  records the true fact that a lane, not an external actor, authored the
  PR — the opposite of what `mark-pr-external.sh` is for.

## Verified

`test_cli_record_dispatch_issue_optional.sh` (9 cases, no tmux): the
argument-matrix change itself, including that `--issue`+`--pr` together and
`--issue` alone are both unaffected, and that the issue-less record's
evidence honestly says "issues: none" rather than inventing one.

`test_register_pr_dispatch_self.sh` (10 cases, live isolated tmux): the
wrapper script directly — refuses with no `$TMUX_PANE`, refuses a
non-numeric `--pr`, refuses to register the supervisor's own window,
registers and self-confirms a real pane as a real PR's contributor,
idempotent on re-run.

`test_merge_pr_estate_lane_identity.sh` (7 cases, live isolated tmux) —
the mutation check both directions, against `merge-pr.sh` itself:

1. a PR closing no issue (agent-supervisor#531's own shape), author
   self-registered, reviewed by a genuinely different lane → **merges**.
2. the identical shape, but the reviewer IS the author lane → **refuses**,
   named as a self-review.
3. the identical shape, nobody ever self-registered → **refuses**, the
   same "independence unknown" message as before this change.
4. case 1's exact fixtures, re-run after 2/3 → **merges again**, proving
   the intervening refusals were the registration state and nothing else.

**agent-supervisor#531 becomes mergeable under this change.** Case 1 above
reproduces its exact shape (no closing issue, genuinely independent
review) end to end and merges. This was proven by reproducing the shape in
an isolated ledger and tmux server, not by running
`register-pr-dispatch-self.sh` against the live estate's own ledger or the
real PR #531 — this decision's own author is not lane `estate:2`, and
running the tool as if it were would be exactly the fabricated-identity
hazard the tool exists to prevent. `estate:2` (or an operator on its
behalf, from that lane's own pane) still needs to run it once against the
real PR before #531 itself merges through the sanctioned path.

## What this does not fix (named, not silently dropped)

- **`estate:1` (the director) still has no `lanes` row.** This decision's
  mechanism registers a lane the moment it has a PR to record; a director
  that dispatches work but authors nothing never reaches that call. The
  correct fix is `register-lane-self.sh` — unmodified, already sufficient
  — run once from the director's own pane at its own startup. This was not
  wired into any bootstrap script here: the live `estate` tmux session's
  window 1 (`director`, confirmed live 2026-08-23) and `director-loop.sh`'s
  own default target (`director:@3`, a DIFFERENT session name) do not
  agree, and patching a bootstrap path on an assumption about which
  process actually drives the live window — the exact "an instrument that
  cannot see a thing looks exactly like the thing being absent" failure
  this repo's own `AGENTS.md` names first — is worse than naming the gap.
  Follow-up: confirm which process actually starts `estate:1` before wiring
  `register-lane-self.sh` into it.
- **`harness_session_id` stays empty for a retroactive registration.**
  `harness_session.py`'s resolver expects to run moments after a brief was
  sent, keyed by an epoch read before the send (`--since`) — using it
  hours into an existing session, after the fact, risks returning a
  plausible but WRONG session id, which invariant 3 (`AGENTS.md`, "restore
  refuses rather than invents") treats as strictly worse than the empty
  string this leaves behind. `restore.sh` already handles an empty
  `harness_session_id` correctly (reports the lane unrecoverable rather
  than starting a fresh agent under its name) — this is the same accepted,
  documented degraded mode `register-lane-self.sh` itself already lives in,
  not a new gap this decision introduces.
