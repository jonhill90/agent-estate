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
