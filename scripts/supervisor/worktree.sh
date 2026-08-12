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
#   worktree.sh gc [--dry-run] [repo] [base]
#                                           remove every worktree whose
#                                            branch's content is already on
#                                            [base] and whose tree is clean;
#                                            --dry-run reports what it would
#                                            remove and changes nothing
#
# [repo] defaults to the current directory; [base] defaults to origin/main.
# <slug> becomes branch `lane/<slug>` -- pass the issue and a short reason,
# e.g. `73-worktree-isolation`, so the branch name says what it is without
# opening a pane.
#
# WHY `gc` exists and what it deliberately does not do (agent-dotfiles#165):
# `new` has run on every dispatch since #81 and nothing ever calls `done` --
# `lane-done.sh` renames the tmux window and closes the ledger task but never
# touches the worktree it was given. 92 registered worktrees held 66 branches
# the night this was measured, and a held branch cannot be deleted: `gh pr
# merge --delete-branch` failed twice with "used by worktree at ...".
#
# The obvious fix, "lane-done.sh calls done", is wrong for two reasons a
# completion event cannot see. First, a finished lane's branch is normally
# pushed and in review -- a reviewer or a follow-up fix may still want that
# tree, so removing it at completion time is early, not automatic garbage.
# Second, `done` correctly refuses a dirty tree, so a lane that finished with
# uncommitted work would silently fail to clean and the leak would persist
# exactly where losing it matters, with nobody told.
#
# `gc` is a sweep, not a completion hook: it removes a worktree only once
# both are true -- [base] already contains the branch's content (see
# `branch_content_is_on_base` below; this was tip-ancestry until #169, which
# a squash merge never satisfies) and its tree is clean (the same guard
# `done` already applies, reused rather than reimplemented). Anything
# unmerged or dirty is left alone and reported, not retried or forced.
#
# What `gc` does NOT reach, measured on the live checkout 2026-08-11 and
# stated here so the next reader does not re-derive it: of 44 non-ancestor
# worktree-held branches, 24 have a MERGED PR, and the content predicate
# reaches 7 of those. The other 17 are merged *and then superseded* -- later
# commits on `main` edited the same files, so the scoped diff is non-empty
# and the branch's tip content no longer matches `main`'s. Nothing local can
# tell that apart from unmerged work; only the PR state can (#169 sketches
# a `gh pr view` path, deliberately not taken here because it must fail
# closed offline and that is a separate decision). Converging is not
# emptying: `gc` reaching 38 of 70 held trees and refusing the rest is the
# intended shape, not a shortfall to fix by loosening the predicate.
#
# `gc` removes worktrees, never branches. A wrong "already merged" verdict
# would free a tree, not delete a ref -- but it would still discard whatever
# was only in that tree, so every check above fails closed. This makes `gc` idempotent and safe to run repeatedly
# from wherever it ends up wired in -- deliberately NOT wired into the
# dispatch/lane-done pair or the Director tick by this change. This estate
# has shipped that exact shape wrong five times already (`acp_transport.py`,
# `claim.sh` before #74, `worktree.sh` itself before #81, and two more --
# see dispatch.sh's header): a tool that fails closed when called, that
# nothing calls. Wiring `gc` in is a separate decision for whoever owns the
# Director tick, not bundled into landing the tool.
set -uo pipefail

usage() { sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; exit 2; }

# Shared by `done` and `gc`: remove TARGET, refusing anything that would
# discard work. Never call this on a target that has not already been
# checked for uncommitted changes belonging to work in progress -- `gc`
# checks "merged" separately, before this runs.
safe_remove() {
  local target="$1"
  local dry="${2:-}"
  # A worktree with uncommitted changes is someone's unfinished work, not
  # garbage -- refuse rather than guess, same as safe-deletion.
  local status
  status=$(git -C "$target" status --porcelain 2>&1)
  if [ -n "$status" ]; then
    echo "worktree: $target has uncommitted changes -- not removing" >&2
    echo "$status" >&2
    return 1
  fi
  # A clean `git status` says nothing about a detached HEAD: a lane can
  # `checkout --detach` and commit there, and the dirty-tree check above
  # never sees it. Removing the worktree at that point makes the commit
  # unreachable from any ref -- a dangling object, invisible and eventually
  # GC-eligible. Refuse unless some branch, local or remote, already
  # contains HEAD.
  if ! git -C "$target" symbolic-ref -q HEAD >/dev/null; then
    local containing
    containing=$(git -C "$target" for-each-ref refs/heads refs/remotes --contains HEAD --format='%(refname)' 2>/dev/null)
    if [ -z "$containing" ]; then
      echo "worktree: $target is on a detached HEAD at $(git -C "$target" rev-parse --short HEAD 2>/dev/null) with no branch containing it -- not removing (would lose the commit)" >&2
      return 1
    fi
  fi
  # --dry-run runs every refusal above and stops here: the answer a real run
  # would give, without the removal.
  [ -n "$dry" ] && return 0
  git -C "$target" worktree remove "$target" >&2 || return 1
  return 0
}

# Does <base> already contain branch <b>'s work? (agent-dotfiles#169)
#
# NOT ancestry. This repo squash-merges, and a squash merge writes one new
# commit on <base> with no parent link to the branch, so a fully-merged
# branch is never an ancestor of <base>. `gc`'s original predicate,
# `merge-base --is-ancestor`, therefore said "unmerged" about 24 of the 44
# non-ancestor worktree-held branches measured on 2026-08-11 -- permanently,
# and one more with every squash merge.
#
# The formulation is loop-tick.md's ("Which diff answers which question"),
# not a third invention: two-dot scoped to the paths the branch touched.
# Neither plain form works -- `base...b` still lists squashed work as
# outstanding, and `base..b` reports <base>'s own newer files as the
# branch's deletions once <base> drifts.
#
# Two traps, both of which fail in the "already merged" direction, which is
# the direction that deletes someone's work:
#   - The pathspec must be piped straight from `--name-only -z` into
#     `xargs -0`. Held in a variable first, command substitution strips the
#     NUL bytes and the whole list collapses into one nonexistent path that
#     matches nothing -- an empty diff, read as "merged". That is not
#     hypothetical: it happened while measuring this change, and inflated
#     the count from 12 to 41 before it was caught.
#   - Empty input to xargs is not portable: GNU runs the command once with
#     no pathspec (whole-tree diff), BSD/macOS does not run it at all (empty
#     output, read as "merged"). Decided here instead, before xargs sees it.
branch_content_is_on_base() {
  local repo="$1" b="$2" base="$3"
  # Ancestry still answers yes cheaply when it survives (a rebase-merged or
  # fast-forwarded branch); it is only insufficient, never wrong.
  git -C "$repo" merge-base --is-ancestor "refs/heads/$b" "$base" 2>/dev/null && return 0
  local mb
  mb=$(git -C "$repo" merge-base "$base" "refs/heads/$b" 2>/dev/null) || return 1
  [ -n "$mb" ] || return 1
  # No paths at all: the branch's tree is identical to the merge base, so it
  # carries commits with no net content. Fail closed rather than reason about
  # what an empty pathspec means to xargs.
  git -C "$repo" diff --quiet "$mb" "refs/heads/$b" 2>/dev/null
  case $? in
    0) return 1 ;;   # tree-identical to the merge base
    1) ;;            # differences exist -- the expected path
    *) return 1 ;;   # git failed; fail closed
  esac
  local out rc
  out=$(git -C "$repo" diff --name-only -z "$mb" "refs/heads/$b" \
        | xargs -0 git -C "$repo" diff --stat "$base..refs/heads/$b" -- 2>&1); rc=$?
  # Empty output *and* a clean exit. Anything git could not answer is not an
  # answer of "merged".
  [ "$rc" -eq 0 ] && [ -z "$out" ]
}

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
  safe_remove "$TARGET" || exit 1
  exit 0 ;;

gc)
  shift
  DRY=""
  if [ "${1:-}" = "--dry-run" ]; then DRY=1; shift; fi
  REPO="${1:-$PWD}"
  BASE="${2:-origin/main}"
  # The worktree gc is running from is nobody's garbage -- it is the caller's
  # own tree. `git worktree remove` refuses it anyway; refusing here says so
  # in gc's own words rather than as a git error.
  SELF=$(git -C "$PWD" rev-parse --show-toplevel 2>/dev/null || true)
  # Parse `worktree list --porcelain`: records are blank-line separated,
  # each starting with `worktree <path>`, optionally followed by a
  # `branch refs/heads/<name>` line (absent for detached/bare entries).
  # The first record is always the main worktree (REPO itself) -- never a
  # gc candidate, so it is dropped before the loop below.
  path="" branch="" first=1
  paths=() branches=()
  while IFS= read -r line; do
    case "$line" in
      worktree\ *) path="${line#worktree }"; branch="" ;;
      branch\ refs/heads/*) branch="${line#branch refs/heads/}" ;;
      "")
        if [ -n "$path" ]; then
          if [ "$first" -eq 1 ]; then
            first=0
          else
            paths+=("$path")
            branches+=("$branch")
          fi
        fi
        path="" ;;
    esac
  done < <(git -C "$REPO" worktree list --porcelain)
  if [ -n "$path" ] && [ "$first" -eq 0 ]; then
    paths+=("$path")
    branches+=("$branch")
  fi

  removed=0 skipped=0
  for i in "${!paths[@]}"; do
    p="${paths[$i]}"
    b="${branches[$i]}"
    if [ -z "$b" ]; then
      echo "worktree: gc skipping $p -- no branch (detached or bare)" >&2
      skipped=$((skipped + 1))
      continue
    fi
    if [ ! -d "$p" ]; then
      echo "worktree: gc skipping $p -- registered but missing on disk (run 'git worktree prune')" >&2
      skipped=$((skipped + 1))
      continue
    fi
    if [ -n "$SELF" ] && [ "$p" = "$SELF" ]; then
      echo "worktree: gc skipping $p -- this is the worktree gc is running in" >&2
      skipped=$((skipped + 1))
      continue
    fi
    if ! branch_content_is_on_base "$REPO" "$b" "$BASE"; then
      echo "worktree: gc skipping $p -- $BASE does not already contain branch '$b'" >&2
      skipped=$((skipped + 1))
      continue
    fi
    if safe_remove "$p" "$DRY"; then
      if [ -n "$DRY" ]; then
        echo "worktree: gc would remove $p (branch '$b' -- its content is already on $BASE)" >&2
      else
        echo "worktree: gc removed $p (branch '$b' -- its content is already on $BASE)" >&2
      fi
      removed=$((removed + 1))
    else
      skipped=$((skipped + 1))
    fi
  done
  if [ -n "$DRY" ]; then
    echo "worktree: gc dry run done -- would remove $removed, skipped $skipped (nothing changed)" >&2
  else
    echo "worktree: gc done -- removed $removed, skipped $skipped" >&2
  fi
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
