#!/bin/bash
# agent-supervisor#572: a failed dispatch must not leave the ISSUE claim
# behind for nobody to release. ARM 1 (exercised directly): SIGTERM right
# after the issue claim, with a mutation check removing the issue-claim
# trap to prove the retry path is load-bearing. ARM 2 (exercised directly):
# a genuinely concurrent claim by a different lane is left alone, not
# stolen back.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DISPATCH="$HERE/../../scripts/supervisor/dispatch.sh"
# agent-supervisor#227: dispatch.sh now runs the quota gate before doing
# anything else. Every case in this file is testing something OTHER than the
# gate, so it needs a deterministic SAFE verdict, not the real quota.sh
# calling out to codexbar against whatever account state happens to be
# logged in on this machine. Exported so it covers every "$DISPATCH"
# invocation below, including the ones outside run() that build their own
# env block by hand. The dedicated gate tests live in
# test_dispatch_quota_gate.sh and override this per case.
export QUOTA_GATE="$HERE/stubs/quota-safe"
# agent-supervisor#500: dispatch.sh now runs a host-pressure gate before
# even the quota gate above -- same reasoning as #227's: every case in this
# file is testing something OTHER than the gate, and a CI runner's real
# load/free-memory at test time is neither controlled nor this suite's
# concern. 0 disables a check entirely (host-pressure.sh's own documented
# convention, mirroring daemon/internal/pressure.Limits). The dedicated
# gate tests live in test_host_pressure.sh (the gate's own logic, faked
# sysctl/vm_stat) and test_dispatch_host_pressure.sh (dispatch.sh's
# integration with it, mutation-checked both directions) and override these
# per case.
export SUPERVISOR_MAX_LOAD_PER_CORE=0
export SUPERVISOR_MIN_FREE_MEM_GB=0
# agent-supervisor#171: this suite is specifically about the tmux/send-keys
# flow (window naming, verified_type/verified_submit, the #241 id-vs-index
# split, ...) -- none of it stubs a `claude` binary, so leaving the new
# claude-print default on would route every plain single-issue claude case
# below at whatever REAL `claude` happens to be on this machine's PATH.
# `DISPATCH_LIVE_PANE=1` is `--live-pane` for every call this file makes;
# see dispatch.sh's own comment on `LIVE_PANE`'s initialization.
export DISPATCH_LIVE_PANE=1
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

echo "dispatch.sh -- a failed dispatch must not leave the issue claim stranded (agent-supervisor#572)"

D=$(mktemp -d); mkdir -p "$D/bin" "$D/roots"
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cp "$HERE/stubs/tmux-dispatch" "$D/bin/tmux"

# agent-supervisor#400: dispatch.sh now shells out to prior-attempts.sh
# before sending a brief, and prior-attempts.sh reads $SUPERVISOR_STATE/results
# by default -- unset, that is $HOME/.local/state/agent-dotfiles-supervisor on
# whatever machine runs this suite, the exact "test writes into the live
# estate" problem AGENT_SUPERVISOR_STATE_DIR exists to prevent below, for a
# different env var this PR added. A results dir that exists but holds one
# file matching no issue this suite uses makes prior-attempts.sh report
# "genuinely fresh" (rc=1, silent, brief untouched) for every case here,
# which is what every case here was already written to assume.
export SUPERVISOR_STATE="$D/pa-state"
mkdir -p "$SUPERVISOR_STATE/results"
: > "$SUPERVISOR_STATE/results/zzz-unrelated-to-any-test-issue.md"

# A minimal origin + clone, standing in for the shared checkout every lane
# would otherwise share.
git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/repo" 2>/dev/null
REPO="$D/repo"
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name "Test"
git -C "$REPO" checkout -q -b main
echo one > "$REPO/file.txt"
git -C "$REPO" add file.txt
git -C "$REPO" commit -q -m "initial"
git -C "$REPO" push -q -u origin main
# agent-supervisor#17: dispatch.sh now compares this origin against the
# [repo] argument every case below passes ("acme/agent-dotfiles") and refuses
# on mismatch, so the fixture's origin has to actually read as that repo --
# `git worktree add` shares its parent's remotes, so setting it once here
# covers every worktree any case below creates. `remote set-url` only edits
# config; nothing here fetches or pushes to it, so the URL never needs to
# resolve.
git -C "$REPO" remote set-url origin "git@github.com:acme/agent-dotfiles.git"

cat > "$D/issues" <<'FIX'
81|| worktree.sh has no automated caller
82|| Something else entirely
FIX
: > "$D/prs"
echo "do the thing" > "$D/brief.md"

# lanes fixture: index|name|command|status-line|seconds-since-output|in-mode
# Window 1 is the supervisor and is never offered; window 2 is mid-turn.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
2|ad82-other|claude.exe|esc to interrupt 3s|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX

# agent-dotfiles#174: dispatch.sh now READS the ledger to pick a lane, where
# before this suite was written it only ever WROTE to it (#140, nothing read
# it back). Every case in this file that dispatches to lane t:3 under the
# implicit default state dir used to be independent BY ACCIDENT -- nothing
# read the previous case's leftover ledger row, so it did not matter that
# they shared one. Now it would: a second dispatch to t:3 under a ledger that
# still shows the first one's task open would be correctly refused as
# occupied, breaking every case below that expects a second dispatch to
# succeed under the SAME implicit state dir. Each call gets an UNSHARED
# default state dir via `mktemp`, so every case that has not opted into a
# shared one via LEDGER_STATE keeps testing exactly what it tested before
# this landed -- one dispatch, in isolation. Cases that explicitly set
# LEDGER_STATE (to inspect what a specific dispatch recorded, or to force a
# broken ledger) are unaffected; they never relied on the implicit default.
#
# `mktemp -d`, not a counter: `run()` is almost always called as
# `out=$(run ...)`, and command substitution forks a SUBSHELL -- a counter
# variable incremented inside `run()` would increment only that subshell's
# copy and never advance in the parent, so every call would compute the same
# "next" value. `mktemp` needs no shared, persistent state to stay unique.
run() {
  : > "$D/tmux.log"
  rm -rf "$D/panes"; mkdir -p "$D/panes"
  # agent-dotfiles#216: same shape as DISPATCH_TEST_RACE_HOOK below -- test-only,
  # opt-in, no non-test caller sets it. Runs against the FRESH panes dir this
  # call just created, before dispatch.sh's own probe of it, so a case can
  # model a lane whose harness was already recorded (bootstrap-session.sh /
  # `cli.py register`) the way a real pre-existing lane would be, instead of
  # every dispatch starting from a pane with no options set at all.
  if [ -n "${RUN_PRESEED_PANES:-}" ]; then
    PATH="$D/bin:$PATH" LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_PANES="$D/panes" \
      bash -c "$RUN_PRESEED_PANES"
  fi
  # RUN_SESSION names the tmux SESSION this dispatch runs against, defaulting
  # to `t` so every existing case is untouched. agent-supervisor#108 needs two
  # dispatches against ONE ledger under two different session names -- that is
  # what a `tmux rename-session` looks like from the ledger's side, and it is
  # the only thing that changes between the two halves of that case.
  PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    LANES_FIXTURE="$D/lanes" LANES_SESSION="${RUN_SESSION:-t}" TMUX_LOG="$D/tmux.log" \
    TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 \
    DISPATCH_RESPAWN_SETTLE="${DISPATCH_RESPAWN_SETTLE:-0}" \
    DISPATCH_LAUNCH_SETTLE="${DISPATCH_LAUNCH_SETTLE:-0}" \
    DISPATCH_DROP_PREFIX="${DISPATCH_DROP_PREFIX:-0}" \
    DISPATCH_LANE="${DISPATCH_LANE:-}" \
    DISPATCH_PANE_ROWS="${DISPATCH_PANE_ROWS:-}" \
    DISPATCH_PANE_COLS="${DISPATCH_PANE_COLS:-60}" \
    DISPATCH_MESSAGE_BUDGET="${DISPATCH_MESSAGE_BUDGET:-430}" \
    AGENT_SUPERVISOR_STATE_DIR="${LEDGER_STATE:-$(mktemp -d "$D/state.XXXXXX")}" \
    STUB_PANE_PATH="${STUB_PANE_PATH:-$REPO}" \
    DISPATCH_SWALLOW_ENTER="${DISPATCH_SWALLOW_ENTER:-0}" \
    DISPATCH_SWALLOW_PRECLEAR_ENTER="${DISPATCH_SWALLOW_PRECLEAR_ENTER:-0}" \
    DISPATCH_SWALLOW_BRIEF_ENTER="${DISPATCH_SWALLOW_BRIEF_ENTER:-0}" \
    DISPATCH_LEAK_BEFORE_TYPE="${DISPATCH_LEAK_BEFORE_TYPE:-}" \
    DISPATCH_CONFIRM_TRIES="${DISPATCH_CONFIRM_TRIES:-2}" \
    DISPATCH_SESSION_TIMEOUT="${DISPATCH_SESSION_TIMEOUT:-0}" \
    WORKTREE_ROOT="$D/roots" bash "${DISPATCH_SCRIPT:-$DISPATCH}" "$@" 2>&1
}
# AGENT_SUPERVISOR_STATE_DIR is not optional in this harness. Without it the
# ledger record dispatch.sh now writes (#140) would land in the REAL supervisor
# state directory under $HOME -- a test suite writing into the live estate's
# ledger. LEDGER_STATE overrides it for the cases that need a broken one.
ledger() { AGENT_SUPERVISOR_STATE_DIR="${LEDGER_STATE:-$D/state}" python3 "$HERE/../../scripts/supervisor/cli.py" "$@"; }
# agent-supervisor#308 item 4: dispatch.sh's PR-contributor resolution chain
# now lives in resolve-pr-contributors.sh, sourced by `$HERE/...` (dispatch.sh's
# OWN directory, resolved from its BASH_SOURCE). A mutation check that used to
# patch a copy of dispatch.sh's inline text and rewrite its HERE assignment
# back to the real scripts/ dir (so every OTHER sourced file stayed real) can
# no longer isolate a mutation to the resolution chain that way -- the thing
# being mutated is itself one of the sourced files now. This makes a full,
# real copy of the whole scripts/supervisor/ directory instead: a mutation
# test overwrites exactly the file it needs to change (resolve-pr-
# contributors.sh, or dispatch.sh itself for a same-file mutation) in that
# copy, and every other file -- including the newly-split one when it is NOT
# the mutation target -- runs unmodified. `__pycache__` is skipped; a stale
# compiled module from a previous run must never be picked up over the
# source `.py` this copy just wrote.
make_mutant_scripts_dir() {
  local dir
  dir=$(mktemp -d "$D/mutant.XXXXXX")
  cp -R "$HERE/../../scripts/supervisor/." "$dir/"
  rm -rf "$dir/__pycache__"
  chmod +x "$dir"/*.sh
  printf '%s' "$dir"
}
# Registers a lane as known-and-free directly, the way `cli.py lane-free`'s
# first-sight backfill would if it ever saw this lane named `free-N` -- used
# where a case needs the ledger to ALREADY know a lane before dispatch.sh's
# own pane-identity probe is made to fail, so that probe's failure is
# isolated to the thing it is actually testing rather than also breaking the
# unrelated backfill probe dispatch.sh's lane-selection step makes first.
preregister_lane() {
  local state="$1" lane="$2" target="$3"
  AGENT_SUPERVISOR_STATE_DIR="$state" PATH="$D/bin:$PATH" \
    LANES_FIXTURE="$D/lanes" LANES_SESSION=t \
    STUB_PANE_PATH="${STUB_PANE_PATH:-$REPO}" \
    python3 "$HERE/../../scripts/supervisor/cli.py" register \
      --lane "$lane" --target "$target" --harness claude --repo "$REPO" >/dev/null
}
# Registers `lane` (free, no task) AND a task already under `task_id`,
# assigned to a DIFFERENT lane -- so a later `record-dispatch` call that
# tries to use `task_id` for `lane` collides deterministically at the
# application layer. Used to prove step 6's ledger write staying non-fatal
# without touching filesystem permissions -- a read-only sqlite file was
# tried first and dropped as non-deterministic across process boundaries
# (see the comment where this is used).
seed_conflicting_task() {
  local state="$1" lane="$2" task_id="$3" issue="$4"
  AGENT_SUPERVISOR_STATE_DIR="$state" python3 - "$HERE/../../scripts/supervisor" "$lane" "$task_id" "$issue" <<'PY'
import os
import sys
from pathlib import Path

sys.path.insert(0, sys.argv[1])
from core import Ledger

lane, task_id, issue = sys.argv[2], sys.argv[3], sys.argv[4]
ledger = Ledger(Path(os.environ["AGENT_SUPERVISOR_STATE_DIR"]))
ledger.register_lane(
    lane=lane, pane_id="%seed", nonce="seed-free", harness="claude", repo="/nonexistent",
    server_id="seed:1700000000", session_id="$0", command="claude.exe",
)
ledger.register_lane(
    lane="t:seed-other", pane_id="%seed-other", nonce="seed-other", harness="claude", repo="/nonexistent",
    server_id="seed:1700000000", session_id="$0", command="claude.exe",
)
ledger.reconstruct_task(
    task_id=task_id, source_kind="issue",
    source_url=f"https://github.com/acme/agent-dotfiles/issues/{issue}", source_ref=issue,
    summary="a different lane got here first", source_state="OPEN", status="created",
    evidence=["seeded by test_dispatch.sh"], status_marker=None,
)
ledger.assign(task_id=task_id, lane="t:seed-other", pane_nonce="seed-other", summary="a different lane got here first")
PY
}
# The ledger's own answer, asked directly: True (registered, free), False
# (registered, an outstanding task owns it), None (never registered). No tmux
# in the path, so this reports on ledger state and nothing else.
lane_available() {  # lane_available <state-dir> <lane>
  AGENT_SUPERVISOR_STATE_DIR="$1" python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
print(Ledger(sys.argv[2]).lane_available(sys.argv[3]))
' "$HERE/../../scripts/supervisor" "$1" "$2" 2>&1
}

tmuxlog()   { cat "$D/tmux.log"; }
assignees() { awk -F'|' -v n="$1" '$1==n{print $2}' "$D/issues"; }
worktrees() { ls "$D/roots" 2>/dev/null | wc -l | tr -d ' '; }
# agent-supervisor#373: worktree.sh new's `git worktree add -b` creates the
# branch BEFORE anything downstream can refuse -- `worktrees()` above only
# proves the WORKTREE is gone after a refusal; the BRANCH `worktree.sh done`
# never touches (see worktree.sh's own header) is a separate leak.
branch_exists() { git -C "$REPO" show-ref --verify --quiet "refs/heads/$1"; }

# --- agent-supervisor#572: a failed dispatch must not leave the ISSUE claim
# behind, poisoning every retry as "already claimed" -----------------------
#
# WHY THIS IS DIFFERENT FROM #209's LANE-CLAIM TESTS ABOVE: every failure
# dispatch.sh ENUMERATES already released the issue claim inline (`claim.sh
# release`, wired into CLAIM_FAILED, the worktree check and every abort_send
# call, long before this issue existed) -- so a busy-lane-style ordinary
# refusal was never the gap. #571's own measured sequence names the actual
# one: an issue claimed by a run that never reached one of those enumerated
# lines. release_lane_claim already had a signal trap for exactly that shape
# (#209); release_claim (this same claim, for the GitHub issue) did not, so a
# `kill`, a timeout wrapper or a closed terminal after step 2 stranded the
# assignee forever -- the next attempt, and every attempt after it, read
# "already claimed by jonhill90" with no way to tell that apart from someone
# else genuinely working it.
#
# The kill is delivered the same way #209's lane-claim tests deliver theirs
# (standing in for the thing that is called right after the commit, killing
# the dispatcher the instant it returns success) -- here that thing is
# claim.sh, not cli.py claim-lane, so the stand-in is a wrapped copy of
# claim.sh in a full mutant scripts/ directory, not a DISPATCH_PYTHON shim.
CLAIM_KILL_DIR=$(make_mutant_scripts_dir)
mv "$CLAIM_KILL_DIR/claim.sh" "$CLAIM_KILL_DIR/claim-real.sh"
cat > "$CLAIM_KILL_DIR/claim.sh" <<'WRAP'
#!/bin/bash
# Stands in for claim.sh in the #572 mutant scripts dir: runs the real
# claim.sh unchanged, and once a `take` has actually succeeded, signals its
# own parent -- dispatch.sh calls claim.sh directly (no subshell), so $PPID
# here IS the dispatcher, the same way `--owner-pid $$` names it for
# cli.py claim-lane above.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
out=$("$HERE/claim-real.sh" "$@" 2>&1); rc=$?
printf '%s\n' "$out"
if [ "${1:-}" = take ] && [ "$rc" -eq 0 ]; then
  kill "-${DISPATCH_TEST_KILL_SIGNAL:-KILL}" "$PPID" 2>/dev/null
  sleep 1
fi
exit $rc
WRAP
chmod +x "$CLAIM_KILL_DIR/claim.sh" "$CLAIM_KILL_DIR/claim-real.sh"

run_issue_claim_killed() {  # run_issue_claim_killed <state> <issue> <slug> <signal> <dispatch-script>
  local state="$1" issue="$2" slug="$3" signal="$4" script="$5"
  printf '%s|| a dispatcher signalled right after the ISSUE claim\n' "$issue" >> "$D/issues"
  ICLAIM_OUT=$(LEDGER_STATE="$state" DISPATCH_TEST_KILL_SIGNAL="$signal" DISPATCH_SCRIPT="$script" \
               run "$issue" "$slug" "$D/brief.md" acme/agent-dotfiles "$REPO")
  ICLAIM_RC=$?
}

# --- ARM 1 (exercised directly): SIGTERM right after the issue claim -------
# Trappable, so the new trap must release it AT ONCE -- no reap, no second
# dispatch needed to notice.
IC_TERM_STATE="$D/state-issue-claim-term"
run_issue_claim_killed "$IC_TERM_STATE" 801 issue-claim-term TERM "$CLAIM_KILL_DIR/dispatch.sh"
want_exit "a dispatcher SIGTERMed right after the ISSUE claim exits through its trap" "$ICLAIM_RC" 143 "$ICLAIM_OUT"
if [ -z "$(assignees 801)" ]; then
  ok "...and the TRAP released the GitHub claim immediately -- no assignee left behind"
else
  bad "...and the TRAP released the GitHub claim immediately" "still assigned: $(assignees 801)"
fi

# THE ARM THE BRIEF ASKS FOR: a genuine retry of the SAME issue, against a
# fresh dispatch (a fresh state dir, same as every other lane in this file
# reclaims between cases) -- this is exactly #571's attempt 4, minus the
# manual `gh issue edit --remove-assignee` that used to be the only way to
# get there.
IC_RETRY_STATE="$D/state-issue-claim-retry"
retry_out=$(LEDGER_STATE="$IC_RETRY_STATE" run 801 issue-claim-retry "$D/brief.md" acme/agent-dotfiles "$REPO"); retry_rc=$?
want_exit "...so a RETRY of #801 succeeds instead of refusing as already claimed" "$retry_rc" 0 "$retry_out"
want_missing "...no 'already claimed' mystery for the operator to chase" "already claimed" "$retry_out"

# --- MUTATION: remove the issue-claim trap, and the retry must go red -----
# Same discipline as #209's own mutation checks above: the assertion is only
# worth anything if deleting the fix actually breaks it. A SECOND full mutant
# directory (not a lone-file patch like #209's own mutation checks use) so
# `HERE` still resolves to a real, complete scripts/supervisor/ tree of its
# own -- including this test's kill-wrapper claim.sh, copied in unchanged.
NO_ICLAIM_TRAP_DIR=$(make_mutant_scripts_dir)
cp "$CLAIM_KILL_DIR/claim.sh" "$CLAIM_KILL_DIR/claim-real.sh" "$NO_ICLAIM_TRAP_DIR/"
chmod +x "$NO_ICLAIM_TRAP_DIR/claim.sh" "$NO_ICLAIM_TRAP_DIR/claim-real.sh"
# agent-supervisor#716: the traps now live in dispatch-lane-select.sh, not
# dispatch.sh's own text -- search the whole copied directory instead.
patch_rc=0
PYTHONPATH="$HERE" python3 - "$NO_ICLAIM_TRAP_DIR" <<'PY' || patch_rc=$?
import sys
import _dispatch_mutate as M
target = sys.argv[1]
marker = """trap 'release_claim_on_signal; release_lane_claim' EXIT
trap 'release_claim_on_signal; release_lane_claim; exit 143' TERM   # 128 + 15
trap 'release_claim_on_signal; release_lane_claim; exit 130' INT    # 128 + 2"""
M.patch(
    target,
    marker,
    "trap release_lane_claim EXIT\n"
    "trap 'release_lane_claim; exit 143' TERM   # 128 + 15  -- MUTATED: issue claim no longer trapped (#572)\n"
    "trap 'release_lane_claim; exit 130' INT    # 128 + 2",
)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh with no issue-claim trap" \
    "could not patch $NO_ICLAIM_TRAP_DIR/dispatch.sh (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh with no issue-claim trap"
  NO_TRAP_STATE="$D/state-no-iclaim-trap"
  run_issue_claim_killed "$NO_TRAP_STATE" 802 issue-claim-no-trap TERM "$NO_ICLAIM_TRAP_DIR/dispatch.sh"
  if [ -n "$(assignees 802)" ]; then
    ok "mutation confirmed: with no issue-claim trap, a SIGTERMed dispatcher strands the GitHub claim (the assertion above would now be red)"
  else
    bad "mutation confirmed: with no issue-claim trap, a SIGTERMed dispatcher strands the GitHub claim" \
      "expected #802 to still show an assignee, got none; rc=$ICLAIM_RC out=$ICLAIM_OUT"
  fi
fi

# --- ARM 2 (exercised directly): a genuinely concurrent claim is still
# refused -- the fix must not have traded #572's stall for a race ----------
# The other side of #209's own two-guard split (dispatch.sh's comment on
# CLAIM_LANE vs CLAIM_COMMITTED, above): nothing about gating release_claim
# on CLAIM_COMMITTED touches claim.sh's own "already claimed" read-then-write
# check, so an issue a DIFFERENT lane genuinely holds must still refuse here,
# exactly as it did before #572.
printf '803|someone-else| still being worked by another lane\n' >> "$D/issues"
concurrent_out=$(run 803 concurrent-claim "$D/brief.md" acme/agent-dotfiles "$REPO"); concurrent_rc=$?
want_exit "a dispatch against an issue another lane genuinely holds is still refused" "$concurrent_rc" 1 "$concurrent_out"
want_contains "...naming the real holder, same as before this fix" "someone-else" "$concurrent_out"
want_contains "...and that lane's claim is left completely alone" "someone-else" "$(assignees 803)"
want_missing "...no brief sent to any lane over a claim this dispatch does not hold" "send-keys" "$(cat "$D/tmux.log")"

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
