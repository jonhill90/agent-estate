import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
from tests.supervisor.test_core_helpers import PRE_PR_SCHEMA_SCRIPT  # noqa: E402


def _seed_pre_source_tasks_migration_database(root: Path):
    """Build a ledger.sqlite3 with the pre-#144 schema (old `source_url`
    table-level UNIQUE constraint, current lanes/tasks) and one source_tasks
    row."""
    root.mkdir(parents=True, exist_ok=True)
    db_path = root / "ledger.sqlite3"
    connection = sqlite3.connect(db_path)
    try:
        connection.execute("PRAGMA foreign_keys = ON")
        connection.executescript(PRE_PR_SCHEMA_SCRIPT)
        connection.execute(
            """
            INSERT INTO lanes(lane, pane_id, nonce, harness, repo, server_id, session_id, command, updated_at)
            VALUES ('app-review', '%22', 'nonce-22-a', 'codex', '/repo/app', 'server-a', '$4', 'codex', 1000)
            """
        )
        connection.execute(
            """
            INSERT INTO source_tasks(
                id, source_kind, source_url, source_ref, summary, source_state,
                status, evidence_json, status_marker, updated_at
            ) VALUES (
                'ad999-first', 'issue', 'https://github.com/jonhill90/agent-dotfiles/issues/999', '999',
                '#999 first', 'OPEN', 'created', '[]', NULL, 1000
            )
            """
        )
        connection.commit()
    finally:
        connection.close()




class SourceTasksMigrationTest(unittest.TestCase):
    """agent-dotfiles#144 finding 1: `source_tasks.source_url`'s old
    table-level UNIQUE constraint made a second dispatch of the same GitHub
    issue, under a different task id, fail forever. `CREATE TABLE IF NOT
    EXISTS` does not migrate an existing DB -- every ledger created before
    this fix still carries that constraint unless this migration runs."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.root = Path(self.tempdir.name)
        _seed_pre_source_tasks_migration_database(self.root)

    def _raw_source_tasks_sql(self, root=None):
        connection = sqlite3.connect((root or self.root) / "ledger.sqlite3")
        try:
            return connection.execute(
                "SELECT sql FROM sqlite_master WHERE type='table' AND name='source_tasks'"
            ).fetchone()[0]
        finally:
            connection.close()

    def test_reproduction_old_unique_constraint_rejects_a_second_row_with_the_same_url_directly(self):
        """Before any migration exists, a second source_tasks row for the
        same URL under a different id must fail against the raw pre-#144
        schema -- this is the bug the migration fixes."""
        connection = sqlite3.connect(self.root / "ledger.sqlite3")
        try:
            with self.assertRaises(sqlite3.IntegrityError):
                connection.execute(
                    "INSERT INTO source_tasks(id, source_kind, source_url, source_ref, summary, "
                    "source_state, status, evidence_json, status_marker, updated_at) VALUES "
                    "('ad999-rereview', 'issue', 'https://github.com/jonhill90/agent-dotfiles/issues/999', "
                    "'999', '#999 rereview', 'OPEN', 'created', '[]', NULL, 1000)"
                )
        finally:
            connection.close()

    def test_opening_pre_144_database_migrates_schema_and_preserves_every_row(self):
        ledger = Ledger(self.root, clock=lambda: 2_000)

        migrated_sql = self._raw_source_tasks_sql()
        self.assertNotIn("UNIQUE", migrated_sql)

        existing = ledger.get_source_task("ad999-first")
        self.assertEqual("#999 first", existing["summary"])

        # The point: a second dispatch of the same issue now works against
        # the migrated schema.
        second = ledger.reconstruct_task(
            task_id="ad999-rereview", source_kind="issue",
            source_url="https://github.com/jonhill90/agent-dotfiles/issues/999", source_ref="999",
            summary="#999 rereview", source_state="OPEN", status="created",
            evidence=["claimed by dispatch.sh for lane free-4"], status_marker=None,
        )
        self.assertEqual("#999 rereview", second["summary"])
        self.assertEqual(2, len(ledger.list_source_tasks()))

        # Every other table survives the rebuild untouched.
        self.assertEqual(1, len(ledger.list_lanes()))

    def test_migration_failure_rolls_back_leaving_original_table_and_rows_intact(self):
        for failpoint in ("after_create", "after_copy", "after_drop", "after_rename"):
            with self.subTest(failpoint=failpoint):
                root = Path(tempfile.mkdtemp())
                self.addCleanup(lambda r=root: __import__("shutil").rmtree(r, ignore_errors=True))
                _seed_pre_source_tasks_migration_database(root)

                with self.assertRaisesRegex(RuntimeError, failpoint):
                    Ledger(root, clock=lambda: 2_000, _migration_failpoint=failpoint)

                connection = sqlite3.connect(root / "ledger.sqlite3")
                connection.row_factory = sqlite3.Row
                try:
                    sql = connection.execute(
                        "SELECT sql FROM sqlite_master WHERE type='table' AND name='source_tasks'"
                    ).fetchone()["sql"]
                    self.assertIn("UNIQUE", sql, f"schema rebuilt past rollback at {failpoint}")
                    row = dict(connection.execute("SELECT * FROM source_tasks WHERE id='ad999-first'").fetchone())
                    self.assertEqual("#999 first", row["summary"])
                    self.assertEqual(1, connection.execute("SELECT COUNT(*) FROM source_tasks").fetchone()[0])
                finally:
                    connection.close()

                # And migration recovers cleanly on the very next open.
                recovered = Ledger(root, clock=lambda: 3_000)
                self.assertNotIn("UNIQUE", self._raw_source_tasks_sql(root))
                second = recovered.reconstruct_task(
                    task_id="ad999-rereview", source_kind="issue",
                    source_url="https://github.com/jonhill90/agent-dotfiles/issues/999", source_ref="999",
                    summary="#999 rereview", source_state="OPEN", status="created",
                    evidence=["x"], status_marker=None,
                )
                self.assertEqual("#999 rereview", second["summary"])

    def test_fresh_ledger_source_tasks_never_enforces_url_uniqueness(self):
        fresh_root = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: __import__("shutil").rmtree(fresh_root, ignore_errors=True))
        ledger = Ledger(fresh_root, clock=lambda: 1_000)
        ledger.reconstruct_task(
            task_id="t1", source_kind="issue",
            source_url="https://github.com/jonhill90/Hill90/issues/1", source_ref="1",
            summary="one", source_state="OPEN", status="created", evidence=[], status_marker=None,
        )
        second = ledger.reconstruct_task(
            task_id="t2", source_kind="issue",
            source_url="https://github.com/jonhill90/Hill90/issues/1", source_ref="1",
            summary="two", source_state="OPEN", status="created", evidence=[], status_marker=None,
        )
        self.assertEqual("two", second["summary"])


