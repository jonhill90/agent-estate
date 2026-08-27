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


class ReusedLaneClaimTokens(unittest.TestCase):
    """agent-supervisor#174: a healthy, idle lane refused forever.

    `dispatch.sh` derives `CLAIM_TOKEN` from the window name (dispatch.sh:666),
    which is deterministic for a given issue/lane pairing -- a retried review
    recomputes the exact same token an earlier, now-finished dispatch already
    used. `claim_lane`'s task id is `ledger-claim:{lane}:{token}`
    (`CLAIM_TASK_PREFIX`), so that id is a PRIMARY KEY this table has already
    seen: the earlier dispatch's own `record_dispatch` cancelled that exact
    row via `_register_lane_tx`'s changed-identity path (a fresh nonce is
    minted every call), leaving it behind forever as `cancelled` rather than
    deleting it.

    The second `claim_lane` call's INSERT collides with that dead row and
    raises `IntegrityError`, which `claim_lane` swallows -- but the SELECT
    that follows finds no active row anywhere for the lane (there genuinely
    is none; the lane is free), so it reports `occupied` with `holder: None`.
    Measured on the live estate 2026-08-15: `release-lane-claim` cannot free
    a row that is not there to release, `reap-lane-claims` only ever touches
    `CLAIM_STATUS_RESERVED` rows and finds none, and the lane never comes
    back -- exactly "claim refused (occupied; no holder reported)" with every
    claim row already `cancelled` or `complete`.
    """

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.ledger = Ledger(Path(self._tmp.name))
        self.ledger.register_lane(
            lane="free-4", pane_id="%4", nonce="nonce-0", harness="claude",
            repo="/repo/app", server_id="socket:1", session_id="$0", command="claude.exe",
        )
        # A first, complete dispatch under the token a retry will reuse --
        # exactly dispatch.sh's own steps 4, 4.5 and 6, in order.
        first = self.ledger.claim_lane("free-4", token="as163-rev168", owner=claim_owner_token(4242))
        self.assertTrue(first["claimed"])
        self.ledger.commit_lane_claim("free-4", token="as163-rev168")
        self.ledger.record_dispatch(
            lane="free-4", pane_id="%4", nonce="nonce-1", harness="claude",
            repo="/repo/app", server_id="socket:1", session_id="$0", command="claude.exe",
            task_id="ad163-rev168", source_kind="issue", source_url="https://example/163",
            source_ref="163", summary="review #168", source_state="open",
            evidence=["claimed by dispatch.sh for lane free-4"],
        )
        self.ledger.complete("ad163-rev168", b"ok", pane_nonce="nonce-1")
        # The claim placeholder is closed, the real task is closed, and
        # nothing else is outstanding -- the lane reads free by every
        # measure this ledger has.
        self.assertEqual("cancelled", self.ledger.get_task("ledger-claim:free-4:as163-rev168")["status"])
        self.assertEqual("complete", self.ledger.get_task("ad163-rev168")["status"])
        self.assertTrue(self.ledger.lane_available("free-4"))

    def test_a_free_lane_is_still_claimable_under_a_reused_token(self):
        """The defect itself: a lane every read calls free refuses the write."""
        second = self.ledger.claim_lane("free-4", token="as163-rev168", owner=claim_owner_token(4343))
        self.assertTrue(second["claimed"], second)
        self.assertIsNone(second["reason"])
        self.assertFalse(self.ledger.lane_available("free-4"))

    def test_release_reports_truthfully_once_the_lane_is_actually_recoverable(self):
        """Acceptance criterion 4: `release-lane-claim` must not call a
        refusal "no reserved claim matched" once the row it names is the
        live reservation actually blocking dispatch."""
        self.ledger.claim_lane("free-4", token="as163-rev168", owner=claim_owner_token(4343))
        self.assertTrue(self.ledger.release_lane_claim("free-4", token="as163-rev168"))
        self.assertTrue(self.ledger.lane_available("free-4"))


