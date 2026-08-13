#!/bin/bash
# poller-recover.sh must make the inbox-poll window self-correcting --
# agent-supervisor#10. A poller pane is launched with
# `exec scripts/supervisor/inbox-poll.sh`, which replaces the pane's shell
# rather than running under one; without `remain-on-exit`, ANY exit of that
# process -- clean, crashed, or SIGKILLed -- takes the whole WINDOW with it,
# and nothing is left for a restart mechanism to address.
#
# These tests drive REAL tmux, the same reasoning test_bootstrap_session.sh
# gives for doing so: the thing under test IS window/pane lifecycle, and a
# stub that pretended to model remain-on-exit and pane_dead would prove
# nothing about it.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RECOVER="$HERE/../../scripts/supervisor/poller-recover.sh"
source "$HERE/../../scripts/supervisor/tmux-isolation.sh"

S="poller-recover-test-$$"
RT="$(mktemp -d "${TMPDIR:-/tmp}/poller-recover-tmux.XXXXXX")"
unset TMUX
export TMUX_TMPDIR="$RT"
assert_isolated_tmux || exit 1
pass=0; fail=0

ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }

cleanup() { unset TMUX; export TMUX_TMPDIR="$RT"; tmux kill-session -t "$S" 2>/dev/null; }
cleanup_all() { cleanup; rm -rf "$RT" "${STATE:-}"; }
trap cleanup_all EXIT INT TERM

if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP no tmux on PATH"; exit 0
fi

echo "poller-recover.sh"

tmux new-session -d -s "$S" -x 200 -y 50

# The stand-in "poller": a script that behaves like inbox-poll.sh for the one
# property this suite needs -- it runs until told to stop, and it EXITS
# CLEANLY, exactly like inbox-poll.sh's own RESTART_FLAG / report_stop path,
# rather than being killed. A STOP file (not a signal) is the trigger, so the
# test controls exactly when the exit happens without racing a kill against
# a respawn.
STAND_IN="$RT/poller.sh"
cat > "$STAND_IN" <<'EOF'
#!/bin/bash
STOP="${POLLER_STOP_FILE:?}"
PIDFILE="${POLLER_PID_FILE:?}"
echo "$$" > "$PIDFILE"
while [ ! -f "$STOP" ]; do sleep 0.1; done
echo "poller process exiting"
exit 0
EOF
chmod +x "$STAND_IN"

STATE="$(mktemp -d "${TMPDIR:-/tmp}/poller-recover-state.XXXXXX")"
LAUNCH_CMD="POLLER_STOP_FILE='$STATE/stop' POLLER_PID_FILE='$STATE/pid' exec '$STAND_IN'"

recover() { POLLER_WINDOW=inbox-poll POLLER_LAUNCH_CMD="$LAUNCH_CMD" \
            POLLER_RECOVER_LOCK="$STATE/.lock" POLLER_RECOVER_LOG="$STATE/log" \
            SUPERVISOR_STATE="$STATE" bash "$RECOVER" "$S"; }

pane_dead() { tmux list-panes -t "$S:inbox-poll" -F '#{pane_dead}' 2>/dev/null | head -1; }
window_count() { tmux list-windows -t "$S" -F '#{window_name}' 2>/dev/null | grep -cFx inbox-poll; }
live_pid() { cat "$STATE/pid" 2>/dev/null; }
pid_alive() { [ -n "${1:-}" ] && kill -0 "$1" 2>/dev/null; }

# --- 1. RED: the gap this issue is about, without the fix -----------------
# Reproduced directly, not inferred: launch the poller the way advance-live.sh
# already does (`exec` into a plain new window, no remain-on-exit) and force
# its clean exit. The window must vanish -- that IS #10 -- and the restart
# path (`find_poller_pane`-style: a pane whose own process matches the
# poller) has nothing left to target.
tmux new-window -t "$S" -n repro-inbox-poll -d
tmux send-keys -t "$S:repro-inbox-poll" -l "$LAUNCH_CMD" 2>/dev/null
tmux send-keys -t "$S:repro-inbox-poll" Enter 2>/dev/null
for _ in $(seq 1 50); do [ -f "$STATE/pid" ] && break; sleep 0.1; done
: >"$STATE/stop"
for _ in $(seq 1 50); do
  tmux list-windows -t "$S" -F '#{window_name}' 2>/dev/null | grep -qFx repro-inbox-poll || break
  sleep 0.1
done
if tmux list-windows -t "$S" -F '#{window_name}' 2>/dev/null | grep -qFx repro-inbox-poll; then
  bad "RED: an exited poller's window disappears (without the fix)" \
    "window repro-inbox-poll is still there -- reproduction did not exercise the bug"
else
  ok "RED: an exited poller's window disappears (without the fix) — reproduced"
fi
rm -f "$STATE/pid" "$STATE/stop"

# --- 2. GREEN: poller-recover.sh creates the window from nothing ----------
tmux kill-window -t "$S:inbox-poll" 2>/dev/null
recover >/dev/null 2>&1
for _ in $(seq 1 50); do [ -f "$STATE/pid" ] && break; sleep 0.1; done
[ "$(window_count)" = "1" ] && ok "creates the window when absent" \
  || bad "creates the window when absent" "window_count=$(window_count)"
p1="$(live_pid)"
pid_alive "$p1" && ok "the poller is actually running after creation" \
  || bad "the poller is actually running after creation" "no live pid recorded"
ro=$(tmux show-window-options -t "$S:inbox-poll" 2>/dev/null | grep -c '^remain-on-exit on')
[ "$ro" -ge 1 ] && ok "remain-on-exit is set on the created window" \
  || bad "remain-on-exit is set on the created window" "$(tmux show-window-options -t "$S:inbox-poll" 2>/dev/null)"

# --- 3. GREEN: a clean exit leaves the window with a dead pane, not gone --
: >"$STATE/stop"
for _ in $(seq 1 50); do [ "$(pane_dead)" = "1" ] && break; sleep 0.1; done
[ "$(pane_dead)" = "1" ] && ok "the pane goes dead on clean exit, the window survives" \
  || bad "the pane goes dead on clean exit, the window survives" "pane_dead=$(pane_dead)"
[ "$(window_count)" = "1" ] && ok "still exactly one inbox-poll window after the exit" \
  || bad "still exactly one inbox-poll window after the exit" "window_count=$(window_count)"
rm -f "$STATE/pid" "$STATE/stop"

# --- 4. GREEN: recovery respawns the SAME window, not a second one --------
recover >/dev/null 2>&1
for _ in $(seq 1 50); do [ -f "$STATE/pid" ] && break; sleep 0.1; done
p2="$(live_pid)"
[ "$(window_count)" = "1" ] && ok "respawn reuses the same window, no second one created" \
  || bad "respawn reuses the same window, no second one created" "window_count=$(window_count)"
[ "$(pane_dead)" = "0" ] && ok "the pane is alive again after respawn" \
  || bad "the pane is alive again after respawn" "pane_dead=$(pane_dead)"
pid_alive "$p2" && [ "$p2" != "$p1" ] && ok "a genuinely new poller process is running" \
  || bad "a genuinely new poller process is running" "old=$p1 new=$p2"

# --- 5. A live poller is left completely alone -----------------------------
recover >/dev/null 2>&1
p3="$(live_pid)"
[ "$p3" = "$p2" ] && ok "recover against a healthy poller is a no-op" \
  || bad "recover against a healthy poller is a no-op" "pid changed from $p2 to $p3"
[ "$(pane_dead)" = "0" ] && ok "the healthy pane is still alive" \
  || bad "the healthy pane is still alive" "pane_dead=$(pane_dead)"

# --- 6. Non-clean exit (SIGKILL) recovers exactly the same way ------------
# #10's bar: say what differs for a non-clean exit rather than assume
# symmetry. What differs is notification (inbox-poll.sh's own report_stop
# trap cannot run under SIGKILL, so nothing pages Jon at the moment of
# death -- that gap belongs to #163's heartbeat check, not this script).
# Recovery itself does not differ: it depends only on tmux's own pane_dead,
# which fires on a killed child exactly as it does on a clean exit.
kill -KILL "$p3" 2>/dev/null
for _ in $(seq 1 50); do [ "$(pane_dead)" = "1" ] && break; sleep 0.1; done
[ "$(pane_dead)" = "1" ] && ok "SIGKILL also leaves the pane dead, window intact" \
  || bad "SIGKILL also leaves the pane dead, window intact" "pane_dead=$(pane_dead)"
rm -f "$STATE/pid"
recover >/dev/null 2>&1
for _ in $(seq 1 50); do [ -f "$STATE/pid" ] && break; sleep 0.1; done
p4="$(live_pid)"
[ "$(window_count)" = "1" ] && [ "$(pane_dead)" = "0" ] && pid_alive "$p4" \
  && ok "recovery from a SIGKILL is identical to recovery from a clean exit" \
  || bad "recovery from a SIGKILL is identical to recovery from a clean exit" \
      "window_count=$(window_count) pane_dead=$(pane_dead) pid_alive=$(pid_alive "$p4" && echo yes || echo no)"
: >"$STATE/stop"; sleep 0.3

# --- 6b. A lock left by a process that never ran its EXIT trap is reclaimed
# (a real possibility -- SIGKILL, a hard crash, a LaunchAgent enforcing a
# hard kill -- none of which run the trap that normally clears $LOCK) rather
# than wedging recovery shut forever.
mkdir -p "$STATE/.lock"
printf '99999999' >"$STATE/.lock/pid"   # a pid nothing on this machine holds
echo $(( $(date +%s) - 3600 )) >"$STATE/.lock/started"   # an hour old
rm -f "$STATE/pid" "$STATE/stop"
tmux kill-window -t "$S:inbox-poll" 2>/dev/null
recover >/dev/null 2>&1
for _ in $(seq 1 50); do [ -f "$STATE/pid" ] && break; sleep 0.1; done
[ -f "$STATE/pid" ] && ok "a stale lock (dead holder, old) is reclaimed rather than wedging recovery shut" \
  || bad "a stale lock (dead holder, old) is reclaimed rather than wedging recovery shut" \
      "no poller launched -- recovery treated the stale lock as a live one"
: >"$STATE/stop"; sleep 0.3
rm -f "$STATE/pid" "$STATE/stop"

# A YOUNG lock with an unreadable/dead-looking holder is NOT reclaimed --
# only age (not a dead-looking pid alone) proves the original holder is
# gone rather than mid-write of its own pid/started files.
mkdir -p "$STATE/.lock"
printf '99999999' >"$STATE/.lock/pid"
date +%s >"$STATE/.lock/started"   # young
tmux kill-window -t "$S:inbox-poll" 2>/dev/null
recover >/dev/null 2>&1
[ ! -f "$STATE/pid" ] && ok "a young lock is left alone even if its recorded holder looks dead" \
  || bad "a young lock is left alone even if its recorded holder looks dead" \
      "recovery acted through a lock that should still have been honoured"
rm -rf "$STATE/.lock"

# --- 6c. Two windows sharing the poller's name: refuse, do not guess ------
# The one case window-NAME addressing (necessary for the initial lookup,
# since that is the deployment identity) cannot resolve safely -- tmux
# allows duplicate window names, and a human recreating the window by hand
# during an outage (exactly what the issue this fixes describes) while an
# old, not-yet-cleaned-up one still exists produces precisely this state.
tmux kill-window -t "$S:inbox-poll" 2>/dev/null
tmux new-window -t "$S" -n inbox-poll -d 2>/dev/null
tmux new-window -t "$S" -n inbox-poll -d 2>/dev/null
dup_count_before="$(window_count)"
out=$(recover 2>&1); rc=$?
[ "$rc" -ne 0 ] && ok "two windows sharing the poller's name: recovery refuses (nonzero exit)" \
  || bad "two windows sharing the poller's name: recovery refuses (nonzero exit)" "exit $rc: $out"
[ "$(window_count)" = "$dup_count_before" ] && ok "neither duplicate window is touched" \
  || bad "neither duplicate window is touched" "window_count changed from $dup_count_before to $(window_count)"
# `kill-window -t session:inbox-poll` is itself ambiguous while two windows
# share that name -- same reason recovery refused above -- so clean up by ID.
while IFS= read -r wid; do
  [ -n "$wid" ] && tmux kill-window -t "$S:$wid" 2>/dev/null
done < <(tmux list-windows -t "$S" -F '#{window_name}	#{window_id}' 2>/dev/null | awk -F'\t' '$1=="inbox-poll"{print $2}')
rm -f "$STATE/pid" "$STATE/stop"

echo
echo "poller-recover.sh: mutation -- concurrent recovery must not create a second poller"

# --- 7. MUTATION: two recoveries racing an ABSENT window ------------------
# The one place a second poller can happen: `tmux new-window` has no
# compare-and-swap, so two callers that both observe "no window named
# inbox-poll" can both create one. Raced for real, from a shared barrier
# (test_advance_live.sh's methodology), not asserted from reasoning alone.
tmux kill-window -t "$S:inbox-poll" 2>/dev/null
rm -f "$STATE/pid" "$STATE/stop"
RACE_ITERS=10
race_bad=0
for ((n=1; n<=RACE_ITERS; n++)); do
  tmux kill-window -t "$S:inbox-poll" 2>/dev/null
  rm -f "$STATE/pid" "$STATE/stop"
  R=$(mktemp -d "$STATE/race.XXXXXX")
  race_child() {
    : >"$R/ready.$1"
    while [ ! -e "$R/go" ]; do :; done
    recover >"$R/out.$1" 2>&1
  }
  race_child A & race_child B &
  while [ ! -e "$R/ready.A" ] || [ ! -e "$R/ready.B" ]; do :; done
  : >"$R/go"
  wait
  for _ in $(seq 1 50); do [ -f "$STATE/pid" ] && break; sleep 0.05; done
  sleep 0.2   # let a second, wrongly-unlocked create finish landing if it would
  wc="$(window_count)"
  [ "$wc" = "1" ] || race_bad=$((race_bad+1))
done
if [ "$race_bad" -eq 0 ]; then
  ok "$RACE_ITERS concurrent recoveries against an absent window: exactly one poller, every time"
else
  bad "$RACE_ITERS concurrent recoveries against an absent window: exactly one poller, every time" \
    "$race_bad/$RACE_ITERS iterations left more than one inbox-poll window"
fi
: >"$STATE/stop"; sleep 0.3

# --- 8. MUTATION CONFIRMED: the same race, with the lock removed ----------
# Proves iteration 7 is actually pinning the lock, not passing by luck: a
# copy of poller-recover.sh with the mkdir lock stripped out, raced the same
# way, MUST be able to produce two windows. If this stayed green the race
# above would be worthless -- it would mean nothing in this suite can tell a
# locked recovery from an unlocked one.
UNLOCKED="$STATE/poller-recover.unlocked.sh"
patch_rc=0
python3 - "$RECOVER" "$UNLOCKED" <<'PY' || patch_rc=$?
import re, sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''if ! acquire_lock; then
  log "SKIPPED -- another recovery is already in flight ($LOCK held)"
  exit 0
fi
trap 'rm -rf "$LOCK" 2>/dev/null' EXIT'''
assert marker in text, "lock block not found -- poller-recover.sh shape changed"
assert text.count(marker) == 1, "lock block not unique -- poller-recover.sh shape changed"
open(dst, "w").write(text.replace(marker, ": # lock removed for this mutation test", 1))
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a lock-free copy of poller-recover.sh" \
    "could not patch $RECOVER (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a lock-free copy of poller-recover.sh"
  chmod +x "$UNLOCKED"
  unlocked_recover() { POLLER_WINDOW=inbox-poll POLLER_LAUNCH_CMD="$LAUNCH_CMD" \
    POLLER_RECOVER_LOCK="$STATE/.lock" POLLER_RECOVER_LOG="$STATE/log" \
    SUPERVISOR_STATE="$STATE" bash "$UNLOCKED" "$S"; }
  saw_two=0
  for ((n=1; n<=RACE_ITERS; n++)); do
    tmux kill-window -t "$S:inbox-poll" 2>/dev/null
    rm -f "$STATE/pid" "$STATE/stop"
    R=$(mktemp -d "$STATE/urace.XXXXXX")
    urace_child() {
      : >"$R/ready.$1"
      while [ ! -e "$R/go" ]; do :; done
      unlocked_recover >"$R/out.$1" 2>&1
    }
    urace_child A & urace_child B &
    while [ ! -e "$R/ready.A" ] || [ ! -e "$R/ready.B" ]; do :; done
    : >"$R/go"
    wait
    sleep 0.3
    wc="$(window_count)"
    [ "$wc" -gt 1 ] && saw_two=1
    : >"$STATE/stop" 2>/dev/null; sleep 0.1
  done
  if [ "$saw_two" -eq 1 ]; then
    ok "mutation confirmed: removing the lock lets a race create a second poller window (iteration 7's assertion would now be red)"
  else
    bad "mutation confirmed: removing the lock lets a race create a second poller window" \
      "never observed two inbox-poll windows in $RACE_ITERS unlocked iterations -- the race window may be too narrow on this machine to trust iteration 7's pass as meaningful"
  fi
fi
: >"$STATE/stop" 2>/dev/null

if [ "$fail" -eq 0 ]; then
  echo "$pass passed, $fail failed"
else
  echo "$pass passed, $fail failed"
  exit 1
fi
