#!/bin/bash
# agent-supervisor#474: the other half of #466. director-loop.sh's quota
# gate was already fail-closed (correct, and left unweakened here) but ran
# `"$QUOTA_GATE" check >/dev/null 2>&1` -- discarding the gate's own
# diagnostic output -- and, on anything but SAFE/WIND DOWN, only ever wrote
# one `log` line before exiting quietly. Measured live: the Director's loop
# went quiet for ~19 hours while its log filled with nothing but
# "quota UNKNOWN (rc=2) -- never treated as safe, not ticking", repeated,
# and was first misread as a mistuned guard rather than a genuine
# instrument failure. This mirrors test_director_loop_stale_streak.sh's
# shape for the SAME class of defect (#466) applied to the quota gate: a
# correct refusal must not look identical to a healthy tick after N
# consecutive occurrences.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOOP="$HERE/../../scripts/supervisor/director-loop.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_count() {
  local label="$1" needle="$2" file="$3" expect="$4"
  local got; got=$(grep -cF -- "$needle" "$file" 2>/dev/null || true)
  if [ "$got" = "$expect" ]; then ok "$label"; else bad "$label" "expected $expect of '$needle' in $file, got $got:
$(cat "$file" 2>/dev/null)"; fi
}

echo "director-loop.sh -- escalates after N CONSECUTIVE quota-gate UNKNOWN ticks, and stops discarding the gate's own diagnostics (#474)"

D=$(mktemp -d)
trap 'rm -rf "$D"' EXIT

QUOTA_UNKNOWN="$HERE/stubs/quota-unknown"
QUOTA_SAFE="$HERE/stubs/quota-safe"
cp "$HERE/stubs/notify-quota-watch" "$D/notify.sh"; chmod +x "$D/notify.sh"

tick() { # tick <quota-gate> <state-dir> <log-file> <notify-log>
  SUPERVISOR_STATE="$2" QUOTA_GATE="$1" DIRECTOR_LOOP_TARGET="director:@3" \
    DIRECTOR_LOOP_NOTIFY_SCRIPT="$D/notify.sh" NOTIFY_LOG="$4" \
    DIRECTOR_LOOP_QUOTA_ESCALATE_AFTER=3 DIRECTOR_LOOP_ALARM_COOLDOWN=0 \
    bash "$LOOP" >>"$3/out.log" 2>&1
}

# --- the gate's own diagnostic output is no longer discarded --------------
STATE=$(mktemp -d "$D/state.XXXXXX")
tick "$QUOTA_UNKNOWN" "$STATE" "$STATE" "$D/nlog.unused"
if grep -q "could not reach the quota source" "$STATE/out.log"; then
  ok "a single UNKNOWN tick's log carries the gate's own per-sample diagnostic, not just the one-line summary"
else
  bad "a single UNKNOWN tick's log carries the gate's own per-sample diagnostic" "$(cat "$STATE/out.log")"
fi

# --- GREEN: three consecutive UNKNOWN ticks escalate on the third --------
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.green"; : > "$NLOG"
tick "$QUOTA_UNKNOWN" "$STATE" "$STATE" "$NLOG"
want_count "1st consecutive UNKNOWN tick: no page yet" "director-loop: quota gate UNKNOWN" "$NLOG" 0
tick "$QUOTA_UNKNOWN" "$STATE" "$STATE" "$NLOG"
want_count "2nd consecutive UNKNOWN tick: still no page" "director-loop: quota gate UNKNOWN" "$NLOG" 0
tick "$QUOTA_UNKNOWN" "$STATE" "$STATE" "$NLOG"
want_count "3rd consecutive UNKNOWN tick (the configured threshold): pages exactly once" "director-loop: quota gate UNKNOWN" "$NLOG" 1
if grep -q "3 consecutive ticks" "$NLOG"; then
  ok "the page names the streak length"
else
  bad "the page names the streak length" "$(cat "$NLOG")"
fi

# --- a SAFE tick resets the streak: the next incident needs its own 3 ----
tick "$QUOTA_SAFE" "$STATE" "$STATE" "$NLOG"
if [ "$(cat "$STATE/.director-loop-quota-streak.state" 2>/dev/null)" = "0" ]; then
  ok "a SAFE tick resets the quota streak counter to 0"
else
  bad "a SAFE tick resets the quota streak counter to 0" "streak file: $(cat "$STATE/.director-loop-quota-streak.state" 2>/dev/null)"
fi
tick "$QUOTA_UNKNOWN" "$STATE" "$STATE" "$NLOG"
tick "$QUOTA_UNKNOWN" "$STATE" "$STATE" "$NLOG"
want_count "after recovery, two more UNKNOWN ticks still do not re-page (streak restarted)" "director-loop: quota gate UNKNOWN" "$NLOG" 1
tick "$QUOTA_UNKNOWN" "$STATE" "$STATE" "$NLOG"
want_count "...and the SECOND incident pages again once it also reaches 3" "director-loop: quota gate UNKNOWN" "$NLOG" 2

# --- a WIND DOWN tick does NOT count toward the quota-unknown streak, and
# does not itself escalate -- it is the gate doing its job, not a failure.
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.winddown"; : > "$NLOG"
cat > "$D/quota-winddown" <<'FIX'
#!/bin/bash
echo "quota: WIND DOWN -- 10% remaining in session, below 15%" >&2
exit 1
FIX
chmod +x "$D/quota-winddown"
tick "$QUOTA_UNKNOWN" "$STATE" "$STATE" "$NLOG"
tick "$QUOTA_UNKNOWN" "$STATE" "$STATE" "$NLOG"
tick "$D/quota-winddown" "$STATE" "$STATE" "$NLOG"
want_count "a WIND DOWN reading is never reported as a quota-gate page" "director-loop: quota gate UNKNOWN" "$NLOG" 0
if [ "$(cat "$STATE/.director-loop-quota-streak.state" 2>/dev/null)" = "0" ]; then
  ok "WIND DOWN also resets the UNKNOWN streak (it is not itself an instrument failure)"
else
  bad "WIND DOWN also resets the UNKNOWN streak" "streak file: $(cat "$STATE/.director-loop-quota-streak.state" 2>/dev/null)"
fi

# --- RED: mutate the escalation threshold so it can never fire -----------
BROKEN="$D/director-loop-broken-quota-streak.sh"
sed 's/if \[ "\$qstreak" -ge "\$QUOTA_ESCALATE_AFTER" \]; then/if [ "$qstreak" -ge 999999 ]; then/' \
  "$LOOP" > "$BROKEN"
if grep -qF 'if [ "$qstreak" -ge 999999 ]; then' "$BROKEN"; then
  ok "constructed a mutated copy whose quota escalation threshold can never be reached"
else
  bad "constructed a mutated copy whose quota escalation threshold can never be reached" "sed did not match -- check director-loop.sh's exact source line"
fi
chmod +x "$BROKEN"

STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.red"; : > "$NLOG"
tick_broken() {
  SUPERVISOR_STATE="$STATE" QUOTA_GATE="$QUOTA_UNKNOWN" DIRECTOR_LOOP_TARGET="director:@3" \
    DIRECTOR_LOOP_NOTIFY_SCRIPT="$D/notify.sh" NOTIFY_LOG="$NLOG" \
    DIRECTOR_LOOP_QUOTA_ESCALATE_AFTER=3 DIRECTOR_LOOP_ALARM_COOLDOWN=0 \
    bash "$BROKEN" >>"$STATE/out.log" 2>&1
}
tick_broken; tick_broken; tick_broken; tick_broken; tick_broken
want_count "RED: with the counter broken, five consecutive UNKNOWN ticks still never page" "director-loop: quota gate UNKNOWN" "$NLOG" 0
if [ "$(cat "$STATE/.director-loop-quota-streak.state" 2>/dev/null)" -ge 5 ]; then
  ok "RED: ...even though the streak itself kept counting (proves the mutation is in the escalate GATE, not the counter)"
else
  bad "RED: ...even though the streak itself kept counting" "streak file: $(cat "$STATE/.director-loop-quota-streak.state" 2>/dev/null)"
fi

echo
echo "director-loop quota streak: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
