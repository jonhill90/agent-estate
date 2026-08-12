import hashlib
import concurrent.futures
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

from core import Ledger, claim_owner_token, pid_is_alive  # noqa: E402


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

        after = ledger.get_task("review-870")
        self.assertEqual(before["id"], after["id"])
        self.assertEqual(before["lane"], after["lane"])
        self.assertEqual(before["pane_nonce"], after["pane_nonce"])
        self.assertEqual(before["summary"], after["summary"])
        self.assertEqual(before["status"], after["status"])
        self.assertEqual(before["created_at"], after["created_at"])
        self.assertIsNone(after["delivery_attempted_at"])

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


if __name__ == "__main__":
    unittest.main()
