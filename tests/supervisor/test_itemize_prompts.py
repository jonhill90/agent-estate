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


class SyntheticProvenanceTests(unittest.TestCase):
    """agent-supervisor#583: eval-scenario fixture prompts, itemised as if Jon
    had typed them, keyed structurally on `context` -- never on how the text
    reads, per the issue's own point that a well-written fixture is
    indistinguishable from a real directive by content alone."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.ledger = Ledger(self.tmp.name, clock=lambda: 1_000)

    def test_drop_noise_drops_a_known_eval_fixture_prompt(self):
        """Mutation direction 1: a known eval-fixture-shaped prompt (names a
        file, `reconcile.py`, that exists only under
        skills/*/references/eval-scenario*/) must be dropped when its
        transcript carries no prior-turn context."""
        self.ledger.record_prompt(
            "p1", at=1_000, context=itemize_prompts.CONTEXT_UNDETERMINED,
            text_raw="Find and fix the bug in reconcile.py that let a mid-reconcile "
                     "crash leave some claims marked released while their result "
                     "files are missing.",
        )
        dropped, kept = itemize_prompts.drop_noise(self.ledger)
        self.assertEqual((1, 0), (dropped, kept))
        item = self.ledger.get_item(itemize_prompts._item_id(
            "p1", 0, f"noise:{itemize_prompts.SYNTHETIC_REASON}"))
        self.assertIsNotNone(item)
        self.assertEqual("dropped", item["status"])
        self.assertIn("583", item["status_reason"])
        self.assertEqual([], self.ledger.read_prompt_view("unacknowledged"))

    def test_drop_noise_keeps_a_real_directive_with_the_same_shape_of_content(self):
        """Mutation direction 2: a real operator directive -- same
        directive-shaped phrasing naming a real file -- must survive when its
        transcript carries genuine prior-turn context. Content alone must
        not be what trips the filter."""
        self.ledger.record_prompt(
            "p2", at=2_000, context="deciding how the transcript-mining pass should run",
            text_raw="Find and fix the bug in send_input.sh that drops keystrokes "
                     "when the pane is scrolled.",
        )
        dropped, kept = itemize_prompts.drop_noise(self.ledger)
        self.assertEqual((0, 1), (dropped, kept))
        rows = itemize_prompts.extract(self.ledger)
        self.assertEqual(["p2"], [row["id"] for row in rows])

    def test_drop_noise_on_synthetic_fixture_is_idempotent(self):
        self.ledger.record_prompt(
            "p1", at=1_000, context=itemize_prompts.CONTEXT_UNDETERMINED,
            text_raw="Review finalize.py's two-write crash sequence.",
        )
        first = itemize_prompts.drop_noise(self.ledger)
        second = itemize_prompts.drop_noise(self.ledger)
        self.assertEqual((1, 0), first)
        self.assertEqual((0, 0), second)

    def test_reclassify_synthetic_drops_an_already_itemised_open_item(self):
        """A prompt itemised BEFORE this filter existed (already has an open
        `directive`/`hard` item) gets that item corrected to dropped, not
        deleted and not duplicated with a second item."""
        self.ledger.record_prompt(
            "p1", at=1_000, context=itemize_prompts.CONTEXT_UNDETERMINED,
            text_raw="Review finalize.py's two-write crash sequence and fix "
                     "anything that could leave the record wrong.",
        )
        self.ledger.add_item(
            "it-preexisting", prompt_id="p1", kind="directive",
            body="Review finalize.py's two-write crash sequence.", weight="hard",
        )
        reclassified, kept = itemize_prompts.reclassify_synthetic(self.ledger)
        self.assertEqual((1, 0), (reclassified, kept))
        item = self.ledger.get_item("it-preexisting")
        self.assertEqual("dropped", item["status"])
        self.assertIn("583", item["status_reason"])
        # Judgement fields are untouched -- only status/status_reason changed.
        self.assertEqual("directive", item["kind"])
        self.assertEqual("hard", item["weight"])
        self.assertEqual([], self.ledger.read_prompt_view("unacknowledged"))

    def test_reclassify_synthetic_leaves_a_real_open_item_alone(self):
        self.ledger.record_prompt(
            "p2", at=2_000, context="a watchdog report from earlier in the session",
            text_raw="the worktree sweep should run by content, not ancestry",
        )
        self.ledger.add_item(
            "it-real", prompt_id="p2", kind="question",
            body="should the worktree sweep run by content or ancestry", weight="hard",
        )
        reclassified, kept = itemize_prompts.reclassify_synthetic(self.ledger)
        self.assertEqual((0, 1), (reclassified, kept))
        item = self.ledger.get_item("it-real")
        self.assertEqual("open", item["status"])

    def test_reclassify_synthetic_is_idempotent(self):
        self.ledger.record_prompt(
            "p1", at=1_000, context=itemize_prompts.CONTEXT_UNDETERMINED,
            text_raw="Confirm the credential check in check-credential.sh.",
        )
        self.ledger.add_item("it-1", prompt_id="p1", kind="directive", body="b", weight="hard")
        first = itemize_prompts.reclassify_synthetic(self.ledger)
        second = itemize_prompts.reclassify_synthetic(self.ledger)
        self.assertEqual((1, 0), first)
        self.assertEqual((0, 0), second)


if __name__ == "__main__":
    unittest.main()
