#!/bin/bash
# heartbeat.sh must ESCALATE, not just log, when a STALLED estate cannot be
# reached or a nudge it sent cannot be verified (agent-supervisor#273).
#
# Same class of defect as quota-watch.sh's "resume did NOT take": a correct
# refusal ("a human should look") used to go only to a logfile nobody reads,
# which produced the same outward silence as no check at all.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HEARTBEAT="$HERE/../../scripts/supervisor/heartbeat.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_count() {
  local got; got=$(grep -cF -- "$2" "$3" 2>/dev/null || true)
  if [ "$got" = "$4" ]; then ok "$1"; else bad "$1" "expected $4 occurrence(s) of '$2' in $3, got $got:
$(cat "$3" 2>/dev/null)"; fi
}
want_empty() {
  if [ ! -s "$2" ]; then ok "$1"; else bad "$1" "expected no activity, got:
$(cat "$2")"; fi
}

echo "heartbeat.sh -- a STALLED estate that cannot be reached escalates to a human (#273)"

D=$(mktemp -d)
cp "$HERE/stubs/notify-quota-watch" "$D/notify.sh"; chmod +x "$D/notify.sh"

cat > "$D/gate-safe" <<'EOF'
#!/bin/bash
exit 0
EOF
chmod +x "$D/gate-safe"

run_heartbeat() {
  # $1=state dir $2=nlog $3=TMUX_SESSIONS (empty = no sessions exist)
  PATH="$D:$PATH" TMUX_LOG="$1/tmux.log" TMUX_SESSIONS="${3:-}" \
    QUOTA_GATE="$D/gate-safe" SUPERVISOR_STATE="$1" \
    HEARTBEAT_TARGET="director:@3" HEARTBEAT_STALE_AFTER=0 HEARTBEAT_NUDGE_COOLDOWN=0 \
    HEARTBEAT_NOTIFY_SCRIPT="$D/notify.sh" NOTIFY_LOG="$2" \
    bash "$HEARTBEAT" --once >"$1/heartbeat.out" 2>&1
}

# --- case 1: target session does not exist at all -- nowhere to nudge -----
STATE=$(mktemp -d "$D/state.XXXXXX"); : > "$STATE/ledger.sqlite3"
NLOG="$D/nlog.1"; : > "$NLOG"
cp "$HERE/stubs/tmux-heartbeat-target" "$D/tmux"; chmod +x "$D/tmux"
run_heartbeat "$STATE" "$NLOG" ""
want_count "no target session -- escalates exactly once" "no target session" "$NLOG" 1
want_count "...as the authorized supervisor caller" "CALLER=supervisor" "$NLOG" 1

# --- case 2: THE REPRODUCTION -- a nudge is sent but the pane never
#             confirms (the resume-did-not-take equivalent for heartbeat) --
STATE=$(mktemp -d "$D/state.XXXXXX"); : > "$STATE/ledger.sqlite3"
NLOG="$D/nlog.2"; : > "$NLOG"
cp "$HERE/stubs/tmux-heartbeat-stranded" "$D/tmux"; chmod +x "$D/tmux"
run_heartbeat "$STATE" "$NLOG"
want_count "nudge sent but pane did NOT start working -- escalates exactly once" "nudge did NOT take" "$NLOG" 1
want_count "...naming the target so the reader knows where to look" "director" "$NLOG" 1

echo
echo "heartbeat escalation: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
