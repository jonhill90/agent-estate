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
# accepted=True (agent-supervisor#193): the real, fixed pipeline sets
# accepted_at itself the moment record_dispatch's own send is confirmed to
# have landed -- see that method's docstring. Without it the completion
# sweep this test drives now leaves the task `failed`, not `complete` (the
# whole point of #193's fix); test_reconcile_lane_completions.py's own
# test_never_accepted_task_is_failed_not_completed covers that scenario
# directly, so this integration test stays about the watchdog WIRING.
ledger.record_dispatch(
    lane="lanetest:2", pane_id="%2", nonce="nonce-2", harness="claude",
    repo="/repo/lanetest", server_id="server-a", session_id="$2", command="claude",
    task_id="as155-observed-completion", source_kind="issue",
    source_url="https://github.com/jonhill90/agent-supervisor/issues/155",
    source_ref="155", summary="issue #155", source_state="OPEN",
    evidence=["claimed by dispatch.sh for lane lanetest:2", "issues: 155"],
    status_marker=None,
    accepted=True,
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

# --- agent-supervisor#193: never-accepted must not become complete --------
# Same shape as #155's own scenario above -- a lane free past the dwell,
# no wait-for signal ever sent -- but this time record_dispatch was called
# WITHOUT accepted=True, exactly `at25-rev33`'s measured shape: a brief that
# never confirmed it landed. watchdog.sh, run through the real wiring (not
# the reconciler in isolation -- test_reconcile_lane_completions.py already
# covers that), must terminate it `failed`, never `complete`.
D2=$(mktemp -d); mkdir -p "$D2/bin" "$D2/transcripts"
cat > "$D2/fixture" <<'FIX'
1|supervisor-pane|claude.exe|esc to interrupt 3s|1|0
2|as193-never-accepted|claude.exe|⏵⏵ bypass permissions on (shift+tab to cycle) · ← 1 agent|400|0
FIX
cp "$HERE/stubs/tmux-lanes" "$D2/bin/tmux"
cp "$HERE/stubs/ps-lanes" "$D2/bin/ps"

python3 - "$D2" "$SUPERVISOR_DIR" <<'PY'
import sys
state_dir, supervisor_dir = sys.argv[1], sys.argv[2]
sys.path.insert(0, supervisor_dir)
from core import Ledger

ledger = Ledger(state_dir, clock=lambda: 1_000)
ledger.record_dispatch(
    lane="lanetest:2", pane_id="%2", nonce="nonce-2", harness="claude",
    repo="/repo/lanetest", server_id="server-a", session_id="$2", command="claude",
    task_id="as193-never-accepted", source_kind="issue",
    source_url="https://github.com/jonhill90/agent-supervisor/issues/193",
    source_ref="193", summary="issue #193", source_state="OPEN",
    evidence=["claimed by dispatch.sh for lane lanetest:2", "issues: 193"],
    status_marker=None,
    # accepted deliberately omitted -- this IS the #193 scenario.
)
PY

SUPERVISOR_PATH="$D2/bin:$STUBS:/usr/bin:/bin" \
LANES_FIXTURE="$D2/fixture" LANES_SESSION="lanetest" \
SUPERVISOR_STATE="$D2" SUPERVISOR_STATUS="$D2/st" SUPERVISOR_LOG="$D2/lg" \
SUPERVISOR_STAMP="$D2/stamp" SUPERVISOR_HISTORY="$D2/hist" NOTIFY_ENV="$D2/none.env" \
SUPERVISOR_PANE="lanetest:1.1" \
SLEEPCHECK_DIR="$D2/transcripts" \
bash "$WATCHDOG" >/dev/null 2>"$D2/err"

never_accepted_status=$(AGENT_SUPERVISOR_STATE_DIR="$D2" python3 "$SUPERVISOR_DIR/cli.py" status \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print([t for t in d["tasks"] if t["id"]=="as193-never-accepted"][0]["status"])')
echo "never-accepted after: status=$never_accepted_status"
if [ "$never_accepted_status" = "failed" ]; then
  echo "  ok   watchdog.sh terminates a never-accepted lane failed, not complete (agent-supervisor#193)"; pass=$((pass+1))
else
  echo "  FAIL a never-accepted lane should terminate failed, not '$never_accepted_status'"
  echo "       stderr: $(cat "$D2/err" 2>/dev/null)"
  fail=$((fail+1))
fi

never_accepted_line=$(grep '^lane-sweep:' "$D2/st" 2>/dev/null)
echo "lane-sweep line: $never_accepted_line"
if [[ "$never_accepted_line" == *"failed_unaccepted=1"*"as193-never-accepted"* ]]; then
  echo "  ok   the sweep names the never-accepted lane too, not just a count"; pass=$((pass+1))
else
  echo "  FAIL sweep should have named the never-accepted lane: $never_accepted_line"
  fail=$((fail+1))
fi
rm -rf "$D2"

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
