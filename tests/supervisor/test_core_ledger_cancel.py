import hashlib
import sys
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from tests.supervisor.test_core_helpers import LedgerTestBase  # noqa: E402


class CancelTest(LedgerTestBase):
    def test_cancel_open_task_requires_a_result_or_abandoned(self):
        """agent-supervisor#649: no default -- a caller must say which,
        rather than a bare cancel silently landing as an abandonment. This is
        the change that stops the next 951."""
        self.assign()
        with self.assertRaisesRegex(ValueError, "result or abandoned=True"):
            self.ledger.cancel_open_task("app-review")

    def test_cancel_open_task_refuses_both_a_result_and_abandoned(self):
        self.assign()
        with self.assertRaisesRegex(ValueError, "not both"):
            self.ledger.cancel_open_task("app-review", result=b"shipped", abandoned=True)

    def test_cancel_open_task_with_a_result_persists_it_with_a_hash(self):
        """The mutation-check in the direction the issue is actually about:
        a cancel carrying a result must not be indistinguishable from an
        abandonment. `result_sha256` is written alongside `result_path`, the
        way `record_completion` (`Ledger.complete`) already does."""
        self.assign()
        cancelled = self.ledger.cancel_open_task("app-review", result=b"# Delivered\n\nPR merged; pane was gone.\n")
        self.assertEqual("cancelled", cancelled["status"])
        self.assertIsNotNone(cancelled["result_path"])
        self.assertIsNotNone(cancelled["result_sha256"])
        self.assertEqual(
            hashlib.sha256(b"# Delivered\n\nPR merged; pane was gone.\n").hexdigest(),
            cancelled["result_sha256"],
        )
        self.assertEqual(
            b"# Delivered\n\nPR merged; pane was gone.\n", Path(cancelled["result_path"]).read_bytes()
        )

    def test_cancel_open_task_abandoned_persists_no_result(self):
        """The other direction of the same mutation-check: an explicit
        abandonment must still be recordable with no result, and must not
        pick up a stray result_path/sha256 from anywhere."""
        self.assign()
        cancelled = self.ledger.cancel_open_task("app-review", abandoned=True)
        self.assertEqual("cancelled", cancelled["status"])
        self.assertIsNone(cancelled["result_path"])
        self.assertIsNone(cancelled["result_sha256"])

    def test_cancel_open_task_result_and_abandoned_produce_different_rows(self):
        """A change where every cancel looks the same as before has done
        nothing -- assert the two are actually distinguishable, not just
        independently correct."""
        self.assign("review-870")
        with_result = self.ledger.cancel_open_task("app-review", result=b"# shipped\n")
        self.assign("review-871")
        abandoned = self.ledger.cancel_open_task("app-review", abandoned=True)
        self.assertIsNotNone(with_result["result_path"])
        self.assertIsNone(abandoned["result_path"])

    def test_list_terminal_tasks_missing_result_finds_an_abandoned_cancel_not_a_result_bearing_one(self):
        """agent-supervisor#649: the discoverability half -- a terminal row
        with no result must be queryable without knowing the schema, and the
        cheap regression the issue names: a PR-scoped task whose PR merged
        (recorded here as a cancel carrying a result) must never show up in
        this list the way `cancelled` used to for all 951 rows."""
        self.ledger.reconstruct_task(
            task_id="as649-shipped", source_kind="pull", source_url="https://example/pull/649",
            source_ref="649", summary="PR merged before the pane died", source_state="OPEN",
            status="created", evidence=[], status_marker=None,
        )
        self.ledger.assign(
            task_id="as649-shipped", lane="app-review", pane_nonce="nonce-22-a", summary="shipped"
        )
        self.ledger.cancel_open_task("app-review", result=b"# PR #649 merged\n")

        self.assign("review-870")
        self.ledger.cancel_open_task("app-review", abandoned=True)

        missing_ids = {row["id"] for row in self.ledger.list_terminal_tasks_missing_result()}
        self.assertIn("review-870", missing_ids)
        self.assertNotIn("as649-shipped", missing_ids)
