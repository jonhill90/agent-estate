#!/bin/bash
# Make the inbox-poll window self-correcting when the poller process behind
# it is gone -- agent-supervisor#10.
#
# THE GAP THIS CLOSES: the poller's pane is launched with
# `cd '$LIVE' && exec scripts/supervisor/inbox-poll.sh` (see inbox-poll.sh's
# own header and advance-live.sh's maybe_restart_poller). `exec` replaces the
# pane's shell with the poller itself, so there is no shell left underneath
# it. Without `remain-on-exit`, tmux's default behaviour, ANY exit of that
# process -- clean (`report_stop`'s trap), a version-triggered restart
# (advance-live.sh's INBOX_POLL_RESTART_FLAG), a crash, or a signal -- closes
# the WINDOW along with it. Nothing is then left for a restart mechanism to
# address; see the issue for the incident this was measured from.
#
# THE FIX HAS TWO HALVES:
#
#   1. `remain-on-exit on` on the poller's window, set here on every run so
#      it applies whether this script created the window or found one a
#      human launched by hand before this existed. With it set, the window
#      and pane SURVIVE the process exiting; the pane just goes dead
#      (`#{pane_dead}` = 1) instead of taking the window with it. This is a
#      tmux-level property of the pane, not something the exiting process
#      has to cooperate with -- it holds for a clean exit, an uncaught
#      crash, and a SIGKILL alike, because it is tmux (the pty owner)
#      noticing the child died, not a trap inside the child. See the
#      "non-clean exit" note below for the one respect in which those paths
#      still differ.
#
#   2. This script, run periodically (from watchdog.sh, which already ticks
#      every ~180s and already owns "notice something is down and act"),
#      notices the dead pane -- or a missing window entirely, for a poller
#      whose window predates `remain-on-exit` or was closed by hand -- and
#      relaunches the same command into it. `tmux respawn-pane` reuses the
#      SAME pane; it can never produce a second poller by itself, which is
#      what makes idempotency here a property of the mechanism rather than
#      something a caller has to get right.
#
# THE ONE PLACE A SECOND POLLER COULD STILL HAPPEN: two invocations of this
# script racing when the window is ENTIRELY ABSENT. `tmux new-window` has no
# compare-and-swap form -- two concurrent callers can both observe "no window
# named inbox-poll" and both create one, tmux allows duplicate window names,
# and the result is two live pollers double-delivering Jon's messages (the
# exact failure named in the brief for this fix). The mkdir-based lock below
# closes that: mkdir is atomic even without flock(1), which macOS does not
# ship (see inbox.sh / director-inbox.sh, which solve the same constraint
# with Python's fcntl.flock for a different kind of lock -- this one only
# ever needs mutual exclusion between shell processes on one machine, so
# mkdir is enough and avoids a second language for it).
#
# NON-CLEAN EXIT (crash, SIGKILL): recovery is IDENTICAL to the clean-exit
# path, because it depends only on tmux's own `pane_dead`, not on the exiting
# process running any code of its own -- a SIGKILLed poller cannot run a
# trap, but it cannot stop tmux noticing its pty child died either. What
# DOES differ is notification: a clean exit still pages Jon proactively
# through inbox-poll.sh's own `report_stop` trap (agent-dotfiles#155/#160);
# a SIGKILL runs no userspace code at all and pages nobody at the moment of
# death. That gap is #163's inbox-poll heartbeat staleness check
# (watchdog.sh's check_inbox_heartbeat), which this script does not
# duplicate -- it is a distinct concern (tell a human) from this one (make
# the poller come back without one).
#
# WHAT THIS DOES NOT DO: restart a poller that is merely stuck (alive, wedged
# on a bad connection). That is advance-live.sh's cooperative restart via
# INBOX_POLL_RESTART_FLAG, unchanged by this fix except that it no longer
# also queues a relaunch command with send-keys -- that queuing relied on
# the same false "a shell is still under there" assumption this issue is
# about (see advance-live.sh's maybe_restart_poller), and is now redundant:
# once the flagged poller exits, this script's own next tick relaunches it
# with whatever code is on disk, which is exactly what advance-live.sh's
# send-keys was trying to arrange by hand.
#
# Usage: poller-recover.sh [session]
# Env overrides (mirroring the rest of this directory's scripts):
#   SUPERVISOR_STATE      state dir; default ~/.local/state/agent-dotfiles-supervisor
#   SUPERVISOR_LIVE        live worktree path; default $SUPERVISOR_STATE/live
#   LANES_SESSION          tmux session; default agent-dotfiles
#   POLLER_WINDOW           window name; default inbox-poll -- kept in sync
#                          with lanes.sh's own LANES_POLLER_WINDOW default
#   POLLER_LAUNCH_CMD       the command relaunched into the pane; default
#                          `cd '$LIVE' && exec scripts/supervisor/inbox-poll.sh`
#   POLLER_RECOVER_LOCK     lock dir; default $SUPERVISOR_STATE/.poller-recover.lock
#   POLLER_RECOVER_LOG      log file; default $SUPERVISOR_STATE/poller-recover.log

set -uo pipefail

SESSION="${1:-${LANES_SESSION:-agent-dotfiles}}"
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
LIVE="${SUPERVISOR_LIVE:-$STATE/live}"
WINDOW="${POLLER_WINDOW:-inbox-poll}"
LAUNCH_CMD="${POLLER_LAUNCH_CMD:-cd '$LIVE' && exec scripts/supervisor/inbox-poll.sh}"
LOCK="${POLLER_RECOVER_LOCK:-$STATE/.poller-recover.lock}"
LOG="${POLLER_RECOVER_LOG:-$STATE/poller-recover.log}"

# Writes to both the log file (the durable record) and stdout (so a caller
# capturing this script's output -- watchdog.sh -- can fold the same line
# into its own log without a second, differently-worded message to keep in
# sync).
log() {
  mkdir -p "$(dirname "$LOG")" 2>/dev/null
  printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" | tee -a "$LOG" 2>/dev/null
}

command -v tmux >/dev/null 2>&1 || { log "no tmux on PATH -- nothing to do"; exit 0; }
tmux has-session -t "$SESSION" 2>/dev/null || { log "no session '$SESSION' -- nothing to recover into"; exit 0; }

mkdir -p "$STATE" 2>/dev/null
# Atomic: mkdir either creates the directory and succeeds, or finds it
# already there and fails, with no window in between where two callers can
# both see "absent". A stale lock (this process died holding it) is bounded
# by the caller's own cadence -- watchdog.sh ticks every ~180s and this exits
# in well under that, so the next tick clears it by trying again; nothing
# here waits on it.
if ! mkdir "$LOCK" 2>/dev/null; then
  log "SKIPPED -- another recovery is already in flight ($LOCK held)"
  exit 0
fi
trap 'rmdir "$LOCK" 2>/dev/null' EXIT

target="$SESSION:$WINDOW"

if ! tmux list-windows -t "$SESSION" -F '#{window_name}' 2>/dev/null | grep -qFx "$WINDOW"; then
  tmux new-window -t "$SESSION" -n "$WINDOW" -d 2>/dev/null
  tmux set-window-option -t "$target" remain-on-exit on 2>/dev/null
  tmux send-keys -t "$target" -l "$LAUNCH_CMD" 2>/dev/null
  tmux send-keys -t "$target" Enter 2>/dev/null
  log "RECREATED window $target and launched: $LAUNCH_CMD"
  exit 0
fi

# Idempotent even when this window already had it set (by a previous tick,
# or by hand) -- and covers a window that predates this script entirely.
tmux set-window-option -t "$target" remain-on-exit on 2>/dev/null

dead=$(tmux list-panes -t "$target" -F '#{pane_dead}' 2>/dev/null | head -1)
if [ "$dead" = "1" ]; then
  # -k also lets this recover from a pane left dead by something other than
  # the poller exiting on its own; it never fires against a LIVE pane, since
  # that branch is only reached when pane_dead already read 1.
  tmux respawn-pane -k -t "$target" -- "$LAUNCH_CMD" 2>/dev/null
  log "RESPAWNED dead pane $target: $LAUNCH_CMD"
  exit 0
fi

log "OK -- $target alive, nothing to do"
