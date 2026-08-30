#!/bin/bash
# #110: look.py's unit tests (test_look.py) cover its pure logic against a
# fake tmux. That proves the decoding and diffing are correct, but not that
# the tool actually sees a real pane -- capture-pane's exact escape shapes
# were only pinned by measuring a real tmux server (see look.py's module
# docstring). This suite drives a real, isolated tmux server end to end:
# capture reads back real content, navigate lands a real keypress, and
# frames proves motion against a genuinely animating pane -- and, just as
# important, proves NO motion against a genuinely static one. Same
# isolation discipline as test_laneview_tui_interactive.sh and
# test_bootstrap_session.sh: TMUX_TMPDIR-scoped, never the attached server.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/../../scripts/supervisor/tmux-isolation.sh"
LOOK="$HERE/../../scripts/supervisor/look.py"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }

echo "look.py (real isolated tmux)"

if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP no tmux on PATH"; exit 0
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "  SKIP no python3 on PATH"; exit 0
fi

RT="$(mktemp -d "${TMPDIR:-/tmp}/look-it.XXXXXX")"
unset TMUX
export TMUX_TMPDIR="$RT"
assert_isolated_tmux || exit 1
S="look-test-$$"
cleanup() { unset TMUX; export TMUX_TMPDIR="$RT"; assert_isolated_tmux; tmux kill-server 2>/dev/null; rm -rf "$RT"; }
trap cleanup EXIT INT TERM

tmux -f /dev/null new-session -d -s "$S" -n shell -x 60 -y 8
# The shell needs a moment to finish its own startup before send-keys lands
# reliably -- measured live: a send-keys issued immediately after
# new-session is silently dropped while the shell is still initializing.
sleep 1.5

# --- capture: read-only, plain text -----------------------------------
tmux send-keys -t "$S:shell" "printf 'hello-look\\n'"
sleep 0.3
tmux send-keys -t "$S:shell" Enter
sleep 1
plain="$(python3 "$LOOK" capture -t "$S:shell")"
if grep -q 'hello-look' <<<"$plain"; then
  ok "capture (plain) reads back real pane content"
else
  bad "capture (plain) reads back real pane content" "$plain"
fi

# --- capture: escapes + annotate reproduce the #109 diagnosis ---------
tmux send-keys -t "$S:shell" "printf '\\033[7mSELECTED\\033[0m\\n'"
sleep 0.3
tmux send-keys -t "$S:shell" Enter
sleep 1
escaped="$(python3 "$LOOK" capture -t "$S:shell" --escapes)"
if grep -qF $'\e[7m' <<<"$escaped"; then
  ok "capture --escapes carries the real reverse-video byte (#109's own check)"
else
  bad "capture --escapes carries the real reverse-video byte" "$escaped"
fi

# --- capture: agent-supervisor#521's own red/green -----------------------
# A dim (SGR 2) span painted onto a real pane -- the exact shape Claude
# Code's prompt-suggestion feature paints into an idle input box. The
# pre-#521 plain path (`capture-pane -p`, no escapes fetched at all) could
# not have told this apart from real typed content; this is the RED half.
# `look.py capture`'s plain path must now drop it entirely (GREEN); the
# escaped path must still show it, since a caller asking for the DOM wants
# the real bytes.
# The shell echoes the command line it was given BEFORE running it, so
# "DIM-SUGGESTION" is on screen twice under the un-fixed behavior: once as
# the echoed source text (never dim, unaffected either way) and once as the
# rendered dim output capture-pane -p would previously have returned
# unfiltered. Counting occurrences (not a bare substring match) is what
# actually isolates the RENDERED instance from the echoed one.
tmux send-keys -t "$S:shell" "printf '\\033[2mDIM-SUGGESTION\\033[0m real-text\\n'"
sleep 0.3
tmux send-keys -t "$S:shell" Enter
sleep 1
dim_plain="$(python3 "$LOOK" capture -t "$S:shell")"
dim_plain_hits="$(grep -oc 'DIM-SUGGESTION' <<<"$dim_plain")"
if [ "$dim_plain_hits" -eq 1 ] && grep -q 'real-text' <<<"$dim_plain"; then
  ok "capture (plain) drops the rendered dim suggestion, keeps the echoed command + real text (#521)"
else
  bad "capture (plain) drops dim-styled suggestion text (#521)" "hits=$dim_plain_hits body=$dim_plain"
fi

dim_escaped="$(python3 "$LOOK" capture -t "$S:shell" --escapes)"
dim_escaped_hits="$(grep -oc 'DIM-SUGGESTION' <<<"$dim_escaped")"
if [ "$dim_escaped_hits" -eq 2 ]; then
  ok "capture --escapes still carries the dim span for a caller who wants the real bytes"
else
  bad "capture --escapes still carries the dim span" "hits=$dim_escaped_hits body=$dim_escaped"
fi

annotated="$(python3 "$LOOK" capture -t "$S:shell" --annotate)"
if grep -q 'reverse' <<<"$annotated" && grep -q 'SELECTED' <<<"$annotated"; then
  ok "capture --annotate names the reverse-video run in text, not just bytes"
else
  bad "capture --annotate names the reverse-video run in text" "$annotated"
fi

# --- navigate: MUTATES -- sends a key, re-captures -------------------
nav_out="$(python3 "$LOOK" navigate -t "$S:shell" "printf 'nav-landed\\n'" Enter --settle 1)"
if grep -q 'nav-landed' <<<"$nav_out"; then
  ok "navigate sends keys and returns the frame after they land"
else
  bad "navigate sends keys and returns the frame after they land" "$nav_out"
fi

# --- frames: static pane must report NO motion ------------------------
tmux new-window -t "$S" -n static "bash -c 'clear; echo FROZEN; sleep 30'"
sleep 1
static_report="$(python3 "$LOOK" frames -t "$S:static" --count 3 --interval 0.3)"
if grep -q 'NO CHANGE' <<<"$static_report"; then
  ok "frames on a genuinely static pane reports NO CHANGE -- the defect #110 wants catchable"
else
  bad "frames on a genuinely static pane reports NO CHANGE" "$static_report"
fi
if python3 "$LOOK" frames -t "$S:static" --count 2 --interval 0.3 --assert-motion >/tmp/look-assert-motion.$$ 2>&1; then
  bad "--assert-motion exits nonzero on a static pane" "$(cat /tmp/look-assert-motion.$$)"
else
  ok "--assert-motion exits nonzero on a static pane"
fi
rm -f "/tmp/look-assert-motion.$$"

# --- frames: genuinely animating pane must report motion ---------------
tmux new-window -t "$S" -n spinner "bash -c 'i=0; while true; do i=\$((i+1)); clear; echo \"frame-\$i\"; sleep 0.2; done'"
sleep 1
motion_report="$(python3 "$LOOK" frames -t "$S:spinner" --count 4 --interval 0.4)"
if grep -q 'MOTION DETECTED' <<<"$motion_report"; then
  ok "frames on a genuinely animating pane reports MOTION DETECTED"
else
  bad "frames on a genuinely animating pane reports MOTION DETECTED" "$motion_report"
fi
if python3 "$LOOK" frames -t "$S:spinner" --count 3 --interval 0.4 --assert-motion --json >/tmp/look-motion.$$ 2>&1; then
  ok "--assert-motion exits 0 on a genuinely animating pane"
else
  bad "--assert-motion exits 0 on a genuinely animating pane" "$(cat /tmp/look-motion.$$)"
fi
rm -f "/tmp/look-motion.$$"

# --- png: the actual screenshot pipeline (#110's corrected pipeline) --
# Skips, not fails, when no headless Chrome is on this host -- same
# convention as the curses skip above: an unmet real dependency is reported,
# not faked.
chrome_bin="$(PYTHONPATH="$HERE/../../scripts/supervisor" python3 -c '
import look
print(look.find_chrome_binary() or "")
')"
if [ -z "$chrome_bin" ]; then
  echo "  SKIP look.py png -- no headless Chrome found on this host"
else
  PNG_OUT="$RT/frame.png"
  if png_msg="$(python3 "$LOOK" png -t "$S:shell" -o "$PNG_OUT" 2>&1)"; then
    if [ -s "$PNG_OUT" ] && [ "$(od -An -tx1 -N4 "$PNG_OUT" | tr -d ' \n')" = "89504e47" ]; then
      ok "look.py png renders a real PNG file (starts with the PNG magic bytes)"
    else
      bad "look.py png renders a real PNG file" "no file, or not a PNG: $(ls -la "$PNG_OUT" 2>&1)"
    fi
  else
    bad "look.py png renders a real PNG file" "$png_msg"
  fi
fi

echo "look.py: $pass ok, $fail failed"
[ "$fail" -eq 0 ]
