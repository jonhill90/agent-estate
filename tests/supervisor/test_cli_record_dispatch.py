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


class RecordDispatchCliTest(unittest.TestCase):
    """agent-dotfiles#144: exercises `record-dispatch` / `record-completion`
    end to end through `cli.main`, the way `dispatch.sh` / `lane-done.sh`
    actually call them -- not just `Ledger.record_dispatch` directly."""

    def _dispatch(self, root, *, lane, task, issue, pane_id="%3"):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            rc = cli.main([
                "--state-dir", root, "record-dispatch",
                "--lane", lane, "--task", task, "--summary", f"#{issue} summary",
                "--pane-id", pane_id, "--pane-path", root, "--command", "claude.exe",
                "--server-id", "socket:1", "--session-id", "$0",
                "--issue", str(issue), "--github", "jonhill90/agent-dotfiles",
            ])
        return rc, output.getvalue()

    def test_re_dispatching_the_same_issue_under_a_different_task_id_is_recorded(self):
        """agent-dotfiles#144 finding 1, the reviewer's own repro: a lane
        fails, work is re-briefed under a new task id, same issue. This used
        to raise `UNIQUE constraint failed: source_tasks.source_url` on the
        second call."""
        with tempfile.TemporaryDirectory() as root:
            rc1, out1 = self._dispatch(root, lane="free-3", task="ad999-first", issue=999)
            self.assertEqual(0, rc1, out1)
            rc2, out2 = self._dispatch(root, lane="free-4", task="ad999-rereview", issue=999, pane_id="%4")
            self.assertEqual(0, rc2, out2)
            self.assertEqual("delivered", json.loads(out2)["task"]["status"])

    def test_confirm_landed_flag_sets_accepted_at_without_omitting_it_stays_null(self):
        """agent-supervisor#193: `--confirm-landed` is `dispatch.sh`'s own
        evidence that its send actually landed (a position-anchored proof
        check plus a confirmed-empty box) -- forwarded straight through to
        `Ledger.record_dispatch(accepted=...)`. Omitted (every call before
        this flag existed, and every call that omits it still) leaves
        `accepted_at` null, same as before."""
        with tempfile.TemporaryDirectory() as root:
            rc, out = self._dispatch(root, lane="free-3", task="as193-confirmed", issue=193)
            self.assertEqual(0, rc, out)
            self.assertIsNone(Ledger(Path(root)).get_task("as193-confirmed")["accepted_at"])

            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                rc2 = cli.main([
                    "--state-dir", root, "record-dispatch",
                    "--lane", "free-4", "--task", "as193-landed", "--summary", "#193 summary",
                    "--pane-id", "%4", "--pane-path", root, "--command", "claude.exe",
                    "--server-id", "socket:1", "--session-id", "$0",
                    "--issue", "193", "--github", "jonhill90/agent-dotfiles",
                    "--confirm-landed",
                ])
            self.assertEqual(0, rc2, output.getvalue())
            landed_task = Ledger(Path(root)).get_task("as193-landed")
            self.assertIsNotNone(landed_task["accepted_at"])
            # And it stays `delivered` -- NOT the self-report status
            # `Ledger.accept` uses -- so it is still `list_delivered_open_tasks`'s
            # candidate set (the completion reconciler's whole input).
            self.assertEqual("delivered", landed_task["status"])

    def test_redispatching_the_same_issue_and_slug_after_completion_gets_a_distinct_task_id(self):
        """agent-supervisor#140: `tasks.id` is `<prefix><issue>-<slug>`
        (dispatch.sh's tmux window-name convention) -- per issue+slug, not
        per dispatch ATTEMPT. A completed prior attempt's row is the
        historical record CLAUDE.md invariant 1 requires; it must not be
        overwritten, and a later re-dispatch of the SAME issue+slug must not
        collide with it either. Before this fix, the second `_dispatch`
        below raised `ValueError("task id already exists with different
        assignment")` inside `Ledger.record_dispatch`, which `cli.py`'s
        `record_dispatch` only reported as a generic, non-fatal 'LEDGER
        RECORD FAILED' -- the lane was left working under a HELD placeholder
        instead of its real task (this repro is the issue's own, confirmed
        via `sqlite3 ledger.sqlite3` against a live estate)."""
        with tempfile.TemporaryDirectory() as root:
            rc1, out1 = self._dispatch(root, lane="free-3", task="as101-review-as114", issue=101)
            self.assertEqual(0, rc1, out1)

            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                rc = cli.main([
                    "--state-dir", root, "record-completion",
                    "--task", "as101-review-as114", "--note", "first attempt done",
                ])
            self.assertEqual(0, rc, output.getvalue())
            self.assertEqual("complete", Ledger(Path(root)).get_task("as101-review-as114")["status"])

            # The redispatch: same issue, same slug, same --task -- a
            # different lane doing the same window-name-convention dispatch
            # dispatch.sh would perform for a re-briefed retry.
            rc2, out2 = self._dispatch(root, lane="free-4", task="as101-review-as114", issue=101, pane_id="%4")
            self.assertEqual(0, rc2, out2)
            second = json.loads(out2)["task"]
            # A genuinely NEW row, not a collision with the first.
            self.assertNotEqual("as101-review-as114", second["id"])
            self.assertTrue(second["id"].startswith("as101-review-as114-r"), second["id"])
            self.assertEqual("delivered", second["status"])
            self.assertEqual("free-4", second["lane"])
            # The first attempt's row is untouched -- the record of what
            # actually happened, not silently overwritten.
            first_after = Ledger(Path(root)).get_task("as101-review-as114")
            self.assertEqual("complete", first_after["status"])
            self.assertEqual("free-3", first_after["lane"])

            # lane-done.sh always passes --task with the bare window name
            # (dispatch.sh never learns the suffixed id) -- record_completion's
            # own --lane fallback must still resolve the REDISPATCHED task,
            # not report "unknown task" for a lane that in fact just finished.
            output2 = io.StringIO()
            with contextlib.redirect_stdout(output2):
                rc3 = cli.main([
                    "--state-dir", root, "record-completion",
                    "--task", "as101-review-as114", "--lane", "free-4",
                    "--note", "second attempt done",
                ])
            self.assertEqual(0, rc3, output2.getvalue())
            self.assertEqual(second["id"], json.loads(output2.getvalue())["id"])
            self.assertEqual("complete", Ledger(Path(root)).get_task(second["id"])["status"])

    def test_record_completion_of_an_unknown_task_raises_rather_than_reporting_success(self):
        """agent-dotfiles#144 finding 4: `record_completion` looked up the
        task and raised `RuntimeError(f"unknown task: {task}")` when it was
        missing. Pins that the CLI surfaces this as a failure, not as a
        silently empty success -- a mutation to `return {}` instead must turn
        this red."""
        with tempfile.TemporaryDirectory() as root:
            proc = subprocess.run(
                [sys.executable, str(SUPERVISOR_DIR / "cli.py"), "--state-dir", root,
                 "record-completion", "--task", "does-not-exist", "--note", "done"],
                capture_output=True, text=True,
            )
            self.assertNotEqual(0, proc.returncode)
            self.assertIn("unknown task", proc.stderr)

    def test_record_dispatch_refuses_an_unmapped_pane_command_without_a_harness_override(self):
        """agent-dotfiles#144 finding 4: `HARNESS_BY_COMMAND` only maps
        codex/claude/claude.exe. A pane running an unmapped command (the
        review's example: `node`, seen live on agent-dotfiles:7/:8) must
        raise and name the command, not silently default to a harness the
        pane is not actually running."""
        with tempfile.TemporaryDirectory() as root:
            proc = subprocess.run(
                [sys.executable, str(SUPERVISOR_DIR / "cli.py"), "--state-dir", root,
                 "record-dispatch", "--lane", "free-7", "--task", "ad999-node",
                 "--summary", "#999 summary", "--pane-id", "%7", "--pane-path", root,
                 "--command", "node", "--server-id", "socket:1", "--session-id", "$0",
                 "--issue", "999"],
                capture_output=True, text=True,
            )
            self.assertNotEqual(0, proc.returncode)
            self.assertIn("cannot tell which harness", proc.stderr)
            self.assertIn("node", proc.stderr)
            # And no partial record was left behind by the refusal.
            self.assertIsNone(Ledger(Path(root)).get_task("ad999-node"))

    def _dispatch_subprocess(self, root, *, lane, task, issue, pane_id="%3"):
        proc = subprocess.run(
            [sys.executable, str(SUPERVISOR_DIR / "cli.py"), "--state-dir", root,
             "record-dispatch", "--lane", lane, "--task", task, "--summary", f"#{issue} summary",
             "--pane-id", pane_id, "--pane-path", root, "--command", "claude.exe",
             "--server-id", "socket:1", "--session-id", "$0",
             "--issue", str(issue), "--github", "jonhill90/agent-dotfiles"],
            capture_output=True, text=True,
        )
        return proc.returncode, proc.stderr

    def test_a_failed_record_dispatch_leaves_the_lane_held_not_free(self):
        """agent-dotfiles#188 finding 1, exercised through `cli.main` itself
        (via subprocess, the way `dispatch.sh` actually invokes it) -- not
        `Ledger.mark_lane_held` directly -- so this actually proves the
        wiring in `cli.record_dispatch`'s except clause, not just that the
        method works in isolation. Without that except clause calling
        `mark_lane_held` before re-raising, `lane_available` would still
        read True after the failed call below: this is the mutation this
        test is written to catch.

        The collision seeded below is a task id still ACTIVELY held by a
        DIFFERENT lane (status `created`, never delivered or completed) --
        a genuine, unresolved identity conflict, not a redispatch of a
        finished attempt. agent-supervisor#140's fix pass (`cli.py`'s
        `_unique_redispatch_task_id`) only steps around a collision with a
        TERMINAL prior row; a live collision like this one must still fail
        loud exactly as before. (Before that fix pass this test seeded the
        collision by cancelling free-9's own task and redispatching under
        the same id with a mismatched summary -- which the fix now legally
        resolves with a suffixed id instead of raising, so it no longer
        exercises this test's actual point.)"""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.register_lane(
                lane="free-9", pane_id="%9", nonce="nonce-9", harness="claude",
                repo=root, server_id="socket:1", session_id="$0", command="claude.exe",
            )
            self.assertTrue(ledger.lane_available("free-9"))
            ledger.register_lane(
                lane="free-8", pane_id="%8", nonce="nonce-8", harness="claude",
                repo=root, server_id="socket:1", session_id="$0", command="claude.exe",
            )
            ledger.reconstruct_task(
                task_id="ad900-collide", source_kind="issue",
                source_url="https://github.com/jonhill90/agent-dotfiles/issues/900", source_ref="900",
                summary="a different lane got here first", source_state="OPEN", status="created",
                evidence=["seeded by test_cli.py"], status_marker=None,
            )
            ledger.assign(
                task_id="ad900-collide", lane="free-8", pane_nonce="nonce-8",
                summary="a different lane got here first",
            )

            # Dispatch to free-9 reusing the exact task id already live under
            # free-8 -- `_assign_tx` refuses this outright (agent-
            # dotfiles#144 finding 2's docstring), which is exactly the
            # collision step 6 of `dispatch.sh` can hit against a live pane.
            rc2, err2 = self._dispatch_subprocess(root, lane="free-9", task="ad900-collide", issue=901, pane_id="%9")
            self.assertNotEqual(0, rc2, err2)
            self.assertFalse(
                Ledger(Path(root)).lane_available("free-9"),
                "a failed record-dispatch left a previously-free lane reading free again",
            )
