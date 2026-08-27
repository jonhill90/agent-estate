import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import lane_population  # noqa: E402


class LanePopulationTest(unittest.TestCase):
    """agent-supervisor#292 item 3: naming which population a lane is in, so
    a refusal message can say WHY two lanes could not be told apart instead
    of implying a rename problem that was never the cause."""

    def test_send_keys_and_acp_rows_are_tmux(self):
        self.assertEqual("tmux", lane_population("t:3", {"transport": "send-keys"}))
        self.assertEqual("tmux", lane_population("t:3", {"transport": "acp"}))

    def test_claude_print_and_pi_rpc_rows_name_themselves(self):
        self.assertEqual(
            "claude-print",
            lane_population("as284-finish-migration-b", {"transport": "claude-print"}),
        )
        self.assertEqual("pi-rpc", lane_population("as99-task", {"transport": "pi-rpc"}))

    def test_no_row_falls_back_to_id_shape(self):
        self.assertEqual("tmux", lane_population("t:3", None))
        self.assertEqual("off-pane", lane_population("as284-finish-migration-b", None))
        self.assertEqual("off-pane", lane_population("lane/89-rev95", None))


# agent-supervisor#153: this table is a DECISION record (WE adopted this
# session), never a measurement -- see the schema comment in core.py for the
# authorship-test reasoning. These tests cover the ledger half only; the
# tri-state read that also checks live tmux existence (`session_state`) is
# covered in test_cli.py, since it lives there.
