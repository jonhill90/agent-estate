#!/bin/bash
# dispatch-claude-print.sh must refuse a NEW dispatch when host_pressure.py
# reports the host cannot safely take it, and never silently proceed just
# because the gate could not be consulted -- the same property
# test_dispatch_host_pressure.sh proves for dispatch.sh's tmux flow.
#
# WHY THIS EXISTS: agent-supervisor#643 measured, directly (grep for
# "host-pressure" across scripts/supervisor), that dispatch-claude-print.sh
# started a brand-new `claude -p` subprocess with NO host-pressure check of
# any kind -- host-pressure.sh's gate covered only dispatch.sh's tmux flow.
# This suite is the "a fix is not complete until a test that failed before
# it passes after it" evidence for that gap, applied to a new integration
# rather than a bugfix: a REFUSE case that must go RED if the gate call is
# ever removed from dispatch-claude-print.sh again.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DISPATCH="$HERE/../../scripts/supervisor/dispatch-claude-print.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }

echo "dispatch-claude-print.sh -- the host-pressure gate (#643)"

D=$(mktemp -d); mkdir -p "$D/bin" "$D/roots"
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cp "$HERE/stubs/claude" "$D/bin/claude"

git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/repo" 2>/dev/null
REPO="$D/repo"
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name "Test"
git -C "$REPO" checkout -q -b main
echo one > "$REPO/file.txt"
git -C "$REPO" add file.txt
git -C "$REPO" commit -q -m initial
git -C "$REPO" push -q -u origin main

cat > "$D/issues" <<'FIX'
643|| the claude-print host-pressure gate itself
644|| the claude-print host-pressure gate itself, mutation case
FIX
: > "$D/prs"
echo "do the thing" > "$D/brief.md"

n=0
run() {
  n=$((n+1))
  PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    AGENT_SUPERVISOR_STATE_DIR="$(mktemp -d "$D/state.XXXXXX")" \
    WORKTREE_ROOT="$D/roots" \
    "$@" \
    bash "${DISPATCH_SCRIPT:-$DISPATCH}" 643 "claude-print-hp-$n" "$D/brief.md" acme/agent-dotfiles "$REPO" 2>&1
}
assignees() { awk -F'|' -v n="$1" '$1==n{print $2}' "$D/issues"; }

# --- the load-bearing REFUSE case: an impossible threshold refuses before
# claim.sh ever runs. Every real host's load/core is > 0.001. ---------------
REFUSE_OUT=$(run env SUPERVISOR_MAX_LOAD_PER_CORE=0.001 SUPERVISOR_MIN_FREE_MEM_GB=0); REFUSE_RC=$?
want_exit "an impossible load/core threshold refuses to dispatch" "$REFUSE_RC" 1 "$REFUSE_OUT"
want_contains "...names host-pressure as the reason" "host-pressure" "$REFUSE_OUT"
want_contains "...names the actual over-threshold comparison" ">= 0.001" "$REFUSE_OUT"
if [ -z "$(assignees 643)" ]; then
  ok "...and the issue was never claimed"
else
  bad "...and the issue was never claimed" "still assigned: $(assignees 643)"
fi

# --- the load-bearing ALLOW case: disabled thresholds (0 = off) proceed
# past the gate to a full, successful dispatch. ------------------------------
ALLOW_OUT=$(run env SUPERVISOR_MAX_LOAD_PER_CORE=0 SUPERVISOR_MIN_FREE_MEM_GB=0); ALLOW_RC=$?
want_exit "disabled thresholds let the same dispatch proceed" "$ALLOW_RC" 0 "$ALLOW_OUT"

# --- MUTATION: remove the gate call from dispatch-claude-print.sh -> REFUSE
# case must go RED. Proves the REFUSE assertion above is pinned to the
# script actually calling host_pressure.py, not to something else already
# refusing. Placed INSIDE scripts/supervisor/ itself, not $D, for the same
# reason test_dispatch_host_pressure.sh's own mutation does: sibling calls
# ($HERE/claim.sh etc) resolve from the mutant's own dirname.
MUT_NOGATE="$HERE/../../scripts/supervisor/dispatch-claude-print-mutant-nogate.sh"
trap 'rm -f "$MUT_NOGATE"' EXIT
mut_rc=0
python3 - "$DISPATCH" "$MUT_NOGATE" <<'PY' || mut_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''HOST_PRESSURE_OUT=$("$PYTHON" "$HERE/host_pressure.py"); HOST_PRESSURE_RC=$?
if [ "$HOST_PRESSURE_RC" -ne 0 ]; then
  echo "dispatch-claude-print: $HOST_PRESSURE_OUT -- NOT dispatching #$ISSUE" >&2
  exit 1
fi'''
assert marker in text, "host-pressure gate block not found verbatim -- script shape changed"
assert text.count(marker) == 1, "host-pressure gate block not unique -- script shape changed"
text = text.replace(marker, "true  # host-pressure gate removed by mutation test", 1)
open(dst, "w").write(text)
PY
if [ "$mut_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch-claude-print.sh with the host-pressure call site disabled" \
    "could not patch $DISPATCH (exit $mut_rc)"
else
  ok "setup: patched a copy of dispatch-claude-print.sh with the host-pressure call site disabled"
  chmod +x "$MUT_NOGATE"
  echo '644|| a mutation case, issue never actually claimed by the real gate' > "$D/mut-issues"
  cat "$D/issues" "$D/mut-issues" | sort -u -t'|' -k1,1 > "$D/issues.tmp" && mv "$D/issues.tmp" "$D/issues"
  MUT_OUT=$(PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    AGENT_SUPERVISOR_STATE_DIR="$(mktemp -d "$D/state.XXXXXX")" WORKTREE_ROOT="$D/roots" \
    SUPERVISOR_MAX_LOAD_PER_CORE=0.001 SUPERVISOR_MIN_FREE_MEM_GB=0 \
    bash "$MUT_NOGATE" 644 claude-print-hp-mut "$D/brief.md" acme/agent-dotfiles "$REPO" 2>&1); MUT_RC=$?
  if [ "$MUT_RC" -eq 0 ]; then
    ok "mutation confirmed: removing the gate call lets an impossible-threshold dispatch proceed anyway (REFUSE case's GREEN assertion would now be RED)"
  else
    bad "mutation confirmed: removing the gate call lets an impossible-threshold dispatch proceed anyway" \
      "expected exit 0 on the mutant, got $MUT_RC: $MUT_OUT"
  fi
fi

echo
echo "dispatch-claude-print.sh host-pressure gate: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
