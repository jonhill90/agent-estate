#!/bin/bash
# Record that a PR was authored OUTSIDE the lane system -- GATED.
#
# WHY THIS WRAPPER EXISTS (agent-supervisor#308 item 3, #321's own review,
# item 5): `cli.py mark-pr-external` writes the `pr_external_authorship` row
# directly, with no caller verification of any kind -- contrast `accept`/
# `complete`, which call `_verify_caller`. Any lane with shell access could
# call it against a PR it contributed to itself, launder that PR as
# "no lane contributed", and then dispatch (or self-approve) a "review" that
# is actually the contributor reviewing its own work -- exactly the shape
# agent-supervisor#190 exists to refuse.
#
# The gate: before ever calling `cli.py mark-pr-external`, this script runs
# the SAME exhaustive resolution chain dispatch.sh's `--reviews-pr` guard
# runs (`resolve-pr-contributors.sh`, shared rather than reimplemented --
# see that file's header). Marking external is refused outright when:
#   - the PR or its head branch cannot be read at all (unknown, not safe --
#     agent-supervisor#190 forbids proceeding on missing data, and this is
#     no exception: "I could not check" is not "I checked and found
#     nothing").
#   - the resolution chain finds ANY contributor by ANY of its five paths
#     (issue, PR-task, PR-contributor, worktree, legacy branch) -- that PR
#     already has a real, ledger-known author; marking it external now
#     would erase that fact, not record a new one.
# Only when the chain runs to completion AND finds nobody does this proceed
# -- "no lane contributed" becomes a POSITIVE, evidenced claim instead of an
# operator's unchecked assertion.
#
# agent-supervisor#550: the five-path chain above can only prove "no
# REGISTERED lane contributed" -- it cannot tell that apart from "an
# unregistered internal actor (the director, agent-supervisor#532 -- no
# `lanes` row exists for it at all) contributed." Four rows were written on
# that exact confusion (#549) before this guard existed. Closed here, ahead
# of #532's real fix, with the same observed-not-asserted anchor every
# other `*-self.sh` tool in this repo uses: `$TMUX_PANE`. Every estate
# actor -- every lane AND the director -- runs inside a tmux pane; nothing
# that operates this repo from outside one exists today (`watchdog.sh` is a
# `launchd` job, no controlling tmux session at all; a human's own `gh`/git
# session run from their own terminal, #472/#495's own shape, has no
# `TMUX_PANE` either). So `$TMUX_PANE` being set at all -- regardless of
# whether the ledger has ever heard of that pane, which is exactly the
# director's own gap -- is treated as "an estate participant is asking,"
# and refused outright, before the five-path chain even runs. Fail-closed:
# this cannot positively confirm the caller is external, only that it does
# not look like an internal one from the one signal it can observe; an
# unset `$TMUX_PANE` is not proof of external authorship either, which is
# exactly why the five-path chain below still runs regardless and still
# has the final word on the PR's own content.
#
# Usage:
#   mark-pr-external.sh <repo> <pr> <note> [repo-path]
#
# <repo>       OWNER/NAME, e.g. jonhill90/agent-supervisor -- required (the
#              resolution chain needs it to query gh and the ledger by the
#              same key dispatch.sh uses).
# <pr>         the PR number.
# <note>       why this PR is believed external (a human's own commit, the
#              watchdog acting directly, ...) -- required, stored verbatim.
# [repo-path]  the local checkout used to check which worktree, if any, has
#              this PR's branch checked out (resolution step 3). Optional,
#              but recommended: without it, that one resolution path is
#              never consulted, exactly like dispatch.sh's own [repo-path]
#              omission -- see resolve-pr-contributors.sh step 3. Passing it
#              makes the check strictly MORE exhaustive, never less.
#
# Exit 0 only once `pr_external_authorship` is actually written. Exit 1 on
# any refusal, with the reason on stderr -- unreadable PR, a genuine
# contributor found (named), or missing arguments.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./resolve-pr-contributors.sh
. "$HERE/resolve-pr-contributors.sh"

REPO="${1:-}"
PR="${2:-}"
NOTE="${3:-}"
REPO_PATH="${4:-}"

if [ -z "$REPO" ] || [ -z "$PR" ] || [ -z "$NOTE" ]; then
  echo "usage: mark-pr-external.sh <repo> <pr> <note> [repo-path]" >&2
  exit 2
fi

# --- agent-supervisor#550: refuse an estate participant outright ---------
# Same $TMUX_PANE anchor register-lane-self.sh uses, so a caller cannot
# assert its way past this by passing an argument -- there is no flag here
# to name a caller from outside, on purpose. Checked BEFORE the five-path
# resolution chain (cheap, local, no network/ledger round-trip) so a
# participant is refused without ever touching gh or the ledger.
if [ -n "${TMUX_PANE:-}" ]; then
  echo "mark-pr-external: refusing -- \$TMUX_PANE is set ($TMUX_PANE), so this call is coming from inside the estate's own tmux system" >&2
  echo "mark-pr-external: every lane and the director both run inside a tmux pane; a genuinely external actor (a human's own terminal, the watchdog's launchd job) never has \$TMUX_PANE set -- see this script's own header on agent-supervisor#550/#532" >&2
  echo "mark-pr-external: this is refused regardless of whether the ledger has ever registered this pane as a lane -- that gap (an unregistered internal actor reading as 'nobody') is exactly what this check exists to close" >&2
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
  echo "mark-pr-external: refusing -- authorship could not be checked at all, which is not the same as checked-and-empty" >&2
  exit 1
fi

if [ -n "$CONTRIBUTORS_RESOLVED" ] && [ ${#AUTHOR_LANES[@]} -gt 0 ]; then
  echo "mark-pr-external: refusing to mark PR #$PR external -- the ledger already resolves real contributor(s):" >&2
  i=0
  while [ "$i" -lt "${#AUTHOR_LANES[@]}" ]; do
    echo "  ${AUTHOR_LANES[$i]} (task ${AUTHOR_TASKS[$i]})" >&2
    i=$((i + 1))
  done
  echo "mark-pr-external: marking this external now would erase a known contributor's record, not record an absent one -- that is the self-review-laundering path agent-supervisor#308/#321 closed this gate against" >&2
  exit 1
fi
if [ -z "$REPO_PATH" ]; then
  echo "mark-pr-external: NOTE -- no [repo-path] given, so the worktree resolution path (step 3) was never consulted; pass one to make this check more exhaustive" >&2
fi

"$LEDGER_PYTHON" "$LEDGER_CLI" mark-pr-external --repo "$REPO" --pr "$PR" --note "$NOTE" --chain-verified
rc=$?
if [ $rc -ne 0 ]; then
  echo "mark-pr-external: cli.py mark-pr-external failed (exit $rc)" >&2
  exit $rc
fi
echo "mark-pr-external: PR #$PR marked external -- no lane contributor found by any resolution path (issue, PR-task, PR-contributor, worktree, legacy branch)" >&2
