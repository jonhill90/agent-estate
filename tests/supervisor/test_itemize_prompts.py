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


class DropNoiseTests(unittest.TestCase):
    """agent-supervisor#313: FILTER NON-JON TEXT FIRST, mechanically, no model."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.ledger = Ledger(self.tmp.name, clock=lambda: 1_000)

    def test_dispatch_brief_is_dropped_not_extracted(self):
        self.ledger.record_prompt(
            "p1", at=1_000, context="dispatch",
            text_raw="Read /tmp/brief.md and do exactly what it says. "
                     "That file is your complete brief.",
        )
        self.ledger.record_prompt("p2", at=2_000, text_raw="make render LIVE", context="ctx")

        dropped, kept = itemize_prompts.drop_noise(self.ledger)
        self.assertEqual((1, 1), (dropped, kept))

        rows = itemize_prompts.extract(self.ledger)
        self.assertEqual(["p2"], [row["id"] for row in rows])

    def test_dropped_row_carries_a_reason_and_never_reaches_open_views(self):
        self.ledger.record_prompt(
            "p1", at=1_000, context="cron", text_raw="Supervisor loop tick. Follow loop-tick.md.",
        )
        itemize_prompts.drop_noise(self.ledger)

        self.assertEqual([], self.ledger.read_prompt_view("unacknowledged"))
        self.assertEqual([], self.ledger.read_prompt_view("live_parameters"))
        item = self.ledger.get_item(itemize_prompts._item_id(
            "p1", 0, "noise:loop-tick cron text (scripts/supervisor/loop-tick.md)"))
        self.assertIsNotNone(item)
        self.assertEqual("dropped", item["status"])
        self.assertTrue(item["status_reason"])

    def test_drop_noise_is_idempotent(self):
        self.ledger.record_prompt(
            "p1", at=1_000, context="ctx", text_raw="## Context Usage\n\n**Model:** x",
        )
        first = itemize_prompts.drop_noise(self.ledger)
        second = itemize_prompts.drop_noise(self.ledger)
        self.assertEqual((1, 0), first)
        self.assertEqual((0, 0), second)

    def test_jon_text_that_merely_mentions_a_brief_is_kept(self):
        self.ledger.record_prompt(
            "p1", at=1_000, context="ctx",
            text_raw="did you read the brief I sent? what did it say about scope",
        )
        dropped, kept = itemize_prompts.drop_noise(self.ledger)
        self.assertEqual((0, 1), (dropped, kept))


if __name__ == "__main__":
    unittest.main()
