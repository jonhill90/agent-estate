#!/bin/bash
# Report the real state of every worker lane in a tmux session.
#
# WHY: "idle" was doing four different jobs, and the supervisor could not tell
# them apart. All four were hit in one night, 2026-08-11:
#
#   free     an agent is running and waiting for work        -> dispatch to it
#   busy     an agent is mid-turn                            -> leave alone
#   hung     the pane looks busy but has stopped advancing   -> needs a human
#   blocked  the agent is waiting on an interactive prompt    -> needs a human
#   unsent   a brief is typed into the box and never submitted -> needs a human
#   dead     no agent at all, just a shell                   -> restart the agent
#   service  a supervisor service, deliberately not a lane   -> leave alone
#   unknown  no probe recognizes the last line                -> ask a human
#            (a non-Claude harness, or a Claude Code shape that is not the
#            enumerated ready footer -- see READY_RE below. #126: this is
#            the default now, not the exception -- see the comment there.)
#
# #141 added `unsent`, and it is worth being precise about what it buys. Two
# lanes sat for 40 minutes holding a full brief that had been typed in and
# never submitted, because the `Enter` landed while `/clear` was repainting.
# `--free` did NOT offer them -- the whitelist held, and they read `unknown` --
# but `unknown` means "not offered", not "someone should look". There is no
# way to tell a lane that has been unknown for 40 minutes holding work from
# one that was unknown for 4 seconds while repainting. So the point of
# `unsent` is VISIBILITY: it is the same withholding, with the reason attached
# and a count line in the table. See input-box.sh for how it is detected and
# for what was measured rather than guessed.
#
# `capture-pane` alone reports the last three identically. A dispatch sent to a
# dead lane lands in zsh, which answers "no such file or directory: /clear" and
# the work is silently lost. A dispatch sent to a hung lane is queued forever.
#
# Two signals, neither sufficient alone:
#   - `pane_current_command` separates dead from everything else.
#   - Sampling the pane twice separates hung from busy: a live turn's elapsed
#     timer advances between samples, a wedged one does not.
#
# Usage: lanes.sh [session]           human-readable table
#        lanes.sh --free [session]    print only lane names safe to dispatch to
#        lanes.sh --blocked [session] print only lane names waiting on a human
#        lanes.sh --json [session]
#
# Exit 0 always when the session exists; the states are the output, not the
# exit code. Exit 1 if the session does not exist -- which is NOT "no lanes".

set -uo pipefail

LANES_HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./input-box.sh
. "$LANES_HERE/input-box.sh"

SESSION="${2:-${LANES_SESSION:-agent-dotfiles}}"
MODE="${1:-}"
case "$MODE" in
  --free|--blocked|--json) ;;
  "") ;;
  *) SESSION="$MODE"; MODE="" ;;
esac

# Shells mean "the agent exited and left the pane behind".
SHELLS="bash|zsh|sh|fish|login"
# #154: ...with one exception, and it is a NARROW one. `inbox-poll.sh` is
# deployed as a window in the lane session, per the deployment path its own
# header recommends. It is a bash script, so its pane's command is legitimately
# `bash`, and this classifier reported it `dead` -- whereupon the count line
# below told whoever read the table to restart it. Restarting it replaces the
# poller with an agent and Jon's Telegram replies stop arriving, which looks
# exactly like nobody having written anything: the precise defect the inbound
# half exists to prevent.
#
# THE SIGNAL IS THE PANE'S OWN PROCESS, not the window's name. #154 proposed a
# name convention ("anything not `free-N` is not a lane") as the cheap option,
# and it is the wrong one: a lane that finished, was renamed, and then lost its
# agent is a state this estate has seen (#102), and under that rule it would
# stop being reported at all. `pane_start_command` was measured next and is
# empty for every window under tmux 3.5, including the poller -- it cannot
# carry this. What does, measured against the live session on 2026-08-11:
#
#   window 11 (the poller)  pane_pid -> `bash .../inbox-poll.sh`
#   every lane, 1-10        pane_pid -> `-zsh`
#
# because the poller's window was created with the script as its command while
# a lane's agent runs as a CHILD of the pane's login shell. So a pane is a
# service iff its FIRST process is one of this directory's long-running
# services. That cannot drift the way a config list can, and it says nothing
# about any window whose process is a shell -- every genuinely dead lane,
# renamed or not, is classified exactly as it was before #154.
#
# Deliberately narrow in two ways. Only the pane's own process is inspected,
# not its descendants: a poller someone typed into an interactive shell still
# reads `dead`, because a rule that searched the process tree would exempt any
# lane that ever shelled out, and hiding one real dead lane is worse than this
# whole bug. And only `inbox-poll.sh` is listed, because it is the only service
# this estate actually runs in the lane session -- `watchdog.sh` runs from a
# LaunchAgent and never occupies a window. LANES_SERVICE_RE extends it without
# editing code; whatever is added must be observed running that way first.
SERVICE_RE="${LANES_SERVICE_RE:-(^|/)inbox-poll\.sh( |$)}"
# The supervisor's own pane. It is never a dispatch target: sending a worker
# brief there /clear's the loop and replaces it with someone else's task.
# Done twice on 2026-08-11 -- once via an empty tmux target, once because
# --free cheerfully offered window 1 while the supervisor sat idle between
# ticks. "Free" and "yours to take" are different questions.
SUPERVISOR_WINDOW="${LANES_SUPERVISOR_WINDOW:-1}"
# A lane is hung if it looks busy but tmux has seen no output from it for this
# long. Must exceed the slowest legitimate repaint interval -- Claude Code's
# footer drops to MINUTE granularity past 60s, so a live turn can go ~60s
# without changing a single byte.
HUNG_AFTER="${LANES_HUNG_AFTER:-180}"
# Footer chrome that means "this pane is waiting on a human keystroke".
#
# The first version of this probe matched only `Enter to select`, inferred from
# a single example. Review of #124 drove a real Claude Code pane (v2.1.220)
# through four genuine prompts and only ONE of them says that:
#
#   folder trust     Enter to confirm · Esc to cancel
#   /model           Enter to set as default · s to use this session only · Esc to cancel
#   bash permission  Esc to cancel · Tab to amend · ctrl+e to explain
#   /theme           Enter to select · Esc to cancel
#
# All three of the misses read `free`, including the bash tool-permission
# approval -- the commonest blocking event a supervised lane hits, and the one
# the whole state exists for. `Esc to cancel` is the substring common to all
# four; it is what every dismissible dialog paints.
#
# A false positive here is worse than the bug -- it makes a lane permanently
# undispatchable -- so this was checked in the other direction too, against the
# same live pane: idle at the prompt, text typed but unsent, the `/` command
# popup, the `@` file popup, `?` shortcuts, and a running turn. None of those
# last lines contain the marker; they end in `⏸ manual mode on · ...`,
# `/keybindings to customize`, or `esc to interrupt`.
#
# `Enter to select` is kept as a second marker, not as the only one: it costs
# nothing and covers any menu that paints a selection footer without an Esc
# line. Anything added here must be observed on a real pane first, not inferred.
BLOCKED_MARKERS='Esc to cancel|Enter to select'

# #133: a confirmation dialog's SELECTED OPTION ROW, e.g.
#
#   ❯ 1. Post the comment
#   ❯ 2. Show me the comment
#
# The first of those is verbatim what lane 6 displayed on 2026-08-11 while
# blocked on an approval prompt, holding a completed-but-unposted review
# verdict -- and `READY_RE`'s bare-`❯` half matches it, so had that row been
# the last line the lane would have read `free` and a dispatch would have
# destroyed the verdict.
#
# The bad case is still not demonstrated: driving a real Claude Code pane
# (v2.1.220) through the folder-trust dialog at heights 30 down to 6 kept the
# `Esc to cancel` footer anchored last every time, and the /theme dialog
# overflowed instead, putting body text last. Neither ever put an option row
# last. But #133's point stands regardless of which shapes were reachable on
# one evening: an option row means a dialog is up, so matching it as `blocked`
# removes the dependency on where the footer lands rather than waiting for a
# capture that proves the hazard. The direction that matters is FEWER things
# reading `free`, never more.
#
# `❯` here is followed by an ORDINARY space. The live input box uses a
# NO-BREAK SPACE (see input-box.sh), so a lane whose own prompt begins `1. `
# cannot collide with this.
OPTION_ROW_RE='^[[:space:]]*❯ [0-9]+\.[[:space:]]'

# #126: free used to be "whatever is left after busy/hung/blocked/dead are
# ruled out" -- a blacklist. Two lanes running an approved billed eval and a
# research task read free while each had delegated to a background subagent:
# the main agent was idle (no `esc to interrupt`), but the lane's work was
# not done. That was the THIRD blacklist patch in one night (#102 renamed-but-
# finished, #123 blocked-on-a-prompt, this one) -- three fixes to one
# predicate says the predicate is inverted, not that a fourth pattern is
# missing. So free is now a whitelist: only a recognised ready shape is
# offered; every last line this probe has not been shown is `unknown`, which
# --free already excludes, rather than guessed as available.
#
# Two ready shapes are known-safe, both confirmed against real Claude Code
# panes (v2.1.220) on 2026-08-11 -- lanes sitting at the prompt with nothing
# delegated. Their last non-empty line reads, e.g.:
#   ⏵⏵ bypass permissions on (shift+tab to cycle) · ← 1 agent
#
# The captured status line ends `← 1 agent` -- the count includes the main
# agent itself, so 1 means nothing is delegated. The #126 panes read
# `← 2 agents` (this file's own test fixture, captured off a live idle
# footer during #124's review) or a bare task-list row with no count at all
# (`◯ general-purpose  Verifying tick-7.log with grep      42s`,
# `✻ Waiting for 1 background agent to finish` -- both observed live on the
# lanes #126 reported). None of those end `← 1 agent`, so none match.
#
# The note that used to stand here described those panes as holding "a
# typed-but-unsent brief". Re-measuring for #141 showed that was a misreading:
# what the box held was Claude Code's own DIM placeholder suggestion, which
# occupies the same row and is byte-identical to typed text in a plain capture
# (`❯ now echo goodbye` was a SUGGESTED follow-up, not something anyone typed).
# A box genuinely holding typed text drops the `← 1 agent` segment from this
# footer -- measured across three live panes -- so it does not match READY_RE
# at all, and such a lane read `unknown`, which is exactly what #141 records.
# The correction matters because the old wording implied this footer had been
# checked against the #141 hazard and found safe. It had not been.
#
# The second shape, a bare `❯ ...` line with no footer at all, covers older
# captures and the test fixtures that stand in for "a normal prompt" without
# spelling out real footer chrome -- harmless to keep since it still refuses
# anything containing `←`, so a stray agent-count segment on a `❯` line still
# fails the match.
READY_RE='^❯ [^←]*$|← 1 agent$'

if ! tmux has-session -t "$SESSION" 2>/dev/null; then
  echo "lanes: session '$SESSION' does not exist" >&2
  exit 1
fi

# First sample of every pane, taken together so the gap is shared rather than
# paid per lane.
TAB=$'\t'   # tmux does not interpret a literal \t inside -F
# One list-panes call for every field, all from the ACTIVE pane of each window
# -- the pane that capture-pane reads and send-keys would hit. Reading the
# command from ":$w.1" while capturing from ":$w" meant a split lane could
# report the first pane's command and the active pane's screen.
declare -a IDX NAME CMD ACTIVITY PANEMODE PANEPID
while IFS=$'\t' read -r w n c a m p; do
  [ -n "$w" ] || continue
  IDX+=("$w"); NAME+=("$n"); CMD+=("$c"); ACTIVITY+=("$a"); PANEMODE+=("$m"); PANEPID+=("$p")
done < <(tmux list-panes -s -t "$SESSION" -f '#{pane_active}' \
           -F "#{window_index}${TAB}#{window_name}${TAB}#{pane_current_command}${TAB}#{window_activity}${TAB}#{pane_in_mode}${TAB}#{pane_pid}" 2>/dev/null)

# #154. Answers one question about a pane whose command is a shell: is that
# shell one of this directory's services, or is it the wreckage of an agent
# that exited? `pane_pid` is tmux's own handle on the pane's FIRST process, so
# this reads what that process is running rather than trusting a name, a
# window title, or a config list. Nothing about the pane's TEXT is consulted --
# a dead lane whose scrollback merely names the script is still dead, which is
# the #65 discipline applied to a probe that is not a status-line probe at all.
is_service_pane() {
  local pid="${1:-}" argv
  [ -n "$pid" ] || return 1
  argv=$(ps -o args= -p "$pid" 2>/dev/null) || return 1
  [ -n "$argv" ] || return 1
  grep -qE "$SERVICE_RE" <<<"$argv"
}

now_epoch=$(date +%s)

emit_rows() {
  local i
  for i in "${!IDX[@]}"; do
    local w="${IDX[$i]}" name="${NAME[$i]}" cmd="${CMD[$i]}" act="${ACTIVITY[$i]}" mode="${PANEMODE[$i]}" pid="${PANEPID[$i]}"
    local pane state age
    # ONLY the status line -- the last non-empty line of the visible pane.
    #
    # This used to grep `capture-pane -S -6`, which is six scrollback lines
    # PLUS the whole visible pane (~60 lines). Any lane that had merely
    # PRINTED the phrase -- reviewing this very file, reading loop-tick.md --
    # matched and was reported busy, then hung. Two live lanes were in that
    # state during the review that found it. An earlier comment here blamed
    # the Copilot harness; that was a misdiagnosis. It was never
    # harness-specific, it was the capture window.
    pane=$(tmux capture-pane -p -t "$SESSION:$w" 2>/dev/null | grep -v '^[[:space:]]*$' | tail -1)

    # A SECOND, separate capture, and deliberately not a widening of the one
    # above. `-e` keeps the SGR attributes that input-box.sh needs to tell a
    # dim placeholder from typed text, and it is read ONLY by input_box_state,
    # which anchors on a marker (`❯` + NO-BREAK SPACE) that nothing but the
    # live input box paints. The status-line probes keep reading exactly one
    # line, which is the #65 discipline; this does not relax it, and taking a
    # second capture rather than reusing one keeps that impossible to blur.
    box=$(tmux capture-pane -pe -t "$SESSION:$w" 2>/dev/null | input_box_state)

    if [ "$w" = "$SUPERVISOR_WINDOW" ]; then
      state=supervisor
    elif [ "${mode:-0}" != "0" ]; then
      # A pane in copy mode still captures its underlying screen, so an idle
      # agent someone scrolled up reads free -- but keys sent there are eaten
      # by the copy-mode key table and never reach the agent. Reproduced live
      # against a real tmux server in review of #65: the dispatch vanished and
      # copy mode exited.
      state=scrolled
    elif [[ "$cmd" =~ ^($SHELLS)$ ]]; then
      # #154: a pure NARROWING of `dead`, the same shape #141 used for `free`.
      # The only lane this can move is one that was already going to be called
      # dead, and it can only move it to `service` -- no other state's
      # classification is reachable from here, so `--free`, `--blocked`, and
      # every existing row are untouched. See SERVICE_RE above for why the
      # signal is the pane's own process and not the window's name.
      if is_service_pane "$pid"; then state=service; else state=dead; fi
    elif [[ ! "$cmd" =~ ^(claude|claude\.exe)$ ]]; then
      # The busy probe greps Claude Code's own status string. Other harnesses
      # paint different UIs, and guessing produces false alarms: a healthy idle
      # Copilot pane was classified `hung` because that string appeared in its
      # scrollback. Report what is known and refuse to invent the rest.
      state=unknown
    elif grep -qE "$BLOCKED_MARKERS" <<<"$pane" || grep -qE "$OPTION_ROW_RE" <<<"$pane"; then
      # An interactive prompt, not idle: the agent is waiting on a human, not
      # on work. Must be decided before free, since that is the classification
      # it is stealing from. Match the harness's own footer chrome, not the
      # option text -- that is model-authored and varies per prompt. And match
      # only the status line above, the same fix #65 required for the busy
      # probe: an exact-match sweep over scrollback caught a lane merely
      # displaying the phrase, not showing it.
      state=blocked
    elif grep -q 'esc to interrupt' <<<"$pane"; then
      # Busy-looking. Hung iff tmux has seen no output for HUNG_AFTER.
      #
      # This deliberately does NOT diff pane text across a short gap. That was
      # the first version and it was wrong: Claude Code's elapsed footer shows
      # minute granularity past 60s, so a turn running 61-119s prints an
      # identical byte string for a whole minute and was reported hung while
      # fully alive. Found in review of #65. tmux's own activity timestamp is
      # independent of whatever the harness chooses to paint.
      age=$(( now_epoch - ${act:-now_epoch} ))
      if [ "$age" -ge "$HUNG_AFTER" ]; then state=hung; else state=busy; fi
    elif [ "$box" = text ]; then
      # #141. Decided AFTER busy/hung -- a lane typing ahead into the box
      # while a turn runs is busy, and busy is the more useful thing to say --
      # and BEFORE free, because free is the classification it is stealing.
      #
      # This is a pure narrowing: `text` can only move a lane out of free,
      # never into it. `unknown` from input_box_state changes nothing, so
      # every lane this probe cannot read is classified exactly as it was
      # before #141.
      state=unsent
    elif grep -qE "$READY_RE" <<<"$pane"; then
      state=free
    else
      # #126: the old code fell through to `free` here. Everything that is
      # not busy, hung, blocked, dead, scrolled, or another harness used to
      # count as available -- including a lane that had delegated to a
      # background subagent and was nowhere near done. Default is now
      # unknown: nothing lands here unless it positively matches READY_RE.
      state=unknown
    fi
    printf '%s\t%s\t%s\t%s\n' "$w" "$name" "$cmd" "$state"
  done
}

rows=$(emit_rows)

case "$MODE" in
  --free)
    # session:index, never the window name: names are not unique (the live
    # session briefly had two windows both called ad65-lanes-review) and
    # `send-keys -t session:name` silently hits the first match.
    awk -F'\t' -v s="$SESSION" '$4=="free"{print s ":" $1}' <<<"$rows" ;;
  --blocked)
    # Same session:index shape as --free, for the same reason. This is the
    # routing signal agent-dotfiles#142 builds inbound Telegram delivery on:
    # a lane in `blocked` is waiting on an interactive prompt, and Jon's
    # reply is presumed to be the answer to it.
    awk -F'\t' -v s="$SESSION" '$4=="blocked"{print s ":" $1}' <<<"$rows" ;;
  --json)
    printf '['
    awk -F'\t' 'BEGIN{c=0}
      {if(c++)printf(",");printf("{\"window\":%s,\"name\":\"%s\",\"command\":\"%s\",\"state\":\"%s\"}",$1,$2,$3,$4)}
      END{}' <<<"$rows"
    printf ']\n' ;;
  *)
    printf '%-4s %-24s %-12s %s\n' WINDOW NAME COMMAND STATE
    awk -F'\t' '{printf("%-4s %-24s %-12s %s\n",$1,$2,$3,$4)}' <<<"$rows"
    dead=$(awk -F'\t' '$4=="dead"' <<<"$rows" | wc -l | tr -d ' ')
    hung=$(awk -F'\t' '$4=="hung"' <<<"$rows" | wc -l | tr -d ' ')
    blocked=$(awk -F'\t' '$4=="blocked"' <<<"$rows" | wc -l | tr -d ' ')
    unsent=$(awk -F'\t' '$4=="unsent"' <<<"$rows" | wc -l | tr -d ' ')
    unknown=$(awk -F'\t' '$4=="unknown"' <<<"$rows" | wc -l | tr -d ' ')
    [ "$dead" -gt 0 ] && echo "  ${dead} lane(s) have no agent — restart before dispatching"
    [ "$hung" -gt 0 ] && echo "  ${hung} lane(s) look wedged — a dispatch there would queue forever"
    # A blocked lane is a question nobody has heard -- surface it, don't just
    # exclude it from --free.
    [ "$blocked" -gt 0 ] && echo "  ${blocked} lane(s) are blocked on a prompt — a human must answer before dispatching"
    # #141: the state that was invisible for 40 minutes. It is withheld from
    # --free either way; what this line adds is that anyone reading the table
    # is TOLD, instead of seeing a lane quietly missing from the free list.
    [ "$unsent" -gt 0 ] && echo "  ${unsent} lane(s) hold an unsent prompt — a brief is typed in the box and was never submitted"
    # #126: free is now a whitelist, so unknown is the expected home for any
    # shape this probe has not been shown yet -- it must never be silent, or
    # an operator reading only the table would see the same lanes vanish from
    # --free with no clue why.
    [ "$unknown" -gt 0 ] && echo "  ${unknown} lane(s) are unclassified — not a recognised ready shape, --free withholds them"
    : ;;
esac
