"""`claude -p` transport; a sibling to `pi_transport.py` and `acp_transport.py`,
not a replacement for either (agent-supervisor#171).

WHY THIS IS SIMPLER THAN THE OTHER TWO: `pi --mode rpc` and ACP are both
line-delimited JSON-RPC peers over a long-lived process -- a `prompt` (or
`session/prompt`) call either blocks for a synchronous acknowledgement and
then has to be awaited out to an asynchronous `agent_settled`/`stopReason`
event (pi), or blocks directly for the settled result (ACP), and either way
the process is meant to keep running between calls so a session can be
resumed without re-spawning it. `claude -p --output-format json` is not that
kind of surface at all: `claude` is invoked, runs the turn to completion, and
exits -- there is no persistent process to hold a connection to between
calls, only a `session_id` on disk that a *later* invocation can resume with
`--resume`. So there is no reader thread, no pending-request table, and no
"settled or connection closed" race to adjudicate here: `subprocess.run` with
a timeout either returns valid JSON with a real result, or it doesn't, and
that is the whole contract.

Measured live (see the PR description for the exact transcript):
`claude -p --output-format json --session-id <uuid> "<first turn>"` returns a
single JSON object on stdout once the turn is done --
`{"is_error":false,...,"session_id":"<uuid>","result":"<final text>",
"subtype":"success",...}` -- and a second, independent process,
`claude -p --output-format json --resume <uuid> "<next turn>"`, replies aware
of the first turn's content, confirming `--resume` genuinely continues the
same conversation across process boundaries, exactly the property
`ClaudePrintAdapter.assign_task` needs.
"""

from __future__ import annotations

import json
import subprocess
from pathlib import Path

DEFAULT_TIMEOUT_SECONDS = 900
# A `claude -p` turn can run tools, read files, edit code and open a PR --
# minutes, not seconds. Generous on purpose, same posture
# `pi_transport.DEFAULT_PROMPT_TIMEOUT_SECONDS` takes for the same reason; a
# caller that wants a shorter leash passes its own `timeout`.


class ClaudePrintTimeoutError(TimeoutError):
    pass


class ClaudePrintProtocolError(RuntimeError):
    """`claude -p` exited, but stdout was not the single JSON result object
    the `--output-format json` contract promises -- a crash, a version whose
    output shape changed, or a non-zero exit before any turn ran."""

    pass


class ClaudePrintTransport:
    """One `claude -p` invocation's worth of configuration -- `cwd`, which
    session to resume (if any), and which model to run. Nothing here starts
    a process at construction time; `run` spawns one fresh subprocess per
    call and lets it exit, mirroring `PiRPCTransport.spawn`'s one-process-
    per-CLI-invocation shape for `ACPAdapter`/`PiRPCAdapter`, not
    `PiRPCTransport`'s own persistent-process shape.
    """

    def __init__(
        self, *, cwd, session_id=None, model=None, claude_bin="claude", timeout=None,
        dangerously_skip_permissions=True,
    ):
        self.cwd = cwd
        self.session_id = session_id
        self.model = model
        self.claude_bin = claude_bin
        self.timeout = timeout if timeout is not None else DEFAULT_TIMEOUT_SECONDS
        # Same convention `harness/claude.sh`'s own `HARNESS_LAUNCH_CMD` uses
        # for every OTHER claude lane in this estate -- measured live
        # dispatching agent-supervisor#232 through this transport before this
        # flag existed: `claude -p` refused to read the brief file (it lives
        # outside the lane's own worktree, by this estate's own convention --
        # see dispatch.sh's own header) and returned a permission-refusal
        # result instead of doing the work. Default True, not opt-in, because
        # a lane that cannot read its own brief is not a lane that can do
        # dispatch-and-collect work at all.
        self.dangerously_skip_permissions = dangerously_skip_permissions

    @classmethod
    def spawn(cls, *, cwd, session_id=None, model=None, **kwargs):
        """Named to match `PiRPCTransport.spawn`'s call shape (a keyword-only
        factory `transport_factory(cwd=..., session=...)` calls), even though
        no process is actually launched until `run`."""
        return cls(cwd=cwd, session_id=session_id, model=model, **kwargs)

    def _base_command(self):
        command = [self.claude_bin, "-p", "--output-format", "json"]
        if self.model:
            command += ["--model", self.model]
        if self.dangerously_skip_permissions:
            command += ["--dangerously-skip-permissions"]
        return command

    def run(self, prompt):
        """Run one turn to completion and return the parsed result object.

        First call (no `session_id` recorded yet) mints a fresh session with
        `--session-id <uuid>` the caller supplies; every later call resumes
        it with `--resume <uuid>`. Raises `ClaudePrintTimeoutError` if the
        process does not exit within `self.timeout`, and
        `ClaudePrintProtocolError` if it exits without a well-formed JSON
        result -- never returns a success-shaped value for either failure,
        the same fail-closed contract `PiRPCTransport.send_literal` and
        `ACPTransport.send_literal` both keep.
        """
        command = self._base_command()
        if self.session_id:
            command += ["--resume", self.session_id]
        else:
            raise ClaudePrintProtocolError("ClaudePrintTransport.run requires a session_id (mint one before the first call)")
        command.append(prompt)
        try:
            completed = subprocess.run(
                command,
                cwd=self.cwd,
                capture_output=True,
                text=True,
                timeout=self.timeout,
            )
        except subprocess.TimeoutExpired as exc:
            raise ClaudePrintTimeoutError(
                f"claude -p did not exit within {self.timeout}s (session {self.session_id})"
            ) from exc
        stdout = (completed.stdout or "").strip()
        if not stdout:
            raise ClaudePrintProtocolError(
                f"claude -p exited {completed.returncode} with no stdout -- stderr: {(completed.stderr or '').strip()[:2000]}"
            )
        try:
            result = json.loads(stdout)
        except json.JSONDecodeError as exc:
            raise ClaudePrintProtocolError(f"claude -p stdout was not valid JSON: {stdout[:2000]!r}") from exc
        if result.get("type") != "result" or "session_id" not in result:
            raise ClaudePrintProtocolError(f"claude -p returned an unexpected shape: {result!r}")
        return result

    def run_detached(self, prompt, *, log_path=None):
        """Start one turn and return IMMEDIATELY, without waiting for it.

        agent-supervisor#278. `run()` above waits for the whole turn, so a
        dispatch does not return until the WORK finishes -- and the caller
        cannot tell "still working" from "hung". Measured cost on 2026-08-17:
        two lanes died at the 900s cap, `as308-fix-author-resolution` sat at
        `delivery_pending` for over two hours with 411 lines of uncommitted
        work in its worktree, and every one of those lanes had to be salvaged
        by hand. Dispatch must return once the lane HAS the brief; whether the
        work then succeeds is a separate question answered by the ledger.

        Returns the Popen object so the caller can record the pid. The child
        is put in its own process group (`start_new_session=True`) so that a
        timeout, a Ctrl-C, or the dispatcher exiting does not take the work
        with it -- the exact way the earlier corpus batch was lost.

        stdout/stderr go to `log_path` when given, never to a pipe: an
        unread pipe fills its buffer and blocks the child, which would
        reintroduce the hang this method exists to remove.
        """
        command = self._base_command()
        if self.session_id:
            command += ["--resume", self.session_id]
        else:
            raise ClaudePrintProtocolError(
                "ClaudePrintTransport.run_detached requires a session_id (mint one before the first call)"
            )
        command.append(prompt)
        if log_path is not None:
            log_path = Path(log_path)
            log_path.parent.mkdir(parents=True, exist_ok=True)
            sink = open(log_path, "ab", buffering=0)
        else:
            sink = subprocess.DEVNULL
        try:
            return subprocess.Popen(
                command,
                cwd=self.cwd,
                stdout=sink,
                stderr=subprocess.STDOUT if log_path is not None else subprocess.DEVNULL,
                stdin=subprocess.DEVNULL,
                start_new_session=True,
            )
        finally:
            if log_path is not None:
                sink.close()

    def start_session(self, session_id, prompt):
        """The FIRST call for a brand-new session -- unlike `run`, this mints
        the session with `--session-id <uuid>` rather than resuming one, and
        is the fail-closed handshake `ClaudePrintAdapter.register_lane` uses
        to prove `claude` is actually reachable before a lane is registered
        at all, the same role `PiRPCAdapter.register_lane`'s `get_state` call
        plays.
        """
        command = self._base_command()
        command += ["--session-id", session_id, prompt]
        try:
            completed = subprocess.run(
                command,
                cwd=self.cwd,
                capture_output=True,
                text=True,
                timeout=self.timeout,
            )
        except subprocess.TimeoutExpired as exc:
            raise ClaudePrintTimeoutError(
                f"claude -p did not exit within {self.timeout}s (session {session_id})"
            ) from exc
        stdout = (completed.stdout or "").strip()
        if not stdout:
            raise ClaudePrintProtocolError(
                f"claude -p exited {completed.returncode} with no stdout -- stderr: {(completed.stderr or '').strip()[:2000]}"
            )
        try:
            result = json.loads(stdout)
        except json.JSONDecodeError as exc:
            raise ClaudePrintProtocolError(f"claude -p stdout was not valid JSON: {stdout[:2000]!r}") from exc
        if result.get("type") != "result" or not result.get("session_id"):
            raise ClaudePrintProtocolError(f"claude -p returned an unexpected shape: {result!r}")
        self.session_id = result["session_id"]
        return result

    def terminate(self):
        """No-op: `run`/`start_session` already waited the subprocess out to
        exit before returning. Present only so `ClaudePrintAdapter` can call
        it unconditionally in a `finally`, the same shape `PiRPCAdapter` and
        `ACPAdapter` both rely on for their own transports."""
        pass

    def close(self):
        self.terminate()
