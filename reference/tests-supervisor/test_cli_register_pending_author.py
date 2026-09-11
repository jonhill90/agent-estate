import io
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "reference" / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import cli  # noqa: E402
from core import Ledger  # noqa: E402


class RegisterPendingAuthorCommandTest(unittest.TestCase):
    """agent-estate#1395/#1394 (wire-author-registration fix pass): the bash
    side of `register_pending_author`. This is the ONE choke point all five
    bash call sites (`director-loop.sh`, `heartbeat.sh`, `quota-watch.sh`,
    `director-route.sh`, `watchdog.sh`) share -- tested thoroughly here once,
    rather than mocking a tmux server five times for the same underlying
    call."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.root = Path(self.tempdir.name)

    def _run(self, argv, stdin_text):
        with mock.patch.object(sys, "stdin", io.StringIO(stdin_text)):
            return cli.main(["--state-dir", str(self.root), *argv])

    def test_registers_text_from_stdin_as_supervisor(self):
        rc = self._run(["register-pending-author", "--author", "supervisor"], "the Director's own tick text")
        self.assertEqual(0, rc)
        ledger = Ledger(self.root)
        self.assertEqual("supervisor", ledger.consume_pending_author("the Director's own tick text"))

    def test_registers_text_from_stdin_as_director(self):
        rc = self._run(["register-pending-author", "--author", "director"], "a director-authored brief")
        self.assertEqual(0, rc)
        ledger = Ledger(self.root)
        self.assertEqual("director", ledger.consume_pending_author("a director-authored brief"))

    def test_rejects_jon_at_the_argparse_layer_before_touching_the_ledger(self):
        """MUTATION-CHECK: `--author jon` must never reach `register_pending_author`
        at all -- argparse's own `choices=` refuses it first (belt and
        suspenders on top of the Python-level ValueError `register_pending_author`
        itself raises), so a caller typo can't even open the ledger."""
        with self.assertRaises(SystemExit):
            with mock.patch.object(sys, "stdin", io.StringIO("x")):
                cli.main(["--state-dir", str(self.root), "register-pending-author", "--author", "jon"])
        # Confirm nothing was written -- the ledger for this test root was
        # never even opened by the rejected call.
        self.assertFalse((self.root / "ledger.sqlite3").exists())

    def test_empty_stdin_refuses_rather_than_registering_nothing_silently(self):
        with self.assertRaisesRegex(ValueError, "stdin was empty"):
            self._run(["register-pending-author", "--author", "supervisor"], "")

    def test_multiline_text_survives_the_stdin_round_trip(self):
        """The whole reason this goes over stdin, not argv (agent-estate#1395):
        a real brief/tick is many lines and may contain shell-special
        characters. Confirm a real multi-line message with a `$`, backticks
        and quotes round-trips byte-for-byte."""
        text = "line one\nline two with $VAR and `backticks` and \"quotes\"\nline three"
        rc = self._run(["register-pending-author", "--author", "supervisor"], text)
        self.assertEqual(0, rc)
        ledger = Ledger(self.root)
        self.assertEqual("supervisor", ledger.consume_pending_author(text))


if __name__ == "__main__":
    unittest.main()
