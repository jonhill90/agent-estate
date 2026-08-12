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

# Rewrite one `NAME=<value>` assignment in a copy of a harness adapter, used
# by the mutation checks below to break a matcher on purpose.
#
# agent-dotfiles#250: this exists because the mutation checks used to find
# their target by pasting the adapter's CURRENT regex in as a literal string
# and asserting it was present. That couples every mutation check to the exact
# bytes of the thing it mutates: #250's one-character widening of codex's
# ready matcher made the assert fail, and the failure landed in the check's
# SETUP, not in an assertion -- so the suite went from 116 passed / 0 failed
# to 112 passed / 1 failed. Four checks stopped running and the only sign was
# a smaller total. Anchoring on the variable NAME instead means a matcher can
# be changed without silently deleting the checks that prove it matters. The
# uniqueness assert keeps the anchor honest: an adapter that grew a second
# assignment to the same name fails loudly rather than mutating the wrong one.
mutate_assign() { # mutate_assign <file> <var-name> <new-value-with-quoting>
  python3 - "$1" "$2" "$3" <<'PY'
import re, sys
path, var, value = sys.argv[1], sys.argv[2], sys.argv[3]
text = open(path).read()
pattern = re.compile(r"^%s=.*$" % re.escape(var), re.M)
found = pattern.findall(text)
assert len(found) == 1, \
    "%s: expected exactly one %s= assignment, found %d -- file shape changed" % (
        path, var, len(found))
open(path, "w").write(pattern.sub(lambda m: "%s=%s" % (var, value), text, count=1))
PY
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
31|w-unrecognized-blocked|claude.exe|Confirm the target environment · Esc to cancel|1|0
32|w-codex-ready|codex|  gpt-5.5 medium · /repo/path|1|0
33|w-codex-busy|codex|• Working (9s • esc to interrupt)\n\n› Improve documentation in @filename\n\n  gpt-5.5 medium · /repo/path|1|0
34|w-codex-trust|codex|› 1. Yes, continue\n  2. No, quit\n\n  Press enter to continue|1|0
35|w-copilot-ready|node| ← open sidebar · / commands · ? help · tab next tab   Claude Sonnet 5|1|0
36|w-copilot-busy|node| ◎ Working esc interrupt                              Claude Sonnet 5|1|0
37|w-shell-busy|claude.exe|⏵⏵ bypass permissions on · 1 shell · esc to interrupt · ← 1 agent · ↓ to manage|1|0
38|w-shell-idle|claude.exe|⏵⏵ bypass permissions on · 1 shell · ← 1 agent · ↓ to manage|1|0
39|w-shell-idle-hung|claude.exe|⏵⏵ bypass permissions on · 1 shell · ← 1 agent · ↓ to manage|900|0
40|w-shell-tasks|claude.exe|⏵⏵ bypass permissions on · 1 shell · ctrl+t to hide tasks · ← 1 agent · ↓ to manage|1|0
41|w-shells-plural|claude.exe|⏵⏵ bypass permissions on · 2 shells · ← 1 agent · ↓ to manage|1|0
42|w-codex-ready-tilde|codex|  gpt-5.5 medium · ~/source/repos/Personal/agent-dotfiles|1|0
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
# to, or "do not fix this by refusing everything" is untestable. #164:
# text-blocked now requires its OWN positive marker (TEXT_PROMPT_RE) rather
# than being reached by absence of menu evidence, so this fixture's text
# ("Type the name") was kept as-is because it already carries that marker --
# it still reads text-blocked, unchanged, proving the inversion is a pure
# narrowing of an unrelated case, not a rewrite of this one.
want "a dismissible free-text prompt is text-blocked, not menu-blocked" w-text-blocked text-blocked "$out"

# #164, the regression this issue is about: independent review of #161 found
# that ANY unrecognised blocked shape -- no "Enter to <verb>" footer, no
# option row, and (pre-fix) no positive text marker either -- fell through
# to text-blocked by DEFAULT, so an unrecognised menu got free text typed
# into it. This fixture has neither menu evidence nor TEXT_PROMPT_RE's "Type
# the ..." phrase; before the #164 fix it read text-blocked here (the exact
# bug), and now it must read menu-blocked -- refuse-and-relay is the safe
# default for anything this probe cannot positively place, same posture #126
# already established for `free`.
want "an unrecognised blocked shape defaults to menu-blocked, not text-blocked" w-unrecognized-blocked menu-blocked "$out"

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

# --- agent-dotfiles#201: a harness adapter, not a Claude-only whitelist ---
# All five rows below are verbatim real captures (harness/codex.sh,
# harness/copilot.sh) off a real codex (v0.147.0) and copilot (v1.0.79) pane
# in a throwaway tmux session on 2026-08-12, never the pre-#201 fixture's
# invented `w-copilot|node` row (which models an UNRECOGNISED harness, not
# copilot -- kept as-is above since that is still a real and separate case).
want "a real codex idle footer is free"                  w-codex-ready free "$out"
want "a real codex mid-turn pane is busy, not free"       w-codex-busy busy "$out"
want "codex's own startup trust menu is menu-blocked"     w-codex-trust menu-blocked "$out"
want "a real copilot idle footer is free"                 w-copilot-ready free "$out"
want "a real copilot mid-turn pane is busy, not free"     w-copilot-busy busy "$out"

# --- agent-dotfiles#250: codex abbreviates a cwd under $HOME ---------------
# The row above carries `/repo/path` because #201's throwaway lab ran out of
# a private temp directory. Every lane in this estate runs out of $HOME, and
# there codex prints the cwd tilde-abbreviated instead -- so the absolute-path
# anchor #201 shipped matched the lab and never the real thing. This row is
# the live `free-codex` lane's last non-empty line on 2026-08-12, verbatim,
# and it read `unknown` until harness/codex.sh's ready matcher accepted `~`.
# Unknown is the correct default for a shape nothing recognises (#126), which
# is why this never showed up as a broken lane: it showed up as no lane at
# all, withheld from --free forever with no error anywhere.
want "a codex footer with a tilde-abbreviated cwd is free (#250)" w-codex-ready-tilde free "$out"

# --- agent-dotfiles#207: a background shell changes Claude's footer shape --
# All five rows below are verbatim real captures off a real Claude Code pane
# (v2.1.220) in a throwaway tmux server, private TMUX_TMPDIR, 2026-08-12 --
# never the live agent-dotfiles session. w-shell-idle and w-shell-tasks match
# the issue's own two live captures byte-for-byte (win 3 and win 10). Mid-turn
# WITH a background shell registered, the footer carries both `esc to
# interrupt` and `↓ to manage` -- already caught by the pre-existing busy
# regex, kept here as w-shell-busy so the fix is proven not to have narrowed
# that case. Once the turn ends but the shell is still running, `esc to
# interrupt` drops and only `↓ to manage` remains -- w-shell-idle -- which read
# `unknown` before this fix, and is why `#203`/`#184` sat withheld from --free
# indefinitely (the lane leak #207 describes). Regardless of which harness
# chrome painted it, this shape is not idle -- a background shell is still
# doing the lane's work -- so it reads busy, never free.
want "a mid-turn pane with a background shell is busy"                 w-shell-busy      busy   "$out"
want "an idle pane with a registered background shell is busy, not unknown (#207)" w-shell-idle busy "$out"
want "the ctrl+t-to-hide-tasks variant is busy too (#207)"             w-shell-tasks     busy   "$out"
want "the plural shells variant is busy too (#207)"                    w-shells-plural   busy   "$out"
# THE ONE THAT MATTERS (#207): before this fix, `hung` was unreachable for
# this shape -- it never had a busy branch to compute liveness inside. Same
# pane text as w-shell-idle, frozen for 900s (LANES_HUNG_AFTER defaults to
# 180) instead of 1s.
want "an idle-with-shell pane frozen past HUNG_AFTER is hung, not unknown (#207)" w-shell-idle-hung hung "$out"

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

# #159: nine blocked lanes -- the six menu/text captures, w-text-blocked,
# #164's w-unrecognized-blocked (menu-blocked by the new default), and
# #201's w-codex-trust (codex's own startup menu, verified against a real
# pane). None of #154's fixture rows (service/dead) are blocked, so this
# total is unaffected by that merge.
if grep -qE '9 lane\(s\) (is|are) blocked' <<<"$out"; then
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
           w-text-blocked w-unrecognized-blocked w-codex-busy w-codex-trust w-copilot-busy \
           w-shell-busy w-shell-idle w-shell-idle-hung w-shell-tasks w-shells-plural \
           telegram-poller ad102-renamed-lane free-27 w-hand-run-poller w-mentions-poller; do
  bi=$(awk -F'|' -v n="$bad" '$2==n{print $1}' "$D/fixture")
  if grep -qE "^[^	]*:${bi}	" <<<"$free"; then echo "  FAIL --free offered $bad"; fail=$((fail+1));
  else echo "  ok   --free withholds $bad"; pass=$((pass+1)); fi
done

# And it must still offer a lane that IS a recognised ready shape -- the
# whitelist must not collapse into refusing everything.
for good in w-real-free w-mentions w-mentions-blocked w-placeholder w-empty-box \
            w-codex-ready w-codex-ready-tilde w-copilot-ready; do
  gi=$(awk -F'|' -v n="$good" '$2==n{print $1}' "$D/fixture")
  if grep -qE "^[^	]*:${gi}	" <<<"$free"; then echo "  ok   --free offers $good"; pass=$((pass+1));
  else echo "  FAIL --free withheld $good"; fail=$((fail+1)); fi
done

# agent-dotfiles#241. --free's shape: TWO tab-separated columns, the lane
# (session:index, what the ledger keys on) and the tmux target
# (session:@id, what send-keys must be given). Anchored on the lane field for
# the same reason the --blocked loop below is: a bare substring match cannot
# tell the two columns apart, and this suite's whole job here is that they
# ARE different strings.
#
# The stub synthesises window ids as 100 + index precisely so an
# index-addressed call and an id-addressed one cannot be confused for each
# other -- see stubs/tmux-lanes.
for good in w-real-free w-codex-ready w-copilot-ready; do
  gi=$(awk -F'|' -v n="$good" '$2==n{print $1}' "$D/fixture")
  if grep -qE "^[^	]*:${gi}	[^	]*:@$((100+gi))\$" <<<"$free"; then
    echo "  ok   --free pairs $good's lane with its window-id target (session:$gi -> session:@$((100+gi)))"; pass=$((pass+1));
  else
    echo "  FAIL --free did not pair $good's lane with a window-id target"; sed 's/^/       /' <<<"$free"; fail=$((fail+1));
  fi
done
# ...and NO line may be target-less. An empty second column would be handed
# to `send-keys -t session:` by dispatch.sh, which does not error -- it hits
# the ACTIVE window, i.e. the supervisor. That is the incident loop-tick.md
# records; the shape of this output is the first place it can be prevented.
if [ -n "$free" ] && grep -qvE "^[^	]*:[0-9]+	[^	]*:@[0-9]+\$" <<<"$free"; then
  echo "  FAIL --free emitted a row that is not lane<TAB>window-id target:"; sed 's/^/       /' <<<"$free"; fail=$((fail+1));
else
  echo "  ok   every --free row is lane<TAB>window-id target, none empty"; pass=$((pass+1));
fi

# agent-dotfiles#142: --blocked is what inbound Telegram routing is built on
# -- the same session:index shape as --free, but the opposite predicate. #159
# adds a second tab-separated field naming the kind (menu/text), so a match
# here has to anchor on the lane field, not any substring of the line --
# `.*:$wi` would also match a longer index that happens to end the same way,
# and now has a `\tkind` suffix a bare `-x` exact match would never see.
blocked=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" bash "$LANES" --blocked 2>&1)
for want_blocked in w-blocked:menu w-trust:menu w-model:menu w-permission:menu w-text-blocked:text \
                    w-unrecognized-blocked:menu w-codex-trust:menu; do
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
if grep -q '{"window":26,"window_id":"@126","name":"telegram-poller","command":"bash","state":"service"}' <<<"$json"; then
  echo "  ok   --json reports the service window as service"; pass=$((pass+1));
else
  echo "  FAIL --json missing the service object in:"; sed 's/^/       /' <<<"$json"; fail=$((fail+1));
fi
if grep -q '{"window":4,"window_id":"@104","name":"w-dead","command":"zsh","state":"dead"}' <<<"$json"; then
  echo "  ok   --json is unchanged for a dead lane, apart from the added window_id"; pass=$((pass+1));
else
  echo "  FAIL --json changed shape for a dead lane:"; sed 's/^/       /' <<<"$json"; fail=$((fail+1));
fi
# agent-dotfiles#241: `window` stays the INDEX and stays a JSON number, so no
# existing consumer of this renderer (supervisor_view.py's LanesSource passes
# the objects through untouched) has to change; `window_id` is added beside
# it. Both, or a consumer cannot do the one job the split exists for.
if grep -qE '\{"window":[0-9]+,"window_id":"@[0-9]+"' <<<"$json"; then
  echo "  ok   --json carries the index as a number AND the window id beside it"; pass=$((pass+1));
else
  echo "  FAIL --json is missing one of the two identities:"; sed 's/^/       /' <<<"$json"; fail=$((fail+1));
fi
if python3 -c 'import json,sys; rows=json.load(sys.stdin); sys.exit(0 if rows and all(isinstance(r["window"], int) and r["window_id"].startswith("@") for r in rows) else 1)' <<<"$json"; then
  echo "  ok   --json still parses, with an int window and an @-prefixed window_id on every row"; pass=$((pass+1));
else
  echo "  FAIL --json does not parse or has a malformed identity:"; sed 's/^/       /' <<<"$json"; fail=$((fail+1));
fi

# --- agent-dotfiles#164 mutation check: prove the default can go wrong -----
# Revert lanes.sh's menu/text fallback to the pre-#164 shape (absence of
# menu evidence -> text-blocked) and confirm w-unrecognized-blocked flips to
# text-blocked -- the exact hazard this issue is about, reproduced on
# demand so the assertion above is provably checking something.
#
# The mutant has to live NEXT TO the real lanes.sh, not in $D or $HERE:
# lanes.sh resolves input-box.sh relative to its own path
# (LANES_HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" ...)"), so a mutated
# copy dropped anywhere else fails to source its sibling and the test would
# be asserting on a script that never ran.
LANES_DIR="$(cd "$(dirname "$LANES")" && pwd)"
MUTANT="$LANES_DIR/.lanes-mutant-164.sh"
trap 'rm -f "$MUTANT"' EXIT
patch_rc=0
python3 - "$LANES" "$MUTANT" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''      pane_tail=$(tail -n "${H_MENU_TAIL[$hidx]}" <<<"$pane_lines")
      if { [ -n "${H_MENU_ENTER_RE[$hidx]}" ] && grep -qE "${H_MENU_ENTER_RE[$hidx]}" <<<"$pane"; } \\
        || { [ -n "${H_OPTION_ROW_RE[$hidx]}" ] && grep -qE "${H_OPTION_ROW_RE[$hidx]}" <<<"$pane_tail"; }; then
        state=menu-blocked
      elif [ -n "${H_TEXT_PROMPT_RE[$hidx]}" ] && grep -qE "${H_TEXT_PROMPT_RE[$hidx]}" <<<"$pane"; then
        state=text-blocked
      else
        state=menu-blocked
      fi'''
assert marker in text, "menu/text fallback block not found -- lanes.sh shape changed"
assert text.count(marker) == 1, "menu/text fallback block not unique -- lanes.sh shape changed"
replacement = '''      pane_tail=$(tail -n "${H_MENU_TAIL[$hidx]}" <<<"$pane_lines")
      if { [ -n "${H_MENU_ENTER_RE[$hidx]}" ] && grep -qE "${H_MENU_ENTER_RE[$hidx]}" <<<"$pane"; } \\
        || { [ -n "${H_OPTION_ROW_RE[$hidx]}" ] && grep -qE "${H_OPTION_ROW_RE[$hidx]}" <<<"$pane_tail"; }; then
        state=menu-blocked
      else
        state=text-blocked
      fi'''
open(dst, "w").write(text.replace(marker, replacement, 1))
PY
if [ "$patch_rc" -ne 0 ]; then
  echo "  FAIL setup: could not patch a pre-#164 copy of lanes.sh (exit $patch_rc)"; fail=$((fail+1));
else
  echo "  ok   setup: patched a pre-#164 copy of lanes.sh"; pass=$((pass+1));
  chmod +x "$MUTANT"
  mutant_out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" bash "$MUTANT" 2>&1)
  if grep -qE "^[0-9]+ +w-unrecognized-blocked +[^ ]+ +text-blocked$" <<<"$mutant_out"; then
    echo "  ok   mutation confirmed: the pre-#164 fallback types free text into an unrecognised blocked shape (the assertion above would be red)"; pass=$((pass+1));
  else
    echo "  FAIL mutation confirmed: pre-#164 fallback did not reproduce text-blocked for w-unrecognized-blocked:"; sed 's/^/       /' <<<"$mutant_out"; fail=$((fail+1));
  fi
fi
rm -f "$MUTANT"
trap - EXIT

# --- agent-dotfiles#201 mutation check: the seam does not leak -----------
# Break ONLY codex's ready matcher and confirm ONLY the codex lane goes
# unknown while claude, and copilot, stay correctly classified. If breaking
# one adapter ever moves another harness's lane, the "adapter, not a
# regex" contract has failed -- #201's acceptance bar is exactly this.
HDIR="$(cd "$(dirname "$LANES")/harness" && pwd)"
MUTHDIR=$(mktemp -d)
cp "$HDIR"/*.sh "$MUTHDIR"/
mutation_rc=0
mutate_assign "$MUTHDIR/codex.sh" HARNESS_READY_RE "'NEVER_MATCHES_ANYTHING_XYZZY'" || mutation_rc=$?
if [ "$mutation_rc" -ne 0 ]; then
  echo "  FAIL setup: could not mutate a copy of harness/codex.sh (exit $mutation_rc)"; fail=$((fail+1));
else
  echo "  ok   setup: mutated a copy of harness/codex.sh's ready matcher"; pass=$((pass+1));
  seam_out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" LANES_HARNESS_DIR="$MUTHDIR" bash "$LANES" 2>&1)
  if grep -qE "^[0-9]+ +w-codex-ready +[^ ]+ +unknown$" <<<"$seam_out"; then
    echo "  ok   breaking codex's ready matcher moves the codex lane to unknown"; pass=$((pass+1));
  else
    echo "  FAIL breaking codex's ready matcher did not move w-codex-ready to unknown:"; sed 's/^/       /' <<<"$seam_out"; fail=$((fail+1));
  fi
  if grep -qE "^[0-9]+ +w-real-free +[^ ]+ +free$" <<<"$seam_out"; then
    echo "  ok   the seam does not leak: claude's lane is still free"; pass=$((pass+1));
  else
    echo "  FAIL the seam leaked: breaking codex's adapter moved a claude lane too:"; sed 's/^/       /' <<<"$seam_out"; fail=$((fail+1));
  fi
  if grep -qE "^[0-9]+ +w-copilot-ready +[^ ]+ +free$" <<<"$seam_out"; then
    echo "  ok   the seam does not leak: copilot's lane is still free"; pass=$((pass+1));
  else
    echo "  FAIL the seam leaked: breaking codex's adapter moved a copilot lane too:"; sed 's/^/       /' <<<"$seam_out"; fail=$((fail+1));
  fi
fi
rm -rf "$MUTHDIR"

# --- agent-dotfiles#250 mutation check: revert to the absolute-path anchor -
# The #201 check above proves an unmatchable matcher moves the codex lane. It
# does NOT prove the tilde case needs anything, because a NEVER_MATCHES value
# breaks both codex rows at once. This one reverts codex's ready matcher to
# the exact pre-#250 value -- absolute paths only -- and asserts the two rows
# part company: the tilde-abbreviated footer goes `unknown` while the
# absolute-path footer stays `free`. Without the fix under test this check
# cannot pass, so the assertion above is provably checking something.
#
# The direction is the point (#124, #126): the pre-#250 matcher must send the
# tilde lane to `unknown`, never to `free`. The one-way ratchet is that no
# change may make a lane AVAILABLE by accident; withholding a lane that is
# genuinely idle is the safe failure, and is exactly the (costly, silent)
# failure #250 fixes.
MUTHDIR3=$(mktemp -d)
cp "$HDIR"/*.sh "$MUTHDIR3"/
mutation_rc=0
mutate_assign "$MUTHDIR3/codex.sh" HARNESS_READY_RE \
  "'^[[:space:]]*[^·[:space:]][^·]*·[[:space:]]*/'" || mutation_rc=$?
if [ "$mutation_rc" -ne 0 ]; then
  echo "  FAIL setup: could not revert a copy of harness/codex.sh to the pre-#250 anchor (exit $mutation_rc)"; fail=$((fail+1));
else
  echo "  ok   setup: reverted a copy of harness/codex.sh to the pre-#250 absolute-path anchor"; pass=$((pass+1));
  tilde_out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" LANES_HARNESS_DIR="$MUTHDIR3" bash "$LANES" 2>&1)
  if grep -qE "^[0-9]+ +w-codex-ready-tilde +[^ ]+ +unknown$" <<<"$tilde_out"; then
    echo "  ok   mutation confirmed: the pre-#250 anchor cannot see a tilde-abbreviated cwd"; pass=$((pass+1));
  else
    echo "  FAIL mutation confirmed: the pre-#250 anchor did not move w-codex-ready-tilde to unknown:"; sed 's/^/       /' <<<"$tilde_out"; fail=$((fail+1));
  fi
  if grep -qE "^[0-9]+ +w-codex-ready-tilde +[^ ]+ +free$" <<<"$tilde_out"; then
    echo "  FAIL the one-way ratchet broke: w-codex-ready-tilde became free under the reverted anchor"; fail=$((fail+1));
  else
    echo "  ok   the one-way ratchet holds: w-codex-ready-tilde never became free"; pass=$((pass+1));
  fi
  if grep -qE "^[0-9]+ +w-codex-ready +[^ ]+ +free$" <<<"$tilde_out"; then
    echo "  ok   the widening is narrow: the absolute-path footer read free before #250 and still does"; pass=$((pass+1));
  else
    echo "  FAIL the absolute-path codex footer stopped reading free under the pre-#250 anchor:"; sed 's/^/       /' <<<"$tilde_out"; fail=$((fail+1));
  fi
fi
rm -rf "$MUTHDIR3"

# --- agent-dotfiles#207 mutation check: break the new busy matcher --------
# Drop the `↓ to manage` alternative harness/claude.sh#207 added and confirm
# w-shell-idle falls to `unknown` -- and explicitly, never to `free` -- and
# that w-shell-idle-hung, which depends on the same branch to compute
# liveness at all, goes with it. This is the shape the issue's own live
# lanes were stuck in before the fix.
MUTHDIR2=$(mktemp -d)
cp "$HDIR"/*.sh "$MUTHDIR2"/
mutation_rc=0
mutate_assign "$MUTHDIR2/claude.sh" HARNESS_BUSY_RE "'esc to interrupt'" || mutation_rc=$?
if [ "$mutation_rc" -ne 0 ]; then
  echo "  FAIL setup: could not mutate a copy of harness/claude.sh (exit $mutation_rc)"; fail=$((fail+1));
else
  echo "  ok   setup: mutated a copy of harness/claude.sh's busy matcher"; pass=$((pass+1));
  shell_out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" LANES_HARNESS_DIR="$MUTHDIR2" bash "$LANES" 2>&1)
  if grep -qE "^[0-9]+ +w-shell-idle +[^ ]+ +unknown$" <<<"$shell_out"; then
    echo "  ok   breaking the #207 busy matcher moves w-shell-idle to unknown"; pass=$((pass+1));
  else
    echo "  FAIL breaking the #207 busy matcher did not move w-shell-idle to unknown:"; sed 's/^/       /' <<<"$shell_out"; fail=$((fail+1));
  fi
  if grep -qE "^[0-9]+ +w-shell-idle +[^ ]+ +free$" <<<"$shell_out"; then
    echo "  FAIL the one-way ratchet broke: w-shell-idle became free"; fail=$((fail+1));
  else
    echo "  ok   the one-way ratchet holds: w-shell-idle never became free"; pass=$((pass+1));
  fi
  if grep -qE "^[0-9]+ +w-shell-idle-hung +[^ ]+ +unknown$" <<<"$shell_out"; then
    echo "  ok   breaking the busy matcher also makes hung unreachable for this shape, confirming hung depended on it"; pass=$((pass+1));
  else
    echo "  FAIL w-shell-idle-hung did not also go unknown:"; sed 's/^/       /' <<<"$shell_out"; fail=$((fail+1));
  fi
  # And the seam still doesn't leak: breaking claude's matcher must not move
  # codex's or copilot's already-passing rows.
  if grep -qE "^[0-9]+ +w-codex-busy +[^ ]+ +busy$" <<<"$shell_out"; then
    echo "  ok   the seam does not leak: codex's busy lane is untouched"; pass=$((pass+1));
  else
    echo "  FAIL the seam leaked: breaking claude's busy matcher moved a codex lane too:"; sed 's/^/       /' <<<"$shell_out"; fail=$((fail+1));
  fi
fi
rm -rf "$MUTHDIR2"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
