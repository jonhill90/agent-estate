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
cp "$HERE/stubs/ps-lanes" "$D/bin/ps"
# columns: index|name|command|status-line|seconds-since-output|in-mode|input-box|pane-argv
# The 7th column is optional and models the input box (#141). Omitted, no box
# is rendered and classification is exactly what it was before #141 -- which
# is why every pre-existing row below is left untouched. `-` is an empty box,
# `dim:X` is an empty box showing Claude Code's dim placeholder, anything else
# is text typed into the box and never submitted. See stubs/tmux-lanes.
# The 8th column is also optional and models the argv of the pane's own first
# process (#154), which is what separates a service window from a dead lane.
# Omitted, it is `-zsh` -- what every real lane measured as -- so every
# pre-existing row is untouched by that too.
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
18|w-unsent|claude.exe|⏵⏵ bypass permissions on (shift+tab to cycle)|1|0|Read /brief.md and do exactly what it says.
19|w-unsent-ready-footer|claude.exe|⏵⏵ bypass permissions on (shift+tab to cycle) · ← 1 agent|1|0|Read /brief.md and do exactly what it says.
20|w-placeholder|claude.exe|⏵⏵ bypass permissions on (shift+tab to cycle) · ← 1 agent|1|0|dim:Try "write a test for <filepath>"
21|w-empty-box|claude.exe|⏵⏵ bypass permissions on (shift+tab to cycle) · ← 1 agent|1|0|-
22|w-typeahead|claude.exe|esc to interrupt 3s|1|0|typed while the turn runs
23|w-optionrow|claude.exe|❯ 1. Post the comment|1|0
24|w-optionrow-yes|claude.exe|❯ 1. Yes|1|0
25|w-text-blocked|claude.exe|Which environment should I target? Type the name, or press Esc to cancel|1|0
26|telegram-poller|bash|inbox-poll: waiting on Telegram|1|0||bash /repo/scripts/supervisor/inbox-poll.sh
27|ad102-renamed-lane|zsh|❯ |1|0
28|free-27|zsh|❯ |1|0
29|w-hand-run-poller|bash|inbox-poll: waiting on Telegram|1|0||-zsh
30|w-mentions-poller|zsh|running scripts/supervisor/inbox-poll.sh\n\n❯ |1|0
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
want "a lane on a selection menu is menu-blocked, not free" w-blocked menu-blocked "$out"
# Same #65 shape as w-mentions above, but for the blocked footer: a lane that
# merely printed the footer text earlier, with a normal last line, is free.
want "a lane that merely printed the footer is free"     w-mentions-blocked free "$out"

# The #124 finding: `Enter to select` was inferred from one example and only
# /theme emits it. These three status lines were captured off a real Claude
# Code pane (v2.1.220) driven through each prompt; all three read `free` before
# the marker set was widened, and (3) -- a bash tool-permission approval -- is
# the commonest blocking event a supervised lane actually hits. #159: all four
# real captures are menus (see lanes.sh's MENU_ENTER_RE comment), so all four
# read menu-blocked here, not text-blocked.
want "the folder-trust dialog is menu-blocked"           w-trust      menu-blocked "$out"
want "the /model menu is menu-blocked"                   w-model      menu-blocked "$out"
want "a bash tool-permission prompt is menu-blocked"     w-permission menu-blocked "$out"

# #159: no real free-text blocked prompt has been captured in this estate --
# every one of the four real captures above is a menu. This fixture models
# the shape the design calls for anyway (a dismissible prompt with neither an
# "Enter to <verb>" footer nor a numbered option row nearby): the routing
# decision (inbox-route.sh) still has to have a text-blocked case to deliver
# to, or "do not fix this by refusing everything" is untestable.
want "a dismissible free-text prompt is text-blocked, not menu-blocked" w-text-blocked text-blocked "$out"

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

# --- #141: a lane holding a brief nobody submitted -------------------------
# The incident shape, verbatim: the footer a real pane paints while the box
# holds typed text (the `← 1 agent` segment drops when you are composing), and
# a brief sitting in the box. Before #141 this read `unknown` -- withheld, but
# indistinguishable from a lane that was unclassified for four seconds while
# repainting, which is why it went unnoticed for 40 minutes.
want "a lane holding an unsent brief is unsent"          w-unsent unsent "$out"
# THE ONE THAT MATTERS. Same unsent brief, but the footer is a recognised
# ready shape, so READY_RE matches and every version of lanes.sh before #141
# would have called this lane free and dispatched over the brief. `free` now
# depends on the box being empty, not only on the footer.
want "a ready footer does not make an unsent lane free"  w-unsent-ready-footer unsent "$out"
# The false positive that a first cut of input-box.sh produced. An EMPTY box
# is not blank: it paints a rotating dim suggestion in the same row an unsent
# brief occupies, and in a plain-text capture the two are identical. Calling
# this `unsent` withholds every freshly started idle lane in the estate and
# --free collapses to nothing.
want "a dim placeholder is an EMPTY box, so free"        w-placeholder free "$out"
want "a genuinely empty box is free"                     w-empty-box free "$out"
# unsent is decided after busy: a lane typed into while its turn runs is busy,
# and busy is the more useful thing to say about it.
want "type-ahead during a live turn is busy, not unsent" w-typeahead busy "$out"

# --- #133: a confirmation dialog's option row ------------------------------
# `❯ 1. Post the comment` is verbatim what lane 6 displayed while blocked on an
# approval prompt, holding a completed-but-unposted review verdict. READY_RE's
# bare-`❯` half matches it, so as the last line it read `free` and a dispatch
# would have destroyed the verdict.
want "an option row is menu-blocked, not free"           w-optionrow menu-blocked "$out"
want "a short option row is menu-blocked, not free"      w-optionrow-yes menu-blocked "$out"

# --- #154: a service window is not a dead lane ----------------------------
# The live shape, verbatim: `inbox-poll.sh` deployed as `agent-dotfiles:11`
# per the deployment path its own header recommends. Its pane command is
# legitimately `bash` -- the script IS a bash script -- so every version of
# lanes.sh before #154 reported it `dead`, and the count line told whoever
# read the table to restart it. Restarting it replaces the poller with an
# agent and Jon's Telegram replies stop arriving, silently.
#
# What separates it from a dead lane is measured, not named: on 2026-08-11
# every lane's `pane_pid` process was the login shell `-zsh` (the agent runs
# as its child), while window 11's was `bash .../inbox-poll.sh` directly,
# because the window was created with the script as its command.
want "a supervisor service window is service, not dead"  telegram-poller service "$out"
# THE REGRESSION THAT MATTERS. Silencing the poller by silencing the check is
# worse than the bug: a lane that really has lost its agent must still say so.
want "a free-N lane that lost its agent is still dead"   free-27 dead "$out"
want "the generic shell lane is still dead"              w-dead  dead "$out"
# The #102 case the issue flags against the name-convention design: a lane
# that finished, was renamed, and then lost its agent. Under "anything not
# named free-N is not a lane" this window would have become invisible. The
# probe here keys on what the pane's process IS, not on what the window is
# called, so a renamed lane is classified exactly as it was before #154.
want "a renamed lane that lost its agent is still dead"  ad102-renamed-lane dead "$out"
# The exemption is deliberately narrow: it covers the deployment path
# `inbox-poll.sh`'s header recommends (the script as the window's command),
# not "any pane with a poller somewhere in it". A poller typed by hand into an
# interactive shell leaves the pane's own process as the login shell, and that
# pane still reads dead. Over-reporting dead is the safe direction; an
# exemption broad enough to cover any lane that ever shelled out is not.
want "a poller hand-run inside a login shell is still dead" w-hand-run-poller dead "$out"
# The #65 discipline, applied to this probe: classification must not be
# reachable from pane TEXT. A dead lane whose scrollback merely names the
# poller script is dead -- the service signal comes from ps, and the status
# probes still read exactly one line.
want "a dead lane that merely printed the script name is dead" w-mentions-poller dead "$out"

# #154: the count line is the hazard, not the table row -- `loop-tick.md` tells
# a reader to restart every lane it counts. Five dead lanes here (w-dead,
# ad102-renamed-lane, free-27, w-hand-run-poller, w-mentions-poller); the
# service window must not be one of them.
if grep -qE '5 lane\(s\) have no agent' <<<"$out"; then
  echo "  ok   the dead count line counts only genuinely dead lanes"; pass=$((pass+1));
else
  echo "  FAIL dead count line is not 5 in:"; sed 's/^/       /' <<<"$out"; fail=$((fail+1));
fi

# #159: seven blocked lanes -- the six menu/text captures above plus
# w-text-blocked. None of #154's new fixture rows (service/dead) are blocked,
# so this total is unaffected by that merge.
if grep -qE '7 lane\(s\) (is|are) blocked' <<<"$out"; then
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

# #141: withholding the lane was never the missing part -- the whitelist
# already did that. Being TOLD is. w-unsent and w-unsent-ready-footer are the
# two here.
if grep -qE '2 lane\(s\) hold an unsent prompt' <<<"$out"; then
  echo "  ok   the table prints a count line for unsent lanes"; pass=$((pass+1));
else
  echo "  FAIL no unsent count line in:"; sed 's/^/       /' <<<"$out"; fail=$((fail+1));
fi

# --free must never offer a lane that would swallow the dispatch.
free=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" bash "$LANES" --free 2>&1)
for bad in arch w-dead w-hung w-busy w-copilot w-minute-tick w-scrolled w-blocked \
           w-trust w-model w-permission w-idle-footer w-subagent-task w-subagent-wait \
           w-unsent w-unsent-ready-footer w-typeahead w-optionrow w-optionrow-yes \
           w-text-blocked \
           telegram-poller ad102-renamed-lane free-27 w-hand-run-poller w-mentions-poller; do
  bi=$(awk -F'|' -v n="$bad" '$2==n{print $1}' "$D/fixture")
  if grep -qx ".*:$bi" <<<"$free"; then echo "  FAIL --free offered $bad"; fail=$((fail+1));
  else echo "  ok   --free withholds $bad"; pass=$((pass+1)); fi
done

# And it must still offer a lane that IS a recognised ready shape -- the
# whitelist must not collapse into refusing everything.
for good in w-real-free w-mentions w-mentions-blocked w-placeholder w-empty-box; do
  gi=$(awk -F'|' -v n="$good" '$2==n{print $1}' "$D/fixture")
  if grep -qx ".*:$gi" <<<"$free"; then echo "  ok   --free offers $good"; pass=$((pass+1));
  else echo "  FAIL --free withheld $good"; fail=$((fail+1)); fi
done

# agent-dotfiles#142: --blocked is what inbound Telegram routing is built on
# -- the same session:index shape as --free, but the opposite predicate. #159
# adds a second tab-separated field naming the kind (menu/text), so a match
# here has to anchor on the lane field, not any substring of the line --
# `.*:$wi` would also match a longer index that happens to end the same way,
# and now has a `\tkind` suffix a bare `-x` exact match would never see.
blocked=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" bash "$LANES" --blocked 2>&1)
for want_blocked in w-blocked:menu w-trust:menu w-model:menu w-permission:menu w-text-blocked:text; do
  want_blocked_name="${want_blocked%%:*}"; want_blocked_kind="${want_blocked##*:}"
  wi=$(awk -F'|' -v n="$want_blocked_name" '$2==n{print $1}' "$D/fixture")
  if grep -qE "^[^	]*:${wi}	${want_blocked_kind}\$" <<<"$blocked"; then
    echo "  ok   --blocked offers $want_blocked_name as $want_blocked_kind"; pass=$((pass+1));
  else echo "  FAIL --blocked withheld $want_blocked_name as $want_blocked_kind ($blocked)"; fail=$((fail+1)); fi
done
for not_blocked in arch w-dead w-hung w-busy w-real-free w-mentions w-mentions-blocked \
                   telegram-poller free-27; do
  ni=$(awk -F'|' -v n="$not_blocked" '$2==n{print $1}' "$D/fixture")
  if grep -qE "^[^	]*:${ni}	" <<<"$blocked"; then echo "  FAIL --blocked offered $not_blocked"; fail=$((fail+1));
  else echo "  ok   --blocked withholds $not_blocked"; pass=$((pass+1)); fi
done

# #154: --json carries whatever the table carries, so the new state must show
# up there rather than being flattened into one of the old ones -- and the
# objects for pre-existing states must be untouched.
json=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" bash "$LANES" --json 2>&1)
if grep -q '{"window":26,"name":"telegram-poller","command":"bash","state":"service"}' <<<"$json"; then
  echo "  ok   --json reports the service window as service"; pass=$((pass+1));
else
  echo "  FAIL --json missing the service object in:"; sed 's/^/       /' <<<"$json"; fail=$((fail+1));
fi
if grep -q '{"window":4,"name":"w-dead","command":"zsh","state":"dead"}' <<<"$json"; then
  echo "  ok   --json is unchanged for a dead lane"; pass=$((pass+1));
else
  echo "  FAIL --json changed shape for a dead lane:"; sed 's/^/       /' <<<"$json"; fail=$((fail+1));
fi

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
