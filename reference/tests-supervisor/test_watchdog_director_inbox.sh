#!/bin/bash
# as#151: the director inbox can stall for hours while `director-route.sh`'s
# own escalate (as#34/#42) reports nothing wrong, because that check only
# ever runs inside `inbox-poll.sh`'s own flush loop -- once per Telegram
# long-poll iteration, while that loop is alive and actually reaching it.
# Measured on the live estate: `notify.log`'s entire history (787 lines back
# to 2026-08-11) has no record of "Director inbox has undelivered
# message(s)" ever firing, despite at least one earlier incident (as#34's own
# filing) that described a queue stale well past the 1800s threshold. And if
# `inbox-poll.sh` itself is down, nothing calls `--flush` at all, so the
# in-loop escalate never runs regardless of how stale the queue gets.
#
# These tests drive the real watchdog.sh end to end with a stub notifier and
# the real director-inbox.sh against a scratch DIRECTOR_INBOX -- the same
# shape as test_watchdog.sh's #163 heartbeat section -- and prove the
# staleness alarm as#151 adds is truly OUTSIDE the loop: it fires from
# watchdog.sh's own tick regardless of the pane's busy/idle state, and
# regardless of whether inbox-poll.sh or director-route.sh ever run at all.
#
# The DIRECTOR_INBOX fixture always lives OUTSIDE the per-case workdir
# ($D/fx, never $D/w): di_run's `rm -rf "$dir"` would otherwise delete a
# fixture written into the same tree it wipes.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCHDOG="$HERE/../../scripts/supervisor/watchdog.sh"
DIRECTOR_INBOX_BIN="$HERE/../../scripts/supervisor/director-inbox.sh"
STUBS="$HERE/stubs"
# agent-supervisor#199/#205: each case below hands watchdog.sh a fresh
# SUPERVISOR_STATE, so check_worktree_guard_audit's own throttle (a stamp
# file under that state dir) never has a prior run to find and would run the
# real worktree-guard-audit.sh -- against whatever repo this worktree
# happens to be checked out in -- on every single tick. That check has its
# own dedicated test (test_watchdog_worktree_guard_audit.sh); this file is
# about director-inbox staleness, so disable it here the same way that test
# disables the checks it isn't about.
export SUPERVISOR_GUARD_AUDIT_INTERVAL=99999999999
pass=0; fail=0
say_ok()  { echo "  ok   $1"; pass=$((pass+1)); }
say_bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }
check() { # check <name> <expected-substring> <file>
  if grep -q "$2" "$3" 2>/dev/null; then say_ok "$1";
  else say_bad "$1" "expected '$2' in $(cat "$3" 2>/dev/null | tr '\n' ' ')"; fi
}

echo "watchdog.sh -- director-inbox staleness (as#151)"

stamp_ago() { # stamp_ago <seconds-ago> -- a director-inbox.jsonl "at" timestamp
  python3 -c 'import datetime,sys
print((datetime.datetime.now(datetime.timezone.utc)
       - datetime.timedelta(seconds=int(sys.argv[1]))).strftime("%Y-%m-%dT%H:%M:%SZ"))' "$1"
}
write_inbox() { # write_inbox <path> <seconds-ago> <text>
  python3 -c 'import json,sys
print(json.dumps({"at": sys.argv[1], "read": False, "text": sys.argv[2]}))' \
    "$(stamp_ago "$2")" "$3" >"$1"
}

# Stub notifier -- records every call instead of touching a real channel.
# Shared across cases below; each case truncates $UP.calls first.
NOTIFY_DIR=$(mktemp -d)
cat >"$NOTIFY_DIR/up.sh" <<'EOF'
#!/bin/bash
echo "$1|$2" >> "$0.calls"
EOF
chmod +x "$NOTIFY_DIR/up.sh"
UP="$NOTIFY_DIR/up.sh"
up_calls() { cat "$UP.calls" 2>/dev/null; }
up_call_count() { wc -l <"$UP.calls" 2>/dev/null | tr -d ' '; }

di_run() { # di_run <workdir> <inbox-jsonl-path> [notify-script] [pane-state]
  local dir="$1" inbox="$2" notify="${3:-}" pane="${4:-busy}"
  rm -rf "$dir"; mkdir -p "$dir" "$dir/transcripts"
  SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE="$pane" STUB_SENT="$dir/sent" \
  SUPERVISOR_STATE="$dir" SUPERVISOR_STATUS="$dir/st" SUPERVISOR_LOG="$dir/lg" \
  SUPERVISOR_STAMP="$dir/stamp" SUPERVISOR_HISTORY="$dir/hist" NOTIFY_ENV="$dir/none.env" \
  SLEEPCHECK_DIR="$dir/transcripts" NOTIFY_SCRIPT="$notify" \
  SUPERVISOR_DIRECTOR_INBOX_BIN="$DIRECTOR_INBOX_BIN" DIRECTOR_INBOX="$inbox" \
  SUPERVISOR_DIRECTOR_INBOX_EPISODE="$dir/.di-episode.json" \
  DIRECTOR_INBOX_STALE_SECONDS=100 \
    bash "$WATCHDOG" >/dev/null 2>"$dir/err"
}

# 1. No director-inbox.jsonl at all (fresh install, nothing ever posted):
#    reported empty, never paged.
D=$(mktemp -d)
di_run "$D/w" "$D/fx-absent/director-inbox.jsonl" "$UP"
check "an absent director inbox is reported empty" "^inbox:.*empty" "$D/w/st"
if [ -z "$(up_calls)" ]; then say_ok "an absent director inbox never pages"
else say_bad "an absent director inbox never pages" "paged: $(up_calls)"; fi

# 2. A fresh pending message, well under the 100s threshold: no page, but the
#    pending count and age are still visible (requirement #3 -- surfaced even
#    when nothing is wrong yet).
D=$(mktemp -d); mkdir -p "$D/fx"
write_inbox "$D/fx/inbox.jsonl" 5 "hello"
di_run "$D/w" "$D/fx/inbox.jsonl" "$UP"
check "a fresh pending message is reported, not stale" "^inbox:.*1 pending" "$D/w/st"
if [ -z "$(up_calls)" ]; then say_ok "a fresh pending message never pages"
else say_bad "a fresh pending message never pages" "paged: $(up_calls)"; fi

# 3. THE CENTRAL CASE. A message stale past the 100s threshold, the pane
#    BUSY the entire time (director-route.sh's own escalate never gets a
#    chance to run inside a flush loop that never nudges a busy pane) --
#    watchdog.sh must still page, because this check does not go through the
#    pane, the poller, or director-route.sh at all.
: >"$UP.calls"
D=$(mktemp -d); mkdir -p "$D/fx"
write_inbox "$D/fx/inbox.jsonl" 500 "Telegram from Jon: are you there?"
di_run "$D/w" "$D/fx/inbox.jsonl" "$UP" busy
check "a stale director inbox is reported in watchdog.status" \
      "^inbox:.*new stale director-inbox episode" "$D/w/st"
if grep -q "director inbox has 1 undelivered message" "$UP.calls" 2>/dev/null; then
  say_ok "a stale director inbox pages through the notify path, independent of the pane"
else
  say_bad "a stale director inbox pages through the notify path" "got: $(up_calls)"
fi

# 4. Same stale episode, second tick: deduped, not paged again -- reuses
#    $D/fx/inbox.jsonl and $D/w's episode file from case 3.
SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE=busy STUB_SENT="$D/w/sent" \
SUPERVISOR_STATE="$D/w" SUPERVISOR_STATUS="$D/w/st" SUPERVISOR_LOG="$D/w/lg" \
SUPERVISOR_STAMP="$D/w/stamp" SUPERVISOR_HISTORY="$D/w/hist" NOTIFY_ENV="$D/w/none.env" \
SLEEPCHECK_DIR="$D/w/transcripts" NOTIFY_SCRIPT="$UP" \
SUPERVISOR_DIRECTOR_INBOX_BIN="$DIRECTOR_INBOX_BIN" DIRECTOR_INBOX="$D/fx/inbox.jsonl" \
SUPERVISOR_DIRECTOR_INBOX_EPISODE="$D/w/.di-episode.json" \
DIRECTOR_INBOX_STALE_SECONDS=100 \
  bash "$WATCHDOG" >/dev/null 2>"$D/w/err2"
if [ "$(up_call_count)" = 1 ]; then
  say_ok "a stale director-inbox episode is not re-paged every tick"
else
  say_bad "a stale director-inbox episode is not re-paged every tick" "paged $(up_call_count) times"
fi

# 5. The message is drained (delivered_at set) between ticks -- the queue is
#    genuinely empty now, and a later, unrelated stale message must page
#    again, not stay silenced by the earlier episode forever.
: >"$UP.calls"
DIRECTOR_INBOX="$D/fx/inbox.jsonl" "$DIRECTOR_INBOX_BIN" drain >/dev/null
di_run "$D/w2" "$D/fx/inbox.jsonl" "$UP" busy
check "a drained inbox is reported empty" "^inbox:.*empty" "$D/w2/st"
write_inbox "$D/fx/inbox2.jsonl" 500 "Telegram from Jon: second stall"
di_run "$D/w2" "$D/fx/inbox2.jsonl" "$UP" busy
if [ "$(up_call_count)" = 1 ]; then
  say_ok "a fresh stall after recovery pages again, not deduped against the old episode"
else
  say_bad "a fresh stall after recovery pages again" "paged $(up_call_count) time(s): $(up_calls)"
fi

# 6. Independent from the loop-restart escalation: both alarms can fire in
#    the same tick without one suppressing the other or the pages colliding
#    into one.
: >"$UP.calls"
D=$(mktemp -d); mkdir -p "$D/w/transcripts" "$D/fx"
write_inbox "$D/fx/inbox.jsonl" 500 "Telegram from Jon: stuck"
now=$(date +%s); for i in 1 2 3; do echo $((now - 60)); done >"$D/w/hist"
SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE=idle STUB_SENT="$D/w/sent" \
SUPERVISOR_STATE="$D/w" SUPERVISOR_STATUS="$D/w/st" SUPERVISOR_LOG="$D/w/lg" \
SUPERVISOR_STAMP="$D/w/stamp" SUPERVISOR_HISTORY="$D/w/hist" NOTIFY_ENV="$D/w/none.env" \
SLEEPCHECK_DIR="$D/w/transcripts" NOTIFY_SCRIPT="$UP" \
SUPERVISOR_DIRECTOR_INBOX_BIN="$DIRECTOR_INBOX_BIN" DIRECTOR_INBOX="$D/fx/inbox.jsonl" \
SUPERVISOR_DIRECTOR_INBOX_EPISODE="$D/w/.di-episode.json" \
DIRECTOR_INBOX_STALE_SECONDS=100 \
  bash "$WATCHDOG" >/dev/null 2>"$D/w/err"
check "the watchdog still escalates the loop-restart failure" "state:    escalate" "$D/w/st"
check "and separately reports the stale director inbox" "^inbox:.*new stale director-inbox episode" "$D/w/st"
if [ "$(up_call_count)" = 2 ]; then
  say_ok "two independent alarms in one tick each page once, not zero and not a burst"
else
  say_bad "two independent alarms in one tick each page once, not zero and not a burst" \
    "paged $(up_call_count) time(s): $(up_calls)"
fi

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
