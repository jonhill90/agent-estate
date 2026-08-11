#!/bin/bash
# Behaviour tests for watchdog.sh using stub tmux/gh binaries.
#
# These exist because three bugs shipped in this script for want of a test:
# an inverted ghost-text comparison, a failed `gh` query counted as zero work,
# and a /loop delivered into a busy pane where it queues as inert plain text.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCHDOG="$HERE/../../scripts/supervisor/watchdog.sh"
STUBS="$HERE/stubs"
pass=0; fail=0
check() { # check <name> <expected-substring> <file>
  if grep -q "$2" "$3" 2>/dev/null; then echo "  ok   $1"; pass=$((pass+1));
  else echo "  FAIL $1 — expected '$2' in $(cat "$3" 2>/dev/null | tr '\n' ' ')"; fail=$((fail+1)); fi
}
run() { # run <state> <workdir>
  # An empty transcript dir by default: sleepcheck finds no pending wakeup, so
  # these tests exercise the watchdog's own decisions rather than the live
  # supervisor's sleep state. Without this the suite passes or fails depending
  # on whether the real loop happens to be asleep when it runs.
  rm -rf "$2"; mkdir -p "$2" "$2/transcripts"
  SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE="$1" STUB_SENT="$2/sent" \
  STUB_BUSY_AFTER="${STUB_BUSY_AFTER:-}" STUB_COUNTER="$2/counter" \
  SUPERVISOR_STATE="$2" SUPERVISOR_STATUS="$2/st" SUPERVISOR_LOG="$2/lg" \
  SUPERVISOR_STAMP="$2/stamp" SUPERVISOR_HISTORY="$2/hist" NOTIFY_ENV="$2/none.env" \
  SLEEPCHECK_DIR="${STUB_SLEEPCHECK_DIR:-$2/transcripts}" \
  bash "$WATCHDOG" >/dev/null 2>&1
}

echo "watchdog.sh"

# A busy supervisor is working, not dead. Nothing may be sent to it.
D=$(mktemp -d); run busy "$D/w"
check "busy pane reports working" "state:    working" "$D/w/st"
[ ! -s "$D/w/sent" ] && { echo "  ok   busy pane receives no keystrokes"; pass=$((pass+1)); } \
                     || { echo "  FAIL busy pane was sent: $(cat "$D/w/sent")"; fail=$((fail+1)); }

# An idle pane with work is a dead loop: restart it, and the /loop must
# actually be delivered.
D=$(mktemp -d); run idle "$D/w"
check "idle pane with work restarts" "state:    restarted" "$D/w/st"
check "restart delivers a /loop"     "/loop" "$D/w/sent"

# The race: idle when first checked, busy by the time the /loop is sent.
# Without the pre-send guard the command is queued as plain text, never
# parses as a slash command, and the loop silently never re-arms.
D=$(mktemp -d); STUB_BUSY_AFTER=1 run idle "$D/w"
check "pane that turns busy mid-probe is not sent to" "state:    working" "$D/w/st"
if grep -q '/loop' "$D/w/sent" 2>/dev/null; then
  echo "  FAIL a /loop was delivered into a busy pane"; fail=$((fail+1))
else
  echo "  ok   no /loop delivered into a busy pane"; pass=$((pass+1))
fi

# A loop with a pending wakeup is asleep, not dead. The watchdog must leave it
# alone even though the pane is idle and there is queued work -- restarting a
# sleeping loop is what churned the supervisor all night before #59.
D=$(mktemp -d); mkdir -p "$D/sleeping"
python3 - "$D/sleeping/t.jsonl" <<'PYEOF'
import json, sys, datetime
stamp = (datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(seconds=60)).strftime("%Y-%m-%dT%H:%M:%S.000Z")
rec = {"timestamp": stamp, "message": {"content": [
    {"type": "tool_use", "name": "ScheduleWakeup", "input": {"delaySeconds": 3600}}]}}
open(sys.argv[1], "w").write(json.dumps(rec) + "\n")
PYEOF
STUB_SLEEPCHECK_DIR="$D/sleeping" run idle "$D/w"
check "a sleeping loop is left alone" "state:    asleep" "$D/w/st"
if grep -q '/loop' "$D/w/sent" 2>/dev/null; then
  echo "  FAIL a sleeping loop was restarted"; fail=$((fail+1))
else
  echo "  ok   a sleeping loop receives no keystrokes"; pass=$((pass+1))
fi

# The status file must name the code that produced it. The LaunchAgent runs
# this script from the repo working tree, so the live guard is whatever branch
# is checked out -- an invisible dependency until it is printed.
D=$(mktemp -d); run idle "$D/w"
check "status names the running branch and sha" "^code:" "$D/w/st"

# A missing state directory used to make every status write fail silently:
# exit 0, and watchdog.status quietly stops updating -- indistinguishable from
# a dead cron, which is the condition this tool exists to detect.
D=$(mktemp -d); rm -rf "$D/absent"
SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE=busy \
SUPERVISOR_STATE="$D/absent" SLEEPCHECK_DIR="$D/none" \
  bash "$WATCHDOG" >/dev/null 2>&1
if [ -f "$D/absent/watchdog.status" ]; then
  echo "  ok   a missing state directory is created, not failed into silently"; pass=$((pass+1))
else
  echo "  FAIL status was not written when the state directory was absent"; fail=$((fail+1))
fi

# The live copy runs from a DETACHED worktree, so 'git rev-parse --abbrev-ref'
# returns the literal "HEAD". Reporting that would make the provenance line
# useless exactly where it matters most.
D=$(mktemp -d); mkdir -p "$D/gitstub" "$D/w"
cat > "$D/gitstub/git" <<'GITEOF'
#!/bin/bash
for a in "$@"; do
  case "$a" in
    --abbrev-ref) echo "HEAD"; exit 0 ;;
    --points-at)  echo "main"; exit 0 ;;
    --short)      echo "deadbee"; exit 0 ;;
  esac
done
exit 0
GITEOF
chmod +x "$D/gitstub/git"
SUPERVISOR_PATH="$D/gitstub:$STUBS:/usr/bin:/bin" STUB_PANE_STATE=busy \
SUPERVISOR_STATE="$D/w" SLEEPCHECK_DIR="$D/none" \
  bash "$WATCHDOG" >/dev/null 2>&1
check "a detached worktree reports a real ref, not HEAD" "^code: *main" "$D/w/watchdog.status"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
