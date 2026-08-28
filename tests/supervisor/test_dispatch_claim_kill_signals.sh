#!/bin/bash
# agent-dotfiles#209 (both rounds): a dispatcher process killed mid-dispatch
# must leave the lane claim in a state a later reap can recover, never
# silently HELD forever and never silently freed while work is actually in
# flight. SIGKILL (untrappable, the reap is the only cover) and SIGTERM
# (trappable, the trap must not wait for a later reap) are each exercised
# both before AND after the point of no return (the SEND), with mutation
# checks proving the trap and the reap are each load-bearing, and a
# regression check on where the commit point sits.
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

echo "dispatch.sh -- a dispatcher killed between claim and release (agent-dotfiles#209)"

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

# --- agent-dotfiles#209: a dispatcher killed between claim and release -----
#
# Everything above proves that every abort path dispatch.sh ENUMERATES
# releases its lane claim. #209 is about the paths it cannot enumerate: a
# `kill`, an OOM, a closed terminal, a host crash. The placeholder
# `claim_lane` writes is a task with status `created`, and `lane_available`
# counts any non-terminal status as occupied -- so a dispatcher that dies
# holding one leaves the lane reading occupied with nothing working it. That
# is agent-dotfiles#102's failure shape (dispatch capacity silently falling to
# zero while lanes sit idle) reached through the mechanism built to prevent
# it, and it was hand-reconciled nine times in two days before this test
# existed.
#
# The kill is delivered by standing in for `python3` (DISPATCH_PYTHON, which
# dispatch.sh already reads) and signalling the DISPATCHER the instant its
# claim has committed -- the exact instant the gap opens. No new seam in
# dispatch.sh, and the victim is named by the `--owner-pid` argument the claim
# itself carries, so the test cannot kill the wrong process.
cat > "$D/kill-after-claim.sh" <<'KILL'
#!/bin/bash
set -uo pipefail
out=$(python3 "$@" 2>&1); rc=$?
printf '%s\n' "$out"
case " $* " in
  *" claim-lane "*) ;;
  *) exit $rc ;;
esac
grep -qF '"claimed":true' <<<"$out" || exit $rc
victim=""; prev=""
for a in "$@"; do
  [ "$prev" = "--owner-pid" ] && victim="$a"
  prev="$a"
done
[ -n "$victim" ] || { echo "kill-after-claim: no --owner-pid in: $*" >&2; exit $rc; }
kill "-${DISPATCH_TEST_KILL_SIGNAL:-KILL}" "$victim" 2>/dev/null
# On a TERM the dispatcher runs its trap only once this foreground child is
# gone; on a KILL it is already dead. The pause keeps either from racing the
# assertions that follow.
sleep 1
exit $rc
KILL
chmod +x "$D/kill-after-claim.sh"

# Runs one dispatch that gets signalled right after its claim commits.
# Leaves $CRASH_RC and $CRASH_OUT.
run_killed_dispatch() {  # run_killed_dispatch <state> <issue> <slug> <signal> <script>
  local state="$1" issue="$2" slug="$3" signal="$4" script="$5"
  printf '%s|| a dispatcher signalled between claim and release\n' "$issue" >> "$D/issues"
  CRASH_OUT=$(LEDGER_STATE="$state" DISPATCH_PYTHON="$D/kill-after-claim.sh" \
              DISPATCH_TEST_KILL_SIGNAL="$signal" DISPATCH_SCRIPT="$script" \
              run "$issue" "$slug" "$D/brief.md" acme/agent-dotfiles "$REPO")
  CRASH_RC=$?
}

# --- SIGKILL: untrappable by any shell, so the reap is the only cover ------
CRASH_STATE="$D/state-crash"
run_killed_dispatch "$CRASH_STATE" 601 crash-after-claim KILL "$DISPATCH"
want_exit "a dispatcher SIGKILLed right after its claim dies un-cleanly" "$CRASH_RC" 137 "$CRASH_OUT"
crash_status=$(LEDGER_STATE="$CRASH_STATE" ledger status)
want_contains "...leaving its claim placeholder behind: nothing released it" \
  '"id":"ledger-claim:t:3:ad601-crash-after-claim"' "$crash_status"
want_contains "...and the placeholder records the owner that died" '[owner=' "$crash_status"
want_contains "...so the ledger reads lane t:3 OCCUPIED with nothing working it (#102's shape)" \
  "False" "$(lane_available "$CRASH_STATE" t:3)"

echo '602|| the lane must come back after a killed dispatcher' >> "$D/issues"
out=$(LEDGER_STATE="$CRASH_STATE" run 602 after-crash "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "the NEXT dispatch reclaims that lane: the stranded claim is reaped" "$rc" 0 "$out"
want_contains "...and says what it cleared instead of doing it silently" "cleared stranded lane claim" "$out"
want_contains "...and the brief actually goes out" "dispatch: #602 -> " "$out"

# --- SIGTERM: trappable, and the trap must not wait for a later reap -------
TERM_STATE="$D/state-term"
run_killed_dispatch "$TERM_STATE" 603 term-after-claim TERM "$DISPATCH"
want_exit "a dispatcher SIGTERMed right after its claim exits through its trap" "$CRASH_RC" 143 "$CRASH_OUT"
want_contains "...and the TRAP released the claim immediately -- lane free with no reap yet" \
  "True" "$(lane_available "$TERM_STATE" t:3)"
term_status=$(LEDGER_STATE="$TERM_STATE" ledger status)
want_missing "...and left no placeholder behind at all" "ledger-claim:t:3" "$term_status"

# --- MUTATION: remove the trap, and the SIGTERM case must go red ----------
# The reap cannot stand in for this: a TERMed dispatcher's pid is gone, so a
# LATER dispatch would reap its claim either way. What the trap buys is the
# lane coming back AT ONCE rather than at the mercy of the next dispatch, so
# the assertion this mutation has to break is the one taken immediately after
# the signal, with no dispatch in between.
# agent-supervisor#716: the traps live in dispatch-lane-select.sh now, not
# dispatch.sh's own text -- make_mutant_scripts_dir copies the WHOLE
# directory, and _dispatch_mutate.py's patch() searches it for whichever
# file actually carries the marker.
NO_TRAP_DIR=$(make_mutant_scripts_dir)
NO_TRAP_MUTANT="$NO_TRAP_DIR/dispatch.sh"
patch_rc=0
PYTHONPATH="$HERE${PYTHONPATH:+:$PYTHONPATH}" python3 - "$NO_TRAP_DIR" <<'PY' || patch_rc=$?
import sys
import _dispatch_mutate as M
target = sys.argv[1]
marker = '''trap 'release_claim_on_signal; release_lane_claim' EXIT
trap 'release_claim_on_signal; release_lane_claim; exit 143' TERM   # 128 + 15
trap 'release_claim_on_signal; release_lane_claim; exit 130' INT    # 128 + 2'''
M.patch(target, marker, ': # MUTATED: no trap -- only the enumerated abort paths release')
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh with no claim-release trap" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh with no claim-release trap"
  NO_TRAP_STATE="$D/state-no-trap"
  run_killed_dispatch "$NO_TRAP_STATE" 604 term-no-trap TERM "$NO_TRAP_MUTANT"
  if [ "$(lane_available "$NO_TRAP_STATE" t:3)" = "False" ]; then
    ok "mutation confirmed: with no trap, a SIGTERMed dispatcher strands its claim (the assertion above would now be red)"
  else
    bad "mutation confirmed: with no trap, a SIGTERMed dispatcher strands its claim" \
      "expected lane_available False, got '$(lane_available "$NO_TRAP_STATE" t:3)'; rc=$CRASH_RC out=$CRASH_OUT"
  fi
fi

# --- MUTATION: remove the reap, and the SIGKILL case must go red ----------
# agent-supervisor#716: the reap block now lives in dispatch-guards.sh.
NO_REAP_DIR=$(make_mutant_scripts_dir)
NO_REAP_MUTANT="$NO_REAP_DIR/dispatch.sh"
patch_rc=0
PYTHONPATH="$HERE${PYTHONPATH:+:$PYTHONPATH}" python3 - "$NO_REAP_DIR" <<'PY' || patch_rc=$?
import sys
import _dispatch_mutate as M
target = sys.argv[1]
marker = '''if REAP_OUT=$("$LEDGER_PYTHON" "$LEDGER_CLI" reap-lane-claims 2>&1); then'''
M.patch(target, marker, 'if REAP_OUT="" && false; then  # MUTATED: no reap of stranded claims')
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh that never reaps a stranded claim" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh that never reaps a stranded claim"
  NO_REAP_STATE="$D/state-no-reap"
  run_killed_dispatch "$NO_REAP_STATE" 605 crash-no-reap KILL "$NO_REAP_MUTANT"
  echo '606|| the lane the un-reaped mutant can never get back' >> "$D/issues"
  out=$(LEDGER_STATE="$NO_REAP_STATE" DISPATCH_SCRIPT="$NO_REAP_MUTANT" \
        run 606 after-crash-no-reap "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
  if [ "$rc" -ne 0 ] && grep -qF "no free lane" <<<"$out"; then
    ok "mutation confirmed: with no reap, the killed dispatcher's lane never comes back (the assertions above would now be red)"
  else
    bad "mutation confirmed: with no reap, the killed dispatcher's lane never comes back" \
      "expected a refusal naming 'no free lane'; rc=$rc out=$out"
  fi
  want_contains "and the refusal names the stranded claim id" "ledger-claim:t:3:ad605-crash-no-reap" "$out"
  want_contains "and the refusal names how to clear that stranded claim by hand" "release-lane-claim --lane t:3 --token ad605-crash-no-reap" "$out"
fi

# --- agent-dotfiles#209 round 2: the point of no return is the SEND -------
#
# Every case above signals the dispatcher right after `claim-lane`, which is
# the EARLIEST instant the claim exists -- nothing has been typed, no lane has
# been renamed, and freeing the claim there is correct. None of them signals
# after the brief is live, and that absence is what let the first round of
# this fix ship with the cleanup pointed at the wrong instant.
#
# The re-review's measurement: the trap and the reap both treated
# `CLAIM_COMMITTED` (set after step 5's confirmation loop) as the point of no
# return, but the brief goes live ~70 lines earlier, at the `send-keys Enter`
# that submits it -- up to DISPATCH_CONFIRM_TRIES x DISPATCH_SETTLE (10s by
# default) of wall clock in between. A signal landing in that window ran the
# trap and deleted the claim AFTER the lane had been renamed and the brief
# submitted, and `lane_available` then answered True for a lane that was
# actively working. #102/#126's failure shape produced by the cleanup instead
# of prevented by it, which dispatch.sh's own step 6 comment says in its own
# words must not happen.
#
# So the commit point is now the ledger fact `commit-lane-claim` writes
# IMMEDIATELY BEFORE that Enter, and the two assertions below are about the
# same instant from the two sides that can reach it: a trappable signal
# (SIGTERM, the trap runs) and an untrappable one (SIGKILL, only the reap
# runs). Both must leave the lane HELD.
#
# The signal is delivered by the tmux stub at the submit -- see
# DISPATCH_TEST_SEND_SIGNAL there. No live pane anywhere in this.

# Runs one dispatch that gets signalled at the instant its brief is submitted.
# Leaves $LIVE_RC and $LIVE_OUT.
run_signalled_at_send() {  # run_signalled_at_send <state> <issue> <slug> <signal> <script>
  local state="$1" issue="$2" slug="$3" signal="$4" script="$5"
  printf '%s|| a dispatcher signalled as its brief goes live\n' "$issue" >> "$D/issues"
  LIVE_OUT=$(LEDGER_STATE="$state" DISPATCH_TEST_SEND_SIGNAL="$signal" \
             DISPATCH_SCRIPT="$script" \
             run "$issue" "$slug" "$D/brief.md" acme/agent-dotfiles "$REPO")
  LIVE_RC=$?
}

# --- SIGTERM at the submit: the trap must NOT free a working lane ---------
LIVE_TERM_STATE="$D/state-live-term"
run_signalled_at_send "$LIVE_TERM_STATE" 701 live-then-term TERM "$DISPATCH"
want_exit "a dispatcher SIGTERMed as its brief goes live dies through its trap" "$LIVE_RC" 143 "$LIVE_OUT"
# The probe is only worth anything if the brief REALLY went out first. Assert
# that before asserting anything about the ledger: a signal that landed early
# would leave the lane held for the wrong reason and pass the check below
# while proving nothing.
live_term_log=$(cat "$D/tmux.log")
want_contains "...and the signal was delivered at the submit, not earlier" "signalled TERM to " "$live_term_log"
want_contains "...the lane really was renamed to the task first (a real dispatch)" \
  "rename-window -t t:@103 ad701-live-then-term" "$live_term_log"
want_contains "...and the brief really was submitted into the pane" "send-keys -t t:@103 Enter" "$live_term_log"
# THE ASSERTION. Before the commit point moved, this read True.
want_contains "...so the lane stays HELD: a brief is live in it and no cleanup may free it" \
  "False" "$(lane_available "$LIVE_TERM_STATE" t:3)"
live_term_status=$(LEDGER_STATE="$LIVE_TERM_STATE" ledger status)
want_contains "...and the claim placeholder is still there holding it" \
  '"id":"ledger-claim:t:3:ad701-live-then-term"' "$live_term_status"
# agent-supervisor#572: the ISSUE claim (GitHub assignee, distinct from the
# lane placeholder above) must survive this signal too, for the same reason
# -- release_claim is now trapped alongside release_lane_claim (#572), and
# both share the CLAIM_COMMITTED gate, so a signal past step 4.5 must not
# release either. This is "genuinely concurrent work in progress must still
# read as claimed" from the other side: nothing here proves a SECOND
# dispatcher was refused, but it proves the mechanism that refusal depends on
# (the assignee) was not wiped out from under the first one by its own
# signal handler -- a fix that cleared this on any signal would have traded
# #572's stall for a race the moment two dispatchers ever crossed paths here.
if [ "$(assignees 701)" = "jonhill90" ]; then
  ok "...and the GitHub issue claim also stays HELD -- the signal did not release work that is live"
else
  bad "...and the GitHub issue claim also stays HELD" "assignees: $(assignees 701)"
fi

# --- SIGKILL at the submit: the reap must NOT free a working lane ---------
# The dangerous half, and the one moving an in-process flag cannot fix: a
# SIGKILL leaves the placeholder behind, and at that moment the placeholder is
# the ONLY record that the lane is occupied, because step 6's `record_dispatch`
# never ran. A reap that judges only "is the owner pid gone" cannot tell that
# apart from a claim taken with nothing sent yet -- so the fact the send
# happened has to be written to the LEDGER before the send, which is what
# `commit-lane-claim` does and what the reap now refuses to touch.
LIVE_KILL_STATE="$D/state-live-kill"
run_signalled_at_send "$LIVE_KILL_STATE" 702 live-then-kill KILL "$DISPATCH"
want_exit "a dispatcher SIGKILLed as its brief goes live dies un-cleanly" "$LIVE_RC" 137 "$LIVE_OUT"
live_kill_log=$(cat "$D/tmux.log")
want_contains "...with the lane renamed and the brief submitted first" \
  "rename-window -t t:@103 ad702-live-then-kill" "$live_kill_log"
want_contains "...and the brief really was submitted into the pane" "send-keys -t t:@103 Enter" "$live_kill_log"
want_contains "...so the lane reads HELD immediately after the kill" \
  "False" "$(lane_available "$LIVE_KILL_STATE" t:3)"

# ...and it must STILL read held after a reap runs, which is what the next
# dispatch does first. This is the assertion the reap's liveness rule alone
# cannot satisfy: the owner pid IS provably gone.
echo '703|| the next dispatch must not take a lane that is working' >> "$D/issues"
out=$(LEDGER_STATE="$LIVE_KILL_STATE" run 703 the-next-one "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "the NEXT dispatch refuses rather than take the lane the killed dispatcher left working" "$rc" 1 "$out"
want_contains "...naming the only lane it had as unavailable" "no free lane" "$out"
want_contains "...and it did NOT reap the live claim away" \
  '"id":"ledger-claim:t:3:ad702-live-then-kill"' "$(LEDGER_STATE="$LIVE_KILL_STATE" ledger status 2>&1)"
want_missing "...and typed nothing into that pane" "rename-window -t t:@103 ad703-the-next-one" "$(cat "$D/tmux.log")"
# Fail-closed has a price and the refusal must name it: this lane needs a
# human, and `release-lane-claim` deliberately will not clear it.
want_contains "...and says to record a completion when a live brief finished without signalling" \
  "record-completion --lane t:3" "$out"
want_missing "...and does not suggest release-lane-claim for a live delivered claim" \
  "release-lane-claim" "$out"
want_missing "...and does not suggest cancel-open-task for a delivered idle lane" \
  "cancel-open-task" "$out"

# --- the previous round's finding must not regress ------------------------
# Moving the commit point later would be an easy way to satisfy everything
# above and reopen #209 round 1: a dispatcher killed BEFORE the brief is live
# must still release its claim at once. That is what the `603` case near the
# top of this block asserts (SIGTERM right after `claim-lane` -> lane_available
# True with no reap yet), and this repeats it against the SEND-time harness so
# both instants are covered by the same file: same signal, same dispatcher,
# but the brief never gets typed because the send fails outright.
NO_SEND_STATE="$D/state-no-send"
echo '704|| a dispatcher killed before the brief goes live' >> "$D/issues"
NO_SEND_OUT=$(LEDGER_STATE="$NO_SEND_STATE" DISPATCH_PYTHON="$D/kill-after-claim.sh" \
              DISPATCH_TEST_KILL_SIGNAL=TERM run 704 killed-before-send "$D/brief.md" \
              acme/agent-dotfiles "$REPO"); NO_SEND_RC=$?
want_exit "a dispatcher killed BEFORE the brief goes live still exits through its trap" "$NO_SEND_RC" 143 "$NO_SEND_OUT"
want_missing "...having submitted nothing into the pane" "send-keys -t t:@103 Enter" "$(cat "$D/tmux.log")"
want_contains "...and its claim IS released at once -- nothing is working that lane" \
  "True" "$(lane_available "$NO_SEND_STATE" t:3)"

# --- MUTATION: move the commit point back to where it was -----------------
# The guard has to survive its own mutation. This patches a copy whose
# `commit-lane-claim` call is removed and whose CLAIM_COMMITTED is set where it
# used to be -- after step 5's confirmation loop, ~70 lines past the submit --
# which is exactly the shape the re-review reproduced. Both assertions above
# must go red on it, and by the two DIFFERENT mechanisms they test: the trap
# (TERM) and the reap (KILL).
# agent-supervisor#716: the commit-lane-claim call and its CLAIM_COMMITTED=1
# stay together in dispatch-send.sh (step 4.5 was not split across a file
# boundary) but step 6's header -- where this mutation re-inserts a LATE
# CLAIM_COMMITTED=1 -- is now in dispatch-record.sh, a different file. Patched
# as two independent M.patch() calls against the same sandbox: each finds its
# own target file by searching, same as every other mutation in this suite.
LATE_COMMIT_DIR=$(make_mutant_scripts_dir)
LATE_COMMIT_MUTANT="$LATE_COMMIT_DIR/dispatch.sh"
patch_rc=0
PYTHONPATH="$HERE${PYTHONPATH:+:$PYTHONPATH}" python3 - "$LATE_COMMIT_DIR" <<'PY' || patch_rc=$?
import sys
import _dispatch_mutate as M
target = sys.argv[1]
commit_span = '''COMMIT_OUT=$("$LEDGER_PYTHON" "$LEDGER_CLI" commit-lane-claim --lane "$LANE" --token "$CLAIM_TOKEN" 2>&1) \\
  || COMMIT_OUT="${COMMIT_OUT:-commit-lane-claim failed to run}"
if ! grep -qF '"committed":true' <<<"$COMMIT_OUT"; then
  sed 's/^/  /' <<<"$COMMIT_OUT" >&2
  abort_send "could not mark $LANE's claim live before sending -- #$ISSUE_ARG was NOT dispatched (nothing was submitted)"
fi
CLAIM_COMMITTED=1'''
M.patch(target, commit_span, ': # MUTATED: no ledger commit, and CLAIM_COMMITTED set late instead')
late = "# --- 6. record what was dispatched."
M.patch(target, late, "CLAIM_COMMITTED=1  # MUTATED: back where round 1 had it\n\n" + late)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh whose commit point is back after the send" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh whose commit point is back after the send"
  LATE_TERM_STATE="$D/state-late-term"
  run_signalled_at_send "$LATE_TERM_STATE" 705 late-commit-term TERM "$LATE_COMMIT_MUTANT"
  if [ "$(lane_available "$LATE_TERM_STATE" t:3)" = "True" ] \
     && grep -qF "send-keys -t t:@103 Enter" "$D/tmux.log"; then
    ok "mutation confirmed: with the commit point late, the TRAP frees a lane whose brief is live (the assertion above would now be red)"
  else
    bad "mutation confirmed: with the commit point late, the TRAP frees a lane whose brief is live" \
      "expected lane_available True after a submitted brief, got '$(lane_available "$LATE_TERM_STATE" t:3)'; rc=$LIVE_RC out=$LIVE_OUT"
  fi

  LATE_KILL_STATE="$D/state-late-kill"
  run_signalled_at_send "$LATE_KILL_STATE" 706 late-commit-kill KILL "$LATE_COMMIT_MUTANT"
  echo '707|| the lane the late-commit mutant hands out from under a worker' >> "$D/issues"
  out=$(LEDGER_STATE="$LATE_KILL_STATE" DISPATCH_SCRIPT="$LATE_COMMIT_MUTANT" \
        run 707 late-commit-next "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
  if [ "$rc" -eq 0 ] && grep -qF "rename-window -t t:@103 ad707-late-commit-next" "$D/tmux.log"; then
    ok "mutation confirmed: with the commit point late, the REAP hands a working lane to the next dispatcher (the assertions above would now be red)"
  else
    bad "mutation confirmed: with the commit point late, the REAP hands a working lane to the next dispatcher" \
      "expected the next dispatch to succeed into t:3; rc=$rc out=$out"
  fi
fi

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
