import sys
import tempfile
import unittest
import uuid
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from adapter import ACPAdapter, ClaudePrintAdapter, PiRPCAdapter, TmuxAdapter, classify_capture  # noqa: E402
from core import Ledger  # noqa: E402
import sqlite3


class FakeTransport:
    def __init__(self):
        self.panes = {
            "%19": {
                "pane_id": "%19",
                "command": "codex",
                "path": "/repo/hill90",
                "server_id": "server-a",
                "session_id": "$4",
                "capture": "─ Worked for 1m ─\n\n› Continue\n",
                "options": {},
                "after_send": "• Working (1s • esc to interrupt)\n\n› Continue\n",
            },
            "%8": {
                "pane_id": "%8",
                "command": "claude.exe",
                "path": "/repo/hill90",
                "server_id": "server-a",
                "session_id": "$4",
                "capture": "✻ Crunched for 1s\n\n❯ \n────────────────\n",
                "options": {},
                "after_send": "✻ Thinking…\n\n❯ \n────────────────\n",
            },
        }
        self.sends = []

    def metadata(self, target):
        return dict(self.panes[target])

    def capture(self, target, lines=25):
        return self.panes[target]["capture"]

    def set_option(self, target, name, value):
        self.panes[target]["options"][name] = value

    def get_option(self, target, name):
        return self.panes[target]["options"].get(name, "")

    def send_literal(self, target, payload):
        self.sends.append((target, payload))
        self.panes[target]["capture"] = payload + "\n" + self.panes[target]["after_send"]


class AdapterTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.ledger = Ledger(Path(self.tempdir.name), clock=lambda: 1_000)
        self.transport = FakeTransport()
        self.adapter = TmuxAdapter(self.ledger, self.transport, clock=lambda: 1_000)
        self.adapter.register_lane(
            lane="architecture", target="%19", harness="codex", repo="/repo/hill90", nonce="nonce-19"
        )
        self.adapter.register_lane(
            lane="infra-claude", target="%8", harness="claude", repo="/repo/hill90", nonce="nonce-8"
        )
        self._source_number = 899

    def seed_source(self, task_id, summary):
        self._source_number += 1
        self.ledger.reconstruct_task(
            task_id=task_id,
            source_kind="issue",
            source_url=f"https://github.com/jonhill90/Hill90/issues/{self._source_number}",
            source_ref="a" * 40,
            summary=summary,
            source_state="OPEN",
            status="created",
            evidence=[],
            status_marker=None,
        )

    def test_register_lane_records_send_keys_as_its_transport(self):
        """agent-supervisor#58: a send-keys lane is acceptable, an UNLABELLED
        one is not -- `TmuxAdapter` must write its transport down explicitly,
        not rely on a default that could silently change underneath it."""
        record = self.ledger.get_lane("architecture")
        self.assertEqual("send-keys", record["transport"])

    def test_harness_classifiers_cover_idle_active_blocked_and_approval(self):
        self.assertEqual("idle", classify_capture("codex", "─ Worked for 1m ─\n\n› Continue\n"))
        self.assertEqual("active", classify_capture("codex", "• Working (2s • esc to interrupt)\n› Continue\n"))
        self.assertEqual("blocked", classify_capture("codex", "■ You've hit your usage limit.\n› Continue\n"))
        self.assertEqual("idle", classify_capture("claude", "✻ Crunched for 1s\n\n❯ \n────────\n"))
        self.assertEqual("active", classify_capture("claude", "✻ Thinking…\n\n❯ \n────────\n"))
        self.assertEqual("blocked", classify_capture("claude", "You've hit your weekly limit · resets tomorrow\n❯ \n"))
        self.assertEqual("approval", classify_capture("claude", "Allow this command? [Y/n]\n❯ \n"))

    def test_assignment_is_task_bound_and_accepts_real_codex_and_claude_activity(self):
        self.seed_source("codex-task", "Review one artifact")
        self.seed_source("claude-task", "Inspect one issue")
        codex = self.adapter.assign_task(
            lane="architecture", task_id="codex-task", summary="Review one artifact"
        )
        claude = self.adapter.assign_task(
            lane="infra-claude", task_id="claude-task", summary="Inspect one issue"
        )
        self.assertEqual("delivered", codex["status"])
        self.assertEqual("delivered", claude["status"])
        self.assertIn("codex-task", self.transport.sends[0][1])
        self.assertIn("claude-task", self.transport.sends[1][1])
        self.assertIn("complete", self.transport.sends[0][1])

    def test_blocked_lane_gets_no_assignment_input(self):
        self.transport.panes["%8"]["capture"] = "You've hit your weekly limit\n❯ \n"
        with self.assertRaisesRegex(RuntimeError, "blocked"):
            self.adapter.assign_task(lane="infra-claude", task_id="blocked-task", summary="Do not send")
        self.assertEqual([], self.transport.sends)
        self.assertIsNone(self.ledger.get_task("blocked-task"))

    def test_idle_outstanding_task_emits_attention_without_observed_transition(self):
        self.seed_source("short-task", "Short review")
        self.adapter.assign_task(lane="architecture", task_id="short-task", summary="Short review")
        self.transport.panes["%19"]["capture"] = "Review finished\n─ Worked for 2m ─\n\n› Continue\n"
        event = self.adapter.observe_lane("architecture")
        self.assertEqual("attention:short-task", event["key"])
        repeated = self.adapter.observe_lane("architecture")
        self.assertEqual(event["key"], repeated["key"])

    def test_blocked_after_echo_does_not_ack_architecture_notification(self):
        self.seed_source("review-task", "Review")
        self.adapter.assign_task(lane="infra-claude", task_id="review-task", summary="Review")
        self.ledger.complete("review-task", b"# Result\n\nNo findings.\n", pane_nonce="nonce-8")
        self.transport.panes["%19"]["after_send"] = "■ You have hit your usage limit.\n\n› Continue\n"
        notified = self.adapter.notify_supervisor(lane="architecture", retry_after=900)
        self.assertFalse(notified)
        event = self.ledger.get_event("completion:review-task")
        self.assertEqual("pending", event["status"])

    def test_reused_pane_id_with_wrong_nonce_is_rejected_before_send(self):
        self.transport.panes["%19"]["options"]["@hill90_lane_nonce"] = "reused"
        with self.assertRaisesRegex(RuntimeError, "incarnation"):
            self.adapter.assign_task(lane="architecture", task_id="wrong-pane", summary="Must not send")
        self.assertEqual([], self.transport.sends)

    def test_ambiguous_send_persists_delivery_pending_and_blocks_automatic_resend(self):
        def failing_send(target, payload):
            self.transport.sends.append((target, payload))
            raise RuntimeError("tmux send-keys timed out")

        self.seed_source("flaky-task", "Ambiguous delivery")
        self.transport.send_literal = failing_send
        with self.assertRaisesRegex(RuntimeError, "timed out"):
            self.adapter.assign_task(lane="architecture", task_id="flaky-task", summary="Ambiguous delivery")

        task = self.ledger.get_task("flaky-task")
        self.assertEqual("delivery_pending", task["status"])
        self.assertEqual(1, len(self.transport.sends))

        # A never-attempted task simply has no ledger row at all.
        self.assertIsNone(self.ledger.get_task("never-attempted"))

        # The same task id cannot be silently resent while unconfirmed.
        with self.assertRaisesRegex(RuntimeError, "reconcile"):
            self.adapter.assign_task(lane="architecture", task_id="flaky-task", summary="Ambiguous delivery")
        self.assertEqual(1, len(self.transport.sends))

        # A human, not echoed pane text, resolves the ambiguity.
        reconciled = self.ledger.reconcile_delivery("flaky-task", pane_nonce="nonce-19", outcome="failed")
        self.assertEqual("failed", reconciled["status"])

    def test_successful_send_does_not_infer_delivery_from_echoed_prompt_text(self):
        # Even though the pane echoes the sent prompt back into its own capture,
        # assign_task must not use that capture to decide the task was delivered.
        self.seed_source("quiet-task", "No echo needed")
        self.transport.panes["%19"]["after_send"] = "flaky terminal chrome, no active/idle marker\n"
        task = self.adapter.assign_task(lane="architecture", task_id="quiet-task", summary="No echo needed")
        self.assertEqual("delivered", task["status"])

    def test_one_hundred_unchanged_observations_send_nothing(self):
        for _ in range(100):
            self.assertIsNone(self.adapter.observe_lane("architecture"))
            self.assertFalse(self.adapter.notify_supervisor(lane="architecture", retry_after=900))
        self.assertEqual([], self.transport.sends)

    def test_blocked_approval_and_unknown_outstanding_tasks_emit_durable_attention(self):
        """Red: observe_lane only calls ledger.observe_idle when state == "idle";
        blocked/approval/unknown states hit `return None` and produce no
        durable event at all -- a restart between the pane going blocked and a
        human noticing loses that signal entirely."""
        self.seed_source("needs-help", "Review")
        self.adapter.assign_task(lane="architecture", task_id="needs-help", summary="Review")
        for capture_text, reason in (
            ("■ You've hit your usage limit.\n› Continue\n", "blocked"),
            ("Allow this command? [Y/n]\n› Continue\n", "approval"),
            ("unexpected terminal chrome with no recognizable marker\n", "unknown"),
        ):
            with self.subTest(reason=reason):
                self.transport.panes["%19"]["capture"] = capture_text
                event = self.adapter.observe_lane("architecture")
                self.assertIsNotNone(event, f"no durable event for {reason}")
                self.assertEqual(f"attention:needs-help:{reason}", event["key"])


class FakeACPTransport:
    """Stands in for a freshly `ACPTransport.spawn()`-ed process per call --
    the CLI is one process per command, so there is no live subprocess to
    reuse between `assign_task` invocations (see `ACPTransport.load_session`
    docstring)."""

    instances = []

    def __init__(self, sessions=None, stop_reason="end_turn", message="Done."):
        self.sessions = sessions if sessions is not None else {}
        self.stop_reason = stop_reason
        self.message = message
        self.initialized = False
        self.loaded_sessions = []
        self.prompts = []
        self.closed = False
        self.terminated = False
        self.__class__.instances.append(self)

    def initialize(self, **kwargs):
        self.initialized = True
        return {}

    def new_session(self, cwd, **kwargs):
        session_id = f"sess-{len(self.sessions) + 1}"
        self.sessions[session_id] = cwd
        return session_id

    def load_session(self, session_id, *, cwd):
        self.loaded_sessions.append((session_id, cwd))
        return session_id

    def send_literal(self, target, payload):
        self.prompts.append((target, payload))
        return {
            "stop_reason": self.stop_reason,
            "message": self.message,
            "token_usage": {"input_tokens": 10, "output_tokens": 5},
            "context_window": None,
        }

    def close(self):
        self.closed = True

    def terminate(self):
        self.terminated = True
        self.close()


class ACPAdapterTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.ledger = Ledger(Path(self.tempdir.name), clock=lambda: 1_000)
        FakeACPTransport.instances = []
        self.shared_sessions = {}
        self.adapter = ACPAdapter(
            self.ledger,
            lambda: FakeACPTransport(sessions=self.shared_sessions),
            clock=lambda: 1_000,
        )
        self._source_number = 899

    def seed_source(self, task_id, summary):
        self._source_number += 1
        self.ledger.reconstruct_task(
            task_id=task_id,
            source_kind="issue",
            source_url=f"https://github.com/jonhill90/Hill90/issues/{self._source_number}",
            source_ref="a" * 40,
            summary=summary,
            source_state="OPEN",
            status="created",
            evidence=[],
            status_marker=None,
        )

    def test_register_lane_opens_an_acp_session_and_stores_it_as_the_lane_identity(self):
        record = self.adapter.register_lane(
            lane="copilot-worker", target=None, harness="copilot-acp", repo="/repo/hill90", nonce="nonce-acp"
        )
        self.assertEqual("copilot-acp", record["harness"])
        self.assertEqual(record["pane_id"], record["session_id"])
        # agent-supervisor#58: recorded explicitly, not left to a default.
        self.assertEqual("acp", record["transport"])
        self.assertTrue(FakeACPTransport.instances[0].initialized)
        self.assertTrue(FakeACPTransport.instances[0].closed)

    def test_assign_task_resumes_the_session_and_completes_synchronously_from_the_stop_reason(self):
        self.adapter.register_lane(
            lane="copilot-worker", target=None, harness="copilot-acp", repo="/repo/hill90", nonce="nonce-acp"
        )
        self.seed_source("acp-task", "Review one artifact")
        task = self.adapter.assign_task(lane="copilot-worker", task_id="acp-task", summary="Review one artifact")
        self.assertEqual("complete", task["status"])

        # A fresh transport was spawned for this call and closed afterward --
        # no subprocess lingers between CLI invocations.
        assign_transport = FakeACPTransport.instances[-1]
        self.assertTrue(assign_transport.closed)
        self.assertEqual(1, len(assign_transport.loaded_sessions))
        self.assertIn("acp-task", assign_transport.prompts[0][1])

    def test_assign_task_to_unregistered_lane_raises(self):
        with self.assertRaisesRegex(RuntimeError, "unknown lane"):
            self.adapter.assign_task(lane="missing", task_id="t1", summary="x")

    def test_observe_lane_is_a_no_op_because_prompts_are_synchronous(self):
        self.adapter.register_lane(
            lane="copilot-worker", target=None, harness="copilot-acp", repo="/repo/hill90", nonce="nonce-acp"
        )
        self.assertIsNone(self.adapter.observe_lane("copilot-worker"))


class FakePiRPCTransport:
    """Stands in for a freshly `PiRPCTransport.spawn()`-ed process per call --
    same one-process-per-CLI-invocation shape as `FakeACPTransport`, adapted
    to pi RPC's `get_state`/`send_literal` surface."""

    instances = []

    def __init__(self, cwd=None, session=None, sessions=None, stop_reason="end_turn", message="Done."):
        self.cwd = cwd
        self.session = session
        self.sessions = sessions if sessions is not None else {}
        self.stop_reason = stop_reason
        self.message = message
        self.prompts = []
        self.closed = False
        self.terminated = False
        if session is None:
            session_id = f"pi-sess-{len(self.sessions) + 1}"
            self.sessions[session_id] = cwd
            self._state = {"sessionId": session_id, "sessionFile": f"/tmp/{session_id}.json"}
        else:
            self._state = {"sessionId": session, "sessionFile": f"/tmp/{session}.json"}
        self.__class__.instances.append(self)

    def get_state(self):
        return dict(self._state)

    def send_literal(self, target, payload):
        self.prompts.append((target, payload))
        return {
            "stop_reason": self.stop_reason,
            "message": self.message,
            "token_usage": {"input_tokens": 10, "output_tokens": 5},
        }

    def close(self):
        self.closed = True

    def terminate(self):
        self.terminated = True
        self.close()


class PiRPCAdapterTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.ledger = Ledger(Path(self.tempdir.name), clock=lambda: 1_000)
        FakePiRPCTransport.instances = []
        self.shared_sessions = {}
        self.adapter = PiRPCAdapter(
            self.ledger,
            lambda **kwargs: FakePiRPCTransport(sessions=self.shared_sessions, **kwargs),
            clock=lambda: 1_000,
        )
        self._source_number = 899

    def seed_source(self, task_id, summary):
        self._source_number += 1
        self.ledger.reconstruct_task(
            task_id=task_id,
            source_kind="issue",
            source_url=f"https://github.com/jonhill90/Hill90/issues/{self._source_number}",
            source_ref="a" * 40,
            summary=summary,
            source_state="OPEN",
            status="created",
            evidence=[],
            status_marker=None,
        )

    def test_register_lane_opens_a_pi_rpc_session_and_stores_it_as_the_lane_identity(self):
        record = self.adapter.register_lane(
            lane="pi-worker", target=None, harness="pi", repo="/repo/hill90", nonce="nonce-pi"
        )
        self.assertEqual("pi", record["harness"])
        self.assertEqual(record["pane_id"], record["session_id"])
        # agent-supervisor#58: the whole point -- this lane's transport must
        # read back from the ledger, not be inferred from its harness alone.
        self.assertEqual("pi-rpc", record["transport"])
        self.assertTrue(FakePiRPCTransport.instances[0].terminated)

    def test_register_lane_rejects_a_non_pi_harness(self):
        with self.assertRaisesRegex(RuntimeError, "only supports pi lanes"):
            self.adapter.register_lane(
                lane="codex-worker", target=None, harness="codex", repo="/repo/hill90", nonce="nonce-x"
            )

    def test_assign_task_resumes_the_session_and_completes_synchronously_from_the_stop_reason(self):
        self.adapter.register_lane(
            lane="pi-worker", target=None, harness="pi", repo="/repo/hill90", nonce="nonce-pi"
        )
        self.seed_source("pi-task", "Review one artifact")
        task = self.adapter.assign_task(lane="pi-worker", task_id="pi-task", summary="Review one artifact")
        self.assertEqual("complete", task["status"])

        # A fresh transport was spawned for this call and terminated
        # afterward -- no subprocess lingers between CLI invocations.
        assign_transport = FakePiRPCTransport.instances[-1]
        self.assertTrue(assign_transport.terminated)
        self.assertIsNotNone(assign_transport.session)
        self.assertIn("pi-task", assign_transport.prompts[0][1])

    def test_assign_task_to_unregistered_lane_raises(self):
        with self.assertRaisesRegex(RuntimeError, "unknown lane"):
            self.adapter.assign_task(lane="missing", task_id="t1", summary="x")

    def test_assign_task_does_not_mark_delivered_when_the_transport_reports_a_dropped_stream(self):
        """agent-supervisor#61: `send_literal` on a stream that closed before
        `agent_settled` must raise (`pi_transport.PiRPCConnectionClosedError`
        in production), not return a success-shaped result. This checks the
        adapter's end of that contract -- a raising transport must leave the
        task `delivery_pending`, not `complete`, and must still terminate the
        transport it spawned."""

        class DroppedStreamTransport(FakePiRPCTransport):
            def send_literal(self, target, payload):
                self.prompts.append((target, payload))
                raise RuntimeError("pi RPC connection closed before the prompt settled")

        self.adapter = PiRPCAdapter(
            self.ledger,
            lambda **kwargs: DroppedStreamTransport(sessions=self.shared_sessions, **kwargs),
            clock=lambda: 1_000,
        )
        self.adapter.register_lane(
            lane="pi-worker", target=None, harness="pi", repo="/repo/hill90", nonce="nonce-pi"
        )
        self.seed_source("pi-task", "Review one artifact")

        with self.assertRaisesRegex(RuntimeError, "connection closed"):
            self.adapter.assign_task(lane="pi-worker", task_id="pi-task", summary="Review one artifact")

        task = self.ledger.get_task("pi-task")
        self.assertEqual("delivery_pending", task["status"])
        assign_transport = FakePiRPCTransport.instances[-1]
        self.assertTrue(assign_transport.terminated)

    def test_assign_task_refuses_a_lane_registered_as_send_keys(self):
        """agent-supervisor#58: `pi` is the one harness allowed either
        transport -- this adapter must refuse a lane recorded `send-keys`
        rather than silently drive it over RPC anyway."""
        self.ledger.register_lane(
            lane="pi-worker",
            pane_id="%1",
            nonce="nonce-pi",
            harness="pi",
            repo="/repo/hill90",
            server_id="server-a",
            session_id="$1",
            command="pi",
            transport="send-keys",
        )
        with self.assertRaisesRegex(RuntimeError, "not a pi-rpc lane"):
            self.adapter.assign_task(lane="pi-worker", task_id="t1", summary="x")

    def test_observe_lane_is_a_no_op_because_prompts_are_synchronous(self):
        self.adapter.register_lane(
            lane="pi-worker", target=None, harness="pi", repo="/repo/hill90", nonce="nonce-pi"
        )
        self.assertIsNone(self.adapter.observe_lane("pi-worker"))


class FakeClaudePrintTransport:
    """Stands in for a freshly-spawned `claude -p` subprocess per call --
    same one-process-per-CLI-invocation shape as `FakeACPTransport`/
    `FakePiRPCTransport`, adapted to `claude -p`'s
    `start_session`/`run` surface."""

    instances = []

    def __init__(self, cwd=None, session_id=None, sessions=None, result="Done.", subtype="success", is_error=False):
        self.cwd = cwd
        self.session_id = session_id
        self.sessions = sessions if sessions is not None else {}
        self.result_text = result
        self.subtype = subtype
        self.is_error = is_error
        self.prompts = []
        self.terminated = False
        self.__class__.instances.append(self)

    def start_session(self, session_id, prompt):
        self.session_id = session_id
        self.sessions[session_id] = self.cwd
        self.prompts.append((session_id, prompt))
        return {
            "type": "result",
            "session_id": session_id,
            "result": self.result_text,
            "subtype": self.subtype,
            "is_error": self.is_error,
        }

    def run(self, prompt):
        self.prompts.append((self.session_id, prompt))
        return {
            "type": "result",
            "session_id": self.session_id,
            "result": self.result_text,
            "subtype": self.subtype,
            "is_error": self.is_error,
        }

    def terminate(self):
        self.terminated = True

    def close(self):
        self.terminate()


class ClaudePrintAdapterTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.ledger = Ledger(Path(self.tempdir.name), clock=lambda: 1_000)
        FakeClaudePrintTransport.instances = []
        self.shared_sessions = {}
        self.adapter = ClaudePrintAdapter(
            self.ledger,
            lambda **kwargs: FakeClaudePrintTransport(sessions=self.shared_sessions, **kwargs),
            clock=lambda: 1_000,
        )
        self._source_number = 899

    def seed_source(self, task_id, summary):
        self._source_number += 1
        self.ledger.reconstruct_task(
            task_id=task_id,
            source_kind="issue",
            source_url=f"https://github.com/jonhill90/Hill90/issues/{self._source_number}",
            source_ref="a" * 40,
            summary=summary,
            source_state="OPEN",
            status="created",
            evidence=[],
            status_marker=None,
        )

    def test_register_lane_opens_a_claude_print_session_and_stores_it_as_the_lane_identity(self):
        record = self.adapter.register_lane(
            lane="claude-print-worker", target=None, harness="claude", repo="/repo/hill90", nonce="nonce-cp"
        )
        self.assertEqual("claude", record["harness"])
        self.assertEqual(record["pane_id"], record["session_id"])
        # agent-supervisor#171: recorded explicitly, not left to a default --
        # the same posture #58 established for `pi-rpc`.
        self.assertEqual("claude-print", record["transport"])
        self.assertTrue(FakeClaudePrintTransport.instances[0].terminated)

    def test_register_lane_mints_a_real_uuid_session_id(self):
        """Reproduction: `uuid.uuid4().hex` (no dashes) minted a session id
        `claude --session-id` rejects outright ('Invalid session ID. Must be
        a valid UUID.'), caught live dispatching agent-supervisor#232 through
        this transport before this fix -- `uuid.UUID(...)` here raises on
        exactly that shape, the same way `claude` itself does."""
        record = self.adapter.register_lane(
            lane="claude-print-worker", target=None, harness="claude", repo="/repo/hill90", nonce="nonce-cp"
        )
        uuid.UUID(record["session_id"])
        self.assertIn("-", record["session_id"])

    def test_register_lane_rejects_a_non_claude_harness(self):
        with self.assertRaisesRegex(RuntimeError, "only supports claude lanes"):
            self.adapter.register_lane(
                lane="codex-worker", target=None, harness="codex", repo="/repo/hill90", nonce="nonce-x"
            )

    def test_register_lane_rejects_an_error_handshake(self):
        self.adapter = ClaudePrintAdapter(
            self.ledger,
            lambda **kwargs: FakeClaudePrintTransport(sessions=self.shared_sessions, is_error=True, **kwargs),
            clock=lambda: 1_000,
        )
        with self.assertRaisesRegex(RuntimeError, "handshake reported an error"):
            self.adapter.register_lane(
                lane="claude-print-worker", target=None, harness="claude", repo="/repo/hill90", nonce="nonce-cp"
            )

    def test_assign_task_resumes_the_session_and_completes_synchronously_from_the_result(self):
        self.adapter.register_lane(
            lane="claude-print-worker", target=None, harness="claude", repo="/repo/hill90", nonce="nonce-cp"
        )
        self.seed_source("cp-task", "Review one artifact")
        task = self.adapter.assign_task(lane="claude-print-worker", task_id="cp-task", summary="Review one artifact")
        self.assertEqual("complete", task["status"])

        # A fresh transport was spawned for this call and terminated
        # afterward -- no subprocess lingers between CLI invocations.
        assign_transport = FakeClaudePrintTransport.instances[-1]
        self.assertTrue(assign_transport.terminated)
        self.assertIsNotNone(assign_transport.session_id)
        self.assertIn("cp-task", assign_transport.prompts[0][1])

    def test_assign_task_to_unregistered_lane_raises(self):
        with self.assertRaisesRegex(RuntimeError, "unknown lane"):
            self.adapter.assign_task(lane="missing", task_id="t1", summary="x")

    def test_assign_task_does_not_mark_delivered_when_the_transport_raises(self):
        """A `claude -p` call that times out or exits without a well-formed
        result (`ClaudePrintTimeoutError`/`ClaudePrintProtocolError` in
        production) must leave the task `delivery_pending`, not `complete`,
        and must still terminate the transport it spawned -- same contract
        `PiRPCAdapter`'s dropped-stream test checks for pi RPC."""

        class RaisingTransport(FakeClaudePrintTransport):
            def run(self, prompt):
                self.prompts.append((self.session_id, prompt))
                raise RuntimeError("claude -p exited without a well-formed result")

        self.adapter = ClaudePrintAdapter(
            self.ledger,
            lambda **kwargs: RaisingTransport(sessions=self.shared_sessions, **kwargs),
            clock=lambda: 1_000,
        )
        self.adapter.register_lane(
            lane="claude-print-worker", target=None, harness="claude", repo="/repo/hill90", nonce="nonce-cp"
        )
        self.seed_source("cp-task", "Review one artifact")

        with self.assertRaisesRegex(RuntimeError, "well-formed result"):
            self.adapter.assign_task(lane="claude-print-worker", task_id="cp-task", summary="Review one artifact")

        task = self.ledger.get_task("cp-task")
        self.assertEqual("delivery_pending", task["status"])
        assign_transport = FakeClaudePrintTransport.instances[-1]
        self.assertTrue(assign_transport.terminated)

    def test_assign_task_refuses_a_lane_registered_as_send_keys(self):
        """agent-supervisor#171: `claude` is the one harness allowed either
        transport -- this adapter must refuse a lane recorded `send-keys`
        rather than silently drive it over `claude -p` anyway."""
        self.ledger.register_lane(
            lane="claude-print-worker",
            pane_id="%1",
            nonce="nonce-cp",
            harness="claude",
            repo="/repo/hill90",
            server_id="server-a",
            session_id="$1",
            command="claude",
            transport="send-keys",
        )
        with self.assertRaisesRegex(RuntimeError, "not a claude-print lane"):
            self.adapter.assign_task(lane="claude-print-worker", task_id="t1", summary="x")

    def test_observe_lane_is_a_no_op_because_prompts_are_synchronous(self):
        self.adapter.register_lane(
            lane="claude-print-worker", target=None, harness="claude", repo="/repo/hill90", nonce="nonce-cp"
        )
        self.assertIsNone(self.adapter.observe_lane("claude-print-worker"))


if __name__ == "__main__":
    unittest.main()


class ClaudePrintRecoverabilityTest(unittest.TestCase):
    """A claude-print lane must be RECOVERABLE, not merely registered.

    Regression for the defect found 2026-08-17: `ClaudePrintAdapter.register_lane`
    minted a session uuid, handed it to `claude -p --session-id`, and passed it to
    the ledger as `session_id` -- but NOT as `harness_session_id`. Measured on the
    live ledger at the time: 33 claude-print lanes, 0 with a usable
    harness_session_id, against 20 of 20 for send-keys.

    That matters because `restore.sh` refuses rather than invents (AGENTS.md
    invariant 3): with no harness session id it reports UNRECOVERABLE and leaves
    the lane, even though the conversation is sitting on disk and `claude -p
    --resume <uuid>` would bring it straight back. The estate has died seven
    times. The old send-keys transport stranded prompts but SURVIVED a crash;
    the new one delivered reliably and lost everything, which is the worse trade.

    A second, narrower defect survived the first fix pass: `harness_session_id`
    was written but its companion `harness_project_dir` was not, and
    `restore.sh` (~155-175) checks that column INDEPENDENTLY of
    `harness_session_id` -- an empty `harness_project_dir` refuses regardless of
    whether a usable session id is sitting right next to it. Both columns are
    asserted below for that reason.
    """

    def test_register_lane_records_a_resumable_harness_session_id(self):
        root = Path(tempfile.mkdtemp())
        ledger = Ledger(root, clock=lambda: 2_000)

        class _Transport:
            def __init__(self, *a, **k):
                pass

            def start_session(self, session_id, prompt):
                return {"is_error": False, "session_id": session_id, "result": "ready."}

            def terminate(self):
                pass

        adapter = ClaudePrintAdapter(ledger=ledger, transport_factory=lambda **k: _Transport())
        adapter.register_lane(
            lane="t:1", target="claude-print:t:1", harness="claude",
            repo=str(root), nonce="n1",
        )

        db = next(root.rglob("*.sqlite3"))
        row = sqlite3.connect(db).execute(
            "select transport, session_id, harness_session_id, harness_project_dir from lanes where lane='t:1'"
        ).fetchone()

        self.assertEqual(row[0], "claude-print")
        self.assertTrue(
            row[2],
            "harness_session_id is empty -- restore.sh will report this lane "
            "UNRECOVERABLE even though `claude -p --resume` could return it",
        )
        self.assertTrue(
            row[3],
            "harness_project_dir is empty -- restore.sh's independent check on "
            "this column (~155-175) will refuse this lane even though "
            "harness_session_id above is populated",
        )
        # `repo` is the directory `transport_factory(cwd=repo)` actually
        # launched `claude -p` in -- the same notion dispatch.sh resolves as
        # `HARNESS_PROJECT_DIR` and records alongside `harness_session_id` in
        # one call, never independently (core.py ~1284-1294).
        self.assertEqual(row[3], str(root))
        # For claude-print the two ids are the same uuid by construction: it is
        # what --session-id minted and what --resume takes.
        self.assertEqual(row[1], row[2])
