import contextlib
import io
import json
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
        self.registered = []
        self.__class__.instances.append(self)

    def register_lane(self, *, lane, target, harness, repo, nonce):
        self.registered.append((lane, harness))
        return {"lane": lane, "harness": harness, "pane_id": "%1", "transport": "send-keys"}

    def observe_lane(self, lane):
        self.observed.append(lane)
        return None

    def notify_supervisor(self, *, lane, retry_after):
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

    def notify_supervisor(self, *, lane, retry_after):
        return False


class RecordingPiRPCAdapter:
    """`main()` always constructs one of these -- it is `pi`'s alternative
    transport, not `pi`'s only one -- so a plain instance count cannot tell
    a test whether it was actually DRIVEN. Assertions below check
    `registered`/`assigned`, which only grow when this adapter's own methods
    are called, not merely constructed."""

    instances = []

    def __init__(self, ledger, transport_factory):
        self.registered = []
        self.assigned = []
        self.__class__.instances.append(self)

    def register_lane(self, *, lane, target, harness, repo, nonce):
        self.registered.append((lane, harness))
        return {"lane": lane, "harness": harness, "transport": "pi-rpc", "pane_id": "sess-1", "session_id": "sess-1"}

    def assign_task(self, *, lane, task_id, summary):
        self.assigned.append((lane, task_id, summary))
        return {"status": "complete"}

    def observe_lane(self, lane):
        return None

    def notify_supervisor(self, *, lane, retry_after):
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

    # as#132: the acceptance test. `observe`'s lane filter used to hardcode
    # the literal "architecture" -- a flag/default rename alone leaves that
    # literal matching nothing, so the supervisor lane stops being excluded
    # and starts being treated as an ordinary dispatchable worker. This is
    # the mutation the fix (`_is_supervisor_lane`) targets; the PR body
    # carries the before/after demonstration with the literal reverted by
    # hand.
    def test_observe_excludes_the_supervisor_lane_registered_as_architecture(self):
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            for lane in ("architecture", "worker"):
                ledger.register_lane(
                    lane=lane, pane_id=f"%{lane}", nonce=lane, harness="codex", repo="/repo",
                    server_id="server", session_id="$1", command="codex",
                )
            RecordingAdapter.instances.clear()
            output = io.StringIO()
            with patch.object(cli, "TmuxAdapter", RecordingAdapter), contextlib.redirect_stdout(output):
                self.assertEqual(0, cli.main(["--state-dir", root, "observe"]))
            adapter = RecordingAdapter.instances[-1]
            self.assertEqual(["worker"], adapter.observed, "the supervisor lane must never be observed as a worker")

    def test_observe_excludes_the_supervisor_lane_registered_as_supervisor(self):
        """Item 3: the lane lookup accepts EITHER name for one release, so
        an estate whose ledger row is already migrated to "supervisor"
        survives too, not just the pre-migration "architecture" row."""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            for lane in ("supervisor", "worker"):
                ledger.register_lane(
                    lane=lane, pane_id=f"%{lane}", nonce=lane, harness="codex", repo="/repo",
                    server_id="server", session_id="$1", command="codex",
                )
            RecordingAdapter.instances.clear()
            output = io.StringIO()
            with patch.object(cli, "TmuxAdapter", RecordingAdapter), contextlib.redirect_stdout(output):
                self.assertEqual(0, cli.main(["--state-dir", root, "observe"]))
            adapter = RecordingAdapter.instances[-1]
            self.assertEqual(["worker"], adapter.observed, "the supervisor lane must never be observed as a worker")

    def test_architecture_lane_flag_is_a_hidden_alias_for_supervisor_lane(self):
        """Item 2: --architecture-lane must still work and must produce the
        same result as --supervisor-lane, for a caller not yet migrated."""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            for lane in ("director", "worker"):
                ledger.register_lane(
                    lane=lane, pane_id=f"%{lane}", nonce=lane, harness="codex", repo="/repo",
                    server_id="server", session_id="$1", command="codex",
                )
            RecordingAdapter.instances.clear()
            output = io.StringIO()
            with patch.object(cli, "TmuxAdapter", RecordingAdapter), contextlib.redirect_stdout(output):
                self.assertEqual(
                    0,
                    cli.main(["--state-dir", root, "observe", "--architecture-lane", "director"]),
                )
            via_old_flag = RecordingAdapter.instances[-1].observed

            RecordingAdapter.instances.clear()
            output = io.StringIO()
            with patch.object(cli, "TmuxAdapter", RecordingAdapter), contextlib.redirect_stdout(output):
                self.assertEqual(
                    0,
                    cli.main(["--state-dir", root, "observe", "--supervisor-lane", "director"]),
                )
            via_new_flag = RecordingAdapter.instances[-1].observed

            self.assertEqual(["worker"], via_old_flag)
            self.assertEqual(via_new_flag, via_old_flag, "the hidden alias must produce the same result as the new flag")

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

    def test_register_pi_with_pi_rpc_transport_dispatches_through_pi_rpc_adapter(self):
        RecordingPiRPCAdapter.instances.clear()
        with tempfile.TemporaryDirectory() as root:
            output = io.StringIO()
            with patch.object(cli, "PiRPCAdapter", RecordingPiRPCAdapter), contextlib.redirect_stdout(output):
                self.assertEqual(
                    0,
                    cli.main([
                        "--state-dir", root, "register",
                        "--lane", "pi-worker", "--target", "unused",
                        "--harness", "pi", "--transport", "pi-rpc", "--repo", "/repo",
                    ]),
                )
            value = json.loads(output.getvalue())
            self.assertEqual("pi", value["harness"])
            self.assertEqual([("pi-worker", "pi")], RecordingPiRPCAdapter.instances[-1].registered)

    def test_register_pi_without_transport_stays_on_tmux_adapter(self):
        """agent-supervisor#58: `pi` may be driven either way -- omitting
        `--transport` must default to plain send-keys, not silently pick RPC."""
        RecordingPiRPCAdapter.instances.clear()
        RecordingAdapter.instances.clear()
        with tempfile.TemporaryDirectory() as root:
            output = io.StringIO()
            with patch.object(cli, "PiRPCAdapter", RecordingPiRPCAdapter), \
                 patch.object(cli, "TmuxAdapter", RecordingAdapter), \
                 contextlib.redirect_stdout(output):
                cli.main([
                    "--state-dir", root, "register",
                    "--lane", "pi-worker", "--target", "unused",
                    "--harness", "pi", "--repo", "/repo",
                ])
            self.assertEqual([("pi-worker", "pi")], RecordingAdapter.instances[-1].registered)
            for instance in RecordingPiRPCAdapter.instances:
                self.assertEqual([], instance.registered, "the RPC adapter must never be called for a send-keys register")

    def test_assign_to_a_pi_rpc_lane_dispatches_through_pi_rpc_adapter_not_tmux(self):
        """The real point of the wiring (agent-supervisor#58): a lane
        registered with transport=pi-rpc must route through PiRPCTransport
        for dispatch, keyed off the ledger's recorded transport, not the
        harness alone -- a `pi` lane recorded `send-keys` must not."""
        RecordingPiRPCAdapter.instances.clear()
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.register_lane(
                lane="pi-worker", pane_id="sess-1", nonce="nonce-pi", harness="pi",
                repo="/repo", server_id="pi-rpc", session_id="sess-1", command="pi",
                transport="pi-rpc",
            )
            output = io.StringIO()
            with patch.object(cli, "PiRPCAdapter", RecordingPiRPCAdapter), contextlib.redirect_stdout(output):
                self.assertEqual(
                    0,
                    cli.main([
                        "--state-dir", root, "assign",
                        "--lane", "pi-worker", "--task", "t1", "--summary", "Do it",
                    ]),
                )
            adapter = RecordingPiRPCAdapter.instances[-1]
            self.assertEqual([("pi-worker", "t1", "Do it")], adapter.assigned)

    def test_assign_to_a_send_keys_pi_lane_does_not_use_the_pi_rpc_adapter(self):
        RecordingPiRPCAdapter.instances.clear()
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.register_lane(
                lane="pi-worker", pane_id="%1", nonce="nonce-pi", harness="pi",
                repo="/repo", server_id="server", session_id="$1", command="pi",
                transport="send-keys",
            )
            output = io.StringIO()
            with patch.object(cli, "PiRPCAdapter", RecordingPiRPCAdapter), contextlib.redirect_stdout(output):
                # No live tmux underneath -- the assignment itself will fail
                # against the real TmuxTransport, but must fail INSIDE
                # TmuxAdapter, never by reaching the RPC adapter at all.
                with self.assertRaises(Exception):
                    cli.main([
                        "--state-dir", root, "assign",
                        "--lane", "pi-worker", "--task", "t1", "--summary", "Do it",
                    ])
            for instance in RecordingPiRPCAdapter.instances:
                self.assertEqual([], instance.assigned, "the RPC adapter must never be called for a send-keys lane")

    def test_reconstruct_task_writes_an_open_source_task_with_no_marker_required(self):
        """agent-supervisor#160: `reconstruct` (the GithubTaskSource path) is
        gated on a `hill90-supervisor:v1` marker no issue in the estate
        carries; `reconstruct-task` is the generic, unmarked write
        `dispatch-pi-rpc.sh` needs instead."""
        with tempfile.TemporaryDirectory() as root:
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                self.assertEqual(
                    0,
                    cli.main([
                        "--state-dir", root, "reconstruct-task",
                        "--task", "t1",
                        "--source-url", "https://github.com/acme/repo/issues/160",
                        "--source-ref", "160",
                        "--summary", "#160 pi-rpc transport",
                    ]),
                )
            value = json.loads(output.getvalue())
            self.assertEqual("OPEN", value["source_state"])
            self.assertEqual("created", value["status"])
            ledger = Ledger(Path(root))
            self.assertIsNotNone(ledger.get_source_task("t1"))

    def test_reconstruct_task_then_assign_composes_into_a_full_pi_rpc_dispatch(self):
        """The exact sequence dispatch-pi-rpc.sh runs: register, then
        reconstruct-task, then assign -- proven here to compose into one
        real ledger flow, with delivery routed through PiRPCAdapter."""
        RecordingPiRPCAdapter.instances.clear()
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.register_lane(
                lane="pi-worker", pane_id="sess-1", nonce="nonce-pi", harness="pi",
                repo="/repo", server_id="pi-rpc", session_id="sess-1", command="pi",
                transport="pi-rpc",
            )
            output = io.StringIO()
            with patch.object(cli, "PiRPCAdapter", RecordingPiRPCAdapter), contextlib.redirect_stdout(output):
                self.assertEqual(0, cli.main([
                    "--state-dir", root, "reconstruct-task",
                    "--task", "t1",
                    "--source-url", "https://github.com/acme/repo/issues/160",
                    "--source-ref", "160",
                    "--summary", "#160 pi-rpc transport",
                ]))
                self.assertEqual(0, cli.main([
                    "--state-dir", root, "assign",
                    "--lane", "pi-worker", "--task", "t1", "--summary", "Read the brief",
                ]))
            adapter = RecordingPiRPCAdapter.instances[-1]
            self.assertEqual([("pi-worker", "t1", "Read the brief")], adapter.assigned)
            ledger = Ledger(Path(root))
            lane = ledger.get_lane("pi-worker")
            self.assertEqual("pi-rpc", lane["transport"])

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
