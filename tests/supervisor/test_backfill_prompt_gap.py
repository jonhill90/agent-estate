import json
import sys
import tempfile
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import backfill_prompt_gap  # noqa: E402
from core import Ledger  # noqa: E402


def _line(role, text, at="2026-08-24T10:00:00Z"):
    return json.dumps({"timestamp": at, "message": {"role": role, "content": text}}) + "\n"


class CollectTests(unittest.TestCase):
    """`collect` must attach `project` from the transcript's own directory,
    never from `source_file` (agent-supervisor#696 -- that column is only a
    session UUID), and must respect the (since, until] window exactly."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.project_dir = self.root / "-Users-jon-source-repos-Personal-widget"
        self.project_dir.mkdir()

    def _write(self, name, *lines):
        (self.project_dir / name).write_text("".join(lines))

    def test_project_recovered_from_directory_not_source_file(self):
        self._write(
            "session-abc.jsonl",
            _line("assistant", "prior turn", at="2026-08-24T10:00:00Z"),
            _line("user", "do the thing", at="2026-08-24T10:00:05Z"),
        )
        rows = backfill_prompt_gap.collect(str(self.root), excludes=(), since=0, until=2_000_000_000)
        self.assertEqual(1, len(rows))
        self.assertEqual("-Users-jon-source-repos-Personal-widget", rows[0]["project"])
        self.assertEqual("session-abc.jsonl", rows[0]["source"])  # still just the basename

    def test_window_is_exclusive_lower_inclusive_upper(self):
        self._write(
            "session.jsonl",
            _line("user", "before window", at="2026-08-24T09:59:59Z"),
            _line("user", "at lower bound", at="2026-08-24T10:00:00Z"),
            _line("user", "inside window", at="2026-08-24T10:00:01Z"),
            _line("user", "at upper bound", at="2026-08-24T10:00:02Z"),
            _line("user", "after window", at="2026-08-24T10:00:03Z"),
        )
        from mine_prompts import _epoch
        lower = _epoch("2026-08-24T10:00:00Z")
        upper = _epoch("2026-08-24T10:00:02Z")
        rows = backfill_prompt_gap.collect(str(self.root), excludes=(), since=lower, until=upper)
        texts = [r["text"] for r in rows]
        self.assertEqual(["inside window", "at upper bound"], texts)


class StoreTests(unittest.TestCase):
    """Idempotent like `mine_prompts.store_rows`, plus the one thing that
    function does not do: populate `prompts.project` (no ledger API for it,
    so this goes through one explicit UPDATE -- see module docstring)."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.ledger = Ledger(self.tmp.name, clock=lambda: 1_000)
        # `project` column pre-exists on the live ledger but core.py's schema
        # never declares it (see module docstring) -- add it here the same
        # ad hoc way, so this test ledger has somewhere for the UPDATE to land.
        with self.ledger._locked(), self.ledger._transaction() as connection:
            connection.execute("ALTER TABLE prompts ADD COLUMN project TEXT")

    def _row(self):
        return {
            "at": "2026-08-24T10:00:05Z",
            "_epoch": 1787644805,
            "text": "do the thing",
            "typed": True,
            "source": "session-abc.jsonl",
            "context": "prior turn",
            "project": "-Users-jon-source-repos-Personal-widget",
        }

    def test_first_run_writes_row_and_project(self):
        written, skipped, oldest, newest = backfill_prompt_gap.store([self._row()], self.ledger, dry_run=False)
        self.assertEqual((1, 0), (written, skipped))
        from mine_prompts import _prompt_id
        prompt_id = _prompt_id(self._row())
        stored = self.ledger.get_prompt(prompt_id)
        self.assertEqual("do the thing", stored["text_raw"])
        self.assertEqual("-Users-jon-source-repos-Personal-widget", stored["project"])

    def test_second_run_is_a_no_op(self):
        backfill_prompt_gap.store([self._row()], self.ledger, dry_run=False)
        written, skipped, oldest, newest = backfill_prompt_gap.store([self._row()], self.ledger, dry_run=False)
        self.assertEqual((0, 1), (written, skipped))

    def test_dry_run_writes_nothing(self):
        written, skipped, oldest, newest = backfill_prompt_gap.store([self._row()], self.ledger, dry_run=True)
        self.assertEqual(1, written)
        from mine_prompts import _prompt_id
        prompt_id = _prompt_id(self._row())
        self.assertIsNone(self.ledger.get_prompt(prompt_id))


if __name__ == "__main__":
    unittest.main()
