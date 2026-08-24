import json
import re
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
    _APPROVED_TOKENS,
    _author_lane_line,
    _bare_decision_line,
    _classify_decision_text,
    _content_unchanged_since,
    _default_patch_id,
    _label_inside_inline_code,
    _normalise_decision_text,
    _parse_author_lane,
    _parse_verdict_comment,
    _scan_verdict_lines,
    _VERDICT_LINE_RE,
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


def _comment_runner(*, reviews=None, comments=None, author=None, commits=None):
    """A `runner` for `gh pr view ... --json reviews,comments,author,commits`
    -- agent-supervisor#53, `commits` added by #213 so the comment-verdict
    freshness backstop has PR commit timestamps to compare against. Raises
    for anything else, the same discipline `_reviews_runner` uses, so a
    test that does not opt into the rebase comparison gets a fail-closed
    "cannot compute" rather than a stub silently answering for a call it
    was never given."""
    payload = json.dumps(
        {"reviews": reviews or [], "comments": comments or [], "author": author or {}, "commits": commits or []}
    )

    def runner(cmd):
        if cmd[:3] == ["gh", "pr", "view"]:
            return payload
        raise RuntimeError(f"no stub for command: {cmd!r}")

    return runner


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


class Issue513AuthorLaneTrailerTests(unittest.TestCase):
    """agent-supervisor#513: `_parse_author_lane` is the PR-body complement
    to `_parse_review_lane` -- measured against agent-dotfiles#308, which
    stamped `Author-Lane: estate:4` in a PR body that `git grep -rn
    'Author-Lane' -- scripts/` proved nothing ever read. Mirrors that
    function's own regex shape line for line (`_AUTHOR_LANE_LINE_RE`, no
    `\\s*` between the colon and the capture group) rather than
    re-deriving it, for the exact reason #232's own comment on
    `_REVIEW_LANE_LINE_RE` gives."""

    def test_lane_shaped_token_parses_despite_trailing_prose(self):
        """Same parse #232 fixed for Review-Lane: the first lane-shaped
        token on the line, ignoring trailing prose after it."""
        body = "Some PR body text.\n\nAuthor-Lane: estate:4 (opened this from my own dispatch)\n"
        self.assertEqual(_parse_author_lane(body), "estate:4")

    def test_absent_trailer_is_none_not_a_guess(self):
        self.assertIsNone(_parse_author_lane("no trailer of any kind here"))
        self.assertIsNone(_parse_author_lane(""))
        self.assertIsNone(_parse_author_lane(None))

    def test_unparseable_line_with_no_lane_token_and_no_ledger_is_none(self):
        body = "Author-Lane: nonsense with no lane token"
        self.assertIsNone(_parse_author_lane(body))

    def test_off_pane_lane_id_recognised_via_ledger_fallback(self):
        """agent-supervisor#292's own reasoning applies here too: a
        claude-print/pi-rpc lane id has no `<session>:<index>` shape for
        `_LANE_SHAPE_RE` to find, so a registered off-pane lane must still
        resolve through the ledger fallback, exactly like
        `_parse_review_lane`."""

        class _FakeLedger:
            def get_lane(self, lane_id):
                return {"lane": lane_id} if lane_id == "ad182-review-186" else None

        body = "Author-Lane: ad182-review-186\n"
        self.assertEqual(_parse_author_lane(body, ledger=_FakeLedger()), "ad182-review-186")
        self.assertIsNone(_parse_author_lane(body, ledger=None))

    def test_blank_author_lane_does_not_swallow_the_next_line(self):
        """The deliverable this issue names explicitly: `^\\s*Author-Lane:
        \\s*(.*)$` (a stray `\\s*` between the colon and the capture group)
        lets `\\s*` walk across the newline a blank trailer leaves and
        capture the FOLLOWING line's text as if it were the Author-Lane
        value -- the exact defect fixed for Review-Lane in skills#260,
        agent-tui#113 and agent-dotfiles#305, reproduced here for its
        Author-Lane sibling. `_AUTHOR_LANE_LINE_RE` has no such `\\s*`; a
        blank `Author-Lane:` line followed immediately by a line that
        itself contains a lane-shaped token (`Review-Lane: t:9`) must
        parse as no claim at all, never as `t:9`."""
        body = "Author-Lane:\nReview-Lane: t:9\n"
        # The raw matched line is the blank trailer alone, not the next line.
        self.assertEqual(_author_lane_line(body), "Author-Lane:")
        self.assertIsNone(_parse_author_lane(body))

    def test_mutation_reintroducing_the_swallow_regex_would_fail_the_above(self):
        """Proves the test above is real evidence, not a check that cannot
        fail (this repo's own CLAUDE.md requirement): re-derive the SAME
        parse with the historically-buggy pattern (`\\s*` reinstated
        between the colon and the capture group) and confirm it DOES
        swallow the next line -- i.e. that the fixed regex in
        `verdict.py` is doing real work, not passing by construction."""
        buggy_re = re.compile(r"(?im)^\s*Author-Lane:\s*(.*)$")
        body = "Author-Lane:\nReview-Lane: t:9\n"
        match = buggy_re.search(body)
        self.assertIsNotNone(match, "buggy regex should still match something")
        self.assertIn("t:9", match.group(1), "the buggy regex swallows the next line's content")


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


class ParseVerdictCommentAmbiguityTests(unittest.TestCase):
    """agent-supervisor#196: the line-scan #192 introduced reaches every
    qualifying line in a comment, not just the first -- which made a SECOND
    verdict line inside the same comment reachable for the first time (the
    old body-start anchor could only ever see the first `**Verdict:` of the
    whole body). A reviewer who drafts `Verdict: APPROVE`, reconsiders, and
    writes `Verdict: REQUEST CHANGES` below it must not have that rejection
    silently overridden by the earlier approval -- first-match-wins is
    exactly the "REQUEST CHANGES read as approved" failure this module
    exists to prevent, just triggered from inside one comment instead of
    across two. Table-driven over every combination the docstring commits
    to a rule for."""

    # agent-supervisor#595: every `Verdict:` line below now carries its OWN
    # `Review-Lane:`/`Reviewed-SHA:` pair immediately after it, so each one
    # stays a qualifying line under the new complete-block requirement -- a
    # comment with two conflicting verdict BLOCKS must still refuse as
    # ambiguous, the same as it did with two bare labels before this fix.
    _TRAILER_A = "\nReview-Lane: t:4\nReviewed-SHA: " + "a" * 40
    _TRAILER_B = "\nReview-Lane: t:5\nReviewed-SHA: " + "b" * 40

    CASES = [
        (
            "conflicting lines, approve then reject, refuse as ambiguous",
            "Verdict: APPROVE" + _TRAILER_A + "\n\nActually wait.\n\nVerdict: REQUEST CHANGES" + _TRAILER_B,
            None,
        ),
        (
            "conflicting lines, reject then approve, refuse as ambiguous",
            "Verdict: REQUEST CHANGES" + _TRAILER_A + "\n\nOn reflection.\n\nVerdict: APPROVE" + _TRAILER_B,
            None,
        ),
        (
            "two identical decisions, not a conflict",
            "Verdict: APPROVE" + _TRAILER_A + "\n\nStill true after re-reading.\n\nVerdict: APPROVE" + _TRAILER_B,
            "approved",
        ),
        (
            "first line's decision word is unrecognised, a later line is valid",
            "Verdict: LOOKS OK TO ME" + _TRAILER_A + "\n\nOn second thought:\n\nVerdict: REQUEST CHANGES" + _TRAILER_B,
            "rejected",
        ),
        (
            "one line only, unaffected by the multi-line rule",
            "Verdict: REQUEST CHANGES" + _TRAILER_A,
            "rejected",
        ),
    ]

    def test_multi_line_ambiguity_cases(self):
        for name, body, expected in self.CASES:
            with self.subTest(name):
                self.assertEqual(_parse_verdict_comment(body), expected)

    def test_mutation_reverting_to_first_match_wins_turns_the_conflict_cases_red(self):
        """Mutate back to "return on the first qualifying line" -- #196's
        own bug, and #192's actual shape before this fix -- and confirm the
        conflicting-line cases above go red. If they did not, the ambiguity
        table above would not be real evidence of the fix."""

        def first_match_wins_parse(body):
            in_fence = False
            for raw_line in (body or "").splitlines():
                line = raw_line.strip()
                if line.startswith("```"):
                    in_fence = not in_fence
                    continue
                if in_fence or line.startswith(">"):
                    continue
                match = _VERDICT_LINE_RE.match(line)
                if not match:
                    continue
                rest = match.group(2)
                end = rest.find("**")
                if end != -1:
                    rest = rest[:end]
                decision_text = re.sub(r"[*_`]+$", "", rest.strip()).strip()
                decision_text = decision_text.rstrip(".:;,!").strip().upper()
                if "REQUEST CHANGES" in decision_text:
                    return "rejected"
                if "APPROVE" in decision_text:
                    return "approved"
                return None
            return None

        regressions = [
            (name, body, expected)
            for name, body, expected in self.CASES
            if first_match_wins_parse(body) != expected
        ]
        self.assertTrue(
            regressions,
            "reverting to first-match-wins did not turn any ambiguity case red -- "
            "the ambiguity table has no coverage of the #196 defect",
        )
        for name, body, expected in regressions:
            with self.subTest(name):
                self.assertEqual(_parse_verdict_comment(body), expected)


class BareDecisionLineTests(unittest.TestCase):
    """agent-supervisor#475 case 3 (agent-supervisor#472, round 2) used to be
    a FEATURE here: a lane wrote `Verdict:` on its own line at the top of a
    long comment and `APPROVE` as a standalone line at the very end, and
    `_bare_decision_line` scanned the rest of the comment for that standalone
    decision to adopt when the label's own text was empty.

    agent-supervisor#595 RETIRES that feature, not merely leaves it alone.
    `_scan_verdict_lines` now requires an operative `Verdict:` match to be
    the first line of an unbroken three-line `Verdict:`/`Review-Lane:`/
    `Reviewed-SHA:` block -- and the #475 case-3 shape structurally can
    never satisfy that: an empty `Verdict:` label is, by definition, not
    immediately followed by a `Review-Lane:` line (it is followed by prose,
    then eventually a bare decision word many lines later). Keeping the old
    fallback wired in would have meant a bare, untrailered label could still
    manufacture an operative verdict by scanning forward for a decision
    word -- exactly the kind of unanchored inference #595 closes off. See
    the comment at `_scan_verdict_lines`'s own (removed) call site in
    `scripts/supervisor/verdict.py` for the same reasoning stated next to
    the code.

    What used to be `POSITIVE_CASES` (shapes that used to resolve to a
    decision) now correctly resolve to `None` -- an empty `Verdict:` label
    is just another unrecognised/incomplete line now, the same fail-closed
    answer any other label with nothing genuine following it gets. The two
    tests exercising `_bare_decision_line` directly are kept: that function
    is still defined (only its WIRING into `_scan_verdict_lines` was
    removed), and its own pure-function behaviour is still real code worth
    covering."""

    RETIRED_CASES = [
        (
            "#475 case 3 itself: empty label at the top, bare APPROVE at the end -- now None",
            "Verdict:\n\nThe patch-id comparison looks right, tests pass, "
            "and the freshness check covers the rebase case correctly.\n\nAPPROVE",
        ),
        (
            "empty label, bare REQUEST CHANGES at the end -- now None",
            "Verdict:\n\nThe freshness check does not compare the base branch correctly.\n\nREQUEST CHANGES",
        ),
        (
            "empty label, bare decision immediately below it -- now None",
            "Verdict:\nAPPROVE",
        ),
    ]

    NEGATIVE_CASES = [
        (
            "no Verdict: label at all -- a bare APPROVE alone must not become a verdict",
            "Looks fine to me.\n\nAPPROVE",
        ),
        (
            "empty label, two conflicting bare lines -- refuses as ambiguous",
            "Verdict:\n\nAPPROVE\n\nOn reflection:\n\nREQUEST CHANGES",
        ),
        (
            "empty label, bare decision only inside a fenced code block -- not consulted",
            "Verdict:\n\n```\nAPPROVE\n```",
        ),
        (
            "empty label, bare decision only inside a blockquote -- not consulted",
            "Verdict:\n\n> APPROVE",
        ),
        (
            "empty label, no bare decision anywhere -- unchanged from before the fix",
            "Verdict:\n\nStill reviewing, no conclusion yet.",
        ),
    ]

    def test_every_retired_case_now_resolves_to_none(self):
        """agent-supervisor#595 supersedes #475 case 3: an empty `Verdict:`
        label with a bare decision word elsewhere in the comment is no
        longer promoted to a verdict -- it never had a complete trailer
        block, so it was never operative under the new rule."""
        for name, body in self.RETIRED_CASES:
            with self.subTest(name):
                self.assertIsNone(_parse_verdict_comment(body))

    def test_every_negative_case_reads_none(self):
        for name, body in self.NEGATIVE_CASES:
            with self.subTest(name):
                self.assertIsNone(_parse_verdict_comment(body))

    def test_mutation_reintroducing_the_bare_scan_wiring_would_turn_case_3_green_again(self):
        """The inverse of this file's usual mutation-test idiom: this class
        asserts a FEATURE was removed, so the "mutation" that would turn the
        RETIRED_CASES table's own outcome around is re-wiring the retired
        `_bare_decision_line` fallback back into a from-scratch scan --
        proving the retired case's `None` result actually depends on the
        wiring being gone, not on some unrelated accident."""

        def pre_595_scan_with_bare_fallback(body):
            in_fence = False
            lines = []
            for raw_line in (body or "").splitlines():
                line = raw_line.strip()
                if line.startswith("```"):
                    in_fence = not in_fence
                    continue
                if in_fence or line.startswith(">"):
                    continue
                lines.append(line)

            results = []
            has_empty_label = False
            for line in lines:
                match = _VERDICT_LINE_RE.match(line)
                if not match:
                    continue
                decision_text = _normalise_decision_text(match.group(2))
                if decision_text == "":
                    has_empty_label = True
                results.append((_classify_decision_text(decision_text), decision_text))

            if has_empty_label:
                bare = _bare_decision_line(lines)
                if bare is not None:
                    bare_decision, bare_text = bare
                    results = [
                        (bare_decision, bare_text) if decision is None and text == "" else (decision, text)
                        for decision, text in results
                    ]
            return results

        def pre_595_parse(body):
            decisions = {d for d, _ in pre_595_scan_with_bare_fallback(body) if d is not None}
            if len(decisions) == 1:
                return decisions.pop()
            return None

        case_3 = next(body for name, body in self.RETIRED_CASES if "case 3 itself" in name)
        self.assertIsNone(_parse_verdict_comment(case_3), "the real, current parser must NOT resolve this")
        self.assertEqual(
            pre_595_parse(case_3), "approved",
            "re-wiring the old fallback must still resolve this -- proving the retirement, not an accident, is why the real parser now refuses",
        )

    def test_bare_decision_line_ignores_lines_with_their_own_label(self):
        """A line that already carries its own `Verdict:` label is never a
        BARE candidate -- it is handled (and classified) by the label scan
        itself; treating it as bare too would double-count it. This tests
        `_bare_decision_line` directly, as a pure function -- it is no
        longer called from `_scan_verdict_lines` (agent-supervisor#595), but
        the function itself is still real code."""
        self.assertIsNone(_bare_decision_line(["Verdict: APPROVE"]))

    def test_bare_decision_line_empty_input_reads_none(self):
        self.assertIsNone(_bare_decision_line([]))


class DecisionTokenTests(unittest.TestCase):
    """agent-supervisor#198: the decision half of `_parse_verdict_comment`
    was an `in` substring test -- `"APPROVE" in decision_text` reads
    `Verdict: NOT APPROVED` and `Verdict: DISAPPROVE` as approved, because
    both strings CONTAIN "APPROVE". Measured on `main` before this fix
    (the issue's own table):

        Verdict: NOT APPROVED   -> approved
        Verdict: DISAPPROVE     -> approved

    And the asymmetry that made it dangerous rather than merely wrong:
    `REQUEST CHANGES` was checked first, so a body naming both phrases
    correctly refused -- but any rejection phrased WITHOUT those two exact
    words that happened to contain "APPROVE" as a substring landed on
    approve. The failure was one-directional and it always favoured
    merging. This table is red against the pre-fix substring test and
    green against `_classify_decision_text` -- see
    `test_mutation_reverting_to_a_substring_test_turns_the_negation_cases_red`
    below for the actual red/green transition."""

    # (name, raw decision text as it appears after "Verdict:", expected)
    CASES = [
        ("plain approve", "APPROVE", "approved"),
        ("approved, past tense", "APPROVED", "approved"),
        ("request changes, space form", "REQUEST CHANGES", "rejected"),
        ("request changes, hyphen form", "REQUEST-CHANGES", "rejected"),
        ("rejected, one word", "REJECTED", "rejected"),
        # agent-supervisor#475: the reversed, natural word order.
        ("reversed order, changes requested", "CHANGES REQUESTED", "rejected"),
        ("reversed order, lowercase", "changes requested", "rejected"),
        ("lowercase approve", "approve", "approved"),
        ("lowercase request changes", "request changes", "rejected"),
        ("extra internal whitespace", "REQUEST   CHANGES", "rejected"),
        ("surrounding whitespace", "   APPROVE   ", "approved"),
        # The issue's own measured regressions:
        ("negated with NOT -- the issue's own measurement", "NOT APPROVED", None),
        ("negated with DIS -- the issue's own measurement", "DISAPPROVE", None),
        ("negated in prose", "not approving this", None),
        ("negated with a contraction", "I don't approve this", None),
        ("trailing question mark reads as uncertain, not a decision", "APPROVE?", None),
        # The no-right-answer case named explicitly in the brief: a real
        # thing reviewers write, and it is neither approval nor rejection.
        ("approved with changes -- neither, must not resolve", "APPROVED WITH CHANGES", None),
        ("no decision at all", "", None),
        ("unrelated text", "LOOKS GOOD", None),
    ]

    def _normalise(self, decision_text):
        text = re.sub(r"[*_`]+$", "", decision_text.strip()).strip()
        text = text.rstrip(".:;,!").strip()
        return re.sub(r"\s+", " ", text).upper()

    def test_every_decision_token_case(self):
        for name, decision_text, expected in self.CASES:
            with self.subTest(name):
                self.assertEqual(_classify_decision_text(self._normalise(decision_text)), expected)

    def test_every_decision_token_case_through_a_full_verdict_line(self):
        """Same table, exercised through the real entry point (a whole
        `Verdict:` line), not just the normalised-text helper. agent-
        supervisor#595: a `Review-Lane:`/`Reviewed-SHA:` pair is appended so
        the label is even CONSIDERED under the new complete-block
        requirement -- without it every case in this table would resolve
        `None` for a reason unrelated to what this test is actually
        checking (decision-token classification, not block completeness)."""
        for name, decision_text, expected in self.CASES:
            with self.subTest(name):
                body = f"Verdict: {decision_text}\nReview-Lane: t:4\nReviewed-SHA: " + "a" * 40
                self.assertEqual(_parse_verdict_comment(body), expected)

    def test_mutation_reverting_to_a_substring_test_turns_the_negation_cases_red(self):
        """Mutate back to the pre-#198 shape -- `"REQUEST CHANGES" in text`
        then `"APPROVE" in text` -- and confirm the negation cases from the
        table above (the ones the issue measured on `main`) go red. If they
        did not, the table above would not be real evidence of the fix."""

        def pre_198_classify(decision_text):
            if "REQUEST CHANGES" in decision_text:
                return "rejected"
            if "APPROVE" in decision_text:
                return "approved"
            return None

        regressions = [
            (name, decision_text, expected)
            for name, decision_text, expected in self.CASES
            if pre_198_classify(self._normalise(decision_text)) != expected
        ]
        self.assertTrue(
            regressions,
            "reverting to a substring test did not turn any case red -- "
            "the decision-token table has no coverage of the #198 defect",
        )
        for name, decision_text, expected in regressions:
            with self.subTest(name):
                self.assertEqual(_classify_decision_text(self._normalise(decision_text)), expected)


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


class Issue571DiagnosisCommentShadowingTests(unittest.TestCase):
    """agent-supervisor#571: a SECOND shape of #540's shadowing bug, live on
    `agent-supervisor#547` -- a comment DIAGNOSING the failing `gate` check
    quotes the gate's own failure sentence, which mentions "REQUEST CHANGES"
    and "Verdict:" in the same breath, but carries no trailer block at all
    (no `Review-Lane:`, no `Reviewed-SHA:`). This is the exact comment
    (`gh pr view 547 --json comments`, posted `2026-08-24T02:20:57Z` by
    `@jonhill90`), verbatim -- not a paraphrase.

    #540/#544 already close THIS specific text: both real `Verdict:`
    mentions inside it sit either inside a fenced code block (the literal
    quote of the gate's own assertion) or inside a single-backtick span
    (`` `Verdict: REQUEST CHANGES` ``, `` `Verdict:` ``) -- exactly the two
    shapes `_scan_verdict_lines`/`_label_inside_inline_code` already
    exclude. `test_mutation_reverting_540s_guard_turns_this_second_shape_red`
    below proves that anchoring, not a coincidence of this text, is what
    keeps it from shadowing: reverting to the pre-#540 regex (no
    inline-code exclusion) DOES misclassify line 44 of this comment as a
    decisive `REQUEST CHANGES` -- the second shape #571 names. This class
    locks the current, correct behaviour in as a regression test built from
    the real fixture, per #571's own brief ("use the exact #547 comment...
    verify both arms")."""

    DIAGNOSIS_COMMENT = (
        "## Diagnosed the failing `gate` check — it's Fixpass evidence, not UI evidence (`#565`)\n"
        "\n"
        "`gh pr checks 547` shows two checks both named `gate` — exactly the `#565`\n"
        "ambiguity. Resolved which is which by checking each run's own workflow\n"
        "name, not the display label:\n"
        "\n"
        "```\n"
        'run 32681427761  conclusion=failure  name="Fixpass evidence"   headSha=becbbaa4\n'
        'run 32681427793  conclusion=success  name="UI evidence"        headSha=becbbaa4\n'
        "```\n"
        "\n"
        "**The failing one is `Fixpass evidence`.** Its job log (`Check fixpass\n"
        "evidence` step) prints the actual assertion:\n"
        "\n"
        "```\n"
        "PR #547 had a REQUEST CHANGES review or a rejected Verdict: line, but\n"
        "carries no <!-- fixpass-evidence:v1 --> marker. Re-run the reviewer's own\n"
        "reproduction command after the fix and paste, prefixed with the marker:\n"
        "\n"
        "  <!-- fixpass-evidence:v1 -->\n"
        "  $ <reviewer's exact reproduction command>\n"
        "  <its output>\n"
        "  exit code: <captured directly -- never $? after a pipe>\n"
        "```\n"
        "\n"
        "That's the same cause that blocked `#553` earlier — a REQUEST CHANGES\n"
        "verdict on record with no qualifying evidence block. **One nuance worth\n"
        "naming so the fix lands right the first time:** that CI run executed at\n"
        "`01:57:14Z`, 14 seconds after `becbbaa4` was pushed — 38 seconds *before*\n"
        "a `<!-- fixpass-evidence:v1 -->` block was posted at `01:57:52Z`. So the\n"
        'job log\'s specific wording ("carries no marker") is a snapshot of that\n'
        "instant, not the current state. Re-running the gate live just now gives a\n"
        "more precise, current reason:\n"
        "\n"
        "```\n"
        "$ python3 scripts/supervisor/fixpass_evidence_gate.py --repo jonhill90/agent-supervisor --number 547\n"
        "PR #547 carries 1 populated <!-- fixpass-evidence:v1 --> block(s), but\n"
        "all of them predate the most recent rejection (at 2026-08-24T02:01:28Z)\n"
        "-- that round's rejection has no fresh evidence posted after it yet\n"
        "exit: 1\n"
        "```\n"
        "\n"
        "The `01:57:52Z` evidence block is real, but it's dated *before* the\n"
        "`02:01:28Z` `Verdict: REQUEST CHANGES` / `Review-Lane: estate:4` comment\n"
        "— which is itself a re-post (identity-correction only, `register-lane-\n"
        "self.sh`) of an *earlier* finding whose substance `becbbaa4` (pushed\n"
        "`01:57:00Z`) already fixes: \"this round's own count is eight blocked\n"
        'PRs... not the seven `0008` lists." The gate has no way to know that\n'
        "re-post's substance was already addressed before it was even reposted —\n"
        "it only sees a `Verdict:`/`Review-Lane:` pair with a later timestamp than\n"
        "any existing evidence block, and refuses on that basis. Correct behavior\n"
        "for what it can see, even though the underlying finding is stale.\n"
        "\n"
        "**What this means for closing it, per this repo's own rule (`#553`'s\n"
        "same cause):** the fix is **not** a bare marker to satisfy the parser,\n"
        "and **not** a no-op commit to force a re-run. Post a real\n"
        "`<!-- fixpass-evidence:v1 -->` block — a command, its actual output, and\n"
        "a resolved exit code — demonstrating the residual-count fix `becbbaa4`\n"
        "already made, timestamped after `02:01:28Z` so the gate has something to\n"
        "resolve the standing rejection against. (For reference, something in the\n"
        'shape of `grep -c "eight blocked PRs" docs/decisions/0008-...` against\n'
        "the current head would demonstrate it directly, but the exact command is\n"
        "the author's call, not mine to prescribe.)\n"
        "\n"
        "Not touched: `#553`'s own remaining failure (`shell-suites (shard 4)`) is\n"
        "out of scope here — flagged to me as already attributed elsewhere\n"
        "(`#567`, `test_inbox_poll.sh`'s SIGTERM escalation, distinct from `#548`)\n"
        "with its own owner; not re-derived or acted on in this comment.\n"
    )

    # --- unit level: the instrument itself -------------------------------

    def test_diagnosis_comment_yields_no_scan_line_at_all(self):
        """The real #547 diagnosis comment must never be a scan candidate --
        not merely unrecognised. Both real `Verdict:` mentions inside it are
        excluded: one by the fenced-code-block check (the literal quote of
        the gate's own assertion), the other two by `_label_inside_inline_code`
        (`` `Verdict: REQUEST CHANGES` `` and `` `Verdict:` ``, both
        single-backtick-quoted)."""
        self.assertEqual(_scan_verdict_lines(self.DIAGNOSIS_COMMENT), [])

    def test_mutation_reverting_540s_guard_turns_this_second_shape_red(self):
        """Proves the exclusion, not a coincidence of this text, is what
        keeps the diagnosis comment from shadowing: the pre-#540 regex (no
        label-capturing group, so `_label_inside_inline_code` cannot run)
        DOES match the `` `Verdict: REQUEST CHANGES` `` line inside this
        comment and classifies it as a decisive `REQUEST CHANGES` --
        exactly the false rejection #571 describes. Fence exclusion alone
        (independent of #540) is not enough for this second shape, because
        this particular line sits OUTSIDE any fenced block."""
        pre_540_re = re.compile(r"^#{0,6}\s*.*?\*{0,2}verdict:\**\s*(.*)$", re.IGNORECASE)
        offending_line = "`02:01:28Z` `Verdict: REQUEST CHANGES` / `Review-Lane: estate:4` comment"
        self.assertIn(offending_line, self.DIAGNOSIS_COMMENT)
        match = pre_540_re.match(offending_line)
        self.assertIsNotNone(match, "the pre-fix regex must still match this second shape (that IS the gap)")
        self.assertEqual(_classify_decision_text(_normalise_decision_text(match.group(1))), "rejected")

    # --- end-to-end: the actual refusal path ------------------------------

    def test_diagnosis_comment_does_not_shadow_the_real_approval_on_547(self):
        """Reproduces #547's live shape end to end: a properly trailered
        `REQUEST CHANGES`, superseded by a properly trailered `APPROVE` at
        the current head, followed by the diagnosis comment as the NEWEST
        comment on the PR. The diagnosis comment carries no trailer at all
        and must never become the operative verdict -- the genuine, fresher
        `APPROVE` must still be what resolves, exactly as it does on the
        real PR today."""
        head_sha = "b" * 40
        comments = [
            {
                "author": {"login": "jonhill90"},
                "body": "Verdict: REQUEST CHANGES\nReview-Lane: estate:4\nReviewed-SHA: " + "a" * 40,
                "createdAt": "2026-08-24T02:01:28Z",
            },
            {
                "author": {"login": "jonhill90"},
                "body": "Verdict: APPROVE\nReview-Lane: estate:5\nReviewed-SHA: " + head_sha,
                "createdAt": "2026-08-24T02:05:44Z",
            },
            {
                "author": {"login": "jonhill90"},
                "body": self.DIAGNOSIS_COMMENT,
                "createdAt": "2026-08-24T02:20:57Z",
            },
        ]
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=comments))
        result = source.verdict(repo=REPO, number=547, head_sha=head_sha)
        self.assertEqual(result["verdict"], "approved")
        self.assertNotIn("not recognised", result.get("detail", ""))

    def test_a_properly_trailered_rejection_still_blocks_with_the_diagnosis_comment_newest(self):
        """The other arm #571 asks for explicitly: don't regress #540's own
        fix. A properly trailered `REQUEST CHANGES` at the current head must
        still be selected and still block, even with the untrailered
        diagnosis comment posted after it as the newest comment on the PR."""
        head_sha = "c" * 40
        comments = [
            {
                "author": {"login": "jonhill90"},
                "body": "Verdict: REQUEST CHANGES\nReview-Lane: estate:4\nReviewed-SHA: " + head_sha,
                "createdAt": "2026-08-24T02:01:28Z",
            },
            {
                "author": {"login": "jonhill90"},
                "body": self.DIAGNOSIS_COMMENT,
                "createdAt": "2026-08-24T02:20:57Z",
            },
        ]
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=comments))
        result = source.verdict(repo=REPO, number=547, head_sha=head_sha)
        self.assertEqual(result["verdict"], "rejected")


def _pre_595_scan(body):
    """Reimplementation of `_scan_verdict_lines` AS IT WAS before
    agent-supervisor#595/#609: fenced code blocks and blockquotes excluded,
    the inline-code label guard (#540) applied, but NO three-line block
    requirement -- a bare `Verdict:`-shaped label alone was enough. Used
    only by this class's mutation tests, to prove each poisoning fixture
    below actually fooled the OLD code (not merely that it fails to fool
    the new code, which could be true for an unrelated reason)."""
    lines = []
    in_fence = False
    for raw_line in (body or "").splitlines():
        line = raw_line.strip()
        if line.startswith("```"):
            in_fence = not in_fence
            continue
        if in_fence or line.startswith(">"):
            continue
        lines.append(line)

    results = []
    for line in lines:
        match = _VERDICT_LINE_RE.match(line)
        if not match:
            continue
        if _label_inside_inline_code(line, match.start(1), match.end(1)):
            continue
        decision_text = _normalise_decision_text(match.group(2))
        results.append((_classify_decision_text(decision_text), decision_text))
    return results


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


class Issue609FencedTrailerBlockTests(unittest.TestCase):
    """agent-supervisor#609: the mirror-image bug, closed by the SAME fix
    (#595's decision comment says so explicitly) -- a real, correct,
    approving verdict trailer sitting inside a fenced code block on PR
    `#608` was invisible to the old parser, which excluded every fenced
    line unconditionally. `REAL_FENCED_COMMENT` and
    `REAL_UNFENCED_REPOST` below are the exact two real comments on PR
    `#608` (`gh pr view 608 --json comments,commits,headRefOid --repo
    jonhill90/agent-supervisor`, fetched live for this fix, not
    paraphrased) -- the second comment is the author's own re-post after
    discovering the fenced one did not parse, itself now a live example of
    the same shape without a fence."""

    REAL_HEAD = "0e7e21c0550af0e4c712e1f778a723c6beff12e5"

    REAL_FENCED_COMMENT = (
        "## Independent review — verified, not just re-run\n\n"
        "Read the prior attempt's own report (`as605-as605-daemon-ns.md`) but "
        "re-derived every claim in it directly rather than trusting the "
        "writeup — confirmed all of it, nothing superseded or contradicted.\n\n"
        "**Diff scope** (`origin/main`..`pr608`, after refreshing my local "
        "`main` — my first diff was stale and wrongly included the unrelated "
        "#572 trap commit already in history on both sides): only `core.py` "
        "(+77), `cli.py` (+19), `test_core.py` (+104), `test_merge_pr.sh` "
        "(+196). Narrow, as the issue asked.\n\n"
        "### The three constraints, checked myself against the real "
        "functions (not the tests' own assertions):\n\n"
        "1. **No new self-assertion path.** `daemon_lane_verified` requires "
        "the ledger's own row for the id to carry `server_id='supervisord'`, "
        "`transport='claude-print'`, `pane_id=''` — the exact signature only "
        "`EnsureLane` writes.\n\n"
        "No findings. This is a narrow, correctly-scoped fix that does "
        "exactly what #605's decision asked and nothing more.\n\n"
        "```\n"
        "Verdict: APPROVE\n"
        "Review-Lane: agent-supervisor:2\n"
        f"Reviewed-SHA: {REAL_HEAD}\n"
        "```"
    )

    REAL_UNFENCED_REPOST = (
        "Reposting the recorded decision from my prior comment without a "
        "code fence around the trailers — the merge gate's parser skips "
        "fenced content, so it saw no decision at all (filed as #609). "
        "Substance unchanged: same head SHA as my earlier review, so "
        "nothing to re-assess.\n\n"
        "Verdict: APPROVE\n"
        "Review-Lane: agent-supervisor:2\n"
        f"Reviewed-SHA: {REAL_HEAD}"
    )

    def test_the_real_608_fenced_trailer_now_resolves_approved(self):
        self.assertEqual(_parse_verdict_comment(self.REAL_FENCED_COMMENT), "approved")

    def test_mutation_reverting_the_fence_exclusion_turns_608_red(self):
        """Proves the fence exclusion specifically, not a coincidence of
        this text, is what used to hide this real trailer -- the OLD scan
        (fences excluded) finds nothing at all in this comment; the current
        one resolves it."""
        old_scan = _pre_595_scan(self.REAL_FENCED_COMMENT)
        self.assertEqual(old_scan, [], "the old, fence-excluding scan must find nothing here -- that IS the #609 bug")
        self.assertEqual(_parse_verdict_comment(self.REAL_FENCED_COMMENT), "approved")

    def test_the_real_608_unfenced_repost_also_resolves_approved(self):
        """The author's own re-post (after discovering the fenced version
        did not parse) is a live, real example of the same trailer without
        a fence -- must resolve the same way."""
        self.assertEqual(_parse_verdict_comment(self.REAL_UNFENCED_REPOST), "approved")

    def test_both_608_comments_end_to_end_through_the_real_source_resolve_approved(self):
        """Driven through the real `GithubReviewVerdictSource.verdict()`,
        with both real PR #608 comments present in chronological order (the
        fenced original, then the unfenced re-post) and the PR's real
        `headRefOid` -- confirms the fenced comment is not merely
        parseable in isolation but is what the estate's own merge gate
        would now read for this PR."""
        comments = [
            {"author": {"login": "jonhill90"}, "body": self.REAL_FENCED_COMMENT, "createdAt": "2026-08-24T05:00:00Z"},
            {"author": {"login": "jonhill90"}, "body": self.REAL_UNFENCED_REPOST, "createdAt": "2026-08-24T05:10:00Z"},
        ]
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=comments))
        result = source.verdict(repo="jonhill90/agent-supervisor", number=608, head_sha=self.REAL_HEAD)
        self.assertEqual(result["verdict"], "approved")


if __name__ == "__main__":
    unittest.main()
