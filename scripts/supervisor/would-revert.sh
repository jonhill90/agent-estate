#!/bin/bash
# Answer "would merging this branch revert X" by actually merging it,
# instead of by reading a diff.
#
# WHY: agent-dotfiles#114. `loop-tick.md` already explains, at length, that
# `git diff main..branch` renders every commit the branch lacks as a
# deletion and that this is not a prediction about merging -- #107 wrote
# that section from scratch, with a reproduction. It held for about an
# hour: a lane read the two-dot diff on PR #111, reported a revert of
# #104, and held the PR. Refuted by merging into a scratch worktree --
# nothing was actually deleted. Two false holds in two hours, from the
# same reading, with the correction already merged and in the file the
# supervisor is told to follow.
#
# A rule written into a ~500-line document only works if the reader
# happens to read that section before they need it. A lane dispatched to
# review a PR is briefed on the PR, not on the tick file, and has no
# reason to consult a merge-mechanics section it does not know exists.
# This is the same move as `worktree.sh` and `claim.sh`: a paragraph a
# reader might skip becomes a command a reviewer can run.
#
# WHAT IT DOES: merges <branch-or-ref> into a scratch worktree at [base],
# built with `worktree.sh new` so cleanup follows the same contract every
# other lane worktree does, and reports three things distinctly --
#   DELETES    files the merge removes that [base] has -- the actual
#              question being asked. Computed even when the merge also
#              CONFLICTS (agent-dotfiles#119): a file can be conflict-free
#              and cleanly, silently deleted in the SAME merge that
#              conflicts on a different file, and that deletion is the
#              dangerous half -- reporting only the conflict and saying
#              "not a deletion either" would be confidently wrong.
#   CONFLICTS  files the merge cannot resolve on its own -- the other real
#              cause of "this looks like a revert" and NOT the same thing
#              as a deletion.
#   also touches   everything else the merge adds or modifies.
# The scratch worktree AND the scratch branch `worktree.sh new` creates for
# it are removed on every exit path, including a failed merge, a conflict,
# or this script being killed mid-run (agent-dotfiles#119) -- a conflicted
# merge leaves the tree dirty, and `worktree.sh done` refuses to remove
# anything dirty on purpose (the safe-deletion contract), so a conflict is
# aborted first; an interrupted `worktree.sh new` can leave the worktree
# administratively `locked`, which `worktree.sh done` also refuses, so
# cleanup unlocks and force-removes rather than trusting the normal path.
# `worktree.sh done` only ever removes the WORKTREE, never the BRANCH it
# was created on -- that is correct for a lane's branch, which has to
# outlive its worktree to become a PR, but this script's branch is scratch
# with nothing worth keeping, so cleanup deletes it directly. `trap ... EXIT`
# alone does not fire reliably when a signal lands while bash is blocked in
# a command substitution (observed live: SIGTERM during `worktree.sh new`
# left the process dead with no trap run at all) -- INT and TERM are
# trapped explicitly, and cleanup is idempotent so all three trap paths can
# call it without double-running.
#
# WHAT IT NEVER DOES: touch the working tree, index, or current branch of
# the repo it is run from. It never `git checkout`s, `git merge`s, or
# writes into the working tree of that repo -- `worktree.sh new` only adds
# a linked worktree and its branch, both removed by this script's own
# cleanup, so nothing outlives a run. A tool for checking a merge that
# mutates the caller's checkout would be worse than the diff it replaces.
#
# Usage:
#   would-revert.sh <branch-or-ref> [base]
#
# [base] defaults to origin/main. Run from the repo to check; set
# WOULD_REVERT_REPO to point at a different one.
#
# Exit 0    the merge deletes nothing (clean, usable as a check).
# Exit 1    the merge deletes files [base] has.
# Exit 2    the merge conflicts, or the merge/worktree could not be built.
# Exit 130  interrupted (SIGINT).
# Exit 143  interrupted (SIGTERM).
set -uo pipefail

usage() { sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; exit 2; }

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

REF="${1:-}"
[ -n "$REF" ] || usage
BASE="${2:-origin/main}"
REPO="${WOULD_REVERT_REPO:-$PWD}"

git -C "$REPO" rev-parse --verify --quiet "$REF" >/dev/null \
  || { echo "would-revert: '$REF' is not a ref in $REPO" >&2; exit 2; }
git -C "$REPO" rev-parse --verify --quiet "$BASE" >/dev/null \
  || { echo "would-revert: '$BASE' is not a ref in $REPO" >&2; exit 2; }

# Named before the worktree is built, and matching `worktree.sh new`'s own
# `lane/$SLUG` convention exactly, so cleanup can find and remove this
# script's branch even if it dies before WORKTREE is ever set.
SLUG="would-revert-$$"
BRANCH="lane/$SLUG"
WORKTREE=""
CLEANED=""

cleanup() {
  # Runs from EXIT and from the INT/TERM handlers below; both paths must
  # not run this twice.
  [ -z "$CLEANED" ] || return 0
  CLEANED=1
  # A SIGTERM/SIGINT bash was already blocked on when it arrived is not
  # acted on until the current foreground command returns (observed live:
  # sending it once during the merge step did not fire the trap until
  # THIS function's own `worktree.sh done` call, well after the CLEANED
  # guard above was already set -- the deferred, re-delivered signal then
  # re-entered the INT/TERM trap, hit the guard, and its `exit N` killed
  # the process while the real removal below was still in flight,
  # abandoning it mid-`git worktree remove` and leaving the worktree
  # locked). Ignore both from here on so a re-delivered signal cannot cut
  # this removal short; the guard above already makes cleanup itself
  # idempotent, this stops a signal from racing it.
  trap '' INT TERM
  if [ -n "$WORKTREE" ]; then
    # A conflicted or half-finished merge leaves the scratch tree dirty;
    # `worktree.sh done` refuses to remove anything dirty, matching
    # safe-deletion. Abort first -- there is nothing here worth keeping.
    git -C "$WORKTREE" merge --abort >/dev/null 2>&1 || true
    # A run killed mid-`worktree.sh new` can leave the worktree
    # administratively `locked` (git's own state, not an OS lock) --
    # `worktree.sh done` refuses a locked tree the same as a dirty one, so
    # unlock first and fall back to a forced removal that handles both
    # dirty and locked in one call.
    git -C "$REPO" worktree unlock "$WORKTREE" >/dev/null 2>&1 || true
    "$HERE/worktree.sh" done "$WORKTREE" >/dev/null 2>&1 \
      || git -C "$REPO" worktree remove --force "$WORKTREE" >/dev/null 2>&1 \
      || echo "would-revert: could not remove scratch worktree $WORKTREE -- remove it by hand" >&2
  fi
  # `worktree.sh new` creates $BRANCH along with the worktree; `worktree.sh
  # done` (and the forced fallback above) only ever remove the WORKTREE.
  # Left alone, every run -- successful or not -- leaves one more local
  # branch behind forever (agent-dotfiles#119, #120). Only delete it if it
  # exists: a run that failed before the worktree was built never created it.
  if git -C "$REPO" show-ref --verify --quiet "refs/heads/$BRANCH"; then
    git -C "$REPO" branch -D "$BRANCH" >/dev/null 2>&1 \
      || echo "would-revert: could not remove scratch branch $BRANCH -- remove it by hand" >&2
  fi
}
trap cleanup EXIT
trap 'cleanup; exit 130' INT
trap 'cleanup; exit 143' TERM

WORKTREE_ERR=$(mktemp)
WORKTREE=$("$HERE/worktree.sh" new "$SLUG" "$REPO" "$BASE" 2>"$WORKTREE_ERR")
rc=$?
if [ "$rc" -ne 0 ] || [ -z "$WORKTREE" ] || [ ! -d "$WORKTREE" ]; then
  echo "would-revert: could not build a scratch worktree at $BASE" >&2
  sed 's/^/  /' "$WORKTREE_ERR" >&2
  rm -f "$WORKTREE_ERR"
  WORKTREE=""
  exit 2
fi
rm -f "$WORKTREE_ERR"

# Captured before the merge, not re-derived from $BASE afterwards: $BASE can
# be a moving ref like origin/main, and the diff must be against the exact
# commit this worktree started from.
START_SHA=$(git -C "$WORKTREE" rev-parse HEAD)

MERGE_OUT=$(git -C "$WORKTREE" \
  -c user.name="would-revert.sh" -c user.email="would-revert@localhost" \
  -c commit.gpgsign=false \
  merge --no-edit "$REF" 2>&1)
merge_rc=$?

if [ "$merge_rc" -ne 0 ]; then
  conflicts=$(git -C "$WORKTREE" diff --name-only --diff-filter=U)
  if [ -n "$conflicts" ]; then
    # A conflict on one file does not stop git from cleanly and silently
    # resolving a DELETION on a different file in the SAME merge attempt
    # (agent-dotfiles#119): main never touching a file the branch removes
    # is not a conflict, it auto-applies. Checked here too, not only on
    # the clean-merge path below -- "CONFLICTS" must never be read as "and
    # therefore nothing was deleted".
    deletes=$(git -C "$WORKTREE" diff --name-status --diff-filter=D "$START_SHA" | awk '{print $2}')
    echo "would-revert: merging $REF into $BASE CONFLICTS:"
    sed 's/^/  /' <<<"$conflicts"
    if [ -n "$deletes" ]; then
      echo "would-revert: AND merging $REF into $BASE DELETES:"
      sed 's/^/  /' <<<"$deletes"
    fi
    exit 2
  fi
  echo "would-revert: merge of $REF into $BASE failed:" >&2
  echo "$MERGE_OUT" >&2
  exit 2
fi

STATUS=$(git -C "$WORKTREE" diff --name-status "$START_SHA" HEAD)
DELETES=$(awk '$1=="D"{print $2}' <<<"$STATUS")
OTHERS=$(awk '$1!="D"{ $1=""; sub(/^ /,""); print }' <<<"$STATUS")

if [ -n "$DELETES" ]; then
  echo "would-revert: merging $REF into $BASE DELETES:"
  sed 's/^/  /' <<<"$DELETES"
  [ -z "$OTHERS" ] || { echo "would-revert: also adds/modifies:"; sed 's/^/  /' <<<"$OTHERS"; }
  exit 1
fi

echo "would-revert: merging $REF into $BASE deletes nothing"
[ -z "$OTHERS" ] || { echo "would-revert: adds/modifies:"; sed 's/^/  /' <<<"$OTHERS"; }
exit 0
