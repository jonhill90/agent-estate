#!/bin/bash
# agent-supervisor#597: fixpass-evidence.yml's issue_comment run used to
# report against the default branch's tip, never the PR's actual head --
# the same #468 defect ui-evidence.yml already fixed for its own gate. This
# script (fixpass-evidence-gate.sh) is the adapter that lets
# ui-evidence-report.sh (already generic over which gate it drives, via
# UI_EVIDENCE_GATE_BIN) drive fixpass_evidence_gate.py, which speaks
# `--repo owner/name --number N`, not the bare `"$GATE" "$PR"` convention
# ui-evidence-report.sh calls its gate with.
#
# ui-evidence-report.sh already maps any non-zero gate exit to `failure`
# (pinned by test_ui_evidence_report.sh) -- what this suite pins is the one
# property specific to THIS adapter: it must pass fixpass_evidence_gate.py's
# own exit code straight through, unmodified, rather than translate it. A
# stub for fixpass_evidence_gate.py stands in so this never touches the
# network or a real `gh`.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ADAPTER="$HERE/../../scripts/supervisor/fixpass-evidence-gate.sh"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }

echo "fixpass-evidence-gate.sh"

D=$(mktemp -d)

# Stub fixpass_evidence_gate.py: exits whatever FAKE_GATE_RC says, and
# records the --repo/--number it was invoked with so the adapter's
# argument-building can be checked too.
cat > "$D/fixpass_evidence_gate.py" <<'STUB'
import sys
print("stub invoked with: %s" % sys.argv[1:])
sys.exit(int(__import__("os").environ.get("FAKE_GATE_RC", "0")))
STUB

run_adapter() {
  FAKE_GATE_RC="$1" FIXPASS_EVIDENCE_GATE_PY="$D/fixpass_evidence_gate.py" \
    FIXPASS_EVIDENCE_REPO="owner/repo" \
    bash "$ADAPTER" 42
}

# --- case 0: no PR number -> usage error, exit 2 ------------------------
out="$(FIXPASS_EVIDENCE_GATE_PY="$D/fixpass_evidence_gate.py" FIXPASS_EVIDENCE_REPO="owner/repo" bash "$ADAPTER" 2>&1)"
rc=$?
if [ "$rc" -eq 2 ] && grep -q 'usage:' <<<"$out"; then
  ok "refuses without a PR number (exit 2)"
else
  bad "refuses without a PR number" "rc=$rc out=$out"
fi

# --- case 1: no repo resolvable -> config error, exit 2 -----------------
out="$(env -u GITHUB_REPOSITORY FIXPASS_EVIDENCE_GATE_PY="$D/fixpass_evidence_gate.py" bash "$ADAPTER" 42 2>&1)"
rc=$?
if [ "$rc" -eq 2 ] && grep -q 'FIXPASS_EVIDENCE_REPO or GITHUB_REPOSITORY' <<<"$out"; then
  ok "refuses when no repo can be resolved (exit 2)"
else
  bad "refuses when no repo can be resolved" "rc=$rc out=$out"
fi

# --- case 2: underlying gate passes (exit 0) -> adapter exits 0 ---------
out="$(run_adapter 0 2>&1)"; rc=$?
if [ "$rc" -eq 0 ] && grep -qF -- "'--repo', 'owner/repo'" <<<"$out" && grep -qF -- "'--number', '42'" <<<"$out"; then
  ok "passes through exit 0 and builds --repo/--number correctly"
else
  bad "exit 0 passthrough" "rc=$rc out=$out"
fi

# --- case 3: underlying gate refuses (exit 1) -> adapter exits 1, not translated to 2 or 0
out="$(run_adapter 1 2>&1)"; rc=$?
if [ "$rc" -eq 1 ]; then
  ok "passes through exit 1 unmodified"
else
  bad "exit 1 passthrough" "rc=$rc out=$out"
fi

# --- case 4: underlying gate errors (exit 2, e.g. its own config error) -- pinned per the task brief: this must never collapse into 0 or 1
out="$(run_adapter 2 2>&1)"; rc=$?
if [ "$rc" -eq 2 ]; then
  ok "passes through exit 2 unmodified (never read as passing)"
else
  bad "exit 2 passthrough" "rc=$rc out=$out"
fi

rm -rf "$D"
echo "fixpass-evidence-gate.sh: $pass ok, $fail failed"
[ "$fail" -eq 0 ]
