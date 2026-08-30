#!/bin/bash
# Sourced-only, from dispatch.sh, right after dispatch-lane-select.sh. Part of
# the agent-supervisor#716 split (see dispatch-rehome.sh's own header for the
# shape and precedent). Never run standalone.
#
# Step 2 (the GitHub claim), step 3 (the worktree -- not optional, not
# recoverable), step 3.1 (the worktree's origin must actually BE $REPO, #17),
# step 3.2 (the pre-dispatch collision check, #291), and the message-budget
# check (#118) ahead of step 3.5. Defines `cleanup_dispatch_branch` and
# `abort_send` -- the one rollback path every later failure in this script
# calls, so it lives here, right after the worktree it unwinds is built.
# `abort_send` calls `release_claim`/`release_lane_claim`
# (dispatch-lane-select.sh, sourced just before this file) and
# `dispatch_rehome_lane` (dispatch-rehome.sh, sourced first of all) -- both
# already defined by the time this file's own definitions run, let alone by
# the time `abort_send` is ever actually called.
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
#
# agent-supervisor#159: a PR-scoped dispatch (`PR_SCOPED` set) skips this
# loop entirely -- ITS ISSUE(S) STAY CLAIMED BY THE ORIGINAL WORK, on
# purpose. That claim is not this dispatch's to take: #159 was filed
# because `claim.sh take` correctly refuses here (the issue IS claimed,
# by the in-flight work that opened the PR under review/fix), and that
# correct refusal was what pushed dispatch to a ledger-invisible tmux
# hand-off. Step 0.6 above is this dispatch's real ownership check, against
# the PR, not the issue; `CLAIMED` stays empty so `release_claim` (declared
# earlier, alongside the trap that now also covers it -- #572) is already a
# correct no-op for this path with no special-casing needed there.
CLAIM_FAILED=""
if [ -n "$PR_SCOPED" ]; then
  echo "dispatch: PR-scoped dispatch (PR #$PR_SCOPED) -- issue(s) ${ISSUES[*]} left claimed by the original work, no GitHub assignee taken" >&2
else
  for i in "${ISSUES[@]}"; do
    # agent-supervisor#572, the same shape #209 round 2 found for CLAIM_LANE:
    # `$i` is appended to CLAIMED BEFORE `claim.sh take` runs, not after it
    # returns true. `claim.sh take` is a foreground child, so a TERM landing
    # while dispatch.sh is blocked in `wait()` on it is only delivered once
    # that child exits -- and bash runs the pending trap right then, before
    # this loop body gets to resume at `CLAIMED+=("$i")`. Appending first
    # means the trap's release_claim always finds the issue it just claimed,
    # even when the signal lands in that exact gap; popped back off below if
    # the claim never actually landed, so a failed/refused claim is never
    # handed to release_claim as something to unwind.
    CLAIMED+=("$i")
    if "$HERE/claim.sh" take "$i" "$REPO" "$WINDOW_NAME"; then
      :
    else
      unset 'CLAIMED[${#CLAIMED[@]}-1]'
      echo "dispatch: #$i is not available -- pick different work" >&2
      CLAIM_FAILED=1
      break
    fi
  done
fi

if [ -n "$CLAIM_FAILED" ]; then
  release_claim
  release_lane_claim
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
  release_lane_claim
  exit 1
fi
rm -f "$WORKTREE_ERR"

# agent-supervisor#373: `worktree.sh new` just created branch `lane/<slug>`
# via `git worktree add -b` -- that call FAILS if the branch already existed,
# so reaching this line means THIS invocation, and no earlier one, created
# it (worktree.sh's `done` only ever removes the WORKTREE, never the branch
# -- see its own header -- which is what left `lane/<issue>-<slug>` behind on
# every refusal below until now). Recorded once, here, so every cleanup path
# past this point deletes exactly the branch this dispatch made and nothing
# a caller or an earlier lane already owned.
DISPATCH_BRANCH="lane/${ISSUE}-${SLUG}"
cleanup_dispatch_branch() {
  # Guarded by show-ref, same posture would-revert.sh's own scratch-branch
  # cleanup already takes: only delete a branch that still exists under the
  # exact name this dispatch created, never a branch a caller or a later
  # dispatch retargeted onto (#373 point 3 -- clean up only what THIS
  # invocation created).
  if git -C "$REPO_PATH" show-ref --verify --quiet "refs/heads/$DISPATCH_BRANCH"; then
    git -C "$REPO_PATH" branch -D "$DISPATCH_BRANCH" >/dev/null 2>&1 \
      || echo "dispatch: could not remove stray branch $DISPATCH_BRANCH -- remove it by hand: git -C $REPO_PATH branch -D $DISPATCH_BRANCH" >&2
  fi
}

# `abort_send` is defined here, right after the worktree exists, because step
# 3.5 below is now the first thing that can fail with both a claim and a
# worktree already committed -- the same shape every later failure in this
# script already has. Moved up from just after the rename (where it used to
# sit) with no change to its body.
abort_send() {
  echo "dispatch: $1" >&2
  cleanup_worktree=1
  lane_current_path=$(tmux display-message -p -t "$LANE_TARGET" '#{pane_current_path}' 2>/dev/null || true)
  case "$lane_current_path" in
    "$WORKTREE"|"$WORKTREE"/*)
      if dispatch_rehome_lane "$LANE_TARGET" "$REPO_PATH" "$LANE_HARNESS" >/dev/null 2>&1; then
        echo "dispatch: re-homed $LANE out of $WORKTREE before rollback cleanup" >&2
      else
        cleanup_worktree=0
        echo "dispatch: leaving worktree $WORKTREE in place because $LANE still appears to be inside it" >&2
        echo "dispatch: recover with: $0 --rehome-lane $LANE_TARGET '$REPO_PATH' ${LANE_HARNESS:-}" >&2
      fi
      ;;
  esac
  if [ "$cleanup_worktree" = 1 ]; then
    "$HERE/worktree.sh" done "$WORKTREE" >/dev/null 2>&1
    # agent-supervisor#373: only once the worktree itself is actually gone --
    # a branch still checked out by a worktree cannot be deleted anyway, and
    # the branch is left alone (like the worktree) in the "$LANE still
    # appears to be inside it" case above, since that is not a clean abort.
    cleanup_dispatch_branch
  fi
  release_claim
  release_lane_claim
  # agent-dotfiles#209 round 2. Past step 4.5 the lane claim is LIVE and
  # `release_lane_claim` above is a deliberate no-op, so this abort leaves the
  # lane held while releasing the issue and the worktree. That is the
  # fail-closed side of moving the commit point to the send, and an operator
  # must not have to infer it from a silent absence -- an abort that says
  # "NOT dispatched" while a lane stays occupied is exactly the kind of
  # mismatch between message and state this estate keeps filing bugs about.
  if [ -n "${CLAIM_COMMITTED:-}" ]; then
    echo "dispatch: $LANE STAYS HELD -- the brief may have gone live in it, and a lane wrongly freed is not recoverable." >&2
    echo "dispatch: CHECK THE PANE. If the lane finished but never signalled, write the real completion with:" >&2
    # This row is a claim, not a task ($WINDOW_NAME is not its id) -- --lane
    # resolves whichever row shape holds it, same as cancel-open-task below.
    echo "dispatch:   $LEDGER_PYTHON $LEDGER_CLI record-completion --lane $LANE --note <note>" >&2
    echo "dispatch: If nothing real ran and the live brief must be discarded, free it with:" >&2
    echo "dispatch:   $LEDGER_PYTHON $LEDGER_CLI cancel-open-task --lane $LANE" >&2
  fi
  exit 1
}

# --- 3.1 the worktree must actually BE $REPO (#17) --------------------------
# claim.sh (step 2, above) claimed the issue against $REPO. worktree.sh (step
# 3, above) built the worktree from $REPO_PATH. Those two arguments encode
# the same fact -- which repository this dispatch is for -- and until now
# nothing compared them, though both are known by this point. Skipped only
# when REPO was never given: `gh` then resolves the claim from REPO_PATH
# itself, so there is nothing for the two to disagree about.
if [ -n "$REPO" ]; then
  WORKTREE_ORIGIN=$(git -C "$WORKTREE" remote get-url origin 2>/dev/null)
  # Normalize `git@github.com:owner/name.git`, `https://github.com/owner/name`
  # and a bare `owner/name` down to the same OWNER/NAME shape $REPO is given
  # in -- the same awk this repo's watchdog.sh already uses to read its own
  # identity from `origin`.
  WORKTREE_REPO=$(sed 's/\.git$//' <<<"$WORKTREE_ORIGIN" | awk -F'[:/]' 'NF>=2{print $(NF-1)"/"$NF}')
  if [ -z "$WORKTREE_REPO" ] || [ "$WORKTREE_REPO" != "$REPO" ]; then
    abort_send "worktree $WORKTREE has origin '${WORKTREE_ORIGIN:-<unreadable>}' (repo '${WORKTREE_REPO:-unknown}'), not the claimed repo '$REPO' -- refusing rather than drop a lane claimed against one repository into a worktree of another (#17); #$ISSUE_ARG was NOT dispatched"
  fi
fi

# --- 3.2 the pre-dispatch collision check (agent-supervisor#291) -----------
# as#263 and as#266 independently wrote the same fix to the same file
# (scripts/supervisor/quota-watch.sh) -- one lane's work was entirely
# wasted, plus a review, plus a merge decision. This is the refusal that
# catches that shape before it repeats: does #$ISSUE_ARG's own file set (the
# brief, its branch, and the PR it is scoped to, if any -- see
# collision-check.sh's own header) overlap the file set an ALREADY in-flight
# lane is actually touching (measured by `git diff`, not guessed).
#
# HERE, not earlier: this is the first point a real worktree exists for
# `_files_changed_in_worktree` to read (signal 2, a resumed branch's own
# content) and past step 3.1 so the worktree is already confirmed to BE
# $REPO. `abort_send` (defined right after the worktree was built, above)
# is reused rather than reimplemented -- it already unwinds the claim, the
# lane claim, and the worktree in the right order, and already handles a
# lane whose pane cwd is somehow already inside $WORKTREE.
#
# UNKNOWN (no file signal at all) ALLOWS and says so -- most issues never
# name a file, and refusing on "I could not tell" would refuse nearly every
# dispatch; see collision-check.sh's header for why this is the one place
# the fail-closed posture inverts, deliberately. A real refusal is fatal:
# a duplicated PR costs far more than a refused dispatch, and `--force`
# exists for the case an operator genuinely intends the overlap.
COLLISION_OUT=$("$HERE/collision-check.sh" check \
  --issue "$ISSUE_ARG" --brief "$BRIEF" --worktree "$WORKTREE" \
  --repo-path "$REPO_PATH" --repo "$REPO" \
  --exclude-lane "$LANE" \
  ${PR_SCOPED:+--pr "$PR_SCOPED"} \
  ${COLLISION_FORCE:+--force} 2>&1)
COLLISION_RC=$?
if [ "$COLLISION_RC" -ne 0 ]; then
  if [ -n "$REVIEWS_PR_EXPLICIT" ]; then
    # agent-supervisor#645: collision-check.sh's REFUSE means "an in-flight
    # lane already holds these files", which is only a hazard for a dispatch
    # that is going to WRITE them. A `--reviews-pr` dispatch's deliverable is
    # a PR comment -- it reads the files under review, never commits to them
    # -- so it cannot produce the two-writers-one-file collision #291 exists
    # to catch, no matter whose files it overlaps (the PR's own author lane,
    # another review lane, anyone). The overlap is still worth knowing, so
    # it is downgraded to the same informational stdout line ALLOW already
    # gets below, not silenced -- only the refusal (stderr + abort_send)
    # goes away.
    #
    # agent-supervisor#650 (a real bypass, reproduced): this keys on
    # REVIEWS_PR_EXPLICIT, never on REVIEWS_PR itself. REVIEWS_PR is also
    # set by the agent-supervisor#70 inference block below (a title merely
    # matching "review" + "PR #N"), and that inference is deliberately wide
    # -- "annoying, not dangerous" is what its own comment calls a false
    # positive, on the assumption that misinferring costs at most a wrongly
    # excluded lane or a refused dispatch. Keying the downgrade on the
    # inferred value turned that same misinference into a silent bypass of
    # the writer-vs-writer collision guard for a dispatch that really is
    # going to write: a plain write dispatch, no `--reviews-pr` flag, whose
    # issue title happens to match the inference pattern (e.g. "rebase it
    # so it can be reviewed, PR #500", #101's own cited false-positive
    # example) proceeded against a file an in-flight lane was actively
    # editing. Only an operator's EXPLICIT `--reviews-pr` -- "this dispatch
    # is a review, and I accept a real overlap will not be re-checked" -- is
    # the assertion this downgrade is entitled to trust. A plain (write)
    # dispatch, whether or not REVIEWS_PR ends up inferred, still hits the
    # abort_send branch below, unchanged.
    :
  else
    # A refusal, same as every other guard's stderr above -- agent-dotfiles#199
    # only requires SILENCE on a SUCCESSFUL dispatch; this one is not one.
    sed 's/^/dispatch: collision-check: /' <<<"$COLLISION_OUT" >&2
    abort_send "#$ISSUE_ARG's files collide with an in-flight lane -- NOT dispatched. Re-run with --force if this overlap is known and intended (agent-supervisor#291)"
  fi
fi
# ALLOW (no-conflict, unknown, or forced), or a REFUSE downgraded to
# information for an EXPLICIT `--reviews-pr` dispatch (see the
# REVIEWS_PR_EXPLICIT branch above) -- on stdout, not stderr:
# agent-dotfiles#199 requires stderr silent on a successful dispatch, and
# "say UNKNOWN, don't let it read as nothing" (the issue's own words) only
# requires this is SAID, not that it is said on
# stderr specifically.
sed 's/^/dispatch: collision-check: /' <<<"$COLLISION_OUT"

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
#
# Built and budget-checked here, BEFORE step 3.5 relaunches the harness: an
# over-budget message means this dispatch is refused regardless of anything
# below, and there is no reason to kill and relaunch a lane's harness only to
# then abort without ever typing into it.
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
