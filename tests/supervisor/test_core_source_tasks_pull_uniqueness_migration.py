import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402


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


