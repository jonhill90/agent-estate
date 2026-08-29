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
#   SUPERVISOR_REPO       git checkout to recreate a MISSING live worktree
#                         from (agent-supervisor#367); default
#                         ~/source/repos/Personal/agent-estate (or
#                         agent-supervisor, whichever exists -- #729)
#   SUPERVISOR_STATUS     the LIVE watchdog's own status file (read, not written)
#   ADVANCE_LOG           default $SUPERVISOR_STATE/advance-live.log
#   ADVANCE_ROLLBACK      default $SUPERVISOR_STATE/.live-rollback-sha
#   ADVANCE_TICK_INTERVAL watchdog cadence in seconds; default 180
#   ADVANCE_SAFETY_BUFFER seconds before the next tick to stay clear of; default 30
#   ADVANCE_WATCHDOG_STALE_MULTIPLE  how many tick intervals old `checked:` may
#                                    get before this is treated as the
#                                    watchdog LaunchAgent being gone rather
#                                    than mid-cadence; default 3
#   ADVANCE_SKIP_STREAK_FILE      where the consecutive-skip counter (#800)
#                                 persists across invocations; default
#                                 $SUPERVISOR_STATE/.advance-live-skip-streak
#   ADVANCE_SKIP_ESCALATE_AFTER  consecutive skip()s before this reports
#                                 loudly and pages a human (#800); default 20
#   ADVANCE_SKIP_ESCALATE_EPISODE  dedup marker so the page fires once per
#                                   streak, not once per tick past the
#                                   threshold; default
#                                   $SUPERVISOR_STATE/.advance-live-escalate-episode
#   ADVANCE_NOTIFY_SCRIPT   the human-notification script the escalation
#                           above calls; default $HERE/notify.sh, same
#                           override convention as heartbeat.sh's
#                           HEARTBEAT_NOTIFY_SCRIPT (agent-supervisor#273) --
#                           tests point this at a recording stub instead
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
#                                        defaults agent-supervisor / inbox-poll
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./poller-window.sh
. "$HERE/poller-window.sh"
# shellcheck source=./session-defaults.sh
. "$HERE/session-defaults.sh"

STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
LIVE="${1:-${SUPERVISOR_LIVE:-$STATE/live}}"
WATCHDOG_STATUS="${SUPERVISOR_STATUS:-$STATE/watchdog.status}"
LOG="${ADVANCE_LOG:-$STATE/advance-live.log}"
ROLLBACK="${ADVANCE_ROLLBACK:-$STATE/.live-rollback-sha}"
TICK_INTERVAL="${ADVANCE_TICK_INTERVAL:-180}"
SAFETY_BUFFER="${ADVANCE_SAFETY_BUFFER:-30}"
STALE_MULTIPLE="${ADVANCE_WATCHDOG_STALE_MULTIPLE:-3}"
STALE_AFTER=$((TICK_INTERVAL * STALE_MULTIPLE))
# agent-supervisor#51: before this fetch had a fetch, a hung one had never
# been possible -- the comparison read a local ref. Now every watchdog tick
# (every TICK_INTERVAL) makes a real network call, synchronously, with
# nothing above this script bounding it. A remote that accepts the TCP
# connection and then never answers (down DNS, a black-holing firewall, an
# auth prompt with no TTY to answer it) would wedge the tick indefinitely --
# worse than the silent-stale bug #11 fixed, per review on PR #51.
FETCH_TIMEOUT_SECONDS="${ADVANCE_FETCH_TIMEOUT_SECONDS:-20}"

SKIP_STREAK_FILE="${ADVANCE_SKIP_STREAK_FILE:-$STATE/.advance-live-skip-streak}"
SKIP_ESCALATE_AFTER="${ADVANCE_SKIP_ESCALATE_AFTER:-20}"
SKIP_ESCALATE_EPISODE="${ADVANCE_SKIP_ESCALATE_EPISODE:-$STATE/.advance-live-escalate-episode}"
NOTIFY_SCRIPT="${ADVANCE_NOTIFY_SCRIPT:-$HERE/notify.sh}"

log() { mkdir -p "$(dirname "$LOG")" 2>/dev/null; printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >>"$LOG"; }
fail() { log "FAIL: $*"; echo "advance-live: $*" >&2; exit 1; }

# --- agent-estate#800: make a long run of skips as loud as a failure -------
# Measured on the real estate (#800): 486 passing smoke tests, 302 SKIPs,
# and -- separately, and this matters -- the "0 advances" figure #800 led
# with does not hold up against this file's own history (see this PR's own
# description for the correction). What IS real and unchanged by that
# correction: every SKIP here is individually well-formed, exits 0, and logs
# like a healthy, conservative refusal. Read one at a time, 302 of them are
# indistinguishable from 302 legitimate "not this tick"s. Only the RUN of
# them -- the same guard declining pass after pass with no advance in
# between -- is the signal that something is actually stuck, and nothing
# before this counted that run.
#
# A FILE, NOT AN IN-PROCESS COUNTER: this script exits after every single
# invocation (`skip()`/`fail()` both `exit`), so nothing survives between
# ticks except what is written to disk. $SKIP_STREAK_FILE is that counter,
# bumped on every skip and cleared on every non-skip terminal state
# (ADVANCED, CURRENT) so the streak measures CONSECUTIVE skips, not a
# lifetime total.
#
# ESCALATE ONCE PER STREAK, NOT ONCE PER TICK PAST THE THRESHOLD. The
# lane that first designed this (orphaned diff, preserved on #800) named the
# exact failure a naive "streak >= threshold -> page" would cause: with a
# 180s cadence, a streak that reaches 302 stays past a 20-skip threshold for
# 282 more ticks, which would page 282 times for one incident.
# $SKIP_ESCALATE_EPISODE is a marker written the first time a streak crosses
# the threshold; its presence means "already paged for this streak," and it
# is cleared alongside the counter on the next non-skip terminal state --
# the next genuinely new streak pages again, the same streak never does.
read_skip_streak() {
  local n
  n=$(cat "$SKIP_STREAK_FILE" 2>/dev/null)
  case "$n" in ''|*[!0-9]*) echo 0 ;; *) echo "$n" ;; esac
}

reset_skip_streak() {
  rm -f "$SKIP_STREAK_FILE" "$SKIP_ESCALATE_EPISODE" 2>/dev/null
}

# Bumps the on-disk streak, echoes the new value, and pages once when it
# first crosses $SKIP_ESCALATE_AFTER. Never fails the caller: a failure to
# write the streak file or to send the page must not turn a SKIP into a
# FAIL -- the underlying decision not to advance is still correct even if
# this bookkeeping fails, so every step below is best-effort and logged
# rather than propagated.
bump_and_report_skip_streak() {
  local n
  n=$(( $(read_skip_streak) + 1 ))
  mkdir -p "$(dirname "$SKIP_STREAK_FILE")" 2>/dev/null
  { printf '%s\n' "$n" >"$SKIP_STREAK_FILE.$$" && mv -f "$SKIP_STREAK_FILE.$$" "$SKIP_STREAK_FILE"; } 2>/dev/null

  [ "$n" -ge "$SKIP_ESCALATE_AFTER" ] || return 0
  if [ -f "$SKIP_ESCALATE_EPISODE" ]; then
    return 0
  fi
  mkdir -p "$(dirname "$SKIP_ESCALATE_EPISODE")" 2>/dev/null
  { printf '%s\n' "$n" >"$SKIP_ESCALATE_EPISODE.$$" && mv -f "$SKIP_ESCALATE_EPISODE.$$" "$SKIP_ESCALATE_EPISODE"; } 2>/dev/null

  local subject="advance-live: $n consecutive skips"
  local body="$LIVE has not advanced in $n straight passes (>= ${SKIP_ESCALATE_AFTER}). Every one of those passes logged a well-formed SKIP and exited 0, which reads as healthy from any single log line -- only the run of them is the problem. See $LOG."
  log "ESCALATE: $n consecutive skips (>= ${SKIP_ESCALATE_AFTER}) -- $LIVE has not advanced in $n straight passes"
  echo "advance-live: ESCALATE -- $n consecutive skips, $LIVE has not advanced" >&2
  if ! AGENT_NOTIFY_CALLER=supervisor bash "$NOTIFY_SCRIPT" "$subject" "$body" >>"$LOG" 2>&1; then
    log "ESCALATE: notify.sh did not confirm delivery -- see $LOG above for its own output"
  fi
}

skip() { log "SKIP: $*"; echo "advance-live: $*"; bump_and_report_skip_streak; exit 0; }

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
# the read side). This writes a flag file -- it never signals the process. A
# restart it triggers can therefore never land mid-drain: inbox-poll.sh only
# checks the flag between iterations, after a batch's offset has been both
# acknowledged to Telegram AND fully routed, so nothing is skipped and
# nothing is asked for twice.
#
# agent-supervisor#10: this used to ALSO queue a relaunch command into the
# poller's pane with `tmux send-keys`, reasoning that inbox-poll.sh never
# reads stdin, so the keystrokes would sit unread until "the shell resumes
# reading". There is no shell to resume: the pane's command is
# `exec scripts/supervisor/inbox-poll.sh`, which replaces the pane's shell
# rather than running under one, so when the flagged poller actually exits
# there is nothing left in the pane to read those keys -- and, absent
# `remain-on-exit`, no pane left at all. That queuing is gone. It is also no
# longer needed: poller-recover.sh notices the pane go dead once the flagged
# poller exits and relaunches it with whatever code is on disk at $LIVE. The
# watchdog still calls poller-recover.sh every tick as the backstop, but
# waiting for that cadence was agent-supervisor#47: every merge could leave
# inbound down until the next 180s sweep. The prompt path below starts a
# short background waiter from the process that wrote the flag, waits for the
# old pid to exit, then invokes the same poller-recover.sh mechanism
# immediately. poller-recover.sh keeps ownership of launch idempotency and
# duplicate-poller prevention; advance-live.sh only removes the cadence gap.
#
# THE PANE IS FOUND BY THE POLLER WINDOW, not by `pane_current_command` or the
# pane process argv. The live poller can read as a shell even when healthy, so
# process matching is not the poller identity. The shared helper is also used
# by lanes.sh and poller-recover.sh so the three call sites cannot drift.
INBOX_POLL_STATUS_PATH="${SUPERVISOR_INBOX_POLL_STATUS:-$STATE/inbox-poll.status}"
INBOX_POLL_RESTART_FLAG="${INBOX_POLL_RESTART_FLAG:-$STATE/.inbox-poll-restart-requested}"
INBOX_POLL_SESSION="$(lanes_session_or_default)"
INBOX_POLL_RELAUNCH_WAIT_SECONDS="${INBOX_POLL_RELAUNCH_WAIT_SECONDS:-45}"

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
  local live_sha="$1" poller_sha pane poller_pid
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
  poller_pid=$(grep -m1 '^pid:' "$INBOX_POLL_STATUS_PATH" 2>/dev/null | awk '{print $2}')
  pane=$(find_poller_pane)
  pane_rc=$?
  # agent-supervisor#154: a poller window is no longer the only way this
  # process is hosted. `pane_rc` used to gate the restart-flag write itself,
  # so a service-hosted poller (no tmux window at all -- rc 1, or no tmux
  # session/binary to ask -- rc 3) never got flagged for a version-triggered
  # restart: the whole feature silently stopped working the moment the
  # window it was written for went away. The flag write below is now
  # unconditional except for rc 2 (multiple windows -- genuinely ambiguous,
  # still refused rather than guessed). Only the WINDOW-SPECIFIC prompt
  # relaunch beneath it -- queuing poller-recover.sh's tmux respawn -- stays
  # gated on rc 0: a service-hosted poller needs no prompting at all, its own
  # LaunchAgent/systemd restart policy relaunches it the instant the flagged
  # process exits, same cadence advantage the prompt path exists to give the
  # window-hosted one.
  if [ "$pane_rc" -eq 2 ]; then
    log "POLLER-CHECK: poller at $poller_sha, live now $live_sha, but multiple poller windows named '$POLLER_WINDOW_NAME' exist in session '$INBOX_POLL_SESSION' -- refusing to guess"
    return 0
  fi

  mkdir -p "$(dirname "$INBOX_POLL_RESTART_FLAG")" 2>/dev/null
  if ! : >"$INBOX_POLL_RESTART_FLAG" 2>/dev/null; then
    log "POLLER-CHECK: could not write restart flag $INBOX_POLL_RESTART_FLAG -- not restarting"
    return 0
  fi

  if [ "$pane_rc" -ne 0 ]; then
    # No poller window to prompt (service-hosted, or one predating the
    # window -- rc 1/3 alike): the flag is enough. Whatever relaunches this
    # process (a LaunchAgent's KeepAlive, poller-recover.sh's next tick for
    # a window-hosted deployment still mid-attrition) owns picking it up.
    log "POLLER-RESTART-REQUESTED: no poller window in session '$INBOX_POLL_SESSION' (pane_rc=$pane_rc) -- poller was $poller_sha, live now $live_sha; flag written, its own restart policy (service manager or watchdog poller-recover.sh) owns the relaunch"
    return 0
  fi

  if prompt_poller_relaunch "$pane" "$poller_sha" "$live_sha" "$poller_pid"; then
    log "POLLER-RESTART-REQUESTED: pane $pane, poller was $poller_sha, live now $live_sha -- flag written; prompt poller-recover.sh waiter started (watchdog remains the backstop)"
  else
    log "POLLER-RESTART-REQUESTED: pane $pane, poller was $poller_sha, live now $live_sha -- flag written; prompt relaunch could not be started, watchdog poller-recover.sh remains the backstop"
    # agent-supervisor#41 (agent-supervisor#57): this line used to reach only
    # advance-live.log. watchdog.sh captures this script's STDOUT into its
    # own `advance:` status line (advance_on_exit, watchdog.sh) -- but only
    # ever the top-level "advance-live: current"/"advanced" echo, which
    # reports the git-advance outcome and says nothing about a
    # poller-restart-request nested inside it. On 2026-08-13 that produced
    # exactly this: `advance-live: current, ...` every tick for hours while
    # the SAME tick's prompt relaunch failed silently underneath it -- a
    # trivially "successful" tick masking a real failure it also owns. This
    # echo is what lets watchdog.status's `advance:` field, and so
    # digest.sh, see the failure instead of only the outer success.
    echo "advance-live: POLLER-RESTART-REQUESTED but prompt relaunch could not be started -- watchdog poller-recover.sh remains the backstop"
  fi
  return 0
}

prompt_poller_relaunch() { # prompt_poller_relaunch <pane> <old-sha> <live-sha> <old-pid>
  local pane="$1" poller_sha="$2" live_sha="$3" poller_pid="$4"
  if [ -z "$poller_pid" ]; then
    log "POLLER-PROMPT-RELAUNCH-SKIPPED: $INBOX_POLL_STATUS_PATH has no pid: line, so advance-live.sh cannot tell when the old poller is gone; watchdog poller-recover.sh remains the backstop"
    echo "advance-live: poller-restart skipped -- $INBOX_POLL_STATUS_PATH has no pid: line"
    return 1
  fi
  if [ ! -e "$HERE/poller-recover.sh" ]; then
    log "POLLER-PROMPT-RELAUNCH-SKIPPED: poller-recover.sh is missing beside advance-live.sh; reinstall or advance the live worktree"
    echo "advance-live: poller-restart skipped -- poller-recover.sh is missing beside advance-live.sh"
    return 1
  fi
  if [ ! -x "$HERE/poller-recover.sh" ]; then
    log "POLLER-PROMPT-RELAUNCH-SKIPPED: poller-recover.sh exists but is not executable; run chmod +x $HERE/poller-recover.sh or restore the committed 100755 mode"
    echo "advance-live: poller-restart skipped -- poller-recover.sh exists but is not executable"
    return 1
  fi
  # agent-supervisor#75: this waiter runs to a `sleep`-bound deadline and then
  # execs poller-recover.sh -- outliving the "bash $copy" invocation above it
  # (see watchdog.sh's advance_on_exit) by design. Under the real watchdog
  # LaunchAgent, "bash $copy" exiting is also the job's main process exiting,
  # and launchd's default AbandonProcessGroup=false sends SIGTERM (then
  # SIGKILL) to anything left in the job's process group once that happens --
  # including this waiter, mid-sleep or mid-exec, before it reaches any of the
  # log lines below. Eight starts and zero terminal lines (#75) was exactly
  # that: not a bug in the waiter's own logic, a process-group reap from
  # outside it. `set -m` (job control) makes bash put a background job in ITS
  # OWN process group instead of the shell's -- confirmed against a real,
  # throwaway LaunchAgent (see #75) that only this, not a SIGTERM trap alone,
  # keeps the waiter alive past the parent's exit. The trap below is defense
  # in depth for the signal that DOES still reach it (SIGTERM, if grace-period
  # timing or a future launchd change ever lets one through) -- SIGKILL cannot
  # be trapped by any means, which is why the mutation test kills the waiter
  # to confirm zero-outcome is still detectable, not to demand this handle it.
  set -m
  (
    trap 'log "POLLER-PROMPT-RELAUNCH-KILLED: waiter for pane $pane received SIGTERM before finishing; watchdog poller-recover.sh remains the backstop"; exit 143' TERM
    deadline=$(( $(date +%s) + INBOX_POLL_RELAUNCH_WAIT_SECONDS ))
    if [ -n "$poller_pid" ]; then
      while kill -0 "$poller_pid" 2>/dev/null; do
        [ "$(date +%s)" -lt "$deadline" ] || {
          log "POLLER-PROMPT-RELAUNCH-TIMEOUT: old pid $poller_pid still alive after ${INBOX_POLL_RELAUNCH_WAIT_SECONDS}s; watchdog poller-recover.sh remains the backstop"
          exit 0
        }
        sleep 0.2
      done
    fi
    # agent-supervisor#450: $poller_pid can already be dead the moment this
    # waiter starts -- not from the flag just written above, but from an
    # earlier, unrelated exit (a crash, or a prior restart request whose own
    # relaunch never landed). When that happens the `while kill -0` loop
    # above runs zero iterations, so nothing ever reached the one place this
    # flag is normally consumed: inbox-poll.sh's own "see it, remove it, exit"
    # sequence on ITS way out. Left in place, the flag survives into the
    # brand-new poller poller-recover.sh is about to launch and is the first
    # thing that poller's own loop checks -- it reads a request meant for the
    # corpse it replaced, not for itself, and exits within its first ~0.1s
    # tick. Measured directly (agent-supervisor#450): a "RESPAWNED" pid
    # recorded in poller-recover.log and gone by the next liveness check,
    # exactly the shape test_watchdog_poller_copy.sh's "two quick restart
    # requests" caught in CI. Clearing it here -- once the old poller is
    # confirmed gone by any means, not only by its own hand -- guarantees the
    # flag can never outlive the process it was written for.
    rm -f "$INBOX_POLL_RESTART_FLAG" 2>/dev/null
    out=$(SUPERVISOR_STATE="$STATE" SUPERVISOR_LIVE="$LIVE" LANES_SESSION="$INBOX_POLL_SESSION" \
          "$HERE/poller-recover.sh" 2>&1)
    rc=$?
    if [ "$rc" -ne 0 ]; then
      log "POLLER-PROMPT-RELAUNCH-FAILED rc=$rc: pane $pane, poller was $poller_sha, live now $live_sha: $out"
    else
      log "POLLER-PROMPT-RELAUNCH: pane $pane, poller was $poller_sha, live now $live_sha: $out"
    fi
  ) >/dev/null 2>&1 &
  set +m
}

watchdog_stale_check

# --- agent-supervisor#367: live/ was DELETED, not merely stale -------------
# Measured on 2026-08-19: live/ vanished entirely -- not pruned, not merely
# missing from disk, its `git worktree list` registration was gone too --
# and the only response this script had was "not a git worktree: $LIVE", a
# correct refusal with no way out. `advance-live.sh` could advance an
# existing live/ but could not REBUILD one; recovery was done by hand,
# reading `.live-rollback-sha` and re-running the exact `git worktree add`
# this block now performs. That does not scale to a 3am incident.
#
# RECREATE ONLY, NEVER GUESS THE TARGET: the sha comes from $ROLLBACK, the
# same file `advance-live.sh` itself writes right before every mutation
# (see "capture the rollback target before any mutation" below) -- it is
# already the estate's record of where live/ is SUPPOSED to be, not a new
# invention. No rollback file means no known-good target, so this fails
# closed exactly like restore.sh does for an unrecoverable lane (invariant
# 3, CLAUDE.md): reported, not invented.
#
# REFUSE TO OVERWRITE A SURPRISE: a present-but-non-worktree $LIVE could be
# an interrupted recreate, a hand-mounted directory, or someone's real
# files under the wrong path. Only an ABSENT or genuinely EMPTY directory is
# treated as safe to build into -- anything else is the safe-deletion
# contract's "contents don't match the description" case, and is refused
# rather than rm -rf'd.
#
# THE RECREATE SOURCE is a separate git checkout, not $LIVE itself -- there
# is nothing at $LIVE to read a remote from once it is gone. SUPERVISOR_REPO
# names it, defaulting to the shared checkout this whole estate already
# treats as canonical for this repo (cli.py's own REPOS table, and
# CLAUDE.md's "the Director runs scripts/supervisor/ out of the shared
# checkout at ~/source/repos/Personal/agent-estate"). agent-estate#729:
# the rename actually happened, so prefer whichever of the two names exists
# on disk rather than swapping one hardcoded literal for another.
if ! git -C "$LIVE" rev-parse --git-dir >/dev/null 2>&1; then
  if [ -e "$LIVE" ] && [ -n "$(ls -A "$LIVE" 2>/dev/null)" ]; then
    fail "not a git worktree: $LIVE -- and it is a non-empty directory, not merely absent; refusing to overwrite it without a human confirming its contents are not needed"
  fi
  [ -f "$ROLLBACK" ] || fail "not a git worktree: $LIVE (missing) -- and no rollback sha recorded at $ROLLBACK, so there is no known-good target to recreate it at; a human must supply one"
  recreate_sha=$(tr -d '[:space:]' <"$ROLLBACK" 2>/dev/null)
  [ -n "$recreate_sha" ] || fail "not a git worktree: $LIVE (missing) -- and $ROLLBACK is empty, so there is no known-good target to recreate it at; a human must supply one"
  _RECREATE_REPO_DEFAULT="$HOME/source/repos/Personal/agent-estate"
  [ -d "$_RECREATE_REPO_DEFAULT" ] || _RECREATE_REPO_DEFAULT="$HOME/source/repos/Personal/agent-supervisor"
  RECREATE_REPO="${SUPERVISOR_REPO:-$_RECREATE_REPO_DEFAULT}"
  git -C "$RECREATE_REPO" rev-parse --git-dir >/dev/null 2>&1 \
    || fail "not a git worktree: $LIVE (missing) -- and the recreate source $RECREATE_REPO is not a git repository either; set SUPERVISOR_REPO to a working checkout"
  mkdir -p "$(dirname "$LIVE")" || fail "could not create the parent directory of $LIVE -- not recreating"
  rmdir "$LIVE" 2>/dev/null # only removes it if empty; a non-empty dir already failed above
  if ! git -C "$RECREATE_REPO" worktree add --detach "$LIVE" "$recreate_sha" >>"$LOG" 2>&1; then
    fail "live worktree $LIVE was missing and could not be recreated at recorded sha $recreate_sha from $RECREATE_REPO -- not advancing"
  fi
  log "RECREATED $LIVE at $recreate_sha (from $ROLLBACK, via $RECREATE_REPO)"
  echo "advance-live: recreated $LIVE at ${recreate_sha:0:12} (was missing) -- re-run to advance it further if origin/main has since moved"
  exit 0
fi

cur=$(git -C "$LIVE" rev-parse HEAD 2>/dev/null) || fail "cannot read HEAD in $LIVE"

# --- agent-supervisor#11: fetch before comparing ---------------------------
# Until this fix, `origin/main` below was whatever the worktree's LOCAL ref
# last happened to hold -- nothing on this path ever refreshed it, so a
# worktree that was genuinely behind compared clean against its own stale
# ref and printed NOTHING, twice, in production (issue #11: live sat still
# for up to 30 minutes holding a merged fix out of deployment, with neither
# an ADVANCED nor an ADVANCE-REFUSED line to show for it). A failed fetch
# must not fall through to that same silent "current" -- it is reported and
# refused exactly as loudly as any other fail() below, never folded into
# skip()'s quiet exit-0 shape. "could not tell" and "genuinely current" were
# the same silence before; they are not the same message now.
#
# agent-supervisor#51: bounded with a hard timeout, for the same reason --
# a hang here is not "current", it is a fourth thing (WEDGED) that must not
# read as any of the other three. Not a `timeout`/`gtimeout` wrapper: the
# watchdog pins PATH deliberately (SUPERVISOR_PATH, watchdog.sh) and this
# repo's own test suite runs advance-live.sh against a PATH of only
# /usr/bin:/bin to prove that pin holds -- neither ships GNU coreutils on
# macOS, so a script that *required* an external timeout binary would fail
# closed on every real production tick.
#
# Not `set -m` (bash job control) either: a process-group kill was the
# first version of this fix, and it was dropped after this exact test
# hung the suite intermittently under this harness -- `wait` on a job
# started under job control from inside a captured `$(...)` did not
# reliably see the child exit, a known class of bash/job-control/pipe
# interaction, not specific to this script. The poll loop below is
# strictly less elegant (a 1s-granularity `kill -0` loop, not an
# interrupt) and only reaches the `git` pid itself, not any transport
# helper it forks (ssh, git-remote-https) -- but it is deterministic,
# which a fetch bound cannot compromise on without becoming exactly the
# kind of intermittent hang it exists to prevent.
fetch_out_file=$(mktemp "${TMPDIR:-/tmp}/advance-live-fetch.XXXXXX") || fail "could not create a scratch file for the fetch's output -- not advancing"
git -C "$LIVE" fetch origin main >"$fetch_out_file" 2>&1 &
fetch_pid=$!
fetch_waited=0
fetch_timed_out=0
while kill -0 "$fetch_pid" 2>/dev/null; do
  if [ "$fetch_waited" -ge "$FETCH_TIMEOUT_SECONDS" ]; then
    fetch_timed_out=1
    kill -TERM "$fetch_pid" 2>/dev/null
    sleep 1
    kill -KILL "$fetch_pid" 2>/dev/null
    break
  fi
  sleep 1
  fetch_waited=$((fetch_waited + 1))
done
wait "$fetch_pid" 2>/dev/null
fetch_rc=$?
fetch_out=$(cat "$fetch_out_file" 2>/dev/null)
rm -f "$fetch_out_file"
if [ "$fetch_timed_out" -eq 1 ]; then
  fail "git fetch origin/main in $LIVE did not finish within ${FETCH_TIMEOUT_SECONDS}s and was killed -- refusing to compare against a ref nothing finished refreshing; this is UNKNOWN, not current
$fetch_out"
fi
if [ "$fetch_rc" -ne 0 ]; then
  fail "could not fetch origin/main in $LIVE (git fetch exit $fetch_rc) -- refusing to compare against a ref nothing just refreshed; this is UNKNOWN, not current
$fetch_out"
fi

target=$(git -C "$LIVE" rev-parse origin/main 2>/dev/null) || fail "origin/main unreadable in $LIVE even after a successful fetch -- not advancing"

# --- fast-forward-only guard (agent-supervisor#654 Part 2) -----------------
# Same discipline #73's guard already established for the shared checkout --
# refuse rather than force, refuse rather than silently diverge -- applied
# here to a clone nothing but this script's own advance ever touches.
# `git checkout --detach $target` moves HEAD unconditionally: given a $cur
# that is NOT an ancestor of $target (a local commit landed in $LIVE that
# should never exist there, since nothing else is supposed to write to it),
# the checkout below would still "succeed", silently abandoning that commit
# rather than refusing over it -- the exact silent-divergence this issue's
# Part 2 forbids. `behind` below counts commits reachable from $target that
# $cur lacks; it says nothing about commits $cur has that $target lacks, so
# it cannot catch this on its own. This is-ancestor check by construction
# always passes in the ordinary case (nothing else ever commits here) and is
# the one thing standing between "the update step's own bug" and a silent
# loss if that assumption is ever violated -- exactly the case worth failing
# loudly on, per this issue's own framing: "a refusal here is itself
# diagnostic ... rather than an expected steady state".
if ! git -C "$LIVE" merge-base --is-ancestor "$cur" "$target"; then
  fail "cannot fast-forward $LIVE from $cur to $target -- $cur is not an ancestor of origin/main, so this is a real divergence, not ordinary staleness. Nothing but this script's own advance step is supposed to write to $LIVE; something else did. Refusing to force or silently diverge -- live worktree left at $cur. Investigate what committed there before re-running."
fi

behind=$(git -C "$LIVE" rev-list --count HEAD..origin/main 2>/dev/null)
case "$behind" in
  ''|*[!0-9]*) fail "behind-count unreadable in $LIVE -- not advancing" ;;
esac

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
#
# CHECKED BEFORE THE "CURRENT" SHORTCUT BELOW, not after it (agent-supervisor
# #312): a live tree hand-edited in place without ever moving HEAD -- exactly
# what #312 found, `adapter.py` +21 lines against an unchanged live head --
# has cur == target and behind == 0, so the old ordering hit the CURRENT
# shortcut's `exit 0` and returned before this check ever ran. That let a
# dirty-but-not-behind live tree sail through silently on every tick
# forever; #312 was only found by a human reading a diff by hand. Ordering
# this first means ANY drift from the recorded commit -- ahead, behind, or
# merely dirty at the same sha -- fails loud instead of depending on someone
# happening to look.
dirty=$(dirty_status)
if [ -n "$dirty" ]; then
  fail "live worktree $LIVE has uncommitted changes -- refusing to advance a dirty tree, not stashing it
$dirty"
fi

if [ "$cur" = "$target" ] || [ "$behind" -eq 0 ]; then
  log "CURRENT: $cur already matches origin/main after a fresh fetch, nothing to advance"
  echo "advance-live: current, $cur already matches origin/main (fetched fresh)"
  # #800: CURRENT is not a skip -- live genuinely has nothing to do, which is
  # a healthy terminal state, not a guard declining. Reset the streak so a
  # tree that is caught up does not keep an old skip run alive.
  reset_skip_streak
  maybe_restart_poller "$cur"
  exit 0
fi

# --- the window must EXIST, but it is not entered yet --------------------
#
# THE WINDOW PROTECTS THE MUTATION, NOT THE SMOKE TEST. This ordering used to
# be the other way round, and it made the gate unsatisfiable.
#
# Measured 2026-08-20 in this estate's own watchdog.status:
#
#     advance: not this tick -- advance-live: watchdog tick window closed
#     while the smoke test ran (recheck age 152s, outside the 0-150s window)
#
# The smoke test takes longer than the window it had to finish inside, so the
# sequence was: enter the window, run the test, validate the correct commit,
# PASS -- and then throw the result away because the window had closed while
# testing. A test that cannot fit in the window can never satisfy the gate.
#
# HOW MUCH THIS COST IS NOT STATED HERE, DELIBERATELY. An earlier version of
# this comment claimed "65 of 258 passes were discarded". A cross-harness
# review (Codex, 2026-08-20) went looking for that number and found no
# counter, no fixture, no log and no report behind it -- and zero matching
# records in the live supervisor state. It was inherited, not measured, so it
# is gone. The defect above is structural and provable from the two durations
# alone; it does not need a frequency claim propped up by a number nobody can
# reproduce.
#
# The fix is ordering, not a bigger number: what must be inside the window is
# `git checkout --detach`, a ref update taking milliseconds.
#
# WHAT THE SMOKE TEST DOES TOUCH, because the first version of this comment
# got it wrong. It does NOT say "the smoke test never touches $LIVE" -- that
# claim was refuted in the same review. `git -C "$LIVE" worktree add` writes
# $LIVE's git administrative files, and `worktree-guard-audit.sh` reads
# `git worktree list` on every watchdog exit, so the scratch worktree IS
# observable to the running watchdog.
#
# The narrower rationale is the one that actually holds, and it is enough:
# the window exists to stop the working tree being SWAPPED under a watchdog
# mid-tick. Adding and removing a registered scratch worktree does not swap
# $LIVE's checkout; `git checkout --detach` does. That mutation, and only
# that mutation, is what the window must contain.
#
# We still require a watchdog status to EXIST and be readable before spending
# minutes on a smoke test -- a watchdog that has never ticked from $LIVE means
# advancing is meaningless. What moves below the smoke test is only the
# freshness assertion, which is re-read immediately before the mutation
# anyway (it always was; that recheck is now the real gate rather than a
# duplicate of an earlier one).
if [ ! -f "$WATCHDOG_STATUS" ]; then
  skip "no watchdog status at $WATCHDOG_STATUS -- watchdog has not ticked from $LIVE yet, not advancing this pass"
fi
watchdog_age >/dev/null || skip "no readable checked: timestamp in $WATCHDOG_STATUS -- not advancing this pass"
safe_until=$((TICK_INTERVAL - SAFETY_BUFFER))

# --- agent-supervisor#709: fail fast when the window is ALREADY closed ----
# The final recheck below (right before the mutation) is the real gate and
# stays untouched -- it has to be, because the smoke test itself can still
# push a genuinely-fresh entry past the window. This is a SEPARATE, earlier
# check for a different case that final recheck cannot distinguish from that
# one: `checked:` already being older than `safe_until` before the smoke test
# even starts.
#
# MEASURED, not assumed (agent-supervisor#709): every one of the four SKIP
# magnitudes #709 cites (302798s, 874s, 920s, 318s) reflects `checked:`
# already stale by roughly that much on entry -- the watchdog LaunchAgent not
# having ticked recently (#659's territory, or a deliberate Director stop),
# not the smoke test's own duration. Reproduced directly: seeding
# watchdog.status 179s stale (window is 150s) and running this script
# unmodified still executes the full `git worktree add` + candidate
# `watchdog.sh` smoke test before discovering, at the final recheck, that the
# age is now 180s -- one second of real elapsed time from the smoke test
# itself, on top of a deficit that already existed before any of it ran. A
# doomed pass was still paying full smoke-test cost, and the eventual "recheck
# age Ns" message reported that already-existing deficit as if it were
# entirely the smoke test's overrun, which is what led #666/#667 to first
# suspect the smoke test's duration rather than the watchdog's own staleness.
#
# This does not widen the window and does not change whether a pass CAN
# succeed -- it only stops paying for a smoke test whose result the existing
# final recheck was always going to discard, and reports the honest reason
# (already closed on entry, distinct from closed during the test).
pre_age=$(watchdog_age) || skip "watchdog status became unreadable -- not advancing this pass"
if [ "$pre_age" -lt 0 ] || [ "$pre_age" -gt "$safe_until" ]; then
  skip "watchdog tick window is already closed before the smoke test starts (checked: is ${pre_age}s old, outside the 0-${safe_until}s post-tick window) -- not advancing this pass; not running the smoke test for a mutation the recheck could never allow"
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
# agent-estate#808: SCRATCH is a linked worktree of THIS repo ($LIVE), so
# worktree-guard-audit.sh's `git worktree list` -- shared administration
# data across every worktree of one repo -- sees the SAME full production
# farm the real watchdog audits, not a small scratch-only set. That made
# this smoke test re-walk every live worktree on every promotion: measured
# 123.4s of a 138.4s smoke run against the 150s tick window, only 11.6s of
# slack, and it already missed a real tick once (recheck age 313s).
#
# SUPERVISOR_GUARD_AUDIT_MAX_WORKTREES bounds the audit to the first N
# worktrees ONLY for this smoke-test invocation -- production ticks never
# set this var, so watchdog-checks.sh's check_worktree_guard_audit leaves
# WORKTREE_GUARD_MAX_WORKTREES unset and worktree-guard-audit.sh's own
# default (0, unlimited) applies there, unchanged. The audit's job inside a
# smoke test is to prove the CANDIDATE watchdog.sh writes a well-formed
# status, not to re-benchmark full-farm performance on every promotion -- a
# structural bug in the audit's logic shows up on a bounded subset just as
# reliably as on the full farm (verified directly, agent-estate#808: a
# deliberately unguarded fixture worktree was still caught with
# WORKTREE_GUARD_MAX_WORKTREES set well below the fixture repo's total).
#
# 40 is sized for a REAL safety margin, not bare-minimum fitting -- #808
# measured ~0.084s per file@worktree check (123.4s / (113 worktrees x 13
# files)), so 40 worktrees x 13 files ~= 520 checks ~= 44s of audit, versus
# the unbounded ~123s. That leaves the smoke test's other ~15s of non-audit
# overhead at ~59s total against the 150s window -- ~91s of slack, comfortably
# above the 60s+ target and nowhere near the 11.6s that caused the miss.
SUPERVISOR_STATE="$SMOKE" SUPERVISOR_STATUS="$SMOKE/watchdog.status" \
SUPERVISOR_LOG="$SMOKE/watchdog.log" SUPERVISOR_STAMP="$SMOKE/.last-restart" \
SUPERVISOR_HISTORY="$SMOKE/.restart-history" NOTIFY_ENV="$SMOKE/none.env" \
SUPERVISOR_PANE="advance-live-smoke-test:999.1" \
SUPERVISOR_LIVE="$SMOKE/live" \
SUPERVISOR_GUARD_AUDIT_MAX_WORKTREES=40 \
  bash "$SCRATCH/scripts/supervisor/watchdog.sh" >"$SMOKE/stdout" 2>"$SMOKE/stderr"
smoke_rc=$?

if [ "$smoke_rc" -ne 0 ] || [ ! -s "$SMOKE/watchdog.status" ] \
   || ! grep -q '^checked:' "$SMOKE/watchdog.status" \
   || ! grep -q '^state:' "$SMOKE/watchdog.status"; then
  log "smoke test at $target: rc=$smoke_rc status=$(cat "$SMOKE/watchdog.status" 2>/dev/null | tr '\n' ' ')"
  # COUNCIL FINDING 3 (Codex, 2026-08-20): with the smoke test now ahead of the
  # freshness check, a smoke failure during an ALREADY-CLOSED window would be
  # reported as ADVANCE-REFUSED -- a hard failure for a pass that could not
  # have mutated anything regardless. That turns an unremarkable "not this
  # tick" into an alarm, and this estate has been trained by 18,900 lines of
  # watchdog log to ignore alarms that fire without consequence.
  #
  # A smoke failure is only a REFUSAL if advancing was actually possible.
  # Otherwise it is a skip, and the next open window will re-run the test.
  late_age=$(watchdog_age 2>/dev/null) || late_age=""
  if [ -n "$late_age" ] && { [ "$late_age" -lt 0 ] || [ "$late_age" -gt "$safe_until" ]; }; then
    skip "candidate watchdog.sh at $target did not write a well-formed status, but the post-tick window was already closed (age ${late_age}s) so no advance was possible this pass -- skipping rather than refusing"
  fi
  fail "candidate watchdog.sh at $target did not write a well-formed status -- not advancing, live worktree unchanged"
fi
log "smoke test at $target passed: $(grep '^state:' "$SMOKE/watchdog.status")"

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

# COUNCIL FINDING 2 (Codex, 2026-08-20): THE CANDIDATE MUST STILL BE THE TIP.
#
# `target` is resolved from origin/main BEFORE the smoke test and was never
# re-resolved. Under the old ordering the post-tick window bounded how stale
# `target` could get; moving the smoke test earlier removes that bound, so
# main advancing mid-smoke would promote LIVE to an ALREADY-STALE commit --
# reintroducing, by a different route, the exact defect this PR exists to fix.
#
# The smoke test validated THIS sha. If main has moved, that evidence is about
# a commit we are no longer deploying, so the honest action is to skip and let
# the next pass smoke-test the new tip. We do NOT advance to the newer sha on
# untested evidence, and we do NOT advance to the older one now that something
# newer exists.
fresh_target=$(git -C "$LIVE" rev-parse origin/main 2>/dev/null || echo "")
if [ -z "$fresh_target" ]; then
  skip "could not re-resolve origin/main before the mutation -- not advancing on an unverified target"
fi
if [ "$fresh_target" != "$target" ]; then
  skip "origin/main moved from $target to $fresh_target while the smoke test ran -- the passing smoke test is evidence about $target, not $fresh_target; not advancing on untested evidence, the next pass will test the new tip"
fi
age=$(watchdog_age) || skip "watchdog status became unreadable while the smoke test ran -- not advancing this pass"
if [ "$age" -lt 0 ] || [ "$age" -gt "$safe_until" ]; then
  # THIS is the race gate now -- the only one, and it guards the mutation
  # directly. Reaching it means the smoke test already passed, so the pass is
  # not being thrown away: the next tick re-runs against the same target and
  # this check is the only thing that has to fit in the window. What follows
  # it is `git checkout --detach`, a ref update measured in milliseconds.
  skip "watchdog tick window closed while the smoke test ran (recheck age ${age}s, outside the 0-${safe_until}s post-tick window) -- not advancing this pass; the smoke test PASSED, only the mutation waits"
fi

# --- capture the rollback target, AFTER every skip, before any mutation ----
#
# THIS MOVED, and the test suite is why. It used to sit above the guards
# below. When the freshness/window checks ran BEFORE the smoke test, a stale
# window skipped early and never reached this write. Moving the smoke test
# above the window check (this PR's whole point) also moved the skip below
# this write, so a SKIPPED advance started recording a rollback target for a
# mutation that never happened:
#
#     FAIL a skipped advance records no rollback target -- wrote ab26111...
#
# A rollback target that names a sha nothing was moved off is worse than
# none: the next reader treats it as evidence an advance occurred. So the
# write now happens after every `skip` path and immediately before the
# checkout -- still "before any mutation", which is the property that
# matters, and now also after every decision NOT to mutate.
mkdir -p "$(dirname "$ROLLBACK")" 2>/dev/null
tmp="$ROLLBACK.$$"
if ! { printf '%s\n' "$cur" >"$tmp" && mv -f "$tmp" "$ROLLBACK"; }; then
  fail "could not record rollback target $cur to $ROLLBACK -- not advancing"
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
# #800: an actual advance is the streak's real reset point -- the guard did
# what it exists to do, so whatever run of skips came before is over.
reset_skip_streak
maybe_restart_poller "$newsha"
