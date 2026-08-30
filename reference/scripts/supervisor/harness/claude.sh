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
# agent-supervisor#494: every lane also inherited the OPERATOR's whole MCP
# surface, because claude with no `--strict-mcp-config` loads the global
# `mcpServers` from ~/.claude.json plus any project-scoped block matching the
# lane's cwd. A lane does git, gh and bash; it has never needed Mintlify,
# Linear, Cloudflare, Playwright, context7, microsoft-learn or deepwiki. Each
# server is a process the lane pays for at launch and holds for its whole
# life, multiplied by every concurrent lane -- on 2026-08-21 the machine sat
# at 3% free memory with 8.7GB swapped and typing lag in the terminal.
#
# Same posture as `--model` above and for the same reason: this is the one
# line dispatch.sh types on every relaunch, so a default here is inherited
# forever, and an omission is inherited forever too. Default to NO MCP;
# `CLAUDE_LANE_MCP_CONFIG` lets a lane that genuinely needs a server have
# exactly that one, deliberately, by explicit env rather than by inheriting
# the operator's entire desktop configuration.
#
# Checked against the shipped CLI, not assumed (v2.1.220 `claude --help`):
# `--strict-mcp-config` is documented as "Only use MCP servers from
# --mcp-config, ignoring all other MCP configurations", and `--mcp-config
# <configs...>` as the way to name them. `--strict-mcp-config` with no
# `--mcp-config` therefore means zero servers, which is the default here.
CLAUDE_LANE_MCP_FLAGS="--strict-mcp-config"
if [ -n "${CLAUDE_LANE_MCP_CONFIG:-}" ]; then
  CLAUDE_LANE_MCP_FLAGS="--strict-mcp-config --mcp-config ${CLAUDE_LANE_MCP_CONFIG}"
fi

# agent-supervisor#521: Claude Code's own "prompt suggestion" feature paints
# a dim, unsubmitted predicted-next-message into an empty input box after
# every turn -- on by default. #521's investigation (a comment on the issue,
# not code) reproduced this live and traced it to the root cause of
# recurring unattributed text seen in idle build panes: nobody typed it, the
# harness painted it.
#
# `claude --help` (v2.1.220) documents a CLI flag for this,
# `--prompt-suggestions [value]` (`choices: "true", "false", ...`) -- tried
# first, since a documented flag is the more obvious answer than an
# undocumented env var. It does NOT work: `--prompt-suggestions=false`,
# tried live in an isolated tmux socket (`tmux -L agent521-repro`, never the
# shared `estate` session), still painted a dim post-turn suggestion
# identical to the unflagged pane -- confirmed on two separate turns. The
# flag is real (it appears in `--help` and is accepted, no CLI error) but
# does not reach whatever gates this specific TUI behavior; recorded here
# rather than left for the next reader to also discover the hard way.
# `CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false` (named in the shipped
# binary's own strings, not in `--help` at all) is what actually works --
# verified live in the same isolated session: an idle box painted nothing
# after each of two separate turns, versus the unflagged pane's dim
# suggestion appearing after every turn including before any turn ran at
# all. This is the primary fix: no suggestion ever painted means nothing
# for any reader (`send.sh`, `tick-scan.sh`, `look.py capture`) to misread
# as real text. `input-box.sh`'s dim-stripping rule (agent-dotfiles#141,
# agent-supervisor#193) and `dim-strip.sh`'s shared version of it
# (agent-supervisor#521 part 2) stay in place regardless, as defense in
# depth for a harness release where this env var stops working the way the
# CLI flag already turned out not to.
HARNESS_LAUNCH_CMD="CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false claude --model ${CLAUDE_LANE_MODEL:-sonnet} --dangerously-skip-permissions ${CLAUDE_LANE_MCP_FLAGS}"
HARNESS_SEND_LITERAL=1

# agent-dotfiles#256: the launch command above IS already claude's unattended
# posture -- Jon's own `cdsp` alias (`~/.zshrc:123`) is this same
# `--dangerously-skip-permissions` flag. Recorded again here, under its own
# name, so bootstrap-session.sh's `--unattended` has an explicit, per-harness
# answer to resolve rather than assuming H_LAUNCH_CMD is always safe to reuse
# for that (harness/copilot.sh's H_UNATTENDED_CMD is deliberately empty for
# exactly the case where that assumption would not hold).
HARNESS_UNATTENDED_CMD="$HARNESS_LAUNCH_CMD"

# agent-dotfiles#237: how this harness is told to come back to an EXISTING
# conversation. `%s` is the session id the ledger recorded at dispatch;
# `restore.sh` is the only caller. Checked against the shipped CLI, not
# assumed: `claude --help` on v2.1.220 documents `-r, --resume [value]` as
# "Resume a conversation by session ID", and `--session-id <uuid>` as a
# separate flag for choosing one up front. A harness file that leaves this
# unset says "no resume dialect here", and restore refuses its lanes rather
# than starting a fresh agent -- which is #237's whole failure direction.
# agent-supervisor#521: carries the same CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false
# as HARNESS_LAUNCH_CMD above -- a resumed lane repaints the same idle-box
# suggestion a freshly launched one does, so a resume dialect that omitted
# this would silently reopen the bug for every restore.
HARNESS_RESUME_CMD='CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false claude --dangerously-skip-permissions --resume %s'

# agent-supervisor#(codex adapter gap, 2026-08-23): the glob `restore.sh`
# checks before trusting a recorded session id is really on disk -- moved
# out of restore.sh's own body (previously a Claude-only literal hardcoded
# there) so a second harness (harness/codex.sh) can record its own,
# differently-shaped, transcript path instead of restore.sh growing a
# per-harness `if` for something every other resume-capable field already
# externalises here. `%s` is the session id; Claude's own filename IS the
# id (`<session-id>.jsonl`, no prefix), unlike codex's timestamp-prefixed
# rollout files -- see harness/codex.sh's own HARNESS_TRANSCRIPT_GLOB for
# the contrast.
HARNESS_TRANSCRIPT_GLOB='.claude/projects/*/%s.jsonl'

# Ready shape -- last non-empty line only (the #65 discipline).
#
# HISTORY, why this is no longer a suffix allowlist: #216, #314, #324, #350
# and a shape sighted 2026-08-18 were five incidents wearing one defect. The
# text after the counter marker in Claude's footer (a digit, the bare word
# "for", nothing at all) is decorative -- #216 measured this directly, live:
# a lane holding nothing but the splash screen, an empty transcript, and no
# child process still read a plural count. Pinning each observed suffix as
# its own alternative is a promise to keep chasing somebody else's UI, and
# every miss fails toward silence: an unmatched footer reads `unknown`, and
# `unknown` withholds the lane rather than raising an alarm. Three
# enumerated alternatives (a literal count, a literal "for", and a fourth
# with no suffix at all) each fixed one incident and left the shape open for
# the next.
#
# THE FIX matches the PREFIX instead -- the mode banner Claude Code paints
# only once nothing else occupies that row. Both banners below were captured
# live, and in every capture on file (see HARNESS_BUSY_RE below) a pane that
# is mid-turn or still holding a registered background shell drops the
# `(shift+tab to cycle)` hint entirely -- it is painted only when there is
# genuinely nothing to interrupt. That makes the banner text itself, not
# what Claude appends after it, the positive signal: a fifth or sixth suffix
# variant needs no new alternative here, because nothing here reads the
# suffix at all.
#
# This still depends on `busy` and `blocked` being decided BEFORE `free`
# (lanes.sh's classification chain) -- neither of those probes reads this
# prefix, so a footer that is mid-turn (`esc to interrupt`), carries a live
# background shell (`↓ to manage`), or is showing a dialog classifies ahead
# of this regardless of what banner text precedes it; test_lanes.sh pins
# that ordering. Unanchored on purpose: this regex is matched against the
# pane's LAST LINE only (lanes.sh:411), never scrollback (the #65
# discipline), so a substring match here cannot pick up text the pane merely
# printed elsewhere the way a whole-scrollback sweep could.
#
# A bare `❯ ...` prompt line with no footer at all is the remaining
# alternative, kept for older captures and fixtures that stand in for "a
# normal prompt" without spelling out footer chrome.
HARNESS_READY_RE='^❯ [^←]*$|⏸ manual mode on|⏵⏵ bypass permissions on \(shift\+tab to cycle\)'

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
# HARNESS_READY_RE: neither shape above carries the `(shift+tab to cycle)`
# hint HARNESS_READY_RE keys on -- Claude drops that hint whenever a
# background shell is registered, busy or not -- so both already fail the
# ready match on their own, before busy is even checked. Adding this
# alternative here cannot make a lane MORE free, only less unknown, which is
# the #124/#126 one-way ratchet this file is under.
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

# agent-estate#446: this harness's own input-box marker and close mode, for
# `input-box.sh`'s `input_box_state`/`input_box_text` -- see that file's own
# header (section 4) for the full contract. Literal duplicate of that file's
# own `INPUT_BOX_PROMPT` constant, not a reference to it: this file is
# sourced independently by `harness-registry.sh`, in a loop that unsets and
# re-sources one harness at a time, with no guarantee `input-box.sh` has been
# sourced into the same shell at all -- the same reason every other
# harness-specific literal in this file (HARNESS_LAUNCH_CMD, the regexes
# above) is a duplicate rather than a cross-file reference. `❯` (U+276F)
# immediately followed by U+00A0 (NBSP); `rule`, Claude's own box closed by a
# `─`/`━` row -- both unchanged from what `input-box.sh` measured pre-#446.
HARNESS_INPUT_BOX_PROMPT=$'\xe2\x9d\xaf\xc2\xa0'
HARNESS_INPUT_BOX_CLOSE=rule
