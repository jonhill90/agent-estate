import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
from tests.supervisor.test_core_helpers import MutableClock  # noqa: E402


class SessionSupervisionTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.clock = MutableClock()
        self.ledger = Ledger(Path(self.tempdir.name), clock=self.clock)

    def test_a_session_with_no_row_is_not_marked_supervised(self):
        self.assertFalse(self.ledger.session_marked_supervised("Hill90"))

    def test_adopt_session_marks_it_supervised(self):
        self.ledger.adopt_session("agent-supervisor")
        self.assertTrue(self.ledger.session_marked_supervised("agent-supervisor"))

    def test_adopt_session_is_idempotent_and_refreshes_the_timestamp(self):
        self.clock.value = 1000
        first = self.ledger.adopt_session("agent-supervisor")
        self.clock.value = 2000
        second = self.ledger.adopt_session("agent-supervisor")
        self.assertEqual(1000, first["supervised_at"])
        self.assertEqual(2000, second["supervised_at"])
        self.assertEqual(1, len(self.ledger.list_sessions()))

    def test_adopting_one_session_does_not_mark_another(self):
        self.ledger.adopt_session("agent-supervisor")
        self.assertFalse(self.ledger.session_marked_supervised("Hill90"))

    def test_list_sessions_reflects_every_adopted_session(self):
        self.ledger.adopt_session("agent-supervisor")
        self.ledger.adopt_session("agent-tui")
        names = sorted(row["session"] for row in self.ledger.list_sessions())
        self.assertEqual(["agent-supervisor", "agent-tui"], names)

    # #153's own example of stale ledger knowledge: a row can exist for a
    # session that is long gone. The ledger method itself has no opinion on
    # that -- it only answers "did we ever adopt this name" -- which is
    # exactly why `session_state` in cli.py must ALSO check tmux before
    # calling anything supervised (covered in test_cli.py).
    def test_a_row_survives_regardless_of_whether_the_session_still_exists(self):
        self.ledger.adopt_session("agent-dotfiles")
        self.assertTrue(self.ledger.session_marked_supervised("agent-dotfiles"))


