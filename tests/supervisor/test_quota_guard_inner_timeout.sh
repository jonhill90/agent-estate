#!/bin/bash
# agent-supervisor#474: codexbar degraded to ~60s per `guard` call, which put
# it PAST quota.sh's own GUARD_TIMEOUT_SECONDS (default 45s, and the tighter
# bounds these tests use) -- so `with_timeout` SIGKILLed it before codexbar's
# own default --timeout (60s) ever fired. codexbar's own `guard --help` says
# 69 is a stable, documented code ("quota unavailable or fetch timed out")
# with a machine-readable JSON reason attached when it gets the chance to
# self-report. A SIGKILLed process never gets that chance -- measured live:
#
#   $ codexbar guard --provider claude --timeout 1 --json
#   {"decision":"unknown","remainingPercent":null,"exitCode":69,
#    "unavailableReason":"timeout", ...}                    (rc=69, ~1.1s)
#
# quota.sh now passes codexbar its OWN --timeout, a few seconds under
# GUARD_TIMEOUT_SECONDS (GUARD_INNER_TIMEOUT_SECONDS), so a slow-but-honest
# codexbar gets the chance to self-report a diagnosable 69 instead of being
# killed into silence. This file pins two directions the issue asked for
# directly:
#
#   1. codexbar honours --timeout and self-reports 69 within budget -- the
#      reason (unavailableReason) reaches quota.sh's output, and quota.sh
#      still refuses (never treats this as SAFE).
#   2. codexbar answers SAFE, but slowly (reachable, not instant) -- must
#      NOT be mistaken for "unreachable" just because it took a while; a
#      real answer inside the timeout budget is still SAFE.
#
# Plus a regression guard: a codexbar that ignores --timeout entirely (an
# old/misbehaving build) must still be bounded by the OUTER with_timeout --
# passing codexbar its own timeout is an enhancement, never a replacement
# for the existing kill.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
QUOTA="$HERE/../../scripts/supervisor/quota.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_not_contains() { if grep -qF -- "$2" <<<"$3"; then bad "$1" "did not want '$2' in: $3"; else ok "$1"; fi }

echo "quota.sh -- codexbar's own --timeout, unreachable vs slow-but-reachable (#474)"

D=$(mktemp -d)

# --- case 1: codexbar honours --timeout, self-reports 69 within budget ----
# Reads the --timeout value quota.sh passed it, sleeps just under that, then
# emits the same shape codexbar itself was measured emitting on a real
# timeout. Records the args it was called with so the test can assert
# quota.sh actually asked for an inner timeout LESS than the outer bound.
cat > "$D/codexbar-self-report" <<'FIX'
#!/bin/bash
argfile="${CODEXBAR_ARGFILE:-/dev/null}"
printf '%s\n' "$*" > "$argfile"
timeout_val=10
prev=""
for a in "$@"; do
  if [ "$prev" = "--timeout" ]; then timeout_val="$a"; fi
  prev="$a"
done
# Sleep just under codexbar's own claimed timeout, then self-report -- the
# behaviour codexbar's --help documents and this repo measured directly.
sleep_for=$((timeout_val > 1 ? timeout_val - 1 : 0))
sleep "$sleep_for"
echo '{"decision":"unknown","remainingPercent":null,"exitCode":69,"unavailableReason":"timeout","minimumRemainingPercent":15,"window":"session","provider":"claude"}'
exit 69
FIX
chmod +x "$D/codexbar-self-report"

STATE_DIR="$D/state-1"
ARGFILE="$D/args-1.txt"
start=$(date +%s)
OUT=$(CODEXBAR_BIN="$D/codexbar-self-report" CODEXBAR_ARGFILE="$ARGFILE" \
        QUOTA_GUARD_TIMEOUT_SECONDS=8 QUOTA_CHECK_SAMPLES=1 \
        AGENT_SUPERVISOR_STATE_DIR="$STATE_DIR" bash "$QUOTA" check 2>&1)
RC=$?
elapsed=$(( $(date +%s) - start ))
want_exit "a codexbar that self-reports 69 within its own --timeout still refuses (rc=2)" "$RC" 2 "$OUT"
want_contains "...and relays codexbar's own reason (unavailableReason)" "quota unknown (timeout)" "$OUT"
want_not_contains "...never claims SAFE" "SAFE" "$OUT"
if [ "$elapsed" -le 8 ]; then
  ok "...finished within the outer bound (${elapsed}s), not the full 8s SIGKILL wait"
else
  bad "...finished within the outer bound" "took ${elapsed}s, expected well under 8s"
fi
args=$(cat "$ARGFILE" 2>/dev/null || echo "<no argfile written>")
want_contains "...quota.sh told codexbar its own --timeout" "--timeout" "$args"
case "$args" in
  *"--timeout 8"*) bad "...inner --timeout is strictly less than the outer bound" "args: $args" ;;
  *"--timeout"*)   ok "...inner --timeout is strictly less than the outer bound" ;;
  *)               bad "...inner --timeout is strictly less than the outer bound" "no --timeout seen: $args" ;;
esac

# --- case 2: reachable but slow -- must resolve SAFE, not UNAVAILABLE -----
cat > "$D/codexbar-slow-safe" <<'FIX'
#!/bin/bash
# Answers well inside the outer timeout, but not instantly -- the exact
# "reachable-but-slow is not mistaken for unreachable" case #474 asked for.
sleep 2
echo '{"decision":"ok","exitCode":0,"remainingPercent":51,"provider":"claude","window":"session","minimumRemainingPercent":15,"unavailableReason":null}'
exit 0
FIX
chmod +x "$D/codexbar-slow-safe"

STATE_DIR2="$D/state-2"
start=$(date +%s)
OUT2=$(CODEXBAR_BIN="$D/codexbar-slow-safe" QUOTA_GUARD_TIMEOUT_SECONDS=8 QUOTA_CHECK_SAMPLES=1 \
         AGENT_SUPERVISOR_STATE_DIR="$STATE_DIR2" bash "$QUOTA" check 2>&1)
RC2=$?
elapsed2=$(( $(date +%s) - start ))
want_exit "a slow-but-reachable SAFE answer (2s, budget 8s) still exits 0" "$RC2" 0 "$OUT2"
want_contains "...and says SAFE, not UNAVAILABLE" "SAFE" "$OUT2"
want_not_contains "...never says TIMEOUT for an answer that arrived" "TIMEOUT" "$OUT2"
if [ "$elapsed2" -le 8 ]; then
  ok "...within budget (${elapsed2}s)"
else
  bad "...within budget" "took ${elapsed2}s"
fi

# --- case 3: codexbar ignores --timeout entirely -- outer bound still wins
cat > "$D/codexbar-ignores-timeout" <<'FIX'
#!/bin/bash
# A misbehaving/old codexbar that never honours --timeout at all. The outer
# with_timeout in quota.sh must be the backstop regardless.
sleep 60
FIX
chmod +x "$D/codexbar-ignores-timeout"

STATE_DIR3="$D/state-3"
start=$(date +%s)
OUT3=$(CODEXBAR_BIN="$D/codexbar-ignores-timeout" QUOTA_GUARD_TIMEOUT_SECONDS=3 QUOTA_CHECK_SAMPLES=1 \
         AGENT_SUPERVISOR_STATE_DIR="$STATE_DIR3" bash "$QUOTA" check 2>&1)
RC3=$?
elapsed3=$(( $(date +%s) - start ))
want_exit "a codexbar that ignores --timeout entirely is still refused (rc=2)" "$RC3" 2 "$OUT3"
want_contains "...as TIMEOUT via the outer bound" "TIMEOUT" "$OUT3"
if [ "$elapsed3" -le 10 ]; then
  ok "...killed within the outer bound (${elapsed3}s), not the stub's 60s sleep"
else
  bad "...killed within the outer bound" "took ${elapsed3}s"
fi

rm -rf "$D"

echo
echo "quota guard inner timeout: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
