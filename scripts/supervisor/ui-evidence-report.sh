#!/bin/bash
# agent-supervisor#468: an issue_comment-triggered run of this gate is, at
# trigger time, permanently associated by GitHub with the default branch's
# tip -- github.sha and the run's own head_sha resolve there regardless of
# what actions/checkout fetches inside the job (see the comment on the
# issue_comment trigger in .github/workflows/ui-evidence.yml, and the issue
# for the measured evidence: two runs on #467 both reported headSha
# ed779d7e, the main tip, not the PR's actual head 8d3a51b9). Relying on
# the job's own implicit status-to-commit association therefore reports
# the gate's result on a commit nobody is merging, and the PR's real head
# never sees it.
#
# This script breaks that implicit association: it resolves the PR's
# current head SHA itself (`gh pr view --json headRefOid`) and explicitly
# publishes a check-run against that SHA via the Checks API
# (repos/:owner/:repo/check-runs), instead of letting the workflow job's
# own pass/fail become the check GitHub attaches to the trigger-time SHA.
# ci_gate.py reads checks by SHA (commits/{sha}/check-runs), so this is
# what actually makes a comment-only evidence post turn a PR's gate green
# -- not the job's own status, which nothing here still depends on.
#
# FAILS CLOSED: any failure to resolve the head SHA, or to publish the
# check-run once resolved, exits non-zero without posting anything. A gate
# that could not confirm the right commit must never report green on the
# wrong one -- see the issue's "direction worse than the bug": a check-run
# posted against the wrong commit is worse than no check-run at all,
# because it converts "nobody checked this PR" into "CI approved it."
#
# Usage: ui-evidence-report.sh <pr-number>
#   Same required/optional env as ui-evidence-gate.sh (UI_EVIDENCE_PATH_RE
#   required, UI_EVIDENCE_MARKER optional), plus:
#   UI_EVIDENCE_CHECK_NAME   check-run name to publish (default: ui-evidence)
#   UI_EVIDENCE_REPO         owner/repo (default: $GITHUB_REPOSITORY)
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATE="${UI_EVIDENCE_GATE_BIN:-$HERE/ui-evidence-gate.sh}"

PR="${1:-}"
if [ -z "$PR" ]; then
  echo "usage: ui-evidence-report.sh <pr-number>" >&2
  exit 2
fi
if ! command -v gh >/dev/null 2>&1; then
  echo "ui-evidence-report: gh is required on PATH" >&2
  exit 2
fi

REPO="${UI_EVIDENCE_REPO:-${GITHUB_REPOSITORY:-}}"
if [ -z "$REPO" ]; then
  echo "ui-evidence-report: UI_EVIDENCE_REPO or GITHUB_REPOSITORY is required" >&2
  exit 2
fi
CHECK_NAME="${UI_EVIDENCE_CHECK_NAME:-ui-evidence}"

head_sha="$(gh pr view "$PR" --json headRefOid -q .headRefOid 2>&1)" || {
  echo "ui-evidence-report: could not resolve PR #$PR head SHA: $head_sha" >&2
  exit 2
}
if [ -z "$head_sha" ]; then
  echo "ui-evidence-report: gh returned an empty head SHA for PR #$PR" >&2
  exit 2
fi

gate_output="$("$GATE" "$PR" 2>&1)"
gate_rc=$?

# Never map a non-zero gate exit to a green check-run -- including the
# gate's own usage/config errors (exit 2), which are not "no UI paths
# changed" and must not read as passing either.
if [ "$gate_rc" -eq 0 ]; then
  conclusion=success
else
  conclusion=failure
fi

summary="$(printf '%s' "$gate_output" | head -c 60000)"
post_output="$(gh api "repos/$REPO/check-runs" \
  -f "name=$CHECK_NAME" \
  -f "head_sha=$head_sha" \
  -f "status=completed" \
  -f "conclusion=$conclusion" \
  -f "output[title]=UI evidence gate" \
  -f "output[summary]=$summary" 2>&1)"
post_rc=$?

if [ "$post_rc" -ne 0 ]; then
  echo "ui-evidence-report: failed to publish check-run against $head_sha: $post_output" >&2
  echo "$gate_output" >&2
  exit 2
fi

echo "ui-evidence-report: published '$CHECK_NAME' = $conclusion against $head_sha"
echo "$gate_output"

exit "$gate_rc"
