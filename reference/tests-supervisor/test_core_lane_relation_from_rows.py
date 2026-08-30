import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import (  # noqa: E402
    lane_relation,
    lane_relation_from_rows,
)


class LaneRelationFromRowsTest(unittest.TestCase):
    """agent-supervisor#292: `lane_relation`'s string-shape check can never
    place a claude-print/pi-rpc lane (its id IS its task id -- no window to
    index) positive of anything. `lane_relation_from_rows` is the widening:
    identity from the ledger's own `lanes.pane_id` registry instead, which
    every transport writes at registration regardless of whether it has a
    tmux window at all."""

    def _row(self, pane_id, transport="send-keys"):
        return {"pane_id": pane_id, "transport": transport}

    def test_same_pane_id_is_the_same_lane(self):
        # The tmux pane id itself, unlike the lane id string, does not change
        # on a session rename -- so two rows recorded under different lane
        # STRINGS for the very same pane still resolve `same`.
        self.assertEqual(
            "same",
            lane_relation_from_rows(self._row("%12"), self._row("%12")),
        )

    def test_different_pane_id_is_positively_different(self):
        # The measured case: a tmux candidate against the claude-print author
        # of PR #288. Neither id parses as `<session>:<index>` on the
        # claude-print side, but both rows are known and their pane_ids
        # differ -- a claude-print lane's pane_id is `claude-print:<lane>`,
        # unique to the one process it names.
        self.assertEqual(
            "different",
            lane_relation_from_rows(
                self._row("%12", "send-keys"),
                self._row("claude-print:as284-finish-migration-b", "claude-print"),
            ),
        )

    def test_both_claude_print_lanes_with_different_pane_ids_are_different(self):
        # The other direction #292 requires tested: a claude-print lane
        # reviewing a claude-print-authored PR.
        self.assertEqual(
            "different",
            lane_relation_from_rows(
                self._row("claude-print:skills4-review", "claude-print"),
                self._row("claude-print:as284-finish-migration-b", "claude-print"),
            ),
        )

    def test_a_claude_print_lane_reviewing_itself_is_the_same_lane(self):
        self.assertEqual(
            "same",
            lane_relation_from_rows(
                self._row("claude-print:as284-finish-migration-b", "claude-print"),
                self._row("claude-print:as284-finish-migration-b", "claude-print"),
            ),
        )

    def test_a_missing_row_on_either_side_is_unknown_not_different(self):
        # Fail-closed: a lane the ledger has never heard of establishes
        # nothing, and admitting it as "different" is exactly the loosening
        # #292 refuses to do.
        self.assertEqual("unknown", lane_relation_from_rows(None, self._row("%12")))
        self.assertEqual("unknown", lane_relation_from_rows(self._row("%12"), None))
        self.assertEqual("unknown", lane_relation_from_rows(None, None))

    def test_an_empty_pane_id_is_unknown(self):
        self.assertEqual(
            "unknown",
            lane_relation_from_rows(self._row(""), self._row("%12")),
        )


