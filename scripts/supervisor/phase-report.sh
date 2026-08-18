#!/usr/bin/env bash
# The phase report Jon reads every 30 minutes alongside closed-report.sh.
#
# WHY. Jon, 2026-08-18: "And also send me a phase report at that time as well.
# That way i can see progress."
#
# Progress, not activity. The distinction is the whole design: this estate can
# generate motion indefinitely -- panes busy, lanes claimed, commits landing --
# while the pile of unstarted work does not move. So the headline number here is
# deliberately the one that is least flattering.
#
# DERIVED FROM LIVE STATE, NOT FROM PROSE. PHASES.md is 1800 lines of narrative
# and a parser over it would report what someone last wrote down rather than
# what is true. Every number below comes from GitHub or the ledger, measured at
# send time.
#
# THE SHAPE IS ALREADY SPECIFIED. PHASES.md:732 -- "sections: SHIPPED · IN
# FLIGHT · NOT STARTED · DROPPED-WITH-REASONS" -- and :741 -- "Every report to
# Jon names the NOT STARTED count, so the pile stays visible". This implements
# that, rather than inventing a new format.
#
# NO MODEL IN THIS PATH.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
LEDGER="$STATE/ledger.sqlite3"
REPOS="${PHASE_REPORT_REPOS:-jonhill90/agent-supervisor jonhill90/agent-dotfiles jonhill90/agent-tui jonhill90/skills jonhill90/agent-evals}"
DRY="${PHASE_REPORT_DRY:-0}"

while [ $# -gt 0 ]; do
  case "$1" in
    --dry) DRY=1; shift ;;
    *) shift ;;
  esac
done

log() { printf '%s phase-report: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*"; }

now_iso="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
today="${now_iso%%T*}"

open_total=0; pr_total=0; closed_today_total=0
per_repo=""; unreadable=""

for repo in $REPOS; do
  short="${repo#*/}"
  oi=$(gh issue list --repo "$repo" --state open  --limit 200 --json number --jq 'length' 2>/dev/null)
  op=$(gh pr    list --repo "$repo" --state open  --limit 100 --json number --jq 'length' 2>/dev/null)
  ct=$(gh issue list --repo "$repo" --state closed --limit 100 --json closedAt \
         --jq "[.[] | select(.closedAt >= \"${today}T00:00:00Z\")] | length" 2>/dev/null)

  case "${oi}${op}${ct}" in
    ''|*[!0-9]*)
      # An unreadable repo is named, never folded into the totals as a zero.
      unreadable="${unreadable}${short} "
      per_repo="${per_repo}  ${short}: UNREADABLE
"
      continue ;;
  esac

  open_total=$((open_total + oi))
  pr_total=$((pr_total + op))
  closed_today_total=$((closed_today_total + ct))
  per_repo="${per_repo}  ${short}: ${oi} open, ${op} PRs, ${ct} closed today
"
done

# IN FLIGHT, from the ledger rather than from pane appearance. A pane that looks
# busy may be waiting on a prompt, and a claimed lane whose process died still
# reads busy from outside -- so count what was actually delivered and not yet
# accepted, bounded by recency. An unbounded count reads every zombie row from
# every dead lane the estate has ever had (measured: 18 vs 1).
in_flight="?"
if [ -f "$LEDGER" ]; then
  in_flight=$(sqlite3 "$LEDGER" \
    "SELECT COUNT(*) FROM tasks
      WHERE delivered_at IS NOT NULL
        AND accepted_at IS NULL
        AND (status IS NULL OR status NOT IN ('cancelled','complete'))
        AND delivered_at > strftime('%s','now') - 21600;" 2>/dev/null) || in_flight="?"
fi

# Lane capacity, live. Free lanes with work waiting is the actionable number --
# it is the one that says the estate is idling rather than saturated.
free_lanes=0; busy_lanes=0; blocked_lanes=0
for sess in agent-supervisor agent-dotfiles agent-tui skills; do
  out=$(bash "$HERE/lanes.sh" "$sess" 2>/dev/null) || continue
  free_lanes=$((free_lanes    + $(printf '%s\n' "$out" | grep -c ' free$')))
  busy_lanes=$((busy_lanes    + $(printf '%s\n' "$out" | grep -c ' busy$')))
  blocked_lanes=$((blocked_lanes + $(printf '%s\n' "$out" | grep -c 'menu-blocked')))
done

# --- THE CORPUS. Jon, 2026-08-18: "I want stuff from the corpus in this report
# as well. I know we have it itemize but we dont have issues or task or
# parameters or something from it."
#
# He is right, and the schema proves it rather than merely suggesting it:
# `items` has no issue column and no task column. Measured -- 3,673 prompts
# became 4,000+ items and then stopped. The corpus is a closed loop. Nothing he
# has ever said becomes work unless a human or an agent happens to read it back.
#
# So these numbers are not decoration next to the GitHub ones. They are the
# SECOND backlog -- the one made entirely of his own words -- and it is invisible
# today. `open` here means: he said it, it was itemised, and nothing was done.
corpus_open="?"; corpus_unack="?"; corpus_params="?"; corpus_q="?"; corpus_conflicts="?"
if [ -f "$LEDGER" ]; then
  corpus_open=$(sqlite3   "$LEDGER" "SELECT COUNT(*) FROM items WHERE status='open' AND weight='hard';" 2>/dev/null || echo '?')
  corpus_params=$(sqlite3 "$LEDGER" "SELECT COUNT(*) FROM items WHERE status='open' AND weight='hard' AND kind='parameter';" 2>/dev/null || echo '?')
  corpus_q=$(sqlite3      "$LEDGER" "SELECT COUNT(*) FROM items WHERE status='open' AND weight='hard' AND kind IN ('question','directive');" 2>/dev/null || echo '?')
  corpus_unack=$(sqlite3  "$LEDGER" "SELECT COUNT(*) FROM unacknowledged;" 2>/dev/null || echo '?')
  # Zero conflicts is a real finding, not a missing measurement -- it means no
  # two of his stated parameters contradict each other, which is what makes the
  # corpus safe to answer questions from instead of asking him.
  corpus_conflicts=$(sqlite3 "$LEDGER" "SELECT COUNT(*) FROM conflicts;" 2>/dev/null || echo '?')
fi

subject="Phase report - ${open_total} open, ${closed_today_total} closed today"

body="As of ${now_iso}

NOT STARTED: ${open_total} open issues. That is the pile, and it is the number
that matters -- everything else below is motion.

IN FLIGHT: ${in_flight} tasks delivered and not yet accepted (last 6h).
SHIPPED TODAY: ${closed_today_total} issues closed, ${pr_total} PRs open awaiting review or merge.

Per repo:
${per_repo}
Lanes: ${free_lanes} free, ${busy_lanes} busy, ${blocked_lanes} blocked on a prompt.

FROM YOUR CORPUS -- the second backlog, made of your own words:
  ${corpus_open} hard items still open (${corpus_params} parameters, ${corpus_q} questions/directives)
  ${corpus_unack} never acknowledged at all
  ${corpus_conflicts} conflicts between what you have said

None of these are linked to an issue, because 'items' has no issue column --
that is the gap you spotted. Until it exists, this count is the corpus backlog
and the GitHub count above is a different pile entirely."

if [ "$blocked_lanes" -gt 0 ]; then
  body="${body}

${blocked_lanes} lane(s) are sitting on a prompt and doing nothing. Being cleared, not reported at you."
fi
if [ -n "$unreadable" ]; then
  body="${body}

Could not read: ${unreadable}-- the counts above are floors, not totals."
fi

if [ "$DRY" = "1" ]; then
  printf '=== %s ===\n%s\n' "$subject" "$body"
  exit 0
fi

if AGENT_NOTIFY_CALLER=supervisor "$HERE/notify.sh" "$subject" "$body" >/dev/null 2>&1; then
  log "sent -- ${open_total} open, ${closed_today_total} closed today"
  exit 0
fi
log "FAILED to send the phase report"
exit 1
