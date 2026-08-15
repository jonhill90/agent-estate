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
#   stale    no agent, and the window name still claims a task -> restore.sh,
#            and do not believe the name (#237)
#   broken   the pane's cwd no longer exists                  -> re-home it
#   service  a supervisor service, deliberately not a lane   -> leave alone
#   unknown  no probe recognizes the last line                -> ask a human
#            (a harness with no adapter under harness/, or a recognised
#            harness's shape that is not its adapter's enumerated ready
#            footer -- see harness/*.sh. #126: this is the default now, not
#            the exception -- see harness_index_for_command below.)
#   never-busy  same unrecognised shape as `unknown`, but for NEVER_BUSY_AFTER
#            seconds straight -- not "not yet classified", stuck since launch
#            -> needs a human, loudly (#112)
#
# #112 split `unknown` a second way. Both worker lanes in the estate sat at a
# Claude Code first-run dialog for FOUR HOURS -- `lanes.sh` correctly withheld
# them from `--free` as `unknown`, but nothing distinguished that from a pane
# merely mid-repaint, so nothing escalated and the estate ran with zero
# worker capacity in total silence. `unknown`'s ordinary case clears within a
# tick or two; a pane that has gone NEVER_BUSY_AFTER seconds with no repaint
# at all, still unrecognised, has done nothing since the window came up. See
# NEVER_BUSY_AFTER and is_never_busy below for the measurement, and
# watchdog.sh for how it reaches a human -- deliberately time-based, not a
# new dialog shape to pattern-match: #164 already spent one incident proving
# that a marker widened without a real capture is a guess.
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
# shellcheck source=./poller-window.sh
. "$LANES_HERE/poller-window.sh"
# shellcheck source=./session-defaults.sh
. "$LANES_HERE/session-defaults.sh"

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

SESSION="${2:-$(lanes_session_or_default)}"
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
# agent-supervisor#10: the window NAME the poller is deployed under, used
# ONLY as a narrow fallback below when its PANE'S PROCESS cannot answer this
# question at all -- see the comment at that fallback's one call site for
# why that gap exists and why trusting the name there does not reopen #237.
# The supervisor's own pane. It is never a dispatch target: sending a worker
# brief there /clear's the loop and replaces it with someone else's task.
# Done twice on 2026-08-11 -- once via an empty tmux target, once because
# --free cheerfully offered window 1 while the supervisor sat idle between
# ticks. "Free" and "yours to take" are different questions.
SUPERVISOR_WINDOW="${LANES_SUPERVISOR_WINDOW:-1}"
# agent-dotfiles#237: the shape `dispatch.sh` mints for a dispatched lane's
# window -- `<repo initials><issue number>-<slug>`, e.g. `ad180-status-accuracy`
# or `sk159-plugin-json` (dispatch.sh:157). A window name matching this is
# CLAIMING a task; every other name (`free-27`, `arch`, `telegram-poller`,
# anything hand-typed) claims nothing. Deliberately a POSITIVE match on the
# estate's own convention rather than "not free-N": the #126 whitelist
# posture, and it keeps every window this probe has never been shown reading
# exactly as it did before #237.
TASK_NAME_RE="${LANES_TASK_NAME_RE:-^[A-Za-z]+[0-9]+-}"
# A lane is hung if it looks busy but tmux has seen no output from it for this
# long. Must exceed the slowest legitimate repaint interval -- Claude Code's
# footer drops to MINUTE granularity past 60s, so a live turn can go ~60s
# without changing a single byte.
HUNG_AFTER="${LANES_HUNG_AFTER:-180}"

# agent-supervisor#112: `unknown` was doing two jobs. A pane whose shape this
# probe has never been taught (a marker gap) and a pane that has sat at a
# first-run dialog since the moment it launched, with no brief ever
# delivered, both fell through to the same word -- and #112 measured four
# hours of zero worker capacity hiding behind it. `unknown` is a whitelist
# miss and usually clears within a tick or two as the pane keeps repainting
# something, even a shape nothing here recognises; #{window_activity} keeps
# advancing as long as ANY byte on screen changes. A pane that has gone this
# long with NO repaint at all, while still unrecognised, has not merely
# escaped the whitelist -- it has done nothing since the window came up.
#
# Deliberately NOT a new marker to pattern-match. #164 already warns against
# that game: a marker widened without a real capture is a guess, and this
# issue's own first-run dialog is one dialog among an unbounded set this
# estate will never finish enumerating. Time is the signal that generalises
# across every shape nothing here has been shown, present or future.
#
# Larger than HUNG_AFTER on purpose: HUNG_AFTER catches a turn that stopped
# mid-flight, seconds after it was legitimately busy, and needs a tight
# bound. This catches a lane that never got that far -- a first-run dialog,
# an update prompt, anything sitting untouched since launch -- so the bound
# only needs to rule out a slow but normal startup, not a repaint hiccup.
NEVER_BUSY_AFTER="${LANES_NEVER_BUSY_AFTER:-1200}"

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
declare -a IDX NAME CMD ACTIVITY PANEMODE PANEPID WID PANE_PATH
while IFS=$'\t' read -r w n c a m p wid path; do
  [ -n "$w" ] || continue
  IDX+=("$w"); NAME+=("$n"); CMD+=("$c"); ACTIVITY+=("$a"); PANEMODE+=("$m"); PANEPID+=("$p"); WID+=("$wid"); PANE_PATH+=("$path")
done < <(tmux list-panes -s -t "$SESSION" -f '#{pane_active}' \
           -F "#{window_index}${TAB}#{window_name}${TAB}#{pane_current_command}${TAB}#{window_activity}${TAB}#{pane_in_mode}${TAB}#{pane_pid}${TAB}#{window_id}${TAB}#{pane_current_path}" 2>/dev/null)

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

# agent-supervisor#83: a pane whose status line still paints busy chrome
# (Claude's `↓ to manage`, a mid-turn footer, etc.) but has not repainted in
# HUNG_AFTER seconds is not necessarily wedged -- it may be idle at the
# prompt, correctly waiting for a long background command (a full test
# suite, a Monitor'd task) that it started and is still running. Measured
# live, #83: a lane that had posted "I will pause here and wait for the
# Monitor task notification" sat frozen for 8m35s and was reported `hung`,
# with the table's own warning ("a dispatch there would queue forever")
# emphatic enough that acting on it nearly stood the lane down mid-review.
#
# `#{window_activity}` -- what HUNG_AFTER's `age` already reads -- cannot
# distinguish this case from a genuinely wedged one: it is exactly what
# stopped advancing, on purpose, while the lane waits. What DOES distinguish
# them is the kernel's own process tree: a pane that is truly waiting on
# work it started still has that work running as a descendant of the pane's
# process, where a wedged one does not. That is a measurement neither tmux
# nor this system authored, so per CLAUDE.md's authorship test it may be
# read directly, the same posture `is_service_pane` above already takes with
# `ps -o args=`.
#
# Deliberately checked only on the path that is about to report `hung` --
# once a pane has already gone HUNG_AFTER seconds with no repaint -- not on
# every busy pane on every tick. A live turn's own elapsed footer already
# proves liveness without a `ps` call; this only has to answer the question
# for the rare pane that both looks busy and has stopped repainting.
#
# review of the first cut of this function (PR #92): `$pid` here is
# `#{pane_pid}`, tmux's handle on the pane's FIRST process -- per the module
# comment above and `is_service_pane`, that is the login shell, and the
# harness (claude.exe, codex, ...) runs as ITS direct child for as long as
# the harness is alive at all, busy or wedged. A version that asked "does
# $pid have ANY live child" was therefore true whenever the harness process
# had not exited, which is every genuinely wedged live agent too -- it
# narrowed `hung` to "the harness process itself died", not "the pane is
# waiting on work", making hung unreachable for exactly the frozen-but-alive
# shape it exists to catch. What actually distinguishes waiting from wedged
# is whether the HARNESS has live children of ITS OWN -- a grandchild of the
# pane -- since that is where a spawned test suite or Monitor'd task
# actually runs; the harness being alive is not that evidence.
pane_has_live_children() {
  local pid="${1:-}" tree kids k
  [ -n "$pid" ] || return 1
  tree=$(ps -o pid=,ppid= -ax 2>/dev/null) || return 1
  [ -n "$tree" ] || return 1
  kids=$(awk -v p="$pid" '$2==p{print $1}' <<<"$tree")
  for k in $kids; do
    awk -v p="$k" '$2==p{f=1} END{exit !f}' <<<"$tree" && return 0
  done
  return 1
}

# agent-supervisor#112. The one seam both `state=unknown` sites share below
# -- no adapter claims the pane's command at all, and an adapter claims it
# but nothing in it recognises the pane's shape -- narrowed the same way in
# both places, so the threshold cannot drift out of sync between them the
# way two independent `if`s eventually would.
#
# A PURE NARROWING of `unknown`, the same shape #141 used for `unsent` and
# #154/#237 used for `service`/`stale`: the only lane this can move is one
# that was already going to be called unknown, and the only place it can
# move it to is never-busy -- never free, never busy, never blocked. A
# caller that greps this file's output for `unknown` today keeps matching
# every lane it matched before; the four-hour case in #112 simply stops
# being one of them.
# as#149: this used to print the state itself (`state=$(never_busy_or_unknown
# "$age")`), which is a command substitution, not a bareword after `state=`.
# agent-tui's cross-repo guard (`states_lanessh_test.go`) parses this file
# with a regex that requires a literal `state=<word>` -- command substitution
# returns [] to that parser, so the guard would stay green without ever
# learning `never-busy` exists, the exact silent-pass failure it exists to
# catch. Keep this helper for the age decision only; callers assign the
# literal state themselves so every emitted state stays statically greppable.
is_never_busy() {
  local age="$1"
  [ "$age" -ge "$NEVER_BUSY_AFTER" ]
}

now_epoch=$(date +%s)

emit_rows() {
  local i
  for i in "${!IDX[@]}"; do
    local w="${IDX[$i]}" name="${NAME[$i]}" cmd="${CMD[$i]}" act="${ACTIVITY[$i]}" mode="${PANEMODE[$i]}" pid="${PANEPID[$i]}" wid="${WID[$i]}" path="${PANE_PATH[$i]}"
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

    # Computed once, ahead of the branches below, so both the hung check and
    # the never-busy check (#112) below read the SAME sample rather than two
    # tmux queries a scheduling gap could disagree on. Re-read at the bottom
    # for the printed row rather than trusted verbatim after: it is not
    # mutated in between, so re-derivation there is only ever the same
    # number, computed the cheap way (arithmetic, not a second tmux call).
    age=$(( now_epoch - ${act:-now_epoch} ))

    if [ "$w" = "$SUPERVISOR_WINDOW" ]; then
      state=supervisor
    elif [ "${mode:-0}" != "0" ]; then
      # A pane in copy mode still captures its underlying screen, so an idle
      # agent someone scrolled up reads free -- but keys sent there are eaten
      # by the copy-mode key table and never reach the agent. Reproduced live
      # against a real tmux server in review of #65: the dispatch vanished and
      # copy mode exited.
      state=scrolled
    elif [ -n "$path" ] && [ ! -d "$path" ]; then
      # agent-supervisor#88: a pane whose current working directory has been
      # removed cannot start another turn at all. It is not hung (there may
      # be no running turn), not dead (the harness process may still be
      # alive), and never free. The remedy is to re-home the pane into a
      # directory that exists, so report a distinct state rather than hide it
      # under one of the older buckets.
      state=broken
    elif [[ "$cmd" =~ ^($SHELLS)$ ]]; then
      # #154: a pure NARROWING of `dead`, the same shape #141 used for `free`.
      # The only lane this can move is one that was already going to be called
      # dead, and it can only move it to `service` -- no other state's
      # classification is reachable from here, so `--free`, `--blocked`, and
      # every existing row are untouched. See SERVICE_RE above for why the
      # signal is the pane's own process and not the window's name.
      # agent-dotfiles#237: a THIRD outcome for a pane running a shell, and
      # the same shape of narrowing #154 used above -- `stale` can only be
      # reached from `dead`, so no other state moves and `--free` is
      # untouched.
      #
      # `dead` was the correct answer during the #237 incident and it was not
      # ENOUGH. Nine panes came back as dead shells still wearing window
      # names from hours-finished work (`sk159-plugin-json`,
      # `ad180-status-accuracy`), an operator read those names as current,
      # and respawning them killed the live agents that had been resumed by
      # hand in the meantime. The name was confidently wrong and nothing in
      # the table said so.
      #
      # The name is read here for exactly ONE thing: to notice that this pane
      # is CLAIMING a task. It is never read to decide what the lane was
      # doing -- that answer lives in the ledger, which is what `restore.sh`
      # consults. A shell cannot be running the task its window advertises,
      # so a shell under a task-shaped name is a lie in the table, whoever
      # wrote it. `free-N` is the estate's "no claim" convention and stays
      # plain `dead`: nothing about it misleads.
      if is_service_pane "$pid"; then
        state=service
      elif is_poller_window_name "$name" && [ -n "$pid" ] && ! ps -p "$pid" >/dev/null 2>&1; then
        # agent-supervisor#10: poller-recover.sh now sets `remain-on-exit` on
        # this window, so a poller that exits leaves a DEAD pane here instead
        # of taking the window with it, for as long as it takes the next
        # watchdog tick (<=180s) to notice and respawn it. `is_service_pane`
        # cannot see through that gap -- its whole contract is reading the
        # pane's OWN process, and there is none: `pid` no longer names a
        # process at all, which is exactly what the `! ps -p "$pid"` guard
        # requires here rather than merely a process that isn't inbox-poll.sh.
        # Without this branch that gap reads plain `dead`, and dispatch.sh's
        # normal handling of `dead` is to launch a worker agent into it --
        # permanently handing the poller's window to a lane and leaving
        # nothing for poller-recover.sh to find on its next tick.
        #
        # Trusting the NAME here does not reopen #237, whose lesson was that
        # a LANE's name is a claim a human can leave stale long after the
        # work it names finished. This is not a claim: it is this estate's
        # one hardcoded service window, the same kind of exception the
        # `"$w" = "$SUPERVISOR_WINDOW"` branch above already makes for the
        # supervisor's own window by name, and it only ever promotes a pane
        # `is_service_pane` already couldn't rule OUT (no live process to
        # check) rather than overriding one it ruled positively something
        # else.
        state=service
      elif [[ "$name" =~ $TASK_NAME_RE ]]; then
        state=stale
      else
        state=dead
      fi
    elif ! hidx=$(harness_index_for_command "$cmd"); then
      # agent-dotfiles#201: no adapter's HARNESS_COMMAND_RE claims this pane's
      # command -- a harness this file has never been shown (opencode does
      # not even hold a pane long enough to reach here), or a plain shell
      # variant SHELLS above did not already catch. Report what is known and
      # refuse to invent the rest, same posture the pre-#201 Claude-only
      # check had for "not Claude".
      #
      # #112: this branch cannot even name a harness, so it is the least
      # informed classification in the file -- and it is exactly where a
      # lane can sit silently the longest, since no adapter is watching for
      # ANY shape here, recognised or not. is_never_busy below is the one
      # narrowing that applies to it.
      if is_never_busy "$age"; then
        state=never-busy
      else
        state=unknown
      fi
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
      #
      # #83: a frozen status line alone is not enough -- a pane can go
      # HUNG_AFTER seconds with no repaint because it is correctly idle at
      # the prompt, waiting on a background command it started (see
      # pane_has_live_children above for why window_activity cannot tell
      # these apart, and why the check belongs here rather than on every
      # tick). Only checked once the pane is already past HUNG_AFTER, so a
      # busy pane inside its normal window never pays the `ps` call. `age`
      # is the sample taken once, above the whole if/elif chain (#112).
      if [ "$age" -ge "$HUNG_AFTER" ] && ! pane_has_live_children "$pid"; then
        state=hung
      else
        state=busy
      fi
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
      #
      # #112: THIS is the branch the four-hour incident actually fell into --
      # a recognised harness (claude.exe) whose first-run dialog matched none
      # of ready/busy/blocked. is_never_busy narrows it exactly as it
      # narrowed the no-adapter branch above: a pane this old that has never
      # painted a recognised shape has not merely dodged the whitelist, it
      # has not done anything since it came up.
      if is_never_busy "$age"; then
        state=never-busy
      else
        state=unknown
      fi
    fi
    # #241 appends the window id as a FIFTH column rather than inserting it,
    # so every awk expression below keeps the field numbers it already had.
    #
    # agent-supervisor#36 appends idle_seconds, derived from tmux's own
    # `#{window_activity}` reading. A caller comparing ledger state to pane
    # state needs that measured pane age; a task timestamp this system wrote
    # would be a ledger record, not observable pane state.
    age=$(( now_epoch - ${act:-now_epoch} ))
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$w" "$name" "$cmd" "$state" "$wid" "$age"
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
      {if(c++)printf(",");printf("{\"window\":%s,\"window_id\":\"%s\",\"name\":\"%s\",\"command\":\"%s\",\"state\":\"%s\",\"idle_seconds\":%s}",$1,$5,$2,$3,$4,$6)}
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
    stale=$(awk -F'\t' '$4=="stale"' <<<"$rows" | wc -l | tr -d ' ')
    broken=$(awk -F'\t' '$4=="broken"' <<<"$rows" | wc -l | tr -d ' ')
    hung=$(awk -F'\t' '$4=="hung"' <<<"$rows" | wc -l | tr -d ' ')
    # #159: menu-blocked and text-blocked are counted together here -- this
    # line answers "how many lanes need a human", the same question it
    # answered before the split. Which kind matters to inbox-route.sh, not to
    # someone scanning the table for whether to look.
    blocked=$(awk -F'\t' '$4=="menu-blocked" || $4=="text-blocked"' <<<"$rows" | wc -l | tr -d ' ')
    unsent=$(awk -F'\t' '$4=="unsent"' <<<"$rows" | wc -l | tr -d ' ')
    unknown=$(awk -F'\t' '$4=="unknown"' <<<"$rows" | wc -l | tr -d ' ')
    never_busy=$(awk -F'\t' '$4=="never-busy"' <<<"$rows" | wc -l | tr -d ' ')
    [ "$dead" -gt 0 ] && echo "  ${dead} lane(s) have no agent — restart before dispatching"
    # #237: louder than `dead` on purpose. The danger here is not the missing
    # agent, it is the name, which reads as live work to anyone scanning the
    # table -- and acting on it is what turned one tmux server loss into two.
    [ "$stale" -gt 0 ] && echo "  ${stale} lane(s) have no agent but still carry a task name — the name is STALE, do not trust it; run restore.sh"
    [ "$broken" -gt 0 ] && echo "  ${broken} lane(s) have a missing cwd — re-home before dispatching"
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
    # #112: the four-hour incident. Never withheld silently like plain
    # `unknown` -- a lane this old that has never painted a recognised shape
    # needs a human, the same posture `hung`/`blocked`/`unsent` already take,
    # not just the passive whitelist exclusion `unknown` gets.
    [ "$never_busy" -gt 0 ] && echo "  ${never_busy} lane(s) have been up ${NEVER_BUSY_AFTER}s+ and have never gone ready or busy — stuck since launch, not merely unclassified; a human must look"
    : ;;
esac
