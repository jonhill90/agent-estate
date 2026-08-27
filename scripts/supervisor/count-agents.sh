#!/usr/bin/env bash
# count-agents.sh -- print the number of Claude agent SESSIONS running on
# this host, and nothing else. Nothing wires this in yet; it is the
# instrument the estate tick's STEP 0 host guard should call instead of its
# current `pgrep -f claude | wc -l`.
#
# WHY THIS EXISTS (agent-supervisor#663): `pgrep -f claude` matches
# on ANY substring of a process's full argv, not on what the process
# actually IS. Measured live on this host, one sample, `pgrep -f claude`
# read 22 against 17 real agent sessions:
#   17  agent session   claude --model sonnet <harness/claude.sh's own launch flags>
#    2  transient zsh   /bin/zsh -c source ~/.claude/shell-snapshots/snapshot-*.sh ...
#    1  sed             the measuring pipeline itself, matching its own argv
#    1  tail -f         following a log living under a path containing "claude"
#    1  daemon          bg-spare (cc-daemon's spare-claim helper)
#    1  daemon          bg-pty-host (cc-daemon's pty host)
# The noise is transient (readings the same morning: 19, 21, 19, 20, 22, 24,
# 22) while the agent-session count held at 17 throughout -- so the host
# guard was firing early and non-deterministically against a ceiling written
# about agent sessions, not shell noise.
#
# THE FIX IS THE INSTRUMENT, NOT THE THRESHOLD. #663 explicitly does not
# touch whether 20 is the right ceiling for agent sessions -- only that the
# number compared against it should BE that.
#
# HOW THIS MATCHES THE PROGRAM, NOT A PATH: `ps`'s `comm` field is the
# kernel's own short process name/title (the same field tmux's own
# `pane_current_command` reads, and the field `lanes.sh`'s adapters already
# match panes on -- see harness/claude.sh's HARNESS_COMMAND_RE, sourced
# below rather than re-typed here so the two never drift apart). It is NOT
# argv -- a `zsh -c '... ~/.claude/shell-snapshots/... ...'` shell's comm is
# `/bin/zsh` regardless of what its argv mentions, and this script's own
# `ps`/`awk` subprocesses have comm `ps`/`awk`, never `claude`, so a run of
# this script inside a pipeline whose own text contains the word "claude"
# cannot move its own count (verified live, see PR description).
#
# THE PIVOT THAT MAKES THIS SAFE: `comm` MUST be requested as the LAST `-o`
# field, or BSD ps truncates it to a fixed width (observed: `claude
# bg-pty-host` truncates to `claude bg-pty-ho` at 16 chars when comm is not
# last) -- which would silently misclassify a truncated daemon-helper title
# as something else. Every `ps` call below keeps comm last for exactly this
# reason; do not reorder the `-o` spec.
#
# WHY DAEMON HELPERS DO NOT COUNT: `claude bg-pty-host` and `claude
# bg-spare` are cc-daemon's own infrastructure processes (a pooled pty host
# and its spare-claim helper) -- real, but not a session anyone is running
# an agent turn in. Their comm strings are titles the daemon sets on itself
# (`claude bg-pty-host`, `claude bg-spare`), which is exactly why matching
# comm EXACTLY against `claude`/`claude.exe` (never a prefix, never a
# substring) excludes them without needing to name them specially.
#
# SCOPE, DELIBERATELY: Claude only, matching #663's own measurement. Other
# harness adapters exist (harness/codex.sh: `^codex$`, harness/copilot.sh:
# `^node$`), but copilot's `^node$` would match ANY node process host-wide
# -- unsafe to fold into a host census without its own dedicated
# measurement, which #663 did not do. Widening this past Claude is a
# separate, unmeasured claim; left out rather than guessed at.
#
# Usage:
#   count-agents.sh              -- prints one line: the integer count
#   count-agents.sh --verbose    -- also prints, to stderr, every process
#                                    matched or excluded and why -- so a
#                                    disputed number is a lookup, not an
#                                    argument.
# Exit 0 on a successful count (including zero -- an idle host is a real,
# reportable state). Exit 2 if `ps` itself could not be read at all -- this
# estate's own "absence is a typed value" rule (host-pressure.sh's own
# comment): a census that saw nothing must not look identical to a host
# that was never asked.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# The one source of truth for what a real Claude agent process's comm looks
# like -- lanes.sh's own adapter, not a second copy of the same regex. Pure
# variable assignments (checked: no top-level command invocation in the
# file), safe to source for its value alone.
# shellcheck source=./harness/claude.sh
. "$HERE/harness/claude.sh"
AGENT_COMMAND_RE="$HARNESS_COMMAND_RE"

VERBOSE=0
for arg in "$@"; do
  case "$arg" in
    --verbose|-v) VERBOSE=1 ;;
    *) echo "count-agents.sh: unknown argument: $arg" >&2; exit 2 ;;
  esac
done

# pid + comm only, comm LAST so it is never truncated (see header comment).
# The loop below reconstructs comm by dropping just the first (numeric)
# field rather than splitting on every space, since a daemon helper's own
# comm can legitimately contain spaces ("claude bg-pty-host").
PS_OUT=$(ps -Ao pid=,comm= 2>/dev/null) || {
  echo "count-agents.sh: could not read the process table (ps failed) -- refusing to guess" >&2
  exit 2
}
if [ -z "$PS_OUT" ]; then
  echo "count-agents.sh: ps returned nothing at all -- refusing to trust an empty process table" >&2
  exit 2
fi

count=0
matched_lines=()
excluded_pids=()
while IFS= read -r line; do
  [ -n "$line" ] || continue
  pid="${line%% *}"
  comm="${line#* }"
  if [[ "$comm" =~ $AGENT_COMMAND_RE ]]; then
    count=$((count + 1))
    matched_lines+=("$pid  $comm")
  else
    excluded_pids+=("$pid")
  fi
done <<<"$PS_OUT"

echo "$count"

if [ "$VERBOSE" -eq 1 ]; then
  # A full excluded dump is every OTHER process on the host (hundreds) --
  # useless as a breakdown. Narrow to the pids a NAIVE `pgrep -f claude`
  # would also have matched (comm or full argv mentions "claude"
  # case-insensitively) -- that is the actual disputed set #663 measured,
  # and the only one worth explaining. A second `ps` call, command= LAST
  # so it is never truncated either (same rule as comm above).
  CMD_OUT=$(ps -Ao pid=,command= 2>/dev/null || true)
  {
    echo "count-agents: $count agent session(s), matched against comm =~ $AGENT_COMMAND_RE"
    echo "counted:"
    if [ "${#matched_lines[@]}" -eq 0 ]; then
      echo "  (none)"
    else
      printf '  %s\n' "${matched_lines[@]}"
    fi
    echo "excluded, restricted to pids a naive 'pgrep -f claude' would also have matched:"
    shown=0
    for pid in "${excluded_pids[@]:-}"; do
      [ -n "$pid" ] || continue
      cmdline=$(awk -v p="$pid" '$1==p { $1=""; sub(/^ /,""); print; exit }' <<<"$CMD_OUT")
      [ -n "$cmdline" ] || continue
      case "$cmdline" in
        *[Cc][Ll][Aa][Uu][Dd][Ee]*) : ;;
        *) continue ;;
      esac
      shown=$((shown + 1))
      case "$cmdline" in
        "claude bg-pty-host"*) reason="daemon helper (cc-daemon pty host)" ;;
        "claude bg-spare"*)    reason="daemon helper (cc-daemon spare-claim)" ;;
        "claude daemon run"*)  reason="daemon helper (cc-daemon supervisor)" ;;
        /bin/zsh*|/bin/bash*|*/zsh*|*/bash*) reason="transient shell (argv references a .claude path)" ;;
        tail*)                 reason="log follower (path contains \"claude\")" ;;
        sed*|awk*|grep*|ugrep*|rg*) reason="measuring pipeline itself" ;;
        *)                      reason="other, comm did not match $AGENT_COMMAND_RE" ;;
      esac
      printf '  %-6s %-55s -- %s\n' "$pid" "$cmdline" "$reason"
    done
    if [ "$shown" -eq 0 ]; then
      echo "  (none -- no excluded process's argv mentions \"claude\" at all)"
    fi
  } >&2
fi

exit 0
