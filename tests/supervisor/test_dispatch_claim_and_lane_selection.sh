#!/bin/bash
# Scenarios for how dispatch.sh picks a lane and claims an issue once a
# worktree can be created: a task-named lane is still someone's (not free
# just because it looks idle), DISPATCH_LANE pinning, the optional [repo]
# argument not shifting positional args, multi-issue dispatch (#112) and its
# partial-failure unwind, the done-brief contract (#117), the input-box
# budget (#118), and the dispatch being RECORDED, not merely sent (#140).
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

echo "dispatch.sh -- claim and lane-selection mechanics"

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

# --- "free" is not "unowned": a task-named lane is still someone's --------
# `lanes.sh --free` decides free from pane content alone. A lane that finished
# and was never renamed, and a lane paused on an approval prompt, both show no
# busy marker and are indistinguishable from a genuinely unowned lane. The name
# is the only signal that survives that, which is why `claim.sh:124` and
# `loop-tick.md:292-295` both key on `free-N`. The supervisor made this exact
# mistake by hand on 2026-08-11: `--free | head -1` returned another
# dispatcher's task-named lane and it was `/clear`ed.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|ad82-other|claude.exe|❯ ready|1|0
FIX
printf '95|| Needs an unowned lane\n' > "$D/issues"
before=$(worktrees)
out=$(run 95 owned-lane "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "an idle lane still named after a task is not dispatched to" "$rc" 1 "$out"
log=$(tmuxlog)
want_missing "no brief is sent to a task-named lane" "send-keys" "$log"
want_missing "a task-named lane is not renamed out from under its owner" "rename-window" "$log"
want_contains "the refusal says the name convention is why" "free-" "$out"
if [ "$(assignees 95)" = "" ]; then ok "a task-named lane means no claim is taken"; else bad "a task-named lane means no claim is taken" "assignees: $(assignees 95)"; fi
if [ "$(worktrees)" = "$before" ]; then ok "a task-named lane means no worktree is created"; else bad "a task-named lane means no worktree is created" "$before -> $(worktrees)"; fi

# ...and the name filter picks the renamed lane rather than whatever comes
# first, so an owned lane sitting ahead of a free one does not block dispatch.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|ad82-other|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
printf '98|| Needs the renamed lane, not the first one\n' >> "$D/issues"
out=$(run 98 free-named-lane "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "an owned lane ahead of a free one does not block the dispatch" "$rc" 0 "$out"
log=$(tmuxlog)
want_contains "the brief goes to the lane named free-N" "send-keys -t t:@104" "$log"
want_missing "the task-named lane is left untouched" "-t t:@103" "$log"

# --- DISPATCH_LANE is gone: no env var aims a dispatch --------------------
# It used to be honoured verbatim -- no free check, no name check, no
# supervisor exclusion. Reproduced with `DISPATCH_LANE=t:1`: the issue was
# claimed and `/clear` plus the full brief went into the SUPERVISOR's own pane,
# exit 0. That is the incident `loop-tick.md:248-253` documents, reachable
# through a stray env var instead of an empty string. There was no caller, so
# the override was removed rather than gated; these assert it stays removed.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
FIX
printf '96|| Must not land in the supervisor\n' > "$D/issues"
before=$(worktrees)
out=$(DISPATCH_LANE=t:1 run 96 env-override "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "DISPATCH_LANE cannot dispatch when no lane is free" "$rc" 1 "$out"
log=$(tmuxlog)
want_missing "DISPATCH_LANE cannot reach the supervisor's window" "t:1" "$log"
want_missing "DISPATCH_LANE sends nothing at all" "send-keys" "$log"
if [ "$(assignees 96)" = "" ]; then ok "DISPATCH_LANE takes no claim"; else bad "DISPATCH_LANE takes no claim" "assignees: $(assignees 96)"; fi
if [ "$(worktrees)" = "$before" ]; then ok "DISPATCH_LANE creates no worktree"; else bad "DISPATCH_LANE creates no worktree" "$before -> $(worktrees)"; fi

# ...and when a real free lane exists, the env var does not redirect the brief.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '99|| Must go where lanes.sh says\n' >> "$D/issues"
out=$(DISPATCH_LANE=t:1 run 99 no-redirect "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "DISPATCH_LANE does not redirect a dispatch" "$rc" 0 "$out"
log=$(tmuxlog)
want_contains "the brief goes to the lane lanes.sh chose" "send-keys -t t:@103" "$log"
want_missing "not to the window DISPATCH_LANE named" "-t t:@101" "$log"

# --- the optional [repo] argument must not shift the lane into its slot ---
#
# claim.sh's interface is positional: `take <issue> [repo] [lane]`. dispatch.sh
# used to append the repo only when it was non-empty, so omitting it did not
# shorten the argument list -- it moved the WINDOW NAME into the repo slot.
# `claim.sh take 95 ad95-claim-refuses-closed` then ran
# `gh issue view 95 -R ad95-claim-refuses-closed`, which fails, and dispatch.sh
# reported `claim: could not assign #95` for an issue that was open and
# unclaimed. Observed live on 2026-08-11 against agent-dotfiles#95; the same
# dispatch WITH an explicit repo argument succeeded, which is what made the
# positional shift visible.
#
# The failure was indistinguishable from a legitimate "someone else has it".
# Its own fixture issue: every number used above is either claimed by an
# earlier test or absent from $D/issues, and reusing one makes this test depend
# on their order rather than on the behaviour it is checking.
echo '77|| An issue dispatched without a repo argument' >> "$D/issues"
# An EMPTY repo argument, with the fixture repo path still supplied. Passing
# no trailing arguments at all would make dispatch.sh fall back to its default
# repo path -- the real working directory -- and this test would create a real
# branch and worktree in the actual repository. It did exactly that once while
# being written: `lane/77-no-repo-arg` and a stray worktree had to be pruned by
# hand, and the second run then failed because the branch already existed. A
# test that mutates the repo it is testing is not repeatable.
out=$(run 77 no-repo-arg "$D/brief.md" "" "$REPO"); rc=$?
want_exit "a dispatch with no [repo] argument succeeds" "$rc" 0 "$out"
if grep -q "could not assign" <<<"$out"; then
  bad "no-[repo] dispatch does not report a phantom claim failure" "$out"
else
  ok "no-[repo] dispatch does not report a phantom claim failure"
fi
# The lane name must reach claim.sh as the LANE, not as the repo -- assert the
# issue actually ends up claimed rather than trusting the exit code alone.
if [ -n "$(assignees 77)" ]; then
  ok "no-[repo] dispatch actually takes the claim"
else
  bad "no-[repo] dispatch actually takes the claim" "issue 77 has no assignee"
fi
# THE load-bearing assertion for the positional shift, and the only one here
# that is stub-independent. The gh stub ignores -R, so a bogus repo argument
# does not fail under test -- an assertion on exit code or assignee passes with
# the bug still present, which an earlier version of this test did.
#
# claim.sh echoes `#<issue> taken by $LANE`. Under the shift, the window name is
# consumed as the repo and LANE falls back to `hostname -s`, so the claim is
# recorded against the MACHINE rather than the lane -- which is also what makes
# `claim.sh stale` unable to match it to a window later. The window prefix is
# derived from the repo basename, so it is `repo77-` under this fixture and
# `ad77-` in production -- assert the fixture's form, not production's.
want_contains "the lane name reaches claim.sh as the lane, not as the repo" \
  "taken by repo77-no-repo-arg" "$out"

# --- a closed issue: refused end to end, nothing left behind (#95) --------
# On 2026-08-11 dispatch.sh sent two lanes to issues closed nearly three hours
# earlier, because claim.sh's `take` did not check issue state. The fix lives
# in claim.sh, and dispatch.sh's existing "every failure aborts" contract must
# do the rest: no assignee, no worktree, no brief sent.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '150||Closed nearly three hours ago|CLOSED\n' >> "$D/issues"
before=$(worktrees)
out=$(run 150 already-closed "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a dispatch against a closed issue is refused" "$rc" 1 "$out"
log=$(tmuxlog)
want_missing "no brief is sent for a closed issue" "send-keys" "$log"
want_missing "the lane is not renamed for a closed issue" "rename-window" "$log"
if [ "$(assignees 150)" = "" ]; then ok "a closed issue gets no assignee via dispatch"; else bad "a closed issue gets no assignee via dispatch" "assignees: $(assignees 150)"; fi
if [ "$(worktrees)" = "$before" ]; then ok "a closed issue leaves no worktree behind"; else bad "a closed issue leaves no worktree behind" "$before -> $(worktrees)"; fi

# --- multi-issue dispatch: one brief, several issues (#112) ---------------
# #109 and #110 came out of one review of one PR and were dispatched to one
# lane in one brief. dispatch.sh claimed only #110 -- #109 sat open and
# looked free to the next dispatcher while a lane was actively on it. A
# comma-separated issue list must claim every issue named, and the window
# (which lanes.sh and `claim.sh stale` both match on) must still come from
# the FIRST issue, no matter how many are in the list.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '200|| First of a three-issue brief\n202|| Second of a three-issue brief\n203|| Third of a three-issue brief\n' >> "$D/issues"
out=$(run 200,202,203 multi-issue "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a multi-issue dispatch succeeds" "$rc" 0 "$out"
want_contains "the first issue in the list is claimed" "jonhill90" "$(assignees 200)"
want_contains "the second issue in the list is claimed" "jonhill90" "$(assignees 202)"
want_contains "the third issue in the list is claimed" "jonhill90" "$(assignees 203)"
log=$(tmuxlog)
want_contains "the window name comes from the FIRST issue" "ad200-multi-issue" "$log"
want_missing "the window name does not carry the second issue" "ad202-multi-issue" "$log"
want_missing "the window name does not carry the third issue" "ad203-multi-issue" "$log"

# --- a failure partway through the list unwinds what was already claimed --
# #211 is already claimed by another lane, so the take on it must fail. #210
# was claimed first and must be RELEASED, not left assigned: a claim nobody
# can see -- because the dispatch reported failure -- is worse than no claim.
# #211's original holder must be untouched, not overwritten and not cleared.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '210|| Claimable, claimed first\n211|someone-else| Already claimed, second in the list\n' >> "$D/issues"
before=$(worktrees)
out=$(run 210,211 partial-claim "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a failed claim partway through the list aborts the dispatch" "$rc" 1 "$out"
if [ "$(assignees 210)" = "" ]; then
  ok "the earlier successful claim is released on abort"
else
  bad "the earlier successful claim is released on abort" "assignees: $(assignees 210)"
fi
want_contains "the other lane's claim is left alone" "someone-else" "$(assignees 211)"
log=$(tmuxlog)
want_missing "no brief is sent when a claim in the list fails" "send-keys" "$log"
if [ "$(worktrees)" = "$before" ]; then ok "no worktree is left behind by a partial claim"; else bad "no worktree is left behind by a partial claim" "$before -> $(worktrees)"; fi

# --- the lane is told what "done" means, by the dispatcher (#117) ----------
#
# A lane completed #112 correctly -- tests green, mutation-checked, committed --
# and stopped, because its brief never said to push. It was right to be literal.
# From outside that is indistinguishable from a lane that did nothing: no PR, no
# comment, issue still claimed, and the work living only as an unpushed commit
# in a temporary worktree.
#
# Every other brief that night said "open a PR when done". Depending on that is
# depending on whoever wrote the brief remembering, which is the mechanism that
# failed in #114. So the dispatcher states it, on every dispatch.
echo '78|| a dispatch that must say what done means' >> "$D/issues"
cp "$D/brief.md" "$D/brief-orig.md"
out=$(run 78 deliverable-contract "$D/brief.md" "" "$REPO"); rc=$?
want_exit "a dispatch still succeeds with the contract attached" "$rc" 0 "$out"
brief=$(cat "$D/brief.md")
want_contains "the lane is told to push and open a PR" "push your branch and open a PR" "$brief"
want_contains "a no-code lane is told to comment instead" "post your findings as a comment" "$brief"
want_contains "and told why it matters" "unshipped work looks exactly like no work" "$brief"
want_contains "the contract defers to the brief, so a read-only brief still wins" \
  "Unless this brief says otherwise" "$brief"
want_contains "the brief's own content is left alone" "$(head -1 "$D/brief-orig.md")" "$brief"

# The contract goes in the BRIEF, not the typed message -- see the next block
# for why. Assert that directly, or a later edit could move it back into the
# pane and every assertion above would still pass.
want_missing "the contract is not typed into the pane" "unshipped work looks exactly like no work" "$(tmuxlog)"

# Re-dispatching the same brief must not stack the contract up.
out=$(run 78 deliverable-contract "$D/brief.md" "" "$REPO")
if [ "$(grep -c 'dispatch:deliverable-contract' "$D/brief.md")" = 1 ]; then
  ok "a re-dispatch does not append the contract twice"
else
  bad "a re-dispatch does not append the contract twice" \
    "found $(grep -c 'dispatch:deliverable-contract' "$D/brief.md") copies"
fi

# --- the typed message must fit a lane's visible input box (#118) ----------
#
# THE REGRESSION THIS PINS. The message is typed into the lane's input box and
# then verified by reading the pane back. That box shows only its last few rows
# and scrolls INTERNALLY: past roughly 450 characters at 80x24 the head leaves
# the visible region, `capture-pane` cannot see it, the grep for `Read $BRIEF`
# fails, dispatch retypes once, fails again and aborts -- unwinding the claim
# and the worktree for a message that actually arrived.
#
# Measured against a real Claude Code TUI, not this stub: at 80x24 a 610-char
# message failed 4/4 and the 389-char one passed 4/4; at 126x60 both passed.
# `free-9` and `free-10` are 80x24, so the long version broke dispatch to real
# lanes -- while all 78 assertions here stayed green, because the stub modelled
# an input line of unbounded height. DISPATCH_PANE_ROWS now models that box.
echo '79|| a dispatch into a small lane' >> "$D/issues"
out=$(DISPATCH_PANE_ROWS=7 DISPATCH_PANE_COLS=60 DISPATCH_MESSAGE_BUDGET=430 run 79 small-lane "$D/brief.md" "" "$REPO"); rc=$?
want_exit "a dispatch succeeds into a lane whose input box shows only 7 rows" "$rc" 0 "$out"

# And the guard is load-bearing: a message too long for that box must FAIL, or
# the assertion above is satisfied by a stub that cannot see the problem.
#
# The length comes from a DEEP BRIEF PATH rather than a giant slug, because the
# slug also names the branch and the worktree directory and a filesystem-illegal
# name aborts the dispatch earlier, for the wrong reason. Long paths are also
# what actually happens here: the worktree paths in this estate run past 90
# characters on their own.
DEEP="$D/$(printf 'nested-brief-directory/%.0s' $(seq 1 12))"
mkdir -p "$DEEP" && cp "$D/brief-orig.md" "$DEEP/brief.md"
echo '80|| a dispatch whose message is too long' >> "$D/issues"
out=$(DISPATCH_PANE_ROWS=7 DISPATCH_PANE_COLS=60 DISPATCH_MESSAGE_BUDGET=99999 \
      run 80 long-message "$DEEP/brief.md" "" "$REPO"); rc=$?
want_exit "an over-long message is caught by the pane check, not silently sent" "$rc" 1 "$out"
want_contains "and says the brief did not land" "did not land intact" "$out"

# The budget check is the cheaper guard in front of it: it refuses before
# touching a lane at all, and names the reason.
echo '81|| a dispatch over the message budget' >> "$D/issues"
out=$(DISPATCH_MESSAGE_BUDGET=430 run 81 long-message "$DEEP/brief.md" "" "$REPO"); rc=$?
want_exit "a message over the budget refuses up front" "$rc" 1 "$out"
want_contains "and explains the 80x24 limit" "over the 430-char budget" "$out"
want_missing "nothing is typed at a lane when the budget is blown" "send-keys" "$(tmuxlog)"

# --- the dispatch is RECORDED, not left to be inferred later (#140) -------
#
# Every signal that a lane is busy is inferred from pane content today, and
# inference is what produced the false-`free` bugs #102, #123 and #126. A
# successful dispatch must leave a record that says so, in the ledger, without
# changing anything about what runs: the assertions above this line are the
# same ones, and they pass unchanged.
#
# Its own state directory, because the successful dispatches earlier in this
# file already recorded a task against lane t:3 and the ledger allows one
# outstanding task per lane. In production lane-done.sh completes that task
# before the lane can be redispatched -- it does the rename that makes the
# lane eligible at all -- so the sequence this isolates for is the test
# file's, not the estate's.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '140|| a dispatch that must be recorded\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-140" run 140 ledger-record "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a dispatch that will be recorded still succeeds" "$rc" 0 "$out"
status=$(LEDGER_STATE="$D/state-140" ledger status 2>&1)
want_contains "the lane the brief went to is recorded" '"lane":"t:3"' "$status"
want_contains "the pane identity is recorded, not guessed" '"pane_id":"%3"' "$status"
want_contains "the harness is recorded" '"harness":"claude"' "$status"
want_contains "the task is recorded under the window name the estate keys on" \
  '"id":"ad140-ledger-record"' "$status"
want_contains "the task is recorded as delivered -- the brief was verified in the pane" \
  '"status":"delivered"' "$status"
want_contains "the record carries the worktree the lane was given" "$D/roots" "$status"
want_contains "the record carries the issue it was dispatched for" '"source_ref":"140"' "$status"

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
