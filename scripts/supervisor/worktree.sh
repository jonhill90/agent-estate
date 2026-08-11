#!/bin/bash
# Give every lane task its own git worktree, so lanes and the Director stop
# sharing one working tree.
#
# WHY: agent-dotfiles#73. Every lane, the Director, and the supervisor
# operated in one checkout -- `~/source/repos/Personal/agent-dotfiles` -- with
# no coordination. Observed on 2026-08-11: a lane working #28 had its branch
# switched out from under it mid-task by another lane. Its uncommitted edits
# to four files were discarded, and its staged deletion of
# `.github/instructions/skill-authoring.instructions.md` was swept into an
# unrelated lane's commit (62393b1, shipped as PR #66 carrying a deletion its
# own message never mentions). That is the shared checkout destroying one
# agent's work and silently corrupting another's commit.
#
# The estate's own contract already says bounded issue work belongs in
# disposable worktrees. Writing that in loop-tick.md was not sufficient on its
# own -- the estate has already learned that a rule living in someone's memory
# gets skipped, which is why lanes.sh and claim.sh exist as tools rather than
# paragraphs. This is the same move: `new` hands a dispatch a ready worktree so
# there is nothing to remember, and `done`/`guard` refuse to discard anyone's
# uncommitted work, matching the safe-deletion contract -- a worktree with
# uncommitted changes is someone's unfinished work, not garbage.
#
# Usage:
#   worktree.sh new  <slug> [repo] [base]   create a worktree, print its path
#   worktree.sh done <path>                 remove a worktree; refuses if dirty
#   worktree.sh guard <repo>                exit 1 if <repo> itself is dirty
#
# [repo] defaults to the current directory; [base] defaults to origin/main.
# <slug> becomes branch `lane/<slug>` -- pass the issue and a short reason,
# e.g. `73-worktree-isolation`, so the branch name says what it is without
# opening a pane.
set -uo pipefail

usage() { sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; exit 2; }

CMD="${1:-}"
case "$CMD" in

new)
  SLUG="${2:-}"
  [ -n "$SLUG" ] || usage
  REPO="${3:-$PWD}"
  BASE="${4:-origin/main}"
  BRANCH="lane/$SLUG"
  ROOT="${WORKTREE_ROOT:-${TMPDIR:-/tmp}}"
  DEST="$ROOT/ad-$SLUG-$$"
  if [ -e "$DEST" ]; then
    echo "worktree: $DEST already exists" >&2
    exit 1
  fi
  # git worktree add already writes its progress to stderr; leave stdout
  # clean so a caller can capture exactly the path from the line below.
  git -C "$REPO" worktree add -b "$BRANCH" "$DEST" "$BASE" 1>&2 || exit 1
  echo "$DEST"
  exit 0 ;;

done)
  TARGET="${2:-}"
  [ -n "$TARGET" ] || usage
  [ -d "$TARGET" ] || { echo "worktree: $TARGET does not exist" >&2; exit 1; }
  # A worktree with uncommitted changes is someone's unfinished work, not
  # garbage -- refuse rather than guess, same as safe-deletion.
  status=$(git -C "$TARGET" status --porcelain 2>&1)
  if [ -n "$status" ]; then
    echo "worktree: $TARGET has uncommitted changes -- not removing" >&2
    echo "$status" >&2
    exit 1
  fi
  git -C "$TARGET" worktree remove "$TARGET" >&2 || exit 1
  exit 0 ;;

guard)
  REPO="${2:-$PWD}"
  # The Director branching on top of a lane's half-finished edits is the bug
  # this whole tool exists to prevent -- for the Director's own use of the
  # shared checkout, not just lane dispatch. Call this before doing anything
  # in the shared checkout that assumes it is clean.
  status=$(git -C "$REPO" status --porcelain 2>&1)
  if [ -n "$status" ]; then
    echo "worktree: $REPO has uncommitted changes -- that is someone's live work, not a base to branch on. Use worktree.sh new instead of working in the shared checkout." >&2
    echo "$status" >&2
    exit 1
  fi
  exit 0 ;;

*) usage ;;
esac
