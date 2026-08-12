import contextlib
import io
import json
import os
import socket
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
        test is written to catch."""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.register_lane(
                lane="free-9", pane_id="%9", nonce="nonce-9", harness="claude",
                repo=root, server_id="socket:1", session_id="$0", command="claude.exe",
            )
            self.assertTrue(ledger.lane_available("free-9"))
            rc1, out1 = self._dispatch(root, lane="free-9", task="ad900-collide", issue=900, pane_id="%9")
            self.assertEqual(0, rc1, out1)
            self.assertFalse(Ledger(Path(root)).lane_available("free-9"))
            ledger2 = Ledger(Path(root))
            ledger2.cancel_open_task("free-9")
            self.assertTrue(ledger2.lane_available("free-9"))

            # Re-dispatch to the same lane reusing the exact task id, but
            # under a different issue (so the summary no longer matches the
            # cancelled row) -- `_assign_tx` refuses this outright (agent-
            # dotfiles#144 finding 2's docstring), which is exactly the
            # collision step 6 of `dispatch.sh` can hit against a live pane.
            rc2, err2 = self._dispatch_subprocess(root, lane="free-9", task="ad900-collide", issue=901, pane_id="%9")
            self.assertNotEqual(0, rc2, err2)
            self.assertFalse(
                Ledger(Path(root)).lane_available("free-9"),
                "a failed record-dispatch left a previously-free lane reading free again",
            )


class FakeMetadataTransport:
    """Stands in for `TmuxTransport.metadata`/`get_option` -- lane_free's only
    tmux touches (agent-dotfiles#216 added the option read)."""

    def __init__(self, metadata, options=None):
        self._metadata = metadata
        self._options = options or {}
        self.calls = []

    def metadata(self, target):
        self.calls.append(target)
        return self._metadata

    def get_option(self, target, name):
        self.calls.append((target, name))
        return self._options.get(name, "")


class LaneFreeTest(unittest.TestCase):
    """agent-dotfiles#174: the read side of the seam #140 opened.
    `dispatch.sh` calls `cli.py lane-free` once per idle-looking candidate
    instead of trusting the window name -- these exercise `lane_free` (the
    function `main` dispatches to) directly, without a real tmux."""

    def test_a_lane_the_ledger_already_knows_answers_without_touching_tmux(self):
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.register_lane(
                lane="t:3", pane_id="%3", nonce="n", harness="claude", repo="/repo",
                server_id="server", session_id="$0", command="claude.exe",
            )
            transport = FakeMetadataTransport({"pane_id": "%3", "command": "claude.exe", "path": "/repo", "server_id": "server", "session_id": "$0"})

            result = cli.lane_free(ledger, transport, lane="t:3", target="t:3", window_name="ad999-some-task")

            self.assertEqual(
                {"lane": "t:3", "known": True, "free": True, "backfilled": False, "harness": "claude"}, result
            )
            self.assertEqual([], transport.calls, "a known lane must not need a pane read")

    def test_an_occupied_lane_is_not_free_regardless_of_its_current_window_name(self):
        """THE INVERSION: a window renamed to `free-N` by hand must not
        change what the ledger already knows."""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.register_lane(
                lane="t:3", pane_id="%3", nonce="n", harness="claude", repo="/repo",
                server_id="server", session_id="$0", command="claude.exe",
            )
            ledger.reconstruct_task(
                task_id="ad1-task", source_kind="issue",
                source_url="https://github.com/acme/repo/issues/1", source_ref="1",
                summary="in flight", source_state="OPEN", status="created", evidence=["seed"], status_marker=None,
            )
            ledger.assign(task_id="ad1-task", lane="t:3", pane_nonce="n", summary="in flight")
            transport = FakeMetadataTransport({})

            result = cli.lane_free(ledger, transport, lane="t:3", target="t:3", window_name="free-3")

            self.assertFalse(result["free"])
            self.assertEqual([], transport.calls)

    def test_an_unknown_lane_named_free_n_is_backfilled_free_on_first_sight(self):
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            transport = FakeMetadataTransport(
                {"pane_id": "%3", "command": "claude.exe", "path": "/repo", "server_id": "server", "session_id": "$0"},
                options={cli.HARNESS_OPTION: "claude"},
            )

            result = cli.lane_free(ledger, transport, lane="t:3", target="t:3", window_name="free-3")

            self.assertEqual(
                {"lane": "t:3", "known": True, "free": True, "backfilled": True, "harness": "claude"}, result
            )
            self.assertEqual(["t:3", ("t:3", cli.HARNESS_OPTION)], transport.calls)
            self.assertIsNotNone(ledger.get_lane("t:3"))
            self.assertTrue(ledger.lane_available("t:3"))

            # And the SECOND call for the same never-seen-again lane answers
            # from the ledger, not the name -- first sight only.
            transport.calls.clear()
            second = cli.lane_free(ledger, transport, lane="t:3", target="t:3", window_name="free-3")
            self.assertEqual(
                {"lane": "t:3", "known": True, "free": True, "backfilled": False, "harness": "claude"}, second
            )
            self.assertEqual([], transport.calls)

    def test_a_copilot_lane_is_backfilled_free_from_its_recorded_harness_option(self):
        """agent-dotfiles#216: the bug's own reproduction. `council-copilot`
        runs as `node` -- indistinguishable by process name from any other
        Node harness -- so this only works because the harness option is
        read as a recorded fact, not guessed from `command`."""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            transport = FakeMetadataTransport(
                {"pane_id": "%7", "command": "node", "path": "/repo", "server_id": "server", "session_id": "$0"},
                options={cli.HARNESS_OPTION: "copilot"},
            )

            result = cli.lane_free(ledger, transport, lane="t:7", target="t:7", window_name="free-7")

            self.assertEqual(
                {"lane": "t:7", "known": True, "free": True, "backfilled": True, "harness": "copilot"}, result
            )
            self.assertEqual("copilot", ledger.get_lane("t:7")["harness"])

    def test_a_codex_lane_running_a_binary_not_literally_named_codex_is_backfilled(self):
        """agent-dotfiles#216 acceptance: codex under a launcher (also `node`
        live, per the issue's window-8 measurement) must work too, once its
        harness is recorded."""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            transport = FakeMetadataTransport(
                {"pane_id": "%8", "command": "node", "path": "/repo", "server_id": "server", "session_id": "$0"},
                options={cli.HARNESS_OPTION: "codex"},
            )

            result = cli.lane_free(ledger, transport, lane="t:8", target="t:8", window_name="free-8")

            self.assertEqual(
                {"lane": "t:8", "known": True, "free": True, "backfilled": True, "harness": "codex"}, result
            )

    def test_an_unknown_lane_not_named_free_n_is_unknown_not_free(self):
        """Fail closed (agent-dotfiles#174): a lane this code cannot
        positively place is never offered -- the same posture `lanes.sh`'s
        own whitelist (#126) takes for pane state."""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            transport = FakeMetadataTransport({"command": "claude.exe"})

            result = cli.lane_free(ledger, transport, lane="t:3", target="t:3", window_name="ad999-some-task")

            self.assertEqual({"lane": "t:3", "known": False, "free": False, "backfilled": False}, result)
            self.assertEqual([], transport.calls, "an ineligible name must not even probe tmux")
            self.assertIsNone(ledger.get_lane("t:3"))

    def test_an_unrecorded_harness_refuses_the_backfill_without_crashing(self):
        """The bug this exists for: #216's exact reproduction. A `node` pane
        with no `HARNESS_OPTION` set is unidentifiable and MUST stay refused
        -- this is the correct behaviour the issue itself names, not the
        defect. Only a positively recorded harness may pass."""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            transport = FakeMetadataTransport(
                {"pane_id": "%3", "command": "node", "path": "/repo", "server_id": "server", "session_id": "$0"}
            )

            result = cli.lane_free(ledger, transport, lane="t:3", target="t:3", window_name="free-3")

            self.assertFalse(result["free"])
            self.assertIn("node", result.get("reason", ""))
            self.assertIsNone(ledger.get_lane("t:3"))

    def test_a_recorded_harness_the_live_pane_contradicts_refuses_the_backfill(self):
        """Mutation-check target (agent-dotfiles#216 acceptance): a pane
        recorded "claude" but actually running the process a copilot/codex
        lane runs (`node`) must NOT be trusted just because an option is
        present. If this refusal is ever weakened to trust any recorded
        value outright, this is the test that must go red."""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            transport = FakeMetadataTransport(
                {"pane_id": "%3", "command": "node", "path": "/repo", "server_id": "server", "session_id": "$0"},
                options={cli.HARNESS_OPTION: "claude"},
            )

            result = cli.lane_free(ledger, transport, lane="t:3", target="t:3", window_name="free-3")

            self.assertFalse(result["free"])
            self.assertIn("claude", result.get("reason", ""))
            self.assertIn("node", result.get("reason", ""))
            self.assertIsNone(ledger.get_lane("t:3"))


class StrandedClaimRecoveryIsReachable(unittest.TestCase):
    """agent-dotfiles#209. The recovery for a stranded claim has to be
    RUNNABLE, not merely implemented.

    `Ledger.cancel_open_task` was the #144 review's own example of a method
    with no caller, and it still had none: not in `cli.py`'s parser, not
    invoked anywhere outside `tests/`. `reap_stale_lane_claims` would have
    been the second if it shipped without wiring. These drive `cli.py` as a
    SUBPROCESS, the way `dispatch.sh` and a human operator both reach it --
    an import-level test would pass against a parser that never learned the
    subcommand.
    """

    def _run(self, root, *args):
        return subprocess.run(
            [sys.executable, str(SUPERVISOR_DIR / "cli.py"), "--state-dir", str(root), *args],
            capture_output=True,
            text=True,
            timeout=30,
        )

    def _lane(self, root):
        ledger = Ledger(Path(root))
        ledger.register_lane(
            lane="free-9", pane_id="%9", nonce="nonce-9", harness="claude",
            repo=root, server_id="socket:1", session_id="$0", command="claude.exe",
        )
        return ledger

    def test_claim_lane_records_the_owner_pid_it_is_given(self):
        with tempfile.TemporaryDirectory() as root:
            self._lane(root)
            proc = self._run(root, "claim-lane", "--lane", "free-9", "--token", "ad1-x", "--owner-pid", "4242")
            self.assertEqual(0, proc.returncode, proc.stderr)
            self.assertTrue(json.loads(proc.stdout)["claimed"])
            task = Ledger(Path(root)).get_task("ledger-claim:free-9:ad1-x")
            self.assertIn(f"{socket.gethostname()}:4242", task["summary"])

    def test_claim_lane_without_an_owner_is_still_accepted(self):
        """`--owner-pid` is additive. A caller that omits it gets exactly the
        pre-#209 behaviour rather than an error."""
        with tempfile.TemporaryDirectory() as root:
            self._lane(root)
            proc = self._run(root, "claim-lane", "--lane", "free-9", "--token", "ad1-x")
            self.assertEqual(0, proc.returncode, proc.stderr)
            self.assertTrue(json.loads(proc.stdout)["claimed"])
            self.assertNotIn("[owner=", Ledger(Path(root)).get_task("ledger-claim:free-9:ad1-x")["summary"])

    def test_reap_lane_claims_is_runnable_and_frees_a_dead_owners_lane(self):
        with tempfile.TemporaryDirectory() as root:
            ledger = self._lane(root)
            dead = subprocess.Popen([sys.executable, "-c", "pass"])
            dead.wait()
            self.assertEqual(0, self._run(
                root, "claim-lane", "--lane", "free-9", "--token", "ad1-x", "--owner-pid", str(dead.pid),
            ).returncode)
            self.assertFalse(ledger.lane_available("free-9"))

            proc = self._run(root, "reap-lane-claims")
            self.assertEqual(0, proc.returncode, proc.stderr)
            self.assertEqual(1, json.loads(proc.stdout)["count"])
            self.assertTrue(Ledger(Path(root)).lane_available("free-9"))

    def test_reap_lane_claims_leaves_a_live_owners_lane_alone(self):
        with tempfile.TemporaryDirectory() as root:
            ledger = self._lane(root)
            self.assertEqual(0, self._run(
                root, "claim-lane", "--lane", "free-9", "--token", "ad1-x", "--owner-pid", str(os.getpid()),
            ).returncode)
            proc = self._run(root, "reap-lane-claims")
            self.assertEqual(0, proc.returncode, proc.stderr)
            self.assertEqual(0, json.loads(proc.stdout)["count"])
            self.assertFalse(ledger.lane_available("free-9"))

    def test_commit_lane_claim_is_runnable_and_puts_the_claim_out_of_reap_range(self):
        """agent-dotfiles#209 round 2. `dispatch.sh` calls this immediately
        before the `send-keys Enter` that submits the brief, so from the CLI's
        side the contract is: after it returns committed, a reap that finds
        the owner provably dead must still leave the lane held."""
        with tempfile.TemporaryDirectory() as root:
            ledger = self._lane(root)
            dead = subprocess.Popen([sys.executable, "-c", "pass"])
            dead.wait()
            self.assertEqual(0, self._run(
                root, "claim-lane", "--lane", "free-9", "--token", "ad1-x", "--owner-pid", str(dead.pid),
            ).returncode)

            proc = self._run(root, "commit-lane-claim", "--lane", "free-9", "--token", "ad1-x")
            self.assertEqual(0, proc.returncode, proc.stderr)
            self.assertTrue(json.loads(proc.stdout)["committed"])

            reap = self._run(root, "reap-lane-claims")
            self.assertEqual(0, reap.returncode, reap.stderr)
            self.assertEqual(0, json.loads(reap.stdout)["count"])
            self.assertFalse(ledger.lane_available("free-9"))

            # ...and the scoped release the trap uses will not free it either,
            # and says so: an operator following the refusal's recovery steps
            # must not read `released: true` off a command that freed nothing.
            rel = self._run(root, "release-lane-claim", "--lane", "free-9", "--token", "ad1-x")
            self.assertEqual(0, rel.returncode, rel.stderr)
            self.assertFalse(json.loads(rel.stdout)["released"])
            self.assertIn("cancel-open-task", json.loads(rel.stdout)["hint"])
            self.assertFalse(Ledger(Path(root)).lane_available("free-9"))

    def test_release_lane_claim_reports_true_only_when_it_freed_something(self):
        """The control: the same command on a claim that is still only a
        reservation reports the release it actually performed."""
        with tempfile.TemporaryDirectory() as root:
            ledger = self._lane(root)
            self.assertEqual(0, self._run(
                root, "claim-lane", "--lane", "free-9", "--token", "ad1-x", "--owner-pid", "4242",
            ).returncode)
            rel = self._run(root, "release-lane-claim", "--lane", "free-9", "--token", "ad1-x")
            self.assertEqual(0, rel.returncode, rel.stderr)
            self.assertTrue(json.loads(rel.stdout)["released"])
            self.assertTrue(ledger.lane_available("free-9"))

    def test_commit_lane_claim_refuses_a_claim_that_was_never_made(self):
        """`dispatch.sh` treats a non-committed result as fatal and does not
        send, so this refusal has to be visible in the exit-0 JSON rather than
        implied by an absence."""
        with tempfile.TemporaryDirectory() as root:
            self._lane(root)
            proc = self._run(root, "commit-lane-claim", "--lane", "free-9", "--token", "never-claimed")
            self.assertEqual(0, proc.returncode, proc.stderr)
            value = json.loads(proc.stdout)
            self.assertFalse(value["committed"])
            self.assertEqual("missing", value["reason"])
            self.assertTrue(Ledger(Path(root)).lane_available("free-9"))

    def test_cancel_open_task_is_the_operators_hammer_and_is_runnable(self):
        """The manual half of the recovery `dispatch.sh`'s refusal now names:
        it clears whatever outstanding task holds the lane, including a
        `ledger-hold:` row the automatic reap deliberately will not touch."""
        with tempfile.TemporaryDirectory() as root:
            ledger = self._lane(root)
            ledger.mark_lane_held("free-9", note="ledger record failed")
            self.assertFalse(ledger.lane_available("free-9"))
            self.assertEqual(0, self._run(root, "reap-lane-claims").returncode)
            self.assertFalse(ledger.lane_available("free-9"), "the reap must not touch a deliberate hold")

            proc = self._run(root, "cancel-open-task", "--lane", "free-9")
            self.assertEqual(0, proc.returncode, proc.stderr)
            self.assertEqual("cancelled", json.loads(proc.stdout)["cancelled"]["status"])
            self.assertTrue(Ledger(Path(root)).lane_available("free-9"))

    def test_cancel_open_task_on_an_already_free_lane_is_a_no_op(self):
        with tempfile.TemporaryDirectory() as root:
            self._lane(root)
            proc = self._run(root, "cancel-open-task", "--lane", "free-9")
            self.assertEqual(0, proc.returncode, proc.stderr)
            self.assertIsNone(json.loads(proc.stdout)["cancelled"])


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

            self.assertEqual(
                {"task": "ad193-telegram-to-director", "known": True, "lane": "t:3"}, result
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

            self.assertEqual(
                {"task": "ad999-never-dispatched", "known": False, "lane": None}, result
            )


if __name__ == "__main__":
    unittest.main()
