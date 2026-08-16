"""agent-supervisor#189: `session_attach`/`session_detach` over a REAL MCP
pipe, against a REAL isolated tmux server with two real attached clients.

This is deliberately not a unit test against `supervisor_view.py` directly.
The issue's own finding is that the defect lives in the gap between calling
the Python source function (which happens to run with a real tty, so tmux's
implicit "client currently in use" resolution accidentally works) and
calling it the way every real caller does -- over `mcp_server.py`'s stdio,
which is wired to pipes and has no controlling terminal at all. Only driving
this through a subprocess talking JSON-RPC over pipes, exactly the shape
agent-tui's `internal/mcp.Client.Start` uses, exercises that gap.

Isolation, per CLAUDE.md: every tmux server here is a throwaway one, created
under a private `TMUX_TMPDIR` with `TMUX` unset in the child environment.
Nothing in this file ever touches the ambient/default socket, and no verb
here is `kill-server` (`kill_isolated_server` targets ITS OWN socket by
`-L`/`TMUX_TMPDIR`, never the bare command).
"""

from __future__ import annotations

import json
import os
import pty
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
MCP_SERVER = SUPERVISOR_DIR / "mcp_server.py"

TMUX_AVAILABLE = shutil.which("tmux") is not None


def _isolated_env(tmux_tmpdir):
    env = dict(os.environ)
    env["TMUX_TMPDIR"] = tmux_tmpdir
    env.pop("TMUX", None)  # a client's own env leaking in would defeat isolation
    env.setdefault("TERM", "xterm-256color")  # a CI runner leaves this unset; without
    # it tmux can't initialize the terminal over the pty and attach-session never
    # registers as a client at all -- setUp times out with zero clients, before any
    # code under test runs
    return env


class _RealClient:
    """One real tmux client, attached via its own pty -- not simulated: an
    actual `tmux attach-session` process with an actual controlling
    terminal, so `#{client_tty}` is a real, distinct identity per client,
    same as two real terminals attached to the same server."""

    def __init__(self, session, env):
        self.master_fd, slave_fd = pty.openpty()
        self.tty = os.ttyname(slave_fd)
        self.proc = subprocess.Popen(
            ["tmux", "attach-session", "-t", session],
            stdin=slave_fd,
            stdout=slave_fd,
            stderr=slave_fd,
            env=env,
            start_new_session=True,  # its own session leader -- this pty becomes ITS controlling terminal
        )
        os.close(slave_fd)

    def close(self):
        if self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait(timeout=5)
        os.close(self.master_fd)


class _MCPPipe:
    """`mcp_server.py` run exactly as agent-tui runs it: a child process
    with stdin/stdout wired to pipes, no tty at all."""

    def __init__(self, env):
        self.proc = subprocess.Popen(
            [sys.executable, str(MCP_SERVER)],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=env,
            text=True,
            bufsize=1,
        )
        self._id = 0
        self._request("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}})

    def _request(self, method, params=None):
        self._id += 1
        message = {"jsonrpc": "2.0", "id": self._id, "method": method}
        if params is not None:
            message["params"] = params
        self.proc.stdin.write(json.dumps(message) + "\n")
        self.proc.stdin.flush()
        line = self.proc.stdout.readline()
        if not line:
            stderr = self.proc.stderr.read()
            raise AssertionError(f"mcp_server.py produced no response (stderr: {stderr})")
        return json.loads(line)

    def call(self, name, **arguments):
        return self._request("tools/call", {"name": name, "arguments": arguments})

    def close(self):
        self.proc.stdin.close()
        try:
            self.proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.proc.kill()
            self.proc.wait(timeout=5)


def _tmux(env, *args):
    return subprocess.run(["tmux", *args], env=env, check=True, capture_output=True, text=True, timeout=10)


def _list_clients(env):
    """tty -> session, for every client attached to this isolated server."""
    output = _tmux(env, "list-clients", "-F", "#{client_tty}\t#{client_session}").stdout
    clients = {}
    for line in output.splitlines():
        if not line:
            continue
        tty, _, session = line.partition("\t")
        clients[tty] = session
    return clients


def _wait_until(predicate, *, timeout=5, interval=0.1):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return True
        time.sleep(interval)
    return predicate()


@unittest.skipUnless(TMUX_AVAILABLE, "tmux is not installed")
class TwoClientIdentityTest(unittest.TestCase):
    """agent-supervisor#189: two real clients attached, driven through the
    real MCP pipe. This is the test that fails on `main`."""

    def setUp(self):
        self.tmpdir = tempfile.mkdtemp(prefix="as189-tmux-")
        self.env = _isolated_env(self.tmpdir)
        _tmux(self.env, "new-session", "-d", "-s", "alpha")
        _tmux(self.env, "new-session", "-d", "-s", "beta")
        self.client_a = _RealClient("alpha", self.env)
        self.client_b = _RealClient("alpha", self.env)
        ok = _wait_until(lambda: len(_list_clients(self.env)) == 2)
        self.assertTrue(ok, f"two clients never attached: {_list_clients(self.env)}")
        self.addCleanup(self._cleanup)

    def _cleanup(self):
        self.client_a.close()
        self.client_b.close()
        subprocess.run(
            ["tmux", "kill-server"], env=self.env, capture_output=True, timeout=10
        )  # this server's OWN isolated socket only -- TMUX_TMPDIR scopes it, never the default (CLAUDE.md)
        shutil.rmtree(self.tmpdir, ignore_errors=True)

    def test_two_clients_no_target_refuses_and_neither_client_moves(self):
        """The test that matters: fails on `main` today."""
        before = _list_clients(self.env)
        pipe = _MCPPipe(self.env)
        try:
            response = pipe.call("session_attach", session="beta")
        finally:
            pipe.close()
        self.assertTrue(response["result"]["isError"], response)
        text = response["result"]["content"][0]["text"]
        self.assertIn("more than one tmux client", text)
        after = _list_clients(self.env)
        self.assertEqual(before, after, "a client moved despite the refusal")

    def test_two_clients_explicit_target_moves_only_the_named_client(self):
        pipe = _MCPPipe(self.env)
        try:
            response = pipe.call("session_attach", session="beta", client=self.client_a.tty)
        finally:
            pipe.close()
        self.assertFalse(response["result"]["isError"], response)
        payload = json.loads(response["result"]["content"][0]["text"])
        self.assertEqual({"session": "beta", "client": self.client_a.tty, "attached": True}, payload)
        clients = _list_clients(self.env)
        self.assertEqual("beta", clients[self.client_a.tty])
        self.assertEqual("alpha", clients[self.client_b.tty])  # the other client never moved

    def test_two_clients_no_target_detach_refuses_and_neither_client_detaches(self):
        pipe = _MCPPipe(self.env)
        try:
            response = pipe.call("session_detach")
        finally:
            pipe.close()
        self.assertTrue(response["result"]["isError"], response)
        self.assertEqual(2, len(_list_clients(self.env)))

    def test_two_clients_explicit_target_detaches_only_the_named_client(self):
        pipe = _MCPPipe(self.env)
        try:
            response = pipe.call("session_detach", client=self.client_a.tty)
        finally:
            pipe.close()
        self.assertFalse(response["result"]["isError"], response)
        ok = _wait_until(lambda: self.client_a.tty not in _list_clients(self.env))
        self.assertTrue(ok, _list_clients(self.env))
        self.assertIn(self.client_b.tty, _list_clients(self.env))


@unittest.skipUnless(TMUX_AVAILABLE, "tmux is not installed")
class SingleClientFallbackTest(unittest.TestCase):
    """With exactly one client attached, the pre-#189 no-target call still
    works -- established in the issue as the one unambiguous case, and kept
    on purpose (see `SessionAttachSource`'s docstring)."""

    def setUp(self):
        self.tmpdir = tempfile.mkdtemp(prefix="as189-tmux-single-")
        self.env = _isolated_env(self.tmpdir)
        _tmux(self.env, "new-session", "-d", "-s", "alpha")
        _tmux(self.env, "new-session", "-d", "-s", "beta")
        self.client = _RealClient("alpha", self.env)
        ok = _wait_until(lambda: len(_list_clients(self.env)) == 1)
        self.assertTrue(ok, f"client never attached: {_list_clients(self.env)}")
        self.addCleanup(self._cleanup)

    def _cleanup(self):
        self.client.close()
        subprocess.run(["tmux", "kill-server"], env=self.env, capture_output=True, timeout=10)
        shutil.rmtree(self.tmpdir, ignore_errors=True)

    def test_single_client_no_target_still_attaches(self):
        pipe = _MCPPipe(self.env)
        try:
            response = pipe.call("session_attach", session="beta")
        finally:
            pipe.close()
        self.assertFalse(response["result"]["isError"], response)
        payload = json.loads(response["result"]["content"][0]["text"])
        self.assertEqual({"session": "beta", "client": self.client.tty, "attached": True}, payload)
        self.assertEqual("beta", _list_clients(self.env)[self.client.tty])


if __name__ == "__main__":
    unittest.main()
