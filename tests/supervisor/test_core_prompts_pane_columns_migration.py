import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402


def _seed_pre_pane_columns_database(root: Path):
    """Build a ledger.sqlite3 with a `prompts` table in the exact pre-#755
    shape (no `tmux_pane`/`tmux_pane_target` columns), one row seeded, so
    `Ledger.__init__`'s own `_migrate_prompts_pane_columns` has something
    real to migrate. Built with a real `Ledger` first (so every OTHER
    table matches production) and `prompts` then rebuilt down to the older
    shape -- the same "build real, then narrow just the one table this
    migration touches" approach `_seed_pre_is_review_column_database`
    already uses for `source_tasks`."""
    root.mkdir(parents=True, exist_ok=True)
    Ledger(root, clock=lambda: 1_000)  # creates every table at its CURRENT shape

    connection = sqlite3.connect(root / "ledger.sqlite3")
    try:
        connection.execute("DROP TABLE prompts")
        connection.execute(
            """
            CREATE TABLE prompts (
                id TEXT PRIMARY KEY,
                at INTEGER NOT NULL,
                text_raw TEXT NOT NULL,
                text_clean TEXT,
                context TEXT NOT NULL,
                session TEXT,
                source_file TEXT
            )
            """
        )
        connection.execute(
            "INSERT INTO prompts (id, at, text_raw, context, session, source_file) VALUES (?, ?, ?, ?, ?, ?)",
            ("hp-pre-migration", 1_000, "a real directive", "ctx", "s1", None),
        )
        connection.commit()
    finally:
        connection.close()


class PromptsPaneColumnsMigrationTest(unittest.TestCase):
    """agent-supervisor#755 part B: `prompts.tmux_pane`/`tmux_pane_target`
    are added with `ALTER TABLE ... ADD COLUMN`, the same shape
    `_migrate_source_tasks_review_column` (#640) already uses -- see
    `_migrate_prompts_pane_columns`'s own docstring for why that suffices
    here (no CHECK constraint being widened, no rebuild needed)."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.root = Path(self.tempdir.name)
        _seed_pre_pane_columns_database(self.root)

    def _table_info(self, root=None):
        connection = sqlite3.connect((root or self.root) / "ledger.sqlite3")
        connection.row_factory = sqlite3.Row
        try:
            return {row["name"]: row for row in connection.execute("PRAGMA table_info(prompts)").fetchall()}
        finally:
            connection.close()

    def test_columns_are_absent_before_migration_runs(self):
        columns = self._table_info()
        self.assertNotIn("tmux_pane", columns)
        self.assertNotIn("tmux_pane_target", columns)

    def test_opening_migrates_the_columns_and_preserves_every_row(self):
        ledger = Ledger(self.root, clock=lambda: 2_000)

        columns = self._table_info()
        self.assertIn("tmux_pane", columns)
        self.assertIn("tmux_pane_target", columns)

        row = ledger.get_prompt("hp-pre-migration")
        self.assertIsNotNone(row)
        self.assertEqual("a real directive", row["text_raw"])
        self.assertIsNone(row["tmux_pane"])
        self.assertIsNone(row["tmux_pane_target"])

    def test_migration_failure_rolls_back_leaving_columns_absent(self):
        for failpoint in (
            "before_add_tmux_pane_column",
            "after_add_tmux_pane_column",
            "before_add_tmux_pane_target_column",
        ):
            with self.subTest(failpoint=failpoint):
                root = Path(tempfile.mkdtemp())
                self.addCleanup(lambda r=root: __import__("shutil").rmtree(r, ignore_errors=True))
                _seed_pre_pane_columns_database(root)

                with self.assertRaisesRegex(RuntimeError, failpoint):
                    Ledger(root, clock=lambda: 2_000, _migration_failpoint=failpoint)

                # `before_add_tmux_pane_target_column` fires AFTER tmux_pane
                # itself already landed in this transaction -- both columns
                # are added inside one `_transaction()`, so a rollback wipes
                # both, never leaves the table half-migrated.
                columns = self._table_info(root)
                self.assertNotIn("tmux_pane", columns)
                self.assertNotIn("tmux_pane_target", columns)

                connection = sqlite3.connect(root / "ledger.sqlite3")
                try:
                    self.assertEqual(1, connection.execute("SELECT COUNT(*) FROM prompts").fetchone()[0])
                finally:
                    connection.close()

                recovered = Ledger(root, clock=lambda: 3_000)
                self.assertIn("tmux_pane", self._table_info(root))
                self.assertEqual("a real directive", recovered.get_prompt("hp-pre-migration")["text_raw"])

    def test_fresh_ledger_already_carries_both_columns(self):
        fresh_root = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: __import__("shutil").rmtree(fresh_root, ignore_errors=True))
        Ledger(fresh_root, clock=lambda: 1_000)
        columns = self._table_info(fresh_root)
        self.assertIn("tmux_pane", columns)
        self.assertIn("tmux_pane_target", columns)

    def test_record_prompt_writes_and_round_trips_pane_columns(self):
        ledger = Ledger(self.root, clock=lambda: 2_000)
        ledger.record_prompt(
            "hp-with-pane", at=2_000, text_raw="tick text", context="ctx",
            tmux_pane="%22", tmux_pane_target="estate:1",
        )
        row = ledger.get_prompt("hp-with-pane")
        self.assertEqual("%22", row["tmux_pane"])
        self.assertEqual("estate:1", row["tmux_pane_target"])

    def test_record_prompt_defaults_pane_columns_to_null(self):
        ledger = Ledger(self.root, clock=lambda: 2_000)
        ledger.record_prompt("hp-no-pane", at=2_000, text_raw="a claude-print lane's turn", context="ctx")
        row = ledger.get_prompt("hp-no-pane")
        self.assertIsNone(row["tmux_pane"])
        self.assertIsNone(row["tmux_pane_target"])


if __name__ == "__main__":
    unittest.main()
