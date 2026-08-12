#!/bin/bash
# Rename a lane back to free-N when its worker signals completion -- the
# second half of the dispatch/rename convention that agent-dotfiles#102
# found missing.
#
# WHY: dispatch.sh performs the first half automatically -- claim, worktree,
# rename to the task name, send. Nothing performed the second half, so every
# completed task permanently removed one lane from the pool until a human
# noticed `dispatch.sh` refusing "no free lane" and renamed by hand. That
# happened twice in one evening, 2026-08-11 (#102).
#
# WHY tied to the wait-for signal and nothing else: `lanes.sh` cannot tell
# "finished" from "idle between tool calls" or "blocked on an approval
# prompt holding an unposted verdict" -- all three read identically to
# `capture-pane`. Reclaiming on idle-alone was tried against a live lane the
# same night and nearly destroyed a verdict (#102). The worker's
# `tmux wait-for -S <channel>` is the one signal that cannot fire early: it
# is the brief's literal last instruction, sent only after everything else
# -- including posting a verdict -- is done (SPEC §14.1). Blocking on
# bare `wait-for` and renaming only when it returns needs no pane inspection
# at all, so it cannot mistake an approval prompt for completion.
#
# WHY the bare form and not `-L`: they are two different mechanisms, and only
# the bare form is the counterpart of the worker's `-S` (#108). `-L` locks a
# channel and blocks only against other *lockers*, released by `wait-for -U`;
# locking a never-locked channel succeeds immediately, and `-S` does not
# release a client blocked in `-L` at all. Verified against tmux 3.5:
#
#   $ timeout 3 tmux wait-for -L never-touched; echo $?
#   0                      # returns at once; nobody ever ran -S
#   $ timeout 3 tmux wait-for never-touched-2; echo $?
#   124                    # bare form blocks, as intended
#
# This script shipped with `-L` first, which made it rename every lane on the
# first call whether or not its worker had finished -- worse than the leak it
# was written to fix. tests/supervisor/test_lane_done.sh now covers the
# pairing against real tmux, not only against the stub.
#
# Usage: lane-done.sh <window> <expected-name> <channel> [session]
#
# <window>        the lane's tmux WINDOW ID (`@12`) -- what `dispatch.sh`
#                 prints as `target:` and what `lanes.sh --free` emits in its
#                 second column. A bare window INDEX is still accepted, for
#                 the operator typing this by hand off the window list, but it
#                 is not what a script should pass (#241).
#
#                 WHY THE ID (#241): this script is the longest-lived resolved
#                 target in the estate. It blocks on `wait-for` for as long as
#                 the work takes -- hours -- and this server runs
#                 `renumber-windows on`, so any window closing in the meantime
#                 shifts every higher index down by one. An index resolved at
#                 dispatch and used when the channel fires can name a
#                 different window entirely, and the name-match guard below
#                 then refuses a lane that genuinely finished: the rename is
#                 lost and the ledger release with it, which is #102's shape.
#                 A window id is stable for the window's lifetime and never
#                 reused, so it still names the same window when the signal
#                 arrives -- or names nothing at all, if that window closed,
#                 which is the honest answer rather than a stranger's.
# <expected-name> the task name dispatch.sh set on that window, e.g.
#                 `ad102-lane-rename-on-completion`. Renaming is refused if
#                 the window carries any other name when the signal arrives
#                 -- someone already handled it, or the lane was redispatched
#                 while this waiter was still up, and renaming now would
#                 steal the name out from under new work.
# <channel>       the wait-for channel named in the worker's brief. Nothing
#                 enforces uniqueness -- tmux channels are a flat global
#                 namespace on the server, and any `-S` on the same string
#                 releases this waiter, whoever sent it. Keep the convention
#                 every example here uses and tie the channel to the issue
#                 number (`ad102-done`), which makes collisions as unlikely
#                 as duplicate issue numbers.
#
# Run this BACKGROUNDED (Bash tool `run_in_background`, or `&` from a
# script) immediately after a successful dispatch, so the supervisor's tick
# stays free while it waits -- SPEC §14.3's fast path.
#
# Exit 0 once the signal is confirmed and the name still matches, whether or
#   not the rename itself succeeds (agent-dotfiles#194) -- see the ledger
#   release and rename-window comments below for why the rename is cosmetic
#   now, not the release condition.
# Exit 1 if the channel was never signaled, or if the window no longer
#   carries <expected-name> when it was.
# `tmux wait-for` itself has no timeout (SPEC §14.2 L3): a worker that
# crashes or wedges before reaching its final action leaves this blocked
# forever, same as the underlying mechanism today. That is an accepted,
# already-documented limit of `wait-for`, not a new one introduced here.

set -uo pipefail

WINDOW="${1:-}"
EXPECTED_NAME="${2:-}"
CHANNEL="${3:-}"
SESSION="${4:-${LANES_SESSION:-agent-dotfiles}}"

if [ -z "$WINDOW" ] || [ -z "$EXPECTED_NAME" ] || [ -z "$CHANNEL" ]; then
  sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2
  exit 2
fi

# One string, used for every tmux call below, built once (#241). `$WINDOW` is
# a window id (`@12`) from a script or an index from a human; `session:@12`
# and `session:5` are both valid tmux window targets, so no branch is needed
# to accept either -- what differs is only whether the target can drift, and
# that is the caller's choice to make correctly.
TARGET="${SESSION}:${WINDOW}"

if ! tmux wait-for "$CHANNEL" 2>/dev/null; then
  echo "lane-done: channel '$CHANNEL' was not signaled -- not renaming ${TARGET}" >&2
  exit 1
fi

CURRENT="$(tmux display-message -p -t "$TARGET" '#{window_name}' 2>/dev/null)"
if [ "$CURRENT" != "$EXPECTED_NAME" ]; then
  echo "lane-done: ${TARGET} is now '$CURRENT', not '$EXPECTED_NAME' -- already handled, not renaming. If this lane is actually stranded, recover with: cli.py record-completion --task '$EXPECTED_NAME'" >&2
  exit 1
fi

# agent-dotfiles#194: the ledger release is the authoritative operation now
# (agent-dotfiles#174 test 5, "the rename is cosmetic") -- it runs FIRST and
# unconditionally, so a failed rename below can no longer strand a finished
# lane in the ledger. Reordering this ahead of the rename means the note can
# no longer assert the rename already happened; it only asserts what this
# script actually knows at this point, which is that the signal fired for a
# window still carrying the expected name.
#
# Record the completion (agent-dotfiles#140, updated by agent-dotfiles#174,
# reordered by agent-dotfiles#194). BEST EFFORT, NEVER FATAL: dispatch.sh's
# fail-closed read (#174) treats a missing/failed record as "still occupied"
# and simply never offers the lane again, rather than risk offering one that
# is not actually free. That is the safe direction to be wrong in, which is
# why this stays best-effort rather than turning a genuine completion into a
# reported failure. Loud on failure, never silent. The task id is the window
# name dispatch.sh set, which is what it recorded the task under.
#
# Not `cli.py complete`: that verifies $TMUX_PANE owns the lane and wants a
# --result-file. This script runs in the supervisor's pane and holds no result
# artifact -- the fact it has is that the worker's channel fired.
if ! LEDGER_OUT=$("${LANE_DONE_PYTHON:-python3}" \
    "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/cli.py" \
    record-completion --task "$EXPECTED_NAME" \
    --note "lane-done: ${CHANNEL} signaled for ${TARGET}, still named '$EXPECTED_NAME'" 2>&1); then
  echo "lane-done: LEDGER RECORD FAILED for $EXPECTED_NAME -- the lane IS free, the record is not written" >&2
  sed 's/^/  /' <<<"$LEDGER_OUT" >&2
fi

# Rename back to free-N is now COSMETIC, not the release condition
# (agent-dotfiles#194): the ledger write above is what actually frees the
# lane, so a failed rename -- the window was closed, renamed by hand, the
# index moved -- must not undo or block that release. Loud on failure, not
# fatal, and NOT `|| exit 1` any more: `loop-tick.md`'s old "rename it back
# to free-N by hand" recovery is unnecessary once the ledger already
# released the lane, and would be actively wrong if attempted against a
# window meanwhile reused for other work.
#
# A lane released here but left misnamed IS still dispatchable: dispatch.sh's
# candidate list comes from `lanes.sh --free`, which classifies a lane from
# PANE content (agent idle and ready), not the window name, and `cli.py
# lane_free` answers a lane the ledger already knows purely from the ledger,
# ignoring the name entirely (see that function's docstring) -- the name
# convention only gates lanes the ledger has never seen. So this is a stated
# property, not an assumption: a stale name after a failed rename does not
# withhold the lane from dispatch.
#
# The `free-N` the window is renamed to takes N from the window's CURRENT
# index, read back here rather than from the argument (#241). Two reasons,
# and both are the same defect seen from either end: the argument is a window
# id now, so it carries no index to use; and even when a human passes an
# index, `renumber-windows on` means the window may have moved since -- the
# old code would then have named a window `free-7` while it sat at index 5,
# which is a projection that lies about the very thing Jon reads the window
# list for. If the index cannot be read the window is gone, and there is
# nothing to rename; the ledger release above has already happened either way.
FREE_IDX="$(tmux display-message -p -t "$TARGET" '#{window_index}' 2>/dev/null)"
if [ -z "$FREE_IDX" ]; then
  echo "lane-done: could not read the current window index of ${TARGET} -- the lane IS released in the ledger; the window was not renamed (still dispatchable -- see comment above)" >&2
elif ! tmux rename-window -t "$TARGET" "free-${FREE_IDX}"; then
  echo "lane-done: rename-window FAILED for ${TARGET} -- the lane IS released in the ledger; the window keeps its stale name '$EXPECTED_NAME' (still dispatchable -- see comment above)" >&2
fi

exit 0
