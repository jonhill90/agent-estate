#!/bin/bash
# The ledger, not the tmux window name, decides whether `dispatch.sh` may use
# a lane. agent-dotfiles#174: "i think using tmux as a database is stupid...
# Its a tool not a database." This file exercises the five tests #174 itself
# names, plus the mutation check it names: a `dispatch.sh` that falls back
# to the window name on a ledger miss must make test 2 go red.
#
# test_dispatch.sh already covers the surrounding dispatch mechanics (claim,
# worktree, message delivery, the #140 write-tolerance) end to end and is not
# repeated here. This file is narrowly about one question: who decides a
# lane is free.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DISPATCH="$HERE/../../scripts/supervisor/dispatch.sh"
CLI="$HERE/../../scripts/supervisor/cli.py"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

echo "dispatch.sh -- the ledger is authoritative for lane availability (#174)"

D=$(mktemp -d); mkdir -p "$D/bin" "$D/roots"
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cp "$HERE/stubs/tmux-dispatch" "$D/bin/tmux"

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

: > "$D/issues"
: > "$D/prs"
echo "do the thing" > "$D/brief.md"

run() {
  : > "$D/tmux.log"
  rm -rf "$D/panes"; mkdir -p "$D/panes"
  PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
    TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 DISPATCH_CONFIRM_TRIES=2 \
    AGENT_SUPERVISOR_STATE_DIR="${LEDGER_STATE:?run needs LEDGER_STATE}" \
    STUB_PANE_PATH="${STUB_PANE_PATH:-$REPO}" \
    WORKTREE_ROOT="$D/roots" bash "${DISPATCH_SCRIPT:-$DISPATCH}" "$@" 2>&1
}
# Directly what `cli.py lane-free`'s first-sight backfill would write, and
# what `dispatch.sh record-dispatch` (step 6) writes on a real send -- used
# here to put the ledger into a known state WITHOUT going through a whole
# dispatch, so each test below isolates the one thing it is checking.
seed_free_lane() {
  local state="$1" lane="$2"
  PATH="$D/bin:$PATH" LANES_FIXTURE="$D/lanes" LANES_SESSION=t \
    AGENT_SUPERVISOR_STATE_DIR="$state" STUB_PANE_PATH="$REPO" \
    python3 "$CLI" register --lane "$lane" --target "$lane" --harness claude --repo "$REPO" >/dev/null
}
ledger_status() { AGENT_SUPERVISOR_STATE_DIR="$1" python3 "$CLI" status 2>&1; }
tmuxlog()   { cat "$D/tmux.log"; }
assignees() { awk -F'|' -v n="$1" '$1==n{print $2}' "$D/issues"; }

# --- test 1: a lane the ledger says is free is dispatched to --------------
# Seeded, NOT via the free-N backfill -- the window is named after an old,
# already-finished task, something the pre-#174 code would have refused
# outright. Only the ledger says this lane is free.
S1="$D/state-1"
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|ad-old-task|claude.exe|❯ ready|1|0
FIX
seed_free_lane "$S1" t:3
printf '201|| test 1: ledger says free\n' >> "$D/issues"
out=$(LEDGER_STATE="$S1" run 201 ledger-says-free "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a lane the ledger says is free is dispatched to" "$rc" 0 "$out"
want_contains "even though the window is still named after an old task" \
  "send-keys -t t:3" "$(tmuxlog)"

# --- test 2: a lane the ledger says is occupied is refused, even named ----
# free-N -- THE INVERSION #174 exists to prove.
S2="$D/state-2"
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '202|| test 2a: occupy the lane first\n' >> "$D/issues"
out=$(LEDGER_STATE="$S2" run 202 occupy-first "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: a first dispatch occupies the lane" "$rc" 0 "$out"

printf '203|| test 2b: same lane, still says free-3, must be refused\n' >> "$D/issues"
before=$(ls -d "$D"/roots/* 2>/dev/null | wc -l | tr -d ' ')
out=$(LEDGER_STATE="$S2" run 203 occupied-despite-name "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a lane the ledger says is occupied is refused" "$rc" 1 "$out"
want_missing "even though its window still says free-3" "send-keys" "$(tmuxlog)"
if [ "$(assignees 203)" = "" ]; then ok "the occupied-lane refusal takes no claim"; else bad "the occupied-lane refusal takes no claim" "assignees: $(assignees 203)"; fi
after=$(ls -d "$D"/roots/* 2>/dev/null | wc -l | tr -d ' ')
if [ "$before" = "$after" ]; then ok "the occupied-lane refusal creates no worktree"; else bad "the occupied-lane refusal creates no worktree" "$before -> $after"; fi

# --- test 3: a hand-renamed window does not change the decision -----------
# Both directions: renaming an OCCUPIED lane to something innocuous does not
# free it, and renaming a FREE lane to something that is not `free-N` does
# not occupy it.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|somebody-renamed-this|claude.exe|❯ ready|1|0
FIX
printf '204|| test 3a: occupied lane, hand-renamed, still refused\n' >> "$D/issues"
out=$(LEDGER_STATE="$S2" run 204 renamed-still-occupied "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "an occupied lane hand-renamed to anything else is still refused" "$rc" 1 "$out"
want_missing "the rename does not free it" "send-keys" "$(tmuxlog)"

S3="$D/state-3"
seed_free_lane "$S3" t:3
printf '205|| test 3b: free lane, hand-renamed, still dispatched to\n' >> "$D/issues"
out=$(LEDGER_STATE="$S3" run 205 renamed-still-free "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a free lane hand-renamed to anything else is still dispatched to" "$rc" 0 "$out"
want_contains "the rename does not occupy it" "send-keys -t t:3" "$(tmuxlog)"

# --- test 4: an unreadable ledger refuses to dispatch and says why --------
printf '206|| test 4: the ledger cannot be read at all\n' >> "$D/issues"
out=$(LEDGER_STATE="$D/brief.md/not-a-directory" run 206 unreadable-ledger "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "an unreadable ledger refuses the dispatch" "$rc" 1 "$out"
want_contains "and says the ledger is why" "ledger is unreadable" "$out"
want_missing "nothing is sent" "send-keys" "$(tmuxlog)"

# --- test 5: lane-done.sh's completion releases the lane in the ledger,   -
# and dispatch.sh trusts that release. The rename is cosmetic: this asserts
# the LEDGER effect, which lane-done.sh's own suite (test_lane_done.sh)
# does not -- it only asserts the rename and the record-completion call.
S5="$D/state-5"
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '207|| test 5a: occupy the lane\n' >> "$D/issues"
out=$(LEDGER_STATE="$S5" run 207 occupy-for-release "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: occupy the lane for the release test" "$rc" 0 "$out"
status=$(ledger_status "$S5")
want_contains "setup: the lane now carries an open task" '"lane":"t:3"' "$status"

printf '208|| test 5b: same lane, before release, still refused\n' >> "$D/issues"
out=$(LEDGER_STATE="$S5" run 208 before-release "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "before lane-done's release, the lane is still refused" "$rc" 1 "$out"

# The exact call lane-done.sh makes (agent-dotfiles#140's record-completion),
# against the task id the setup dispatch recorded under (its window name).
AGENT_SUPERVISOR_STATE_DIR="$S5" python3 "$CLI" record-completion \
  --task ad207-occupy-for-release --note "test: lane-done released this lane" >/dev/null

printf '209|| test 5c: same lane, after release, dispatched to\n' >> "$D/issues"
out=$(LEDGER_STATE="$S5" run 209 after-release "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "lane-done's release lets the lane be dispatched to again" "$rc" 0 "$out"
want_contains "and the brief actually goes to it" "send-keys -t t:3" "$(tmuxlog)"

# --- mutation-check 2: a name-fallback on a ledger miss must go red -------
# agent-dotfiles#174's own words: "make dispatch.sh fall back to the window
# name on a ledger miss, and confirm it goes red." Patched to treat ANY
# free-N-named idle candidate as free, the way the pre-#174 code did,
# regardless of what `lane-free` answers -- and rerun test 2's central
# assertion against it.
FALLBACK_DISPATCH="$D/dispatch-name-fallback.sh"
patch_rc=0
python3 - "$DISPATCH" "$FALLBACK_DISPATCH" <<'PY' || patch_rc=$?
import os
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''  CHECK=$("$LEDGER_PYTHON" "$LEDGER_CLI" lane-free --lane "$candidate" --target "$candidate" --window-name "$wname" 2>/dev/null) || continue
  grep -qF '"free":true' <<<"$CHECK" || continue

  # Test-only instrumentation (agent-dotfiles#184): when set, run this
  # command with the candidate lane as $1 right after it reads free and
  # before this dispatch claims it -- exactly the gap a second dispatcher
  # would need to land a whole competing dispatch in to prove the race.
  # No caller sets this outside tests/supervisor/test_dispatch.sh.
  if [ -n "${DISPATCH_TEST_RACE_HOOK:-}" ]; then
    "$DISPATCH_TEST_RACE_HOOK" "$candidate" || true
  fi

  # CLAIM_LANE is set BEFORE the claim call, not after it (agent-dotfiles#209).
  # The placeholder row is written INSIDE that call, so assigning afterwards
  # left a real window: a TERM landing while the dispatcher waited on this
  # command substitution ran the trap with CLAIM_LANE still empty and the row
  # already committed -- a stranded claim on the one signal path the trap
  # exists to cover. Naming a lane this dispatch did not win costs nothing:
  # `release_lane_claim` is scoped to (lane, THIS dispatch's token,
  # status='created'), so it matches no row unless the claim really succeeded.
  #
  # `--owner-pid $$` is THIS script's pid, not the `cli.py` child's: the child
  # exits the moment the claim is written, so its pid would read dead
  # instantly and step 0.5's reap would clear a live dispatch's claim. `$$` is
  # the parent shell's pid even inside this command substitution.
  CLAIM_LANE="$candidate"
  CLAIM=$("$LEDGER_PYTHON" "$LEDGER_CLI" claim-lane --lane "$candidate" --token "$CLAIM_TOKEN" --owner-pid $$ 2>/dev/null) || { release_lane_claim; continue; }
  if grep -qF '"claimed":true' <<<"$CLAIM"; then
    LANE="$candidate"
    break
  fi
  # Lost this candidate to another dispatcher: move on, exactly as before.
  # The release is a no-op in that case (the row is the winner's, not ours)
  # and only bites when the claim committed but its result did not come back
  # readable -- which would otherwise leak a claim only the reap could clear.
  release_lane_claim'''
assert marker in text, "lane-free selection block not found -- script shape changed"
assert text.count(marker) == 1, "lane-free selection block not unique -- script shape changed"
mutated = '''  # MUTATED: reproduces the pre-#174 bug -- trust the window name on a
  # ledger miss instead of refusing, bypassing the ledger read AND the
  # agent-dotfiles#184 claim that would otherwise still catch this (the
  # claim's own INSERT would collide with the real occupant, so the
  # mutation must skip it too to actually reproduce the pre-#174 shape).
  if [[ "$wname" =~ ^free-[0-9]+$ ]]; then
    LANE="$candidate"
    break
  fi'''
text = text.replace(marker, mutated, 1)
# agent-dotfiles#209 round 2 adds a THIRD guard on the same path, for the same
# reason the claim had to be skipped above: step 4.5 refuses to send unless it
# can mark THIS dispatch's claim live, and a mutant that never claimed has
# none to mark. Left in, the fallback copy aborts at the commit instead of
# dispatching to an occupied lane, and this case would report the pre-#174 bug
# as closed by a guard that has nothing to do with reading the ledger.
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
  bad "setup: patched a copy of dispatch.sh that falls back to the window name" \
    "could not patch $DISPATCH (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch.sh that falls back to the window name"
  cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
  printf '210|| mutation setup: occupy the lane\n' >> "$D/issues"
  S6="$D/state-6"
  out=$(LEDGER_STATE="$S6" run 210 mutation-occupy "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
  if [ "$rc" -ne 0 ]; then
    bad "setup: the mutation-check's own occupying dispatch must succeed" "$out"
  fi
  printf '211|| mutation check: same lane, still says free-3\n' >> "$D/issues"
  out=$(DISPATCH_SCRIPT="$FALLBACK_DISPATCH" LEDGER_STATE="$S6" \
        run 211 mutation-fallback "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
  log=$(tmuxlog)
  if [ "$rc" = 0 ] && grep -qF "send-keys -t t:3" <<<"$log"; then
    ok "mutation confirmed: a name-fallback dispatches to an occupied lane (the assertion above would now be red)"
  else
    bad "mutation confirmed: a name-fallback dispatches to an occupied lane" \
      "the fallback copy still refused -- the patch missed the real selection logic: rc=$rc, log=$log"
  fi
fi

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
