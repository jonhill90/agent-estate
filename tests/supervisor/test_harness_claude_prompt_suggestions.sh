#!/bin/bash
# agent-supervisor#521: Claude Code's own "prompt suggestion" feature paints
# a dim, unsubmitted predicted-next-message into an empty input box after
# every turn -- on by default. That is the mechanism #521's investigation
# traced for recurring unattributed text seen in idle build panes: nobody
# typed it, the harness painted it. The fix is a launch-time default, same
# posture as #120's `--model` pin -- HARNESS_LAUNCH_CMD is the ONE line
# dispatch.sh types on every relaunch, so whatever is (or isn't) set here is
# what every lane silently inherits at launch AND at every turnover after.
#
# `--prompt-suggestions=false` (the documented `claude --help` flag) was
# tried first and does NOT work -- verified live in an isolated tmux socket,
# a pane launched with it still painted the exact same dim post-turn
# suggestion as an unflagged pane, on two separate turns. What actually
# works, also verified live the same way, is the undocumented env var
# `CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false` (named in the shipped
# binary's own strings). This test pins the env var, not the flag -- a
# regression to the documented-but-broken flag would fail this test even
# though it looks like the "obvious" fix.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAUDE_SH="$HERE/../../scripts/supervisor/harness/claude.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

echo "harness/claude.sh: prompt-suggestion suppression (#521)"

launch_cmd=$(bash -c "source '$CLAUDE_SH'; echo \"\$HARNESS_LAUNCH_CMD\"")
want_contains "launch carries the working env var" "CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false" "$launch_cmd"
want_missing "launch does NOT rely on the documented-but-broken CLI flag" "--prompt-suggestions" "$launch_cmd"

resume_cmd=$(bash -c "source '$CLAUDE_SH'; echo \"\$HARNESS_RESUME_CMD\"")
want_contains "resume carries the same env var (a restore must not reopen #521)" "CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false" "$resume_cmd"
want_missing "resume does NOT rely on the broken CLI flag either" "--prompt-suggestions" "$resume_cmd"

unattended_cmd=$(bash -c "source '$CLAUDE_SH'; echo \"\$HARNESS_UNATTENDED_CMD\"")
want_contains "unattended launch (same string as HARNESS_LAUNCH_CMD) carries it too" "CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false" "$unattended_cmd"

# --- mutation check: the pre-#521 command (no suppression at all) must not
# still be what ships -- proves the assertions above anchor on presence of
# the var, not merely absence of something else.
old_cmd="claude --model sonnet --dangerously-skip-permissions --strict-mcp-config"
if [ "$launch_cmd" = "$old_cmd" ]; then
  bad "the pre-#521 unsuppressed command is no longer produced" "$launch_cmd"
else
  ok "the pre-#521 unsuppressed command is no longer produced"
fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
