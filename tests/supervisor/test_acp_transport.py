"""Tests drive ACPTransport against a fake JSON-RPC peer -- no live `copilot --acp`
process is ever spawned. Two os.pipe() pairs stand in for stdio: the transport
under test gets one end of each, a background peer thread gets the other and
reads/writes the exact frames a real ACP agent would (or a scripted deviation,
per test)."""

import json
import os
import sys
import threading
import time
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from acp_transport import ACPProtocolError, ACPTimeoutError, ACPTransport, PermissionPolicy  # noqa: E402


class FakePeer:
    """The "agent" side of the wire: reads lines the transport sends, and lets
    the test script exactly what frames go back, in order, on its own thread.
    """

    def __init__(self):
        transport_read_fd, self._peer_write_fd = os.pipe()
        self._peer_read_fd, transport_write_fd = os.pipe()
        self.transport_reader = os.fdopen(transport_read_fd, "r", buffering=1)
        self.transport_writer = os.fdopen(transport_write_fd, "w", buffering=1)
        self._peer_reader = os.fdopen(self._peer_read_fd, "r", buffering=1)
        self._peer_writer = os.fdopen(self._peer_write_fd, "w", buffering=1)
        self.received = []
        self._handler = None
        self._thread = threading.Thread(target=self._loop, daemon=True)
        self._thread.start()

    def on_request(self, handler):
        """handler(obj) -> None; call self.send(...) from within it to reply."""
        self._handler = handler

    def send(self, obj):
        line = json.dumps(obj)
        self._peer_writer.write(line + "\n")
        self._peer_writer.flush()

    def _loop(self):
        for line in self._peer_reader:
            line = line.strip()
            if not line:
                continue
            obj = json.loads(line)
            self.received.append(obj)
            if self._handler is not None:
                self._handler(obj)

    def close(self):
        try:
            self._peer_writer.close()
        except Exception:
            pass


def _respond(peer, request_id, result):
    peer.send({"jsonrpc": "2.0", "id": request_id, "result": result})


class ACPTransportInitializeTest(unittest.TestCase):
    def test_initialize_returns_agent_info_and_populates_metadata(self):
        peer = FakePeer()
        peer.on_request(lambda obj: _respond(peer, obj["id"], {
            "protocolVersion": 1,
            "agentCapabilities": {},
            "agentInfo": {"name": "Copilot", "version": "1.0.79"},
        }))
        transport = ACPTransport(peer.transport_reader, peer.transport_writer)
        try:
            result = transport.initialize()
            self.assertEqual(1, result["protocolVersion"])
            metadata = transport.metadata()
            self.assertEqual("Copilot", metadata["agent_name"])
            self.assertEqual("1.0.79", metadata["agent_version"])
            self.assertEqual(1, metadata["protocol_version"])
        finally:
            transport.close()
            peer.close()

    def test_initialize_sends_well_formed_request_frame(self):
        peer = FakePeer()
        peer.on_request(lambda obj: _respond(peer, obj["id"], {"protocolVersion": 1}))
        transport = ACPTransport(peer.transport_reader, peer.transport_writer)
        try:
            transport.initialize()
            time.sleep(0.05)
            self.assertEqual(1, len(peer.received))
            frame = peer.received[0]
            self.assertEqual("2.0", frame["jsonrpc"])
            self.assertEqual("initialize", frame["method"])
            self.assertEqual(1, frame["params"]["protocolVersion"])
        finally:
            transport.close()
            peer.close()


class ACPTransportSessionTest(unittest.TestCase):
    def _new_session_transport(self, peer, session_id="sess-1"):
        def handler(obj):
            if obj["method"] == "session/new":
                _respond(peer, obj["id"], {"sessionId": session_id})

        peer.on_request(handler)
        transport = ACPTransport(peer.transport_reader, peer.transport_writer)
        returned_id = transport.new_session("/tmp/example")
        return transport, returned_id

    def test_new_session_returns_session_id_from_result(self):
        peer = FakePeer()
        try:
            transport, session_id = self._new_session_transport(peer)
            self.assertEqual("sess-1", session_id)
        finally:
            transport.close()
            peer.close()

    def test_load_session_sends_the_stored_session_id_and_registers_it_as_known(self):
        """The CLI is a fresh process per invocation (SPEC §15 boundary): a
        lane's `copilot --acp` subprocess does not survive between `assign`
        calls, so a later call must resume the session it registered by id
        via `loadSession` rather than starting a new one -- `agentCapabilities
        including loadSession` is the proven fact this depends on."""
        peer = FakePeer()

        def handler(obj):
            if obj["method"] == "session/load":
                _respond(peer, obj["id"], {})

        peer.on_request(handler)
        transport = ACPTransport(peer.transport_reader, peer.transport_writer)
        try:
            transport.load_session("sess-1", cwd="/tmp/example")
            self.assertEqual("session/load", peer.received[0]["method"])
            self.assertEqual(
                {"sessionId": "sess-1", "cwd": "/tmp/example"}, peer.received[0]["params"]
            )
            self.assertTrue(transport.metadata("sess-1")["session_known"])
        finally:
            transport.close()
            peer.close()


class ACPTransportPromptTest(unittest.TestCase):
    def test_send_literal_returns_structured_stop_reason_and_token_usage(self):
        """SPEC §14 L4: tmux wait-for carries no payload. ACP's session/prompt
        response is the structured completion this transport must surface."""
        peer = FakePeer()

        def handler(obj):
            if obj["method"] == "session/prompt":
                peer.send({
                    "jsonrpc": "2.0",
                    "method": "session/update",
                    "params": {
                        "sessionId": obj["params"]["sessionId"],
                        "update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "hello"}},
                    },
                })
                _respond(peer, obj["id"], {
                    "stopReason": "end_turn",
                    "usage": {
                        "inputTokens": 26386,
                        "outputTokens": 4,
                        "totalTokens": 26390,
                        "thoughtTokens": 0,
                        "cachedReadTokens": 0,
                        "cachedWriteTokens": 26384,
                    },
                })

        peer.on_request(handler)
        transport = ACPTransport(peer.transport_reader, peer.transport_writer)
        try:
            result = transport.send_literal("sess-1", "Say hello.")
            self.assertEqual("end_turn", result["stop_reason"])
            self.assertEqual("hello", result["message"])
            self.assertEqual(26386, result["token_usage"]["input_tokens"])
            self.assertEqual(4, result["token_usage"]["output_tokens"])
            self.assertEqual(0, result["token_usage"]["cached_read_tokens"])
            self.assertEqual(26384, result["token_usage"]["cached_write_tokens"])
        finally:
            transport.close()
            peer.close()

    def test_send_literal_prompt_frame_carries_the_payload_as_text(self):
        peer = FakePeer()

        def handler(obj):
            if obj["method"] == "session/prompt":
                _respond(peer, obj["id"], {"stopReason": "end_turn", "usage": {}})

        peer.on_request(handler)
        transport = ACPTransport(peer.transport_reader, peer.transport_writer)
        try:
            transport.send_literal("sess-1", "do the thing")
            time.sleep(0.05)
            frame = peer.received[-1]
            self.assertEqual("sess-1", frame["params"]["sessionId"])
            self.assertEqual("do the thing", frame["params"]["prompt"][0]["text"])
        finally:
            transport.close()
            peer.close()

    def test_send_literal_clears_prior_message_chunks_between_calls(self):
        peer = FakePeer()
        replies = iter(["first", "second"])

        def handler(obj):
            if obj["method"] == "session/prompt":
                text = next(replies)
                peer.send({
                    "jsonrpc": "2.0",
                    "method": "session/update",
                    "params": {
                        "sessionId": obj["params"]["sessionId"],
                        "update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": text}},
                    },
                })
                _respond(peer, obj["id"], {"stopReason": "end_turn", "usage": {}})

        peer.on_request(handler)
        transport = ACPTransport(peer.transport_reader, peer.transport_writer)
        try:
            first = transport.send_literal("sess-1", "one")
            second = transport.send_literal("sess-1", "two")
            self.assertEqual("first", first["message"])
            self.assertEqual("second", second["message"])
        finally:
            transport.close()
            peer.close()

    def test_send_literal_times_out_when_no_response_arrives(self):
        peer = FakePeer()
        peer.on_request(lambda obj: None)  # never responds
        transport = ACPTransport(peer.transport_reader, peer.transport_writer, request_timeout=0.2)
        try:
            with self.assertRaises(ACPTimeoutError):
                transport.send_literal("sess-1", "hello?")
        finally:
            transport.close()
            peer.close()

    def test_send_literal_raises_on_jsonrpc_error(self):
        peer = FakePeer()

        def handler(obj):
            if obj["method"] == "session/prompt":
                peer.send({"jsonrpc": "2.0", "id": obj["id"], "error": {"code": -32000, "message": "boom"}})

        peer.on_request(handler)
        transport = ACPTransport(peer.transport_reader, peer.transport_writer)
        try:
            with self.assertRaises(ACPProtocolError):
                transport.send_literal("sess-1", "hello")
        finally:
            transport.close()
            peer.close()


class PermissionPolicyTest(unittest.TestCase):
    OPTIONS = [
        {"optionId": "allow_once", "kind": "allow_once", "name": "Allow once"},
        {"optionId": "allow_always", "kind": "allow_always", "name": "Always allow"},
        {"optionId": "reject_once", "kind": "reject_once", "name": "Deny"},
    ]

    def test_default_policy_denies_unauthorised_tool_calls(self):
        policy = PermissionPolicy()
        option_id, allowed = policy.decide({"title": "Run rm -rf /"}, self.OPTIONS)
        self.assertFalse(allowed)
        self.assertEqual("reject_once", option_id)

    def test_policy_allows_only_explicitly_authorised_titles(self):
        policy = PermissionPolicy(authorized_titles={"Run tests"})
        option_id, allowed = policy.decide({"title": "Run tests"}, self.OPTIONS)
        self.assertTrue(allowed)
        self.assertEqual("allow_once", option_id)

        option_id, allowed = policy.decide({"title": "Delete database"}, self.OPTIONS)
        self.assertFalse(allowed)
        self.assertEqual("reject_once", option_id)

    def test_policy_with_no_reject_option_returns_none_and_cancels(self):
        policy = PermissionPolicy()
        option_id, allowed = policy.decide({"title": "anything"}, [{"optionId": "x", "kind": "allow_once"}])
        self.assertFalse(allowed)
        self.assertIsNone(option_id)


class ACPTransportPermissionHandlingTest(unittest.TestCase):
    def test_incoming_permission_request_is_answered_and_logged_with_default_deny(self):
        peer = FakePeer()
        replies = []
        obj_id_holder = {}

        def real_dispatch(obj):
            if obj.get("method") == "session/prompt":
                obj_id_holder["id"] = obj["id"]
                request = {
                    "jsonrpc": "2.0",
                    "id": "perm-1",
                    "method": "session/request_permission",
                    "params": {
                        "sessionId": obj["params"]["sessionId"],
                        "toolCall": {
                            "toolCallId": "toolu_1",
                            "title": "Run shell command",
                            "kind": "execute",
                            "rawInput": {"command": "echo hi"},
                        },
                        "options": [
                            {"optionId": "allow_once", "kind": "allow_once", "name": "Allow once"},
                            {"optionId": "reject_once", "kind": "reject_once", "name": "Deny"},
                        ],
                    },
                }
                peer.send(request)
            elif obj.get("id") == "perm-1":
                replies.append(obj)
                _respond(peer, obj_id_holder["id"], {"stopReason": "end_turn", "usage": {}})

        peer.on_request(real_dispatch)

        transport = ACPTransport(peer.transport_reader, peer.transport_writer)
        try:
            transport.send_literal("sess-1", "run something")
            self.assertEqual(1, len(replies))
            self.assertEqual({"outcome": {"outcome": "selected", "optionId": "reject_once"}}, replies[0]["result"])
            self.assertEqual(1, len(transport.permission_log))
            entry = transport.permission_log[0]
            self.assertEqual("Run shell command", entry["title"])
            self.assertEqual({"command": "echo hi"}, entry["raw_input"])
            self.assertFalse(entry["allowed"])
            self.assertEqual("reject_once", entry["option_id"])
        finally:
            transport.close()
            peer.close()

    def test_authorised_permission_request_is_allowed_and_logged(self):
        peer = FakePeer()
        obj_id_holder = {}
        replies = []

        def dispatch(obj):
            if obj.get("method") == "session/prompt":
                obj_id_holder["id"] = obj["id"]
                peer.send({
                    "jsonrpc": "2.0",
                    "id": "perm-2",
                    "method": "session/request_permission",
                    "params": {
                        "sessionId": obj["params"]["sessionId"],
                        "toolCall": {
                            "toolCallId": "toolu_2",
                            "title": "Run tests",
                            "kind": "execute",
                            "rawInput": {"command": "python3 -m unittest"},
                        },
                        "options": [
                            {"optionId": "allow_once", "kind": "allow_once", "name": "Allow once"},
                            {"optionId": "reject_once", "kind": "reject_once", "name": "Deny"},
                        ],
                    },
                })
            elif obj.get("id") == "perm-2":
                replies.append(obj)
                _respond(peer, obj_id_holder["id"], {"stopReason": "end_turn", "usage": {}})

        peer.on_request(dispatch)
        policy = PermissionPolicy(authorized_titles={"Run tests"})
        transport = ACPTransport(peer.transport_reader, peer.transport_writer, permission_policy=policy)
        try:
            transport.send_literal("sess-1", "run the tests")
            self.assertEqual({"outcome": {"outcome": "selected", "optionId": "allow_once"}}, replies[0]["result"])
            entry = transport.permission_log[0]
            self.assertTrue(entry["allowed"])
        finally:
            transport.close()
            peer.close()


if __name__ == "__main__":
    unittest.main()
