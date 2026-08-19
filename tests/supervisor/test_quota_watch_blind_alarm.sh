#!/bin/bash
# quota-watch.sh must ALARM when it goes blind across a reset boundary, and
# must NOT alarm on the correct quiet case (agent-supervisor#305).
#
# WHY THIS EXISTS. Measured 2026-08-17: quota-watch.sh was alive, on
# schedule, ticking every 300s under launchd -- and its two readings either
# side of a session-window reset were both UNKNOWN (rc=2). `confirmed` is
# correctly never overwritten by a transient UNKNOWN (that is the fix from
# #260), so the watcher sat on its last confirmed state, which happened to
# be WINDDOWN. From outside, that looks EXACTLY like a watcher correctly
# holding a real wind-down. Nothing distinguished the two, so nothing
# alarmed, and the estate stayed down until Jon noticed by hand -- he was
# the detector, which is the actual defect this issue exists to fix.
#
# `confirmed` staying frozen through a transient blip must NOT change --
# that refusal is correct (see quota-watch.sh's own header and #260's
# tests). What must change is that a RUN of UNKNOWNs pages a human, because
# a watcher that cannot see is not the same fact as a watcher that saw a
# real wind-down, even though `confirmed` reads identically either way.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCH="$HERE/../../scripts/supervisor/quota-watch.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_count() {
  # $1 label, $2 needle, $3 file, $4 expected count
  local got; got=$(grep -cF -- "$2" "$3" 2>/dev/null || true)
  if [ "$got" = "$4" ]; then ok "$1"; else bad "$1" "expected $4 occurrence(s) of '$2' in $3, got $got:
$(cat "$3" 2>/dev/null)"; fi
}
want_empty() {
  # $1 label, $2 file -- asserts the file is absent or empty (nothing sent)
  if [ ! -s "$2" ]; then ok "$1"; else bad "$1" "expected no activity, got:
$(cat "$2")"; fi
}

echo "quota-watch.sh -- blind-alarm across a reset boundary, no false pages (#305)"

D=$(mktemp -d)
cp "$HERE/stubs/tmux-quota-watch" "$D/tmux"; chmod +x "$D/tmux"
cp "$HERE/stubs/notify-quota-watch" "$D/notify.sh"; chmod +x "$D/notify.sh"

gate() {
  local path="$1" rc="$2"
  cat > "$path" <<FIX
#!/bin/bash
exit $rc
FIX
  chmod +x "$path"
}
gate "$D/gate-safe" 0        # rc 0 SAFE
gate "$D/gate-winddown" 1    # rc 1 WIND DOWN
gate "$D/gate-unknown2" 2    # rc 2 UNAVAILABLE -- codexbar reached but flaky
gate "$D/gate-unknown3" 3    # rc 3 MISSING -- codexbar not installed

tick() {
  # $1 = quota gate path, uses $STATE/$LOG/$NLOG from caller scope
  PATH="$D:$PATH" QUOTA_GATE="$1" TMUX_LOG="$LOG" SUPERVISOR_STATE="$STATE" \
    QUOTA_WATCH_TARGET="t:@1" QUOTA_WATCH_NOTIFY_SCRIPT="$D/notify.sh" \
    NOTIFY_LOG="$NLOG" \
    bash "$WATCH" --once >>"$STATE/quota-watch.out" 2>&1
}

# --- case 1: THE MEASURED SHAPE -- a reset boundary where the watcher goes
# blind exactly like 2026-08-17 (WINDDOWN confirmed, then two UNKNOWNs
# straddling the reset) must page, because it is indistinguishable from
# outside from a watcher correctly holding a real wind-down. -------------
STATE=$(mktemp -d "$D/state.XXXXXX"); LOG="$D/log.1"; NLOG="$D/nlog.1"; : > "$LOG"; : > "$NLOG"
tick "$D/gate-safe"
tick "$D/gate-winddown"
want_count "the genuine wind-down still sends exactly one QUOTA IS LOW" "QUOTA IS LOW" "$LOG" 1
want_empty "...and does not page (a real WINDDOWN is a definite reading, not blindness)" "$NLOG"
tick "$D/gate-unknown2"
want_empty "one UNKNOWN across the reset boundary alone does not yet page" "$NLOG"
tick "$D/gate-unknown3"
want_count "a SECOND consecutive UNKNOWN across the reset boundary pages exactly once" "quota-watch BLIND" "$NLOG" 1
want_count "...and the page names the caller as supervisor (notify.sh's authorized-caller contract)" "CALLER=supervisor" "$NLOG" 1
STAMP="$STATE/.quota-watch.state"
want_count "the heartbeat stamp records the streak that triggered it" "unknown_streak: 2" "$STAMP" 1
want_count "...and records the alarm as sent" "blind_alarm_sent: 1" "$STAMP" 1

# --- ...and staying blind on later ticks does not re-page every tick -----
# (#273: "escalate on the unrecoverable condition only, and rate-limit it --
# one message per incident, not one per tick" -- a repeated alarm becomes a
# log, and a log becomes ignored, which is the exact defect #273 exists to
# fix. This alarm must not reproduce it.)
tick "$D/gate-unknown2"
tick "$D/gate-unknown3"
want_count "staying blind for two more ticks does not send a second page" "quota-watch BLIND" "$NLOG" 1

# --- ...and once vision returns, a LATER blind spell pages again ---------
tick "$D/gate-safe"
want_count "vision returning clears the streak (no stray page on recovery)" "quota-watch BLIND" "$NLOG" 1
tick "$D/gate-unknown2"
tick "$D/gate-unknown3"
want_count "a fresh blind spell after recovery pages again -- not suppressed forever" "quota-watch BLIND" "$NLOG" 2

# --- case 2: THE QUIET CASE -- a single, isolated UNKNOWN blip between two
# SAFE readings (codexbar flaked once, nothing wrong) must NOT page. A
# pager that cries wolf on a single blip gets ignored, and this alarm exists
# specifically so Jon does not have to keep being the detector. -----------
STATE=$(mktemp -d "$D/state.XXXXXX"); LOG="$D/log.2"; NLOG="$D/nlog.2"; : > "$LOG"; : > "$NLOG"
tick "$D/gate-safe"
tick "$D/gate-unknown2"
tick "$D/gate-safe"
want_empty "a single isolated UNKNOWN blip between two SAFE readings never pages" "$NLOG"
tick "$D/gate-unknown3"
tick "$D/gate-safe"
want_empty "...and neither does a second, later isolated blip (each one alone, not a run)" "$NLOG"

# --- case 3: a run of UNKNOWNs with no prior confirmed state at all (cold
# start straight into blindness) must still page -- the alarm cannot depend
# on `confirmed` already being WINDDOWN. -----------------------------------
STATE=$(mktemp -d "$D/state.XXXXXX"); LOG="$D/log.3"; NLOG="$D/nlog.3"; : > "$LOG"; : > "$NLOG"
tick "$D/gate-unknown2"
want_empty "one UNKNOWN on a cold start (no prior confirmed state) does not yet page" "$NLOG"
tick "$D/gate-unknown3"
want_count "a second UNKNOWN on a cold start pages, even with no prior confirmed reading" "quota-watch BLIND" "$NLOG" 1

# --- case 4: a failed page (channel unreachable) is retried, not silently
# marked as sent -- the same "flag means told, not tried" discipline
# watchdog_notify.py's episode logic already uses. --------------------------
STATE=$(mktemp -d "$D/state.XXXXXX"); LOG="$D/log.4"; NLOG="$D/nlog.4"; : > "$LOG"; : > "$NLOG"
tick_failing() {
  PATH="$D:$PATH" QUOTA_GATE="$1" TMUX_LOG="$LOG" SUPERVISOR_STATE="$STATE" \
    QUOTA_WATCH_TARGET="t:@1" QUOTA_WATCH_NOTIFY_SCRIPT="$D/notify.sh" \
    NOTIFY_LOG="$NLOG" NOTIFY_FAIL=1 \
    bash "$WATCH" --once >>"$STATE/quota-watch.out" 2>&1
}
tick_failing "$D/gate-unknown2"
tick_failing "$D/gate-unknown3"
want_count "a failed channel is still attempted on the triggering tick" "quota-watch BLIND" "$NLOG" 1
STAMP4="$STATE/.quota-watch.state"
want_count "...and is NOT marked as sent, since the channel refused it" "blind_alarm_sent: 0" "$STAMP4" 1
tick_failing "$D/gate-unknown2"
want_count "...so the next still-blind tick retries the page" "quota-watch BLIND" "$NLOG" 2

echo
echo "quota-watch blind-alarm: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
