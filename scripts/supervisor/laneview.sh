#!/bin/bash
# Drive one laneview/ implementation from lanes.sh's own json.
#
# WHY (agent-dotfiles#178): Jon wants a tmux plugin that can act as a
# meta-harness "working together and apart" with the headless supervisor.
# Neither half may become required by the other, so the human-facing render
# is defined once, as a contract, and every renderer -- a plain stdout feed
# or a tmux-plugin sidebar -- is a swappable implementation of it, never a
# second reader of tmux or the ledger. See laneview/README.md for the
# contract text and #173 for the measured cost of the tmux-sidebar path.
#
# Usage: laneview.sh <impl> [session]
#   impl:    the basename of a script under laneview/, e.g. text, opensessions
#   session: tmux session to report on (default: agent-supervisor)
#
# Adding a renderer is a new file under laneview/ that names every state
# lanes.sh ships (laneview/README.md rule 4, enforced by
# validate_repository.py). Removing one is `rm laneview/<impl>.sh` plus the
# cases in tests/supervisor/test_laneview.sh that assert that renderer's own
# behaviour -- nothing in scripts/ names any implementation, this file
# re-enumerates the directory. See laneview/README.md for why the test
# naming one is deliberate and is not coupling.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# The list is of names that can actually be passed as <impl>, so it is built
# from `laneview/*.sh` rather than from every entry in the directory: `ls |
# sed 's/\.sh$//'` offered `README.md` as an implementation, in the one
# message a human reads when they have got the name wrong (#4 finding D).
#
# Each renderer's own one-line description comes from a `# laneview-summary:`
# comment in that renderer's own file, not from a table kept here -- a
# renderer added without one shows up nameless (never silently dropped from
# the list), so the gap is visible in the one message a human actually reads
# instead of requiring this script to be edited in step with laneview/.
usage() {
  cat >&2 <<EOF
usage: laneview.sh <impl> [session]

laneview/ is a stopgap lane-state renderer (laneview/README.md), not the
supervisor's interface. Its renderers:
$(impl_list)
EOF
}

impl_list() {
  local f name summary
  for f in "$HERE"/laneview/*.sh; do
    [ -f "$f" ] || continue
    name="$(basename "$f" .sh)"
    summary="$(grep -m1 '^# laneview-summary: ' "$f" 2>/dev/null | sed 's/^# laneview-summary: //')"
    printf '  %-14s %s\n' "$name" "${summary:-(no description -- add a '# laneview-summary:' line to laneview/$name.sh)}"
  done
}

if [ $# -lt 1 ]; then
  usage
  exit 1
fi

IMPL="$1"
# shellcheck source=./session-defaults.sh
. "$HERE/session-defaults.sh"
SESSION="${2:-$(lanes_session_or_default)}"
IMPL_SCRIPT="$HERE/laneview/$IMPL.sh"

if [ ! -x "$IMPL_SCRIPT" ]; then
  echo "laneview.sh: no renderer named '$IMPL'" >&2
  usage
  exit 1
fi

# agent-dotfiles#239's $TMUX_PANE fallback (session-defaults.sh's
# `supervisor_window_id`) assumes its caller runs as a child of the
# SUPERVISOR's own pane -- true for the supervisor's own tick, never true
# for a viewer. laneview.sh is read-only by contract (rule 1 above) and is
# routinely run from inside an ordinary lane's own pane (a human popping
# open the tmux plugin, or `laneview.sh tui` driven live, as
# test_laneview_tui_interactive.sh does) -- left alone, that pane's own
# $TMUX_PANE would be handed straight to lanes.sh, which would then report
# THIS viewer's own window as `supervisor` instead of the real one. Cleared
# here, once, before the first lanes.sh call, so every downstream read this
# script or an exec'd renderer makes (including tui.sh's own periodic
# re-fetch, a fresh `bash lanes.sh --json` subprocess each refresh tick)
# inherits an environment with no $TMUX_PANE to misread.
unset TMUX_PANE

# lanes.sh is the one reader of tmux measurements and the ledger (#178
# brief, "tmux is not a database"); every implementation gets state only
# through this json, never by polling tmux or the ledger itself.
if ! json=$(bash "$HERE/lanes.sh" --json "$SESSION") || [ -z "$json" ]; then
  # Without this, a failing lanes.sh handed the renderer an empty string and
  # the human saw a json.loads traceback instead of a diagnosis (review of
  # #231). Empty is unambiguous: `--json` prints `[]` for a session with no
  # lanes, so no output at all means lanes.sh could not read the session,
  # and a view of state that could not be read is not a view.
  echo "laneview.sh: lanes.sh --json $SESSION produced no output -- cannot render a view of state it could not read." >&2
  exit 1
fi

exec "$IMPL_SCRIPT" "$SESSION" "$json"
