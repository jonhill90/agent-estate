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
# agent-estate#613 sub-decision 1: THAT comm-exact-match ALSO excludes a
# second, wider family of cc-daemon infrastructure -- but by accident, not
# by name. On a host where a node-launched `claude.exe` resolves through
# `comm` as the FULL interpreter path (e.g.
# `/opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe`,
# confirmed live via `ps -Ao pid=,comm=`), that path never equals the bare
# `claude.exe` this regex expects -- so `claude.exe daemon run ...` and the
# `--session-id ... --fork-session --resume ...` processes cc-daemon forks
# from its pooled pty host are dropped from the count too, but only because
# the path happens not to match, not because anything here recognizes them.
# If the install path ever resolves such that comm becomes the bare
# `claude.exe` again, these would silently start counting -- no code
# change, no signal, just less headroom than expected the next ceiling
# trip. DAEMON_ARGV_RE below (checked against full argv, independent of
# whatever comm happens to resolve to) makes this deliberate: a process is
# daemon-owned because its OWN argv says so (`--bg-pty-host`, `--bg-spare`,
# the `daemon run` subcommand, or `--fork-session` -- the flag cc-daemon's
# pooled-session forking uses that no estate launch/resume path ever
# passes: HARNESS_LAUNCH_CMD and HARNESS_RESUME_CMD in harness/claude.sh
# carry neither `--session-id` nor `--fork-session`), never because comm
# happened not to match.
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

# agent-estate#613 sub-decision 1 / agent-estate#871 reviews (ae871-rev871,
# REQUEST_CHANGES on 28adf24; ae871-rerev871, REQUEST_CHANGES on c34b6c3):
# argv shapes that positively identify a cc-daemon-owned process,
# independent of what `comm` resolves to on this host (see the header
# comment above).
#
# ae871-rev871's finding (round 1): the first cut of this regex anchored
# ONLY the `daemon run` alternative to a token sequence; the other three
# (--bg-pty-host, --bg-spare, --fork-session) were bare substring matches
# against the WHOLE argv string. A dispatched claude -p lane's own task
# prompt is itself a positional argv element (claude_print_transport.py:
# `command.append(prompt)`, always the LAST token, after every real launch
# flag) -- so a lane merely asked to review/discuss this issue (whose
# prompt then names these flags) had its own comm=claude session excluded.
#
# ae871-rerev871's finding (round 2): the round-1 fix anchored every
# alternative with `(^|/)claude...`, intending "the daemon's own launch
# line starts here" -- but `(^|/)` is not a start-of-string anchor, it is
# "start of string, OR after ANY slash anywhere in the string". A lane
# whose prompt merely QUOTES the daemon's own path-qualified launch line
# (e.g. reviewing this very PR, whose diff/description/comments all do
# exactly that) still has a `/`-preceded `claude(.exe)` token sitting in
# its argv, deep inside the free-text prompt -- so the same false-exclusion
# reopened one layer down. Narrowing the alternatives further would only
# repeat this: the underlying defect is that a flattened `ps` command
# string has no positional information once matched with a non-anchored
# regex, so ANY substring-shaped check -- however cleverly worded -- can be
# satisfied by prose that happens to quote the real shape.
#
# THE FIX (round 3): anchor every alternative to the ACTUAL start of the
# argv string with a real `^`, not `(^|/)`. CMD_OUT's lines are built by
# `daemon_argv_for_pid`, which strips only the leading pid field -- so the
# string checked here begins EXACTLY at argv[0] (the invoked program name
# or path), with no earlier text to anchor past. `^([^[:space:]]*/)?claude`
# matches "claude/claude.exe as the literal first token" (bare name) or "a
# path ending in claude(.exe) as the literal first token" (full
# interpreter path) -- and ONLY there: position 0 of the whole string, not
# after every slash the string happens to contain. A print-mode lane's own
# launch flags (`-p`, `--output-format`, `--model`, `--resume <id>`, ...)
# occupy argv[1] onward, and its free-text prompt is pushed to the END --
# neither can ever BE argv[0], so prose quoting the daemon's launch line,
# however faithfully, can no longer match: `^` requires the daemon's own
# invocation to be the thing that started the process, not text the
# process was merely told to think about. Each alternative names ONE
# daemon role, mirroring the header's own bg-pty-host/bg-spare naming:
#   --bg-pty-host   cc-daemon's pooled pty host -- launched as
#                    `claude(.exe) [bg-pty-host ]--bg-pty-host ...` (the
#                    `bg-pty-host` subcommand token is optional: present
#                    when comm still carries the daemon's own title, absent
#                    when comm has resolved to a bare interpreter path)
#   --bg-spare      cc-daemon's spare-claim helper -- same shape,
#                    `claude(.exe) [bg-spare ]--bg-spare ...`
#   daemon run      the `claude(.exe) daemon run` subcommand, cc-daemon's
#                    supervisor process -- anchored to "claude(.exe) daemon
#                    run" as a token sequence starting at argv[0], not a
#                    bare substring
#   --fork-session  the flag cc-daemon uses to fork a pooled session,
#                    always launched as `claude(.exe) --session-id <id>
#                    --fork-session ...` -- checked against
#                    harness/claude.sh's own HARNESS_LAUNCH_CMD and
#                    HARNESS_RESUME_CMD (2026-08-29): neither ever passes
#                    `--session-id` immediately after the invocation's own
#                    name, so this shape appearing there is never a real
#                    dispatched or restored lane, only cc-daemon's own
#                    forking
DAEMON_ARGV_RE='^([^[:space:]]*/)?claude(\.exe)?[[:space:]]+(bg-pty-host[[:space:]]+)?--bg-pty-host([[:space:]]|$)'
DAEMON_ARGV_RE="$DAEMON_ARGV_RE"'|^([^[:space:]]*/)?claude(\.exe)?[[:space:]]+(bg-spare[[:space:]]+)?--bg-spare([[:space:]]|$)'
DAEMON_ARGV_RE="$DAEMON_ARGV_RE"'|^([^[:space:]]*/)?claude(\.exe)?[[:space:]]+--session-id[[:space:]]+[^[:space:]]+[[:space:]]+--fork-session([[:space:]]|$)'
DAEMON_ARGV_RE="$DAEMON_ARGV_RE"'|^([^[:space:]]*/)?claude(\.exe)?[[:space:]]+daemon[[:space:]]+run([[:space:]]|$)'

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

# pid + full command, command LAST for the same truncation reason as comm
# above. Needed for the DAEMON_ARGV_RE check below, not just for
# --verbose's breakdown, so this now runs unconditionally rather than only
# under --verbose. FAILS OPEN, deliberately: if this second `ps` call fails
# or returns nothing, CMD_OUT stays empty, every argv lookup below then
# finds no line for a given pid, and the loop falls through to the plain
# comm-only classification it always had -- i.e. a daemon process that
# cannot be positively identified via argv is counted rather than excluded.
# This is the fail-closed posture #613 asks for: never default to excluding
# something you can't positively identify as daemon infrastructure. Only
# the FIRST `ps` call (comm=, above) is fatal -- that one is the count
# itself; this one is strictly an additional exclusion refinement.
CMD_OUT=$(ps -Ao pid=,command= 2>/dev/null || true)

# Look up pid's full command from CMD_OUT, or "" if not found (ps failed,
# or the pid isn't there -- both handled by the fail-open posture above).
daemon_argv_for_pid() {
  awk -v p="$1" '$1==p { $1=""; sub(/^ /,""); print; exit }' <<<"$CMD_OUT"
}

count=0
matched_lines=()
excluded_pids=()
while IFS= read -r line; do
  [ -n "$line" ] || continue
  # agent-supervisor#678: BSD `ps`'s pid= column is fixed-width, padded
  # with LEADING spaces to the width of the widest pid currently on the
  # host (not to the width of any one line) -- so a shorter pid is
  # preceded by one or more spaces the moment any process elsewhere in
  # the table has more digits. `${line%% *}` looks for the LONGEST
  # trailing match of " *", and a leading space already satisfies that
  # pattern starting at position 0 -- so on a padded line the "pid" it
  # extracts is EMPTY and "comm" becomes the pid digits glued to the
  # real comm ("8454 claude", never exactly "claude"). That candidate
  # then fails the regex below and is silently dropped -- counted
  # nowhere, not even into excluded_pids, which is exactly the shape
  # #678 reported (16 counted + 2 excluded = 18 of 19; the 19th process
  # satisfied comm==claude but appeared in neither block). Confirmed
  # live: `ps -Ao pid=,comm=` on this host padded pid to 5 characters,
  # and every line whose pid needed fewer than 5 digits carried a
  # leading space that reproduced the empty-pid/glued-comm failure
  # under the old `%%`/`#` split.
  #
  # `read` fixes this because it splits on RUNS of IFS whitespace and
  # discards leading/trailing runs entirely, which is what a
  # fixed-width padded column actually needs -- and because only two
  # variables are named, the second one is assigned everything left on
  # the line after the first token, so a multi-word comm like "claude
  # bg-pty-host" still arrives whole rather than truncated to its first
  # word. Deterministic on pid digit-width, not a race: the same
  # process is dropped on every run for as long as some pid elsewhere in
  # the table stays wider, which matches #678's "stable across three
  # paired runs six seconds apart".
  read -r pid comm <<<"$line"
  # agent-estate#613 sub-decision 1: check argv for a daemon-owned shape
  # BEFORE the comm check, and independent of it -- a process is excluded
  # as daemon infrastructure because its OWN argv says so
  # (DAEMON_ARGV_RE), never because comm happened not to match
  # AGENT_COMMAND_RE. Narrowed to pids whose comm at least mentions
  # "claude" case-insensitively (bare name, daemon helper title, or a full
  # interpreter path all satisfy this) so this doesn't spend an awk lookup
  # on every unrelated process on the host.
  if [[ "$comm" == *[Cc][Ll][Aa][Uu][Dd][Ee]* ]]; then
    cmdline=$(daemon_argv_for_pid "$pid")
    if [ -n "$cmdline" ] && [[ "$cmdline" =~ $DAEMON_ARGV_RE ]]; then
      excluded_pids+=("$pid")
      continue
    fi
  fi
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
  # and the only one worth explaining. Reuses CMD_OUT (fetched
  # unconditionally above, for the daemon-argv check every run now does),
  # rather than a second `ps` call.
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
      cmdline=$(daemon_argv_for_pid "$pid")
      [ -n "$cmdline" ] || continue
      case "$cmdline" in
        *[Cc][Ll][Aa][Uu][Dd][Ee]*) : ;;
        *) continue ;;
      esac
      shown=$((shown + 1))
      # agent-estate#613 sub-decision 1: matched against argv CONTENT
      # (DAEMON_ARGV_RE's own alternatives), never a comm-shaped prefix --
      # a comm-prefix match like `"claude bg-pty-host"*` breaks the moment
      # comm resolves to a full interpreter path instead of the bare
      # daemon-set title (see the header comment), which is exactly the
      # accidental behavior this issue fixes.
      case "$cmdline" in
        *--bg-pty-host*)  reason="daemon helper (cc-daemon pty host, --bg-pty-host)" ;;
        *--bg-spare*)     reason="daemon helper (cc-daemon spare-claim, --bg-spare)" ;;
        *daemon\ run*)    reason="daemon helper (cc-daemon supervisor, daemon run)" ;;
        *--fork-session*) reason="daemon helper (cc-daemon forked session, --fork-session)" ;;
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
