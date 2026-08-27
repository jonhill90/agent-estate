#!/bin/bash
# agent-supervisor#666's own defect, isolated: advance-live.sh's post-smoke-
# test race gate used to compare ELAPSED WALL TIME (watchdog_age, read
# against the calling tick's own frozen checked: line) against a fixed
# TICK_INTERVAL-SAFETY_BUFFER budget. That budget is trivially exhausted by
# the gate's OWN necessary work -- a `git worktree add` plus a full second
# watchdog.sh run -- with zero concurrency involved. Measured live (this
# issue's own repro, see advance-live.sh's own baseline_checked comment):
# a single on_exit sub-check alone hit its own 120s internal timeout.
# Historically (advance-live.log) 166 of 347 non-current advance attempts
# (47.8%) were rejected this way, clustered just above the old 150s budget.
#
# The fix diffs the RAW checked: VALUE captured at entry against a fresh
# read immediately before the mutation, instead of comparing an absolute
# age. This suite proves BOTH directions deterministically, using tiny
# ADVANCE_TICK_INTERVAL/ADVANCE_SAFETY_BUFFER overrides (a 6s safety budget)
# and a candidate that sleeps 5s past most of it -- no real multi-minute
# smoke test needed to reproduce the shape.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ADVANCE="$HERE/../../scripts/supervisor/advance-live.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }

echo "advance-live.sh -- #666 race gate (checked-value diff, not elapsed age)"

D=$(mktemp -d)
git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/src"
SRC="$D/src"
git -C "$SRC" config user.email test@example.com
git -C "$SRC" config user.name "Test"
git -C "$SRC" checkout -q -b main
mkdir -p "$SRC/scripts/supervisor"

# A candidate that sleeps for 5s before writing its own well-formed scratch
# status -- standing in for a slow but HONEST smoke test (the common case:
# a real watchdog tick's own on_exit battery, or the smoke test's own
# duplicate of it, simply takes a while). It never touches the OUTER
# $SUPERVISOR_STATUS (the live status file advance-live.sh itself reads),
# so nothing has genuinely changed by the time it finishes.
cat >"$SRC/scripts/supervisor/watchdog.sh" <<'EOF'
#!/bin/bash
set -uo pipefail
sleep 5
STATUS="${SUPERVISOR_STATUS:?}"
mkdir -p "$(dirname "$STATUS")"
{
  printf 'checked:  %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'state:    pane_unreadable\n'
} >"$STATUS"
exit 0
EOF
chmod +x "$SRC/scripts/supervisor/watchdog.sh"
git -C "$SRC" add -A
git -C "$SRC" commit -q -m "slow but honest watchdog.sh"
git -C "$SRC" push -q -u origin main

LIVE="$D/live"
git -C "$SRC" worktree add -q --detach "$LIVE" origin/main

# A second commit so LIVE has something real to advance to.
echo two >"$SRC/file.txt"
git -C "$SRC" add file.txt
git -C "$SRC" commit -q -m "second commit"
git -C "$SRC" push -q origin main
git -C "$LIVE" fetch -q origin main
target_sha=$(git -C "$LIVE" rev-parse origin/main)
before_sha=$(git -C "$LIVE" rev-parse HEAD)

# A TINY window (safe_until = 10 - 4 = 6s) so the candidate's 5s sleep, plus
# ordinary fetch/worktree-add overhead, blows the old budget without needing
# a real multi-minute smoke test.
run() { # run <state-dir>
  ADVANCE_TICK_INTERVAL=10 ADVANCE_SAFETY_BUFFER=4 \
  SUPERVISOR_STATE="$1" bash "$ADVANCE" "$LIVE"
}

# =====================================================================
# Direction 1 (the fix): fresh entry, nothing genuinely changes during a
# smoke test that outlives the tiny window -- MUST advance.
# =====================================================================
S=$(mktemp -d)
printf 'checked:  %s\nstate:    working\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$S/watchdog.status"
out=$(run "$S" 2>&1); rc=$?
want_exit "honest-but-slow candidate: advance-live.sh exits 0" "$rc" 0 "$out"
after=$(git -C "$LIVE" rev-parse HEAD)
if [ "$after" = "$target_sha" ]; then
  ok "honest-but-slow candidate: LIVE advances despite outliving the old fixed window"
else
  bad "honest-but-slow candidate: LIVE advances despite outliving the old fixed window" "at $after, wanted $target_sha (out: $out)"
fi

# --- mutation: the SAME fixture against the PRE-FIX gate, printing the changed line
# Reconstruct the pre-#666 gate: a straight age check against a frozen
# baseline, exactly what shipped before this PR. Pinned to the merge-base
# with origin/main (not a bare HEAD) so this stays correct once this
# branch's own fix is committed -- HEAD would then BE the fix.
PRE_FIX="$D/advance-live-pre666.sh"
PRE_FIX_SHA=$(git -C "$HERE/../.." merge-base HEAD origin/main 2>/dev/null)
if [ -n "$PRE_FIX_SHA" ] && git -C "$HERE/../.." show "$PRE_FIX_SHA:scripts/supervisor/advance-live.sh" > "$PRE_FIX" 2>/dev/null; then
  chmod +x "$PRE_FIX"
  # Reset LIVE back behind target so there is something to (not) advance.
  git -C "$LIVE" checkout -q --detach "$before_sha"
  S2=$(mktemp -d)
  printf 'checked:  %s\nstate:    working\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$S2/watchdog.status"
  pre_out=$(ADVANCE_TICK_INTERVAL=10 ADVANCE_SAFETY_BUFFER=4 SUPERVISOR_STATE="$S2" bash "$PRE_FIX" "$LIVE" 2>&1); pre_rc=$?
  pre_after=$(git -C "$LIVE" rev-parse HEAD)
  echo "  (pre-fix HEAD gate against the same fixture: exit=$pre_rc, live at $([ "$pre_after" = "$target_sha" ] && echo ADVANCED || echo UNCHANGED))"
  if [ "$pre_after" != "$target_sha" ]; then
    ok "mutation confirmed: the pre-fix gate (HEAD) refuses this exact honest-but-slow fixture (the assertion above would be red on it)"
  else
    bad "mutation confirmed: the pre-fix gate (HEAD) refuses this exact honest-but-slow fixture" "pre-fix gate also advanced -- fixture does not isolate the fix ($pre_out)"
  fi
  # restore for the rest of this suite
  git -C "$LIVE" checkout -q --detach "$target_sha"
else
  bad "mutation setup: read HEAD's advance-live.sh for the pre-fix comparison" "git show failed"
fi

# =====================================================================
# Direction 2 (the guard still guards): fresh entry, but the candidate
# genuinely rewrites the OUTER checked: value mid-run (a real different
# tick reporting) -- MUST still refuse, even with the diff-based gate.
# =====================================================================
echo three >"$SRC/file.txt"
git -C "$SRC" add file.txt
cat >"$SRC/scripts/supervisor/watchdog.sh" <<'EOF'
#!/bin/bash
set -uo pipefail
STATUS="${SUPERVISOR_STATUS:?}"
mkdir -p "$(dirname "$STATUS")"
printf 'checked:  %s\nstate:    pane_unreadable\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$STATUS"
# Standing in for a genuinely different, concurrent real tick: rewrite the
# OUTER live watchdog.status (not this candidate's own scratch one) with a
# DIFFERENT checked: value while the smoke test is "running".
if [ -n "${TEST_OUTER_STATUS:-}" ]; then
  printf 'checked:  %s\nstate:    working\n' "$(date -u -v+5S +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '5 seconds' +%Y-%m-%dT%H:%M:%SZ)" >"$TEST_OUTER_STATUS"
fi
exit 0
EOF
git -C "$SRC" add scripts/supervisor/watchdog.sh
git -C "$SRC" commit -q -m "candidate that mutates the outer status mid-run"
git -C "$SRC" push -q origin main
git -C "$LIVE" fetch -q origin main
before_sha2=$(git -C "$LIVE" rev-parse HEAD)

S3=$(mktemp -d)
printf 'checked:  %s\nstate:    working\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$S3/watchdog.status"
export TEST_OUTER_STATUS="$S3/watchdog.status"
out3=$(run "$S3" 2>&1); rc3=$?
unset TEST_OUTER_STATUS
want_exit "genuinely-changed status mid-run: exits 0 (skip, not a hard failure)" "$rc3" 0 "$out3"
after3=$(git -C "$LIVE" rev-parse HEAD)
if [ "$after3" = "$before_sha2" ]; then
  ok "genuinely-changed status mid-run: LIVE is NOT advanced -- the guard still refuses on real evidence"
else
  bad "genuinely-changed status mid-run: LIVE is NOT advanced -- the guard still refuses on real evidence" "moved to $after3, wanted to stay at $before_sha2"
fi
want_contains "the refusal names the mechanism (changed, not merely elapsed)" "watchdog status changed while the smoke test ran" "$out3"

echo
echo "advance-live.sh #666 race gate: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
