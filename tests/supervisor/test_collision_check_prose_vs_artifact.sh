#!/bin/bash
# collision-check.sh -- agent-supervisor#617, defect B: prefer the artifact
# (a real diff) over prose (a backtick-quoted path in the brief) when BOTH
# are available for the same candidate; only fall back to prose when no
# artifact exists at all.
#
# THE MEASURED FALSE POSITIVE: a docs-only PR (#531, twelve `docs/**` paths
# changed) was credited with `scripts/supervisor/dispatch.sh` and
# `scripts/supervisor/mark-pr-external.sh` purely because the issue's own
# discussion quoted them as context -- rule 1 (`_files_named_in`) harvested
# those paths from prose and UNIONED them onto #531's real file set, even
# though #531's own diff never touched either. That inflated set then
# collided with an unrelated scripts-only PR/lane, and #531 was refused
# against work it never touched.
#
# Case 1 below reproduces exactly that shape: a candidate with a real
# artifact (a resumed branch -- rule 2) whose actual diff is disjoint from an
# in-flight lane's files, but whose BRIEF prose also quotes the in-flight
# lane's file as unrelated context. Before this fix, that collided; after,
# it must not.
#
# Case 2 is the "do not weaken the guard" direction (agent-supervisor#291,
# restated in this issue's own brief): a candidate whose artifact diff
# GENUINELY touches the same file an in-flight lane holds must still refuse
# -- the fix must only stop prose from being UNIONED onto a real diff, never
# stop a real diff's own overlap from being caught.
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
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

echo "collision-check.sh -- prose vs artifact (#617)"

D=$(mktemp -d)
git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/base" 2>/dev/null
REPO="$D/base"
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name Test
git -C "$REPO" checkout -q -b main
mkdir -p "$REPO/scripts/supervisor" "$REPO/docs"
echo original > "$REPO/scripts/supervisor/dispatch.sh"
echo original > "$REPO/scripts/supervisor/mark-pr-external.sh"
echo original > "$REPO/docs/guide.md"
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

# --- 1. GREEN: an artifact (resumed branch) that never touches the
#        in-flight lane's file must not be refused just because the BRIEF
#        prose mentions that file as context -- the #531 false positive. ----
STATE1=$(mktemp -d "$D/state1.XXXXXX")
LANE_A_WT="$D/laneA"
git -C "$REPO" worktree add -q -b lane/500-scripts-fix "$LANE_A_WT" main
echo "lane A's real fix" >> "$LANE_A_WT/scripts/supervisor/dispatch.sh"
register_lane "$STATE1" "as500-scripts-fix" "agent-supervisor:3" "$LANE_A_WT"

CAND_WT="$D/laneB"
git -C "$REPO" worktree add -q -b lane/531-docs "$CAND_WT" main
# The candidate's REAL diff (rule 2, an artifact) -- docs only.
echo "docs change" >> "$CAND_WT/docs/guide.md"
git -C "$CAND_WT" add -A
git -C "$CAND_WT" commit -q -m "docs update"
# The BRIEF's prose (rule 1) quotes the in-flight lane's file too, purely as
# context -- exactly what #531's own issue discussion did.
cat > "$D/brief-531.md" <<'EOF'
Update the docs. See also `scripts/supervisor/dispatch.sh` and
`scripts/supervisor/mark-pr-external.sh` for how the routing this documents
actually works -- not touched by this change, just background reading.
EOF

out1=$(AGENT_SUPERVISOR_STATE_DIR="$STATE1" DISPATCH_PYTHON=python3 \
  "$CHECK" check --issue 531 --brief "$D/brief-531.md" --worktree "$CAND_WT" \
  --repo-path "$REPO" --exclude-lane "agent-supervisor:4" 2>&1)
rc1=$?
want_exit "an artifact-disjoint candidate is allowed despite prose quoting the colliding file" "$rc1" 0 "$out1"
want_contains "...says no-conflict" "no-conflict" "$out1"
want_missing "...never names the prose-only file as a candidate file" "dispatch.sh" "$out1"
want_contains "...the real diff's own file is the one counted" "docs/guide.md" "$out1"

# --- 2. RED: do not weaken the guard -- an artifact whose diff GENUINELY
#        overlaps an in-flight lane's file must still refuse. ---------------
STATE2=$(mktemp -d "$D/state2.XXXXXX")
LANE_C_WT="$D/laneC"
git -C "$REPO" worktree add -q -b lane/501-scripts-fix "$LANE_C_WT" main
echo "lane C's real fix" >> "$LANE_C_WT/scripts/supervisor/mark-pr-external.sh"
register_lane "$STATE2" "as501-scripts-fix" "agent-supervisor:5" "$LANE_C_WT"

CAND_WT2="$D/laneD"
git -C "$REPO" worktree add -q -b lane/532-scripts "$CAND_WT2" main
# The candidate's REAL diff also touches mark-pr-external.sh -- a genuine
# overlap, measured from an actual diff on both sides, not prose.
echo "candidate's own change" >> "$CAND_WT2/scripts/supervisor/mark-pr-external.sh"
git -C "$CAND_WT2" add -A
git -C "$CAND_WT2" commit -q -m "also touches mark-pr-external.sh"
echo "unrelated brief text, no backtick paths at all" > "$D/brief-532.md"

out2=$(AGENT_SUPERVISOR_STATE_DIR="$STATE2" DISPATCH_PYTHON=python3 \
  "$CHECK" check --issue 532 --brief "$D/brief-532.md" --worktree "$CAND_WT2" \
  --repo-path "$REPO" --exclude-lane "agent-supervisor:6" 2>&1)
rc2=$?
want_exit "a genuine artifact-to-artifact overlap still refuses" "$rc2" 1 "$out2"
want_contains "...naming the colliding lane" "agent-supervisor:5" "$out2"
want_contains "...naming the colliding file" "mark-pr-external.sh" "$out2"

rm -rf "$D" "$GH_BIN"

echo
echo "collision-check.sh prose vs artifact: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
