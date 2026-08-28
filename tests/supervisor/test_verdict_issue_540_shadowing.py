import re
import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import (  # noqa: E402
    GithubReviewVerdictSource,
    _VERDICT_LINE_RE,
    _label_inside_inline_code,
    _normalise_decision_text,
    _scan_verdict_lines,
)

from tests.supervisor.test_verdict_helpers import (  # noqa: E402
    REPO,
    _comment_runner,
)


class Issue540ShadowingTests(unittest.TestCase):
    """agent-supervisor#540: a review comment DISCUSSING the verdict gate
    could shadow a real verdict elsewhere on the same PR. Reproduction,
    verbatim off `agent-supervisor#539`'s own comment thread (`gh pr view
    539 --json comments`), not a paraphrase -- the numbered-list item a
    reviewer wrote while debating whether the gate should accept an
    `Author-Lane:`-shaped trailer as a second source of truth:

        2. **Gate accepts an independent `Verdict:` trailer as a second
        source of authorship truth.

    `Verdict:` sits inside a pair of single backticks there -- prose
    QUOTING the trailer's own syntax, not a real label. Before this fix,
    `_VERDICT_LINE_RE` matched it anyway (the literal substring "verdict:"
    immediately followed by a colon is exactly what agent-supervisor#213
    already made this regex accept, deliberately, for legitimate lead-in
    text like "## Independent review verdict: APPROVE") and the merge gate
    refused with `decision text not recognised: "TRAILER AS A SECOND
    SOURCE OF..."` -- while a correctly formatted `Verdict: APPROVE` sat
    elsewhere on the same PR."""

    SHADOW_LINE = (
        "2. **Gate accepts an independent `Verdict:` trailer as a second "
        "source of authorship truth."
    )

    # --- unit level: the instrument itself -------------------------------

    def test_backtick_quoted_verdict_is_not_a_scan_line(self):
        """The exact real shadowing text yields NO scan result at all --
        not merely an unrecognised one. `_scan_verdict_lines` must treat
        this line as if it were never a candidate, the same way a line
        inside a fenced code block already isn't."""
        self.assertEqual(_scan_verdict_lines(self.SHADOW_LINE), [])

    def test_label_inside_inline_code_detects_the_real_shape(self):
        """`_label_inside_inline_code` is the actual mechanism -- confirm
        it correctly locates the label's own span as backtick-wrapped on
        the real text, not just that the end-to-end scan comes back
        empty (which a bug in a DIFFERENT part of the pipeline could also
        produce)."""
        match = _VERDICT_LINE_RE.match(self.SHADOW_LINE)
        self.assertIsNotNone(match, "the label must still MATCH the regex -- #540's own bug was in what happens after the match, not the match itself")
        self.assertTrue(_label_inside_inline_code(self.SHADOW_LINE, match.start(1), match.end(1)))

    def test_decision_value_wrapped_in_backticks_still_matches(self):
        """Mirror case, required so this fix does not overcorrect: a REAL,
        unquoted label whose DECISION VALUE happens to be wrapped in
        backticks ("Verdict: `APPROVE`") must still resolve -- the label
        itself ("Verdict:") sits outside any backtick span here, only the
        value after it does, and `_normalise_decision_text` already
        strips that wrapping on its own. agent-supervisor#595: a
        `Review-Lane:`/`Reviewed-SHA:` pair is appended so this stays a
        qualifying line under the new complete-block requirement."""
        body = "Verdict: `APPROVE`\nReview-Lane: t:4\nReviewed-SHA: " + "a" * 40
        self.assertEqual(_scan_verdict_lines(body), [("approved", "APPROVE")])

    def test_leadin_prose_without_backticks_still_matches(self):
        """#213's own widening -- lead-in text before the label with NO
        backticks anywhere -- must be unaffected by this fix; only a
        BACKTICK-QUOTED label is excluded, not any lead-in text at all.
        agent-supervisor#595: trailer pair appended for the same reason as
        the test above."""
        body = "## Independent review of #333 -- verdict: APPROVE\nReview-Lane: t:4\nReviewed-SHA: " + "a" * 40
        self.assertEqual(_scan_verdict_lines(body), [("approved", "APPROVE")])

    # --- end-to-end: the actual refusal path ------------------------------

    def test_shadowing_comment_does_not_override_an_earlier_real_verdict(self):
        """Reproduces #540 end to end through `GithubReviewVerdictSource`,
        not just the line-scan helper: an EARLIER comment carries a real,
        correctly-formatted verdict; a LATER comment (chronologically
        last, exactly as it was on #539 before that PR later grew a
        genuine fresh verdict past it) contains ONLY the backtick-quoted
        shadowing line and ordinary discussion text around it -- no real
        decision anywhere in that later comment. The real, earlier verdict
        must still be the one resolved; before this fix, the later
        comment's own unrecognised scan became `last` and refused with
        "decision text not recognised"."""
        comments = [
            {
                "author": {"login": "jonhill90"},
                "body": "Verdict: APPROVE\nReview-Lane: estate:4\nReviewed-SHA: " + "a" * 40,
                "createdAt": "2026-08-23T21:53:26Z",
            },
            {
                "author": {"login": "jonhill90"},
                "body": (
                    "**Retracting my prior APPROVE.**\n\n"
                    "Some open questions from this review, not yet resolved:\n\n"
                    "1. Should the ledger be the only source of truth?\n"
                    + self.SHADOW_LINE
                    + "\n\n3. What about a third identity source entirely?\n"
                ),
                "createdAt": "2026-08-23T22:00:39Z",
            },
        ]
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=comments))
        result = source.verdict(repo=REPO, number=539, head_sha="a" * 40)
        self.assertEqual(result["verdict"], "approved")
        self.assertNotIn("not recognised", result.get("detail", ""))

    def test_a_genuinely_garbled_last_verdict_attempt_still_refuses(self):
        """Mutation check in the OTHER direction, required so this fix does
        not quietly undo agent-supervisor#198's own protection: a LAST
        comment that is a real (if unrecognisable) verdict ATTEMPT -- a
        `Verdict:` label with NO backticks around it, decision text this
        module cannot classify -- must still shadow an earlier approval
        and refuse, exactly as #198 established. This is what tells
        "prose that merely quotes the syntax" (this issue) apart from "a
        genuine but garbled decision" (#198) -- only the FIRST is now
        excluded. agent-supervisor#595: the garbled attempt now needs its
        OWN complete `Review-Lane:`/`Reviewed-SHA:` trailer to be operative
        at all under the new rule -- without one, `_comment_verdict` would
        simply skip past it (never finding it a qualifying line) straight
        to the earlier APPROVE, which is a real behaviour change #595
        causes but not the one this test is about; giving the garbled
        attempt its own trailer keeps this test on ITS OWN guard."""
        comments = [
            {
                "author": {"login": "jonhill90"},
                "body": "Verdict: APPROVE\nReview-Lane: estate:4\nReviewed-SHA: " + "a" * 40,
                "createdAt": "2026-08-23T21:53:26Z",
            },
            {
                "author": {"login": "jonhill90"},
                "body": "Verdict: I am genuinely unsure, leaning against\nReview-Lane: estate:4\nReviewed-SHA: " + "b" * 40,
                "createdAt": "2026-08-23T22:00:39Z",
            },
        ]
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=comments))
        result = source.verdict(repo=REPO, number=539, head_sha="a" * 40)
        self.assertEqual(result["verdict"], "unknown")
        self.assertIn("not recognised", result["detail"])

    # --- mutation check: prove the test is anchored on the real fix ------

    def test_mutation_reverting_the_inline_code_guard_turns_540_red(self):
        """Revert to the pre-fix regex (no label-capturing group, so the
        exclusion this fix adds cannot run) and confirm the exact real
        shadowing text goes back to matching as a scan line -- proving
        `test_backtick_quoted_verdict_is_not_a_scan_line` above is red
        without the fix, not passing for an unrelated reason."""
        pre_540_re = re.compile(r"^#{0,6}\s*.*?\*{0,2}verdict:\**\s*(.*)$", re.IGNORECASE)
        match = pre_540_re.match(self.SHADOW_LINE)
        self.assertIsNotNone(match, "the pre-fix regex must still match the shadowing line (that IS the bug)")
        self.assertIn("TRAILER AS A SECOND SOURCE OF", _normalise_decision_text(match.group(1)))
