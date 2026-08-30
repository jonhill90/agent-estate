#!/usr/bin/env bash
# Contest a STOP-CONCLUSION automatically, without anyone choosing to.
#
# WHY THIS EXISTS. Jon, 2026-08-19: "I should not be the one pointing this out.
# What about our council we talked about. we need sanity checks outside of this.
# I mean everything i said is obvious. I should not have needed to say it."
#
# He is right, and he said it before: agent-supervisor#150, filed 2026-08-15
# from his own Telegram -- "make collaboration a priority ... sanity check
# afterwards multiple agents different prompts" -- filed, and never wired.
# (skills#186 is a later, same-day follow-up on the same quote -- it names the
# concrete gap this script closes ("wire it into the loop, not the prose") but
# lives in a different repo than the one this dispatch's [repo] arg names, so
# it cannot be the dispatch's own issue reference. #150 is the agent-supervisor
# issue asking for exactly this, still open, in the repo this dispatches into.)
#
# THE PATTERN THE ESTATE KEEPS REPEATING, of which this is one more instance:
# build the thing, do not wire it. The corpus exists and no code reads it.
# `lane-logs/*.log` are written and nothing consumes them. `mark-pr-external`
# records an exemption the merge gate never checks. `ask-a-council`,
# `sanity-check` and `devils-advocate` all exist and NOTHING INVOKES THEM.
# A capability nobody triggers is indistinguishable from a capability nobody
# built.
#
# WHAT TO CONTEST, and why this is affordable. Jon's constraint is explicit --
# "it gets expensive auditing each other everything. It should be high level
# only." So this does not review work. It contests exactly one class of claim:
#
#     A CONCLUSION THAT STOPS WORK.
#
# "Nothing to dispatch." "Blocked on the rate limit." "No Phase 4 surface."
# "This needs Jon." Every failure Jon caught on 2026-08-19 was one of these,
# reached alone and contested by nobody:
#
#   - "no Phase 4 surface"     -> agent-tui#52 was open the whole time
#   - "blocked on REST"        -> REST read 4530/5000 when checked
#   - "Validate cancelled x2"  -> it was in_progress; the short SHA returns
#                                 nothing from the runs API, only the full one
#   - "this needs Jon"         -> four escalations, all answerable from the code
#   - seven windows at 46-62% unused, reported seven times, never treated as a
#     defect with a cause
#
# Stop-conclusions are RARE. That is the whole economic argument: contesting
# every one of them costs a fraction of reviewing ordinary work, and they are
# the only claims whose failure mode is silence.
#
# THE ASYMMETRY THAT JUSTIFIES IT. A wrong "keep working" wastes one lane's
# turn. A wrong "stop" wastes the entire window and produces no signal that
# anything was lost -- an idle estate looks exactly like a finished one.
#
# NO MODEL DECIDES WHETHER TO RUN THIS. That is the point. An agent that could
# choose to skip the check is an agent that will skip it precisely when it is
# most confident and most wrong.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./session-defaults.sh
. "$HERE/session-defaults.sh"
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
STAMP="$STATE/.contest-stop.last"
CLAIM=""
EVIDENCE=""
DRY="${CONTEST_STOP_DRY:-0}"
# One contest per this many seconds, so a Director idling all night does not
# spawn a reviewer every tick. The failures this catches persist for hours;
# checking hourly finds them just as well as checking every 15 minutes.
MIN_INTERVAL="${CONTEST_STOP_MIN_INTERVAL:-3000}"

while [ $# -gt 0 ]; do
  case "$1" in
    --claim)    CLAIM="${2:-}"; shift 2 ;;
    --evidence) EVIDENCE="${2:-}"; shift 2 ;;
    --dry)      DRY=1; shift ;;
    *) shift ;;
  esac
done

log() { printf '%s contest-stop: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*"; }

if [ -z "${CLAIM// }" ]; then
  echo "usage: contest-stop.sh --claim '<the conclusion that stops work>' [--evidence '<what it rests on>']" >&2
  exit 2
fi

# Rate limit. A stop-conclusion that is genuinely correct will still be there
# in fifty minutes; one that is wrong costs a window either way.
now=$(date -u +%s)
if [ -f "$STAMP" ]; then
  last=$(cat "$STAMP" 2>/dev/null || echo 0)
  case "$last" in ''|*[!0-9]*) last=0 ;; esac
  age=$(( now - last ))
  if [ "$age" -lt "$MIN_INTERVAL" ]; then
    log "contested ${age}s ago (min ${MIN_INTERVAL}s) -- not contesting again this soon"
    exit 0
  fi
fi

# The prompt is built HERE, in a tool, rather than by the agent whose
# conclusion is under test. An agent that writes its own reviewer's prompt
# writes one its reasoning already survives -- that is the documented failure
# in the sanity-check skill: "a reviewer handed a conclusion confirms it."
BRIEF="$STATE/contest-$(date -u '+%Y%m%dT%H%M%SZ').md"
cat > "$BRIEF" <<EOF
# Contest this stop-conclusion

An agent has concluded that work should STOP. Your single job is to find out
whether that is true. You are not reviewing the work; you are testing one claim.

## The claim

> ${CLAIM}

## What it is said to rest on

${EVIDENCE:-(no evidence was recorded with the claim -- that absence is itself worth reporting)}

## Your lens, which you can fail

**Is there dispatchable work right now that this conclusion missed?**

"No, the conclusion is correct and the estate genuinely has nothing to do" is a
real and useful verdict. Do not manufacture work to justify the check.

## Check these specifically, because each has been wrong before

1. **Unclaimed issues in every repo** -- agent-supervisor, agent-tui, skills,
   agent-dotfiles. Not just the one the agent was looking at. On 2026-08-19 a
   "no Phase 4 surface" conclusion held while agent-tui#52 was open and
   unclaimed the entire time.
2. **Is the stated blocker still true NOW?** Re-measure it; do not trust the
   reading the claim rests on. Two blockers that day had expired: a REST rate
   limit that read 4530/5000 when checked, and a CI job read as "cancelled
   twice" that was actually \`in_progress\` -- the abbreviated SHA returns
   nothing from the runs API, only the full 40-character form does.
3. **Open PRs needing review**, in all four repos.
4. **\`cli.py prompts unacknowledged\`** -- work Jon asked for that nobody
   picked up. Note the corpus caveats: \`conflicts\` is always 0 because
   \`links\` is empty, and a parameter mined from a question is weak evidence.
5. **If the claim is "this needs Jon"**, it is almost certainly wrong. His
   standing rule is \`escalation=only_when_unanswerable_from_repo\`. Four such
   escalations that day were all answerable from the code in under two minutes.

## Rules

- **Evidence per finding**: the command and its real output. An unevidenced
  claim is dropped, not asserted.
- **Verify with a positive control before reporting an absence.** A grep that
  matches nothing may mean the file does not exist -- that happened twice that
  day, once producing a confident claim about an \`AGENTS.md\` that is not in
  the repo at all.
- **"Could not check" is distinct** from a clean result.

## Output

1. VERDICT -- one line: is the stop-conclusion correct?
2. WORK FOUND -- specific issues or PRs, with numbers, that could be dispatched now.
3. EXPIRED BLOCKERS -- anything the claim rests on that is no longer true.
4. COULD NOT CHECK.

If you find dispatchable work, say so plainly and name it. Do not dispatch it
yourself -- reporting it is the job.
EOF

log "brief written: $BRIEF"

if [ "$DRY" = "1" ]; then
  log "DRY -- not dispatching"
  printf '%s\n' "$BRIEF"
  exit 0
fi

# Dispatch through the normal path so the contest is a lane like any other:
# claimed, recorded, and visible in the ledger rather than a side channel.
if [ -x "$HERE/dispatch.sh" ]; then
  QUOTA_GUARD_TIMEOUT_SECONDS="${QUOTA_GUARD_TIMEOUT_SECONDS:-60}" \
  QUOTA_USAGE_TIMEOUT_SECONDS="${QUOTA_USAGE_TIMEOUT_SECONDS:-60}" \
  bash "$HERE/dispatch.sh" 150 contest-stop "$BRIEF" "$AGENT_SUPERVISOR_DEFAULT_REPO_GITHUB" \
    "$(cd "$HERE/../.." && pwd)" --not-a-review >"$STATE/contest-stop.dispatch.log" 2>&1
  rc=$?
  if [ "$rc" -eq 0 ]; then
    printf '%s' "$now" > "$STAMP"
    log "contest dispatched -- the stop-conclusion is now being tested by a lane that did not reach it"
    exit 0
  fi
  log "dispatch failed (rc=$rc) -- see $STATE/contest-stop.dispatch.log; NOT stamping, so the next pass retries"
  exit 1
fi

log "dispatch.sh not executable at $HERE -- cannot contest"
exit 1
