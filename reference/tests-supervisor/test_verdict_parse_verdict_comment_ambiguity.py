import re
import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import (  # noqa: E402
    _VERDICT_LINE_RE,
    _parse_verdict_comment,
)


class ParseVerdictCommentAmbiguityTests(unittest.TestCase):
    """agent-supervisor#196: the line-scan #192 introduced reaches every
    qualifying line in a comment, not just the first -- which made a SECOND
    verdict line inside the same comment reachable for the first time (the
    old body-start anchor could only ever see the first `**Verdict:` of the
    whole body). A reviewer who drafts `Verdict: APPROVE`, reconsiders, and
    writes `Verdict: REQUEST CHANGES` below it must not have that rejection
    silently overridden by the earlier approval -- first-match-wins is
    exactly the "REQUEST CHANGES read as approved" failure this module
    exists to prevent, just triggered from inside one comment instead of
    across two. Table-driven over every combination the docstring commits
    to a rule for."""

    # agent-supervisor#595: every `Verdict:` line below now carries its OWN
    # `Review-Lane:`/`Reviewed-SHA:` pair immediately after it, so each one
    # stays a qualifying line under the new complete-block requirement -- a
    # comment with two conflicting verdict BLOCKS must still refuse as
    # ambiguous, the same as it did with two bare labels before this fix.
    _TRAILER_A = "\nReview-Lane: t:4\nReviewed-SHA: " + "a" * 40
    _TRAILER_B = "\nReview-Lane: t:5\nReviewed-SHA: " + "b" * 40

    CASES = [
        (
            "conflicting lines, approve then reject, refuse as ambiguous",
            "Verdict: APPROVE" + _TRAILER_A + "\n\nActually wait.\n\nVerdict: REQUEST CHANGES" + _TRAILER_B,
            None,
        ),
        (
            "conflicting lines, reject then approve, refuse as ambiguous",
            "Verdict: REQUEST CHANGES" + _TRAILER_A + "\n\nOn reflection.\n\nVerdict: APPROVE" + _TRAILER_B,
            None,
        ),
        (
            "two identical decisions, not a conflict",
            "Verdict: APPROVE" + _TRAILER_A + "\n\nStill true after re-reading.\n\nVerdict: APPROVE" + _TRAILER_B,
            "approved",
        ),
        (
            "first line's decision word is unrecognised, a later line is valid",
            "Verdict: LOOKS OK TO ME" + _TRAILER_A + "\n\nOn second thought:\n\nVerdict: REQUEST CHANGES" + _TRAILER_B,
            "rejected",
        ),
        (
            "one line only, unaffected by the multi-line rule",
            "Verdict: REQUEST CHANGES" + _TRAILER_A,
            "rejected",
        ),
    ]

    def test_multi_line_ambiguity_cases(self):
        for name, body, expected in self.CASES:
            with self.subTest(name):
                self.assertEqual(_parse_verdict_comment(body), expected)

    def test_mutation_reverting_to_first_match_wins_turns_the_conflict_cases_red(self):
        """Mutate back to "return on the first qualifying line" -- #196's
        own bug, and #192's actual shape before this fix -- and confirm the
        conflicting-line cases above go red. If they did not, the ambiguity
        table above would not be real evidence of the fix."""

        def first_match_wins_parse(body):
            in_fence = False
            for raw_line in (body or "").splitlines():
                line = raw_line.strip()
                if line.startswith("```"):
                    in_fence = not in_fence
                    continue
                if in_fence or line.startswith(">"):
                    continue
                match = _VERDICT_LINE_RE.match(line)
                if not match:
                    continue
                rest = match.group(2)
                end = rest.find("**")
                if end != -1:
                    rest = rest[:end]
                decision_text = re.sub(r"[*_`]+$", "", rest.strip()).strip()
                decision_text = decision_text.rstrip(".:;,!").strip().upper()
                if "REQUEST CHANGES" in decision_text:
                    return "rejected"
                if "APPROVE" in decision_text:
                    return "approved"
                return None
            return None

        regressions = [
            (name, body, expected)
            for name, body, expected in self.CASES
            if first_match_wins_parse(body) != expected
        ]
        self.assertTrue(
            regressions,
            "reverting to first-match-wins did not turn any ambiguity case red -- "
            "the ambiguity table has no coverage of the #196 defect",
        )
        for name, body, expected in regressions:
            with self.subTest(name):
                self.assertEqual(_parse_verdict_comment(body), expected)
