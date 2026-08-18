#!/bin/bash
# lanes.sh adapter: Claude Code.
#
# agent-dotfiles#201 gave every harness its own file so `lanes.sh` itself
# could stop naming any of them. Every regex below is UNCHANGED from
# pre-#201 lanes.sh -- moved here verbatim, not re-derived. The evidence
# each one is built from (real Claude Code panes, v2.1.220, driven through
# real dialogs during #124/#133/#159/#161/#164/#126) lives in this file's
# git history by way of lanes.sh's history; see those issues for the
# captures. This file only relocates the constants behind the adapter
# contract -- it does not re-measure anything.

HARNESS_NAME=claude
HARNESS_COMMAND_RE='^(claude|claude\.exe)$'

# Launch + send-keys dialect. #201's acceptance is `lanes.sh` alone; these
# two fields are recorded here for the next consumer (dispatch.sh,
# bootstrap-session.sh) and are NOT wired into either yet. Checked, not
# assumed: dispatch.sh's own message send (dispatch.sh:364) calls
# `tmux send-keys -t "$LANE" "$MESSAGE"` with no `-l`, and inbox-route.sh's
# does pass `-l` (inbox-route.sh:139) -- the estate is inconsistent about
# this today, which is exactly the kind of harness-specific literal #201
# says should live in an adapter instead of being reasoned about per call
# site. `1` here records what lanes.sh's own docs say Claude needs; wiring
# either caller to read it is follow-up, out of #201's scope.
#
# agent-supervisor#120: no `--model` here meant every lane launched on the
# account default, which is Opus -- measured at ~3x Sonnet's per-token cost
# for comparable worker volume (issue #120, 2026-08-12 ccusage split). This
# is a launch-time default, not a symptom to patch at turnover: dispatch.sh
# types this same string on every relaunch (dispatch.sh H_LAUNCH_CMD), so
# whatever model is hardcoded here is what every lane silently inherits
# forever. Default to the cheap model; `CLAUDE_LANE_MODEL` lets a lane that
# genuinely needs Opus have it deliberately, by explicit env, not by
# omission.
HARNESS_LAUNCH_CMD="claude --model ${CLAUDE_LANE_MODEL:-sonnet} --dangerously-skip-permissions"
HARNESS_SEND_LITERAL=1

# agent-dotfiles#237: how this harness is told to come back to an EXISTING
# conversation. `%s` is the session id the ledger recorded at dispatch;
# `restore.sh` is the only caller. Checked against the shipped CLI, not
# assumed: `claude --help` on v2.1.220 documents `-r, --resume [value]` as
# "Resume a conversation by session ID", and `--session-id <uuid>` as a
# separate flag for choosing one up front. A harness file that leaves this
# unset says "no resume dialect here", and restore refuses its lanes rather
# than starting a fresh agent -- which is #237's whole failure direction.
HARNESS_RESUME_CMD='claude --dangerously-skip-permissions --resume %s'

# Ready shape -- last non-empty line only (the #65 discipline). Two shapes:
# the real idle footer (`← 1 agent`, the count including the main agent
# itself so 1 means nothing delegated -- #126) and a bare `❯ ...` line with
# no footer, kept for older captures and fixtures that stand in for "a
# normal prompt" without spelling out footer chrome. See lanes.sh's own
# `emit_rows` for the ordering this is checked in.
# agent-supervisor#216: the count is `[0-9]+`, not a literal 1. `lanes.sh`
# tests this against the pane's LAST LINE only, so on a Claude pane the
# footer is the only line it ever sees and the `^❯ ...$` alternative below
# can never fire -- which made `← 1 agent$` the sole working matcher, and a
# footer reading `← 2 agents` unclassifiable. Six healthy lanes across both
# live sessions read `unknown` on 2026-08-16T02:26Z for exactly this, and
# with `DISPATCH_LANE` removed (#89) there is no override: estate capacity
# was zero. See the issue for the measurement that established the counter
# is not a delegation indicator -- a lane holding nothing but the splash
# screen, with an empty transcript and no child process, still read
# `← 2 agents`.
#
# Widening READY_RE runs against the #124/#126 one-way ratchet, so what
# bounds it matters: `busy` is decided before `free` (HARNESS_BUSY_RE below
# -- `esc to interrupt`, `↓ to manage`, the token-count tail), and none of
# those alternatives involve the agent count. A plural footer that is also
# mid-turn, or that still has a background shell registered, stays busy.
# test_lanes.sh pins both.
#
# agent-supervisor#314: a THIRD ready shape, `← for agents`, with no count at
# all. This is the SAME incident the 2026-08-16T02:26Z note above records --
# estate capacity zero because every idle lane read `unknown` -- recurring
# with a new footer string, so the lesson is that pinning the agent-count
# shape is what keeps breaking, not that any one string was wrong.
#
# Measured live 2026-08-17, two idle claude panes (`agent-supervisor:@42`
# post-turn with an empty box, `@43` on the fresh splash), both footers
# reading:
#   ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents
# `pane` is `tail -1` of the non-blank capture (lanes.sh:411), so the FOOTER
# is what READY_RE is matched against, never the `❯` line above it -- the
# first alternative here can only fire when the prompt line is itself last.
# `← [0-9]+ agents?$` cannot match a footer with no digits, so both panes
# fell through to `unknown` and `--free` withheld them. Three dispatches
# (#308, #311, #313) refused with "no free lane" against an estate that had
# two.
#
# Same one-way-ratchet argument as the alternative above, and it holds for
# the identical reason: `busy` and `blocked` are both decided BEFORE `free`,
# and neither of those probes involves the agent counter, so a footer that is
# mid-turn (`esc to interrupt`), has a background shell (`↓ to manage`), or
# is showing a prompt still classifies ahead of this. Anchored to
# end-of-line, so a footer with `· ↓ to manage` trailing it fails this
# alternative exactly as it fails the numeric one.
HARNESS_READY_RE='^❯ [^←]*$|← [0-9]+ agents?$|← for agents$'

# Busy -- last line only. Claude's elapsed-turn footer IS the last line
# while a turn runs (`esc to interrupt`), unlike Codex, whose equivalent
# marker sits several lines above a footer that never changes -- see
# harness/codex.sh's HARNESS_BUSY_TAIL comment for why that harness needs a
# wider window and this one does not.
#
# agent-dotfiles#207: a second busy shape, `↓ to manage`, appended whenever a
# background shell is registered -- singular or plural (`1 shell`/`2 shells`),
# with or without `ctrl+t to hide tasks` in between. Measured live against a
# real Claude Code pane (v2.1.220) in a throwaway tmux server, private
# TMUX_TMPDIR, 2026-08-12: mid-turn with a backgrounded shell running, the
# footer carries BOTH `esc to interrupt` and `↓ to manage` --
#   ⏵⏵ bypass permissions on · 1 shell · esc to interrupt · ← 1 agent · ↓ to manage
# --  already caught by the first alternative. Once the turn itself ends but
# the shell is still running, `esc to interrupt` drops and only the second
# alternative still matches --
#   ⏵⏵ bypass permissions on · 1 shell · ← 1 agent · ↓ to manage
# -- the exact shape agent-dotfiles#207 captured off two live dispatched
# lanes (`ad203-verdict-adapter`, `live184-ad184-race`) reading `unknown`.
# That pane is not mid-turn, but it is not idle either: a background shell it
# started is still doing work on the lane's behalf, and #207's acceptance is
# to treat that as busy (not a new state, and never free) rather than offer
# it to the dispatcher while the shell runs. Deliberately does NOT touch
# HARNESS_READY_RE: `← 1 agent$` is anchored to end-of-line, so a footer with
# `· ↓ to manage` trailing it already fails that anchor on its own -- adding
# this alternative here cannot make a lane MORE free, only less unknown,
# which is the #124/#126 one-way ratchet this file is under.
# agent-supervisor#164: a THIRD busy shape, live off `agent-tui:2` twice ~20
# minutes apart, PR #22 delivered shortly after -- so the lane was healthy
# and working the whole time it read `unknown`. A background subagent's task
# rail pushes the turn footer (the `esc to interrupt` line above) up off the
# last line, and puts its own row there instead, verbatim:
#
#   ✽ Building supervisor-side session write tools… (14m 13s · ↓ 82.3k tokens)
#     ⎿  ◼ Supervisor PR: session write tools + fix #153 marker consumption
#        ◻ agent-tui: rail attach/detach/add/remove UI + MCP client calls
#     ⏵⏵ bypass permissions on · esc to interrupt · ctrl+t to hide tasks · ← 1 agent · ↓ to manage
#     ⏺ main
#     ◯ general-purpose  Build supervisor session write tools   21m 11s · ↓ 243.4k tokens
#
# The last non-empty line is the subagent's OWN row, not the footer, so
# `HARNESS_BUSY_TAIL=1` never sees `esc to interrupt` -- widening the tail
# instead (codex's approach, HARNESS_BUSY_TAIL=4) does not fit here: the
# number of task-list lines and agent rows above the footer is unbounded --
# a lane with a long task list or several concurrent subagents pushes the
# footer arbitrarily far from the end, and #65 already found what happens
# when this probe reads further back than one screen-line of intent (it
# starts matching text the pane merely printed).
#
# What stays exactly one line deep is the subagent row's own trailing
# `<elapsed> · ↓ <tokens> tokens` -- the same live-progress readout the main
# turn's spinner line prints (`14m 13s · ↓ 82.3k tokens`, above), reused
# verbatim for a subagent still executing. This is deliberately NOT "any
# `◯ <name> ...` row": w-subagent-task (agent-supervisor#126, a real capture
# from an EARLIER Claude Code release) is the same row shape with no token
# readout at all --
#
#   ◯ general-purpose  Verifying tick-7.log with grep      42s
#
# -- and that one must keep reading unknown: #126 established that a
# subagent row alone is not proof of anything (a finished-but-not-yet-
# cleared row could look identical), so only the row carrying the live
# token counter -- the same counter the running main turn also prints --
# counts as busy. The counter's exact format (`243.4k` vs some other
# release's rendering) is the one piece of this that could drift with a
# harness release; `[0-9.]+k?` is kept loose enough to survive a
# no-decimal or no-`k` count without loosening the anchor that matters,
# which is the literal `↓ ... tokens` readout at end of line.
HARNESS_BUSY_RE='esc to interrupt|↓ to manage|↓ [0-9]+(\.[0-9]+)?k? tokens$'
HARNESS_BUSY_TAIL=1

# Blocked gate (last line) plus the menu/text split (#159/#164). `Esc to
# cancel` / `Enter to select` cover four real dialogs (folder trust, /model,
# bash permission, /theme); the option-row shape (`❯ 1. ...`) covers #133's
# case where a numbered row, not footer chrome, was the last line.
HARNESS_BLOCKED_MARKERS='Esc to cancel|Enter to select'
HARNESS_OPTION_ROW_RE='^[[:space:]]*❯ [0-9]+\.[[:space:]]'
HARNESS_MENU_ENTER_RE='Enter to [a-z]+'
HARNESS_MENU_TAIL=6
# MODELED, NOT OBSERVED (#164): no genuine free-text-blocked prompt has ever
# been captured against a real Claude Code pane. This exists only so the
# estate's own modelled fixture keeps exercising inbox-route.sh's delivery
# mechanics; widening it to match a real prompt requires a real capture
# first, same rule as every marker above.
HARNESS_TEXT_PROMPT_RE='Type the [a-z]'

# agent-supervisor#115: which model this pane is ACTUALLY running, not what
# it was dispatched with or launched under -- see the issue for why a
# process-tree read (`pgrep -fl claude`) cannot answer this: the visible
# child of a busy pane is whatever the turn spawned (`npm exec
# playwright-mcp`, `caffeinate`), not the harness itself, and even a
# correctly-scoped read of the harness's OWN argv only proves what it was
# launched with -- a live `/model` switch changes what is running without
# touching argv at all. The self-report has to come from the pane's own
# screen.
#
# Measured live against this estate's real session on 2026-08-15, across
# eight lanes mid-conversation (`agent-supervisor:3` through `:10`) plus the
# Director's own pane: NONE of Claude Code v2.1.220's busy/idle/blocked
# footer chrome names the model at all. `grep -oiE "opus|sonnet|haiku"`
# against the full visible screen -- the "obvious" read #115's own issue
# proposes -- found exactly one match across all nine panes, and it was a
# FALSE ONE: a window's own conversation-title bar, "Setup Opus orchestrator
# for agent-dotfiles architecture", matched `Opus` because that word was in
# the TASK NAME, not the model. A bare substring match on the model family
# name is not safe at all -- it fires on prose the agent or a human wrote
# about a model, indistinguishable from the harness reporting its own.
#
# The one place the model genuinely IS self-reported, also captured live
# (`agent-tui:1`, the welcome screen before any turn has run):
#
#   ╭─── Claude Code v2.1.220 ──────────────────────────────...──────────╮
#   │                  Welcome back Jon!                 │ Tips ...     │
#   │     Sonnet 5 with medium effort · Claude Max ·     │ ...          │
#
# -- "<Family> <version> with <effort> effort · <plan> ·", inside the
# startup splash. This is the ONLY genuine self-report found: once a
# conversation is underway the splash scrolls off (confirmed live: a 50000-
# line `capture-pane -S -50000` against a long-running lane found no trace
# of it) and nothing later in the session repeats it -- Claude Code does not
# carry the model in its ordinary footer the way Codex's `<model> <effort> ·
# <cwd>` last line or Copilot's trailing `Claude Sonnet 5` do (see those
# harnesses' own files). So this regex matches ONLY the freshly-launched
# case -- which is exactly the shape #115's own incident needs: a lane
# respawned without `--model sonnet` shows its wrong model on this splash
# for as long as it survives on screen, before the conversation scrolls it
# away. Anywhere else, it will not match, and the caller must read that as
# `unknown`, not as "must be Sonnet" -- silently assuming compliance is the
# exact failure #115 is filed over. Widening this beyond what is captured
# above (e.g. dropping "with <effort> effort" to match a shape never
# observed) is a guess, not a fix, per every other marker in this file.
HARNESS_MODEL_RE='(Opus|Sonnet|Haiku)[[:space:]]+[0-9]+(\.[0-9]+)?([[:space:]]+with[[:space:]]+[a-z]+[[:space:]]+effort)?[[:space:]]*·'
