import sys
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from tests.supervisor.test_core_helpers import LedgerTestBase  # noqa: E402


class LaneClaimsTest(LedgerTestBase):
    def test_mark_lane_held_makes_a_free_lane_read_occupied(self):
        """agent-dotfiles#188 finding 1: this is what closes the window a
        rolled-back `record_dispatch` used to leave open -- a lane the ledger
        already had registered free must not go on reading free just because
        the write that was supposed to claim it failed."""
        self.assertTrue(self.ledger.lane_available("app-review"))
        held = self.ledger.mark_lane_held("app-review", note="record_dispatch failed for task review-870")
        self.assertEqual("created", held["status"])
        self.assertEqual("app-review", held["lane"])
        self.assertFalse(self.ledger.lane_available("app-review"))

    def test_mark_lane_held_on_an_unknown_lane_is_a_no_op(self):
        """Nothing to close: an unregistered lane already reads `None`
        (unknown), and unknown is not free -- `dispatch.sh`'s `lane-free`
        never offers it. Writing a placeholder task here would need a
        `lanes` row that does not exist and violate the FK."""
        self.assertIsNone(self.ledger.lane_available("never-registered"))
        self.assertIsNone(self.ledger.mark_lane_held("never-registered", note="unused"))
        self.assertIsNone(self.ledger.lane_available("never-registered"))

    def test_mark_lane_held_on_an_already_occupied_lane_is_a_no_op(self):
        """The lane is already exactly as unavailable as this method would
        make it -- the one_open_task_per_lane index refuses the second
        placeholder, and that refusal is fine: it means something else
        already guarantees the property this method exists for."""
        self.assign()
        self.assertFalse(self.ledger.lane_available("app-review"))
        self.assertIsNone(self.ledger.mark_lane_held("app-review", note="unused"))
        self.assertFalse(self.ledger.lane_available("app-review"))

    def test_record_dispatch_failure_leaves_the_lane_held_not_free(self):
        """The mutation this guards against, end to end: seed a lane already
        free, force `record_dispatch` to fail deep inside its transaction
        (the same `assign` collision #144's own atomicity test uses), and
        confirm the failure does not leave the pre-existing free row intact.
        Without `mark_lane_held` wired into the caller, this goes red -- the
        rollback restores exactly the free row asserted at the top."""
        self.assertTrue(self.ledger.lane_available("app-review"))
        self.seed_source("collide-870")
        self.ledger.assign(
            task_id="collide-870", lane="app-review", pane_nonce="nonce-22-a", summary="already here"
        )
        self.ledger.cancel_open_task("app-review", abandoned=True)
        self.assertTrue(self.ledger.lane_available("app-review"))
        # A second dispatch racing the first tries to reuse "collide-870" for
        # a DIFFERENT lane -- `_assign_tx` refuses it, `record_dispatch`
        # rolls back every write it had made for "app-review" so far, and
        # the pre-existing free row for "app-review" survives the rollback
        # untouched, exactly the #188 finding 1 scenario.
        with self.assertRaisesRegex(ValueError, "already exists with different assignment"):
            self.ledger.record_dispatch(
                lane="app-review",
                pane_id="%22",
                nonce="nonce-22-b",
                harness="codex",
                repo="/repo/app",
                server_id="server-a",
                session_id="$4",
                command="codex",
                task_id="collide-870",
                source_kind="issue",
                source_url="https://github.com/jonhill90/agent-dotfiles/issues/870",
                source_ref="870",
                summary="a different task body entirely",
                source_state="OPEN",
                evidence=["claimed by dispatch.sh for lane app-review"],
                status_marker=None,
            )
        self.assertTrue(self.ledger.lane_available("app-review"))  # the bug, absent the fix
        self.ledger.mark_lane_held("app-review", note="record_dispatch failed for task collide-870")
        self.assertFalse(self.ledger.lane_available("app-review"))  # closed
