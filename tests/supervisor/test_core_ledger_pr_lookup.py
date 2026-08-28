import sqlite3
import sys
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from tests.supervisor.test_core_helpers import LedgerTestBase  # noqa: E402


class PrLookupTest(LedgerTestBase):
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

    def test_record_pr_for_task_round_trips_through_get_task_for_pr_number(self):
        """agent-supervisor#308 item 1: the explicit "task X's own work
        opened PR N" record, written after the fact for an issue-keyed
        dispatch that had no PR number yet when it started."""
        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-3",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="as308-original",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/308",
            source_ref="308",
            summary="#308 original fix",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3"],
            status_marker=None,
        )
        self.assertIsNone(self.ledger.get_task_for_pr_number(repo="acme/agent-supervisor", pr_number="316"))

        self.ledger.record_pr_for_task(task_id="as308-original", repo="acme/agent-supervisor", pr_number="316")
        found = self.ledger.get_task_for_pr_number(repo="acme/agent-supervisor", pr_number="316")
        self.assertIsNotNone(found)
        self.assertEqual("free-3", found["lane"])
        self.assertEqual("as308-original", found["id"])

        # An int and its string spelling are the same PR.
        self.assertEqual(
            "as308-original",
            self.ledger.get_task_for_pr_number(repo="acme/agent-supervisor", pr_number=316)["id"],
        )

        # A second call for the same (repo, pr_number) CORRECTS, not
        # duplicates -- an operator re-running lane-done.sh's best-effort
        # write must not raise a primary-key conflict.
        self.ledger.record_pr_for_task(task_id="as308-original", repo="acme/agent-supervisor", pr_number="316")

    def test_record_pr_for_task_refuses_an_unknown_task(self):
        with self.assertRaises(ValueError):
            self.ledger.record_pr_for_task(task_id="no-such-task", repo="acme/agent-supervisor", pr_number="316")
