#!/bin/bash
# Dispatch one issue to one lane over `pi --mode rpc`. `dispatch.sh`'s sibling,
# not its replacement -- see agent-supervisor#160.
#
# WHY A SEPARATE SCRIPT: `dispatch.sh` is a tmux dispatcher end to end -- it
# picks a lane by scanning tmux panes (`lanes.sh`), types a brief into one
# with `send-keys`, and confirms delivery by watching the input box empty.
# None of that applies to a pi-rpc lane. `PiRPCAdapter` (agent-supervisor#58,
# landed in #61) is deliberately headless: it spawns its own `pi --mode rpc`
# subprocess per call and its `observe_lane` is a permanent no-op ("there is
# no pane to poll") -- so there is no tmux pane for `lanes.sh` to classify as
# free, no window to rename, no input box to watch empty. Threading a
# fundamentally paneless transport through a dispatcher built entirely around
# panes would mean adding an `if pi-rpc, skip this` branch to nearly every one
# of `dispatch.sh`'s ~20 steps -- which is how a dispatcher earns a second,
# divergent copy of itself inside one file. This script is that dispatcher's
# shape, kept separate on purpose; it reuses `claim.sh` and `worktree.sh`
# unchanged (the two steps that have nothing to do with tmux) and nothing
# else.
#
# WHY NOT A NEW harness/pi.sh: every file in harness/ exists to answer one
# question -- "what does an IDLE/BUSY/BLOCKED pane painted by this harness
# look like?" -- for `lanes.sh` and `harness-registry.sh` to classify a tmux
# pane by. A pi-rpc lane has no pane; there is nothing for a shape regex to
# match. `pi` sending a brief over tmux `send-keys` -- a real, already-working
# mode this repo does not touch -- is a `harness/pi.sh` candidate on its own
# merits, but that is a different transport for the same harness and out of
# #160's one-lane scope. "Which transport a lane speaks" is answered instead
# by the ledger's own `transport` column, exactly the way `cli.py`'s
# `adapter_for_harness` already picks `PiRPCAdapter` over `TmuxAdapter` by
# reading it (agent-supervisor#58) -- this script's job is only to write that
# column truthfully, via `register --transport pi-rpc`, and then hand off to
# the adapter that already knows what to do with it.
#
# FAIL CLOSED, EVERYWHERE. `register` below performs a REAL `pi --mode rpc`
# handshake (`PiRPCAdapter.register_lane` calls `get_state` on a live
# subprocess) -- if `pi` is missing, crashes, or never answers, this refuses
# and unwinds the claim and the worktree. `assign` then performs the REAL
# delivery: a blocking RPC `prompt` call that waits out the async
# agent_start -> ... -> agent_settled stream (see `pi_transport.py`'s module
# docstring) and only returns once the turn is genuinely done. Nothing in
# this script ever falls back to `tmux send-keys` -- there is no tmux
# handling here at all to fall back to. A broken RPC path means a failed
# `register` or `assign` call, a non-zero exit, and a claim released back to
# the pool; it never means a silently degraded dispatch.
#
# Usage:
#   dispatch-pi-rpc.sh <issue> <slug> <brief-file> <repo> [repo-path]
#
# <issue>      one GitHub issue number.
# <slug>       short reason; becomes part of the lane id, task id and the
#              worktree's branch name (`lane/<issue>-<slug>`), same
#              convention `dispatch.sh` uses.
# <brief-file> the worker's complete brief, sent by path (the agent has its
#              own file-read tool) -- same convention `dispatch.sh` uses,
#              same standing deliverable contract appended if missing.
# <repo>       OWNER/NAME, for `claim.sh` and the ledger's `--github` record.
# [repo-path]  the local checkout `worktree.sh` branches from; default $PWD.
#
# Exit 0 only once a real RPC turn has settled and the task is recorded
# complete. Exit non-zero on any refusal -- no free `pi` binary, an issue
# already claimed, a worktree that could not be built, a register or assign
# that could not reach `pi` -- and the issue's claim is released so it is not
# stranded looking taken.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PYTHON="${DISPATCH_PYTHON:-python3}"
CLI="$HERE/cli.py"

ISSUE="${1:-}"
SLUG="${2:-}"
BRIEF="${3:-}"
REPO="${4:-}"
REPO_PATH="${5:-$PWD}"

if [ -z "$ISSUE" ] || [ -z "$SLUG" ] || [ -z "$BRIEF" ] || [ -z "$REPO" ]; then
  echo "usage: dispatch-pi-rpc.sh <issue> <slug> <brief-file> <repo> [repo-path]" >&2
  exit 2
fi

[ -f "$BRIEF" ] || { echo "dispatch-pi-rpc: no brief file at $BRIEF" >&2; exit 1; }
BRIEF="$(cd "$(dirname "$BRIEF")" && pwd)/$(basename "$BRIEF")"

# Fail closed before anything else is touched: no `pi` on PATH means every
# step below would fail anyway, and this is the cheapest place to say so.
command -v pi >/dev/null 2>&1 || {
  echo "dispatch-pi-rpc: no 'pi' binary on PATH -- refusing rather than falling back to send-keys" >&2
  exit 1
}

# Same window-naming convention `dispatch.sh` uses (README/loop-tick.md):
# repo initials + issue + slug, so this lane and task are legible next to
# every tmux-dispatched one in `cli.py status`.
NAME_PART="${REPO##*/}"
if [[ "$NAME_PART" == *-* ]]; then
  PREFIX=$(tr '-' '\n' <<<"$NAME_PART" | cut -c1 | tr -d '\n')
else
  PREFIX="$NAME_PART"
fi
LABEL="${PREFIX}${ISSUE}-${SLUG}"
LANE="$LABEL"
TASK_ID="$LABEL"

# --- 0. host-pressure gate ---------------------------------------------------
# agent-supervisor#643: this script starts a new agent process (a `pi --mode
# rpc` subprocess) exactly like dispatch.sh's tmux flow does, but until now
# had NO host-pressure check of any kind -- measured directly (grep for
# "host-pressure" across scripts/supervisor turned up only dispatch.sh).
# host_pressure.py (this dispatch's Python port of daemon/internal/pressure,
# see its own module docstring) is used here, before claim.sh below, same
# "cheapest failure first" reason dispatch.sh's own comment gives: a refused
# dispatch must leave the estate exactly as it found it.
HOST_PRESSURE_OUT=$("$PYTHON" "$HERE/host_pressure.py"); HOST_PRESSURE_RC=$?
if [ "$HOST_PRESSURE_RC" -ne 0 ]; then
  echo "dispatch-pi-rpc: $HOST_PRESSURE_OUT -- NOT dispatching #$ISSUE" >&2
  exit 1
fi

# --- 1. claim the issue on GitHub, same tool dispatch.sh uses --------------
if ! "$HERE/claim.sh" take "$ISSUE" "$REPO" "$LANE"; then
  echo "dispatch-pi-rpc: could not claim #$ISSUE -- not dispatching" >&2
  exit 1
fi

# agent-supervisor#647: same fix as dispatch-claude-print.sh's own release_claim
# -- this used to be ungated, so a `claim.sh release` failure (its `gh api -X
# DELETE` call hitting a rate limit or network blip) left the issue claimed
# with nothing said about it. Reported the same way dispatch.sh's own
# release_claim has since #209, so the failure is never silent on any
# transport.
release_claim() {
  "$HERE/claim.sh" release "$ISSUE" "$REPO" >/dev/null 2>&1 \
    || echo "dispatch-pi-rpc: could not release the claim on #$ISSUE -- release it by hand: $HERE/claim.sh release $ISSUE $REPO" >&2
}

# --- 2. give the lane its own worktree, same tool dispatch.sh uses --------
WORKTREE_ERR=$(mktemp)
WORKTREE=$("$HERE/worktree.sh" new "${ISSUE}-${SLUG}" "$REPO_PATH" 2>"$WORKTREE_ERR")
WORKTREE_RC=$?
if [ "$WORKTREE_RC" -ne 0 ] || [ -z "$WORKTREE" ]; then
  echo "dispatch-pi-rpc: worktree.sh new failed for #$ISSUE in $REPO_PATH -- NOT dispatching" >&2
  sed 's/^/  /' "$WORKTREE_ERR" >&2
  rm -f "$WORKTREE_ERR"
  release_claim
  exit 1
fi
rm -f "$WORKTREE_ERR"

abort() {
  echo "dispatch-pi-rpc: $1" >&2
  "$HERE/worktree.sh" done "$WORKTREE" >/dev/null 2>&1
  release_claim
  exit 1
}

# --- 3. the standing deliverable contract, same text dispatch.sh appends --
CONTRACT_MARKER="<!-- dispatch:deliverable-contract -->"
if ! grep -qF "$CONTRACT_MARKER" "$BRIEF" 2>/dev/null; then
  cat >>"$BRIEF" <<EOF || abort "could not append the deliverable contract to $BRIEF -- #$ISSUE was NOT dispatched"

$CONTRACT_MARKER
## Delivering this work

Added by \`dispatch-pi-rpc.sh\` on every dispatch, not by the brief's author.

Unless this brief says otherwise, when you are finished:
**push your branch and open a PR**.
If you produced no code -- a review, an investigation, an options paper --
**post your findings as a comment** on the issue or PR the brief names.

Do not stop with the work only in your worktree. From outside, a lane that
finished without shipping is indistinguishable from a lane that did nothing:
unshipped work looks exactly like no work, and the worktree is temporary.
EOF
fi

# --- 4. register the lane -- a REAL pi --mode rpc handshake ---------------
# `PiRPCAdapter.register_lane` spawns `pi --mode rpc` in $WORKTREE and calls
# `get_state` to get a real session id back. This is the fail-closed gate:
# a `pi` that cannot start, crashes, or never answers makes THIS call fail,
# which is what a mutation test breaking the RPC path is expected to show.
REGISTER_OUT=$("$PYTHON" "$CLI" register \
  --lane "$LANE" \
  --target "pi-rpc:$LANE" \
  --harness pi \
  --transport pi-rpc \
  --repo "$WORKTREE" 2>&1)
REGISTER_RC=$?
if [ "$REGISTER_RC" -ne 0 ]; then
  abort "register failed -- pi RPC is not reachable in $WORKTREE, refusing rather than falling back to send-keys:
$REGISTER_OUT"
fi

# --- 5. record the work as a task, before any RPC delivery is attempted ---
# `assign` (next step) requires a `source_tasks` row already OPEN --
# `reconstruct-task` is that write, generic and unmarked (see its own
# comment in cli.py for why `reconstruct`'s hill90-supervisor:v1 gate does
# not apply here: this dispatch already confirmed the issue itself, in
# step 1, against GitHub directly).
SOURCE_URL="https://github.com/$REPO/issues/$ISSUE"
RECONSTRUCT_OUT=$("$PYTHON" "$CLI" reconstruct-task \
  --task "$TASK_ID" \
  --source-url "$SOURCE_URL" \
  --source-ref "$ISSUE" \
  --summary "#$ISSUE $SLUG; worktree=$WORKTREE; brief=$BRIEF" \
  --evidence "claimed by dispatch-pi-rpc.sh for lane $LANE" 2>&1)
RECONSTRUCT_RC=$?
if [ "$RECONSTRUCT_RC" -ne 0 ]; then
  abort "reconstruct-task failed -- #$ISSUE was NOT dispatched:
$RECONSTRUCT_OUT"
fi

# --- 6. deliver -- a REAL, blocking pi RPC prompt call ---------------------
# `assign` routes to `PiRPCAdapter.assign_task` because the lane it just
# registered carries transport=pi-rpc: it re-spawns `pi --mode rpc --session
# <id>` against $WORKTREE, sends this message as a `prompt`, and blocks
# until `agent_settled` -- there is no tmux pane and no send-keys anywhere in
# this call. A dead process, a timeout, or a connection close before settle
# all raise, `assign` exits non-zero, and this script refuses exactly like
# every step above it -- never a silent report of success.
MESSAGE="Read $BRIEF and do exactly what it says. That file is your complete brief. Do all of your work in the worktree at $WORKTREE -- it is yours, already branched; never work in the shared checkout at $REPO_PATH."
ASSIGN_OUT=$("$PYTHON" "$CLI" assign \
  --lane "$LANE" \
  --task "$TASK_ID" \
  --summary "$MESSAGE" 2>&1)
ASSIGN_RC=$?
if [ "$ASSIGN_RC" -ne 0 ]; then
  abort "assign failed -- pi RPC did not deliver #$ISSUE, refusing rather than falling back to send-keys:
$ASSIGN_OUT"
fi

echo "dispatch-pi-rpc: #$ISSUE delivered to $LANE over pi-rpc, task $TASK_ID complete"
echo "  worktree: $WORKTREE"
echo "  brief:    $BRIEF"
echo "$ASSIGN_OUT"
exit 0
