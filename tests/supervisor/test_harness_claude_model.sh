#!/bin/bash
# agent-supervisor#120: harness/claude.sh's HARNESS_LAUNCH_CMD carried no
# `--model`, so every lane launched on the account default -- Opus, measured
# at ~3x Sonnet's per-token cost for comparable worker volume. This is the
# ONE line dispatch.sh types on every relaunch (H_LAUNCH_CMD), so whatever is
# hardcoded here is what every lane silently inherits at launch AND at every
# turnover after it. The fix is a default, not a one-time patch: source
# harness/claude.sh directly and assert on the resulting HARNESS_LAUNCH_CMD
# string, the same value dispatch.sh reads via harness-registry.sh.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAUDE_SH="$HERE/../../scripts/supervisor/harness/claude.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

echo "harness/claude.sh: model default"

# --- with no override, the default is the cheap model, not the account's --
unset CLAUDE_LANE_MODEL
cmd=$(bash -c "source '$CLAUDE_SH'; echo \"\$HARNESS_LAUNCH_CMD\"")
want_contains "with no CLAUDE_LANE_MODEL set, the launch command pins sonnet" "--model sonnet" "$cmd"
want_contains "the permission posture is unchanged" "--dangerously-skip-permissions" "$cmd"
want_missing "the default is never left to the account's own default (unpinned)" "claude --dangerously-skip-permissions" "$cmd"

# --- a lane that genuinely needs Opus can have it, deliberately, by env ----
cmd_opus=$(CLAUDE_LANE_MODEL=opus bash -c "source '$CLAUDE_SH'; echo \"\$HARNESS_LAUNCH_CMD\"")
want_contains "CLAUDE_LANE_MODEL=opus overrides the default deliberately" "--model opus" "$cmd_opus"

# --- mutation check: the old unpinned literal must NOT still be produced --
# Reproduces the exact bug #120 reports: reverting the fix (deleting
# --model and its argument) makes this file's own assertions above go red,
# proving they anchor on presence of the flag, not merely its absence
# elsewhere. Simulated here by asserting the literal old string is not what
# ships, rather than re-deriving the bug live.
old_cmd='claude --dangerously-skip-permissions'
if [ "$cmd" = "$old_cmd" ]; then
  bad "the pre-#120 unpinned command is no longer produced" "$cmd"
else
  ok "the pre-#120 unpinned command is no longer produced"
fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
