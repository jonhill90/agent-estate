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

# Launch + send-keys dialect. agent-supervisor#30 measured on 2026-08-13
# against codex-cli 0.147.0 in throwaway tmux sockets only: `codex --help`
# documents `-a, --ask-for-approval <APPROVAL_POLICY>` with accepted values
# `untrusted`, `on-request`, and `never`, plus `-s, --sandbox` with
# `danger-full-access`. The live codex lane had been launched as
# `codex --dangerously-bypass-approvals-and-sandbox` (`ps -o command=`) and
# still stalled on both "Would you like to run the following command?" and
# "Would you like to make the following edits?". The explicit posture below
# is the measured working replacement from #30: disable approval asks with
# `-a never` and separately keep the old no-sandbox posture with
# `-s danger-full-access`. Do not append the old dangerous shortcut here:
# the CLI rejects combining it with `--ask-for-approval never`, and keeping
# it alone leaves the actual approval policy implicit.
HARNESS_LAUNCH_CMD='codex -a never -s danger-full-access'

# agent-dotfiles#256: this IS codex's unattended posture, measured live above
# (#30) -- the old `--dangerously-bypass-approvals-and-sandbox` shortcut
# still stalled on prompts; this explicit `-a never -s danger-full-access`
# pair is the one that did not. Recorded again under its own name so
# bootstrap-session.sh's `--unattended` resolves it explicitly per harness,
# the same way harness/claude.sh does, instead of a caller having to assume
# H_LAUNCH_CMD is always the unattended answer (it is not, for every
# harness -- see harness/copilot.sh).
HARNESS_UNATTENDED_CMD="$HARNESS_LAUNCH_CMD"
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

# agent-supervisor#124: the usage-limit banner. Measured live off a real,
# stuck codex lane (`agent-supervisor:5`, `codex` 0.147.0, 2026-08-19) after
# TWO briefs had been typed into it and both answered by this banner instead
# of the agent, verbatim:
#
#   › Read .../rev100.md and do exactly what it says. You are reviewing PR #100 ...
#   ■ You've hit your usage limit. Upgrade to Pro ... or try again at Aug 20th, 2026 12:11 AM.
#   › Improve documentation in @filename
#     gpt-5.5 medium · /private/var/folders/.../ad-96-rev100-13003
#
# `lanes.sh --free` reported this lane free: the banner is not a menu (no
# "Press enter to continue", no numbered option row) and codex repaints its
# ordinary idle footer below it, so by the time lanes.sh samples, the last
# line is the SAME footer HARNESS_READY_RE matches for a genuinely idle
# lane. That is #124's whole finding -- idle-at-the-shell and able-to-work
# have come apart, and the existing last-line-only probes cannot tell them
# apart because the banner has already scrolled out of the one line they
# read.
#
# `hit your usage limit` intentionally drops the apostrophe glyph
# (`You've`) rather than spelling it -- that character's exact byte is not
# confirmed stable across terminals/fonts, and the surrounding words are
# distinctive enough on their own that loosening this one byte does not
# risk matching anything else codex paints.
#
# lanes.sh matches this against the SAME bounded, visible-pane-only tail it
# already uses for menu detection (HARNESS_MENU_TAIL, not scrollback -- the
# #65 discipline still applies, this only widens which of the visible lines
# count, not how far back capture-pane looks). Landing on `menu-blocked`
# (lanes.sh's own default for "blocked, but not one of the positively-typed
# kinds") is deliberate: a lane pinned here needs a human, exactly the
# `blocked`/`--blocked` semantics this estate already has, and reads
# `unknown` under a whitelist-not-guess an inch away from `free`.
HARNESS_LIMIT_RE='hit your usage limit'

# agent-supervisor#115: NOT WIRED. Codex's own ready-shape footer already
# carries the model on its last line every tick (`gpt-5.5 medium · <cwd>`,
# see HARNESS_READY_RE above) -- unlike Claude's splash-only self-report, a
# real HARNESS_MODEL_RE here would be reliable rather than launch-moment-only.
# #115's own incident and this estate's fleet are Claude-only, so this is
# left unset rather than guessed at from the READY_RE comment's captures
# without re-verifying the exact column split live. An unset H_MODEL_RE
# reads `unknown` for every codex lane, which is the correct, honest default
# until that capture is done -- not a regression, since nothing reads model
# for codex lanes today either.
HARNESS_MODEL_RE=
