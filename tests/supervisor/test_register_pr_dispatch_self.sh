#!/bin/bash
# agent-supervisor#538/#539. register-pr-dispatch-self.sh, driven through a
# REAL (isolated) tmux server -- same discipline as test_lane_identity.sh's
# own coverage of register-lane-self.sh, which this script wraps.
#
# #539's own review finding: the script used to take `--pr`/`--repo` as pure
# caller assertions with nothing verifying the pane's worktree actually
# produced that PR -- a false claim became TRUSTED, load-bearing input to
# the merge gate's independence check. This suite's mutation check (below)
# is built from that exact attack, not a paraphrase of it: a lane sitting on
# the PR's real branch registers; a lane sitting on any other branch (or no
# branch, or an unresolvable PR) is refused.
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
command -v git  >/dev/null 2>&1 || missing "no git on PATH"

S="pr-dispatch-self-test-$$"
RT="$(mktemp -d "${TMPDIR:-/tmp}/pr-dispatch-self-tmux.XXXXXX")"
D="$(mktemp -d "${TMPDIR:-/tmp}/pr-dispatch-self.XXXXXX")"
STATE="$D/state"; WORK="$D/work"; BIN="$D/bin"; FIX="$D/fixtures"
mkdir -p "$STATE" "$WORK" "$BIN" "$FIX"
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

# A fake `gh` keyed on PR number, fixture files so each test case can give a
# different answer without racing another. Only `pr view --json headRefName`
# is exercised by this script; anything else is an error in the test itself.
cat > "$BIN/gh" <<'FAKE'
#!/bin/bash
set -uo pipefail
FIX="${GH_FIX:?}"
if [ "$1 $2" = "pr view" ]; then
  num="$3"
  f="$FIX/pr_$num"
  if [ -f "$f" ]; then jq -r '.headRefName' "$f"; exit 0; fi
  echo "fake gh: no fixture for pr view $num" >&2
  exit 1
fi
echo "fake gh: unexpected command: $*" >&2
exit 1
FAKE
chmod +x "$BIN/gh"
export PATH="$BIN:$PATH" GH_FIX="$FIX" AGENT_GH_BIN="$BIN/gh"

# A real git worktree, not just a directory -- the cross-check reads
# `git branch --show-current`, so this must be an actual repo with a real
# branch checked out for the "matches" case to mean anything.
git -C "$WORK" init -q -b real-branch
git -C "$WORK" config user.email t@example.com
git -C "$WORK" config user.name Test
echo one > "$WORK/f"; git -C "$WORK" add f; git -C "$WORK" commit -q -m one

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

run_self() {  # run_self <pr>
  TMUX_PANE="$LANE_PANE" LANES_SUPERVISOR_WINDOW="$SUP_IDX" \
    bash "$SELF" --pr "$1" --repo "$REPO" --harness codex 2>&1
}

# ============================================================================
# #539's mutation check, both directions, from the actual attack
# ============================================================================

# --- the real attack: a lane claims a PR number that is NOT its branch -----
printf '{"headRefName": "someone-elses-branch"}\n' > "$FIX/pr_600"
out=$(run_self 600); rc=$?
[ "$rc" -eq 1 ] && ok "#539: refuses when the PR's real branch is NOT the branch this worktree has checked out" \
  || bad "#539: refuses on branch mismatch" "rc=$rc: $out"
grep -q "real-branch" <<<"$out" && grep -q "someone-elses-branch" <<<"$out" \
  && ok "...and names both branches in the refusal" || bad "names both branches" "$out"
[ "$(contrib_lane 600)" = "" ] && ok "...and wrote NO contributor row for it -- the false claim never reached the ledger" \
  || bad "no row written for a false claim" "$(contrib_lane 600)"

# --- the positive case: the worktree really IS on the PR's own branch ------
printf '{"headRefName": "real-branch"}\n' > "$FIX/pr_531"
out=$(run_self 531); rc=$?
[ "$rc" -eq 0 ] && ok "#539: registers when the PR's real branch matches this worktree's checked-out branch" \
  || bad "#539: registers on a genuine branch match" "rc=$rc: $out"
[ "$(contrib_lane 531)" = "$LANE" ] && ok "...and contributor-pr-lanes resolves it to the real lane" \
  || bad "contributor-pr-lanes resolves the real lane" "got '$(contrib_lane 531)', want '$LANE'"

# --- fails closed: gh cannot resolve the PR at all --------------------------
# No fixture file for 602 -- the fake gh's own "no fixture" path, exit 1.
out=$(run_self 602); rc=$?
[ "$rc" -eq 1 ] && ok "#539: refuses when the PR's branch cannot be resolved at all (fails closed, not open)" \
  || bad "#539: refuses on unresolvable PR" "rc=$rc: $out"
[ "$(contrib_lane 602)" = "" ] && ok "...and wrote no row" || bad "no row for an unresolvable PR" "$(contrib_lane 602)"

# --- fails closed: detached HEAD, no branch to compare ----------------------
git -C "$WORK" checkout -q --detach
printf '{"headRefName": "real-branch"}\n' > "$FIX/pr_603"
out=$(run_self 603); rc=$?
[ "$rc" -eq 1 ] && ok "#539: refuses on a detached HEAD -- no branch to honestly compare against the PR" \
  || bad "#539: refuses on detached HEAD" "rc=$rc: $out"
[ "$(contrib_lane 603)" = "" ] && ok "...and wrote no row" || bad "no row for a detached-HEAD claim" "$(contrib_lane 603)"
git -C "$WORK" checkout -q real-branch

# --- idempotent: re-running the genuine case does not error or duplicate ---
out=$(run_self 531); rc=$?
[ "$rc" -eq 0 ] && ok "re-running the genuine case is idempotent, not an error" \
  || bad "re-running is idempotent" "rc=$rc: $out"

# --- the supervisor's own window is never registered as a lane (inherited
# from register-lane-self.sh, exercised here to prove the wrapper does not
# route around it) ----------------------------------------------------------
SUP_PANE=$(tmux display-message -p -t "$S:$SUP_IDX" '#{pane_id}')
printf '{"headRefName": "real-branch"}\n' > "$FIX/pr_999"
out=$(TMUX_PANE="$SUP_PANE" LANES_SUPERVISOR_WINDOW="$SUP_IDX" \
      bash "$SELF" --pr 999 --repo "$REPO" --harness codex 2>&1); rc=$?
[ "$rc" -eq 1 ] && ok "refuses to register the supervisor window as a PR author" \
  || bad "refuses supervisor window" "rc=$rc: $out"
[ "$(contrib_lane 999)" = "" ] && ok "...and wrote no contributor row for it" \
  || bad "wrote no row for the supervisor window" "$(contrib_lane 999)"

echo "  -> $pass ok, $fail failed"
[ "$fail" -eq 0 ]
