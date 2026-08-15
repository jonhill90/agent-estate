"""Small tmux transport; terminal output stays inside the adapter process."""

from __future__ import annotations

import subprocess
import time


TMUX_TIMEOUT_SECONDS = 10


class TmuxTransport:
    def __init__(self, tmux_bin="tmux", *, timeout=TMUX_TIMEOUT_SECONDS):
        self.tmux_bin = tmux_bin
        self.timeout = timeout

    def _run(self, *args, check=True):
        return subprocess.run(
            [self.tmux_bin, *args],
            check=check,
            capture_output=True,
            text=True,
            timeout=self.timeout,
        )

    def metadata(self, target):
        fmt = "#{pane_id}|#{pane_current_command}|#{pane_current_path}|#{socket_path}|#{session_created}|#{session_id}"
        output = self._run("display-message", "-p", "-t", target, fmt).stdout.rstrip("\n")
        pane_id, command, path, socket_path, session_created, session_id = output.split("|", 5)
        return {
            "pane_id": pane_id,
            "command": command,
            "path": path,
            "server_id": f"{socket_path}:{session_created}",
            "session_id": session_id,
        }

    def capture(self, target, lines=25):
        return self._run("capture-pane", "-p", "-J", "-t", target, "-S", f"-{int(lines)}").stdout

    def set_option(self, target, name, value):
        self._run("set-option", "-p", "-t", target, name, value)

    def get_option(self, target, name):
        return self._run("display-message", "-p", "-t", target, f"#{{{name}}}").stdout.rstrip("\n")

    def send_literal(self, target, payload):
        self._run("send-keys", "-t", target, "C-u")
        self._run("send-keys", "-t", target, "-l", "--", payload)
        time.sleep(0.1)
        self._run("send-keys", "-t", target, "Enter")
        time.sleep(0.5)

    def respawn_pane(self, target):
        """Kill whatever is running in `target` and restart its command.

        Used by `recycle.respawn_supervisor` to replace a long-lived
        supervisor session with a fresh one; `send_literal` afterward seeds
        the tick prompt. `-k` kills the current pane process first, so this
        is destructive to whatever was running there -- it is never called
        against a pane with unflushed state.
        """
        self._run("respawn-pane", "-k", "-t", target)

    def list_panes(self, session):
        """Every pane's window name and current working directory, for `session`.

        agent-tui#14's worktree detection (`session_guard.remove_guard`) needs
        every pane in the session, not just the active one per window -- `-s`
        ("every pane in the session", not `-t <window>` alone) is what makes
        that true. `=name` is the same exact-match target discipline
        `session_exists` already documents; a session removal guard is
        exactly the kind of caller #137's prefix-match bug would burn.
        """
        fmt = "#{window_name}\t#{pane_current_path}"
        output = self._run("list-panes", "-t", f"={session}", "-s", "-F", fmt).stdout
        panes = []
        for line in output.splitlines():
            if not line:
                continue
            window_name, _, path = line.partition("\t")
            panes.append({"window_name": window_name, "path": path})
        return panes

    def switch_client(self, session):
        """Switch the invoking client's attached session to `session`.

        `=name` exact-match, same discipline as `session_exists` -- never a
        prefix match onto a session this was not asked for.
        """
        self._run("switch-client", "-t", f"={session}")

    def detach_client(self):
        """Detach whatever client is attached to THIS process's own tty.

        Deliberately no `-t`: `tmux detach-client` with no target detaches
        the client for the invoking process's controlling terminal, which is
        correct here because `mcp_server.py` runs as a child of whatever
        TUI/client invoked it and inherits its tty.
        """
        self._run("detach-client")

    def kill_session(self, session):
        """Kill exactly one session, by exact name.

        `=name`, never `kill-server` and never a bare/prefix target -- the
        same #137 exact-match discipline `session_exists` already documents,
        load-bearing here because the blast radius of getting this wrong is
        destroying every session on the box instead of one.
        """
        self._run("kill-session", "-t", f"={session}")

    def session_exists(self, session):
        """Does this tmux server currently have a session by this exact name.

        agent-supervisor#153: `has-session -t foo` prefix-matches when no
        exact `foo` exists (bootstrap-session.sh's #137 finding) -- `=name`
        is tmux's exact-match target syntax and is required here for the
        same reason it is required there. This is the live half of a
        session's supervision state; the ledger (`Ledger.session_marked_
        supervised`) is the recorded half, and the two are never merged
        into one query -- a session the ledger marked supervised that no
        longer exists here must read as drift, not as "supervised", which is
        exactly what a caller that skipped this check would get wrong.
        """
        try:
            self._run("has-session", "-t", f"={session}")
            return True
        except subprocess.CalledProcessError:
            return False
