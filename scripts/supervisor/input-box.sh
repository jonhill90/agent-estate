#!/bin/bash
# Is a lane's input box empty, or is it holding text nobody sent?
#
# WHY: agent-dotfiles#141. Two lanes sat for 40 minutes each holding a full
# brief that had been typed into the input box and never submitted -- the
# `Enter` arrived while `/clear` was still repainting and was swallowed. From
# outside, that lane is indistinguishable from an idle one: an agent is
# running, no turn is in flight, the prompt is sitting there. The work was not
# queued, not running, and not lost. It was just sitting there.
#
# `lanes.sh` reads ONLY the last non-empty line of the pane (the #65 fix), and
# the input box is never that line -- the footer is. So the box's contents were
# invisible to every instrument the estate has.
#
# EVERYTHING BELOW WAS MEASURED, not inferred. Driven against a real Claude
# Code TUI (v2.1.220) in a throwaway tmux server on a private TMUX_TMPDIR,
# never a live lane.
#
# --- 1. finding the box ---------------------------------------------------
# Three shapes that all render as "a chevron and some text" are distinct at the
# byte level:
#
#   live input box     e2 9d af  c2 a0  ...     `❯` + U+00A0 NO-BREAK SPACE
#   transcript echo    e2 9d af  20     ...     `❯` + ordinary space
#   dialog option row  e2 9d af  20  32 2e ...  `❯` + ordinary space + `2. `
#
# Only the live input box paints `❯` immediately followed by a NO-BREAK SPACE.
# That is what makes reading more than one line safe here: #65's failure was a
# probe that matched text the pane had merely PRINTED, and neither an echoed
# prompt nor an option row can forge the NO-BREAK SPACE. `capture-pane` with no
# `-S` still reads only the visible screen; no scrollback is consulted.
#
# The box is closed below by a horizontal rule, so its full extent -- a long
# brief wraps across several rows, and a message begun with a newline leaves
# the first row empty -- is the prompt row through to that rule:
#
#     ────────────────────────────────────────────
#     ❯<NBSP>
#       second line text
#     ────────────────────────────────────────────
#       ⏵⏵ bypass permissions on (shift+tab to cycle)
#
# --- 2. telling text from the placeholder ---------------------------------
# AN EMPTY BOX IS NOT BLANK. It paints a rotating suggestion in the same
# position an unsent brief occupies:
#
#     ❯<NBSP>Try "write a test for <filepath>"
#     ❯<NBSP>now echo goodbye            (a follow-up suggested from the turn)
#
# A first version of this file matched on the box being non-blank and called
# both of those `text`. That is not a near miss: it withholds EVERY freshly
# started idle lane, and `--free` collapses to nothing.
#
# The suggestion is painted DIM and typed text is not. `capture-pane -e` keeps
# the attributes, and the difference is unambiguous:
#
#   placeholder   ^[[39m❯<NBSP> ^[[2mTry "write a test for <filepath>"^[[0m
#   typed text    ^[[39m❯<NBSP> Read /brief.md and do it.
#
# So the box is empty iff it holds nothing outside a dim span. That is why this
# file demands `capture-pane -e` and parses SGR rather than reading plain text.
#
# `#{cursor_x}` was tried as the discriminator and rejected: it is 2 on an
# empty box, but also 2 whenever the cursor sits at the start of a full one, so
# it cannot be trusted in the direction that matters. It agrees with the dim
# rule on every shape measured here; it is not used, because the dim rule
# answers the question directly and the cursor only correlates with it.
#
# --- 3. contract ----------------------------------------------------------
# Reads a `capture-pane -pe` capture on stdin, prints exactly one of
#
#   text     the box holds characters that were never submitted
#   empty    the box is present and holds nothing but its placeholder
#   unknown  no box could be identified -- an older or another harness, a
#            dialog covering the prompt, or a pane too short to show the box
#
# `unknown` is the fail-safe answer and callers must treat it as "no new
# information", never as `empty`. Callers may only use `text` to WITHHOLD a
# lane or FAIL a dispatch; nothing here may ever be the reason a lane becomes
# available. That is the one-way ratchet #124 and #126 put these states under.
#
# --- 4. harness-parameterized, agent-estate#446 ----------------------------
# Everything above this line was measured against Claude Code ONLY and this
# file's own header used to say so. #446: driven live against a real codex
# pane (0.148.0, then re-confirmed on 0.149.0 -- throwaway tmux socket,
# `TMUX_TMPDIR`, never a live lane), `input_box_state`/`input_box_text` read
# `unknown` against a codex pane every time, empty or holding typed text,
# because codex's own chrome differs at the byte level in TWO ways from what
# was hardcoded here: the marker glyph is `›` (U+203A), not `❯` (U+276F) --
# already noted in `harness/codex.sh`'s own comment on `HARNESS_OPTION_ROW_RE`
# -- and codex's ready-state box has NO closing horizontal rule at all; it is
# closed by the first blank line instead (confirmed live: an empty box is one
# row -- `› ` plus a dim placeholder -- followed directly by a blank line,
# then the `<model> · <cwd>` footer; a wrapped long message's continuation
# rows are plain, unmarked, indented text, also followed by a blank line
# before the footer once the message ends).
#
# So both functions below now take the box's own MARKER and its CLOSE mode as
# parameters instead of the two module constants directly, following the same
# per-harness-field contract `harness-registry.sh` already uses for
# `H_OPTION_ROW_RE`/`H_READY_RE`/etc: `harness/claude.sh` and
# `harness/codex.sh` each record their own `HARNESS_INPUT_BOX_PROMPT` /
# `HARNESS_INPUT_BOX_CLOSE`, and `send.sh`'s callers thread them through via
# `--box-prompt`/`--box-close` (see that file). Called with NO arguments --
# every existing caller in this repo before #446, and every fixture in
# `test_input_box.sh` -- both functions behave EXACTLY as before: `${1-...}`
# (not `${1:-...}`) means an OMITTED argument defaults to Claude's own marker
# and the rule-close mode, while an EXPLICITLY EMPTY marker (what a harness
# with no measured box shape at all -- `harness/copilot.sh` today -- passes)
# fails closed to `unknown` immediately, never silently reusing Claude's
# shape. That distinction is why this is `${1-x}` throughout and not the more
# common `${1:-x}`, which cannot tell "omitted" from "passed empty" apart.
#
#   prompt       the literal marker bytes, immediately followed by whatever
#                separator that harness paints after it (Claude: `❯` + NBSP;
#                Codex: `›` + an ordinary space) -- matched the same way the
#                original Claude-only code did, `index(line, prompt) == 1`,
#                so a harness's own choice of separator is what keeps this
#                from matching an echoed transcript line or a menu option row
#                that reuses the same leading glyph (see harness/codex.sh's
#                own note: the `›` on an echoed past turn is painted DIM,
#                so `strip_dim_sgr` removes the marker itself before this
#                function ever sees it -- confirmed live, not assumed).
#   close_mode   `rule` (default) -- Claude's own shape, closed by a row of
#                `─`/`━`. `blank` -- Codex's shape, closed by the first row
#                that is empty once whitespace is stripped.

# `❯` (U+276F) immediately followed by U+00A0. Written as byte escapes because
# `\u` escapes need bash 4 and /bin/bash on macOS is 3.2.
INPUT_BOX_PROMPT=$'\xe2\x9d\xaf\xc2\xa0'
INPUT_BOX_NBSP=$'\xc2\xa0'

# agent-supervisor#521: the dim-stripping SGR walk this file pioneered now
# lives in `dim-strip.sh`, factored out so `tick-scan.sh` and `look.py`
# reuse the exact same rule instead of re-deriving or half-deriving it. This
# file sources it rather than keeping its own copy -- see that file's own
# doc comment for the full rationale.
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/dim-strip.sh"

# input_box_state [prompt] [close_mode] < capture-pane -pe output
input_box_state() {
  local prompt="${1-$INPUT_BOX_PROMPT}"
  local close_mode="${2-rule}"

  # No known box shape for this harness at all (agent-estate#446) --
  # `harness/copilot.sh` today. `unknown` is the fail-safe answer, never a
  # guess that reuses Claude's own marker for a harness nobody has measured.
  if [ -z "$prompt" ]; then
    echo unknown
    return
  fi

  strip_dim_sgr | awk -v prompt="$prompt" -v nbsp="$INPUT_BOX_NBSP" -v close_mode="$close_mode" '
    # dim spans are already gone (strip_dim_sgr ran first) -- what is left
    # is plain text: the box marker and any typed content, never the
    # placeholder. Same detection rule as before: the marker is not itself
    # painted dim, so its position is unaffected by that upstream step.
    { plain[NR] = $0 }
    END {
      # The LAST prompt row on the visible screen. A pane can hold more than
      # one only if the harness repainted mid-capture, or (agent-estate#446,
      # measured live against codex) an earlier turn ECHOES the same marker
      # -- codex paints that echo dim, so strip_dim_sgr already removed the
      # marker itself from it, and this loop never sees it. Either way the
      # live box is the lower one.
      p = 0
      for (i = 1; i <= NR; i++) if (index(plain[i], prompt) == 1) p = i
      if (p == 0) { print "unknown"; exit }

      # What closes the box. Without it the box has been cut off by the
      # bottom of the pane and its full contents are not on screen, so
      # nothing can be concluded.
      e = 0
      if (close_mode == "blank") {
        # agent-estate#446: codex draws no closing rule at all -- measured
        # live, its box (empty or holding a wrapped multi-row message) is
        # always followed by the first genuinely blank row before the
        # `<model> · <cwd>` footer.
        for (i = p + 1; i <= NR; i++) {
          t = plain[i]; gsub(/[[:space:]]/, "", t)
          if (t == "") { e = i; break }
        }
      } else {
        for (i = p + 1; i <= NR; i++) if (plain[i] ~ /^[─━]+$/) { e = i; break }
      }
      if (e == 0) { print "unknown"; exit }

      # Drop everything up to and including the prompt marker itself.
      body = plain[p]
      k = index(body, prompt)
      if (k > 0) body = substr(body, k + length(prompt))
      for (i = p + 1; i < e; i++) body = body plain[i]
      gsub(/[[:space:]]/, "", body)
      gsub(nbsp, "", body)
      print (body == "" ? "empty" : "text")
    }
  '
}

# input_box_text < capture-pane -pe output
#
# agent-supervisor#193. `input_box_state` above answers "empty or text";
# this answers "text OF WHAT" -- the box's own content, prompt marker and
# SGR stripped, whitespace and NBSP collapsed out exactly the way
# `input_box_state` normalises before comparing to "". Needed wherever a
# caller must know WHERE something appears in the box, not merely THAT it
# appears somewhere on the pane: `send.sh`'s `--proof-head` anchors a token
# to the START of this string, which a whole-pane substring search (the
# ORIGINAL proof check) cannot do -- that gap is exactly what let a `/clear`
# glued onto the front of a brief still read as "landed", because
# `Read <brief>` was still a true substring of `/clearRead <brief>`.
#
# Same box-finding rules as `input_box_state` (last prompt row, closed by a
# rule, everything in between); this is deliberately NOT implemented by
# calling that function and re-deriving the body from its verdict -- there
# is no verdict to re-derive FROM, only the underlying awk, so the box-
# finding logic is repeated here rather than invented differently. Prints
# the body (possibly empty) when a complete box was found on screen, or
# NOTHING (not even a newline) when it was not -- `unknown` has no content
# to report, and a caller must treat empty output the same fail-closed way
# `input_box_state`'s callers already treat `unknown`: not evidence of
# anything.
input_box_text() {
  local prompt="${1-$INPUT_BOX_PROMPT}"
  local close_mode="${2-rule}"

  # Same fail-closed rule as input_box_state above: no known marker means no
  # content to report, same as `unknown` has none.
  if [ -z "$prompt" ]; then
    return
  fi

  strip_dim_sgr | awk -v prompt="$prompt" -v nbsp="$INPUT_BOX_NBSP" -v close_mode="$close_mode" '
    { plain[NR] = $0 }
    END {
      p = 0
      for (i = 1; i <= NR; i++) if (index(plain[i], prompt) == 1) p = i
      if (p == 0) { exit }

      e = 0
      if (close_mode == "blank") {
        for (i = p + 1; i <= NR; i++) {
          t = plain[i]; gsub(/[[:space:]]/, "", t)
          if (t == "") { e = i; break }
        }
      } else {
        for (i = p + 1; i <= NR; i++) if (plain[i] ~ /^[─━]+$/) { e = i; break }
      }
      if (e == 0) { exit }

      body = plain[p]
      k = index(body, prompt)
      if (k > 0) body = substr(body, k + length(prompt))
      for (i = p + 1; i < e; i++) body = body plain[i]
      gsub(/[[:space:]]/, "", body)
      gsub(nbsp, "", body)
      printf "%s", body
    }
  '
}
