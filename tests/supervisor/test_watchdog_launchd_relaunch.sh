#!/bin/bash
# agent-supervisor#75: eight prompt-relaunch waiters started, zero reached any
# terminal log line. tests/supervisor/test_watchdog_poller_copy.sh (#57) drives
# the real watchdog.sh exit trap, but as a plain child of the test's own bash
# process -- never through the LaunchAgent that runs it in production
# (com.jonhill.supervisor-watchdog.plist). That gap is why #57's suite could
# not see #75: launchd's default AbandonProcessGroup=false sends SIGTERM, then
# SIGKILL, to anything left in a job's process group once the job's main
# process exits -- and advance-live.sh's waiter is exactly that, a background
# job still running (up to INBOX_POLL_RELAUNCH_WAIT_SECONDS) after
# "bash $copy" (and so watchdog.sh) has already returned. A plain-bash parent
# never sends that signal; only launchd does. These tests drive watchdog.sh
# through a real, throwaway LaunchAgent -- unique label, own plist, unloaded
# and deleted on every exit path -- never the live estate's own job.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
pass=0; fail=0
say_ok()  { echo "  ok   $1"; pass=$((pass+1)); }
say_bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }

echo "watchdog.sh: agent-supervisor#75 -- the prompt relaunch waiter under a real LaunchAgent"

if [ "$(uname -s)" != "Darwin" ] || ! command -v launchctl >/dev/null 2>&1; then
  echo "  SKIP launchd is macOS-only (uname=$(uname -s 2>/dev/null))"
  echo "  0 passed, 0 failed"
  exit 0
fi
if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP no tmux on PATH"
  echo "  0 passed, 0 failed"
  exit 0
fi

# shellcheck source=../../scripts/supervisor/tmux-isolation.sh
source "$SUP/tmux-isolation.sh"
# shellcheck source=./lib/reap-verified.sh
source "$HERE/lib/reap-verified.sh"
# Explicitly under /tmp, not "${TMPDIR:-/tmp}": the throwaway LaunchAgent job
# below must be able to actually exec its script from wherever this points,
# and a per-user $TMPDIR is not guaranteed launchd-executable in every
# environment this suite runs in (a real, reproduced failure while writing
# this test -- the job got a pid and no log line, mirroring #75's own zero-
# outcome shape, until moved here). Plain /tmp is.
RT=$(mktemp -d "/tmp/watchdog-75-tmux.XXXXXX")
OLD_TMUX="${TMUX-}"
OLD_TMUX_TMPDIR="${TMUX_TMPDIR-}"
unset TMUX
export TMUX_TMPDIR="$RT"

UID_N=$(id -u)
LABEL="com.jonhill.test-relaunch-75-$$"
DOMAIN="gui/$UID_N"
JOB="$DOMAIN/$LABEL"
PLIST_PATH=""

# agent-supervisor#104: `pkill -KILL -f "$STAND_IN"` (the previous shape here)
# is fire-and-forget -- it never confirms the process is actually gone and
# never reports if it is not. It happened to be safe (STAND_IN is a unique
# path under this test's own $A), but it gave no evidence either way, and
# `trap cleanup EXIT` alone -- with no INT/TERM registered -- means an
# untrapped SIGTERM landing while bash is between commands (not blocked in a
# `wait`) skips this function entirely (see inbox-poll.sh's own header on the
# same gap, agent-dotfiles#187). PID_HISTORY (recorded by the stand-in itself
# at spawn) lets cleanup reap by pid, verified, and report if it can't.
cleanup() {
  launchctl bootout "$JOB" >/dev/null 2>&1
  [ -n "$PLIST_PATH" ] && rm -f "$PLIST_PATH"
  [ -n "${SESSION:-}" ] && tmux kill-session -t "$SESSION" >/dev/null 2>&1
  [ -n "${PID_HISTORY:-}" ] && [ -n "${A:-}" ] && reap_pid_history_verified "$PID_HISTORY" "$A" 5
  [ -n "${A:-}" ] && [ -n "${SRC:-}" ] && [ -n "${LIVE:-}" ] && {
    git -C "$SRC" worktree remove --force "$LIVE" >/dev/null 2>&1
    git -C "$SRC" worktree prune >/dev/null 2>&1
  }
  [ -n "${A:-}" ] && rm -rf "$A"
  [ -n "${NOTIFY_DIR:-}" ] && rm -rf "$NOTIFY_DIR"
  rm -rf "$RT"
  [ -n "$OLD_TMUX_TMPDIR" ] && export TMUX_TMPDIR="$OLD_TMUX_TMPDIR" || unset TMUX_TMPDIR
  [ -n "$OLD_TMUX" ] && export TMUX="$OLD_TMUX"
}
trap cleanup EXIT INT TERM

if ! assert_isolated_tmux; then
  say_bad "setup: isolated tmux socket for #75" "TMUX_TMPDIR=$TMUX_TMPDIR"
  echo "  $pass passed, $fail failed"
  exit 1
fi
say_ok "setup: isolated tmux socket for #75"

# The throwaway job's own tmux calls (inside watchdog.sh) must land on this
# same private socket -- TMUX_TMPDIR is passed through the plist's own
# EnvironmentVariables below, and TMUX is never added to it, matching the
# unset above (a launchd job does not inherit this script's exported env).
SESSION="watchdog-75-$$"

# --- a real origin + a real LIVE worktree, never the estate's own ----------
# Same /tmp-not-$TMPDIR reasoning as RT above: watchdog.sh itself lives here.
A=$(mktemp -d "/tmp/watchdog-75-state.XXXXXX")
git init -q --bare "$A/origin.git"
git clone -q "$A/origin.git" "$A/src" 2>/dev/null
SRC="$A/src"
git -C "$SRC" config user.email t@e.com; git -C "$SRC" config user.name T
git -C "$SRC" checkout -q -b main
mkdir -p "$SRC/scripts/supervisor"
# agent-supervisor#163: lanes.sh (and its own input-box.sh dependency) added
# so watchdog.sh's never-busy check finds a real lanes.sh beside it here, the
# same as a real live worktree always does -- without it, this fixture's
# "lanes.sh is missing beside this watchdog" was never the scenario #75's
# relaunch-waiter path is about, and #163's new fail-streak escalation
# started paging on it, which is what actually broke "pages nobody" below.
for f in watchdog.sh advance-live.sh poller-window.sh poller-recover.sh session-defaults.sh \
         sleepcheck.py watchdog_notify.py loop-tick.md harness-registry.sh lanes.sh input-box.sh; do
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

tmux new-session -d -s "$SESSION" -x 200 -y 50 \
  -- bash -c 'printf "  ⏵⏵ bypass permissions on \xc2\xb7 esc to interrupt \xc2\xb7 \xe2\x86\x90 1 agent\n"; sleep 300'

STAND_IN="$A/inbox-poll.sh"
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
while :; do
  if [ -f "$FLAG" ]; then
    rm -f "$FLAG"
    write_status stopped
    exit 0
  fi
  sleep 0.1
done
EOF
chmod +x "$STAND_IN"

STATE="$A/state"; mkdir -p "$STATE" "$STATE/transcripts"
FLAG="$STATE/.inbox-poll-restart-requested"
STATUS_FILE="$STATE/inbox-poll.status"
PID_FILE="$A/pid"
PID_HISTORY="$A/pids"
launch_cmd() { # launch_cmd <sha>
  printf "INBOX_POLL_STATUS='%s' INBOX_POLL_RESTART_FLAG='%s' POLLER_PID_FILE='%s' POLLER_PID_HISTORY='%s' POLLER_STATUS_SHA='%s' exec '%s'" \
    "$STATUS_FILE" "$FLAG" "$PID_FILE" "$PID_HISTORY" "$1" "$STAND_IN"
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

echo second >"$SRC/marker.txt"
git -C "$SRC" add -A >/dev/null 2>&1
git -C "$SRC" commit -q -m second
git -C "$SRC" push -q origin main
live_sha1=$(git -C "$SRC" rev-parse HEAD)
CUR_LIVE_SHA="$live_sha1"

seed_poller_status() { # seed_poller_status <sha> <pid>
  {
    printf 'checked: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf 'sha:     %s\n' "$1"
    printf 'state:   ok\n'
    printf 'pid:     %s\n' "$2"
  } >"$STATUS_FILE"
}

# write_plist <tick-tag> -- overwrites the throwaway job's plist so its own
# EnvironmentVariables carry the same overrides the #57 suite passes directly:
# only SUPERVISOR_STATE and SUPERVISOR_PANE/LANES_SESSION are pinned to this
# test's own scratch dir and isolated tmux socket -- watchdog.sh derives every
# other path (LOG, STATUS, STAMP, HISTORY) from SUPERVISOR_STATE the same way
# it would from a real EnvironmentVariables dict in production, so this is the
# real job shape, not a stand-in for it.
write_plist() {
  PLIST_PATH="$A/$LABEL.plist"
  cat >"$PLIST_PATH" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key>
  <array><string>$LIVE/scripts/supervisor/watchdog.sh</string></array>
  <key>RunAtLoad</key><false/>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key><string>$PATH</string>
    <key>HOME</key><string>$HOME</string>
    <key>SUPERVISOR_LIVE</key><string>$LIVE</string>
    <key>SUPERVISOR_PANE</key><string>$SESSION:0.1</string>
    <key>LANES_SESSION</key><string>$SESSION</string>
    <key>TMUX_TMPDIR</key><string>${TMUX_TMPDIR:-/tmp}</string>
    <key>SUPERVISOR_STATE</key><string>$STATE</string>
    <key>NOTIFY_ENV</key><string>$STATE/none.env</string>
    <key>NOTIFY_SCRIPT</key><string>$UP</string>
    <key>SLEEPCHECK_DIR</key><string>$STATE/transcripts</string>
    <key>POLLER_LAUNCH_CMD</key><string>$(launch_cmd "$CUR_LIVE_SHA")</string>
    <key>POLLER_RECOVER_LOCK</key><string>$STATE/.poller-recover.lock</string>
    <key>POLLER_RECOVER_LOG</key><string>$STATE/poller-recover.log</string>
    <key>INBOX_POLL_RELAUNCH_WAIT_SECONDS</key><string>8</string>
  </dict>
  <key>StandardOutPath</key><string>$A/launchd.stdout.log</string>
  <key>StandardErrorPath</key><string>$A/launchd.stderr.log</string>
</dict>
</plist>
PLIST
}

NOTIFY_DIR=$(mktemp -d)
cat >"$NOTIFY_DIR/up.sh" <<'EOF'
#!/bin/bash
echo "$1|$2" >> "$0.calls"
EOF
chmod +x "$NOTIFY_DIR/up.sh"
UP="$NOTIFY_DIR/up.sh"
up_calls() { cat "$UP.calls" 2>/dev/null; }

# wd_tick_launchd -- one real watchdog.sh tick, run by real launchd through a
# throwaway job, never through this test's own bash. Every tmux call inside
# watchdog.sh reaches this test's private socket via TMUX_TMPDIR (set in the
# plist below), but TMUX -- unset here, never exported to the job -- keeps
# watchdog.sh from believing it is inside any tmux client itself. The job is
# torn down (bootout) after every tick so state does not leak between calls.
# A pid reported by `launchctl print` right after kickstart is not proof the
# job actually ran watchdog.sh to completion -- it can be transient. The real
# evidence is watchdog.sh's own STATUS file (SUPERVISOR_STATUS, defaulted from
# SUPERVISOR_STATE, always written near the top of every tick): if its mtime
# has not moved after a kickstart, this tick did not run, and bootstrap +
# kickstart is retried rather than trusted on a single attempt.
status_mtime() { stat -f %m "$STATE/watchdog.status" 2>/dev/null || echo 0; }
wd_tick_launchd() {
  write_plist
  local before after attempt ran=0
  before=$(status_mtime)
  for attempt in 1 2 3 4 5 6; do
    launchctl bootout "$JOB" >/dev/null 2>&1
    launchctl bootstrap "$DOMAIN" "$PLIST_PATH" >/dev/null 2>&1
    launchctl kickstart -k "$JOB" >/dev/null 2>&1
    local deadline=$((SECONDS + 4))
    while [ "$SECONDS" -lt "$deadline" ]; do
      after=$(status_mtime)
      [ "$after" != "$before" ] && [ "$after" -gt 0 ] && { ran=1; break; }
      sleep 0.15
    done
    [ "$ran" -eq 1 ] && break
  done
  if [ "$ran" -eq 1 ]; then
    # The tick itself (proven above) has finished, but its background waiter
    # can still be running up to INBOX_POLL_RELAUNCH_WAIT_SECONDS later --
    # give it the same window plus a margin before tearing the job down.
    sleep "$(( ${INBOX_POLL_RELAUNCH_WAIT_SECONDS:-8} + 2 ))"
  fi
  launchctl bootout "$JOB" >/dev/null 2>&1
}

waiter_started_count() { grep -c "POLLER-RESTART-REQUESTED:.*waiter started" "$STATE/advance-live.log" 2>/dev/null || echo 0; }
terminal_count() {
  local n
  n=$(grep -cE "POLLER-PROMPT-RELAUNCH:|POLLER-PROMPT-RELAUNCH-TIMEOUT:|POLLER-PROMPT-RELAUNCH-FAILED |POLLER-PROMPT-RELAUNCH-KILLED:" \
    "$STATE/advance-live.log" 2>/dev/null) || n=0
  printf '%s\n' "$n"
}

# --- 1. drive the relaunch through a real LaunchAgent -----------------------
seed_poller_status "$live_sha0" "$old_pid"
rm -f "$FLAG"
wd_tick_launchd
new_pid=$(await_replacement "$old_pid" || true)
if [ -n "$new_pid" ]; then
  say_ok "watchdog.sh under a real LaunchAgent relaunches the poller and a different live pid appears within seconds"
else
  say_bad "watchdog.sh under a real LaunchAgent relaunches the poller and a different live pid appears within seconds" \
    "old=$old_pid current=$(cat "$PID_FILE" 2>/dev/null) advance.log=$(cat "$STATE/advance-live.log" 2>/dev/null | tr '\n' ' ') recover.log=$(cat "$STATE/poller-recover.log" 2>/dev/null | tr '\n' ' ')"
fi
[ "$(window_count)" = "1" ] && [ "$(live_poller_count)" = "1" ] \
  && say_ok "exactly one live poller and one inbox-poll window after the relaunch" \
  || say_bad "exactly one live poller and one inbox-poll window after the relaunch" \
    "windows=$(window_count) live_processes=$(live_poller_count) pids=$(cat "$PID_HISTORY" 2>/dev/null | tr '\n' ' ')"

started1=$(waiter_started_count)
terminal1=$(terminal_count)
[ "$started1" -gt 0 ] && [ "$started1" = "$terminal1" ] \
  && say_ok "every waiter started reaches exactly one terminal log line (started=$started1 terminal=$terminal1)" \
  || say_bad "every waiter started reaches exactly one terminal log line" \
    "started=$started1 terminal=$terminal1 -- a gap here is a waiter that vanished with zero outcome, #75's own symptom"

# --- 2. two restart requests in quick succession end with exactly one live
#        poller -- not zero, not #18's two. ----------------------------------
cur_pid=$(cat "$PID_FILE" 2>/dev/null)
seed_poller_status "stale-second-request" "$cur_pid"
rm -f "$FLAG"
wd_tick_launchd
wd_tick_launchd
newer_pid=$(await_replacement "$cur_pid" || true)
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
  say_ok "two quick restart requests through real launchd end with exactly one live poller"
else
  say_bad "two quick restart requests through real launchd end with exactly one live poller" \
    "replacement=${newer_pid:-none} windows=$(window_count) live_processes=$(live_poller_count) pids=$(cat "$PID_HISTORY" 2>/dev/null | tr '\n' ' ')"
fi

if [ -z "$(up_calls)" ]; then say_ok "the launchd-driven relaunch path never pages Jon"
else say_bad "the launchd-driven relaunch path never pages Jon" "paged: $(up_calls)"; fi

# --- 3. mutation: revert the fix (drop `set -m` AND the SIGTERM trap, the
#        true pre-#75 shape -- keeping the trap alone would still log a
#        KILLED line and hide the zero-outcome gap this is meant to prove),
#        confirm the ORIGINAL bug reproduces under the same real LaunchAgent.
#        If this does not go red, tests 1 and 2 above are not actually pinned
#        to the fix.
#
# Patched into $SRC and pushed, NOT edited in place inside $LIVE: advance-
# live.sh's own dirty-worktree guard (agent-supervisor#75's own test run hit
# this first) refuses to advance $LIVE at all once it has an uncommitted
# local edit, which would make every assertion below vacuously true for the
# wrong reason. A real commit, advanced into $LIVE the ordinary way, keeps
# the tree clean.
patch_rc=0
python3 - "$SRC/scripts/supervisor/advance-live.sh" <<'PY' || patch_rc=$?
import sys
path = sys.argv[1]
text = open(path).read()
marker = '''  set -m
  (
    trap 'log "POLLER-PROMPT-RELAUNCH-KILLED: waiter for pane $pane received SIGTERM before finishing; watchdog poller-recover.sh remains the backstop"; exit 143' TERM
    deadline='''
assert marker in text, "waiter background block not found -- advance-live.sh shape changed"
assert text.count(marker) == 1, "waiter background block not unique -- advance-live.sh shape changed"
text = text.replace(marker, "  (\n    deadline=", 1)
open(path, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  say_bad "setup: patched \$SRC's advance-live.sh back to the pre-#75 unguarded background" "patch failed with exit $patch_rc"
else
  say_ok "setup: patched \$SRC's advance-live.sh back to the pre-#75 unguarded background"
  git -C "$SRC" add -A >/dev/null 2>&1
  git -C "$SRC" commit -q -m "mutation: pre-#75 unguarded waiter background"
  git -C "$SRC" push -q origin main
  mut_target=$(git -C "$SRC" rev-parse HEAD)

  # Advance $LIVE to the mutated commit first -- this tick still copies and
  # runs the OLD (fixed) advance-live.sh, since watchdog.sh's copy step
  # snapshots $HERE before this checkout happens, so it is a clean, ordinary
  # advance and proves nothing about the mutation yet.
  cur_before_mut=$(cat "$PID_FILE" 2>/dev/null)
  seed_poller_status "$cur_before_mut" "$cur_before_mut"
  rm -f "$FLAG"
  wd_tick_launchd
  advanced_to_mut=$([ "$(git -C "$LIVE" rev-parse HEAD 2>/dev/null)" = "$mut_target" ] && echo 1 || echo 0)

  if [ "$advanced_to_mut" != "1" ]; then
    say_bad "setup: \$LIVE advanced to the mutated commit" \
      "HEAD=$(git -C "$LIVE" rev-parse HEAD 2>/dev/null) want=$mut_target"
  else
    say_ok "setup: \$LIVE advanced to the mutated commit"
    # Now $HERE is the mutated advance-live.sh. A second, separate tick's
    # copy step picks it up -- this is the one that actually exercises the
    # pre-#75 shape under the real LaunchAgent.
    mut_pid=$(cat "$PID_FILE" 2>/dev/null)
    seed_poller_status "stale-for-mutation-test" "$mut_pid"
    rm -f "$FLAG"
    before_started=$(waiter_started_count)
    before_terminal=$(terminal_count)
    wd_tick_launchd
    mut_new=$(await_replacement "$mut_pid" || true)
    after_started=$(waiter_started_count)
    after_terminal=$(terminal_count)
    gap=$(( (after_started - before_started) - (after_terminal - before_terminal) ))
    if [ -z "$mut_new" ] && [ "$gap" -gt 0 ]; then
      say_ok "mutation confirmed: without \`set -m\` and the SIGTERM trap, the same real LaunchAgent kills the waiter -- no replacement pid, a waiter with zero terminal outcome (RED without the fix)"
    else
      say_bad "mutation confirmed: without \`set -m\` and the SIGTERM trap, the same real LaunchAgent kills the waiter -- no replacement pid, a waiter with zero terminal outcome (RED without the fix)" \
        "replacement=${mut_new:-none} started_delta=$((after_started - before_started)) terminal_delta=$((after_terminal - before_terminal)) -- the mutation should have reproduced #75, not been absorbed by it"
    fi
  fi
fi

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
