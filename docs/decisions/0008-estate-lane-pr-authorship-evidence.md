---
type: Decision
description: Should a lane self-assert its own PR authorship, or must it only ever be recorded by whatever dispatched the work -- decided, with the losing argument answered.
generated:
  at: 2026-08-23T18:50:00-04:00
---

# 0008 — An estate-lane's PR authorship: self-run cross-check now, an independent poller as the real fix, on an explicit trigger

`2026-08-23`. Decided by the director loop
(`estate-loop/director-authorship-design.md`), not implemented here — see
"Sequencing" for what's delegated and to whom.

**Numbering note, resolved:** three decision docs forked from the same
`origin/main` (still at `0005`) all claimed low numbers —
`agent-supervisor#533`'s `0006-agent-tui-merges-into-agent-supervisor.md`,
`agent-supervisor#539`'s `0006-estate-lane-dispatch-identity.md`, and this
one. Tiebreak: **whichever PR was opened first keeps the number it
claimed; every later one increments.** By `gh pr view --json createdAt`:
`#533` (21:24 UTC) keeps `0006`; `#539` (21:46 UTC) becomes `0007` —
stated on both PRs so the losing side renumbers rather than both waiting
on the other; this document (`#547`, 22:52 UTC) is `0008`. On the general
case: this is the first collision of its kind in this repo's five prior
decision docs, and it happened on an unusually high-velocity day
(concurrent migration decisions). Treating it as an **accepted, occasional
cost** — a rename-and-recommit when it happens — rather than adding a
reservation convention for something that has occurred once.

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
3. **File the independent-poller work now, with a single, checkable
   trigger — not "later," not "when we get round to it."** Filed
   alongside this document: `agent-supervisor#546`, due **before
   `agent-supervisor#541`'s step 7 runs** (`agent-tui` repo deletion —
   the migration's own point of no return). This is checkable by anyone,
   pass or fail, at any time: has step 7 run yet, and if so, is `#546`
   merged. Learned directly from the `worktree.sh gc` precedent, which
   had no such condition and sat unwired for 12 days.
4. **A lane running `register-pr-dispatch-self.sh` on itself remains
   observable, not just trusted.** `test_merge_pr_estate_lane_identity.sh`'s
   existing case 3 (unregistered PRs still refuse byte-for-byte) is the
   right shape of evidence; ask whoever reviews `#539` to also confirm a
   case where the cross-check itself fails (a lane's worktree does not
   hold the claimed PR's commits) and the registration correctly refuses
   rather than silently degrading to "trust the number."

## What the stopgap does not prove, even after the SHA fix

**Found after this decision was first written, and it sharpens rather than
overturns it:** `estate:2` built a working attack against the
branch-*name* version of the cross-check — `git checkout -b
<the-PR's-branch-name>` on an unrelated local branch satisfies a name
comparison with no relationship to the PR's actual content, and a
*reviewing* lane doing the completely ordinary `git worktree add <path>
<branch>` to read a diff would register itself as that PR's author under
the same check. `estate:5` is switching to a commit-SHA comparison in
response.

**The SHA fix closes the name-forgery attack. It does not close the
second one, and this document should not pretend otherwise:** a worktree
holding the PR's exact commits proves the worktree *has* them — it does
not prove *this lane produced them*. A reviewing lane's worktree, checked
out from the PR's own branch specifically so it *can* read the diff (the
completely normal shape of every review this session — `estate:3`'s own
`/tmp/at140-review` worktree earlier today is exactly this pattern),
satisfies a SHA comparison identically to the lane that actually authored
those commits. **This residual gap is accepted, not solved, by this
decision** — accepted because `#539`'s reviewers are not the ones running
`register-pr-dispatch-self.sh` on themselves as a matter of the tool's
intended use (a reviewer's own worktree registering itself as a PR's
author would require someone to run the authorship-claiming script from a
review worktree, which is a misuse of the tool, not its normal path) — but
it is a real, load-bearing reason `#546`'s independent poller is not
optional follow-up work, and whoever reviews `#539` should confirm the
tool's own usage guidance says plainly not to run it from a worktree
checked out to review someone else's work.

## What happens to the stopgap once `#546` lands

**Removed, not kept as a second source.** A self-run check and an
independent poller answering the same question is not defense in depth —
it is two mechanisms of different evidentiary strength both feeding the
same trusted fact, where the weaker one can still be the one consulted.
Once `#546`'s poller is live and has resolved `agent-supervisor#531`
(the concretely-confirmed case) and passed its own review,
`register-pr-dispatch-self.sh`'s self-run path should be retired —
either removed outright or demoted to a diagnostic a lane can run to
predict what the poller will find, never itself the fact `merge-pr.sh`
trusts. State this now rather than let "we'll decide later" become the
reason the weaker mechanism outlives its reason for existing, the exact
failure this whole decision is trying to avoid.

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
