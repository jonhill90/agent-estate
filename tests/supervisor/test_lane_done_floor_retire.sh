#!/bin/bash
# lane-done.sh must retire a completed lane's agent process DOWN TO A FLOOR,
# not to zero -- agent-supervisor#563.
#
# WHY: every completed task used to come straight back as a live free-N lane
# holding a running agent process forever, so idle processes accumulated
# across every repo session until the host guard tripped (#563's own filed
# evidence: 23 claude processes against a ceiling of 20, two restarts). The
# naive fix -- reclaim every idle lane -- was tried the same night and broke
# `dispatch.sh` outright ("no lane rows were readable from lanes.sh"),
# because the reclaimed lanes WERE the entire dispatch pool. The load-bearing
# properties this suite proves, against a REAL tmux server and a REAL git
# worktree (a stub cannot lie about `git status`, `rename-window`, or
# whether a process is actually still alive the way it can about a canned
# reply):
#
#   1. with MORE than the floor of free lanes, completing a task ends that
#      lane's agent process (a REAL pid, checked with `kill -0`) but leaves
#      the WINDOW itself untouched, same as #564/#570's own invariant.
#   2. AT the floor, retirement does not fire -- the process survives.
#   3. a lane with uncommitted OR unpushed work is refused retirement even
#      when the floor would otherwise allow it -- the process survives, and
#      the refusal names why. Exercised against a REAL dirty git worktree,
#      not a synthetic status string.
#   4. retiring the last free lane in a session prints the warning the
#      brief asks for, so the cost is visible at the point of retirement
#      instead of surfacing later as a different-looking dispatch failure.
#
# lanes.sh's own classification (which pane content reads "free") is already
# covered by tests/supervisor/test_lanes.sh; LANE_DONE_LANES_SH lets this
# suite substitute a trivial stand-in that reports a fixed free-lane count,
# so the FLOOR decision and the retire/refuse guard below are pinned
# deterministically without re-deriving that classifier.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LANE_DONE="$HERE/../../scripts/supervisor/lane-done.sh"
source "$HERE/../../scripts/supervisor/tmux-isolation.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }

echo "lane-done.sh -- #563 floor retirement"

if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP: tmux not installed -- the floor-retirement mechanism is UNVERIFIED"
  echo "lane-done.sh -- #563 floor retirement: 0 passed, 0 failed (skipped)"
  exit 0
fi

D=$(mktemp -d)
RT="$D/tmux-rt"; mkdir -p "$RT"
rtmux() { env -u TMUX TMUX_TMPDIR="$RT" tmux -f /dev/null "$@"; }
SESS="ldftest-$$"
if ! rtmux new-session -d -s "$SESS" -n supervisor-placeholder -c "$D" 2>/dev/null; then
  echo "FATAL: could not start a throwaway tmux server under \$RT" >&2
  exit 2
fi
cleanup_rt() { unset TMUX; export TMUX_TMPDIR="$RT"; assert_isolated_tmux && tmux -f /dev/null kill-server 2>/dev/null; }
trap cleanup_rt EXIT

# A minimal origin + clone, standing in for the shared checkout -- same
# fixture shape as test_lane_retire.sh.
git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/repo"
git -C "$D/repo" config user.email test@example.com
git -C "$D/repo" config user.name "Test"
git -C "$D/repo" checkout -q -b main
echo one > "$D/repo/file.txt"
git -C "$D/repo" add file.txt
git -C "$D/repo" commit -q -m initial
git -C "$D/repo" push -q -u origin main

mk_worktree() {
  local name="$1" push="${2:-}"
  git -C "$D/repo" worktree add -q -B "lane/$name" "$D/wt-$name" main >/dev/null 2>&1
  if [ "$push" = push ]; then
    git -C "$D/wt-$name" push -q -u origin "lane/$name"
  fi
  printf '%s\n' "$D/wt-$name"
}

wait_pane_cwd() {
  local win="$1" tries=0
  while [ "$tries" -lt 20 ]; do
    [ -n "$(rtmux display-message -p -t "${SESS}:${win}" '#{pane_current_path}' 2>/dev/null)" ] && return 0
    sleep 0.1
    tries=$((tries+1))
  done
  return 1
}

# A REAL, long-running process stands in for "the agent" -- a bash loop that
# never exits on its own, so this suite can tell "still running" (kill -0
# the same pid) from "ended" (a fresh shell now owns the pane, no such pid)
# without needing a real harness's ready-prompt shape (already lanes.sh's
# own contract, covered by test_lanes.sh).
spawn_lane() {
  local win="$1" wt="$2" name="$3"
  rtmux new-window -t "${SESS}:${win}" -n "$name" -c "$wt" \
    'exec bash -c "while :; do sleep 3600; done"'
  wait_pane_cwd "$win"
}
lane_pid() { rtmux display-message -p -t "${SESS}:$1" '#{pane_pid}' 2>/dev/null; }
alive() { kill -0 "$1" 2>/dev/null; }

register_open_task() {
  local idx="$1" task="$2"
  local lane="${SESS}:${idx}"
  python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
ledger = Ledger(sys.argv[2])
ledger.record_dispatch(
    lane=sys.argv[3], pane_id="%" + sys.argv[3].replace(":", "-"), nonce="nonce-" + sys.argv[3],
    harness="claude", repo="test/repo", server_id="srv", session_id="sess",
    command="claude", task_id=sys.argv[4], source_kind="issue", source_url="https://example/1",
    source_ref="1", summary="test fixture", source_state="OPEN", evidence=["test fixture seed"],
)
' "$HERE/../../scripts/supervisor" "$D/state" "$lane" "$task"
}

# A fake lanes.sh: prints exactly $1 tab-separated placeholder rows, the
# same shape `lanes.sh --free` prints, regardless of what tmux actually
# shows -- so each scenario below pins a specific free-lane count without
# needing every lane to also carry a recognised harness's ready shape.
fake_lanes_sh() {
  local count="$1" out="$2"
  {
    echo '#!/bin/bash'
    echo "for i in \$(seq 1 $count); do printf '${SESS}:%s\\t${SESS}:@%s\\n' \"\$i\" \"\$i\"; done"
  } > "$out"
  chmod +x "$out"
}

run_done() {
  local win="$1" name="$2" chan="$3" lanes_sh="$4"
  ( sleep 1; rtmux wait-for -S "$chan" ) &
  local sigpid=$!
  local out
  out=$(AGENT_SUPERVISOR_STATE_DIR="$D/state" TMUX_TMPDIR="$RT" LANE_DONE_LANES_SH="$lanes_sh" \
    env -u TMUX timeout 6 bash "$LANE_DONE" "${SESS}:${win}" "$name" "$chan" "$SESS" 2>&1)
  local rc=$?
  wait "$sigpid" 2>/dev/null
  printf '%s\n%s\n' "$rc" "$out"
}

FREE2="$D/fake-lanes-2"; fake_lanes_sh 2 "$FREE2"
FREE1="$D/fake-lanes-1"; fake_lanes_sh 1 "$FREE1"

# ============================================================================
# 1. more than the floor -> the lane's process is ended, window survives
# ============================================================================
CLEAN_WT=$(mk_worktree clean push)
spawn_lane 2 "$CLEAN_WT" as563-clean-lane
IDX=$(rtmux list-windows -t "$SESS" -F '#{window_index} #{window_name}' | awk '/as563-clean-lane/{print $1}')
register_open_task "$IDX" as563-clean-lane
PID_BEFORE=$(lane_pid "$IDX")
alive "$PID_BEFORE" && ok "setup: the stand-in agent process is running before completion" \
  || bad "setup: the stand-in agent process is running before completion" "pid $PID_BEFORE not alive"

result=$(run_done "$IDX" as563-clean-lane ld563-clean-done "$FREE2")
rc=$(head -1 <<<"$result"); out=$(tail -n +2 <<<"$result")
want_exit "completion still exits zero" "$rc" 0 "$out"
want_contains "...and says the process was retired" "retired to the floor" "$out"
if rtmux list-windows -t "$SESS" -F '#{window_index}' | grep -qx "$IDX"; then
  ok "the window itself was NOT killed"
else
  bad "the window itself was NOT killed" "window $IDX no longer exists"
fi
if alive "$PID_BEFORE"; then
  bad "the stand-in agent process was actually ended" "pid $PID_BEFORE is still alive"
else
  ok "the stand-in agent process was actually ended"
fi
name_after=$(rtmux display-message -p -t "${SESS}:${IDX}" '#{window_name}')
want_contains "the window is renamed back to free-N" "free-${IDX}" "$name_after"

# ============================================================================
# 2. at the floor -> retirement does not fire, the process survives
# ============================================================================
CLEAN_WT2=$(mk_worktree clean2 push)
spawn_lane 3 "$CLEAN_WT2" as563-floor-lane
IDX2=$(rtmux list-windows -t "$SESS" -F '#{window_index} #{window_name}' | awk '/as563-floor-lane/{print $1}')
register_open_task "$IDX2" as563-floor-lane
PID2_BEFORE=$(lane_pid "$IDX2")

result=$(run_done "$IDX2" as563-floor-lane ld563-floor-done "$FREE1")
rc=$(head -1 <<<"$result"); out=$(tail -n +2 <<<"$result")
want_exit "completion at the floor still exits zero" "$rc" 0 "$out"
if alive "$PID2_BEFORE"; then
  ok "at the floor, the process is left running"
else
  bad "at the floor, the process is left running" "pid $PID2_BEFORE was ended even though free count == floor"
fi

# ============================================================================
# 3. uncommitted changes -> retirement refused even above the floor
# ============================================================================
DIRTY_WT=$(mk_worktree dirty)
echo "unsaved" > "$DIRTY_WT/scratch.txt"
spawn_lane 4 "$DIRTY_WT" as563-dirty-lane
IDX3=$(rtmux list-windows -t "$SESS" -F '#{window_index} #{window_name}' | awk '/as563-dirty-lane/{print $1}')
register_open_task "$IDX3" as563-dirty-lane
PID3_BEFORE=$(lane_pid "$IDX3")

result=$(run_done "$IDX3" as563-dirty-lane ld563-dirty-done "$FREE2")
rc=$(head -1 <<<"$result"); out=$(tail -n +2 <<<"$result")
want_exit "completion with a dirty worktree still exits zero (the ledger release is unaffected)" "$rc" 0 "$out"
want_contains "...but retirement is refused" "retirement refused" "$out"
want_contains "...and says why" "uncommitted changes" "$out"
if alive "$PID3_BEFORE"; then
  ok "a dirty worktree's process is left running, not ended"
else
  bad "a dirty worktree's process is left running, not ended" "pid $PID3_BEFORE was ended despite uncommitted work"
fi

# ============================================================================
# 4. retiring the LAST free lane in the session warns at the point of
#    retirement, not later as an unrelated-looking dispatch failure
# ============================================================================
CLEAN_WT4=$(mk_worktree clean4 push)
spawn_lane 5 "$CLEAN_WT4" as563-last-lane
IDX4=$(rtmux list-windows -t "$SESS" -F '#{window_index} #{window_name}' | awk '/as563-last-lane/{print $1}')
register_open_task "$IDX4" as563-last-lane

FREE1_ZEROFLOOR="$D/fake-lanes-1b"; fake_lanes_sh 1 "$FREE1_ZEROFLOOR"
( sleep 1; rtmux wait-for -S ld563-last-done ) &
sigpid=$!
out=$(AGENT_SUPERVISOR_STATE_DIR="$D/state" TMUX_TMPDIR="$RT" LANE_DONE_LANES_SH="$FREE1_ZEROFLOOR" \
  SUPERVISOR_LANE_FLOOR=0 env -u TMUX timeout 6 bash "$LANE_DONE" "${SESS}:${IDX4}" as563-last-lane ld563-last-done "$SESS" 2>&1)
rc=$?
wait "$sigpid" 2>/dev/null
want_exit "retiring the session's only free lane (floor 0) still exits zero" "$rc" 0 "$out"
want_contains "...and prints the last-free-lane warning at the point of retirement" \
  "WARNING -- this was the last free lane" "$out"

echo "lane-done.sh -- #563 floor retirement: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
