"""Small tmux transport; terminal output stays inside the adapter process."""

from __future__ import annotations

import subprocess
import time


TMUX_TIMEOUT_SECONDS = 10

# agent-supervisor#186: how long `send_literal` polls the pane after `Enter`
# before deciding the send is confirmed rather than stranded. Small, because
# every caller today (`assign_task`, `notify_supervisor`, `recycle.py`) holds
# `self.ledger.operation_lock()` or an equivalent serial path while this runs.
SUBMIT_CONFIRM_TRIES = 5
SUBMIT_CONFIRM_SETTLE_SECONDS = 0.5


def _stripped(text):
    """Whitespace stripped out entirely, not just trimmed -- a real pane
    wraps a long line across several rows and indents the continuation, so a
    literal substring check has to compare against text with no spaces or
    newlines on either side, the same discipline send.sh's bash primitive
    uses for the same reason."""
    return "".join(text.split())


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
        """Type `payload` into `target` and submit it -- verified, not blind.

        agent-supervisor#178: `Enter` does not submit text a PREVIOUS
        `send-keys` left sitting in the box; `C-u` then retyping submits
        every time. This used to be `C-u`, type, sleep, `Enter`, sleep, with
        nothing in between checking that the keys actually landed before
        `Enter` was risked -- the same gap #178 found in every direct
        `tmux send-keys` caller. Fixed the same way `send.sh`'s bash
        primitive is (that file is this method's sibling, not its ancestor;
        a shell function and a Python method cannot share one body, so the
        contract is shared instead): type, verify BOTH ends of the message
        against what the pane actually shows, retry once via `C-u` and a
        full retype on failure, THEN submit.

        Raises RuntimeError rather than returning if the payload never
        confirms landed -- #178's fail-closed rule: "cannot tell" must never
        read as "sent". `assign_task`/`notify_supervisor` already persist
        the ambiguous `delivery_pending` ledger state before calling this
        (see their own comments), precisely so an exception here leaves that
        record in place rather than a lie that says the send happened.

        agent-supervisor#186: landing was verified but submission was not --
        this method used to send `Enter` and return unconditionally, so an
        Enter swallowed by the harness (the literal #178 failure `send.sh`'s
        `verified_submit` exists to catch) came back as a normal return, and
        every caller's docstring says a normal return means `delivered`. The
        `send.sh` sibling this method mirrors polls `input_box_state` for
        the box actually going empty; that check needs `capture-pane -e` and
        an SGR-aware placeholder parse this file does not carry. What is
        available here without porting that parser: whether the pane changes
        AT ALL after `Enter` is pressed. A swallowed `Enter` is *nothing
        happening* -- no cursor move, no new line, no placeholder redraw --
        so a pane byte-identical to its just-landed capture after several
        settled polls is the same "cannot tell" that `send.sh` treats as
        failure, not success.
        """
        self._run("send-keys", "-t", target, "C-u")
        needle = _stripped(payload)
        # Both ends, not just the head: a dropped prefix is what a repaint
        # eats first, and it is also what an over-long message hides by
        # scrolling -- checking only the head conflates "arrived" with
        # "fits"; checking only the tail would pass a dropped prefix. Both,
        # or neither is evidence (send.sh's own comment, same reasoning).
        head, tail = needle[:40], needle[-40:]
        landed = False
        landed_pane = None
        for attempt in range(2):
            self._run("send-keys", "-t", target, "-l", "--", payload)
            time.sleep(0.1)
            raw_pane = self._run("capture-pane", "-p", "-t", target).stdout
            pane = _stripped(raw_pane)
            if head in pane and tail in pane:
                landed = True
                landed_pane = raw_pane
                break
            if attempt == 0:
                self._run("send-keys", "-t", target, "C-u")
                time.sleep(0.1)
        if not landed:
            raise RuntimeError(
                f"TmuxTransport.send_literal: payload did not land in {target} "
                "after a clear-and-retype -- refusing to send Enter (agent-supervisor#178)"
            )
        self._run("send-keys", "-t", target, "Enter")
        for _ in range(SUBMIT_CONFIRM_TRIES):
            time.sleep(SUBMIT_CONFIRM_SETTLE_SECONDS)
            pane = self._run("capture-pane", "-p", "-t", target).stdout
            if pane != landed_pane:
                return
        raise RuntimeError(
            f"TmuxTransport.send_literal: pane at {target} unchanged after Enter -- "
            "treating as stranded, not submitted (agent-supervisor#186)"
        )

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
