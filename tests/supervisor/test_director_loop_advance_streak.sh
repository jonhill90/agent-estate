#!/bin/bash
# agent-supervisor#470: the remaining half of #466's pattern -- #474/#476
# already fixed the quota-gate half (test_director_loop_quota_streak.sh);
# this covers the other silent-skip gate #470 named. advance-live.sh's own
# `git fetch` has a hard ADVANCE_FETCH_TIMEOUT_SECONDS bound (20s default)
# and can time out under load. Before this fix, director-loop.sh's LIVE
# MISSING recovery block paged on the very FIRST failed recovery attempt --
# a single flaky fetch escalated immediately, the opposite failure mode from
# the quota gate's old silence, but still not the "consecutive occurrences
# of a standing incident" shape #466 established. This mirrors
# test_director_loop_quota_streak.sh's shape applied to the advance-live
# recovery path: N-1 consecutive failures must not page, the Nth must, and
# a single success in between must reset the streak.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_count() {
  local label="$1" needle="$2" file="$3" expect="$4"
  local got; got=$(grep -cF -- "$needle" "$file" 2>/dev/null || true)
  if [ "$got" = "$expect" ]; then ok "$label"; else bad "$label" "expected $expect of '$needle' in $file, got $got:
$(cat "$file" 2>/dev/null)"; fi
}

echo "director-loop.sh -- escalates after N CONSECUTIVE advance-live fetch-timeout skips, resets on recovery (#470)"

D=$(mktemp -d)
trap 'rm -rf "$D"' EXIT

QUOTA_SAFE="$HERE/stubs/quota-safe"
cp "$HERE/stubs/notify-quota-watch" "$D/notify.sh"; chmod +x "$D/notify.sh"

# director-loop.sh resolves advance-live.sh beside its OWN script directory
# ("$HERE/advance-live.sh"), so a scratch copy of director-loop.sh with a
# controllable advance-live.sh stub beside it is required -- same technique
# test_director_loop_live_check.sh's S4 case uses for "no advance-live.sh".
SCRATCH="$D/scratch"
mkdir -p "$SCRATCH"
cp "$HERE/../../scripts/supervisor/director-loop.sh" "$SCRATCH/director-loop.sh"
chmod +x "$SCRATCH/director-loop.sh"
LOOP="$SCRATCH/director-loop.sh"

use_failing_advance_live() { # use_failing_advance_live <dir-beside-director-loop.sh>
  cat > "$1/advance-live.sh" <<'FIX'
#!/bin/bash
echo "advance-live: git fetch origin/main in $1 did not finish within 20s and was killed -- refusing to compare against a ref nothing finished refreshing; this is UNKNOWN, not current" >&2
exit 1
FIX
  chmod +x "$1/advance-live.sh"
}

use_succeeding_advance_live() { # use_succeeding_advance_live <dir-beside-director-loop.sh>
  cat > "$1/advance-live.sh" <<'FIX'
#!/bin/bash
mkdir -p "$1" && git init -q "$1" >/dev/null 2>&1
exit 0
FIX
  chmod +x "$1/advance-live.sh"
}

tick() { # tick <state-dir> <notify-log>
  SUPERVISOR_STATE="$1" SUPERVISOR_LIVE="$1/live" QUOTA_GATE="$QUOTA_SAFE" \
    DIRECTOR_LOOP_TARGET="no-such-session-470:@1" \
    DIRECTOR_LOOP_NOTIFY_SCRIPT="$D/notify.sh" NOTIFY_LOG="$2" \
    DIRECTOR_LOOP_ADVANCE_ESCALATE_AFTER=3 DIRECTOR_LOOP_ALARM_COOLDOWN=0 \
    bash "$LOOP" >>"$1/out.log" 2>&1
}

# --- a failed recovery attempt's own output is not discarded ---------------
use_failing_advance_live "$SCRATCH"
STATE=$(mktemp -d "$D/state.XXXXXX")
tick "$STATE" "$D/nlog.unused"
if grep -q "did not finish within 20s" "$STATE/out.log"; then
  ok "a failed recovery tick's log carries advance-live.sh's own diagnostic output"
else
  bad "a failed recovery tick's log carries advance-live.sh's own diagnostic output" "$(cat "$STATE/out.log")"
fi

# --- GREEN: three consecutive fetch-timeout skips escalate on the third ----
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.green"; : > "$NLOG"
tick "$STATE" "$NLOG"
want_count "1st consecutive fetch-timeout skip: no page yet" "director-loop: live/ MISSING and self-heal failed" "$NLOG" 0
tick "$STATE" "$NLOG"
want_count "2nd consecutive fetch-timeout skip: still no page" "director-loop: live/ MISSING and self-heal failed" "$NLOG" 0
tick "$STATE" "$NLOG"
want_count "3rd consecutive fetch-timeout skip (the configured threshold): pages exactly once" "director-loop: live/ MISSING and self-heal failed" "$NLOG" 1
if grep -q "3 consecutive ticks" "$NLOG"; then
  ok "the page names the streak length"
else
  bad "the page names the streak length" "$(cat "$NLOG")"
fi

# --- a successful recovery resets the streak: the next incident needs its
# own 3 --------------------------------------------------------------------
use_succeeding_advance_live "$SCRATCH"
tick "$STATE" "$NLOG"
if [ "$(cat "$STATE/.director-loop-advance-streak.state" 2>/dev/null)" = "0" ]; then
  ok "a successful recovery tick resets the advance streak counter to 0"
else
  bad "a successful recovery tick resets the advance streak counter to 0" "streak file: $(cat "$STATE/.director-loop-advance-streak.state" 2>/dev/null)"
fi
use_failing_advance_live "$SCRATCH"
tick "$STATE" "$NLOG"
tick "$STATE" "$NLOG"
want_count "after recovery, two more fetch-timeout skips still do not re-page (streak restarted)" "director-loop: live/ MISSING and self-heal failed" "$NLOG" 1
tick "$STATE" "$NLOG"
want_count "...and the SECOND incident pages again once it also reaches 3" "director-loop: live/ MISSING and self-heal failed" "$NLOG" 2

# --- RED: mutate the escalation threshold so it can never fire -------------
BROKEN="$D/director-loop-broken-advance-streak.sh"
sed 's/if \[ "\$astreak" -ge "\$ADVANCE_ESCALATE_AFTER" \]; then/if [ "$astreak" -ge 999999 ]; then/' \
  "$LOOP" > "$BROKEN"
if grep -qF 'if [ "$astreak" -ge 999999 ]; then' "$BROKEN"; then
  ok "constructed a mutated copy whose advance escalation threshold can never be reached"
else
  bad "constructed a mutated copy whose advance escalation threshold can never be reached" "sed did not match -- check director-loop.sh's exact source line"
fi
chmod +x "$BROKEN"
# BROKEN resolves "$HERE/advance-live.sh" against its OWN directory ($D),
# not $SCRATCH -- give it its own failing stub there.
use_failing_advance_live "$D"

STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.red"; : > "$NLOG"
tick_broken() {
  SUPERVISOR_STATE="$STATE" SUPERVISOR_LIVE="$STATE/live" QUOTA_GATE="$QUOTA_SAFE" \
    DIRECTOR_LOOP_TARGET="no-such-session-470:@1" \
    DIRECTOR_LOOP_NOTIFY_SCRIPT="$D/notify.sh" NOTIFY_LOG="$NLOG" \
    DIRECTOR_LOOP_ADVANCE_ESCALATE_AFTER=3 DIRECTOR_LOOP_ALARM_COOLDOWN=0 \
    bash "$BROKEN" >>"$STATE/out.log" 2>&1
}
tick_broken; tick_broken; tick_broken; tick_broken; tick_broken
want_count "RED: with the counter broken, five consecutive fetch-timeout skips still never page" "director-loop: live/ MISSING and self-heal failed" "$NLOG" 0
if [ "$(cat "$STATE/.director-loop-advance-streak.state" 2>/dev/null)" -ge 5 ]; then
  ok "RED: ...even though the streak itself kept counting (proves the mutation is in the escalate GATE, not the counter)"
else
  bad "RED: ...even though the streak itself kept counting" "streak file: $(cat "$STATE/.director-loop-advance-streak.state" 2>/dev/null)"
fi

echo
echo "director-loop advance streak: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
