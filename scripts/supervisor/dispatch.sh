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
# REFINEMENT (agent-dotfiles#222): the rule above has an opposite failure
# mode, not just the nothing-calls-it one. An abstraction can be present and
# CORRECTLY avoided. When callers route around a seam because its
# implementation is worse than the ad-hoc code it would replace, that is
# indistinguishable from outside from the nothing-calls-it defect above --
# and is its opposite. Wiring the caller in "fixes" it by importing the
# defect. The test for an adapter is "is the implementation fit to be
# called?", not "is there a caller?". When the answer is no, the avoidance
# must be recorded at the seam, not only in whichever caller dodged it. Live
# instance: adapter.classify_capture's header comment, avoided by this very
# script's dispatch path -- see that comment for the mechanics.
#
# EVERY FAILURE ABORTS THE DISPATCH. In particular a failed `worktree.sh new`
# is fatal: a lane with no worktree works in the shared checkout, and that is
# the original bug, not a degraded mode of operation. Whatever was already
# done -- the claim, the worktree -- is undone before exiting, so a failed
# dispatch leaves the estate exactly as it found it and the issue stays
# available to the next tick.
#
# Usage:
#   dispatch.sh <issue>[,<issue>...] <slug> <brief-file> [repo] [repo-path] [--reviews-pr <PR>]
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
# --reviews-pr <PR>
#              this dispatch is a review of PR <PR>. dispatch.sh (#212) then
#              refuses any candidate lane that authored that PR's branch,
#              fails closed if authorship cannot be determined at all, and
#              proceeds unchanged if the flag is omitted -- see step 0.5.
#
# Exit 0 only when a lane has been sent a brief. Exit 1 on any refusal --
# no free lane, an issue someone else already claimed, a worktree that could
# not be created, a send that failed, or a review whose only free lane wrote
# the PR under review.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./input-box.sh
. "$HERE/input-box.sh"
SESSION="${LANES_SESSION:-agent-dotfiles}"

# `--reviews-pr <PR>` is pulled out wherever it appears rather than bound to a
# fixed position: every other argument here is positional and some of them
# (repo, repo-path) are already optional, so a new optional flag is scanned
# out first and the remaining args keep their existing $1..$5 meaning
# untouched. This is deliberately NOT `DISPATCH_LANE` reborn under a new name
# -- see the lane-selection loop below -- it names which PR is under review,
# it never names which lane to use.
REVIEWS_PR=""
POSITIONAL=()
while [ $# -gt 0 ]; do
  case "$1" in
    --reviews-pr)
      # A `--reviews-pr` with no value after it (flag last, value forgotten)
      # must refuse rather than hang: `shift 2` with only 1 arg left ($#=1)
      # fails under `set -uo pipefail` (no `set -e` here), and a `case` loop
      # that never shifts on a failed shift spins at ~100% CPU forever --
      # indistinguishable from outside from a lane still working. Refusing
      # loudly here is also why this is a dedicated check rather than
      # `shift 2 || shift`: silently falling back to `shift 1` would make the
      # flag consume the next positional argument (e.g. the brief path) as
      # its value instead, which is its own defect, not a fix for this one.
      if [ $# -lt 2 ]; then
        echo "dispatch: --reviews-pr requires a PR number" >&2
        sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2
        exit 1
      fi
      REVIEWS_PR="$2"
      shift 2
      ;;
    *)
      POSITIONAL+=("$1")
      shift
      ;;
  esac
done
# bash 3.2 (macOS's real /bin/bash, no associative arrays -- see #213's
# `declare -A` removal in this same file) treats "${arr[@]}" on an EMPTY
# array as an unbound-variable error under `set -u`, even though modern bash
# treats it as zero words. POSITIONAL is empty on dispatch.sh's own
# zero-argument path (the usage-error branch just below), which every
# invocation with a typo hits, so the 3.2-safe idiom is required here, not
# optional: "${arr[@]+"${arr[@]}"}" expands to nothing when the array is
# empty (the `+` alternate-value test never triggers) and to the array's
# words otherwise.
set -- "${POSITIONAL[@]+"${POSITIONAL[@]}"}"

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

# --- 0. the ledger must be readable before any lane is trusted ------------
# agent-dotfiles#174. Everything below this line asks the LEDGER whether a
# lane is free, not the window name -- the whole point of the change. That
# only holds if the ledger itself can be read at all: an unreadable ledger
# answering nothing must mean "cannot tell what is free", never "assume
# everything is free". This is the inverse of #140's original ledger WRITE,
# which was made non-fatal precisely because nothing read it yet (see step 6
# below for where that reasoning still applies, and why).
#
# Checked once, up front, rather than folded into the per-candidate query in
# step 1: a broken ledger fails every one of those queries identically, and
# diffusing the same failure across a loop would report it as "no free lane"
# -- true, but not why, and indistinguishable from an estate that is
# genuinely full.
LEDGER_PYTHON="${DISPATCH_PYTHON:-python3}"
LEDGER_CLI="$HERE/cli.py"
if ! LEDGER_STATUS_OUT=$("$LEDGER_PYTHON" "$LEDGER_CLI" status 2>&1); then
  echo "dispatch: the ledger is unreadable -- refusing to dispatch #$ISSUE_ARG" >&2
  echo "dispatch: cannot tell which lanes are free without it, so nothing is safe to pick" >&2
  sed 's/^/  /' <<<"$LEDGER_STATUS_OUT" >&2
  exit 1
fi

# --- 0.5 clear claims whose dispatcher died where nothing could clean up ---
# agent-dotfiles#209. Step 1's claim is released on every abort path below and
# by the EXIT/TERM/INT trap installed with it -- but SIGKILL, an OOM kill and
# a host crash cannot be trapped by any shell, and a dispatcher lost that way
# leaves its placeholder behind holding a lane that nothing is working. The
# ledger reads that lane occupied forever, which is #102's exact shape
# (dispatch capacity silently falling to zero while lanes sit idle) reached
# through the mechanism that exists to prevent it.
#
# `reap-lane-claims` removes only claim placeholders whose recorded owner pid
# is provably gone on this host -- not a TTL, which could not tell a slow
# dispatch from a dead one and would reopen #184's race by expiring a live
# claim (see `Ledger.reap_stale_lane_claims`). So this cannot make a lane
# available that was not already unowned, which is #124/#126's ratchet.
#
# HERE rather than in a new daemon, and here rather than the watchdog: the
# dispatcher is the only thing that reads lane availability to act on it, so
# it is where a stranded claim actually costs something, and running the reap
# immediately before selection means capacity comes back on the very next
# dispatch attempt instead of on some sweep's schedule.
#
# NEVER FATAL. A reap that fails leaves exactly the state that existed before
# this block -- some lanes stranded -- and refusing to dispatch over it would
# turn a partial capacity loss into a total one.
if REAP_OUT=$("$LEDGER_PYTHON" "$LEDGER_CLI" reap-lane-claims 2>&1); then
  if ! grep -qF '"count":0' <<<"$REAP_OUT"; then
    echo "dispatch: cleared stranded lane claim(s) whose dispatcher is gone: $REAP_OUT" >&2
  fi
else
  echo "dispatch: WARNING -- could not reap stranded lane claims; continuing" >&2
  sed 's/^/  /' <<<"$REAP_OUT" >&2
fi

# --- 0.5. a review must not land on the lane that wrote what it reviews ---
# agent-dotfiles#212. On 2026-08-12 a review of #204 was dispatched to lane
# 4, the same lane that had written the code under review (ad193/ad204),
# and its APPROVE had to be thrown away. This is that refusal, built the way
# #174 requires: BY LEDGER RECORD, never by window name -- for a lane the
# ledger already knows, `cli.py lane_free` answers from the ledger alone and
# the window name is never consulted (see step 1's own comment), so a name
# cannot be used to steer a review away from its author either.
#
# Only runs when the caller says this dispatch IS a review, via
# `--reviews-pr`. Ordinary (non-review) dispatches are unaffected -- there is
# no author to avoid.
#
# THE MAPPING THIS RELIES ON, verified against the code that writes both
# sides of it rather than assumed:
#   * `worktree.sh new "${ISSUE}-${SLUG}"` (step 3 below) creates branch
#     `lane/${ISSUE}-${SLUG}` -- literally `BRANCH="lane/$SLUG"` in
#     worktree.sh, where the caller's $SLUG here is already `$ISSUE-$SLUG`.
#   * Step 6 below records that same dispatch's task under id
#     `$WINDOW_NAME`, i.e. `${PREFIX}${ISSUE}-${SLUG}` -- and `tasks.id` is a
#     SQLite PRIMARY KEY; `Ledger._assign_tx` raises rather than let a second
#     lane reuse an existing task id (core.py), so a task id's `lane` column
#     never silently changes owner once written.
# So `lane/<n>-<slug>` on a PR's head branch and task id `${PREFIX}<n>-<slug>`
# are two spellings of the same fact, produced by the same dispatch, and the
# second is a permanent ledger row. Neither spelling includes REPO's OWNER,
# so this check only makes sense within one repo -- exactly what a single
# `dispatch.sh` invocation already is.
#
# FAILS CLOSED throughout: `gh` unreachable, a PR with no head branch, a head
# branch outside the `lane/<n>-<slug>` convention (hand-pushed, or from
# before #81 wired worktrees into every dispatch), or a task id the ledger
# has no row for -- every one of these means authorship cannot be
# determined, and this refuses the WHOLE dispatch rather than guess. A
# candidate lane is only ever excluded, never assumed innocent from missing
# data.
AUTHOR_LANE=""
AUTHOR_TASK=""
if [ -n "$REVIEWS_PR" ]; then
  GH_REPO_ARGS=()
  [ -n "$REPO" ] && GH_REPO_ARGS=(-R "$REPO")
  # Same bash 3.2 empty-array hazard as POSITIONAL above: [repo] is
  # documented as optional on this exact flag (`--reviews-pr` with [repo]
  # omitted), so GH_REPO_ARGS is empty on that path and "${GH_REPO_ARGS[@]}"
  # alone would abort under 3.2 before `gh` ever runs.
  PR_JSON=$(gh pr view "$REVIEWS_PR" "${GH_REPO_ARGS[@]+"${GH_REPO_ARGS[@]}"}" --json headRefName 2>&1)
  if [ $? -ne 0 ]; then
    echo "dispatch: cannot read PR #$REVIEWS_PR -- refusing to dispatch its review (authorship unknown, failing closed)" >&2
    sed 's/^/  /' <<<"$PR_JSON" >&2
    exit 1
  fi
  HEAD_REF=$(sed -n 's/.*"headRefName":"\([^"]*\)".*/\1/p' <<<"$PR_JSON")
  if [ -z "$HEAD_REF" ]; then
    echo "dispatch: PR #$REVIEWS_PR's head branch is unreadable -- refusing to dispatch its review (authorship unknown, failing closed)" >&2
    exit 1
  fi
  if [[ "$HEAD_REF" =~ ^lane/([0-9]+)-(.+)$ ]]; then
    AUTHOR_TASK="${PREFIX}${BASH_REMATCH[1]}-${BASH_REMATCH[2]}"
  else
    echo "dispatch: PR #$REVIEWS_PR's branch '$HEAD_REF' is not a lane/<issue>-<slug> branch -- cannot determine its author, refusing (authorship unknown, failing closed)" >&2
    exit 1
  fi
  AUTHOR_JSON=$("$LEDGER_PYTHON" "$LEDGER_CLI" task-lane --task "$AUTHOR_TASK" 2>&1)
  if [ $? -ne 0 ]; then
    echo "dispatch: could not query the ledger for task $AUTHOR_TASK -- refusing to dispatch the review of PR #$REVIEWS_PR (authorship unknown, failing closed)" >&2
    sed 's/^/  /' <<<"$AUTHOR_JSON" >&2
    exit 1
  fi
  if grep -qF '"known":true' <<<"$AUTHOR_JSON"; then
    AUTHOR_LANE=$(sed -n 's/.*"lane":"\([^"]*\)".*/\1/p' <<<"$AUTHOR_JSON")
  fi
  if [ -z "$AUTHOR_LANE" ]; then
    echo "dispatch: the ledger has no record of task $AUTHOR_TASK, the task PR #$REVIEWS_PR's branch names as its author -- refusing (authorship unknown, failing closed)" >&2
    exit 1
  fi
fi

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
# cleanup. Neither alone is sufficient.
#
# And neither is allowed past step 4.5 (agent-dotfiles#209 round 2). A SIGKILL
# AFTER the brief goes live leaves a claim the reap deliberately will not
# clear, so that one case ends at the documented manual recovery rather than
# at an automatic cleanup -- because the alternative is handing the next
# dispatcher a lane with a worker in it, and that is the loss this whole
# subsystem exists to prevent.
#
# release_lane_claim is idempotent (a scoped DELETE that matches no row the
# second time), so the TERM/INT handlers re-entering it via EXIT is a no-op.
trap release_lane_claim EXIT
trap 'release_lane_claim; exit 143' TERM   # 128 + 15
trap 'release_lane_claim; exit 130' INT    # 128 + 2

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
  idx="${candidate##*:}"
  wname="${WINDOW_NAME_BY_INDEX[$idx]:-}"
  # agent-dotfiles#212: excluded BEFORE the ledger's free/occupied query, not
  # inside it -- a candidate that authored the PR under review is unsafe
  # regardless of what `lane-free` would say, and this way the exclusion is
  # visible on its own rather than folded into that check's result. An
  # ordinary (non-review) dispatch never sets AUTHOR_LANE and never reaches
  # this branch.
  if [ -n "$AUTHOR_LANE" ] && [ "$candidate" = "$AUTHOR_LANE" ]; then
    echo "dispatch: skipping $candidate -- it authored task $AUTHOR_TASK, the PR #$REVIEWS_PR under review; an author does not review their own work" >&2
    AUTHOR_SKIPPED=1
    continue
  fi
  # #241: `--lane` stays the index (the ledger's slot identity) and `--target`
  # becomes the window id. Before this merge both arguments were `$candidate`,
  # so the ledger recorded an index as the thing to address the window with --
  # which is the defect, one seam later.
  CHECK=$("$LEDGER_PYTHON" "$LEDGER_CLI" lane-free --lane "$candidate" --target "$candidate_target" --window-name "$wname" 2>/dev/null) || continue
  grep -qF '"free":true' <<<"$CHECK" || continue

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
  # Lost this candidate to another dispatcher: move on, exactly as before.
  # The release is a no-op in that case (the row is the winner's, not ours)
  # and only bites when the claim committed but its result did not come back
  # readable -- which would otherwise leak a claim only the reap could clear.
  release_lane_claim
done < <("$HERE/lanes.sh" --free "$SESSION" 2>/dev/null)

if [ -z "$LANE" ]; then
  if [ -n "$AUTHOR_SKIPPED" ]; then
    echo "dispatch: no free lane other than the author of PR #$REVIEWS_PR (task $AUTHOR_TASK) -- not dispatching its review #$ISSUE_ARG" >&2
    echo "dispatch: an author never reviews their own PR, even when it is the only free lane" >&2
  fi
  echo "dispatch: no free lane in session '$SESSION' -- not dispatching #$ISSUE_ARG" >&2
  echo "dispatch: the ledger must say a lane is free to be dispatchable --" >&2
  echo "dispatch: one it has never seen is backfilled only if named 'free-N'; one it knows is occupied stays occupied regardless of name" >&2
  echo "dispatch: a lane that read free just now may have already been claimed by another dispatcher" >&2
  # agent-dotfiles#209, following `lane-done.sh`'s precedent of naming its own
  # recovery command in the refusal itself rather than leaving an operator to
  # reconstruct it. Step 0.5's reap has ALREADY run by the time this prints,
  # so anything still held here is held by something the reap will not touch:
  # a claim whose owner pid is still alive (or has been recycled), a claim
  # written on a different host, or a `ledger-hold:` row from a failed
  # `record_dispatch` (#188) that is waiting on a human by design.
  #
  # agent-dotfiles#209 round 2 adds a FOURTH case that must be named here,
  # because it is the one the fail-closed reordering deliberately creates and
  # `release-lane-claim` deliberately will not clear: a claim marked live
  # (status `delivered`) whose dispatcher then died or aborted. Telling an
  # operator to run a command that silently matches no row would be worse than
  # printing nothing, so the status is what selects the command.
  echo "dispatch: if this is wrong and a lane is held by a claim nobody owns:" >&2
  echo "dispatch:   1. $LEDGER_PYTHON $LEDGER_CLI status   # look for '\"id\":\"ledger-claim:<lane>:<token>\"' -- the id IS the lane and token" >&2
  echo "dispatch:   2. $LEDGER_PYTHON $LEDGER_CLI release-lane-claim --lane <lane> --token <token>" >&2
  echo "dispatch: that clears a claim still at status 'created' -- one that never sent anything." >&2
  echo "dispatch: a claim at status 'delivered' is a claim with a live brief behind it: its dispatcher got as far as submitting" >&2
  echo "dispatch: into the pane, so release-lane-claim will NOT touch it (that is the guard, not a bug). If the pane really is idle:" >&2
  echo "dispatch:   $LEDGER_PYTHON $LEDGER_CLI cancel-open-task --lane <lane>" >&2
  echo "dispatch: a lane held by a 'ledger-hold:' row instead is a failed ledger record awaiting reconciliation, not a stranded claim --" >&2
  echo "dispatch: clear that one with the same cancel-open-task --lane <lane>   (frees whatever outstanding task owns the lane)" >&2
  echo "dispatch: CHECK THE PANE FIRST. All of these make the lane dispatchable again; on a lane that is actually working, that is #102." >&2
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

# --- 4. the lane is told what it is doing, then given the work ------------
if ! tmux rename-window -t "$LANE_TARGET" "$WINDOW_NAME" 2>/dev/null; then
  echo "dispatch: could not rename $LANE -- not dispatching #$ISSUE_ARG" >&2
  "$HERE/worktree.sh" done "$WORKTREE" >/dev/null 2>&1
  release_claim
  release_lane_claim
  exit 1
fi

abort_send() {
  echo "dispatch: $1" >&2
  "$HERE/worktree.sh" done "$WORKTREE" >/dev/null 2>&1
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
    echo "dispatch: CHECK THE PANE. If nothing is running there, free it with:" >&2
    echo "dispatch:   $LEDGER_PYTHON $LEDGER_CLI cancel-open-task --lane $LANE" >&2
  fi
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

# agent-dotfiles#237: the instant BEFORE the `/clear` that starts this lane's
# new conversation. `harness_session.py` uses it to tell the transcript this
# dispatch created from every other transcript on the machine -- see that
# module for why "began after this moment" is one of the three tests it
# requires, and why nothing weaker resolves a lane in this estate.
DISPATCH_SEND_EPOCH=$(date +%s)

# `/clear` first: an author reviewing their own PR is not an independent
# reviewer, and a lane carrying the last task's context is not a fresh one.
tmux send-keys -t "$LANE_TARGET" "/clear" Enter 2>/dev/null \
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
  tmux send-keys -t "$LANE_TARGET" "$MESSAGE" 2>/dev/null \
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
  pane=$(tmux capture-pane -p -t "$LANE_TARGET" 2>/dev/null | tr -d ' \n')
  if grep -qF "$(tr -d ' ' <<<"Read $BRIEF")" <<<"$pane" \
     && grep -qF "$(tr -d ' ' <<<"$WORKTREE")" <<<"$pane" \
     && grep -qF "$(tr -d ' ' <<<"never work in the shared checkout at $REPO_PATH.")" <<<"$pane"; then
    sent=1
    break
  fi
  # Clear whatever partial text is in the input and retype once.
  tmux send-keys -t "$LANE_TARGET" C-u 2>/dev/null
  sleep "${DISPATCH_SETTLE:-1}"
done

[ "$sent" = 1 ] || abort_send "the brief did not land intact in $LANE -- #$ISSUE_ARG was NOT dispatched (check the pane by hand)"

# --- 4.5 THE POINT OF NO RETURN, AND IT IS THE SEND -----------------------
# agent-dotfiles#209 round 2. `CLAIM_COMMITTED` used to be set ~70 lines below
# this, after step 5's confirmation loop -- and step 5 costs up to
# DISPATCH_CONFIRM_TRIES x DISPATCH_SETTLE (10s by default) of wall clock. So
# for that whole window the lane was renamed and the brief was ABOUT to be
# live, while both cleanup paths still believed the dispatch was unwindable. A
# SIGTERM landing there ran the trap and deleted the claim, and
# `lane_available` answered True for a lane that was actively working -- #102's
# shape produced BY the cleanup, which step 6's own comment below says in its
# own words must not happen. Reproduced against the stubs, both directions, in
# tests/supervisor/test_dispatch.sh.
#
# So the commit happens HERE, before the Enter rather than after the
# confirmation, because "the brief is live" starts at the submit and not at
# the moment the bookkeeping finishes noticing.
#
# IT IS WRITTEN TO THE LEDGER, not just to this shell. `CLAIM_COMMITTED` below
# is only a fast path for the trap; it dies with this process, and the SIGKILL
# case is exactly the one where this process stops existing while the pane
# keeps working. `commit-lane-claim` moves the placeholder to a status both
# `release_lane_claim` and `reap-lane-claims` refuse to touch, so the
# protection survives a kill that no shell can trap. That ordering also means
# a signal arriving BETWEEN the ledger write and the assignment below is
# already safe: the trap's release matches no row.
#
# WHAT THE REORDERING COSTS, stated rather than discovered later. From here
# every failure leaves the lane HELD -- including a send that fails outright,
# and step 5 concluding the brief never left the input box. Those used to free
# the lane. That is deliberate and it is the fail-closed direction: a lane
# wrongly held costs capacity and is recovered by the documented command the
# "no free lane" refusal prints; a lane wrongly freed costs a running lane's
# work and is recovered by nothing. `lanes.sh` still shows such a lane
# `unsent`, so the cost is visible rather than silent.
#
# FATAL IF IT FAILS, and that is the same argument pointing the other way:
# nothing has gone into the pane yet, so refusing is still free, and sending a
# brief we could not first mark as live would leave the exact window this
# block exists to close.
COMMIT_OUT=$("$LEDGER_PYTHON" "$LEDGER_CLI" commit-lane-claim --lane "$LANE" --token "$CLAIM_TOKEN" 2>&1) \
  || COMMIT_OUT="${COMMIT_OUT:-commit-lane-claim failed to run}"
if ! grep -qF '"committed":true' <<<"$COMMIT_OUT"; then
  sed 's/^/  /' <<<"$COMMIT_OUT" >&2
  abort_send "could not mark $LANE's claim live before sending -- #$ISSUE_ARG was NOT dispatched (nothing was submitted)"
fi
CLAIM_COMMITTED=1

tmux send-keys -t "$LANE_TARGET" Enter 2>/dev/null \
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
#
# LATENCY: this loop adds ~DISPATCH_SETTLE (default 1s) to every dispatch,
# even one that lands instantly, because the first sleep runs before the
# first check -- and up to DISPATCH_CONFIRM_TRIES x DISPATCH_SETTLE (10s by
# default) to a slow-confirming one. That is the price of #141: it is what
# turns "the dispatcher printed success" into "the box actually went empty",
# so do not tune DISPATCH_CONFIRM_TRIES down to make dispatch feel faster
# without understanding that the loop is what makes an unsent brief
# detectable instead of silent.
CONFIRM_TRIES="${DISPATCH_CONFIRM_TRIES:-10}"
submitted=""
box=""
for ((attempt = 1; attempt <= CONFIRM_TRIES; attempt++)); do
  sleep "${DISPATCH_SETTLE:-1}"
  box=$(tmux capture-pane -pe -t "$LANE_TARGET" 2>/dev/null | input_box_state)
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
  if [ -n "$LANE_HARNESS" ]; then
    if ! HARNESS_SESSION_ID=$("${DISPATCH_PYTHON:-python3}" "$HERE/harness_session.py" \
        --harness "$LANE_HARNESS" \
        --marker "$WORKTREE" \
        --since "$DISPATCH_SEND_EPOCH" \
        --timeout "${DISPATCH_SESSION_TIMEOUT:-20}" 2>&1); then
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
    --github "$REPO"
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
  if ! LEDGER_OUT=$("${DISPATCH_PYTHON:-python3}" "$HERE/cli.py" "${LEDGER_ARGS[@]}" 2>&1); then
    ledger_record_failed "$LEDGER_OUT"
  fi
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
