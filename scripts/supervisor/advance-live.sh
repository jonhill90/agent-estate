#!/bin/bash
# Advance the LIVE worktree the watchdog LaunchAgent runs from, to
# origin/main -- the half of #99 that nothing did. #100 built the report
# (`code: ... behind origin/main`); this is the deploy step that acts on it.
#
# WHO CALLS THIS: watchdog.sh, on the way out of every tick, whenever the copy
# running is the pinned live one -- and loop-tick.md's first step, once per
# supervisor tick. Invoked, not merely documented (the `acp_transport.py`/
# `worktree.sh`/`lane-done.sh` shape: a tool nothing calls is a documentation
# rule with a binary attached).
#
# The watchdog was originally REJECTED as the caller, in #99's own comments,
# on the grounds that "a broken watchdog would reinstall itself every 180s and
# nothing would be left to notice." #130 reversed that, and the reversal is
# the more interesting half: the objection is answered by THE GATE below, not
# by declining to deploy. A candidate that cannot run is never installed, so
# the copy left unable to notice anything never becomes live. Meanwhile the
# loop -- the caller the objection left in place -- is the component that goes
# down, and is down by design during an escalation, so the live worktree
# stopped advancing exactly during the incidents it needed to be current for.
# The loop's call is kept: it is the path that still works when the watchdog
# is running from somewhere other than the pinned worktree.
#
# Still rejected, unchanged:
#   - a merge webhook / CI step: puts the deploy decision in the same system
#     that produced the change, which is what makes "merged does not mean
#     running" a safety property here.
#   - a plain timer: a clock deciding when to deploy, with none of the gates
#     a caller can supply. A supervisor tick is gated on real activity
#     (dispatch, review, merge) and a watchdog tick has already finished its
#     duties before it calls this; a bare timer is neither.
#
# CALLED FROM watchdog.sh, THIS SCRIPT CHECKS OUT OVER ITSELF. The watchdog
# runs a COPY of this file from a temporary path for that reason; do not
# "simplify" that away. Everything below runs from the copy, and the smoke
# test runs the candidate's own watchdog.sh out of the scratch worktree, so
# no process is ever reading a file this checkout is replacing.
#
# THE GATE: CI green is a property of the merge commit, not proof this
# machine's copy runs. Before switching LIVE's pin, check out the candidate
# commit into a throwaway worktree and run ITS OWN watchdog.sh once, pointed
# at scratch state, and confirm it writes a well-formed status file. That
# exercises the real entry point without ever touching the live loop: the
# smoke run's SUPERVISOR_PANE targets a pane that cannot exist, so the
# candidate takes the pane_unreadable/no_session branch and returns before
# any tmux send-keys is possible, and its SUPERVISOR_LIVE names a path it is
# not running from, so the candidate's own advance step sees that it is not
# the live copy and does nothing. A smoke test that advanced the live
# worktree would be the gate performing the act it exists to gate.
#
# THE RACE: the LaunchAgent runs watchdog.sh from LIVE on a fixed cadence.
# Swapping LIVE's working tree mid-tick can hand that tick a half-rewritten
# file. There is no lock -- watchdog.sh is not touched here, and adding one
# would change code #100 already shipped. Instead this reads watchdog.status's
# own `checked:` timestamp (the same file the watchdog writes every tick) and
# only advances in the window right after a tick, never blind and never in
# the stretch just before the next one is due. Called from the watchdog's own
# exit path that window is satisfied by construction -- the timestamp being
# read was written seconds earlier by the caller -- and the check still earns
# its place: it is what makes the LOOP's call safe, and what catches a tick
# that overran its own cadence.
#
# TWO CALLERS, STILL NO LOCK (#136). Since #132 two uncoordinated callers can
# reach this at once -- loop-tick.md's step 0 and watchdog.sh's exit trap -- and
# the normal case is the watchdog running from the pinned copy exactly when a
# supervisor tick begins. #136 filed that as low severity on the reasoned claim
# that git's own `index.lock` bounds the worst case. That claim was then driven
# rather than argued: 200 concurrent double-invocations against a throwaway
# worktree, two processes released from a shared barrier, swept across start
# offsets. 400 invocations, five distinct outcomes, all of them benign --
# 377 advanced, 10 refused on the locked checkout, 8 found the tree already
# current, 3 + 2 refused on a transiently dirty read. Zero left an invalid or
# unrecoverable worktree; no `index.lock` was ever orphaned; every iteration
# ended at the target with at least one caller having advanced it.
#
# Two of those outcomes were not predicted by the issue and are worth knowing
# before debugging one: the dirty guards can fire on a tree that is not dirty.
# A concurrent `git checkout --detach` writes files before it moves HEAD, so the
# other invocation's `git status --porcelain` can catch a real file reported as
# untracked. The message says "became dirty while the smoke test ran" and the
# tree is clean by the time a human looks. That is a misleading diagnosis, not
# an unsafe one -- it still refuses, loudly, with LIVE untouched.
#
# So NO LOCK, deliberately. A lock would convert "one caller refuses and the
# other advances" into "one caller waits", and the caller most likely to wait is
# watchdog.sh's exit trap -- a watchdog blocked on a mutex is a watchdog not
# watching, which is the failure this whole tool exists downstream of. The
# current shape already has the property a lock would buy: the advance is never
# lost, because the caller that refuses is the redundant one.
# tests/supervisor/test_advance_live.sh holds `index.lock` to pin the refusal
# mechanism and races 20 real double-invocations to pin the invariants.
#
# ROLLBACK: the pre-advance sha is written to disk before anything is
# mutated, because it is only knowable then -- after `checkout --detach` you
# are guessing from reflog.
#
# FAILURE IS LOUD: a failed smoke test, an unreadable origin/main, or a
# checkout that lands somewhere other than the target all exit non-zero with
# the live worktree left exactly where it was. No silent revert, no
# half-state.
#
# Usage:
#   advance-live.sh [live-worktree-path]
#
# Env overrides (mirroring watchdog.sh's, for testing and for a second
# machine layout):
#   SUPERVISOR_STATE     state dir; default ~/.local/state/agent-dotfiles-supervisor
#   SUPERVISOR_LIVE       live worktree path; default $SUPERVISOR_STATE/live
#   SUPERVISOR_STATUS     the LIVE watchdog's own status file (read, not written)
#   ADVANCE_LOG           default $SUPERVISOR_STATE/advance-live.log
#   ADVANCE_ROLLBACK      default $SUPERVISOR_STATE/.live-rollback-sha
#   ADVANCE_TICK_INTERVAL watchdog cadence in seconds; default 180
#   ADVANCE_SAFETY_BUFFER seconds before the next tick to stay clear of; default 30
#   ADVANCE_WATCHDOG_STALE_MULTIPLE  how many tick intervals old `checked:` may
#                                    get before this is treated as the
#                                    watchdog LaunchAgent being gone rather
#                                    than mid-cadence; default 3
#   WATCHDOG_LAUNCHD_LABEL  the watchdog's own LaunchAgent label, used to
#                           attribute a bootout; default
#                           com.jonhill.supervisor-watchdog
#   WATCHDOG_LAUNCHCTL_CMD / WATCHDOG_LOG_CMD  override the `launchctl` /
#                           `log` binaries the bootout classifier shells out
#                           to; for testing (stub binaries) only
#   SUPERVISOR_INBOX_POLL_STATUS  inbox-poll.sh's own status file (read, not
#                                 written); default $SUPERVISOR_STATE/inbox-poll.status
#   INBOX_POLL_RESTART_FLAG       written to request a poller restart; must
#                                 match inbox-poll.sh's own default/override
#   LANES_SESSION / LANES_POLLER_WINDOW  same poller-window recognition knobs
#                                        lanes.sh and poller-recover.sh use;
#                                        defaults agent-dotfiles / inbox-poll
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./poller-window.sh
. "$HERE/poller-window.sh"

STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
LIVE="${1:-${SUPERVISOR_LIVE:-$STATE/live}}"
WATCHDOG_STATUS="${SUPERVISOR_STATUS:-$STATE/watchdog.status}"
LOG="${ADVANCE_LOG:-$STATE/advance-live.log}"
ROLLBACK="${ADVANCE_ROLLBACK:-$STATE/.live-rollback-sha}"
TICK_INTERVAL="${ADVANCE_TICK_INTERVAL:-180}"
SAFETY_BUFFER="${ADVANCE_SAFETY_BUFFER:-30}"
STALE_MULTIPLE="${ADVANCE_WATCHDOG_STALE_MULTIPLE:-3}"
STALE_AFTER=$((TICK_INTERVAL * STALE_MULTIPLE))

log() { mkdir -p "$(dirname "$LOG")" 2>/dev/null; printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >>"$LOG"; }
fail() { log "FAIL: $*"; echo "advance-live: $*" >&2; exit 1; }
skip() { log "SKIP: $*"; echo "advance-live: $*"; exit 0; }

# `git status --porcelain` on LIVE. Read fresh every call -- never cache the
# result, because every caller of this exists to catch LIVE changing out
# from under an earlier read.
dirty_status() { git -C "$LIVE" status --porcelain 2>&1; }

# Re-derive the watchdog's tick age from $WATCHDOG_STATUS on disk. Echoes
# the age in seconds and returns 0, or returns 1 with nothing echoed if the
# file, its checked: line, or the timestamp is unreadable. Never reuse a
# prior call's result -- same reasoning as dirty_status above.
watchdog_age() {
  local line epoch now
  [ -f "$WATCHDOG_STATUS" ] || return 1
  line=$(grep -m1 '^checked:' "$WATCHDOG_STATUS" 2>/dev/null | sed 's/^checked:  *//')
  [ -n "$line" ] || return 1
  epoch=$(date -u -j -f '%Y-%m-%dT%H:%M:%SZ' "$line" +%s 2>/dev/null \
        || date -u -d "$line" +%s 2>/dev/null)
  [ -n "$epoch" ] || return 1
  now=$(date -u +%s)
  echo $((now - epoch))
}

# --- agent-supervisor#24: the watchdog's own absence must be loud ----------
# `watchdog.status`'s `checked:` line is the watchdog's own heartbeat: it is
# rewritten on every tick, including every early-exit path (watchdog.sh's
# report()), so it going stale means the LaunchAgent itself stopped ticking,
# not that the loop is merely idle. On 2026-08-13 the watchdog was unloaded
# for 91 minutes (10:14:38Z-11:45:36Z) and nothing here noticed: it was found
# only because a human happened to `cat` this file and compare it to the
# clock by hand.
#
# THIS is where that check lives, not a new script, and not lanes.sh:
#   - advance-live.sh already runs on every supervisor tick -- it is
#     loop-tick.md's literal first step -- so wiring the check in here needs
#     nothing new remembered on the tick's path. A separate script the tick
#     has to additionally call is exactly the shape #81/#99 both criticised
#     ("a step in a brief is a step that can be skipped"; "a tool nothing
#     calls is a documentation rule with a binary attached").
#   - advance-live.sh already parses this exact file's `checked:` line, for
#     the unrelated post-tick race gate below (watchdog_age). Reusing that
#     function keeps one parser for one timestamp instead of two that could
#     drift.
#   - lanes.sh answers "what is tmux doing" from pane content; it has no
#     reason to open a state-directory status file, and teaching it to would
#     duplicate watchdog_age() for no caller this script does not already
#     have.
#
# WHY THIS RUNS BEFORE EVERYTHING ELSE, not folded into the race gate lower
# down: the race gate is only reached when LIVE is genuinely behind
# origin/main (the `cur = target` shortcut returns first) -- so a dead
# watchdog with an up-to-date live copy would sail through this script
# reporting nothing wrong, the exact failure this check exists to close.
#
# WHY A MULTIPLE OF THE TICK INTERVAL, not the race gate's own
# TICK_INTERVAL-SAFETY_BUFFER window: that window is deliberately tight (it
# exists to stay OUT of a live tick), and tripping this alarm on ordinary
# scheduling jitter between two watchdog ticks would make it noise within a
# single healthy cadence -- exactly the false-alarm-every-tick shape #22
# named as how a real alarm gets ignored. Three tick intervals (9 minutes at
# the default 180s cadence) is short enough to catch a dead watchdog in a
# couple of ticks and long enough that a healthy watchdog, which rewrites
# this line every ~180s, never gets near it.
#
# Exits non-zero and never returns: a stale watchdog is reported LOUDLY, on
# this script's own exit code, rather than folded into `skip()`'s quiet
# exit-0 shape used for "outside the safe window, try again next tick" --
# those are both correct-and-boring; this is not -- UNLESS the classifier
# below attributes the staleness to a deliberate bootout, in which case it is
# reported and this returns 0.

WATCHDOG_LAUNCHD_LABEL="${WATCHDOG_LAUNCHD_LABEL:-com.jonhill.supervisor-watchdog}"
LAUNCHCTL_CMD="${WATCHDOG_LAUNCHCTL_CMD:-launchctl}"
LOG_CMD="${WATCHDOG_LOG_CMD:-log}"

# --- agent-supervisor#37 (review of #24): stopped is not dead ---------------
# #24's own review (PR #37) rejected the fix above as-is: `:216` said the
# LaunchAgent "may be unloaded or dead", collapsing two very different causes
# into one alarm. The Director deliberately stops this same LaunchAgent
# during incidents, so an alarm that cannot tell "I stopped it on purpose"
# from "it died" is an alarm a human learns to ignore -- which is the exact
# failure #24 itself was filed over (a real signal, ignored). This
# classifies staleness before it is reported, using the same evidence the
# reviewer confirmed was available for the #24 incident itself: launchd's own
# unified log, and whether the LaunchAgent is currently loaded.
#
# TWO INDEPENDENT SIGNALS, EITHER SUFFICIENT, because either one going
# unreadable must not silently downgrade a real death:
#   - the unified log carries launchd's own bootout record for this label --
#     the most direct evidence a human or `launchctl bootout` acted on it.
#   - `launchctl print` for the label currently failing is corroborating
#     evidence PROVIDED watchdog_age() already proved this label was ticking
#     before (a status file with a readable `checked:` line does not exist
#     unless something loaded and ran); a label that is both stale AND still
#     loaded is not explained by a bootout at all -- that shape is a hung or
#     crash-looping process, i.e. dead, not stopped.
#
# FAIL CLOSED ON "CANNOT TELL": if neither signal is readable (log command
# missing/erroring, launchctl unreadable) this echoes "unknown", which
# watchdog_stale_check treats identically to "dead" -- loud. An unreadable
# log downgrading a real death to a shrug is worse than a false alarm on a
# deliberate stop; #24 was filed over a signal nobody saw, not over one
# extra alert.
#
# Echoes exactly one of stopped|dead|loaded_not_firing|unknown and always
# returns 0 -- this is a classifier, not a gate; watchdog_stale_check decides
# what each means.
# Never caches a result: same discipline as watchdog_age/dirty_status above,
# and for the same reason -- every caller exists to catch state changing
# out from under an earlier read.
watchdog_bootout_classify() {
  local age="$1"
  local log_out log_rc launchctl_out launchctl_rc
  local log_readable=0 log_found=0
  local launchctl_readable=0 launchctl_loaded=1
  local lookback=$((age + 300))

  # `command` is load-bearing, not decoration: this script already defines a
  # `log()` shell function (the file logger near the top), and LOG_CMD's own
  # default value is the literal string "log" -- an unqualified call would
  # resolve to that function, not /usr/bin/log, and would silently "succeed"
  # with empty output every time, which reads as log-readable-but-no-bootout
  # on every call. `command` forces PATH lookup, bypassing shell functions.
  log_out=$(command "$LOG_CMD" show --style syslog --last "${lookback}s" \
    --predicate "eventMessage contains \"$WATCHDOG_LAUNCHD_LABEL\" and (eventMessage contains \"Bootout\" or eventMessage contains \"bootout\")" \
    2>&1)
  log_rc=$?
  if [ "$log_rc" -eq 0 ]; then
    log_readable=1
    [ -n "$log_out" ] && log_found=1
  fi

  launchctl_out=$(command "$LAUNCHCTL_CMD" print "gui/$(id -u)/$WATCHDOG_LAUNCHD_LABEL" 2>&1)
  launchctl_rc=$?
  if [ "$launchctl_rc" -eq 0 ]; then
    launchctl_readable=1
    launchctl_loaded=1
  elif grep -qi "could not find\|no such process\|not found" <<<"$launchctl_out"; then
    # launchctl's own "no such service" shape -- exit codes for this have
    # moved across macOS releases, so the message is the stable signal, not
    # the code.
    launchctl_readable=1
    launchctl_loaded=0
  fi

  if [ "$log_found" -eq 1 ]; then
    echo stopped
    return 0
  fi
  if [ "$launchctl_readable" -eq 1 ] && [ "$launchctl_loaded" -eq 0 ]; then
    echo stopped
    return 0
  fi
  # --- agent-supervisor#44: loaded and active is not automatically dead -----
  # Measured live: `launchctl print` for a watchdog stale 18 minutes, no
  # bootout, showed the label loaded, `state = active`, and `last exit code =
  # 0` -- a clean exit, not a crash. `launchctl kickstart -k <label>` fixed it
  # and normal cadence resumed unattended. That single field is the
  # discriminator the issue itself measured between this and a genuine death:
  # a hung or crash-looping process (the DEAD case below) has either a
  # nonzero last exit code or none at all, because it never got that far.
  # Checked only once launchctl_loaded=1 is already established above, so
  # this can never fire for an absent/unloaded label (that is `stopped`,
  # already handled) or an unreadable one (falls through to the fail-closed
  # cases below).
  if [ "$launchctl_readable" -eq 1 ] && [ "$launchctl_loaded" -eq 1 ] \
     && grep -qE '^\s*last exit code = 0\s*$' <<<"$launchctl_out"; then
    echo loaded_not_firing
    return 0
  fi
  if [ "$log_readable" -eq 1 ] || [ "$launchctl_readable" -eq 1 ]; then
    echo dead
    return 0
  fi
  echo unknown
  return 0
}

watchdog_stale_check() {
  local age line classification
  age=$(watchdog_age) || return 0
  [ "$age" -gt "$STALE_AFTER" ] || return 0
  line=$(grep -m1 '^checked:' "$WATCHDOG_STATUS" 2>/dev/null | sed 's/^checked:  *//')
  classification=$(watchdog_bootout_classify "$age")
  case "$classification" in
    stopped)
      # Reported on stdout too, not just $LOG -- this is a report, not an
      # alarm, but a report nobody can see is the exact failure #24 was
      # filed over. `log` alone (file-only) would repeat that mistake.
      line="WATCHDOG STOPPED -- $WATCHDOG_STATUS last checked ${line:-unknown} (${age}s ago); a launchd bootout for $WATCHDOG_LAUNCHD_LABEL is attributable -- this is the Director deliberately stopping the watchdog, not a death; not reported as an alarm (agent-supervisor#37)"
      log "$line"
      echo "advance-live: $line"
      return 0
      ;;
    loaded_not_firing)
      # Loud, like DEAD -- per the brief, classification refines the report
      # but must never gate whether the staleness alarm fires. Only the
      # diagnosis and remedy differ: this is not "the agent is gone", it is
      # "launchd is not re-firing it", so the message names the one command
      # that fixed the measured incident instead of pointing at a corpse that
      # is not there.
      fail "WATCHDOG STALE -- $WATCHDOG_STATUS last checked ${line:-unknown} (${age}s ago), older than ${STALE_AFTER}s (${STALE_MULTIPLE}x the ${TICK_INTERVAL}s tick interval) -- LOADED BUT NOT FIRING: $WATCHDOG_LAUNCHD_LABEL is loaded and active with a clean last exit (code 0) but no attributable bootout -- launchd is not re-firing it; try: launchctl kickstart -k gui/$(id -u)/$WATCHDOG_LAUNCHD_LABEL"
      ;;
    dead)
      fail "WATCHDOG STALE -- $WATCHDOG_STATUS last checked ${line:-unknown} (${age}s ago), older than ${STALE_AFTER}s (${STALE_MULTIPLE}x the ${TICK_INTERVAL}s tick interval) -- DEAD: no attributable bootout for $WATCHDOG_LAUNCHD_LABEL in the launchd unified log or launchctl print; nothing is restarting the supervisor loop if it dies"
      ;;
    *)
      fail "WATCHDOG STALE -- $WATCHDOG_STATUS last checked ${line:-unknown} (${age}s ago), older than ${STALE_AFTER}s (${STALE_MULTIPLE}x the ${TICK_INTERVAL}s tick interval) -- UNKNOWN: could not read the launchd unified log or launchctl print to tell a deliberate stop from a death; treated as the loud case rather than silently downgrading a possible death"
      ;;
  esac
}

# --- agent-dotfiles#187: restart a stale inbox-poll.sh ----------------------
# inbox-poll.sh is the estate's OTHER long-running process pinned to LIVE --
# same defect #130/#132 fixed here for the watchdog itself, deployed later
# and left with no equivalent. This is the analogous fix, not a second
# mechanism: it runs from right here, where LIVE's post-advance head sha is
# already known, and compares it against the `sha:` line inbox-poll.sh
# writes into its own status file every iteration.
#
# COOPERATIVE, NOT SIGNALED (see inbox-poll.sh's matching header comment for
# the read side). This only ever writes a flag file -- it never signals the
# process. A restart it triggers can therefore never land mid-drain:
# inbox-poll.sh only checks the flag between iterations, after a batch's
# offset has been both acknowledged to Telegram AND fully routed, so nothing
# is skipped and nothing is asked for twice.
#
# agent-supervisor#10: this used to ALSO queue a relaunch command into the
# poller's pane with `tmux send-keys`, reasoning that inbox-poll.sh never
# reads stdin, so the keystrokes would sit unread until "the shell resumes
# reading". There is no shell to resume: the pane's command is
# `exec scripts/supervisor/inbox-poll.sh`, which replaces the pane's shell
# rather than running under one, so when the flagged poller actually exits
# there is nothing left in the pane to read those keys -- and, absent
# `remain-on-exit`, no pane left at all. That queuing is gone. It is also no
# longer needed: poller-recover.sh (called from watchdog.sh every tick)
# notices the pane go dead once the flagged poller exits and relaunches it
# with whatever code is on disk at $LIVE, which is exactly what the queued
# command was trying to arrange by hand, just from a caller for which "no
# pane left to type into" is not a failure mode.
#
# THE PANE IS FOUND BY THE POLLER WINDOW, not by `pane_current_command` or the
# pane process argv. The live poller can read as a shell even when healthy, so
# process matching is not the poller identity. The shared helper is also used
# by lanes.sh and poller-recover.sh so the three call sites cannot drift.
INBOX_POLL_STATUS_PATH="${SUPERVISOR_INBOX_POLL_STATUS:-$STATE/inbox-poll.status}"
INBOX_POLL_RESTART_FLAG="${INBOX_POLL_RESTART_FLAG:-$STATE/.inbox-poll-restart-requested}"
INBOX_POLL_SESSION="${LANES_SESSION:-agent-dotfiles}"

# Echoes "session:@windowid" for the poller window in $INBOX_POLL_SESSION.
find_poller_pane() {
  poller_window_target "$INBOX_POLL_SESSION"
}

# maybe_restart_poller <live-head-sha> -- never fails the tick it is called
# from: every branch below is a `log` and a `return 0`, mirroring
# advance_on_exit's own "a refused advance is a report, not a crash" rule in
# watchdog.sh, because this runs on the way out of an otherwise-successful
# advance-live.sh pass.
maybe_restart_poller() {
  local live_sha="$1" poller_sha pane
  if [ -f "$INBOX_POLL_RESTART_FLAG" ]; then
    log "POLLER-CHECK: restart already requested at $INBOX_POLL_RESTART_FLAG, waiting for the poller to notice"
    return 0
  fi
  if [ ! -f "$INBOX_POLL_STATUS_PATH" ]; then
    log "POLLER-CHECK: no inbox-poll.status at $INBOX_POLL_STATUS_PATH -- poller not running or state wiped, not restarting"
    return 0
  fi
  poller_sha=$(grep -m1 '^sha:' "$INBOX_POLL_STATUS_PATH" 2>/dev/null | awk '{print $2}')
  if [ -z "$poller_sha" ]; then
    log "POLLER-CHECK: $INBOX_POLL_STATUS_PATH has no sha: line -- cannot compare, not restarting"
    return 0
  fi
  if [ "$poller_sha" = "$live_sha" ]; then
    log "POLLER-CHECK: poller already at $live_sha, current"
    return 0
  fi
  pane=$(find_poller_pane)
  pane_rc=$?
  case "$pane_rc" in
    0) ;;
    1)
      log "POLLER-CHECK: poller at $poller_sha, live now $live_sha, but no poller window named '$POLLER_WINDOW_NAME' exists in session '$INBOX_POLL_SESSION' -- not restarting"
      return 0
      ;;
    2)
      log "POLLER-CHECK: poller at $poller_sha, live now $live_sha, but multiple poller windows named '$POLLER_WINDOW_NAME' exist in session '$INBOX_POLL_SESSION' -- refusing to guess"
      return 0
      ;;
    *)
      log "POLLER-CHECK: poller at $poller_sha, live now $live_sha, but tmux could not read session '$INBOX_POLL_SESSION' -- not restarting"
      return 0
      ;;
  esac

  mkdir -p "$(dirname "$INBOX_POLL_RESTART_FLAG")" 2>/dev/null
  if ! : >"$INBOX_POLL_RESTART_FLAG" 2>/dev/null; then
    log "POLLER-CHECK: could not write restart flag $INBOX_POLL_RESTART_FLAG -- not restarting"
    return 0
  fi

  log "POLLER-RESTART-REQUESTED: pane $pane, poller was $poller_sha, live now $live_sha -- flag written; poller-recover.sh relaunches it once it exits (agent-supervisor#10)"
  return 0
}

watchdog_stale_check

git -C "$LIVE" rev-parse --git-dir >/dev/null 2>&1 || fail "not a git worktree: $LIVE"

cur=$(git -C "$LIVE" rev-parse HEAD 2>/dev/null) || fail "cannot read HEAD in $LIVE"
target=$(git -C "$LIVE" rev-parse origin/main 2>/dev/null) || fail "origin/main unreadable in $LIVE -- not advancing"

behind=$(git -C "$LIVE" rev-list --count HEAD..origin/main 2>/dev/null)
case "$behind" in
  ''|*[!0-9]*) fail "behind-count unreadable in $LIVE -- not advancing" ;;
esac

if [ "$cur" = "$target" ] || [ "$behind" -eq 0 ]; then
  log "current: $cur already matches origin/main, nothing to advance"
  maybe_restart_poller "$cur"
  exit 0
fi

# --- dirty guard: refuse rather than advance over someone's live edits ----
# Borrows worktree.sh's `guard`/`done` rule: uncommitted changes in a
# worktree are someone's unfinished work, not garbage. The reason this has
# to be a refusal and not a courtesy check: `git checkout --detach` does
# NOT discard a working-tree edit that doesn't conflict with the incoming
# diff -- it silently carries the edit forward. A dirty LIVE plus a
# checkout that otherwise succeeds reports ADVANCED and lands the right sha
# in `git log`, while the file actually on disk and executing is old
# content plus a local edit that nothing recorded. No stash: a stash
# sitting on the loop's own advancement guard is state nobody would go
# looking for. Refuse and report loudly instead.
dirty=$(dirty_status)
if [ -n "$dirty" ]; then
  fail "live worktree $LIVE has uncommitted changes -- refusing to advance a dirty tree, not stashing it
$dirty"
fi

# --- race gate: only advance in the window right after a tick -----------
if [ ! -f "$WATCHDOG_STATUS" ]; then
  skip "no watchdog status at $WATCHDOG_STATUS -- watchdog has not ticked from $LIVE yet, not advancing this pass"
fi
age=$(watchdog_age) || skip "no readable checked: timestamp in $WATCHDOG_STATUS -- not advancing this pass"
safe_until=$((TICK_INTERVAL - SAFETY_BUFFER))
if [ "$age" -lt 0 ] || [ "$age" -gt "$safe_until" ]; then
  skip "watchdog last ticked ${age}s ago, outside the 0-${safe_until}s post-tick window -- not advancing this pass"
fi

# --- gate: the candidate must demonstrably run, not just have CI-green --
SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/ad99-advance-smoke.XXXXXX")"
cleanup() {
  git -C "$LIVE" worktree remove --force "$SCRATCH" >/dev/null 2>&1
  rm -rf "$SCRATCH" 2>/dev/null
  git -C "$LIVE" worktree prune >/dev/null 2>&1
}
trap cleanup EXIT

if ! git -C "$LIVE" worktree add --detach "$SCRATCH" "$target" >>"$LOG" 2>&1; then
  fail "could not create a scratch worktree at $target for the smoke test -- not advancing"
fi

SMOKE="$SCRATCH/.smoke"
mkdir -p "$SMOKE"
SUPERVISOR_STATE="$SMOKE" SUPERVISOR_STATUS="$SMOKE/watchdog.status" \
SUPERVISOR_LOG="$SMOKE/watchdog.log" SUPERVISOR_STAMP="$SMOKE/.last-restart" \
SUPERVISOR_HISTORY="$SMOKE/.restart-history" NOTIFY_ENV="$SMOKE/none.env" \
SUPERVISOR_PANE="advance-live-smoke-test:999.1" \
SUPERVISOR_LIVE="$SMOKE/live" \
  bash "$SCRATCH/scripts/supervisor/watchdog.sh" >"$SMOKE/stdout" 2>"$SMOKE/stderr"
smoke_rc=$?

if [ "$smoke_rc" -ne 0 ] || [ ! -s "$SMOKE/watchdog.status" ] \
   || ! grep -q '^checked:' "$SMOKE/watchdog.status" \
   || ! grep -q '^state:' "$SMOKE/watchdog.status"; then
  log "smoke test at $target: rc=$smoke_rc status=$(cat "$SMOKE/watchdog.status" 2>/dev/null | tr '\n' ' ')"
  fail "candidate watchdog.sh at $target did not write a well-formed status -- not advancing, live worktree unchanged"
fi
log "smoke test at $target passed: $(grep '^state:' "$SMOKE/watchdog.status")"

# --- capture the rollback target before any mutation ---------------------
mkdir -p "$(dirname "$ROLLBACK")" 2>/dev/null
tmp="$ROLLBACK.$$"
if ! { printf '%s\n' "$cur" >"$tmp" && mv -f "$tmp" "$ROLLBACK"; }; then
  fail "could not record rollback target $cur to $ROLLBACK -- not advancing"
fi

# --- re-check BOTH guards IMMEDIATELY before the mutation -----------------
# Same discipline watchdog.sh applies to its own busy check right before it
# sends: "the earlier check is stale by several seconds." Here it is stale
# by however long `git worktree add` and the candidate's own watchdog.sh
# smoke test took to run -- both variable-duration, several subprocesses --
# which is long enough for LIVE to have been edited, or for the post-tick
# window to have closed. Re-read state fresh; do not reuse $dirty or $age
# from above.
dirty=$(dirty_status)
if [ -n "$dirty" ]; then
  fail "live worktree $LIVE became dirty while the smoke test ran -- refusing to advance, not stashing it
$dirty"
fi
age=$(watchdog_age) || skip "watchdog status became unreadable while the smoke test ran -- not advancing this pass"
if [ "$age" -lt 0 ] || [ "$age" -gt "$safe_until" ]; then
  skip "watchdog tick window closed while the smoke test ran (recheck age ${age}s, outside the 0-${safe_until}s window) -- not advancing this pass"
fi

# --- advance --------------------------------------------------------------
if ! git -C "$LIVE" checkout --detach "$target" >>"$LOG" 2>&1; then
  fail "checkout to $target failed in $LIVE -- live worktree left at $cur, rollback recorded at $ROLLBACK"
fi

newsha=$(git -C "$LIVE" rev-parse HEAD 2>/dev/null)
if [ "$newsha" != "$target" ]; then
  fail "post-checkout HEAD ($newsha) does not match target ($target) in $LIVE -- inconsistent, check by hand; rollback target $cur recorded at $ROLLBACK"
fi

# A matching sha is not proof of a clean result: `git checkout --detach`
# updates HEAD even when it silently carried a working-tree edit forward
# alongside it, which is exactly what the dirty guards above exist to catch
# earlier. This is the backstop in case something dirtied LIVE in the
# instant between the re-check above and this checkout -- a result this
# script reports must actually be clean, not just at the right sha.
post_status=$(dirty_status)
if [ -n "$post_status" ]; then
  fail "post-checkout $LIVE is dirty even though HEAD reached $target -- the checkout carried forward a local edit; do not trust this as a clean advance, rollback target $cur recorded at $ROLLBACK
$post_status"
fi

log "ADVANCED $LIVE from $cur to $target ($behind commit(s))"
echo "advance-live: advanced $LIVE from ${cur:0:12} to ${target:0:12} ($behind commit(s))"
maybe_restart_poller "$newsha"
