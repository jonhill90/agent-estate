#!/bin/bash
# agent-supervisor#112. lanes.sh alone can only WITHHOLD a never-busy lane
# from --free; nothing reads its output on a schedule outside a human
# choosing to run `lanes.sh` by hand. The four-hour incident happened
# because nothing did that reading for four hours straight. This asserts
# the WIRING: watchdog.sh, which already runs unattended every
# ${TICK_INTERVAL}s outside the loop (see that file's own header), must
# read lanes.sh --json on every exit path and page a human -- via notify.sh,
# the path #118/#123 restored -- when it finds one.
#
# Same discipline test_watchdog_source_task_sweep.sh uses for #133: this
# only ever runs watchdog.sh itself, exactly as the LaunchAgent would, not
# the check function in isolation -- if the call from watchdog.sh into
# lanes.sh --json is ever removed while lanes.sh's own classification stays
# intact, this goes red.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCHDOG="$HERE/../../scripts/supervisor/watchdog.sh"
STUBS="$HERE/stubs"
pass=0; fail=0

D=$(mktemp -d); mkdir -p "$D/bin"
trap 'rm -rf "$D"' EXIT

# The combined fixture: window 1 is the SUPERVISOR's own pane -- given a
# recognised busy shape so watchdog.sh's own busy/idle probe takes the
# quickest healthy exit and this test stays about the never-busy wiring, not
# the loop-restart logic other tests already cover. Window 2 is a worker
# lane sitting on the #112 incident's actual capture, aged past
# NEVER_BUSY_AFTER's default (1200s) with no repaint since. Window 3 is a
# second, ordinary idle lane -- proves this alarm does not fire for every
# lane in the session, only the stuck one.
cat > "$D/fixture" <<'FIX'
1|supervisor-pane|claude.exe|esc to interrupt 3s|1|0
2|w-firstrun|claude.exe|Paste code here if prompted >|1300|0
3|w-ready|claude.exe|⏵⏵ bypass permissions on (shift+tab to cycle) · ← 1 agent|1|0
FIX

# Reuse lanes.sh's own tested stubs verbatim rather than re-deriving tmux's
# and ps's contract a second time -- the whole point of #112's design is
# that lanes.sh is the SOLE authority on the classification; this test must
# not duplicate that logic, only prove watchdog.sh reads its answer.
cp "$HERE/stubs/tmux-lanes" "$D/bin/tmux"
cp "$HERE/stubs/ps-lanes" "$D/bin/ps"
# A `notify.sh` stub that records every call it receives instead of paging
# anyone for real.
cat > "$D/bin/notify.sh" <<'NOTIFY'
#!/bin/bash
printf 'CALLER=%s SUBJECT=%s BODY=%s\n' "${AGENT_NOTIFY_CALLER:-}" "$1" "$2" >> "${NOTIFY_CALLS:?}"
exit 0
NOTIFY
chmod +x "$D/bin/notify.sh"

run() {
  LANES_FIXTURE="$D/fixture" NOTIFY_CALLS="$D/notify-calls" \
  SUPERVISOR_PATH="$D/bin:$STUBS:/usr/bin:/bin" NOTIFY_SCRIPT="$D/bin/notify.sh" \
  SUPERVISOR_PANE="lanetest:1.1" LANES_SESSION="lanetest" \
  SUPERVISOR_STATE="$D" SUPERVISOR_STATUS="$D/st" SUPERVISOR_LOG="$D/lg" \
  SUPERVISOR_STAMP="$D/stamp" SUPERVISOR_HISTORY="$D/hist" NOTIFY_ENV="$D/none.env" \
  SLEEPCHECK_DIR="$D/transcripts" \
  bash "$WATCHDOG" >/dev/null 2>"$D/err"
}
mkdir -p "$D/transcripts"

echo "watchdog.sh -- #112 never-busy lane wiring"

run
if grep -qE '^never-busy: 1 lane\(s\) stuck since launch — w-firstrun' "$D/st"; then
  echo "  ok   watchdog.status carries the never-busy line, naming the stuck lane"; pass=$((pass+1));
else
  echo "  FAIL no never-busy line in watchdog.status:"; sed 's/^/       /' "$D/st"; fail=$((fail+1));
fi

if [ -f "$D/notify-calls" ] && grep -qE 'CALLER=supervisor' "$D/notify-calls" \
  && grep -qE 'w-firstrun' "$D/notify-calls"; then
  echo "  ok   notify.sh was called, naming the stuck lane, with the supervisor caller gate"; pass=$((pass+1));
else
  echo "  FAIL notify.sh was not called correctly:"; sed 's/^/       /' "$D/notify-calls" 2>/dev/null; fail=$((fail+1));
fi

if grep -qE 'w-ready' "$D/notify-calls" 2>/dev/null; then
  echo "  FAIL the ordinary idle lane (w-ready) was named in the alert"; fail=$((fail+1));
else
  echo "  ok   the ordinary idle lane was not swept into the alert"; pass=$((pass+1));
fi

calls_after_first=$([ -f "$D/notify-calls" ] && wc -l < "$D/notify-calls" | tr -d ' ' || echo 0)

# Run again with the SAME stuck lane. Must NOT page a second time -- the
# episode is still the same set of stuck names, and re-paging every 180s for
# a condition a human has already been told about is the noisy-alarm failure
# mode, not the silent one #112 measured.
run
calls_after_second=$([ -f "$D/notify-calls" ] && wc -l < "$D/notify-calls" | tr -d ' ' || echo 0)
if [ "$calls_after_second" = "$calls_after_first" ]; then
  echo "  ok   a second tick with the same stuck lane does not page again (dedup)"; pass=$((pass+1));
else
  echo "  FAIL the same stuck lane paged a second time:"; sed 's/^/       /' "$D/notify-calls"; fail=$((fail+1));
fi

# Clear the stuck lane (respawn it as ready) and confirm the episode file is
# dropped -- proven indirectly: a THIRD tick, with the lane stuck again
# (simulating a second, later incident), must page again rather than reading
# as the same still-open episode.
cat > "$D/fixture" <<'FIX'
1|supervisor-pane|claude.exe|esc to interrupt 3s|1|0
2|w-firstrun|claude.exe|⏵⏵ bypass permissions on (shift+tab to cycle) · ← 1 agent|1|0
3|w-ready|claude.exe|⏵⏵ bypass permissions on (shift+tab to cycle) · ← 1 agent|1|0
FIX
run
cat > "$D/fixture" <<'FIX'
1|supervisor-pane|claude.exe|esc to interrupt 3s|1|0
2|w-firstrun|claude.exe|Paste code here if prompted >|1300|0
3|w-ready|claude.exe|⏵⏵ bypass permissions on (shift+tab to cycle) · ← 1 agent|1|0
FIX
run
calls_after_recurrence=$([ -f "$D/notify-calls" ] && wc -l < "$D/notify-calls" | tr -d ' ' || echo 0)
if [ "$calls_after_recurrence" -gt "$calls_after_second" ]; then
  echo "  ok   the SAME lane going stuck again, after recovering, pages again"; pass=$((pass+1));
else
  echo "  FAIL a second, later incident for the same lane did not page:"; sed 's/^/       /' "$D/notify-calls"; fail=$((fail+1));
fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
