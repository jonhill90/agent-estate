import contextlib
import hashlib
import concurrent.futures
import json
import os
import socket
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import (  # noqa: E402
    Ledger,
    claim_owner_token,
    lane_population,
    lane_relation,
    lane_relation_from_rows,
    pid_is_alive,
)


# The exact schema `_initialize` wrote at 3af0b431, before `delivery_pending`
# or `delivery_attempted_at` existed. `CREATE TABLE IF NOT EXISTS` never
# rewrites a table that already exists this way, so any ledger created under
# that commit needs `_migrate_tasks_table` to reach the current schema.
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
# looks like a tasks test.
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


class MutableClock:
    def __init__(self, value=1_000):
        self.value = value

    def __call__(self):
        return self.value


class LedgerTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.clock = MutableClock()
        self.ledger = Ledger(Path(self.tempdir.name), clock=self.clock)
        self.ledger.register_lane(
            lane="app-review",
            pane_id="%22",
            nonce="nonce-22-a",
            harness="codex",
            repo="/repo/app",
            server_id="server-a",
            session_id="$4",
            command="codex",
        )

    def seed_source(self, task_id, summary="Review PR 870 without editing", source_state="OPEN"):
        self._source_number = getattr(self, "_source_number", 869) + 1
        self.ledger.reconstruct_task(
            task_id=task_id,
            source_kind="issue",
            source_url=f"https://github.com/jonhill90/Hill90/issues/{self._source_number}",
            source_ref="a" * 40,
            summary=summary,
            source_state=source_state,
            status="created",
            evidence=[],
            status_marker=None,
        )

    def assign(self, task_id="review-870"):
        self.seed_source(task_id)
        return self.ledger.assign(
            task_id=task_id,
            lane="app-review",
            pane_nonce="nonce-22-a",
            summary="Review PR 870 without editing",
        )

    def test_reconstruct_task_allows_a_second_dispatch_of_the_same_issue_under_a_different_task_id(self):
        """agent-dotfiles#144 finding 1. `source_url` used to be declared
        `NOT NULL UNIQUE`, and is derived from the issue number alone
        (`cli.py`'s `record_dispatch`) -- while `reconstruct_task` upserts
        `ON CONFLICT(id)`, a DIFFERENT key. A second dispatch of the same
        issue under a different task id (a lane failing and being
        re-briefed, a follow-up review -- this estate does this constantly)
        raised `UNIQUE constraint failed: source_tasks.source_url`,
        permanently, for that issue.

        The modelling decision (argued in full on `_migrate_source_tasks_table`):
        each `source_tasks` row is one recorded DISPATCH ATTEMPT, several of
        which legitimately share a URL, so the column is no longer unique at
        all -- not scoped to "one open attempt", because this table's status
        is never advanced past 'created' by this recording path and a scoped
        constraint would immediately wedge on exactly the case it exists to
        protect.
        """
        url = "https://github.com/jonhill90/agent-dotfiles/issues/999"
        first = self.ledger.reconstruct_task(
            task_id="ad999-first", source_kind="issue", source_url=url, source_ref="999",
            summary="#999 first attempt", source_state="OPEN", status="created",
            evidence=["claimed by dispatch.sh for lane free-3"], status_marker=None,
        )
        self.assertEqual(url, first["source_url"])

        # Not exotic: re-dispatch under a different task id, same URL.
        second = self.ledger.reconstruct_task(
            task_id="ad999-rereview", source_kind="issue", source_url=url, source_ref="999",
            summary="#999 rereview", source_state="OPEN", status="created",
            evidence=["claimed by dispatch.sh for lane free-4"], status_marker=None,
        )
        self.assertEqual(url, second["source_url"])

        # Both rows survive independently -- this is not an upsert collapsing
        # the two attempts into one.
        self.assertEqual("#999 first attempt", self.ledger.get_source_task("ad999-first")["summary"])
        self.assertEqual("#999 rereview", self.ledger.get_source_task("ad999-rereview")["summary"])
        urls = {row["source_url"] for row in self.ledger.list_source_tasks()}
        self.assertEqual({url}, urls)

    def test_assignment_requires_a_reconstructed_open_github_source(self):
        """Red: current assign() writes free-text summaries with no GitHub
        source check at all -- nothing gates assignment on the GitHub-side
        state actually permitting it."""
        with self.assertRaisesRegex(ValueError, "reconstructed GitHub source"):
            self.ledger.assign(
                task_id="no-source-task", lane="app-review", pane_nonce="nonce-22-a", summary="No source"
            )

        self.seed_source("closed-task", source_state="CLOSED")
        with self.assertRaisesRegex(ValueError, "source is not open"):
            self.ledger.assign(
                task_id="closed-task", lane="app-review", pane_nonce="nonce-22-a", summary="Closed source"
            )

        task = self.assign("open-task")
        self.assertEqual("created", task["status"])

    def test_update_source_task_state_writes_only_the_two_derived_columns(self):
        """agent-supervisor#127: the writer `source_tasks` never had. It must
        touch `source_state`/`status` and nothing this row was dispatched
        with -- `source_url`, `summary`, `evidence` all survive unchanged."""
        self.seed_source("as127-a", summary="Fix the reconciler")
        before = self.ledger.get_source_task("as127-a")

        self.clock.value += 10
        after = self.ledger.update_source_task_state("as127-a", source_state="CLOSED", status="complete")

        self.assertEqual("CLOSED", after["source_state"])
        self.assertEqual("complete", after["status"])
        self.assertEqual(before["source_url"], after["source_url"])
        self.assertEqual(before["summary"], after["summary"])
        self.assertEqual(before["evidence"], after["evidence"])

    def test_update_source_task_state_one_field_leaves_the_other_untouched(self):
        """A caller that could only resolve `source_state` (GitHub answered,
        but no local `tasks` row) or only `status` (GitHub was unreachable)
        must not clobber the column it has no fresh fact for."""
        self.seed_source("as127-b")

        only_state = self.ledger.update_source_task_state("as127-b", source_state="CLOSED")
        self.assertEqual("CLOSED", only_state["source_state"])
        self.assertEqual("created", only_state["status"])

        only_status = self.ledger.update_source_task_state("as127-b", status="complete")
        self.assertEqual("CLOSED", only_status["source_state"])
        self.assertEqual("complete", only_status["status"])

    def test_update_source_task_state_is_idempotent(self):
        """Same values, run twice: the second call must not move `updated_at`
        -- this is what lets a scheduled sweep report zero writes when
        nothing changed."""
        self.seed_source("as127-c")
        self.ledger.update_source_task_state("as127-c", source_state="CLOSED", status="complete")
        with contextlib.closing(self.ledger._connect()) as connection:
            first_updated_at = connection.execute(
                "SELECT updated_at FROM source_tasks WHERE id = ?", ("as127-c",)
            ).fetchone()["updated_at"]

        self.clock.value += 100
        self.ledger.update_source_task_state("as127-c", source_state="CLOSED", status="complete")
        with contextlib.closing(self.ledger._connect()) as connection:
            second_updated_at = connection.execute(
                "SELECT updated_at FROM source_tasks WHERE id = ?", ("as127-c",)
            ).fetchone()["updated_at"]

        self.assertEqual(first_updated_at, second_updated_at)

    def test_update_source_task_state_rejects_an_unsupported_status(self):
        self.seed_source("as127-d")
        with self.assertRaisesRegex(ValueError, "unsupported source task status"):
            self.ledger.update_source_task_state("as127-d", status="delivery_pending")

    def test_update_source_task_state_requires_unknown_task(self):
        with self.assertRaisesRegex(ValueError, "unknown source task"):
            self.ledger.update_source_task_state("no-such-task", source_state="CLOSED")

    def test_one_nonterminal_task_per_lane_and_bound_acceptance(self):
        task = self.assign()
        self.assertEqual("created", task["status"])
        with self.assertRaisesRegex(ValueError, "outstanding task"):
            self.assign("review-871")

        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")
        accepted = self.ledger.accept("review-870", pane_nonce="nonce-22-a")
        self.assertEqual("accepted", accepted["status"])
        with self.assertRaisesRegex(ValueError, "pane incarnation"):
            self.ledger.accept("review-870", pane_nonce="reused-pane")

    def test_lane_available_is_tristate_unknown_free_or_occupied(self):
        """agent-dotfiles#174: `dispatch.sh` now trusts this instead of the
        tmux window name, and the three-way split matters -- an unregistered
        lane is a different claim from a registered-but-busy one, and the
        caller (the CLI's first-sight backfill) needs to tell them apart.
        """
        self.assertIsNone(self.ledger.lane_available("never-registered"))

        self.assertTrue(self.ledger.lane_available("app-review"))

        task = self.assign()
        self.ledger.mark_delivery_pending(task["id"], pane_nonce="nonce-22-a")
        self.ledger.mark_delivered(task["id"], pane_nonce="nonce-22-a")
        self.assertFalse(self.ledger.lane_available("app-review"))

        self.ledger.complete(task["id"], b"done", pane_nonce="nonce-22-a")
        self.assertTrue(self.ledger.lane_available("app-review"))

    def test_delivery_pending_persists_before_send_and_blocks_direct_delivered(self):
        self.assign()
        with self.assertRaisesRegex(ValueError, "cannot transition"):
            self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")

        pending = self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.assertEqual("delivery_pending", pending["status"])
        # Once ambiguous, it stays ambiguous until a transition names an outcome;
        # a second delivery attempt cannot be represented as a fresh "created" task.
        with self.assertRaisesRegex(ValueError, "outstanding task"):
            self.assign("review-871")

        delivered = self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")
        self.assertEqual("delivered", delivered["status"])

    def test_reconcile_delivery_confirms_or_retires_an_ambiguous_task(self):
        self.assign()
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        with self.assertRaisesRegex(ValueError, "outcome"):
            self.ledger.reconcile_delivery("review-870", pane_nonce="nonce-22-a", outcome="bogus")

        confirmed = self.ledger.reconcile_delivery("review-870", pane_nonce="nonce-22-a", outcome="delivered")
        self.assertEqual("delivered", confirmed["status"])

        self.ledger.cancel_open_task("app-review")
        self.assign("review-871")
        self.ledger.mark_delivery_pending("review-871", pane_nonce="nonce-22-a")
        retired = self.ledger.reconcile_delivery("review-871", pane_nonce="nonce-22-a", outcome="failed")
        self.assertEqual("failed", retired["status"])
        # A retired ambiguous task frees the lane for a genuinely new assignment.
        self.assign("review-872")

    def test_completion_requires_the_owning_pane_incarnation(self):
        """Red: complete() takes no pane_nonce at all -- any caller who knows
        a task_id can complete it, regardless of which pane incarnation
        actually owns the lane the task is bound to."""
        self.assign()
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")
        self.ledger.accept("review-870", pane_nonce="nonce-22-a")

        # A different (impersonating) incarnation must not be able to complete
        # a task it does not own.
        with self.assertRaisesRegex(ValueError, "pane incarnation"):
            self.ledger.complete("review-870", b"# Result\n\nDone.\n", pane_nonce="some-other-nonce")

        completed = self.ledger.complete("review-870", b"# Result\n\nDone.\n", pane_nonce="nonce-22-a")
        self.assertEqual("complete", completed["status"])

    def test_fail_unaccepted_terminates_a_delivered_task_with_no_accepted_at(self):
        """agent-supervisor#193's own primitive: a `delivered` task with no
        `accepted_at` gets terminated `failed`, not `complete` -- see
        `fail_unaccepted`'s own docstring for why. Mirrors `complete()`'s
        shape (immutable result, pane-nonce check, idempotent) on purpose."""
        self.assign()
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")

        with self.assertRaisesRegex(ValueError, "pane incarnation"):
            self.ledger.fail_unaccepted("review-870", b"never accepted", pane_nonce="some-other-nonce")

        failed = self.ledger.fail_unaccepted("review-870", b"never accepted", pane_nonce="nonce-22-a")
        self.assertEqual("failed", failed["status"])
        self.assertIsNotNone(failed["completed_at"])
        # Idempotent: a second call with the SAME result is a no-op, not an error.
        again = self.ledger.fail_unaccepted("review-870", b"never accepted", pane_nonce="nonce-22-a")
        self.assertEqual("failed", again["status"])
        # `failed` is terminal -- the lane is free for a fresh dispatch.
        self.assertTrue(self.ledger.lane_available("app-review"))

    def test_fail_unaccepted_refuses_a_task_that_was_actually_accepted(self):
        """The one thing this must never do: erase a GENUINE acceptance.
        `accepted_at` already set is the caller's own evidence being stale or
        wrong -- refuse rather than silently overwrite the record."""
        self.assign()
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")
        self.ledger.accept("review-870", pane_nonce="nonce-22-a")

        with self.assertRaisesRegex(ValueError, "accepted"):
            self.ledger.fail_unaccepted("review-870", b"never accepted", pane_nonce="nonce-22-a")

    def test_fail_unaccepted_refuses_a_task_that_is_not_delivered(self):
        """Only `delivered` is eligible -- a `created` (unsent) or `complete`
        task has no business going through this path at all."""
        task = self.assign()
        with self.assertRaisesRegex(ValueError, "only 'delivered' is eligible"):
            self.ledger.fail_unaccepted(task["id"], b"x", pane_nonce="nonce-22-a")

    def test_record_dispatch_accepted_flag_sets_accepted_at_but_leaves_status_delivered(self):
        """agent-supervisor#193: `accepted=True` is dispatch's OWN evidence
        (see `record_dispatch`'s docstring), stamped as `accepted_at` WITHOUT
        moving `status` to `accepted` -- that status is the self-report
        path's (`Ledger.accept`), and `list_delivered_open_tasks` (the
        completion reconciler's whole candidate set) selects on
        `status='delivered'`. `accepted=False` (the default) must leave
        `accepted_at` null, exactly as every caller before this flag
        existed."""
        unaccepted = self.ledger.record_dispatch(
            lane="app-review", pane_id="%22", nonce="nonce-22-a", harness="codex",
            repo="/repo/app", server_id="server-a", session_id="$2", command="codex",
            task_id="review-900", source_kind="issue",
            source_url="https://github.com/acme/app/issues/900",
            source_ref="900", summary="issue #900", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane app-review", "issues: 900"],
        )["task"]
        self.assertEqual("delivered", unaccepted["status"])
        self.assertIsNone(unaccepted["accepted_at"])

        self.ledger.cancel_open_task("app-review")
        accepted = self.ledger.record_dispatch(
            lane="app-review", pane_id="%22", nonce="nonce-22-b", harness="codex",
            repo="/repo/app", server_id="server-a", session_id="$3", command="codex",
            task_id="review-901", source_kind="issue",
            source_url="https://github.com/acme/app/issues/901",
            source_ref="901", summary="issue #901", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane app-review", "issues: 901"],
            accepted=True,
        )["task"]
        self.assertEqual("delivered", accepted["status"])
        self.assertIsNotNone(accepted["accepted_at"])
        # Directly observable candidate-set effect: still selected by the
        # exact query the completion reconciler runs.
        delivered_ids = {t["id"] for t in self.ledger.list_delivered_open_tasks()}
        self.assertIn("review-901", delivered_ids)

    def test_lane_reregistration_cancels_an_outstanding_non_pending_task_instead_of_rebinding_it(self):
        """register_lane does an unconditional INSERT ... ON CONFLICT UPDATE,
        so re-registering with a CHANGED identity would silently rebind a
        live task's identity out from under it if nothing intervened.
        `delivery_pending` is exempted: it has its own reconciliation escape
        valve keyed off the task's own pane_nonce (see `_reconcile_transition`),
        which #871 relies on to survive a dead pane being re-registered.

        agent-dotfiles#144 finding 3: for every OTHER outstanding status this
        used to raise here, permanently -- the only way out was `lane-done.sh`
        completing that exact task by renaming the window it owned, so a lane
        freed any other way (renamed by hand, a worker that died mid-turn)
        wedged every subsequent register_lane call for that lane forever. A
        changed identity is the evidence that the old task can never complete
        through this pane again, so this now CANCELS the stale task and
        proceeds with the re-registration, rather than wedging."""
        task = self.assign()
        self.assertEqual("created", task["status"])
        registered = self.ledger.register_lane(
            lane="app-review",
            pane_id="%23",
            nonce="nonce-23-b",
            harness="codex",
            repo="/repo/app",
            server_id="server-a",
            session_id="$4",
            command="codex",
        )
        self.assertEqual("nonce-23-b", registered["nonce"])
        self.assertEqual("nonce-23-b", self.ledger.get_lane("app-review")["nonce"])
        stale = self.ledger.get_task("review-870")
        self.assertEqual("cancelled", stale["status"])
        # And the lane is genuinely free again: a new task can be assigned to
        # it under the new incarnation without hitting the one-open-task guard.
        self.seed_source("review-871")
        reassigned = self.ledger.assign(
            task_id="review-871", lane="app-review", pane_nonce="nonce-23-b", summary="Second task"
        )
        self.assertEqual("created", reassigned["status"])

    def test_lane_reregistration_with_the_same_identity_does_not_cancel_the_outstanding_task(self):
        """The cancel-on-reregistration self-heal (#144 finding 3) is scoped
        to a CHANGED identity. Re-registering the exact same pane_id/nonce/
        harness/repo/server_id/session_id/command -- the ordinary no-op path,
        e.g. a duplicate register call -- must leave a genuinely still-running
        task alone."""
        task = self.assign()
        self.assertEqual("created", task["status"])
        self.ledger.register_lane(
            lane="app-review",
            pane_id="%22",
            nonce="nonce-22-a",
            harness="codex",
            repo="/repo/app",
            server_id="server-a",
            session_id="$4",
            command="codex",
        )
        self.assertEqual("created", self.ledger.get_task("review-870")["status"])

    def test_lane_reregistration_does_not_cancel_a_delivery_pending_task(self):
        """#871: a `delivery_pending` task survives re-registration untouched
        -- it has its own reconciliation path keyed off the task's own
        pane_nonce, and must not be silently cancelled by a re-registration
        racing an in-flight send."""
        self.assign()
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.register_lane(
            lane="app-review",
            pane_id="%23",
            nonce="nonce-23-b",
            harness="codex",
            repo="/repo/app",
            server_id="server-a",
            session_id="$4",
            command="codex",
        )
        self.assertEqual("delivery_pending", self.ledger.get_task("review-870")["status"])

    def test_completion_is_immutable_idempotent_and_event_is_exactly_once(self):
        self.assign()
        result = b"# Result\n\nNo findings.\n"
        completed = self.ledger.complete("review-870", result, pane_nonce="nonce-22-a")
        self.assertEqual("complete", completed["status"])
        self.assertEqual(hashlib.sha256(result).hexdigest(), completed["result_sha256"])

        repeated = self.ledger.complete("review-870", result, pane_nonce="nonce-22-a")
        self.assertEqual(completed["result_sha256"], repeated["result_sha256"])
        events = self.ledger.list_events(task_id="review-870", event_type="completion")
        self.assertEqual(["completion:review-870"], [event["key"] for event in events])
        with self.assertRaisesRegex(ValueError, "immutable result"):
            self.ledger.complete("review-870", b"different result\n", pane_nonce="nonce-22-a")

    def test_completion_reconciles_each_injected_crash_point(self):
        result = b"# Evidence\n\nchecks passed\n"
        for failpoint in ("after_result", "after_task", "after_event"):
            with self.subTest(failpoint=failpoint):
                task_id = f"task-{failpoint}"
                if failpoint != "after_result":
                    self.ledger.cancel_open_task("app-review")
                self.assign(task_id)
                with self.assertRaisesRegex(RuntimeError, failpoint):
                    self.ledger.complete(task_id, result, pane_nonce="nonce-22-a", failpoint=failpoint)
                recovered = self.ledger.complete(task_id, result, pane_nonce="nonce-22-a")
                self.assertEqual("complete", recovered["status"])
                events = self.ledger.list_events(task_id=task_id, event_type="completion")
                self.assertEqual(1, len(events))

    def test_record_dispatch_is_atomic_across_every_injected_crash_point(self):
        """agent-dotfiles#144 finding 2. `record_dispatch` used to make five
        independent `Ledger` calls -- register_lane, reconstruct_task, assign,
        mark_delivery_pending, mark_delivered -- each its own lock and
        transaction. A crash between any two left whatever had already
        committed: the review reproduced this as an orphan `lanes` row (the
        lane registered, nothing else recorded) claiming a lane occupied for a
        dispatch nothing else records. That is exactly the state this layer
        exists to prevent, so this parametrises over EVERY step, not just the
        one the reviewer happened to hit.

        A failure at ANY step must leave NO lane row, NO task row and NO
        source_tasks row for that attempt -- not a partial write, not an
        orphan claiming the lane is busy. A clean retry afterwards must then
        succeed exactly as if the failed attempt never happened.
        """
        failpoints = (
            "after_register_lane",
            "after_reconstruct_task",
            "after_assign",
            "after_mark_delivery_pending",
            "after_mark_delivered",
        )
        for failpoint in failpoints:
            with self.subTest(failpoint=failpoint):
                lane = f"lane-{failpoint}"
                task_id = f"task-{failpoint}"
                url = f"https://github.com/jonhill90/agent-dotfiles/issues/{hash(failpoint) % 10_000}"
                kwargs = dict(
                    lane=lane,
                    pane_id="%9",
                    nonce="nonce-9",
                    harness="codex",
                    repo="/repo/app",
                    server_id="server-a",
                    session_id="$9",
                    command="codex",
                    task_id=task_id,
                    source_kind="issue",
                    source_url=url,
                    source_ref="999",
                    summary="Atomicity check",
                    source_state="OPEN",
                    evidence=["seeded"],
                    status_marker=None,
                )
                with self.assertRaisesRegex(RuntimeError, failpoint):
                    self.ledger.record_dispatch(**kwargs, failpoint=failpoint)

                # THE assertion: no partial write survives the crash.
                self.assertIsNone(self.ledger.get_lane(lane), f"orphan lane row survived {failpoint}")
                self.assertIsNone(self.ledger.get_task(task_id), f"orphan task row survived {failpoint}")
                self.assertIsNone(
                    self.ledger.get_source_task(task_id), f"orphan source_tasks row survived {failpoint}"
                )

                # And a clean retry afterwards succeeds normally -- the crash
                # left nothing behind to conflict with it.
                result = self.ledger.record_dispatch(**kwargs)
                self.assertEqual("nonce-9", result["lane"]["nonce"])
                self.assertEqual("delivered", result["task"]["status"])

    def test_record_dispatch_succeeds_end_to_end_and_matches_the_five_call_sequence(self):
        """Not just atomic -- still does the same work the five-call sequence
        used to, in the same order, so a caller (`cli.py`'s `record_dispatch`)
        that switches to this one call sees identical results."""
        result = self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-3",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="ad999-first",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-dotfiles/issues/999",
            source_ref="999",
            summary="#999 first",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3", "issues: 999"],
            status_marker=None,
        )
        self.assertEqual("nonce-3", result["lane"]["nonce"])
        self.assertEqual("claude", result["lane"]["harness"])
        self.assertEqual("delivered", result["task"]["status"])
        self.assertIsNotNone(result["task"]["delivered_at"])
        source = self.ledger.get_source_task("ad999-first")
        self.assertEqual("https://github.com/jonhill90/agent-dotfiles/issues/999", source["source_url"])

    def test_record_dispatch_records_the_originating_project_dir_alongside_the_session_id(self):
        """agent-supervisor#172. `repo` is the lane's WORKING directory (a
        worktree); `harness_project_dir` is the directory the harness process
        was actually LAUNCHED in, which `restore.sh` needs because
        `claude --resume` is scoped to it, not to `repo`. This is the
        red-before-the-fix case: a lane whose two directories genuinely
        differ, which the pre-#172 code had nowhere to record at all."""
        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-3",
            harness="claude",
            repo="/repo/free-3-worktree",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="ad999-first",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-dotfiles/issues/999",
            source_ref="999",
            summary="#999 first",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3", "issues: 999"],
            status_marker=None,
            harness_session_id="7cef6d59-0000-4000-8000-000000000000",
            harness_project_dir="/repo/free-3-originating",
        )
        lane = self.ledger.get_lane("free-3")
        self.assertEqual("/repo/free-3-worktree", lane["repo"])
        self.assertEqual("/repo/free-3-originating", lane["harness_project_dir"])
        self.assertNotEqual(lane["repo"], lane["harness_project_dir"])

        plan = {row["lane"]: row for row in self.ledger.restore_plan()}
        self.assertEqual("/repo/free-3-originating", plan["free-3"]["harness_project_dir"])
        self.assertEqual("/repo/free-3-worktree", plan["free-3"]["repo"])

    def test_a_changed_pane_identity_clears_harness_project_dir_with_the_session_id(self):
        """The pairing rule: `harness_project_dir` must never survive a
        re-registration that clears `harness_session_id`, or a stale
        directory would sit next to a session id that no longer names it --
        exactly the mismatch #172 exists to prevent, just introduced from the
        other direction."""
        self.ledger.register_lane(
            lane="free-3", pane_id="%3", nonce="nonce-3", harness="claude",
            repo="/repo/free-3", server_id="server-a", session_id="$3", command="claude.exe",
            harness_session_id="7cef6d59-0000-4000-8000-000000000000",
            harness_project_dir="/repo/free-3-originating",
        )
        before = self.ledger.get_lane("free-3")
        self.assertEqual("/repo/free-3-originating", before["harness_project_dir"])

        # A different pane_id is a new incarnation (`_register_lane_tx`'s own
        # `changed_identity` test) -- the old conversation's directory must
        # not be carried forward onto a process that was never launched in it.
        self.ledger.register_lane(
            lane="free-3", pane_id="%99", nonce="nonce-99", harness="claude",
            repo="/repo/free-3", server_id="server-a", session_id="$3", command="claude.exe",
        )
        after = self.ledger.get_lane("free-3")
        self.assertEqual("", after["harness_session_id"])
        self.assertEqual("", after["harness_project_dir"])

    def test_get_task_for_issue_resolves_by_issue_never_by_branch(self):
        """agent-supervisor#35: `dispatch.sh`'s `--reviews-pr` guard used to
        determine a PR's author by regexing its head branch. The ledger
        already carries the fact this needs -- `source_tasks.source_ref` is
        the issue number `record_dispatch` was called FOR (see that
        function's own docstring) -- so this reads it back keyed on the
        issue, with no branch name involved anywhere in this test."""
        self.assertIsNone(self.ledger.get_task_for_issue("501"))

        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-3",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="ad501-scrub-secrets",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-dotfiles/issues/501",
            source_ref="501",
            summary="#501 scrub secrets",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3"],
            status_marker=None,
        )
        found = self.ledger.get_task_for_issue("501")
        self.assertEqual("free-3", found["lane"])
        self.assertEqual("ad501-scrub-secrets", found["id"])

        # An int and its string spelling are the same issue -- callers pass
        # both (dispatch.sh always passes a string; cli.py's own argparse
        # default for --issue is a str too, but this must not be brittle
        # about the caller's type).
        self.assertEqual("ad501-scrub-secrets", self.ledger.get_task_for_issue(501)["id"])

        # A DIFFERENT issue number, even one that shares a prefix, is not a
        # match -- this is not a substring or LIKE lookup.
        self.assertIsNone(self.ledger.get_task_for_issue("5011"))
        self.assertIsNone(self.ledger.get_task_for_issue("50"))

    def test_get_task_for_issue_prefers_the_most_recent_dispatch(self):
        """An issue re-dispatched after a prior task finished (recycled, or
        handed to a second lane) has more than one `source_tasks` row for
        the same issue -- the most recent one is the lane that actually
        holds it now, not the first lane that ever touched it."""
        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-3",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="ad502-first-attempt",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-dotfiles/issues/502",
            source_ref="502",
            summary="#502 first attempt",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3"],
            status_marker=None,
        )
        self.ledger.cancel_open_task("free-3")
        self.clock.value += 1

        self.ledger.record_dispatch(
            lane="free-4",
            pane_id="%4",
            nonce="nonce-4",
            harness="claude",
            repo="/repo/free-4",
            server_id="server-a",
            session_id="$4",
            command="claude.exe",
            task_id="ad502-second-attempt",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-dotfiles/issues/502",
            source_ref="502",
            summary="#502 second attempt",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-4"],
            status_marker=None,
        )

        found = self.ledger.get_task_for_issue("502")
        self.assertEqual("free-4", found["lane"])
        self.assertEqual("ad502-second-attempt", found["id"])

    def test_get_open_task_for_pr_resolves_by_pr_never_by_issue(self):
        """agent-supervisor#159: a PR-scoped dispatch (a review, or a fix
        pass, on PR N while the issue it closes stays claimed by the
        in-flight work that opened it) is recorded `source_kind='pull'`,
        keyed by the PR number -- never the issue. This is the read side,
        the one `dispatch.sh` asks BEFORE picking a lane so a second
        dispatcher can see the PR is already spoken for instead of minting a
        second task for it (the measured "...b" suffix duplication)."""
        self.assertIsNone(self.ledger.get_open_task_for_pr("149"))

        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-3",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="as159-rev149",
            source_kind="pull",
            source_url="https://github.com/jonhill90/agent-supervisor/pull/149",
            source_ref="149",
            summary="review PR #149",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3", "pr: 149"],
            status_marker=None,
        )
        found = self.ledger.get_open_task_for_pr("149")
        self.assertEqual("free-3", found["lane"])
        self.assertEqual("as159-rev149", found["id"])

        # An int and its string spelling are the same PR, the same as
        # `get_task_for_issue` above.
        self.assertEqual("as159-rev149", self.ledger.get_open_task_for_pr(149)["id"])

        # This task's issue (say, #112, the review's own tracking issue) was
        # never claimed as its SOURCE -- `issue-lane` for it must not answer
        # with this task.
        self.assertIsNone(self.ledger.get_task_for_issue("112"))

    def test_get_open_task_for_pr_ignores_completed_or_cancelled_tasks(self):
        """UNLIKE `get_task_for_issue` (whose only caller today is a
        diagnostic query with no live effect), this is asked by
        `dispatch.sh` step 0.6 as a REFUSAL gate before a lane is even
        picked -- so it must not go on refusing every future dispatch of the
        same PR forever just because one prior review of it finished or was
        cancelled."""
        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-3",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="as159-rev150-first",
            source_kind="pull",
            source_url="https://github.com/jonhill90/agent-supervisor/pull/150",
            source_ref="150",
            summary="review PR #150",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3", "pr: 150"],
            status_marker=None,
        )
        row = self.ledger.get_task("as159-rev150-first")
        self.ledger.complete("as159-rev150-first", b"approved", pane_nonce=row["pane_nonce"])

        self.assertIsNone(self.ledger.get_open_task_for_pr("150"))

    def test_record_dispatch_refuses_a_second_open_dispatch_of_the_same_pr(self):
        """agent-supervisor#169: `dispatch.sh` step 0.6's `pr-lane` read is a
        TOCTOU by itself -- two dispatchers can both read "not yet claimed"
        before either one's `record_dispatch` write lands, seconds later.
        This is the write-side gate that closes it: the SECOND writer's
        INSERT into `source_tasks` must fail atomically, not merely be
        caught by an earlier read that a race can outrun."""
        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-a",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="as169-race-a",
            source_kind="pull",
            source_url="https://github.com/jonhill90/agent-supervisor/pull/960",
            source_ref="960",
            summary="fix pass on PR #960",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3", "pr: 960"],
            status_marker=None,
        )
        with self.assertRaises(sqlite3.IntegrityError) as exc:
            self.ledger.record_dispatch(
                lane="free-4",
                pane_id="%4",
                nonce="nonce-b",
                harness="claude",
                repo="/repo/free-4",
                server_id="server-a",
                session_id="$4",
                command="claude.exe",
                task_id="as169-race-b",
                source_kind="pull",
                source_url="https://github.com/jonhill90/agent-supervisor/pull/960",
                source_ref="960",
                summary="a second dispatcher also tries PR #960",
                source_state="OPEN",
                evidence=["claimed by dispatch.sh for lane free-4", "pr: 960"],
                status_marker=None,
            )
        self.assertIn("source_tasks.source_ref", str(exc.exception))

        # The whole five-write transaction rolled back -- the losing
        # dispatcher's task was never created, not left half-written.
        self.assertIsNone(self.ledger.get_task("as169-race-b"))
        # And the winner is exactly, and only, the first writer.
        self.assertEqual("free-3", self.ledger.get_open_task_for_pr("960")["lane"])

        # A CLOSED prior dispatch of the same PR never blocks a fresh one --
        # the index is scoped to open rows, same as `get_open_task_for_pr`'s
        # own filter.
        row = self.ledger.get_task("as169-race-a")
        self.ledger.complete("as169-race-a", b"done", pane_nonce=row["pane_nonce"])
        self.ledger.record_dispatch(
            lane="free-4",
            pane_id="%4",
            nonce="nonce-c",
            harness="claude",
            repo="/repo/free-4",
            server_id="server-a",
            session_id="$4",
            command="claude.exe",
            task_id="as169-race-c",
            source_kind="pull",
            source_url="https://github.com/jonhill90/agent-supervisor/pull/960",
            source_ref="960",
            summary="a fresh dispatch, after the first one closed",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-4", "pr: 960"],
            status_marker=None,
        )
        self.assertEqual("free-4", self.ledger.get_open_task_for_pr("960")["lane"])

    def test_get_author_task_for_issue_does_not_drift_to_later_reviews(self):
        """agent-supervisor#76: review tasks for the same issue must never
        become the author. The bug is drift, so this asserts after each new
        review dispatch instead of checking only the final ledger shape."""

        def dispatch(lane, task_id, summary):
            self.ledger.record_dispatch(
                lane=lane,
                pane_id=f"%{lane.rsplit('-', 1)[-1]}",
                nonce=f"nonce-{task_id}",
                harness="claude",
                repo=f"/repo/{lane}",
                server_id="server-a",
                session_id="$3",
                command="claude.exe",
                task_id=task_id,
                source_kind="issue",
                source_url="https://github.com/jonhill90/agent-supervisor/issues/76",
                source_ref="76",
                summary=summary,
                source_state="OPEN",
                evidence=[f"claimed by dispatch.sh for lane {lane}"],
                status_marker=None,
            )
            self.ledger.complete(task_id, b"# Result\n\nDone.\n", pane_nonce=f"nonce-{task_id}")
            self.clock.value += 1

        dispatch("free-3", "as76-author-lane-drift", "#76 fix author resolver")
        self.assertEqual("free-3", self.ledger.get_author_task_for_issue("76")["lane"])

        for lane, task_id in [
            ("free-4", "as76-review-as73"),
            ("free-5", "as76-rev73b"),
            ("free-6", "as76-review-as73c"),
            ("free-7", "as76-review-as73d"),
        ]:
            dispatch(lane, task_id, "#76 review PR #73")
            author = self.ledger.get_author_task_for_issue("76")
            self.assertEqual("free-3", author["lane"])
            self.assertEqual("as76-author-lane-drift", author["id"])

    def test_get_author_task_for_issue_resolves_by_head_ref_after_redispatch(self):
        """agent-supervisor#77: the reviewer's own reproduction -- a first
        attempt abandoned, then a second lane re-dispatched for the same
        issue actually produces the PR. Resolving to "the first non-review
        task" (the old rule) picks the stale, abandoned lane; this must
        resolve to whichever lane the PR's head branch names instead."""
        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-first",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="as13-as13-ci-as-a-gate",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/13",
            source_ref="13",
            summary="#13 first attempt",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3"],
            status_marker=None,
        )
        self.ledger.complete("as13-as13-ci-as-a-gate", b"# Result\n\nAbandoned.\n", pane_nonce="nonce-first")
        self.clock.value += 1

        self.ledger.record_dispatch(
            lane="free-5",
            pane_id="%5",
            nonce="nonce-second",
            harness="claude",
            repo="/repo/free-5",
            server_id="server-a",
            session_id="$5",
            command="claude.exe",
            task_id="as13-as13-reopen",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/13",
            source_ref="13",
            summary="#13 reopened attempt",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-5"],
            status_marker=None,
        )
        self.ledger.complete("as13-as13-reopen", b"# Result\n\nDone.\n", pane_nonce="nonce-second")

        # No head ref, two non-review candidates: genuinely ambiguous.
        self.assertIsNone(self.ledger.get_author_task_for_issue("13"))

        # The old "first" rule would answer free-3 here -- exactly the wrong
        # lane the reviewer caught. The head ref names the lane that actually
        # produced the PR.
        author = self.ledger.get_author_task_for_issue("13", head_ref="lane/13-as13-reopen")
        self.assertEqual("free-5", author["lane"])
        self.assertEqual("as13-as13-reopen", author["id"])

    def test_get_author_task_for_issue_unknown_when_only_reviews_exist(self):
        self.ledger.record_dispatch(
            lane="free-4",
            pane_id="%4",
            nonce="nonce-review-only",
            harness="claude",
            repo="/repo/free-4",
            server_id="server-a",
            session_id="$4",
            command="claude.exe",
            task_id="as76-review-as73",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/76",
            source_ref="76",
            summary="#76 review PR #73",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-4"],
            status_marker=None,
        )
        self.assertIsNone(self.ledger.get_author_task_for_issue("76"))

    def test_get_author_task_for_issue_survives_completion(self):
        """agent-supervisor#90: the guard added by #70 (and generalised by
        #76/#77) refused a review dispatched back to its author's lane, but
        only while that lane's task read as still open -- `record-completion`
        runs on every lane as routine reconciliation, and closing the task
        made the same dispatch land on the author.

        This query has no status filter at all -- unlike `lane_available` /
        `get_open_task_for_lane` (which exist specifically to answer "is this
        lane busy right now") -- so a task moving to `complete` must not
        change the answer here, the same way #79/#80 already proved
        `cancelled` does not. The mutation below is `dispatch.sh`'s own
        author-lookup query with a status filter spliced in -- exactly the
        defect this test exists to catch -- and confirms this test would
        have gone red against it."""
        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-90",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="as90-authorship-outlives-task",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/90",
            source_ref="90",
            summary="#90 authorship outlives task",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3"],
            status_marker=None,
        )
        row = self.ledger.get_task("as90-authorship-outlives-task")
        self.ledger.complete("as90-authorship-outlives-task", b"# Result\n\ndone\n", pane_nonce=row["pane_nonce"])

        author = self.ledger.get_author_task_for_issue("90")
        self.assertIsNotNone(author, "authorship must not go unknown just because the task completed")
        self.assertEqual("free-3", author["lane"])
        self.assertEqual("as90-authorship-outlives-task", author["id"])

        # RED CHECK: an "only open tasks" mutation of the same query -- the
        # bug #90 reported -- must lose this exact answer. If it did not,
        # the assertions above would not be exercising the status-independence
        # they claim to.
        with contextlib.closing(self.ledger._connect()) as connection:
            mutant_row = connection.execute(
                """
                SELECT tasks.* FROM tasks
                JOIN source_tasks ON source_tasks.id = tasks.id
                WHERE source_tasks.source_kind = 'issue' AND source_tasks.source_ref = ?
                  AND tasks.status NOT IN ('complete', 'failed', 'cancelled')
                ORDER BY tasks.created_at ASC, tasks.id ASC
                """,
                ("90",),
            ).fetchone()
        self.assertIsNone(
            mutant_row,
            "mutation sanity check failed: the 'only open tasks' variant should find nothing "
            "for a completed author task -- if it found one, this test cannot tell the fix from the bug",
        )

    def test_get_contributor_tasks_for_issue_includes_every_non_review_task(self):
        """agent-supervisor#190: `get_author_task_for_issue` narrows multiple
        non-review candidates for the same issue down to one (or `None`
        when it cannot). `get_contributor_tasks_for_issue` must NOT narrow --
        a fix-pass task dispatched against the same issue as an original
        author (both non-review) belongs in the CONTRIBUTOR SET dispatch.sh
        excludes a review from, even though `get_author_task_for_issue`
        itself would refuse to pick between them."""

        def dispatch(lane, task_id, summary):
            self.ledger.record_dispatch(
                lane=lane,
                pane_id=f"%{lane.rsplit('-', 1)[-1]}",
                nonce=f"nonce-{task_id}",
                harness="claude",
                repo=f"/repo/{lane}",
                server_id="server-a",
                session_id="$3",
                command="claude.exe",
                task_id=task_id,
                source_kind="issue",
                source_url="https://github.com/jonhill90/agent-supervisor/issues/190",
                source_ref="190",
                summary=summary,
                source_state="OPEN",
                evidence=[f"claimed by dispatch.sh for lane {lane}"],
                status_marker=None,
            )
            self.clock.value += 1

        dispatch("free-3", "as190-original-author", "#190 original fix")
        dispatch("free-4", "as190-fix-pass", "#190 fix pass after review findings")
        dispatch("free-5", "as190-review-only", "#190 review PR #460")

        contributors = self.ledger.get_contributor_tasks_for_issue("190")
        lanes = {row["lane"] for row in contributors}
        self.assertEqual({"free-3", "free-4"}, lanes, "both non-review tasks are contributors")
        task_ids = {row["id"] for row in contributors}
        self.assertNotIn("as190-review-only", task_ids, "a review task is never a contributor")

        # Unlike get_contributor_tasks_for_issue, the single-author lookup
        # for the SAME issue is genuinely ambiguous here (two non-review
        # candidates, no head_ref to disambiguate) and correctly answers
        # "don't know" -- proving the two methods answer different
        # questions rather than one silently reusing the other's narrowing.
        self.assertIsNone(self.ledger.get_author_task_for_issue("190"))

    def test_get_contributor_tasks_for_issue_unknown_issue_is_empty(self):
        self.assertEqual([], self.ledger.get_contributor_tasks_for_issue("no-such-issue"))

    def test_get_task_for_worktree_resolves_when_the_branch_slug_differs_from_the_task_slug(self):
        """agent-supervisor#117: the actual, measured bug. Task
        `as101-pr-inference-fix` produced a PR whose head branch was
        `fix/101-not-a-review-escape` -- a slug sharing no text with the
        dispatch's own -- so nothing reconstructed from that branch name
        could ever find this task. The worktree path is not renamed the way
        the branch is, so recording it at dispatch time and looking it back
        up here resolves this regardless of what the branch was later
        called."""
        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-117",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="as101-pr-inference-fix",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/101",
            source_ref="101",
            summary="#101 pr-inference-fix; worktree=/tmp/ad-101-pr-inference-fix-42",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3"],
            status_marker=None,
            worktree_path="/tmp/ad-101-pr-inference-fix-42",
        )

        found = self.ledger.get_task_for_worktree("/tmp/ad-101-pr-inference-fix-42")
        self.assertIsNotNone(found)
        self.assertEqual("free-3", found["lane"])
        self.assertEqual("as101-pr-inference-fix", found["id"])

    def test_get_task_for_worktree_unknown_for_an_unrecorded_path(self):
        self.assertIsNone(self.ledger.get_task_for_worktree("/tmp/never-recorded"))

    def test_get_task_for_worktree_unknown_for_a_blank_path(self):
        """A pre-#117 row's `worktree_path` reads '' (see
        `_migrate_tasks_table`), and matching '' against another blank path
        would wrongly declare every such row the same worktree."""
        self.assertIsNone(self.ledger.get_task_for_worktree(""))

    def test_get_task_for_worktree_never_returns_a_review_task(self):
        """agent-supervisor#76's rule holds here too: a review task must
        never stand in as the author, even if (implausibly) it shared a
        worktree path with the real one."""
        self.ledger.record_dispatch(
            lane="free-4",
            pane_id="%4",
            nonce="nonce-review",
            harness="claude",
            repo="/repo/free-4",
            server_id="server-a",
            session_id="$4",
            command="claude.exe",
            task_id="as101-review-as114",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/101",
            source_ref="101",
            summary="#101 review PR #114",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-4"],
            status_marker=None,
            worktree_path="/tmp/ad-101-review-as114-7",
        )
        self.assertIsNone(self.ledger.get_task_for_worktree("/tmp/ad-101-review-as114-7"))

    def test_mark_lane_held_makes_a_free_lane_read_occupied(self):
        """agent-dotfiles#188 finding 1: this is what closes the window a
        rolled-back `record_dispatch` used to leave open -- a lane the ledger
        already had registered free must not go on reading free just because
        the write that was supposed to claim it failed."""
        self.assertTrue(self.ledger.lane_available("app-review"))
        held = self.ledger.mark_lane_held("app-review", note="record_dispatch failed for task review-870")
        self.assertEqual("created", held["status"])
        self.assertEqual("app-review", held["lane"])
        self.assertFalse(self.ledger.lane_available("app-review"))

    def test_mark_lane_held_on_an_unknown_lane_is_a_no_op(self):
        """Nothing to close: an unregistered lane already reads `None`
        (unknown), and unknown is not free -- `dispatch.sh`'s `lane-free`
        never offers it. Writing a placeholder task here would need a
        `lanes` row that does not exist and violate the FK."""
        self.assertIsNone(self.ledger.lane_available("never-registered"))
        self.assertIsNone(self.ledger.mark_lane_held("never-registered", note="unused"))
        self.assertIsNone(self.ledger.lane_available("never-registered"))

    def test_mark_lane_held_on_an_already_occupied_lane_is_a_no_op(self):
        """The lane is already exactly as unavailable as this method would
        make it -- the one_open_task_per_lane index refuses the second
        placeholder, and that refusal is fine: it means something else
        already guarantees the property this method exists for."""
        self.assign()
        self.assertFalse(self.ledger.lane_available("app-review"))
        self.assertIsNone(self.ledger.mark_lane_held("app-review", note="unused"))
        self.assertFalse(self.ledger.lane_available("app-review"))

    def test_record_dispatch_failure_leaves_the_lane_held_not_free(self):
        """The mutation this guards against, end to end: seed a lane already
        free, force `record_dispatch` to fail deep inside its transaction
        (the same `assign` collision #144's own atomicity test uses), and
        confirm the failure does not leave the pre-existing free row intact.
        Without `mark_lane_held` wired into the caller, this goes red -- the
        rollback restores exactly the free row asserted at the top."""
        self.assertTrue(self.ledger.lane_available("app-review"))
        self.seed_source("collide-870")
        self.ledger.assign(
            task_id="collide-870", lane="app-review", pane_nonce="nonce-22-a", summary="already here"
        )
        self.ledger.cancel_open_task("app-review")
        self.assertTrue(self.ledger.lane_available("app-review"))
        # A second dispatch racing the first tries to reuse "collide-870" for
        # a DIFFERENT lane -- `_assign_tx` refuses it, `record_dispatch`
        # rolls back every write it had made for "app-review" so far, and
        # the pre-existing free row for "app-review" survives the rollback
        # untouched, exactly the #188 finding 1 scenario.
        with self.assertRaisesRegex(ValueError, "already exists with different assignment"):
            self.ledger.record_dispatch(
                lane="app-review",
                pane_id="%22",
                nonce="nonce-22-b",
                harness="codex",
                repo="/repo/app",
                server_id="server-a",
                session_id="$4",
                command="codex",
                task_id="collide-870",
                source_kind="issue",
                source_url="https://github.com/jonhill90/agent-dotfiles/issues/870",
                source_ref="870",
                summary="a different task body entirely",
                source_state="OPEN",
                evidence=["claimed by dispatch.sh for lane app-review"],
                status_marker=None,
            )
        self.assertTrue(self.ledger.lane_available("app-review"))  # the bug, absent the fix
        self.ledger.mark_lane_held("app-review", note="record_dispatch failed for task collide-870")
        self.assertFalse(self.ledger.lane_available("app-review"))  # closed

    def test_idle_attention_is_level_triggered_until_task_disposition(self):
        self.assign()
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")
        self.ledger.accept("review-870", pane_nonce="nonce-22-a")
        event = self.ledger.observe_idle("app-review", pane_nonce="nonce-22-a")
        self.assertEqual("attention:review-870", event["key"])
        self.ledger.mark_notified([event["key"]], retry_after=60)
        self.assertEqual([], self.ledger.events_due(now=1_059))
        self.assertEqual([event["key"]], [item["key"] for item in self.ledger.events_due(now=1_060)])
        with self.assertRaisesRegex(ValueError, "task disposition"):
            self.ledger.ack([event["key"]])

        self.ledger.complete("review-870", b"# Result\n\nDone.\n", pane_nonce="nonce-22-a")
        self.ledger.ack([event["key"]])
        self.assertEqual("acked", self.ledger.get_event(event["key"])["status"])

    def test_failed_component_collection_does_not_advance_baseline(self):
        first = self.ledger.record_component("github", snapshot=b"head-a\n", healthy=True)
        self.assertEqual(hashlib.sha256(b"head-a\n").hexdigest(), first["snapshot_sha256"])
        failed = self.ledger.record_component("github", healthy=False, error="timeout")
        self.assertEqual(first["snapshot_sha256"], failed["snapshot_sha256"])
        recovered = self.ledger.record_component("github", snapshot=b"head-b\n", healthy=True)
        self.assertNotEqual(first["snapshot_sha256"], recovered["snapshot_sha256"])

    def test_snapshot_changes_emit_one_bounded_diff_event(self):
        self.assertIsNone(self.ledger.record_snapshot("github", b"pr=870 pending\n"))
        self.assertIsNone(self.ledger.record_snapshot("github", b"pr=870 pending\n"))
        event = self.ledger.record_snapshot("github", b"pr=870 success\n")
        self.assertEqual("sensor", event["type"])
        self.assertTrue(event["key"].startswith("sensor:github:"))
        payload = Path(event["payload_path"]).read_text()
        self.assertIn("-pr=870 pending", payload)
        self.assertIn("+pr=870 success", payload)
        repeated = self.ledger.record_snapshot("github", b"pr=870 success\n")
        self.assertIsNone(repeated)
        self.assertEqual(1, len(self.ledger.list_events(event_type="sensor")))

        large = b"x" * (80 * 1024)
        truncated = self.ledger.record_snapshot("github", large)
        bounded = Path(truncated["payload_path"]).read_bytes()
        self.assertLessEqual(len(bounded), 64 * 1024)
        self.assertIn(b"[DIFF TRUNCATED]", bounded)

    def test_codex_and_claude_lanes_share_schema_but_keep_adapter_identity(self):
        self.ledger.register_lane(
            lane="infra-claude",
            pane_id="%8",
            nonce="nonce-8-a",
            harness="claude",
            repo="/repo/hill90",
            server_id="server-a",
            session_id="$4",
            command="claude.exe",
        )
        codex_lane = self.ledger.get_lane("app-review")
        claude_lane = self.ledger.get_lane("infra-claude")
        self.assertEqual(set(codex_lane), set(claude_lane))
        self.assertEqual("codex", codex_lane["harness"])
        self.assertEqual("claude", claude_lane["harness"])
        self.assertNotEqual(codex_lane["nonce"], claude_lane["nonce"])

    def test_copilot_acp_is_a_registerable_harness(self):
        """Red: register_lane's Python-level check and the lanes table's CHECK
        constraint both still hard-code ('codex', 'claude') -- a copilot-acp
        lane cannot be registered at all, so ACPTransport has no lane to
        dispatch through."""
        lane = self.ledger.register_lane(
            lane="copilot-worker",
            pane_id="session-1",
            nonce="nonce-acp",
            harness="copilot-acp",
            repo="/repo/app",
            server_id="acp",
            session_id="session-1",
            command="copilot",
        )
        self.assertEqual("copilot-acp", lane["harness"])
        self.assertEqual("copilot-acp", self.ledger.get_lane("copilot-worker")["harness"])

    def test_concurrent_assignments_leave_exactly_one_open_task(self):
        self.seed_source("race-a", summary="Task race-a")
        self.seed_source("race-b", summary="Task race-b")

        def assign(task_id):
            ledger = Ledger(Path(self.tempdir.name), clock=self.clock)
            try:
                ledger.assign(
                    task_id=task_id,
                    lane="app-review",
                    pane_nonce="nonce-22-a",
                    summary=f"Task {task_id}",
                )
                return "created"
            except ValueError as error:
                return str(error)

        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            outcomes = list(pool.map(assign, ("race-a", "race-b")))
        self.assertEqual(1, outcomes.count("created"))
        self.assertEqual(1, sum("outstanding task" in outcome for outcome in outcomes))
        open_tasks = [task for task in self.ledger.list_tasks() if task["status"] not in ("complete", "failed", "cancelled")]
        self.assertEqual(1, len(open_tasks))


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


class SourceTasksPullUniquenessMigrationTest(unittest.TestCase):
    """agent-supervisor#169: `one_open_pull_per_source_ref` is the write-time
    gate that closes the PR-duplicate TOCTOU step 0.6's read cannot -- see
    `Ledger._migrate_source_tasks_pull_uniqueness`. It is a TRIGGER, not a
    plain partial index (`source_tasks.status` never advances past
    'created', so an index keyed on it would wrongly block a fresh dispatch
    after a prior one legitimately completed -- see that migration's own
    docstring for the measured reason). Adding a trigger needs no table
    rebuild (unlike `SourceTasksMigrationTest` above), but an existing
    ledger can already carry the exact duplicate this fix exists to
    prevent, and the migration must not silently drop or pick a winner
    among them."""

    def _raw_trigger_sql(self, root):
        connection = sqlite3.connect(root / "ledger.sqlite3")
        try:
            return connection.execute(
                "SELECT sql FROM sqlite_master WHERE type='trigger' AND name='one_open_pull_per_source_ref'"
            ).fetchone()
        finally:
            connection.close()

    def _seed_duplicate_open_pull_rows(self, root):
        """A ledger in exactly the state #157/#149/#181 left one in: two
        OPEN `pull`-kind dispatches (`tasks` + `source_tasks` rows, written
        the same way `record_dispatch` always writes them) for the same PR,
        from before this migration ever ran. Built by opening a real
        (fresh, correctly migrated) `Ledger` once, then dropping the
        trigger and dispatching the second, colliding row through the same
        `record_dispatch` write path used in production -- the only way to
        reach this state at all, since with the trigger present the second
        write would have been refused (that refusal is `LedgerTest`'s own
        test, above)."""
        ledger = Ledger(root, clock=lambda: 1_000)
        ledger.record_dispatch(
            lane="free-3", pane_id="%3", nonce="nonce-a", harness="claude", repo="/repo/free-3",
            server_id="server-a", session_id="$3", command="claude.exe", task_id="as169-legacy-a",
            source_kind="pull", source_url="https://github.com/jonhill90/agent-supervisor/pull/961",
            source_ref="961", summary="fix pass on PR #961", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3", "pr: 961"], status_marker=None,
        )
        connection = sqlite3.connect(root / "ledger.sqlite3")
        try:
            connection.execute("DROP TRIGGER one_open_pull_per_source_ref")
            connection.commit()
        finally:
            connection.close()
        ledger.record_dispatch(
            lane="free-4", pane_id="%4", nonce="nonce-b", harness="claude", repo="/repo/free-4",
            server_id="server-a", session_id="$4", command="claude.exe", task_id="as169-legacy-b",
            source_kind="pull", source_url="https://github.com/jonhill90/agent-supervisor/pull/961",
            source_ref="961", summary="a second dispatcher also tried PR #961, before this fix existed",
            source_state="OPEN", evidence=["claimed by dispatch.sh for lane free-4", "pr: 961"],
            status_marker=None,
        )

    def test_fresh_ledger_gets_the_trigger(self):
        root = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: __import__("shutil").rmtree(root, ignore_errors=True))
        Ledger(root, clock=lambda: 1_000)
        self.assertIsNotNone(self._raw_trigger_sql(root))

    def test_preexisting_duplicate_open_pull_rows_refuse_to_migrate_rather_than_pick_a_winner(self):
        root = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: __import__("shutil").rmtree(root, ignore_errors=True))
        self._seed_duplicate_open_pull_rows(root)

        # Every subsequent open refuses, loudly, naming the PR and both
        # conflicting task ids -- not just the first one after the
        # duplicate appeared.
        for _ in range(2):
            with self.assertRaisesRegex(RuntimeError, "961") as exc:
                Ledger(root, clock=lambda: 2_000)
            self.assertIn("as169-legacy-a", str(exc.exception))
            self.assertIn("as169-legacy-b", str(exc.exception))

        # Neither row was touched -- refusing to migrate did not silently
        # drop or alter either one.
        connection = sqlite3.connect(root / "ledger.sqlite3")
        try:
            rows = connection.execute(
                "SELECT tasks.id, tasks.status FROM tasks "
                "JOIN source_tasks ON source_tasks.id = tasks.id "
                "WHERE source_tasks.source_ref='961' ORDER BY tasks.id"
            ).fetchall()
        finally:
            connection.close()
        self.assertEqual([("as169-legacy-a", "delivered"), ("as169-legacy-b", "delivered")], rows)
        self.assertIsNone(self._raw_trigger_sql(root))

    def test_resolving_the_duplicate_by_hand_lets_the_next_open_create_the_trigger(self):
        root = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: __import__("shutil").rmtree(root, ignore_errors=True))
        self._seed_duplicate_open_pull_rows(root)
        with self.assertRaises(RuntimeError):
            Ledger(root, clock=lambda: 2_000)

        # The human reconciliation the migration's own error message points
        # at: cancel the row that lost the race by hand -- the same
        # operation `cli.py cancel-open-task --lane <lane>` performs --
        # without the migration's help (it cannot know which of the two is
        # actually safe to abandon; see the migration's own docstring).
        connection = sqlite3.connect(root / "ledger.sqlite3")
        try:
            connection.execute("UPDATE tasks SET status='cancelled' WHERE id='as169-legacy-b'")
            connection.commit()
        finally:
            connection.close()

        recovered = Ledger(root, clock=lambda: 3_000)
        self.assertIsNotNone(self._raw_trigger_sql(root))
        self.assertEqual("free-3", recovered.get_open_task_for_pr("961")["lane"])

    def test_migration_failure_leaves_no_trigger_and_recovers_on_the_next_open(self):
        for failpoint in ("before_pull_trigger", "after_pull_trigger"):
            with self.subTest(failpoint=failpoint):
                root = Path(tempfile.mkdtemp())
                self.addCleanup(lambda r=root: __import__("shutil").rmtree(r, ignore_errors=True))

                with self.assertRaisesRegex(RuntimeError, failpoint):
                    Ledger(root, clock=lambda: 2_000, _migration_failpoint=failpoint)
                self.assertIsNone(self._raw_trigger_sql(root))

                recovered = Ledger(root, clock=lambda: 3_000)
                self.assertIsNotNone(self._raw_trigger_sql(root))
                recovered.record_dispatch(
                    lane="free-3", pane_id="%3", nonce="nonce-a", harness="claude", repo="/repo/free-3",
                    server_id="server-a", session_id="$3", command="claude.exe", task_id="as169-after-fail",
                    source_kind="pull", source_url="https://github.com/jonhill90/agent-supervisor/pull/962",
                    source_ref="962", summary="fix pass on PR #962", source_state="OPEN",
                    evidence=["claimed by dispatch.sh for lane free-3", "pr: 962"], status_marker=None,
                )
                self.assertEqual("free-3", recovered.get_open_task_for_pr("962")["lane"])

    def test_reopening_the_same_task_id_is_not_a_collision_with_itself(self):
        """The trigger's own `id != NEW.id` exclusion: `_reconstruct_task_tx`
        is an `ON CONFLICT(id) DO UPDATE` upsert, and a legitimate retry
        that re-registers the SAME task id must not be refused as a
        collision with its own prior row."""
        root = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: __import__("shutil").rmtree(root, ignore_errors=True))
        ledger = Ledger(root, clock=lambda: 1_000)
        ledger.record_dispatch(
            lane="free-3", pane_id="%3", nonce="nonce-a", harness="claude", repo="/repo/free-3",
            server_id="server-a", session_id="$3", command="claude.exe", task_id="as169-retry",
            source_kind="pull", source_url="https://github.com/jonhill90/agent-supervisor/pull/963",
            source_ref="963", summary="fix pass on PR #963", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3", "pr: 963"], status_marker=None,
        )
        second = ledger.reconstruct_task(
            task_id="as169-retry", source_kind="pull",
            source_url="https://github.com/jonhill90/agent-supervisor/pull/963", source_ref="963",
            summary="fix pass on PR #963, re-registered", source_state="OPEN", status="created",
            evidence=["retried"], status_marker=None,
        )
        self.assertEqual("fix pass on PR #963, re-registered", second["summary"])


class DeliveryTimestampTest(unittest.TestCase):
    """Correctness defect: mark_delivery_pending must not write delivered_at."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.clock = MutableClock()
        self.ledger = Ledger(Path(self.tempdir.name), clock=self.clock)
        self.ledger.register_lane(
            lane="app-review", pane_id="%22", nonce="nonce-22-a", harness="codex", repo="/repo/app",
            server_id="server-a", session_id="$4", command="codex",
        )
        self.ledger.reconstruct_task(
            task_id="review-870", source_kind="issue",
            source_url="https://github.com/jonhill90/Hill90/issues/870", source_ref="a" * 40,
            summary="Review PR 870 without editing", source_state="OPEN", status="created",
            evidence=[], status_marker=None,
        )
        self.ledger.assign(
            task_id="review-870", lane="app-review", pane_nonce="nonce-22-a",
            summary="Review PR 870 without editing",
        )

    def test_mark_delivery_pending_sets_delivery_attempted_at_not_delivered_at(self):
        self.clock.value = 1_500
        pending = self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.assertEqual(1_500, pending["delivery_attempted_at"])
        self.assertIsNone(pending["delivered_at"])

    def test_confirmed_delivery_sets_delivered_at_and_keeps_attempt_timestamp(self):
        self.clock.value = 1_500
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.clock.value = 1_600
        delivered = self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")
        self.assertEqual(1_500, delivered["delivery_attempted_at"])
        self.assertEqual(1_600, delivered["delivered_at"])

    def test_failed_reconciliation_leaves_delivered_at_null_but_retains_attempt_timestamp(self):
        self.clock.value = 1_500
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.clock.value = 1_700
        retired = self.ledger.reconcile_delivery("review-870", pane_nonce="nonce-22-a", outcome="failed")
        self.assertEqual("failed", retired["status"])
        self.assertIsNone(retired["delivered_at"])
        self.assertEqual(1_500, retired["delivery_attempted_at"])
        self.assertEqual(1_700, retired["completed_at"])


class ReconcileAfterReRegistrationTest(unittest.TestCase):
    """Blocker 2: reconcile must survive a re-registered lane."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.clock = MutableClock()
        self.ledger = Ledger(Path(self.tempdir.name), clock=self.clock)
        self.ledger.register_lane(
            lane="app-review", pane_id="%22", nonce="nonce-dead", harness="codex", repo="/repo/app",
            server_id="server-a", session_id="$4", command="codex",
        )
        self.ledger.reconstruct_task(
            task_id="review-870", source_kind="issue",
            source_url="https://github.com/jonhill90/Hill90/issues/870", source_ref="a" * 40,
            summary="Review PR 870 without editing", source_state="OPEN", status="created",
            evidence=[], status_marker=None,
        )
        self.ledger.assign(
            task_id="review-870", lane="app-review", pane_nonce="nonce-dead",
            summary="Review PR 870 without editing",
        )
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-dead")

    def test_reproduction_reconciling_with_the_lanes_current_nonce_wedges(self):
        """Reproduces the reported bug: passing the LANE's current (new)
        nonce - the CLI's old behavior - fails against the task's own
        pane_nonce recorded at send time."""
        self.ledger.register_lane(
            lane="app-review", pane_id="%23", nonce="nonce-reborn", harness="codex", repo="/repo/app",
            server_id="server-a", session_id="$4", command="codex",
        )
        current_lane_nonce = self.ledger.get_lane("app-review")["nonce"]
        self.assertEqual("nonce-reborn", current_lane_nonce)
        with self.assertRaisesRegex(ValueError, "pane incarnation does not match task"):
            self.ledger.reconcile_delivery("review-870", pane_nonce=current_lane_nonce, outcome="delivered")

    def test_reconcile_with_task_pane_nonce_succeeds_after_lane_re_registration(self):
        self.ledger.register_lane(
            lane="app-review", pane_id="%23", nonce="nonce-reborn", harness="codex", repo="/repo/app",
            server_id="server-a", session_id="$4", command="codex",
        )
        task_pane_nonce = self.ledger.get_task("review-870")["pane_nonce"]
        self.assertEqual("nonce-dead", task_pane_nonce)
        reconciled = self.ledger.reconcile_delivery("review-870", pane_nonce=task_pane_nonce, outcome="delivered")
        self.assertEqual("delivered", reconciled["status"])

    def test_reconcile_still_rejects_a_wrong_task_nonce(self):
        with self.assertRaisesRegex(ValueError, "pane incarnation does not match task"):
            self.ledger.reconcile_delivery("review-870", pane_nonce="some-guessed-nonce", outcome="delivered")


class StrandedLaneClaims(unittest.TestCase):
    """agent-dotfiles#209: what happens to a `claim_lane` placeholder whose
    owning dispatcher died before it could release.

    `dispatch.sh`'s trap covers every exit a shell can observe. SIGKILL, an
    OOM kill and a host crash are not among them, and the placeholder they
    leave makes `lane_available` read False forever -- agent-dotfiles#102's
    failure shape (capacity silently falling to zero while lanes sit idle)
    reached through the mechanism built to prevent it.

    The reap is the only cover for that, so what it must NOT do matters at
    least as much as what it must: reaping a live dispatcher's claim would
    reopen the race #184 closed, which is why every ambiguous case below is
    asserted to leave the claim alone.
    """

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.ledger = Ledger(Path(self._tmp.name))
        self.ledger.register_lane(
            lane="free-9", pane_id="%9", nonce="nonce-9", harness="claude",
            repo="/repo/app", server_id="socket:1", session_id="$0", command="claude.exe",
        )

    def claim(self, token, *, pid, host="this-host"):
        return self.ledger.claim_lane("free-9", token=token, owner=claim_owner_token(pid, host=host))

    def test_a_claim_left_behind_holds_the_lane_shut(self):
        """The defect itself, stated as a test: no reap, no recovery."""
        self.assertTrue(self.ledger.lane_available("free-9"))
        self.assertTrue(self.claim("ad1-crash", pid=4242)["claimed"])
        self.assertFalse(self.ledger.lane_available("free-9"))
        # Nothing else in the ledger frees it -- this is what nine hand
        # reconciliations in two days were paying for.
        self.assertEqual([], self.ledger.reap_stale_lane_claims(host="this-host", is_alive=lambda pid: True))
        self.assertFalse(self.ledger.lane_available("free-9"))

    def test_reap_clears_a_claim_whose_owner_is_gone(self):
        self.claim("ad1-crash", pid=4242)
        reaped = self.ledger.reap_stale_lane_claims(host="this-host", is_alive=lambda pid: False)
        self.assertEqual(["ledger-claim:free-9:ad1-crash"], [row["id"] for row in reaped])
        self.assertEqual(["this-host:4242"], [row["owner"] for row in reaped])
        self.assertTrue(self.ledger.lane_available("free-9"))

    def test_reap_never_touches_a_claim_whose_owner_is_alive(self):
        """The one-way ratchet of #124/#126: this may only ever withhold."""
        self.claim("ad1-running", pid=4242)
        self.assertEqual([], self.ledger.reap_stale_lane_claims(host="this-host", is_alive=lambda pid: True))
        self.assertFalse(self.ledger.lane_available("free-9"))

    def test_reap_never_touches_a_claim_owned_by_another_host(self):
        """A pid is only meaningful on the machine that minted it. Judging a
        remote dispatcher's claim by whether some LOCAL process holds that
        number would reap live claims at random."""
        self.claim("ad1-elsewhere", pid=4242, host="other-host")
        self.assertEqual([], self.ledger.reap_stale_lane_claims(host="this-host", is_alive=lambda pid: False))
        self.assertFalse(self.ledger.lane_available("free-9"))

    def test_reap_never_touches_a_claim_with_no_recorded_owner(self):
        """Claims written before #209, or by any caller that omits `owner`,
        behave exactly as they did before it: automatic recovery does not
        apply to them, and they are not guessed at."""
        self.ledger.claim_lane("free-9", token="ad1-ownerless")
        self.assertEqual([], self.ledger.reap_stale_lane_claims(host="this-host", is_alive=lambda pid: False))
        self.assertFalse(self.ledger.lane_available("free-9"))

    def test_reap_never_touches_a_held_lane_from_a_failed_record(self):
        """`mark_lane_held` (#188) writes a DELIBERATE hold awaiting a human
        after a `record_dispatch` failure -- against a lane whose pane is
        live and working. Reaping one would hand a working lane to the next
        dispatcher, which is the exact incident #188 exists to prevent."""
        self.ledger.mark_lane_held("free-9", note="ledger record failed")
        self.assertFalse(self.ledger.lane_available("free-9"))
        self.assertEqual([], self.ledger.reap_stale_lane_claims(host="this-host", is_alive=lambda pid: False))
        self.assertFalse(self.ledger.lane_available("free-9"))

    def test_reap_never_touches_a_real_dispatch_task(self):
        self.ledger.reconstruct_task(
            task_id="ad700-real", source_kind="issue", source_url="https://example/700",
            source_ref="700", summary="real work", source_state="open",
            evidence=["claimed by dispatch.sh for lane free-9"], status="created", status_marker=None,
        )
        self.ledger.assign(task_id="ad700-real", lane="free-9", pane_nonce="nonce-9", summary="real work")
        self.assertEqual([], self.ledger.reap_stale_lane_claims(host="this-host", is_alive=lambda pid: False))
        self.assertFalse(self.ledger.lane_available("free-9"))

    def test_pid_liveness_resolves_every_ambiguity_as_alive(self):
        """`pid_is_alive` is deliberately asymmetric: a false 'dead' reopens
        #184's race, a false 'alive' only defers a cleanup."""
        self.assertTrue(pid_is_alive(os.getpid()))
        self.assertTrue(pid_is_alive(0))
        self.assertTrue(pid_is_alive(-1))
        self.assertTrue(pid_is_alive("not-a-pid"))
        self.assertTrue(pid_is_alive(None))

    def test_pid_liveness_reports_a_dead_child(self):
        """A real, definitely-finished process -- not a number picked for
        being unlikely to exist."""
        proc = subprocess.Popen([sys.executable, "-c", "pass"])
        proc.wait()
        # The zombie is reaped by wait(), so the pid is genuinely gone.
        self.assertFalse(pid_is_alive(proc.pid))

    def test_owner_token_carries_this_host_by_default(self):
        self.assertEqual(f"{socket.gethostname()}:77", claim_owner_token(77))


class CommittedLaneClaims(unittest.TestCase):
    """agent-dotfiles#209 round 2: a claim with a LIVE brief behind it.

    Round 1 gave the dispatcher two cleanup paths -- a trap for the exits a
    shell can observe, a reap for the ones it cannot -- and drew the line
    between "still unwindable" and "a worker may be running" with an
    in-process bash flag set well AFTER the brief was submitted into the pane.
    Inside that window both paths freed a lane that was actively working:
    #102/#126's failure produced by the cleanup rather than prevented by it.

    `commit_lane_claim` moves that line onto the send and writes it to the
    LEDGER, which is what makes it survive the SIGKILL case -- at that instant
    the placeholder is the only record the lane is occupied at all, because
    `record_dispatch` has not run. So the assertions that matter here are the
    refusals: after a commit, neither cleanup path may free the lane, however
    provably gone its owner is.
    """

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.ledger = Ledger(Path(self._tmp.name))
        self.ledger.register_lane(
            lane="free-9", pane_id="%9", nonce="nonce-9", harness="claude",
            repo="/repo/app", server_id="socket:1", session_id="$0", command="claude.exe",
        )
        self.ledger.claim_lane("free-9", token="ad1-live", owner=claim_owner_token(4242, host="this-host"))

    def test_commit_marks_the_claim_live_and_keeps_the_lane_occupied(self):
        result = self.ledger.commit_lane_claim("free-9", token="ad1-live")
        self.assertTrue(result["committed"])
        self.assertIsNone(result["reason"])
        self.assertEqual("delivered", self.ledger.get_task("ledger-claim:free-9:ad1-live")["status"])
        self.assertFalse(self.ledger.lane_available("free-9"))

    def test_release_will_not_free_a_committed_claim(self):
        """The trap fires on every exit including a signal, and calls release
        unconditionally. This scope is what makes that safe."""
        self.ledger.commit_lane_claim("free-9", token="ad1-live")
        # ...and it says so, rather than reporting a release it did not do.
        self.assertFalse(self.ledger.release_lane_claim("free-9", token="ad1-live"))
        self.assertIsNotNone(self.ledger.get_task("ledger-claim:free-9:ad1-live"))
        self.assertFalse(self.ledger.lane_available("free-9"))

    def test_release_reports_the_row_it_really_removed(self):
        """The control for the assertion above: on a claim still reserved,
        the same call returns True and the lane comes back."""
        self.assertTrue(self.ledger.release_lane_claim("free-9", token="ad1-live"))
        self.assertTrue(self.ledger.lane_available("free-9"))
        # Idempotent, and honest the second time.
        self.assertFalse(self.ledger.release_lane_claim("free-9", token="ad1-live"))

    def test_reap_will_not_free_a_committed_claim_even_when_its_owner_is_gone(self):
        """The dangerous half. `is_alive` is forced False -- the owner is as
        provably dead as the reap can ever establish -- and the claim must
        still survive, because a dead owner does not mean an idle pane."""
        self.ledger.commit_lane_claim("free-9", token="ad1-live")
        self.assertEqual([], self.ledger.reap_stale_lane_claims(host="this-host", is_alive=lambda pid: False))
        self.assertFalse(self.ledger.lane_available("free-9"))

    def test_an_uncommitted_claim_is_still_reaped_and_released(self):
        """The control, and the previous round's finding held: before the
        commit nothing is working the lane, so both paths must still clear it.
        A guard that withheld here would trade #209's bug for #102's."""
        self.assertEqual(
            ["ledger-claim:free-9:ad1-live"],
            [row["id"] for row in self.ledger.reap_stale_lane_claims(host="this-host", is_alive=lambda pid: False)],
        )
        self.assertTrue(self.ledger.lane_available("free-9"))

    def test_commit_is_idempotent(self):
        self.assertTrue(self.ledger.commit_lane_claim("free-9", token="ad1-live")["committed"])
        again = self.ledger.commit_lane_claim("free-9", token="ad1-live")
        self.assertTrue(again["committed"])
        self.assertEqual("delivered", self.ledger.get_task("ledger-claim:free-9:ad1-live")["status"])

    def test_commit_refuses_a_claim_that_does_not_exist(self):
        """Refuses rather than inventing the row. `dispatch.sh` treats this as
        fatal and does not send, which is free: nothing is in the pane yet."""
        result = self.ledger.commit_lane_claim("free-9", token="somebody-elses-token")
        self.assertFalse(result["committed"])
        self.assertEqual("missing", result["reason"])
        self.assertIsNone(self.ledger.get_task("ledger-claim:free-9:somebody-elses-token"))

    def test_commit_refuses_a_claim_already_released(self):
        self.ledger.release_lane_claim("free-9", token="ad1-live")
        self.assertFalse(self.ledger.commit_lane_claim("free-9", token="ad1-live")["committed"])
        self.assertTrue(self.ledger.lane_available("free-9"))

    def test_a_clean_dispatch_still_supersedes_a_committed_claim(self):
        """The success path, and the reason `delivered` is the status used.

        `_register_lane_tx` excludes `delivery_pending` from the outstanding
        task it cancels through, so parking the claim there would make it
        survive `record_dispatch` and then collide with its task INSERT under
        `one_open_task_per_lane` -- breaking every clean dispatch. `delivered`
        is cancelled normally, so the ordinary path is unchanged.
        """
        self.ledger.commit_lane_claim("free-9", token="ad1-live")
        self.ledger.register_lane(
            lane="free-9", pane_id="%9", nonce="nonce-9-new", harness="claude",
            repo="/repo/app", server_id="socket:1", session_id="$0", command="claude.exe",
        )
        self.assertEqual("cancelled", self.ledger.get_task("ledger-claim:free-9:ad1-live")["status"])
        self.assertTrue(self.ledger.lane_available("free-9"))


class ReusedLaneClaimTokens(unittest.TestCase):
    """agent-supervisor#174: a healthy, idle lane refused forever.

    `dispatch.sh` derives `CLAIM_TOKEN` from the window name (dispatch.sh:666),
    which is deterministic for a given issue/lane pairing -- a retried review
    recomputes the exact same token an earlier, now-finished dispatch already
    used. `claim_lane`'s task id is `ledger-claim:{lane}:{token}`
    (`CLAIM_TASK_PREFIX`), so that id is a PRIMARY KEY this table has already
    seen: the earlier dispatch's own `record_dispatch` cancelled that exact
    row via `_register_lane_tx`'s changed-identity path (a fresh nonce is
    minted every call), leaving it behind forever as `cancelled` rather than
    deleting it.

    The second `claim_lane` call's INSERT collides with that dead row and
    raises `IntegrityError`, which `claim_lane` swallows -- but the SELECT
    that follows finds no active row anywhere for the lane (there genuinely
    is none; the lane is free), so it reports `occupied` with `holder: None`.
    Measured on the live estate 2026-08-15: `release-lane-claim` cannot free
    a row that is not there to release, `reap-lane-claims` only ever touches
    `CLAIM_STATUS_RESERVED` rows and finds none, and the lane never comes
    back -- exactly "claim refused (occupied; no holder reported)" with every
    claim row already `cancelled` or `complete`.
    """

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.ledger = Ledger(Path(self._tmp.name))
        self.ledger.register_lane(
            lane="free-4", pane_id="%4", nonce="nonce-0", harness="claude",
            repo="/repo/app", server_id="socket:1", session_id="$0", command="claude.exe",
        )
        # A first, complete dispatch under the token a retry will reuse --
        # exactly dispatch.sh's own steps 4, 4.5 and 6, in order.
        first = self.ledger.claim_lane("free-4", token="as163-rev168", owner=claim_owner_token(4242))
        self.assertTrue(first["claimed"])
        self.ledger.commit_lane_claim("free-4", token="as163-rev168")
        self.ledger.record_dispatch(
            lane="free-4", pane_id="%4", nonce="nonce-1", harness="claude",
            repo="/repo/app", server_id="socket:1", session_id="$0", command="claude.exe",
            task_id="ad163-rev168", source_kind="issue", source_url="https://example/163",
            source_ref="163", summary="review #168", source_state="open",
            evidence=["claimed by dispatch.sh for lane free-4"],
        )
        self.ledger.complete("ad163-rev168", b"ok", pane_nonce="nonce-1")
        # The claim placeholder is closed, the real task is closed, and
        # nothing else is outstanding -- the lane reads free by every
        # measure this ledger has.
        self.assertEqual("cancelled", self.ledger.get_task("ledger-claim:free-4:as163-rev168")["status"])
        self.assertEqual("complete", self.ledger.get_task("ad163-rev168")["status"])
        self.assertTrue(self.ledger.lane_available("free-4"))

    def test_a_free_lane_is_still_claimable_under_a_reused_token(self):
        """The defect itself: a lane every read calls free refuses the write."""
        second = self.ledger.claim_lane("free-4", token="as163-rev168", owner=claim_owner_token(4343))
        self.assertTrue(second["claimed"], second)
        self.assertIsNone(second["reason"])
        self.assertFalse(self.ledger.lane_available("free-4"))

    def test_release_reports_truthfully_once_the_lane_is_actually_recoverable(self):
        """Acceptance criterion 4: `release-lane-claim` must not call a
        refusal "no reserved claim matched" once the row it names is the
        live reservation actually blocking dispatch."""
        self.ledger.claim_lane("free-4", token="as163-rev168", owner=claim_owner_token(4343))
        self.assertTrue(self.ledger.release_lane_claim("free-4", token="as163-rev168"))
        self.assertTrue(self.ledger.lane_available("free-4"))


class ReviveWhileLaneGenuinelyOccupied(unittest.TestCase):
    """agent-supervisor#182: PR #175's revival guard (`ReusedLaneClaimTokens`
    above) tells apart "my own dead token" (safe to revive) from "a
    stranger's active claim" (must refuse) -- but only by checking the
    STATUS of the row sitting on this claim's own id. It never asks whether
    the LANE ITSELF is free once that revival succeeds.

    A stale token retried after the placeholder it names has already been
    superseded (by `commit_lane_claim` + `record_dispatch`, exactly
    `dispatch.sh`'s own sequence) by a live, non-claim dispatch on the same
    lane used to hit the revival UPDATE unconditionally: reviving the dead
    placeholder collides with `one_open_task_per_lane` against that still-
    active dispatch -- a SECOND `sqlite3.IntegrityError`, raised from inside
    the `except IntegrityError:` block that was supposed to be handling the
    first one. Nothing caught it: `_transaction()` rolled back and
    re-raised, and `cli.py`'s `claim-lane` has no try/except around this
    call, so `dispatch.sh` got a bare traceback where it expects JSON.

    Before #175 this returned the ordinary refusal:
    `{'claimed': False, 'reason': 'occupied', 'holder': 'real-task-1'}`.
    It must again -- and the genuine holder must be left untouched.
    """

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.ledger = Ledger(Path(self._tmp.name))
        self.ledger.register_lane(
            lane="free-4", pane_id="%4", nonce="nonce-0", harness="claude",
            repo="/repo/app", server_id="socket:1", session_id="$0", command="claude.exe",
        )
        first = self.ledger.claim_lane("free-4", token="tok-A", owner=claim_owner_token(1))
        self.assertTrue(first["claimed"])
        self.ledger.commit_lane_claim("free-4", token="tok-A")
        # The real dispatch supersedes the placeholder -- `_register_lane_tx`
        # cancels `ledger-claim:free-4:tok-A` in place, same as #174's setup.
        self.ledger.record_dispatch(
            lane="free-4", pane_id="%4", nonce="nonce-1", harness="claude",
            repo="/repo/app", server_id="socket:1", session_id="$0", command="claude.exe",
            task_id="real-task-1", source_kind="issue", source_url="https://example/182",
            source_ref="182", summary="genuine work", source_state="open",
            evidence=["claimed by dispatch.sh for lane free-4"],
        )
        self.assertEqual("cancelled", self.ledger.get_task("ledger-claim:free-4:tok-A")["status"])
        # real-task-1 is left ACTIVE -- genuinely working, not completed.
        self.assertNotIn(self.ledger.get_task("real-task-1")["status"], ("complete", "failed", "cancelled"))

    def test_a_stale_retry_is_refused_not_crashed(self):
        """The defect itself: retrying the dead token against a lane a
        different, still-active task now holds must return JSON, not raise."""
        second = self.ledger.claim_lane("free-4", token="tok-A", owner=claim_owner_token(2))
        self.assertFalse(second["claimed"])
        self.assertEqual("occupied", second["reason"])
        self.assertEqual("real-task-1", second["holder"])

    def test_the_real_holder_is_undisturbed(self):
        """Ratchet check: the refusal must not revive the dead placeholder
        nor touch the genuine occupant it collided with."""
        before_holder = self.ledger.get_task("real-task-1")
        before_placeholder = self.ledger.get_task("ledger-claim:free-4:tok-A")
        self.ledger.claim_lane("free-4", token="tok-A", owner=claim_owner_token(2))
        self.assertEqual(before_holder, self.ledger.get_task("real-task-1"))
        self.assertEqual(before_placeholder, self.ledger.get_task("ledger-claim:free-4:tok-A"))


class LaneRelationTest(unittest.TestCase):
    """agent-supervisor#108: a lane id embeds the session's NAME, and renaming
    the session renames every lane at once without moving a single window. The
    comparison this guard rests on has to answer for the window, and has to
    have somewhere to put "cannot tell" other than "different"."""

    def test_identical_ids_are_the_same_lane(self):
        self.assertEqual("same", lane_relation("agent-supervisor:3", "agent-supervisor:3"))

    def test_a_renamed_session_is_still_the_same_window(self):
        # The measured pair from the incident: 526 rows say the first, the
        # lanes dispatched since say the second, and they are one window.
        self.assertEqual("same", lane_relation("agent-dotfiles:3", "agent-supervisor:3"))

    def test_different_indices_are_different_windows_whatever_the_session(self):
        self.assertEqual("different", lane_relation("agent-dotfiles:3", "agent-supervisor:4"))
        self.assertEqual("different", lane_relation("t:3", "t:4"))

    def test_a_leading_zero_does_not_invent_a_second_window(self):
        self.assertEqual("same", lane_relation("t:03", "t:3"))

    def test_an_unparseable_id_is_unknown_not_different(self):
        # The `Review-Lane:` stamp on PR #95 was literally `lane/89-rev95` --
        # a branch name, not a lane id. Nothing about it establishes that the
        # reviewer is a different window, and `unknown` is the only answer
        # that does not launder that into independence.
        self.assertEqual("unknown", lane_relation("agent-dotfiles:3", "lane/89-rev95"))
        self.assertEqual("unknown", lane_relation("free-3", "t:3"))
        self.assertEqual("unknown", lane_relation("t:3", ""))
        self.assertEqual("unknown", lane_relation(None, "t:3"))
        self.assertEqual("unknown", lane_relation("t:3", "t:x"))


class LaneRelationFromRowsTest(unittest.TestCase):
    """agent-supervisor#292: `lane_relation`'s string-shape check can never
    place a claude-print/pi-rpc lane (its id IS its task id -- no window to
    index) positive of anything. `lane_relation_from_rows` is the widening:
    identity from the ledger's own `lanes.pane_id` registry instead, which
    every transport writes at registration regardless of whether it has a
    tmux window at all."""

    def _row(self, pane_id, transport="send-keys"):
        return {"pane_id": pane_id, "transport": transport}

    def test_same_pane_id_is_the_same_lane(self):
        # The tmux pane id itself, unlike the lane id string, does not change
        # on a session rename -- so two rows recorded under different lane
        # STRINGS for the very same pane still resolve `same`.
        self.assertEqual(
            "same",
            lane_relation_from_rows(self._row("%12"), self._row("%12")),
        )

    def test_different_pane_id_is_positively_different(self):
        # The measured case: a tmux candidate against the claude-print author
        # of PR #288. Neither id parses as `<session>:<index>` on the
        # claude-print side, but both rows are known and their pane_ids
        # differ -- a claude-print lane's pane_id is `claude-print:<lane>`,
        # unique to the one process it names.
        self.assertEqual(
            "different",
            lane_relation_from_rows(
                self._row("%12", "send-keys"),
                self._row("claude-print:as284-finish-migration-b", "claude-print"),
            ),
        )

    def test_both_claude_print_lanes_with_different_pane_ids_are_different(self):
        # The other direction #292 requires tested: a claude-print lane
        # reviewing a claude-print-authored PR.
        self.assertEqual(
            "different",
            lane_relation_from_rows(
                self._row("claude-print:skills4-review", "claude-print"),
                self._row("claude-print:as284-finish-migration-b", "claude-print"),
            ),
        )

    def test_a_claude_print_lane_reviewing_itself_is_the_same_lane(self):
        self.assertEqual(
            "same",
            lane_relation_from_rows(
                self._row("claude-print:as284-finish-migration-b", "claude-print"),
                self._row("claude-print:as284-finish-migration-b", "claude-print"),
            ),
        )

    def test_a_missing_row_on_either_side_is_unknown_not_different(self):
        # Fail-closed: a lane the ledger has never heard of establishes
        # nothing, and admitting it as "different" is exactly the loosening
        # #292 refuses to do.
        self.assertEqual("unknown", lane_relation_from_rows(None, self._row("%12")))
        self.assertEqual("unknown", lane_relation_from_rows(self._row("%12"), None))
        self.assertEqual("unknown", lane_relation_from_rows(None, None))

    def test_an_empty_pane_id_is_unknown(self):
        self.assertEqual(
            "unknown",
            lane_relation_from_rows(self._row(""), self._row("%12")),
        )


class LanePopulationTest(unittest.TestCase):
    """agent-supervisor#292 item 3: naming which population a lane is in, so
    a refusal message can say WHY two lanes could not be told apart instead
    of implying a rename problem that was never the cause."""

    def test_send_keys_and_acp_rows_are_tmux(self):
        self.assertEqual("tmux", lane_population("t:3", {"transport": "send-keys"}))
        self.assertEqual("tmux", lane_population("t:3", {"transport": "acp"}))

    def test_claude_print_and_pi_rpc_rows_name_themselves(self):
        self.assertEqual(
            "claude-print",
            lane_population("as284-finish-migration-b", {"transport": "claude-print"}),
        )
        self.assertEqual("pi-rpc", lane_population("as99-task", {"transport": "pi-rpc"}))

    def test_no_row_falls_back_to_id_shape(self):
        self.assertEqual("tmux", lane_population("t:3", None))
        self.assertEqual("off-pane", lane_population("as284-finish-migration-b", None))
        self.assertEqual("off-pane", lane_population("lane/89-rev95", None))


# agent-supervisor#153: this table is a DECISION record (WE adopted this
# session), never a measurement -- see the schema comment in core.py for the
# authorship-test reasoning. These tests cover the ledger half only; the
# tri-state read that also checks live tmux existence (`session_state`) is
# covered in test_cli.py, since it lives there.
class SessionSupervisionTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.clock = MutableClock()
        self.ledger = Ledger(Path(self.tempdir.name), clock=self.clock)

    def test_a_session_with_no_row_is_not_marked_supervised(self):
        self.assertFalse(self.ledger.session_marked_supervised("Hill90"))

    def test_adopt_session_marks_it_supervised(self):
        self.ledger.adopt_session("agent-supervisor")
        self.assertTrue(self.ledger.session_marked_supervised("agent-supervisor"))

    def test_adopt_session_is_idempotent_and_refreshes_the_timestamp(self):
        self.clock.value = 1000
        first = self.ledger.adopt_session("agent-supervisor")
        self.clock.value = 2000
        second = self.ledger.adopt_session("agent-supervisor")
        self.assertEqual(1000, first["supervised_at"])
        self.assertEqual(2000, second["supervised_at"])
        self.assertEqual(1, len(self.ledger.list_sessions()))

    def test_adopting_one_session_does_not_mark_another(self):
        self.ledger.adopt_session("agent-supervisor")
        self.assertFalse(self.ledger.session_marked_supervised("Hill90"))

    def test_list_sessions_reflects_every_adopted_session(self):
        self.ledger.adopt_session("agent-supervisor")
        self.ledger.adopt_session("agent-tui")
        names = sorted(row["session"] for row in self.ledger.list_sessions())
        self.assertEqual(["agent-supervisor", "agent-tui"], names)

    # #153's own example of stale ledger knowledge: a row can exist for a
    # session that is long gone. The ledger method itself has no opinion on
    # that -- it only answers "did we ever adopt this name" -- which is
    # exactly why `session_state` in cli.py must ALSO check tmux before
    # calling anything supervised (covered in test_cli.py).
    def test_a_row_survives_regardless_of_whether_the_session_still_exists(self):
        self.ledger.adopt_session("agent-dotfiles")
        self.assertTrue(self.ledger.session_marked_supervised("agent-dotfiles"))


class RecordSessionEventTest(unittest.TestCase):
    """agent-tui#14: `session_remove`'s audit trail -- logging every removal
    to the ledger with what was running at the time, via the same `events`
    table `complete`/`observe_attention`/`record_snapshot` already write."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.clock = MutableClock()
        self.ledger = Ledger(Path(self.tempdir.name), clock=self.clock)

    def test_writes_a_durable_event_carrying_the_detail(self):
        detail = {"session": "work", "safe_to_remove": True, "refusals": []}
        row = self.ledger.record_session_event("work", event="removed", detail=detail)
        self.assertEqual("session", row["type"])
        self.assertIsNone(row["task_id"])
        self.assertTrue(row["key"].startswith("session:removed:work:"))

    def test_the_event_is_readable_back_through_list_events(self):
        self.ledger.record_session_event("work", event="removed", detail={"a": 1})
        events = self.ledger.list_events()
        self.assertEqual(1, len(events))
        self.assertEqual("session", events[0]["type"])

    def test_the_full_detail_payload_survives_on_disk(self):
        detail = {"session": "work", "worktrees": [{"path": "/wt", "clean": True}]}
        row = self.ledger.record_session_event("work", event="removed", detail=detail)
        payload = json.loads(Path(row["payload_path"]).read_text())
        self.assertEqual(detail, payload)

    def test_two_removals_of_the_same_session_get_distinct_rows(self):
        self.clock.value = 1000
        first = self.ledger.record_session_event("work", event="removed", detail={"n": 1})
        self.clock.value = 2000
        second = self.ledger.record_session_event("work", event="removed", detail={"n": 2})
        self.assertNotEqual(first["key"], second["key"])
        self.assertEqual(2, len(self.ledger.list_events()))

    def test_session_is_required(self):
        with self.assertRaises(ValueError):
            self.ledger.record_session_event("", event="removed", detail={})

    def test_event_is_required(self):
        with self.assertRaises(ValueError):
            self.ledger.record_session_event("work", event="", detail={})


if __name__ == "__main__":
    unittest.main()
