import ast
import json
import re
import subprocess
import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import supervisor_view  # noqa: E402
from supervisor_view import (  # noqa: E402
    CLOSED_TASK_STATUSES,
    DigestSource,
    LanesSource,
    LedgerSource,
    SupervisorUnavailable,
    SupervisorView,
    build_source,
)


def completed(returncode=0, stdout="", stderr=""):
    return subprocess.CompletedProcess([], returncode, stdout=stdout, stderr=stderr)


def runner_returning(result):
    def runner(command, *, timeout):
        runner.command = command
        return result

    runner.command = None
    return runner


class LanesSourceTest(unittest.TestCase):
    def test_parses_lanes_json(self):
        rows = [{"window": 1, "name": "free-1", "command": "claude", "state": "free"}]
        source = LanesSource(runner=runner_returning(completed(stdout=json.dumps(rows))))
        self.assertEqual({"lanes": rows, "count": 1}, source.read())

    def test_session_argument_is_passed_through(self):
        runner = runner_returning(completed(stdout="[]"))
        LanesSource(runner=runner).read(session="harness-lab")
        self.assertEqual(["--json", "harness-lab"], runner.command[1:])

    def test_missing_session_is_an_error_not_an_empty_list(self):
        """lanes.sh exits 1 when the session does not exist, which its own
        header says is NOT 'no lanes'. That distinction has to survive the
        process boundary."""
        source = LanesSource(runner=runner_returning(completed(1, stderr="no server running")))
        with self.assertRaises(SupervisorUnavailable) as caught:
            source.read()
        self.assertIn("no server running", str(caught.exception))

    def test_silent_success_is_an_error(self):
        source = LanesSource(runner=runner_returning(completed(0, stdout="")))
        with self.assertRaises(SupervisorUnavailable) as caught:
            source.read()
        self.assertIn("no output", str(caught.exception))

    def test_non_json_output_is_an_error(self):
        source = LanesSource(runner=runner_returning(completed(0, stdout="WINDOW NAME STATE\n")))
        with self.assertRaises(SupervisorUnavailable):
            source.read()

    def test_json_that_is_not_a_lane_list_is_an_error(self):
        source = LanesSource(runner=runner_returning(completed(0, stdout='{"lanes":[]}')))
        with self.assertRaises(SupervisorUnavailable):
            source.read()

    def test_missing_script_is_an_error(self):
        with self.assertRaises(SupervisorUnavailable) as caught:
            LanesSource(script="/nonexistent/lanes.sh").read()
        self.assertIn("not found", str(caught.exception))


class DigestSourceTest(unittest.TestCase):
    def test_returns_the_digest_object(self):
        payload = {"checked": "2026-08-12T00:00:00Z", "errors": [], "ok": True, "lanes": {"free": "free-1"}}
        source = DigestSource(runner=runner_returning(completed(stdout=json.dumps(payload))))
        self.assertEqual(payload, source.read())

    def test_partial_digest_passes_through_with_its_errors_named(self):
        """digest.sh emits a partial digest with the failures named and exits
        0. Raising would throw away the readable half; the consumer sees
        ok:false and the reasons instead."""
        payload = {"checked": "2026-08-12T00:00:00Z", "errors": ["merged-list failed for skills"], "ok": False}
        source = DigestSource(runner=runner_returning(completed(stdout=json.dumps(payload))))
        self.assertEqual(payload, source.read())

    def test_missing_jq_is_an_error_not_a_digest(self):
        """digest.sh's jq guard prints {"errors":[...],"ok":false} and exits 1."""
        source = DigestSource(
            runner=runner_returning(
                completed(1, stdout='{"errors":["jq is required but not installed"],"ok":false}')
            )
        )
        with self.assertRaises(SupervisorUnavailable) as caught:
            source.read()
        self.assertIn("jq is required", str(caught.exception))

    def test_zero_byte_payload_is_an_error(self):
        source = DigestSource(runner=runner_returning(completed(0, stdout="")))
        with self.assertRaises(SupervisorUnavailable):
            source.read()

    def test_object_with_no_digest_fields_is_an_error(self):
        source = DigestSource(runner=runner_returning(completed(0, stdout='{"unrelated":1}')))
        with self.assertRaises(SupervisorUnavailable):
            source.read()


LEDGER_STATUS = {
    "lanes": [
        {
            "lane": "free-1",
            "pane_id": "%19",
            "nonce": "deadbeefdeadbeef",
            "harness": "claude",
            "repo": "agent-dotfiles",
            "server_id": "/tmp/x:1",
            "session_id": "$1",
            "command": "claude",
            "updated_at": 1,
        },
        {
            "lane": "free-2",
            "pane_id": "%20",
            "nonce": "cafecafecafecafe",
            "harness": "codex",
            "repo": "skills",
            "server_id": "/tmp/x:1",
            "session_id": "$1",
            "command": "codex",
            "updated_at": 2,
        },
    ],
    "tasks": [
        {"id": "t1", "lane": "free-1", "pane_nonce": "deadbeefdeadbeef", "summary": "open work", "status": "running",
         "created_at": 1, "updated_at": 2},
        {"id": "t0", "lane": "free-2", "pane_nonce": "cafecafecafecafe", "summary": "old work", "status": "complete",
         "created_at": 0, "updated_at": 1},
    ],
    "source_tasks": [],
    "events": [],
}


class LedgerSourceTest(unittest.TestCase):
    def source(self, result):
        return LedgerSource(runner=runner_returning(result), state_dir="/tmp/state")

    def test_reduces_to_open_tasks_and_availability(self):
        value = self.source(completed(stdout=json.dumps(LEDGER_STATUS))).read()
        self.assertEqual(["t1"], [task["id"] for task in value["open_tasks"]])
        self.assertEqual(["free-2"], value["available_lanes"])
        self.assertEqual([False, True], [lane["available"] for lane in value["lanes"]])

    def test_never_publishes_a_pane_nonce(self):
        """lanes.nonce and tasks.pane_nonce are what cli.py authenticates a
        caller with. Publishing either over MCP would hand every consumer the
        ability to impersonate a lane."""
        rendered = json.dumps(self.source(completed(stdout=json.dumps(LEDGER_STATUS))).read())
        self.assertNotIn("deadbeefdeadbeef", rendered)
        self.assertNotIn("cafecafecafecafe", rendered)
        self.assertNotIn("nonce", rendered)

    def test_unreadable_ledger_is_an_error_not_an_empty_estate(self):
        source = self.source(completed(1, stderr="sqlite3.DatabaseError: file is not a database"))
        with self.assertRaises(SupervisorUnavailable) as caught:
            source.read()
        self.assertIn("not a database", str(caught.exception))

    def test_status_shaped_wrong_is_an_error(self):
        with self.assertRaises(SupervisorUnavailable):
            self.source(completed(0, stdout='{"lanes":[]}')).read()

    def test_state_dir_is_passed_to_the_cli(self):
        runner = runner_returning(completed(stdout=json.dumps(LEDGER_STATUS)))
        LedgerSource(runner=runner, state_dir="/tmp/scratch").read()
        self.assertIn("/tmp/scratch", runner.command)
        self.assertEqual("status", runner.command[-1])

    def test_closed_status_set_matches_the_ledger_s_own(self):
        """`lane_available` and the one_open_task_per_lane index define
        "outstanding" in SQL. This constant restates it, so it is pinned
        against that SQL rather than left to drift."""
        core = (SUPERVISOR_DIR / "core.py").read_text(encoding="utf-8")
        rendered = ", ".join(f"'{status}'" for status in CLOSED_TASK_STATUSES)
        self.assertIn(f"status NOT IN ({rendered})", core)


class SupervisorViewTest(unittest.TestCase):
    def test_describes_every_read_source(self):
        names = [source["name"] for source in SupervisorView().describe()]
        self.assertEqual(["lanes", "digest", "ledger"], names)

    def test_every_exposed_source_is_read_only(self):
        self.assertTrue(all(not source["mutates"] for source in SupervisorView().describe()))

    def test_write_sources_is_empty_until_authorisation_exists(self):
        """#198 asks for reads first. This is the assertion that keeps a
        write tool from arriving without the story that has to come with it."""
        self.assertEqual({}, supervisor_view.WRITE_SOURCES)

    def test_unknown_source_is_refused(self):
        with self.assertRaises(KeyError):
            SupervisorView().read("dispatch")
        with self.assertRaises(KeyError):
            build_source("dispatch")

    def test_unknown_argument_is_refused(self):
        view = SupervisorView([LanesSource(runner=runner_returning(completed(stdout="[]")))])
        with self.assertRaises(ValueError):
            view.read("lanes", lane="free-1")


class SeamTest(unittest.TestCase):
    def test_the_view_names_no_transport(self):
        """The seam only holds if it points one way: the view must not know
        MCP, or removing the MCP layer would break the scripts."""
        text = (SUPERVISOR_DIR / "supervisor_view.py").read_text(encoding="utf-8")
        code = "\n".join(line for line in text.splitlines() if not line.lstrip().startswith("#"))
        body = code.split('"""', 2)[-1]
        self.assertNotIn("import mcp_server", body)
        self.assertNotIn("jsonrpc", body.lower())

    def test_no_supervisor_script_imports_the_mcp_layer(self):
        """Delete mcp_server.py and everything under scripts/supervisor/ still
        works. If this fails, the seam is in the wrong place.

        Checked against what the code actually depends on -- imported module
        names for Python, invocation for shell -- not against prose. Both
        `supervisor_view.py` and this suite NAME the transport in comments on
        purpose; naming it is documentation, importing it would be coupling.
        """
        invocation = re.compile(r"mcp_server\.py")
        for path in sorted(SUPERVISOR_DIR.rglob("*")):
            if not path.is_file() or path.name == "mcp_server.py":
                continue
            text = path.read_text(encoding="utf-8", errors="replace")
            if path.suffix == ".py":
                imported = set()
                for node in ast.walk(ast.parse(text, filename=str(path))):
                    if isinstance(node, ast.Import):
                        imported.update(alias.name.split(".")[0] for alias in node.names)
                    elif isinstance(node, ast.ImportFrom) and node.module:
                        imported.add(node.module.split(".")[0])
                self.assertNotIn("mcp_server", imported, f"{path.name} imports the MCP transport")
            elif path.suffix == ".sh":
                code = "\n".join(line for line in text.splitlines() if not line.lstrip().startswith("#"))
                self.assertIsNone(invocation.search(code), f"{path.name} invokes the MCP transport")


if __name__ == "__main__":
    unittest.main()
