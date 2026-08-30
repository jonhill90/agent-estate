#!/bin/bash
# director-loop.sh must ESCALATE, not just log, when it hits a state it
# cannot recover from itself (agent-supervisor#273) -- same class of defect
# as quota-watch.sh's "resume did NOT take": a correct "a human should
# look" refusal used to go only to a logfile nobody reads.
#
# UNLIKE heartbeat.sh (which only enters its stalled branch once per
# NUDGE_COOLDOWN) and quota-watch.sh (which only fires on a state EDGE),
# director-loop.sh ticks every LOOP_INTERVAL with no built-in cooldown, so
# its escalation needs its own rate limit -- covered by case 3 below.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOOP="$HERE/../../scripts/supervisor/director-loop.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_count() {
  local got; got=$(grep -cF -- "$2" "$3" 2>/dev/null || true)
  if [ "$got" = "$4" ]; then ok "$1"; else bad "$1" "expected $4 occurrence(s) of '$2' in $3, got $got:
$(cat "$3" 2>/dev/null)"; fi
}

echo "director-loop.sh -- an unrecoverable state escalates to a human (#273)"

D=$(mktemp -d)
cp "$HERE/stubs/notify-quota-watch" "$D/notify.sh"; chmod +x "$D/notify.sh"
QUOTA_SAFE="$HERE/stubs/quota-safe"

# A fixture repo standing in for the shared checkout SUPERVISOR_REPO names,
# same fixture shape test_director_loop_live_check.sh already uses -- a
# minimal real git repo so the live/ registration check passes cleanly and
# these cases can reach the tmux-target logic under test.
git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/src"
SRC="$D/src"
git -C "$SRC" config user.email test@example.com
git -C "$SRC" config user.name "Test"
git -C "$SRC" checkout -q -b main
mkdir -p "$SRC/scripts/supervisor"
: > "$SRC/scripts/supervisor/.keep"
git -C "$SRC" add -A
git -C "$SRC" commit -q -m base
git -C "$SRC" push -q -u origin main
LIVE="$D/live"
git -C "$SRC" worktree add -q --detach "$LIVE" origin/main

# --- case 1: target session does not exist -- escalates exactly once -----
STATE=$(mktemp -d "$D/state.XXXXXX")
cp "$HERE/stubs/tmux-heartbeat-target" "$D/tmux"; chmod +x "$D/tmux"
NLOG="$D/nlog.1"; : > "$NLOG"
PATH="$D:$PATH" TMUX_LOG="$STATE/tmux.log" TMUX_SESSIONS="" \
  SUPERVISOR_STATE="$STATE" SUPERVISOR_LIVE="$LIVE" SUPERVISOR_REPO="$SRC" \
  QUOTA_GATE="$QUOTA_SAFE" DIRECTOR_LOOP_TARGET="director:@3" \
  DIRECTOR_LOOP_NOTIFY_SCRIPT="$D/notify.sh" NOTIFY_LOG="$NLOG" \
  bash "$LOOP" >"$STATE/out.1" 2>&1
want_count "no target session -- escalates exactly once" "target session missing" "$NLOG" 1
want_count "...as the authorized director caller" "CALLER=director" "$NLOG" 1

# --- case 2: THE REPRODUCTION -- ticked, but the pane never confirms -----
STATE=$(mktemp -d "$D/state.XXXXXX")
cp "$HERE/stubs/tmux-heartbeat-stranded" "$D/tmux"; chmod +x "$D/tmux"
NLOG="$D/nlog.2"; : > "$NLOG"
PATH="$D:$PATH" TMUX_LOG="$STATE/tmux.log" \
  SUPERVISOR_STATE="$STATE" SUPERVISOR_LIVE="$LIVE" SUPERVISOR_REPO="$SRC" \
  QUOTA_GATE="$QUOTA_SAFE" DIRECTOR_LOOP_TARGET="director:@3" \
  DIRECTOR_LOOP_NOTIFY_SCRIPT="$D/notify.sh" NOTIFY_LOG="$NLOG" \
  bash "$LOOP" >"$STATE/out.2" 2>&1
want_count "ticked but pane did NOT start working -- escalates exactly once" "tick did NOT take" "$NLOG" 1
want_count "...telling the reader what to do" "tmux attach" "$NLOG" 1

# --- case 3: RATE LIMIT -- two stalls in a row within the cooldown window
# page only once, since director-loop.sh has no NUDGE_COOLDOWN of its own
# the way heartbeat.sh does. Reuses the SAME state dir (so the alarm stamp
# persists) and a long cooldown so the second tick is still inside it. -----
STATE=$(mktemp -d "$D/state.XXXXXX")
NLOG="$D/nlog.3"; : > "$NLOG"
run3() {
  PATH="$D:$PATH" TMUX_LOG="$STATE/tmux.log" \
    SUPERVISOR_STATE="$STATE" SUPERVISOR_LIVE="$LIVE" SUPERVISOR_REPO="$SRC" \
    QUOTA_GATE="$QUOTA_SAFE" DIRECTOR_LOOP_TARGET="director:@3" \
    DIRECTOR_LOOP_NOTIFY_SCRIPT="$D/notify.sh" NOTIFY_LOG="$NLOG" \
    DIRECTOR_LOOP_ALARM_COOLDOWN=3600 \
    bash "$LOOP" >>"$STATE/out.3" 2>&1
}
run3
run3
want_count "a second stalled tick inside the cooldown does not page again" "tick did NOT take" "$NLOG" 1
grep -q "alarm suppressed (cooldown" "$STATE/out.3" \
  && ok "...and says the second one was suppressed by cooldown, not silently dropped" \
  || bad "...and says the second one was suppressed by cooldown, not silently dropped" "$(cat "$STATE/out.3")"

rm -rf "$D"

echo
echo "director-loop escalation: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
