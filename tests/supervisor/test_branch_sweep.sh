#!/bin/bash
# agent-supervisor#580: `git merge-base --is-ancestor` cannot see a squash
# merge, so the "delete local branches already merged to origin/main" sweep
# never fired -- 417 local branches, 7 ancestors, 0 deleted. branch-sweep.sh
# replaces the ancestor test with a tree comparison
# (`git merge-tree --write-tree`) and adds the deletion-rule guards the
# brief requires.
#
# Mutation-checked BOTH directions, by construction, not by reading the
# script (a sweep that deletes nothing and a sweep that deletes everything
# both pass a one-directional test):
#   1. A branch whose content is fully squash-merged into origin/main, old
#      enough, unheld -- MUST be swept.
#   2. A branch with genuine unmerged work -- MUST be kept, and reported as
#      "not merged", not silently folded into any other reason.
# Plus every other guard the brief names, each proven to actually fire:
#   3. A branch that would conflict merging into origin/main -- kept, and
#      reported as its own outcome ("conflict"), never counted as
#      "unmerged" (the brief's own stated trap: `merge-tree --write-tree`
#      exits non-zero and does not hand back a bare tree oid on conflict).
#   4. A branch with real commits and no remote ref at all -- kept
#      ("local-only") even though its content happens to already be on
#      main, because nothing outside this clone would remember it if that
#      guess were wrong.
#   5. A branch whose content is merged but whose last commit is only
#      seconds old -- kept ("too-young") under the default 3600s floor, and
#      swept once the floor is lowered to 0 -- proves the age gate is live,
#      not decorative.
#   6. A branch checked out in a worktree -- kept ("worktree-held") no
#      matter how old or how merged; worktree.sh's own `gc` owns that case.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SWEEP="$HERE/../../scripts/supervisor/branch-sweep.sh"
pass=0; fail=0

D=$(mktemp -d)
trap 'rm -rf "$D"' EXIT

# Backdate a commit's committer/author date -- portable across macOS and
# Linux `git commit --amend`, same technique the gc-sweep test suite already
# uses via python3 for mtimes; here it is git's own date flags instead,
# since what matters is commit time, not filesystem mtime.
backdate_head() {   # backdate_head <repo> <iso8601>
  local repo="$1" when="$2"
  GIT_COMMITTER_DATE="$when" git -C "$repo" -c commit.gpgsign=false commit -q --amend --no-edit --date="$when"
}

check() {   # check <description> <expected 0|1> <actual-rc>
  local desc="$1" expect="$2" actual="$3"
  if [ "$expect" -eq "$actual" ]; then
    pass=$((pass + 1))
    echo "ok - $desc"
  else
    fail=$((fail + 1))
    echo "NOT OK - $desc (expected rc $expect, got $actual)"
  fi
}

# --- build the throwaway repo, with a real remote so origin/* refs exist ---
REPO="$D/repo"
BARE="$D/bare.git"
mkdir -p "$REPO"
git -C "$REPO" -c init.defaultBranch=main init -q
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name test
echo base > "$REPO/f.txt"
git -C "$REPO" add -A && git -C "$REPO" -c commit.gpgsign=false commit -q -m base
git init -q --bare "$BARE"
git -C "$REPO" remote add origin "$BARE"
git -C "$REPO" push -q origin main

OLD="2020-01-01T00:00:00"

# 1. topic-merged: real work, squash-merged into main, old commit -- SWEEP.
git -C "$REPO" checkout -qb topic-merged main
echo squashed > "$REPO/merged.txt"
git -C "$REPO" add -A && git -C "$REPO" -c commit.gpgsign=false commit -q -m "topic work"
backdate_head "$REPO" "$OLD"
git -C "$REPO" checkout -q main
git -C "$REPO" merge -q --squash topic-merged
git -C "$REPO" -c commit.gpgsign=false commit -q -m "squash merge topic-merged"
git -C "$REPO" push -q origin main
git -C "$REPO" push -q origin topic-merged

# 2. topic-unmerged: real, never-landed work -- KEEP, reason "not-merged".
git -C "$REPO" checkout -qb topic-unmerged main
echo unmerged_work > "$REPO/unmerged.txt"
git -C "$REPO" add -A && git -C "$REPO" -c commit.gpgsign=false commit -q -m "real unmerged work"
backdate_head "$REPO" "$OLD"
git -C "$REPO" push -q origin topic-unmerged
git -C "$REPO" checkout -q main

# 3. topic-conflict: edits the same line main later changes -- KEEP, reason
#    "conflict", not "not-merged".
git -C "$REPO" checkout -qb topic-conflict main
echo conflict_change > "$REPO/f.txt"
git -C "$REPO" add -A && git -C "$REPO" -c commit.gpgsign=false commit -q -m "conflicting edit"
backdate_head "$REPO" "$OLD"
git -C "$REPO" push -q origin topic-conflict
git -C "$REPO" checkout -q main
echo main_moved_on >> "$REPO/f.txt"
git -C "$REPO" add -A && git -C "$REPO" -c commit.gpgsign=false commit -q -m "main edits the same file"
git -C "$REPO" push -q origin main

# 4. topic-local-only: content already matches main (a coincidental empty
#    diff against a fresh merge-base), never pushed -- KEEP, reason
#    "local-only", regardless of what the tree test would say.
git -C "$REPO" checkout -qb topic-local-only main
echo local_only_marker > "$REPO/local-only.txt"
git -C "$REPO" add -A && git -C "$REPO" -c commit.gpgsign=false commit -q -m "local only work, never pushed"
backdate_head "$REPO" "$OLD"
git -C "$REPO" checkout -q main

# 5. topic-too-young: branched from main's tip AFTER the squash merge, no
#    further changes -- its tree already equals main's (merged) but its own
#    last-commit time is genuinely fresh (right now, not backdated) -- KEEP
#    under the default floor, reason "too-young".
git -C "$REPO" checkout -qb topic-too-young main
git -C "$REPO" push -q origin topic-too-young
git -C "$REPO" checkout -q main

# 6. topic-worktree-held: merged content, old commit, but checked out in a
#    live worktree -- KEEP, reason "worktree-held", unconditionally.
WT="$D/wt-held"
git -C "$REPO" worktree add -q -b topic-worktree-held "$WT" topic-merged >/dev/null
git -C "$REPO" push -q origin topic-worktree-held

echo "=== dry-run against the fixture ==="
OUT=$("$SWEEP" --dry-run --no-github "$REPO" origin/main 2>&1)
echo "$OUT"

echo
echo "=== per-branch verdicts (the changed lines) ==="
line_merged=$(grep -F 'topic-merged --' <<<"$OUT")
line_unmerged=$(grep -F 'topic-unmerged --' <<<"$OUT")
line_conflict=$(grep -F 'topic-conflict --' <<<"$OUT")
line_local=$(grep -F 'topic-local-only --' <<<"$OUT")
line_young=$(grep -F 'topic-too-young --' <<<"$OUT")
line_worktree=$(grep -F 'topic-worktree-held --' <<<"$OUT")
printf 'topic-merged:         %s\n' "$line_merged"
printf 'topic-unmerged:       %s\n' "$line_unmerged"
printf 'topic-conflict:       %s\n' "$line_conflict"
printf 'topic-local-only:     %s\n' "$line_local"
printf 'topic-too-young:      %s\n' "$line_young"
printf 'topic-worktree-held:  %s\n' "$line_worktree"
echo

case "$line_merged" in "branch-sweep: would delete topic-merged"*) r=0 ;; *) r=1 ;; esac
check "merged, old, unheld branch is swept" 0 "$r"

case "$line_unmerged" in *"skip topic-unmerged -- origin/main does not already contain its content"*) r=0 ;; *) r=1 ;; esac
check "genuinely unmerged branch is kept, reason not-merged" 0 "$r"

case "$line_conflict" in *"skip topic-conflict -- would conflict merging into origin/main"*) r=0 ;; *) r=1 ;; esac
check "conflicting branch is kept, reason conflict (not folded into not-merged)" 0 "$r"

case "$line_local" in *"skip topic-local-only"*"local-only branches are never deleted"*) r=0 ;; *) r=1 ;; esac
check "local-only branch is kept even though its content matches main" 0 "$r"

case "$line_young" in *"skip topic-too-young"*"younger than the 3600s floor"*) r=0 ;; *) r=1 ;; esac
check "merged-but-fresh branch is kept under the default age floor" 0 "$r"

case "$line_worktree" in *"skip topic-worktree-held -- checked out in worktree"*) r=0 ;; *) r=1 ;; esac
check "worktree-held branch is kept unconditionally" 0 "$r"

# --- prove --dry-run changed nothing -----------------------------------
still_there=$(git -C "$REPO" show-ref --verify --quiet refs/heads/topic-merged && echo yes || echo no)
[ "$still_there" = "yes" ]
check "dry-run left topic-merged in place" 0 $?

# --- live run: only topic-merged is actually deleted -------------------
echo
echo "=== live run against the fixture ==="
"$SWEEP" --no-github "$REPO" origin/main 2>&1

git -C "$REPO" show-ref --verify --quiet refs/heads/topic-merged
check "live run actually deleted topic-merged" 1 $?
for b in topic-unmerged topic-conflict topic-local-only topic-too-young topic-worktree-held; do
  git -C "$REPO" show-ref --verify --quiet "refs/heads/$b"
  check "live run left $b alone" 0 $?
done

# --- age gate is live, not decorative: lower the floor to 0 and re-sweep,
#     topic-too-young must now go. ---------------------------------------
echo
echo "=== live run with BRANCH_SWEEP_MIN_AGE_SECONDS=0 ==="
BRANCH_SWEEP_MIN_AGE_SECONDS=0 "$SWEEP" --no-github "$REPO" origin/main 2>&1
git -C "$REPO" show-ref --verify --quiet refs/heads/topic-too-young
check "with the age floor at 0, topic-too-young is now swept" 1 $?

# --- GitHub cross-check: disagreement is reported, not silently resolved --
# Stub `gh` (STUBS dir prepended to PATH) so this runs offline and
# deterministically: `topic-merged-disagree` passes the tree test (its
# content is on main) but the stub reports its PR as OPEN, not MERGED --
# the tree test and GitHub's own record disagree, and the brief requires
# that be reported rather than either signal silently winning.
git -C "$REPO" checkout -qb topic-merged-disagree main
echo squashed_disagree > "$REPO/merged-disagree.txt"
git -C "$REPO" add -A && git -C "$REPO" -c commit.gpgsign=false commit -q -m "topic work (disagree fixture)"
backdate_head "$REPO" "$OLD"
git -C "$REPO" checkout -q main
git -C "$REPO" merge -q --squash topic-merged-disagree
git -C "$REPO" -c commit.gpgsign=false commit -q -m "squash merge topic-merged-disagree"
git -C "$REPO" push -q origin main
git -C "$REPO" push -q origin topic-merged-disagree

# Cosmetic only: branch-sweep.sh reads `git remote get-url origin` purely to
# derive an owner/repo slug for `gh`, and never fetches or pushes again in
# this test -- repointing it here does not disturb the refs already synced
# to the real (local, bare) origin above.
git -C "$REPO" remote set-url origin https://github.com/example-owner/example-repo.git

STUBS="$HERE/stubs-branch-sweep"
echo
echo "=== live run WITH the GitHub cross-check (stubbed) ==="
GH_OUT=$(STUB_GH_PR_ROWS=$'topic-merged-disagree\tOPEN' PATH="$STUBS:$PATH" "$SWEEP" "$REPO" origin/main 2>&1)
echo "$GH_OUT"
line_disagree=$(grep -F 'topic-merged-disagree --' <<<"$GH_OUT")
echo
echo "topic-merged-disagree: $line_disagree"
case "$line_disagree" in *"skip topic-merged-disagree"*"tree test says merged"*"GitHub's PR record for it is not MERGED"*) r=0 ;; *) r=1 ;; esac
check "GitHub-disagreement branch is reported and kept, not silently resolved" 0 "$r"
git -C "$REPO" show-ref --verify --quiet refs/heads/topic-merged-disagree
check "GitHub-disagreement branch was not deleted" 0 $?

# Same run, sanity check that the GitHub path did not regress the plain
# tree-test outcomes proven above -- topic-unmerged (a real PR, OPEN state
# in the stub) must still read as not-merged, not as some new GitHub-driven
# outcome.
case "$GH_OUT" in *"skip topic-unmerged -- origin/main does not already contain its content"*) r=0 ;; *) r=1 ;; esac
check "GitHub path does not disturb the plain not-merged verdict" 0 "$r"

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
