import sqlite3
import sys
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402

PRE_PR_SCHEMA_SCRIPT = """
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
        status IN ('created', 'delivered', 'accepted', 'running',
                   'complete', 'failed', 'cancelled')
    ),
    result_path TEXT,
    result_sha256 TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
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




def _seed_pre_pr_database(root: Path):
    """Build a ledger.sqlite3 with exactly the 3af0b431 schema and one row."""
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


# Old `lanes` CHECK constraint (no `copilot-acp`) paired with the *current*
# `tasks` schema, so opening this database exercises `_migrate_lanes_table`
# in isolation: `_migrate_tasks_table` finds its markers already present and
# no-ops. `_migrate_lanes_table` runs first in `Ledger.__init__`, and both
# migrations share failpoint names (`after_create`, `after_copy`,
# `after_drop`, `after_rename`), so seeding with `_seed_pre_pr_database`
# instead would trip the *lanes* rebuild's failpoint before the tasks
# rebuild ever starts — silently no-op-ing any attempt to drive a tasks
# checkpoint, and equally hiding a lanes-only rollback assertion inside what


def _seed_pre_is_review_column_database(root: Path):
    """Build a ledger.sqlite3 with the CURRENT lanes/tasks schema (and the
    `one_open_pull_per_source_ref` trigger, if this run created one) but a
    `source_tasks` table that predates `is_review` (agent-supervisor#640).

    Built by opening a real `Ledger` first -- so lanes/tasks and the trigger
    match production exactly, rather than a hand-copied schema that can
    drift from it -- then rebuilding ONLY `source_tasks` back down to the
    pre-#640 shape, the one piece this migration actually touches. Two rows
    are seeded, both `source_kind='pull'` and both terminal (closed) so a
    later dispatch in the SAME test against a different PR never trips
    `one_open_pull_per_source_ref`:

    * `as637-rerev636` (PR 636) -- one of the two rows agent-supervisor#640
      measured by hand and named in `_KNOWN_MISCLASSIFIED_REVIEW_TASK_IDS`;
      must come back `is_review=1` after `Ledger.__init__`'s backfill runs.
    * `as1-fix-999` (PR 999) -- an ordinary, NOT-known-mislabelled row;
      must come back `is_review IS NULL` (untouched) after the same
      `__init__`, proving the backfill is scoped to the two named ids and
      does not touch every legacy row.
    """
    root.mkdir(parents=True, exist_ok=True)
    ledger = Ledger(root, clock=lambda: 1_000)
    ledger.record_dispatch(
        lane="free-1", pane_id="%1", nonce="nonce-1", harness="claude", repo="/repo/free-1",
        server_id="server-a", session_id="$1", command="claude.exe",
        task_id="as637-rerev636", source_kind="pull",
        source_url="https://github.com/jonhill90/agent-supervisor/pull/636", source_ref="636",
        summary="re-review of PR 636's fix", source_state="OPEN",
        evidence=["claimed by dispatch.sh for lane free-1", "pr: 636"], status_marker=None,
    )
    ledger.complete("as637-rerev636", b"done", pane_nonce="nonce-1")
    ledger.record_dispatch(
        lane="free-2", pane_id="%2", nonce="nonce-2", harness="claude", repo="/repo/free-2",
        server_id="server-a", session_id="$2", command="claude.exe",
        task_id="as1-fix-999", source_kind="pull",
        source_url="https://github.com/jonhill90/agent-supervisor/pull/999", source_ref="999",
        summary="fix pass on PR 999", source_state="OPEN",
        evidence=["claimed by dispatch.sh for lane free-2", "pr: 999"], status_marker=None,
    )
    ledger.complete("as1-fix-999", b"done", pane_nonce="nonce-2")

    connection = sqlite3.connect(root / "ledger.sqlite3")
    connection.row_factory = sqlite3.Row
    try:
        connection.execute("PRAGMA foreign_keys = OFF")
        trigger_row = connection.execute(
            "SELECT sql FROM sqlite_master WHERE type='trigger' AND name='one_open_pull_per_source_ref'"
        ).fetchone()
        connection.execute("BEGIN IMMEDIATE")
        connection.execute("DROP TRIGGER IF EXISTS one_open_pull_per_source_ref")
        connection.execute(
            """
            CREATE TABLE source_tasks_pre640 (
                id TEXT PRIMARY KEY,
                source_kind TEXT NOT NULL CHECK (source_kind IN ('issue', 'pull')),
                source_url TEXT NOT NULL,
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
            )
            """
        )
        connection.execute(
            """
            INSERT INTO source_tasks_pre640 (
                id, source_kind, source_url, source_ref, summary, source_state,
                status, evidence_json, status_marker, updated_at
            )
            SELECT id, source_kind, source_url, source_ref, summary, source_state,
                   status, evidence_json, status_marker, updated_at
            FROM source_tasks
            """
        )
        connection.execute("DROP TABLE source_tasks")
        connection.execute("ALTER TABLE source_tasks_pre640 RENAME TO source_tasks")
        if trigger_row is not None:
            connection.execute(trigger_row["sql"])
        connection.commit()
    finally:
        connection.close()



class MutableClock:
    def __init__(self, value=1_000):
        self.value = value

    def __call__(self):
        return self.value


