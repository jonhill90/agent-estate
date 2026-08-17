#!/bin/bash
# Make quota-watch.sh self-correcting when it has hung or died --
# agent-supervisor#276.
#
# THE GAP THIS CLOSES: quota-watch.sh is the estate's ONLY path back to work
# once a quota window closes (see that file's own header). On 2026-08-16 it
# hung for three hours inside an unbounded `quota.sh check` call and nothing
# restarted it -- there was no watcher for the watcher at all, PID-based or
# otherwise. #267 bounded `quota.sh`'s own codexbar calls, closing the hang
# itself, but a long-lived process keeps the code it started with: deploying
# that fix does not apply it to a copy already running. This script is the
# delivery mechanism -- restarting quota-watch.sh is how a merged fix like
# #267 actually reaches a process that has been running since before it
# merged, the same "a merge is not a deployment" lesson advance-live.sh
# exists to close for the supervisor loop itself.
#
# WHY A HEARTBEAT DRIVES THIS, NOT A PID. watchdog.sh's
# check_quota_watch_heartbeat already answers "is quota-watch.sh doing its
# job", by reading the `checked:`/`state:` stamp quota-watch.sh writes AFTER
# each iteration's work -- the same shape #163 gave inbox-poll.status. This
# script reuses that exact answer for the RESTART decision instead of asking
# `pgrep` a second, weaker question: a live pid proves nothing about whether
# the process is making progress, which is precisely how the three-hour hang
# went unnoticed. Only when the heartbeat itself is missing or stale does
# this script act -- never on pid presence/absence alone.
#
# THE LOCK IS THE SAME SHAPE poller-recover.sh USES, copied rather than
# reinvented (CLAUDE.md's own rule: "a LaunchAgent restarts it ... copy it
# rather than inventing a second one" applies equally to this in-repo
# mkdir-lock pattern) -- mkdir is atomic, records its holder's pid/started
# time, and reclaims a lock whose holder is provably dead or old enough that
# no legitimate run could still be inside it, so a SIGKILLed recovery run
# cannot wedge every later tick behind a lock nothing will ever release.
#
# quota-watch.sh is a plain nohup'd background process, not a tmux pane
# (unlike inbox-poll.sh) -- there is no window to respawn into, so recovery
# here is: find any live process, kill it if the heartbeat says it is not
# actually working, then launch a fresh one with nohup, exactly the shape
# quota-watch.sh's own header documents for starting it by hand.
#
# Usage: quota-watch-recover.sh
# Env overrides (mirroring poller-recover.sh):
#   SUPERVISOR_STATE            state dir; default ~/.local/state/agent-dotfiles-supervisor
#   SUPERVISOR_LIVE              live worktree path; default $SUPERVISOR_STATE/live
#   QUOTA_WATCH_STATUS_PATH       heartbeat stamp; default $SUPERVISOR_STATE/.quota-watch.state
#   QUOTA_WATCH_STALE_AFTER       seconds before a heartbeat is stale; default 600 (matches
#                                  watchdog.sh's QUOTA_WATCH_HEARTBEAT_STALE_AFTER default)
#   QUOTA_WATCH_LAUNCH_CMD         the command relaunched; default
#                                  `cd '$LIVE' && exec scripts/supervisor/quota-watch.sh`
#   QUOTA_WATCH_RECOVER_LOCK       lock dir; default $SUPERVISOR_STATE/.quota-watch-recover.lock
#   QUOTA_WATCH_RECOVER_LOCK_MAX_AGE  seconds before an unreclaimed lock with no provably-live
#                                  holder is reclaimed; default 60
#   QUOTA_WATCH_RECOVER_LOG        log file; default $SUPERVISOR_STATE/quota-watch-recover.log

set -uo pipefail

STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
LIVE="${SUPERVISOR_LIVE:-$STATE/live}"
STAMP="${QUOTA_WATCH_STATUS_PATH:-$STATE/.quota-watch.state}"
STALE_AFTER="${QUOTA_WATCH_STALE_AFTER:-600}"
LAUNCH_CMD="${QUOTA_WATCH_LAUNCH_CMD:-cd '$LIVE' && exec scripts/supervisor/quota-watch.sh}"
# Matches the process this script itself launches AND a hand-started one --
# `quota-watch.sh`, optionally preceded by an interpreter, anywhere in argv.
SERVICE_RE="${QUOTA_WATCH_SERVICE_RE:-(^|/)quota-watch\.sh( |$)}"
LOCK="${QUOTA_WATCH_RECOVER_LOCK:-$STATE/.quota-watch-recover.lock}"
LOCK_MAX_AGE="${QUOTA_WATCH_RECOVER_LOCK_MAX_AGE:-60}"
LOG="${QUOTA_WATCH_RECOVER_LOG:-$STATE/quota-watch-recover.log}"

log() {
  mkdir -p "$(dirname "$LOG")" 2>/dev/null
  printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" | tee -a "$LOG" 2>/dev/null
}

mkdir -p "$STATE" 2>/dev/null

# Same mkdir-lock idiom as poller-recover.sh's acquire_lock -- see that
# script's header for why an EXIT trap alone cannot be trusted to always
# clear it (SIGKILL, a hard crash, a LaunchAgent enforcing a hard kill on a
# hung tick all skip it), and why reclaim is gated on provable death or age
# rather than on the lock's mere presence.
acquire_lock() {
  if mkdir "$LOCK" 2>/dev/null; then
    printf '%s' "$$" >"$LOCK/pid" 2>/dev/null
    date +%s >"$LOCK/started" 2>/dev/null
    return 0
  fi
  local holder_pid started now age
  holder_pid=$(cat "$LOCK/pid" 2>/dev/null)
  started=$(cat "$LOCK/started" 2>/dev/null)
  now=$(date +%s)
  if [ -n "$holder_pid" ] && kill -0 "$holder_pid" 2>/dev/null; then
    return 1
  fi
  if [ -z "$started" ]; then
    return 1
  fi
  age=$((now - started))
  [ "$age" -lt "$LOCK_MAX_AGE" ] && return 1
  log "RECLAIMING stale lock $LOCK (holder pid ${holder_pid:-unknown}, age ${age}s) -- its EXIT trap never ran"
  rm -rf "$LOCK" 2>/dev/null
  mkdir "$LOCK" 2>/dev/null || return 1
  printf '%s' "$$" >"$LOCK/pid" 2>/dev/null
  date +%s >"$LOCK/started" 2>/dev/null
  return 0
}

if ! acquire_lock; then
  log "SKIPPED -- another recovery is already in flight ($LOCK held)"
  exit 0
fi
trap 'rm -rf "$LOCK" 2>/dev/null' EXIT

# heartbeat_age -- prints the stamp's age in seconds, or nothing (nonzero
# return) if the stamp is absent or its `checked:` line cannot be parsed.
# Read the same way watchdog_notify.py's classify_heartbeat does, so this
# script's restart decision and watchdog.sh's alarm decision can never
# silently disagree about what "stale" means.
heartbeat_age() {
  [ -f "$STAMP" ] || return 1
  local checked now epoch
  checked=$(sed -n 's/^checked: *//p' "$STAMP" 2>/dev/null | head -1)
  [ -n "$checked" ] || return 1
  epoch=$(python3 -c 'import calendar,time,sys
try:
    print(calendar.timegm(time.strptime(sys.argv[1], "%Y-%m-%dT%H:%M:%SZ")))
except Exception:
    sys.exit(1)' "$checked" 2>/dev/null) || return 1
  now=$(date +%s)
  echo $((now - epoch))
}

age=$(heartbeat_age)
age_rc=$?
if [ "$age_rc" -eq 0 ] && [ "$age" -le "$STALE_AFTER" ]; then
  log "OK -- heartbeat ${age}s old, within ${STALE_AFTER}s -- nothing to do"
  exit 0
fi

if [ "$age_rc" -ne 0 ]; then
  reason="no readable heartbeat at $STAMP"
else
  reason="heartbeat ${age}s old, over ${STALE_AFTER}s"
fi

# Kill anything already running -- it is either hung (the case this exists
# for) or has already exited (kill is then a harmless no-op per pid). Scoped
# to $LIVE's cwd, the same discipline poller-recover.sh's own orphan check
# uses, so a second deployment's quota-watch.sh on the same machine is left
# alone.
real_path() { (cd "$1" 2>/dev/null && pwd -P) || printf '%s' "$1"; }
live_real=$(real_path "$LIVE")
killed=""
if command -v pgrep >/dev/null 2>&1 && command -v lsof >/dev/null 2>&1; then
  while IFS= read -r cand_pid; do
    [ -n "$cand_pid" ] || continue
    cmd=$(ps -o command= -p "$cand_pid" 2>/dev/null) || continue
    [[ "$cmd" =~ $SERVICE_RE ]] || continue
    cand_cwd=$(lsof -a -p "$cand_pid" -d cwd -Fn 2>/dev/null | sed -n 's/^n//p' | head -1)
    [ "$(real_path "${cand_cwd:-/nonexistent}")" = "$live_real" ] || continue
    kill -TERM "$cand_pid" 2>/dev/null
    killed="${killed}${killed:+,}${cand_pid}"
  done < <(pgrep -f quota-watch.sh 2>/dev/null)
  if [ -n "$killed" ]; then
    sleep 1
    for pid in ${killed//,/ }; do kill -KILL "$pid" 2>/dev/null; done
  fi
fi

# Verified, not assumed (same discipline poller-recover.sh takes for every
# tmux mutation): `&` alone always "succeeds" from bash's point of view, so
# the only trustworthy check is that a matching process actually exists
# afterward.
nohup bash -c "$LAUNCH_CMD" >>"$STATE/quota-watch.log" 2>&1 &
new_pid=$!
disown 2>/dev/null || true
sleep 1
if ! kill -0 "$new_pid" 2>/dev/null; then
  log "FAILED -- launched quota-watch.sh (pid $new_pid) but it is not alive 1s later ($reason)"
  exit 1
fi

log "RESTARTED quota-watch.sh (pid $new_pid) -- $reason${killed:+; terminated stale pid(s) $killed}: $LAUNCH_CMD"
exit 0
