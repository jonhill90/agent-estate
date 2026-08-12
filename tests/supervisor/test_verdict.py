import json
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
from verdict import (  # noqa: E402
    GithubReviewVerdictSource,
    LedgerVerdictSource,
    _content_unchanged_since,
    _default_patch_id,
    build_source,
    main,
    resolve,
)


REPO = "jonhill90/agent-dotfiles"

# Two diffs that make the SAME content change (`old line` -> `new line`) but
# at different hunk offsets, the way a rebase shifts a patch without touching
# what it changes. `git patch-id --stable` ignores hunk line numbers, so
# these two must hash equal -- that normalisation is the whole mechanism
# #226 relies on.
REBASE_DIFF_OLD_OFFSET = (
    "diff --git a/foo.txt b/foo.txt\n"
    "index 1111111..2222222 100644\n"
    "--- a/foo.txt\n"
    "+++ b/foo.txt\n"
    "@@ -10,3 +10,3 @@\n"
    " line before\n"
    "-old line\n"
    "+new line\n"
    " line after\n"
)
REBASE_DIFF_NEW_OFFSET = (
    "diff --git a/foo.txt b/foo.txt\n"
    "index 3333333..4444444 100644\n"
    "--- a/foo.txt\n"
    "+++ b/foo.txt\n"
    "@@ -25,3 +25,3 @@\n"
    " line before\n"
    "-old line\n"
    "+new line\n"
    " line after\n"
)
# Same file, same offset as REBASE_DIFF_NEW_OFFSET, but a genuinely different
# replacement line -- a real content change, not just a rebase shift.
CONTENT_CHANGED_DIFF = (
    "diff --git a/foo.txt b/foo.txt\n"
    "index 3333333..5555555 100644\n"
    "--- a/foo.txt\n"
    "+++ b/foo.txt\n"
    "@@ -25,3 +25,3 @@\n"
    " line before\n"
    "-old line\n"
    "+a genuinely different line\n"
    " line after\n"
)


# A patch that is on the branch in BOTH the reviewed and the current head,
# at two different hunk offsets -- what a rebase does to a commit it does not
# touch. `git patch-id --stable` must hash these equal.
def _patch(marker, offset=10, replacement="new line"):
    return (
        f"diff --git a/{marker}.txt b/{marker}.txt\n"
        "index 1111111..2222222 100644\n"
        f"--- a/{marker}.txt\n"
        f"+++ b/{marker}.txt\n"
        f"@@ -{offset},3 +{offset},3 @@\n"
        " line before\n"
        "-old line\n"
        f"+{replacement}\n"
        " line after\n"
    )


BASE_REF = "main"


def _api_runner(*, reviews="{}", branches=None, patches=None, base_ref=BASE_REF, seen=None):
    """A `runner` over the four calls the #226 comparison makes.

    `branches` maps a head SHA to the commit SHAs that head introduces over
    the base branch; `patches` maps a commit SHA to its diff. The stub REFUSES
    any compare whose base is not `base_ref` -- that is the regression guard
    for agent-dotfiles#229's blocking finding: the first implementation asked
    `compare/old...new` and `compare/new...old`, which resolve to the same
    (pre-rebase) merge base and so measured whether `main` had moved. Anchor
    on the PR's base branch or the comparison cannot be computed at all."""
    branches = branches or {}
    patches = patches or {}

    def runner(cmd):
        if seen is not None:
            seen.append(cmd)
        if cmd[:3] == ["gh", "pr", "view"]:
            if cmd[-1] == "baseRefName":
                return json.dumps({"baseRefName": base_ref})
            return reviews
        joined = " ".join(cmd)
        if "/compare/" in joined:
            spec = joined.split("/compare/", 1)[1].split(" ", 1)[0]
            base, _, head = spec.partition("...")
            if base != base_ref:
                raise AssertionError(
                    "compare must be anchored on the PR's base branch, not on the other "
                    f"head (agent-dotfiles#226/#229) -- asked for compare/{spec}"
                )
            return "".join(f"{sha}\n" for sha in branches[head])
        if "/commits/" in joined:
            sha = joined.split("/commits/", 1)[1].split(" ", 1)[0]
            return patches[sha]
        raise RuntimeError(f"no stub for command: {cmd!r}")

    return runner


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


def _raising_runner(cmd):
    """A `runner` that always raises -- used where a test's fake SHAs are not
    real git objects, so the #226 rebase-content comparison must fail closed
    ("cannot compute") rather than reach a real `gh`/network."""
    raise RuntimeError(f"no stub for command: {cmd!r}")


class LedgerVerdictSourceTests(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.ledger = Ledger(self._tmp.name, clock=lambda: 1000)
        self.source = LedgerVerdictSource(self.ledger, runner=_raising_runner)

    def test_never_recorded_reads_none(self):
        self.assertEqual(self.source.verdict(repo=REPO, number=1), {"verdict": "none", "detail": ""})

    def test_recorded_approval_reads_approved(self):
        self.ledger.record_pr_verdict(
            repo=REPO, number=1, verdict="approved", head_sha="a" * 40, reviewer="lane-1"
        )
        self.assertEqual(self.source.verdict(repo=REPO, number=1)["verdict"], "approved")

    def test_recorded_rejection_reads_rejected(self):
        self.ledger.record_pr_verdict(
            repo=REPO, number=1, verdict="rejected", head_sha="a" * 40, reviewer="lane-1"
        )
        self.assertEqual(self.source.verdict(repo=REPO, number=1)["verdict"], "rejected")

    def test_re_recording_overwrites_the_prior_verdict(self):
        self.ledger.record_pr_verdict(
            repo=REPO, number=1, verdict="rejected", head_sha="a" * 40, reviewer="lane-1"
        )
        self.ledger.record_pr_verdict(
            repo=REPO, number=1, verdict="approved", head_sha="b" * 40, reviewer="lane-2"
        )
        self.assertEqual(self.source.verdict(repo=REPO, number=1)["verdict"], "approved")

    def test_recorded_verdict_at_current_head_is_reported(self):
        self.ledger.record_pr_verdict(
            repo=REPO, number=1, verdict="approved", head_sha="a" * 40, reviewer="lane-1"
        )
        result = self.source.verdict(repo=REPO, number=1, head_sha="a" * 40)
        self.assertEqual(result["verdict"], "approved")

    def test_regression_218_recorded_verdict_against_a_superseded_head_reads_unknown(self):
        """Same question as GithubReviewVerdictSourceTests's #218 cases, in
        the ledger's shape: a verdict recorded at SHA A must not answer for
        a PR a push has since moved to SHA B."""
        self.ledger.record_pr_verdict(
            repo=REPO, number=1, verdict="approved", head_sha="a" * 40, reviewer="lane-1"
        )
        result = self.source.verdict(repo=REPO, number=1, head_sha="b" * 40)
        self.assertEqual(result["verdict"], "unknown")
        self.assertNotEqual(result["verdict"], "none")

    def test_mutation_218_ledger_sha_comparison_that_always_passes_must_turn_this_red(self):
        self.ledger.record_pr_verdict(
            repo=REPO, number=1, verdict="rejected", head_sha="a" * 40, reviewer="lane-1"
        )
        stale = self.source.verdict(repo=REPO, number=1, head_sha="b" * 40)
        current = self.source.verdict(repo=REPO, number=1, head_sha="a" * 40)
        self.assertNotEqual(stale["verdict"], current["verdict"])

    def test_no_head_sha_given_preserves_pre_218_behaviour(self):
        self.ledger.record_pr_verdict(
            repo=REPO, number=1, verdict="rejected", head_sha="a" * 40, reviewer="lane-1"
        )
        self.assertEqual(self.source.verdict(repo=REPO, number=1)["verdict"], "rejected")

    def test_a_different_pr_number_is_unaffected(self):
        self.ledger.record_pr_verdict(
            repo=REPO, number=1, verdict="rejected", head_sha="a" * 40, reviewer="lane-1"
        )
        self.assertEqual(self.source.verdict(repo=REPO, number=2)["verdict"], "none")

    def test_broken_ledger_fails_closed_to_unknown(self):
        class BrokenLedger:
            def get_pr_verdict(self, *, repo, number):
                raise RuntimeError("sqlite3.DatabaseError: disk image is malformed")

        source = LedgerVerdictSource(BrokenLedger())
        self.assertEqual(source.verdict(repo=REPO, number=1)["verdict"], "unknown")

    def test_invalid_verdict_value_row_fails_closed_to_unknown(self):
        class CorruptLedger:
            def get_pr_verdict(self, *, repo, number):
                return {"verdict": "maybe", "reviewer": "lane-1", "updated_at": 1}

        source = LedgerVerdictSource(CorruptLedger())
        self.assertEqual(source.verdict(repo=REPO, number=1)["verdict"], "unknown")

    def test_record_rejects_bad_verdict_value(self):
        with self.assertRaises(ValueError):
            self.ledger.record_pr_verdict(
                repo=REPO, number=1, verdict="maybe", head_sha="a" * 40, reviewer="lane-1"
            )

    def test_226_pure_rebase_promotes_the_recorded_verdict_and_names_the_basis(self):
        old_sha, head_sha = "a" * 40, "c" * 40
        self.ledger.record_pr_verdict(repo=REPO, number=1, verdict="approved", head_sha=old_sha, reviewer="lane-1")

        runner = _api_runner(
            branches={old_sha: [old_sha], head_sha: [head_sha]},
            patches={old_sha: _patch("only", offset=10), head_sha: _patch("only", offset=99)},
        )
        source = LedgerVerdictSource(self.ledger, runner=runner)
        result = source.verdict(repo=REPO, number=1, head_sha=head_sha)
        self.assertEqual(result["verdict"], "approved")
        self.assertIn(old_sha, result["detail"])
        self.assertIn(head_sha, result["detail"])
        self.assertIn("patch-id", result["detail"])

    def test_226_a_real_content_change_since_the_recorded_verdict_stays_unknown(self):
        old_sha, head_sha = "a" * 40, "c" * 40
        self.ledger.record_pr_verdict(repo=REPO, number=1, verdict="approved", head_sha=old_sha, reviewer="lane-1")

        runner = _api_runner(
            branches={old_sha: [old_sha], head_sha: [head_sha]},
            patches={
                old_sha: _patch("only", offset=10),
                head_sha: _patch("only", offset=99, replacement="a genuinely different line"),
            },
        )
        source = LedgerVerdictSource(self.ledger, runner=runner)
        result = source.verdict(repo=REPO, number=1, head_sha=head_sha)
        self.assertEqual(result["verdict"], "unknown")


class ContentUnchangedSinceTests(unittest.TestCase):
    """Direct tests of the #226 comparison primitive, independent of which
    verdict source calls it."""

    OLD, NEW = "a" * 40, "c" * 40

    def _compare(self, **kwargs):
        return _content_unchanged_since(
            patch_id_fn=_default_patch_id, repo=REPO, number=1, old_sha=self.OLD, new_sha=self.NEW, **kwargs
        )

    def test_identical_shas_are_trivially_unchanged(self):
        unchanged, _ = _content_unchanged_since(
            runner=_raising_runner,
            patch_id_fn=lambda d: "x",
            repo=REPO,
            number=1,
            old_sha="a" * 40,
            new_sha="a" * 40,
        )
        self.assertTrue(unchanged)

    def test_matching_patch_ids_read_unchanged(self):
        unchanged, basis = self._compare(
            runner=_api_runner(
                branches={self.OLD: [self.OLD], self.NEW: [self.NEW]},
                patches={self.OLD: _patch("only", offset=10), self.NEW: _patch("only", offset=99)},
            )
        )
        self.assertTrue(unchanged)
        self.assertIn("patch-id", basis)
        self.assertIn(BASE_REF, basis)

    def test_differing_patch_ids_read_changed_not_unknown(self):
        unchanged, _ = self._compare(
            runner=_api_runner(
                branches={self.OLD: [self.OLD], self.NEW: [self.NEW]},
                patches={
                    self.OLD: _patch("only", offset=10),
                    self.NEW: _patch("only", offset=99, replacement="a genuinely different line"),
                },
            )
        )
        self.assertIs(unchanged, False)

    def test_229_a_rebase_onto_a_moved_main_is_still_unchanged(self):
        """The case the first implementation got wrong, in its own shape:
        `main` gained commits under the branch. Those commits belong to
        neither side's list, because each side is measured from its own
        merge-base with the base branch -- so the branch's two patches
        still match and the verdict is promoted."""
        unchanged, basis = self._compare(
            runner=_api_runner(
                branches={self.OLD: ["o1", self.OLD], self.NEW: ["n1", self.NEW]},
                patches={
                    "o1": _patch("first", offset=10),
                    self.OLD: _patch("second", offset=20),
                    "n1": _patch("first", offset=310),
                    self.NEW: _patch("second", offset=420),
                },
            )
        )
        self.assertTrue(unchanged)
        self.assertIn("identical set of 2", basis)

    def test_229_a_rebase_that_drops_a_superseded_commit_is_unchanged_and_says_so(self):
        """agent-dotfiles#226's OWN example has this shape, measured
        2026-08-12: `0538cc6` -> `69784bd` dropped a `known_references.json`
        refresh because upstream #210 replaced that file with `.txt`. Nothing
        unreviewed entered, so the verdict is promoted -- and the count of
        dropped patches is stated, because a reader must be able to see that
        the branch is not byte-for-byte what was approved."""
        unchanged, basis = self._compare(
            runner=_api_runner(
                branches={self.OLD: ["o1", "o2", self.OLD], self.NEW: ["n1", self.NEW]},
                patches={
                    "o1": _patch("first", offset=10),
                    "o2": _patch("superseded", offset=10),
                    self.OLD: _patch("second", offset=20),
                    "n1": _patch("first", offset=310),
                    self.NEW: _patch("second", offset=420),
                },
            )
        )
        self.assertTrue(unchanged)
        self.assertIn("1 of 3", basis)

    def test_229_an_extra_commit_is_changed_not_unchanged(self):
        unchanged, _ = self._compare(
            runner=_api_runner(
                branches={self.OLD: [self.OLD], self.NEW: [self.OLD, self.NEW]},
                patches={self.OLD: _patch("first"), self.NEW: _patch("added")},
            )
        )
        self.assertIs(unchanged, False)

    def test_a_diff_fetch_failure_reads_none_not_unchanged(self):
        unchanged, _ = self._compare(runner=_raising_runner)
        self.assertIsNone(unchanged)

    def test_an_unreadable_base_branch_reads_none_not_unchanged(self):
        """No base branch means no anchor, and an unanchored compare is the
        defect this fixes -- refuse to answer rather than fall back to it."""

        def runner(cmd):
            if cmd[:3] == ["gh", "pr", "view"]:
                return "{}"
            raise AssertionError("must not reach a compare with no base branch")

        unchanged, _ = self._compare(runner=runner)
        self.assertIsNone(unchanged)

    def test_a_commit_list_that_does_not_end_at_the_head_reads_none(self):
        """A truncated page understates the branch, which is the one error
        direction that could promote an unreviewed patch. Fail closed."""
        unchanged, _ = self._compare(
            runner=_api_runner(
                branches={self.OLD: [self.OLD], self.NEW: ["n1"]},
                patches={self.OLD: _patch("only"), "n1": _patch("only")},
            )
        )
        self.assertIsNone(unchanged)

    def test_a_commit_whose_patch_cannot_be_read_reads_none(self):
        unchanged, _ = self._compare(
            runner=_api_runner(
                branches={self.OLD: [self.OLD], self.NEW: [self.NEW]},
                patches={self.OLD: _patch("only"), self.NEW: "   \n"},
            )
        )
        self.assertIsNone(unchanged)

    def test_real_git_patch_id_normalises_hunk_offsets(self):
        """No mocking of patch-id itself -- proves the actual instrument
        (`git patch-id --stable`) does what #226 relies on: the same change
        at two different hunk offsets hashes equal, a genuinely different
        change does not."""
        id_old = _default_patch_id(REBASE_DIFF_OLD_OFFSET)
        id_new = _default_patch_id(REBASE_DIFF_NEW_OFFSET)
        id_changed = _default_patch_id(CONTENT_CHANGED_DIFF)
        self.assertIsNotNone(id_old)
        self.assertEqual(id_old, id_new)
        self.assertNotEqual(id_old, id_changed)

    def test_empty_diff_reads_none(self):
        self.assertIsNone(_default_patch_id(""))
        self.assertIsNone(_default_patch_id("   \n"))


class ResolveTests(unittest.TestCase):
    class Stub:
        def __init__(self, result):
            self.result = result

        def verdict(self, *, repo, number, head_sha=None):
            return self.result

    def test_first_decisive_source_wins(self):
        stubs = {"a": self.Stub({"verdict": "rejected", "detail": ""}), "b": self.Stub({"verdict": "approved", "detail": ""})}
        with self._patched(stubs):
            self.assertEqual(resolve(["a", "b"], state_dir="unused", repo=REPO, number=1)["verdict"], "rejected")

    def test_later_decisive_source_used_when_earlier_is_none(self):
        stubs = {"a": self.Stub({"verdict": "none", "detail": ""}), "b": self.Stub({"verdict": "approved", "detail": ""})}
        with self._patched(stubs):
            self.assertEqual(resolve(["a", "b"], state_dir="unused", repo=REPO, number=1)["verdict"], "approved")

    def test_unknown_is_not_masked_by_a_later_none(self):
        """Fail-closed across sources: an error from one adapter must not be
        silently swallowed just because another configured source has
        nothing on record."""
        stubs = {"a": self.Stub({"verdict": "unknown", "detail": "broken"}), "b": self.Stub({"verdict": "none", "detail": ""})}
        with self._patched(stubs):
            self.assertEqual(resolve(["a", "b"], state_dir="unused", repo=REPO, number=1)["verdict"], "unknown")

    def test_all_none_reads_none(self):
        stubs = {"a": self.Stub({"verdict": "none", "detail": ""}), "b": self.Stub({"verdict": "none", "detail": ""})}
        with self._patched(stubs):
            self.assertEqual(resolve(["a", "b"], state_dir="unused", repo=REPO, number=1)["verdict"], "none")

    def test_head_sha_is_threaded_through_to_the_source(self):
        """resolve() must pass its head_sha argument on to each source's
        verdict() call, not swallow it -- a source cannot detect a stale
        review or ledger record (#218) if resolve() never gives it the
        current head to compare against."""
        received = {}

        class CapturingStub:
            def verdict(self, *, repo, number, head_sha=None):
                received["head_sha"] = head_sha
                return {"verdict": "none", "detail": ""}

        with self._patched({"a": CapturingStub()}):
            resolve(["a"], state_dir="unused", repo=REPO, number=1, head_sha="deadbeef" * 5)
        self.assertEqual(received["head_sha"], "deadbeef" * 5)

    def test_unknown_source_name_fails_closed_to_unknown(self):
        result = resolve(["not-a-real-source"], state_dir="unused", repo=REPO, number=1)
        self.assertEqual(result["verdict"], "unknown")

    def _patched(self, stubs):
        import verdict as verdict_module

        return _PatchSources(verdict_module, stubs)


class _PatchSources:
    def __init__(self, module, stubs):
        self.module = module
        self.stubs = stubs
        self._original = module.build_source

    def __enter__(self):
        def fake_build_source(name, *, state_dir):
            if name not in self.stubs:
                raise ValueError(f"unknown verdict source: {name}")
            return self.stubs[name]

        self.module.build_source = fake_build_source
        return self

    def __exit__(self, *exc):
        self.module.build_source = self._original


class BuildSourceTests(unittest.TestCase):
    def test_unknown_name_raises(self):
        with self.assertRaises(ValueError):
            build_source("not-a-real-source", state_dir="unused")

    def test_ledger_name_resolves_to_a_working_ledger_source(self):
        with tempfile.TemporaryDirectory() as tmp:
            source = build_source("ledger", state_dir=tmp)
            self.assertIsInstance(source, LedgerVerdictSource)
            self.assertEqual(source.verdict(repo=REPO, number=1)["verdict"], "none")

    def test_github_name_resolves_to_a_github_source(self):
        source = build_source("github", state_dir="unused")
        self.assertIsInstance(source, GithubReviewVerdictSource)


class CliTests(unittest.TestCase):
    def test_record_then_get_roundtrips_through_the_ledger(self):
        with tempfile.TemporaryDirectory() as tmp:
            rc = main(
                [
                    "--state-dir",
                    tmp,
                    "record",
                    "--repo",
                    REPO,
                    "--number",
                    "9",
                    "--verdict",
                    "rejected",
                    "--head-sha",
                    "a" * 40,
                    "--reviewer",
                    "lane-3",
                ]
            )
            self.assertEqual(rc, 0)

            import io
            import contextlib
            import json

            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                rc = main(
                    ["--state-dir", tmp, "get", "--repo", REPO, "--number", "9", "--source", "ledger"]
                )
            self.assertEqual(rc, 0)
            self.assertEqual(json.loads(out.getvalue())["verdict"], "rejected")

    def test_get_with_head_sha_detects_a_stale_ledger_record(self):
        """agent-dotfiles#218 end-to-end through the CLI: record at SHA A,
        `get` with a DIFFERENT --head-sha, confirm the CLI itself (not just
        the class under test) resolves to unknown rather than approved."""
        with tempfile.TemporaryDirectory() as tmp:
            rc = main(
                [
                    "--state-dir", tmp, "record", "--repo", REPO, "--number", "9",
                    "--verdict", "approved", "--head-sha", "a" * 40, "--reviewer", "lane-3",
                ]
            )
            self.assertEqual(rc, 0)

            import io
            import contextlib
            import json

            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                rc = main(
                    [
                        "--state-dir", tmp, "get", "--repo", REPO, "--number", "9",
                        "--source", "ledger", "--head-sha", "b" * 40,
                    ]
                )
            self.assertEqual(rc, 0)
            self.assertEqual(json.loads(out.getvalue())["verdict"], "unknown")

    def test_get_never_raises_even_for_a_broken_state_dir(self):
        """main() must always produce a well-formed unknown verdict, never a
        traceback a caller could mistake for a hung or crashed process."""
        import io
        import contextlib
        import json

        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            rc = main(
                [
                    "--state-dir",
                    "/nonexistent/definitely/not/writable/anywhere",
                    "get",
                    "--repo",
                    REPO,
                    "--number",
                    "1",
                    "--source",
                    "ledger",
                ]
            )
        self.assertEqual(rc, 0)
        self.assertEqual(json.loads(out.getvalue())["verdict"], "unknown")


if __name__ == "__main__":
    unittest.main()
