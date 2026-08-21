#!/bin/bash
# agent-supervisor#450: prompt_poller_relaunch (advance-live.sh) writes
# INBOX_POLL_RESTART_FLAG before backgrounding a waiter that, once the old
# poller pid is confirmed gone, hands off to poller-recover.sh to launch a
# replacement. The ordinary way that flag gets consumed is the OLD poller
# itself: seeing it, removing it, THEN exiting (inbox-poll.sh's own
# "restart flag seen" path).
#
# That path assumes the old poller is ALIVE long enough to see the flag. If
# it is already dead when the flag is written -- a crash, or a leftover
# corpse from an earlier, unrelated restart request -- the waiter's own
# `while kill -0 "$poller_pid"` loop runs ZERO iterations and nothing ever
# consumes the flag. It survives into the brand-new poller poller-recover.sh
# is about to launch, which reads it on its own first loop tick as a request
# meant for ITSELF and exits within ~0.1s of starting -- a live poller that
# was provably started (it wrote its own pid) and just as provably dead by
# the next check. This is the exact shape
# tests/supervisor/test_watchdog_poller_copy.sh's "two quick restart
# requests end with exactly one live poller" caught in CI (agent-supervisor
# CI run against #450): a named replacement pid, zero live processes.
#
# This test isolates that one behavior directly against the real
# prompt_poller_relaunch function (extracted from advance-live.sh, not
# reimplemented) instead of driving it through watchdog.sh's full copy path
# and a real git worktree -- test_watchdog_poller_copy.sh already covers
# that end-to-end integration; asynchronous waiters left over from ITS
# earlier steps made this specific narrow scenario unreliable to pin down
# there (a residual waiter from a prior step can still fire seconds into a
# later step and produce a false failure indistinguishable from this bug).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
pass=0; fail=0
say_ok()  { echo "  ok   $1"; pass=$((pass+1)); }
say_bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }

echo "advance-live.sh: agent-supervisor#450 -- a restart flag written for an already-dead poller must not kill its replacement"

if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP no tmux on PATH"
  echo "  0 passed, 0 failed"
  exit 0
fi

# shellcheck source=../../scripts/supervisor/tmux-isolation.sh
source "$SUP/tmux-isolation.sh"
# shellcheck source=./lib/reap-verified.sh
source "$HERE/lib/reap-verified.sh"
RT=$(mktemp -d "${TMPDIR:-/tmp}/advance-live-450-tmux.XXXXXX")
OLD_TMUX="${TMUX-}"
OLD_TMUX_TMPDIR="${TMUX_TMPDIR-}"
unset TMUX
export TMUX_TMPDIR="$RT"
SESSION="advance-live-450-$$"

cleanup_all() {
  [ -n "${PID_HISTORY:-}" ] && reap_pid_history_verified "$PID_HISTORY" "$RT" 5
  unset TMUX; export TMUX_TMPDIR="$RT"
  [ -n "${SESSION:-}" ] && tmux kill-session -t "$SESSION" >/dev/null 2>&1
  rm -rf "${A:-}" "$RT"
  [ "$OLD_TMUX_TMPDIR" ] && export TMUX_TMPDIR="$OLD_TMUX_TMPDIR" || unset TMUX_TMPDIR
  [ "$OLD_TMUX" ] && export TMUX="$OLD_TMUX"
}
trap cleanup_all EXIT INT TERM

if ! assert_isolated_tmux; then
  say_bad "setup: isolated tmux socket for #450" "TMUX_TMPDIR=$TMUX_TMPDIR"
  echo "  $pass passed, $fail failed"
  exit 1
fi
say_ok "setup: isolated tmux socket for #450"

A=$(mktemp -d)
STATE="$A/state"; mkdir -p "$STATE"
LOG="$STATE/advance-live.log"
LIVE="$A/live-placeholder"; mkdir -p "$LIVE"   # only used by poller-recover.sh's zero-window branch, never reached here
INBOX_POLL_STATUS_PATH="$STATE/inbox-poll.status"
INBOX_POLL_RESTART_FLAG="$STATE/.inbox-poll-restart-requested"
INBOX_POLL_SESSION="$SESSION"
INBOX_POLL_RELAUNCH_WAIT_SECONDS=8
POLLER_WINDOW_NAME="inbox-poll"

tmux new-session -d -s "$SESSION" -x 200 -y 50 -- sleep 300

STAND_IN="$RT/inbox-poll.sh"
cat >"$STAND_IN" <<'EOF'
#!/bin/bash
set -u
FLAG="${INBOX_POLL_RESTART_FLAG:?}"
PID_FILE="${POLLER_PID_FILE:?}"
PID_HISTORY="${POLLER_PID_HISTORY:?}"
echo "$$" >"$PID_FILE"
echo "$$" >>"$PID_HISTORY"
while :; do
  if [ -f "$FLAG" ]; then
    rm -f "$FLAG"
    exit 0
  fi
  sleep 0.1
done
EOF
chmod +x "$STAND_IN"

PID_FILE="$A/pid"
PID_HISTORY="$A/pids"
LAUNCH_CMD="INBOX_POLL_RESTART_FLAG='$INBOX_POLL_RESTART_FLAG' POLLER_PID_FILE='$PID_FILE' POLLER_PID_HISTORY='$PID_HISTORY' exec '$STAND_IN'"
# Must be exported: poller-recover.sh is invoked as a separate process by
# prompt_poller_relaunch below (not passed this inline like SUPERVISOR_STATE
# etc.), so it only sees this if it is in the environment, not just a shell
# variable in this script.
export POLLER_LAUNCH_CMD="$LAUNCH_CMD"

tmux new-window -t "$SESSION" -n "$POLLER_WINDOW_NAME" -d -- "$LAUNCH_CMD"
tmux set-window-option -t "$SESSION:$POLLER_WINDOW_NAME" remain-on-exit on >/dev/null 2>&1

wait_for_pid_file() {
  local deadline=$((SECONDS + 8))
  while [ ! -s "$PID_FILE" ] && [ "$SECONDS" -lt "$deadline" ]; do sleep 0.1; done
  [ -s "$PID_FILE" ]
}
pid_alive() { [ -n "${1:-}" ] && kill -0 "$1" 2>/dev/null; }
window_count() { tmux list-windows -t "$SESSION" -F '#{window_name}' 2>/dev/null | grep -cFx "$POLLER_WINDOW_NAME"; }
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
  say_ok "setup: the stand-in poller is running"
else
  old_pid=""
  say_bad "setup: the stand-in poller is running" "no pid file"
fi

# Kill it directly (SIGKILL) -- an independent death, NOT via the restart
# flag, so nothing consumes any flag on its way out. This is the exact
# precondition prompt_poller_relaunch's own `while kill -0 "$poller_pid"`
# loop treats as "already gone, zero iterations, proceed immediately".
if [ -n "$old_pid" ]; then
  kill -9 "$old_pid" 2>/dev/null
  deadline=$((SECONDS + 5))
  while pid_alive "$old_pid" && [ "$SECONDS" -lt "$deadline" ]; do sleep 0.1; done
fi
if [ -n "$old_pid" ] && ! pid_alive "$old_pid"; then
  say_ok "setup: the old poller is confirmed dead (SIGKILLed, not via the flag)"
else
  say_bad "setup: the old poller is confirmed dead (SIGKILLed, not via the flag)" \
    "old_pid=${old_pid:-none} still alive after SIGKILL"
fi

# Extract the real prompt_poller_relaunch (and its log() dependency) from
# advance-live.sh rather than reimplementing it -- this test must exercise
# the actual fix, not a paraphrase of it that could drift from the real
# function and pass or fail for the wrong reason.
ADVANCE="$SUP/advance-live.sh"
log_fn=$(awk '/^log\(\) \{/{print; exit}' "$ADVANCE")
relaunch_fn=$(awk '/^prompt_poller_relaunch\(\) \{/{flag=1} flag{print} flag && /^}/{exit}' "$ADVANCE")
if [ -z "$log_fn" ] || [ -z "$relaunch_fn" ]; then
  say_bad "setup: extracted log() and prompt_poller_relaunch() from advance-live.sh" \
    "advance-live.sh's shape changed -- log_fn=${#log_fn} chars, relaunch_fn=${#relaunch_fn} chars"
  echo "  $pass passed, $fail failed"
  exit 1
fi
say_ok "setup: extracted log() and prompt_poller_relaunch() from advance-live.sh"
eval "$log_fn"
eval "$relaunch_fn"

pane="$SESSION:$POLLER_WINDOW_NAME"
mkdir -p "$(dirname "$INBOX_POLL_RESTART_FLAG")"
: >"$INBOX_POLL_RESTART_FLAG"
if [ -f "$INBOX_POLL_RESTART_FLAG" ]; then
  say_ok "setup: a restart flag is on disk, written for the now-dead old poller"
else
  say_bad "setup: a restart flag is on disk, written for the now-dead old poller" "write failed"
fi

STATE="$STATE" HERE="$SUP" prompt_poller_relaunch "$pane" "old-sha" "live-sha" "$old_pid"

replacement=$(await_replacement "$old_pid" || true)
if [ -n "$replacement" ]; then
  say_ok "prompt_poller_relaunch still produces a replacement for an already-dead old poller"
else
  say_bad "prompt_poller_relaunch still produces a replacement for an already-dead old poller" \
    "old=$old_pid current=$(cat "$PID_FILE" 2>/dev/null) log=$(cat "$LOG" 2>/dev/null | tr '\n' ' ')"
fi

still_alive=1
if [ -n "$replacement" ]; then
  checks=0
  while [ "$checks" -lt 15 ]; do
    cur=$(cat "$PID_FILE" 2>/dev/null)
    if [ "$cur" != "$replacement" ] || ! pid_alive "$replacement" || [ "$(live_poller_count)" != "1" ]; then
      still_alive=0
      break
    fi
    checks=$((checks + 1))
    sleep 0.2
  done
else
  still_alive=0
fi
if [ "$still_alive" -eq 1 ]; then
  say_ok "that replacement stays the sole live poller for 3s straight -- it did not self-terminate on the stale flag"
else
  say_bad "that replacement stays the sole live poller for 3s straight -- it did not self-terminate on the stale flag" \
    "replacement=${replacement:-none} current=$(cat "$PID_FILE" 2>/dev/null) live_processes=$(live_poller_count) flag=$([ -f "$INBOX_POLL_RESTART_FLAG" ] && echo present || echo absent) pids=$(cat "$PID_HISTORY" 2>/dev/null | tr '\n' ' ')"
fi

if [ ! -f "$INBOX_POLL_RESTART_FLAG" ]; then
  say_ok "the stale flag does not outlive the replacement it would have killed"
else
  say_bad "the stale flag does not outlive the replacement it would have killed" "still present at $INBOX_POLL_RESTART_FLAG"
fi

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
