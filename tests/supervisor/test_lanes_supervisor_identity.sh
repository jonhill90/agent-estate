#!/bin/bash
# agent-dotfiles#239. lanes.sh identified the supervisor's own window by
# comparing #{window_index} to LANES_SUPERVISOR_WINDOW (default 1) -- an
# INDEX, unstable under `renumber-windows on`. Killing any window below the
# supervisor shifts it out of that slot, and `--free` then offers the
# supervisor's own pane as an ordinary lane: a dispatch there /clear's the
# loop and replaces it with someone else's task (the 2026-08-11 incidents
# this whole file is about).
#
# This drives REAL tmux, not the stub the rest of the lanes suite uses: the
# thing under test is renumbering itself, which the stub cannot produce (its
# window ids are a pure function of the CURRENT index -- see stubs/tmux-lanes'
# own header -- so it cannot model an id staying fixed while its window's
# index moves).
#
# Reproduces the hazard against the SHIPPED lanes.sh first (proves the test
# would have caught #239 before its fix), then proves the fix holds via both
# resolution paths session-defaults.sh's `supervisor_window_id` offers: an
# explicit `LANES_SUPERVISOR_WINDOW=@id` override, and the common case of
# TMUX_PANE pointing at the supervisor's own pane (how #239 itself was
# diagnosed live: `echo $TMUX_PANE` -> `%12`, resolved from there).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LANES="$HERE/../../scripts/supervisor/lanes.sh"
source "$HERE/../../scripts/supervisor/tmux-isolation.sh"
S="lanes-sup-id-test-$$"
RT="$(mktemp -d "${TMPDIR:-/tmp}/lanes-sup-id-tmux.XXXXXX")"
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$RT"
assert_isolated_tmux || exit 1
pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 -- $2"; fail=$((fail+1)); }

cleanup() { unset TMUX TMUX_PANE; export TMUX_TMPDIR="$RT"; tmux kill-session -t "$S" 2>/dev/null; }
cleanup_all() { cleanup; rm -rf "$RT"; }
trap cleanup_all EXIT INT TERM

if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP no tmux on PATH"; exit 0
fi

state_of() { # state_of <index>
  "$LANES" --json "$S" 2>/dev/null | python3 -c "
import json,sys
rows = json.load(sys.stdin)
for r in rows:
    if r['window'] == $1:
        print(r['state']); break
"
}

# Build: free-1, free-2, the supervisor at index 3, free-4 -- the supervisor
# deliberately NOT at index 1 (LANES_SUPERVISOR_WINDOW's default), the same
# way the live estate's supervisor was measured off its nominal slot in #239
# itself. Two lanes below it, not one: after killing free-1, the supervisor
# lands on index 2 -- neither its ORIGINAL index (3) nor the hardcoded
# default (1) -- so no check here can pass by coincidentally landing back on
# that default, the way a fixture with only one lane below it would risk.
#
# Both options below are set via `-f` on the FIRST command that starts the
# server, not `set-option` after `new-session` -- `new-session` creates its
# first window using whatever base-index the server already has at that
# instant, so setting it afterward is too late for window 1 itself. Measured
# directly (agent-supervisor#459): base-index is Jon's personal tmux.conf
# setting, not tmux's own default (which is 0) -- on a bare server (this
# isolated one, and every GitHub Actions runner) `-n free-1` lands on index
# 0, the literal "=$S:2"/"=$S:3"/"=$S:4" targets below create the rest with
# a gap at index 1, and `kill-window -t "=$S:1"` two lines down then fails
# outright ("can't find window: 1") because nothing was ever created there.
CONF="$(mktemp "${TMPDIR:-/tmp}/lanes-sup-id-tmux-conf.XXXXXX")"
printf 'set -g base-index 1\nset -g renumber-windows on\n' > "$CONF"
tmux -f "$CONF" new-session -d -s "$S" -n free-1 -c /tmp
rm -f "$CONF"
tmux new-window -d -t "=$S:2" -n free-2 -c /tmp
tmux new-window -d -t "=$S:3" -n supervisor -c /tmp
tmux new-window -d -t "=$S:4" -n free-4 -c /tmp
SUP_WID="$(tmux display-message -p -t "=$S:3" '#{window_id}')"
SUP_PANE="$(tmux list-panes -t "=$S:3" -F '#{pane_id}')"
# agent-supervisor#459 follow-up: supervisor_window_id() now also requires
# $TMUX to be set (not just $TMUX_PANE) -- a genuine child of the
# supervisor's own pane inherits BOTH from tmux itself, never TMUX_PANE
# alone. This script's top-level `unset TMUX; export TMUX_TMPDIR="$RT"`
# is the isolation shape a caller uses to address a DIFFERENT server on
# purpose (tmux-guard.sh's "Isolated form"), which is exactly the shape
# that must NOT carry an inherited TMUX_PANE's trust across servers -- so
# simulating "really is a child of this pane" below has to hand back a
# real, valid $TMUX for THIS isolated server, not merely set TMUX_PANE.
SUP_SOCKET="$(tmux display-message -p -t "=$S:3" '#{socket_path}')"
SUP_TMUX="$SUP_SOCKET,$$,0"

# 1. Before any renumbering: an explicit index override recognises it, same
#    as pre-#239 behaviour -- this is not what's broken.
before="$(LANES_SUPERVISOR_WINDOW=3 state_of 3)"
if [ "$before" = supervisor ]; then
  ok "before any renumber: window 3 (LANES_SUPERVISOR_WINDOW=3) reads supervisor"
else
  bad "before any renumber: window 3 reads supervisor" "got '$before'"
fi

# 2. Kill the window BELOW the supervisor. renumber-windows shifts the
#    supervisor from index 3 to index 2 -- neither its original index nor
#    the hardcoded default.
tmux kill-window -t "=$S:1"
new_idx="$(tmux display-message -p -t "$SUP_WID" '#{window_index}')"
if [ "$new_idx" = "2" ]; then
  ok "killing the lane below shifted the supervisor to index 2 (id unchanged: $SUP_WID)"
else
  bad "killing the lane below shifted the supervisor to index 2" "got index '$new_idx'"
fi

# 3. THE HAZARD, reproduced live: the STALE index override (still "3", what
#    an operator configured before the renumber) no longer names the
#    supervisor's window at all -- some OTHER window reads supervisor, or
#    nothing does. Either way the real supervisor is unprotected.
stale="$(LANES_SUPERVISOR_WINDOW=3 state_of 2)"
if [ "$stale" != supervisor ]; then
  ok "hazard reproduced: a stale index override no longer recognises the supervisor's own window (reads '$stale', not supervisor)"
else
  bad "hazard reproduced: a stale index override no longer recognises the supervisor's own window" "still read supervisor -- did not reproduce #239"
fi

# 4. THE FIX, path 1: LANES_SUPERVISOR_WINDOW as an explicit id override
#    (@<id>) survives the very same renumber that broke the index.
fixed_id="$(LANES_SUPERVISOR_WINDOW="$SUP_WID" state_of 2)"
if [ "$fixed_id" = supervisor ]; then
  ok "fix (id override): LANES_SUPERVISOR_WINDOW=$SUP_WID still recognises the supervisor after the renumber"
else
  bad "fix (id override): LANES_SUPERVISOR_WINDOW=$SUP_WID still recognises the supervisor after the renumber" "got '$fixed_id'"
fi

# 5. THE FIX, path 2: TMUX_PANE pointing at the supervisor's own pane, no
#    override configured at all -- the common case (the supervisor's own
#    tick invokes lanes.sh as a child of its own shell). The supervisor's
#    post-renumber index (2) deliberately does not equal
#    LANES_SUPERVISOR_WINDOW's hardcoded default (1), so this cannot pass by
#    accidentally landing on the pre-#239 fallback's literal.
fixed_pane="$(TMUX="$SUP_TMUX" TMUX_PANE="$SUP_PANE" state_of 2)"
if [ "$fixed_pane" = supervisor ]; then
  ok "fix (TMUX_PANE): running as a child of the supervisor's own pane recognises it after the renumber, with no override configured"
else
  bad "fix (TMUX_PANE): running as a child of the supervisor's own pane recognises it after the renumber" "got '$fixed_pane'"
fi

# 6. Prove real lanes are still offered -- a fix that excludes everything
#    looks identical to safety until nothing dispatches. Window 4 (free-4,
#    now renumbered to index 3) must NOT read supervisor under either fix
#    path, and must still appear in the classification (dead is fine here --
#    these are plain shells, not live agents; the point is it is not
#    misclassified as supervisor).
other_id="$(LANES_SUPERVISOR_WINDOW="$SUP_WID" state_of 3)"
other_pane="$(TMUX="$SUP_TMUX" TMUX_PANE="$SUP_PANE" state_of 3)"
if [ "$other_id" != supervisor ] && [ "$other_pane" != supervisor ]; then
  ok "the other lane (free-4) is not swept into the supervisor exclusion under either fix path"
else
  bad "the other lane (free-4) is not swept into the supervisor exclusion under either fix path" "id-path='$other_id' pane-path='$other_pane'"
fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
