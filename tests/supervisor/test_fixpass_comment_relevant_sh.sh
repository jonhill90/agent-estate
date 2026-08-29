#!/bin/bash
# agent-estate#811: fixpass-comment-relevant.sh is the pre-filter that
# stops fixpass-evidence.yml's issue_comment trigger from re-evaluating the
# gate on a comment that structurally cannot change its answer (a plain
# "fix pass" narration comment, posted before the real evidence comment,
# was measured publishing a spurious FAILURE check-run on #810 -- twice).
#
# This is the mutation-check the brief asks for, run in both directions:
# a comment shaped like #810's real evidence marker or a real Verdict:
# rejection must still be judged RELEVANT (exit 0, the gate must still run
# and therefore must still be able to fail a genuine rejection with no
# evidence, and pass once the marker exists -- this script does not touch
# that logic, only whether it is invoked); a comment carrying neither must
# be judged IRRELEVANT (exit 1) so it is skipped instead of spuriously
# failing the gate.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$HERE/../../scripts/supervisor/fixpass-comment-relevant.sh"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }

echo "fixpass-comment-relevant.sh"

check_relevant() {
  local desc="$1" body="$2"
  if printf '%s' "$body" | bash "$SCRIPT"; then
    ok "$desc -> relevant"
  else
    bad "$desc" "expected relevant (exit 0), got exit $?"
  fi
}

check_irrelevant() {
  local desc="$1" body="$2"
  if printf '%s' "$body" | bash "$SCRIPT"; then
    bad "$desc" "expected irrelevant (exit 1), got exit 0"
  else
    ok "$desc -> irrelevant"
  fi
}

# --- mutation direction 1: genuinely relevant comments must still fire ---

check_relevant "a real evidence-marker comment (#810's own shape)" \
'<!-- fixpass-evidence:v1 -->
$ python3 -m unittest tests.supervisor.test_itemize_prompts
Ran 44 tests in 0.291s
OK
exit code: 0'

check_relevant "a real REQUEST_CHANGES verdict comment" \
'Some findings here.

Verdict: REQUEST_CHANGES
Review-Lane: agent-estate:2
Reviewed-SHA: d421d105ecb125f67dbecf9d73414b9900b71ec7'

check_relevant "an APPROVE verdict comment (still a Verdict: line)" \
'Looks good.

Verdict: APPROVE
Review-Lane: agent-estate:3
Reviewed-SHA: abc1234'

check_relevant "lowercase verdict: label still counts (case-insensitive)" \
'quick note -- verdict: request_changes, see above'

check_relevant "marker embedded mid-comment, not just at the top" \
'Here is my fix pass writeup, and below is the evidence.

<!-- fixpass-evidence:v1 -->
$ some command
output
exit code: 0'

# --- mutation direction 2: genuinely irrelevant comments must be skipped -

check_irrelevant "#810's actual spurious narration comment (measured, no marker)" \
'Fix pass on the `REQUEST_CHANGES`: both `NOISE_PATTERNS` regexes now anchor
with `^\s*` (leading-whitespace tolerant) instead of unanchored `.search()`.

## The defect and the fix

A dispatcher-generated prompt always begins with its scaffold; a human
quoting one does not.'

check_irrelevant "plain acknowledgement with neither marker nor Verdict:" \
'Thanks, pushed a fix for this.'

check_irrelevant "empty comment body" ''

check_irrelevant "prose that mentions 'evidence' and 'verdict' as words, not trailers" \
'I read your evidence and think your verdict on this was reasonable.'

echo "fixpass-comment-relevant.sh: $pass ok, $fail failed"
[ "$fail" -eq 0 ]
