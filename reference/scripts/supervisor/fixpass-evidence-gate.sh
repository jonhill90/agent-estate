#!/bin/bash
# agent-supervisor#597: ui-evidence-report.sh (#468's fix) already resolves a
# comment-triggered check's PR head SHA itself and publishes an explicit
# check-run against it, instead of trusting the run's own (wrong,
# default-branch-tip) status association -- see that script's own header.
# It is generic over which gate it runs (UI_EVIDENCE_GATE_BIN), calling it as
# `"$GATE" "$PR"`. fixpass_evidence_gate.py does not speak that convention --
# it takes `--repo owner/name --number N`, not a bare PR number, because its
# other caller (fixpass-evidence.yml's pull_request step) already has the
# repo available and passes it explicitly. This script is the adapter: it
# fills in the repo from GITHUB_REPOSITORY (or FIXPASS_EVIDENCE_REPO, for
# tests / manual runs outside Actions) so ui-evidence-report.sh can drive
# fixpass_evidence_gate.py the same way it drives ui-evidence-gate.sh.
#
# Exit codes are passed through, not translated: fixpass_evidence_gate.py's
# main() exits 0 (allow) or 1 (refuse); this script's OWN usage/config
# checks exit 2 on the same "never read as passing" basis
# ui-evidence-report.sh's header documents for its gate's exit 2 -- a
# missing PR number or unresolvable repo is not "nothing to gate", and must
# not be reported as success.
#
# Usage: fixpass-evidence-gate.sh <pr-number>
#   FIXPASS_EVIDENCE_REPO   owner/repo (default: $GITHUB_REPOSITORY)
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATE_PY="${FIXPASS_EVIDENCE_GATE_PY:-$HERE/fixpass_evidence_gate.py}"

PR="${1:-}"
if [ -z "$PR" ]; then
  echo "usage: fixpass-evidence-gate.sh <pr-number>" >&2
  exit 2
fi

REPO="${FIXPASS_EVIDENCE_REPO:-${GITHUB_REPOSITORY:-}}"
if [ -z "$REPO" ]; then
  echo "fixpass-evidence-gate: FIXPASS_EVIDENCE_REPO or GITHUB_REPOSITORY is required" >&2
  exit 2
fi

python3 "$GATE_PY" --repo "$REPO" --number "$PR"
exit $?
