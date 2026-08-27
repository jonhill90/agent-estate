import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
from tests.supervisor.test_core_helpers import _seed_pre_is_review_column_database  # noqa: E402


class SourceTasksReviewColumnMigrationTest(unittest.TestCase):
    """agent-supervisor#640: `source_tasks.is_review` is added with
    `ALTER TABLE ... ADD COLUMN`, not the drop-and-recreate dance
    `_migrate_tasks_table`/`_migrate_source_tasks_table` need -- see
    `Ledger._migrate_source_tasks_review_column`'s own docstring for why
    that suffices here and never risks agent-supervisor#635/#636's
    trigger-rename hazard. This class proves the mechanics directly: the
    column appears, every pre-existing row (other than the two named
    backfill exceptions, covered by `KnownMisclassifiedReviewBackfillTest`
    below) reads `NULL`, and the `one_open_pull_per_source_ref` trigger --
    deliberately never touched by this migration -- survives untouched."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.root = Path(self.tempdir.name)
        _seed_pre_is_review_column_database(self.root)

    def _table_info(self, root=None):
        connection = sqlite3.connect((root or self.root) / "ledger.sqlite3")
        connection.row_factory = sqlite3.Row
        try:
            return {row["name"]: row for row in connection.execute("PRAGMA table_info(source_tasks)").fetchall()}
        finally:
            connection.close()

    def test_column_is_absent_before_migration_runs(self):
        """Pins what the seed helper actually built, so a failure in the
        test below is legible as a migration bug, not a bad fixture."""
        self.assertNotIn("is_review", self._table_info())

    def test_opening_migrates_the_column_and_preserves_every_row(self):
        ledger = Ledger(self.root, clock=lambda: 2_000)

        self.assertIn("is_review", self._table_info())
        self.assertEqual(2, len(ledger.list_source_tasks()))

        # The known-mislabelled id is backfilled (KnownMisclassifiedReview-
        # BackfillTest below covers this in depth); the ordinary row next to
        # it is NOT -- proving the backfill is scoped, not a blanket sweep.
        self.assertEqual(1, ledger.get_source_task("as637-rerev636")["is_review"])
        self.assertIsNone(ledger.get_source_task("as1-fix-999")["is_review"])

        # `one_open_pull_per_source_ref` is deliberately untouched by this
        # migration (`ADD COLUMN` never renames the table, unlike
        # `_migrate_tasks_table`/`_migrate_source_tasks_table`, which must
        # drop and recreate it around their own rebuilds) -- still present
        # after `__init__` runs.
        connection = sqlite3.connect(self.root / "ledger.sqlite3")
        try:
            trigger = connection.execute(
                "SELECT 1 FROM sqlite_master WHERE type='trigger' AND name='one_open_pull_per_source_ref'"
            ).fetchone()
            self.assertIsNotNone(trigger, "the pull-uniqueness trigger must survive this migration untouched")
        finally:
            connection.close()

    def test_migration_failure_rolls_back_leaving_column_absent(self):
        for failpoint in ("before_add_is_review_column", "after_add_is_review_column"):
            with self.subTest(failpoint=failpoint):
                root = Path(tempfile.mkdtemp())
                self.addCleanup(lambda r=root: __import__("shutil").rmtree(r, ignore_errors=True))
                _seed_pre_is_review_column_database(root)

                with self.assertRaisesRegex(RuntimeError, failpoint):
                    Ledger(root, clock=lambda: 2_000, _migration_failpoint=failpoint)

                self.assertNotIn("is_review", self._table_info(root))
                connection = sqlite3.connect(root / "ledger.sqlite3")
                try:
                    self.assertEqual(2, connection.execute("SELECT COUNT(*) FROM source_tasks").fetchone()[0])
                finally:
                    connection.close()

                # And migration recovers cleanly on the very next open.
                recovered = Ledger(root, clock=lambda: 3_000)
                self.assertIn("is_review", self._table_info(root))
                self.assertEqual(1, recovered.get_source_task("as637-rerev636")["is_review"])

    def test_fresh_ledger_already_carries_the_column(self):
        fresh_root = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: __import__("shutil").rmtree(fresh_root, ignore_errors=True))
        Ledger(fresh_root, clock=lambda: 1_000)
        self.assertIn("is_review", self._table_info(fresh_root))


