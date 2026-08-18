#!/usr/bin/env bash
# Shared "is this PR's verdict independent of its author?" computation.
#
# WHY A LIBRARY, not inline in one script: agent-supervisor#179 found
# merge-pr.sh -- "the ONLY path a lane or the supervisor should use to merge
# a PR in this repo, so the gate cannot be skipped by habit" (its own header)
# -- with no notion of authorship or review independence at all. Every guard
# this estate has for "an author does not review/merge their own work"
# (dispatch.sh's `--reviews-pr` author exclusion) sat on the DISPATCH path;
# free text typed straight into a pane walks around all of them and reaches
# `gh pr merge` (via merge-pr.sh) directly. A prompt reading "merge the PR"
# was found sitting in the input box of the lane that AUTHORED PR #168 while
# its verdict was `none` -- it did not submit only because of an unrelated
# defect (#178), which is luck, not a guard.
#
# The author-lane / lane-relation / verdict computation below already
# existed once, in digest.sh, built for REPORTING independence in the
# estate's status digest. Re-deriving it a second time for ENFORCEMENT here
# is exactly the shape agent-supervisor#108 already burned a day on: two
# implementations of "is this the same lane" that quietly stopped agreeing
# the moment the session was renamed. So this file is that one computation,
# pulled out so digest.sh (reporting) and merge-pr.sh (enforcement) both call
# it, and digest.sh's own copy is deleted rather than kept alongside a
# duplicate.
#
# Callers must set, before sourcing this file:
#   HERE                              directory containing cli.py / verdict.py
#   STATE                             ledger state dir
#   LEDGER_PYTHON, LEDGER_CLI         how to invoke cli.py
#   VERDICT_PYTHON, VERDICT_SOURCE    how to invoke verdict.py
#   VERDICT_BIN (optional)            stub override -- takes --repo/--number/
#                                     --head-sha, prints {"verdict":...} JSON
#
# Every function here fails CLOSED: `known:false`, `value:null`, or
# `verdict:"unknown"` -- never a guess dressed up as an answer. A caller that
# treats anything but `independence_verdict`'s `value == true` as permission
# to proceed has broken the contract this file exists to hold.

# agent-supervisor#108: the ONE comparison of two lane ids in this system --
# `core.lane_relation`, exposed via `cli.py lane-relation` -- so this and
# `dispatch.sh`'s author-exclusion guard cannot drift on what "the same
# lane" means the way two hand-rolled `==` comparisons already did once. A
# lane id embeds the tmux SESSION name, and renaming the session (done
# 2026-08-14 to recover from #102) changes that name for every window at
# once without touching which window is which -- comparing raw strings after
# a rename answers "different lane" for the exact same window.
lane_relation() {  # lane_relation <lane> <other> [lane-pane-id] -> same|different|unknown
  # agent-supervisor#332: the optional third argument mirrors dispatch.sh's
  # own copy of this function (#235) -- a pane id for `$1`, forwarded as
  # `cli.py lane-relation --lane-pane-id`, so the answer is reconciled
  # against the ledger's `pane_id` registry INSTEAD OF trusting `$1`'s
  # index-shape half, which `renumber-windows on` rewrites out from under a
  # lane the instant a lower window closes. Omitted (the pre-#332 shape),
  # this is unchanged: a positive shape-check answer is trusted, which is
  # exactly the gap #332 closes for merge-pr.sh and digest.sh -- see
  # resolve_lane_relation() below, the one place this file now supplies a
  # pane id for those two callers.
  local json rel lane_pane_id_args=()
  [ -z "${3:-}" ] || lane_pane_id_args=(--lane-pane-id "$3")
  json=$("$LEDGER_PYTHON" "$LEDGER_CLI" --state-dir "$STATE" lane-relation --lane "$1" --other "$2" "${lane_pane_id_args[@]}" 2>/dev/null) || json=""
  rel=$(jq -r '.relation // ""' 2>/dev/null <<<"$json") || rel=""
  case "$rel" in
    same|different) printf '%s\n' "$rel" ;;
    *) printf 'unknown\n' ;;
  esac
}

# agent-supervisor#332: `merge-pr.sh` (enforcement) and `digest.sh`
# (reporting) both compared author/reviewer lane ids through the bare
# `lane_relation()` above with NO pane id -- the one call shape
# `dispatch.sh`'s #235 fix never touched, because it lives in a shared
# author-exclusion loop `dispatch.sh` owns, not here. That leaves a
# positive-looking shape answer (`same` on matching index, `different` on a
# mismatched one) trusted at the exact two call sites #179 built specifically
# because dispatch-time guards can be routed around (this file's own header).
# A window renumber between an author's dispatch and a reviewer's makes that
# shape answer wrong in BOTH directions: same index now naming two genuinely
# different windows reads a legitimate independent review as a self-review
# (a false refusal), and a self-review whose window picked up a new index
# reads as independent (the actual security hole -- invariant 9).
#
# The fix: a lane id that CONTRIBUTED here (author, or a stamped reviewer)
# was, by construction, registered by `record-dispatch` at some point, so
# the ledger's own `pane_id` registry -- not a fresh tmux measurement,
# dispatch.sh's luxury as the only caller with a live candidate target
# already resolved (#235's own comment) -- is the fact this file has for
# free. Try `$1`'s own recorded pane id first; if `$1` was never registered
# (a hand-typed `Review-Lane:` stamp naming nothing this ledger knows), fall
# back to `$2`'s. Only when NEITHER side has a registered pane id does this
# refuse to answer -- `unknown`, never the untrustworthy shape-only guess --
# which is the AMBIGUOUS/fail-closed posture the rest of this file already
# holds everywhere else.
resolve_lane_relation() {  # resolve_lane_relation <lane> <other> -> same|different|unknown
  local lane="$1" other="$2" pane_id
  pane_id=$(_lane_own_pane_id "$lane")
  if [ -n "$pane_id" ]; then
    lane_relation "$lane" "$other" "$pane_id"
    return
  fi
  pane_id=$(_lane_own_pane_id "$other")
  if [ -n "$pane_id" ]; then
    lane_relation "$other" "$lane" "$pane_id"
    return
  fi
  printf 'unknown\n'
}

# The ledger's own recorded `pane_id` for one lane id, or empty if that lane
# was never registered (`register_lane`, via `record-dispatch`) or the
# ledger cannot be read. Not exposed as a `cli.py` subcommand of its own --
# this is the one place it is needed, and adding argparse surface for a
# single-field lookup `cli.py lane-relation --lane X --other X` cannot
# already answer would be a second way to ask the same question (#108's own
# lesson).
_lane_own_pane_id() {  # _lane_own_pane_id <lane> -> pane_id or empty
  "$LEDGER_PYTHON" -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
row = Ledger(sys.argv[2]).get_lane(sys.argv[3])
print((row or {}).get("pane_id") or "")
' "$HERE" "$STATE" "$1" 2>/dev/null
}

# `dispatch.sh`'s task ids are minted `<window-prefix><issue>-<slug>`, where
# the window prefix is the repo name's initials (hyphen-joined words) or the
# whole name for a one-word repo -- e.g. "agent-supervisor" -> "as",
# "skills" -> "skills". Needed only for the legacy branch-name fallback in
# `author_lane_for` below.
repo_task_prefix() {
  local repo_full="$1" name part prefix
  name="${repo_full##*/}"
  if [[ "$name" == *-* ]]; then
    prefix=""
    while IFS= read -r part; do
      prefix="${prefix}${part:0:1}"
    done < <(tr '-' '\n' <<<"$name")
    printf '%s\n' "$prefix"
  else
    printf '%s\n' "$name"
  fi
}

# Resolve the lane that authored a PR by the same fail-closed chain
# dispatch.sh --reviews-pr uses: closing issue -> author-issue-lane, then
# branch name as a last-resort task-id hint. Missing data is not an error to
# raise; it only makes `{known:false}`, and every caller here treats that as
# "authorship not established", never as "no author to worry about".
#
# agent-supervisor#144: left as GraphQL (`gh pr view`), deliberately.
# `closingIssuesReferences` -- the "Fixes #N" link GitHub itself resolves --
# is a GraphQL-only concept; REST has no endpoint that answers it.
author_lane_for() {
  local repo_full="$1" number="$2" pr_json head_ref candidates candidate issue_json
  local prefix fallback_task fallback_json
  if ! pr_json=$(gh pr view "$number" -R "$repo_full" --json headRefName,closingIssuesReferences,commits 2>/dev/null); then
    jq -nc --arg detail "independence unknown -- PR author lane unresolved: gh pr view failed" \
      '{known:false, lane:null, task:null, detail:$detail}'
    return
  fi
  if ! jq -e . >/dev/null 2>&1 <<<"$pr_json"; then
    jq -nc --arg detail "independence unknown -- PR author lane unresolved: gh pr view produced unreadable JSON" \
      '{known:false, lane:null, task:null, detail:$detail}'
    return
  fi
  head_ref=$(jq -r '.headRefName // ""' <<<"$pr_json")
  candidates=$(
    {
      jq -r '.closingIssuesReferences[]?.number // empty' <<<"$pr_json"
      jq -r '.commits[]? | (.messageHeadline // ""), (.messageBody // "")' <<<"$pr_json" \
        | grep -ioE '(fixes|closes|resolves) #[0-9]+' | grep -oE '[0-9]+' || true
    } | awk '!seen[$0]++'
  )
  for candidate in $candidates; do
    if issue_json=$("$LEDGER_PYTHON" "$LEDGER_CLI" --state-dir "$STATE" author-issue-lane --issue "$candidate" --head-ref "$head_ref" 2>/dev/null) \
       && jq -e '.known == true' >/dev/null 2>&1 <<<"$issue_json"; then
      jq -nc --arg lane "$(jq -r '.lane' <<<"$issue_json")" \
             --arg task "$(jq -r '.task // ""' <<<"$issue_json")" \
             '{known:true, lane:$lane, task:$task, detail:""}'
      return
    fi
  done
  prefix=$(repo_task_prefix "$repo_full")
  if [[ "$head_ref" =~ ^(lane|fix|feat|chore|docs)/([0-9]+)-(.+)$ ]]; then
    fallback_task="${prefix}${BASH_REMATCH[2]}-${BASH_REMATCH[3]}"
    if fallback_json=$("$LEDGER_PYTHON" "$LEDGER_CLI" --state-dir "$STATE" task-lane --task "$fallback_task" 2>/dev/null) \
       && jq -e '.known == true' >/dev/null 2>&1 <<<"$fallback_json"; then
      jq -nc --arg lane "$(jq -r '.lane' <<<"$fallback_json")" \
             --arg task "$fallback_task" \
             '{known:true, lane:$lane, task:$task, detail:""}'
      return
    fi
  fi
  jq -nc --arg head "$head_ref" \
         --arg task "${fallback_task:-none}" \
         '{known:false, lane:null, task:null, detail:("independence unknown -- PR author lane unresolved from ledger issue lookup or branch " + ($head|if length > 0 then . else "unknown" end) + " (task " + $task + ")")}'
}

# Resolves one PR's verdict through the `verdict.py` adapter. Always prints
# SOME JSON -- a source failure or a missing/broken stub reads as
# `{"verdict":"unknown"}`, never as empty output a caller could mistake for
# "no verdict field".
#
# head_sha is the PR's current headRefOid, passed through so a source can
# tell a review or ledger record filed against an OLDER commit apart from
# one that answers for this head (agent-dotfiles#218).
verdict_for() {
  local repo_full="$1" number="$2" head_sha="$3" out
  if [ -n "${VERDICT_BIN:-}" ]; then
    out=$("$VERDICT_BIN" --repo "$repo_full" --number "$number" --head-sha "$head_sha" 2>/dev/null)
  else
    out=$("$VERDICT_PYTHON" "$HERE/verdict.py" --state-dir "$STATE" \
          get --repo "$repo_full" --number "$number" --source "$VERDICT_SOURCE" \
          --head-sha "$head_sha" 2>/dev/null)
  fi
  if ! jq -e . >/dev/null 2>&1 <<<"$out"; then
    out='{"verdict":"unknown","detail":"verdict source produced no readable output"}'
  fi
  printf '%s\n' "$out"
}

# The independence decision itself, given an already-fetched verdict JSON
# (`verdict_for`'s output), author JSON (`author_lane_for`'s output), and the
# `lane_relation` of the two lane ids (computed by the caller -- see
# agent-supervisor#108's comment on `lane_relation` above for why this is a
# ledger question, not a jq string comparison).
#
# Returns `{value, detail}`. `value` is `true` only when EVERY one of these
# holds:
#   - the verdict came from a source that can name its reviewer (a `**Verdict:`
#     PR comment or a ledger record) -- a plain GitHub review-state approval
#     carries no lane id under this estate's single shared account, so it can
#     never be told apart from a self-approval and is never independent here;
#   - that verdict is decisive (`approved` or `rejected`);
#   - the reviewer stamped a `Review-Lane:` (comment) or was recorded as
#     `reviewer` (ledger) -- a verdict with no lane to compare is not
#     independent, it is UNCHECKABLE;
#   - the PR's author lane is known;
#   - `lane_relation` positively says the author and reviewer lanes are
#     `different` -- `same` refuses (a real self-review, or the same window
#     under a renamed session), and `unknown` ALSO refuses (two ids this
#     system cannot compare establish nothing; treating that as permission is
#     exactly how a self-review gets laundered across the next rename).
# Anything else is `false` or `null`, and a caller that gates a merge on this
# must treat both identically: refuse.
independence_verdict() {  # independence_verdict <verdict-json> <author-json> <lane-relation>
  local v="$1" author="$2" lane_rel="$3"
  jq -n --argjson v "$v" --argjson author "$author" --arg lane_rel "$lane_rel" '
    if (($v.verdict_kind // "") | IN("comment", "ledger")) and (($v.verdict // "") | IN("approved", "rejected")) then
      if (($v.reviewer_lane // "") | length) == 0 then
        {value:null, detail:"independence unknown -- reviewer lane unresolved; comment verdicts must include Review-Lane: <lane-id>"}
      elif ($author.known == true) then
        if ($lane_rel == "same") then
          {value:false, detail:("NOT independent -- author lane " + $author.lane + " reviewed its own PR"
            + (if ($v.reviewer_lane != $author.lane) then " (reviewed as " + $v.reviewer_lane + " -- the same window, renamed session)" else "" end))}
        elif ($lane_rel == "different") then
          {value:true, detail:("independent -- author lane " + $author.lane + ", reviewer lane " + $v.reviewer_lane)}
        else
          {value:null, detail:("independence unknown -- author lane " + $author.lane + " and reviewer lane " + $v.reviewer_lane + " are not comparable lane ids")}
        end
      else
        {value:null, detail:$author.detail}
      end
    elif (($v.verdict // "none") == "none") then
      # #184: "not yet reviewed" is not the same statement as "independence
      # unknown" -- restoring the pre-extraction (main) behavior byte for
      # byte for this specific case. Every never-reviewed PR used to carry
      # an empty detail here; the general catch-all below was made to
      # explain itself for merge-pr.sh#179 refusal messages, and that
      # explanation leaked onto this, the single most common case in the
      # digest, as noise nobody asked for.
      {value:null, detail:""}
    elif (($v.verdict // "unknown") == "unknown") and (($v.detail // "") | length) > 0 then
      # agent-supervisor#213: an `unknown` verdict already carries a
      # SPECIFIC reason -- most often now "covers Reviewed-SHA X, not
      # current head Y" or the timestamp-backstop refusal (#213), but also
      # the pre-existing review-object staleness message (#218) which was
      # ALSO being discarded here before this branch existed. The generic
      # "not a lane-stamped comment or ledger record" text below is correct
      # for a genuinely undated verdict but actively misleading for one
      # that failed a SHA/timestamp check -- surface the sources own
      # detail instead of overwriting it, so merge-pr.sh refusal names
      # which SHA the verdict covered and which is being merged, not just
      # "independence unknown".
      {value:null, detail:("independence unknown -- " + $v.detail)}
    else
      {value:null, detail:("independence unknown -- verdict is " + ($v.verdict // "unknown")
        + (if (($v.verdict_kind // "") | length) > 0 then " (" + $v.verdict_kind + ")" else "" end)
        + ", not a lane-stamped comment or ledger record -- a plain GitHub review-state approval carries no lane id under this shared account and cannot be told apart from a self-approval")}
    end'
}
