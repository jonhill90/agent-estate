"""Transactional task/event ledger for the Hill90 supervisor.

The core deliberately knows nothing about Codex, Claude terminal chrome, tmux
keystrokes, or GitHub. Harness adapters register pane incarnations and move
tasks through this shared lifecycle.
"""

from __future__ import annotations

import contextlib
import difflib
import fcntl
import hashlib
import json
import os
import re
import sqlite3
import tempfile
import time
from pathlib import Path


TERMINAL_STATUSES = ("complete", "failed", "cancelled")
TASK_ID_RE = re.compile(r"^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$")
COMPONENT_RE = re.compile(r"^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$")
MAX_RESULT_BYTES = 64 * 1024


class Ledger:
    def __init__(self, root: Path | str, *, clock=None, _migration_failpoint=None):
        self.root = Path(root)
        self.clock = clock or (lambda: int(time.time()))
        self.results_dir = self.root / "results"
        self.snapshots_dir = self.root / "snapshots"
        self.event_payloads_dir = self.root / "event-payloads"
        self.db_path = self.root / "ledger.sqlite3"
        self.lock_path = self.root / "ledger.lock"
        self.root.mkdir(parents=True, exist_ok=True, mode=0o700)
        os.chmod(self.root, 0o700)
        self.results_dir.mkdir(exist_ok=True, mode=0o700)
        os.chmod(self.results_dir, 0o700)
        self.snapshots_dir.mkdir(exist_ok=True, mode=0o700)
        self.event_payloads_dir.mkdir(exist_ok=True, mode=0o700)
        os.chmod(self.snapshots_dir, 0o700)
        os.chmod(self.event_payloads_dir, 0o700)
        self.lock_path.touch(mode=0o600, exist_ok=True)
        os.chmod(self.lock_path, 0o600)
        self._lock_depth = 0
        self._initialize()
        self._migrate_lanes_table(failpoint=_migration_failpoint)
        self._migrate_tasks_table(failpoint=_migration_failpoint)
        self._migrate_source_tasks_table(failpoint=_migration_failpoint)

    def _connect(self, *, foreign_keys=True):
        connection = sqlite3.connect(self.db_path, timeout=30, isolation_level=None)
        connection.row_factory = sqlite3.Row
        connection.execute(f"PRAGMA foreign_keys = {'ON' if foreign_keys else 'OFF'}")
        connection.execute("PRAGMA journal_mode = WAL")
        connection.execute("PRAGMA synchronous = FULL")
        return connection

    @contextlib.contextmanager
    def _locked(self):
        if self._lock_depth:
            self._lock_depth += 1
            try:
                yield
            finally:
                self._lock_depth -= 1
            return
        with self.lock_path.open("r+") as lock_file:
            fcntl.flock(lock_file.fileno(), fcntl.LOCK_EX)
            self._lock_depth = 1
            try:
                yield
            finally:
                self._lock_depth = 0
                fcntl.flock(lock_file.fileno(), fcntl.LOCK_UN)

    def operation_lock(self):
        """Hold the ledger lock across adapter I/O and state transitions."""
        return self._locked()

    @contextlib.contextmanager
    def _transaction(self):
        connection = self._connect()
        connection.execute("BEGIN IMMEDIATE")
        try:
            yield connection
        except BaseException:
            connection.rollback()
            raise
        else:
            connection.commit()
        finally:
            connection.close()

    def _initialize(self):
        with self._locked(), self._transaction() as connection:
            connection.executescript(
                """
                CREATE TABLE IF NOT EXISTS lanes (
                    lane TEXT PRIMARY KEY,
                    pane_id TEXT NOT NULL,
                    nonce TEXT NOT NULL,
                    harness TEXT NOT NULL CHECK (harness IN ('codex', 'claude', 'copilot-acp')),
                    repo TEXT NOT NULL,
                    server_id TEXT NOT NULL,
                    session_id TEXT NOT NULL,
                    command TEXT NOT NULL,
                    updated_at INTEGER NOT NULL
                );

                CREATE TABLE IF NOT EXISTS tasks (
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

                CREATE UNIQUE INDEX IF NOT EXISTS one_open_task_per_lane
                    ON tasks(lane)
                    WHERE status NOT IN ('complete', 'failed', 'cancelled');

                CREATE TABLE IF NOT EXISTS source_tasks (
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
                );

                CREATE TABLE IF NOT EXISTS events (
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

                CREATE TABLE IF NOT EXISTS components (
                    name TEXT PRIMARY KEY,
                    healthy INTEGER NOT NULL,
                    error TEXT,
                    snapshot_sha256 TEXT,
                    updated_at INTEGER NOT NULL
                );
                """
            )
        os.chmod(self.db_path, 0o600)

    _TASKS_SCHEMA_MARKERS = ("delivery_pending", "delivery_attempted_at")
    _LANES_SCHEMA_MARKERS = ("copilot-acp",)

    def _migrate_lanes_table(self, *, failpoint=None):
        """Widen an existing `lanes` table's harness CHECK constraint in place.

        `CREATE TABLE IF NOT EXISTS` in `_initialize` never touches a table
        that already exists, so a ledger created before `copilot-acp` existed
        keeps rejecting that harness forever unless this runs. SQLite has no
        `ALTER TABLE ... ALTER COLUMN` / `DROP CONSTRAINT`, so the only way to
        widen a CHECK constraint is to rebuild the table, mirroring
        `_migrate_tasks_table`.

        Every row is preserved. The rebuild is one transaction: any failure
        mid-migration rolls back to the original table, unmodified. Foreign
        key enforcement is turned off only around this rebuild (it cannot be
        toggled mid-transaction) because `tasks.lane REFERENCES lanes(lane)`
        would otherwise block dropping the original table while rows still
        reference it; the table this rebuild produces is named `lanes` again
        by the time this returns, so that reference is satisfied exactly as
        before.
        """
        with self._locked():
            with contextlib.closing(self._connect()) as probe:
                existing = probe.execute(
                    "SELECT sql FROM sqlite_master WHERE type='table' AND name='lanes'"
                ).fetchone()
                if existing is None:
                    return
                if all(marker in existing["sql"] for marker in self._LANES_SCHEMA_MARKERS):
                    return

            connection = self._connect(foreign_keys=False)
            try:
                connection.execute("BEGIN IMMEDIATE")
                try:
                    connection.execute(
                        """
                        CREATE TABLE lanes_migrated (
                            lane TEXT PRIMARY KEY,
                            pane_id TEXT NOT NULL,
                            nonce TEXT NOT NULL,
                            harness TEXT NOT NULL CHECK (harness IN ('codex', 'claude', 'copilot-acp')),
                            repo TEXT NOT NULL,
                            server_id TEXT NOT NULL,
                            session_id TEXT NOT NULL,
                            command TEXT NOT NULL,
                            updated_at INTEGER NOT NULL
                        )
                        """
                    )
                    self._fail(failpoint, "after_create")
                    connection.execute(
                        """
                        INSERT INTO lanes_migrated (
                            lane, pane_id, nonce, harness, repo, server_id, session_id,
                            command, updated_at
                        )
                        SELECT lane, pane_id, nonce, harness, repo, server_id, session_id,
                               command, updated_at
                        FROM lanes
                        """
                    )
                    self._fail(failpoint, "after_copy")
                    connection.execute("DROP TABLE lanes")
                    self._fail(failpoint, "after_drop")
                    connection.execute("ALTER TABLE lanes_migrated RENAME TO lanes")
                    self._fail(failpoint, "after_rename")
                except BaseException:
                    connection.rollback()
                    raise
                else:
                    connection.commit()
            finally:
                connection.close()

    def _migrate_tasks_table(self, *, failpoint=None):
        """Widen an existing `tasks` table to the current schema in place.

        `CREATE TABLE IF NOT EXISTS` in `_initialize` never touches a table
        that already exists, so a ledger created before `delivery_pending`
        and `delivery_attempted_at` existed keeps its old CHECK constraint
        and column set forever unless this runs. SQLite has no
        `ALTER TABLE ... ALTER COLUMN` / `DROP CONSTRAINT`, so the only way
        to widen a CHECK constraint is to rebuild the table.

        Every row and the `one_open_task_per_lane` index are preserved. The
        rebuild is one transaction: any failure mid-migration rolls back to
        the original table, unmodified. Foreign key enforcement is turned
        off only around this rebuild (it cannot be toggled mid-transaction)
        because `events.task_id REFERENCES tasks(id)` would otherwise block
        dropping the original table while rows still reference it; the
        table this rebuild produces is named `tasks` again by the time this
        returns, so that reference is satisfied exactly as before.
        """
        with self._locked():
            with contextlib.closing(self._connect()) as probe:
                existing = probe.execute(
                    "SELECT sql FROM sqlite_master WHERE type='table' AND name='tasks'"
                ).fetchone()
                if existing is None:
                    return
                if all(marker in existing["sql"] for marker in self._TASKS_SCHEMA_MARKERS):
                    return
                columns = {info["name"] for info in probe.execute("PRAGMA table_info(tasks)").fetchall()}
            attempted_column = "delivery_attempted_at" if "delivery_attempted_at" in columns else "NULL"

            connection = self._connect(foreign_keys=False)
            try:
                connection.execute("BEGIN IMMEDIATE")
                try:
                    connection.execute(
                        """
                        CREATE TABLE tasks_migrated (
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
                    self._fail(failpoint, "after_create")
                    connection.execute(
                        f"""
                        INSERT INTO tasks_migrated (
                            id, lane, pane_nonce, summary, status, result_path, result_sha256,
                            created_at, updated_at, delivery_attempted_at, delivered_at,
                            accepted_at, completed_at
                        )
                        SELECT id, lane, pane_nonce, summary, status, result_path, result_sha256,
                               created_at, updated_at, {attempted_column}, delivered_at,
                               accepted_at, completed_at
                        FROM tasks
                        """
                    )
                    self._fail(failpoint, "after_copy")
                    connection.execute("DROP TABLE tasks")
                    self._fail(failpoint, "after_drop")
                    connection.execute("ALTER TABLE tasks_migrated RENAME TO tasks")
                    self._fail(failpoint, "after_rename")
                    connection.execute(
                        """
                        CREATE UNIQUE INDEX IF NOT EXISTS one_open_task_per_lane
                            ON tasks(lane)
                            WHERE status NOT IN ('complete', 'failed', 'cancelled')
                        """
                    )
                except BaseException:
                    connection.rollback()
                    raise
                else:
                    connection.commit()
            finally:
                connection.close()

    _SOURCE_TASKS_OLD_UNIQUE_MARKER = "source_url TEXT NOT NULL UNIQUE"

    def _migrate_source_tasks_table(self, *, failpoint=None):
        """Drop `source_tasks.source_url`'s table-level UNIQUE constraint.

        agent-dotfiles#144 finding 1: `source_url` is derived from the issue
        number alone (`cli.py`'s `record_dispatch`), and `reconstruct_task`
        upserts `ON CONFLICT(id)` -- a DIFFERENT key. A second dispatch of the
        same issue under a different task id (a retry after a lane died, a
        re-brief, a follow-up review -- this estate does this constantly; see
        the docstring on `reconstruct_task`) raised `UNIQUE constraint failed:
        source_tasks.source_url` on the second attempt, permanently, for that
        issue.

        The modelling decision, argued once here rather than worked around:
        each `source_tasks` row is one recorded DISPATCH ATTEMPT (its primary
        key is the task id, not the issue), not one row per GitHub issue. The
        one caller that DOES want "one row per issue" --
        `GithubTaskSource.reconstruct`, the marker-based path -- already gets
        that for free, because its `task_id` is `_task_id(parts)`, a pure
        function of the same URL; reconstructing the same issue twice through
        that path always hits the same `id` and upserts in place regardless of
        this constraint. The URL-uniqueness was never doing anything for that
        caller and was actively wrong for `cli.py`'s -- several attempts
        legitimately share a URL, so the column is dropped rather than
        replaced with a scoped ("one OPEN attempt per URL") constraint: this
        table's own `status` is never advanced past 'created' by the
        `record_dispatch` recording path (nothing here tracks a task's real
        lifecycle back into `source_tasks`), so an open-attempt constraint
        would immediately wedge on the very case it exists to protect --
        finding 3's issue by another name. Concurrent double-dispatch of the
        same issue is `claim.sh`'s job, upstream of this layer entirely (see
        `record_dispatch`'s own docstring); this table records what was
        dispatched, it does not arbitrate whether it should have been.

        Same SQLite limitation as `_migrate_lanes_table` / `_migrate_tasks_table`:
        there is no `ALTER TABLE ... DROP CONSTRAINT`, so the only way to widen
        a column is to rebuild the table. Every row is preserved; the rebuild
        is one transaction, rolled back whole on any failure.
        """
        with self._locked():
            with contextlib.closing(self._connect()) as probe:
                existing = probe.execute(
                    "SELECT sql FROM sqlite_master WHERE type='table' AND name='source_tasks'"
                ).fetchone()
                if existing is None:
                    return
                if self._SOURCE_TASKS_OLD_UNIQUE_MARKER not in existing["sql"]:
                    return

            connection = self._connect(foreign_keys=False)
            try:
                connection.execute("BEGIN IMMEDIATE")
                try:
                    connection.execute(
                        """
                        CREATE TABLE source_tasks_migrated (
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
                    self._fail(failpoint, "after_create")
                    connection.execute(
                        """
                        INSERT INTO source_tasks_migrated (
                            id, source_kind, source_url, source_ref, summary, source_state,
                            status, evidence_json, status_marker, updated_at
                        )
                        SELECT id, source_kind, source_url, source_ref, summary, source_state,
                               status, evidence_json, status_marker, updated_at
                        FROM source_tasks
                        """
                    )
                    self._fail(failpoint, "after_copy")
                    connection.execute("DROP TABLE source_tasks")
                    self._fail(failpoint, "after_drop")
                    connection.execute("ALTER TABLE source_tasks_migrated RENAME TO source_tasks")
                    self._fail(failpoint, "after_rename")
                except BaseException:
                    connection.rollback()
                    raise
                else:
                    connection.commit()
            finally:
                connection.close()

    @staticmethod
    def _dict(row):
        return dict(row) if row is not None else None

    @staticmethod
    def _require_task_id(task_id):
        if not TASK_ID_RE.fullmatch(task_id):
            raise ValueError("invalid task id")

    @staticmethod
    def _verify_lane_nonce(connection, lane, pane_nonce):
        row = connection.execute("SELECT nonce FROM lanes WHERE lane = ?", (lane,)).fetchone()
        if row is None:
            raise ValueError(f"unknown lane: {lane}")
        if row["nonce"] != pane_nonce:
            raise ValueError("pane incarnation does not match registered lane")

    @staticmethod
    def _cancel_task_row(connection, task_id, now):
        connection.execute(
            "UPDATE tasks SET status='cancelled', updated_at=?, completed_at=? WHERE id=?",
            (now, now, task_id),
        )

    def _register_lane_tx(self, connection, *, lane, pane_id, nonce, harness, repo, server_id, session_id, command, now):
        if harness not in ("codex", "claude", "copilot-acp"):
            raise ValueError("unsupported harness")
        if not all((lane, pane_id, nonce, repo, server_id, session_id, command)):
            raise ValueError("lane registration fields must be non-empty")
        current = connection.execute("SELECT * FROM lanes WHERE lane = ?", (lane,)).fetchone()
        if current is not None and any(
            current[field] != value
            for field, value in (
                ("pane_id", pane_id),
                ("nonce", nonce),
                ("harness", harness),
                ("repo", repo),
                ("server_id", server_id),
                ("session_id", session_id),
                ("command", command),
            )
        ):
            # A changed identity is a genuinely new incarnation. An
            # outstanding task still bound to the old incarnation must not
            # be silently REBOUND to it -- except `delivery_pending`,
            # which has its own reconciliation escape valve keyed off the
            # task's own recorded pane_nonce (`_reconcile_transition`),
            # not the lane's current one. #871 depends on re-registration
            # succeeding over a `delivery_pending` task to recover a dead
            # pane; every other outstanding status has no such recovery
            # path and would otherwise be orphaned by this rebind.
            outstanding = connection.execute(
                "SELECT id FROM tasks WHERE lane = ? AND status NOT IN "
                "('complete', 'failed', 'cancelled', 'delivery_pending')",
                (lane,),
            ).fetchone()
            if outstanding is not None:
                # agent-dotfiles#144 finding 3: this used to raise here,
                # unconditionally, and the ONLY way out was `lane-done.sh`
                # moving that exact task to a terminal status by renaming the
                # window it owns. A lane freed any other way -- renamed by
                # hand, a worker that died mid-turn, the completion signal
                # never firing (#102, #123, #126 were exactly this) -- wedged
                # every subsequent `register_lane` call for that lane forever,
                # with no self-heal. `cancel_open_task` already existed for
                # exactly this and had no caller anywhere in this tree: the
                # fifth instance of a durability tool built with no wiring
                # (`acp_transport.py` #56, `worktree.sh` #81, `advance-live.sh`,
                # and #140 itself named this exact risk before merging).
                #
                # A CHANGED IDENTITY is the evidence that the old task can
                # never complete through this pane again -- nothing watching
                # tmux would ever see the rename or crash that produced this
                # call; only the caller registering a BRAND NEW incarnation in
                # the lane's place can know that. Cancel the stale task and
                # proceed, rather than wedge the recorder for that lane
                # permanently. Scoped to the exact row `outstanding` above
                # already found -- `delivery_pending` is excluded from that
                # query and stays excluded here, unchanged from #871: it has
                # its own reconciliation path and must not be silently
                # discarded by a re-registration racing an in-flight send.
                self._cancel_task_row(connection, outstanding["id"], now)
        connection.execute(
            """
            INSERT INTO lanes(lane, pane_id, nonce, harness, repo, server_id,
                              session_id, command, updated_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(lane) DO UPDATE SET
                pane_id=excluded.pane_id,
                nonce=excluded.nonce,
                harness=excluded.harness,
                repo=excluded.repo,
                server_id=excluded.server_id,
                session_id=excluded.session_id,
                command=excluded.command,
                updated_at=excluded.updated_at
            """,
            (lane, pane_id, nonce, harness, repo, server_id, session_id, command, now),
        )
        row = connection.execute("SELECT * FROM lanes WHERE lane = ?", (lane,)).fetchone()
        return self._dict(row)

    def register_lane(self, *, lane, pane_id, nonce, harness, repo, server_id, session_id, command):
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            return self._register_lane_tx(
                connection,
                lane=lane,
                pane_id=pane_id,
                nonce=nonce,
                harness=harness,
                repo=repo,
                server_id=server_id,
                session_id=session_id,
                command=command,
                now=now,
            )

    def get_lane(self, lane):
        with contextlib.closing(self._connect()) as connection:
            return self._dict(connection.execute("SELECT * FROM lanes WHERE lane = ?", (lane,)).fetchone())

    def list_lanes(self):
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute("SELECT * FROM lanes ORDER BY lane").fetchall()
        return [self._dict(row) for row in rows]

    def get_component(self, name):
        with contextlib.closing(self._connect()) as connection:
            row = connection.execute("SELECT * FROM components WHERE name = ?", (name,)).fetchone()
        return self._dict(row)

    def get_task(self, task_id):
        with contextlib.closing(self._connect()) as connection:
            return self._dict(connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone())

    def list_tasks(self):
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute("SELECT * FROM tasks ORDER BY created_at, id").fetchall()
        return [self._dict(row) for row in rows]

    @staticmethod
    def _source_task_dict(row):
        if row is None:
            return None
        value = dict(row)
        value["task_id"] = value.pop("id")
        value["evidence"] = json.loads(value.pop("evidence_json"))
        value.pop("updated_at")
        return value

    def get_source_task(self, task_id):
        with contextlib.closing(self._connect()) as connection:
            row = connection.execute("SELECT * FROM source_tasks WHERE id = ?", (task_id,)).fetchone()
        return self._source_task_dict(row)

    def list_source_tasks(self):
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute("SELECT * FROM source_tasks ORDER BY id").fetchall()
        return [self._source_task_dict(row) for row in rows]

    def _reconstruct_task_tx(
        self,
        connection,
        *,
        task_id,
        source_kind,
        source_url,
        source_ref,
        summary,
        source_state,
        status,
        evidence,
        status_marker,
        now,
    ):
        self._require_task_id(task_id)
        if source_kind not in ("issue", "pull"):
            raise ValueError("unsupported GitHub source kind")
        if status not in ("created", "delivered", "accepted", "running", "complete", "failed", "cancelled"):
            raise ValueError("unsupported source task status")
        if not all(isinstance(value, str) and value for value in (source_url, source_ref, summary, source_state)):
            raise ValueError("source task fields must be non-empty")
        if not isinstance(evidence, list) or not all(isinstance(value, str) and value for value in evidence):
            raise ValueError("source task evidence must be non-empty strings")
        if status in TERMINAL_STATUSES and not evidence:
            raise ValueError("terminal source task requires evidence")
        if status_marker is not None and not isinstance(status_marker, str):
            raise ValueError("source task status marker must be text")
        encoded_evidence = json.dumps(evidence, sort_keys=True, separators=(",", ":"))
        connection.execute(
            """
            INSERT INTO source_tasks(
                id, source_kind, source_url, source_ref, summary, source_state,
                status, evidence_json, status_marker, updated_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(id) DO UPDATE SET
                source_kind=excluded.source_kind,
                source_url=excluded.source_url,
                source_ref=excluded.source_ref,
                summary=excluded.summary,
                source_state=excluded.source_state,
                status=excluded.status,
                evidence_json=excluded.evidence_json,
                status_marker=excluded.status_marker,
                updated_at=excluded.updated_at
            """,
            (
                task_id,
                source_kind,
                source_url,
                source_ref,
                summary,
                source_state,
                status,
                encoded_evidence,
                status_marker,
                now,
            ),
        )
        row = connection.execute("SELECT * FROM source_tasks WHERE id = ?", (task_id,)).fetchone()
        return self._source_task_dict(row)

    def reconstruct_task(
        self,
        *,
        task_id,
        source_kind,
        source_url,
        source_ref,
        summary,
        source_state,
        status,
        evidence,
        status_marker,
    ):
        """Replace one local source spool record with facts read from GitHub.

        This intentionally has no lane or pane dependency: reconstruction must
        work after the entire supervisor state directory has been recreated.
        """
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            return self._reconstruct_task_tx(
                connection,
                task_id=task_id,
                source_kind=source_kind,
                source_url=source_url,
                source_ref=source_ref,
                summary=summary,
                source_state=source_state,
                status=status,
                evidence=evidence,
                status_marker=status_marker,
                now=now,
            )

    def _assign_tx(self, connection, *, task_id, lane, pane_nonce, summary, now):
        self._require_task_id(task_id)
        if not summary.strip():
            raise ValueError("task summary must be non-empty")
        self._verify_lane_nonce(connection, lane, pane_nonce)
        source = connection.execute("SELECT * FROM source_tasks WHERE id = ?", (task_id,)).fetchone()
        if source is None:
            raise ValueError("task requires a reconstructed GitHub source")
        if source["source_state"].upper() != "OPEN":
            raise ValueError("GitHub source is not open")
        if source["status"] != "created":
            raise ValueError(f"GitHub source is already {source['status']}")
        existing = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
        if existing is not None:
            if existing["lane"] == lane and existing["pane_nonce"] == pane_nonce and existing["summary"] == summary:
                return self._dict(existing)
            raise ValueError("task id already exists with different assignment")
        try:
            connection.execute(
                """
                INSERT INTO tasks(id, lane, pane_nonce, summary, status, created_at, updated_at)
                VALUES (?, ?, ?, ?, 'created', ?, ?)
                """,
                (task_id, lane, pane_nonce, summary, now, now),
            )
        except sqlite3.IntegrityError as error:
            if "tasks.lane" in str(error):
                raise ValueError(f"lane has an outstanding task: {lane}") from error
            raise
        row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
        return self._dict(row)

    def assign(self, *, task_id, lane, pane_nonce, summary):
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            return self._assign_tx(connection, task_id=task_id, lane=lane, pane_nonce=pane_nonce, summary=summary, now=now)

    def _transition_tx(self, connection, task_id, pane_nonce, allowed, target, timestamp_column, now):
        row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
        if row is None:
            raise ValueError("unknown task")
        self._verify_lane_nonce(connection, row["lane"], pane_nonce)
        if row["pane_nonce"] != pane_nonce:
            raise ValueError("pane incarnation does not match task")
        if row["status"] == target:
            return self._dict(row)
        if row["status"] not in allowed:
            raise ValueError(f"cannot transition task from {row['status']} to {target}")
        connection.execute(
            f"UPDATE tasks SET status = ?, updated_at = ?, {timestamp_column} = ? WHERE id = ?",
            (target, now, now, task_id),
        )
        row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
        return self._dict(row)

    def _transition(self, task_id, pane_nonce, allowed, target, timestamp_column):
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            return self._transition_tx(connection, task_id, pane_nonce, allowed, target, timestamp_column, now)

    def mark_delivery_pending(self, task_id, *, pane_nonce):
        """Persist an ambiguous, non-resendable state before the physical send.

        A task in this state has had delivery attempted but not confirmed. It
        is deliberately not "delivered": nothing here trusts that the send
        reached the pane, only that an attempt is now on record (in
        `delivery_attempted_at`, not `delivered_at`) and cannot be silently
        repeated. `delivered_at` stays null until delivery is genuinely
        confirmed.
        """
        return self._transition(task_id, pane_nonce, ("created",), "delivery_pending", "delivery_attempted_at")

    def mark_delivered(self, task_id, *, pane_nonce):
        return self._transition(task_id, pane_nonce, ("delivery_pending",), "delivered", "delivered_at")

    def _reconcile_transition(self, task_id, pane_nonce, target, timestamp_column):
        """Resolve a `delivery_pending` task without requiring the current lane.

        Unlike `_transition`, this does not check the *lane's* current
        nonce. Reconciliation exists precisely for the case where the pane
        that received the ambiguous send is gone and its lane has since
        been re-registered with a new nonce - requiring the current
        incarnation here would make every stuck delivery permanently
        unreconcilable. Authentication instead comes from the task's own
        `pane_nonce`, recorded at send time: a caller who does not know it
        cannot reconcile the wrong task by guessing.
        """
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
            if row is None:
                raise ValueError("unknown task")
            if row["pane_nonce"] != pane_nonce:
                raise ValueError("pane incarnation does not match task")
            if row["status"] == target:
                return self._dict(row)
            if row["status"] != "delivery_pending":
                raise ValueError(f"cannot reconcile a {row['status']} task")
            connection.execute(
                f"UPDATE tasks SET status = ?, updated_at = ?, {timestamp_column} = ? WHERE id = ?",
                (target, now, now, task_id),
            )
            row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
        return self._dict(row)

    def reconcile_delivery(self, task_id, *, pane_nonce, outcome):
        """Resolve an ambiguous delivery by explicit human decision.

        This is the only path out of `delivery_pending` other than the
        adapter's own post-send confirmation. It exists for the case where
        that confirmation itself failed or the operator inspected the pane
        directly and reached a conclusion the ledger cannot infer on its own
        from echoed terminal text. It authenticates against the task's own
        recorded pane_nonce, not the lane's current one - see
        `_reconcile_transition`.
        """
        if outcome == "delivered":
            return self._reconcile_transition(task_id, pane_nonce, "delivered", "delivered_at")
        if outcome == "failed":
            return self._reconcile_transition(task_id, pane_nonce, "failed", "completed_at")
        raise ValueError("reconciliation outcome must be 'delivered' or 'failed'")

    def accept(self, task_id, *, pane_nonce):
        return self._transition(task_id, pane_nonce, ("delivered",), "accepted", "accepted_at")

    def _cancel_open_task_tx(self, connection, lane, now):
        row = connection.execute(
            "SELECT * FROM tasks WHERE lane = ? AND status NOT IN ('complete','failed','cancelled')",
            (lane,),
        ).fetchone()
        if row is None:
            return None
        self._cancel_task_row(connection, row["id"], now)
        return self._dict(connection.execute("SELECT * FROM tasks WHERE id=?", (row["id"],)).fetchone())

    def cancel_open_task(self, lane):
        """Free a lane's outstanding task by marking it cancelled.

        agent-dotfiles#144 finding 3: this had no caller anywhere in the tree
        until that review. `_register_lane_tx` now calls the narrower
        `_cancel_task_row` directly (same UPDATE, scoped to the specific
        outstanding row it already found, excluding `delivery_pending` --
        this method's own SELECT is intentionally broader and stays available
        standalone, e.g. for a human operator freeing a lane by hand).
        """
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            return self._cancel_open_task_tx(connection, lane, now)

    def record_dispatch(
        self,
        *,
        lane,
        pane_id,
        nonce,
        harness,
        repo,
        server_id,
        session_id,
        command,
        task_id,
        source_kind,
        source_url,
        source_ref,
        summary,
        source_state,
        evidence,
        status_marker=None,
        failpoint=None,
    ):
        """Atomically register the lane, record the GitHub source, assign, and mark delivered.

        agent-dotfiles#144 finding 2: `cli.py`'s free function `record_dispatch`
        used to call `register_lane`, `reconstruct_task`, `assign`,
        `mark_delivery_pending` and `mark_delivered` as five INDEPENDENT
        `Ledger` calls, each its own lock and its own transaction. A crash
        between any two of them left on disk whatever had already committed --
        reproduced by the #144 review as an orphan `lanes` row (`register_lane`
        had committed; nothing after it had) claiming a lane occupied for a
        dispatch that was otherwise never recorded. An orphan row asserting a
        lane is busy is exactly the state this layer exists to prevent.

        This performs the same five writes against ONE connection inside ONE
        transaction, so a failure at any step rolls back everything this call
        has done so far -- there is no window where step 1 committed and step
        2 did not. `failpoint` exists so a test can inject that failure after
        any named step below without a real crash; see `_fail`.

        This does not weaken #140's central safety property. The caller
        (`cli.py`'s `record_dispatch`, invoked from `dispatch.sh`) still
        tolerates this raising -- a ledger failure here still cannot abort a
        dispatch that has already physically happened in the pane. This only
        removes the partial-write window *inside* the recording step itself;
        it does not make the recording step itself load-bearing for the
        dispatch.
        """
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            lane_record = self._register_lane_tx(
                connection,
                lane=lane,
                pane_id=pane_id,
                nonce=nonce,
                harness=harness,
                repo=repo,
                server_id=server_id,
                session_id=session_id,
                command=command,
                now=now,
            )
            self._fail(failpoint, "after_register_lane")
            registered_nonce = lane_record["nonce"]
            self._reconstruct_task_tx(
                connection,
                task_id=task_id,
                source_kind=source_kind,
                source_url=source_url,
                source_ref=source_ref,
                summary=summary,
                source_state=source_state,
                status="created",
                evidence=evidence,
                status_marker=status_marker,
                now=now,
            )
            self._fail(failpoint, "after_reconstruct_task")
            self._assign_tx(
                connection, task_id=task_id, lane=lane, pane_nonce=registered_nonce, summary=summary, now=now
            )
            self._fail(failpoint, "after_assign")
            # The intermediate `delivery_pending` write is not skipped: it is
            # the state machine's only route to `delivered`, and its own
            # guard against a silent resend (see `mark_delivery_pending`'s
            # docstring).
            self._transition_tx(
                connection, task_id, registered_nonce, ("created",), "delivery_pending", "delivery_attempted_at", now
            )
            self._fail(failpoint, "after_mark_delivery_pending")
            task = self._transition_tx(
                connection, task_id, registered_nonce, ("delivery_pending",), "delivered", "delivered_at", now
            )
            self._fail(failpoint, "after_mark_delivered")
        return {"lane": lane_record, "task": task}

    def _write_result(self, task_id, result):
        if not isinstance(result, bytes):
            raise TypeError("result must be bytes")
        if not result.strip():
            raise ValueError("result must be non-empty")
        if len(result) > MAX_RESULT_BYTES:
            raise ValueError("result exceeds 64 KiB limit")
        digest = hashlib.sha256(result).hexdigest()
        destination = self.results_dir / f"{task_id}.md"
        if destination.exists():
            if hashlib.sha256(destination.read_bytes()).hexdigest() != digest:
                raise ValueError("immutable result conflicts with existing content")
            return destination, digest
        descriptor, temporary_name = tempfile.mkstemp(prefix=f".{task_id}.", dir=self.results_dir)
        try:
            with os.fdopen(descriptor, "wb") as handle:
                handle.write(result)
                handle.flush()
                os.fsync(handle.fileno())
            os.chmod(temporary_name, 0o600)
            try:
                os.link(temporary_name, destination)
            except FileExistsError:
                if hashlib.sha256(destination.read_bytes()).hexdigest() != digest:
                    raise ValueError("immutable result conflicts with concurrent content")
            os.chmod(destination, 0o600)
        finally:
            with contextlib.suppress(FileNotFoundError):
                os.unlink(temporary_name)
        return destination, digest

    @staticmethod
    def _fail(failpoint, expected):
        if failpoint == expected:
            raise RuntimeError(expected)

    def complete(self, task_id, result, *, pane_nonce, failpoint=None):
        self._require_task_id(task_id)
        with self._locked():
            with contextlib.closing(self._connect()) as probe:
                existing = probe.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
                if existing is None:
                    raise ValueError("unknown task")
                if existing["pane_nonce"] != pane_nonce:
                    raise ValueError("pane incarnation does not match task")
            destination, digest = self._write_result(task_id, result)
            self._fail(failpoint, "after_result")
            now = int(self.clock())
            with self._transaction() as connection:
                row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
                if row is None:
                    raise ValueError("unknown task")
                if row["pane_nonce"] != pane_nonce:
                    raise ValueError("pane incarnation does not match task")
                if row["status"] == "complete":
                    if row["result_sha256"] != digest:
                        raise ValueError("immutable result conflicts with completed task")
                    return self._dict(row)
                if row["status"] in ("failed", "cancelled"):
                    raise ValueError(f"cannot complete {row['status']} task")
                connection.execute(
                    """
                    UPDATE tasks SET status='complete', result_path=?, result_sha256=?,
                                     updated_at=?, completed_at=? WHERE id=?
                    """,
                    (str(destination), digest, now, now, task_id),
                )
                self._fail(failpoint, "after_task")
                connection.execute(
                    """
                    INSERT OR IGNORE INTO events(key, type, task_id, status, payload_path, created_at)
                    VALUES (?, 'completion', ?, 'pending', ?, ?)
                    """,
                    (f"completion:{task_id}", task_id, str(destination), now),
                )
                self._fail(failpoint, "after_event")
                row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
            return self._dict(row)

    def observe_attention(self, lane, *, pane_nonce, reason="idle"):
        """Durably record that an outstanding task's lane needs attention.

        `reason` names *why* (idle, blocked, approval, unknown). Idle keeps
        the original unsuffixed key (`attention:<task>`) so existing
        idle-triggered event consumers are unaffected; every other reason
        gets its own key (`attention:<task>:<reason>`) so a lane that is
        blocked and later becomes idle (or vice versa) is tracked as two
        distinct durable events, not silently collapsed into one.
        """
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            self._verify_lane_nonce(connection, lane, pane_nonce)
            task = connection.execute(
                """
                SELECT * FROM tasks WHERE lane=?
                  AND status IN ('delivered','accepted','running')
                """,
                (lane,),
            ).fetchone()
            if task is None:
                return None
            key = f"attention:{task['id']}" if reason == "idle" else f"attention:{task['id']}:{reason}"
            connection.execute(
                """
                INSERT OR IGNORE INTO events(key, type, task_id, status, created_at)
                VALUES (?, 'attention', ?, 'pending', ?)
                """,
                (key, task["id"], now),
            )
            event = connection.execute("SELECT * FROM events WHERE key=?", (key,)).fetchone()
        return self._dict(event)

    def observe_idle(self, lane, *, pane_nonce):
        return self.observe_attention(lane, pane_nonce=pane_nonce, reason="idle")

    def list_events(self, *, task_id=None, event_type=None):
        clauses = []
        values = []
        if task_id is not None:
            clauses.append("task_id = ?")
            values.append(task_id)
        if event_type is not None:
            clauses.append("type = ?")
            values.append(event_type)
        where = f" WHERE {' AND '.join(clauses)}" if clauses else ""
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(f"SELECT * FROM events{where} ORDER BY created_at, key", values).fetchall()
        return [self._dict(row) for row in rows]

    def get_event(self, key):
        with contextlib.closing(self._connect()) as connection:
            return self._dict(connection.execute("SELECT * FROM events WHERE key=?", (key,)).fetchone())

    def events_due(self, *, now=None):
        now = int(self.clock() if now is None else now)
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(
                """
                SELECT * FROM events
                 WHERE status='pending'
                    OR (status='notified' AND retry_at IS NOT NULL AND retry_at <= ?)
                 ORDER BY created_at, key
                """,
                (now,),
            ).fetchall()
        return [self._dict(row) for row in rows]

    def mark_notified(self, keys, *, retry_after):
        now = int(self.clock())
        retry_at = now + int(retry_after)
        with self._locked(), self._transaction() as connection:
            for key in keys:
                if connection.execute("SELECT 1 FROM events WHERE key=?", (key,)).fetchone() is None:
                    raise ValueError(f"unknown event: {key}")
                connection.execute(
                    "UPDATE events SET status='notified', notified_at=?, retry_at=? WHERE key=? AND status!='acked'",
                    (now, retry_at, key),
                )

    def ack(self, keys):
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            for key in keys:
                event = connection.execute("SELECT * FROM events WHERE key=?", (key,)).fetchone()
                if event is None:
                    raise ValueError(f"unknown event: {key}")
                if event["type"] == "attention":
                    task = connection.execute("SELECT status FROM tasks WHERE id=?", (event["task_id"],)).fetchone()
                    if task is not None and task["status"] not in TERMINAL_STATUSES:
                        raise ValueError("attention requires task disposition before acknowledgement")
                connection.execute(
                    "UPDATE events SET status='acked', acked_at=?, retry_at=NULL WHERE key=?",
                    (now, key),
                )

    def record_component(self, name, *, healthy, snapshot=None, error=None):
        if healthy and snapshot is None:
            raise ValueError("healthy component requires a snapshot")
        if not healthy and not error:
            raise ValueError("failed component requires an error")
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            existing = connection.execute("SELECT * FROM components WHERE name=?", (name,)).fetchone()
            digest = hashlib.sha256(snapshot).hexdigest() if healthy else (existing["snapshot_sha256"] if existing else None)
            connection.execute(
                """
                INSERT INTO components(name, healthy, error, snapshot_sha256, updated_at)
                VALUES (?, ?, ?, ?, ?)
                ON CONFLICT(name) DO UPDATE SET
                    healthy=excluded.healthy,
                    error=excluded.error,
                    snapshot_sha256=excluded.snapshot_sha256,
                    updated_at=excluded.updated_at
                """,
                (name, int(healthy), None if healthy else error, digest, now),
            )
            row = connection.execute("SELECT * FROM components WHERE name=?", (name,)).fetchone()
        return self._dict(row)

    @staticmethod
    def _atomic_replace(destination, content):
        descriptor, temporary_name = tempfile.mkstemp(prefix=f".{destination.name}.", dir=destination.parent)
        try:
            with os.fdopen(descriptor, "wb") as handle:
                handle.write(content)
                handle.flush()
                os.fsync(handle.fileno())
            os.chmod(temporary_name, 0o600)
            os.replace(temporary_name, destination)
            os.chmod(destination, 0o600)
        finally:
            with contextlib.suppress(FileNotFoundError):
                os.unlink(temporary_name)

    def record_snapshot(self, name, snapshot):
        """Record a healthy component snapshot and emit one bounded diff event."""
        if not COMPONENT_RE.fullmatch(name):
            raise ValueError("invalid component name")
        if not isinstance(snapshot, bytes):
            raise TypeError("snapshot must be bytes")
        digest = hashlib.sha256(snapshot).hexdigest()
        baseline_path = self.snapshots_dir / f"{name}.txt"
        with self._locked():
            if not baseline_path.exists():
                self.record_component(name, snapshot=snapshot, healthy=True)
                self._atomic_replace(baseline_path, snapshot)
                return None
            previous = baseline_path.read_bytes()
            if previous == snapshot:
                self.record_component(name, snapshot=snapshot, healthy=True)
                return None

            old_text = previous.decode("utf-8", errors="replace").splitlines(keepends=True)
            new_text = snapshot.decode("utf-8", errors="replace").splitlines(keepends=True)
            diff = "".join(
                difflib.unified_diff(old_text, new_text, fromfile=f"{name}:previous", tofile=f"{name}:{digest}")
            ).encode()
            marker = b"\n[DIFF TRUNCATED]\n"
            if len(diff) > MAX_RESULT_BYTES:
                diff = diff[: MAX_RESULT_BYTES - len(marker)] + marker
            payload_path = self.event_payloads_dir / f"{name}-{digest}.diff"
            if payload_path.exists():
                if payload_path.read_bytes() != diff:
                    raise ValueError("sensor event payload conflicts with existing content")
            else:
                self._atomic_replace(payload_path, diff)

            now = int(self.clock())
            key = f"sensor:{name}:{digest}"
            with self._transaction() as connection:
                connection.execute(
                    """
                    INSERT INTO components(name, healthy, error, snapshot_sha256, updated_at)
                    VALUES (?, 1, NULL, ?, ?)
                    ON CONFLICT(name) DO UPDATE SET
                        healthy=1, error=NULL, snapshot_sha256=excluded.snapshot_sha256,
                        updated_at=excluded.updated_at
                    """,
                    (name, digest, now),
                )
                connection.execute(
                    """
                    INSERT OR IGNORE INTO events(key, type, status, payload_path, created_at)
                    VALUES (?, 'sensor', 'pending', ?, ?)
                    """,
                    (key, str(payload_path), now),
                )
                event = connection.execute("SELECT * FROM events WHERE key=?", (key,)).fetchone()
            self._atomic_replace(baseline_path, snapshot)
        return self._dict(event)
