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
    WORKTREE_ROOT="$D/roots" bash "$DISPATCH" "$@" 2>&1
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

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
