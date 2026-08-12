"""Read-only view of the supervisor: one interface, several backing sources.

agent-dotfiles#198 asks for the supervisor to be consumable by any harness,
not just the one that happens to run inside Jon's tmux session. MCP is the
transport that answers that -- Claude Code, Codex and Copilot all speak it.
This module is deliberately NOT that transport. It is the seam the transport
sits on.

The split follows the shape the estate already settled on twice: `verdict.py`
(a `VerdictSource` base, concrete sources, a `SOURCES` registry, one caller
that knows none of them) and `harness/*.sh` (one file per harness, a generic
`lanes.sh` that names none of them). Same rule here -- `mcp_server.py` holds
no knowledge of `lanes.sh`, `digest.sh` or the ledger, and this file holds no
knowledge of MCP. Deleting `mcp_server.py` leaves every script in this
directory working exactly as it does today; that is the test of whether the
seam is in the right place, and it is asserted in
`tests/supervisor/test_supervisor_view.py`.

Every source WRAPS an existing entry point rather than reimplementing it.
`lanes.sh`, `digest.sh` and `cli.py status` stay the single implementation of
their own answers, so there is no second behaviour to drift -- the failure
mode skills#158 hit and #187 hit again in a different place.

## Fail loud, never empty

Every source raises `SupervisorUnavailable` when it cannot see its backing
store. It never returns an empty list or an empty object to mean "I could not
look". This is the one rule this module exists to enforce: a tool that answers
`[]` when the session is gone, jq is missing, or the ledger is corrupt is
indistinguishable from a genuinely quiet estate, and a caller cannot tell a
healthy idle from a blind instrument. `digest.sh`'s own header states the same
contract for itself ("an empty `prs` list and an unreachable GitHub must not
look the same"); this module carries it across the process boundary.

Note the deliberate difference from `verdict.py`, which fails CLOSED to
"unknown" rather than raising. There, a caller is iterating several sources
and one blowing up must not take the others down. Here there is exactly one
source per call and the caller is a remote agent, so the failure has to reach
it as a failure.

## Read only, and secrets never leave

Only reads are exposed. `Ledger` rows carry `nonce` (lanes) and `pane_nonce`
(tasks), which are the credentials `cli.py`'s `_verify_caller` authenticates
callers with -- publishing them over any transport would hand every consumer
the ability to impersonate a lane. `LedgerSource` therefore projects rows
through an explicit field ALLOWLIST, not a denylist: a column added to the
schema later is omitted by default and has to be named to appear.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path


HERE = Path(__file__).resolve().parent

DEFAULT_TIMEOUT_SECONDS = 60

# Mirrors `cli.py`'s DEFAULT_STATE so a view and the CLI it shells out to
# always read the same ledger without the caller spelling either out.
DEFAULT_STATE_DIR = Path(
    os.environ.get("AGENT_SUPERVISOR_STATE_DIR", Path.home() / ".local/state/agent-dotfiles-supervisor")
)

# The task statuses `Ledger.lane_available` treats as closed, and therefore
# the complement of "outstanding". Duplicated here as a constant rather than
# re-deriving the idea, and pinned against `core.py`'s own SQL by
# `test_supervisor_view.py` so the two cannot drift silently.
CLOSED_TASK_STATUSES = ("complete", "failed", "cancelled")

# Explicit allowlists -- see the module docstring. `nonce`/`pane_nonce` are
# authentication material and are absent from both by construction.
LANE_FIELDS = ("lane", "harness", "repo", "pane_id", "updated_at")
TASK_FIELDS = ("id", "lane", "summary", "status", "created_at", "updated_at")


class SupervisorUnavailable(RuntimeError):
    """A source could not see its backing store.

    Raised, never swallowed into an empty result. The message names the
    command that failed and what it said, because the consumer is a remote
    agent that cannot look at the machine itself.
    """


def _subprocess_runner(command, *, timeout):
    """Run `command` and hand back the CompletedProcess, errors included.

    Deliberately not `check=True`: a non-zero exit carries stderr that the
    caller needs to put in front of a remote agent, and `CalledProcessError`
    stringifies to the exit code alone.
    """
    try:
        return subprocess.run(command, capture_output=True, text=True, timeout=timeout)
    except FileNotFoundError as error:
        raise SupervisorUnavailable(f"{command[0]}: not found ({error})") from error
    except PermissionError as error:
        raise SupervisorUnavailable(f"{command[0]}: not executable ({error})") from error
    except subprocess.TimeoutExpired as error:
        raise SupervisorUnavailable(f"{command[0]}: timed out after {timeout}s") from error


def _tail(text, limit=600):
    text = (text or "").strip()
    return text if len(text) <= limit else "..." + text[-limit:]


def _decode(completed, *, label):
    """Turn a CompletedProcess into parsed JSON, or raise.

    Three distinct blindnesses, all of which would otherwise arrive as an
    empty answer: a non-zero exit (the session does not exist, jq is missing,
    the ledger would not open), a zero-byte stdout (the shape `digest.sh`
    produced for weeks when jq was absent, before its own guard landed), and
    output that is not JSON at all.
    """
    if completed.returncode != 0:
        raise SupervisorUnavailable(
            f"{label} exited {completed.returncode}: {_tail(completed.stderr) or _tail(completed.stdout) or 'no output'}"
        )
    if not (completed.stdout or "").strip():
        raise SupervisorUnavailable(f"{label} exited 0 but produced no output")
    try:
        return json.loads(completed.stdout)
    except json.JSONDecodeError as error:
        raise SupervisorUnavailable(f"{label} did not return JSON ({error}): {_tail(completed.stdout, 200)}") from error


class ReadSource:
    """One question the supervisor can answer, in transport-neutral terms.

    `name`, `summary` and `parameters` are what a transport needs to publish
    the source; `read()` is what it needs to answer. Nothing here is
    MCP-shaped -- `mcp_server.py` translates `parameters` into JSON Schema,
    and a second consumer (a REST wrapper, a CLI) would translate it
    differently without this file changing.

    `mutates` exists so the exposure rule is a property of the source rather
    than a fact a transport has to remember. See `WRITE_SOURCES` below.
    """

    name = None
    summary = ""
    parameters = ()
    mutates = False

    def read(self, **arguments):
        raise NotImplementedError


class LanesSource(ReadSource):
    """Lane states, straight from `lanes.sh --json`.

    `lanes.sh` exits 1 when the session does not exist, which is explicitly
    NOT "no lanes" -- its own header says so. That exit becomes an error here
    rather than an empty list.
    """

    name = "lanes"
    summary = (
        "Worker lane states in the supervisor tmux session: free, busy, hung, "
        "blocked, unsent, dead, service or unknown. Use before dispatching or "
        "when asked what the lanes are doing."
    )
    parameters = (
        {
            "name": "session",
            "type": "string",
            "required": False,
            "help": "tmux session to inspect; defaults to the supervisor session.",
        },
    )

    def __init__(self, *, script=None, runner=_subprocess_runner, timeout=DEFAULT_TIMEOUT_SECONDS):
        self.script = Path(script) if script else HERE / "lanes.sh"
        self.runner = runner
        self.timeout = timeout

    def read(self, *, session=None):
        command = [str(self.script), "--json"]
        if session:
            command.append(session)
        rows = _decode(self.runner(command, timeout=self.timeout), label="lanes.sh --json")
        if not isinstance(rows, list):
            raise SupervisorUnavailable("lanes.sh --json returned something that is not a list of lanes")
        return {"lanes": rows, "count": len(rows)}


class DigestSource(ReadSource):
    """The whole-estate digest, straight from `digest.sh --json`.

    `digest.sh` may emit a PARTIAL digest with the failures named in
    `errors` and `ok: false`, and exit 0. That payload is passed through
    intact rather than being raised on: the readable half is still worth
    having, and the unreadable half is named in the payload the consumer
    receives. Only a digest that could not be built at all -- non-zero exit,
    no output, unparseable, or a payload that is not digest-shaped -- is an
    error.
    """

    name = "digest"
    summary = (
        "One-shot estate status: watchdog, poller, lane counts by state, open "
        "PRs with CI and verdict, and recently merged work. Use to answer "
        "'what is happening right now' without running several commands."
    )
    parameters = ()

    def __init__(self, *, script=None, runner=_subprocess_runner, timeout=DEFAULT_TIMEOUT_SECONDS):
        self.script = Path(script) if script else HERE / "digest.sh"
        self.runner = runner
        self.timeout = timeout

    def read(self):
        payload = _decode(self.runner([str(self.script), "--json"], timeout=self.timeout), label="digest.sh --json")
        if not isinstance(payload, dict):
            raise SupervisorUnavailable("digest.sh --json returned something that is not a digest object")
        if "checked" not in payload and "errors" not in payload:
            raise SupervisorUnavailable("digest.sh --json returned an object with no digest fields in it")
        return payload


class LedgerSource(ReadSource):
    """Open tasks and lane availability, from `cli.py status`.

    Two reductions on the raw status, both deliberate:

    * Tasks are filtered to the OUTSTANDING ones, using the same status set
      `Ledger.lane_available` and the `one_open_task_per_lane` index use. A
      completed task is history; a consumer asking "what is in flight" pays
      tokens for every row it gets back.
    * Rows are projected through `LANE_FIELDS`/`TASK_FIELDS`. That drops the
      pane nonces, which are credentials, and it drops the columns nobody
      consuming this needs.

    `available` is computed from the same rule rather than read back per lane
    so that one `status` call answers the whole question -- the tri-state
    `Ledger.lane_available` returns collapses here because every lane in this
    output is by definition registered.
    """

    name = "ledger"
    summary = (
        "Supervisor ledger: registered lanes with whether each is free to take "
        "work, and every task still outstanding. The authority on lane "
        "availability -- a tmux window name is not."
    )
    parameters = ()

    def __init__(
        self,
        *,
        cli=None,
        python=None,
        state_dir=None,
        runner=_subprocess_runner,
        timeout=DEFAULT_TIMEOUT_SECONDS,
    ):
        self.cli = Path(cli) if cli else HERE / "cli.py"
        self.python = python or sys.executable or "python3"
        self.state_dir = Path(state_dir) if state_dir else DEFAULT_STATE_DIR
        self.runner = runner
        self.timeout = timeout

    def read(self):
        command = [self.python, str(self.cli), "--state-dir", str(self.state_dir), "status"]
        payload = _decode(self.runner(command, timeout=self.timeout), label="cli.py status")
        if not isinstance(payload, dict) or "lanes" not in payload or "tasks" not in payload:
            raise SupervisorUnavailable("cli.py status returned something that is not a ledger status")

        open_tasks = [
            {field: task.get(field) for field in TASK_FIELDS}
            for task in payload["tasks"]
            if task.get("status") not in CLOSED_TASK_STATUSES
        ]
        busy_lanes = {task["lane"] for task in open_tasks}
        lanes = [
            {**{field: lane.get(field) for field in LANE_FIELDS}, "available": lane.get("lane") not in busy_lanes}
            for lane in payload["lanes"]
        ]
        return {
            "lanes": lanes,
            "open_tasks": open_tasks,
            "available_lanes": sorted(lane["lane"] for lane in lanes if lane["available"]),
        }


READ_SOURCES = {source.name: source for source in (LanesSource, DigestSource, LedgerSource)}

# Intentionally empty, and intentionally present.
#
# #198 asks for the read surface first and says dispatch and merge are write
# operations with real blast radius. They are not in this registry because
# the authorisation story is not built, not because there is nowhere to put
# them. What a `WriteSource` needs before it can be added here, none of which
# exists yet:
#
#   * A caller identity. `cli.py`'s `_verify_caller` authenticates a lane by
#     the pane nonce it can only know from inside its own pane. An MCP client
#     is not in a pane and has no such secret, so every write would arrive
#     unauthenticated -- and the whole point of #198 is that the client may be
#     on a work laptop, off this machine entirely.
#   * A blast-radius bound. `dispatch.sh` claims a lane, writes a brief and
#     sends keys into a live terminal; `#184`/`#209` are both about two
#     dispatchers racing for one lane. A tool that any consuming agent may
#     call on its own initiative is a third dispatcher.
#   * An audit trail that survives the client. The ledger records who
#     dispatched by pid; an MCP call has no pid on this machine.
#
# The seam is here so adding one is a class plus an entry, exactly as in
# `verdict.py`. `mcp_server.py` builds its tool list from `READ_SOURCES`
# alone and asserts `mutates is False` on everything it publishes, so adding
# to this dict does not silently expose it.
WRITE_SOURCES = {}


def build_source(name, **options):
    """Instantiate a read source by name. Unknown names are refused, not guessed."""
    if name not in READ_SOURCES:
        raise KeyError(f"unknown supervisor source: {name!r} (known: {', '.join(sorted(READ_SOURCES))})")
    return READ_SOURCES[name](**options)


class SupervisorView:
    """The read surface as one object -- what a transport is handed.

    Sources are instantiated once and reused, so a transport holds no
    construction knowledge. `read(name, **arguments)` is the whole interface.
    """

    def __init__(self, sources=None):
        self.sources = sources if sources is not None else [build_source(name) for name in READ_SOURCES]
        self._by_name = {source.name: source for source in self.sources}

    def describe(self):
        return [
            {"name": s.name, "summary": s.summary, "parameters": list(s.parameters), "mutates": s.mutates}
            for s in self.sources
        ]

    def read(self, name, **arguments):
        source = self._by_name.get(name)
        if source is None:
            raise KeyError(f"unknown supervisor source: {name!r} (known: {', '.join(sorted(self._by_name))})")
        known = {parameter["name"] for parameter in source.parameters}
        unexpected = sorted(set(arguments) - known)
        if unexpected:
            raise ValueError(f"{name}: unknown argument(s) {', '.join(unexpected)}")
        return source.read(**arguments)
