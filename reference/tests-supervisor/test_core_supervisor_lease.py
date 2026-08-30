import os
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
)


class SupervisorLease(unittest.TestCase):
    """agent-dotfiles#238: WHICH process is the supervisor was never a
    recorded fact, only inferred from a tmux window index -- and on
    2026-08-12 a second, fully legitimate instance resumed elsewhere and
    dispatched the same five issues a first instance had just claimed.

    `take_supervisor_lease` is the recorded fact; `reap_stale_supervisor_lease`
    is what lets a legitimate restart reclaim a lease its predecessor never
    released -- a lease that outlives a crashed supervisor with no way back
    would be strictly worse than two dispatchers, the failure this whole
    mechanism exists to avoid.
    """

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.ledger = Ledger(Path(self._tmp.name))

    def test_a_fresh_lease_is_taken(self):
        result = self.ledger.take_supervisor_lease(owner="this-host:1111")
        self.assertEqual({"leased": True, "holder": "this-host:1111", "owner": "this-host:1111"}, result)
        self.assertEqual("this-host:1111", self.ledger.supervisor_lease()["owner"])

    def test_a_second_instance_is_refused_and_told_the_holder(self):
        """The load-bearing case: a second instance must be able to learn --
        from a recorded fact, not from where it is sitting -- that one
        already holds the role, and must refuse to proceed."""
        self.ledger.take_supervisor_lease(owner="this-host:1111")
        result = self.ledger.take_supervisor_lease(owner="this-host:2222")
        self.assertEqual({"leased": False, "holder": "this-host:1111", "owner": "this-host:2222"}, result)
        # The lease is untouched -- a refused take must not have side effects.
        self.assertEqual("this-host:1111", self.ledger.supervisor_lease()["owner"])

    def test_the_same_owner_re_taking_is_idempotent(self):
        """A later tick re-affirming its own, still-held lease is success,
        not a collision with itself."""
        self.ledger.take_supervisor_lease(owner="this-host:1111")
        result = self.ledger.take_supervisor_lease(owner="this-host:1111")
        self.assertEqual({"leased": True, "holder": "this-host:1111", "owner": "this-host:1111"}, result)

    def test_release_only_undoes_the_same_owners_lease(self):
        self.ledger.take_supervisor_lease(owner="this-host:1111")
        self.assertFalse(self.ledger.release_supervisor_lease(owner="this-host:2222"))
        self.assertIsNotNone(self.ledger.supervisor_lease())
        self.assertTrue(self.ledger.release_supervisor_lease(owner="this-host:1111"))
        self.assertIsNone(self.ledger.supervisor_lease())

    def test_reap_never_touches_a_lease_whose_owner_is_alive(self):
        """The one-way ratchet: reaping may only ever withhold. A false
        'dead' here would let a second instance steal a live supervisor's
        role out from under it."""
        self.ledger.take_supervisor_lease(owner="this-host:1111")
        self.assertIsNone(self.ledger.reap_stale_supervisor_lease(host="this-host", is_alive=lambda pid: True))
        self.assertIsNotNone(self.ledger.supervisor_lease())

    def test_reap_never_touches_a_lease_owned_by_another_host(self):
        self.ledger.take_supervisor_lease(owner="other-host:1111")
        self.assertIsNone(self.ledger.reap_stale_supervisor_lease(host="this-host", is_alive=lambda pid: False))
        self.assertIsNotNone(self.ledger.supervisor_lease())

    def test_reap_clears_a_lease_whose_owner_is_gone(self):
        self.ledger.take_supervisor_lease(owner="this-host:1111")
        reaped = self.ledger.reap_stale_supervisor_lease(host="this-host", is_alive=lambda pid: False)
        self.assertEqual("this-host:1111", reaped["owner"])
        self.assertEqual("1111", reaped["reaped_pid"])
        self.assertIsNone(self.ledger.supervisor_lease())

    def test_reap_of_nothing_held_is_a_no_op(self):
        self.assertIsNone(self.ledger.reap_stale_supervisor_lease(host="this-host", is_alive=lambda pid: False))

    def test_a_legitimate_restart_reclaims_a_lease_left_by_a_real_dead_process(self):
        """End to end, against a real process rather than a fake liveness
        check: a crashed supervisor's lease must not strand the estate --
        the exact property the brief asks be proven, not just asserted."""
        proc = subprocess.Popen([sys.executable, "-c", "pass"])
        proc.wait()  # reaped: proc.pid is now genuinely gone
        self.ledger.take_supervisor_lease(owner=claim_owner_token(proc.pid, host="this-host"))

        # A restart with a fresh pid is refused first -- the dead lease is
        # not silently bypassed, it must be reclaimed explicitly.
        restart_owner = claim_owner_token(os.getpid(), host="this-host")
        refused = self.ledger.take_supervisor_lease(owner=restart_owner)
        self.assertFalse(refused["leased"])

        reaped = self.ledger.reap_stale_supervisor_lease(host="this-host")
        self.assertIsNotNone(reaped)

        retaken = self.ledger.take_supervisor_lease(owner=restart_owner)
        self.assertTrue(retaken["leased"])
        self.assertEqual(restart_owner, self.ledger.supervisor_lease()["owner"])


