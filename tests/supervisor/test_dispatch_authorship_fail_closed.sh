#!/bin/bash
# When authorship cannot be resolved, dispatch.sh must fail closed rather
# than guess. Covers a renamed tmux session still resolving to the same
# lane for authorship purposes (agent-supervisor#108), a branch the ledger
# has no task for refusing rather than assuming free, the ledger deciding
# authorship rather than the branch name (agent-supervisor#35) with its own
# mutation check, and #473's cross-repo authorship case (an issue in repo A,
# a review dispatched in repo B).
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

echo "dispatch.sh -- authorship that cannot be determined refuses the whole dispatch (agent-supervisor#108/#35)"

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

# --- agent-supervisor#108: renaming the session does not create a new lane -
#
# WHY: on 2026-08-14 the live tmux session `agent-dotfiles` was renamed to
# `agent-supervisor` to recover from #102. Lane identity is the string
# `<session>:<index>`, so 526 task rows now name a lane that -- AS A STRING --
# no longer exists, while the WINDOW each of them names is still there, under
# the new session name. The author-exclusion guard compared those strings, so
# `agent-dotfiles:3` never equalled `agent-supervisor:3` and the guard stopped
# excluding the one window it was pointed at: a self-review would be dispatched
# and reported as independent.
#
# Modelled exactly that way, with nothing else moved: the authoring dispatch
# runs under session `old`, the review under session `t`, against ONE shared
# ledger. The fixture is the same file both times -- same windows, same panes,
# same indices -- because a rename changes the session's LABEL and nothing else.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
printf '410|| the code PR #420 was written from, before the session rename\n' >> "$D/issues"
printf '411|| review PR #420, dispatched after the session rename\n' >> "$D/issues"
# Branch slug ("public-420") deliberately differs from the authoring dispatch's
# own slug ("pre-rename-author"), the same divergence #35/#90 use: it forces
# authorship through the ledger's ISSUE lookup rather than the branch-name
# fallback, so what is under test is the lane identity comparison and not a
# lucky string match on a task id.
printf '420|Fixes #410|lane/410-public-420\n' >> "$D/prs"

out=$(LEDGER_STATE="$D/state-108" RUN_SESSION=old run 410 pre-rename-author "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the authoring dispatch under the OLD session name succeeds" "$rc" 0 "$out"
want_contains "setup: and the ledger records its lane under the old session name" \
  '"lane":"old:3"' "$(LEDGER_STATE="$D/state-108" ledger status)"
LEDGER_STATE="$D/state-108" ledger record-completion --task ad410-pre-rename-author --note done >/dev/null

# The rename has happened: same server, same windows, same panes, new session
# name. The review is dispatched under it.
out=$(LEDGER_STATE="$D/state-108" run 411 rev-420-after-rename "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 420); rc=$?
want_exit "a review dispatched after the rename still succeeds" "$rc" 0 "$out"
want_contains "the author's WINDOW is skipped even though its recorded lane names the old session" \
  "skipping t:3" "$out"
want_contains "the skip names the pre-rename authoring task" "ad410-pre-rename-author" "$out"
want_contains "and says what was compared, so the skip is readable" "old:3" "$out"
log=$(tmuxlog)
want_contains "and the review lands on the OTHER free lane, t:4 (target t:@104)" "send-keys -t t:@104" "$log"
want_missing "never on the author's window (t:3, target t:@103)" "send-keys -t t:@103 " "$log"

# The only-free-lane variant, across the same boundary: t:4 is now busy with
# the review just dispatched, so the author's own window is the only candidate
# left. It must refuse outright -- the same refusal a same-session author gets.
#
# agent-supervisor#159: PR 422, not 420 again -- 420 already has an open task
# (ad411-rev-420-after-rename, deliberately left open so t:4 stays occupied)
# and #159's own duplicate-PR check would refuse a second 420 dispatch for
# THAT reason first. 422 closes the same issue (#410).
printf '422|Fixes #410|lane/410-public-420\n' >> "$D/prs"
printf '412|| review PR #422 again, only the pre-rename author is free\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-108" run 412 rev-422-only-author "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 422); rc=$?
want_exit "a review refused when the only free window is the pre-rename author" "$rc" 1 "$out"
want_contains "and names the pre-rename authoring task, not just a lane string" "ad410-pre-rename-author" "$out"
if [ -z "$(assignees 412)" ]; then ok "the refused cross-rename review takes no claim on its own issue"
else bad "the refused cross-rename review takes no claim on its own issue" "still assigned: $(assignees 412)"; fi

# THE OTHER DIRECTION, which is what keeps this from being "block every review
# after a rename": a genuinely DIFFERENT window, whose recorded lane also names
# the old session, is still dispatchable. The author here is window 4; the
# review must land on window 3 and must not be refused.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
printf '413|| the code PR #421 was written from, on a different window\n' >> "$D/issues"
printf '414|| review PR #421 after the rename, from another window\n' >> "$D/issues"
printf '421|Fixes #413|lane/413-public-421\n' >> "$D/prs"
out=$(LEDGER_STATE="$D/state-108b" RUN_SESSION=old run 413 other-window-author "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the authoring dispatch lands on window 4 under the old session name" "$rc" 0 "$out"
want_contains "setup: recorded as old:4" '"lane":"old:4"' "$(LEDGER_STATE="$D/state-108b" ledger status)"
LEDGER_STATE="$D/state-108b" ledger record-completion --task ad413-other-window-author --note done >/dev/null
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
out=$(LEDGER_STATE="$D/state-108b" run 414 rev-421-other-window "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 421); rc=$?
want_exit "a different window is still allowed to review across the rename" "$rc" 0 "$out"
log=$(tmuxlog)
want_contains "and the review lands on it (t:3, target t:@103)" "send-keys -t t:@103" "$log"
want_missing "no over-correction into refusing every post-rename review" "no free lane other than the author" "$out"

# --- fails closed: authorship that cannot be determined refuses the WHOLE
# dispatch, not just the candidate it could not clear -------------------
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '207|| review of a PR with no lane/ branch\n' >> "$D/issues"
# "Fixes #100" resolves a candidate issue via closingIssuesReferences, but
# #100 was never dispatched -- the ledger has no record of it either -- and
# the branch itself is not a type-prefixed one to fall back to. Every
# source comes up empty, not just the branch-name one.
printf '299|Fixes #100|some-hand-pushed-branch\n' >> "$D/prs"
out=$(LEDGER_STATE="$D/state-212-closed" run 207 rev-299 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 299); rc=$?
want_exit "authorship that cannot be read from the branch refuses the dispatch" "$rc" 1 "$out"
want_contains "and says why: no branch task to fall back to either" "(task none)" "$out"
want_contains "and says authorship is unknown, not assumed safe" "authorship unknown" "$out"
if [ -z "$(assignees 207)" ]; then ok "a fail-closed refusal takes no claim"
else bad "a fail-closed refusal takes no claim" "still assigned: $(assignees 207)"; fi

printf '208|| review of a PR from an untracked branch\n' >> "$D/issues"
printf '300|Fixes #101|lane/101-never-dispatched\n' >> "$D/prs"
out=$(LEDGER_STATE="$D/state-212-closed" run 208 rev-300 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 300); rc=$?
want_exit "a branch the ledger has no task for also refuses, not assumes free" "$rc" 1 "$out"
want_contains "and names the unresolvable task" "ad101-never-dispatched" "$out"

# A dispatch that never says --reviews-pr is unaffected by any of the above
# -- ordinary work is not held to the review rule.
printf '209|| ordinary dispatch, not a review\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-212-closed" run 209 ordinary "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a dispatch with no --reviews-pr is not held to the authorship check" "$rc" 0 "$out"

# --- agent-supervisor#35: the ledger decides authorship, not the branch --
#
# Before this, a `chore/<n>-<slug>` branch (or `fix/`, `feat/`, `docs/` --
# every prefix CLAUDE.md's Work Tracking section actually asks for except
# `lane/`) never matched dispatch.sh's branch regex at all, so EVERY review
# of one refused outright with "authorship unknown" -- whether or not the
# candidate lane actually wrote it. That is why the old guard "looked
# healthy": a same-author review and a different-author review produced the
# exact same refusal, so nothing distinguished a guard that worked from one
# that just failed closed on everything. These two cases assert the REASON,
# not just exit code, and only pass when the ledger -- not the branch --
# actually told them apart.
#
# Case 1: chore branch, author lane IS the only free lane -> still REFUSED,
# and refused BECAUSE it is the author (the same message #212's own
# same-author case gets), not because the branch shape is unrecognized.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '195|| the code PR #350 was written from (chore branch)\n' >> "$D/issues"
printf '196|| review PR #350, same-author case\n' >> "$D/issues"
# The PR's branch slug ("public-scrub") is deliberately NOT the authoring
# dispatch's own slug ("scrub-secrets", task ad195-scrub-secrets) -- so the
# widened branch-name fallback, if it fired, would resolve to a DIFFERENT,
# unknown task (ad195-public-scrub) and find nothing. Only the ledger's
# author-issue-lane lookup can resolve this one correctly; the mutation below
# proves that.
printf '350|Fixes #195|chore/195-public-scrub\n' >> "$D/prs"

out=$(LEDGER_STATE="$D/state-35a" run 195 scrub-secrets "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the authoring dispatch (#195, a chore/ branch) succeeds" "$rc" 0 "$out"
LEDGER_STATE="$D/state-35a" ledger record-completion --task ad195-scrub-secrets --note done >/dev/null

out=$(LEDGER_STATE="$D/state-35a" run 196 rev-350-same "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 350); rc=$?
want_exit "a chore/ PR's review is refused when its author is the only free lane" "$rc" 1 "$out"
want_contains "refused BECAUSE it is the author, resolved via the ledger despite the chore/ branch" \
  "ad195-scrub-secrets" "$out"
want_contains "not because the branch shape was unrecognized" "no free lane other than the author" "$out"
want_missing "and no branch-shape refusal text leaks in instead" "authorship unknown" "$out"
log=$(tmuxlog)
want_missing "nothing was sent" "send-keys" "$log"

# Case 2: chore branch, author lane is a DIFFERENT lane -> DISPATCHED, and
# lands on the OTHER free lane -- proving the ledger both identified the
# chore/ branch's real author AND still let a genuinely different lane take
# the review, which the old branch-only guard could never reach (it refused
# every chore/ PR before it got this far).
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
printf '197|| the code PR #351 was written from (chore branch)\n' >> "$D/issues"
printf '198|| review PR #351, different-author case\n' >> "$D/issues"
# Same divergence as case 1: the branch slug ("public-scrub-2") differs from
# the authoring dispatch's slug ("scrub-secrets-2", task
# ad197-scrub-secrets-2), so a branch-name fallback alone would not resolve
# this either.
printf '351|Fixes #197|chore/197-public-scrub-2\n' >> "$D/prs"

out=$(LEDGER_STATE="$D/state-35b" run 197 scrub-secrets-2 "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the authoring dispatch (#197, a chore/ branch) succeeds" "$rc" 0 "$out"
log=$(tmuxlog)
want_contains "and lands on the first free lane, t:3 (target t:@103)" "send-keys -t t:@103" "$log"
LEDGER_STATE="$D/state-35b" ledger record-completion --task ad197-scrub-secrets-2 --note done >/dev/null

out=$(LEDGER_STATE="$D/state-35b" run 198 rev-351-diff "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 351); rc=$?
want_exit "a chore/ PR's review IS dispatched when its author is a different lane" "$rc" 0 "$out"
want_contains "the author's lane (t:3) is named and skipped, via the ledger not the branch" "skipping t:3" "$out"
want_contains "the skip names the authoring task the ledger resolved by issue" "ad197-scrub-secrets-2" "$out"
log=$(tmuxlog)
want_contains "and the review lands on the OTHER free lane, t:4 (target t:@104)" "send-keys -t t:@104" "$log"
want_missing "never on the author's lane (t:3, target t:@103)" "send-keys -t t:@103 " "$log"

# MUTATION: break the ledger contributor-issue-lanes lookup (return unknown
# for every issue) and confirm case 2 goes red -- with it silenced,
# dispatch.sh falls through to the chore/ branch regex, which resolves to
# nothing (only `lane/` was ever understood there before this brief widened
# it, and even widened, plain regex matching is not what proves the LEDGER
# decided this), so the review should refuse instead of skip-and-dispatch.
MUTANT_DIR_35=$(make_mutant_scripts_dir)
MUTATED_35="$MUTANT_DIR_35/dispatch.sh"
patch_rc=0
python3 - "$MUTANT_DIR_35/resolve-pr-contributors.sh" <<'PY' || patch_rc=$?
import sys
target = sys.argv[1]
text = open(target).read()
marker = 'issue_json=$("$ledger_python" "$ledger_cli" contributor-issue-lanes --issue "$candidate_issue" "${issue_repo_args[@]+"${issue_repo_args[@]}"}" 2>&1)'
assert text.count(marker) == 1, "contributor-issue-lanes lookup not found or not unique -- script shape changed"
text = text.replace(marker, 'issue_json=\'{"known":false}\'  # MUTATED: ledger contributor-issue-lanes never consulted', 1)
open(target, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh whose contributor-issue-lanes lookup is silenced" \
    "could not patch $MUTANT_DIR_35/resolve-pr-contributors.sh (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh whose contributor-issue-lanes lookup is silenced"
  chmod +x "$MUTATED_35"
  cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
  printf '199|| the code PR #352 was written from (chore branch, mutant)\n' >> "$D/issues"
  printf '211|| review PR #352 against the mutated guard\n' >> "$D/issues"
  LEDGER_STATE="$D/state-35-mutant" run 199 scrub-secrets-3 "$D/brief.md" acme/agent-dotfiles "$REPO" >/dev/null
  LEDGER_STATE="$D/state-35-mutant" ledger record-completion --task ad199-scrub-secrets-3 --note done >/dev/null
  # Same divergence again: the branch slug does not match the authoring
  # dispatch's real slug, so with author-issue-lane silenced NEITHER the ledger
  # NOR the branch-name fallback can resolve this -- it must refuse.
  printf '352|Fixes #199|chore/199-public-scrub-3\n' >> "$D/prs"
  out=$(DISPATCH_SCRIPT="$MUTATED_35" LEDGER_STATE="$D/state-35-mutant" \
        run 211 rev-352 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 352); rc=$?
  want_exit "mutation confirmed: with the ledger's issue lookup silenced, a chore/ PR's review refuses again" "$rc" 1 "$out"
  want_contains "mutation confirmed: back to authorship unknown (the assertions above would now be red)" \
    "authorship unknown" "$out"
fi

# --- MUTATION-CHECK: remove the refusal and watch dispatch send a review
# straight to its own author --------------------------------------------
#
# The load-bearing assertion this proves alive: "the author's lane is named
# and skipped" above, and "the review lands on the OTHER free lane" -- if
# the exclusion in the lane-selection loop is deleted, both go red because
# dispatch sends the self-review to t:3 instead of refusing/rerouting it.
MUTATED="$D/dispatch-no-author-guard.sh"
patch_rc=0
python3 - "$DISPATCH" "$MUTATED" <<'PY' || patch_rc=$?
import os
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = 'if [ "$(lane_relation "$candidate" "$al" "$candidate_pane_id" "$al_pane_id")" != different ]; then'
assert marker in text, "author-exclusion guard not found -- script shape changed"
assert text.count(marker) == 1, "author-exclusion guard not unique -- script shape changed"
text = text.replace(marker, "if false; then  # MUTATED: author-exclusion always skipped", 1)
here = 'HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"'
assert text.count(here) == 1, "HERE assignment not found or not unique -- script shape changed"
text = text.replace(here, 'HERE=%r' % os.path.dirname(os.path.abspath(src)), 1)
open(dst, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh whose author-exclusion is disabled" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh whose author-exclusion is disabled"
  chmod +x "$MUTATED"
  cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
  # A fresh issue number for this second authoring dispatch -- #193 is
  # already claimed (permanently, on GitHub) by the earlier, unmutated run
  # above, and reusing it here would confuse the claim on GH state left over
  # from that case rather than exercise the mutation.
  printf '194|| the code PR #220 was written from\n' >> "$D/issues"
  printf '210|| review PR #220 against the mutated guard\n' >> "$D/issues"
  LEDGER_STATE="$D/state-212-mutant" run 194 telegram-to-director-2 "$D/brief.md" acme/agent-dotfiles "$REPO" >/dev/null
  LEDGER_STATE="$D/state-212-mutant" ledger record-completion --task ad194-telegram-to-director-2 --note done >/dev/null
  printf '220|Fixes #194|lane/194-telegram-to-director-2\n' >> "$D/prs"
  out=$(DISPATCH_SCRIPT="$MUTATED" LEDGER_STATE="$D/state-212-mutant" \
        run 210 rev-220 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 220); rc=$?
  want_exit "mutation confirmed: the unguarded copy dispatches a self-review" "$rc" 0 "$out"
  log=$(tmuxlog)
  want_contains "mutation confirmed: it lands on the author's own lane, t:3 (target t:@103)" "send-keys -t t:@103" "$log"
fi

# --- agent-supervisor#473: cross-repo authorship -- an issue in repo A,
# a PR in repo B ---------------------------------------------------------
#
# WHY: #472 is the live case. A lane is dispatched against an issue in one
# repo (agent-dotfiles) but its PR is opened in a DIFFERENT repo
# (agent-supervisor) because that is where the code it touches lives.
# `closingIssuesReferences` names each reference's own repository -- but
# steps 1&2 used to discard that and query `contributor-issue-lanes` with
# the PR's OWN repo regardless, which does not just fail to find the
# cross-repo contributor, it makes `get_contributor_tasks_for_issue`'s own
# repo-narrowing (#146) filter the TRUE contributor OUT, correctly, against
# the WRONG repo -- so the review refused "authorship unknown" for a PR
# whose author the ledger actually knew.
#
# A second local checkout stands in for repo B: repo.sh's own origin check
# (#17) means a dispatch claiming to be for "acme/agent-supervisor" must run
# against a checkout whose remote actually reads that.
git init -q --bare "$D/origin-b.git"
git clone -q "$D/origin-b.git" "$D/repo-b" 2>/dev/null
git -C "$D/repo-b" config user.email test@example.com
git -C "$D/repo-b" config user.name "Test"
git -C "$D/repo-b" checkout -q -b main
echo one > "$D/repo-b/file.txt"
git -C "$D/repo-b" add file.txt
git -C "$D/repo-b" commit -q -m "initial"
git -C "$D/repo-b" push -q -u origin main
git -C "$D/repo-b" remote set-url origin "git@github.com:acme/agent-supervisor.git"

# Case 1: the author IS recorded (repo A's ledger dispatched issue #1001,
# whose closing PR #1002 lives in repo B) -> the review DISPATCHES, skipping
# the true cross-repo author, landing on the other free lane -- proving both
# resolution AND exclusion, not just a refusal indistinguishable from #212's
# "unknown" case.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '1001|| the code PR #1002 was written from (repo A, agent-dotfiles)\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-473" run 1001 xrepo-diagram "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the authoring dispatch (issue #1001, repo A) succeeds" "$rc" 0 "$out"
LEDGER_STATE="$D/state-473" ledger record-completion --task ad1001-xrepo-diagram --note done >/dev/null

# The PR's body closes #1001 with an explicit cross-repo qualifier, exactly
# as GitHub renders "Fixes jonhill90/agent-dotfiles#299" in the live case.
printf '1002|Fixes acme/agent-dotfiles#1001|docs/1001-xrepo|\n' >> "$D/prs"
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
printf '1003|| review PR #1002 (repo B), whose closing issue lives in repo A\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-473" run 1003 rev-1002 "$D/brief.md" acme/agent-supervisor "$D/repo-b" --reviews-pr 1002); rc=$?
want_exit "a cross-repo PR's review IS dispatched once authorship resolves via the issue's OWN repo" "$rc" 0 "$out"
want_contains "the cross-repo author (ad1001-xrepo-diagram, repo A) is named and skipped" "ad1001-xrepo-diagram" "$out"
want_contains "...and skipped, via the ledger, not assumed absent" "skipping t:3" "$out"
log=$(tmuxlog)
want_contains "and lands on the OTHER free lane, t:4 (target t:@104)" "send-keys -t t:@104" "$log"
want_missing "never on the cross-repo author's own lane (t:3, target t:@103)" "send-keys -t t:@103 " "$log"

# The review dispatched above is still OPEN against PR #1002 -- complete it,
# or the #169 PR-duplicate guard refuses the mutation-check re-dispatch below
# before the cross-repo resolution is even reached.
LEDGER_STATE="$D/state-473" ledger record-completion --task as1003-rev-1002 --note done >/dev/null

# MUTATION-CHECK, direction 1 (agent-supervisor#473 requirement 3a): break
# the cross-repo resolution -- discard `closingIssuesReferences`' own
# `repository` field and query every candidate issue against the PR's OWN
# repo again, exactly what this file did before the fix. The live case (and
# the case above) must go RED: refused "authorship unknown" instead of
# dispatched.
MUTANT_DIR_473A=$(make_mutant_scripts_dir)
MUTATED_473A="$MUTANT_DIR_473A/dispatch.sh"
patch_rc=0
python3 - "$MUTANT_DIR_473A/resolve-pr-contributors.sh" <<'PY' || patch_rc=$?
import sys
target = sys.argv[1]
text = open(target).read()
marker = '    emit(f"{owner}/{name}" if owner and name else own_repo, number)'
assert text.count(marker) == 1, "cross-repo emit() not found or not unique -- script shape changed"
text = text.replace(marker, '    emit(own_repo, number)  # MUTATED: cross-repo repository field discarded', 1)
open(target, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh whose cross-repo resolution is reverted" \
    "could not patch $MUTANT_DIR_473A/resolve-pr-contributors.sh (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh whose cross-repo resolution is reverted"
  chmod +x "$MUTATED_473A"
  cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
  printf '1006|| review PR #1002 again, against the mutated (reverted) resolution\n' >> "$D/issues"
  out=$(DISPATCH_SCRIPT="$MUTATED_473A" LEDGER_STATE="$D/state-473" \
        run 1006 rev-1002-mutant "$D/brief.md" acme/agent-supervisor "$D/repo-b" --reviews-pr 1002); rc=$?
  want_exit "mutation confirmed: reverting the cross-repo fix refuses the live case again" "$rc" 1 "$out"
  want_contains "mutation confirmed: back to authorship unknown" "authorship unknown" "$out"
fi

# Case 2: the author is GENUINELY unknown (repo B's PR closes an issue in
# repo A that no lane ever touched) -> STILL REFUSES. This is the dangerous
# direction agent-supervisor#473 calls out by name: a fix that makes cross-
# repo resolution too eager must not turn "nobody wrote this" into "assume
# safe".
printf '1005|| an issue in repo A that nothing was ever dispatched against\n' >> "$D/issues"
printf '1004|Fixes acme/agent-dotfiles#1005|some-branch-nobody-worktreed|\n' >> "$D/prs"
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '1007|| review PR #1004, cross-repo but genuinely unauthored\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-473b" run 1007 rev-1004 "$D/brief.md" acme/agent-supervisor "$D/repo-b" --reviews-pr 1004); rc=$?
want_exit "a cross-repo PR whose author is genuinely unknown STILL REFUSES" "$rc" 1 "$out"
want_contains "and says authorship is unknown, not assumed safe" "authorship unknown" "$out"

# MUTATION-CHECK, direction 2 (agent-supervisor#473 requirement 3b, "the
# dangerous direction"): remove the `known:true` gate on the cross-repo
# lookup's response -- any response (including a genuine `known:false`) is
# then treated as a resolved, EMPTY contributor set, which is exactly the
# "empty instrument reads as an empty world" defect this file's own header
# warns about. The refusal above MUST go RED -- an unauthored PR must not
# become reviewable by every free lane.
MUTANT_DIR_473B=$(make_mutant_scripts_dir)
MUTATED_473B="$MUTANT_DIR_473B/dispatch.sh"
patch_rc=0
python3 - "$MUTANT_DIR_473B/resolve-pr-contributors.sh" <<'PY' || patch_rc=$?
import sys
target = sys.argv[1]
text = open(target).read()
marker = '''    if grep -qF '"known":true' <<<"$issue_json"; then
      CONTRIBUTORS_RESOLVED=1
      while IFS=$'\\t' read -r c_lane c_task; do'''
assert text.count(marker) == 1, "cross-repo known:true gate not found or not unique -- script shape changed"
mutated = '''    if true; then  # MUTATED: known:true gate removed, any response treated as resolved
      CONTRIBUTORS_RESOLVED=1
      while IFS=$'\\t' read -r c_lane c_task; do'''
text = text.replace(marker, mutated, 1)
open(target, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh whose known:true gate is removed" \
    "could not patch $MUTANT_DIR_473B/resolve-pr-contributors.sh (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh whose known:true gate is removed"
  cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
  printf '1008|| review PR #1004 again, against the mutated (over-eager) gate\n' >> "$D/issues"
  out=$(DISPATCH_SCRIPT="$MUTATED_473B" LEDGER_STATE="$D/state-473b" \
        run 1008 rev-1004-mutant "$D/brief.md" acme/agent-supervisor "$D/repo-b" --reviews-pr 1004); rc=$?
  want_exit "mutation confirmed: removing the known:true gate WRONGLY dispatches the genuinely-unauthored PR" "$rc" 0 "$out"
  log=$(tmuxlog)
  want_contains "mutation confirmed: it lands on the only free lane, t:3 (target t:@103), no author excluded" "send-keys -t t:@103" "$log"
fi

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
