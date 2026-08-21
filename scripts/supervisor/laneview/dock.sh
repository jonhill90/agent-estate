#!/bin/bash
# laneview-summary: a docked vertical sidebar pane, refreshing on a timer -- zero dependency, no daemon
#
# laneview implementation: the "docked pane" #464 asked for (agent-dotfiles#197
# settled it can be built with tmux primitives already in this estate's
# dependency graph -- #178's premise that vertical tabs need a plugin or a
# custom TUI is false). Unlike tui.sh (curses, selectable, needs its own
# focus) or opensessions.sh (a third-party daemon injected into every
# window), this is meant to be opened once with `tmux split-window` into a
# narrow column beside a lane and left there: it never reads a key, never
# selects a window, never asks for focus -- it only redraws.
#
# Usage: dock.sh <session> <lanes.sh --json output>   (called by laneview.sh)
#
# To actually dock it (a plain tmux primitive, nothing this repo has to
# ship): from inside the session,
#   tmux split-window -h -l 28 'bash scripts/supervisor/laneview.sh dock'
#
# CONTRACT (laneview/README.md):
#   1. Read, never write. Every frame comes from the json this script was
#      handed, or (on the live refresh loop below) from re-asking lanes.sh
#      --json for a fresh copy -- the one reader of tmux's measurements and
#      the ledger. This script issues no other tmux command at all: no
#      select-window, no rename, no send-keys, nothing dispatch.sh or
#      claim.sh's job.
#   2. Degrade to absence, not staleness. A refresh tick whose `lanes.sh
#      --json` fails or comes back empty replaces the WHOLE frame with an
#      UNREACHABLE line, never leaves the last good render on screen as if
#      it were current.
#   3. Cost nothing when unused. Nothing spawns this but a human (or a
#      launcher they wire up) running `laneview.sh dock`. No daemon exists
#      before that, and nothing survives after the pane running it closes.
#   4. Name every state -- STATE_GLYPH below is the same 14-entry map
#      text.sh and tui.sh carry, so a state lanes.sh has never emitted to
#      this renderer still gets its own marker ("#"), never silently
#      reading as free/idle. `validate_laneview_state_maps` in
#      scripts/validate_repository.py checks this map the same way it
#      checks the other two.
#
# REFRESH INTERVAL: a docked pane is meant to stay open for a whole tmux
# session, not the few seconds a popup lives for -- tui.sh's 2s default
# (chosen for a screen a human is actively looking at and about to press a
# key in) would run `lanes.sh --json` (a `tmux list-panes` plus a `ps` per
# window) roughly 1800 times an hour for every attached client with a dock
# open. 5s keeps a lane's free/busy transition visible within one glance
# without keeping tmux and ps busy in the background indefinitely.
# Overridable with LANEVIEW_DOCK_REFRESH for a human who wants it faster.
# Either way this is `sleep` between fetches, never a tight poll: the loop
# below blocks on `sleep "$REFRESH_SECS"` every single tick, so idle time
# between refreshes costs nothing to the CPU or to tmux.
#
# When stdout is not a terminal (piped output, a test harness, `laneview.sh
# dock session > file`), there is nothing to dock into and no point looping
# forever -- render ONE static frame in the same shape text.sh uses and
# exit, so this renderer is testable the same way text.sh is: call it
# directly with canned json and no tty, and so the contract's "never show
# staleness" holds headlessly (a single frame is never stale, since nothing
# after it claims to still be current).

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LANES_SH="$HERE/../lanes.sh"

SESSION="$1"
JSON="$2"

REFRESH_SECS="${LANEVIEW_DOCK_REFRESH:-5}"

# Kept identical to text.sh's and tui.sh's map on purpose -- three renderers
# naming the same state differently would be a second classifier by another
# name. `validate_laneview_state_maps` (scripts/validate_repository.py)
# reads these keys and errors if lanes.sh grows a state this dict does not
# name.
render_frame() {
  local sess="$1" raw="$2"
  python3 - "$sess" "$raw" <<'PY'
import json, sys

session, raw = sys.argv[1], sys.argv[2]

STATE_GLYPH = {
    "free": "-", "busy": "*", "hung": "!", "dead": "x",
    "menu-blocked": "?", "text-blocked": "?", "unsent": "~",
    "service": ".", "supervisor": ".", "unknown": "?",
    "scrolled": "^", "stale": "X", "broken": "B", "never-busy": "?",
}

try:
    rows = json.loads(raw)
except json.JSONDecodeError as exc:
    print(f"laneview dock -- {session}")
    print(f"  UNREACHABLE -- lanes.sh --json produced unparseable output: {exc}")
    sys.exit(0)

print(f"laneview dock -- {session}")
for r in rows:
    g = STATE_GLYPH.get(r["state"], "#")
    name = r["name"]
    # Narrow on purpose -- this is meant to sit in a `tmux split-window -l
    # 28` column beside a lane, not to be the widest thing on screen.
    if len(name) > 16:
        name = name[:15] + "…"
    print(f" {g} {name:<16} {r['state']}")
PY
}

if [ ! -t 1 ]; then
  render_frame "$SESSION" "$JSON"
  exit 0
fi

json="$JSON"
while true; do
  # ANSI clear + home, not `clear(1)` -- one write, no extra process per
  # tick, and it behaves the same whether or not $TERM is set to something
  # `clear` recognises (a fresh split pane's environment is not guaranteed
  # to have inherited one).
  printf '\033[2J\033[H'
  if [ -z "$json" ]; then
    printf 'laneview dock -- %s\n  UNREACHABLE -- lanes.sh --json produced no output\n' "$SESSION"
  else
    render_frame "$SESSION" "$json"
  fi
  # The blocking wait, not a poll: nothing between one redraw and the next
  # touches tmux, ps, or the CPU. See the REFRESH INTERVAL note above for
  # why 5s.
  sleep "$REFRESH_SECS"
  json="$(bash "$LANES_SH" --json "$SESSION" 2>/dev/null)" || json=""
done
