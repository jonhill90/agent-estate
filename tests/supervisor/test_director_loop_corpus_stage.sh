#!/bin/bash
# agent-estate#735: director-loop.sh wires the corpus-STAGING tick step
# (corpus-stage.sh) in and escalates through the SAME alarm path
# (`send_takeover_alarm` -> notify.sh) it already uses for its other
# tick-level incidents (quota/live/stale-target), rather than inventing a
# second escalation idiom -- and never on the quota gate's critical path:
# the step runs, and can escalate, even under a SAFE quota reading with no
# Director session present at all.
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

echo "director-loop.sh -- wires corpus-stage.sh into the tick and escalates through send_takeover_alarm (#735)"

D=$(mktemp -d)
trap 'rm -rf "$D"' EXIT

QUOTA_SAFE="$HERE/stubs/quota-safe"
cp "$HERE/stubs/notify-quota-watch" "$D/notify.sh"; chmod +x "$D/notify.sh"

mk_stub() { # mk_stub <path> <exit-code> <stdout-metrics-line>
  cat > "$1" <<STUB
#!/bin/bash
echo "$3"
exit $2
STUB
  chmod +x "$1"
}

STAGE_OK="$D/corpus-stage-ok"
mk_stub "$STAGE_OK" 0 "count=3 oldest_age_seconds=120 age_threshold_seconds=86400 count_threshold=168"

STAGE_LOUD="$D/corpus-stage-loud"
mk_stub "$STAGE_LOUD" 1 "count=4 oldest_age_seconds=345600 age_threshold_seconds=86400 count_threshold=168"

STAGE_FAILED="$D/corpus-stage-failed"
mk_stub "$STAGE_FAILED" 2 ""

tick() { # tick <corpus-stage-stub> <state-dir> <notify-log>
  SUPERVISOR_STATE="$2" QUOTA_GATE="$QUOTA_SAFE" DIRECTOR_LOOP_TARGET="no-such-session-735:@1" \
    CORPUS_STAGE_SCRIPT="$1" DIRECTOR_LOOP_NOTIFY_SCRIPT="$D/notify.sh" NOTIFY_LOG="$3" \
    DIRECTOR_LOOP_ALARM_COOLDOWN=0 \
    bash "$LOOP" >>"$2/out.log" 2>&1
}

# --- a clean tick under threshold never pages ------------------------------
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.ok"; : > "$NLOG"
tick "$STAGE_OK" "$STATE" "$NLOG"
want_count "an under-threshold tick reports nothing to a human" "director-loop: prompt corpus backlog" "$NLOG" 0
if grep -q "count=3 oldest_age_seconds=120" "$STATE/out.log"; then
  ok "the tick's own log carries corpus-stage.sh's metrics line"
else
  bad "the tick's own log carries corpus-stage.sh's metrics line" "$(cat "$STATE/out.log")"
fi

# --- a threshold-crossing tick escalates through send_takeover_alarm ------
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.loud"; : > "$NLOG"
tick "$STAGE_LOUD" "$STATE" "$NLOG"
want_count "a threshold-crossing tick pages exactly once" "director-loop: prompt corpus backlog crossed loud-absence threshold" "$NLOG" 1
if grep -q "count=4 oldest_age_seconds=345600" "$NLOG"; then
  ok "the page carries the actual count/age metrics, not just a bare 'crossed' claim"
else
  bad "the page carries the actual count/age metrics" "$(cat "$NLOG")"
fi
if grep -q "CALLER=director" "$NLOG"; then
  ok "the page goes out as the director caller, same as every other alarm in this file"
else
  bad "the page goes out as the director caller" "$(cat "$NLOG")"
fi

# --- an extract failure (instrument failure) escalates differently, and is
# never mistaken for an empty backlog ---------------------------------------
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.failed"; : > "$NLOG"
tick "$STAGE_FAILED" "$STATE" "$NLOG"
want_count "an extract failure pages as an instrument failure" "director-loop: corpus staging failed" "$NLOG" 1
want_count "...and is never reported as a threshold escalation" "director-loop: prompt corpus backlog crossed" "$NLOG" 0

# --- respects the shared alarm cooldown, same as every other escalation in
# this file (no separate cooldown invented for this one) --------------------
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.cooldown"; : > "$NLOG"
SUPERVISOR_STATE="$STATE" QUOTA_GATE="$QUOTA_SAFE" DIRECTOR_LOOP_TARGET="no-such-session-735:@1" \
  CORPUS_STAGE_SCRIPT="$STAGE_LOUD" DIRECTOR_LOOP_NOTIFY_SCRIPT="$D/notify.sh" NOTIFY_LOG="$NLOG" \
  DIRECTOR_LOOP_ALARM_COOLDOWN=3600 \
  bash "$LOOP" >>"$STATE/out.log" 2>&1
SUPERVISOR_STATE="$STATE" QUOTA_GATE="$QUOTA_SAFE" DIRECTOR_LOOP_TARGET="no-such-session-735:@1" \
  CORPUS_STAGE_SCRIPT="$STAGE_LOUD" DIRECTOR_LOOP_NOTIFY_SCRIPT="$D/notify.sh" NOTIFY_LOG="$NLOG" \
  DIRECTOR_LOOP_ALARM_COOLDOWN=3600 \
  bash "$LOOP" >>"$STATE/out.log" 2>&1
want_count "a second still-loud tick inside the cooldown does not page again" "director-loop: prompt corpus backlog crossed loud-absence threshold" "$NLOG" 1

# --- a missing corpus-stage.sh is skipped, not fatal to the tick ----------
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.missing"; : > "$NLOG"
tick "$D/no-such-corpus-stage.sh" "$STATE" "$NLOG"
if grep -q "corpus-stage: .*not found or not executable -- skipping" "$STATE/out.log"; then
  ok "a missing corpus-stage.sh is logged and skipped rather than crashing the tick"
else
  bad "a missing corpus-stage.sh is logged and skipped rather than crashing the tick" "$(cat "$STATE/out.log")"
fi
want_count "a missing corpus-stage.sh does not page" "director-loop: prompt corpus backlog" "$NLOG" 0
want_count "...nor does it page as a staging failure" "director-loop: corpus staging failed" "$NLOG" 0

# --- RED: mutate the exit-code dispatch so a threshold crossing (rc=1) is
# read the same as clean (rc=0) -- proves the GREEN case above actually
# depends on the case statement, not on incidental log text ----------------
BROKEN="$D/director-loop-broken-corpus.sh"
sed 's/^    1)$/    99)/' "$LOOP" > "$BROKEN"
if grep -qF '    99)' "$BROKEN"; then
  ok "constructed a mutated copy whose rc=1 case arm is unreachable"
else
  bad "constructed a mutated copy whose rc=1 case arm is unreachable" "sed did not match -- check director-loop.sh's exact source line"
fi
chmod +x "$BROKEN"
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.red"; : > "$NLOG"
SUPERVISOR_STATE="$STATE" QUOTA_GATE="$QUOTA_SAFE" DIRECTOR_LOOP_TARGET="no-such-session-735:@1" \
  CORPUS_STAGE_SCRIPT="$STAGE_LOUD" DIRECTOR_LOOP_NOTIFY_SCRIPT="$D/notify.sh" NOTIFY_LOG="$NLOG" \
  DIRECTOR_LOOP_ALARM_COOLDOWN=0 \
  bash "$BROKEN" >>"$STATE/out.log" 2>&1
want_count "RED: with rc=1 mutated to fall into the rc=0 no-op branch, the same loud stub no longer pages" \
  "director-loop: prompt corpus backlog crossed loud-absence threshold" "$NLOG" 0

echo
echo "director-loop corpus-stage wiring: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
