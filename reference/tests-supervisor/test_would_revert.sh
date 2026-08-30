#!/bin/bash
# would-revert.sh must answer "would merging this branch revert X" by
# actually merging it, and must never touch the caller's working tree,
# index, or current branch while doing so.
#
# This is the agent-dotfiles#114 scenario: a two-dot diff renders every
# commit a branch is merely behind on as a deletion, and a lane read that
# as a real revert twice in two hours even after the correction was
# written into loop-tick.md. The load-bearing cases are: a branch that is
# only behind reports no deletions, a branch that truly deletes a file
# reports it and fails, a conflict is reported as a conflict and not
# silently folded into either of those, and the caller's own checkout is
# untouched throughout.
#
# agent-dotfiles#119 found three more, all fixed here and covered below: a
# scratch branch left behind on every run (not just the worktree), cleanup
# not running when the process is actually killed mid-run, and a conflict
# on one file hiding a genuine, silent deletion of another file in the
# SAME merge -- the exact case this tool exists to catch.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WR="$HERE/../../scripts/supervisor/would-revert.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }

echo "would-revert.sh"

D=$(mktemp -d)
export WORKTREE_ROOT="$D/roots"
mkdir -p "$WORKTREE_ROOT"

git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/repo"
REPO="$D/repo"
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name "Test"
git -C "$REPO" checkout -q -b main
echo one > "$REPO/mainfile.txt"
git -C "$REPO" add mainfile.txt
git -C "$REPO" commit -q -m "initial"
git -C "$REPO" push -q -u origin main
git -C "$REPO" remote set-head origin main >/dev/null 2>&1 || true

export WOULD_REVERT_REPO="$REPO"

# --- false-positive case: a branch that is merely behind -------------------
# This is the exact shape that produced both false holds in #114: main gains
# a commit the branch never had, which a two-dot diff renders as the branch
# "deleting" it. A real merge reverts nothing.
git -C "$REPO" checkout -q -b behind-branch
echo feature > "$REPO/feature.txt"
git -C "$REPO" add feature.txt
git -C "$REPO" commit -q -m "branch work"

git -C "$REPO" checkout -q main
echo two >> "$REPO/mainfile.txt"
git -C "$REPO" commit -q -am "main moved on without the branch"
git -C "$REPO" push -q origin main

out=$(bash "$WR" behind-branch origin/main 2>&1); rc=$?
want_exit "behind branch: exits 0" "$rc" 0 "$out"
if grep -q "DELETES" <<<"$out"; then bad "behind branch: reports no deletions" "$out"; else ok "behind branch: reports no deletions"; fi

# --- true-positive case: a branch that genuinely deletes a base file -------
git -C "$REPO" checkout -q main
git -C "$REPO" checkout -q -b delete-branch
git -C "$REPO" rm -q mainfile.txt
git -C "$REPO" commit -q -m "branch actually removes mainfile.txt"

out=$(bash "$WR" delete-branch origin/main 2>&1); rc=$?
want_exit "deleting branch: exits non-zero" "$rc" 1 "$out"
if grep -q "DELETES" <<<"$out" && grep -q "mainfile.txt" <<<"$out"; then
  ok "deleting branch: reports the deletion"
else
  bad "deleting branch: reports the deletion" "$out"
fi
if grep -qi "CONFLICT" <<<"$out"; then bad "deleting branch: does not also claim a conflict" "$out"; else ok "deleting branch: does not also claim a conflict"; fi

# --- conflict case -----------------------------------------------------------
git -C "$REPO" checkout -q main
git -C "$REPO" checkout -q -b conflict-branch
echo "branch version" > "$REPO/mainfile.txt"
git -C "$REPO" commit -q -am "branch edits the same line main also changed"

git -C "$REPO" checkout -q main
echo "main version" > "$REPO/mainfile.txt"
git -C "$REPO" commit -q -am "main edits the same line"
git -C "$REPO" push -q origin main

out=$(bash "$WR" conflict-branch origin/main 2>&1); rc=$?
want_exit "conflict: exits non-zero" "$rc" 2
# Not just `grep -qi CONFLICT` -- git's OWN raw merge output also contains
# that word, so that alone would pass even with would-revert.sh's own
# classification deleted (agent-dotfiles#119: confirmed by mutation, see
# below). Assert on the script's own structured line and file list, and
# that git's raw "Automatic merge failed" fallback text is NOT what
# produced the match -- that text only appears on the unclassified-failure
# path, never the classified one.
if grep -q "would-revert:.*CONFLICTS" <<<"$out" && grep -qF "mainfile.txt" <<<"$out"; then
  ok "conflict: reported as a conflict, by name, in would-revert.sh's own report"
else
  bad "conflict: reported as a conflict, by name, in would-revert.sh's own report" "$out"
fi
if grep -q "Automatic merge failed" <<<"$out"; then
  bad "conflict: classified, not just git's raw failure text relayed" "$out"
else
  ok "conflict: classified, not just git's raw failure text relayed"
fi
if grep -q "DELETES" <<<"$out"; then bad "conflict: not reported as a deletion" "$out"; else ok "conflict: not reported as a deletion"; fi

# --- conflict AND a genuine, silent deletion in the SAME merge -------------
# A conflict on one file does not stop git from cleanly auto-resolving a
# DELETION on a different file in the same merge attempt: main never
# touching a file the branch removes is not a conflict, it just applies.
# Before agent-dotfiles#119's fix, the deletion check was skipped entirely
# whenever the merge conflicted, and the report said "not a deletion
# either" while a real deletion had happened. The deletion is the
# dangerous half; a conflict must not hide it.
# A fresh pair of files, forked from a common point BOTH sides then
# diverge from -- reusing mainfile.txt here would make branch the only
# side to touch it (main already moved on to "main version" earlier in
# this file and never changes it again), which merges clean and proves
# nothing. sidefile.txt only main touches, so branch's deletion of it
# applies without a fight.
git -C "$REPO" checkout -q main
echo "shared" > "$REPO/sharedfile.txt"
echo "keep me" > "$REPO/sidefile.txt"
git -C "$REPO" add sharedfile.txt sidefile.txt
git -C "$REPO" commit -q -m "common base for the conflict+deletion case"
git -C "$REPO" push -q origin main

git -C "$REPO" checkout -q -b mixed-branch
echo "branch version" > "$REPO/sharedfile.txt"
git -C "$REPO" rm -q sidefile.txt
git -C "$REPO" commit -q -am "branch edits sharedfile.txt, deletes sidefile.txt"

git -C "$REPO" checkout -q main
echo "main version" > "$REPO/sharedfile.txt"
git -C "$REPO" commit -q -am "main edits sharedfile.txt too, never touches sidefile.txt"
git -C "$REPO" push -q origin main

out=$(bash "$WR" mixed-branch origin/main 2>&1); rc=$?
want_exit "conflict+deletion: exits non-zero" "$rc" 2
if grep -q "would-revert:.*CONFLICTS" <<<"$out" && grep -qF "sharedfile.txt" <<<"$out"; then
  ok "conflict+deletion: the conflict is reported"
else
  bad "conflict+deletion: the conflict is reported" "$out"
fi
if grep -q "would-revert:.*DELETES" <<<"$out" && grep -qF "sidefile.txt" <<<"$out"; then
  ok "conflict+deletion: the deletion is ALSO reported, not hidden by the conflict"
else
  bad "conflict+deletion: the deletion is ALSO reported, not hidden by the conflict" "$out"
fi

# --- neither classification bleeds into the other's single-issue case ------
# Guards against "fixing" the false negative by reporting everything
# always: a pure conflict must not claim a deletion, and (checked earlier
# in the true-positive case above) a pure deletion must not claim a
# conflict.
if grep -q "would-revert:.*DELETES" <<<"$(bash "$WR" conflict-branch origin/main 2>&1)"; then
  bad "conflict-only: does not also claim a deletion" "did"
else
  ok "conflict-only: does not also claim a deletion"
fi

# --- caller's working tree, index, and branch are untouched ----------------
git -C "$REPO" checkout -q main
echo "uncommitted edit" >> "$REPO/feature-not-tracked.txt"
before_branch=$(git -C "$REPO" branch --show-current)
before_status=$(git -C "$REPO" status --porcelain)

bash "$WR" delete-branch origin/main >/dev/null 2>&1

after_branch=$(git -C "$REPO" branch --show-current)
after_status=$(git -C "$REPO" status --porcelain)

if [ "$before_branch" = "$after_branch" ]; then ok "caller branch is unchanged"; else bad "caller branch is unchanged" "was $before_branch, now $after_branch"; fi
if [ "$before_status" = "$after_status" ]; then ok "caller working tree/index is unchanged"; else bad "caller working tree/index is unchanged" "before:$before_status  after:$after_status"; fi

# --- no scratch worktree is left behind, on any of the paths above ---------
leftover=$(git -C "$REPO" worktree list | grep -c "would-revert-" || true)
if [ "$leftover" -eq 0 ]; then ok "no scratch worktree left behind"; else bad "no scratch worktree left behind" "$(git -C "$REPO" worktree list)"; fi

# --- no scratch BRANCH is left behind either (agent-dotfiles#119) ----------
# `worktree.sh new` creates `lane/would-revert-$$` for every run; `worktree.sh
# done` only ever removes the WORKTREE. Every run above -- clean, deleting,
# conflicting, and conflict+deleting -- must leave zero of these, or they
# accumulate one per invocation forever.
stray_branches=$(git -C "$REPO" branch --list 'lane/would-revert-*')
if [ -z "$stray_branches" ]; then
  ok "no scratch branch left behind by any run above"
else
  bad "no scratch branch left behind by any run above" "$stray_branches"
fi

rm -rf "$D"

# --- an interrupted run leaves neither the worktree nor the branch ---------
# `trap cleanup EXIT` alone does not fire when a signal lands while bash is
# blocked in a command substitution (agent-dotfiles#119) -- send a REAL
# SIGTERM, not a simulated failure, timed at 0.15s, the delay that
# reproduced the leak against the unfixed script.
D2=$(mktemp -d)
mkdir -p "$D2/roots"
git init -q --bare "$D2/origin.git"
git clone -q "$D2/origin.git" "$D2/repo"
REPO2="$D2/repo"
git -C "$REPO2" config user.email test@example.com
git -C "$REPO2" config user.name "Test"
git -C "$REPO2" checkout -q -b main
echo one > "$REPO2/f.txt"
git -C "$REPO2" add f.txt
git -C "$REPO2" commit -q -m initial
git -C "$REPO2" push -q -u origin main
git -C "$REPO2" checkout -q -b interrupt-branch
echo a > "$REPO2/a.txt"
git -C "$REPO2" add a.txt
git -C "$REPO2" commit -q -m "branch work"
git -C "$REPO2" checkout -q main

WORKTREE_ROOT="$D2/roots" WOULD_REVERT_REPO="$REPO2" \
  bash "$WR" interrupt-branch origin/main >"$D2/out.log" 2>&1 &
WR_PID=$!
sleep 0.15
kill -TERM "$WR_PID" 2>/dev/null
wait "$WR_PID" 2>/dev/null

leftover_wt=$(git -C "$REPO2" worktree list | grep -c "would-revert-" || true)
leftover_br=$(git -C "$REPO2" branch --list 'lane/would-revert-*')
if [ "$leftover_wt" -eq 0 ]; then ok "an interrupted run leaves no worktree"; else bad "an interrupted run leaves no worktree" "$(git -C "$REPO2" worktree list)"; fi
if [ -z "$leftover_br" ]; then ok "an interrupted run leaves no branch"; else bad "an interrupted run leaves no branch" "$leftover_br"; fi

rm -rf "$D2"

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
