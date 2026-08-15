#!/bin/bash
# agent-supervisor#199: the isolation guard (`assert_isolated_tmux`,
# scripts/supervisor/tmux-isolation.sh, b489ebc/#258) lives INSIDE the files
# that source it. A lane's own worktree is a full checkout pinned to
# whatever commit it last fetched -- if that commit predates the guard, the
# copy of e.g. test_bootstrap_session.sh sitting in that worktree has no
# `unset TMUX; export TMUX_TMPDIR=...; assert_isolated_tmux || exit 1` at
# all, and running it from inside a tmux pane (a lane's own condition)
# creates its throwaway `bootstrap-test-$$` session on whatever socket
# $TMUX already points at -- the live estate's default socket. That is the
# exact shape #177, #180 and #199 all measured.
#
# A guard shipped to `main` only protects a worktree once that worktree has
# advanced past the guard's commit. This script closes the observability
# gap between "the guard is on main" and "every worktree that can run these
# tests actually has it": it walks every worktree `git worktree list` knows
# about and asks, of each one's PINNED commit (not `main`'s), whether the
# guard marker is present in every test file this repo treats as touching
# real tmux create/destroy verbs.
#
# This is read-only against every worktree -- `git show <sha>:<path>`, never
# a checkout, a switch, or a tmux call of any kind. It cannot itself leak a
# session, because it never runs one.
#
# A worktree missing the marker is not proof it will leak (it may never run
# that suite again before being pruned), but it is exactly the condition
# that produced #199's leak, and unlike a leaked session on the live socket,
# this is safe to check for continuously.
#
# A file that has never yet learned to call a real create/destroy tmux verb
# cannot leak regardless of the marker's presence -- an early run of this
# audit against test_digest.sh's own history proved that the naive
# marker-only check is a false-positive generator: many worktrees are pinned
# to a commit where the file has NEITHER the verb NOR the guard, because
# both were added together in the same commit (#100, #48). Flagging those
# would report a gap where nothing was ever unguarded. So a file only counts
# as a gap if its PINNED content contains a real verb (VERB_MARKER) without
# also containing the guard (WORKTREE_GUARD_MARKER).
#
# Usage: worktree-guard-audit.sh [<repo-path>]
#   <repo-path>   defaults to the current directory. Must be a worktree of
#                 the repo whose OTHER worktrees you want audited --
#                 `git worktree list` is shared across all of them.
#
#   WORKTREE_GUARD_FILES   newline- or space-separated list of repo-relative
#                           paths to check (default: the tmux-touching test
#                           files enumerated for #199 below).
#   WORKTREE_GUARD_MARKER  grep -E pattern that counts as "guarded"
#                           (default: assert_isolated_tmux). test_restore.sh
#                           is deliberately excluded from the default list:
#                           it isolates itself with its own private-socket
#                           PATH wrapper (`tmux -L "$SOCKET"`), a different
#                           and independently safe technique documented in
#                           its own header, not a gap this script should flag.
set -uo pipefail

REPO="${1:-.}"
if ! git -C "$REPO" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "worktree-guard-audit: $REPO is not a git worktree" >&2
  exit 2
fi

DEFAULT_FILES="tests/supervisor/test_bootstrap_session.sh
tests/supervisor/test_advance_live.sh
tests/supervisor/test_digest.sh
tests/supervisor/test_inbox_poll_service.sh
tests/supervisor/test_lane_done.sh
tests/supervisor/test_lanes_env_parity.sh
tests/supervisor/test_laneview_tmux_plugin.sh
tests/supervisor/test_laneview_tui_interactive.sh
tests/supervisor/test_look.sh
tests/supervisor/test_poller_recover.sh
tests/supervisor/test_poller_window.sh
tests/supervisor/test_watchdog_launchd_relaunch.sh
tests/supervisor/test_watchdog_poller_copy.sh"

FILES="${WORKTREE_GUARD_FILES:-$DEFAULT_FILES}"
MARKER="${WORKTREE_GUARD_MARKER:-assert_isolated_tmux}"
VERB_MARKER="${WORKTREE_GUARD_VERB_MARKER:-tmux (new-session|kill-session|kill-server|kill-window|respawn-(pane|window))}"

gaps=0
checked=0
while IFS= read -r line; do
  [ -n "$line" ] || continue
  wt_path="$(awk '{print $1}' <<<"$line")"
  wt_sha="$(awk '{print $2}' <<<"$line")"
  [ -n "$wt_sha" ] || continue
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    content="$(git -C "$REPO" show "${wt_sha}:${f}" 2>/dev/null)" || continue  # file absent at this commit: nothing to leak
    checked=$((checked + 1))
    grep -qE "$VERB_MARKER" <<<"$content" || continue  # no real create/destroy verb at this commit: nothing to leak
    if ! grep -qE "$MARKER" <<<"$content"; then
      echo "GAP  $wt_path  ($wt_sha)  $f  -- calls a real tmux verb without '$MARKER'" >&2
      gaps=$((gaps + 1))
    fi
  done <<<"$FILES"
done < <(git -C "$REPO" worktree list | awk '{print $1, $2}')

echo "worktree-guard-audit: $checked file@worktree pairs checked, $gaps gap(s)"
[ "$gaps" -eq 0 ]
