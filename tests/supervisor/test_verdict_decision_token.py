import re
import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import (  # noqa: E402
    _classify_decision_text,
    _parse_verdict_comment,
)


class DecisionTokenTests(unittest.TestCase):
    """agent-supervisor#198: the decision half of `_parse_verdict_comment`
    was an `in` substring test -- `"APPROVE" in decision_text` reads
    `Verdict: NOT APPROVED` and `Verdict: DISAPPROVE` as approved, because
    both strings CONTAIN "APPROVE". Measured on `main` before this fix
    (the issue's own table):

        Verdict: NOT APPROVED   -> approved
        Verdict: DISAPPROVE     -> approved

    And the asymmetry that made it dangerous rather than merely wrong:
    `REQUEST CHANGES` was checked first, so a body naming both phrases
    correctly refused -- but any rejection phrased WITHOUT those two exact
    words that happened to contain "APPROVE" as a substring landed on
    approve. The failure was one-directional and it always favoured
    merging. This table is red against the pre-fix substring test and
    green against `_classify_decision_text` -- see
    `test_mutation_reverting_to_a_substring_test_turns_the_negation_cases_red`
    below for the actual red/green transition."""

    # (name, raw decision text as it appears after "Verdict:", expected)
    CASES = [
        ("plain approve", "APPROVE", "approved"),
        ("approved, past tense", "APPROVED", "approved"),
        ("request changes, space form", "REQUEST CHANGES", "rejected"),
        ("request changes, hyphen form", "REQUEST-CHANGES", "rejected"),
        ("rejected, one word", "REJECTED", "rejected"),
        # agent-supervisor#475: the reversed, natural word order.
        ("reversed order, changes requested", "CHANGES REQUESTED", "rejected"),
        ("reversed order, lowercase", "changes requested", "rejected"),
        ("lowercase approve", "approve", "approved"),
        ("lowercase request changes", "request changes", "rejected"),
        ("extra internal whitespace", "REQUEST   CHANGES", "rejected"),
        ("surrounding whitespace", "   APPROVE   ", "approved"),
        # The issue's own measured regressions:
        ("negated with NOT -- the issue's own measurement", "NOT APPROVED", None),
        ("negated with DIS -- the issue's own measurement", "DISAPPROVE", None),
        ("negated in prose", "not approving this", None),
        ("negated with a contraction", "I don't approve this", None),
        ("trailing question mark reads as uncertain, not a decision", "APPROVE?", None),
        # The no-right-answer case named explicitly in the brief: a real
        # thing reviewers write, and it is neither approval nor rejection.
        ("approved with changes -- neither, must not resolve", "APPROVED WITH CHANGES", None),
        ("no decision at all", "", None),
        ("unrelated text", "LOOKS GOOD", None),
    ]

    def _normalise(self, decision_text):
        text = re.sub(r"[*_`]+$", "", decision_text.strip()).strip()
        text = text.rstrip(".:;,!").strip()
        return re.sub(r"\s+", " ", text).upper()

    def test_every_decision_token_case(self):
        for name, decision_text, expected in self.CASES:
            with self.subTest(name):
                self.assertEqual(_classify_decision_text(self._normalise(decision_text)), expected)

    def test_every_decision_token_case_through_a_full_verdict_line(self):
        """Same table, exercised through the real entry point (a whole
        `Verdict:` line), not just the normalised-text helper. agent-
        supervisor#595: a `Review-Lane:`/`Reviewed-SHA:` pair is appended so
        the label is even CONSIDERED under the new complete-block
        requirement -- without it every case in this table would resolve
        `None` for a reason unrelated to what this test is actually
        checking (decision-token classification, not block completeness)."""
        for name, decision_text, expected in self.CASES:
            with self.subTest(name):
                body = f"Verdict: {decision_text}\nReview-Lane: t:4\nReviewed-SHA: " + "a" * 40
                self.assertEqual(_parse_verdict_comment(body), expected)

    def test_mutation_reverting_to_a_substring_test_turns_the_negation_cases_red(self):
        """Mutate back to the pre-#198 shape -- `"REQUEST CHANGES" in text`
        then `"APPROVE" in text` -- and confirm the negation cases from the
        table above (the ones the issue measured on `main`) go red. If they
        did not, the table above would not be real evidence of the fix."""

        def pre_198_classify(decision_text):
            if "REQUEST CHANGES" in decision_text:
                return "rejected"
            if "APPROVE" in decision_text:
                return "approved"
            return None

        regressions = [
            (name, decision_text, expected)
            for name, decision_text, expected in self.CASES
            if pre_198_classify(self._normalise(decision_text)) != expected
        ]
        self.assertTrue(
            regressions,
            "reverting to a substring test did not turn any case red -- "
            "the decision-token table has no coverage of the #198 defect",
        )
        for name, decision_text, expected in regressions:
            with self.subTest(name):
                self.assertEqual(_classify_decision_text(self._normalise(decision_text)), expected)
