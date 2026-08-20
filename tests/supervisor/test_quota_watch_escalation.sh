#!/bin/bash
# quota-watch.sh must ESCALATE, not just log, when a send it made cannot be
# verified (agent-supervisor#273).
#
# WHY THIS EXISTS. quota-watch.sh logged, at 14:49:30:
#   "resume did NOT take -- pane is not working after send; a human should look"
# No human looked for an hour, because the warning went to a logfile nobody
# reads. That refusal was CORRECT -- the estate was genuinely unreachable --
# and must not be weakened; the defect is that a correct refusal produced
# the same outward silence as no check at all. This is the exact
# reproduction case from the issue:
#   1. confirmed state is WINDDOWN
#   2. quota.sh reports SAFE (the genuine WINDDOWN -> SAFE edge)
#   3. the resume send goes out, but the pane never shows the working marker
# and asserts a page fires exactly once, names the target, and says what to
# do -- not just "a human should look".
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCH="$HERE/../../scripts/supervisor/quota-watch.sh"
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

echo "quota-watch.sh -- an unverified send escalates to a human, not just a logfile (#273)"

D=$(mktemp -d)
# Two tmux stubs: GOOD confirms every send (used to prime a real, correctly
# delivered state without exercising the escalation path); STRANDED never
# shows the working marker (used for the tick under test).
mkdir -p "$D/good" "$D/bad"
cp "$HERE/stubs/tmux-quota-watch" "$D/good/tmux"; chmod +x "$D/good/tmux"
cp "$HERE/stubs/tmux-quota-watch-stranded" "$D/bad/tmux"; chmod +x "$D/bad/tmux"
cp "$HERE/stubs/notify-quota-watch" "$D/notify.sh"; chmod +x "$D/notify.sh"

gate() {
  local path="$1" rc="$2"
  cat > "$path" <<FIX
#!/bin/bash
exit $rc
FIX
  chmod +x "$path"
}
gate "$D/gate-safe" 0
gate "$D/gate-winddown" 1

tick_with() {
  # $1 = tmux stub dir (good/bad), $2 = quota gate path
  PATH="$D/$1:$PATH" QUOTA_GATE="$2" TMUX_LOG="$LOG" SUPERVISOR_STATE="$STATE" \
    QUOTA_WATCH_TARGET="t:@1" QUOTA_WATCH_NOTIFY_SCRIPT="$D/notify.sh" \
    NOTIFY_LOG="$NLOG" \
    bash "$WATCH" --once >>"$STATE/quota-watch.out" 2>&1
}
tick() { tick_with bad "$1"; }   # default: the stub under test (stranded)

# --- case 1: THE ISSUE'S OWN REPRODUCTION -- WINDDOWN -> SAFE with a pane
# that never confirms must page exactly once, and the page must say what to
# check, not just "a human should look". ----------------------------------
STATE=$(mktemp -d "$D/state.XXXXXX"); LOG="$D/log.1"; NLOG="$D/nlog.1"; : > "$LOG"; : > "$NLOG"
tick_with good "$D/gate-winddown"
want_empty "no page on a wind-down that DOES take" "$NLOG"
tick "$D/gate-safe"
want_count "the resume did NOT take -- exactly one escalation fires" "resume did NOT take" "$NLOG" 1
want_count "...naming the target, not just 'a human should look'" "t:@1" "$NLOG" 1
want_count "...telling the reader what to do, not just that something is wrong" "tmux attach" "$NLOG" 1
want_count "...and it is sent as the authorized supervisor caller" "CALLER=supervisor" "$NLOG" 1

# --- case 2: staying stuck on later ticks (still WINDDOWN->confirmed=SAFE,
# no further edge) does not repage -- the edge already fired and confirmed
# is now SAFE, so a third SAFE tick sends nothing more, escalation included.
tick "$D/gate-safe"
want_count "a repeated SAFE reading (no new edge) does not escalate again" "resume did NOT take" "$NLOG" 1

# --- case 3: the SAME reproduction on the OTHER edge (SAFE -> WINDDOWN) --
STATE=$(mktemp -d "$D/state.XXXXXX"); LOG="$D/log.3"; NLOG="$D/nlog.3"; : > "$LOG"; : > "$NLOG"
tick "$D/gate-safe"
want_empty "no page on the first-ever SAFE reading" "$NLOG"
tick "$D/gate-winddown"
want_count "the wind-down did NOT take -- exactly one escalation fires" "wind-down did NOT take" "$NLOG" 1
want_count "...naming the target" "t:@1" "$NLOG" 1

echo
echo "quota-watch escalation: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
