#!/bin/bash
# agent-supervisor#619: worktree resolution for a review must not depend on
# `git worktree list` seeing it -- the ledger's own open-worktrees record
# is an independent source, checked here for a PR whose worktree the git
# list never heard of, and for a ledger row whose worktree_path was blanked
# directly, plus a mutation check silencing the ledger source specifically.
# agent-supervisor#640: dispatch.sh records review-ness as a FACT
# (--is-review), not inferred later, checked with a mutation check
# reverting that forwarding.
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

echo "dispatch.sh -- worktree resolution must not depend on git's own worktree list (agent-supervisor#619/#640)"

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

# --- agent-supervisor#619: worktree resolution must not depend on
# repo_path's own `git worktree list` still knowing about the worktree ------
#
# WHY: #618 was a real PR (task as531-redo531) whose lane renamed its
# worktree's branch to a slug sharing no text with the dispatch (the exact
# shape #117's own test above already covers via `git branch -m`, run from
# the SAME repo_path that created the worktree). #619 measured that #117's
# fix alone was not enough: `python3 cli.py worktree-lane --path <path>`
# resolved the worktree directly, on the FIRST try, even though the SAME
# review dispatch's `git worktree list` on the repo_path IT was given came
# back with no record at all. This reproduces that gap: the worktree is
# created against one clone ($REPO), but the review is dispatched with a
# SECOND, independent clone of the same origin ($D/repo2) as its repo_path --
# `git worktree list` on repo2 has never heard of a worktree rooted under
# $REPO and never will, exactly like a review dispatched against a
# repo_path whose own worktree admin state has drifted from reality. Only
# the ledger's OWN record of the worktree (`open-worktrees`, checked
# directly against the worktree's own on-disk branch) can resolve this.
D619="$D/state-619"
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
printf '619|| the code PR #6190 was written from\n' >> "$D/issues"
printf '620|| review PR #6190, branch renamed to an unrelated slug, resolved via repo2\n' >> "$D/issues"

out=$(LEDGER_STATE="$D619" run 619 pr-619-author "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the authoring dispatch (#619) succeeds" "$rc" 0 "$out"
WT_619=$(sed -n 's/^  worktree: //p' <<<"$out")
if [ -z "$WT_619" ] || [ ! -d "$WT_619" ]; then
  bad "setup: the authoring dispatch printed a real worktree path" "got: '$WT_619' from: $out"
else
  ok "setup: the authoring dispatch printed a real worktree path"
fi
# `git checkout -b`, not `git branch -m` -- a NEW branch, exactly the
# reflog shape #619's issue quotes ("checkout: moving from ... to ..."),
# sharing no text with the dispatch slug and not matching the legacy
# `(lane|fix|feat|chore|docs)/<n>-<slug>` convention either.
git -C "$WT_619" checkout -q -b "docs/okf-index-measurement-redo619"

# The PR's own "Fixes #<N>" names an issue (990) nothing in this ledger was
# ever dispatched for, same technique #117's test above uses -- steps 1/2
# (issue-keyed lookup) must come up silent, so only a worktree-based path
# can resolve this.
printf '6190|Fixes #990|docs/okf-index-measurement-redo619\n' >> "$D/prs"

# A second, INDEPENDENT clone of the same origin -- never shares a `.git`
# with $REPO, so `git worktree list` run against it can never enumerate a
# worktree rooted under $REPO no matter what branch that worktree is on.
REPO2="$D/repo2"
git clone -q "$REPO" "$REPO2" 2>/dev/null
git -C "$REPO2" remote set-url origin "git@github.com:acme/agent-dotfiles.git"

out=$(LEDGER_STATE="$D619" run 620 rev-6190 "$D/brief.md" acme/agent-dotfiles "$REPO2" --reviews-pr 6190); rc=$?
want_exit "a review of PR #6190 is still dispatched, resolved by the ledger's own worktree record even though repo_path's git worktree list never heard of it" "$rc" 0 "$out"
want_contains "the author's lane is named and skipped" "skipping t:3" "$out"
want_contains "the skip names the real authoring task, not a reconstruction from the branch" \
  "ad619-pr-619-author" "$out"
want_missing "never the legacy fallback's wrong reconstruction" "ad619-not-a-review-escape" "$out"
log=$(tmuxlog)
want_contains "and the review lands on the OTHER free lane, t:4 (target t:@104)" "send-keys -t t:@104" "$log"
want_missing "never on the author's lane (t:3, target t:@103)" "send-keys -t t:@103 " "$log"

# --- verification bar item 2: a PR from a worktree with NO dispatch record
# still fails closed, even against the SAME repo2 path used above -----------
printf '6191|Fixes #991|some-branch-nobody-ever-dispatched-619\n' >> "$D/prs"
printf '621|| review PR #6191, nothing in the ledger names it\n' >> "$D/issues"
out=$(LEDGER_STATE="$D619" run 621 rev-6191-unknown "$D/brief.md" acme/agent-dotfiles "$REPO2" --reviews-pr 6191); rc=$?
want_exit "a PR with no dispatch record anywhere still fails closed" "$rc" 1 "$out"
want_contains "...refusing on authorship unknown, not guessing" "authorship unknown, failing closed" "$out"

# --- verification bar item 3: a worktree whose ledger row exists but whose
# worktree_path is NULL (blank) still fails closed ---------------------------
#
# Simulates a task dispatched before agent-supervisor#117 added the column
# (or one #611 could not backfill): the row is real, the lane is real, but
# the one field this whole chain depends on was never written. Blanking it
# directly is the only way to construct that state -- `#611` (see this
# worktree's own CLAUDE.md entry) guarantees every fresh `assign_task` now
# writes it, so there is no dispatch-level knob left to leave it empty.
printf '622|| the code PR #6192 was written from\n' >> "$D/issues"
printf '623|| review PR #6192, but its worktree_path row was blanked\n' >> "$D/issues"
# t:3 and t:4 are both still occupied by the #619/#620 tasks above (neither
# was completed or cancelled) -- a fresh free lane so this authoring
# dispatch has somewhere to land.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
6|free-6|claude.exe|❯ ready|1|0
FIX
out=$(LEDGER_STATE="$D619" run 622 pr-622-author "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the second authoring dispatch (#622) succeeds" "$rc" 0 "$out"
WT_622=$(sed -n 's/^  worktree: //p' <<<"$out")
# Task id convention every other case in this file already relies on
# (`ad81-dispatch-worktree`, `ad101-pr-inference-fix`, ...): "<prefix><issue>-<slug>".
TASK_622="ad622-pr-622-author"
if [ -z "$WT_622" ] || [ ! -d "$WT_622" ]; then
  bad "setup: the second authoring dispatch printed a real worktree path" "got: '$WT_622' from: $out"
else
  ok "setup: the second authoring dispatch printed a real worktree path"
fi
git -C "$WT_622" checkout -q -b "docs/okf-index-measurement-redo622"
printf '6192|Fixes #992|docs/okf-index-measurement-redo622\n' >> "$D/prs"
blank_rc=0
AGENT_SUPERVISOR_STATE_DIR="$D619" python3 - "$HERE/../../scripts/supervisor" "$TASK_622" <<'PY' || blank_rc=$?
import os
import sqlite3
import sys
from pathlib import Path

sys.path.insert(0, sys.argv[1])
from core import Ledger

task_id = sys.argv[2]
ledger = Ledger(Path(os.environ["AGENT_SUPERVISOR_STATE_DIR"]))
connection = sqlite3.connect(ledger.db_path)
try:
    cur = connection.execute("UPDATE tasks SET worktree_path = '' WHERE id = ?", (task_id,))
    assert cur.rowcount == 1, f"expected exactly one row for task {task_id!r}, updated {cur.rowcount}"
    connection.commit()
finally:
    connection.close()
PY
if [ "$blank_rc" -ne 0 ]; then
  bad "setup: blanked task $TASK_622's worktree_path directly in the ledger" "exit $blank_rc"
else
  blanked=$(AGENT_SUPERVISOR_STATE_DIR="$D619" python3 "$HERE/../../scripts/supervisor/cli.py" worktree-lane --path "$WT_622")
  want_contains "setup: worktree-lane no longer resolves this path (worktree_path blanked)" '"known":false' "$blanked"
fi
out=$(LEDGER_STATE="$D619" run 623 rev-6192-blank "$D/brief.md" acme/agent-dotfiles "$REPO2" --reviews-pr 6192); rc=$?
want_exit "a worktree whose ledger row exists but whose worktree_path was blanked still fails closed" "$rc" 1 "$out"
want_contains "...refusing on authorship unknown, not guessing from the branch it is sitting on" "authorship unknown, failing closed" "$out"

# MUTATION-CHECK: silence Source B (the ledger's open-worktrees consultation)
# and confirm the FIRST scenario above goes red -- with repo_path's own git
# worktree list still blind to the worktree (repo2, as above), and Source B
# disabled, nothing is left that can resolve PR #6190.
MUTANT_DIR_619=$(make_mutant_scripts_dir)
MUTATED_619="$MUTANT_DIR_619/dispatch.sh"
patch_rc=0
python3 - "$MUTANT_DIR_619/resolve-pr-contributors.sh" <<'PY' || patch_rc=$?
import sys
target = sys.argv[1]
text = open(target).read()
marker = 'open_worktrees_json=$("$ledger_python" "$ledger_cli" open-worktrees 2>&1)'
assert text.count(marker) == 1, "open-worktrees lookup not found or not unique -- script shape changed"
text = text.replace(
    marker,
    'open_worktrees_json=\'{"tasks":[]}\'  # MUTATED (#619): ledger open-worktrees never consulted',
    1,
)
open(target, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh whose open-worktrees lookup is silenced" \
    "could not patch $MUTANT_DIR_619/resolve-pr-contributors.sh (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh whose open-worktrees lookup is silenced"
  chmod +x "$MUTATED_619"
  cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
  printf '624|| review PR #6190 again, against the mutated guard\n' >> "$D/issues"
  out=$(DISPATCH_SCRIPT="$MUTATED_619" LEDGER_STATE="$D619" \
        run 624 rev-6190-mutant "$D/brief.md" acme/agent-dotfiles "$REPO2" --reviews-pr 6190); rc=$?
  want_exit "mutation confirmed: with the ledger's open-worktrees source silenced, the same review now refuses" "$rc" 1 "$out"
  want_contains "mutation confirmed: back to authorship unknown (the assertions above would now be red)" \
    "authorship unknown" "$out"
fi

# --- agent-supervisor#640: dispatch.sh records review-ness as a FACT, not -
# a name it hopes a regex will guess back correctly later ------------------
#
# WHY: `Ledger.get_contributor_tasks_for_pr` used to infer review-ness
# entirely from `_task_looks_like_review`, a regex over the task id and
# summary. It requires "rev"/"review" right after `^`, `-` or `_` --
# `rerev636` never matches (the "re" right before "rev" has no separator),
# so a `--reviews-pr` dispatch named that way silently scored as an AUTHOR
# of the PR it reviewed, and `merge-pr.sh` refused its own verdict as a
# self-review. `dispatch.sh` now forwards `--is-review` to `record-dispatch`
# whenever `$REVIEWS_PR` (not just `$PR_SCOPED`) is set -- the exact fact
# this script already knows -- so `source_tasks.is_review` records the
# truth regardless of what the dispatch ends up named.
read_is_review() {  # read_is_review <state-dir> <task-id>
  AGENT_SUPERVISOR_STATE_DIR="$1" python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
print(Ledger(sys.argv[2]).get_source_task(sys.argv[3])["is_review"])
' "$HERE/../../scripts/supervisor" "$1" "$2"
}
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX

# case 1: a --reviews-pr dispatch named exactly the shape that defeated the
# old regex ("rerev...", "re" running straight into "rev" with no `-`/`_`
# between them). Routed through mark-pr-external so the dispatch succeeds
# without the author-exclusion chain needing to resolve anything -- this
# case is only about what gets RECORDED, not about who gets skipped.
printf '6402|Some fix|fix/rev640-branch\n' >> "$D/prs"
printf '6404|| review PR #6402, entirely external\n' >> "$D/issues"
LEDGER_STATE="$D/state-640a" ledger mark-pr-external --repo acme/agent-dotfiles --pr 6402 \
  --note "authored outside the lane system, for agent-supervisor#640's own test" --chain-verified >/dev/null

out=$(LEDGER_STATE="$D/state-640a" run 6404 rerev6402 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 6402); rc=$?
want_exit "a --reviews-pr dispatch named rerev... (the exact shape the old regex missed) succeeds" "$rc" 0 "$out"
recorded=$(read_is_review "$D/state-640a" ad6404-rerev6402)
if [ "$recorded" = "1" ]; then
  ok "dispatch.sh recorded is_review=1 -- the FACT --reviews-pr carried, not a name guess"
else
  bad "dispatch.sh recorded is_review=1 -- the FACT --reviews-pr carried, not a name guess" "got: $recorded"
fi

# case 2: a --pr fix-pass (no --reviews-pr) named like a review must record
# is_review=0 explicitly -- not leave it NULL for the regex to mishandle in
# the OTHER direction (`revamp-parser` matches `_task_looks_like_review`
# directly; see that method's own docstring).
printf '6405|| the code a fix pass on PR #6406 targets\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-640b" run 6405 original-6406 "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the original dispatch (#6405) succeeds" "$rc" 0 "$out"

out=$(LEDGER_STATE="$D/state-640b" run 6405 revamp-parser-6406 "$D/brief.md" acme/agent-dotfiles "$REPO" --pr 6406); rc=$?
want_exit "a --pr fix-pass named revamp-parser (no --reviews-pr) still dispatches" "$rc" 0 "$out"
recorded=$(read_is_review "$D/state-640b" ad6405-revamp-parser-6406)
if [ "$recorded" = "0" ]; then
  ok "dispatch.sh recorded is_review=0 -- explicitly NOT a review, whatever it's named"
else
  bad "dispatch.sh recorded is_review=0 -- explicitly NOT a review, whatever it's named" "got: $recorded"
fi

# case 3 -- mutation check: revert dispatch.sh's own --is-review forwarding
# and confirm case 1's assertion above would go red.
#
# What "red" looks like here is worth being precise about: `cli.py`'s
# `--pr` alone (no `--is-review`) records a KNOWN `0`, not `NULL` -- that is
# case 2's own point, and it holds regardless of WHY the flag was omitted.
# So reverting the forwarding line does not put this row back to "unknown,
# ask the regex" -- it makes dispatch.sh silently record `is_review=0`, a
# CONFIDENT "not a review", for a dispatch that was actually a review. That
# is worse than the pre-#640 regex miss, not equivalent to it: the old
# behaviour left `get_contributor_tasks_for_pr` free to consult
# `_task_looks_like_review` for a legacy row; a wrongly-recorded `0` is
# trusted outright and never falls back to anything. This is exactly why
# this one forwarding line is load-bearing and gets its own mutation check.
# agent-supervisor#716: the --is-review forwarding (step 6) now lives in
# dispatch-record.sh.
MUTANT_DIR_640=$(make_mutant_scripts_dir)
MUTATED_640="$MUTANT_DIR_640/dispatch.sh"
patch_rc=0
PYTHONPATH="$HERE${PYTHONPATH:+:$PYTHONPATH}" python3 - "$MUTANT_DIR_640" <<'PY' || patch_rc=$?
import sys
import _dispatch_mutate as M
target = sys.argv[1]
marker = '  [ -z "$REVIEWS_PR" ] || LEDGER_ARGS+=(--is-review)\n'
M.patch(target, marker, "")
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh with --is-review forwarding removed" \
    "could not patch $MUTATED_640 (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh with --is-review forwarding removed"
  printf '6408|Some fix|fix/rev640c-branch\n' >> "$D/prs"
  LEDGER_STATE="$D/state-640c" ledger mark-pr-external --repo acme/agent-dotfiles --pr 6408 \
    --note "authored outside the lane system, for agent-supervisor#640's mutation check" --chain-verified >/dev/null
  printf '6409|| review PR #6408, entirely external\n' >> "$D/issues"
  out=$(DISPATCH_SCRIPT="$MUTATED_640" LEDGER_STATE="$D/state-640c" \
        run 6409 rerev6408 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 6408); rc=$?
  want_exit "mutation setup: the review still dispatches (only the recorded fact changes)" "$rc" 0 "$out"
  recorded=$(read_is_review "$D/state-640c" ad6409-rerev6408)
  if [ "$recorded" = "0" ]; then
    ok "mutation confirmed: with --is-review forwarding reverted, the same review now silently records is_review=0 (the assertion above would now be red)"
  else
    bad "mutation confirmed: with --is-review forwarding reverted, the same review now silently records is_review=0" \
      "expected 0 (a review wrongly recorded as confidently NOT one), got: $recorded"
  fi
fi

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
