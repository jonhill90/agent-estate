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


class PrLaneCliTest(unittest.TestCase):
    """agent-supervisor#159: `dispatch.sh` had no way to represent "work on
    PR N" as distinct from "work on issue N" -- a review or a fix pass on a
    PR whose issue was already claimed by the in-flight work that opened it
    had nowhere to record itself except that same issue's claim, which
    correctly refused. `record-dispatch --pr <N>` records the dispatch keyed
    by the PR instead (`source_kind='pull'`); `pr-lane` is its read, exercised
    end to end through `cli.main` the same way `IssueLaneCliTest` exercises
    `issue-lane`.
    """

    def _record_dispatch(self, root, *, lane, task, issue=None, pr=None, worktree=None):
        output = io.StringIO()
        argv = [
            "--state-dir", root, "record-dispatch",
            "--lane", lane, "--task", task, "--summary", f"{task} summary",
            "--pane-id", "%3", "--pane-path", root, "--command", "claude.exe",
            "--server-id", "socket:1", "--session-id", "$0",
            "--issue", str(issue if issue is not None else 1),
            "--github", "jonhill90/agent-dotfiles",
        ]
        if pr is not None:
            argv += ["--pr", str(pr)]
        if worktree is not None:
            argv += ["--worktree", worktree]
        with contextlib.redirect_stdout(output):
            rc = cli.main(argv)
        self.assertEqual(0, rc, output.getvalue())

    def _pr_lane(self, root, pr):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            rc = cli.main(["--state-dir", root, "pr-lane", "--pr", str(pr)])
        self.assertEqual(0, rc, output.getvalue())
        return json.loads(output.getvalue())

    def _issue_lane(self, root, issue):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            rc = cli.main(["--state-dir", root, "issue-lane", "--issue", str(issue)])
        self.assertEqual(0, rc, output.getvalue())
        return json.loads(output.getvalue())

    def test_a_pr_scoped_dispatch_answers_with_the_lane_working_it(self):
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(root, lane="t:3", task="as159-rev149", issue=112, pr=149)

            result = self._pr_lane(root, 149)

            self.assertEqual(
                {"pr": "149", "known": True, "lane": "t:3", "task": "as159-rev149"}, result
            )

    def test_an_unrecorded_pr_answers_known_false_rather_than_erroring(self):
        """Fails closed the same way `issue-lane`/`task-lane` do: `known:false`
        is what `dispatch.sh` step 0.6 treats as "not already claimed"."""
        with tempfile.TemporaryDirectory() as root:
            result = self._pr_lane(root, 404)

            self.assertEqual({"pr": "404", "known": False, "lane": None, "task": None}, result)

    def test_a_pr_scoped_dispatch_does_not_answer_issue_lane_for_its_issue(self):
        """The whole point of keying by `source_kind='pull'` instead of
        `'issue'`: this dispatch's issue stays claimed by the ORIGINAL work
        (never touched by this call at all), and `issue-lane` for that issue
        must keep answering whatever it already answered -- never this
        PR-scoped task, which recorded no `source_kind='issue'` row."""
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(root, lane="t:3", task="as112-original", issue=112)
            self._record_dispatch(root, lane="t:4", task="as159-rev149", issue=112, pr=149)

            self.assertEqual("t:3", self._issue_lane(root, 112)["lane"])
            self.assertEqual("t:4", self._pr_lane(root, 149)["lane"])

    def test_a_completed_prs_task_no_longer_answers_known_true(self):
        """agent-supervisor#159's own acceptance: `pr-lane` is what
        `dispatch.sh` asks BEFORE picking a lane, to refuse a duplicate
        dispatch of a PR someone already has -- but a review that finished
        (or was cancelled) must not go on blocking every later dispatch of
        the same PR forever. Filtered to OPEN status, unlike `issue-lane`
        (which has no live caller and answers the latest row regardless)."""
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(root, lane="t:3", task="as159-rev149", issue=112, pr=149)
            row = Ledger(Path(root)).get_task("as159-rev149")
            Ledger(Path(root)).complete("as159-rev149", b"approved", pane_nonce=row["pane_nonce"])

            result = self._pr_lane(root, 149)

            self.assertEqual({"pr": "149", "known": False, "lane": None, "task": None}, result)
