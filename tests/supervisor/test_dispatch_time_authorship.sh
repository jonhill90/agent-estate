#!/bin/bash
# agent-supervisor#553 / docs/decisions/0009-estate-lane-authorship-dispatch-
# time-only.md. register-lane-dispatch-self.sh (record a task BEFORE any PR
# exists) + register-pr-for-lane-self.sh (attach a PR to that already-open
# task, once one exists) -- the retirement of register-pr-dispatch-self.sh's
# worktree-content comparison (numeric-only -> branch-name -> commit-SHA,
# all three defeated in one review cycle) in favour of a fact about
# ASSIGNMENT rather than a fact about CONTENT.
#
# Same "REAL isolated tmux server, not stubbed" discipline as
# test_register_pr_dispatch_self.sh, which this suite's own mutation check
# (below) is built directly against -- reproducing the exact shape
# agent-supervisor#552 measured live: a lane whose PANE sits in one repo's
# worktree while the PR it is attaching belongs to a DIFFERENT repo. The
# retired mechanism refused this (a false negative for a genuine author);
# this suite proves the replacement does not, and that it does not need to
# read the pane's cwd, branch, or any commit at all to get there.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
DISPATCH_SELF="$SUP/register-lane-dispatch-self.sh"
ATTACH_SELF="$SUP/register-pr-for-lane-self.sh"
REGISTER_SELF="$SUP/register-lane-self.sh"
CLI="$SUP/cli.py"
source "$SUP/tmux-isolation.sh"
REPO="acme/repo"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

echo "dispatch-time authorship recording (agent-supervisor#553)"

missing() {
  if [ -n "${CI:-}" ]; then
    echo "  FAIL $1 -- required on CI, where validate.yml installs it; a silent skip here would be a suite that tests nothing"
    exit 1
  fi
  echo "  SKIP $1"
  exit 0
}
command -v tmux >/dev/null 2>&1 || missing "no tmux on PATH"
command -v node >/dev/null 2>&1 || missing "no node on PATH (needed for a pane whose command names a real harness)"

S="dispatch-auth-test-$$"
RT="$(mktemp -d "${TMPDIR:-/tmp}/dispatch-auth-tmux.XXXXXX")"
D="$(mktemp -d "${TMPDIR:-/tmp}/dispatch-auth.XXXXXX")"
STATE="$D/state"; WORK="$D/agent-supervisor-shaped"; OTHERWORK="$D/agent-dotfiles-shaped"
mkdir -p "$STATE" "$WORK" "$OTHERWORK"
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

pr_task_lane() {  # pr_task_lane <pr> -> the resolved lane, or ""
  python3 "$CLI" --state-dir "$STATE" pr-task --repo "$REPO" --pr "$1" 2>/dev/null \
    | python3 -c "import json,sys; print(json.load(sys.stdin).get('lane') or '')" 2>/dev/null
}

wait_for_pane() {  # wait_for_pane <session> -- polls until its pane's own command settles
  for _ in $(seq 1 40); do
    [ "$(tmux list-panes -a -F '#{pane_current_command}' 2>/dev/null | sort -u | tr '\n' ' ')" = "node " ] && return 0
    sleep 0.25
  done
}

PANE_CMD="node -e 'setTimeout(function(){}, 600000)'"
export LANES_SUPERVISOR_WINDOW=99

# ============================================================================
# #552's own reproduction: a lane whose PANE is anchored in one repo's
# worktree, attaching a PR opened against a DIFFERENT repo -- the exact
# shape estate:5 hit trying to register #539 itself, and the exact shape
# the retired branch/SHA comparison could never pass.
# ============================================================================
tmux new-session -d -s "$S" -c "$OTHERWORK" "$PANE_CMD" || { echo "  FAIL could not create the isolated session"; exit 1; }
wait_for_pane "$S"
LANE_PANE=$(tmux display-message -p -t "$S:0" '#{pane_id}')
LANE=$(tmux display-message -p -t "$LANE_PANE" '#{session_name}:#{window_index}')

out=$(TMUX_PANE="$LANE_PANE" bash "$DISPATCH_SELF" --task "estate-crossrepo-task" --summary "cross-repo brief" --harness codex 2>&1)
rc=$?
[ "$rc" -eq 0 ] && ok "dispatch-time record succeeds before any PR exists, regardless of pane cwd" \
  || bad "dispatch-time record" "rc=$rc: $out"

out=$(TMUX_PANE="$LANE_PANE" bash "$ATTACH_SELF" --pr 900 --repo "$REPO" 2>&1)
rc=$?
[ "$rc" -eq 0 ] && ok "#552 CLOSED: a lane whose pane and whose PR's repo differ still attaches successfully" \
  || bad "#552 cross-repo attach" "rc=$rc: $out"

[ "$(pr_task_lane 900)" = "$LANE" ] && ok "...and author_lane_for's own pr-task path (Path 3) resolves it to the real lane, no worktree/branch/commit ever consulted" \
  || bad "pr-task resolves the real lane" "got '$(pr_task_lane 900)', want '$LANE'"

# --- multi-PR: a second PR from the SAME still-open task also attaches ----
out=$(TMUX_PANE="$LANE_PANE" bash "$ATTACH_SELF" --pr 950 --repo "$REPO" 2>&1)
rc=$?
[ "$rc" -eq 0 ] && ok "a second PR from the same still-open dispatch also attaches (one brief, more than one PR -- agent-supervisor#547's own finding that this is not always 1:1)" \
  || bad "second PR attach" "rc=$rc: $out"
[ "$(pr_task_lane 950)" = "$LANE" ] && ok "...and resolves to the same lane as the first" \
  || bad "second PR resolves the real lane" "got '$(pr_task_lane 950)', want '$LANE'"

# ============================================================================
# fail-closed cases
# ============================================================================

# --- registered, but no open task -------------------------------------------
S2="dispatch-auth-b-$$"
tmux new-session -d -s "$S2" -c "$WORK" "$PANE_CMD"
wait_for_pane "$S2"
PANE2=$(tmux display-message -p -t "$S2:0" '#{pane_id}')
TMUX_PANE="$PANE2" bash "$REGISTER_SELF" --harness codex >/dev/null 2>&1
out=$(TMUX_PANE="$PANE2" bash "$ATTACH_SELF" --pr 901 --repo "$REPO" 2>&1)
rc=$?
[ "$rc" -eq 1 ] && ok "fail-closed: a REGISTERED lane with no open task cannot attach a PR" || bad "no-open-task refusal" "rc=$rc: $out"
grep -q "no open task" <<<"$out" && ok "...and names that specific reason" || bad "names the reason" "$out"
tmux kill-session -t "$S2" 2>/dev/null

# --- never registered at all: a DIFFERENT, more precise refusal -----------
S2b="dispatch-auth-c-$$"
tmux new-session -d -s "$S2b" -c "$WORK" "$PANE_CMD"
wait_for_pane "$S2b"
PANE2b=$(tmux display-message -p -t "$S2b:0" '#{pane_id}')
out=$(TMUX_PANE="$PANE2b" bash "$ATTACH_SELF" --pr 903 --repo "$REPO" 2>&1)
rc=$?
[ "$rc" -eq 1 ] && ok "fail-closed: a NEVER-registered lane also refuses" || bad "never-registered refusal" "rc=$rc: $out"
grep -q "not a confirmed, live registration" <<<"$out" && ok "...with a distinct reason from the no-open-task case, not the same message reused" \
  || bad "distinct reason for never-registered" "$out"
tmux kill-session -t "$S2b" 2>/dev/null

# --- no \$TMUX_PANE: both scripts refuse -------------------------------------
out=$(env -u TMUX_PANE bash "$DISPATCH_SELF" --task x --summary y 2>&1); rc=$?
[ "$rc" -eq 1 ] && ok "fail-closed: register-lane-dispatch-self.sh refuses with no \$TMUX_PANE" || bad "no pane, dispatch-self" "rc=$rc: $out"
out=$(env -u TMUX_PANE bash "$ATTACH_SELF" --pr 1 --repo "$REPO" 2>&1); rc=$?
[ "$rc" -eq 1 ] && ok "fail-closed: register-pr-for-lane-self.sh refuses with no \$TMUX_PANE" || bad "no pane, pr-for-lane-self" "rc=$rc: $out"

# --- usage errors -------------------------------------------------------------
out=$(bash "$DISPATCH_SELF" --task x 2>&1); rc=$?
[ "$rc" -eq 2 ] && ok "usage error with no --summary" || bad "usage: no summary" "rc=$rc: $out"
out=$(bash "$DISPATCH_SELF" --summary y 2>&1); rc=$?
[ "$rc" -eq 2 ] && ok "usage error with no --task" || bad "usage: no task" "rc=$rc: $out"
out=$(bash "$ATTACH_SELF" --repo "$REPO" 2>&1); rc=$?
[ "$rc" -eq 2 ] && ok "usage error with no --pr" || bad "usage: no pr" "rc=$rc: $out"
out=$(bash "$ATTACH_SELF" --pr abc --repo "$REPO" 2>&1); rc=$?
[ "$rc" -eq 1 ] && ok "refuses a non-numeric --pr (past the usage check, a real refusal)" || bad "non-numeric --pr" "rc=$rc: $out"

# ============================================================================
# mutation check: the PR-attachment CLI command resolves the task
# SERVER-SIDE, from --lane alone -- there is no --task argument for a caller
# to misuse, so the check is: a lane can only ever attach to ITS OWN open
# task, never another lane's, even asked directly at the CLI layer
# (bypassing the self-anchored *-self.sh scripts entirely).
# ============================================================================
S3="dispatch-auth-d-$$"
tmux new-session -d -s "$S3" -c "$WORK" "$PANE_CMD"
wait_for_pane "$S3"
PANE3=$(tmux display-message -p -t "$S3:0" '#{pane_id}')
TMUX_PANE="$PANE3" bash "$DISPATCH_SELF" --task "estate-lane3-task" --summary "lane 3's own brief" --harness codex >/dev/null 2>&1
LANE3=$(tmux display-message -p -t "$PANE3" '#{session_name}:#{window_index}')
out=$(python3 "$CLI" --state-dir "$STATE" attach-pr-to-open-task --lane "$LANE3" --repo "$REPO" --pr 902 2>&1)
rc=$?
[ "$rc" -eq 0 ] && ok "attach-pr-to-open-task resolves lane3's OWN task when asked for lane3 (sanity: the mechanism works at all)" \
  || bad "sanity check" "rc=$rc: $out"
[ "$(pr_task_lane 902)" = "$LANE3" ] && ok "...and PR 902 resolves to lane3, never lane1 -- no cross-lane leak possible since there is no --task argument to name someone else's" \
  || bad "no cross-lane leak" "got '$(pr_task_lane 902)', want '$LANE3'"
tmux kill-session -t "$S3" 2>/dev/null

# --- mutation: prove the per-lane resolution is real, not incidental ------
# Patch a COPY of cli.py so attach-pr-to-open-task falls back to LANE3's
# real open task (from the section above) whenever the CALLING lane has
# none of its own, instead of refusing. Then ask it to attach a PR as a
# lane with NO open task of its own -- a real cross-lane leak if the
# mutation succeeds, which the un-mutated code already proved (above) it
# refuses. This anchors that refusal on the actual per-lane check, not on
# some unrelated validation happening to also catch this case.
MUT_CLI="$D/cli-mutated.py"
python3 - "$CLI" "$MUT_CLI" "$LANE3" <<'PY'
import sys
src, dst, fallback_lane = sys.argv[1], sys.argv[2], sys.argv[3]
text = open(src).read()
anchor = "open_task = ledger.get_open_task_for_lane(args.lane)\n        if open_task is None:\n            raise RuntimeError("
assert anchor in text, "mutation target moved -- attach-pr-to-open-task's own handler shape changed"
mutated = text.replace(
    anchor,
    f'open_task = ledger.get_open_task_for_lane(args.lane) or ledger.get_open_task_for_lane({fallback_lane!r})\n'
    '        if False:\n            raise RuntimeError(',
    1,
)
assert mutated != text
open(dst, "w").write(mutated)
PY
S4="dispatch-auth-e-$$"
tmux new-session -d -s "$S4" -c "$WORK" "$PANE_CMD"
wait_for_pane "$S4"
PANE4=$(tmux display-message -p -t "$S4:0" '#{pane_id}')
TMUX_PANE="$PANE4" bash "$REGISTER_SELF" --harness codex >/dev/null 2>&1
LANE4=$(tmux display-message -p -t "$PANE4" '#{session_name}:#{window_index}')
# Confirm this lane genuinely has no open task of its own first -- the
# baseline the mutation is supposed to change.
baseline_out=$(python3 "$CLI" --state-dir "$STATE" attach-pr-to-open-task --lane "$LANE4" --repo "$REPO" --pr 998 2>&1)
baseline_rc=$?
[ "$baseline_rc" -eq 1 ] && ok "mutation setup: confirmed lane4 genuinely has no open task before mutating anything" \
  || bad "mutation setup baseline" "rc=$baseline_rc: $baseline_out"
mut_out=$(PYTHONPATH="$SUP" python3 "$MUT_CLI" --state-dir "$STATE" attach-pr-to-open-task --lane "$LANE4" --repo "$REPO" --pr 999 2>&1)
mut_rc=$?
[ "$mut_rc" -eq 0 ] && ok "mutation confirmed: removing the per-lane resolution check lets lane4 attach a PR anyway -- the real refusal above is load-bearing, not incidental" \
  || bad "mutation should have let lane4 through" "rc=$mut_rc: $mut_out"
tmux kill-session -t "$S4" 2>/dev/null

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
