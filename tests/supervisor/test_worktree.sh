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
source "$HERE/../../scripts/supervisor/tmux-isolation.sh"
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

# gc (agent-supervisor#478) now checks tmux pane occupancy before removing
# anything. Every gc call in this suite runs against a private, throwaway
# tmux server -- never this test process's own attached session, if any --
# so "unoccupied" is measured for real instead of silently degrading to
# "tmux unavailable, keep everything" whenever this suite happens to run
# outside tmux. `-f /dev/null` skips the operator's own tmux.conf, same as
# test_lane_done.sh's real-tmux fixture.
RT="$D/tmux-rt"
mkdir -p "$RT"
rtmux() { env -u TMUX TMUX_TMPDIR="$RT" tmux -f /dev/null "$@"; }
cleanup_rt() { unset TMUX; export TMUX_TMPDIR="$RT"; assert_isolated_tmux && tmux -f /dev/null kill-server 2>/dev/null; }
ANCHOR="wt478-anchor-$$"
if ! rtmux new-session -d -s "$ANCHOR" -c "$D" 2>/dev/null; then
  echo "  FATAL: could not start a throwaway tmux server under \$RT -- gc's occupancy checks cannot be tested for real" >&2
  exit 2
fi

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

# --- agent-supervisor#427: refs/stash is shared by every worktree of one
# repo, so `git stash pop` in one lane can silently apply a DIFFERENT lane's
# uncommitted WIP as its own. `new` must install a guard that refuses to let
# a lane ADD to that shared stash -- proven against a REAL git stash push,
# not a mock, so a later change that quietly drops the guard shows up here as
# a false "ok" turning into a real contamination, not a skipped assertion.
stashA=$(bash "$WT" new 427-stashA "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "new (stash guard case A) exits 0" "$rc" 0 "$stashA"
require_dest "new (stash guard case A)" "$stashA"
stashB=$(bash "$WT" new 427-stashB "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "new (stash guard case B) exits 0" "$rc" 0 "$stashB"
require_dest "new (stash guard case B)" "$stashB"

echo "lane A work in progress" >> "$stashA/file.txt"
stash_out=$(git -C "$stashA" stash push -m "427-laneA-wip" 2>&1); stash_rc=$?
if [ "$stash_rc" -ne 0 ]; then ok "git stash push is refused inside a lane worktree (#427)"; else bad "git stash push is refused inside a lane worktree (#427)" "$stash_out"; fi
if grep -qi "427" <<<"$stash_out"; then ok "the refusal names #427"; else bad "the refusal names #427" "$stash_out"; fi
if grep -q "lane A work in progress" "$stashA/file.txt" 2>/dev/null; then ok "the refused stash push left lane A's edit in place, nothing lost"; else bad "the refused stash push left lane A's edit in place, nothing lost" "$(cat "$stashA/file.txt" 2>/dev/null)"; fi
if git -C "$stashA" stash list 2>/dev/null | grep -q .; then bad "no stash entry exists after the refusal" "$(git -C "$stashA" stash list)"; else ok "no stash entry exists after the refusal"; fi

# Lane B, an entirely different worktree, must never be able to see or pop
# whatever lane A tried to push -- the load-bearing assertion, since a guard
# that only complains but still lets the write through would not close #427.
if git -C "$stashB" stash list 2>/dev/null | grep -q .; then bad "lane B sees no stash entry from lane A" "$(git -C "$stashB" stash list)"; else ok "lane B sees no stash entry from lane A"; fi

git -C "$stashA" checkout -q -- file.txt
bash "$WT" done "$stashA" >/dev/null 2>&1
bash "$WT" done "$stashB" >/dev/null 2>&1

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
# This section is about the merge/dirty predicate, not liveness -- run
# against the isolated tmux server (so occupancy reads as a real "no" rather
# than "unavailable") with the age floor lowered to 0 so these freshly
# created fixtures clear it immediately, same as gc did before #478.
gc478() { env -u TMUX TMUX_TMPDIR="$RT" WORKTREE_GC_MIN_AGE_SECONDS=0 bash "$WT" "$@"; }
before=$(git -C "$REPO" worktree list)
dry_out=$(gc478 gc --dry-run "$REPO" origin/main 2>&1); dry_rc=$?
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

gc_out=$(gc478 gc "$REPO" origin/main 2>&1); gc_rc=$?
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
gc_out2=$(gc478 gc "$REPO" origin/main 2>&1); gc_rc2=$?
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

# --- gc (agent-supervisor#478): clean+merged is not the same question as
# "is anyone using this tree right now". Measured 2026-08-21, twice in one
# tick: a lane holding text typed-but-not-yet-submitted, or one that had just
# started, has a perfectly clean and often already-merged tree -- the old
# predicate matched it exactly and reclaimed a live lane's worktree mid-task,
# costing ~20 minutes of in-progress work on PR #489.
# Candidate G: clean, merged, UNOCCUPIED, YOUNG (just created) -> the age
# backstop keeps it, with the real default 3600s floor -- no override here.
out=$(bash "$WT" new 478-young "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "gc setup: new (young case) exits 0" "$rc" 0 "$out"
YOUNG_DEST="$out"
require_dest "new (young case)" "$YOUNG_DEST"
echo "young change" >> "$YOUNG_DEST/file.txt"
git -C "$YOUNG_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "young merged work"
git -C "$YOUNG_DEST" push -q origin lane/478-young
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --no-edit origin/lane/478-young
git -C "$REPO" push -q origin main
git -C "$YOUNG_DEST" fetch -q origin

young_out=$(env -u TMUX TMUX_TMPDIR="$RT" bash "$WT" gc "$REPO" origin/main 2>&1); young_rc=$?
want_exit "gc (young case) exits 0" "$young_rc" 0 "$young_out"
if [ -d "$YOUNG_DEST" ]; then ok "gc leaves a clean, merged, unoccupied worktree in place while it is too young (age backstop, #478)"; else bad "gc leaves a clean, merged, unoccupied worktree in place while it is too young (age backstop, #478)" "$young_out"; fi
if grep -q "liveness floor" <<<"$young_out"; then ok "the skip names the liveness floor"; else bad "the skip names the liveness floor" "$young_out"; fi

# Candidate H: clean, merged, UNOCCUPIED, OLD ENOUGH (floor lowered to 1s and
# given time to clear it) -> gc DOES remove it. This is the "gc must still
# collect something" direction -- mutation-checked below.
out=$(bash "$WT" new 478-old "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "gc setup: new (old-enough case) exits 0" "$rc" 0 "$out"
OLD_DEST="$out"
require_dest "new (old-enough case)" "$OLD_DEST"
echo "old-enough change" >> "$OLD_DEST/file.txt"
git -C "$OLD_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "old-enough merged work"
git -C "$OLD_DEST" push -q origin lane/478-old
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --no-edit origin/lane/478-old
git -C "$REPO" push -q origin main
git -C "$OLD_DEST" fetch -q origin
sleep 2

old_out=$(env -u TMUX TMUX_TMPDIR="$RT" WORKTREE_GC_MIN_AGE_SECONDS=1 bash "$WT" gc "$REPO" origin/main 2>&1); old_rc=$?
want_exit "gc (old-enough case) exits 0" "$old_rc" 0 "$old_out"
if [ -d "$OLD_DEST" ]; then bad "gc removes a clean, merged, unoccupied worktree once it clears the age floor (#478)" "$OLD_DEST still present: $old_out"; else ok "gc removes a clean, merged, unoccupied worktree once it clears the age floor (#478)"; fi

# Candidate I: clean, merged, OLD ENOUGH, but OCCUPIED by a REAL tmux pane
# -> gc must NOT remove it, no matter how clean/merged/old it looks. This is
# the dangerous direction -- #478's actual incident -- and "verify the
# instrument" means proving a real pane is actually detected, not assuming
# an occupancy check that always answers "unoccupied" would look the same.
out=$(bash "$WT" new 478-occupied "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "gc setup: new (occupied case) exits 0" "$rc" 0 "$out"
OCC_DEST="$out"
require_dest "new (occupied case)" "$OCC_DEST"
echo "occupied change" >> "$OCC_DEST/file.txt"
git -C "$OCC_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "occupied merged work"
git -C "$OCC_DEST" push -q origin lane/478-occupied
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --no-edit origin/lane/478-occupied
git -C "$REPO" push -q origin main
git -C "$OCC_DEST" fetch -q origin
sleep 2

OCC_SESSION="wt478-occupied-$$"
if rtmux new-session -d -s "$OCC_SESSION" -c "$OCC_DEST" 2>/dev/null; then
  ok "verify the instrument: a scratch tmux pane was pointed at the candidate worktree"
  occ_out=$(env -u TMUX TMUX_TMPDIR="$RT" WORKTREE_GC_MIN_AGE_SECONDS=1 bash "$WT" gc "$REPO" origin/main 2>&1); occ_rc=$?
  want_exit "gc (occupied case) exits 0" "$occ_rc" 0 "$occ_out"
  if [ -d "$OCC_DEST" ]; then ok "gc refuses to remove a worktree a live tmux pane is pointed at (#478)"; else bad "gc refuses to remove a worktree a live tmux pane is pointed at (#478)" "$occ_out"; fi
  if grep -q "tmux pane's cwd is inside it" <<<"$occ_out"; then ok "the refusal names the occupying pane as the reason"; else bad "the refusal names the occupying pane as the reason" "$occ_out"; fi
  rtmux kill-session -t "$OCC_SESSION" 2>/dev/null
else
  bad "verify the instrument: a scratch tmux pane was pointed at the candidate worktree" "tmux new-session failed -- occupancy could not be proven for real"
fi

# Candidate J: same shape as H (clean/merged/old-enough/unoccupied) but tmux
# itself is UNAVAILABLE -- an empty TMUX_TMPDIR with no server ever started
# there -- so gc must KEEP it and say why, not read blindness as permission.
out=$(bash "$WT" new 478-unavail "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "gc setup: new (tmux-unavailable case) exits 0" "$rc" 0 "$out"
UNAVAIL_DEST="$out"
require_dest "new (tmux-unavailable case)" "$UNAVAIL_DEST"
echo "unavailable-signal change" >> "$UNAVAIL_DEST/file.txt"
git -C "$UNAVAIL_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "tmux-unavailable merged work"
git -C "$UNAVAIL_DEST" push -q origin lane/478-unavail
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --no-edit origin/lane/478-unavail
git -C "$REPO" push -q origin main
git -C "$UNAVAIL_DEST" fetch -q origin
sleep 2

NO_TMUX_DIR="$D/tmux-empty"
mkdir -p "$NO_TMUX_DIR"
unavail_out=$(env -u TMUX TMUX_TMPDIR="$NO_TMUX_DIR" WORKTREE_GC_MIN_AGE_SECONDS=1 bash "$WT" gc "$REPO" origin/main 2>&1); unavail_rc=$?
want_exit "gc (tmux-unavailable case) exits 0" "$unavail_rc" 0 "$unavail_out"
if [ -d "$UNAVAIL_DEST" ]; then ok "gc keeps a clean/merged/old/unoccupied worktree when tmux itself cannot be asked (#478)"; else bad "gc keeps a clean/merged/old/unoccupied worktree when tmux itself cannot be asked (#478)" "$unavail_out"; fi
if grep -q "could not query tmux panes" <<<"$unavail_out"; then ok "the skip says tmux could not be asked, not that the tree is unoccupied"; else bad "the skip says tmux could not be asked, not that the tree is unoccupied" "$unavail_out"; fi

# --- MUTATION: disable the occupancy check -> candidate I must go RED. ------
# Proves that assertion is actually pinned to the occupancy check, not to
# the age floor or something else already refusing removal.
#
# Disables BOTH _gc_tmux_occupies and _gc_process_refs, not tmux alone.
# `rtmux new-session -c "$OCC_DEST"` starts a real shell whose CWD is
# $OCC_DEST -- before 2026-08-22 the process-cwd check was a `ps` argv grep
# and never saw that shell (its argv is just `-bash`/`zsh`, no path in it),
# so mutating tmux alone was sufficient to flip this candidate. The lsof-cwd
# rewrite (this same PR) sees that shell's cwd directly and independently
# refuses to remove it -- correct, and worth exactly this: proof that
# occupancy is now caught two ways, not a reason to leave the mutation
# testing a single-point-of-failure that no longer exists.
MUT_OCC="$D/worktree-mutant-occupancy.sh"
mut_rc=0
python3 - "$WT" "$MUT_OCC" <<'PY' || mut_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
markers = ['  _gc_tmux_occupies "$target_real"; rc=$?', '  _gc_process_refs "$target_real"; rc=$?']
for marker in markers:
    assert marker in text, f"occupancy call not found -- script shape changed: {marker!r}"
    assert text.count(marker) == 1, f"occupancy call not unique -- script shape changed: {marker!r}"
    text = text.replace(marker, marker + "; rc=1", 1)
open(dst, "w").write(text)
PY
if [ "$mut_rc" -ne 0 ]; then
  bad "setup: patched a copy of worktree.sh with the occupancy check forced to 'not occupied'" "could not patch $WT (exit $mut_rc)"
else
  ok "setup: patched a copy of worktree.sh with the occupancy check forced to 'not occupied'"
  chmod +x "$MUT_OCC"
  if rtmux new-session -d -s "$OCC_SESSION" -c "$OCC_DEST" 2>/dev/null; then
    mut_occ_out=$(env -u TMUX TMUX_TMPDIR="$RT" WORKTREE_GC_MIN_AGE_SECONDS=1 bash "$MUT_OCC" gc "$REPO" origin/main 2>&1)
    rtmux kill-session -t "$OCC_SESSION" 2>/dev/null
    if [ ! -d "$OCC_DEST" ]; then
      ok "mutation confirmed: disabling the occupancy check lets gc remove a worktree a live pane is pointed at (candidate I's GREEN assertion would now be RED)"
    else
      bad "mutation confirmed: disabling the occupancy check lets gc remove a worktree a live pane is pointed at" "expected removal on the mutant, $OCC_DEST is still present: $mut_occ_out"
    fi
  else
    bad "mutation confirmed: disabling the occupancy check lets gc remove a worktree a live pane is pointed at" "tmux new-session failed on the mutant run"
  fi
fi

# --- MUTATION: force the whole liveness predicate to always say "live" -----
# (the shape of a gc that appears to run but collects nothing, same failure
# the header warns caused an M3 Pro's 829-worktree pile-up) -> candidate H
# must go RED.
MUT_NONE="$D/worktree-mutant-nogc.sh"
mut_rc=0
python3 - "$WT" "$MUT_NONE" <<'PY' || mut_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '_gc_is_live() {'
replacement = '_gc_is_live() { return 0; }\n_gc_is_live_disabled() {'
assert marker in text, "_gc_is_live definition not found -- script shape changed"
assert text.count(marker) == 1, "_gc_is_live definition not unique -- script shape changed"
open(dst, "w").write(text.replace(marker, replacement, 1))
PY
if [ "$mut_rc" -ne 0 ]; then
  bad "setup: patched a copy of worktree.sh whose liveness check always says 'live'" "could not patch $WT (exit $mut_rc)"
else
  ok "setup: patched a copy of worktree.sh whose liveness check always says 'live'"
  chmod +x "$MUT_NONE"
  out=$(bash "$WT" new 478-nogc "$REPO" origin/main 2>/dev/null); rc=$?
  if [ "$rc" -eq 0 ] && [ -n "$out" ] && [ -d "$out" ]; then
    NOGC_DEST="$out"
    echo "nogc change" >> "$NOGC_DEST/file.txt"
    git -C "$NOGC_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "nogc merged work"
    git -C "$NOGC_DEST" push -q origin lane/478-nogc
    git -C "$REPO" fetch -q origin
    git -C "$REPO" merge -q --no-edit origin/lane/478-nogc
    git -C "$REPO" push -q origin main
    git -C "$NOGC_DEST" fetch -q origin
    sleep 2
    mut_none_out=$(env -u TMUX TMUX_TMPDIR="$RT" WORKTREE_GC_MIN_AGE_SECONDS=1 bash "$MUT_NONE" gc "$REPO" origin/main 2>&1)
    if [ -d "$NOGC_DEST" ]; then
      ok "mutation confirmed: a liveness check that always says 'live' collects nothing, even a clean/merged/old/unoccupied worktree (candidate H's GREEN assertion would now be RED)"
    else
      bad "mutation confirmed: a liveness check that always says 'live' collects nothing" "expected $NOGC_DEST to survive the mutant, it was removed: $mut_none_out"
    fi
    bash "$WT" done "$NOGC_DEST" >/dev/null 2>&1
  else
    bad "mutation confirmed: a liveness check that always says 'live' collects nothing" "setup: new (nogc case) failed, rc=$rc: $out"
  fi
fi

# --- Candidate K: clean, merged, old enough, OCCUPIED by a real process ---
# whose CWD is the worktree but whose ARGV never names it -- the exact miss
# measured 2026-08-22: `ps -eo command | grep -o '/[^ ]*worktrees[^ ]*'`
# found nothing for a live `claude` process sitting in a worktree it was
# `cd`'d into rather than launched against, while
# `lsof -a -d cwd -c claude -c node -c bash` found it immediately. `sleep`
# launched via `exec` after a `cd` reproduces the same shape without
# depending on any particular CLI being installed: its argv is just
# `sleep 90`, nothing that a `grep -F "$target_real"` over `ps` output could
# ever match.
#
# Same portability stance as test_poller_recover.sh's own lsof-off-PATH
# suite: `_gc_process_refs` itself fails CLOSED with no lsof (rc=2, "keep"),
# so a machine without it would not silently mis-collect anything -- but the
# EXACT skip message this candidate greps for only prints when lsof actually
# ran, so this candidate SKIPs rather than FAILs there instead of asserting
# a string that machine cannot produce.
if command -v lsof >/dev/null 2>&1 || [ -x /usr/sbin/lsof ]; then
out=$(bash "$WT" new 502-cwd-only "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "gc setup: new (cwd-only occupancy case) exits 0" "$rc" 0 "$out"
CWD_DEST="$out"
require_dest "new (cwd-only occupancy case)" "$CWD_DEST"
echo "cwd-only change" >> "$CWD_DEST/file.txt"
git -C "$CWD_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "cwd-only merged work"
git -C "$CWD_DEST" push -q origin lane/502-cwd-only
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --no-edit origin/lane/502-cwd-only
git -C "$REPO" push -q origin main
git -C "$CWD_DEST" fetch -q origin
sleep 2

( cd "$CWD_DEST" && exec sleep 90 ) &
CWD_PID=$!
sleep 1
if kill -0 "$CWD_PID" 2>/dev/null; then
  ok "verify the instrument: a real process is running with its cwd inside the candidate worktree"
else
  bad "verify the instrument: a real process is running with its cwd inside the candidate worktree" "background sleep did not start"
fi
cwd_occ_out=$(env -u TMUX TMUX_TMPDIR="$RT" WORKTREE_GC_MIN_AGE_SECONDS=1 bash "$WT" gc "$REPO" origin/main 2>&1); cwd_occ_rc=$?
want_exit "gc (cwd-only occupancy case) exits 0" "$cwd_occ_rc" 0 "$cwd_occ_out"
if [ -d "$CWD_DEST" ]; then ok "gc refuses to remove a worktree a real process's cwd is inside, even with no path in its argv (2026-08-22)"; else bad "gc refuses to remove a worktree a real process's cwd is inside, even with no path in its argv (2026-08-22)" "$cwd_occ_out"; fi
if grep -q "a running process's cwd is inside it" <<<"$cwd_occ_out"; then ok "the refusal names the occupying process's cwd as the reason"; else bad "the refusal names the occupying process's cwd as the reason" "$cwd_occ_out"; fi
if grep -qF "$CWD_DEST" <<<"$(ps -eo command)"; then bad "sanity: this scenario's argv genuinely does not name the worktree (if this fails, the test stopped reproducing the bug)" "ps -eo command unexpectedly contains the path"; else ok "sanity: this scenario's argv genuinely does not name the worktree -- ps -eo command | grep would have missed this exactly as measured"; fi

# With the occupying process actually gone (real kill, real unmutated gc --
# done BEFORE the mutation test below, which consumes this same worktree by
# actually removing it), the same worktree must now be free to collect --
# the "no process there -> ALLOW" half of the same guard.
kill "$CWD_PID" 2>/dev/null; wait "$CWD_PID" 2>/dev/null
gone_out=$(env -u TMUX TMUX_TMPDIR="$RT" WORKTREE_GC_MIN_AGE_SECONDS=1 bash "$WT" gc "$REPO" origin/main 2>&1)
if [ ! -d "$CWD_DEST" ]; then ok "gc removes the same worktree once the occupying process is actually gone (real code, no mutation)"; else bad "gc removes the same worktree once the occupying process is actually gone" "$gone_out"; fi

# --- MUTATION: force the process-cwd check to always say "not occupied" ---
# (the pre-2026-08-22 shape, minus even the argv grep) -> a fresh instance
# of candidate K must go RED while a real sleep process is running in it.
# Fresh worktree because the previous one was just genuinely removed above
# -- reusing it here would prove nothing about the mutant, only that an
# already-gone directory stays gone.
out=$(bash "$WT" new 502-cwd-only-mut "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "gc setup: new (cwd-only mutation case) exits 0" "$rc" 0 "$out"
CWD_DEST2="$out"
require_dest "new (cwd-only mutation case)" "$CWD_DEST2"
echo "cwd-only mutation change" >> "$CWD_DEST2/file.txt"
git -C "$CWD_DEST2" -c user.email=test@example.com -c user.name=Test commit -q -am "cwd-only mutation merged work"
git -C "$CWD_DEST2" push -q origin lane/502-cwd-only-mut
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --no-edit origin/lane/502-cwd-only-mut
git -C "$REPO" push -q origin main
git -C "$CWD_DEST2" fetch -q origin
sleep 2

( cd "$CWD_DEST2" && exec sleep 90 ) &
CWD_PID2=$!
sleep 1

MUT_PROC="$D/worktree-mutant-proc.sh"
mut_rc=0
python3 - "$WT" "$MUT_PROC" <<'PY' || mut_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '  _gc_process_refs "$target_real"; rc=$?'
replacement = '  _gc_process_refs "$target_real"; rc=$?; rc=1'
assert marker in text, "process-cwd call not found -- script shape changed"
assert text.count(marker) == 1, "process-cwd call not unique -- script shape changed"
open(dst, "w").write(text.replace(marker, replacement, 1))
PY
if [ "$mut_rc" -ne 0 ]; then
  bad "setup: patched a copy of worktree.sh with the process-cwd check forced to 'not occupied'" "could not patch $WT (exit $mut_rc)"
else
  ok "setup: patched a copy of worktree.sh with the process-cwd check forced to 'not occupied'"
  chmod +x "$MUT_PROC"
  if kill -0 "$CWD_PID2" 2>/dev/null; then
    mut_proc_out=$(env -u TMUX TMUX_TMPDIR="$RT" WORKTREE_GC_MIN_AGE_SECONDS=1 bash "$MUT_PROC" gc "$REPO" origin/main 2>&1)
    if [ ! -d "$CWD_DEST2" ]; then
      ok "mutation confirmed: disabling the process-cwd check lets gc remove a worktree a real process is sitting in (candidate K's GREEN assertion would now be RED)"
    else
      bad "mutation confirmed: disabling the process-cwd check lets gc remove a worktree a real process is sitting in" "expected removal on the mutant, $CWD_DEST2 is still present: $mut_proc_out"
    fi
  else
    bad "mutation confirmed: disabling the process-cwd check lets gc remove a worktree a real process is sitting in" "the background sleep process died before the mutant run -- cannot prove anything"
  fi
fi
kill "$CWD_PID2" 2>/dev/null; wait "$CWD_PID2" 2>/dev/null
[ -d "$CWD_DEST2" ] && rm -rf "$CWD_DEST2" 2>/dev/null   # mutant may have left it if the mutation itself failed above
else
  echo "  SKIP candidate K (cwd-only occupancy) -- no lsof on this machine (checked PATH and /usr/sbin/lsof)"
fi

# --- agent-supervisor#367: the live worktree must never be removed, even --
# --- when it is exactly the shape gc/done would otherwise happily take: ---
# --- clean, and its branch's content already merged to origin/main. ------
LIVE_DEST="$D/live"
git -C "$REPO" worktree add -q --detach "$LIVE_DEST" origin/main

# `done` refuses it directly.
out=$(SUPERVISOR_LIVE="$LIVE_DEST" bash "$WT" done "$LIVE_DEST" 2>&1); rc=$?
want_exit "done refuses to remove the live worktree (nonzero exit)" "$rc" 1 "$out"
if [ -d "$LIVE_DEST" ]; then ok "live worktree survives a 'done' call"; else bad "live worktree survives a 'done' call" "$out"; fi
if grep -qi "367\|live worktree" <<<"$out"; then ok "the refusal names the live worktree"; else bad "the refusal names the live worktree" "$out"; fi

# `gc` skips it too, even though it is clean and its content is already on
# origin/main -- exactly the predicate gc otherwise removes on.
gc_live_out=$(SUPERVISOR_LIVE="$LIVE_DEST" bash "$WT" gc "$REPO" origin/main 2>&1)
if [ -d "$LIVE_DEST" ]; then ok "live worktree survives a 'gc' sweep"; else bad "live worktree survives a 'gc' sweep" "$gc_live_out"; fi

# The guard is a realpath compare, not a string compare -- prove it still
# catches a live path reached via a trailing slash.
out2=$(SUPERVISOR_LIVE="$LIVE_DEST/" bash "$WT" done "$LIVE_DEST" 2>&1); rc2=$?
want_exit "the live guard survives a trailing slash on SUPERVISOR_LIVE (nonzero exit)" "$rc2" 1 "$out2"
if [ -d "$LIVE_DEST" ]; then ok "live worktree survives 'done' with a trailing-slash SUPERVISOR_LIVE"; else bad "live worktree survives 'done' with a trailing-slash SUPERVISOR_LIVE" "$out2"; fi

git -C "$REPO" worktree remove --force "$LIVE_DEST" >/dev/null 2>&1
git -C "$REPO" worktree prune >/dev/null 2>&1

# --- agent-supervisor#427: refs/stash is shared by every worktree of one
# repo -- git has no per-worktree stash. Prove that first, directly, so the
# rest of this section is provably testing a real hazard and not a phantom
# one: a stash pushed from the shared checkout must be visible from a lane
# worktree. Pushed from $REPO, not from a lane worktree created by `new` --
# #433 (agent-supervisor#427, the OTHER half of this same issue number)
# installs a reference-transaction hook on every worktree `new` hands out
# that refuses a `git stash push` from inside it, so proving the underlying
# hazard now has to happen from the one place that hook is never installed:
# the shared checkout itself. This is also the exact scenario #435 guards --
# "the shared repo carries an unclaimed stash" -- so pushing from $REPO is
# not a workaround, it is the real trigger.
out=$(bash "$WT" new 427-stash-a "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "new (stash case A) exits 0" "$rc" 0 "$out"
STASH_A="$out"
require_dest "new (stash case A)" "$STASH_A"
out=$(bash "$WT" new 427-stash-b "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "new (stash case B) exits 0" "$rc" 0 "$out"
STASH_B="$out"
require_dest "new (stash case B)" "$STASH_B"

echo "shared checkout's uncommitted work" >> "$REPO/file.txt"
git -C "$REPO" -c user.email=test@example.com -c user.name=Test stash push -q -m "shared WIP"
stash_seen_from_b=$(git -C "$STASH_B" stash list 2>/dev/null)
if [ -n "$stash_seen_from_b" ]; then
  ok "git worktrees genuinely share refs/stash (the #427 hazard is real, not a phantom)"
else
  bad "git worktrees genuinely share refs/stash (the #427 hazard is real, not a phantom)" "worktree B saw no stash from worktree A -- this git/version may not reproduce #427"
fi

# The fix: `new` must refuse to hand out a THIRD lane's worktree while that
# stash sits unclaimed in the shared repo -- that is the exact window #427's
# reporting lane fell into (another lane's stash surfacing mid-task).
out=$(bash "$WT" new 427-stash-c "$REPO" origin/main 2>&1); rc=$?
want_exit "new refuses to dispatch into a repo carrying an unclaimed stash" "$rc" 1 "$out"
if grep -q "unclaimed git stash" <<<"$out"; then ok "the refusal names the unclaimed stash (#427)"; else bad "the refusal names the unclaimed stash (#427)" "$out"; fi
if ls "$WORKTREE_ROOT" 2>/dev/null | grep -q "^ad-427-stash-c-"; then bad "no worktree was created for the refused dispatch" "a directory exists"; else ok "no worktree was created for the refused dispatch"; fi

# Resolving the stash clears the way for the next lane again.
git -C "$REPO" stash pop -q
out=$(bash "$WT" new 427-stash-d "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "new proceeds once the stash is resolved" "$rc" 0 "$out"
STASH_D="$out"
require_dest "new (stash resolved case)" "$STASH_D"

git -C "$REPO" checkout -q -- file.txt
bash "$WT" done "$STASH_A" >/dev/null 2>&1
bash "$WT" done "$STASH_B" >/dev/null 2>&1
bash "$WT" done "$STASH_D" >/dev/null 2>&1

# Tear down the private throwaway tmux server -- never the default socket,
# scoped to $RT and gated by assert_isolated_tmux, same as test_lane_done.sh.
cleanup_rt

rm -rf "$D"

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
