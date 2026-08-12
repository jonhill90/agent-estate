import io
import json
import subprocess
import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from mcp_server import (  # noqa: E402
    PREFERRED_PROTOCOL_VERSION,
    SUPPORTED_PROTOCOL_VERSIONS,
    MCPServer,
    tool_definitions,
)
from supervisor_view import ReadSource, SupervisorUnavailable, SupervisorView  # noqa: E402


class StubSource(ReadSource):
    name = "lanes"
    summary = "stub"
    parameters = ({"name": "session", "type": "string", "required": False, "help": "session"},)

    def __init__(self, payload=None, error=None):
        self.payload = payload
        self.error = error
        self.calls = []

    def read(self, **arguments):
        self.calls.append(arguments)
        if self.error is not None:
            raise self.error
        return self.payload


class MutatingSource(ReadSource):
    name = "dispatch"
    summary = "sends work to a lane"
    mutates = True

    def read(self, **arguments):
        return {}


def server_for(source):
    return MCPServer(SupervisorView([source]), stdout=io.StringIO(), stderr=io.StringIO())


def request(method, params=None, message_id=1):
    message = {"jsonrpc": "2.0", "id": message_id, "method": method}
    if params is not None:
        message["params"] = params
    return message


class HandshakeTest(unittest.TestCase):
    def test_initialize_echoes_a_supported_client_version(self):
        result = server_for(StubSource({})).handle(
            request("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}})
        )["result"]
        self.assertEqual("2025-06-18", result["protocolVersion"])
        self.assertEqual({"name": "supervisor", "version": "0.1.0"}, result["serverInfo"])
        self.assertIn("tools", result["capabilities"])

    def test_initialize_offers_its_own_version_when_the_client_asks_for_an_unknown_one(self):
        result = server_for(StubSource({})).handle(
            request("initialize", {"protocolVersion": "1999-01-01"})
        )["result"]
        self.assertEqual(PREFERRED_PROTOCOL_VERSION, result["protocolVersion"])

    def test_every_supported_version_is_answered_with_itself(self):
        server = server_for(StubSource({}))
        for version in SUPPORTED_PROTOCOL_VERSIONS:
            result = server.handle(request("initialize", {"protocolVersion": version}))["result"]
            self.assertEqual(version, result["protocolVersion"])

    def test_notifications_are_never_answered(self):
        server = server_for(StubSource({}))
        self.assertIsNone(server.handle({"jsonrpc": "2.0", "method": "notifications/initialized"}))

    def test_ping_is_answered(self):
        self.assertEqual({}, server_for(StubSource({})).handle(request("ping"))["result"])

    def test_unknown_method_is_a_protocol_error(self):
        response = server_for(StubSource({})).handle(request("resources/list"))
        self.assertEqual(-32601, response["error"]["code"])

    def test_a_non_jsonrpc_message_is_refused(self):
        self.assertEqual(-32600, server_for(StubSource({})).handle({"method": "ping", "id": 1})["error"]["code"])


class ToolListTest(unittest.TestCase):
    def test_lists_the_view_s_sources_with_json_schema(self):
        tools = server_for(StubSource({})).handle(request("tools/list"))["result"]["tools"]
        self.assertEqual(["lanes"], [tool["name"] for tool in tools])
        self.assertEqual(
            {"type": "object", "properties": {"session": {"type": "string", "description": "session"}}},
            tools[0]["inputSchema"],
        )

    def test_a_required_parameter_reaches_the_schema(self):
        class Required(StubSource):
            parameters = ({"name": "lane", "type": "string", "required": True, "help": "lane"},)

        tools = tool_definitions(SupervisorView([Required({})]))
        self.assertEqual(["lane"], tools[0]["inputSchema"]["required"])

    def test_refuses_to_publish_a_mutating_source(self):
        """The write-tool gate is enforced here, not remembered. #198 asks for
        reads first; this is what makes 'first' mean something."""
        with self.assertRaises(ValueError) as caught:
            tool_definitions(SupervisorView([MutatingSource()]))
        self.assertIn("dispatch", str(caught.exception))

    def test_the_real_surface_is_three_read_tools(self):
        tools = tool_definitions(SupervisorView())
        self.assertEqual(["lanes", "digest", "ledger"], [tool["name"] for tool in tools])


class ToolCallTest(unittest.TestCase):
    def test_returns_the_source_payload_as_json_text(self):
        source = StubSource({"lanes": [{"name": "free-1", "state": "free"}], "count": 1})
        result = server_for(source).handle(request("tools/call", {"name": "lanes", "arguments": {}}))["result"]
        self.assertFalse(result["isError"])
        self.assertEqual(source.payload, json.loads(result["content"][0]["text"]))

    def test_arguments_reach_the_source(self):
        source = StubSource({"lanes": [], "count": 0})
        server_for(source).handle(request("tools/call", {"name": "lanes", "arguments": {"session": "harness-lab"}}))
        self.assertEqual([{"session": "harness-lab"}], source.calls)

    def test_a_blind_source_is_an_error_result_not_an_empty_one(self):
        """The defect this whole module exists to prevent: a tool answering
        `[]` when it could not see. The client has to be able to tell a quiet
        estate from an unreadable one."""
        source = StubSource(error=SupervisorUnavailable("lanes.sh exited 1: no server running"))
        result = server_for(source).handle(request("tools/call", {"name": "lanes", "arguments": {}}))["result"]
        self.assertTrue(result["isError"])
        text = result["content"][0]["text"]
        self.assertIn("no server running", text)
        self.assertNotIn("[]", text)

    def test_an_unexpected_source_exception_still_reaches_the_client_as_a_failure(self):
        source = StubSource(error=RuntimeError("boom"))
        result = server_for(source).handle(request("tools/call", {"name": "lanes", "arguments": {}}))["result"]
        self.assertTrue(result["isError"])
        self.assertIn("boom", result["content"][0]["text"])

    def test_unknown_tool_is_a_protocol_error(self):
        response = server_for(StubSource({})).handle(request("tools/call", {"name": "dispatch", "arguments": {}}))
        self.assertEqual(-32602, response["error"]["code"])

    def test_unknown_argument_is_a_protocol_error(self):
        response = server_for(StubSource({})).handle(
            request("tools/call", {"name": "lanes", "arguments": {"lane": "free-1"}})
        )
        self.assertEqual(-32602, response["error"]["code"])

    def test_non_object_arguments_are_refused(self):
        response = server_for(StubSource({})).handle(request("tools/call", {"name": "lanes", "arguments": []}))
        self.assertEqual(-32602, response["error"]["code"])


class StdioLoopTest(unittest.TestCase):
    def loop(self, lines, source=None):
        stdout = io.StringIO()
        server = MCPServer(SupervisorView([source or StubSource({"count": 0})]), stdout=stdout, stderr=io.StringIO())
        server.serve(io.StringIO("".join(line + "\n" for line in lines)))
        return [json.loads(line) for line in stdout.getvalue().splitlines() if line]

    def test_one_response_per_request_in_order(self):
        responses = self.loop(
            [
                json.dumps(request("initialize", {"protocolVersion": "2025-06-18"}, 1)),
                json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}),
                json.dumps(request("tools/list", None, 2)),
            ]
        )
        self.assertEqual([1, 2], [response["id"] for response in responses])

    def test_blank_lines_are_skipped(self):
        self.assertEqual([], self.loop(["", "   "]))

    def test_malformed_json_gets_a_parse_error_and_the_loop_survives(self):
        responses = self.loop(["{not json", json.dumps(request("ping", None, 7))])
        self.assertEqual(-32700, responses[0]["error"]["code"])
        self.assertEqual(7, responses[1]["id"])


class LiveSurfaceTest(unittest.TestCase):
    """Runs the shipped entry point as a subprocess -- no imports, no stubs."""

    def run_server(self, lines):
        process = subprocess.run(
            [sys.executable, str(SUPERVISOR_DIR / "mcp_server.py")],
            input="".join(line + "\n" for line in lines),
            capture_output=True,
            text=True,
            timeout=60,
        )
        return process

    def test_handshake_and_tools_list_over_real_stdio(self):
        process = self.run_server(
            [
                json.dumps(request("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                                                  "clientInfo": {"name": "test", "version": "0"}}, 1)),
                json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}),
                json.dumps(request("tools/list", None, 2)),
            ]
        )
        self.assertEqual(0, process.returncode, process.stderr)
        frames = [json.loads(line) for line in process.stdout.splitlines() if line.strip()]
        self.assertEqual(2, len(frames), process.stdout)
        self.assertEqual("supervisor", frames[0]["result"]["serverInfo"]["name"])
        self.assertEqual(["lanes", "digest", "ledger"], [tool["name"] for tool in frames[1]["result"]["tools"]])

    def test_a_missing_session_surfaces_as_an_error_not_an_empty_lane_list(self):
        """End-to-end mutation check: point the tool at a tmux session that
        does not exist and confirm the failure reaches the client."""
        process = self.run_server(
            [json.dumps(request("tools/call",
                                {"name": "lanes",
                                 "arguments": {"session": "no-such-session-198"}}, 1))]
        )
        result = json.loads(process.stdout.splitlines()[0])["result"]
        self.assertTrue(result["isError"], process.stdout)
        self.assertIn("lanes.sh", result["content"][0]["text"])

    def test_print_tools_emits_the_measurable_surface(self):
        process = subprocess.run(
            [sys.executable, str(SUPERVISOR_DIR / "mcp_server.py"), "--print-tools"],
            capture_output=True,
            text=True,
            timeout=60,
        )
        self.assertEqual(0, process.returncode, process.stderr)
        self.assertEqual(3, len(json.loads(process.stdout)["tools"]))


if __name__ == "__main__":
    unittest.main()
