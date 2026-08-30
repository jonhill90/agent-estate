#!/bin/bash
# agent-supervisor#308: item 2 is resolution path five, a --pr-scoped
# dispatch resolving authorship a different way than the issue-based paths
# above it; item 3 is "authored outside the lane system" -- an out-of-band
# agent whose branch closes no issue the ledger can even name
# (mark-pr-external); item 1 is the READ half of dispatch.sh step 2.1's own
# resolution logic.
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

echo "dispatch.sh -- PR-scoped resolution paths and external authorship (agent-supervisor#308)"

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

# --- agent-supervisor#308 item 2: resolution path five -- a `--pr`-scoped -
# fix-pass lane is a genuine contributor and must be excluded from later
# reviewing the SAME PR, even though it was never dispatched by ISSUE and
# its own worktree was never checked out on the PR's actual head branch.
#
# WHY: the motivating incident, reproduced directly. Two fix-pass lanes
# dispatched directly against PR #970 (`--pr 970`) sat in `source_tasks` as
# `source_kind='pull'` rows, invisible to the issue-keyed lookup (steps
# 1&2) -- and its worktree's branch (`worktree.sh new`'s own default,
# never renamed to the PR's real head branch, which in this shape belongs
# to nobody's worktree at all) cannot resolve it via step 3 either. So the
# fix-pass lane would read as a stranger and be handed the review of its
# own fix -- the exact #190 harm, on a path #190 could not see because #159
# (PR-scoped dispatch) did not exist yet when #190 shipped.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
6|free-6|claude.exe|❯ ready|1|0
FIX
printf '925|| the code a fix pass on PR #970 targets\n' >> "$D/issues"

out=$(LEDGER_STATE="$D/state-308a" run 925 original-970 "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the authoring dispatch (#925) succeeds" "$rc" 0 "$out"
want_contains "setup: it landed on the first free lane, t:3" "send-keys -t t:@103" "$(tmuxlog)"

# t:3's task is left OPEN (not completed yet) so the fix-pass below cannot
# land back on t:3 -- it must go to t:4, a genuinely different lane, the
# same technique agent-supervisor#190's own test above uses.
out=$(LEDGER_STATE="$D/state-308a" run 925 fix-970 "$D/brief.md" acme/agent-dotfiles "$REPO" --pr 970); rc=$?
want_exit "setup: the fix-pass dispatch (--pr 970) succeeds" "$rc" 0 "$out"
want_contains "setup: it landed on t:4, not the original author's t:3" "send-keys -t t:@104" "$(tmuxlog)"

# Both contributing tasks complete before the review below -- their lanes'
# panes will read idle/ready again, so what excludes them from the review
# candidate pool must be the CONTRIBUTOR lookup, not "still busy".
LEDGER_STATE="$D/state-308a" ledger record-completion --task ad925-original-970 --note done >/dev/null
LEDGER_STATE="$D/state-308a" ledger record-completion --task ad925-fix-970 --note done >/dev/null

# PR #970's real head branch belongs to NEITHER worktree -- it was written
# outside the lane system for the purposes of THIS PR, which the fix-pass
# lane pushed commits onto without ever checking it out itself. This
# isolates the assertion to the PR-number path: it cannot be satisfied by
# step 3 (worktree) or step 3.1 (legacy branch convention) by accident.
printf '970|Fixes #925|some-preexisting-branch-nobody-worktreed\n' >> "$D/prs"
printf '926|| review PR #970, must exclude BOTH contributors\n' >> "$D/issues"

out=$(LEDGER_STATE="$D/state-308a" run 926 rev-970 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 970); rc=$?
want_exit "a review of PR #970 dispatches, excluding every real contributor" "$rc" 0 "$out"
want_contains "the issue-keyed author (t:3) is skipped" "skipping t:3" "$out"
want_contains "the --pr-scoped fix-pass contributor (t:4) is ALSO skipped -- the #308 fix" "skipping t:4" "$out"
log=$(tmuxlog)
want_contains "and the review lands on the one lane that never touched this PR, t:6" "send-keys -t t:@106" "$log"
want_missing "never on the fix-pass lane's target (t:4, t:@104)" "send-keys -t t:@104 " "$log"

# The review dispatched above is still OPEN against PR #970 -- complete it
# first, or the unrelated agent-supervisor#169 PR-duplicate guard (step 0.6)
# refuses the mutation-check dispatch below before authorship is even asked.
LEDGER_STATE="$D/state-308a" ledger record-completion --task ad926-rev-970 --note done >/dev/null

# MUTATION-CHECK: silence the PR-scoped contributor lookup and confirm the
# fix-pass lane (t:4) is WRONGLY treated as available -- proving this test
# actually exercises the new path, not something step 1-3.1 already covered.
MUTANT_DIR_308A=$(make_mutant_scripts_dir)
MUTATED_308A="$MUTANT_DIR_308A/dispatch.sh"
patch_rc=0
python3 - "$MUTANT_DIR_308A/resolve-pr-contributors.sh" <<'PY' || patch_rc=$?
import sys
target = sys.argv[1]
text = open(target).read()
marker = 'pr_contrib_json=$("$ledger_python" "$ledger_cli" contributor-pr-lanes --pr "$pr" "${repo_args[@]+"${repo_args[@]}"}" 2>&1)'
assert text.count(marker) == 1, "contributor-pr-lanes lookup not found or not unique -- script shape changed"
text = text.replace(marker, 'pr_contrib_json=\'{"known":false}\'  # MUTATED: contributor-pr-lanes never consulted', 1)
open(target, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh whose contributor-pr-lanes lookup is silenced" \
    "could not patch $MUTANT_DIR_308A/resolve-pr-contributors.sh (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh whose contributor-pr-lanes lookup is silenced"
  chmod +x "$MUTATED_308A"
  cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
6|free-6|claude.exe|❯ ready|1|0
FIX
  printf '927|| review PR #970 again, against the mutated guard\n' >> "$D/issues"
  out=$(DISPATCH_SCRIPT="$MUTATED_308A" LEDGER_STATE="$D/state-308a" \
        run 927 rev-970-mutant "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 970); rc=$?
  want_exit "mutation confirmed: dispatch still succeeds (only issue-keyed author excluded)" "$rc" 0 "$out"
  want_missing "mutation confirmed: the fix-pass contributor is NO LONGER skipped -- it reads free" "skipping t:4" "$out"
fi

# --- agent-supervisor#308 item 3: "authored outside the lane system" is a --
# RECORDABLE, first-class state, distinct from "unresolvable" -- and NEVER
# inferred automatically from every path above coming up silent.
#
# WHY: the #316/#301/#300 shape -- a PR authored by a human or an
# out-of-band agent, closing no issue the ledger can even name, whose branch
# fails the legacy `<prefix>/<issue>-<slug>` convention outright. RED FIRST:
# every resolution path (1-3.1) is silent for this PR, and today that
# refuses, indistinguishably from a genuinely unresolvable case.
printf '930|Some fix|fix/lane-ready-footer\n' >> "$D/prs"
printf '928|| review PR #930, authored outside the lane system entirely\n' >> "$D/issues"
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX

out=$(LEDGER_STATE="$D/state-308b" run 928 rev-930-red "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 930); rc=$?
want_exit "RED: a PR with no lane contributor at all is refused just like a genuinely unknown one" "$rc" 1 "$out"
want_contains "...refusing (authorship unknown, failing closed)" "authorship unknown, failing closed" "$out"
want_contains "...and now names the escape hatch: record it, don't guess it" \
  "mark-pr-external" "$out"

# The escape: an operator explicitly records the fact, auditable, never a
# flag dispatch.sh itself can flip.
# PR #331 review, finding 2: cli.py mark-pr-external now refuses without
# --chain-verified (an explicit claim the exhaustive chain ran) -- an
# operator using this escape hatch directly, having verified by hand, passes
# it themselves; mark-pr-external.sh passes it automatically once its own
# resolve_pr_contributors chain completes clean.
LEDGER_STATE="$D/state-308b" ledger mark-pr-external --repo acme/agent-dotfiles --pr 930 \
  --note "authored directly by the watchdog, no lane ever dispatched against it" --chain-verified >/dev/null

out=$(LEDGER_STATE="$D/state-308b" run 928 rev-930-green "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 930); rc=$?
want_exit "GREEN: the SAME PR, same silence from every automatic path, now dispatches once recorded" "$rc" 0 "$out"
want_contains "...and says explicitly why: recorded, not guessed" \
  "recorded as authored OUTSIDE the lane system (marked external)" "$out"
log=$(tmuxlog)
want_contains "...lands on the one free lane, nothing excluded" "send-keys -t t:@103" "$log"

# The guard must still refuse the genuinely unknown case even after this
# feature exists -- recording is per-PR, not a global switch.
printf '931|Another fix|fix/some-other-branch\n' >> "$D/prs"
printf '929|| review PR #931, still genuinely unknown -- never recorded\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-308b" run 929 rev-931-still-red "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 931); rc=$?
want_exit "an UNRECORDED PR with no automatic resolution still refuses -- recording is not a global switch" "$rc" 1 "$out"
want_contains "...still authorship unknown" "authorship unknown, failing closed" "$out"

# --- agent-supervisor#308 item 1 (READ half): dispatch.sh step 2.1's --------
# `pr-task` lookup resolves a PR's real contributor from the EXPLICIT record
# `lane-done.sh` writes after the fact (`Ledger.record_pr_for_task`), not
# from an issue reference, a `--pr`-scoped dispatch row, or a live worktree.
#
# WHY: this is the shape #308 item 1 exists for -- a task dispatched by ISSUE
# NUMBER, whose PR did not exist yet at dispatch time, so nothing in steps
# 1&2 (issue-keyed) or step 2.2 (`source_kind='pull'`, which only a
# `--pr`/`--reviews-pr`-scoped dispatch ever writes) can find it once the
# PR's own body/commits stop naming the issue in a form this script parses.
# Isolated to exactly that: the PR fixture below carries no `Fixes #`
# reference at all (steps 1&2 can find nothing) and its branch belongs to no
# worktree (step 3 can find nothing either) -- the ONLY path that can
# possibly resolve this PR's contributor is the explicit record.
#
# PR-method-level (red/green) coverage of `record_pr_for_task`/
# `get_task_for_pr_number` already exists in test_core.py; this is the shell
# glue around it -- the real `dispatch.sh`, the real `cli.py pr-task`, no
# hand-authored fixture in place of either. The WRITE half of this same
# mechanism (`lane-done.sh`'s best-effort call to `record-pr-for-task`) has
# its own real-`lane-done.sh` integration coverage in test_lane_done.sh,
# deliberately kept separate rather than chained through a second script
# invocation here -- see that file's comment on why gluing the two stubs
# together would test stub compatibility, not either script's contract. The
# CLI command is the seam where the two halves meet, and it is exercised for
# real on both sides.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
6|free-6|claude.exe|❯ ready|1|0
FIX
printf '933|| the code a fix pass on PR #972 targets\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-308c" run 933 original-972 "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the originating dispatch (task ad933-original-972) succeeds" "$rc" 0 "$out"
want_contains "setup: it landed on t:4" "send-keys -t t:@104" "$(tmuxlog)"

# Completed before its own PR is recorded -- record_pr_for_task's own
# docstring: the record is a durable fact about what happened, independent
# of the task's current status.
LEDGER_STATE="$D/state-308c" ledger record-completion --task ad933-original-972 --note done >/dev/null

# The explicit write lane-done.sh would have made, done here directly with
# the same CLI command it calls -- this test is about dispatch.sh's READ,
# not about re-proving the write (test_lane_done.sh owns that).
record_out=$(LEDGER_STATE="$D/state-308c" ledger record-pr-for-task \
  --task ad933-original-972 --repo acme/agent-dotfiles --pr 972 2>&1); record_rc=$?
want_exit "setup: PR #972's authorship is explicitly recorded against ad933-original-972" "$record_rc" 0 "$record_out"

# No "Fixes #" reference (steps 1&2 blind) and a branch no worktree ever
# checked out (step 3 blind) -- isolates the assertion to step 2.1 alone.
printf '972|A fix with no issue reference at all|some-branch-nobody-worktreed\n' >> "$D/prs"
printf '934|| review PR #972, resolved ONLY via the explicit PR-authorship record\n' >> "$D/issues"

out=$(LEDGER_STATE="$D/state-308c" run 934 rev-972 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 972); rc=$?
want_exit "a review of PR #972 dispatches, excluding the recorded contributor" "$rc" 0 "$out"
want_contains "the task step 2.1's lookup names is skipped" "skipping t:4" "$out"
log=$(tmuxlog)
want_contains "...and lands on the one lane that never touched this PR, t:6" "send-keys -t t:@106" "$log"
want_missing "never on the recorded contributor's own lane" "send-keys -t t:@104 " "$log"

# The review dispatched above is still OPEN against PR #972 -- complete it
# first, or the agent-supervisor#169 PR-duplicate guard refuses the
# mutation-check dispatch below before step 2.1 is even reached.
LEDGER_STATE="$D/state-308c" ledger record-completion --task ad934-rev-972 --note done >/dev/null

# MUTATION-CHECK: silence the `pr-task` lookup and confirm the recorded
# contributor (t:4) is WRONGLY treated as available -- proving this test
# actually exercises step 2.1, not something steps 1-2/3 already covered
# (the PR fixture above was built specifically so they cannot).
MUTANT_DIR_308C=$(make_mutant_scripts_dir)
MUTATED_308C="$MUTANT_DIR_308C/dispatch.sh"
patch_rc=0
python3 - "$MUTANT_DIR_308C/resolve-pr-contributors.sh" <<'PY' || patch_rc=$?
import sys
target = sys.argv[1]
text = open(target).read()
marker = 'pr_task_json=$("$ledger_python" "$ledger_cli" pr-task --repo "$repo" --pr "$pr" 2>&1)'
assert text.count(marker) == 1, "pr-task lookup not found or not unique -- script shape changed"
text = text.replace(marker, 'pr_task_json=\'{"known":false}\'  # MUTATED: pr-task never consulted', 1)
open(target, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh whose pr-task lookup is silenced" \
    "could not patch $MUTANT_DIR_308C/resolve-pr-contributors.sh (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh whose pr-task lookup is silenced"
  chmod +x "$MUTATED_308C"
  cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
6|free-6|claude.exe|❯ ready|1|0
FIX
  printf '935|| review PR #972 again, against the mutated lookup\n' >> "$D/issues"
  out=$(DISPATCH_SCRIPT="$MUTATED_308C" LEDGER_STATE="$D/state-308c" \
        run 935 rev-972-mutant "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 972); rc=$?
  # Unlike #308 item 2's mutation check above, this PR fixture carries no
  # issue reference and no worktree ever sat on its branch -- deliberately,
  # so nothing but step 2.1 could ever resolve it. Silencing step 2.1 does
  # not leave dispatch to proceed with an empty (safe) contributor set; it
  # removes the ONLY path that resolves this PR at all, so the fail-closed
  # guard now refuses the whole dispatch -- which is itself the proof: the
  # real script's earlier success above did not happen "for free".
  want_exit "mutation confirmed: with step 2.1 silenced, PR #972 has NO resolution path left -- dispatch refuses entirely" "$rc" 1 "$out"
  want_contains "...fails closed rather than guessing, same posture as the real refusal path" "authorship unknown, failing closed" "$out"
fi

# Restore the fixture to what the #236 section below expects (only t:3
# free) -- this section borrowed t:4/t:6 for its own scenarios and must not
# leak that shape past its own end.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
