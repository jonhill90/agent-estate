#!/bin/bash
# agent-supervisor#694: a bare repo name ("agent-supervisor" instead of
# "jonhill90/agent-supervisor") built `repos/agent-supervisor/issues/N`,
# `gh api` 404'd, and claim.sh reported the identical "cannot read #$ISSUE
# -- refusing to claim on an unreadable state" it reports for a genuine
# GitHub outage -- indistinguishable from a real 404 on a valid repo. Both
# directions of the mutation check below: a malformed [repo] must now fail
# with the new argument error BEFORE any gh call, and a genuinely unreadable
# state (valid OWNER/NAME shape, issue that doesn't exist) must still fail
# closed exactly as before.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAIM="$HERE/../../scripts/supervisor/claim.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_contains() { if grep -q -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "$3"; fi }
want_exit() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2"; fi }

D=$(mktemp -d); mkdir -p "$D/bin"
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cp "$HERE/stubs/tmux-lanes" "$D/bin/tmux"
cp "$HERE/stubs/ps-lanes" "$D/bin/ps"

cat > "$D/issues" <<'FIX'
683|| An open issue in the right repo
FIX
: > "$D/prs"
: > "$D/lanes"

run() { PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
        LANES_FIXTURE="$D/lanes" LANES_SESSION=t bash "$CLAIM" "$@" 2>&1; }

echo "claim.sh -- [repo] argument shape (#694)"

# --- direction 1: a bare repo name is now a loud argument error, not a 404 -
out=$(run check 683 agent-supervisor); rc=$?
want_exit "a bare repo name is refused, not sent to gh" "$rc" 2
want_contains "the refusal names the bad argument" "not OWNER/NAME" "$out"
if grep -qi "unreadable state" <<<"$out"; then
  bad "a bad argument is not reported as an unreadable state" "$out"
else
  ok "a bad argument is not reported as an unreadable state"
fi

out=$(run take 683 agent-supervisor lane-2); rc=$?
want_exit "take also refuses a bare repo name before claiming" "$rc" 2
want_contains "take's refusal also names the bad argument" "not OWNER/NAME" "$out"

# too many slashes is just as malformed as none
out=$(run check 683 jonhill90/agent-supervisor/extra); rc=$?
want_exit "a repo with an extra slash is refused the same way" "$rc" 2
want_contains "the extra-slash refusal names the bad argument" "not OWNER/NAME" "$out"

# --- direction 2: a genuinely unreadable state still fails closed as before
out=$(run check 999999 jonhill90/agent-supervisor); rc=$?
want_exit "a well-shaped repo with a nonexistent issue still refuses" "$rc" 2
want_contains "it is still reported as unreadable, not an argument error" "cannot read" "$out"

# a well-shaped repo with a real issue is unaffected
out=$(run check 683 jonhill90/agent-supervisor); rc=$?
want_exit "a well-shaped repo with a real open issue is still claimable" "$rc" 0

# omitting [repo] entirely (gh resolves it from the working directory) must
# still work -- the validation only fires when [repo] is non-empty.
out=$(run list); rc=$?
want_exit "omitting [repo] entirely is unaffected" "$rc" 0

echo
echo "claim.sh [repo] argument shape (#694): $pass passed, $fail failed"
[ "$fail" -eq 0 ]
