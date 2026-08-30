import sys
import unittest
from pathlib import Path

SCRIPTS_DIR = Path(__file__).resolve().parents[2] / "scripts"
sys.path.insert(0, str(SCRIPTS_DIR))

import validate_repository as vr  # noqa: E402


class ValidateLaneviewStateMapsTest(unittest.TestCase):
    """agent-supervisor#105: this is what actually runs the guard the
    laneview renderers' comments cite -- see validate_repository.py's
    module docstring for why it lives here and not in agent-dotfiles."""

    def test_every_lanes_sh_state_is_named_in_every_renderer(self):
        errors = vr.validate_laneview_state_maps(warn=lambda msg: None)
        self.assertEqual(errors, [])

    def test_lanes_sh_state_extraction_finds_every_known_state(self):
        # Locks the extraction itself, not just today's clean result -- a
        # regex that silently matched nothing would make the test above
        # pass for the wrong reason.
        states = vr.lanes_sh_states()
        for expected in (
            "free", "busy", "hung", "dead", "menu-blocked", "text-blocked",
            "unsent", "service", "supervisor", "unknown", "scrolled",
            "stale", "broken", "never-busy",
        ):
            self.assertIn(expected, states)

    def test_discovers_all_shipped_renderers(self):
        names = {p.name for p in vr.renderer_scripts()}
        self.assertEqual(
            names, {"text.sh", "opensessions.sh", "tui.sh", "dock.sh"}
        )

    def test_a_state_missing_from_a_renderers_map_is_reported_by_name(self):
        # #231's own failure shape, reproduced without editing the real
        # files: a state present in lanes_sh_states() but absent from a
        # renderer's own map must be named in that renderer's error.
        text_sh = next(p for p in vr.renderer_scripts() if p.name == "text.sh")
        mapped = vr.renderer_mapped_states(text_sh)
        self.assertNotIn("nonexistent-state", mapped)

    def test_a_file_with_no_dict_or_case_shape_maps_to_no_states(self):
        # laneview/README.md rule 4: a renderer this function can't read a
        # map from is warned, not failed -- this is the shape that path
        # detects. This test file itself has neither a dict-literal glyph
        # map nor a bash case statement, so it stands in for that renderer.
        self.assertEqual(vr.renderer_mapped_states(Path(__file__)), set())


if __name__ == "__main__":
    unittest.main()
