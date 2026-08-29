#!/bin/bash
# agent-estate#811: fixpass-evidence.yml's issue_comment trigger fires on
# EVERY comment posted to a PR, not just ones that could change the gate's
# answer. Measured on #810, twice: a lane posted a "fix pass" narration
# comment (no marker) before its actual evidence comment -- 3m04s apart the
# first time, 6s the second -- and each narration comment alone re-ran
# fixpass_evidence_gate.py, which (correctly, at that instant) found no
# evidence yet and published an explicit FAILURE check-run. The gate's own
# answer was never wrong; the trigger fired on comments that could not
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
