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

# --- case 5: agent-estate#789's own reproduction -- the SESSION named by
#             the stale configured target has zero matching windows, but a
#             DIFFERENT live session does. The guess path must search every
#             live session, not only the one parsed from the stale target,
#             or this is exactly #789's own "refuses and cannot find the
#             live supervisor panes that do exist in agent-estate and
#             estate" defect. -----------------------------------------------
STATE=$(mktemp -d "$D/state.XXXXXX"); LOG="$D/log.5"; : > "$LOG"
PATH="$D:$PATH" QUOTA_GATE="$D/gate-winddown" TMUX_LOG="$LOG" SUPERVISOR_STATE="$STATE" \
  TMUX_SESSIONS="agent-supervisor estate" \
  TMUX_WINDOWS_NAMED_agent_supervisor='@11\tas311-fix\n@23\tfree-4' \
  TMUX_WINDOWS_NAMED_estate='@2\tdirector\n@7\tsupervisor' \
  QUOTA_WATCH_TARGET="agent-supervisor:@1" \
  bash "$WATCH" --once >>"$STATE/quota-watch.out" 2>&1
want_count "#789: configured session has no match, but a DIFFERENT live session does -> found and sent there" \
  "send-keys -t =estate:@7" "$LOG" 3
grep -q "resolved to estate:@7" "$STATE/quota-watch.out" \
  && ok "#789: logs the cross-session resolution" \
  || bad "#789: logs the cross-session resolution" "$(cat "$STATE/quota-watch.out")"

# --- case 6: MUTATION CHECK -- with the guess path narrowed back to only
#             the session parsed from the (stale) configured target, case
#             5's own fixture must reproduce #789's failure: refuses,
#             delivers nothing, even though estate:@7 is live and named
#             right. A guard that only ever exercises the fixed behaviour
#             would pass unchanged if this widening were reverted. ---------
MUTANT_DIR=$(mktemp -d "$D/mutant.XXXXXX")
cp -R "$HERE/../../scripts/supervisor/." "$MUTANT_DIR/"
rm -rf "$MUTANT_DIR/__pycache__"
chmod +x "$MUTANT_DIR"/*.sh
MUTATED="$MUTANT_DIR/quota-watch.sh"
patch_rc=0
python3 - "$MUTATED" <<'PY' || patch_rc=$?
import sys
target = sys.argv[1]
text = open(target).read()
# Narrow the "every live session" enumeration back down to only the session
# parsed from the (possibly stale) configured target -- reproducing #789's
# own bug with one targeted line change, rather than reverting the whole
# multi-session block (which makes the mutation fragile to unrelated
# rewording nearby).
marker = "  live_sessions=$(tmux list-sessions -F '#{session_name}' 2>/dev/null)\n"
assert text.count(marker) == 1, "list-sessions enumeration line not found or not unique -- script shape changed"
reverted = '  live_sessions="${TARGET%%:*}"  # MUTATED (agent-estate#789 test): back to one session\n'
text = text.replace(marker, reverted, 1)
open(target, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "#789 mutation: patched a mutant copy of quota-watch.sh, guess scoped back to one session" \
    "could not patch $MUTATED (exit $patch_rc)"
else
  ok "#789 mutation: patched a mutant copy of quota-watch.sh, guess scoped back to one session"
  bash -n "$MUTATED" || bad "mutant quota-watch.sh is still valid bash" "syntax error"

  STATE6=$(mktemp -d "$D/state6.XXXXXX"); LOG6="$D/log.6"; : > "$LOG6"
  PATH="$D:$PATH" QUOTA_GATE="$D/gate-winddown" TMUX_LOG="$LOG6" SUPERVISOR_STATE="$STATE6" \
    TMUX_SESSIONS="agent-supervisor estate" \
    TMUX_WINDOWS_NAMED_agent_supervisor='@11\tas311-fix\n@23\tfree-4' \
    TMUX_WINDOWS_NAMED_estate='@2\tdirector\n@7\tsupervisor' \
    QUOTA_WATCH_TARGET="agent-supervisor:@1" \
    bash "$MUTATED" --once >>"$STATE6/quota-watch.out" 2>&1

  if ! grep -q '^send-keys' "$LOG6"; then
    ok "#789 mutation confirmed: session-scoped guess reproduces the incident -- fails loudly, sends nothing"
  else
    bad "#789 mutation confirmed: session-scoped guess reproduces the incident -- fails loudly, sends nothing" \
      "$(cat "$LOG6")"
  fi
  grep -q "refusing to guess" "$STATE6/quota-watch.out" \
    && ok "#789 mutation confirmed: refusal is logged, never a silent success" \
    || bad "#789 mutation confirmed: refusal is logged, never a silent success" "$(cat "$STATE6/quota-watch.out")"
fi

echo
echo "quota-watch target: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
