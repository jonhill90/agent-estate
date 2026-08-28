#!/bin/bash
# agent-supervisor#236: the launch command dispatch.sh sends must be the
# pane's actual PROCESS, not just typed text -- and a normal dispatch (no
# menu, no mutation) still has to work end to end. agent-supervisor#456
# (VerifySurvived): a ledger row must not read as live until the pane has
# been RE-OBSERVED after the launch -- a pane that dies right after the
# brief was submitted must not be recorded as a successful dispatch, while
# a healthy one still is.
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

echo "dispatch.sh -- the launch command is the pane's PROCESS, and success is verified survival (agent-supervisor#236/#456)"

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

# --- agent-supervisor#236: the launch command is the pane's PROCESS, ------
# never keystrokes typed into whatever the respawn produced ----------------
#
# The live incident: a lane was found blocked on a Claude Code menu offering
# to run a pasted `claude --dangerously-skip-permissions --model sonnet` --
# option 2 would have spawned a nested Claude session inside the lane.
# `stubs/tmux-dispatch`'s `STUB_MENU_PANES` models exactly this: a target
# window whose `send-keys` lands as menu NAVIGATION, never as text, with
# `Enter` committing whichever option is pending (or `STUB_MENU_DEFAULT` if
# nothing digit-shaped was ever sent) -- the same model #159's own
# regression suite (test_inbox_route.sh) already uses for the sibling
# incident (a reply routed into a menu-blocked lane).
#
# `MUTATED_236` is the pre-fix shape of the harness-relaunch step, patched
# out of the REAL dispatch.sh source (never hand re-implemented) the same
# way `MUTATED_17`/`MUTATED_169` above prove a check is actually reached: a
# straight string swap back to `respawn-pane -k`, a settle sleep, then a
# blind `send-keys "$LAUNCH_CMD" Enter`.
printf '236|| dispatch.sh must never type its launch command\n' >> "$D/issues"
printf '238|| dispatch.sh must never type its launch command (fixed run)\n' >> "$D/issues"
# agent-supervisor#716: both markers below live in dispatch-send.sh now
# (step 3.5 was not split across a file boundary), not in dispatch.sh's own
# text -- make_mutant_scripts_dir copies the whole directory and
# _dispatch_mutate.py's patch() finds the file that carries each marker.
MUTATED_236_DIR=$(make_mutant_scripts_dir)
MUTATED_236="$MUTATED_236_DIR/dispatch.sh"
patch_rc=0
PYTHONPATH="$HERE${PYTHONPATH:+:$PYTHONPATH}" python3 - "$MUTATED_236_DIR" <<'PY' || patch_rc=$?
import sys
import _dispatch_mutate as M
target = sys.argv[1]

launch_cmd_marker = 'LAUNCH_CMD="${H_LAUNCH_CMD[$HARNESS_HIDX]}"\n'
M.patch(
    target,
    launch_cmd_marker,
    launch_cmd_marker + 'LAUNCH_LITERAL="${H_SEND_LITERAL[$HARNESS_HIDX]:-0}"\n',
)

respawn_marker = (
    'if ! tmux respawn-pane -k -t "$LANE_TARGET" -c "$WORKTREE" "$LAUNCH_CMD" 2>/dev/null; then\n'
    '  abort_send "tmux respawn-pane failed for $LANE -- could not put it in its worktree; #$ISSUE_ARG was NOT dispatched"\n'
    'fi\n'
)
pre_236_shape = (
    'if ! tmux respawn-pane -k -t "$LANE_TARGET" -c "$WORKTREE" 2>/dev/null; then\n'
    '  abort_send "tmux respawn-pane failed for $LANE -- could not put it in its worktree; #$ISSUE_ARG was NOT dispatched"\n'
    'fi\n'
    '\n'
    'sleep "${DISPATCH_RESPAWN_SETTLE:-1}"\n'
    '\n'
    'if [ "$LAUNCH_LITERAL" = 1 ]; then\n'
    '  tmux send-keys -t "$LANE_TARGET" -l "$LAUNCH_CMD" 2>/dev/null \\\n'
    '    && tmux send-keys -t "$LANE_TARGET" Enter 2>/dev/null \\\n'
    '    || abort_send "could not relaunch harness \'$LANE_HARNESS\' in $LANE -- #$ISSUE_ARG was NOT dispatched"\n'
    'else\n'
    '  tmux send-keys -t "$LANE_TARGET" "$LAUNCH_CMD" Enter 2>/dev/null \\\n'
    '    || abort_send "could not relaunch harness \'$LANE_HARNESS\' in $LANE -- #$ISSUE_ARG was NOT dispatched"\n'
    'fi\n'
)
M.patch(target, respawn_marker, pre_236_shape)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh in the pre-#236 blind-type shape" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  chmod +x "$MUTATED_236"
  ok "setup: patched a copy of dispatch.sh in the pre-#236 blind-type shape"

  # RED: the pre-#236 shape, against a lane whose pane is an agent's menu at
  # the moment the launch would be sent -- STUB_MENU_DEFAULT=2 is the exact
  # option the live incident's menu had pending (spawn a nested Claude).
  export STUB_MENU_PANES=3 STUB_MENU_DEFAULT=2
  pre236_out=$(DISPATCH_SCRIPT="$MUTATED_236" run 236 launch-cmd-typed-blind "$D/brief.md" acme/agent-dotfiles "$REPO" 2>&1)
  pre236_log=$(tmuxlog)
  pre236_pre_rename=$(sed -n '1,/^rename-window/{/^rename-window/!p;}' <<<"$pre236_log")
  want_contains "pre-#236 shape: the launch command IS typed at the pane, before anything checks what is listening" \
    "send-keys -t t:@103" "$pre236_pre_rename"
  selected_236=$(cat "$D/panes/3.selected" 2>/dev/null | head -1)
  # agent-supervisor#421: the guard's bin dir is now prefixed onto LAUNCH_CMD
  # before this mutation's blind `send-keys -l` ever runs (that prefix sits
  # ahead of the respawn-pane call this mutation patches out, so the mutation
  # inherits it same as production would). The stub's literal-send path scans
  # EVERY character for a digit and treats the last one seen as the pending
  # menu option -- previously moot because "claude --model sonnet
  # --dangerously-skip-permissions" has no digits at all, so nothing but
  # STUB_MENU_DEFAULT was ever pending. The guard's state-dir path (this
  # sandbox's own mktemp name) can contain digits, so the option actually
  # committed is now whichever digit was last typed, not necessarily
  # STUB_MENU_DEFAULT. Derive the expectation from what was actually sent
  # (the same hazard either way: an unvalidated menu commits blindly) rather
  # than hard-coding a value this sandbox's own temp-dir naming can change.
  #
  # agent-supervisor#548: derive the digits from the KEYS only (everything
  # after `-l `), not the whole send-keys line. The whole line also carries
  # `-t t:@103`, the tmux pane target -- digits tmux consumes as an option,
  # never keystrokes the stub sees. Scanning the whole line let the pane
  # id's own last digit stand in for the pending menu option, so the
  # assertion only passed when the pane id happened to end in
  # STUB_MENU_DEFAULT and flaked on every other pane id (confirmed via a
  # captured failing run: pane `@103`, STUB_MENU_DEFAULT=2, predicted 3,
  # actually committed 2).
  literal_send_line=$(grep -m1 '^send-keys .*-l ' <<<"$pre236_log")
  literal_keys=${literal_send_line#*-l }
  expected_menu_commit=$(grep -o '[0-9]' <<<"$literal_keys" | tail -1)
  expected_menu_commit="${expected_menu_commit:-$STUB_MENU_DEFAULT}"
  want_contains "pre-#236 shape: that blind Enter commits the menu's pending option -- the nested-claude spawn the live incident found" \
    "$expected_menu_commit" "$selected_236"

  # GREEN: the real, fixed dispatch.sh, same menu-pane lane, same default.
  green_out=$(run 238 launch-cmd-typed-fixed "$D/brief.md" acme/agent-dotfiles "$REPO" 2>&1)
  green_log=$(tmuxlog)
  green_pre_rename=$(sed -n '1,/^rename-window/{/^rename-window/!p;}' <<<"$green_log")
  want_missing "fixed: nothing is ever typed at the pane before the rename -- respawn-pane is the only call" \
    "send-keys" "$green_pre_rename"
  want_contains "fixed: the harness is the pane's PROCESS -- respawn-pane's own argv carries the launch command" \
    "respawn-pane -k -t t:@103 -c" "$green_pre_rename"
  want_contains "...specifically the harness's launch command, not a bare shell" \
    "claude --model sonnet --dangerously-skip-permissions" "$green_pre_rename"
  respawn_cmd=$(cat "$D/panes/3.respawn-cmd" 2>/dev/null || true)
  want_contains "...recorded as the pane's actual respawned process, not as typed keys" \
    "claude --model sonnet --dangerously-skip-permissions" "$respawn_cmd"
  unset STUB_MENU_PANES STUB_MENU_DEFAULT
fi

# --- ...and a normal dispatch (no menu, no mutation) still works end to end,
# unchanged by #236 -- the very first case in this file (`a dispatch to a
# free lane succeeds`, issue 81, asserted above) already covers this: it
# ran against the real $DISPATCH with the #236 fix in place and passed like
# every other assertion in this run. Reasserted here, by name, as the
# explicit "and a normal dispatch still works" the issue's acceptance
# criteria calls for -- not left as an implication of the suite staying
# green.
printf '237|| a normal dispatch still works end to end after #236\n' >> "$D/issues"
normal_out=$(run 237 normal-dispatch-after-236 "$D/brief.md" acme/agent-dotfiles "$REPO"); normal_rc=$?
want_exit "a normal dispatch (no menu in the way) still succeeds end to end after #236" "$normal_rc" 0 "$normal_out"
normal_log=$(tmuxlog)
want_contains "...the brief still lands in the lane" "$D/brief.md" "$normal_log"
want_contains "...and is still submitted" "send-keys -t t:@103 Enter" "$normal_log"

# --- agent-supervisor#456: VerifySurvived -----------------------------------
# Gastown's `StartSession` re-checks `HasSession` AFTER startup and refuses
# to trust the launch command's own exit code; #453 mined this as the fix for
# our own "dispatch reports success, the agent never started" shape
# (agent-dotfiles#255, #178/#179's swallowed submits). The property under
# test: a ledger row must not read live until the pane has been RE-OBSERVED
# running the agent, not merely "tmux accepted the send".
#
# The ledger's own answer for a specific task's `accepted_at` -- the exact
# column `record_dispatch(confirm_landed=...)` stamps, and the one a
# reconciler reads instead of taking "the lane went quiet" as a stand-in for
# "the work began" (see core.py's `record_dispatch` docstring, agent-
# supervisor#193). No tmux in the path, same posture as `lane_available`
# above.
task_accepted_at() {  # task_accepted_at <state-dir> <task-id>
  AGENT_SUPERVISOR_STATE_DIR="$1" python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
task = Ledger(sys.argv[2]).get_task(sys.argv[3])
print(task["accepted_at"] if task else "NO-SUCH-TASK")
' "$HERE/../../scripts/supervisor" "$1" "$2" 2>&1
}

# --- 1. THE KILL: the pane is gone right after the brief was submitted -----
# STUB_KILL_ON_SUBMIT marks the window dead at the SAME send-keys call that
# submits the brief (see stubs/tmux-dispatch's own comment on the hook point)
# -- `verified_submit`'s own capture-pane read still sees a real, empty box
# (a real crash empties the box on its way out too), so SEND_STATUS reads
# `submitted` exactly as a healthy dispatch would. Only the LATER
# `verified_survived` read (display-message, not capture-pane) finds the
# window gone. That ordering is the whole point: this is not distinguishable
# from a healthy dispatch by verified_submit alone, which is exactly why
# #456 says nothing before it could have caught this.
printf '456|| verify the pane survived startup before trusting ledger success\n' >> "$D/issues"
STATE_456="$D/state-456"
before_456=$(worktrees)
died_out=$(LEDGER_STATE="$STATE_456" STUB_KILL_ON_SUBMIT=1 DISPATCH_SURVIVE_SETTLE=0 DISPATCH_SURVIVE_RETRIES=1 \
  run 456 survive-456 "$D/brief.md" acme/agent-dotfiles "$REPO"); died_rc=$?
want_exit "dispatch.sh exits non-zero when the pane died right after submit -- not a clean success" "$died_rc" 1 "$died_out"
want_contains "...the warning names the lane and says the pane is GONE" "pane is GONE" "$died_out"
want_missing "...and the success line is never printed over it" "dispatch: #456 -> " "$died_out"

died_status=$(LEDGER_STATE="$STATE_456" ledger status 2>&1)
# The pane is genuinely gone by this point (`.killed` fired at the submit
# Enter, before LANE_META's own read) -- `LANE_META` reads every field
# blank, exactly as real tmux does for an unresolvable target (see
# verified_survived's own header), and `record_dispatch`'s PRE-EXISTING
# `_register_lane_tx` guard already refuses to register a lane from blank
# identity fields. So no fresh task row is ever created for this
# dispatch -- only the step-4.5 claim placeholder remains -- which is a
# STRONGER "the ledger does not record success" than a row with
# accepted_at unset would be: there is no dispatch-shaped row at all.
died_accepted=$(task_accepted_at "$STATE_456" "ad456-survive-456")
want_contains "...no fresh task row is ever created for the dead dispatch -- not even an unaccepted one" "NO-SUCH-TASK" "$died_accepted"
want_missing "...specifically: the ledger's own id for what record-dispatch WOULD have written never appears" '"id":"ad456-survive-456"' "$died_status"
want_contains "...only the pre-existing claim placeholder survives, still held" '"status":"delivered"' "$died_status"
if [ "$(lane_available "$STATE_456" "t:3")" = False ]; then
  ok "...and the lane stays HELD, per #456's own invariant (a lane wrongly freed costs a running lane's work)"
else
  bad "...and the lane stays HELD, per #456's own invariant" "lane_available: $(lane_available "$STATE_456" "t:3")"
fi

# --- 2. A HEALTHY DISPATCH STILL SUCCEEDS, ACCEPTED AND ALL -----------------
# The other direction the brief asks for: this new re-check must not turn a
# genuinely fine dispatch into a false refusal. Same lane fixture, no kill.
printf '457|| a healthy dispatch still survives and is accepted\n' >> "$D/issues"
STATE_457="$D/state-457"
alive_out=$(LEDGER_STATE="$STATE_457" DISPATCH_SURVIVE_SETTLE=0 DISPATCH_SURVIVE_RETRIES=1 \
  run 457 survive-457-healthy "$D/brief.md" acme/agent-dotfiles "$REPO"); alive_rc=$?
want_exit "a healthy dispatch (pane never dies) still exits 0 with verified_survived in place" "$alive_rc" 0 "$alive_out"
alive_accepted=$(task_accepted_at "$STATE_457" "ad457-survive-457-healthy")
want_missing "...and IS marked accepted -- verified_survived does not weaken #193's confirm-landed signal" "None" "$alive_accepted"

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
