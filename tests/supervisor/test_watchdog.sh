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
  rm -rf "$2"; mkdir -p "$2"
  SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE="$1" STUB_SENT="$2/sent" \
  STUB_BUSY_AFTER="${STUB_BUSY_AFTER:-}" STUB_COUNTER="$2/counter" \
  SUPERVISOR_STATE="$2" SUPERVISOR_STATUS="$2/st" SUPERVISOR_LOG="$2/lg" \
  SUPERVISOR_STAMP="$2/stamp" SUPERVISOR_HISTORY="$2/hist" NOTIFY_ENV="$2/none.env" \
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

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
