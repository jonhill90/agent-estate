import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
from tests.supervisor.test_core_helpers import _seed_pre_pr_database  # noqa: E402


PRE_LANES_MIGRATION_SCHEMA_SCRIPT = """
CREATE TABLE lanes (
    lane TEXT PRIMARY KEY,
    pane_id TEXT NOT NULL,
    nonce TEXT NOT NULL,
    harness TEXT NOT NULL CHECK (harness IN ('codex', 'claude')),
    repo TEXT NOT NULL,
    server_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    command TEXT NOT NULL,
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


# agent-dotfiles#216: the intermediate state most ledgers on disk today
# actually are in -- already widened once for 'copilot-acp', never widened
# again for the plain tmux 'copilot' harness #216 adds. `_LANES_SCHEMA_MARKERS`
# has to require BOTH markers, not just re-check the first one, or a ledger
# in exactly this state would read "already migrated" and reject 'copilot'
# forever -- this is the schema that proves it.
PRE_216_LANES_SCHEMA_SCRIPT = PRE_LANES_MIGRATION_SCHEMA_SCRIPT.replace(
    "harness TEXT NOT NULL CHECK (harness IN ('codex', 'claude'))",
    "harness TEXT NOT NULL CHECK (harness IN ('codex', 'claude', 'copilot-acp'))",
)


def _seed_pre_216_lanes_migration_database(root: Path):
    """Build a ledger.sqlite3 already widened for copilot-acp but not yet for
    the plain tmux 'copilot' harness -- one existing lane of each kind."""
    root.mkdir(parents=True, exist_ok=True)
    db_path = root / "ledger.sqlite3"
    connection = sqlite3.connect(db_path)
    try:
        connection.execute("PRAGMA foreign_keys = ON")
        connection.executescript(PRE_216_LANES_SCHEMA_SCRIPT)
        connection.execute(
            """
            INSERT INTO lanes(lane, pane_id, nonce, harness, repo, server_id, session_id, command, updated_at)
            VALUES ('app-review', '%22', 'nonce-22-a', 'codex', '/repo/app', 'server-a', '$4', 'codex', 1000)
            """
        )
        connection.execute(
            """
            INSERT INTO lanes(lane, pane_id, nonce, harness, repo, server_id, session_id, command, updated_at)
            VALUES ('copilot-worker', 'session-1', 'nonce-acp', 'copilot-acp', '/repo/app', 'acp', 'session-1',
                    'copilot', 1000)
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



def _seed_pre_lanes_migration_database(root: Path):
    """Build a ledger.sqlite3 with an old `lanes` table and a current `tasks` table."""
    root.mkdir(parents=True, exist_ok=True)
    db_path = root / "ledger.sqlite3"
    connection = sqlite3.connect(db_path)
    try:
        connection.execute("PRAGMA foreign_keys = ON")
        connection.executescript(PRE_LANES_MIGRATION_SCHEMA_SCRIPT)
        connection.execute(
            """
            INSERT INTO lanes(lane, pane_id, nonce, harness, repo, server_id, session_id, command, updated_at)
            VALUES ('app-review', '%22', 'nonce-22-a', 'codex', '/repo/app', 'server-a', '$4', 'codex', 1000)
            """
        )
        connection.execute(
            """
            INSERT INTO tasks(id, lane, pane_nonce, summary, status, created_at, updated_at)
            VALUES ('review-870', 'app-review', 'nonce-22-a', 'Review PR 870 without editing', 'created', 1000, 1000)
            """
        )
        connection.execute(
            """
            INSERT INTO events(key, type, task_id, status, created_at)
            VALUES ('attention:review-870', 'attention', 'review-870', 'pending', 1000)
            """
        )
        connection.commit()
    finally:
        connection.close()



class LaneTableMigrationTest(unittest.TestCase):
    """`CREATE TABLE IF NOT EXISTS` does not migrate an existing DB: every
    ledger created before copilot-acp existed still carries the old
    CHECK (harness IN ('codex', 'claude')) constraint and would reject a
    copilot-acp registration forever unless this migration runs."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.root = Path(self.tempdir.name)
        _seed_pre_pr_database(self.root)

    def _raw_lanes_sql(self):
        connection = sqlite3.connect(self.root / "ledger.sqlite3")
        try:
            return connection.execute(
                "SELECT sql FROM sqlite_master WHERE type='table' AND name='lanes'"
            ).fetchone()[0]
        finally:
            connection.close()

    def test_reproduction_old_check_constraint_rejects_copilot_acp_directly(self):
        connection = sqlite3.connect(self.root / "ledger.sqlite3")
        try:
            with self.assertRaises(sqlite3.IntegrityError):
                connection.execute(
                    "INSERT INTO lanes(lane, pane_id, nonce, harness, repo, server_id, "
                    "session_id, command, updated_at) VALUES "
                    "('copilot-worker', 'session-1', 'nonce-acp', 'copilot-acp', '/repo/app', "
                    "'acp', 'session-1', 'copilot', 1000)"
                )
        finally:
            connection.close()

    def test_opening_pre_pr_database_migrates_lanes_schema_and_preserves_every_row(self):
        ledger = Ledger(self.root, clock=lambda: 2_000)

        migrated_sql = self._raw_lanes_sql()
        self.assertIn("copilot-acp", migrated_sql)
        # agent-dotfiles#216: the same rebuild also widens for the plain
        # tmux/Node 'copilot' harness, one migration doing both jumps at once
        # for a ledger old enough to have needed the first.
        self.assertIn("'copilot'", migrated_sql)

        existing = ledger.get_lane("app-review")
        self.assertEqual("codex", existing["harness"])
        self.assertEqual("nonce-22-a", existing["nonce"])

        # The point: registering a copilot-acp lane now works against the migrated schema.
        registered = ledger.register_lane(
            lane="copilot-worker", pane_id="session-1", nonce="nonce-acp", harness="copilot-acp",
            repo="/repo/app", server_id="acp", session_id="session-1", command="copilot",
        )
        self.assertEqual("copilot-acp", registered["harness"])

        # And a plain tmux copilot lane, the harness #216 added.
        tmux_copilot = ledger.register_lane(
            lane="council-copilot", pane_id="%7", nonce="nonce-7", harness="copilot",
            repo="/repo/app", server_id="server-a", session_id="$4", command="node",
        )
        self.assertEqual("copilot", tmux_copilot["harness"])

        # The outstanding task still bound to the untouched lane survives the rebuild.
        self.assertEqual("created", ledger.get_task("review-870")["status"])
        self.assertEqual(3, len(ledger.list_lanes()))

    def test_opening_a_post_copilot_acp_pre_216_database_widens_for_plain_copilot(self):
        """agent-dotfiles#216: a ledger already migrated once for copilot-acp
        must migrate AGAIN for the plain tmux 'copilot' harness -- this is
        the schema state most real ledgers are actually in, and the marker
        check that decides "already migrated" has to require both."""
        root = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: __import__("shutil").rmtree(root, ignore_errors=True))
        _seed_pre_216_lanes_migration_database(root)

        ledger = Ledger(root, clock=lambda: 2_000)

        connection = sqlite3.connect(root / "ledger.sqlite3")
        try:
            migrated_sql = connection.execute(
                "SELECT sql FROM sqlite_master WHERE type='table' AND name='lanes'"
            ).fetchone()[0]
        finally:
            connection.close()
        self.assertIn("copilot-acp", migrated_sql)
        self.assertIn("'copilot'", migrated_sql)

        # Both pre-existing lanes survived the second rebuild untouched.
        self.assertEqual("codex", ledger.get_lane("app-review")["harness"])
        self.assertEqual("copilot-acp", ledger.get_lane("copilot-worker")["harness"])

        registered = ledger.register_lane(
            lane="council-copilot", pane_id="%7", nonce="nonce-7", harness="copilot",
            repo="/repo/app", server_id="server-a", session_id="$4", command="node",
        )
        self.assertEqual("copilot", registered["harness"])

    def test_migration_failure_rolls_back_leaving_original_table_and_rows_intact(self):
        for failpoint in ("after_create", "after_copy", "after_drop", "after_rename"):
            with self.subTest(failpoint=failpoint):
                root = Path(tempfile.mkdtemp())
                self.addCleanup(lambda r=root: __import__("shutil").rmtree(r, ignore_errors=True))
                _seed_pre_lanes_migration_database(root)

                with self.assertRaisesRegex(RuntimeError, failpoint):
                    Ledger(root, clock=lambda: 2_000, _migration_failpoint=failpoint)

                # Rolled back: the table is exactly as it was before the attempt.
                connection = sqlite3.connect(root / "ledger.sqlite3")
                connection.row_factory = sqlite3.Row
                try:
                    sql = connection.execute(
                        "SELECT sql FROM sqlite_master WHERE type='table' AND name='lanes'"
                    ).fetchone()["sql"]
                    self.assertNotIn("copilot-acp", sql, f"schema leaked past rollback at {failpoint}")
                    row = dict(connection.execute("SELECT * FROM lanes WHERE lane='app-review'").fetchone())
                    self.assertEqual("codex", row["harness"])
                    self.assertEqual("nonce-22-a", row["nonce"])
                    self.assertEqual(1, connection.execute("SELECT COUNT(*) FROM lanes").fetchone()[0])
                    self.assertEqual(1, connection.execute("SELECT COUNT(*) FROM tasks").fetchone()[0])
                    self.assertEqual(1, connection.execute("SELECT COUNT(*) FROM events").fetchone()[0])
                finally:
                    connection.close()

                # And migration recovers cleanly on the very next open.
                recovered = Ledger(root, clock=lambda: 3_000)
                connection = sqlite3.connect(root / "ledger.sqlite3")
                try:
                    migrated_sql = connection.execute(
                        "SELECT sql FROM sqlite_master WHERE type='table' AND name='lanes'"
                    ).fetchone()[0]
                finally:
                    connection.close()
                self.assertIn("copilot-acp", migrated_sql)
                existing = recovered.get_lane("app-review")
                self.assertEqual("codex", existing["harness"])
                registered = recovered.register_lane(
                    lane="copilot-worker", pane_id="session-1", nonce="nonce-acp", harness="copilot-acp",
                    repo="/repo/app", server_id="acp", session_id="session-1", command="copilot",
                )
                self.assertEqual("copilot-acp", registered["harness"])

    def test_fresh_ledger_never_triggers_a_lanes_migration_rebuild(self):
        fresh_root = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: __import__("shutil").rmtree(fresh_root, ignore_errors=True))
        ledger = Ledger(fresh_root, clock=lambda: 1_000)
        registered = ledger.register_lane(
            lane="copilot-worker", pane_id="session-1", nonce="n", harness="copilot-acp",
            repo="/r", server_id="acp", session_id="session-1", command="copilot",
        )
        self.assertEqual("copilot-acp", registered["harness"])


