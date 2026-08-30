#!/bin/bash
# Exit-trap wiring for watchdog.sh (agent-supervisor#704 split):
# advance_on_exit() (advance the pinned LIVE worktree once a tick's other
# duties are done, only from the pinned copy, held during an escalation)
# and on_exit(), the actual trap handler that runs every check_*
# function from watchdog-checks.sh on every exit path and then calls
# advance_on_exit. Sets `trap on_exit EXIT` itself, at the bottom of this
# file, so sourcing this file is what arms the trap -- watchdog.sh does not
# need a separate trap line of its own. Sourced only -- not meant to be run
# standalone.
#
# Depends on watchdog-status.sh (real_path, advance_note, log, last_state)
# and watchdog-checks.sh (every check_* function called from on_exit) both
# being sourced before this file, and on globals watchdog.sh's own preamble
# sets ($HERE, $STATUS, $LIVE, $STATE, $sha).

# --- advancing the code this watchdog itself runs from ---------------------
#
# The LaunchAgent runs this file out of a PINNED detached worktree
# ($SUPERVISOR_LIVE). Nothing about a merge updates that worktree, so the
# `code:` line above exists to say how far behind it is. For a while that was
# the whole story: report the drift, and let the supervisor loop run
# advance-live.sh once per tick to act on it.
#
# That put the fixer in the component that goes down. The loop is exactly what
# stops running when something is wrong -- and during a deliberate escalation
# it is down BY DESIGN -- so the live worktree stopped advancing precisely
# during the incidents it most needed to be current for. The guard ran its
# stalest code in the one situation it exists to handle. Observed 2026-08-11:
# `1 behind origin/main` held across an escalation while the loop slept; the
# missed commit was benign, and would not have been if it had been a fix to
# this file.
#
# The objection this design was originally rejected over is real and is
# answered by the gate, not by refusing to deploy: "a broken watchdog would
# reinstall itself every 180s and nothing would be left to notice."
# advance-live.sh does not move the pin until the candidate commit's OWN
# watchdog.sh has been run from a throwaway worktree and has written a
# well-formed status. A candidate that cannot run cannot be installed, so the
# copy that would be left unable to notice anything never becomes live. That
# check is not reimplemented here; this calls the one script that owns it.
#
# WHY ON THE WAY OUT, AND NOT AT THE TOP:
#   1. Every duty of this tick is already done. An advance can therefore not
#      cost the tick its status write, its restart, or its page.
#   2. advance-live.sh only advances in the window just after a watchdog tick,
#      read from watchdog.status's own `checked:` line. Called at the top of a
#      tick it would read the PREVIOUS tick's timestamp -- ~180s old, outside
#      the window -- and skip forever. Called here it reads the line this tick
#      just wrote.
#   3. Bash reads a script incrementally, by file offset. Checking out over
#      the file a running bash is still reading is how a script executes
#      garbage. An EXIT trap is the one place where nothing further will be
#      read from this file: the trap body is already in memory and the shell
#      exits when it returns. For the same reason advance-live.sh is run from
#      a COPY -- it lives beside this file and would otherwise be overwritten
#      mid-run by its own checkout.
#   4. Most ticks exit early (working / asleep / waiting_on_jon). A call
#      placed inline at the bottom would only ever run on the restart path.
#
# AND ONLY FROM THE PINNED COPY. The identity check below is what keeps this
# from checking out over a developer's own checkout during a test run, and
# what stops the smoke run advance-live.sh performs -- which executes a
# candidate watchdog.sh, this code included -- from recursing into a second
# advance.
#
# DURING AN ESCALATION, IT HOLDS. This is a deliberate choice against the
# opposite one, so the argument belongs here rather than the conclusion alone:
#
#   For advancing: escalation is when stale guard code is most dangerous, and
#   if the escalation is caused by a watchdog bug, the fix sitting on main is
#   the thing that ends it.
#
#   Against, and decisive: escalation is the ONE state in which a human has
#   already been paged. Staleness is then bounded by a person who is on their
#   way and who can run advance-live.sh by hand; that is how the live worktree
#   was advanced before any of this was automated. Set against that bound, the
#   cost of advancing is unbounded: the sha in the status file a human was
#   paged with must still be the sha they find when they look, or they are
#   debugging a system that is rewriting itself underneath the diagnosis. A
#   merge is also a leading suspect for whatever made the loop die three times
#   in an hour, and pulling further changes into a live incident is the way to
#   turn one unknown into several. Freezing is trivially reversible by hand;
#   redeploying mid-diagnosis is not reversible at all in the sense that
#   matters, because the confusion has already happened.
#
#   Residual risk, named rather than papered over: if the page never got out
#   (`notify:` says FAILED) nobody is coming, and the freeze outlasts the
#   incident. That is bounded by this watchdog retrying the page every tick.
#   Gating the hold on delivery was considered and rejected: it makes whether
#   the estate deploys itself depend on whether a phone had signal.

# Runs on EVERY exit path. It must never change this tick's exit status and
# must never abort it: a refused advance is a report, not a crash -- the tick
# it rode out on had already succeeded, and failing it would turn "the code is
# one commit stale" into "the watchdog is down". Takes rc as an argument
# rather than reading $? itself: it is no longer the trap handler directly
# (on_exit, below, is, so it can also run check_inbox_heartbeat on every exit
# path) and $? inside a called function reflects THAT call, not the script's.
advance_on_exit() {
  local rc="$1"
  rm -f "$STATUS.$$" 2>/dev/null

  local root
  root=$(git -C "$HERE" rev-parse --show-toplevel 2>/dev/null) || return $rc
  [ -n "$root" ] || return $rc
  [ "$(real_path "$root")" = "$(real_path "$LIVE")" ] || return $rc
  [ -f "$HERE/advance-live.sh" ] || {
    log "ADVANCE-UNAVAILABLE: no advance-live.sh beside this watchdog"
    advance_note "unavailable — no advance-live.sh beside this watchdog"
    return $rc
  }

  if [ "$last_state" = escalate ]; then
    log "ADVANCE-HELD: escalation in effect — leaving the live copy at $sha for the human who was paged"
    advance_note "held — escalation in effect, live copy left at $sha for diagnosis; run advance-live.sh by hand to move it"
    return $rc
  fi

  # The copy: see point 3 above. Deleted whatever happens, including when the
  # advance it performed replaced the original underneath it.
  #
  # Every file advance-live.sh reaches for via $HERE must land beside the
  # copy too, or its checks silently no-op inside copy_dir (agent-supervisor
  # #57: poller-recover.sh was missing here, so the watchdog's every-tick
  # relaunch attempt gave up before starting, every time, with only a log
  # line to show for it). Keep this list in sync with `grep -n '\$HERE/'
  # advance-live.sh`.
  #
  # poller-window.sh is a HARD dependency (advance-live.sh sources it
  # unconditionally near the top): missing it aborts the copy entirely, the
  # same refuse-rather-than-run posture the rest of this function already
  # takes. poller-recover.sh is not: advance-live.sh already degrades
  # gracefully when it is missing (POLLER-PROMPT-RELAUNCH-SKIPPED, #48/#56),
  # falling back to the watchdog's own periodic backstop call below. Refusing
  # the WHOLE advance over a missing poller-recover.sh would trade a live-code
  # freeze for a poller-relaunch degradation that already reports itself
  # loudly through its own path -- worse than the bug this fixes. So this
  # copies it best-effort and logs loudly on failure, but never blocks the
  # advance on it.
  local copy_dir copy out arc
  copy_dir=$(mktemp -d "${TMPDIR:-/tmp}/watchdog-advance-live.XXXXXX" 2>/dev/null) || return $rc
  copy="$copy_dir/advance-live.sh"
  cp "$HERE/advance-live.sh" "$copy" 2>/dev/null || { rm -rf "$copy_dir" 2>/dev/null; return $rc; }
  cp "$HERE/poller-window.sh" "$copy_dir/poller-window.sh" 2>/dev/null || { rm -rf "$copy_dir" 2>/dev/null; return $rc; }
  cp "$HERE/session-defaults.sh" "$copy_dir/session-defaults.sh" 2>/dev/null || { rm -rf "$copy_dir" 2>/dev/null; return $rc; }
  # -p: poller-recover.sh is exec'd directly (not sourced via bash), so its
  # executable bit must survive the copy, not fall through the umask.
  if ! cp -p "$HERE/poller-recover.sh" "$copy_dir/poller-recover.sh" 2>/dev/null; then
    log "ADVANCE-COPY-INCOMPLETE: poller-recover.sh could not be copied beside advance-live.sh -- the prompt poller relaunch will no-op this tick, watchdog poller-recover.sh remains the backstop"
  fi
  # agent-supervisor#666: see refresh_checked_for_advance's own comment.
  # Right here, immediately before the hand-off, not any earlier -- every
  # check_* call above this point in on_exit() is exactly the same-tick work
  # this re-stamp exists to stop being counted against advance-live.sh's
  # post-tick window.
  refresh_checked_for_advance
  out=$(SUPERVISOR_STATE="$STATE" SUPERVISOR_STATUS="$STATUS" bash "$copy" "$root" 2>&1)
  arc=$?
  # advance-live.sh's prompt_poller_relaunch backgrounds a waiter that execs
  # poller-recover.sh only once the OLD poller pid exits -- up to
  # INBOX_POLL_RELAUNCH_WAIT_SECONDS later -- and returns immediately itself,
  # so "bash $copy" above has already come back by the time that waiter is
  # still running. Deleting copy_dir synchronously here race-deletes
  # poller-recover.sh out from under it before it ever gets exec'd
  # (agent-supervisor#57: this reproduced as ENOENT even with poller-recover.sh
  # correctly copied in above). Defer the cleanup past that waiter's own
  # deadline instead of racing it.
  ( sleep "$(( ${INBOX_POLL_RELAUNCH_WAIT_SECONDS:-45} + 15 ))"; rm -rf "$copy_dir" ) >/dev/null 2>&1 &
  out=$(printf '%s' "$out" | tr '\n' ' ')

  if [ "$arc" -ne 0 ]; then
    log "ADVANCE-REFUSED rc=$arc: $out"
    advance_note "refused — $out"
  elif [[ "$out" == *"advance-live: advanced"* ]]; then
    log "ADVANCED: $out"
    advance_note "advanced — $out (the code: line above is what THIS tick ran)"
  elif [ -z "$out" ] || [[ "$out" == *"advance-live: current"* ]]; then
    # agent-supervisor#11: advance-live.sh now fetches before it can call
    # this "current" -- so the empty-output case (an older candidate that
    # predates that fetch) and the explicit "advance-live: current" report
    # both mean the same thing here: genuinely current, not merely silent.
    advance_note "current — ${out:-live copy already at origin/main (fetched fresh)}"
  else
    # A gate declining is the ordinary case, not a fault: outside the post-tick
    # window, no status to compare against yet. Says so without shouting.
    advance_note "not this tick — $out"
  fi
  return $rc
}

# The actual trap handler. Captures $? FIRST, exactly as advance_on_exit used
# to do directly -- everything after this line runs commands of its own that
# would otherwise clobber it.
on_exit() {
  local rc=$?
  check_inbox_heartbeat
  check_quota_watch_heartbeat
  check_weekly_watch_heartbeat
  check_director_inbox
  check_poller_process_count
  check_poller_window
  check_quota_watch_recover
  check_source_task_sweep
  check_lane_completion_sweep
  check_never_busy_lanes
  check_worktree_guard_audit
  check_worktree_gc_sweep
  advance_on_exit "$rc"
  return $rc
}
trap on_exit EXIT
