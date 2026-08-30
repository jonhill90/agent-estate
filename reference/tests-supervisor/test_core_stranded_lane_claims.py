import os
import socket
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import (  # noqa: E402
    Ledger,
    claim_owner_token,
    pid_is_alive,
)


class StrandedLaneClaims(unittest.TestCase):
    """agent-dotfiles#209: what happens to a `claim_lane` placeholder whose
    owning dispatcher died before it could release.

    `dispatch.sh`'s trap covers every exit a shell can observe. SIGKILL, an
    OOM kill and a host crash are not among them, and the placeholder they
    leave makes `lane_available` read False forever -- agent-dotfiles#102's
    failure shape (capacity silently falling to zero while lanes sit idle)
    reached through the mechanism built to prevent it.

    The reap is the only cover for that, so what it must NOT do matters at
    least as much as what it must: reaping a live dispatcher's claim would
    reopen the race #184 closed, which is why every ambiguous case below is
    asserted to leave the claim alone.
    """

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.ledger = Ledger(Path(self._tmp.name))
        self.ledger.register_lane(
            lane="free-9", pane_id="%9", nonce="nonce-9", harness="claude",
            repo="/repo/app", server_id="socket:1", session_id="$0", command="claude.exe",
        )

    def claim(self, token, *, pid, host="this-host"):
        return self.ledger.claim_lane("free-9", token=token, owner=claim_owner_token(pid, host=host))

    def test_a_claim_left_behind_holds_the_lane_shut(self):
        """The defect itself, stated as a test: no reap, no recovery."""
        self.assertTrue(self.ledger.lane_available("free-9"))
        self.assertTrue(self.claim("ad1-crash", pid=4242)["claimed"])
        self.assertFalse(self.ledger.lane_available("free-9"))
        # Nothing else in the ledger frees it -- this is what nine hand
        # reconciliations in two days were paying for.
        self.assertEqual([], self.ledger.reap_stale_lane_claims(host="this-host", is_alive=lambda pid: True))
        self.assertFalse(self.ledger.lane_available("free-9"))

    def test_reap_clears_a_claim_whose_owner_is_gone(self):
        self.claim("ad1-crash", pid=4242)
        reaped = self.ledger.reap_stale_lane_claims(host="this-host", is_alive=lambda pid: False)
        self.assertEqual(["ledger-claim:free-9:ad1-crash"], [row["id"] for row in reaped])
        self.assertEqual(["this-host:4242"], [row["owner"] for row in reaped])
        self.assertTrue(self.ledger.lane_available("free-9"))

    def test_reap_never_touches_a_claim_whose_owner_is_alive(self):
        """The one-way ratchet of #124/#126: this may only ever withhold."""
        self.claim("ad1-running", pid=4242)
        self.assertEqual([], self.ledger.reap_stale_lane_claims(host="this-host", is_alive=lambda pid: True))
        self.assertFalse(self.ledger.lane_available("free-9"))

    def test_reap_never_touches_a_claim_owned_by_another_host(self):
        """A pid is only meaningful on the machine that minted it. Judging a
        remote dispatcher's claim by whether some LOCAL process holds that
        number would reap live claims at random."""
        self.claim("ad1-elsewhere", pid=4242, host="other-host")
        self.assertEqual([], self.ledger.reap_stale_lane_claims(host="this-host", is_alive=lambda pid: False))
        self.assertFalse(self.ledger.lane_available("free-9"))

    def test_reap_never_touches_a_claim_with_no_recorded_owner(self):
        """Claims written before #209, or by any caller that omits `owner`,
        behave exactly as they did before it: automatic recovery does not
        apply to them, and they are not guessed at."""
        self.ledger.claim_lane("free-9", token="ad1-ownerless")
        self.assertEqual([], self.ledger.reap_stale_lane_claims(host="this-host", is_alive=lambda pid: False))
        self.assertFalse(self.ledger.lane_available("free-9"))

    def test_reap_never_touches_a_held_lane_from_a_failed_record(self):
        """`mark_lane_held` (#188) writes a DELIBERATE hold awaiting a human
        after a `record_dispatch` failure -- against a lane whose pane is
        live and working. Reaping one would hand a working lane to the next
        dispatcher, which is the exact incident #188 exists to prevent."""
        self.ledger.mark_lane_held("free-9", note="ledger record failed")
        self.assertFalse(self.ledger.lane_available("free-9"))
        self.assertEqual([], self.ledger.reap_stale_lane_claims(host="this-host", is_alive=lambda pid: False))
        self.assertFalse(self.ledger.lane_available("free-9"))

    def test_reap_never_touches_a_real_dispatch_task(self):
        self.ledger.reconstruct_task(
            task_id="ad700-real", source_kind="issue", source_url="https://example/700",
            source_ref="700", summary="real work", source_state="open",
            evidence=["claimed by dispatch.sh for lane free-9"], status="created", status_marker=None,
        )
        self.ledger.assign(task_id="ad700-real", lane="free-9", pane_nonce="nonce-9", summary="real work")
        self.assertEqual([], self.ledger.reap_stale_lane_claims(host="this-host", is_alive=lambda pid: False))
        self.assertFalse(self.ledger.lane_available("free-9"))

    def test_pid_liveness_resolves_every_ambiguity_as_alive(self):
        """`pid_is_alive` is deliberately asymmetric: a false 'dead' reopens
        #184's race, a false 'alive' only defers a cleanup."""
        self.assertTrue(pid_is_alive(os.getpid()))
        self.assertTrue(pid_is_alive(0))
        self.assertTrue(pid_is_alive(-1))
        self.assertTrue(pid_is_alive("not-a-pid"))
        self.assertTrue(pid_is_alive(None))

    def test_pid_liveness_reports_a_dead_child(self):
        """A real, definitely-finished process -- not a number picked for
        being unlikely to exist."""
        proc = subprocess.Popen([sys.executable, "-c", "pass"])
        proc.wait()
        # The zombie is reaped by wait(), so the pid is genuinely gone.
        self.assertFalse(pid_is_alive(proc.pid))

    def test_owner_token_carries_this_host_by_default(self):
        self.assertEqual(f"{socket.gethostname()}:77", claim_owner_token(77))


