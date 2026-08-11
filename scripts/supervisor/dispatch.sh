#!/bin/bash
# Dispatch one issue to one lane: pick a free lane, claim the issue, CREATE
# THE LANE'S WORKTREE, then send the brief. One command, or nothing happens.
#
# WHY: agent-dotfiles#81. `worktree.sh` was built for #73 and nothing called
# it -- `grep -rn worktree.sh` found three code fences in loop-tick.md and a
# section of the supervisor README, and that was all. The tool fails closed
# when it is called; what was missing was anything that calls it. Enforcement
# was "the dispatcher reads the file and runs the command", which is the same
# mechanism whose failure produced #73: a lane had its branch switched out
# from under it in the shared checkout and lost four files of uncommitted
# work. The risk moved from the lanes to the dispatcher; it did not go away.
#
# The estate has now hit this shape three times: acp_transport.py (302 lines,
# tested, zero importers, #56), claim.sh (wired into the dispatch step by #74,
# the one that got it right), and worktree.sh (#81). A tool that fails closed
# when called, and that nothing calls, is a documentation rule with a binary
# attached. So the sequence a dispatcher used to perform by hand -- read
# lanes.sh, run claim.sh, run worktree.sh, rename the window, send-keys --
# lives here, where the worktree step cannot be the one that gets skipped.
#
# EVERY FAILURE ABORTS THE DISPATCH. In particular a failed `worktree.sh new`
# is fatal: a lane with no worktree works in the shared checkout, and that is
# the original bug, not a degraded mode of operation. Whatever was already
# done -- the claim, the worktree -- is undone before exiting, so a failed
# dispatch leaves the estate exactly as it found it and the issue stays
# available to the next tick.
#
# Usage:
#   dispatch.sh <issue> <slug> <brief-file> [repo] [repo-path]
#
# <slug>       short reason, e.g. `dispatch-worktree`; with <issue> it names
#              both the lane branch and the tmux window.
# <brief-file> the worker's complete brief. Sent by path, not pasted: a brief
#              large enough to be worth writing is too large for send-keys.
# [repo]       OWNER/NAME for the claim; omitted, gh resolves it from [repo-path].
# [repo-path]  the shared checkout to branch the worktree from; default $PWD.
#
# Exit 0 only when a lane has been sent a brief. Exit 1 on any refusal --
# no free lane, an issue someone else already claimed, a worktree that could
# not be created, a send that failed.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SESSION="${LANES_SESSION:-agent-dotfiles}"

ISSUE="${1:-}"
SLUG="${2:-}"
BRIEF="${3:-}"
REPO="${4:-}"
REPO_PATH="${5:-$PWD}"

if [ -z "$ISSUE" ] || [ -z "$SLUG" ] || [ -z "$BRIEF" ]; then
  sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2
  exit 2
fi

# Checked before anything is claimed or created: a typo in the brief path is
# the cheapest failure available, and it must stay that way.
[ -f "$BRIEF" ] || { echo "dispatch: no brief file at $BRIEF" >&2; exit 1; }
BRIEF="$(cd "$(dirname "$BRIEF")" && pwd)/$(basename "$BRIEF")"

# Window name: <prefix><issue>-<slug>, the convention loop-tick.md requires so
# Jon can read the tmux window list and know what the estate is doing. The
# prefix is the repo's initials when its name is hyphenated (agent-dotfiles ->
# ad, agent-evals -> ae) and the name itself when it is not (skills ->
# skills139-...), which is what the live session already looks like.
NAME_PART="${REPO##*/}"
[ -n "$NAME_PART" ] || NAME_PART="$(basename "$REPO_PATH")"
if [[ "$NAME_PART" == *-* ]]; then
  PREFIX=$(tr '-' '\n' <<<"$NAME_PART" | cut -c1 | tr -d '\n')
else
  PREFIX="$NAME_PART"
fi
WINDOW_NAME="${PREFIX}${ISSUE}-${SLUG}"

# --- 1. a lane that is actually safe to dispatch to ------------------------
# `send-keys -t session:` with an empty index does not error; it targets the
# active window, which is usually the supervisor. Refuse an empty target
# rather than discover where the brief landed.
#
# TWO questions, and `lanes.sh --free` only answers the first. "Is an agent
# there and not mid-turn" it answers from pane content. "Is this lane UNOWNED"
# it cannot answer at all: a lane that finished and was never renamed, and a
# lane paused on an approval prompt, both show no busy marker and are
# byte-identical to a genuinely idle one. The window NAME is the only signal
# that survives that, which is why claim.sh's `stale` keys on
# `$2 !~ /^free-[0-9]+$/` and loop-tick.md requires the rename on completion.
# This is now the only path a brief takes to a lane, so the rule has to hold
# here too. On 2026-08-11 the supervisor took `--free | head -1` by hand, got
# another dispatcher's task-named lane, and `/clear`ed it; nothing was lost
# only because that lane had already shipped.
#
# There is deliberately NO env-var override of this selection. `DISPATCH_LANE`
# used to be honoured verbatim -- no free check, no name check, no supervisor
# exclusion -- and `DISPATCH_LANE=t:1` put `/clear` plus a full brief into the
# supervisor's own pane at exit 0, which is the incident loop-tick.md records
# under "an empty tmux target hits the ACTIVE window", reached through a stray
# environment variable instead of an empty string. Nothing called it. An
# escape hatch around the only guard is not worth a caller it does not have;
# to aim a dispatch, rename the target lane `free-N` first, which is the same
# thing its owner would have done on finishing.
FREE_NAMED=$("$HERE/lanes.sh" "$SESSION" 2>/dev/null \
  | awk 'NR>1 && $1 ~ /^[0-9]+$/ && $2 ~ /^free-[0-9]+$/ {print $1}')
LANE=""
while read -r candidate; do
  [ -n "$candidate" ] || continue
  # lanes.sh --free prints session:index, and the index is what names the row.
  if grep -qx -- "${candidate##*:}" <<<"$FREE_NAMED"; then
    LANE="$candidate"
    break
  fi
done < <("$HERE/lanes.sh" --free "$SESSION" 2>/dev/null)

if [ -z "$LANE" ]; then
  echo "dispatch: no free lane in session '$SESSION' -- not dispatching #$ISSUE" >&2
  echo "dispatch: a lane must be idle AND named 'free-N' to be dispatchable --" >&2
  echo "dispatch: one still carrying a task name is still working on it" >&2
  "$HERE/lanes.sh" "$SESSION" >&2
  exit 1
fi

# --- 2. the claim, before anything else is built --------------------------
# The repo slot is ALWAYS passed, even empty. claim.sh's interface is
# positional -- `take <issue> [repo] [lane]` -- so dropping an empty repo does
# not shorten the argument list, it SHIFTS the lane name into the repo slot.
# `dispatch.sh 95 claim-refuses-closed brief.md` with no repo argument ran
# `gh issue view 95 -R claim-refuses-closed`, which fails, and reported
# `claim: could not assign #95` for an open, unclaimed issue. Indistinguishable
# from a legitimate refusal, and it aborted the dispatch every time.
CLAIM_ARGS=("$ISSUE" "$REPO")
if ! "$HERE/claim.sh" take "${CLAIM_ARGS[@]}" "$WINDOW_NAME"; then
  echo "dispatch: #$ISSUE is not available -- pick different work" >&2
  exit 1
fi

release_claim() {
  "$HERE/claim.sh" release "${CLAIM_ARGS[@]}" >/dev/null 2>&1 \
    || echo "dispatch: could not release the claim on #$ISSUE -- release it by hand" >&2
}

# --- 3. the worktree. Not optional, not recoverable ------------------------
# worktree.sh prints the path on stdout and git's progress on stderr, so the
# two are captured separately: the path is consumed here, the diagnostics are
# only shown if it fails. Called ONCE -- a retry would leave an orphan.
WORKTREE_ERR=$(mktemp)
WORKTREE=$("$HERE/worktree.sh" new "${ISSUE}-${SLUG}" "$REPO_PATH" 2>"$WORKTREE_ERR")
rc=$?
if [ "$rc" -ne 0 ] || [ -z "$WORKTREE" ] || [ ! -d "$WORKTREE" ]; then
  echo "dispatch: worktree.sh new failed for #$ISSUE in $REPO_PATH -- NOT dispatching" >&2
  echo "dispatch: a lane with no worktree works in the shared checkout, which is #73" >&2
  sed 's/^/  /' "$WORKTREE_ERR" >&2
  rm -f "$WORKTREE_ERR"
  release_claim
  exit 1
fi
rm -f "$WORKTREE_ERR"

# --- 4. the lane is told what it is doing, then given the work ------------
if ! tmux rename-window -t "$LANE" "$WINDOW_NAME" 2>/dev/null; then
  echo "dispatch: could not rename $LANE -- not dispatching #$ISSUE" >&2
  "$HERE/worktree.sh" done "$WORKTREE" >/dev/null 2>&1
  release_claim
  exit 1
fi

MESSAGE="Read $BRIEF and do exactly what it says. That file is your complete brief. Do all of your work in the worktree at $WORKTREE -- it is yours, already branched; never work in the shared checkout at $REPO_PATH."

abort_send() {
  echo "dispatch: $1" >&2
  "$HERE/worktree.sh" done "$WORKTREE" >/dev/null 2>&1
  release_claim
  exit 1
}

# `/clear` first: an author reviewing their own PR is not an independent
# reviewer, and a lane carrying the last task's context is not a fresh one.
tmux send-keys -t "$LANE" "/clear" Enter 2>/dev/null \
  || abort_send "send-keys to $LANE failed -- #$ISSUE was not dispatched"

# THEN WAIT. Observed live on 2026-08-11 while building this: typing the brief
# immediately after `/clear` lost the leading characters -- the lane's prompt
# read `/var/.../brief.md and do exactly what it says`, with `Read ` gone,
# because the harness was still repainting. A brief that arrives mangled is
# worse than one that does not arrive: the lane acts on it anyway.
sleep "${DISPATCH_SETTLE:-2}"

# Type, verify, THEN submit. The verification is why the Enter is a separate
# call: what the pane actually shows is the only evidence that the keys landed.
sent=0
for attempt in 1 2; do
  tmux send-keys -t "$LANE" "$MESSAGE" 2>/dev/null \
    || abort_send "send-keys to $LANE failed -- #$ISSUE was not dispatched"
  sleep "${DISPATCH_SETTLE:-1}"
  # Check the HEAD of the message and the worktree path: the head is what a
  # dropped prefix eats first, and the path is what the lane must not lose.
  # Spaces and newlines come out because a real pane wraps a long path across
  # lines and indents the continuation.
  pane=$(tmux capture-pane -p -t "$LANE" 2>/dev/null | tr -d ' \n')
  if grep -qF "$(tr -d ' ' <<<"Read $BRIEF")" <<<"$pane" \
     && grep -qF "$(tr -d ' ' <<<"$WORKTREE")" <<<"$pane"; then
    sent=1
    break
  fi
  # Clear whatever partial text is in the input and retype once.
  tmux send-keys -t "$LANE" C-u 2>/dev/null
  sleep "${DISPATCH_SETTLE:-1}"
done

[ "$sent" = 1 ] || abort_send "the brief did not land intact in $LANE -- #$ISSUE was NOT dispatched (check the pane by hand)"

tmux send-keys -t "$LANE" Enter 2>/dev/null \
  || abort_send "could not submit the brief in $LANE -- #$ISSUE was not dispatched"

echo "dispatch: #$ISSUE -> $LANE ($WINDOW_NAME)"
echo "  worktree: $WORKTREE"
echo "  brief:    $BRIEF"
exit 0
