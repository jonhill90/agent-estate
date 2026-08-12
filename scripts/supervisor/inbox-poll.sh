#!/bin/bash
# Long-poll Telegram for Jon's replies and route each one the moment it
# arrives, instead of waiting for the Director's next tick.
#
# agent-dotfiles#142. #142 weighs long polling against a webhook and long
# polling wins: `getUpdates` with a `timeout` holds the request open and
# returns the instant a message lands, needs no public endpoint, and works
# from a laptop behind NAT. A webhook needs somewhere reachable to receive
# the POST, which this estate does not have and is a meaningfully bigger
# commitment for the same outcome. See #142 for the full argument.
#
# This script IS the poller. It does not reimplement Telegram's offset
# handling or the "unreachable is not the same as empty" distinction --
# `inbox.sh` already has both, correctly, and this calls it with
# INBOX_TIMEOUT set high so the same script that a Director tick calls with
# timeout=0 blocks here until Jon writes something. `inbox.sh`'s own locking
# (agent-dotfiles#142) is what keeps this long-lived loop and an occasional
# direct Director call from racing on the same offset file.
#
# WHERE THIS RUNS: this file is the portable core, not the launcher. The
# closest precedent in this tree is `watchdog.sh` -- code lives here,
# restart-on-crash lives in a LaunchAgent that belongs to the machine-specific
# adapter repository (Hill90), not here (see this directory's README). Two
# properties this script needs from whatever runs it:
#
#   - restart on exit, so a crash (network blip that outlives curl's
#     timeout, an unhandled error) is a brief gap, not a dead poller. A
#     LaunchAgent with KeepAlive is the standing pattern for that in this
#     estate -- watchdog.sh's own LaunchAgent is the example.
#   - run continuously, not on a schedule. A cron-style relaunch every N
#     seconds defeats the point: the whole value of long polling is that the
#     request is open and waiting, not that it is retried often.
#
# A plain tmux window running this script directly satisfies both today with
# zero new infrastructure, and is where this should start: `tmux new-window
# ... 'scripts/supervisor/inbox-poll.sh'`. It is one line, needs no plist, and
# a crashed pane is as visible in `tmux list-windows` as a dead lane already
# is. Promote it to a LaunchAgent (Hill90's adapter) once it has been run
# that way and proven stable -- the same order watchdog.sh itself went
# through.
#
# A DEAD POLLER MUST NOT LOOK LIKE SILENCE. Two failure shapes, two answers:
#
#   Telegram is unreachable but the poller is alive: `inbox.sh` exits 1 on
#   every retry. After INBOX_POLL_FAIL_THRESHOLD consecutive failures this
#   sends ONE notification through notify.sh (not one per failure -- a
#   flapping connection would otherwise page Jon every retry) and keeps
#   retrying; a later success sends nothing but resets so a fresh outage
#   pages again.
#
#   The poller process itself is going down, for any reason: an EXIT trap
#   fires on every exit path bash can still act on and tells Jon through
#   notify.sh before the process is gone. This is the one report a dying
#   process can still make for itself; it cannot detect being SIGKILLed or
#   the machine going to sleep (SIGKILL runs no userspace code at all, trap
#   included -- verified, not assumed), which is exactly why continuous
#   running under a restart-on-crash launcher (above) is part of the design,
#   not an optional extra.
#
# agent-dotfiles#155: the EXIT trap above originally fired unconditionally,
# so a 12-second pre-flight run -- confirming the poller starts, nothing
# more -- paged Jon exactly like a production death would. Two things now
# gate the page, not the record (see report_stop below):
#
#   Deliberate stop: the loop reaching INBOX_POLL_ITERATIONS and returning is
#   only ever a test or pre-flight run -- production leaves ITERATIONS at 0
#   (forever) and never reaches this path by exhaustion. It sets DELIBERATE
#   before breaking, and report_stop stays quiet whenever that flag is set,
#   regardless of how long the run lasted.
#
#   Too young to matter: anything else -- a signal, an unhandled error --
#   stays quiet if it happens before INBOX_POLL_MIN_UPTIME seconds have
#   passed. A run that dies at second 12 was never the estate's poller in
#   any sense Jon needs paged about; a run that dies after the threshold is
#   treated as the real thing going down and pages exactly as before.
#   Default is 60s: an order of magnitude past the 12s pre-flight that
#   caused #155, so no plausible pre-flight or `--help`-style smoke check
#   crosses it by accident, while still being short enough that a genuine
#   crash is normally well past it. The accepted gap: a poller that starts
#   clean and then dies inside that first 60s -- e.g. a config typo that
#   only breaks on the first real inbox.sh call -- goes unreported by this
#   path. That is deliberate, not an oversight: the same window is exactly
#   when a human is watching (a deploy, a restart, a pre-flight), the status
#   file (below) still records "stopped" for whoever checks it, and widening
#   the window to cover that case reintroduces the false pages this issue
#   exists to remove. If unattended early deaths in that window turn out to
#   matter in practice, the fix is a supervising watchdog that notices the
#   heartbeat go stale (report() below already provides that signal), not a
#   lower threshold here.
#
# agent-dotfiles#187: this script executes whatever text it was started
# with and has no mechanism of its own to notice a merge. #130 was the same
# defect in watchdog.sh, and #132 fixed it there by having the watchdog
# advance its own pinned worktree every tick -- the fix here is the
# analogous half for THIS process, not a second copy of that mechanism:
# advance-live.sh (called from watchdog.sh's exit trap, once it has decided
# what LIVE's own head sha is) compares that sha against the `sha:` line
# this script writes into its own status file below, and if they differ it
# writes INBOX_POLL_RESTART_FLAG and queues a relaunch into this pane -- see
# advance-live.sh's `maybe_restart_poller` for the write side.
#
# COOPERATIVE, NOT SIGNALED. The flag is checked once per loop iteration,
# strictly between an inbox.sh call and the next one -- never while a batch
# of Jon's messages is being routed. inbox.sh acknowledges (persists) a
# whole batch's Telegram offset before this loop routes any of them (see
# inbox.sh's own header), so the ONE window that must never be interrupted
# is that per-iteration read-route span; checking between iterations instead
# of via a signal means a restart can never land inside it, so nothing this
# process was already responsible for delivering is skipped, and nothing it
# already acknowledged to Telegram is asked for again.
#
# A version-triggered restart sets DELIBERATE (like the INBOX_POLL_ITERATIONS
# path above) so report_stop below does not page Jon -- #160 gates the page
# on "was this deliberate", and a deploy restart is exactly that. An
# unexpected death still has DELIBERATE unset and still pages, same as ever.
#
# Usage: inbox-poll.sh [session]
# Config: INBOX_POLL_TIMEOUT          Telegram long-poll seconds (default 25)
#         INBOX_POLL_FAIL_THRESHOLD   consecutive failures before paging (default 3)
#         INBOX_POLL_ITERATIONS       stop after N loop iterations (default 0 = forever;
#                                     tests use this, production never sets it)
#         INBOX_POLL_MIN_UPTIME       seconds a run must have been alive before an
#                                     unexpected exit pages Jon (default 60; #155)
#         INBOX_POLL_BACKOFF_BASE     seconds of backoff per consecutive failure,
#                                     capped past 12 (default 5; tests set 0)
#         INBOX_POLL_RESTART_FLAG     path checked once per iteration; its presence
#                                     is a deliberate, non-paging restart request
#                                     (default $STATE/.inbox-poll-restart-requested; #187)

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SESSION="${1:-${LANES_SESSION:-agent-dotfiles}}"
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
STATUS="${INBOX_POLL_STATUS:-$STATE/inbox-poll.status}"
LOG="${INBOX_POLL_LOG:-$STATE/inbox-poll.log}"
POLL_TIMEOUT="${INBOX_POLL_TIMEOUT:-25}"
FAIL_THRESHOLD="${INBOX_POLL_FAIL_THRESHOLD:-3}"
ITERATIONS="${INBOX_POLL_ITERATIONS:-0}"
MIN_UPTIME="${INBOX_POLL_MIN_UPTIME:-60}"
BACKOFF_BASE="${INBOX_POLL_BACKOFF_BASE:-5}"
RESTART_FLAG="${INBOX_POLL_RESTART_FLAG:-$STATE/.inbox-poll-restart-requested}"
START_TS=$(date +%s)
# The commit this running process was started from -- written into every
# status report so an external reader (advance-live.sh) can tell a stale
# poller from a current one without guessing at process age. "unknown" when
# $HERE is not a git worktree at all (a stray copy run by hand); that value
# never equals a real sha, so it never falsely reads as current.
POLLER_SHA=$(git -C "$HERE" rev-parse HEAD 2>/dev/null || echo unknown)

mkdir -p "$(dirname "$STATUS")" 2>/dev/null

log() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >>"$LOG" 2>/dev/null; }

# Heartbeat, written every iteration regardless of outcome -- the same "one
# file answers where we are" contract watchdog.status keeps. An external
# health check (this estate's own watchdog, or Hill90's adapter) can compare
# its `checked:` timestamp against wall clock to notice a poller that stopped
# updating without exiting cleanly enough for the trap below to fire.
report() {  # report <state> <detail>
  local tmp="$STATUS.$$"
  {
    printf 'checked: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf 'sha:     %s\n' "$POLLER_SHA"
    printf 'state:   %s\n' "$1"
    printf 'detail:  %s\n' "${2:-}"
    printf 'pid:     %s\n' "$$"
  } >"$tmp" 2>/dev/null && mv -f "$tmp" "$STATUS" 2>/dev/null
}

STOPPING=""
DELIBERATE=""
DELIBERATE_REASON=""
report_stop() {
  [ -n "$STOPPING" ] && return
  STOPPING=1
  # The record and the page are different things (#155): the status file and
  # log line below happen on every exit, paged or not, so a later reader
  # (human or watchdog) always has the fact even when Jon does not get pinged.
  report stopped "poller process exiting"
  local uptime=$(( $(date +%s) - START_TS ))
  log "STOPPING pid $$ after ${uptime}s"

  if [ -n "$DELIBERATE" ]; then
    log "deliberate stop (${DELIBERATE_REASON:-reason not recorded}) -- not paging Jon"
    return
  fi
  if [ "$uptime" -lt "$MIN_UPTIME" ]; then
    log "stopped after ${uptime}s, under INBOX_POLL_MIN_UPTIME=${MIN_UPTIME}s -- too young to have been the estate's real poller, not paging Jon"
    return
  fi

  if ! AGENT_NOTIFY_CALLER=supervisor "$HERE/notify.sh" \
       "Telegram inbox poller stopped" \
       "pid $$ on this machine is no longer polling -- replies will queue until the Director's next inbox.sh tick or the poller restarts" \
       >>"$LOG" 2>&1; then
    log "COULD NOT REACH JON ABOUT THE STOP EITHER -- last resort is this log line"
  fi
}
trap report_stop EXIT
# agent-dotfiles#187: EXIT alone is not enough, and the gap is not theoretical.
# An UNTRAPPED SIGTERM only reaches the EXIT trap when bash happens to be
# waiting on a foreground child (the inbox.sh long poll, most of the time) --
# it defers the signal, the child finishes, the shell exits normally and the
# trap runs. When the signal instead lands while bash is executing its own
# builtins -- the status write, the log line, the loop arithmetic -- the
# default disposition kills the shell outright and the EXIT trap NEVER RUNS.
# Measured on this file: 150 SIGTERMs at a loop boundary, 4 produced no page
# to Jon and no `stopped` heartbeat at all. That is #160's guarantee failing
# a few percent of the time, silently, and it is what turned this branch's CI
# red -- the two heartbeat assertions in test_inbox_poll.sh are exactly the
# ones that lose the race. Trapping the signal explicitly makes bash run the
# handler at the next command boundary no matter what it was doing.
# report_stop's own STOPPING guard makes the EXIT trap that follows a no-op.
trap 'report_stop; exit 143' TERM   # 128 + 15
trap 'report_stop; exit 130' INT    # 128 + 2
trap 'report_stop; exit 129' HUP    # 128 + 1

fail_count=0
notified_down=""
iter=0
while :; do
  iter=$((iter + 1))
  out=$(INBOX_TIMEOUT="$POLL_TIMEOUT" "$HERE/inbox.sh" 2>>"$LOG")
  rc=$?

  if [ "$rc" -ne 0 ]; then
    fail_count=$((fail_count + 1))
    report degraded "$fail_count consecutive inbox.sh failure(s)"
    log "FAIL ($fail_count): inbox.sh exited $rc"
    if [ "$fail_count" -ge "$FAIL_THRESHOLD" ] && [ -z "$notified_down" ]; then
      if AGENT_NOTIFY_CALLER=supervisor "$HERE/notify.sh" \
           "Telegram inbox poller cannot reach Telegram" \
           "$fail_count consecutive failures -- Jon's replies are not being read" \
           >>"$LOG" 2>&1; then
        notified_down=1
      else
        log "COULD NOT NOTIFY ABOUT THE OUTAGE EITHER -- will retry notifying next failure"
      fi
    fi
  else
    if [ "$fail_count" -gt 0 ]; then
      log "RECOVERED after $fail_count failure(s)"
    fi
    fail_count=0
    notified_down=""
    report ok "listening"
    if [ -n "$out" ]; then
      # agent-dotfiles#186: a burst that drains as N messages with zero lanes
      # blocked used to produce N identical "no lane is waiting" pings --
      # observed live as 23 messages -> 21 pings in ~21 seconds. The batch
      # boundary already exists right here (one drain of $out), so each
      # no-lane-waiting message is routed with INBOX_ROUTE_BATCH=1, which
      # makes inbox-route.sh stay quiet and exit 3 instead of notifying Jon
      # itself (see inbox-route.sh's own comment on that branch). This loop
      # tallies how many hit that branch and sends ONE summary notice after
      # the drain, covering the whole batch. Messages that route to a lane,
      # or that hit a menu/ambiguity refusal (exit 2), are untouched -- those
      # still notify exactly as before, per message, because each of those is
      # its own distinct, actionable event, not repetition of the same one.
      no_lane_count=0
      no_lane_last=""
      while IFS=$'\t' read -r text display; do
        [ -n "$text" ] || continue
        # inbox.sh (#152) emits TEXT and DISPLAY tab-separated on one line --
        # route the bare TEXT (what a lane should receive), log the DISPLAY
        # form (what a human reading $LOG wants to see). Never re-derive TEXT
        # by parsing DISPLAY back apart.
        if INBOX_ROUTE_BATCH=1 "$HERE/inbox-route.sh" "$text" "$SESSION" >>"$LOG" 2>&1; then
          log "ROUTED: ${display:-$text}"
        else
          route_rc=$?
          # #164: inbox-route.sh exits 2 for "nothing was typed anywhere,
          # but Jon was told why" (a menu refusal, zero lanes waiting, or
          # ambiguity) -- distinct from exit 1 (couldn't even reach Jon) and
          # from exit 0 (delivered). This used to share exit 0 with a real
          # delivery and get logged as ROUTED, which recorded a message as
          # routed when it was deliberately not.
          # #186: exit 3 is exit 2's batched sibling -- zero lanes waiting,
          # deliberately not notified yet because this loop owns one summary
          # notice for the whole drain instead. It is still a refusal for
          # logging purposes; only the notification is deferred.
          if [ "$route_rc" -eq 2 ]; then
            log "REFUSED: ${display:-$text}"
          elif [ "$route_rc" -eq 3 ]; then
            log "REFUSED: ${display:-$text}"
            no_lane_count=$((no_lane_count + 1))
            no_lane_last="${display:-$text}"
          else
            log "ROUTE FAILED: ${display:-$text}"
          fi
        fi
      done <<<"$out"

      if [ "$no_lane_count" -gt 0 ]; then
        # #186: the no-drop guarantee (#142) is unchanged -- Jon is still
        # told, exactly once per message when N=1 (unchanged wording), and
        # once total naming the count when N>1. If notify.sh itself fails,
        # the REFUSED lines already logged above are the record that no
        # message here was silently dropped, same as every other notify
        # failure path in this script.
        if [ "$no_lane_count" -eq 1 ]; then
          subject="Telegram message received, no lane is waiting"
          body="No blocked lane to route to -- got: $no_lane_last"
        else
          subject="Telegram messages received, no lane is waiting"
          body="$no_lane_count messages received, no lane waiting"
        fi
        if AGENT_NOTIFY_CALLER=supervisor "$HERE/notify.sh" "$subject" "$body" >>"$LOG" 2>&1; then
          log "NOTIFIED: one summary notice for $no_lane_count no-lane message(s)"
        else
          log "COULD NOT NOTIFY ABOUT $no_lane_count NO-LANE MESSAGE(S) EITHER -- the REFUSED lines above are the record"
        fi
      fi
    fi
  fi

  # agent-dotfiles#187: checked here, between iterations -- never inside the
  # block above -- so a restart can only land at a boundary where this
  # iteration's whole read-route span has already finished (see the header
  # comment). The flag is this process's own responsibility to clear: it is
  # the only reader, and leaving it behind would make the NEXT process to
  # start (the one this restart is deploying) read it as a second request.
  if [ -f "$RESTART_FLAG" ]; then
    rm -f "$RESTART_FLAG"
    DELIBERATE=1
    DELIBERATE_REASON="version-triggered restart requested"
    log "restart flag seen at $RESTART_FLAG -- exiting cleanly so the queued relaunch picks up the current code"
    break
  fi

  if [ "$ITERATIONS" -gt 0 ] && [ "$iter" -ge "$ITERATIONS" ]; then
    DELIBERATE=1
    DELIBERATE_REASON="INBOX_POLL_ITERATIONS reached"
    break
  fi
  # Only a failed call needs a local backoff -- a successful call already
  # blocked for up to POLL_TIMEOUT seconds inside Telegram's own long poll.
  [ "$rc" -ne 0 ] && sleep "$(( fail_count < 12 ? fail_count * BACKOFF_BASE : 60 ))"
done
