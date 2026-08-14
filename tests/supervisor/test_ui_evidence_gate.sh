#!/bin/bash
# #110's "make it the standard": ui-evidence-gate.sh is the mechanical part
# of "captured frames become required evidence on any UI PR" -- this pins
# its four outcomes against a stubbed `gh` so the check itself is verified
# without touching the network. .github/workflows/ui-evidence.yml is what
# wires this to actually run on every PR; this suite is what proves the
# gate's own logic before that CI wiring is trusted.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATE="$HERE/../../scripts/supervisor/ui-evidence-gate.sh"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }

echo "ui-evidence-gate.sh"

D=$(mktemp -d); mkdir -p "$D/bin"
# Stub gh: scripted purely from env vars set before each call, so each case
# below is self-contained rather than depending on call order.
#   GH_STUB_DIFF       newline-separated changed file paths
#   GH_STUB_BODY       PR body text
#   GH_STUB_COMMENTS   newline-separated comment bodies
cat > "$D/bin/gh" <<'STUB'
#!/bin/bash
case "$1 $2" in
  "pr diff")
    printf '%s\n' "$GH_STUB_DIFF"
    ;;
  "pr view")
    if [[ "$*" == *'-q .body'* ]]; then
      printf '%s' "$GH_STUB_BODY"
    elif [[ "$*" == *'.comments[]'* ]]; then
      printf '%s\n' "$GH_STUB_COMMENTS"
    else
      echo "gh stub: unrecognized pr view args: $*" >&2; exit 2
    fi
    ;;
  *)
    echo "gh stub: unrecognized args: $*" >&2; exit 2
    ;;
esac
STUB
chmod +x "$D/bin/gh"
export PATH="$D/bin:$PATH"

run_gate() {
  GH_STUB_DIFF="$1" GH_STUB_BODY="$2" GH_STUB_COMMENTS="$3" \
    UI_EVIDENCE_PATH_RE='scripts/supervisor/laneview/' bash "$GATE" 42
}

# --- case 0: UI_EVIDENCE_PATH_RE unset -> refuse, not guess -------------
# The gate does not default this itself -- laneview/README.md rule 3 says
# no headless script names the viewer outside a comment, and
# test_laneview_isolation.sh enforces that by grepping *.sh code. Which
# paths are UI is pushed into config (.github/workflows/ui-evidence.yml)
# instead.
out="$(GH_STUB_DIFF='' GH_STUB_BODY='' GH_STUB_COMMENTS='' bash "$GATE" 42 2>&1)"
rc=$?
if [ "$rc" -eq 2 ] && grep -q 'UI_EVIDENCE_PATH_RE is required' <<<"$out"; then
  ok "refuses to run without UI_EVIDENCE_PATH_RE set, rather than guessing a default"
else
  bad "refuses to run without UI_EVIDENCE_PATH_RE set" "rc=$rc out=$out"
fi

# --- case 1: no UI paths changed -> pass, regardless of evidence -------
out="$(run_gate $'scripts/supervisor/lanes.sh\ntests/supervisor/test_lanes.sh' '' '')"
rc=$?
if [ "$rc" -eq 0 ] && grep -q 'no UI paths changed' <<<"$out"; then
  ok "non-UI PR passes without needing evidence"
else
  bad "non-UI PR passes without needing evidence" "rc=$rc out=$out"
fi

# --- case 2: UI path changed, no marker anywhere -> fail ---------------
out="$(run_gate 'scripts/supervisor/laneview/tui.sh' 'just a description, no evidence' 'looks great' 2>&1)"
rc=$?
if [ "$rc" -eq 1 ] && grep -q 'carries no' <<<"$out"; then
  ok "UI PR with no marker anywhere fails, and says why"
else
  bad "UI PR with no marker anywhere fails" "rc=$rc out=$out"
fi

# --- case 3: UI path changed, marker in the PR body -> pass ------------
out="$(run_gate 'scripts/supervisor/laneview/tui.sh' $'<!-- ui-evidence:v1 -->\ncaptured frame here' '' 2>&1)"
rc=$?
if [ "$rc" -eq 0 ] && grep -q 'evidence present' <<<"$out"; then
  ok "UI PR with the marker in the body passes"
else
  bad "UI PR with the marker in the body passes" "rc=$rc out=$out"
fi

# --- case 4: UI path changed, marker only in a comment -> pass ---------
out="$(run_gate 'scripts/supervisor/laneview/tui.sh' 'no evidence up front' $'<!-- ui-evidence:v1 -->\nadded it after review asked' 2>&1)"
rc=$?
if [ "$rc" -eq 0 ] && grep -q 'evidence present' <<<"$out"; then
  ok "UI PR with the marker only in a follow-up comment passes"
else
  bad "UI PR with the marker only in a follow-up comment passes" "rc=$rc out=$out"
fi

# --- mutation check: prove the check can actually fail ------------------
# Corrupt the marker the gate looks for so it can never match, and confirm
# a PR that WOULD have passed now fails -- the check is not a tautology.
BROKEN=$(mktemp)
sed "s/<!-- ui-evidence:v1 -->/<!-- wrong-marker -->/" "$GATE" > "$BROKEN"
out="$(GH_STUB_DIFF='scripts/supervisor/laneview/tui.sh' \
  GH_STUB_BODY=$'<!-- ui-evidence:v1 -->\nreal evidence' GH_STUB_COMMENTS='' \
  UI_EVIDENCE_PATH_RE='scripts/supervisor/laneview/' \
  PATH="$D/bin:$PATH" bash "$BROKEN" 42 2>&1)"
rc=$?
rm -f "$BROKEN"
if [ "$rc" -eq 1 ]; then
  ok "mutation check: breaking the marker constant flips a real pass to a fail"
else
  bad "mutation check: breaking the marker constant flips a real pass to a fail" "rc=$rc out=$out"
fi

rm -rf "$D"
echo "ui-evidence-gate.sh: $pass ok, $fail failed"
[ "$fail" -eq 0 ]
