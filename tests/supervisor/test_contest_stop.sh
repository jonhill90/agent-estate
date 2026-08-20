#!/bin/bash
# agent-supervisor#390 review: contest-stop.sh is 183 lines of new automation
# that live-dispatches a lane into the real estate on a regex match against
# pane text, with no human in the loop, and shipped with zero test coverage
# (`grep -rl contest tests/supervisor/` found nothing). This is the minimal
# coverage the review asked for: the dispatch call it makes when it decides
# to fire, the rate limit that keeps it from firing every tick, and the
# usage/failure edges around it.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC="$HERE/../../scripts/supervisor/contest-stop.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }

echo "contest-stop.sh -- trigger, dispatch args, and rate limit (#390)"

# contest-stop.sh calls "$HERE/dispatch.sh" (its own directory), not a PATH
# lookup, so it is copied into a scratch dir next to a stub dispatch.sh that
# records exactly the arguments it was called with.
D=$(mktemp -d)
cp "$SRC" "$D/contest-stop.sh"
chmod +x "$D/contest-stop.sh"

cat >"$D/dispatch.sh" <<'EOF'
#!/bin/bash
printf '%s\n' "$*" >"$DISPATCH_CALL_LOG"
exit "${DISPATCH_STUB_EXIT:-0}"
EOF
chmod +x "$D/dispatch.sh"

STATE_BASE="$D/state"

run() { # run <state-dir>
  local statedir="$1"; shift
  SUPERVISOR_STATE="$statedir" DISPATCH_CALL_LOG="$statedir/dispatch-call.log" \
    bash "$D/contest-stop.sh" "$@"
}

# --- usage: no --claim ------------------------------------------------
S1="$STATE_BASE/s1"; mkdir -p "$S1"
out=$(run "$S1" 2>&1); rc=$?
want_exit "no --claim: refuses (exit 2)" "$rc" 2 "$out"
want_contains "no --claim: prints usage" "usage: contest-stop.sh" "$out"

# --- --dry: writes the brief, does not dispatch ------------------------
S2="$STATE_BASE/s2"; mkdir -p "$S2"
out=$(run "$S2" --claim "nothing to dispatch" --evidence "tick 42" --dry 2>&1); rc=$?
want_exit "--dry: exits 0" "$rc" 0 "$out"
brief=$(tail -1 <<<"$out")
if [ -f "$brief" ]; then ok "--dry: brief file exists at printed path"; else bad "--dry: brief file exists at printed path" "$out"; fi
if grep -qF "nothing to dispatch" "$brief" 2>/dev/null; then ok "--dry: brief contains the claim"; else bad "--dry: brief contains the claim" "$(cat "$brief" 2>&1)"; fi
if grep -qF "tick 42" "$brief" 2>/dev/null; then ok "--dry: brief contains the evidence"; else bad "--dry: brief contains the evidence" "$(cat "$brief" 2>&1)"; fi
if [ -f "$S2/dispatch-call.log" ]; then bad "--dry: dispatch.sh is not invoked" "$(cat "$S2/dispatch-call.log")"; else ok "--dry: dispatch.sh is not invoked"; fi

# --- fires: dispatches with the agent-supervisor#150 issue reference,
#     the correct repo, and --not-a-review; stamps the rate-limit file ------
S3="$STATE_BASE/s3"; mkdir -p "$S3"
out=$(run "$S3" --claim "no phase 4 surface" 2>&1); rc=$?
want_exit "fires: exits 0" "$rc" 0 "$out"
call=$(cat "$S3/dispatch-call.log" 2>/dev/null || echo "<no call recorded>")
want_contains "fires: dispatches issue 150 (not the merged/unrelated 186)" "150 contest-stop" "$call"
want_contains "fires: dispatches into jonhill90/agent-supervisor" "jonhill90/agent-supervisor" "$call"
want_contains "fires: passes --not-a-review" "--not-a-review" "$call"
if [ -f "$S3/.contest-stop.last" ]; then ok "fires: stamps the rate-limit file"; else bad "fires: stamps the rate-limit file" "$out"; fi

# --- rate limit: a recent stamp suppresses the next fire ----------------
S4="$STATE_BASE/s4"; mkdir -p "$S4"
now=$(date -u +%s)
printf '%s' "$now" >"$S4/.contest-stop.last"
out=$(CONTEST_STOP_MIN_INTERVAL=3000 run "$S4" --claim "no phase 4 surface" 2>&1); rc=$?
want_exit "rate limit: still exits 0 (not an error, just not contesting yet)" "$rc" 0 "$out"
want_contains "rate limit: logs that it is not contesting again this soon" "not contesting again this soon" "$out"
if [ -f "$S4/dispatch-call.log" ]; then bad "rate limit: dispatch.sh is not invoked" "$(cat "$S4/dispatch-call.log")"; else ok "rate limit: dispatch.sh is not invoked"; fi

# --- rate limit expired: an old stamp does not suppress -----------------
S5="$STATE_BASE/s5"; mkdir -p "$S5"
old=$(( $(date -u +%s) - 4000 ))
printf '%s' "$old" >"$S5/.contest-stop.last"
out=$(CONTEST_STOP_MIN_INTERVAL=3000 run "$S5" --claim "no phase 4 surface" 2>&1); rc=$?
want_exit "rate limit expired: fires (exit 0)" "$rc" 0 "$out"
if [ -f "$S5/dispatch-call.log" ]; then ok "rate limit expired: dispatch.sh is invoked"; else bad "rate limit expired: dispatch.sh is invoked" "$out"; fi

# --- dispatch failure: does not stamp, so the next pass retries ---------
S6="$STATE_BASE/s6"; mkdir -p "$S6"
out=$(DISPATCH_STUB_EXIT=1 run "$S6" --claim "no phase 4 surface" 2>&1); rc=$?
want_exit "dispatch failure: exits 1" "$rc" 1 "$out"
if [ -f "$S6/.contest-stop.last" ]; then bad "dispatch failure: does NOT stamp the rate-limit file" "$out"; else ok "dispatch failure: does NOT stamp the rate-limit file"; fi

rm -rf "$D"

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
