import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
from tests.supervisor.test_core_helpers import MutableClock  # noqa: E402


class ReconcileAfterReRegistrationTest(unittest.TestCase):
    """Blocker 2: reconcile must survive a re-registered lane."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.clock = MutableClock()
        self.ledger = Ledger(Path(self.tempdir.name), clock=self.clock)
        self.ledger.register_lane(
            lane="app-review", pane_id="%22", nonce="nonce-dead", harness="codex", repo="/repo/app",
            server_id="server-a", session_id="$4", command="codex",
        )
        self.ledger.reconstruct_task(
            task_id="review-870", source_kind="issue",
            source_url="https://github.com/jonhill90/Hill90/issues/870", source_ref="a" * 40,
            summary="Review PR 870 without editing", source_state="OPEN", status="created",
            evidence=[], status_marker=None,
        )
        self.ledger.assign(
            task_id="review-870", lane="app-review", pane_nonce="nonce-dead",
            summary="Review PR 870 without editing",
        )
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-dead")

    def test_reproduction_reconciling_with_the_lanes_current_nonce_wedges(self):
        """Reproduces the reported bug: passing the LANE's current (new)
        nonce - the CLI's old behavior - fails against the task's own
        pane_nonce recorded at send time."""
        self.ledger.register_lane(
            lane="app-review", pane_id="%23", nonce="nonce-reborn", harness="codex", repo="/repo/app",
            server_id="server-a", session_id="$4", command="codex",
        )
        current_lane_nonce = self.ledger.get_lane("app-review")["nonce"]
        self.assertEqual("nonce-reborn", current_lane_nonce)
        with self.assertRaisesRegex(ValueError, "pane incarnation does not match task"):
            self.ledger.reconcile_delivery("review-870", pane_nonce=current_lane_nonce, outcome="delivered")

    def test_reconcile_with_task_pane_nonce_succeeds_after_lane_re_registration(self):
        self.ledger.register_lane(
            lane="app-review", pane_id="%23", nonce="nonce-reborn", harness="codex", repo="/repo/app",
            server_id="server-a", session_id="$4", command="codex",
        )
        task_pane_nonce = self.ledger.get_task("review-870")["pane_nonce"]
        self.assertEqual("nonce-dead", task_pane_nonce)
        reconciled = self.ledger.reconcile_delivery("review-870", pane_nonce=task_pane_nonce, outcome="delivered")
        self.assertEqual("delivered", reconciled["status"])

    def test_reconcile_still_rejects_a_wrong_task_nonce(self):
        with self.assertRaisesRegex(ValueError, "pane incarnation does not match task"):
            self.ledger.reconcile_delivery("review-870", pane_nonce="some-guessed-nonce", outcome="delivered")


