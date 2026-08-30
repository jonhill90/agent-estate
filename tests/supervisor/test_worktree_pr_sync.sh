#!/bin/bash
# agent-estate#886: a lane's local branch ref is never updated when a
# review fixup is pushed directly to the same PR's branch on GitHub --
# #885's reviewer found this by hand against PR #845 (`lane/840-fix840`):
# the local ref stayed at the commit BEFORE the fixup, so `worktree.sh
# gc`/`reap`'s own ancestry check reads a genuinely-landed branch as
# permanently "not ahead of origin/main". This file proves the sync point
# (`_gc_sync_pr_head` / `_gc_landed_with_sync` in worktree.sh) closes that
# gap, and that it changes nothing for a branch that never merged.
#
# The reproduction shape below mirrors the real PR #845 case exactly: a
# lane pushes commit C1, a REVIEWER (a separate clone, standing in for a
# fixup pushed straight to the PR branch on GitHub) pushes C2 on top of it
# directly to origin, main squash-merges the C1+C2 result, and origin's own
# `refs/heads/<branch>` is then deleted -- the real behavior this repo's
# own merge-pr.sh triggers, and the reason the sync fetches by PR NUMBER
# (`refs/pull/<n>/head`, which GitHub keeps addressable after branch
# deletion) rather than re-fetching the branch by name. The lane's own
# worktree is never told about C2 -- exactly what #885 found.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WT="$HERE/../../scripts/supervisor/worktree.sh"
STUBS="$HERE/stubs-pr-sync"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
require_dest() {
  if [ -z "${2:-}" ] || [ ! -d "${2:-}" ]; then
    echo "  FATAL $1: worktree.sh new gave no usable path ('${2:-}'); aborting rather than letting git -C '' run here" >&2
    exit 2
  fi
}

echo "worktree.sh -- agent-estate#886 PR head-sync point"

D=$(mktemp -d)

git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/repo"
REPO="$D/repo"
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name "Test"
git -C "$REPO" checkout -q -b main
echo one > "$REPO/file.txt"
git -C "$REPO" add file.txt
git -C "$REPO" commit -q -m "initial"
git -C "$REPO" push -q -u origin main
git -C "$REPO" remote set-head origin main >/dev/null 2>&1 || true

# `new`'s pre-push hook (agent-supervisor#562) refuses a push with no
# ledger dispatch record -- irrelevant to this file's own concern, same
# stub test_worktree.sh's own fixtures already use.
mkdir -p "$D/bin"
cat >"$D/bin/allow-python3" <<'STUB'
#!/bin/bash
echo '{"known":true,"lane":"stub:0","path":"stub","task":"stub"}'
STUB
chmod +x "$D/bin/allow-python3"
export AGENT_PYTHON_BIN="$D/bin/allow-python3"
export WORKTREE_ROOT="$REPO/.worktrees"

gh_env() { env PATH="$STUBS:$PATH" "$@"; }

# ============================================================================
# Case 1: a fixup pushed directly to the PR after the lane's own last push --
# the sync must pick it up and the branch must then read as landed.
# ============================================================================

lane_out=$(bash "$WT" new 886-sync "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "setup: new (case 1, lane's own worktree) exits 0" "$rc" 0 "$lane_out"
LANE_DEST="$lane_out"
require_dest "new (case 1)" "$LANE_DEST"

echo v1 > "$LANE_DEST/foo.txt"
git -C "$LANE_DEST" add foo.txt
git -C "$LANE_DEST" -c user.email=test@example.com -c user.name=Test commit -q -m "lane's own commit (C1)"
git -C "$LANE_DEST" push -q origin lane/886-sync
LANE_LOCAL_TIP=$(git -C "$LANE_DEST" rev-parse lane/886-sync)

# The reviewer fixup: a SEPARATE clone, standing in for a push made
# straight to the PR branch on GitHub, never touching the lane's own
# worktree -- exactly the gap #886 describes.
REVIEWER="$D/reviewer-clone"
git clone -q "$D/origin.git" "$REVIEWER"
git -C "$REVIEWER" checkout -q lane/886-sync
echo v2 > "$REVIEWER/foo.txt"
git -C "$REVIEWER" -c user.email=reviewer@example.com -c user.name=Reviewer commit -q -am "review fixup (C2)"
git -C "$REVIEWER" push -q origin lane/886-sync
FIXUP_TIP=$(git -C "$REVIEWER" rev-parse lane/886-sync)
if [ "$FIXUP_TIP" != "$LANE_LOCAL_TIP" ]; then ok "sanity: the fixup landed on origin's branch, not on the lane's own local ref"; else bad "sanity: the fixup landed on origin's branch, not on the lane's own local ref" "same tip $FIXUP_TIP"; fi

# GitHub itself keeps refs/pull/<n>/head in sync with a PR's actual current
# head, independent of the branch name -- reproduced directly rather than
# assumed. PR number is arbitrary (this is a local fixture, not a real
# GitHub PR) -- the stub gh below is what tells worktree.sh which number to
# use for branch 'lane/886-sync'.
PR_NUM=9001
git -C "$REVIEWER" push -q origin "refs/heads/lane/886-sync:refs/pull/$PR_NUM/head"

# Squash-merge the fixed-up branch into main, then delete origin's own
# branch ref -- this repo's own merge-pr.sh does exactly this
# (--delete-branch), and PR #845's real headRefName was gone by the time
# #885's reviewer looked. Proves the sync must work by PR NUMBER, not by
# re-fetching a branch name that may no longer exist.
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --squash origin/lane/886-sync
git -C "$REPO" commit -q -m "squashed: lane/886-sync"
git -C "$REPO" push -q origin main
git -C "$REVIEWER" push -q origin --delete lane/886-sync
if git -C "$REPO" ls-remote origin lane/886-sync 2>/dev/null | grep -q .; then bad "sanity: origin's own branch ref is gone after the simulated merge (proves the sync can't just re-fetch the branch name)" "still present"; else ok "sanity: origin's own branch ref is gone after the simulated merge (proves the sync can't just re-fetch the branch name)"; fi

# The lane's own worktree learns main has moved on (any lane would, at
# minimum, fetch base before checking its own status) but is NEVER told
# about the fixup -- this is the exact gap, reproduced, not assumed.
git -C "$LANE_DEST" fetch -q origin main
LANE_TIP_AFTER=$(git -C "$LANE_DEST" rev-parse lane/886-sync)
if [ "$LANE_TIP_AFTER" = "$LANE_LOCAL_TIP" ]; then ok "sanity: the lane's own local branch ref is still stale (never received C2)"; else bad "sanity: the lane's own local branch ref is still stale (never received C2)" "ref moved to $LANE_TIP_AFTER"; fi

sleep 2

# --- Baseline: no sync (--no-github) -- the gap, reproduced ---------------
base_out=$(WORKTREE_GC_MIN_AGE_SECONDS=0 bash "$WT" reap --dry-run --no-github "$LANE_DEST" origin/main 2>&1); base_rc=$?
want_exit "baseline (--no-github, no sync): reap refuses the landed-but-stale branch" "$base_rc" 1 "$base_out"
if grep -q "does not already contain branch" <<<"$base_out"; then ok "baseline correctly reproduces #885's finding -- the local ref reads as not-landed"; else bad "baseline correctly reproduces #885's finding -- the local ref reads as not-landed" "$base_out"; fi

# --- With the sync point: correctly resolves as landed ---------------------
sync_out=$(WORKTREE_GC_MIN_AGE_SECONDS=0 STUB_GH_PR_STATE_ROWS=$'lane/886-sync\tMERGED' STUB_GH_PR_NUM_ROWS=$'lane/886-sync\t'"$PR_NUM" gh_env bash "$WT" reap --dry-run "$LANE_DEST" origin/main 2>&1); sync_rc=$?
want_exit "with the sync point: reap now resolves the branch as landed" "$sync_rc" 0 "$sync_out"
if grep -q "synced in" <<<"$sync_out"; then ok "the offer names the sync as the reason, not the plain content check"; else bad "the offer names the sync as the reason, not the plain content check" "$sync_out"; fi
if [ -d "$LANE_DEST" ]; then ok "--dry-run changed nothing -- the worktree still exists"; else bad "--dry-run changed nothing -- the worktree still exists" "removed by a --dry-run call"; fi

# --- Read/fetch-only: refs/heads/<branch> itself was never touched --------
LANE_TIP_POST_SYNC=$(git -C "$LANE_DEST" rev-parse lane/886-sync)
if [ "$LANE_TIP_POST_SYNC" = "$LANE_LOCAL_TIP" ]; then ok "the sync never moved refs/heads/lane/886-sync itself (only a scratch ref)"; else bad "the sync never moved refs/heads/lane/886-sync itself (only a scratch ref)" "ref moved to $LANE_TIP_POST_SYNC"; fi
if git -C "$LANE_DEST" show-ref --verify --quiet refs/gc-pr-sync/lane/886-sync; then bad "the scratch sync ref is cleaned up after the check" "refs/gc-pr-sync/lane/886-sync still present"; else ok "the scratch sync ref is cleaned up after the check"; fi
if [ -n "$(git -C "$LANE_DEST" status --porcelain)" ]; then bad "the worktree's own working tree is still clean after the sync (never touched)" "$(git -C "$LANE_DEST" status --porcelain)"; else ok "the worktree's own working tree is still clean after the sync (never touched)"; fi

# --- The real (non-dry-run) reap now actually removes it -------------------
real_out=$(WORKTREE_GC_MIN_AGE_SECONDS=0 STUB_GH_PR_STATE_ROWS=$'lane/886-sync\tMERGED' STUB_GH_PR_NUM_ROWS=$'lane/886-sync\t'"$PR_NUM" gh_env bash "$WT" reap "$LANE_DEST" origin/main 2>&1); real_rc=$?
want_exit "with the sync point: a real (non-dry-run) reap succeeds" "$real_rc" 0 "$real_out"
if [ -d "$LANE_DEST" ]; then bad "the worktree is actually removed once the sync confirms it landed" "$LANE_DEST still present: $real_out"; else ok "the worktree is actually removed once the sync confirms it landed"; fi

# ============================================================================
# Case 2: a genuinely never-merged branch -- unaffected by the sync, in
# both directions (no PR on record at all, and a PR on record whose content
# still never reached main).
# ============================================================================

neverm_out=$(bash "$WT" new 886-neverm "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "setup: new (case 2, genuinely unmerged) exits 0" "$rc" 0 "$neverm_out"
NEVERM_DEST="$neverm_out"
require_dest "new (case 2)" "$NEVERM_DEST"
echo "never merged anywhere" > "$NEVERM_DEST/never.txt"
git -C "$NEVERM_DEST" add never.txt
git -C "$NEVERM_DEST" -c user.email=test@example.com -c user.name=Test commit -q -m "genuinely unmerged work"
git -C "$NEVERM_DEST" push -q origin lane/886-neverm
sleep 2

# 2a: no PR on record for this branch at all -- the sync must not even try.
noPR_out=$(WORKTREE_GC_MIN_AGE_SECONDS=0 STUB_GH_PR_STATE_ROWS=$'lane/999-unrelated\tMERGED' STUB_GH_PR_NUM_ROWS=$'lane/999-unrelated\t1' gh_env bash "$WT" reap --dry-run "$NEVERM_DEST" origin/main 2>&1); noPR_rc=$?
want_exit "case 2a: no PR on record for this branch -- reap still refuses" "$noPR_rc" 1 "$noPR_out"
if [ -d "$NEVERM_DEST" ]; then ok "case 2a: the genuinely unmerged worktree survives"; else bad "case 2a: the genuinely unmerged worktree survives" "removed: $noPR_out"; fi

# 2b: a PR number IS on record (an open PR, never merged) and its head is
# fetchable -- the sync fetch succeeds, but the content still never reached
# main, so the branch must still refuse. Point the fake PR ref at the
# branch's own (unmerged) tip -- exactly what an OPEN PR's own head looks
# like, no fixup at all.
NEVERM_PR_NUM=9002
NEVERM_TIP=$(git -C "$NEVERM_DEST" rev-parse lane/886-neverm)
git -C "$REPO" fetch -q origin lane/886-neverm
git -C "$REPO" push -q origin "$NEVERM_TIP:refs/pull/$NEVERM_PR_NUM/head"
withPR_out=$(WORKTREE_GC_MIN_AGE_SECONDS=0 STUB_GH_PR_STATE_ROWS=$'lane/886-neverm\tOPEN' STUB_GH_PR_NUM_ROWS=$'lane/886-neverm\t'"$NEVERM_PR_NUM" gh_env bash "$WT" reap --dry-run "$NEVERM_DEST" origin/main 2>&1); withPR_rc=$?
want_exit "case 2b: a fetchable, still-open PR -- reap still refuses (its content genuinely never landed)" "$withPR_rc" 1 "$withPR_out"
if [ -d "$NEVERM_DEST" ]; then ok "case 2b: the genuinely unmerged worktree survives even once the sync can fetch its PR"; else bad "case 2b: the genuinely unmerged worktree survives even once the sync can fetch its PR" "removed: $withPR_out"; fi
if git -C "$NEVERM_DEST" show-ref --verify --quiet refs/gc-pr-sync/lane/886-neverm; then bad "case 2b: the scratch sync ref is cleaned up even on a refusal" "refs/gc-pr-sync/lane/886-neverm still present"; else ok "case 2b: the scratch sync ref is cleaned up even on a refusal"; fi

# ============================================================================
# MUTATION, both directions -- prove the assertions above are pinned to the
# real sync logic, not something else already deciding the outcome.
# ============================================================================

# 3a: patch `_gc_landed_with_sync` to always say "landed" -> case 2a/2b's
# own refusal (still on disk, correctly preserved above) must go RED.
MUT_ALWAYS_LANDED="$D/worktree-mut-always-landed.sh"
mut_rc=0
python3 - "$WT" "$MUT_ALWAYS_LANDED" <<'PY' || mut_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '_gc_landed_with_sync() {\n  local repo="$1" b="$2" base="$3" pr_num_file="$4"\n'
replacement = marker + '  _GC_LANDED_WHY="mutant: always landed"\n  return 0\n'
assert text.count(marker) == 1, "_gc_landed_with_sync definition not unique -- script shape changed"
open(dst, "w").write(text.replace(marker, replacement, 1))
PY
if [ "$mut_rc" -ne 0 ]; then
  bad "setup: patched a copy of worktree.sh whose _gc_landed_with_sync always says 'landed'" "could not patch $WT (exit $mut_rc)"
else
  ok "setup: patched a copy of worktree.sh whose _gc_landed_with_sync always says 'landed'"
  chmod +x "$MUT_ALWAYS_LANDED"
  mut_out=$(WORKTREE_GC_MIN_AGE_SECONDS=0 STUB_GH_PR_STATE_ROWS=$'lane/999-unrelated\tMERGED' STUB_GH_PR_NUM_ROWS=$'lane/999-unrelated\t1' gh_env bash "$MUT_ALWAYS_LANDED" reap --dry-run "$NEVERM_DEST" origin/main 2>&1)
  if [ ! -d "$NEVERM_DEST" ] || grep -q "would reap" <<<"$mut_out"; then
    ok "mutation confirmed: an always-landed sync check would reap the genuinely unmerged worktree (case 2a's own refusal would now be RED)"
  else
    bad "mutation confirmed: an always-landed sync check would reap the genuinely unmerged worktree" "expected the mutant to offer removal, it still refused: $mut_out"
  fi
fi

# 3b: patch `_gc_sync_pr_head` to always FAIL (no sync ever available) ->
# case 1's own "landed once synced" assertion (real code, not a fixture
# artifact) must go RED, proving that verdict is pinned to the sync
# actually running, not to something else already reading it as landed.
LANE2_out=$(bash "$WT" new 886-sync2 "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "setup: new (case 3b, second sync-shaped worktree) exits 0" "$rc" 0 "$LANE2_out"
LANE2_DEST="$LANE2_out"
require_dest "new (case 3b)" "$LANE2_DEST"
echo v1 > "$LANE2_DEST/bar.txt"
git -C "$LANE2_DEST" add bar.txt
git -C "$LANE2_DEST" -c user.email=test@example.com -c user.name=Test commit -q -m "lane's own commit (C1, second fixture)"
git -C "$LANE2_DEST" push -q origin lane/886-sync2
REVIEWER2="$D/reviewer-clone2"
git clone -q "$D/origin.git" "$REVIEWER2"
git -C "$REVIEWER2" checkout -q lane/886-sync2
echo v2 > "$REVIEWER2/bar.txt"
git -C "$REVIEWER2" -c user.email=reviewer@example.com -c user.name=Reviewer commit -q -am "review fixup (C2, second fixture)"
git -C "$REVIEWER2" push -q origin lane/886-sync2
PR_NUM2=9003
git -C "$REVIEWER2" push -q origin "refs/heads/lane/886-sync2:refs/pull/$PR_NUM2/head"
git -C "$REPO" fetch -q origin
git -C "$REPO" merge -q --squash origin/lane/886-sync2
git -C "$REPO" commit -q -m "squashed: lane/886-sync2"
git -C "$REPO" push -q origin main
git -C "$REVIEWER2" push -q origin --delete lane/886-sync2
git -C "$LANE2_DEST" fetch -q origin main
sleep 2

MUT_NO_SYNC="$D/worktree-mut-no-sync.sh"
mut2_rc=0
python3 - "$WT" "$MUT_NO_SYNC" <<'PY' || mut2_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '_gc_sync_pr_head() {\n  local repo="$1" b="$2" pr_num_file="$3"\n'
replacement = marker + '  return 1\n'
assert text.count(marker) == 1, "_gc_sync_pr_head definition not unique -- script shape changed"
open(dst, "w").write(text.replace(marker, replacement, 1))
PY
if [ "$mut2_rc" -ne 0 ]; then
  bad "setup: patched a copy of worktree.sh whose _gc_sync_pr_head never syncs" "could not patch $WT (exit $mut2_rc)"
else
  ok "setup: patched a copy of worktree.sh whose _gc_sync_pr_head never syncs"
  chmod +x "$MUT_NO_SYNC"
  # Deliberately NOT naming lane/886-sync2 as MERGED in STUB_GH_PR_STATE_ROWS
  # here -- doing so would let the PRE-EXISTING #682 MERGED-PR fallback
  # reap it too, which would pass for the wrong reason and mask exactly
  # what this mutation is meant to isolate: whether the SYNC PATH
  # specifically is load-bearing for case 1's "landed once synced" verdict.
  mut2_out=$(WORKTREE_GC_MIN_AGE_SECONDS=0 STUB_GH_PR_STATE_ROWS=$'lane/999-unrelated\tMERGED' STUB_GH_PR_NUM_ROWS=$'lane/886-sync2\t'"$PR_NUM2" gh_env bash "$MUT_NO_SYNC" reap --dry-run "$LANE2_DEST" origin/main 2>&1)
  if grep -q "does not already contain branch" <<<"$mut2_out"; then
    ok "mutation confirmed: with the sync disabled, the landed-but-stale branch reads as not-landed again (case 1's own GREEN assertion would now be RED)"
  else
    bad "mutation confirmed: with the sync disabled, the landed-but-stale branch reads as not-landed again" "expected a refusal on the mutant, got: $mut2_out"
  fi
fi

echo "worktree.sh -- agent-estate#886: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
