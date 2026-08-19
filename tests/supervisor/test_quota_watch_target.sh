#!/bin/bash
# quota-watch.sh must not trust a hardcoded tmux @id for its send target
# (agent-supervisor#346). Measured 2026-08-18: the estate's tmux was
# restarted and quota-watch's default (agent-supervisor:@1) went stale
# identically to director-loop.sh's director:@3 -- same root cause, named in
# the issue as one of the three known instances.
#
# UNLIKE heartbeat.sh's `director` session, `agent-supervisor` legitimately
# holds many windows (one per lane), so "fall back when there is exactly one
# window" is the wrong rule here -- it would frequently find several and
# refuse even in the ordinary case. The durable key for this target is the
# window NAME: bootstrap-session.sh already names the supervisor's own
# window `supervisor` (LANES_SUPERVISOR_NAME), and a respawned window can be
# given that name back even though it cannot be given its old @id back.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCH="$HERE/../../scripts/supervisor/quota-watch.sh"
pass=0; fail=0

ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_count() {
  local label="$1" needle="$2" file="$3" expect="$4"
  local got; got=$(grep -cF -- "$needle" "$file" 2>/dev/null || true)
  if [ "$got" = "$expect" ]; then ok "$label"; else bad "$label" "expected $expect of '$needle' in $file, got $got:
$(cat "$file" 2>/dev/null)"; fi
}

echo "quota-watch.sh -- target resolution survives a tmux server restart (#346)"

D=$(mktemp -d)
cp "$HERE/stubs/tmux-quota-watch-target" "$D/tmux"; chmod +x "$D/tmux"

cat > "$D/gate-winddown" <<'EOF'
#!/bin/bash
exit 1
EOF
chmod +x "$D/gate-winddown"

tick() {
  # $1=state dir $2=log file $3=sessions $4=good-target $5=windows(id<TAB>name, \n-joined)
  PATH="$D:$PATH" QUOTA_GATE="$D/gate-winddown" TMUX_LOG="$2" SUPERVISOR_STATE="$1" \
    TMUX_SESSIONS="$3" TMUX_GOOD_TARGET="$4" TMUX_WINDOWS_NAMED="$5" \
    QUOTA_WATCH_TARGET="agent-supervisor:@1" \
    bash "$WATCH" --once >>"$1/quota-watch.out" 2>&1
}

# --- case 1: @1 is gone; several lane windows exist, but exactly one is
#             named `supervisor` -- resolve by NAME and send there ---------
STATE=$(mktemp -d "$D/state.XXXXXX"); LOG="$D/log.1"; : > "$LOG"
WINS='@11\tas311-fix\n@23\tfree-4\n@40\tsupervisor'
tick "$STATE" "$LOG" "agent-supervisor" "agent-supervisor:@40" "$WINS"
want_count "@1 is gone, one window is named 'supervisor' (now @40) -> resolves and sends there" \
  "send-keys -t =agent-supervisor:@40" "$LOG" 3
grep -q "resolved to agent-supervisor:@40" "$STATE/quota-watch.out" \
  && ok "logs the resolution by name" \
  || bad "logs the resolution by name" "$(cat "$STATE/quota-watch.out")"

# --- case 2: @1 is gone and NO window is named `supervisor` -- refuse,
#             never fall back to guessing a lane window -------------------
STATE=$(mktemp -d "$D/state.XXXXXX"); LOG="$D/log.2"; : > "$LOG"
WINS='@11\tas311-fix\n@23\tfree-4'
tick "$STATE" "$LOG" "agent-supervisor" "" "$WINS"
if ! grep -q '^send-keys' "$LOG"; then
  ok "no window named 'supervisor' -> refuses, sends nothing to any lane window"
else
  bad "no window named 'supervisor' -> refuses, sends nothing to any lane window" "$(cat "$LOG")"
fi
grep -q "refusing" "$STATE/quota-watch.out" \
  && ok "says why it refused" \
  || bad "says why it refused" "$(cat "$STATE/quota-watch.out")"

# --- case 3: @1 is gone and MORE THAN ONE window is named `supervisor`
#             (a rename race, or a stale second one) -- still refuse -------
STATE=$(mktemp -d "$D/state.XXXXXX"); LOG="$D/log.3"; : > "$LOG"
WINS='@11\tsupervisor\n@23\tsupervisor'
tick "$STATE" "$LOG" "agent-supervisor" "" "$WINS"
if ! grep -q '^send-keys' "$LOG"; then
  ok "two windows named 'supervisor' -> refuses rather than guessing which"
else
  bad "two windows named 'supervisor' -> refuses rather than guessing which" "$(cat "$LOG")"
fi

# --- case 4: the configured target still exists -- no resolution needed,
#             ordinary behaviour is unchanged ------------------------------
STATE=$(mktemp -d "$D/state.XXXXXX"); LOG="$D/log.4"; : > "$LOG"
WINS='@1\tsupervisor'
tick "$STATE" "$LOG" "agent-supervisor" "agent-supervisor:@1" "$WINS"
want_count "the configured target still exists -> sent directly, no resolution needed" \
  "send-keys -t =agent-supervisor:@1" "$LOG" 3

echo
echo "quota-watch target: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
