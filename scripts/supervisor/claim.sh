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
#
# [repo] is OWNER/NAME; omitted, gh resolves it from the working directory.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SESSION="${LANES_SESSION:-agent-dotfiles}"

CMD="${1:-}"
case "$CMD" in
  check|take|release) ISSUE="${2:-}"; REPO="${3:-}"; LANE="${4:-$(hostname -s 2>/dev/null || echo lane)}" ;;
  list|stale)         ISSUE=""; REPO="${2:-}" ;;
  *) sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; exit 2 ;;
esac

R=()
[ -n "${REPO:-}" ] && R=(-R "$REPO")

if [ "$CMD" != list ] && [ "$CMD" != stale ] && [ -z "$ISSUE" ]; then
  echo "claim: $CMD needs an issue number" >&2; exit 2
fi

holder_of() { # holder_of <issue> -> comma-separated logins, empty if unclaimed
  gh issue view "$1" ${R[@]+"${R[@]}"} --json assignees \
     -q '.assignees|map(.login)|join(",")' 2>/dev/null
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
  info=$(gh issue view "$ISSUE" ${R[@]+"${R[@]}"} --json state,assignees \
     -q '"\(.state)\t\(.assignees|map(.login)|join(","))"' 2>/dev/null)
  if [ -z "$info" ]; then
    echo "claim: cannot read #$ISSUE — refusing to claim on an unreadable state" >&2
    exit 2
  fi
  state="${info%%$'\t'*}"
  h="${info#*$'\t'}"
  if [ "$state" != "OPEN" ]; then
    echo "claim: #$ISSUE is not open (state=$state) — not dispatching to $LANE" >&2
    exit 1
  fi
  if [ -n "$h" ]; then
    # The refusal a second dispatcher must see. Say who, so the supervisor can
    # check whether that claim is stale rather than assuming it is live.
    echo "claim: #$ISSUE is already claimed by $h — not dispatching to $LANE" >&2
    exit 1
  fi
  gh issue edit "$ISSUE" ${R[@]+"${R[@]}"} --add-assignee @me >/dev/null 2>&1 || {
    echo "claim: could not assign #$ISSUE" >&2; exit 2; }
  h=$(holder_of "$ISSUE")
  [ -n "$h" ] || { echo "claim: assignment to #$ISSUE did not stick" >&2; exit 2; }
  echo "claim: #$ISSUE taken by $LANE (assignee $h)"
  exit 0 ;;

release)
  gh issue edit "$ISSUE" ${R[@]+"${R[@]}"} --remove-assignee @me >/dev/null 2>&1 || {
    echo "claim: could not unassign #$ISSUE" >&2; exit 2; }
  echo "claim: #$ISSUE released"
  exit 0 ;;

list)
  # What the dispatch step reads INSTEAD of a bare `gh issue list`.
  gh issue list ${R[@]+"${R[@]}"} --state open --limit 200 --json number,assignees \
     -q '.[]|"\(.number)\t\(.assignees|map(.login)|join(","))"' 2>/dev/null \
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
  live_numbers=$(
    "$HERE/lanes.sh" "$SESSION" 2>/dev/null \
      | awk 'NR>1 && $1 ~ /^[0-9]+$/ && $NF!="dead" && $NF!="stale" && $2 !~ /^free-[0-9]+$/ {print $2}' \
      | sed -E -n 's/^[A-Za-z]+([0-9]+)-.*/\1/p'
    gh pr list ${R[@]+"${R[@]}"} --state open --limit 200 --json number,body \
       -q '.[]|"\(.number)\t\(.body)"' 2>/dev/null \
      | cut -f2- | grep -oiE '(fixes|closes|resolves) #[0-9]+' | grep -oE '[0-9]+'
  )
  gh issue list ${R[@]+"${R[@]}"} --state open --limit 200 --json number,assignees \
     -q '.[]|"\(.number)\t\(.assignees|map(.login)|join(","))"' 2>/dev/null \
    | awk -F'\t' '$2!=""{print $1}' \
    | while read -r n; do
        grep -qx "$n" <<<"$live_numbers" || echo "$n"
      done
  exit 0 ;;

esac
