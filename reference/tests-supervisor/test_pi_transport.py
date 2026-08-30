"""Tests drive PiRPCTransport against a fake `pi --mode rpc` peer -- no live
`pi` process is ever spawned. Two os.pipe() pairs stand in for stdio: the
transport under test gets one end of each, a background peer thread gets the
other and reads/writes the exact frames pi's own docs (rpc.md) and as#40's
live measurement describe (or a scripted deviation, per test)."""

import json
import os
import sys
import threading
import time
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from pi_transport import (  # noqa: E402
    PiRPCConnectionClosedError,
    PiRPCProtocolError,
    PiRPCTimeoutError,
    PiRPCTransport,
)


class FakePeer:
    """The `pi --mode rpc` side of the wire: reads lines the transport sends,
    and lets the test script exactly what frames go back, in order, on its
    own thread."""

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


def _ack(peer, request_id, data=None):
    frame = {"id": request_id, "type": "response", "success": True}
    if data is not None:
        frame["data"] = data
    peer.send(frame)


class PiRPCTransportGetStateTest(unittest.TestCase):
    def test_get_state_returns_the_data_payload(self):
        peer = FakePeer()
        peer.on_request(lambda obj: _ack(peer, obj["id"], {"sessionId": "sess-1", "sessionFile": "/tmp/sess-1.json"}))
        transport = PiRPCTransport(peer.transport_reader, peer.transport_writer)
        try:
            state = transport.get_state()
            self.assertEqual("sess-1", state["sessionId"])
            self.assertEqual("/tmp/sess-1.json", state["sessionFile"])
        finally:
            transport.close()
            peer.close()

    def test_get_state_sends_well_formed_request_frame(self):
        peer = FakePeer()
        peer.on_request(lambda obj: _ack(peer, obj["id"], {"sessionId": "sess-1"}))
        transport = PiRPCTransport(peer.transport_reader, peer.transport_writer)
        try:
            transport.get_state()
            time.sleep(0.05)
            self.assertEqual(1, len(peer.received))
            frame = peer.received[0]
            self.assertEqual("get_state", frame["type"])
            self.assertIn("id", frame)
        finally:
            transport.close()
            peer.close()


class PiRPCTransportPromptTest(unittest.TestCase):
    def test_send_literal_blocks_for_agent_settled_not_the_prompt_ack(self):
        """pi's `prompt` command only acks acceptance (module docstring) --
        the real answer streams asynchronously as message_end -> agent_settled
        with no id to correlate. This is the one structural difference from
        ACPTransport's synchronous session/prompt this transport must hide."""
        peer = FakePeer()

        def handler(obj):
            if obj["type"] == "prompt":
                _ack(peer, obj["id"])
                # Simulate the async stream arriving after the ack, not with it.
                peer.send({
                    "type": "message_end",
                    "message": {
                        "role": "assistant",
                        "stopReason": "end_turn",
                        "content": [{"type": "text", "text": "hello"}],
                        "usage": {"input": 100, "output": 12, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 112},
                    },
                })
                peer.send({"type": "agent_settled"})

        peer.on_request(handler)
        transport = PiRPCTransport(peer.transport_reader, peer.transport_writer)
        try:
            result = transport.send_literal("sess-1", "Say hello.")
            self.assertEqual("end_turn", result["stop_reason"])
            self.assertEqual("hello", result["message"])
            self.assertEqual(100, result["token_usage"]["input_tokens"])
            self.assertEqual(12, result["token_usage"]["output_tokens"])
            self.assertEqual(112, result["token_usage"]["total_tokens"])
        finally:
            transport.close()
            peer.close()

    def test_send_literal_prompt_frame_carries_the_payload_as_message(self):
        peer = FakePeer()

        def handler(obj):
            if obj["type"] == "prompt":
                _ack(peer, obj["id"])
                peer.send({"type": "agent_settled"})

        peer.on_request(handler)
        transport = PiRPCTransport(peer.transport_reader, peer.transport_writer)
        try:
            transport.send_literal("sess-1", "do the thing")
            time.sleep(0.05)
            frame = peer.received[-1]
            self.assertEqual("prompt", frame["type"])
            self.assertEqual("do the thing", frame["message"])
        finally:
            transport.close()
            peer.close()

    def test_send_literal_target_is_ignored_because_the_process_is_already_scoped(self):
        """`target` exists only for signature symmetry with TmuxTransport and
        ACPTransport -- a pi RPC subprocess is scoped to one session by the
        `--session`/`--session-id` flag `spawn` gave it, so there is nothing
        to route between."""
        peer = FakePeer()

        def handler(obj):
            if obj["type"] == "prompt":
                _ack(peer, obj["id"])
                peer.send({"type": "agent_settled"})

        peer.on_request(handler)
        transport = PiRPCTransport(peer.transport_reader, peer.transport_writer)
        try:
            transport.send_literal("this-argument-is-unused", "hi")
            frame = peer.received[-1]
            self.assertNotIn("sessionId", frame)
            self.assertNotIn("target", frame)
        finally:
            transport.close()
            peer.close()

    def test_send_literal_clears_prior_turn_between_calls(self):
        peer = FakePeer()
        replies = iter(["first", "second"])

        def handler(obj):
            if obj["type"] == "prompt":
                _ack(peer, obj["id"])
                text = next(replies)
                peer.send({
                    "type": "message_end",
                    "message": {"role": "assistant", "stopReason": "end_turn", "content": [{"type": "text", "text": text}]},
                })
                peer.send({"type": "agent_settled"})

        peer.on_request(handler)
        transport = PiRPCTransport(peer.transport_reader, peer.transport_writer)
        try:
            first = transport.send_literal("sess-1", "one")
            second = transport.send_literal("sess-1", "two")
            self.assertEqual("first", first["message"])
            self.assertEqual("second", second["message"])
        finally:
            transport.close()
            peer.close()

    def test_send_literal_ignores_non_assistant_message_end_frames(self):
        peer = FakePeer()

        def handler(obj):
            if obj["type"] == "prompt":
                _ack(peer, obj["id"])
                peer.send({
                    "type": "message_end",
                    "message": {"role": "user", "content": [{"type": "text", "text": "should not surface"}]},
                })
                peer.send({
                    "type": "message_end",
                    "message": {"role": "assistant", "stopReason": "end_turn", "content": [{"type": "text", "text": "real answer"}]},
                })
                peer.send({"type": "agent_settled"})

        peer.on_request(handler)
        transport = PiRPCTransport(peer.transport_reader, peer.transport_writer)
        try:
            result = transport.send_literal("sess-1", "hi")
            self.assertEqual("real answer", result["message"])
        finally:
            transport.close()
            peer.close()

    def test_send_literal_raises_when_prompt_ack_fails(self):
        peer = FakePeer()

        def handler(obj):
            if obj["type"] == "prompt":
                peer.send({"id": obj["id"], "type": "response", "success": False, "error": "no active session"})

        peer.on_request(handler)
        transport = PiRPCTransport(peer.transport_reader, peer.transport_writer)
        try:
            with self.assertRaises(PiRPCProtocolError):
                transport.send_literal("sess-1", "hello")
        finally:
            transport.close()
            peer.close()

    def test_send_literal_times_out_when_the_ack_never_arrives(self):
        peer = FakePeer()
        peer.on_request(lambda obj: None)  # never acks
        transport = PiRPCTransport(peer.transport_reader, peer.transport_writer, ack_timeout=0.2)
        try:
            with self.assertRaises(PiRPCTimeoutError):
                transport.send_literal("sess-1", "hello?")
        finally:
            transport.close()
            peer.close()

    def test_send_literal_times_out_when_the_turn_never_settles(self):
        """Acceptance without settlement is the failure mode this transport
        exists to hide from callers -- an ack alone must never read as a
        completed turn."""
        peer = FakePeer()

        def handler(obj):
            if obj["type"] == "prompt":
                _ack(peer, obj["id"])
                # No agent_settled ever arrives.

        peer.on_request(handler)
        transport = PiRPCTransport(peer.transport_reader, peer.transport_writer, prompt_timeout=0.2)
        try:
            with self.assertRaises(PiRPCTimeoutError):
                transport.send_literal("sess-1", "hello?")
        finally:
            transport.close()
            peer.close()

    def test_connection_closed_wakes_a_blocked_send_literal(self):
        """A dead process settles nothing further -- a blocked send_literal
        must be woken rather than wait out its full timeout, AND must raise
        rather than return a success-shaped result. `_read_loop` sets the
        same settle event a real settled turn uses, so a naive fix that only
        wakes the wait leaves `send_literal` returning
        `stop_reason=None, message=''` -- a dropped stream indistinguishable
        from a delivered, empty turn. That is the exact failure direction
        Phase 4a exists to retire (a response must be able to say "the pipe
        died", not just "no answer yet")."""
        peer = FakePeer()

        def handler(obj):
            if obj["type"] == "prompt":
                _ack(peer, obj["id"])
                peer.close()  # process exits before settling

        peer.on_request(handler)
        transport = PiRPCTransport(peer.transport_reader, peer.transport_writer, prompt_timeout=5)
        started = time.time()
        with self.assertRaises(PiRPCConnectionClosedError):
            transport.send_literal("sess-1", "hello?")
        elapsed = time.time() - started
        self.assertLess(elapsed, 2, "connection close should wake send_literal well before its 5s timeout")
        transport.close()

    def test_connection_closed_after_real_settle_does_not_mask_the_result(self):
        """The inverse race: `agent_settled` arrives and THEN the stream
        closes (e.g. the process exits right after finishing). The already-
        settled turn must still be returned -- closure must never overwrite
        a settle that already won."""
        peer = FakePeer()

        def handler(obj):
            if obj["type"] == "prompt":
                _ack(peer, obj["id"])
                peer.send({
                    "type": "message_end",
                    "message": {
                        "role": "assistant",
                        "stopReason": "end_turn",
                        "content": [{"type": "text", "text": "done"}],
                        "usage": {},
                    },
                })
                peer.send({"type": "agent_settled"})
                peer.close()

        peer.on_request(handler)
        transport = PiRPCTransport(peer.transport_reader, peer.transport_writer, prompt_timeout=5)
        try:
            result = transport.send_literal("sess-1", "hello?")
            self.assertEqual("end_turn", result["stop_reason"])
            self.assertEqual("done", result["message"])
        finally:
            transport.close()


if __name__ == "__main__":
    unittest.main()
