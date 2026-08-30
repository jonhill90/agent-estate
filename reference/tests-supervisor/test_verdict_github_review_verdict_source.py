import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import GithubReviewVerdictSource  # noqa: E402

from tests.supervisor.test_verdict_helpers import (  # noqa: E402
    BASE_REF,
    REPO,
    _api_runner,
    _patch,
)


def _reviews_runner(payload):
    """A `runner` that answers `gh pr view --json reviews` with `payload` and
    raises for anything else (in particular the `gh api .../compare/...`
    calls #226 added) -- so a test that does not opt into the rebase-aware
    comparison gets the same "cannot compute -> stays unknown" fail-closed
    answer #218 already established, instead of silently matching on
    whatever string this stub happens to return."""

    def runner(cmd):
        if cmd[:3] == ["gh", "pr", "view"]:
            return payload
        raise RuntimeError(f"no stub for command: {cmd!r}")

    return runner


class GithubReviewVerdictSourceTests(unittest.TestCase):
    def test_no_reviews_reads_none(self):
        source = GithubReviewVerdictSource(runner=lambda cmd: '{"reviews":[]}')
        self.assertEqual(source.verdict(repo=REPO, number=1), {"verdict": "none", "detail": ""})

    def test_changes_requested_reads_rejected(self):
        source = GithubReviewVerdictSource(
            runner=lambda cmd: '{"reviews":[{"state":"CHANGES_REQUESTED"}]}'
        )
        result = source.verdict(repo=REPO, number=1)
        self.assertEqual(result["verdict"], "rejected")

    def test_approved_reads_approved(self):
        source = GithubReviewVerdictSource(runner=lambda cmd: '{"reviews":[{"state":"APPROVED"}]}')
        result = source.verdict(repo=REPO, number=1)
        self.assertEqual(result["verdict"], "approved")

    def test_rejection_wins_over_approval(self):
        source = GithubReviewVerdictSource(
            runner=lambda cmd: '{"reviews":[{"state":"APPROVED"},{"state":"CHANGES_REQUESTED"}]}'
        )
        self.assertEqual(source.verdict(repo=REPO, number=1)["verdict"], "rejected")

    def test_regression_comment_prose_is_never_consulted(self):
        """agent-dotfiles#203: the field this replaces regex-matched the last
        comment's prose and read "I cannot approve this, it is unsafe." as an
        APPROVE. This source only ever looks at each review's `state` field --
        a body containing "approve" on a COMMENTED (non-deciding) review must
        not flip the verdict."""
        payload = (
            '{"reviews":['
            '{"state":"COMMENTED","body":"I cannot approve this, it is unsafe."},'
            '{"state":"COMMENTED","body":"Verdict: APPROVE"}'
            "]}"
        )
        source = GithubReviewVerdictSource(runner=lambda cmd: payload)
        self.assertEqual(source.verdict(repo=REPO, number=1)["verdict"], "none")

    def test_regression_request_changes_prose_on_an_approved_pr(self):
        """agent-dotfiles#214: the 2026-08-12 misreport. A reviewer's comment
        on PR #206 described the very bug this module fixes, using the
        literal string "REQUEST CHANGES" inside a sentence about the bug --
        not as a review decision. The old prose-regex read that string and
        reported the PR as REQUEST CHANGES even though its real GitHub
        review state was APPROVE. This source must read only the `state`
        field and report "approved", never let that substring flip it."""
        payload = (
            '{"reviews":['
            '{"state":"COMMENTED","body":'
            '"digest.sh misread this because the comment contained the '
            'literal string REQUEST CHANGES inside a sentence describing '
            'the bug."},'
            '{"state":"APPROVED","body":"Looks good."}'
            "]}"
        )
        source = GithubReviewVerdictSource(runner=lambda cmd: payload)
        self.assertEqual(source.verdict(repo=REPO, number=206)["verdict"], "approved")

    def test_approved_at_current_head_reads_approved(self):
        source = GithubReviewVerdictSource(
            runner=lambda cmd: '{"reviews":[{"state":"APPROVED","commit":{"oid":"' + "a" * 40 + '"}}]}'
        )
        result = source.verdict(repo=REPO, number=1, head_sha="a" * 40)
        self.assertEqual(result["verdict"], "approved")

    def test_regression_218_review_filed_against_a_superseded_head_reads_unknown_not_approved(self):
        """agent-dotfiles#218: a review APPROVED at SHA A, followed by a push
        moving the head to SHA B, must not still answer for B. Reporting
        "approved" here is the same failure shape as the pre-#206
        prose-regex bug -- the field says something about a PR that nobody
        actually reviewed at its current state -- arriving by a different
        route (a stale SHA instead of misread prose)."""
        source = GithubReviewVerdictSource(
            runner=_reviews_runner('{"reviews":[{"state":"APPROVED","commit":{"oid":"' + "a" * 40 + '"}}]}')
        )
        result = source.verdict(repo=REPO, number=1, head_sha="b" * 40)
        self.assertEqual(result["verdict"], "unknown")
        self.assertNotEqual(result["verdict"], "none", "a review WAS filed -- 'none' would misreport an absence")

    def test_regression_218_rejection_filed_against_a_superseded_head_reads_unknown_not_rejected(self):
        source = GithubReviewVerdictSource(
            runner=_reviews_runner('{"reviews":[{"state":"CHANGES_REQUESTED","commit":{"oid":"' + "a" * 40 + '"}}]}')
        )
        result = source.verdict(repo=REPO, number=1, head_sha="b" * 40)
        self.assertEqual(result["verdict"], "unknown")

    def test_mutation_218_a_sha_comparison_that_always_passes_must_turn_this_red(self):
        """The bar #218 sets: mutate the SHA check to always agree and watch
        the suite go red. This test IS that mutation-detector -- it only
        passes while `verdict()` actually compares `commit.oid` against
        `head_sha` rather than ignoring `head_sha` (or always matching)."""
        source = GithubReviewVerdictSource(
            runner=_reviews_runner('{"reviews":[{"state":"APPROVED","commit":{"oid":"' + "a" * 40 + '"}}]}')
        )
        stale = source.verdict(repo=REPO, number=1, head_sha="b" * 40)
        current = source.verdict(repo=REPO, number=1, head_sha="a" * 40)
        self.assertNotEqual(stale["verdict"], current["verdict"])

    def test_no_head_sha_given_preserves_pre_218_behaviour(self):
        """A caller with no head to check against (head_sha=None, the
        default) gets the pre-#218 answer -- unable to tell current from
        stale, same as before this module knew about SHAs at all."""
        source = GithubReviewVerdictSource(
            runner=lambda cmd: '{"reviews":[{"state":"APPROVED","commit":{"oid":"' + "a" * 40 + '"}}]}'
        )
        self.assertEqual(source.verdict(repo=REPO, number=1)["verdict"], "approved")

    def test_runner_failure_fails_closed_to_unknown(self):
        def raiser(cmd):
            raise RuntimeError("gh: command failed")

        source = GithubReviewVerdictSource(runner=raiser)
        self.assertEqual(source.verdict(repo=REPO, number=1)["verdict"], "unknown")

    def test_malformed_json_fails_closed_to_unknown(self):
        source = GithubReviewVerdictSource(runner=lambda cmd: "not json")
        self.assertEqual(source.verdict(repo=REPO, number=1)["verdict"], "unknown")

    def test_reviews_not_a_list_fails_closed_to_unknown(self):
        source = GithubReviewVerdictSource(runner=lambda cmd: '{"reviews": "oops"}')
        self.assertEqual(source.verdict(repo=REPO, number=1)["verdict"], "unknown")

    def test_226_pure_rebase_promotes_the_stale_review_and_names_the_basis(self):
        """agent-dotfiles#226: a rebase changes every SHA on the branch even
        when it changes no content. The stale review must be reported
        current, with the basis named -- not silently ("unknown" would be
        the pre-#226 answer; approving with no annotation would hide how we
        know)."""
        old_sha, head_sha = "a" * 40, "c" * 40
        reviews = '{"reviews":[{"state":"APPROVED","commit":{"oid":"' + old_sha + '"}}]}'
        # Both heads carry the same two commits; the rebase moved every SHA
        # and shifted both patches to new hunk offsets. `main` also gained
        # commits in between -- they belong to neither branch list, which is
        # exactly what anchoring on the base branch buys.
        runner = _api_runner(
            reviews=reviews,
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
        self.assertIn("patch-id", result["detail"])

    def test_226_a_real_content_change_since_review_stays_unknown(self):
        """The direction that must not regress (#219/#218): a head move that
        actually changed the reviewed content still reads unknown, exactly
        as before #226. Here the rebase also AMENDED the second commit."""
        old_sha, head_sha = "a" * 40, "c" * 40
        reviews = '{"reviews":[{"state":"APPROVED","commit":{"oid":"' + old_sha + '"}}]}'
        runner = _api_runner(
            reviews=reviews,
            branches={old_sha: ["old1", old_sha], head_sha: ["new1", head_sha]},
            patches={
                "old1": _patch("first", offset=10),
                old_sha: _patch("second", offset=10),
                "new1": _patch("first", offset=42),
                head_sha: _patch("second", offset=77, replacement="a genuinely different line"),
            },
        )
        source = GithubReviewVerdictSource(runner=runner)
        result = source.verdict(repo=REPO, number=1, head_sha=head_sha)
        self.assertEqual(result["verdict"], "unknown")

    def test_229_a_commit_added_after_the_review_stays_unknown(self):
        """The other way unreviewed content arrives: not an amend, an extra
        commit pushed on top. The reviewed patches are all still there --
        a subset test in the WRONG direction would pass this."""
        old_sha, head_sha = "a" * 40, "c" * 40
        reviews = '{"reviews":[{"state":"APPROVED","commit":{"oid":"' + old_sha + '"}}]}'
        runner = _api_runner(
            reviews=reviews,
            branches={old_sha: [old_sha], head_sha: [old_sha, head_sha]},
            patches={old_sha: _patch("first"), head_sha: _patch("added")},
        )
        source = GithubReviewVerdictSource(runner=runner)
        self.assertEqual(source.verdict(repo=REPO, number=1, head_sha=head_sha)["verdict"], "unknown")

    def test_229_the_compare_is_anchored_on_the_pr_base_branch(self):
        """agent-dotfiles#229's blocking finding, asserted directly on the
        commands issued: `merge-base(old, new)` is symmetric, so comparing
        the two heads against EACH OTHER resolves both sides to the
        pre-rebase base and makes the "new" side carry everything `main`
        gained. Every compare must name the base branch."""
        old_sha, head_sha = "a" * 40, "c" * 40
        reviews = '{"reviews":[{"state":"APPROVED","commit":{"oid":"' + old_sha + '"}}]}'
        seen = []
        runner = _api_runner(
            reviews=reviews,
            branches={old_sha: [old_sha], head_sha: [head_sha]},
            patches={old_sha: _patch("only", offset=10), head_sha: _patch("only", offset=99)},
            seen=seen,
        )
        GithubReviewVerdictSource(runner=runner).verdict(repo=REPO, number=1, head_sha=head_sha)
        compares = [" ".join(c).split("/compare/", 1)[1].split(" ", 1)[0] for c in seen if "/compare/" in " ".join(c)]
        self.assertTrue(compares, "no compare was issued at all")
        for spec in compares:
            self.assertTrue(spec.startswith(f"{BASE_REF}..."), f"compare not anchored on the base branch: {spec}")

    def test_226_an_unreachable_compare_stays_unknown_not_approved(self):
        """Fail closed (#226's stated bar): when the comparison itself
        cannot be computed, the verdict must stay unknown -- never
        approved-by-default."""
        old_sha, head_sha = "a" * 40, "c" * 40
        reviews = '{"reviews":[{"state":"APPROVED","commit":{"oid":"' + old_sha + '"}}]}'

        def runner(cmd):
            if cmd[:3] == ["gh", "pr", "view"]:
                return reviews
            raise RuntimeError("gh: connection reset")

        source = GithubReviewVerdictSource(runner=runner)
        result = source.verdict(repo=REPO, number=1, head_sha=head_sha)
        self.assertEqual(result["verdict"], "unknown")
