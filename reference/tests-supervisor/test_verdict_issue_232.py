import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import GithubReviewVerdictSource  # noqa: E402

from tests.supervisor.test_verdict_helpers import (  # noqa: E402
    REPO,
    _comment_runner,
)


class Issue232Tests(unittest.TestCase):
    """agent-supervisor#232, measured on skills#193 (2026-08-16T06:15Z): a
    correct APPROVE was refused for two independent trailer defects at once.
    The verdict body and both SHAs below are quoted from the issue verbatim
    -- driven through the REAL `GithubReviewVerdictSource.verdict()`, never a
    reimplementation of the parsing.

    agent-supervisor#595's ADJACENCY FINDING, surfaced here rather than
    quietly worked around: the real, verbatim #232 body has a BLANK LINE
    between `**Verdict: APPROVE**` and `Review-Lane:` -- and #595's decision
    is explicit that the three trailer lines must be consecutive with
    NOTHING between them, not even a blank line. Measured directly below
    (`test_the_real_232_body_no_longer_resolves_under_the_stricter_595_rule`):
    the real #232 fixture, unmodified, now resolves `none` instead of
    `approved`. This is a genuine, real consequence of #595's decision, not
    a bug in this fix -- #595's own text weighs this tradeoff explicitly
    ("three consecutive lines, nothing between them") against the risk a
    looser rule reopens (a comment illustrating someone else's trailer for
    reference would itself parse as operative). `MEASURED_BODY_ADJACENT`
    below is the SAME real content with only that one blank line closed up,
    used by the rest of this class so #232's own guards (the trailing-
    parenthetical lane parse, the truncated-vs-malformed SHA distinction)
    stay covered by a fixture that is still operative under the new rule."""

    REAL_HEAD = "e7b7ac103e66f3a9f1d54998c3203dba2e54ab42"
    TRUNCATED_SHA = "e7b7ac103e66f3a9f1d54998c3203dba2e54ab4"  # one char short
    MEASURED_BODY = (
        "**Verdict: APPROVE**\n\n"
        "Review-Lane: skills:2 (task `skills190-rev193`, worktree "
        "`/var/.../ad-190-rev193-94816`, confirmed against the ledger "
        "directly — not `tmux display-message`, and not `cli.py "
        "worktree-lane`, which refuses to answer for a review task per "
        "agent-supervisor#76/#212)\n"
        f"Reviewed-SHA: {TRUNCATED_SHA}"
    )
    MEASURED_BODY_ADJACENT = (
        "**Verdict: APPROVE**\n"
        "Review-Lane: skills:2 (task `skills190-rev193`, worktree "
        "`/var/.../ad-190-rev193-94816`, confirmed against the ledger "
        "directly — not `tmux display-message`, and not `cli.py "
        "worktree-lane`, which refuses to answer for a review task per "
        "agent-supervisor#76/#212)\n"
        f"Reviewed-SHA: {TRUNCATED_SHA}"
    )

    def test_the_real_232_body_no_longer_resolves_under_the_stricter_595_rule(self):
        """agent-supervisor#595's own accepted tradeoff, measured against the
        exact real #232 fixture rather than asserted in prose: a blank line
        between `Verdict:` and `Review-Lane:` -- present in the real,
        unedited #232 body -- now means the block is not adjacent, so
        nothing in this comment is operative at all."""
        comments = [{"author": {"login": "jonhill90"}, "body": self.MEASURED_BODY}]
        result = GithubReviewVerdictSource(runner=_comment_runner(comments=comments)).verdict(repo=REPO, number=193)
        self.assertEqual(result["verdict"], "none")

    def test_review_lane_with_trailing_parenthetical_now_parses(self):
        """Guard 1: the lane id is extracted despite the trailing prose --
        this is the parse the old whole-line-anchored regex could not make;
        it must now succeed even before the SHA is considered at all (no
        `head_sha` given, so the freshness guard is skipped)."""
        comments = [{"author": {"login": "jonhill90"}, "body": self.MEASURED_BODY_ADJACENT}]
        result = GithubReviewVerdictSource(runner=_comment_runner(comments=comments)).verdict(repo=REPO, number=193)
        self.assertEqual(result["verdict"], "approved")
        self.assertEqual(result["reviewer_lane"], "skills:2")

    def test_truncated_reviewed_sha_refuses_as_malformed_not_a_mismatch(self):
        """Guard 2: #218's SHA check finally gets to run once guard 1 no
        longer refuses first -- and a 39-character trailer against the real
        40-character head must be named MALFORMED, not "does not match".
        This is real hex, and the truncated value is an actual prefix of
        `REAL_HEAD` -- proof the old mismatch-only path could not be trusted
        to catch this even if it ran, since a git ref lookup would happily
        resolve a valid prefix to the very commit being compared against."""
        comments = [{"author": {"login": "jonhill90"}, "body": self.MEASURED_BODY_ADJACENT}]
        result = GithubReviewVerdictSource(runner=_comment_runner(comments=comments)).verdict(
            repo=REPO, number=193, head_sha=self.REAL_HEAD
        )
        self.assertEqual(result["verdict"], "unknown")
        self.assertNotEqual(result["verdict"], "approved")
        self.assertIn("malformed", result["detail"])
        self.assertIn("39 chars", result["detail"])
        self.assertNotIn("does not match", result["detail"])

    def test_a_correctly_shaped_reviewed_sha_matching_head_is_accepted(self):
        """Confirms guard 2 refuses the TRUNCATION specifically, not the SHA
        trailer mechanism in general -- fix only the missing character and
        the same fixture is accepted."""
        body = self.MEASURED_BODY_ADJACENT.replace(self.TRUNCATED_SHA, self.REAL_HEAD)
        comments = [{"author": {"login": "jonhill90"}, "body": body}]
        result = GithubReviewVerdictSource(runner=_comment_runner(comments=comments)).verdict(
            repo=REPO, number=193, head_sha=self.REAL_HEAD
        )
        self.assertEqual(result["verdict"], "approved")
        self.assertEqual(result["reviewer_lane"], "skills:2")

    def test_both_trailer_defects_at_once_are_reported_in_one_pass(self):
        """"Report every trailer problem found in one pass, not just the
        first" -- a comment with BOTH an unparseable Review-Lane line (no
        lane-shaped token anywhere on it) and a malformed Reviewed-SHA must
        name both in the one refusal, not just whichever is checked first.
        No blank line here (agent-supervisor#595) -- the block must be
        adjacent for either trailer defect to even be considered."""
        body = (
            "**Verdict: APPROVE**\n"
            "Review-Lane: not-a-lane-id-at-all\n"
            f"Reviewed-SHA: {self.TRUNCATED_SHA}"
        )
        comments = [{"author": {"login": "jonhill90"}, "body": body}]
        result = GithubReviewVerdictSource(runner=_comment_runner(comments=comments)).verdict(
            repo=REPO, number=193, head_sha=self.REAL_HEAD
        )
        self.assertEqual(result["verdict"], "unknown")
        self.assertIn("could not parse lane id", result["detail"])
        self.assertIn("not-a-lane-id-at-all", result["detail"])
        self.assertIn("malformed", result["detail"])
        self.assertIn("39 chars", result["detail"])

    def test_a_genuinely_unparseable_review_lane_line_is_quoted_in_the_refusal(self):
        """"Print the line it could not parse, not just the requirement" --
        a `Review-Lane:` line present but carrying no lane-shaped token at
        all must name the actual line text in the refusal. agent-supervisor
        #595: a `Reviewed-SHA:` line is appended so the block is complete
        enough to be operative at all -- without it this comment would now
        resolve `none`, not `unknown`, for an unrelated reason (block
        incompleteness, not the unparseable lane this test is about)."""
        comments = [{
            "author": {"login": "jonhill90"},
            "body": "**Verdict: APPROVE**\nReview-Lane: nonsense with no lane token\nReviewed-SHA: " + "a" * 40,
        }]
        result = GithubReviewVerdictSource(runner=_comment_runner(comments=comments)).verdict(repo=REPO, number=1)
        self.assertEqual(result["verdict"], "unknown")
        self.assertIn("nonsense with no lane token", result["detail"])
