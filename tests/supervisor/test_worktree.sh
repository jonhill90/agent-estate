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

# Every fixture path below is fed to `git -C`. An empty one is not an error in
# git -- `git -C ""` is a documented no-op, so the command silently runs in
# whatever directory this test was started from. That is how a run of this file
# with a wrong path to worktree.sh committed the author's own working tree
# under the fixture's name and identity while #169 was being written. Stop the
# moment a path is missing, before any git command can be misaimed.
require_dest() {
  if [ -z "${2:-}" ] || [ ! -d "${2:-}" ]; then
    echo "  FATAL $1: worktree.sh new gave no usable path ('${2:-}'); aborting rather than letting git -C '' run here" >&2
    exit 2
  fi
}

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
require_dest "new" "$DEST"
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
require_dest "new (detach case)" "$DETACH_DEST"
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
require_dest "new (merged case)" "$MERGED_DEST"
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
require_dest "new (unmerged case)" "$UNMERGED_DEST"
echo "unmerged change" >> "$UNMERGED_DEST/file.txt"
git -C "$UNMERGED_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "unmerged work"

# Candidate C: branch merged, tree dirty -> gc must leave it (mutation-check
# below drops exactly this guard).
out=$(bash "$WT" new 165-dirty "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "gc setup: new (dirty-but-merged case) exits 0" "$rc" 0 "$out"
DIRTY_DEST="$out"
require_dest "new (dirty-but-merged case)" "$DIRTY_DEST"
echo "dirty merged change" >> "$DIRTY_DEST/file.txt"
git -C "$DIRTY_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "dirty-merged base"
git -C "$DIRTY_DEST" push -q origin lane/165-dirty
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --no-edit origin/lane/165-dirty
git -C "$REPO" push -q origin main
git -C "$DIRTY_DEST" fetch -q origin
echo "uncommitted on top of merged branch" >> "$DIRTY_DEST/file.txt"

# Candidate D: branch SQUASH-merged into main, tree clean -> gc must remove it
# (agent-dotfiles#169). A squash merge writes a new commit with no parent link
# to the branch, so the branch is not an ancestor of main and the pre-#169
# ancestry predicate refused this case forever.
#
# Two details are load-bearing, not decoration. The branch touches a path with
# a space in it, so a pathspec that word-splits matches nothing and the diff
# comes back empty -- which reads as "merged" and deletes work. And main drifts
# on an unrelated file after the squash, so an *unscoped* `main..branch` would
# report that file as a deletion: the diff must be scoped to the paths the
# branch touched or this test cannot pass.
out=$(bash "$WT" new 169-squash "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "gc setup: new (squash-merged case) exits 0" "$rc" 0 "$out"
SQUASH_DEST="$out"
require_dest "new (squash-merged case)" "$SQUASH_DEST"
echo "squashed change" >> "$SQUASH_DEST/file.txt"
echo "spaced" > "$SQUASH_DEST/a file with spaces.txt"
git -C "$SQUASH_DEST" add -A
git -C "$SQUASH_DEST" -c user.email=test@example.com -c user.name=Test commit -q -m "squash-merged work"
git -C "$SQUASH_DEST" push -q origin lane/169-squash
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --squash origin/lane/169-squash
git -C "$REPO" commit -q -m "squashed: lane/169-squash (#169)"
echo "later unrelated work on main" > "$REPO/drift.txt"
git -C "$REPO" add drift.txt
git -C "$REPO" commit -q -m "main drifts after the squash"
git -C "$REPO" push -q origin main
git -C "$SQUASH_DEST" fetch -q origin
if git -C "$REPO" merge-base --is-ancestor refs/heads/lane/169-squash origin/main 2>/dev/null; then
  bad "the squash-merged branch is genuinely not an ancestor of main" "ancestry survived -- the fixture is not reproducing #169"
else
  ok "the squash-merged branch is genuinely not an ancestor of main"
fi

# Candidates E and F: squash-merged branches whose trees are dirty in the two
# flavours the unstaged case above does not cover -- staged, and untracked-only
# (issue #169 test 5). The predicate got looser; the dirty refusal must not.
out=$(bash "$WT" new 169-staged "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "gc setup: new (squash-merged, staged-dirty case) exits 0" "$rc" 0 "$out"
STAGED_DEST="$out"
require_dest "new (staged-dirty case)" "$STAGED_DEST"
# Its own file, not file.txt: squashing another edit to file.txt into main
# would supersede candidate D's content and gc would then (correctly) refuse
# D, testing the wrong thing.
echo "staged-case work" > "$STAGED_DEST/staged-case.txt"
git -C "$STAGED_DEST" add staged-case.txt
git -C "$STAGED_DEST" -c user.email=test@example.com -c user.name=Test commit -q -m "staged-case base"
git -C "$STAGED_DEST" push -q origin lane/169-staged
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --squash origin/lane/169-staged
git -C "$REPO" commit -q -m "squashed: lane/169-staged"
git -C "$REPO" push -q origin main
git -C "$STAGED_DEST" fetch -q origin
echo "staged but uncommitted" >> "$STAGED_DEST/staged-case.txt"
git -C "$STAGED_DEST" add staged-case.txt

out=$(bash "$WT" new 169-untracked "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "gc setup: new (squash-merged, untracked-dirty case) exits 0" "$rc" 0 "$out"
UNTRACKED_DEST="$out"
require_dest "new (untracked-dirty case)" "$UNTRACKED_DEST"
echo "untracked-case work" > "$UNTRACKED_DEST/untracked-case.txt"
git -C "$UNTRACKED_DEST" add untracked-case.txt
git -C "$UNTRACKED_DEST" -c user.email=test@example.com -c user.name=Test commit -q -m "untracked-case base"
git -C "$UNTRACKED_DEST" push -q origin lane/169-untracked
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --squash origin/lane/169-untracked
git -C "$REPO" commit -q -m "squashed: lane/169-untracked"
git -C "$REPO" push -q origin main
git -C "$UNTRACKED_DEST" fetch -q origin
echo "someone's unsaved scratch file" > "$UNTRACKED_DEST/scratch.txt"

# --- gc --dry-run: says what a real run would do, changes nothing ----------
before=$(git -C "$REPO" worktree list)
dry_out=$(bash "$WT" gc --dry-run "$REPO" origin/main 2>&1); dry_rc=$?
after=$(git -C "$REPO" worktree list)
want_exit "gc --dry-run exits 0" "$dry_rc" 0 "$dry_out"
if [ "$before" = "$after" ]; then ok "gc --dry-run leaves 'git worktree list' byte-identical"; else bad "gc --dry-run leaves 'git worktree list' byte-identical" "$(diff <(echo "$before") <(echo "$after"))"; fi
if [ -d "$MERGED_DEST" ] && [ -d "$SQUASH_DEST" ]; then ok "gc --dry-run removed nothing from disk"; else bad "gc --dry-run removed nothing from disk" "a candidate worktree is gone"; fi
# git reports the resolved path (/private/var/... on macOS, where TMPDIR is a
# symlink), so compare against that -- matching the unresolved path would make
# every "does not offer to remove" assertion below pass for the wrong reason.
SQUASH_REAL=$(cd "$SQUASH_DEST" && pwd -P)
UNMERGED_REAL=$(cd "$UNMERGED_DEST" && pwd -P)
DIRTY_REAL=$(cd "$DIRTY_DEST" && pwd -P)
if grep -q "would remove $SQUASH_REAL" <<<"$dry_out"; then ok "gc --dry-run names the squash-merged worktree it would remove"; else bad "gc --dry-run names the squash-merged worktree it would remove" "$dry_out"; fi
if grep -q "would remove $UNMERGED_REAL" <<<"$dry_out"; then bad "gc --dry-run does not offer to remove the unmerged worktree" "$dry_out"; else ok "gc --dry-run does not offer to remove the unmerged worktree"; fi
if grep -q "would remove $DIRTY_REAL" <<<"$dry_out"; then bad "gc --dry-run does not offer to remove the dirty worktree" "$dry_out"; else ok "gc --dry-run does not offer to remove the dirty worktree"; fi

gc_out=$(bash "$WT" gc "$REPO" origin/main 2>&1); gc_rc=$?
# What the dry run promised is what the real run did.
dry_would=$(grep -o "gc would remove [^ ]*" <<<"$dry_out" | sed 's/.*gc would remove //' | sort)
gc_did=$(grep -o "gc removed [^ ]*" <<<"$gc_out" | sed 's/.*gc removed //' | sort)
if [ "$dry_would" = "$gc_did" ]; then ok "gc --dry-run listed exactly what the real run removed"; else bad "gc --dry-run listed exactly what the real run removed" "dry: $dry_would / real: $gc_did"; fi
want_exit "gc exits 0 (a sweep reports, does not fail)" "$gc_rc" 0 "$gc_out"

if [ -d "$MERGED_DEST" ]; then bad "gc removes a merged, clean worktree" "$MERGED_DEST still present: $gc_out"; else ok "gc removes a merged, clean worktree"; fi
if [ -d "$UNMERGED_DEST" ]; then ok "gc leaves an unmerged worktree in place"; else bad "gc leaves an unmerged worktree in place" "removed despite being unmerged"; fi
if [ -d "$DIRTY_DEST" ]; then ok "gc leaves a merged-but-dirty worktree in place"; else bad "gc leaves a merged-but-dirty worktree in place" "removed despite uncommitted edits"; fi
if [ -d "$SQUASH_DEST" ]; then bad "gc removes a squash-merged, clean worktree (#169)" "$SQUASH_DEST still present: $gc_out"; else ok "gc removes a squash-merged, clean worktree (#169)"; fi
if [ -d "$STAGED_DEST" ]; then ok "gc leaves a squash-merged worktree with staged changes in place"; else bad "gc leaves a squash-merged worktree with staged changes in place" "removed despite staged work"; fi
if [ -d "$UNTRACKED_DEST" ]; then ok "gc leaves a squash-merged worktree with untracked files in place"; else bad "gc leaves a squash-merged worktree with untracked files in place" "removed despite an untracked file"; fi
if git -C "$REPO" branch -D lane/169-squash >/dev/null 2>&1; then
  ok "branch -D succeeds on the squash-merged branch gc freed"
else
  bad "branch -D succeeds on the squash-merged branch gc freed" "still held after gc"
fi

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

# Clean up the survivors so the fixture directory can be removed.
git -C "$DIRTY_DEST" checkout -q -- file.txt
bash "$WT" done "$DIRTY_DEST" >/dev/null 2>&1
bash "$WT" done "$UNMERGED_DEST" >/dev/null 2>&1
git -C "$STAGED_DEST" reset -q --hard
bash "$WT" done "$STAGED_DEST" >/dev/null 2>&1
rm -f "$UNTRACKED_DEST/scratch.txt"
bash "$WT" done "$UNTRACKED_DEST" >/dev/null 2>&1

rm -rf "$D"

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
