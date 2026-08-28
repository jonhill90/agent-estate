import json
import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import GithubReviewVerdictSource  # noqa: E402

from tests.supervisor.test_verdict_helpers import (  # noqa: E402
    REPO,
    _api_runner,
    _comment_runner,
    _patch,
)


class GithubCommentVerdictTests(unittest.TestCase):
    """agent-supervisor#53: the codex lane posts verdicts with `gh pr
    comment`, not `gh pr review`, so `digest.sh` reported `verdict=none` for
    a PR with a full REQUEST CHANGES posted 14 minutes earlier. These are
    the "Red first" cases from the issue/brief, each named after the guard
    it exercises."""

    def test_verdict_comment_alone_is_reported_not_none(self):
        """Guard 1: a PR whose only verdict is a `**Verdict:` comment must
        be reported, not read as `none`. agent-supervisor#595: a
        `Review-Lane:`/`Reviewed-SHA:` pair is appended immediately after the
        label line so this stays an operative comment under the new
        complete-block requirement."""
        comments = [{
            "author": {"login": "codex"},
            "body": "**Verdict: REQUEST CHANGES**  head SHA abc123\nReview-Lane: t:4\nReviewed-SHA: " + "a" * 40,
            "createdAt": "2026-08-13T18:03:05Z",
        }]
        source = GithubReviewVerdictSource(
            runner=_comment_runner(comments=comments, author={"login": "jonhill90"})
        )
        result = source.verdict(repo=REPO, number=51)
        self.assertEqual(result["verdict"], "rejected")
        self.assertNotEqual(result["verdict"], "none")

    def test_a_review_object_is_unaffected_by_comment_logic(self):
        """Guard 2: a PR with a decisive review object behaves exactly as
        before -- comments are never even consulted when the review side
        already has a decisive answer."""
        source = GithubReviewVerdictSource(
            runner=_comment_runner(
                reviews=[{"state": "APPROVED"}],
                comments=[{"author": {"login": "codex"}, "body": "**Verdict: REQUEST CHANGES**"}],
            )
        )
        result = source.verdict(repo=REPO, number=1)
        self.assertEqual(result["verdict"], "approved")

    def test_neither_review_nor_comment_reads_none(self):
        """Guard 3: a detector that finds a verdict everywhere is as
        useless as one that finds none -- absence of both must still read
        `none`."""
        source = GithubReviewVerdictSource(runner=_comment_runner())
        result = source.verdict(repo=REPO, number=1)
        self.assertEqual(result["verdict"], "none")

    def test_verdict_mentioned_mid_body_is_not_counted(self):
        """Guard 4: a comment merely mentioning the word "verdict" mid-body
        must NOT be counted -- anchored on the `**Verdict:` prefix, not a
        substring search."""
        comments = [
            {
                "author": {"login": "codex"},
                "body": "I think the verdict here should be REQUEST CHANGES, but I won't file one yet.",
            }
        ]
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=comments))
        result = source.verdict(repo=REPO, number=1)
        self.assertEqual(result["verdict"], "none")

    def test_verdict_comment_exposes_a_review_lane_stamp(self):
        comments = [{
            "author": {"login": "jonhill90"},
            "body": "**Verdict: APPROVE**\nReview-Lane: t:4\nReviewed-SHA: " + "a" * 40,
        }]
        source = GithubReviewVerdictSource(
            runner=_comment_runner(comments=comments, author={"login": "jonhill90"})
        )
        result = source.verdict(repo=REPO, number=1)
        self.assertEqual(result["verdict"], "approved")
        self.assertEqual(result["reviewer_lane"], "t:4")
        self.assertEqual(result["verdict_kind"], "comment")

    def test_unstamped_verdict_comment_does_not_guess_independence_from_login(self):
        """This case's own name and shape changed under agent-supervisor#595
        -- it used to prove a `Verdict:` comment with NO `Review-Lane:` at
        all still resolved to `approved` (just without a `reviewer_lane`
        field), i.e. that the source never guessed independence from the
        comment author's GitHub login. Per #595's decision, a bare
        `Verdict:` label with nothing genuine following it (no trailer at
        all) is no longer operative -- it is exactly one of the poisoning
        shapes #595 closes -- so this now correctly resolves to `none`, not
        `approved`. The property this test still protects (never inferring
        independence from `author.login`) is now proven by the ABSENCE of
        a decisive verdict rather than by the absence of a `reviewer_lane`
        field on a decisive one."""
        comments = [{"author": {"login": "codex"}, "body": "**Verdict: APPROVE**"}]
        source = GithubReviewVerdictSource(
            runner=_comment_runner(comments=comments, author={"login": "jonhill90"})
        )
        result = source.verdict(repo=REPO, number=1)
        self.assertEqual(result["verdict"], "none")
        self.assertNotIn("reviewer_lane", result)
        self.assertNotIn("independent", result.get("detail", ""))

    def test_the_most_recent_matching_comment_wins(self):
        comments = [
            {
                "author": {"login": "codex"},
                "body": "**Verdict: REQUEST CHANGES**\nReview-Lane: t:4\nReviewed-SHA: " + "a" * 40,
                "createdAt": "2026-08-13T10:00:00Z",
            },
            {
                "author": {"login": "codex"},
                "body": "**Verdict: APPROVE**\nReview-Lane: t:4\nReviewed-SHA: " + "b" * 40,
                "createdAt": "2026-08-13T18:00:00Z",
            },
        ]
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=comments))
        result = source.verdict(repo=REPO, number=1)
        self.assertEqual(result["verdict"], "approved")

    def test_a_stale_review_falls_back_to_a_comment_verdict(self):
        """A review filed against a superseded head has nothing decisive to
        say (#218) -- a fresh `**Verdict:` comment must still be found
        rather than the PR reading `unknown` forever. The comment carries a
        `Reviewed-SHA:` trailer matching `head_sha` (#213) so this test
        keeps exercising ITS OWN guard -- the review-to-comment fallback --
        without also tripping the freshness guard #213 added to the comment
        path itself; that guard has its own tests below. agent-supervisor
        #595: a `Review-Lane:` line is now also required, immediately
        between `Verdict:` and `Reviewed-SHA:`, for the block to be
        operative at all."""
        old_sha = "a" * 40
        head_sha = "b" * 40
        reviews = [{"state": "APPROVED", "commit": {"oid": old_sha}}]
        comments = [{
            "author": {"login": "codex"},
            "body": f"**Verdict: REQUEST CHANGES**\nReview-Lane: t:4\nReviewed-SHA: {head_sha}",
        }]
        source = GithubReviewVerdictSource(
            runner=_comment_runner(reviews=reviews, comments=comments), patch_id=lambda diff: None
        )
        result = source.verdict(repo=REPO, number=1, head_sha=head_sha)
        self.assertEqual(result["verdict"], "rejected")

    def test_regression_213_stale_comment_approval_survives_a_later_push_reads_unknown_not_approved(self):
        """agent-supervisor#213's measured shape: PRs #204 and #207 both
        merged with an APPROVE **comment** (the codex lane's own posting
        path -- #53) written minutes BEFORE the commit that actually got
        merged. `_review_verdict` already compared `head_sha` (#218); the
        comment path never did, so `ci_gate.py`'s reason ("all checks
        green at <sha>") was the only thing anyone read, and it is true
        while answering a question nobody asked. No `Reviewed-SHA:`
        trailer here -- this is the timestamp backstop's job: a commit
        landed newer than the verdict, so it must refuse and name both.

        agent-supervisor#595: a comment with NO `Reviewed-SHA:` trailer at
        all is no longer operative through `GithubReviewVerdictSource
        .verdict()` -- `_scan_verdict_lines` requires the complete
        three-line block for a `Verdict:` line to be found at all, so this
        body (label + `Review-Lane:` only) now resolves `none` end-to-end,
        never reaching `_comment_freshness`'s timestamp backstop. That
        mechanism is still real, correct code (see its own docstring in
        `verdict.py` for why it is kept), so this test now exercises it
        DIRECTLY, the same way `BareDecisionLineTests` exercises
        `_bare_decision_line` directly after its own end-to-end wiring was
        retired."""
        head_sha = "c" * 40
        source = GithubReviewVerdictSource(runner=lambda cmd: (_ for _ in ()).throw(RuntimeError("no gh call expected")))
        fresh, note, refusal = source._comment_freshness(
            body="**Verdict: APPROVE**\nReview-Lane: t:4",
            created_at="2026-08-15T22:48:01Z",
            head_sha=head_sha,
            commits=[{"oid": head_sha, "committedDate": "2026-08-15T22:56:42Z"}],
            repo=REPO,
            number=204,
        )
        self.assertFalse(fresh)
        self.assertIn(head_sha, refusal)

    def test_mutation_213_a_freshness_check_that_always_passes_must_turn_this_red(self):
        """The bar #213 sets for the comment path, mirroring #218's own
        mutation test for the review-object path: the same APPROVE
        comment, at the same `head_sha`, must answer differently depending
        only on whether a newer commit exists. A freshness check that
        always agrees (or is never consulted) collapses these to the same
        verdict and this test goes red.

        agent-supervisor#595: exercised directly against `_comment_freshness`
        now, for the same reason the test above is -- an untrailered
        comment is no longer reachable through the end-to-end `.verdict()`
        path at all."""
        head_sha = "c" * 40
        source = GithubReviewVerdictSource(runner=lambda cmd: (_ for _ in ()).throw(RuntimeError("no gh call expected")))
        stale, _, _ = source._comment_freshness(
            body="**Verdict: APPROVE**\nReview-Lane: t:4",
            created_at="2026-08-15T22:48:01Z",
            head_sha=head_sha,
            commits=[{"oid": head_sha, "committedDate": "2026-08-15T22:56:42Z"}],
            repo=REPO,
            number=1,
        )
        fresh, _, _ = source._comment_freshness(
            body="**Verdict: APPROVE**\nReview-Lane: t:4",
            created_at="2026-08-15T22:48:01Z",
            head_sha=head_sha,
            commits=[{"oid": head_sha, "committedDate": "2026-08-15T20:00:00Z"}],
            repo=REPO,
            number=1,
        )
        self.assertNotEqual(stale, fresh)

    def test_regression_213_reviewed_sha_trailer_matching_head_wins_over_timestamp_backstop(self):
        """The honest mechanism (#213 proposal 1): a reviewer who states the
        SHA their verdict covers is believed on IDENTITY, not timing -- a
        commit landing after the comment does not matter once the trailer
        names the current head."""
        head_sha = "d" * 40
        comments = [
            {
                "author": {"login": "codex"},
                "body": f"**Verdict: APPROVE**\nReview-Lane: t:4\nReviewed-SHA: {head_sha}",
                "createdAt": "2026-08-15T00:00:00Z",
            }
        ]
        commits = [{"oid": head_sha, "committedDate": "2026-08-16T00:00:00Z"}]
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=comments, commits=commits))
        result = source.verdict(repo=REPO, number=1, head_sha=head_sha)
        self.assertEqual(result["verdict"], "approved")

    def test_regression_213_reviewed_sha_trailer_mismatch_refuses_naming_both_shas(self):
        """Fail closed (#213's explicit requirement): a `Reviewed-SHA:` that
        does not match `head_sha`, and whose content cannot be confirmed
        unchanged (no base-branch comparison available to this stub),
        refuses -- and the refusal names BOTH the SHA the verdict covered
        and the SHA being merged, not just "all checks green"."""
        old_sha = "e" * 40
        head_sha = "f" * 40
        comments = [
            {
                "author": {"login": "codex"},
                "body": f"**Verdict: APPROVE**\nReview-Lane: t:4\nReviewed-SHA: {old_sha}",
                "createdAt": "2026-08-15T00:00:00Z",
            }
        ]
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=comments))
        result = source.verdict(repo=REPO, number=1, head_sha=head_sha)
        self.assertEqual(result["verdict"], "unknown")
        self.assertIn(old_sha, result["detail"])
        self.assertIn(head_sha, result["detail"])

    def test_regression_213_reviewed_sha_trailer_mismatch_but_content_unchanged_since_rebase_promotes(self):
        """A `Reviewed-SHA:` trailer gets the same rebase tolerance #226 gave
        the review-object path: identity is about CONTENT, and a pure
        rebase changes every SHA on a branch without changing what was
        reviewed."""
        old_sha, head_sha = "a" * 40, "c" * 40
        payload = json.dumps(
            {
                "reviews": [],
                "comments": [
                    {
                        "author": {"login": "codex"},
                        "body": f"**Verdict: APPROVE**\nReview-Lane: t:4\nReviewed-SHA: {old_sha}",
                        "createdAt": "2026-08-15T00:00:00Z",
                    }
                ],
                "commits": [{"oid": head_sha, "committedDate": "2026-08-16T00:00:00Z"}],
            }
        )
        runner = _api_runner(
            reviews=payload,
            branches={old_sha: ["old1", old_sha], head_sha: ["new1", head_sha]},
            patches={
                "old1": _patch("first", offset=10),
                old_sha: _patch("second", offset=10),
                "new1": _patch("first", offset=42),
                head_sha: _patch("second", offset=77),
            },
        )
        source = GithubReviewVerdictSource(runner=runner)
        result = source.verdict(repo=REPO, number=1, head_sha=head_sha)
        self.assertEqual(result["verdict"], "approved")
        self.assertIn(old_sha, result["detail"])
        self.assertIn(head_sha, result["detail"])

    def test_regression_213_no_trailer_but_no_commit_newer_than_the_verdict_stays_approved(self):
        """The timestamp backstop (#213 proposal 2) for a verdict comment
        posted before this fix existed and therefore carries no
        `Reviewed-SHA:` trailer: when nothing on the branch is newer than
        the verdict, it still stands.

        agent-supervisor#595: exercised directly against `_comment_freshness`
        -- an untrailered comment cannot reach this mechanism through the
        end-to-end `.verdict()` path anymore (see the two tests above)."""
        head_sha = "d" * 40
        source = GithubReviewVerdictSource(runner=lambda cmd: (_ for _ in ()).throw(RuntimeError("no gh call expected")))
        fresh, note, refusal = source._comment_freshness(
            body="**Verdict: APPROVE**\nReview-Lane: t:4",
            created_at="2026-08-15T23:00:00Z",
            head_sha=head_sha,
            commits=[{"oid": head_sha, "committedDate": "2026-08-15T22:00:00Z"}],
            repo=REPO,
            number=1,
        )
        self.assertTrue(fresh)
        self.assertEqual(refusal, "")

    def test_regression_213_no_head_sha_given_preserves_pre_213_behaviour(self):
        """A caller with no head to check against skips the freshness guard
        entirely, same as the review-object side (#218). agent-supervisor
        #595: `Reviewed-SHA:` is added so this stays operative at all under
        the new complete-block requirement -- unaffected by which freshness
        branch would fire, since `head_sha=None` skips the guard entirely."""
        comments = [{
            "author": {"login": "codex"},
            "body": "**Verdict: APPROVE**\nReview-Lane: t:4\nReviewed-SHA: " + "a" * 40,
        }]
        result = GithubReviewVerdictSource(runner=_comment_runner(comments=comments)).verdict(repo=REPO, number=1)
        self.assertEqual(result["verdict"], "approved")
