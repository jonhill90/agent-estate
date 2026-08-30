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

from transport import TmuxTransport


HERE = Path(__file__).resolve().parent

DEFAULT_TIMEOUT_SECONDS = 60

# session_send drives a live agent turn through `supervisord send`, not a
# shell script -- DEFAULT_TIMEOUT_SECONDS above (60s) is sized for lanes.sh/
# digest.sh/bootstrap-session.sh, not for a Claude Code turn that can run
# tools and edit code. Sized past agent.DefaultTimeout (15 minutes, Go side)
# with headroom, so the SUBPROCESS RUNNER never kills `supervisord send`
# before it can observe its own deadline and report -- a Python-side kill
# here would produce no JSON at all, the one outcome this source exists to
# avoid (see SessionSendSource.write's own comment on this).
SEND_DEFAULT_TIMEOUT_SECONDS = 960


def _default_supervisord_binary():
    """Where `supervisord send` (agent-supervisor#508) actually lives.

    No prior WriteSource has ever shelled out to the Go binary -- the other
    four all call a shell script or `cli.py`. $AGENT_SUPERVISOR_SUPERVISORD_BIN
    is the explicit override; $AGENT_SUPERVISOR_REPO (the same estate-wide
    "where is this checkout" variable main.go's own defaultClaimScript and
    agent-tui's CLAUDE.md already use) resolves a locally built
    `daemon/supervisord`; bare `supervisord` falls through to PATH as a last
    resort. Never silently substitutes a script or a tmux command -- if none
    of these resolves to a real binary, SessionSendSource.write's own runner
    reports that as FileNotFoundError -> SupervisorUnavailable, the same as
    every other source's missing-script case.
    """
    if env := os.environ.get("AGENT_SUPERVISOR_SUPERVISORD_BIN"):
        return env
    if repo := os.environ.get("AGENT_SUPERVISOR_REPO"):
        return str(Path(repo) / "daemon" / "supervisord")
    return "supervisord"

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
#
# `transport` is here because an app cannot answer "how is this lane driven"
# without it -- `send-keys`, `acp` and `pi-rpc` are not interchangeable, and
# agent-supervisor#87 makes that answer part of the read surface's job.
LANE_FIELDS = ("lane", "harness", "repo", "pane_id", "transport", "updated_at")
TASK_FIELDS = ("id", "lane", "summary", "status", "created_at", "updated_at")

# `events` carries no credential (unlike `lanes`/`tasks`), but the allowlist
# discipline stays the same: a column added to the table later is omitted by
# default and has to be named to appear here.
EVENT_FIELDS = ("key", "type", "task_id", "status", "payload_path", "created_at", "notified_at", "retry_at", "acked_at")


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


class SessionsSource(ReadSource):
    """Lane states across EVERY tmux session, straight from `sessions.sh --json`.

    agent-tui#13: `LanesSource` above answers for one session because
    `lanes.sh` does, by construction -- and a client that only ever asks
    `lanes` cannot show more than the session it happened to name, which is
    the whole regression that issue traces. `sessions.sh` wraps `lanes.sh`
    per session rather than replacing it (see that script's own header), so
    `lanes`'s existing single-session answer is untouched by this source
    existing.

    Each entry's `supervised` field is agent-supervisor#153's own marker: the
    ledger's `sessions` table, written once by `bootstrap-session.sh` (via
    `cli.py adopt-session`) at the moment it creates a session, independent
    of whether any lane has since been dispatched into it. See `sessions.sh`
    for exactly what that does and does not prove -- in particular, that it
    is a simplified reading of #153's tri-state `session_state()` (the "does
    tmux still have this session" half is moot here by construction, since
    this source only ever lists sessions tmux currently returns).
    """

    name = "sessions"
    summary = (
        "Worker lane states for every tmux session on the box, grouped by "
        "session, each with agent-supervisor#153's 'supervised' signal (the "
        "ledger's adopted-session marker, not a guess). Use when asked what "
        "is happening across sessions, not just one."
    )
    parameters = ()

    def __init__(self, *, script=None, runner=_subprocess_runner, timeout=DEFAULT_TIMEOUT_SECONDS):
        self.script = Path(script) if script else HERE / "sessions.sh"
        self.runner = runner
        self.timeout = timeout

    def read(self):
        command = [str(self.script), "--json"]
        sessions = _decode(self.runner(command, timeout=self.timeout), label="sessions.sh --json")
        if not isinstance(sessions, list):
            raise SupervisorUnavailable("sessions.sh --json returned something that is not a list of sessions")
        return {"sessions": sessions, "count": len(sessions)}


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


class EventsSource(ReadSource):
    """The ledger's own event log, from `cli.py events` -- completion and
    attention events, in the delivery order `list_events` already returns
    them (`ORDER BY created_at, key`).

    This is the answer to "learn a lane changed state without polling
    `lanes.sh` in a loop": a consumer calls once with no `after_key` to see
    the whole log, then passes back each response's `next_cursor` on the
    next call to get only what is new. The cursor is an event `key`, not a
    timestamp -- `created_at` has one-second resolution and the table has no
    other ordering column, so two events in the same second would be
    indistinguishable by time alone. Keys are unique and already the table's
    ordering tiebreaker, so resuming after one is exact.

    Nothing here is filtered to "pending" -- `acked`/`notified` events are
    history a consumer may still want (a TUI showing what a lane recently
    finished), and `cli.py events --due` already exists for the narrower
    "what still needs delivering" question a notifier asks. This source
    answers the other one.
    """

    name = "events"
    summary = (
        "The supervisor's event log: completion and attention events, in "
        "delivery order. Call once with no argument to see the whole log, "
        "then pass the response's next_cursor back as after_key to fetch "
        "only what is new -- this is how to learn a lane changed state "
        "without polling lanes in a loop."
    )
    parameters = (
        {
            "name": "after_key",
            "type": "string",
            "required": False,
            "help": "resume after this event's key (a prior call's next_cursor); omit for the full log.",
        },
    )

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

    def read(self, *, after_key=None):
        command = [self.python, str(self.cli), "--state-dir", str(self.state_dir), "events"]
        rows = _decode(self.runner(command, timeout=self.timeout), label="cli.py events")
        if not isinstance(rows, list):
            raise SupervisorUnavailable("cli.py events returned something that is not a list of events")

        events = [{field: row.get(field) for field in EVENT_FIELDS} for row in rows]

        if after_key is None:
            selected = events
        else:
            index = next((i for i, event in enumerate(events) if event["key"] == after_key), None)
            if index is None:
                raise ValueError(f"events: unknown cursor {after_key!r}")
            selected = events[index + 1 :]

        next_cursor = selected[-1]["key"] if selected else after_key
        return {"events": selected, "count": len(selected), "next_cursor": next_cursor}


class SessionRemoveCheckSource(ReadSource):
    """Would `session_remove` refuse this session, and why -- agent-tui#14.

    A pure read: it evaluates every refusal `session_remove` would evaluate
    and reports the verdict, but takes no lock and changes nothing, so a
    caller can check as many times as it wants before ever calling the write.
    `session_remove` itself re-evaluates the SAME `session_guard.remove_guard`
    fresh at the moment it actually kills something -- see that source's own
    docstring for why a cached result from this one is never trusted there.
    """

    name = "session_remove_check"
    summary = (
        "Would removing this tmux session be refused, and why: is it "
        "supervised, are any of its lanes busy, is every worktree it holds "
        "clean and fully pushed. Call before session_remove; that write "
        "re-checks the same thing fresh rather than trusting this result."
    )
    parameters = (
        {
            "name": "session",
            "type": "string",
            "required": True,
            "help": "tmux session to evaluate for removal.",
        },
    )

    def __init__(
        self,
        *,
        transport=None,
        cli=None,
        python=None,
        state_dir=None,
        lanes_script=None,
        runner=_subprocess_runner,
        timeout=DEFAULT_TIMEOUT_SECONDS,
    ):
        self.transport = transport if transport is not None else TmuxTransport()
        self.cli = Path(cli) if cli else HERE / "cli.py"
        self.python = python or sys.executable or "python3"
        self.state_dir = Path(state_dir) if state_dir else DEFAULT_STATE_DIR
        self.lanes_script = Path(lanes_script) if lanes_script else HERE / "lanes.sh"
        self.runner = runner
        self.timeout = timeout

    def read(self, *, session):
        from session_guard import remove_guard

        return remove_guard(
            session,
            transport=self.transport,
            cli=self.cli,
            python=self.python,
            state_dir=self.state_dir,
            lanes_script=self.lanes_script,
            runner=self.runner,
            timeout=self.timeout,
        )


READ_SOURCES = {
    source.name: source
    for source in (LanesSource, SessionsSource, DigestSource, LedgerSource, EventsSource, SessionRemoveCheckSource)
}


class WriteSource:
    """One MUTATING operation the supervisor can perform, in transport-neutral
    terms -- the write-side mirror of `ReadSource`.

    `mutates` is fixed `True` here rather than left as a default a subclass
    could forget to set, the same way `ReadSource.mutates` is fixed `False`:
    `SupervisorView.__init__` asserts every source built from `WRITE_SOURCES`
    actually has `mutates is True` (and every `READ_SOURCES` source has it
    `False`), so a class registered in the wrong dict fails loudly at
    construction rather than silently over the wire.
    """

    name = None
    summary = ""
    parameters = ()
    mutates = True

    def write(self, **arguments):
        raise NotImplementedError


def _resolve_unambiguous_client(transport, client, *, verb):
    """Fail closed instead of letting tmux guess which client an unscoped
    switch-client/detach-client would act on (agent-supervisor#189).

    `mcp_server.py` has no controlling terminal of its own -- its stdio is a
    pipe -- so when a caller does not name a `client`, the only way to know
    whether tmux's own resolution is safe is to look at how many clients are
    actually attached: exactly one is unambiguous (nothing else for it to
    pick), zero or more than one is not and must refuse rather than let the
    write land on a client nobody named. Returns the tty of the one client
    to act on, for the caller to pass through and later verify against.
    """
    if client is not None:
        return client
    attached = transport.list_clients()
    if len(attached) > 1:
        ttys = ", ".join(sorted(c["tty"] for c in attached))
        raise SupervisorUnavailable(
            f"{verb}: more than one tmux client is attached ({ttys}) and no `client` "
            "was given to say which one -- refusing to guess (agent-supervisor#189)"
        )
    if not attached:
        raise SupervisorUnavailable(f"{verb}: no tmux client is attached")
    return attached[0]["tty"]


class SessionAttachSource(WriteSource):
    """Switch one tmux client's session -- agent-tui#14.

    The guard is existence, and nothing else: attaching to a session is not
    destructive, so there is no supervision/busy/worktree check here the way
    `session_remove` needs one. `SupervisorUnavailable` (not a protocol
    error) for a missing session, because this tool RAN and could not find
    it -- the same "ran and could not" channel `LanesSource` etc. already use
    for a session `lanes.sh` cannot see.

    agent-supervisor#189: `client` names which tmux client moves, as a
    target-client string (a tty path -- see `TmuxTransport.list_clients`).
    Omitted, this refuses outright unless exactly one client is attached
    server-wide (`_resolve_unambiguous_client`) -- previously it silently
    reused whichever client tmux's own "current client" fallback picked,
    which with `mcp_server.py`'s pipe stdio and more than one client
    attached was an arbitrary, unnamed client, reported as success. The
    single-client case is kept as the one case that fallback is provably
    unambiguous in (nothing else for it to resolve to), but this now
    verifies that resolved client's session by reading tmux back, rather
    than trusting the command's exit code either way -- `attached: true`
    is only ever returned once the named client is actually on `session`.
    """

    name = "session_attach"
    summary = "Attach one tmux client's session to `session`. Refuses if tmux has no session by that exact name, or if which client should move is ambiguous."
    parameters = (
        {
            "name": "session",
            "type": "string",
            "required": True,
            "help": "tmux session to attach to, by exact name.",
        },
        {
            "name": "client",
            "type": "string",
            "required": False,
            "help": "tmux target-client (tty path, e.g. /dev/ttys011) to attach. Required when more than one client is attached.",
        },
    )

    def __init__(self, *, transport=None):
        self.transport = transport if transport is not None else TmuxTransport()

    def write(self, *, session, client=None):
        if not self.transport.session_exists(session):
            raise SupervisorUnavailable(f"tmux has no session named {session!r}")
        target = _resolve_unambiguous_client(self.transport, client, verb="session_attach")
        self.transport.switch_client(session, client=client)
        after = {c["tty"]: c["session"] for c in self.transport.list_clients()}
        if after.get(target) != session:
            raise SupervisorUnavailable(
                f"switch-client reported success but {target!r} is not attached to "
                f"{session!r} on read-back"
            )
        return {"session": session, "client": target, "attached": True}


class SessionDetachSource(WriteSource):
    """Detach one tmux client.

    agent-supervisor#189: `client` names which client detaches, as a
    target-client string (a tty path -- see `TmuxTransport.list_clients`).
    Omitted, this refuses unless exactly one client is attached
    server-wide, for the same reason and by the same guard as
    `SessionAttachSource` -- see its docstring. Reports `detached: true`
    only once a read-back confirms the named client is no longer among
    tmux's attached clients.
    """

    name = "session_detach"
    summary = "Detach one tmux client. Refuses if which client should detach is ambiguous."
    parameters = (
        {
            "name": "client",
            "type": "string",
            "required": False,
            "help": "tmux target-client (tty path, e.g. /dev/ttys011) to detach. Required when more than one client is attached.",
        },
    )

    def __init__(self, *, transport=None):
        self.transport = transport if transport is not None else TmuxTransport()

    def write(self, *, client=None):
        target = _resolve_unambiguous_client(self.transport, client, verb="session_detach")
        self.transport.detach_client(client=client)
        still_attached = {c["tty"] for c in self.transport.list_clients()}
        if target in still_attached:
            raise SupervisorUnavailable(
                f"detach-client reported success but {target!r} is still attached on read-back"
            )
        return {"client": target, "detached": True}


class SessionAddSource(WriteSource):
    """Create a new tmux session by wrapping `bootstrap-session.sh` -- agent-tui#14.

    Never passes `--add-lanes`: that flag lets `bootstrap-session.sh` top up
    a session that already exists, which is exactly the safety `session_add`
    relies on that script for -- without it, `bootstrap-session.sh` refuses
    outright if `session` already exists (see its own SAFETY section), and
    that refusal is this tool's only guard against silently modifying a
    session `add` did not create.

    `bootstrap-session.sh` is not a JSON emitter -- its exit code is the
    signal, and a non-zero one carries its own plain-text explanation on
    stderr (reused here via `_tail` rather than re-explained). A zero exit is
    verified, not just trusted: `cli.py session-state` is called afterward to
    report what the operation actually produced, which should read
    `supervised` because `bootstrap-session.sh` calls `adopt-session` on the
    branch that creates a session.
    """

    name = "session_add"
    summary = (
        "Create a new tmux session with worker lanes (bootstrap-session.sh). "
        "Refuses if the session already exists -- this never modifies one "
        "that does."
    )
    parameters = (
        {"name": "session", "type": "string", "required": True, "help": "tmux session to create."},
        {"name": "lanes", "type": "integer", "required": False, "help": "total windows including the supervisor."},
        {"name": "agent", "type": "string", "required": False, "help": "command started in each lane."},
        {"name": "cwd", "type": "string", "required": False, "help": "working directory for every window."},
    )

    def __init__(
        self,
        *,
        script=None,
        cli=None,
        python=None,
        state_dir=None,
        runner=_subprocess_runner,
        timeout=DEFAULT_TIMEOUT_SECONDS,
    ):
        self.script = Path(script) if script else HERE / "bootstrap-session.sh"
        self.cli = Path(cli) if cli else HERE / "cli.py"
        self.python = python or sys.executable or "python3"
        self.state_dir = Path(state_dir) if state_dir else DEFAULT_STATE_DIR
        self.runner = runner
        self.timeout = timeout

    def write(self, *, session, lanes=None, agent=None, cwd=None):
        command = [str(self.script), "--session", session]
        if lanes is not None:
            command += ["--lanes", str(lanes)]
        if agent is not None:
            command += ["--agent", agent]
        if cwd is not None:
            command += ["--cwd", cwd]
        completed = self.runner(command, timeout=self.timeout)
        if completed.returncode != 0:
            raise SupervisorUnavailable(
                f"bootstrap-session.sh exited {completed.returncode}: "
                f"{_tail(completed.stderr) or _tail(completed.stdout) or 'no output'}"
            )
        state_command = [
            self.python, str(self.cli), "--state-dir", str(self.state_dir), "session-state", "--session", session,
        ]
        state_payload = _decode(self.runner(state_command, timeout=self.timeout), label="cli.py session-state")
        if not isinstance(state_payload, dict) or "state" not in state_payload:
            raise SupervisorUnavailable("cli.py session-state returned something that is not a session-state object")
        return {
            "session": session,
            "created": True,
            "state": state_payload["state"],
            "bootstrap_output": _tail(completed.stdout),
        }


class SessionRemoveSource(WriteSource):
    """Kill exactly one tmux session, after re-checking it is safe -- agent-tui#14.

    `confirm` must be the literal `True`; anything else raises `ValueError`
    BEFORE the guard is ever evaluated -- "you forgot to confirm" is a caller
    bug (a protocol-level INVALID_PARAMS over MCP), not a safety verdict, and
    is deliberately a different failure channel from a guard refusal.

    The guard itself (`session_guard.remove_guard`, the same function
    `session_remove_check` reads) is evaluated FRESH here, every call --
    never a cached `session_remove_check` result the caller may be holding,
    because a lane can go busy or a commit can land in the gap between a
    check and this confirm.

    On success: the audit event is written to the ledger BEFORE the session
    is killed, not after -- if the kill itself somehow fails, the record that
    removal was authorized (and what was running at the time) still exists,
    which is the direction #14 actually cares about losing. The kill targets
    `session` by exact name (`TmuxTransport.kill_session`), never
    `kill-server` and never a prefix match.
    """

    name = "session_remove"
    summary = (
        "Remove a tmux session, but only if it is supervised, has no busy "
        "lanes, and every worktree it holds is clean and pushed. Logs the "
        "removal and what authorized it to the ledger before killing "
        "anything. Requires confirm=true."
    )
    parameters = (
        {"name": "session", "type": "string", "required": True, "help": "tmux session to remove, by exact name."},
        {
            "name": "confirm",
            "type": "boolean",
            "required": True,
            "help": "must be true; see session_remove_check first for what will be evaluated.",
        },
    )

    def __init__(
        self,
        *,
        transport=None,
        cli=None,
        python=None,
        state_dir=None,
        lanes_script=None,
        runner=_subprocess_runner,
        timeout=DEFAULT_TIMEOUT_SECONDS,
    ):
        self.transport = transport if transport is not None else TmuxTransport()
        self.cli = Path(cli) if cli else HERE / "cli.py"
        self.python = python or sys.executable or "python3"
        self.state_dir = Path(state_dir) if state_dir else DEFAULT_STATE_DIR
        self.lanes_script = Path(lanes_script) if lanes_script else HERE / "lanes.sh"
        self.runner = runner
        self.timeout = timeout

    def write(self, *, session, confirm=None):
        if confirm is not True:
            raise ValueError(
                "session_remove requires confirm=true, naming exactly what will be destroyed "
                "-- see session_remove_check first"
            )
        from session_guard import remove_guard

        guard = remove_guard(
            session,
            transport=self.transport,
            cli=self.cli,
            python=self.python,
            state_dir=self.state_dir,
            lanes_script=self.lanes_script,
            runner=self.runner,
            timeout=self.timeout,
        )
        if not guard["safe_to_remove"]:
            raise SupervisorUnavailable(f"refuses to remove {session}: " + "; ".join(guard["refusals"]))

        event_command = [
            self.python, str(self.cli), "--state-dir", str(self.state_dir),
            "record-session-event", "--session", session, "--event", "removed",
            "--detail", json.dumps(guard, sort_keys=True),
        ]
        _decode(self.runner(event_command, timeout=self.timeout), label="cli.py record-session-event")

        self.transport.kill_session(session)
        return {"session": session, "removed": True, "guard": guard}


class SessionSendSource(WriteSource):
    """Send one ad-hoc message to an EXISTING agent session -- agent-supervisor#508.

    This is the capability agent-tui's SPEC-shell.md S7 is blocked on: none
    of the other nine sources (six reads, three writes before this one) nor
    `supervisord run` (shaped for fresh ledger-task dispatch, `-task ID
    -brief FILE`, not "continue thread T") can resume an existing session.
    `supervisord send -session-id ID -message TEXT` (daemon/cmd/supervisord,
    built for this issue) is the missing half -- this class is its Python
    write-source wrapper, the SAME shape `SessionAddSource` above already
    takes: shell out via `self.runner`, decode the result, never trust an
    exit code alone.

    **Why NOT `_decode()`:** every other write in this file treats a
    non-zero exit as an opaque failure (`_decode` raises before even
    looking at stdout). `supervisord send` deliberately uses THREE exit
    codes (0 delivered, 1 failed, 3 unknown/timeout) and prints a
    `{"status": ...}` JSON object on stdout for all three, because the
    distinction between "failed" and "unknown, not confirmed" is the whole
    point of daemon/internal/sendmsg (agent-supervisor#488: a timeout must
    never be stamped as a definite failure). Using `_decode()` here would
    throw that distinction away before this method ever saw it. So this
    parses stdout directly, regardless of exit code, and only falls back to
    a bare `_tail(stderr)` message if `supervisord send` produced no JSON
    at all (a crash before it could report on itself).

    `write()` still has the same two-outcome contract every WriteSource
    has -- a dict on success or a raised SupervisorUnavailable, nothing
    else, per `SupervisorView`'s own registration assert -- so "unknown"
    is a raise too, exactly as `supervisord run`'s own CLI output collapses
    a timeout and a real failure into one "OUTCOME: NOT DELIVERED" at its
    own layer. What is preserved is the MESSAGE: an unknown outcome says so
    explicitly ("outcome UNKNOWN, not failed") rather than reusing the
    failed-send wording, so a caller reading the raised text (the only
    thing an MCP client sees) can still tell the two apart and react
    differently -- retry a real failure, but not stack a second turn on
    top of one that may still be running.
    """

    name = "session_send"
    summary = (
        "Send an ad-hoc message to an EXISTING agent session (--resume under "
        "the hood, via supervisord send). Never tmux send-keys. Reports "
        "delivered, failed, or unknown -- a send that cannot be confirmed is "
        "never reported as delivered."
    )
    parameters = (
        {
            "name": "session_id",
            "type": "string",
            "required": True,
            "help": "the harness's own session id to resume (e.g. Claude Code's session_id) -- not a tmux session name.",
        },
        {"name": "message", "type": "string", "required": True, "help": "the message to send to that session."},
        {"name": "harness", "type": "string", "required": False, "help": "which vendor CLI drives the session: claude | codex. Defaults to claude."},
        {"name": "cwd", "type": "string", "required": False, "help": "working directory for the agent turn."},
        {"name": "model", "type": "string", "required": False, "help": "model override."},
        {"name": "timeout", "type": "integer", "required": False, "help": "per-turn timeout in seconds. Defaults to the daemon's own 15-minute default."},
    )

    def __init__(self, *, binary=None, runner=_subprocess_runner, timeout=SEND_DEFAULT_TIMEOUT_SECONDS):
        self.binary = binary or _default_supervisord_binary()
        self.runner = runner
        self.timeout = timeout

    def write(self, *, session_id, message, harness=None, cwd=None, model=None, timeout=None):
        command = [self.binary, "send", "-session-id", session_id, "-message", message]
        if harness:
            command += ["-harness", harness]
        if cwd:
            command += ["-cwd", cwd]
        if model:
            command += ["-model", model]
        # The Python-side runner timeout must outlive the Go side's own
        # deadline -- see SEND_DEFAULT_TIMEOUT_SECONDS's own comment. If the
        # caller asks for a longer -timeout than this instance's default,
        # widen the runner's own ceiling to match, with the same headroom.
        runner_timeout = self.timeout
        if timeout is not None:
            command += ["-timeout", f"{int(timeout)}s"]
            runner_timeout = max(self.timeout, int(timeout) + 60)

        completed = self.runner(command, timeout=runner_timeout)
        stdout = (completed.stdout or "").strip()
        if not stdout:
            raise SupervisorUnavailable(
                f"supervisord send exited {completed.returncode} with no output: "
                f"{_tail(completed.stderr) or 'no stderr'}"
            )
        try:
            # supervisord send prints exactly one JSON object; take the last
            # line defensively in case anything upstream of it ever writes
            # to stdout first.
            report = json.loads(stdout.splitlines()[-1])
        except json.JSONDecodeError as error:
            raise SupervisorUnavailable(
                f"supervisord send did not return JSON ({error}): {_tail(stdout, 200)}"
            ) from error

        status = report.get("status")
        if status == "delivered":
            return {
                "session_id": report.get("session_id") or session_id,
                "delivered": True,
                "turns": report.get("turns", 0),
                "cost_usd": report.get("cost_usd", 0.0),
            }
        if status == "unknown":
            raise SupervisorUnavailable(
                "send outcome UNKNOWN, not failed -- a turn did not confirm before its "
                f"deadline (agent-supervisor#488): {report.get('error', 'no detail')}"
            )
        # status == "failed" (sendmsg.StatusFailed), or an unrecognised value
        # from a future daemon build this Python has not caught up with --
        # either way, never laundered into delivered=True.
        raise SupervisorUnavailable(f"send failed: {report.get('error') or _tail(completed.stderr) or 'no detail'}")


# Five writes, and ONLY these five -- not the general write capability #198's
# own read-surface PR (and the docstring this replaces) refused to build.
# That refusal stands: `dispatch`/`merge` are still excluded, and still for
# the three reasons originally written here --
#
#   * A caller identity. `cli.py`'s `_verify_caller` authenticates a lane by
#     the pane nonce it can only know from inside its own pane. An MCP client
#     is not in a pane and has no such secret, so `dispatch`/`merge` would
#     arrive unauthenticated.
#   * A blast-radius bound. `dispatch.sh` claims a lane, writes a brief and
#     sends keys into a live terminal; `#184`/`#209` are both about two
#     dispatchers racing for one lane. A tool any consuming agent may call on
#     its own initiative is a third dispatcher.
#   * An audit trail that survives the client. The ledger records who
#     dispatched by pid; an MCP call has no pid on this machine.
#
# agent-tui#14's four are a NARROWER, differently-shaped risk, which is why
# they are safe to add despite the above still holding for dispatch/merge:
#
#   * No lane-claim race. None of the four claims a lane the way
#     `dispatch.sh`/`claim.sh` do -- there is nothing here for a second
#     dispatcher to race against.
#   * Every guard is state-based and re-evaluated fresh at call time, never a
#     cached prior answer. `session_attach` checks `session_exists` at the
#     moment it runs; `session_remove` re-runs `session_guard.remove_guard`
#     at the moment it runs, not whatever `session_remove_check` last
#     reported.
#   * The blast radius is exactly one named session, addressed by exact
#     name. Never `kill-server`, never a prefix match -- see
#     `TmuxTransport.kill_session` and `.session_exists`'s own `=name`
#     discipline, which every one of these four targets through.
#   * `session_remove` writes an audit event to the ledger BEFORE acting --
#     `Ledger.record_session_event` -- so "log every removal with what was
#     running at the time" (the issue's own words) is satisfied, not merely
#     implied by the ledger's general event log.
#
# `session_send` (agent-supervisor#508) is a DIFFERENT risk shape from the
# four above, and is called out here rather than folded into the bullets
# above as if it were another `session_attach`:
#
#   * It is the first write in this file that runs a live agent turn, not a
#     tmux control-plane operation -- real cost and real side effects (the
#     turn can edit code, same as any other Claude Code turn), not a
#     metadata change to who is attached to what.
#   * It is still bounded the same way the four above are: one exact,
#     caller-supplied `session_id`, resumed via `--resume` (never an
#     implicit "current" session the way the pre-#189 attach/detach bug
#     picked an arbitrary client) -- there is no session-name ambiguity to
#     resolve, because the caller must already know which session it means.
#   * No lane-claim race either: sending to an existing session claims
#     nothing new in the ledger, the same "nothing here for a second
#     dispatcher to race against" property `session_attach`/`session_detach`
#     already have.
#   * Delivery is OBSERVED, not inferred, same as `supervisord run` --
#     `daemon/internal/sendmsg` never reports delivered unless the
#     underlying process actually returned a result, and a timeout is
#     surfaced as unknown, never laundered into delivered=True. That is the
#     one property that makes this safe to add: a caller cannot get a false
#     "yes, that was sent."
#
# This is still additive to the ten-source MCP surface, not a side door: a
# properly typed, properly registered WriteSource, the same
# `WRITE_SOURCES`/`SupervisorView.__init__` assert every other write already
# goes through -- see `SessionSendSource`'s own docstring for why it does
# not (and structurally cannot, per that assert) bypass this mechanism.
WRITE_SOURCES = {
    source.name: source
    for source in (
        SessionAttachSource,
        SessionDetachSource,
        SessionAddSource,
        SessionRemoveSource,
        SessionSendSource,
    )
}


def build_source(name, **options):
    """Instantiate a read source by name. Unknown names are refused, not guessed."""
    if name not in READ_SOURCES:
        raise KeyError(f"unknown supervisor source: {name!r} (known: {', '.join(sorted(READ_SOURCES))})")
    return READ_SOURCES[name](**options)


def build_write_source(name, **options):
    """Instantiate a write source by name. Unknown names are refused, not guessed."""
    if name not in WRITE_SOURCES:
        raise KeyError(f"unknown supervisor write: {name!r} (known: {', '.join(sorted(WRITE_SOURCES))})")
    return WRITE_SOURCES[name](**options)


class SupervisorView:
    """The whole surface as one object -- what a transport is handed.

    Sources are instantiated once and reused, so a transport holds no
    construction knowledge. `read(name, **arguments)` and
    `write(name, **arguments)` are the two halves of the interface;
    `call(name, **arguments)` looks up whichever registry actually holds
    `name` so a transport (`mcp_server.py`) needs exactly one call site
    rather than an if/else that has to know both registries.

    Construction asserts the one invariant this whole split depends on:
    every source built from `READ_SOURCES` has `mutates is False`, and every
    source built from `WRITE_SOURCES` has `mutates is True`. A source
    registered in the wrong dict -- by mistake, or by a future edit that
    forgot to flip `mutates` -- fails loudly HERE, at construction, instead
    of silently reaching `mcp_server.py`'s `tool_definitions`, which trusts
    this assertion rather than re-checking it.
    """

    def __init__(self, sources=None, write_sources=None):
        self.sources = sources if sources is not None else [build_source(name) for name in READ_SOURCES]
        self.write_sources = (
            write_sources if write_sources is not None else [build_write_source(name) for name in WRITE_SOURCES]
        )
        for source in self.sources:
            if source.mutates is not False:
                raise ValueError(
                    f"{source.name!r} is registered as a read source but mutates={source.mutates!r} -- "
                    "it belongs in WRITE_SOURCES, not READ_SOURCES"
                )
        for source in self.write_sources:
            if source.mutates is not True:
                raise ValueError(
                    f"{source.name!r} is registered as a write source but mutates={source.mutates!r} -- "
                    "it belongs in READ_SOURCES, not WRITE_SOURCES"
                )
        self._by_name = {source.name: source for source in self.sources}
        self._write_by_name = {source.name: source for source in self.write_sources}

    def describe(self):
        return [
            {"name": s.name, "summary": s.summary, "parameters": list(s.parameters), "mutates": s.mutates}
            for s in (*self.sources, *self.write_sources)
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

    def write(self, name, **arguments):
        source = self._write_by_name.get(name)
        if source is None:
            raise KeyError(f"unknown supervisor write: {name!r} (known: {', '.join(sorted(self._write_by_name))})")
        known = {parameter["name"] for parameter in source.parameters}
        unexpected = sorted(set(arguments) - known)
        if unexpected:
            raise ValueError(f"{name}: unknown argument(s) {', '.join(unexpected)}")
        return source.write(**arguments)

    def call(self, name, **arguments):
        """Route to whichever registry actually holds `name`.

        The one call site `mcp_server.py` needs: it does not have to know
        which dict a tool came from, only that `SupervisorView` does.
        """
        if name in self._by_name:
            return self.read(name, **arguments)
        if name in self._write_by_name:
            return self.write(name, **arguments)
        known = sorted(set(self._by_name) | set(self._write_by_name))
        raise KeyError(f"unknown supervisor tool: {name!r} (known: {', '.join(known)})")
