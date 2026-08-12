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
# Usage: lane-done.sh <window-index> <expected-name> <channel> [session]
#
# <window-index>  the lane's tmux window index, e.g. what dispatch.sh sent
#                 the brief to.
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
# Exit 0 only after a successful rename.
# Exit 1 if the channel was never signaled, or if the window no longer
#   carries <expected-name> when it was.
# `tmux wait-for` itself has no timeout (SPEC §14.2 L3): a worker that
# crashes or wedges before reaching its final action leaves this blocked
# forever, same as the underlying mechanism today. That is an accepted,
# already-documented limit of `wait-for`, not a new one introduced here.

set -uo pipefail

IDX="${1:-}"
EXPECTED_NAME="${2:-}"
CHANNEL="${3:-}"
SESSION="${4:-${LANES_SESSION:-agent-dotfiles}}"

if [ -z "$IDX" ] || [ -z "$EXPECTED_NAME" ] || [ -z "$CHANNEL" ]; then
  sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2
  exit 2
fi

if ! tmux wait-for "$CHANNEL" 2>/dev/null; then
  echo "lane-done: channel '$CHANNEL' was not signaled -- not renaming ${SESSION}:${IDX}" >&2
  exit 1
fi

CURRENT="$(tmux display-message -p -t "${SESSION}:${IDX}" '#{window_name}' 2>/dev/null)"
if [ "$CURRENT" != "$EXPECTED_NAME" ]; then
  echo "lane-done: ${SESSION}:${IDX} is now '$CURRENT', not '$EXPECTED_NAME' -- already handled, not renaming. If this lane is actually stranded, recover with: cli.py record-completion --task '$EXPECTED_NAME'" >&2
  exit 1
fi

# agent-dotfiles#174 test 5 ("the rename is cosmetic") is NOT yet true here,
# tracked as a deliberate deferral in agent-dotfiles#194: this rename is
# still `|| exit 1`, evaluated before the ledger release below, so a failed
# rename (e.g. the window closed) leaves the lane occupied in the ledger
# with no rename to retry by hand. Not fixed in #183/#174 because
# `test_lane_done.sh` has its own tested invariant -- no completion is ever
# recorded for a rename that did not happen -- that a reorder would need to
# revisit under real tmux, which is a completion-path behavior change, not
# the availability-side change #174 scoped itself to. See #194.
tmux rename-window -t "${SESSION}:${IDX}" "free-${IDX}" || exit 1

# Record the completion (agent-dotfiles#140, updated by agent-dotfiles#174).
# BEST EFFORT, NEVER FATAL, but for a different reason now that dispatch.sh
# reads this record to decide what is free: the rename above already
# happened, so the lane IS free in reality regardless of whether this write
# lands. If it fails, the ledger keeps showing this lane's last task open --
# dispatch.sh's fail-closed read (#174) then treats the lane as occupied and
# simply never offers it again, rather than risk mistakenly offering a lane
# that is not actually free. That is the safe direction to be wrong in, which
# is why this stays best-effort rather than turning a genuine completion into
# a reported failure. Loud on failure, never silent.
#
# Runs AFTER the rename, so the only completion ever recorded is one that
# actually released the lane. The task id is the window name dispatch.sh set,
# which is what it recorded the task under.
#
# Not `cli.py complete`: that verifies $TMUX_PANE owns the lane and wants a
# --result-file. This script runs in the supervisor's pane and holds no result
# artifact -- the fact it has is that the worker's channel fired.
if ! LEDGER_OUT=$("${LANE_DONE_PYTHON:-python3}" \
    "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/cli.py" \
    record-completion --task "$EXPECTED_NAME" \
    --note "lane-done: ${CHANNEL} signaled; ${SESSION}:${IDX} renamed to free-${IDX}" 2>&1); then
  echo "lane-done: LEDGER RECORD FAILED for $EXPECTED_NAME -- the lane IS free, the record is not written" >&2
  sed 's/^/  /' <<<"$LEDGER_OUT" >&2
fi

exit 0
