#!/bin/bash
# Shared tmux session defaults for lane-facing scripts.
#
# The default is centralized because a repo rename left a dozen independent
# literals disagreeing about where new work should land. LANES_SESSION remains
# the public override at every call site.

AGENT_SUPERVISOR_DEFAULT_LANES_SESSION="${AGENT_SUPERVISOR_DEFAULT_LANES_SESSION:-agent-supervisor}"

lanes_session_or_default() {
  printf '%s\n' "${LANES_SESSION:-$AGENT_SUPERVISOR_DEFAULT_LANES_SESSION}"
}

# agent-supervisor#111: one tmux session per repo, named for the repo -- a
# lane working in jonhill90/X runs in session X, so a rail that has moved to
# its own repo (the Go rail's move to agent-tui, #107) stops sharing lanes
# with the repo it left, the same shape #99 fixed one layer up (the SESSION
# DEFAULT naming the parent repo; this is the SESSION ITSELF naming whichever
# repo's lane happens to run in it).
#
# `$1` is whatever the caller already has on hand to name the repo with --
# `owner/name`, a bare `name`, or a directory whose basename is the repo
# checkout. Only the last path segment is ever used, so all three shapes
# resolve the same way; `agent-supervisor#111`'s NAME_PART in dispatch.sh
# already computes exactly this before this function existed, and callers
# should pass that value rather than duplicate the derivation.
#
# LANES_SESSION still wins outright, unconditionally, over every other
# signal -- kept as override at every call site (#99), never bypassed by a
# repo name computed here. A repo name that resolves to empty (no `$1`, no
# path) falls back to `lanes_session_or_default`, which is `director`'s and
# every non-repo-specific caller's existing behaviour and stays unchanged for
# them.
session_for_repo() {
  # Two statements, not `local repo=... name=...`: bash evaluates every RHS
  # in a compound `local` statement before ANY of that statement's variables
  # are assigned, so `name`'s expansion of `$repo` reads the caller's outer
  # (unset) `repo`, not the value this line just declared -- name comes back
  # empty and this always fell through to the no-repo default. Measured
  # directly: `local repo=X name=${repo##*/}` in a fresh function gives
  # `name=""` even though `echo $repo` right after shows `X`.
  local repo="${1:-}"
  local name="${repo##*/}"
  if [ -n "$name" ]; then
    printf '%s\n' "${LANES_SESSION:-$name}"
  else
    lanes_session_or_default
  fi
}

# agent-dotfiles#239: the supervisor's own window was identified by comparing
# `#{window_index}` to `LANES_SUPERVISOR_WINDOW` (default 1) -- unstable under
# `renumber-windows on`, which this estate runs with. Killing any window BELOW
# the supervisor shifts it out of slot 1, and every caller that used this
# comparison (lanes.sh's `--free` exclusion, lane-done.sh's rename refusal,
# dispatch.sh's diagnostic filter) stopped recognising it. Measured live
# 2026-08-12: the supervisor sat at index 5, renamed by an unrelated dispatch,
# and `lanes.sh` reported index 1 -- a different pane entirely -- as
# `supervisor`.
#
# `#{window_id}` (`@N`) is tmux's own handle: stable for the window's whole
# lifetime and never reused, exactly the property #241 already threads
# through dispatch.sh/lanes.sh/lane-done.sh's lane targets. This resolves the
# SAME kind of handle for the supervisor's window, in order:
#
#   1. LANES_SUPERVISOR_WINDOW itself, when it already looks like an id
#      (`@N`) -- an explicit override for a caller with no TMUX_PANE of its
#      own (watchdog.sh, invoked by a LaunchAgent outside tmux entirely; see
#      lanes.sh's #163 PATH comment for the same caller). An operator sets
#      this once, by hand, from the supervisor's own pane
#      (`tmux display-message -p '#{window_id}'`).
#   2. $TMUX_PANE, when set -- the common case. The supervisor's own tick
#      invokes lanes.sh/dispatch.sh/lane-done.sh as children of its own
#      shell, so tmux has already stamped TMUX_PANE with the exact pane whose
#      window this needs to identify; no config or persistence required. This
#      is how #239 itself was diagnosed live: `echo $TMUX_PANE` -> `%12`,
#      resolved from there to the window that mattered.
#
# Verified against the target SESSION when one is given, so a caller whose
# own pane happens to sit in some OTHER tmux session cannot accidentally
# exclude that other session's window 1.
#
# agent-supervisor#459 follow-up: the session-name check above is NOT
# sufficient on its own -- pane ids are unique per tmux SERVER, not
# globally. A caller whose ambient $TMUX_PANE was minted on one server (its
# own personal tmux session, say) while $TMUX_TMPDIR points at a DIFFERENT
# server -- exactly this codebase's own documented isolation shape,
# `env -u TMUX TMUX_TMPDIR=...` (tmux-guard.sh's "Isolated form"; every
# real-tmux test in this suite does this) -- can have that stale
# $TMUX_PANE number coincidentally alias a REAL pane on the server actually
# being queried. If that alias happens to sit in a same-named session, the
# check above passes on a window that has nothing to do with the caller.
# Measured live: test_lane_done.sh's #259 fixture, run from a shell already
# inside its own tmux session, leaked that shell's ambient `%6` past the
# fixture's `env -u TMUX` isolation (TMUX_PANE was never part of that
# isolation); the isolated test server had its own, unrelated `%6` sitting
# in the very session under test, and a finished lane's window was
# misidentified as the supervisor's own -- lane-done.sh then refused to
# rename it back to free-N (agent-supervisor#459).
#
# $TMUX is only ever set BY tmux itself for a process that is truly a
# child of a real pane, and its presence is what makes every plain `tmux`
# command in THIS process consistently address that pane's own server:
# tmux's own connection precedence prefers $TMUX's embedded socket path
# over $TMUX_TMPDIR whenever both are set (verified directly,
# agent-supervisor#459). Requiring it here guarantees $TMUX_PANE is
# resolved on the exact server it was minted on -- not merely "whatever
# server $TMUX_TMPDIR happens to name today". A caller that legitimately
# wants to address a DIFFERENT session/server always unsets $TMUX to do
# so; by unsetting it, it also correctly forfeits this function's trust in
# whatever stale $TMUX_PANE it separately inherited.
#
# Returns 1 with no output when neither resolves -- the caller's contract is
# to fall back to its pre-#239 index comparison in that case (degraded, not
# broken: exactly today's behaviour), never to treat empty as "no supervisor
# window exists".
supervisor_window_id() {
  local session="${1:-}"
  case "${LANES_SUPERVISOR_WINDOW:-}" in
    @*) printf '%s\n' "$LANES_SUPERVISOR_WINDOW"; return 0 ;;
  esac
  [ -n "${TMUX_PANE:-}" ] || return 1
  [ -n "${TMUX:-}" ] || return 1
  local wid
  wid="$(tmux display-message -p -t "$TMUX_PANE" '#{window_id}' 2>/dev/null)" || return 1
  [ -n "$wid" ] || return 1
  if [ -n "$session" ]; then
    local pane_session
    pane_session="$(tmux display-message -p -t "$TMUX_PANE" '#{session_name}' 2>/dev/null)" || return 1
    [ "$pane_session" = "$session" ] || return 1
  fi
  printf '%s\n' "$wid"
}
