#!/bin/bash
# poller-window.sh is the shared tmux window lookup for advance-live.sh,
# lanes.sh, and poller-recover.sh. Duplicate poller windows are ambiguous:
# callers must get rc=2 and no guessed target.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HELPER="$HERE/../../scripts/supervisor/poller-window.sh"
source "$HERE/../../scripts/supervisor/tmux-isolation.sh"

S="poller-window-test-$$"
RT="$(mktemp -d "${TMPDIR:-/tmp}/poller-window-tmux.XXXXXX")"
unset TMUX
export TMUX_TMPDIR="$RT"
assert_isolated_tmux || exit 1
pass=0; fail=0

ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 - $2"; fail=$((fail+1)); }

cleanup() { unset TMUX; export TMUX_TMPDIR="$RT"; tmux kill-session -t "$S" 2>/dev/null; rm -rf "$RT"; }
trap cleanup EXIT INT TERM

if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP no tmux on PATH"; exit 0
fi

echo "poller-window.sh"

tmux new-session -d -s "$S" -x 200 -y 50 -n not-poller
source "$HELPER"

out=$(poller_window_target "$S" 2>&1); rc=$?
[ "$rc" -eq 1 ] && [ -z "$out" ] && ok "zero poller windows returns 1 and prints nothing" \
  || bad "zero poller windows returns 1 and prints nothing" "rc=$rc out=$out"

tmux new-window -t "$S" -n inbox-poll -d
want=$(poller_window_ids "$S" | head -1)
out=$(poller_window_target "$S" 2>&1); rc=$?
[ "$rc" -eq 0 ] && [ "$out" = "$S:$want" ] && ok "one poller window returns its session-qualified window id" \
  || bad "one poller window returns its session-qualified window id" "rc=$rc out=$out want=$S:$want"

tmux new-window -t "$S" -n inbox-poll -d
out=$(poller_window_target "$S" 2>&1); rc=$?
[ "$rc" -eq 2 ] && [ -z "$out" ] && ok "two poller windows returns 2 and prints nothing" \
  || bad "two poller windows returns 2 and prints nothing" "rc=$rc out=$out"

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
