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

# agent-supervisor#562: `new` now installs a pre-push hook on every worktree
# it hands out (install_dispatch_origin_guard), refusing a push unless the
# ledger has a dispatch record for that worktree's own path. Every OTHER
# fixture in this file below (`gc`'s "already merged upstream" simulations,
# the dirty/staged/untracked cases, ...) pushes straight out of a `new`
# worktree as scaffolding for a concern that has nothing to do with #562 --
# none of them dispatch through the ledger first, and rewriting fourteen
# unrelated fixtures to seed a dispatch record each would make this file
# about #562 instead of about worktree.sh's other, older contracts. Stub
# `AGENT_PYTHON_BIN` globally to a fake interpreter that reports every
# worktree as known-dispatched, so this file's PRE-EXISTING pushes keep
# working unchanged; the "--- agent-supervisor#562" section below overrides
# this back to the real interpreter for its own pushes, because proving the
# real refusal/pass/ambiguous behavior against the real cli.py and a real
# scratch ledger is the entire point of that section.
mkdir -p "$D/bin"
cat >"$D/bin/allow-python3" <<'STUB'
#!/bin/bash
echo '{"known":true,"lane":"stub:0","path":"stub","task":"stub"}'
STUB
chmod +x "$D/bin/allow-python3"
export AGENT_PYTHON_BIN="$D/bin/allow-python3"

# `new`'s worktrees must land inside $REPO/.worktrees/ -- gc's scope filter
# (agent-supervisor#527 follow-up) now refuses anything outside it, so every
# fixture below that goes through `new` has to actually resolve there for
# the rest of this suite's gc assertions to mean what they say. Set only now
# that $REPO exists: an earlier `mkdir -p "$WORKTREE_ROOT"` would make `git
# clone` above refuse a non-empty target directory.
export WORKTREE_ROOT="$REPO/.worktrees"
mkdir -p "$WORKTREE_ROOT"
# Ignore .worktrees/ in $REPO's own status -- confirmed directly that git
# reports a directory holding a linked worktree as an untracked path in the
# repo that owns it (`?? .worktrees/`), which would make every `guard`
# assertion below see the shared checkout as permanently dirty the moment
# the first .worktrees/ fixture exists, for a reason unrelated to what
# `guard` is meant to catch.
echo ".worktrees/" >> "$REPO/.gitignore"
git -C "$REPO" add .gitignore
git -C "$REPO" commit -q -m "ignore .worktrees/"
git -C "$REPO" push -q origin main

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

# --- agent-supervisor#562: `new` must also install a pre-push guard that
# refuses to push a lane worktree the ledger never saw dispatch.sh register
# -- built from the real shape of the twelve PRs this issue measures:
# started by writing a brief straight into a pane, never dispatched, still
# green-and-approved, still refused at merge -- just far too late. This
# proves the refusal fires at PUSH TIME instead, before any review is spent.
DOG_STATE="$D/state-562"
export AGENT_SUPERVISOR_STATE_DIR="$DOG_STATE"
dog_out=$(bash "$WT" new 562-dispatch-origin "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "new (dispatch-origin guard case) exits 0" "$rc" 0 "$dog_out"
DOG_DEST="$dog_out"
require_dest "new (dispatch-origin guard case)" "$DOG_DEST"

echo "undispatched work" >> "$DOG_DEST/file.txt"
git -C "$DOG_DEST" add file.txt
git -C "$DOG_DEST" commit -q -m "562: work nobody dispatched"

# --- negative case: no dispatch record for this worktree's own path -> the
# push itself is refused, naming the actual remedy (dispatch it, or push it
# from outside a lane worktree if it is genuinely human-authored). The real
# interpreter, not this file's global allow-stub (see the note above where
# that stub is installed) -- proving the real hook's real logic is the whole
# point from here through the mutation check below.
push_out=$(AGENT_PYTHON_BIN=python3 git -C "$DOG_DEST" push origin lane/562-dispatch-origin 2>&1); push_rc=$?
want_exit "push from an undispatched worktree is refused" "$push_rc" 1 "$push_out"
if grep -qi "562" <<<"$push_out"; then ok "the refusal names #562"; else bad "the refusal names #562" "$push_out"; fi
if grep -q "dispatch this work through dispatch.sh" <<<"$push_out"; then
  ok "the refusal names the remedy (dispatch.sh)"
else
  bad "the refusal names the remedy (dispatch.sh)" "$push_out"
fi
if git -C "$D/origin.git" show-ref --verify --quiet refs/heads/lane/562-dispatch-origin; then
  bad "the refused push did not land on the remote" "refs/heads/lane/562-dispatch-origin exists on \$D/origin.git"
else
  ok "the refused push did not land on the remote"
fi

# --- ambiguous case: the ledger genuinely cannot be read (a broken
# interpreter, standing in for a wedged lock or missing state dir) -> also
# refused, but the boundary this issue names explicitly: "cannot determine"
# must print as distinguishable from "determined to be undispatched", never
# the same sentence.
amb_out=$(AGENT_PYTHON_BIN=/definitely/does/not/exist git -C "$DOG_DEST" push origin lane/562-dispatch-origin 2>&1); amb_rc=$?
want_exit "push is refused when the ledger cannot be read at all" "$amb_rc" 1 "$amb_out"
if grep -q "ambiguous" <<<"$amb_out" && ! grep -q "has no dispatch record" <<<"$amb_out"; then
  ok "an unreadable ledger reads as ambiguous, not as 'determined undispatched'"
else
  bad "an unreadable ledger reads as ambiguous, not as 'determined undispatched'" "$amb_out"
fi

# --- positive case: a real dispatch record for THIS worktree's own path (the
# same field record-dispatch --worktree writes, agent-supervisor#117) lets
# the identical push through unchanged. A guard that blocks real work is
# worse than the gap it closes -- this is the arm that proves it doesn't.
AGENT_SUPERVISOR_STATE_DIR="$DOG_STATE" python3 - "$HERE/../../scripts/supervisor" <<PY
import sys, os
sys.path.insert(0, sys.argv[1])
from core import Ledger
ledger = Ledger(os.environ["AGENT_SUPERVISOR_STATE_DIR"])
ledger.register_lane(lane="t:562", pane_id="%562", nonce="n562", harness="claude",
                      repo="$REPO", server_id="seed:1700000000", session_id="s0",
                      command="claude.exe")
ledger.reconstruct_task(task_id="as562-fix", source_kind="issue",
                         source_url="https://github.com/acme/agent-supervisor/issues/562",
                         source_ref="562", summary="dispatched, real work", source_state="OPEN",
                         status="created", evidence=["seed"], status_marker=None)
ledger.assign(task_id="as562-fix", lane="t:562", pane_nonce="n562",
              summary="dispatched, real work", worktree_path="$DOG_DEST")
PY
dispatched_out=$(AGENT_PYTHON_BIN=python3 git -C "$DOG_DEST" push origin lane/562-dispatch-origin 2>&1); dispatched_rc=$?
want_exit "push from a worktree the ledger DOES have a dispatch record for passes" "$dispatched_rc" 0 "$dispatched_out"
if git -C "$D/origin.git" show-ref --verify --quiet refs/heads/lane/562-dispatch-origin; then
  ok "the now-permitted push landed on the remote"
else
  bad "the now-permitted push landed on the remote" "$dispatched_out"
fi

# --- mutation check: prove the refusal above is load-bearing, not vacuous --
# Flip the installed hook itself (not worktree.sh's source -- the hook is
# already materialised on disk per-worktree) to always allow, on a THIRD,
# still-undispatched worktree, and confirm the same push that was refused
# above now succeeds. If this passed even with the real guard already
# broken, the earlier "refused" assertions would be proving nothing.
mut_out=$(bash "$WT" new 562-mutation "$REPO" origin/main 2>/dev/null); mut_rc=$?
want_exit "new (mutation case) exits 0" "$mut_rc" 0 "$mut_out"
MUT_DEST="$mut_out"
require_dest "new (mutation case)" "$MUT_DEST"
MUT_GIT_DIR=$(git -C "$MUT_DEST" rev-parse --git-dir)
case "$MUT_GIT_DIR" in
  /*) : ;;
  *) MUT_GIT_DIR="$MUT_DEST/$MUT_GIT_DIR" ;;
esac
MUT_HOOKS_DIR=$(git -C "$MUT_DEST" config --worktree core.hooksPath)
printf '#!/bin/bash\nexit 0\n' > "$MUT_HOOKS_DIR/pre-push"
chmod +x "$MUT_HOOKS_DIR/pre-push"
echo "still undispatched" >> "$MUT_DEST/file.txt"
git -C "$MUT_DEST" add file.txt
git -C "$MUT_DEST" commit -q -m "562: mutation case, still undispatched"
mut_push_out=$(git -C "$MUT_DEST" push origin lane/562-mutation 2>&1); mut_push_rc=$?
if [ "$mut_push_rc" -eq 0 ]; then
  ok "mutation confirmed: disabling the pre-push hook lets an undispatched worktree's push through (the negative-case assertions above would now be RED)"
else
  bad "mutation confirmed: disabling the pre-push hook lets an undispatched worktree's push through" "expected the disabled hook to let this through, still refused: $mut_push_out"
fi

unset AGENT_SUPERVISOR_STATE_DIR
bash "$WT" done "$DOG_DEST" >/dev/null 2>&1
bash "$WT" done "$MUT_DEST" >/dev/null 2>&1

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

# --- gc: scoped to $REPO/.worktrees/ (agent-supervisor#527 follow-up) ------
# `git worktree list --porcelain` answers for every worktree git knows about
# for this repo, wherever it was registered on disk -- not just
# $REPO/.worktrees/. Measured on the live estate: two registered worktrees
# sat outside any repo's .worktrees/ tree, one under a macOS temp dir, one
# under an unrelated loop's own state directory holding that loop's own
# operational memory files -- a live sweep reaching either would delete
# state or an unrelated tree, not disposable code.
#
# Two REAL registered worktrees, both otherwise fully gc-eligible (clean,
# merged, unoccupied, old enough via the same age-floor-lowered gc478
# helper the rest of this file already uses): one registered OUTSIDE
# $REPO/.worktrees/ (a sibling mktemp -d, standing in for a temp dir or
# another loop's own state directory), one INSIDE it. The outside one must
# never be offered, even though it would otherwise qualify for removal; the
# inside one must still be.
OUTSIDE_ROOT=$(mktemp -d)
OUTSIDE_DEST="$OUTSIDE_ROOT/scope-outside"
git -C "$REPO" worktree add -q -b lane/527-scope-outside "$OUTSIDE_DEST" origin/main
echo "outside-scope change" >> "$OUTSIDE_DEST/file.txt"
git -C "$OUTSIDE_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "outside-scope merged work"
git -C "$OUTSIDE_DEST" push -q origin lane/527-scope-outside
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --no-edit origin/lane/527-scope-outside
git -C "$REPO" push -q origin main
git -C "$OUTSIDE_DEST" fetch -q origin

INSIDE_DEST="$WORKTREE_ROOT/scope-inside"
git -C "$REPO" worktree add -q -b lane/527-scope-inside "$INSIDE_DEST" origin/main
echo "inside-scope change" >> "$INSIDE_DEST/file.txt"
git -C "$INSIDE_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "inside-scope merged work"
git -C "$INSIDE_DEST" push -q origin lane/527-scope-inside
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --no-edit origin/lane/527-scope-inside
git -C "$REPO" push -q origin main
git -C "$INSIDE_DEST" fetch -q origin

OUTSIDE_REAL=$(cd "$OUTSIDE_DEST" && pwd -P)
INSIDE_REAL=$(cd "$INSIDE_DEST" && pwd -P)

scope_dry=$(gc478 gc --dry-run "$REPO" origin/main 2>&1); scope_dry_rc=$?
want_exit "gc --dry-run (scope test) exits 0" "$scope_dry_rc" 0 "$scope_dry"
if grep -q "would remove $OUTSIDE_REAL" <<<"$scope_dry"; then bad "gc --dry-run never offers a worktree registered outside \$REPO/.worktrees/" "$scope_dry"; else ok "gc --dry-run never offers a worktree registered outside \$REPO/.worktrees/"; fi
if grep -q "gc skipping $OUTSIDE_REAL -- outside .*\.worktrees/" <<<"$scope_dry"; then ok "the skip names the .worktrees/ scope, not some other reason"; else bad "the skip names the .worktrees/ scope, not some other reason" "$scope_dry"; fi
if grep -q "would remove $INSIDE_REAL" <<<"$scope_dry"; then ok "gc --dry-run still offers a worktree registered inside \$REPO/.worktrees/"; else bad "gc --dry-run still offers a worktree registered inside \$REPO/.worktrees/" "$scope_dry"; fi

scope_out=$(gc478 gc "$REPO" origin/main 2>&1); scope_rc=$?
want_exit "gc (scope test) exits 0" "$scope_rc" 0 "$scope_out"
if [ -d "$OUTSIDE_DEST" ]; then ok "gc leaves the outside-.worktrees/ worktree in place, no matter how clean/merged/old/unoccupied it looks"; else bad "gc leaves the outside-.worktrees/ worktree in place" "$OUTSIDE_DEST was removed: $scope_out"; fi
if [ -d "$INSIDE_DEST" ]; then bad "gc removes the inside-.worktrees/ worktree" "$INSIDE_DEST still present: $scope_out"; else ok "gc removes the inside-.worktrees/ worktree"; fi

# --- MUTATION: disable the .worktrees/ scope filter -> the outside
# candidate (still on disk -- correctly preserved above) must go RED,
# proving the assertion above is pinned to the scope filter and not to
# something else already refusing this candidate (e.g. liveness or the
# merge predicate).
MUT_SCOPE="$D/worktree-mutant-scope.sh"
mut_rc=0
python3 - "$WT" "$MUT_SCOPE" <<'PY' || mut_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''    in_scope=1
    if [ -z "$WORKTREES_ROOT_REAL" ]; then
      in_scope=0
    else
      case "$p_real" in
        "$WORKTREES_ROOT_REAL"|"$WORKTREES_ROOT_REAL"/*) : ;;
        *) in_scope=0 ;;
      esac
    fi'''
assert marker in text, "scope-filter block not found -- script shape changed"
assert text.count(marker) == 1, "scope-filter block not unique -- script shape changed"
open(dst, "w").write(text.replace(marker, "    in_scope=1", 1))
PY
if [ "$mut_rc" -ne 0 ]; then
  bad "setup: patched a copy of worktree.sh with the .worktrees/ scope filter disabled" "could not patch $WT (exit $mut_rc)"
else
  ok "setup: patched a copy of worktree.sh with the .worktrees/ scope filter disabled"
  chmod +x "$MUT_SCOPE"
  mut_scope_out=$(env -u TMUX TMUX_TMPDIR="$RT" WORKTREE_GC_MIN_AGE_SECONDS=0 bash "$MUT_SCOPE" gc "$REPO" origin/main 2>&1)
  if [ ! -d "$OUTSIDE_DEST" ]; then
    ok "mutation confirmed: disabling the .worktrees/ scope filter lets gc remove a worktree registered outside it (the outside candidate's GREEN assertion would now be RED)"
  else
    bad "mutation confirmed: disabling the .worktrees/ scope filter lets gc remove a worktree registered outside it" "expected removal on the mutant, $OUTSIDE_DEST is still present: $mut_scope_out"
  fi
fi
rm -rf "$OUTSIDE_ROOT" 2>/dev/null
git -C "$REPO" worktree prune >/dev/null 2>&1

# --- gc: squash-merged, then superseded -- PR-merged fallback (#682) ------
# `branch_content_is_on_base`'s own scoped-diff test says "not merged" once
# `origin/main` has moved on and touched the same paths again after a squash
# merge -- the gap that function's own comment and this file's header
# document by measurement ("17 with a MERGED PR whose files main has since
# changed again: merged, then superseded"). A branch's own MERGED PR record
# is the second, independent signal `gc` now uses to still recognise that
# case as safe -- via a stubbed `gh` on PATH, same shape and stub file
# branch-sweep.sh's own tests already use
# (tests/supervisor/stubs-branch-sweep/gh, STUB_GH_PR_ROWS).
GH_STUB_DIR="$HERE/stubs-branch-sweep"
gc682() { env -u TMUX TMUX_TMPDIR="$RT" WORKTREE_GC_MIN_AGE_SECONDS=0 PATH="$GH_STUB_DIR:$PATH" "$@"; }

out=$(bash "$WT" new 682-super "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "gc setup: new (squash-merged-then-superseded case) exits 0" "$rc" 0 "$out"
SUPER_DEST="$out"
require_dest "new (squash-merged-then-superseded case)" "$SUPER_DEST"
echo "squashed-then-superseded change" > "$SUPER_DEST/super.txt"
git -C "$SUPER_DEST" add super.txt
git -C "$SUPER_DEST" -c user.email=test@example.com -c user.name=Test commit -q -m "squash-merged, later superseded"
git -C "$SUPER_DEST" push -q origin lane/682-super
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --squash origin/lane/682-super
git -C "$REPO" commit -q -m "squashed: lane/682-super"
echo "later commit on main touches the same file again" >> "$REPO/super.txt"
git -C "$REPO" commit -q -am "main supersedes what the squash merge added"
git -C "$REPO" push -q origin main
git -C "$SUPER_DEST" fetch -q origin
SUPER_REAL=$(cd "$SUPER_DEST" && pwd -P)

# Baseline: verify the instrument first. Without the GitHub cross-check
# (--no-github, same as gh being entirely unavailable), the content check
# alone must still leave this candidate stuck -- proving this fixture
# reproduces the documented gap, not something the content check already
# handled.
nogh_out=$(STUB_GH_PR_ROWS=$'lane/682-super\tMERGED' gc682 bash "$WT" gc --dry-run --no-github "$REPO" origin/main 2>&1)
if grep -q "would remove $SUPER_REAL" <<<"$nogh_out"; then
  bad "gc --no-github leaves the squash-merged-then-superseded worktree stuck (baseline, #682)" "$nogh_out"
else
  ok "gc --no-github leaves the squash-merged-then-superseded worktree stuck (baseline, #682)"
fi

# With the cross-check: the stub reports lane/682-super as MERGED, so gc
# must now remove it, and say why.
super_dry=$(STUB_GH_PR_ROWS=$'lane/682-super\tMERGED' gc682 bash "$WT" gc --dry-run "$REPO" origin/main 2>&1)
if grep -q "would remove $SUPER_REAL" <<<"$super_dry"; then ok "gc --dry-run offers the squash-merged-then-superseded worktree once its PR is on record as MERGED (#682)"; else bad "gc --dry-run offers the squash-merged-then-superseded worktree once its PR is on record as MERGED (#682)" "$super_dry"; fi
if grep -q "would remove $SUPER_REAL.*MERGED PR" <<<"$super_dry"; then ok "the offer names the MERGED PR as the reason, not the content check"; else bad "the offer names the MERGED PR as the reason, not the content check" "$super_dry"; fi

super_out=$(STUB_GH_PR_ROWS=$'lane/682-super\tMERGED' gc682 bash "$WT" gc "$REPO" origin/main 2>&1)
if [ -d "$SUPER_DEST" ]; then bad "gc removes a squash-merged-then-superseded worktree once its PR is on record as MERGED (#682)" "$SUPER_DEST still present: $super_out"; else ok "gc removes a squash-merged-then-superseded worktree once its PR is on record as MERGED (#682)"; fi

# --- gc: genuinely unmerged content is still refused even with the GitHub
# cross-check active (#682, the other direction) -----------------------
out=$(bash "$WT" new 682-real-unmerged "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "gc setup: new (genuinely unmerged, gh cross-check active) exits 0" "$rc" 0 "$out"
REALUNMERGED_DEST="$out"
require_dest "new (genuinely unmerged, gh cross-check active)" "$REALUNMERGED_DEST"
echo "never merged anywhere" > "$REALUNMERGED_DEST/never-merged.txt"
git -C "$REALUNMERGED_DEST" add never-merged.txt
git -C "$REALUNMERGED_DEST" -c user.email=test@example.com -c user.name=Test commit -q -m "genuinely unmerged work"
git -C "$REALUNMERGED_DEST" push -q origin lane/682-real-unmerged
REALUNMERGED_REAL=$(cd "$REALUNMERGED_DEST" && pwd -P)

# The stub's merged-PR set names an unrelated branch only -- lane/682-real-
# unmerged must not be swept in just because the cross-check ran at all.
realunmerged_out=$(STUB_GH_PR_ROWS=$'lane/999-unrelated\tMERGED' gc682 bash "$WT" gc "$REPO" origin/main 2>&1)
if [ -d "$REALUNMERGED_DEST" ]; then ok "gc leaves a genuinely unmerged worktree in place even with the GitHub cross-check active (#682)"; else bad "gc leaves a genuinely unmerged worktree in place even with the GitHub cross-check active (#682)" "$REALUNMERGED_DEST removed: $realunmerged_out"; fi
if grep -q "no MERGED PR is on record" <<<"$realunmerged_out"; then ok "the refusal says no MERGED PR is on record, not just 'not merged'"; else bad "the refusal says no MERGED PR is on record, not just 'not merged'" "$realunmerged_out"; fi

# --- MUTATION: force the PR-merged fallback to always say yes -> the
# genuinely-unmerged candidate above (still on disk, correctly preserved)
# must go RED, proving the two assertions above are pinned to a real check
# reading the merged-PR file, not something else already refusing it.
MUT_PR="$D/worktree-mutant-pr-merged.sh"
mut_pr_rc=0
python3 - "$WT" "$MUT_PR" <<'PY' || mut_pr_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''_gc_pr_merged_from_file() {
  local merged_file="$1" b="$2"
  [ -n "$merged_file" ] && [ -f "$merged_file" ] || return 1
  grep -qxF "$b" "$merged_file"
}'''
assert marker in text, "_gc_pr_merged_from_file not found -- script shape changed"
assert text.count(marker) == 1, "_gc_pr_merged_from_file not unique -- script shape changed"
open(dst, "w").write(text.replace(marker, "_gc_pr_merged_from_file() {\n  return 0\n}", 1))
PY
if [ "$mut_pr_rc" -ne 0 ]; then
  bad "setup: patched a copy of worktree.sh with the PR-merged fallback forced to 'yes'" "could not patch $WT (exit $mut_pr_rc)"
else
  ok "setup: patched a copy of worktree.sh with the PR-merged fallback forced to 'yes'"
  chmod +x "$MUT_PR"
  mut_pr_out=$(STUB_GH_PR_ROWS=$'lane/999-unrelated\tMERGED' gc682 bash "$MUT_PR" gc "$REPO" origin/main 2>&1)
  if [ ! -d "$REALUNMERGED_DEST" ]; then
    ok "mutation confirmed: forcing the PR-merged fallback to always say yes lets gc remove a genuinely unmerged worktree (the refusal's GREEN assertion would now be RED)"
  else
    bad "mutation confirmed: forcing the PR-merged fallback to always say yes lets gc remove a genuinely unmerged worktree" "expected removal on the mutant, $REALUNMERGED_DEST is still present: $mut_pr_out"
  fi
fi
git -C "$REPO" worktree prune >/dev/null 2>&1

# --- gc: WORKTREE_GC_EXTRA_ROOTS opts an external root into scope, but
# only for candidates matching `new`'s own ad-<slug>-<pid> naming (#682) --
EXTRA_ROOT=$(mktemp -d)
AD_NAMED_DEST="$EXTRA_ROOT/ad-682-extraroot-$$"
git -C "$REPO" worktree add -q -b lane/682-extraroot "$AD_NAMED_DEST" origin/main
echo "extra-root, ad-named change" >> "$AD_NAMED_DEST/file.txt"
git -C "$AD_NAMED_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "extra-root ad-named merged work"
git -C "$AD_NAMED_DEST" push -q origin lane/682-extraroot
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --no-edit origin/lane/682-extraroot
git -C "$REPO" push -q origin main
git -C "$AD_NAMED_DEST" fetch -q origin

PLAIN_NAMED_DEST="$EXTRA_ROOT/plain-name-$$"
git -C "$REPO" worktree add -q -b lane/682-plainname "$PLAIN_NAMED_DEST" origin/main
echo "extra-root, plain-named change" >> "$PLAIN_NAMED_DEST/file.txt"
git -C "$PLAIN_NAMED_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "extra-root plain-named merged work"
git -C "$PLAIN_NAMED_DEST" push -q origin lane/682-plainname
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --no-edit origin/lane/682-plainname
git -C "$REPO" push -q origin main
git -C "$PLAIN_NAMED_DEST" fetch -q origin

AD_NAMED_REAL=$(cd "$AD_NAMED_DEST" && pwd -P)
PLAIN_NAMED_REAL=$(cd "$PLAIN_NAMED_DEST" && pwd -P)

# Neither is offered without opting the root in -- same default as every
# other worktree registered outside $REPO/.worktrees/.
noextra_out=$(gc478 gc --dry-run "$REPO" origin/main 2>&1)
if grep -q "would remove $AD_NAMED_REAL" <<<"$noextra_out"; then bad "gc --dry-run never offers an ad-named worktree outside .worktrees/ without WORKTREE_GC_EXTRA_ROOTS" "$noextra_out"; else ok "gc --dry-run never offers an ad-named worktree outside .worktrees/ without WORKTREE_GC_EXTRA_ROOTS"; fi

# Opting EXTRA_ROOT in: the ad-<slug>-<pid>-named candidate is now offered,
# the plain-named one in the SAME opted-in root is still not -- the pattern
# match is a whitelist, not "everything under this root now qualifies".
extra_out=$(env -u TMUX TMUX_TMPDIR="$RT" WORKTREE_GC_MIN_AGE_SECONDS=0 WORKTREE_GC_EXTRA_ROOTS="$EXTRA_ROOT" bash "$WT" gc --dry-run "$REPO" origin/main 2>&1)
if grep -q "would remove $AD_NAMED_REAL" <<<"$extra_out"; then ok "gc --dry-run offers an ad-named worktree once its root is opted in via WORKTREE_GC_EXTRA_ROOTS (#682)"; else bad "gc --dry-run offers an ad-named worktree once its root is opted in via WORKTREE_GC_EXTRA_ROOTS (#682)" "$extra_out"; fi
if grep -q "would remove $PLAIN_NAMED_REAL" <<<"$extra_out"; then bad "gc --dry-run still refuses a plain-named worktree in the SAME opted-in root (naming, not root, gates scope)" "$extra_out"; else ok "gc --dry-run still refuses a plain-named worktree in the SAME opted-in root (naming, not root, gates scope)"; fi

extra_real_out=$(env -u TMUX TMUX_TMPDIR="$RT" WORKTREE_GC_MIN_AGE_SECONDS=0 WORKTREE_GC_EXTRA_ROOTS="$EXTRA_ROOT" bash "$WT" gc "$REPO" origin/main 2>&1)
if [ -d "$AD_NAMED_DEST" ]; then bad "gc removes an ad-named worktree once its root is opted in" "$AD_NAMED_DEST still present: $extra_real_out"; else ok "gc removes an ad-named worktree once its root is opted in"; fi
if [ -d "$PLAIN_NAMED_DEST" ]; then ok "gc leaves a plain-named worktree in the same opted-in root untouched"; else bad "gc leaves a plain-named worktree in the same opted-in root untouched" "$PLAIN_NAMED_DEST removed: $extra_real_out"; fi
rm -rf "$EXTRA_ROOT" 2>/dev/null
git -C "$REPO" worktree prune >/dev/null 2>&1

# Tear down the private throwaway tmux server -- never the default socket,
# scoped to $RT and gated by assert_isolated_tmux, same as test_lane_done.sh.
cleanup_rt

rm -rf "$D"

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
