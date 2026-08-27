#!/bin/bash
# agent-supervisor#117: resolve authorship by the WORKTREE a PR's branch
# came from, when the branch name and ledger issue lookup both come up
# empty -- with its own mutation check. agent-supervisor#101: an inferred
# review must stay escapable (explicit flags override the inference)
# without becoming a silent bypass of the authorship guard.
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

echo "dispatch.sh -- resolving authorship by worktree when no other signal names it (agent-supervisor#117/#101)"

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

# --- agent-supervisor#117: resolve authorship by the WORKTREE, when the
# PR's branch shares no text with the dispatch slug and the ledger's issue
# lookup has nothing to go on either -------------------------------------
#
# WHY: the measured incident. Task `as101-reviewspr-inference` produced PR
# branch `fix/101-not-a-review-escape` -- reconstructing a task id from that
# branch name (`${PREFIX}101-not-a-review-escape`) never matches the real
# task id, so the old fallback refused a review the ledger could actually
# answer. This reproduces the divergence directly: dispatch a real task,
# then RENAME its real worktree's branch (the way a lane renames its
# checkout to satisfy the type-prefix convention with a slug of its own
# choosing) to something sharing no text with the dispatch slug, and give
# the review a PR whose "Fixes #<N>" line names an issue the ledger has NO
# record of at all -- so the issue-keyed lookup (steps 1/2) is silenced on
# purpose, and only a worktree-based lookup can resolve this.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
printf '101|| the code PR #600 was written from\n' >> "$D/issues"
printf '102|| review PR #600, branch renamed away from the dispatch slug\n' >> "$D/issues"

out=$(LEDGER_STATE="$D/state-117" run 101 pr-inference-fix "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the authoring dispatch (#101) succeeds" "$rc" 0 "$out"
WT_117=$(sed -n 's/^  worktree: //p' <<<"$out")
if [ -z "$WT_117" ] || [ ! -d "$WT_117" ]; then
  bad "setup: the authoring dispatch printed a real worktree path" "got: '$WT_117' from: $out"
else
  ok "setup: the authoring dispatch printed a real worktree path"
fi
# The rename itself: same worktree, a branch name sharing no text with
# "101-pr-inference-fix" -- exactly what a lane does to satisfy the
# type-prefix convention with its own descriptive slug.
git -C "$WT_117" branch -m "fix/101-not-a-review-escape"

# The PR's own "Fixes #<N>" deliberately names an issue (999) nothing in
# this ledger was ever dispatched for -- steps 1/2 (the issue-keyed lookup)
# must come up silent, so only the worktree-based fallback can resolve this.
printf '600|Fixes #999|fix/101-not-a-review-escape\n' >> "$D/prs"

out=$(LEDGER_STATE="$D/state-117" run 102 rev-600 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 600); rc=$?
want_exit "a review of PR #600 is still dispatched, resolved by worktree not branch text" "$rc" 0 "$out"
want_contains "the author's lane is named and skipped" "skipping t:3" "$out"
want_contains "the skip names the real authoring task, not a reconstruction from the branch" \
  "ad101-pr-inference-fix" "$out"
want_missing "never the old fallback's wrong reconstruction" "ad101-not-a-review-escape" "$out"
log=$(tmuxlog)
want_contains "and the review lands on the OTHER free lane, t:4 (target t:@104)" "send-keys -t t:@104" "$log"
want_missing "never on the author's lane (t:3, target t:@103)" "send-keys -t t:@103 " "$log"

# The only-free-lane variant: the author must still be refused even when it
# is the only candidate, same as every other authorship path.
#
# agent-supervisor#159: a DIFFERENT PR number (601), not 600 again -- 600
# already has an open task (ad102-rev-600, still delivered, deliberately
# left open so t:4 stays occupied and "only t:3 free" holds, same as before
# this PR) and #159's own new duplicate-PR check would refuse a second
# dispatch of 600 for THAT reason, before authorship is ever consulted --
# a real and correct refusal, but not the one this case exists to prove.
# 601 shares the same "Fixes #999" / renamed-branch shape so authorship
# still resolves by WORKTREE, exactly as this block is about.
printf '601|Fixes #999|fix/101-not-a-review-escape\n' >> "$D/prs"
printf '103|| review PR #601 again, only the author is free\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-117" run 103 rev-601-only-author "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 601); rc=$?
want_exit "a review refused when the only free lane is the author, resolved by worktree" "$rc" 1 "$out"
want_contains "names the real authoring task" "ad101-pr-inference-fix" "$out"

# MUTATION-CHECK: silence the worktree-based lookup (`worktree-lane` always
# reads unknown) and confirm the SAME scenario goes red -- the divergent
# branch means the legacy branch-name fallback cannot pick up the slack
# either, so this must go from "dispatched, author skipped" to "refused,
# authorship unknown".
MUTANT_DIR_117=$(make_mutant_scripts_dir)
MUTATED_117="$MUTANT_DIR_117/dispatch.sh"
patch_rc=0
python3 - "$MUTANT_DIR_117/resolve-pr-contributors.sh" <<'PY' || patch_rc=$?
import sys
target = sys.argv[1]
text = open(target).read()
marker = 'worktree_json=$("$ledger_python" "$ledger_cli" worktree-lane --path "$matched_worktree" 2>&1)'
assert text.count(marker) == 1, "worktree-lane lookup not found or not unique -- script shape changed"
text = text.replace(marker, 'worktree_json=\'{"known":false}\'  # MUTATED: worktree-lane never consulted', 1)
open(target, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh whose worktree-lane lookup is silenced" \
    "could not patch $MUTANT_DIR_117/resolve-pr-contributors.sh (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh whose worktree-lane lookup is silenced"
  chmod +x "$MUTATED_117"
  cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
  printf '104|| review PR #600 against the mutated guard\n' >> "$D/issues"
  out=$(DISPATCH_SCRIPT="$MUTATED_117" LEDGER_STATE="$D/state-117" \
        run 104 rev-600-mutant "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 600); rc=$?
  want_exit "mutation confirmed: with worktree-lane silenced, the same review now refuses" "$rc" 1 "$out"
  want_contains "mutation confirmed: back to authorship unknown (the assertions above would now be red)" \
    "authorship unknown" "$out"
fi

# --- agent-supervisor#101: an inferred review must be escapable without
# rewording the brief --------------------------------------------------------
#
# WHY: #70's inference reads prose, and prose ABOUT a PR is not a review OF
# that PR. Measured on this estate: a rebase dispatch whose brief said
# "rebase it so it can be reviewed" next to "PR #93" was read as a review of
# #93 and then refused on authorship grounds -- for a task where authorship is
# irrelevant (a rebase by a non-author is normal). The operator escaped by
# rewording the brief, which teaches writing around the tool.
#
# The fix does NOT narrow detection: every narrowing available reads the same
# prose and would drop real reviews #70 catches today, which is the dangerous
# direction. It adds `--not-a-review`, said at the dispatch instead of in the
# brief. The cases below hold BOTH directions: the escape works, and with the
# escape absent the same brief is still inferred and still excludes the author.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
BRIEF_REBASE="$D/brief-rebase.md"
printf 'This branch conflicts with main. Rebase it so PR #510 can be reviewed.\n' > "$BRIEF_REBASE"
printf '259|| the code PR #510 was written from\n' >> "$D/issues"
printf '260|| rebase a conflicted branch\n' >> "$D/issues"
printf '510|Fixes #259|lane/259-escape-author\n' >> "$D/prs"

out=$(LEDGER_STATE="$D/state-101" run 259 escape-author "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the authoring dispatch (#259) succeeds" "$rc" 0 "$out"
LEDGER_STATE="$D/state-101" ledger record-completion --task ad259-escape-author --note done >/dev/null

# RED FIRST #1 -- the reported defect. The brief merely MENTIONS PR #510 on a
# line that also says "reviewed"; the dispatch is a rebase, and the only free
# lane is the branch's own author, which is fine for a rebase. `--not-a-review`
# must let it through untouched.
out=$(LEDGER_STATE="$D/state-101" run 260 rebase-510 "$BRIEF_REBASE" acme/agent-dotfiles "$REPO" --not-a-review); rc=$?
want_exit "--not-a-review lets a non-review brief that mentions a PR proceed" "$rc" 0 "$out"
want_missing "nothing was inferred under --not-a-review" "inferred --reviews-pr" "$out"
want_missing "and no authorship refusal was reached" "authorship unknown" "$out"
log=$(tmuxlog)
want_contains "the rebase lands on the branch's own author lane, t:3 (target t:@103)" "send-keys -t t:@103" "$log"
LEDGER_STATE="$D/state-101" ledger record-completion --task ad260-rebase-510 --note done >/dev/null

# RED FIRST #2 -- the same brief WITHOUT the escape is still inferred and
# still refuses the author's lane. This is the #70 behaviour the fix must not
# weaken: it is green before this change and green after it.
printf '266|| rebase a conflicted branch, no escape flag\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-101" run 266 rebase-510-noflag "$BRIEF_REBASE" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "without the escape, the same brief is still inferred and still refused" "$rc" 1 "$out"
want_contains "still says what it inferred" "inferred --reviews-pr 510 from the brief" "$out"
want_contains "and points at the escape rather than at the brief" "--not-a-review" "$out"

# A GENUINE review brief -- naming both "review" and the PR -- still infers
# the flag and still excludes the author. Same fixtures, review wording, no
# escape flag.
BRIEF_REVIEW_101="$D/brief-review-101.md"
printf 'Independent review of PR #510: correctness and merge readiness.\n' > "$BRIEF_REVIEW_101"
printf '261|| do the independent review\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-101" run 261 rev-510 "$BRIEF_REVIEW_101" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a genuine review brief is still inferred and still refused to its author" "$rc" 1 "$out"
want_contains "names the inferred PR" "inferred --reviews-pr 510 from the brief" "$out"
want_contains "names the authoring task" "ad259-escape-author" "$out"

# ...and with a second free lane it still lands on the NON-author, i.e. the
# guard is doing its job, not merely refusing everything.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
out=$(LEDGER_STATE="$D/state-101" run 261 rev-510 "$BRIEF_REVIEW_101" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "with another free lane, the genuine review is dispatched" "$rc" 0 "$out"
want_contains "the author's lane is skipped" "skipping t:3" "$out"
log=$(tmuxlog)
want_contains "and it lands on the other lane, t:4 (target t:@104)" "send-keys -t t:@104" "$log"
want_missing "never on the author's lane (t:3, target t:@103)" "send-keys -t t:@103 " "$log"
LEDGER_STATE="$D/state-101" ledger record-completion --task ad261-rev-510 --note done >/dev/null

# The issue's third red-first item: inference fires AND authorship is
# unresolvable -- today those two findings arrive together and read as one
# failure about authorship. PR #511's head branch carries a prefix the
# fallback does not read and closes an issue no lane authored, so authorship
# genuinely cannot be resolved.
printf '263|| review PR #511 please\n' >> "$D/issues"
printf '511|Fixes #262|hotfix/511-never-dispatched\n' >> "$D/prs"
out=$(LEDGER_STATE="$D/state-101" run 263 rev-511-unknown "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "an inferred review whose author is unresolvable still refuses" "$rc" 1 "$out"
want_contains "says authorship could not be determined" "could not determine PR #511's author" "$out"
want_contains "and separately says the review status was only INFERRED" "PR #511 was INFERRED from issue #263's title" "$out"
want_contains "and names the escape for the not-a-review case" "re-run with --not-a-review" "$out"

# The same dispatch, declared not-a-review, proceeds: an unresolvable author
# is not a question a non-review dispatch has to answer at all.
out=$(LEDGER_STATE="$D/state-101" run 263 rev-511-escaped "$D/brief.md" acme/agent-dotfiles "$REPO" --not-a-review); rc=$?
want_exit "the same dispatch under --not-a-review proceeds" "$rc" 0 "$out"
want_missing "the authorship question never arises" "could not determine" "$out"
LEDGER_STATE="$D/state-101" ledger record-completion --task ad263-rev-511-escaped --note done >/dev/null

# Both flags at once are contradictory statements about one dispatch. Refused
# before anything is claimed -- honouring either one silently would mean
# guessing which of two explicit answers the caller meant.
printf '267|| a dispatch that says both things at once\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-101" run 267 both-flags "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 510 --not-a-review); rc=$?
want_exit "--reviews-pr with --not-a-review is refused, not resolved" "$rc" 2 "$out"
want_contains "and says why" "contradict each other" "$out"
if [ -z "$(assignees 267)" ]; then ok "the contradictory dispatch claims nothing"
else bad "the contradictory dispatch claims nothing" "still assigned: $(assignees 267)"; fi

# MUTATION-CHECK: remove the `--not-a-review` arm from the argument scanner
# (the flag then falls through to POSITIONAL and sets nothing) and confirm the
# first case above goes red again -- the escape is what carries it, not some
# other change in this diff.
MUTATED_101="$D/dispatch-no-escape.sh"
patch_rc=0
python3 - "$DISPATCH" "$MUTATED_101" <<'PY' || patch_rc=$?
import os
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '    --not-a-review)\n'
assert marker in text, "--not-a-review arm not found -- script shape changed"
start = text.index(marker)
end = text.index('      ;;\n', start) + len('      ;;\n')
text = text[:start] + text[end:]
assert '--not-a-review)' not in text, "the flag's case arm survived the cut"
here = 'HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"'
assert text.count(here) == 1, "HERE assignment not found or not unique -- script shape changed"
text = text.replace(here, 'HERE=%r' % os.path.dirname(os.path.abspath(src)), 1)
open(dst, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh with --not-a-review removed" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh with --not-a-review removed"
  chmod +x "$MUTATED_101"
  cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
  printf '264|| the code PR #512 was written from\n' >> "$D/issues"
  printf '265|| rebase a conflicted branch, mutant\n' >> "$D/issues"
  printf '512|Fixes #264|lane/264-escape-mutant\n' >> "$D/prs"
  printf 'This branch conflicts with main. Rebase it so PR #512 can be reviewed.\n' > "$D/brief-rebase-mutant.md"
  LEDGER_STATE="$D/state-101-mutant" run 264 escape-mutant "$D/brief.md" acme/agent-dotfiles "$REPO" >/dev/null
  LEDGER_STATE="$D/state-101-mutant" ledger record-completion --task ad264-escape-mutant --note done >/dev/null
  out=$(DISPATCH_SCRIPT="$MUTATED_101" LEDGER_STATE="$D/state-101-mutant" \
        run 265 rebase-512 "$D/brief-rebase-mutant.md" acme/agent-dotfiles "$REPO" --not-a-review); rc=$?
  want_exit "mutation confirmed: without the escape arm, the rebase is refused again" "$rc" 1 "$out"
  want_contains "mutation confirmed: the flag was ignored and the review inferred" "inferred --reviews-pr 512" "$out"
fi

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
