<!--
This is the version-controlled copy of `run/briefs/_REVIEW-TEMPLATE.md`
(agent-estate#1324, agent-estate#1332). `run/` is not a git repository, so
the fix for the defect that let a red PR through (PR #1323 approved while
`gh pr checks` showed `estate fail`) was itself unversioned. Briefs are
still authored from the `run/briefs/` path — this copy exists so the
template survives independently of that directory. If the two drift,
`run/briefs/_REVIEW-TEMPLATE.md` is the live one in active use; update this
copy to match rather than the other way around.
-->

# Review brief template — use this for every review brief

Written 2026-09-08 after an audit found **0 of 18** review briefs instructed the
reviewer to check CI, while **8 asserted "CI green" as a fact in the header**.
The consequence: PR #1323 received `Verdict: APPROVE` while `gh pr checks`
showed `estate fail` on both runs. The brief told the reviewer CI was fine, so
it did not look.

## Rules for the author of a brief

1. **Never state CI status as a fact.** It is a live value; whatever you saw
   when writing the brief may be stale by the time the lane reads it. Write
   "check CI" — never "CI green".
2. **Never state any live value as settled.** Head SHA is safe (it is pinned).
   Counts, test results, CI, vault state — all are things the reviewer
   re-derives, not things you hand over.
3. **State what you believe and mark it as yours to attack.** "The PR claims X"
   is correct. "X is true" is not, unless you ran the command in this brief and
   pasted its output.

## Required section — paste into every review brief verbatim

```
## 0. CI — check this before anything else

    gh pr checks <N>

Every check must pass. **Paste the output.** If anything is red:

- Read the failure (`gh run view <run-id> --log-failed`).
- Establish whether it is this PR's or pre-existing on `origin/main`.
- If it is this PR's, that is a blocking finding and the verdict is
  REQUEST-CHANGES regardless of how good the diff is. Green CI is a merge
  condition; a passing local suite is not the same thing and is what let a red
  PR get approved on 2026-09-08.
- If it is pre-existing, say so with the evidence that it reproduces on
  unmodified `origin/main`, and continue.
```

## Also standard in every review brief

- Head SHA pinned in the verdict lines, so an approval cannot drift.
- "You did not write this PR" — reviewer ≠ author is the whole point.
- At least one **mutation** check: break the fix, confirm the test fails. A
  test that passes either way pins nothing.
- A named claim the reviewer must **falsify**, not confirm. Three reviews in a
  row this session found their one real defect in a *claim in the PR
  description*, not in the diff.
- The constraints block: sources read-only including `touch`; never regenerate
  the shared knowledge index; do not run `estate vault-view` unless the task is
  a vault write; `standinglaw.go` carries the #1286 dispatch-fatal pin.
4. **Never copy a hash, count, or SHA from another brief -- and never read one
   out of a checkout you have not confirmed is current.** Read it from the file
   it lives in, on `origin/main`, and paste the command you ran.

   The worked example is the Director getting this exactly backwards on
   2026-09-10. It read the standing-law `HashPrefix` as `ecf40670309d` from its
   own working checkout, declared the `26d4a45ea2a7` sitting in 24 briefs
   stale, and "corrected" two of those briefs and a handoff. The checkout was
   **50 commits behind origin/main**. `26d4a45ea2a7` was the live value the
   whole time; `ecf40670309d` is two re-pins old (`a255964bbdcf` ->
   `ecf40670309d` -> `26d4a45ea2a7`, per `standinglaw.go`'s own comment). A
   stale checkout does not announce itself: the file parses, the test passes,
   the value looks authoritative.

       git fetch origin && git rev-list --count HEAD..origin/main   # must be 0
       git show origin/main:src/estate/internal/corpus/standinglaw.go | grep 'HashPrefix:'

   Grepping `HashPrefix` across a tree also returns a test fixture
   (`deadbeefdead`) first, and the file's comments name three historical
   values. Only the single `HashPrefix:` assignment on `origin/main` is live,
   and getting it wrong hands a lane a dispatch-fatal false premise.
- `go build ./src/estate/...` — **not** `./...`.
