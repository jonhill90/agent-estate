import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
from tests.supervisor.test_core_helpers import _seed_pre_pr_database  # noqa: E402


class TaskTableMigrationWithExistingPullTriggerTest(unittest.TestCase):
    """agent-supervisor#635: `Ledger()` raised `OperationalError: error in
    trigger one_open_pull_per_source_ref: no such table: main.tasks` and
    NEVER CONSTRUCTED on any estate that already had
    `ONE_OPEN_PULL_PER_SOURCE_REF` (created by a prior
    `_migrate_source_tasks_pull_uniqueness` run) and also still needed a
    `_migrate_tasks_table` rebuild (missing `pane_id` / `worktree_path`,
    agent-supervisor#631/#117) -- exactly the state the live estate was
    actually in. `TaskTableMigrationTest` above never catches this: its
    fixture (`_seed_pre_pr_database`) predates the trigger's own migration
    entirely, so the trigger never exists when that class's rebuild runs.
    This is the scenario that shipped broken -- a SECOND migration, run
    against a ledger that already has the FIRST migration's trigger."""

    def _seed_pre_631_database_with_pull_trigger(self, root):
        """A ledger that already ran `_migrate_source_tasks_pull_uniqueness`
        (so it carries the trigger) but whose `tasks` table still predates
        `pane_id` / `worktree_path` -- built by opening a real, fully
        migrated `Ledger` (so the trigger is the genuine, current one) and
        then hand-downgrading just the `tasks` table underneath it, the
        only way to reach a state where a NEWER column migration lands on
        an OLDER `tasks` shape with the trigger already in place."""
        ledger = Ledger(root, clock=lambda: 1_000)
        ledger.register_lane(
            lane="app-review", pane_id="%1", nonce="n", harness="codex", repo="/r",
            server_id="s", session_id="$1", command="codex",
        )
        ledger.record_dispatch(
            lane="app-review", pane_id="%1", nonce="n", harness="codex", repo="/r",
            server_id="s", session_id="$1", command="codex", task_id="review-870",
            source_kind="pull", source_url="https://github.com/jonhill90/agent-supervisor/pull/870",
            source_ref="870", summary="Review PR 870 without editing", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane app-review", "pr: 870"], status_marker=None,
        )
        connection = sqlite3.connect(root / "ledger.sqlite3")
        try:
            self.assertIsNotNone(
                connection.execute(
                    "SELECT 1 FROM sqlite_master WHERE type='trigger' AND name='one_open_pull_per_source_ref'"
                ).fetchone(),
                "fixture bug: the real trigger did not get created",
            )
            # Downgrade `tasks` to its pre-#631/#117 shape in place, exactly
            # the create-copy-drop-rename dance `_migrate_tasks_table` itself
            # does, just run backwards by hand here -- with the trigger
            # (which joins `tasks`) still attached to `source_tasks` the
            # whole time, same as it would be on a real long-lived estate.
            connection.execute("PRAGMA legacy_alter_table = ON")
            connection.execute("BEGIN")
            connection.execute(
                """
                CREATE TABLE tasks_old (
                    id TEXT PRIMARY KEY,
                    lane TEXT NOT NULL REFERENCES lanes(lane),
                    pane_nonce TEXT NOT NULL,
                    summary TEXT NOT NULL,
                    status TEXT NOT NULL CHECK (
                        status IN ('created', 'delivery_pending', 'delivered', 'accepted',
                                   'running', 'complete', 'failed', 'cancelled')
                    ),
                    result_path TEXT,
                    result_sha256 TEXT,
                    created_at INTEGER NOT NULL,
                    updated_at INTEGER NOT NULL,
                    delivery_attempted_at INTEGER,
                    delivered_at INTEGER,
                    accepted_at INTEGER,
                    completed_at INTEGER
                )
                """
            )
            connection.execute(
                """
                INSERT INTO tasks_old (id, lane, pane_nonce, summary, status, result_path, result_sha256,
                    created_at, updated_at, delivery_attempted_at, delivered_at, accepted_at, completed_at)
                SELECT id, lane, pane_nonce, summary, status, result_path, result_sha256,
                    created_at, updated_at, delivery_attempted_at, delivered_at, accepted_at, completed_at
                FROM tasks
                """
            )
            connection.execute("DROP TABLE tasks")
            connection.execute("ALTER TABLE tasks_old RENAME TO tasks")
            connection.commit()
        finally:
            connection.close()

    def test_reproduction_construction_fails_without_the_fix(self):
        """Proves this fixture actually reaches the reported failure: run
        against `_migrate_tasks_table` with its trigger-preservation code
        skipped, the way it read before agent-supervisor#635's fix."""
        root = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: __import__("shutil").rmtree(root, ignore_errors=True))
        self._seed_pre_631_database_with_pull_trigger(root)

        connection = sqlite3.connect(root / "ledger.sqlite3")
        try:
            connection.execute("BEGIN")
            connection.execute(
                """
                CREATE TABLE tasks_migrated (
                    id TEXT PRIMARY KEY, lane TEXT NOT NULL REFERENCES lanes(lane),
                    pane_nonce TEXT NOT NULL, summary TEXT NOT NULL,
                    status TEXT NOT NULL CHECK (
                        status IN ('created', 'delivery_pending', 'delivered', 'accepted',
                                   'running', 'complete', 'failed', 'cancelled')
                    ),
                    result_path TEXT, result_sha256 TEXT, created_at INTEGER NOT NULL,
                    updated_at INTEGER NOT NULL, delivery_attempted_at INTEGER, delivered_at INTEGER,
                    accepted_at INTEGER, completed_at INTEGER,
                    worktree_path TEXT NOT NULL DEFAULT '', pane_id TEXT NOT NULL DEFAULT ''
                )
                """
            )
            connection.execute(
                """
                INSERT INTO tasks_migrated (id, lane, pane_nonce, summary, status, result_path, result_sha256,
                    created_at, updated_at, delivery_attempted_at, delivered_at, accepted_at, completed_at,
                    worktree_path, pane_id)
                SELECT id, lane, pane_nonce, summary, status, result_path, result_sha256,
                    created_at, updated_at, delivery_attempted_at, delivered_at, accepted_at, completed_at,
                    '', ''
                FROM tasks
                """
            )
            connection.execute("DROP TABLE tasks")
            with self.assertRaisesRegex(sqlite3.OperationalError, "one_open_pull_per_source_ref"):
                connection.execute("ALTER TABLE tasks_migrated RENAME TO tasks")
        finally:
            connection.rollback()
            connection.close()

    def test_opening_migrates_tasks_and_preserves_the_trigger_and_every_row(self):
        root = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: __import__("shutil").rmtree(root, ignore_errors=True))
        self._seed_pre_631_database_with_pull_trigger(root)

        # This is the reported failure: constructing a fresh `Ledger` against
        # this exact on-disk state used to raise `OperationalError` and
        # never return.
        ledger = Ledger(root, clock=lambda: 2_000)

        after = ledger.get_task("review-870")
        self.assertEqual("app-review", after["lane"])
        self.assertEqual("", after["pane_id"])
        self.assertEqual("", after["worktree_path"])
        self.assertEqual(1, len(ledger.list_tasks()))

        connection = sqlite3.connect(root / "ledger.sqlite3")
        try:
            trigger_sql = connection.execute(
                "SELECT sql FROM sqlite_master WHERE type='trigger' AND name='one_open_pull_per_source_ref'"
            ).fetchone()
        finally:
            connection.close()
        self.assertIsNotNone(trigger_sql, "the duplicate-open-PR guard must survive the tasks rebuild")

        # The trigger still actually enforces, on a DIFFERENT lane/PR pair --
        # not just present in sqlite_master as dead SQL.
        ledger.register_lane(
            lane="free-3", pane_id="%3", nonce="nonce-a", harness="claude", repo="/repo/free-3",
            server_id="server-a", session_id="$3", command="claude.exe",
        )
        ledger.register_lane(
            lane="free-4", pane_id="%4", nonce="nonce-b", harness="claude", repo="/repo/free-4",
            server_id="server-a", session_id="$4", command="claude.exe",
        )
        ledger.record_dispatch(
            lane="free-3", pane_id="%3", nonce="nonce-a", harness="claude", repo="/repo/free-3",
            server_id="server-a", session_id="$3", command="claude.exe", task_id="as635-a",
            source_kind="pull", source_url="https://github.com/jonhill90/agent-supervisor/pull/961",
            source_ref="961", summary="fix pass on PR #961", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3", "pr: 961"], status_marker=None,
        )
        with self.assertRaisesRegex(Exception, "source_tasks.source_ref"):
            ledger.record_dispatch(
                lane="free-4", pane_id="%4", nonce="nonce-b", harness="claude", repo="/repo/free-4",
                server_id="server-a", session_id="$4", command="claude.exe", task_id="as635-b",
                source_kind="pull", source_url="https://github.com/jonhill90/agent-supervisor/pull/961",
                source_ref="961", summary="second dispatcher for the same PR -- must be refused",
                source_state="OPEN", evidence=["claimed by dispatch.sh for lane free-4", "pr: 961"],
                status_marker=None,
            )

    def test_migration_failure_rolls_back_and_the_trigger_is_never_left_missing(self):
        """The dangerous direction the brief calls out by name: if this
        fix dropped the trigger and forgot to recreate it, the guard would
        silently disappear. Every failpoint -- including the two new ones
        added around the trigger drop/recreate -- must leave the ORIGINAL
        table and the ORIGINAL trigger intact after rollback, and a clean
        retry must restore both together."""
        for failpoint in (
            "after_drop_pull_trigger", "after_create", "after_copy",
            "after_drop", "after_rename", "after_recreate_pull_trigger",
        ):
            with self.subTest(failpoint=failpoint):
                root = Path(tempfile.mkdtemp())
                self.addCleanup(lambda r=root: __import__("shutil").rmtree(r, ignore_errors=True))
                self._seed_pre_631_database_with_pull_trigger(root)

                with self.assertRaisesRegex(RuntimeError, failpoint):
                    Ledger(root, clock=lambda: 2_000, _migration_failpoint=failpoint)

                connection = sqlite3.connect(root / "ledger.sqlite3")
                connection.row_factory = sqlite3.Row
                try:
                    sql = connection.execute(
                        "SELECT sql FROM sqlite_master WHERE type='table' AND name='tasks'"
                    ).fetchone()["sql"]
                    self.assertNotIn("pane_id", sql, f"schema leaked past rollback at {failpoint}")
                    # The one thing this fix must never do: leave the guard
                    # missing after a rollback, at ANY failpoint.
                    trigger = connection.execute(
                        "SELECT 1 FROM sqlite_master WHERE type='trigger' "
                        "AND name='one_open_pull_per_source_ref'"
                    ).fetchone()
                    self.assertIsNotNone(
                        trigger, f"trigger missing after rollback at {failpoint} -- guard silently gone"
                    )
                    self.assertEqual(1, connection.execute("SELECT COUNT(*) FROM tasks").fetchone()[0])
                finally:
                    connection.close()

                recovered = Ledger(root, clock=lambda: 3_000)
                after = recovered.get_task("review-870")
                self.assertEqual("", after["pane_id"])
                connection = sqlite3.connect(root / "ledger.sqlite3")
                try:
                    self.assertIsNotNone(
                        connection.execute(
                            "SELECT 1 FROM sqlite_master WHERE type='trigger' "
                            "AND name='one_open_pull_per_source_ref'"
                        ).fetchone()
                    )
                finally:
                    connection.close()


