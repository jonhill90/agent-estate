#!/bin/bash
# Sourced-only, from dispatch.sh, right after dispatch-guards.sh. Part of the
# agent-supervisor#716 split (see dispatch-rehome.sh's own header for the
# shape and precedent). Never run standalone.
#
# Step 1: pick a lane that is actually safe to dispatch to, and step 1.5: the
# claude-print default routing (#171). This is the largest and most delicate
# piece of the split -- it owns the claim/release lifecycle (`release_lane_claim`,
# `release_claim`, `release_claim_on_signal`, and the EXIT/TERM/INT traps that
# call them -- agent-dotfiles#209), the candidate-search helpers
# (`json_field`, `append_exclusion`, `claim_token_from_task`,
# `describe_excluded_lane`), the `WINDOW_NAME_BY_INDEX` table, the candidate
# loop itself (author-exclusion against $AUTHOR_LANES, the ledger `lane-free`/
# `claim-lane` race-closing pair), the "no free lane" diagnostic dump, and the
# `--live-pane`-gated routing of a plain single-issue `claude` dispatch onto
# `dispatch-claude-print.sh` instead of this candidate's own tmux pane.
#
# Defines the trap handlers dispatch-worktree.sh's `abort_send` and every
# later file's failure paths call by name (`release_claim`,
# `release_lane_claim`) -- those calls resolve at CALL time, long after every
# file here is sourced, so the forward reference from earlier-sourced files
# back into ones sourced later is never live.
# --- 1. a lane that is actually safe to dispatch to ------------------------
# `send-keys -t session:` with an empty index does not error; it targets the
# active window, which is usually the supervisor. Refuse an empty target
# rather than discover where the brief landed.
#
# TWO questions, and `lanes.sh --free` only answers the first. "Is an agent
# there and not mid-turn" it answers from pane content -- that stays exactly
# as it was; lanes.sh keeps classifying panes, this change is not about that.
# "Is this lane UNOWNED" is the ledger's question now, not the window name's:
# a lane that finished and was never renamed, and a lane paused on an
# approval prompt, both show no busy marker and are byte-identical to a
# genuinely idle one from pane content alone, and the ledger is what breaks
# that tie. On 2026-08-11 the supervisor took `--free | head -1` by hand, got
# another dispatcher's task-named lane, and `/clear`ed it; nothing was lost
# only because that lane had already shipped.
#
# The window NAME still matters for exactly one thing: MIGRATION. A lane the
# ledger has never heard of -- every lane alive before this landed, or one
# opened by hand -- is backfilled into the ledger as free the FIRST time it
# is seen named `free-N`, and never consulted by name again after that (see
# `cli.py lane_free`'s docstring). A lane the ledger already knows about, free
# or occupied, answers from the ledger alone regardless of what it is
# currently named -- that is the inversion #174 exists to prove: an occupied
# lane hand-renamed to `free-N` is still not offered.
#
# There is deliberately NO env-var override of this selection. `DISPATCH_LANE`
# used to be honoured verbatim -- no free check, no name check, no supervisor
# exclusion -- and `DISPATCH_LANE=t:1` put `/clear` plus a full brief into the
# supervisor's own pane at exit 0, which is the incident loop-tick.md records
# under "an empty tmux target hits the ACTIVE window", reached through a stray
# environment variable instead of an empty string. Nothing called it. An
# escape hatch around the only guard is not worth a caller it does not have.
#
# agent-dotfiles#184 (closing #188 finding 2's own gap): `lane-free` is a
# QUERY, not a claim -- see `cli.py lane_free`'s own docstring for the
# measured proof. Left alone, nothing re-checks between a candidate reading
# free and the first `send-keys` several steps below, and that window is not
# sub-second the way `claim.sh`'s is: it spans claim, worktree creation and
# the send itself. So a candidate reading free is now followed IMMEDIATELY
# by `claim-lane`, an atomic write-then-verify (see `Ledger.claim_lane`'s
# docstring): it inserts a placeholder occupying the lane, protected by the
# same `one_open_task_per_lane` unique index the rest of the ledger already
# relies on, and re-reads to confirm the placeholder it just wrote is still
# the one occupying the lane. Two dispatchers racing the SAME candidate are
# serialized by that call, not by this loop -- the loser's claim is refused,
# not merged, and it moves on to the next candidate instead of stopping.
# `record_dispatch` (step 6) still mints a fresh nonce and cancels whatever
# was outstanding for the lane on every call (measured, #183 round 3) -- but
# by the time it runs, "whatever was outstanding" is this dispatch's OWN
# claim, not a stranger's, because the claim already closed the window a
# stranger could have used.
CLAIM_TOKEN="$WINDOW_NAME"
# agent-dotfiles#209. Two guards, and they are not the same guard.
#
# CLAIM_LANE: nothing has been claimed yet, so there is nothing to release.
#
# CLAIM_COMMITTED: step 4.5 has marked the claim LIVE and the brief is going
# into a real pane, so this dispatch is no longer unwindable. Past that point
# releasing the claim would free a lane that is actively working --
# #102/#126's failure, caused by the cleanup rather than prevented by it. It
# matters because of the trap below, which fires on the SUCCESS path too: on a
# clean dispatch `record_dispatch`'s own `_register_lane_tx` has already
# cancelled this placeholder, so the release would be a harmless no-op -- but
# when `record_dispatch` FAILS (non-fatal by design, step 6) the placeholder is
# still the only thing holding the lane, and deleting it would hand a working
# lane to the next dispatcher.
#
# THIS FLAG IS A FAST PATH, NOT THE GUARANTEE (agent-dotfiles#209 round 2).
# Round 1 had only this flag, set ~70 lines after the brief was submitted, and
# a signal landing in between freed a working lane. The durable half is now
# step 4.5's `commit-lane-claim`, which moves the row to a status
# `release-lane-claim` is scoped away from -- so even a signal arriving
# between that ledger write and the assignment of this variable is safe, and
# so is a SIGKILL that never lets this shell run anything again.
release_lane_claim() {
  [ -n "${CLAIM_LANE:-}" ] || return 0
  [ -z "${CLAIM_COMMITTED:-}" ] || return 0
  "$LEDGER_PYTHON" "$LEDGER_CLI" release-lane-claim --lane "$CLAIM_LANE" --token "$CLAIM_TOKEN" >/dev/null 2>&1
}

# agent-supervisor#572: the GitHub issue claim (`claim.sh take`, step 2 below)
# is the same shape of held resource the lane claim above is -- and until now
# it had exactly the gap #209 closed for the lane claim: every INTENTIONAL
# refusal this script enumerates (CLAIM_FAILED, a failed worktree, every
# abort_send) already released it inline, but a `kill`, a timeout wrapper, a
# closed terminal or a crashed shell hit none of those lines, same as #209
# found for release_lane_claim. Measured against #571: the issue was left
# assigned by a run that never reached one of the explicit release_claim call
# sites, and every retry after that read "already claimed" -- indistinguishable
# from someone else genuinely working it.
#
# CLAIMED and this function are declared here, BEFORE the trap below, rather
# than at step 2 where the claim itself is taken -- a signal landing before
# step 2 must find `release_claim` already callable and `$CLAIMED` already
# empty (a no-op), not an undefined function. Step 2 (below) only ever
# APPENDS to $CLAIMED; it does not redeclare it.
#
# release_claim itself stays UNGATED -- unlike release_lane_claim, it is not
# only the trap's cleanup, it is also every abort_send's own explicit call,
# and abort_send is reached AFTER step 4.5 too (the swallowed-Enter and
# never-cleared-menu cases: the commit moved before the send on purpose,
# #209 round 2). Those refusals deliberately release the ISSUE claim even
# with CLAIM_COMMITTED set -- an unconfirmed lane stays HELD (nothing may
# free the LANE once the brief might be live), but the ISSUE goes back to
# the pool so another lane can pick it up while this one is investigated;
# "the claim is released when the brief never starts" (test_dispatch.sh)
# pins exactly this asymmetry. Gating release_claim itself on
# CLAIM_COMMITTED would have silently turned that release into a no-op.
#
# release_claim_on_signal, below, is the ONLY thing gated: the trap must not
# release a claim out from under a signal landing while the brief may
# genuinely be live and unconfirmed, the same #102 shape release_lane_claim
# already guards against -- but it has to be a SEPARATE function so the
# explicit abort_send call sites above keep their existing, deliberate
# behaviour unchanged.
CLAIMED=()
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
release_claim_on_signal() {
  [ -z "${CLAIM_COMMITTED:-}" ] || return 0
  release_claim
}

# The claim is a held resource, and every sibling script in this directory
# that holds one guards it with a trap: `advance-live.sh:296`,
# `would-revert.sh:138-140`, `watchdog.sh:428`, `inbox-poll.sh:200,215-217`.
# dispatch.sh had none. Its four inline `release_lane_claim` calls cover the
# four failures it ENUMERATES; a `kill`, a timeout wrapper, a closed terminal
# or a crashed shell are not among them and left the claim behind.
#
# EXIT alone is not enough, for the reason #187 measured on inbox-poll.sh: an
# untrapped SIGTERM reaches the EXIT trap only when bash happens to be waiting
# on a foreground child, and lands as an outright kill otherwise -- and this
# script spends most of its life in `sleep` and `tmux`, so both cases are
# routine. TERM and INT are therefore trapped explicitly.
#
# SIGKILL CANNOT BE TRAPPED BY ANY SHELL, and neither can a host crash. This
# trap does not cover them and does not claim to; step 0.5's reap is what
# covers what the trap cannot, and the two together are the whole of #209's
# cleanup. Neither alone is sufficient. `claim.sh audit`/`reap` (#359) is the
# same second half for the issue claim -- SIGKILL leaves it stale, not
# released, exactly like the lane claim.
#
# And neither is allowed past step 4.5 (agent-dotfiles#209 round 2). A SIGKILL
# AFTER the brief goes live leaves a claim the reap deliberately will not
# clear, so that one case ends at the documented manual recovery rather than
# at an automatic cleanup -- because the alternative is handing the next
# dispatcher a lane with a worker in it, and that is the loss this whole
# subsystem exists to prevent. release_claim_on_signal's own CLAIM_COMMITTED
# gate, above, is the same protection for the issue claim -- the trap below
# calls THAT, never the ungated release_claim directly, or every clean
# successful dispatch's own EXIT would try to release the claim it just
# delivered on.
#
# release_lane_claim and release_claim_on_signal are both idempotent (a
# scoped DELETE/no-CLAIMED-left that matches nothing the second time), so
# the TERM/INT handlers re-entering them via EXIT is a no-op.
trap 'release_claim_on_signal; release_lane_claim' EXIT
trap 'release_claim_on_signal; release_lane_claim; exit 143' TERM   # 128 + 15
trap 'release_claim_on_signal; release_lane_claim; exit 130' INT    # 128 + 2

# agent-dotfiles#199: NOT `declare -A`. macOS ships /bin/bash 3.2, which has
# no associative arrays -- `declare -A` is rejected there and prints
# straight to stderr on every dispatch, which reads like a broken guard on
# the command that decides where work goes. A plain (indexed) array works
# without it: bash auto-vivifies WINDOW_NAME_BY_INDEX on first assignment,
# and every subscript below is a tmux window index (numeric, from
# `lanes.sh`'s own window-index column), so each key keeps its own slot the
# same way an associative array would. This is only safe because the keys
# stay numeric -- a non-numeric key here would silently collapse to index 0
# instead of getting its own slot.
while IFS=$'\t' read -r idx wname; do
  [ -n "$idx" ] || continue
  WINDOW_NAME_BY_INDEX["$idx"]="$wname"
done < <("$HERE/lanes.sh" "$SESSION" 2>/dev/null | awk 'NR>1 && $1 ~ /^[0-9]+$/ {print $1"\t"$2}')

LANE=""
LANE_TARGET=""
CLAIM_LANE=""
LANE_HARNESS=""
AUTHOR_SKIPPED=""
EXCLUSION_LINES=""
SUGGEST_RECORD_COMPLETION=""
SUGGEST_RELEASE_CLAIM=""
json_field() {
  local key="$1" json="$2"
  sed -n "s/.*\"$key\":\\([^,}]*\\).*/\\1/p" <<<"$json" | head -1 | sed -E 's/^"//; s/"$//'
}
append_exclusion() {
  local line="$1"
  EXCLUSION_LINES="${EXCLUSION_LINES}${line}"$'\n'
}
claim_token_from_task() {
  local lane="$1" task="$2" prefix
  prefix="ledger-claim:${lane}:"
  case "$task" in
    "$prefix"*) printf '%s' "${task#"$prefix"}" ;;
    *) printf '' ;;
  esac
}
describe_excluded_lane() {
  local lane="$1" pane_state="$2" diag task status age_base age_minutes token line
  diag=$("$LEDGER_PYTHON" "$LEDGER_CLI" lane-diagnostic --lane "$lane" 2>/dev/null) || diag=""
  task=$(json_field task "$diag")
  status=$(json_field status "$diag")
  [ "$task" = null ] && task=""
  [ "$status" = null ] && status=""

  if [ -z "$diag" ]; then
    append_exclusion "dispatch:   $lane: pane state $pane_state; ledger diagnostic unavailable"
    return
  fi
  if [ -z "$task" ]; then
    if [ "$pane_state" = free ]; then
      append_exclusion "dispatch:   $lane: pane is idle, but no claim could be won"
    else
      append_exclusion "dispatch:   $lane: pane state $pane_state; no open ledger task"
    fi
    return
  fi

  token=$(claim_token_from_task "$lane" "$task")
  line="dispatch:   $lane: "
  if [ "$pane_state" = free ]; then
    line="${line}busy (task $task"
    [ -n "$status" ] && line="${line} $status"
    age_base=$(json_field delivered_at "$diag")
    [ "$age_base" = null ] || [ -n "$age_base" ] || age_base=$(json_field updated_at "$diag")
    if [[ "$age_base" =~ ^[0-9]+$ ]]; then
      age_minutes=$(( ($(date +%s) - age_base) / 60 ))
      [ "$age_minutes" -ge 0 ] && line="${line} ${age_minutes}m ago"
    fi
    line="${line}); pane idle"
    append_exclusion "$line"
    if [ "$status" = delivered ]; then
      SUGGEST_RECORD_COMPLETION="${lane}	${task}"
    fi
  else
    append_exclusion "${line}pane state $pane_state (task $task${status:+ $status})"
  fi

  if [ -n "$token" ] && [ "$status" = created ]; then
    SUGGEST_RELEASE_CLAIM="${lane}	${token}"
  fi
}
# TWO IDENTITIES PER CANDIDATE, AND THEY ANSWER DIFFERENT QUESTIONS (#241).
#
# `$candidate` is `session:<index>` -- the LANE, which is what the ledger
# keys on and what every operator recovery command below names. It is a slot
# number and it must stay one: a lane has to keep its identity across a
# window being closed and recreated, and a window id is destroyed by exactly
# that.
#
# `$candidate_target` is `session:@<id>` -- the TMUX TARGET, and the only
# thing any tmux call below is allowed to be given. tmux window INDICES are
# not stable on this server (`renumber-windows on`, measured in #241):
# closing any window shifts every higher index down by one. The gap between
# resolving a lane here and the final `send-keys Enter` spans a claim, a
# worktree creation and a rename -- "not sub-second the way `claim.sh`'s is",
# as the comment above already says of the ledger race #184 closed. The same
# gap lets an index silently come to mean another pane, and on 2026-08-12
# three briefs landed in windows other than the ones this script reported.
# A window id cannot move: tmux guarantees it for the window's lifetime and
# never reuses it.
while IFS=$'\t' read -r candidate candidate_target; do
  [ -n "$candidate" ] || continue
  # THE EMPTY-TARGET REFUSAL, EXTENDED TO THE NEW SHAPE (#241). `send-keys -t
  # session:` with an empty index does not error -- it targets the ACTIVE
  # window, which is usually the supervisor, and that is the incident
  # loop-tick.md records under "an empty tmux target hits the ACTIVE window".
  # `session:@` is empty in exactly the same way and must be refused exactly
  # as hard, so this is a POSITIVE check on the shape rather than a
  # non-emptiness one: a candidate whose target is not a real `@N` handle is
  # skipped, never guessed at and never fallen back to the index for. A
  # `lanes.sh` that stopped emitting the second column would then dispatch
  # nothing at all, which is the fail-closed direction.
  if [[ ! "$candidate_target" =~ :@[0-9]+$ ]]; then
    echo "dispatch: skipping candidate '$candidate' -- lanes.sh gave no usable window-id target ('${candidate_target:-}')" >&2
    continue
  fi
  # agent-supervisor#668: --adopt-pane narrows this whole search to ONE
  # candidate, the window id the caller named -- every other free lane in
  # $SESSION is skipped rather than being eligible to win this dispatch. If
  # that window never shows up here at all (busy, unknown to the ledger, or
  # simply not a window lanes.sh classifies free), the loop ends with LANE
  # still empty and falls into the ordinary "no free lane" refusal below --
  # the same refusal shape any other dispatch gets, not a special case.
  if [ -n "$ADOPT_PANE_ID" ] && [ "$candidate_target" != "$SESSION:$ADOPT_PANE_ID" ]; then
    continue
  fi
  idx="${candidate##*:}"
  wname="${WINDOW_NAME_BY_INDEX[$idx]:-}"
  # agent-dotfiles#212: excluded BEFORE the ledger's free/occupied query, not
  # inside it -- a candidate that contributed to the PR under review is
  # unsafe regardless of what `lane-free` would say, and this way the
  # exclusion is visible on its own rather than folded into that check's
  # result. An ordinary (non-review) dispatch never populates AUTHOR_LANES
  # and never reaches this branch.
  #
  # agent-supervisor#108: the comparison is `lane_relation`, not string
  # equality. A lane id embeds the session's NAME, and renaming the session
  # (done on 2026-08-14 to recover from #102) changed that name for every
  # window at once -- so a contributor row `agent-dotfiles:3` stopped
  # matching the very same window now called `agent-supervisor:3`, and this
  # guard silently admitted a contributor. Only a POSITIVE `different` --
  # both ids parse and their window indices differ -- lets a candidate
  # through; `same` and `unknown` both exclude, which is the same
  # fail-closed posture step 0.5 already takes when the contributor set
  # cannot be resolved at all.
  #
  # agent-supervisor#190: checked against EVERY lane in the set, not one --
  # `lane_relation` short-circuits on the first that is not positively
  # `different` (a match against ANY contributor is disqualifying), so a
  # candidate that made it through against the first ten contributors but
  # matches the eleventh is still excluded, not admitted by majority.
  if [ ${#AUTHOR_LANES[@]} -gt 0 ]; then
    # agent-supervisor#235: measured HERE, off `$candidate_target` -- the
    # window-id form `lanes.sh` already gave this candidate, immune to the
    # renumber that makes `$candidate`'s INDEX half untrustworthy -- so
    # `lane_relation` below can reconcile against the ledger's `pane_id`
    # registry instead of trusting that index against a contributor's. Empty
    # (tmux gone, target stale between `lanes.sh` and here) is passed through
    # unchanged: `lane_relation` already treats a missing pane id as "cannot
    # widen", falling back to the pre-#235 shape check, never to admission.
    candidate_pane_id=$(tmux display-message -p -t "$candidate_target" '#{pane_id}' 2>/dev/null) || candidate_pane_id=""
    MATCHED_CONTRIBUTOR_LANE=""
    MATCHED_CONTRIBUTOR_TASK=""
    for ai in "${!AUTHOR_LANES[@]}"; do
      al="${AUTHOR_LANES[$ai]}"
      # agent-supervisor#631: this contributor's frozen pane_id, when
      # `resolve_pr_contributors` recorded one -- so the comparison below is
      # against THIS task's own pane, never whatever `$al` currently
      # resolves to in the ledger's `lanes` table.
      al_pane_id="${AUTHOR_PANE_IDS[$ai]:-}"
      if [ "$(lane_relation "$candidate" "$al" "$candidate_pane_id" "$al_pane_id")" != different ]; then
        MATCHED_CONTRIBUTOR_LANE="$al"
        MATCHED_CONTRIBUTOR_TASK="${AUTHOR_TASKS[$ai]}"
        break
      fi
    done
    if [ -n "$MATCHED_CONTRIBUTOR_LANE" ]; then
      if [ "$candidate" = "$MATCHED_CONTRIBUTOR_LANE" ]; then
        echo "dispatch: skipping $candidate -- it contributed task $MATCHED_CONTRIBUTOR_TASK to PR #$REVIEWS_PR under review; a contributor does not review their own work" >&2
      elif [ -n "$LANE_REL_POPULATION_CANDIDATE" ] && [ -n "$LANE_REL_POPULATION_OTHER" ] && [ "$LANE_REL_POPULATION_CANDIDATE" != "$LANE_REL_POPULATION_OTHER" ]; then
        # agent-supervisor#292: the populations differ (one side has a tmux
        # window, the other does not), so the pre-#292 "a session rename
        # changes a lane's name" text would be actively wrong here -- there
        # was never a window on both sides to rename. The ledger's own
        # registry (pane_id) could not tell the two apart either, so this is
        # STILL a refusal, just an honest one about why.
        echo "dispatch: skipping $candidate ($LANE_REL_POPULATION_CANDIDATE) -- it cannot be told apart from contributor lane $MATCHED_CONTRIBUTOR_LANE ($LANE_REL_POPULATION_OTHER, task $MATCHED_CONTRIBUTOR_TASK, contributed to PR #$REVIEWS_PR under review); the ledger has no pane_id record proving these are different lanes" >&2
      else
        echo "dispatch: skipping $candidate -- it cannot be told apart from contributor lane $MATCHED_CONTRIBUTOR_LANE (task $MATCHED_CONTRIBUTOR_TASK, contributed to PR #$REVIEWS_PR under review); a session rename changes a lane's name, not which window it is" >&2
      fi
      AUTHOR_SKIPPED=1
      continue
    fi
  fi
  # #241: `--lane` stays the index (the ledger's slot identity) and `--target`
  # becomes the window id. Before this merge both arguments were `$candidate`,
  # so the ledger recorded an index as the thing to address the window with --
  # which is the defect, one seam later.
  CHECK=$("$LEDGER_PYTHON" "$LEDGER_CLI" lane-free --lane "$candidate" --target "$candidate_target" --window-name "$wname" 2>/dev/null) || continue
  if ! grep -qF '"free":true' <<<"$CHECK"; then
    if grep -qF '"known":false' <<<"$CHECK" && [[ ! "$wname" =~ ^free-[0-9]+$ ]]; then
      append_exclusion "dispatch:   $candidate: pane idle, but unknown to the ledger and window name '$wname' is not the free-N migration shape"
    else
      describe_excluded_lane "$candidate" free
    fi
    continue
  fi

  # Test-only instrumentation (agent-dotfiles#184): when set, run this
  # command with the candidate lane as $1 right after it reads free and
  # before this dispatch claims it -- exactly the gap a second dispatcher
  # would need to land a whole competing dispatch in to prove the race.
  # No caller sets this outside tests/supervisor/test_dispatch.sh.
  if [ -n "${DISPATCH_TEST_RACE_HOOK:-}" ]; then
    "$DISPATCH_TEST_RACE_HOOK" "$candidate" || true
  fi

  # CLAIM_LANE is set BEFORE the claim call, not after it (agent-dotfiles#209).
  # The placeholder row is written INSIDE that call, so assigning afterwards
  # left a real window: a TERM landing while the dispatcher waited on this
  # command substitution ran the trap with CLAIM_LANE still empty and the row
  # already committed -- a stranded claim on the one signal path the trap
  # exists to cover. Naming a lane this dispatch did not win costs nothing:
  # `release_lane_claim` is scoped to (lane, THIS dispatch's token,
  # status='created'), so it matches no row unless the claim really succeeded.
  #
  # `--owner-pid $$` is THIS script's pid, not the `cli.py` child's: the child
  # exits the moment the claim is written, so its pid would read dead
  # instantly and step 0.5's reap would clear a live dispatch's claim. `$$` is
  # the parent shell's pid even inside this command substitution.
  CLAIM_LANE="$candidate"
  CLAIM=$("$LEDGER_PYTHON" "$LEDGER_CLI" claim-lane --lane "$candidate" --token "$CLAIM_TOKEN" --owner-pid $$ 2>/dev/null) || { release_lane_claim; continue; }
  if grep -qF '"claimed":true' <<<"$CLAIM"; then
    LANE="$candidate"
    LANE_TARGET="$candidate_target"
    # agent-dotfiles#216: `lane-free` above already resolved this lane's
    # RECORDED harness (from its @hill90_lane_harness pane option, or the
    # ledger row if it was already known) -- carried forward to step 6 so
    # `record-dispatch` gets an explicit --harness instead of re-guessing one
    # from `#{pane_current_command}`, which cannot tell a Node harness like
    # copilot apart from any other. Empty is possible only if `lane-free`'s
    # own JSON shape ever changes underneath this grep; step 6's existing
    # fallback (HARNESS_BY_COMMAND) covers that, unchanged.
    LANE_HARNESS=$(grep -oE '"harness":"[a-z-]*"' <<<"$CHECK" | head -1 | sed -E 's/.*:"([a-z-]*)"/\1/')
    break
  fi
  claim_reason=$(json_field reason "$CLAIM")
  claim_holder=$(json_field holder "$CLAIM")
  [ "$claim_holder" = null ] && claim_holder=""
  if [ -n "$claim_holder" ]; then
    append_exclusion "dispatch:   $candidate: claim refused ($claim_reason; holder $claim_holder)"
  else
    append_exclusion "dispatch:   $candidate: claim refused ($claim_reason; no holder reported; token '$CLAIM_TOKEN' may already exist)"
  fi
  # Lost this candidate to another dispatcher: move on, exactly as before.
  # The release is a no-op in that case (the row is the winner's, not ours)
  # and only bites when the claim committed but its result did not come back
  # readable -- which would otherwise leak a claim only the reap could clear.
  release_lane_claim
done < <("$HERE/lanes.sh" --free "$SESSION" 2>/dev/null)

if [ -z "$LANE" ]; then
  if [ -n "$ADOPT_PANE_ID" ]; then
    echo "dispatch: --adopt-pane $ADOPT_PANE requested, but window $ADOPT_PANE_ID in session '$SESSION' is not free per lanes.sh/the ledger -- refusing rather than adopt a pane that is not genuinely idle (agent-supervisor#668)" >&2
  fi
  if [ -n "$AUTHOR_SKIPPED" ]; then
    # AUTHOR_TASKS is guaranteed non-empty here: AUTHOR_SKIPPED is only ever
    # set inside the loop above, which only runs its exclusion branch when
    # AUTHOR_LANES (and so its parallel AUTHOR_TASKS) is non-empty.
    # Wording kept verbatim from before #190 ("no free lane other than the
    # author of PR", "an author never reviews their own PR") -- tests predating
    # the widening grep for those exact phrases, and they are still literally
    # true: every excluded candidate matched SOME lane in the contributor set.
    CONTRIBUTOR_TASKS_JOINED=$(IFS=,; echo "${AUTHOR_TASKS[*]}")
    echo "dispatch: no free lane other than the author of PR #$REVIEWS_PR (tasks $CONTRIBUTOR_TASKS_JOINED) -- not dispatching its review #$ISSUE_ARG" >&2
    echo "dispatch: an author never reviews their own PR, even when it is the only free lane" >&2
    if [ -n "$REVIEWS_PR_INFERRED" ]; then
      echo "dispatch: --reviews-pr was never passed; PR #$REVIEWS_PR was INFERRED from $INFERRED_FROM -- if this is not a review, re-run with --not-a-review (agent-supervisor#101)" >&2
    fi
  fi
  echo "dispatch: no free lane in session '$SESSION' -- not dispatching #$ISSUE_ARG" >&2
  echo "dispatch: the ledger must say a lane is free to be dispatchable --" >&2
  echo "dispatch: one it has never seen is backfilled only if named 'free-N'; one it knows is occupied stays occupied regardless of name" >&2
  echo "dispatch: a lane that read free just now may have already been claimed by another dispatcher" >&2

  LANE_ROWS_JSON=$("$HERE/lanes.sh" --json "$SESSION" 2>/dev/null || printf '[]')
  while IFS=$'\t' read -r diag_idx diag_state; do
    [ -n "$diag_idx" ] || continue
    # agent-dotfiles#239: this used to compare $diag_idx to
    # ${LANES_SUPERVISOR_WINDOW:-1} -- a window INDEX, unstable under
    # renumber-windows on, the exact defect this issue is about. lanes.sh is
    # the sole authority on which window is the supervisor's (session-defaults.sh's
    # id-based `supervisor_window_id`, #239's fix there) and already emits
    # `state=supervisor` for it; asking IT rather than re-deriving a second,
    # independently stale answer here is the fix, not a workaround.
    [ "$diag_state" = supervisor ] && continue
    [ "$diag_state" = free ] && continue
    describe_excluded_lane "$SESSION:$diag_idx" "$diag_state"
  done < <(printf '%s' "$LANE_ROWS_JSON" | "$LEDGER_PYTHON" -c 'import json,sys
for row in json.load(sys.stdin):
    print("{}\t{}".format(row.get("window", ""), row.get("state", "")))' 2>/dev/null)

  if [ -n "$EXCLUSION_LINES" ]; then
    echo "dispatch: lane exclusion diagnostics:" >&2
    printf '%s' "$EXCLUSION_LINES" >&2
  else
    echo "dispatch: lane exclusion diagnostics: no lane rows were readable from lanes.sh" >&2
  fi

  if [ -n "$SUGGEST_RECORD_COMPLETION" ]; then
    completion_lane="${SUGGEST_RECORD_COMPLETION%%	*}"
    completion_task="${SUGGEST_RECORD_COMPLETION#*	}"
    echo "dispatch: suggested recovery: inspect $completion_lane; if task $completion_task finished but never signalled, run:" >&2
    echo "dispatch:   $LEDGER_PYTHON $LEDGER_CLI record-completion --lane $completion_lane --note '<what finished>'" >&2
  elif [ -n "$SUGGEST_RELEASE_CLAIM" ]; then
    claim_lane="${SUGGEST_RELEASE_CLAIM%%	*}"
    claim_token="${SUGGEST_RELEASE_CLAIM#*	}"
    echo "dispatch: suggested recovery: $LEDGER_PYTHON $LEDGER_CLI release-lane-claim --lane $claim_lane --token $claim_token" >&2
  else
    echo "dispatch: no ledger surgery suggested; inspect or wait on panes whose state is not ready" >&2
  fi
  "$HERE/lanes.sh" "$SESSION" >&2
  exit 1
fi

# The refusal above is about there being no lane. This one is about not
# knowing WHERE the lane is, and it is the same guard the loop applies per
# candidate, restated once for the winner so that no path can reach a tmux
# call with an unusable target (#241). Nothing has been claimed on GitHub or
# created on disk yet, so refusing here is still free -- and the alternative
# is `send-keys -t session:` landing in the active window, which is the
# supervisor.
if [[ ! "$LANE_TARGET" =~ :@[0-9]+$ ]]; then
  echo "dispatch: lane $LANE has no usable tmux window-id target ('${LANE_TARGET:-}') -- not dispatching #$ISSUE_ARG" >&2
  echo "dispatch: an empty or index-shaped target is refused: an empty tmux target hits the ACTIVE window, which is the supervisor" >&2
  release_lane_claim
  exit 1
fi

# --- 1.5. flip the default (agent-supervisor#171): a fresh `claude` dispatch
# goes over `claude-print`, not this candidate's tmux pane ------------------
#
# THE ROLES THAT MUST KEEP A LIVE PANE, NAMED HERE, IN CODE (#171's own
# brief asks for this, not left in a comment elsewhere): a lane that must be
# INTERRUPTED mid-turn, a lane that must answer an INTERACTIVE PROMPT (a
# usage-limit dialog, a permission request, a menu), or a lane WATCHED AND
# RESUMED BY A HUMAN directly -- `cli.py`'s own `adapter_for_harness` comment
# names the same three. dispatch.sh's own job -- claim an issue, hand a lane
# a brief, collect a PR -- is never one of them (#255 measured this: "almost
# everything in this estate is dispatch-and-collect"), and there is no lane
# PROPERTY this script can read back out of a pane to auto-detect the three
# roles above -- only the human dispatching knows a given call is one of
# them. So `--live-pane` is a CALLER decision (this dispatch keeps the old
# tmux flow below, unchanged), never something inferred from the pane.
#
# WHY THE CANDIDATE IS RELEASED, NOT USED: `$LANE`/`$LANE_TARGET` are a real
# tmux pane from the fixed, standing pool -- one of the "existing lanes"
# #171's brief says not to touch. Routing THIS dispatch over `claude-print`
# instead means that pane is never respawned, never sent a keystroke, and
# never recorded against -- `release_lane_claim` (already wired to this
# script's EXIT trap) clears its ledger claim the moment this branch exits,
# leaving it exactly as free as it was before the loop above found it, for
# a later dispatch (one that passes --live-pane) to actually use.
#
# WHY GATED TO A PLAIN, SINGLE-ISSUE, NON-PR-SCOPED DISPATCH:
# `dispatch-claude-print.sh` (the mechanism this calls into) does not speak
# `--reviews-pr`'s author-exclusion bookkeeping, `--pr`'s PR-scoped source
# recording, or a comma-joined multi-issue list -- see its own usage
# comment. Falling through to the pre-#171 tmux flow for those three shapes
# is a known, tracked scope boundary (agent-supervisor#171), not a silent
# fallback: nothing has failed yet at this point, so this is a routing
# choice made BEFORE any commitment, the opposite of the forbidden kind of
# fallback (a REAL claude-print failure below never falls back to send-keys
# -- see the `exit $?` a few lines down and dispatch-claude-print.sh's own
# fail-closed header).
if [ "$LANE_HARNESS" = claude ] && [ -z "$LIVE_PANE" ] \
    && [ "${#ISSUES[@]}" -eq 1 ] && [ -z "$REVIEWS_PR" ] && [ -z "$PR" ]; then
  # `dispatch-claude-print.sh` requires <repo> non-empty (its own usage
  # error otherwise); dispatch.sh itself allows [repo] to be omitted and
  # left for `gh`/`claim.sh` to resolve from the working directory. Resolved
  # here the same way, from $REPO_PATH -- and if it cannot be resolved, this
  # falls through to the pre-#171 tmux flow rather than refusing the whole
  # dispatch over a routing decision, not a failure.
  CLAUDE_PRINT_REPO="$REPO"
  if [ -z "$CLAUDE_PRINT_REPO" ]; then
    CLAUDE_PRINT_REPO=$(cd "$REPO_PATH" 2>/dev/null && gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null) || CLAUDE_PRINT_REPO=""
  fi
  if [ -z "$CLAUDE_PRINT_REPO" ]; then
    echo "dispatch: claude lane $LANE selected, but [repo] could not be resolved for claude-print -- falling through to $LANE's own tmux pane for #$ISSUE_ARG (agent-supervisor#171)" >&2
  elif ! command -v claude >/dev/null 2>&1; then
    # FAIL CLOSED AND LOUDLY (#171's own guard): no `claude` binary on PATH
    # means the new default cannot run at all -- this refuses the dispatch
    # rather than silently falling back to $LANE's tmux pane, which would
    # make the ledger's absence of a claude-print row look like a choice
    # instead of a missing binary. `--live-pane` is the way to actually ask
    # for the tmux pane; a missing binary is not that ask.
    echo "dispatch: claude lane $LANE selected but no 'claude' binary on PATH -- refusing rather than falling back to send-keys (agent-supervisor#171); #$ISSUE_ARG was NOT dispatched" >&2
    exit 1
  else
    echo "dispatch: claude lane $LANE selected -- routing #$ISSUE_ARG over claude-print instead (agent-supervisor#171); $LANE stays free for --live-pane work" >&2
    # agent-supervisor#617: --force was parsed into COLLISION_FORCE above and
    # forwarded to the tmux flow's own collision-check call below (:1735),
    # but this claude-print flow -- the DEFAULT for a plain single-issue
    # dispatch (#171) -- dropped it on the floor: dispatch-claude-print.sh
    # runs its own collision check and only honours --force if it is on
    # dispatch-claude-print.sh's OWN argv. Not forwarding it here made the
    # documented escape hatch unreachable on the common path.
    "$HERE/dispatch-claude-print.sh" "$ISSUE_ARG" "$SLUG" "$BRIEF" "$CLAUDE_PRINT_REPO" "$REPO_PATH" ${COLLISION_FORCE:+--force}
    exit $?
  fi
fi
