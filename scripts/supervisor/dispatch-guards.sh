#!/bin/bash
# Sourced-only, from dispatch.sh, right after dispatch-preflight.sh. Part of
# the agent-supervisor#716 split (see dispatch-rehome.sh's own header for the
# shape and precedent). Never run standalone.
#
# The ledger-backed guards that must all clear before step 1 picks a lane:
# the ledger-readable check, `lane_relation()` (shared by the author-exclusion
# loop below and by dispatch-lane-select.sh's candidate loop), the supervisor
# lease check (agent-dotfiles#238), the stale-lane-claim reap (#209), the
# review author-exclusion / PR-contributor resolution (#212/#190/#308), and
# the "PR already claimed by a lane" check (#159/#169). Sources
# resolve-pr-contributors.sh itself, at the point it is first needed, same as
# dispatch.sh did before this split.
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
# agent-supervisor#108: "are these two lane ids the same lane?" is answered by
# `core.lane_relation` through the ledger CLI, never by `=` here. Two callers
# ask it (this script's author-exclusion guard, and digest.sh's independence
# report) and they must not disagree; a bash string comparison in either one is
# how they came to disagree with the ledger in the first place.
#
# Anything that is not a positively parsed answer -- python missing, cli.py
# broken, JSON in a shape this cannot read -- prints `unknown`, and every
# caller treats `unknown` as "do not admit". Failing to run the check must
# never read as permission.
#
# agent-supervisor#292: `cli.py lane-relation` now ALSO widens a shape-check
# `unknown` through the ledger's registry (a claude-print/pi-rpc lane has no
# `<session>:<index>` to compare, so the shape check alone can never place it
# positive of anything -- see `core.lane_relation_from_rows`'s own comment),
# and on any answer that is still not `different` it names which POPULATION
# each side is in (`lane_population`/`other_population` in the JSON) so a
# refusal can say WHY, not just THAT. Both are stashed here as a side effect
# -- `LANE_REL_POPULATION_CANDIDATE`/`LANE_REL_POPULATION_OTHER` -- so the
# skip message below does not need a second round-trip to explain itself.
# Empty when the relation is `different` (never needed there) or the JSON
# carried no such field (an older cli.py; still safe, just less specific).
LANE_REL_POPULATION_CANDIDATE=""
LANE_REL_POPULATION_OTHER=""
lane_relation() {  # lane_relation <lane> <other> [lane-pane-id] [other-pane-id] -> same|different|unknown
  # agent-supervisor#235: the optional third argument is a LIVE pane id the
  # caller just measured off tmux for `$1` -- see the author-exclusion loop
  # below, which is the one caller that has a real tmux target to measure.
  # Reconciled through the ledger's `pane_id` registry INSTEAD OF the
  # `<session>:<index>` shape check, which trusts the window INDEX half of
  # `$1` and is exactly what `renumber-windows on` (Jon's tmux setting)
  # rewrites out from under a lane the instant a lower window closes -- see
  # `core.py`'s own comment on `cli.py lane-relation --lane-pane-id`.
  #
  # agent-supervisor#631: the optional FOURTH argument is `$2`'s own FROZEN
  # pane id -- a contributor task's `tasks.pane_id` snapshot
  # (`AUTHOR_PANE_IDS`, see the author-exclusion loop below) -- used INSTEAD
  # of re-resolving `$2` through the ledger's mutable `lanes` table by
  # string. That live lookup is exactly what a later, unrelated dispatch can
  # silently overwrite for a contributor's OLD lane string once
  # `renumber-windows on` hands it to a different pane: `$2` would then
  # answer for the NEW occupant, not the historical contributor this
  # comparison is actually about.
  local json rel lane_pane_id_args=() other_pane_id_args=()
  [ -z "${3:-}" ] || lane_pane_id_args=(--lane-pane-id "$3")
  [ -z "${4:-}" ] || other_pane_id_args=(--other-pane-id "$4")
  json=$("$LEDGER_PYTHON" "$LEDGER_CLI" lane-relation --lane "$1" --other "$2" \
    "${lane_pane_id_args[@]}" "${other_pane_id_args[@]}" 2>/dev/null) || json=""
  rel=$(sed -n 's/.*"relation":"\([a-z]*\)".*/\1/p' <<<"$json" | head -1)
  LANE_REL_POPULATION_CANDIDATE=$(sed -n 's/.*"lane_population":"\([a-zA-Z-]*\)".*/\1/p' <<<"$json" | head -1)
  LANE_REL_POPULATION_OTHER=$(sed -n 's/.*"other_population":"\([a-zA-Z-]*\)".*/\1/p' <<<"$json" | head -1)
  case "$rel" in
    same|different) printf '%s\n' "$rel" ;;
    *) printf 'unknown\n' ;;
  esac
}
if ! LEDGER_STATUS_OUT=$("$LEDGER_PYTHON" "$LEDGER_CLI" status 2>&1); then
  echo "dispatch: the ledger is unreadable -- refusing to dispatch #$ISSUE_ARG" >&2
  echo "dispatch: cannot tell which lanes are free without it, so nothing is safe to pick" >&2
  sed 's/^/  /' <<<"$LEDGER_STATUS_OUT" >&2
  exit 1
fi

# --- 0.2 refuse to dispatch for a supervisor that does not hold the lease -
# agent-dotfiles#238. On 2026-08-12 a second, fully legitimate supervisor
# instance resumed in an ordinary tmux window (identity had only ever been
# INFERRED from a window index, never recorded) and dispatched the same five
# issues a first instance had claimed seconds earlier. `claim.sh`'s per-issue
# claim could not catch it: both instances authenticate as the same GitHub
# user, so an assignee one took reads as claimed to the other too, not as a
# collision signal. This is the ledger-recorded fact that closes the gap one
# level up, at the supervisor ROLE itself -- see `Ledger.take_supervisor_lease`.
#
# `SUPERVISOR_LEASE_OWNER_PID` names the process that is supposed to hold the
# lease -- defaults to `$PPID`, the process that invoked this script, which
# in production is the long-lived supervisor loop itself (loop-tick.md takes
# the lease with its own `$$` at tick start, before calling this script; see
# that file's lease gate). Overridable for a caller that is not its own
# direct parent (none, today).
#
# Fails CLOSED only against a genuine conflict -- a lease recorded for some
# OTHER pid -- never against the mere absence of one. A ledger that has never
# seen `take-supervisor-lease` (every existing test fixture, and any manual
# `dispatch.sh` run outside the loop) has no row to conflict with, so this
# proceeds silently rather than demanding every caller adopt lease tracking
# before it dispatches anything -- the loop is what negotiates the lease with
# the WHOLE estate; a lone `dispatch.sh` invocation reading no lease at all
# has nothing to conflict with and nothing to protect against. Per
# agent-dotfiles#199, stderr on a dispatch that is not failing stays clean --
# an absent or unreadable lease is not, by itself, a failure.
SUPERVISOR_LEASE_OWNER_PID="${SUPERVISOR_LEASE_OWNER_PID:-$PPID}"
if LEASE_OUT=$("$LEDGER_PYTHON" "$LEDGER_CLI" supervisor-lease 2>&1); then
  if grep -qF '"held":true' <<<"$LEASE_OUT"; then
    LEASE_OWNER=$(sed -n 's/.*"owner":"\([^"]*\)".*/\1/p' <<<"$LEASE_OUT" | head -1)
    LEASE_PID="${LEASE_OWNER##*:}"
    if [ -n "$LEASE_PID" ] && [ "$LEASE_PID" != "$SUPERVISOR_LEASE_OWNER_PID" ]; then
      echo "dispatch: the supervisor lease is held by $LEASE_OWNER, not this process (expected pid $SUPERVISOR_LEASE_OWNER_PID) -- refusing to dispatch #$ISSUE_ARG" >&2
      echo "dispatch: a second supervisor instance must stand down, not dispatch; see agent-dotfiles#238" >&2
      exit 1
    fi
  fi
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
# THE PRIMARY MAPPING (issue -> author lane) is `source_tasks.source_ref` (the
# issue number `record_dispatch` wrote it as, step 6 below) joined to
# `tasks.lane` by task id, with review tasks explicitly filtered out -- see
# `Ledger.get_author_task_for_issue`. Neither side comes from a branch name.
#
# THE FALLBACK MAPPING, used only when that lookup is silent, is #117's own
# fix: the WORKTREE `worktree.sh new` built for a dispatch (step 3 below) is
# recorded against that dispatch's task id at dispatch time (`--worktree`,
# step 6), and never touched again. That worktree is not renamed even when
# its branch is -- a lane routinely renames its checkout to a type-prefixed
# branch (`fix/`, `feat/`, ...) with a slug of its OWN choosing, unrelated
# to the dispatch slug the task id was minted from, so the branch on a PR is
# frequently NOT `<prefix>/<n>-<slug>` for the `$PREFIX`/`$SLUG` this
# dispatch would reconstruct (agent-supervisor#117: task
# `as101-reviewspr-inference` produced branch `fix/101-not-a-review-escape`
# -- the two slugs share no text at all). So this asks `git worktree list`
# which worktree currently has the PR's HEAD_REF checked out, then asks the
# ledger which task that worktree's PATH was recorded against -- a fact
# nobody has to reconstruct, because it was written once and never changes.
#
# A LEGACY fallback (step 3.1) still reconstructs a task id from the branch
# name, kept only for tasks dispatched before this column existed (they
# recorded no worktree path at all, so step 3 can never match them) -- see
# its own comment below for why it is trusted no further than before.
#
# FAILS CLOSED throughout: `gh` unreachable, a PR with no head branch, or
# every source below coming up silent -- every one of these means authorship
# cannot be determined, and this refuses the WHOLE dispatch rather than
# guess. A candidate lane is only ever excluded, never assumed innocent from
# missing data.
#
# agent-supervisor#35: a branch name is a label someone typed at dispatch
# time, not a record this system wrote -- the ledger row is. THE LEDGER IS
# ASKED FIRST NOW, keyed by the issue the PR closes, and the branch name is
# only ever consulted through a ledger record it points at, never trusted on
# its own -- first the worktree that produced it (#117), then, only for
# rows that predate that, the task id it implies by convention (#35).
# Measured on this repo's own merged history when #35 shipped: 3 of 11
# merged PRs, plus open PR #33, were unreviewable through the branch-only
# path -- see the #35 issue body for the count.
# agent-supervisor#190: this used to resolve a SINGLE `AUTHOR_LANE` -- the
# one task the ledger could name as having produced the PR's branch. Two
# live dispatches (this issue's own evidence) show why that is not enough:
# a FIX-PASS task dispatched against the same issue to address review
# findings (`as178-fix186`, fixing PR #186) is a second, later contributor
# to the same PR, sitting in the exact same `source_tasks` rows the single
# lookup below queries -- it was just discarded by the "narrow to one"
# step, and the lane that wrote it went on to approve its own fix.
#
# So this now builds a SET, `AUTHOR_LANES` (with `AUTHOR_TASKS` as its
# parallel "why" for messages) -- every lane the ledger can show contributed
# to this PR, not just the one it can name as "the" author. Plain indexed
# arrays, not `declare -A` (bash 3.2 has none -- see #199's own removal
# above), so membership is a linear scan (`author_lane_known`, below) and
# both are guarded with the same `"${arr[@]+"${arr[@]}"}"` bash-3.2-empty-
# array idiom `POSITIONAL` already established in this file.
# agent-supervisor#308 item 4: the resolution chain itself (issue, PR-task,
# PR-contributor, worktree, legacy branch) now lives in
# resolve-pr-contributors.sh, shared with mark-pr-external.sh's laundering
# gate -- see that file's header for why a second, drifted copy is exactly
# the defect #321's review measured in verdict-independence.sh.
# shellcheck source=./resolve-pr-contributors.sh
. "$HERE/resolve-pr-contributors.sh"
AUTHOR_LANES=()
AUTHOR_TASKS=()
AUTHOR_PANE_IDS=()
FALLBACK_TASK=""
# True (1) only when `contributor-issue-lanes` (or a fallback below) was
# consulted and answered with a NON-empty, known set. Distinguishes "no PR
# review requested, no set was ever needed" (both arrays legitimately empty,
# no refusal below) from "a review WAS requested and the ledger came back
# silent" (arrays empty for a reason, and step 4 below must refuse), which an
# empty-array check alone cannot tell apart.
CONTRIBUTORS_RESOLVED=""
if [ -n "$REVIEWS_PR" ]; then
  if ! resolve_pr_contributors "$REPO" "$REVIEWS_PR" "$REPO_PATH" "$PREFIX" "$LEDGER_PYTHON" "$LEDGER_CLI"; then
    exit 1
  fi

  # 3.2. agent-supervisor#308 item 3: "authored outside the lane system" as
  # a FIRST-CLASS, RECORDABLE state -- checked only when every real
  # resolution path above came back silent, and NEVER treated as "proceed
  # when authorship is unknown" (#190 forbids that flag outright; this is
  # not it). A PR marked here was a DECISION an operator made -- that no
  # lane wrote it -- so the contributor set is KNOWN-EMPTY, not unresolved:
  # every free lane is a valid independent reviewer, the safe case. This is
  # the #316/#301/#300 shape: a PR authored directly by a human or the
  # watchdog, closing no issue the ledger can name, whose branch fails the
  # legacy convention outright. See `Ledger.get_pr_external` / `mark-pr-external`.
  if [ -z "$CONTRIBUTORS_RESOLVED" ]; then
    EXTERNAL_JSON=$("$LEDGER_PYTHON" "$LEDGER_CLI" pr-external --repo "$REPO" --pr "$REVIEWS_PR" 2>&1) || EXTERNAL_JSON=""
    if grep -qF '"known":true' <<<"$EXTERNAL_JSON"; then
      CONTRIBUTORS_RESOLVED=1
      echo "dispatch: PR #$REVIEWS_PR is recorded as authored OUTSIDE the lane system (marked external) -- no lane contributors to exclude, every free lane is a valid independent reviewer" >&2
    fi
  fi

  # 3.3. agent-estate#741: "authored directly by the Director, verified, no
  # lane contributed" as its OWN first-class, recordable state -- the
  # sibling check to 3.2 above, kept a visibly DISTINCT log line on purpose
  # (never reuse the "marked external" wording -- the two must stay
  # distinguishable in every log/error path). See
  # `Ledger.mark_pr_director_authored` / `mark-pr-director-authored.sh` for
  # why this is not the same table as `pr_external_authorship`: the
  # Director is an internal estate actor, not one authored outside the lane
  # system, and register-lane-self.sh structurally excludes it from ever
  # becoming a lane row either.
  if [ -z "$CONTRIBUTORS_RESOLVED" ]; then
    DIRECTOR_JSON=$("$LEDGER_PYTHON" "$LEDGER_CLI" pr-director --repo "$REPO" --pr "$REVIEWS_PR" 2>&1) || DIRECTOR_JSON=""
    if grep -qF '"known":true' <<<"$DIRECTOR_JSON"; then
      CONTRIBUTORS_RESOLVED=1
      echo "dispatch: PR #$REVIEWS_PR is recorded as director-authored -- no lane contributors to exclude, every free lane is a valid independent reviewer" >&2
    fi
  fi

  # 4. Still silent -> refuse. Every source above answered "no record", not
  # "safe". agent-supervisor#190's fail-closed requirement: an unresolvable
  # contributor set must make this dispatch LESS likely to proceed, never
  # fall back to some narrower, single-author check that would admit a
  # candidate this wider set does not yet clear (#124/#126).
  if [ -z "$CONTRIBUTORS_RESOLVED" ]; then
    # Wording kept verbatim from before #190 ("could not determine PR
    # #N's author", "authorship unknown") -- both predate the widening and
    # every earlier caller, including tests, greps for those exact phrases.
    # The two describe the same fact before and after: nobody the ledger
    # will vouch for produced (or contributed to) this PR.
    echo "dispatch: could not determine PR #$REVIEWS_PR's author -- the ledger has no record by issue, by commit, or by branch '$HEAD_REF' (task ${FALLBACK_TASK:-none}) -- refusing (authorship unknown, failing closed)" >&2
    echo "dispatch: if this PR was genuinely authored outside the lane system (a human, or the watchdog), record that once with: $HERE/mark-pr-external.sh '$REPO' $REVIEWS_PR '<why>' '$REPO_PATH'" >&2
    echo "dispatch: NOTE -- use mark-pr-external.sh, not cli.py mark-pr-external directly; the CLI now refuses without --chain-verified, which only the wrapper's own exhaustive resolution chain earns (PR #331 review, finding 2)" >&2
    echo "dispatch: if this PR was authored DIRECTLY BY THE DIRECTOR (agent-estate#741), record that once, from the Director's own pane, with: $HERE/mark-pr-director-authored.sh '$REPO' $REVIEWS_PR '<why>' '$REPO_PATH'" >&2
    # agent-supervisor#101, third red-first item: on the inferred path these
    # are TWO separate findings arriving together -- "this looked like a
    # review" and "its contributors are unresolvable" -- and read as one
    # failure about authorship. An operator whose dispatch was never a
    # review has no authorship problem to fix; they have an inference to
    # switch off. Say which of the two is theirs.
    if [ -n "$REVIEWS_PR_INFERRED" ]; then
      echo "dispatch: NOTE -- --reviews-pr was never passed; PR #$REVIEWS_PR was INFERRED from $INFERRED_FROM" >&2
      echo "dispatch: two separate things are true here -- this dispatch LOOKED like a review, and that PR's contributors cannot be resolved" >&2
      echo "dispatch: if it is not a review at all (a rebase, a fix pass, a follow-up), re-run with --not-a-review and the authorship question does not arise" >&2
    fi
    exit 1
  fi
fi

# --- 0.6 a PR already claimed by a lane is refused, not double-dispatched -
# agent-supervisor#159 (issue comment, third occurrence measured the same
# night): two lanes worked #157's review and two lanes worked #149's fix
# pass, both times through the SAME task id with a "b" suffix -- something
# minted a second task for work that already had one instead of detecting
# the first. The shared cause: a ledger-bypassing tmux hand-off (the
# workaround for the issue-claim refusal this PR removes) leaves no row a
# second dispatcher can see. This is the detection that was missing --
# checked BEFORE step 1 picks a lane, so it costs nothing when it refuses:
# no lane claim taken yet, no worktree built.
#
# Only runs for a PR-scoped dispatch (`PR_SCOPED` set, see the flag block
# above) -- an ordinary issue-scoped dispatch has no PR to check yet, and
# `claim.sh` in step 2 below is exactly this same guarantee for that case,
# already proven. `cli.py pr-lane` asks `Ledger.get_open_task_for_pr`, which
# is OPEN-status only (unlike `issue-lane`): a completed or cancelled prior
# review of this PR must not block a fresh one.
#
# agent-supervisor#169: THIS CHECK ALONE IS A TOCTOU, and is not the whole
# guarantee -- a reviewer of this PR reproduced the exact "b"-suffixed
# collision above THROUGH it: it is a plain read, run once, before a lane is
# even picked, while the write that actually claims the PR
# (`record-dispatch --pr`, step 6, far below) does not happen until after
# lane selection, worktree creation and the brief itself -- a real,
# multi-second window, not the sub-second gap `claim.sh`'s own docstring
# accepts for the GitHub-assignee race. Two dispatchers can both pass this
# exact check before either one has recorded anything. What actually closes
# the race is `core.py`'s `one_open_pull_per_source_ref` trigger on
# `record-dispatch`'s write, at the bottom of this script -- this check
# stays because it is the FRIENDLY, EARLY refusal for the common case (no
# wasted worktree, no stray brief); it is not, by itself, load-bearing for
# correctness anymore.
if [ -n "$PR_SCOPED" ]; then
  PR_LANE_REPO_ARGS=()
  [ -n "$REPO" ] && PR_LANE_REPO_ARGS=(--repo "$REPO")
  PR_LANE_JSON=$("$LEDGER_PYTHON" "$LEDGER_CLI" pr-lane --pr "$PR_SCOPED" "${PR_LANE_REPO_ARGS[@]+"${PR_LANE_REPO_ARGS[@]}"}" 2>&1)
  if [ $? -ne 0 ]; then
    echo "dispatch: could not ask the ledger whether PR #$PR_SCOPED already has a lane -- refusing rather than risk a duplicate" >&2
    sed 's/^/  /' <<<"$PR_LANE_JSON" >&2
    exit 1
  fi
  if grep -qF '"known":true' <<<"$PR_LANE_JSON"; then
    PR_HOLDER_LANE=$(sed -n 's/.*"lane":"\([^"]*\)".*/\1/p' <<<"$PR_LANE_JSON")
    PR_HOLDER_TASK=$(sed -n 's/.*"task":"\([^"]*\)".*/\1/p' <<<"$PR_LANE_JSON")
    echo "dispatch: PR #$PR_SCOPED is already claimed by lane $PR_HOLDER_LANE (task $PR_HOLDER_TASK) -- not dispatching, pick different work" >&2
    exit 1
  fi
fi
