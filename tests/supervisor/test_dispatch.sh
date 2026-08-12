#!/bin/bash
# dispatch.sh must be the thing that CALLS lanes.sh, claim.sh and worktree.sh,
# so a lane cannot be handed work without a worktree of its own.
#
# WHY: agent-dotfiles#81. worktree.sh was built for #73 and, at the time, no
# script invoked `new`, `done` or `guard` -- the only references to it were
# three code fences in loop-tick.md and a section of the supervisor README.
# Enforcement was "the dispatcher reads the file and runs the command", which
# is the same mechanism whose failure produced #73 in the first place. The
# lesson from acp_transport.py (tested, zero importers) and from claim.sh
# (#74, wired the same day it landed) is that a tool with no caller is a
# documentation rule with a binary attached.
#
# The load-bearing test here is `a failed worktree aborts the dispatch`: if
# worktree.sh new fails and the brief goes out anyway, the lane works in the
# shared checkout, which IS #73. Sending nothing is the correct outcome.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DISPATCH="$HERE/../../scripts/supervisor/dispatch.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

echo "dispatch.sh"

D=$(mktemp -d); mkdir -p "$D/bin" "$D/roots"
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cp "$HERE/stubs/tmux-dispatch" "$D/bin/tmux"

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
  PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
    TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 \
    DISPATCH_DROP_PREFIX="${DISPATCH_DROP_PREFIX:-0}" \
    DISPATCH_LANE="${DISPATCH_LANE:-}" \
    DISPATCH_PANE_ROWS="${DISPATCH_PANE_ROWS:-}" \
    DISPATCH_PANE_COLS="${DISPATCH_PANE_COLS:-60}" \
    DISPATCH_MESSAGE_BUDGET="${DISPATCH_MESSAGE_BUDGET:-430}" \
    AGENT_SUPERVISOR_STATE_DIR="${LEDGER_STATE:-$(mktemp -d "$D/state.XXXXXX")}" \
    STUB_PANE_PATH="${STUB_PANE_PATH:-$REPO}" \
    DISPATCH_SWALLOW_ENTER="${DISPATCH_SWALLOW_ENTER:-0}" \
    DISPATCH_CONFIRM_TRIES="${DISPATCH_CONFIRM_TRIES:-2}" \
    WORKTREE_ROOT="$D/roots" bash "${DISPATCH_SCRIPT:-$DISPATCH}" "$@" 2>&1
}
# AGENT_SUPERVISOR_STATE_DIR is not optional in this harness. Without it the
# ledger record dispatch.sh now writes (#140) would land in the REAL supervisor
# state directory under $HOME -- a test suite writing into the live estate's
# ledger. LEDGER_STATE overrides it for the cases that need a broken one.
ledger() { AGENT_SUPERVISOR_STATE_DIR="${LEDGER_STATE:-$D/state}" python3 "$HERE/../../scripts/supervisor/cli.py" "$@"; }
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

# --- the whole point: dispatch creates the worktree itself ----------------
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
want_missing "a mangled brief is never submitted" "send-keys -t t:@103 Enter" "$log"
if [ "$(assignees 84)" = "" ]; then ok "a mangled brief releases the claim"; else bad "a mangled brief releases the claim" "assignees: $(assignees 84)"; fi
if [ "$(worktrees)" = "$before" ]; then ok "a mangled brief leaves no worktree behind"; else bad "a mangled brief leaves no worktree behind" "$before -> $(worktrees)"; fi
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

# --- agent-dotfiles#216: a copilot lane, GREEN against the stub -----------
#
# The bug's own reproduction, against a stub instead of the live
# council-copilot pane #216 explicitly forbids touching: a lane running
# `node` -- copilot's process name is indistinguishable from any other Node
# harness -- reads `free` from `lanes.sh` and is STILL refused by
# `dispatch.sh` before this fix, because `cli.py lane-free`'s backfill could
# only map `codex`/`claude`/`claude.exe` process names to a harness. Once the
# pane's harness is a RECORDED fact (the `@hill90_lane_harness` option
# `bootstrap-session.sh`/`cli.py register` would have set), the same dispatch
# reaches it end to end: claimed, briefed, and recorded with the harness
# that was actually running, not a guess from `node`.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
7|free-7|node|← open sidebar|1|0
FIX
printf '216|| a copilot lane must be dispatchable\n' >> "$D/issues"
RUN_PRESEED_PANES='tmux set-option -p -t t:7 @hill90_lane_harness copilot' \
  out=$(LEDGER_STATE="$D/state-216" run 216 copilot-lane "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a dispatch to a recorded-copilot lane succeeds" "$rc" 0 "$out"
log=$(tmuxlog)
want_contains "the brief reaches the copilot lane, by its window id" "send-keys -t t:@107" "$log"
want_contains "the brief is submitted to the copilot lane" "send-keys -t t:@107 Enter" "$log"
status=$(LEDGER_STATE="$D/state-216" ledger status 2>&1)
want_contains "the lane the brief went to is recorded" '"lane":"t:7"' "$status"
want_contains "the RECORDED harness is copilot, not guessed from 'node'" '"harness":"copilot"' "$status"

# The unidentifiable case still refuses (agent-dotfiles#216's own rule): the
# SAME node lane with no harness option recorded must not be dispatched to --
# "cannot tell" staying refused is correct behaviour, not the defect.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
8|free-8|node|← open sidebar|1|0
FIX
printf '217|| an unrecorded node lane must stay refused\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-217" run 217 unrecorded-node "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a dispatch with no free lane refuses" "$rc" 1 "$out"
want_contains "the refusal names the unidentifiable lane" "no free lane" "$out"
log=$(tmuxlog)
want_missing "nothing was ever sent to the unrecorded lane" "send-keys -t t:@108" "$log"

# --- THE LANE_META SANITY GUARD (agent-dotfiles#144 finding 4) ------------
#
# The pane-identity probe the ledger recording block makes right before
# `record-dispatch` can itself fail against a real tmux -- a target it
# cannot resolve prints a single-line error, not the pipe-joined template
# dispatch.sh expects. The guard exists to catch that BEFORE `IFS='|' read`
# scatters an error string across PANE_ID/PANE_CMD/etc and hands it to
# cli.py as if it were real pane identity. The brief must still go out --
# this is a bookkeeping failure, not a dispatch failure, same contract as
# #140's own ledger-failure tolerance.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '145|| a dispatch whose pane-identity probe itself fails\n' >> "$D/issues"
# Pre-registered so agent-dotfiles#174's OWN pane-identity read (lane
# selection's first-sight backfill, step 1) does not also hit
# STUB_LANE_META_BROKEN and refuse the lane before this test's actual target
# -- the step 6 probe -- is ever reached. With the lane already known-free,
# step 1 answers from the ledger alone and never touches tmux.
preregister_lane "$D/state-145" t:3 t:3
export STUB_LANE_META_BROKEN=1
out=$(LEDGER_STATE="$D/state-145" run 145 ledger-meta-broken "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
unset STUB_LANE_META_BROKEN
want_exit "a broken pane-identity probe does NOT abort the dispatch" "$rc" 0 "$out"
log=$(tmuxlog)
want_contains "the brief still goes out" "send-keys -t t:@103" "$log"
want_contains "and is still submitted" "send-keys -t t:@103 Enter" "$log"
want_contains "the guard names the failure as an unreadable pane probe" \
  "could not read pane metadata" "$out"
status=$(LEDGER_STATE="$D/state-145" ledger status 2>&1)
want_contains "the pre-registered lane is still the only one on record" '"lane":"t:3"' "$status"
# agent-dotfiles#184: this used to assert `"tasks":[]` -- true before this
# dispatch's own step-1 `claim-lane` call existed, because nothing wrote to
# the tasks table until step 6, and step 6 never runs a real record-dispatch
# call down this branch (the malformed probe is caught before it). Now step
# 1's claim placeholder is what is left behind, and that is correct, not
# garbage: the brief DID go out (asserted above), so the lane must keep
# reading occupied even though bookkeeping past the claim degraded -- the
# same property #188 added `mark_lane_held` for on the record-dispatch side.
want_contains "the claim placeholder is what is left recording the lane occupied" \
  '"id":"ledger-claim:t:3:ad145-ledger-meta-broken"' "$status"

# ...and that guard is load-bearing. Patch a copy that always takes the
# "well-formed" branch regardless of what LANE_META actually contains, and
# confirm the specific reason string above disappears -- a suite that still
# reports "could not read pane metadata" with the guard removed has not
# tested the guard, only the ledger-failure tolerance underneath it.
BROKEN_META_GUARD="$D/dispatch-lane-meta-unguarded.sh"
patch_rc=0
python3 - "$DISPATCH" "$BROKEN_META_GUARD" <<'PY' || patch_rc=$?
import os
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = 'if [ -z "$LANE_META" ] || [[ "$LANE_META" != *"|"* ]]; then'
assert marker in text, "LANE_META guard not found -- script shape changed"
assert text.count(marker) == 1, "LANE_META guard not unique -- script shape changed"
text = text.replace(marker, "if false; then  # MUTATED: guard always skipped", 1)
here = 'HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"'
assert text.count(here) == 1, "HERE assignment not found or not unique -- script shape changed"
text = text.replace(here, 'HERE=%r' % os.path.dirname(os.path.abspath(src)), 1)
open(dst, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh whose LANE_META guard is skipped" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh whose LANE_META guard is skipped"
  printf '146|| the same broken probe, against the unguarded copy\n' >> "$D/issues"
  preregister_lane "$D/state-146" t:3 t:3
  export STUB_LANE_META_BROKEN=1
  out=$(DISPATCH_SCRIPT="$BROKEN_META_GUARD" LEDGER_STATE="$D/state-146" \
        run 146 ledger-meta-unguarded "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
  unset STUB_LANE_META_BROKEN
  if ! grep -qF "could not read pane metadata" <<<"$out"; then
    ok "mutation confirmed: skipping the guard loses the specific pane-probe diagnosis (the assertion above would now be red)"
  else
    bad "mutation confirmed: skipping the guard loses the specific pane-probe diagnosis" \
      "the unguarded copy still reported 'could not read pane metadata' -- the patch missed the real guard: $out"
  fi
  want_exit "the unguarded copy still does not abort the dispatch" "$rc" 0 "$out"
fi

# --- AN UNREADABLE LEDGER REFUSES TO DISPATCH (agent-dotfiles#174) --------
#
# The inversion from #140. That ledger write was made non-fatal precisely
# BECAUSE nothing read it -- see step 6's comment. Step 1 above now reads it
# for every candidate lane before picking one, so an unreadable ledger can no
# longer mean "proceed as if every lane were free"; it has to mean "cannot
# tell, refuse". This is issue test 4.
#
# The break is a state directory that cannot exist -- a path whose parent is a
# regular file -- so the ledger genuinely errors rather than being skipped.
# Checked before any claim or worktree: nothing about this issue is touched.
printf '141|| a dispatch whose ledger cannot be read\n' >> "$D/issues"
before=$(worktrees)
out=$(LEDGER_STATE="$D/brief.md/state" run 141 ledger-broken "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "an unreadable ledger refuses the dispatch" "$rc" 1 "$out"
log=$(tmuxlog)
want_missing "nothing is sent when the ledger cannot be read" "send-keys" "$log"
want_contains "and says the ledger is why" "ledger is unreadable" "$out"
if [ "$(assignees 141)" = "" ]; then ok "an unreadable ledger takes no claim"; else bad "an unreadable ledger takes no claim" "assignees: $(assignees 141)"; fi
if [ "$(worktrees)" = "$before" ]; then ok "an unreadable ledger creates no worktree"; else bad "an unreadable ledger creates no worktree" "$before -> $(worktrees)"; fi

# ...and that guard is load-bearing. Patch a copy that skips the step-0 check
# and confirm the case above goes red against it.
BROKEN_READ_GUARD="$D/dispatch-ledger-read-unguarded.sh"
patch_rc=0
python3 - "$DISPATCH" "$BROKEN_READ_GUARD" <<'PY' || patch_rc=$?
import os
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = 'if ! LEDGER_STATUS_OUT=$("$LEDGER_PYTHON" "$LEDGER_CLI" status 2>&1); then'
assert marker in text, "ledger readability guard not found -- script shape changed"
assert text.count(marker) == 1, "ledger readability guard not unique -- script shape changed"
text = text.replace(marker, "if false; then  # MUTATED: readability guard always skipped", 1)
here = 'HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"'
assert text.count(here) == 1, "HERE assignment not found or not unique -- script shape changed"
text = text.replace(here, 'HERE=%r' % os.path.dirname(os.path.abspath(src)), 1)
open(dst, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh whose ledger readability guard is skipped" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh whose ledger readability guard is skipped"
  printf '147|| the same broken ledger, against the unguarded copy\n' >> "$D/issues"
  out=$(DISPATCH_SCRIPT="$BROKEN_READ_GUARD" LEDGER_STATE="$D/brief.md/state" \
        run 147 ledger-read-unguarded "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
  if ! grep -qF "ledger is unreadable" <<<"$out"; then
    ok "mutation confirmed: skipping the guard loses the up-front refusal (the assertion above would now be red)"
  else
    bad "mutation confirmed: skipping the guard loses the up-front refusal" \
      "the unguarded copy still reported 'ledger is unreadable' -- the patch missed the real guard: $out"
  fi
fi

# --- A LEDGER *WRITE* FAILURE STILL DOES NOT ABORT A DISPATCH -------------
#
# #140's original property, narrowed by #174 rather than dropped: once a lane
# is ALREADY known free (so step 1 needed no write of its own -- see the
# lane-free command's docstring), a dispatch proceeds through claim, worktree
# and a real, verified send before step 6 ever touches the ledger again. If
# THAT final write fails, the brief has already reached a live pane; unwinding
# the claim and the worktree at that point would strand a worker that is
# actually running, which is worse than one stale ledger row. See step 6's
# own comment for the full argument.
#
# The break is deliberately an APPLICATION-level conflict, not a filesystem
# one: a task already exists under the exact id this dispatch's window name
# will produce (`ad148-ledger-write-broken`), assigned to a DIFFERENT lane.
# `Ledger.record_dispatch`'s own assign step refuses that outright (agent-
# dotfiles#144 finding 2's docstring). A read-only sqlite file was tried
# first and dropped: whether SQLite's WAL machinery lets a given read
# through after the main file is chmod'd read-only turned out to depend on
# per-process lock/checkpoint state and was not deterministic across
# separate `cli.py` invocations -- exactly the kind of flake this suite
# should not carry. Reads (step 0, step 1) are untouched by this seed; only
# the write `record-dispatch` performs at the very end collides.
LSTATE="$D/state-148"
seed_conflicting_task "$LSTATE" t:3 ad148-ledger-write-broken 148
printf '148|| a dispatch whose final ledger write fails\n' >> "$D/issues"
out=$(LEDGER_STATE="$LSTATE" run 148 ledger-write-broken "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a ledger write that collides still does not abort the dispatch" "$rc" 0 "$out"
log=$(tmuxlog)
want_contains "the brief still goes out" "send-keys -t t:@103" "$log"
want_contains "and is still submitted" "send-keys -t t:@103 Enter" "$log"
want_contains "the write failure is loud, not swallowed" "LEDGER RECORD FAILED" "$out"
want_contains "and says which dispatch lost its record" "ad148-ledger-write-broken" "$out"
want_contains "the claim is NOT unwound over a bookkeeping failure" "jonhill90" "$(assignees 148)"

# agent-dotfiles#188 finding 1: lane t:3 was ALREADY registered free before
# this dispatch (seed_conflicting_task's first register_lane call) -- the
# case #144's own recovery argument does not cover. `record_dispatch` rolled
# back every write it attempted for t:3, which restores that pre-existing
# free row unless the caller (`cli.py record_dispatch`) explicitly closes it.
# A lane running a live, unrecorded brief must never read free again.
free_check=$(AGENT_SUPERVISOR_STATE_DIR="$LSTATE" python3 "$HERE/../../scripts/supervisor/cli.py" \
  lane-free --lane t:3 --target t:3 --window-name ad148-ledger-write-broken 2>&1)
want_missing "the lane a failed record just wrote to no longer reads free" '"free":true' "$free_check"
want_contains "the ledger already knows this lane, so the answer is not a name-based backfill" '"known":true' "$free_check"

# ...and that tolerance is load-bearing. Patch a copy that makes the write
# fatal and confirm the case above goes red against it -- a suite that still
# passes with the failure-tolerance removed has not tested the property.
BROKEN_DISPATCH="$D/dispatch-ledger-fatal.sh"
patch_rc=0
python3 - "$DISPATCH" "$BROKEN_DISPATCH" <<'PY' || patch_rc=$?
import os
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = "  return 0  # the ledger write is never fatal -- agent-dotfiles#140"
assert marker in text, "ledger failure-tolerance line not found -- script shape changed"
assert text.count(marker) == 1, "ledger failure-tolerance line not unique -- script shape changed"
text = text.replace(marker, "  exit 1  # MUTATED: ledger write made fatal", 1)
# The copy runs from a temp directory, and dispatch.sh finds lanes.sh,
# claim.sh, worktree.sh and cli.py relative to its own location. Pin HERE to
# the real one, or the copy refuses "no free lane" before reaching the line
# under test and the mutation check passes for the wrong reason -- which is
# what it did on the first run of this test.
here = 'HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"'
assert text.count(here) == 1, "HERE assignment not found or not unique -- script shape changed"
text = text.replace(here, 'HERE=%r' % os.path.dirname(os.path.abspath(src)), 1)
open(dst, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh whose ledger write is fatal" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh whose ledger write is fatal"
  LSTATE2="$D/state-149"
  seed_conflicting_task "$LSTATE2" t:3 ad149-ledger-fatal 149
  printf '149|| the same write failure, against the fatal copy\n' >> "$D/issues"
  out=$(DISPATCH_SCRIPT="$BROKEN_DISPATCH" LEDGER_STATE="$LSTATE2" \
        run 149 ledger-fatal "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
  if [ "$rc" -ne 0 ]; then
    ok "mutation confirmed: making the ledger write fatal fails a dispatch that WORKED (the assertion above would now be red)"
  else
    bad "mutation confirmed: making the ledger write fatal fails a dispatch that worked" \
      "the fatal copy still exited 0 -- the patch missed the real failure-tolerance path: $out"
  fi
  want_contains "and the brief had already gone out when it did -- which is why fatal is wrong here" \
    "send-keys -t t:@103 Enter" "$(tmuxlog)"
fi

# --- AN ABORTED DISPATCH LEAVES NO RECORD SAYING WORK IS IN FLIGHT --------
#
# The subtle one. A record claiming a lane is BUSY, written by a dispatch that
# then aborted, is worse than no record: the entire point of the ledger is to
# be believed. The guarantee is ordering, not cleanup -- the write happens
# after the last abort path -- so this asserts no TASK is ever recorded,
# which is what "busy" actually means to `lane_available` (agent-dotfiles#174:
# `lanes":[]` no longer holds here on its own -- step 1's lane selection runs
# BEFORE the claim and the worktree, and its first-sight backfill legitimately
# registers a never-seen `free-N` lane as free while deciding whether to use
# it, whether or not the rest of the dispatch goes on to succeed. That row
# says free, truthfully, and free is not the claim this test guards against.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '143|| a dispatch that aborts on its worktree\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-143" run 143 ledger-abort "$D/brief-orig.md" acme/agent-dotfiles "$D/not-a-git-repo"); rc=$?
want_exit "a dispatch that aborts on a failed worktree still fails" "$rc" 1 "$out"
status=$(LEDGER_STATE="$D/state-143" ledger status 2>&1)
want_missing "an aborted dispatch records no task" "ad143-ledger-abort" "$status"
want_contains "and no task at all" '"tasks":[]' "$status"

# The same, for the abort that happens before a worktree is even attempted:
# an issue someone else has claimed.
printf '144|someone-else| already claimed, must record nothing\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-144" run 144 ledger-claimed "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a dispatch refused at the claim still fails" "$rc" 1 "$out"
status=$(LEDGER_STATE="$D/state-144" ledger status 2>&1)
want_missing "a refused claim records no task" "ad144-ledger-claimed" "$status"
want_contains "and no task at all" '"tasks":[]' "$status"
# --- the brief must actually START, not merely be typed (#141) -------------
# Two lanes sat for 40 minutes each holding a full brief that was typed in and
# never submitted: `/clear` takes longer to repaint than the dispatcher waits,
# so the Enter that followed was swallowed. dispatch.sh printed
# `dispatch: #N -> lane` and walked away, claim.sh showed the issue claimed,
# and the work was invisible -- not queued, not running, not lost.
#
# DISPATCH_SWALLOW_ENTER models exactly that: the keys arrive, the box keeps
# the text, nothing runs.
echo '160|| a dispatch whose Enter is swallowed' >> "$D/issues"
# Successful dispatches earlier in this file leave their worktrees in place,
# so the assertion is that this one ADDS none -- not that none exist.
before=$(worktrees)
out=$(LEDGER_STATE="$D/state-160" DISPATCH_SWALLOW_ENTER=1 \
      run 160 unsent-brief "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a brief that never submits fails the dispatch" "$rc" 1 "$out"
want_contains "and says it was typed but never submitted" "never submitted" "$out"
want_missing "and does not print a success line" "dispatch: #160 -> " "$out"
# Unwound like every other refusal: the issue goes back to the pool rather
# than looking claimed-and-running while nothing runs.
if [ -z "$(assignees 160)" ]; then ok "the claim is released when the brief never starts"
else bad "the claim is released when the brief never starts" "still assigned: $(assignees 160)"; fi
if [ "$(worktrees)" = "$before" ]; then ok "no worktree is left behind by an unsent brief"
else bad "no worktree is left behind by an unsent brief" "$before before, $(worktrees) after"; fi
# The ordering #140's own comment demands, now that there is an abort BELOW
# the final Enter: the ledger records "work is in flight" and this dispatch
# has none. The #141 confirmation therefore runs BEFORE the ledger write, not
# after it, or every swallowed Enter would leave a record asserting a lane is
# working on an issue it was never given.
status=$(LEDGER_STATE="$D/state-160" ledger status 2>&1)
# The exact id `record-dispatch --task "$WINDOW_NAME"` would write, which is
# distinct from the claim placeholder's `ledger-claim:<lane>:<token>` id even
# though both end in the window name.
want_missing "an unsent brief records no DISPATCH task in the ledger" '"id":"ad160-unsent-brief"' "$status"
# ...but the LANE CLAIM stays, and this assertion is the cost of
# agent-dotfiles#209 round 2 written down rather than discovered later.
#
# It used to read `"tasks":[]`. The Enter that this case swallows is sent
# AFTER step 4.5 marks the claim live, so by the time step 5 concludes the box
# never emptied, this dispatcher has already crossed its own point of no
# return -- and the whole of round 2 is that nothing may free a lane from the
# far side of that line. `input_box_state` saying `text` is strong evidence
# nothing was submitted, but it is still evidence read out of pane content,
# which is the instrument #102, #123 and #126 were all filed against; trading
# a bounded capacity loss for the chance of handing out a working lane is the
# trade this subsystem exists to refuse.
#
# So a swallowed Enter now costs a HELD lane needing one manual command, where
# before it cost nothing. That is a real regression in ergonomics for a
# failure that has been observed live (#141, twice on 2026-08-11), accepted in
# exchange for the guarantee. It is not silent: `lanes.sh` reports the lane
# `unsent`, and the abort prints the exact command below.
want_contains "but the lane claim REMAINS -- past the commit point, nothing may free a lane" \
  '"id":"ledger-claim:t:3:ad160-unsent-brief"' "$status"
want_contains "...and the claim is marked live, not merely reserved" '"status":"delivered"' "$status"
want_contains "...so the ledger reads that lane occupied" "False" "$(lane_available "$D/state-160" t:3)"
want_contains "...and the abort says the lane stays held rather than leaving it to be discovered" \
  "STAYS HELD" "$out"
want_contains "...and names the one command that frees it" "cancel-open-task --lane t:3" "$out"

# The other direction, and the one that keeps the check honest: a dispatch
# that DOES submit must pass silently. A confirmation that fires on every
# dispatch is the same as no confirmation.
echo '161|| a dispatch that submits normally' >> "$D/issues"
out=$(run 161 submits-fine "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a brief that submits still exits 0" "$rc" 0 "$out"
want_contains "and reports the dispatch" "dispatch: #161 -> " "$out"
want_missing "and warns about nothing" "WARNING" "$out"


# --- agent-dotfiles#199: dispatch.sh is silent on stderr under bash 3.2 ---
# WHY: `declare -A WINDOW_NAME_BY_INDEX` used to be rejected by macOS's real
# /bin/bash (3.2, no associative arrays), which prints straight to stderr on
# every dispatch and reads exactly like a broken guard on the command that
# decides where work goes -- see #199. `run()` above launches dispatch.sh
# with whatever `bash` PATH resolves to, which is a modern bash on most dev
# machines and never hits the bug; this case runs the script directly so
# its own `#!/bin/bash` shebang picks the interpreter, matching how the
# supervisor actually invokes it in production, and asserts stderr is empty
# on a dispatch that succeeds. That is the regression guard: it is what
# would catch the next 3.2-incompatible construct landing on this path.
echo '170|| dispatch must be silent on stderr' >> "$D/issues"
: > "$D/tmux.log"
rm -rf "$D/panes"; mkdir -p "$D/panes"
STDERR_FILE="$D/dispatch199.err"
PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
  LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
  TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 DISPATCH_PANE_COLS=60 \
  DISPATCH_MESSAGE_BUDGET=430 DISPATCH_CONFIRM_TRIES=2 \
  AGENT_SUPERVISOR_STATE_DIR="$(mktemp -d "$D/state.XXXXXX")" \
  STUB_PANE_PATH="$REPO" WORKTREE_ROOT="$D/roots" \
  "$DISPATCH" 170 stderr-clean "$D/brief.md" acme/agent-dotfiles "$REPO" \
  1>"$D/dispatch199.out" 2>"$STDERR_FILE"
rc=$?
out=$(cat "$D/dispatch199.out")
err=$(cat "$STDERR_FILE")
want_exit "a dispatch run under its own shebang still succeeds" "$rc" 0 "$out"
# Empty, not just missing "declare:" -- a substring check only catches a
# re-introduced declare -A and waves through any other stray write (proved
# by injecting `echo ... >&2` before dispatch.sh's final exit 0 and watching
# this suite stay green). stderr on a successful dispatch is not a place for
# progress lines: the supervisor treats dispatch.sh's stderr as the signal
# that something is wrong, so a legitimate message belongs on stdout instead.
if [ -z "$err" ]; then ok "stderr is clean on a successful dispatch (agent-dotfiles#199)"
else bad "stderr is clean on a successful dispatch (agent-dotfiles#199)" "expected empty stderr, got: $err"; fi

# --- agent-dotfiles#184: two dispatchers racing the SAME candidate lane ---
#
# Every case above dispatches once, in isolation -- the second dispatcher
# never exists. #184 is specifically about what happens when it does: two
# dispatchers can both read `lanes.sh --free` + `cli.py lane-free` and see
# the SAME lane free before either one finishes acting on it. A test that
# only calls dispatch.sh twice IN SEQUENCE proves nothing (the second call
# losing is what happens either way) -- this drives dispatcher A into the
# stub, splices a WHOLE second dispatch (dispatcher B, for a different
# issue, against the real unmodified dispatch.sh) in via a test-only hook
# right after A reads the lane free and before A can act on that read, and
# only then lets A continue. That is "dispatcher A reads availability, then
# B completes a whole dispatch, then A sends" -- #184's own required shape,
# not two calls back to back.
#
# DISPATCH_TEST_RACE_HOOK is dispatch.sh's only concession to this: a command
# run with the candidate lane as $1, at exactly the point a second dispatcher
# would need to land a competing dispatch to prove the race. No caller sets
# it outside this file.
cat > "$D/race-hook.sh" <<'HOOK'
#!/bin/bash
set -uo pipefail
env -u DISPATCH_TEST_RACE_HOOK bash "$RACE_DISPATCH" "$RACE_B_ISSUE" "$RACE_B_SLUG" \
  "$RACE_B_BRIEF" "$RACE_B_REPO_SLUG" "$RACE_B_REPO_PATH" > "$RACE_B_LOG" 2>&1
echo $? > "$RACE_B_RC"
HOOK
chmod +x "$D/race-hook.sh"

# Runs dispatcher A for issue $1 through dispatch script $2 (the real
# dispatch.sh, or a mutated copy), with dispatcher B (issue $3, ALWAYS the
# real, unmutated dispatch.sh -- the race is about what A does with a
# genuine competing dispatch, not about B's own correctness) spliced in via
# the hook. Leaves $RACE_RC_A/$RACE_OUT_A for A and $RACE_RC_B/$RACE_OUT_B
# for B, plus $RACE_LOG (the shared tmux log both dispatchers wrote to).
run_race() {
  local issue_a="$1" script="$2" issue_b="$3"
  local state="$D/state-race-$issue_a"
  printf '%s|| dispatcher A races for the only free lane\n%s|| dispatcher B wins the same race\n' \
    "$issue_a" "$issue_b" >> "$D/issues"
  : > "$D/race-b.out"; : > "$D/race-b.rc"
  local out
  out=$(LEDGER_STATE="$state" \
        DISPATCH_TEST_RACE_HOOK="$D/race-hook.sh" \
        RACE_DISPATCH="$DISPATCH" RACE_B_ISSUE="$issue_b" RACE_B_SLUG="race-b-$issue_b" \
        RACE_B_BRIEF="$D/brief.md" RACE_B_REPO_SLUG=acme/agent-dotfiles \
        RACE_B_REPO_PATH="$REPO" RACE_B_LOG="$D/race-b.out" RACE_B_RC="$D/race-b.rc" \
        DISPATCH_SCRIPT="$script" \
        run "$issue_a" "race-a-$issue_a" "$D/brief.md" acme/agent-dotfiles "$REPO")
  RACE_RC_A=$?
  RACE_OUT_A="$out"
  RACE_OUT_B=$(cat "$D/race-b.out" 2>/dev/null)
  RACE_RC_B=$(cat "$D/race-b.rc" 2>/dev/null)
  RACE_LOG=$(tmuxlog)
}

# --- the fixed shape: exactly one dispatcher wins, the other is refused loud
run_race 501 "$DISPATCH" 502
want_exit "dispatcher B (spliced in mid-A's selection) completes its own dispatch" "$RACE_RC_B" 0 "$RACE_OUT_B"
want_contains "...and B's brief actually went out" "dispatch: #502 -> " "$RACE_OUT_B"
want_exit "dispatcher A is refused: B already won the only free lane" "$RACE_RC_A" 1 "$RACE_OUT_A"
want_contains "...and the refusal is LOUD, not silent" "no free lane" "$RACE_OUT_A"
cnt=$(grep -c "rename-window -t t:@103" <<<"$RACE_LOG")
if [ "$cnt" = 1 ]; then
  ok "only one brief reaches the shared lane t:3"
else
  bad "only one brief reaches the shared lane t:3" "rename-window -t t:@103 appeared $cnt times: $RACE_LOG"
fi
if [ "$(assignees 501)" = "" ]; then
  ok "A's issue is never claimed -- refused before claim.sh even runs"
else
  bad "A's issue is never claimed" "assignees: $(assignees 501)"
fi
want_contains "B's issue IS claimed" "jonhill90" "$(assignees 502)"

# --- RED BEFORE THE FIX: a copy with no atomic claim at all, the exact shape
# dispatch.sh had on origin/main before agent-dotfiles#184 -- `lane-free`'s
# read picked a candidate and nothing closed the gap before send-keys.
NO_CLAIM_MUTANT="$D/dispatch-no-claim.sh"
patch_rc=0
python3 - "$DISPATCH" "$NO_CLAIM_MUTANT" <<'PY' || patch_rc=$?
import os
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''  CLAIM_LANE="$candidate"
  CLAIM=$("$LEDGER_PYTHON" "$LEDGER_CLI" claim-lane --lane "$candidate" --token "$CLAIM_TOKEN" --owner-pid $$ 2>/dev/null) || { release_lane_claim; continue; }
  if grep -qF '"claimed":true' <<<"$CLAIM"; then
    LANE="$candidate"
    LANE_TARGET="$candidate_target"
    # agent-dotfiles#216: `lane-free` above already resolved this lane's
    # RECORDED harness (from its @hill90_lane_harness pane option, or the
    # ledger row if it was already known) -- carried forward to step 6 so
    # `record-dispatch` gets an explicit --harness instead of re-guessing one
    # from `#{pane_current_command}`, which cannot tell a Node harness like
    # copilot apart from any other. Empty is possible only if `lane-free`'s
    # own JSON shape ever changes underneath this grep; step 6's existing
    # fallback (HARNESS_BY_COMMAND) covers that, unchanged.
    LANE_HARNESS=$(grep -oE '"harness":"[a-z-]*"' <<<"$CHECK" | head -1 | sed -E 's/.*:"([a-z-]*)"/\\1/')
    break
  fi
  # Lost this candidate to another dispatcher: move on, exactly as before.
  # The release is a no-op in that case (the row is the winner's, not ours)
  # and only bites when the claim committed but its result did not come back
  # readable -- which would otherwise leak a claim only the reap could clear.
  release_lane_claim'''
assert marker in text, "claim-lane block not found -- script shape changed"
assert text.count(marker) == 1, "claim-lane block not unique -- script shape changed"
replacement = '''  LANE="$candidate"  # MUTATED: no atomic claim at all -- agent-dotfiles#184 pre-fix shape
  LANE_TARGET="$candidate_target"
  break'''
text = text.replace(marker, replacement, 1)
# agent-dotfiles#209 round 2: also neutralise step 4.5's commit guard. It
# refuses to send when the claim it is asked to mark live does not exist --
# correct, and exactly what a mutant with no claim (or an ignored verify)
# produces. Left in, dispatcher A would be stopped by the COMMIT check
# rather than sail past the missing CLAIM check, and this case would report
# a race closed by the wrong guard. Same reason d2bce42 extended
# test_dispatch_ledger.sh's fallback mutation past the claim call.
commit_guard = 'if ! grep -qF \'"committed":true\' <<<"$COMMIT_OUT"; then'
assert commit_guard in text, "commit guard not found -- script shape changed"
assert text.count(commit_guard) == 1, "commit guard not unique -- script shape changed"
text = text.replace(commit_guard, 'if false; then  # MUTATED: step 4.5 commit guard bypassed', 1)
here = 'HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"'
assert text.count(here) == 1, "HERE assignment not found or not unique -- script shape changed"
text = text.replace(here, 'HERE=%r' % os.path.dirname(os.path.abspath(src)), 1)
open(dst, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh with no lane claim at all" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh with no lane claim at all"
  run_race 503 "$NO_CLAIM_MUTANT" 504
  cnt=$(grep -c "rename-window -t t:@103" <<<"$RACE_LOG")
  if [ "$RACE_RC_A" = 0 ] && [ "$cnt" -ge 2 ]; then
    ok "RED before the fix: with no atomic claim, BOTH dispatchers land a brief in lane t:3 (x$cnt) -- this is the race #184 reports"
  else
    bad "RED before the fix: with no atomic claim, BOTH dispatchers land a brief in lane t:3" \
      "expected A to also succeed (exit 0) and t:3 renamed >=2 times; rcA=$RACE_RC_A count=$cnt outA=$RACE_OUT_A"
  fi
fi

# --- MUTATION KILL: the fix's own verify-read, made non-fatal on mismatch --
# #184 names this explicitly: mutate the verify-read's mismatch to non-fatal
# and confirm the suite goes red, or the test proves nothing. This defeats
# dispatch.sh's OWN check of the claim result -- the bash-side half of
# claim-then-verify -- while leaving the ledger call itself untouched.
VERIFY_DEFEATED_MUTANT="$D/dispatch-verify-defeated.sh"
patch_rc=0
python3 - "$DISPATCH" "$VERIFY_DEFEATED_MUTANT" <<'PY' || patch_rc=$?
import os
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = 'if grep -qF \'"claimed":true\' <<<"$CLAIM"; then'
assert marker in text, "claim verify guard not found -- script shape changed"
assert text.count(marker) == 1, "claim verify guard not unique -- script shape changed"
text = text.replace(marker, 'if true; then  # MUTATED: claim-lane verify-read mismatch made non-fatal', 1)
# agent-dotfiles#209 round 2: also neutralise step 4.5's commit guard. It
# refuses to send when the claim it is asked to mark live does not exist --
# correct, and exactly what a mutant with no claim (or an ignored verify)
# produces. Left in, dispatcher A would be stopped by the COMMIT check
# rather than sail past the missing CLAIM check, and this case would report
# a race closed by the wrong guard. Same reason d2bce42 extended
# test_dispatch_ledger.sh's fallback mutation past the claim call.
commit_guard = 'if ! grep -qF \'"committed":true\' <<<"$COMMIT_OUT"; then'
assert commit_guard in text, "commit guard not found -- script shape changed"
assert text.count(commit_guard) == 1, "commit guard not unique -- script shape changed"
text = text.replace(commit_guard, 'if false; then  # MUTATED: step 4.5 commit guard bypassed', 1)
here = 'HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"'
assert text.count(here) == 1, "HERE assignment not found or not unique -- script shape changed"
text = text.replace(here, 'HERE=%r' % os.path.dirname(os.path.abspath(src)), 1)
open(dst, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh whose claim verify-read is non-fatal" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh whose claim verify-read is non-fatal"
  run_race 505 "$VERIFY_DEFEATED_MUTANT" 506
  cnt=$(grep -c "rename-window -t t:@103" <<<"$RACE_LOG")
  if [ "$RACE_RC_A" = 0 ] && [ "$cnt" -ge 2 ]; then
    ok "mutation confirmed: an ignored claim verify-read reopens the race (the assertions above would now be red)"
  else
    bad "mutation confirmed: an ignored claim verify-read reopens the race" \
      "expected A to also succeed (exit 0) and t:3 renamed >=2 times; rcA=$RACE_RC_A count=$cnt outA=$RACE_OUT_A"
  fi
fi

# --- agent-dotfiles#209: a dispatcher killed between claim and release -----
#
# Everything above proves that every abort path dispatch.sh ENUMERATES
# releases its lane claim. #209 is about the paths it cannot enumerate: a
# `kill`, an OOM, a closed terminal, a host crash. The placeholder
# `claim_lane` writes is a task with status `created`, and `lane_available`
# counts any non-terminal status as occupied -- so a dispatcher that dies
# holding one leaves the lane reading occupied with nothing working it. That
# is agent-dotfiles#102's failure shape (dispatch capacity silently falling to
# zero while lanes sit idle) reached through the mechanism built to prevent
# it, and it was hand-reconciled nine times in two days before this test
# existed.
#
# The kill is delivered by standing in for `python3` (DISPATCH_PYTHON, which
# dispatch.sh already reads) and signalling the DISPATCHER the instant its
# claim has committed -- the exact instant the gap opens. No new seam in
# dispatch.sh, and the victim is named by the `--owner-pid` argument the claim
# itself carries, so the test cannot kill the wrong process.
cat > "$D/kill-after-claim.sh" <<'KILL'
#!/bin/bash
set -uo pipefail
out=$(python3 "$@" 2>&1); rc=$?
printf '%s\n' "$out"
case " $* " in
  *" claim-lane "*) ;;
  *) exit $rc ;;
esac
grep -qF '"claimed":true' <<<"$out" || exit $rc
victim=""; prev=""
for a in "$@"; do
  [ "$prev" = "--owner-pid" ] && victim="$a"
  prev="$a"
done
[ -n "$victim" ] || { echo "kill-after-claim: no --owner-pid in: $*" >&2; exit $rc; }
kill "-${DISPATCH_TEST_KILL_SIGNAL:-KILL}" "$victim" 2>/dev/null
# On a TERM the dispatcher runs its trap only once this foreground child is
# gone; on a KILL it is already dead. The pause keeps either from racing the
# assertions that follow.
sleep 1
exit $rc
KILL
chmod +x "$D/kill-after-claim.sh"

# Runs one dispatch that gets signalled right after its claim commits.
# Leaves $CRASH_RC and $CRASH_OUT.
run_killed_dispatch() {  # run_killed_dispatch <state> <issue> <slug> <signal> <script>
  local state="$1" issue="$2" slug="$3" signal="$4" script="$5"
  printf '%s|| a dispatcher signalled between claim and release\n' "$issue" >> "$D/issues"
  CRASH_OUT=$(LEDGER_STATE="$state" DISPATCH_PYTHON="$D/kill-after-claim.sh" \
              DISPATCH_TEST_KILL_SIGNAL="$signal" DISPATCH_SCRIPT="$script" \
              run "$issue" "$slug" "$D/brief.md" acme/agent-dotfiles "$REPO")
  CRASH_RC=$?
}

# --- SIGKILL: untrappable by any shell, so the reap is the only cover ------
CRASH_STATE="$D/state-crash"
run_killed_dispatch "$CRASH_STATE" 601 crash-after-claim KILL "$DISPATCH"
want_exit "a dispatcher SIGKILLed right after its claim dies un-cleanly" "$CRASH_RC" 137 "$CRASH_OUT"
crash_status=$(LEDGER_STATE="$CRASH_STATE" ledger status)
want_contains "...leaving its claim placeholder behind: nothing released it" \
  '"id":"ledger-claim:t:3:ad601-crash-after-claim"' "$crash_status"
want_contains "...and the placeholder records the owner that died" '[owner=' "$crash_status"
want_contains "...so the ledger reads lane t:3 OCCUPIED with nothing working it (#102's shape)" \
  "False" "$(lane_available "$CRASH_STATE" t:3)"

echo '602|| the lane must come back after a killed dispatcher' >> "$D/issues"
out=$(LEDGER_STATE="$CRASH_STATE" run 602 after-crash "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "the NEXT dispatch reclaims that lane: the stranded claim is reaped" "$rc" 0 "$out"
want_contains "...and says what it cleared instead of doing it silently" "cleared stranded lane claim" "$out"
want_contains "...and the brief actually goes out" "dispatch: #602 -> " "$out"

# --- SIGTERM: trappable, and the trap must not wait for a later reap -------
TERM_STATE="$D/state-term"
run_killed_dispatch "$TERM_STATE" 603 term-after-claim TERM "$DISPATCH"
want_exit "a dispatcher SIGTERMed right after its claim exits through its trap" "$CRASH_RC" 143 "$CRASH_OUT"
want_contains "...and the TRAP released the claim immediately -- lane free with no reap yet" \
  "True" "$(lane_available "$TERM_STATE" t:3)"
term_status=$(LEDGER_STATE="$TERM_STATE" ledger status)
want_missing "...and left no placeholder behind at all" "ledger-claim:t:3" "$term_status"

# --- MUTATION: remove the trap, and the SIGTERM case must go red ----------
# The reap cannot stand in for this: a TERMed dispatcher's pid is gone, so a
# LATER dispatch would reap its claim either way. What the trap buys is the
# lane coming back AT ONCE rather than at the mercy of the next dispatch, so
# the assertion this mutation has to break is the one taken immediately after
# the signal, with no dispatch in between.
NO_TRAP_MUTANT="$D/dispatch-no-trap.sh"
patch_rc=0
python3 - "$DISPATCH" "$NO_TRAP_MUTANT" <<'PY' || patch_rc=$?
import os
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''trap release_lane_claim EXIT
trap 'release_lane_claim; exit 143' TERM   # 128 + 15
trap 'release_lane_claim; exit 130' INT    # 128 + 2'''
assert marker in text, "claim-release traps not found -- script shape changed"
assert text.count(marker) == 1, "claim-release traps not unique -- script shape changed"
text = text.replace(marker, ': # MUTATED: no trap -- only the four enumerated abort paths release', 1)
here = 'HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"'
assert text.count(here) == 1, "HERE assignment not found or not unique -- script shape changed"
text = text.replace(here, 'HERE=%r' % os.path.dirname(os.path.abspath(src)), 1)
open(dst, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh with no claim-release trap" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh with no claim-release trap"
  NO_TRAP_STATE="$D/state-no-trap"
  run_killed_dispatch "$NO_TRAP_STATE" 604 term-no-trap TERM "$NO_TRAP_MUTANT"
  if [ "$(lane_available "$NO_TRAP_STATE" t:3)" = "False" ]; then
    ok "mutation confirmed: with no trap, a SIGTERMed dispatcher strands its claim (the assertion above would now be red)"
  else
    bad "mutation confirmed: with no trap, a SIGTERMed dispatcher strands its claim" \
      "expected lane_available False, got '$(lane_available "$NO_TRAP_STATE" t:3)'; rc=$CRASH_RC out=$CRASH_OUT"
  fi
fi

# --- MUTATION: remove the reap, and the SIGKILL case must go red ----------
NO_REAP_MUTANT="$D/dispatch-no-reap.sh"
patch_rc=0
python3 - "$DISPATCH" "$NO_REAP_MUTANT" <<'PY' || patch_rc=$?
import os
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''if REAP_OUT=$("$LEDGER_PYTHON" "$LEDGER_CLI" reap-lane-claims 2>&1); then'''
assert marker in text, "reap block not found -- script shape changed"
assert text.count(marker) == 1, "reap block not unique -- script shape changed"
text = text.replace(marker, 'if REAP_OUT="" && false; then  # MUTATED: no reap of stranded claims', 1)
here = 'HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"'
assert text.count(here) == 1, "HERE assignment not found or not unique -- script shape changed"
text = text.replace(here, 'HERE=%r' % os.path.dirname(os.path.abspath(src)), 1)
open(dst, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh that never reaps a stranded claim" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh that never reaps a stranded claim"
  NO_REAP_STATE="$D/state-no-reap"
  run_killed_dispatch "$NO_REAP_STATE" 605 crash-no-reap KILL "$NO_REAP_MUTANT"
  echo '606|| the lane the un-reaped mutant can never get back' >> "$D/issues"
  out=$(LEDGER_STATE="$NO_REAP_STATE" DISPATCH_SCRIPT="$NO_REAP_MUTANT" \
        run 606 after-crash-no-reap "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
  if [ "$rc" -ne 0 ] && grep -qF "no free lane" <<<"$out"; then
    ok "mutation confirmed: with no reap, the killed dispatcher's lane never comes back (the assertions above would now be red)"
  else
    bad "mutation confirmed: with no reap, the killed dispatcher's lane never comes back" \
      "expected a refusal naming 'no free lane'; rc=$rc out=$out"
  fi
  want_contains "and the refusal names how to clear a stranded claim by hand" "release-lane-claim --lane <lane> --token <token>" "$out"
  want_contains "and how to read the token out of the ledger" 'ledger-claim:<lane>:<token>' "$out"
fi

# --- agent-dotfiles#209 round 2: the point of no return is the SEND -------
#
# Every case above signals the dispatcher right after `claim-lane`, which is
# the EARLIEST instant the claim exists -- nothing has been typed, no lane has
# been renamed, and freeing the claim there is correct. None of them signals
# after the brief is live, and that absence is what let the first round of
# this fix ship with the cleanup pointed at the wrong instant.
#
# The re-review's measurement: the trap and the reap both treated
# `CLAIM_COMMITTED` (set after step 5's confirmation loop) as the point of no
# return, but the brief goes live ~70 lines earlier, at the `send-keys Enter`
# that submits it -- up to DISPATCH_CONFIRM_TRIES x DISPATCH_SETTLE (10s by
# default) of wall clock in between. A signal landing in that window ran the
# trap and deleted the claim AFTER the lane had been renamed and the brief
# submitted, and `lane_available` then answered True for a lane that was
# actively working. #102/#126's failure shape produced by the cleanup instead
# of prevented by it, which dispatch.sh's own step 6 comment says in its own
# words must not happen.
#
# So the commit point is now the ledger fact `commit-lane-claim` writes
# IMMEDIATELY BEFORE that Enter, and the two assertions below are about the
# same instant from the two sides that can reach it: a trappable signal
# (SIGTERM, the trap runs) and an untrappable one (SIGKILL, only the reap
# runs). Both must leave the lane HELD.
#
# The signal is delivered by the tmux stub at the submit -- see
# DISPATCH_TEST_SEND_SIGNAL there. No live pane anywhere in this.

# Runs one dispatch that gets signalled at the instant its brief is submitted.
# Leaves $LIVE_RC and $LIVE_OUT.
run_signalled_at_send() {  # run_signalled_at_send <state> <issue> <slug> <signal> <script>
  local state="$1" issue="$2" slug="$3" signal="$4" script="$5"
  printf '%s|| a dispatcher signalled as its brief goes live\n' "$issue" >> "$D/issues"
  LIVE_OUT=$(LEDGER_STATE="$state" DISPATCH_TEST_SEND_SIGNAL="$signal" \
             DISPATCH_SCRIPT="$script" \
             run "$issue" "$slug" "$D/brief.md" acme/agent-dotfiles "$REPO")
  LIVE_RC=$?
}

# --- SIGTERM at the submit: the trap must NOT free a working lane ---------
LIVE_TERM_STATE="$D/state-live-term"
run_signalled_at_send "$LIVE_TERM_STATE" 701 live-then-term TERM "$DISPATCH"
want_exit "a dispatcher SIGTERMed as its brief goes live dies through its trap" "$LIVE_RC" 143 "$LIVE_OUT"
# The probe is only worth anything if the brief REALLY went out first. Assert
# that before asserting anything about the ledger: a signal that landed early
# would leave the lane held for the wrong reason and pass the check below
# while proving nothing.
live_term_log=$(cat "$D/tmux.log")
want_contains "...and the signal was delivered at the submit, not earlier" "signalled TERM to " "$live_term_log"
want_contains "...the lane really was renamed to the task first (a real dispatch)" \
  "rename-window -t t:@103 ad701-live-then-term" "$live_term_log"
want_contains "...and the brief really was submitted into the pane" "send-keys -t t:@103 Enter" "$live_term_log"
# THE ASSERTION. Before the commit point moved, this read True.
want_contains "...so the lane stays HELD: a brief is live in it and no cleanup may free it" \
  "False" "$(lane_available "$LIVE_TERM_STATE" t:3)"
live_term_status=$(LEDGER_STATE="$LIVE_TERM_STATE" ledger status)
want_contains "...and the claim placeholder is still there holding it" \
  '"id":"ledger-claim:t:3:ad701-live-then-term"' "$live_term_status"

# --- SIGKILL at the submit: the reap must NOT free a working lane ---------
# The dangerous half, and the one moving an in-process flag cannot fix: a
# SIGKILL leaves the placeholder behind, and at that moment the placeholder is
# the ONLY record that the lane is occupied, because step 6's `record_dispatch`
# never ran. A reap that judges only "is the owner pid gone" cannot tell that
# apart from a claim taken with nothing sent yet -- so the fact the send
# happened has to be written to the LEDGER before the send, which is what
# `commit-lane-claim` does and what the reap now refuses to touch.
LIVE_KILL_STATE="$D/state-live-kill"
run_signalled_at_send "$LIVE_KILL_STATE" 702 live-then-kill KILL "$DISPATCH"
want_exit "a dispatcher SIGKILLed as its brief goes live dies un-cleanly" "$LIVE_RC" 137 "$LIVE_OUT"
live_kill_log=$(cat "$D/tmux.log")
want_contains "...with the lane renamed and the brief submitted first" \
  "rename-window -t t:@103 ad702-live-then-kill" "$live_kill_log"
want_contains "...and the brief really was submitted into the pane" "send-keys -t t:@103 Enter" "$live_kill_log"
want_contains "...so the lane reads HELD immediately after the kill" \
  "False" "$(lane_available "$LIVE_KILL_STATE" t:3)"

# ...and it must STILL read held after a reap runs, which is what the next
# dispatch does first. This is the assertion the reap's liveness rule alone
# cannot satisfy: the owner pid IS provably gone.
echo '703|| the next dispatch must not take a lane that is working' >> "$D/issues"
out=$(LEDGER_STATE="$LIVE_KILL_STATE" run 703 the-next-one "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "the NEXT dispatch refuses rather than take the lane the killed dispatcher left working" "$rc" 1 "$out"
want_contains "...naming the only lane it had as unavailable" "no free lane" "$out"
want_missing "...and it did NOT reap the live claim away" "ledger-claim:t:3:ad702-live-then-kill" "$out"
want_missing "...and typed nothing into that pane" "rename-window -t t:@103 ad703-the-next-one" "$(cat "$D/tmux.log")"
# Fail-closed has a price and the refusal must name it: this lane needs a
# human, and `release-lane-claim` deliberately will not clear it.
want_contains "...and says how to clear a claim with a live brief behind it" "cancel-open-task --lane" "$out"

# --- the previous round's finding must not regress ------------------------
# Moving the commit point later would be an easy way to satisfy everything
# above and reopen #209 round 1: a dispatcher killed BEFORE the brief is live
# must still release its claim at once. That is what the `603` case near the
# top of this block asserts (SIGTERM right after `claim-lane` -> lane_available
# True with no reap yet), and this repeats it against the SEND-time harness so
# both instants are covered by the same file: same signal, same dispatcher,
# but the brief never gets typed because the send fails outright.
NO_SEND_STATE="$D/state-no-send"
echo '704|| a dispatcher killed before the brief goes live' >> "$D/issues"
NO_SEND_OUT=$(LEDGER_STATE="$NO_SEND_STATE" DISPATCH_PYTHON="$D/kill-after-claim.sh" \
              DISPATCH_TEST_KILL_SIGNAL=TERM run 704 killed-before-send "$D/brief.md" \
              acme/agent-dotfiles "$REPO"); NO_SEND_RC=$?
want_exit "a dispatcher killed BEFORE the brief goes live still exits through its trap" "$NO_SEND_RC" 143 "$NO_SEND_OUT"
want_missing "...having submitted nothing into the pane" "send-keys -t t:@103 Enter" "$(cat "$D/tmux.log")"
want_contains "...and its claim IS released at once -- nothing is working that lane" \
  "True" "$(lane_available "$NO_SEND_STATE" t:3)"

# --- MUTATION: move the commit point back to where it was -----------------
# The guard has to survive its own mutation. This patches a copy whose
# `commit-lane-claim` call is removed and whose CLAIM_COMMITTED is set where it
# used to be -- after step 5's confirmation loop, ~70 lines past the submit --
# which is exactly the shape the re-review reproduced. Both assertions above
# must go red on it, and by the two DIFFERENT mechanisms they test: the trap
# (TERM) and the reap (KILL).
LATE_COMMIT_MUTANT="$D/dispatch-late-commit.sh"
patch_rc=0
python3 - "$DISPATCH" "$LATE_COMMIT_MUTANT" <<'PY' || patch_rc=$?
import os
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = 'COMMIT_OUT=$("$LEDGER_PYTHON" "$LEDGER_CLI" commit-lane-claim'
assert marker in text, "commit-lane-claim call not found -- script shape changed"
assert text.count(marker) == 1, "commit-lane-claim call not unique -- script shape changed"
start = text.rindex("\n", 0, text.index(marker))
end = text.index("CLAIM_COMMITTED=1", start) + len("CLAIM_COMMITTED=1")
text = text[:start] + "\n: # MUTATED: no ledger commit, and CLAIM_COMMITTED set late instead" + text[end:]
late = "# --- 6. record what was dispatched."
assert late in text, "step 6's header not found -- script shape changed"
assert text.count(late) == 1, "step 6's header not unique -- script shape changed"
text = text.replace(late, "CLAIM_COMMITTED=1  # MUTATED: back where round 1 had it\n\n" + late, 1)
here = 'HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"'
assert text.count(here) == 1, "HERE assignment not found or not unique -- script shape changed"
text = text.replace(here, 'HERE=%r' % os.path.dirname(os.path.abspath(src)), 1)
open(dst, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh whose commit point is back after the send" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh whose commit point is back after the send"
  LATE_TERM_STATE="$D/state-late-term"
  run_signalled_at_send "$LATE_TERM_STATE" 705 late-commit-term TERM "$LATE_COMMIT_MUTANT"
  if [ "$(lane_available "$LATE_TERM_STATE" t:3)" = "True" ] \
     && grep -qF "send-keys -t t:@103 Enter" "$D/tmux.log"; then
    ok "mutation confirmed: with the commit point late, the TRAP frees a lane whose brief is live (the assertion above would now be red)"
  else
    bad "mutation confirmed: with the commit point late, the TRAP frees a lane whose brief is live" \
      "expected lane_available True after a submitted brief, got '$(lane_available "$LATE_TERM_STATE" t:3)'; rc=$LIVE_RC out=$LIVE_OUT"
  fi

  LATE_KILL_STATE="$D/state-late-kill"
  run_signalled_at_send "$LATE_KILL_STATE" 706 late-commit-kill KILL "$LATE_COMMIT_MUTANT"
  echo '707|| the lane the late-commit mutant hands out from under a worker' >> "$D/issues"
  out=$(LEDGER_STATE="$LATE_KILL_STATE" DISPATCH_SCRIPT="$LATE_COMMIT_MUTANT" \
        run 707 late-commit-next "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
  if [ "$rc" -eq 0 ] && grep -qF "rename-window -t t:@103 ad707-late-commit-next" "$D/tmux.log"; then
    ok "mutation confirmed: with the commit point late, the REAP hands a working lane to the next dispatcher (the assertions above would now be red)"
  else
    bad "mutation confirmed: with the commit point late, the REAP hands a working lane to the next dispatcher" \
      "expected the next dispatch to succeed into t:3; rc=$rc out=$out"
  fi
fi

# --- agent-dotfiles#241: EVERY tmux target is a window ID, never an index --
#
# tmux window indices are not stable on this server: `renumber-windows on`
# means closing any window shifts every higher index down by one (measured in
# #241, and reproduced end to end against real tmux in
# tests/supervisor/test_lane_done.sh's `#241` section). dispatch.sh resolves a
# lane and then spends a claim, a worktree creation and a rename before its
# first `send-keys` -- so an index resolved at the top of that sequence can
# name a different pane by the bottom of it. Observed 2026-08-12: three
# briefs reported as lanes 8/9/10 were found in other windows.
#
# The individual assertions above already pin each call site's target
# (`send-keys -t t:@103`, `rename-window -t t:@103`). This one is the
# WHOLE-LOG property, which is what actually has to hold: no tmux call
# dispatch.sh makes may address a window by index, because one that slipped
# through would be invisible in a green suite until an estate under load
# renumbered underneath it.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
2|ad82-other|claude.exe|esc to interrupt 3s|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '241|| stable window ids\n' > "$D/issues"
out=$(LEDGER_STATE="$D/state-241" run 241 stable-window-ids "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a dispatch under #241's shape still succeeds" "$rc" 0 "$out"
log=$(tmuxlog)
# Every logged `-t` argument, one per line. The stub logs rename-window and
# send-keys verbatim, which is every tmux call that WRITES to a pane.
targets=$(grep -oE -- '-t [^ ]+' <<<"$log" | sort -u)
if [ -n "$targets" ] && ! grep -qvE -- '^-t t:@[0-9]+$' <<<"$targets"; then
  ok "every tmux target dispatch.sh writes through is a window id: $(tr '\n' ' ' <<<"$targets")"
else
  bad "every tmux target dispatch.sh writes through is a window id" \
    "an index-shaped or empty target is present: $targets"
fi
# The dispatch is still recorded under the LANE (session:index), not under the
# target. The two identities are deliberately different things: the ledger
# keys on a slot that survives a window being closed and recreated, which a
# window id does not. If this ever flips, every operator recovery command the
# refusal path prints (`cancel-open-task --lane t:3`) starts naming something
# no human can read off the window list.
want_contains "the dispatch is still recorded under the lane index, not the window id" \
  '"lane":"t:3"' "$(LEDGER_STATE="$D/state-241" ledger status 2>&1)"

# --- ...and a target that is empty or missing is REFUSED, not guessed -----
# `send-keys -t t:` with an empty index does not error: it hits the ACTIVE
# window, which is usually the supervisor (loop-tick.md, "An empty tmux
# target hits the ACTIVE window"). `t:@` is empty in exactly the same way,
# and #241 must not reintroduce that incident through a new spelling. So the
# guard is a POSITIVE check on the target's shape, and a `lanes.sh` that
# stops emitting one makes dispatch refuse outright rather than fall back to
# the index.
#
# Shadow supervisor directory: every real file symlinked, `lanes.sh` replaced
# by one whose `--free` prints the lane and NO target. dispatch.sh resolves
# its siblings from its own directory, so a mutated lanes.sh anywhere else
# would never be the one it calls.
SHADOW="$D/shadow-supervisor"
rm -rf "$SHADOW"; mkdir -p "$SHADOW"
for f in "$HERE/../../scripts/supervisor/"*; do ln -s "$f" "$SHADOW/$(basename "$f")"; done
rm -f "$SHADOW/lanes.sh"
cat > "$SHADOW/lanes.sh" <<'LANESTUB'
#!/bin/bash
# A lanes.sh that has lost its window-id column -- the shape #241's guard has
# to refuse rather than paper over.
case "${1:-}" in
  --free)    printf 't:3\n' ;;
  --blocked) : ;;
  --json)    printf '[]\n' ;;
  *)         printf 'WINDOW NAME COMMAND STATE\n3 free-3 claude.exe free\n' ;;
esac
LANESTUB
chmod +x "$SHADOW/lanes.sh"
printf '242|| a lanes.sh with no window-id column\n' > "$D/issues"
before=$(worktrees)
out=$(DISPATCH_SCRIPT="$SHADOW/dispatch.sh" run 242 no-target "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a candidate with no window-id target is refused, not dispatched to" "$rc" 1 "$out"
want_contains "...and the refusal says why" "no usable window-id target" "$out"
log=$(tmuxlog)
want_missing "...nothing is sent anywhere -- not to the lane, not to the active window" "send-keys" "$log"
want_missing "...and no window is renamed" "rename-window" "$log"
if [ "$(assignees 242)" = "" ]; then ok "...and no claim is taken"; else bad "...and no claim is taken" "assignees: $(assignees 242)"; fi
if [ "$(worktrees)" = "$before" ]; then ok "...and no worktree is created"; else bad "...and no worktree is created" "$before -> $(worktrees)"; fi

# --- MUTATION: put the index back at ONE call site ------------------------
# The whole-log assertion above is only worth anything if a single reverted
# target turns it red. This repository's most-repeated defect is a test that
# passes without running its subject (#192 was the eighth instance), and a
# log-shape assertion is exactly the kind that can look thorough while
# checking nothing.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
2|ad82-other|claude.exe|esc to interrupt 3s|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
# A SECOND shadow directory, with the REAL lanes.sh: the mutant must be
# stopped by nothing except its own reverted target, and $SHADOW's lanes.sh
# is deliberately broken.
SHADOW2="$D/shadow-supervisor-2"
rm -rf "$SHADOW2"; mkdir -p "$SHADOW2"
for f in "$HERE/../../scripts/supervisor/"*; do ln -s "$f" "$SHADOW2/$(basename "$f")"; done
rm -f "$SHADOW2/dispatch.sh"
INDEX_MUTANT="$SHADOW2/dispatch.sh"
patch_rc=0
python3 - "$DISPATCH" "$INDEX_MUTANT" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
# The brief submit -- the single most consequential target in the script.
marker = 'tmux send-keys -t "$LANE_TARGET" "$MESSAGE" 2>/dev/null'
assert marker in text, "the brief's send-keys not found -- script shape changed"
assert text.count(marker) == 1, "the brief's send-keys not unique -- script shape changed"
open(dst, "w").write(text.replace(marker, 'tmux send-keys -t "$LANE" "$MESSAGE" 2>/dev/null', 1))
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh with one index-addressed send-keys" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh with one index-addressed send-keys"
  chmod +x "$INDEX_MUTANT"
  printf '243|| one call site reverted to the index\n' > "$D/issues"
  out=$(DISPATCH_SCRIPT="$INDEX_MUTANT" run 243 index-target "$D/brief.md" acme/agent-dotfiles "$REPO")
  mutant_targets=$(grep -oE -- '-t [^ ]+' <<<"$(tmuxlog)" | sort -u)
  if grep -qE -- '^-t t:3$' <<<"$mutant_targets"; then
    ok "mutation confirmed: one reverted call site puts an index target back in the log (the assertion above would now be red): $(tr '\n' ' ' <<<"$mutant_targets")"
  else
    bad "mutation confirmed: one reverted call site puts an index target back in the log" \
      "expected '-t t:3' among the mutant's targets, got: $mutant_targets / $out"
  fi
fi

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
# confirm the SAME PR's review is refused outright when the author is the
# only free lane, not silently sent anyway.
out=$(LEDGER_STATE="$D/state-212" run 206 rev-204-again "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 204); rc=$?
want_exit "a review refused when the only free lane is the author" "$rc" 1 "$out"
want_contains "names the PR" "PR #204" "$out"
want_contains "names the authoring task, not just the lane" "ad193-telegram-to-director" "$out"
if [ -z "$(assignees 206)" ]; then ok "the refused review takes no claim on its own issue"
else bad "the refused review takes no claim on its own issue" "still assigned: $(assignees 206)"; fi

# --- fails closed: authorship that cannot be determined refuses the WHOLE
# dispatch, not just the candidate it could not clear -------------------
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '207|| review of a PR with no lane/ branch\n' >> "$D/issues"
printf '299|Fixes #100|some-hand-pushed-branch\n' >> "$D/prs"
out=$(LEDGER_STATE="$D/state-212-closed" run 207 rev-299 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 299); rc=$?
want_exit "authorship that cannot be read from the branch refuses the dispatch" "$rc" 1 "$out"
want_contains "and says why: not the lane/<issue>-<slug> convention" "not a lane/<issue>-<slug> branch" "$out"
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
marker = 'if [ -n "$AUTHOR_LANE" ] && [ "$candidate" = "$AUTHOR_LANE" ]; then'
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
MUTATED_225A="$D/dispatch-no-flag-guard.sh"
patch_rc=0
python3 - "$DISPATCH" "$MUTATED_225A" <<'PY' || patch_rc=$?
import os
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
guarded = '''      if [ $# -lt 2 ]; then
        echo "dispatch: --reviews-pr requires a PR number" >&2
        sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \\{0,1\\}//' >&2
        exit 1
      fi
      REVIEWS_PR="$2"
      shift 2'''
assert text.count(guarded) == 1, "flag-value guard not found or not unique -- script shape changed"
text = text.replace(guarded, '      REVIEWS_PR="${2:-}"\n      shift 2', 1)
here = 'HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"'
assert text.count(here) == 1, "HERE assignment not found or not unique -- script shape changed"
text = text.replace(here, 'HERE=%r' % os.path.dirname(os.path.abspath(src)), 1)
open(dst, "w").write(text)
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
MUTATED_225B="$D/dispatch-no-array-guard.sh"
patch_rc=0
python3 - "$DISPATCH" "$MUTATED_225B" <<'PY' || patch_rc=$?
import os
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
n = text.count('set -- "${POSITIONAL[@]+"${POSITIONAL[@]}"}"')
assert n == 1, "POSITIONAL 3.2-safe expansion not found or not unique -- script shape changed"
text = text.replace('set -- "${POSITIONAL[@]+"${POSITIONAL[@]}"}"', 'set -- "${POSITIONAL[@]}"', 1)
n = text.count('gh pr view "$REVIEWS_PR" "${GH_REPO_ARGS[@]+"${GH_REPO_ARGS[@]}"}" --json headRefName')
assert n == 1, "GH_REPO_ARGS 3.2-safe expansion not found or not unique -- script shape changed"
text = text.replace(
    'gh pr view "$REVIEWS_PR" "${GH_REPO_ARGS[@]+"${GH_REPO_ARGS[@]}"}" --json headRefName',
    'gh pr view "$REVIEWS_PR" "${GH_REPO_ARGS[@]}" --json headRefName',
    1,
)
here = 'HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"'
assert text.count(here) == 1, "HERE assignment not found or not unique -- script shape changed"
text = text.replace(here, 'HERE=%r' % os.path.dirname(os.path.abspath(src)), 1)
open(dst, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch.sh with both 3.2-safe expansions reverted" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
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

rm -rf "$D"


echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
