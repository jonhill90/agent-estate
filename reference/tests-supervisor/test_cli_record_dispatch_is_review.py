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


class RecordDispatchIsReviewCliTest(unittest.TestCase):
    """agent-supervisor#640: `record-dispatch --is-review` is `dispatch.sh`'s
    OWN forwarded `--reviews-pr` fact, not a guess -- exercised end to end
    through `cli.main`, the same layer `dispatch.sh` actually calls, so a
    drift between the argparse flag and what `Ledger.record_dispatch` does
    with it would show up here rather than only in a `core.py`-level test.
    """

    def _record_dispatch(self, root, *, lane, task, pr, is_review=False, issue=112):
        output = io.StringIO()
        argv = [
            "--state-dir", root, "record-dispatch",
            "--lane", lane, "--task", task, "--summary", f"{task} summary",
            "--pane-id", "%3", "--pane-path", root, "--command", "claude.exe",
            "--server-id", "socket:1", "--session-id", "$0",
            "--issue", str(issue), "--pr", str(pr),
            "--github", "jonhill90/agent-supervisor",
        ]
        if is_review:
            argv.append("--is-review")
        with contextlib.redirect_stdout(output):
            rc = cli.main(argv)
        self.assertEqual(0, rc, output.getvalue())

    def test_is_review_flag_records_1_regardless_of_task_name(self):
        """The exact shape agent-supervisor#640 measured: a task named
        `rerev...` must record `is_review=1` because `--reviews-pr` (which
        forwards as `--is-review`) said so -- never because its name
        happened to match a regex."""
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(root, lane="t:3", task="as637-rerev636", pr=636, is_review=True)

            self.assertEqual(1, Ledger(Path(root)).get_source_task("as637-rerev636")["is_review"])

    def test_omitting_is_review_with_pr_records_an_explicit_0_not_unknown(self):
        """A `--pr`-scoped fix pass (no `--is-review`) must record a KNOWN
        `0`, not leave the column `NULL` -- `get_contributor_tasks_for_pr`
        only falls back to the regex for a row nobody ever recorded an
        answer for, and a fix pass IS an answered case."""
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(root, lane="t:3", task="as1-revamp-parser", pr=640, is_review=False)

            self.assertEqual(0, Ledger(Path(root)).get_source_task("as1-revamp-parser")["is_review"])

    def test_issue_scoped_dispatch_never_records_is_review(self):
        """No `--pr` at all (an ordinary issue-scoped dispatch) -- `is_review`
        is moot for a `source_kind='issue'` row (`get_contributor_tasks_for_pr`
        never reads it), and must stay `NULL`, not `0`."""
        with tempfile.TemporaryDirectory() as root:
            output = io.StringIO()
            argv = [
                "--state-dir", root, "record-dispatch",
                "--lane", "t:3", "--task", "as1-plain-issue-dispatch", "--summary", "summary",
                "--pane-id", "%3", "--pane-path", root, "--command", "claude.exe",
                "--server-id", "socket:1", "--session-id", "$0",
                "--issue", "112", "--github", "jonhill90/agent-supervisor",
            ]
            with contextlib.redirect_stdout(output):
                rc = cli.main(argv)
            self.assertEqual(0, rc, output.getvalue())

            self.assertIsNone(Ledger(Path(root)).get_source_task("as1-plain-issue-dispatch")["is_review"])

    def test_is_review_end_to_end_through_contributor_pr_lanes(self):
        """The full path `dispatch.sh --reviews-pr` relies on: a task
        recorded `--is-review` never shows up as a contributor, whatever
        it's named -- and a `--pr`-only fix pass recorded WITHOUT it stays a
        contributor even when its own name would trip the regex fallback."""
        with tempfile.TemporaryDirectory() as root:
            self._record_dispatch(root, lane="t:3", task="as1-revamp-parser", pr=640, is_review=False)
            Ledger(Path(root)).complete(
                "as1-revamp-parser", b"done", pane_nonce=Ledger(Path(root)).get_task("as1-revamp-parser")["pane_nonce"]
            )
            self._record_dispatch(root, lane="t:4", task="as2-rereview640", pr=640, is_review=True)

            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                rc = cli.main(["--state-dir", root, "contributor-pr-lanes", "--pr", "640"])
            self.assertEqual(0, rc, output.getvalue())
            result = json.loads(output.getvalue())

            self.assertEqual(
                {"pr": "640", "known": True, "contributors": [{"lane": "t:3", "task": "as1-revamp-parser"}]},
                result,
            )
