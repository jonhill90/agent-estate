#!/bin/bash
# weekly-watch.sh must page Jon exactly once per threshold per week, must
# tolerate a flaky codexbar sample without missing a threshold, must never
# silently mark a failed page as sent, and must write a liveness heartbeat
# on EVERY invocation -- success or failure -- so a hung process, a broken
# meter, or a launchd job that silently stopped firing reads as DEAD rather
# than as healthy (agent-supervisor#327, #341).
#
# Same discipline as test_quota_watch_blind_alarm.sh: the real script runs
# with --once-per-invocation semantics (it has no loop of its own -- launchd
# ticks it), so each `tick` call below is exactly one real invocation, and
# the state machine has to get its dedup right across ticks, not just
# within one process.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC="$HERE/../../scripts/supervisor/weekly-watch.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_count() {
  local got; got=$(grep -cF -- "$2" "$3" 2>/dev/null || true)
  if [ "$got" = "$4" ]; then ok "$1"; else bad "$1" "expected $4 occurrence(s) of '$2' in $3, got $got:
$(cat "$3" 2>/dev/null)"; fi
}
want_empty() {
  if [ ! -s "$2" ]; then ok "$1"; else bad "$1" "expected no activity, got:
$(cat "$2")"; fi
}

echo "weekly-watch.sh -- one page per threshold per week, no silent-sent on failure (#327), heartbeat on every run (#341)"

D=$(mktemp -d)
cp "$SRC" "$D/weekly-watch.sh"; chmod +x "$D/weekly-watch.sh"
cp "$HERE/stubs/notify-weekly-watch" "$D/notify.sh"; chmod +x "$D/notify.sh"
cp "$HERE/stubs/codexbar-weekly-watch" "$D/codexbar"; chmod +x "$D/codexbar"

RESET1="2026-08-24T00:00:00Z"
RESET2="2026-08-31T00:00:00Z"

tick() {
  # $1 = comma-separated codexbar sample sequence for this one invocation.
  # Each real invocation is a fresh process, so the stub's sample counter
  # must restart at 1 every tick, not accumulate across ticks.
  rm -f "$CTR"
  WEEKLY_TEST_SEQUENCE="$1" WEEKLY_TEST_COUNTER="$CTR" \
    PATH="$D:$PATH" SUPERVISOR_STATE="$STATE" \
    bash "$D/weekly-watch.sh" >>"$STATE/weekly-watch.out" 2>&1
  rc=$?
}

# --- case 1: below both thresholds never pages ----------------------------
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.1"; : > "$NLOG"; CTR="$STATE/ctr"
NOTIFY_LOG="$NLOG" tick "50|$RESET1|on_pace"
want_empty "50% used pages nobody" "$NLOG"

# --- case 2: crossing the low threshold pages exactly once, and a repeat
# tick at the same percent does not re-page ---------------------------------
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.2"; : > "$NLOG"; CTR="$STATE/ctr"
NOTIFY_LOG="$NLOG" tick "90|$RESET1|on_pace"
want_count "crossing 90% pages exactly once" "SUBJECT=Weekly quota at 90%" "$NLOG" 1
NOTIFY_LOG="$NLOG" tick "90|$RESET1|on_pace"
want_count "an identical repeat reading (same week, same percent) does not re-page" "SUBJECT=Weekly quota at 90%" "$NLOG" 1
NOTIFY_LOG="$NLOG" tick "91|$RESET1|on_pace"
want_count "staying above 90% (same week) does not re-page" "SUBJECT=Weekly quota at 90%" "$NLOG" 1

# --- case 3: crossing 97% later in the same week pages a SECOND, distinct
# message, and repeats there stay quiet too ---------------------------------
NOTIFY_LOG="$NLOG" tick "97|$RESET1|on_pace"
want_count "crossing 97% sends the second, more urgent page" "SUBJECT=Weekly quota nearly gone - 97%" "$NLOG" 1
NOTIFY_LOG="$NLOG" tick "98|$RESET1|on_pace"
want_count "staying above 97% does not re-send the 97% page" "SUBJECT=Weekly quota nearly gone - 97%" "$NLOG" 1
want_count "...and the 90% page total is still exactly one across the whole week" "SUBJECT=Weekly quota at 90%" "$NLOG" 1

# --- case 4: a new week (resetsAt changes) re-arms both thresholds --------
NOTIFY_LOG="$NLOG" tick "94|$RESET2|on_pace"
want_count "a new week's crossing pages again -- not suppressed forever" "SUBJECT=Weekly quota at 94%" "$NLOG" 1

# --- case 5: a flaky first sample does not cost the threshold -- the retry
# loop must find the first clean read within the same invocation -----------
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.5"; : > "$NLOG"; CTR="$STATE/ctr"
NOTIFY_LOG="$NLOG" tick "empty,bad,95|$RESET1|on_pace"
want_count "two bad samples then a clean one still pages the crossed threshold" "SUBJECT=Weekly quota at 95%" "$NLOG" 1

# --- case 6: three straight bad samples cannot read the meter -- must
# exit nonzero and page nobody (UNKNOWN is never treated as fine) ----------
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.6"; : > "$NLOG"; CTR="$STATE/ctr"
NOTIFY_LOG="$NLOG" tick "empty,bad,empty"
[ "$rc" -eq 2 ] && ok "three failed samples exits 2" || bad "three failed samples exits 2" "got rc=$rc"
want_empty "...and pages nobody" "$NLOG"

# --- case 6a (#341): the failed-read path must STILL write a heartbeat --
# this is the exact gap the issue names: "$STATE isn't even created on a
# failed read -- the only persisted state, $STAMP, is written exclusively
# inside the successful-page branch." A hung process, a broken meter, and a
# dead launchd job all looked identical to "quiet, below threshold" before
# this stamp existed on this path.
HB="$STATE/.weekly-watch.state"
if [ -f "$HB" ]; then ok "a failed read still writes a heartbeat file"
else bad "a failed read still writes a heartbeat file" "no $HB after an all-samples-failed run"; fi
if grep -q '^checked: ' "$HB" 2>/dev/null; then ok "...with a checked: timestamp"
else bad "...with a checked: timestamp" "$(cat "$HB" 2>/dev/null)"; fi
if grep -q '^state: unknown' "$HB" 2>/dev/null; then ok "...and state: unknown, distinct from a clean read"
else bad "...and state: unknown, distinct from a clean read" "$(cat "$HB" 2>/dev/null)"; fi

# --- case 7: a page that fails to send must NOT be recorded as fired --
# the next tick has to retry it, or a real page silently never reaches Jon -
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.7"; : > "$NLOG"; CTR="$STATE/ctr"
NOTIFY_LOG="$NLOG" NOTIFY_FAIL=1 tick "90|$RESET1|on_pace"
want_count "a failed channel is still attempted on the triggering tick" "SUBJECT=Weekly quota at 90%" "$NLOG" 1
NOTIFY_LOG="$NLOG" tick "90|$RESET1|on_pace"
want_count "...so the next tick retries the unsent page" "SUBJECT=Weekly quota at 90%" "$NLOG" 2

# --- case 8 (#341): a successful read (paged or not) writes a heartbeat
# distinctly marked "ok", not just the failed path from case 6a -----------
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.8"; : > "$NLOG"; CTR="$STATE/ctr"
NOTIFY_LOG="$NLOG" tick "50|$RESET1|on_pace"
HB="$STATE/.weekly-watch.state"
if grep -q '^state: ok' "$HB" 2>/dev/null; then ok "a clean read below threshold still writes state: ok"
else bad "a clean read below threshold still writes state: ok" "$(cat "$HB" 2>/dev/null)"; fi

# --- case 9 (#341): the heartbeat is a SEPARATE file from $STAMP (the
# per-threshold page dedup) -- it must exist even on a run that never pages
# and never touches $STAMP at all.
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.9"; : > "$NLOG"; CTR="$STATE/ctr"
NOTIFY_LOG="$NLOG" tick "10|$RESET1|on_pace"
if [ ! -f "$STATE/.weekly-watch.paged" ] && [ -f "$STATE/.weekly-watch.state" ]; then
  ok "a run that pages nobody still leaves a heartbeat, even with no page-dedup stamp"
else
  bad "a run that pages nobody still leaves a heartbeat, even with no page-dedup stamp" \
    "paged=$([ -f "$STATE/.weekly-watch.paged" ] && echo present || echo absent) heartbeat=$([ -f "$STATE/.weekly-watch.state" ] && echo present || echo absent)"
fi

echo
echo "weekly-watch: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
