"""pi RPC transport; a sibling to `transport.py` and `acp_transport.py`, not a
replacement for either.

Measured live against `pi --mode rpc` (version 0.80.6, see the PR description
for the exact frames): `{"type":"get_state"}` returns a `sessionId` and a
`sessionFile` on disk the moment the process starts, and a `prompt` command
gets a synchronous `{"id":...,"type":"response","command":"prompt",
"success":true}` acknowledging ACCEPTANCE only -- the actual answer streams
asynchronously afterward as `agent_start` -> ... -> `agent_end` ->
`agent_settled`, with no `id` on any of those event frames to correlate
against. That is the one structural difference from `ACPTransport`: ACP's
`session/prompt` is a single request/response call that blocks for the
settled OUTCOME; pi RPC's `prompt` blocks only for acceptance, so this
transport must itself wait for `agent_settled` to know a turn is done.
`send_literal` hides that seam behind the same "send a prompt, get back a
structured result" surface `ACPTransport.send_literal` exposes.

Resuming a session across process boundaries (the CLI dispatches one process
per command, exactly as `ACPTransport.load_session`'s docstring describes for
copilot) is `pi --mode rpc --session <id>`, confirmed live: a second process
started with that flag saw the first process's conversation and correctly
recalled a fact planted in it.
"""

from __future__ import annotations

import json
import subprocess
import threading
import time


DEFAULT_ACK_TIMEOUT_SECONDS = 30
# A prompt turn can run tools, read files, and think for minutes; this is not
# a network round trip. Generous on purpose -- a caller that wants a shorter
# leash passes its own `prompt_timeout`.
DEFAULT_PROMPT_TIMEOUT_SECONDS = 600


class PiRPCTimeoutError(TimeoutError):
    pass


class PiRPCProtocolError(RuntimeError):
    pass


class _PendingAck:
    def __init__(self):
        self.event = threading.Event()
        self.response = None


class PiRPCTransport:
    """A line-delimited JSON peer over an already-connected reader/writer pair.

    Tests inject a fake process's streams directly -- no real `pi` binary
    involved. Production use goes through `PiRPCTransport.spawn`, which
    launches the real `pi --mode rpc` process and wires its stdout/stdin here.
    """

    def __init__(
        self,
        reader,
        writer,
        *,
        clock=None,
        ack_timeout=DEFAULT_ACK_TIMEOUT_SECONDS,
        prompt_timeout=DEFAULT_PROMPT_TIMEOUT_SECONDS,
    ):
        self._reader_stream = reader
        self._writer_stream = writer
        self.clock = clock or time.time
        self.ack_timeout = ack_timeout
        self.prompt_timeout = prompt_timeout

        self._id_lock = threading.Lock()
        self._next_id = 1

        self._pending_lock = threading.Lock()
        self._pending = {}

        self._write_lock = threading.Lock()
        self._closed = threading.Event()
        self._process = None

        # A prompt's real answer arrives as untagged events, not as the
        # `prompt` command's own response -- see module docstring. `send_literal`
        # creates a fresh Event before sending and this loop sets it on the
        # matching `agent_settled`, so two overlapping prompts on one
        # transport would race; nothing here calls this transport
        # concurrently (mirrors ACPTransport's per-target session state,
        # scoped down to "one session per process" since that is what a pi
        # RPC subprocess actually is).
        self._settle_lock = threading.Lock()
        self._settle_event = None
        self._last_turn = {"stop_reason": None, "message": "", "usage": {}}

        self._reader_thread = threading.Thread(target=self._read_loop, daemon=True)
        self._reader_thread.start()

    @classmethod
    def spawn(cls, *, cwd, session=None, session_id=None, name=None, extra_args=(), **kwargs):
        """Launch a real `pi --mode rpc` process.

        `session` resumes an existing session by id or path (`--session`,
        confirmed live to replay prior turns into a fresh process). Omit both
        `session` and `session_id` to let pi mint a new session -- `get_state`
        on the resulting transport returns its id, which the caller must
        record before the process exits or the conversation is unresumable.
        """
        command = ["pi", "--mode", "rpc"]
        if session:
            command += ["--session", session]
        elif session_id:
            command += ["--session-id", session_id]
        if name:
            command += ["--name", name]
        command += list(extra_args)
        process = subprocess.Popen(
            command,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
            cwd=cwd,
        )
        transport = cls(process.stdout, process.stdin, **kwargs)
        transport._process = process
        return transport

    def close(self):
        self._closed.set()
        try:
            self._writer_stream.close()
        except Exception:
            pass

    def terminate(self):
        if self._process is not None:
            self._process.terminate()
        self.close()

    # -- wire plumbing --------------------------------------------------

    def _allocate_id(self):
        with self._id_lock:
            value = self._next_id
            self._next_id += 1
            return str(value)

    def _write(self, obj):
        line = json.dumps(obj)
        with self._write_lock:
            self._writer_stream.write(line + "\n")
            self._writer_stream.flush()

    def _read_loop(self):
        try:
            for line in self._reader_stream:
                line = line.strip()
                if not line:
                    continue
                try:
                    obj = json.loads(line)
                except json.JSONDecodeError:
                    continue
                self._dispatch(obj)
        finally:
            self._closed.set()
            with self._pending_lock:
                pending = list(self._pending.values())
                self._pending.clear()
            for entry in pending:
                entry.response = {"success": False, "error": "pi RPC connection closed before a response arrived"}
                entry.event.set()
            with self._settle_lock:
                event = self._settle_event
            # A dead process settles nothing further; wake a blocked
            # `send_literal` rather than let it wait out its full timeout.
            if event is not None:
                event.set()

    def _dispatch(self, obj):
        kind = obj.get("type")
        if kind == "response":
            self._handle_response(obj)
        elif kind == "message_end":
            self._record_message_end(obj)
        elif kind == "agent_settled":
            self._record_settled()

    def _handle_response(self, obj):
        request_id = obj.get("id")
        if request_id is None:
            return
        with self._pending_lock:
            entry = self._pending.pop(request_id, None)
        if entry is None:
            return
        entry.response = obj
        entry.event.set()

    def _record_message_end(self, obj):
        message = obj.get("message") or {}
        if message.get("role") != "assistant":
            return
        text = "".join(
            block.get("text", "") for block in message.get("content") or [] if block.get("type") == "text"
        )
        with self._settle_lock:
            self._last_turn = {
                "stop_reason": message.get("stopReason"),
                "message": text,
                "usage": message.get("usage") or {},
            }

    def _record_settled(self):
        with self._settle_lock:
            event = self._settle_event
        if event is not None:
            event.set()

    def _request(self, type_, extra=None, *, timeout=None):
        request_id = self._allocate_id()
        entry = _PendingAck()
        with self._pending_lock:
            self._pending[request_id] = entry
        frame = {"id": request_id, "type": type_}
        if extra:
            frame.update(extra)
        self._write(frame)
        wait_for = timeout if timeout is not None else self.ack_timeout
        if not entry.event.wait(wait_for):
            with self._pending_lock:
                self._pending.pop(request_id, None)
            raise PiRPCTimeoutError(f"pi RPC {type_!r} was not acknowledged after {wait_for}s")
        response = entry.response
        if response.get("success") is False:
            raise PiRPCProtocolError(f"pi RPC {type_!r} failed: {response.get('error')}")
        return response

    # -- surface used by the supervisor ----------------------------------

    def get_state(self):
        return self._request("get_state").get("data") or {}

    def send_literal(self, target, payload):
        """Send `payload` as a prompt and block until the turn settles.

        `target` is accepted only for a signature symmetric with
        `TmuxTransport.send_literal` / `ACPTransport.send_literal`; it is
        unused because a pi RPC subprocess is already scoped to exactly one
        session by the `--session`/`--session-id` flag `spawn` gave it -- see
        `spawn`'s docstring. There is nothing here to route between.
        """
        with self._settle_lock:
            self._last_turn = {"stop_reason": None, "message": "", "usage": {}}
            settle_event = threading.Event()
            self._settle_event = settle_event

        self._request("prompt", {"message": payload}, timeout=self.ack_timeout)

        if not settle_event.wait(self.prompt_timeout):
            raise PiRPCTimeoutError(f"pi RPC prompt did not settle within {self.prompt_timeout}s")

        with self._settle_lock:
            turn = dict(self._last_turn)

        usage = turn["usage"] or {}
        return {
            "stop_reason": turn["stop_reason"],
            "message": turn["message"],
            "token_usage": {
                "input_tokens": usage.get("input"),
                "output_tokens": usage.get("output"),
                "cache_read_tokens": usage.get("cacheRead"),
                "cache_write_tokens": usage.get("cacheWrite"),
                "total_tokens": usage.get("totalTokens"),
            },
        }
