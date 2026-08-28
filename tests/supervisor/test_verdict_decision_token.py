import re
import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import (  # noqa: E402
    _classify_decision_text,
    _normalise_decision_text,
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
        # agent-estate#798 (found reviewing agent-estate#797): GitHub's own
        # spelling. `_normalise_decision_text` used to read the intraword
        # `_` as markdown emphasis and truncate the decision text to
        # "REQUEST" before it was ever classified -- fails closed (`None`,
        # not a wrong "approved"), but a rejection reading as unresolved is
        # the exact "no verdict" vs. "a verdict of no" confusion this
        # module exists to avoid. See
        # `test_mutation_reverting_the_underscore_fold_turns_this_case_red`
        # below for the red/green transition.
        ("request changes, underscore form (GitHub's own spelling)", "REQUEST_CHANGES", "rejected"),
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
        # agent-estate#798: fold an INTRAWORD underscore (alphanumeric on
        # both sides, e.g. the `_` in "REQUEST_CHANGES") to a space, the
        # same way the real `_normalise_decision_text` now does -- a
        # boundary underscore (`_APPROVE_`) is left for the trailing-marker
        # strip below, unchanged from before this fix.
        text = re.sub(r"(?<=[A-Za-z0-9])_(?=[A-Za-z0-9])", " ", decision_text.strip())
        text = re.sub(r"[*_`]+$", "", text).strip()
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

    def test_mutation_reverting_the_underscore_fold_turns_this_case_red(self):
        """agent-estate#798: mutate `_normalise_decision_text` back to its
        pre-fix shape -- no intraword-underscore fold, so a remaining `_`
        is treated purely as an emphasis marker and truncates the decision
        text at the first one found -- and confirm the underscore case goes
        red (`None`, not `"rejected"`) while the space/hyphen/approve forms
        it did not touch stay green. If it did not go red, the new case
        above would not be real evidence of this fix; the mutation is a
        literal revert of the one line this fix adds, not a rewrite of the
        whole function, so a green result here would mean the case is
        classified some other way, not by the fold this PR ships."""
        import re as _re

        def pre_798_normalise(rest):
            text = rest.strip()
            text = _re.sub(r"^[*_`]+", "", text)
            marker = _re.search(r"[*_`]", text)
            if marker:
                text = text[: marker.start()]
            text = text.strip().rstrip(".:;,!").strip()
            text = text.replace("-", " ")
            text = _re.sub(r"\s+", " ", text).upper()
            if "+" in text:
                text = text.split("+", 1)[0].strip()
            return text

        mutated = _classify_decision_text(pre_798_normalise("REQUEST_CHANGES"))
        self.assertIsNone(
            mutated,
            "reverting the underscore fold did not turn the underscore case red -- "
            "it is not exercising the fold this fix adds",
        )

        # The fold this fix adds does not touch these -- confirm they are
        # unaffected by the mutation, not merely unaffected by the fix.
        for decision_text, expected in (
            ("REQUEST CHANGES", "rejected"),
            ("REQUEST-CHANGES", "rejected"),
            ("APPROVE", "approved"),
        ):
            with self.subTest(decision_text):
                self.assertEqual(
                    _classify_decision_text(pre_798_normalise(decision_text)), expected
                )

        # And the real, unmutated function must classify all four the same
        # way -- three unaffected, one now fixed.
        for decision_text, expected in (
            ("REQUEST_CHANGES", "rejected"),
            ("REQUEST-CHANGES", "rejected"),
            ("REQUEST CHANGES", "rejected"),
            ("APPROVE", "approved"),
        ):
            with self.subTest("fixed:" + decision_text):
                self.assertEqual(
                    _classify_decision_text(_normalise_decision_text(decision_text)), expected
                )
