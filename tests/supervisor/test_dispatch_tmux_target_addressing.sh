#!/bin/bash
# agent-dotfiles#241: a tmux target must always be a window ID (`t:@103`),
# never a positional index -- an index shifts when another window closes,
# so addressing by index hits whatever now sits at that slot. Covers the
# id-vs-index split itself, a target that is empty or missing being
# refused rather than guessed, and a mutation check putting the index back
# at one call site.
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

echo "dispatch.sh -- every tmux target is a window ID, never an index (agent-dotfiles#241)"

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
INDEX_MUTANT="$SHADOW2/dispatch.sh"
patch_rc=0
# agent-supervisor#716: the brief's verified_type call now lives in
# dispatch-send.sh, not dispatch.sh's own text -- so it is THAT symlink,
# not dispatch.sh's, that must be replaced with a real mutated file.
# dispatch.sh itself stays an untouched symlink; `HERE` still resolves to
# $SHADOW2 either way (BASH_SOURCE[0] is the invoked symlink path, not its
# target), so it keeps finding every sibling -- mutated or not -- right here.
PYTHONPATH="$HERE" python3 - "$SHADOW2" <<'PY' || patch_rc=$?
import os
import sys
import _dispatch_mutate as M
target_dir = sys.argv[1]
# The brief submit -- the single most consequential target in the script.
# agent-supervisor#178 moved the actual `tmux send-keys` call for the brief
# into send.sh's verified_type (shared by every caller, so mutating it here
# would mutate them all); what stayed in dispatch.sh, and is now the
# equivalent regression to reproduce, is the ARGUMENT it hands that shared
# function -- $LANE_TARGET (a window id) vs $LANE (an index).
marker = 'verified_type "$LANE_TARGET" "$MESSAGE" \\'
hits = M.read_owning_file(target_dir, marker)
assert len(hits) == 1, "the brief's verified_type call not found in exactly one file -- script shape changed: %r" % hits
owner = hits[0]
text = open(owner).read()
os.remove(owner)  # drop the symlink before writing a real, mutated file in its place
open(owner, "w").write(text.replace(marker, 'verified_type "$LANE" "$MESSAGE" \\', 1))
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

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
