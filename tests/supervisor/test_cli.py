import contextlib
import io
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import cli  # noqa: E402
from core import Ledger  # noqa: E402


class BlindGithubSensor:
    def __init__(self, ledger, repositories, timeout):
        self.ledger = ledger

    def collect_all(self):
        return {"events": [], "errors": [{"component": "github-Hill90", "error": "timeout"}], "recoveries": []}


class RecordingAdapter:
    instances = []

    def __init__(self, ledger, transport):
        self.observed = []
        self.notified = False
        self.__class__.instances.append(self)

    def observe_lane(self, lane):
        self.observed.append(lane)
        return None

    def notify_architecture(self, *, lane, retry_after):
        self.notified = True
        return True


class RecordingACPAdapter:
    instances = []

    def __init__(self, ledger, transport_factory):
        self.assigned = []
        self.__class__.instances.append(self)

    def register_lane(self, *, lane, target, harness, repo, nonce):
        return {"lane": lane, "harness": harness, "pane_id": "sess-1", "session_id": "sess-1"}

    def assign_task(self, *, lane, task_id, summary):
        self.assigned.append((lane, task_id, summary))
        return {"status": "complete"}

    def observe_lane(self, lane):
        return None

    def notify_architecture(self, *, lane, retry_after):
        return False


class CliTest(unittest.TestCase):
    def test_tick_gates_lane_advancement_and_notification_while_github_is_blind(self):
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            for lane in ("architecture", "worker"):
                ledger.register_lane(
                    lane=lane, pane_id=f"%{lane}", nonce=lane, harness="codex", repo="/repo",
                    server_id="server", session_id="$1", command="codex",
                )
            RecordingAdapter.instances.clear()
            output = io.StringIO()
            with patch.object(cli, "StateSensor", BlindGithubSensor), patch.object(cli, "TmuxAdapter", RecordingAdapter), contextlib.redirect_stdout(output):
                self.assertEqual(0, cli.main(["--state-dir", root, "tick"]))
            value = json.loads(output.getvalue())
            adapter = RecordingAdapter.instances[-1]
            self.assertTrue(value["gated"])
            self.assertEqual(["github-Hill90"], value["sensor_blockers"])
            self.assertEqual([], adapter.observed)
            self.assertFalse(adapter.notified)

    def test_reconcile_resolves_an_ambiguous_delivery_without_pane_capture(self):
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.register_lane(
                lane="architecture", pane_id="%19", nonce="nonce-19", harness="codex", repo="/repo",
                server_id="server", session_id="$1", command="codex",
            )
            ledger.reconstruct_task(
                task_id="flaky-task", source_kind="issue",
                source_url="https://github.com/jonhill90/Hill90/issues/901", source_ref="a" * 40,
                summary="Ambiguous", source_state="OPEN", status="created", evidence=[], status_marker=None,
            )
            ledger.assign(task_id="flaky-task", lane="architecture", pane_nonce="nonce-19", summary="Ambiguous")
            ledger.mark_delivery_pending("flaky-task", pane_nonce="nonce-19")

            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                self.assertEqual(
                    0,
                    cli.main(
                        ["--state-dir", root, "reconcile", "--task", "flaky-task", "--outcome", "delivered"]
                    ),
                )
            value = json.loads(output.getvalue())
            self.assertEqual("delivered", value["status"])
            self.assertEqual("delivered", ledger.get_task("flaky-task")["status"])

    def test_reconcile_survives_lane_re_registration_after_a_dead_pane(self):
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.register_lane(
                lane="architecture", pane_id="%19", nonce="nonce-dead", harness="codex", repo="/repo",
                server_id="server", session_id="$1", command="codex",
            )
            ledger.reconstruct_task(
                task_id="flaky-task", source_kind="issue",
                source_url="https://github.com/jonhill90/Hill90/issues/902", source_ref="a" * 40,
                summary="Ambiguous", source_state="OPEN", status="created", evidence=[], status_marker=None,
            )
            ledger.assign(task_id="flaky-task", lane="architecture", pane_nonce="nonce-dead", summary="Ambiguous")
            ledger.mark_delivery_pending("flaky-task", pane_nonce="nonce-dead")

            # The pane died and was re-registered under a new tmux pane and nonce
            # before a human got to reconcile the stuck delivery.
            ledger.register_lane(
                lane="architecture", pane_id="%25", nonce="nonce-reborn", harness="codex", repo="/repo",
                server_id="server", session_id="$1", command="codex",
            )
            self.assertEqual("nonce-reborn", ledger.get_lane("architecture")["nonce"])

            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                self.assertEqual(
                    0,
                    cli.main(
                        ["--state-dir", root, "reconcile", "--task", "flaky-task", "--outcome", "delivered"]
                    ),
                )
            value = json.loads(output.getvalue())
            self.assertEqual("delivered", value["status"])
            self.assertEqual("delivered", ledger.get_task("flaky-task")["status"])

    def test_register_with_copilot_acp_harness_dispatches_through_acp_adapter(self):
        RecordingACPAdapter.instances.clear()
        with tempfile.TemporaryDirectory() as root:
            output = io.StringIO()
            with patch.object(cli, "ACPAdapter", RecordingACPAdapter), contextlib.redirect_stdout(output):
                self.assertEqual(
                    0,
                    cli.main([
                        "--state-dir", root, "register",
                        "--lane", "copilot-worker", "--target", "unused",
                        "--harness", "copilot-acp", "--repo", "/repo",
                    ]),
                )
            value = json.loads(output.getvalue())
            self.assertEqual("copilot-acp", value["harness"])
            self.assertEqual(1, len(RecordingACPAdapter.instances))

    def test_assign_to_a_copilot_acp_lane_dispatches_through_acp_adapter_not_tmux(self):
        """The real point of the wiring: a lane registered as copilot-acp
        must route through ACPTransport for dispatch, while every other lane
        keeps using TmuxAdapter untouched."""
        RecordingACPAdapter.instances.clear()
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.register_lane(
                lane="copilot-worker", pane_id="sess-1", nonce="nonce-acp", harness="copilot-acp",
                repo="/repo", server_id="acp", session_id="sess-1", command="copilot",
            )
            output = io.StringIO()
            with patch.object(cli, "ACPAdapter", RecordingACPAdapter), contextlib.redirect_stdout(output):
                self.assertEqual(
                    0,
                    cli.main([
                        "--state-dir", root, "assign",
                        "--lane", "copilot-worker", "--task", "t1", "--summary", "Do it",
                    ]),
                )
            adapter = RecordingACPAdapter.instances[-1]
            self.assertEqual([("copilot-worker", "t1", "Do it")], adapter.assigned)

    def test_tick_without_the_canonical_sensor_is_also_gated(self):
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.register_lane(
                lane="architecture", pane_id="%architecture", nonce="architecture", harness="codex", repo="/repo",
                server_id="server", session_id="$1", command="codex",
            )
            RecordingAdapter.instances.clear()
            output = io.StringIO()
            with patch.object(cli, "TmuxAdapter", RecordingAdapter), contextlib.redirect_stdout(output):
                self.assertEqual(0, cli.main(["--state-dir", root, "tick", "--no-sensors"]))
            value = json.loads(output.getvalue())
            adapter = RecordingAdapter.instances[-1]
            self.assertTrue(value["gated"])
            self.assertEqual(["github-sensor-disabled"], value["sensor_blockers"])
            self.assertFalse(adapter.notified)


class CliIsRunnable(unittest.TestCase):
    """Every other test in this file calls ``cli.main()`` by import, which proves
    the logic and says nothing about whether the file can be RUN. It could not:
    ``cli.py`` defined ``main()`` and never called it, so
    ``python3 scripts/supervisor/cli.py --help`` printed zero bytes and exited 0.

    Exit 0 with no output is the worst possible failure here -- a wrapper script
    checking the return code sees success, and a human running --help sees a
    tool with no options. Drive the file as a subprocess, the way a caller does.
    """

    def _run(self, *args):
        return subprocess.run(
            [sys.executable, str(SUPERVISOR_DIR / "cli.py"), *args],
            capture_output=True,
            text=True,
            timeout=30,
        )

    def test_help_prints_usage_and_exits_zero(self):
        proc = self._run("--help")
        self.assertEqual(0, proc.returncode, proc.stderr)
        # The specific assertion that fails on a missing __main__ guard: an
        # unreachable module is silent, and silence used to pass.
        self.assertTrue(proc.stdout.strip(), "cli.py --help produced no output")
        self.assertIn("usage", proc.stdout.lower())

    def test_default_repositories_are_the_harness_repos_only(self):
        """`tick` runs GitHub sensors against every entry in DEFAULT_REPOSITORIES.
        The list arrived with the port and named a different estate entirely --
        one this supervisor must not touch. It was inert only because the module
        had no entry point; adding one made it live. Pin the list.
        """
        self.assertEqual(
            ["agent-dotfiles", "agent-evals", "skills", "skills-private"],
            sorted(repo["name"] for repo in cli.DEFAULT_REPOSITORIES),
        )
        # Match on repo identity, not a substring: the GitHub owner is
        # `jonhill90`, so a naive `"hill90/" in blob` check flags every
        # legitimate entry. The first version of this test did exactly that.
        estate = {"jonhill90/Hill90", "jonhill90/hill90-app", "jonhill90/hill90-docs"}
        self.assertEqual(set(), estate & {repo["github"] for repo in cli.DEFAULT_REPOSITORIES})

    def test_a_real_subcommand_dispatches_end_to_end(self):
        """The two tests above pin argparse, not dispatch. Shown by review of
        #94: a `cli.py` whose entry point ran `parser().parse_args()` and then
        `sys.exit(0)` -- parser wired, `main()` never called -- passed both of
        them and the whole suite. That is the same defect one layer down.

        `status` is the right subcommand for this: it touches no tmux, no
        network, and writes only inside the temporary state directory it is
        given.
        """
        with tempfile.TemporaryDirectory() as root:
            proc = self._run("--state-dir", root, "status")
            self.assertEqual(0, proc.returncode, proc.stderr)
            # Parsing alone cannot produce this: it is main()'s own output.
            json.loads(proc.stdout)

    def test_default_repository_paths_exist_with_the_case_they_are_written_in(self):
        """`/Users/jon/source/repos/Personal/skills` resolved on this machine
        only because the volume is case-insensitive APFS; the directory is
        `Skills`. On a case-sensitive volume `_collect_git` raises, the
        `git-skills` component goes unhealthy, and `tick` gates only on
        components whose name starts with `github-` -- so a blind `git-` sensor
        would not gate and `tick` would carry on as if the repo were fine.

        `os.path.realpath` is what distinguishes them: a plain `isdir` returns
        True for either spelling here, which is why the original check missed it.
        """
        for repo in cli.DEFAULT_REPOSITORIES:
            path = repo["path"]
            if not os.path.isdir(path):
                continue  # not this machine; nothing to compare against
            self.assertEqual(
                os.path.realpath(path), path,
                f"{repo['name']}: on-disk name differs in case from {path}",
            )

    def test_unknown_subcommand_is_a_nonzero_exit(self):
        # Guards the other half: if the entry point were added but wired to
        # something that always returns 0, this would still pass wrongly. A real
        # argparse dispatch rejects garbage.
        proc = self._run("no-such-subcommand")
        self.assertNotEqual(0, proc.returncode)


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


if __name__ == "__main__":
    unittest.main()
