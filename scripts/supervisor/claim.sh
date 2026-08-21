#!/bin/bash
# Claim an issue on GitHub before dispatching it to a lane.
#
# WHY: the estate's contract says GitHub Issues are the authoritative task,
# CLAIM and status record. Task and status were true; claim was not. Nothing
# wrote a claim anywhere, so a dispatcher ran `gh issue list`, saw an open
# issue, and could not tell that another lane had taken it ninety seconds
# earlier. On 2026-08-11 issue #28 was dispatched twice -- once by the
# Director, once by the supervisor -- and both lanes produced complete,
# near-identical fixes (#68 merged, #69 closed). About an hour of lane work was
# spent twice. Neither dispatcher was wrong; the signal was missing.
#
# THE CLAIM IS THE ASSIGNEE. `gh issue edit N --add-assignee @me` writes a
# first-class GitHub field: visible in the UI, readable by any machine with the
# repo, and surviving the loss of all local state -- which the estate requires,
# since local state is a reconstructable spool and GitHub is the record. No new
# label taxonomy and no second convention to keep in sync.
#
#   In these four repos, an assignee means CLAIMED BY A LANE. Do not hand-assign
#   an issue you are not dispatching; that reads as taken and will be skipped.
#   (At the time this was written, no open issue in any of the four repos had an
#   assignee, so the convention costs nothing to adopt.)
#
# WHAT IT DOES NOT DO: `--add-assignee` is add-to-a-set, not compare-and-swap.
# Two dispatchers that both read "unassigned" within the same second will both
# write and both succeed, and because every lane authenticates as the same
# GitHub user the read-back cannot tell them apart. This closes the observed
# failure -- a window measured in minutes -- and leaves a sub-second one. GitHub
# offers no CAS on issues; a narrower race would need a different substrate
# (an atomic ref push), which is not worth it for two dispatchers on unrelated
# cadences.
#
# WHICH lane holds a claim is answered by the tmux window name, which
# loop-tick.md already requires be set to `<repo><issue>-<slug>` on every
# dispatch. That is why `stale` needs no extra bookkeeping.
#
# Usage:
#   claim.sh check   <issue> [repo]          exit 0 claimable, 1 already claimed
#   claim.sh take    <issue> [repo] [lane]   claim it; exit 1 if already claimed
#   claim.sh release <issue> [repo]          drop the claim
#   claim.sh list    [repo]                  open issue numbers with no claim
#   claim.sh stale   [repo]                  claimed issues whose lane is gone
#   claim.sh audit   [repo]                  report stale claims; exit 0 iff none
#   claim.sh reap    [repo]                  release every stale claim, logging each
#
# [repo] is OWNER/NAME; omitted, gh resolves it from the working directory.
#
# agent-supervisor#359: `release` above (wired into `dispatch.sh`'s pre-send
# abort paths, and now, via cli.py's `complete`/`record-completion`/
# `cancel-open-task`, into every terminal completion path too) can never be
# the WHOLE fix -- a killed tmux server or a SIGKILLed process runs no
# cleanup at all. `audit`/`reap` are that second half: the same liveness
# signal `stale` already computes (a claimed issue with no live-looking
# window and no open PR referencing it), reported with a `stale=<n>` count
# for `reap` visible before the backlog silently locks itself, and actually
# released for `reap`, one line logged per release -- silent release is its
# own hazard (an issue that quietly becomes available twice can be
# dispatched twice). Both REFUSE outright (exit 2, no `stale=` line, nothing
# released) if either underlying signal -- `lanes.sh`, or the open-PR read --
# could not be read at all: an issue this cannot see is UNKNOWN, and UNKNOWN
# is never treated as safe to report clear or release. `stale` itself keeps
# its old behaviour unchanged (best-effort, no rc check) for the callers and
# tests already built on it; `audit`/`reap` are the fail-closed callers.
#
# agent-supervisor#144: every gh call in this file is REST core
# (`gh api repos/...`), not GraphQL (`gh issue view`/`gh issue list`/`gh pr
# list`). This is the deadlock the issue names: `claim.sh check`'s old
# GraphQL read failed closed with "cannot read issue" once the shared
# GraphQL budget hit 0/5000, which made `dispatch.sh` correctly refuse --
# so the fix for GraphQL exhaustion could not itself be dispatched while
# GraphQL was exhausted. REST core sits on its own 5000-point budget.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./session-defaults.sh
. "$HERE/session-defaults.sh"
SESSION="$(lanes_session_or_default)"

CMD="${1:-}"
case "$CMD" in
  check|take|release)      ISSUE="${2:-}"; REPO="${3:-}"; LANE="${4:-$(hostname -s 2>/dev/null || echo lane)}" ;;
  list|stale|audit|reap)   ISSUE=""; REPO="${2:-}" ;;
  *) sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; exit 2 ;;
esac

case "$CMD" in
  list|stale|audit|reap) ;;
  *) [ -n "$ISSUE" ] || { echo "claim: $CMD needs an issue number" >&2; exit 2; } ;;
esac

# api_base -> "repos/OWNER/NAME" when [repo] was given, else the same
# {owner}/{repo} placeholder `gh` itself resolves from the working directory
# for every other command here -- `gh api` has no `-R` flag, so the repo has
# to be baked into the path instead.
api_base() {
  if [ -n "${REPO:-}" ]; then printf 'repos/%s\n' "$REPO"
  else printf 'repos/{owner}/{repo}\n'; fi
}

me_login() { # the authenticated user's login, for the assignees write endpoints
  gh api user -q .login 2>/dev/null
}

holder_of() { # holder_of <issue> -> comma-separated logins, empty if unclaimed
  gh api "$(api_base)/issues/$1" -q '.assignees|map(.login)|join(",")' 2>/dev/null
}

# agent-supervisor#480: `claim.sh stale` reported EVERY claude-print
# dispatch (a plain `claude -p` subprocess, dispatch-claude-print.sh, no
# tmux window at all by design) as stale from the moment it started until a
# PR opened -- the tmux-window half of `compute_stale`'s liveness check can
# never be true for it, and the PR half is naturally false for anything
# still in progress. Reproduced live against #473 and #470: both genuinely
# `accepted`/`delivered` in the ledger (confirmed via `cli.py task-lane` and
# a live `ps`), both reported stale anyway.
#
# `issue-lane` is the ledger's own answer for "what is the most recent
# dispatch of this issue number" (already used by dispatch.sh's authorship
# guard) -- asked here as a THIRD liveness signal alongside the tmux window
# and the open PR: a task whose most recent dispatch is `delivered` or
# `accepted` with no `completed_at` yet is a lane genuinely still working,
# tmux window or not. Best-effort like the rest of `compute_stale` (no rc
# check here, same as the tmux/PR reads) -- python3 or cli.py being
# unavailable just forfeits this extra signal rather than blocking `stale`
# outright; STALE_LANES_RC/STALE_PULLS_RC below are still the only things
# `audit`/`reap` refuse on.
ledger_claims_live() { # ledger_claims_live <issue-number> -> 0 if the ledger says a dispatch of this issue is still open
  local n="$1" json repo_args=()
  [ -n "${REPO:-}" ] && repo_args=(--repo "$REPO")
  json=$(python3 "$HERE/cli.py" issue-lane --issue "$n" "${repo_args[@]+"${repo_args[@]}"}" 2>/dev/null) || return 1
  grep -qF '"known":true' <<<"$json" || return 1
  case "$json" in
    *'"status":"delivered"'*|*'"status":"accepted"'*) ;;
    *) return 1 ;;
  esac
  grep -qF '"completed_at":null' <<<"$json"
}

# compute_stale sets three globals -- STALE_CANDIDATES (claimed issue
# numbers, one per line, whose lane is gone and whose PR is not open),
# STALE_LANES_RC and STALE_PULLS_RC (the exit codes of the two underlying
# reads). MUST be called directly, never as `x=$(compute_stale)` -- a caller
# that must not act on an UNKNOWN answer (agent-supervisor#359's
# `audit`/`reap`, below) needs STALE_LANES_RC/STALE_PULLS_RC to land in ITS
# OWN shell, and `$(...)` runs the function in a subshell whose variable
# assignments vanish the moment it exits. Refactored out of the `stale` case
# body unchanged -- see that case (still the direct, best-effort caller with
# no rc check of its own) for the liveness rules themselves: a lane window
# `lanes.sh` does not report `dead`/`stale`, an open PR saying `fixes #<n>`,
# or (agent-supervisor#480) the ledger saying this issue's most recent
# dispatch is still `delivered`/`accepted` with no `completed_at`.
compute_stale() {
  local lanes_out pulls_out live_numbers
  lanes_out=$("$HERE/lanes.sh" "$SESSION" 2>/dev/null); STALE_LANES_RC=$?
  pulls_out=$(gh api --paginate "$(api_base)/pulls?state=open&per_page=100" \
     -q '.[]|"\(.number)\t\(.body)"' 2>/dev/null); STALE_PULLS_RC=$?
  live_numbers=$(
    awk 'NR>1 && $1 ~ /^[0-9]+$/ && $NF!="dead" && $NF!="stale" && $2 !~ /^free-[0-9]+$/ {print $2}' <<<"$lanes_out" \
      | sed -E -n 's/^[A-Za-z]+([0-9]+)-.*/\1/p'
    cut -f2- <<<"$pulls_out" | grep -oiE '(fixes|closes|resolves) #[0-9]+' | grep -oE '[0-9]+'
  )
  STALE_CANDIDATES=$(
    gh api --paginate "$(api_base)/issues?state=open&per_page=100" \
       -q '.[]|select(has("pull_request")|not)|"\(.number)\t\(.assignees|map(.login)|join(","))"' 2>/dev/null \
      | awk -F'\t' '$2!=""{print $1}' \
      | while read -r n; do
          grep -qx "$n" <<<"$live_numbers" && continue
          ledger_claims_live "$n" && continue
          echo "$n"
        done
  )
}

case "$CMD" in

check)
  h=$(holder_of "$ISSUE") || { echo "claim: cannot read issue #$ISSUE" >&2; exit 2; }
  if [ -n "$h" ]; then echo "claimed by $h"; exit 1; fi
  echo "claimable"; exit 0 ;;

take)
  # Read state and assignees in the one call, immediately before the write: an
  # issue closed hours ago (#95) must not pass "is this available" just
  # because `--add-assignee` itself has no opinion about issue state. An
  # unreadable answer -- network error, gh failure, empty output -- is refused
  # rather than treated as open; #59, #92 and #95 are all the same shape of an
  # unreadable answer being read as permissive.
  # REST's `state` comes back lowercase ("open"/"closed"), unlike GraphQL's
  # "OPEN"/"CLOSED" -- compared lowercase below.
  #
  # agent-supervisor#228: the mirror-image bug. A rate-limited (or otherwise
  # failed) response is NOT empty -- gh does not apply the `-q` jq filter to
  # an error body, it prints the raw error JSON to stdout instead. The old
  # guard only checked `[ -z "$info" ]`, so that raw `{"message":"API rate
  # limit exceeded..."}` counted as "readable", its leading `{` became
  # `state`, and `{ != open` reported a genuinely OPEN issue as closed.
  # "Cannot tell" became a fact in the other direction.
  #
  # Fixed by never trusting the body alone: `-i` captures the HTTP status
  # line so failure is read off the status, not inferred from what jq did to
  # a body it never got applied to; and `state` is only ever accepted as
  # exactly `open` or `closed` -- anything else, including a JSON fragment,
  # is unreadable, never a state.
  resp=$(gh api -i "$(api_base)/issues/$ISSUE" 2>&1)
  status=""
  first_line="$(head -1 <<<"$resp")"
  case "$first_line" in
    HTTP/*) status="$(awk '{print $2}' <<<"$first_line")" ;;
  esac
  body=$(awk 'h{print;next} /^\r?$/{h=1;next}' <<<"$resp")

  if [ -z "$status" ]; then
    echo "claim: cannot read #$ISSUE — no HTTP response — refusing to claim on an unreadable state" >&2
    exit 2
  fi

  if [ "${status:0:1}" != "2" ]; then
    # Non-2xx: the body's content is never treated as a state answer, only
    # ever as a diagnostic. A rate limit gets a specific message, because a
    # bare "rate limited" would send the reader chasing the wrong budget --
    # `gh api` (this call) spends REST core's 5000/hr; `gh issue
    # list`/`gh pr view` spend GraphQL's separate 5000/hr, and during the
    # incident that prompted this fix, GraphQL calls kept succeeding while
    # every REST read failed. `gh api rate_limit`'s reported remaining count
    # is not authoritative either -- it does not see the secondary (burst)
    # limit, so this treats the 403 itself as the signal, never that
    # endpoint's count.
    msg=$(jq -r '.message // empty' <<<"$body" 2>/dev/null)
    if { [ "$status" = "403" ] || [ "$status" = "429" ]; } && grep -qi 'rate limit' <<<"$msg"; then
      reset=$(grep -i '^x-ratelimit-reset:' <<<"$resp" | head -1 | cut -d: -f2 | tr -d ' \r')
      retry_after=$(grep -i '^retry-after:' <<<"$resp" | head -1 | cut -d: -f2 | tr -d ' \r')
      when=""
      [ -n "$reset" ] && when=" (resets at epoch $reset)"
      [ -z "$when" ] && [ -n "$retry_after" ] && when=" (retry after ${retry_after}s)"
      echo "claim: REST core rate limit hit reading #$ISSUE (HTTP $status)$when — GraphQL is a separate 5000/hr budget and may still be fine; this is not a state answer, wait for the reset rather than re-queuing #$ISSUE elsewhere" >&2
      exit 2
    fi
    echo "claim: cannot read #$ISSUE (HTTP $status) — refusing to claim on an unreadable state" >&2
    exit 2
  fi

  state=$(jq -r '.state // empty' <<<"$body" 2>/dev/null)
  case "$state" in
    open|closed) ;;
    *)
      echo "claim: #$ISSUE returned no readable state — refusing to claim on an unreadable state" >&2
      exit 2 ;;
  esac
  h=$(jq -r '(.assignees // [])|map(.login)|join(",")' <<<"$body" 2>/dev/null)
  if [ "$state" != "open" ]; then
    echo "claim: #$ISSUE is not open (state=$state) — not dispatching to $LANE" >&2
    exit 1
  fi
  if [ -n "$h" ]; then
    # The refusal a second dispatcher must see. Say who, so the supervisor can
    # check whether that claim is stale rather than assuming it is live.
    echo "claim: #$ISSUE is already claimed by $h — not dispatching to $LANE" >&2
    exit 1
  fi
  me=$(me_login)
  [ -n "$me" ] || { echo "claim: could not resolve the authenticated user" >&2; exit 2; }
  gh api -X POST "$(api_base)/issues/$ISSUE/assignees" -f "assignees[]=$me" >/dev/null 2>&1 || {
    echo "claim: could not assign #$ISSUE" >&2; exit 2; }
  h=$(holder_of "$ISSUE")
  [ -n "$h" ] || { echo "claim: assignment to #$ISSUE did not stick" >&2; exit 2; }
  echo "claim: #$ISSUE taken by $LANE (assignee $h)"
  exit 0 ;;

release)
  me=$(me_login)
  [ -n "$me" ] || { echo "claim: could not resolve the authenticated user" >&2; exit 2; }
  gh api -X DELETE "$(api_base)/issues/$ISSUE/assignees" -f "assignees[]=$me" >/dev/null 2>&1 || {
    echo "claim: could not unassign #$ISSUE" >&2; exit 2; }
  echo "claim: #$ISSUE released"
  exit 0 ;;

list)
  # What the dispatch step reads INSTEAD of a bare `gh issue list`. REST's
  # /issues endpoint answers for PRs too (they carry a `pull_request` key) --
  # filtered out here to match `gh issue list`'s issues-only answer.
  gh api --paginate "$(api_base)/issues?state=open&per_page=100" \
     -q '.[]|select(has("pull_request")|not)|"\(.number)\t\(.assignees|map(.login)|join(","))"' 2>/dev/null \
    | awk -F'\t' '$2==""{print $1}'
  exit 0 ;;

stale)
  # EXPIRY: a claim does not expire on a clock. A legitimate lane task here
  # runs for hours, so any timeout short enough to be useful would steal live
  # work -- the exact hour this mechanism exists to protect. A claim expires
  # when the lane holding it is gone.
  #
  # A claim is LIVE if either holds:
  #   - a lane window names that issue number and lanes.sh does not report it
  #     `dead` or `stale` (#66 gave the estate the first signal and #237 split
  #     the second out of it -- a dead pane whose window name still claims a
  #     task. Both mean the same thing HERE: no agent is running, so the name
  #     is not evidence of a live claim. Excluding only `dead` after #237
  #     would make every stale name hold its issue hostage), or
  #   - an open PR says it fixes the issue. A finished lane is renamed `free-N`
  #     while its PR waits for review; the work is real and must not be redone.
  #
  # Everything else is reported as a candidate. This REPORTS, it never
  # releases: matching on a bare issue number cannot tell agent-dotfiles#70
  # from skills#70, and the two errors are not symmetric -- leaving a claim in
  # place costs a tick of delay, dropping a live one costs an hour of work.
  # Release deliberately stays a decision, `claim.sh release <n>`.
  compute_stale
  [ -n "$STALE_CANDIDATES" ] && printf '%s\n' "$STALE_CANDIDATES"
  exit 0 ;;

audit)
  # agent-supervisor#359: `stale`'s own REPORT, made fail-closed and given a
  # one-line summary a caller (or a human) can grep for -- the acceptance
  # check this issue shipped with is exactly `claim.sh audit | grep -q
  # "stale=0"`. Never releases anything; see `reap` below for that.
  compute_stale
  if [ "$STALE_LANES_RC" -ne 0 ] || [ "$STALE_PULLS_RC" -ne 0 ]; then
    echo "claim: audit: cannot read live state ($SESSION lanes rc=$STALE_LANES_RC, open PRs rc=$STALE_PULLS_RC) -- refusing to report, UNKNOWN is never treated as safe" >&2
    exit 2
  fi
  count=0
  if [ -n "$STALE_CANDIDATES" ]; then
    while IFS= read -r n; do
      [ -n "$n" ] || continue
      echo "claim: audit: #$n is claimed with no live lane and no open PR referencing it"
      count=$((count + 1))
    done <<<"$STALE_CANDIDATES"
  fi
  echo "stale=$count"
  [ "$count" -eq 0 ] ;;

reap)
  # The second half #359 asks for: `release` (wired into dispatch.sh's
  # pre-send aborts and, via cli.py, into every terminal completion path)
  # can never be complete on its own -- a killed tmux server or a SIGKILLed
  # lane runs no cleanup. This sweeps whatever that still misses. Same
  # fail-closed refusal as `audit` -- an unreadable liveness signal releases
  # NOTHING, because a live claim reported stale by mistake is destroyed
  # in-flight work, the worse failure (#359's own framing).
  compute_stale
  if [ "$STALE_LANES_RC" -ne 0 ] || [ "$STALE_PULLS_RC" -ne 0 ]; then
    echo "claim: reap: cannot read live state ($SESSION lanes rc=$STALE_LANES_RC, open PRs rc=$STALE_PULLS_RC) -- refusing to release anything, UNKNOWN is never treated as safe" >&2
    exit 2
  fi
  released=0
  failed=0
  if [ -n "$STALE_CANDIDATES" ]; then
    while IFS= read -r n; do
      [ -n "$n" ] || continue
      if "$HERE/claim.sh" release "$n" "$REPO" >/dev/null 2>&1; then
        echo "claim: reap: released #$n -- no live lane, no open PR"
        released=$((released + 1))
      else
        echo "claim: reap: could not release #$n -- release it by hand" >&2
        failed=$((failed + 1))
      fi
    done <<<"$STALE_CANDIDATES"
  fi
  echo "reaped=$released failed=$failed"
  [ "$failed" -eq 0 ] ;;

esac
