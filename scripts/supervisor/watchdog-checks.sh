#!/bin/bash
# The on-exit check_* functions for watchdog.sh (agent-supervisor#704
# split): inbox-poll/quota-watch/weekly-watch heartbeat staleness, the
# director-inbox staleness check, the poller process-count and window
# checks, the quota-watch recovery call, the source-task and
# lane-completion sweeps, the never-busy worker-lane check, the
# worktree-guard-audit wiring, and the worktree-gc sweep. All of these run
# from on_exit() (watchdog-advance.sh) on EVERY exit path, independent of
# which branch of the main busy/idle/asleep logic short-circuited a given
# tick -- see each function's own header comment for why. Sourced only --
# not meant to be run standalone.
#
# Depends on the note-function/log/report helpers in watchdog-status.sh
# (sourced before this file) and on the many $SUPERVISOR_*-overridable
# globals watchdog.sh's own preamble sets (state paths, episode files,
# thresholds, stamps, intervals -- see that preamble for each one).

# Runs on EVERY exit path, regardless of which early `exit 0` above fired --
# that is the whole reason this lives in the trap rather than in the main
# body: the supervisor-loop checks below (busy/idle/asleep/...) all return
# early, but the inbox-poll heartbeat is a different subsystem entirely and
# must be read every tick no matter which of those branches this one took.
# Mirrors the escalate notifier's call shape (report(), above): read the
# episode-gated decision from watchdog_notify.py, log it, and surface it in
# watchdog.status even when the page itself could not be delivered (#163's
# "failure must be visible locally" constraint) -- notify.log and
# watchdog.status are what remain when the channel is the thing that broke.
check_inbox_heartbeat() {
  local notify_out notify_rc
  notify_out=$(python3 "$HERE/watchdog_notify.py" \
    --mode heartbeat \
    --heartbeat-status-path "$INBOX_POLL_STATUS_PATH" \
    --threshold-seconds "$INBOX_HEARTBEAT_STALE_AFTER" \
    --episode-state-path "$INBOX_HEARTBEAT_EPISODE" \
    --log-path "$STATE/watchdog-notify.log" \
    --notify-script "${NOTIFY_SCRIPT:-}" 2>&1)
  notify_rc=$?
  heartbeat_note "$(printf '%s' "$notify_out" | tr '\n' ' ')"
  if [ "$notify_rc" -ne 0 ]; then
    log "HEARTBEAT-CHECK FAILED rc=$notify_rc: $notify_out"
  else
    log "HEARTBEAT-CHECK: $notify_out"
  fi
}

# agent-supervisor#276: runs on EVERY exit path, same reasoning as
# check_inbox_heartbeat above -- quota-watch.sh is a different subsystem
# from the supervisor-loop checks (busy/idle/asleep/...) that return early
# throughout this file, and the whole point of watching it from here is that
# a hung quota-watch.sh leaves the loop itself looking perfectly healthy (it
# is the loop's own wake-up path that dies, not the loop). Identical shape to
# check_inbox_heartbeat, against a different status file and a separate
# episode -- the two staleness alarms must not share dedup state, the same
# discipline #163/as#151 already established for every other check here.
check_quota_watch_heartbeat() {
  local notify_out notify_rc
  notify_out=$(python3 "$HERE/watchdog_notify.py" \
    --mode heartbeat \
    --heartbeat-status-path "$QUOTA_WATCH_STATUS_PATH" \
    --threshold-seconds "$QUOTA_WATCH_HEARTBEAT_STALE_AFTER" \
    --episode-state-path "$QUOTA_WATCH_HEARTBEAT_EPISODE" \
    --log-path "$STATE/watchdog-notify.log" \
    --notify-script "${NOTIFY_SCRIPT:-}" 2>&1)
  notify_rc=$?
  quota_watch_note "$(printf '%s' "$notify_out" | tr '\n' ' ')"
  if [ "$notify_rc" -ne 0 ]; then
    log "QUOTA-WATCH-HEARTBEAT-CHECK FAILED rc=$notify_rc: $notify_out"
  else
    log "QUOTA-WATCH-HEARTBEAT-CHECK: $notify_out"
  fi
}

# agent-supervisor#341: runs on EVERY exit path, same reasoning as
# check_quota_watch_heartbeat above -- weekly-watch.sh is a different
# scheduler watching a different quota window, and the whole point of
# watching it from here is that a hung or never-firing weekly-watch.sh
# leaves everything else in this estate looking perfectly healthy (it is
# the ONLY alarm that tells Jon the weekly quota is nearly gone, and
# nothing else notices if it goes quiet). Identical shape to
# check_quota_watch_heartbeat, against a separate status file and episode
# -- the two staleness alarms must not share dedup state, the same
# discipline #163/#276 already established for every other check here.
check_weekly_watch_heartbeat() {
  local notify_out notify_rc
  notify_out=$(python3 "$HERE/watchdog_notify.py" \
    --mode heartbeat \
    --heartbeat-status-path "$WEEKLY_WATCH_STATUS_PATH" \
    --threshold-seconds "$WEEKLY_WATCH_HEARTBEAT_STALE_AFTER" \
    --episode-state-path "$WEEKLY_WATCH_HEARTBEAT_EPISODE" \
    --log-path "$STATE/watchdog-notify.log" \
    --notify-script "${NOTIFY_SCRIPT:-}" 2>&1)
  notify_rc=$?
  weekly_watch_note "$(printf '%s' "$notify_out" | tr '\n' ' ')"
  if [ "$notify_rc" -ne 0 ]; then
    log "WEEKLY-WATCH-HEARTBEAT-CHECK FAILED rc=$notify_rc: $notify_out"
  else
    log "WEEKLY-WATCH-HEARTBEAT-CHECK: $notify_out"
  fi
}

# as#151: runs on EVERY exit path, same reasoning as check_inbox_heartbeat
# above -- a different subsystem from the busy/idle/asleep checks that
# return early throughout this file, and it must be read every tick
# regardless of which of those branches fired, because a busy pane (the
# common, expected case) is exactly when this needs to keep watching.
check_director_inbox() {
  local notify_out notify_rc
  notify_out=$(python3 "$HERE/watchdog_notify.py" \
    --mode director-inbox \
    --director-inbox-bin "$DIRECTOR_INBOX_BIN" \
    --threshold-seconds "$DIRECTOR_INBOX_STALE_SECONDS" \
    --episode-state-path "$DIRECTOR_INBOX_EPISODE" \
    --log-path "$STATE/watchdog-notify.log" \
    --notify-script "${NOTIFY_SCRIPT:-}" 2>&1)
  notify_rc=$?
  inbox_note "$(printf '%s' "$notify_out" | tr '\n' ' ')"
  if [ "$notify_rc" -ne 0 ]; then
    log "DIRECTOR-INBOX-CHECK FAILED rc=$notify_rc: $notify_out"
  else
    log "DIRECTOR-INBOX-CHECK: $notify_out"
  fi
}

# agent-supervisor#18: inbox-poll.status is authored by the poller itself, and
# four duplicate pollers still reported one healthy pid because the last writer
# won. The duplicate detector must therefore ask the kernel, not the status
# file. It reports zero distinctly from more-than-one, and it never reaps: a
# wrong reap bounces the live inbound channel.
#
# agent-supervisor#147: measured live, the "second process" firing this check
# every ~3 minutes was neither the poller's own child (that pgid-matches its
# parent and is suppressed below) nor a second estate poller -- it was a
# watchdog test fixture's copy of inbox-poll.sh, launched from a mktemp'd
# sandbox and still alive because it survived SIGTERM (#104).
#
# A first version of this fix excluded any command path containing
# /var/folders/ or /tmp/, applied BEFORE parentage suppression even ran. A
# review of that PR (#194) constructed the adversarial case it invites: a
# GENUINE independent second poller -- unrelated ppid/pgid, deployed by hand
# from a temp checkout like /tmp/deploy-copy/... -- and the check went
# silent. Path alone proves nothing about whether a process holds ledger
# state or acks the production Telegram offset; it only proves where its
# script happens to live. That is exactly the shape #147 exists to catch, so
# a path-only exclusion is worse than the noise it silenced: nothing is
# watching, and nobody knows.
#
# The fix: parentage runs first, against EVERY process matching
# POLLER_SERVICE_RE, regardless of path -- the poller's own same-pgid child
# is suppressed exactly as before. Only a process that survives that (an
# unrelated ppid/pgid -- never the estate's own child) is even considered
# for the fixture exclusion, and even then it is excluded only when it
# carries a marker the test harness itself writes beside the fixture script
# (poller_fixture_marker, below) -- something a genuine second poller has no
# reason to ever have, accidentally or otherwise. No marker, no exclusion:
# per the ratchet direction (#124/#126), an unresolved "is this harmless?"
# makes the alert fire, never stays quiet.
#
# agent-supervisor#382: poller_fixture_marker/poller_is_verified_fixture/
# poller_process_rows moved to poller-lib.sh (sourced above) so
# poller-leak-cleanup.sh enumerates the exact same population this alarm
# does, instead of a second hand-rolled `pgrep -f inbox-poll.sh` that could
# silently drift from it. This function filters poller_process_rows' fixture
# column itself -- the ONLY thing that changed is where the rows come from.
check_poller_process_count() {
  local all_rows rows rows_rc count detail pid start fixture cmd
  all_rows=$(poller_process_rows)
  rows_rc=$?
  rows=$(awk -F'\t' '$3=="0"' <<<"$all_rows")
  if [ "$rows_rc" -ne 0 ]; then
    log "POLLER-CHECK FAILED rc=$rows_rc: could not measure live inbox-poll.sh processes from pgrep/ps"
    poller_note "unknown — could not measure live inbox-poll.sh processes from pgrep/ps"
    return 0
  fi
  count=$(grep -c . <<<"$rows" 2>/dev/null || true)
  if [ "$count" -eq 1 ]; then
    return 0
  fi
  if [ "$count" -eq 0 ]; then
    log "POLLER-CHECK: zero live inbox-poll.sh processes by pid — dead-poller recovery handles this"
    poller_note "dead — zero live inbox-poll.sh processes by pid; recovery handles this"
    return 0
  fi

  detail="${count} live inbox-poll.sh processes by pid"
  while IFS=$'\t' read -r pid start fixture cmd; do
    [ -n "$pid" ] || continue
    detail="$detail; pid $pid started ${start:-unknown}"
  done <<<"$rows"
  log "POLLER-DUPLICATE: $detail"
  poller_note "DUPLICATE — $detail"
  return 0
}

# agent-supervisor#10: a poller that exits takes its window with it, so
# nothing is left for the cooperative restart path to address. Runs on EVERY
# exit path, same reasoning as check_inbox_heartbeat above -- this is a
# different subsystem from the supervisor-loop checks (busy/idle/asleep/...)
# that return early throughout this file, and has to be read every tick
# regardless of which of those branches this one took. poller-recover.sh
# owns its own idempotency (a lock around window creation, and a respawn
# that can only ever land on the one pane it already found) -- nothing here
# needs to serialize it further.
check_poller_window() {
  if [ ! -e "$HERE/poller-recover.sh" ]; then
    log "POLLER-RECOVER-MISSING: poller-recover.sh is missing beside this watchdog; reinstall or advance the live worktree"
    recovery_note "missing — poller-recover.sh is missing beside this watchdog; reinstall or advance the live worktree"
    return 0
  fi
  if [ ! -x "$HERE/poller-recover.sh" ]; then
    log "POLLER-RECOVER-BROKEN: poller-recover.sh exists but is not executable; run chmod +x $HERE/poller-recover.sh or restore the committed 100755 mode"
    recovery_note "broken — poller-recover.sh exists but is not executable; run chmod +x $HERE/poller-recover.sh or restore the committed 100755 mode"
    return 0
  fi
  local out rc
  # The poller lives in the same session $PANE does -- derived from it
  # rather than a second independent default, so a deployment that points
  # SUPERVISOR_PANE at a non-default session does not leave poller-recover.sh
  # quietly acting on (or missing) the wrong one. LANES_SESSION, if the
  # caller already set it, still wins -- same override precedence lanes.sh
  # and advance-live.sh give it.
  out=$(SUPERVISOR_STATE="$STATE" SUPERVISOR_LIVE="$LIVE" \
        LANES_SESSION="${LANES_SESSION:-${PANE%%:*}}" \
        "$HERE/poller-recover.sh" 2>&1)
  rc=$?
  # agent-supervisor#41 (agent-supervisor#28): a poller-recover.sh that runs
  # and exits nonzero for a real reason (ambiguous windows, an orphan it
  # refuses to duplicate, tmux itself failing) used to reach only
  # watchdog.log -- FAILED rc=1 lines piled up there for 37 straight ticks
  # while digest.sh, which never reads watchdog.log, kept reporting
  # `poller: alive=true state=ok`. The recovery MECHANISM failing is a
  # different fact from the poller PROCESS being up, and only the log
  # captured it. recovery_note is the outcome field digest.sh reads; the
  # healthy path (rc=0) stays silent below, same as the missing/broken
  # checks above -- a recovery attempt that found nothing to fix is not
  # noise, only one that failed is.
  if [ "$rc" -ne 0 ]; then
    local streak=0
    [ -r "$POLLER_RECOVERY_FAIL_STREAK" ] && streak=$(cat "$POLLER_RECOVERY_FAIL_STREAK" 2>/dev/null)
    [[ "$streak" =~ ^[0-9]+$ ]] || streak=0
    streak=$((streak + 1))
    printf '%s' "$streak" >"$POLLER_RECOVERY_FAIL_STREAK" 2>/dev/null
    local last_success=""
    [ -r "$POLLER_RECOVERY_LAST_SUCCESS" ] && last_success=$(cat "$POLLER_RECOVERY_LAST_SUCCESS" 2>/dev/null)
    log "POLLER-RECOVER FAILED rc=$rc: $out"
    recovery_note "failed (attempt ${streak} in a row) — rc=$rc: $(printf '%s' "$out" | tr '\n' ' ') — last confirmed recovery: ${last_success:-never}"
  else
    printf '0' >"$POLLER_RECOVERY_FAIL_STREAK" 2>/dev/null
    printf '%s' "$iso" >"$POLLER_RECOVERY_LAST_SUCCESS" 2>/dev/null
    if [ -n "$out" ]; then
      log "POLLER-RECOVER: $out"
    fi
  fi
}

# agent-supervisor#276: runs on EVERY exit path, same reasoning as
# check_poller_window above -- restarting a hung quota-watch.sh is a
# different subsystem from the supervisor-loop checks (busy/idle/asleep/...)
# that return early throughout this file, and must not depend on the loop
# itself being idle to get a turn. quota-watch-recover.sh reads the SAME
# heartbeat stamp check_quota_watch_heartbeat above just alarmed on (or
# didn't) -- the alarm and the fix act on one fact, not two that could
# disagree, and the recover script is idempotent (its own mkdir lock, its
# own "nothing to do" branch on a fresh heartbeat) so calling it every tick
# is cheap on the ordinary healthy path.
check_quota_watch_recover() {
  if [ ! -e "$HERE/quota-watch-recover.sh" ]; then
    log "QUOTA-WATCH-RECOVER-MISSING: quota-watch-recover.sh is missing beside this watchdog; reinstall or advance the live worktree"
    quota_watch_recovery_note "missing — quota-watch-recover.sh is missing beside this watchdog; reinstall or advance the live worktree"
    return 0
  fi
  if [ ! -x "$HERE/quota-watch-recover.sh" ]; then
    log "QUOTA-WATCH-RECOVER-BROKEN: quota-watch-recover.sh exists but is not executable; run chmod +x $HERE/quota-watch-recover.sh or restore the committed 100755 mode"
    quota_watch_recovery_note "broken — quota-watch-recover.sh exists but is not executable; run chmod +x $HERE/quota-watch-recover.sh or restore the committed 100755 mode"
    return 0
  fi
  local out rc
  out=$(SUPERVISOR_STATE="$STATE" SUPERVISOR_LIVE="$LIVE" \
        QUOTA_WATCH_STATUS_PATH="$QUOTA_WATCH_STATUS_PATH" \
        QUOTA_WATCH_STALE_AFTER="$QUOTA_WATCH_HEARTBEAT_STALE_AFTER" \
        "$HERE/quota-watch-recover.sh" 2>&1)
  rc=$?
  if [ "$rc" -ne 0 ]; then
    log "QUOTA-WATCH-RECOVER FAILED rc=$rc: $out"
    quota_watch_recovery_note "failed — rc=$rc: $(printf '%s' "$out" | tr '\n' ' ')"
  elif [[ "$out" == *"RESTARTED"* ]]; then
    log "QUOTA-WATCH-RECOVER: $out"
    quota_watch_recovery_note "$(printf '%s' "$out" | tr '\n' ' ')"
  fi
}

# agent-supervisor#133: runs on EVERY exit path, same reasoning as
# check_poller_window/check_inbox_heartbeat above -- this is a different
# subsystem from the supervisor-loop checks (busy/idle/asleep/...) that
# return early throughout this file, and the whole point of putting it here
# is that it must still run on the ticks where those checks short-circuit.
# Self-throttled against SOURCE_SWEEP_STAMP (see that var's definition for
# the cost/cadence reasoning); most ticks return in the first branch below
# having done nothing.
check_source_task_sweep() {
  local last=0
  if [ -r "$SOURCE_SWEEP_STAMP" ]; then
    last=$(cat "$SOURCE_SWEEP_STAMP" 2>/dev/null)
  fi
  [[ "$last" =~ ^[0-9]+$ ]] || last=0
  if [ $(( now - last )) -lt "$SOURCE_SWEEP_INTERVAL" ]; then
    return 0
  fi
  if [ ! -e "$HERE/cli.py" ]; then
    log "SOURCE-SWEEP-MISSING: cli.py is missing beside this watchdog; reinstall or advance the live worktree"
    sweep_note "missing — cli.py is missing beside this watchdog"
    return 0
  fi
  local out rc
  out=$("${SUPERVISOR_PYTHON:-python3}" "$HERE/cli.py" --state-dir "$STATE" reconcile-source-tasks 2>&1)
  rc=$?
  # The stamp is written whether the sweep succeeded or not: a repo-fetch
  # failure inside the sweep already leaves its own rows untouched and
  # reports itself in `errors` (reconcile_sources.py's own fail-closed
  # contract) -- retrying every 180s instead of waiting out the interval
  # would not recover a down `gh`/network any faster, only spend more of the
  # rate-limit budget finding out again.
  printf '%s' "$now" >"$SOURCE_SWEEP_STAMP" 2>/dev/null
  if [ "$rc" -ne 0 ]; then
    log "SOURCE-SWEEP FAILED rc=$rc: $out"
    sweep_note "failed — rc=$rc: $(printf '%s' "$out" | tr '\n' ' ')"
    return 0
  fi
  # Review fix (PR #142): this formatter used to build its f-strings with
  # backslash-escaped double quotes inside the `{...}` expression --
  # `f"updated={len(d.get(\"updated\", []))} "` -- which is a SyntaxError on
  # every CPython from 3.9 to 3.14, not a version-specific issue. `python3
  # -c` therefore failed before running a single line, on every invocation,
  # success or not. That would have been visible immediately except the next
  # line redirected the formatter's stderr to /dev/null -- so a SUCCESSFUL
  # sweep (the row really did flip OPEN -> CLOSED) still rendered "not
  # parseable" in watchdog.status forever, and nothing short of reading the
  # Python by eye would have caught it. Fixed two ways: the f-strings below
  # hold no quote characters at all (values are computed into plain
  # variables first, so there is nothing left to escape), and the
  # formatter's stderr is now captured, not discarded -- a crash in the
  # formatter itself surfaces as FORMATTER-CRASHED, distinct from the sweep's
  # own report genuinely being unparseable JSON (a real, different outcome
  # `reconcile_sources.py` can also produce).
  local summary py_rc py_err py_err_file
  py_err_file="$STATUS.sweep-fmt-err.$$"
  summary=$(printf '%s' "$out" | "${SUPERVISOR_PYTHON:-python3}" -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    print("unparseable report")
    sys.exit(0)
updated = len(d.get("updated", []))
unchanged = len(d.get("unchanged", []))
unresolved = len(d.get("unresolved", []))
errors = len(d.get("errors", []))
print(f"updated={updated} unchanged={unchanged} unresolved={unresolved} errors={errors}")
' 2>"$py_err_file")
  py_rc=$?
  py_err=$(cat "$py_err_file" 2>/dev/null)
  rm -f "$py_err_file" 2>/dev/null
  if [ "$py_rc" -ne 0 ]; then
    log "SOURCE-SWEEP-FORMATTER-CRASHED rc=$py_rc: $py_err"
    sweep_note "formatter crashed — rc=$py_rc: $(printf '%s' "$py_err" | tr '\n' ' ')"
    return 0
  fi
  [ -n "$summary" ] || summary="ran, output not parseable: $(printf '%s' "$out" | tr '\n' ' ')"
  log "SOURCE-SWEEP: $summary"
  sweep_note "$summary"
  return 0
}

# agent-supervisor#155: runs on EVERY exit path, same reasoning as
# check_source_task_sweep above -- a lane that finishes and never signals
# does so regardless of which branch of this script's own busy/idle/asleep
# logic short-circuited this tick. Self-throttled against LANE_SWEEP_STAMP;
# most ticks return in the first branch below having done nothing, and the
# interval is short (120s default) because, unlike the source sweep, this
# one costs no external API call -- see LANE_SWEEP_INTERVAL's own comment.
check_lane_completion_sweep() {
  local last=0
  if [ -r "$LANE_SWEEP_STAMP" ]; then
    last=$(cat "$LANE_SWEEP_STAMP" 2>/dev/null)
  fi
  [[ "$last" =~ ^[0-9]+$ ]] || last=0
  if [ $(( now - last )) -lt "$LANE_SWEEP_INTERVAL" ]; then
    return 0
  fi
  if [ ! -e "$HERE/cli.py" ]; then
    log "LANE-SWEEP-MISSING: cli.py is missing beside this watchdog; reinstall or advance the live worktree"
    lane_sweep_note "missing — cli.py is missing beside this watchdog"
    return 0
  fi
  local out rc
  out=$("${SUPERVISOR_PYTHON:-python3}" "$HERE/cli.py" --state-dir "$STATE" \
        reconcile-lane-completions --idle-after "$LANE_SWEEP_IDLE_AFTER" \
        --stale-after "$LANE_SWEEP_STALE_AFTER" 2>&1)
  rc=$?
  # Stamp written whether the sweep succeeded or not -- same reasoning as
  # SOURCE-SWEEP: a failure already reports itself in `errors` (this
  # reconciler's own fail-closed contract, see reconcile_lane_completions.py),
  # and retrying every tick instead of waiting out the interval buys nothing.
  printf '%s' "$now" >"$LANE_SWEEP_STAMP" 2>/dev/null
  if [ "$rc" -ne 0 ]; then
    log "LANE-SWEEP FAILED rc=$rc: $out"
    lane_sweep_note "failed — rc=$rc: $(printf '%s' "$out" | tr '\n' ' ')"
    return 0
  fi
  local summary py_rc py_err py_err_file
  py_err_file="$STATUS.lanesweep-fmt-err.$$"
  summary=$(printf '%s' "$out" | "${SUPERVISOR_PYTHON:-python3}" -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    print("unparseable report")
    sys.exit(0)
completed = d.get("completed", [])
# agent-supervisor#193: never-accepted lanes the sweep now terminates
# failed instead of complete (see reconcile_lane_completions.py) -- named
# here for the same reason a completion is named, not just counted (issue
# 118 lesson): a human scanning watchdog.log must be able to tell "this
# lane was auto-completed" from "this lane was never accepted and failed"
# without opening the ledger. No apostrophes in this block: it is embedded
# in a single-quoted bash -c string, and a bare apostrophe here ends that
# string early and breaks the shell parse, not the python one.
failed_unaccepted = d.get("failed_unaccepted", [])
# agent-supervisor#374: non-tmux (claude-print/pi-rpc) lanes resolved on
# wall-clock dwell instead of an observed pane -- named separately from
# failed_unaccepted above for the same reason: the two are different
# evidence and a human scanning the log should be able to tell them apart.
failed_stale_delivery = d.get("failed_stale_delivery", [])
# agent-supervisor#414: a no-pane lane whose worker got as far as `accept`
# and then went silent -- distinct evidence again from failed_stale_delivery
# above (that one never even confirmed acceptance), named separately for the
# same reason.
failed_stale_acceptance = d.get("failed_stale_acceptance", [])
# agent-supervisor#488: a claude-print liveness check blocked a failure
# stamp this sweep would otherwise have written -- named separately from
# unresolved below (which also covers ordinary too-young rows) for the
# same reason the other categories are: a human scanning watchdog.log must
# be able to tell "still genuinely running" and "could not tell" apart from
# an unremarkable dwell.
liveness_alive = d.get("liveness_alive", [])
liveness_indeterminate = d.get("liveness_indeterminate", [])
unresolved = len(d.get("unresolved", []))
errors = len(d.get("errors", []))
names = ",".join(completed)
failed_names = ",".join(failed_unaccepted)
stale_names = ",".join(failed_stale_delivery)
stale_accepted_names = ",".join(failed_stale_acceptance)
alive_names = ",".join(liveness_alive)
indeterminate_names = ",".join(liveness_indeterminate)
print(
    f"completed={len(completed)} failed_unaccepted={len(failed_unaccepted)} "
    f"failed_stale_delivery={len(failed_stale_delivery)} "
    f"failed_stale_acceptance={len(failed_stale_acceptance)} unresolved={unresolved} errors={errors}"
    + (f" ({names})" if names else "")
    + (f" (never-accepted: {failed_names})" if failed_names else "")
    + (f" (no-pane-stale: {stale_names})" if stale_names else "")
    + (f" (no-pane-accepted-stale: {stale_accepted_names})" if stale_accepted_names else "")
    + (f" (liveness-blocked-failure: {alive_names})" if alive_names else "")
    + (f" (liveness-indeterminate: {indeterminate_names})" if indeterminate_names else "")
)
' 2>"$py_err_file")
  py_rc=$?
  py_err=$(cat "$py_err_file" 2>/dev/null)
  rm -f "$py_err_file" 2>/dev/null
  if [ "$py_rc" -ne 0 ]; then
    log "LANE-SWEEP-FORMATTER-CRASHED rc=$py_rc: $py_err"
    lane_sweep_note "formatter crashed — rc=$py_rc: $(printf '%s' "$py_err" | tr '\n' ' ')"
    return 0
  fi
  [ -n "$summary" ] || summary="ran, output not parseable: $(printf '%s' "$out" | tr '\n' ' ')"
  # Loud on purpose (#155 acceptance point 4, #118's lesson): a completed
  # lane names itself in the log, not just a count, so a human scanning
  # watchdog.log can tell which lane this sweep -- not a hand-written
  # `record-completion` -- released.
  log "LANE-SWEEP: $summary"
  lane_sweep_note "$summary"
  return 0
}

# agent-supervisor#112. Every check above this one watches a subsystem of
# THIS watchdog (the loop it restarts, the poller, source_tasks); this is
# the first to read WORKER lanes -- the panes lanes.sh classifies, in the
# same session poller-recover.sh already derives from $PANE
# (check_poller_window, above). Runs on every exit path for the same reason
# check_source_task_sweep does: the incident this exists for is the tick
# where the supervisor loop itself has nothing to say -- no dispatch, no
# restart, no escalation -- because zero worker capacity produces no signal
# of its own. If this only ran from inside a busy/idle branch below, the
# tick that most needed it would be the one skipping it.
#
# lanes.sh is the sole authority on the classification (#112's own design:
# time-based, not a new dialog shape to grep for here too) -- this function
# only reads its --json output, counts `never-busy` rows, and pages once per
# distinct set of stuck names via notify.sh, the path #118/#123 restored.
# agent-supervisor#163: the fail-streak bookkeeping and escalation for the
# never-busy check ITSELF being unable to run -- distinct from the check
# running and finding a stuck lane, which is the rest of check_never_busy_
# lanes below. <reason> is what goes in watchdog.log and never_busy_note;
# it does not itself page -- only crossing a multiple of
# NEVER_BUSY_CHECK_FAIL_ESCALATE_AFTER does, via the same notify.sh
# resolution the stuck-lane page below uses, so a relocated tree cannot
# silently lose this alarm either.
never_busy_check_failed() {
  local reason="$1" streak=0 notify_script notify_out notify_rc
  [ -r "$NEVER_BUSY_CHECK_FAIL_STREAK" ] && streak=$(cat "$NEVER_BUSY_CHECK_FAIL_STREAK" 2>/dev/null)
  [[ "$streak" =~ ^[0-9]+$ ]] || streak=0
  streak=$((streak + 1))
  printf '%s' "$streak" >"$NEVER_BUSY_CHECK_FAIL_STREAK" 2>/dev/null
  log "NEVER-BUSY-CHECK FAILED (streak ${streak}): $reason"
  never_busy_note "unknown — $reason (failed ${streak} check(s) in a row)"
  if [ $(( streak % NEVER_BUSY_CHECK_FAIL_ESCALATE_AFTER )) -ne 0 ]; then
    return 0
  fi
  notify_script="${NOTIFY_SCRIPT:-}"
  if [ -z "$notify_script" ] || [ ! -x "$notify_script" ]; then
    notify_script="$HERE/notify.sh"
  fi
  if [ ! -x "$notify_script" ]; then
    log "NEVER-BUSY-CHECK-ESCALATE-UNAVAILABLE: no notifier at $notify_script"
    return 0
  fi
  notify_out=$(AGENT_NOTIFY_CALLER=supervisor "$notify_script" \
    "never-busy safety check has failed ${streak} times in a row" \
    "agent-supervisor#163: the #112 stuck-lane detector cannot run: ${reason}. This has failed ${streak} consecutive ticks — the detector may be silently blind, the same shape #163 measured nine times in a row." 2>&1)
  notify_rc=$?
  if [ "$notify_rc" -ne 0 ]; then
    log "NEVER-BUSY-CHECK-ESCALATE-FAILED rc=$notify_rc: $notify_out"
  else
    log "NEVER-BUSY-CHECK-ESCALATE: $notify_out"
  fi
  return 0
}

check_never_busy_lanes() {
  if [ ! -x "$HERE/lanes.sh" ]; then
    never_busy_check_failed "lanes.sh is missing beside this watchdog"
    return 0
  fi
  local session out out_rc names names_rc count message prev notify_script notify_out notify_rc joined
  session="${LANES_SESSION:-${PANE%%:*}}"
  out=$("$HERE/lanes.sh" --json "$session" 2>&1)
  out_rc=$?
  if [ "$out_rc" -ne 0 ]; then
    never_busy_check_failed "lanes.sh --json $session: $(printf '%s' "$out" | tr '\n' ' ')"
    return 0
  fi
  # `sort` here is not cosmetic: it makes the dedup key below stable across
  # ticks even though tmux's own listing order is not guaranteed, so a
  # notified set does not read as "changed" purely from row reordering.
  names=$("${SUPERVISOR_PYTHON:-python3}" -c '
import json, sys
try:
    rows = json.loads(sys.argv[1])
except Exception:
    sys.exit(1)
print("\n".join(sorted(r.get("name", "") for r in rows if r.get("state") == "never-busy")))
' "$out" 2>/dev/null)
  names_rc=$?
  if [ "$names_rc" -ne 0 ]; then
    never_busy_check_failed "could not parse lanes.sh --json $session output"
    return 0
  fi
  # The check itself ran and answered, whatever the answer -- reset the fail
  # streak so a LATER unrelated failure starts counting from zero rather than
  # continuing a streak that was actually already broken by a healthy tick.
  printf '0' >"$NEVER_BUSY_CHECK_FAIL_STREAK" 2>/dev/null
  if [ -z "$names" ]; then
    # Recovered (or never stuck): clear the episode so a LATER occurrence,
    # even of the exact same lane name, pages again rather than reading as
    # the same episode still in progress.
    if [ -s "$NEVER_BUSY_EPISODE" ]; then
      log "NEVER-BUSY-CLEAR: previously stuck lane(s) in $session are no longer never-busy"
      rm -f "$NEVER_BUSY_EPISODE" 2>/dev/null
    fi
    return 0
  fi
  count=$(grep -c . <<<"$names")
  joined=$(tr '\n' ',' <<<"$names" | sed 's/,$//')
  message="agent-supervisor#112: ${count} lane(s) in ${session} have never gone ready or busy since launch — ${joined}. lanes.sh withholds them from --free; look at the pane directly."
  never_busy_note "${count} lane(s) stuck since launch — ${joined}"
  log "NEVER-BUSY: $message"

  prev=""
  [ -r "$NEVER_BUSY_EPISODE" ] && prev=$(cat "$NEVER_BUSY_EPISODE" 2>/dev/null)
  if [ "$prev" = "$names" ]; then
    log "NEVER-BUSY-DEDUP: same stuck lane(s) already paged this episode"
    return 0
  fi
  printf '%s' "$names" >"$NEVER_BUSY_EPISODE" 2>/dev/null

  # Same resolution #123 gave the escalate path: the configured notifier if
  # it actually resolves, otherwise the one shipped beside this watchdog --
  # so a relocated tree cannot silently lose this alarm the way #118 found
  # the escalate one had.
  notify_script="${NOTIFY_SCRIPT:-}"
  if [ -z "$notify_script" ] || [ ! -x "$notify_script" ]; then
    notify_script="$HERE/notify.sh"
  fi
  if [ ! -x "$notify_script" ]; then
    log "NEVER-BUSY-NOTIFY-UNAVAILABLE: no notifier at $notify_script"
    never_busy_note "${count} lane(s) stuck since launch — FAILED to notify, no notifier at $notify_script"
    return 0
  fi
  notify_out=$(AGENT_NOTIFY_CALLER=supervisor "$notify_script" "Lane(s) stuck since launch" "$message" 2>&1)
  notify_rc=$?
  if [ "$notify_rc" -ne 0 ]; then
    log "NEVER-BUSY-NOTIFY-FAILED rc=$notify_rc: $notify_out"
    # Same "failure must be visible locally" posture report() takes for the
    # escalate path: a notifier that cannot deliver must not read as "a
    # human was told" just because this check ran and found something.
    never_busy_note "${count} lane(s) stuck since launch — FAILED to reach a human: $(printf '%s' "$notify_out" | tr '\n' ' ')"
  else
    log "NEVER-BUSY-NOTIFY: $notify_out"
  fi
  return 0
}

# agent-supervisor#199/#205: the check ITSELF being unable to run (script
# missing/not executable, this worktree's toplevel unreadable) is a different
# fact from it running and reporting gaps -- see GUARD_AUDIT_FAIL_STREAK's
# definition above for why this mirrors never_busy_check_failed rather than
# just logging.
guard_audit_check_failed() {
  local reason="$1" streak=0 notify_script notify_out notify_rc
  [ -r "$GUARD_AUDIT_FAIL_STREAK" ] && streak=$(cat "$GUARD_AUDIT_FAIL_STREAK" 2>/dev/null)
  [[ "$streak" =~ ^[0-9]+$ ]] || streak=0
  streak=$((streak + 1))
  printf '%s' "$streak" >"$GUARD_AUDIT_FAIL_STREAK" 2>/dev/null
  log "GUARD-AUDIT-CHECK FAILED (streak ${streak}): $reason"
  guard_audit_note "unknown — $reason (failed ${streak} check(s) in a row)"
  if [ $(( streak % GUARD_AUDIT_FAIL_ESCALATE_AFTER )) -ne 0 ]; then
    return 0
  fi
  notify_script="${NOTIFY_SCRIPT:-}"
  if [ -z "$notify_script" ] || [ ! -x "$notify_script" ]; then
    notify_script="$HERE/notify.sh"
  fi
  if [ ! -x "$notify_script" ]; then
    log "GUARD-AUDIT-CHECK-ESCALATE-UNAVAILABLE: no notifier at $notify_script"
    return 0
  fi
  notify_out=$(AGENT_NOTIFY_CALLER=supervisor "$notify_script" \
    "worktree-guard-audit safety check has failed ${streak} times in a row" \
    "agent-supervisor#199/#205: the continuous worktree-guard-audit.sh check cannot run: ${reason}. This has failed ${streak} consecutive ticks — a stale, unguarded worktree could be leaking tmux sessions with nothing watching for it." 2>&1)
  notify_rc=$?
  if [ "$notify_rc" -ne 0 ]; then
    log "GUARD-AUDIT-CHECK-ESCALATE-FAILED rc=$notify_rc: $notify_out"
  else
    log "GUARD-AUDIT-CHECK-ESCALATE: $notify_out"
  fi
  return 0
}

# agent-supervisor#199/#205: runs on EVERY exit path, same reasoning as
# check_source_task_sweep/check_lane_completion_sweep above -- a stale
# unguarded worktree does not care which branch of this script's own
# busy/idle/asleep logic short-circuited this tick. Self-throttled against
# GUARD_AUDIT_STAMP (see that var's own comment for the cadence reasoning);
# most ticks return in the first branch below having done nothing.
#
# This is the caller #205's review demanded: worktree-guard-audit.sh is
# read-only against every worktree's PINNED commit (git show only, see that
# script's own header) -- nothing here mutates a worktree, kills a session,
# or touches tmux at all.
check_worktree_guard_audit() {
  local last=0
  if [ -r "$GUARD_AUDIT_STAMP" ]; then
    last=$(cat "$GUARD_AUDIT_STAMP" 2>/dev/null)
  fi
  [[ "$last" =~ ^[0-9]+$ ]] || last=0
  if [ $(( now - last )) -lt "$GUARD_AUDIT_INTERVAL" ]; then
    return 0
  fi
  # Stamped whether the run below succeeds or not, same reasoning the source
  # and lane sweeps give: retrying every 180s instead of waiting out the
  # interval buys nothing, since a worktree does not un-advance between ticks.
  printf '%s' "$now" >"$GUARD_AUDIT_STAMP" 2>/dev/null

  if [ ! -e "$HERE/worktree-guard-audit.sh" ]; then
    guard_audit_check_failed "worktree-guard-audit.sh is missing beside this watchdog; reinstall or advance the live worktree"
    return 0
  fi
  if [ ! -x "$HERE/worktree-guard-audit.sh" ]; then
    guard_audit_check_failed "worktree-guard-audit.sh exists but is not executable; run chmod +x $HERE/worktree-guard-audit.sh or restore the committed 100755 mode"
    return 0
  fi
  # SUPERVISOR_GUARD_AUDIT_REPO overrides which repository's worktree list is
  # audited -- same override shape as SUPERVISOR_STATE/SUPERVISOR_REPOS above.
  # A test needs this: proving the WIRING means running the real
  # worktree-guard-audit.sh through the real watchdog.sh (not a stub), but
  # pointed at a disposable throwaway repo with a deliberately unguarded
  # worktree, never at the real farm this machine happens to have checked out.
  local root
  root="${SUPERVISOR_GUARD_AUDIT_REPO:-$(git -C "$HERE" rev-parse --show-toplevel 2>/dev/null)}"
  if [ -z "$root" ]; then
    guard_audit_check_failed "cannot resolve this repository's toplevel to audit its worktree list"
    return 0
  fi

  # agent-supervisor#205 review (skills:2): bounded the same way
  # advance-live.sh#51 bounds its `git fetch` -- background + a `kill -0`
  # poll loop, not a `timeout`/`gtimeout` wrapper, for the same reason that
  # file's own comment gives: SUPERVISOR_PATH pins a minimal PATH and this
  # repo's own suite proves scripts under this watchdog run with only
  # /usr/bin:/bin, which ships neither on macOS. A hang here is a fourth
  # thing (unknown), not a clean result and not a gap -- it must never fall
  # through to either.
  local out out_file rc audit_pid waited timed_out
  out_file=$(mktemp "${TMPDIR:-/tmp}/watchdog-guard-audit.XXXXXX") || {
    guard_audit_check_failed "could not create a scratch file for worktree-guard-audit.sh's output"
    return 0
  }
  "$HERE/worktree-guard-audit.sh" "$root" >"$out_file" 2>&1 &
  audit_pid=$!
  waited=0
  timed_out=0
  while kill -0 "$audit_pid" 2>/dev/null; do
    if [ "$waited" -ge "$GUARD_AUDIT_TIMEOUT" ]; then
      timed_out=1
      kill -TERM "$audit_pid" 2>/dev/null
      sleep 1
      kill -KILL "$audit_pid" 2>/dev/null
      break
    fi
    sleep 1
    waited=$((waited + 1))
  done
  wait "$audit_pid" 2>/dev/null
  rc=$?
  out=$(cat "$out_file" 2>/dev/null)
  rm -f "$out_file"

  if [ "$timed_out" -eq 1 ]; then
    guard_audit_check_failed "worktree-guard-audit.sh did not finish within ${GUARD_AUDIT_TIMEOUT}s and was killed -- treating as unknown, not clean"
    return 0
  fi

  # The script ran and answered -- clean or gaps, either way a real result,
  # not a check failure, so the fail streak resets even when gaps were found.
  printf '0' >"$GUARD_AUDIT_FAIL_STREAK" 2>/dev/null

  local summary
  summary=$(grep -m1 '^worktree-guard-audit:' <<<"$out")
  [ -n "$summary" ] || summary="ran, rc=$rc, no summary line: $(printf '%s' "$out" | tr '\n' ' ')"

  if [ "$rc" -eq 0 ]; then
    log "GUARD-AUDIT: $summary"
    guard_audit_note "$summary"
    if [ -s "$GUARD_AUDIT_EPISODE" ]; then
      log "GUARD-AUDIT-CLEAR: previously reported gap(s) are gone"
      rm -f "$GUARD_AUDIT_EPISODE" 2>/dev/null
    fi
    return 0
  fi

  # Nonzero: the script's own contract is "exit nonzero iff gaps>0 or
  # unknowns>0" (its own `[ "$gaps" -eq 0 ] && [ "$unknowns" -eq 0 ]` final
  # line) -- treat any nonzero the same way, gaps and/or unknowns reported or
  # not, rather than silently swallowing an unrecognised failure shape as if
  # it were the clean case. UNKNOWN lines (a single `git show` that hit its
  # own bound, #205 review) are included here on purpose: a per-file timeout
  # is neither a gap nor clean, and must page the same as a gap rather than
  # vanish because no GAP line happened to be present.
  local gaps
  gaps=$(grep -E '^(GAP|UNKNOWN)' <<<"$out")
  log "GUARD-AUDIT-GAP: $summary"
  guard_audit_note "GAP — $summary"

  local prev
  prev=""
  [ -r "$GUARD_AUDIT_EPISODE" ] && prev=$(cat "$GUARD_AUDIT_EPISODE" 2>/dev/null)
  if [ "$prev" = "$gaps" ]; then
    log "GUARD-AUDIT-DEDUP: same gap(s) already paged this episode"
    return 0
  fi
  printf '%s' "$gaps" >"$GUARD_AUDIT_EPISODE" 2>/dev/null

  local notify_script notify_out notify_rc
  notify_script="${NOTIFY_SCRIPT:-}"
  if [ -z "$notify_script" ] || [ ! -x "$notify_script" ]; then
    notify_script="$HERE/notify.sh"
  fi
  if [ ! -x "$notify_script" ]; then
    log "GUARD-AUDIT-NOTIFY-UNAVAILABLE: no notifier at $notify_script"
    guard_audit_note "GAP — $summary — FAILED to notify, no notifier at $notify_script"
    return 0
  fi
  notify_out=$(AGENT_NOTIFY_CALLER=supervisor "$notify_script" \
    "worktree-guard-audit found unguarded worktree(s)" \
    "agent-supervisor#199/#205: $summary. $(printf '%s' "$gaps" | tr '\n' ' ' | sed 's/[[:space:]]*$//')" 2>&1)
  notify_rc=$?
  if [ "$notify_rc" -ne 0 ]; then
    log "GUARD-AUDIT-NOTIFY-FAILED rc=$notify_rc: $notify_out"
    # Same "failure must be visible locally" posture the escalate/never-busy
    # paths take: a notifier that cannot deliver must not read as "a human
    # was told" just because this check ran and found something.
    guard_audit_note "GAP — $summary — FAILED to reach a human: $(printf '%s' "$notify_out" | tr '\n' ' ')"
  else
    log "GUARD-AUDIT-NOTIFY: $notify_out"
  fi
  return 0
}

# agent-supervisor#526: the "separate decision" worktree.sh's own header says
# wiring `gc` in requires. Self-throttled against GC_SWEEP_STAMP (see that
# var's own comment for the cadence reasoning), same shape as every sweep
# above -- most ticks return in the first branch having done nothing.
#
# DRY-RUN unless SUPERVISOR_GC_LIVE is set (see GC_SWEEP_LIVE's own comment):
# this call reports what `gc` would remove across every repo in the estate,
# never removes anything itself, until that flag is deliberately flipped.
check_worktree_gc_sweep() {
  local last=0
  if [ -r "$GC_SWEEP_STAMP" ]; then
    last=$(cat "$GC_SWEEP_STAMP" 2>/dev/null)
  fi
  [[ "$last" =~ ^[0-9]+$ ]] || last=0
  if [ $(( now - last )) -lt "$GC_SWEEP_INTERVAL" ]; then
    return 0
  fi
  # Stamped whether the sweep below succeeds or not -- same reasoning every
  # other sweep here gives: a worktree does not un-qualify between ticks, so
  # retrying sooner than the interval buys nothing.
  printf '%s' "$now" >"$GC_SWEEP_STAMP" 2>/dev/null

  if [ ! -e "$HERE/worktree.sh" ]; then
    log "GC-SWEEP-MISSING: worktree.sh is missing beside this watchdog; reinstall or advance the live worktree"
    gc_sweep_note "missing — worktree.sh is missing beside this watchdog"
    return 0
  fi

  # Repo paths: SUPERVISOR_GC_REPOS (colon-separated) overrides for tests --
  # see that var's own comment. Production reads cli.py's own
  # DEFAULT_REPOSITORIES table rather than a second hardcoded path list.
  local repos_raw
  if [ -n "${SUPERVISOR_GC_REPOS:-}" ]; then
    repos_raw="$SUPERVISOR_GC_REPOS"
  elif [ -e "$HERE/cli.py" ]; then
    repos_raw=$("${SUPERVISOR_PYTHON:-python3}" -c '
import sys
sys.path.insert(0, "'"$HERE"'")
try:
    import cli
except Exception:
    sys.exit(1)
print(":".join(r["path"] for r in cli.DEFAULT_REPOSITORIES))
' 2>/dev/null)
  fi
  if [ -z "${repos_raw:-}" ]; then
    log "GC-SWEEP-MISSING: could not resolve a repository list (cli.py missing or unimportable, and SUPERVISOR_GC_REPOS unset)"
    gc_sweep_note "missing — could not resolve a repository list"
    return 0
  fi

  local mode="dry-run"
  local dry_flag="--dry-run"
  if [ -n "$GC_SWEEP_LIVE" ]; then
    mode="live"
    dry_flag=""
  fi

  local total_removed=0 total_skipped=0 failed=""
  local repo out rc line removed skipped
  local saved_ifs="$IFS"
  IFS=':'
  for repo in $repos_raw; do
    IFS="$saved_ifs"
    [ -n "$repo" ] || continue
    # A repo named in the table but not checked out on THIS machine is not a
    # failure -- the estate's canonical list is shared across machines, this
    # sweep only reaches what is actually present here.
    if [ ! -d "$repo" ] || ! git -C "$repo" rev-parse --show-toplevel >/dev/null 2>&1; then
      continue
    fi
    if [ -n "$dry_flag" ]; then
      out=$(bash "$HERE/worktree.sh" gc "$dry_flag" "$repo" "$GC_SWEEP_BASE" 2>&1)
    else
      out=$(bash "$HERE/worktree.sh" gc "$repo" "$GC_SWEEP_BASE" 2>&1)
    fi
    rc=$?
    if [ "$rc" -ne 0 ]; then
      failed="${failed}${repo} "
      log "GC-SWEEP FAILED for $repo rc=$rc: $(printf '%s' "$out" | tr '\n' ' ')"
      continue
    fi
    line=$(grep -m1 -E 'gc (dry run )?done' <<<"$out")
    # Two distinct phrasings share this line: a live run says "removed N,
    # skipped M", --dry-run says "would remove N, skipped M" -- no "d". Match
    # both rather than assuming the live wording everywhere.
    removed=$(sed -n 's/.*remove[d]* \([0-9]\{1,\}\).*/\1/p' <<<"$line")
    skipped=$(sed -n 's/.*skipped \([0-9]\{1,\}\).*/\1/p' <<<"$line")
    [[ "$removed" =~ ^[0-9]+$ ]] || removed=0
    [[ "$skipped" =~ ^[0-9]+$ ]] || skipped=0
    total_removed=$((total_removed + removed))
    total_skipped=$((total_skipped + skipped))
  done
  IFS="$saved_ifs"

  local summary="mode=$mode removed=$total_removed skipped=$total_skipped"
  if [ -n "$failed" ]; then
    summary="$summary failed=${failed% }"
  fi
  log "GC-SWEEP: $summary"
  gc_sweep_note "$summary"
  return 0
}
