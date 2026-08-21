#!/bin/bash
# agent-supervisor#57: watchdog.sh's advance_on_exit runs advance-live.sh from
# a temp copy_dir, copying only advance-live.sh and poller-window.sh.
# prompt_poller_relaunch() (added by #48) also needs poller-recover.sh beside
# it -- $HERE inside the copy resolves to copy_dir, so without it the prompt
# relaunch silently gave up on EVERY watchdog tick, every time, leaving only
# the 180s backstop to actually relaunch the poller.
#
# tests/supervisor/test_advance_live.sh's #47 relaunch tests all invoke
# advance-live.sh IN PLACE (`bash "$ADVANCE" ...`), never through watchdog.sh's
# copy-and-invoke path, so copy_dir's contents had zero coverage --
# `grep -rln "copy_dir" tests/supervisor/ scripts/supervisor/` returned only
# watchdog.sh itself. These tests close that gap by driving the real
# watchdog.sh end to end (its own exit trap builds copy_dir and runs the copy),
# against a real, isolated tmux session and a real git worktree -- never the
# live estate's own.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
pass=0; fail=0
say_ok()  { echo "  ok   $1"; pass=$((pass+1)); }
say_bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }
want_exit() { if [ "$2" = "$3" ]; then say_ok "$1"; else say_bad "$1" "expected exit $3, got $2: ${4:-}"; fi }

echo "watchdog.sh: agent-supervisor#57 -- poller-recover.sh missing from the copy path"

if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP no tmux on PATH"
  echo "  0 passed, 0 failed"
  exit 0
fi

# shellcheck source=../../scripts/supervisor/tmux-isolation.sh
source "$SUP/tmux-isolation.sh"
# shellcheck source=./lib/reap-verified.sh
source "$HERE/lib/reap-verified.sh"
RT=$(mktemp -d "${TMPDIR:-/tmp}/watchdog-57-tmux.XXXXXX")
OLD_TMUX="${TMUX-}"
OLD_TMUX_TMPDIR="${TMUX_TMPDIR-}"
unset TMUX
export TMUX_TMPDIR="$RT"
SESSION="watchdog-57-$$"

# agent-supervisor#104: this suite previously had NO trap at all -- cleanup
# (including `tmux kill-session`, the only thing that reaches the stand-in
# poller process below) ran only if every assertion below was reached and the
# script fell off the end normally. Any early exit -- an unbound variable
# under `set -u`, this suite's own process being killed by the test harness's
# timeout -- skipped cleanup entirely and left the stand-in `inbox-poll.sh`
# process running for as long as the machine stayed up. Two of exactly that
# shape were measured still alive hours later (#104's own issue). PID_HISTORY
# is written by the stand-in itself (below) the moment it starts, so even a
# trap firing on a signal that lands before the rest of setup finishes still
# has something to reap by pid -- never by name.
cleanup_all() {
  [ -n "${PID_HISTORY:-}" ] && reap_pid_history_verified "$PID_HISTORY" "$RT" 5
  unset TMUX; export TMUX_TMPDIR="$RT"
  [ -n "${SESSION:-}" ] && tmux kill-session -t "$SESSION" >/dev/null 2>&1
  [ -n "${SRC:-}" ] && [ -n "${LIVE:-}" ] && {
    git -C "$SRC" worktree remove --force "$LIVE" >/dev/null 2>&1
    git -C "$SRC" worktree prune >/dev/null 2>&1
  }
  rm -rf "${A:-}" "$RT" "${NOTIFY_DIR:-}"
  [ "$OLD_TMUX_TMPDIR" ] && export TMUX_TMPDIR="$OLD_TMUX_TMPDIR" || unset TMUX_TMPDIR
  [ "$OLD_TMUX" ] && export TMUX="$OLD_TMUX"
}
trap cleanup_all EXIT INT TERM

if ! assert_isolated_tmux; then
  say_bad "setup: isolated tmux socket for #57" "TMUX_TMPDIR=$TMUX_TMPDIR"
  echo "  $pass passed, $fail failed"
  exit 1
fi
say_ok "setup: isolated tmux socket for #57"

# --- a real origin + a real LIVE worktree, never the estate's own ----------
A=$(mktemp -d)
git init -q --bare "$A/origin.git"
git clone -q "$A/origin.git" "$A/src" 2>/dev/null
SRC="$A/src"
git -C "$SRC" config user.email t@e.com; git -C "$SRC" config user.name T
git -C "$SRC" checkout -q -b main
mkdir -p "$SRC/scripts/supervisor"
# agent-supervisor#163: lanes.sh (and its own input-box.sh dependency) added
# so watchdog.sh's never-busy check finds a real lanes.sh beside it here, the
# same as a real live worktree always does -- without it, this fixture's
# "lanes.sh is missing beside this watchdog" was never the scenario #57's
# poller-copy path is about, and #163's new fail-streak escalation started
# paging on it, which is what actually broke "pages nobody" below.
for f in watchdog.sh advance-live.sh poller-window.sh poller-recover.sh session-defaults.sh \
         sleepcheck.py watchdog_notify.py loop-tick.md harness-registry.sh lanes.sh input-box.sh \
         poller-lib.sh; do
  cp "$SUP/$f" "$SRC/scripts/supervisor/"
done
cp -R "$SUP/harness" "$SRC/scripts/supervisor/"
chmod +x "$SRC/scripts/supervisor/poller-recover.sh"
git -C "$SRC" add -A >/dev/null 2>&1
git -C "$SRC" commit -q -m "first"
git -C "$SRC" push -q -u origin main
LIVE="$A/live"
git -C "$SRC" worktree add -q --detach "$LIVE" origin/main
live_sha0=$(git -C "$LIVE" rev-parse HEAD)

# --- the real, isolated tmux session -----------------------------------
# Window 0: a stand-in for the director's own pane. Busy chrome so the
# watchdog never tries to restart a /loop into it -- this fix is about the
# poller relaunch, not the director-loop restart, and a busy pane keeps that
# machinery out of the way entirely.
tmux new-session -d -s "$SESSION" -x 200 -y 50 \
  -- bash -c 'printf "  ⏵⏵ bypass permissions on \xc2\xb7 esc to interrupt \xc2\xb7 \xe2\x86\x90 1 agent\n"; sleep 300'

STAND_IN="$RT/inbox-poll.sh"
cat >"$STAND_IN" <<'EOF'
#!/bin/bash
set -u
STATUS="${INBOX_POLL_STATUS:?}"
FLAG="${INBOX_POLL_RESTART_FLAG:?}"
PID_FILE="${POLLER_PID_FILE:?}"
PID_HISTORY="${POLLER_PID_HISTORY:?}"
SHA="${POLLER_STATUS_SHA:?}"
mkdir -p "$(dirname "$STATUS")"
echo "$$" >"$PID_FILE"
echo "$$" >>"$PID_HISTORY"
write_status() {
  {
    printf 'checked: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf 'sha:     %s\n' "$SHA"
    printf 'state:   %s\n' "$1"
    printf 'pid:     %s\n' "$$"
  } >"$STATUS"
}
write_status ok
trap 'write_status stopped' TERM

# agent-supervisor#449: a SIGKILL of the test script (WATCHDOG_TEST_GUARDIAN_PID
# below) skips its own `trap cleanup_all EXIT INT TERM` entirely -- a trap
# cannot fire on SIGKILL -- so #104's cleanup, which only ever runs INSIDE the
# dying test process, never runs either. Nothing outside that dead process was
# watching, so this stand-in was found still running, alone, 5h26m later,
# under a tmux session whose parent test process no longer existed. The fix
# has to live HERE, in the thing that outlives the test, not in one more trap
# on the test: this loop checks its own guardian's liveness every tick and, if
# the guardian is gone, tears down its own tmux session and exits -- no
# external sweep to keep running, no cron to wire up, nothing else has to be
# correctly configured for this to hold. A bounded max lifetime is a second,
# independent backstop for the case the guardian pid got recycled by an
# unrelated process before this loop ever checked it again.
GUARDIAN_PID="${WATCHDOG_TEST_GUARDIAN_PID:-}"
SELF_SESSION="${WATCHDOG_TEST_SESSION:-}"
SELF_TMUX_TMPDIR="${WATCHDOG_TEST_TMUX_TMPDIR:-}"
MAX_LIFETIME="${WATCHDOG_TEST_MAX_LIFETIME_SECS:-180}"
START_EPOCH=$(date +%s)

self_destruct() { # self_destruct <reason>
  write_status stopped
  echo "inbox-poll.sh(#449 stand-in $$): self-terminating -- $1" >&2
  # Same isolation posture as tmux-isolation.sh's assert_isolated_tmux --
  # never address the default socket, and never kill a session this fixture
  # cannot prove is its own isolated one.
  if [ -n "$SELF_SESSION" ] && [ -n "$SELF_TMUX_TMPDIR" ] && [ -d "$SELF_TMUX_TMPDIR" ]; then
    env -u TMUX TMUX_TMPDIR="$SELF_TMUX_TMPDIR" tmux kill-session -t "$SELF_SESSION" >/dev/null 2>&1
  fi
  exit 0
}

while :; do
  if [ -f "$FLAG" ]; then
    rm -f "$FLAG"
    write_status stopped
    exit 0
  fi
  if [ -n "$GUARDIAN_PID" ] && ! kill -0 "$GUARDIAN_PID" 2>/dev/null; then
    self_destruct "guardian pid $GUARDIAN_PID (the test script) is no longer alive"
  fi
  now=$(date +%s)
  if [ $((now - START_EPOCH)) -ge "$MAX_LIFETIME" ]; then
    self_destruct "exceeded max lifetime ${MAX_LIFETIME}s independent of guardian liveness"
  fi
  sleep 0.1
done
EOF
chmod +x "$STAND_IN"
# agent-supervisor#194: this stand-in is itself a real, unrelated-lineage
# process named inbox-poll.sh under a mktemp'd path -- exactly the shape
# poller_process_rows (watchdog.sh) now requires a marker to trust as
# harmless, since #194 found path alone proved nothing. Mark it so this
# test's own fixture doesn't add POLLER-DUPLICATE noise to the ticks below.
touch "$RT/.watchdog-test-fixture"

STATE="$A/state"; mkdir -p "$STATE" "$STATE/transcripts"
FLAG="$STATE/.inbox-poll-restart-requested"
STATUS_FILE="$STATE/inbox-poll.status"
PID_FILE="$A/pid"
PID_HISTORY="$A/pids"
launch_cmd() { # launch_cmd <sha>
  # agent-supervisor#449: WATCHDOG_TEST_GUARDIAN_PID is this test script's own
  # $$, so the stand-in can detect a SIGKILL of its guardian (see the
  # self_destruct block in the heredoc above) and tear down its own tmux
  # session itself -- nothing outside the stand-in ever has to notice.
  printf "INBOX_POLL_STATUS='%s' INBOX_POLL_RESTART_FLAG='%s' POLLER_PID_FILE='%s' POLLER_PID_HISTORY='%s' POLLER_STATUS_SHA='%s' WATCHDOG_TEST_GUARDIAN_PID='%s' WATCHDOG_TEST_SESSION='%s' WATCHDOG_TEST_TMUX_TMPDIR='%s' exec '%s'" \
    "$STATUS_FILE" "$FLAG" "$PID_FILE" "$PID_HISTORY" "$1" "$$" "$SESSION" "$RT" "$STAND_IN"
}
tmux new-window -t "$SESSION" -n inbox-poll -d -- "$(launch_cmd "$live_sha0")"
tmux set-window-option -t "$SESSION:inbox-poll" remain-on-exit on >/dev/null 2>&1

wait_for_pid_file() {
  local deadline=$((SECONDS + 8))
  while [ ! -s "$PID_FILE" ] && [ "$SECONDS" -lt "$deadline" ]; do sleep 0.1; done
  [ -s "$PID_FILE" ]
}
pid_alive() { [ -n "${1:-}" ] && kill -0 "$1" 2>/dev/null; }
window_count() { tmux list-windows -t "$SESSION" -F '#{window_name}' 2>/dev/null | grep -cFx inbox-poll; }
live_poller_count() {
  local count=0 p
  while IFS= read -r p; do
    [ -n "$p" ] && kill -0 "$p" 2>/dev/null && count=$((count + 1))
  done < <(pgrep -f "$STAND_IN" 2>/dev/null)
  printf '%s\n' "$count"
}
await_replacement() {
  local old="$1" deadline=$((SECONDS + 10)) new
  while [ "$SECONDS" -lt "$deadline" ]; do
    new=$(cat "$PID_FILE" 2>/dev/null)
    if [ -n "$new" ] && [ "$new" != "$old" ] && pid_alive "$new"; then
      printf '%s\n' "$new"
      return 0
    fi
    sleep 0.1
  done
  return 1
}

if wait_for_pid_file; then
  old_pid=$(cat "$PID_FILE")
  say_ok "setup: stale poller is running before the watchdog tick"
else
  old_pid=""
  say_bad "setup: stale poller is running before the watchdog tick" "no pid file"
fi

# Advance origin/main so the poller's sha (recorded at $live_sha0) is stale
# once the watchdog advances LIVE to it.
echo second >"$SRC/marker.txt"
git -C "$SRC" add -A >/dev/null 2>&1
git -C "$SRC" commit -q -m second
git -C "$SRC" push -q origin main
live_sha1=$(git -C "$SRC" rev-parse HEAD)
# The sha a freshly-relaunched poller reports it is running -- must track
# wherever LIVE currently is, or every relaunched poller reports the SAME
# (now stale-again) sha and the next tick relaunches it all over again,
# cascading indefinitely instead of settling.
CUR_LIVE_SHA="$live_sha1"

seed_poller_status() { # seed_poller_status <sha> <pid>
  {
    printf 'checked: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf 'sha:     %s\n' "$1"
    printf 'state:   ok\n'
    printf 'pid:     %s\n' "$2"
  } >"$STATUS_FILE"
}

NOTIFY_DIR=$(mktemp -d)
cat >"$NOTIFY_DIR/up.sh" <<'EOF'
#!/bin/bash
echo "$1|$2" >> "$0.calls"
EOF
chmod +x "$NOTIFY_DIR/up.sh"
UP="$NOTIFY_DIR/up.sh"
up_calls() { cat "$UP.calls" 2>/dev/null; }

# wd_tick <state-dir> -- one real watchdog.sh tick, through its own exit trap,
# against the real LIVE worktree and the real, isolated tmux session above.
# This is the one thing #47's tests never did: reach prompt_poller_relaunch
# through watchdog.sh's copy_dir, not by invoking advance-live.sh directly.
wd_tick() {
  rm -rf "$1"; mkdir -p "$1/transcripts"
  SUPERVISOR_LIVE="$LIVE" SUPERVISOR_PANE="$SESSION:0.1" LANES_SESSION="$SESSION" \
  SUPERVISOR_STATE="$STATE" SUPERVISOR_STATUS="$1/st" SUPERVISOR_LOG="$1/lg" \
  SUPERVISOR_STAMP="$1/stamp" SUPERVISOR_HISTORY="$1/hist" NOTIFY_ENV="$1/none.env" \
  NOTIFY_SCRIPT="$UP" SLEEPCHECK_DIR="$1/transcripts" \
  POLLER_LAUNCH_CMD="$(launch_cmd "$CUR_LIVE_SHA")" POLLER_RECOVER_LOCK="$STATE/.poller-recover.lock" \
  POLLER_RECOVER_LOG="$STATE/poller-recover.log" INBOX_POLL_RELAUNCH_WAIT_SECONDS=8 \
    bash "$LIVE/scripts/supervisor/watchdog.sh" >/dev/null 2>"$1/err"
}

# --- 1. drive the relaunch through watchdog.sh's copy path -----------------
seed_poller_status "$live_sha0" "$old_pid"
rm -f "$FLAG"
wd_tick "$A/t1"
new_pid=$(await_replacement "$old_pid" || true)
if [ -n "$new_pid" ]; then
  say_ok "watchdog.sh's copy path relaunches the poller and a different live pid appears within seconds"
else
  say_bad "watchdog.sh's copy path relaunches the poller and a different live pid appears within seconds" \
    "old=$old_pid current=$(cat "$PID_FILE" 2>/dev/null) advance.log=$(cat "$STATE/advance-live.log" 2>/dev/null | tr '\n' ' ') recover.log=$(cat "$STATE/poller-recover.log" 2>/dev/null | tr '\n' ' ') watchdog.log=$(cat "$A/t1/lg" 2>/dev/null | tr '\n' ' ')"
fi
[ "$(window_count)" = "1" ] && [ "$(live_poller_count)" = "1" ] \
  && say_ok "exactly one live poller and one inbox-poll window after the relaunch" \
  || say_bad "exactly one live poller and one inbox-poll window after the relaunch" \
    "windows=$(window_count) live_processes=$(live_poller_count) pids=$(cat "$PID_HISTORY" 2>/dev/null | tr '\n' ' ')"
if [ -z "$(up_calls)" ]; then say_ok "a normal relaunch pages nobody"
else say_bad "a normal relaunch pages nobody" "paged: $(up_calls)"; fi

# --- 1b. mutation: reproduce the pre-fix shape, confirm it goes RED --------
# Patches $LIVE's OWN watchdog.sh copy step back to what shipped before this
# fix (advance-live.sh and poller-window.sh only, no poller-recover.sh copy
# attempt at all). poller-recover.sh stays present beside it in
# $LIVE/scripts/supervisor -- exactly the pre-fix bug: the file exists, but
# the copy step never carries it into copy_dir, so $HERE/poller-recover.sh
# resolves inside copy_dir and is missing. If this does not go red, test 1
# above is not actually pinned to the fix.
patch_rc=0
python3 - "$LIVE/scripts/supervisor/watchdog.sh" <<'PY' || patch_rc=$?
import sys
path = sys.argv[1]
text = open(path).read()
marker_start = '  cp "$HERE/poller-window.sh" "$copy_dir/poller-window.sh"'
marker_end = "  out=$(SUPERVISOR_STATE=\"$STATE\" SUPERVISOR_STATUS=\"$STATUS\" bash \"$copy\" \"$root\" 2>&1)"
start = text.index(marker_start)
end = text.index(marker_end)
assert start != -1 and end != -1, "copy-step block not found -- watchdog.sh shape changed"
replacement = (
    '  cp "$HERE/poller-window.sh" "$copy_dir/poller-window.sh" 2>/dev/null || '
    '{ rm -rf "$copy_dir" 2>/dev/null; return $rc; }\n  '
    'cp "$HERE/session-defaults.sh" "$copy_dir/session-defaults.sh" 2>/dev/null || '
    '{ rm -rf "$copy_dir" 2>/dev/null; return $rc; }\n  '
)
open(path, "w").write(text[:start] + replacement + text[end:])
PY
if [ "$patch_rc" -ne 0 ]; then
  say_bad "setup: patched \$LIVE's watchdog.sh back to the pre-#57 copy step" "patch failed with exit $patch_rc"
else
  say_ok "setup: patched \$LIVE's watchdog.sh back to the pre-#57 copy step"

  echo third >"$SRC/marker.txt"
  git -C "$SRC" add -A >/dev/null 2>&1
  git -C "$SRC" commit -q -m third
  git -C "$SRC" push -q origin main

  mut_pid=$(cat "$PID_FILE" 2>/dev/null)
  seed_poller_status "$live_sha1" "$mut_pid"
  rm -f "$FLAG"
  wd_tick "$A/t1b"
  # A bounded wait, same shape as await_replacement, but here NO replacement
  # is the expected (RED) outcome -- the old poller is flagged to stop, but
  # with poller-recover.sh missing from copy_dir nothing ever relaunches it.
  if new_after_mut=$(await_replacement "$mut_pid"); then
    say_bad "mutation confirmed: the pre-#57 copy step leaves no different live pid within the bound (RED without the fix)" \
      "old=$mut_pid but a new live pid $new_after_mut appeared -- the mutated copy step relaunched anyway, so test 1 is not pinned to this fix"
  else
    say_ok "mutation confirmed: the pre-#57 copy step leaves no different live pid within the bound (RED without the fix)"
  fi
fi
# Restore the real, fixed watchdog.sh: discard the in-place patch above by
# resetting to the tip of origin/main, which was never mutated -- only
# $LIVE's working tree was.
git -C "$LIVE" fetch -q origin main
git -C "$LIVE" reset -q --hard origin/main
chmod +x "$LIVE/scripts/supervisor/watchdog.sh"

# --- 2. two restart requests in quick succession end with exactly one live
#        poller -- not zero (the pre-#57 bug once the 180s backstop is out of
#        the picture) and not two (#18's failure, worse). ------------------
cur_pid=$(cat "$PID_FILE" 2>/dev/null)
seed_poller_status "stale-second-request" "$cur_pid"
rm -f "$FLAG"
wd_tick "$A/t2a"
wd_tick "$A/t2b"
newer_pid=$(await_replacement "$cur_pid" || true)
# A replacement pid can appear and then be superseded by a second, still
# in-flight relaunch a moment later (two ticks each started their own
# background waiter) -- checking the instant the first replacement shows up
# races that settling. Poll until the counts hold steady twice in a row
# instead of sampling once.
settled=0
if [ -n "$newer_pid" ]; then
  stable_reads=0
  deadline=$((SECONDS + 8))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if [ "$(window_count)" = "1" ] && [ "$(live_poller_count)" = "1" ]; then
      stable_reads=$((stable_reads + 1))
      [ "$stable_reads" -ge 2 ] && { settled=1; break; }
    else
      stable_reads=0
    fi
    sleep 0.3
  done
fi
if [ "$settled" -eq 1 ]; then
  say_ok "two quick restart requests end with exactly one live poller"
else
  say_bad "two quick restart requests end with exactly one live poller" \
    "replacement=${newer_pid:-none} windows=$(window_count) live_processes=$(live_poller_count) pids=$(cat "$PID_HISTORY" 2>/dev/null | tr '\n' ' ')"
fi

# --- 3. the deliberate-stop path still does not page, and the watchdog does
#        not double-page across its own backstop poller-recover.sh call and
#        advance-live.sh's prompt relaunch racing in the same tick. --------
if [ -z "$(up_calls)" ]; then
  say_ok "the deliberate-stop / prompt-relaunch path never pages Jon"
else
  say_bad "the deliberate-stop / prompt-relaunch path never pages Jon" "paged: $(up_calls)"
fi

# cleanup_all runs via the EXIT trap above -- no duplicate teardown here.
echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
