#!/bin/bash
# healthtick.sh <deadline_epoch> <interval_seconds>
#
# Event-driven estate watcher. Polls estate/PR state and exits as soon as the
# state STRING changes, so the caller wakes on a real transition, not a timer.
# Exiting on a real change is correct and stays -- it is the wake mechanism.
#
# Reconstructed 2026-09-07 after a Claude update removed
# ~/.claude/jobs/8182f39f/tmp/, which held the only copy. THIS is the durable
# master; the old path is now a symlink to it. Keep it in the repo tree, never
# in job scratch.
#
# agent-estate#1248: the gap this closes is leg 4 (re-arm). Every exit path
# (WAKE on a real change, or the deadline running out with none) now launches
# a detached successor with a fresh window of the same length before it
# prints its terminal line, so the polling loop itself never depends on a
# Director turn -- or any session -- noticing and restarting it by hand.
# `nohup "$0" ... & disown` is used, not `setsid` (this host has none): a
# child launched this way is reparented to launchd/init and measurably
# outlives the shell that spawned it -- verified in the PR body by starting
# one from a Bash call and finding it still alive, PPID 1, after that call's
# own process had already exited.
#
# This does NOT make delivery of the wake itself session-independent -- only
# whichever session holds a live background-task handle on the *current*
# generation gets notified when it exits, same as before. What changes is
# that a generation always exists to hold a handle on, instead of the chain
# going quiet the first time nobody was watching. A fresh session that wants
# to be woken re-attaches by waiting on the running PID (`pgrep -f
# healthtick`), not by starting a new one on top of it.
#
# What supervises this: nothing, above the OS itself, once a generation is
# detached -- and that is deliberate, not an oversight. Jon has a recorded
# hard parameter (it-abf738372b578388): never set up a cron outside the
# Claude Code ecosystem -- crons must be Claude Code-native, not e.g.
# launchd/crontab. CronCreate and Monitor, the two Claude-Code-native async
# primitives, are both explicitly session-only (CronCreate: "gone when
# Claude exits"; Monitor: "runs until ... the session ends") -- neither can
# rearm anything past a session's death, which is the exact failure this
# issue is about. A self-relaunching detached process is the only mechanism
# available that survives that boundary without adding an external
# scheduler; it is a chain of one-shot processes, not a recurring OS-level
# schedule entry, so it does not itself open a new launchd/crontab surface --
# but it is still an unsupervised process with no ceiling once armed. If a
# machine reboot, a `pkill -f healthtick`, or something else ends every
# generation, nothing restarts the chain; the 3-minute backstop still catches
# that (pgrep empty) exactly as before. HEALTHTICK_STOPFILE (default
# /tmp/estate-healthtick.stop) is the deliberate off switch: touch it and the
# current generation prints why and does not spawn a successor. Whether a
# perpetually self-renewing background process is an acceptable standing
# fixture, versus something narrower, is Jon's call, not this patch's -- see
# the PR body.
set -u
DEADLINE=${1:?deadline_epoch required}
INTERVAL=${2:-180}
STOPFILE=${HEALTHTICK_STOPFILE:-/tmp/estate-healthtick.stop}
LOG=${HEALTHTICK_LOG:-/tmp/estate-healthtick.log}
# The window length this generation was given, preserved across re-arms so
# each successor gets the same size window, not the original absolute
# deadline (which would be in the past by the time it re-arms).
NOW0=$(date +%s)
DURATION=$(( DEADLINE - NOW0 ))
[ "$DURATION" -lt "$INTERVAL" ] && DURATION=$INTERVAL
# Repos whose open PRs count toward run state. agent-dotfiles was invisible
# here until 2026-09-07 -- #348 sat open and unreviewable with the watcher
# reporting IDLE, the same blind spot the old flow-watch had (Fable finding 3).
REPOS="jonhill90/agent-estate jonhill90/agent-dotfiles jonhill90/skills"
BIN=/tmp/estate-bin
# Fail loud if the binary vanished the way the job scratch did. A missing BIN
# silently read active=0 and turned every wake into a false IDLE/STALLED.
# Deliberately does NOT re-arm: a binary that is gone stays gone until a
# human rebuilds it, and respawning on top of that would fast-loop instead
# of failing loud. The backstop's pgrep-empty plus this line in $LOG is the
# signal.
if [ ! -x "$BIN" ]; then
  printf 'HEALTHTICK-ERROR: %s missing or not executable -- refusing to report state\n' "$BIN" >&2
  exit 2
fi
# PRs held by standing order -- never counted as a stall.
PARKED="1014 1015 1223 1224"

state() {
  local active prs open line n info head mergeable ci verdicts label
  active=$( { "$BIN" inflight 2>&1 || true; } | grep -cE '^[[:space:]]*(dispatch|run)-' )
  prs=""
  for REPO in $REPOS; do
    for n in $(gh pr list --repo "$REPO" --state open --json number --jq '.[].number' 2>/dev/null | sort -n); do
      prs="$prs ${REPO}#${n}"
    done
  done
  open=""; line=""
  for ref in $prs; do
    REPO=${ref%%#*}; n=${ref##*#}
    short=$(printf '%s' "$REPO" | sed 's|jonhill90/agent-estate|est|;s|jonhill90/agent-dotfiles|dot|;s|jonhill90/skills|skl|')
    open="${open}${short}${n},"
    info=$(gh pr view "$n" --repo "$REPO" --json headRefOid,mergeable 2>/dev/null)
    head=$(printf '%s' "$info" | sed -n 's/.*"headRefOid":"\([0-9a-f]*\)".*/\1/p')
    mergeable=$(printf '%s' "$info" | sed -n 's/.*"mergeable":"\([A-Z]*\)".*/\1/p')
    ci=$(gh pr checks "$n" --repo "$REPO" 2>/dev/null | awk '{print $2}' | sort -u | paste -sd/ -)
    verdicts=$(gh pr view "$n" --repo "$REPO" --json comments \
      --jq "[.comments[]|select(.body|test(\"Reviewed-SHA: ${head}\"))|select(.body|test(\"Verdict: APPROVE\"))]|length" 2>/dev/null)
    [ -z "$verdicts" ] && verdicts=0
    line="${line} ${short}${n}=${mergeable},ci=${ci},verdicts@head=${verdicts}"
  done
  # PARKED holds PRs deferred by standing order; they are not a stall.
  # 1014/1015 (held), 1223 (held on provenance), 1224/K6.0 (deferred).
  label="IDLE-no-eligible-work"
  if [ "$active" = "0" ]; then
    for ref in $prs; do
      n=${ref##*#}; REPO=${ref%%#*}
      case " $PARKED " in *" $n "*) continue ;; esac
      short=$(printf '%s' "$REPO" | sed 's|jonhill90/agent-estate|est|;s|jonhill90/agent-dotfiles|dot|;s|jonhill90/skills|skl|')
      if printf '%s' "$line" | grep -q "${short}${n}=MERGEABLE,ci=pass"; then
        label="STALLED"
      fi
    done
  fi
  printf '%s | active=%s open=%s%s' "$label" "$active" "$open" "$line"
}

# Launch a detached successor with a fresh DURATION-length window, unless
# told to stop. Runs right before the terminal printf on every exit path so
# re-arming is not conditional on which path was taken.
rearm() {
  if [ -f "$STOPFILE" ]; then
    printf 'HEALTHTICK-STOP: %s present -- not re-arming\n' "$STOPFILE"
    return
  fi
  local newdeadline
  newdeadline=$(( $(date +%s) + DURATION ))
  nohup "$0" "$newdeadline" "$INTERVAL" >>"$LOG" 2>&1 </dev/null &
  disown
  printf 'REARM: pid=%s deadline=%s log=%s\n' "$!" "$newdeadline" "$LOG"
}

BASE=$(state)
printf 'BASELINE: %s\n' "$BASE"
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  if [ -f "$STOPFILE" ]; then
    printf 'HEALTHTICK-STOP: %s present -- exiting without re-arm\n' "$STOPFILE"
    exit 0
  fi
  sleep "$INTERVAL"
  NOW=$(state)
  if [ "$NOW" != "$BASE" ]; then
    printf 'WAKE: status change\n  was: %s\n  now: %s\n' "$BASE" "$NOW"
    rearm
    exit 0
  fi
done
printf 'WAKE: deadline reached\n  final: %s\n' "$BASE"
rearm
