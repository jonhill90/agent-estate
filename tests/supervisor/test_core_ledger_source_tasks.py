import contextlib
import sys
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from tests.supervisor.test_core_helpers import LedgerTestBase  # noqa: E402


class SourceTasksTest(LedgerTestBase):
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
