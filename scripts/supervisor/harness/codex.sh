#!/bin/bash
# lanes.sh adapter: Codex CLI.
#
# agent-dotfiles#201. Every regex below is measured against a REAL codex
# pane (v0.147.0), not inferred, in a throwaway tmux session
# (`ad201-lab-<pid>`, bootstrap-session.sh --agent codex) on 2026-08-12 --
# never the live `agent-dotfiles` session, `director`, `harness-lab`, or
# `hill90`. The session was killed at the end of the review; the captures
# below are what it produced along the way.

HARNESS_NAME=codex
HARNESS_COMMAND_RE='^codex$'

# Launch + send-keys dialect. Not wired into dispatch.sh/bootstrap-session.sh
# by #201 (see harness/claude.sh's note -- same scope line). `codex --help`
# confirms `--dangerously-bypass-approvals-and-sandbox` exists ("Skip all
# confirmation prompts and execute commands without sandboxing") -- codex's
# named analogue of Claude's `--dangerously-skip-permissions` -- but it was
# NOT exercised live here: driving it would mean approving filesystem trust
# unattended, which is the exact #159/#161 hazard this issue is about.
# Recorded as the documented candidate, not a verified one.
HARNESS_LAUNCH_CMD='codex --dangerously-bypass-approvals-and-sandbox'
# Verified live: typing a literal `$1` into a running codex pane via
# `tmux send-keys -l` reproduced it byte-for-byte in the transcript --
# codex does not need anything Claude's `-l` doesn't already give it.
HARNESS_SEND_LITERAL=1

# Ready shape. Codex's footer -- "<model> <effort> · <cwd>" -- is the LAST
# non-empty line whether or not a turn is running (see HARNESS_BUSY_TAIL
# below); it is NOT proof of idle by itself, only proof this is a codex
# pane's normal chrome. Anchored on the middle dot before a path rather than
# the model name, which is a user setting (`gpt-5.5 medium` here, changeable
# via `/model`) and not safe to hardcode.
#
# agent-dotfiles#250: the path may start with `~` as well as `/`. Codex prints
# the cwd tilde-abbreviated whenever it is under $HOME, which is the ordinary
# case for this estate -- the live `free-codex` lane's last non-empty line, on
# 2026-08-12, verbatim:
#
#   gpt-5.5 medium · ~/source/repos/Personal/agent-dotfiles
#
# An absolute-path-only anchor never matched it, so the lane read `unknown`,
# `--free` withheld it exactly as #126/#131 intend, and dispatch.sh could
# never give the only untouched harness in the estate any work. The class is
# widened by exactly one character: `[/~]` covers the two cwd renderings codex
# is observed to emit and nothing else. It is deliberately NOT relaxed to "any
# text after the dot" -- #201's finding is that a matcher loose enough to
# cover every possible chrome is how one harness's shapes start falsely
# matching another's.
HARNESS_READY_RE='^[[:space:]]*[^·[:space:]][^·]*·[[:space:]]*[/~]'

# Busy. The turn indicator (`• Working (Ns • esc to interrupt)`) sits ABOVE
# the footer, not on it -- captured live:
#
#   • Working (9s • esc to interrupt)
#   › Improve documentation in @filename
#     gpt-5.5 medium · /private/.../ad-…
#
# Two non-empty lines separate it from the last line (the placeholder
# suggestion row, then the footer), so a last-line-only check (Claude's
# HARNESS_BUSY_TAIL=1) never sees it and every busy codex lane would read
# free. `4` bounds the window the way lanes.sh's own MENU_TAIL_LINES
# bounds menu detection: wide enough to hold the working line plus the two
# rows below it with margin, narrow enough to stay clear of the #65
# mistake (matching text the pane had merely printed, further up in a
# normal capture).
HARNESS_BUSY_RE='esc to interrupt'
HARNESS_BUSY_TAIL=4

# Blocked -- the #159/#161 hazard on new ground. Codex opens every session
# on a folder-trust menu, captured live, verbatim:
#
#   › 1. Yes, continue
#     2. No, quit
#
#     Press enter to continue
#
# `Press enter to continue` is codex's own last line while this menu is up
# (not `Esc to cancel` -- that phrase was never observed on a real codex
# pane, so it is not reused here). The option-row shape uses `›` (U+203A),
# not Claude's `❯` (U+276F) -- a real, easy-to-miss byte-level difference;
# Claude's HARNESS_OPTION_ROW_RE would silently never match a codex pane.
HARNESS_BLOCKED_MARKERS='[Pp]ress enter to continue'
HARNESS_OPTION_ROW_RE='^[[:space:]]*› [0-9]+\.[[:space:]]'
HARNESS_MENU_ENTER_RE='[Ee]nter to [a-z]+'
HARNESS_MENU_TAIL=6
# No genuine free-text-blocked prompt has been observed on a real codex pane
# (same posture as Claude's #164 note in harness/claude.sh, and the estate's
# own rule: absence of evidence never adds a case). Left unset rather than
# guessed -- an unrecognised codex blocked shape falls through to
# menu-blocked, lanes.sh's documented safe default, same as an unrecognised
# Claude one.
HARNESS_TEXT_PROMPT_RE=
