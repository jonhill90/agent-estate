#!/bin/bash
# agent-supervisor#154: the poller should be a managed service, not a tmux
# window -- because its fragility was a consequence of that hosting, not of
# its own logic (see the issue for the measured incident list: duplicate
# tmux *windows* acking the same offset, a restart loop tied to a *pane*
# relaunch, a dead *pane* leaving the channel silently down, a cleanup that
# killed windows *by index*). `watchdog.sh` is this repo's own
# counter-example: it runs from a LaunchAgent, outside tmux, and survives
# `tmux kill-server` by construction.
#
# This drives the REAL inbox-poll.sh through a real, throwaway LaunchAgent --
# unique label, own plist, unloaded and deleted on every exit path, never the
# live estate's own job -- the same posture
# test_watchdog_launchd_relaunch.sh (#75) already established for
# watchdog.sh. Three things are proven, in order:
#
#   1. Single-instance: starting a second instance while the first holds the
#      lock is REFUSED OUTRIGHT (#154's own single-instance guarantee, not
#      merely detected after two pollers are already acking).
#   2. THE MUTATION CHECK, and the whole point of #154: `tmux kill-server` on
#      an ISOLATED, throwaway tmux socket (never the live server -- see
#      tmux-isolation.sh) while the poller runs. The poller keeps acking.
#      inbox-poll.sh itself makes no tmux call at all (grep it -- the only
#      tmux-touching piece of this pipeline is director-route.sh, stubbed
#      out below exactly as test_inbox_poll.sh already does for its own,
#      narrower suite), so this is not a near miss: there is nothing here
#      for the server's death to reach.
#   3. Restart-on-crash with no double-ack: `kill -9` the poller pid. The
#      LaunchAgent's KeepAlive relaunches it -- no tmux window to respawn
#      into, because there was never one. Every message the stub inbox.sh
#      offered is routed EXACTLY once across the crash and restart: the
#      stub's own position file is this test's stand-in for the real
#      Telegram offset file, and it is never rewound by a restart, the same
#      persistence-across-restart property the real offset file gets from
#      never being touched by anything but inbox.sh's own locked read-ack
#      cycle (see inbox.sh's header).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
pass=0; fail=0
say_ok()  { echo "  ok   $1"; pass=$((pass+1)); }
say_bad() { echo "  FAIL $1 -- $2"; fail=$((fail+1)); }

echo "inbox-poll.sh: agent-supervisor#154 -- poller as a service"

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
. "$SUP/tmux-isolation.sh"
# shellcheck source=./lib/reap-verified.sh
. "$HERE/lib/reap-verified.sh"
RT=$(mktemp -d "/tmp/inbox-poll-154-tmux.XXXXXX")
OLD_TMUX="${TMUX-}"
OLD_TMUX_TMPDIR="${TMUX_TMPDIR-}"
unset TMUX
export TMUX_TMPDIR="$RT"

UID_N=$(id -u)
LABEL="com.jonhill.test-poller-154-$$"
DOMAIN="gui/$UID_N"
JOB="$DOMAIN/$LABEL"
PLIST_PATH=""
SESSION="inbox-poll-154-$$"

# agent-supervisor#104: this is the one suite that runs the REAL
# inbox-poll.sh under a real KeepAlive=true LaunchAgent -- if `launchctl
# bootout` is fired-and-forgotten and does not actually take (a real,
# reproduced launchd flake, not hypothetical), KeepAlive means launchd keeps
# relaunching this job forever, worse than a leaked plain process because
# nothing short of another bootout ever stops it. bootout is now verified: it
# must not still answer `launchctl print` before this function accepts it as
# done, and it retries a few times rather than trusting one attempt. The pid
# reap below is the backstop for the process itself, scoped to $LIVE (this
# test's own sandbox) so it can never touch a real poller running anywhere
# else -- same reap_pid_verified primitive #57 and #75 use, never `pkill -f`.
verify_job_gone() {
  local tries
  for tries in 1 2 3 4 5; do
    launchctl bootout "$JOB" >/dev/null 2>&1
    launchctl print "$JOB" >/dev/null 2>&1 || return 0
    sleep 0.3
  done
  echo "cleanup: COULD NOT CONFIRM $JOB is gone after 5 bootout attempts -- KeepAlive may still be relaunching it (agent-supervisor#104)" >&2
  return 1
}
cleanup() {
  verify_job_gone
  [ -n "$PLIST_PATH" ] && rm -f "$PLIST_PATH"
  [ -n "${SECOND_PID:-}" ] && kill -KILL "$SECOND_PID" 2>/dev/null
  [ -n "${pid1:-}" ] && [ -n "${LIVE:-}" ] && reap_pid_verified "$pid1" "$LIVE" 5
  [ -n "${pid2:-}" ] && [ -n "${LIVE:-}" ] && reap_pid_verified "$pid2" "$LIVE" 5
  tmux kill-server >/dev/null 2>&1
  [ -n "${A:-}" ] && rm -rf "$A"
  rm -rf "$RT"
  [ -n "$OLD_TMUX_TMPDIR" ] && export TMUX_TMPDIR="$OLD_TMUX_TMPDIR" || unset TMUX_TMPDIR
  [ -n "$OLD_TMUX" ] && export TMUX="$OLD_TMUX"
}
trap cleanup EXIT INT TERM

if ! assert_isolated_tmux; then
  say_bad "setup: isolated tmux socket for #154" "TMUX_TMPDIR=$TMUX_TMPDIR"
  echo "  $pass passed, $fail failed"
  exit 1
fi
say_ok "setup: isolated tmux socket for #154"

# A real tmux SERVER on the isolated socket -- something for kill-server
# below to actually destroy. If this were still the pre-#154 world, this
# session and its inbox-poll window would be where the poller lived.
tmux new-session -d -s "$SESSION" -x 80 -y 24 -- sleep 300
tmux has-session -t "$SESSION" 2>/dev/null && say_ok "setup: a real tmux server is up on the isolated socket" \
  || say_bad "setup: a real tmux server is up on the isolated socket" ""

A=$(mktemp -d "/tmp/inbox-poll-154-state.XXXXXX")
LIVE="$A/live"; mkdir -p "$LIVE/scripts/supervisor"
cp "$SUP/inbox-poll.sh" "$SUP/session-defaults.sh" "$LIVE/scripts/supervisor/"
chmod +x "$LIVE/scripts/supervisor/inbox-poll.sh"

# Stub inbox.sh: the same scripted-line-per-call shape test_inbox_poll.sh's
# own stub uses (reused in spirit, not literally, since that one lives
# inline in a different suite) -- each line is "ok:<msg>" or "ok:" (nothing
# new), consumed in order from a position file that IS this stub's offset,
# the same role the real Telegram offset file plays for the real inbox.sh.
# It is never reset by this script, by a crash, or by the LaunchAgent
# relaunching its process -- only by being consumed -- which is exactly the
# property that makes "never double-acked across a restart" checkable here.
FIXTURE="$A/fixture"
printf 'ok:one\nok:two\nok:\nok:three\n' >"$FIXTURE"
cat >"$LIVE/scripts/supervisor/inbox.sh" <<STUB
#!/bin/bash
SCRIPT="$FIXTURE"
POS="\$SCRIPT.pos"
pos=\$(cat "\$POS" 2>/dev/null || echo 0)
line=\$(sed -n "\$((pos+1))p" "\$SCRIPT")
if [ -z "\$line" ]; then
  sleep 0.2
  exit 0
fi
echo \$((pos+1)) > "\$POS"
msg="\${line#ok:}"
[ -z "\$msg" ] && exit 0
printf '%s\t[telegram %s from Jon] %s\n' "\$msg" "\$pos" "\$msg"
STUB
chmod +x "$LIVE/scripts/supervisor/inbox.sh"

ROUTE_LOG="$A/route.log"; : >"$ROUTE_LOG"
cat >"$LIVE/scripts/supervisor/director-route.sh" <<STUB
#!/bin/bash
[ "\$1" = "--flush" ] && exit 0
echo "\$1" >> "$ROUTE_LOG"
exit 0
STUB
chmod +x "$LIVE/scripts/supervisor/director-route.sh"

cat >"$LIVE/scripts/supervisor/notify.sh" <<STUB
#!/bin/bash
echo "\$*" >> "$A/notify.log"
exit 0
STUB
chmod +x "$LIVE/scripts/supervisor/notify.sh"

STATE="$A/state"; mkdir -p "$STATE"
STATUS="$STATE/inbox-poll.status"

write_plist() {
  PLIST_PATH="$A/$LABEL.plist"
  cat >"$PLIST_PATH" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key>
  <array>
    <string>$LIVE/scripts/supervisor/inbox-poll.sh</string>
    <string>$SESSION</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key><string>$PATH</string>
    <key>HOME</key><string>$HOME</string>
    <key>SUPERVISOR_STATE</key><string>$STATE</string>
    <key>INBOX_POLL_BACKOFF_BASE</key><string>0</string>
  </dict>
  <key>StandardOutPath</key><string>$A/launchd.stdout.log</string>
  <key>StandardErrorPath</key><string>$A/launchd.stderr.log</string>
</dict>
</plist>
PLIST
}
write_plist

status_state()   { grep -m1 '^state:' "$STATUS" 2>/dev/null | awk '{print $2}'; }
status_pid()     { grep -m1 '^pid:' "$STATUS" 2>/dev/null | awk '{print $2}'; }
status_checked() { grep -m1 '^checked:' "$STATUS" 2>/dev/null | awk '{print $2}'; }

await_status_ok() {  # await_status_ok [pid-to-exclude] -- wait for a fresh,
                     # live "ok" heartbeat, optionally from a DIFFERENT pid
                     # than the one given (used after a kill, to wait for
                     # the replacement rather than a stale read of the old
                     # process's last-written file).
  local exclude="${1:-}" deadline=$((SECONDS + 15))
  while [ "$SECONDS" -lt "$deadline" ]; do
    local st pid; st=$(status_state); pid=$(status_pid)
    if [ "$st" = "ok" ] && [ -n "$pid" ] && { [ -z "$exclude" ] || [ "$pid" != "$exclude" ]; } \
       && kill -0 "$pid" 2>/dev/null; then
      printf '%s\n' "$pid"; return 0
    fi
    sleep 0.2
  done
  return 1
}

launchctl bootout "$JOB" >/dev/null 2>&1
launchctl bootstrap "$DOMAIN" "$PLIST_PATH" >/dev/null 2>&1
launchctl kickstart -k "$JOB" >/dev/null 2>&1

pid1=$(await_status_ok || true)
if [ -n "$pid1" ]; then
  say_ok "the poller starts under a real LaunchAgent and reports state=ok"
else
  say_bad "the poller starts under a real LaunchAgent and reports state=ok" \
    "status=$(cat "$STATUS" 2>/dev/null | tr '\n' ' ') stderr=$(cat "$A/launchd.stderr.log" 2>/dev/null)"
fi

# --- 1. single-instance: a second start refuses while the lock is held -----
if [ -n "$pid1" ]; then
  out2=$(SUPERVISOR_STATE="$STATE" INBOX_POLL_BACKOFF_BASE=0 timeout 5 \
    bash "$LIVE/scripts/supervisor/inbox-poll.sh" "$SESSION" 2>&1); rc2=$?
  if [ "$rc2" -ne 0 ] && grep -qi 'refusing to start' <<<"$out2"; then
    say_ok "a second instance refuses to start while the lock is held (agent-supervisor#154)"
  else
    say_bad "a second instance refuses to start while the lock is held" "rc=$rc2 out=$out2"
  fi
  [ "$(status_pid)" = "$pid1" ] && say_ok "the original poller's pid/heartbeat is unaffected by the refused second start" \
    || say_bad "the original poller's pid/heartbeat is unaffected by the refused second start" "pid now $(status_pid), was $pid1"
fi

# --- 2. THE MUTATION CHECK: tmux kill-server, the poller keeps acking ------
checked_before=$(status_checked)
tmux kill-server >/dev/null 2>&1
sleep 0.3
tmux has-session -t "$SESSION" 2>/dev/null \
  && say_bad "setup: the isolated tmux server is actually gone after kill-server" "session '$SESSION' still answers" \
  || say_ok "setup: the isolated tmux server is actually gone after kill-server"

still_alive=0
[ -n "$pid1" ] && kill -0 "$pid1" 2>/dev/null && still_alive=1
[ "$still_alive" -eq 1 ] && say_ok "the poller process is still alive immediately after tmux kill-server" \
  || say_bad "the poller process is still alive immediately after tmux kill-server" "pid1=$pid1"

deadline=$((SECONDS + 10)); advanced=0
while [ "$SECONDS" -lt "$deadline" ]; do
  cur=$(status_checked)
  if [ -n "$cur" ] && [ "$cur" != "$checked_before" ] && [ "$(status_pid)" = "$pid1" ]; then
    advanced=1; break
  fi
  sleep 0.2
done
[ "$advanced" -eq 1 ] && say_ok "the poller keeps acking (heartbeat advances, same pid) after tmux kill-server -- the whole point of #154" \
  || say_bad "the poller keeps acking after tmux kill-server" \
    "checked before=$checked_before now=$(status_checked) pid before=$pid1 now=$(status_pid)"

# --- 3. restart-on-crash, no tmux window, no double-ack ---------------------
if [ -n "$pid1" ]; then
  kill -KILL "$pid1" 2>/dev/null
  pid2=$(await_status_ok "$pid1" || true)
  if [ -n "$pid2" ] && [ "$pid2" != "$pid1" ]; then
    say_ok "the LaunchAgent's KeepAlive relaunches a killed poller with a new pid -- no tmux window involved"
  else
    say_bad "the LaunchAgent's KeepAlive relaunches a killed poller with a new pid" \
      "pid1=$pid1 pid2=${pid2:-none} status=$(cat "$STATUS" 2>/dev/null | tr '\n' ' ')"
  fi

  # Give the replacement a moment to drain whatever the stub still has.
  deadline=$((SECONDS + 10))
  while [ "$SECONDS" -lt "$deadline" ] && [ "$(cat "$FIXTURE.pos" 2>/dev/null || echo 0)" -lt 4 ]; do
    sleep 0.2
  done

  routed=$(sort "$ROUTE_LOG" 2>/dev/null)
  want=$'one\nthree\ntwo'
  [ "$routed" = "$want" ] && say_ok "every message is routed exactly once across the crash and restart -- no double-ack, nothing dropped" \
    || say_bad "every message is routed exactly once across the crash and restart" \
      "route.log=$(cat "$ROUTE_LOG" 2>/dev/null | tr '\n' ' ') pos=$(cat "$FIXTURE.pos" 2>/dev/null)"
fi

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
