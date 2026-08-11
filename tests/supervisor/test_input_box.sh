#!/bin/bash
# input-box.sh answers one question -- is this lane holding a brief nobody
# submitted -- and it is the question #141 turns on. Two lanes sat for 40
# minutes in exactly that state while every instrument said they were idle.
#
# Every capture below is the shape a REAL Claude Code TUI (v2.1.220) paints,
# taken from a throwaway tmux server on a private TMUX_TMPDIR. The bytes are
# the point: `❯` + U+00A0 for the live box, `❯` + an ordinary space for a
# transcript echo or a dialog option row, and SGR 2 (dim) around the
# placeholder. Anything that reads this file as plain text cannot tell those
# apart, which is precisely how the state stayed invisible.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HERE/../../scripts/supervisor/input-box.sh"
pass=0; fail=0

want() { # want <name> <expected> <capture>
  local got; got=$(input_box_state <<<"$3")
  if [ "$got" = "$2" ]; then echo "  ok   $1"; pass=$((pass+1));
  else echo "  FAIL $1 — want '$2', got '$got', for:"; sed 's/^/       /' <<<"$3"; fail=$((fail+1)); fi
}

echo "input-box.sh"

RULE=$(printf '─%.0s' $(seq 1 40))
FOOTER='  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← 1 agent'
# The live box's prompt: `❯` then U+00A0. Byte-for-byte what capture-pane
# returns for a real pane.
P=$'\033[39m\xe2\x9d\xaf\xc2\xa0'

box() { printf '%s\n%s\n%s\n%s\n' "$RULE" "$1" "$RULE" "$FOOTER"; }

# --- the state #141 is about ----------------------------------------------
want "a brief typed and never submitted is text" text \
  "$(box "${P}Read /brief.md and do exactly what it says.")"
want "a single stray character is text" text "$(box "${P}x")"

# --- the false positive that would have broken the estate ------------------
# An empty box is NOT blank. Claude Code paints a rotating suggestion in the
# same row, and in a plain-text capture it is indistinguishable from an unsent
# brief. A first version of this file matched "box is non-blank" and called
# these `text`, which withholds every freshly started idle lane and collapses
# `--free` to nothing. The dim attribute is the whole discriminator.
want "a dim placeholder is empty, not text" empty \
  "$(box "${P}$(printf '\033[2mTry "write a test for <filepath>"\033[0m')")"
# Observed live: after a turn, the suggestion is a plausible follow-up rather
# than generic help text. `now echo goodbye` was read as a typed message during
# this investigation and it was not -- nobody typed it.
want "a dim follow-up suggestion is empty, not text" empty \
  "$(box "${P}$(printf '\033[2mnow echo goodbye\033[0m')")"
want "a box with nothing in it at all is empty" empty "$(box "${P}")"

# --- the box is more than one row ------------------------------------------
# A 360-character brief wraps across five rows in an 80-column lane, and a
# message begun with a newline leaves the FIRST row empty with the text below
# it -- checking only the prompt row would call that empty and offer the lane.
want "text wrapped onto continuation rows is text" text \
  "$(printf '%s\n%s\n%s\n%s\n%s\n' "$RULE" "${P}Read /brief.md and do exactly" \
     "  what it says. Do all of your work in the worktree" "$RULE" "$FOOTER")"
want "an empty first row with text below is text" text \
  "$(printf '%s\n%s\n%s\n%s\n%s\n' "$RULE" "${P}" "  second line text" "$RULE" "$FOOTER")"

# --- fail safe -------------------------------------------------------------
# Every one of these must answer `unknown`, and callers must treat `unknown`
# as "no new information". Answering `empty` for any of them would be the one
# direction that must never happen: it is the answer that makes a lane
# available.
want "a pane with no box at all is unknown" unknown \
  "$(printf '%s\n%s\n' 'some transcript output' "$FOOTER")"
want "a box cut off by the bottom of the pane is unknown" unknown \
  "$(printf '%s\n%s\n' "$RULE" "${P}Read /brief.md and do exactly what it says.")"
want "another harness's pane is unknown" unknown \
  "$(printf '%s\n' 'copilot> waiting for input')"

# --- the #65 discipline: printed text cannot forge a box -------------------
# A transcript echo of a submitted message renders `❯` + an ORDINARY space, and
# a dialog option row does the same. Neither can be mistaken for the live box,
# which is what makes reading more than one line safe here at all. A lane whose
# scrollback is full of chevrons is still `unknown`, never `text`.
want "a transcript echo of a past message is not a box" unknown \
  "$(printf '%s\n%s\n%s\n' '❯ say only the word pong' '⏺ pong' "$FOOTER")"
want "a dialog option row is not a box" unknown \
  "$(printf '%s\n%s\n%s\n' '❯ 1. Post the comment' '  2. Show me the comment' ' Esc to cancel')"
# The nastiest version: the pane is displaying THIS FILE, so a literal
# `❯` + U+00A0 appears in its output. It is still not the live box, because the
# box's closing rule is what bounds it -- and a review pane showing source has
# a real box of its own below, which is the one that answers.
want "source text quoting the marker does not become the box" empty \
  "$(printf '%s\n%s\n%s\n%s\n' "reviewing: ${P}Read /brief.md" "$RULE" "${P}" "$RULE")"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
