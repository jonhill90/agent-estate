#!/bin/bash
# tui.sh's interactive curses path (as opposed to its no-tty static-frame
# fallback, covered in test_laneview.sh) can only be exercised with a real
# terminal -- curses needs an actual tty to read keys from. A real isolated
# tmux pane gives it exactly that, so this suite drives tui.sh live inside
# one, the same isolation discipline test_bootstrap_session.sh and
# test_laneview_tmux_plugin.sh use.
#
# This exists because of a real bug found in review, not a hypothetical
# one: `python3 - <<'PY' ... PY` feeds the script's OWN SOURCE to python
# over stdin, which means stdin is a heredoc, not the terminal -- curses
# then reads -1 (no key) forever, and every key the popup/plugin ever sent
# was silently dropped. It rendered correctly (output only needs stdout)
# so nothing about it looked broken until a live jump was actually tried.
# The fix moved the python source to a temp file so stdin stays the tty.
# These cases pin that fix -- j/k selection and the enter-to-jump target
# must land on the real window that was navigated to, live, not asserted.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/../../scripts/supervisor/tmux-isolation.sh"
LANEVIEW="$HERE/../../scripts/supervisor/laneview.sh"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }

echo "laneview tui.sh (interactive, real tty)"

if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP no tmux on PATH"; exit 0
fi
if ! command -v python3 >/dev/null 2>&1 || ! python3 -c 'import curses' 2>/dev/null; then
  echo "  SKIP no python3 curses module available"; exit 0
fi

RT="$(mktemp -d "${TMPDIR:-/tmp}/lv-tui-it.XXXXXX")"
unset TMUX
export TMUX_TMPDIR="$RT"
assert_isolated_tmux || exit 1
S="laneview-tui-test-$$"
cleanup() { unset TMUX; export TMUX_TMPDIR="$RT"; assert_isolated_tmux; tmux kill-server 2>/dev/null; rm -rf "$RT"; }
trap cleanup EXIT INT TERM

tmux -f /dev/null new-session -d -s "$S" -n arch -x 100 -y 30
tmux new-window -t "$S" -n free-2
tmux new-window -t "$S" -n free-3
# tui.sh itself runs in a fourth window's pane -- send-keys addresses that
# pane directly, so it doesn't matter that it is not the session's active
# window when the keys are sent.
tmux new-window -t "$S" -n runner "bash '$LANEVIEW' tui '$S'"

# Give curses.wrapper time to init and reach its first getch().
attempt=0
while [ "$attempt" -lt 20 ]; do
  out="$(tmux capture-pane -t "$S:runner" -p 2>/dev/null)"
  grep -q 'laneview tui' <<<"$out" && break
  attempt=$((attempt + 1))
  sleep 0.5
done

out="$(tmux capture-pane -t "$S:runner" -p)"
if grep -q 'laneview tui' <<<"$out" && grep -qE '^\s*\. free-2\s+supervisor$' <<<"$out"; then
  ok "the interactive screen renders the supervisor row too, same as the static frame"
else
  bad "the interactive screen renders the supervisor row too, same as the static frame" "$out"
fi

# lanes, in window order, excluding the supervisor row: arch, free-3,
# runner. `j` moves the selection off the default (arch, index 0) onto
# free-3 (index 1); `enter` must jump the SESSION's current window to
# free-3 specifically -- select-window + switch-client, never a rename,
# never a dispatch/claim call.
tmux send-keys -t "$S:runner" j
sleep 1
tmux send-keys -t "$S:runner" Enter
sleep 2

current="$(tmux display-message -t "$S" -p '#{window_name}')"
if [ "$current" = "free-3" ]; then
  ok "j then enter jumps the session's current window to the selected lane"
else
  bad "j then enter jumps the session's current window to the selected lane" "current window is '$current', wanted free-3"
fi

before_names="$(tmux list-windows -t "$S" -F '#{window_name}' | sort)"
if [ "$before_names" = "$(printf 'arch\nfree-2\nfree-3\nrunner')" ]; then
  ok "jumping renamed no window"
else
  bad "jumping renamed no window" "$before_names"
fi

tmux send-keys -t "$S:runner" q
sleep 1

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
