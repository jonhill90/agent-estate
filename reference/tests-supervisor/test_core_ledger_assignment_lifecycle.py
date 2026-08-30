import sys
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from tests.supervisor.test_core_helpers import LedgerTestBase  # noqa: E402


class AssignmentLifecycleTest(LedgerTestBase):
    def test_assignment_requires_a_reconstructed_open_github_source(self):
        """Red: current assign() writes free-text summaries with no GitHub
        source check at all -- nothing gates assignment on the GitHub-side
        state actually permitting it."""
        with self.assertRaisesRegex(ValueError, "reconstructed GitHub source"):
            self.ledger.assign(
                task_id="no-source-task", lane="app-review", pane_nonce="nonce-22-a", summary="No source"
            )

        self.seed_source("closed-task", source_state="CLOSED")
        with self.assertRaisesRegex(ValueError, "source is not open"):
            self.ledger.assign(
                task_id="closed-task", lane="app-review", pane_nonce="nonce-22-a", summary="Closed source"
            )

        task = self.assign("open-task")
        self.assertEqual("created", task["status"])

    def test_one_nonterminal_task_per_lane_and_bound_acceptance(self):
        task = self.assign()
        self.assertEqual("created", task["status"])
        with self.assertRaisesRegex(ValueError, "outstanding task"):
            self.assign("review-871")

        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")
        accepted = self.ledger.accept("review-870", pane_nonce="nonce-22-a")
        self.assertEqual("accepted", accepted["status"])
        with self.assertRaisesRegex(ValueError, "pane incarnation"):
            self.ledger.accept("review-870", pane_nonce="reused-pane")

    def test_lane_available_is_tristate_unknown_free_or_occupied(self):
        """agent-dotfiles#174: `dispatch.sh` now trusts this instead of the
        tmux window name, and the three-way split matters -- an unregistered
        lane is a different claim from a registered-but-busy one, and the
        caller (the CLI's first-sight backfill) needs to tell them apart.
        """
        self.assertIsNone(self.ledger.lane_available("never-registered"))

        self.assertTrue(self.ledger.lane_available("app-review"))

        task = self.assign()
        self.ledger.mark_delivery_pending(task["id"], pane_nonce="nonce-22-a")
        self.ledger.mark_delivered(task["id"], pane_nonce="nonce-22-a")
        self.assertFalse(self.ledger.lane_available("app-review"))

        self.ledger.complete(task["id"], b"done", pane_nonce="nonce-22-a")
        self.assertTrue(self.ledger.lane_available("app-review"))

    def test_delivery_pending_persists_before_send_and_blocks_direct_delivered(self):
        self.assign()
        with self.assertRaisesRegex(ValueError, "cannot transition"):
            self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")

        pending = self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.assertEqual("delivery_pending", pending["status"])
        # Once ambiguous, it stays ambiguous until a transition names an outcome;
        # a second delivery attempt cannot be represented as a fresh "created" task.
        with self.assertRaisesRegex(ValueError, "outstanding task"):
            self.assign("review-871")

        delivered = self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")
        self.assertEqual("delivered", delivered["status"])

    def test_reconcile_delivery_confirms_or_retires_an_ambiguous_task(self):
        self.assign()
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        with self.assertRaisesRegex(ValueError, "outcome"):
            self.ledger.reconcile_delivery("review-870", pane_nonce="nonce-22-a", outcome="bogus")

        confirmed = self.ledger.reconcile_delivery("review-870", pane_nonce="nonce-22-a", outcome="delivered")
        self.assertEqual("delivered", confirmed["status"])

        self.ledger.cancel_open_task("app-review", abandoned=True)
        self.assign("review-871")
        self.ledger.mark_delivery_pending("review-871", pane_nonce="nonce-22-a")
        retired = self.ledger.reconcile_delivery("review-871", pane_nonce="nonce-22-a", outcome="failed")
        self.assertEqual("failed", retired["status"])
        # A retired ambiguous task frees the lane for a genuinely new assignment.
        self.assign("review-872")
