#!/bin/bash
# heartbeat.sh must not trust a hardcoded tmux @id for its nudge target
# (agent-supervisor#346). tmux window ids are unique within a server but do
# NOT survive a server restart -- director-loop.sh hit this exact defect with
# the exact same default (director:@3) and #346 asks for the class to be
# fixed, not just that one instance. heartbeat.sh shares the identical
# pattern and was not in the issue's own table, which is exactly the kind of
# instance the sweep is supposed to catch.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HEARTBEAT="$HERE/../../scripts/supervisor/heartbeat.sh"
pass=0; fail=0

ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_count() {
  local label="$1" needle="$2" file="$3" expect="$4"
  local got; got=$(grep -cF -- "$needle" "$file" 2>/dev/null || true)
  if [ "$got" = "$expect" ]; then ok "$label"; else bad "$label" "expected $expect of '$needle' in $file, got $got:
$(cat "$file" 2>/dev/null)"; fi
}

echo "heartbeat.sh -- target resolution survives a tmux server restart (#346)"

D=$(mktemp -d)
cp "$HERE/stubs/tmux-heartbeat-target" "$D/tmux"; chmod +x "$D/tmux"

cat > "$D/gate-safe" <<'EOF'
#!/bin/bash
exit 0
EOF
chmod +x "$D/gate-safe"

run_heartbeat() {
  # $1=state dir $2=log file $3=sessions $4=good-target $5=windows
  PATH="$D:$PATH" TMUX_LOG="$2" TMUX_SESSIONS="$3" TMUX_GOOD_TARGET="$4" TMUX_WINDOWS="$5" \
    QUOTA_GATE="$D/gate-safe" SUPERVISOR_STATE="$1" \
    HEARTBEAT_TARGET="director:@3" HEARTBEAT_STALE_AFTER=0 HEARTBEAT_NUDGE_COOLDOWN=0 \
    bash "$HEARTBEAT" --once >"$1/heartbeat.out" 2>&1
}

# --- case 1: the configured window is gone; the session has come back with
#             exactly ONE window (the restart shape director-loop.sh's own
#             fix was measured against) -- resolve and nudge it -------------
STATE=$(mktemp -d "$D/state.XXXXXX"); : > "$STATE/ledger.sqlite3"
LOG="$D/log.1"; : > "$LOG"
run_heartbeat "$STATE" "$LOG" "director" "director:@35" "@35"
want_count "configured @3 is gone, session has one window (@35) -> resolves and nudges it" \
  "send-keys -t =director:@35" "$LOG" 3
grep -q "resolved to director:@35" "$STATE/heartbeat.out" \
  && ok "logs the resolution, naming both the stale and the resolved target" \
  || bad "logs the resolution, naming both the stale and the resolved target" "$(cat "$STATE/heartbeat.out")"

# --- case 2: the configured window is gone and the session has come back
#             with MORE THAN ONE window -- refuse to guess which is the
#             Director, and prove nothing was sent to any of them ----------
STATE=$(mktemp -d "$D/state.XXXXXX"); : > "$STATE/ledger.sqlite3"
LOG="$D/log.2"; : > "$LOG"
run_heartbeat "$STATE" "$LOG" "director" "" "@12 @35"
if ! grep -q '^send-keys' "$LOG"; then
  ok "ambiguous restart (2 windows) sends nothing -- never guesses which is the Director"
else
  bad "ambiguous restart (2 windows) sends nothing -- never guesses which is the Director" "$(cat "$LOG")"
fi
grep -q "refusing to guess" "$STATE/heartbeat.out" \
  && ok "says why it refused" \
  || bad "says why it refused" "$(cat "$STATE/heartbeat.out")"

# --- case 3: the configured window is still there (no restart happened) --
#             sanity check that the resolution path is a no-op on the
#             common case, not a change in ordinary behaviour -------------
STATE=$(mktemp -d "$D/state.XXXXXX"); : > "$STATE/ledger.sqlite3"
LOG="$D/log.3"; : > "$LOG"
run_heartbeat "$STATE" "$LOG" "director" "director:@3" "@3"
want_count "the configured target still exists -> nudged directly, no resolution needed" \
  "send-keys -t =director:@3" "$LOG" 3

echo
echo "heartbeat target: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
