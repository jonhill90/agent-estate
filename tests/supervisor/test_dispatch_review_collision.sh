#!/bin/bash
# agent-supervisor#645: the pre-dispatch collision check (agent-supervisor#291)
# refuses a `--reviews-pr` dispatch when the PR's own author lane (or another
# in-flight review lane) still holds the files under review, treating a
# reader exactly like a writer.
#
# WHY THIS IS WRONG, MEASURED (the issue's own reproduction): a review lane's
# deliverable is a PR comment, not a commit -- it reads the files under
# review, it never writes them, so it is structurally incapable of the
# two-writers-one-file collision #291 exists to catch. The refusal fires on
# the NORMAL path for reviewing an in-flight PR, so the routine workaround
# becomes `--force`, which also suppresses the genuine writer-vs-writer
# collisions the check exists for.
#
# THE FIX PROVED HERE, both directions (a change that makes both pass has
# removed the guard, not scoped it):
#   1. a `--reviews-pr` dispatch overlapping an in-flight lane's files must
#      now DISPATCH (not refuse), whether the overlap is against the PR's
#      own author lane (case 2) or another, unrelated in-flight review lane
#      (case 3) -- a reader cannot collide with either kind of holder.
#   2. a plain (write) dispatch overlapping an in-flight lane must still
#      REFUSE (case 1) -- unaffected by this fix.
#   3. `--reviews-pr` combined with `--force` still dispatches (case 4) --
#      the pre-existing escape hatch is not the mechanism this fix relies on.
#   4. a plain (write) dispatch with NO `--reviews-pr` flag, whose issue
#      title happens to match the agent-supervisor#70 inference pattern,
#      must still REFUSE (case 5, agent-supervisor#650) -- the downgrade is
#      earned by the explicit flag, never by the inference alone.
# A mutation check (reverting the fix) confirms case 2/3 go back to refusing,
# proving the assertions above are actually exercising the fix and not
# passing by construction.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CORE_DIR="$HERE/../../scripts/supervisor"
DISPATCH="$CORE_DIR/dispatch.sh"
export QUOTA_GATE="$HERE/stubs/quota-safe"
export SUPERVISOR_MAX_LOAD_PER_CORE=0
export SUPERVISOR_MIN_FREE_MEM_GB=0
export DISPATCH_LIVE_PANE=1
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

echo "dispatch.sh -- a review dispatch does not trip the write-collision check (#645)"

D=$(mktemp -d); mkdir -p "$D/bin" "$D/roots"
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cp "$HERE/stubs/tmux-dispatch" "$D/bin/tmux"

git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/repo" 2>/dev/null
REPO="$D/repo"
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name "Test"
git -C "$REPO" checkout -q -b main
echo original > "$REPO/file.txt"
echo original > "$REPO/other.txt"
git -C "$REPO" add file.txt other.txt
git -C "$REPO" commit -q -m initial
git -C "$REPO" push -q -u origin main
git -C "$REPO" remote set-url origin "git@github.com:acme/agent-dotfiles.git"

lanes_two_free() {
  cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
}

run() {
  : > "$D/tmux.log"
  rm -rf "$D/panes"; mkdir -p "$D/panes"
  PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
    TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 \
    DISPATCH_RESPAWN_SETTLE=0 DISPATCH_LAUNCH_SETTLE=0 \
    DISPATCH_CONFIRM_TRIES=2 DISPATCH_SESSION_TIMEOUT=0 \
    AGENT_SUPERVISOR_STATE_DIR="$LEDGER_STATE" \
    STUB_PANE_PATH="$REPO" \
    WORKTREE_ROOT="$D/roots" bash "${DISPATCH_SCRIPT:-$DISPATCH}" "$@" 2>&1
}

tmuxlog() { cat "$D/tmux.log"; }

# Same primitives a real dispatch uses (register_lane -> reconstruct_task ->
# assign), the shape test_collision_check.sh's and
# test_dispatch_force_collision.sh's own fixtures already use -- so this only
# exercises shapes the real ledger API can produce.
register_inflight_lane() {
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

# --- shared fixture: PR #500's own authoring task, an in-flight lane still
#     holding it, and a review issue for it -----------------------------------
#
# resolve-pr-contributors.sh matches PR #500's authoring task by comparing
# its OWN worktree's checked-out branch against the PR's `headRefName` --
# `lane/300-authloop`, verbatim. Every case below that dispatches
# `--reviews-pr 500` needs the SAME branch name to resolve authorship at
# all, so one shared author worktree (checked out once, on that exact
# branch) is reused across every review case's own fresh ledger state --
# the ledger state is per-case, but the git worktree on disk is not.
echo '300|| authoring issue for PR 500' >> "$D/issues"
echo '302|| unrelated write issue' >> "$D/issues"
printf '500|Fixes #300|lane/300-authloop\n' >> "$D/prs"
echo '501|| review PR #500' >> "$D/issues"
# agent-supervisor#650's own reproduction, verbatim: #101's cited
# false-positive title -- matches the agent-supervisor#70 inference pattern
# ("review" + "PR #500") but is a rebase request, not a review. No
# `--reviews-pr` flag is passed for this case; only the title infers one.
echo '303|| rebase it so it can be reviewed, PR #500' >> "$D/issues"
printf 'Read the diff for PR #500 in `file.txt`.\n' > "$D/brief-review.md"
printf 'Fix the double-counting in `file.txt`.\n' > "$D/brief-write.md"
printf 'Read the diff for PR #500, and cross-check `other.txt`.\n' > "$D/brief-review-other.md"

AUTHOR_WT="$D/author-wt"
git -C "$REPO" worktree add -q -b lane/300-authloop "$AUTHOR_WT" main
echo "author's in-flight change" >> "$AUTHOR_WT/file.txt"
register_author() {  # register_author <ledger-state>
  register_inflight_lane "$1" "as300-authloop" "agent-supervisor:9" "$AUTHOR_WT"
}

# ============================================================================
# case 1 (baseline, unaffected): a PLAIN (write) dispatch overlapping an
# in-flight lane's files still refuses. Reuses the same in-flight author
# worktree/file as the review cases below -- the only thing that changes is
# whether the NEW dispatch is a review.
# ============================================================================
lanes_two_free
LEDGER_STATE="$D/state-write"; mkdir -p "$LEDGER_STATE"
register_author "$LEDGER_STATE"

out_write=$(LEDGER_STATE="$LEDGER_STATE" run 302 write-collides "$D/brief-write.md" acme/agent-dotfiles "$REPO"); rc_write=$?
want_exit "case 1: a plain (write) dispatch overlapping an in-flight lane still REFUSES" "$rc_write" 1 "$out_write"
want_contains "...naming the colliding file" "file.txt" "$out_write"

# ============================================================================
# case 2: a --reviews-pr dispatch overlapping the PR's OWN AUTHOR lane's
# files now DISPATCHES, and still prints the overlap as information.
# ============================================================================
lanes_two_free
LEDGER_STATE="$D/state-review-author"; mkdir -p "$LEDGER_STATE"
register_author "$LEDGER_STATE"

out_rev_author=$(LEDGER_STATE="$LEDGER_STATE" run 501 rev-500 "$D/brief-review.md" acme/agent-dotfiles "$REPO" --reviews-pr 500); rc_rev_author=$?
want_exit "case 2: --reviews-pr overlapping the PR's own author lane now DISPATCHES" "$rc_rev_author" 0 "$out_rev_author"
want_contains "...still names the overlapping file, as information" "file.txt" "$out_rev_author"
want_contains "...names the author's lane" "agent-supervisor:9" "$out_rev_author"
log_rev_author=$(tmuxlog)
want_contains "...and the brief was actually sent over tmux" "send-keys" "$log_rev_author"

# ============================================================================
# case 3: a --reviews-pr dispatch overlapping a DIFFERENT, unrelated in-flight
# REVIEW lane's files (not just the PR's own author) also DISPATCHES -- the
# fix is "review dispatches never write", not "this one PR's author is
# excused". The author lane is still registered here too (authorship must
# still resolve for the dispatch to reach the collision check at all); the
# assertion below is on the SECOND, unrelated lane's file.
# ============================================================================
lanes_two_free
LEDGER_STATE="$D/state-review-other-review"; mkdir -p "$LEDGER_STATE"
register_author "$LEDGER_STATE"
OTHER_REVIEW_WT="$D/other-review-wt"
git -C "$REPO" worktree add -q -b lane/600-other-review "$OTHER_REVIEW_WT" main
echo "another review lane's in-flight (uncommitted) change" >> "$OTHER_REVIEW_WT/other.txt"
register_inflight_lane "$LEDGER_STATE" "as600-other-review" "agent-supervisor:11" "$OTHER_REVIEW_WT"

out_rev_other=$(LEDGER_STATE="$LEDGER_STATE" run 501 rev-500-b "$D/brief-review-other.md" acme/agent-dotfiles "$REPO" --reviews-pr 500); rc_rev_other=$?
want_exit "case 3: --reviews-pr overlapping ANOTHER review lane's files also DISPATCHES" "$rc_rev_other" 0 "$out_rev_other"
want_contains "...still names the overlapping file, as information" "other.txt" "$out_rev_other"

# ============================================================================
# case 4: --reviews-pr combined with --force still dispatches (the
# pre-existing escape hatch keeps working; this fix is not it).
# ============================================================================
lanes_two_free
LEDGER_STATE="$D/state-review-force"; mkdir -p "$LEDGER_STATE"
register_author "$LEDGER_STATE"

out_rev_force=$(LEDGER_STATE="$LEDGER_STATE" run --force 501 rev-500-c "$D/brief-review.md" acme/agent-dotfiles "$REPO" --reviews-pr 500); rc_rev_force=$?
want_exit "case 4: --reviews-pr with --force also dispatches" "$rc_rev_force" 0 "$out_rev_force"

# ============================================================================
# case 5 (agent-supervisor#650's own reproduction): a plain WRITE dispatch,
# NO `--reviews-pr` flag, whose issue title happens to match the
# agent-supervisor#70 inference pattern ("review" + "PR #500") must still
# REFUSE. The downgrade in case 2/3 is earned by an operator's EXPLICIT
# `--reviews-pr`, never by the inference alone -- keying it on the inferred
# value silently removed the writer-vs-writer collision guard for a
# dispatch that really was going to write (the exact bypass #650 reported,
# reproduced with #101's own cited false-positive title).
# ============================================================================
lanes_two_free
LEDGER_STATE="$D/state-write-infer"; mkdir -p "$LEDGER_STATE"
register_author "$LEDGER_STATE"

out_write_infer=$(LEDGER_STATE="$LEDGER_STATE" run 303 write-infer-collides "$D/brief-write.md" acme/agent-dotfiles "$REPO"); rc_write_infer=$?
want_exit "case 5: a write dispatch with an inference-matching title, no --reviews-pr flag, still REFUSES" "$rc_write_infer" 1 "$out_write_infer"
want_contains "...naming the colliding file" "file.txt" "$out_write_infer"

# ============================================================================
# MUTATION CHECK: revert the fix (drop the REVIEWS_PR downgrade, restore the
# unconditional abort_send) and confirm case 2's exact scenario goes back to
# refusing -- proving the assertions above are exercising the fix, not
# passing because this fixture can never collide.
# ============================================================================
MUT_DIR=$(mktemp -d "$D/mutant.XXXXXX")
cp -R "$CORE_DIR/." "$MUT_DIR/"
rm -rf "$MUT_DIR/__pycache__"
chmod +x "$MUT_DIR"/*.sh
# agent-supervisor#716: the REVIEWS_PR downgrade branch now lives in
# dispatch-worktree.sh, not dispatch.sh's own text -- $MUT_DIR already holds
# the whole copied directory (above), so search it for whichever file
# carries the needle instead of assuming dispatch.sh.
python3 - "$MUT_DIR" <<'PY'
import glob
import os
import re
import sys
target_dir = sys.argv[1]
needle = '''if [ "$COLLISION_RC" -ne 0 ]; then
  if [ -n "$REVIEWS_PR_EXPLICIT" ]; then'''
candidates = sorted(glob.glob(os.path.join(target_dir, "dispatch*.sh")))
hits = [f for f in candidates if needle in open(f).read()]
assert len(hits) == 1, (
    "the REVIEWS_PR downgrade branch is not found in exactly one file -- "
    "update the mutation marker: %r" % hits
)
path = hits[0]
text = open(path).read()
# Replace the whole if/else block with the pre-fix unconditional abort_send,
# byte-for-byte what dispatch.sh had before agent-supervisor#645.
pattern = re.compile(
    r'if \[ "\$COLLISION_RC" -ne 0 \]; then\n'
    r'.*?\nfi\n',
    re.DOTALL,
)
original = '''if [ "$COLLISION_RC" -ne 0 ]; then
  # A refusal, same as every other guard's stderr above -- agent-dotfiles#199
  # only requires SILENCE on a SUCCESSFUL dispatch; this one is not one.
  sed 's/^/dispatch: collision-check: /' <<<"$COLLISION_OUT" >&2
  abort_send "#$ISSUE_ARG's files collide with an in-flight lane -- NOT dispatched. Re-run with --force if this overlap is known and intended (agent-supervisor#291)"
fi
'''
new_text, n = pattern.subn(original, text, count=1)
assert n == 1, "mutation regex did not match exactly once"
open(path, "w").write(new_text)
PY
bash -n "$MUT_DIR/dispatch.sh" || bad "setup: mutant dispatch.sh is still valid bash" "bash -n failed"

lanes_two_free
LEDGER_STATE="$D/state-review-mutant"; mkdir -p "$LEDGER_STATE"
register_author "$LEDGER_STATE"

out_mut=$(LEDGER_STATE="$LEDGER_STATE" DISPATCH_SCRIPT="$MUT_DIR/dispatch.sh" run 501 rev-500-mut "$D/brief-review.md" acme/agent-dotfiles "$REPO" --reviews-pr 500); rc_mut=$?
want_exit "mutation confirmed: with the REVIEWS_PR downgrade reverted, the SAME review dispatch refuses again (the fix above is load-bearing)" "$rc_mut" 1 "$out_mut"

rm -rf "$D"

echo
echo "dispatch.sh review-collision (#645): $pass passed, $fail failed"
[ "$fail" -eq 0 ]
