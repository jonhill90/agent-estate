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
cleanup_all() { cleanup; pkill -KILL -f "$RT/inbox-poll.sh" 2>/dev/null; rm -rf "$RT" "${STATE:-}"; }
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

# --- 6d. RED: reclaim must not steal the lock from a live, mid-acquisition
# holder (agent-supervisor#14 review, the retracted-APPROVE comment) --
# acquire_lock(), on a failed mkdir, reads $LOCK/pid and $LOCK/started
# immediately. In the real window between the winner's mkdir succeeding and
# its own pid/started writes landing two lines later (each a fork+exec, not
# a builtin), both files are still empty, so neither guard fires and a
# second caller falls straight through to RECLAIMING with no age check at
# all. Reproduced with a *copy* of poller-recover.sh carrying a deliberate
# delay inserted into that exact window -- widening the always-present race
# to make it deterministic to observe, the reviewer's own method -- as the
# slow, legitimately-alive acquirer ("A"). Every assertion below is about
# what the REAL, UNMODIFIED script ("B", via recover()) does when it lands
# in that window, not about the copy.
WINDOW_NAME=inbox-poll
poller_proc_count() { pgrep -f "$STAND_IN" 2>/dev/null | wc -l | tr -d ' '; }
reset_race_state() {
  : >"$STATE/stop" 2>/dev/null
  for _ in $(seq 1 30); do [ "$(poller_proc_count)" = "0" ] && break; sleep 0.1; done
  pkill -KILL -f "$STAND_IN" 2>/dev/null
  while IFS= read -r wid; do
    [ -n "$wid" ] && tmux kill-window -t "$S:$wid" 2>/dev/null
  done < <(tmux list-windows -t "$S" -F '#{window_name}	#{window_id}' 2>/dev/null | awk -F'\t' -v w="$WINDOW_NAME" '$1==w{print $2}')
  rm -f "$STATE/pid" "$STATE/stop"
  rm -rf "$STATE/.lock"
}
reset_race_state

SLOW="$STATE/poller-recover.slow.sh"
patch_rc=0
python3 - "$RECOVER" "$SLOW" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''  if mkdir "$LOCK" 2>/dev/null; then
    printf '%s' "$$" >"$LOCK/pid" 2>/dev/null
    date +%s >"$LOCK/started" 2>/dev/null
    return 0
  fi'''
assert marker in text, "acquire_lock mkdir-success block not found -- poller-recover.sh shape changed"
assert text.count(marker) == 1, "acquire_lock mkdir-success block not unique -- poller-recover.sh shape changed"
patched = '''  if mkdir "$LOCK" 2>/dev/null; then
    sleep "${POLLER_RECOVER_TEST_ACQUIRE_DELAY:-2}"
    printf '%s' "$$" >"$LOCK/pid" 2>/dev/null
    date +%s >"$LOCK/started" 2>/dev/null
    return 0
  fi'''
open(dst, "w").write(text.replace(marker, patched, 1))
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a slow-acquire copy of poller-recover.sh" \
    "could not patch $RECOVER (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a slow-acquire copy of poller-recover.sh"
  chmod +x "$SLOW"

  # A: the copy, slow -- wins the mkdir, then sleeps mid-acquisition before it
  # has written pid/started. Real tmux, real LAUNCH_CMD, same STATE as B.
  POLLER_RECOVER_TEST_ACQUIRE_DELAY=2 POLLER_WINDOW="$WINDOW_NAME" POLLER_LAUNCH_CMD="$LAUNCH_CMD" \
    POLLER_RECOVER_LOCK="$STATE/.lock" POLLER_RECOVER_LOG="$STATE/log-A" \
    SUPERVISOR_STATE="$STATE" bash "$SLOW" "$S" >"$STATE/out-A" 2>&1 &
  A_JOB=$!

  caught_window=0
  for _ in $(seq 1 150); do
    if [ -d "$STATE/.lock" ] && [ ! -s "$STATE/.lock/pid" ] && [ ! -s "$STATE/.lock/started" ]; then
      caught_window=1; break
    fi
    sleep 0.02
  done

  if [ "$caught_window" -ne 1 ]; then
    bad "setup: caught the in-progress-acquisition window (lock dir present, pid/started still empty)" \
      "never observed that state -- widen POLLER_RECOVER_TEST_ACQUIRE_DELAY or the polling loop"
    wait "$A_JOB" 2>/dev/null
  else
    # B: the REAL, unmodified script, run directly into that window.
    out_b=$(recover 2>&1); rc_b=$?
    wait "$A_JOB" 2>/dev/null

    for _ in $(seq 1 50); do [ -f "$STATE/pid" ] && break; sleep 0.1; done
    sleep 0.3   # let a second, wrongly-launched poller finish landing if it would

    if echo "$out_b" | grep -q "RECLAIMING"; then
      bad "the real script does not steal the lock from a live, mid-acquisition holder" \
        "B reclaimed: $out_b"
    else
      ok "the real script does not steal the lock from a live, mid-acquisition holder"
    fi

    procs="$(poller_proc_count)"
    [ "$procs" = "1" ] && ok "exactly one poller process results from the race (not two)" \
      || bad "exactly one poller process results from the race (not two)" "poller_proc_count=$procs"

    [ "$(window_count)" = "1" ] && ok "exactly one inbox-poll window results from the race" \
      || bad "exactly one inbox-poll window results from the race" "window_count=$(window_count)"
  fi
fi
reset_race_state

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

echo
echo "poller-recover.sh: agent-supervisor#19 -- window absence is not process absence"

# --- 9. GREEN: a live poller in a window recover() never created -- genuine
# no-op. #10's own suite (test 5, above) only ever exercises a poller THIS
# script itself launched. #19's incident poller predates the tick that found
# it -- launched by hand, or by advance-live.sh's cooperative restart path,
# same as test 1's own repro method. This is the brief's required acceptance
# test: no window created, no second process, exit 0.
tmux kill-window -t "$S:inbox-poll" 2>/dev/null
rm -f "$STATE/pid" "$STATE/stop"
tmux new-window -t "$S" -n inbox-poll -d 2>/dev/null
tmux send-keys -t "$S:inbox-poll" -l "$LAUNCH_CMD" 2>/dev/null
tmux send-keys -t "$S:inbox-poll" Enter 2>/dev/null
for _ in $(seq 1 50); do [ -f "$STATE/pid" ] && break; sleep 0.1; done
p9="$(live_pid)"
pid_alive "$p9" && ok "setup: a poller is running in a window recover() did not create" \
  || bad "setup: a poller is running in a window recover() did not create" "no live pid recorded"
out9=$(recover 2>&1); rc9=$?
[ "$rc9" -eq 0 ] && ok "recover() against it exits 0" \
  || bad "recover() against it exits 0" "exit $rc9: $out9"
[ "$(window_count)" = "1" ] && ok "no second window created" \
  || bad "no second window created" "window_count=$(window_count)"
[ "$(live_pid)" = "$p9" ] && pid_alive "$p9" && ok "no second process -- the original poller is untouched" \
  || bad "no second process -- the original poller is untouched" "pid was $p9, now $(live_pid)"
: >"$STATE/stop"; sleep 0.3
rm -f "$STATE/pid" "$STATE/stop"

# --- 10. GREEN: a windowless orphan -- recover refuses to duplicate it -----
# Measured root cause (see poller-recover.sh's own comment at the fix): the
# real inbox-poll.sh traps HUP (a killed window sends it one) and its
# handler makes a network call before the process actually exits -- a real
# gap where the window is gone but the process is not. Modeled here without
# a network dependency: this stand-in's own HUP handler blocks on a release
# file, the same shape (window destroyed, handler still running, process
# still alive) without the timing flakiness a real sleep-based delay would
# add. The file MUST be named inbox-poll.sh: that is the identity
# poller-recover.sh's new orphan check keys on (SERVICE_RE, shared with
# lanes.sh and advance-live.sh), not a window name -- the whole point of
# this test.
ORPHAN_STAND_IN="$RT/inbox-poll.sh"
cat > "$ORPHAN_STAND_IN" <<'EOF'
#!/bin/bash
STOP="${POLLER_STOP_FILE:?}"
PIDFILE="${POLLER_PID_FILE:?}"
RELEASE="${POLLER_HUP_RELEASE_FILE:?}"
echo "$$" > "$PIDFILE"
on_hup() {
  while [ ! -f "$RELEASE" ]; do sleep 0.1; done
  exit 129
}
trap on_hup HUP
while [ ! -f "$STOP" ]; do sleep 0.1; done
exit 0
EOF
chmod +x "$ORPHAN_STAND_IN"
ORPHAN_RELEASE="$STATE/hup-release"
orphan_pid_count() { pgrep -f "$ORPHAN_STAND_IN" 2>/dev/null | wc -l | tr -d ' '; }

tmux kill-window -t "$S:inbox-poll" 2>/dev/null
rm -f "$STATE/pid" "$STATE/stop" "$ORPHAN_RELEASE"
# poller-recover.sh's orphan check scopes its process-table match to $LIVE
# (real_path of it) so a deployment never refuses to recover its OWN poller
# over some unrelated inbox-poll.sh elsewhere on the machine -- production's
# own poller, for one, is very likely running on any dev box this suite runs
# on. `cd` into recover()'s own default LIVE ($STATE/live, since SUPERVISOR_LIVE
# is not overridden below) so this stand-in's cwd matches it, the same shape
# `cd '$LIVE' && exec ...` gives the real poller.
TEST_LIVE="$STATE/live"
mkdir -p "$TEST_LIVE"
ORPHAN_CMD="cd '$TEST_LIVE' && POLLER_STOP_FILE='$STATE/stop' POLLER_PID_FILE='$STATE/pid' POLLER_HUP_RELEASE_FILE='$ORPHAN_RELEASE' exec '$ORPHAN_STAND_IN'"
tmux new-window -t "$S" -n inbox-poll -d 2>/dev/null
tmux send-keys -t "$S:inbox-poll" -l "$ORPHAN_CMD" 2>/dev/null
tmux send-keys -t "$S:inbox-poll" Enter 2>/dev/null
for _ in $(seq 1 50); do [ -f "$STATE/pid" ] && break; sleep 0.1; done
p10="$(live_pid)"
pid_alive "$p10" && ok "setup: the orphan-to-be is running, in a window" \
  || bad "setup: the orphan-to-be is running, in a window" "no live pid recorded"

tmux kill-window -t "$S:inbox-poll" 2>/dev/null
for _ in $(seq 1 50); do [ "$(window_count)" = "0" ] && break; sleep 0.1; done
[ "$(window_count)" = "0" ] && ok "the window is gone" \
  || bad "the window is gone" "window_count=$(window_count)"
pid_alive "$p10" && ok "the poller process survives its window -- a genuine windowless orphan" \
  || bad "the poller process survives its window -- a genuine windowless orphan" "pid $p10 is not alive"

out10=$(recover 2>&1); rc10=$?
[ "$rc10" -ne 0 ] && ok "recover() against a windowless orphan refuses (nonzero exit), it does not silently retry-forever" \
  || bad "recover() against a windowless orphan refuses (nonzero exit)" "exit $rc10: $out10"
[ "$(window_count)" = "0" ] && ok "no window is created over a live orphan" \
  || bad "no window is created over a live orphan" "window_count=$(window_count)"
[ "$(orphan_pid_count)" = "1" ] && ok "no second inbox-poll.sh process is started -- exactly one, still the orphan" \
  || bad "no second inbox-poll.sh process is started" "orphan_pid_count=$(orphan_pid_count)"

# release the orphan and confirm the NEXT tick recovers normally once it is
# actually gone -- the refusal above must be a hold, not a permanent wedge.
: >"$ORPHAN_RELEASE"
for _ in $(seq 1 50); do [ "$(orphan_pid_count)" = "0" ] && break; sleep 0.1; done
[ "$(orphan_pid_count)" = "0" ] && ok "the orphan finishes exiting once released" \
  || bad "the orphan finishes exiting once released" "orphan_pid_count=$(orphan_pid_count)"
rm -f "$STATE/pid" "$STATE/stop"
recover >/dev/null 2>&1
for _ in $(seq 1 50); do [ -f "$STATE/pid" ] && break; sleep 0.1; done
[ "$(window_count)" = "1" ] && pid_alive "$(live_pid)" \
  && ok "once the orphan is truly gone, the next recovery creates the window normally" \
  || bad "once the orphan is truly gone, the next recovery creates the window normally" \
      "window_count=$(window_count) pid_alive=$(pid_alive "$(live_pid)" && echo yes || echo no)"
: >"$STATE/stop"; sleep 0.3
rm -f "$STATE/pid" "$STATE/stop"

# --- 11. MUTATION CONFIRMED: strip the orphan check, the race reappears ---
# Proves test 10 is actually pinning the new guard, not passing by luck --
# same discipline as section 8's lock-removal mutation.
NO_ORPHAN_CHECK="$STATE/poller-recover.no-orphan-check.sh"
patch_rc=0
python3 - "$RECOVER" "$NO_ORPHAN_CHECK" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
start_marker = '  orphan_pid=""\n'
end_marker = '''  if [ -n "$orphan_pid" ]; then
    log "FAILED -- inbox-poll.sh is already running (pid $orphan_pid) with no window named '$WINDOW' in session '$SESSION' -- refusing to start a second poller; it needs a window reattached, not a duplicate"
    exit 1
  fi
'''
assert start_marker in text, "orphan-check start marker not found -- poller-recover.sh shape changed"
assert end_marker in text, "orphan-check end marker not found -- poller-recover.sh shape changed"
start_i = text.index(start_marker)
end_i = text.index(end_marker) + len(end_marker)
patched = text[:start_i] + text[end_i:]
assert patched != text, "orphan-check block did not shrink the script -- patch had no effect"
open(dst, "w").write(patched)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of poller-recover.sh with the orphan check removed" \
    "could not patch $RECOVER (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of poller-recover.sh with the orphan check removed"
  chmod +x "$NO_ORPHAN_CHECK"
  no_orphan_check_recover() { POLLER_WINDOW=inbox-poll POLLER_LAUNCH_CMD="$LAUNCH_CMD" \
    POLLER_RECOVER_LOCK="$STATE/.lock" POLLER_RECOVER_LOG="$STATE/log" \
    SUPERVISOR_STATE="$STATE" bash "$NO_ORPHAN_CHECK" "$S"; }

  rm -f "$STATE/pid" "$STATE/stop" "$ORPHAN_RELEASE"
  tmux kill-window -t "$S:inbox-poll" 2>/dev/null
  tmux new-window -t "$S" -n inbox-poll -d 2>/dev/null
  tmux send-keys -t "$S:inbox-poll" -l "$ORPHAN_CMD" 2>/dev/null
  tmux send-keys -t "$S:inbox-poll" Enter 2>/dev/null
  for _ in $(seq 1 50); do [ -f "$STATE/pid" ] && break; sleep 0.1; done
  tmux kill-window -t "$S:inbox-poll" 2>/dev/null
  for _ in $(seq 1 50); do [ "$(window_count)" = "0" ] && break; sleep 0.1; done

  out_m=$(no_orphan_check_recover 2>&1)
  for _ in $(seq 1 50); do [ "$(window_count)" = "1" ] && break; sleep 0.1; done
  # The duplicate lands via $LAUNCH_CMD (the generic $STAND_IN), not
  # $ORPHAN_STAND_IN, so the signature is a NEW window/pid file appearing
  # while the orphan (still counted separately, by its own script path) is
  # untouched -- two live pollers, exactly what agent-supervisor#19 is about.
  if [ "$(window_count)" = "1" ] && [ "$(orphan_pid_count)" = "1" ]; then
    ok "mutation confirmed: removing the orphan check lets recover() duplicate a windowless poller (test 10's assertion would now be red)"
  else
    bad "mutation confirmed: removing the orphan check lets recover() duplicate a windowless poller" \
      "window_count=$(window_count) orphan_pid_count=$(orphan_pid_count) -- expected a new window created (the duplicate) alongside the untouched orphan; recover() output: $out_m"
  fi
  : >"$ORPHAN_RELEASE"
  for _ in $(seq 1 50); do [ "$(orphan_pid_count)" = "0" ] && break; sleep 0.1; done
  : >"$STATE/stop" 2>/dev/null
fi
tmux kill-window -t "$S:inbox-poll" 2>/dev/null
rm -f "$STATE/pid" "$STATE/stop" "$ORPHAN_RELEASE"

if [ "$fail" -eq 0 ]; then
  echo "$pass passed, $fail failed"
else
  echo "$pass passed, $fail failed"
  exit 1
fi
