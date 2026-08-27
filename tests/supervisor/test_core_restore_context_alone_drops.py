import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402


class RestoreContextAloneDropsTest(unittest.TestCase):
    """agent-supervisor#652: an item the pre-#652 `reclassify_synthetic`
    dropped on `context` alone must come back out of `dropped` -- restored
    to `needs_review`, not silently left mis-classified forever just because
    the ledger already existed when the fix landed."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.root = Path(self.tempdir.name)

    def test_pre_652_context_alone_drop_is_restored_to_needs_review_on_next_open(self):
        ledger = Ledger(self.root, clock=lambda: 1_000)
        ledger.record_prompt(
            "p1", at=1_000, text_raw="Update the stale defect note in AGENTS.md "
                                      "referencing commit b00db9b.",
            context="[context undetermined: no prior assistant turn in this transcript file]",
        )
        # Simulate the pre-#652 write path directly: `add_item(status='dropped', ...)`
        # is exactly what `itemize_prompts.drop_noise` did before this fix pass.
        ledger.add_item(
            "it-real", prompt_id="p1", kind="directive",
            body="Update the stale defect note in AGENTS.md referencing commit b00db9b.",
            weight="hard", status="dropped",
            status_reason="agent-supervisor#583: synthetic eval-scenario fixture "
                           "(context='[context undetermined: no prior assistant turn "
                           "in this transcript file]')",
        )
        self.assertEqual("dropped", ledger.get_item("it-real")["status"])

        # Re-opening the SAME ledger runs the migration/backfill again --
        # this is the shape `restore.sh`/every other migration in this file
        # takes: a ledger that predates a fix gets corrected the next time
        # anything opens it, not only via a one-off manual script.
        reopened = Ledger(self.root, clock=lambda: 2_000)
        row = reopened.get_item("it-real")
        self.assertEqual("needs_review", row["status"])
        self.assertIn("652", row["status_reason"])
        # Judgement fields untouched.
        self.assertEqual("directive", row["kind"])
        self.assertEqual("hard", row["weight"])
        self.assertIn("it-real", [r["id"] for r in reopened.read_prompt_view("needs_review")])

    def test_restore_is_idempotent(self):
        ledger = Ledger(self.root, clock=lambda: 1_000)
        ledger.record_prompt("p1", at=1_000, text_raw="x", context="ctx")
        ledger.add_item(
            "it-real", prompt_id="p1", kind="directive", body="x", weight="hard", status="dropped",
            status_reason="agent-supervisor#583: synthetic eval-scenario fixture (context='ctx')",
        )
        Ledger(self.root, clock=lambda: 2_000)
        first = Ledger(self.root, clock=lambda: 3_000).get_item("it-real")
        second = Ledger(self.root, clock=lambda: 4_000).get_item("it-real")
        self.assertEqual(first["status"], second["status"])
        self.assertEqual(first["status_reason"], second["status_reason"])

    def test_a_confirmed_true_drop_unrelated_to_583_is_left_alone(self):
        """Only rows carrying the exact pre-#652 #583 reason prefix move --
        an ordinary text-shape drop (agent-supervisor#313, dispatch briefs
        etc.) is unaffected."""
        ledger = Ledger(self.root, clock=lambda: 1_000)
        ledger.record_prompt("p1", at=1_000, text_raw="x", context="ctx")
        ledger.add_item(
            "it-noise", prompt_id="p1", kind="thought", body="x", weight="retracted",
            status="dropped", status_reason="dispatch brief (claude-print contract line)",
        )
        reopened = Ledger(self.root, clock=lambda: 2_000)
        self.assertEqual("dropped", reopened.get_item("it-noise")["status"])


if __name__ == "__main__":
    unittest.main()

