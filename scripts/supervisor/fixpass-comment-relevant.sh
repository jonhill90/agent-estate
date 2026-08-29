#!/bin/bash
# agent-estate#811: fixpass-evidence.yml's issue_comment trigger fires on
# EVERY comment posted to a PR, not just ones that could change the gate's
# answer. #811 originally claimed this raced twice on #810; #812's reviewer
# matched every fixpass-evidence.yml run against #810 to its triggering
# comment and log line, which found ONE genuine race, not two (see #811's
# correction comment, and #812's PR body):
#
#   comment (UTC)                          gate output              race?
#   11:39:18 round-1 rejection             "carries no marker"      no -- correct, no evidence yet
#   11:48:52 fix-pass narration, no marker "carries no marker"      YES -- the one genuine race
#   11:51:43 evidence                      SUCCESS                  --
#   12:05:24 round-2 rejection             "predate the most        no -- correct
#                                            recent rejection"
#   12:13:52 evidence, carries the marker  "all predate the most    no -- the comment itself was
#                                            recent rejection"        malformed (missing its
#                                                                      `exit code:` line); the gate
#                                                                      was right to not count it
#   12:15:52 evidence re-post              SUCCESS                  --
#
# The 11:48:52 narration comment (no marker) re-ran fixpass_evidence_gate.py,
# which (correctly, at that instant) found no evidence yet and published an
# explicit FAILURE check-run seconds before the real evidence existed -- that
# is the one defect this filter fixes. The gate's own answer was never
# wrong in either case; the trigger fired on a comment that could not
# possibly change it.
#
# THE FIX IS A PRE-FILTER, NOT A GRACE WINDOW. fixpass_evidence_gate.py's
# own evaluate() can only be swayed by a comment in exactly two ways: it
# carries the literal EVIDENCE_MARKER (fixpass_evidence_gate.py's
# `_evidence_blocks`), or it is a rejection -- which requires a `Verdict:`
# line (`_is_rejected_verdict_comment`, verdict.py's `_scan_verdict_lines`).
# A comment containing NEITHER substring is a structural no-op for the
# gate: skipping evaluation for it cannot hide a real answer, only avoid
# publishing a stale re-read of the same unchanged state.
#
# This is deliberately a case-insensitive SUPERSET match on 'verdict:',
# never narrower than verdict.py's own classifier (which also requires a
# paired Review-Lane:/Reviewed-SHA: block, three-line-exact per #595) --
# false positives here just mean an occasional harmless extra evaluation;
# false negatives would mean a real verdict comment gets silently skipped,
# which is the direction that must never happen.
#
# Reads the comment body on stdin (never interpolated into a `run:` shell
# script directly -- caller passes it via an env var to avoid GH Actions
# script injection from untrusted comment text). Exit 0 = relevant,
# evaluate the gate. Exit 1 = irrelevant, skip.
set -uo pipefail

body="$(cat)"

if grep -qF -- '<!-- fixpass-evidence:v1 -->' <<<"$body"; then
  exit 0
fi
if grep -qi -- 'verdict:' <<<"$body"; then
  exit 0
fi
exit 1
