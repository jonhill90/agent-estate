#!/bin/bash
# quota-watch.sh must be autonomous in BOTH directions and must not fire on a
# false edge (agent-supervisor#260).
#
# WHY THIS EXISTS. Measured 2026-08-16: the script correctly logged
# SAFE -> WINDDOWN at 09:40:13 and sent nothing (it only ever acted on
# WINDDOWN -> SAFE) -- the expensive half of the cycle stayed manual. Then the
# resume half turned out to be wrong too: it fired on ANY observed SAFE that
# differed from the previous *raw* reading, so a transient UNKNOWN (a
# codexbar timeout) sitting next to an unrelated SAFE read as a fake
# WINDDOWN -> SAFE edge. Four of five "QUOTA IS BACK" resumes it ever sent
# were false -- the estate was running, with work in flight, when they
# landed.
#
# Every case below is run with --once against a shared, persisted state dir,
# so each `run` call is exactly one tick of the real loop and the state
# machine has to get its edge detection right across ticks, not just within
# one process.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCH="$HERE/../../scripts/supervisor/quota-watch.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_count() {
  # $1 label, $2 needle, $3 file, $4 expected count
  local got; got=$(grep -cF -- "$2" "$3" 2>/dev/null || true)
  if [ "$got" = "$4" ]; then ok "$1"; else bad "$1" "expected $4 occurrence(s) of '$2' in $3, got $got:
$(cat "$3" 2>/dev/null)"; fi
}
want_empty() {
  # $1 label, $2 file -- asserts the file is absent or empty (nothing sent)
  if [ ! -s "$2" ]; then ok "$1"; else bad "$1" "expected no activity, got:
$(cat "$2")"; fi
}

echo "quota-watch.sh -- autonomous both directions, no false edges (#260)"

D=$(mktemp -d)
cp "$HERE/stubs/tmux-quota-watch" "$D/tmux"; chmod +x "$D/tmux"

gate() {
  # writes a one-shot quota gate stub that always exits with $1
  local path="$1" rc="$2"
  cat > "$path" <<FIX
#!/bin/bash
exit $rc
FIX
  chmod +x "$path"
}
gate "$D/gate-safe" 0        # rc 0 SAFE
gate "$D/gate-winddown" 1    # rc 1 WIND DOWN
gate "$D/gate-unknown2" 2    # rc 2 UNAVAILABLE
gate "$D/gate-unknown3" 3    # rc 3 MISSING
gate "$D/gate-127" 127       # rc 127 -- quota.sh absent from the deploy tree, #260
gate "$D/gate-124" 124       # rc 124 -- codexbar timeout, made first-class by #267
gate "$D/gate-42" 42         # rc 42 -- ported from #263: an arbitrary code the
                             # gate has never defined, not one of the curated
                             # ones above. Proves the `*)` wildcard, not a list
                             # of special cases this test happens to know about.

tick() {
  # $1 = quota gate path, uses $STATE and $LOG from caller scope
  PATH="$D:$PATH" QUOTA_GATE="$1" TMUX_LOG="$LOG" SUPERVISOR_STATE="$STATE" \
    QUOTA_WATCH_TARGET="t:@1" bash "$WATCH" --once >>"$STATE/quota-watch.out" 2>&1
}

# --- case 1: SAFE observed with no prior wind-down sends nothing ----------
STATE=$(mktemp -d "$D/state.XXXXXX"); LOG="$D/log.1"; : > "$LOG"
tick "$D/gate-safe"
want_empty "first-ever SAFE reading (no prior state) sends nothing" "$LOG"

# --- case 2: 0 -> 1 sends exactly one wind-down ----------------------------
tick "$D/gate-winddown"
want_count "the SAFE -> WIND DOWN edge sends exactly one wind-down" "QUOTA IS LOW" "$LOG" 1
tick "$D/gate-winddown"
want_count "...and staying in WIND DOWN does not resend it" "QUOTA IS LOW" "$LOG" 1
want_count "...and never sends a resume while still down" "QUOTA IS BACK" "$LOG" 0

# --- case 3: 1 -> 0 sends exactly one resume, because a wind-down WAS
#             recorded for this edge ---------------------------------------
tick "$D/gate-safe"
want_count "the WIND DOWN -> SAFE edge sends exactly one resume" "QUOTA IS BACK" "$LOG" 1
tick "$D/gate-safe"
want_count "...and staying SAFE does not resend it" "QUOTA IS BACK" "$LOG" 1
want_count "...and the wind-down count from case 2 is untouched" "QUOTA IS LOW" "$LOG" 1

# --- case 4: a single rc=2 between two SAFE readings sends nothing ---------
# Measured 2026-08-16: quota.sh returned rc=2 UNAVAILABLE (timeout) once,
# then three consecutive SAFE readings seconds later. That UNKNOWN blip must
# not be read as a wind-down (nothing to send) and must not make the SAFE
# readings either side of it look like a fake resume edge.
STATE=$(mktemp -d "$D/state.XXXXXX"); LOG="$D/log.4"; : > "$LOG"
tick "$D/gate-safe"
tick "$D/gate-unknown2"
tick "$D/gate-safe"
want_empty "SAFE, rc=2 UNKNOWN, SAFE sends nothing at any step" "$LOG"

# --- case 4b: same, but the UNKNOWN sits between a real wind-down and the
#              real resume -- the resume must still fire once the transient
#              clears, not be swallowed by it -----------------------------
STATE=$(mktemp -d "$D/state.XXXXXX"); LOG="$D/log.4b"; : > "$LOG"
tick "$D/gate-safe"
tick "$D/gate-winddown"
tick "$D/gate-unknown3"
tick "$D/gate-safe"
want_count "wind-down survives a transient UNKNOWN and still sends once" "QUOTA IS LOW" "$LOG" 1
want_count "...and the resume still fires once the UNKNOWN clears" "QUOTA IS BACK" "$LOG" 1

# --- case 5: rc=127 is unsafe, not SAFE and not WIND DOWN ------------------
# agent-supervisor#260: quota.sh is absent from the live deployment tree and
# a caller shelling out to it there gets bash's own exit 127. That must land
# in the same UNKNOWN bucket as rc=2/3, never be read as either defined state.
STATE=$(mktemp -d "$D/state.XXXXXX"); LOG="$D/log.5"; : > "$LOG"
tick "$D/gate-safe"
tick "$D/gate-127"
want_empty "rc=127 next to a prior SAFE sends nothing (not read as WIND DOWN)" "$LOG"
tick "$D/gate-winddown"
want_count "...and a genuine wind-down right after still sends exactly one" "QUOTA IS LOW" "$LOG" 1
tick "$D/gate-127"
tick "$D/gate-safe"
want_count "...and rc=127 between WIND DOWN and SAFE does not block the resume" "QUOTA IS BACK" "$LOG" 1

# --- case 6: rc=124 is unsafe too, not SAFE and not WIND DOWN --------------
# #267 makes 124 (codexbar timeout, normalised through `timeout`) a first-class
# UNAVAILABLE shape. The `*)` wildcard already lands it in UNKNOWN by not
# matching 0 or 1, but that was implied, not tested -- test it explicitly
# rather than leaving 124 to ride on the same coverage as an untested code.
STATE=$(mktemp -d "$D/state.XXXXXX"); LOG="$D/log.6"; : > "$LOG"
tick "$D/gate-safe"
tick "$D/gate-124"
want_empty "rc=124 next to a prior SAFE sends nothing (not read as WIND DOWN)" "$LOG"
tick "$D/gate-winddown"
want_count "...and a genuine wind-down right after still sends exactly one" "QUOTA IS LOW" "$LOG" 1
tick "$D/gate-124"
tick "$D/gate-safe"
want_count "...and rc=124 between WIND DOWN and SAFE does not block the resume" "QUOTA IS BACK" "$LOG" 1

# --- case 7: an arbitrary, never-enumerated code is UNKNOWN too ------------
# Ported from #263 (test_quota_watch_standdown.sh), whose own version this
# test does not otherwise cover: every case above uses a code this file
# already knows the meaning of (2, 3, 124, 127). rc=42 is not one of those --
# it exists only to prove the `*)` branch catches ANY code this script has
# never seen, not just the ones this suite happens to enumerate.
STATE=$(mktemp -d "$D/state.XXXXXX"); LOG="$D/log.7"; : > "$LOG"
tick "$D/gate-safe"
tick "$D/gate-42"
want_empty "an arbitrary never-enumerated code (rc=42) sends nothing" "$LOG"
tick "$D/gate-winddown"
want_count "...and a genuine wind-down right after still sends exactly one" "QUOTA IS LOW" "$LOG" 1
tick "$D/gate-42"
tick "$D/gate-safe"
want_count "...and rc=42 between WIND DOWN and SAFE does not block the resume" "QUOTA IS BACK" "$LOG" 1

echo
echo "quota-watch: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
