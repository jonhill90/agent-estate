#!/bin/bash
# Print a harness's own H_LAUNCH_CMD (the interactive launch line
# bootstrap-session.sh types into a fresh lane -- no prompt folded in) and
# exit 0; print nothing to stdout and exit 1 if the name is unknown to the
# registry or that harness records no launch command.
#
# agent-estate#827 fix2: `_vacate_pane_before_reap`
# (reconcile_lane_completions.py) has to relaunch the SAME harness a
# finished lane was running, from Python, so its parked pane keeps reading
# `free` to lanes.sh instead of going bare-shell (see that method's own
# docstring for why a bare shell is the defect this closes). Python has no
# access to harness-registry.sh's bash-3.2 indexed arrays, and hardcoding a
# second copy of H_LAUNCH_CMD there would drift the moment a harness/*.sh
# file changes its own -- this is the one CLI seam onto the registry,
# mirroring `harness_index_for_name`'s existing in-process lookup rather
# than inventing a second resolution path.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
. "$HERE/harness-registry.sh"

name="${1:?usage: harness-launch-cmd.sh <harness-name>}"
idx=$(harness_index_for_name "$name") || {
  echo "harness-launch-cmd.sh: no harness named '$name' in the registry" >&2
  exit 1
}
cmd="${H_LAUNCH_CMD[$idx]}"
if [ -z "$cmd" ]; then
  echo "harness-launch-cmd.sh: harness '$name' records no H_LAUNCH_CMD" >&2
  exit 1
fi
printf '%s\n' "$cmd"
