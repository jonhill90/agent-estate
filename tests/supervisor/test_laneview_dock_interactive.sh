#!/bin/bash
# dock.sh's live refresh loop (as opposed to its no-tty static-frame
# fallback, covered in test_laneview.sh) only runs with a real tty behind
# stdout -- exactly the property a real isolated tmux pane gives it. Same
# isolation discipline test_laneview_tui_interactive.sh and
# test_laneview_tmux_plugin.sh use: an isolated TMUX_TMPDIR server, never
# the shared one, never a bare `tmux kill-server`.
#
# What this pins, that the headless suite cannot: dock.sh actually DOCKS --
# `tmux split-window` puts it in a second pane beside a real lane's own
# pane in the SAME window, not a separate window or a popup that steals
# focus -- and it actually REFRESHES a rename made after the first frame,
# proving the loop re-asks lanes.sh --json on its own timer rather than
# rendering once and going stale.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/../../scripts/supervisor/tmux-isolation.sh"
LANEVIEW="$HERE/../../scripts/supervisor/laneview.sh"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }

echo "laneview dock.sh (interactive, real tty)"

if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP no tmux on PATH"; exit 0
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "  SKIP no python3 available"; exit 0
fi
if [ ! -f "$LANEVIEW" ]; then
  echo "  SKIP no laneview.sh -- viewer adapter not installed"; exit 0
fi

RT="$(mktemp -d "${TMPDIR:-/tmp}/lv-dock-it.XXXXXX")"
unset TMUX
export TMUX_TMPDIR="$RT"
assert_isolated_tmux || exit 1
S="laneview-dock-test-$$"
cleanup() { unset TMUX; export TMUX_TMPDIR="$RT"; assert_isolated_tmux; tmux kill-server 2>/dev/null; rm -rf "$RT"; }
trap cleanup EXIT INT TERM

# base-index must be set on the FIRST command that starts the server, via
# `-f` -- `new-session` fixes window 1's own index to whatever base-index
# the server already has at that instant, so setting it afterward is too
# late (agent-supervisor#459, test_lanes_supervisor_identity.sh's own
# comment). Without this, a bare isolated server defaults to base-index 0,
# which lands "arch" on window 0 and shifts LANES_SUPERVISOR_WINDOW's
# hardcoded default of 1 onto "free-2" instead -- lanes.sh would then
# (correctly, given that config) call free-2 the supervisor, and every
# assertion below would be checking the wrong row for the wrong reason.
CONF="$(mktemp "${TMPDIR:-/tmp}/laneview-dock-tmux-conf.XXXXXX")"
printf 'set -g base-index 1\nset -g renumber-windows on\n' > "$CONF"
tmux -f "$CONF" new-session -d -s "$S" -n arch -x 100 -y 30
rm -f "$CONF"
tmux new-window -t "$S" -n free-2
tmux new-window -t "$S" -n workbench

# A one-second refresh so the test doesn't wait through dock.sh's real 5s
# default -- LANEVIEW_DOCK_REFRESH is the documented override for exactly
# this.
tmux split-window -h -t "$S:workbench" -l 24 \
  "env LANEVIEW_DOCK_REFRESH=1 bash '$LANEVIEW' dock '$S'"

# The split must land as a SECOND PANE of the same window, not a new
# window -- that is the difference between "docked" and "another tab".
panes="$(tmux list-panes -t "$S:workbench" | wc -l | tr -d ' ')"
if [ "$panes" = "2" ]; then
  ok "split-window docks the sidebar into a second pane of the same window, not a new window"
else
  bad "split-window docks the sidebar into a second pane of the same window, not a new window" "panes=$panes"
fi

DOCK_PANE="$S:workbench.1"

attempt=0
while [ "$attempt" -lt 20 ]; do
  out="$(tmux capture-pane -t "$DOCK_PANE" -p -J 2>/dev/null)"
  grep -q 'laneview dock' <<<"$out" && break
  attempt=$((attempt + 1))
  sleep 0.5
done

out="$(tmux capture-pane -t "$DOCK_PANE" -p -J)"
# free-2 is a bare shell -- no claude.exe-shaped process behind it -- so
# lanes.sh classifies it `dead`, not `free`. Asserting exactly that is the
# point: a renderer that only echoed the window's own name would have no
# way to produce this, and #173's finding was precisely that a name-only
# view can't distinguish "named free-2" from "actually available". The
# sidebar showing `dead` here, not the name's own implication of free,
# proves it is state and not decoration.
if grep -qE '^\s*x free-2\s+dead$' <<<"$out"; then
  ok "the docked pane renders real lane STATE from the live session, not just a window name"
else
  bad "the docked pane renders real lane STATE from the live session, not just a window name" "$out"
fi
if grep -qE '^\s*\. arch\s+supervisor$' <<<"$out"; then
  ok "the docked pane renders the supervisor row too, same as the static frame"
else
  bad "the docked pane renders the supervisor row too, same as the static frame" "$out"
fi

# Rename free-2 after the first frame; the loop's next refresh (<=1s away)
# must pick it up on its own -- nothing pokes the dock pane to redraw.
tmux rename-window -t "$S:free-2" free-2-renamed

attempt=0
picked_up=0
while [ "$attempt" -lt 10 ]; do
  out="$(tmux capture-pane -t "$DOCK_PANE" -p -J 2>/dev/null)"
  if grep -qE '^\s*x free-2-renamed\s+dead$' <<<"$out"; then
    picked_up=1
    break
  fi
  attempt=$((attempt + 1))
  sleep 0.5
done

if [ "$picked_up" = "1" ]; then
  ok "the docked pane's own refresh timer picks up a rename with no interaction"
else
  bad "the docked pane's own refresh timer picks up a rename with no interaction" "$out"
fi

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
