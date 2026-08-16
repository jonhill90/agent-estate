#!/bin/bash
# quota.sh -- timeout guards (agent-supervisor#264).
#
# WHY THIS EXISTS. `quota.sh` had ZERO timeout guards on any `codexbar`
# invocation, and it is step 0 of every loop tick in the estate. Measured
# 2026-08-16: `codexbar guard` took 14s, `codexbar usage` timed out at 20s,
# and an earlier unbounded call hung past 120s. A hang here stalls every
# loop simultaneously, and it fails in the worst direction -- an agent
# blocked on step 0 is indistinguishable from one quietly working.
#
# This is the test the issue asks for directly: stub a hanging `codexbar`
# and assert `quota.sh` returns 2 (UNAVAILABLE) within its configured
# timeout, not after the stub's full sleep. A stub that sleeps 60s must not
# make this file take 60s to run -- every case below sets a 1-2s timeout via
# QUOTA_GUARD_TIMEOUT_SECONDS / QUOTA_USAGE_TIMEOUT_SECONDS and asserts
# elapsed wall-clock stays well under the stub's sleep.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
QUOTA="$HERE/../../scripts/supervisor/quota.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }

echo "quota.sh -- timeout guards (#264)"

D=$(mktemp -d)

cat > "$D/codexbar-hang" <<'FIX'
#!/bin/bash
# Never returns on its own within any sane test window.
sleep 60
FIX
chmod +x "$D/codexbar-hang"

cat > "$D/codexbar-ok" <<'FIX'
#!/bin/bash
case "$1" in
  guard) echo '{"remainingPercent":52}'; exit 0 ;;
  usage) echo '[{"provider":"claude","usage":{"primary":{"usedPercent":48},"secondary":{"usedPercent":10}},"pace":{"primary":{"etaSeconds":3600,"summary":"ok"}}}]' ;;
esac
FIX
chmod +x "$D/codexbar-ok"

# --- the load-bearing case: a hanging guard returns 2, fast --------------
STATE_DIR="$D/state-1"
start=$(date +%s)
OUT=$(CODEXBAR_BIN="$D/codexbar-hang" QUOTA_GUARD_TIMEOUT_SECONDS=2 \
        AGENT_SUPERVISOR_STATE_DIR="$STATE_DIR" bash "$QUOTA" check 2>&1)
RC=$?
elapsed=$(( $(date +%s) - start ))
want_exit "a hanging 'guard' returns 2 (UNAVAILABLE), never 0" "$RC" 2 "$OUT"
want_contains "...and says TIMEOUT, not silence" "TIMEOUT" "$OUT"
if [ "$elapsed" -le 10 ]; then
  ok "...within its configured timeout, not the stub's full 60s sleep (${elapsed}s)"
else
  bad "...within its configured timeout" "took ${elapsed}s, stub sleeps 60s -- the guard is not bounding the call"
fi

# --- a hanging 'usage' (summary/eta) also returns 2, fast -----------------
start=$(date +%s)
OUT=$(CODEXBAR_BIN="$D/codexbar-hang" QUOTA_USAGE_TIMEOUT_SECONDS=2 \
        AGENT_SUPERVISOR_STATE_DIR="$D/state-2" bash "$QUOTA" summary 2>&1)
RC=$?
elapsed=$(( $(date +%s) - start ))
want_exit "a hanging 'usage' (summary) returns 2" "$RC" 2 "$OUT"
if [ "$elapsed" -le 10 ]; then
  ok "...within its configured timeout (${elapsed}s)"
else
  bad "...within its configured timeout" "took ${elapsed}s"
fi

start=$(date +%s)
OUT=$(CODEXBAR_BIN="$D/codexbar-hang" QUOTA_USAGE_TIMEOUT_SECONDS=2 \
        AGENT_SUPERVISOR_STATE_DIR="$D/state-3" bash "$QUOTA" eta 2>&1)
RC=$?
elapsed=$(( $(date +%s) - start ))
want_exit "a hanging 'usage' (eta) returns 2" "$RC" 2 "$OUT"
if [ "$elapsed" -le 10 ]; then
  ok "...within its configured timeout (${elapsed}s)"
else
  bad "...within its configured timeout" "took ${elapsed}s"
fi

# --- a timed-out guard degrades to a cached reading, but still exits 2 ----
# #264 item 4: stale-but-dated beats hung, but the gate's own exit code must
# never soften to 0 just because a cached number exists.
STATE_DIR="$D/state-4"
: "$(CODEXBAR_BIN="$D/codexbar-ok" AGENT_SUPERVISOR_STATE_DIR="$STATE_DIR" bash "$QUOTA" check)"
OUT=$(CODEXBAR_BIN="$D/codexbar-hang" QUOTA_GUARD_TIMEOUT_SECONDS=1 \
        AGENT_SUPERVISOR_STATE_DIR="$STATE_DIR" bash "$QUOTA" check 2>&1)
RC=$?
want_exit "a cached-but-stale reading still returns 2, never 0" "$RC" 2 "$OUT"
want_contains "...and shows the cached reading with its age" "last known" "$OUT"

# --- a cache older than the max age is not shown; UNKNOWN, not fiction ----
OUT=$(CODEXBAR_BIN="$D/codexbar-hang" QUOTA_GUARD_TIMEOUT_SECONDS=1 QUOTA_CACHE_MAX_AGE_SECONDS=0 \
        AGENT_SUPERVISOR_STATE_DIR="$STATE_DIR" bash "$QUOTA" check 2>&1)
RC=$?
want_exit "a cache older than the max age still returns 2" "$RC" 2 "$OUT"
if grep -qF "last known" <<<"$OUT"; then
  bad "...and does not show the aged-out cache as current" "$OUT"
else
  ok "...and does not show the aged-out cache as current"
fi

# --- exit 124 (the raw timeout convention) is never allowed to fall through
# to "not 1, therefore proceed" -- normalised to 2 explicitly.
grep -q '124)' "$QUOTA" && ok "quota.sh explicitly normalises a 124 exit to 2" \
  || bad "quota.sh explicitly normalises a 124 exit to 2" "no '124)' case found in $QUOTA"

echo
echo "quota.sh timeout guards: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
