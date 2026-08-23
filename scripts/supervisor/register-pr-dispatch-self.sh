#!/bin/bash
# Register, retroactively, that THIS PANE'S OWN LANE authored a PR that
# already exists -- so the merge gate and restore.sh can resolve an identity
# for work the estate loop dispatched outside `dispatch.sh` entirely.
#
# WHY THIS EXISTS (agent-supervisor#538). The estate loop hands a lane a
# brief file (not a GitHub issue) and lets it build its own worktree by
# hand -- `dispatch.sh` is never called, so none of the five paths
# `author_lane_for` (verdict-independence.sh) tries ever find anything: no
# `source_tasks` row by issue, no `pr-task` row, no `contributor-pr-lanes`
# row, no branch-name fallback match. A correctly-reviewed PR then reads
# "independence unknown -- PR author lane unresolved" from `merge-pr.sh`
# forever (agent-supervisor#531), and `restore.sh` has no open task to
# resume from and no `lanes` row for the director itself (#532). Both are
# the same root cause: an estate-lane dispatch never registers an identity
# the ledger can resolve.
#
# WHY NOT JUST CALL `dispatch.sh`. Read its own header first. It picks a
# FREE lane from `lanes.sh`'s classification, claims a GitHub issue with
# `claim.sh take`, and builds the worktree itself (`worktree.sh new`) --
# every one of those steps assumes work starts from an open issue in a repo
# `dispatch.sh` is driving. An estate-loop brief is a local file, often with
# no issue behind it at all (this script's own PR, agent-supervisor#538,
# closes none), and the lane's worktree already exists by the time this
# runs. Calling `dispatch.sh` for this would mean inventing an issue to
# claim purely so the claim machinery has something to point at -- a fact
# nobody has, recorded as if it were one. That is exactly the kind of
# self-attested record `register-lane-self.sh`'s own header warns against.
# `cli.py record-dispatch`'s own docstring already describes recording
# "a dispatch that ALREADY happened" -- this script uses that tool for
# exactly the shape it says it supports, after the PR/worktree/lane already
# exist, instead of building a second claim-and-worktree pipeline that
# would duplicate `dispatch.sh` badly.
#
# WHAT THIS DOES NOT CHANGE. `--issue` on `cli.py record-dispatch` is now
# optional when `--pr` is given (see that flag's own comment) -- every
# existing `dispatch.sh` caller still passes both, unaffected. Nothing here
# touches `verdict.py`, the `Reviewed-SHA` freshness check, or
# `mark-pr-external.sh` -- an internal author is never laundered as
# external; this records the OPPOSITE fact, that the author genuinely was a
# lane, so `author_lane_for` can find it and the fail-closed refusal it
# would otherwise return correctly turns into a resolved contributor set.
#
# SAME SELF-ATTESTATION DISCIPLINE AS register-lane-self.sh. No flag names
# a lane, a worktree, or a pane from outside -- everything but `--pr` and
# `--repo` (the two facts this pane genuinely cannot observe about itself:
# which PR it opened, and which repo it is in without a `gh` round-trip
# this script deliberately does not make) is measured off `$TMUX_PANE`.
# `--repo` is required rather than inferred so this never guesses which of
# several repos a shared checkout's `origin` might actually mean.
#
# Usage:
#   register-pr-dispatch-self.sh --pr <N> --repo <owner/name> [--task <slug>] [--harness NAME]
#
# --pr       the PR number this lane's own worktree produced. Required.
# --repo     GitHub owner/name the PR was opened against. Required -- see
#            above for why this is not inferred from the pane's cwd.
# --task     a short slug for the ledger's task id. Default: a name derived
#            from the lane and PR (`estate-pr<PR>-<lane-safe>`), then run
#            through `cli.py record-dispatch`'s own de-duplication
#            (`_unique_redispatch_task_id`), same as `dispatch.sh`'s window
#            names.
# --harness  override the harness inferred from the pane's own command; see
#            register-lane-self.sh's own flag of the same name for why this
#            can only disambiguate, never assert a harness the pane is
#            visibly not running.
#
# Exit 0   recorded, and the record was read back and confirmed against the
#          ledger (`cli.py contributor-pr-lanes`) before reporting success.
# Exit 1   refused, or the record could not be confirmed.
# Exit 2   usage error.
set -uo pipefail

usage() { sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; exit 2; }

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PYTHON="${AGENT_PYTHON_BIN:-python3}"
TMUX_BIN="${AGENT_TMUX_BIN:-tmux}"
STATE="${AGENT_SUPERVISOR_STATE_DIR:-${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}}"
REGISTER_SELF="${REGISTER_LANE_SELF_BIN:-$HERE/register-lane-self.sh}"

PR=""
REPO=""
TASK=""
HARNESS=""
while [ $# -gt 0 ]; do
  case "$1" in
    --pr) PR="${2:-}"; [ -n "$PR" ] || usage; shift 2 ;;
    --repo) REPO="${2:-}"; [ -n "$REPO" ] || usage; shift 2 ;;
    --task) TASK="${2:-}"; [ -n "$TASK" ] || usage; shift 2 ;;
    --harness) HARNESS="${2:-}"; [ -n "$HARNESS" ] || usage; shift 2 ;;
    *) echo "register-pr-dispatch-self.sh: unrecognised argument '$1'" >&2; usage ;;
  esac
done
[ -n "$PR" ] && [ -n "$REPO" ] || usage
[[ "$PR" =~ ^[0-9]+$ ]] || { echo "register-pr-dispatch-self.sh: refusing -- --pr must be a number, got '$PR'" >&2; exit 1; }

# --- the anchor: this process's OWN pane, exactly register-lane-self.sh's
# check, duplicated rather than sourced so this script's exit codes stay its
# own and a change to one does not silently reshape the other's contract.
if [ -z "${TMUX_PANE:-}" ]; then
  echo "register-pr-dispatch-self.sh: refusing -- \$TMUX_PANE is not set, so this process cannot observe which pane it is in" >&2
  echo "register-pr-dispatch-self.sh: run this FROM the lane's own pane -- see invariant 10 and register-lane-self.sh's own header." >&2
  exit 1
fi
PANE="$TMUX_PANE"

# --- ensure the lane's own identity is registered and fresh first --------
# Idempotent (register_lane upserts) -- re-running this after a tmux restart
# or a stale nonce is the documented remedy, same as register-lane-self.sh's
# own header says about itself. If the lane cannot register itself, there is
# nothing for this script to attach a PR record to.
REGISTER_ARGS=()
[ -n "$HARNESS" ] && REGISTER_ARGS+=(--harness "$HARNESS")
if ! REG_OUT=$("$REGISTER_SELF" "${REGISTER_ARGS[@]}" 2>&1); then
  echo "register-pr-dispatch-self.sh: refusing -- register-lane-self.sh could not confirm this pane's own lane identity:" >&2
  echo "$REG_OUT" >&2
  exit 1
fi
LANE=$(sed -n 's/.*registered and confirmed \([^ ]*\).*/\1/p' <<<"$REG_OUT" | head -1)
if [ -z "$LANE" ]; then
  echo "register-pr-dispatch-self.sh: refusing -- could not parse a lane id back out of register-lane-self.sh's own confirmation output:" >&2
  echo "$REG_OUT" >&2
  exit 1
fi

# --- re-read the pane's own facts, the same way register-lane-self.sh does,
# for the fields record-dispatch itself requires ------------------------
META=$("$TMUX_BIN" display-message -p -t "$PANE" \
  '#{pane_id}|#{pane_current_path}|#{pane_current_command}|#{socket_path}|#{session_created}|#{session_id}' 2>&1)
if [ $? -ne 0 ] || [ -z "$META" ] || [[ "$META" != *"|"* ]]; then
  echo "register-pr-dispatch-self.sh: refusing -- could not read pane $PANE off tmux: $META" >&2
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
  echo "register-pr-dispatch-self.sh: refusing -- cannot tell which harness pane command '$PANE_CMD' is; pass --harness" >&2
  exit 1
fi

WORKTREE=$(cd "$PANE_PATH" 2>/dev/null && pwd -P) || WORKTREE="$PANE_PATH"

if [ -z "$TASK" ]; then
  LANE_SLUG=$(tr -c 'A-Za-z0-9' '-' <<<"$LANE")
  TASK="estate-pr${PR}-${LANE_SLUG}"
fi

# --- the actual retroactive record ----------------------------------------
# agent-supervisor#538: `--issue` omitted -- allowed now that `--pr` is
# given (see cli.py's own comment on that flag). This is a `source_kind=
# 'pull'` `source_tasks` row exactly like a `dispatch.sh --pr` caller
# produces, keyed by `$PR`, which is what `author_lane_for`'s Path 4
# (`contributor-pr-lanes`) reads.
REC_OUT=$("$PYTHON" "$HERE/cli.py" --state-dir "$STATE" record-dispatch \
  --lane "$LANE" --task "$TASK" \
  --summary "PR #$PR ($REPO) self-registered by lane $LANE via register-pr-dispatch-self.sh" \
  --pane-id "$PANE_ID" --pane-path "$PANE_PATH" --command "$PANE_CMD" \
  --server-id "${SOCKET_PATH}:${SESSION_CREATED}" --session-id "$TMUX_SESSION_ID" \
  --harness "$HARNESS" --github "$REPO" --worktree "$WORKTREE" --pr "$PR" 2>&1)
REC_RC=$?
if [ "$REC_RC" -ne 0 ]; then
  echo "register-pr-dispatch-self.sh: refusing -- cli.py record-dispatch failed (exit $REC_RC): $REC_OUT" >&2
  exit 1
fi

# --- read it back: "the write returned 0" is not evidence -----------------
# Same discipline as register-lane-self.sh's own read-back via
# lane_identity.py -- confirm the row this just wrote is actually the row
# `merge-pr.sh` will find, through the exact lookup it uses
# (`author_lane_for`'s Path 4), not just trust the write's exit code.
CONTRIB_OUT=$("$PYTHON" "$HERE/cli.py" --state-dir "$STATE" contributor-pr-lanes --pr "$PR" --repo "$REPO" 2>&1)
CONTRIB_RC=$?
if [ "$CONTRIB_RC" -ne 0 ]; then
  echo "register-pr-dispatch-self.sh: recorded but could NOT read the record back (exit $CONTRIB_RC): $CONTRIB_OUT" >&2
  exit 1
fi
if ! "$PYTHON" -c '
import json, sys
data = json.loads(sys.argv[1])
lane = sys.argv[2]
sys.exit(0 if data.get("known") and any(c.get("lane") == lane for c in data.get("contributors", [])) else 1)
' "$CONTRIB_OUT" "$LANE"; then
  echo "register-pr-dispatch-self.sh: recorded but the read-back does not show lane $LANE as a contributor to PR #$PR -- treat this as unregistered: $CONTRIB_OUT" >&2
  exit 1
fi

echo "register-pr-dispatch-self.sh: recorded and confirmed -- lane $LANE is now a resolvable contributor to $REPO#$PR (task $TASK)"
printf '%s\n' "$CONTRIB_OUT"
exit 0
