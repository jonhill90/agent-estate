#!/bin/bash
# worktree.sh must give a lane an isolated tree, and must never discard
# uncommitted work when tearing one down.
#
# This is the agent-dotfiles#73 scenario: a lane working #28 had its branch
# switched out from under it in the shared checkout, and its uncommitted
# edits were destroyed. The load-bearing tests are `new` producing a working
# isolated checkout, and `done`/`guard` refusing to touch anything dirty.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WT="$HERE/../../scripts/supervisor/worktree.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }

echo "worktree.sh"

D=$(mktemp -d)
export WORKTREE_ROOT="$D/roots"
mkdir -p "$WORKTREE_ROOT"

# A minimal origin + clone, standing in for the real shared checkout.
git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/repo"
REPO="$D/repo"
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name "Test"
git -C "$REPO" checkout -q -b main
echo one > "$REPO/file.txt"
git -C "$REPO" add file.txt
git -C "$REPO" commit -q -m "initial"
git -C "$REPO" push -q -u origin main
git -C "$REPO" remote set-head origin main >/dev/null 2>&1 || true

# --- new: produces an isolated, checked-out worktree on its own branch ----
# stdout only -- git worktree's own progress text goes to stderr and must not
# land in the path this script hands back to a caller.
out=$(bash "$WT" new 73-test "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "new exits 0" "$rc" 0 "$out"
DEST="$out"
if [ -d "$DEST/.git" ] || git -C "$DEST" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  ok "new prints a real worktree path"
else
  bad "new prints a real worktree path" "$out"
fi
branch=$(git -C "$DEST" branch --show-current 2>/dev/null)
if [ "$branch" = "lane/73-test" ]; then ok "new checks out a lane-specific branch"; else bad "new checks out a lane-specific branch" "got '$branch'"; fi

# The shared checkout must stay untouched by creating a lane worktree.
main_branch=$(git -C "$REPO" branch --show-current 2>/dev/null)
if [ "$main_branch" = "main" ]; then ok "shared checkout branch is undisturbed"; else bad "shared checkout branch is undisturbed" "got '$main_branch'"; fi

# --- done: refuses to discard uncommitted work -----------------------------
echo "unsaved edit" >> "$DEST/file.txt"
out=$(bash "$WT" done "$DEST" 2>&1); rc=$?
want_exit "done refuses a dirty worktree" "$rc" 1 "$out"
if [ -d "$DEST" ]; then ok "done left the dirty worktree in place"; else bad "done left the dirty worktree in place" "removed despite uncommitted edit"; fi

# Clean it up, then removal succeeds.
git -C "$DEST" checkout -q -- file.txt
out=$(bash "$WT" done "$DEST" 2>&1); rc=$?
want_exit "done removes a clean worktree" "$rc" 0 "$out"
if [ -d "$DEST" ]; then bad "worktree directory is gone" "still present at $DEST"; else ok "worktree directory is gone"; fi

# --- done: refuses a detached HEAD carrying a commit unreachable from any
# branch (agent-dotfiles#79 finding A) -- `git status --porcelain` is clean
# in this case, so the dirty-tree check above cannot catch it.
out=$(bash "$WT" new 79-detach "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "new (detach case) exits 0" "$rc" 0 "$out"
DETACH_DEST="$out"
git -C "$DETACH_DEST" checkout -q --detach
echo "detached commit" >> "$DETACH_DEST/file.txt"
git -C "$DETACH_DEST" add file.txt
git -C "$DETACH_DEST" commit -q -m "detached work"
DETACHED_SHA=$(git -C "$DETACH_DEST" rev-parse HEAD)
out=$(bash "$WT" done "$DETACH_DEST" 2>&1); rc=$?
want_exit "done refuses a detached HEAD with an unreachable commit" "$rc" 1 "$out"
if [ -d "$DETACH_DEST" ]; then ok "done left the detached worktree in place"; else bad "done left the detached worktree in place" "removed despite an unreachable commit"; fi
if git -C "$REPO" cat-file -e "$DETACHED_SHA" 2>/dev/null; then ok "detached commit is still reachable in the object store"; else bad "detached commit is still reachable in the object store" "commit $DETACHED_SHA missing"; fi

# --- guard: refuses to treat a dirty shared checkout as a clean base ------
echo "half-finished lane edit" >> "$REPO/file.txt"
out=$(bash "$WT" guard "$REPO" 2>&1); rc=$?
want_exit "guard refuses a dirty shared checkout" "$rc" 1 "$out"

git -C "$REPO" checkout -q -- file.txt
out=$(bash "$WT" guard "$REPO" 2>&1); rc=$?
want_exit "guard passes a clean shared checkout" "$rc" 0 "$out"

# --- guard: also catches an untracked-only dirty tree, not just modified
# tracked files (agent-dotfiles#79 finding B) --------------------------------
echo "new file" > "$REPO/untracked.txt"
out=$(bash "$WT" guard "$REPO" 2>&1); rc=$?
want_exit "guard refuses an untracked-only dirty shared checkout" "$rc" 1 "$out"

rm -f "$REPO/untracked.txt"
out=$(bash "$WT" guard "$REPO" 2>&1); rc=$?
want_exit "guard passes after removing the untracked-only file" "$rc" 0 "$out"

# --- gc: removes a merged, clean worktree; leaves unmerged/dirty ones alone
# (agent-dotfiles#165) ------------------------------------------------------
git -C "$REPO" checkout -q -- file.txt 2>/dev/null || true

# Candidate A: branch merged into origin/main, tree clean -> gc removes it.
out=$(bash "$WT" new 165-merged "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "gc setup: new (merged case) exits 0" "$rc" 0 "$out"
MERGED_DEST="$out"
echo "merged change" >> "$MERGED_DEST/file.txt"
git -C "$MERGED_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "merged work"
git -C "$MERGED_DEST" push -q origin lane/165-merged
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --no-edit origin/lane/165-merged
git -C "$REPO" push -q origin main
git -C "$MERGED_DEST" fetch -q origin

# Candidate B: branch unmerged, tree clean -> gc must leave it. It needs a
# commit of its own -- a branch with no unique commits is trivially an
# ancestor of main and gc would (correctly) treat it as merged.
out=$(bash "$WT" new 165-unmerged "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "gc setup: new (unmerged case) exits 0" "$rc" 0 "$out"
UNMERGED_DEST="$out"
echo "unmerged change" >> "$UNMERGED_DEST/file.txt"
git -C "$UNMERGED_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "unmerged work"

# Candidate C: branch merged, tree dirty -> gc must leave it (mutation-check
# below drops exactly this guard).
out=$(bash "$WT" new 165-dirty "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "gc setup: new (dirty-but-merged case) exits 0" "$rc" 0 "$out"
DIRTY_DEST="$out"
echo "dirty merged change" >> "$DIRTY_DEST/file.txt"
git -C "$DIRTY_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "dirty-merged base"
git -C "$DIRTY_DEST" push -q origin lane/165-dirty
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --no-edit origin/lane/165-dirty
git -C "$REPO" push -q origin main
git -C "$DIRTY_DEST" fetch -q origin
echo "uncommitted on top of merged branch" >> "$DIRTY_DEST/file.txt"

gc_out=$(bash "$WT" gc "$REPO" origin/main 2>&1); gc_rc=$?
want_exit "gc exits 0 (a sweep reports, does not fail)" "$gc_rc" 0 "$gc_out"

if [ -d "$MERGED_DEST" ]; then bad "gc removes a merged, clean worktree" "$MERGED_DEST still present: $gc_out"; else ok "gc removes a merged, clean worktree"; fi
if [ -d "$UNMERGED_DEST" ]; then ok "gc leaves an unmerged worktree in place"; else bad "gc leaves an unmerged worktree in place" "removed despite being unmerged"; fi
if [ -d "$DIRTY_DEST" ]; then ok "gc leaves a merged-but-dirty worktree in place"; else bad "gc leaves a merged-but-dirty worktree in place" "removed despite uncommitted edits"; fi

# The assertion that ties this to the actual complaint: a branch gc freed can
# now be deleted, where it failed while the worktree held it.
if git -C "$REPO" branch -D lane/165-merged >/dev/null 2>&1; then
  ok "branch -D succeeds on the branch gc freed"
else
  bad "branch -D succeeds on the branch gc freed" "still held after gc"
fi
if git -C "$REPO" branch -D lane/165-unmerged >/dev/null 2>&1; then
  bad "unmerged branch should still be held by its worktree" "branch -D unexpectedly succeeded"
else
  ok "unmerged branch is still held by its worktree, as expected"
fi

# Idempotent: a second run over the same repo changes nothing further -- the
# unmerged and dirty candidates are still there, and gc reports 0 removed.
gc_out2=$(bash "$WT" gc "$REPO" origin/main 2>&1); gc_rc2=$?
want_exit "gc second run exits 0" "$gc_rc2" 0 "$gc_out2"
if grep -q "removed 0" <<<"$gc_out2"; then ok "gc is idempotent -- second run removes nothing"; else bad "gc is idempotent -- second run removes nothing" "$gc_out2"; fi
if [ -d "$UNMERGED_DEST" ] && [ -d "$DIRTY_DEST" ]; then ok "gc second run left the same worktrees untouched"; else bad "gc second run left the same worktrees untouched" "one of them disappeared"; fi

# Clean up the two survivors so the fixture directory can be removed.
git -C "$DIRTY_DEST" checkout -q -- file.txt
bash "$WT" done "$DIRTY_DEST" >/dev/null 2>&1
bash "$WT" done "$UNMERGED_DEST" >/dev/null 2>&1

rm -rf "$D"

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
