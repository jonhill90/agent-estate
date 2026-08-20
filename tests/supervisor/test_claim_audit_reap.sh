#!/bin/bash
# agent-supervisor#359: `release` on every terminal completion path can never
# be the whole fix -- a killed tmux server or a SIGKILLed lane runs no
# cleanup at all. `claim.sh audit`/`claim.sh reap` are the reaper that catches
# what that still misses: the same liveness signal `stale` already computes
# (a claimed issue with no live-looking lane and no open PR), reported with a
# `stale=<n>` count `audit` can be grepped for, and actually released --
# loudly, one line per release -- by `reap`.
#
# The issue's own acceptance check is exactly:
#   bash scripts/supervisor/claim.sh audit 2>/dev/null | grep -q "stale=0"
#
# BOTH must refuse outright, releasing nothing and reporting nothing, when
# either underlying signal (`lanes.sh`, or the open-PR read) could not be
# read at all -- an unreadable signal is UNKNOWN, and #359 is explicit that
# releasing a live claim by mistake (UNKNOWN treated as safe) destroys
# in-flight work, the worse failure than leaving a stale claim in place a
# tick longer.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAIM="$HERE/../../scripts/supervisor/claim.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

echo "claim.sh audit/reap (#359)"

D=$(mktemp -d); mkdir -p "$D/bin"
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cp "$HERE/stubs/tmux-lanes" "$D/bin/tmux"
cp "$HERE/stubs/ps-lanes" "$D/bin/ps"

cat > "$D/issues" <<'FIX'
28|| Duplicate identities
70|| Nothing marks an issue as claimed
57|| Name the agent hierarchy
FIX
: > "$D/prs"

# lanes fixture: index|name|command|status-line|seconds-since-output|in-mode
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
2|ad70-claim-signal|claude.exe|esc to interrupt 3s|1|0
3|free-3|claude.exe|❯ ready|1|0
4|ad57-hierarchy|zsh|❯ |1|0
FIX

run() { PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
        LANES_FIXTURE="$D/lanes" LANES_SESSION=t bash "$CLAIM" "$@" 2>&1; }

# --- setup: three claimed issues, three different liveness shapes ---------
run take 70 acme/repo lane-2 >/dev/null   # window 2 is ad70-*, busy -- LIVE
run take 57 acme/repo lane-4 >/dev/null   # window 4 is ad57-*, dead -- STALE
run take 28 acme/repo lane-9 >/dev/null   # no window mentions 28 at all -- STALE

# --- audit: reports, never releases ----------------------------------------
out=$(run audit acme/repo); rc=$?
want_contains "audit reports the count" "stale=2" "$out"
want_contains "audit names the dead-lane claim" "#57" "$out"
want_contains "audit names the no-lane claim" "#28" "$out"
want_missing "audit does not name the live claim" "#70" "$out"
want_exit "audit exits non-zero while any claim is stale" "$rc" 1 "$out"

out=$(run check 28 acme/repo); rc=$?
want_exit "audit itself released nothing -- #28 is still claimed" "$rc" 1 "$out"
out=$(run check 57 acme/repo); rc=$?
want_exit "audit itself released nothing -- #57 is still claimed" "$rc" 1 "$out"

# --- reap: releases every stale claim, logging each one --------------------
out=$(run reap acme/repo); rc=$?
want_contains "reap logs the release of #57" "released #57" "$out"
want_contains "reap logs the release of #28" "released #28" "$out"
want_missing "reap never releases the live claim #70" "released #70" "$out"
want_contains "reap reports its own count" "reaped=2 failed=0" "$out"
want_exit "reap succeeds when every stale claim releases cleanly" "$rc" 0 "$out"

out=$(run check 28 acme/repo); rc=$?
want_exit "reap actually released #28" "$rc" 0 "$out"
out=$(run check 57 acme/repo); rc=$?
want_exit "reap actually released #57" "$rc" 0 "$out"
out=$(run check 70 acme/repo); rc=$?
want_exit "reap left the live claim #70 alone" "$rc" 1 "$out"

# --- a clean estate reports stale=0, the issue's own acceptance check -----
out=$(run audit acme/repo); rc=$?
want_contains "a clean estate's audit reads stale=0" "stale=0" "$out"
want_exit "a clean estate's audit exits 0" "$rc" 0 "$out"

# --- fail closed: an unreadable lanes.sh releases and reports nothing -----
# `lanes.sh` fails when it cannot resolve a fixture at all (LANES_FIXTURE
# pointed somewhere empty is the simplest way to make its own read fail
# rather than merely report zero windows -- LANES_FIXTURE unset falls back to
# a real tmux server, which this sandbox has none of, so the underlying
# `tmux list-windows` call itself fails).
echo '199|| A fourth issue, claimed after the sweep above' >> "$D/issues"
run take 199 acme/repo lane-9 >/dev/null
out=$(PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
      LANES_SESSION=t bash "$CLAIM" audit acme/repo 2>&1); rc=$?
want_missing "an unreadable lanes.sh never reports a stale count" "stale=" "$out"
want_exit "audit refuses outright when lanes.sh cannot be read" "$rc" 2 "$out"

out=$(PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
      LANES_SESSION=t bash "$CLAIM" reap acme/repo 2>&1); rc=$?
want_missing "an unreadable lanes.sh never releases anything" "released #" "$out"
want_exit "reap refuses outright when lanes.sh cannot be read" "$rc" 2 "$out"
out=$(run check 199 acme/repo); rc=$?
want_exit "the claim reap could not verify liveness for stays claimed" "$rc" 1 "$out"

echo
echo "claim.sh audit/reap: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
