#!/bin/bash
# agent-supervisor#538. register-pr-dispatch-self.sh, driven through a REAL
# (isolated) tmux server -- same discipline as test_lane_identity.sh's own
# coverage of register-lane-self.sh, which this script wraps.
#
# INVARIANT 4: creates a session, so `TMUX_TMPDIR` is set and
# `assert_isolated_tmux` gates it before anything is created or killed.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
SELF="$SUP/register-pr-dispatch-self.sh"
CLI="$SUP/cli.py"
source "$SUP/tmux-isolation.sh"
REPO="acme/repo"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

echo "register-pr-dispatch-self.sh (live tmux)"

missing() {
  if [ -n "${CI:-}" ]; then
    echo "  FAIL $1 -- required on CI, where validate.yml installs it; a silent skip here would be a suite that tests nothing"
    exit 1
  fi
  echo "  SKIP $1"
  exit 0
}
command -v tmux >/dev/null 2>&1 || missing "no tmux on PATH"
command -v jq   >/dev/null 2>&1 || missing "no jq"
command -v node >/dev/null 2>&1 || missing "no node on PATH (needed for a pane whose command names a real harness)"

S="pr-dispatch-self-test-$$"
RT="$(mktemp -d "${TMPDIR:-/tmp}/pr-dispatch-self-tmux.XXXXXX")"
D="$(mktemp -d "${TMPDIR:-/tmp}/pr-dispatch-self.XXXXXX")"
STATE="$D/state"; WORK="$D/work"
mkdir -p "$STATE" "$WORK"
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$RT"
assert_isolated_tmux || exit 1
export AGENT_SUPERVISOR_STATE_DIR="$STATE"

cleanup() {
  unset TMUX TMUX_PANE
  export TMUX_TMPDIR="$RT"
  tmux kill-session -t "$S" 2>/dev/null
  rm -rf "$RT" "$D"
}
trap cleanup EXIT INT TERM

contrib_lane() {  # contrib_lane <pr> -> the first contributor lane, or ""
  python3 "$CLI" --state-dir "$STATE" contributor-pr-lanes --pr "$1" --repo "$REPO" 2>/dev/null \
    | jq -r '.contributors[0].lane // ""'
}

# --- refuses with no $TMUX_PANE to anchor on --------------------------------
out=$(env -u TMUX_PANE bash "$SELF" --pr 1 --repo "$REPO" 2>&1); rc=$?
[ "$rc" -eq 1 ] && ok "refuses when \$TMUX_PANE is unset" || bad "refuses without TMUX_PANE" "rc=$rc: $out"

# --- refuses a non-numeric --pr before touching tmux at all -----------------
out=$(TMUX_PANE="%does-not-matter" bash "$SELF" --pr abc --repo "$REPO" 2>&1); rc=$?
[ "$rc" -eq 1 ] && ok "refuses a non-numeric --pr" || bad "refuses non-numeric --pr" "rc=$rc: $out"

# --- usage errors on missing required flags ---------------------------------
out=$(bash "$SELF" --repo "$REPO" 2>&1); rc=$?
[ "$rc" -eq 2 ] && ok "usage error with no --pr" || bad "usage error with no --pr" "rc=$rc: $out"
out=$(bash "$SELF" --pr 1 2>&1); rc=$?
[ "$rc" -eq 2 ] && ok "usage error with no --repo" || bad "usage error with no --repo" "rc=$rc: $out"

# --- build the live session -------------------------------------------------
PANE_CMD="node -e 'setTimeout(function(){}, 600000)'"
tmux new-session -d -s "$S" -c "$WORK" "$PANE_CMD" || { echo "  FAIL could not create the isolated session"; exit 1; }
tmux new-window -t "$S" -c "$WORK" "$PANE_CMD"
for _ in $(seq 1 40); do
  [ "$(tmux list-panes -a -F '#{pane_current_command}' 2>/dev/null | sort -u | tr '\n' ' ')" = "node " ] && break
  sleep 0.25
done
SUP_IDX=$(tmux list-windows -t "$S" -F '#{window_index}' | head -n1)
LANE_IDX=$(tmux list-windows -t "$S" -F '#{window_index}' | tail -n1)
LANE="$S:$LANE_IDX"
LANE_PANE=$(tmux display-message -p -t "$S:$LANE_IDX" '#{pane_id}')

# --- the estate-loop shape: registers a PR with NO issue anywhere ----------
out=$(TMUX_PANE="$LANE_PANE" LANES_SUPERVISOR_WINDOW="$SUP_IDX" \
      bash "$SELF" --pr 531 --repo "$REPO" --harness codex 2>&1); rc=$?
[ "$rc" -eq 0 ] && ok "#538: registers the pane's own lane as PR #531's contributor" \
  || bad "#538: registers PR #531" "rc=$rc: $out"
[ "$(contrib_lane 531)" = "$LANE" ] && ok "...and contributor-pr-lanes resolves it to the real lane" \
  || bad "contributor-pr-lanes resolves the real lane" "got '$(contrib_lane 531)', want '$LANE'"
grep -q "recorded and confirmed" <<<"$out" && ok "...and reports its own read-back confirmation" \
  || bad "reports read-back confirmation" "$out"

# --- idempotent: re-running for the same PR does not error or duplicate ---
out=$(TMUX_PANE="$LANE_PANE" LANES_SUPERVISOR_WINDOW="$SUP_IDX" \
      bash "$SELF" --pr 531 --repo "$REPO" --harness codex 2>&1); rc=$?
[ "$rc" -eq 0 ] && ok "re-running for the same PR is idempotent, not an error" \
  || bad "re-running is idempotent" "rc=$rc: $out"

# --- the supervisor's own window is never registered as a lane (inherited
# from register-lane-self.sh, exercised here to prove the wrapper does not
# route around it) ----------------------------------------------------------
SUP_PANE=$(tmux display-message -p -t "$S:$SUP_IDX" '#{pane_id}')
out=$(TMUX_PANE="$SUP_PANE" LANES_SUPERVISOR_WINDOW="$SUP_IDX" \
      bash "$SELF" --pr 999 --repo "$REPO" --harness codex 2>&1); rc=$?
[ "$rc" -eq 1 ] && ok "refuses to register the supervisor window as a PR author" \
  || bad "refuses supervisor window" "rc=$rc: $out"
[ "$(contrib_lane 999)" = "" ] && ok "...and wrote no contributor row for it" \
  || bad "wrote no row for the supervisor window" "$(contrib_lane 999)"

echo "  -> $pass ok, $fail failed"
[ "$fail" -eq 0 ]
