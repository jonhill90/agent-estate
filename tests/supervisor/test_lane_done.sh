#!/bin/bash
# lane-done.sh must rename a lane back to free-N ONLY when its worker has
# actually signaled completion -- never on idle pane state alone.
#
# WHY: agent-dotfiles#102. dispatch.sh renames a lane to its task name on
# dispatch; nothing renamed it back on completion, so every finished lane
# permanently left the pool until a human noticed and renamed by hand
# (twice, one evening: ad94/ad93/ad97/ad96, then ad101/ad44). The obvious
# fix -- "rename any idle lane" -- is wrong: `lanes.sh` calls a lane idle
# whether it finished, is between tool calls, or is blocked on an approval
# prompt holding an unposted verdict, and reclaiming on idle alone nearly
# destroyed a live verdict the same night. lane-done.sh instead blocks on
# the worker's own `tmux wait-for -S <channel>` -- the brief's literal last
# action -- and renames only when that returns.
#
# The load-bearing case is the first one below: a channel that has not been
# signaled must produce NO rename-window call, full stop. Everything else is
# secondary.
#
# The stub cases are necessary and NOT sufficient. This suite was once fully
# green while lane-done.sh renamed unfinished lanes in production, because it
# used `wait-for -L` (the lock primitive, which returns immediately on an
# unlocked channel) and the stub implemented `-L` with the *bare* form's
# semantics -- the same misreading, so nothing could disagree (#108). A stub
# is a claim about an external tool's behaviour; a claim nobody checked
# against the tool is not evidence. Hence the last two sections: one that
# runs the real tmux binary, and one that pins the old primitive as a
# permanent regression -- reverting to `-L` must turn this suite red.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LANE_DONE="$HERE/../../scripts/supervisor/lane-done.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

echo "lane-done.sh"

D=$(mktemp -d)
cp "$HERE/stubs/tmux-lane-done" "$D/tmux"
chmod +x "$D/tmux"

cat > "$D/lanes" <<'FIX'
5|ad102-lane-rename-on-completion
6|ad103-something-else
FIX

mkdir -p "$D/wait"
# The stub's block is a bounded poll (a real one is unbounded and would hang
# the suite). One second is plenty to tell "blocked" from "returned at once".
run() { run_script "$LANE_DONE" "$@"; }
run_script() {
  local script="$1"; shift
  : > "$D/tmux.log"
  PATH="$D:$PATH" LANES_FIXTURE="$D/lanes" TMUX_LOG="$D/tmux.log" \
    WAIT_DIR="$D/wait" STUB_WAIT_TIMEOUT=1 \
    AGENT_SUPERVISOR_STATE_DIR="${LEDGER_STATE:-$D/state}" bash "$script" "$@" 2>&1
}
# AGENT_SUPERVISOR_STATE_DIR is not optional in this harness. lane-done.sh now
# records the completion in the ledger (#140); without it, every case in this
# file would write into the REAL supervisor state directory under $HOME.
ledger() { AGENT_SUPERVISOR_STATE_DIR="${LEDGER_STATE:-$D/state}" python3 "$HERE/../../scripts/supervisor/cli.py" "$@"; }
signal() { mkdir -p "$D/wait"; : > "$D/wait/$1.signaled"; }
tmuxlog() { cat "$D/tmux.log"; }

# --- THE safety property: not signaled -> no rename, ever ------------------
rm -rf "$D/wait"; mkdir -p "$D/wait"
out=$(run 5 ad102-lane-rename-on-completion ad102-done t); rc=$?
want_exit "an unsignaled channel exits non-zero" "$rc" 1 "$out"
want_missing "an unsignaled channel is never renamed" "rename-window" "$(tmuxlog)"

# --- signaled, name still matches -> renamed to free-N ----------------------
signal ad102-done
out=$(run 5 ad102-lane-rename-on-completion ad102-done t); rc=$?
want_exit "a signaled channel exits zero" "$rc" 0 "$out"
want_contains "a finished lane is renamed back to free-N" \
  "rename-window -t t:5 free-5" "$(tmuxlog)"

# --- signaled, but the window no longer carries the expected name ----------
# Someone already handled it (or the lane was redispatched into that slot
# while this waiter was still up) -- renaming now would steal the new name.
signal ad102-done
out=$(run 6 ad102-lane-rename-on-completion ad102-done t); rc=$?
want_exit "a name mismatch exits non-zero" "$rc" 1 "$out"
want_missing "a name mismatch is never renamed" "rename-window" "$(tmuxlog)"

# --- ...and a name mismatch must not RELEASE THE LANE either (#205) --------
#
# The two assertions above check the rename and the exit code. Neither looks
# at the ledger, and after #194 reordered the release ahead of the rename the
# ledger write is the operation that actually frees the lane -- so those two
# assertions no longer cover the thing that matters. This name-match check
# (`lane-done.sh:90-93`) is now the ONLY gate between a fired `wait-for`
# channel and an unconditional release: #194 deliberately removed the second
# gate, rename success. #205's reviewer hoisted the release above this guard
# and the suite stayed fully green, which is what this section closes.
#
# The case that gets through every other layer is a window renamed by hand
# while its task is still open: `Ledger.complete` refuses cancelled/failed
# tasks and verifies `pane_nonce` (`core.py`), so a redispatched lane is
# caught there and surfaces as a loud LEDGER RECORD FAILED -- but a live task
# under a hand-renamed window authenticates fine and would be marked
# complete, marking a lane free with its worker still running (#184's shape).
#
# Seeded through the shipped recorder, under the name lane-done.sh is called
# with, on a lane whose window now carries a different name (window 6 is
# `ad103-something-else` in the fixture).
NAMEMATCH_STATE="$D/state-205"
LEDGER_STATE="$NAMEMATCH_STATE" ledger record-dispatch \
  --lane t:6 --task ad205-name-match-guard \
  --summary "#205 name-match guard" \
  --pane-id '%6' --pane-path "$D" --command claude \
  --server-id 'socket:1' --session-id '$0' --issue 205 >/dev/null 2>&1
seed_rc=$?
if [ "$seed_rc" -ne 0 ]; then
  bad "setup: an open task exists for the lane whose window was renamed away" \
    "record-dispatch exited $seed_rc"
else
  ok "setup: an open task exists for the lane whose window was renamed away"
  signal ad102-done
  out=$(LEDGER_STATE="$NAMEMATCH_STATE" run 6 ad205-name-match-guard ad102-done t); rc=$?
  want_exit "a name mismatch with a live task still exits non-zero" "$rc" 1 "$out"
  after=$(LEDGER_STATE="$NAMEMATCH_STATE" ledger status 2>&1)
  want_contains "a name mismatch leaves the task open -- still delivered" \
    '"status":"delivered"' "$after"
  want_missing "a name mismatch records NO completion -- the guard is the only gate on the release (#205)" \
    '"status":"complete"' "$after"
fi

# ...and prove that assertion is load-bearing rather than decorative. Patch a
# copy that hoists the ledger release above the name-match guard -- exactly
# the mutation #205's reviewer ran against the shipped file, which the suite
# did not notice -- and confirm the same call now marks the lane complete
# despite the mismatch, i.e. the assertion above would go red against it.
# Unlike the two mutation copies above, this one must actually reach the
# ledger: lane-done.sh resolves `cli.py` from its OWN directory, so a copy
# dropped in $D would fail with "can't open file .../cli.py" and the mutant
# would look innocent for the wrong reason. Build a shadow of
# `scripts/supervisor/` -- every real file symlinked, `lane-done.sh` the
# mutated copy -- so the mutant runs against the same cli.py the real one does.
SHIPDIR="$D/shipdir"
rm -rf "$SHIPDIR"; mkdir -p "$SHIPDIR"
for f in "$HERE/../../scripts/supervisor/"*; do
  ln -s "$f" "$SHIPDIR/$(basename "$f")"
done
rm -f "$SHIPDIR/lane-done.sh"
HOISTED="$SHIPDIR/lane-done.sh"
patch_rc3=0
python3 - "$LANE_DONE" "$HOISTED" <<'PY' || patch_rc3=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()

guard_start = 'if [ "$CURRENT" != "$EXPECTED_NAME" ]; then'
guard_end_marker = "exit 1\nfi\n\n"
i0 = text.index(guard_start)
i1 = text.index(guard_end_marker, i0) + len(guard_end_marker)
assert text.count(guard_start) == 1, "name-match guard not unique -- script shape changed"
guard_block = text[i0:i1]

ledger_start = 'if ! LEDGER_OUT=$('
ledger_end_marker = "sed 's/^/  /' <<<\"$LEDGER_OUT\" >&2\nfi\n"
j0 = text.index(ledger_start)
j1 = text.index(ledger_end_marker, j0) + len(ledger_end_marker)
assert j0 > i1, "ledger release is not after the name-match guard -- script shape changed, mutation would not test the guard"
ledger_block = text[j0:j1]

open(dst, "w").write(text[:i0] + text[i1:j0] + ledger_block + "\n" + guard_block + text[j1:])
PY
if [ "$patch_rc3" -ne 0 ]; then
  bad "setup: patched a copy of lane-done.sh with the ledger release hoisted above the name-match guard" \
    "could not patch $LANE_DONE (exit $patch_rc3) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of lane-done.sh with the ledger release hoisted above the name-match guard"
  chmod +x "$HOISTED"
  HOISTED_STATE="$D/state-205-mutant"
  LEDGER_STATE="$HOISTED_STATE" ledger record-dispatch \
    --lane t:6 --task ad205-name-match-guard \
    --summary "#205 mutation: release hoisted above the guard" \
    --pane-id '%6' --pane-path "$D" --command claude \
    --server-id 'socket:1' --session-id '$0' --issue 205 >/dev/null 2>&1
  signal ad102-done
  out=$(LEDGER_STATE="$HOISTED_STATE" run_script "$HOISTED" 6 ad205-name-match-guard ad102-done t)
  mutated_status=$(LEDGER_STATE="$HOISTED_STATE" ledger status 2>&1)
  if grep -qF '"status":"complete"' <<<"$mutated_status"; then
    ok "mutation confirmed: hoisting the release above the guard frees a lane whose window was renamed away (the assertion above would now be red)"
  else
    bad "mutation confirmed: hoisting the release above the guard frees a lane whose window was renamed away" \
      "expected the hoisted copy to mark the task complete on a name mismatch; got: $mutated_status / $out"
  fi
fi

# --- the completion is RECORDED, not just renamed away (#140) --------------
#
# The rename returns the lane to the pool; nothing recorded that the work
# finished, which is the completion-signal gap #140 names. lane-done.sh now
# writes it. The task id is the window name, which is what dispatch.sh
# recorded it under -- and is already the identifier `lanes.sh` and
# `claim.sh stale` key on, so the two halves agree without a new identifier.
#
# Seeded through the shipped recorder rather than by hand: a fixture written
# straight into SQLite would prove the assertion below and nothing about
# whether the two commands actually meet.
LEDGER_STATE="$D/state-140" ledger record-dispatch \
  --lane t:5 --task ad102-lane-rename-on-completion \
  --summary "#102 lane-rename-on-completion" \
  --pane-id '%5' --pane-path "$D" --command claude \
  --server-id 'socket:1' --session-id '$0' --issue 102 >/dev/null 2>&1
seed_rc=$?
if [ "$seed_rc" -ne 0 ]; then
  bad "setup: a dispatch record exists for the lane about to finish" "record-dispatch exited $seed_rc"
else
  ok "setup: a dispatch record exists for the lane about to finish"
  before=$(LEDGER_STATE="$D/state-140" ledger status 2>&1)
  want_contains "the seeded task starts out delivered, not complete" '"status":"delivered"' "$before"

  signal ad102-done
  out=$(LEDGER_STATE="$D/state-140" run 5 ad102-lane-rename-on-completion ad102-done t); rc=$?
  want_exit "a signaled lane whose completion is recorded still exits zero" "$rc" 0 "$out"
  want_contains "the lane is still renamed back to free-N" "rename-window -t t:5 free-5" "$(tmuxlog)"
  after=$(LEDGER_STATE="$D/state-140" ledger status 2>&1)
  want_contains "lane-done.sh marks the task complete" '"status":"complete"' "$after"
  want_contains "the completion has a result artifact, hashed and immutable" '"result_sha256":"' "$after"
  # The signal it was evidenced by, in the result the ledger stores -- not in
  # the row, which only carries the path and the hash.
  want_contains "and that artifact records which channel fired" "ad102-done" \
    "$(cat "$D/state-140/results/ad102-lane-rename-on-completion.md" 2>&1)"
fi

# ...and a ledger that errors must not turn a lane that genuinely finished
# into a reported failure -- same best-effort-and-loud contract dispatch.sh
# carries, for the same reason: nothing reads the ledger yet.
signal ad102-done
out=$(LEDGER_STATE="$D/lanes/state" run 5 ad102-lane-rename-on-completion ad102-done t); rc=$?
want_exit "a broken ledger does not fail a completion" "$rc" 0 "$out"
want_contains "the lane is still returned to the pool" "rename-window -t t:5 free-5" "$(tmuxlog)"
want_contains "the ledger failure is loud, not swallowed" "LEDGER RECORD FAILED" "$out"

# --- prove the safety assertion is load-bearing -----------------------------
# Patch a copy of lane-done.sh to drop the wait-for guard entirely -- the
# exact regression #102's "rename any idle lane" temptation would produce --
# and confirm the FIRST assertion above (no rename on an unsignaled channel)
# now fails against it. If this sub-test cannot turn the assertion red, the
# assertion was not testing the guard.
BROKEN="$D/lane-done-broken.sh"
patch_rc=0
python3 - "$LANE_DONE" "$BROKEN" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = 'if ! tmux wait-for "$CHANNEL" 2>/dev/null; then\n  echo "lane-done: channel \'$CHANNEL\' was not signaled -- not renaming ${TARGET}" >&2\n  exit 1\nfi\n\n'
assert marker in text, "wait-for guard block not found -- script shape changed"
assert text.count(marker) == 1, "wait-for guard block not unique -- script shape changed"
open(dst, "w").write(text.replace(marker, "", 1))
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a guard-free copy of lane-done.sh" \
    "could not patch $LANE_DONE (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a guard-free copy of lane-done.sh"
  chmod +x "$BROKEN"
  : > "$D/tmux.log"; rm -rf "$D/wait"; mkdir -p "$D/wait"
  out=$(run_script "$BROKEN" 5 ad102-lane-rename-on-completion ad102-done t)
  log="$(tmuxlog)"
  if grep -qF "rename-window" <<<"$log"; then
    ok "mutation confirmed: removing the guard renames an unfinished lane (the assertion above would now be red)"
  else
    bad "mutation confirmed: removing the guard renames an unfinished lane" \
      "expected the broken copy to rename with no signal present, it did not -- the guard-removal patch missed the real guard: $log"
  fi
fi

# --- pin the #108 regression: `-L` is not the counterpart of `-S` -----------
# The shipped-then-reverted defect. `wait-for -L` locks a channel and returns
# immediately when nobody holds the lock, so lane-done.sh renamed every lane
# on its first call regardless of whether the worker had finished. Assert
# that swapping the primitive back turns the load-bearing case red -- if this
# passes with `-L` restored, the stub has drifted back to modelling a
# fictional tmux and the suite is worthless again.
LOCKVER="$D/lane-done-lock.sh"
patch_rc=0
python3 - "$LANE_DONE" "$LOCKVER" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = 'if ! tmux wait-for "$CHANNEL" 2>/dev/null; then'
assert marker in text, "wait-for call not found -- script shape changed"
assert text.count(marker) == 1, "wait-for call not unique -- script shape changed"
open(dst, "w").write(text.replace(marker, marker.replace('wait-for "$CHANNEL"', 'wait-for -L "$CHANNEL"'), 1))
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a wait-for -L copy of lane-done.sh" \
    "could not patch $LANE_DONE (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a wait-for -L copy of lane-done.sh"
  rm -rf "$D/wait"; mkdir -p "$D/wait"
  out=$(run_script "$LOCKVER" 5 ad102-lane-rename-on-completion ad102-done t)
  if grep -qF "rename-window" <<<"$(tmuxlog)"; then
    ok "regression pinned: wait-for -L renames an unsignaled lane (the stub now models the real lock semantics, so the old code fails here)"
  else
    bad "regression pinned: wait-for -L renames an unsignaled lane" \
      "the -L copy did NOT rename -- the stub is implementing -L as a rendezvous wait again, which is the #108 bug: $out"
  fi
fi

# --- against REAL tmux, not the stub ---------------------------------------
# Everything above this line is a claim about tmux made by a file in this
# repository. This section is the only part that asks tmux itself. It is
# skippable when tmux is absent (CI containers), because a skip that says so
# is honest and a stub-only suite that calls itself complete is not.
echo "  -- real tmux --"
if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP real-tmux checks: tmux not installed (stub-only run -- the wait primitive is UNVERIFIED)"
else
  # Total isolation from any live estate: a private server socket via
  # TMUX_TMPDIR, no user config (-f /dev/null), a name nothing else uses, and
  # kill-server on the way out. This suite must never touch a working lane.
  RT="$D/tmuxsrv"; mkdir -p "$RT"
  RSESS="lanedonetest-$$"
  rtmux() { env -u TMUX TMUX_TMPDIR="$RT" tmux -f /dev/null "$@"; }
  # `timeout` execs a binary and cannot run a shell function, so the bounded
  # form spells the same command out: timeout_rtmux <seconds> <tmux args...>
  timeout_rtmux() { local t="$1"; shift; timeout "$t" env -u TMUX TMUX_TMPDIR="$RT" tmux -f /dev/null "$@"; }
  cleanup_rt() { env -u TMUX TMUX_TMPDIR="$RT" tmux kill-server 2>/dev/null; }
  trap cleanup_rt EXIT

  if ! rtmux new-session -d -s "$RSESS" -n placeholder 2>/dev/null; then
    bad "real tmux: started a throwaway server" "tmux is installed but new-session failed"
  else
    ok "real tmux: started a throwaway server ($(rtmux -V), socket under \$TMUX_TMPDIR, session $RSESS)"

    # 1. The primitive itself: bare wait-for blocks; -L does not.
    timeout_rtmux 2 wait-for "rt-never-signaled-$$" >/dev/null 2>&1; rc=$?
    want_exit "real tmux: bare wait-for BLOCKS on an unsignaled channel (124 = still waiting when killed)" \
      "$rc" 124 ""
    timeout_rtmux 2 wait-for -L "rt-never-signaled-$$" >/dev/null 2>&1; rc=$?
    want_exit "real tmux: wait-for -L does NOT block on that same channel -- it is the lock primitive, not the rendezvous (this is #108)" \
      "$rc" 0 ""

    # 2. The pairing: -S is what releases a bare waiter.
    ( sleep 1; rtmux wait-for -S "rt-paired-$$" ) &
    sigpid=$!
    timeout_rtmux 5 wait-for "rt-paired-$$" >/dev/null 2>&1; rc=$?
    wait "$sigpid" 2>/dev/null
    want_exit "real tmux: bare wait-for returns 0 once -S fires on the same channel" "$rc" 0 ""

    # 3. lane-done.sh end to end against the real thing.
    W=$(rtmux list-windows -t "$RSESS" -F '#{window_index}' | head -1)
    rtmux rename-window -t "${RSESS}:${W}" ad108-realcheck
    real_run() {
      env -u TMUX TMUX_TMPDIR="$RT" AGENT_SUPERVISOR_STATE_DIR="$D/state-realtmux" \
        timeout "$1" bash "$2" \
        "$W" ad108-realcheck "rt-lanedone-$$" "$RSESS" 2>&1
    }

    out=$(real_run 3 "$LANE_DONE"); rc=$?
    want_exit "real tmux: lane-done.sh keeps waiting while the channel is unsignaled" "$rc" 124 "$out"
    name=$(rtmux display-message -p -t "${RSESS}:${W}" '#{window_name}')
    want_contains "real tmux: an unfinished lane is NOT renamed" "ad108-realcheck" "$name"

    # Same call, but the worker now does what a brief's last line says.
    ( sleep 1; env -u TMUX TMUX_TMPDIR="$RT" tmux -f /dev/null wait-for -S "rt-lanedone-$$" ) &
    sigpid=$!
    out=$(real_run 6 "$LANE_DONE"); rc=$?
    wait "$sigpid" 2>/dev/null
    want_exit "real tmux: lane-done.sh returns 0 after the worker signals" "$rc" 0 "$out"
    name=$(rtmux display-message -p -t "${RSESS}:${W}" '#{window_name}')
    want_contains "real tmux: a finished lane is renamed back to free-N" "free-${W}" "$name"

    # 4. Both directions: the old primitive must fail this same check.
    if [ "$patch_rc" -eq 0 ]; then
      rtmux rename-window -t "${RSESS}:${W}" ad108-realcheck
      out=$(real_run 3 "$LOCKVER"); rc=$?
      name=$(rtmux display-message -p -t "${RSESS}:${W}" '#{window_name}')
      if [ "$rc" -eq 0 ] && [ "$name" = "free-${W}" ]; then
        ok "real tmux: the wait-for -L copy renames an unfinished lane immediately -- confirms the fix is what makes the checks above pass, not the harness"
      else
        bad "real tmux: the wait-for -L copy renames an unfinished lane immediately" \
          "expected exit 0 and window free-${W}, got exit $rc and window '$name': $out"
      fi
    fi

    # 5. THE RENAME-WINDOW GUARD, REVISED (agent-dotfiles#194, superseding
    #    #144 finding 4): #144 required that a rename FAILURE must not be
    #    treated as a released lane -- lane-done.sh recorded a completion
    #    only AFTER a successful rename. #174 test 5 requires the opposite
    #    property: the rename must be cosmetic, and the ledger release must
    #    not depend on it succeeding, because #174 made the ledger's answer
    #    win over the window name regardless of what it is renamed to -- so
    #    a rename failure under the OLD ordering left a finished lane
    #    permanently unrenamable AND permanently occupied in the ledger,
    #    with no recovery route left (#194's own deadlock finding). #194
    #    resolves the conflict by keeping #144's spirit -- a completion is
    #    still recorded only for a genuine signal against a name that still
    #    matches, never for idle-alone or a stolen name -- while dropping
    #    the specific mechanism (gating the ledger write behind rename
    #    success) that made the deadlock possible. Shim `tmux` so every call
    #    proxies to the real server except `rename-window`, which is
    #    refused -- modelling a genuine tmux failure (a racing rename, a
    #    dying server) rather than assuming rename-window always succeeds.
    REAL_TMUX="$(command -v tmux)"
    RTSHIM="$D/rtshim"; mkdir -p "$RTSHIM"
    cat > "$RTSHIM/tmux" <<SHIM
#!/bin/bash
if [ "\$1" = "rename-window" ]; then
  echo "shim: rename-window refused" >&2
  exit 1
fi
exec env -u TMUX TMUX_TMPDIR="$RT" "$REAL_TMUX" -f /dev/null "\$@"
SHIM
    chmod +x "$RTSHIM/tmux"

    LEDGER_STATE="$D/state-renamefail" ledger record-dispatch \
      --lane "${RSESS}:${W}" --task ad144-renamefail \
      --summary "#144 rename-window guard" \
      --pane-id '%9' --pane-path "$D" --command claude \
      --server-id 'socket:1' --session-id '$0' --issue 144 >/dev/null 2>&1

    rtmux rename-window -t "${RSESS}:${W}" ad144-renamefail
    ( sleep 1; rtmux wait-for -S "rt-renamefail-$$" ) &
    sigpid=$!
    out=$(PATH="$RTSHIM:$PATH" env -u TMUX AGENT_SUPERVISOR_STATE_DIR="$D/state-renamefail" \
      timeout 6 bash "$LANE_DONE" "$W" ad144-renamefail "rt-renamefail-$$" "$RSESS" 2>&1)
    rc=$?
    wait "$sigpid" 2>/dev/null
    want_exit "real tmux: a failed rename-window still exits zero -- the release is what matters, not the rename (agent-dotfiles#194)" "$rc" 0 "$out"
    name=$(rtmux display-message -p -t "${RSESS}:${W}" '#{window_name}')
    want_contains "real tmux: the window keeps its stale task name when rename-window fails" "ad144-renamefail" "$name"
    after=$(LEDGER_STATE="$D/state-renamefail" ledger status 2>&1)
    want_contains "real tmux: the lane is STILL released in the ledger despite the failed rename (#194 -- this is the fix)" '"status":"complete"' "$after"

    # ...and that reordering is what makes the property true, not the
    # harness. Patch a copy that puts the rename back in FRONT of the ledger
    # release with its `|| exit 1` restored -- exactly #183's original
    # shipped ordering, which is the bug #194 exists to fix -- and confirm
    # the SAME failed rename now leaves the ledger showing the task still
    # delivered, not complete: the assertion just above would go red against
    # this copy. If it does not, the assertion above is not actually
    # exercising the reorder.
    OLDORDER="$D/lane-done-old-order.sh"
    patch_rc2=0
    python3 - "$LANE_DONE" "$OLDORDER" <<'PY' || patch_rc2=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()

ledger_start = 'if ! LEDGER_OUT=$('
ledger_end_marker = "sed 's/^/  /' <<<\"$LEDGER_OUT\" >&2\nfi\n"
i0 = text.index(ledger_start)
i1 = text.index(ledger_end_marker, i0) + len(ledger_end_marker)
assert i0 > 0 and i1 > i0, "ledger release block not found -- script shape changed"
ledger_block = text[i0:i1]

rename_start = 'FREE_IDX="$(tmux display-message -p -t "$TARGET" \'#{window_index}\' 2>/dev/null)"'
rename_end_marker = "fi\n\nexit 0\n"
j0 = text.index(rename_start)
j1 = text.index(rename_end_marker, j0) + len(rename_end_marker)
assert j0 > i1, "rename-window block is not after the ledger block -- script shape changed, mutation would not test the reorder"
rename_block = text[j0:j1]

before = text[:i0]
after = text[j1:]
old_rename = 'tmux rename-window -t "$TARGET" "free-${WINDOW}" || exit 1\n\n'
open(dst, "w").write(before + old_rename + ledger_block + "\nexit 0\n" + after)
PY
    if [ "$patch_rc2" -ne 0 ]; then
      bad "setup: patched a copy of lane-done.sh with the pre-#194 rename-before-release ordering" \
        "could not patch $LANE_DONE (exit $patch_rc2) -- treating as a failure, not a skip"
    else
      ok "setup: patched a copy of lane-done.sh with the pre-#194 rename-before-release ordering"
      LEDGER_STATE="$D/state-renamefail2" ledger record-dispatch \
        --lane "${RSESS}:${W}" --task ad144-renamefail2 \
        --summary "#194 mutation: pre-#194 ordering" \
        --pane-id '%9' --pane-path "$D" --command claude \
        --server-id 'socket:1' --session-id '$0' --issue 144 >/dev/null 2>&1
      rtmux rename-window -t "${RSESS}:${W}" ad144-renamefail2
      ( sleep 1; rtmux wait-for -S "rt-renamefail2-$$" ) &
      sigpid=$!
      out=$(PATH="$RTSHIM:$PATH" env -u TMUX AGENT_SUPERVISOR_STATE_DIR="$D/state-renamefail2" \
        timeout 6 bash "$OLDORDER" "$W" ad144-renamefail2 "rt-renamefail2-$$" "$RSESS" 2>&1)
      rc=$?
      wait "$sigpid" 2>/dev/null
      name=$(rtmux display-message -p -t "${RSESS}:${W}" '#{window_name}')
      mutated_status=$(LEDGER_STATE="$D/state-renamefail2" ledger status 2>&1)
      if [ "$rc" -eq 1 ] && [ "$name" = "ad144-renamefail2" ] && grep -qF '"status":"delivered"' <<<"$mutated_status"; then
        ok "mutation confirmed: restoring rename-before-release strands the lane in the ledger on a failed rename (the assertion above would now be red)"
      else
        bad "mutation confirmed: restoring rename-before-release strands the lane in the ledger on a failed rename" \
          "expected exit 1, window STILL ad144-renamefail2, and a delivered (not complete) status; got exit $rc, window '$name': $out / $mutated_status"
      fi
    fi

    # --- 6. THE #241 REGRESSION: a resolved index names a different window --
    #
    # This is the defect, reproduced end to end against real tmux rather than
    # asserted about. `renumber-windows on` -- this server's setting, read
    # directly in agent-dotfiles#241 -- makes closing any window shift every
    # HIGHER index down by one. lane-done.sh is where that costs most,
    # because it holds a resolved target for as long as the work takes: it is
    # handed a window at dispatch and uses it when the worker's channel fires,
    # hours later, and windows close in between.
    #
    # The harm is not a stolen rename -- the name-match guard prevents that,
    # and it stays exactly as it is. The harm is a LOST one: the finished
    # lane's window is no longer at the index that was resolved, some
    # stranger's is, the guard correctly refuses the stranger, and a lane that
    # genuinely finished is never released. That is #102's shape (capacity
    # silently falling to zero) reached through the mechanism built to prevent
    # it.
    #
    # Four windows so there is something above AND below the one that closes;
    # the lane under test sits above it, which is the only position the shift
    # can reach.
    RS2="lanedone241-$$"
    if ! rtmux new-session -d -s "$RS2" -n keep-a 2>/dev/null; then
      bad "real tmux #241: started a second throwaway session" "new-session failed"
    else
      # The setting the whole defect depends on, set HERE rather than assumed:
      # a test that silently ran against `renumber-windows off` would pass for
      # the wrong reason and pin nothing.
      rtmux set-option -t "$RS2" renumber-windows on >/dev/null 2>&1
      rtmux new-window -t "$RS2" -n victim >/dev/null 2>&1
      rtmux new-window -t "$RS2" -n ad241-finished-lane >/dev/null 2>&1
      rtmux new-window -t "$RS2" -n keep-b >/dev/null 2>&1

      idx_of() { rtmux list-windows -t "$RS2" -F '#{window_name} #{window_index}' | awk -v n="$1" '$1==n{print $2}'; }
      wid_of() { rtmux list-windows -t "$RS2" -F '#{window_name} #{window_id}'    | awk -v n="$1" '$1==n{print $2}'; }

      # What a dispatcher resolves, at dispatch time: both identities of the
      # same window, exactly the pair `lanes.sh --free` now emits.
      RESOLVED_IDX="$(idx_of ad241-finished-lane)"
      RESOLVED_ID="$(wid_of ad241-finished-lane)"

      # ...and then a lower-indexed window closes, which is an ordinary event
      # in this estate: every completed lane's window, every hand-opened
      # scratch window, every `kill-window` in a recovery.
      rtmux kill-window -t "$RS2:$(idx_of victim)" 2>/dev/null
      SHIFTED_IDX="$(idx_of ad241-finished-lane)"
      STRANGER="$(rtmux display-message -p -t "$RS2:$RESOLVED_IDX" '#{window_name}' 2>/dev/null)"

      if [ "$SHIFTED_IDX" = "$RESOLVED_IDX" ]; then
        bad "real tmux #241: closing a lower window shifts the lane's index" \
          "index did not move ($RESOLVED_IDX -> $SHIFTED_IDX) -- renumber-windows is not on for this server, so this section proves nothing"
      else
        ok "real tmux #241: closing a lower-indexed window shifted the lane from index $RESOLVED_IDX to $SHIFTED_IDX; the resolved index now names '$STRANGER'"
        want_contains "real tmux #241: ...and the window ID did NOT move -- it still names the lane" \
          "ad241-finished-lane" "$(rtmux display-message -p -t "$RS2:$RESOLVED_ID" '#{window_name}' 2>/dev/null)"

        # A shared runner: same script, same signal, one argument different.
        # `$1` is the script, `$2` the window argument, `$3` the state dir.
        # NOT called inside a command substitution: `rc241` has to survive
        # into the assertions, and an exit code set in a subshell does not.
        # The output goes to a file for the same reason.
        run241() {
          ( sleep 1; rtmux wait-for -S "rt-241-$$" ) &
          local sigpid=$!
          env -u TMUX TMUX_TMPDIR="$RT" AGENT_SUPERVISOR_STATE_DIR="$3" \
            timeout 8 bash "$1" "$2" ad241-finished-lane "rt-241-$$" "$RS2" >"$D/out241" 2>&1
          rc241=$?
          wait "$sigpid" 2>/dev/null
        }
        seed241() {
          LEDGER_STATE="$1" ledger record-dispatch \
            --lane "${RS2}:${RESOLVED_IDX}" --task ad241-finished-lane \
            --summary "#241 stable window ids" \
            --pane-id '%241' --pane-path "$D" --command claude \
            --server-id 'socket:1' --session-id '$0' --issue 241 >/dev/null 2>&1
        }

        # THE PRE-#241 SHAPE: the index-addressed contract, and the rename
        # built from the argument rather than from the window's current index.
        # Both halves reverted together, because both are what changed.
        PRE241="$SHIPDIR/lane-done-pre241.sh"
        patch_rc241=0
        python3 - "$LANE_DONE" "$PRE241" <<'PY' || patch_rc241=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
start = 'FREE_IDX="$(tmux display-message -p -t "$TARGET" \'#{window_index}\' 2>/dev/null)"'
end_marker = "fi\n\nexit 0\n"
i0 = text.index(start)
i1 = text.index(end_marker, i0) + len(end_marker)
assert text.count(start) == 1, "free-N index lookup not unique -- script shape changed"
old = ('if ! tmux rename-window -t "$TARGET" "free-${WINDOW}"; then\n'
       '  echo "lane-done: rename-window FAILED for ${TARGET}" >&2\n'
       'fi\n\nexit 0\n')
open(dst, "w").write(text[:i0] + old + text[i1:])
PY
        if [ "$patch_rc241" -ne 0 ]; then
          bad "setup: patched a pre-#241 (index-addressed) copy of lane-done.sh" \
            "could not patch $LANE_DONE (exit $patch_rc241) -- treating as a failure, not a skip"
        else
          ok "setup: patched a pre-#241 (index-addressed) copy of lane-done.sh"
          chmod +x "$PRE241"

          # RED: the old shape, given the index it resolved at dispatch.
          seed241 "$D/state-241-pre"
          run241 "$PRE241" "$RESOLVED_IDX" "$D/state-241-pre"; out=$(cat "$D/out241")
          pre_name="$(rtmux display-message -p -t "$RS2:$RESOLVED_ID" '#{window_name}')"
          pre_status=$(LEDGER_STATE="$D/state-241-pre" ledger status 2>&1)
          if [ "$rc241" -ne 0 ] && [ "$pre_name" = "ad241-finished-lane" ] \
             && grep -qF '"status":"delivered"' <<<"$pre_status"; then
            ok "real tmux #241: the index-addressed shape lands on '$STRANGER', refuses on the name guard, and leaves a FINISHED lane unrenamed and unreleased (exit $rc241) -- this is the defect"
          else
            bad "real tmux #241: the index-addressed shape strands the finished lane" \
              "expected non-zero exit, the window still named ad241-finished-lane, and a delivered (not complete) status; got exit $rc241, window '$pre_name': $out / $pre_status"
          fi

          # GREEN: the shipped script, given the window id -- same signal,
          # same estate, same instant.
          seed241 "$D/state-241-fix"
          run241 "$LANE_DONE" "$RESOLVED_ID" "$D/state-241-fix"; out=$(cat "$D/out241")
          fix_name="$(rtmux display-message -p -t "$RS2:$RESOLVED_ID" '#{window_name}')"
          fix_status=$(LEDGER_STATE="$D/state-241-fix" ledger status 2>&1)
          want_exit "real tmux #241: the id-addressed shape finds the lane and exits zero" "$rc241" 0 "$out"
          want_contains "real tmux #241: the finished lane IS released in the ledger" '"status":"complete"' "$fix_status"
          # free-N takes N from the window's CURRENT index, not from the one
          # resolved at dispatch: a window that moved to index 2 must not be
          # labelled free-3, or the name lies about the very thing the window
          # list is read for.
          if [ "$fix_name" = "free-${SHIFTED_IDX}" ]; then
            ok "real tmux #241: renamed to free-${SHIFTED_IDX} -- the CURRENT index, not the resolved one (free-${RESOLVED_IDX})"
          else
            bad "real tmux #241: renamed using the window's current index" \
              "expected free-${SHIFTED_IDX}, got '$fix_name': $out"
          fi
          # And the stranger the old shape reached was never touched by the
          # new one. A fix that merely moved which window it damages is not a
          # fix.
          want_contains "real tmux #241: the window at the stale index is untouched" \
            "$STRANGER" "$(rtmux display-message -p -t "$RS2:$RESOLVED_IDX" '#{window_name}' 2>/dev/null)"
        fi
      fi
      rtmux kill-session -t "$RS2" 2>/dev/null
    fi
  fi
  cleanup_rt
  trap - EXIT
fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
