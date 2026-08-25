#!/bin/bash
# Tick-time drift detector for the loop's own tooling execution surface.
# agent-supervisor#654, Part 1 ("C" of the decision comment).
#
# WHAT THIS MEASURES, AND WHY IT EXISTS ON ITS OWN, NOT FOLDED INTO
# advance-live.sh. #654 measured a merged fix (dispatch.sh, #650) that was
# inert against the running loop for hours -- CI green, tests passing from a
# clean clone, and the process actually executing it still ran the pre-fix
# copy, because the copy is a working tree's state, and a merge changes
# nothing about a working tree nobody has advanced. The only way anyone
# noticed was a by-hand diff against origin/main. This script is that diff,
# run automatically instead of by hand -- it does not advance anything, does
# not fix anything, and never mutates the directory it inspects. It exists so
# "is a merged fix live here" has an answer that costs one command instead of
# a manual `git diff` per file, per merge.
#
# THIS DOES NOT REPLACE advance-live.sh (agent-supervisor#654's "B", the
# dedicated-clone fix). It is the cheap, immediate half, useful before B
# ships as the only visibility into the gap, and useful after B ships as a
# regression guard confirming the dedicated clone itself never quietly falls
# behind either -- run it against $SUPERVISOR_LIVE the same way advance-live.sh
# is, and a report of anything but "in sync" for every file means something
# is wrong with the update step, not merely with a human's checkout.
#
# PER-FILE, NOT PER-DIRECTORY, DELIBERATELY. A working tree is at one commit,
# but that commit being "N behind origin/main" says nothing about whether any
# GIVEN file actually changed in those N commits -- #654's own measurement
# found dispatch.sh/cli.py/core.py/itemize_prompts.py drifted while
# merge-pr.sh/collision-check.sh/branch-sweep.sh/verdict.py, present in the
# same stale checkout, did not, because no commit in the gap touched them.
# Reporting per file is what lets a reader tell "this merged fix is inert"
# from "this merged fix does not apply here, and correctly so".
#
# THREE STATES, NOT TWO. "differs" alone conflates two very different causes:
#   in sync           -- the file's committed content at HEAD in $DIR matches
#                         its content at the remote ref. Nothing to act on.
#   behind by N commits -- content differs, and $DIR is missing N commits on
#                         the remote ref that touched this exact path. The
#                         normal, expected shape of a stale-but-honest
#                         checkout: advance it (advance-live.sh, or a human
#                         pull for an interactive one) and the gap closes.
#   diverged          -- content differs, and the remote ref has NO commit
#                         touching this path that $DIR is missing. $DIR's own
#                         copy carries content the remote does not -- an
#                         uncommitted local edit, or a local commit nothing
#                         upstream has. For a directory nothing but an update
#                         step is supposed to touch (the dedicated clone this
#                         issue's Part 2 builds), this is diagnostic on its
#                         own: something wrote to a clone that should have
#                         exactly one writer.
#
# WHAT IT DOES NOT DO: fetch is opt-out, never silent. Comparing against a
# stale local `origin/main` ref would read as "in sync" when it is merely
# "in sync with what this script itself never bothered to refresh" -- the
# exact silent-staleness shape advance-live.sh's own header warns against
# (agent-supervisor#11). Set TOOLING_DRIFT_NO_FETCH=1 only for a directory
# whose remote-tracking ref has already been refreshed by the caller (tests
# use this to compare against a fixture without a network fetch).
#
# Usage:
#   tooling-drift.sh [dir] [file...]
#
# dir defaults to $SUPERVISOR_LIVE, or $SUPERVISOR_STATE/live, or
# ~/.local/state/agent-dotfiles-supervisor/live -- the same default chain
# advance-live.sh uses, so running this with no arguments answers "is the
# loop's own pinned clone current" without the caller having to know the path.
#
# file... defaults to every file `git ls-files` tracks directly under
# scripts/supervisor in $dir (not its subdirectories -- laneview/ and
# harness/ are UI and per-harness code, not the dispatch/merge path this
# issue is about). That default is deliberately broader than the specific
# list #654's own body names (dispatch.sh, merge-pr.sh, cli.py, core.py,
# collision-check.sh, branch-sweep.sh, verdict.py, itemize_prompts.py) --
# that list is itself a citation of what #654 happened to measure, not a
# closed set (its own text: "read the loop's own invocation points to find
# the real list, don't assume this one is complete"). Checking every
# top-level tracked file costs nothing extra and removes the risk of a
# curated list going stale the next time a new script joins the dispatch
# path. Pass an explicit file list to narrow it.
#
# Env:
#   SUPERVISOR_LIVE / SUPERVISOR_STATE   same meaning as advance-live.sh
#   TOOLING_DRIFT_REMOTE_REF             default origin/main
#   TOOLING_DRIFT_NO_FETCH               skip the fetch (see above)
#
# Exit code: 0 if every file reports "in sync", 1 if anything drifted or
# diverged, 2 for a usage/setup failure (not a git repo, fetch failed, etc).
set -uo pipefail

STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
DIR="${1:-${SUPERVISOR_LIVE:-$STATE/live}}"
[ $# -gt 0 ] && shift
REMOTE_REF="${TOOLING_DRIFT_REMOTE_REF:-origin/main}"

if ! git -C "$DIR" rev-parse --git-dir >/dev/null 2>&1; then
  echo "tooling-drift: not a git repository: $DIR" >&2
  exit 2
fi

if [ -z "${TOOLING_DRIFT_NO_FETCH:-}" ]; then
  if ! git -C "$DIR" fetch origin >/dev/null 2>&1; then
    echo "tooling-drift: could not fetch origin in $DIR -- refusing to report against a possibly-stale remote ref (set TOOLING_DRIFT_NO_FETCH=1 to compare against whatever is already there)" >&2
    exit 2
  fi
fi

if ! git -C "$DIR" rev-parse --verify "$REMOTE_REF" >/dev/null 2>&1; then
  echo "tooling-drift: $REMOTE_REF is not resolvable in $DIR" >&2
  exit 2
fi

FILES=("$@")
if [ ${#FILES[@]} -eq 0 ]; then
  while IFS= read -r f; do
    FILES+=("$f")
  done < <(git -C "$DIR" ls-files -- 'scripts/supervisor/*.sh' 'scripts/supervisor/*.py' \
            | awk -F/ 'NF==3' | sort)
fi

if [ ${#FILES[@]} -eq 0 ]; then
  echo "tooling-drift: no files to check in $DIR (nothing tracked directly under scripts/supervisor, or an empty explicit list)" >&2
  exit 2
fi

status=0
printf '%-42s %-24s\n' "FILE" "STATE"
for f in "${FILES[@]}"; do
  local_blob=$(git -C "$DIR" rev-parse -q --verify "HEAD:$f" 2>/dev/null || true)
  remote_blob=$(git -C "$DIR" rev-parse -q --verify "$REMOTE_REF:$f" 2>/dev/null || true)

  if [ -z "$remote_blob" ]; then
    printf '%-42s %-24s\n' "$f" "NOT-ON-REMOTE"
    status=1
    continue
  fi
  if [ -z "$local_blob" ]; then
    printf '%-42s %-24s\n' "$f" "MISSING-LOCALLY"
    status=1
    continue
  fi

  # A tracked file's working-tree copy can differ from HEAD's committed blob
  # even though this directory is meant to have exactly one writer -- that is
  # itself worth surfacing, not silently overridden by comparing the
  # committed blob alone. Checked independently of the HEAD-vs-remote
  # comparison below: a dirty file can be either in sync or behind at HEAD.
  wt_blob=$(git -C "$DIR" hash-object "$DIR/$f" 2>/dev/null || true)
  dirty_suffix=""
  if [ -n "$wt_blob" ] && [ "$wt_blob" != "$local_blob" ]; then
    dirty_suffix=" (uncommitted local edit)"
  fi

  if [ "$local_blob" = "$remote_blob" ]; then
    if [ -n "$dirty_suffix" ]; then
      printf '%-42s %-24s\n' "$f" "diverged${dirty_suffix}"
      status=1
    else
      printf '%-42s %-24s\n' "$f" "in sync"
    fi
    continue
  fi

  behind=$(git -C "$DIR" rev-list --count "HEAD..$REMOTE_REF" -- "$f" 2>/dev/null || echo "")
  case "$behind" in
    ''|*[!0-9]*)
      printf '%-42s %-24s\n' "$f" "UNKNOWN (could not count)"
      status=1
      ;;
    0)
      printf '%-42s %-24s\n' "$f" "diverged${dirty_suffix}"
      status=1
      ;;
    *)
      printf '%-42s %-24s\n' "$f" "behind by $behind commit(s)${dirty_suffix}"
      status=1
      ;;
  esac
done

exit $status
