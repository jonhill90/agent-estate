import contextlib
import io
import json
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import cli  # noqa: E402
from core import Ledger  # noqa: E402


class TaskLaneCliTest(unittest.TestCase):
    """agent-dotfiles#212: `dispatch.sh` refuses a review dispatched to the
    lane that authored the PR under review, and it must answer that from the
    ledger, not by touching tmux or guessing from a window name. `task-lane`
    is the read `cli.main` exposes for that -- exercised end to end, the way
    `dispatch.sh` actually calls it, not just `Ledger.get_task` directly.
    """

    def _record_dispatch(self, root, *, lane, task, issue):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            rc = cli.main([
                "--state-dir", root, "record-dispatch",
                "--lane", lane, "--task", task, "--summary", f"#{issue} summary",
                "--pane-id", "%3", "--pane-path", root, "--command", "claude.exe",
                "--server-id", "socket:1", "--session-id", "$0",
                "--issue", str(issue), "--github", "jonhill90/agent-dotfiles",
            ])
        self.assertEqual(0, rc, output.getvalue())

    def _task_lane(self, root, task):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            rc = cli.main(["--state-dir", root, "task-lane", "--task", task])
        self.assertEqual(0, rc, output.getvalue())
        return json.loads(output.getvalue())

    def test_a_dispatched_task_answers_with_the_lane_that_authored_it(self):
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(root, lane="t:3", task="ad193-telegram-to-director", issue=193)

            result = self._task_lane(root, "ad193-telegram-to-director")

            # agent-supervisor#631/#634: `pane_id` is this task's frozen
            # `lanes.pane_id` snapshot, taken at assignment time so a later
            # renumber-driven reuse of the "t:3" STRING for a different
            # pane cannot corrupt this task's own recorded identity. It is
            # not incidental output -- `resolve-pr-contributors.sh` and
            # `verdict-independence.sh` both read it (`task-lane`'s own
            # `elif` branch in cli.py cites both) to feed
            # `lane-relation --other-pane-id`, so it belongs in this
            # contract, not just in the row this test happens to produce.
            self.assertEqual(
                {"task": "ad193-telegram-to-director", "known": True, "lane": "t:3", "pane_id": "%3"},
                result,
            )

    def test_the_answer_survives_the_task_completing_and_the_lane_going_free_again(self):
        """The whole point (#212): a lane finishing its work and going idle
        again must not erase who wrote it. `tasks.id` is a SQLite PRIMARY
        KEY and `Ledger._assign_tx` raises rather than let a second lane
        reuse an existing task id -- authorship is permanent even after
        `lane_available` starts answering True for the same lane again."""
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(root, lane="t:3", task="ad193-telegram-to-director", issue=193)
            ledger = Ledger(Path(root))
            row = ledger.get_task("ad193-telegram-to-director")
            ledger.complete("ad193-telegram-to-director", b"done", pane_nonce=row["pane_nonce"])
            self.assertTrue(ledger.lane_available("t:3"), "lane should read free once its task is complete")

            result = self._task_lane(root, "ad193-telegram-to-director")

            self.assertEqual("t:3", result["lane"], "authorship must outlive the lane going free again")

    def test_an_unknown_task_id_answers_known_false_rather_than_erroring(self):
        """Fails closed the same way `lane_free` does for an unknown lane
        (#174): `dispatch.sh` treats `known:false` as "authorship cannot be
        determined" and refuses the whole dispatch rather than guessing."""
        with tempfile.TemporaryDirectory() as root:
            result = self._task_lane(root, "ad999-never-dispatched")

            # `pane_id` is `None` here, not `""` -- the same distinction
            # `lane` already draws between "this task genuinely does not
            # exist" (`None`) and "this task exists but predates a column"
            # (`""`, see the dispatched-task test above for a task that DOES
            # exist). Collapsing the two into one falsy value would erase
            # that distinction for every OTHER unknown-row verb in this file
            # (`issue-lane`, `pr-lane` do the same for their own fields), not
            # just this one -- `known:false` staying a self-consistent
            # shape across every lookup verb is worth more than shaving one
            # key off this specific response.
            self.assertEqual(
                {"task": "ad999-never-dispatched", "known": False, "lane": None, "pane_id": None}, result
            )
