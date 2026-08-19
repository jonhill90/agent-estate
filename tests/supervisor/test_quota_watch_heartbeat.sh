#!/bin/bash
# quota-watch.sh -- heartbeat, not a process check (agent-supervisor#276).
#
# WHY THIS EXISTS. On 2026-08-16 quota-watch.sh hung inside an unbounded
# `quota.sh check` call for three hours and was reported "healthy" on every
# tick, because the only thing anything checked was `pgrep` finding its pid.
# `quota.sh` itself is now bounded (#267), which is what keeps this call from
# hanging forever -- but a long-lived process keeps the code it started with,
# so the fix here is a second, independent line of defense: quota-watch.sh's
# own stamp must prove FORWARD PROGRESS ("the last iteration finished"), not
# merely that the process object exists. This drives the real quota-watch.sh
# end to end against a hanging codexbar stub and asserts the stamp still
# updates, on time, in the shape watchdog.sh's generic heartbeat check reads.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
QUOTA_WATCH="$HERE/../../scripts/supervisor/quota-watch.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }

echo "quota-watch.sh -- heartbeat, not a process check (#276)"

D=$(mktemp -d)

cat > "$D/codexbar-hang" <<'FIX'
#!/bin/bash
sleep 60
FIX
chmod +x "$D/codexbar-hang"

# --- the load-bearing case: a hung quota.sh check must not stop the stamp
# from advancing, and must not take anywhere near the stub's 60s sleep -----
STATE_DIR="$D/state-1"
start=$(date +%s)
OUT=$(CODEXBAR_BIN="$D/codexbar-hang" QUOTA_GUARD_TIMEOUT_SECONDS=2 \
        AGENT_SUPERVISOR_STATE_DIR="$STATE_DIR" \
        SUPERVISOR_STATE="$STATE_DIR" \
        bash "$QUOTA_WATCH" --once --target "nonexistent:@1" 2>&1)
RC=$?
elapsed=$(( $(date +%s) - start ))
want_exit "a single --once tick with a hanging codexbar exits 0" "$RC" 0 "$OUT"
if [ "$elapsed" -le 15 ]; then
  ok "...within quota.sh's own timeout, not the stub's 60s sleep (${elapsed}s)"
else
  bad "...within quota.sh's own timeout" "took ${elapsed}s -- the hang reached quota-watch.sh's loop"
fi

STAMP="$STATE_DIR/.quota-watch.state"
if [ -f "$STAMP" ]; then
  ok "the heartbeat stamp was written despite the hang"
else
  bad "the heartbeat stamp was written despite the hang" "no file at $STAMP"
fi
stamp_body=$(cat "$STAMP" 2>/dev/null)
want_contains "...carries a checked: timestamp (inbox-poll.status's own shape)" "checked: " "$stamp_body"
want_contains "...and a state: line" "state: " "$stamp_body"
# A hanging codexbar makes quota.sh check exit 2 (UNAVAILABLE) -- that must
# land in the stamp as UNKNOWN, never as SAFE. Reporting a hang as SAFE would
# be the fail-open direction #264/#267 exist to close, one layer further out.
want_contains "...and the state is UNKNOWN, never SAFE, for an unbounded reading" "state: UNKNOWN" "$stamp_body"

# --- written AFTER the work, not before: an iteration whose own send_resume
# never gets a chance to run (prev is empty on the very first tick, so no
# resume is attempted) still has to reach the write -- already covered above.
# This second case pins the ordering claim itself: the timestamp inside the
# stamp must be current wall-clock AT WRITE TIME, not stale by the length of
# the hang it survived.
checked_line=$(grep '^checked: ' "$STAMP" | head -1 | sed 's/^checked: *//')
threshold=$(date -u -v-30S '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d '30 seconds ago' '+%Y-%m-%dT%H:%M:%SZ')
if [[ "$checked_line" < "$threshold" ]]; then
  bad "the stamp's checked: time is current, not stale by the hang" "checked=$checked_line threshold=$threshold"
else
  ok "the stamp's checked: time is current, not stale by the hang"
fi

echo
echo "quota-watch.sh heartbeat: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
