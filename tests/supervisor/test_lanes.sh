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
# columns: index|name|command|status-line|seconds-since-output|in-mode|input-box|pane-argv|pane-current-path
# The 7th column is optional and models the input box (#141). Omitted, no box
# is rendered and classification is exactly what it was before #141 -- which
# is why every pre-existing row below is left untouched. `-` is an empty box,
# `dim:X` is an empty box showing Claude Code's dim placeholder, anything else
# is text typed into the box and never submitted. See stubs/tmux-lanes.
# The 8th column is also optional and models the argv of the pane's own first
# process (#154), which is what separates a service window from a dead lane.
# Omitted, it is `-zsh` -- what every real lane measured as -- so every
# pre-existing row is untouched by that too.
# The 9th column is also optional and models whether the pane's HARNESS
# process (claude.exe, codex, ...) has a live child of its OWN (#83, #92) --
# a grandchild of the pane -- which is what `pane_has_live_children` in
# lanes.sh now walks the process tree for. `child` means a live grandchild
# exists (a background test suite, a Monitor'd task); omitted (every
# pre-existing row) means it does not, so every row that predates #83 is
# classified exactly as it was before this column existed. PR #92's own
# REQUEST CHANGES review found that a pane's login-shell process ALWAYS has
# the harness itself as a live direct child for as long as the harness has
# not exited -- busy, waiting, or genuinely wedged alike -- so stubs/ps-lanes
# models three tiers (pane -> harness -> work), not two, and only a row
# marked `child` here gets the third tier.
# The 10th column is optional and models `#{pane_current_path}` (#88).
# Omitted, it defaults to this harness's existing temp directory; a
# nonexistent path means the lane cannot start any turn and must not be
# offered.
MISSING_CWD="$D/missing-cwd"
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
45|w-hung-live-child|claude.exe|esc to interrupt 40m|900|0||-zsh|child
40|w-shell-tasks|claude.exe|⏵⏵ bypass permissions on · 1 shell · ctrl+t to hide tasks · ← 1 agent · ↓ to manage|1|0
41|w-shells-plural|claude.exe|⏵⏵ bypass permissions on · 2 shells · ← 1 agent · ↓ to manage|1|0
42|w-codex-ready-tilde|codex|  gpt-5.5 medium · ~/source/repos/Personal/agent-dotfiles|1|0
43|inbox-poll|zsh|poller process exiting\n\n❯ |1|0||NONE
44|inbox-poll|zsh|❯ |1|0
46|w-shell-busy-live-child|claude.exe|⏵⏵ bypass permissions on · 1 shell · ← 1 agent · ↓ to manage|900|0||-zsh|child
47|w-codex-hung-live-child|codex|• Working (9s • esc to interrupt)\n\n› Improve documentation in @filename\n\n  gpt-5.5 medium · /repo/path|900|0||-zsh|child
48|w-codex-hung|codex|• Working (9s • esc to interrupt)\n\n› Improve documentation in @filename\n\n  gpt-5.5 medium · /repo/path|900|0
50|w-firstrun|claude.exe|Paste code here if prompted >|1300|0
51|w-firstrun-fresh|claude.exe|Paste code here if prompted >|5|0
FIX
printf '49|w-missing-cwd|codex|  gpt-5.5 medium · /repo/path|1|0||||%s\n' "$MISSING_CWD" >> "$D/fixture"
out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" bash "$LANES" 2>&1)

echo "lanes.sh"
want "a turn whose output advances is busy"              w-busy    busy    "$out"
want "a turn frozen across samples is hung, not busy"    w-hung    hung    "$out"
# #83, the regression this issue is about: a pane whose status line has been
# frozen past HUNG_AFTER used to read `hung` unconditionally. w-hung above
# (no live child in the fixture) proves that reading still holds when the
# pane genuinely has stopped -- the case #83's own brief calls out as the
# regression risk: a fix that treats every idle pane as busy would make
# `hung` unreachable. This row is the SAME frozen status line, only with a
# live child process (#83's own measured shape: a lane idle at the prompt
# with a background test suite still running) -- it must read busy, not
# hung.
want "a frozen pane with a live child process is busy, not hung (#83)" w-hung-live-child busy "$out"
# PR #92's REQUEST CHANGES review: the harness process itself is ALWAYS a
# live direct child of the pane while alive, busy or wedged alike, so a
# check that stops at "does the pane have any live child" can never move a
# lane OUT of hung -- it would call every alive harness busy. w-shell-busy
# below carries the #207 footer chrome claiming a registered background
# shell (`↓ to manage`, `1 shell`) with NO live grandchild in the process
# tree -- the shell it claims has already exited without being
# deregistered -- and must still read hung: the kernel's process tree, not
# the harness's own footer text, is the authority. w-shell-busy-live-child
# is the SAME footer with a genuine grandchild and must read busy.
want "a registered background shell with no live grandchild is hung, not busy (#83, text is not evidence)" w-shell-idle-hung hung "$out"
want "a registered background shell with a live grandchild is busy, matching the real work (#83)" w-shell-busy-live-child busy "$out"
# Harness-agnostic: `pane_has_live_children` reads the kernel process tree,
# not any harness's own footer chrome, so the same distinction must hold for
# codex (HARNESS_BUSY_TAIL=4, a different busy shape entirely) and not just
# for claude.exe.
want "a frozen codex pane with a live grandchild is busy, not hung (#83, harness-agnostic)" w-codex-hung-live-child busy "$out"
want "a genuinely wedged codex pane is still hung, not busy (#83, harness-agnostic)" w-codex-hung hung "$out"
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
# agent-supervisor#112. Both worker lanes in the real incident sat on
# exactly this shape -- unrecognised by any harness marker -- for four
# hours, and `lanes.sh` reported `unknown` the whole time: correctly
# withheld from `--free`, but indistinguishable from a pane that had been
# unrecognised for four SECONDS while repainting. w-firstrun has sat this
# long (1300s, past the default NEVER_BUSY_AFTER=1200) with no repaint at
# all -- it must read the new state, and it must NOT read free.
want "a lane stuck at an unrecognised dialog since launch is never-busy, not free" w-firstrun never-busy "$out"
# The same unrecognised text, moments after the window came up (age=5s),
# must NOT flip to never-busy -- that would be #124/#126's one-way ratchet
# run in reverse for the WRONG state (never-busy is exactly as withheld from
# --free as unknown is, but conflating "just appeared" with "stuck for
# 20+ minutes" would make the new state as noisy as the old unknown count
# already was, defeating the reason #112 exists). It must still read plain
# unknown, exactly as this shape read before this change.
want "the same unrecognised dialog seconds after launch is still plain unknown" w-firstrun-fresh unknown "$out"
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
want "a lane whose cwd no longer exists is broken, not free" w-missing-cwd broken "$out"

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
#
# agent-dotfiles#237 SPLIT this row out of `dead` without weakening anything
# above: the pane's process is still what decides that no agent is running,
# and the lane is still reported. What `stale` adds is the second fact #154
# had no way to say -- this window's NAME still claims a task it cannot be
# doing. That is the #237 incident exactly (nine dead shells wearing finished
# work's names, read as current, respawned over live agents), and the name is
# consulted here ONLY to notice the claim, never to decide what the lane was
# doing; `restore.sh` answers that from the ledger.
want "a renamed lane that lost its agent is stale, not dead" ad102-renamed-lane stale "$out"
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

# agent-supervisor#10: poller-recover.sh sets `remain-on-exit` on the poller's
# window, so a poller that exits leaves a DEAD pane here (no process at all)
# instead of taking the window with it, for up to one watchdog tick. Without
# the name fallback below, `is_service_pane` cannot see a process that no
# longer exists and this would read plain `dead` -- and dispatch.sh's normal
# response to `dead` is to launch a worker agent into it, permanently handing
# the poller's window to a lane.
want "a poller mid-recovery (process gone, remain-on-exit pane) is service, not dead" inbox-poll service "$out"
# THE REGRESSION THAT MATTERS, applied to the new branch: the fallback is
# gated on the process actually being gone, not just on the window's name.
# A pane that merely CLAIMS the poller's window name while a real, unrelated
# shell is alive underneath it must still read dead -- trusting the name
# outright here would be exactly the #237 mistake this file already rejected
# once. Two fixture rows share the name "inbox-poll" on purpose, to prove
# the branch keys on the pid, not a name lookup that stops at the first hit.
want "a plain shell squatting the poller's window name is still dead" inbox-poll dead "$out"

# #154: the count line is the hazard, not the table row -- `loop-tick.md` tells
# a reader to restart every lane it counts. Five dead lanes here (w-dead,
# free-27, w-hand-run-poller, w-mentions-poller, and #10's imposter row); the
# service window (both the alive one and the mid-recovery one) must not be
# one of them, and #237 moved ad102-renamed-lane to its own `stale` count
# line below -- it is still counted, still visible, just not under a heading
# whose instruction ("restart before dispatching") is the wrong one for it.
if grep -qE '5 lane\(s\) have no agent' <<<"$out"; then
  echo "  ok   the dead count line counts only genuinely dead lanes"; pass=$((pass+1));
else
  echo "  FAIL dead count line is not 5 in:"; sed 's/^/       /' <<<"$out"; fail=$((fail+1));
fi

if grep -qE '1 lane\(s\) have a missing cwd' <<<"$out"; then
  echo "  ok   the broken count line names missing cwd lanes"; pass=$((pass+1));
else
  echo "  FAIL no broken count line in:"; sed 's/^/       /' <<<"$out"; fail=$((fail+1));
fi

# agent-dotfiles#237: the stale count line, and the WORDING is the point. The
# incident was not caused by a missing row -- `dead` was reported correctly
# for all nine panes. It was caused by nobody being told the NAMES were a
# stale snapshot, so an operator acted on them. A count line that said
# "restart before dispatching" is exactly the instruction that killed live
# agents.
if grep -qE '1 lane\(s\) have no agent but still carry a task name' <<<"$out" \
  && grep -qiE 'STALE, do not trust it' <<<"$out"; then
  echo "  ok   the stale count line names the name itself as the hazard"; pass=$((pass+1));
else
  echo "  FAIL stale count line missing or does not warn about the name:"; sed 's/^/       /' <<<"$out"; fail=$((fail+1));
fi
# The narrowing must be exactly that: `stale` is reachable ONLY from `dead`.
# If a single lane that used to read free/busy/blocked/unknown now reads
# stale, this change has broken dispatch, not improved reporting.
if [ "$(awk '$NF=="stale"' <<<"$out" | wc -l | tr -d ' ')" = 1 ]; then
  echo "  ok   stale claimed exactly one row, and it came from dead"; pass=$((pass+1));
else
  echo "  FAIL stale claimed rows other than the one dead lane:"; sed 's/^/       /' <<<"$out"; fail=$((fail+1));
fi
# A stale lane is never offered for work -- the same withholding `dead` had.
if grep -qE '(^|:)27$' <<<"$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" bash "$LANES" --free 2>&1)"; then
  echo "  FAIL --free offered the stale lane"; fail=$((fail+1));
else
  echo "  ok   --free withholds the stale lane"; pass=$((pass+1));
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
# w-subagent-task, w-subagent-wait, and w-firstrun-fresh are unknown here
# -- 5. (w-firstrun itself has aged out into never-busy, below, and must
# NOT be counted here too.)
if grep -qE '5 lane\(s\) are unclassified' <<<"$out"; then
  echo "  ok   the table prints a count line for unknown lanes"; pass=$((pass+1));
else
  echo "  FAIL no unknown count line in:"; sed 's/^/       /' <<<"$out"; fail=$((fail+1));
fi

# #112: the state this issue exists to add. w-firstrun is the only fixture
# old enough (1300s, past NEVER_BUSY_AFTER=1200) and still unrecognised.
if grep -qE '1 lane\(s\) have been up 1200s\+ and have never gone ready or busy' <<<"$out"; then
  echo "  ok   the table prints a count line for never-busy lanes"; pass=$((pass+1));
else
  echo "  FAIL no never-busy count line in:"; sed 's/^/       /' <<<"$out"; fail=$((fail+1));
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
           telegram-poller ad102-renamed-lane free-27 w-hand-run-poller w-mentions-poller \
           w-missing-cwd w-firstrun w-firstrun-fresh; do
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
if python3 -c 'import json,sys; rows=json.load(sys.stdin); sys.exit(0 if any(r.get("window")==26 and r.get("window_id")=="@126" and r.get("name")=="telegram-poller" and r.get("command")=="bash" and r.get("state")=="service" for r in rows) else 1)' <<<"$json"; then
  echo "  ok   --json reports the service window as service"; pass=$((pass+1));
else
  echo "  FAIL --json missing the service object in:"; sed 's/^/       /' <<<"$json"; fail=$((fail+1));
fi
if python3 -c 'import json,sys; rows=json.load(sys.stdin); sys.exit(0 if any(r.get("window")==4 and r.get("window_id")=="@104" and r.get("name")=="w-dead" and r.get("command")=="zsh" and r.get("state")=="dead" for r in rows) else 1)' <<<"$json"; then
  echo "  ok   --json carries the dead lane object with its state intact"; pass=$((pass+1));
else
  echo "  FAIL --json changed shape for a dead lane:"; sed 's/^/       /' <<<"$json"; fail=$((fail+1));
fi
if python3 -c 'import json,sys; rows=json.load(sys.stdin); sys.exit(0 if any(r.get("window")==49 and r.get("window_id")=="@149" and r.get("name")=="w-missing-cwd" and r.get("state")=="broken" for r in rows) else 1)' <<<"$json"; then
  echo "  ok   --json reports a missing-cwd lane as broken"; pass=$((pass+1));
else
  echo "  FAIL --json missing the broken lane object:"; sed 's/^/       /' <<<"$json"; fail=$((fail+1));
fi
# #112: --json must carry the new state too, not just the human-readable table.
if python3 -c 'import json,sys; rows=json.load(sys.stdin); sys.exit(0 if any(r.get("name")=="w-firstrun" and r.get("state")=="never-busy" for r in rows) else 1)' <<<"$json"; then
  echo "  ok   --json reports the stuck-since-launch lane as never-busy"; pass=$((pass+1));
else
  echo "  FAIL --json missing the never-busy lane object:"; sed 's/^/       /' <<<"$json"; fail=$((fail+1));
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
# agent-supervisor#36: a ledger-vs-pane reconciler has to threshold on a
# measurement tmux produced, not on a timestamp this system wrote. `lanes.sh`
# already reads `#{window_activity}` for hung/busy; --json must expose the
# derived idle seconds so a caller can join that observable pane state to the
# ledger without reimplementing lane classification.
if python3 -c 'import json,sys; rows=json.load(sys.stdin); sys.exit(0 if rows and all(isinstance(r.get("idle_seconds"), int) for r in rows) else 1)' <<<"$json"; then
  echo "  ok   --json exposes tmux-derived idle_seconds on every row"; pass=$((pass+1));
else
  echo "  FAIL --json is missing idle_seconds:"; sed 's/^/       /' <<<"$json"; fail=$((fail+1));
fi
if python3 -c 'import json,sys; rows=json.load(sys.stdin); row=next(r for r in rows if r["name"]=="w-hung"); sys.exit(0 if row["idle_seconds"] >= 900 else 1)' <<<"$json"; then
  echo "  ok   --json idle_seconds comes from window_activity age"; pass=$((pass+1));
else
  echo "  FAIL --json idle_seconds did not preserve the fixture age:"; sed 's/^/       /' <<<"$json"; fail=$((fail+1));
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

# --- agent-supervisor#83 mutation check: drop the live-child check ---------
# Mutate `pane_has_live_children` to always fail (as if lanes.sh never
# checked the process tree at all) and confirm w-hung-live-child flips to
# `hung` -- the exact regression #83 fixes, reproduced on demand so the
# assertion above is provably checking something. w-hung, which has no live
# child in the fixture either way, must stay `hung` throughout: the mutant
# only removes a check that could move a lane OUT of hung, so nothing that
# was already hung is affected.
MUTANT83="$LANES_DIR/.lanes-mutant-83.sh"
trap 'rm -f "$MUTANT83"' EXIT
patch83_rc=0
python3 - "$LANES" "$MUTANT83" <<'PY' || patch83_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''pane_has_live_children() {
  local pid="${1:-}" tree kids k
  [ -n "$pid" ] || return 1
  tree=$(ps -o pid=,ppid= -ax 2>/dev/null) || return 1
  [ -n "$tree" ] || return 1
  kids=$(awk -v p="$pid" '$2==p{print $1}' <<<"$tree")
  for k in $kids; do
    awk -v p="$k" '$2==p{f=1} END{exit !f}' <<<"$tree" && return 0
  done
  return 1
}'''
replacement = '''pane_has_live_children() {
  return 1
}'''
assert text.count(marker) == 1, "expected exactly one pane_has_live_children body, found %d -- file shape changed" % text.count(marker)
open(dst, "w").write(text.replace(marker, replacement, 1))
PY
if [ "$patch83_rc" -ne 0 ]; then
  echo "  FAIL setup: could not patch a copy of lanes.sh with pane_has_live_children disabled (exit $patch83_rc)"; fail=$((fail+1));
else
  echo "  ok   setup: patched a copy of lanes.sh with pane_has_live_children disabled"; pass=$((pass+1));
  chmod +x "$MUTANT83"
  mutant83_out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" bash "$MUTANT83" 2>&1)
  if grep -qE "^[0-9]+ +w-hung-live-child +[^ ]+ +hung$" <<<"$mutant83_out"; then
    echo "  ok   mutation confirmed: without the live-child check, a lane with live work is misreported hung (the assertion above would be red)"; pass=$((pass+1));
  else
    echo "  FAIL mutation confirmed: disabling the live-child check did not reproduce hung for w-hung-live-child:"; sed 's/^/       /' <<<"$mutant83_out"; fail=$((fail+1));
  fi
  if grep -qE "^[0-9]+ +w-hung +[^ ]+ +hung$" <<<"$mutant83_out"; then
    echo "  ok   a genuinely wedged lane is still hung with the check disabled"; pass=$((pass+1));
  else
    echo "  FAIL w-hung stopped reading hung once the live-child check was disabled:"; sed 's/^/       /' <<<"$mutant83_out"; fail=$((fail+1));
  fi
fi
rm -f "$MUTANT83"
trap - EXIT

# --- agent-supervisor#92 mutation check: reintroduce the exact reviewer-
# flagged bug -- checking whether the PANE has any live child (the harness
# itself, always true while it is alive) instead of whether the HARNESS has
# a live child of its own (real background work). PR #92's REQUEST CHANGES
# review demonstrated this narrows `hung` to "the harness process exited",
# which is not the state `hung` represents -- a genuinely wedged w-hung
# lane, which has the harness alive with no work under it, would be
# misreported busy. Reproduced on demand rather than trusted from the review
# comment, per this repo's own discipline: an instrument that cannot show a
# thing failing looks exactly like the thing never having been true.
MUTANT92="$LANES_DIR/.lanes-mutant-92.sh"
trap 'rm -f "$MUTANT92"' EXIT
patch92_rc=0
python3 - "$LANES" "$MUTANT92" <<'PY' || patch92_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''pane_has_live_children() {
  local pid="${1:-}" tree kids k
  [ -n "$pid" ] || return 1
  tree=$(ps -o pid=,ppid= -ax 2>/dev/null) || return 1
  [ -n "$tree" ] || return 1
  kids=$(awk -v p="$pid" '$2==p{print $1}' <<<"$tree")
  for k in $kids; do
    awk -v p="$k" '$2==p{f=1} END{exit !f}' <<<"$tree" && return 0
  done
  return 1
}'''
replacement = '''pane_has_live_children() {
  local pid="${1:-}"
  [ -n "$pid" ] || return 1
  ps -o pid=,ppid= -ax 2>/dev/null | awk -v p="$pid" '$2==p{f=1} END{exit !f}'
}'''
assert text.count(marker) == 1, "expected exactly one pane_has_live_children body, found %d -- file shape changed" % text.count(marker)
open(dst, "w").write(text.replace(marker, replacement, 1))
PY
if [ "$patch92_rc" -ne 0 ]; then
  echo "  FAIL setup: could not patch a copy of lanes.sh with the PR #92 review's flagged one-level check (exit $patch92_rc)"; fail=$((fail+1));
else
  echo "  ok   setup: patched a copy of lanes.sh with the pane-level-only live-child check"; pass=$((pass+1));
  chmod +x "$MUTANT92"
  mutant92_out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" bash "$MUTANT92" 2>&1)
  if grep -qE "^[0-9]+ +w-hung +[^ ]+ +busy$" <<<"$mutant92_out"; then
    echo "  ok   mutation confirmed: a pane-level-only check misreports a genuinely wedged lane busy, the exact bug PR #92's review found"; pass=$((pass+1));
  else
    echo "  FAIL a pane-level-only live-child check did not reproduce the reviewer-flagged bug for w-hung:"; sed 's/^/       /' <<<"$mutant92_out"; fail=$((fail+1));
  fi
fi
rm -f "$MUTANT92"
trap - EXIT

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
