#!/bin/bash
# agent-supervisor#494: neither launch site passed `--strict-mcp-config`, so
# every lane inherited the OPERATOR's whole MCP surface -- claude with no such
# flag loads the global `mcpServers` from ~/.claude.json plus any
# project-scoped block matching the lane's cwd. Measured on 2026-08-21: the
# machine sat at 3% free memory with 8.7GB of 10.2GB swap in use, and the
# terminal lagged on keystrokes.
#
# A lane does git, gh and bash. It has never needed Mintlify, Linear,
# Cloudflare, Playwright, context7, microsoft-learn or deepwiki.
#
# TWO sites, deliberately, because #135 is the precedent: #120 was closed as
# "fixed" after patching harness/claude.sh alone while a second launch literal
# in another file went on shipping the old behaviour. tmux lanes launch from
# harness/claude.sh; print-mode lanes launch from claude_print_transport.py.
# This file asserts on both, and the last check fails on ANY third site.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAUDE_SH="$HERE/../../scripts/supervisor/harness/claude.sh"
SUPERVISOR_DIR="$HERE/../../scripts/supervisor"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

echo "harness/claude.sh + claude_print_transport.py: MCP surface"

# --- site 1, tmux lanes: strict by default, no servers ---------------------
unset CLAUDE_LANE_MCP_CONFIG
cmd=$(bash -c "source '$CLAUDE_SH'; echo \"\$HARNESS_LAUNCH_CMD\"")
want_contains "tmux lane launches --strict-mcp-config by default" "--strict-mcp-config" "$cmd"
want_missing  "with no override, no --mcp-config is named (zero servers)" "--mcp-config" "$cmd"
want_contains "the model pin from #120 is not disturbed" "--model sonnet" "$cmd"
want_contains "the permission posture from #232 is not disturbed" "--dangerously-skip-permissions" "$cmd"

# --- a lane that genuinely needs one server can have exactly that one ------
cmd_one=$(CLAUDE_LANE_MCP_CONFIG=/tmp/one.json bash -c "source '$CLAUDE_SH'; echo \"\$HARNESS_LAUNCH_CMD\"")
want_contains "CLAUDE_LANE_MCP_CONFIG names a config deliberately" "--mcp-config /tmp/one.json" "$cmd_one"
want_contains "and it stays strict, so nothing else is inherited alongside it" "--strict-mcp-config" "$cmd_one"

# --- site 2, print-mode lanes ---------------------------------------------
py_cmd=$(cd "$SUPERVISOR_DIR" && python3 -c "
from claude_print_transport import ClaudePrintTransport
t = ClaudePrintTransport(cwd='/tmp', model='sonnet')
print(' '.join(t._base_command()))
" 2>&1)
want_contains "print-mode lane launches --strict-mcp-config by default" "--strict-mcp-config" "$py_cmd"
want_missing  "print-mode names no --mcp-config by default (zero servers)" "--mcp-config" "$py_cmd"

py_cmd_one=$(cd "$SUPERVISOR_DIR" && python3 -c "
from claude_print_transport import ClaudePrintTransport
t = ClaudePrintTransport(cwd='/tmp', model='sonnet', mcp_config='/tmp/one.json')
print(' '.join(t._base_command()))
" 2>&1)
want_contains "print-mode mcp_config names a config deliberately" "--mcp-config /tmp/one.json" "$py_cmd_one"
want_contains "and print-mode stays strict alongside it" "--strict-mcp-config" "$py_cmd_one"

# --- MUTATION CHECK, both directions --------------------------------------
# Direction 1 (the guard is real): strip --strict-mcp-config from a COPY of
# the harness and confirm the assertion above would have gone red. A check
# that stays green when the flag is deleted is not a check.
mutant="$(mktemp -t claude-mcp-mutant).sh"
# Remove the flag wherever it appears -- both inside the CLAUDE_LANE_MCP_FLAGS
# assignment (no leading space) and appended to the launch string. An earlier
# version of this mutation stripped only " --strict-mcp-config" with a leading
# space, which left the assignment intact and produced a mutant that still
# shipped the flag: the mutation reported the guard as load-bearing without
# ever having removed it. That is the false-green this whole file exists to
# prevent, so it is recorded rather than quietly corrected.
sed 's/--strict-mcp-config//g' "$CLAUDE_SH" > "$mutant"
mutant_cmd=$(bash -c "source '$mutant'; echo \"\$HARNESS_LAUNCH_CMD\"")
if grep -qF -- "--strict-mcp-config" <<<"$mutant_cmd"; then
  bad "mutation: removing the flag must change the launch command" "$mutant_cmd"
else
  ok "mutation: removing the flag DOES change the launch command (guard is load-bearing)"
fi
rm -f "$mutant"

# Direction 2 (the fix is real): the pre-#494 literal -- a claude launch with
# a model and permissions but no MCP scoping -- must not be what ships.
old_cmd='claude --model sonnet --dangerously-skip-permissions'
if [ "$cmd" = "$old_cmd" ]; then
  bad "the pre-#494 unscoped command is no longer produced" "$cmd"
else
  ok "the pre-#494 unscoped command is no longer produced"
fi

# --- no THIRD site: any other claude launch literal must be scoped too -----
# What this covers: any FILE under scripts/supervisor/ that builds a claude
# launch (identified by --dangerously-skip-permissions, the flag every one of
# them carries) but never mentions --strict-mcp-config anywhere in it. The
# check is per-FILE, not per-line, deliberately: the print-mode transport
# appends the two flags on separate lines, so a line-scoped grep reports the
# fixed file as a violation -- which is exactly what an earlier version of
# this check did.
#
# What it does NOT cover: a literal assembled from concatenated variables this
# grep cannot see textually, or a launch literal for codex/copilot (those
# harnesses have their own MCP story and their own adapters).
stray=""
while IFS= read -r f; do
  case "$f" in */harness/*|*/laneview/*|*__pycache__*) continue ;; esac
  grep -qF -- '--strict-mcp-config' "$f" || stray="$stray$f"$'\n'
done < <(grep -rlI -- '--dangerously-skip-permissions' "$SUPERVISOR_DIR" 2>/dev/null | grep -E '\.(sh|py)$' || true)
if [ -z "$stray" ]; then
  ok "no unscoped (no --strict-mcp-config) claude launch file outside harness/"
else
  bad "no unscoped (no --strict-mcp-config) claude launch file outside harness/" "$stray"
fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
