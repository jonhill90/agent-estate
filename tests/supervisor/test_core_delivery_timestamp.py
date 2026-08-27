import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
from tests.supervisor.test_core_helpers import MutableClock  # noqa: E402


class DeliveryTimestampTest(unittest.TestCase):
    """Correctness defect: mark_delivery_pending must not write delivered_at."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.clock = MutableClock()
        self.ledger = Ledger(Path(self.tempdir.name), clock=self.clock)
        self.ledger.register_lane(
            lane="app-review", pane_id="%22", nonce="nonce-22-a", harness="codex", repo="/repo/app",
            server_id="server-a", session_id="$4", command="codex",
        )
        self.ledger.reconstruct_task(
            task_id="review-870", source_kind="issue",
            source_url="https://github.com/jonhill90/Hill90/issues/870", source_ref="a" * 40,
            summary="Review PR 870 without editing", source_state="OPEN", status="created",
            evidence=[], status_marker=None,
        )
        self.ledger.assign(
            task_id="review-870", lane="app-review", pane_nonce="nonce-22-a",
            summary="Review PR 870 without editing",
        )

    def test_mark_delivery_pending_sets_delivery_attempted_at_not_delivered_at(self):
        self.clock.value = 1_500
        pending = self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.assertEqual(1_500, pending["delivery_attempted_at"])
        self.assertIsNone(pending["delivered_at"])

    def test_confirmed_delivery_sets_delivered_at_and_keeps_attempt_timestamp(self):
        self.clock.value = 1_500
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.clock.value = 1_600
        delivered = self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")
        self.assertEqual(1_500, delivered["delivery_attempted_at"])
        self.assertEqual(1_600, delivered["delivered_at"])

    def test_failed_reconciliation_leaves_delivered_at_null_but_retains_attempt_timestamp(self):
        self.clock.value = 1_500
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.clock.value = 1_700
        retired = self.ledger.reconcile_delivery("review-870", pane_nonce="nonce-22-a", outcome="failed")
        self.assertEqual("failed", retired["status"])
        self.assertIsNone(retired["delivered_at"])
        self.assertEqual(1_500, retired["delivery_attempted_at"])
        self.assertEqual(1_700, retired["completed_at"])


