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


class WorktreeLaneCliTest(unittest.TestCase):
    """agent-supervisor#117: `dispatch.sh --reviews-pr`'s last-resort read,
    exercised through `cli.main` the same way `TaskLaneCliTest` and
    `IssueLaneCliTest` exercise their own lookups end to end."""

    def _record_dispatch(self, root, *, lane, task, issue, worktree):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            rc = cli.main([
                "--state-dir", root, "record-dispatch",
                "--lane", lane, "--task", task, "--summary", f"#{issue} summary",
                "--pane-id", "%3", "--pane-path", root, "--command", "claude.exe",
                "--server-id", "socket:1", "--session-id", "$0",
                "--issue", str(issue), "--github", "jonhill90/agent-dotfiles",
                "--worktree", worktree,
            ])
        self.assertEqual(0, rc, output.getvalue())

    def _worktree_lane(self, root, path):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            rc = cli.main(["--state-dir", root, "worktree-lane", "--path", path])
        self.assertEqual(0, rc, output.getvalue())
        return json.loads(output.getvalue())

    def test_a_recorded_worktree_answers_with_the_lane_that_built_it(self):
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(
                root, lane="t:3", task="as117-fix", issue=117, worktree="/tmp/ad-117-fix-99"
            )

            result = self._worktree_lane(root, "/tmp/ad-117-fix-99")

            self.assertEqual(
                {"path": "/tmp/ad-117-fix-99", "known": True, "lane": "t:3", "task": "as117-fix"},
                result,
            )

    def test_an_unrecorded_worktree_answers_known_false_rather_than_erroring(self):
        """Fails closed the same way `task-lane`/`issue-lane` do: `known:false`
        is "cannot be determined", never guessed at."""
        with tempfile.TemporaryDirectory() as root:
            result = self._worktree_lane(root, "/tmp/never-recorded")

            self.assertEqual(
                {"path": "/tmp/never-recorded", "known": False, "lane": None, "task": None}, result
            )

    def test_the_answer_does_not_depend_on_the_branch_the_pr_under_review_used(self):
        """agent-supervisor#117's own reproduction: task
        `as101-reviewspr-inference` produced PR branch
        `fix/101-not-a-review-escape` -- a slug sharing no text with the
        dispatch's own. This lookup never reads a branch name at all, so it
        answers identically no matter what the eventual branch was called."""
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(
                root, lane="t:3", task="as101-pr-inference-fix", issue=101,
                worktree="/tmp/ad-101-pr-inference-fix-7",
            )

            result = self._worktree_lane(root, "/tmp/ad-101-pr-inference-fix-7")

            self.assertEqual("t:3", result["lane"])
            self.assertEqual("as101-pr-inference-fix", result["task"])

    def _worktree_lane_include_reviews(self, root, path):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            rc = cli.main(
                ["--state-dir", root, "worktree-lane", "--path", path, "--include-reviews"]
            )
        self.assertEqual(0, rc, output.getvalue())
        return json.loads(output.getvalue())

    def test_include_reviews_answers_for_a_reviewing_lanes_own_worktree(self):
        """agent-supervisor#212: AGENTS.md invariant 10 documented this
        command as a reviewing lane's own self-lookup, but never ran it
        from one -- the default filters out exactly the review-shaped task
        id a reviewing lane's own worktree carries. `--include-reviews` is
        the flag a self-lookup caller passes; exercised end to end through
        `cli.main`, the same way the rest of this class does."""
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(
                root, lane="t:6", task="as211-rev212", issue=211, worktree="/tmp/ad-211-rev212-9"
            )

            without_flag = self._worktree_lane(root, "/tmp/ad-211-rev212-9")
            self.assertEqual(
                {"path": "/tmp/ad-211-rev212-9", "known": False, "lane": None, "task": None},
                without_flag,
                "default behaviour (dispatch.sh --reviews-pr's question) is unchanged",
            )

            with_flag = self._worktree_lane_include_reviews(root, "/tmp/ad-211-rev212-9")
            self.assertEqual(
                {"path": "/tmp/ad-211-rev212-9", "known": True, "lane": "t:6", "task": "as211-rev212"},
                with_flag,
            )
