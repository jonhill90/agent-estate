---
type: Decision
description: Should a lane self-assert its own PR authorship, or must it only ever be recorded by whatever dispatched the work -- decided, with the losing argument answered.
generated:
  at: 2026-08-23T18:50:00-04:00
---

# 0007 — An estate-lane's PR authorship: self-run cross-check now, an independent poller as the real fix, on an explicit trigger

`2026-08-23`. Decided by the director loop
(`estate-loop/director-authorship-design.md`), not implemented here — see
"Sequencing" for what's delegated and to whom.

**Numbering note:** `agent-supervisor#539`'s own branch also adds a
`0006` (`docs/decisions/0006-estate-lane-dispatch-identity.md`), and this
repo's `main` only goes up to `0005` as of this writing. Whichever of
`#539` and `agent-supervisor#533` merges second will collide on `0006` —
flagging this here so the merging lane renumbers rather than silently
overwriting the other's file. This document takes `0007` since both `0006`
candidates are still open.

## The question

`agent-supervisor#539` lets an estate-loop-dispatched lane (one handed a
brief file, not routed through `dispatch.sh`) record its own PR authorship
by running `register-pr-dispatch-self.sh` from its own pane. Reviewing its
own fix, `estate:5` found the original version took `--pr <N>` on the
caller's word alone — no check the pane was ever on that PR's branch. Any
lane could claim authorship of any PR, and the ledger would record the
claim as fact. That fact is not decorative: `verdict-independence.sh`'s
`author_lane_for` (Path 4, reading `contributor-pr-lanes`) is what
`merge-pr.sh` calls to decide whether a reviewer is independent of the
author — a resolved, different-lane comparison **unlocks a merge**. This
is structurally unlike the `Author-Lane:` PR-body trailer, which this
repo's own code comments say explicitly must stay refuse-only: a
self-attested claim "must never be able to WIDEN" the contributor set
(`verdict-independence.sh`, `claimed_author_conflict()`'s own header).
`#539`'s ledger row does widen it. Should authorship instead only ever be
recorded by whatever dispatched the work — before the PR exists, by an
actor other than the lane being judged?

## What was actually measured before deciding

**One PR, not ten, is concretely confirmed blocked by this exact gap.**
The originating brief said "ten PRs are stuck behind it"; that figure does
not check out against the record. `agent-supervisor#538` names exactly
one — `agent-supervisor#531` — with the literal refusal text
"independence unknown -- PR author lane unresolved." Corrected here rather
than repeated, matching this repo's own habit of re-measuring a stale
count before trusting it (see `0006-agent-tui-merges-into-agent-supervisor.md`'s
own PR-reference-count correction, same discipline applied again).

**`#539`'s cross-check already strengthened itself, live, through the
review process — before this decision was reached.** Its third commit
(`127930b7`, "register-pr-dispatch-self.sh verifies its PR claim") added a
real `git branch --show-current` vs. `gh pr view --json headRefName`
comparison, replacing the earlier version that trusted the `--pr`/`--repo`
arguments on the caller's word alone. This is the review gate working as
designed, not a guard being weakened under pressure — worth stating
because the brief's own framing ("that pressure is exactly when a guard
gets weakened") predicts the opposite of what happened here.

**This estate has a same-day, same-repo precedent for the failure mode
the brief warns about, and it is not hypothetical.** `register-lane-self.sh`'s
own header documents that on 2026-08-23 — today — four lanes
(`estate:2..5`) had ledger rows registered **out of band, by hand**: a
`pane_id` matching no live pane, a `repo` naming the wrong checkout, one
`nonce` that was a hand-written string rather than anything minted. Every
one of those rows would have passed downstream checks and fed `merge-pr.sh`
a fabricated identity. That incident is what `reregister-lane.md`'s
tmux-server-recreation fix (handled earlier in this same session) exists to
correct — it is real, dated today, and it is the concrete shape "a guard
weakened under pressure" takes in this repo, not an abstraction.

**A candidate alternative — an independent poller anchored at PR-open
time, run on `watchdog.sh`'s cadence rather than the lane's own command —
was designed and then attacked directly, on the objection the brief named
verbatim.** It did not survive intact:

- The "already-trusted" lane→worktree mapping it would anchor on
  (`lanes.repo`) is upserted on every redispatch, not per-task — if a lane
  is redispatched before the poller ticks, the mapping now points at the
  lane's *new* location, not the one that produced the PR under review.
  The poller's anchor has the same "moment matters" problem, shifted
  later and made less accurate, not eliminated.
- The poller still needs the same branch/head-SHA comparison `#539`'s
  cross-check already performs — it does not get a cheaper mechanism,
  only a different actor running it, and it has no a-priori fact telling
  it *which* worktree to check without either brute-force scanning every
  worktree in the farm or being told by some other actor (reopening the
  same attestation question one level removed).
- **This repo has a dispositive precedent for what "ship the safe part
  now, defer the hard part" actually does here**: `worktree.sh gc` was
  designed (`agent-dotfiles#165/#169`, filed 2026-08-11) and sat
  **completely unwired for 12 days** — 92 worktrees accumulating, then
  64 — until `#526`/`#527` finally wired it into `watchdog.sh`, merged
  **today**, and even then shipped dry-run-only because "a second
  decision this PR does NOT make." A deferred "build a poller later" with
  no issue, no owner, and no forcing function is not a hypothetical risk
  in this repo — it is the default outcome, measured.

## Decision

**`#539`, as it now stands post-`127930b7` (the real branch/head-SHA
cross-check, not the earlier numeric-only version), may land as the
current mechanism.** This is not "ship the weaker stopgap because the
board is blocked" — the review process already produced the stronger
version before this decision was reached, and the alternative this
decision seriously considered (an independent poller) does not clear its
own bar either, on the evidence above, without the exact same "build it
later" gap that has already cost this repo 12 days once this month on a
structurally identical deferral.

**Conditions, not a bare approval:**

1. **The cross-check must apply universally, with no permanent
   carve-out — including to a lane's own PR.** `estate:5` was told not to
   use the new mechanism on its own PR (`#539` itself) while the
   mechanism was still under review — that is correct, and temporary: a
   PR cannot use its own not-yet-approved feature to certify its own
   merge, the same bootstrapping problem `Invariant 3`'s "restore refuses
   rather than invents" exists to avoid elsewhere. Once `#539` merges
   through the existing, unaffected evidence paths (an independent lane's
   direct review, `Author-Lane:` where applicable), the restriction lifts
   for everyone, permanently — it must not become a standing exemption
   that quietly persists after the reason for it is gone.
2. **`Author-Lane:` stays refuse-only; `claimed_author_conflict()` stays
   untouched.** `#539`'s own decision doc already states this as
   preserved (`0006-estate-lane-dispatch-identity.md`, "What this
   deliberately preserves") — restated here as a condition of this
   decision, not just a description of the diff, so it is checked at
   review time, not assumed.
3. **File the independent-poller work now, with an owner and a trigger —
   not "later."** Filed alongside this document:
   `agent-supervisor#546`. Learn from the `worktree.sh gc` precedent
   directly: the trigger is not a date, it is **"before the next
   migration or bulk-PR event that would put load-bearing weight on this
   mechanism again"** — the `agent-estate` migration currently gated on
   quiesce is itself that event, so this should land before step 7 of
   `agent-supervisor#541`'s runbook (repo deletion), not after.
4. **A lane running `register-pr-dispatch-self.sh` on itself remains
   observable, not just trusted.** `test_merge_pr_estate_lane_identity.sh`'s
   existing case 3 (unregistered PRs still refuse byte-for-byte) is the
   right shape of evidence; ask whoever reviews `#539` to also confirm a
   case where the branch/SHA cross-check itself fails (a lane's worktree
   is NOT on the claimed PR's branch) and the registration correctly
   refuses rather than silently degrading to "trust the number."

## Sequencing

1. `#539` continues review through the normal, independent path (not by
   me — I am sequencing this, not reviewing or merging it).
2. `agent-supervisor#546` (the independent-poller issue) is filed now,
   assigned no lane yet — pick one once the board's current review load
   clears, not before, matching the standing PR-drain freeze.
3. Once `#539` merges: the "not on your own PR" restriction lifts for
   everyone. Retroactively resolve `agent-supervisor#531` (the one
   concretely-confirmed blocked PR) through the new mechanism, reviewed
   by an independent lane the same as any other merge.
4. `#543` lands before `agent-supervisor#541`'s step 7 (the point of no
   return — `agent-tui` deletion). This is the actual trigger, not a date
   picked out of the air: the migration is exactly the kind of load the
   devils-advocate pass's own "smallest change that would make [a
   deferred design] survive" named — ship the stronger mechanism before
   the event that most needs it, not after.

## What this does not decide

- Whether `#543`'s poller design is exactly "watch `watchdog.sh`'s
  existing sweep" or something else — that is real engineering work for
  whoever picks it up, scoped by the issue, not fixed here.
- Any code review of `#539` itself — its correctness is `#539`'s own
  reviewers' job, independent of this document.
