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
#   session: tmux session to report on (default: agent-dotfiles)
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

IMPL="${1:?usage: laneview.sh <impl> [session]}"
SESSION="${2:-agent-dotfiles}"
IMPL_SCRIPT="$HERE/laneview/$IMPL.sh"

if [ ! -x "$IMPL_SCRIPT" ]; then
  echo "laneview.sh: no renderer at $IMPL_SCRIPT (implementations: $(ls "$HERE/laneview" 2>/dev/null | sed 's/\.sh$//' | tr '\n' ' '))" >&2
  exit 1
fi

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
