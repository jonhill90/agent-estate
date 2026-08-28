#!/bin/bash
# Sourced-only, from dispatch.sh, immediately after the pre-existing siblings
# (input-box.sh, send.sh, harness-registry.sh, session-defaults.sh,
# tmux-guard.sh). agent-supervisor#716: split out of dispatch.sh (2753 lines)
# behind a re-export-free source, same shape as #713's watchdog.sh split --
# see that PR's own header for the precedent. Never run standalone: it sets
# nothing of its own (`set -uo pipefail` lives once, in dispatch.sh itself,
# before this is sourced) and relies on $HERE and the harness-registry.sh /
# tmux-guard.sh functions dispatch.sh already sourced ahead of it.
#
# `dispatch_rehome_lane` -- the recovery path `abort_send` (dispatch-worktree.sh)
# points an operator at when a lane is left inside a worktree that is about to
# be torn down -- and dispatch.sh's own `--rehome-lane` entry point, which must
# run and exit BEFORE any of the ordinary dispatch flow (flag parsing, gates,
# lane selection) does anything. Both live here, together, because the second
# is nothing but a thin CLI wrapper around the first.
dispatch_rehome_lane() {
  local target="$1" dir="$2" harness="${3:-}" hidx="" cmd="" launch_cmd=""
  [ -n "$target" ] || { echo "dispatch: --rehome-lane requires a tmux target" >&2; return 2; }
  [ -n "$dir" ] || dir="${HOME:-/tmp}"
  [ -d "$dir" ] || { echo "dispatch: re-home target directory does not exist: $dir" >&2; return 1; }

  if [ -n "$harness" ]; then
    hidx=$(harness_index_for_name "$harness") || hidx=""
  else
    cmd=$(tmux display-message -p -t "$target" '#{pane_current_command}' 2>/dev/null) || cmd=""
    [ -n "$cmd" ] && hidx=$(harness_index_for_command "$cmd") || hidx=""
  fi
  if [ -z "$hidx" ] || [ -z "${H_LAUNCH_CMD[$hidx]:-}" ]; then
    echo "dispatch: cannot re-home $target -- no launch command for harness '${harness:-${cmd:-unknown}}'" >&2
    return 1
  fi

  launch_cmd="${H_LAUNCH_CMD[$hidx]}"
  # agent-supervisor#236: the harness is the pane's PROCESS, handed to
  # respawn-pane as its own argv -- never typed into whatever the respawn
  # produces. The prior shape (respawn, sleep, then blind `send-keys
  # "$launch_cmd" Enter`) is the mechanism #236 reports: a lane was found
  # blocked on a menu offering to run a pasted launch command, because
  # nothing checked what was listening for those keystrokes a second later.
  # One tmux call now does what three did, so there is no window in which
  # the launch command exists as text and nothing is left to settle for --
  # H_SEND_LITERAL governed how `send-keys` parsed ITS OWN argument for tmux
  # key names, which does not apply to a shell command handed to
  # respawn-pane's own argv.
  #
  # agent-supervisor#166/#421: re-homing a lane respawns its process the same
  # way bootstrap-session.sh/restore.sh do, so it gets the same tmux guard on
  # PATH ahead of the real binary -- best effort, same as those two sites.
  local guard_bin=""
  guard_bin="$(install_tmux_guard 2>&2)" || guard_bin=""
  if [ -n "$guard_bin" ]; then
    launch_cmd="PATH=\"$guard_bin:\$PATH\" $launch_cmd"
  fi
  if ! tmux respawn-pane -k -t "$target" -c "$dir" "$launch_cmd" 2>/dev/null; then
    echo "dispatch: tmux respawn-pane failed while re-homing $target to $dir" >&2
    return 1
  fi
}

if [ "${1:-}" = "--rehome-lane" ]; then
  rehome_target="${2:-}"
  rehome_dir="${3:-${HOME:-/tmp}}"
  rehome_harness="${4:-}"
  dispatch_rehome_lane "$rehome_target" "$rehome_dir" "$rehome_harness"
  exit $?
fi
