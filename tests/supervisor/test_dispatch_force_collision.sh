#!/bin/bash
# agent-supervisor#617, defect A: `--force` clears a collision refusal on
# BOTH dispatch transports, not just one.
#
# MEASURED (the issue's own reproduction, confirmed a second time in its
# first comment): `dispatch.sh --force <n> <slug> <brief> --reviews-pr <pr>
# --live-pane` dispatches through a real collision; the exact same flag on a
# plain single-issue dispatch (no --live-pane) -- the DEFAULT transport since
# #171 -- still refuses. `dispatch.sh` parses `--force` into COLLISION_FORCE
# and forwards it to its OWN collision-check call (the tmux flow, `:1735`
# before this fix), but the earlier claude-print routing branch called
# `dispatch-claude-print.sh` (which runs its OWN, separate collision-check
# call) without forwarding COLLISION_FORCE at all -- the flag was parsed and
# then silently dropped on that one path.
#
# This suite proves the fix by reproducing a REAL collision (an in-flight
# lane's worktree with an actual uncommitted change to a file the new
# dispatch's brief also names) and showing --force clears it identically on
# both transports. It also proves the MUTATION direction: with the fix
# reverted (the forwarded flag deleted from dispatch.sh's claude-print call),
# the claude-print path goes back to refusing even with --force on the
# argv -- the exact defect this issue reported.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CORE_DIR="$HERE/../../scripts/supervisor"
DISPATCH="$CORE_DIR/dispatch.sh"
export QUOTA_GATE="$HERE/stubs/quota-safe"
export SUPERVISOR_MAX_LOAD_PER_CORE=0
export SUPERVISOR_MIN_FREE_MEM_GB=0
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

echo "dispatch.sh -- --force reaches both dispatch transports (#617)"

D=$(mktemp -d); mkdir -p "$D/bin" "$D/roots"
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cp "$HERE/stubs/tmux-dispatch" "$D/bin/tmux"
cp "$HERE/stubs/claude" "$D/bin/claude"

git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/repo" 2>/dev/null
REPO="$D/repo"
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name "Test"
git -C "$REPO" checkout -q -b main
echo original > "$REPO/file.txt"
git -C "$REPO" add file.txt
git -C "$REPO" commit -q -m initial
git -C "$REPO" push -q -u origin main
git -C "$REPO" remote set-url origin "git@github.com:acme/agent-dotfiles.git"

: > "$D/prs"

one_claude_lane() {
  cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
}

run() {
  : > "$D/tmux.log"
  rm -rf "$D/panes"; mkdir -p "$D/panes"
  PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
    TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 \
    DISPATCH_RESPAWN_SETTLE=0 DISPATCH_LAUNCH_SETTLE=0 \
    DISPATCH_CONFIRM_TRIES=2 DISPATCH_SESSION_TIMEOUT=0 \
    AGENT_SUPERVISOR_STATE_DIR="$LEDGER_STATE" \
    STUB_PANE_PATH="$REPO" \
    WORKTREE_ROOT="$D/roots" bash "${DISPATCH_SCRIPT:-$DISPATCH}" "$@" 2>&1
}

# An in-flight lane with a REAL uncommitted change, built with the same
# primitives a real dispatch uses (register_lane -> reconstruct_task ->
# assign), same fixture shape test_collision_check.sh's own register_lane
# uses -- so this only exercises shapes the real ledger API can produce.
register_inflight_lane() {
  local state="$1" task="$2" lane="$3" worktree="$4"
  PYTHONPATH="$CORE_DIR" python3 -c "
import core
l = core.Ledger('$state')
l.register_lane(lane='$lane', pane_id='%1', nonce='n1', harness='claude',
  repo='$REPO', server_id='s1', session_id='sess1', command='claude')
l.reconstruct_task(task_id='$task', source_kind='issue', source_url='https://x',
  source_ref='1', summary='test', source_state='OPEN', status='created',
  evidence=['test'], status_marker=None)
l.assign(task_id='$task', lane='$lane', pane_nonce='n1', summary='test', worktree_path='$worktree')
"
}

lane_available() {
  AGENT_SUPERVISOR_STATE_DIR="$1" python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
print(Ledger(sys.argv[2]).lane_available(sys.argv[3]))
' "$CORE_DIR" "$1" "$2" 2>&1
}

# `Fix the double-counting in `file.txt`.` -- a real, backtick-quoted path
# the candidate's own brief names. The candidate itself has no diff of its
# own yet (a fresh dispatch, worktree not built until this call runs), so
# this is rule 1 (prose), the ONLY signal a plain fresh dispatch can ever
# give at the point the check runs -- exactly the shape #291's motivating
# case protects, and unaffected by this issue's defect B fix (that fix only
# changes the case where an ARTIFACT -- a real diff -- also exists).
printf 'Fix the double-counting in `file.txt`.\n' > "$D/brief.md"

# --- 1. the claude-print path (the DEFAULT since #171): a genuine collision
#        refuses without --force, and --force clears it ----------------------
one_claude_lane
echo '910|| claude-print force test' > "$D/issues"
LEDGER_STATE="$D/state-cp"
mkdir -p "$LEDGER_STATE"
INFLIGHT_CP="$D/inflight-cp"
git -C "$REPO" worktree add -q -b lane/900-inflight-cp "$INFLIGHT_CP" main
echo "in-flight change" >> "$INFLIGHT_CP/file.txt"
register_inflight_lane "$LEDGER_STATE" "as900-inflight-cp" "agent-supervisor:9" "$INFLIGHT_CP"

out_cp_noforce=$(run 910 cp-force-test "$D/brief.md" acme/agent-dotfiles "$REPO"); rc_cp_noforce=$?
want_exit "claude-print: a genuine collision refuses without --force" "$rc_cp_noforce" 1 "$out_cp_noforce"
want_contains "...naming the colliding file" "file.txt" "$out_cp_noforce"
AVAIL_CP=$(lane_available "$LEDGER_STATE" "t:3")
if [ "$AVAIL_CP" = "True" ]; then
  ok "...and the candidate lane t:3 is still free after the refusal"
else
  bad "...and the candidate lane t:3 is still free after the refusal" "lane_available: $AVAIL_CP"
fi

out_cp_force=$(run --force 910 cp-force-test2 "$D/brief.md" acme/agent-dotfiles "$REPO"); rc_cp_force=$?
want_exit "claude-print: --force clears the SAME collision and dispatches" "$rc_cp_force" 0 "$out_cp_force"
want_contains "...records the collision was forced" "forced" "$out_cp_force"
want_contains "...still routed over claude-print" "routing #910 over claude-print" "$out_cp_force"

# --- 2. --force in the OTHER documented argument position (before the
#        issue number, exactly the issue's own reproduction) -----------------
echo '911|| claude-print force test, other position' > "$D/issues"
INFLIGHT_CP2="$D/inflight-cp2"
git -C "$REPO" worktree add -q -b lane/900-inflight-cp2 "$INFLIGHT_CP2" main
echo "in-flight change" >> "$INFLIGHT_CP2/file.txt"
register_inflight_lane "$LEDGER_STATE" "as900-inflight-cp2" "agent-supervisor:10" "$INFLIGHT_CP2"
out_cp_force2=$(run 911 cp-force-test3 "$D/brief.md" acme/agent-dotfiles "$REPO" --force); rc_cp_force2=$?
want_exit "claude-print: --force after the positionals also clears the collision" "$rc_cp_force2" 0 "$out_cp_force2"

# --- 3. the live-pane path: same genuine collision, --force clears it too --
one_claude_lane
echo '912|| live-pane force test' > "$D/issues"
LEDGER_STATE="$D/state-live"
mkdir -p "$LEDGER_STATE"
INFLIGHT_LIVE="$D/inflight-live"
git -C "$REPO" worktree add -q -b lane/900-inflight-live "$INFLIGHT_LIVE" main
echo "in-flight change" >> "$INFLIGHT_LIVE/file.txt"
register_inflight_lane "$LEDGER_STATE" "as900-inflight-live" "agent-supervisor:11" "$INFLIGHT_LIVE"

out_live_noforce=$(run --live-pane 912 live-force-test "$D/brief.md" acme/agent-dotfiles "$REPO"); rc_live_noforce=$?
want_exit "live-pane: the same shaped collision refuses without --force" "$rc_live_noforce" 1 "$out_live_noforce"

out_live_force=$(run --live-pane --force 912 live-force-test2 "$D/brief.md" acme/agent-dotfiles "$REPO"); rc_live_force=$?
want_exit "live-pane: --force clears it and dispatches over tmux" "$rc_live_force" 0 "$out_live_force"
want_contains "...sent the brief over tmux" "send-keys" "$(cat "$D/tmux.log")"

# --- MUTATION CHECK: revert the fix (stop forwarding COLLISION_FORCE on the
#     claude-print call) and confirm the refusal comes back even with
#     --force on argv -- proving case 1 above is only green because the fix
#     is load-bearing, not because this fixture can never collide. ----------
MUT_DIR=$(mktemp -d "$D/mutant.XXXXXX")
cp -R "$CORE_DIR/." "$MUT_DIR/"
rm -rf "$MUT_DIR/__pycache__"
chmod +x "$MUT_DIR"/*.sh
python3 - "$MUT_DIR/dispatch.sh" <<'PY'
import sys
path = sys.argv[1]
text = open(path).read()
needle = '"$HERE/dispatch-claude-print.sh" "$ISSUE_ARG" "$SLUG" "$BRIEF" "$CLAUDE_PRINT_REPO" "$REPO_PATH" ${COLLISION_FORCE:+--force}'
replacement = '"$HERE/dispatch-claude-print.sh" "$ISSUE_ARG" "$SLUG" "$BRIEF" "$CLAUDE_PRINT_REPO" "$REPO_PATH"'
assert needle in text, "the forwarded --force call is not where this test expects -- update the mutation marker"
text = text.replace(needle, replacement)
open(path, "w").write(text)
PY
bash -n "$MUT_DIR/dispatch.sh" || bad "setup: mutant dispatch.sh is still valid bash" "bash -n failed"

one_claude_lane
echo '913|| mutation: force dropped again' > "$D/issues"
LEDGER_STATE="$D/state-mut"
mkdir -p "$LEDGER_STATE"
INFLIGHT_MUT="$D/inflight-mut"
git -C "$REPO" worktree add -q -b lane/900-inflight-mut "$INFLIGHT_MUT" main
echo "in-flight change" >> "$INFLIGHT_MUT/file.txt"
register_inflight_lane "$LEDGER_STATE" "as900-inflight-mut" "agent-supervisor:12" "$INFLIGHT_MUT"

out_mut=$(DISPATCH_SCRIPT="$MUT_DIR/dispatch.sh" run --force 913 mut-force-test "$D/brief.md" acme/agent-dotfiles "$REPO"); rc_mut=$?
want_exit "mutation confirmed: with the forward deleted, --force no longer clears the claude-print refusal (the fix above is load-bearing)" "$rc_mut" 1 "$out_mut"

rm -rf "$D"

echo
echo "dispatch.sh --force both transports: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
