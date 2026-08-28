import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402

from verdict import GithubReviewVerdictSource  # noqa: E402

from tests.supervisor.test_verdict_helpers import (  # noqa: E402
    REPO,
    _comment_runner,
)


class Issue276ClaudePrintReviewLaneTests(unittest.TestCase):
    """agent-supervisor#292/#276, measured on PR #277: `_LANE_SHAPE_RE` only
    recognises a tmux lane id (`<session>:<index>`) -- a claude-print/pi-rpc
    lane's id is its task id, a free-text shape with no colon at all, so a
    genuinely correct `Review-Lane: ad182-review-186` trailer was refused as
    unparseable, indistinguishable from hand-typed nonsense. Driven through
    the REAL `GithubReviewVerdictSource.verdict()` with a real (tempdir)
    ledger, never a reimplementation of `_parse_review_lane`."""

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.ledger = Ledger(self._tmp.name)

    def test_a_registered_claude_print_lane_id_now_parses(self):
        self.ledger.register_lane(
            lane="ad182-review-186", pane_id="claude-print:ad182-review-186", nonce="nonce-review",
            harness="claude", repo="/tmp/repo", server_id="srv", session_id="sess", command="claude",
            transport="claude-print",
        )
        comments = [{
            "author": {"login": "jonhill90"},
            # agent-supervisor#595: no blank line between the label and its
            # trailer pair -- the three lines must be strictly consecutive.
            "body": "**Verdict: APPROVE**\nReview-Lane: ad182-review-186\nReviewed-SHA: sha-21",
        }]
        result = GithubReviewVerdictSource(
            runner=_comment_runner(comments=comments), ledger=self.ledger
        ).verdict(repo=REPO, number=49)
        self.assertEqual(result["verdict"], "approved")
        self.assertEqual(result["reviewer_lane"], "ad182-review-186")

    def test_an_unregistered_token_of_the_same_shape_still_refuses(self):
        """The ledger check is what tells `ad182-review-186` apart from
        hand-typed nonsense of the identical shape -- an unregistered token
        must still refuse, or this widening would just be a guess wearing a
        ledger lookup. agent-supervisor#595: a `Reviewed-SHA:` line is
        appended so the block is complete enough to be operative at all."""
        comments = [{
            "author": {"login": "jonhill90"},
            "body": "**Verdict: APPROVE**\nReview-Lane: not-a-lane-id-at-all\nReviewed-SHA: " + "a" * 40,
        }]
        result = GithubReviewVerdictSource(
            runner=_comment_runner(comments=comments), ledger=self.ledger
        ).verdict(repo=REPO, number=1)
        self.assertEqual(result["verdict"], "unknown")
        self.assertIn("could not parse lane id", result["detail"])

    def test_no_ledger_given_falls_back_to_the_prior_unparseable_behaviour(self):
        """`ledger=None` (the default) must not change behaviour for every
        caller that never had one to offer -- the exact fixture from the
        #292 reproduction, run with no ledger at all. agent-supervisor#595:
        a `Reviewed-SHA:` line is appended so the block is complete enough
        to be operative at all."""
        comments = [{
            "author": {"login": "jonhill90"},
            "body": "**Verdict: APPROVE**\nReview-Lane: ad182-review-186\nReviewed-SHA: " + "a" * 40,
        }]
        result = GithubReviewVerdictSource(runner=_comment_runner(comments=comments)).verdict(repo=REPO, number=1)
        self.assertEqual(result["verdict"], "unknown")
        self.assertIn("could not parse lane id", result["detail"])
