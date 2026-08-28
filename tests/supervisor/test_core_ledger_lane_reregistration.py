import sys
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from tests.supervisor.test_core_helpers import LedgerTestBase  # noqa: E402


class LaneReregistrationTest(LedgerTestBase):
    def test_lane_reregistration_cancels_an_outstanding_non_pending_task_instead_of_rebinding_it(self):
        """register_lane does an unconditional INSERT ... ON CONFLICT UPDATE,
        so re-registering with a CHANGED identity would silently rebind a
        live task's identity out from under it if nothing intervened.
        `delivery_pending` is exempted: it has its own reconciliation escape
        valve keyed off the task's own pane_nonce (see `_reconcile_transition`),
        which #871 relies on to survive a dead pane being re-registered.

        agent-dotfiles#144 finding 3: for every OTHER outstanding status this
        used to raise here, permanently -- the only way out was `lane-done.sh`
        completing that exact task by renaming the window it owned, so a lane
        freed any other way (renamed by hand, a worker that died mid-turn)
        wedged every subsequent register_lane call for that lane forever. A
        changed identity is the evidence that the old task can never complete
        through this pane again, so this now CANCELS the stale task and
        proceeds with the re-registration, rather than wedging."""
        task = self.assign()
        self.assertEqual("created", task["status"])
        registered = self.ledger.register_lane(
            lane="app-review",
            pane_id="%23",
            nonce="nonce-23-b",
            harness="codex",
            repo="/repo/app",
            server_id="server-a",
            session_id="$4",
            command="codex",
        )
        self.assertEqual("nonce-23-b", registered["nonce"])
        self.assertEqual("nonce-23-b", self.ledger.get_lane("app-review")["nonce"])
        stale = self.ledger.get_task("review-870")
        self.assertEqual("cancelled", stale["status"])
        # And the lane is genuinely free again: a new task can be assigned to
        # it under the new incarnation without hitting the one-open-task guard.
        self.seed_source("review-871")
        reassigned = self.ledger.assign(
            task_id="review-871", lane="app-review", pane_nonce="nonce-23-b", summary="Second task"
        )
        self.assertEqual("created", reassigned["status"])

    def test_lane_reregistration_with_the_same_identity_does_not_cancel_the_outstanding_task(self):
        """The cancel-on-reregistration self-heal (#144 finding 3) is scoped
        to a CHANGED identity. Re-registering the exact same pane_id/nonce/
        harness/repo/server_id/session_id/command -- the ordinary no-op path,
        e.g. a duplicate register call -- must leave a genuinely still-running
        task alone."""
        task = self.assign()
        self.assertEqual("created", task["status"])
        self.ledger.register_lane(
            lane="app-review",
            pane_id="%22",
            nonce="nonce-22-a",
            harness="codex",
            repo="/repo/app",
            server_id="server-a",
            session_id="$4",
            command="codex",
        )
        self.assertEqual("created", self.ledger.get_task("review-870")["status"])

    def test_lane_reregistration_does_not_cancel_a_delivery_pending_task(self):
        """#871: a `delivery_pending` task survives re-registration untouched
        -- it has its own reconciliation path keyed off the task's own
        pane_nonce, and must not be silently cancelled by a re-registration
        racing an in-flight send."""
        self.assign()
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.register_lane(
            lane="app-review",
            pane_id="%23",
            nonce="nonce-23-b",
            harness="codex",
            repo="/repo/app",
            server_id="server-a",
            session_id="$4",
            command="codex",
        )
        self.assertEqual("delivery_pending", self.ledger.get_task("review-870")["status"])
