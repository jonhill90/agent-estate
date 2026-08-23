#!/usr/bin/env bash
# PreToolUse guard: refuse a branch switch inside the SHARED checkout.
#
# WHY. On 2026-08-17 the watchdog edited notify.sh in the shared checkout,
# committed to a branch, and pushed. Something then switched the shared checkout
# back to main and the edit vanished from the working tree mid-session. The
# watchdog had told Jon "telegram works" -- it had worked, and then silently
# stopped, because the file under it changed.
#
# Lanes already isolate: `git worktree list` shows 362 worktrees. The shared
# checkout at scripts/../ is the one place with no owner, so anything working
# there is racing everything else. This makes that a refusal instead of a
# surprise.
#
# It blocks the SWITCH, not the work: commit, push, add, status, log, diff all
# pass. If you need a different branch, make a worktree -- which is what every
# lane already does.
set -uo pipefail
SHARED="${SUPERVISOR_SHARED_CHECKOUT:-$HOME/source/repos/Personal/agent-supervisor}"

payload=$(cat 2>/dev/null || true)
cmd=$(printf '%s' "$payload" | python3 -c 'import json,sys
try: print((json.load(sys.stdin).get("tool_input") or {}).get("command",""))
except Exception: print("")' 2>/dev/null)
[ -z "$cmd" ] && exit 0

# agent-supervisor#511: grep-matching the raw command text false-positives when
# `git checkout`/`git switch` appears inside a QUOTED STRING argument (a commit
# message, a PR body, an echoed string) rather than a real shell invocation. A
# full tokenizer is out of scope; truncating at the first unescaped quote or
# backtick is enough -- everything a real git verb needs appears before any
# quoting starts, and prose inside a string argument never gets there.
cmd_prefix=$(printf '%s' "$cmd" | python3 -c 'import sys
s = sys.stdin.read()
i = 0
n = len(s)
while i < n:
    c = s[i]
    if c == "\\":
        i += 2
        continue
    if c in ("\x27", "\"", "`"):
        sys.stdout.write(s[:i])
        break
    i += 1
else:
    sys.stdout.write(s)
' 2>/dev/null)

# Only care about branch-changing git verbs, checked against the unquoted prefix.
printf '%s' "$cmd_prefix" | grep -qE 'git +(checkout|switch)( |$)' || exit 0
# `git checkout -- <path>` and `checkout <file>` restore files; they do not move HEAD.
printf '%s' "$cmd_prefix" | grep -qE 'git +checkout +--( |$)' && exit 0

# Is this command aimed at the shared checkout? Either it cd's there explicitly,
# or the session is already there.
here=$(pwd -P 2>/dev/null)
target="$here"
if printf '%s' "$cmd" | grep -qE 'cd +[^&;|]*agent-supervisor'; then target="$SHARED"; fi
[ "$target" = "$(cd "$SHARED" 2>/dev/null && pwd -P)" ] || exit 0

cat >&2 <<MSG
BLOCKED: branch switch inside the SHARED checkout ($SHARED).

  $cmd

Lanes isolate with worktrees (362 exist). The shared checkout has no owner, so
switching it yanks the working tree out from under anything else using it --
that is how a verified notify.sh fix silently reverted on 2026-08-17.

Use a worktree instead:
  git worktree add /tmp/<name> -b <branch>
MSG
exit 2
