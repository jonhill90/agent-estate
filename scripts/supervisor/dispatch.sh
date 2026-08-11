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
#   dispatch.sh <issue>[,<issue>...] <slug> <brief-file> [repo] [repo-path]
#
# <issue>      one issue number, or a comma-separated list (agent-dotfiles#112)
#              when one brief covers several -- e.g. `110,109`. Every issue in
#              the list is claimed; the lane still gets ONE worktree and ONE
#              brief, because it is doing one piece of work that happens to
#              close more than one issue.
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
# shellcheck source=./input-box.sh
. "$HERE/input-box.sh"
SESSION="${LANES_SESSION:-agent-dotfiles}"

ISSUE_ARG="${1:-}"
SLUG="${2:-}"
BRIEF="${3:-}"
REPO="${4:-}"
REPO_PATH="${5:-$PWD}"

if [ -z "$ISSUE_ARG" ] || [ -z "$SLUG" ] || [ -z "$BRIEF" ]; then
  sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2
  exit 2
fi

# One brief, possibly several issues (agent-dotfiles#112): #109 and #110 came
# from the same review of the same PR and were dispatched to one lane, but
# dispatch.sh only ever claimed the issue it was given -- the rest sat open
# and looked free to the next dispatcher while a lane was actively on them.
# ISSUE (singular, first of the list) is what names the lane branch and the
# tmux window; a list changing the window name mid-estate would break
# `lanes.sh` and `claim.sh stale`, which both match on it.
IFS=',' read -r -a ISSUES <<< "$ISSUE_ARG"
ISSUE="${ISSUES[0]:-}"

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
  echo "dispatch: no free lane in session '$SESSION' -- not dispatching #$ISSUE_ARG" >&2
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
#
# CLAIMED holds only what actually got claimed, in claim order, so a failure
# partway through a multi-issue list (agent-dotfiles#112) unwinds exactly the
# issues this dispatch took and none it did not touch. Aborting the WHOLE
# dispatch when any one claim fails, rather than proceeding with a partial
# claim, matches the existing "every failure aborts" contract: a lane already
# dispatched to a partial claim would be actively working issues the estate
# cannot see as taken, which is the exact failure #112 was filed over.
CLAIMED=()
CLAIM_FAILED=""
for i in "${ISSUES[@]}"; do
  if "$HERE/claim.sh" take "$i" "$REPO" "$WINDOW_NAME"; then
    CLAIMED+=("$i")
  else
    echo "dispatch: #$i is not available -- pick different work" >&2
    CLAIM_FAILED=1
    break
  fi
done

release_claim() {
  local failed=() i
  # Reverse of claim order; the order itself has no observable effect on
  # GitHub state, but unwinding newest-first mirrors how the failure was hit.
  for ((idx = ${#CLAIMED[@]} - 1; idx >= 0; idx--)); do
    i="${CLAIMED[idx]}"
    "$HERE/claim.sh" release "$i" "$REPO" >/dev/null 2>&1 || failed+=("$i")
  done
  if [ "${#failed[@]}" -gt 0 ]; then
    # Loud and unambiguous: a claim nobody can see is worse than no claim,
    # and a silently half-undone abort is exactly that -- issues in $failed
    # are still assigned even though this dispatch is telling its caller it
    # sent nothing.
    echo "dispatch: could not release the claim on #${failed[*]} -- release ${failed[*]} by hand" >&2
  fi
}

if [ -n "$CLAIM_FAILED" ]; then
  release_claim
  exit 1
fi

# --- 3. the worktree. Not optional, not recoverable ------------------------
# worktree.sh prints the path on stdout and git's progress on stderr, so the
# two are captured separately: the path is consumed here, the diagnostics are
# only shown if it fails. Called ONCE -- a retry would leave an orphan.
WORKTREE_ERR=$(mktemp)
WORKTREE=$("$HERE/worktree.sh" new "${ISSUE}-${SLUG}" "$REPO_PATH" 2>"$WORKTREE_ERR")
rc=$?
if [ "$rc" -ne 0 ] || [ -z "$WORKTREE" ] || [ ! -d "$WORKTREE" ]; then
  echo "dispatch: worktree.sh new failed for #$ISSUE_ARG in $REPO_PATH -- NOT dispatching" >&2
  echo "dispatch: a lane with no worktree works in the shared checkout, which is #73" >&2
  sed 's/^/  /' "$WORKTREE_ERR" >&2
  rm -f "$WORKTREE_ERR"
  release_claim
  exit 1
fi
rm -f "$WORKTREE_ERR"

# --- 4. the lane is told what it is doing, then given the work ------------
if ! tmux rename-window -t "$LANE" "$WINDOW_NAME" 2>/dev/null; then
  echo "dispatch: could not rename $LANE -- not dispatching #$ISSUE_ARG" >&2
  "$HERE/worktree.sh" done "$WORKTREE" >/dev/null 2>&1
  release_claim
  exit 1
fi

abort_send() {
  echo "dispatch: $1" >&2
  "$HERE/worktree.sh" done "$WORKTREE" >/dev/null 2>&1
  release_claim
  exit 1
}

# WHAT IS TYPED INTO THE PANE STAYS SHORT, AND HERE IS THE MEASURED REASON.
#
# This message is typed into the lane's input box and then verified by reading
# the pane back. The box shows only its last few rows: past a certain length it
# scrolls INTERNALLY, the head disappears from the visible region, and
# `capture-pane` cannot see it however correct the delivery was.
#
# Measured against a real Claude Code TUI at 80x24 (throwaway tmux server, one
# probe per length, never a live lane):
#
#   ~450 chars -> head visible          ~500 chars -> HEAD LOST
#
# The first version of the deliverable contract lived in this string and took
# it to 610 characters. That failed verification 4 times out of 4 at 80x24 and
# passed 3/3 at 126x60 -- and `free-9` and `free-10` are 80x24, so it broke
# dispatch to real lanes while every stub test stayed green (#118 review).
#
# So the contract is NOT in this string. The message is back to 389 characters
# with representative paths, and `MESSAGE_BUDGET` below pins that this stays
# true: the paths are the bulk of it and they vary, so the margin is thin and
# it is enforced rather than remembered.
MESSAGE="Read $BRIEF and do exactly what it says. That file is your complete brief. Do all of your work in the worktree at $WORKTREE -- it is yours, already branched; never work in the shared checkout at $REPO_PATH."

# The head of the message is what an internally-scrolling box hides first, so
# the length that matters is the whole string. 450 is the measured cliff at
# 80x24; 430 leaves a little room for a slow repaint eating a row.
MESSAGE_BUDGET="${DISPATCH_MESSAGE_BUDGET:-430}"
if [ "${#MESSAGE}" -gt "$MESSAGE_BUDGET" ]; then
  echo "dispatch: the brief message is ${#MESSAGE} chars, over the ${MESSAGE_BUDGET}-char budget for an 80x24 lane." >&2
  echo "  Past ~450 the input box scrolls the head out of view, capture-pane cannot see it," >&2
  echo "  and dispatch aborts even though the message arrived. Shorten it, or put the text in" >&2
  abort_send "the brief message is over the ${MESSAGE_BUDGET}-char budget -- #$ISSUE_ARG was NOT dispatched"
fi


# The standing deliverable contract (#117), written into the BRIEF rather than
# typed at the pane. A lane completed #112 correctly -- tests green,
# mutation-checked, committed -- and stopped, because the brief never said to
# push. It was right to be literal. From outside, a lane that finished without
# shipping is indistinguishable from one that did nothing: no PR, no comment,
# issue still claimed, and the work living only as an unpushed commit in a
# temporary worktree one cleanup away from being lost.
#
# Still structural, which is the whole point of #117: the DISPATCHER writes it
# on every dispatch, so it does not depend on whoever wrote the brief
# remembering -- the mechanism that failed in #114. It moved out of the typed
# message because that string has a hard length budget and this text does not
# fit in it; the brief file has no such limit and is the thing the lane is told
# to read.
#
# It also stops the message contradicting itself. Typed at the pane, the
# dispatcher said "that file is your COMPLETE brief" and then added an
# instruction that was not in it -- and for a read-only review brief, "push
# your branch and open a PR" contradicted the brief's own first line. In the
# file it sits with the rest of the instructions and defers to them.
CONTRACT_MARKER="<!-- dispatch:deliverable-contract -->"
if ! grep -qF "$CONTRACT_MARKER" "$BRIEF" 2>/dev/null; then
  cat >>"$BRIEF" <<EOF || abort_send "could not append the deliverable contract to $BRIEF -- #$ISSUE_ARG was NOT dispatched"

$CONTRACT_MARKER
## Delivering this work

Added by \`dispatch.sh\` on every dispatch, not by the brief's author.

Unless this brief says otherwise, when you are finished:
**push your branch and open a PR**.
If you produced no code -- a review, an investigation, an options paper --
**post your findings as a comment** on the issue or PR the brief names.

Do not stop with the work only in your worktree. From outside, a lane that
finished without shipping is indistinguishable from a lane that did nothing:
unshipped work looks exactly like no work, and the worktree is temporary.
EOF
fi

# `/clear` first: an author reviewing their own PR is not an independent
# reviewer, and a lane carrying the last task's context is not a fresh one.
tmux send-keys -t "$LANE" "/clear" Enter 2>/dev/null \
  || abort_send "send-keys to $LANE failed -- #$ISSUE_ARG was not dispatched"

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
    || abort_send "send-keys to $LANE failed -- #$ISSUE_ARG was not dispatched"
  sleep "${DISPATCH_SETTLE:-1}"
  # Check BOTH ENDS of the message plus the worktree path. The head is what a
  # dropped prefix eats first (observed live, 2026-08-11), and it is also the
  # first thing an over-long message hides by scrolling -- so checking the head
  # alone conflates "arrived and is visible" with "fits". The tail is the part
  # that stays visible under scrolling, so it is the half that still reports
  # honestly when the box is full; checking only the tail would pass a dropped
  # prefix, which is the failure this loop exists for. Both, or neither is
  # evidence.
  #
  # The tail token is the closing phrase plus $REPO_PATH, not the path alone:
  # the harness prints the working directory in its own header, so the bare
  # path matches ordinary pane furniture and would pass on a blank pane.
  # Spaces and newlines come out because a real pane wraps a long path across
  # lines and indents the continuation.
  pane=$(tmux capture-pane -p -t "$LANE" 2>/dev/null | tr -d ' \n')
  if grep -qF "$(tr -d ' ' <<<"Read $BRIEF")" <<<"$pane" \
     && grep -qF "$(tr -d ' ' <<<"$WORKTREE")" <<<"$pane" \
     && grep -qF "$(tr -d ' ' <<<"never work in the shared checkout at $REPO_PATH.")" <<<"$pane"; then
    sent=1
    break
  fi
  # Clear whatever partial text is in the input and retype once.
  tmux send-keys -t "$LANE" C-u 2>/dev/null
  sleep "${DISPATCH_SETTLE:-1}"
done

[ "$sent" = 1 ] || abort_send "the brief did not land intact in $LANE -- #$ISSUE_ARG was NOT dispatched (check the pane by hand)"

tmux send-keys -t "$LANE" Enter 2>/dev/null \
  || abort_send "could not submit the brief in $LANE -- #$ISSUE_ARG was not dispatched"

# --- 5. AND THE BRIEF ACTUALLY STARTED ------------------------------------
# #141. Everything above proves the brief was TYPED. Nothing proved it was
# SUBMITTED, and on 2026-08-11 two lanes sat for 40 minutes each holding a
# full brief in the input box because the Enter arrived while `/clear` was
# still repainting and was swallowed. The dispatcher printed
# `dispatch: #N -> lane` and walked away. This is the #81 and #130 shape
# again: the dispatcher's success message is not evidence of dispatch.
#
# What "started" means is measured, not assumed. The obvious check -- wait for
# the footer to show a running shape -- is racy: driving a real Claude Code
# pane through a short turn, `esc to interrupt` was gone from the footer
# within six seconds, so a fast first turn looks exactly like a brief that
# never ran. The input box emptying is the durable signal: it is true while
# the turn runs AND after it finishes, and it is false in precisely the
# failure this exists for.
CONFIRM_TRIES="${DISPATCH_CONFIRM_TRIES:-10}"
submitted=""
box=""
for ((attempt = 1; attempt <= CONFIRM_TRIES; attempt++)); do
  sleep "${DISPATCH_SETTLE:-1}"
  box=$(tmux capture-pane -pe -t "$LANE" 2>/dev/null | input_box_state)
  if [ "$box" = empty ]; then submitted=1; break; fi
done

if [ -z "$submitted" ]; then
  if [ "$box" = text ]; then
    # Confirmed failure: the message is still sitting in the box. Unwind, so
    # the issue goes back to the pool rather than looking claimed-and-running.
    #
    # The text is deliberately NOT cleared on the way out. C-u does not
    # reliably empty a multi-row box on a real pane, so "cleared" would be
    # another unverified claim -- and a lane left holding it is now visible:
    # `lanes.sh` reports it `unsent` with a count line, which is the state
    # #141 added for exactly this.
    abort_send "the brief was typed into $LANE but never submitted -- #$ISSUE_ARG was NOT dispatched (lanes.sh will show that lane 'unsent')"
  fi
  # `unknown`: the box could not be identified at all -- another harness, or a
  # pane too short to show it. The brief may well be running, so unwinding
  # would release a claim out from under a working lane, which is its own
  # failure. Say so loudly instead of printing a clean success line.
  echo "dispatch: WARNING -- could not confirm the brief started in $LANE" >&2
  echo "dispatch: the input box was not readable (input_box_state: ${box:-none})." >&2
  echo "dispatch: #$ISSUE_ARG is claimed and the worktree exists; CHECK THE PANE BY HAND." >&2
fi

# --- 6. record what was dispatched. BEST EFFORT, NEVER FATAL --------------
#
# agent-dotfiles#140. Every signal that a lane is busy is today inferred from
# pane content, and inference is what produced the false-`free` bugs #102,
# #123 and #126. This writes the fact down instead. Nothing reads it yet --
# `lanes.sh` classifies panes exactly as it did before this block existed --
# and that is deliberate: a recording layer nothing depends on can be wrong
# without taking the estate down, and its records can be checked against
# reality before anything trusts them.
#
# THIS BLOCK MUST NOT ABORT THE DISPATCH, AND THAT IS THE OPPOSITE OF EVERY
# OTHER STEP ABOVE. "Every failure aborts and unwinds" is right for the claim
# and the worktree, which are real resources a half-dispatch would strand. It
# is wrong for a bookkeeping write nothing consumes: a broken ledger that
# stopped the estate dispatching would trade the whole estate for a record
# with no reader. So this is best-effort and LOUD -- never silent, never
# fatal. Do not "fix" it into an abort_send; tests/supervisor/test_dispatch.sh
# mutation-checks that removing this tolerance turns the suite red.
#
# WHY IT RUNS LAST, after the final Enter and past every abort path: a record
# asserting work is in flight, left behind by a dispatch that then aborted, is
# worse than no record at all -- the point of the ledger is to be believed.
# Ordering is what guarantees that, not cleanup, so nothing above this line
# needs an unwind for it.
#
# That is also why step 5 (#141) sits ABOVE this block rather than below it.
# Step 5 can abort_send -- a brief that was typed but never submitted unwinds
# the claim and the worktree -- and a ledger record written before it would be
# exactly the "work is in flight" claim this paragraph rules out, asserted
# about a lane that is running nothing.
ledger_record_failed() {
  echo "dispatch: LEDGER RECORD FAILED for $WINDOW_NAME -- the dispatch STANDS, the record does not" >&2
  sed 's/^/  /' <<<"${1:-}" >&2
  echo "dispatch: the lane is working; nothing reads the ledger yet, so this costs a record, not the run" >&2
  return 0  # the ledger write is never fatal -- agent-dotfiles#140
}

# One tmux call for the pane identity the ledger records. The recorder itself
# never talks to tmux: a durable record that cannot be written without a live
# tmux server is not the portability the ledger is for, and the caller here is
# already holding a tmux connection.
LANE_META=$(tmux display-message -p -t "$LANE" \
  '#{pane_id}|#{pane_current_command}|#{pane_current_path}|#{socket_path}|#{session_created}|#{session_id}' 2>&1)
if [ -z "$LANE_META" ] || [[ "$LANE_META" != *"|"* ]]; then
  ledger_record_failed "could not read pane metadata for $LANE: $LANE_META"
else
  IFS='|' read -r PANE_ID PANE_CMD PANE_PATH SOCKET_PATH SESSION_CREATED SESSION_ID <<<"$LANE_META"
  LEDGER_ARGS=(
    record-dispatch
    --lane "$LANE"
    --task "$WINDOW_NAME"
    --summary "#$ISSUE_ARG $SLUG; worktree=$WORKTREE; brief=$BRIEF"
    --pane-id "$PANE_ID"
    --pane-path "$PANE_PATH"
    --command "$PANE_CMD"
    --server-id "${SOCKET_PATH}:${SESSION_CREATED}"
    --session-id "$SESSION_ID"
    --github "$REPO"
  )
  for i in "${ISSUES[@]}"; do
    LEDGER_ARGS+=(--issue "$i")
  done
  if ! LEDGER_OUT=$("${DISPATCH_PYTHON:-python3}" "$HERE/cli.py" "${LEDGER_ARGS[@]}" 2>&1); then
    ledger_record_failed "$LEDGER_OUT"
  fi
fi

echo "dispatch: #$ISSUE_ARG -> $LANE ($WINDOW_NAME)"
echo "  worktree: $WORKTREE"
echo "  brief:    $BRIEF"
exit 0
