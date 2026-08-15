#!/bin/bash
# agent-supervisor#144 MUTATION CHECK, both directions -- claim.sh's own half
# of the deadlock the issue names: `claim.sh check`'s old GraphQL read
# (`gh issue view`) failed closed once the shared GraphQL budget hit
# 0/5000, and `dispatch.sh` correctly refused to dispatch on that refusal --
# so the fix for GraphQL exhaustion could not itself be dispatched while
# GraphQL was exhausted.
#
# Direction 1: GraphQL exhausted, REST alive -- claim.sh must still work.
# Direction 2: REST also unreachable -- claim.sh must still refuse rather
# than treat an unreadable answer as permissive (the shape #59/#92/#95 were
# all the same bug of).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAIM="$HERE/../../scripts/supervisor/claim.sh"
pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

D=$(mktemp -d); mkdir -p "$D/bin"
cp "$HERE/stubs/tmux-lanes" "$D/bin/tmux"
cp "$HERE/stubs/ps-lanes" "$D/bin/ps"
cat > "$D/issues" <<'FIX'
28|| GraphQL budget exhausted
FIX
: > "$D/prs"
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
FIX

echo "claim.sh -- REST-vs-GraphQL mutation check (#144)"

# --- direction 1: GraphQL exhausted, REST alive ----------------------------
cp "$HERE/stubs/gh-claim" "$D/bin/gh-claim"
cp "$HERE/stubs/gh-claim-graphql-exhausted" "$D/bin/gh"
run() { PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
        LANES_FIXTURE="$D/lanes" LANES_SESSION=t bash "$CLAIM" "$@" 2>&1; }

out=$(run check 28 acme/repo); rc=$?
if [ "$rc" = "0" ]; then ok "check succeeds with GraphQL exhausted (REST alive)"; else bad "check succeeds with GraphQL exhausted" "$out"; fi

out=$(run take 28 acme/repo lane-9); rc=$?
if [ "$rc" = "0" ]; then ok "take succeeds with GraphQL exhausted (REST alive)"; else bad "take succeeds with GraphQL exhausted" "$out"; fi

out=$(run list acme/repo)
if grep -qx '28' <<<"$out"; then bad "list withholds the now-claimed issue" "$out"; else ok "list withholds the now-claimed issue, with GraphQL exhausted"; fi

out=$(run stale acme/repo)
if grep -qx '28' <<<"$out"; then ok "stale reports the claim as a candidate (no live lane), with GraphQL exhausted"; else bad "stale reports the claim" "$out"; fi

out=$(run release 28 acme/repo); rc=$?
if [ "$rc" = "0" ]; then ok "release succeeds with GraphQL exhausted (REST alive)"; else bad "release succeeds with GraphQL exhausted" "$out"; fi

# --- direction 2: REST unreachable too --------------------------------------
# A gh that fails everything, GraphQL or REST -- must not be mistaken for
# "nothing claimed". claim.sh must refuse (non-zero, no assignment written),
# never proceed as if the read had answered "open, unclaimed".
printf '#!/bin/bash\necho "gh: connection failed" >&2\nexit 1\n' > "$D/bin/gh"
chmod +x "$D/bin/gh"
out=$(run check 28 acme/repo); rc=$?
if [ "$rc" = "2" ]; then ok "check refuses (exit 2) when REST is also unreachable"; else bad "check refuses when REST is also unreachable" "exit $rc: $out"; fi

out=$(run take 28 acme/repo lane-9); rc=$?
if [ "$rc" -ne 0 ]; then ok "take refuses when REST is also unreachable"; else bad "take refuses when REST is also unreachable" "$out"; fi

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
