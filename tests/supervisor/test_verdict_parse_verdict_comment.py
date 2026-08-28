import re
import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import (  # noqa: E402
    _APPROVED_TOKENS,
    _VERDICT_LINE_RE,
    _normalise_decision_text,
    _parse_verdict_comment,
)


class ParseVerdictCommentTests(unittest.TestCase):
    """agent-supervisor#192: `**Verdict:` alone missed every reviewer who
    wrote plain `Verdict: APPROVE` or the heading form `## Verdict: ...` --
    #169, #176 and #191 sat blocked, and #185 merged only because ONE of its
    two simultaneous approvals happened to be bold (the issue's own
    measurement table). Table-driven over every real form plus the negative
    cases the line-scan approach newly has to guard against on its own
    (a comment-scan is reachable from inside quoted/fenced material in a way
    a whole-body-anchored check never was)."""

    # agent-supervisor#595: every POSITIVE_CASES body below now carries a
    # `Review-Lane:`/`Reviewed-SHA:` pair immediately after its `Verdict:`
    # line -- a lone label is no longer operative on its own (see
    # `_scan_verdict_lines`'s own docstring). The pair is inserted right
    # after the label line specifically, not merely appended to the end of
    # the body, so a case that puts more prose AFTER the verdict line
    # ("verdict line followed by more content on later lines") still tests
    # what it always tested: the label plus its block, with unrelated
    # trailing prose after the block, not inside it.
    _TRAILER = "\nReview-Lane: t:4\nReviewed-SHA: " + "a" * 40

    POSITIVE_CASES = [
        ("plain", "Verdict: APPROVE" + _TRAILER, "approved"),
        ("plain lowercase label", "verdict: APPROVE" + _TRAILER, "approved"),
        ("bold, no closing stars", "**Verdict: APPROVE**" + _TRAILER, "approved"),
        ("bold label only, decision outside", "**Verdict:** APPROVE" + _TRAILER, "approved"),
        ("bold, closing stars immediately after word", "**Verdict: APPROVE**" + _TRAILER, "approved"),
        ("heading form (#169's 18:45 REQUEST CHANGES)", "## Verdict: REQUEST CHANGES" + _TRAILER, "rejected"),
        ("heading, bold decision", "## **Verdict: REQUEST CHANGES**" + _TRAILER, "rejected"),
        ("indented", "    Verdict: APPROVE" + _TRAILER, "approved"),
        ("trailing punctuation", "Verdict: APPROVE." + _TRAILER, "approved"),
        ("trailing period, bold", "**Verdict: APPROVE.**" + _TRAILER, "approved"),
        ("verdict line after prose (real reviews explain themselves first)",
         "Looks correct, tests pass.\n\nVerdict: APPROVE" + _TRAILER, "approved"),
        ("verdict line followed by more content on later lines",
         "**Verdict: REQUEST CHANGES**" + _TRAILER + "\n\nThe patch-id comparison is wrong.", "rejected"),
        ("request changes, plain", "Verdict: REQUEST CHANGES" + _TRAILER, "rejected"),
        # agent-supervisor#213: three real APPROVE verdicts blocked by
        # formatting alone, measured verbatim off real PRs the day this was
        # filed.
        ("#321: prefix text before the label, plus a trailing '+' action",
         "## Independent review verdict: APPROVE + MERGE" + _TRAILER, "approved"),
        ("#331: emphasis wrapped AROUND the decision, not just the label",
         "## Verdict: **APPROVE**" + _TRAILER, "approved"),
        ("#333: prefix text before the label, em-dash separator",
         "## Independent review of #333 — verdict: APPROVE" + _TRAILER, "approved"),
        # agent-supervisor#475: three real verdicts measured 2026-08-21 that
        # a human reader would call unambiguous and this module refused.
        ("#475 case 1 (agent-supervisor#472, round 1): title case, reversed word order",
         "## Verdict: Changes requested" + _TRAILER, "rejected"),
        ("#475 case 2 (jonhill90/skills#228, round 1): lowercase, hyphenated, reversed order",
         "Verdict: changes-requested" + _TRAILER, "rejected"),
    ]

    NEGATIVE_CASES = [
        ("word 'verdict' in prose, no colon-label", "I think the verdict here should be REQUEST CHANGES, but I won't file one yet."),
        ("verdict quoted inside a fenced code block",
         "Example of the format:\n```\nVerdict: APPROVE\n```\nDon't actually use this yet."),
        ("verdict quoting ANOTHER comment (markdown blockquote)",
         "> Verdict: APPROVE\n\nI disagree with this, filing my own review separately."),
        ("verdict quoting another comment, bold form",
         "> **Verdict: APPROVE**\n\nThat was someone else's, not mine."),
        ("no verdict line at all", "LGTM, thanks for the fix."),
        ("label present, decision word unrecognised", "Verdict: LOOKS OK TO ME"),
        ("empty body", ""),
        ("None body", None),
        # agent-supervisor#213 verification bar: adversarial cases that must
        # still refuse.
        ("empty decision after the colon", "Verdict:"),
        ("'approve' mentioned in prose, no verdict line at all",
         "I'd approve this if the tests passed, but they don't yet."),
        ("'approved with changes' -- a real qualifier, not a trailing action",
         "Verdict: APPROVED WITH CHANGES"),
    ]

    def test_every_real_and_wild_form_is_recognised(self):
        for name, body, expected in self.POSITIVE_CASES:
            with self.subTest(name):
                self.assertEqual(_parse_verdict_comment(body), expected)

    def test_every_negative_case_reads_none(self):
        for name, body in self.NEGATIVE_CASES:
            with self.subTest(name):
                self.assertIsNone(_parse_verdict_comment(body))

    def test_mutation_restricting_the_match_to_bold_only_turns_the_plain_and_heading_cases_red(self):
        """The bar #192 sets, run the way #218's own mutation test in this
        file is: restrict the match back to exactly the pre-#192 shape
        (`**Verdict:` at the very start of the whole body) and confirm the
        plain-text and heading-form cases -- the ones #169, #176 and #191
        actually used -- go red. This IS the "mutate and watch it fail"
        step; if this test ever passed with the restricted pattern, the
        positive-case tests above would not be real evidence of the fix."""
        bold_only_prefix = "**Verdict:"

        def pre_192_parse(body):
            text = (body or "").strip()
            if not text.startswith(bold_only_prefix):
                return None
            remainder = text[len(bold_only_prefix):]
            end = remainder.find("**")
            decision_text = (remainder[:end] if end != -1 else remainder).strip().upper()
            if "REQUEST CHANGES" in decision_text:
                return "rejected"
            if "APPROVE" in decision_text:
                return "approved"
            return None

        regressions = [
            (name, body, expected)
            for name, body, expected in self.POSITIVE_CASES
            if pre_192_parse(body) != expected
        ]
        self.assertTrue(
            regressions,
            "restricting the match to bold-only did not turn any current-fix case red -- "
            "the positive-case table has no coverage of the #192 defect",
        )
        # And the fixed parser must still get every one of those right --
        # proving the RED->GREEN transition the mutation is meant to show.
        for name, body, expected in regressions:
            with self.subTest(name):
                self.assertEqual(_parse_verdict_comment(body), expected)

    def test_verdict_line_regex_requires_the_colon(self):
        """A sanity check on the instrument itself, not just its caller:
        "Verdict" without a trailing colon must not match at all -- a label
        without ":" is not one of the forms #192 names."""
        self.assertIsNone(_VERDICT_LINE_RE.match("Verdict APPROVE"))

    def test_mutation_restricting_the_label_to_the_line_start_turns_213_cases_red(self):
        """agent-supervisor#213: mutate `_VERDICT_LINE_RE` back to requiring
        "verdict:" immediately after the optional heading/emphasis markers
        (its shape before this widening) and confirm #321's and #333's
        prefix-text cases -- real APPROVE verdicts that sat blocked a full
        dispatch cycle -- go red. If they did not, the positive-case table
        would not be real evidence of the fix."""
        pre_213_re = re.compile(r"^#{0,6}\s*\*{0,2}verdict:\**\s*(.*)$", re.IGNORECASE)

        def pre_213_parse(body):
            for raw_line in (body or "").splitlines():
                line = raw_line.strip()
                if not pre_213_re.match(line):
                    continue
                return "matched"
            return None

        prefix_cases = [
            (name, body, expected)
            for name, body, expected in self.POSITIVE_CASES
            if "prefix text before the label" in name
        ]
        self.assertTrue(prefix_cases, "no prefix-text case in the positive table to prove the mutation against")
        for name, body, expected in prefix_cases:
            with self.subTest(name):
                self.assertIsNone(pre_213_parse(body))
                self.assertEqual(_parse_verdict_comment(body), expected)

    def test_mutation_truncating_at_the_first_asterisk_turns_331_red(self):
        """agent-supervisor#213: mutate the decision normaliser back to
        "cut at the first `**` found anywhere in the rest of the line" --
        the pre-fix shape, which reads the OPENING emphasis of a decision
        wrapped like `**APPROVE**` as if it were a closing marker and
        truncates the decision to "". Confirm #331's case goes red under
        that shape."""
        def pre_213_normalise(rest):
            text = rest.strip()
            end = text.find("**")
            if end != -1:
                text = text[:end]
            text = re.sub(r"[*_`]+$", "", text.strip()).strip()
            text = text.rstrip(".:;,!").strip()
            return re.sub(r"\s+", " ", text).upper()

        match = _VERDICT_LINE_RE.match("## Verdict: **APPROVE**")
        self.assertIsNotNone(match)
        self.assertEqual(pre_213_normalise(match.group(2)), "")
        # agent-supervisor#595: `_parse_verdict_comment` now requires a
        # complete trailer block, so the label alone (used above to test the
        # regex/normaliser directly) needs its own Review-Lane:/Reviewed-SHA:
        # pair to reach a decision at all.
        self.assertEqual(_parse_verdict_comment("## Verdict: **APPROVE**" + self._TRAILER), "approved")

    def test_mutation_475_a_rejected_token_set_without_the_reversed_forms_turns_cases_1_and_2_red(self):
        """agent-supervisor#475: mutate `_REJECTED_TOKENS` back to its
        pre-fix shape -- no "CHANGES REQUESTED" entry -- and confirm the two
        reversed-word-order cases (measured verbatim off real PR comments)
        go red. If they did not, the positive-case table would not be real
        evidence of the fix."""
        pre_475_rejected_tokens = frozenset({"REQUEST CHANGES", "REQUEST-CHANGES", "REJECTED"})

        def pre_475_classify(decision_text):
            if any(marker in decision_text for marker in ("NOT", "DIS", "NO", "N'T")):
                return None
            if decision_text in _APPROVED_TOKENS:
                return "approved"
            if decision_text in pre_475_rejected_tokens:
                return "rejected"
            return None

        cases_475 = [
            (name, body, expected)
            for name, body, expected in self.POSITIVE_CASES
            if name.startswith("#475 case")
        ]
        self.assertTrue(cases_475, "no #475 case in the positive table to prove the mutation against")
        for name, body, expected in cases_475:
            with self.subTest(name):
                # agent-supervisor#595: `body` now carries a trailer block
                # after the label line, so matching the WHOLE body no longer
                # works (`_VERDICT_LINE_RE` has no DOTALL/MULTILINE, so `.*$`
                # cannot span the newline into the trailer) -- match just the
                # label's own first line, exactly what the real per-line scan
                # in `_scan_verdict_lines` does.
                first_line = body.strip().splitlines()[0]
                match = _VERDICT_LINE_RE.match(first_line)
                self.assertIsNotNone(match)
                decision_text = _normalise_decision_text(match.group(2))
                self.assertIsNone(pre_475_classify(decision_text))
                self.assertEqual(_parse_verdict_comment(body), expected)

    def test_475_case_2_hyphenated_reversed_order_normalises_via_hyphen_folding(self):
        """`_normalise_decision_text` folds hyphens to spaces before the
        token compare, so "changes-requested" reaches the same normalised
        form as "changes requested" and needs no separate hyphenated entry
        in `_REJECTED_TOKENS` (agent-supervisor#475 case 2, jonhill90/skills#228)."""
        match = _VERDICT_LINE_RE.match("Verdict: changes-requested")
        self.assertIsNotNone(match)
        self.assertEqual(_normalise_decision_text(match.group(2)), "CHANGES REQUESTED")
