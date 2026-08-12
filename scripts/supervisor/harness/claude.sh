#!/bin/bash
# lanes.sh adapter: Claude Code.
#
# agent-dotfiles#201 gave every harness its own file so `lanes.sh` itself
# could stop naming any of them. Every regex below is UNCHANGED from
# pre-#201 lanes.sh -- moved here verbatim, not re-derived. The evidence
# each one is built from (real Claude Code panes, v2.1.220, driven through
# real dialogs during #124/#133/#159/#161/#164/#126) lives in this file's
# git history by way of lanes.sh's history; see those issues for the
# captures. This file only relocates the constants behind the adapter
# contract -- it does not re-measure anything.

HARNESS_NAME=claude
HARNESS_COMMAND_RE='^(claude|claude\.exe)$'

# Launch + send-keys dialect. #201's acceptance is `lanes.sh` alone; these
# two fields are recorded here for the next consumer (dispatch.sh,
# bootstrap-session.sh) and are NOT wired into either yet. Checked, not
# assumed: dispatch.sh's own message send (dispatch.sh:364) calls
# `tmux send-keys -t "$LANE" "$MESSAGE"` with no `-l`, and inbox-route.sh's
# does pass `-l` (inbox-route.sh:139) -- the estate is inconsistent about
# this today, which is exactly the kind of harness-specific literal #201
# says should live in an adapter instead of being reasoned about per call
# site. `1` here records what lanes.sh's own docs say Claude needs; wiring
# either caller to read it is follow-up, out of #201's scope.
HARNESS_LAUNCH_CMD='claude --dangerously-skip-permissions'
HARNESS_SEND_LITERAL=1

# Ready shape -- last non-empty line only (the #65 discipline). Two shapes:
# the real idle footer (`← 1 agent`, the count including the main agent
# itself so 1 means nothing delegated -- #126) and a bare `❯ ...` line with
# no footer, kept for older captures and fixtures that stand in for "a
# normal prompt" without spelling out footer chrome. See lanes.sh's own
# `emit_rows` for the ordering this is checked in.
HARNESS_READY_RE='^❯ [^←]*$|← 1 agent$'

# Busy -- last line only. Claude's elapsed-turn footer IS the last line
# while a turn runs (`esc to interrupt`), unlike Codex, whose equivalent
# marker sits several lines above a footer that never changes -- see
# harness/codex.sh's HARNESS_BUSY_TAIL comment for why that harness needs a
# wider window and this one does not.
#
# agent-dotfiles#207: a second busy shape, `↓ to manage`, appended whenever a
# background shell is registered -- singular or plural (`1 shell`/`2 shells`),
# with or without `ctrl+t to hide tasks` in between. Measured live against a
# real Claude Code pane (v2.1.220) in a throwaway tmux server, private
# TMUX_TMPDIR, 2026-08-12: mid-turn with a backgrounded shell running, the
# footer carries BOTH `esc to interrupt` and `↓ to manage` --
#   ⏵⏵ bypass permissions on · 1 shell · esc to interrupt · ← 1 agent · ↓ to manage
# --  already caught by the first alternative. Once the turn itself ends but
# the shell is still running, `esc to interrupt` drops and only the second
# alternative still matches --
#   ⏵⏵ bypass permissions on · 1 shell · ← 1 agent · ↓ to manage
# -- the exact shape agent-dotfiles#207 captured off two live dispatched
# lanes (`ad203-verdict-adapter`, `live184-ad184-race`) reading `unknown`.
# That pane is not mid-turn, but it is not idle either: a background shell it
# started is still doing work on the lane's behalf, and #207's acceptance is
# to treat that as busy (not a new state, and never free) rather than offer
# it to the dispatcher while the shell runs. Deliberately does NOT touch
# HARNESS_READY_RE: `← 1 agent$` is anchored to end-of-line, so a footer with
# `· ↓ to manage` trailing it already fails that anchor on its own -- adding
# this alternative here cannot make a lane MORE free, only less unknown,
# which is the #124/#126 one-way ratchet this file is under.
HARNESS_BUSY_RE='esc to interrupt|↓ to manage'
HARNESS_BUSY_TAIL=1

# Blocked gate (last line) plus the menu/text split (#159/#164). `Esc to
# cancel` / `Enter to select` cover four real dialogs (folder trust, /model,
# bash permission, /theme); the option-row shape (`❯ 1. ...`) covers #133's
# case where a numbered row, not footer chrome, was the last line.
HARNESS_BLOCKED_MARKERS='Esc to cancel|Enter to select'
HARNESS_OPTION_ROW_RE='^[[:space:]]*❯ [0-9]+\.[[:space:]]'
HARNESS_MENU_ENTER_RE='Enter to [a-z]+'
HARNESS_MENU_TAIL=6
# MODELED, NOT OBSERVED (#164): no genuine free-text-blocked prompt has ever
# been captured against a real Claude Code pane. This exists only so the
# estate's own modelled fixture keeps exercising inbox-route.sh's delivery
# mechanics; widening it to match a real prompt requires a real capture
# first, same rule as every marker above.
HARNESS_TEXT_PROMPT_RE='Type the [a-z]'
