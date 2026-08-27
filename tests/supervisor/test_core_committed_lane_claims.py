import socket
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import (  # noqa: E402
    Ledger,
    claim_owner_token,
)


class CommittedLaneClaims(unittest.TestCase):
    """agent-dotfiles#209 round 2: a claim with a LIVE brief behind it.

    Round 1 gave the dispatcher two cleanup paths -- a trap for the exits a
    shell can observe, a reap for the ones it cannot -- and drew the line
    between "still unwindable" and "a worker may be running" with an
    in-process bash flag set well AFTER the brief was submitted into the pane.
    Inside that window both paths freed a lane that was actively working:
    #102/#126's failure produced by the cleanup rather than prevented by it.

    `commit_lane_claim` moves that line onto the send and writes it to the
    LEDGER, which is what makes it survive the SIGKILL case -- at that instant
    the placeholder is the only record the lane is occupied at all, because
    `record_dispatch` has not run. So the assertions that matter here are the
    refusals: after a commit, neither cleanup path may free the lane, however
    provably gone its owner is.
    """

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.ledger = Ledger(Path(self._tmp.name))
        self.ledger.register_lane(
            lane="free-9", pane_id="%9", nonce="nonce-9", harness="claude",
            repo="/repo/app", server_id="socket:1", session_id="$0", command="claude.exe",
        )
        self.ledger.claim_lane("free-9", token="ad1-live", owner=claim_owner_token(4242, host="this-host"))

    def test_commit_marks_the_claim_live_and_keeps_the_lane_occupied(self):
        result = self.ledger.commit_lane_claim("free-9", token="ad1-live")
        self.assertTrue(result["committed"])
        self.assertIsNone(result["reason"])
        self.assertEqual("delivered", self.ledger.get_task("ledger-claim:free-9:ad1-live")["status"])
        self.assertFalse(self.ledger.lane_available("free-9"))

    def test_release_will_not_free_a_committed_claim(self):
        """The trap fires on every exit including a signal, and calls release
        unconditionally. This scope is what makes that safe."""
        self.ledger.commit_lane_claim("free-9", token="ad1-live")
        # ...and it says so, rather than reporting a release it did not do.
        self.assertFalse(self.ledger.release_lane_claim("free-9", token="ad1-live"))
        self.assertIsNotNone(self.ledger.get_task("ledger-claim:free-9:ad1-live"))
        self.assertFalse(self.ledger.lane_available("free-9"))

    def test_release_reports_the_row_it_really_removed(self):
        """The control for the assertion above: on a claim still reserved,
        the same call returns True and the lane comes back."""
        self.assertTrue(self.ledger.release_lane_claim("free-9", token="ad1-live"))
        self.assertTrue(self.ledger.lane_available("free-9"))
        # Idempotent, and honest the second time.
        self.assertFalse(self.ledger.release_lane_claim("free-9", token="ad1-live"))

    def test_reap_will_not_free_a_committed_claim_even_when_its_owner_is_gone(self):
        """The dangerous half. `is_alive` is forced False -- the owner is as
        provably dead as the reap can ever establish -- and the claim must
        still survive, because a dead owner does not mean an idle pane."""
        self.ledger.commit_lane_claim("free-9", token="ad1-live")
        self.assertEqual([], self.ledger.reap_stale_lane_claims(host="this-host", is_alive=lambda pid: False))
        self.assertFalse(self.ledger.lane_available("free-9"))

    def test_an_uncommitted_claim_is_still_reaped_and_released(self):
        """The control, and the previous round's finding held: before the
        commit nothing is working the lane, so both paths must still clear it.
        A guard that withheld here would trade #209's bug for #102's."""
        self.assertEqual(
            ["ledger-claim:free-9:ad1-live"],
            [row["id"] for row in self.ledger.reap_stale_lane_claims(host="this-host", is_alive=lambda pid: False)],
        )
        self.assertTrue(self.ledger.lane_available("free-9"))

    def test_commit_is_idempotent(self):
        self.assertTrue(self.ledger.commit_lane_claim("free-9", token="ad1-live")["committed"])
        again = self.ledger.commit_lane_claim("free-9", token="ad1-live")
        self.assertTrue(again["committed"])
        self.assertEqual("delivered", self.ledger.get_task("ledger-claim:free-9:ad1-live")["status"])

    def test_commit_refuses_a_claim_that_does_not_exist(self):
        """Refuses rather than inventing the row. `dispatch.sh` treats this as
        fatal and does not send, which is free: nothing is in the pane yet."""
        result = self.ledger.commit_lane_claim("free-9", token="somebody-elses-token")
        self.assertFalse(result["committed"])
        self.assertEqual("missing", result["reason"])
        self.assertIsNone(self.ledger.get_task("ledger-claim:free-9:somebody-elses-token"))

    def test_commit_refuses_a_claim_already_released(self):
        self.ledger.release_lane_claim("free-9", token="ad1-live")
        self.assertFalse(self.ledger.commit_lane_claim("free-9", token="ad1-live")["committed"])
        self.assertTrue(self.ledger.lane_available("free-9"))

    def test_a_clean_dispatch_still_supersedes_a_committed_claim(self):
        """The success path, and the reason `delivered` is the status used.

        `_register_lane_tx` excludes `delivery_pending` from the outstanding
        task it cancels through, so parking the claim there would make it
        survive `record_dispatch` and then collide with its task INSERT under
        `one_open_task_per_lane` -- breaking every clean dispatch. `delivered`
        is cancelled normally, so the ordinary path is unchanged.
        """
        self.ledger.commit_lane_claim("free-9", token="ad1-live")
        self.ledger.register_lane(
            lane="free-9", pane_id="%9", nonce="nonce-9-new", harness="claude",
            repo="/repo/app", server_id="socket:1", session_id="$0", command="claude.exe",
        )
        self.assertEqual("cancelled", self.ledger.get_task("ledger-claim:free-9:ad1-live")["status"])
        self.assertTrue(self.ledger.lane_available("free-9"))


