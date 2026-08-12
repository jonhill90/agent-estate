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
#            (a harness with no adapter under harness/, or a recognised
#            harness's shape that is not its adapter's enumerated ready
#            footer -- see harness/*.sh. #126: this is the default now, not
#            the exception -- see harness_index_for_command below.)
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
# TWO WAYS TO NAME A WINDOW, AND THEY ARE NOT INTERCHANGEABLE (#241). A
# window INDEX (`session:5`) is a slot number: humans read it, it is what the
# tmux window list shows, and it is what this estate means by "lane 5". It is
# NOT a stable handle -- this server runs `renumber-windows on`, so closing
# any window shifts every higher index down by one, measured in #241. A
# window ID (`session:@12`) is tmux's own handle: stable for the window's
# lifetime and never reused.
#
# So every renderer below emits BOTH, and each consumer takes the one that
# answers its question:
#
#   the table   index only -- it is what Jon reads, and that must not change
#   --json      "window" (index) and "window_id"
#   --free      "<session>:<index>\t<session>:@<id>" -- the LANE identity that
#               the ledger keys on, then the TMUX TARGET to address it with
#
# `--free`'s two columns are the seam `cli.py lane_free` already has as
# `--lane` and `--target`. The ledger identity stays the index on purpose: it
# names a SLOT that must survive a window being closed and recreated, which
# a window id deliberately does not.
#
# Exit 0 always when the session exists; the states are the output, not the
# exit code. Exit 1 if the session does not exist -- which is NOT "no lanes".

set -uo pipefail

LANES_HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./input-box.sh
. "$LANES_HERE/input-box.sh"

# agent-dotfiles#201: every harness-specific shape this file used to hold as
# a literal now lives in its own file under harness/ -- lanes.sh asks the
# adapter instead of naming a harness itself. Adding a harness is a new file
# there; removing one is a deletion, no edit here required. See
# harness/claude.sh and harness/codex.sh for the contract each file fills in
# and the real-pane evidence behind its values; harness/copilot.sh documents
# where that harness's contract is intentionally incomplete.
#
# The loader itself, and `harness_index_for_command`, moved out to
# harness-registry.sh (agent-dotfiles#215) so watchdog.sh could ask the same
# adapters instead of keeping its own Claude-only literal. Nothing about the
# arrays or the lookup changed in the move; only their location did. See that
# file for the arrays this defines and for why they are parallel INDEXED
# arrays rather than `declare -A`.
# shellcheck source=./harness-registry.sh
. "$LANES_HERE/harness-registry.sh"

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

# agent-dotfiles#201: the footer/menu/ready chrome that used to sit here as
# global constants (BLOCKED_MARKERS, OPTION_ROW_RE, MENU_ENTER_RE,
# MENU_TAIL_LINES, TEXT_PROMPT_RE, READY_RE) is now per-harness, sourced
# above from harness/*.sh into the H_* arrays. See harness/claude.sh for the
# real-Claude-Code-pane evidence those constants used to document in place
# (moved, not re-derived), and harness/codex.sh / harness/copilot.sh for
# what the other two harnesses need instead. `emit_rows` below looks up a
# window's own harness by its pane command and applies that harness's
# H_* entries; nothing in this file names a harness by string any more.

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
#
# #241 adds `#{window_id}` to the same call rather than a second one: the
# index and the id must describe the SAME window, and two calls could not
# guarantee that -- a window closing between them is the very race this is
# being read for. It is appended last so every existing positional read of
# this list keeps its column.
declare -a IDX NAME CMD ACTIVITY PANEMODE PANEPID WID
while IFS=$'\t' read -r w n c a m p wid; do
  [ -n "$w" ] || continue
  IDX+=("$w"); NAME+=("$n"); CMD+=("$c"); ACTIVITY+=("$a"); PANEMODE+=("$m"); PANEPID+=("$p"); WID+=("$wid")
done < <(tmux list-panes -s -t "$SESSION" -f '#{pane_active}' \
           -F "#{window_index}${TAB}#{window_name}${TAB}#{pane_current_command}${TAB}#{window_activity}${TAB}#{pane_in_mode}${TAB}#{pane_pid}${TAB}#{window_id}" 2>/dev/null)

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
    local w="${IDX[$i]}" name="${NAME[$i]}" cmd="${CMD[$i]}" act="${ACTIVITY[$i]}" mode="${PANEMODE[$i]}" pid="${PANEPID[$i]}" wid="${WID[$i]}"
    local pane state age pane_lines pane_tail hidx busy_tail target
    # #241: the captures below address the window by ID, not by index. The
    # list-panes call above and these two captures are separated by a whole
    # loop iteration per lane, and under `renumber-windows on` a window
    # closing in between shifts every higher index -- which would attribute
    # one lane's pane content to another lane's row. The id cannot shift.
    # Falls back to the index only if tmux gave no id, and says so when it
    # does. A target is never allowed to be empty -- an empty tmux target
    # hits the ACTIVE window, which is the incident dispatch.sh's own refusal
    # exists for -- so the fallback has to be to something, and the index is
    # what this file used before #241. It is a silent-regression path
    # otherwise, which is exactly the shape #241 is about, so it is loud.
    #
    # THE ONE WAY THIS FIRES IN PRACTICE is a truncated `list-panes` row.
    # `IFS=$'\t' read` collapses a RUN of tabs into one delimiter (a tab is
    # IFS whitespace), so any empty field above shifts every later one left
    # and empties this last variable. Real tmux never emits an empty field
    # for any of the six; a stub that did is what found this.
    if [ -z "$wid" ]; then
      echo "lanes: no window id for ${SESSION}:${w} -- addressing it by index, which is not stable under renumber-windows (#241)" >&2
    fi
    target="$SESSION:${wid:-$w}"
    # ONLY the status line -- the last non-empty line of the visible pane.
    #
    # This used to grep `capture-pane -S -6`, which is six scrollback lines
    # PLUS the whole visible pane (~60 lines). Any lane that had merely
    # PRINTED the phrase -- reviewing this very file, reading loop-tick.md --
    # matched and was reported busy, then hung. Two live lanes were in that
    # state during the review that found it. An earlier comment here blamed
    # the Copilot harness; that was a misdiagnosis. It was never
    # harness-specific, it was the capture window.
    pane_lines=$(tmux capture-pane -p -t "$target" 2>/dev/null | grep -v '^[[:space:]]*$')
    pane=$(tail -1 <<<"$pane_lines")

    # A SECOND, separate capture, and deliberately not a widening of the one
    # above. `-e` keeps the SGR attributes that input-box.sh needs to tell a
    # dim placeholder from typed text, and it is read ONLY by input_box_state,
    # which anchors on a marker (`❯` + NO-BREAK SPACE) that nothing but the
    # live input box paints. The status-line probes keep reading exactly one
    # line, which is the #65 discipline; this does not relax it, and taking a
    # second capture rather than reusing one keeps that impossible to blur.
    box=$(tmux capture-pane -pe -t "$target" 2>/dev/null | input_box_state)

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
    elif ! hidx=$(harness_index_for_command "$cmd"); then
      # agent-dotfiles#201: no adapter's HARNESS_COMMAND_RE claims this pane's
      # command -- a harness this file has never been shown (opencode does
      # not even hold a pane long enough to reach here), or a plain shell
      # variant SHELLS above did not already catch. Report what is known and
      # refuse to invent the rest, same posture the pre-#201 Claude-only
      # check had for "not Claude".
      state=unknown
    elif { [ -n "${H_BLOCKED_MARKERS[$hidx]}" ] && grep -qE "${H_BLOCKED_MARKERS[$hidx]}" <<<"$pane"; } \
      || { [ -n "${H_OPTION_ROW_RE[$hidx]}" ] && grep -qE "${H_OPTION_ROW_RE[$hidx]}" <<<"$pane"; }; then
      # An interactive prompt, not idle: the agent is waiting on a human, not
      # on work. Must be decided before free, since that is the classification
      # it is stealing from. Match the harness's own footer chrome, not the
      # option text -- that is model-authored and varies per prompt. And match
      # only the status line above, the same fix #65 required for the busy
      # probe: an exact-match sweep over scrollback caught a lane merely
      # displaying the phrase, not showing it.
      #
      # #159: which KIND of prompt matters to a caller deciding whether free
      # text is a safe thing to type into it -- see each harness file's
      # HARNESS_MENU_ENTER_RE. #164: `menu-blocked` is the default for
      # anything blocked that this probe cannot positively place -- see
      # HARNESS_TEXT_PROMPT_RE for why. `text-blocked` requires its own
      # positive marker, same posture as `free`'s whitelist since #126:
      # observed evidence adds a case, the absence of evidence never removes
      # one -- so absence of MENU evidence no longer defaults to text.
      pane_tail=$(tail -n "${H_MENU_TAIL[$hidx]}" <<<"$pane_lines")
      if { [ -n "${H_MENU_ENTER_RE[$hidx]}" ] && grep -qE "${H_MENU_ENTER_RE[$hidx]}" <<<"$pane"; } \
        || { [ -n "${H_OPTION_ROW_RE[$hidx]}" ] && grep -qE "${H_OPTION_ROW_RE[$hidx]}" <<<"$pane_tail"; }; then
        state=menu-blocked
      elif [ -n "${H_TEXT_PROMPT_RE[$hidx]}" ] && grep -qE "${H_TEXT_PROMPT_RE[$hidx]}" <<<"$pane"; then
        state=text-blocked
      else
        state=menu-blocked
      fi
    elif { busy_tail=$(tail -n "${H_BUSY_TAIL[$hidx]}" <<<"$pane_lines"); [ -n "${H_BUSY_RE[$hidx]}" ] && grep -qE "${H_BUSY_RE[$hidx]}" <<<"$busy_tail"; }; then
      # Busy-looking. Hung iff tmux has seen no output for HUNG_AFTER.
      #
      # This deliberately does NOT diff pane text across a short gap. That was
      # the first version and it was wrong: Claude Code's elapsed footer shows
      # minute granularity past 60s, so a turn running 61-119s prints an
      # identical byte string for a whole minute and was reported hung while
      # fully alive. Found in review of #65. tmux's own activity timestamp is
      # independent of whatever the harness chooses to paint.
      #
      # HARNESS_BUSY_TAIL is the harness's own bound on how far back this
      # looks -- 1 (last line only, the #65 discipline) for Claude and
      # Copilot, whose busy marker IS the last line; wider for Codex, whose
      # busy marker sits above a footer that never changes. See
      # harness/codex.sh for the real capture that shape is measured from.
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
    elif grep -qE "${H_READY_RE[$hidx]}" <<<"$pane"; then
      state=free
    else
      # #126: the old code fell through to `free` here. Everything that is
      # not busy, hung, blocked, dead, scrolled, or another harness used to
      # count as available -- including a lane that had delegated to a
      # background subagent and was nowhere near done. Default is now
      # unknown: nothing lands here unless it positively matches the
      # harness's own HARNESS_READY_RE.
      state=unknown
    fi
    # #241 appends the window id as a FIFTH column rather than inserting it,
    # so every awk expression below keeps the field numbers it already had.
    printf '%s\t%s\t%s\t%s\t%s\n' "$w" "$name" "$cmd" "$state" "$wid"
  done
}

rows=$(emit_rows)

case "$MODE" in
  --free)
    # Never the window name: names are not unique (the live session briefly
    # had two windows both called ad65-lanes-review) and `send-keys -t
    # session:name` silently hits the first match.
    #
    # TWO tab-separated columns since #241, the same shape `--blocked` below
    # has carried since #159:
    #
    #   1. session:index -- the LANE, the identity `cli.py lane-free`,
    #      `record-dispatch` and every operator recovery command key on. It
    #      names a slot, and a slot has to outlive the window sitting in it.
    #   2. session:@id   -- the TMUX TARGET, what `rename-window`, `send-keys`
    #      and `capture-pane` must be given. Stable for the window's lifetime
    #      and never reused, so it cannot come to mean another pane between
    #      the moment this line is printed and the moment it is used.
    #
    # #241 is that second column's whole reason: under `renumber-windows on`
    # (this server's setting) closing any window shifts every higher INDEX
    # down by one, so a target resolved here and used after a close addresses
    # a different pane. dispatch.sh reads both; a human running this by hand
    # reads the first.
    awk -F'\t' -v s="$SESSION" '$4=="free"{print s ":" $1 "\t" s ":" $5}' <<<"$rows" ;;
  --blocked)
    # Same session:index shape as --free, for the same reason, plus a second
    # tab-separated field naming the KIND (#159) -- `menu` or `text`. This is
    # the routing table agent-dotfiles#142/#159 build inbound Telegram
    # delivery on: a lane in `menu-blocked` or `text-blocked` is waiting on
    # an interactive prompt, and Jon's reply is presumed to be the answer to
    # it, but only a `text` lane can safely receive it as typed text --
    # inbox-route.sh needs the kind to decide that, not just the lane.
    awk -F'\t' -v s="$SESSION" '
      $4=="menu-blocked"{print s ":" $1 "\tmenu"}
      $4=="text-blocked"{print s ":" $1 "\ttext"}
    ' <<<"$rows" ;;
  --json)
    # #241: both identities, because a JSON consumer may be doing either job.
    # `window` is the index (unchanged, still a number, so no existing reader
    # breaks); `window_id` is the tmux handle to address it with.
    printf '['
    awk -F'\t' 'BEGIN{c=0}
      {if(c++)printf(",");printf("{\"window\":%s,\"window_id\":\"%s\",\"name\":\"%s\",\"command\":\"%s\",\"state\":\"%s\"}",$1,$5,$2,$3,$4)}
      END{}' <<<"$rows"
    printf ']\n' ;;
  *)
    # THE TABLE PRINTS THE INDEX AND NOTHING ELSE (#241). Jon reads the tmux
    # window list by index and this table beside it; adding a `@12` column
    # would change that reading to buy nothing -- no machine consumes this
    # renderer, and the two that do (`--free`, `--json`) carry the id
    # already. `dispatch.sh`'s window-name map parses this output positionally
    # and its keys must stay numeric, which is a second reason not to widen it.
    printf '%-4s %-24s %-12s %s\n' WINDOW NAME COMMAND STATE
    awk -F'\t' '{printf("%-4s %-24s %-12s %s\n",$1,$2,$3,$4)}' <<<"$rows"
    dead=$(awk -F'\t' '$4=="dead"' <<<"$rows" | wc -l | tr -d ' ')
    hung=$(awk -F'\t' '$4=="hung"' <<<"$rows" | wc -l | tr -d ' ')
    # #159: menu-blocked and text-blocked are counted together here -- this
    # line answers "how many lanes need a human", the same question it
    # answered before the split. Which kind matters to inbox-route.sh, not to
    # someone scanning the table for whether to look.
    blocked=$(awk -F'\t' '$4=="menu-blocked" || $4=="text-blocked"' <<<"$rows" | wc -l | tr -d ' ')
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
