#!/bin/bash
# lane-retire.sh must free a lane WITHOUT ever destroying the window it
# occupies, and must refuse outright when doing so would discard work --
# agent-supervisor#564.
#
# WHY: retiring three dispatched lanes by hand (`tmux kill-window`) destroyed
# the underlying windows, two of which predated the dispatch that claimed and
# renamed them. All three lanes happened to be idle with every branch already
# pushed, so nothing was actually lost -- but had any of them been mid-edit,
# the retirement would have taken a live pane's uncommitted work with it. The
# load-bearing properties this suite proves, against a REAL tmux server and a
# REAL git worktree (a stub cannot lie about `git status` or `rename-window`
# the way it can about a canned reply):
#
#   1. a clean, fully-pushed lane is freed AND its window survives, renamed
#      back to free-N -- never killed.
#   2. a lane with uncommitted changes is refused, and the window is left
#      completely untouched (not renamed, not killed).
#   3. a lane with commits that exist nowhere but its own worktree is refused
#      the same way, even with a perfectly clean tree.
#   4. the supervisor's own window is refused on IDENTITY, independent of
#      what state its tree happens to be in.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RETIRE="$HERE/../../scripts/supervisor/lane-retire.sh"
source "$HERE/../../scripts/supervisor/tmux-isolation.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }

echo "lane-retire.sh"

D=$(mktemp -d)

# --- a real, isolated tmux server (invariant 4) -----------------------------
RT="$D/tmux-rt"
mkdir -p "$RT"
rtmux() { env -u TMUX TMUX_TMPDIR="$RT" tmux -f /dev/null "$@"; }
SESS="lrtest-$$"
# Window 0 stands in for the supervisor's own window -- LANES_SUPERVISOR_WINDOW
# defaults to index 1, so lane windows below start at 2, well clear of it.
if ! rtmux new-session -d -s "$SESS" -n supervisor-placeholder -c "$D" 2>/dev/null; then
  echo "FATAL: could not start a throwaway tmux server under \$RT" >&2
  exit 2
fi
cleanup_rt() { unset TMUX; export TMUX_TMPDIR="$RT"; assert_isolated_tmux && tmux -f /dev/null kill-server 2>/dev/null; }
trap cleanup_rt EXIT

run() {
  local window="$1"; shift
  AGENT_SUPERVISOR_STATE_DIR="$D/state" TMUX_TMPDIR="$RT" \
    env -u TMUX bash "$RETIRE" "${SESS}:${window}" "$SESS" 2>&1
}
ledger() { AGENT_SUPERVISOR_STATE_DIR="$D/state" python3 "$HERE/../../scripts/supervisor/cli.py" "$@"; }
wname() { rtmux display-message -p -t "${SESS}:$1" '#{window_name}' 2>/dev/null; }
# `#{pane_current_path}` can read empty for a couple of ticks right after
# `new-window` returns -- the pane's shell process exists but tmux has not
# yet sampled its cwd. Poll rather than trust the first read, so this suite
# does not go flaky on exactly the signal lane-retire.sh's own safety checks
# depend on.
wait_pane_cwd() {
  local win="$1" tries=0
  while [ "$tries" -lt 20 ]; do
    [ -n "$(rtmux display-message -p -t "${SESS}:${win}" '#{pane_current_path}' 2>/dev/null)" ] && return 0
    sleep 0.1
    tries=$((tries+1))
  done
  return 1
}

# A minimal origin + clone, standing in for the shared checkout.
git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/repo"
git -C "$D/repo" config user.email test@example.com
git -C "$D/repo" config user.name "Test"
git -C "$D/repo" checkout -q -b main
echo one > "$D/repo/file.txt"
git -C "$D/repo" add file.txt
git -C "$D/repo" commit -q -m initial
git -C "$D/repo" push -q -u origin main

mk_worktree() {
  local name="$1" push="${2:-}"
  git -C "$D/repo" worktree add -q -B "lane/$name" "$D/wt-$name" main >/dev/null 2>&1
  if [ "$push" = push ]; then
    git -C "$D/wt-$name" push -q -u origin "lane/$name"
  fi
  printf '%s\n' "$D/wt-$name"
}

register_open_task() {
  # Give lane $SESS:$1 an open task in the ledger, the same shape a real
  # dispatch leaves behind -- lane-retire.sh's ledger release needs an open
  # row to close, exactly like lane-done.sh's does. Goes through
  # `Ledger.record_dispatch`, the same one atomic write dispatch.sh's own
  # `cli.py record-dispatch` calls, rather than reassembling dispatch.sh's
  # full multi-flag CLI surface for a fixture that only cares the row exists
  # and is open.
  local idx="$1" task="$2"
  local lane="${SESS}:${idx}"
  python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
ledger = Ledger(sys.argv[2])
ledger.record_dispatch(
    lane=sys.argv[3], pane_id="%" + sys.argv[3].replace(":", "-"), nonce="nonce-" + sys.argv[3],
    harness="claude", repo="test/repo", server_id="srv", session_id="sess",
    command="claude", task_id=sys.argv[4], source_kind="issue", source_url="https://example/1",
    source_ref="1", summary="test fixture", source_state="OPEN", evidence=["test fixture seed"],
)
' "$HERE/../../scripts/supervisor" "$D/state" "$lane" "$task"
}

# ============================================================================
# 1. clean + pushed -> freed AND the window survives, renamed to free-N
# ============================================================================
CLEAN_WT=$(mk_worktree clean push)
git -C "$D/repo" worktree list --porcelain >/dev/null
rtmux new-window -t "$SESS:2" -n as564-clean-lane -c "$CLEAN_WT"
wait_pane_cwd 2
IDX1=$(rtmux list-windows -t "$SESS" -F '#{window_index} #{window_name}' | awk '/as564-clean-lane/{print $1}')
register_open_task "$IDX1" as564-clean-lane

out=$(run "$IDX1"); rc=$?
want_exit "a clean, pushed lane is retired (exit 0)" "$rc" 0 "$out"
want_contains "renamed back to free-N" "free-${IDX1}" "$(wname "$IDX1")"
if rtmux list-windows -t "$SESS" -F '#{window_index}' | grep -qx "$IDX1"; then
  ok "the window itself was NOT killed"
else
  bad "the window itself was NOT killed" "window $IDX1 no longer exists after retirement"
fi
open_after=$(python3 - "$D/state" "$SESS:$IDX1" <<'PY'
import sqlite3, sys, os
db = os.path.join(sys.argv[1], "ledger.sqlite3")
con = sqlite3.connect(db)
row = con.execute(
    "SELECT 1 FROM tasks WHERE lane = ? AND status NOT IN ('complete','failed','cancelled')",
    (sys.argv[2],),
).fetchone()
print("open" if row else "none")
PY
)
want_contains "the ledger no longer shows an open task for the lane" "none" "$open_after"

# ============================================================================
# 2. uncommitted changes -> refused, window untouched
# ============================================================================
DIRTY_WT=$(mk_worktree dirty)
echo "unsaved" > "$DIRTY_WT/scratch.txt"
rtmux new-window -t "$SESS:3" -n as564-dirty-lane -c "$DIRTY_WT"
wait_pane_cwd 3
IDX2=$(rtmux list-windows -t "$SESS" -F '#{window_index} #{window_name}' | awk '/as564-dirty-lane/{print $1}')
register_open_task "$IDX2" as564-dirty-lane

out=$(run "$IDX2"); rc=$?
want_exit "a dirty worktree is refused (exit 1)" "$rc" 1 "$out"
want_contains "...and says why" "uncommitted changes" "$out"
want_contains "the window keeps its original name" "as564-dirty-lane" "$(wname "$IDX2")"

# ============================================================================
# 3. clean tree, but commits nobody else has -> refused
# ============================================================================
UNPUSHED_WT=$(mk_worktree unpushed)
echo two > "$UNPUSHED_WT/file.txt"
git -C "$UNPUSHED_WT" add file.txt
git -C "$UNPUSHED_WT" commit -q -m "local only, never pushed"
rtmux new-window -t "$SESS:4" -n as564-unpushed-lane -c "$UNPUSHED_WT"
wait_pane_cwd 4
IDX3=$(rtmux list-windows -t "$SESS" -F '#{window_index} #{window_name}' | awk '/as564-unpushed-lane/{print $1}')
register_open_task "$IDX3" as564-unpushed-lane

out=$(run "$IDX3"); rc=$?
want_exit "unpushed commits are refused (exit 1)" "$rc" 1 "$out"
want_contains "...and says why" "no upstream" "$out"
want_contains "the window keeps its original name" "as564-unpushed-lane" "$(wname "$IDX3")"

# And once that same branch IS pushed, retiring it succeeds.
git -C "$UNPUSHED_WT" push -q -u origin "lane/unpushed"
out=$(run "$IDX3"); rc=$?
want_exit "the same lane retires cleanly once pushed" "$rc" 0 "$out"
want_contains "renamed back to free-N" "free-${IDX3}" "$(wname "$IDX3")"

# ============================================================================
# 4. the supervisor's own window is refused on identity, regardless of tree
# ============================================================================
SUP_WT=$(mk_worktree supervisor push)
rtmux new-window -t "$SESS:5" -n as564-sup-lane -c "$SUP_WT"
wait_pane_cwd 5
IDXS=$(rtmux list-windows -t "$SESS" -F '#{window_index} #{window_name}' | awk '/as564-sup-lane/{print $1}')
SUP_WID=$(rtmux display-message -p -t "${SESS}:${IDXS}" '#{window_id}')

out=$(AGENT_SUPERVISOR_STATE_DIR="$D/state" TMUX_TMPDIR="$RT" LANES_SUPERVISOR_WINDOW="$SUP_WID" \
  env -u TMUX bash "$RETIRE" "${SESS}:${IDXS}" "$SESS" 2>&1); rc=$?
want_exit "the supervisor's own window is refused (exit 1)" "$rc" 1 "$out"
want_contains "...and says why" "supervisor's own window" "$out"
want_contains "the window keeps its original name" "as564-sup-lane" "$(wname "$IDXS")"

echo "lane-retire.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
