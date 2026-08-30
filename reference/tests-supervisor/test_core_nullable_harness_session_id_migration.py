import json
import os
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402


# agent-supervisor#65: the schema the live estate was actually running --
# already widened for 'copilot', 'copilot-acp' and a NULLABLE
# `harness_session_id` (added by #237's own migration, before this repo
# existed, so no earlier fixture here carries it), but not yet for `'pi'` or
# `transport` (agent-supervisor#58's migration). `harness_session_id` being
# NULL for a lane is not corruption -- #237's own comment on the current
# schema says empty/absent means "not resolved", and codex has never had a
# resolver, so every codex lane ever dispatched has NULL here. #61's
# `lanes_migrated` table declared this column NOT NULL and copied these rows
# straight across; that INSERT threw `sqlite3.IntegrityError`, and every
# ledger read (`status`, `delivered-open`, `digest.sh`'s reconcile) failed
# with it live on the estate.
PRE_58_LANES_SCHEMA_SCRIPT = """
CREATE TABLE lanes (
    lane TEXT PRIMARY KEY,
    pane_id TEXT NOT NULL,
    nonce TEXT NOT NULL,
    harness TEXT NOT NULL CHECK (harness IN ('codex', 'claude', 'copilot', 'copilot-acp')),
    repo TEXT NOT NULL,
    server_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    command TEXT NOT NULL,
    harness_session_id TEXT,
    updated_at INTEGER NOT NULL
);

CREATE TABLE tasks (
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
);

CREATE UNIQUE INDEX one_open_task_per_lane
    ON tasks(lane)
    WHERE status NOT IN ('complete', 'failed', 'cancelled');

CREATE TABLE source_tasks (
    id TEXT PRIMARY KEY,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('issue', 'pull')),
    source_url TEXT NOT NULL UNIQUE,
    source_ref TEXT NOT NULL,
    summary TEXT NOT NULL,
    source_state TEXT NOT NULL,
    status TEXT NOT NULL CHECK (
        status IN ('created', 'delivered', 'accepted', 'running',
                   'complete', 'failed', 'cancelled')
    ),
    evidence_json TEXT NOT NULL,
    status_marker TEXT,
    updated_at INTEGER NOT NULL
);

CREATE TABLE events (
    key TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    task_id TEXT REFERENCES tasks(id),
    status TEXT NOT NULL CHECK (status IN ('pending', 'notified', 'acked')),
    payload_path TEXT,
    created_at INTEGER NOT NULL,
    notified_at INTEGER,
    retry_at INTEGER,
    acked_at INTEGER
);

CREATE TABLE components (
    name TEXT PRIMARY KEY,
    healthy INTEGER NOT NULL,
    error TEXT,
    snapshot_sha256 TEXT,
    updated_at INTEGER NOT NULL
);
"""


def _seed_pre_58_lanes_migration_database(root: Path):
    """Build a ledger.sqlite3 shaped like the live estate's before #65's fix:
    a codex lane with a genuine NULL `harness_session_id` (codex has never had
    a session resolver) alongside a claude lane whose id resolved fine."""
    root.mkdir(parents=True, exist_ok=True)
    db_path = root / "ledger.sqlite3"
    connection = sqlite3.connect(db_path)
    try:
        connection.execute("PRAGMA foreign_keys = ON")
        connection.executescript(PRE_58_LANES_SCHEMA_SCRIPT)
        connection.execute(
            """
            INSERT INTO lanes(lane, pane_id, nonce, harness, repo, server_id, session_id, command,
                               harness_session_id, updated_at)
            VALUES ('app-review', '%22', 'nonce-22-a', 'codex', '/repo/app', 'server-a', '$4', 'codex',
                    NULL, 1000)
            """
        )
        connection.execute(
            """
            INSERT INTO lanes(lane, pane_id, nonce, harness, repo, server_id, session_id, command,
                               harness_session_id, updated_at)
            VALUES ('claude-review', '%23', 'nonce-23-a', 'claude', '/repo/app', 'server-a', '$5', 'claude',
                    'session-abc', 1000)
            """
        )
        connection.execute(
            """
            INSERT INTO tasks(id, lane, pane_nonce, summary, status, created_at, updated_at)
            VALUES ('review-870', 'app-review', 'nonce-22-a', 'Review PR 870 without editing', 'created', 1000, 1000)
            """
        )
        connection.commit()
    finally:
        connection.close()



class NullableHarnessSessionIdMigrationTest(unittest.TestCase):
    """agent-supervisor#65. #61's migration declared the rebuilt `lanes`
    table's `harness_session_id` NOT NULL and copied existing rows straight
    across; every codex lane -- codex has never had a session resolver, so
    every one of them legitimately has NULL there -- made that INSERT raise
    `sqlite3.IntegrityError`, taking down `status`, `delivered-open` and
    `digest.sh`'s reconcile on the live estate. The column must be nullable;
    the NULL is correct data, not a gap to backfill."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.root = Path(self.tempdir.name)
        _seed_pre_58_lanes_migration_database(self.root)

    def test_reproduction_a_null_harness_session_id_row_exists_before_migration(self):
        connection = sqlite3.connect(self.root / "ledger.sqlite3")
        try:
            row = connection.execute(
                "SELECT harness_session_id FROM lanes WHERE lane = 'app-review'"
            ).fetchone()
            self.assertIsNone(row[0])
        finally:
            connection.close()

    def test_a_null_harness_session_id_row_migrates_cleanly_and_status_returns(self):
        ledger = Ledger(self.root, clock=lambda: 2_000)

        migrated_sql = self._raw_lanes_sql()
        self.assertIn("'pi'", migrated_sql)
        self.assertIn("transport", migrated_sql)
        # agent-supervisor#172: this schema (pre-#58) has no
        # `harness_project_dir` column at all -- the migration must add it,
        # same as `'pi'` and `transport` above.
        self.assertIn("harness_project_dir", migrated_sql)

        # The point: this used to raise sqlite3.IntegrityError opening the
        # Ledger at all. Reading it back now works, and the NULL survived
        # the rebuild as NULL -- not silently turned into a fake id.
        codex_lane = ledger.get_lane("app-review")
        self.assertIsNone(codex_lane["harness_session_id"])
        self.assertEqual("send-keys", codex_lane["transport"])
        # agent-supervisor#172. A row from before this column existed backfills
        # to '' -- NOT to `repo`, and NOT to NULL either (`harness_session_id`
        # above is the one legitimately-nullable column; this one follows
        # `harness_project_dir`'s own `DEFAULT ''`) -- the same "not resolved"
        # reading `restore.sh` already gives an absent `harness_session_id`.
        # Guessing `repo` here would be exactly the defect #172 exists to fix,
        # reintroduced by the migration that is supposed to close it.
        self.assertEqual("", codex_lane["harness_project_dir"])

        # A row that DID resolve a session id keeps it, proving the copy
        # isn't just dropping the column's value wholesale.
        claude_lane = ledger.get_lane("claude-review")
        self.assertEqual("session-abc", claude_lane["harness_session_id"])

        # status's own read path: list_lanes() must not choke on the NULL.
        lanes = ledger.list_lanes()
        self.assertEqual(2, len(lanes))
        by_lane = {row["lane"]: row for row in lanes}
        self.assertIsNone(by_lane["app-review"]["harness_session_id"])

    def test_status_delivered_open_and_digest_reconcile_succeed_against_such_a_ledger(self):
        """The three callers issue #65 named as broken on the live box,
        exercised the way an operator actually hits them: through `cli.py`
        as a subprocess, against this exact NULL-carrying ledger."""
        ledger = Ledger(self.root, clock=lambda: 2_000)
        del ledger  # only needed to run the migration before the CLI opens it

        cli_path = SUPERVISOR_DIR / "cli.py"
        env = dict(os.environ, AGENT_SUPERVISOR_STATE_DIR=str(self.root))

        status = subprocess.run(
            [sys.executable, str(cli_path), "status"],
            cwd=self.root, env=env, capture_output=True, text=True,
        )
        self.assertEqual(0, status.returncode, status.stderr)
        self.assertIn('"app-review"', status.stdout)

        delivered_open = subprocess.run(
            [sys.executable, str(cli_path), "delivered-open"],
            cwd=self.root, env=env, capture_output=True, text=True,
        )
        self.assertEqual(0, delivered_open.returncode, delivered_open.stderr)

        restore_plan = subprocess.run(
            [sys.executable, str(cli_path), "restore-plan"],
            cwd=self.root, env=env, capture_output=True, text=True,
        )
        self.assertEqual(0, restore_plan.returncode, restore_plan.stderr)
        plan = json.loads(restore_plan.stdout)
        by_lane = {row["lane"]: row for row in plan}
        self.assertIsNone(by_lane["app-review"]["harness_session_id"])
        # agent-supervisor#172: the field `restore.sh` now reads to pick the
        # resume directory, present in the plan (empty, not absent) even for
        # a lane migrated from a schema that never had it.
        self.assertEqual("", by_lane["app-review"]["harness_project_dir"])

    def test_migration_running_twice_is_a_harmless_no_op(self):
        """Confirms the schema-marker check (`_LANES_SCHEMA_MARKERS`) treats
        a successfully migrated table as done, and a half-applied
        `lanes_migrated` cannot be left behind: the rebuild is one
        transaction (`test_migration_failure_rolls_back_leaving_original_
        table_and_rows_intact` covers the rollback itself), so a second open
        either finds the already-current schema and no-ops, or -- had the
        first run failed -- finds the untouched original and retries clean."""
        first = Ledger(self.root, clock=lambda: 2_000)
        before = first.get_lane("app-review")

        second = Ledger(self.root, clock=lambda: 3_000)
        after = second.get_lane("app-review")

        self.assertEqual(dict(before), dict(after))
        connection = sqlite3.connect(self.root / "ledger.sqlite3")
        try:
            tables = {
                row[0]
                for row in connection.execute(
                    "SELECT name FROM sqlite_master WHERE type='table'"
                ).fetchall()
            }
        finally:
            connection.close()
        self.assertNotIn("lanes_migrated", tables)

    def _raw_lanes_sql(self):
        connection = sqlite3.connect(self.root / "ledger.sqlite3")
        try:
            return connection.execute(
                "SELECT sql FROM sqlite_master WHERE type='table' AND name='lanes'"
            ).fetchone()[0]
        finally:
            connection.close()


