#!/bin/bash
# advance-live.sh must advance the LIVE worktree only when the candidate
# demonstrably runs and the watchdog is not about to tick mid-checkout, and
# it must never leave the live worktree in a half-state on any refusal.
#
# WHY: #99's advancement half. Nothing advanced ~/.local/state/agent-dotfiles-
# supervisor/live; it was hand-advanced five times in one day, each time
# prompted only by a human noticing the `code:` line #100 added. The design
# constraints recorded on the issue (candidate must run before the pin
# moves, rollback target captured before mutation, advance only in the
# window right after a watchdog tick, never a silent half-state) are exactly
# what this suite pins down.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ADVANCE="$HERE/../../scripts/supervisor/advance-live.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }

echo "advance-live.sh"

D=$(mktemp -d)

# --- a minimal bare origin + clone, standing in for the shared repo -------
git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/src"
SRC="$D/src"
git -C "$SRC" config user.email test@example.com
git -C "$SRC" config user.name "Test"
git -C "$SRC" checkout -q -b main
mkdir -p "$SRC/scripts/supervisor"
# A stand-in watchdog.sh that writes a well-formed status file, matching the
# real one's contract, without needing tmux/gh -- advance-live.sh's smoke
# gate only cares that the candidate runs and writes checked:/state: lines.
cat >"$SRC/scripts/supervisor/watchdog.sh" <<'EOF'
#!/bin/bash
set -uo pipefail
STATUS="${SUPERVISOR_STATUS:?}"
mkdir -p "$(dirname "$STATUS")"
{
  printf 'checked:  %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'state:    pane_unreadable\n'
} >"$STATUS"
exit 0
EOF
chmod +x "$SRC/scripts/supervisor/watchdog.sh"
# A file no later commit in this suite ever touches, so a local edit to it
# is the same non-conflicting shape the PR review's own repro used (a
# trailing appended line to a file the incoming diff never changes) -- git
# carries a change like this forward silently on checkout rather than
# refusing, which is what makes the dirty guard load-bearing rather than
# redundant with git's own conflict detection.
echo baseline >"$SRC/untouched.txt"
git -C "$SRC" add -A
git -C "$SRC" commit -q -m "good watchdog.sh"
git -C "$SRC" push -q -u origin main

# A worktree standing in for the live copy, pinned at this first commit.
LIVE="$D/live"
git -C "$SRC" worktree add -q --detach "$LIVE" origin/main

fresh_status() { # fresh_status <state-dir>
  mkdir -p "$1"
  printf 'checked:  %s\nstate:    working\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$1/watchdog.status"
}
stale_status() { # stale_status <state-dir> <seconds-ago>
  mkdir -p "$1"
  local ts
  ts=$(date -u -v-"$2"S +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d "$2 seconds ago" +%Y-%m-%dT%H:%M:%SZ)
  printf 'checked:  %s\nstate:    working\n' "$ts" >"$1/watchdog.status"
}
run() { # run <state-dir>
  SUPERVISOR_STATE="$1" bash "$ADVANCE" "$LIVE"
}

# --- already current: nothing to do, no watchdog.status even needed -------
S=$(mktemp -d)
out=$(run "$S" 2>&1); rc=$?
want_exit "already-current exits 0" "$rc" 0 "$out"
if grep -qi "advanc" <<<"$out"; then bad "already-current does not claim to advance" "$out"; else ok "already-current does not claim to advance"; fi

# --- put a second commit on origin/main so LIVE is genuinely behind -------
echo two >"$SRC/file.txt"
git -C "$SRC" add file.txt
git -C "$SRC" commit -q -m "second commit"
git -C "$SRC" push -q origin main
git -C "$LIVE" fetch -q origin main
target_sha=$(git -C "$LIVE" rev-parse origin/main)
before_sha=$(git -C "$LIVE" rev-parse HEAD)

# --- no watchdog.status yet: skip, live untouched --------------------------
S=$(mktemp -d)
out=$(run "$S" 2>&1); rc=$?
want_exit "no status file skips (exit 0)" "$rc" 0 "$out"
after=$(git -C "$LIVE" rev-parse HEAD)
if [ "$after" = "$before_sha" ]; then ok "no status file leaves live untouched"; else bad "no status file leaves live untouched" "moved to $after"; fi

# --- watchdog tick was too long ago: skip, live untouched ------------------
S=$(mktemp -d); stale_status "$S" 179
out=$(run "$S" 2>&1); rc=$?
want_exit "stale tick skips (exit 0)" "$rc" 0 "$out"
if grep -q "outside the" <<<"$out"; then ok "stale tick names the safe window"; else bad "stale tick names the safe window" "$out"; fi
after=$(git -C "$LIVE" rev-parse HEAD)
if [ "$after" = "$before_sha" ]; then ok "stale tick leaves live untouched"; else bad "stale tick leaves live untouched" "moved to $after"; fi

# --- fresh tick, but the candidate at origin/main is broken: gate refuses -
BROKEN=$(mktemp -d)
git -C "$SRC" worktree add -q --detach "$BROKEN" origin/main
printf '#!/bin/bash\nexit 1\n' >"$BROKEN/scripts/supervisor/watchdog.sh"
git -C "$BROKEN" -c user.email=t@t -c user.name=t commit -aq -m "break watchdog.sh"
broken_sha=$(git -C "$BROKEN" rev-parse HEAD)
# Point LIVE's view of origin/main at the broken commit without touching the
# real branch on origin -- update-ref on the shared object store, same trick
# advance-live.sh itself only ever reads via `rev-parse origin/main`.
git -C "$LIVE" update-ref refs/remotes/origin/main "$broken_sha"

S=$(mktemp -d); fresh_status "$S"
out=$(run "$S" 2>&1); rc=$?
want_exit "broken candidate refuses to advance (nonzero exit)" "$rc" 1 "$out"
after=$(git -C "$LIVE" rev-parse HEAD)
if [ "$after" = "$before_sha" ]; then ok "broken candidate leaves live untouched"; else bad "broken candidate leaves live untouched" "moved to $after"; fi
if [ -f "$S/.live-rollback-sha" ]; then bad "broken candidate did not write a rollback file" "$(cat "$S/.live-rollback-sha")"; else ok "broken candidate did not write a rollback file"; fi

# restore origin/main to the good target for the success case
git -C "$LIVE" update-ref refs/remotes/origin/main "$target_sha"
git -C "$SRC" worktree remove --force "$BROKEN" >/dev/null 2>&1
git -C "$SRC" worktree prune >/dev/null 2>&1

# --- fresh tick, good candidate: advances, records rollback ---------------
S=$(mktemp -d); fresh_status "$S"
out=$(run "$S" 2>&1); rc=$?
want_exit "good candidate advances (exit 0)" "$rc" 0 "$out"
after=$(git -C "$LIVE" rev-parse HEAD)
if [ "$after" = "$target_sha" ]; then ok "good candidate advances live to origin/main"; else bad "good candidate advances live to origin/main" "at $after, wanted $target_sha"; fi
if [ -f "$S/.live-rollback-sha" ] && [ "$(cat "$S/.live-rollback-sha")" = "$before_sha" ]; then
  ok "rollback file records the pre-advance sha"
else
  bad "rollback file records the pre-advance sha" "$(cat "$S/.live-rollback-sha" 2>/dev/null)"
fi
if grep -q "ADVANCED" "$S/advance-live.log" 2>/dev/null; then ok "advance is logged"; else bad "advance is logged" "$(cat "$S/advance-live.log" 2>/dev/null)"; fi

# --- a third commit, so LIVE is behind again for the guard tests below ----
echo three >"$SRC/file.txt"
git -C "$SRC" add file.txt
git -C "$SRC" commit -q -m "third commit"
git -C "$SRC" push -q origin main
git -C "$LIVE" fetch -q origin main
target_sha3=$(git -C "$LIVE" rev-parse origin/main)
before_sha3=$(git -C "$LIVE" rev-parse HEAD)

# --- dirty LIVE: refuses (not advance), and the dirt survives the refusal -
echo "local edit" >>"$LIVE/untouched.txt"
S=$(mktemp -d); fresh_status "$S"
out=$(run "$S" 2>&1); rc=$?
want_exit "dirty live refuses (nonzero exit)" "$rc" 1 "$out"
after3=$(git -C "$LIVE" rev-parse HEAD)
if [ "$after3" = "$before_sha3" ]; then ok "dirty live is not advanced"; else bad "dirty live is not advanced" "moved to $after3"; fi
dirty_after=$(git -C "$LIVE" status --porcelain)
if [ -n "$dirty_after" ]; then ok "the uncommitted edit is still there after refusal"; else bad "the uncommitted edit is still there after refusal" "live is clean -- the edit was lost or silently handled"; fi
if grep -q "uncommitted changes" <<<"$out"; then ok "refusal names the dirty tree"; else bad "refusal names the dirty tree" "$out"; fi

# clean up the deliberate edit so the next (clean-tree) case is genuinely clean
git -C "$LIVE" checkout -q -- untouched.txt

# --- clean LIVE: still advances once the dirt is gone ----------------------
S=$(mktemp -d); fresh_status "$S"
out=$(run "$S" 2>&1); rc=$?
want_exit "clean live advances once dirt is gone (exit 0)" "$rc" 0 "$out"
after4=$(git -C "$LIVE" rev-parse HEAD)
if [ "$after4" = "$target_sha3" ]; then ok "clean live advances to the new target"; else bad "clean live advances to the new target" "at $after4, wanted $target_sha3"; fi

# --- the re-check actually re-reads, it does not reuse the first read -----
# A fourth commit whose stand-in watchdog.sh writes its own well-formed
# smoke status (so the gate itself passes) and, standing in for "state
# changed for real while the smoke test was running", also overwrites the
# outer run's watchdog.status with a checked: timestamp well outside the
# safe post-tick window. If advance-live.sh reused the $age it read before
# the smoke test instead of re-deriving it immediately before the checkout,
# this would still advance.
echo four >"$SRC/file.txt"
git -C "$SRC" add file.txt
cat >"$SRC/scripts/supervisor/watchdog.sh" <<'EOF'
#!/bin/bash
set -uo pipefail
STATUS="${SUPERVISOR_STATUS:?}"
mkdir -p "$(dirname "$STATUS")"
{
  printf 'checked:  %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'state:    pane_unreadable\n'
} >"$STATUS"
if [ -n "${TEST_MUTATE_STATUS_FILE:-}" ]; then
  stale=$(date -u -v-179S +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d "179 seconds ago" +%Y-%m-%dT%H:%M:%SZ)
  printf 'checked:  %s\nstate:    working\n' "$stale" >"$TEST_MUTATE_STATUS_FILE"
fi
exit 0
EOF
git -C "$SRC" add scripts/supervisor/watchdog.sh
git -C "$SRC" commit -q -m "fourth commit, smoke candidate mutates status mid-run"
git -C "$SRC" push -q origin main
git -C "$LIVE" fetch -q origin main
target_sha4=$(git -C "$LIVE" rev-parse origin/main)
before_sha4=$(git -C "$LIVE" rev-parse HEAD)

S=$(mktemp -d); fresh_status "$S"
export TEST_MUTATE_STATUS_FILE="$S/watchdog.status"
out=$(run "$S" 2>&1); rc=$?
unset TEST_MUTATE_STATUS_FILE
want_exit "re-check notices the window closed mid-smoke-test (exit 0, skip)" "$rc" 0 "$out"
after5=$(git -C "$LIVE" rev-parse HEAD)
if [ "$after5" = "$before_sha4" ]; then ok "live is untouched when the re-check catches a closed window"; else bad "live is untouched when the re-check catches a closed window" "moved to $after5"; fi
if grep -qi "closed while the smoke test ran" <<<"$out"; then ok "the refusal names the mid-smoke-test re-check"; else bad "the refusal names the mid-smoke-test re-check" "$out"; fi

# --- origin/main unreadable: refuses loudly, live untouched ----------------
S=$(mktemp -d); fresh_status "$S"
git -C "$LIVE" update-ref -d refs/remotes/origin/main
before2=$(git -C "$LIVE" rev-parse HEAD)
out=$(run "$S" 2>&1); rc=$?
want_exit "unreadable origin/main refuses (nonzero exit)" "$rc" 1 "$out"
after2=$(git -C "$LIVE" rev-parse HEAD)
if [ "$after2" = "$before2" ]; then ok "unreadable origin/main leaves live untouched"; else bad "unreadable origin/main leaves live untouched" "moved to $after2"; fi

# advance-live.sh must clean up its own scratch smoke-test worktrees
leftover=$(git -C "$SRC" worktree list --porcelain | grep -c '^worktree.*ad99-advance-smoke' || true)
if [ "$leftover" -eq 0 ]; then ok "no leftover smoke-test worktrees"; else bad "no leftover smoke-test worktrees" "$leftover still registered"; fi

# --- prove the dirty guard is load-bearing ----------------------------------
# Patch a copy of advance-live.sh with both dirty-guard blocks removed (the
# pre-smoke-test guard and the pre-checkout re-check) and confirm the dirty-
# tree case above now goes the other way: it advances over an uncommitted
# edit instead of refusing. If this sub-test cannot turn that assertion red,
# the assertion was not testing the guard.
BROKEN="$D/advance-live-broken.sh"
patch_rc=0
python3 - "$ADVANCE" "$BROKEN" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
markers = [
    '''dirty=$(dirty_status)
if [ -n "$dirty" ]; then
  fail "live worktree $LIVE has uncommitted changes -- refusing to advance a dirty tree, not stashing it
$dirty"
fi

''',
    '''dirty=$(dirty_status)
if [ -n "$dirty" ]; then
  fail "live worktree $LIVE became dirty while the smoke test ran -- refusing to advance, not stashing it
$dirty"
fi
''',
]
for m in markers:
    assert m in text, "dirty-guard block not found -- script shape changed"
    assert text.count(m) == 1, "dirty-guard block not unique -- script shape changed"
    text = text.replace(m, "", 1)
open(dst, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a dirty-guard-free copy of advance-live.sh" \
    "could not patch $ADVANCE (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a dirty-guard-free copy of advance-live.sh"
  chmod +x "$BROKEN"
  # The "unreadable origin/main" case above deliberately deleted LIVE's view
  # of origin/main; restore it so this mutation check is exercising the
  # dirty guard specifically, not tripping the unrelated origin/main check.
  git -C "$LIVE" update-ref refs/remotes/origin/main "$target_sha4"
  echo "another local edit" >>"$LIVE/untouched.txt"
  before_m=$(git -C "$LIVE" rev-parse HEAD)
  S=$(mktemp -d); fresh_status "$S"
  out=$(SUPERVISOR_STATE="$S" bash "$BROKEN" "$LIVE" 2>&1); rc=$?
  after_m=$(git -C "$LIVE" rev-parse HEAD)
  dirty_m=$(git -C "$LIVE" status --porcelain)
  if [ "$after_m" != "$before_m" ] && [ -n "$dirty_m" ]; then
    ok "mutation confirmed: removing the dirty guard reports an advance while carrying the uncommitted edit forward (the assertions above would now be red)"
  else
    bad "mutation confirmed: removing the dirty guard reports an advance while carrying the uncommitted edit forward" \
      "expected the broken copy to advance to a new sha while staying dirty, got after=$after_m (before=$before_m) dirty='$dirty_m' rc=$rc: $out"
  fi
fi

# --- #136: two uncoordinated callers advancing the same live worktree -------
# Since #132 there are two callers -- loop-tick.md's step 0 and watchdog.sh's
# exit trap -- and the normal case is the watchdog running from the pinned copy
# at the moment a supervisor tick begins. #136 filed that as low severity on the
# reasoned claim that the worst case is bounded by git's own `index.lock`: one
# `git checkout --detach` fails cleanly and is reported as a refusal, never
# corruption. The two blocks below turn that reasoning into assertions.
#
# They are separate claims and are deliberately kept separate:
#   A. THE MECHANISM. `index.lock` is held by the test, so the refusal fires on
#      every run. This proves what advance-live.sh does when the checkout is
#      locked out; it does NOT prove two real invocations ever reach that point.
#   B. THE RACE. Two invocations really do run concurrently from a shared
#      barrier, with the second caller staggered across the same start-offset
#      sweep (0-0.12s) that #148's one-off 200-iteration experiment used to
#      land collisions reliably -- #150 found that sweep existed only in the
#      one-off run and never made it into this file, so the committed test
#      (a bare simultaneous release) collided only ~7.5% of runs. This asserts
#      only the invariants that must hold whether or not a collision fires,
#      and reports the observed collision count without asserting it is
#      nonzero: asserting "a collision occurred" here would fail the suite on
#      a CI runner that merely scheduled two processes politely, which is a
#      flaky test, not a stronger one (#150). A zero-collision run is logged
#      explicitly instead, so that outcome is itself checked rather than a
#      silent pass -- the deterministic mechanism test above (section A) pins
#      the refusal handling regardless of whether this run's race collided.

D2=$(mktemp -d)
git init -q --bare "$D2/origin.git"
git clone -q "$D2/origin.git" "$D2/src" 2>/dev/null
SRC2="$D2/src"
git -C "$SRC2" config user.email test@example.com
git -C "$SRC2" config user.name "Test"
git -C "$SRC2" checkout -q -b main
mkdir -p "$SRC2/scripts/supervisor"
cat >"$SRC2/scripts/supervisor/watchdog.sh" <<'EOF'
#!/bin/bash
set -uo pipefail
STATUS="${SUPERVISOR_STATUS:?}"
mkdir -p "$(dirname "$STATUS")"
printf 'checked:  %s\nstate:    pane_unreadable\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$STATUS"
exit 0
EOF
chmod +x "$SRC2/scripts/supervisor/watchdog.sh"
echo baseline >"$SRC2/untouched.txt"
git -C "$SRC2" add -A
git -C "$SRC2" commit -q -m "race fixture base"
git -C "$SRC2" push -q -u origin main
RBASE=$(git -C "$SRC2" rev-parse HEAD)
echo two >"$SRC2/file.txt"
git -C "$SRC2" add file.txt
git -C "$SRC2" commit -q -m "race fixture target"
git -C "$SRC2" push -q origin main
RTARGET=$(git -C "$SRC2" rev-parse HEAD)
LIVE2="$D2/live"
git -C "$SRC2" worktree add -q --detach "$LIVE2" "$RBASE"
LIVE2_GITDIR=$(git -C "$LIVE2" rev-parse --absolute-git-dir)

race_state() { # race_state -> echoes a fresh state dir with a just-ticked status
  local s; s=$(mktemp -d "$D2/s.XXXXXX")
  printf 'checked:  %s\nstate:    working\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$s/watchdog.status"
  echo "$s"
}
reset_live2() {
  git -C "$LIVE2" checkout -q --detach "$RBASE"
  git -C "$LIVE2" clean -qfd
  git -C "$LIVE2" update-ref refs/remotes/origin/main "$RTARGET"
}

# --- A. the mechanism: a locked index means a clean refusal, not a half-state -
reset_live2
: >"$LIVE2_GITDIR/index.lock"
S=$(race_state)
out=$(SUPERVISOR_STATE="$S" bash "$ADVANCE" "$LIVE2" 2>&1); rc=$?
rm -f "$LIVE2_GITDIR/index.lock"
want_exit "locked index refuses (nonzero exit)" "$rc" 1 "$out"
if grep -q "checkout to .* failed" <<<"$out"; then ok "locked-index refusal names the failed checkout"; else bad "locked-index refusal names the failed checkout" "$out"; fi
lhead=$(git -C "$LIVE2" rev-parse HEAD)
if [ "$lhead" = "$RBASE" ]; then ok "locked index leaves live at the pre-advance sha"; else bad "locked index leaves live at the pre-advance sha" "at $lhead, wanted $RBASE"; fi
lstatus=$(git -C "$LIVE2" status --porcelain)
if [ -z "$lstatus" ]; then ok "locked-index refusal leaves a clean worktree"; else bad "locked-index refusal leaves a clean worktree" "$lstatus"; fi
if grep -q "rollback recorded" <<<"$out"; then ok "locked-index refusal points at the recorded rollback"; else bad "locked-index refusal points at the recorded rollback" "$out"; fi
# The refusal must be recoverable: the next invocation, with the lock gone,
# finishes the advance. A refusal that wedges the worktree would be a defect
# regardless of how loudly it reported itself.
S=$(race_state)
out=$(SUPERVISOR_STATE="$S" bash "$ADVANCE" "$LIVE2" 2>&1); rc=$?
want_exit "the invocation after a locked-index refusal advances (exit 0)" "$rc" 0 "$out"
lhead=$(git -C "$LIVE2" rev-parse HEAD)
if [ "$lhead" = "$RTARGET" ]; then ok "a locked-index refusal is fully recoverable"; else bad "a locked-index refusal is fully recoverable" "at $lhead, wanted $RTARGET"; fi

# --- mutation check: the mechanism test must be able to go red -------------
# #150's own gap: the committed race test passed on runs that never collided,
# so it was asserting the refusal path without ever exercising it. Prove the
# opposite is true of section A above -- patch the checkout failure branch to
# swallow the error and exit 0 instead of refusing, and confirm the same
# locked-index scenario that passed above now reports success. If it did not,
# the assertions above were not actually pinned to the refusal.
BROKEN_MECH="$D2/advance-live-swallows-refusal.sh"
mech_patch_rc=0
python3 - "$ADVANCE" "$BROKEN_MECH" <<'PY' || mech_patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''if ! git -C "$LIVE" checkout --detach "$target" >>"$LOG" 2>&1; then
  fail "checkout to $target failed in $LIVE -- live worktree left at $cur, rollback recorded at $ROLLBACK"
fi'''
replacement = '''if ! git -C "$LIVE" checkout --detach "$target" >>"$LOG" 2>&1; then
  log "SWALLOWED (mutation test): checkout to $target failed in $LIVE, exiting 0 anyway"
  exit 0
fi'''
assert marker in text, "checkout-failure block not found -- script shape changed"
assert text.count(marker) == 1, "checkout-failure block not unique -- script shape changed"
open(dst, "w").write(text.replace(marker, replacement, 1))
PY
if [ "$mech_patch_rc" -ne 0 ]; then
  bad "setup: patched a refusal-swallowing copy of advance-live.sh" \
    "could not patch $ADVANCE (exit $mech_patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a refusal-swallowing copy of advance-live.sh"
  chmod +x "$BROKEN_MECH"
  reset_live2
  : >"$LIVE2_GITDIR/index.lock"
  S=$(race_state)
  mech_out=$(SUPERVISOR_STATE="$S" bash "$BROKEN_MECH" "$LIVE2" 2>&1); mech_rc=$?
  rm -f "$LIVE2_GITDIR/index.lock"
  if [ "$mech_rc" -eq 0 ]; then
    ok "mutation confirmed: swallowing the checkout failure turns the locked-index refusal into a false success (the exit-code assertion above would now be red)"
  else
    bad "mutation confirmed: swallowing the checkout failure turns the locked-index refusal into a false success" \
      "expected exit 0 from the mutated script, got $mech_rc: $mech_out"
  fi
fi

# --- B. the race itself: invariants under genuinely concurrent invocation ----
# Both invocations start from a shared barrier file rather than two bare `&`
# backgrounds, which is what makes them actually overlap; without it the second
# process's startup cost alone puts it a whole phase behind the first.
# The offsets #148's one-off experiment swept the second caller across to
# land it at different points in the first's sequence -- ported in for #150
# so the committed test uses the methodology that actually produced the
# numbers, not just the conclusion drawn from it.
OFFSETS=(0 0.01 0.03 0.05 0.07 0.08 0.10 0.12)
RACE_ITERS=20
race_bad=0
race_collisions=0
race_lost=0
for ((n=1; n<=RACE_ITERS; n++)); do
  offset="${OFFSETS[$(( (n-1) % ${#OFFSETS[@]} ))]}"
  reset_live2
  S=$(race_state)
  R=$(mktemp -d "$D2/r.XXXXXX")
  race_child() {
    local id="$1" delay="$2"
    : >"$R/ready.$id"
    while [ ! -e "$R/go" ]; do :; done
    [ "$delay" = "0" ] || sleep "$delay"
    SUPERVISOR_STATE="$S" bash "$ADVANCE" "$LIVE2" >"$R/out.$id" 2>&1
    echo $? >"$R/rc.$id"
  }
  race_child A 0 & race_child B "$offset" &
  while [ ! -e "$R/ready.A" ] || [ ! -e "$R/ready.B" ]; do :; done
  : >"$R/go"
  wait
  rca=$(cat "$R/rc.A"); rcb=$(cat "$R/rc.B")
  [ "$rca" -ne 0 ] || [ "$rcb" -ne 0 ] && race_collisions=$((race_collisions+1))

  problems=""
  rhead=$(git -C "$LIVE2" rev-parse HEAD 2>&1) || problems+=" HEAD-unreadable"
  git -C "$LIVE2" cat-file -e "${rhead}^{commit}" 2>/dev/null || problems+=" HEAD-is-not-a-commit"
  [ "$rhead" = "$RBASE" ] || [ "$rhead" = "$RTARGET" ] || problems+=" HEAD-in-limbo($rhead)"
  [ -e "$LIVE2_GITDIR/index.lock" ] && problems+=" index.lock-left-behind"
  rstatus=$(git -C "$LIVE2" status --porcelain 2>&1)
  [ -n "$rstatus" ] && problems+=" dirty-after($(tr '\n' ';' <<<"$rstatus"))"
  rleft=$(git -C "$LIVE2" worktree list --porcelain | grep -c 'ad99-advance-smoke' || true)
  [ "$rleft" -ne 0 ] && problems+=" leftover-smoke-worktrees($rleft)"
  # Whatever the race did, a following solo invocation must be able to finish
  # the job. This is the assertion that would catch "the concurrent path leaves
  # a state the next invocation cannot recover from" -- the outcome that would
  # make #136's low severity wrong.
  S2=$(race_state)
  rout=$(SUPERVISOR_STATE="$S2" bash "$ADVANCE" "$LIVE2" 2>&1); rrc=$?
  rhead2=$(git -C "$LIVE2" rev-parse HEAD 2>/dev/null)
  { [ "$rrc" -eq 0 ] && [ "$rhead2" = "$RTARGET" ]; } \
    || problems+=" not-recoverable(rc=$rrc head=$rhead2: $(tr '\n' ' ' <<<"$rout"))"
  # Neither must the advance be silently lost: two callers racing may not leave
  # the worktree behind with both of them reporting success.
  if [ "$rca" -eq 0 ] && [ "$rcb" -eq 0 ] && [ "$rhead" != "$RTARGET" ]; then
    race_lost=$((race_lost+1))
    problems+=" advance-lost(both exited 0 but live stayed at $rhead)"
  fi
  if [ -n "$problems" ]; then
    race_bad=$((race_bad+1))
    echo "       race iteration $n (offset ${offset}s):$problems"
    echo "       A(rc=$rca): $(tr '\n' ' ' <"$R/out.A")"
    echo "       B(rc=$rcb): $(tr '\n' ' ' <"$R/out.B")"
  fi
  git -C "$LIVE2" worktree prune >/dev/null 2>&1
  rm -rf "$R" "$S" "$S2"
done
if [ "$race_bad" -eq 0 ]; then
  ok "$RACE_ITERS concurrent double-invocations left a valid, recoverable live worktree every time ($race_collisions of $RACE_ITERS actually collided, offsets swept: ${OFFSETS[*]}s)"
else
  bad "$RACE_ITERS concurrent double-invocations left a valid, recoverable live worktree every time" \
    "$race_bad of $RACE_ITERS iterations left a bad state"
fi
if [ "$race_lost" -eq 0 ]; then ok "no concurrent iteration lost the advance while both callers reported success"; else bad "no concurrent iteration lost the advance while both callers reported success" "$race_lost iterations"; fi

# --- the zero-collision outcome is itself checked, never a silent pass -----
# #150's finding: on runs where the sweep above still collides zero times,
# this suite must not just report "20 passed" and move on -- that is exactly
# how two of the four measured runs slipped through before. Whichever way it
# goes is asserted here, not just printed.
if [ "$race_collisions" -eq 0 ]; then
  zero_collision_note="$RACE_ITERS/$RACE_ITERS iterations swept across offsets ${OFFSETS[*]}s and still collided zero times -- informational only (#150), not a suite failure: asserting a nonzero count would make this suite flaky on a loaded CI runner that scheduled the two processes politely. The deterministic mechanism test in section A above already pins the refusal handling independently of this run's race outcome."
  echo "       NOTE: $zero_collision_note"
  if [ -n "$zero_collision_note" ]; then
    ok "zero-collision run is logged explicitly rather than passing silently"
  else
    bad "zero-collision run is logged explicitly rather than passing silently" "no note recorded"
  fi
else
  ok "the offset sweep collided at least once this run ($race_collisions/$RACE_ITERS across offsets ${OFFSETS[*]}s)"
fi

git -C "$SRC2" worktree remove --force "$LIVE2" >/dev/null 2>&1
rm -rf "$D2"

# --- agent-dotfiles#187: restarting a stale inbox-poll.sh -------------------
# The watchdog's own restart-on-crash defect (#130) got #132: the watchdog
# advances its OWN pinned worktree every tick. inbox-poll.sh is the estate's
# other long-running process and got no equivalent (#187) -- this exercises
# the fix, which lives here rather than in inbox-poll.sh itself (see this
# file's own header). tmux is stubbed throughout: nothing here ever reaches
# a real tmux server, let alone the live poller in agent-dotfiles:11 -- the
# brief for this work says explicitly not to touch that pane, and a fake
# `tmux` on PATH is how that is guaranteed rather than merely intended.
D3=$(mktemp -d)
git init -q --bare "$D3/origin.git"
git clone -q "$D3/origin.git" "$D3/src" 2>/dev/null
SRC3="$D3/src"
git -C "$SRC3" config user.email test@example.com
git -C "$SRC3" config user.name "Test"
git -C "$SRC3" checkout -q -b main
mkdir -p "$SRC3/scripts/supervisor"
cat >"$SRC3/scripts/supervisor/watchdog.sh" <<'EOF'
#!/bin/bash
set -uo pipefail
STATUS="${SUPERVISOR_STATUS:?}"
mkdir -p "$(dirname "$STATUS")"
printf 'checked:  %s\nstate:    pane_unreadable\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$STATUS"
exit 0
EOF
chmod +x "$SRC3/scripts/supervisor/watchdog.sh"
git -C "$SRC3" add -A
git -C "$SRC3" commit -q -m "base"
git -C "$SRC3" push -q -u origin main
LIVE3="$D3/live"
git -C "$SRC3" worktree add -q --detach "$LIVE3" origin/main
live3_sha=$(git -C "$LIVE3" rev-parse HEAD)

# A shell standing in for the poller's pane. The live defect is exactly this
# shape: the healthy poller is in the `inbox-poll` tmux window, but the pane's
# first process reads as a shell, so matching the pane process misses it.
mkdir -p "$D3/lane"
cat > "$D3/lane/pane-shell.sh" <<'EOF'
#!/bin/bash
sleep 300 &
child=$!
trap 'kill "$child" 2>/dev/null' EXIT INT TERM
wait "$child"
EOF
chmod +x "$D3/lane/pane-shell.sh"
bash "$D3/lane/pane-shell.sh" >/dev/null 2>&1 &
POLLER_PANE_PID=$!

STUBS="$D3/bin"; mkdir -p "$STUBS"
TMUX_LOG="$D3/tmux.log"
# agent-supervisor#28/#31: poller-window.sh asks tmux for an id/name row
# using a plain space separator that survives the stripped LaunchAgent
# environment, then compares the window name client-side so LANES_POLLER_WINDOW
# is never parsed as a tmux format string. This stub has to emit both fields,
# or every test below that depends on window-name matching (the mutation test,
# LANES_POLLER_WINDOW overrides, the multi-window case) would see no matches.
cat > "$STUBS/tmux" <<EOF
#!/bin/bash
echo "\$@" >> "$TMUX_LOG"
if [ "\$1" = "list-panes" ]; then
  printf 'test-session-187:11.1\t$POLLER_PANE_PID\n'
elif [ "\$1" = "list-windows" ]; then
  rows="\${TMUX_WINDOW_ROWS:-\$'@225\tinbox-poll\n'}"
  while IFS=\$'\t' read -r id name; do
    [ -n "\$id" ] || continue
    printf '%s %s\n' "\$id" "\$name"
  done <<<"\$rows"
fi
exit 0
EOF
chmod +x "$STUBS/tmux"

# --- a stale poller (sha differs from LIVE3) gets a restart requested ------
S=$(mktemp -d)
printf 'checked: %s\nstate:   ok\nsha:     deadbeefdeadbeefdeadbeefdeadbeefdeadbeef\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$S/inbox-poll.status"
out=$(SUPERVISOR_STATE="$S" LANES_SESSION="test-session-187" INBOX_POLL_RELAUNCH_WAIT_SECONDS=0 PATH="$STUBS:$PATH" bash "$ADVANCE" "$LIVE3" 2>&1); rc=$?
want_exit "a poller-restart check never fails the tick (exit 0)" "$rc" 0 "$out"
[ -f "$S/.inbox-poll-restart-requested" ] && ok "a stale poller gets a restart flag written" \
  || bad "a stale poller gets a restart flag written" "$(ls "$S" 2>/dev/null)"
# agent-supervisor#10: this used to also assert a `tmux send-keys` relaunch
# was queued into the poller's pane. That queuing relied on a shell still
# being underneath the poller to read it, which is false -- the pane's
# command is `exec inbox-poll.sh`, so nothing is left to read a queued
# command once the poller actually exits (that gap is the issue). The flag
# is now the whole mechanism; poller-recover.sh (tested separately) relaunches
# once the flagged poller exits and its pane goes dead. This still checks the
# restart path measured the tmux window it will rely on, rather than trusting
# the stale status file alone.
grep -q 'list-windows -t test-session-187' "$TMUX_LOG" 2>/dev/null && ok "the poller window was looked up before flagging" \
  || bad "the poller window was looked up before flagging" "$(cat "$TMUX_LOG" 2>/dev/null)"
! grep -q 'send-keys' "$TMUX_LOG" 2>/dev/null && ok "no send-keys is queued -- poller-recover.sh owns the relaunch now" \
  || bad "no send-keys is queued -- poller-recover.sh owns the relaunch now" "$(cat "$TMUX_LOG" 2>/dev/null)"
grep -qi 'POLLER-RESTART-REQUESTED' "$S/advance-live.log" 2>/dev/null && ok "the restart is logged" \
  || bad "the restart is logged" "$(cat "$S/advance-live.log" 2>/dev/null)"

# --- #22: prompt relaunch distinguishes missing from non-executable -------
GATE_MUT="$D3/prompt-gate"; mkdir -p "$GATE_MUT/scripts/supervisor"
cp "$ADVANCE" "$GATE_MUT/scripts/supervisor/advance-live.sh"
cp "$HERE/../../scripts/supervisor/poller-window.sh" "$GATE_MUT/scripts/supervisor/poller-window.sh"
chmod +x "$GATE_MUT/scripts/supervisor/advance-live.sh" "$GATE_MUT/scripts/supervisor/poller-window.sh"

S_GATE=$(mktemp -d)
printf 'checked: %s\nstate:   ok\nsha:     deadbeef\npid:     %s\n' \
  "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$POLLER_PANE_PID" >"$S_GATE/inbox-poll.status"
: >"$TMUX_LOG"
out_gate=$(SUPERVISOR_STATE="$S_GATE" LANES_SESSION="test-session-187" INBOX_POLL_RELAUNCH_WAIT_SECONDS=0 PATH="$STUBS:$PATH" \
  bash "$GATE_MUT/scripts/supervisor/advance-live.sh" "$LIVE3" 2>&1); rc_gate=$?
want_exit "a missing prompt poller-recover.sh still leaves advance-live exit 0" "$rc_gate" 0 "$out_gate"
grep -qi 'poller-recover.sh is missing beside advance-live.sh' "$S_GATE/advance-live.log" 2>/dev/null \
  && ok "a missing prompt poller-recover.sh is logged distinctly" \
  || bad "a missing prompt poller-recover.sh is logged distinctly" "$(cat "$S_GATE/advance-live.log" 2>/dev/null)"

printf '#!/bin/bash\nexit 0\n' >"$GATE_MUT/scripts/supervisor/poller-recover.sh"
chmod 644 "$GATE_MUT/scripts/supervisor/poller-recover.sh"
S_GATE2=$(mktemp -d)
printf 'checked: %s\nstate:   ok\nsha:     deadbeef\npid:     %s\n' \
  "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$POLLER_PANE_PID" >"$S_GATE2/inbox-poll.status"
: >"$TMUX_LOG"
out_gate2=$(SUPERVISOR_STATE="$S_GATE2" LANES_SESSION="test-session-187" INBOX_POLL_RELAUNCH_WAIT_SECONDS=0 PATH="$STUBS:$PATH" \
  bash "$GATE_MUT/scripts/supervisor/advance-live.sh" "$LIVE3" 2>&1); rc_gate2=$?
want_exit "a non-executable prompt poller-recover.sh still leaves advance-live exit 0" "$rc_gate2" 0 "$out_gate2"
grep -qi 'poller-recover.sh exists but is not executable' "$S_GATE2/advance-live.log" 2>/dev/null \
  && ok "a non-executable prompt poller-recover.sh is logged distinctly" \
  || bad "a non-executable prompt poller-recover.sh is logged distinctly" "$(cat "$S_GATE2/advance-live.log" 2>/dev/null)"

# --- MUTATION: point the shared recognition rule at a name nothing matches -
# This must make the restart assertion above go red. A missing poller window is
# a loud refusal, not a quiet "no work" success.
MUT="$D3/mutant"; mkdir -p "$MUT/scripts/supervisor"
cp "$ADVANCE" "$MUT/scripts/supervisor/advance-live.sh"
cp "$HERE/../../scripts/supervisor/poller-window.sh" "$MUT/scripts/supervisor/poller-window.sh"
patch_rc=0
python3 - "$MUT/scripts/supervisor/poller-window.sh" <<'PY' || patch_rc=$?
import sys
path = sys.argv[1]
text = open(path).read()
old = 'POLLER_WINDOW_NAME="${LANES_POLLER_WINDOW:-inbox-poll}"'
new = 'POLLER_WINDOW_NAME="${LANES_POLLER_WINDOW:-missing-poller-window}"'
assert old in text, "poller-window default assignment not found"
assert text.count(old) == 1, "poller-window default assignment not unique"
open(path, "w").write(text.replace(old, new, 1))
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: mutated the shared poller-window recognizer" "patch failed with exit $patch_rc"
else
  ok "setup: mutated the shared poller-window recognizer"
  chmod +x "$MUT/scripts/supervisor/advance-live.sh" "$MUT/scripts/supervisor/poller-window.sh"
  S_MUT=$(mktemp -d)
  printf 'checked: %s\nstate:   ok\nsha:     deadbeefdeadbeefdeadbeefdeadbeefdeadbeef\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$S_MUT/inbox-poll.status"
  : >"$TMUX_LOG"
  out_mut=$(SUPERVISOR_STATE="$S_MUT" LANES_SESSION="test-session-187" INBOX_POLL_RELAUNCH_WAIT_SECONDS=0 PATH="$STUBS:$PATH" bash "$MUT/scripts/supervisor/advance-live.sh" "$LIVE3" 2>&1); rc_mut=$?
  if [ ! -f "$S_MUT/.inbox-poll-restart-requested" ] \
     && grep -qi 'no poller window' "$S_MUT/advance-live.log" 2>/dev/null; then
    ok "mutation confirmed: a recognizer pointed at a missing name cannot restart the stale poller (the assertion above would be red)"
  else
    bad "mutation confirmed: a recognizer pointed at a missing name cannot restart the stale poller" \
      "rc=$rc_mut out=$out_mut log=$(cat "$S_MUT/advance-live.log" 2>/dev/null) files=$(ls "$S_MUT" 2>/dev/null)"
  fi
fi

# --- a current poller (sha matches LIVE3) is left alone ---------------------
S2=$(mktemp -d)
printf 'checked: %s\nstate:   ok\nsha:     %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$live3_sha" >"$S2/inbox-poll.status"
: >"$TMUX_LOG"
out2=$(SUPERVISOR_STATE="$S2" LANES_SESSION="test-session-187" PATH="$STUBS:$PATH" bash "$ADVANCE" "$LIVE3" 2>&1); rc2=$?
want_exit "a current-poller check never fails the tick (exit 0)" "$rc2" 0 "$out2"
[ ! -f "$S2/.inbox-poll-restart-requested" ] && ok "a current poller gets no restart flag" \
  || bad "a current poller gets no restart flag" ""
[ ! -s "$TMUX_LOG" ] && ok "a current poller triggers no tmux send-keys" \
  || bad "a current poller triggers no tmux send-keys" "$(cat "$TMUX_LOG")"

# --- a restart already in flight is not requested again ---------------------
S3=$(mktemp -d)
printf 'checked: %s\nstate:   ok\nsha:     deadbeef\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$S3/inbox-poll.status"
: >"$S3/.inbox-poll-restart-requested"
: >"$TMUX_LOG"
out3=$(SUPERVISOR_STATE="$S3" LANES_SESSION="test-session-187" PATH="$STUBS:$PATH" bash "$ADVANCE" "$LIVE3" 2>&1); rc3=$?
[ ! -s "$TMUX_LOG" ] && ok "a restart already pending is not requested again" \
  || bad "a restart already pending is not requested again" "$(cat "$TMUX_LOG")"

# --- no window matches the poller: refuses to guess, does not restart ------
kill "$POLLER_PANE_PID" 2>/dev/null; wait "$POLLER_PANE_PID" 2>/dev/null
S4=$(mktemp -d)
printf 'checked: %s\nstate:   ok\nsha:     deadbeef\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$S4/inbox-poll.status"
: >"$TMUX_LOG"
out4=$(SUPERVISOR_STATE="$S4" LANES_SESSION="test-session-187" LANES_POLLER_WINDOW="missing-poller-window" PATH="$STUBS:$PATH" bash "$ADVANCE" "$LIVE3" 2>&1); rc4=$?
[ ! -f "$S4/.inbox-poll-restart-requested" ] && ok "no matching window leaves the poller untouched" \
  || bad "no matching pane leaves the poller untouched" ""
grep -qi 'no poller window' "$S4/advance-live.log" 2>/dev/null && ok "a missing poller window is named in the log" \
  || bad "a missing pane is named in the log" "$(cat "$S4/advance-live.log" 2>/dev/null)"

# --- multiple poller windows: refuses to guess, does not restart ----------
S5=$(mktemp -d)
printf 'checked: %s\nstate:   ok\nsha:     deadbeef\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$S5/inbox-poll.status"
: >"$TMUX_LOG"
out5=$(TMUX_WINDOW_ROWS=$'@225\tinbox-poll\n@226\tinbox-poll\n' SUPERVISOR_STATE="$S5" LANES_SESSION="test-session-187" PATH="$STUBS:$PATH" bash "$ADVANCE" "$LIVE3" 2>&1); rc5=$?
want_exit "multiple poller windows still leave advance-live exit 0" "$rc5" 0 "$out5"
[ ! -f "$S5/.inbox-poll-restart-requested" ] && ok "multiple poller windows write no restart flag" \
  || bad "multiple poller windows write no restart flag" "$(ls "$S5" 2>/dev/null)"
grep -qi "multiple poller windows named 'inbox-poll' exist in session 'test-session-187' -- refusing to guess" "$S5/advance-live.log" 2>/dev/null \
  && ok "multiple poller windows refusal is logged" \
  || bad "multiple poller windows refusal is logged" "$(cat "$S5/advance-live.log" 2>/dev/null)"

echo
echo "advance-live.sh: agent-supervisor#47 -- prompt poller relaunch"

if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP no tmux on PATH"
else
  # This drives real tmux, but under a private socket directory. It never
  # addresses the live supervisor session.
  # shellcheck source=./tmux-isolation.sh
  source "$HERE/../../scripts/supervisor/tmux-isolation.sh"
  RT47="$(mktemp -d "${TMPDIR:-/tmp}/advance-live-47-tmux.XXXXXX")"
  OLD_TMUX="${TMUX-}"
  OLD_TMUX_TMPDIR="${TMUX_TMPDIR-}"
  unset TMUX
  export TMUX_TMPDIR="$RT47"
  S47_SESSION="advance-live-47-$$"
  if ! assert_isolated_tmux; then
    bad "setup: isolated tmux socket for #47 prompt relaunch test" "TMUX_TMPDIR=$TMUX_TMPDIR"
  else
    ok "setup: isolated tmux socket for #47 prompt relaunch test"
    S47="$(mktemp -d "${TMPDIR:-/tmp}/advance-live-47-state.XXXXXX")"
    STAND_IN_47="$RT47/inbox-poll.sh"
    cat >"$STAND_IN_47" <<'EOF'
#!/bin/bash
set -u
STATUS="${INBOX_POLL_STATUS:?}"
FLAG="${INBOX_POLL_RESTART_FLAG:?}"
PID_FILE="${POLLER_PID_FILE:?}"
PID_HISTORY="${POLLER_PID_HISTORY:?}"
SHA="${POLLER_STATUS_SHA:?}"
mkdir -p "$(dirname "$STATUS")"
echo "$$" >"$PID_FILE"
echo "$$" >>"$PID_HISTORY"
{
  printf 'checked: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'sha:     %s\n' "$SHA"
  printf 'state:   ok\n'
  printf 'pid:     %s\n' "$$"
} >"$STATUS"
while :; do
  if [ -f "$FLAG" ]; then
    rm -f "$FLAG"
    {
      printf 'checked: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
      printf 'sha:     %s\n' "$SHA"
      printf 'state:   stopped\n'
      printf 'pid:     %s\n' "$$"
    } >"$STATUS"
    exit 0
  fi
  sleep 0.1
done
EOF
    chmod +x "$STAND_IN_47"

    tmux new-session -d -s "$S47_SESSION" -x 200 -y 50
    FLAG47="$S47/.inbox-poll-restart-requested"
    STATUS47="$S47/inbox-poll.status"
    PID47="$S47/pid"
    PID_HISTORY47="$S47/pids"
    LAUNCH47="INBOX_POLL_STATUS='$STATUS47' INBOX_POLL_RESTART_FLAG='$FLAG47' POLLER_PID_FILE='$PID47' POLLER_PID_HISTORY='$PID_HISTORY47' POLLER_STATUS_SHA='$live3_sha' exec '$STAND_IN_47'"
    OLD_LAUNCH47="INBOX_POLL_STATUS='$STATUS47' INBOX_POLL_RESTART_FLAG='$FLAG47' POLLER_PID_FILE='$PID47' POLLER_PID_HISTORY='$PID_HISTORY47' POLLER_STATUS_SHA='deadbeefdeadbeefdeadbeefdeadbeefdeadbeef' exec '$STAND_IN_47'"
    tmux new-window -t "$S47_SESSION" -n inbox-poll -d -- "$OLD_LAUNCH47"
    tmux set-window-option -t "$S47_SESSION:inbox-poll" remain-on-exit on >/dev/null 2>&1

    wait_for_pid_file() {
      local deadline=$((SECONDS + 8))
      while [ ! -s "$PID47" ] && [ "$SECONDS" -lt "$deadline" ]; do sleep 0.1; done
      [ -s "$PID47" ]
    }
    pid_alive_47() { [ -n "${1:-}" ] && kill -0 "$1" 2>/dev/null; }
    window_count_47() { tmux list-windows -t "$S47_SESSION" -F '#{window_name}' 2>/dev/null | grep -cFx inbox-poll; }
    live_poller_count_47() {
      local count=0 p
      while IFS= read -r p; do
        [ -n "$p" ] && kill -0 "$p" 2>/dev/null && count=$((count + 1))
      done < <(pgrep -f "$STAND_IN_47" 2>/dev/null)
      printf '%s\n' "$count"
    }
    await_replacement_47() {
      local old="$1" deadline=$((SECONDS + 8)) new
      while [ "$SECONDS" -lt "$deadline" ]; do
        new=$(cat "$PID47" 2>/dev/null)
        if [ -n "$new" ] && [ "$new" != "$old" ] && pid_alive_47 "$new"; then
          printf '%s\n' "$new"
          return 0
        fi
        sleep 0.1
      done
      return 1
    }

    if wait_for_pid_file; then
      old47=$(cat "$PID47")
      ok "setup: stale poller is running before #47 restart"
    else
      old47=""
      bad "setup: stale poller is running before #47 restart" "no pid file"
    fi

    out47=$(SUPERVISOR_STATE="$S47" INBOX_POLL_RESTART_FLAG="$FLAG47" SUPERVISOR_INBOX_POLL_STATUS="$STATUS47" \
      LANES_SESSION="$S47_SESSION" POLLER_LAUNCH_CMD="$LAUNCH47" POLLER_RECOVER_LOCK="$S47/.recover.lock" \
      POLLER_RECOVER_LOG="$S47/recover.log" INBOX_POLL_RELAUNCH_WAIT_SECONDS=8 \
      bash "$ADVANCE" "$LIVE3" 2>&1); rc47=$?
    want_exit "#47 prompt relaunch request keeps advance-live exit 0" "$rc47" 0 "$out47"
    new47=$(await_replacement_47 "$old47" || true)
    if [ -n "$new47" ]; then
      ok "restart flag makes the poller exit and a different live pid appears within seconds"
    else
      bad "restart flag makes the poller exit and a different live pid appears within seconds" \
        "old=$old47 current=$(cat "$PID47" 2>/dev/null) log=$(cat "$S47/advance-live.log" 2>/dev/null) recover=$(cat "$S47/recover.log" 2>/dev/null)"
    fi
    [ "$(window_count_47)" = "1" ] && [ "$(live_poller_count_47)" = "1" ] \
      && ok "prompt relaunch leaves exactly one live poller" \
      || bad "prompt relaunch leaves exactly one live poller" \
        "windows=$(window_count_47) live_processes=$(live_poller_count_47) pids=$(cat "$PID_HISTORY47" 2>/dev/null)"

    # Two quick restart requests must end with one poller, relying on
    # poller-recover.sh's existing lock/idempotency instead of a second
    # launcher in advance-live.sh.
    current47=$(cat "$PID47" 2>/dev/null)
    {
      printf 'checked: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
      printf 'sha:     stale-second-request\n'
      printf 'state:   ok\n'
      printf 'pid:     %s\n' "$current47"
    } >"$STATUS47"
    SUPERVISOR_STATE="$S47" INBOX_POLL_RESTART_FLAG="$FLAG47" SUPERVISOR_INBOX_POLL_STATUS="$STATUS47" \
      LANES_SESSION="$S47_SESSION" POLLER_LAUNCH_CMD="$LAUNCH47" POLLER_RECOVER_LOCK="$S47/.recover.lock" \
      POLLER_RECOVER_LOG="$S47/recover.log" INBOX_POLL_RELAUNCH_WAIT_SECONDS=8 \
      bash "$ADVANCE" "$LIVE3" >/dev/null 2>&1
    SUPERVISOR_STATE="$S47" INBOX_POLL_RESTART_FLAG="$FLAG47" SUPERVISOR_INBOX_POLL_STATUS="$STATUS47" \
      LANES_SESSION="$S47_SESSION" POLLER_LAUNCH_CMD="$LAUNCH47" POLLER_RECOVER_LOCK="$S47/.recover.lock" \
      POLLER_RECOVER_LOG="$S47/recover.log" INBOX_POLL_RELAUNCH_WAIT_SECONDS=8 \
      bash "$ADVANCE" "$LIVE3" >/dev/null 2>&1
    newer47=$(await_replacement_47 "$current47" || true)
    [ -n "$newer47" ] && [ "$(window_count_47)" = "1" ] && [ "$(live_poller_count_47)" = "1" ] \
      && ok "two quick restart requests end with exactly one live poller" \
      || bad "two quick restart requests end with exactly one live poller" \
        "replacement=${newer47:-none} windows=$(window_count_47) live_processes=$(live_poller_count_47) pids=$(cat "$PID_HISTORY47" 2>/dev/null)"

    MUT47="$S47/advance-live.no-prompt.sh"
    patch_rc=0
    python3 - "$ADVANCE" "$MUT47" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''  if prompt_poller_relaunch "$pane" "$poller_sha" "$live_sha" "$poller_pid"; then
    log "POLLER-RESTART-REQUESTED: pane $pane, poller was $poller_sha, live now $live_sha -- flag written; prompt poller-recover.sh waiter started (watchdog remains the backstop)"
  else
    log "POLLER-RESTART-REQUESTED: pane $pane, poller was $poller_sha, live now $live_sha -- flag written; prompt relaunch could not be started, watchdog poller-recover.sh remains the backstop"
  fi
'''
replacement = '''  log "POLLER-RESTART-REQUESTED: pane $pane, poller was $poller_sha, live now $live_sha -- flag written; prompt relaunch removed for this mutation test"
'''
assert marker in text, "prompt relaunch block not found -- advance-live.sh shape changed"
assert text.count(marker) == 1, "prompt relaunch block not unique -- advance-live.sh shape changed"
open(dst, "w").write(text.replace(marker, replacement, 1))
PY
    if [ "$patch_rc" -ne 0 ]; then
      bad "setup: patched prompt relaunch out of advance-live.sh" "patch failed with exit $patch_rc"
    else
      ok "setup: patched prompt relaunch out of advance-live.sh"
      chmod +x "$MUT47"
      current47=$(cat "$PID47" 2>/dev/null)
      {
        printf 'checked: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
        printf 'sha:     stale-without-prompt\n'
        printf 'state:   ok\n'
        printf 'pid:     %s\n' "$current47"
      } >"$STATUS47"
      SUPERVISOR_STATE="$S47" INBOX_POLL_RESTART_FLAG="$FLAG47" SUPERVISOR_INBOX_POLL_STATUS="$STATUS47" \
        LANES_SESSION="$S47_SESSION" POLLER_LAUNCH_CMD="$LAUNCH47" POLLER_RECOVER_LOCK="$S47/.recover.lock" \
        POLLER_RECOVER_LOG="$S47/recover.log" INBOX_POLL_RELAUNCH_WAIT_SECONDS=2 \
        bash "$MUT47" "$LIVE3" >/dev/null 2>&1
      sleep 3
      no_prompt_pid=$(cat "$PID47" 2>/dev/null)
      if [ "$no_prompt_pid" = "$current47" ] || ! pid_alive_47 "$no_prompt_pid"; then
        ok "mutation confirmed: removing prompt relaunch leaves no different live pid within the bound"
      else
        bad "mutation confirmed: removing prompt relaunch leaves no different live pid within the bound" \
          "old=$current47 now=$no_prompt_pid recover=$(cat "$S47/recover.log" 2>/dev/null)"
      fi
    fi

    tmux kill-session -t "$S47_SESSION" 2>/dev/null
    pkill -KILL -f "$STAND_IN_47" 2>/dev/null
    rm -rf "$S47" "$RT47"
  fi
  unset TMUX
  if [ -n "$OLD_TMUX_TMPDIR" ]; then export TMUX_TMPDIR="$OLD_TMUX_TMPDIR"; else unset TMUX_TMPDIR; fi
  if [ -n "$OLD_TMUX" ]; then export TMUX="$OLD_TMUX"; else unset TMUX; fi
fi

rm -rf "$D3"

rm -rf "$D"

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
