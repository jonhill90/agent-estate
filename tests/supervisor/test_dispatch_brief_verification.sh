#!/bin/bash
# A typed brief is not a delivered one: this file covers the
# verified-start check (#141) and its ledger-availability signal, the
# pre-clear step's own verification (#193), a refusal AFTER the worktree
# already exists not leaving a worktree behind either (#373), the input box
# gaining text after verified_preclear cleared it (#240), and dispatch.sh
# staying silent-clean on stderr under bash 3.2 (agent-dotfiles#199).
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

echo "dispatch.sh -- the brief must actually START, not merely be typed (#141)"

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

# --- the brief must actually START, not merely be typed (#141) -------------
# Two lanes sat for 40 minutes each holding a full brief that was typed in and
# never submitted: `/clear` takes longer to repaint than the dispatcher waits,
# so the Enter that followed was swallowed. dispatch.sh printed
# `dispatch: #N -> lane` and walked away, claim.sh showed the issue claimed,
# and the work was invisible -- not queued, not running, not lost.
#
# DISPATCH_SWALLOW_BRIEF_ENTER models exactly that: the keys arrive, the box
# keeps the text, nothing runs. NOT DISPATCH_SWALLOW_ENTER (agent-
# supervisor#193): that swallows every Enter unconditionally, including the
# pre-clear's own -- which, now that the pre-clear is itself verified (see
# the NEW case just below this one), would abort the dispatch a full step
# earlier than this case means to exercise, before the claim ever reaches
# its point of no return. DISPATCH_SWALLOW_BRIEF_ENTER lets the pre-clear
# succeed normally and swallows only the BRIEF's later submit -- #141's
# original shape, isolated from #193's.
echo '160|| a dispatch whose Enter is swallowed' >> "$D/issues"
# Successful dispatches earlier in this file leave their worktrees in place,
# so the assertion is that this one ADDS none -- not that none exist.
before=$(worktrees)
out=$(LEDGER_STATE="$D/state-160" DISPATCH_SWALLOW_BRIEF_ENTER=1 \
      run 160 unsent-brief "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a brief that never submits fails the dispatch" "$rc" 1 "$out"
want_contains "and says it was typed but never submitted" "never submitted" "$out"
want_missing "and does not print a success line" "dispatch: #160 -> " "$out"
# Unwound like every other refusal: the issue goes back to the pool rather
# than looking claimed-and-running while nothing runs.
if [ -z "$(assignees 160)" ]; then ok "the claim is released when the brief never starts"
else bad "the claim is released when the brief never starts" "still assigned: $(assignees 160)"; fi
if [ "$(worktrees)" = "$before" ]; then ok "no worktree is left behind by an unsent brief"
else bad "no worktree is left behind by an unsent brief" "$before before, $(worktrees) after"; fi
# The ordering #140's own comment demands, now that there is an abort BELOW
# the final Enter: the ledger records "work is in flight" and this dispatch
# has none. The #141 confirmation therefore runs BEFORE the ledger write, not
# after it, or every swallowed Enter would leave a record asserting a lane is
# working on an issue it was never given.
status=$(LEDGER_STATE="$D/state-160" ledger status 2>&1)
# The exact id `record-dispatch --task "$WINDOW_NAME"` would write, which is
# distinct from the claim placeholder's `ledger-claim:<lane>:<token>` id even
# though both end in the window name.
want_missing "an unsent brief records no DISPATCH task in the ledger" '"id":"ad160-unsent-brief"' "$status"
# ...but the LANE CLAIM stays, and this assertion is the cost of
# agent-dotfiles#209 round 2 written down rather than discovered later.
#
# It used to read `"tasks":[]`. The Enter that this case swallows is sent
# AFTER step 4.5 marks the claim live, so by the time step 5 concludes the box
# never emptied, this dispatcher has already crossed its own point of no
# return -- and the whole of round 2 is that nothing may free a lane from the
# far side of that line. `input_box_state` saying `text` is strong evidence
# nothing was submitted, but it is still evidence read out of pane content,
# which is the instrument #102, #123 and #126 were all filed against; trading
# a bounded capacity loss for the chance of handing out a working lane is the
# trade this subsystem exists to refuse.
#
# So a swallowed Enter now costs a HELD lane needing one manual command, where
# before it cost nothing. That is a real regression in ergonomics for a
# failure that has been observed live (#141, twice on 2026-08-11), accepted in
# exchange for the guarantee. It is not silent: `lanes.sh` reports the lane
# `unsent`, and the abort prints the exact command below.
want_contains "but the lane claim REMAINS -- past the commit point, nothing may free a lane" \
  '"id":"ledger-claim:t:3:ad160-unsent-brief"' "$status"
want_contains "...and the claim is marked live, not merely reserved" '"status":"delivered"' "$status"
want_contains "...so the ledger reads that lane occupied" "False" "$(lane_available "$D/state-160" t:3)"
want_contains "...and the abort says the lane stays held rather than leaving it to be discovered" \
  "STAYS HELD" "$out"
want_contains "...and names record-completion --lane when the lane finished but never signalled" \
  "record-completion --lane t:3" "$out"
want_contains "...and still names cancel-open-task only for cancelling non-work" \
  "cancel-open-task --lane t:3" "$out"

# The other direction, and the one that keeps the check honest: a dispatch
# that DOES submit must pass silently. A confirmation that fires on every
# dispatch is the same as no confirmation.
echo '161|| a dispatch that submits normally' >> "$D/issues"
out=$(run 161 submits-fine "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a brief that submits still exits 0" "$rc" 0 "$out"
want_contains "and reports the dispatch" "dispatch: #161 -> " "$out"
want_missing "and warns about nothing" "WARNING" "$out"

# --- agent-supervisor#193: the pre-clear itself must be verified -----------
# `at25-rev33`'s actual shape: `/clear`'s OWN Enter never submitted, and the
# retyped brief landed glued onto the unsubmitted "/clear" -- a corruption
# every proof-token check downstream still read as `landed`, because the
# check had no notion of position (fixed separately -- see test_send.sh's
# `--proof-head` coverage). The fix HERE is upstream of all of that: confirm
# the screen actually blanked before anything else is ever typed, and abort
# if it did not, rather than type over an unsubmitted "/clear".
#
# DISPATCH_SWALLOW_PRECLEAR_ENTER swallows ONLY the pre-clear's own Enter
# (a buffer holding EXACTLY "/clear"), every attempt -- modelling a
# persistent failure so the abort direction is unambiguous. Unlike #160
# above, this must abort BEFORE the claim's point of no return: nothing has
# been typed into the pane yet, so nothing here is "in flight".
echo '162|| a dispatch whose /clear never submits' >> "$D/issues"
before=$(worktrees)
out=$(LEDGER_STATE="$D/state-193-preclear" DISPATCH_SWALLOW_PRECLEAR_ENTER=1 \
      run 162 unclearable "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a /clear that never submits fails the dispatch" "$rc" 1 "$out"
want_contains "and says the screen never blanked" "did not blank" "$out"
want_missing "and does not print a success line" "dispatch: #162 -> " "$out"
log=$(tmuxlog)
want_missing "the brief itself is never typed -- the pre-clear never got that far" "$D/brief.md" "$log"
if [ "$(worktrees)" = "$before" ]; then ok "no worktree is left behind by an unclearable pre-clear"
else bad "no worktree is left behind by an unclearable pre-clear" "$before before, $(worktrees) after"; fi
if [ -z "$(assignees 162)" ]; then ok "the claim is released -- nothing was ever typed into the pane"
else bad "the claim is released -- nothing was ever typed into the pane" "still assigned: $(assignees 162)"; fi
status=$(LEDGER_STATE="$D/state-193-preclear" ledger status 2>&1)
want_contains "...and the ledger agrees the lane is free again" "True" "$(lane_available "$D/state-193-preclear" t:3)"
want_missing "and records no DISPATCH task in the ledger either" '"id":"ad162-unclearable"' "$status"

# --- agent-supervisor#373: a refusal AFTER the worktree exists must not
# leave the branch `worktree.sh new` created behind. Reproduced with a real
# collision-check.sh REFUSE (agent-supervisor#291's own guard, step 3.2,
# unconditional -- runs on every dispatch, not just reviews): an in-flight
# lane is seeded directly in the ledger this dispatch will read, with an
# uncommitted edit to `file.txt` in its own worktree; the candidate's brief
# names that same file in backticks, collision-check.sh's own convention for
# citing a file (see its `_files_named_in`). `abort_send` (dispatch.sh) calls
# `worktree.sh done "$WORKTREE"`, which only ever removes the WORKTREE --
# never the BRANCH `git worktree add -b` created (worktree.sh's own header) --
# so before the fix `lane/373-collides` survives the refusal and a retry of
# the same issue/slug hits "fatal: a branch named ... already exists", #373's
# own live reproduction.
echo '373|| a dispatch whose files collide with an in-flight lane' >> "$D/issues"
LEDGER_STATE_373="$D/state-373"
INFLIGHT_WT_373="$D/inflight-373"
git -C "$REPO" worktree add -q -b lane/373-in-flight "$INFLIGHT_WT_373" main
echo "in-flight lane's own edit" >> "$INFLIGHT_WT_373/file.txt"
PYTHONPATH="$HERE/../../scripts/supervisor" python3 -c "
import core
l = core.Ledger('$LEDGER_STATE_373')
l.register_lane(lane='t:99', pane_id='%99', nonce='n99', harness='claude',
  repo='$REPO', server_id='s99', session_id='sess99', command='claude')
l.reconstruct_task(task_id='as373-in-flight', source_kind='issue', source_url='https://x',
  source_ref='9999', summary='an in-flight lane', source_state='OPEN', status='created',
  evidence=['test'], status_marker=None)
l.assign(task_id='as373-in-flight', lane='t:99', pane_nonce='n99', summary='test', worktree_path='$INFLIGHT_WT_373')
"
echo 'Fix `file.txt` -- collides with the in-flight lane above.' > "$D/brief-373.md"
before_373=$(worktrees)
out=$(LEDGER_STATE="$LEDGER_STATE_373" \
      run 373 collides "$D/brief-373.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a dispatch whose files collide with an in-flight lane is refused" "$rc" 1 "$out"
want_contains "...says collision-check: REFUSE" "collision-check: REFUSE" "$out"
if [ "$(worktrees)" = "$before_373" ]; then ok "no worktree is left behind by a collision-refused dispatch"
else bad "no worktree is left behind by a collision-refused dispatch" "$before_373 before, $(worktrees) after"; fi
if branch_exists "lane/373-collides"; then
  bad "no stray lane/373-collides branch survives a collision-refused dispatch" "$out"
else
  ok "no stray lane/373-collides branch survives a collision-refused dispatch"
fi
if [ -z "$(assignees 373)" ]; then ok "the claim is released when a collision-refused dispatch aborts"
else bad "the claim is released when a collision-refused dispatch aborts" "still assigned: $(assignees 373)"; fi
git -C "$REPO" worktree remove --force "$INFLIGHT_WT_373" >/dev/null 2>&1
git -C "$REPO" branch -D lane/373-in-flight >/dev/null 2>&1

# --- agent-supervisor#240: the box can gain text AFTER verified_preclear's
# own confirming read, before verified_type ever touches the pane -- a gap
# neither step's own evidence can see into. DISPATCH_LEAK_BEFORE_TYPE models
# exactly that (see tmux-dispatch's own comment for the mechanics). Without
# its own upfront `C-u`, verified_type's first literal send glues the brief
# onto the leak, `--proof-head` correctly refuses that as not landed, and
# `dispatch.sh`'s own retry (it already asks verified_type for 2 attempts) is
# what recovers -- at the cost of a second full type attempt every time this
# happens. `--proof-head` alone was #193's fix for the SAME shape caused by a
# swallowed pre-clear Enter; this is a second, independent source of the
# identical corruption, and only an upfront `C-u` closes both at once.
echo '163|| a dispatch whose box gains text between pre-clear and typing' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-240-leak" DISPATCH_LEAK_BEFORE_TYPE="stray leftover " \
      run 163 leaky-box "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "the dispatch still succeeds either way -- the retry safety net still recovers it" "$rc" 0 "$out"
log=$(tmuxlog)
type_attempts=$(grep -c -- "send-keys .*Read $D/brief.md" <<<"$log")
if [ "$type_attempts" -eq 1 ]; then
  ok "the brief lands in exactly ONE literal type attempt -- verified_type's own C-u caught the leak before typing"
else
  bad "the brief should land in exactly one type attempt once verified_type pre-clears its own send" \
    "saw $type_attempts attempt(s); the retry safety net is doing work an upfront C-u should have made unnecessary -- log: $log"
fi

# --- agent-dotfiles#199: dispatch.sh is silent on stderr under bash 3.2 ---
# WHY: `declare -A WINDOW_NAME_BY_INDEX` used to be rejected by macOS's real
# /bin/bash (3.2, no associative arrays), which prints straight to stderr on
# every dispatch and reads exactly like a broken guard on the command that
# decides where work goes -- see #199. `run()` above launches dispatch.sh
# with whatever `bash` PATH resolves to, which is a modern bash on most dev
# machines and never hits the bug; this case runs the script directly so
# its own `#!/bin/bash` shebang picks the interpreter, matching how the
# supervisor actually invokes it in production, and asserts stderr is empty
# on a dispatch that succeeds. That is the regression guard: it is what
# would catch the next 3.2-incompatible construct landing on this path.
echo '170|| dispatch must be silent on stderr' >> "$D/issues"
: > "$D/tmux.log"
rm -rf "$D/panes"; mkdir -p "$D/panes"
STDERR_FILE="$D/dispatch199.err"
PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
  LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
  TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 DISPATCH_PANE_COLS=60 \
  DISPATCH_MESSAGE_BUDGET=430 DISPATCH_CONFIRM_TRIES=2 \
  DISPATCH_SESSION_TIMEOUT=0 \
  AGENT_SUPERVISOR_STATE_DIR="$(mktemp -d "$D/state.XXXXXX")" \
  STUB_PANE_PATH="$REPO" WORKTREE_ROOT="$D/roots" \
  "$DISPATCH" 170 stderr-clean "$D/brief.md" acme/agent-dotfiles "$REPO" \
  1>"$D/dispatch199.out" 2>"$STDERR_FILE"
rc=$?
out=$(cat "$D/dispatch199.out")
err=$(cat "$STDERR_FILE")
want_exit "a dispatch run under its own shebang still succeeds" "$rc" 0 "$out"
# Empty, not just missing "declare:" -- a substring check only catches a
# re-introduced declare -A and waves through any other stray write (proved
# by injecting `echo ... >&2` before dispatch.sh's final exit 0 and watching
# this suite stay green). stderr on a successful dispatch is not a place for
# progress lines: the supervisor treats dispatch.sh's stderr as the signal
# that something is wrong, so a legitimate message belongs on stdout instead.
if [ -z "$err" ]; then ok "stderr is clean on a successful dispatch (agent-dotfiles#199)"
else bad "stderr is clean on a successful dispatch (agent-dotfiles#199)" "expected empty stderr, got: $err"; fi

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
