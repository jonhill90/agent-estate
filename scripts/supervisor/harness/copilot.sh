#!/bin/bash
# lanes.sh adapter: GitHub Copilot CLI.
#
# agent-dotfiles#201. Measured against a real copilot pane (v1.0.79) in the
# same throwaway `ad201-lab-<pid>` session harness/codex.sh describes, on
# 2026-08-12. Copilot's own idle footer is the reproduction quoted in #201
# itself; the rest below is what driving that live pane through a turn and
# a `/model` menu actually produced.

HARNESS_NAME=copilot
# `pane_current_command` for the copilot CLI reports `node` (it is a Node
# app), not `copilot` -- confirmed live, and it is also exactly what #201's
# own reproduction table shows (`3  free-3  node  unknown`). That string is
# not unique to this harness: any other Node-based CLI landing in a lane
# (there is currently one -- opencode, explicitly out of scope per #201's
# "Not in scope" section because it does not stay resident to have a pane
# at all) would match this COMMAND_RE too. A collision would apply this
# adapter's ready/busy/blocked chrome to a pane that never paints it, which
# fails toward `unknown`, not toward a false `free` -- the fail-safe
# property #201 requires holds either way. Nothing better than the process
# name was available to key on.
HARNESS_COMMAND_RE='^node$'

# Launch + send-keys dialect. Not wired into dispatch.sh/bootstrap-session.sh
# by #201 (see harness/claude.sh's note -- same scope line). `copilot --help`
# lists `--allow-all` ("Enable all permissions") as the named equivalent of
# Claude's `--dangerously-skip-permissions`; not exercised live for the same
# reason codex's analogue wasn't (see harness/codex.sh).
HARNESS_LAUNCH_CMD='copilot --allow-all'
# Verified live: `tmux send-keys -l` delivered a literal prompt to a running
# copilot pane and it appeared in the transcript unmodified.
HARNESS_SEND_LITERAL=1

# Ready shape -- last non-empty line only, verified live and quoted
# verbatim in #201's own reproduction:
#
#   ← open sidebar · / commands · ? help · tab next tab   Claude Sonnet 5
#
# Anchored on `← open sidebar`, the one segment of that footer that does not
# vary with the selected model (the trailing `Claude Sonnet 5 · Medium` or
# similar does).
HARNESS_READY_RE='← open sidebar'

# Busy -- last line only, verified live: copilot's busy footer, unlike
# codex's, IS the last line while a turn runs --
#
#   ◎ Working esc interrupt                              Claude Sonnet 5
#
# Note the wording: `esc interrupt`, not Claude's `esc to interrupt` -- no
# "to". A shared BUSY_RE across harnesses would have missed this one.
HARNESS_BUSY_RE='esc interrupt'
HARNESS_BUSY_TAIL=1

# Blocked / menu detection: NOT SET. A `/model` menu was driven live and
# does render a selection list, but it uses `❯` next to a bare model name
# (`❯ Auto`) with no numbered rows and no "Enter to <verb>" style footer --
# unlike every marker this file's siblings use, nothing observed there was
# a stable, harness-identifying substring safe to key a regex on without
# risking a false match against copilot's own ready/typed-text rows. #201's
# reported hazard is specifically codex's startup trust menu; no genuine
# tool-approval dialog was reachable for copilot in the time available for
# this review. Leaving these unset is the documented, honest gap: an
# unrecognised copilot blocked shape reads `unknown` (fail-safe, withheld by
# --free) rather than a guessed regex that might not fire on the real thing
# or might fire on the wrong thing. Widening this requires a real capture
# first, same rule as every other marker in this directory.
HARNESS_BLOCKED_MARKERS=
HARNESS_OPTION_ROW_RE=
HARNESS_MENU_ENTER_RE=
HARNESS_MENU_TAIL=6
HARNESS_TEXT_PROMPT_RE=
