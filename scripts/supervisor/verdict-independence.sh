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
  #
  # agent-supervisor#251: left unwrapped deliberately -- a single sqlite
  # query with no evidence of ever hanging, and CI proved that bounding
  # every local call here (not just the ones shown to hang) pushed
  # test_digest.sh's wall-clock past its own 300s ceiling. See the
  # `VERDICT_CALL_TIMEOUT_SECONDS` comment below for what IS bounded and why.
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

# agent-supervisor#520 / the false-pass half of #513. Everything above turns
# on `lanes.pane_id`: two rows with different pane ids read as positively
# DIFFERENT lanes, and that is the single fact separating an independent
# review from a self-merge. Nothing ever asked whether a row is still TRUE.
#
# Measured on this estate 2026-08-23: all four of `estate:2`..`estate:5` held
# rows whose `pane_id` (`%38`, `%39`, `%51`, `%52`) named no pane on the
# running tmux server (whose panes were `%1`..`%5`), whose `repo` named a
# different checkout than the panes were in, and whose `server_id` named a
# session created the previous day. Every one satisfied `post-verdict.sh`'s
# "is this lane registered" check and handed THIS file a `pane_id` it treated
# as an identity. A registration nobody re-measures is not a weaker guard than
# no registration -- it is the opposite guard, because it converts a refusal
# into a pass.
#
# `lane_identity.py` answers three ways, and only one of them is acted on
# here. `contradicted` -- the tmux server the row itself names is reachable
# and disagrees with the row -- refuses. `unverifiable` does NOT refuse, and
# that boundary is deliberate and is the honest limit of this check:
#   * an off-pane lane (`claude-print`, `pi-rpc`, `acp`) has no tmux pane by
#     construction, and #292 exists specifically so those can be compared at
#     all -- refusing them here would undo it;
#   * a lane registered against a tmux server that is simply GONE (socket
#     removed, host rebooted) cannot be checked by anything;
#   * a fixture ledger with no live server behind it -- every shell test in
#     this repo -- is the same shape.
# So this narrows what a stale-or-fabricated row can do; it does not turn
# "cannot check" into a refusal. Stated rather than implied, because a gate
# that reports green on a case it structurally cannot see is the exact defect
# #513 filed.
_lane_identity_status() {  # _lane_identity_status <lane> -> "<status>\t<detail>"
  local out status detail
  out=$("$LEDGER_PYTHON" "$HERE/lane_identity.py" --lane "$1" --state-dir "$STATE" \
        --tmux-bin "${VERDICT_TMUX_BIN:-tmux}" 2>/dev/null)
  status=$(jq -r '.status // "unverifiable"' 2>/dev/null <<<"$out") || status="unverifiable"
  detail=$(jq -r '.detail // ""' 2>/dev/null <<<"$out") || detail=""
  case "$status" in
    verified|contradicted|unverifiable) : ;;
    *) status="unverifiable"; detail="lane_identity.py produced no readable status" ;;
  esac
  printf '%s\t%s\n' "$status" "$detail"
}

# agent-supervisor#251 (the same defect its own CI failure was measured
# against, not a new one): `author_lane_for` below shelled out to `gh pr
# view` with no bound at all -- the one call in this file with no timer on
# it, even though every OTHER `gh` call this estate makes (digest.sh's
# `gh_call`, quota.sh's `codexbar` guard, #267) already learned this lesson.
# Reproduced live: `tests/supervisor/test_shell_suites.py`'s own harness sends
# SIGTERM then SIGKILL to the WHOLE process group of a suite that hangs past
# 300s, and still could not confirm the group dead -- a `gh` blocked on a
# network round-trip is exactly the shape that produces. `with_timeout` here
# is the same self-contained `kill -0` poll loop as digest.sh's (#251),
# quota.sh's (#267) and advance-live.sh's (#51) -- not `timeout`/`gtimeout`,
# because this file is sourced by both digest.sh and merge-pr.sh and neither
# script may assume a GNU-coreutils PATH. Redefining the function here (both
# digest.sh and this file now define it) is harmless -- the two bodies are
# identical, and a caller that never sources digest.sh (merge-pr.sh) still
# needs its own copy to be self-contained.
# agent-supervisor#251 (second fix pass): the version of this function that
# shipped in the first fix pass killed only the direct backgrounded pid --
# `kill -TERM "$pid"` -- not any process THAT pid itself spawned (`verdict.py`
# shelling out to `gh`/`git` for #226's rebase-content check). A killed
# parent does not take its already-forked children down with it; they are
# reparented and keep running to their own completion. Reproduced directly:
# a `with_timeout`-wrapped call whose child backgrounds its own grandchild
# left that grandchild alive and unkillable through `$pid` after the timeout
# fired, which is the exact "SIGTERM escalated to SIGKILL, group still not
# confirmed dead" shape `test_shell_suites.py`'s harness reported against
# this suite. Fix: turn on job control (`set -m`) only around the
# backgrounding line, so the child starts its OWN process group instead of
# inheriting this script's, then signal the negative pid (`-"$pid"`) -- a
# process-group signal reaches the child and everything it forked, group
# leader included. `set -m` is restored to whatever it was before, so this
# does not change job-control behaviour for the rest of the script.
# agent-supervisor#251 (third fix pass -- see digest.sh's own copy of this
# function for the full measurement): the prior `kill -0`-poll-loop shape
# paid a fixed ~1s floor per with_timeout call regardless of how fast CMD
# actually finished, because the first poll check races the fork and almost
# always finds the child still alive. Four calls per PR x 17 PRs x 3+ full
# passes in test_digest.sh is what blew the 300s budget, not a genuine hang.
# A first attempt replaced the poll with a `wait "$pid"` raced against a
# background watchdog subshell; discarded after it reproduced sporadic empty
# `gh`/`verdict.py` output for individual PRs when run alongside other
# lanes' concurrent test suites on this shared box -- a job-control race
# from doubling the backgrounded jobs per call, not worth the timing win.
# Kept instead: the same poll loop, unchanged in shape, with the interval
# cut from a fixed 1s to 0.05s (`waited`/`secs` moved to centiseconds so the
# comparison stays integer-only) -- the fixed-second floor is gone, nothing
# about the wait/kill/timeout mechanics changed.
if ! declare -F with_timeout >/dev/null 2>&1; then
  with_timeout() {
    local secs="$1" outfile="$2"; shift 2
    local prev_monitor=0
    case $- in *m*) prev_monitor=1 ;; esac
    set -m
    "$@" >"$outfile" 2>/dev/null &
    local pid=$!
    [ "$prev_monitor" -eq 1 ] || set +m 2>/dev/null
    local waited_cs=0 max_cs=$((secs * 100)) timed_out=0
    while kill -0 "$pid" 2>/dev/null; do
      if [ "$waited_cs" -ge "$max_cs" ]; then
        timed_out=1
        kill -TERM -- -"$pid" 2>/dev/null
        sleep 1
        kill -KILL -- -"$pid" 2>/dev/null
        break
      fi
      sleep 0.05
      waited_cs=$((waited_cs + 5))
    done
    wait "$pid" 2>/dev/null
    local rc=$?
    [ "$timed_out" -eq 1 ] && return 124
    return "$rc"
  }
fi
AUTHOR_LANE_GH_TIMEOUT_SECONDS="${AUTHOR_LANE_GH_TIMEOUT_SECONDS:-20}"

# `verdict.py get` is the other call on this path that measurably hung
# (agent-supervisor#251's own CI: without a bound here, `test_digest.sh`'s
# ledger-source tests hung past 300s even with `author_lane_for`'s `gh`
# call already bounded above). Left the OTHER local calls in this file
# (`cli.py lane-relation`, `author-issue-lane`, `task-lane`) unwrapped
# deliberately: they are single sqlite queries with no evidence of ever
# hanging, and wrapping every one of them in a background-job-plus-poll
# (even a cheap one) adds real overhead multiplied across every PR this
# script processes -- measured live, that overhead alone pushed this same
# suite's total wall-clock past the 300s ceiling on GitHub's runner, turning
# a fixed hang into a new timeout. Bound what is proven to hang; measure
# before bounding the rest.
#
# `verdict.py get` is not one query, either -- a rebase-promotion case
# (`_content_unchanged_since`, #226) chains several of ITS OWN `gh`/`git`
# calls, each already bounded at 30s internally (verdict.py's own
# `_subprocess_runner`). Sized well above any realistic chain of those
# 30s-capped internal calls, still far under the 300s hard kill this whole
# suite runs under.
VERDICT_CALL_TIMEOUT_SECONDS="${VERDICT_CALL_TIMEOUT_SECONDS:-90}"
run_bounded() {  # run_bounded SECONDS CMD... -> stdout on success; rc 124 on timeout
  local secs="$1" outfile out rc; shift
  outfile=$(mktemp "${TMPDIR:-/tmp}/vi-bounded.XXXXXX") || return 1
  with_timeout "$secs" "$outfile" "$@"
  rc=$?
  out=$(cat "$outfile" 2>/dev/null)
  rm -f "$outfile"
  printf '%s' "$out"
  return "$rc"
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
#
# agent-supervisor#376: `mark-pr-external.sh` records a PR as authored
# OUTSIDE the lane system entirely (`cli.py pr-external`, gated by its own
# exhaustive contributor-resolution chain -- see that script's header). Not
# consulting that record here left every externally-marked PR falling
# through every ledger-lookup path below to the same `known:false` this
# function already returns for a genuinely unresolved author, so
# `merge-pr.sh` refused it with the identical "author lane unresolved"
# error whether or not anyone had ever marked it external -- the marking
# was recorded but had no effect on the gate it exists to satisfy. Checked
# FIRST, before any ledger-lookup path: a PR marked external has, by
# construction, no lane to find, so there is nothing more specific worth
# trying first.
# agent-supervisor#200: returns the FULL contributor set for a PR -- every
# lane the ledger can show contributed to it, not the single narrowed-down
# "author" this used to resolve. dispatch.sh's `--reviews-pr` author-
# exclusion guard was widened to this same shape by #190 (a fix-pass task
# dispatched against the same issue to address review findings is itself a
# contributor, not just "the author"); this file's own gate did not follow,
# so a fix-pass lane could still approve or merge its own fix HERE even
# though dispatch.sh already refuses to DISPATCH it that same review. See
# `Ledger.get_contributor_tasks_for_issue` (#190) and `contributor-issue-
# lanes` for the primitive reused below -- not re-derived a second time,
# the exact drift #108 already burned a day on.
#
# Deliberately does NOT source resolve-pr-contributors.sh (dispatch.sh's own
# copy of this chain, #308/#321): that helper's `gh pr view` call has no
# timeout, and merge-pr.sh's own hang protection (#251, `AUTHOR_LANE_GH_
# TIMEOUT_SECONDS` below) exists specifically because this file's `gh` call
# is reachable from the one path that must never block a merge decision
# forever. The ledger-side primitives (`contributor-issue-lanes`, `pr-task`,
# `contributor-pr-lanes`) are reused directly instead -- single sqlite
# queries, not `gh`, with no history of hanging (see this file's own comment
# on `VERDICT_CALL_TIMEOUT_SECONDS` for why those are left unwrapped
# elsewhere in this file).
author_lane_for() {
  local repo_full="$1" number="$2" pr_json head_ref candidates candidate issue_json
  local prefix fallback_task fallback_json outfile rc external_json external_note
  local pr_task_json pr_contrib_json contrib_json
  local contrib_lanes=() contrib_tasks=()

  _al_contrib_known() {
    local want="$1" have
    for have in "${contrib_lanes[@]+"${contrib_lanes[@]}"}"; do
      [ "$have" = "$want" ] && return 0
    done
    return 1
  }
  _al_add_contrib() {
    local lane="$1" task="$2"
    [ -n "$lane" ] || return 0
    _al_contrib_known "$lane" && return 0
    contrib_lanes+=("$lane")
    contrib_tasks+=("$task")
  }
  _al_contrib_json() {
    local json="[]" i
    for i in "${!contrib_lanes[@]}"; do
      json=$(jq -nc --argjson acc "$json" --arg lane "${contrib_lanes[$i]}" --arg task "${contrib_tasks[$i]:-}" \
        '$acc + [{lane:$lane, task:$task}]')
    done
    printf '%s' "$json"
  }
  _al_cleanup() { unset -f _al_contrib_known _al_add_contrib _al_contrib_json _al_cleanup; }

  external_json=$("$LEDGER_PYTHON" "$LEDGER_CLI" --state-dir "$STATE" pr-external --repo "$repo_full" --pr "$number" 2>/dev/null)
  if jq -e '.known == true' >/dev/null 2>&1 <<<"$external_json"; then
    external_note=$(jq -r '.note // ""' <<<"$external_json")
    _al_cleanup
    jq -nc --arg note "$external_note" \
      '{known:true, contributors:[], external:true,
        detail:("authored outside the lane system" + (if ($note|length) > 0 then " -- " + $note else "" end))}'
    return
  fi
  outfile=$(mktemp "${TMPDIR:-/tmp}/author-lane-gh.XXXXXX") || {
    _al_cleanup
    jq -nc --arg detail "independence unknown -- PR author lane unresolved: could not create a scratch file" \
      '{known:false, contributors:[], detail:$detail}'
    return
  }
  with_timeout "$AUTHOR_LANE_GH_TIMEOUT_SECONDS" "$outfile" \
    gh pr view "$number" -R "$repo_full" --json headRefName,closingIssuesReferences,commits
  rc=$?
  pr_json=$(cat "$outfile" 2>/dev/null)
  rm -f "$outfile"
  if [ "$rc" -eq 124 ]; then
    _al_cleanup
    jq -nc --arg detail "independence unknown -- PR author lane unresolved: gh pr view timed out after ${AUTHOR_LANE_GH_TIMEOUT_SECONDS}s" \
      '{known:false, contributors:[], detail:$detail}'
    return
  fi
  if [ "$rc" -ne 0 ] || [ -z "$pr_json" ]; then
    _al_cleanup
    jq -nc --arg detail "independence unknown -- PR author lane unresolved: gh pr view failed" \
      '{known:false, contributors:[], detail:$detail}'
    return
  fi
  if ! jq -e . >/dev/null 2>&1 <<<"$pr_json"; then
    _al_cleanup
    jq -nc --arg detail "independence unknown -- PR author lane unresolved: gh pr view produced unreadable JSON" \
      '{known:false, contributors:[], detail:$detail}'
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
  # Path 1&2: every non-review task the ledger holds against each candidate
  # issue -- #190's widened set, unioned across every issue this PR closes
  # (dispatch.sh's own resolve_pr_contributors does the same union).
  # agent-supervisor#146: `--repo "$repo_full"` disambiguates an issue
  # NUMBER that collides across the repos this estate tracks in parallel
  # (session-per-repo, #111) -- the same cross-repo scoping #146 gave
  # `author-issue-lane` applies equally here, since a candidate issue
  # number alone cannot tell #181 in one repo from #181 in another.
  for candidate in $candidates; do
    if issue_json=$("$LEDGER_PYTHON" "$LEDGER_CLI" --state-dir "$STATE" contributor-issue-lanes --issue "$candidate" --repo "$repo_full" 2>/dev/null) \
       && jq -e '.known == true' >/dev/null 2>&1 <<<"$issue_json"; then
      while IFS=$'\t' read -r c_lane c_task; do
        _al_add_contrib "$c_lane" "$c_task"
      done < <(jq -r '.contributors[] | "\(.lane)\t\(.task)"' <<<"$issue_json")
    fi
  done
  # agent-supervisor#415/#419: a PR with no closing-issue reference and a
  # branch name that doesn't match the legacy `lane/fix/feat/chore/docs`
  # regex (e.g. PR #400, `feat/prior-attempts`) fell through both paths
  # above to `known:false` even when the ledger already holds the real
  # contributor record for it -- `record_pr_for_task`, written by
  # `lane-done.sh` at completion, queried here through `cli.py pr-task`.
  # Folded into the contributor-set accumulation (#200) below rather than
  # returning early with a single lane: a fix-pass task recorded via
  # `pr-task` is a contributor like any other, not automatically the
  # PR's sole author.
  #
  # Path 3 (agent-supervisor#308 item 1): the explicit "task X's work opened
  # PR N" record `lane-done.sh` writes -- covers a PR with no closing-issue
  # reference the branch/commit regexes above can find.
  if pr_task_json=$("$LEDGER_PYTHON" "$LEDGER_CLI" --state-dir "$STATE" pr-task --repo "$repo_full" --pr "$number" 2>/dev/null) \
     && jq -e '.known == true' >/dev/null 2>&1 <<<"$pr_task_json"; then
    _al_add_contrib "$(jq -r '.lane' <<<"$pr_task_json")" "$(jq -r '.task // ""' <<<"$pr_task_json")"
  fi
  # Path 4 (agent-supervisor#308 item 2): the PR's own source_tasks rows,
  # asked directly by PR number -- a contributor dispatched PR-scoped rather
  # than issue-scoped (`dispatch.sh --pr`).
  if pr_contrib_json=$("$LEDGER_PYTHON" "$LEDGER_CLI" --state-dir "$STATE" contributor-pr-lanes --pr "$number" 2>/dev/null) \
     && jq -e '.known == true' >/dev/null 2>&1 <<<"$pr_contrib_json"; then
    while IFS=$'\t' read -r c_lane c_task; do
      _al_add_contrib "$c_lane" "$c_task"
    done < <(jq -r '.contributors[] | "\(.lane)\t\(.task)"' <<<"$pr_contrib_json")
  fi
  if [ "${#contrib_lanes[@]}" -gt 0 ]; then
    contrib_json=$(_al_contrib_json)
    _al_cleanup
    jq -nc --argjson contributors "$contrib_json" '{known:true, external:false, contributors:$contributors, detail:""}'
    return
  fi
  # Path 5, legacy last resort: the branch-name convention alone, kept only
  # for tasks dispatched before #117 gave every dispatch a worktree the
  # ledger can key on.
  prefix=$(repo_task_prefix "$repo_full")
  if [[ "$head_ref" =~ ^(lane|fix|feat|chore|docs)/([0-9]+)-(.+)$ ]]; then
    fallback_task="${prefix}${BASH_REMATCH[2]}-${BASH_REMATCH[3]}"
    if fallback_json=$("$LEDGER_PYTHON" "$LEDGER_CLI" --state-dir "$STATE" task-lane --task "$fallback_task" 2>/dev/null) \
       && jq -e '.known == true' >/dev/null 2>&1 <<<"$fallback_json"; then
      _al_cleanup
      jq -nc --arg lane "$(jq -r '.lane' <<<"$fallback_json")" \
             --arg task "$fallback_task" \
             '{known:true, external:false, contributors:[{lane:$lane, task:$task}], detail:""}'
      return
    fi
  fi
  _al_cleanup
  jq -nc --arg head "$head_ref" \
         --arg task "${fallback_task:-none}" \
         '{known:false, contributors:[], detail:("independence unknown -- PR author lane unresolved from ledger issue lookup or branch " + ($head|if length > 0 then . else "unknown" end) + " (task " + $task + ")")}'
}

# agent-supervisor#200: aggregates resolve_lane_relation() (#332) across
# EVERY lane in author_lane_for's now-widened contributor set, not just a
# single previously-resolved author -- the merge-time analogue of
# dispatch.sh's own contributor-exclusion loop (#190). Fails toward "same"
# over "different": a reviewer matching ANY contributor is a self-review,
# reported immediately. When no contributor matches but at least one
# comparison could not be resolved (`unknown`), that also refuses --
# independence_verdict below treats `unknown` exactly as hard as `same`, the
# same fail-closed posture this whole file holds everywhere else. Only when
# the reviewer differs from EVERY contributor, and none of those comparisons
# came back `unknown`, does this report `different`.
#
# agent-supervisor#520: before any of that, the REVIEWER's own registration
# must not be contradicted by the live tmux server it claims to be on (see
# `_lane_identity_status` above for what that means and what it deliberately
# does not cover). Checked first and short-circuiting, because a `pane_id`
# the server disagrees with is not weaker evidence of independence -- it is
# not evidence at all, and every comparison below would be reading it.
contributor_lane_relation() {  # contributor_lane_relation <author-json> <reviewer-lane> -> JSON
  local author_json="$1" reviewer_lane="$2"
  local n lane task rel overall="different" matched_lane="" matched_task="" i=0
  local identity id_status id_detail

  identity=$(_lane_identity_status "$reviewer_lane")
  id_status="${identity%%$'\t'*}"
  id_detail="${identity#*$'\t'}"
  if [ "$id_status" = "contradicted" ]; then
    jq -nc --arg lane "$reviewer_lane" --arg detail "$id_detail" \
      '{overall:"contradicted", matched_lane:$lane, matched_task:null, detail:$detail}'
    return
  fi

  n=$(jq '(.contributors // []) | length' <<<"$author_json" 2>/dev/null) || n=0
  while [ "$i" -lt "$n" ]; do
    lane=$(jq -r ".contributors[$i].lane" <<<"$author_json")
    task=$(jq -r ".contributors[$i].task // \"\"" <<<"$author_json")
    rel=$(resolve_lane_relation "$lane" "$reviewer_lane")
    if [ "$rel" = "same" ]; then
      overall="same"; matched_lane="$lane"; matched_task="$task"
      break
    elif [ "$rel" = "unknown" ] && [ "$overall" != "same" ]; then
      overall="unknown"; matched_lane="$lane"; matched_task="$task"
    fi
    i=$((i + 1))
  done
  jq -nc --arg overall "$overall" --arg lane "$matched_lane" --arg task "$matched_task" \
    '{overall:$overall,
      matched_lane: (if ($lane|length) > 0 then $lane else null end),
      matched_task: (if ($task|length) > 0 then $task else null end)}'
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
    # #251/#267/#205's own shape: verdict.py's individual `gh`/`git` calls
    # already carry their own 30s subprocess.run timeouts (verdict.py's
    # _subprocess_runner), but the PYTHON PROCESS ITSELF -- sqlite opens,
    # the ledger's own lock file -- had no outer bound. A wedged lock is not
    # a network hang, but it is still a hang, and #251's own brief asks for
    # every shell-out on the path, not just the one already caught.
    out=$(run_bounded "$VERDICT_CALL_TIMEOUT_SECONDS" "$VERDICT_PYTHON" "$HERE/verdict.py" --state-dir "$STATE" \
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
#   - the reviewer lane's own registration is not CONTRADICTED by the live
#     tmux server it names (agent-supervisor#520) -- a row recording a pane
#     that server does not have is not a weaker identity, it is a false one,
#     and it is what four of this estate's lanes were carrying when #513 was
#     filed. `unverifiable` is not a refusal here; see `_lane_identity_status`
#     above for exactly where that line is drawn and why.
# Anything else is `false` or `null`, and a caller that gates a merge on this
# must treat both identically: refuse.
independence_verdict() {  # independence_verdict <verdict-json> <author-json> <lane-rel-json>
  local v="$1" author="$2" rel="$3"
  jq -n --argjson v "$v" --argjson author "$author" --argjson rel "$rel" '
    if (($v.verdict_kind // "") | IN("comment", "ledger")) and (($v.verdict // "") | IN("approved", "rejected")) then
      if (($v.reviewer_lane // "") | length) == 0 then
        {value:null, detail:"independence unknown -- reviewer lane unresolved; comment verdicts must include Review-Lane: <lane-id>"}
      elif ($author.known == true) then
        if ($author.external == true) then
          # agent-supervisor#376: a PR marked external has, by construction,
          # no author lane to compare against the reviewer -- there is
          # nothing for $rel to say "same" about. Treated exactly like a
          # provably-different author (independence = true), carrying the
          # external note through for auditability rather than the bare
          # "independent -- author lane X" phrasing the ledger-resolved path
          # below uses.
          {value:true, detail:("independent -- PR " + $author.detail + ", reviewer lane " + $v.reviewer_lane)}
        elif ($rel.overall == "same") then
          # agent-supervisor#200: $rel.matched_lane is whichever contributor
          # in the WIDENED set (author_lane_for) matched the reviewer -- not
          # necessarily the single lane a pre-#200 caller would have named
          # "the author". A fix-pass lane approving its own fix reports its
          # own lane here, not the original author.
          {value:false, detail:("NOT independent -- author lane " + $rel.matched_lane + " reviewed its own PR"
            + (if ($v.reviewer_lane != $rel.matched_lane) then " (reviewed as " + $v.reviewer_lane + " -- the same window, renamed session)" else "" end))}
        elif ($rel.overall == "contradicted") then
          # agent-supervisor#520: the reviewer lane IS registered, but the
          # tmux server named by its own row disagrees with that row -- a
          # stale or never-measured registration. Reported as value:false,
          # not null: this is a positive finding about the evidence, not an
          # absence of it, and the remedy is concrete (re-register from the
          # pane belonging to that lane, then obtain a fresh review).
          {value:false, detail:("NOT independent -- reviewer lane " + $v.reviewer_lane
            + " has a registration the live tmux server contradicts: " + ($rel.detail // "no detail")
            + " -- re-register from the pane belonging to that lane (register-lane-self.sh) and obtain a fresh review")}
        elif ($rel.overall == "different") then
          {value:true, detail:("independent -- author lane"
            + (if (($author.contributors // []) | length) > 1 then "s" else "" end) + " "
            + (($author.contributors // []) | map(.lane) | join(", "))
            + ", reviewer lane " + $v.reviewer_lane)}
        else
          {value:null, detail:("independence unknown -- author lane " + ($rel.matched_lane // "?") + " and reviewer lane " + $v.reviewer_lane + " are not comparable lane ids")}
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
