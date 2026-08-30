#!/usr/bin/env bash
# TPM entrypoint for the second viewer jonhill90/agent-supervisor#7 asked
# for: "a tmux plugin", Jon's own, as opposed to opensessions.sh which
# drives someone else's program. This file is what TPM (or a manual
# `run-shell`, see README.md in this directory) executes on tmux start.
#
# It does exactly one thing: bind a key that opens a popup running a
# laneview.sh renderer (tui.sh by default). It is a LAUNCHER for the
# existing contract, not a second reader of tmux or the ledger -- the
# popup's content comes entirely from `laneview.sh <impl> <session>`,
# which is the same command a human could type by hand. Nothing here runs
# outside of the keypress: no daemon, no background timer, no sidebar pane
# injected into every window (the #173-measured cost of the OpenSessions
# path). That is rule 3 of laneview/README.md, "cost nothing when unused",
# satisfied by construction -- TPM only sources this file; it does not run
# it in a loop.
#
# Never edits ~/.tmux.conf (agent-dotfiles#178 brief constraint). Install
# by adding ONE line there yourself -- see README.md.

set -uo pipefail

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

DEFAULT_KEY="L"
key="$(tmux show-option -gqv @laneview-key)"
key="${key:-$DEFAULT_KEY}"

tmux bind-key "$key" run-shell "$CURRENT_DIR/popup.sh"
