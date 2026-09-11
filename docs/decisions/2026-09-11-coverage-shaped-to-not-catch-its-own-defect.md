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

**2. #354's own new fail-closed test.** The fix added `main-targets`
resolution and a named list of unresolvable shapes it refuses on:
`$VAR` expansions, `pushd`, `||` after a `cd`, `git -C $DIR`, and — in
the doc comment only — `cd -`. `test_unresolvable_target_fails_closed`
tests every item on that list except `cd -`, which is the one the
resolver actually gets wrong (it strips the leading `-` as if it were an
option flag, so `cd -`'s real target, `$OLDPWD`, silently resolves as
plain `cd`'s target, `$HOME`, instead of being refused as unresolvable).
The test's own list of cases was drawn from the same enumeration as the
doc comment's claim, so the one item the implementation actually missed
was also, unsurprisingly, the one item the test never tried.

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

In each case, the code being tested branches on a distinction — session
cwd vs. actual target; a named-unresolvable shape vs. every other shape;
scratch-clone branch vs. session's own worktree branch — and the test's
fixture held the two sides of that distinction **equal**, not different,
either by omission (case 1: no fixture ever put them in different
directories) or because the list of cases tested was generated from the
same source as the claim being tested, rather than independently against
the actual code (case 2: the doc comment's own list, missing exactly what
the resolver missed). A passing test in all three states nothing about
which branch the code actually took, because both branches produce the
same observable result when the fixture doesn't force them apart.

## Whether this generalises

**As close to a rule as this record can honestly offer, not a mechanical
check**: before trusting a test (or a review transcript) that claims a
resolver correctly distinguishes A from B, ask whether the fixture
actually makes A and B different values — not merely two different-looking
scenarios that happen to collapse to the same directory, the same case
list, or the same name. No linter can decide this generally; it requires
reading what the code under test actually branches on and confirming the
fixture varies exactly that. Case 2 adds a narrower, checkable corollary:
a test suite whose list of cases is enumerated from the same doc comment
as the implementation's own claim will share that claim's blind spots —
independent derivation of the test list (from the code's own branches,
not its comment) is the only way around it.

This is not offered as something CI could enforce, and the three cases
span two repos and three unrelated pieces of code — a real pattern, not a
manufactured one, but a judgment call for the next reviewer to re-apply,
not a rule this record settles for them.

## References

- agent-dotfiles#353, agent-dotfiles#354 — the guard, its original
  blind spot, and the `cd -` gap the fix's own test list didn't cover
- agent-estate#1408 — the confounded Part 1 experiment and its
  re-derivation once the two branches were made to differ
