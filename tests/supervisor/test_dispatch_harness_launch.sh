#!/bin/bash
# Harness-specific launch behavior: codex's no-approval relaunch posture
# (agent-supervisor#30), codex folding the brief pointer into its LAUNCH_CMD
# (agent-dotfiles#255) with its own mutation check, a copilot lane dispatch
# green against the stub (agent-dotfiles#216), and the LANE_META sanity
# guard (agent-supervisor#144 finding 4) that catches a ledger row whose
# recorded harness disagrees with what was actually launched.
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

echo "dispatch.sh -- harness-specific launch shape (codex/copilot) and the LANE_META guard"

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
cp "$D/brief.md" "$D/brief-orig.md"

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

# --- agent-supervisor#30: codex relaunch uses explicit no-approval posture ---
#
# The old codex launch shortcut (`--dangerously-bypass-approvals-and-sandbox`)
# was present in the adapter and visible in the live lane's `ps` output, but
# #30 measured that the lane still stalled on command/edit approvals. The
# adapter now records the explicit CLI knobs that control the two dimensions:
# `-a never` for approval policy and `-s danger-full-access` for sandboxing.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
4|free-4|codex|  gpt-5.5 medium · /repo/path|1|0
FIX
printf '30|| codex lane must not stall on approvals\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-30" run 30 codex-approval "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a dispatch to a codex lane succeeds" "$rc" 0 "$out"
log=$(tmuxlog)
want_contains "the codex harness is relaunched with explicit no-approval flags" \
  "codex -a never -s danger-full-access" "$log"
want_missing "the old ambiguous codex shortcut is not relaunched" \
  "codex --dangerously-bypass-approvals-and-sandbox" "$log"
status=$(LEDGER_STATE="$D/state-30" ledger status 2>&1)
want_contains "the codex harness is recorded" '"harness":"codex"' "$status"

# --- agent-dotfiles#255: codex folds the brief pointer into its LAUNCH_CMD --
#
# codex's own CLI accepts the initial user prompt as a launch argument
# (`codex [OPTIONS] [PROMPT]`), and a prompt given that way starts a real
# turn immediately -- verified live against codex-cli 0.148.0, never
# reproduced against a mock, because codex's fresh-lane path does NOT treat
# the first message TYPED into a live pane as a real turn (it is consumed as
# the session's auto-generated title instead). harness/codex.sh's
# HARNESS_LAUNCH_TAKES_PROMPT tells dispatch.sh to fold the short
# "Read $BRIEF ..." pointer into codex's own LAUNCH_CMD instead of typing it
# into the pane after launch, so there is no separate typed message left for
# a title-eating quirk, a corrupted `/clear`, or a bracketed-paste stall to
# land on.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
5|free-5|codex|  gpt-5.5 medium · /repo/path|1|0
FIX
printf '255|| dispatch.sh must fold the brief into codex launch, not type it\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-255" run 255 codex-launch-prompt "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a dispatch to a fresh codex lane succeeds" "$rc" 0 "$out"
log=$(tmuxlog)
want_contains "the brief pointer is folded into codex's own LAUNCH_CMD, not typed after" \
  "codex -a never -s danger-full-access Read" "$log"
want_missing "the folded prompt is never typed into the pane as a separate send-keys" \
  "send-keys -t t:@105 Read" "$log"

# --- MUTATION-CHECK, agent-dotfiles#255 -----------------------------------
# codex paints its own known failure signature ("Session renamed to") when
# the folded launch prompt was NOT accepted as a turn. dispatch.sh must
# refuse, not report success -- exactly the silent-success shape #255 is
# about: window renamed, worktree created, ledger updated, exit 0, no work
# ever started.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
6|free-6|codex|  Session renamed to whatever the launch prompt was · /repo/path|1|0
FIX
printf '256|| a codex lane that ate the launch prompt as a title must refuse\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-256" run 256 codex-title-eaten "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "dispatch refuses when codex's own title-eaten signature is on the pane" "$rc" 1 "$out"
want_contains "the refusal names what happened" \
  "did not accept the folded launch prompt" "$out"
status=$(LEDGER_STATE="$D/state-256" ledger status 2>&1)
# NOT '"status":"delivered"' -- that string legitimately appears on the
# CLAIM placeholder itself (`commit_lane_claim`'s CLAIM_STATUS_LIVE == the
# literal string "delivered"), set at step 4.5's point of no return, BEFORE
# verified_launch_prompt ever runs (agent-dotfiles#209: so a killed
# dispatcher never loses track of a possibly-live brief). What a refused
# dispatch must never produce is the REAL dispatch record `record_dispatch`
# writes under the window name -- same convention #162's own check above
# already uses.
want_missing "the issue has no real DISPATCH record, only the held claim placeholder" \
  '"id":"ad256-codex-title-eaten"' "$status"

# agent-dotfiles#255 round 2's OTHER finding -- a codex lane still stuck on
# its own directory-trust menu after the folded-launch settle, reproduced
# LIVE against a real codex lane -- is covered in tests/supervisor/test_send.sh
# (section 9, `verified_launch_prompt`'s `--blocked-re`/`--option-row-re`),
# not here: this stub's lane fixture is one static line read BOTH at lane
# SELECTION (this file's `lanes.sh --free` step) and by any later capture-pane
# this dispatch takes, so it cannot model "read free, then painted a menu
# only after respawn-pane relaunched it" -- the exact shape a real dispatch
# hits and the earlier "codex-title-eaten" case above does not. `test_send.sh`
# calls `verified_launch_prompt` directly, without a lane-selection step in
# front of it, so it can set that pane content on its own terms.

# --- agent-dotfiles#216: a copilot lane, GREEN against the stub -----------
#
# The bug's own reproduction, against a stub instead of the live
# council-copilot pane #216 explicitly forbids touching: a lane running
# `node` -- copilot's process name is indistinguishable from any other Node
# harness -- reads `free` from `lanes.sh` and is STILL refused by
# `dispatch.sh` before this fix, because `cli.py lane-free`'s backfill could
# only map `codex`/`claude`/`claude.exe` process names to a harness. Once the
# pane's harness is a RECORDED fact (the `@hill90_lane_harness` option
# `bootstrap-session.sh`/`cli.py register` would have set), the same dispatch
# reaches it end to end: claimed, briefed, and recorded with the harness
# that was actually running, not a guess from `node`.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
7|free-7|node|← open sidebar|1|0
FIX
printf '216|| a copilot lane must be dispatchable\n' >> "$D/issues"
RUN_PRESEED_PANES='tmux set-option -p -t t:7 @hill90_lane_harness copilot' \
  out=$(LEDGER_STATE="$D/state-216" run 216 copilot-lane "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a dispatch to a recorded-copilot lane succeeds" "$rc" 0 "$out"
log=$(tmuxlog)
want_contains "the brief reaches the copilot lane, by its window id" "send-keys -t t:@107" "$log"
want_contains "the brief is submitted to the copilot lane" "send-keys -t t:@107 Enter" "$log"
status=$(LEDGER_STATE="$D/state-216" ledger status 2>&1)
want_contains "the lane the brief went to is recorded" '"lane":"t:7"' "$status"
want_contains "the RECORDED harness is copilot, not guessed from 'node'" '"harness":"copilot"' "$status"

# The unidentifiable case still refuses (agent-dotfiles#216's own rule): the
# SAME node lane with no harness option recorded must not be dispatched to --
# "cannot tell" staying refused is correct behaviour, not the defect.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
8|free-8|node|← open sidebar|1|0
FIX
printf '217|| an unrecorded node lane must stay refused\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-217" run 217 unrecorded-node "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a dispatch with no free lane refuses" "$rc" 1 "$out"
want_contains "the refusal names the unidentifiable lane" "no free lane" "$out"
log=$(tmuxlog)
want_missing "nothing was ever sent to the unrecorded lane" "send-keys -t t:@108" "$log"

# --- THE LANE_META SANITY GUARD (agent-dotfiles#144 finding 4) ------------
#
# The pane-identity probe the ledger recording block makes right before
# `record-dispatch` can itself fail against a real tmux -- a target it
# cannot resolve prints a single-line error, not the pipe-joined template
# dispatch.sh expects. The guard exists to catch that BEFORE `IFS='|' read`
# scatters an error string across PANE_ID/PANE_CMD/etc and hands it to
# cli.py as if it were real pane identity. The brief must still go out --
# this is a bookkeeping failure, not a dispatch failure, same contract as
# #140's own ledger-failure tolerance.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '145|| a dispatch whose pane-identity probe itself fails\n' >> "$D/issues"
# Pre-registered so agent-dotfiles#174's OWN pane-identity read (lane
# selection's first-sight backfill, step 1) does not also hit
# STUB_LANE_META_BROKEN and refuse the lane before this test's actual target
# -- the step 6 probe -- is ever reached. With the lane already known-free,
# step 1 answers from the ledger alone and never touches tmux.
preregister_lane "$D/state-145" t:3 t:3
export STUB_LANE_META_BROKEN=1
out=$(LEDGER_STATE="$D/state-145" run 145 ledger-meta-broken "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
unset STUB_LANE_META_BROKEN
want_exit "a broken pane-identity probe does NOT abort the dispatch" "$rc" 0 "$out"
log=$(tmuxlog)
want_contains "the brief still goes out" "send-keys -t t:@103" "$log"
want_contains "and is still submitted" "send-keys -t t:@103 Enter" "$log"
want_contains "the guard names the failure as an unreadable pane probe" \
  "could not read pane metadata" "$out"
status=$(LEDGER_STATE="$D/state-145" ledger status 2>&1)
want_contains "the pre-registered lane is still the only one on record" '"lane":"t:3"' "$status"
# agent-dotfiles#184: this used to assert `"tasks":[]` -- true before this
# dispatch's own step-1 `claim-lane` call existed, because nothing wrote to
# the tasks table until step 6, and step 6 never runs a real record-dispatch
# call down this branch (the malformed probe is caught before it). Now step
# 1's claim placeholder is what is left behind, and that is correct, not
# garbage: the brief DID go out (asserted above), so the lane must keep
# reading occupied even though bookkeeping past the claim degraded -- the
# same property #188 added `mark_lane_held` for on the record-dispatch side.
want_contains "the claim placeholder is what is left recording the lane occupied" \
  '"id":"ledger-claim:t:3:ad145-ledger-meta-broken"' "$status"

# ...and that guard is load-bearing. Patch a copy that always takes the
# "well-formed" branch regardless of what LANE_META actually contains, and
# confirm the specific reason string above disappears -- a suite that still
# reports "could not read pane metadata" with the guard removed has not
# tested the guard, only the ledger-failure tolerance underneath it.
# agent-supervisor#716: the LANE_META guard now lives in dispatch-record.sh.
BROKEN_META_DIR=$(make_mutant_scripts_dir)
BROKEN_META_GUARD="$BROKEN_META_DIR/dispatch.sh"
patch_rc=0
PYTHONPATH="$HERE${PYTHONPATH:+:$PYTHONPATH}" python3 - "$BROKEN_META_DIR" <<'PY' || patch_rc=$?
import sys
import _dispatch_mutate as M
target = sys.argv[1]
marker = 'if [ -z "$LANE_META" ] || [[ "$LANE_META" != *"|"* ]]; then'
M.patch(target, marker, "if false; then  # MUTATED: guard always skipped")
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh whose LANE_META guard is skipped" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh whose LANE_META guard is skipped"
  printf '146|| the same broken probe, against the unguarded copy\n' >> "$D/issues"
  preregister_lane "$D/state-146" t:3 t:3
  export STUB_LANE_META_BROKEN=1
  out=$(DISPATCH_SCRIPT="$BROKEN_META_GUARD" LEDGER_STATE="$D/state-146" \
        run 146 ledger-meta-unguarded "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
  unset STUB_LANE_META_BROKEN
  if ! grep -qF "could not read pane metadata" <<<"$out"; then
    ok "mutation confirmed: skipping the guard loses the specific pane-probe diagnosis (the assertion above would now be red)"
  else
    bad "mutation confirmed: skipping the guard loses the specific pane-probe diagnosis" \
      "the unguarded copy still reported 'could not read pane metadata' -- the patch missed the real guard: $out"
  fi
  want_exit "the unguarded copy still does not abort the dispatch" "$rc" 0 "$out"
fi

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
