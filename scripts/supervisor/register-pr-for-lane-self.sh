#!/bin/bash
# Attach a just-opened PR to THIS PANE'S OWN LANE's already-open dispatch
# record -- the second half of dispatch-time authorship recording.
#
# WHY THIS EXISTS (agent-supervisor#553, docs/decisions/0009-estate-lane-
# authorship-dispatch-time-only.md). register-lane-dispatch-self.sh records,
# at the START of a piece of work, that this lane holds it -- before any PR
# exists. Once a PR does exist, something has to connect the two so
# `author_lane_for` (verdict-independence.sh, Path 3 -- `pr-task`) can find
# it. This script is that connection, and it is deliberately NOT a repeat
# of register-pr-dispatch-self.sh's retired mistake: that script took `--pr`
# on the caller's word and tried to VERIFY the claim against the pane's own
# worktree content (branch name, then commit SHA) -- both verifications
# failed in the same review cycle (agent-supervisor#552, a false negative
# for a cross-repo lane whose pane and whose work repo differ). This script
# verifies nothing about worktree content at all. It asks the ledger a
# different question entirely: "does THIS lane currently hold an open task
# it has not yet attached a PR story to" -- resolved server-side by
# `cli.py attach-pr-to-open-task` from `--lane` alone
# (`Ledger.get_open_task_for_lane`), never from a task id this script or its
# caller supplies. A lane can only ever attach a PR to ITS OWN currently-
# open task, recorded earlier by register-lane-dispatch-self.sh -- it
# structurally cannot name another lane's task, because there is no
# `--task` argument here to lie in.
#
# THIS CLOSES agent-supervisor#552 BY CONSTRUCTION, not by re-deriving a
# stronger content check: a cross-repo lane's pane cwd never enters this
# script at all. The only facts this script reads off the pane are its
# identity ($TMUX_PANE, via register-lane-self.sh) and, from the caller,
# which PR and which repo -- exactly the two facts a pane cannot observe
# about itself, same as register-pr-dispatch-self.sh's --pr/--repo could
# not be inferred either. Which worktree the pane's cwd happens to be in
# is irrelevant to this check and is never read.
#
# THE RESIDUAL GAP, restated per 0009's own instruction not to let a reader
# assume it: this still cannot prove the lane's OWN COMMITS are what the PR
# contains -- only that the lane the ledger already knows held this task
# before the PR existed is the one attaching it now. Dispatch-time timing
# closes a DIFFERENT gap than worktree-possession did (see this repo's own
# docs/decisions/0009-...md, "The residual gap already disclosed... still
# applies"): it answers "did the dispatcher hand this lane the work before
# any commit existed," not "does this worktree hold the right commits" --
# the latter question, and the reviewing-lane false-positive it enabled,
# does not even arise here, because nothing about worktree content is ever
# consulted.
#
# Usage:
#   register-pr-for-lane-self.sh --pr <N> --repo <owner/name>
#
# --pr       the PR number this lane just opened. Required.
# --repo     GitHub owner/name the PR was opened against. Required -- not
#            inferred, same reasoning register-pr-dispatch-self.sh's own
#            header gives for its own --repo.
#
# No --harness here, deliberately: this script never registers anything (see
# below), only confirms an existing registration -- there is no harness
# value for a flag to override.
#
# Exit 0   attached, and the attachment was read back and confirmed
#          (`cli.py pr-task`) before reporting success.
# Exit 1   refused -- no open task for this lane, or the record could not
#          be confirmed.
# Exit 2   usage error.
set -uo pipefail

usage() { sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; exit 2; }

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PYTHON="${AGENT_PYTHON_BIN:-python3}"
TMUX_BIN="${AGENT_TMUX_BIN:-tmux}"
STATE="${AGENT_SUPERVISOR_STATE_DIR:-${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}}"

PR=""
REPO=""
while [ $# -gt 0 ]; do
  case "$1" in
    --pr) PR="${2:-}"; [ -n "$PR" ] || usage; shift 2 ;;
    --repo) REPO="${2:-}"; [ -n "$REPO" ] || usage; shift 2 ;;
    *) echo "register-pr-for-lane-self.sh: unrecognised argument '$1'" >&2; usage ;;
  esac
done
[ -n "$PR" ] && [ -n "$REPO" ] || usage
[[ "$PR" =~ ^[0-9]+$ ]] || { echo "register-pr-for-lane-self.sh: refusing -- --pr must be a number, got '$PR'" >&2; exit 1; }

# --- the anchor: this process's OWN pane -----------------------------------
if [ -z "${TMUX_PANE:-}" ]; then
  echo "register-pr-for-lane-self.sh: refusing -- \$TMUX_PANE is not set, so this process cannot observe which pane it is in" >&2
  echo "register-pr-for-lane-self.sh: run this FROM the lane's own pane -- see invariant 10 and register-lane-self.sh's own header." >&2
  exit 1
fi
PANE="$TMUX_PANE"

# --- resolve the lane id, READ-ONLY -- deliberately NOT a call to
# register-lane-self.sh. -----------------------------------------------------
# register-lane-self.sh RE-REGISTERS (mints a fresh incarnation nonce every
# call, by design -- see its own header). Measured directly, not assumed:
# a fresh nonce makes `Ledger.register_lane`'s own incarnation-change logic
# CANCEL this lane's already-open task -- exactly the task this script
# exists to attach a PR to. Calling it here would make this script destroy
# its own prerequisite on every invocation. This script instead reads the
# pane's identity the same way register-lane-self.sh does internally
# (`tmux display-message`, the #187-safe explicit `-t "$PANE"` form) and
# confirms the EXISTING registration against the live server via
# `lane_identity.py` -- read-only, mints nothing, cancels nothing. A lane
# that has never registered at all (never ran register-lane-dispatch-self.sh
# or register-lane-self.sh) still refuses here, correctly -- this script
# does not register on a caller's behalf, only confirms.
META=$("$TMUX_BIN" display-message -p -t "$PANE" '#{pane_id}|#{session_name}:#{window_index}' 2>&1)
if [ $? -ne 0 ] || [ -z "$META" ] || [[ "$META" != *"|"* ]]; then
  echo "register-pr-for-lane-self.sh: refusing -- could not read pane $PANE off tmux: $META" >&2
  exit 1
fi
IFS='|' read -r LIVE_PANE LANE <<<"$META"
if [ "$LIVE_PANE" != "$PANE" ]; then
  echo "register-pr-for-lane-self.sh: refusing -- asked tmux about $PANE and it answered for $LIVE_PANE" >&2
  exit 1
fi
if [ -z "$LANE" ]; then
  echo "register-pr-for-lane-self.sh: refusing -- tmux returned an incomplete description of pane $PANE: '$META'" >&2
  exit 1
fi
ID_OUT=$("$PYTHON" "$HERE/lane_identity.py" --lane "$LANE" --state-dir "$STATE" --tmux-bin "$TMUX_BIN" 2>&1)
ID_RC=$?
if [ "$ID_RC" -ne 0 ]; then
  echo "register-pr-for-lane-self.sh: refusing -- lane $LANE is not a confirmed, live registration:" >&2
  echo "$ID_OUT" >&2
  echo "register-pr-for-lane-self.sh: run register-lane-dispatch-self.sh (or register-lane-self.sh) from this pane first." >&2
  exit 1
fi

# --- the attachment: resolved server-side from --lane alone ---------------
# No --task here on purpose -- see this file's own header. cli.py resolves
# this lane's own open task itself and refuses if there is none.
ATTACH_OUT=$("$PYTHON" "$HERE/cli.py" --state-dir "$STATE" attach-pr-to-open-task \
  --lane "$LANE" --repo "$REPO" --pr "$PR" 2>&1)
ATTACH_RC=$?
if [ "$ATTACH_RC" -ne 0 ]; then
  echo "register-pr-for-lane-self.sh: refusing -- cli.py attach-pr-to-open-task failed (exit $ATTACH_RC): $ATTACH_OUT" >&2
  exit 1
fi

# --- read it back: "the write returned 0" is not evidence -----------------
# Same discipline as every other *-self.sh tool -- confirm the row this
# just wrote is actually the row author_lane_for's Path 3 (pr-task) will
# find, through the exact lookup it uses.
PRTASK_OUT=$("$PYTHON" "$HERE/cli.py" --state-dir "$STATE" pr-task --repo "$REPO" --pr "$PR" 2>&1)
PRTASK_RC=$?
if [ "$PRTASK_RC" -ne 0 ]; then
  echo "register-pr-for-lane-self.sh: attached but could NOT read it back (exit $PRTASK_RC): $PRTASK_OUT" >&2
  exit 1
fi
if ! "$PYTHON" -c '
import json, sys
data = json.loads(sys.argv[1])
lane = sys.argv[2]
sys.exit(0 if data.get("known") and data.get("lane") == lane else 1)
' "$PRTASK_OUT" "$LANE"; then
  echo "register-pr-for-lane-self.sh: attached but the read-back does not show lane $LANE as $REPO#$PR's recorded author -- treat this as unattached: $PRTASK_OUT" >&2
  exit 1
fi

echo "register-pr-for-lane-self.sh: recorded and confirmed -- lane $LANE is now a resolvable author of $REPO#$PR"
printf '%s\n' "$PRTASK_OUT"
exit 0
