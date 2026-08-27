import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import (  # noqa: E402
    cross_namespace_lane_relation,
    daemon_lane_verified,
    is_daemon_shaped,
)


class DaemonNamespaceTest(unittest.TestCase):
    """agent-supervisor#605: `daemon`/`d-<task>` (EnsureLane,
    daemon/internal/ledger/ledger.go) and `<session>:<index>` (a tmux lane)
    are disjoint namespaces -- a tmux lane has no write path into the
    daemon's own author-lane field. `cross_namespace_lane_relation` is the
    narrow widening: `different` for a verified daemon lane against a
    genuine tmux lane, `None` (fall through, unchanged) for everything
    else, including daemon-vs-daemon."""

    def _daemon_row(self, pane_id=""):
        # The EXACT signature `EnsureLane` writes -- nothing else in this
        # estate ever produces this combination.
        return {"server_id": "supervisord", "transport": "claude-print", "pane_id": pane_id}

    def _tmux_row(self, pane_id):
        return {"server_id": "srv", "transport": "send-keys", "pane_id": pane_id}

    def test_daemon_is_shaped_correctly(self):
        self.assertTrue(is_daemon_shaped("daemon"))
        self.assertTrue(is_daemon_shaped("d-as605-daemon-ns"))
        self.assertFalse(is_daemon_shaped("estate:5"))
        self.assertFalse(is_daemon_shaped("dashboard:3"))  # starts with "d" but not "d-"
        self.assertFalse(is_daemon_shaped(None))
        self.assertFalse(is_daemon_shaped(""))

    def test_daemon_lane_verified_requires_the_exact_ensurelane_signature(self):
        self.assertTrue(daemon_lane_verified("daemon", self._daemon_row()))
        self.assertTrue(daemon_lane_verified("d-as605-daemon-ns", self._daemon_row()))
        # Shape alone, no ledger row at all -- a hand-typed trailer with
        # nothing behind it.
        self.assertFalse(daemon_lane_verified("daemon", None))
        # A row exists but doesn't carry supervisord's own signature -- e.g.
        # someone registered a lane literally named "daemon" through the
        # ordinary tmux path.
        self.assertFalse(daemon_lane_verified("daemon", self._tmux_row("%12")))
        # A row with the right transport but a nonempty pane_id -- not what
        # EnsureLane ever writes.
        self.assertFalse(daemon_lane_verified("daemon", {**self._daemon_row(), "pane_id": "%1"}))
        # Not daemon-shaped at all.
        self.assertFalse(daemon_lane_verified("estate:5", self._daemon_row()))

    def test_daemon_author_vs_tmux_reviewer_resolves_different(self):
        self.assertEqual(
            "different",
            cross_namespace_lane_relation("daemon", self._daemon_row(), "estate:5", self._tmux_row("%39")),
        )
        # Symmetric: reviewer first, daemon second.
        self.assertEqual(
            "different",
            cross_namespace_lane_relation("estate:3", self._tmux_row("%12"), "d-as603-fix", self._daemon_row()),
        )

    def test_unverified_daemon_shaped_id_does_not_resolve(self):
        # A hand-typed `Author-Lane: daemon` trailer with no real ledger row
        # behind it must still fall through to `unknown` -- this function
        # must not manufacture "different" from the string alone.
        self.assertIsNone(
            cross_namespace_lane_relation("daemon", None, "estate:5", self._tmux_row("%39"))
        )
        self.assertIsNone(
            cross_namespace_lane_relation("daemon", self._tmux_row("%1"), "estate:5", self._tmux_row("%39"))
        )

    def test_daemon_vs_daemon_is_out_of_scope(self):
        # Same-namespace comparison stays with the existing machinery --
        # this function must never call two daemon lanes "different".
        self.assertIsNone(
            cross_namespace_lane_relation("d-as603-fix", self._daemon_row(), "d-as604-fix", self._daemon_row())
        )
        self.assertIsNone(
            cross_namespace_lane_relation("daemon", self._daemon_row(), "daemon", self._daemon_row())
        )

    def test_neither_side_daemon_is_out_of_scope(self):
        self.assertIsNone(
            cross_namespace_lane_relation("estate:3", self._tmux_row("%1"), "estate:5", self._tmux_row("%39"))
        )

    def test_daemon_vs_tmux_lane_with_no_resolvable_pane_id_stays_unresolved(self):
        # The tmux side must have a REAL, resolvable pane id -- an
        # unregistered or empty-pane_id "tmux" candidate establishes
        # nothing either.
        self.assertIsNone(
            cross_namespace_lane_relation("daemon", self._daemon_row(), "estate:5", None)
        )
        self.assertIsNone(
            cross_namespace_lane_relation("daemon", self._daemon_row(), "estate:5", self._tmux_row(""))
        )

    def test_daemon_vs_a_non_tmux_shaped_id_stays_unresolved(self):
        # The non-daemon side must actually be tmux-shaped (`LANE_ID_RE`) --
        # a claude-print/pi-rpc lane id compared against daemon is a
        # different open question this function does not answer.
        self.assertIsNone(
            cross_namespace_lane_relation(
                "daemon", self._daemon_row(), "as284-finish-migration-b",
                {"server_id": "srv", "transport": "claude-print", "pane_id": "claude-print:as284-finish-migration-b"},
            )
        )


