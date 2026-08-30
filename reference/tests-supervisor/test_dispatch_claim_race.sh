#!/bin/bash
# agent-dotfiles#184: two dispatchers racing the SAME candidate lane must
# resolve to exactly one winner, never both. This file has the fixed shape
# (one wins, the other is refused loud), a RED-BEFORE-THE-FIX copy with no
# atomic claim at all reproducing the pre-fix race, and a mutation kill of
# the fix's own verify-read made non-fatal on mismatch.
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
export SUPERVISOR_MAX_AGENT_SESSIONS=0
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

echo "dispatch.sh -- two dispatchers racing the same candidate lane (agent-dotfiles#184)"

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

# --- agent-dotfiles#184: two dispatchers racing the SAME candidate lane ---
#
# Every case above dispatches once, in isolation -- the second dispatcher
# never exists. #184 is specifically about what happens when it does: two
# dispatchers can both read `lanes.sh --free` + `cli.py lane-free` and see
# the SAME lane free before either one finishes acting on it. A test that
# only calls dispatch.sh twice IN SEQUENCE proves nothing (the second call
# losing is what happens either way) -- this drives dispatcher A into the
# stub, splices a WHOLE second dispatch (dispatcher B, for a different
# issue, against the real unmodified dispatch.sh) in via a test-only hook
# right after A reads the lane free and before A can act on that read, and
# only then lets A continue. That is "dispatcher A reads availability, then
# B completes a whole dispatch, then A sends" -- #184's own required shape,
# not two calls back to back.
#
# DISPATCH_TEST_RACE_HOOK is dispatch.sh's only concession to this: a command
# run with the candidate lane as $1, at exactly the point a second dispatcher
# would need to land a competing dispatch to prove the race. No caller sets
# it outside this file.
cat > "$D/race-hook.sh" <<'HOOK'
#!/bin/bash
set -uo pipefail
# Self-disarming after the first firing (agent-supervisor#169): the hook
# runs once PER CANDIDATE LANE dispatcher A's loop tries, unconditionally --
# #184's own race never notices because it seeds exactly one free lane, so
# there is only ever one candidate and the loop ends the moment A's claim on
# it fails. A race that needs A and B to land on TWO DIFFERENT lanes (this
# one) seeds two free lanes, so A's loop tries a SECOND candidate after
# losing the first -- and without this guard, the hook fired dispatcher B a
# second, spurious time, which then raced against ITS OWN first (already
# `delivered`) dispatch instead of proving anything about A.
[ -e "$RACE_HOOK_FIRED" ] && exit 0
: > "$RACE_HOOK_FIRED"
env -u DISPATCH_TEST_RACE_HOOK bash "$RACE_DISPATCH" "$RACE_B_ISSUE" "$RACE_B_SLUG" \
  "$RACE_B_BRIEF" "$RACE_B_REPO_SLUG" "$RACE_B_REPO_PATH" ${RACE_B_EXTRA:-} > "$RACE_B_LOG" 2>&1
echo $? > "$RACE_B_RC"
HOOK
chmod +x "$D/race-hook.sh"

# Runs dispatcher A for issue $1 through dispatch script $2 (the real
# dispatch.sh, or a mutated copy), with dispatcher B (issue $3, ALWAYS the
# real, unmutated dispatch.sh -- the race is about what A does with a
# genuine competing dispatch, not about B's own correctness) spliced in via
# the hook. Leaves $RACE_RC_A/$RACE_OUT_A for A and $RACE_RC_B/$RACE_OUT_B
# for B, plus $RACE_LOG (the shared tmux log both dispatchers wrote to).
#
# Any trailing args ($4+) are forwarded to BOTH dispatcher A and dispatcher
# B -- agent-supervisor#169's own race (both dispatchers racing the SAME
# `--pr N`, not the #184 shape below of two dispatchers racing the same
# LANE for two different issues) needs this; every existing caller passes
# none, so this is purely additive.
run_race() {
  local issue_a="$1" script="$2" issue_b="$3"; shift 3
  local extra=("$@")
  local state="$D/state-race-$issue_a"
  printf '%s|| dispatcher A races for the only free lane\n%s|| dispatcher B wins the same race\n' \
    "$issue_a" "$issue_b" >> "$D/issues"
  : > "$D/race-b.out"; : > "$D/race-b.rc"; rm -f "$D/race-hook-fired"
  local out
  out=$(LEDGER_STATE="$state" \
        DISPATCH_TEST_RACE_HOOK="$D/race-hook.sh" \
        RACE_HOOK_FIRED="$D/race-hook-fired" \
        RACE_DISPATCH="$DISPATCH" RACE_B_ISSUE="$issue_b" RACE_B_SLUG="race-b-$issue_b" \
        RACE_B_BRIEF="$D/brief.md" RACE_B_REPO_SLUG=acme/agent-dotfiles \
        RACE_B_REPO_PATH="$REPO" RACE_B_LOG="$D/race-b.out" RACE_B_RC="$D/race-b.rc" \
        RACE_B_EXTRA="${extra[*]:-}" \
        DISPATCH_SCRIPT="$script" \
        run "$issue_a" "race-a-$issue_a" "$D/brief.md" acme/agent-dotfiles "$REPO" "${extra[@]}")
  RACE_RC_A=$?
  RACE_OUT_A="$out"
  RACE_OUT_B=$(cat "$D/race-b.out" 2>/dev/null)
  RACE_RC_B=$(cat "$D/race-b.rc" 2>/dev/null)
  RACE_LOG=$(tmuxlog)
}

# --- the fixed shape: exactly one dispatcher wins, the other is refused loud
run_race 501 "$DISPATCH" 502
want_exit "dispatcher B (spliced in mid-A's selection) completes its own dispatch" "$RACE_RC_B" 0 "$RACE_OUT_B"
want_contains "...and B's brief actually went out" "dispatch: #502 -> " "$RACE_OUT_B"
want_exit "dispatcher A is refused: B already won the only free lane" "$RACE_RC_A" 1 "$RACE_OUT_A"
want_contains "...and the refusal is LOUD, not silent" "no free lane" "$RACE_OUT_A"
cnt=$(grep -c "rename-window -t t:@103" <<<"$RACE_LOG")
if [ "$cnt" = 1 ]; then
  ok "only one brief reaches the shared lane t:3"
else
  bad "only one brief reaches the shared lane t:3" "rename-window -t t:@103 appeared $cnt times: $RACE_LOG"
fi
if [ "$(assignees 501)" = "" ]; then
  ok "A's issue is never claimed -- refused before claim.sh even runs"
else
  bad "A's issue is never claimed" "assignees: $(assignees 501)"
fi
want_contains "B's issue IS claimed" "jonhill90" "$(assignees 502)"

# --- RED BEFORE THE FIX: a copy with no atomic claim at all, the exact shape
# dispatch.sh had on origin/main before agent-dotfiles#184 -- `lane-free`'s
# read picked a candidate and nothing closed the gap before send-keys.
# agent-supervisor#716: both markers below now live in dispatch-lane-select.sh
# (the claim-lane block) and dispatch-send.sh (the commit guard) -- two
# different files. make_mutant_scripts_dir copies the whole directory and
# _dispatch_mutate.py's patch() finds whichever one carries each marker.
NO_CLAIM_DIR=$(make_mutant_scripts_dir)
NO_CLAIM_MUTANT="$NO_CLAIM_DIR/dispatch.sh"
patch_rc=0
PYTHONPATH="$HERE${PYTHONPATH:+:$PYTHONPATH}" python3 - "$NO_CLAIM_DIR" <<'PY' || patch_rc=$?
import sys
import _dispatch_mutate as M
target = sys.argv[1]
marker = '''  CLAIM_LANE="$candidate"
  CLAIM=$("$LEDGER_PYTHON" "$LEDGER_CLI" claim-lane --lane "$candidate" --token "$CLAIM_TOKEN" --owner-pid $$ 2>/dev/null) || { release_lane_claim; continue; }
  if grep -qF '"claimed":true' <<<"$CLAIM"; then
    LANE="$candidate"
    LANE_TARGET="$candidate_target"
    # agent-dotfiles#216: `lane-free` above already resolved this lane's
    # RECORDED harness (from its @hill90_lane_harness pane option, or the
    # ledger row if it was already known) -- carried forward to step 6 so
    # `record-dispatch` gets an explicit --harness instead of re-guessing one
    # from `#{pane_current_command}`, which cannot tell a Node harness like
    # copilot apart from any other. Empty is possible only if `lane-free`'s
    # own JSON shape ever changes underneath this grep; step 6's existing
    # fallback (HARNESS_BY_COMMAND) covers that, unchanged.
    LANE_HARNESS=$(grep -oE '"harness":"[a-z-]*"' <<<"$CHECK" | head -1 | sed -E 's/.*:"([a-z-]*)"/\\1/')
    break
  fi
  claim_reason=$(json_field reason "$CLAIM")
  claim_holder=$(json_field holder "$CLAIM")
  [ "$claim_holder" = null ] && claim_holder=""
  if [ -n "$claim_holder" ]; then
    append_exclusion "dispatch:   $candidate: claim refused ($claim_reason; holder $claim_holder)"
  else
    append_exclusion "dispatch:   $candidate: claim refused ($claim_reason; no holder reported; token '$CLAIM_TOKEN' may already exist)"
  fi
  # Lost this candidate to another dispatcher: move on, exactly as before.
  # The release is a no-op in that case (the row is the winner's, not ours)
  # and only bites when the claim committed but its result did not come back
  # readable -- which would otherwise leak a claim only the reap could clear.
  release_lane_claim'''
replacement = '''  LANE="$candidate"  # MUTATED: no atomic claim at all -- agent-dotfiles#184 pre-fix shape
  LANE_TARGET="$candidate_target"
  # #15: kept even in this mutant -- this test targets the atomic-claim race
  # specifically, and an unset LANE_HARNESS would instead fail dispatcher A
  # closed at step 3.5's harness-relaunch guard, reporting a defect this test
  # is not about.
  LANE_HARNESS=$(grep -oE '"harness":"[a-z-]*"' <<<"$CHECK" | head -1 | sed -E 's/.*:"([a-z-]*)"/\\1/')
  break'''
M.patch(target, marker, replacement)
# agent-dotfiles#209 round 2: also neutralise step 4.5's commit guard. It
# refuses to send when the claim it is asked to mark live does not exist --
# correct, and exactly what a mutant with no claim (or an ignored verify)
# produces. Left in, dispatcher A would be stopped by the COMMIT check
# rather than sail past the missing CLAIM check, and this case would report
# a race closed by the wrong guard. Same reason d2bce42 extended
# test_dispatch_ledger.sh's fallback mutation past the claim call.
commit_guard = 'if ! grep -qF \'"committed":true\' <<<"$COMMIT_OUT"; then'
M.patch(target, commit_guard, 'if false; then  # MUTATED: step 4.5 commit guard bypassed')
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh with no lane claim at all" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh with no lane claim at all"
  run_race 503 "$NO_CLAIM_MUTANT" 504
  cnt=$(grep -c "rename-window -t t:@103" <<<"$RACE_LOG")
  if [ "$RACE_RC_A" = 0 ] && [ "$cnt" -ge 2 ]; then
    ok "RED before the fix: with no atomic claim, BOTH dispatchers land a brief in lane t:3 (x$cnt) -- this is the race #184 reports"
  else
    bad "RED before the fix: with no atomic claim, BOTH dispatchers land a brief in lane t:3" \
      "expected A to also succeed (exit 0) and t:3 renamed >=2 times; rcA=$RACE_RC_A count=$cnt outA=$RACE_OUT_A"
  fi
fi

# --- MUTATION KILL: the fix's own verify-read, made non-fatal on mismatch --
# #184 names this explicitly: mutate the verify-read's mismatch to non-fatal
# and confirm the suite goes red, or the test proves nothing. This defeats
# dispatch.sh's OWN check of the claim result -- the bash-side half of
# claim-then-verify -- while leaving the ledger call itself untouched.
VERIFY_DEFEATED_DIR=$(make_mutant_scripts_dir)
VERIFY_DEFEATED_MUTANT="$VERIFY_DEFEATED_DIR/dispatch.sh"
patch_rc=0
PYTHONPATH="$HERE${PYTHONPATH:+:$PYTHONPATH}" python3 - "$VERIFY_DEFEATED_DIR" <<'PY' || patch_rc=$?
import sys
import _dispatch_mutate as M
target = sys.argv[1]
marker = 'if grep -qF \'"claimed":true\' <<<"$CLAIM"; then'
M.patch(target, marker, 'if true; then  # MUTATED: claim-lane verify-read mismatch made non-fatal')
# agent-dotfiles#209 round 2: also neutralise step 4.5's commit guard. It
# refuses to send when the claim it is asked to mark live does not exist --
# correct, and exactly what a mutant with no claim (or an ignored verify)
# produces. Left in, dispatcher A would be stopped by the COMMIT check
# rather than sail past the missing CLAIM check, and this case would report
# a race closed by the wrong guard. Same reason d2bce42 extended
# test_dispatch_ledger.sh's fallback mutation past the claim call.
commit_guard = 'if ! grep -qF \'"committed":true\' <<<"$COMMIT_OUT"; then'
M.patch(target, commit_guard, 'if false; then  # MUTATED: step 4.5 commit guard bypassed')
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh whose claim verify-read is non-fatal" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh whose claim verify-read is non-fatal"
  run_race 505 "$VERIFY_DEFEATED_MUTANT" 506
  cnt=$(grep -c "rename-window -t t:@103" <<<"$RACE_LOG")
  if [ "$RACE_RC_A" = 0 ] && [ "$cnt" -ge 2 ]; then
    ok "mutation confirmed: an ignored claim verify-read reopens the race (the assertions above would now be red)"
  else
    bad "mutation confirmed: an ignored claim verify-read reopens the race" \
      "expected A to also succeed (exit 0) and t:3 renamed >=2 times; rcA=$RACE_RC_A count=$cnt outA=$RACE_OUT_A"
  fi
fi

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
