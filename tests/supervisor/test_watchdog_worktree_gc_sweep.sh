#!/bin/bash
# agent-supervisor#526: `worktree.sh gc` existed with nothing calling it --
# worktree.sh's own header says wiring it in is "a separate decision for
# whoever owns the Director tick, not bundled into landing the tool". This is
# that decision, wired the same way #199/#205 wired worktree-guard-audit.sh:
# through this watchdog, which already runs unattended outside the loop.
#
# Same discipline test_watchdog_worktree_guard_audit.sh already uses: this
# only ever runs watchdog.sh itself, exactly as the LaunchAgent would,
# against a disposable throwaway repo built here -- never against the real
# agent-supervisor worktree farm. Proving the wiring, not re-deriving
# branch_content_is_on_base's own correctness (test_worktree.sh already
# covers that function directly); this suite's job is: does the sweep
# actually reach it, in both directions, dry-run and live.
#
# Mutation-checked BOTH directions, by construction, not by reading the
# script:
#   1. A genuinely landed worktree (2 real commits, squash-merged into main,
#      the exact #526-brief shape) is reported disposable in dry-run and
#      actually removed once SUPERVISOR_GC_LIVE=1.
#   2. A worktree with one real unlanded commit is reported NOT disposable in
#      both modes and is never touched.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCHDOG="$HERE/../../scripts/supervisor/watchdog.sh"
STUBS="$HERE/stubs"
pass=0; fail=0

D=$(mktemp -d)
trap 'rm -rf "$D"' EXIT

# --- build the throwaway repo -------------------------------------------
REPO="$D/repo"
mkdir -p "$REPO"
git -C "$REPO" -c init.defaultBranch=main init -q
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name test
echo base > "$REPO/file.txt"
git -C "$REPO" add -A && git -C "$REPO" -c commit.gpgsign=false commit -q -m base

# Candidate A: two real commits, later squash-merged into main -- the
# multi-commit squash shape the #526 brief measured (eval-266/PR#267).
git -C "$REPO" checkout -qb lane/landed-multi
echo change1 >> "$REPO/file.txt"
git -C "$REPO" commit -qam c1
echo change2 >> "$REPO/file.txt"
git -C "$REPO" commit -qam c2
git -C "$REPO" checkout -q main
git -C "$REPO" merge -q --squash lane/landed-multi
git -C "$REPO" commit -q -m "squashed: lane/landed-multi"

# Candidate B: one real commit, never merged -- the ordinary in-progress
# case. Branched from main's current (post-squash) tip so it starts clean.
git -C "$REPO" checkout -qb lane/unlanded
echo "unlanded work" >> "$REPO/file.txt"
git -C "$REPO" commit -qam "unlanded work"
git -C "$REPO" checkout -q main

WT_LANDED="$D/wt-landed"
WT_UNLANDED="$D/wt-unlanded"
git -C "$REPO" worktree add -q "$WT_LANDED" lane/landed-multi
git -C "$REPO" worktree add -q "$WT_UNLANDED" lane/unlanded

run() {
  SUPERVISOR_GC_REPOS="$REPO" \
  SUPERVISOR_GC_BASE=main \
  SUPERVISOR_GC_INTERVAL=0 \
  SUPERVISOR_GC_STAMP="$D/gc-stamp" \
  SUPERVISOR_GC_LIVE="${GC_LIVE_FOR_RUN:-}" \
  WORKTREE_GC_MIN_AGE_SECONDS=0 \
  SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" \
  STUB_PANE_STATE=busy \
  SUPERVISOR_STATE="$D" SUPERVISOR_STATUS="$D/st" SUPERVISOR_LOG="$D/lg" \
  SUPERVISOR_STAMP="$D/stamp" SUPERVISOR_HISTORY="$D/hist" NOTIFY_ENV="$D/none.env" \
  SLEEPCHECK_DIR="$D/transcripts" \
  bash "$WATCHDOG" >/dev/null 2>"$D/err"
}
mkdir -p "$D/transcripts"

echo "watchdog.sh -- #526 worktree-gc sweep wiring"

# --- dry-run (the shipped default) --------------------------------------
GC_LIVE_FOR_RUN=""
run

if grep -qE '^worktree-gc: mode=dry-run removed=1 skipped=1' "$D/st" 2>/dev/null; then
  echo "  ok   dry-run reports 1 disposable (landed), 1 kept (unlanded)"; pass=$((pass+1))
else
  echo "  FAIL dry-run summary wrong:"; sed 's/^/       /' "$D/st" 2>/dev/null; fail=$((fail+1))
fi

if [ -d "$WT_LANDED" ] && [ -d "$WT_UNLANDED" ]; then
  echo "  ok   dry-run changed nothing on disk"; pass=$((pass+1))
else
  echo "  FAIL dry-run removed a worktree from disk (WT_LANDED=$([ -d "$WT_LANDED" ] && echo present || echo GONE), WT_UNLANDED=$([ -d "$WT_UNLANDED" ] && echo present || echo GONE))"; fail=$((fail+1))
fi

# --- live (SUPERVISOR_GC_LIVE=1) ----------------------------------------
GC_LIVE_FOR_RUN=1
run

if grep -qE '^worktree-gc: mode=live removed=1 skipped=1' "$D/st" 2>/dev/null; then
  echo "  ok   live run reports removing the landed worktree, keeping the unlanded one"; pass=$((pass+1))
else
  echo "  FAIL live summary wrong:"; sed 's/^/       /' "$D/st" 2>/dev/null; fail=$((fail+1))
fi

if [ ! -d "$WT_LANDED" ]; then
  echo "  ok   the genuinely landed worktree was actually removed"; pass=$((pass+1))
else
  echo "  FAIL the landed worktree is still on disk after a live sweep"; fail=$((fail+1))
fi

if [ -d "$WT_UNLANDED" ]; then
  echo "  ok   the worktree with unlanded work was left alone"; pass=$((pass+1))
else
  echo "  FAIL the unlanded worktree was removed -- this would have destroyed real work"; fail=$((fail+1))
fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
