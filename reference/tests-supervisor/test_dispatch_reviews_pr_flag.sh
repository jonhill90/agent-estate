#!/bin/bash
# agent-dotfiles#225: --reviews-pr with no value must refuse, not hang, and
# the two empty-array expansions it introduced must not break under bash
# 3.2 -- also checked against the existing stderr-clean guard (#199).
# agent-supervisor#70: a forgotten --reviews-pr is not a silent
# self-review; it must be caught and refused explicitly.
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

echo "dispatch.sh -- --reviews-pr flag handling (agent-dotfiles#225/agent-supervisor#70)"

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

# --- agent-dotfiles#225: --reviews-pr with no value must refuse, not hang -
#
# WHY: `REVIEWS_PR="${2:-}"; shift 2` -- with the flag last and its value
# forgotten, $# is 1 when the case arm runs, so `shift 2` fails and shifts
# nothing. Under `set -uo pipefail` (this script has no `set -e`), a failed
# `shift` does not abort -- the `while [ $# -gt 0 ]` loop just re-enters the
# same arm forever. That is a hang, not a crash, so it needs `timeout` to
# reproduce and to prove fixed: an ordinary `$(...)` capture would sit here
# for the life of the test run.
: > "$D/tmux.log"
rm -rf "$D/panes"; mkdir -p "$D/panes"
printf '213|| a dangling --reviews-pr must refuse, not hang\n' >> "$D/issues"
out=$(timeout 10 env PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
  LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
  TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 \
  AGENT_SUPERVISOR_STATE_DIR="$(mktemp -d "$D/state.XXXXXX")" \
  STUB_PANE_PATH="$REPO" WORKTREE_ROOT="$D/roots" \
  "$DISPATCH" 213 dangling-flag "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 2>&1); rc=$?
want_exit "a --reviews-pr with no value refuses instead of hanging" "$rc" 1 "$out"
want_contains "and explains the usage" "--reviews-pr requires a PR number" "$out"
if [ -z "$(assignees 213)" ]; then ok "the refused dispatch takes no claim on its own issue"
else bad "the refused dispatch takes no claim on its own issue" "still assigned: $(assignees 213)"; fi

# MUTATION-CHECK: put the un-guarded `${2:-}; shift 2` back and confirm the
# suite actually notices -- a test that only ever ran the fixed script would
# pass whether or not the guard exists.
# agent-supervisor#716: the flag loop (including this guard) now lives in
# dispatch-args.sh, whose own `${BASH_SOURCE[0]}` usages were rewritten to
# `"$HERE/dispatch.sh"` at split time (a sourced file's BASH_SOURCE[0] is
# ITS OWN path, not dispatch.sh's -- see dispatch-args.sh's own header) --
# the marker below matches that rewritten text, not the pre-split original.
MUTATED_225A_DIR=$(make_mutant_scripts_dir)
MUTATED_225A="$MUTATED_225A_DIR/dispatch.sh"
patch_rc=0
PYTHONPATH="$HERE${PYTHONPATH:+:$PYTHONPATH}" python3 - "$MUTATED_225A_DIR" <<'PY' || patch_rc=$?
import sys
import _dispatch_mutate as M
target = sys.argv[1]
guarded = '''      if [ $# -lt 2 ]; then
        echo "dispatch: --reviews-pr requires a PR number" >&2
        sed -n '/^# Usage:/,/^$/p' "$HERE/dispatch.sh" | sed 's/^# \\{0,1\\}//' >&2
        exit 1
      fi
      REVIEWS_PR="$2"
      REVIEWS_PR_EXPLICIT=1
      shift 2'''
M.patch(target, guarded, '      REVIEWS_PR="${2:-}"\n      REVIEWS_PR_EXPLICIT=1\n      shift 2')
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh with the flag-value guard reverted" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh with the flag-value guard reverted"
  chmod +x "$MUTATED_225A"
  : > "$D/tmux.log"
  rm -rf "$D/panes"; mkdir -p "$D/panes"
  mut_rc=0
  timeout 10 env PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
    TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 \
    AGENT_SUPERVISOR_STATE_DIR="$(mktemp -d "$D/state.XXXXXX")" \
    STUB_PANE_PATH="$REPO" WORKTREE_ROOT="$D/roots" \
    "$MUTATED_225A" 213 dangling-flag-2 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr \
    >/dev/null 2>&1 || mut_rc=$?
  want_exit "mutation confirmed: the unguarded copy hangs (killed by timeout)" "$mut_rc" 124
fi


# --- agent-dotfiles#225: two empty-array expansions break under bash 3.2 --
#
# WHY: dispatch.sh is `#!/bin/bash`, and loop-tick.md invokes it directly, so
# on macOS that is /bin/bash 3.2.57 -- where "${arr[@]}" on an EMPTY array
# under `set -u` is an unbound-variable error, not zero words (bash >= 4.4
# fixed this; 3.2 never will). Both cases below invoke "$DISPATCH" directly
# (not `bash "$DISPATCH"`, which would pick up PATH's bash and never see the
# bug), the same style #199's stderr-clean case above uses, so the script's
# own shebang selects the interpreter exactly as production does.
#
# The two assertions here are portable and always run. The mutation-check at
# the end of the block is NOT portable -- it asserts a crash only pre-4.4
# bash produces -- and probes the interpreter before demanding it; see the
# comment there.
echo "--- agent-dotfiles#225: bash 3.2 empty-array sites ---"

# Site 1: dispatch.sh:82's `set -- "${POSITIONAL[@]}"` on the zero-argument
# path, where POSITIONAL is empty. Every invocation with a missing/typo'd
# argument hits this before anything else runs.
STDERR_225B="$D/dispatch225-zeroarg.err"
"$DISPATCH" 1>"$D/dispatch225-zeroarg.out" 2>"$STDERR_225B"
rc=$?
zeroarg_err=$(cat "$STDERR_225B")
want_exit "dispatch.sh with no args still exits 2 (usage), not a 3.2 crash" "$rc" 2 "$zeroarg_err"
want_missing "no unbound-variable error on the zero-arg path" "unbound variable" "$zeroarg_err"

# Site 2: dispatch.sh:188's `"${GH_REPO_ARGS[@]}"`, empty whenever [repo] is
# omitted on a --reviews-pr dispatch -- documented as supported in the flag's
# own usage text. Uses the real gh stub so the call reaches line 188 and
# fails (or succeeds) for a REASON, not because `gh` itself is missing.
printf '212|| review of a PR, [repo] omitted\n' >> "$D/issues"
printf '301|Fixes #102|lane/102-omitted-repo\n' >> "$D/prs"
STDERR_225C="$D/dispatch225-reviewargs.err"
: > "$D/tmux.log"
rm -rf "$D/panes"; mkdir -p "$D/panes"
PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
  LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
  TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 \
  AGENT_SUPERVISOR_STATE_DIR="$(mktemp -d "$D/state.XXXXXX")" \
  STUB_PANE_PATH="$REPO" WORKTREE_ROOT="$D/roots" \
  "$DISPATCH" 212 rev-301 "$D/brief.md" "" "$REPO" --reviews-pr 301 \
  1>"$D/dispatch225-reviewargs.out" 2>"$STDERR_225C"
reviewargs_err=$(cat "$STDERR_225C")
want_missing "no unbound-variable error with [repo] omitted on --reviews-pr" "unbound variable" "$reviewargs_err"
# With [repo] empty, NAME_PART falls back to basename($REPO_PATH) -- here
# the test clone's directory, literally named "repo" -- so the task id this
# resolves to is repo102-omitted-repo, not ad102-omitted-repo; see
# dispatch.sh's own NAME_PART fallback just above step 0. The ledger has no
# record of it (nothing ever dispatched #301's branch), so this still
# refuses -- fails closed, same outcome the finding describes, just for the
# right reason (`gh` actually ran) instead of the wrong one (`gh` never ran
# because the shell crashed first).
want_contains "and still fails closed for the documented reason: no ledger record" "repo102-omitted-repo" "$reviewargs_err"

# MUTATION-CHECK: put both raw "${arr[@]}" expansions back and confirm the
# suite actually notices under real /bin/bash.
MUTANT_DIR_225B=$(make_mutant_scripts_dir)
MUTATED_225B="$MUTANT_DIR_225B/dispatch.sh"
# agent-supervisor#716: POSITIONAL's expansion now lives in dispatch-args.sh,
# not dispatch.sh's own text -- search the whole mutant dir for it instead.
patch_rc=0
PYTHONPATH="$HERE${PYTHONPATH:+:$PYTHONPATH}" python3 - "$MUTANT_DIR_225B" "$MUTANT_DIR_225B/resolve-pr-contributors.sh" <<'PY' || patch_rc=$?
import sys
import _dispatch_mutate as M
mutant_dir, resolve_path = sys.argv[1], sys.argv[2]

M.patch(mutant_dir, 'set -- "${POSITIONAL[@]+"${POSITIONAL[@]}"}"', 'set -- "${POSITIONAL[@]}"')

text = open(resolve_path).read()
n = text.count('gh pr view "$pr" "${gh_repo_args[@]+"${gh_repo_args[@]}"}" --json headRefName')
assert n == 1, "gh_repo_args 3.2-safe expansion not found or not unique -- script shape changed"
text = text.replace(
    'gh pr view "$pr" "${gh_repo_args[@]+"${gh_repo_args[@]}"}" --json headRefName',
    'gh pr view "$pr" "${gh_repo_args[@]}" --json headRefName',
    1,
)
open(resolve_path, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh with both 3.2-safe expansions reverted" \
    "could not patch $MUTANT_DIR_225B (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh with both 3.2-safe expansions reverted"
  chmod +x "$MUTATED_225B"

  # The two POSITIVE cases above run everywhere: the shipped expansions are
  # correct under every bash, so asserting "no unbound-variable error" is a
  # portable claim. The MUTATION half is not -- it asserts the reverted copy
  # CRASHES, and only pre-4.4 bash raises that error at all. On Ubuntu CI
  # /bin/bash is 5.x, the mutant runs cleanly, and both assertions failed for
  # a reason that had nothing to do with dispatch.sh: that is what turned this
  # branch's CI red at head 860e586 while the same suite passed on macOS.
  #
  # So probe whether the bug is reproducible in this interpreter before
  # demanding the mutant reproduce it. Probe the BEHAVIOUR, not
  # $BASH_VERSION: what this case needs to know is whether an empty
  # "${arr[@]}" under `set -u` errors here, which is a property of the shell
  # in front of us, and a version string is a proxy for it that can be wrong
  # (distro backports, a rebuilt bash) in either direction.
  #
  # Probe the interpreter the MUTANT will actually use, read off its own
  # shebang, so the probe and the mutant can never disagree about which bash
  # is under test -- the mutant is executed directly (not via PATH's `bash`)
  # precisely so its shebang chooses, exactly as production does.
  MUT_SHELL=$(sed -n '1s|^#!||p' "$MUTATED_225B" | awk '{print $1}')
  [ -n "$MUT_SHELL" ] || MUT_SHELL=/bin/bash
  # TWO probes, because one cannot separate the two ways this can go wrong.
  # The obvious single probe -- run the expansion and treat exit 1 as "it
  # errored" -- is wrong: measured on this machine, /bin/bash 3.2.57 exits
  # **127** on that expansion, not 1, so keying on 1 would have mis-read real
  # 3.2 as "no bug here" and silently skipped the mutation on the one platform
  # that has the bug. And 127 is also what a missing shell returns, so the
  # expansion probe alone cannot tell 3.2 apart from "no such interpreter".
  #
  # So: probe 1 asks only "can this shell run anything at all", probe 2 asks
  # only "did the empty expansion abort before reaching exit 7". A shell that
  # cannot run is a FAILURE, never a skip -- a mutation-check that silently
  # stops running is the exact failure mode this block exists to prevent.
  "$MUT_SHELL" -c 'exit 7' >/dev/null 2>&1
  shell_rc=$?
  "$MUT_SHELL" -c 'set -uo pipefail; A=(); printf "%s" "${A[@]}"; exit 7' >/dev/null 2>&1
  probe_rc=$?
  if [ "$shell_rc" -ne 7 ]; then
    bad "setup: probed whether an empty \"\${arr[@]}\" errors under $MUT_SHELL" \
      "cannot run $MUT_SHELL at all (a bare 'exit 7' returned $shell_rc) -- the mutant runs under this interpreter via its shebang, so this is a failure, not a skip"
  elif [ "$probe_rc" -eq 7 ]; then
    echo "  (skipped, not passed: $MUT_SHELL expands an empty \"\${arr[@]}\" to zero words under set -u -- bash >= 4.4 -- so reverting the 3.2-safe expansions is UNOBSERVABLE here and the mutation cannot be checked on this machine. The two positive cases above did run. Exercise this on macOS's real /bin/bash 3.2.)"
  else
    mut_zeroarg_err=$("$MUTATED_225B" 2>&1 1>/dev/null)
    want_contains "mutation confirmed: the zero-arg path crashes under 3.2" "unbound variable" "$mut_zeroarg_err"

    : > "$D/tmux.log"
    rm -rf "$D/panes"; mkdir -p "$D/panes"
    mut_reviewargs_err=$(PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
      LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
      TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 \
      AGENT_SUPERVISOR_STATE_DIR="$(mktemp -d "$D/state.XXXXXX")" \
      STUB_PANE_PATH="$REPO" WORKTREE_ROOT="$D/roots" \
      "$MUTATED_225B" 212 rev-301-2 "$D/brief.md" "" "$REPO" --reviews-pr 301 2>&1 1>/dev/null)
    want_contains "mutation confirmed: [repo]-omitted --reviews-pr crashes under 3.2" "unbound variable" "$mut_reviewargs_err"
  fi
fi

# --- agent-dotfiles#225: does the existing stderr-clean guard (#199) catch
# finding 2's message on its own? -------------------------------------------
#
# The brief asks this explicitly: #199's assertion is `[ -z "$err" ]` over a
# SUCCESSFUL dispatch's stderr, and both of finding 2's sites only run at
# all on the --reviews-pr path, which #199's own case never takes (it
# dispatches ordinary work, no --reviews-pr). So the existing guard's reach
# does not cover this: it was never exercised against this path, not
# defeated by it. The dedicated cases above are what actually catch it.
echo "  (agent-dotfiles#199's stderr-clean case never takes the --reviews-pr path, so it could not have caught finding 2 either way -- confirmed by inspection, not a case here)"

# --- agent-supervisor#70: a forgotten --reviews-pr is not a silent self-
# review -------------------------------------------------------------------
#
# WHY: `--reviews-pr` (#212/#35) resolves authorship correctly and refuses a
# self-review -- but only when the caller remembers to pass it, and the
# supervisor forgot it three times in one day (PR #62 twice, #69 once),
# dispatching a self-review every time. This exercises the exact same #212
# refusal reached WITHOUT the flag: dispatch.sh infers it from the issue
# title's own "review PR #<N>" shape (the shape every review issue in this
# estate's history already uses -- see the #212/#35 fixtures above).
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '240|| the code PR #500 was written from\n' >> "$D/issues"
printf '241|| review PR #500, no flag passed\n' >> "$D/issues"
printf '500|Fixes #240|lane/240-infer-author\n' >> "$D/prs"

out=$(LEDGER_STATE="$D/state-70" run 240 infer-author "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "the authoring dispatch (#240) succeeds" "$rc" 0 "$out"
LEDGER_STATE="$D/state-70" ledger record-completion --task ad240-infer-author --note done >/dev/null

# Case 1 (red first #1): the author's lane is the ONLY free lane -> refuses,
# naming the lane and the PR, even though --reviews-pr was never passed.
out=$(LEDGER_STATE="$D/state-70" run 241 rev-500-noflag "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a review inferred from the issue title is refused when the only free lane is the author" "$rc" 1 "$out"
want_contains "and says the flag was inferred, from the issue title" "inferred --reviews-pr 500 from issue #241's title" "$out"
want_contains "names the PR" "PR #500" "$out"
want_contains "names the authoring task, not just the lane" "ad240-infer-author" "$out"
if [ -z "$(assignees 241)" ]; then ok "the refused inferred review takes no claim on its own issue"
else bad "the refused inferred review takes no claim on its own issue" "still assigned: $(assignees 241)"; fi

# Case 2 (red first #2): a second free lane exists -> the inferred review
# lands on it, silently -- this is what keeps inference usable rather than
# obstructive.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
out=$(LEDGER_STATE="$D/state-70" run 241 rev-500-noflag "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "with another free lane available, the inferred review IS dispatched" "$rc" 0 "$out"
want_contains "the author's lane is named and skipped, from inference alone" "skipping t:3" "$out"
log=$(tmuxlog)
want_contains "and lands on the OTHER free lane, t:4 (target t:@104)" "send-keys -t t:@104" "$log"
want_missing "never on the author's lane (t:3, target t:@103)" "send-keys -t t:@103 " "$log"

# Case 3 (red first #3): an ordinary dispatch -- no "review" + "PR #N" shape
# anywhere in the issue title or brief -- is unaffected. This is the
# regression that matters: an over-eager inference would block a normal fix
# pass by the PR's own author, stalling every PR.
printf '242|| add a missing null check\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-70" run 242 null-check "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "an ordinary dispatch with no review shape in title or brief is unaffected" "$rc" 0 "$out"
want_missing "nothing was inferred" "inferred --reviews-pr" "$out"
LEDGER_STATE="$D/state-70" ledger record-completion --task ad242-null-check --note done >/dev/null

# Inference also reads the BRIEF, not just the title, when the title alone
# does not name a PR -- e.g. a generic "do the review" issue whose brief is
# where the PR number actually lives.
BRIEF_REVIEW="$D/brief-review.md"
printf 'Review PR #500 for correctness and merge readiness.\n' > "$BRIEF_REVIEW"
printf '243|| do the review\n' >> "$D/issues"
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
out=$(LEDGER_STATE="$D/state-70" run 243 rev-500-brief "$BRIEF_REVIEW" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a review inferred from the BRIEF (title alone has no PR number) is refused when the only free lane is the author" "$rc" 1 "$out"
want_contains "and says the flag was inferred, from the brief" "inferred --reviews-pr 500 from the brief" "$out"

# An explicit --reviews-pr always wins and is never second-guessed by
# inference -- passing a DIFFERENT PR than the one the title/brief would
# have inferred must resolve the flag's PR, not the inferred one.
printf '244|| the code PR #501 was written from\n' >> "$D/issues"
printf '245|| review PR #500, but --reviews-pr says 501\n' >> "$D/issues"
printf '501|Fixes #244|lane/244-infer-explicit\n' >> "$D/prs"
out=$(LEDGER_STATE="$D/state-70" run 244 infer-explicit "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the second authoring dispatch (#244) succeeds" "$rc" 0 "$out"
LEDGER_STATE="$D/state-70" ledger record-completion --task ad244-infer-explicit --note done >/dev/null
out=$(LEDGER_STATE="$D/state-70" run 245 rev-explicit-wins "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 501); rc=$?
want_exit "the explicit --reviews-pr 501 is refused (its own author, t:3, is the only free lane)" "$rc" 1 "$out"
want_contains "names PR #501, the flag's PR, not #500 from the title" "PR #501" "$out"
want_missing "never inferred -- the explicit flag short-circuits detection" "inferred --reviews-pr" "$out"

# agent-supervisor#72: the repo-qualified form ("PR owner/repo#N") is the
# exact shape the Director's own review briefs use ("the independent review
# of PR jonhill90/agent-supervisor#N"), and it was missed entirely -- only
# bare "PR #N" was recognised. Same fixture shape as the #70 title tests
# above, just with the repo-qualified spelling.
printf '248|| the code PR #503 was written from\n' >> "$D/issues"
printf '249|| independent review of PR acme/agent-dotfiles#503 closing issue #240\n' >> "$D/issues"
printf '503|Fixes #248|lane/248-infer-qualified\n' >> "$D/prs"
out=$(LEDGER_STATE="$D/state-70" run 248 infer-qualified "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: the authoring dispatch (#248) succeeds" "$rc" 0 "$out"
LEDGER_STATE="$D/state-70" ledger record-completion --task ad248-infer-qualified --note done >/dev/null
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
out=$(LEDGER_STATE="$D/state-70" run 249 rev-503-qualified "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a review inferred from a repo-qualified PR reference (owner/repo#N) is refused when the only free lane is the author" "$rc" 1 "$out"
want_contains "and says the flag was inferred, from the issue title" "inferred --reviews-pr 503 from issue #249's title" "$out"
want_contains "names the PR" "PR #503" "$out"
# The line also names issue #240 right next to the PR -- confirm the wrong
# number (the issue being closed) was never picked up.
want_missing "never inferred the issue number instead of the PR number" "inferred --reviews-pr 240" "$out"

# A bare "owner/repo#N" with no "PR" word is this repo's own convention for
# citing an ISSUE inline (see "Fixes #240" fixtures throughout this file) --
# it must NOT be read as a PR reference, or an issue mention would silently
# become the inferred review PR.
printf '250|| review: see acme/agent-dotfiles#503 for the change, closing #240\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-70" run 250 no-bare-qualified "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a bare owner/repo#N with no 'PR' word is not inferred as a review" "$rc" 0 "$out"
want_missing "nothing was inferred" "inferred --reviews-pr" "$out"
LEDGER_STATE="$D/state-70" ledger record-completion --task ad250-no-bare-qualified --note done >/dev/null

# MUTATION-CHECK: disable the inference block and confirm a forgotten flag
# again dispatches straight to the author, the exact regression #70 exists
# to close.
# agent-supervisor#716: the inference block now lives in dispatch-preflight.sh.
MUTATED_70_DIR=$(make_mutant_scripts_dir)
MUTATED_70="$MUTATED_70_DIR/dispatch.sh"
patch_rc=0
PYTHONPATH="$HERE${PYTHONPATH:+:$PYTHONPATH}" python3 - "$MUTATED_70_DIR" <<'PY' || patch_rc=$?
import sys
import _dispatch_mutate as M
target = sys.argv[1]
marker = 'if [ -z "$REVIEWS_PR" ] && [ -z "$NOT_A_REVIEW" ]; then\n  INFER_GH_REPO_ARGS=()'
M.patch(target, marker, 'if false; then  # MUTATED: inference disabled\n  INFER_GH_REPO_ARGS=()')
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh with inference disabled" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh with inference disabled"
  chmod +x "$MUTATED_70"
  cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
  printf '246|| the code PR #502 was written from\n' >> "$D/issues"
  printf '247|| review PR #502, mutant\n' >> "$D/issues"
  printf '502|Fixes #246|lane/246-infer-mutant\n' >> "$D/prs"
  LEDGER_STATE="$D/state-70-mutant" run 246 infer-mutant "$D/brief.md" acme/agent-dotfiles "$REPO" >/dev/null
  LEDGER_STATE="$D/state-70-mutant" ledger record-completion --task ad246-infer-mutant --note done >/dev/null
  out=$(DISPATCH_SCRIPT="$MUTATED_70" LEDGER_STATE="$D/state-70-mutant" \
        run 247 rev-502-mutant "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
  want_exit "mutation confirmed: with inference disabled, a forgotten flag dispatches again" "$rc" 0 "$out"
  log=$(tmuxlog)
  want_contains "mutation confirmed: straight to the author's own lane, t:3 (target t:@103)" "send-keys -t t:@103" "$log"
fi

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
