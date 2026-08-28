#!/bin/bash
# The host-pressure gate (agent-supervisor#500 / corpus directive
# it-ef548e51e71daebe): `dispatch.sh` must refuse a NEW dispatch when
# host-pressure.sh reports the host cannot safely take it, and must never
# silently proceed just because the gate could not be consulted.
#
# WHY THIS EXISTS. Jon, blunt: the supervisor's own resource usage was
# making his machine unusable, and #500 confirmed no concurrent-process cap
# or load-based dispatch gate existed anywhere in this estate. This file is
# the test that proves the fix is load-bearing both directions -- mutate the
# gate call out of dispatch.sh and watch the refusal case go red; mutate
# host-pressure.sh itself to always-refuse and watch the success case go
# red -- per this repository's own "a fix is not complete until a test that
# failed before it passes after it" policy, applied to a new feature rather
# than a bugfix.
#
# This suite controls the REAL host-pressure.sh against the REAL host's
# metrics, tuned via its own SUPERVISOR_MAX_LOAD_PER_CORE/MIN_FREE_MEM_GB
# env overrides (host-pressure.sh's own documented seam) -- an impossible
# threshold (0.001 load/core) is refused by every real host without faking
# sysctl/vm_stat, and a trivial one (0 -- disabled) is never refused by any
# real host. test_host_pressure.sh (this same directory) is where
# sysctl/vm_stat ARE faked, to test host-pressure.sh's own arithmetic in
# isolation; this file is about dispatch.sh's INTEGRATION with it.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DISPATCH="$HERE/../../scripts/supervisor/dispatch.sh"
HOST_PRESSURE="$HERE/../../scripts/supervisor/host-pressure.sh"
export DISPATCH_LIVE_PANE=1
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }

echo "dispatch.sh -- the host-pressure gate (#500)"

D=$(mktemp -d); mkdir -p "$D/bin" "$D/roots"
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cp "$HERE/stubs/tmux-dispatch" "$D/bin/tmux"

git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/repo" 2>/dev/null
REPO="$D/repo"
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name "Test"
git -C "$REPO" checkout -q -b main
echo one > "$REPO/file.txt"
git -C "$REPO" add file.txt
git -C "$REPO" commit -q -m "initial"
git -C "$REPO" push -q -u origin main
git -C "$REPO" remote set-url origin "git@github.com:acme/agent-dotfiles.git"

cat > "$D/issues" <<'FIX'
500|| the host-pressure gate itself
501|| the host-pressure gate itself, mutation cases (separate issue: 500 is claimed by the ALLOW case above and stays claimed)
FIX
: > "$D/prs"
echo "do the thing" > "$D/brief.md"

cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX

# All of these except the ALLOWED case are refused BEFORE the claim step
# (host-pressure runs first in dispatch.sh, before even the quota gate --
# see dispatch.sh's own comment at the call site for why cheapest-first
# ordering also shortens the "quota check slowing things down" complaint's
# own latency), so they never touch GitHub and can all safely reuse #500.
n=0
run() {
  n=$((n+1))
  : > "$D/tmux.log"
  rm -rf "$D/panes"; mkdir -p "$D/panes"
  PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
    TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 DISPATCH_CONFIRM_TRIES=2 \
    DISPATCH_RESPAWN_SETTLE=0 DISPATCH_LAUNCH_SETTLE=0 DISPATCH_SESSION_TIMEOUT=0 \
    AGENT_SUPERVISOR_STATE_DIR="$(mktemp -d "$D/state.XXXXXX")" \
    STUB_PANE_PATH="$REPO" WORKTREE_ROOT="$D/roots" \
    QUOTA_GATE="$HERE/stubs/quota-safe" \
    "$@" \
    bash "$DISPATCH" 500 "hostpressure-case-$n" "$D/brief.md" acme/agent-dotfiles "$REPO" 2>&1
}

# --- the load-bearing REFUSE case: an impossible threshold refuses, never
# proceeds. Every real host's load/core is > 0.001. ------------------------
REFUSE_OUT=$(run env SUPERVISOR_MAX_LOAD_PER_CORE=0.001 SUPERVISOR_MIN_FREE_MEM_GB=0); REFUSE_RC=$?
want_exit "an impossible load/core threshold refuses to dispatch" "$REFUSE_RC" 1 "$REFUSE_OUT"
want_contains "...names host-pressure as the reason" "host-pressure" "$REFUSE_OUT"
want_contains "...names the actual over-threshold comparison" ">= 0.001" "$REFUSE_OUT"
if [ -s "$D/tmux.log" ]; then
  bad "...and never sends the brief" "tmux saw activity: $(cat "$D/tmux.log")"
else
  ok "...and never sends the brief"
fi
CLAIM_LOG="$D/panes"
if [ -d "$CLAIM_LOG" ] && [ -n "$(ls -A "$CLAIM_LOG" 2>/dev/null)" ]; then
  bad "...and never claims the issue or builds a worktree" "pane activity recorded: $(ls -A "$CLAIM_LOG")"
else
  ok "...and never claims the issue or builds a worktree"
fi

# --- the load-bearing ALLOW case: disabled thresholds (0 = off, matching
# host-pressure.sh's own documented convention) proceed past the gate to a
# full, successful dispatch. -------------------------------------------------
ALLOW_OUT=$(run env SUPERVISOR_MAX_LOAD_PER_CORE=0 SUPERVISOR_MIN_FREE_MEM_GB=0); ALLOW_RC=$?
want_exit "disabled thresholds let the same dispatch proceed" "$ALLOW_RC" 0 "$ALLOW_OUT"

# --- MUTATION 1: remove the gate call from dispatch.sh -> REFUSE case must
# go RED. Proves the REFUSE assertion above is pinned to dispatch.sh
# actually calling host-pressure.sh, not to something else already
# refusing (the quota stub is SAFE, the lane fixture is valid). -------------
# agent-supervisor#716: the gate call now lives in dispatch-preflight.sh, not
# dispatch.sh's own text -- copy the WHOLE directory (same sibling-resolution
# reasoning the comment above already gives for why a mutant needs its
# siblings alongside it) into a scratch dir, then patch whichever file in
# that copy actually carries the marker.
MUT_NOGATE_DIR=$(mktemp -d "$D/mutant-nogate.XXXXXX")
cp -R "$HERE/../../scripts/supervisor/." "$MUT_NOGATE_DIR/"
rm -rf "$MUT_NOGATE_DIR/__pycache__"
chmod +x "$MUT_NOGATE_DIR"/*.sh
MUT_NOGATE="$MUT_NOGATE_DIR/dispatch.sh"
mut_rc=0
PYTHONPATH="$HERE" python3 - "$MUT_NOGATE_DIR" <<'PY' || mut_rc=$?
import sys
import _dispatch_mutate as M
target = sys.argv[1]
marker = '''if [ -x "$HERE/host-pressure.sh" ]; then
  HOST_PRESSURE_OUT=$("$HERE/host-pressure.sh"); HOST_PRESSURE_RC=$?
  if [ "$HOST_PRESSURE_RC" -ne 0 ]; then
    echo "dispatch: $HOST_PRESSURE_OUT -- NOT dispatching #$ISSUE_ARG" >&2
    exit 1
  fi
else
  echo "dispatch: host-pressure.sh missing or not executable at $HERE -- refusing to guess whether this host can take another dispatch" >&2
  exit 1
fi'''
# Simulates the fix never having been added: no gate call, no refusal, no
# fail-closed else -- straight through to the rest of dispatch.sh.
M.patch(target, marker, "true  # host-pressure gate removed by mutation test")
PY
if [ "$mut_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh with the host-pressure call site disabled" "could not patch $DISPATCH (exit $mut_rc)"
else
  ok "setup: patched a copy of dispatch.sh with the host-pressure call site disabled"
  chmod +x "$MUT_NOGATE"
  n=$((n+1))
  : > "$D/tmux.log"; rm -rf "$D/panes"; mkdir -p "$D/panes"
  MUT_OUT=$(PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" \
    DISPATCH_SETTLE=0 DISPATCH_CONFIRM_TRIES=2 DISPATCH_RESPAWN_SETTLE=0 \
    DISPATCH_LAUNCH_SETTLE=0 DISPATCH_SESSION_TIMEOUT=0 \
    AGENT_SUPERVISOR_STATE_DIR="$(mktemp -d "$D/state.XXXXXX")" \
    STUB_PANE_PATH="$REPO" WORKTREE_ROOT="$D/roots" QUOTA_GATE="$HERE/stubs/quota-safe" \
    SUPERVISOR_MAX_LOAD_PER_CORE=0.001 SUPERVISOR_MIN_FREE_MEM_GB=0 \
    bash "$MUT_NOGATE" 501 "hostpressure-mut-$n" "$D/brief.md" acme/agent-dotfiles "$REPO" 2>&1); MUT_RC=$?
  if [ "$MUT_RC" -eq 0 ]; then
    ok "mutation confirmed: removing the gate call lets an impossible-threshold dispatch proceed anyway (REFUSE case's GREEN assertion would now be RED)"
  else
    bad "mutation confirmed: removing the gate call lets an impossible-threshold dispatch proceed anyway" "expected exit 0 on the mutant, got $MUT_RC: $MUT_OUT"
  fi
fi

# --- MUTATION 2: force host-pressure.sh to always refuse -> ALLOW case
# must go RED. Proves the ALLOW assertion above is pinned to the gate's
# real verdict, not to dispatch.sh skipping the gate structurally. ----------
MUT_ALWAYS_REFUSE="$D/host-pressure-mutant-refuse.sh"
cat > "$MUT_ALWAYS_REFUSE" <<'FIX'
#!/usr/bin/env bash
echo "host-pressure: mutated to always refuse -- test instrument, not a real reading"
exit 1
FIX
chmod +x "$MUT_ALWAYS_REFUSE"
# dispatch.sh calls "$HERE/host-pressure.sh" by fixed sibling path, not via
# PATH -- swap the whole scripts dir's copy for the duration of this one
# case, restore immediately after, so no other test in this file (or a
# concurrent run) ever sees the mutant.
cp "$HOST_PRESSURE" "$D/host-pressure.sh.orig"
cp "$MUT_ALWAYS_REFUSE" "$HOST_PRESSURE"
n=$((n+1))
: > "$D/tmux.log"; rm -rf "$D/panes"; mkdir -p "$D/panes"
MUT2_OUT=$(PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
  LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" \
  DISPATCH_SETTLE=0 DISPATCH_CONFIRM_TRIES=2 DISPATCH_RESPAWN_SETTLE=0 \
  DISPATCH_LAUNCH_SETTLE=0 DISPATCH_SESSION_TIMEOUT=0 \
  AGENT_SUPERVISOR_STATE_DIR="$(mktemp -d "$D/state.XXXXXX")" \
  STUB_PANE_PATH="$REPO" WORKTREE_ROOT="$D/roots" QUOTA_GATE="$HERE/stubs/quota-safe" \
  SUPERVISOR_MAX_LOAD_PER_CORE=0 SUPERVISOR_MIN_FREE_MEM_GB=0 \
  bash "$DISPATCH" 501 "hostpressure-mut2-$n" "$D/brief.md" acme/agent-dotfiles "$REPO" 2>&1); MUT2_RC=$?
cp "$D/host-pressure.sh.orig" "$HOST_PRESSURE"
if [ "$MUT2_RC" -eq 1 ]; then
  ok "mutation confirmed: a host-pressure.sh that always refuses blocks even a disabled-threshold dispatch (ALLOW case's GREEN assertion would now be RED)"
else
  bad "mutation confirmed: a host-pressure.sh that always refuses blocks even a disabled-threshold dispatch" "expected exit 1 on the mutant, got $MUT2_RC: $MUT2_OUT"
fi

echo
echo "host-pressure gate: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
