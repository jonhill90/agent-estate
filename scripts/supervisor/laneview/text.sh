#!/bin/bash
# laneview implementation: plain stdout, no daemon, no tmux plugin.
#
# This is the "apart" implementation (agent-dotfiles#178): it has no
# dependency beyond python3 for json parsing, works over a bare SSH session,
# in cron, or with no tmux client attached at all. It is the control that
# proves the meta-harness half is not required for the estate to have a
# lane view.
#
# Usage: text.sh <session> <lanes.sh --json output>   (called by laneview.sh)

set -uo pipefail

SESSION="$1"
JSON="$2"

python3 - "$SESSION" "$JSON" <<'PY'
import json, sys

session, raw = sys.argv[1], sys.argv[2]
rows = json.loads(raw)

# One key per state lanes.sh ships. `validate_laneview_state_maps` in
# scripts/validate_repository.py reads these keys and errors if lanes.sh
# grows a state this dict does not name -- `scrolled` was missing until
# review of #231 found it (lanes.sh's copy-mode branch), which is what the
# check now prevents recurring.
glyph = {
    "free": "-", "busy": "*", "hung": "!", "dead": "x",
    "menu-blocked": "?", "text-blocked": "?", "unsent": "~",
    "service": ".", "supervisor": ".", "unknown": "?",
    "scrolled": "^",
}

print(f"laneview text -- {session}")
for r in rows:
    # A state with no glyph gets one of its own, not `?`: `?` means "this
    # renderer knows the lane is blocked or indeterminate", and a state
    # this renderer has never heard of is a different claim. The state name
    # itself is printed verbatim either way, which is why this renderer can
    # never mislead the way the sidebar's fixed vocabulary can.
    g = glyph.get(r["state"], "#")
    print(f"  {g} {r['name']:<24} {r['state']}")
PY
