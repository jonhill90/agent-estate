#!/usr/bin/env bash
# Re-run a CLOSED issue's acceptance test and reopen it if the symptom is back.
# agent-supervisor#328.
#
# THE FAILURE THIS EXISTS FOR, measured rather than argued.
#
# 2026-08-16: PR #294 "fix(supervisor): lane identity for author exclusion"
# merged. Task `as292-lane-identity` recorded complete. A second task,
# `as108-lane-identity-session`, had already completed on 2026-08-14.
# 2026-08-18, two days later: the identity bug excluded EVERY free lane from
# review dispatch, #235 was still open, and the Director reported "all three
# free lanes are now excluded ... and the guard is right in every case."
#
# Two completions, one merged PR, symptom alive. PHASES.md already records the
# general form: "seven issues closed while their symptom continued, including
# the leaked test session closed TWICE while it recurred hourly."
#
# WHY IT KEEPS HAPPENING. Jon's corpus is full of verification rules --
# `bugfix_workflow=failing_test_first`, `batch_deploy=per_fix_verification`,
# `build_verification=read_back_the_label_not_the_exit_code`,
# `completion=proven_by_running`, `verification=unprompted_before_claiming_done`.
# The intent has never been unclear. Every one of them is enforced by an agent
# REMEMBERING it at the right moment, and the verification runs exactly once,
# inside the lane, before the merge. Nothing re-runs it afterwards. So an issue
# closes on a claim and the claim decays silently.
#
# WHAT THIS DOES. An issue body may carry a fenced block:
#
#     ```acceptance
#     bash scripts/supervisor/dispatch.sh 284 x /dev/null owner/repo . --reviews-pr 205
#     ```
#
# The commands are run in the repo root. EXIT 0 MEANS THE SYMPTOM IS GONE.
# A nonzero exit on a CLOSED issue means the fix regressed, and this reopens it
# with the actual output attached.
#
# DESIGN RULES, each one earned:
#
#   - The test lives in the ISSUE, not a separate file. It is written while the
#     failing condition is still observable, the lane must satisfy it rather
#     than invent its own, and it cannot drift away from the issue it belongs to.
#   - REPORT-ONLY on open issues. An open issue whose acceptance test fails is
#     just work not done yet; saying so is noise. Only a CLOSED issue that fails
#     is news.
#   - It NEVER edits the acceptance block and never marks anything passed. Tools
#     detect and report; they never rewrite meaning (RULE E).
#   - A missing block is NOT a failure. Most issues will not have one, and
#     treating absence as a defect would make this unusable on day one. It is
#     counted and reported so coverage is visible.
#   - An UNREADABLE test is not a pass. If gh cannot fetch the issue, or the
#     command cannot run at all, say UNKNOWN -- never green.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
REPO="${ACCEPTANCE_REPO:-jonhill90/agent-supervisor}"
LIMIT="${ACCEPTANCE_LIMIT:-20}"
TIMEOUT="${ACCEPTANCE_TIMEOUT:-120}"
REOPEN="${ACCEPTANCE_REOPEN:-0}"      # default DRY: report, do not reopen
ONLY=""

while [ $# -gt 0 ]; do
  case "$1" in
    --issue)   ONLY="${2:-}"; shift 2 ;;
    --reopen)  REOPEN=1; shift ;;
    --limit)   LIMIT="${2:-20}"; shift 2 ;;
    --repo)    REPO="${2:-}"; shift 2 ;;
    *) shift ;;
  esac
done

log() { printf '%s acceptance: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*"; }

# Pull the ```acceptance fenced block out of an issue body. Prints nothing when
# there is no block -- the caller distinguishes "no block" from "empty block".
extract_block() {
  python3 -c '
import sys, re
body = sys.stdin.read()
m = re.search(r"```acceptance\s*\n(.*?)```", body, re.S)
if not m:
    sys.exit(3)
block = m.group(1).strip()
if not block:
    sys.exit(4)
print(block)
'
}

check_one() {
  local num="$1" state="$2"
  local body rc block out

  body=$(gh issue view "$num" --repo "$REPO" --json body --jq .body 2>/dev/null)
  if [ -z "$body" ]; then
    log "#$num UNKNOWN -- could not read the issue; an unreadable test is never a pass"
    UNKNOWN=$((UNKNOWN+1)); return
  fi

  block=$(printf '%s' "$body" | extract_block); rc=$?
  case "$rc" in
    3) NOBLOCK=$((NOBLOCK+1)); return ;;
    4) log "#$num has an EMPTY acceptance block -- that is a defect in the issue, not a pass"
       UNKNOWN=$((UNKNOWN+1)); return ;;
  esac

  HASBLOCK=$((HASBLOCK+1))
  out=$(cd "$REPO_ROOT" && timeout "$TIMEOUT" bash -c "$block" 2>&1); rc=$?

  if [ "$rc" -eq 0 ]; then
    log "#$num PASS ($state)"
    PASS=$((PASS+1)); return
  fi

  if [ "$state" != "CLOSED" ]; then
    # Open issue failing its own test is just work outstanding. Not news.
    log "#$num fails ($state) -- expected, the work is not done"
    OPENFAIL=$((OPENFAIL+1)); return
  fi

  log "#$num REGRESSED -- closed, but its acceptance test now exits $rc"
  REGRESSED=$((REGRESSED+1))
  REGRESSED_LIST="${REGRESSED_LIST}#${num} "

  if [ "$REOPEN" = "1" ]; then
    local note
    note="Reopened by \`acceptance.sh\`: this issue is closed but its own acceptance test now fails (exit $rc).

<details><summary>output</summary>

\`\`\`
$(printf '%s' "$out" | tail -40)
\`\`\`

</details>

This is not a new bug report -- it is the ORIGINAL symptom, still present after the issue was closed. See #328 for why closure on a claim is not closure."
    if gh issue reopen "$num" --repo "$REPO" >/dev/null 2>&1 \
       && gh issue comment "$num" --repo "$REPO" --body "$note" >/dev/null 2>&1; then
      log "#$num reopened with the failing output attached"
    else
      log "#$num regressed but could NOT be reopened -- a human must look"
    fi
  fi
}

PASS=0; REGRESSED=0; OPENFAIL=0; NOBLOCK=0; HASBLOCK=0; UNKNOWN=0; REGRESSED_LIST=""

if [ -n "$ONLY" ]; then
  state=$(gh issue view "$ONLY" --repo "$REPO" --json state --jq .state 2>/dev/null)
  [ -n "$state" ] || { log "could not read #$ONLY"; exit 2; }
  check_one "$ONLY" "$state"
else
  # Closed first -- those are the ones where a failure is news.
  while read -r num; do
    [ -n "$num" ] || continue
    check_one "$num" CLOSED
  done < <(gh issue list --repo "$REPO" --state closed --limit "$LIMIT" --json number --jq '.[].number' 2>/dev/null)
  while read -r num; do
    [ -n "$num" ] || continue
    check_one "$num" OPEN
  done < <(gh issue list --repo "$REPO" --state open --limit "$LIMIT" --json number --jq '.[].number' 2>/dev/null)
fi

log "checked $((HASBLOCK+NOBLOCK)) issues: ${HASBLOCK} with an acceptance block, ${NOBLOCK} without"
log "  pass=${PASS} regressed=${REGRESSED} open-and-failing=${OPENFAIL} unknown=${UNKNOWN}"
[ "$REGRESSED" -gt 0 ] && log "REGRESSED: ${REGRESSED_LIST}-- closed issues whose symptom is back"

# Exit 1 ONLY on a regression. Coverage gaps and open failures are reported,
# not alarmed -- an alarm that fires on the normal state gets ignored.
[ "$REGRESSED" -gt 0 ] && exit 1
exit 0
