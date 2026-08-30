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
    EventsSource,
    LanesSource,
    LedgerSource,
    ReadSource,
    SessionAddSource,
    SessionAttachSource,
    SessionDetachSource,
    SessionRemoveCheckSource,
    SessionRemoveSource,
    SessionSendSource,
    SessionsSource,
    SupervisorUnavailable,
    SupervisorView,
    WriteSource,
    build_source,
    build_write_source,
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


class SessionsSourceTest(unittest.TestCase):
    def test_parses_sessions_json(self):
        rows = [
            {"session": "director", "supervised": False, "lanes": [{"window": 1, "state": "supervisor"}]},
            {"session": "agent-supervisor", "supervised": True, "lanes": []},
        ]
        source = SessionsSource(runner=runner_returning(completed(stdout=json.dumps(rows))))
        self.assertEqual({"sessions": rows, "count": 2}, source.read())

    def test_takes_no_session_argument(self):
        """Unlike `lanes`, `sessions` answers for every session that exists --
        naming one would be the single-session shape agent-tui#13 is about."""
        self.assertEqual((), SessionsSource.parameters)

    def test_no_tmux_sessions_is_an_error_not_an_empty_list(self):
        """sessions.sh exits 1 when tmux has no sessions at all -- the same
        'blind, not quiet' contract lanes.sh's own exit already carries."""
        source = SessionsSource(runner=runner_returning(completed(1, stderr="no tmux sessions")))
        with self.assertRaises(SupervisorUnavailable) as caught:
            source.read()
        self.assertIn("no tmux sessions", str(caught.exception))

    def test_silent_success_is_an_error(self):
        source = SessionsSource(runner=runner_returning(completed(0, stdout="")))
        with self.assertRaises(SupervisorUnavailable):
            source.read()

    def test_json_that_is_not_a_session_list_is_an_error(self):
        source = SessionsSource(runner=runner_returning(completed(0, stdout='{"sessions":[]}')))
        with self.assertRaises(SupervisorUnavailable):
            source.read()

    def test_missing_script_is_an_error(self):
        with self.assertRaises(SupervisorUnavailable) as caught:
            SessionsSource(script="/nonexistent/sessions.sh").read()
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
            "transport": "send-keys",
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
            "transport": "pi-rpc",
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

    def test_publishes_each_lane_s_transport(self):
        """agent-supervisor#87: 'how is this lane driven' has to be
        answerable from the read surface, not just from a local ledger
        query -- send-keys, acp and pi-rpc are not interchangeable."""
        value = self.source(completed(stdout=json.dumps(LEDGER_STATUS))).read()
        self.assertEqual(
            {"free-1": "send-keys", "free-2": "pi-rpc"},
            {lane["lane"]: lane["transport"] for lane in value["lanes"]},
        )

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
        against that SQL rather than left to drift.

        `lane_available` lives in `core_ledger_lanes.py`, not `core.py`,
        since agent-supervisor#706 split `core.py` behind a re-export shim
        (the `sync.py`/#336 pattern) -- `core.py` is now a composition root
        and no longer holds any SQL literal itself."""
        core = (SUPERVISOR_DIR / "core_ledger_lanes.py").read_text(encoding="utf-8")
        rendered = ", ".join(f"'{status}'" for status in CLOSED_TASK_STATUSES)
        self.assertIn(f"status NOT IN ({rendered})", core)


EVENT_ROWS = [
    {
        "key": "completion:t1",
        "type": "completion",
        "task_id": "t1",
        "status": "pending",
        "payload_path": "/state/results/t1.md",
        "created_at": 1,
        "notified_at": None,
        "retry_at": None,
        "acked_at": None,
        "pane_nonce": "should-never-appear",
    },
    {
        "key": "completion:t2",
        "type": "completion",
        "task_id": "t2",
        "status": "acked",
        "payload_path": "/state/results/t2.md",
        "created_at": 2,
        "notified_at": 2,
        "retry_at": None,
        "acked_at": 3,
    },
]


class EventsSourceTest(unittest.TestCase):
    def source(self, result):
        return EventsSource(runner=runner_returning(result), state_dir="/tmp/state")

    def test_returns_the_whole_log_with_no_cursor(self):
        value = self.source(completed(stdout=json.dumps(EVENT_ROWS))).read()
        self.assertEqual(["completion:t1", "completion:t2"], [event["key"] for event in value["events"]])
        self.assertEqual(2, value["count"])
        self.assertEqual("completion:t2", value["next_cursor"])

    def test_after_key_returns_only_what_is_new(self):
        value = self.source(completed(stdout=json.dumps(EVENT_ROWS))).read(after_key="completion:t1")
        self.assertEqual(["completion:t2"], [event["key"] for event in value["events"]])
        self.assertEqual("completion:t2", value["next_cursor"])

    def test_after_key_at_the_end_returns_nothing_new_and_holds_the_cursor(self):
        value = self.source(completed(stdout=json.dumps(EVENT_ROWS))).read(after_key="completion:t2")
        self.assertEqual([], value["events"])
        self.assertEqual("completion:t2", value["next_cursor"])

    def test_unknown_cursor_is_refused_not_treated_as_the_start(self):
        """A stale or made-up cursor must not silently resolve to 'the whole
        log' -- that would hide a gap from a consumer resuming after a
        restart."""
        with self.assertRaises(ValueError):
            self.source(completed(stdout=json.dumps(EVENT_ROWS))).read(after_key="no-such-key")

    def test_pane_nonce_is_not_in_the_allowlist(self):
        rendered = json.dumps(self.source(completed(stdout=json.dumps(EVENT_ROWS))).read())
        self.assertNotIn("should-never-appear", rendered)

    def test_unreadable_ledger_is_an_error_not_an_empty_log(self):
        source = self.source(completed(1, stderr="sqlite3.DatabaseError: file is not a database"))
        with self.assertRaises(SupervisorUnavailable):
            source.read()

    def test_json_that_is_not_an_event_list_is_an_error(self):
        source = self.source(completed(0, stdout='{"events":[]}'))
        with self.assertRaises(SupervisorUnavailable):
            source.read()


class SupervisorViewTest(unittest.TestCase):
    def test_describes_every_read_source(self):
        names = [source["name"] for source in SupervisorView().describe() if not source["mutates"]]
        self.assertEqual(["lanes", "sessions", "digest", "ledger", "events", "session_remove_check"], names)

    def test_describes_every_write_source(self):
        names = [source["name"] for source in SupervisorView().describe() if source["mutates"]]
        self.assertEqual(
            ["session_attach", "session_detach", "session_add", "session_remove", "session_send"], names
        )

    def test_every_read_source_is_read_only(self):
        self.assertTrue(all(not s["mutates"] for s in SupervisorView().describe() if s["name"] in READ_SOURCES_NAMES))

    def test_every_write_source_mutates(self):
        self.assertTrue(all(s["mutates"] for s in SupervisorView().describe() if s["name"] in WRITE_SOURCES_NAMES))

    def test_write_sources_are_exactly_agent_tui_14s_four_plus_508s_send(self):
        """agent-tui#14's original four, plus session_send (agent-supervisor#508)
        -- still not a general write capability; `dispatch`/`merge` stay
        excluded (see `WRITE_SOURCES`'s own docstring for why)."""
        self.assertEqual(
            {"session_attach", "session_detach", "session_add", "session_remove", "session_send"},
            set(supervisor_view.WRITE_SOURCES),
        )

    def test_unknown_source_is_refused(self):
        with self.assertRaises(KeyError):
            SupervisorView().read("dispatch")
        with self.assertRaises(KeyError):
            build_source("dispatch")

    def test_unknown_write_is_refused(self):
        with self.assertRaises(KeyError):
            SupervisorView().write("dispatch")
        with self.assertRaises(KeyError):
            build_write_source("dispatch")

    def test_unknown_argument_is_refused(self):
        view = SupervisorView(
            [LanesSource(runner=runner_returning(completed(stdout="[]")))], []
        )
        with self.assertRaises(ValueError):
            view.read("lanes", lane="free-1")

    def test_call_routes_reads_and_writes_to_the_right_registry(self):
        read_source = StubReadSource()
        write_source = StubWriteSource()
        view = SupervisorView([read_source], [write_source])
        self.assertEqual({"ok": "read"}, view.call("stub-read"))
        self.assertEqual({"ok": "write"}, view.call("stub-write"))

    def test_call_on_an_unknown_name_is_refused(self):
        view = SupervisorView([StubReadSource()], [StubWriteSource()])
        with self.assertRaises(KeyError):
            view.call("no-such-tool")

    def test_construction_refuses_a_read_source_that_mutates(self):
        """The structural gate agent-tui#14 asks for: a source registered in
        READ_SOURCES with mutates=True must fail loudly at construction, not
        reach a transport silently."""

        class MisregisteredRead(ReadSource):
            name = "sneaky"
            mutates = True

            def read(self, **arguments):
                return {}

        with self.assertRaises(ValueError):
            SupervisorView([MisregisteredRead()], [])

    def test_construction_refuses_a_write_source_that_does_not_mutate(self):
        class MisregisteredWrite(WriteSource):
            name = "sneaky"
            mutates = False

            def write(self, **arguments):
                return {}

        with self.assertRaises(ValueError):
            SupervisorView([], [MisregisteredWrite()])


READ_SOURCES_NAMES = {"lanes", "sessions", "digest", "ledger", "events", "session_remove_check"}
WRITE_SOURCES_NAMES = {"session_attach", "session_detach", "session_add", "session_remove", "session_send"}


class StubReadSource(ReadSource):
    name = "stub-read"

    def read(self, **arguments):
        return {"ok": "read"}


class StubWriteSource(WriteSource):
    name = "stub-write"

    def write(self, **arguments):
        return {"ok": "write"}


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


class FakeTransport:
    def __init__(self, *, exists=True, clients=None):
        self.exists = exists
        self.switched = []
        self.detached = 0
        self.killed = []
        # One attached client by default -- the unambiguous case -- so
        # existing callers that never pass `client` keep working. Tests of
        # the agent-supervisor#189 guard override this directly.
        self._clients = clients if clients is not None else [{"tty": "/dev/ttys000", "session": "elsewhere"}]

    def session_exists(self, session):
        return self.exists

    def list_clients(self):
        return [dict(c) for c in self._clients]

    def switch_client(self, session, client=None):
        self.switched.append(session)
        for c in self._clients:
            if client is None or c["tty"] == client:
                c["session"] = session
                if client is not None:
                    break

    def detach_client(self, client=None):
        self.detached += 1
        if client is None:
            self._clients = []
        else:
            self._clients = [c for c in self._clients if c["tty"] != client]

    def kill_session(self, session):
        self.killed.append(session)


class SessionRemoveCheckSourceTest(unittest.TestCase):
    def test_reads_the_guard_verbatim(self):
        transport = FakeTransport()
        transport.list_panes = lambda session: []
        runner = runner_returning(completed(stdout=""))

        def scripted(command, *, timeout):
            joined = " ".join(command)
            if "session-state" in joined:
                return completed(stdout='{"session": "work", "state": "supervised"}')
            if "lanes.sh" in joined:
                return completed(stdout="[]")
            raise AssertionError(joined)

        source = SessionRemoveCheckSource(transport=transport, runner=scripted)
        result = source.read(session="work")
        self.assertTrue(result["safe_to_remove"])
        self.assertEqual([], result["refusals"])


class SessionAttachSourceTest(unittest.TestCase):
    def test_refuses_a_missing_session(self):
        source = SessionAttachSource(transport=FakeTransport(exists=False))
        with self.assertRaises(SupervisorUnavailable):
            source.write(session="no-such-session")

    def test_single_client_switches_and_reports_success(self):
        """agent-supervisor#189: exactly one attached client is unambiguous
        -- no `client` argument required, and the fallback stays."""
        transport = FakeTransport(exists=True, clients=[{"tty": "/dev/ttys000", "session": "elsewhere"}])
        source = SessionAttachSource(transport=transport)
        result = source.write(session="work")
        self.assertEqual({"session": "work", "client": "/dev/ttys000", "attached": True}, result)
        self.assertEqual(["work"], transport.switched)

    def test_two_clients_with_no_target_refuses(self):
        """agent-supervisor#189: this is the case that used to switch an
        arbitrary, unnamed client and still report success."""
        transport = FakeTransport(
            exists=True,
            clients=[{"tty": "/dev/ttys000", "session": "a"}, {"tty": "/dev/ttys001", "session": "b"}],
        )
        source = SessionAttachSource(transport=transport)
        with self.assertRaises(SupervisorUnavailable):
            source.write(session="work")
        self.assertEqual([], transport.switched)

    def test_two_clients_with_explicit_target_moves_only_that_client(self):
        transport = FakeTransport(
            exists=True,
            clients=[{"tty": "/dev/ttys000", "session": "a"}, {"tty": "/dev/ttys001", "session": "b"}],
        )
        source = SessionAttachSource(transport=transport)
        result = source.write(session="work", client="/dev/ttys000")
        self.assertEqual({"session": "work", "client": "/dev/ttys000", "attached": True}, result)
        self.assertEqual("work", {c["tty"]: c["session"] for c in transport.list_clients()}["/dev/ttys000"])
        self.assertEqual("b", {c["tty"]: c["session"] for c in transport.list_clients()}["/dev/ttys001"])

    def test_no_client_attached_refuses(self):
        transport = FakeTransport(exists=True, clients=[])
        source = SessionAttachSource(transport=transport)
        with self.assertRaises(SupervisorUnavailable):
            source.write(session="work")

    def test_read_back_mismatch_is_reported_as_failure(self):
        """agent-supervisor#189 requirement 3: `attached: true` only once a
        read of tmux confirms the named client actually moved."""
        transport = FakeTransport(exists=True, clients=[{"tty": "/dev/ttys000", "session": "elsewhere"}])
        transport.switch_client = lambda session, client=None: None  # command "succeeds" but nothing moves
        source = SessionAttachSource(transport=transport)
        with self.assertRaises(SupervisorUnavailable):
            source.write(session="work")


class SessionDetachSourceTest(unittest.TestCase):
    def test_single_client_detaches_and_reports_success(self):
        transport = FakeTransport(clients=[{"tty": "/dev/ttys000", "session": "work"}])
        source = SessionDetachSource(transport=transport)
        self.assertEqual({"client": "/dev/ttys000", "detached": True}, source.write())
        self.assertEqual(1, transport.detached)

    def test_two_clients_with_no_target_refuses(self):
        transport = FakeTransport(
            clients=[{"tty": "/dev/ttys000", "session": "a"}, {"tty": "/dev/ttys001", "session": "b"}]
        )
        source = SessionDetachSource(transport=transport)
        with self.assertRaises(SupervisorUnavailable):
            source.write()
        self.assertEqual(0, transport.detached)

    def test_two_clients_with_explicit_target_detaches_only_that_client(self):
        transport = FakeTransport(
            clients=[{"tty": "/dev/ttys000", "session": "a"}, {"tty": "/dev/ttys001", "session": "b"}]
        )
        source = SessionDetachSource(transport=transport)
        result = source.write(client="/dev/ttys000")
        self.assertEqual({"client": "/dev/ttys000", "detached": True}, result)
        self.assertEqual(["/dev/ttys001"], [c["tty"] for c in transport.list_clients()])

    def test_read_back_mismatch_is_reported_as_failure(self):
        transport = FakeTransport(clients=[{"tty": "/dev/ttys000", "session": "work"}])
        transport.detach_client = lambda client=None: None  # command "succeeds" but nothing detaches
        source = SessionDetachSource(transport=transport)
        with self.assertRaises(SupervisorUnavailable):
            source.write()


class SessionAddSourceTest(unittest.TestCase):
    def test_bootstrap_failure_surfaces_its_stderr_tail(self):
        runner = runner_returning(completed(1, stderr="bootstrap-session: session 'work' already exists -- refusing."))
        source = SessionAddSource(runner=runner)
        with self.assertRaises(SupervisorUnavailable) as caught:
            source.write(session="work")
        self.assertIn("already exists", str(caught.exception))

    def test_success_reports_the_resulting_session_state(self):
        def scripted(command, *, timeout):
            joined = " ".join(command)
            if "bootstrap-session.sh" in joined:
                scripted.bootstrap_command = command
                return completed(0, stdout="bootstrap-session: 3 created, 0 left alone")
            if "session-state" in joined:
                return completed(stdout='{"session": "work", "state": "supervised"}')
            raise AssertionError(joined)

        source = SessionAddSource(runner=scripted)
        result = source.write(session="work", lanes=4, agent="claude", cwd="/tmp/work")
        self.assertEqual(
            {
                "session": "work",
                "created": True,
                "state": "supervised",
                "bootstrap_output": "bootstrap-session: 3 created, 0 left alone",
            },
            result,
        )
        self.assertIn("--lanes", scripted.bootstrap_command)
        self.assertIn("--agent", scripted.bootstrap_command)
        self.assertIn("--cwd", scripted.bootstrap_command)
        self.assertNotIn("--add-lanes", scripted.bootstrap_command)


class SessionSendSourceTest(unittest.TestCase):
    """agent-supervisor#508. Mirrors `daemon/internal/sendmsg`'s own three
    states one layer up: `supervisord send`'s stdout JSON, not tmux, is what
    this class trusts -- and NEVER by exit code alone (see the class's own
    docstring for why `_decode()` is not used here)."""

    def test_delivered_reports_success(self):
        runner = runner_returning(
            completed(0, stdout=json.dumps({"status": "delivered", "session_id": "s1", "turns": 2, "cost_usd": 0.03}))
        )
        source = SessionSendSource(binary="/opt/supervisord", runner=runner)
        result = source.write(session_id="s1", message="keep going")
        self.assertEqual({"session_id": "s1", "delivered": True, "turns": 2, "cost_usd": 0.03}, result)
        self.assertEqual(["/opt/supervisord", "send", "-session-id", "s1", "-message", "keep going"], runner.command)

    def test_failed_is_never_reported_as_delivered(self):
        """The direction the issue calls out as mattering more: a confirmed
        failure must raise, not return a delivered=True-shaped dict. This is
        the mutation-check's RED case -- flip the `status == "delivered"`
        branch to `if True` and this test is what catches it."""
        runner = runner_returning(
            completed(1, stdout=json.dumps({"status": "failed", "error": "agent: turn reported is_error"}))
        )
        source = SessionSendSource(binary="/opt/supervisord", runner=runner)
        with self.assertRaises(SupervisorUnavailable) as caught:
            source.write(session_id="s1", message="keep going")
        self.assertIn("is_error", str(caught.exception))

    def test_unknown_timeout_is_distinguished_from_failed(self):
        """agent-supervisor#488: a timeout must not be stamped as a definite
        failure. Both raise (write() has no third return shape), but the
        raised text must say UNKNOWN, not reuse the failed-send wording --
        the one thing a caller reading the exception can still tell apart."""
        runner = runner_returning(
            completed(3, stdout=json.dumps({"status": "unknown", "error": "did not complete before the deadline"}))
        )
        source = SessionSendSource(binary="/opt/supervisord", runner=runner)
        with self.assertRaises(SupervisorUnavailable) as caught:
            source.write(session_id="s1", message="keep going")
        message = str(caught.exception)
        self.assertIn("UNKNOWN", message)
        self.assertNotIn("send failed", message)

    def test_no_output_at_all_is_an_error_not_a_silent_success(self):
        runner = runner_returning(completed(1, stderr="exec: \"supervisord\": file not found"))
        source = SessionSendSource(binary="/opt/supervisord", runner=runner)
        with self.assertRaises(SupervisorUnavailable) as caught:
            source.write(session_id="s1", message="keep going")
        self.assertIn("file not found", str(caught.exception))

    def test_optional_arguments_are_passed_through(self):
        runner = runner_returning(completed(0, stdout=json.dumps({"status": "delivered", "session_id": "s1"})))
        source = SessionSendSource(binary="/opt/supervisord", runner=runner)
        source.write(session_id="s1", message="hi", harness="codex", cwd="/tmp/w", model="o1", timeout=30)
        self.assertIn("-harness", runner.command)
        self.assertIn("-cwd", runner.command)
        self.assertIn("-model", runner.command)
        self.assertIn("-timeout", runner.command)
        self.assertIn("30s", runner.command)


SAFE_GUARD = {
    "session": "work",
    "exists": True,
    "supervision": "supervised",
    "busy_lanes": [],
    "worktrees": [],
    "safe_to_remove": True,
    "refusals": [],
}


def _guard_runner(*, state="supervised", lanes_json="[]"):
    def scripted(command, *, timeout):
        joined = " ".join(command)
        if "session-state" in joined:
            return completed(stdout=f'{{"session": "work", "state": "{state}"}}')
        if "lanes.sh" in joined:
            return completed(stdout=lanes_json)
        if "record-session-event" in joined:
            return completed(stdout='{"key": "session:removed:work:1"}')
        raise AssertionError(joined)

    return scripted


class SessionRemoveSourceTest(unittest.TestCase):
    def test_confirm_missing_raises_value_error_before_the_guard_runs(self):
        calls = []

        def runner(command, *, timeout):
            calls.append(command)
            raise AssertionError("guard must not run before confirm is checked")

        source = SessionRemoveSource(transport=FakeTransport(), runner=runner)
        with self.assertRaises(ValueError):
            source.write(session="work")
        self.assertEqual([], calls)

    def test_confirm_false_raises_value_error_before_the_guard_runs(self):
        source = SessionRemoveSource(
            transport=FakeTransport(),
            runner=lambda command, timeout: (_ for _ in ()).throw(AssertionError("must not run")),
        )
        with self.assertRaises(ValueError):
            source.write(session="work", confirm=False)

    def test_refuses_when_unsupervised(self):
        transport = FakeTransport()
        transport.list_panes = lambda session: []
        source = SessionRemoveSource(transport=transport, runner=_guard_runner(state="unknown"))
        with self.assertRaises(SupervisorUnavailable) as caught:
            source.write(session="work", confirm=True)
        self.assertIn("not supervised", str(caught.exception))
        self.assertEqual([], transport.killed)

    def test_refuses_when_a_lane_is_busy(self):
        transport = FakeTransport()
        transport.list_panes = lambda session: []
        source = SessionRemoveSource(
            transport=transport, runner=_guard_runner(lanes_json='[{"name": "free-1", "state": "busy"}]')
        )
        with self.assertRaises(SupervisorUnavailable) as caught:
            source.write(session="work", confirm=True)
        self.assertIn("busy", str(caught.exception))
        self.assertEqual([], transport.killed)

    def test_refuses_when_a_worktree_is_dirty(self):
        transport = FakeTransport()
        transport.list_panes = lambda session: [{"window_name": "free-1", "path": "/wt"}]

        def scripted(command, *, timeout):
            joined = " ".join(command)
            if "session-state" in joined:
                return completed(stdout='{"session": "work", "state": "supervised"}')
            if "lanes.sh" in joined:
                return completed(stdout="[]")
            if "git" in command[0] and "status" in joined:
                return completed(stdout=" M dirty.py\n")
            raise AssertionError(joined)

        source = SessionRemoveSource(transport=transport, runner=scripted)
        with self.assertRaises(SupervisorUnavailable) as caught:
            source.write(session="work", confirm=True)
        self.assertIn("/wt", str(caught.exception))
        self.assertEqual([], transport.killed)

    def test_happy_path_writes_the_audit_event_then_kills_via_the_stub(self):
        transport = FakeTransport()
        transport.list_panes = lambda session: []
        calls = []

        def scripted(command, *, timeout):
            calls.append(list(command))
            return _guard_runner()(command, timeout=timeout)

        source = SessionRemoveSource(transport=transport, runner=scripted)
        result = source.write(session="work", confirm=True)
        self.assertTrue(result["removed"])
        self.assertEqual("work", result["session"])
        self.assertTrue(result["guard"]["safe_to_remove"])
        self.assertEqual(["work"], transport.killed)
        # audit event recorded before the kill: the ledger write is a
        # scripted `runner` call, and the kill is the (separately tracked)
        # transport call -- both happened, in that order relative to the
        # guard read that authorized them.
        self.assertTrue(any("record-session-event" in " ".join(c) for c in calls))


if __name__ == "__main__":
    unittest.main()
