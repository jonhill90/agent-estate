#!/bin/bash
# agent-supervisor#668: `dispatch.sh --adopt-pane <window-id>` hands an
# already-running, idle pane a fresh brief WITHOUT killing and respawning its
# process -- the mechanism the estate loop was missing when it fell back to
# typing a brief straight into a pane by hand (#664's own shape). This suite
# proves the two things #668's brief asks for directly:
#
#   1. dispatching via --adopt-pane to a genuinely free pane succeeds, no
#      `respawn-pane` is ever called, and the ledger resolves authorship
#      afterward (`cli.py issue-lane` reads known:true with a contributor) --
#      exactly what record-dispatch (step 6) gives an ordinary dispatch and
#      what a hand-typed brief never did.
#   2. dispatching to a pane that is NOT free is refused the same way an
#      ordinary dispatch refusal reads -- no claim taken, no worktree built,
#      nothing sent.
#
# test_dispatch.sh and test_dispatch_ledger.sh already cover the surrounding
# dispatch mechanics end to end; this file is narrowly about the one new
# branch --adopt-pane adds.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DISPATCH="$HERE/../../scripts/supervisor/dispatch.sh"
CLI="$HERE/../../scripts/supervisor/cli.py"
export QUOTA_GATE="$HERE/stubs/quota-safe"
export SUPERVISOR_MAX_LOAD_PER_CORE=0
export SUPERVISOR_MIN_FREE_MEM_GB=0
# agent-supervisor#171/#668: every PLAIN dispatch call in this suite that does
# NOT pass --adopt-pane (--adopt-pane forces LIVE_PANE=1 itself, see
# dispatch.sh) must still force the pre-#171 tmux flow explicitly. This suite
# stubs no `claude` binary, and a real one is routinely on a dev machine's own
# PATH -- see test_dispatch_ledger.sh's identical export and comment for the
# incident this avoids: an un-forced plain dispatch here would silently reach
# dispatch-claude-print.sh's REAL, blocking `claude -p` handshake instead of
# exercising the tmux path this suite actually wants to test.
export DISPATCH_LIVE_PANE=1
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

echo "dispatch.sh --adopt-pane -- adopt an idle pane instead of spawning a new process (#668)"

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
git -C "$REPO" remote set-url origin "git@github.com:acme/agent-dotfiles.git"

: > "$D/issues"
: > "$D/prs"
echo "do the thing" > "$D/brief.md"

# agent-supervisor#500's own carry-forward-history block reads
# $SUPERVISOR_STATE/results, defaulting to the REAL, live supervisor state
# dir when unset -- pinned here to a scratch directory so this suite never
# touches production history (see test_dispatch.sh's own identical setup).
export SUPERVISOR_STATE="$D/pa-state"
mkdir -p "$SUPERVISOR_STATE/results"

run() {
  : > "$D/tmux.log"
  rm -rf "$D/panes"; mkdir -p "$D/panes"
  PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
    TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 DISPATCH_CONFIRM_TRIES=2 \
    DISPATCH_RESPAWN_SETTLE=0 DISPATCH_LAUNCH_SETTLE=0 DISPATCH_SESSION_TIMEOUT=0 \
    AGENT_SUPERVISOR_STATE_DIR="${LEDGER_STATE:?run needs LEDGER_STATE}" \
    STUB_PANE_PATH="${STUB_PANE_PATH:-$REPO}" \
    WORKTREE_ROOT="$D/roots" bash "$DISPATCH" "$@" 2>&1
}
seed_free_lane() {
  local state="$1" lane="$2"
  PATH="$D/bin:$PATH" LANES_FIXTURE="$D/lanes" LANES_SESSION=t \
    AGENT_SUPERVISOR_STATE_DIR="$state" STUB_PANE_PATH="$REPO" \
    python3 "$CLI" register --lane "$lane" --target "$lane" --harness claude --repo "$REPO" >/dev/null
}
ledger_status() { AGENT_SUPERVISOR_STATE_DIR="$1" python3 "$CLI" status 2>&1; }
tmuxlog()   { cat "$D/tmux.log"; }
assignees() { awk -F'|' -v n="$1" '$1==n{print $2}' "$D/issues"; }

# --- test 1: adopt a genuinely free pane -----------------------------------
# Window 3's id is @103 (WID_BASE 100 + index 3, stubs/tmux-dispatch's own
# scheme) -- both the bare-index and the @-prefixed spellings are exercised
# below, since dispatch.sh accepts either (see --adopt-pane's own usage
# comment).
S1="$D/state-1"
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|ad-old-task|claude.exe|❯ ready|1|0
FIX
seed_free_lane "$S1" t:3
printf '301|| test 1: adopt a free pane by bare index\n' >> "$D/issues"
out=$(LEDGER_STATE="$S1" run 301 adopt-free-pane "$D/brief.md" acme/agent-dotfiles "$REPO" --adopt-pane 103); rc=$?
want_exit "adopting a genuinely free pane succeeds" "$rc" 0 "$out"
log=$(tmuxlog)
want_contains "the brief is sent to the adopted pane" "send-keys -t t:@103" "$log"
want_missing "no process is spawned -- respawn-pane is never called" "respawn-pane" "$log"
want_contains "dispatch says which pane it adopted" "adopting" "$out"

status=$(ledger_status "$S1")
want_contains "the ledger records the dispatch against the adopted lane" '"lane":"t:3"' "$status"

ISSUE_LANE=$(AGENT_SUPERVISOR_STATE_DIR="$S1" python3 "$CLI" issue-lane --issue 301 --repo acme/agent-dotfiles 2>&1)
want_contains "issue-lane resolves authorship for the adopted dispatch (the whole point of #668)" '"known":true' "$ISSUE_LANE"
want_contains "issue-lane names the adopted lane as the contributor" '"lane":"t:3"' "$ISSUE_LANE"

# --- test 2: the @-prefixed spelling is accepted identically ---------------
S1B="$D/state-1b"
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|ad-old-task|claude.exe|❯ ready|1|0
FIX
seed_free_lane "$S1B" t:3
printf '302|| test 1b: adopt a free pane by @-prefixed id\n' >> "$D/issues"
out=$(LEDGER_STATE="$S1B" run 302 adopt-free-pane-at "$D/brief.md" acme/agent-dotfiles "$REPO" --adopt-pane @103); rc=$?
want_exit "adopting by the @-prefixed window id succeeds the same way" "$rc" 0 "$out"
want_missing "still no process spawned" "respawn-pane" "$(tmuxlog)"

# --- test 3: the target pane is NOT free -- refused, same shape as ordinary
# dispatch's own refusal, no claim taken, no worktree built, nothing sent ---
S2="$D/state-2"
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|ad213-running|claude.exe|esc to interrupt 3s|1|0
FIX
printf '303|| test 3: the requested pane is busy\n' >> "$D/issues"
before=$(ls -d "$D"/roots/* 2>/dev/null | wc -l | tr -d ' ')
out=$(LEDGER_STATE="$S2" run 303 adopt-busy-pane "$D/brief.md" acme/agent-dotfiles "$REPO" --adopt-pane 103); rc=$?
want_exit "adopting a busy pane is refused" "$rc" 1 "$out"
want_contains "the refusal names --adopt-pane specifically" "--adopt-pane" "$out"
want_missing "nothing is sent to the busy pane" "send-keys" "$(tmuxlog)"
if [ "$(assignees 303)" = "" ]; then ok "the refused adopt takes no issue claim"; else bad "the refused adopt takes no issue claim" "assignees: $(assignees 303)"; fi
after=$(ls -d "$D"/roots/* 2>/dev/null | wc -l | tr -d ' ')
if [ "$before" = "$after" ]; then ok "the refused adopt creates no worktree"; else bad "the refused adopt creates no worktree" "$before -> $after"; fi

# --- test 4: a pane the ledger already holds is refused too (occupied, -----
# not merely pane-busy) -- the ledger's lane-free check, not lanes.sh's pane
# content alone, is still what decides (#174), unchanged by this mode.
S3="$D/state-3"
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '304|| test 4a: occupy the lane first\n' >> "$D/issues"
out=$(LEDGER_STATE="$S3" run 304 occupy-first "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "setup: an ordinary dispatch occupies the lane" "$rc" 0 "$out"

printf '305|| test 4b: adopt the same, now-occupied pane -- refused\n' >> "$D/issues"
out=$(LEDGER_STATE="$S3" run 305 adopt-occupied-pane "$D/brief.md" acme/agent-dotfiles "$REPO" --adopt-pane 103); rc=$?
want_exit "adopting a ledger-occupied pane is refused even though it reads free-3" "$rc" 1 "$out"
want_missing "nothing is sent to the occupied pane" "send-keys -t t:@103" "$(tmuxlog)"

# --- test 5: an unresolvable window id is refused, not silently ignored ---
S4="$D/state-4"
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|ad-old-task|claude.exe|❯ ready|1|0
FIX
printf '306|| test 5: adopt an id nothing free-classifies\n' >> "$D/issues"
out=$(LEDGER_STATE="$S4" run 306 adopt-unknown-pane "$D/brief.md" acme/agent-dotfiles "$REPO" --adopt-pane 999); rc=$?
want_exit "adopting a window id no free candidate matches is refused" "$rc" 1 "$out"
want_missing "nothing is sent" "send-keys" "$(tmuxlog)"

# --- test 6: --adopt-pane rejects an unparseable window id up front -------
printf '307|| test 6: a malformed window id\n' >> "$D/issues"
out=$(LEDGER_STATE="$S4" run 307 adopt-bad-id "$D/brief.md" acme/agent-dotfiles "$REPO" --adopt-pane not-a-window); rc=$?
want_exit "a malformed --adopt-pane value is refused before anything is claimed" "$rc" 2 "$out"
if [ "$(assignees 307)" = "" ]; then ok "the malformed --adopt-pane value takes no claim"; else bad "the malformed --adopt-pane value takes no claim" "assignees: $(assignees 307)"; fi

# --- test 7: --adopt-pane combined with --reviews-pr is refused up front --
printf '308|| review PR #500, --adopt-pane not yet supported here\n' >> "$D/issues"
out=$(LEDGER_STATE="$S4" run 308 adopt-review "$D/brief.md" acme/agent-dotfiles "$REPO" --adopt-pane 103 --reviews-pr 500); rc=$?
want_exit "--adopt-pane with --reviews-pr is refused rather than silently narrowed" "$rc" 2 "$out"

# --- mutation check: without --adopt-pane, dispatch.sh's ordinary tmux flow
# behaves exactly as test_dispatch_ledger.sh already proves -- respawn-pane
# IS called and nothing about the plain path changed. Guards against a fix
# that accidentally makes the new skip unconditional.
S5="$D/state-5"
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
seed_free_lane "$S5" t:3
printf '309|| mutation check: ordinary dispatch still respawns\n' >> "$D/issues"
out=$(LEDGER_STATE="$S5" run 309 ordinary-still-respawns "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "an ordinary (non-adopt) dispatch still succeeds" "$rc" 0 "$out"
want_contains "and it still respawns the candidate's process (unlike --adopt-pane)" "respawn-pane" "$(tmuxlog)"

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
