#!/bin/bash
# agent-estate#804: `worktree.sh reap <path>` is the single-target twin of
# `gc` that `reconcile_lane_completions.py`'s new worktree reaper calls once
# per just-terminated task. It must reuse `gc`'s own guard chain verbatim --
# merged/clean/not-live removed, unmerged/dirty/live survive -- against
# EXACTLY the one path it is given, never a sibling worktree.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WT="$HERE/../../scripts/supervisor/worktree.sh"
source "$HERE/../../scripts/supervisor/tmux-isolation.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
require_dest() {
  if [ -z "${2:-}" ] || [ ! -d "${2:-}" ]; then
    echo "  FATAL $1: worktree.sh new gave no usable path ('${2:-}'); aborting rather than letting git -C '' run here" >&2
    exit 2
  fi
}

echo "worktree.sh reap"

D=$(mktemp -d)

# A throwaway, isolated tmux server for the liveness case -- never the
# operator's own attached session (CLAUDE.md invariant 4).
RT="$D/tmux-rt"
mkdir -p "$RT"
rtmux() { env -u TMUX TMUX_TMPDIR="$RT" tmux -f /dev/null "$@"; }
cleanup_rt() { unset TMUX; export TMUX_TMPDIR="$RT"; assert_isolated_tmux && tmux -f /dev/null kill-server 2>/dev/null; }
ANCHOR="wt804-anchor-$$"
if ! rtmux new-session -d -s "$ANCHOR" -c "$D" 2>/dev/null; then
  echo "  FATAL: could not start a throwaway tmux server under \$RT -- reap's liveness check cannot be tested for real" >&2
  exit 2
fi

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

# `install_dispatch_origin_guard`'s pre-push hook checks the ledger for a
# dispatch record -- irrelevant to this file's own concern (reap's guard
# chain), so stub it the same way test_worktree.sh does for its own
# unrelated fixtures.
mkdir -p "$D/bin"
cat >"$D/bin/allow-python3" <<'STUB'
#!/bin/bash
echo '{"known":true,"lane":"stub:0","path":"stub","task":"stub"}'
STUB
chmod +x "$D/bin/allow-python3"
export AGENT_PYTHON_BIN="$D/bin/allow-python3"

# `_gc_is_live`'s age floor (agent-supervisor#478) defaults to 3600s so a
# lane between tool calls is never mistaken for abandoned -- every fixture
# in this file is seconds old by construction, so exercising the age-gated
# half of `_gc_is_live` (rather than just skipping every case on "too young")
# needs the same override test_worktree.sh's own #478 fixtures already use.
# `0`, not `1`: a fixture built and reaped inside the same wall-clock second
# measures age=0, and `age -lt 1` is true right on that boundary -- flaky
# without either a `sleep` this file has no other need for, or (as here) an
# age floor low enough that age=0 already clears it.
export WORKTREE_GC_MIN_AGE_SECONDS=0
export WORKTREE_ROOT="$REPO/.worktrees"
mkdir -p "$WORKTREE_ROOT"
echo ".worktrees/" >> "$REPO/.gitignore"
git -C "$REPO" add .gitignore
git -C "$REPO" commit -q -m "ignore .worktrees/"
git -C "$REPO" push -q origin main

# --- Mutation case 1: terminal, clean, merged worktree -> removed ----------
out=$(bash "$WT" new 804-merged "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "setup: new (merged case) exits 0" "$rc" 0 "$out"
MERGED_DEST="$out"
require_dest "new (merged case)" "$MERGED_DEST"
echo "merged change" >> "$MERGED_DEST/file.txt"
git -C "$MERGED_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "merged work"
git -C "$MERGED_DEST" push -q origin lane/804-merged
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --no-edit origin/lane/804-merged
git -C "$REPO" push -q origin main
git -C "$MERGED_DEST" fetch -q origin

reap_out=$(env -u TMUX TMUX_TMPDIR="$RT" bash "$WT" reap --no-github "$MERGED_DEST" origin/main 2>&1); reap_rc=$?
want_exit "reap removes a terminal, clean, merged worktree" "$reap_rc" 0 "$reap_out"
if [ -d "$MERGED_DEST" ]; then bad "the merged worktree is actually gone" "$MERGED_DEST still present: $reap_out"; else ok "the merged worktree is actually gone"; fi

# --- Mutation case 2 (the one that matters): terminal task, but the branch
# is UNMERGED (unpushed too, since it never left the worktree) -> survives.
out=$(bash "$WT" new 804-unmerged "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "setup: new (unmerged case) exits 0" "$rc" 0 "$out"
UNMERGED_DEST="$out"
require_dest "new (unmerged case)" "$UNMERGED_DEST"
echo "unmerged, unpushed change" >> "$UNMERGED_DEST/file.txt"
git -C "$UNMERGED_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "unmerged, unpushed work"

reap_out=$(env -u TMUX TMUX_TMPDIR="$RT" bash "$WT" reap --no-github "$UNMERGED_DEST" origin/main 2>&1); reap_rc=$?
want_exit "reap refuses an unmerged, unpushed branch" "$reap_rc" 1 "$reap_out"
if [ -d "$UNMERGED_DEST" ]; then ok "the unmerged worktree survives"; else bad "the unmerged worktree survives" "removed despite unmerged, unpushed work: $reap_out"; fi
if git -C "$D/origin.git" show-ref --verify --quiet refs/heads/lane/804-unmerged; then
  bad "sanity: the unmerged branch never reached the remote (if this fails, the fixture is not reproducing 'unpushed')" "refs/heads/lane/804-unmerged exists on \$D/origin.git"
else
  ok "sanity: the unmerged branch never reached the remote -- this really is testing 'unpushed', not merely 'unmerged locally'"
fi

# --- Mutation case 2b: terminal task, branch merged, but the TREE is dirty
# (uncommitted edit on top) -> survives.
out=$(bash "$WT" new 804-dirty "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "setup: new (dirty case) exits 0" "$rc" 0 "$out"
DIRTY_DEST="$out"
require_dest "new (dirty case)" "$DIRTY_DEST"
echo "dirty-but-merged base" >> "$DIRTY_DEST/file.txt"
git -C "$DIRTY_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "dirty-but-merged base"
git -C "$DIRTY_DEST" push -q origin lane/804-dirty
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --no-edit origin/lane/804-dirty
git -C "$REPO" push -q origin main
git -C "$DIRTY_DEST" fetch -q origin
echo "uncommitted on top of a merged branch" >> "$DIRTY_DEST/file.txt"

reap_out=$(env -u TMUX TMUX_TMPDIR="$RT" bash "$WT" reap --no-github "$DIRTY_DEST" origin/main 2>&1); reap_rc=$?
want_exit "reap refuses a dirty worktree even though its branch is merged" "$reap_rc" 1 "$reap_out"
if [ -d "$DIRTY_DEST" ] && grep -q "uncommitted on top of a merged branch" "$DIRTY_DEST/file.txt" 2>/dev/null; then
  ok "the dirty worktree survives with its uncommitted edit intact"
else
  bad "the dirty worktree survives with its uncommitted edit intact" "$reap_out"
fi

# --- Mutation case 2c: terminal task, branch merged, tree clean, but a live
# tmux pane's cwd is inside it -> survives (the process-inside direction).
out=$(bash "$WT" new 804-live "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "setup: new (live case) exits 0" "$rc" 0 "$out"
LIVE_DEST="$out"
require_dest "new (live case)" "$LIVE_DEST"
echo "live-but-merged work" >> "$LIVE_DEST/file.txt"
git -C "$LIVE_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "live-but-merged work"
git -C "$LIVE_DEST" push -q origin lane/804-live
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --no-edit origin/lane/804-live
git -C "$REPO" push -q origin main
git -C "$LIVE_DEST" fetch -q origin

LIVE_SESSION="wt804-live-$$"
rtmux new-session -d -s "$LIVE_SESSION" -c "$LIVE_DEST"
reap_out=$(env -u TMUX TMUX_TMPDIR="$RT" bash "$WT" reap --no-github "$LIVE_DEST" origin/main 2>&1); reap_rc=$?
want_exit "reap refuses a worktree a live tmux pane's cwd is inside" "$reap_rc" 1 "$reap_out"
if [ -d "$LIVE_DEST" ]; then ok "the live worktree survives"; else bad "the live worktree survives" "removed despite a live pane inside it: $reap_out"; fi
if grep -q "a tmux pane's cwd is inside it" <<<"$reap_out"; then ok "the refusal names the live pane as the reason"; else bad "the refusal names the live pane as the reason" "$reap_out"; fi
rtmux kill-session -t "$LIVE_SESSION" 2>/dev/null

# Now that the occupying pane is gone, the SAME worktree is reapable -- the
# "no occupant -> allowed" half of the same guard, real code, no mutation.
reap_out=$(env -u TMUX TMUX_TMPDIR="$RT" bash "$WT" reap --no-github "$LIVE_DEST" origin/main 2>&1); reap_rc=$?
want_exit "reap removes the same worktree once the occupying pane is actually gone" "$reap_rc" 0 "$reap_out"
if [ -d "$LIVE_DEST" ]; then bad "the formerly-live worktree is now gone" "$LIVE_DEST still present: $reap_out"; else ok "the formerly-live worktree is now gone"; fi

# --- Non-terminal-shaped input: a path that is not a worktree at all ------
NOT_A_WORKTREE="$D/not-a-worktree"
mkdir -p "$NOT_A_WORKTREE"
reap_out=$(env -u TMUX TMUX_TMPDIR="$RT" bash "$WT" reap --no-github "$NOT_A_WORKTREE" origin/main 2>&1); reap_rc=$?
want_exit "reap refuses a directory with no branch" "$reap_rc" 1 "$reap_out"

MISSING="$D/does-not-exist"
reap_out=$(env -u TMUX TMUX_TMPDIR="$RT" bash "$WT" reap --no-github "$MISSING" origin/main 2>&1); reap_rc=$?
want_exit "reap refuses a target that does not exist" "$reap_rc" 1 "$reap_out"

# --- Mutation, the other direction: prove the removal is load-bearing, not
# vacuous -- a patched copy of worktree.sh whose `_gc_is_live` always says
# "not live" must still correctly REMOVE the merged case above (it already
# does, case 1) while a patched copy whose `branch_content_is_on_base`
# always says "merged" must incorrectly reap the genuinely unmerged worktree
# from case 2, confirming that check is what protected it.
MUT_ALWAYS_MERGED="$D/worktree-mut-merged.sh"
mut_rc=0
python3 - "$WT" "$MUT_ALWAYS_MERGED" <<'PY' || mut_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = "branch_content_is_on_base() {\n  local repo=\"$1\" b=\"$2\" base=\"$3\"\n"
replacement = marker + "  return 0\n"
assert text.count(marker) == 1, "branch_content_is_on_base definition not unique -- script shape changed"
open(dst, "w").write(text.replace(marker, replacement, 1))
PY
if [ "$mut_rc" -ne 0 ]; then
  bad "setup: patched a copy of worktree.sh whose merged-content check always says 'merged'" "could not patch $WT (exit $mut_rc)"
else
  ok "setup: patched a copy of worktree.sh whose merged-content check always says 'merged'"
  chmod +x "$MUT_ALWAYS_MERGED"
  out=$(bash "$WT" new 804-mut-unmerged "$REPO" origin/main 2>/dev/null); rc=$?
  want_exit "setup: new (mutation case) exits 0" "$rc" 0 "$out"
  MUT_DEST="$out"
  require_dest "new (mutation case)" "$MUT_DEST"
  echo "genuinely unmerged, unpushed work" >> "$MUT_DEST/file.txt"
  git -C "$MUT_DEST" -c user.email=test@example.com -c user.name=Test commit -q -am "genuinely unmerged work"
  sleep 2
  mut_out=$(env -u TMUX TMUX_TMPDIR="$RT" bash "$MUT_ALWAYS_MERGED" reap --no-github "$MUT_DEST" origin/main 2>&1)
  if [ ! -d "$MUT_DEST" ]; then
    ok "mutation confirmed: a merged-content check that always says 'merged' reaps genuinely unmerged work (case 2's own refusal would now be RED)"
  else
    bad "mutation confirmed: a merged-content check that always says 'merged' reaps genuinely unmerged work" "expected $MUT_DEST to be removed by the mutant, it survived: $mut_out"
  fi
fi

cleanup_rt
echo "worktree.sh reap: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
