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

# --- guard: refuses to treat a dirty shared checkout as a clean base ------
echo "half-finished lane edit" >> "$REPO/file.txt"
out=$(bash "$WT" guard "$REPO" 2>&1); rc=$?
want_exit "guard refuses a dirty shared checkout" "$rc" 1 "$out"

git -C "$REPO" checkout -q -- file.txt
out=$(bash "$WT" guard "$REPO" 2>&1); rc=$?
want_exit "guard passes a clean shared checkout" "$rc" 0 "$out"

rm -rf "$D"

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
