import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import lane_relation  # noqa: E402


class LaneRelationTest(unittest.TestCase):
    """agent-supervisor#108: a lane id embeds the session's NAME, and renaming
    the session renames every lane at once without moving a single window. The
    comparison this guard rests on has to answer for the window, and has to
    have somewhere to put "cannot tell" other than "different"."""

    def test_identical_ids_are_the_same_lane(self):
        self.assertEqual("same", lane_relation("agent-supervisor:3", "agent-supervisor:3"))

    def test_a_renamed_session_is_still_the_same_window(self):
        # The measured pair from the incident: 526 rows say the first, the
        # lanes dispatched since say the second, and they are one window.
        self.assertEqual("same", lane_relation("agent-dotfiles:3", "agent-supervisor:3"))

    def test_different_indices_are_different_windows_whatever_the_session(self):
        self.assertEqual("different", lane_relation("agent-dotfiles:3", "agent-supervisor:4"))
        self.assertEqual("different", lane_relation("t:3", "t:4"))

    def test_a_leading_zero_does_not_invent_a_second_window(self):
        self.assertEqual("same", lane_relation("t:03", "t:3"))

    def test_an_unparseable_id_is_unknown_not_different(self):
        # The `Review-Lane:` stamp on PR #95 was literally `lane/89-rev95` --
        # a branch name, not a lane id. Nothing about it establishes that the
        # reviewer is a different window, and `unknown` is the only answer
        # that does not launder that into independence.
        self.assertEqual("unknown", lane_relation("agent-dotfiles:3", "lane/89-rev95"))
        self.assertEqual("unknown", lane_relation("free-3", "t:3"))
        self.assertEqual("unknown", lane_relation("t:3", ""))
        self.assertEqual("unknown", lane_relation(None, "t:3"))
        self.assertEqual("unknown", lane_relation("t:3", "t:x"))


