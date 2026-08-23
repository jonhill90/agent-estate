#!/bin/bash
# agent-supervisor#520 (and the false-pass half of #513).
#
# `verdict-independence.sh` decides independence by comparing `lanes.pane_id`
# for two lane ids. Nothing on that path ever re-measured a row against tmux,
# so a stale or never-observed registration supplied the merge gate with a
# `pane_id` it treated as a real identity. Measured on this estate on
# 2026-08-23: all four of `estate:2`..`estate:5` held rows naming panes
# (`%38`, `%39`, `%51`, `%52`) that did not exist on the running server (whose
# panes were `%1`..`%5`), and every one of them satisfied `post-verdict.sh`'s
# "is this lane registered" check.
#
# This drives REAL tmux, because the thing under test is precisely the
# disagreement between a ledger row and a live server -- a stub that answers
# from the same fixtures the ledger was seeded from cannot produce it.
#
# INVARIANT 4: this suite creates a session, so `TMUX_TMPDIR` is set and
# `assert_isolated_tmux` gates it before anything is created or killed
# (#185 extended that requirement from the destructive verbs to creation).
# `lane_identity.py` itself only ever runs `list-panes`/`display-message`.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
IDENTITY="$SUP/lane_identity.py"
REGISTER_SELF="$SUP/register-lane-self.sh"
source "$SUP/tmux-isolation.sh"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

echo "lane_identity.py / register-lane-self.sh"

command -v tmux >/dev/null 2>&1 || { echo "  SKIP no tmux on PATH"; exit 0; }
command -v jq   >/dev/null 2>&1 || { echo "  SKIP no jq"; exit 0; }
# A pane whose `#{pane_current_command}` is a REAL entry in
# `adapter.HARNESS_COMMANDS` is needed, because `cli.py register` refuses a
# harness the live pane contradicts and this suite must not route around that.
# `node` is that entry (it is what every Node-based harness reads as -- see
# HARNESS_COMMANDS own comment), and it is deliberately AMBIGUOUS between
# codex and copilot, which exercises the `--harness` path as well.
# Copying or symlinking some other binary under a harness name was tried
# first and does not work on macOS: a copied system binary fails code-signing
# and the pane dies instantly, and a symlink reports the TARGET name.
command -v node >/dev/null 2>&1 || { echo "  SKIP no node on PATH (needed for a pane whose command names a real harness)"; exit 0; }

S="lane-identity-test-$$"
RT="$(mktemp -d "${TMPDIR:-/tmp}/lane-identity-tmux.XXXXXX")"
D="$(mktemp -d "${TMPDIR:-/tmp}/lane-identity.XXXXXX")"
STATE="$D/state"; BIN="$D/bin"; WORK="$D/work"
mkdir -p "$STATE" "$BIN" "$WORK"
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$RT"
assert_isolated_tmux || exit 1

cleanup() {
  unset TMUX TMUX_PANE
  export TMUX_TMPDIR="$RT"
  tmux kill-session -t "$S" 2>/dev/null
  rm -rf "$RT" "$D"
}
trap cleanup EXIT INT TERM

PANE_CMD="node -e 'setTimeout(function(){}, 600000)'"

status_of() {  # status_of <lane>
  python3 "$IDENTITY" --lane "$1" --state-dir "$STATE" 2>/dev/null | jq -r '.status // "no-output"'
}
detail_of() {  # detail_of <lane>
  python3 "$IDENTITY" --lane "$1" --state-dir "$STATE" 2>/dev/null | jq -r '.detail // ""'
}
seed_row() {  # seed_row <lane> <pane_id> <server_id> [transport] [harness]
  python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
Ledger(sys.argv[2]).register_lane(
    lane=sys.argv[3], pane_id=sys.argv[4], nonce="n", harness=sys.argv[8], repo=sys.argv[6],
    server_id=sys.argv[5], session_id="$0", command="node", transport=sys.argv[7],
)
' "$SUP" "$STATE" "$1" "$2" "$3" "$WORK" "${4:-send-keys}" "${5:-codex}"
}

# --- build the live session ------------------------------------------------
tmux new-session -d -s "$S" -c "$WORK" "$PANE_CMD" || { echo "  FAIL could not create the isolated session"; exit 1; }
tmux new-window -t "$S" -c "$WORK" "$PANE_CMD"
# Settle: `#{pane_current_command}` reads the shell for a moment after spawn.
for _ in $(seq 1 40); do
  [ "$(tmux display-message -p -t "$S" '#{pane_current_command}' 2>/dev/null)" = "node" ] && break
  sleep 0.25
done
if [ "$(tmux display-message -p -t "$S" '#{pane_current_command}' 2>/dev/null)" != "node" ]; then
  echo "  SKIP the isolated pane never settled on a node command"; exit 0
fi

# `list-windows`, not `display-message -t "$S"`: targeting a SESSION answers
# for whichever window is ACTIVE (the one just created), which is invariant
# 10's trap reproduced inside its own test. Ask for the list and take the ends.
SUP_IDX=$(tmux list-windows -t "$S" -F '#{window_index}' | head -n1)  # first window
LANE_IDX=$(tmux list-windows -t "$S" -F '#{window_index}' | tail -n1) # second window
LANE="$S:$LANE_IDX"
LANE_PANE=$(tmux display-message -p -t "$S:$LANE_IDX" '#{pane_id}')
SERVER_ID=$(tmux display-message -p -t "$S:$LANE_IDX" '#{socket_path}:#{session_created}')

# ============================================================================
# lane_identity.py -- all three answers, each from a real cause
# ============================================================================

# --- verified: the row agrees with the live server -------------------------
seed_row "$LANE" "$LANE_PANE" "$SERVER_ID"
got=$(status_of "$LANE")
[ "$got" = "verified" ] && ok "a row matching the live pane is verified" \
  || bad "a row matching the live pane is verified" "got '$got' -- $(detail_of "$LANE")"

# --- contradicted: the exact estate:2..estate:5 shape ----------------------
# The row names a pane that does not exist on the server the row itself names.
# This is the case that used to read as a perfectly good identity.
seed_row "$LANE" "%99999" "$SERVER_ID"
got=$(status_of "$LANE")
[ "$got" = "contradicted" ] && ok "#520: a row naming a pane the live server does not have is contradicted" \
  || bad "#520: pane absent from the live server is contradicted" "got '$got' -- $(detail_of "$LANE")"
# Captured to a variable, not piped: `grep -q` exits at the first match and
# closes the pipe, which SIGPIPEs `jq` upstream -- under `pipefail` that makes
# a SUCCESSFUL match read as a failed pipeline.
d=$(detail_of "$LANE")
case "$d" in
  *"%99999"*) ok "#520: the refusal names the pane it could not find" ;;
  *) bad "#520: refusal names the pane" "$d" ;;
esac

# --- contradicted: pane is live but is a DIFFERENT lane now ----------------
# A window renumber, or a lane id reused for another pane. The pane exists, so
# a bare "does this pane_id exist" check would pass; the identity still does
# not hold.
seed_row "$S:$SUP_IDX" "$LANE_PANE" "$SERVER_ID"
got=$(status_of "$S:$SUP_IDX")
[ "$got" = "contradicted" ] && ok "#520: a live pane that is a different lane now is contradicted" \
  || bad "#520: live pane, wrong lane, is contradicted" "got '$got' -- $(detail_of "$S:$SUP_IDX")"

# --- contradicted: right pane and lane, but a dead server incarnation ------
# The ids line up by coincidence on a later server. Registered against an
# older `session_created`, which is exactly what a tmux restart leaves behind
# (this estate produced that on 2026-08-23, rows written at 06:03-06:16
# against a session created the previous day).
seed_row "$LANE" "$LANE_PANE" "${SERVER_ID%:*}:1"
got=$(status_of "$LANE")
[ "$got" = "contradicted" ] && ok "#520: a row from an older server incarnation is contradicted" \
  || bad "#520: older server incarnation is contradicted" "got '$got' -- $(detail_of "$LANE")"

# --- unverifiable, three ways, none of which may read as a pass ------------
got=$(status_of "no-such-lane:7")
[ "$got" = "unverifiable" ] && ok "an unregistered lane is unverifiable, not verified" \
  || bad "unregistered lane is unverifiable" "got '$got'"

seed_row "$LANE" "claude-print:$LANE" "$SERVER_ID" claude-print claude
got=$(status_of "$LANE")
[ "$got" = "unverifiable" ] && ok "#292: an off-pane (claude-print) lane is unverifiable, never contradicted" \
  || bad "off-pane lane is unverifiable" "got '$got' -- $(detail_of "$LANE")"

seed_row "$LANE" "$LANE_PANE" "$D/no-such-socket:1"
got=$(status_of "$LANE")
[ "$got" = "unverifiable" ] && ok "a socket that is not there at all is unverifiable, never contradicted" \
  || bad "missing socket is unverifiable" "got '$got' -- $(detail_of "$LANE")"

# --- the instrument can fail: prove the verified case is load-bearing ------
# `verify-the-instrument`: a checker that answered "verified" unconditionally
# would have passed the first case above. Re-seed the true row and confirm it
# flips back, so "verified" is a measurement and not a constant.
seed_row "$LANE" "$LANE_PANE" "$SERVER_ID"
got=$(status_of "$LANE")
[ "$got" = "verified" ] && ok "verified is a measurement -- it returns after each contradicted case" \
  || bad "verified returns after the contradicted cases" "got '$got' -- $(detail_of "$LANE")"

# ============================================================================
# register-lane-self.sh -- observation only, no caller-supplied identity
# ============================================================================
rm -rf "$STATE"; mkdir -p "$STATE"
export AGENT_SUPERVISOR_STATE_DIR="$STATE"

# --- refuses outright with no $TMUX_PANE to anchor on ----------------------
out=$(env -u TMUX_PANE bash "$REGISTER_SELF" 2>&1); rc=$?
[ "$rc" -eq 1 ] && ok "refuses when \$TMUX_PANE is unset (cannot observe its own pane)" \
  || bad "refuses without TMUX_PANE" "rc=$rc: $out"
grep -q 'TMUX_PANE' <<<"$out" && ok "...and the refusal says why" || bad "refusal names TMUX_PANE" "$out"

# --- registers the lane it is really in ------------------------------------
# TMUX_PANE is exported here the way tmux exports it into a pane's own
# process environment -- that is the fact the script is anchored on, and it
# is the only part of this case that is simulated rather than live. Every
# other value (lane id, cwd, command, server) is read off the real server.
out=$(TMUX_PANE="$LANE_PANE" LANES_SUPERVISOR_WINDOW="$SUP_IDX" bash "$REGISTER_SELF" --harness codex 2>&1); rc=$?
[ "$rc" -eq 0 ] && ok "registers the pane it is actually running in" || bad "registers its own pane" "rc=$rc: $out"
got=$(status_of "$LANE")
[ "$got" = "verified" ] && ok "...and the row it wrote verifies against the live server" \
  || bad "self-registered row verifies" "got '$got' -- $(detail_of "$LANE")"
recorded=$(python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
row = Ledger(sys.argv[2]).get_lane(sys.argv[3]) or {}
print(row.get("pane_id",""), row.get("repo",""))
' "$SUP" "$STATE" "$LANE")
# `realpath`-normalised: macOS resolves $TMPDIR through /private, so the cwd
# tmux reports is the resolved form of the directory this test created.
WORK_REAL=$(cd "$WORK" && pwd -P)
[ "$recorded" = "$LANE_PANE $WORK_REAL" ] && ok "...recording the MEASURED pane and cwd, not anything supplied" \
  || bad "records measured pane and cwd" "got '$recorded', wanted '$LANE_PANE $WORK_REAL'"

# --- the nonce it minted was really stamped onto the pane ------------------
# The nonce is an incarnation token written to BOTH the ledger and the pane by
# `adapter.TmuxAdapter.register_lane`; a registration that wrote only the
# ledger half would leave `_verified_lane` unable to confirm the pane later.
ledger_nonce=$(python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
print((Ledger(sys.argv[2]).get_lane(sys.argv[3]) or {}).get("nonce",""))
' "$SUP" "$STATE" "$LANE")
pane_nonce=$(tmux display-message -p -t "$LANE_PANE" '#{@hill90_lane_nonce}')
[ -n "$ledger_nonce" ] && [ "$ledger_nonce" = "$pane_nonce" ] \
  && ok "the minted nonce is stamped on the pane and matches the ledger" \
  || bad "nonce stamped on the pane" "ledger='$ledger_nonce' pane='$pane_nonce'"

# --- --expect-lane is a CHECK, never an override ---------------------------
rm -rf "$STATE"; mkdir -p "$STATE"
out=$(TMUX_PANE="$LANE_PANE" LANES_SUPERVISOR_WINDOW="$SUP_IDX" bash "$REGISTER_SELF" --harness codex --expect-lane "somewhere:42" 2>&1); rc=$?
[ "$rc" -eq 1 ] && ok "--expect-lane naming a different lane refuses" || bad "--expect-lane mismatch refuses" "rc=$rc: $out"
[ "$(status_of "somewhere:42")" = "unverifiable" ] && [ "$(status_of "$LANE")" = "unverifiable" ] \
  && ok "...and registers nothing at all -- neither the claimed id nor the real one" \
  || bad "expect-lane mismatch registers nothing" "$out"

# --- the supervisor's own window is never registered as a lane -------------
SUP_PANE=$(tmux display-message -p -t "$S:$SUP_IDX" '#{pane_id}')
out=$(TMUX_PANE="$SUP_PANE" LANES_SUPERVISOR_WINDOW="$SUP_IDX" bash "$REGISTER_SELF" --harness codex 2>&1); rc=$?
[ "$rc" -eq 1 ] && ok "refuses to register the supervisor window as a lane" || bad "supervisor window refused" "rc=$rc: $out"
[ "$(status_of "$S:$SUP_IDX")" = "unverifiable" ] && ok "...and wrote no row for it" \
  || bad "supervisor window wrote no row" "$out"

# ============================================================================
# post-verdict.sh -- the same contradiction, caught before anything is posted
# ============================================================================
# The merge gate is the enforcement point (test_merge_pr_lane_identity.sh);
# this is the cheap-failure point. A fake `gh` proves the refusal happens
# BEFORE any post, not after one.
PVBIN="$D/pvbin"; mkdir -p "$PVBIN"
cat > "$PVBIN/gh" <<'FAKEGH'
#!/bin/bash
echo "posted" > "${GH_CALLED:?}"
echo "https://example.invalid/x#issuecomment-1"
FAKEGH
chmod +x "$PVBIN/gh"
export GH_CALLED="$D/gh-called"
export AGENT_GH_BIN="$PVBIN/gh"

rm -rf "$STATE"; mkdir -p "$STATE"
seed_row "$LANE" "%99999" "$SERVER_ID"
rm -f "$GH_CALLED"
out=$(printf 'Verdict: APPROVE
Review-Lane: %s
' "$LANE"   | LANES_SUPERVISOR_WINDOW="$SUP_IDX" bash "$SUP/post-verdict.sh" acme/repo 1 2>&1); rc=$?
[ "$rc" -eq 8 ] && ok "#520: post-verdict refuses a Review-Lane whose registration the server contradicts (exit 8)"   || bad "post-verdict refuses a contradicted Review-Lane" "rc=$rc: $out"
[ ! -f "$GH_CALLED" ] && ok "...and nothing was posted" || bad "nothing posted for a contradicted lane" "$out"

# ...and the same body posts once the registration is true, so the refusal
# above is the check firing and not this path being broken outright.
seed_row "$LANE" "$LANE_PANE" "$SERVER_ID"
rm -f "$GH_CALLED"
out=$(printf 'Verdict: APPROVE
Review-Lane: %s
' "$LANE"   | LANES_SUPERVISOR_WINDOW="$SUP_IDX" bash "$SUP/post-verdict.sh" acme/repo 1 2>&1); rc=$?
[ -f "$GH_CALLED" ] && ok "...and the identical body posts once the registration is verified"   || bad "verified lane posts normally" "rc=$rc: $out"

echo "  -> $pass ok, $fail failed"
[ "$fail" -eq 0 ]
