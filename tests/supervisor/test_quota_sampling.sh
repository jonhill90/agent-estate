#!/bin/bash
# agent-supervisor#262: the gate that governs every tick is intermittently
# unreadable. `codexbar` fetch failures are transient -- the SAME machine,
# SAME minute, SAME copy of quota.sh returned UNAVAILABLE and then SAFE
# seconds apart. A single sample cannot distinguish a blip from a real
# reading, and quota.sh must decide by rule over a small bounded number of
# samples, never by whichever answer arrived last and never by retrying
# until it gets a clean one (that silently turns fail-closed into fail-open
# -- the mechanism behind the $80 -> $8 burn).
#
# This is the guard going red before the fix (a bare last-sample read) and
# green after (sample N times, decide by rule): break it and watch it fail.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
QUOTA="$HERE/../../scripts/supervisor/quota.sh"
STUB="$HERE/stubs/codexbar-sequence"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_not_contains() { if grep -qF -- "$2" <<<"$3"; then bad "$1" "did not want '$2' in: $3"; else ok "$1"; fi }

echo "quota.sh -- bounded sampling over a flaky gate (#262)"

D=$(mktemp -d)

run() {
  local seq="$1"
  local counter="$D/counter-$$-$RANDOM"
  local state_dir="$D/state-$$-$RANDOM"
  # Each call gets its own AGENT_SUPERVISOR_STATE_DIR: `check` now writes a
  # last-good guard reading to the cache on every SAFE/WIND DOWN sample
  # (#264 composed with #262), and without this a run here would read or
  # write the real machine's quota-cache dir -- polluting production state
  # and leaking a stale reading across unrelated cases in this same file.
  QUOTA_TEST_SEQUENCE="$seq" QUOTA_TEST_COUNTER="$counter" \
    CODEXBAR_BIN="$STUB" QUOTA_CHECK_SAMPLES=3 QUOTA_CHECK_DELAY=0 \
    AGENT_SUPERVISOR_STATE_DIR="$state_dir" \
    bash "$QUOTA" check 2>&1
}

# --- all samples fail -> UNKNOWN, and dispatch must not proceed -----------
OUT=$(run "empty,empty,empty"); RC=$?
want_exit "all-fail: exits 2 (UNAVAILABLE)" "$RC" 2 "$OUT"
want_contains "all-fail: says UNAVAILABLE" "UNAVAILABLE" "$OUT"
want_contains "all-fail: never claims safe" "treat as unknown, never as safe" "$OUT"
want_not_contains "all-fail: does not say SAFE" "quota: SAFE" "$OUT"

# --- one WIND DOWN plus two SAFE -> the pessimistic branch wins -----------
OUT=$(run "winddown,safe,safe"); RC=$?
want_exit "1 winddown + 2 safe: exits 1 (WIND DOWN)" "$RC" 1 "$OUT"
want_contains "...says WIND DOWN" "WIND DOWN" "$OUT"

OUT=$(run "safe,safe,winddown"); RC=$?
want_exit "winddown last still wins (not last-sample-wins)" "$RC" 1 "$OUT"

# --- one SAFE among two failures -> proceed, and say it was 1-of-3 --------
OUT=$(run "empty,safe,empty"); RC=$?
want_exit "1 safe + 2 fetch-failures: exits 0 (proceed)" "$RC" 0 "$OUT"
want_contains "...shows it was 1-of-3" "sample 2/3" "$OUT"
want_contains "...says SAFE" "SAFE" "$OUT"

# --- the two failure shapes are distinguished, not merged ------------------
OUT=$(run "empty,unavailable,empty"); RC=$?
want_exit "mixed failure shapes: still UNAVAILABLE" "$RC" 2 "$OUT"
want_contains "...names the unreachable count" "could not reach" "$OUT"
want_contains "...names the source-unknown count" "reached it and got told unknown" "$OUT"

# --- every sample is logged with its exit code -----------------------------
OUT=$(run "empty,safe,empty")
want_contains "logs sample 1 with its exit code" "sample 1/3" "$OUT"
want_contains "logs sample 2 with its exit code" "sample 2/3" "$OUT"
want_contains "logs sample 3 with its exit code" "sample 3/3" "$OUT"

# --- bounded: exactly SAMPLES calls, never an unbounded retry loop --------
COUNTER="$D/bound-counter"
QUOTA_TEST_SEQUENCE="empty,empty,empty,safe,safe,safe" QUOTA_TEST_COUNTER="$COUNTER" \
  CODEXBAR_BIN="$STUB" QUOTA_CHECK_SAMPLES=3 QUOTA_CHECK_DELAY=0 \
  AGENT_SUPERVISOR_STATE_DIR="$D/state-bound" \
  bash "$QUOTA" check >/dev/null 2>&1
CALLS=$(cat "$COUNTER" 2>/dev/null || echo "?")
if [ "$CALLS" = "3" ]; then
  ok "no unbounded retry: exactly 3 calls made for QUOTA_CHECK_SAMPLES=3, even though a later sample would have been SAFE"
else
  bad "no unbounded retry" "expected exactly 3 calls, stub was invoked $CALLS times"
fi

echo
echo "quota sampling: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
