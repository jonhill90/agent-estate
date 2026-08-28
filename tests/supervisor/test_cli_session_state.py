import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import cli  # noqa: E402
from core import Ledger  # noqa: E402


class FakeSessionTransport:
    """Stands in for `TmuxTransport.session_exists` -- `session_state`'s
    only tmux touch."""

    def __init__(self, existing_sessions):
        self._existing = set(existing_sessions)
        self.calls = []

    def session_exists(self, session):
        self.calls.append(session)
        return session in self._existing


class RaisingLedger:
    """A ledger whose `session_marked_supervised` always raises -- stands in
    for a locked ledger, a corrupt file, or an old ledger missing the
    `sessions` table. Used by the acceptance-test mutation below."""

    def session_marked_supervised(self, session):
        raise sqlite3.OperationalError("no such table: sessions")


class SessionStateTest(unittest.TestCase):
    """agent-supervisor#153: `session_state` is the one three-state answer
    every caller is meant to use instead of re-deriving supervision from
    `lanes.lane` strings. Exercised directly (no real tmux), the same way
    `LaneFreeTest` exercises `lane_free`."""

    def test_a_session_never_adopted_is_unknown(self):
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            transport = FakeSessionTransport(["Hill90"])

            self.assertEqual("unknown", cli.session_state(ledger, transport, session="Hill90"))

    def test_an_adopted_session_is_supervised(self):
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.adopt_session("agent-supervisor")
            transport = FakeSessionTransport(["agent-supervisor"])

            self.assertEqual("supervised", cli.session_state(ledger, transport, session="agent-supervisor"))

    # #153's own measured drift: a ledger row for a session that no longer
    # exists must not read as supervised -- there is nothing to act on.
    def test_an_adopted_session_that_no_longer_exists_is_unknown_not_supervised(self):
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.adopt_session("agent-dotfiles")  # #153's own stale example
            transport = FakeSessionTransport([])  # tmux has no such session

            self.assertEqual("unknown", cli.session_state(ledger, transport, session="agent-dotfiles"))

    def test_the_ledger_is_not_even_consulted_when_the_session_does_not_exist(self):
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.adopt_session("agent-dotfiles")
            transport = FakeSessionTransport([])

            cli.session_state(ledger, transport, session="agent-dotfiles")

            self.assertEqual(["agent-dotfiles"], transport.calls)

    # THIS IS THE ACCEPTANCE TEST (as153-brief.md item 3): break the marker
    # read and confirm the result degrades to unsupervised (`unknown`),
    # never to `supervised`.
    def test_a_broken_marker_read_degrades_to_unknown_never_to_supervised(self):
        transport = FakeSessionTransport(["Hill90"])

        result = cli.session_state(RaisingLedger(), transport, session="Hill90")

        self.assertEqual("unknown", result)
