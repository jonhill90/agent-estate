#!/bin/bash
# Status-file and log helpers for watchdog.sh (agent-supervisor#704 split):
# log(), report() (the atomic watchdog.status writer plus the escalate
# notifier hookup), refresh_checked_for_advance(), real_path(), and the
# per-subsystem `advance_note`/`heartbeat_note`/.../`gc_sweep_note` append
# functions the check_* functions in watchdog-checks.sh write through.
# Sourced only -- not meant to be run standalone. See watchdog.sh's own
# header for the split's shape.
#
# Depends on globals watchdog.sh sets before sourcing this file: $iso,
# $LOG, $STATUS, $PANE, $ESCALATE_WINDOW, $recent (report() reads it via
# ${recent:-0}, set later in watchdog.sh's own main body -- report() is not
# called until after that point), $repo, $branch, $sha, $code_note, $HERE,
# $STATE, $NOTIFY_SCRIPT. Defines the global `last_state`, read by
# advance_on_exit() in watchdog-advance.sh.

log() { printf '%s %s\n' "$iso" "$*" >>"$LOG"; }

# Heartbeat. Written on EVERY exit path, including the healthy one — that is
# the whole point of finding 2. Atomic so a reader never sees a half file.
report() {                       # report <state> <detail> [notify-line]
  local tmp="$STATUS.$$"
  # Remembered for the advance step, which runs on the way out of EVERY exit
  # path and has to know which one it was. Reading it back off the status file
  # would work today and break the first time a path exits without reporting.
  last_state="$1"
  # A missing state directory used to make every write fail silently: the
  # script still exited 0 while watchdog.status quietly stopped updating,
  # which is indistinguishable from a dead cron -- the exact failure this
  # tool exists to detect, occurring inside the tool itself.
  mkdir -p "$(dirname "$STATUS")" 2>/dev/null
  {
    printf 'checked:  %s\n' "$iso"
    printf 'state:    %s\n' "$1"
    printf 'detail:   %s\n' "${2:-}"
    printf 'pane:     %s\n' "$PANE"
    printf 'restarts: %s in the last %ss\n' "${recent:-0}" "$ESCALATE_WINDOW"
    # Which code is actually running. The LaunchAgent executes this file from
    # the repo WORKING TREE, so whichever branch happens to be checked out is
    # what guards the loop. On 2026-08-11 the live watchdog spent a stretch
    # running from a test branch purely because that was the last checkout --
    # it worked, but by luck. An unexpected branch here is a real finding.
    printf 'code:     %s@%s @ %s%s\n' "$repo" "$branch" "$sha" "$code_note"
    # Present only when a send was attempted and failed (#91). "escalate with
    # no notify: line" is therefore "a human was reached"; this line is the
    # difference between that and "the loop is down and NOBODY KNOWS". Written
    # on the second pass below, because the notifier has to read this file to
    # decide before there is any outcome to report.
    #
    # An `if`, not `[ ... ] && printf`: a false test as the LAST command in
    # this group makes the group exit non-zero, the `&& mv` below never runs,
    # and every tick reports CANNOT WRITE STATUS instead of writing one.
    if [ -n "${3:-}" ]; then printf 'notify:   %s\n' "$3"; fi
  } >"$tmp" 2>/dev/null && mv -f "$tmp" "$STATUS" 2>/dev/null \
    || printf '%s WATCHDOG CANNOT WRITE STATUS to %s\n' "$iso" "$STATUS" >&2

  # A recursive call carrying the outcome; it must not run the notifier again.
  if [ -n "${3:-}" ]; then return 0; fi

  # escalate is the only state a human needs told about; every other state
  # (working/waiting_on_jon/cooling_down/restarted/...) stays silent, and
  # dedup is one message per escalation episode, not one per tick. That
  # decision and its dedup state live in tracked, tested code — this line
  # is the whole hookup. See scripts/supervisor/watchdog_notify.py in
  # agent-dotfiles (#50). Resolved from $HERE, not from a guessed clone
  # path: the notifier ships beside this script, so the copy that runs is
  # always the copy that was reviewed alongside the watchdog invoking it --
  # and a test running this file from a worktree exercises that worktree's
  # notifier rather than whatever happens to be in the shared checkout.
  local notify_out notify_rc
  notify_out=$(python3 "$HERE/watchdog_notify.py" \
    --status-path "$STATUS" \
    --episode-state-path "$STATE/.watchdog-escalate-episode.json" \
    --log-path "$STATE/watchdog-notify.log" \
    --notify-script "${NOTIFY_SCRIPT:-}" 2>&1)
  notify_rc=$?
  if [ "$notify_rc" -ne 0 ]; then
    log "NOTIFY-CHECK rc=$notify_rc: $notify_out"
    # Say so in the one file a human `cat`s. The log is append-only and easy
    # to scroll past; watchdog.status is the answer to "where are we", and
    # "escalate" alone reads as "Jon has been told" when the truth may be
    # that nothing got out. Newlines collapsed so the field stays one line.
    report "$1" "${2:-}" "FAILED — escalation did NOT reach a human, retrying next tick: $(printf '%s' "$notify_out" | tr '\n' ' ')"
  fi
}

# --- agent-supervisor#666: refresh checked: right before advance-live.sh ---
#
# `$iso` (and so every `checked:` line report() writes) is stamped ONCE, at
# this file's own top, before ANY of this tick's own work runs -- including
# every check_* call in on_exit() above advance_on_exit, and including
# advance-live.sh's own fetch + smoke test once THAT starts. advance-live.sh's
# post-tick window (agent-supervisor#654) re-reads that same `checked:` line
# and refuses to mutate LIVE once too much time has passed since it -- correct
# for loop-tick.md's OTHER caller, which has no relationship to this tick and
# genuinely needs to know how long ago the watchdog last confirmed alive.
#
# For THIS caller it measures the wrong thing. advance-live.sh's own header
# says the window is "satisfied by construction" when called from the
# watchdog's own exit path, "the timestamp being read was written seconds
# earlier by the caller" -- true when this function ran right after report().
# It no longer does: on_exit() (#199, #205, #450, #526...) now runs a dozen
# other checks first, and one of them, worktree-guard-audit.sh, has its own
# 120s timeout and was measured failing that timeout on 131-132 consecutive
# ticks in this estate's own watchdog.log before this fix -- eating most of
# the window by itself before advance-live.sh is ever invoked. Measured live
# (agent-supervisor#666): "recheck age 874s" and "920s" against a 150s
# window, with EVERY logged attempt in advance-live.log refusing the same
# way back to 2026-08-25 -- not a one-off stale read, a structurally
# unsatisfiable gate for this caller.
#
# The fix is not a bigger number (the issue is explicit: a window widened
# without knowing what it guards is worse than one that is too tight) and it
# is not skipping the check for this caller either -- advance-live.sh's OWN
# fetch + smoke test can still legitimately overrun the window (a hung
# candidate, a slow remote), and that must still refuse; the re-check
# immediately before the mutation (advance-live.sh's own, untouched by this
# fix) still catches that case.
#
# What is wrong is only the REFERENCE POINT: re-stamp `checked:` to "now"
# right here, immediately before handing off, so the window advance-live.sh
# re-reads bounds ONLY the work that is actually this caller's own (the
# fetch and the smoke test) -- restoring the "seconds earlier" property the
# design always assumed, without changing TICK_INTERVAL, SAFETY_BUFFER, or
# any guard advance-live.sh itself applies. Truthful, not a workaround: the
# watchdog genuinely is still alive and mid-exit-trap at this instant, so
# recording that instant as its own most recent confirmation is accurate,
# not merely convenient.
#
# Rewrites only the `checked:` line, atomically (tmp+mv, same pattern
# report() uses), and leaves every other field (state/detail/pane/
# restarts/code/advance/notify) exactly as report() last wrote them --
# nothing here should overwrite this tick's own outcome before it is known.
# Best-effort: a failed refresh leaves the older timestamp in place, which
# only reintroduces the pre-fix behaviour for this one tick, never something
# worse. Never touches notification/dedup state -- this is not a new report,
# it is the existing report's own timestamp catching up to reality.
refresh_checked_for_advance() {
  [ -f "$STATUS" ] || return 0
  local tmp now
  tmp="$STATUS.refresh.$$"
  now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  awk -v now="$now" '
    /^checked:/ { print "checked:  " now; done=1; next }
    { print }
    END { if (!done) print "checked:  " now }
  ' "$STATUS" >"$tmp" 2>/dev/null && [ -s "$tmp" ] && mv -f "$tmp" "$STATUS" 2>/dev/null
  rm -f "$tmp" 2>/dev/null
  return 0
}

last_state=""

# Physical path, so a symlink anywhere in $HOME cannot stop the live copy from
# recognising itself and quietly disable the whole mechanism.
real_path() { (cd "$1" 2>/dev/null && pwd -P) || printf '%s' "$1"; }

# Add or replace the `advance:` line in the status file. Additive by
# construction: the report written above is the contract, this is one more
# line on the end of it. Written with the same tmp+rename the report uses, so
# a reader never sees a half file.
advance_note() {                 # advance_note <line>
  local tmp="$STATUS.adv.$$"
  [ -f "$STATUS" ] || return 0
  { grep -v '^advance:' "$STATUS"; printf 'advance:  %s\n' "$1"; } >"$tmp" 2>/dev/null
  # Only rename a file that has content. A truncated write reaching $STATUS
  # would erase the tick's whole report to add one line to it.
  if [ -s "$tmp" ]; then mv -f "$tmp" "$STATUS" 2>/dev/null; fi
  rm -f "$tmp" 2>/dev/null
  return 0
}

# Same tmp+rename append advance_note uses, for a second, unrelated line: the
# result of #163's inbox-poll heartbeat check. Kept as its own function rather
# than generalizing advance_note, so a change to one line's format cannot
# silently reach the other.
heartbeat_note() {               # heartbeat_note <line>
  local tmp="$STATUS.hb.$$"
  [ -f "$STATUS" ] || return 0
  { grep -v '^heartbeat:' "$STATUS"; printf 'heartbeat: %s\n' "$1"; } >"$tmp" 2>/dev/null
  if [ -s "$tmp" ]; then mv -f "$tmp" "$STATUS" 2>/dev/null; fi
  rm -f "$tmp" 2>/dev/null
  return 0
}

# Same tmp+rename append shape again, for agent-supervisor#276's quota-watch
# heartbeat check. A distinct line from `heartbeat:` (which is inbox-poll's)
# rather than reusing it -- two different subsystems' staleness must both
# stay visible in one `cat watchdog.status`, not overwrite each other.
quota_watch_note() {             # quota_watch_note <line>
  local tmp="$STATUS.qw.$$"
  [ -f "$STATUS" ] || return 0
  { grep -v '^quota-watch:' "$STATUS"; printf 'quota-watch: %s\n' "$1"; } >"$tmp" 2>/dev/null
  if [ -s "$tmp" ]; then mv -f "$tmp" "$STATUS" 2>/dev/null; fi
  rm -f "$tmp" 2>/dev/null
  return 0
}

# Same tmp+rename append shape again, for agent-supervisor#341's
# weekly-watch heartbeat check. A distinct line from `quota-watch:` --
# two different schedulers watching two different quota windows, and
# collapsing them into one field would hide either one's staleness behind
# whichever ran last.
weekly_watch_note() {            # weekly_watch_note <line>
  local tmp="$STATUS.ww.$$"
  [ -f "$STATUS" ] || return 0
  { grep -v '^weekly-watch:' "$STATUS"; printf 'weekly-watch: %s\n' "$1"; } >"$tmp" 2>/dev/null
  if [ -s "$tmp" ]; then mv -f "$tmp" "$STATUS" 2>/dev/null; fi
  rm -f "$tmp" 2>/dev/null
  return 0
}

# Same tmp+rename append shape again, for the process-table measurement of
# inbox-poll.sh. This line is intentionally absent in the healthy one-poller
# case: the detector should be silent when the process table is exactly right.
poller_note() {                  # poller_note <line>
  local tmp="$STATUS.poller.$$"
  [ -f "$STATUS" ] || return 0
  { grep -v '^poller:' "$STATUS"; printf 'poller:    %s\n' "$1"; } >"$tmp" 2>/dev/null
  if [ -s "$tmp" ]; then mv -f "$tmp" "$STATUS" 2>/dev/null; fi
  rm -f "$tmp" 2>/dev/null
  return 0
}

# Same tmp+rename append shape again, for as#151's director-inbox staleness
# check. Written EVERY tick, stale or not -- this is the visibility half of
# the fix (requirement #3), not just the alarm half: a human running
# `cat watchdog.status` sees the oldest-pending age unconditionally, the
# same way `heartbeat:`/`poller:` already answer "where are we" without
# anyone having to reach for digest.sh or wait for a Director tick.
inbox_note() {                   # inbox_note <line>
  local tmp="$STATUS.inbox.$$"
  [ -f "$STATUS" ] || return 0
  { grep -v '^inbox:' "$STATUS"; printf 'inbox:    %s\n' "$1"; } >"$tmp" 2>/dev/null
  if [ -s "$tmp" ]; then mv -f "$tmp" "$STATUS" 2>/dev/null; fi
  rm -f "$tmp" 2>/dev/null
  return 0
}

# Same tmp+rename append shape again, for the recovery mechanism's own
# availability. Absent is different from present-but-not-runnable: the first is
# a partial install, the second is a broken install with a concrete chmod fix.
recovery_note() {                # recovery_note <line>
  local tmp="$STATUS.recovery.$$"
  [ -f "$STATUS" ] || return 0
  { grep -v '^recovery:' "$STATUS"; printf 'recovery: %s\n' "$1"; } >"$tmp" 2>/dev/null
  if [ -s "$tmp" ]; then mv -f "$tmp" "$STATUS" 2>/dev/null; fi
  rm -f "$tmp" 2>/dev/null
  return 0
}

# Same tmp+rename append shape again, for agent-supervisor#276's
# quota-watch-recover.sh outcome. A separate line from `recovery:` (the
# inbox poller's) -- two different recovery mechanisms for two different
# processes, and collapsing them into one field would hide either restart
# behind whichever ran last.
quota_watch_recovery_note() {    # quota_watch_recovery_note <line>
  local tmp="$STATUS.qwrecovery.$$"
  [ -f "$STATUS" ] || return 0
  { grep -v '^quota-watch-recover:' "$STATUS"; printf 'quota-watch-recover: %s\n' "$1"; } >"$tmp" 2>/dev/null
  if [ -s "$tmp" ]; then mv -f "$tmp" "$STATUS" 2>/dev/null; fi
  rm -f "$tmp" 2>/dev/null
  return 0
}

# Same tmp+rename append shape again, for the source_tasks sweep (#133).
# Absent (no line at all) means "not due this tick", the ordinary case --
# only a tick that actually ran the sweep, or found it could not, writes one.
sweep_note() {                   # sweep_note <line>
  local tmp="$STATUS.sweep.$$"
  [ -f "$STATUS" ] || return 0
  { grep -v '^sweep:' "$STATUS"; printf 'sweep:    %s\n' "$1"; } >"$tmp" 2>/dev/null
  if [ -s "$tmp" ]; then mv -f "$tmp" "$STATUS" 2>/dev/null; fi
  rm -f "$tmp" 2>/dev/null
  return 0
}

# Same tmp+rename append shape again, for the lane-completion sweep (#155).
lane_sweep_note() {              # lane_sweep_note <line>
  local tmp="$STATUS.lanesweep.$$"
  [ -f "$STATUS" ] || return 0
  { grep -v '^lane-sweep:' "$STATUS"; printf 'lane-sweep: %s\n' "$1"; } >"$tmp" 2>/dev/null
  if [ -s "$tmp" ]; then mv -f "$tmp" "$STATUS" 2>/dev/null; fi
  rm -f "$tmp" 2>/dev/null
  return 0
}

# Same tmp+rename append shape again, for #112's never-busy lane check.
# Absent (no line at all) means every worker lane has either gone ready,
# busy, or is unclassified for a normal reason -- the ordinary case, silent
# on purpose like poller_note.
never_busy_note() {              # never_busy_note <line>
  local tmp="$STATUS.neverbusy.$$"
  [ -f "$STATUS" ] || return 0
  { grep -v '^never-busy:' "$STATUS"; printf 'never-busy: %s\n' "$1"; } >"$tmp" 2>/dev/null
  if [ -s "$tmp" ]; then mv -f "$tmp" "$STATUS" 2>/dev/null; fi
  rm -f "$tmp" 2>/dev/null
  return 0
}

# Same tmp+rename append shape again, for agent-supervisor#199/#205's
# continuous worktree-guard-audit wiring. Written whether the check ran clean,
# found a gap, or could not run at all -- `cat watchdog.status` is where a
# human looks first, the same posture every other note function here takes.
guard_audit_note() {             # guard_audit_note <line>
  local tmp="$STATUS.guardaudit.$$"
  [ -f "$STATUS" ] || return 0
  { grep -v '^guard-audit:' "$STATUS"; printf 'guard-audit: %s\n' "$1"; } >"$tmp" 2>/dev/null
  if [ -s "$tmp" ]; then mv -f "$tmp" "$STATUS" 2>/dev/null; fi
  rm -f "$tmp" 2>/dev/null
  return 0
}

# Same tmp+rename append shape again, for agent-supervisor#526's worktree-gc
# sweep. Written whether the sweep ran dry, ran live, or could not run at all
# -- same posture every other note function here takes.
gc_sweep_note() {                # gc_sweep_note <line>
  local tmp="$STATUS.gcsweep.$$"
  [ -f "$STATUS" ] || return 0
  { grep -v '^worktree-gc:' "$STATUS"; printf 'worktree-gc: %s\n' "$1"; } >"$tmp" 2>/dev/null
  if [ -s "$tmp" ]; then mv -f "$tmp" "$STATUS" 2>/dev/null; fi
  rm -f "$tmp" 2>/dev/null
  return 0
}
