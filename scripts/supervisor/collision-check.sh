#!/bin/bash
# Pre-dispatch collision check: refuse a dispatch whose files overlap an
# in-flight lane's files -- agent-supervisor#291.
#
# THE FAILURE THIS PREVENTS, MEASURED: as#263 and as#266, two lanes that
# independently wrote the same fix to the same file
# (scripts/supervisor/quota-watch.sh), one at 10 files/+689 lines, the other
# at 3 files/+243. One of those PRs was entirely wasted work, plus a review,
# plus a merge decision -- the second measured instance of as#181 (two
# dispatchers, one ledger).
#
# WHAT "OVERLAP" MEANS HERE, AND WHY: whole-file overlap, nothing finer.
# Line-range or hunk-level overlap would be more precise but requires
# predicting where in a file an unwritten change will land, which is
# guesswork this estate has already rejected once (a maintained dependency
# graph, killed by this same issue's own devils-advocate pass: stale edges,
# false confidence). File-level overlap is the cheapest signal that is still
# an honest measurement rather than a prediction -- "will lane B touch the
# same FILE lane A is already touching", not "will they touch the same
# LINE". False positives are possible (two lanes editing the same file in
# genuinely disjoint ways) but rare enough, and `--force` exists for the
# known-intended case; false negatives (same logical fix landing in two
# differently-named files) are not caught and are not this issue's scope.
#
# WHAT COUNTS AS "IN FLIGHT": every task the ledger has NOT recorded as
# complete, failed, or cancelled AND that has a recorded worktree path
# (`Ledger.list_open_worktrees`, agent-supervisor#117's column). A lane
# between claim and delivery counts equally with one mid-brief -- both can
# still collide with a fresh dispatch.
#
# WHAT COUNTS AS A CANDIDATE'S FILES ("files N is likely to touch"), in the
# cheapest-to-costliest order the issue itself specifies:
#   1. Files named in the issue body or the brief -- backtick-quoted paths
#      (`` `scripts/supervisor/quota-watch.sh` ``, this estate's own
#      convention for citing a file, confirmed by grepping this repo's own
#      briefs) that resolve to a real path via `git ls-files`, matched
#      either exactly or by unambiguous basename.
#   2. Files already touched by the candidate's own branch, if one already
#      existed before this dispatch (a resumed or re-dispatched issue) --
#      a fresh `worktree.sh new` branch is identical to its base and
#      contributes nothing here; this only fires when there is prior
#      content.
#   3. Files touched by the PR this dispatch is scoped to, if any
#      (`--pr`/`--reviews-pr` review or fix-pass work) -- `gh pr diff
#      --name-only`, the most precise signal available because the diff
#      already exists.
# The union of whatever those three produce is the candidate's file set.
#
# WHAT COUNTS AS AN IN-FLIGHT LANE'S FILES: its own worktree's actual diff --
# committed-since-merge-base plus uncommitted -- never a guess from that
# lane's brief text. This is `git diff`, not prediction: the work already
# exists on disk.
#
# agent-supervisor#291 (from the issue itself): "If the file set cannot be
# determined: say UNKNOWN, ALLOW the dispatch, and log it." Most issues will
# not name files, so refusing on unknown would refuse nearly everything --
# the one place this file's usual fail-closed posture inverts, DELIBERATELY,
# and only for the CANDIDATE side. An in-flight lane whose worktree cannot be
# read (removed, gc'd, unreadable) is simply skipped for that one lane, the
# same "best effort, do not let one bad row block every other check" posture
# `dispatch.sh` already takes with its own reap step -- it does not turn the
# whole check unknown, because every OTHER in-flight lane is still checkable.
#
# Usage:
#   collision-check.sh check --issue <n> --brief <path> --worktree <path>
#                             --repo-path <path> [--repo <owner/name>]
#                             [--pr <n>] [--force] [--exclude-lane <lane>]
#
# Exit 0  -- allow. stdout starts with one of:
#              ALLOW no-conflict
#              ALLOW unknown -- <why files could not be determined>
#              ALLOW forced -- <lane> <file> [<lane> <file> ...]
# Exit 1  -- refuse (no --force). stdout starts with:
#              REFUSE <lane> <file> [<lane> <file> ...]
# Exit 2  -- usage error.
#
# --exclude-lane <lane>  never treated as a collision source -- for a
#                         candidate lane whose OWN worktree has already been
#                         recorded against it (recorded before this check
#                         runs, or a re-run against an already-claimed lane);
#                         without this a lane would collide with itself.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PYTHON="${DISPATCH_PYTHON:-python3}"
CLI="$HERE/cli.py"

usage() { sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; exit 2; }

# --- file detection ---------------------------------------------------------

# Every backtick-quoted, path-shaped token in $1 (a file), resolved against
# `git -C $2 ls-files` -- exact match first, then unambiguous basename match.
# A token matching more than one file by basename is skipped: which one was
# meant is exactly the guesswork this check refuses to do.
_files_named_in() {
  local text_file="$1" repo="$2" all_files token matches
  [ -f "$text_file" ] || return 0
  all_files=$(git -C "$repo" ls-files 2>/dev/null) || return 0
  [ -n "$all_files" ] || return 0
  while IFS= read -r token; do
    [ -n "$token" ] || continue
    if grep -qxF "$token" <<<"$all_files"; then
      printf '%s\n' "$token"
      continue
    fi
    matches=$(grep -F "/$token" <<<"$all_files")
    if [ "$(wc -l <<<"${matches:-}")" -eq 1 ] && [ -n "$matches" ]; then
      printf '%s\n' "$matches"
    fi
  done < <(grep -oE '`[A-Za-z0-9_./-]+\.[A-Za-z0-9]+`' "$text_file" 2>/dev/null | tr -d '`' | sort -u)
}

# Files this PR's diff touches, relative to the repo -- the most precise
# signal available, because the diff already exists.
_files_in_pr() {
  local repo="$1" pr="$2" gh_args=()
  [ -n "$repo" ] && gh_args=(-R "$repo")
  gh pr diff "$pr" "${gh_args[@]+"${gh_args[@]}"}" --name-only 2>/dev/null
}

# Files already changed on a worktree's branch, relative to the repo:
# committed-since-merge-base union with uncommitted. Empty for a freshly
# created worktree (identical to its base) -- that is correct, not a bug;
# see this file's header on why signal 2 only fires for a RESUMED branch.
_files_changed_in_worktree() {
  local worktree="$1" base="${2:-origin/main}" mb
  [ -d "$worktree" ] || return 0
  mb=$(git -C "$worktree" merge-base HEAD "$base" 2>/dev/null) || mb=""
  if [ -n "$mb" ]; then
    git -C "$worktree" diff --name-only "$mb" HEAD 2>/dev/null
  fi
  git -C "$worktree" status --porcelain 2>/dev/null | awk '{print $NF}'
}

# stdout: the candidate's file set (one path per line, de-duplicated).
# Nothing printed and exit 1 means UNKNOWN -- every signal came up silent.
detect_candidate_files() {
  local brief="$1" repo_path="$2" worktree="$3" pr="$4" repo="$5" found
  found=$(
    {
      _files_named_in "$brief" "$repo_path"
      [ -n "$worktree" ] && _files_changed_in_worktree "$worktree"
      [ -n "$pr" ] && _files_in_pr "$repo" "$pr"
    } | sort -u
  )
  found=$(grep -v '^[[:space:]]*$' <<<"$found" || true)
  [ -n "$found" ] || return 1
  printf '%s\n' "$found"
}

# stdout: one "<lane>\t<file>" line per file touched by an in-flight lane's
# worktree. Never fails -- an unreadable worktree is skipped for that lane
# alone (best effort, see this file's header).
in_flight_lane_files() {
  local exclude_lane="$1" json
  json=$("$PYTHON" "$CLI" open-worktrees 2>/dev/null) || return 0
  "$PYTHON" -c '
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    data = {"tasks": []}
for row in data.get("tasks", []):
    lane = row.get("lane", "")
    if lane == sys.argv[1]:
        continue
    wt = row.get("worktree_path", "")
    if not wt:
        continue
    print("{}\t{}".format(lane, wt))
' "$exclude_lane" <<<"$json"
}

# --- orchestration -----------------------------------------------------------

cmd="${1:-}"
[ "$cmd" = "check" ] || usage
shift

ISSUE="" BRIEF="" WORKTREE="" REPO_PATH="" REPO="" PR="" FORCE="" EXCLUDE_LANE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --issue) ISSUE="$2"; shift 2 ;;
    --brief) BRIEF="$2"; shift 2 ;;
    --worktree) WORKTREE="$2"; shift 2 ;;
    --repo-path) REPO_PATH="$2"; shift 2 ;;
    --repo) REPO="$2"; shift 2 ;;
    --pr) PR="$2"; shift 2 ;;
    --exclude-lane) EXCLUDE_LANE="$2"; shift 2 ;;
    --force) FORCE=1; shift ;;
    *) echo "collision-check: unknown argument '$1'" >&2; usage ;;
  esac
done
[ -n "$ISSUE" ] && [ -n "$BRIEF" ] && [ -n "$WORKTREE" ] && [ -n "$REPO_PATH" ] || usage

CANDIDATE_FILES=$(detect_candidate_files "$BRIEF" "$REPO_PATH" "$WORKTREE" "$PR" "$REPO")
if [ $? -ne 0 ] || [ -z "$CANDIDATE_FILES" ]; then
  # Deliberately NOT the phrase "could not determine" -- dispatch.sh's own
  # `--reviews-pr` authorship guard uses that exact phrase for an unrelated
  # refusal ("could not determine PR #N's author"), and a caller that greps
  # its own combined stdout+stderr for that phrase (this repo's test suite
  # does, asserting it NEVER appears on a non-review dispatch) would see this
  # allow-path message and misread it as that other, unrelated failure.
  echo "ALLOW unknown -- no file signal found for #$ISSUE (no path named in the issue/brief, no PR diff, no prior branch content); allowing rather than blocking most dispatches on a signal most issues never give"
  exit 0
fi

COLLISIONS=""
while IFS=$'\t' read -r lane wt; do
  [ -n "$lane" ] || continue
  lane_files=$(_files_changed_in_worktree "$wt")
  [ -n "$lane_files" ] || continue
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    if grep -qxF "$f" <<<"$lane_files"; then
      COLLISIONS="${COLLISIONS}${lane}	${f}"$'\n'
    fi
  done <<<"$CANDIDATE_FILES"
done < <(in_flight_lane_files "$EXCLUDE_LANE")

# AN OPEN PR IS A HOLDER TOO, and this half was missing.
#
# The loop above asks the LEDGER which lanes hold a worktree. That misses every
# open PR authored outside the lane system -- by a human, by the watchdog, or by
# a supervisor working directly. Such a PR has a diff, is unmerged, and is
# exactly as much of a collision as a live lane: the next lane to touch those
# files rebases into a conflict, or worse, silently reverts the PR's change.
#
# Measured 2026-08-20, and it is why this exists: PR #430 (open, modifying
# scripts/supervisor/worktree.sh) was invisible here, so #427 -- whose root
# cause is in that same file -- dispatched clean. Two writers, one file, and the
# guard said no-conflict. The candidate side already reads `gh pr diff`
# (`_files_in_pr`); only the holder side did not.
#
# Best effort by design, matching this file's existing posture: if `gh` cannot
# be reached the PR half contributes nothing and the lane half still runs. A
# collision check that refuses every dispatch when GitHub is slow would be
# removed within a day, and a guard nobody runs guards nothing.
#
# WHICH REPO, agent-supervisor#432's own review (do not re-break this): `--repo`
# is OPTIONAL -- dispatch.sh's own #17 guard documents that a caller may omit it
# and let the claim resolve from `--repo-path` instead. The first cut of this
# fix built `gh_args` only when `$REPO` was non-empty, so an omitted `--repo`
# meant `gh pr list`/`gh pr diff` ran with NO `-R` at all, and gh resolved the
# repo from the process's ambient CWD -- not from `--repo-path`, the only
# repo-identifying argument this script actually requires. Reproduced live:
# standing in a checkout of a DIFFERENT repo, a dispatch check for this repo was
# refused by that other repo's own open PR touching a same-named file. That is
# agent-supervisor#441 (bare-path, no-repo-scope) amplified from same-repo to
# cross-repo, and it fired on every plain dispatch, not just `--pr`-scoped ones.
#
# FIX: when `--repo` is absent, derive it from `--repo-path`'s own `origin`
# remote -- the exact normalization dispatch.sh's own #17 guard already uses
# (`git@host:owner/name.git`, `https://host/owner/name`, bare `owner/name`, all
# collapsed to `owner/name`). `gh` is NEVER invoked without an explicit `-R`
# here. If the repo cannot be determined either way, the PR half is skipped --
# not silently: see PR_CHECK_STATUS below, which must be readable in the
# output so this failure mode never reads as "checked, found nothing."
_repo_from_path() {
  local repo_path="$1" origin owner_name
  origin=$(git -C "$repo_path" remote get-url origin 2>/dev/null) || return 1
  owner_name=$(sed 's/\.git$//' <<<"$origin" | awk -F'[:/]' 'NF>=2{print $(NF-1)"/"$NF}')
  [ -n "$owner_name" ] || return 1
  printf '%s\n' "$owner_name"
}

# stdout: "PR#<n>\t<file>" per file in every open PR's diff, excluding
# $exclude_pr. Exit 1 (nothing printed) means the PR half could not run --
# `gh` missing, unreachable, or unauthenticated -- and the caller must show
# that, not treat empty stdout as "checked, found nothing" (see PR_CHECK_STATUS
# below). One `gh pr list --json number,files` call, not one `gh pr list` plus
# a `gh pr diff` per open PR -- the files are already in the list response, so
# this is O(1) network round-trips instead of O(open PRs) (#432 review finding
# 3, measured at ~5.1-5.5s added per dispatch against 8 open PRs).
open_pr_files() {
  local repo="$1" exclude_pr="$2" json
  json=$(gh pr list -R "$repo" --state open --json number,files 2>/dev/null) || return 1
  "$PYTHON" -c '
import json, sys
try:
    data = json.loads(sys.stdin.read())
except Exception:
    data = []
exclude = sys.argv[1]
for pr in data:
    n = str(pr.get("number") or "")
    if not n or n == exclude:
        continue
    for f in pr.get("files") or []:
        path = f.get("path", "")
        if path:
            print("PR#{}\t{}".format(n, path))
' "$exclude_pr" <<<"$json" || return 1
}

# PR_CHECK_STATUS is stated in every ALLOW/REFUSE/FORCED line below -- #432's
# own review measured that a `gh` outage produced output byte-identical to a
# real clean check ("ALLOW no-conflict", rc 0, no distinguishing text
# anywhere). Blindness must never read as cleanliness.
PR_CHECK_STATUS="ok"
PR_REPO="$REPO"
if [ -z "$PR_REPO" ]; then
  PR_REPO=$(_repo_from_path "$REPO_PATH") || PR_REPO=""
fi
if [ -z "$PR_REPO" ]; then
  # NOT the phrase "could not determine" -- see this file's own note above
  # detect_candidate_files's ALLOW-unknown message for why: dispatch.sh's own
  # test suite asserts that exact phrase never appears on a non-review
  # dispatch (test_dispatch.sh's "the authorship question never arises"), and
  # this script's combined stdout+stderr is folded into dispatch.sh's own.
  PR_CHECK_STATUS="SKIPPED -- repo unresolved (pass --repo, or run somewhere --repo-path's origin remote resolves to owner/name)"
elif ! command -v gh >/dev/null 2>&1; then
  PR_CHECK_STATUS="SKIPPED -- gh not on PATH"
else
  PR_HOLDER_OUT=$(open_pr_files "$PR_REPO" "$PR") || PR_CHECK_STATUS="SKIPPED -- gh pr list failed (GitHub unreachable, rate-limited, or not authenticated)"
fi

if [ "$PR_CHECK_STATUS" = ok ] && [ -n "${PR_HOLDER_OUT:-}" ]; then
  while IFS=$'\t' read -r holder f; do
    [ -n "$holder" ] || continue
    if grep -qxF "$f" <<<"$CANDIDATE_FILES"; then
      COLLISIONS="${COLLISIONS}${holder}	${f}"$'\n'
    fi
  done <<<"$PR_HOLDER_OUT"
fi

if [ -z "$COLLISIONS" ]; then
  echo "ALLOW no-conflict -- #$ISSUE's candidate files (${CANDIDATE_FILES//$'\n'/, }) do not overlap any in-flight lane (open-PR check: $PR_CHECK_STATUS)"
  exit 0
fi

if [ -n "$FORCE" ]; then
  echo "ALLOW forced -- #$ISSUE overlaps in-flight lane(s), dispatched anyway by --force (open-PR check: $PR_CHECK_STATUS):"
  printf '%s' "$COLLISIONS" | while IFS=$'\t' read -r lane f; do
    [ -n "$lane" ] && echo "  $lane: $f"
  done
  exit 0
fi

echo "REFUSE -- #$ISSUE's files overlap in-flight lane(s) (open-PR check: $PR_CHECK_STATUS):"
printf '%s' "$COLLISIONS" | while IFS=$'\t' read -r lane f; do
  [ -n "$lane" ] && echo "  $lane: $f"
done
exit 1
