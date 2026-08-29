#!/bin/bash
# Sourced-only, from dispatch.sh, right after dispatch-send.sh -- the last
# file in the source chain. Part of the agent-supervisor#716 split (see
# dispatch-rehome.sh's own header for the shape and precedent). Never run
# standalone.
#
# Step 6: record what was dispatched (BEST EFFORT, NEVER FATAL --
# agent-dotfiles#140/#174/#188) via `cli.py record-dispatch`, including the
# `ledger_record_failed`/`ledger_record_pr_duplicate` reporters, the
# agent-supervisor#456 died-pane refusal, and dispatch.sh's own final
# stdout success lines and `exit 0`.
# agent-supervisor#193: the ledger's own record of "was this genuinely
# accepted" (`--confirm-landed`, step 6 below), set ONLY when
# `DISPATCH_SUBMIT_STATUS` reads `submitted` -- the box actually confirmed
# empty after a position-anchored, proof-checked send -- AND the pane
# survived to be re-observed running it (step 5.5, agent-supervisor#456,
# `DISPATCH_DIED` unset). `DISPATCH_SUBMIT_STATUS`, not `$SEND_STATUS`:
# verified_survived overwrote the latter with its own `survived`/`died`
# vocabulary above, so reading `$SEND_STATUS` here would silently compare
# against the WRONG check's answer. The `unknown` case above is explicitly
# NOT `submitted` either: the brief may well be running, but nothing here
# CONFIRMED it, and #193's whole point is that the reconciler must not take
# "the lane went quiet" as a stand-in for that confirmation -- #456 extends
# the same rule to "the lane went quiet because it is gone".
DISPATCH_CONFIRM_LANDED_ARGS=()
[ "$DISPATCH_SUBMIT_STATUS" = submitted ] && [ -z "${DISPATCH_DIED:-}" ] && DISPATCH_CONFIRM_LANDED_ARGS=(--confirm-landed)

# The dispatch was committed at step 4.5, not here. This comment used to
# introduce a `CLAIM_COMMITTED=1` on this line, and that placement was
# agent-dotfiles#209's blocking defect: everything it says about a brief being
# live in a real pane was already true ~70 lines and up to
# DISPATCH_CONFIRM_TRIES x DISPATCH_SETTLE earlier, at the `send-keys Enter`
# above. The reasoning was right and the position was wrong, so the position
# moved; do not move it back. What that reasoning still buys, unchanged, is
# the case below: a `record_dispatch` FAILURE is non-fatal by design and falls
# straight through to the exit, where the EXIT trap runs -- and the claim
# placeholder is then the only thing keeping a working lane out of the next
# dispatcher's hands. Step 4.5's ledger commit is what makes both the trap and
# the reap leave it alone.

# --- 6. record what was dispatched. BEST EFFORT, NEVER FATAL --------------
#
# agent-dotfiles#140, updated by agent-dotfiles#174. Every signal that a lane
# is busy used to be inferred from pane content, and inference is what
# produced the false-`free` bugs #102, #123 and #126. This writes the fact
# down instead.
#
# #140 made this write non-fatal because NOTHING READ IT YET -- a recording
# layer nothing depends on can be wrong without taking the estate down. That
# premise is gone: step 1 above now reads exactly this record to pick every
# future lane, and #174 was filed to make that inversion explicit rather than
# leave this comment asserting the opposite of what the code now does. Read
# what follows as "still non-fatal, for a DIFFERENT reason", not as the old
# reason left unexamined.
#
# THIS BLOCK STILL MUST NOT ABORT THE DISPATCH, AND THAT IS STILL THE
# OPPOSITE OF EVERY OTHER STEP ABOVE. The brief has already been typed,
# verified and submitted into a REAL, LIVE pane by the time this block runs --
# unwinding the claim and the worktree here would strand a worker that is
# actively working, which is strictly worse than a stale ledger row. So the
# failure mode this block accepts is not "the estate may dispatch to a lane
# that is actually busy" -- it is "this ONE lane stops being offered until
# reconciliation happens". That cost is bounded and visible (the LOUD message
# below, and `lanes.sh` still showing the window doing nothing); trading the
# live worker for it would not be. Do not "fix" it into an abort_send;
# tests/supervisor/test_dispatch.sh mutation-checks that removing this
# tolerance turns the suite red.
#
# agent-dotfiles#188 finding 1: step 1's fail-closed read does NOT rule out
# "actually busy" on its own here. `Ledger.record_dispatch` is one
# transaction, so a failure rolls back every one of its five writes -- and
# for a lane the ledger already had registered free (every lane after its
# first backfill, and every lane `lane-done.sh` has ever freed, which is
# ordinary steady state, not an edge case), rollback restores exactly that
# pre-existing FREE row, not UNKNOWN. A comment here used to claim the
# unrecorded lane reads UNKNOWN regardless; it does not, and #188 is the
# defect that shape produces (also #145, #170: a comment asserting a
# protection the code does not have). `cli.py`'s `record_dispatch` now closes
# that window itself -- on any failure it calls `Ledger.mark_lane_held`
# before re-raising, which writes a placeholder outstanding task for the
# lane so `lane_available` reads occupied, not whatever it read before the
# call. That write happens inside the Python process handling this exact
# failure, so it is not conditional on this bash block at all; what follows
# here is only the loud, human-facing report of what already happened.
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
  echo "dispatch: the lane is working, and cli.py has marked it HELD (a placeholder occupied task) so it will not be offered again -- reconcile it by hand (cli.py register / record-dispatch) or let a later dispatch overwrite it" >&2
  return 0  # the ledger write is never fatal -- agent-dotfiles#140
}

# agent-supervisor#169: a PR-scoped record-dispatch failure is not an
# ordinary ledger write failure -- `PR-DUPLICATE:` (printed by `cli.py`'s
# `record_dispatch` when the write-time `one_open_pull_per_source_ref` index
# refuses a second open row for the same PR) means ANOTHER lane genuinely
# won the same PR, seconds ago, through the exact race step 0.6 cannot
# always catch. `ledger_record_failed` above is deliberately non-fatal
# because the brief is already live in the pane and nothing here can unsend
# it -- that stays true here too, the dispatch still stands and the lane is
# still marked HELD by the same call. What changes is the EXIT CODE: this
# refusal is loud AND non-zero, so a caller (a human, or a supervisor loop)
# is told this specific dispatch collided with a lane that actually holds
# the PR, rather than reading dispatch.sh's overall success alongside a
# buried warning.
ledger_record_pr_duplicate() {
  echo "dispatch: $1" >&2
  echo "dispatch: the lane is working, and cli.py has marked it HELD (a placeholder occupied task) so it will not be offered again -- reconcile it by hand (cli.py register / record-dispatch, or cli.py cancel-open-task --lane $LANE if this lane's own brief should be abandoned)" >&2
}

# One tmux call for the pane identity the ledger records. The recorder itself
# never talks to tmux: a durable record that cannot be written without a live
# tmux server is not the portability the ledger is for, and the caller here is
# already holding a tmux connection.
LANE_META=$(tmux display-message -p -t "$LANE_TARGET" \
  '#{pane_id}|#{pane_current_command}|#{pane_current_path}|#{socket_path}|#{session_created}|#{session_id}' 2>&1)
if [ -z "$LANE_META" ] || [[ "$LANE_META" != *"|"* ]]; then
  ledger_record_failed "could not read pane metadata for $LANE: $LANE_META"
else
  IFS='|' read -r PANE_ID PANE_CMD PANE_PATH SOCKET_PATH SESSION_CREATED SESSION_ID <<<"$LANE_META"
  # agent-dotfiles#237: the HARNESS conversation id, which is the only part of
  # this record that survives a tmux server loss -- `$SESSION_ID` above is
  # tmux's own `#{session_id}` and dies with the server. Resolved here, in the
  # same block as the rest of the pane identity, and under the same rule as
  # everything else in it: FAILING TO RESOLVE IS NOT A DISPATCH FAILURE. The
  # brief is already live in the pane; a lane recorded with no session id is
  # merely one `restore.sh` will report unrecoverable, which is the outcome
  # #237 asks for over a fresh agent wearing this lane's name.
  #
  # `--marker "$WORKTREE"`: unique to this dispatch (worktree.sh mints a fresh
  # path per dispatch), so the resolver can tell this lane's brand-new
  # transcript from every other one on the machine. See harness_session.py for
  # the two other tests it applies and for what was measured before settling
  # on these.
  HARNESS_SESSION_ID=""
  # agent-supervisor#172: the directory the harness process was actually
  # launched in -- `$PANE_PATH`, read in the same tmux call as everything
  # else above, at the SAME moment the session id is resolved. Never set
  # independently of `HARNESS_SESSION_ID`: `claude --resume` is scoped to
  # this directory, not to whatever `repo` (a worktree, rewritten on every
  # dispatch) happens to hold later, and pairing a real id with the wrong
  # directory is exactly the defect this issue exists to close. Left empty
  # alongside `HARNESS_SESSION_ID` whenever resolution fails, below.
  HARNESS_PROJECT_DIR=""
  if [ -n "$LANE_HARNESS" ]; then
    if HARNESS_SESSION_ID=$("${DISPATCH_PYTHON:-python3}" "$HERE/harness_session.py" \
        --harness "$LANE_HARNESS" \
        --marker "$WORKTREE" \
        --since "$DISPATCH_SEND_EPOCH" \
        --timeout "${DISPATCH_SESSION_TIMEOUT:-20}" 2>&1); then
      HARNESS_PROJECT_DIR="$PANE_PATH"
    else
      # stdout, not stderr (agent-dotfiles#199/#237): this is loud, not a
      # failure -- the brief already went out and the dispatch still
      # succeeds. #199 holds dispatch.sh's stderr clean on any successful
      # dispatch, on purpose, so the supervisor can treat stderr output as
      # "something is wrong" without also having to parse it for this
      # expected, non-fatal case. A resolver that can never find a real
      # harness process (this repo's own test stubs, or a lane whose
      # harness has no transcript path at all) hits this on every dispatch,
      # which is exactly the noise #199 exists to keep off stderr.
      echo "dispatch: no harness session id recorded for $WINDOW_NAME -- ${HARNESS_SESSION_ID:-no reason given}"
      echo "dispatch: the dispatch STANDS; this lane will read unrecoverable to restore.sh until its next dispatch"
      HARNESS_SESSION_ID=""
    fi
  fi
  # agent-supervisor#117: recorded CANONICAL, not as `worktree.sh` printed
  # it. `WORKTREE` is built from `$REPO/.worktrees` by default since
  # agent-estate#821 (or `$TMPDIR`/`WORKTREE_ROOT` when that's overridden),
  # and on macOS both `/tmp` and (the `/var/folders/...` `$TMPDIR` default)
  # `/var` are themselves symlinks into `/private` -- `git worktree list
  # --porcelain`
  # (step 3's own lookup, above) reports the RESOLVED path, so recording
  # the unresolved one here would make every future lookup miss by
  # construction, silently, for every dispatch on such a host. Falls back
  # to the unresolved path only if `cd`+`pwd -P` cannot run (the worktree
  # already gone) rather than dropping the association entirely.
  WORKTREE_CANONICAL="$WORKTREE"
  WORKTREE_CANONICAL_RESOLVED=$(cd "$WORKTREE" 2>/dev/null && pwd -P) || true
  [ -n "$WORKTREE_CANONICAL_RESOLVED" ] && WORKTREE_CANONICAL="$WORKTREE_CANONICAL_RESOLVED"
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
    --harness-session-id "$HARNESS_SESSION_ID"
    --harness-project-dir "$HARNESS_PROJECT_DIR"
    --github "$REPO"
    # agent-supervisor#117: the worktree this dispatch just built, recorded
    # as its own column now (`Ledger.get_task_for_worktree`) instead of only
    # living inside --summary text, so a later --reviews-pr authorship check
    # can look it back up without reconstructing anything from a branch name.
    --worktree "$WORKTREE_CANONICAL"
    # bash 3.2-safe empty-array expansion -- see this file's own comment on
    # POSITIONAL, above, for why the bare "${arr[@]}" form is not.
    "${DISPATCH_CONFIRM_LANDED_ARGS[@]+"${DISPATCH_CONFIRM_LANDED_ARGS[@]}"}"
  )
  # agent-dotfiles#216: forward the harness `lane-free` already resolved
  # (step 1) instead of letting `record-dispatch` re-derive one from
  # $PANE_CMD via its own narrower HARNESS_BY_COMMAND fallback -- that
  # fallback cannot represent a Node harness at all, which is the bug this
  # closes. Omitted when step 1 never resolved one (LANE_HARNESS empty);
  # record-dispatch's fallback still applies then, unchanged.
  [ -z "$LANE_HARNESS" ] || LEDGER_ARGS+=(--harness "$LANE_HARNESS")
  for i in "${ISSUES[@]}"; do
    LEDGER_ARGS+=(--issue "$i")
  done
  # agent-supervisor#159: recorded as the `source_tasks` KEY for a PR-scoped
  # dispatch (`cli.py record_dispatch` writes `source_kind='pull'` when this
  # is present) -- see cli.py's own docstring. The `--issue` args above are
  # still sent; they land in evidence, not the source key, for this path.
  [ -z "$PR_SCOPED" ] || LEDGER_ARGS+=(--pr "$PR_SCOPED")
  # agent-supervisor#640: record review-ness as the FACT this dispatch
  # already knows, instead of leaving `get_contributor_tasks_for_pr` to
  # guess it back later from `$WINDOW_NAME`/`$SLUG` text via
  # `_task_looks_like_review` -- the guess is what let a `--reviews-pr`
  # task named `rerev...` (the "re" immediately before "rev", no `-`/`_`
  # between them, defeats that regex) score as an AUTHOR of the PR it
  # reviewed. `$REVIEWS_PR`, not `$PR_SCOPED`: a `--pr`-only fix pass must
  # record itself as explicitly NOT a review (see cli.py's own docstring),
  # never omit the flag and let it read as "unknown".
  [ -z "$REVIEWS_PR" ] || LEDGER_ARGS+=(--is-review)
  if ! LEDGER_OUT=$("${DISPATCH_PYTHON:-python3}" "$HERE/cli.py" "${LEDGER_ARGS[@]}" 2>&1); then
    if grep -qF 'PR-DUPLICATE:' <<<"$LEDGER_OUT"; then
      PR_DUP_MSG=$(sed -n 's/^PR-DUPLICATE: //p' <<<"$LEDGER_OUT" | head -1)
      ledger_record_pr_duplicate "$PR_DUP_MSG"
      exit 1
    fi
    ledger_record_failed "$LEDGER_OUT"
  fi
fi

# agent-supervisor#456: a died pane never gets this block's own success-
# shaped lines -- step 5.5 already printed a loud, specific warning to
# stderr, and printing "dispatch: #N -> lane" on top of it here would read as
# a clean success to any caller that only checks stdout, exactly the
# mismatch #456 exists to close. The lane IS still claimed and the worktree
# IS still real (nothing above unwinds either, per step 4.5's own
# invariant) -- only "this looks like an ordinary successful dispatch" is
# withheld.
if [ -n "${DISPATCH_DIED:-}" ]; then
  echo "dispatch: #$ISSUE_ARG was claimed against $LANE ($WINDOW_NAME) but the pane DID NOT SURVIVE -- see the WARNING above" >&2
  exit 1
fi

# The target is printed as well as the lane (#241) because the caller's very
# next action is `lane-done.sh <window> <name> <channel>` (loop-tick.md), and
# that waiter blocks for as long as the work takes -- the longest-lived
# resolved target in the estate, and so the one most certain to be addressing
# a renumbered index by the time it fires. Give it the id, not the index.
echo "dispatch: #$ISSUE_ARG -> $LANE ($WINDOW_NAME)"
echo "  target:   $LANE_TARGET   # pass this to lane-done.sh, not the index"
echo "  worktree: $WORKTREE"
echo "  brief:    $BRIEF"
exit 0
