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
#   AUTHOR_PANE_IDS              -- agent-supervisor#631: a THIRD parallel
#                                   array -- each contributor task's frozen
#                                   `tasks.pane_id` snapshot (`task-lane`'s
#                                   own `pane_id` field), or '' when the task
#                                   predates that column. Populated in one
#                                   finishing pass over AUTHOR_TASKS so every
#                                   resolution path above shares it, rather
#                                   than each one threading it through
#                                   separately. See dispatch.sh's
#                                   author-exclusion loop for why this
#                                   matters: comparing a stale contributor
#                                   LANE STRING against a live candidate
#                                   re-resolves through the ledger's mutable
#                                   `lanes` table, which a later, unrelated
#                                   dispatch can silently overwrite for that
#                                   same string (`renumber-windows on`
#                                   reassigning a closed window's index).
#                                   The frozen snapshot is immune to that by
#                                   construction.
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
  AUTHOR_PANE_IDS=()
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
  # agent-supervisor#619: is `$1` already in the candidate-path list passed
  # as the remaining args? Only used to dedupe the two independent worktree-
  # path sources (git-worktree-list, and the ledger's open-worktrees) before
  # looking each one up -- looking the same path up twice would not be
  # wrong (both `_rpc_add_contributor` and the ledger's own point lookup are
  # idempotent), just wasted work.
  _rpc_path_known() {
    local want="$1" have
    shift
    for have in "$@"; do
      [ "$have" = "$want" ] && return 0
    done
    return 1
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
  #
  # agent-supervisor#473: the issue a PR closes can live in a DIFFERENT repo
  # than the PR itself -- a lane dispatched against an issue in repo A opens
  # its PR in repo B because the code it touches lives there (#472 is the
  # live case: PR here in agent-supervisor, closing issue #299 in
  # agent-dotfiles). `closingIssuesReferences` already names each
  # reference's own repository; querying `contributor-issue-lanes` with
  # THIS PR's repo (`$repo`) regardless -- what this used to do -- does not
  # just fail to find the cross-repo contributor, it makes
  # `get_contributor_tasks_for_issue`'s own repo-narrowing (#146) filter the
  # true contributor OUT, working exactly as documented against the WRONG
  # repo. So each candidate below carries the repo it was actually closed
  # FROM. Only the commit-message fallback (`fixes #N`, no owner/repo
  # prefix to read) has no repo of its own to name and is scoped to this
  # PR's own repo, same as before.
  local candidate_pairs
  candidate_pairs=$(python3 -c '
import json, re, sys

own_repo = sys.argv[1]
data = json.loads(sys.argv[2])
seen = set()

def emit(repo, number):
    key = (repo, number)
    if key in seen:
        return
    seen.add(key)
    print(f"{repo}\t{number}")

for ref in data.get("closingIssuesReferences") or []:
    repository = ref.get("repository") or {}
    owner = (repository.get("owner") or {}).get("login")
    name = repository.get("name")
    number = ref.get("number")
    if number is None:
        continue
    emit(f"{owner}/{name}" if owner and name else own_repo, number)

for commit in data.get("commits") or []:
    for field in ("messageHeadline", "messageBody"):
        text = commit.get(field) or ""
        for m in re.finditer(r"(?i)(?:fixes|closes|resolves)\s+#([0-9]+)", text):
            emit(own_repo, int(m.group(1)))
' "$repo" "$pr_json")
  local repo_args=()
  [ -n "$repo" ] && repo_args=(--repo "$repo")

  local candidate_repo candidate_issue issue_json c_lane c_task
  while IFS=$'\t' read -r candidate_repo candidate_issue; do
    [ -n "$candidate_issue" ] || continue
    local issue_repo_args=()
    [ -n "$candidate_repo" ] && issue_repo_args=(--repo "$candidate_repo")
    issue_json=$("$ledger_python" "$ledger_cli" contributor-issue-lanes --issue "$candidate_issue" "${issue_repo_args[@]+"${issue_repo_args[@]}"}" 2>&1)
    if [ $? -ne 0 ]; then
      echo "dispatch: contributor-issue-lanes lookup failed for issue #$candidate_issue -- refusing (authorship unknown, failing closed)" >&2
      sed 's/^/  /' <<<"$issue_json" >&2
      unset -f _rpc_author_lane_known _rpc_add_contributor _rpc_path_known
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
  done <<<"$candidate_pairs"

  # 2.1. agent-supervisor#308 item 1: the explicit "task X's work opened PR
  # N" record.
  local pr_task_json p_lane p_task
  pr_task_json=$("$ledger_python" "$ledger_cli" pr-task --repo "$repo" --pr "$pr" 2>&1)
  if [ $? -ne 0 ]; then
    echo "dispatch: pr-task lookup failed for PR #$pr -- refusing (authorship unknown, failing closed)" >&2
    sed 's/^/  /' <<<"$pr_task_json" >&2
    unset -f _rpc_author_lane_known _rpc_add_contributor _rpc_path_known
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
  pr_contrib_json=$("$ledger_python" "$ledger_cli" contributor-pr-lanes --pr "$pr" "${repo_args[@]+"${repo_args[@]}"}" 2>&1)
  if [ $? -ne 0 ]; then
    echo "dispatch: contributor-pr-lanes lookup failed for PR #$pr -- refusing (authorship unknown, failing closed)" >&2
    sed 's/^/  /' <<<"$pr_contrib_json" >&2
    unset -f _rpc_author_lane_known _rpc_add_contributor _rpc_path_known
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
  # out. Two INDEPENDENT sources of candidate paths, unioned -- not a single
  # source with a fallback, because agent-supervisor#619 measured a real PR
  # (as531-redo531 / #618) where the FIRST source alone came back silent for
  # a worktree the ledger still knew about and could open directly in one
  # call (`worktree-lane --path ...`). A lane that renamed its branch inside
  # its own dispatched worktree already defeats branch- and commit-based
  # lookup above; #619's case additionally shows `git worktree list` on
  # `$repo_path` is not guaranteed to still know about that worktree by the
  # time a review is requested (it depends on THIS repo_path's own worktree
  # admin state agreeing with reality) -- so a second, independent source is
  # consulted too.
  #
  # Source A: `git worktree list --porcelain` on `$repo_path`, exactly as
  # before #619. Cheap, and correct whenever repo_path's own registry is
  # current.
  #
  # Source B (agent-supervisor#619): the LEDGER's own record of every
  # in-flight worktree (`open-worktrees`, agent-supervisor#291's collision
  # check already relies on the same query). This does not depend on
  # `$repo_path` at all -- only on the ledger row `#611` guarantees exists --
  # so a candidate from here is checked directly on disk (`git -C <path>
  # rev-parse --abbrev-ref HEAD`) rather than through repo_path's registry.
  # This is the path #619's issue names as "the record was there the whole
  # time": `tasks.worktree_path` survives a branch rename because the
  # worktree itself is never renamed, only its branch.
  if [ -n "$repo_path" ]; then
    local worktree_list matched_worktree
    local -a candidate_worktree_paths=()
    worktree_list=$(git -C "$repo_path" worktree list --porcelain 2>/dev/null || true)
    if [ -n "$worktree_list" ]; then
      matched_worktree=$(awk -v want="branch refs/heads/$HEAD_REF" '
        /^worktree / { path = substr($0, 10) }
        $0 == want { print path }
      ' <<<"$worktree_list")
      matched_worktree=$(head -n1 <<<"$matched_worktree")
      [ -n "$matched_worktree" ] && candidate_worktree_paths+=("$matched_worktree")
    fi

    local open_worktrees_json ow_path ow_branch
    open_worktrees_json=$("$ledger_python" "$ledger_cli" open-worktrees 2>&1)
    if [ $? -eq 0 ]; then
      while IFS= read -r ow_path; do
        [ -n "$ow_path" ] || continue
        [ -d "$ow_path" ] || continue
        _rpc_path_known "$ow_path" "${candidate_worktree_paths[@]+"${candidate_worktree_paths[@]}"}" && continue
        ow_branch=$(git -C "$ow_path" rev-parse --abbrev-ref HEAD 2>/dev/null) || continue
        [ "$ow_branch" = "$HEAD_REF" ] || continue
        candidate_worktree_paths+=("$ow_path")
      done < <(python3 -c '
import json, sys
try:
    data = json.loads(sys.argv[1])
except Exception:
    data = {"tasks": []}
for row in data.get("tasks", []):
    wt = row.get("worktree_path", "")
    if wt:
        print(wt)
' "$open_worktrees_json")
    fi

    local worktree_json w_lane w_task
    for matched_worktree in "${candidate_worktree_paths[@]+"${candidate_worktree_paths[@]}"}"; do
      worktree_json=$("$ledger_python" "$ledger_cli" worktree-lane --path "$matched_worktree" 2>&1)
      if [ $? -ne 0 ]; then
        echo "dispatch: worktree-lane lookup failed for '$matched_worktree' -- refusing (authorship unknown, failing closed)" >&2
        sed 's/^/  /' <<<"$worktree_json" >&2
        unset -f _rpc_author_lane_known _rpc_add_contributor _rpc_path_known
        return 1
      fi
      if grep -qF '"known":true' <<<"$worktree_json"; then
        CONTRIBUTORS_RESOLVED=1
        w_lane=$(sed -n 's/.*"lane":"\([^"]*\)".*/\1/p' <<<"$worktree_json")
        w_task=$(sed -n 's/.*"task":"\([^"]*\)".*/\1/p' <<<"$worktree_json")
        _rpc_add_contributor "$w_lane" "$w_task"
      fi
    done
  fi

  # 3.1. Legacy last resort, kept only for tasks dispatched before #117.
  if [ ${#AUTHOR_LANES[@]} -eq 0 ] && [[ "$HEAD_REF" =~ ^(lane|fix|feat|chore|docs)/([0-9]+)-(.+)$ ]]; then
    FALLBACK_TASK="${prefix}${BASH_REMATCH[2]}-${BASH_REMATCH[3]}"
    local fallback_json f_lane
    fallback_json=$("$ledger_python" "$ledger_cli" task-lane --task "$FALLBACK_TASK" 2>&1)
    if [ $? -ne 0 ]; then
      echo "dispatch: task-lane lookup failed for task '$FALLBACK_TASK' -- refusing (authorship unknown, failing closed)" >&2
      sed 's/^/  /' <<<"$fallback_json" >&2
      unset -f _rpc_author_lane_known _rpc_add_contributor _rpc_path_known
      return 1
    fi
    if grep -qF '"known":true' <<<"$fallback_json"; then
      CONTRIBUTORS_RESOLVED=1
      f_lane=$(sed -n 's/.*"lane":"\([^"]*\)".*/\1/p' <<<"$fallback_json")
      _rpc_add_contributor "$f_lane" "$FALLBACK_TASK"
    fi
  fi

  # agent-supervisor#631: one finishing pass over the now-final AUTHOR_TASKS
  # -- not folded into `_rpc_add_contributor` above -- because that function
  # is called from several different resolution paths above and this way the
  # pane_id lookup runs exactly once per DISTINCT contributor, after
  # dedup, rather than once per raw hit before it.
  local at_task at_pane_id_json at_pid
  for at_task in "${AUTHOR_TASKS[@]+"${AUTHOR_TASKS[@]}"}"; do
    at_pid=""
    if [ -n "$at_task" ]; then
      at_pane_id_json=$("$ledger_python" "$ledger_cli" task-lane --task "$at_task" 2>/dev/null)
      at_pid=$(sed -n 's/.*"pane_id":"\([^"]*\)".*/\1/p' <<<"$at_pane_id_json" | head -1)
    fi
    AUTHOR_PANE_IDS+=("$at_pid")
  done

  unset -f _rpc_author_lane_known _rpc_add_contributor _rpc_path_known
  return 0
}
