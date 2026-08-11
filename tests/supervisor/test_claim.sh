#!/bin/bash
# claim.sh must make "has this work already been taken" answerable from GitHub.
#
# On 2026-08-11 issue #28 was dispatched twice -- once by the Director, once by
# the supervisor -- because `gh issue list` shows an open issue whether or not
# another lane picked it up ninety seconds ago. Both dispatchers were correct
# against the information available. Roughly an hour of lane work was spent
# twice (PRs #68 and #69, one merged, one closed).
#
# The load-bearing test is `a second dispatcher is refused`: the first claim
# must be visible to a process that shares nothing with it but GitHub.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAIM="$HERE/../../scripts/supervisor/claim.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_contains() { if grep -q -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "$3"; fi }
want_exit() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2"; fi }

D=$(mktemp -d); mkdir -p "$D/bin"
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cp "$HERE/stubs/tmux-lanes" "$D/bin/tmux"
# #154: lanes.sh asks ps what a shell pane's own process is before calling it
# dead. Stubbed here too, so this suite's fixture pids resolve to its own rows
# instead of to whatever the host happens to have running at that pid.
cp "$HERE/stubs/ps-lanes" "$D/bin/ps"

cat > "$D/issues" <<'FIX'
28|| Duplicate identities
70|| Nothing marks an issue as claimed
57|| Name the agent hierarchy
FIX
: > "$D/prs"

# lanes fixture: index|name|command|status-line|seconds-since-output|in-mode
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
2|ad70-claim-signal|claude.exe|esc to interrupt 3s|1|0
3|free-3|claude.exe|❯ ready|1|0
4|ad57-hierarchy|zsh|❯ |1|0
FIX

run() { PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
        LANES_FIXTURE="$D/lanes" LANES_SESSION=t bash "$CLAIM" "$@" 2>&1; }

echo "claim.sh"

# --- the duplicate-dispatch scenario, end to end -------------------------
out=$(run take 28 acme/repo lane-2); rc=$?
want_exit "the first dispatcher takes an unclaimed issue" "$rc" 0
want_contains "the claim says who holds it" "lane-2" "$out"

# A SECOND dispatcher, sharing no state with the first except GitHub.
out=$(run take 28 acme/repo lane-5); rc=$?
want_exit "a second dispatcher is refused a claimed issue" "$rc" 1
want_contains "the refusal names the holder" "jonhill90" "$out"

out=$(run check 28 acme/repo); rc=$?
want_exit "check reports a claimed issue as not claimable" "$rc" 1

# ...and an unclaimed issue is still offered. Skipping everything would also
# have prevented the duplicate, and would be a worse bug.
out=$(run check 70 acme/repo); rc=$?
want_exit "check still offers an unclaimed issue" "$rc" 0

out=$(run list acme/repo)
if grep -qx '70' <<<"$out"; then ok "list offers the unclaimed issue"; else bad "list offers the unclaimed issue" "$out"; fi
if grep -qx '28' <<<"$out"; then bad "list withholds the claimed issue" "$out"; else ok "list withholds the claimed issue"; fi

# --- release --------------------------------------------------------------
out=$(run release 28 acme/repo); rc=$?
want_exit "release drops the claim" "$rc" 0
out=$(run check 28 acme/repo); rc=$?
want_exit "a released issue is claimable again" "$rc" 0

# --- expiry ---------------------------------------------------------------
# A claim is live while the lane holding it is alive. `stale` must not report a
# claim whose lane is mid-turn -- that is exactly the hour of work the whole
# mechanism exists to protect.
run take 70 acme/repo lane-2 >/dev/null   # window 2 is ad70-*, busy
run take 57 acme/repo lane-4 >/dev/null   # window 4 is ad57-*, dead
run take 28 acme/repo lane-9 >/dev/null   # no window mentions 28 at all
out=$(run stale acme/repo)
if grep -qx '70' <<<"$out"; then bad "a claim held by a live lane is not stale" "$out"; else ok "a claim held by a live lane is not stale"; fi
if grep -qx '57' <<<"$out"; then ok "a claim held by a dead lane is stale"; else bad "a claim held by a dead lane is stale" "$out"; fi
if grep -qx '28' <<<"$out"; then ok "a claim with no lane at all is stale"; else bad "a claim with no lane at all is stale" "$out"; fi

# A lane finishes and is renamed free-N before its PR is reviewed. The work is
# real and must not be handed to a second lane; an open PR keeps the claim.
printf '99|Fixes #28\n' > "$D/prs"
out=$(run stale acme/repo)
if grep -qx '28' <<<"$out"; then bad "an open PR keeps the claim alive" "$out"; else ok "an open PR keeps the claim alive"; fi

# stale REPORTS; it never releases. Erring toward leaving a claim in place
# costs a tick of delay; erring the other way costs an hour of duplicated work.
out=$(run check 57 acme/repo); rc=$?
want_exit "stale does not release the claim itself" "$rc" 1

# --- stale must anchor to the leading <repo><issue>- prefix ---------------
# A window slug can contain digits that are not the dispatched issue number
# (a second issue number, a PR number, a date). `stale` must not treat every
# digit substring in the window name as a live issue -- that hides an
# unrelated claimed issue from `stale` forever, since nothing else re-checks
# it.
printf '99|| An unrelated issue\n' >> "$D/issues"
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
2|ad70-issue-99-fix|claude.exe|esc to interrupt 3s|1|0
FIX
: > "$D/prs"
run release 70 acme/repo >/dev/null 2>&1
run take 70 acme/repo lane-2 >/dev/null   # legit: window 2 is dispatched on 70
run take 99 acme/repo lane-9 >/dev/null   # no window actually names 99
out=$(run stale acme/repo)
if grep -qx '70' <<<"$out"; then
  bad "a window's own issue number keeps its claim live" "$out"
else
  ok "a window's own issue number keeps its claim live"
fi
if grep -qx '99' <<<"$out"; then
  ok "an unrelated digit in the window slug does not hide a stale claim"
else
  bad "an unrelated digit in the window slug does not hide a stale claim" "$out"
fi

# --- the optional [repo] argument must actually be optional ---------------
#
# `[repo] is OWNER/NAME; omitted, gh resolves it from the working directory` --
# claim.sh's own usage text. Omitting it aborted the script instead:
#
#   claim.sh: line 66: R[@]: unbound variable
#
# `set -u` plus `"${R[@]}"` on an EMPTY array is an unbound-variable error in
# bash 3.2, which is what macOS ships at /bin/bash and what the shebang selects.
# bash 5 expands an empty array to nothing and is fine, so this is invisible on
# any machine with a modern bash first in PATH -- and every one of these scripts
# runs under /bin/bash on Jon's Mac.
#
# Live consequence, 2026-08-11: `dispatch.sh 95 <slug> <brief>` with no repo
# argument reported `claim: could not assign #95` for an open, unclaimed issue,
# and the dispatch aborted. The failure was indistinguishable from a legitimate
# refusal.
# Deliberately /bin/bash and not `bash`: the `run` helper above uses whatever
# bash is first in PATH, which on this machine is Homebrew's 5.3 -- where an
# empty array expands to nothing and the bug cannot reproduce. The shebang on
# every one of these scripts selects /bin/bash, so that is what must be tested.
# Issue 57 is used because 28 and 70 are claimed by the tests above.
run_system_bash() {
  PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    LANES_FIXTURE="$D/lanes" LANES_SESSION=t /bin/bash "$CLAIM" "$@" 2>&1
}
out=$(run_system_bash check 57); rc=$?
# Assert on exit 2 specifically, not on 0. `check` answers 0 (claimable) or 1
# (claimed by someone) and both are real answers -- which one depends on what
# the tests above this line claimed, so pinning 0 makes this test depend on
# their order. Exit 2 is `cannot read issue`, which is the shape the bug
# produces: the script aborting before it ever reaches gh.
if [ "$rc" = "2" ]; then
  bad "check without [repo] does not abort on an empty array" "exit 2 (cannot read): $out"
else
  ok "check without [repo] does not abort on an empty array"
fi
if grep -q "unbound variable" <<<"$out"; then
  bad "check without [repo] does not emit an unbound-variable error" "$out"
else
  ok "check without [repo] does not emit an unbound-variable error"
fi

# --- a closed issue is not claimable (#95) ---------------------------------
# On 2026-08-11 `claim.sh take` succeeded against two issues closed nearly
# three hours earlier, and dispatch.sh sent lanes to already-finished work.
# `--add-assignee` has no opinion about issue state, so the refusal has to
# live in the read that happens before it.
printf '150||Closed nearly three hours ago|CLOSED\n' >> "$D/issues"
out=$(run take 150 acme/repo lane-9); rc=$?
if [ "$rc" -ne 0 ]; then ok "take refuses a closed issue with a non-zero exit"; else bad "take refuses a closed issue with a non-zero exit" "$out"; fi
want_contains "the refusal names the state" "not open" "$out"
a=$(awk -F'|' '$1==150{print $2}' "$D/issues")
if [ -z "$a" ]; then ok "a closed issue gets no assignee"; else bad "a closed issue gets no assignee" "$a"; fi

# The happy path this whole mechanism exists to protect: an OPEN issue with no
# assignee must still be claimable after the state check is added.
printf '151||Still open|OPEN\n' >> "$D/issues"
out=$(run take 151 acme/repo lane-9); rc=$?
want_exit "take still succeeds against an open issue" "$rc" 0
want_contains "the claim names the taker" "lane-9" "$out"

# --- gh failing outright is a refusal, not a proceed (#95) -----------------
# An issue number gh cannot resolve at all (network error, deleted issue,
# whatever) must not be read as "open, go ahead" -- an empty answer is not a
# yes.
out=$(run take 999999 acme/repo lane-9); rc=$?
if [ "$rc" -ne 0 ]; then ok "an unreadable issue state is refused, not proceeded"; else bad "an unreadable issue state is refused, not proceeded" "$out"; fi
a=$(awk -F'|' '$1==999999{print $2}' "$D/issues")
if [ -z "$a" ]; then ok "an unreadable issue gets no assignee"; else bad "an unreadable issue gets no assignee" "$a"; fi

# --- take with no [repo] argument must not die on the unguarded array (#95) -
# take's new state+assignees read (added by this fix) is a THIRD gh call site
# inside `take`, added after #96 converted the two pre-existing ones to the
# ${R[@]+"${R[@]}"} guard. dispatch.sh now always passes the repo slot, empty
# or not (#96), so `claim.sh take <issue> "" <lane>` is the real shape a
# no-repo dispatch produces. Run under /bin/bash specifically -- see the
# comment above the `check` version of this test for why `bash` (Homebrew 5.3
# here) cannot reproduce bash 3.2's unbound-variable-on-empty-array behaviour
# under `set -u`.
printf '152||No repo argument, open|OPEN\n' >> "$D/issues"
out=$(run_system_bash take 152 "" lane-9); rc=$?
if grep -q "unbound variable" <<<"$out"; then
  bad "take without [repo] does not emit an unbound-variable error" "$out"
else
  ok "take without [repo] does not emit an unbound-variable error"
fi
# Exit 2 is claim.sh's "cannot read" / "could not assign" shape -- the one an
# unbound-variable abort produces. 0 (claimed) is the real answer here since
# #152 is open and unclaimed.
if [ "$rc" = "2" ]; then
  bad "take without [repo] does not abort before reaching gh" "exit 2: $out"
else
  ok "take without [repo] does not abort before reaching gh"
fi
want_exit "take without [repo] still claims a genuinely open issue" "$rc" 0

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
