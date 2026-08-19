#!/bin/bash
# agent-supervisor#346: director-loop.sh must not trust a hardcoded tmux @id
# for its Director target across a server restart. test_heartbeat_target.sh
# and test_quota_watch_target.sh already prove the sibling fixes against a
# STUB tmux; director-loop.sh -- the ORIGINAL instance named in #346's own
# table, measured as nearly two hours of silent non-ticking -- had no test at
# all, stub or real. This one runs against a REAL, isolated tmux server
# (TMUX_TMPDIR + assert_isolated_tmux, never the live estate, per the
# standing rule) and simulates an actual restart: the Director's window is
# pushed to a high @id, the server is killed and a fresh one started, and the
# window that comes back gets a LOW @id -- the exact renumbering shape the
# issue's incident measured (director:@3 -> @35 there; here the numbers are
# reversed but the defect is identical: the configured @id no longer exists).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOOP="$HERE/../../scripts/supervisor/director-loop.sh"
# shellcheck source=../../scripts/supervisor/tmux-isolation.sh
source "$HERE/../../scripts/supervisor/tmux-isolation.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }

echo "director-loop.sh: Director target resolution survives a REAL tmux server restart (#346)"

if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP: tmux not installed -- the real-restart proof is UNVERIFIED"
  echo "0 passed, 0 failed"
  exit 0
fi

D=$(mktemp -d)

# --- a healthy, registered live/ so the LIVE MISSING gate is a no-op -------
git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/src" >/dev/null 2>&1
SRC="$D/src"
git -C "$SRC" config user.email test@example.com
git -C "$SRC" config user.name "Test"
git -C "$SRC" checkout -q -b main
mkdir -p "$SRC/scripts/supervisor"
: > "$SRC/README.md"
git -C "$SRC" add -A
git -C "$SRC" commit -q -m base
git -C "$SRC" push -q -u origin main
LIVE="$D/live"
git -C "$SRC" worktree add -q --detach "$LIVE" origin/main

QUOTA_SAFE="$HERE/stubs/quota-safe"

# --- real, isolated tmux server ---------------------------------------------
RT="$D/tmux-sock"
mkdir -p "$RT"
rtmux() { env -u TMUX TMUX_TMPDIR="$RT" tmux -f /dev/null "$@"; }
cleanup_rt() { unset TMUX; export TMUX_TMPDIR="$RT"; assert_isolated_tmux && tmux kill-server 2>/dev/null; }
trap 'cleanup_rt; rm -rf "$D"' EXIT

if ! rtmux new-session -d -s director -n placeholder-0 -x 80 -y 24 2>/dev/null; then
  bad "real tmux: started a throwaway isolated server" "tmux new-session failed"
  echo "$pass passed, $fail failed"
  exit 1
fi
ok "real tmux: started a throwaway isolated server ($(rtmux -V)) under \$TMUX_TMPDIR"

# Push the window id counter up so the Director's own window lands on a
# non-trivial id, the same way years of ordinary window churn produced @35
# in the real incident -- @0 vs @1 would not distinguish "resolved correctly"
# from "happened to still match". Kill the original window and churn a few
# more before creating the real Director window, so its id is not the
# server's first-assigned one.
for i in 1 2 3 4; do rtmux new-window -t director -n "churn-$i" >/dev/null; done
rtmux kill-window -t director:placeholder-0 >/dev/null 2>&1
rtmux new-window -t director -n the-director-pane >/dev/null
OLD_ID=$(rtmux list-windows -t director -F '#{window_name} #{window_id}' | awk '$1=="the-director-pane"{print $2}')
for i in 1 2 3 4; do rtmux kill-window -t "director:churn-$i" >/dev/null 2>&1; done
if [ -z "$OLD_ID" ]; then
  bad "captured the Director window's pre-restart id" "list-windows produced nothing"
else
  ok "Director window is $OLD_ID before the restart"
fi

# --- simulate the restart: kill the server, start a fresh one --------------
if (unset TMUX; export TMUX_TMPDIR="$RT"; assert_isolated_tmux); then
  ok "assert_isolated_tmux gate passes for the isolated socket"
else
  bad "assert_isolated_tmux gate" "refused on an isolated TMUX_TMPDIR"
fi
rtmux kill-server 2>/dev/null
# The restarted window runs `cat`, not a shell: a shell would try to EXECUTE
# the multi-line tick text as commands (it is not an interactive agent), which
# both pollutes the pane with command-not-found noise and is not what this
# test is trying to prove. `cat` just echoes stdin back, so a capture-pane
# afterward is a clean, direct proof that the text arrived at the SURVIVING
# window and nowhere else.
rtmux new-session -d -s director -n the-director-pane -x 80 -y 24 \
  bash -c 'echo READY; exec cat' >/dev/null 2>&1
sleep 1
NEW_ID=$(rtmux list-windows -t director -F '#{window_name} #{window_id}' | awk '$1=="the-director-pane"{print $2}')
ok "restart simulated: fresh server, Director window is now $NEW_ID"
if [ "$OLD_ID" = "$NEW_ID" ]; then
  bad "the restart actually renumbered the window (test precondition)" "old=$OLD_ID new=$NEW_ID are the same -- this run proves nothing"
fi

run_loop() { # run_loop <script> <target>
  env -u TMUX TMUX_TMPDIR="$RT" \
    SUPERVISOR_STATE="$D/state" SUPERVISOR_LIVE="$LIVE" SUPERVISOR_REPO="$SRC" \
    QUOTA_GATE="$QUOTA_SAFE" DIRECTOR_LOOP_TARGET="director:$OLD_ID" \
    bash "$1"
}

# --- RED: a copy of director-loop.sh with the resolution fallback removed --
# proves this test actually fails without the fix, not just that it passes
# with whatever the file currently says.
BROKEN="$D/director-loop-broken.sh"
awk '
  /^# RESOLVE THE WINDOW, do not trust the id in the default\.$/ { skip=1 }
  skip && /^fi$/ { skip=0; next }
  !skip { print }
' "$LOOP" > "$BROKEN"
chmod +x "$BROKEN"
if grep -q "RESOLVE THE WINDOW" "$BROKEN"; then
  bad "constructed a pre-fix (broken) copy of director-loop.sh" "resolution block still present -- awk strip failed"
fi

rm -rf "$D/state"; mkdir -p "$D/state"
out=$(run_loop "$BROKEN" "director:$OLD_ID" 2>&1); rc=$?
want_exit "RED: pre-fix script cannot capture the stale @id after a restart and refuses to tick" "$rc" 3 "$out"
if grep -qi "could not capture" <<<"$out"; then ok "RED: fails for the right reason (could not capture, no resolution attempted)"; else bad "RED: fails for the right reason" "$out"; fi

# --- GREEN: the real, fixed script resolves past the stale @id -------------
# The surviving window runs `cat`, not a real agent, so it can never show
# "esc to interrupt" -- director-loop.sh's own last check ("ticked but did
# NOT start working") is correctly exit 1 here. What this proves is the part
# #346 is actually about: resolution happened and the text was delivered to
# the RIGHT (renumbered) window, not that a `cat` process looks busy.
rm -rf "$D/state"; mkdir -p "$D/state"
out=$(run_loop "$LOOP" "director:$OLD_ID" 2>&1); rc=$?
want_exit "GREEN: fixed script resolves the stale @id, ticks, and correctly reports the fixture didn't start a turn (exit 1)" "$rc" 1 "$out"
if grep -q "resolved to director:$NEW_ID" <<<"$out"; then
  ok "GREEN: logs the resolution, naming the surviving window id"
else
  bad "GREEN: logs the resolution, naming the surviving window id" "$out"
fi
if grep -q "is gone" <<<"$out" && [ "$(grep -c "is gone" <<<"$out")" -gt 1 ]; then
  bad "GREEN: resolves once and does not keep re-detecting the stale id" "$out"
else
  ok "GREEN: resolves once and does not keep re-detecting the stale id"
fi
pane=$(rtmux capture-pane -p -t "=director:$NEW_ID" -S -100 2>/dev/null)
if grep -q "Director tick" <<<"$pane"; then
  ok "GREEN: the tick text actually landed in the surviving (renumbered) window, not a stale target"
else
  bad "GREEN: the tick text actually landed in the surviving window" "$pane"
fi

# --- ambiguous restart: more than one window, refuse rather than guess -----
rtmux new-window -t director -n second-window >/dev/null
rm -rf "$D/state"; mkdir -p "$D/state"
out=$(run_loop "$LOOP" "director:$OLD_ID" 2>&1); rc=$?
want_exit "ambiguous restart (2 windows): refuses rather than guessing which is the Director" "$rc" 3 "$out"
if grep -qi "refusing to guess" <<<"$out"; then ok "names the refusal reason"; else bad "names the refusal reason" "$out"; fi

echo
echo "director-loop target restart: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
