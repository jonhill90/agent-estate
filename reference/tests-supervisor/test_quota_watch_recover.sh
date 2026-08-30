#!/bin/bash
# quota-watch-recover.sh -- restart on a stale heartbeat, not on pid absence
# (agent-supervisor#276).
#
# WHY THIS EXISTS. quota-watch.sh hung for three hours with nothing to bring
# it back -- there was no restart mechanism for it at all, PID-based or
# otherwise. This drives quota-watch-recover.sh directly against a fake
# "quota-watch.sh" (a sleep loop carrying that name in its argv, launched
# from a throwaway $LIVE) and asserts: a fresh heartbeat is left alone even
# though the fake process never does any real work; a stale or missing
# heartbeat gets the stale process killed and a fresh one launched; and the
# restart decision is driven by the STAMP, never by whether a pid merely
# exists.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RECOVER="$HERE/../../scripts/supervisor/quota-watch-recover.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }

echo "quota-watch-recover.sh -- restart on stale heartbeat (#276)"

D=$(mktemp -d)
LIVE="$D/live"
mkdir -p "$LIVE/scripts/supervisor"

# A fake quota-watch.sh: a plain sleep loop that never writes a heartbeat of
# its own -- this suite writes the stamp by hand to control freshness
# independently of whatever the fake process is actually doing, the same
# separation the real fix makes (the stamp proves progress; the pid proves
# nothing).
cat > "$LIVE/scripts/supervisor/quota-watch.sh" <<'FIX'
#!/bin/bash
trap 'exit 0' TERM
while :; do sleep 1; done
FIX
chmod +x "$LIVE/scripts/supervisor/quota-watch.sh"

stamp_ago() { # stamp_ago <seconds-ago> <state>
  local ts
  ts=$(date -u -v-"$1"S '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d "$1 seconds ago" '+%Y-%m-%dT%H:%M:%SZ')
  printf 'checked: %s\nstate: %s\n' "$ts" "${2:-SAFE}"
}

run() { # run <state-dir>
  SUPERVISOR_STATE="$1" SUPERVISOR_LIVE="$LIVE" \
  QUOTA_WATCH_STATUS_PATH="$1/.quota-watch.state" \
  QUOTA_WATCH_STALE_AFTER=100 \
  QUOTA_WATCH_RECOVER_LOCK="$1/.lock" \
  QUOTA_WATCH_RECOVER_LOG="$1/recover.log" \
    bash "$RECOVER" 2>&1
}

# --- 1. no stamp at all: nothing was ever running -- launch one -----------
D1="$D/s1"; mkdir -p "$D1"
OUT=$(run "$D1")
RC=$?
want_exit "no heartbeat at all: still exits 0" "$RC" 0 "$OUT"
want_contains "...and launches quota-watch.sh" "RESTARTED" "$OUT"
sleep 1
if pgrep -f "$LIVE/scripts/supervisor/quota-watch.sh" >/dev/null 2>&1; then
  ok "a live quota-watch.sh process now exists"
else
  bad "a live quota-watch.sh process now exists" "pgrep found nothing"
fi
pkill -TERM -f "$LIVE/scripts/supervisor/quota-watch.sh" 2>/dev/null; sleep 1

# --- 2. fresh heartbeat, no live process either: still nothing to force --
# (a fresh stamp with no process contradicts itself in production, but this
# suite is pinning the DECISION, not simulating every real combination: a
# fresh heartbeat alone must never trigger a restart.)
D2="$D/s2"; mkdir -p "$D2"
stamp_ago 5 SAFE > "$D2/.quota-watch.state"
OUT=$(run "$D2")
want_contains "a fresh heartbeat: reported OK, not restarted" "OK" "$OUT"
if grep -q RESTARTED <<<"$OUT"; then
  bad "a fresh heartbeat never triggers a restart" "$OUT"
else
  ok "a fresh heartbeat never triggers a restart"
fi

# --- 3. THE LOAD-BEARING CASE: a live pid with a STALE heartbeat is killed
# and replaced -- this is the exact shape that went undetected for three
# hours: `pgrep` alone would have called this healthy. -----------------
D3="$D/s3"; mkdir -p "$D3"
( cd "$LIVE" && exec "$LIVE/scripts/supervisor/quota-watch.sh" ) &
hung_pid=$!
sleep 1
stamp_ago 9999 SAFE > "$D3/.quota-watch.state"
OUT=$(run "$D3")
RC=$?
want_exit "a live pid with a stale heartbeat: recovery still exits 0" "$RC" 0 "$OUT"
want_contains "...and restarts despite the pid being alive" "RESTARTED" "$OUT"
want_contains "...naming the stale heartbeat as the reason, not pid absence" "heartbeat" "$OUT"
sleep 1
if kill -0 "$hung_pid" 2>/dev/null; then
  bad "the stale pid was terminated" "pid $hung_pid is still alive"
else
  ok "the stale pid was terminated"
fi
pkill -TERM -f "$LIVE/scripts/supervisor/quota-watch.sh" 2>/dev/null; sleep 1

# --- 4. missing/unreadable heartbeat: treated the same as stale, not as
# "never started" once a live process already exists (belt-and-suspenders
# against a corrupted stamp masking a real hang). -------------------------
D4="$D/s4"; mkdir -p "$D4"
printf 'garbage, not a heartbeat\n' > "$D4/.quota-watch.state"
OUT=$(run "$D4")
want_contains "an unreadable heartbeat is treated as stale, not fresh" "RESTARTED" "$OUT"

rm -rf "$D"
pkill -TERM -f "$LIVE/scripts/supervisor/quota-watch.sh" 2>/dev/null || true

echo
echo "quota-watch-recover.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
