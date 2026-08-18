#!/bin/bash
# The exhaustive PR-contributor resolution chain -- extracted from
# dispatch.sh's `--reviews-pr` author-exclusion guard (agent-supervisor#190,
# widened by #308) so a SECOND caller can run the identical check rather than
# reimplementing it. #321's independent review measured the cost of a
# second, drifted copy first-hand: `verdict-independence.sh`'s own
# `author_lane_for` reimplements an earlier, narrower version of this same
# resolution and was never updated when dispatch.sh's grew a fifth path
# (issue, PR-task, PR-contributor, worktree, legacy branch) -- see that
# review's item 4. This file exists so agent-supervisor#308 item 3's
# mark-pr-external gate does not become a second instance of the same
# defect.
#
# Usage:
#   . resolve-pr-contributors.sh
#   resolve_pr_contributors <repo> <pr> <repo-path> <prefix> <ledger-python> <ledger-cli>
#
# On success (return 0) these globals are set:
#   AUTHOR_LANES, AUTHOR_TASKS   -- parallel arrays, every lane+task the
#                                   ledger can show contributed to <pr>.
#                                   Both legitimately empty when the PR
#                                   genuinely has none (see
#                                   CONTRIBUTORS_RESOLVED below for how a
#                                   caller tells that apart from "the check
#                                   itself never ran").
#   CONTRIBUTORS_RESOLVED       -- "1" once any resolution path below
#                                   answered with a KNOWN fact (a
#                                   contributor found, or a path that
#                                   confirmed there is nothing on that
#                                   path). This is not "the array is
#                                   non-empty" -- see dispatch.sh's own
#                                   comment on this variable for why an
#                                   empty-array check alone cannot
#                                   distinguish "no review requested" from
#                                   "requested and the ledger came back
#                                   silent".
#   HEAD_REF, FALLBACK_TASK     -- as resolved along the way.
#
# On failure (return 1) either the PR/head branch could not be read at all
# (`gh` unreachable, no head branch), OR one of the five ledger lookups
# below failed mid-chain (the `ledger_cli` process itself errored, not "ran
# and answered known:false") -- an error message is already printed to
# stderr either way. The caller MUST treat this as UNKNOWN, never as
# "checked, found nothing": proceeding here is exactly the "no lane
# contributed" claim agent-supervisor#308 item 3 requires POSITIVE evidence
# for, and this function could not produce any. A mid-chain ledger read
# failure and a genuinely-empty-but-fully-checked chain must never look the
# same to a caller -- see the PR #331 review that added this failure path:
# a failed read left CONTRIBUTORS_RESOLVED unset and AUTHOR_LANES empty,
# byte-for-byte identical to a real "checked every path, found nobody"
# result, which is exactly the "empty instrument reads as an empty world"
# defect this file exists to avoid.
resolve_pr_contributors() {
  local repo="$1" pr="$2" repo_path="$3" prefix="$4" ledger_python="$5" ledger_cli="$6"

  AUTHOR_LANES=()
  AUTHOR_TASKS=()
  FALLBACK_TASK=""
  CONTRIBUTORS_RESOLVED=""
  HEAD_REF=""

  _rpc_author_lane_known() {
    local want="$1" have
    for have in "${AUTHOR_LANES[@]+"${AUTHOR_LANES[@]}"}"; do
      [ "$have" = "$want" ] && return 0
    done
    return 1
  }
  _rpc_add_contributor() {
    local lane="$1" task="$2"
    _rpc_author_lane_known "$lane" && return 0
    AUTHOR_LANES+=("$lane")
    AUTHOR_TASKS+=("$task")
  }

  local gh_repo_args=()
  [ -n "$repo" ] && gh_repo_args=(-R "$repo")
  local pr_json
  pr_json=$(gh pr view "$pr" "${gh_repo_args[@]+"${gh_repo_args[@]}"}" --json headRefName,closingIssuesReferences,commits 2>&1)
  if [ $? -ne 0 ]; then
    echo "dispatch: cannot read PR #$pr -- refusing (authorship unknown, failing closed)" >&2
    sed 's/^/  /' <<<"$pr_json" >&2
    return 1
  fi
  HEAD_REF=$(sed -n 's/.*"headRefName":"\([^"]*\)".*/\1/p' <<<"$pr_json")
  if [ -z "$HEAD_REF" ]; then
    echo "dispatch: PR #$pr's head branch is unreadable -- refusing (authorship unknown, failing closed)" >&2
    return 1
  fi

  # 1 & 2. THE LEDGER, asked by ISSUE -- see dispatch.sh's own long-form
  # comment (preserved there) for why by-issue, pooled across both
  # candidate sources, comes first.
  local candidate_issues
  candidate_issues=$(
    {
      grep -oE '"closingIssuesReferences":\[[^]]*\]' <<<"$pr_json" \
        | grep -oE '"number":[0-9]+' | grep -oE '[0-9]+'
      grep -oE '"message(Headline|Body)":"[^"]*"' <<<"$pr_json" \
        | grep -ioE '(fixes|closes|resolves) #[0-9]+' | grep -oE '[0-9]+'
    } | awk '!seen[$0]++'
  )
  local candidate_issue issue_json c_lane c_task
  for candidate_issue in $candidate_issues; do
    issue_json=$("$ledger_python" "$ledger_cli" contributor-issue-lanes --issue "$candidate_issue" 2>&1)
    if [ $? -ne 0 ]; then
      echo "dispatch: contributor-issue-lanes lookup failed for issue #$candidate_issue -- refusing (authorship unknown, failing closed)" >&2
      sed 's/^/  /' <<<"$issue_json" >&2
      unset -f _rpc_author_lane_known _rpc_add_contributor
      return 1
    fi
    if grep -qF '"known":true' <<<"$issue_json"; then
      CONTRIBUTORS_RESOLVED=1
      while IFS=$'\t' read -r c_lane c_task; do
        [ -n "$c_lane" ] || continue
        _rpc_add_contributor "$c_lane" "$c_task"
      done < <(grep -oE '"lane":"[^"]*","task":"[^"]*"' <<<"$issue_json" \
        | sed -E 's/"lane":"([^"]*)","task":"([^"]*)"/\1\t\2/')
    fi
  done

  # 2.1. agent-supervisor#308 item 1: the explicit "task X's work opened PR
  # N" record.
  local pr_task_json p_lane p_task
  pr_task_json=$("$ledger_python" "$ledger_cli" pr-task --repo "$repo" --pr "$pr" 2>&1)
  if [ $? -ne 0 ]; then
    echo "dispatch: pr-task lookup failed for PR #$pr -- refusing (authorship unknown, failing closed)" >&2
    sed 's/^/  /' <<<"$pr_task_json" >&2
    unset -f _rpc_author_lane_known _rpc_add_contributor
    return 1
  fi
  if grep -qF '"known":true' <<<"$pr_task_json"; then
    CONTRIBUTORS_RESOLVED=1
    p_lane=$(sed -n 's/.*"lane":"\([^"]*\)".*/\1/p' <<<"$pr_task_json")
    p_task=$(sed -n 's/.*"task":"\([^"]*\)".*/\1/p' <<<"$pr_task_json")
    _rpc_add_contributor "$p_lane" "$p_task"
  fi

  # 2.2. agent-supervisor#308 item 2: resolution path five, the PR's own
  # source_tasks rows asked directly by PR number.
  local pr_contrib_json
  pr_contrib_json=$("$ledger_python" "$ledger_cli" contributor-pr-lanes --pr "$pr" 2>&1)
  if [ $? -ne 0 ]; then
    echo "dispatch: contributor-pr-lanes lookup failed for PR #$pr -- refusing (authorship unknown, failing closed)" >&2
    sed 's/^/  /' <<<"$pr_contrib_json" >&2
    unset -f _rpc_author_lane_known _rpc_add_contributor
    return 1
  fi
  if grep -qF '"known":true' <<<"$pr_contrib_json"; then
    CONTRIBUTORS_RESOLVED=1
    while IFS=$'\t' read -r c_lane c_task; do
      [ -n "$c_lane" ] || continue
      _rpc_add_contributor "$c_lane" "$c_task"
    done < <(grep -oE '"lane":"[^"]*","task":"[^"]*"' <<<"$pr_contrib_json" \
      | sed -E 's/"lane":"([^"]*)","task":"([^"]*)"/\1\t\2/')
  fi

  # 3. agent-supervisor#117: which worktree currently has HEAD_REF checked
  # out.
  if [ -n "$repo_path" ]; then
    local worktree_list matched_worktree worktree_json w_lane w_task
    worktree_list=$(git -C "$repo_path" worktree list --porcelain 2>/dev/null || true)
    if [ -n "$worktree_list" ]; then
      matched_worktree=$(awk -v want="branch refs/heads/$HEAD_REF" '
        /^worktree / { path = substr($0, 10) }
        $0 == want { print path }
      ' <<<"$worktree_list")
      matched_worktree=$(head -n1 <<<"$matched_worktree")
      if [ -n "$matched_worktree" ]; then
        worktree_json=$("$ledger_python" "$ledger_cli" worktree-lane --path "$matched_worktree" 2>&1)
        if [ $? -ne 0 ]; then
          echo "dispatch: worktree-lane lookup failed for '$matched_worktree' -- refusing (authorship unknown, failing closed)" >&2
          sed 's/^/  /' <<<"$worktree_json" >&2
          unset -f _rpc_author_lane_known _rpc_add_contributor
          return 1
        fi
        if grep -qF '"known":true' <<<"$worktree_json"; then
          CONTRIBUTORS_RESOLVED=1
          w_lane=$(sed -n 's/.*"lane":"\([^"]*\)".*/\1/p' <<<"$worktree_json")
          w_task=$(sed -n 's/.*"task":"\([^"]*\)".*/\1/p' <<<"$worktree_json")
          _rpc_add_contributor "$w_lane" "$w_task"
        fi
      fi
    fi
  fi

  # 3.1. Legacy last resort, kept only for tasks dispatched before #117.
  if [ ${#AUTHOR_LANES[@]} -eq 0 ] && [[ "$HEAD_REF" =~ ^(lane|fix|feat|chore|docs)/([0-9]+)-(.+)$ ]]; then
    FALLBACK_TASK="${prefix}${BASH_REMATCH[2]}-${BASH_REMATCH[3]}"
    local fallback_json f_lane
    fallback_json=$("$ledger_python" "$ledger_cli" task-lane --task "$FALLBACK_TASK" 2>&1)
    if [ $? -ne 0 ]; then
      echo "dispatch: task-lane lookup failed for task '$FALLBACK_TASK' -- refusing (authorship unknown, failing closed)" >&2
      sed 's/^/  /' <<<"$fallback_json" >&2
      unset -f _rpc_author_lane_known _rpc_add_contributor
      return 1
    fi
    if grep -qF '"known":true' <<<"$fallback_json"; then
      CONTRIBUTORS_RESOLVED=1
      f_lane=$(sed -n 's/.*"lane":"\([^"]*\)".*/\1/p' <<<"$fallback_json")
      _rpc_add_contributor "$f_lane" "$FALLBACK_TASK"
    fi
  fi

  unset -f _rpc_author_lane_known _rpc_add_contributor
  return 0
}
