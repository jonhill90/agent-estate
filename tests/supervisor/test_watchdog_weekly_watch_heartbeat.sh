#!/bin/bash
# watchdog.sh -- weekly-watch heartbeat staleness (agent-supervisor#341).
#
# WHY THIS EXISTS. weekly-watch.sh (from #328/#327) is the ONE alarm that
# tells Jon the weekly quota is nearly gone, and it shipped with no
# liveness instrumentation at all -- if it dies, hangs, or the launchd job
# that fires it silently stops firing (launchd reports a job "loaded" even
# if it has never run since it was loaded), nothing distinguishes that from
# a correctly quiet week below threshold. This drives the real watchdog.sh
# end to end (stub tmux/gh, real watchdog_notify.py) against a hand-written
# weekly-watch heartbeat stamp and proves the same three cases
# test_watchdog_quota_watch_heartbeat.sh proved for #276: a fresh heartbeat
# is silent, a stale one pages once per episode, and a REAL LIVE PROCESS
# with a stale heartbeat is still reported unhealthy -- the check never
# asks the process table (or launchd) anything.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCHDOG="$HERE/../../scripts/supervisor/watchdog.sh"
STUBS="$HERE/stubs"
pass=0; fail=0
say_ok()  { echo "  ok   $1"; pass=$((pass+1)); }
say_bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }
check() { # check <name> <expected-substring> <file>
  if grep -q "$2" "$3" 2>/dev/null; then say_ok "$1";
  else say_bad "$1" "expected '$2' in $(cat "$3" 2>/dev/null | tr '\n' ' ')"; fi
}

echo "watchdog.sh -- weekly-watch heartbeat staleness (#341)"

stamp_ago() { # stamp_ago <seconds-ago> <state>
  local ts
  ts=$(date -u -v-"$1"S '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d "$1 seconds ago" '+%Y-%m-%dT%H:%M:%SZ')
  printf 'checked: %s\nstate: %s\n' "$ts" "${2:-ok}"
}

NOTIFY_DIR=$(mktemp -d)
cat >"$NOTIFY_DIR/up.sh" <<'EOF'
#!/bin/bash
echo "$1|$2" >> "$0.calls"
EOF
chmod +x "$NOTIFY_DIR/up.sh"
UP="$NOTIFY_DIR/up.sh"
up_calls() { cat "$UP.calls" 2>/dev/null; }

ww_run() { # ww_run <workdir> <stamp-seconds-ago-or-absent> <state>
  rm -rf "$1"; mkdir -p "$1" "$1/transcripts"
  if [ "$2" != "absent" ]; then stamp_ago "$2" "$3" > "$1/.weekly-watch.state"; fi
  SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE=busy STUB_SENT="$1/sent" \
  SUPERVISOR_STATE="$1" SUPERVISOR_STATUS="$1/st" SUPERVISOR_LOG="$1/lg" \
  SUPERVISOR_STAMP="$1/stamp" SUPERVISOR_HISTORY="$1/hist" NOTIFY_ENV="$1/none.env" \
  SLEEPCHECK_DIR="$1/transcripts" NOTIFY_SCRIPT="$UP" \
  SUPERVISOR_WEEKLY_WATCH_STATUS="$1/.weekly-watch.state" \
  SUPERVISOR_WEEKLY_WATCH_HEARTBEAT_EPISODE="$1/.ww-episode.json" \
  WEEKLY_WATCH_HEARTBEAT_STALE_AFTER=100 \
    bash "$WATCHDOG" >/dev/null 2>"$1/err"
}

# 1. Missing stamp: never paged, reported distinctly from stale. This is the
# state a scheduler that has NEVER FIRED leaves behind -- exactly the
# "loaded but never firing" trap the issue names, if nothing else here
# distinguished it from a genuinely quiet week.
D=$(mktemp -d)
ww_run "$D/w" absent ok
check "a missing weekly-watch stamp is named in watchdog.status" "^weekly-watch: .*missing" "$D/w/st"
if [ -z "$(up_calls)" ]; then say_ok "a missing stamp never pages"
else say_bad "a missing stamp never pages" "paged: $(up_calls)"; fi

# 2. Fresh heartbeat: no page.
D=$(mktemp -d)
ww_run "$D/w" 5 ok
check "a fresh heartbeat is reported alive" "^weekly-watch: .*alive" "$D/w/st"
if [ -z "$(up_calls)" ]; then say_ok "a fresh heartbeat never pages"
else say_bad "a fresh heartbeat never pages" "paged: $(up_calls)"; fi

# 3. Stale heartbeat, no process involved at all: pages. This is the
# scheduler-never-fires case in its plainest form -- checked: stopped
# advancing and nothing else in the estate would have noticed.
: >"$UP.calls"
D=$(mktemp -d)
ww_run "$D/w" 500 ok
check "a stale heartbeat is reported in watchdog.status" "^weekly-watch: .*new stale-heartbeat episode" "$D/w/st"
if grep -q "heartbeat stale" "$UP.calls" 2>/dev/null; then
  say_ok "a stale heartbeat pages through the notify path"
else
  say_bad "a stale heartbeat pages through the notify path" "got: $(up_calls)"
fi

# 4. THE CASE THAT MATTERS: a REAL, LIVE background process (standing in for
# a hung weekly-watch.sh) is running the whole time, and the stamp is
# stale. `pgrep` would call this healthy; this check must not, because it
# never asks pgrep anything.
: >"$UP.calls"
D=$(mktemp -d); mkdir -p "$D/w"
bash -c 'trap "exit 0" TERM; while :; do sleep 1; done' &
live_pid=$!
sleep 1
if ! kill -0 "$live_pid" 2>/dev/null; then
  say_bad "setup: a live process exists for the duration of this case" "pid $live_pid died immediately"
else
  say_ok "setup: a live process exists for the duration of this case"
fi
ww_run "$D/w" 9999 ok
check "a live process with a stale stamp is reported unhealthy" "^weekly-watch: .*new stale-heartbeat episode" "$D/w/st"
if grep -q "heartbeat stale" "$UP.calls" 2>/dev/null; then
  say_ok "...and pages, the exact case a bare pgrep-based check would miss"
else
  say_bad "...and pages, the exact case a bare pgrep-based check would miss" "got: $(up_calls)"
fi
if kill -0 "$live_pid" 2>/dev/null; then
  say_ok "the live process was left alone by the heartbeat check itself (recovery is a separate concern)"
else
  say_bad "the live process was left alone by the heartbeat check itself" "pid $live_pid is gone"
fi
kill -TERM "$live_pid" 2>/dev/null; wait "$live_pid" 2>/dev/null || true

# 5. Same stale episode, second tick: deduped, not re-paged.
SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE=busy STUB_SENT="$D/w/sent" \
SUPERVISOR_STATE="$D/w" SUPERVISOR_STATUS="$D/w/st" SUPERVISOR_LOG="$D/w/lg" \
SUPERVISOR_STAMP="$D/w/stamp" SUPERVISOR_HISTORY="$D/w/hist" NOTIFY_ENV="$D/w/none.env" \
SLEEPCHECK_DIR="$D/w/transcripts" NOTIFY_SCRIPT="$UP" \
SUPERVISOR_WEEKLY_WATCH_STATUS="$D/w/.weekly-watch.state" \
SUPERVISOR_WEEKLY_WATCH_HEARTBEAT_EPISODE="$D/w/.ww-episode.json" \
WEEKLY_WATCH_HEARTBEAT_STALE_AFTER=100 \
  bash "$WATCHDOG" >/dev/null 2>"$D/w/err2"
call_count=$(wc -l <"$UP.calls" 2>/dev/null | tr -d ' ')
if [ "$call_count" = 1 ]; then
  say_ok "a stale weekly-watch episode is not re-paged every tick"
else
  say_bad "a stale weekly-watch episode is not re-paged every tick" "paged $call_count times"
fi

# 6. This alarm's dedup state is independent of the quota-watch heartbeat's --
# both stale at once must each page once, not zero and not a burst.
: >"$UP.calls"
D=$(mktemp -d); mkdir -p "$D/w/transcripts"
stamp_ago 500 ok > "$D/w/.weekly-watch.state"
stamp_ago 500 SAFE > "$D/w/.quota-watch.state"
SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE=busy STUB_SENT="$D/w/sent" \
SUPERVISOR_STATE="$D/w" SUPERVISOR_STATUS="$D/w/st" SUPERVISOR_LOG="$D/w/lg" \
SUPERVISOR_STAMP="$D/w/stamp" SUPERVISOR_HISTORY="$D/w/hist" NOTIFY_ENV="$D/w/none.env" \
SLEEPCHECK_DIR="$D/w/transcripts" NOTIFY_SCRIPT="$UP" \
SUPERVISOR_QUOTA_WATCH_STATUS="$D/w/.quota-watch.state" \
SUPERVISOR_QUOTA_WATCH_HEARTBEAT_EPISODE="$D/w/.qw-episode.json" \
QUOTA_WATCH_HEARTBEAT_STALE_AFTER=100 \
SUPERVISOR_WEEKLY_WATCH_STATUS="$D/w/.weekly-watch.state" \
SUPERVISOR_WEEKLY_WATCH_HEARTBEAT_EPISODE="$D/w/.ww-episode.json" \
WEEKLY_WATCH_HEARTBEAT_STALE_AFTER=100 \
  bash "$WATCHDOG" >/dev/null 2>"$D/w/err"
check "the quota-watch heartbeat alarm still fires independently" "^quota-watch: .*new stale-heartbeat episode" "$D/w/st"
check "and the weekly-watch heartbeat alarm fires separately" "^weekly-watch: .*new stale-heartbeat episode" "$D/w/st"
call_count=$(wc -l <"$UP.calls" 2>/dev/null | tr -d ' ')
if [ "$call_count" = 2 ]; then
  say_ok "two independent stale heartbeats each page once, not zero and not a burst"
else
  say_bad "two independent stale heartbeats each page once, not zero and not a burst" "paged $call_count time(s): $(up_calls)"
fi

echo
echo "watchdog.sh weekly-watch heartbeat: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
