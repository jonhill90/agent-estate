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
    WAIT_DIR="$D/wait" STUB_WAIT_TIMEOUT=1 bash "$script" "$@" 2>&1
}
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
marker = 'if ! tmux wait-for "$CHANNEL" 2>/dev/null; then\n  echo "lane-done: channel \'$CHANNEL\' was not signaled -- not renaming ${SESSION}:${IDX}" >&2\n  exit 1\nfi\n\n'
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
      env -u TMUX TMUX_TMPDIR="$RT" timeout "$1" bash "$2" \
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
  fi
  cleanup_rt
  trap - EXIT
fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
