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
CLEANUP_PIDS=""
cleanup() {
  local pid
  for pid in $CLEANUP_PIDS; do kill "$pid" 2>/dev/null; done
  for pid in $CLEANUP_PIDS; do wait "$pid" 2>/dev/null; done
  rm -rf "$D"
}
trap cleanup EXIT

have_lsof() { command -v lsof >/dev/null 2>&1 || [ -x /usr/sbin/lsof ]; }
# Backdate a worktree DIRECTORY's own mtime -- _gc_mtime reads the
# directory's mtime, not a file inside it. Done with python3's os.utime
# rather than `touch -t`/`touch -d` so the same call works identically on
# macOS (BSD touch, no -d) and Linux (GNU touch), the same portability
# stance the mutation blocks below already take with python3.
backdate() {   # backdate <path> <seconds-ago>
  python3 -c 'import os,sys,time; t=time.time()-float(sys.argv[2]); os.utime(sys.argv[1], (t, t))' "$1" "$2"
}

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

# Inside $REPO/.worktrees/ -- gc's scope filter (agent-supervisor#527
# follow-up) refuses anything registered outside it, before any
# liveness/age/dirty/merged check runs. A fixture at a sibling path (as this
# suite used before that filter existed) would now be silently skipped as
# out-of-scope and every assertion below would read as "kept" for the wrong
# reason.
mkdir -p "$REPO/.worktrees"
WT_LANDED="$REPO/.worktrees/wt-landed"
WT_UNLANDED="$REPO/.worktrees/wt-unlanded"
git -C "$REPO" worktree add -q "$WT_LANDED" lane/landed-multi
git -C "$REPO" worktree add -q "$WT_UNLANDED" lane/unlanded

# WATCHDOG_FOR_RUN lets the mutation sections below run a patched COPY of the
# whole scripts/supervisor directory through this exact same entry point --
# watchdog.sh resolves its own sibling worktree.sh via `dirname
# "${BASH_SOURCE[0]}"`, so proving a mutation "through the sweep" means
# swapping which watchdog.sh (and therefore which worktree.sh beside it)
# actually runs, not just calling worktree.sh directly (test_worktree.sh
# already does that part).
run() {
  SUPERVISOR_GC_REPOS="$REPO" \
  SUPERVISOR_GC_BASE=main \
  SUPERVISOR_GC_INTERVAL=0 \
  SUPERVISOR_GC_STAMP="$D/gc-stamp" \
  SUPERVISOR_GC_LIVE="${GC_LIVE_FOR_RUN:-}" \
  WORKTREE_GC_MIN_AGE_SECONDS="${AGE_SECONDS_FOR_RUN:-0}" \
  SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" \
  STUB_PANE_STATE=busy \
  SUPERVISOR_STATE="$D" SUPERVISOR_STATUS="$D/st" SUPERVISOR_LOG="$D/lg" \
  SUPERVISOR_STAMP="$D/stamp" SUPERVISOR_HISTORY="$D/hist" NOTIFY_ENV="$D/none.env" \
  SLEEPCHECK_DIR="$D/transcripts" \
  bash "${WATCHDOG_FOR_RUN:-$WATCHDOG}" >/dev/null 2>"$D/err"
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


# --- AGE BOUNDARY, through the sweep -------------------------------------
# worktree.sh's own age floor is already covered directly by test_worktree.sh
# (candidates G/H there). This proves the SAME guard is actually reached by
# THIS entry point (watchdog.sh -> worktree.sh gc), the same way the
# CLEAN/DIRTY fixtures above prove branch_content_is_on_base is reached by
# it, rather than assuming a guard proven in isolation is also wired in here.
#
# MEASURED, not assumed: a single full `watchdog.sh` call through this
# suite's own `run()` helper takes ~124s end-to-end (timed directly against a
# nonexistent SUPERVISOR_GC_REPOS entry, so the gc sweep itself does
# essentially nothing -- the 124s is almost entirely watchdog.sh's OTHER
# checks -- guard-audit, heartbeat, lane sweeps -- that run before
# check_worktree_gc_sweep does). A first draft of this test copied
# test_worktree.sh's own numbers (5s floor, 2s/10s margins), which are only
# safe calling worktree.sh directly (near-instant there): through THIS
# entry point, a single intervening run() call alone burns through a 5s
# margin, so the "young" fixture read as already older than the floor by
# the time gc actually checked it, and was (correctly) removed -- a TEST
# bug, not a production one: the age check read the real mtime correctly,
# the test's assumed timing did not hold for this entry point.
#
# Fixed with a 300s floor and margins sized against the measured ~124s
# cost, with real headroom, not a slight bump: the "old enough" fixture is
# backdated to 100000s (~28h) so it stays unambiguously past the floor no
# matter how many slow run() calls happen before it is actually evaluated.
# The "young" fixture is (re)backdated to a fresh 60s just before the ONE
# call its own assertion depends on (see below), rather than once up front
# shared with the dry-run call -- backdating once for both would let TWO
# calls' worth of overhead (~248s measured) accumulate against a floor of
# only 300s, reproducing the exact same flake at a larger scale.
git -C "$REPO" checkout -qb lane/age-under
echo "age-under change" >> "$REPO/file-age-under.txt"
git -C "$REPO" add file-age-under.txt
git -C "$REPO" -c commit.gpgsign=false commit -qm "age-under work"
git -C "$REPO" checkout -q main
git -C "$REPO" merge -q --squash lane/age-under
git -C "$REPO" -c commit.gpgsign=false commit -q -m "squashed: lane/age-under"

git -C "$REPO" checkout -qb lane/age-over
echo "age-over change" >> "$REPO/file-age-over.txt"
git -C "$REPO" add file-age-over.txt
git -C "$REPO" -c commit.gpgsign=false commit -qm "age-over work"
git -C "$REPO" checkout -q main
git -C "$REPO" merge -q --squash lane/age-over
git -C "$REPO" -c commit.gpgsign=false commit -q -m "squashed: lane/age-over"

WT_AGE_UNDER="$REPO/.worktrees/wt-age-under"
WT_AGE_OVER="$REPO/.worktrees/wt-age-over"
git -C "$REPO" worktree add -q "$WT_AGE_UNDER" lane/age-under
git -C "$REPO" worktree add -q "$WT_AGE_OVER" lane/age-over
backdate "$WT_AGE_OVER" 100000

AGE_FLOOR=300
AGE_SECONDS_FOR_RUN=$AGE_FLOOR
GC_LIVE_FOR_RUN=""
run
if [ -d "$WT_AGE_UNDER" ] && [ -d "$WT_AGE_OVER" ]; then
  echo "  ok   dry-run (age boundary) changed nothing on disk"; pass=$((pass+1))
else
  echo "  FAIL dry-run (age boundary) removed something"; fail=$((fail+1))
fi

# (Re)backdated fresh, immediately before the one call this assertion
# depends on -- see the comment above for why sharing one backdate with the
# dry-run call above it would not survive two calls' overhead against a
# 300s floor.
backdate "$WT_AGE_UNDER" 60
GC_LIVE_FOR_RUN=1
run
if [ -d "$WT_AGE_UNDER" ]; then
  echo "  ok   live sweep keeps a clean/merged worktree younger than the age floor"; pass=$((pass+1))
else
  echo "  FAIL live sweep removed a worktree younger than the age floor"; fail=$((fail+1))
fi
if [ ! -d "$WT_AGE_OVER" ]; then
  echo "  ok   live sweep removes the same shape once it clears the age floor"; pass=$((pass+1))
else
  echo "  FAIL live sweep left a worktree in place once it cleared the age floor"; fail=$((fail+1))
fi

# MUTATION: through the sweep, disable the age floor entirely -> the
# still-young WT_AGE_UNDER must go RED (removed), proving the "kept" result
# above is pinned to the age guard actually running in THIS entry point, not
# to something else (occupancy, the merge predicate) already refusing it.
MUT_AGE_DIR="$D/supervisor-mutant-age"
cp -R "$HERE/../../scripts/supervisor" "$MUT_AGE_DIR"
mut_rc=0
python3 - "$MUT_AGE_DIR/worktree.sh" <<'PY' || mut_rc=$?
import sys
path = sys.argv[1]
text = open(path).read()
marker = '  if [ "$age" -lt "$GC_MIN_AGE_SECONDS" ]; then'
assert marker in text, "age-floor condition not found -- script shape changed"
assert text.count(marker) == 1, "age-floor condition not unique -- script shape changed"
open(path, "w").write(text.replace(marker, "  if false; then", 1))
PY
if [ "$mut_rc" -ne 0 ]; then
  echo "  FAIL setup: patched a mutant copy of scripts/supervisor with the age floor disabled -- could not patch (exit $mut_rc)"; fail=$((fail+1))
else
  echo "  ok   setup: patched a mutant copy of scripts/supervisor with the age floor disabled"; pass=$((pass+1))
  WATCHDOG_FOR_RUN="$MUT_AGE_DIR/watchdog.sh" AGE_SECONDS_FOR_RUN=$AGE_FLOOR GC_LIVE_FOR_RUN=1 run
  if [ ! -d "$WT_AGE_UNDER" ]; then
    echo "  ok   mutation confirmed: disabling the age floor lets the sweep remove a worktree younger than it (the 'kept' assertion above would now be RED)"; pass=$((pass+1))
  else
    echo "  FAIL mutation confirmed: disabling the age floor lets the sweep remove a worktree younger than it -- expected removal on the mutant, still present"; fail=$((fail+1))
  fi
fi

# --- IN-USE (liveness), through the sweep --------------------------------
# Same posture: worktree.sh's own process-cwd occupancy guard is already
# mutation-proven directly by test_worktree.sh (candidate K there); this
# proves watchdog.sh's sweep actually reaches it too, with a REAL running
# process, not a mocked "looks occupied" flag.
if have_lsof; then
  git -C "$REPO" checkout -qb lane/inuse-yes
  echo "inuse-yes change" >> "$REPO/file-inuse-yes.txt"
  git -C "$REPO" add file-inuse-yes.txt
  git -C "$REPO" -c commit.gpgsign=false commit -qm "inuse-yes work"
  git -C "$REPO" checkout -q main
  git -C "$REPO" merge -q --squash lane/inuse-yes
  git -C "$REPO" -c commit.gpgsign=false commit -q -m "squashed: lane/inuse-yes"

  git -C "$REPO" checkout -qb lane/inuse-no
  echo "inuse-no change" >> "$REPO/file-inuse-no.txt"
  git -C "$REPO" add file-inuse-no.txt
  git -C "$REPO" -c commit.gpgsign=false commit -qm "inuse-no work"
  git -C "$REPO" checkout -q main
  git -C "$REPO" merge -q --squash lane/inuse-no
  git -C "$REPO" -c commit.gpgsign=false commit -q -m "squashed: lane/inuse-no"

  WT_INUSE_YES="$REPO/.worktrees/wt-inuse-yes"
  WT_INUSE_NO="$REPO/.worktrees/wt-inuse-no"
  git -C "$REPO" worktree add -q "$WT_INUSE_YES" lane/inuse-yes
  git -C "$REPO" worktree add -q "$WT_INUSE_NO" lane/inuse-no

  ( cd "$WT_INUSE_YES" && exec sleep 90 ) &
  INUSE_PID=$!
  CLEANUP_PIDS="$CLEANUP_PIDS $INUSE_PID"
  sleep 1
  if kill -0 "$INUSE_PID" 2>/dev/null; then
    echo "  ok   verify the instrument: a real process is running with its cwd inside the in-use candidate"; pass=$((pass+1))
  else
    echo "  FAIL verify the instrument: a real process is running with its cwd inside the in-use candidate -- background sleep did not start"; fail=$((fail+1))
  fi

  AGE_SECONDS_FOR_RUN=0
  GC_LIVE_FOR_RUN=""
  run
  if [ -d "$WT_INUSE_YES" ] && [ -d "$WT_INUSE_NO" ]; then
    echo "  ok   dry-run (in-use) changed nothing on disk"; pass=$((pass+1))
  else
    echo "  FAIL dry-run (in-use) removed something"; fail=$((fail+1))
  fi

  GC_LIVE_FOR_RUN=1
  run
  if [ -d "$WT_INUSE_YES" ]; then
    echo "  ok   live sweep refuses to remove a worktree a real process's cwd is inside"; pass=$((pass+1))
  else
    echo "  FAIL live sweep removed a worktree a real process's cwd is inside"; fail=$((fail+1))
  fi
  if [ ! -d "$WT_INUSE_NO" ]; then
    echo "  ok   live sweep removes the same shape with no occupying process"; pass=$((pass+1))
  else
    echo "  FAIL live sweep left a worktree with no occupying process in place"; fail=$((fail+1))
  fi

  # Real code, no mutation: once the occupying process is actually gone, the
  # same worktree becomes free to collect.
  kill "$INUSE_PID" 2>/dev/null; wait "$INUSE_PID" 2>/dev/null
  run
  if [ ! -d "$WT_INUSE_YES" ]; then
    echo "  ok   live sweep removes the same worktree once the occupying process is actually gone (real code, no mutation)"; pass=$((pass+1))
  else
    echo "  FAIL live sweep left the worktree in place after its occupying process actually exited"; fail=$((fail+1))
  fi

  # MUTATION: through the sweep, disable the process-cwd occupancy check on a
  # FRESH candidate (the previous one was just genuinely removed above --
  # reusing it would prove nothing about the mutant) -> a real running
  # process inside it must be ignored and the worktree removed.
  git -C "$REPO" checkout -qb lane/inuse-mut
  echo "inuse-mut change" >> "$REPO/file-inuse-mut.txt"
  git -C "$REPO" add file-inuse-mut.txt
  git -C "$REPO" -c commit.gpgsign=false commit -qm "inuse-mut work"
  git -C "$REPO" checkout -q main
  git -C "$REPO" merge -q --squash lane/inuse-mut
  git -C "$REPO" -c commit.gpgsign=false commit -q -m "squashed: lane/inuse-mut"
  WT_INUSE_MUT="$REPO/.worktrees/wt-inuse-mut"
  git -C "$REPO" worktree add -q "$WT_INUSE_MUT" lane/inuse-mut

  ( cd "$WT_INUSE_MUT" && exec sleep 90 ) &
  INUSE_MUT_PID=$!
  CLEANUP_PIDS="$CLEANUP_PIDS $INUSE_MUT_PID"
  sleep 1

  MUT_PROC_DIR="$D/supervisor-mutant-proc"
  cp -R "$HERE/../../scripts/supervisor" "$MUT_PROC_DIR"
  mut_rc=0
  python3 - "$MUT_PROC_DIR/worktree.sh" <<'PY' || mut_rc=$?
import sys
path = sys.argv[1]
text = open(path).read()
marker = '  _gc_process_refs "$target_real"; rc=$?'
assert marker in text, "process-cwd call not found -- script shape changed"
assert text.count(marker) == 1, "process-cwd call not unique -- script shape changed"
open(path, "w").write(text.replace(marker, '  _gc_process_refs "$target_real"; rc=$?; rc=1', 1))
PY
  if [ "$mut_rc" -ne 0 ]; then
    echo "  FAIL setup: patched a mutant copy of scripts/supervisor with the process-cwd check forced to 'not occupied' -- could not patch (exit $mut_rc)"; fail=$((fail+1))
  else
    echo "  ok   setup: patched a mutant copy of scripts/supervisor with the process-cwd check forced to 'not occupied'"; pass=$((pass+1))
    if kill -0 "$INUSE_MUT_PID" 2>/dev/null; then
      WATCHDOG_FOR_RUN="$MUT_PROC_DIR/watchdog.sh" AGE_SECONDS_FOR_RUN=0 GC_LIVE_FOR_RUN=1 run
      if [ ! -d "$WT_INUSE_MUT" ]; then
        echo "  ok   mutation confirmed: disabling the process-cwd check lets the sweep remove a worktree a real process is sitting in (the refusal above would now be RED)"; pass=$((pass+1))
      else
        echo "  FAIL mutation confirmed: disabling the process-cwd check lets the sweep remove a worktree a real process is sitting in -- expected removal on the mutant, still present"; fail=$((fail+1))
      fi
    else
      echo "  FAIL mutation confirmed: disabling the process-cwd check lets the sweep remove a worktree a real process is sitting in -- the background sleep died before the mutant run"; fail=$((fail+1))
    fi
  fi
  kill "$INUSE_MUT_PID" 2>/dev/null; wait "$INUSE_MUT_PID" 2>/dev/null
else
  echo "  SKIP in-use fixtures -- no lsof on this machine (checked PATH and /usr/sbin/lsof)"
fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
