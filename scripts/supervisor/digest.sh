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
REPOS="${DIGEST_REPOS:-agent-dotfiles skills skills-private agent-evals}"
OWNER="${DIGEST_OWNER:-jonhill90}"
SINCE="${DIGEST_SINCE:-}"
MODE="${1:-}"

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
else
  wd_state="UNREADABLE"; wd_checked=""; wd_restarts=""; wd_heartbeat=""
  note_error "watchdog.status unreadable at $WD_FILE"
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
        --json number,title,headRefOid,headRefName,mergeStateStatus,comments 2>/dev/null) || {
    note_error "gh pr list failed for $OWNER/$repo -- its PRs are NOT in this digest"
    continue
  }
  [ -z "$list" ] && list="[]"
  pr_count=$(jq 'length' <<<"$list")
  for ((i = 0; i < pr_count; i++)); do
    p=$(jq -c ".[$i]" <<<"$list")
    branch=$(jq -r '.headRefName' <<<"$p")
    num=$(jq -r '.number' <<<"$p")
    run=$(gh run list -R "$OWNER/$repo" --branch "$branch" --limit 1 \
          --json headSha,conclusion 2>/dev/null) || {
      note_error "gh run list failed for $OWNER/$repo#$num -- CI status omitted"
      run="[]"
    }
    [ -z "$run" ] && run="[]"
    r=$(jq -c '.[0] // {}' <<<"$run")
    entry=$(jq -n --argjson p "$p" --argjson r "$r" --arg repo "$repo" '
      {
        repo: $repo, number: $p.number, title: $p.title,
        head: $p.headRefOid[0:8],
        run_sha: ($r.headSha // "" | .[0:8]),
        run_conclusion: ($r.conclusion // "NO RUN"),
        # the check is stale unless the run was for THIS head -- the field the
        # UI does not distinguish, and a conflicted branch produces no run at all
        ci_is_current: (($r.headSha // "") == $p.headRefOid),
        merge_state: $p.mergeStateStatus,
        verdict: (
          [ $p.comments[]? | select(.body | test("quota limit") | not) ] | last | .body // ""
          | if test("REQUEST CHANGES";"i") then "REQUEST CHANGES"
            elif test("APPROVE";"i") then "APPROVE" else "none" end
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
  --argjson poller_alive "$poller_alive" --arg poller_state "$poller_state" \
  --arg poller_checked "$poller_checked" \
  --arg free "$(lane_line free)" --arg busy "$(lane_line busy)" \
  --arg blocked "$(lane_line blocked)" --arg menu "$(lane_line menu-blocked)" \
  --arg dead "$(lane_line dead)" --arg service "$(lane_line service)" \
  --arg unknown "$(lane_line unknown)" \
  --argjson prs "$PR_JSON" --argjson merged "$MERGED_JSON" --argjson errors "$ERR_JSON" '
  {checked: $checked,
   watchdog: {state:$wd_state, checked:$wd_checked, restarts:$wd_restarts, heartbeat:$wd_heartbeat},
   poller: {alive:$poller_alive, state:$poller_state, checked:$poller_checked},
   lanes: {free:$free, busy:$busy, blocked:$blocked, menu_blocked:$menu,
           dead:$dead, service:$service, unknown:$unknown},
   prs: $prs, merged_since: $merged, errors: $errors,
   ok: ($errors | length == 0)}')

if [ "$MODE" = "--json" ]; then
  printf '%s\n' "$DIGEST"
else
  jq -r '
    "watchdog: \(.watchdog.state)  restarts=\(.watchdog.restarts)  \(.watchdog.heartbeat)",
    "poller:   alive=\(.poller.alive) state=\(.poller.state)",
    "lanes:    free=[\(.lanes.free)] busy=[\(.lanes.busy)]",
    "          blocked=[\(.lanes.blocked)] menu=[\(.lanes.menu_blocked)] dead=[\(.lanes.dead)]",
    (if (.prs|length) == 0 then "prs:      none open" else "prs:" end),
    # Three distinct CI states, not two: no run at all, a run that failed, and
    # a run that passed but is not for this head. Collapsing "no run" and
    # "stale" cost a real investigation (#149) -- the STALE annotation names a
    # run to distrust, so it is only printed when a run actually exists.
    (.prs[] | "  \(.repo)#\(.number) ci=\(.run_conclusion)\(if (.ci_is_current or .run_conclusion == "NO RUN") then "" else " [STALE - run is for \(.run_sha), head is \(.head)]" end) \(.merge_state) verdict=\(.verdict)"),
    (if (.merged_since|length) > 0 then "merged:" else empty end),
    (.merged_since[] | "  \(.repo)#\(.number) \(.title[0:52])"),
    (if (.errors|length) > 0 then "ERRORS (this digest is INCOMPLETE):" else empty end),
    (.errors[] | "  ! \(.)")
  ' <<<"$DIGEST"
fi

[ "${#ERRORS[@]}" -eq 0 ]
