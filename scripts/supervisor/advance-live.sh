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
#   SUPERVISOR_INBOX_POLL_STATUS  inbox-poll.sh's own status file (read, not
#                                 written); default $SUPERVISOR_STATE/inbox-poll.status
#   INBOX_POLL_RESTART_FLAG       written to request a poller restart; must
#                                 match inbox-poll.sh's own default/override
#   LANES_SERVICE_RE / LANES_SESSION  same pane-identification knobs lanes.sh
#                                     uses to find the poller (#154); default
#                                     session "agent-dotfiles"
set -uo pipefail

STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
LIVE="${1:-${SUPERVISOR_LIVE:-$STATE/live}}"
WATCHDOG_STATUS="${SUPERVISOR_STATUS:-$STATE/watchdog.status}"
LOG="${ADVANCE_LOG:-$STATE/advance-live.log}"
ROLLBACK="${ADVANCE_ROLLBACK:-$STATE/.live-rollback-sha}"
TICK_INTERVAL="${ADVANCE_TICK_INTERVAL:-180}"
SAFETY_BUFFER="${ADVANCE_SAFETY_BUFFER:-30}"

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

# --- agent-dotfiles#187: restart a stale inbox-poll.sh ----------------------
# inbox-poll.sh is the estate's OTHER long-running process pinned to LIVE --
# same defect #130/#132 fixed here for the watchdog itself, deployed later
# and left with no equivalent. This is the analogous fix, not a second
# mechanism: it runs from right here, where LIVE's post-advance head sha is
# already known, and compares it against the `sha:` line inbox-poll.sh
# writes into its own status file every iteration.
#
# COOPERATIVE, NOT SIGNALED (see inbox-poll.sh's matching header comment for
# the read side). This only ever writes a flag file and queues a relaunch
# command into the poller's pane -- it never signals the process. A restart
# it triggers can therefore never land mid-drain: inbox-poll.sh only checks
# the flag between iterations, after a batch's offset has been both
# acknowledged to Telegram AND fully routed, so nothing is skipped and
# nothing is asked for twice.
#
# WHY QUEUEING BEFORE THE POLLER HAS ACTUALLY EXITED IS SAFE: inbox-poll.sh
# never reads stdin, so keystrokes sent to its pane while it is still the
# foreground process sit unread in the pty's input queue until the shell
# resumes reading -- the same mechanism watchdog.sh's own "queued /loop"
# comment documents for a busy pane. No wait loop, no second invocation, no
# race to get right here.
#
# THE PANE IS FOUND BY WHAT ITS OWN FIRST PROCESS IS, not a window number or
# name -- exactly the identification lanes.sh's SERVICE_RE already uses
# (#154), reused rather than reinvented so the two never drift apart.
INBOX_POLL_STATUS_PATH="${SUPERVISOR_INBOX_POLL_STATUS:-$STATE/inbox-poll.status}"
INBOX_POLL_RESTART_FLAG="${INBOX_POLL_RESTART_FLAG:-$STATE/.inbox-poll-restart-requested}"
INBOX_POLL_SESSION="${LANES_SESSION:-agent-dotfiles}"
INBOX_POLL_SERVICE_RE="${LANES_SERVICE_RE:-(^|/)inbox-poll\.sh( |$)}"

# Echoes "session:window.pane" for the first tmux pane in $INBOX_POLL_SESSION
# whose own process matches $INBOX_POLL_SERVICE_RE, or returns 1 with
# nothing echoed if tmux is unavailable, the session does not exist, or no
# pane matches.
find_poller_pane() {
  command -v tmux >/dev/null 2>&1 || return 1
  local target pid cmd
  while IFS=$'\t' read -r target pid; do
    [ -n "$pid" ] || continue
    cmd=$(ps -o command= -p "$pid" 2>/dev/null) || continue
    if [[ "$cmd" =~ $INBOX_POLL_SERVICE_RE ]]; then
      printf '%s' "$target"
      return 0
    fi
  done < <(tmux list-panes -t "$INBOX_POLL_SESSION" -F '#{session_name}:#{window_index}.#{pane_index}	#{pane_pid}' 2>/dev/null)
  return 1
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
  pane=$(find_poller_pane) || {
    log "POLLER-CHECK: poller at $poller_sha, live now $live_sha, but no pane in session '$INBOX_POLL_SESSION' matches inbox-poll.sh -- not restarting"
    return 0
  }

  mkdir -p "$(dirname "$INBOX_POLL_RESTART_FLAG")" 2>/dev/null
  if ! : >"$INBOX_POLL_RESTART_FLAG" 2>/dev/null; then
    log "POLLER-CHECK: could not write restart flag $INBOX_POLL_RESTART_FLAG -- not restarting"
    return 0
  fi

  tmux send-keys -t "$pane" -l "cd '$LIVE' && exec scripts/supervisor/inbox-poll.sh" 2>/dev/null
  tmux send-keys -t "$pane" Enter 2>/dev/null
  log "POLLER-RESTART-QUEUED: pane $pane, poller was $poller_sha, live now $live_sha -- flag written, relaunch queued for once the poller exits"
  return 0
}

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
