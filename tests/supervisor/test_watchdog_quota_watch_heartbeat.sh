#!/bin/bash
# watchdog.sh -- quota-watch heartbeat staleness (agent-supervisor#276).
#
# WHY THIS EXISTS. quota-watch.sh hung for three hours and was reported
# healthy on every tick because the only thing anything checked was `pgrep`
# finding its pid -- "a process exists" is not "a process is working". This
# drives the real watchdog.sh end to end (stub tmux/gh, real
# watchdog_notify.py) against a hand-written quota-watch heartbeat stamp and
# proves the THREE cases the issue names explicitly: a fresh heartbeat is
# silent, a stale one pages once per episode, and -- the case that matters,
# the one that went undetected for three hours -- a REAL LIVE PROCESS with a
# stale heartbeat is still reported unhealthy. The check never asks the
# process table anything.
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

echo "watchdog.sh -- quota-watch heartbeat staleness (#276)"

stamp_ago() { # stamp_ago <seconds-ago> <state>
  local ts
  ts=$(date -u -v-"$1"S '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d "$1 seconds ago" '+%Y-%m-%dT%H:%M:%SZ')
  printf 'checked: %s\nstate: %s\n' "$ts" "${2:-SAFE}"
}

NOTIFY_DIR=$(mktemp -d)
cat >"$NOTIFY_DIR/up.sh" <<'EOF'
#!/bin/bash
echo "$1|$2" >> "$0.calls"
EOF
chmod +x "$NOTIFY_DIR/up.sh"
UP="$NOTIFY_DIR/up.sh"
up_calls() { cat "$UP.calls" 2>/dev/null; }

qw_run() { # qw_run <workdir> <stamp-seconds-ago-or-absent> <state>
  rm -rf "$1"; mkdir -p "$1" "$1/transcripts"
  if [ "$2" != "absent" ]; then stamp_ago "$2" "$3" > "$1/.quota-watch.state"; fi
  SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE=busy STUB_SENT="$1/sent" \
  SUPERVISOR_STATE="$1" SUPERVISOR_STATUS="$1/st" SUPERVISOR_LOG="$1/lg" \
  SUPERVISOR_STAMP="$1/stamp" SUPERVISOR_HISTORY="$1/hist" NOTIFY_ENV="$1/none.env" \
  SLEEPCHECK_DIR="$1/transcripts" NOTIFY_SCRIPT="$UP" \
  SUPERVISOR_QUOTA_WATCH_STATUS="$1/.quota-watch.state" \
  SUPERVISOR_QUOTA_WATCH_HEARTBEAT_EPISODE="$1/.qw-episode.json" \
  QUOTA_WATCH_HEARTBEAT_STALE_AFTER=100 \
    bash "$WATCHDOG" >/dev/null 2>"$1/err"
}

# 1. Missing stamp: never paged, reported distinctly from stale.
D=$(mktemp -d)
qw_run "$D/w" absent SAFE
check "a missing quota-watch stamp is named in watchdog.status" "^quota-watch: .*missing" "$D/w/st"
if [ -z "$(up_calls)" ]; then say_ok "a missing stamp never pages"
else say_bad "a missing stamp never pages" "paged: $(up_calls)"; fi

# 2. Fresh heartbeat: no page.
D=$(mktemp -d)
qw_run "$D/w" 5 SAFE
check "a fresh heartbeat is reported alive" "^quota-watch: .*alive" "$D/w/st"
if [ -z "$(up_calls)" ]; then say_ok "a fresh heartbeat never pages"
else say_bad "a fresh heartbeat never pages" "paged: $(up_calls)"; fi

# 3. Stale heartbeat, no process involved at all: pages.
: >"$UP.calls"
D=$(mktemp -d)
qw_run "$D/w" 500 SAFE
check "a stale heartbeat is reported in watchdog.status" "^quota-watch: .*new stale-heartbeat episode" "$D/w/st"
if grep -q "watchdog: inbox-poll heartbeat stale\|heartbeat stale" "$UP.calls" 2>/dev/null; then
  say_ok "a stale heartbeat pages through the notify path"
else
  say_bad "a stale heartbeat pages through the notify path" "got: $(up_calls)"
fi

# 4. THE CASE THAT MATTERS: a REAL, LIVE background process (standing in for
# a hung quota-watch.sh) is running the whole time, and the stamp is stale.
# `pgrep` would call this healthy; this check must not, because it never
# asks pgrep anything.
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
qw_run "$D/w" 9999 SAFE
check "a live process with a stale stamp is reported unhealthy" "^quota-watch: .*new stale-heartbeat episode" "$D/w/st"
if grep -q "heartbeat stale" "$UP.calls" 2>/dev/null; then
  say_ok "...and pages, exactly the case that went undetected for three hours"
else
  say_bad "...and pages, exactly the case that went undetected for three hours" "got: $(up_calls)"
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
SUPERVISOR_QUOTA_WATCH_STATUS="$D/w/.quota-watch.state" \
SUPERVISOR_QUOTA_WATCH_HEARTBEAT_EPISODE="$D/w/.qw-episode.json" \
QUOTA_WATCH_HEARTBEAT_STALE_AFTER=100 \
  bash "$WATCHDOG" >/dev/null 2>"$D/w/err2"
call_count=$(wc -l <"$UP.calls" 2>/dev/null | tr -d ' ')
if [ "$call_count" = 1 ]; then
  say_ok "a stale quota-watch episode is not re-paged every tick"
else
  say_bad "a stale quota-watch episode is not re-paged every tick" "paged $call_count times"
fi

# 6. This alarm's dedup state is independent of the inbox-poll heartbeat's --
# both stale at once must each page once, not zero and not a burst.
: >"$UP.calls"
D=$(mktemp -d); mkdir -p "$D/w/transcripts"
stamp_ago 500 SAFE > "$D/w/.quota-watch.state"
printf 'checked: %s\nstate:   ok\ndetail:  listening\npid:     999\n' "$(stamp_ago 500 SAFE | head -1 | sed 's/^checked: //')" >"$D/w/inbox-poll.status"
SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE=busy STUB_SENT="$D/w/sent" \
SUPERVISOR_STATE="$D/w" SUPERVISOR_STATUS="$D/w/st" SUPERVISOR_LOG="$D/w/lg" \
SUPERVISOR_STAMP="$D/w/stamp" SUPERVISOR_HISTORY="$D/w/hist" NOTIFY_ENV="$D/w/none.env" \
SLEEPCHECK_DIR="$D/w/transcripts" NOTIFY_SCRIPT="$UP" \
SUPERVISOR_INBOX_POLL_STATUS="$D/w/inbox-poll.status" \
SUPERVISOR_HEARTBEAT_EPISODE="$D/w/.hb-episode.json" \
INBOX_HEARTBEAT_STALE_AFTER=100 \
SUPERVISOR_QUOTA_WATCH_STATUS="$D/w/.quota-watch.state" \
SUPERVISOR_QUOTA_WATCH_HEARTBEAT_EPISODE="$D/w/.qw-episode.json" \
QUOTA_WATCH_HEARTBEAT_STALE_AFTER=100 \
  bash "$WATCHDOG" >/dev/null 2>"$D/w/err"
check "the inbox-poll heartbeat alarm still fires independently" "^heartbeat: .*new stale-heartbeat episode" "$D/w/st"
check "and the quota-watch heartbeat alarm fires separately" "^quota-watch: .*new stale-heartbeat episode" "$D/w/st"
call_count=$(wc -l <"$UP.calls" 2>/dev/null | tr -d ' ')
if [ "$call_count" = 2 ]; then
  say_ok "two independent stale heartbeats each page once, not zero and not a burst"
else
  say_bad "two independent stale heartbeats each page once, not zero and not a burst" "paged $call_count time(s): $(up_calls)"
fi

echo
echo "watchdog.sh quota-watch heartbeat: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
