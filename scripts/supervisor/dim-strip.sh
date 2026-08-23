#!/bin/bash
# The ONE place this estate defines "is this span a Claude Code prompt
# suggestion, or real pane content" -- agent-supervisor#521.
#
# WHY THIS EXISTS: `input-box.sh` (agent-dotfiles#141, agent-supervisor#193)
# already found and solved this for `send.sh`'s own read of the input box:
# Claude Code paints a predicted-next-message placeholder into an idle box,
# styled dim (SGR 2), and typed text never is -- so "is there real text
# here" reduces to "is there anything outside a dim span". #521 found a
# SECOND class of reader that needed the identical answer and didn't have
# it: `tick-scan.sh`'s and `look.py capture`'s default pane reads, both
# plain `tmux capture-pane -p`, with no escapes fetched at all -- so a
# suggestion and real content were byte-for-byte indistinguishable through
# either. This file is the fix for that gap: the SGR walk `input-box.sh`
# already proved correct against real captures, factored out so a second
# (or third, or a different-language) reader calls it instead of
# re-deriving or half-deriving it. `input-box.sh` itself now sources this
# rather than keeping its own copy -- there is exactly one definition of
# the dim rule in this repo, not two that could silently drift apart.
#
# CONTRACT: reads a `tmux capture-pane -p -e` frame on stdin (escapes are
# required input -- there is nothing to strip from a plain capture, which is
# exactly the bug this file exists to close). Prints the same frame with
# every SGR escape code removed and every span that was painted dim (SGR 2)
# removed ENTIRELY -- not merely destyled, gone -- so a suggestion cannot
# read as real text once it comes out the other side. Operates one line at
# a time; a dim span observed to cross a line boundary has never been
# captured live against a real pane (matches `input-box.sh`'s own per-row
# model) -- if that ever changes this needs re-deriving against a fresh
# capture, not widened blind.
strip_dim_sgr() {
  awk '
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
    { print undim($0) }
  '
}

# Usable as a standalone filter (`dim-strip.sh < capture.txt`), not only a
# sourced function -- `look.py` (a different language entirely) shells out
# to this file directly rather than reimplementing the SGR walk in Python,
# so there stays exactly one definition of the dim rule no matter which
# language is doing the reading.
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  strip_dim_sgr
fi
