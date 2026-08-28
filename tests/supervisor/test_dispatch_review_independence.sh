#!/bin/bash
# agent-dotfiles#212: a review must not land on the lane that authored the
# work, resolved through the ledger (never the tmux window name). Covers
# the base exclusion, a FIX-PASS lane being excluded too (agent-supervisor#190)
# and not just the original author, cancelled rows still identifying the
# author for exclusion purposes (#79), and completed rows doing the same
# (#90), plus a mutation check on the "only open tasks" author lookup that
# #90 reported broken.
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

echo "dispatch.sh -- a review must not land on the lane that wrote it (agent-dotfiles#212/agent-supervisor#190/#79/#90)"

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

# --- agent-dotfiles#212: a review must not land on the lane that wrote it -
#
# WHY: on 2026-08-12 the review of #204 was dispatched to lane 4, the same
# lane that had written the code under review (ad193/ad204), and its APPROVE
# had to be thrown away and a second review dispatched. The fix is
# `--reviews-pr`: the caller names which PR is under review, and dispatch.sh
# resolves that PR's authoring task from the ledger -- never from a window
# name -- and refuses to hand it back to its own author.
#
# Two free lanes this time (t:3 and t:4), so there is a genuine choice to
# make: skipping the author must land on the OTHER free lane, not just fail.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
printf '193|| the code PR #204 was written from\n' >> "$D/issues"
printf '205|| review PR #204, first attempt\n' >> "$D/issues"
printf '206|| review PR #204, second attempt\n' >> "$D/issues"
# PR #204's branch names the authoring dispatch's slug -- see worktree.sh
# new's `BRANCH="lane/$SLUG"`, called with dispatch.sh's own
# `${ISSUE}-${SLUG}` -- which is the exact mapping step 0.5 verifies before
# trusting it.
printf '204|Fixes #193|lane/193-telegram-to-director\n' >> "$D/prs"

out=$(LEDGER_STATE="$D/state-212" run 193 telegram-to-director "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "the authoring dispatch (#193) succeeds" "$rc" 0 "$out"
log=$(tmuxlog)
# TARGETS ARE WINDOW IDS HERE, LANE NAMES ARE INDICES (#241, merged after this
# section was written). The stub synthesises `@N` as 100 + index, so lane t:3's
# target is `t:@103`. Which LANE was chosen is still asserted as an index -- see
# "the author's lane is named and skipped" below, which reads `skipping t:3`
# from dispatch.sh's own message. That split is #241's whole point and these
# assertions now carry it: the ledger keys on the slot, tmux is addressed by id.
want_contains "and lands on the first free lane, t:3 (target t:@103)" "send-keys -t t:@103" "$log"

# The authoring lane finishes and goes idle again -- exactly what makes it
# eligible for ordinary dispatch, and exactly the case #212 exists for: a
# lane that is free right now can still be the wrong lane for THIS review.
LEDGER_STATE="$D/state-212" ledger record-completion --task ad193-telegram-to-director --note done >/dev/null

out=$(LEDGER_STATE="$D/state-212" run 205 rev-204 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 204); rc=$?
want_exit "a review of PR #204 is still dispatched" "$rc" 0 "$out"
want_contains "the author's lane is named and skipped" "skipping t:3" "$out"
want_contains "the skip names the authoring task" "ad193-telegram-to-director" "$out"
log=$(tmuxlog)
want_contains "and the review lands on the OTHER free lane, t:4 (target t:@104)" "send-keys -t t:@104" "$log"
# The negative has to move to the id too, or it stops biting: after #241 no
# tmux call names `t:3` at all, so a `want_missing "-t t:3 "` would pass on a
# dispatch that landed squarely on the author.
want_missing "never on the author's lane (t:3, target t:@103)" "send-keys -t t:@103 " "$log"

# Now t:4 (from the review just dispatched) is the only thing standing
# between t:3 (free, but the author) and a refusal -- leave it occupied and
# confirm ANOTHER review of the same authoring issue is refused outright
# when the author is the only free lane, not silently sent anyway.
#
# agent-supervisor#159: a DIFFERENT PR number (207), not 204 again -- 204
# already has an open task (ad205-rev-204, deliberately left open so t:4
# stays occupied and "only t:3 free" holds, same as before this PR) and
# #159's own new duplicate-PR check would refuse a second dispatch of 204
# for THAT reason, before authorship is ever consulted -- a real and
# correct refusal, but not the one this case exists to prove. 207 closes
# the SAME issue (#193), so authorship still resolves the same way.
printf '207|Fixes #193|lane/193-telegram-to-director\n' >> "$D/prs"
out=$(LEDGER_STATE="$D/state-212" run 206 rev-207-again "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 207); rc=$?
want_exit "a review refused when the only free lane is the author" "$rc" 1 "$out"
want_contains "names the PR" "PR #207" "$out"
want_contains "names the authoring task, not just the lane" "ad193-telegram-to-director" "$out"
if [ -z "$(assignees 206)" ]; then ok "the refused review takes no claim on its own issue"
else bad "the refused review takes no claim on its own issue" "still assigned: $(assignees 206)"; fi

# --- agent-supervisor#190: a FIX-PASS lane is excluded too, not just the -
# original author -----------------------------------------------------
#
# WHY: this issue's own live evidence. A lane wrote a fix pass for a PR
# under a SEPARATE tracking issue (the review-findings issue, #178 here --
# not the PR's own originating issue, #186) and was later free to be handed
# that PR's re-review. The single-author lookup, resolved by the PR's own
# issue (#186), never sees a task filed under a DIFFERENT issue at all --
# and the WORKTREE fallback that COULD have caught it (the fix-pass task's
# worktree was checked out on the exact branch under review) used to run
# ONLY when the issue-based lookup came up silent. #186's own author WAS
# findable there, so the worktree fallback never ran, and the fix-pass
# lane's contribution went unchecked.
#
# Modelled with two REAL dispatches (via `run()`, not a hand-built ledger
# row) so the worktree-on-branch state is the real thing dispatch.sh's own
# `git worktree list` will see, the same technique #117's own test above
# uses: the fix-pass worktree is renamed onto the PR's actual branch after
# the original author's own worktree is torn down -- exactly what happens
# when a lane's worktree is replaced by its next dispatch.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
printf '186|| the code PR #460 was written from\n' >> "$D/issues"
# Deliberately NOT the word "review" anywhere in this title (#70's own
# inference triggers on "review" next to a PR number) -- this setup dispatch
# is a fix pass, not a review, and must not be mistaken for one.
printf '178|| apply findings from PR #460 into a follow-up commit\n' >> "$D/issues"

out=$(LEDGER_STATE="$D/state-190" run 186 original-fix "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the authoring dispatch (#186) succeeds" "$rc" 0 "$out"
WT_186=$(sed -n 's/^  worktree: //p' <<<"$out")
# HARD abort, not a logged `bad` that lets execution fall through: every git
# command below this point targets `$WT_186`/`$WT_178` with `-C`, and `git -C
# ""` silently operates on the CALLER's cwd instead of erroring -- an empty
# path here previously renamed THIS TEST SUITE's own real working branch
# (measured directly: agent-supervisor#190's own dev branch got renamed by an
# earlier, unguarded version of this exact test). A `[ -d ]` check alone is
# not enough; nothing past this line may run against a path that turned out
# to be empty.
if [ -z "$WT_186" ] || [ ! -d "$WT_186" ]; then
  bad "setup: the authoring dispatch printed a real worktree path" "got: '$WT_186' from: $out"
  echo "ABORTING the #190 section -- refusing to run 'git -C \"\$WT_186\" ...' against an empty/missing path" >&2
  WT_186=""
fi

if [ -n "$WT_186" ]; then
  # t:3's task is left OPEN (not completed yet) so the fix-pass below cannot
  # land back on t:3 -- it must go to t:4, a genuinely different lane.
  out=$(LEDGER_STATE="$D/state-190" run 178 fix186 "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
  want_exit "setup: the fix-pass dispatch (#178, a DIFFERENT issue) succeeds" "$rc" 0 "$out"
  want_contains "setup: the fix pass landed on t:4, not the author's t:3" "send-keys -t t:@104" "$(tmuxlog)"
  WT_178=$(sed -n 's/^  worktree: //p' <<<"$out")
  if [ -z "$WT_178" ] || [ ! -d "$WT_178" ]; then
    bad "setup: the fix-pass dispatch printed a real worktree path" "got: '$WT_178' from: $out"
    echo "ABORTING the #190 section -- refusing to run 'git -C \"\$WT_178\" ...' against an empty/missing path" >&2
    WT_186=""
  fi
fi

if [ -n "$WT_186" ]; then
  # The original author's task completes and its worktree is torn down --
  # exactly what its OWN next dispatch would do, simulated directly since
  # this test never redispatches t:3.
  LEDGER_STATE="$D/state-190" ledger record-completion --task ad186-original-fix --note done >/dev/null
  git -C "$REPO" worktree remove --force "$WT_186"
  # `worktree remove` only detaches the worktree -- the branch it was on
  # survives until deleted separately, and a `branch -m` onto that name below
  # would otherwise fail with "a branch named ... already exists".
  git -C "$REPO" branch -D lane/186-original-fix

  # The fix-pass worktree takes over the PR's branch -- the same "renamed to
  # a slug of the lane's own choosing" move #117's test above performs,
  # except here it lands on the EXACT branch already under review rather
  # than an unrelated one, because a fix pass pushes onto the SAME PR.
  git -C "$WT_178" branch -m lane/186-original-fix
  LEDGER_STATE="$D/state-190" ledger record-completion --task ad178-fix186 --note done >/dev/null

  printf '460|Fixes #186|lane/186-original-fix\n' >> "$D/prs"
  printf '211|| re-review PR #460 after the fix pass\n' >> "$D/issues"

  cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
5|free-5|claude.exe|❯ ready|1|0
FIX
  out=$(LEDGER_STATE="$D/state-190" run 211 rerev-460 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 460); rc=$?
  want_exit "a review of PR #460 is still dispatched, after excluding BOTH contributors" "$rc" 0 "$out"
  want_contains "the ORIGINAL author's lane (t:3) is skipped" "skipping t:3" "$out"
  want_contains "and names its task" "ad186-original-fix" "$out"
  want_contains "the FIX-PASS lane (t:4) is ALSO skipped -- agent-supervisor#190's own defect" "skipping t:4" "$out"
  want_contains "and names the fix-pass task, not just the original author's" "ad178-fix186" "$out"
  log=$(tmuxlog)
  want_contains "and the review lands on the one lane that never touched this PR, t:5" "send-keys -t t:@105" "$log"
  want_missing "never on the original author's lane (t:3, target t:@103)" "send-keys -t t:@103 " "$log"
  want_missing "never on the fix-pass lane (t:4, target t:@104)" "send-keys -t t:@104 " "$log"

  # MUTATION: run the EXACT SAME scenario through the pre-#190 dispatch.sh --
  # the widening reverted, not a synthetic patch -- and confirm it goes red.
  # Read at `merge-base HEAD origin/main`, NOT literal `HEAD`: this test's
  # own fix is committed on this very branch, so `HEAD` means "after the
  # widening" the moment that commit lands, and `git show HEAD:...` would
  # silently fetch the FIXED script instead of the one it means to revert
  # (measured directly: this exact test read its own fix back once the
  # commit landed). The merge-base is the shared ancestor with main -- the
  # pre-widening script -- regardless of how many commits sit on top here.
  #
  # `origin/main` is not guaranteed to already resolve. A CI checkout of a
  # single branch/PR ref leaves no local `origin/main` at all (agent-supervisor
  # #201: this failed exit-128 in CI, "Not a valid object name origin/main",
  # while passing on every dev machine that happened to have a full clone --
  # the second sighting of the shape PR #194's reviewer had already set aside
  # once, wrongly, as a local-clone artifact). Resolve it ourselves: use an
  # already-resolvable ref if there is one, else fetch main with an explicit
  # refspec (a bare `fetch origin main` on a single-branch clone updates
  # FETCH_HEAD only, never `refs/remotes/origin/main`, and would look like a
  # no-op success while changing nothing -- measured directly here). If the
  # ref genuinely cannot be produced, this SKIPS the mutation check with a
  # stated reason instead of crashing the whole suite or silently passing it.
  MUTATED_190="$D/dispatch-pre190.sh"
  patch_rc=0
  python3 - "$HERE/../../scripts/supervisor/dispatch.sh" "$MUTATED_190" <<'PY' || patch_rc=$?
import os
import subprocess
import sys

dst = sys.argv[2]
repo_dir = os.path.dirname(os.path.abspath(sys.argv[1]))


def git(*args):
    return subprocess.run(["git", "-C", repo_dir, *args], capture_output=True, text=True)


def resolves(ref):
    return git("rev-parse", "--verify", "-q", ref).returncode == 0


target = next((ref for ref in ("origin/main", "main") if resolves(ref)), None)

if target is None:
    fetch = git("fetch", "-q", "origin", "main:refs/remotes/origin/main")
    if fetch.returncode == 0 and resolves("origin/main"):
        target = "origin/main"
    else:
        print(
            "SKIP: no origin/main ref, and fetching one failed -- "
            f"{fetch.stderr.strip() or 'no route to the remote'}",
            file=sys.stderr,
        )
        sys.exit(3)

mb = git("merge-base", "HEAD", target)
if mb.returncode != 0 and git("rev-parse", "--is-shallow-repository").stdout.strip() == "true":
    # A shallow checkout's own history may not reach far enough back to share
    # an ancestor with main even once the ref exists -- unshallow once, then
    # give the merge-base one more try before giving up.
    git("fetch", "-q", "--unshallow", "origin")
    mb = git("merge-base", "HEAD", target)

if mb.returncode != 0:
    print(
        f"SKIP: git merge-base HEAD {target} failed even after fetch/unshallow: "
        f"{mb.stderr.strip()}",
        file=sys.stderr,
    )
    sys.exit(3)

base_ref = mb.stdout.strip()

# agent-supervisor#234: `base_ref` (the merge-base with origin/main) is only
# pre-#190 while #190's own fix has not yet reached main. The moment it
# merges, #190's landing commit itself becomes reachable from origin/main
# forever after -- so for every branch cut from that point on, the
# merge-base IS AT OR AFTER the fix, and `git show base_ref:...` silently
# fetches the ALREADY-FIXED script (measured directly: this is exactly what
# happened once #190 (e30697e) became this repo's own main tip -- the
# merge-base computed above resolved to e30697e itself). Walk dispatch.sh's
# own history backward from base_ref, newest first, until finding a
# revision that predates the widening -- identified by the absence of a
# marker unique to #190's diff, not by any commit message or SHA, so this
# keeps working the same way pre-merge (base_ref itself lacks the marker,
# so the loop uses it unchanged on its first pass) and post-merge alike.
#
# agent-supervisor#725: #720 split dispatch.sh into a composition root plus
# sourced siblings, and AUTHOR_LANES=() moved out of dispatch.sh itself into
# dispatch-guards.sh. Every revision of dispatch.sh from #720 onward -- even
# ones that fully carry #190's fix via the sourced sibling -- therefore
# lacks the marker in dispatch.sh's OWN text, and the walk below picked the
# CURRENT (correct, already-fixed) dispatch.sh on its very first iteration,
# mistaking it for the pre-#190 baseline: both mutation assertions then
# exercised the real, unmutated guard and failed as "still working" instead
# of "reverted". Same class as #706's core.py split breaking the OTHER
# mutation in this file (see the core*.py glob a few hundred lines below) --
# a marker search must follow code across a split, not assume it stayed in
# one named file. Check dispatch-guards.sh at the SAME revision too (it may
# not exist yet at a pre-#720 revision -- `git show` exiting non-zero there
# just means "nothing to find", not an error) and only call the widening
# absent when the marker is in neither file.
marker = "AUTHOR_LANES=()"


def content_at(rev, path):
    result = subprocess.run(
        ["git", "-C", repo_dir, "show", f"{rev}:{path}"],
        capture_output=True, text=True,
    )
    return result.stdout if result.returncode == 0 else None


# NOTE: unlike `<rev>:<path>` above (always root-relative), a `git log --
# <pathspec>` path is resolved relative to `-C`'s directory -- `repo_dir` IS
# `scripts/supervisor` already, so the pathspec here is just the filename,
# not the repo-root-relative `scripts/supervisor/dispatch.sh` used above.
history = subprocess.run(
    ["git", "-C", repo_dir, "log", "--format=%H", base_ref, "--", "dispatch.sh"],
    check=True, capture_output=True, text=True,
).stdout.split()

text = None
for rev in history:
    candidate = content_at(rev, "scripts/supervisor/dispatch.sh")
    guards_candidate = content_at(rev, "scripts/supervisor/dispatch-guards.sh")
    widened = (candidate is not None and marker in candidate) or (
        guards_candidate is not None and marker in guards_candidate
    )
    if candidate is not None and not widened:
        text = candidate
        break

if text is None:
    print(
        "SKIP: every revision of dispatch.sh reachable from the merge-base "
        "already has the #190 widening -- no pre-#190 baseline exists in "
        "this history to mutate",
        file=sys.stderr,
    )
    sys.exit(3)

here = 'HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"'
assert text.count(here) == 1, "HERE assignment not found or not unique -- pre-#190 script shape unexpected"
# The candidate must be self-contained -- a revision that genuinely predates
# both #190 and #720 -- since it is about to be dropped in as a single
# standalone file with no sourced siblings alongside it. If a future split
# ever moves other logic out of dispatch.sh without also moving the marker,
# this catches it as a shape assertion rather than a silent no-op mutation.
assert "dispatch-guards.sh" not in text, (
    "picked dispatch.sh revision sources dispatch-guards.sh but lacks the "
    "AUTHOR_LANES marker in either file -- shape assumption broken, refusing "
    "to write a standalone mutant that can't actually run"
)
text = text.replace(here, 'HERE=%r' % repo_dir, 1)
open(dst, "w").write(text)
PY
  if [ "$patch_rc" -eq 3 ]; then
    echo "  SKIP agent-supervisor#190 mutation check: pre-#190 baseline could not be resolved (see stderr above) -- UNVERIFIED, not a pass"
  elif [ "$patch_rc" -ne 0 ]; then
    bad "setup: fetched the pre-#190 dispatch.sh from git HEAD" \
      "could not fetch/patch (exit $patch_rc) -- treating as a failure, not a skip"
  else
    ok "setup: fetched the pre-#190 dispatch.sh from git HEAD"
    chmod +x "$MUTATED_190"
    # The correct dispatch above already recorded PR #460 as open, under
    # ad211-rerev-460. That write-time PR-dedup gate (agent-supervisor#159,
    # landed after #190's own branch point) lives in the ledger, not in
    # dispatch.sh, so swapping in the pre-#190 SCRIPT does not revert it --
    # the mutant would collide with its OWN earlier claim and refuse before
    # ever reaching the code this section means to exercise, reporting a
    # false red unrelated to #190. Release that claim first: this section
    # is re-running the identical scenario, not proving the PR is still
    # open.
    LEDGER_STATE="$D/state-190" ledger record-completion --task ad211-rerev-460 --note done >/dev/null
    cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
5|free-5|claude.exe|❯ ready|1|0
FIX
    printf '212|| re-review PR #460 against the pre-#190 guard\n' >> "$D/issues"
    out=$(DISPATCH_SCRIPT="$MUTATED_190" LEDGER_STATE="$D/state-190" \
          run 212 rerev-460-mutant "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 460); rc=$?
    want_exit "mutation confirmed: the pre-#190 script still dispatches (it never saw the fix-pass lane)" "$rc" 0 "$out"
    want_missing "mutation confirmed: the fix-pass lane is NOT named as skipped (the assertion above would now be red)" \
      "skipping t:4" "$out"
    log=$(tmuxlog)
    want_contains "mutation confirmed: the review lands on the fix-pass's own lane, t:4 (target t:@104) -- the exact defect #190 reports" \
      "send-keys -t t:@104" "$log"
  fi
else
  bad "the agent-supervisor#190 fix-pass-contributor section" \
    "skipped entirely -- an earlier setup step could not produce a real worktree path"
fi

# --- agent-supervisor#190: fail closed when the contributor set itself
# cannot be resolved -----------------------------------------------------
#
# WHY (#124/#126): an unresolvable question must make a lane LESS
# dispatchable, never more. If the ledger cannot say who contributed to a
# PR at all, this must refuse the whole dispatch -- exactly step 0.5's
# existing single-author refusal, just restated for the wider question. No
# separate code path exists for this; it is the same "still silent ->
# refuse" step 4 the single-author case already used, so this proves it
# still fires now that step 4 asks about a SET rather than one lane.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '213|| review of a PR the ledger has never heard of\n' >> "$D/issues"
printf '461|Fixes #920|some-hand-pushed-branch\n' >> "$D/prs"
out=$(LEDGER_STATE="$D/state-190-closed" run 213 rev-461 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 461); rc=$?
want_exit "an unresolvable contributor set refuses the whole dispatch" "$rc" 1 "$out"
want_contains "and says why" "authorship unknown" "$out"
log=$(tmuxlog)
want_missing "nothing was sent" "send-keys" "$log"

# --- agent-supervisor#79: cancelled rows still identify the PR author ----
#
# `cancel-open-task` is the manual reconciliation hammer for a lane that is
# idle again but still ledger-held. It must free the lane without erasing who
# wrote the PR; otherwise the review guard has no author to exclude and can
# hand the review back to the lane that authored it.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
printf '279|| author row later cancelled for reconciliation\n' >> "$D/issues"
printf '280|| review PR #279 after cancel-open-task\n' >> "$D/issues"
printf '279|Fixes #279|chore/279-cancel-auth\n' >> "$D/prs"

out=$(LEDGER_STATE="$D/state-79" run 279 cancel-auth "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the authoring dispatch (#279) succeeds" "$rc" 0 "$out"
log=$(tmuxlog)
want_contains "setup: the authoring dispatch lands on t:3 (target t:@103)" "send-keys -t t:@103" "$log"
LEDGER_STATE="$D/state-79" ledger cancel-open-task --lane t:3 --abandoned >/dev/null

out=$(LEDGER_STATE="$D/state-79" run 280 rev-279-after-cancel "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 279); rc=$?
want_exit "a review after cancel-open-task is still dispatched to a non-author lane" "$rc" 0 "$out"
want_contains "the cancelled author row is still found and skipped" "skipping t:3" "$out"
want_contains "the skip names the cancelled authoring task" "ad279-cancel-auth" "$out"
log=$(tmuxlog)
want_contains "and the review lands on the OTHER free lane, t:4 (target t:@104)" "send-keys -t t:@104" "$log"
want_missing "never on the cancelled author's lane (t:3, target t:@103)" "send-keys -t t:@103 " "$log"

# --- agent-supervisor#90: completed rows still identify the PR author ----
#
# `record-completion` is not a manual reconciliation hammer like
# `cancel-open-task` (#79) -- the supervisor runs it on EVERY lane, EVERY
# tick, as routine housekeeping the instant a worker's channel fires
# (`lane-done.sh`). #90's own incident: two ticks correctly refused a
# self-review while the author's task read `delivered`; the very next tick,
# after `record-completion` had closed that task, the SAME dispatch landed
# on the author. A guard that a normal tick's housekeeping can turn off is
# not a guard that holds in the steady state.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
printf '390|| author row later closed by routine record-completion\n' >> "$D/issues"
printf '392|| review PR #390 after record-completion\n' >> "$D/issues"
# The branch slug ("public-close") is deliberately NOT the authoring
# dispatch's own slug ("close-auth", task ad390-close-auth) -- same
# divergence #35's own chore/ cases use, and for the same reason: it forces
# resolution through the LEDGER'S ISSUE lookup (`get_author_task_for_issue`),
# not the branch-name fallback (`task-lane`/`get_task`, which never filters
# by status either and would otherwise paper over exactly the regression the
# mutation below proves).
printf '390|Fixes #390|lane/390-public-close\n' >> "$D/prs"

out=$(LEDGER_STATE="$D/state-90" run 390 close-auth "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the authoring dispatch (#390) succeeds" "$rc" 0 "$out"
log=$(tmuxlog)
want_contains "setup: the authoring dispatch lands on t:3 (target t:@103)" "send-keys -t t:@103" "$log"
# #212's own block already proves "task still open -> still refused"; this
# section is only about the ONE thing #90 adds: the SAME PR's review must
# still be refused after `record-completion` closes that task -- exactly the
# routine reconciliation step every tick runs, not a hand-typed recovery
# command.
LEDGER_STATE="$D/state-90" ledger record-completion --task ad390-close-auth --note "lane-done: routine reconciliation" >/dev/null

out=$(LEDGER_STATE="$D/state-90" run 392 rev-390-after-complete "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 390); rc=$?
want_exit "a review after record-completion is still dispatched to a non-author lane" "$rc" 0 "$out"
want_contains "the completed author row is still found and skipped" "skipping t:3" "$out"
want_contains "the skip names the completed authoring task" "ad390-close-auth" "$out"
log=$(tmuxlog)
want_contains "and the review lands on the OTHER free lane, t:4 (target t:@104)" "send-keys -t t:@104" "$log"
want_missing "never on the completed author's lane (t:3, target t:@103)" "send-keys -t t:@103 " "$log"

# The only-free-lane variant: t:4 now busy with the review just dispatched,
# t:3 (free, but the author, and its task is COMPLETE not merely idle) is
# the only other candidate -- must still refuse outright, not dispatch.
#
# agent-supervisor#159: PR 391, not 390 again -- 390 already has an open
# task (ad392-rev-390-after-complete, deliberately left open so t:4 stays
# occupied) and #159's own duplicate-PR check would refuse a second 390
# dispatch for THAT reason first. 391 closes the same issue (#390).
printf '391|Fixes #390|lane/390-public-close\n' >> "$D/prs"
printf '393|| review PR #391, only the completed author is free\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-90" run 393 rev-391-only-author-free "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 391); rc=$?
want_exit "a review refused when the only free lane is the completed author" "$rc" 1 "$out"
want_contains "names the completed authoring task, not just the lane" "ad390-close-auth" "$out"

# --- MUTATION: an "only open tasks" author lookup -- the bug #90 reported -
# The three assertions above are only worth anything if a lookup that DOES
# forget authorship on completion turns them red. Built the same way as
# every other mutation in this file (#184, #192, #241): a shadow copy of the
# WHOLE scripts/supervisor directory, symlinked, with exactly one file
# actually patched -- so nothing except the named defect can be responsible
# for the result. `core.py` here, not `dispatch.sh`: `get_author_task_for_issue`
# is the single function #77's own comment says both `dispatch.sh` and
# `digest.sh` share for this -- see this brief's own note to reuse it -- so
# patching it once proves the guard's actual dependency, not a shell-only
# stand-in for it.
SHADOW90="$D/shadow-supervisor-90"
rm -rf "$SHADOW90"; mkdir -p "$SHADOW90"
for f in "$HERE/../../scripts/supervisor/"*; do ln -s "$f" "$SHADOW90/$(basename "$f")"; done
# The glob above also symlinked __pycache__ -- straight back at the REAL
# scripts/supervisor/__pycache__, which already holds a compiled .pyc of
# whichever module below gets mutated. Left in place, `python3
# $SHADOW90/cli.py` resolves `import core`/the mixin modules against that
# cache before ever reading the mutant file written below, and the mutation
# test passes for the wrong reason: nothing ran the mutated code at all.
# Only ever discard the SYMLINK here, never the real directory it points at.
rm -f "$SHADOW90/__pycache__"
# `cli.py` also cannot stay a symlink (unlike dispatch.sh, which is bash and
# resolves its own directory logically via `cd`+`pwd`): CPython computes
# `sys.path[0]` from the REALPATH of the script it was handed, so
# `python3 $SHADOW90/cli.py` -- if cli.py is only a symlink -- puts the REAL
# scripts/supervisor directory on sys.path, not $SHADOW90, and `import core`
# silently picks up the unmutated original sitting there instead of the
# mutant written below. Measured directly: it answered `known:true` for a
# completed author with the symlink, `known:false` (correctly refusing) with
# this copy. A real file's own directory is never resolved away.
rm -f "$SHADOW90/cli.py"
cp "$HERE/../../scripts/supervisor/cli.py" "$SHADOW90/cli.py"
patch_rc=0
CORE_MUTANT=$(python3 - "$HERE/../../scripts/supervisor" "$SHADOW90" <<'PY'
import sys
from pathlib import Path

src_dir, shadow = Path(sys.argv[1]), Path(sys.argv[2])
marker = (
    "                WHERE source_tasks.source_kind = 'issue' AND source_tasks.source_ref = ?\n"
    "                ORDER BY tasks.created_at ASC, tasks.id ASC\n"
)
# agent-supervisor#706 split core.py's Ledger into mixins under
# core_ledger_*.py -- get_author_task_for_issue's query now lives in
# core_ledger_task_queries.py, not core.py. Search the whole core*.py
# module set rather than one named file, so a future re-split doesn't
# silently stop mutating anything: require exactly one match TOTAL across
# the set (a clause could be unique per-file yet duplicated across files)
# and patch whichever file actually has it.
core_modules = sorted(src_dir.glob("core*.py"))
hits = [(p, p.read_text().count(marker)) for p in core_modules]
total = sum(n for _, n in hits)
assert total == 1, (
    "get_author_task_for_issue's query not found or not unique across "
    f"core*.py -- script shape changed (per-file counts: {hits})"
)
target = next(p for p, n in hits if n == 1)
text = target.read_text()
mutated = marker.replace(
    "ORDER BY",
    "AND tasks.status NOT IN ('complete', 'failed', 'cancelled')\n                ORDER BY",
)
mutated_text = text.replace(marker, mutated, 1)
assert mutated_text != text, f"mutation did not change {target.name}"
# The symlink for the target module points at the REAL file -- writing
# through it would mutate the actual repo source. Remove the symlink first,
# same reasoning as cli.py above, then write a real, mutated copy in its
# place.
dst = shadow / target.name
dst.unlink()
dst.write_text(mutated_text)
print(dst)
PY
) || patch_rc=$?
if [ "$patch_rc" -ne 0 ] || [ -z "$CORE_MUTANT" ]; then
  bad "setup: patched a copy of core.py whose author lookup considers only open tasks" \
    "could not patch core.py (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of core.py whose author lookup considers only open tasks"
  MUTANT_DISPATCH="$SHADOW90/dispatch.sh"
  # Re-run the EXACT scenario the "review after record-completion" assertions
  # above were built on (same lanes, same completed author, same PR) through
  # the mutant instead of the real ledger. dispatch.sh's own fail-closed rule
  # (an unresolved author refuses the WHOLE dispatch, never proceeds as if
  # innocent) means this mutation cannot reproduce #90's incident as a
  # WRONGFUL DISPATCH -- it shows up as a wrongful REFUSAL instead: a
  # legitimate review of a merged PR now gets turned away with "authorship
  # unknown", because the one row that proves who wrote it just stopped
  # counting the moment it finished. Either direction is a real defect (a
  # guard that refuses reviews it has no business refusing is not safe to
  # operate), and either one is what "test 1 goes red" means here: none of
  # the outcomes the passing assertions above depend on -- skipping t:3 by
  # name, landing on t:4 -- survive against this mutant.
  cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
  printf '394|| review PR #390 against the only-open-tasks mutant\n' >> "$D/issues"
  out=$(LEDGER_STATE="$D/state-90" DISPATCH_SCRIPT="$MUTANT_DISPATCH" \
    run 394 rev-390-mutant "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 390); rc=$?
  log=$(tmuxlog)
  if [ "$rc" = 0 ] && grep -qF "skipping t:3" <<<"$out" && grep -qF "send-keys -t t:@104" <<<"$log"; then
    bad "mutation confirmed: the only-open-tasks lookup breaks test 1's outcome (the assertions above would now be red)" \
      "the mutant reproduced the SAME outcome as the real ledger -- this mutation proves nothing: rc=$rc out=$out log=$log"
  else
    ok "mutation confirmed: the only-open-tasks lookup breaks test 1's outcome (the assertions above would now be red): rc=$rc out=$(head -c 160 <<<"$out")"
  fi
fi

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
