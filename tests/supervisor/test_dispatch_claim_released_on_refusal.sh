#!/bin/bash
# agent-supervisor#647: "the claim is taken before the refusal paths run" --
# a claim with no lane behind it is indistinguishable, to every later
# dispatch, from an issue someone is actively working, so it silently stops
# being dispatchable.
#
# INVESTIGATION, not assumed: the issue's own reproduction (#622, two runs in
# a row) shows a collision refusal on dispatch-claude-print.sh leaving the
# issue claimed. Read against the code as it stands (aaaeb53, the same commit
# the issue itself cites for #644's host-pressure gate), every refusal path
# in dispatch.sh, dispatch-claude-print.sh and dispatch-pi-rpc.sh already
# either runs BEFORE claim.sh take (host-pressure, step 0; no-free-lane and
# missing-brief, both pre-claim in dispatch.sh) or releases the claim on
# refusal via abort()/abort_send() (collision, worktree failure) -- this was
# true from the commit that introduced the collision check itself (e3cc6cf,
# 2026-08-18), a full week before #647 was filed. Reproducing #622's exact
# scenario against this worktree (case 1 below) does NOT leak the claim.
#
# So this suite exists for two different reasons:
#
#   1. LOCK IN the already-correct behaviour with a mutation check, so a
#      future refactor cannot silently reintroduce the exact defect #647
#      describes without a test going red. Nothing before this suite
#      asserted on claim state after a refusal -- test_dispatch_force_
#      collision.sh exercises the same refusal but only checks the LANE
#      claim, never the ISSUE claim, so the leak #647 reports could have
#      reproduced with that whole suite green.
#
#   2. FIX the one real gap this investigation found: `claim.sh release`'s
#      own `gh api -X DELETE` call can fail (rate limit, network blip), and
#      until now dispatch-claude-print.sh's and dispatch-pi-rpc.sh's
#      release_claim() swallowed that failure with `>/dev/null 2>&1` and no
#      report -- unlike dispatch.sh's own release_claim, which has reported
#      this loudly since #209. A release that silently fails to land is the
#      one way the rollback code already in place could produce exactly the
#      symptom #647 describes without any ordering bug at all. Case 4 below
#      proves the report is now there, and that it was not before.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CORE_DIR="$HERE/../../scripts/supervisor"
DISPATCH="$CORE_DIR/dispatch.sh"
CLAUDE_PRINT="$CORE_DIR/dispatch-claude-print.sh"
export QUOTA_GATE="$HERE/stubs/quota-safe"
export SUPERVISOR_MAX_LOAD_PER_CORE=0
export SUPERVISOR_MIN_FREE_MEM_GB=0
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

echo "dispatch: a refused dispatch leaves the issue unclaimed (#647)"

D=$(mktemp -d); mkdir -p "$D/bin" "$D/roots"
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cp "$HERE/stubs/tmux-dispatch" "$D/bin/tmux"
cp "$HERE/stubs/claude" "$D/bin/claude"

git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/repo" 2>/dev/null
REPO="$D/repo"
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name "Test"
git -C "$REPO" checkout -q -b main
echo original > "$REPO/file.txt"
git -C "$REPO" add file.txt
git -C "$REPO" commit -q -m initial
git -C "$REPO" push -q -u origin main
git -C "$REPO" remote set-url origin "git@github.com:acme/agent-dotfiles.git"

: > "$D/prs"
one_claude_lane() {
  cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
}
printf 'Fix the double-counting in `file.txt`.\n' > "$D/brief.md"
assignees() { awk -F'|' -v n="$1" '$1==n{print $2}' "$D/issues"; }

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

run_dispatch() {
  local script="$1"; shift
  : > "$D/tmux.log"
  rm -rf "$D/panes"; mkdir -p "$D/panes"
  PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
    TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 \
    DISPATCH_RESPAWN_SETTLE=0 DISPATCH_LAUNCH_SETTLE=0 \
    DISPATCH_CONFIRM_TRIES=2 DISPATCH_SESSION_TIMEOUT=0 \
    AGENT_SUPERVISOR_STATE_DIR="$LEDGER_STATE" \
    STUB_PANE_PATH="$REPO" \
    WORKTREE_ROOT="$D/roots" bash "$script" "$@" 2>&1
}

# =============================================================================
# 1. claude-print (the DEFAULT transport since #171): a genuine collision --
#    the exact shape #622/#647 reports -- refuses and leaves the issue
#    unclaimed.
# =============================================================================
one_claude_lane
echo '910|| claude-print claim-release test' > "$D/issues"
LEDGER_STATE="$D/state-cp"; mkdir -p "$LEDGER_STATE"
INFLIGHT_CP="$D/inflight-cp"
git -C "$REPO" worktree add -q -b lane/900-inflight-cp "$INFLIGHT_CP" main
echo "in-flight change" >> "$INFLIGHT_CP/file.txt"
register_inflight_lane "$LEDGER_STATE" "as900-inflight-cp" "agent-supervisor:9" "$INFLIGHT_CP"

out_cp=$(run_dispatch "$DISPATCH" 910 cp-claim-test "$D/brief.md" acme/agent-dotfiles "$REPO"); rc_cp=$?
want_exit "claude-print: a genuine collision refuses" "$rc_cp" 1 "$out_cp"
if [ -z "$(assignees 910)" ]; then
  ok "...and #910 is unclaimed after the refusal (not stranded like #622 was)"
else
  bad "...and #910 is unclaimed after the refusal (not stranded like #622 was)" "still assigned: $(assignees 910)"
fi

# --- MUTATION: turn release_claim() itself into a no-op -- the exact
#     end-state #647 describes (a refusal that does not release the claim).
#     Removing only the explicit call inside abort() is NOT enough to prove
#     this: dispatch-claude-print.sh's own EXIT trap (release_claim_on_signal,
#     #572/#576) independently calls release_claim on every exit where
#     CLAIM_COMMITTED was never set -- which is true of every refusal before
#     step 6, collision included -- so that trap alone would still release
#     the claim and this test would pass for the wrong reason. Neutralizing
#     release_claim() itself disables both call sites at once, the same as a
#     regression that broke the underlying claim.sh call entirely. -------------
MUT_CP="$CORE_DIR/dispatch-claude-print-mutant-norelease.sh"
trap 'rm -f "$MUT_CP" "$CORE_DIR/dispatch-mutant-norelease.sh"' EXIT
python3 - "$CLAUDE_PRINT" "$MUT_CP" <<'PY'
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''release_claim() {
  "$HERE/claim.sh" release "$ISSUE" "$REPO" >/dev/null 2>&1 \\
    || echo "dispatch-claude-print: could not release the claim on #$ISSUE -- release it by hand: $HERE/claim.sh release $ISSUE $REPO" >&2
}'''
assert marker in text, "release_claim() shape changed -- update the mutation marker"
assert text.count(marker) == 1
mutated = "release_claim() { :; }  # neutralized by mutation test"
print("--- mutated line ---")
print("- " + '  "$HERE/claim.sh" release "$ISSUE" "$REPO" >/dev/null 2>&1 || echo ... (both call sites: abort() and the EXIT trap)')
print("+ " + mutated)
text = text.replace(marker, mutated, 1)
open(dst, "w").write(text)
PY
chmod +x "$MUT_CP"

one_claude_lane
echo '920|| claude-print claim-release mutation test' > "$D/issues"
LEDGER_STATE="$D/state-cp-mut"; mkdir -p "$LEDGER_STATE"
INFLIGHT_MUT="$D/inflight-cp-mut"
git -C "$REPO" worktree add -q -b lane/900-inflight-cp-mut "$INFLIGHT_MUT" main
echo "in-flight change" >> "$INFLIGHT_MUT/file.txt"
register_inflight_lane "$LEDGER_STATE" "as900-inflight-cp-mut" "agent-supervisor:9" "$INFLIGHT_MUT"

out_mut=$(run_dispatch "$MUT_CP" 920 cp-claim-mut "$D/brief.md" acme/agent-dotfiles "$REPO"); rc_mut=$?
want_exit "mutation setup: the same collision still refuses on the mutant" "$rc_mut" 1 "$out_mut"
if [ -n "$(assignees 920)" ]; then
  ok "mutation confirmed: with release_claim removed, #920 stays claimed after the refusal -- reproduces #647/#622 exactly"
else
  bad "mutation confirmed: with release_claim removed, #920 stays claimed after the refusal" "expected #920 to still be assigned, but assignees() reports empty"
fi

# =============================================================================
# 2. dispatch.sh's own tmux/live-pane flow: the same shaped collision, same
#    invariant.
# =============================================================================
one_claude_lane
echo '930|| live-pane claim-release test' > "$D/issues"
LEDGER_STATE="$D/state-live"; mkdir -p "$LEDGER_STATE"
INFLIGHT_LIVE="$D/inflight-live"
git -C "$REPO" worktree add -q -b lane/900-inflight-live "$INFLIGHT_LIVE" main
echo "in-flight change" >> "$INFLIGHT_LIVE/file.txt"
register_inflight_lane "$LEDGER_STATE" "as900-inflight-live" "agent-supervisor:11" "$INFLIGHT_LIVE"

out_live=$(run_dispatch "$DISPATCH" --live-pane 930 live-claim-test "$D/brief.md" acme/agent-dotfiles "$REPO"); rc_live=$?
want_exit "live-pane: the same shaped collision refuses" "$rc_live" 1 "$out_live"
if [ -z "$(assignees 930)" ]; then
  ok "...and #930 is unclaimed after the refusal"
else
  bad "...and #930 is unclaimed after the refusal" "still assigned: $(assignees 930)"
fi

# =============================================================================
# 3. a worktree.sh failure (branch already exists) leaves the issue unclaimed
#    too -- a different refusal path, same invariant.
# =============================================================================
one_claude_lane
echo '940|| worktree-failure claim-release test' > "$D/issues"
LEDGER_STATE="$D/state-wt"; mkdir -p "$LEDGER_STATE"
# A branch with a real commit not on main: worktree.sh's own "reclaim an
# abandoned branch" path (no worktree, no unique commits) does NOT apply --
# it refuses to delete unmerged work, so `worktree.sh new` genuinely fails
# here instead of silently reclaiming an empty stray branch.
WT_TMP="$D/wt-precreate"
git -C "$REPO" worktree add -q -b lane/940-wt-claim-test "$WT_TMP" main
echo "unmerged work" >> "$WT_TMP/file.txt"
git -C "$WT_TMP" add file.txt
git -C "$WT_TMP" commit -q -m "unmerged"
git -C "$REPO" worktree remove --force "$WT_TMP"
out_wt=$(run_dispatch "$DISPATCH" 940 wt-claim-test "$D/brief.md" acme/agent-dotfiles "$REPO"); rc_wt=$?
want_exit "a pre-existing branch makes worktree.sh new fail" "$rc_wt" 1 "$out_wt"
want_contains "...names worktree.sh as the reason" "worktree.sh new failed" "$out_wt"
if [ -z "$(assignees 940)" ]; then
  ok "...and #940 is unclaimed after the refusal"
else
  bad "...and #940 is unclaimed after the refusal" "still assigned: $(assignees 940)"
fi

# =============================================================================
# 4. claim.sh release itself failing is no longer silent (the one real gap
#    this investigation found). Stub gh refuses the DELETE for #950
#    specifically; before this fix, dispatch-claude-print.sh's release_claim
#    swallowed that with `>/dev/null 2>&1` and printed nothing.
# =============================================================================
cat > "$D/bin/gh" <<STUB
#!/bin/bash
if [ "\$1" = api ] && [ "\$2" = -X ] && [ "\$3" = DELETE ]; then
  for a in "\$@"; do case "\$a" in */issues/950/assignees) exit 1 ;; esac; done
fi
exec "$HERE/stubs/gh-claim" "\$@"
STUB
chmod +x "$D/bin/gh"

one_claude_lane
echo '950|| release-failure reporting test' > "$D/issues"
LEDGER_STATE="$D/state-relfail"; mkdir -p "$LEDGER_STATE"
INFLIGHT_RF="$D/inflight-relfail"
git -C "$REPO" worktree add -q -b lane/900-inflight-relfail "$INFLIGHT_RF" main
echo "in-flight change" >> "$INFLIGHT_RF/file.txt"
register_inflight_lane "$LEDGER_STATE" "as900-inflight-relfail" "agent-supervisor:9" "$INFLIGHT_RF"

out_relfail=$(run_dispatch "$CLAUDE_PRINT" 950 cp-relfail-test "$D/brief.md" acme/agent-dotfiles "$REPO"); rc_relfail=$?
want_exit "claude-print: the collision still refuses even though the release call will also fail" "$rc_relfail" 1 "$out_relfail"
want_contains "...and the failed release is reported, not swallowed" "could not release the claim on #950" "$out_relfail"

# --- MUTATION: revert release_claim to the pre-fix, unchecked form and
#     confirm the same scenario reports nothing -- proving the check above
#     is pinned to the new `|| echo ...` clause, not to something else.
python3 - "$CLAUDE_PRINT" "$CORE_DIR/dispatch-mutant-norelease.sh" <<'PY'
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''release_claim() {
  "$HERE/claim.sh" release "$ISSUE" "$REPO" >/dev/null 2>&1 \\
    || echo "dispatch-claude-print: could not release the claim on #$ISSUE -- release it by hand: $HERE/claim.sh release $ISSUE $REPO" >&2
}'''
assert marker in text, "release_claim shape changed -- update the mutation marker"
assert text.count(marker) == 1
mutated = 'release_claim() { "$HERE/claim.sh" release "$ISSUE" "$REPO" >/dev/null 2>&1; }'
print("--- mutated line ---")
print("- " + '    || echo "dispatch-claude-print: could not release the claim on #$ISSUE -- release it by hand: $HERE/claim.sh release $ISSUE $REPO" >&2')
print("+ (removed; release_claim reverts to swallowing the failure)")
text = text.replace(marker, mutated, 1)
open(dst, "w").write(text)
PY
chmod +x "$CORE_DIR/dispatch-mutant-norelease.sh"

one_claude_lane
echo '951|| release-failure reporting mutation test' > "$D/issues"
LEDGER_STATE="$D/state-relfail-mut"; mkdir -p "$LEDGER_STATE"
INFLIGHT_RF2="$D/inflight-relfail-mut"
git -C "$REPO" worktree add -q -b lane/900-inflight-relfail-mut "$INFLIGHT_RF2" main
echo "in-flight change" >> "$INFLIGHT_RF2/file.txt"
register_inflight_lane "$LEDGER_STATE" "as900-inflight-relfail-mut" "agent-supervisor:9" "$INFLIGHT_RF2"
sed -i.bak "s/950/951/" "$D/bin/gh" 2>/dev/null || sed -i '' "s/950/951/" "$D/bin/gh"
chmod +x "$D/bin/gh"

out_relfail_mut=$(run_dispatch "$CORE_DIR/dispatch-mutant-norelease.sh" 951 cp-relfail-mut "$D/brief.md" acme/agent-dotfiles "$REPO"); rc_relfail_mut=$?
want_exit "mutation setup: the same collision still refuses on the mutant" "$rc_relfail_mut" 1 "$out_relfail_mut"
want_missing "mutation confirmed: reverted release_claim swallows the DELETE failure silently again (the report above is load-bearing)" "could not release the claim on #951" "$out_relfail_mut"

rm -rf "$D"
rm -f "$MUT_CP" "$CORE_DIR/dispatch-mutant-norelease.sh"
trap - EXIT

echo
echo "dispatch claim-released-on-refusal: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
