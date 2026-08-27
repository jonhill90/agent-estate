import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
from tests.supervisor.test_core_helpers import _seed_pre_pr_database  # noqa: E402


class TaskTableMigrationTest(unittest.TestCase):
    """Blocker 1: `CREATE TABLE IF NOT EXISTS` does not migrate an existing DB."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.root = Path(self.tempdir.name)
        _seed_pre_pr_database(self.root)

    def _raw_tasks_sql(self):
        connection = sqlite3.connect(self.root / "ledger.sqlite3")
        try:
            return connection.execute(
                "SELECT sql FROM sqlite_master WHERE type='table' AND name='tasks'"
            ).fetchone()[0]
        finally:
            connection.close()

    def _raw_task_row(self, task_id="review-870"):
        connection = sqlite3.connect(self.root / "ledger.sqlite3")
        connection.row_factory = sqlite3.Row
        try:
            row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
            return dict(row) if row else None
        finally:
            connection.close()

    def test_reproduction_old_check_constraint_rejects_delivery_pending_directly(self):
        """Before any migration exists, writing delivery_pending to the raw
        pre-PR schema must fail - this is the bug the migration fixes."""
        connection = sqlite3.connect(self.root / "ledger.sqlite3")
        try:
            with self.assertRaises(sqlite3.IntegrityError):
                connection.execute("UPDATE tasks SET status = 'delivery_pending' WHERE id = 'review-870'")
        finally:
            connection.close()

    def test_opening_pre_pr_database_migrates_schema_and_preserves_every_row(self):
        before = self._raw_task_row()
        self.assertEqual("created", before["status"])
        self.assertNotIn("delivery_attempted_at", before)

        ledger = Ledger(self.root, clock=lambda: 2_000)

        migrated_sql = self._raw_tasks_sql()
        self.assertIn("delivery_pending", migrated_sql)
        self.assertIn("delivery_attempted_at", migrated_sql)
        self.assertIn("worktree_path", migrated_sql)

        after = ledger.get_task("review-870")
        self.assertEqual(before["id"], after["id"])
        self.assertEqual(before["lane"], after["lane"])
        self.assertEqual(before["pane_nonce"], after["pane_nonce"])
        self.assertEqual(before["summary"], after["summary"])
        self.assertEqual(before["status"], after["status"])
        self.assertEqual(before["created_at"], after["created_at"])
        self.assertIsNone(after["delivery_attempted_at"])
        # agent-supervisor#117: a row this old recorded no worktree path
        # anywhere structured (only, maybe, as free text inside `summary`)
        # -- backfilling one by parsing that text would be writing a guess
        # into a column whose entire point is to be authored fact, so the
        # migration deliberately leaves it '', the same "never recorded"
        # answer `get_task_for_worktree` already gives any unmatched path.
        self.assertEqual("", after["worktree_path"])
        self.assertIsNone(ledger.get_task_for_worktree(""))

        # Every other table, and the lane row the task's FK depends on, survives untouched.
        self.assertEqual(1, len(ledger.list_lanes()))
        self.assertEqual(1, len(ledger.list_tasks()))
        self.assertEqual(1, len(ledger.list_events()))

        # The FK from events to tasks is intact and still enforced by name.
        connection = sqlite3.connect(self.root / "ledger.sqlite3")
        try:
            connection.execute("PRAGMA foreign_keys = ON")
            with self.assertRaises(sqlite3.IntegrityError):
                connection.execute(
                    "INSERT INTO events(key, type, task_id, status, created_at) "
                    "VALUES ('bad', 'attention', 'does-not-exist', 'pending', 2000)"
                )
                connection.commit()
        finally:
            connection.close()

        # The whole point: mark_delivery_pending now works against the migrated schema.
        pending = ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.assertEqual("delivery_pending", pending["status"])

        # The one_open_task_per_lane index survived: a second open task on the same lane still fails.
        ledger.reconstruct_task(
            task_id="review-871", source_kind="issue",
            source_url="https://github.com/jonhill90/Hill90/issues/871", source_ref="a" * 40,
            summary="Second task", source_state="OPEN", status="created", evidence=[], status_marker=None,
        )
        with self.assertRaisesRegex(ValueError, "outstanding task"):
            ledger.assign(task_id="review-871", lane="app-review", pane_nonce="nonce-22-a", summary="Second task")

    def test_migration_failure_rolls_back_leaving_original_table_and_rows_intact(self):
        for failpoint in ("after_create", "after_copy", "after_drop", "after_rename"):
            with self.subTest(failpoint=failpoint):
                root = Path(tempfile.mkdtemp())
                self.addCleanup(lambda r=root: __import__("shutil").rmtree(r, ignore_errors=True))
                _seed_pre_pr_database(root)

                with self.assertRaisesRegex(RuntimeError, failpoint):
                    Ledger(root, clock=lambda: 2_000, _migration_failpoint=failpoint)

                # Rolled back: the table is exactly as it was before the attempt.
                connection = sqlite3.connect(root / "ledger.sqlite3")
                connection.row_factory = sqlite3.Row
                try:
                    sql = connection.execute(
                        "SELECT sql FROM sqlite_master WHERE type='table' AND name='tasks'"
                    ).fetchone()["sql"]
                    self.assertNotIn("delivery_pending", sql, f"schema leaked past rollback at {failpoint}")
                    row = dict(connection.execute("SELECT * FROM tasks WHERE id='review-870'").fetchone())
                    self.assertEqual("created", row["status"])
                    self.assertEqual(1, connection.execute("SELECT COUNT(*) FROM tasks").fetchone()[0])
                    self.assertEqual(1, connection.execute("SELECT COUNT(*) FROM events").fetchone()[0])
                finally:
                    connection.close()

                # And migration recovers cleanly on the very next open.
                recovered = Ledger(root, clock=lambda: 3_000)
                self.assertIn("delivery_pending", self._migrated_sql(root))
                after = recovered.get_task("review-870")
                self.assertEqual("created", after["status"])
                pending = recovered.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
                self.assertEqual("delivery_pending", pending["status"])

    @staticmethod
    def _migrated_sql(root):
        connection = sqlite3.connect(root / "ledger.sqlite3")
        try:
            return connection.execute(
                "SELECT sql FROM sqlite_master WHERE type='table' AND name='tasks'"
            ).fetchone()[0]
        finally:
            connection.close()

    def test_fresh_ledger_never_triggers_a_migration_rebuild(self):
        """A brand-new state directory has the current schema from CREATE TABLE
        IF NOT EXISTS alone; `_migrate_tasks_table` must be a no-op for it."""
        fresh_root = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: __import__("shutil").rmtree(fresh_root, ignore_errors=True))
        ledger = Ledger(fresh_root, clock=lambda: 1_000)
        ledger.register_lane(
            lane="app-review", pane_id="%1", nonce="n", harness="codex", repo="/r",
            server_id="s", session_id="$1", command="codex",
        )
        ledger.reconstruct_task(
            task_id="t1", source_kind="issue",
            source_url="https://github.com/jonhill90/Hill90/issues/1", source_ref="a" * 40,
            summary="Do the thing", source_state="OPEN", status="created", evidence=[], status_marker=None,
        )
        task = ledger.assign(task_id="t1", lane="app-review", pane_nonce="n", summary="Do the thing")
        self.assertEqual("created", task["status"])
        pending = ledger.mark_delivery_pending("t1", pane_nonce="n")
        self.assertEqual("delivery_pending", pending["status"])


