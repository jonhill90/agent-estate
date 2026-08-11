#!/bin/bash
# lanes.sh must tell four kinds of "idle" apart. All four were hit in one
# night, 2026-08-11, and each was misread as "nothing to do":
#   free / busy / hung / dead, plus unknown for anything unrecognised.
# #126 inverted free from a blacklist to a whitelist: unknown is now the
# default for a Claude Code lane too, not just for other harnesses -- only a
# recognised ready shape (READY_RE in lanes.sh) reads free.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LANES="$HERE/../../scripts/supervisor/lanes.sh"
pass=0; fail=0
want() { # want <name> <window-name> <expected-state> <output>
  if grep -qE "^[0-9]+ +$2 +[^ ]+ +$3$" <<<"$4"; then echo "  ok   $1"; pass=$((pass+1));
  else echo "  FAIL $1 — $2 not '$3' in:"; sed 's/^/       /' <<<"$4"; fail=$((fail+1)); fi
}

D=$(mktemp -d); mkdir -p "$D/bin"
cp "$HERE/stubs/tmux-lanes" "$D/bin/tmux"
# columns: index|name|command|status-line|seconds-since-output|in-mode
cat > "$D/fixture" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
2|w-busy|claude.exe|esc to interrupt 3s|1|0
3|w-hung|claude.exe|esc to interrupt 40m|900|0
4|w-dead|zsh|❯ |1|0
5|w-copilot|node|esc to interrupt|1|0
6|w-minute-tick|claude.exe|esc to interrupt 1m|30|0
7|w-scrolled|claude.exe|❯ ready|1|1
8|w-mentions|claude.exe|reviewing: grep -q 'esc to interrupt'\n\n❯ ready|900|0
9|w-blocked|claude.exe|Enter to select · ↑/↓ to navigate · Esc to cancel|1|0
10|w-mentions-blocked|claude.exe|reviewing: Enter to select · ↑/↓ to navigate · Esc to cancel\n\n❯ ready|900|0
11|w-trust|claude.exe|❯ 1. Yes, I trust this folder\n   2. No, exit\n Enter to confirm · Esc to cancel|1|0
12|w-model|claude.exe|◐ Medium effort ←/→ to adjust\n Enter to set as default · s to use this session only · Esc to cancel|1|0
13|w-permission|claude.exe|Do you want to proceed?\n❯ 1. Yes\n  2. No\n Esc to cancel · Tab to amend · ctrl+e to explain|1|0
14|w-idle-footer|claude.exe|⏸ manual mode on · ? for shortcuts · ← 2 agents|1|0
15|w-real-free|claude.exe|⏵⏵ bypass permissions on (shift+tab to cycle) · ← 1 agent|1|0
16|w-subagent-task|claude.exe|◯ general-purpose  Verifying tick-7.log with grep      42s|1|0
17|w-subagent-wait|claude.exe|✻ Waiting for 1 background agent to finish|1|0
FIX
out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" bash "$LANES" 2>&1)

echo "lanes.sh"
want "a turn whose output advances is busy"              w-busy    busy    "$out"
want "a turn frozen across samples is hung, not busy"    w-hung    hung    "$out"
want "a pane running a shell is dead, not idle"          w-dead    dead    "$out"
# A Claude-specific probe must not be applied to other harnesses: on
# 2026-08-11 a healthy idle Copilot pane was called `hung` because that
# string appeared in its scrollback.
want "a non-Claude harness is unknown, never guessed"    w-copilot unknown "$out"
# Window 1 is the supervisor. It is idle between ticks and therefore looks
# exactly like a free worker -- --free offered it twice on 2026-08-11 and a
# worker brief /clear'ed the loop both times. "Free" and "yours to take" are
# different questions.
want "the supervisor window is never a worker lane"      arch      supervisor "$out"
# Found in review of #65: Claude Code's elapsed footer is minute-granular past
# 60s, so a turn running 61-119s prints identical bytes for a whole minute. The
# original text-diff detector called that hung and would have paged a human
# about a working lane on every turn crossing a minute boundary.
want "a mid-minute turn is busy, not hung"               w-minute-tick busy "$out"
# Reproduced live against a real tmux server in review of #65: a pane in copy
# mode still captures its screen, so it reads free -- but keys sent there are
# eaten by the copy-mode key table and the dispatch vanishes.
want "a scrolled-up pane is not free"                    w-scrolled scrolled "$out"
# The live failure this whole probe got wrong: capture-pane -S -6 returns six
# scrollback lines PLUS the entire visible pane, so any lane that had merely
# PRINTED the phrase -- reviewing this file, reading loop-tick.md -- matched
# and was reported busy, then hung. Two real lanes were in that state. The
# probe must read only the status line.
want "a lane that merely printed the phrase is free"     w-mentions free    "$out"

# The #123 case: a lane sitting on a selection menu is not idle, it is
# waiting on a human, and must not be offered to the dispatcher.
want "a lane on a selection menu is blocked, not free"   w-blocked blocked "$out"
# Same #65 shape as w-mentions above, but for the blocked footer: a lane that
# merely printed the footer text earlier, with a normal last line, is free.
want "a lane that merely printed the footer is free"     w-mentions-blocked free "$out"

# The #124 finding: `Enter to select` was inferred from one example and only
# /theme emits it. These three status lines were captured off a real Claude
# Code pane (v2.1.220) driven through each prompt; all three read `free` before
# the marker set was widened, and (3) -- a bash tool-permission approval -- is
# the commonest blocking event a supervised lane actually hits.
want "the folder-trust dialog is blocked"                w-trust      blocked "$out"
want "the /model menu is blocked"                        w-model      blocked "$out"
want "a bash tool-permission prompt is blocked"          w-permission blocked "$out"

# #126: free inverted from a blacklist to a whitelist. A lane is dispatchable
# only when its last line is a recognised ready shape; everything else is
# unknown, not free. This fixture was originally captured as Claude Code's
# real idle footer and asserted free under the blacklist model -- it is kept
# on the SAME text to prove the inversion changed its classification, not
# just added a new row next to it.
want "a footer carrying a background agent count is NOT free, it is unknown" w-idle-footer unknown "$out"
# The real free shape, captured live off an idle lane on 2026-08-11: the same
# footer chrome, but the agent count is 1 (self only, nothing delegated).
want "a bare ready prompt with the real footer is free" w-real-free free "$out"
# The #126 live cases verbatim: a background subagent's task-list row, and
# Claude Code's "waiting for background agent" line. Neither contains
# `esc to interrupt`, so the old code offered both -- and both are the
# reason #126 exists: the main agent is idle but the lane's work is not
# done. This is the test that encodes the inversion: under the old
# blacklist, both of these read free.
want "a subagent task-list row is unknown, not free"     w-subagent-task unknown "$out"
want "a waiting-for-background-agent line is unknown, not free" w-subagent-wait unknown "$out"

if grep -qE '4 lane\(s\) (is|are) blocked' <<<"$out"; then
  echo "  ok   the table prints a count line for blocked lanes"; pass=$((pass+1));
else
  echo "  FAIL no blocked count line in:"; sed 's/^/       /' <<<"$out"; fail=$((fail+1));
fi

# #126: unknown becoming common under the whitelist must be loud, not a
# silent hole where lanes used to be. w-copilot, w-idle-footer,
# w-subagent-task, and w-subagent-wait are unknown here -- 4.
if grep -qE '4 lane\(s\) are unclassified' <<<"$out"; then
  echo "  ok   the table prints a count line for unknown lanes"; pass=$((pass+1));
else
  echo "  FAIL no unknown count line in:"; sed 's/^/       /' <<<"$out"; fail=$((fail+1));
fi

# --free must never offer a lane that would swallow the dispatch.
free=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" bash "$LANES" --free 2>&1)
for bad in arch w-dead w-hung w-busy w-copilot w-minute-tick w-scrolled w-blocked \
           w-trust w-model w-permission w-idle-footer w-subagent-task w-subagent-wait; do
  bi=$(awk -F'|' -v n="$bad" '$2==n{print $1}' "$D/fixture")
  if grep -qx ".*:$bi" <<<"$free"; then echo "  FAIL --free offered $bad"; fail=$((fail+1));
  else echo "  ok   --free withholds $bad"; pass=$((pass+1)); fi
done

# And it must still offer a lane that IS a recognised ready shape -- the
# whitelist must not collapse into refusing everything.
for good in w-real-free w-mentions w-mentions-blocked; do
  gi=$(awk -F'|' -v n="$good" '$2==n{print $1}' "$D/fixture")
  if grep -qx ".*:$gi" <<<"$free"; then echo "  ok   --free offers $good"; pass=$((pass+1));
  else echo "  FAIL --free withheld $good"; fail=$((fail+1)); fi
done

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
