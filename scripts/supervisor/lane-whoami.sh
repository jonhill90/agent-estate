#!/bin/bash
# lane-whoami.sh -- answer "who am I" for the lane calling this, without ever
# asking tmux which window is FOCUSED.
#
# WHY THIS EXISTS (agent-supervisor#685). Every review/fix-pass brief this
# estate's loop writes tells the lane to derive its own `Review-Lane:` id
# with a bare
#
#   tmux display-message -p -t "$TMUX_PANE" '#{session_name}:#{window_index}'
#
# A tmux-dispatched lane has `$TMUX_PANE` exported into its process
# environment (the same anchor `register-lane-self.sh` and `loop-tick.md`'s
# lease section rely on), so that command is correct there. A `claude-print`
# lane has NO PANE AT ALL: `$TMUX_PANE` is unset, `-t ""` is not a target
# tmux will honour, and `display-message` silently falls back to answering
# for whichever window happens to be FOCUSED right now -- invariant 10's
# exact trap (#187), reappearing in the verdict trailer instead of in
# registration. Measured on agent-dotfiles#330's review: dispatched to
# `estate:2`, the trailer reported `estate:1` -- the director's own pane,
# which happened to be focused at the time the reviewer ran that command.
#
# This script is the one command a brief can name that works for BOTH lane
# shapes, because it picks its anchor from what the CALLER actually has
# rather than ever falling back to tmux's own guess:
#
#   pane lane           $TMUX_PANE is set   -> read THAT pane back, always
#                        with an explicit -t, exactly as register-lane-self.sh
#                        anchors itself (never a bare display-message)
#   claude-print lane    $TMUX_PANE is unset -> ask the ledger which task
#                        THIS worktree was dispatched as -- AGENTS.md
#                        invariant 10's documented self-lookup, `cli.py
#                        worktree-lane --path <cwd-or-toplevel> --include-reviews`
#
# Neither branch ever calls `tmux display-message` without an explicit `-t`,
# and neither branch accepts a caller-supplied lane id: same self-attestation
# posture as `register-lane-self.sh` -- a flag that could be handed the wrong
# answer would make this script the thing laundering it. This script reads;
# it never writes anything to the ledger or to tmux.
#
# Usage: lane-whoami.sh
#
# Prints the lane id to stdout and exits 0. Exits 1 with a reason on stderr
# if identity cannot be established -- this NEVER guesses and NEVER falls
# back to focus.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LEDGER_PYTHON="${AGENT_PYTHON_BIN:-python3}"
LEDGER_CLI="$HERE/cli.py"
TMUX_BIN="${TMUX_BIN:-tmux}"

if [ -n "${TMUX_PANE:-}" ]; then
  # Pane lane: anchor on the exact pane tmux started THIS process in.
  # Never bare (`display-message` with no `-t`) -- that answers for the
  # session's active window, invariant 10's own trap.
  lane=$("$TMUX_BIN" display-message -p -t "$TMUX_PANE" '#{session_name}:#{window_index}' 2>&1)
  rc=$?
  if [ "$rc" -ne 0 ] || [ -z "$lane" ]; then
    echo "lane-whoami.sh: refused -- \$TMUX_PANE is set to '$TMUX_PANE' but tmux could not read that pane back (rc=$rc): $lane" >&2
    exit 1
  fi
  printf '%s\n' "$lane"
  exit 0
fi

# Pane-less (claude-print/pi-rpc) lane: no pane exists to anchor on, so ask
# the ledger which task THIS worktree was dispatched as. `--include-reviews`
# is required, not optional (agent-supervisor#212): a reviewing lane's own
# worktree legitimately looks review-shaped, and the default
# author-finding filter would answer known:false for a row that exists.
worktree_path=$(git -C "$(pwd -P)" rev-parse --show-toplevel 2>/dev/null)
if [ -z "$worktree_path" ]; then
  worktree_path=$(pwd -P)
fi

out=$("$LEDGER_PYTHON" "$LEDGER_CLI" worktree-lane --path "$worktree_path" --include-reviews 2>&1)
rc=$?
if [ "$rc" -ne 0 ]; then
  echo "lane-whoami.sh: refused -- \$TMUX_PANE is unset (no pane to anchor on) and the worktree-lane lookup for '$worktree_path' failed:" >&2
  echo "$out" | sed 's/^/  /' >&2
  exit 1
fi
if ! grep -qF '"known":true' <<<"$out"; then
  echo "lane-whoami.sh: refused -- \$TMUX_PANE is unset (no pane to anchor on) and the ledger has no task dispatched at worktree '$worktree_path':" >&2
  echo "$out" | sed 's/^/  /' >&2
  exit 1
fi
lane=$(sed -n 's/.*"lane":"\([^"]*\)".*/\1/p' <<<"$out")
if [ -z "$lane" ]; then
  echo "lane-whoami.sh: refused -- worktree-lane reported known:true but no lane id: $out" >&2
  exit 1
fi
printf '%s\n' "$lane"
exit 0
