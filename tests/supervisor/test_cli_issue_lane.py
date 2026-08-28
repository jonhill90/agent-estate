import contextlib
import io
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import cli  # noqa: E402
from core import Ledger  # noqa: E402


class IssueLaneCliTest(unittest.TestCase):
    """agent-supervisor#35: `dispatch.sh`'s `--reviews-pr` guard used to
    determine a PR's author by regexing its head branch -- unreviewable
    through anything but a `lane/<n>-<slug>` branch, which most of this
    repo's own merged PRs are not. `issue-lane` is the read `cli.main`
    exposes so it can ask the ledger by ISSUE instead, the same way
    `task-lane` (above) already asks by task id -- exercised end to end
    through `cli.main`, not `Ledger.get_task_for_issue` directly.
    """

    def _record_dispatch(self, root, *, lane, task, issue, worktree=None, github="jonhill90/agent-dotfiles"):
        output = io.StringIO()
        argv = [
            "--state-dir", root, "record-dispatch",
            "--lane", lane, "--task", task, "--summary", f"#{issue} summary",
            "--pane-id", "%3", "--pane-path", root, "--command", "claude.exe",
            "--server-id", "socket:1", "--session-id", "$0",
            "--issue", str(issue), "--github", github,
        ]
        if worktree is not None:
            argv += ["--worktree", worktree]
        with contextlib.redirect_stdout(output):
            rc = cli.main(argv)
        self.assertEqual(0, rc, output.getvalue())

    def _issue_lane(self, root, issue, repo=None):
        output = io.StringIO()
        argv = ["--state-dir", root, "issue-lane", "--issue", str(issue)]
        if repo is not None:
            argv += ["--repo", repo]
        with contextlib.redirect_stdout(output):
            rc = cli.main(argv)
        self.assertEqual(0, rc, output.getvalue())
        return json.loads(output.getvalue())

    def _author_issue_lane(self, root, issue, head_ref=None, repo=None):
        output = io.StringIO()
        argv = ["--state-dir", root, "author-issue-lane", "--issue", str(issue)]
        if head_ref is not None:
            argv += ["--head-ref", head_ref]
        if repo is not None:
            argv += ["--repo", repo]
        with contextlib.redirect_stdout(output):
            rc = cli.main(argv)
        self.assertEqual(0, rc, output.getvalue())
        return json.loads(output.getvalue())

    def test_a_dispatched_issue_answers_with_the_lane_that_authored_its_task(self):
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(root, lane="t:3", task="ad195-scrub-secrets", issue=195)

            result = self._issue_lane(root, 195)

            self.assertEqual(
                {
                    "issue": "195",
                    "known": True,
                    "lane": "t:3",
                    "task": "ad195-scrub-secrets",
                    "status": "delivered",
                    "completed_at": None,
                },
                result,
            )

    def test_the_answer_survives_cancel_open_task_freeing_the_lane(self):
        """agent-supervisor#79: `cancel-open-task` is a lane-reconcile verb,
        but review authorship is still a durable fact. Cancelling frees the
        lane by making the task terminal; it must not erase the task/source
        rows that `dispatch.sh --reviews-pr` reads through `issue-lane`."""
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(root, lane="t:3", task="as79-reopen", issue=79)
            proc = subprocess.run(
                [
                    sys.executable, str(SUPERVISOR_DIR / "cli.py"),
                    "--state-dir", root, "cancel-open-task", "--lane", "t:3", "--abandoned",
                ],
                text=True,
                capture_output=True,
                check=False,
            )
            self.assertEqual(0, proc.returncode, proc.stderr)
            self.assertTrue(Ledger(Path(root)).lane_available("t:3"), "cancel must still free the lane")

            result = self._issue_lane(root, 79)

            self.assertEqual("t:3", result["lane"], "authorship must outlive cancellation")
            ledger = Ledger(Path(root))
            self.assertEqual("cancelled", ledger.get_task("as79-reopen")["status"])
            self.assertEqual("cancelled", ledger.get_source_task("as79-reopen")["status"])

    def test_the_answer_never_depends_on_the_branch_the_pr_under_review_used(self):
        """The whole point of #35: this reads back what `record_dispatch`
        wrote for the ISSUE -- no branch name is an input anywhere in this
        call. A `chore/`, `fix/`, or hand-pushed branch resolves identically
        to a `lane/` one, because none of them are read here at all."""
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(root, lane="t:4", task="ad196-public-scrub", issue=196)

            self.assertEqual("t:4", self._issue_lane(root, 196)["lane"])

    def test_an_unknown_issue_answers_known_false_rather_than_erroring(self):
        """Fails closed the same way `task-lane` does (#174/#212):
        `dispatch.sh` treats `known:false` as "authorship cannot be
        determined" and tries its next source rather than guessing."""
        with tempfile.TemporaryDirectory() as root:
            result = self._issue_lane(root, 404)

            self.assertEqual(
                {
                    "issue": "404",
                    "known": False,
                    "lane": None,
                    "task": None,
                    "status": None,
                    "completed_at": None,
                },
                result,
            )

    def test_author_issue_lane_skips_later_review_tasks(self):
        """agent-supervisor#76: the authorship read exposed to shell callers
        must not drift when later review tasks are recorded for the issue."""
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(root, lane="t:3", task="as76-author-lane-drift", issue=76)
            self._record_dispatch(root, lane="t:4", task="as76-review-as73", issue=76)
            self._record_dispatch(root, lane="t:5", task="as76-rev73b", issue=76)

            author = self._author_issue_lane(root, 76)

            self.assertEqual(
                {"issue": "76", "known": True, "lane": "t:3", "task": "as76-author-lane-drift"},
                author,
            )

    def test_author_issue_lane_resolves_by_head_ref_after_redispatch(self):
        """agent-supervisor#77: an issue first attempted on one lane, then
        re-dispatched to a second lane that actually produced the PR, must
        resolve to the SECOND lane -- the one the PR's head branch names --
        not the first (stale) non-review task, which is what the old
        "first non-review task" rule picked."""
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(root, lane="t:3", task="as13-as13-ci-as-a-gate", issue=13)
            self._record_dispatch(root, lane="t:5", task="as13-as13-reopen", issue=13)

            author = self._author_issue_lane(root, 13, head_ref="lane/13-as13-reopen")

            self.assertEqual(
                {"issue": "13", "known": True, "lane": "t:5", "task": "as13-as13-reopen"},
                author,
            )

    def test_author_issue_lane_unknown_when_ambiguous_and_head_ref_absent(self):
        """Two non-review tasks and no head ref to disambiguate them: this
        must answer `known:false`, not guess by position."""
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(root, lane="t:3", task="as13-as13-ci-as-a-gate", issue=13)
            self._record_dispatch(root, lane="t:5", task="as13-as13-reopen", issue=13)

            author = self._author_issue_lane(root, 13)

            self.assertEqual({"issue": "13", "known": False, "lane": None, "task": None}, author)

    def test_author_issue_lane_cross_repo_collision_resolves_by_repo_and_refuses_when_ambiguous(self):
        """agent-supervisor#146: the exact measured defect. Issue #181 exists
        in both `jonhill90/agent-dotfiles` and `jonhill90/skills` --
        `author-issue-lane --issue 181 --head-ref ...` (no `--repo`) answered
        `agent-dotfiles:6` with `known:true` when the real author of the
        `skills` PR was `skills:2`. With `--repo` given, this must resolve to
        the right repo's lane; with it omitted, the collision must be
        reproducible as a refusal, not a wrong answer."""
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(
                root, lane="agent-dotfiles:6", task="ad181-roster-the-orphaned-skills",
                issue=181, github="jonhill90/agent-dotfiles",
            )
            self._record_dispatch(
                root, lane="skills:2", task="skills181-declare-fix-conflict",
                issue=181, github="jonhill90/skills",
            )

            skills_author = self._author_issue_lane(root, 181, repo="jonhill90/skills")
            self.assertEqual(
                {"issue": "181", "known": True, "lane": "skills:2", "task": "skills181-declare-fix-conflict"},
                skills_author,
            )

            dotfiles_author = self._author_issue_lane(root, 181, repo="jonhill90/agent-dotfiles")
            self.assertEqual("agent-dotfiles:6", dotfiles_author["lane"])

            # Remove the repo argument again (the pre-fix call shape) and
            # show the collision is reproducible as a fail-closed refusal --
            # never a wrong-repo answer.
            ambiguous = self._author_issue_lane(root, 181)
            self.assertEqual({"issue": "181", "known": False, "lane": None, "task": None}, ambiguous)

    def _contributor_issue_lanes(self, root, issue):
        output = io.StringIO()
        argv = ["--state-dir", root, "contributor-issue-lanes", "--issue", str(issue)]
        with contextlib.redirect_stdout(output):
            rc = cli.main(argv)
        self.assertEqual(0, rc, output.getvalue())
        return json.loads(output.getvalue())

    def test_contributor_issue_lanes_returns_every_non_review_task(self):
        """agent-supervisor#190: unlike `author-issue-lane` (which refuses
        to pick between two non-review candidates with no head_ref to
        disambiguate -- see the ambiguous test just above), this returns
        BOTH -- the full contributor set `dispatch.sh --reviews-pr` excludes
        a review from, not a single narrowed-down author."""
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(root, lane="t:3", task="as190-original-author", issue=190)
            self._record_dispatch(root, lane="t:4", task="as190-fix-pass", issue=190)

            result = self._contributor_issue_lanes(root, 190)

            self.assertEqual("190", result["issue"])
            self.assertTrue(result["known"])
            # Order is not asserted: unlike the core-level test above (which
            # controls a fake clock), these two dispatches race real
            # wall-clock time, so `created_at` may tie and the tiebreak
            # (`tasks.id ASC`) is not what this test is about -- only that
            # BOTH contributors are present, which the set comparison proves.
            self.assertEqual(
                {("t:3", "as190-original-author"), ("t:4", "as190-fix-pass")},
                {(row["lane"], row["task"]) for row in result["contributors"]},
            )

    def test_contributor_issue_lanes_excludes_review_tasks(self):
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(root, lane="t:3", task="as76-author-lane-drift", issue=76)
            self._record_dispatch(root, lane="t:4", task="as76-review-as73", issue=76)

            result = self._contributor_issue_lanes(root, 76)

            self.assertEqual([{"lane": "t:3", "task": "as76-author-lane-drift"}], result["contributors"])

    def test_contributor_issue_lanes_unknown_issue_is_empty_not_an_error(self):
        with tempfile.TemporaryDirectory() as root:
            result = self._contributor_issue_lanes(root, 404)

            self.assertEqual({"issue": "404", "known": False, "contributors": []}, result)
