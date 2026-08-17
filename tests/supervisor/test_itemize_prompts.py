import sys
import tempfile
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import itemize_prompts  # noqa: E402
from core import Ledger  # noqa: E402


class ItemizePromptsTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.ledger = Ledger(self.tmp.name, clock=lambda: 1_000)
        self.ledger.record_prompt("p1", at=1_000, text_raw="make render LIVE", context="deciding render mode")
        self.ledger.record_prompt("p2", at=2_000, text_raw="unrelated turn", context="ctx")

    def test_extract_returns_only_unitemised_prompts(self):
        self.ledger.add_item("i-existing", prompt_id="p2", kind="directive", body="b", weight="hard")
        rows = itemize_prompts.extract(self.ledger)
        self.assertEqual(["p1"], [row["id"] for row in rows])

    def test_load_writes_items_from_a_judged_batch(self):
        judged = [{
            "prompt_id": "p1",
            "items": [
                {"kind": "parameter", "body": "render=LIVE", "weight": "hard"},
            ],
        }]
        written, skipped = itemize_prompts.load(judged, self.ledger)
        self.assertEqual((1, 0), (written, skipped))
        rows = self.ledger.read_prompt_view("live_parameters")
        self.assertEqual(["render=LIVE"], [row["body"] for row in rows])

    def test_load_is_idempotent_on_a_second_pass_over_the_same_judgement(self):
        judged = [{
            "prompt_id": "p1",
            "items": [{"kind": "parameter", "body": "render=LIVE", "weight": "hard"}],
        }]
        itemize_prompts.load(judged, self.ledger)
        written, skipped = itemize_prompts.load(judged, self.ledger)
        self.assertEqual((0, 1), (written, skipped))

    def test_load_never_calls_link_items(self):
        """agent-supervisor#303: conflicts reports recorded links only --
        `load()` has no code path that calls `Ledger.link_items` at all."""
        import inspect
        source = inspect.getsource(itemize_prompts.load)
        self.assertNotIn("link_items", source)


if __name__ == "__main__":
    unittest.main()
