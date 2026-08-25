#!/bin/bash
# Delete local branches whose content is already on origin/main -- the
# hygiene step agent-supervisor#580 found could never fire.
#
# WHY THE OBVIOUS TEST DOESN'T WORK HERE. Every PR in these repos lands as a
# squash merge, so a squashed branch's commits never become ancestors of
# `main` and `git merge-base --is-ancestor` answers "not merged" for content
# that is fully merged. Measured on agent-supervisor before this change: 417
# local branches, 7 ancestors of origin/main (all worktree-held), 184
# belonging to PRs GitHub reports MERGED, 0 deleted -- the sweep reported "0
# deleted" every tick, which reads as "nothing to clean" when the real
# answer is "the test cannot see what it is looking for" (#580).
#
# `git cherry` (patch-id comparison) is not the fix either -- squashing
# rewrites patch-ids, so it has produced wrong numbers in this estate twice
# already: a "40 unique branches" that was really 11, and an "18 worktrees
# with unpushed work" that was really 0 (#580's own text).
#
# THE TEST THAT DOES WORK: compare the TREE a merge into origin/main would
# produce against origin/main's own tree, via `git merge-tree --write-tree`.
# A squash merge changes history but not content, so a squashed branch's
# tree, merged back into main, reproduces main's tree exactly. Verified
# against 47 real branches the morning #580 was filed: this correctly
# separated content already on main from content that was not, where
# --is-ancestor called all 47 unmerged.
#
# THE TRAP: on conflict, `merge-tree --write-tree` does NOT exit 0 with a
# bare tree oid -- it exits non-zero and prints a conflict report (which,
# depending on git version, may still start with a tree-shaped hex string,
# a merge tree carrying literal conflict markers). Comparing that output to
# main's tree oid as if it were a clean answer would call it "differs from
# main", which happens to be the safe direction here, but it is not a
# measurement -- it is an accident of what the conflict report looks like.
# This script checks the EXIT STATUS first and treats conflict as its own
# outcome, never folded into "unmerged".
#
# THE DELETION RULE (agent-supervisor#580's brief, verbatim):
#   never delete a branch that is dirty, ahead of origin/main, referenced by
#   a running process, or under 60 minutes old -- ALL FOUR must hold.
# This script folds "dirty" and "referenced by a running process" into one
# check: any branch checked out in a live worktree is skipped outright,
# unconditionally, and left to worktree.sh's own `gc` (which already owns
# worktree liveness and removal, agent-supervisor#478/#526) -- not
# reimplemented here. "Ahead of origin/main" is exactly what the tree test
# answers. "Under 60 minutes old" is checked against the branch's last
# commit time.
#
# NEVER DELETE A LOCAL-ONLY BRANCH. #580 found two branches with real,
# never-pushed commits and no remote ref, preserved by pushing them to
# `origin/backup/...` rather than deleting them. A branch with no
# `refs/remotes/origin/<name>` and no GitHub PR of any state recorded
# against it is reported and skipped, never deleted, regardless of what the
# tree test says about it -- an unpushed, unrecorded branch is exactly the
# one case where "looks merged" cannot be trusted, because nothing outside
# this one clone would remember it if it is wrong.
#
# Usage:
#   branch-sweep.sh [--dry-run] [--no-github] [repo] [base]
#
# [repo] defaults to the current directory; [base] defaults to origin/main.
# --dry-run reports what would be deleted and changes nothing.
# --no-github skips the GitHub PR cross-check (offline / test fixtures);
#   without it the sweep is tree-test-only, which is sufficient on its own
#   -- GitHub is a second opinion, not a requirement.
#
# Every branch this sweep looks at gets exactly one printed line saying
# what happened to it and why -- deleted, would-delete, or skipped-and-why.
# A silent "0 deleted" is the bug this replaces; this does not reintroduce
# it with a better test underneath.
set -uo pipefail

usage() { sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; exit 2; }

MIN_AGE_SECONDS="${BRANCH_SWEEP_MIN_AGE_SECONDS:-3600}"

_mtime_now() { date +%s; }

DRY=""
USE_GH=1
ARGS=()
for a in "$@"; do
  case "$a" in
    --dry-run) DRY=1 ;;
    --no-github) USE_GH="" ;;
    -h|--help) usage ;;
    *) ARGS+=("$a") ;;
  esac
done
REPO="${ARGS[0]:-$PWD}"
BASE="${ARGS[1]:-origin/main}"

mt=$(git -C "$REPO" rev-parse "$BASE^{tree}" 2>/dev/null) || {
  echo "branch-sweep: could not resolve $BASE's tree in $REPO -- refusing to run" >&2
  exit 1
}

# --- GitHub cross-check (optional second opinion) --------------------------
# Two sets: branches with a merged PR, and branches with a PR of ANY state
# (used for the local-only check below -- a branch with an open, closed-
# unmerged, or merged PR is recorded upstream even if its local ref outran
# or diverged from that PR).
GH_MERGED_FILE=""
GH_KNOWN_FILE=""
if [ -n "$USE_GH" ] && command -v gh >/dev/null 2>&1; then
  slug=$(git -C "$REPO" remote get-url origin 2>/dev/null \
         | sed -E 's#^(git@github\.com:|https://github\.com/)##; s#\.git$##')
  if [ -n "$slug" ] && gh repo view "$slug" >/dev/null 2>&1; then
    GH_MERGED_FILE=$(mktemp)
    GH_KNOWN_FILE=$(mktemp)
    if gh pr list --repo "$slug" --state all --limit 4000 \
         --json headRefName,state -q '.[] | [.headRefName,.state] | @tsv' \
         > "$GH_KNOWN_FILE.raw" 2>/dev/null; then
      awk -F'\t' '{print $1}' "$GH_KNOWN_FILE.raw" | sort -u > "$GH_KNOWN_FILE"
      awk -F'\t' '$2=="MERGED"{print $1}' "$GH_KNOWN_FILE.raw" | sort -u > "$GH_MERGED_FILE"
    else
      echo "branch-sweep: gh pr list failed against $slug -- proceeding tree-test-only, no GitHub cross-check this run" >&2
      rm -f "$GH_MERGED_FILE" "$GH_KNOWN_FILE"
      GH_MERGED_FILE=""; GH_KNOWN_FILE=""
    fi
    rm -f "$GH_KNOWN_FILE.raw"
  else
    echo "branch-sweep: no readable GitHub remote for $REPO -- proceeding tree-test-only, no GitHub cross-check this run" >&2
  fi
fi
cleanup() { [ -n "$GH_MERGED_FILE" ] && rm -f "$GH_MERGED_FILE"; [ -n "$GH_KNOWN_FILE" ] && rm -f "$GH_KNOWN_FILE"; }
trap cleanup EXIT

_gh_merged() { [ -n "$GH_MERGED_FILE" ] && grep -qxF "$1" "$GH_MERGED_FILE"; }
_gh_known()  { [ -n "$GH_KNOWN_FILE" ] && grep -qxF "$1" "$GH_KNOWN_FILE"; }

# --- worktree map: which branches are checked out anywhere -----------------
# Parallel indexed arrays, not `declare -A` -- macOS ships /bin/bash 3.2,
# which has no associative arrays (dispatch.sh#199, lanes.sh, harness-
# registry.sh all made the same call already; this follows the same one).
HELD_BRANCH=()
HELD_PATH=()
path="" branch=""
while IFS= read -r line; do
  case "$line" in
    worktree\ *) path="${line#worktree }"; branch="" ;;
    branch\ refs/heads/*)
      branch="${line#branch refs/heads/}"
      HELD_BRANCH+=("$branch")
      HELD_PATH+=("$path")
      ;;
    "") path=""; branch="" ;;
  esac
done < <(git -C "$REPO" worktree list --porcelain 2>/dev/null)

_held_path_for() {
  local want="$1" i
  for i in "${!HELD_BRANCH[@]}"; do
    [ "${HELD_BRANCH[$i]}" = "$want" ] && { printf '%s' "${HELD_PATH[$i]}"; return 0; }
  done
  return 1
}

CURRENT_BRANCH=$(git -C "$REPO" symbolic-ref -q --short HEAD 2>/dev/null || true)

deleted=0
skipped=0
SKIP_REASON_KEY=()
SKIP_REASON_TALLY=()

_skip() {
  local reason_key="$1" msg="$2" i found=0
  echo "branch-sweep: skip $msg" >&2
  skipped=$((skipped + 1))
  for i in "${!SKIP_REASON_KEY[@]}"; do
    if [ "${SKIP_REASON_KEY[$i]}" = "$reason_key" ]; then
      SKIP_REASON_TALLY[$i]=$(( SKIP_REASON_TALLY[$i] + 1 ))
      found=1
      break
    fi
  done
  if [ "$found" -eq 0 ]; then
    SKIP_REASON_KEY+=("$reason_key")
    SKIP_REASON_TALLY+=(1)
  fi
}

while IFS= read -r b; do
  [ -n "$b" ] || continue

  if [ -n "$CURRENT_BRANCH" ] && [ "$b" = "$CURRENT_BRANCH" ]; then
    _skip checked-out-here "$b -- checked out in $REPO itself"
    continue
  fi

  if held=$(_held_path_for "$b"); then
    _skip worktree-held "$b -- checked out in worktree $held (worktree.sh gc owns worktree-held branches, not this sweep)"
    continue
  fi

  # Local-only guard, before anything else: never delete a branch nothing
  # outside this clone would remember.
  has_origin_ref=0
  git -C "$REPO" show-ref --verify --quiet "refs/remotes/origin/$b" && has_origin_ref=1
  gh_known=0
  _gh_known "$b" && gh_known=1
  if [ "$has_origin_ref" -eq 0 ] && [ "$gh_known" -eq 0 ]; then
    _skip local-only "$b -- no refs/remotes/origin/$b and no GitHub PR (any state) recorded for it; local-only branches are never deleted, only reported (agent-supervisor#580)"
    continue
  fi

  # The tree test: does merging $b into $BASE reproduce $BASE's own tree?
  out=$(git -C "$REPO" merge-tree --write-tree "$BASE" "refs/heads/$b" 2>&1)
  rc=$?
  if [ "$rc" -ne 0 ]; then
    _skip conflict "$b -- would conflict merging into $BASE (git merge-tree exit $rc); conflict is its own outcome, not treated as unmerged"
    continue
  fi
  tree=$(printf '%s\n' "$out" | head -1)
  if [ "$tree" != "$mt" ]; then
    _skip not-merged "$b -- $BASE does not already contain its content (tree test)"
    continue
  fi

  # Second opinion, when available: GitHub's own merged-PR list. Disagree
  # -> report and leave alone rather than silently picking one method.
  if [ "$gh_known" -eq 1 ] && ! _gh_merged "$b"; then
    _skip gh-disagreement "$b -- tree test says merged into $BASE but GitHub's PR record for it is not MERGED; reported rather than silently resolved (agent-supervisor#580)"
    continue
  fi

  # Age: last-commit time, since this branch has no worktree to hold a
  # liveness signal of its own.
  ct=$(git -C "$REPO" log -1 --format=%ct "refs/heads/$b" 2>/dev/null)
  if [ -z "$ct" ]; then
    _skip unreadable-age "$b -- could not read its last-commit time; refusing to guess whether it is young"
    continue
  fi
  age=$(( $(_mtime_now) - ct ))
  if [ "$age" -lt "$MIN_AGE_SECONDS" ]; then
    _skip too-young "$b -- last commit ${age}s ago, younger than the ${MIN_AGE_SECONDS}s floor"
    continue
  fi

  if [ -n "$DRY" ]; then
    echo "branch-sweep: would delete $b -- content already on $BASE, no worktree, not local-only, ${age}s old" >&2
    deleted=$((deleted + 1))
    continue
  fi

  if git -C "$REPO" branch -D "$b" >&2; then
    echo "branch-sweep: deleted $b -- content already on $BASE, no worktree, not local-only, ${age}s old" >&2
    deleted=$((deleted + 1))
  else
    _skip delete-failed "$b -- git branch -D failed"
  fi
done < <(git -C "$REPO" for-each-ref refs/heads --format='%(refname:short)')

verb="deleted"; [ -n "$DRY" ] && verb="would delete"
echo "branch-sweep: done -- $verb $deleted, skipped $skipped" >&2
for i in "${!SKIP_REASON_KEY[@]}"; do
  echo "branch-sweep:   skipped (${SKIP_REASON_KEY[$i]}): ${SKIP_REASON_TALLY[$i]}" >&2
done
exit 0
