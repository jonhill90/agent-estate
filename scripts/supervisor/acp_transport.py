"""ACP (Agent Client Protocol) transport; a sibling to `transport.py`, not a replacement.

Built alongside `TmuxTransport` per SPEC.md §15: the core owns the ledger and
lifecycle, a transport adapter owns only "deliver this prompt and report what
came back." Where tmux can only be watched and scraped, ACP is a JSON-RPC 2.0
peer over stdio (newline-delimited, one JSON object per line -- confirmed by a
live handshake against `copilot --acp`, see the PR description for the exact
frames) that returns two things tmux cannot: a structured `stopReason` instead
of pane-scrollback scraping (SPEC §14 L4), and per-task token usage. Permission
requests arrive as JSON-RPC requests this transport answers in code, via an
explicit, default-deny `PermissionPolicy`.

`send_literal(target, payload)` deliberately mirrors `TmuxTransport.send_literal`'s
name and signature -- same "deliver a prompt to a session" surface -- but ACP's
request/response shape lets it also return the structured result synchronously,
which tmux's fire-and-forget keystrokes cannot.
"""

from __future__ import annotations

import json
import subprocess
import threading
import time


DEFAULT_REQUEST_TIMEOUT_SECONDS = 30


class ACPTimeoutError(TimeoutError):
    pass


class ACPProtocolError(RuntimeError):
    pass


class PermissionPolicy:
    """Default-deny: nothing is approved unless its tool-call title is explicitly authorised.

    Every request is logged via `decide`'s caller regardless of outcome --
    this class only decides, it never suppresses the log entry.
    """

    def __init__(self, authorized_titles=()):
        self.authorized_titles = set(authorized_titles)

    def decide(self, tool_call, options):
        """Return (option_id, allowed) for a `session/request_permission` tool_call/options pair."""
        title = tool_call.get("title")
        if title in self.authorized_titles:
            allow = next((o for o in options if o.get("kind") == "allow_once"), None)
            if allow is not None:
                return allow.get("optionId"), True
        reject = next((o for o in options if o.get("kind") in ("reject_once", "reject_always")), None)
        if reject is not None:
            return reject.get("optionId"), False
        return None, False


class _PendingRequest:
    def __init__(self):
        self.event = threading.Event()
        self.response = None


class ACPTransport:
    """A JSON-RPC 2.0 peer over an already-connected reader/writer pair.

    Tests inject a fake peer's streams directly -- no subprocess involved.
    Production use goes through `ACPTransport.spawn`, which launches the real
    `copilot --acp` process and wires its stdout/stdin here.
    """

    def __init__(self, reader, writer, *, permission_policy=None, clock=None, request_timeout=DEFAULT_REQUEST_TIMEOUT_SECONDS):
        self._reader_stream = reader
        self._writer_stream = writer
        self.permission_policy = permission_policy or PermissionPolicy()
        self.clock = clock or time.time
        self.request_timeout = request_timeout
        self.permission_log = []
        self._agent_info = {}

        self._id_lock = threading.Lock()
        self._next_id = 1

        self._pending_lock = threading.Lock()
        self._pending = {}

        self._sessions_lock = threading.Lock()
        self._sessions = {}

        self._write_lock = threading.Lock()
        self._closed = threading.Event()
        self._process = None

        self._reader_thread = threading.Thread(target=self._read_loop, daemon=True)
        self._reader_thread.start()

    @classmethod
    def spawn(cls, command=("copilot", "--acp"), *, cwd=None, **kwargs):
        process = subprocess.Popen(
            list(command),
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
            return value

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
                entry.response = {"error": {"message": "ACP connection closed before a response arrived"}}
                entry.event.set()

    def _dispatch(self, obj):
        if "method" in obj and "id" in obj:
            self._handle_incoming_request(obj)
        elif "method" in obj:
            self._handle_notification(obj)
        elif "id" in obj:
            self._handle_response(obj)

    def _handle_response(self, obj):
        with self._pending_lock:
            entry = self._pending.pop(obj["id"], None)
        if entry is None:
            return
        entry.response = obj
        entry.event.set()

    def _handle_notification(self, obj):
        if obj.get("method") == "session/update":
            self._record_update(obj.get("params") or {})

    def _record_update(self, params):
        session_id = params.get("sessionId")
        update = params.get("update") or {}
        kind = update.get("sessionUpdate")
        with self._sessions_lock:
            state = self._sessions.setdefault(session_id, {"chunks": [], "context_usage": None})
            if kind == "agent_message_chunk":
                text = (update.get("content") or {}).get("text", "")
                state["chunks"].append(text)
            elif kind == "usage_update":
                state["context_usage"] = {"used": update.get("used"), "size": update.get("size")}

    def _handle_incoming_request(self, obj):
        method = obj.get("method")
        request_id = obj.get("id")
        params = obj.get("params") or {}
        if method == "session/request_permission":
            result = self._answer_permission(params)
        else:
            result = None
        self._write({"jsonrpc": "2.0", "id": request_id, "result": result})

    def _answer_permission(self, params):
        tool_call = params.get("toolCall") or {}
        options = params.get("options") or []
        option_id, allowed = self.permission_policy.decide(tool_call, options)
        self.permission_log.append(
            {
                "session_id": params.get("sessionId"),
                "tool_call_id": tool_call.get("toolCallId"),
                "title": tool_call.get("title"),
                "kind": tool_call.get("kind"),
                "raw_input": tool_call.get("rawInput"),
                "allowed": allowed,
                "option_id": option_id,
                "timestamp": self.clock(),
            }
        )
        if option_id is None:
            return {"outcome": {"outcome": "cancelled"}}
        return {"outcome": {"outcome": "selected", "optionId": option_id}}

    def _request(self, method, params):
        request_id = self._allocate_id()
        entry = _PendingRequest()
        with self._pending_lock:
            self._pending[request_id] = entry
        self._write({"jsonrpc": "2.0", "id": request_id, "method": method, "params": params})
        if not entry.event.wait(self.request_timeout):
            with self._pending_lock:
                self._pending.pop(request_id, None)
            raise ACPTimeoutError(f"ACP request {method!r} timed out after {self.request_timeout}s")
        response = entry.response
        if "error" in response:
            raise ACPProtocolError(f"ACP error for {method!r}: {response['error']}")
        return response.get("result")

    # -- surface used by the supervisor ----------------------------------

    def initialize(self, *, protocol_version=1, client_capabilities=None):
        result = self._request(
            "initialize",
            {"protocolVersion": protocol_version, "clientCapabilities": client_capabilities or {}},
        )
        self._agent_info = result or {}
        return result

    def metadata(self, target=None):
        agent_info = self._agent_info.get("agentInfo", {})
        entry = {
            "protocol_version": self._agent_info.get("protocolVersion"),
            "agent_name": agent_info.get("name"),
            "agent_version": agent_info.get("version"),
        }
        if target is not None:
            with self._sessions_lock:
                entry["session_known"] = target in self._sessions
        return entry

    def new_session(self, cwd, *, mcp_servers=None):
        result = self._request("session/new", {"cwd": cwd, "mcpServers": mcp_servers or []})
        session_id = result["sessionId"]
        with self._sessions_lock:
            self._sessions[session_id] = {"chunks": [], "context_usage": None}
        return session_id

    def load_session(self, session_id, *, cwd):
        """Resume a session a previous, since-exited process created.

        The CLI dispatches one command per process, so the `copilot --acp`
        subprocess behind a lane does not survive between calls -- each
        `assign` reconnects with a fresh `spawn()` and must resume the
        lane's session by id rather than starting a new one. Relies on the
        agent's `loadSession` capability (confirmed present for
        `copilot --acp`, see the PR description for the handshake).
        """
        self._request("session/load", {"sessionId": session_id, "cwd": cwd})
        with self._sessions_lock:
            self._sessions[session_id] = {"chunks": [], "context_usage": None}
        return session_id

    def send_literal(self, target, payload):
        """Deliver `payload` to session `target` and return the structured result.

        Unlike `TmuxTransport.send_literal`, which fires keystrokes and returns
        nothing, ACP's request/response shape lets this report what came back:
        the assembled message text, the structured `stopReason`, and per-task
        token usage -- the two things SPEC §15 names as ACP's justification.
        """
        with self._sessions_lock:
            self._sessions.setdefault(target, {"chunks": [], "context_usage": None})
            self._sessions[target]["chunks"] = []

        result = self._request(
            "session/prompt",
            {"sessionId": target, "prompt": [{"type": "text", "text": payload}]},
        )

        with self._sessions_lock:
            state = self._sessions.get(target, {"chunks": [], "context_usage": None})
            message = "".join(state["chunks"])
            context_usage = state["context_usage"]

        usage = (result or {}).get("usage") or {}
        return {
            "stop_reason": (result or {}).get("stopReason"),
            "message": message,
            "token_usage": {
                "input_tokens": usage.get("inputTokens"),
                "output_tokens": usage.get("outputTokens"),
                "total_tokens": usage.get("totalTokens"),
                "thought_tokens": usage.get("thoughtTokens"),
                "cached_read_tokens": usage.get("cachedReadTokens"),
                "cached_write_tokens": usage.get("cachedWriteTokens"),
            },
            "context_window": context_usage,
        }
