#!/bin/bash
# agent-supervisor#159: a PR-scoped dispatch (review or fix pass) does not
# need to touch the issue the PR closes. Five cases: a review of PR N
# dispatches while its own issue stays claimed by the original work, a fix
# pass (no author to exclude), the author guard still holding on this path
# (acceptance #4), an already-claimed PR refusing via an issue comment
# (acceptance #6), and the fix pass on THIS PR (agent-supervisor#169) where
# step 0.6 is itself the exercised guard.
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

echo "dispatch.sh -- a PR-scoped dispatch does not need its issue (agent-supervisor#159)"

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
# --- agent-supervisor#159: a PR-scoped dispatch does not need its ISSUE ---
#
# WHY: a review of PR N, or a fix pass on PR N, is not new work on a fresh
# issue -- it is work ON THE PR, and the issue that PR closes (or the
# tracking issue a reviewer was handed) is correctly already claimed by the
# in-flight work. `claim.sh take` refusing that was correct FOR ITS OWN
# MODEL; the model was missing a dispatchable representation of "work on PR
# N" distinct from "work on issue N". Three real collisions (measured, see
# the issue) came from working around that refusal with a ledger-bypassing
# tmux hand-off instead of fixing the model: #142's fix pass, #157's review,
# #149's fix pass, the last two landing with a literal "b"-suffixed second
# task id because nothing could see the first one was already there.
#
# RED FIRST (acceptance #3): before this PR, case 1 below failed exactly the
# way the issue quotes -- `claim.sh take` on the review's own issue refused
# because it actually was already claimed, and the whole dispatch aborted.
# Verified by hand: `git stash` on dispatch.sh/cli.py/core.py and re-running
# this section reproduces exit 1 with "is not available -- pick different
# work" for case 1, "cannot tell which harness" is never reached. Restoring
# the stash turns it green. That stash/restore is not re-run by this suite
# (there would be nothing left here to assert against a script that no
# longer exists), so this comment is the record of it.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX

# --- case 1: a review of PR N dispatches while its own issue stays claimed
printf '910|| the code PR #950 was written from\n' >> "$D/issues"
# 911 is pre-claimed by someone else entirely -- unrelated to PR #950's
# authorship, standing in for the tracking issue a real reviewer is handed
# that just happens to already be assigned (the exact shape #149's own
# `dispatch.sh 112 rev149 --reviews-pr 149` hit).
printf '911|someone-else|review PR #950\n' >> "$D/issues"
printf '950|Fixes #910|lane/910-original-work\n' >> "$D/prs"

out=$(LEDGER_STATE="$D/state-159" run 910 original-work "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the authoring dispatch (#910) succeeds" "$rc" 0 "$out"

out=$(LEDGER_STATE="$D/state-159" run 911 rev950 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 950); rc=$?
want_exit "a review of PR #950 dispatches even though issue #911 is already claimed" "$rc" 0 "$out"
want_contains "the author's lane (t:3) is skipped as usual" "skipping t:3" "$out"
log=$(tmuxlog)
want_contains "and the review lands on the other free lane, t:4" "send-keys -t t:@104" "$log"
if [ "$(assignees 911)" = "someone-else" ]; then
  ok "issue #911 stays claimed by the original work -- no GitHub assignee call was made for it"
else
  bad "issue #911 stays claimed by the original work" "assignees changed to: $(assignees 911)"
fi
PR950_LANE=$(LEDGER_STATE="$D/state-159" ledger pr-lane --pr 950)
want_contains "the ledger records this dispatch AGAINST THE PR, visibly" '"known":true' "$PR950_LANE"
want_contains "...naming the lane that took it" '"lane":"t:4"' "$PR950_LANE"

# --- case 2: a fix pass on PR N (not a review -- no author to exclude) ---
# `--pr` alone, no `--reviews-pr`: the author guard must NOT run (no PR
# fixture entry for #951 exists at all -- if dispatch.sh wrongly tried
# `gh pr view 951`, the stub would fail loudly and this would refuse).
printf '912|| the code a fix pass on PR #951 targets\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-159b" run 912 original-951 "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the original dispatch (#912) succeeds" "$rc" 0 "$out"

out=$(LEDGER_STATE="$D/state-159b" run 912 fix-951 "$D/brief.md" acme/agent-dotfiles "$REPO" --pr 951); rc=$?
want_exit "a fix pass on PR #951 dispatches while issue #912 stays claimed by the same in-flight work" "$rc" 0 "$out"
log=$(tmuxlog)
want_contains "and lands on a DIFFERENT lane than the one still working #912" "send-keys -t t:@104" "$log"
PR951_LANE=$(LEDGER_STATE="$D/state-159b" ledger pr-lane --pr 951)
want_contains "the fix pass is visible in the ledger by PR too" '"known":true' "$PR951_LANE"

# --- case 3 (acceptance #4): the author guard still holds on this path ---
# Only ONE free lane (t:3), and it is the author's -- the review must still
# refuse, proving `--reviews-pr`'s new PR-scoped skip of the issue claim did
# not also skip the guard agent-dotfiles#212/#254/#263 and #137 exist for.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
printf '913|| the code PR #952 was written from\n' >> "$D/issues"
printf '914|someone-else|review PR #952\n' >> "$D/issues"
printf '952|Fixes #913|lane/913-author-work\n' >> "$D/prs"

out=$(LEDGER_STATE="$D/state-159c" run 913 author-work "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the authoring dispatch (#913) succeeds" "$rc" 0 "$out"
LEDGER_STATE="$D/state-159c" ledger record-completion --task ad913-author-work --note done >/dev/null
# Now only the author's lane (t:3) is free.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX

out=$(LEDGER_STATE="$D/state-159c" run 914 rev952 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 952); rc=$?
want_exit "the review is still refused when the only free lane is the author -- #159 does not reopen #212" "$rc" 1 "$out"
want_contains "still names the PR" "PR #952" "$out"
want_contains "still names the authoring task" "ad913-author-work" "$out"
if [ "$(assignees 914)" = "someone-else" ]; then
  ok "a refused PR-scoped review leaves issue #914's own (unrelated) claim alone"
else
  bad "a refused PR-scoped review leaves issue #914's own claim alone" "assignees: $(assignees 914)"
fi

# --- case 4 (acceptance #6, issue comment): a PR already claimed refuses,
# rather than minting a second "...b" task -----------------------------
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
printf '916|| first dispatch on PR #953\n' >> "$D/issues"
printf '917|| a second dispatcher tries PR #953 too\n' >> "$D/issues"

out=$(LEDGER_STATE="$D/state-159d" run 916 first-953 "$D/brief.md" acme/agent-dotfiles "$REPO" --pr 953); rc=$?
want_exit "setup: the first PR #953 dispatch succeeds" "$rc" 0 "$out"

before=$(worktrees)
out=$(LEDGER_STATE="$D/state-159d" run 917 second-953b "$D/brief.md" acme/agent-dotfiles "$REPO" --pr 953); rc=$?
want_exit "a second dispatch of the SAME PR is refused, not duplicated" "$rc" 1 "$out"
want_contains "the refusal names the PR" "PR #953" "$out"
want_contains "and names the lane already holding it" "ad916-first-953" "$out"
log=$(tmuxlog)
want_missing "a refused duplicate sends no brief" "send-keys" "$log"
if [ "$(worktrees)" = "$before" ]; then
  ok "a refused duplicate creates no worktree -- no '...b' task is minted"
else
  bad "a refused duplicate creates no worktree" "$before -> $(worktrees)"
fi

# MUTATION-CHECK: silence step 0.6's PR-lane refusal and confirm the SAME
# second dispatch is STILL refused -- proof that step 0.6 is not the ONLY
# thing standing between a duplicate PR dispatch and the ledger.
#
# agent-supervisor#169: before that fix, this assertion read the other way
# (silencing step 0.6 let the duplicate straight through) -- step 0.6 WAS the
# only guard. It no longer is: `core.py`'s `one_open_pull_per_source_ref`
# write-time trigger (untouched by this mutant, which only patches
# dispatch.sh) still catches it, seconds later, at record-dispatch. This is
# now the load-bearing proof of DEFENSE IN DEPTH, not of step 0.6 alone --
# see case 5 below for the mutation check that defeats the write-time gate
# itself and confirms THAT one is load-bearing.
# agent-supervisor#716: the PR-lane duplicate check now lives in
# dispatch-guards.sh.
MUTATED_159_DIR=$(make_mutant_scripts_dir)
MUTATED_159="$MUTATED_159_DIR/dispatch.sh"
patch_rc=0
PYTHONPATH="$HERE${PYTHONPATH:+:$PYTHONPATH}" python3 - "$MUTATED_159_DIR" <<'PY' || patch_rc=$?
import sys
import _dispatch_mutate as M
target = sys.argv[1]
marker = 'if grep -qF \'"known":true\' <<<"$PR_LANE_JSON"; then'
M.patch(target, marker, "if false; then  # MUTATED: PR-lane duplicate check always skipped")
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh whose PR-lane duplicate check is silenced" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh whose PR-lane duplicate check is silenced"
  chmod +x "$MUTATED_159"
  printf '918|| a third dispatcher tries PR #953 against the mutated guard\n' >> "$D/issues"
  out=$(DISPATCH_SCRIPT="$MUTATED_159" LEDGER_STATE="$D/state-159d" \
        run 918 third-953c "$D/brief.md" acme/agent-dotfiles "$REPO" --pr 953); rc=$?
  want_exit "with step 0.6 silenced, the duplicate is STILL refused -- the write-time gate catches it" "$rc" 1 "$out"
  want_contains "...refused at the WRITE this time, not the read" \
    "PR #953 is already claimed by lane t:3 (task ad916-first-953) -- the write refused" "$out"
fi

# --- case 5 (agent-supervisor#169, the fix pass on THIS PR): step 0.6 is a
# TOCTOU by itself -- reproduced directly by a reviewer of #169 using the
# SAME `DISPATCH_TEST_RACE_HOOK` #184 already wires (it fires per lane
# candidate, which is AFTER step 0.6 completes for dispatcher A): dispatcher
# A passes step 0.6 (PR not yet claimed, nothing recorded yet), THEN
# dispatcher B runs a whole competing dispatch for the SAME PR -- B's OWN
# step 0.6 also reads "not yet claimed" (A hasn't written anything either),
# so B proceeds, wins a free lane, and completes its dispatch cleanly. Only
# when A resumes and reaches record-dispatch, seconds later, does the WRITE
# -- not the read -- have to be the thing that catches it. Two free lanes
# this time (t:3, t:4): unlike #184's race (both dispatchers wanting the
# SAME lane), this is two dispatchers wanting the SAME PR on two DIFFERENT
# lanes, which is exactly the "b"-suffixed collision (#157/#149) and the
# real one this estate paid for (#181/#182).
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX

run_race 919 "$DISPATCH" 920 --pr 960
want_exit "dispatcher B (spliced in mid-A's lane selection) completes its own PR #960 dispatch" "$RACE_RC_B" 0 "$RACE_OUT_B"
want_contains "...and B's brief actually went out" "dispatch: #920 -> " "$RACE_OUT_B"
want_exit "dispatcher A is refused: B already won the same PR, at the WRITE, not the read" "$RACE_RC_A" 1 "$RACE_OUT_A"
want_contains "...and the refusal is LOUD, not silent" "PR #960 is already claimed by lane" "$RACE_OUT_A"
# Both briefs DID go out -- unlike step 0.6's own refusal (case 4), which
# catches the common case before any worktree or brief exists, this is the
# LAST-RESORT gate: by the time the write runs, A's brief is already live in
# its own lane's pane (agent-dotfiles#140's own invariant -- nothing can
# unsend it). What must be true is the LEDGER never lies about it afterward.
log=$(tmuxlog)
b_lane_target=$(sed -n 's/.*target: *\(t:@10[0-9]\).*/\1/p' <<<"$RACE_OUT_B" | head -1)
want_contains "B's own brief reached its lane" "send-keys -t $b_lane_target" "$log"
PR960_LANE=$(LEDGER_STATE="$D/state-race-919" ledger pr-lane --pr 960)
want_contains "the ledger records exactly ONE open holder for PR #960 -- not both" '"known":true' "$PR960_LANE"
want_contains "...and it is B, the actual winner of the write" '"task":"ad920-race-b-920"' "$PR960_LANE"
want_contains "A's own lane is marked HELD, not left reading falsely free" "the lane is working, and cli.py has marked it HELD" "$RACE_OUT_A"

# MUTATION-CHECK: defeat the write-time gate (core.py's
# `one_open_pull_per_source_ref` trigger, created but never fires) and
# confirm the SAME race now lets BOTH dispatchers land -- the exact
# collision #181/#182 measured, reproduced through a script that differs
# from the real one only by this one guard being gone. Same technique as
# the step-0.6 mutation check above: a patched COPY of dispatch.sh whose
# `HERE=` points at a patched copy of the whole `scripts/supervisor`
# directory (cli.py imports core.py by relative path, so both must move
# together), used for dispatcher A only -- A opens the shared ledger first
# (well before the hook splices B in), so A's mutated code is what actually
# decides whether the trigger's real logic ever gets created for this race.
MUTATED_169_DIR="$D/mutated-supervisor-169"
MUTATED_169_DISPATCH="$D/dispatch-no-pr-write-gate.sh"
patch_rc=0
python3 - "$HERE/../../scripts/supervisor" "$MUTATED_169_DIR" "$DISPATCH" "$MUTATED_169_DISPATCH" <<'PY' || patch_rc=$?
import shutil
import sys
from pathlib import Path

src_dir, mutated_dir, dispatch_src, dispatch_dst = (
    Path(sys.argv[1]), Path(sys.argv[2]), Path(sys.argv[3]), Path(sys.argv[4])
)
# The whole directory, not just *.py: dispatch.sh also shells out to
# lanes.sh/claim.sh/worktree.sh (and sources input-box.sh/harness-registry.sh/
# session-defaults.sh) via "$HERE/...", and $HERE below points INTO this copy.
shutil.copytree(
    src_dir, mutated_dir, dirs_exist_ok=True,
    ignore=shutil.ignore_patterns("__pycache__", "*.pyc"),
)

# agent-supervisor#706 split core.py's schema (including this trigger)
# out into core_ledger_schema.py behind a re-export shim -- core.py itself
# no longer contains the CREATE TRIGGER text. Search the whole core*.py
# module set rather than the one file that used to hold it, so a future
# re-split doesn't silently stop mutating anything: find every file that
# contains the marker, require exactly one match TOTAL across the set (not
# merely one per file -- a clause could be unique per-file yet duplicated
# across files), and patch that file.
marker = "            WHEN NEW.source_kind = 'pull' AND EXISTS ("
core_modules = sorted(mutated_dir.glob("core*.py"))
hits = [(p, p.read_text().count(marker)) for p in core_modules]
total = sum(n for _, n in hits)
assert total == 1, (
    "pull-uniqueness trigger WHEN clause not found or not unique across "
    f"core*.py -- script shape changed (per-file counts: {hits})"
)
target = next(p for p, n in hits if n == 1)
core_text = target.read_text()
mutated_core = core_text.replace(
    marker,
    "            -- MUTATED: agent-supervisor#169 write-time gate defeated\n"
    "            WHEN 0 AND EXISTS (",
    1,
)
assert mutated_core != core_text, f"mutation did not change {target.name}"
target.write_text(mutated_core)

dispatch_text = dispatch_src.read_text()
here = 'HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"'
assert dispatch_text.count(here) == 1, "HERE assignment not found or not unique -- script shape changed"
dispatch_text = dispatch_text.replace(here, 'HERE=%r' % str(mutated_dir), 1)
dispatch_dst.write_text(dispatch_text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh/core.py whose PR write-time gate is defeated" \
    "could not patch (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh/core.py whose PR write-time gate is defeated"
  chmod +x "$MUTATED_169_DISPATCH"
  run_race 921 "$MUTATED_169_DISPATCH" 922 --pr 964
  want_exit "mutation confirmed: with the write-time gate defeated, A's dispatch of the SAME PR now succeeds too" \
    "$RACE_RC_A" 0 "$RACE_OUT_A"
  want_contains "...both dispatchers, two lanes, one PR -- the exact collision this fix closes" \
    "dispatch: #921 -> " "$RACE_OUT_A"
fi

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
