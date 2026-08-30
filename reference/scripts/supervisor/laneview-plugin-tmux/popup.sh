#!/usr/bin/env bash
# Run by the keybinding laneview.tmux installs. Opens a tmux popup running
# one laneview.sh renderer over the CURRENT session -- read-only, and gone
# the moment the popup closes. Never a sidebar pane injected into every
# window (that is opensessions.sh's, and #173's measured cost of it).
#
# Which renderer: the `@laneview-impl` tmux option, default `tui` (the
# viewer #7 asked for). Deleting scripts/supervisor/laneview/tui.sh does
# not break this plugin -- laneview.sh's own "no renderer" error (naming
# what IS available) is what the popup shows instead, and setting
# `@laneview-impl text` in ~/.tmux.conf switches to the other renderer with
# no edit here. That is the #178 brief's "with adapters we can enable
# disable or remove what we need", applied to which renderer this launcher
# opens.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LANEVIEW_SH="$HERE/../laneview.sh"

impl="$(tmux show-option -gqv @laneview-impl)"
impl="${impl:-tui}"

session="$(tmux display-message -p '#S')"

# Reviewed: single-quoting $session directly into the -E command string
# breaks (and could inject shell syntax) if a session name ever contains a
# single quote -- tmux session names are supervisor-chosen today, but this
# costs nothing to close. %q shell-escapes each value before it is spliced
# into the popup's command string.
printf -v quoted_impl '%q' "$impl"
printf -v quoted_session '%q' "$session"
printf -v quoted_laneview '%q' "$LANEVIEW_SH"

tmux display-popup -w 90% -h 90% -T " laneview: $session " \
  -E "bash $quoted_laneview $quoted_impl $quoted_session"
