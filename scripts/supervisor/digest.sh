#!/usr/bin/env bash
# One command that answers "what is the state of the estate right now".
#
# WHY: the Director reconstructs the same picture every tick from ~26 separate
# subprocess round-trips -- watchdog.status, pgrep, the poller status, lanes.sh,
# then five `gh` calls per open PR. That reasoning is identical every time and
# it is paid for in the most expensive tier in the estate.
#
# Every judgement moved out of a model and into a script is a permanent saving,
# and this is the largest remaining one: the Director's per-tick state read.
#
# The estate's own research (docs/hierarchy-naming-57.md) prices an LLM-backed
# coordination tier at 30-50% extra tokens; the answer here is not to remove the
# tier but to stop making it re-derive facts a script can hand it.
#
# Usage:
#   digest.sh              human-readable summary
#   digest.sh --json       one JSON object
#
# REQUIRES: jq. This is the first script in scripts/supervisor/ to need a
# binary the rest of the estate does not already assume -- every other script
# here is plain bash, and the `gh ... --jq` calls in watchdog.sh and
# loop-tick.md use gh's own bundled implementation, which is present with
# standalone jq absent. #139/#175 are actively looking at running this estate
# on other machines, so the dependency is stated here rather than discovered
# by a reader on a machine where it is missing. A missing jq is refused up
# front (below), named, and exits 1 -- it never degrades to a silent digest.
#
# Exit 0 when the digest was produced. Exit 1 only when it could not be built at
# all -- a partial digest is still emitted, with the failures NAMED. This matters
# more here than usual: this file is what a reader trusts instead of looking, so
# a section that could not be read must say so rather than appear empty. An
# empty `prs` list and an unreachable GitHub must not look the same.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
SESSION="${LANES_SESSION:-agent-dotfiles}"
REPOS="${DIGEST_REPOS:-agent-dotfiles agent-supervisor skills skills-private agent-evals}"
OWNER="${DIGEST_OWNER:-jonhill90}"
SINCE="${DIGEST_SINCE:-}"
MODE="${1:-}"
# The verdict source is an adapter (agent-dotfiles#203, scripts/supervisor/
# verdict.py) -- this script holds no knowledge of where the answer comes
# from, only which source name(s) to ask and a python3 binary to ask them
# with. DIGEST_VERDICT_BIN lets a test replace the whole call with a stub
# that takes --repo/--number and prints {"verdict":...} on stdout, the same
# override shape as DIGEST_LANES_BIN above.
#
# Default is "github", not "ledger" (agent-dotfiles#214): LedgerVerdictSource
# is a table nothing writes -- record_pr_verdict has no caller anywhere in
# this estate outside its own tests -- so "ledger" as a default always reads
# "none". GithubReviewVerdictSource works today with zero further wiring;
# under one GitHub identity the only verdict it can see is a formal
# `--request-changes` (#203), so it reports real rejections truthfully and
# "none" for everything else, which is honest about what it cannot see.
VERDICT_SOURCE="${DIGEST_VERDICT_SOURCE:-github}"
VERDICT_PYTHON="${DIGEST_VERDICT_PYTHON:-python3}"
VERDICT_BIN="${DIGEST_VERDICT_BIN:-}"
LEDGER_PYTHON="${DIGEST_LEDGER_PYTHON:-python3}"
LEDGER_CLI="${DIGEST_LEDGER_CLI:-$HERE/cli.py}"
RECONCILE_IDLE_AFTER="${DIGEST_RECONCILE_IDLE_AFTER:-300}"

# jq is the only dependency this script does not already share with the rest
# of the estate: watchdog.sh and loop-tick.md both use `gh ... --jq`, which is
# gh's own bundled implementation and works with standalone jq absent. This is
# the first script here to shell out to jq directly, so it is guarded like
# every other precondition -- unguarded, `--json` printed a zero-byte payload
# (8 lines of "jq: command not found" on stderr, nothing on stdout), which
# directly violates the "a partial digest is still emitted, with the failures
# named" contract above.
if ! command -v jq >/dev/null 2>&1; then
  if [ "$MODE" = "--json" ]; then
    printf '{"errors":["jq is required but not installed"],"ok":false}\n'
  else
    echo "ERRORS (this digest is INCOMPLETE):"
    echo "  ! jq is required but not installed"
  fi
  exit 1
fi

ERRORS=()
note_error() { ERRORS+=("$1"); }

# Splits only the LABEL prefix off a "label: value" status-file line, not
# every colon in it -- a plain `awk -F': *'` field split truncates any value
# containing its own colon. Reproduced live against watchdog.status:
# `checked:  2026-08-12T03:10:31Z` read back as `2026-08-12T03`.
status_field() {
  local file="$1" label="$2"
  awk -v label="$label" '
    $0 ~ "^" label ":" { sub("^" label ": *", ""); print; exit }
  ' "$file"
}

# --- watchdog -------------------------------------------------------------
WD_FILE="$STATE/watchdog.status"
if [ -r "$WD_FILE" ]; then
  wd_state=$(status_field "$WD_FILE" state)
  wd_checked=$(status_field "$WD_FILE" checked)
  wd_restarts=$(status_field "$WD_FILE" restarts)
  wd_heartbeat=$(status_field "$WD_FILE" heartbeat)
  wd_recovery=$(status_field "$WD_FILE" recovery)
  wd_advance=$(status_field "$WD_FILE" advance)
else
  wd_state="UNREADABLE"; wd_checked=""; wd_restarts=""; wd_heartbeat=""
  wd_recovery=""; wd_advance=""
  note_error "watchdog.status unreadable at $WD_FILE"
fi

# agent-supervisor#41 (agent-supervisor#28): watchdog.status's `recovery:`
# line is poller-recover.sh's own OUTCOME, not the poller process being up --
# `pgrep inbox-poll.sh` and `state: ok` below both stayed green through 37
# straight recovery failures because nothing here read this line at all. A
# recovery attempt that found nothing to fix stays silent (recovery_note in
# watchdog.sh never writes an "ok" line for that case); only a recorded
# failure is degraded, so a healthy estate still reads healthy.
if [[ "$wd_recovery" == failed* ]]; then
  note_error "poller recovery: $wd_recovery"
fi

# agent-supervisor#41 (agent-supervisor#57): watchdog.status's `advance:` line
# can read "current" or "advanced" -- both trivially successful outcomes of
# the git-advance step -- while a poller-restart-request folded into that
# SAME tick failed underneath it (advance-live.sh's maybe_restart_poller).
# "the tick ran and reported success" must not stand in for "everything the
# tick attempted succeeded" (measured 2026-08-13: this exact text repeated on
# every watchdog run for hours with nothing surfacing it). Matched on the raw
# text so it fires no matter which top-level advance outcome carries it.
if [[ "$wd_advance" == *"could not be started"* ]]; then
  note_error "watchdog advance: $wd_advance"
fi

# --- poller ---------------------------------------------------------------
# Liveness by process, health by its own status file. They answer different
# questions: a wedged poller is alive and not listening, and only the second
# catches that.
if pgrep -f "inbox-poll.sh" >/dev/null 2>&1; then poller_alive=true; else poller_alive=false; fi
PL_FILE="$STATE/inbox-poll.status"
if [ -r "$PL_FILE" ]; then
  poller_state=$(status_field "$PL_FILE" state)
  poller_checked=$(status_field "$PL_FILE" checked)
else
  poller_state="UNREADABLE"; poller_checked=""
  [ "$poller_alive" = true ] && note_error "inbox-poll.status unreadable while the process is running"
fi

# --- director inbox --------------------------------------------------------
# agent-supervisor#34: the Director only ever saw a queued Telegram message
# by running a tick, and a tick is exactly what a busy pane can go hours
# without -- 9 messages, 12 hours, nothing downstream able to tell "queued"
# from "lost". This section is delivered-by-construction, the same shape as
# every other field here: it costs a subprocess call regardless of whether
# the Director's pane has ever gone idle, so reading THIS digest is itself a
# delivery of the pending count and the oldest message's age, independent of
# `director-route.sh`'s nudge succeeding.
INBOX_BIN="${DIGEST_INBOX_BIN:-$HERE/director-inbox.sh}"
INBOX_STALE_SECONDS="${DIGEST_INBOX_STALE_SECONDS:-1800}"
inbox_json=$("$INBOX_BIN" stats 2>/dev/null)
if ! jq -e . >/dev/null 2>&1 <<<"$inbox_json"; then
  inbox_json='{"pending":null,"oldest_at":null,"oldest_age_s":null}'
  note_error "director-inbox.sh stats produced no readable output"
fi
inbox_pending=$(jq -r '.pending // "unknown"' <<<"$inbox_json")
inbox_oldest_age=$(jq -r '.oldest_age_s // empty' <<<"$inbox_json")
inbox_stale=false
if [ -n "$inbox_oldest_age" ] && [ "$inbox_oldest_age" -ge "$INBOX_STALE_SECONDS" ] 2>/dev/null; then
  inbox_stale=true
  note_error "director inbox: oldest pending message is ${inbox_oldest_age}s old (>= ${INBOX_STALE_SECONDS}s) -- not yet delivered to the Director"
fi

# --- lanes ----------------------------------------------------------------
# Overridable so a test can exercise a lanes.sh shape (e.g. header row, no
# data rows) without needing a real tmux session.
LANES_BIN="${DIGEST_LANES_BIN:-$HERE/lanes.sh}"
LANES_OUT=$("$LANES_BIN" "$SESSION" 2>/dev/null)
lanes_rc=$?
# Two distinct failure shapes, both invisible to a bare `[ -z ]` check:
# lanes.sh exiting non-zero (session genuinely gone, caught below already by
# emptiness in practice, but not guaranteed by contract), and lanes.sh exiting
# 0 with ONLY its header row -- a real, if narrow, tmux hiccup shape. Either
# one used to render as "the estate is idle in every category", indistinguishable
# from a genuinely idle estate.
lane_rows=$(printf '%s\n' "$LANES_OUT" | awk 'NR>1 && NF' | wc -l | tr -d ' ')
if [ "$lanes_rc" -ne 0 ] || [ -z "$LANES_OUT" ]; then
  note_error "lanes.sh returned nothing for session '$SESSION'"
elif [ "$lane_rows" -eq 0 ]; then
  note_error "lanes.sh returned no lane rows for session '$SESSION' (header only)"
fi
lane_line() { awk -v s="$1" 'NR>1 && $NF==s {print $2}' <<<"$LANES_OUT" | paste -sd, - ; }

# --- delivered-vs-pane reconciliation ------------------------------------
# agent-supervisor#36. `dispatch.sh` reads the ledger and `lanes.sh` reads the
# pane; when a worker finishes but never signals, both can be truthful and
# still disagree forever. This section surfaces only the cheap, observable
# disagreement: a task still `delivered` with no `completed_at`, whose lane's
# pane is now `free` and whose tmux-derived `idle_seconds` exceeds the
# threshold. It deliberately does NOT complete anything; the result note/path
# belongs in `record-completion` after a human reads the pane.
DELIVERED_OPEN_JSON="[]"
RECONCILIATION_JSON="[]"
if delivered_out=$("$LEDGER_PYTHON" "$LEDGER_CLI" --state-dir "$STATE" delivered-open 2>/dev/null); then
  if jq -e . >/dev/null 2>&1 <<<"$delivered_out"; then
    DELIVERED_OPEN_JSON=$(jq -c '.tasks // []' <<<"$delivered_out")
  else
    note_error "delivered-open produced unreadable JSON"
  fi
else
  note_error "delivered-open failed for ledger at $STATE"
fi
LANES_JSON_OUT=$("$LANES_BIN" --json "$SESSION" 2>/dev/null)
lanes_json_rc=$?
if [ "$lanes_json_rc" -ne 0 ] || ! jq -e . >/dev/null 2>&1 <<<"$LANES_JSON_OUT"; then
  note_error "lanes.sh --json failed for session '$SESSION' -- delivered/idle reconciliation omitted"
  LANES_JSON_OUT="[]"
fi
RECONCILIATION_JSON=$(jq -nc \
  --arg session "$SESSION" \
  --arg ledger_python "$LEDGER_PYTHON" \
  --arg ledger_cli "$LEDGER_CLI" \
  --arg state "$STATE" \
  --argjson threshold "$RECONCILE_IDLE_AFTER" \
  --argjson tasks "$DELIVERED_OPEN_JSON" \
  --argjson lanes "$LANES_JSON_OUT" '
  [
    $tasks[] as $task
    | ($lanes[] | select(($session + ":" + (.window|tostring)) == $task.lane)) as $pane
    | select($pane.state == "free")
    | select(($pane.idle_seconds // 0) >= $threshold)
    | {
        task: $task.id,
        lane: $task.lane,
        idle_seconds: ($pane.idle_seconds // 0),
        threshold_seconds: $threshold,
        window: $pane.window,
        window_id: $pane.window_id,
        name: $pane.name,
        recovery: ($ledger_python + " " + $ledger_cli + " --state-dir " + $state + " record-completion --task " + $task.id + " --note <note>")
      }
  ]')

# Resolves one PR's verdict through the adapter. Always prints SOME JSON --
# a source failure or a missing/broken stub must read as {"verdict":"unknown"},
# never as empty output that a caller could mistake for "no verdict field".
#
# head_sha is the PR's current headRefOid (already fetched below via `gh pr
# list`) -- passed through so a source can tell a review or ledger record
# filed against an OLDER commit apart from one that answers for this head
# (agent-dotfiles#218). Without it every verdict source is blind to a push
# that moved the head after the verdict was filed.
verdict_for() {
  local repo_full="$1" number="$2" head_sha="$3" out
  if [ -n "$VERDICT_BIN" ]; then
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
# branch name as a last-resort task-id hint. Missing data is not an error in
# the digest; it only makes verdict independence unknown for that PR.
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
    if issue_json=$("$LEDGER_PYTHON" "$LEDGER_CLI" --state-dir "$STATE" author-issue-lane --issue "$candidate" 2>/dev/null) \
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

# --- pull requests --------------------------------------------------------
# One `gh` call per repo for the PR list, then one `gh run list` per PR,
# scoped to that PR's own branch.
#
# It used to be one `gh run list --limit 40` per REPO, matched against every
# PR by branch name. That call is not scoped to a branch -- it's the 40 most
# recent runs across every branch in the repo. Measured against this repo
# 2026-08-12: ~1 run every 11 minutes sustained, which exhausts a 40-slot
# window in about 7 hours -- less than a PR commonly sits open under review.
# Once a PR's run ages out of the window, `$r` was null and `run_conclusion`
# read "NO RUN" -- indistinguishable from #149's real conflicted-branch case,
# which this field exists to name. `--branch` costs one more call per PR but
# reads the actual latest run for that branch, not a stale slice of a shared
# window.
PR_JSON="[]"
for repo in $REPOS; do
  list=$(gh pr list -R "$OWNER/$repo" --state open \
        --json number,title,headRefOid,headRefName,mergeStateStatus 2>/dev/null) || {
    note_error "gh pr list failed for $OWNER/$repo -- its PRs are NOT in this digest"
    continue
  }
  [ -z "$list" ] && list="[]"
  pr_count=$(jq 'length' <<<"$list")
  for ((i = 0; i < pr_count; i++)); do
    p=$(jq -c ".[$i]" <<<"$list")
    branch=$(jq -r '.headRefName' <<<"$p")
    num=$(jq -r '.number' <<<"$p")
    head_oid=$(jq -r '.headRefOid' <<<"$p")
    run=$(gh run list -R "$OWNER/$repo" --branch "$branch" --limit 1 \
          --json headSha,conclusion 2>/dev/null) || {
      note_error "gh run list failed for $OWNER/$repo#$num -- CI status omitted"
      run="[]"
    }
    [ -z "$run" ] && run="[]"
    r=$(jq -c '.[0] // {}' <<<"$run")
    # Verdict comes from the adapter, not from this jq -- see verdict_for()
    # above and scripts/supervisor/verdict.py. It used to regex-match the
    # last PR comment's prose here, which read "I cannot approve this, it is
    # unsafe." as an APPROVE (agent-dotfiles#203).
    v=$(verdict_for "$OWNER/$repo" "$num" "$head_oid")
    author_lane=$(author_lane_for "$OWNER/$repo" "$num")
    entry=$(jq -n --argjson p "$p" --argjson r "$r" --arg repo "$repo" --argjson v "$v" --argjson author "$author_lane" '
      def independence:
        if (($v.verdict_kind // "") | IN("comment", "ledger")) and (($v.verdict // "") | IN("approved", "rejected")) then
          if (($v.reviewer_lane // "") | length) == 0 then
            {value:null, detail:"independence unknown -- reviewer lane unresolved; comment verdicts must include Review-Lane: <lane-id>"}
          elif ($author.known == true) then
            if ($v.reviewer_lane == $author.lane) then
              {value:false, detail:("NOT independent -- author lane " + $author.lane + " reviewed its own PR")}
            else
              {value:true, detail:("independent -- author lane " + $author.lane + ", reviewer lane " + $v.reviewer_lane)}
            end
          else
            {value:null, detail:$author.detail}
          end
        else
          {value:null, detail:""}
        end;
      independence as $ind |
      {
        repo: $repo, number: $p.number, title: $p.title,
        head: $p.headRefOid[0:8],
        run_sha: ($r.headSha // "" | .[0:8]),
        run_conclusion: ($r.conclusion // "NO RUN"),
        # the check is stale unless the run was for THIS head -- the field the
        # UI does not distinguish, and a conflicted branch produces no run at all
        ci_is_current: (($r.headSha // "") == $p.headRefOid),
        merge_state: $p.mergeStateStatus,
        verdict: ($v.verdict // "unknown"),
        verdict_independent: $ind.value,
        verdict_detail: (
          ($v.detail // "") +
          (if (($v.detail // "") | length) > 0 and ($ind.detail | length) > 0 then "; " else "" end) +
          $ind.detail
        )
      }') || { note_error "jq failed assembling $OWNER/$repo#$num"; continue; }
    PR_JSON=$(jq -nc --argjson acc "$PR_JSON" --argjson e "$entry" '$acc + [$e]')
  done
done

# --- merges since ---------------------------------------------------------
MERGED_JSON="[]"
if [ -n "$SINCE" ]; then
  for repo in $REPOS; do
    m=$(gh pr list -R "$OWNER/$repo" --state merged --limit 30 \
        --json number,title,mergedAt 2>/dev/null) || { note_error "merged-list failed for $repo"; continue; }
    MERGED_JSON=$(jq -n --argjson acc "$MERGED_JSON" --argjson m "${m:-[]}" \
      --arg repo "$repo" --arg since "$SINCE" '
      $acc + [ $m[] | select(.mergedAt > $since) | {repo:$repo, number:.number, title:.title} ]')
  done
fi

ERR_JSON=$(printf '%s\n' "${ERRORS[@]+"${ERRORS[@]}"}" | jq -R . | jq -s 'map(select(. != ""))')

DIGEST=$(jq -n \
  --arg checked "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg wd_state "$wd_state" --arg wd_checked "$wd_checked" \
  --arg wd_restarts "$wd_restarts" --arg wd_heartbeat "$wd_heartbeat" \
  --arg wd_recovery "$wd_recovery" --arg wd_advance "$wd_advance" \
  --argjson poller_alive "$poller_alive" --arg poller_state "$poller_state" \
  --arg poller_checked "$poller_checked" \
  --argjson inbox "$inbox_json" --argjson inbox_stale "$inbox_stale" \
  --arg free "$(lane_line free)" --arg busy "$(lane_line busy)" \
  --arg blocked "$(lane_line blocked)" --arg menu "$(lane_line menu-blocked)" \
  --arg dead "$(lane_line dead)" --arg service "$(lane_line service)" \
  --arg stale "$(lane_line stale)" \
  --arg unknown "$(lane_line unknown)" \
  --argjson reconciliation "$RECONCILIATION_JSON" \
  --argjson prs "$PR_JSON" --argjson merged "$MERGED_JSON" --argjson errors "$ERR_JSON" '
  {checked: $checked,
   watchdog: {state:$wd_state, checked:$wd_checked, restarts:$wd_restarts, heartbeat:$wd_heartbeat,
              recovery:$wd_recovery, advance:$wd_advance},
   poller: {alive:$poller_alive, state:$poller_state, checked:$poller_checked},
   director_inbox: ($inbox + {stale: $inbox_stale}),
   lanes: {free:$free, busy:$busy, blocked:$blocked, menu_blocked:$menu,
           dead:$dead, stale:$stale, service:$service, unknown:$unknown},
   reconciliation: {delivered_idle: $reconciliation},
   prs: $prs, merged_since: $merged, errors: $errors,
   ok: ($errors | length == 0)}')

if [ "$MODE" = "--json" ]; then
  printf '%s\n' "$DIGEST"
else
  jq -r '
    "watchdog: \(.watchdog.state)  restarts=\(.watchdog.restarts)  \(.watchdog.heartbeat)",
    # Printed only when there is an outcome to report -- a recovery attempt
    # that found nothing to fix, or a tick with no poller-restart-request in
    # it, writes no line here, same as .watchdog.heartbeat above. Present
    # means an OUTCOME was recorded, not merely that the process ticked.
    (if (.watchdog.recovery|length) > 0 then "          recovery: \(.watchdog.recovery)" else empty end),
    (if (.watchdog.advance|length) > 0 then "          advance:  \(.watchdog.advance)" else empty end),
    "poller:   alive=\(.poller.alive) state=\(.poller.state)",
    # Printed every time, not only when stale -- this line IS the delivery
    # (agent-supervisor#34): a reader who checks this digest has seen the
    # pending count and the oldest message age regardless of whether the
    # Director pane was ever caught idle to nudge.
    "inbox:    pending=\(.director_inbox.pending // 0) oldest_age_s=\(.director_inbox.oldest_age_s // "n/a")\(if .director_inbox.stale then " [STALE - undelivered >= " + (.director_inbox.oldest_age_s|tostring) + "s]" else "" end)",
    "lanes:    free=[\(.lanes.free)] busy=[\(.lanes.busy)]",
    "          blocked=[\(.lanes.blocked)] menu=[\(.lanes.menu_blocked)] dead=[\(.lanes.dead)]",
    # #237: a stale lane is a dead one whose window name still claims a task.
    # Printed on its own line rather than folded into `dead` because the
    # action differs -- restore.sh, and do not believe the name.
    "          stale=[\(.lanes.stale)]",
    (if (.reconciliation.delivered_idle|length) > 0 then "reconcile:" else empty end),
    (.reconciliation.delivered_idle[] | "  delivered-open \(.task) lane=\(.lane) idle=\(.idle_seconds)s; inspect pane, then record-completion --task \(.task)"),
    (if (.prs|length) == 0 then "prs:      none open" else "prs:" end),
    # Three distinct CI states, not two: no run at all, a run that failed, and
    # a run that passed but is not for this head. Collapsing "no run" and
    # "stale" cost a real investigation (#149) -- the STALE annotation names a
    # run to distrust, so it is only printed when a run actually exists.
    # A verdict of "unknown" caused by a stale review/ledger record
    # (agent-dotfiles#218) is otherwise indistinguishable here from any other
    # unreadable-source "unknown" -- the detail names which commit the
    # verdict was actually filed against, traceable against \(.head) above.
    # The detail prints for EVERY verdict that carries one, not only for
    # "unknown" (agent-dotfiles#229). It used to be gated on `.verdict ==
    # "unknown"`, which silently dropped the basis of an approved/rejected
    # verdict -- including one #226 promoted across a rebase, so a
    # rebase-promoted approval rendered identically to a review filed at the
    # literal current head. A rule that shows the basis only sometimes is a
    # rule the next reader has to learn, and its absence then reads as
    # "nothing to say" -- which is the failure #226 and #229 are both about.
    (.prs[] | "  \(.repo)#\(.number) ci=\(.run_conclusion)\(if (.ci_is_current or .run_conclusion == "NO RUN") then "" else " [STALE - run is for \(.run_sha), head is \(.head)]" end) \(.merge_state) verdict=\(.verdict)\(if (.verdict_detail|length) > 0 then " (\(.verdict_detail))" else "" end)"),
    (if (.merged_since|length) > 0 then "merged:" else empty end),
    (.merged_since[] | "  \(.repo)#\(.number) \(.title[0:52])"),
    (if (.errors|length) > 0 then "ERRORS (this digest is INCOMPLETE):" else empty end),
    (.errors[] | "  ! \(.)")
  ' <<<"$DIGEST"
fi

[ "${#ERRORS[@]}" -eq 0 ]
