#!/bin/bash
# laneview-plugin-tmux is the second viewer jonhill90/agent-supervisor#7
# asked for: "a tmux plugin". Unlike laneview.sh/laneview/*.sh (stub-driven,
# tests/supervisor/test_laneview.sh), this drives REAL tmux -- the thing
# under test IS `tmux bind-key`, and a stub tmux would prove nothing about
# it. Same isolation discipline test_bootstrap_session.sh and
# test_lane_done.sh use: TMUX_TMPDIR + `unset TMUX`, never the shared
# server, never a bare `tmux kill-server` (agent-dotfiles#258).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/../../scripts/supervisor/tmux-isolation.sh"

# Resolved the same way laneview.tmux resolves its own CURRENT_DIR (cd &&
# pwd), so a literal grep against list-keys' output -- which stores
# whatever bind-key was called with, i.e. laneview.tmux's own resolved
# path -- compares like with like instead of a "../.." string that never
# appears in tmux's own state.
PLUGIN_DIR="$(cd "$HERE/../../scripts/supervisor/laneview-plugin-tmux" && pwd)"
PLUGIN="$PLUGIN_DIR/laneview.tmux"
POPUP="$PLUGIN_DIR/popup.sh"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }

echo "laneview-plugin-tmux"

if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP no tmux on PATH"; exit 0
fi

RT="$(mktemp -d "${TMPDIR:-/tmp}/laneview-plugin-tmux.XXXXXX")"
unset TMUX
export TMUX_TMPDIR="$RT"
assert_isolated_tmux || exit 1
S="laneview-plugin-test-$$"
cleanup() { unset TMUX; export TMUX_TMPDIR="$RT"; assert_isolated_tmux; tmux kill-server 2>/dev/null; rm -rf "$RT"; }
trap cleanup EXIT INT TERM

tmux -f /dev/null new-session -d -s "$S" -x 80 -y 24 2>&1

# 1. Sourcing the plugin binds the default key, and only in the prefix
#    table -- it must not shadow a key globally.
tmux run-shell "$PLUGIN" 2>&1
if tmux list-keys -T prefix | grep -qF "$POPUP"; then
  ok "sourcing laneview.tmux binds the default key to popup.sh"
else
  bad "sourcing laneview.tmux binds the default key to popup.sh" "$(tmux list-keys -T prefix)"
fi

# 2. `@laneview-key` overrides which key is bound, without editing
#    laneview.tmux -- so the whole plugin has no hardcoded key.
tmux set-option -g @laneview-key 'v' 2>&1
tmux run-shell "$PLUGIN" 2>&1
if tmux list-keys -T prefix -N | grep -E '^\s*v\s' | grep -qF "$POPUP" \
  || tmux list-keys -T prefix | grep -E '(^|[[:space:]])v([[:space:]])' | grep -qF "$POPUP"; then
  ok "@laneview-key overrides the bound key"
else
  bad "@laneview-key overrides the bound key" "$(tmux list-keys -T prefix)"
fi
tmux set-option -gu @laneview-key 2>&1

# 3. Static check on popup.sh and laneview.tmux: neither names a
#    destructive or state-writing tmux verb. The plugin is a launcher --
#    its only tmux-affecting calls are bind-key (once, at load) and
#    display-popup (per keypress). rename-window, kill-*, and send-keys
#    (which would let a "viewer" type into a lane) must never appear.
FORBIDDEN='rename-window|kill-session|kill-server|kill-window|send-keys'
if ! grep -qE "$FORBIDDEN" "$PLUGIN" && ! grep -qE "$FORBIDDEN" "$POPUP"; then
  ok "the plugin's own scripts name no destructive or writing tmux verb"
else
  bad "the plugin's own scripts name no destructive or writing tmux verb" \
    "$(grep -nE "$FORBIDDEN" "$PLUGIN" "$POPUP")"
fi

# 4. Apart: nothing CODE outside this plugin's own directory depends on
#    it, so `rm -rf` of the directory is a clean deletion -- the #231
#    discipline ("removing either is a deletion", verified with grep, not
#    asserted). Scoped to .sh: laneview/README.md names this directory in
#    prose (the cross-reference added alongside it, see that file's "The
#    tmux plugin" section) -- documentation pointing at a sibling is not a
#    dependency, and grepping markdown would fail on the very doc this
#    property is described in.
REPO_ROOT="$HERE/../.."
hits=$(grep -rl 'laneview-plugin-tmux' "$REPO_ROOT/scripts" "$REPO_ROOT/tests" \
    --include='*.sh' --include='*.tmux' --include='*.py' 2>/dev/null \
  | grep -v '^'"$REPO_ROOT"'/scripts/supervisor/laneview-plugin-tmux/' \
  | grep -v '^'"$REPO_ROOT"'/tests/supervisor/test_laneview_tmux_plugin.sh$')
if [ -z "$hits" ]; then
  ok "nothing outside laneview-plugin-tmux/ and this test depends on it in code"
else
  bad "nothing outside laneview-plugin-tmux/ and this test depends on it in code" "$hits"
fi

# 5. Together: the popup launches laneview.sh, which reads lanes.sh --json
#    directly -- no dependency on the supervisor (dispatch.sh/watchdog.sh)
#    being alive. Verified statically: popup.sh's only tmux-affecting call
#    is display-popup, and its content command is laneview.sh, not any
#    supervisor script.
if grep -q 'display-popup' "$POPUP" && ! grep -qE 'dispatch\.sh|watchdog\.sh|notify\.sh' "$POPUP"; then
  ok "popup.sh's launch path never names a headless supervisor script"
else
  bad "popup.sh's launch path never names a headless supervisor script" "$(cat "$POPUP")"
fi

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
