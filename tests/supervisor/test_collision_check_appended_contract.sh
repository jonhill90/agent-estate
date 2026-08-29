#!/bin/bash
# collision-check.sh -- the appended deliverable contract must not itself
# manufacture a false collision (agent-estate#840).
#
# THE FAILURE THIS PREVENTS, MEASURED: `dispatch-claude-print.sh`,
# `dispatch-pi-rpc.sh` and `dispatch-send.sh` each append a standing
# deliverable contract to every brief they deliver, and that boilerplate
# names the dispatcher's OWN filename ("Added by `dispatch-claude-print.sh`
# on every dispatch..."). A review dispatch never has a diff yet (the
# reviewer has no branch), so collision-check.sh's prose signal is always
# what fires -- and the appended contract guaranteed the dispatcher's own
# filename was in it. Two real #836 review dispatches were refused this way
# while an unrelated lane merely edited dispatch-claude-print.sh; the
# "overlap" was never real.
#
# THE FIX: _files_named_in stops reading prose at the
# `<!-- dispatch:deliverable-contract -->` marker every dispatcher writes
# immediately before its appended contract. Case 1 below is the false
# positive this stops. Case 2 is the true positive that must still fire: a
# brief that names a file in its OWN body (above the marker) still collides.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$HERE/../../scripts/supervisor/collision-check.sh"
CORE_DIR="$HERE/../../scripts/supervisor"
pass=0; fail=0

GH_BIN=$(mktemp -d)
ln -s "$HERE/stubs/gh-collision-check" "$GH_BIN/gh"
export PATH="$GH_BIN:$PATH"
export GH_STUB_PR_LIST_JSON="[]"

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }

echo "collision-check.sh -- appended deliverable contract exclusion"

D=$(mktemp -d)
git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/base" 2>/dev/null
REPO="$D/base"
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name Test
git -C "$REPO" checkout -q -b main
mkdir -p "$REPO/scripts/supervisor"
echo original > "$REPO/scripts/supervisor/dispatch-claude-print.sh"
echo original > "$REPO/scripts/supervisor/other.sh"
git -C "$REPO" add -A
git -C "$REPO" commit -q -m initial
git -C "$REPO" push -q -u origin main

register_lane() {
  local state="$1" task="$2" lane="$3" worktree="$4"
  PYTHONPATH="$CORE_DIR" python3 -c "
import core
l = core.Ledger('$state')
l.register_lane(lane='$lane', pane_id='%1', nonce='n1', harness='claude',
  repo='$REPO', server_id='s1', session_id='sess1', command='claude')
l.reconstruct_task(task_id='$task', source_kind='issue', source_url='https://x',
  source_ref='1', summary='test', source_state='OPEN', status='created',
  evidence=['test'], status_marker=None)
l.assign(task_id='$task', lane='$lane', pane_nonce='n1', summary='test', worktree_path='$worktree')
"
}

# An in-flight lane with an uncommitted edit to dispatch-claude-print.sh --
# the file every dispatcher's own appended contract names in its boilerplate.
STATE=$(mktemp -d "$D/state.XXXXXX")
LANE_A_WT="$D/laneA"
git -C "$REPO" worktree add -q -b lane/838-fix "$LANE_A_WT" main
echo "lane A's fix" >> "$LANE_A_WT/scripts/supervisor/dispatch-claude-print.sh"
register_lane "$STATE" "ae838-fix838" "agent-supervisor:3" "$LANE_A_WT"

CAND_WT="$D/laneB"
git -C "$REPO" worktree add -q -b lane/836-review "$CAND_WT" main

# --- 1. GREEN: the ONLY mention of dispatch-claude-print.sh is in the
#        appended contract -- must no longer collide -------------------------
cat > "$D/brief-836-review.md" <<'EOF'
# Review PR #836

Read the diff, leave a Verdict comment. This brief has nothing to do with
dispatch-claude-print.sh in its own body.

<!-- dispatch:deliverable-contract -->
## Delivering this work

Added by `dispatch-claude-print.sh` on every dispatch, not by the brief's author.

**Your lane id is `agent-supervisor:5`.**
EOF

out1=$(AGENT_SUPERVISOR_STATE_DIR="$STATE" DISPATCH_PYTHON=python3 \
  "$CHECK" check --issue 836 --brief "$D/brief-836-review.md" --worktree "$CAND_WT" \
  --repo-path "$REPO" --exclude-lane "agent-supervisor:5" 2>&1)
rc1=$?
want_exit "a brief whose only mention is in the appended contract is allowed" "$rc1" 0 "$out1"
want_contains "...says allow / no signal found" "ALLOW unknown" "$out1"

# --- 2. RED: the brief's OWN body (above the marker) names the same file --
#        must still collide -- prose detection is intact -------------------
cat > "$D/brief-838-fix.md" <<'EOF'
# Fix #838

Fix `scripts/supervisor/dispatch-claude-print.sh` -- it reroutes the wrong
lane shape.

<!-- dispatch:deliverable-contract -->
## Delivering this work

Added by `dispatch-claude-print.sh` on every dispatch, not by the brief's author.

**Your lane id is `agent-supervisor:6`.**
EOF

out2=$(AGENT_SUPERVISOR_STATE_DIR="$STATE" DISPATCH_PYTHON=python3 \
  "$CHECK" check --issue 838 --brief "$D/brief-838-fix.md" --worktree "$CAND_WT" \
  --repo-path "$REPO" --exclude-lane "agent-supervisor:6" 2>&1)
rc2=$?
want_exit "a brief that names the file in its own body still collides" "$rc2" 1 "$out2"
want_contains "...naming the colliding lane" "agent-supervisor:3" "$out2"
want_contains "...naming the colliding file" "scripts/supervisor/dispatch-claude-print.sh" "$out2"

rm -rf "$D" "$GH_BIN"

echo
echo "test_collision_check_appended_contract.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
