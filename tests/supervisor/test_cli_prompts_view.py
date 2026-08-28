import contextlib
import io
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import cli  # noqa: E402
from core import Ledger  # noqa: E402


class PromptsViewCliTest(unittest.TestCase):
    """agent-supervisor#303: `cli.py prompts <view>` -- a table for a human
    to read (Jon reading `unacknowledged` first), unlike every other command
    here which prints one JSON line for a script."""

    def test_rejects_an_unknown_view_before_touching_the_ledger(self):
        with tempfile.TemporaryDirectory() as root:
            with self.assertRaises(SystemExit):
                cli.main(["--state-dir", root, "prompts", "not-a-real-view"])

    def test_unacknowledged_prints_as_a_table_with_a_header(self):
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.record_prompt("p1", at=1_000, text_raw="raw", context="ctx")
            ledger.add_item("i1", prompt_id="p1", kind="directive", body="do it", weight="hard")

            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                rc = cli.main(["--state-dir", root, "prompts", "unacknowledged"])
            self.assertEqual(0, rc)
            lines = output.getvalue().splitlines()
            self.assertIn("id", lines[0])
            self.assertIn("i1", lines[2])

    def test_an_empty_view_says_so_rather_than_printing_nothing(self):
        with tempfile.TemporaryDirectory() as root:
            Ledger(Path(root))
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                rc = cli.main(["--state-dir", root, "prompts", "open_questions"])
            self.assertEqual(0, rc)
            self.assertEqual("(no rows)", output.getvalue().strip())
