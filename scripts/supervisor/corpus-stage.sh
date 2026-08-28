#!/usr/bin/env bash
# agent-estate#735 (decided on #721): the loop-tick STAGING step for the
# prompt corpus -- extract-and-stage only, NEVER judge. `itemize_prompts.py
# --extract` is a pure SQL read ("picks nothing, decides nothing" -- its own
# docstring), so this is safe to run every tick regardless of quota state: it
# spends no model tokens and writes no `weight`/`kind` judgement to the
# corpus. Judging remains a dispatched, reviewable lane via `--load` --
# nothing in this script calls `--load`, under any conditions, and it must
# stay that way (#721's decision is not this issue's to revisit).
#
# Two jobs, both mechanical:
#
#   1. Stage the current extract batch to a fixed path so the next judging
#      dispatch can read it directly instead of re-running the query.
#      Overwriting the staging file every tick is safe and drops nothing:
#      `--extract` selects EVERY prompt with no `items` row (a LEFT JOIN
#      against `items`, not a "already staged" flag anywhere), so a prompt
#      omitted from a prior batch -- because the judging lane never ran, or
#      ran only partway -- is still selected on the very next extract. See
#      `itemize_prompts.py`'s own docstring and agent-estate#721.
#
#   2. Report whether the unjudged backlog has crossed a loud-absence
#      threshold, via exit code, so the caller (director-loop.sh) can
#      escalate through the SAME alarm path it already uses for its other
#      tick-level incidents (quota/live/stale-target -- `send_takeover_alarm`
#      + `notify.sh`), rather than inventing a second escalation idiom.
#
# THRESHOLD JUSTIFICATION (agent-estate#721/#735), by measurement, not a
# round number:
#
#   The incident that motivated this issue: a weight=hard directive sat
#   unjudged for FOUR DAYS (345600s) before anyone noticed, because nothing
#   watched the age of the oldest unitemised prompt -- a healthy JUDGING
#   half says nothing about a growing backlog the same way #687's
#   `capture_health` view found a healthy CAPTURE half says nothing about a
#   dead one.
#
#   Measured accrual rate, from that same issue (#721): 14 prompts captured
#   in under two hours of ordinary loop operation -- roughly 7/hour.
#
#   AGE threshold: 24 hours. That is a 4x safety margin under the 96-hour
#   incident -- it fires at 1 day old, not 4 -- while still tolerating a
#   judging dispatch that runs roughly once a day rather than continuously.
#   A threshold that would not have caught the 96-hour incident is the wrong
#   threshold (this issue's own words); 24h catches it with 72 hours to
#   spare.
#
#   COUNT threshold: DERIVED from the same accrual rate rather than picked
#   separately -- accrual_rate * age_threshold_hours = 7 * 24 = 168. This is
#   the backlog size the age threshold implies would accumulate in a day at
#   the observed rate, so it also catches a count-only spike (staging itself
#   broken, capture running hot) even when the OLDEST item is still fresh --
#   a case the age check alone would miss.
#
#   Either crossing the line is loud on its own; both are checked every run.
#
# Exit codes:
#   0  staged; backlog is under both thresholds
#   1  staged; backlog crossed the age or count threshold -- caller should escalate
#   2  itemize_prompts.py --extract itself failed -- an INSTRUMENT failure,
#      never read as "empty backlog" (a query that fails looks exactly like
#      an empty one unless the failure is reported separately -- see the
#      brief's "positive control" requirement)
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
ITEMIZE_PROMPTS="${ITEMIZE_PROMPTS:-$HERE/itemize_prompts.py}"
CORPUS_STAGE_FILE="${CORPUS_STAGE_FILE:-$STATE/corpus-stage.json}"
CORPUS_STAGE_AGE_THRESHOLD_HOURS="${CORPUS_STAGE_AGE_THRESHOLD_HOURS:-24}"
CORPUS_ACCRUAL_PER_HOUR="${CORPUS_ACCRUAL_PER_HOUR:-7}"
CORPUS_STAGE_COUNT_THRESHOLD="${CORPUS_STAGE_COUNT_THRESHOLD:-$(( CORPUS_ACCRUAL_PER_HOUR * CORPUS_STAGE_AGE_THRESHOLD_HOURS ))}"

log() { printf '%s corpus-stage: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*" >&2; }

mkdir -p "$STATE" 2>/dev/null || true

extract_out=$(python3 "$ITEMIZE_PROMPTS" --state-dir "$STATE" --extract 2>&1)
extract_rc=$?
if [ "$extract_rc" -ne 0 ]; then
  log "itemize_prompts.py --extract failed (rc=$extract_rc) -- treating as an instrument failure, not an empty backlog"
  while IFS= read -r line; do log "  $line"; done <<<"$extract_out"
  exit 2
fi

tmp_file="$STATE/.corpus-stage.json.tmp.$$"
printf '%s' "$extract_out" > "$tmp_file" && mv "$tmp_file" "$CORPUS_STAGE_FILE"

# count + oldest-unjudged-prompt age, both derived from the SAME staged
# batch this just wrote -- no second query against the ledger, so the
# metrics can never disagree with what was actually staged.
metrics=$(python3 - "$CORPUS_STAGE_FILE" <<'PY'
import json
import sys
import time

with open(sys.argv[1]) as fh:
    rows = json.load(fh)
count = len(rows)
if rows:
    oldest_at = min(int(row["at"]) for row in rows)
    age_seconds = max(0, int(time.time()) - oldest_at)
else:
    age_seconds = 0
print(f"{count} {age_seconds}")
PY
) || { log "could not compute count/age from $CORPUS_STAGE_FILE -- treating as an instrument failure"; exit 2; }
read -r count oldest_age_seconds <<<"$metrics"

age_threshold_seconds=$(( CORPUS_STAGE_AGE_THRESHOLD_HOURS * 3600 ))
age_hours_display=$(( oldest_age_seconds / 3600 ))

log "staged $count unitemised prompt(s) to $CORPUS_STAGE_FILE; oldest is ${age_hours_display}h old (age threshold ${CORPUS_STAGE_AGE_THRESHOLD_HOURS}h / ${age_threshold_seconds}s, count threshold $CORPUS_STAGE_COUNT_THRESHOLD)"

# Machine-readable line on stdout, separate from the log() lines above (which
# go to stderr) -- the caller parses this one line rather than grepping logs.
echo "count=$count oldest_age_seconds=$oldest_age_seconds age_threshold_seconds=$age_threshold_seconds count_threshold=$CORPUS_STAGE_COUNT_THRESHOLD"

if [ "$count" -gt "$CORPUS_STAGE_COUNT_THRESHOLD" ] || [ "$oldest_age_seconds" -gt "$age_threshold_seconds" ]; then
  log "LOUD: unjudged backlog crossed its threshold -- count=$count (threshold $CORPUS_STAGE_COUNT_THRESHOLD), oldest age=${oldest_age_seconds}s (threshold ${age_threshold_seconds}s)"
  exit 1
fi
exit 0
