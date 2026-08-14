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
