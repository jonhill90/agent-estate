#!/bin/bash
# Administratively retire a dispatched lane WITHOUT killing the window it
# occupies -- agent-supervisor#564.
#
# WHY: `lane-done.sh` frees a lane only once its worker signals completion
# (`tmux wait-for`); there was never a tool for the other case -- an operator
# who decides mid-task that a lane should come out of rotation right now.
# Measured live, 2026-08-23: retiring three dispatched lanes by hand
# (`tmux kill-window`) destroyed the tmux WINDOWS those lanes had been
# dispatched into, not just their ledger registrations. Two of those windows
# (`build-2`, `build-3`) predated the dispatch that claimed and renamed them
# -- `dispatch.sh` respawns whatever idle pane `lanes.sh --free` and the
# ledger's `lane-free` query hand it (CLAUDE.md invariant 9: lane identity is
# `<session>:<index>`, not the window's name), so a window a human had put
# other, unrelated work into can be claimed the same as any pool window. The
# estate went from five windows to two -- `director` and `build-4` -- and
# nothing announced it: the loop that drives these lanes reads window names,
# and three of five had simply stopped existing. Recovered only by luck: all
# three lanes were idle and every branch they held was already pushed.
#
# THE FIX (agent-supervisor#564's second option, chosen because it needs no
# new state -- the first option, "a dispatched lane creates its own window",
# would have to invent and persist where that window's SLOT re-enters the
# pool, which the estate does not track today): retiring a lane through this
# script NEVER runs `kill-window` or any other verb that can destroy a pane.
# It unregisters the lane from the ledger (the same `record-completion` call
# `lane-done.sh` makes) and renames the window back to the `free-N`
# convention `lanes.sh`/`dispatch.sh` already expect free lanes to carry --
# the exact rename-back `lane-done.sh` performs on ordinary completion,
# reused rather than reimplemented. The pane itself, and whatever the lane's
# agent process was doing, is left exactly as it was.
#
# THE GUARD agent-supervisor#564 asks for explicitly: retirement refuses
# outright when the target's worktree has uncommitted changes or commits not
# yet pushed anywhere -- the same class of check `worktree.sh`'s `safe_remove`
# and `gc` already apply before discarding a worktree (see that file's own
# comments), for the identical reason: an operator who wants a lane back is
# asking to reclaim a WINDOW, never to discard whatever work is sitting in
# it. A lane mid-edit, or holding commits nobody else has a copy of, is not
# safe to hand back to the pool -- retiring it here would silently orphan
# that work in a worktree the next `worktree.sh gc` sweep is free to reclaim.
#
# Usage: lane-retire.sh <window> [session]
#
# <window>   same contract as lane-done.sh's own <window> argument --
#            session-qualified (`agent-supervisor:@12`, what `lanes.sh
#            --free` and `dispatch.sh` print as `target:`) or a bare
#            `@12`/index for an operator typing this by hand off the window
#            list, in which case [session] supplies the session to qualify
#            it with.
#
# Exit 0  the lane is unregistered (or was already free) and the window
#         still exists, carrying its restored `free-N` name (or its stale
#         name, if the rename itself failed -- loud on stderr either way,
#         same posture as lane-done.sh).
# Exit 1  refused: dirty tree, unpushed commits, target resolves to the
#         supervisor's own window, or the target could not be read at all.
# Exit 2  usage error.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./session-defaults.sh
. "$HERE/session-defaults.sh"

WINDOW="${1:-}"
SESSION="${2:-$(lanes_session_or_default)}"

if [ -z "$WINDOW" ]; then
  sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2
  exit 2
fi

# Same session-prefix detection as lane-done.sh (#259): a caller passing the
# printed `target:` string through the old `${SESSION}:${WINDOW}` build got
# a double-qualified, unparseable target that silently fell back to tmux's
# ACTIVE window -- very often the supervisor's own. Detect a prefix already
# present rather than always adding one.
case "$WINDOW" in
  *:*) TARGET="$WINDOW" ;;
  *)   TARGET="${SESSION}:${WINDOW}" ;;
esac

# --- 1. resolve the pane's cwd; nothing below can be verified without it --
PANE_CWD="$(tmux display-message -p -t "$TARGET" '#{pane_current_path}' 2>/dev/null)"
if [ -z "$PANE_CWD" ] || [ ! -d "$PANE_CWD" ]; then
  echo "lane-retire: could not read a usable working directory for ${TARGET} -- refusing (cannot verify it is safe to reclaim)" >&2
  exit 1
fi

# --- 2. refuse on uncommitted changes -- worktree.sh safe_remove's own check
if ! git -C "$PANE_CWD" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "lane-retire: ${PANE_CWD} (${TARGET}) is not inside a git worktree -- refusing (cannot verify it holds no unsaved work)" >&2
  exit 1
fi
STATUS="$(git -C "$PANE_CWD" status --porcelain 2>&1)"
if [ -n "$STATUS" ]; then
  echo "lane-retire: ${TARGET}'s worktree ($PANE_CWD) has uncommitted changes -- refusing to retire" >&2
  echo "$STATUS" >&2
  exit 1
fi

# --- 3. refuse on commits nobody else has a copy of -----------------------
# Same question worktree.sh's detached-HEAD check answers for `gc` -- "does
# any ref outside this tree already contain HEAD" -- but asked of the
# upstream specifically, since the brief calls out UNPUSHED, not merely
# unmerged: a branch with a real PR open on `origin` is not what this guard
# exists to catch, but one whose tip only this worktree has a copy of is.
if ! HEAD_REV="$(git -C "$PANE_CWD" rev-parse HEAD 2>&1)"; then
  echo "lane-retire: ${TARGET}'s worktree ($PANE_CWD) has no readable HEAD -- refusing to retire" >&2
  echo "$HEAD_REV" >&2
  exit 1
fi
BRANCH="$(git -C "$PANE_CWD" rev-parse --abbrev-ref HEAD 2>/dev/null)"
UPSTREAM="$(git -C "$PANE_CWD" rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>/dev/null)"
if [ -n "$UPSTREAM" ]; then
  COMPARE_AGAINST="$UPSTREAM"
elif [ -n "$BRANCH" ] && [ "$BRANCH" != HEAD ] \
  && git -C "$PANE_CWD" show-ref --verify --quiet "refs/remotes/origin/$BRANCH"; then
  # No tracking branch recorded, but a same-named branch already exists on
  # origin -- compare against that rather than declare every commit unpushed.
  COMPARE_AGAINST="origin/$BRANCH"
else
  COMPARE_AGAINST=""
fi
if [ -n "$COMPARE_AGAINST" ]; then
  UNPUSHED="$(git -C "$PANE_CWD" rev-list --count "${COMPARE_AGAINST}..HEAD" 2>&1)"
  if ! [[ "$UNPUSHED" =~ ^[0-9]+$ ]]; then
    echo "lane-retire: could not compare ${TARGET}'s worktree against ${COMPARE_AGAINST} -- refusing to retire a lane whose push state is unreadable" >&2
    echo "$UNPUSHED" >&2
    exit 1
  fi
  if [ "$UNPUSHED" != 0 ]; then
    echo "lane-retire: ${TARGET}'s worktree ($PANE_CWD) has $UNPUSHED commit(s) not on ${COMPARE_AGAINST} -- refusing to retire" >&2
    git -C "$PANE_CWD" log --oneline "${COMPARE_AGAINST}..HEAD" 2>/dev/null | head -5 >&2
    exit 1
  fi
else
  # No upstream, and no same-named branch on origin either: HEAD is either on
  # a detached checkout or a local-only branch nobody has a copy of. Fail
  # closed exactly as worktree.sh's own detached-HEAD guard does -- an
  # unreadable push state is not a pushed one.
  echo "lane-retire: ${TARGET}'s worktree ($PANE_CWD) has no upstream and no matching branch on origin (HEAD $HEAD_REV) -- refusing to retire a lane whose commits nobody else has a copy of" >&2
  exit 1
fi

# --- 4. never the supervisor's own window ----------------------------------
# Same identity-based guard lane-done.sh applies before its own rename,
# reused verbatim rather than reimplemented (agent-supervisor#348/#239): a
# malformed target resolving to the supervisor's window must be refused on
# window IDENTITY, never on name.
CURRENT_IDX="$(tmux display-message -p -t "$TARGET" '#{window_index}' 2>/dev/null)"
if [ -z "$CURRENT_IDX" ]; then
  echo "lane-retire: could not read the current window index of ${TARGET} -- refusing to retire" >&2
  exit 1
fi
if SUPERVISOR_WID="$(supervisor_window_id "$SESSION" 2>/dev/null)" && [ -n "$SUPERVISOR_WID" ] \
  && [ "$(tmux display-message -p -t "$TARGET" '#{window_id}' 2>/dev/null)" = "$SUPERVISOR_WID" ]; then
  echo "lane-retire: ${TARGET} resolves to the supervisor's own window (id ${SUPERVISOR_WID}) -- refusing to retire it" >&2
  exit 1
elif [ -z "${SUPERVISOR_WID:-}" ] && [ "$CURRENT_IDX" = "${LANES_SUPERVISOR_WINDOW:-1}" ]; then
  echo "lane-retire: ${TARGET} resolves to the supervisor's own window (index ${CURRENT_IDX}, LANES_SUPERVISOR_WINDOW=${LANES_SUPERVISOR_WINDOW:-1}) -- refusing to retire it" >&2
  exit 1
fi

# --- 5. unregister the lane from the ledger --------------------------------
# Same call `lane-done.sh` makes on ordinary completion (agent-dotfiles#194):
# the ledger release is the authoritative operation, best effort and never
# fatal -- a lane the ledger never had an open task for (never dispatched,
# or already freed by hand) is not an error here, just nothing to release.
LANE_ID="${SESSION}:${CURRENT_IDX}"
LANE_RETIRE_CLI="$HERE/cli.py"
if ! LEDGER_OUT=$("${LANE_RETIRE_PYTHON:-python3}" "$LANE_RETIRE_CLI" \
    record-completion --lane "$LANE_ID" \
    --note "lane-retire: administrative retirement of ${TARGET}" 2>&1); then
  echo "lane-retire: no open task to release for $LANE_ID (already free, or never dispatched) -- continuing to restore the window" >&2
  sed 's/^/  /' <<<"$LEDGER_OUT" >&2
fi

# --- 6. rename back to free-N -- NEVER kill the window ----------------------
# Cosmetic, same as lane-done.sh's own rename-back: the ledger release above
# is what actually frees the lane. Re-read the index rather than trust
# $CURRENT_IDX from step 4 -- renumber-windows can move it between here and
# there, and the window is what this must still be naming correctly.
FREE_IDX="$(tmux display-message -p -t "$TARGET" '#{window_index}' 2>/dev/null)"
if [ -z "$FREE_IDX" ]; then
  echo "lane-retire: could not read the current window index of ${TARGET} -- the lane IS released in the ledger; the window was not renamed" >&2
elif ! tmux rename-window -t "$TARGET" "free-${FREE_IDX}"; then
  echo "lane-retire: rename-window FAILED for ${TARGET} -- the lane IS released in the ledger; the window keeps its prior name" >&2
fi

exit 0
