import socket
import sqlite3
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


class ReviveWhileLaneGenuinelyOccupied(unittest.TestCase):
    """agent-supervisor#182: PR #175's revival guard (`ReusedLaneClaimTokens`
    above) tells apart "my own dead token" (safe to revive) from "a
    stranger's active claim" (must refuse) -- but only by checking the
    STATUS of the row sitting on this claim's own id. It never asks whether
    the LANE ITSELF is free once that revival succeeds.

    A stale token retried after the placeholder it names has already been
    superseded (by `commit_lane_claim` + `record_dispatch`, exactly
    `dispatch.sh`'s own sequence) by a live, non-claim dispatch on the same
    lane used to hit the revival UPDATE unconditionally: reviving the dead
    placeholder collides with `one_open_task_per_lane` against that still-
    active dispatch -- a SECOND `sqlite3.IntegrityError`, raised from inside
    the `except IntegrityError:` block that was supposed to be handling the
    first one. Nothing caught it: `_transaction()` rolled back and
    re-raised, and `cli.py`'s `claim-lane` has no try/except around this
    call, so `dispatch.sh` got a bare traceback where it expects JSON.

    Before #175 this returned the ordinary refusal:
    `{'claimed': False, 'reason': 'occupied', 'holder': 'real-task-1'}`.
    It must again -- and the genuine holder must be left untouched.
    """

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.ledger = Ledger(Path(self._tmp.name))
        self.ledger.register_lane(
            lane="free-4", pane_id="%4", nonce="nonce-0", harness="claude",
            repo="/repo/app", server_id="socket:1", session_id="$0", command="claude.exe",
        )
        first = self.ledger.claim_lane("free-4", token="tok-A", owner=claim_owner_token(1))
        self.assertTrue(first["claimed"])
        self.ledger.commit_lane_claim("free-4", token="tok-A")
        # The real dispatch supersedes the placeholder -- `_register_lane_tx`
        # cancels `ledger-claim:free-4:tok-A` in place, same as #174's setup.
        self.ledger.record_dispatch(
            lane="free-4", pane_id="%4", nonce="nonce-1", harness="claude",
            repo="/repo/app", server_id="socket:1", session_id="$0", command="claude.exe",
            task_id="real-task-1", source_kind="issue", source_url="https://example/182",
            source_ref="182", summary="genuine work", source_state="open",
            evidence=["claimed by dispatch.sh for lane free-4"],
        )
        self.assertEqual("cancelled", self.ledger.get_task("ledger-claim:free-4:tok-A")["status"])
        # real-task-1 is left ACTIVE -- genuinely working, not completed.
        self.assertNotIn(self.ledger.get_task("real-task-1")["status"], ("complete", "failed", "cancelled"))

    def test_a_stale_retry_is_refused_not_crashed(self):
        """The defect itself: retrying the dead token against a lane a
        different, still-active task now holds must return JSON, not raise."""
        second = self.ledger.claim_lane("free-4", token="tok-A", owner=claim_owner_token(2))
        self.assertFalse(second["claimed"])
        self.assertEqual("occupied", second["reason"])
        self.assertEqual("real-task-1", second["holder"])

    def test_the_real_holder_is_undisturbed(self):
        """Ratchet check: the refusal must not revive the dead placeholder
        nor touch the genuine occupant it collided with."""
        before_holder = self.ledger.get_task("real-task-1")
        before_placeholder = self.ledger.get_task("ledger-claim:free-4:tok-A")
        self.ledger.claim_lane("free-4", token="tok-A", owner=claim_owner_token(2))
        self.assertEqual(before_holder, self.ledger.get_task("real-task-1"))
        self.assertEqual(before_placeholder, self.ledger.get_task("ledger-claim:free-4:tok-A"))


