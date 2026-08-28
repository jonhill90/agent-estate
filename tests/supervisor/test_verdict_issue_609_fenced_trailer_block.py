import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import (  # noqa: E402
    GithubReviewVerdictSource,
    _parse_verdict_comment,
)

from tests.supervisor.test_verdict_helpers import (  # noqa: E402
    _comment_runner,
    _pre_595_scan,
)


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
