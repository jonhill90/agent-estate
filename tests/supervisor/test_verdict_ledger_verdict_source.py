import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402

from verdict import LedgerVerdictSource  # noqa: E402

from tests.supervisor.test_verdict_helpers import (  # noqa: E402
    REPO,
    _api_runner,
    _patch,
    _raising_runner,
)


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
