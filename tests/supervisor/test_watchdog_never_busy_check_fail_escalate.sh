#!/bin/bash
# agent-supervisor#163: the never-busy check FAILING TO RUN is a different
# fact from it running and finding a stuck lane -- #163 measured nine
# straight ticks of "NEVER-BUSY-CHECK FAILED: could not parse lanes.sh
# --json output" in watchdog.log with nobody paged, because a failed check
# only logged and wrote never_busy_note; nothing counted the streak or
# escalated it. This asserts the wiring: a broken check must page a human
# after NEVER_BUSY_CHECK_FAIL_ESCALATE_AFTER consecutive failures, must keep
# paging on later multiples rather than falling silent after the first page,
# and must reset the streak the moment the check runs successfully again.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCHDOG="$HERE/../../scripts/supervisor/watchdog.sh"
# agent-supervisor#199/#205: this file hands watchdog.sh a fresh
# SUPERVISOR_STATE per case, so check_worktree_guard_audit's own throttle
# (a stamp file under that state dir) never has a prior run to find and
# would run the real worktree-guard-audit.sh -- against whatever repo this
# worktree happens to be checked out in -- on every tick. That check has
# its own dedicated test (test_watchdog_worktree_guard_audit.sh); this file
# is about something else, so disable it here the same way that test
# disables the checks it isn't about.
export SUPERVISOR_GUARD_AUDIT_INTERVAL=99999999999
STUBS="$HERE/stubs"
pass=0; fail=0

D=$(mktemp -d); mkdir -p "$D/bin" "$D/supervisor"
trap 'rm -rf "$D"' EXIT

ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 - $2"; fail=$((fail+1)); }

# A private copy of scripts/supervisor/ with lanes.sh replaced by a stub that
# always fails -- watchdog.sh calls "$HERE/lanes.sh" by a fixed path beside
# itself, not through PATH, so this is the only way to make the CHECK fail on
# demand without also breaking every other check this file runs on every
# exit path (busy/idle, poller, source-task sweep, ...).
REAL_SUPERVISOR="$HERE/../../scripts/supervisor"
cp -R "$REAL_SUPERVISOR"/. "$D/supervisor/"
cat > "$D/supervisor/lanes.sh" <<'STUB'
#!/bin/bash
echo "stub: refusing to answer, on purpose" >&2
exit 1
STUB
chmod +x "$D/supervisor/lanes.sh"

cat > "$D/bin/notify.sh" <<'NOTIFY'
#!/bin/bash
printf 'SUBJECT=%s BODY=%s\n' "$1" "$2" >> "${NOTIFY_CALLS:?}"
exit 0
NOTIFY
chmod +x "$D/bin/notify.sh"

# The supervisor's own pane, given a recognised busy shape so the loop-restart
# logic this file also runs takes its quickest healthy exit and this test
# stays about the never-busy-check wiring, not the restart logic.
cat > "$D/fixture" <<'FIX'
1|supervisor-pane|claude.exe|esc to interrupt 3s|1|0
FIX
cp "$HERE/stubs/tmux-lanes" "$D/bin/tmux"
cp "$HERE/stubs/ps-lanes" "$D/bin/ps"

run() {
  LANES_FIXTURE="$D/fixture" NOTIFY_CALLS="$D/notify-calls" \
  SUPERVISOR_PATH="$D/bin:$STUBS:/usr/bin:/bin" NOTIFY_SCRIPT="$D/bin/notify.sh" \
  SUPERVISOR_PANE="lanetest:1.1" LANES_SESSION="lanetest" \
  SUPERVISOR_STATE="$D" SUPERVISOR_STATUS="$D/st" SUPERVISOR_LOG="$D/lg" \
  SUPERVISOR_STAMP="$D/stamp" SUPERVISOR_HISTORY="$D/hist" NOTIFY_ENV="$D/none.env" \
  SLEEPCHECK_DIR="$D/transcripts" SUPERVISOR_NEVER_BUSY_CHECK_FAIL_ESCALATE_AFTER=3 \
  bash "$D/supervisor/watchdog.sh" >/dev/null 2>"$D/err"
}
mkdir -p "$D/transcripts"

echo "watchdog.sh -- #163 never-busy CHECK fail-streak escalation"

calls() { [ -f "$D/notify-calls" ] && wc -l < "$D/notify-calls" | tr -d ' ' || echo 0; }

run
if grep -qE '^never-busy: unknown .* \(failed 1 check' "$D/st"; then
  ok "watchdog.status names the failure and the streak count (1)"
else
  bad "watchdog.status names the failure and the streak count (1)" "$(cat "$D/st" 2>/dev/null)"
fi
[ "$(calls)" -eq 0 ] && ok "no page on the 1st consecutive failure" \
  || bad "no page on the 1st consecutive failure" "$(cat "$D/notify-calls" 2>/dev/null)"

run
[ "$(calls)" -eq 0 ] && ok "no page on the 2nd consecutive failure" \
  || bad "no page on the 2nd consecutive failure" "$(cat "$D/notify-calls" 2>/dev/null)"

run
if grep -qE '^never-busy: unknown .* \(failed 3 check' "$D/st"; then
  ok "watchdog.status streak reaches 3"
else
  bad "watchdog.status streak reaches 3" "$(cat "$D/st" 2>/dev/null)"
fi
if [ "$(calls)" -eq 1 ] && grep -q '163' "$D/notify-calls"; then
  ok "a human is paged on the 3rd consecutive failure, naming #163"
else
  bad "a human is paged on the 3rd consecutive failure, naming #163" "$(cat "$D/notify-calls" 2>/dev/null)"
fi

run
run
calls_at_5=$(calls)
[ "$calls_at_5" -eq 1 ] && ok "no repeat page on the 4th/5th consecutive failures" \
  || bad "no repeat page on the 4th/5th consecutive failures" "$(cat "$D/notify-calls" 2>/dev/null)"

run
if [ "$(calls)" -eq 2 ]; then
  ok "the 6th consecutive failure (streak 3's next multiple) pages again"
else
  bad "the 6th consecutive failure (streak 3's next multiple) pages again" "$(cat "$D/notify-calls" 2>/dev/null)"
fi

# Restore a real, working lanes.sh: the check must succeed and clear the
# streak, so a LATER unrelated outage starts counting at 1, not 7.
cp "$REAL_SUPERVISOR/lanes.sh" "$D/supervisor/lanes.sh"
chmod +x "$D/supervisor/lanes.sh"
run
if ! grep -q '^never-busy:' "$D/st"; then
  ok "a successful check clears the never-busy line entirely"
else
  bad "a successful check clears the never-busy line entirely" "$(cat "$D/st" 2>/dev/null)"
fi

cat > "$D/supervisor/lanes.sh" <<'STUB'
#!/bin/bash
echo "stub: refusing to answer, on purpose" >&2
exit 1
STUB
chmod +x "$D/supervisor/lanes.sh"
run
if grep -qE '^never-busy: unknown .* \(failed 1 check' "$D/st"; then
  ok "the streak restarts at 1 after an intervening successful check"
else
  bad "the streak restarts at 1 after an intervening successful check" "$(cat "$D/st" 2>/dev/null)"
fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
