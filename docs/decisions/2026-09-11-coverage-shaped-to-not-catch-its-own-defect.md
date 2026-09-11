# 2026-09-11 — three test suites shaped so they could not catch the defect they covered

**Status:** three cases, one shared shape, named — not promoted to a check.
No mechanism was built; this record exists so re-discovering the third
instance doesn't feel like the first.

## The three cases

**1. `main-branch-guard`'s seven pre-existing tests** (agent-dotfiles#353,
#354). The guard resolves the branch a commit would land on from
`TARGET_DIR`, meant to distinguish "the session's own cwd" from "where the
command actually targets" (via `-C` or a preceding `cd`). All seven
existing tests call `run_hook(SCRIPT, <command with no -C/cd at all>,
cwd=str(self.repo))` — with neither in the command, the two things the
guard exists to distinguish are, in every one of the seven fixtures, the
same directory. Confirmed directly against source, not inferred from the
bug report: "this exact set of seven could not have failed on either
Case A or Case B, regardless of which resolution strategy... the hook
used, because the two were never different."

**2. #354's own new fail-closed test.** `command_guard.py`'s own
module-level comment enumerates the resolver's named, refused-rather-
than-guessed limits: expansions (`$VAR`, `$(...)`, backticks) in a
`cd`/`-C` target, `cd -`, `cd ~user`, globs, `pushd`/`popd`, more than one
`cd` argument, `git --git-dir`/`--work-tree`, and `||` after a `cd`.
`test_unresolvable_target_fails_closed` tests five of those (`$DIR`,
`$(pwd)/elsewhere`, `||` after a `cd`, `git -C $DIR`, `pushd`) and omits
`cd -` — the one the resolver actually gets wrong: it strips the leading
`-` as if it were an option flag, so `cd -`'s real target, `$OLDPWD`,
silently resolves as plain `cd`'s target, `$HOME`, instead of being
refused as unresolvable. Unlike case 1, this isn't two fixture values
collapsed into one — it's a single named case missing from a test list
that otherwise matches its own doc comment's broader claim, and nothing
in the diff or its review establishes why that specific item is the one
missing.

**3. #1408 Part 1's original experiment** (voided by its own author after
review). To show the same guard resolves a scratch clone's branch
correctly, a commit was made in a disposable scratch clone whose branch
happened to be named identically to the session's own real worktree
branch. The reviewer's finding: "the guard correctly checked the scratch
clone's branch... or the guard checked [the session's] own real session
cwd... and allowed it for a reason that has nothing to do with the
scratch clone at all... The test as performed cannot distinguish these,
because the scratch branch and the real worktree branch happen to share a
name." The re-run that replaced it deliberately made the two branches
different (literally `main` vs. the session's own worktree branch) and a
directory with no `.git` at all, and got a different, decisive answer.

## What the three share

In each case, something the test needed to exercise to support its claim
was absent from what actually ran, for two different mechanical reasons.
Cases 1 and 3: the code branches on a distinction — session cwd vs. actual
target; scratch-clone branch vs. session's own worktree branch — and the
fixture held both sides of that distinction to the same value, so neither
branch could be told apart. Case 2 is a different shape: nothing collapsed
two values together; a named case was simply left off a test list that
otherwise matched its own doc comment's broader claim, and no evidence in
the diff or its review establishes why. What spans all three: a passing
test told a reader something stronger than what it had actually checked,
each time for a different reason.

## Whether this generalises

**As close to a rule as this record can honestly offer, not a mechanical
check, and it covers cases 1 and 3, not case 2**: before trusting a test
(or a review transcript) that claims a resolver correctly distinguishes A
from B, check that the fixture actually makes A and B different values —
not merely two different-looking scenarios that happen to collapse to the
same directory or the same name. Case 2 needs a different habit: read a
claimed enumeration against what was actually tested, item by item, rather
than trusting that a named list and a tested list are the same list.
Independent derivation of test cases from the code's own branches is not
established here as the only route to catching that gap — the reviewer
who found it did so by testing every item the doc comment already
claimed, one by one (agent-dotfiles#354's own review), which would have
worked too.

All three concern the same guard/resolver family — old tests, new tests,
and a live experiment about the same `main-branch-guard.sh`/
`command_guard.py`, not three unrelated pieces of code — so this is a
narrower base of evidence than a pattern spanning genuinely different
codebases would be. Worth naming because the same class of gap recurred
three times in one guard in one night, not because it has been shown to
generalise past it. Not offered as something CI could enforce; a judgment
call for the next reviewer to re-apply, not a rule this record settles
for them.

Reviewed at agent-dotfiles#354's merged SHA (`53965130`) — the running
guard is not this version. `agent-dotfiles#353` was reopened because the
deployed hook is still the pre-fix one (`agent-dotfiles#356` tracks that
deployment gap separately, out of this record's scope). This record
concerns the reviewed code, not what is currently live.

## References

- agent-dotfiles#353, agent-dotfiles#354 — the guard, its original
  blind spot, and the `cd -` gap the fix's own test list didn't cover
- agent-dotfiles#356 — the deployment gap: #353 reopened because the
  fix in #354 was never deployed; not this record's subject
- agent-estate#1408 — the confounded Part 1 experiment and its
  re-derivation once the two branches were made to differ
