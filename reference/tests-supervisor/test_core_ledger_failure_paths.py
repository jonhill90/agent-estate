import sys
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from tests.supervisor.test_core_helpers import LedgerTestBase  # noqa: E402


class FailurePathsTest(LedgerTestBase):
    def test_fail_unaccepted_terminates_a_delivered_task_with_no_accepted_at(self):
        """agent-supervisor#193's own primitive: a `delivered` task with no
        `accepted_at` gets terminated `failed`, not `complete` -- see
        `fail_unaccepted`'s own docstring for why. Mirrors `complete()`'s
        shape (immutable result, pane-nonce check, idempotent) on purpose."""
        self.assign()
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")

        with self.assertRaisesRegex(ValueError, "pane incarnation"):
            self.ledger.fail_unaccepted("review-870", b"never accepted", pane_nonce="some-other-nonce")

        failed = self.ledger.fail_unaccepted("review-870", b"never accepted", pane_nonce="nonce-22-a")
        self.assertEqual("failed", failed["status"])
        self.assertIsNotNone(failed["completed_at"])
        # Idempotent: a second call with the SAME result is a no-op, not an error.
        again = self.ledger.fail_unaccepted("review-870", b"never accepted", pane_nonce="nonce-22-a")
        self.assertEqual("failed", again["status"])
        # `failed` is terminal -- the lane is free for a fresh dispatch.
        self.assertTrue(self.ledger.lane_available("app-review"))

    def test_fail_unaccepted_refuses_a_task_that_was_actually_accepted(self):
        """The one thing this must never do: erase a GENUINE acceptance.
        `accepted_at` already set is the caller's own evidence being stale or
        wrong -- refuse rather than silently overwrite the record."""
        self.assign()
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")
        self.ledger.accept("review-870", pane_nonce="nonce-22-a")

        with self.assertRaisesRegex(ValueError, "accepted"):
            self.ledger.fail_unaccepted("review-870", b"never accepted", pane_nonce="nonce-22-a")

    def test_fail_unaccepted_refuses_a_task_that_is_not_delivered(self):
        """Only `delivered` is eligible -- a `created` (unsent) or `complete`
        task has no business going through this path at all."""
        task = self.assign()
        with self.assertRaisesRegex(ValueError, "only 'delivered' is eligible"):
            self.ledger.fail_unaccepted(task["id"], b"x", pane_nonce="nonce-22-a")

    def test_fail_stale_delivery_terminates_a_delivered_task_with_accepted_at_set(self):
        """agent-supervisor#374's own primitive: unlike `fail_unaccepted`,
        this never refuses on `accepted_at` -- a claude-print/pi-rpc lane has
        no pane to observe, so `accepted_at` set or not, silence proves
        nothing about success. `accepted=True` here is dispatch's own
        evidence (see `record_dispatch`'s docstring) -- it stamps
        `accepted_at` WITHOUT moving `status` off `delivered`, exactly the
        shape a claude-print lane's row has. Mirrors `complete()`/
        `fail_unaccepted()`'s shape otherwise (immutable result, pane-nonce
        check, idempotent)."""
        self.ledger.record_dispatch(
            lane="app-review", pane_id="%22", nonce="nonce-22-a", harness="codex",
            repo="/repo/app", server_id="server-a", session_id="$2", command="codex",
            task_id="review-870", source_kind="issue",
            source_url="https://github.com/acme/app/issues/870",
            source_ref="870", summary="issue #870", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane app-review", "issues: 870"],
            accepted=True,
        )
        self.assertEqual("delivered", self.ledger.get_task("review-870")["status"])
        self.assertIsNotNone(self.ledger.get_task("review-870")["accepted_at"])

        with self.assertRaisesRegex(ValueError, "pane incarnation"):
            self.ledger.fail_stale_delivery("review-870", b"no pane to observe", pane_nonce="some-other-nonce")

        failed = self.ledger.fail_stale_delivery("review-870", b"no pane to observe", pane_nonce="nonce-22-a")
        self.assertEqual("failed", failed["status"])
        self.assertIsNotNone(failed["completed_at"])
        # Idempotent: a second call with the SAME result is a no-op, not an error.
        again = self.ledger.fail_stale_delivery("review-870", b"no pane to observe", pane_nonce="nonce-22-a")
        self.assertEqual("failed", again["status"])
        # `failed` is terminal -- the lane is free for a fresh dispatch.
        self.assertTrue(self.ledger.lane_available("app-review"))

    def test_fail_stale_delivery_refuses_a_task_that_is_not_delivered(self):
        """Only `delivered` is eligible -- the same discipline `fail_unaccepted` holds."""
        task = self.assign()
        with self.assertRaisesRegex(ValueError, "only 'delivered' is eligible"):
            self.ledger.fail_stale_delivery(task["id"], b"x", pane_nonce="nonce-22-a")

    def test_fail_stale_delivery_refuses_a_completed_or_cancelled_task(self):
        self.assign()
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")
        self.ledger.complete("review-870", b"done", pane_nonce="nonce-22-a")
        # Same content as the completion above -- an intentionally DIFFERENT
        # result here would trip `_write_result`'s immutable-content check
        # before the status check this test is exercising ever runs.
        with self.assertRaisesRegex(ValueError, "cannot fail-stale-delivery"):
            self.ledger.fail_stale_delivery("review-870", b"done", pane_nonce="nonce-22-a")
