import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
from tests.supervisor.test_core_helpers import _seed_pre_is_review_column_database  # noqa: E402


class KnownMisclassifiedReviewBackfillTest(unittest.TestCase):
    """agent-supervisor#640's own measurement named two live rows the regex
    fallback gets wrong forever (`_task_looks_like_review`'s docstring
    explains why, in both directions, and why the regex itself is left
    unfixed): `as637-rerev636` and `Skills266-rerev284`. Backfilled by id,
    not by a widened regex -- see `Ledger._KNOWN_MISCLASSIFIED_REVIEW_
    TASK_IDS`'s own comment for why a general regex fix was rejected."""

    def test_both_known_ids_are_backfilled_to_is_review_1(self):
        root = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: __import__("shutil").rmtree(root, ignore_errors=True))
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
            task_id="Skills266-rerev284", source_kind="pull",
            source_url="https://github.com/jonhill90/Skills/pull/284", source_ref="284",
            summary="re-review of PR 284's fix", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-2", "pr: 284"], status_marker=None,
        )
        ledger.complete("Skills266-rerev284", b"done", pane_nonce="nonce-2")

        # Both rows were written with the column already present and
        # `is_review` unspecified (defaults `None`, exactly what a caller
        # that has not been told about this backfill would send) -- the
        # backfill still catches them because it re-runs, idempotently,
        # every `__init__`, not only immediately after the column migration.
        self.assertIsNone(ledger.get_source_task("as637-rerev636")["is_review"])
        reopened = Ledger(root, clock=lambda: 2_000)
        self.assertEqual(1, reopened.get_source_task("as637-rerev636")["is_review"])
        self.assertEqual(1, reopened.get_source_task("Skills266-rerev284")["is_review"])

    def test_backfill_never_touches_an_issue_scoped_row_of_the_same_id(self):
        """`_backfill_known_misclassified_review_tasks`'s own `WHERE` clause
        requires `source_kind = 'pull'` -- an issue-scoped row that happens
        to reuse one of the two known ids (implausible, but not the same
        guarantee as a real PR-scoped collision) must not be silently
        reclassified as a review."""
        root = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: __import__("shutil").rmtree(root, ignore_errors=True))
        ledger = Ledger(root, clock=lambda: 1_000)
        ledger.reconstruct_task(
            task_id="as637-rerev636", source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/637", source_ref="637",
            summary="unrelated issue-scoped row reusing the same id", source_state="OPEN",
            status="created", evidence=["x"], status_marker=None,
        )

        reopened = Ledger(root, clock=lambda: 2_000)

        self.assertIsNone(reopened.get_source_task("as637-rerev636")["is_review"])

    def test_idempotent_across_repeated_opens(self):
        root = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: __import__("shutil").rmtree(root, ignore_errors=True))
        _seed_pre_is_review_column_database(root)
        Ledger(root, clock=lambda: 2_000)
        second = Ledger(root, clock=lambda: 3_000)
        third = Ledger(root, clock=lambda: 4_000)
        self.assertEqual(1, second.get_source_task("as637-rerev636")["is_review"])
        self.assertEqual(1, third.get_source_task("as637-rerev636")["is_review"])


