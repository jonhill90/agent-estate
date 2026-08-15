import subprocess
import sys
import unittest
from pathlib import Path
from unittest.mock import patch


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from transport import TmuxTransport  # noqa: E402


class TransportTest(unittest.TestCase):
    def test_tmux_calls_have_a_bounded_timeout(self):
        with patch("transport.subprocess.run", return_value=subprocess.CompletedProcess([], 0, stdout="", stderr="")) as run:
            TmuxTransport("/opt/homebrew/bin/tmux").capture("%19")
        self.assertEqual(10, run.call_args.kwargs["timeout"])

    # agent-supervisor#153: existence must be checked with the EXACT-match
    # target (`=name`), the same lesson bootstrap-session.sh's #137 finding
    # already encodes -- `has-session -t foo` prefix-matches `foo-2`.
    def test_session_exists_uses_the_exact_match_target(self):
        with patch("transport.subprocess.run", return_value=subprocess.CompletedProcess([], 0, stdout="", stderr="")) as run:
            TmuxTransport("tmux").session_exists("Hill90")
        self.assertEqual(["tmux", "has-session", "-t", "=Hill90"], run.call_args.args[0])

    def test_session_exists_true_when_tmux_finds_it(self):
        with patch("transport.subprocess.run", return_value=subprocess.CompletedProcess([], 0, stdout="", stderr="")):
            self.assertTrue(TmuxTransport("tmux").session_exists("Hill90"))

    def test_session_exists_false_when_tmux_reports_no_such_session(self):
        with patch(
            "transport.subprocess.run",
            side_effect=subprocess.CalledProcessError(1, ["tmux", "has-session"]),
        ):
            self.assertFalse(TmuxTransport("tmux").session_exists("no-such-session"))

    # agent-tui#14
    def test_list_panes_uses_exact_match_and_every_pane_in_the_session(self):
        with patch(
            "transport.subprocess.run",
            return_value=subprocess.CompletedProcess([], 0, stdout="free-1\t/a\nfree-2\t/b\n", stderr=""),
        ) as run:
            panes = TmuxTransport("tmux").list_panes("work")
        self.assertEqual(["tmux", "list-panes", "-t", "=work", "-s", "-F", "#{window_name}\t#{pane_current_path}"], run.call_args.args[0])
        self.assertEqual(
            [{"window_name": "free-1", "path": "/a"}, {"window_name": "free-2", "path": "/b"}], panes
        )

    def test_switch_client_uses_the_exact_match_target(self):
        with patch("transport.subprocess.run", return_value=subprocess.CompletedProcess([], 0, stdout="", stderr="")) as run:
            TmuxTransport("tmux").switch_client("work")
        self.assertEqual(["tmux", "switch-client", "-t", "=work"], run.call_args.args[0])

    def test_detach_client_has_no_target(self):
        """No `-t`: detaches the client on THIS process's own controlling
        terminal, not a named session."""
        with patch("transport.subprocess.run", return_value=subprocess.CompletedProcess([], 0, stdout="", stderr="")) as run:
            TmuxTransport("tmux").detach_client()
        self.assertEqual(["tmux", "detach-client"], run.call_args.args[0])

    def test_kill_session_uses_the_exact_match_target_never_kill_server(self):
        with patch("transport.subprocess.run", return_value=subprocess.CompletedProcess([], 0, stdout="", stderr="")) as run:
            TmuxTransport("tmux").kill_session("work")
        self.assertEqual(["tmux", "kill-session", "-t", "=work"], run.call_args.args[0])


if __name__ == "__main__":
    unittest.main()
