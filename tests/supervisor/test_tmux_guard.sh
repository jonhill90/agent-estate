#!/bin/bash
# agent-supervisor#166: the tmux server has gone down four times because a
# lane's agent typed a bare `tmux kill-server` into its own shell -- past
# `assert_isolated_tmux` (tmux-isolation.sh), which only guards SCRIPTS in
# this repo that call it, never a command an agent decides to type. This
# proves the tmux-guard.sh wrapper actually refuses that shape, and that a
# genuinely isolated or explicitly targeted call still works -- everything
# here runs on a PRIVATE tmux socket (`-L $SOCKET`); the live default socket
# is never touched.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; shift; [ $# -gt 0 ] && sed 's/^/       /' <<<"$*"; fail=$((fail+1)); }

REAL_TMUX=$(command -v tmux) || { echo "  SKIP no tmux on PATH"; exit 0; }
SOCKET="as166-test-$$"
D=$(mktemp -d)
cleanup() { "$REAL_TMUX" -L "$SOCKET" kill-server 2>/dev/null; rm -rf "$D"; }
trap cleanup EXIT

export AGENT_SUPERVISOR_STATE_DIR="$D/state"
# shellcheck source=../../scripts/supervisor/tmux-guard.sh
. "$SUP/tmux-guard.sh"

GUARD_BIN=$(install_tmux_guard "$REAL_TMUX") || { bad "install_tmux_guard" "failed"; exit 1; }
[ -x "$GUARD_BIN/tmux" ] && ok "install_tmux_guard writes an executable wrapper" \
  || bad "install_tmux_guard writes an executable wrapper" "not found/executable: $GUARD_BIN/tmux"

# Idempotence: a second install against the same real binary must not error
# and must still point at it (bootstrap-session.sh/restore.sh call this on
# every dispatch, not just once).
GUARD_BIN2=$(install_tmux_guard "$REAL_TMUX") || { bad "install_tmux_guard is idempotent" "second call failed"; }
[ "$GUARD_BIN2" = "$GUARD_BIN" ] && grep -qF "REAL_TMUX=\"$REAL_TMUX\"" "$GUARD_BIN/tmux" \
  && ok "install_tmux_guard is idempotent" \
  || bad "install_tmux_guard is idempotent" "dir or real-tmux path changed on re-install"

export PATH="$GUARD_BIN:$PATH"

# One isolated session other tests below can try to destroy.
"$REAL_TMUX" -L "$SOCKET" new-session -d -s smoke
"$REAL_TMUX" -L "$SOCKET" new-window -d -t smoke -n free-2

alive() { "$REAL_TMUX" -L "$SOCKET" has-session -t smoke 2>/dev/null; }

# --- the actual reproduction: a bare kill-server, exactly as typed by the
# lane in every one of the four measured outages. $TMUX is set here (this
# suite runs inside a real tmux pane in CI/dev), matching a lane's shell.
if alive; then
  out=$(tmux kill-server 2>&1); rc=$?
  if [ "$rc" -ne 0 ] && alive; then
    ok "bare 'tmux kill-server' is refused, target session survives"
  else
    bad "bare 'tmux kill-server' is refused, target session survives" "rc=$rc alive=$(alive; echo $?)" "$out"
  fi
  echo "$out" | grep -q "agent-supervisor#166" && ok "refusal message names the issue" \
    || bad "refusal message names the issue" "$out"
else
  bad "bare 'tmux kill-server' is refused, target session survives" "setup: smoke session did not start"
fi

# --- untargeted kill-session / kill-window: refused the same way, for the
# same reason -- these default to the CURRENT session/window, which for a
# lane's shell is the live one.
out=$(env TMUX="/some/live/socket,1,0" tmux kill-session 2>&1); rc=$?
[ "$rc" -ne 0 ] && ok "bare 'tmux kill-session' (no -t) is refused" \
  || bad "bare 'tmux kill-session' (no -t) is refused" "rc=$rc" "$out"

out=$(env TMUX="/some/live/socket,1,0" tmux kill-window 2>&1); rc=$?
[ "$rc" -ne 0 ] && ok "bare 'tmux kill-window' (no -t) is refused" \
  || bad "bare 'tmux kill-window' (no -t) is refused" "rc=$rc" "$out"

# --- explicitly targeted kill-session/kill-window is exactly what every
# production caller in this repo already does (transport.py's kill_session)
# and must keep working -- refusing it would break real supervisor code, not
# just an agent's improvisation.
if alive; then
  tmux -L "$SOCKET" kill-session -t smoke >/dev/null 2>&1
  alive && bad "targeted 'kill-session -t' still works" "session survived" \
    || ok "targeted 'kill-session -t' still works"
else
  bad "targeted 'kill-session -t' still works" "setup: smoke session not alive before this case"
fi

# --- genuinely isolated (explicit -L naming a socket other than the one
# this shell is attached to -- technique B, tmux-isolation.sh's own second
# accepted form) destructive calls must still work: this is the shape every
# isolated test in this suite already uses (test_restore.sh's own wrapper),
# and a guard that blocked it would just be a second, worse
# assert_isolated_tmux.
"$REAL_TMUX" -L "$SOCKET" new-session -d -s smoke2
out=$(tmux -L "$SOCKET" kill-server 2>&1); rc=$?
"$REAL_TMUX" -L "$SOCKET" has-session -t smoke2 2>/dev/null
still_alive=$?
[ "$rc" -eq 0 ] && [ "$still_alive" -ne 0 ] && ok "isolated 'tmux -L ... kill-server' still works" \
  || bad "isolated 'tmux -L ... kill-server' still works" "rc=$rc still_alive=$still_alive" "$out"

# --- technique A (TMUX unset, TMUX_TMPDIR set to an existing dir) is
# accepted too, on its own, with no -L at all -- the exact "env -u TMUX
# TMUX_TMPDIR=... tmux kill-server" shape issue #166 names as correct
# containment. Proven by absence of our refusal message; a real server may
# or may not exist under that TMUX_TMPDIR, which is irrelevant here -- only
# whether the WRAPPER let it reach real tmux at all is under test.
out=$(env -u TMUX TMUX_TMPDIR="$D" tmux kill-server 2>&1); rc=$?
echo "$out" | grep -q "agent-supervisor#166" \
  && bad "technique-A-only (TMUX_TMPDIR, no -L) is accepted" "wrapper refused: $out" \
  || ok "technique-A-only (TMUX_TMPDIR, no -L) is accepted"

# --- a non-destructive verb is untouched: pure pass-through, no refusal
# logic, no behaviour change for the other 99% of tmux calls a lane runs.
"$REAL_TMUX" -L "$SOCKET" new-session -d -s smoke3
out=$(tmux -L "$SOCKET" list-sessions 2>&1); rc=$?
[ "$rc" -eq 0 ] && ok "non-destructive verbs pass through unchanged" \
  || bad "non-destructive verbs pass through unchanged" "rc=$rc" "$out"

echo
echo "test_tmux_guard: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
