#!/bin/bash
# Guard parity across the three dispatchers -- agent-supervisor#590.
#
# WHY: three fixes (#589 signal trap, #243 stray-branch cleanup, #647 a
# swallowed claim-release failure) landed in dispatch.sh and sat unreached in
# its two siblings for as long as four months, found only when a symptom was
# chased into them. #645/#650 was the same shape from the other direction: a
# fix landed in dispatch.sh that dispatch-claude-print.sh could not even
# receive the same way, because the concept (REVIEWS_PR) does not exist in
# that file at all. Nothing asserted any of this -- each fix was written
# against the file where the incident surfaced, and the sibling was
# remembered only if the author happened to grep.
#
# WHAT THIS IS: an explicit inventory of named guards, each one asserted
# present (by a source-level marker -- a function name, a flag, a call into a
# shared helper) in every dispatcher that should carry it, with a per-file
# exemption stating WHY wherever it should not. A naive line-diff is useless
# here on purpose -- dispatch.sh is 2600+ lines, dispatch-pi-rpc.sh is under
# 250, and they legitimately differ in size and structure; presence of a
# named guard is the thing that generalises, not line count.
#
# "REQUIRED" means: this file's own supported entry path can hit the failure
# this guard exists to prevent, so the guard's marker must be present.
# "EXEMPT" means: it cannot, or the guard's concept does not apply here, and
# says so -- a bare absence is exactly what let #589/#243/#647 sit
# unnoticed. Two kinds of exemption reason appear below, and the difference
# matters: "by-design" is permanent (the file has no tmux pane, no
# --reviews-pr flag, is never routed to by default); "gap:#590" is a REAL,
# measured, PRE-EXISTING divergence this suite is not fixing -- #590's own
# brief is explicit that flagged divergences get reported, not silently
# fixed in the same PR, and the check must not be tuned until they vanish.
# Closing a gap:#590 exemption means adding the guard to that file, in its
# own change, then deleting the exemption line here -- not editing the
# pattern this file checks for.
#
# MUTATION-CHECKED BOTH WAYS (see the PR this test shipped in for the
# transcript): commenting out dispatch-claude-print.sh's `trap
# release_claim_on_signal EXIT` line makes this suite fail, naming the file
# and the guard; restoring the line makes it pass again. A check that cannot
# be shown to fail is the failure mode #590 itself named.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; fail=$((fail+1)); }

DISPATCHERS=(dispatch.sh dispatch-claude-print.sh dispatch-pi-rpc.sh)
for f in "${DISPATCHERS[@]}"; do
  [ -f "$SUP/$f" ] || { echo "FATAL: $SUP/$f does not exist -- this suite's own file list is stale" >&2; exit 2; }
done

declare -A STATUS   # STATUS["$guard|$file"] = "REQUIRED" or "EXEMPT:<reason>"
declare -A PATTERN  # PATTERN["$guard"] = extended regex, grep -Ec against the file
GUARDS=()

guard() {
  # guard <id> <pattern> <dispatch.sh status> <dispatch-claude-print.sh status> <dispatch-pi-rpc.sh status>
  local id="$1" pattern="$2"
  GUARDS+=("$id")
  PATTERN["$id"]="$pattern"
  STATUS["$id|dispatch.sh"]="$3"
  STATUS["$id|dispatch-claude-print.sh"]="$4"
  STATUS["$id|dispatch-pi-rpc.sh"]="$5"
}

R="REQUIRED"

# 1. #589/#572 -- the issue claim taken in step 1 must not be strandable by a
# signal landing before this shell reaches its own release call. All three
# files take the same claim.sh claim; only pi-rpc has never had a trap that
# releases it, and #590 measured this as a real, unaddressed gap.
guard signal-trap-issue-claim 'trap .*release_claim_on_signal' \
  "$R" "$R" \
  "EXEMPT:gap:#590 -- pi-rpc takes the same claim.sh claim as its siblings (step 1) and has never had a signal trap protecting it; this is the #589 gap, not fixed in this PR."

# 2. #647/#655 -- a failed claim.sh release (its own gh api -X DELETE call)
# must be reported, never swallowed -- all three dispatchers already do this.
guard release-claim-failure-reported 'could not release the claim' \
  "$R" "$R" "$R"

# 3. #243 -- the stray branch a `worktree.sh new` created must be removed on
# an aborted dispatch, or it is left behind for every failure past that
# point. dispatch.sh has cleanup_dispatch_branch(); neither sibling does,
# though both create the exact same lane/<issue>-<slug> branch and both have
# abort paths reachable after it exists (register/reconstruct-task/assign
# failures in either sibling all call abort() after the worktree step).
guard stray-branch-cleanup-on-abort 'branch -D' \
  "$R" \
  "EXEMPT:gap:#590 -- dispatch-claude-print.sh creates the same lane/<issue>-<slug> branch via worktree.sh new and can abort after it exists (register/reconstruct-task/assign failures below step 2 all call abort()); this is the #243 gap, not fixed in this PR." \
  "EXEMPT:gap:#590 -- same as dispatch-claude-print.sh: same branch shape, same abort-after-creation exposure; the #243 gap, not fixed in this PR."

# 4. #643 -- host-pressure gate before an agent process is started. All three
# already carry one (dispatch.sh via host-pressure.sh, the two print/rpc
# transports via host_pressure.py) -- this is here to keep it that way.
guard host-pressure-gate '[Hh]ost.pressure' \
  "$R" "$R" "$R"

# 5. #227 -- quota.sh's WIND-DOWN/UNKNOWN gate. Present only in dispatch.sh.
# Mitigating, per #590's own issue text: dispatch.sh's delegation to
# dispatch-claude-print.sh (:1471) happens well after its own quota gate
# (:470), so a dispatch that ENTERS through dispatch.sh is gated even though
# the transport that carries it out is not. A direct invocation of either
# sibling -- the same shape #643's host-pressure gap was found in before it
# was closed the same way -- bypasses it entirely. Real, narrower than the
# raw absence suggests, not fixed here.
guard quota-gate 'quota\.sh|quota gate' \
  "$R" \
  "EXEMPT:gap:#590 -- reached ungated only when invoked directly, not via dispatch.sh's own delegation (which already ran quota.sh at :470 before reaching :1471); same shape #643 closed for host-pressure. Not fixed in this PR." \
  "EXEMPT:gap:#590 -- dispatch-pi-rpc.sh is never reached via dispatch.sh at all (no caller in this tree beyond its own tests), so it has no quota gate on any path. Same class of gap as above. Not fixed in this PR."

# 6. #291 -- the pre-dispatch collision check itself (collision-check.sh),
# guarding two writers from touching the same files. dispatch-pi-rpc.sh has
# never had one -- but per #171/#215 it is not routed to by default and has
# never dispatched a live lane (0 of 310 recorded), so this is recorded here
# as a decision, not a gap: nothing currently reaches this script the way it
# reaches its siblings. If that changes -- pi-rpc becomes a routed default --
# this exemption must be revisited alongside guard #7 below.
guard collision-check-present 'collision-check\.sh' \
  "$R" "$R" \
  "EXEMPT:by-design (agent-supervisor#171/#215) -- pi-rpc is not routed to by default and has never dispatched a live lane (0/310); no collision to guard against on any path this script is actually reached by."

# 7. #645/#650 -- the collision refusal downgrades to informational ONLY for
# an EXPLICIT --reviews-pr dispatch (REVIEWS_PR_EXPLICIT), never for the
# wider #70 inference -- a write dispatch must still be refused. Both
# siblings lack this because neither accepts --reviews-pr at all: dispatch.sh
# only delegates to dispatch-claude-print.sh when REVIEWS_PR and PR are both
# empty (dispatch.sh :1538-1539), and dispatch-pi-rpc.sh's usage comment
# never documents the flag either. There is no review-scoped case either
# script can reach for this guard to scope.
guard collision-downgrade-scoped-to-writes 'REVIEWS_PR_EXPLICIT' \
  "$R" \
  "EXEMPT:by-design -- dispatch-claude-print.sh never accepts --reviews-pr (dispatch.sh only delegates here when REVIEWS_PR and PR are both empty, :1538-1539); the write-vs-review distinction this guard encodes has no review-scoped case to apply to, so the unconditional refusal is correct for every case this script can reach." \
  "EXEMPT:by-design -- same as dispatch-claude-print.sh (no --reviews-pr support), and also has no collision check at all (guard #6), so there is nothing here to scope."

# 8. the worktree-occupancy check dispatch.sh's abort_send() runs before
# removing a worktree -- refuses cleanup while the lane's own tmux pane is
# still inside it. #590's own issue thread walked both abort paths and found
# this ABSENT BY DESIGN in both siblings: neither has a tmux pane at all
# (ClaudePrintAdapter.observe_lane and PiRPCAdapter.observe_lane are both
# permanent no-ops -- "there is no pane to poll"), so there is nothing for
# `tmux display-message -p pane_current_path` to check. Importing a tmux call
# into either script would be the wrong fix, not a parity gap.
guard worktree-occupancy-check-before-cleanup 'pane_current_path' \
  "$R" \
  "EXEMPT:by-design -- confirmed in #590's own issue thread: a print-mode lane has no tmux pane (ClaudePrintAdapter.observe_lane is a permanent no-op), so there is nothing for pane_current_path to check." \
  "EXEMPT:by-design -- same reasoning as dispatch-claude-print.sh: PiRPCAdapter.observe_lane is likewise a permanent no-op; no pane exists to check."

echo "dispatcher guard parity (agent-supervisor#590)"
for id in "${GUARDS[@]}"; do
  pat="${PATTERN[$id]}"
  for f in "${DISPATCHERS[@]}"; do
    st="${STATUS[$id|$f]:-}"
    if [ -z "$st" ]; then
      bad "$id / $f -- no status recorded (neither REQUIRED nor EXEMPT); the inventory itself is incomplete"
      continue
    fi
    count=$(grep -Ec "$pat" "$SUP/$f")
    case "$st" in
      REQUIRED)
        if [ "$count" -gt 0 ]; then
          ok "$id present in $f (required)"
        else
          bad "$id MISSING from $f (required, no exemption on file) -- pattern '$pat' matched 0 lines in $SUP/$f"
        fi
        ;;
      EXEMPT:*)
        reason="${st#EXEMPT:}"
        if [ "$count" -gt 0 ]; then
          ok "$id present in $f (exempted but harmless: $reason)"
        else
          ok "$id absent from $f, exempted: $reason"
        fi
        ;;
      *)
        bad "$id / $f -- unrecognised status '$st'"
        ;;
    esac
  done
done

echo
echo "pass=$pass fail=$fail"
[ "$fail" -eq 0 ]
