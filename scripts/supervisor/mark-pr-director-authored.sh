#!/bin/bash
# Record that a PR was authored DIRECTLY BY THE DIRECTOR -- GATED, with the
# OPPOSITE identity check from mark-pr-external.sh.
#
# WHY THIS TOOL EXISTS, AND NOT register-lane-self.sh OR mark-pr-external.sh
# (agent-estate#741, #740/#742, #745/#746, #748):
#
# - register-lane-self.sh refuses outright when the calling pane's window
#   index equals LANES_SUPERVISOR_WINDOW (default 1) -- the Director's own
#   window is explicitly excluded from ever registering as a worker lane.
#   The Director genuinely IS that window; this tool is structurally the
#   wrong shape for the Director's own identity, not merely inconvenient.
# - mark-pr-external.sh's own $TMUX_PANE-set refusal (agent-supervisor#550)
#   exists specifically to stop an internal actor's PR from being laundered
#   as "authored outside the lane system." Using it for a director-authored
#   PR would be exactly the misuse it was built to prevent.
#
# `get_contributor_tasks_for_pr` requires a `tasks` row JOINed to a
# `source_tasks` row -- a director-authored PR has neither, by construction:
# there was no dispatch.sh/assign_task call. `pr_director_authorship`
# (core_ledger_schema.py) is the sibling of `pr_external_authorship` that
# closes this gap -- see that table's own comment for why it is a sibling,
# not a reuse.
#
# THE GATE: exactly the same exhaustive resolution chain mark-pr-external.sh
# runs (resolve-pr-contributors.sh, shared -- not reimplemented), before
# ever calling `cli.py mark-pr-director-authored`. Refused outright when:
#   - the PR or its head branch cannot be read at all (unknown, not safe).
#   - the resolution chain finds ANY contributor by ANY of its five paths.
# Only when the chain runs to completion AND finds nobody does this proceed.
#
# THE IDENTITY CHECK, deliberately the OPPOSITE of mark-pr-external.sh's:
# mark-pr-external.sh refuses when $TMUX_PANE IS set (an estate participant
# asking). This tool requires $TMUX_PANE to be set -- same anchor every
# *-self.sh tool in this repo uses -- AND requires that pane's own live
# window index to actually equal LANES_SUPERVISOR_WINDOW, verified live via
# `tmux display-message -t "$TMUX_PANE"` (never a bare `display-message`
# with no -t -- invariant 10, the #187 defect). Only the Director's own live
# pane can assert this; any other lane's pane is refused, named by its own
# wrong window index rather than a generic error.
#
# Usage:
#   mark-pr-director-authored.sh <repo> <pr> <note> [repo-path]
#
# <repo>       OWNER/NAME, e.g. jonhill90/agent-estate -- required.
# <pr>         the PR number.
# <note>       why this PR is believed director-authored -- required, stored
#              verbatim.
# [repo-path]  the local checkout used to check which worktree, if any, has
#              this PR's branch checked out (resolution step 3). Optional,
#              but recommended -- see resolve-pr-contributors.sh step 3.
#
# Exit 0 only once `pr_director_authorship` is actually written. Exit 1 on
# any refusal, with the reason on stderr -- wrong window, unreadable PR, a
# genuine contributor found (named), or missing arguments. Exit 2 on usage
# error.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./resolve-pr-contributors.sh
. "$HERE/resolve-pr-contributors.sh"

TMUX_BIN="${AGENT_TMUX_BIN:-tmux}"
SUPERVISOR_WINDOW="${LANES_SUPERVISOR_WINDOW:-1}"

REPO="${1:-}"
PR="${2:-}"
NOTE="${3:-}"
REPO_PATH="${4:-}"

if [ -z "$REPO" ] || [ -z "$PR" ] || [ -z "$NOTE" ]; then
  echo "usage: mark-pr-director-authored.sh <repo> <pr> <note> [repo-path]" >&2
  exit 2
fi

# --- the identity check: only the Director's own live pane may assert this -
# Same $TMUX_PANE anchor register-lane-self.sh and mark-pr-external.sh both
# use, so a caller cannot assert its way past this by passing an argument --
# there is deliberately no flag to name a caller from outside.
if [ -z "${TMUX_PANE:-}" ]; then
  echo "mark-pr-director-authored: refusing -- \$TMUX_PANE is not set, so this process cannot observe which pane it is in" >&2
  echo "mark-pr-director-authored: run this FROM the Director's own pane. There is deliberately no flag to name a caller from outside -- see this script's header on agent-estate#741 and invariant 10." >&2
  exit 1
fi
PANE="$TMUX_PANE"

# Every read below is explicitly `-t "$PANE"`. A bare `display-message`
# answers for the session's ACTIVE window, not the caller's own -- invariant
# 10, the #187 defect.
META=$("$TMUX_BIN" display-message -p -t "$PANE" \
  '#{pane_id}|#{window_index}' 2>&1)
if [ $? -ne 0 ] || [ -z "$META" ]; then
  echo "mark-pr-director-authored: refusing -- could not read pane $PANE off tmux: $META" >&2
  exit 1
fi
IFS='|' read -r LIVE_PANE WINDOW_INDEX <<<"$META"

if [ "$LIVE_PANE" != "$PANE" ]; then
  echo "mark-pr-director-authored: refusing -- asked tmux about $PANE and it answered for $LIVE_PANE" >&2
  exit 1
fi
if [ -z "$WINDOW_INDEX" ]; then
  echo "mark-pr-director-authored: refusing -- tmux returned an incomplete description of pane $PANE: '$META'" >&2
  exit 1
fi

if [ "$WINDOW_INDEX" != "$SUPERVISOR_WINDOW" ]; then
  echo "mark-pr-director-authored: refusing -- pane $PANE is window index $WINDOW_INDEX, not the Director's own window (LANES_SUPERVISOR_WINDOW=$SUPERVISOR_WINDOW)" >&2
  echo "mark-pr-director-authored: only the Director's own live pane may mark a PR director-authored -- a worker lane's PR is never this tool's business, see mark-pr-external.sh or the review path instead" >&2
  exit 1
fi

NAME_PART="${REPO##*/}"
if [[ "$NAME_PART" == *-* ]]; then
  PREFIX=$(tr '-' '\n' <<<"$NAME_PART" | cut -c1 | tr -d '\n')
else
  PREFIX="$NAME_PART"
fi

LEDGER_PYTHON="${DISPATCH_PYTHON:-python3}"
LEDGER_CLI="$HERE/cli.py"

if ! resolve_pr_contributors "$REPO" "$PR" "$REPO_PATH" "$PREFIX" "$LEDGER_PYTHON" "$LEDGER_CLI"; then
  # resolve_pr_contributors already printed why (unreadable PR/branch).
  echo "mark-pr-director-authored: refusing -- authorship could not be checked at all, which is not the same as checked-and-empty" >&2
  exit 1
fi

if [ -n "$CONTRIBUTORS_RESOLVED" ] && [ ${#AUTHOR_LANES[@]} -gt 0 ]; then
  echo "mark-pr-director-authored: refusing to mark PR #$PR director-authored -- the ledger already resolves real contributor(s):" >&2
  i=0
  while [ "$i" -lt "${#AUTHOR_LANES[@]}" ]; do
    echo "  ${AUTHOR_LANES[$i]} (task ${AUTHOR_TASKS[$i]})" >&2
    i=$((i + 1))
  done
  echo "mark-pr-director-authored: marking this director-authored now would erase a known contributor's record, not record an absent one -- the same self-review-laundering path agent-supervisor#308/#321 closed this gate against" >&2
  exit 1
fi
if [ -z "$REPO_PATH" ]; then
  echo "mark-pr-director-authored: NOTE -- no [repo-path] given, so the worktree resolution path (step 3) was never consulted; pass one to make this check more exhaustive" >&2
fi

"$LEDGER_PYTHON" "$LEDGER_CLI" mark-pr-director-authored --repo "$REPO" --pr "$PR" --note "$NOTE" --chain-verified
rc=$?
if [ $rc -ne 0 ]; then
  echo "mark-pr-director-authored: cli.py mark-pr-director-authored failed (exit $rc)" >&2
  exit $rc
fi
echo "mark-pr-director-authored: PR #$PR marked director-authored -- no lane contributor found by any resolution path (issue, PR-task, PR-contributor, worktree, legacy branch)" >&2
