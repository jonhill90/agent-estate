#!/bin/bash
# agent-supervisor#666: advance-live.sh's post-tick window re-reads
# watchdog.status's `checked:` line -- stamped ONCE, at watchdog.sh's own
# top, before this tick's own on_exit() checks (and before advance-live.sh's
# fetch + smoke test) ever run. Measured live: "recheck age 874s" and "920s"
# against a 150s window, on ticks whose smoke test PASSED -- and every
# logged attempt in the real advance-live.log refusing the same way, not a
# one-off. watchdog.sh's advance_on_exit now re-stamps `checked:` to "now"
# immediately before handing off to advance-live.sh (refresh_checked_for_
# advance), so the window bounds only advance-live.sh's own work again.
#
# Two real, end-to-end runs of the actual watchdog.sh, against a real git
# origin/LIVE, with stub tmux (SUPERVISOR_PATH, same shape test_watchdog.sh
# uses) standing in only for the pane read this test does not exercise:
#
#   1. SAME-TICK DELAY BEFORE advance-live.sh EVEN STARTS (the real defect):
#      a slow worktree-guard-audit.sh stand-in burns past the window before
#      on_exit() ever reaches advance_on_exit. Without the fix this always
#      skips (age measured from the tick's own start). With the fix, LIVE
#      still advances -- the checked: line was re-stamped right before the
#      hand-off, so only advance-live.sh's own (fast) fetch+smoke counts.
#
#   2. DELAY INSIDE advance-live.sh'S OWN WORK, AFTER THE REFRESH (the guard
#      that must still hold): a slow CANDIDATE watchdog.sh (what the smoke
#      test runs) sleeps past the window itself. This is unrelated to the
#      refresh and exercises advance-live.sh's own untouched re-check --
#      proving the fix does not remove the real protection, only the wrong
#      reference point.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
STUBS="$HERE/stubs"
pass=0; fail=0
say_ok()  { echo "  ok   $1"; pass=$((pass+1)); }
say_bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }

echo "watchdog.sh: agent-supervisor#666 -- advance-live.sh's post-tick window measured from the wrong clock"

# --- shared fixture: a real origin + a real LIVE worktree, never the estate's own
build_fixture() { # build_fixture <root-dir> -> sets SRC/LIVE/A as globals
  A="$1"
  git init -q --bare "$A/origin.git"
  git clone -q "$A/origin.git" "$A/src" 2>/dev/null
  SRC="$A/src"
  git -C "$SRC" config user.email t@e.com; git -C "$SRC" config user.name T
  git -C "$SRC" checkout -q -b main
  mkdir -p "$SRC/scripts/supervisor"
  for f in watchdog.sh watchdog-harness.sh watchdog-status.sh watchdog-checks.sh watchdog-advance.sh \
           advance-live.sh poller-window.sh poller-recover.sh session-defaults.sh \
           sleepcheck.py watchdog_notify.py loop-tick.md harness-registry.sh lanes.sh input-box.sh \
           dim-strip.sh poller-lib.sh worktree-guard-audit.sh; do
    cp "$SUP/$f" "$SRC/scripts/supervisor/"
  done
  cp -R "$SUP/harness" "$SRC/scripts/supervisor/"
  chmod +x "$SRC/scripts/supervisor/"*.sh
}

run_watchdog() { # run_watchdog <state-dir> <live-dir>
  SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE=idle STUB_SENT="$1/sent" \
  STUB_COUNTER="$1/counter" \
  SUPERVISOR_STATE="$1" SUPERVISOR_STATUS="$1/watchdog.status" SUPERVISOR_LOG="$1/watchdog.log" \
  SUPERVISOR_STAMP="$1/stamp" SUPERVISOR_HISTORY="$1/hist" NOTIFY_ENV="$1/none.env" \
  SUPERVISOR_LIVE="$2" SUPERVISOR_PANE="watchdog-666-test:1.1" \
  SLEEPCHECK_DIR="$1/transcripts" \
  ADVANCE_TICK_INTERVAL="$TICK_INTERVAL" ADVANCE_SAFETY_BUFFER="$SAFETY_BUFFER" \
  SUPERVISOR_SOURCE_SWEEP_STAMP="$1/.src-sweep-last" SUPERVISOR_LANE_SWEEP_STAMP="$1/.lane-sweep-last" \
  SUPERVISOR_GC_STAMP="$1/.gc-sweep-last" \
  SUPERVISOR_GUARD_AUDIT_STAMP="${STUB_GUARD_AUDIT_STAMP:-$1/.guard-audit-last}" \
  SUPERVISOR_GUARD_AUDIT_TIMEOUT="${STUB_GUARD_AUDIT_TIMEOUT:-120}" \
    bash "$2/scripts/supervisor/watchdog.sh" >"$1/stdout" 2>"$1/stderr"
}

TICK_INTERVAL=12
SAFETY_BUFFER=2
SAFE_UNTIL=$((TICK_INTERVAL - SAFETY_BUFFER))   # 10

# --- CASE 1: same-tick delay BEFORE advance-live.sh starts, fix engaged ----
A1=$(mktemp -d); build_fixture "$A1"
# A slow worktree-guard-audit.sh, committed to the base LIVE stays at, that
# burns well past SAFE_UNTIL before on_exit() ever reaches advance_on_exit --
# the exact shape measured live (worktree-guard-audit.sh hitting its own
# 120s timeout on 131-132 consecutive real ticks, agent-supervisor#666).
cat >"$SRC/scripts/supervisor/worktree-guard-audit.sh" <<EOF
#!/bin/bash
sleep $((SAFE_UNTIL + 3))
echo "worktree-guard-audit: 0 gaps, 0 unknowns (stand-in for #666)"
exit 0
EOF
chmod +x "$SRC/scripts/supervisor/worktree-guard-audit.sh"
git -C "$SRC" add -A >/dev/null 2>&1
git -C "$SRC" commit -q -m "base, slow worktree-guard-audit.sh"
git -C "$SRC" push -q -u origin main
LIVE1="$A1/live"
git -C "$SRC" worktree add -q --detach "$LIVE1" origin/main
before_sha1=$(git -C "$LIVE1" rev-parse HEAD)

# Target commit LIVE is behind by. The slow worktree-guard-audit.sh is only
# a fixture for the OUTER tick (the one this test measures); the smoke
# test's candidate runs its OWN on_exit() too (the trap always fires, even
# on the pane_unreadable short-circuit), so the target restores a fast one --
# otherwise the smoke test would eat the same delay a second time and this
# case would stop isolating "before advance-live.sh starts" from "inside it".
cat >"$SRC/scripts/supervisor/worktree-guard-audit.sh" <<'EOF'
#!/bin/bash
echo "worktree-guard-audit: 0 gaps, 0 unknowns (stand-in for #666)"
exit 0
EOF
echo marker >"$SRC/marker.txt"
git -C "$SRC" add marker.txt scripts/supervisor/worktree-guard-audit.sh
git -C "$SRC" commit -q -m "target commit LIVE should advance to"
git -C "$SRC" push -q origin main
target_sha1=$(git -C "$SRC" rev-parse origin/main)

D1="$A1/state"; mkdir -p "$D1"
run_watchdog "$D1" "$LIVE1"
after_sha1=$(git -C "$LIVE1" rev-parse HEAD)

if [ "$after_sha1" = "$target_sha1" ]; then
  say_ok "case 1: LIVE advances despite a slow on_exit check burning past the window"
else
  say_bad "case 1: LIVE advances despite a slow on_exit check burning past the window" \
    "still at ${after_sha1:0:12} (wanted ${target_sha1:0:12}); advance: $(grep '^advance:' "$D1/watchdog.status" 2>/dev/null)"
fi
if grep -q '^advance:  *advanced' "$D1/watchdog.status" 2>/dev/null; then
  say_ok "case 1: watchdog.status reports the advance, not a skip"
else
  say_bad "case 1: watchdog.status reports the advance, not a skip" \
    "$(grep '^advance:' "$D1/watchdog.status" 2>/dev/null)"
fi
if grep -qi "recheck age.*outside" "$D1/advance-live.log" 2>/dev/null; then
  say_bad "case 1: no stale-window refusal in advance-live.log" \
    "$(grep -i 'recheck age' "$D1/advance-live.log")"
else
  say_ok "case 1: no stale-window refusal in advance-live.log"
fi

git -C "$SRC" worktree remove --force "$LIVE1" >/dev/null 2>&1
rm -rf "$A1"

# --- CASE 2: delay INSIDE advance-live.sh's own smoke test, AFTER the refresh
A2=$(mktemp -d); build_fixture "$A2"
git -C "$SRC" add -A >/dev/null 2>&1
git -C "$SRC" commit -q -m "base, ordinary worktree-guard-audit.sh"
git -C "$SRC" push -q -u origin main
LIVE2="$A2/live"
git -C "$SRC" worktree add -q --detach "$LIVE2" origin/main
before_sha2=$(git -C "$LIVE2" rev-parse HEAD)

# Target commit: a CANDIDATE watchdog.sh that sleeps well past SAFE_UNTIL
# before writing a well-formed status -- so the smoke test PASSES (exactly
# the reported symptom: "its smoke test passed") but takes too long doing
# it. This is what advance-live.sh's own untouched re-check (immediately
# before the checkout) must still catch.
cat >"$SRC/scripts/supervisor/watchdog.sh" <<EOF
#!/bin/bash
set -uo pipefail
STATUS="\${SUPERVISOR_STATUS:?}"
mkdir -p "\$(dirname "\$STATUS")"
sleep $((SAFE_UNTIL + 3))
{
  printf 'checked:  %s\n' "\$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'state:    pane_unreadable\n'
} >"\$STATUS"
exit 0
EOF
git -C "$SRC" add scripts/supervisor/watchdog.sh
git -C "$SRC" commit -q -m "target commit, slow candidate watchdog.sh"
git -C "$SRC" push -q origin main
target_sha2=$(git -C "$SRC" rev-parse origin/main)

D2="$A2/state"; mkdir -p "$D2"
# Guard-audit stamp pre-seeded to "now" -- this case is about delay INSIDE
# advance-live.sh, not before it, so the other on_exit checks stay fast.
printf '%s' "$(date +%s)" >"$D2/.guard-audit-last"
STUB_GUARD_AUDIT_STAMP="$D2/.guard-audit-last" run_watchdog "$D2" "$LIVE2"
after_sha2=$(git -C "$LIVE2" rev-parse HEAD)

if [ "$after_sha2" = "$before_sha2" ]; then
  say_ok "case 2: LIVE is untouched when the smoke test itself overruns the window"
else
  say_bad "case 2: LIVE is untouched when the smoke test itself overruns the window" \
    "moved to ${after_sha2:0:12}"
fi
if grep -qi "recheck age.*outside" "$D2/advance-live.log" 2>/dev/null; then
  say_ok "case 2: the real post-tick window still refuses (recheck age reported)"
else
  say_bad "case 2: the real post-tick window still refuses (recheck age reported)" \
    "$(tail -5 "$D2/advance-live.log" 2>/dev/null)"
fi
if grep -q '^advance:  *not this tick' "$D2/watchdog.status" 2>/dev/null; then
  say_ok "case 2: watchdog.status reports a refusal, not a false advance"
else
  say_bad "case 2: watchdog.status reports a refusal, not a false advance" \
    "$(grep '^advance:' "$D2/watchdog.status" 2>/dev/null)"
fi

git -C "$SRC" worktree remove --force "$LIVE2" >/dev/null 2>&1
rm -rf "$A2"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
