#!/usr/bin/env python3
"""MCP server over stdio: the supervisor's read surface, for any harness.

agent-dotfiles#198. A worker today has to be on a machine where
`scripts/supervisor/*.sh` exist at the right paths. Claude Code, Codex and
Copilot all speak MCP, so a tool surface is the one interface all three can
consume without the estate growing a per-harness client.

This file is a TRANSPORT and nothing else. It knows JSON-RPC and the MCP
method set; it does not know `lanes.sh`, `digest.sh` or the ledger. Every
answer comes from `supervisor_view.SupervisorView`. Delete this file and
nothing under `scripts/supervisor/` stops working -- that is the seam test
Jon's "adapters, not choices" directive asks for, and
`tests/supervisor/test_mcp_server.py` asserts it.

Stdlib only, and no `mcp` package. MCP stdio framing is newline-delimited
JSON-RPC 2.0, exactly the framing `acp_transport.py` already implements for
ACP in this same directory -- adding a pip dependency to a repository whose
only one is PyYAML would cost more than the ~80 lines of protocol below.

## Two failure channels, used for different things

* A malformed or unroutable REQUEST gets a JSON-RPC `error` object -- the
  client asked for something that does not exist.
* A tool that RAN and could not see gets a normal result with
  `isError: true` and the reason in its text. This is the MCP-specified
  channel for tool failures, and it is the one that matters here: the
  estate's most-repeated defect is an instrument that answers `[]` when it
  is blind. `SupervisorUnavailable` is converted here and never swallowed.

## Cost

Tool definitions are always-on context in every session that loads this
server. Three tools, one optional argument between them. `--print-tools`
emits the exact `tools/list` payload so the cost is measurable rather than
argued about; see the PR for the measurement.

## Write operations

None are exposed. `supervisor_view.WRITE_SOURCES` documents what a write
tool would need first (a caller identity, a blast-radius bound, an audit
trail) and why none of it exists yet. `_tool_definitions` publishes
`READ_SOURCES` only and refuses anything whose `mutates` is true, so the
absence is enforced here rather than remembered.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from supervisor_view import SupervisorUnavailable, SupervisorView  # noqa: E402


SERVER_NAME = "supervisor"
SERVER_VERSION = "0.1.0"

# Revisions this server can speak. MCP requires the server to answer
# `initialize` with a version it supports: the client's own, when that is one
# of these, and otherwise the newest this server knows, leaving the client to
# disconnect if it cannot live with the answer.
SUPPORTED_PROTOCOL_VERSIONS = ("2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25")
PREFERRED_PROTOCOL_VERSION = "2025-06-18"

PARSE_ERROR = -32700
INVALID_REQUEST = -32600
METHOD_NOT_FOUND = -32601
INVALID_PARAMS = -32602
INTERNAL_ERROR = -32603


def _json_schema(parameters):
    """Translate a source's transport-neutral parameters into JSON Schema.

    The translation lives here, not in `supervisor_view`, because JSON Schema
    is MCP's shape -- a different consumer of the same view would render the
    same `parameters` its own way.
    """
    properties = {
        parameter["name"]: {"type": parameter["type"], "description": parameter["help"]}
        for parameter in parameters
    }
    required = [parameter["name"] for parameter in parameters if parameter.get("required")]
    schema = {"type": "object", "properties": properties}
    if required:
        schema["required"] = required
    return schema


def tool_definitions(view):
    """The `tools/list` payload.

    Refuses to publish a mutating source. `WRITE_SOURCES` is empty today, so
    this can only fire if someone later moves a write source into
    `READ_SOURCES` -- which is exactly when a silent exposure would happen.
    """
    tools = []
    for source in view.describe():
        if source["mutates"]:
            raise ValueError(f"refusing to publish mutating source over MCP: {source['name']}")
        tools.append(
            {
                "name": source["name"],
                "description": source["summary"],
                "inputSchema": _json_schema(source["parameters"]),
            }
        )
    return tools


class MCPServer:
    def __init__(self, view=None, *, stdout=None, stderr=None):
        self.view = view if view is not None else SupervisorView()
        self.stdout = stdout if stdout is not None else sys.stdout
        self.stderr = stderr if stderr is not None else sys.stderr

    # -- protocol ---------------------------------------------------------

    def handle(self, message):
        """Route one decoded message. Returns a response dict, or None for a
        notification (which by JSON-RPC rule is never answered)."""
        if not isinstance(message, dict) or message.get("jsonrpc") != "2.0":
            return self._error(None, INVALID_REQUEST, "not a JSON-RPC 2.0 message")
        method = message.get("method")
        message_id = message.get("id")
        if method is None:
            return self._error(message_id, INVALID_REQUEST, "no method")
        if message_id is None:
            # A notification. `notifications/initialized` is the only one the
            # spec requires us to tolerate; every other is ignored the same
            # way, and none may produce output.
            return None
        params = message.get("params") or {}

        if method == "initialize":
            return self._result(message_id, self._initialize(params))
        if method == "ping":
            return self._result(message_id, {})
        if method == "tools/list":
            return self._result(message_id, {"tools": tool_definitions(self.view)})
        if method == "tools/call":
            return self._tools_call(message_id, params)
        return self._error(message_id, METHOD_NOT_FOUND, f"unknown method: {method}")

    def _initialize(self, params):
        requested = params.get("protocolVersion")
        version = requested if requested in SUPPORTED_PROTOCOL_VERSIONS else PREFERRED_PROTOCOL_VERSION
        return {
            "protocolVersion": version,
            "capabilities": {"tools": {"listChanged": False}},
            "serverInfo": {"name": SERVER_NAME, "version": SERVER_VERSION},
        }

    def _tools_call(self, message_id, params):
        name = params.get("name")
        # `or {}` would be wrong here: an empty list is falsy and would be
        # silently accepted as "no arguments" rather than refused as the
        # malformed request it is.
        arguments = params.get("arguments")
        if arguments is None:
            arguments = {}
        if not isinstance(arguments, dict):
            return self._error(message_id, INVALID_PARAMS, "arguments must be an object")
        try:
            payload = self.view.read(name, **arguments)
        except KeyError as error:
            # An unroutable tool name is a bad request, not a tool that ran
            # and failed -- it gets the protocol channel.
            return self._error(message_id, INVALID_PARAMS, str(error))
        except ValueError as error:
            return self._error(message_id, INVALID_PARAMS, str(error))
        except SupervisorUnavailable as error:
            # The load-bearing case. The source ran and could not see; the
            # client is told so in the channel it renders as a failure. It
            # must never receive an empty payload here.
            return self._result(message_id, self._content(f"supervisor read failed: {error}", is_error=True))
        except Exception as error:  # a source bug must still reach the client as a failure
            return self._result(
                message_id, self._content(f"supervisor read raised {type(error).__name__}: {error}", is_error=True)
            )
        return self._result(message_id, self._content(json.dumps(payload, sort_keys=True)))

    @staticmethod
    def _content(text, *, is_error=False):
        return {"content": [{"type": "text", "text": text}], "isError": is_error}

    @staticmethod
    def _result(message_id, result):
        return {"jsonrpc": "2.0", "id": message_id, "result": result}

    @staticmethod
    def _error(message_id, code, message):
        return {"jsonrpc": "2.0", "id": message_id, "error": {"code": code, "message": message}}

    # -- stdio loop -------------------------------------------------------

    def _write(self, response):
        self.stdout.write(json.dumps(response) + "\n")
        self.stdout.flush()

    def serve(self, stdin):
        """Read newline-delimited JSON-RPC from `stdin` until EOF.

        Nothing but responses ever reaches stdout -- a stray print here
        corrupts the frame stream and the client drops the connection, so
        diagnostics go to stderr.
        """
        for line in stdin:
            line = line.strip()
            if not line:
                continue
            try:
                message = json.loads(line)
            except json.JSONDecodeError as error:
                self._write(self._error(None, PARSE_ERROR, f"invalid JSON: {error}"))
                continue
            try:
                response = self.handle(message)
            except Exception as error:  # never die on one bad message
                print(f"mcp_server: {type(error).__name__}: {error}", file=self.stderr)
                response = self._error(message.get("id"), INTERNAL_ERROR, str(error))
            if response is not None:
                self._write(response)
        return 0


def main(argv=None):
    parser = argparse.ArgumentParser(description="Supervisor read surface as an MCP stdio server.")
    parser.add_argument(
        "--print-tools",
        action="store_true",
        help="print the exact tools/list payload and exit; this is what every "
        "consuming session pays for in static context.",
    )
    args = parser.parse_args(argv)
    server = MCPServer()
    if args.print_tools:
        print(json.dumps({"tools": tool_definitions(server.view)}, indent=2))
        return 0
    return server.serve(sys.stdin)


if __name__ == "__main__":
    raise SystemExit(main())
