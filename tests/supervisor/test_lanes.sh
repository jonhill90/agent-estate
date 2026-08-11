#!/bin/bash
# lanes.sh must tell four kinds of "idle" apart. All four were hit in one
# night, 2026-08-11, and each was misread as "nothing to do":
#   free / busy / hung / dead, plus unknown for harnesses with no probe.
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
# The other direction, and the reason the marker set is not widened further: a
# false positive makes a lane permanently undispatchable, which is worse than
# the bug. This is Claude Code's real idle footer, captured off the same pane.
want "the ordinary idle footer is still free"            w-idle-footer free    "$out"

if grep -qE '4 lane\(s\) (is|are) blocked' <<<"$out"; then
  echo "  ok   the table prints a count line for blocked lanes"; pass=$((pass+1));
else
  echo "  FAIL no blocked count line in:"; sed 's/^/       /' <<<"$out"; fail=$((fail+1));
fi

# --free must never offer a lane that would swallow the dispatch.
free=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" bash "$LANES" --free 2>&1)
for bad in arch w-dead w-hung w-busy w-copilot w-minute-tick w-scrolled w-blocked \
           w-trust w-model w-permission; do
  bi=$(awk -F'|' -v n="$bad" '$2==n{print $1}' "$D/fixture")
  if grep -qx ".*:$bi" <<<"$free"; then echo "  FAIL --free offered $bad"; fail=$((fail+1));
  else echo "  ok   --free withholds $bad"; pass=$((pass+1)); fi
done

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
