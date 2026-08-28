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

# input-box.sh matches NBSP by byte escape ($'\xc2\xa0'), not by locale-aware
# character class -- the file's own comment says so (bash 3.2 on macOS has no
# \u). Under a UTF-8 locale, awk's [:space:] class ALSO matches NBSP as a wide
# character, which silently does INPUT_BOX_NBSP's job for it: the gsub on that
# var becomes redundant and a defanged INPUT_BOX_NBSP goes unnoticed. Forcing
# the byte-oriented locale here is what makes the test below actually exercise
# the constant instead of exercising awk's locale tables.
export LC_ALL=C

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

# agent-supervisor#240: the six lines measured live off six stranded lanes,
# 2026-08-16T06:52Z, verbatim -- `lanes.sh` had classified every one of these
# `free`, `busy` or `broken`, never `unsent`, while each pane genuinely held
# this exact text unsubmitted. Reproduced directly against the real Claude
# Code v2.1.220 binary (isolated tmux server, private TMUX_TMPDIR) rather than
# assumed: every one of these already reads `text` from this file unmodified
# -- so the regression #240 reported was never in this parsing logic. These
# rows exist so a FUTURE regression here -- the shape #216 found in the ready
# footer -- cannot silently return unnoticed.
want "#240 measured shape 1 (agent-supervisor:5) is text" text \
  "$(box "${P}check for other stranded lanes")"
want "#240 measured shape 2 (agent-supervisor:6) is text" text \
  "$(box "${P}check on the suite run")"
want "#240 measured shape 3 (agent-tui:4) is text" text \
  "$(box "${P}check the other lanes for anything else stuck")"
want "#240 measured shape 4 (skills:3) is text" text \
  "$(box "${P}check the notify script for any lingering result")"
want "#240 measured shape 5 (skills:4) is text" text \
  "$(box "${P}file agent-supervisor#232 as a filed issue reference check")"
want "#240 measured shape 6 (skills:5) is text" text \
  "$(box "${P}clean up the pr200-review temp branch and worktree")"

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

# --- INPUT_BOX_NBSP: a second constant with a different job ----------------
# INPUT_BOX_PROMPT anchors the box; INPUT_BOX_NBSP strips a trailing NBSP
# from the body once the box is found -- a real terminal can pad an
# otherwise-empty box with a bare, non-dim NBSP rather than an ordinary
# space, and that byte is not the placeholder (not dim) and not whitespace
# under the byte-oriented locale forced above. Only line 148's
# `gsub(nbsp, "", body)` removes it. Defang INPUT_BOX_NBSP and this must go
# to `text`; the anchor's own tests above must not move.
NBSP=$'\xc2\xa0'
want "a bare trailing NBSP with no typed text is still empty" empty \
  "$(box "${P}${NBSP}")"

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

# --- agent-estate#446: a SECOND, genuinely different box shape --------------
# #446's own root cause: `tests/supervisor/stubs/tmux-dispatch` always drew
# Claude's own `❯`+NBSP box chrome regardless of which harness a test said it
# was dispatching to, so nothing in the suite could catch a differently-
# shaped box going unrecognised -- the codex regression shipped invisibly.
# These fixtures are codex's OWN chrome, byte-for-byte what a real, fully-
# ready codex pane (0.148.0, then reconfirmed 0.149.0) painted in a throwaway
# tmux socket, never inferred: marker `›` (U+203A) immediately followed by an
# ORDINARY space (not NBSP, unlike Claude); no closing rule at all -- the box
# is closed by the first blank row instead. See harness/codex.sh's own
# HARNESS_INPUT_BOX_PROMPT/HARNESS_INPUT_BOX_CLOSE comment for the full
# captures these fixtures are built from.
# CODEX_PROMPT_BYTES is what `input_box_state`/`input_box_text` are actually
# called with -- the marker AFTER strip_dim_sgr has already removed every
# SGR code (`›` + an ordinary space, nothing else). CODEX_P below is a
# DIFFERENT thing: the RAW fixture prefix, bold-SGR-wrapped, matching what a
# real capture-pane -pe actually returns before this file's own dim-strip
# runs on it. Conflating the two here was the bug the first draft of this
# test caught the hard way -- an explicit constant for each keeps it from
# happening again.
CODEX_PROMPT_BYTES=$'\xe2\x80\xba '
want_codex() { # want_codex <name> <expected> <capture>
  local got; got=$(input_box_state "$CODEX_PROMPT_BYTES" blank <<<"$3")
  if [ "$got" = "$2" ]; then echo "  ok   $1"; pass=$((pass+1));
  else echo "  FAIL $1 — want '$2', got '$got', for:"; sed 's/^/       /' <<<"$3"; fail=$((fail+1)); fi
}

# Bold marker, reset, ORDINARY space -- captured raw bytes:
#   \x1b[1m\xe2\x80\xba\x1b[0m<space>
CODEX_P=$'\033[1m\xe2\x80\xba\033[0m '
CODEX_FOOTER='  gpt-5.6-terra medium · /private/tmp'

box_codex() { printf '%s\n\n%s\n' "${CODEX_P}$1" "$CODEX_FOOTER"; }

want_codex "codex: a brief typed and never submitted is text" text \
  "$(box_codex 'Read /private/tmp/brief-446-codex.md and do exactly what it says.')"
want_codex "codex: a genuinely empty box (placeholder only) is empty" empty \
  "$(box_codex "$(printf '\033[2mAsk Codex to do anything\033[0m')")"
want_codex "codex: a box with nothing in it at all is empty" empty "$(box_codex '')"

# No closing rule at all (agent-estate#446's own finding) -- the box is
# closed by the first BLANK row, and a wrapped message's continuation rows
# (plain, unmarked, indented -- measured live) are still followed by one
# once the message ends, before the footer.
want_codex "codex: text wrapped onto an unmarked continuation row is text" text \
  "$(printf '%s\n%s\n\n%s\n' "${CODEX_P}Read /brief.md and do exactly" \
     "  what it says. Do all of your work in the worktree" "$CODEX_FOOTER")"

# A past turn's echoed prompt reuses the SAME glyph, but codex paints the
# MARKER ITSELF dim (measured live: `\x1b[1;2m› \x1b[0m<text>`) -- so
# strip_dim_sgr removes it before this file's "last matching row" scan ever
# sees it, the same disambiguation Claude's NBSP-vs-space split gives it, by
# a mechanism this harness happens to paint for free. Still must not become
# the box.
want_codex "codex: a past turn's dim-marked echo is not mistaken for the box" empty \
  "$(printf '%s\n%s\n%s\n\n%s\n' "$(printf '\033[1;2m\xe2\x80\xba\033[0m Say hello and nothing else.')" \
     '' 'Hello' "$(box_codex "$(printf '\033[2mAsk Codex to do anything\033[0m')")")"

# A box cut off by the bottom of the pane (no blank row ever follows) is
# still unknown, the identical fail-safe Claude's rule-close gets.
want_codex "codex: a box cut off by the bottom of the pane is unknown" unknown \
  "$(printf '%s\n' "${CODEX_P}Read /brief.md and do exactly what it says.")"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
