import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import (  # noqa: E402
    GithubReviewVerdictSource,
    _scan_verdict_lines,
)

from tests.supervisor.test_verdict_helpers import (  # noqa: E402
    REPO,
    _comment_runner,
)


class CommentVerdictUnrecognisedTextTests(unittest.TestCase):
    """agent-supervisor#198: `None` from `_parse_verdict_comment` must reach
    the gate as a refusal NAMING the unrecognised text -- not silently fall
    through to `none` (as if nothing had ever been posted) or to a stale,
    superseded decisive comment underneath it. `verdict()` composes
    `_comment_verdict` with the review side; these tests go straight at
    `GithubReviewVerdictSource.verdict()` so the assertion is on what the
    module actually returns to a caller, not on an internal helper."""

    def test_unrecognised_last_comment_refuses_with_the_offending_text_named(self):
        """agent-supervisor#595: a `Review-Lane:`/`Reviewed-SHA:` pair is
        added so this stays the shape it was always meant to test -- a
        comment that LOOKS LIKE a genuine, complete verdict attempt (label
        AND both trailer lines present) but whose decision word this module
        cannot classify. Per #595's decision, that shape must still refuse
        loudly with the text named, not silently resolve to `none` (a bare
        label with no trailer at all is the DIFFERENT, newly-introduced
        `none` case -- see `Issue595TrailerBlockRequiredTests` for that
        one)."""
        comments = [{
            "author": {"login": "codex"},
            "body": "Verdict: NOT APPROVED\nReview-Lane: t:4\nReviewed-SHA: " + "a" * 40,
            "createdAt": "2026-08-15T12:00:00Z",
        }]
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=comments))
        result = source.verdict(repo=REPO, number=1)
        self.assertEqual(result["verdict"], "unknown")
        self.assertNotEqual(result["detail"], "")
        self.assertIn("NOT APPROVED", result["detail"])

    def test_unrecognised_last_comment_does_not_fall_back_to_an_earlier_valid_one(self):
        """The dangerous shape: a reviewer approved, then rejected in words
        this module cannot classify. The rejection must win as a refusal --
        it must NOT silently resolve to the earlier, superseded approval."""
        comments = [
            {
                "author": {"login": "codex"},
                "body": "Verdict: APPROVE\nReview-Lane: t:4\nReviewed-SHA: " + "a" * 40,
                "createdAt": "2026-08-15T10:00:00Z",
            },
            {
                "author": {"login": "codex"},
                "body": "Verdict: DISAPPROVE\nReview-Lane: t:4\nReviewed-SHA: " + "b" * 40,
                "createdAt": "2026-08-15T11:00:00Z",
            },
        ]
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=comments))
        result = source.verdict(repo=REPO, number=1)
        self.assertNotEqual(result["verdict"], "approved")
        self.assertEqual(result["verdict"], "unknown")
        self.assertIn("DISAPPROVE", result["detail"])

    def test_no_verdict_comment_at_all_still_reads_none_with_empty_detail(self):
        """The #184/#192 property this must not disturb: a PR nobody has
        reviewed yet reads `none` with an EMPTY detail, not a manufactured
        reason -- that distinction is what keeps digest.sh's report free of
        "independence unknown" noise on the single most common case."""
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=[{"author": {"login": "codex"}, "body": "LGTM"}]))
        result = source.verdict(repo=REPO, number=1)
        self.assertEqual(result, {"verdict": "none", "detail": ""})

    def test_scan_verdict_lines_pairs_each_line_with_its_raw_decision_text(self):
        body = (
            "Verdict: NOT APPROVED\nReview-Lane: t:4\nReviewed-SHA: " + "a" * 40
            + "\n\nVerdict: APPROVE\nReview-Lane: t:5\nReviewed-SHA: " + "b" * 40
        )
        scan = _scan_verdict_lines(body)
        self.assertEqual(scan, [(None, "NOT APPROVED"), ("approved", "APPROVE")])
