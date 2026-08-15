#!/bin/bash
# agent-supervisor#155. Same discipline test_watchdog_source_task_sweep.sh
# (#133) and test_watchdog_never_busy_lanes.sh (#112) already use for this
# file: this only ever runs watchdog.sh itself, exactly as the LaunchAgent
# would, never `LaneCompletionReconciler(...).sweep()` in isolation --
# test_reconcile_lane_completions.py already covers the sweep function
# thoroughly. If the call from watchdog.sh into
# `cli.py reconcile-lane-completions` is ever removed while
# reconcile_lane_completions.py itself stays intact, THIS goes red.
#
# The scenario is #155's own measured shape: a task dispatched and marked
# `delivered`, whose worker finished and went idle without ever running
# `lane-done.sh`'s `wait-for -S` -- no brief, no waiter, nothing announced.
# `lanes.sh` reads the pane `free` on its own, from the real (stubbed) tmux
# capture -- the ledger never has to be told.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCHDOG="$HERE/../../scripts/supervisor/watchdog.sh"
SUPERVISOR_DIR="$HERE/../../scripts/supervisor"
STUBS="$HERE/stubs"
pass=0; fail=0

D=$(mktemp -d); mkdir -p "$D/bin" "$D/transcripts"
trap 'rm -rf "$D"' EXIT

# Window 1 is the supervisor's own pane -- busy, so watchdog.sh's own
# busy/idle probe takes the quickest healthy exit and this test stays about
# the lane-completion wiring, not the restart/idle logic other tests already
# cover. Window 2 is the worker: a real Claude Code ready-prompt shape,
# idle for 400s -- well past the default 300s dwell -- with NO wait-for
# signal ever sent, exactly #155's measured shape.
cat > "$D/fixture" <<'FIX'
1|supervisor-pane|claude.exe|esc to interrupt 3s|1|0
2|as155-observed-completion|claude.exe|⏵⏵ bypass permissions on (shift+tab to cycle) · ← 1 agent|400|0
FIX
cp "$HERE/stubs/tmux-lanes" "$D/bin/tmux"
cp "$HERE/stubs/ps-lanes" "$D/bin/ps"

# Seed the ledger the way `cli.py record_dispatch` does: one lane, one
# dispatched task, stuck `delivered` -- the pre-sweep state #155 measured.
python3 - "$D" "$SUPERVISOR_DIR" <<'PY'
import sys
state_dir, supervisor_dir = sys.argv[1], sys.argv[2]
sys.path.insert(0, supervisor_dir)
from core import Ledger

ledger = Ledger(state_dir, clock=lambda: 1_000)
ledger.record_dispatch(
    lane="lanetest:2", pane_id="%2", nonce="nonce-2", harness="claude",
    repo="/repo/lanetest", server_id="server-a", session_id="$2", command="claude",
    task_id="as155-observed-completion", source_kind="issue",
    source_url="https://github.com/jonhill90/agent-supervisor/issues/155",
    source_ref="155", summary="issue #155", source_state="OPEN",
    evidence=["claimed by dispatch.sh for lane lanetest:2", "issues: 155"],
    status_marker=None,
)
PY
if [ $? -ne 0 ]; then
  echo "  FAIL fixture setup: could not seed the ledger"; fail=$((fail+1))
fi

before_status=$(AGENT_SUPERVISOR_STATE_DIR="$D" python3 "$SUPERVISOR_DIR/cli.py" status \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print([t for t in d["tasks"] if t["id"]=="as155-observed-completion"][0]["status"])')
echo "before: status=$before_status"
if [ "$before_status" = "delivered" ]; then
  echo "  ok   fixture starts delivered, matching #155's measurement"; pass=$((pass+1))
else
  echo "  FAIL fixture did not seed delivered: $before_status"; fail=$((fail+1))
fi

# Run watchdog.sh exactly once, the way the LaunchAgent does.
SUPERVISOR_PATH="$D/bin:$STUBS:/usr/bin:/bin" \
LANES_FIXTURE="$D/fixture" LANES_SESSION="lanetest" \
SUPERVISOR_STATE="$D" SUPERVISOR_STATUS="$D/st" SUPERVISOR_LOG="$D/lg" \
SUPERVISOR_STAMP="$D/stamp" SUPERVISOR_HISTORY="$D/hist" NOTIFY_ENV="$D/none.env" \
SUPERVISOR_PANE="lanetest:1.1" \
SLEEPCHECK_DIR="$D/transcripts" \
bash "$WATCHDOG" >/dev/null 2>"$D/err"

after_status=$(AGENT_SUPERVISOR_STATE_DIR="$D" python3 "$SUPERVISOR_DIR/cli.py" status \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print([t for t in d["tasks"] if t["id"]=="as155-observed-completion"][0]["status"])')
echo "after:  status=$after_status"
if [ "$after_status" = "complete" ]; then
  echo "  ok   watchdog.sh alone completed the task from observed pane state -- no wait-for signal ever sent"; pass=$((pass+1))
else
  echo "  FAIL watchdog.sh did not invoke the lane-completion sweep -- status is still '$after_status', not complete"
  echo "       stderr: $(cat "$D/err" 2>/dev/null)"
  fail=$((fail+1))
fi

if grep -q '^lane-sweep:' "$D/st" 2>/dev/null; then
  echo "  ok   watchdog.status records the lane-completion sweep outcome"; pass=$((pass+1))
else
  echo "  FAIL watchdog.status has no lane-sweep: line: $(cat "$D/st" 2>/dev/null)"
  fail=$((fail+1))
fi

lane_sweep_line=$(grep '^lane-sweep:' "$D/st" 2>/dev/null)
echo "lane-sweep line: $lane_sweep_line"
if [[ "$lane_sweep_line" == *"completed=1"*"as155-observed-completion"* ]]; then
  echo "  ok   the sweep names the lane it completed, not just a count (#118's lesson: loud, not silent)"; pass=$((pass+1))
else
  echo "  FAIL sweep succeeded (status went delivered -> complete above) but did not name the lane: $lane_sweep_line"
  fail=$((fail+1))
fi

if grep -q 'LANE-SWEEP:' "$D/lg" 2>/dev/null; then
  echo "  ok   watchdog.log carries the LANE-SWEEP line"; pass=$((pass+1))
else
  echo "  FAIL no LANE-SWEEP line in watchdog.log:"; sed 's/^/       /' "$D/lg" 2>/dev/null; fail=$((fail+1))
fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
