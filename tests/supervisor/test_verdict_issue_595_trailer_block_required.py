import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import _parse_verdict_comment  # noqa: E402

from tests.supervisor.test_verdict_helpers import _pre_595_scan  # noqa: E402


def _pre_595_parse(body):
    decisions = {d for d, _ in _pre_595_scan(body) if d is not None}
    if len(decisions) == 1:
        return decisions.pop()
    return None


class Issue595TrailerBlockRequiredTests(unittest.TestCase):
    """agent-supervisor#595: the director's decision, implemented directly --
    an operative verdict requires `Verdict:`/`Review-Lane:`/`Reviewed-SHA:`
    as three consecutive raw lines, nothing between them. The poisoning
    fixtures below are quoted VERBATIM from the two real incidents named in
    #595's own issue body (both PR comments were later edited by their
    authors with a note pointing back to #595 -- the historical record is
    untouched here; these are reconstructed standalone repros, not edits to
    anyone's real comment) plus the fourth shape #595's issue body names
    directly. Every "must resolve" fixture here is proven with a mutation
    test showing the OLD code (`_pre_595_scan`, no block requirement) DOES
    resolve it or DOES fool the parser -- the same "prove the check can
    fail" standard this file already holds itself to everywhere else."""

    # --- #553: a stale-SHA discussion whose own prose contains "verdict:" -

    POISON_553_SENTENCE = (
        "A stale `Reviewed-SHA` does not automatically sink a verdict: "
        "`verdict.py` can promote"
    )
    # #553's OWN separate illustrative line: label AND a SHA-shaped trailer
    # combined on one line, but explicitly NO `Review-Lane:` at all.
    POISON_553_ILLUSTRATIVE_LINE = "verdict   Verdict: APPROVE   Reviewed-SHA: 912f2aa1"

    # --- #531: the same label wrapped at end-of-line ----------------------

    POISON_531_WRAPPED = "...covered is the verdict:"

    # --- #595's own issue body: a bold-wrapped MID-SENTENCE QUOTE ---------

    POISON_QUOTE_VARIANTS = [
        'I saw "**Verdict: APPROVE**" in the log',
        "the comment said **Verdict: APPROVE** but the reviewer had not filed one",
        "| **Verdict: APPROVE** | shadowed |",
        "Do not write **Verdict: APPROVE** in a diagnosis",
    ]

    def test_553_mid_sentence_poisoning_sentence_produces_no_decision(self):
        self.assertIsNone(_parse_verdict_comment(self.POISON_553_SENTENCE))

    def test_mutation_553_sentence_without_the_block_requirement_would_have_poisoned(self):
        """Proves the OLD code was actually vulnerable to this exact text --
        the substring "verdict:" mid-sentence matched the label regex and
        normalised to a garbage decision (measured on `main` as
        `VERDICT.PY` in #595's own incident report)."""
        old_scan = _pre_595_scan(self.POISON_553_SENTENCE)
        self.assertTrue(old_scan, "the old, unfixed scan must find a matching line here -- that IS the bug")
        self.assertIsNone(_parse_verdict_comment(self.POISON_553_SENTENCE), "the real, current parser must refuse")

    def test_553_illustrative_line_with_no_review_lane_produces_no_decision(self):
        """#553's own comment ALSO separately contained a single line
        combining the label and a SHA-shaped trailer with NO `Review-Lane:`
        at all -- must not resolve on its own either."""
        self.assertIsNone(_parse_verdict_comment(self.POISON_553_ILLUSTRATIVE_LINE))

    def test_mutation_553_illustrative_line_is_refused_under_both_old_and_new_logic(self):
        """Measured, not assumed: this specific line does NOT turn out to
        fool the OLD code either -- `Reviewed-SHA: 912f2aa1` trailing on the
        SAME line as the decision becomes part of the decision TEXT once
        normalised (`APPROVE REVIEWED SHA: 912F2AA1`), which is not an exact
        token match under either regime, so it was already refused before
        #595 too. Kept here (not dropped) as a fixture, with the honest
        measured result, rather than asserting a vulnerability this line
        does not actually demonstrate -- #553's real vulnerability is the
        mid-sentence sentence above, not this line."""
        self.assertIsNone(_pre_595_parse(self.POISON_553_ILLUSTRATIVE_LINE))
        self.assertIsNone(_parse_verdict_comment(self.POISON_553_ILLUSTRATIVE_LINE))

    def test_531_end_of_line_wrap_produces_no_decision(self):
        self.assertIsNone(_parse_verdict_comment(self.POISON_531_WRAPPED))

    def test_mutation_531_without_the_block_requirement_would_have_poisoned(self):
        """#531's own incident: the label matched with an EMPTY decision
        text (nothing after the colon on that line), which the OLD code's
        `_bare_decision_line` fallback (wired into `_scan_verdict_lines`
        before #595) could then have gone hunting for a standalone decision
        word elsewhere in the comment to adopt -- exactly the shape #475
        case 3 introduced and #595 retires. Confirmed here on the label
        match alone: it exists, with empty text, under the old code."""
        old_scan = _pre_595_scan(self.POISON_531_WRAPPED)
        self.assertTrue(old_scan, "the old scan must still find the label line -- that IS the exposure")
        self.assertEqual(old_scan[0][1], "", "the decision text is empty, exactly #531's own measured shape")
        self.assertIsNone(_parse_verdict_comment(self.POISON_531_WRAPPED))

    def test_bold_wrapped_mid_sentence_quote_variants_produce_no_decision(self):
        for variant in self.POISON_QUOTE_VARIANTS[:2]:
            with self.subTest(variant):
                self.assertIsNone(_parse_verdict_comment(variant))

    def test_mutation_bold_quote_variants_without_the_block_requirement_would_have_poisoned(self):
        for variant in self.POISON_QUOTE_VARIANTS[:2]:
            with self.subTest(variant):
                old_result = _pre_595_parse(variant)
                self.assertEqual(old_result, "approved", "the old, unfixed parser must resolve this -- that IS the bug")
                self.assertIsNone(_parse_verdict_comment(variant))

    # --- shapes that must still resolve, with a real trailer block --------

    def test_540_leadin_prose_with_a_trailer_block_still_resolves_approved(self):
        body = "Final call — Verdict: APPROVE\nReview-Lane: t:4\nReviewed-SHA: " + "a" * 40
        self.assertEqual(_parse_verdict_comment(body), "approved")

    def test_mutation_540_leadin_prose_without_the_fix_also_resolves_the_same_way(self):
        """Unlike the poisoning fixtures, this one is not SUPPOSED to flip --
        #213's lead-in tolerance was never the bug; #595 only narrows which
        MATCH counts as operative, and this fixture already carries a real,
        complete block. Both the old (bare-label) and new (block-required)
        logic resolve it the same way -- proving #595 did not accidentally
        narrow this legitimate shape."""
        body = "Final call — Verdict: APPROVE\nReview-Lane: t:4\nReviewed-SHA: " + "a" * 40
        self.assertEqual(_pre_595_parse(body), "approved")
        self.assertEqual(_parse_verdict_comment(body), "approved")

    def test_544_bold_label_outside_decision_with_a_trailer_block_still_resolves_approved(self):
        body = "**Verdict:** APPROVE\nReview-Lane: t:4\nReviewed-SHA: " + "a" * 40
        self.assertEqual(_parse_verdict_comment(body), "approved")
