#!/bin/bash
# The only path a lane or the supervisor should use to merge a PR in this
# repo -- so the gates below cannot be skipped by habit.
#
# WHY (CI gate): agent-supervisor#13. Branch protection and rulesets are both
# unavailable here without GitHub Pro -- measured, not inferred:
#
#   $ gh api repos/jonhill90/agent-supervisor/branches/main/protection
#   403 "Upgrade to GitHub Pro or make this repository public..."
#   $ gh api repos/jonhill90/agent-supervisor/rulesets
#   403 (same)
#
# So nothing on GitHub's side stops `gh pr merge` on a red PR. Two things
# this repo's own history shows why that is thin: PR #56 merged while
# reading a badge that took a hand-written comment to interpret correctly,
# and PR #49 sat DIRTY and merged anyway. `ci_gate.py` is the check;
# calling `gh pr merge` directly from a lane or from `dispatch.sh` still
# bypasses it completely -- this script is the ONLY thing that chains the
# two, and it is still convention that callers use this script rather than
# `gh pr merge` by hand. That residual is stated here, not hidden.
#
# WHY (authorship/independence gate): agent-supervisor#179. Every guard this
# estate has for "an author does not review or merge their own work"
# (dispatch.sh's `--reviews-pr` author exclusion, the one-review ceiling)
# sits on the DISPATCH path. A prompt reading "merge the PR" was found
# sitting, unsubmitted, in the input box of the lane that AUTHORED PR #168
# while its verdict was `none` -- free text typed into a pane walks straight
# around every dispatch-time guard and reaches `gh pr merge` (via this
# script) directly. It did not submit only because of an unrelated defect
# (#178, `Enter` not submitting text a previous `send-keys` left in the box)
# -- that is luck, not a guard, and #178's own fix removes the thing that
# saved us. So THIS script -- the one place that "cannot be skipped by
# habit" -- is where the check belongs, not the dispatcher. See
# verdict-independence.sh for the actual computation (shared with digest.sh,
# which already had it for reporting).
#
# WHAT IT NEVER DOES: fall back to merging when either gate cannot be
# evaluated. `ci_gate.py` fails closed (network error, malformed gh
# response, PR not found -- all refuse); the authorship/independence gate
# fails closed the same way -- unresolved authorship, an unreadable verdict,
# or a verdict with no lane to compare against all refuse, never "proceed
# anyway". This script trusts each gate's own exit code / decision without
# re-deriving anything from its output.
#
# Usage:
#   merge-pr.sh <repo> <number> [gh pr merge args...]
#
# <repo> is owner/name. Extra arguments are passed through to `gh pr merge`
# verbatim (e.g. --squash, --auto, --delete-branch); this script adds none
# of its own so it never picks a merge strategy on the caller's behalf.
#
# REQUIRES: jq (already a dependency of digest.sh, which this script now
# shares its authorship/independence computation with via
# verdict-independence.sh).
#
# Exit 0    both gates passed and `gh pr merge` was run (its own exit code,
#           which on success is 0).
# Exit 1    a gate refused; nothing was merged. The refusing gate's reason is
#           printed.
# Exit 2    usage error.
set -uo pipefail

usage() { sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; exit 2; }

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PYTHON="${AGENT_PYTHON_BIN:-python3}"
GH="${AGENT_GH_BIN:-gh}"

REPO="${1:-}"
NUMBER="${2:-}"
[ -n "$REPO" ] && [ -n "$NUMBER" ] || usage
shift 2 || true

GATE_OUT=$("$PYTHON" "$HERE/ci_gate.py" --repo "$REPO" --number "$NUMBER")
gate_rc=$?

if [ "$gate_rc" -ne 0 ]; then
  echo "merge-pr: refused -- $GATE_OUT" >&2
  exit 1
fi

# --- authorship / independence gate (agent-supervisor#179) -----------------
# The SHA `ci_gate.py` just checked is also the SHA the verdict must answer
# for (agent-dotfiles#218: a verdict filed against an older head must not
# count) -- read out of $GATE_OUT rather than re-fetched, so this can never
# disagree with what CI was actually evaluated against.
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
LEDGER_PYTHON="${MERGE_PR_LEDGER_PYTHON:-$PYTHON}"
LEDGER_CLI="${MERGE_PR_LEDGER_CLI:-$HERE/cli.py}"
VERDICT_PYTHON="${MERGE_PR_VERDICT_PYTHON:-$PYTHON}"
# Default "github", matching digest.sh's own default and for the same reason
# (agent-dotfiles#214): LedgerVerdictSource is a table nothing writes yet.
VERDICT_SOURCE="${MERGE_PR_VERDICT_SOURCE:-github}"
VERDICT_BIN="${MERGE_PR_VERDICT_BIN:-}"
# shellcheck source=./verdict-independence.sh
. "$HERE/verdict-independence.sh"

SHA=$(jq -r '.sha // ""' 2>/dev/null <<<"$GATE_OUT")
if [ -z "$SHA" ]; then
  echo "merge-pr: refused -- CI gate passed but reported no head SHA to check authorship against (failing closed)" >&2
  exit 1
fi

V=$(verdict_for "$REPO" "$NUMBER" "$SHA")
AUTHOR=$(author_lane_for "$REPO" "$NUMBER")
REVIEWER_LANE_ID=$(jq -r '.reviewer_lane // ""' <<<"$V")
LANE_REL='{"overall":"unknown","matched_lane":null,"matched_task":null}'
if [ -n "$REVIEWER_LANE_ID" ] && jq -e '.known == true and (.external != true) and ((.contributors // []) | length) > 0' >/dev/null 2>&1 <<<"$AUTHOR"; then
  # agent-supervisor#332: resolve_lane_relation(), not the bare
  # lane_relation() -- see verdict-independence.sh's own comment. This is
  # the ENFORCEMENT gate (#179); trusting a shape-only "different" here on
  # two ids a renumber could have collided is exactly the self-merge #235
  # left open at this call site.
  # agent-supervisor#200: contributor_lane_relation(), not a single-pair
  # resolve_lane_relation() call -- AUTHOR now names the FULL contributor
  # set (author_lane_for's widening), and the reviewer must be proven
  # different from every one of them, not just the one lane a narrower
  # resolution used to name "the author".
  LANE_REL=$(contributor_lane_relation "$AUTHOR" "$REVIEWER_LANE_ID")
fi
IND=$(independence_verdict "$V" "$AUTHOR" "$LANE_REL")

# agent-supervisor#486: independence_verdict answers "was this reviewed
# independently", not "was it approved" -- both an approved AND a rejected
# verdict satisfy its own IN("approved","rejected") branch, so a
# rejected-but-independent review passed this gate's `value == true` the
# same as an approved one (reproduced live on PR #485). Refuse outright on
# a rejected verdict here, independent of and before the independence
# check -- do not fold this into independence_verdict itself, since
# digest.sh's own call to it (reporting "independently rejected" as a fact)
# still needs the unchanged semantics.
if [ "$(jq -r '.verdict // ""' <<<"$V")" = "rejected" ]; then
  echo "merge-pr: refused -- verdict is rejected -- $(jq -r '.detail // ""' <<<"$V")" >&2
  exit 1
fi

if [ "$(jq -r '.value' <<<"$IND")" != "true" ]; then
  # agent-supervisor#192: `independence_verdict`'s "not yet reviewed" branch
  # deliberately returns an empty detail -- #184's reviewer asked for that,
  # because digest.sh's report otherwise carries an "independence unknown..."
  # sentence on every never-reviewed PR, which is noise on the single most
  # common case in that report. That is correct for digest.sh (reporting);
  # it is not acceptable for THIS caller, where "" means the refusal names
  # nothing -- indistinguishable from a crash. Rather than repopulate the
  # shared function's detail (and reintroduce the boilerplate #184 removed
  # for its OTHER caller), fall back HERE, in the caller that actually needs
  # a reason: print the raw verdict/author JSON this decision was made from.
  REASON=$(jq -r '.detail' <<<"$IND")
  if [ -z "$REASON" ]; then
    REASON="no reason given by independence_verdict -- raw verdict=$V author=$AUTHOR"
  fi
  echo "merge-pr: refused -- $REASON" >&2
  exit 1
fi

echo "merge-pr: gate passed -- $GATE_OUT" >&2
echo "merge-pr: independence confirmed -- $(jq -r '.detail' <<<"$IND")" >&2
exec "$GH" pr merge "$NUMBER" --repo "$REPO" "$@"
