#!/bin/bash
# Record, AT DISPATCH TIME, that THIS PANE'S OWN LANE has begun a piece of
# work -- before any commit or PR exists to falsely claim credit for.
#
# WHY THIS EXISTS (agent-supervisor#553, docs/decisions/0009-estate-lane-
# authorship-dispatch-time-only.md). `register-pr-dispatch-self.sh`
# (agent-supervisor#538/#539) recorded authorship the other direction --
# AFTER a PR existed, verified by comparing the pane's own worktree against
# the PR's real branch/commit. That comparison failed in both directions in
# the same review cycle: a fabricated local branch sharing a name passed it
# (agent-supervisor#552... no, estate:2's original attack), and then a
# GENUINE author whose pane is anchored in a DIFFERENT repo than the one it
# committed to (a cross-repo estate lane, agent-supervisor#552) was refused
# by it -- the tool's own reviewer could not use it on the PR that
# introduced it. 0009's decision: retire that direction entirely. Record
# the OPPOSITE moment instead -- not "prove this worktree has the right
# commits" (a fact about content, checkable only after content exists), but
# "did the dispatcher hand this lane this piece of work before any commit
# existed" (a fact about assignment, checkable at the one moment nothing
# has been produced yet to falsely take credit for).
#
# THE TRUST MODEL, stated explicitly because 0009 asks for it: this is
# still the LANE calling a command about itself, same as
# register-pr-dispatch-self.sh was -- the estate-loop's own brief-dispatch
# mechanism (a director typing a brief-file path into an already-existing
# pane) lives outside this repo and could not be located during the
# council that led to 0009, so there is no dispatcher-side hook to record
# this at instead. What makes THIS self-report sound where the retired
# one was not: at the moment this script runs, no commit and no PR exists
# for the task it names. A lane that ran this dishonestly -- claiming a
# task it was not actually handed -- gains nothing yet, because there is
# nothing yet to attribute. The later step (register-pr-for-lane-self.sh)
# can only ever attach a PR to a task THIS lane already holds, per the
# ledger's own record made here, before the PR existed -- it never lets a
# lane assert whose task a PR belongs to at PR-time, which is exactly the
# self-attestation-about-content shape that failed twice.
#
# SAME $TMUX_PANE ANCHOR AS EVERY OTHER *-self.sh TOOL. Delegates to
# register-lane-self.sh, unmodified, for the lane identity itself -- this
# script adds exactly one new fact (a task has begun), never re-derives the
# lane, harness, or repo register-lane-self.sh already measured.
#
# Usage:
#   register-lane-dispatch-self.sh --task <slug> --summary <text> [--harness NAME]
#
# --task     a short slug naming this piece of work (e.g. derived from the
#            estate-loop brief's own filename). Required -- the pane cannot
#            know what it was asked to do without being told, same as
#            register-pr-dispatch-self.sh's --pr/--repo could not be
#            inferred either.
# --summary  one line describing the work, for the ledger's own record.
#            Required.
# --harness  override the harness inferred from the pane's own command; see
#            register-lane-self.sh's own flag of the same name.
#
# Exit 0   recorded, and the record was read back and confirmed against the
#          ledger (`cli.py lane-diagnostic`) before reporting success.
# Exit 1   refused, or the record could not be confirmed.
# Exit 2   usage error.
set -uo pipefail

usage() { sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; exit 2; }

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PYTHON="${AGENT_PYTHON_BIN:-python3}"
TMUX_BIN="${AGENT_TMUX_BIN:-tmux}"
STATE="${AGENT_SUPERVISOR_STATE_DIR:-${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}}"
REGISTER_SELF="${REGISTER_LANE_SELF_BIN:-$HERE/register-lane-self.sh}"

TASK=""
SUMMARY=""
HARNESS=""
while [ $# -gt 0 ]; do
  case "$1" in
    --task) TASK="${2:-}"; [ -n "$TASK" ] || usage; shift 2 ;;
    --summary) SUMMARY="${2:-}"; [ -n "$SUMMARY" ] || usage; shift 2 ;;
    --harness) HARNESS="${2:-}"; [ -n "$HARNESS" ] || usage; shift 2 ;;
    *) echo "register-lane-dispatch-self.sh: unrecognised argument '$1'" >&2; usage ;;
  esac
done
[ -n "$TASK" ] && [ -n "$SUMMARY" ] || usage

# --- the anchor: this process's OWN pane -----------------------------------
if [ -z "${TMUX_PANE:-}" ]; then
  echo "register-lane-dispatch-self.sh: refusing -- \$TMUX_PANE is not set, so this process cannot observe which pane it is in" >&2
  echo "register-lane-dispatch-self.sh: run this FROM the lane's own pane -- see invariant 10 and register-lane-self.sh's own header." >&2
  exit 1
fi
PANE="$TMUX_PANE"

# --- ensure the lane's own identity is registered and fresh first ----------
# Same discipline as register-pr-dispatch-self.sh: idempotent, and if the
# lane cannot register itself there is nothing to attach a dispatch record
# to.
REGISTER_ARGS=()
[ -n "$HARNESS" ] && REGISTER_ARGS+=(--harness "$HARNESS")
if ! REG_OUT=$("$REGISTER_SELF" "${REGISTER_ARGS[@]}" 2>&1); then
  echo "register-lane-dispatch-self.sh: refusing -- register-lane-self.sh could not confirm this pane's own lane identity:" >&2
  echo "$REG_OUT" >&2
  exit 1
fi
LANE=$(sed -n 's/.*registered and confirmed \([^ ]*\).*/\1/p' <<<"$REG_OUT" | head -1)
if [ -z "$LANE" ]; then
  echo "register-lane-dispatch-self.sh: refusing -- could not parse a lane id back out of register-lane-self.sh's own confirmation output:" >&2
  echo "$REG_OUT" >&2
  exit 1
fi

# --- re-read the pane's own facts, the same way register-lane-self.sh does,
# for the fields record-dispatch itself requires ------------------------
META=$("$TMUX_BIN" display-message -p -t "$PANE" \
  '#{pane_id}|#{pane_current_path}|#{pane_current_command}|#{socket_path}|#{session_created}|#{session_id}' 2>&1)
if [ $? -ne 0 ] || [ -z "$META" ] || [[ "$META" != *"|"* ]]; then
  echo "register-lane-dispatch-self.sh: refusing -- could not read pane $PANE off tmux: $META" >&2
  exit 1
fi
IFS='|' read -r PANE_ID PANE_PATH PANE_CMD SOCKET_PATH SESSION_CREATED TMUX_SESSION_ID <<<"$META"

if [ -z "$HARNESS" ]; then
  HARNESS=$("$PYTHON" -c '
import sys
sys.path.insert(0, sys.argv[1])
from adapter import HARNESS_COMMANDS
command = sys.argv[2]
hits = [h for h, commands in HARNESS_COMMANDS.items() if command in commands]
print(hits[0] if len(hits) == 1 else "")
' "$HERE" "$PANE_CMD")
fi
if [ -z "$HARNESS" ]; then
  echo "register-lane-dispatch-self.sh: refusing -- cannot tell which harness pane command '$PANE_CMD' is; pass --harness" >&2
  exit 1
fi

# --- the actual dispatch-time record ---------------------------------------
# agent-supervisor#553: --informal -- no --issue, no --pr. This IS the whole
# point: nothing exists yet for this record to falsely take credit for.
REC_OUT=$("$PYTHON" "$HERE/cli.py" --state-dir "$STATE" record-dispatch \
  --lane "$LANE" --task "$TASK" --summary "$SUMMARY" \
  --pane-id "$PANE_ID" --pane-path "$PANE_PATH" --command "$PANE_CMD" \
  --server-id "${SOCKET_PATH}:${SESSION_CREATED}" --session-id "$TMUX_SESSION_ID" \
  --harness "$HARNESS" --informal 2>&1)
REC_RC=$?
if [ "$REC_RC" -ne 0 ]; then
  echo "register-lane-dispatch-self.sh: refusing -- cli.py record-dispatch failed (exit $REC_RC): $REC_OUT" >&2
  exit 1
fi

# --- read it back: "the write returned 0" is not evidence -----------------
DIAG_OUT=$("$PYTHON" "$HERE/cli.py" --state-dir "$STATE" lane-diagnostic --lane "$LANE" 2>&1)
DIAG_RC=$?
if [ "$DIAG_RC" -ne 0 ]; then
  echo "register-lane-dispatch-self.sh: recorded but could NOT read the record back (exit $DIAG_RC): $DIAG_OUT" >&2
  exit 1
fi
if ! "$PYTHON" -c '
import json, sys
data = json.loads(sys.argv[1])
task = sys.argv[2]
sys.exit(0 if data.get("known") and data.get("task") == task else 1)
' "$DIAG_OUT" "$TASK"; then
  echo "register-lane-dispatch-self.sh: recorded but the read-back does not show task $TASK as this lane's open task -- treat this as unregistered: $DIAG_OUT" >&2
  exit 1
fi

echo "register-lane-dispatch-self.sh: recorded and confirmed -- lane $LANE now holds open task $TASK, no PR yet"
printf '%s\n' "$DIAG_OUT"
exit 0
