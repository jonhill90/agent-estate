#!/bin/bash
# acceptance.sh's central risk: a nonzero exit from a CLOSED issue's own test
# must not be treated as "the symptom is back" when the test never actually
# ran to a verdict (timeout, or the block's own EX_TEMPFAIL signal). Only a
# real assertion failure may reopen the issue. Reviewed on PR #327.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ACCEPTANCE="$HERE/../../scripts/supervisor/acceptance.sh"
pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 -- $2"; fail=$((fail+1)); }

D=$(mktemp -d); mkdir -p "$D/bin"
trap 'rm -rf "$D"' EXIT INT TERM

# A gh stub that serves one issue's state/body from files this test writes,
# and records reopen/comment calls so we can assert they did or didn't fire.
cat > "$D/bin/gh" <<'STUB'
#!/bin/bash
log="$GH_STUB_DIR/calls.log"
case "$1 $2" in
  "issue view")
    num="$3"
    for a in "$@"; do :; done
    if printf '%s\n' "$*" | grep -q -- '--json state'; then
      cat "$GH_STUB_DIR/state-$num" 2>/dev/null
    elif printf '%s\n' "$*" | grep -q -- '--json body'; then
      cat "$GH_STUB_DIR/body-$num" 2>/dev/null
    fi
    ;;
  "issue reopen")
    echo "reopen $3" >> "$log"
    ;;
  "issue comment")
    echo "comment $3" >> "$log"
    ;;
esac
exit 0
STUB
chmod +x "$D/bin/gh"

run() {
  local num="$1" rc
  PATH="$D/bin:$PATH" GH_STUB_DIR="$D" ACCEPTANCE_TIMEOUT="${TIMEOUT:-5}" \
    bash "$ACCEPTANCE" --issue "$num" --repo test/repo "${@:2}" >"$D/out" 2>&1
  rc=$?
  cat "$D/out"
  return "$rc"
}

block() { printf '```acceptance\n%s\n```' "$1"; }

# 1. Closed issue, block exits 75 (EX_TEMPFAIL) -> ENVIRONMENT, never reopened.
: > "$D/calls.log"
echo CLOSED > "$D/state-1"
block 'exit 75' > "$D/body-1"
out=$(run 1 --reopen); rc=$?
[ "$rc" -eq 0 ] && ok "exit 75 does not fail the run" || bad "exit 75 run rc" "$rc"
grep -q "ENVIRONMENT" <<<"$out" && ok "exit 75 is reported ENVIRONMENT" || bad "exit 75 ENVIRONMENT" "$out"
grep -q "REGRESSED" <<<"$out" && bad "exit 75 must not say REGRESSED" "$out" || ok "exit 75 is not called REGRESSED"
[ -s "$D/calls.log" ] && bad "exit 75 must not reopen" "$(cat "$D/calls.log")" || ok "exit 75 never calls gh issue reopen"

# 2. Closed issue, block times out (124) -> ENVIRONMENT, never reopened.
: > "$D/calls.log"
echo CLOSED > "$D/state-2"
block 'sleep 3' > "$D/body-2"
out=$(TIMEOUT=1 run 2 --reopen); rc=$?
[ "$rc" -eq 0 ] && ok "timeout does not fail the run" || bad "timeout run rc" "$rc"
grep -q "ENVIRONMENT" <<<"$out" && ok "timeout is reported ENVIRONMENT" || bad "timeout ENVIRONMENT" "$out"
[ -s "$D/calls.log" ] && bad "timeout must not reopen" "$(cat "$D/calls.log")" || ok "timeout never calls gh issue reopen"

# 3. Closed issue, block genuinely fails (exit 3) -> REGRESSED, reopen fires.
: > "$D/calls.log"
echo CLOSED > "$D/state-3"
block 'exit 3' > "$D/body-3"
out=$(run 3 --reopen); rc=$?
[ "$rc" -eq 1 ] && ok "real regression exits 1" || bad "real regression rc" "$rc"
grep -q "REGRESSED" <<<"$out" && ok "real failure is reported REGRESSED" || bad "real failure REGRESSED" "$out"
grep -q "^reopen 3$" "$D/calls.log" && ok "real regression calls gh issue reopen" || bad "real regression reopens" "$(cat "$D/calls.log")"

# 4. Open issue failing its own test is not news, and never reopened.
: > "$D/calls.log"
echo OPEN > "$D/state-4"
block 'exit 3' > "$D/body-4"
out=$(run 4 --reopen); rc=$?
[ "$rc" -eq 0 ] && ok "open failure does not fail the run" || bad "open failure rc" "$rc"
grep -q "expected, the work is not done" <<<"$out" && ok "open failure is reported as outstanding work" \
  || bad "open failure message" "$out"
[ -s "$D/calls.log" ] && bad "open failure must not reopen" "$(cat "$D/calls.log")" || ok "open failure never calls gh issue reopen"

echo "acceptance: $pass ok, $fail failed"
[ "$fail" -eq 0 ]
