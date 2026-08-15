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


if __name__ == "__main__":
    unittest.main()
