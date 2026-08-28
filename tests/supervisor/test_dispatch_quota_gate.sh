#!/bin/bash
# The quota gate (agent-supervisor#227): `dispatch.sh` must refuse to
# dispatch on anything other than a SAFE (exit 0) verdict from the quota
# gate, and it must treat any exit code it does not explicitly enumerate --
# 127 included -- as UNKNOWN and fail closed.
#
# WHY THIS EXISTS. `scripts/supervisor/quota.sh` was untracked -- absent from
# `main` and from the deployed `~/.local/state/agent-dotfiles-supervisor/live/`
# tree every real supervisor runs from. A caller that read "not 1" as
# "proceed" would read the resulting `No such file or directory` (exit 127)
# as permission to spend, which is the exact inversion the gate exists to
# prevent -- the mechanism behind the $80 -> $8 burn. This file is the test
# that breaks the guard and watches it go red, then restores it and watches
# it go green, per repository policy: a fix is not complete until a test
# that failed before it passes after it.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DISPATCH="$HERE/../../scripts/supervisor/dispatch.sh"
# agent-supervisor#171: this suite is about the gate itself, refusing before
# a lane is ever picked in most cases -- the tmux flow is what its handful
# of SAFE-verdict cases exercise, and it stubs no `claude` binary; see
# test_dispatch.sh's own comment on this same override.
# agent-supervisor#500: dispatch.sh now runs the host-pressure gate BEFORE
# the quota gate this file is about -- disabled here for the same reason
# test_dispatch.sh disables it (this file's own concern is quota, not host
# resources, and a real CI runner's load/free-memory is uncontrolled).
export SUPERVISOR_MAX_LOAD_PER_CORE=0
export SUPERVISOR_MIN_FREE_MEM_GB=0
export SUPERVISOR_MAX_AGENT_SESSIONS=0
export DISPATCH_LIVE_PANE=1
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }

echo "dispatch.sh -- the quota gate (#227)"

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
227|| the quota gate itself
FIX
: > "$D/prs"
echo "do the thing" > "$D/brief.md"

cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX

# All of these except the SAFE case are refused BEFORE the claim step (the
# quota gate runs first in dispatch.sh), so they never touch GitHub and can
# all safely reuse issue #227 -- only the SAFE case below actually claims it.
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
    QUOTA_GATE="${QUOTA_GATE:?run needs QUOTA_GATE}" \
    bash "$DISPATCH" 227 "quota-case-$n" "$D/brief.md" acme/agent-dotfiles "$REPO" 2>&1
}

# --- the load-bearing case: a missing gate refuses, never proceeds --------
# This is the guard going red before the fix and green after: point the gate
# at a path that does not exist (bash's own exit 127 on exec failure) and
# assert the dispatch refuses rather than sending the brief.
MISSING="$D/no-such-quota-gate.sh"
[ ! -e "$MISSING" ] || { echo "setup error: $MISSING unexpectedly exists"; exit 1; }
MISSING_OUT=$(QUOTA_GATE="$MISSING" run); MISSING_RC=$?
want_exit "a missing gate (exit 127) refuses to dispatch" "$MISSING_RC" 1 "$MISSING_OUT"
want_contains "...and says UNKNOWN, not safe" "UNKNOWN" "$MISSING_OUT"
if [ -s "$D/tmux.log" ]; then
  bad "...and never sends the brief" "tmux saw activity: $(cat "$D/tmux.log")"
else
  ok "...and never sends the brief"
fi

# --- restore the gate: the identical dispatch now proceeds ----------------
# Same case, same everything, only the gate path changes -- this is the
# "restore it and assert the test goes green" half.
SAFE_OUT=$(QUOTA_GATE="$HERE/stubs/quota-safe" run); SAFE_RC=$?
want_exit "the same dispatch proceeds once the gate resolves to SAFE (exit 0)" "$SAFE_RC" 0 "$SAFE_OUT"

# --- exit 1: WIND DOWN is a legitimate stop, not proceed ------------------
cat > "$D/bin/quota-1" <<'FIX'
#!/bin/bash
echo "quota: WIND DOWN -- 9% remaining in session, below 15%"
exit 1
FIX
chmod +x "$D/bin/quota-1"
W_OUT=$(QUOTA_GATE="$D/bin/quota-1" run); W_RC=$?
want_exit "exit 1 (WIND DOWN) refuses to dispatch" "$W_RC" 1 "$W_OUT"
want_contains "...names it a legitimate stop" "legitimate stop" "$W_OUT"
want_contains "...names quota-watch.sh as the way back" "quota-watch.sh" "$W_OUT"

# --- exit 2: UNAVAILABLE is UNKNOWN, never safe ----------------------------
cat > "$D/bin/quota-2" <<'FIX'
#!/bin/bash
echo "quota: UNAVAILABLE -- codexbar could not read the quota" >&2
exit 2
FIX
chmod +x "$D/bin/quota-2"
U2_OUT=$(QUOTA_GATE="$D/bin/quota-2" run); U2_RC=$?
want_exit "exit 2 (UNAVAILABLE) refuses to dispatch" "$U2_RC" 1 "$U2_OUT"
want_contains "...and says UNKNOWN" "UNKNOWN" "$U2_OUT"

# --- exit 3: codexbar MISSING is UNKNOWN, never safe -----------------------
cat > "$D/bin/quota-3" <<'FIX'
#!/bin/bash
echo "quota: codexbar not installed -- quota state is UNKNOWN, not fine" >&2
exit 3
FIX
chmod +x "$D/bin/quota-3"
U3_OUT=$(QUOTA_GATE="$D/bin/quota-3" run); U3_RC=$?
want_exit "exit 3 (codexbar MISSING) refuses to dispatch" "$U3_RC" 1 "$U3_OUT"
want_contains "...and says UNKNOWN" "UNKNOWN" "$U3_OUT"

# --- exit 127: exec failure (the actual #227 shape) is UNKNOWN -------------
cat > "$D/bin/quota-127" <<'FIX'
#!/bin/bash
exit 127
FIX
chmod +x "$D/bin/quota-127"
U127_OUT=$(QUOTA_GATE="$D/bin/quota-127" run); U127_RC=$?
want_exit "exit 127 refuses to dispatch" "$U127_RC" 1 "$U127_OUT"
want_contains "...and says UNKNOWN" "UNKNOWN" "$U127_OUT"

# --- an arbitrary unrecognised code is UNKNOWN, not "not 1 so proceed" ----
# This is the exact bug this issue names: `if [ $rc -eq 1 ]` (or any
# enumeration of only the codes someone remembered) treats everything else,
# including a code nobody has seen before, as safe. 42 is picked because it
# is not 0, 1, 2, 3, or 127 -- nothing about it is special otherwise.
cat > "$D/bin/quota-42" <<'FIX'
#!/bin/bash
echo "quota: this code was made up for a test" >&2
exit 42
FIX
chmod +x "$D/bin/quota-42"
U42_OUT=$(QUOTA_GATE="$D/bin/quota-42" run); U42_RC=$?
want_exit "an arbitrary unrecognised exit code (42) refuses to dispatch" "$U42_RC" 1 "$U42_OUT"
want_contains "...and says UNKNOWN" "UNKNOWN" "$U42_OUT"

echo
echo "quota gate: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
