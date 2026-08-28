import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import (  # noqa: E402
    GithubReviewVerdictSource,
    _classify_decision_text,
    _scan_verdict_lines,
)

from tests.supervisor.test_verdict_helpers import (  # noqa: E402
    REPO,
    _comment_runner,
)


class Issue639RetractedVerdictTests(unittest.TestCase):
    """agent-supervisor#639, measured on skills#284: a lane posted `Verdict:
    APPROVE` on its own commit, then immediately posted a follow-up
    retracting it as self-review. The live comment was malformed (a branch
    name where `Review-Lane:` wanted a lane id, a 7-char `Reviewed-SHA:`),
    so it could not resolve regardless -- proving nothing about the WELL-
    FORMED path. These fixtures build that path directly: a complete,
    correctly-stamped APPROVE block, then a complete `Verdict: RETRACTED`
    block from the SAME `Review-Lane:`, and confirm the approval is not
    treated as operative -- while a genuine, unretracted verdict, or one
    retracted only by a DIFFERENT lane, still counts."""

    LANE = "t:4"
    SHA_A = "a" * 40
    SHA_B = "b" * 40

    def _approve(self, *, lane=LANE, sha=SHA_A, created_at="2026-08-25T07:00:00Z", author="jonhill90"):
        return {
            "author": {"login": author},
            "body": f"Verdict: APPROVE\nReview-Lane: {lane}\nReviewed-SHA: {sha}",
            "createdAt": created_at,
        }

    def _retract(self, *, lane=LANE, sha=SHA_A, created_at="2026-08-25T07:05:00Z", author="jonhill90"):
        return {
            "author": {"login": author},
            "body": f"Verdict: RETRACTED\nReview-Lane: {lane}\nReviewed-SHA: {sha}",
            "createdAt": created_at,
        }

    def test_classify_recognises_retracted(self):
        self.assertEqual(_classify_decision_text("RETRACTED"), "retracted")

    def test_a_retracted_block_is_operative_on_its_own_terms(self):
        """#595's own rule is not weakened for retraction: a bare `Verdict:
        RETRACTED` label with no `Review-Lane:`/`Reviewed-SHA:` following it
        is not a qualifying line at all."""
        self.assertEqual(_scan_verdict_lines("Verdict: RETRACTED"), [])
        self.assertEqual(
            _scan_verdict_lines(f"Verdict: RETRACTED\nReview-Lane: {self.LANE}\nReviewed-SHA: {self.SHA_A}"),
            [("retracted", "RETRACTED")],
        )

    def test_a_well_formed_retracted_approve_is_not_treated_as_operative(self):
        """The direction #639 says today's fix passes only by accident: a
        CORRECTLY stamped lane id and a full 40-hex SHA, retracted by a
        complete block from the same lane -- must not resolve `approved`.
        No review object and no other comment exist, so the PR must read
        `none`, not `unknown` and not `approved`."""
        comments = [self._approve(), self._retract()]
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=comments))
        result = source.verdict(repo=REPO, number=284)
        self.assertEqual(result["verdict"], "none")

    def test_mutation_without_retraction_handling_this_case_would_resolve_approved(self):
        """Proves the fixture above is real evidence: strip the RETRACTED
        handling back out (treat every qualifying comment as a plain
        decision, last one wins, same as pre-#639 code) and this exact
        scenario resolves `approved` -- the failure #639 was filed over."""
        comments = [self._approve(), self._retract()]
        scans = [(_scan_verdict_lines(c["body"])) for c in comments]
        # Pre-#639 code took the LAST comment with any qualifying line and
        # read its (only) decision text directly, with no notion of
        # "retracted" as anything but an unrecognised token.
        last_decisions = {d for d, _ in scans[-1] if d is not None}
        self.assertEqual(last_decisions, {"retracted"}, "the retraction comment must carry a recognised decision")

    def test_a_genuine_approve_with_no_retraction_still_counts(self):
        """#639's other bar: a fix loose enough that ANY later comment from
        the same lane voids an approval is its own hazard. A plain
        clarifying follow-up -- prose, no `Verdict:` block at all -- must
        not retract anything."""
        comments = [
            self._approve(),
            {
                "author": {"login": "jonhill90"},
                "body": "Just to be clear, this only touches the docs, not the parser logic.",
                "createdAt": "2026-08-25T07:05:00Z",
            },
        ]
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=comments))
        result = source.verdict(repo=REPO, number=284)
        self.assertEqual(result["verdict"], "approved")

    def test_retraction_from_a_different_lane_does_not_touch_this_lanes_approval(self):
        """Retraction reaches backward only within its OWN review lane -- a
        second reviewer's unrelated comment (even one carrying its own
        `Verdict: RETRACTED` for a different lane) must never void the
        first lane's genuine approval."""
        comments = [
            self._approve(lane="t:4"),
            self._retract(lane="t:5", sha=self.SHA_B, created_at="2026-08-25T07:05:00Z"),
        ]
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=comments))
        result = source.verdict(repo=REPO, number=284)
        self.assertEqual(result["verdict"], "approved")
        self.assertEqual(result["reviewer_lane"], "t:4")

    def test_a_fresh_re_review_after_retraction_from_the_same_lane_still_counts(self):
        """Retraction reaches backward only -- a genuine RE-REVIEW posted by
        the SAME lane after its own retraction is a fresh decision, not
        something the earlier retraction can reach forward and void."""
        comments = [
            self._approve(created_at="2026-08-25T07:00:00Z"),
            self._retract(created_at="2026-08-25T07:05:00Z"),
            self._approve(sha=self.SHA_B, created_at="2026-08-25T07:10:00Z"),
        ]
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=comments))
        result = source.verdict(repo=REPO, number=284)
        self.assertEqual(result["verdict"], "approved")

    def test_an_unattributable_retraction_refuses_rather_than_silently_dropping(self):
        """A retraction whose own `Review-Lane:` cannot be parsed can never
        be trusted to know what it is retracting -- silently ignoring it
        would let the very approval it meant to withdraw stand unresolved,
        the same 'fails safe only by accident' shape #639 was filed over.
        It must refuse (`unknown`), never resolve to `approved`."""
        comments = [
            self._approve(),
            self._retract(lane="not-a-lane-id", created_at="2026-08-25T07:05:00Z"),
        ]
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=comments))
        result = source.verdict(repo=REPO, number=284)
        self.assertEqual(result["verdict"], "unknown")
        self.assertIn("retract", result["detail"].lower())

    def test_the_live_284_shape_end_to_end_stays_refused(self):
        """The live case named in #639's issue: a malformed APPROVE (branch
        name where a lane id belongs, a 7-char SHA), followed by a prose
        retraction with no `Verdict:` block at all. Proves nothing about
        the well-formed path above, but must still refuse, not merge --
        this is the regression guard for the exact transcript in the
        issue."""
        comments = [
            {
                "author": {"login": "jonhill90"},
                "body": "Verdict: APPROVE\nReview-Lane: lane/266-fix284\nReviewed-SHA: 133626f",
                "createdAt": "2026-08-25T07:22:40Z",
            },
            {
                "author": {"login": "jonhill90"},
                "body": (
                    "Retracting the `Verdict: APPROVE` line I posted above. I authored the "
                    "content change in this same comment's commit (`133626f`) -- issuing an "
                    "approval on my own edit is self-review, regardless of the `Review-Lane:` "
                    "label attached to it."
                ),
                "createdAt": "2026-08-25T07:25:00Z",
            },
        ]
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=comments))
        result = source.verdict(repo=REPO, number=284)
        self.assertNotEqual(result["verdict"], "approved")
