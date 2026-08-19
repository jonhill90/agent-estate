#!/bin/bash
# agent-supervisor#367: director-loop.sh must notice live/ is gone and try
# to recover it, because it is the one process on a fixed cadence that keeps
# running when live/ does not exist -- its own LaunchAgent
# (launchd/com.jonhill.director-loop.plist) invokes it from the shared
# checkout, never from live/ itself, unlike watchdog.sh's LaunchAgent (see
# director-loop.sh's own header comment on this check). This is what closes
# the "nothing noticed until a downstream command failed with ENOENT" gap
# from the issue.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOOP="$HERE/../../scripts/supervisor/director-loop.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }

echo "director-loop.sh: live/ existence + registration check"

D=$(mktemp -d)

# A fixture repo standing in for the shared checkout SUPERVISOR_REPO names.
git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/src"
SRC="$D/src"
git -C "$SRC" config user.email test@example.com
git -C "$SRC" config user.name "Test"
git -C "$SRC" checkout -q -b main
mkdir -p "$SRC/scripts/supervisor"
cat >"$SRC/scripts/supervisor/watchdog.sh" <<'EOF'
#!/bin/bash
set -uo pipefail
STATUS="${SUPERVISOR_STATUS:?}"
mkdir -p "$(dirname "$STATUS")"
printf 'checked:  %s\nstate:    pane_unreadable\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$STATUS"
exit 0
EOF
chmod +x "$SRC/scripts/supervisor/watchdog.sh"
git -C "$SRC" add -A
git -C "$SRC" commit -q -m base
git -C "$SRC" push -q -u origin main
GOOD_SHA=$(git -C "$SRC" rev-parse HEAD)

QUOTA_SAFE="$HERE/stubs/quota-safe"
LIVE="$D/live"
run() { # run <state-dir> [live-path]
  local statedir="$1" livepath="${2:-$LIVE}"
  SUPERVISOR_STATE="$statedir" SUPERVISOR_LIVE="$livepath" SUPERVISOR_REPO="$SRC" \
    QUOTA_GATE="$QUOTA_SAFE" DIRECTOR_LOOP_TARGET="no-such-session-367:@1" \
    bash "$LOOP"
}

# --- live/ entirely absent, no rollback sha: refuses, does not invent -----
S1=$(mktemp -d)
out=$(run "$S1" 2>&1); rc=$?
want_exit "missing live/ with no rollback sha exits loud (nonzero, not the tmux-missing code)" "$rc" 6 "$out"
if grep -qi "LIVE MISSING" <<<"$out"; then ok "logs LIVE MISSING"; else bad "logs LIVE MISSING" "$out"; fi
if grep -qi "STILL MISSING" <<<"$out"; then ok "logs that recovery did not succeed"; else bad "logs that recovery did not succeed" "$out"; fi
if [ -e "$LIVE" ]; then bad "nothing is invented at $LIVE" "$out"; else ok "nothing is invented at $LIVE"; fi

# --- live/ absent, rollback sha recorded: recovers and continues the tick -
S2=$(mktemp -d)
printf '%s\n' "$GOOD_SHA" >"$S2/.live-rollback-sha"
out=$(run "$S2" 2>&1); rc=$?
# Recovery succeeds, so the script proceeds to the NEXT gate (the target
# tmux session does not exist in this test) -- exit 3, not 6.
want_exit "missing live/ with a rollback sha recovers and reaches the next gate (exit 3, not LIVE-missing's 6)" "$rc" 3 "$out"
if grep -qi "LIVE RECOVERED" <<<"$out"; then ok "logs LIVE RECOVERED"; else bad "logs LIVE RECOVERED" "$out"; fi
if [ -d "$LIVE" ] && git -C "$LIVE" rev-parse --git-dir >/dev/null 2>&1; then ok "live/ exists and is a real worktree after recovery"; else bad "live/ exists and is a real worktree after recovery" "$out"; fi
rm -rf "$LIVE"
git -C "$SRC" worktree prune >/dev/null 2>&1

# --- live/ already present and registered: the check is a no-op ----------
S3=$(mktemp -d)
git -C "$SRC" worktree add -q --detach "$LIVE" origin/main
out=$(run "$S3" 2>&1); rc=$?
want_exit "a healthy, registered live/ is not touched (reaches the next gate, exit 3)" "$rc" 3 "$out"
if grep -qi "LIVE MISSING" <<<"$out"; then bad "a healthy live/ never logs LIVE MISSING" "$out"; else ok "a healthy live/ never logs LIVE MISSING"; fi
git -C "$SRC" worktree remove --force "$LIVE" >/dev/null 2>&1
git -C "$SRC" worktree prune >/dev/null 2>&1

# --- live/ absent and advance-live.sh is not beside this script: refuses --
SCRATCH=$(mktemp -d)
cp "$HERE/../../scripts/supervisor/director-loop.sh" "$SCRATCH/director-loop.sh"
S4=$(mktemp -d)
out=$(SUPERVISOR_STATE="$S4" SUPERVISOR_LIVE="$LIVE" SUPERVISOR_REPO="$SRC" \
  QUOTA_GATE="$QUOTA_SAFE" DIRECTOR_LOOP_TARGET="no-such-session-367:@1" \
  bash "$SCRATCH/director-loop.sh" 2>&1); rc=$?
want_exit "no advance-live.sh beside the script refuses (exit 6)" "$rc" 6 "$out"
if grep -qi "cannot recover live/ automatically" <<<"$out"; then ok "names the missing advance-live.sh"; else bad "names the missing advance-live.sh" "$out"; fi
rm -rf "$SCRATCH"

rm -rf "$D"

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
