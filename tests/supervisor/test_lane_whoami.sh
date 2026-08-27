#!/bin/bash
# agent-supervisor#685: every review/fix-pass brief this loop writes told the
# lane to derive its own `Review-Lane:` id with a BARE
#
#   tmux display-message -p -t "$TMUX_PANE" '#{session_name}:#{window_index}'
#
# A `claude-print` lane has NO PANE: `$TMUX_PANE` is unset, `-t` becomes
# meaningless, and `display-message` silently answers for whichever window
# happens to be FOCUSED. Measured on agent-dotfiles#330's review: dispatched
# to `estate:2`, the trailer reported `estate:1` -- the director's pane,
# which happened to be focused.
#
# This drives REAL tmux, because the thing under test is precisely tmux's
# own focus-fallback behaviour -- a stub cannot reproduce it.
#
# INVARIANT 4: creates a session, so TMUX_TMPDIR is set and
# assert_isolated_tmux gates it before anything is created or killed.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
WHOAMI="$SUP/lane-whoami.sh"
CLI="$SUP/cli.py"
source "$SUP/tmux-isolation.sh"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

echo "lane-whoami.sh (agent-supervisor#685)"

missing() {  # missing <what> -- SKIP locally, FAIL on CI (validate.yml installs deps there)
  if [ -n "${CI:-}" ]; then
    echo "  FAIL $1 -- required on CI"
    exit 1
  fi
  echo "  SKIP $1"
  exit 0
}
command -v tmux >/dev/null 2>&1 || missing "no tmux on PATH"

S="lane-whoami-test-$$"
RT="$(mktemp -d "${TMPDIR:-/tmp}/lane-whoami-tmux.XXXXXX")"
D="$(mktemp -d "${TMPDIR:-/tmp}/lane-whoami.XXXXXX")"
STATE="$D/state"; WORK="$D/work"
mkdir -p "$STATE" "$WORK"
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$RT"
assert_isolated_tmux || exit 1

cleanup() {
  unset TMUX TMUX_PANE
  export TMUX_TMPDIR="$RT"
  tmux kill-session -t "$S" 2>/dev/null
  rm -rf "$RT" "$D"
}
trap cleanup EXIT INT TERM

tmux new-session -d -s "$S" -c "$WORK" "sleep 600" || { echo "  FAIL could not create the isolated session"; exit 1; }
tmux new-window -t "$S" -c "$WORK" "sleep 600"

# `list-windows`, never `display-message -t "$S"`: targeting a SESSION
# answers for whichever window is ACTIVE, invariant 10's own trap reproduced
# inside the test's own setup.
DIRECTOR_IDX=$(tmux list-windows -t "$S" -F '#{window_index}' | head -n1)
REVIEWER_IDX=$(tmux list-windows -t "$S" -F '#{window_index}' | tail -n1)
REVIEWER_LANE="$S:$REVIEWER_IDX"
REVIEWER_PANE=$(tmux display-message -p -t "$S:$REVIEWER_IDX" '#{pane_id}')

# ============================================================================
# 1. Pane lane -- focus independence
# ============================================================================
# Focus the DIRECTOR window, then ask lane-whoami.sh from the reviewer's own
# pane. If the answer were focus-dependent, this would report the director.
tmux select-window -t "$S:$DIRECTOR_IDX"
got=$(TMUX_PANE="$REVIEWER_PANE" bash "$WHOAMI" 2>&1); rc=$?
[ "$rc" -eq 0 ] && [ "$got" = "$REVIEWER_LANE" ] \
  && ok "pane lane reports its own identity while a different window is focused" \
  || bad "pane lane reports its own identity" "rc=$rc got='$got' want='$REVIEWER_LANE'"

# Now move focus TO the reviewer's own window and confirm the answer does
# not move -- proving the result was never reading focus at all.
tmux select-window -t "$S:$REVIEWER_IDX"
got=$(TMUX_PANE="$REVIEWER_PANE" bash "$WHOAMI" 2>&1); rc=$?
[ "$rc" -eq 0 ] && [ "$got" = "$REVIEWER_LANE" ] \
  && ok "...and the answer does not move when focus changes to match it" \
  || bad "answer stable across a focus change" "rc=$rc got='$got'"

# --- positive control: the OLD bare command actually IS focus-dependent ----
# (proves the harness can detect the defect at all -- a check that could
# never fail would prove nothing above.)
tmux select-window -t "$S:$DIRECTOR_IDX"
old_answer=$(TMUX_PANE="$REVIEWER_PANE" tmux display-message -p '#{session_name}:#{window_index}' 2>&1)
[ "$old_answer" = "$S:$DIRECTOR_IDX" ] \
  && ok "positive control: the bare (no -t) command really does report the focused window, not the reviewer's" \
  || bad "positive control: bare display-message is focus-dependent" "got '$old_answer', focused window is '$S:$DIRECTOR_IDX'"

# ============================================================================
# 2. Pane-less (claude-print) lane -- worktree-lane self-lookup
# ============================================================================
REVIEW_WT="$D/wt-review"
mkdir -p "$REVIEW_WT"
git -C "$REVIEW_WT" init -q
git -C "$REVIEW_WT" config user.email test@example.com
git -C "$REVIEW_WT" config user.name Test
git -C "$REVIEW_WT" commit -q --allow-empty -m init

# Seed the dispatch record `dispatch-claude-print.sh` would have written:
# a review task, worktree-recorded, lane id == task id (no window to index).
python3 "$CLI" --state-dir "$STATE" record-dispatch \
  --lane "ad329-rev330" --task "ad329-rev330" --summary "review PR 330" \
  --pane-id "claude-print:ad329-rev330" --pane-path "$REVIEW_WT" \
  --command claude --server-id srv --session-id sess --issue 330 \
  --harness claude --worktree "$REVIEW_WT" >/dev/null

# --- positive control: the OLD instruction, run with $TMUX_PANE unset (the
# claude-print shape), reports the FOCUSED window -- wrong, and not even a
# lane id shaped like the reviewer's own. Same live session as above, so
# this is genuinely the SAME lane's surrounding estate reporting two
# different answers depending only on the method used. --------------------
tmux select-window -t "$S:$DIRECTOR_IDX"
old_answer=$(cd "$REVIEW_WT" && env -u TMUX_PANE tmux display-message -p '#{session_name}:#{window_index}' 2>&1)
[ "$old_answer" = "$S:$DIRECTOR_IDX" ] && [ "$old_answer" != "ad329-rev330" ] \
  && ok "positive control: the old instruction, with \$TMUX_PANE unset, names the focused window, not the reviewer" \
  || bad "positive control: old instruction wrong for a pane-less lane" "got '$old_answer'"

# --- new: lane-whoami.sh, same worktree, same unset $TMUX_PANE, reports the
# reviewer's TRUE identity instead --------------------------------------
new_answer=$(cd "$REVIEW_WT" && env -u TMUX_PANE AGENT_SUPERVISOR_STATE_DIR="$STATE" bash "$WHOAMI" 2>&1); rc=$?
[ "$rc" -eq 0 ] && [ "$new_answer" = "ad329-rev330" ] \
  && ok "new: a pane-less lane reports its true identity via worktree-lane" \
  || bad "pane-less lane reports true identity" "rc=$rc got='$new_answer'"

# --- fail-closed: a worktree the ledger never dispatched anything to must
# refuse, not guess. If this could not fail, the check above would have
# proven nothing (mutation check, second direction). -----------------------
UNKNOWN_WT="$D/wt-unknown"
mkdir -p "$UNKNOWN_WT"
git -C "$UNKNOWN_WT" init -q
git -C "$UNKNOWN_WT" commit -q --allow-empty -m init 2>/dev/null || true
out=$(cd "$UNKNOWN_WT" && env -u TMUX_PANE AGENT_SUPERVISOR_STATE_DIR="$STATE" bash "$WHOAMI" 2>&1); rc=$?
[ "$rc" -ne 0 ] && [ -z "$(cd "$UNKNOWN_WT" && env -u TMUX_PANE AGENT_SUPERVISOR_STATE_DIR="$STATE" bash "$WHOAMI" 2>/dev/null)" ] \
  && ok "...and an undispatched worktree refuses rather than fabricating an identity" \
  || bad "undispatched worktree refuses" "rc=$rc: $out"

echo "  -> $pass ok, $fail failed"
[ "$fail" -eq 0 ]
