import json
import os
import socket
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))


class SupervisorLeaseCliTest(unittest.TestCase):
    """agent-dotfiles#238, driven as a subprocess -- `dispatch.sh` and
    `loop-tick.md` both reach this through `cli.py`, never through
    `core.Ledger` directly, so a test that only imports `core` would miss a
    subcommand the parser never learned (the exact gap #144 found in
    `cancel-open-task` before it had a caller)."""

    def _run(self, root, *args):
        return subprocess.run(
            [sys.executable, str(SUPERVISOR_DIR / "cli.py"), "--state-dir", str(root), *args],
            capture_output=True,
            text=True,
            timeout=30,
        )

    def test_take_is_runnable_and_records_the_owner(self):
        with tempfile.TemporaryDirectory() as root:
            proc = self._run(root, "take-supervisor-lease", "--owner-pid", "1111")
            self.assertEqual(0, proc.returncode, proc.stderr)
            body = json.loads(proc.stdout)
            self.assertTrue(body["leased"])
            self.assertEqual(f"{socket.gethostname()}:1111", body["owner"])

    def test_a_second_instance_is_refused_and_told_the_holder(self):
        with tempfile.TemporaryDirectory() as root:
            self._run(root, "take-supervisor-lease", "--owner-pid", "1111")
            proc = self._run(root, "take-supervisor-lease", "--owner-pid", "2222")
            self.assertEqual(0, proc.returncode, proc.stderr)
            body = json.loads(proc.stdout)
            self.assertFalse(body["leased"])
            self.assertEqual(f"{socket.gethostname()}:1111", body["holder"])

    def test_status_reports_the_current_holder(self):
        with tempfile.TemporaryDirectory() as root:
            self._run(root, "take-supervisor-lease", "--owner-pid", "1111")
            proc = self._run(root, "supervisor-lease")
            self.assertEqual(0, proc.returncode, proc.stderr)
            body = json.loads(proc.stdout)
            self.assertTrue(body["held"])
            self.assertEqual(f"{socket.gethostname()}:1111", body["owner"])

    def test_reap_leaves_a_live_owners_lease_alone(self):
        with tempfile.TemporaryDirectory() as root:
            self._run(root, "take-supervisor-lease", "--owner-pid", str(os.getpid()))
            proc = self._run(root, "reap-supervisor-lease")
            self.assertEqual(0, proc.returncode, proc.stderr)
            self.assertIsNone(json.loads(proc.stdout)["reaped"])
            self.assertTrue(json.loads(self._run(root, "supervisor-lease").stdout)["held"])

    def test_a_restart_reaps_a_dead_owners_lease_and_retakes_it(self):
        """The property the brief asks be proven end to end: a legitimate
        restart must be able to reclaim a lease its predecessor never
        released -- otherwise a crashed supervisor strands the estate,
        strictly worse than two dispatchers."""
        with tempfile.TemporaryDirectory() as root:
            dead = subprocess.Popen([sys.executable, "-c", "pass"])
            dead.wait()
            self.assertEqual(0, self._run(root, "take-supervisor-lease", "--owner-pid", str(dead.pid)).returncode)

            refused = self._run(root, "take-supervisor-lease", "--owner-pid", str(os.getpid()))
            self.assertFalse(json.loads(refused.stdout)["leased"])

            reap = self._run(root, "reap-supervisor-lease")
            self.assertEqual(0, reap.returncode, reap.stderr)
            self.assertIsNotNone(json.loads(reap.stdout)["reaped"])

            retaken = self._run(root, "take-supervisor-lease", "--owner-pid", str(os.getpid()))
            self.assertTrue(json.loads(retaken.stdout)["leased"])

    def test_release_only_undoes_the_same_owners_lease(self):
        with tempfile.TemporaryDirectory() as root:
            self._run(root, "take-supervisor-lease", "--owner-pid", "1111")
            wrong = self._run(root, "release-supervisor-lease", "--owner-pid", "2222")
            self.assertFalse(json.loads(wrong.stdout)["released"])
            self.assertTrue(json.loads(self._run(root, "supervisor-lease").stdout)["held"])

            right = self._run(root, "release-supervisor-lease", "--owner-pid", "1111")
            self.assertTrue(json.loads(right.stdout)["released"])
            self.assertFalse(json.loads(self._run(root, "supervisor-lease").stdout)["held"])
