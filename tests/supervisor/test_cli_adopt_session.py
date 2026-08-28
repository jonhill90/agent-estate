import contextlib
import io
import json
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import cli  # noqa: E402
from core import Ledger  # noqa: E402


class AdoptSessionCliTest(unittest.TestCase):
    """agent-supervisor#153: the write side, through `cli.main` -- this is
    what `bootstrap-session.sh` actually calls. Touches no tmux (the ledger
    write alone), so it needs no isolated socket; `session-state`'s
    real-tmux, real-drift coverage lives in
    tests/supervisor/test_session_supervision.sh instead, per this repo's
    own split between ledger-only Python tests and tmux-touching bash ones."""

    def _adopt(self, root, session, source=None):
        output = io.StringIO()
        args = ["--state-dir", root, "adopt-session", "--session", session]
        if source is not None:
            args += ["--source", source]
        with contextlib.redirect_stdout(output):
            rc = cli.main(args)
        self.assertEqual(0, rc, output.getvalue())
        return json.loads(output.getvalue())

    def test_adopt_session_records_a_ledger_row(self):
        with tempfile.TemporaryDirectory() as root:
            result = self._adopt(root, "agent-supervisor")

            self.assertEqual("agent-supervisor", result["session"])
            self.assertEqual("bootstrap-session.sh", result["source"])
            ledger = Ledger(Path(root))
            self.assertTrue(ledger.session_marked_supervised("agent-supervisor"))

    def test_status_exposes_the_adopted_sessions(self):
        with tempfile.TemporaryDirectory() as root:
            self._adopt(root, "agent-supervisor")

            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                rc = cli.main(["--state-dir", root, "status"])
            self.assertEqual(0, rc, output.getvalue())
            status = json.loads(output.getvalue())

            self.assertEqual(["agent-supervisor"], [row["session"] for row in status["sessions"]])
