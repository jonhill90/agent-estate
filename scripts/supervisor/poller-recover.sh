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
# THE LOCK MUST BE RECLAIMABLE. An `EXIT` trap does not run on SIGKILL, a
# hard crash, or a LaunchAgent enforcing a hard kill on a hung tick -- all
# real ways this script's OWN process can die while holding $LOCK. Without a
# reclaim path a lock left behind that way is permanent: every later tick
# reads "another recovery is already in flight", logs the same line forever,
# and never touches tmux again -- the poller stays down and NOTHING says so
# distinctly from the ordinary, benign "someone else is already handling
# this tick" case. That is the exact silent, human-in-the-loop failure this
# whole script exists to retire, one layer down. So the lock records who
# holds it and when, and a failed acquire only ever gives up (leaving the
# tick a benign no-op) when the recorded holder is either still alive or the
# lock is too young for that to be ruled out; otherwise it reclaims.
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
# ADDRESSED BY WINDOW ID, NOT NAME, ONCE FOUND. tmux allows duplicate window
# names, and lanes.sh already had to learn this the hard way for the SAME
# reason (#241: "the index and the id must describe the SAME window").
# Addressing a later mutation (`respawn-pane`, `set-window-option`) by
# `session:name` when two windows share that name does not pick one --
# real tmux refuses the target outright ("can't find window"). So the name
# is used for exactly one thing, the initial lookup, and if it resolves to
# MORE than one window this script refuses to guess which is the real
# poller and says so loudly, the same "refuse rather than guess" posture
# advance-live.sh already takes for an ambiguous git state.
#
# EVERY ACTION IS VERIFIED, NOT ASSUMED. A `tmux` call that fails (server
# hiccup, a target that stopped existing between the lookup and the
# mutation) used to be swallowed by `2>/dev/null` with no exit code check,
# so a failed respawn logged the same "RESPAWNED" line as a real one. Every
# branch below now re-reads the state it just tried to produce and reports
# failure (nonzero exit, a FAILED log line) when it does not hold.
#
# Usage: poller-recover.sh [session]
# Env overrides (mirroring the rest of this directory's scripts):
#   SUPERVISOR_STATE       state dir; default ~/.local/state/agent-dotfiles-supervisor
#   SUPERVISOR_LIVE        live worktree path; default $SUPERVISOR_STATE/live
#   LANES_SESSION          tmux session; default agent-dotfiles -- same variable
#                          lanes.sh/advance-live.sh already key on, not a second name
#   LANES_POLLER_WINDOW    window name; default inbox-poll -- same variable
#                          lanes.sh reads for its own service-pane fallback,
#                          deliberately the ONE name so the two cannot drift
#   POLLER_LAUNCH_CMD      the command relaunched into the pane; default
#                          `cd '$LIVE' && exec scripts/supervisor/inbox-poll.sh`
#   POLLER_RECOVER_LOCK    lock dir; default $SUPERVISOR_STATE/.poller-recover.lock
#   POLLER_RECOVER_LOCK_MAX_AGE  seconds before a lock with no provably-live
#                          holder is reclaimed; default 60 -- generous over
#                          this script's normal sub-second runtime
#   POLLER_RECOVER_LOG     log file; default $SUPERVISOR_STATE/poller-recover.log

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./poller-window.sh
. "$HERE/poller-window.sh"
# shellcheck source=./session-defaults.sh
. "$HERE/session-defaults.sh"

SESSION="${1:-$(lanes_session_or_default)}"
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
LIVE="${SUPERVISOR_LIVE:-$STATE/live}"
WINDOW="$POLLER_WINDOW_NAME"
LAUNCH_CMD="${POLLER_LAUNCH_CMD:-cd '$LIVE' && exec scripts/supervisor/inbox-poll.sh}"
# agent-supervisor#19: the same narrow process classifier lanes.sh uses for
# service-vs-dead classification, reused here for the ORPHAN CHECK below.
SERVICE_RE="${LANES_SERVICE_RE:-(^|/)inbox-poll\.sh( |$)}"
LOCK="${POLLER_RECOVER_LOCK:-$STATE/.poller-recover.lock}"
LOCK_MAX_AGE="${POLLER_RECOVER_LOCK_MAX_AGE:-60}"
LOG="${POLLER_RECOVER_LOG:-$STATE/poller-recover.log}"

# Writes to both the log file (the durable record) and stdout (so a caller
# capturing this script's output -- watchdog.sh -- can fold the same line
# into its own log without a second, differently-worded message to keep in
# sync).
log() {
  mkdir -p "$(dirname "$LOG")" 2>/dev/null
  printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" | tee -a "$LOG" 2>/dev/null
}

# Same idiom watchdog.sh's own real_path uses to compare worktree identity --
# reused rather than reinvented, see the orphan check below for why this
# script needs it too.
real_path() { (cd "$1" 2>/dev/null && pwd -P) || printf '%s' "$1"; }

command -v tmux >/dev/null 2>&1 || { log "no tmux on PATH -- nothing to do"; exit 0; }
tmux has-session -t "$SESSION" 2>/dev/null || { log "no session '$SESSION' -- nothing to recover into"; exit 0; }

# agent-supervisor#25: lsof lives at /usr/sbin/lsof on macOS, and
# /usr/sbin is NOT on the LaunchAgent's PATH (watchdog.sh's own hardcoded
# PATH override, which every child it execs -- including this script --
# inherits, lists /opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:$HOME/.local/bin
# and nothing under /sbin). Under that PATH `command -v lsof` finds nothing,
# and the orphan check below used to treat that exactly like "this
# candidate has no cwd" -- i.e. "not the poller", the fail-OPEN defect this
# fix closes. Resolved once, here, by absolute path if bare `lsof` is not
# on PATH, so the orphan check does not depend on the caller's PATH at all.
LSOF_BIN=""
if command -v lsof >/dev/null 2>&1; then
  LSOF_BIN="lsof"
elif [ -x /usr/sbin/lsof ]; then
  LSOF_BIN="/usr/sbin/lsof"
fi

mkdir -p "$STATE" 2>/dev/null

# Atomic: mkdir either creates the directory and succeeds, or finds it
# already there and fails, with no window in between where two callers can
# both see "absent". Records who holds it and when, so a FAILED acquire can
# tell a genuinely concurrent holder (still alive) from a stale one (dead,
# or old enough that no legitimate run could still be inside it) -- see the
# header for why an EXIT trap alone cannot be trusted to always clear this.
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
    return 1   # a real, live holder -- genuinely concurrent, not stale
  fi
  if [ -z "$started" ]; then
    return 1   # lock dir exists but its writer hasn't recorded pid/started yet -- an in-progress acquisition, not a dead one
  fi
  age=$((now - started))
  [ "$age" -lt "$LOCK_MAX_AGE" ] && return 1   # too young to rule out a legitimate holder still writing pid/started
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

# Every window in $SESSION named $WINDOW, by id. Zero: absent, create one.
# One: the ordinary case, act on it. More than one: refuse rather than guess
# which is the real poller -- see the header note on window-id addressing.
# agent-supervisor#28: poller_window_ids's rc is checked here, not just its
# output -- a query it could not trust (tmux missing, list-windows failing)
# must refuse, not fall into the zero-windows branch below and risk
# launching a second poller alongside one it simply failed to see.
ids_raw=$(poller_window_ids "$SESSION")
ids_rc=$?
if [ "$ids_rc" -ne 0 ]; then
  log "FAILED -- could not determine poller windows in session '$SESSION' (tmux query failed, rc=$ids_rc) -- refusing to guess whether a poller exists"
  exit 1
fi
ids=()
while IFS= read -r wid; do
  [ -n "$wid" ] && ids+=("$wid")
done <<<"$ids_raw"

if [ "${#ids[@]}" -gt 1 ]; then
  log "FAILED -- ${#ids[@]} windows named '$WINDOW' in session '$SESSION' (${ids[*]}) -- refusing to guess which is the poller, not touching any of them"
  exit 1
fi

if [ "${#ids[@]}" -eq 0 ]; then
  # agent-supervisor#19: ZERO WINDOWS IS NOT ZERO POLLERS. The "already
  # alive?" question the lookup above answers is scoped to windows named
  # $WINDOW; a poller can be alive with none. Measured cause: inbox-poll.sh
  # traps HUP (killing this window sends it one) and its handler,
  # report_stop, makes a network call (notify.sh) before the process
  # actually exits -- a real, unbounded gap between "window gone" and
  # "process gone" (the incident's pid 71021 needed SIGKILL because it was
  # still inside that handler when someone tried TERM). Every FAILED branch
  # below reaches this same state too: `tmux new-window` always launches
  # the real process before this script can fail on what comes after, so a
  # window-resolution failure a few lines down still leaves a live poller
  # behind for the NEXT tick to find here. Recreating on top of either one
  # is agent-supervisor#19 itself -- a second live poller racing the same
  # Telegram offset. Ask the process table, the same classifier
  # lanes.sh's SERVICE_RE already uses for its service-vs-dead question, and
  # refuse rather than duplicate when it says yes.
  # SCOPED TO THIS $LIVE, not every inbox-poll.sh on the machine. Multiple
  # deployments (production LIVE, another checkout, an agent's own worktree
  # if it ever runs this file directly) can coexist, each launched with
  # `cd '$LIVE' && exec ...`, so the poller's own cwd is that $LIVE for as
  # long as it runs -- the same real_path idiom watchdog.sh already uses to
  # compare worktree identity, reused rather than a second one. Without this
  # a recovery running against a DIFFERENT deployment's LIVE would refuse to
  # ever create its own poller because some unrelated one exists elsewhere.
  live_real=$(real_path "$LIVE")
  orphan_pid=""
  while IFS= read -r cand_pid; do
    [ -n "$cand_pid" ] || continue
    cmd=$(ps -o command= -p "$cand_pid" 2>/dev/null) || continue
    [[ "$cmd" =~ $SERVICE_RE ]] || continue
    # agent-supervisor#25: "cannot determine this candidate's cwd" must mean
    # "cannot rule out a live poller", never "no poller alive". A missing
    # lsof binary, an lsof that errors, and an lsof that runs but reports no
    # cwd were all previously swallowed by `2>/dev/null` into the same empty
    # $cand_cwd and treated as "not the poller" (`continue`) -- exactly the
    # fail-open this issue is about. All three now refuse instead: the tick
    # no-ops (exit 1, no window touched) rather than concluding "no poller
    # alive" on missing information.
    if [ -z "$LSOF_BIN" ]; then
      log "FAILED -- lsof is not available (checked PATH and /usr/sbin/lsof) -- cannot determine cwd of candidate pid $cand_pid ($cmd), refusing to rule out a live poller"
      exit 1
    fi
    cand_lsof_out=$("$LSOF_BIN" -a -p "$cand_pid" -d cwd -Fn 2>&1)
    lsof_rc=$?
    if [ "$lsof_rc" -ne 0 ]; then
      log "FAILED -- $LSOF_BIN exited $lsof_rc probing candidate pid $cand_pid ($cmd): ${cand_lsof_out:-no output} -- refusing to rule out a live poller"
      exit 1
    fi
    cand_cwd=$(printf '%s\n' "$cand_lsof_out" | sed -n 's/^n//p' | head -1)
    if [ -z "$cand_cwd" ]; then
      log "FAILED -- $LSOF_BIN reported no cwd for candidate pid $cand_pid ($cmd) -- refusing to rule out a live poller"
      exit 1
    fi
    [ "$(real_path "$cand_cwd")" = "$live_real" ] || continue
    orphan_pid="$cand_pid"
    break
  done < <(pgrep -f inbox-poll.sh 2>/dev/null)
  if [ -n "$orphan_pid" ]; then
    log "FAILED -- inbox-poll.sh is already running (pid $orphan_pid) with no window named '$WINDOW' in session '$SESSION' -- refusing to start a second poller; it needs a window reattached, not a duplicate"
    exit 1
  fi

  if ! tmux new-window -t "$SESSION" -n "$WINDOW" -d -- "$LAUNCH_CMD" 2>/dev/null; then
    log "FAILED -- tmux new-window for '$WINDOW' in session '$SESSION' did not succeed"
    exit 1
  fi
  wid=$(poller_window_ids "$SESSION" | head -1)
  if [ -z "$wid" ]; then
    log "FAILED -- window '$WINDOW' not found in session '$SESSION' right after creating it"
    exit 1
  fi
  target="$SESSION:$wid"
  tmux set-window-option -t "$target" remain-on-exit on 2>/dev/null
  dead=$(tmux list-panes -t "$target" -F '#{pane_dead}' 2>/dev/null | head -1)
  if [ "$dead" != "0" ]; then
    log "FAILED -- created window $target ($WINDOW) but its pane is not alive right after launch (pane_dead=${dead:-unreadable})"
    exit 1
  fi
  log "RECREATED window $target ($WINDOW) and launched: $LAUNCH_CMD"
  exit 0
fi

target="$SESSION:${ids[0]}"

# Idempotent even when this window already had it set (by a previous tick,
# or by hand) -- and covers a window that predates this script entirely.
tmux set-window-option -t "$target" remain-on-exit on 2>/dev/null

dead=$(tmux list-panes -t "$target" -F '#{pane_dead}' 2>/dev/null | head -1)
if [ "$dead" = "1" ]; then
  # -k also lets this recover from a pane left dead by something other than
  # the poller exiting on its own; it never fires against a LIVE pane, since
  # that branch is only reached when pane_dead already read 1.
  if ! tmux respawn-pane -k -t "$target" -- "$LAUNCH_CMD" 2>/dev/null; then
    log "FAILED -- tmux respawn-pane on $target did not succeed"
    exit 1
  fi
  redead=$(tmux list-panes -t "$target" -F '#{pane_dead}' 2>/dev/null | head -1)
  if [ "$redead" != "0" ]; then
    log "FAILED -- respawned $target but its pane is still not alive (pane_dead=${redead:-unreadable})"
    exit 1
  fi
  log "RESPAWNED dead pane $target: $LAUNCH_CMD"
  exit 0
fi

if [ "$dead" != "0" ]; then
  log "FAILED -- could not read pane_dead for $target (got '${dead:-empty}') -- not acting on an unreadable pane"
  exit 1
fi

log "OK -- $target alive, nothing to do"
