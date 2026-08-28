import concurrent.futures
import sys
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
from tests.supervisor.test_core_helpers import LedgerTestBase  # noqa: E402


class ConcurrencyTest(LedgerTestBase):
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
