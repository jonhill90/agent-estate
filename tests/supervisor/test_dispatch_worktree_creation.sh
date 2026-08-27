#!/bin/bash
# dispatch.sh must create the lane's worktree itself, and a worktree failure
# must abort the dispatch instead of sending the brief into the shared
# checkout (agent-dotfiles#81 -- see the top-level test_dispatch.sh history
# this file was split from for the full WHY). This file covers that seam:
# worktree creation succeeding, the worktree's cwd matching the ledger's
# record (#15), the [repo]/[repo-path] mismatch guard (#17), a missing
# brief file, an already-claimed issue, and a closed issue (#95) -- each one
# checked for the same two things: no worktree left behind, and no GitHub
# claim left behind, when the dispatch is refused.
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

echo "dispatch.sh -- worktree creation and its failure modes (#15/#17/#81/#95)"

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

# --- the whole point: dispatch creates the worktree itself ----------------
# Pinned rather than left to run()'s implicit mktemp default (see #174's own
# comment on `run()` above): #15's own assertions below read this ledger back
# after the dispatch, so they need to know where it landed.
LEDGER_STATE="$D/state-81"
out=$(run 81 dispatch-worktree "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a dispatch to a free lane succeeds" "$rc" 0 "$out"

WT=$(ls -d "$D"/roots/*81* 2>/dev/null | head -1)
if [ -n "$WT" ] && git -C "$WT" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  ok "dispatch created the lane's worktree without being told to"
else
  bad "dispatch created the lane's worktree without being told to" "$out"
fi
branch=$(git -C "${WT:-/nonexistent}" branch --show-current 2>/dev/null)
want_contains "the worktree is on its own lane branch" "81-dispatch-worktree" "$branch"

log=$(tmuxlog)
want_contains "the brief is sent to the free lane, by its window id" "send-keys -t t:@103" "$log"
want_contains "the lane is told which worktree to work in" "${WT:-NO-WORKTREE}" "$log"
want_contains "the lane is pointed at the brief" "$D/brief.md" "$log"
want_contains "the window is renamed to say what is running" "rename-window" "$log"
want_contains "the window name carries the issue number" "ad81-dispatch-worktree" "$log"
want_contains "the lane is cleared before reuse" "/clear" "$log"
want_contains "the issue is claimed before the brief goes out" "jonhill90" "$(assignees 81)"

want_contains "the brief is submitted, not left sitting in the input" "send-keys -t t:@103 Enter" "$log"

# --- #15: the lane's cwd, not just the brief's TEXT, is the worktree ------
# The bug: dispatch.sh named the worktree in the message it typed but never
# put the lane's own process there -- measured live via `lsof -d cwd` on the
# pane's pid resolving to the shared checkout. This stub cannot run `lsof`
# (there is no real process behind a fixture pane), but `respawn-pane -c` is
# the ONE call in dispatch.sh that can change a pane's OS-level cwd, so
# asserting it was made, with the worktree as `-c`, and BEFORE anything else
# is typed, is the equivalent check against this stub's own model of a pane
# (`#{pane_current_path}`, which the stub now updates on respawn-pane the
# same way real tmux updates the real thing).
want_contains "the lane's pane is respawned into its worktree" "respawn-pane -k -t t:@103 -c ${WT:-NO-WORKTREE}" "$log"
respawn_line=$(grep -n '^respawn-pane' <<<"$log" | head -1 | cut -d: -f1)
rename_line=$(grep -n '^rename-window' <<<"$log" | head -1 | cut -d: -f1)
if [ -n "$respawn_line" ] && [ -n "$rename_line" ] && [ "$respawn_line" -lt "$rename_line" ]; then
  ok "the respawn happens before the lane is renamed or given anything to type"
else
  bad "the respawn happens before the lane is renamed or given anything to type" "respawn at line $respawn_line, rename at line $rename_line in: $log"
fi
want_contains "the harness is relaunched, into the worktree, right after the respawn" "claude --model sonnet --dangerously-skip-permissions" "$log"
recorded_path=$(AGENT_SUPERVISOR_STATE_DIR="${LEDGER_STATE:-$D/state}" python3 "$HERE/../../scripts/supervisor/cli.py" status 2>/dev/null | grep -oE '"repo":"[^"]*"' | head -1 | sed -E 's/.*:"([^"]*)"/\1/')
want_contains "the ledger records the lane's cwd as the worktree, not the shared checkout" "${WT:-NO-WORKTREE}" "$recorded_path"

# --- #421: the routine dispatch respawn gets the same tmux guard on PATH ---
# bootstrap-session.sh/restore.sh already prefix the guard's bin dir onto
# PATH ahead of the harness launch command; an independent review of #166
# found this respawn-pane call site (the one every ordinary dispatch goes
# through) untouched by that fix. Asserted against the SAME recorded
# process a real respawn would start (`.respawn-cmd`, see #236's own
# comment on that file above), not the typed-keys log -- the guard has to
# be part of the pane's actual launched process, not text sent afterward.
respawn_cmd_81=$(cat "$D/panes/3.respawn-cmd" 2>/dev/null || true)
# agent-supervisor#521 prefixed harness/claude.sh's own launch command with
# CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false ahead of `claude` -- the guard's
# PATH assignment above stays the FIRST word (it is prepended onto whatever
# HARNESS_LAUNCH_CMD already is), so this moves with it the same way #494's
# --strict-mcp-config did, not weakened to tolerate it.
want_contains "agent-supervisor#421: the guard's bin dir is prefixed onto PATH ahead of the launch command" \
  "PATH=\"${LEDGER_STATE}/tmux-guard/bin:\$PATH\" CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false claude" "$respawn_cmd_81"

# Every case after this one relies on run()'s implicit per-call mktemp state
# dir (see its own comment above) -- unset so LEDGER_STATE pinned just above
# for this one assertion cannot leak into any of them.
unset LEDGER_STATE

: > "$D/tmux.log"
# AGENT_SUPERVISOR_STATE_DIR is pinned here (unlike run()'s implicit mktemp
# default) so the #421 guard-install this rehome path now triggers writes
# its wrapper under this test's own sandbox, never the real
# $HOME/.local/state/agent-dotfiles-supervisor tmux_guard_bin_dir() falls
# back to when the env var is unset.
REHOME_STATE="$D/state-rehome"
rehome_out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/lanes" TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" \
  AGENT_SUPERVISOR_STATE_DIR="$REHOME_STATE" \
  DISPATCH_RESPAWN_SETTLE=0 "$DISPATCH" --rehome-lane t:@103 "$REPO" claude 2>&1); rehome_rc=$?
want_exit "the supported re-home verb succeeds" "$rehome_rc" 0 "$rehome_out"
log=$(tmuxlog)
want_contains "the supported re-home verb respawns the pane into an existing directory" "respawn-pane -k -t t:@103 -c $REPO" "$log"
want_contains "the supported re-home verb relaunches the harness" "claude --model sonnet --dangerously-skip-permissions" "$log"
respawn_cmd_rehome=$(cat "$D/panes/3.respawn-cmd" 2>/dev/null || true)
# Same #521 shape as the assertion above -- moves with harness/claude.sh's
# launch command, not weakened.
want_contains "agent-supervisor#421: re-homing a lane also prefixes the guard's bin dir onto PATH" \
  "PATH=\"${REHOME_STATE}/tmux-guard/bin:\$PATH\" CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false claude" "$respawn_cmd_rehome"
pane_path=$(cat "$D/panes/3.path" 2>/dev/null || true)
want_contains "the supported re-home verb updates the pane cwd" "$REPO" "$pane_path"

# --- a mangled brief is not a delivered brief -----------------------------
# Observed live on 2026-08-11 building this: characters typed straight after
# `/clear` were swallowed while the harness repainted, and the lane's prompt
# read `/var/.../brief.md and do exactly what it says` -- `Read ` gone. A lane
# acts on a truncated brief anyway, so "sent" is not the thing to check; what
# the pane shows is. The stub drops the first 40 characters of the first
# typing attempt, and the retype must recover it.
printf '83|| dropped once\n84|| dropped always\n' >> "$D/issues"
out=$(DISPATCH_DROP_PREFIX=40 run 83 dropped-prefix "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a dropped prefix is retyped, not shipped mangled" "$rc" 0 "$out"
log=$(tmuxlog)
want_contains "the mangled input is cleared before retyping" "send-keys -t t:@103 C-u" "$log"
want_contains "the retyped brief is the one submitted" "send-keys -t t:@103 Enter" "$log"

# ...and if it never lands intact, nothing is submitted at all. This wrapper
# makes EVERY typing attempt lose its prefix, not just the first.
cp "$D/bin/tmux" "$D/bin/tmux-real"
cat > "$D/bin/tmux" <<EOS
#!/bin/bash
rm -f "$D/panes"/*.dropped
exec "$D/bin/tmux-real" "\$@"
EOS
chmod +x "$D/bin/tmux"
before=$(worktrees)
out=$(DISPATCH_DROP_PREFIX=40 run 84 always-dropped "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a brief that never lands intact fails the dispatch" "$rc" 1 "$out"
log=$(tmuxlog)
# #15: the log now opens with the harness-relaunch step's OWN bare
# `send-keys ... Enter` (submitting the launch command into the freshly
# respawned pane) -- unrelated to whether the BRIEF was submitted, and a
# whole-log `want_missing` cannot tell the two apart. Everything this
# assertion actually cares about happens from the rename onward, same as
# every other case in this run that reads `log` after the relaunch step.
log_after_rename_before_rehome=$(sed -n '/^rename-window/,/^respawn-pane/{/^respawn-pane/!p;}' <<<"$log")
want_missing "a mangled brief is never submitted" "send-keys -t t:@103 Enter" "$log_after_rename_before_rehome"
if [ "$(assignees 84)" = "" ]; then ok "a mangled brief releases the claim"; else bad "a mangled brief releases the claim" "assignees: $(assignees 84)"; fi
if [ "$(worktrees)" = "$before" ]; then ok "a mangled brief leaves no worktree behind"; else bad "a mangled brief leaves no worktree behind" "$before -> $(worktrees)"; fi
pane_path=$(cat "$D/panes/3.path" 2>/dev/null || true)
if [ -n "$pane_path" ] && [ -d "$pane_path" ]; then
  ok "a rollback does not leave the lane sitting in a deleted cwd"
else
  bad "a rollback does not leave the lane sitting in a deleted cwd" "pane path: ${pane_path:-<missing>}"
fi
cp "$D/bin/tmux-real" "$D/bin/tmux"

# --- THE LOAD-BEARING CASE: no worktree, no dispatch ---------------------
# A lane with no worktree falls back to the shared checkout, which is #73.
# Failing loudly and sending nothing is the only safe outcome.
before=$(worktrees)
out=$(run 82 broken-repo "$D/brief.md" acme/agent-dotfiles "$D/not-a-git-repo"); rc=$?
want_exit "a failed worktree fails the dispatch" "$rc" 1 "$out"
log=$(tmuxlog)
want_missing "no brief is sent when the worktree could not be created" "send-keys" "$log"
want_contains "the failure says why" "worktree" "$out"
if [ "$(assignees 82)" = "" ]; then
  ok "the claim is released when the dispatch aborts"
else
  bad "the claim is released when the dispatch aborts" "assignees: $(assignees 82)"
fi
if [ "$(worktrees)" = "$before" ]; then ok "no stray worktree is left behind"; else bad "no stray worktree is left behind" "$before -> $(worktrees)"; fi

# --- agent-supervisor#17: the worktree must actually BE [repo] ------------
# `dispatch.sh <issue> <slug> <brief> [repo] [repo-path]` claimed against
# [repo] but built the worktree from [repo-path], and nothing compared the
# two -- so a cross-repo dispatch (repo-path a checkout of some OTHER repo)
# silently claimed one repository and dropped the lane into a worktree of
# another. A second, independent clone with a DIFFERENT origin stands in for
# that other repo.
git init -q --bare "$D/other-origin.git"
git clone -q "$D/other-origin.git" "$D/other-repo" 2>/dev/null
git -C "$D/other-repo" config user.email test@example.com
git -C "$D/other-repo" config user.name "Test"
git -C "$D/other-repo" checkout -q -b main
echo other > "$D/other-repo/file.txt"
git -C "$D/other-repo" add file.txt
git -C "$D/other-repo" commit -q -m "initial"
git -C "$D/other-repo" push -q -u origin main
git -C "$D/other-repo" remote set-url origin "git@github.com:acme/other-repo.git"

printf '17|| the worktree must actually be the claimed repo\n' >> "$D/issues"
before=$(worktrees)
out=$(run 17 worktree-repo-mismatch "$D/brief.md" acme/agent-dotfiles "$D/other-repo"); rc=$?
want_exit "a worktree whose origin does not match [repo] refuses the dispatch" "$rc" 1 "$out"
want_contains "...and the refusal names the CLAIMED repo" "acme/agent-dotfiles" "$out"
want_contains "...and the repo the worktree actually is" "acme/other-repo" "$out"
log=$(tmuxlog)
want_missing "no brief is sent on a repo mismatch" "send-keys -t t:@103 " "$log"
if [ "$(assignees 17)" = "" ]; then
  ok "the claim is released on a repo mismatch"
else
  bad "the claim is released on a repo mismatch" "assignees: $(assignees 17)"
fi
if [ "$(worktrees)" = "$before" ]; then ok "a normal rollback still removes an unused worktree"; else bad "a normal rollback still removes an unused worktree" "$before -> $(worktrees)"; fi

# THE REGRESSION GUARD (#17): origin == [repo] -> dispatch proceeds
# unchanged. A check that refuses every dispatch is not a fix -- every case
# elsewhere in this file already exercises this path (their fixture's origin
# was set to match "acme/agent-dotfiles" above for exactly this reason), and
# this case makes the guard's positive path an explicit, named assertion
# rather than an implication of everything else in the file staying green.
printf '18|| a matching repo dispatches normally\n' >> "$D/issues"
out=$(run 18 worktree-repo-match "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a worktree whose origin matches [repo] dispatches normally" "$rc" 0 "$out"

# MUTATION-CHECK: disable the comparison and confirm the mismatch case above
# goes red -- a check present in the diff but never actually reached would
# leave this suite passing regardless.
MUTATED_17="$D/dispatch-no-origin-check.sh"
patch_rc=0
python3 - "$DISPATCH" "$MUTATED_17" <<'PY' || patch_rc=$?
import os
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = 'if [ -n "$REPO" ]; then\n  WORKTREE_ORIGIN='
assert text.count(marker) == 1, "origin check not found or not unique -- script shape changed"
text = text.replace(marker, 'if false; then\n  WORKTREE_ORIGIN=', 1)
here = 'HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"'
assert text.count(here) == 1, "HERE assignment not found or not unique -- script shape changed"
text = text.replace(here, 'HERE=%r' % os.path.dirname(os.path.abspath(src)), 1)
open(dst, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh whose origin check is disabled" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  chmod +x "$MUTATED_17"
  ok "setup: patched a copy of dispatch.sh whose origin check is disabled"
  printf '19|| mutation: origin check disabled\n' >> "$D/issues"
  mut_out=$(DISPATCH_SCRIPT="$MUTATED_17" run 19 mutation-check "$D/brief.md" acme/agent-dotfiles "$D/other-repo"); mut_rc=$?
  want_exit "mutation confirmed: with the origin check disabled, the mismatch case dispatches anyway (the assertion above would now be red)" "$mut_rc" 0 "$mut_out"
fi

# --- agent-supervisor#17: [repo] given, [repo-path] omitted is a trap -----
# [repo-path] defaults to $PWD, so [repo] alone reads as "target that repo"
# but silently builds the worktree from wherever dispatch.sh happened to run.
# Invoked directly (not through run(), which always supplies both) with only
# 4 positional args.
printf '20|| repo given, repo-path omitted\n' >> "$D/issues"
: > "$D/tmux.log"
rm -rf "$D/panes"; mkdir -p "$D/panes"
NO_PATH_OUT=$(cd "$REPO" && PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
  LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
  TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 \
  AGENT_SUPERVISOR_STATE_DIR="$(mktemp -d "$D/state.XXXXXX")" \
  STUB_PANE_PATH="$REPO" WORKTREE_ROOT="$D/roots" \
  "$DISPATCH" 20 repo-path-omitted "$D/brief.md" acme/agent-dotfiles 2>&1); NO_PATH_RC=$?
want_exit "[repo] with [repo-path] omitted refuses rather than silently use \$PWD" "$NO_PATH_RC" 2 "$NO_PATH_OUT"
want_contains "...and explains the opt-in" "DISPATCH_ALLOW_CWD_REPO_PATH" "$NO_PATH_OUT"
if [ -z "$(assignees 20)" ]; then ok "the refused dispatch takes no claim on its own issue"
else bad "the refused dispatch takes no claim on its own issue" "still assigned: $(assignees 20)"; fi

# ...and the explicit opt-in is honoured: with DISPATCH_ALLOW_CWD_REPO_PATH=1,
# the same 4-argument call uses $PWD (here, $REPO, whose origin matches
# [repo]) and proceeds.
printf '21|| repo given, repo-path omitted, opted in\n' >> "$D/issues"
: > "$D/tmux.log"
rm -rf "$D/panes"; mkdir -p "$D/panes"
OPT_IN_OUT=$(cd "$REPO" && PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
  LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
  TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 DISPATCH_ALLOW_CWD_REPO_PATH=1 \
  AGENT_SUPERVISOR_STATE_DIR="$(mktemp -d "$D/state.XXXXXX")" \
  STUB_PANE_PATH="$REPO" WORKTREE_ROOT="$D/roots" \
  "$DISPATCH" 21 repo-path-opt-in "$D/brief.md" acme/agent-dotfiles 2>&1); OPT_IN_RC=$?
want_exit "DISPATCH_ALLOW_CWD_REPO_PATH=1 opts into \$PWD explicitly" "$OPT_IN_RC" 0 "$OPT_IN_OUT"

# --- already claimed: pick different work, do not build anything ---------
# The issue and slug here must be UNIQUE to this case. This case used to reuse
# #81's number and slug, whose lane branch already existed from the happy path
# earlier in this same run -- so with the claim guard deleted entirely,
# `worktree.sh new` still failed, for the unrelated reason of a duplicate
# branch, and the resulting exit-1/no-send outcome coincidentally matched every
# assertion below. The suite stayed 32/32 green with the guard gone. A fresh
# number and slug is what makes the claim the only thing that can refuse here.
printf '97|someone-else| Claimed by another lane\n' >> "$D/issues"
before=$(worktrees)
out=$(run 97 already-claimed "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a claimed issue is refused" "$rc" 1 "$out"
want_contains "the refusal names the holder of the claim" "someone-else" "$out"
log=$(tmuxlog)
want_missing "a refused claim sends no brief" "send-keys" "$log"
want_missing "a refused claim does not rename the lane" "rename-window" "$log"
want_contains "a refused claim leaves the other lane's claim alone" "someone-else" "$(assignees 97)"
if [ "$(worktrees)" = "$before" ]; then ok "a refused claim creates no worktree"; else bad "a refused claim creates no worktree" "$before -> $(worktrees)"; fi

# --- no free lane: an empty tmux target hits the ACTIVE window ------------
# `send-keys -t t:` with an empty index does not error, it targets whatever
# window is active -- usually the supervisor. That happened on 2026-08-11.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
2|ad82-other|claude.exe|esc to interrupt 3s|1|0
FIX
: > "$D/issues"
printf '90|| Needs a lane\n' > "$D/issues"
before=$(worktrees)
out=$(run 90 no-lane "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "no free lane fails the dispatch" "$rc" 1 "$out"
log=$(tmuxlog)
want_missing "nothing is sent when no lane is free" "send-keys" "$log"
if [ "$(assignees 90)" = "" ]; then ok "no lane means no claim is taken"; else bad "no lane means no claim is taken" "assignees: $(assignees 90)"; fi
if [ "$(worktrees)" = "$before" ]; then ok "no lane means no worktree is created"; else bad "no lane means no worktree is created" "$before -> $(worktrees)"; fi

# --- a missing brief file is caught before anything is claimed -----------
out=$(run 90 no-brief "$D/does-not-exist.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a missing brief fails the dispatch" "$rc" 1 "$out"
if [ "$(assignees 90)" = "" ]; then ok "a missing brief takes no claim"; else bad "a missing brief takes no claim" "assignees: $(assignees 90)"; fi

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
