#!/bin/bash
# agent-supervisor#133: PR #130 shipped `SourceTaskReconciler`/
# `cli.py reconcile-source-tasks`, but nothing called it -- `source_tasks`
# kept every row at OPEN|created and the count only climbed (314 -> 315 ->
# 322 -> 326).
#
# This test asserts the INVOCATION, not the sweep function -- calling
# `SourceTaskReconciler(...).sweep()` directly (as test_reconcile_sources.py
# already does, thoroughly) would pass against today's code and prove
# nothing about this defect. The only thing this file ever runs is
# `watchdog.sh` itself, exactly as the LaunchAgent would; the fixture proves
# the fix by seeding a real ledger row, stubbing `gh` to answer that its
# issue closed, running watchdog.sh once, and reading the row back. If the
# call from watchdog.sh into `cli.py reconcile-source-tasks` is ever removed
# while `reconcile_sources.py` itself stays intact, this goes red -- see the
# handoff note for the delete-the-wiring/restore-it proof run for this PR.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCHDOG="$HERE/../../scripts/supervisor/watchdog.sh"
SUPERVISOR_DIR="$HERE/../../scripts/supervisor"
STUBS="$HERE/stubs"
GH_STUB_DIR="$HERE/stubs-source-sweep"
pass=0; fail=0

D=$(mktemp -d)
trap 'rm -rf "$D"' EXIT

# Seed the ledger the same way `cli.py record_dispatch` does: one lane, one
# dispatched task, its source_tasks row pointing at a real-shaped GitHub
# issue URL and stuck at the pre-sweep OPEN state -- reproducing exactly
# what #133 measured on the live ledger.
python3 - "$D" "$SUPERVISOR_DIR" <<'PY'
import sys
state_dir, supervisor_dir = sys.argv[1], sys.argv[2]
sys.path.insert(0, supervisor_dir)
from core import Ledger

ledger = Ledger(state_dir, clock=lambda: 1_000)
ledger.register_lane(
    lane="free-1", pane_id="%1", nonce="nonce-1", harness="claude",
    repo="/repo/free-1", server_id="server-a", session_id="$1", command="claude",
)
ledger.record_dispatch(
    lane="free-1", pane_id="%1", nonce="nonce-1", harness="claude",
    repo="/repo/free-1", server_id="server-a", session_id="$1", command="claude",
    task_id="as133-sweep-target", source_kind="issue",
    source_url="https://github.com/jonhill90/agent-supervisor/issues/133",
    source_ref="133", summary="issue #133", source_state="OPEN",
    evidence=["claimed by dispatch.sh for lane free-1", "issues: 133"],
    status_marker=None,
)
PY
if [ $? -ne 0 ]; then
  echo "  FAIL fixture setup: could not seed the ledger"; fail=$((fail+1))
fi

before_state=$(AGENT_SUPERVISOR_STATE_DIR="$D" python3 "$SUPERVISOR_DIR/cli.py" status \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print([t for t in d["source_tasks"] if t["task_id"]=="as133-sweep-target"][0]["source_state"])')
echo "before: source_state=$before_state"
if [ "$before_state" = "OPEN" ]; then
  echo "  ok   fixture starts OPEN, matching #133's measurement"; pass=$((pass+1))
else
  echo "  FAIL fixture did not seed OPEN: $before_state"; fail=$((fail+1))
fi

# Run watchdog.sh exactly once, the way the LaunchAgent does. STUB_PANE_STATE
# busy takes the shortest healthy exit path (report working; exit 0) so this
# test is not entangled with the restart/idle logic other tests already
# cover -- the sweep must run on EVERY exit path, this one included, which is
# the whole point of it living in the trap rather than the main body.
SUPERVISOR_PATH="$GH_STUB_DIR:$STUBS:/usr/bin:/bin" \
STUB_PANE_STATE=busy \
STUB_GH_ISSUE_STATE="133=CLOSED" \
SUPERVISOR_STATE="$D" SUPERVISOR_STATUS="$D/st" SUPERVISOR_LOG="$D/lg" \
SUPERVISOR_STAMP="$D/stamp" SUPERVISOR_HISTORY="$D/hist" NOTIFY_ENV="$D/none.env" \
SLEEPCHECK_DIR="$D/transcripts" \
bash "$WATCHDOG" >/dev/null 2>"$D/err"

after_state=$(AGENT_SUPERVISOR_STATE_DIR="$D" python3 "$SUPERVISOR_DIR/cli.py" status \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print([t for t in d["source_tasks"] if t["task_id"]=="as133-sweep-target"][0]["source_state"])')
echo "after:  source_state=$after_state"
if [ "$after_state" = "CLOSED" ]; then
  echo "  ok   watchdog.sh alone reconciled the row to CLOSED"; pass=$((pass+1))
else
  echo "  FAIL watchdog.sh did not invoke the sweep -- row is still '$after_state', not CLOSED"
  fail=$((fail+1))
fi

if grep -q '^sweep:' "$D/st" 2>/dev/null; then
  echo "  ok   watchdog.status records the sweep outcome"; pass=$((pass+1))
else
  echo "  FAIL watchdog.status has no sweep: line: $(cat "$D/st" 2>/dev/null)"
  fail=$((fail+1))
fi

# PR #142 review: check_source_task_sweep()'s summary formatter was invalid
# Python (backslash-escaped quotes inside an f-string expression -- illegal
# on every CPython 3.9-3.14), so it SyntaxErrored on every invocation and
# `2>/dev/null` swallowed the evidence. A SUCCESSFUL sweep -- this fixture's
# sweep, which really did update one row -- still rendered as "not
# parseable". The prior assertion above (`grep -q '^sweep:'`) is exactly the
# shape of test the reviewer warned about: it passes whether the summary
# renders real counts or the swallowed-crash fallback text, so it could not
# have caught this. This one reads the actual line and requires real counts.
sweep_line=$(grep '^sweep:' "$D/st" 2>/dev/null)
echo "sweep line: $sweep_line"
if [[ "$sweep_line" == *"updated=1"*"unchanged=0"*"unresolved=0"*"errors=0"* ]]; then
  echo "  ok   a successful sweep renders its real counts, not a swallowed-crash fallback"
  pass=$((pass+1))
else
  echo "  FAIL sweep succeeded (row went OPEN -> CLOSED above) but its summary did not render counts: $sweep_line"
  fail=$((fail+1))
fi

# PR #142 review, point 2: the lesson is the swallowed stderr, not just the
# quoting -- a formatter that CANNOT parse its input is a different outcome
# from one that BLEW UP, and watchdog.status must be able to tell them
# apart. Force the formatter subprocess itself to crash (independent of
# whether the sweep's own JSON is well-formed) via a stub interpreter, and
# assert the crash is visible and labeled distinctly, not folded into the
# generic "not parseable" text a swallowed SyntaxError produces.
D2=$(mktemp -d)
python3 - "$D2" "$SUPERVISOR_DIR" <<'PY'
import sys
state_dir, supervisor_dir = sys.argv[1], sys.argv[2]
sys.path.insert(0, supervisor_dir)
from core import Ledger

ledger = Ledger(state_dir, clock=lambda: 1_000)
ledger.register_lane(
    lane="free-1", pane_id="%1", nonce="nonce-1", harness="claude",
    repo="/repo/free-1", server_id="server-a", session_id="$1", command="claude",
)
ledger.record_dispatch(
    lane="free-1", pane_id="%1", nonce="nonce-1", harness="claude",
    repo="/repo/free-1", server_id="server-a", session_id="$1", command="claude",
    task_id="as133-sweep-target", source_kind="issue",
    source_url="https://github.com/jonhill90/agent-supervisor/issues/133",
    source_ref="133", summary="issue #133", source_state="OPEN",
    evidence=["claimed by dispatch.sh for lane free-1", "issues: 133"],
    status_marker=None,
)
PY

SUPERVISOR_PATH="$GH_STUB_DIR:$STUBS:/usr/bin:/bin" \
SUPERVISOR_PYTHON="$GH_STUB_DIR/python3-crash" \
STUB_PANE_STATE=busy \
STUB_GH_ISSUE_STATE="133=CLOSED" \
SUPERVISOR_STATE="$D2" SUPERVISOR_STATUS="$D2/st" SUPERVISOR_LOG="$D2/lg" \
SUPERVISOR_STAMP="$D2/stamp" SUPERVISOR_HISTORY="$D2/hist" NOTIFY_ENV="$D2/none.env" \
SLEEPCHECK_DIR="$D2/transcripts" \
bash "$WATCHDOG" >/dev/null 2>"$D2/err"

crash_sweep_line=$(grep '^sweep:' "$D2/st" 2>/dev/null)
echo "crash-scenario sweep line: $crash_sweep_line"
if [[ "$crash_sweep_line" == *"crash"* ]] && [[ "$crash_sweep_line" != *"not parseable"* ]]; then
  echo "  ok   a formatter crash is labeled distinctly from an unparseable report"
  pass=$((pass+1))
else
  echo "  FAIL formatter crash was not surfaced distinctly: $crash_sweep_line"
  fail=$((fail+1))
fi
if grep -q "simulated SyntaxError in the formatter" "$D2/lg" 2>/dev/null; then
  echo "  ok   the formatter's own stderr reached the watchdog log, not /dev/null"
  pass=$((pass+1))
else
  echo "  FAIL the formatter's stderr did not reach the log: $(cat "$D2/lg" 2>/dev/null)"
  fail=$((fail+1))
fi
rm -rf "$D2"

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
