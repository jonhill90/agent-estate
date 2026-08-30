#!/bin/bash
# agent-estate#793: a lane used to be TOLD to derive its own `Review-Lane:`
# id (first with a bare `tmux display-message`, then -- after
# agent-supervisor#685 -- with `lane-whoami.sh`). Both still ask the lane to
# do the deriving, and asking is exactly the step that failed twice
# (`skills#289`, `skills#291`): both dispatched into a non-active window and
# both reported the SUPERVISOR's own coordinates instead.
#
# `dispatch.sh`, `dispatch-claude-print.sh` and `dispatch-pi-rpc.sh` already
# know exactly which lane they are dispatching to -- $LANE is resolved by
# lane selection, before the brief is ever written, with no tmux focus
# anywhere in that resolution. This suite proves the deliverable contract
# they append now STATES that value in the brief file itself, so a lane
# never has to ask anything to get it right.
#
# Mutation, both directions (agent-estate#793's own requirement):
#   1. The stated value TRACKS whichever lane actually got the dispatch, not
#      a fixed or first-window guess -- proven by dispatching two different
#      issues into two different fixtures where the only FREE lane differs
#      (t:3, then t:2) and asserting the brief states the matching id each
#      time, never the other one.
#   2. This check is not vacuous: `tests/supervisor/test_lane_whoami.sh`
#      already carries a REAL-tmux positive control proving the bare
#      `display-message` this replaces really is focus-dependent (its own
#      header explains why that needs real tmux, not a stub); this suite's
#      own "before" state was confirmed by hand -- stashing the dispatch-
#      send.sh/dispatch-claude-print.sh/dispatch-pi-rpc.sh changes and
#      re-running this file fails both assertions below, because the old
#      contract text never named a lane id at all.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DISPATCH="$HERE/../../scripts/supervisor/dispatch.sh"

export QUOTA_GATE="$HERE/stubs/quota-safe"
export SUPERVISOR_MAX_LOAD_PER_CORE=0
export SUPERVISOR_MIN_FREE_MEM_GB=0
export SUPERVISOR_MAX_AGENT_SESSIONS=0
export DISPATCH_LIVE_PANE=1

pass=0; fail=0
ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

echo "dispatch's deliverable contract STATES the lane id (agent-estate#793)"

D=$(mktemp -d); mkdir -p "$D/bin" "$D/roots"
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cp "$HERE/stubs/tmux-dispatch" "$D/bin/tmux"

export SUPERVISOR_STATE="$D/pa-state"
mkdir -p "$SUPERVISOR_STATE/results"

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

: > "$D/prs"

run() {
  local lanes="$1"; shift
  local issue="$1"; shift
  local slug="$1"; shift
  local brief="$1"; shift
  echo "$issue|| a lane that must name its own coordinates correctly" > "$D/issues"
  cp "$lanes" "$D/lanes"
  echo "review this PR and post your verdict" > "$brief"
  : > "$D/tmux.log"
  rm -rf "$D/panes"; mkdir -p "$D/panes"
  PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
    TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 \
    DISPATCH_RESPAWN_SETTLE=0 DISPATCH_LAUNCH_SETTLE=0 \
    DISPATCH_DROP_PREFIX=0 DISPATCH_PANE_COLS=60 \
    DISPATCH_MESSAGE_BUDGET=430 \
    AGENT_SUPERVISOR_STATE_DIR="$D/state-$issue" \
    STUB_PANE_PATH="$REPO" \
    WORKTREE_ROOT="$D/roots" bash "$DISPATCH" "$issue" "$slug" "$brief" acme/agent-dotfiles "$REPO" 2>&1
}

# ============================================================================
# 1a. only window 3 is free -- the stated id must be t:3, never t:1 (the
#     first/active-looking window skills#289/#291 wrongly reported)
# ============================================================================
cat > "$D/lanes-a" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
2|ad82-other|claude.exe|esc to interrupt 3s|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
out=$(run "$D/lanes-a" 793 laneid-a "$D/brief-a.md"); rc=$?
[ "$rc" -eq 0 ] && want_contains "dispatch to the only free lane (t:3) succeeds" "dispatch: #793 -> " "$out" \
  || bad "dispatch succeeds" "rc=$rc: $out"
want_contains "the brief states the ACTUAL dispatched lane's id (t:3)" '`t:3`' "$(cat "$D/brief-a.md")"
want_missing  "...and never states window 1's id instead" '`t:1`' "$(cat "$D/brief-a.md")"

# ============================================================================
# 1b. same shape, but now window 2 is the only free lane -- the stated id
#     must move WITH the dispatch, proving this isn't a fixed/hardcoded value
# ============================================================================
cat > "$D/lanes-b" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
2|free-2|claude.exe|❯ ready|1|0
3|ad82-other|claude.exe|esc to interrupt 3s|1|0
FIX
out=$(run "$D/lanes-b" 794 laneid-b "$D/brief-b.md"); rc=$?
[ "$rc" -eq 0 ] && want_contains "dispatch to the only free lane (t:2) succeeds" "dispatch: #794 -> " "$out" \
  || bad "dispatch succeeds" "rc=$rc: $out"
want_contains "the brief states the ACTUAL dispatched lane's id (t:2) here instead" '`t:2`' "$(cat "$D/brief-b.md")"
want_missing  "...and never states t:3 or t:1 for this dispatch" '`t:3`' "$(cat "$D/brief-b.md")"
want_missing  "" '`t:1`' "$(cat "$D/brief-b.md")"

# ============================================================================
# 2. the stated instruction tells the lane not to derive the value itself
# ============================================================================
want_contains "the brief tells the lane not to derive it via a bare display-message" \
  "Do not run a bare" "$(cat "$D/brief-a.md")"
want_contains "...and names lane-whoami.sh only as a fallback double-check" \
  "lane-whoami.sh" "$(cat "$D/brief-a.md")"

echo "  -> $pass ok, $fail failed"
[ "$fail" -eq 0 ]
