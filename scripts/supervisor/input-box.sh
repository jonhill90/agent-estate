#!/bin/bash
# Is a Claude Code lane's input box empty, or is it holding text nobody sent?
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

# `❯` (U+276F) immediately followed by U+00A0. Written as byte escapes because
# `\u` escapes need bash 4 and /bin/bash on macOS is 3.2.
INPUT_BOX_PROMPT=$'\xe2\x9d\xaf\xc2\xa0'
INPUT_BOX_NBSP=$'\xc2\xa0'

# input_box_state < capture-pane -pe output
input_box_state() {
  awk -v prompt="$INPUT_BOX_PROMPT" -v nbsp="$INPUT_BOX_NBSP" '
    function strip_sgr(s,   out) {
      out = s
      gsub(/\033\[[0-9;?]*[A-Za-z]/, "", out)
      return out
    }
    # Everything the box shows that is NOT inside a dim (SGR 2) span. The
    # placeholder is the whole dim span; typed text is never dim.
    function undim(s,   out, i, n, esc, params, p, np, j, dim) {
      out = ""; dim = 0; i = 1; n = length(s)
      while (i <= n) {
        if (substr(s, i, 2) == "\033[") {
          esc = ""; j = i + 2
          while (j <= n && index("0123456789;?", substr(s, j, 1)) > 0) {
            esc = esc substr(s, j, 1); j++
          }
          # j now sits on the final byte of the sequence (or past the end).
          if (j <= n) {
            if (substr(s, j, 1) == "m") {
              np = split(esc, params, ";")
              if (np == 0) { dim = 0 }
              for (p = 1; p <= np; p++) {
                if (params[p] == "2") dim = 1
                else if (params[p] == "0" || params[p] == "22" || params[p] == "") dim = 0
              }
            }
            i = j + 1
          } else {
            i = n + 1
          }
          continue
        }
        if (!dim) out = out substr(s, i, 1)
        i++
      }
      return out
    }
    { raw[NR] = $0; plain[NR] = strip_sgr($0) }
    END {
      # The LAST prompt row on the visible screen. A pane can hold more than
      # one only if the harness repainted mid-capture; the live box is the
      # lower one either way.
      p = 0
      for (i = 1; i <= NR; i++) if (index(plain[i], prompt) == 1) p = i
      if (p == 0) { print "unknown"; exit }

      # The rule that closes the box. Without it the box has been cut off by
      # the bottom of the pane and its full contents are not on screen, so
      # nothing can be concluded.
      e = 0
      for (i = p + 1; i <= NR; i++) if (plain[i] ~ /^[─━]+$/) { e = i; break }
      if (e == 0) { print "unknown"; exit }

      # Drop everything up to and including the prompt marker itself.
      body = undim(raw[p])
      k = index(body, prompt)
      if (k > 0) body = substr(body, k + length(prompt))
      for (i = p + 1; i < e; i++) body = body undim(raw[i])
      gsub(/[[:space:]]/, "", body)
      gsub(nbsp, "", body)
      print (body == "" ? "empty" : "text")
    }
  '
}
