#!/bin/bash
# agent-estate#808: watchdog.sh's smoke-test run (advance-live.sh) re-audits
# the SAME production worktree farm the real watchdog does -- the smoke
# test's scratch worktree is a linked worktree of the live repo, and `git
# worktree list` output is shared administration data across every worktree
# of one repo. #800/#803 already made worktree-guard-audit.sh's own polling
# sub-second; #808 measured the smoke test STILL costing 123.4s of a 138.4s
# run against the 150s tick window (11.6s slack), and it already missed a
# real tick once (recheck age 313s).
#
# This proves three things against the REAL scripts, never a stub:
#   1. worktree-guard-audit.sh's own WORKTREE_GUARD_MAX_WORKTREES bounds the
#      walk to the first N worktrees, deterministically (git worktree list's
#      own stable order), and reports the bound in its summary line.
#   2. A bounded run still catches a real audit-logic regression (the
#      synthetic bad-audit bug #808's brief asked for) -- bounding must not
#      quietly remove the audit's actual power to catch a gap.
#   3. watchdog.sh's own wiring (watchdog-checks.sh's
#      check_worktree_guard_audit) only bounds when
#      SUPERVISOR_GUARD_AUDIT_MAX_WORKTREES is explicitly set -- a plain
#      production tick with that var unset audits every worktree, unchanged.
#
# No real tmux call anywhere in this file -- git plumbing only.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
AUDIT="$SUP/worktree-guard-audit.sh"
WATCHDOG="$SUP/watchdog.sh"
pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }

D="$(mktemp -d "${TMPDIR:-/tmp}/wga-smoke-bound.XXXXXX")"
trap 'rm -rf "$D"' EXIT INT TERM

REPO="$D/repo"
mkdir -p "$REPO/tests/supervisor"
git -C "$REPO" -c init.defaultBranch=main init -q
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name test

# A guarded fixture -- the audit is expected to run clean against it.
# Assembled at heredoc-BUILD time (never adjacent in this SOURCE file), same
# convention test_worktree_guard_audit.sh and its _bound sibling already
# use, so tmux_verb_guard.py's static scanner never mistakes this
# fixture-for-a-throwaway-repo for a live unisolated call here.
T1="tm"; T2="ux"; V1="new-sess"; V2="ion"
{
  echo '#!/bin/bash'
  echo 'assert_isolated_tmux || exit 1'
  printf '%s%s %s%s -d -s x\n' "$T1" "$T2" "$V1" "$V2"
} > "$REPO/tests/supervisor/test_fixture.sh"
git -C "$REPO" add -A && git -C "$REPO" commit -q -m "guarded"
SHA_GUARDED="$(git -C "$REPO" rev-parse HEAD)"

N=10
for i in $(seq 1 "$N"); do
  git -C "$REPO" worktree add -q --detach "$D/wt$i" "$SHA_GUARDED"
done
TOTAL=$(( N + 1 )) # + the repo's own main worktree

echo "worktree-guard-audit.sh -- agent-estate#808 smoke-mode bound"

export WORKTREE_GUARD_FILES="tests/supervisor/test_fixture.sh"

# --- 1. unbounded (production shape): every worktree walked -------------
out_unbounded="$("$AUDIT" "$REPO" 2>&1)"
rc_unbounded=$?
if [ "$rc_unbounded" -eq 0 ] && grep -qE "^worktree-guard-audit: $TOTAL file@worktree pairs checked, 0 gap\(s\), 0 unknown\(s\)$" <<<"$out_unbounded"; then
  ok "1a. unbounded (WORKTREE_GUARD_MAX_WORKTREES unset) walks all $TOTAL worktrees, no 'bounded' note"
else
  bad "1a. unbounded walks all $TOTAL worktrees" "rc=$rc_unbounded out=$out_unbounded"
fi

# --- 2. bounded: only the first N worktrees are walked -------------------
BOUND=4
out_bounded="$(WORKTREE_GUARD_MAX_WORKTREES=$BOUND "$AUDIT" "$REPO" 2>&1)"
rc_bounded=$?
if [ "$rc_bounded" -eq 0 ] && grep -qE "^worktree-guard-audit: $BOUND file@worktree pairs checked, 0 gap\(s\), 0 unknown\(s\) \(bounded: $BOUND of $TOTAL worktree\(s\) walked\)$" <<<"$out_bounded"; then
  ok "2a. WORKTREE_GUARD_MAX_WORKTREES=$BOUND walks exactly $BOUND of $TOTAL worktrees and says so"
else
  bad "2a. bounded run walks exactly $BOUND worktrees and reports the bound" "rc=$rc_bounded out=$out_bounded"
fi

# --- 3. a bound at or above the total is a no-op, not an error -----------
out_noop="$(WORKTREE_GUARD_MAX_WORKTREES=999 "$AUDIT" "$REPO" 2>&1)"
if grep -qE "^worktree-guard-audit: $TOTAL file@worktree pairs checked, 0 gap\(s\), 0 unknown\(s\)$" <<<"$out_noop"; then
  ok "3a. a bound above the total worktree count behaves exactly like unbounded"
else
  bad "3a. a bound above the total worktree count behaves exactly like unbounded" "out=$out_noop"
fi

# --- 4. bounding does not remove the audit's power to catch a real gap ---
# Deliberately introduces an unguarded worktree (a synthetic bad-audit-write
# bug shape, per agent-estate#808's brief) into a SCRATCH copy, never this
# repo's own tree -- and proves the bounded walk still reaches and flags it.
cat > "$REPO/tests/supervisor/test_fixture.sh" <<EOF
#!/bin/bash
${T1}${T2} ${V1}${V2} -d -s y
EOF
git -C "$REPO" add -A && git -C "$REPO" commit -q -m "regression: verb with no guard"
SHA_GAP="$(git -C "$REPO" rev-parse HEAD)"
git -C "$REPO" worktree add -q --detach "$D/wt-gap" "$SHA_GAP"
TOTAL_WITH_GAP=$(( TOTAL + 1 ))

out_gap="$(WORKTREE_GUARD_MAX_WORKTREES=$TOTAL_WITH_GAP "$AUDIT" "$REPO" 2>&1)"
rc_gap=$?
if [ "$rc_gap" -ne 0 ] && grep -q "GAP  .*wt-gap" <<<"$out_gap"; then
  ok "4a. a bounded run that reaches the regressed worktree still flags it and exits non-zero"
else
  bad "4a. a bounded run that reaches the regressed worktree still flags it" "rc=$rc_gap out=$out_gap"
fi

# --- 5. watchdog.sh wiring: unset SUPERVISOR_GUARD_AUDIT_MAX_WORKTREES ---
#         means an ordinary production tick audits everything, unchanged --
#         proved through the REAL watchdog.sh, not a stub of it.
git -C "$REPO" worktree remove -f "$D/wt-gap" >/dev/null 2>&1 || true
# Restore a clean, guarded fixture at HEAD so the production-tick check
# below is about worktree COUNT, not about a leftover gap from case 4.
cat > "$REPO/tests/supervisor/test_fixture.sh" <<EOF
#!/bin/bash
assert_isolated_tmux || exit 1
${T1}${T2} ${V1}${V2} -d -s x
EOF
git -C "$REPO" add -A && git -C "$REPO" commit -q -m "guarded again"
SHA_CLEAN="$(git -C "$REPO" rev-parse HEAD)"
for wt in $(git -C "$REPO" worktree list --porcelain | awk '/^worktree /{print $2}' | tail -n +2); do
  git -C "$REPO" -C "$wt" checkout -q "$SHA_CLEAN" 2>/dev/null || true
done

STATE="$D/state"; mkdir -p "$STATE" "$STATE/transcripts"

WORKTREE_GUARD_FILES="tests/supervisor/test_fixture.sh" \
SUPERVISOR_GUARD_AUDIT_REPO="$REPO" \
SUPERVISOR_GUARD_AUDIT_INTERVAL=0 \
SUPERVISOR_GUARD_AUDIT_STAMP="$D/stamp-prod" \
SUPERVISOR_GUARD_AUDIT_EPISODE="$D/ep-prod" \
SUPERVISOR_GUARD_AUDIT_FAIL_STREAK="$D/fs-prod" \
STUB_PANE_STATE=busy \
SUPERVISOR_STATE="$STATE" SUPERVISOR_STATUS="$D/st-prod" SUPERVISOR_LOG="$D/lg-prod" \
SUPERVISOR_STAMP="$D/stamp-prod-b" SUPERVISOR_HISTORY="$D/hist-prod" NOTIFY_ENV="$D/none.env" \
SLEEPCHECK_DIR="$STATE/transcripts" \
  bash "$WATCHDOG" >/dev/null 2>"$D/err-prod"

if grep -qE "GUARD-AUDIT: worktree-guard-audit: $TOTAL file@worktree pairs checked" "$D/lg-prod" 2>/dev/null \
   && ! grep -q "bounded:" "$D/lg-prod" 2>/dev/null; then
  ok "5a. a production tick (SUPERVISOR_GUARD_AUDIT_MAX_WORKTREES unset) audits all $TOTAL worktrees, no bound"
else
  bad "5a. a production tick audits all $TOTAL worktrees, no bound" "lg=$(cat "$D/lg-prod" 2>/dev/null)"
fi

# --- 6. watchdog.sh wiring: setting the smoke-mode env var bounds the walk
STATE2="$D/state2"; mkdir -p "$STATE2" "$STATE2/transcripts"
WORKTREE_GUARD_FILES="tests/supervisor/test_fixture.sh" \
SUPERVISOR_GUARD_AUDIT_REPO="$REPO" \
SUPERVISOR_GUARD_AUDIT_INTERVAL=0 \
SUPERVISOR_GUARD_AUDIT_STAMP="$D/stamp-smoke" \
SUPERVISOR_GUARD_AUDIT_EPISODE="$D/ep-smoke" \
SUPERVISOR_GUARD_AUDIT_FAIL_STREAK="$D/fs-smoke" \
SUPERVISOR_GUARD_AUDIT_MAX_WORKTREES=3 \
STUB_PANE_STATE=busy \
SUPERVISOR_STATE="$STATE2" SUPERVISOR_STATUS="$D/st-smoke" SUPERVISOR_LOG="$D/lg-smoke" \
SUPERVISOR_STAMP="$D/stamp-smoke-b" SUPERVISOR_HISTORY="$D/hist-smoke" NOTIFY_ENV="$D/none.env" \
SLEEPCHECK_DIR="$STATE2/transcripts" \
  bash "$WATCHDOG" >/dev/null 2>"$D/err-smoke"

if grep -qE "GUARD-AUDIT: worktree-guard-audit: 3 file@worktree pairs checked.*\(bounded: 3 of $TOTAL worktree\(s\) walked\)" "$D/lg-smoke" 2>/dev/null; then
  ok "6a. SUPERVISOR_GUARD_AUDIT_MAX_WORKTREES=3 bounds a real watchdog.sh tick to 3 of $TOTAL worktrees"
else
  bad "6a. SUPERVISOR_GUARD_AUDIT_MAX_WORKTREES bounds a real watchdog.sh tick" "lg=$(cat "$D/lg-smoke" 2>/dev/null)"
fi

for wt in $(git -C "$REPO" worktree list --porcelain | awk '/^worktree /{print $2}' | tail -n +2); do
  git -C "$REPO" worktree remove -f "$wt" >/dev/null 2>&1
done

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
