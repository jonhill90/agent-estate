#!/bin/bash
# The only path a lane or the supervisor should use to merge a PR in this
# repo -- so the gate `ci_gate.py` implements cannot be skipped by habit.
#
# WHY: agent-supervisor#13. Branch protection and rulesets are both
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
# WHAT IT NEVER DOES: fall back to merging when the gate cannot be
# evaluated. `ci_gate.py` fails closed (network error, malformed gh
# response, PR not found -- all refuse), and this script trusts that exit
# code without re-deriving anything from the gate's stdout.
#
# Usage:
#   merge-pr.sh <repo> <number> [gh pr merge args...]
#
# <repo> is owner/name. Extra arguments are passed through to `gh pr merge`
# verbatim (e.g. --squash, --auto, --delete-branch); this script adds none
# of its own so it never picks a merge strategy on the caller's behalf.
#
# Exit 0    gate passed and `gh pr merge` was run (its own exit code, which
#           on success is 0).
# Exit 1    gate refused; nothing was merged. The gate's reason is printed.
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

echo "merge-pr: gate passed -- $GATE_OUT" >&2
exec "$GH" pr merge "$NUMBER" --repo "$REPO" "$@"
