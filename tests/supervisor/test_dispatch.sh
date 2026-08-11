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

run() {
  : > "$D/tmux.log"
  rm -rf "$D/panes"; mkdir -p "$D/panes"
  PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
    TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 \
    DISPATCH_DROP_PREFIX="${DISPATCH_DROP_PREFIX:-0}" \
    DISPATCH_LANE="${DISPATCH_LANE:-}" \
    DISPATCH_PANE_ROWS="${DISPATCH_PANE_ROWS:-}" \
    DISPATCH_PANE_COLS="${DISPATCH_PANE_COLS:-60}" \
    DISPATCH_MESSAGE_BUDGET="${DISPATCH_MESSAGE_BUDGET:-430}" \
    AGENT_SUPERVISOR_STATE_DIR="${LEDGER_STATE:-$D/state}" \
    STUB_PANE_PATH="${STUB_PANE_PATH:-$REPO}" \
    WORKTREE_ROOT="$D/roots" bash "${DISPATCH_SCRIPT:-$DISPATCH}" "$@" 2>&1
}
# AGENT_SUPERVISOR_STATE_DIR is not optional in this harness. Without it the
# ledger record dispatch.sh now writes (#140) would land in the REAL supervisor
# state directory under $HOME -- a test suite writing into the live estate's
# ledger. LEDGER_STATE overrides it for the cases that need a broken one.
ledger() { AGENT_SUPERVISOR_STATE_DIR="${LEDGER_STATE:-$D/state}" python3 "$HERE/../../scripts/supervisor/cli.py" "$@"; }
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
want_contains "the brief is sent to the free lane, by index" "send-keys -t t:3" "$log"
want_contains "the lane is told which worktree to work in" "${WT:-NO-WORKTREE}" "$log"
want_contains "the lane is pointed at the brief" "$D/brief.md" "$log"
want_contains "the window is renamed to say what is running" "rename-window" "$log"
want_contains "the window name carries the issue number" "ad81-dispatch-worktree" "$log"
want_contains "the lane is cleared before reuse" "/clear" "$log"
want_contains "the issue is claimed before the brief goes out" "jonhill90" "$(assignees 81)"

want_contains "the brief is submitted, not left sitting in the input" "send-keys -t t:3 Enter" "$log"

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
want_contains "the mangled input is cleared before retyping" "send-keys -t t:3 C-u" "$log"
want_contains "the retyped brief is the one submitted" "send-keys -t t:3 Enter" "$log"

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
want_missing "a mangled brief is never submitted" "send-keys -t t:3 Enter" "$log"
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
want_contains "the brief goes to the lane named free-N" "send-keys -t t:4" "$log"
want_missing "the task-named lane is left untouched" "-t t:3" "$log"

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
want_contains "the brief goes to the lane lanes.sh chose" "send-keys -t t:3" "$log"
want_missing "not to the window DISPATCH_LANE named" "-t t:1" "$log"

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
export STUB_LANE_META_BROKEN=1
out=$(LEDGER_STATE="$D/state-145" run 145 ledger-meta-broken "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
unset STUB_LANE_META_BROKEN
want_exit "a broken pane-identity probe does NOT abort the dispatch" "$rc" 0 "$out"
log=$(tmuxlog)
want_contains "the brief still goes out" "send-keys -t t:3" "$log"
want_contains "and is still submitted" "send-keys -t t:3 Enter" "$log"
want_contains "the guard names the failure as an unreadable pane probe" \
  "could not read pane metadata" "$out"
status=$(LEDGER_STATE="$D/state-145" ledger status 2>&1)
want_contains "no garbage dispatch is recorded from the malformed probe" '"lanes":[]' "$status"
want_contains "and no task either" '"tasks":[]' "$status"

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

# --- A LEDGER FAILURE MUST NOT ABORT A DISPATCH ---------------------------
#
# The property the whole design rests on. `dispatch.sh` aborts and unwinds on
# every other failure, which is right for claims and worktrees -- real
# resources. It is wrong here: nothing reads the ledger yet, so a broken one
# that stopped the estate dispatching would trade the estate for a record with
# no reader. The failure must be LOUD and the dispatch must stand.
#
# The break is a state directory that cannot exist -- a path whose parent is a
# regular file -- so the ledger genuinely errors rather than being skipped.
printf '141|| a dispatch whose ledger write fails\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/brief.md/state" run 141 ledger-broken "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a ledger that errors does NOT abort the dispatch" "$rc" 0 "$out"
log=$(tmuxlog)
want_contains "the brief still goes out when the ledger is broken" "send-keys -t t:3" "$log"
want_contains "and is still submitted" "send-keys -t t:3 Enter" "$log"
want_contains "the ledger failure is loud, not swallowed" "LEDGER RECORD FAILED" "$out"
want_contains "and says which dispatch lost its record" "ad141-ledger-broken" "$out"
want_contains "the claim is NOT unwound over a bookkeeping failure" "jonhill90" "$(assignees 141)"

# ...and that tolerance is load-bearing. Patch a copy that makes the ledger
# write fatal and confirm the case above goes red against it -- a suite that
# still passes with the failure-tolerance removed has not tested the property.
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
  printf '142|| the same failure, against the fatal copy\n' >> "$D/issues"
  out=$(DISPATCH_SCRIPT="$BROKEN_DISPATCH" LEDGER_STATE="$D/brief.md/state" \
        run 142 ledger-fatal "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
  if [ "$rc" -ne 0 ]; then
    ok "mutation confirmed: making the ledger write fatal fails a dispatch that WORKED (the assertion above would now be red)"
  else
    bad "mutation confirmed: making the ledger write fatal fails a dispatch that worked" \
      "the fatal copy still exited 0 -- the patch missed the real failure-tolerance path: $out"
  fi
  want_contains "and the brief had already gone out when it did -- which is why fatal is wrong here" \
    "send-keys -t t:3 Enter" "$(tmuxlog)"
fi

# --- AN ABORTED DISPATCH LEAVES NO RECORD SAYING WORK IS IN FLIGHT --------
#
# The subtle one. A record claiming a lane is busy, written by a dispatch that
# then aborted, is worse than no record: the entire point of the ledger is to
# be believed. The guarantee is ordering, not cleanup -- the write happens
# after the last abort path -- so this asserts the ledger is EMPTY, lane row
# included, not merely that the task was tidied up afterwards.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '143|| a dispatch that aborts on its worktree\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-143" run 143 ledger-abort "$D/brief-orig.md" acme/agent-dotfiles "$D/not-a-git-repo"); rc=$?
want_exit "a dispatch that aborts on a failed worktree still fails" "$rc" 1 "$out"
status=$(LEDGER_STATE="$D/state-143" ledger status 2>&1)
want_missing "an aborted dispatch records no task" "ad143-ledger-abort" "$status"
want_contains "and no lane record asserting a lane is occupied" '"lanes":[]' "$status"
want_contains "and no task at all" '"tasks":[]' "$status"

# The same, for the abort that happens before a worktree is even attempted:
# an issue someone else has claimed.
printf '144|someone-else| already claimed, must record nothing\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/state-144" run 144 ledger-claimed "$D/brief-orig.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a dispatch refused at the claim still fails" "$rc" 1 "$out"
status=$(LEDGER_STATE="$D/state-144" ledger status 2>&1)
want_contains "a refused claim records no lane" '"lanes":[]' "$status"
want_contains "a refused claim records no task" '"tasks":[]' "$status"

rm -rf "$D"


echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
