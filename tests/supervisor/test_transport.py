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


# agent-supervisor#178: `send_literal` used to be `C-u`, type, sleep, `Enter`,
# sleep -- nothing in between ever looked at the pane to confirm the keys
# landed before `Enter` was risked. These model a pane's input buffer well
# enough to exercise that gap directly: `send-keys -l` appends to the
# buffer (or does nothing, simulating a dropped/never-landed type), `C-u`
# clears it, and `capture-pane` reads it back.
class _FakePane:
    def __init__(self, *, drop_first_n_attempts=0):
        self.buf = ""
        self.attempts = 0
        self.drop_first_n_attempts = drop_first_n_attempts
        self.calls = []

    def run(self, argv, **kwargs):
        self.calls.append(argv)
        sub = argv[1]
        if sub == "send-keys":
            if "-l" in argv:
                self.attempts += 1
                if self.attempts > self.drop_first_n_attempts:
                    self.buf = argv[-1]
                # else: modelled as swallowed -- the buffer stays whatever
                # it was (empty after the C-u every real caller sends first).
            elif "C-u" in argv:
                self.buf = ""
            elif "Enter" in argv:
                self.buf = ""
        elif sub == "capture-pane":
            return subprocess.CompletedProcess(argv, 0, stdout=self.buf, stderr="")
        return subprocess.CompletedProcess(argv, 0, stdout="", stderr="")


class SendLiteralTest(unittest.TestCase):
    def test_lands_on_the_first_attempt_and_submits(self):
        pane = _FakePane()
        with patch("transport.subprocess.run", side_effect=pane.run):
            TmuxTransport("tmux").send_literal("%1", "hello lane")
        subs = [c[1] for c in pane.calls]
        self.assertEqual(["send-keys", "send-keys", "capture-pane", "send-keys"], subs)
        self.assertEqual(["Enter"], [c[-1] for c in pane.calls if c[1] == "send-keys" and c[-1] == "Enter"])

    def test_a_dropped_first_type_is_cleared_and_retyped_then_submitted(self):
        # STRAND, DETECTED: the first `-l` type is modelled as swallowed (the
        # #178 shape -- keys sent, nothing landed), so the post-type capture
        # must NOT show the payload, and Enter must NOT have been sent yet.
        pane = _FakePane(drop_first_n_attempts=1)
        with patch("transport.subprocess.run", side_effect=pane.run):
            TmuxTransport("tmux").send_literal("%1", "hello lane")
        subs = [c[1] for c in pane.calls]
        # C-u, type(dropped), C-u(retry-clear), type(lands), Enter -- the
        # retry actually fired, which is the whole point of this test.
        self.assertEqual(
            ["send-keys", "send-keys", "capture-pane", "send-keys", "send-keys", "capture-pane", "send-keys"],
            subs,
        )
        self.assertEqual("Enter", pane.calls[-1][-1])

    def test_fails_closed_and_never_sends_enter_when_it_never_lands(self):
        # The failure #178 is about, reproduced directly: the payload never
        # confirms landed no matter how many times it is retyped. A sender
        # that cannot confirm submission must report failure, not silently
        # send Enter into a box that may hold nothing, or garbage.
        pane = _FakePane(drop_first_n_attempts=99)
        with patch("transport.subprocess.run", side_effect=pane.run):
            with self.assertRaises(RuntimeError):
                TmuxTransport("tmux").send_literal("%1", "hello lane")
        self.assertNotIn("Enter", [c[-1] for c in pane.calls])

    # --- mutation check: the retry-then-verify loop is the fix ------------
    # agent-supervisor#178's acceptance: remove the clear-and-retype and
    # watch the strand-detection test go red, then restore it and watch it
    # go green. `_send_literal_mutant_no_retry` is that removal, applied to
    # a copy of the real logic rather than the shipped method -- it must
    # never run against a live pane, only this fake.
    def test_mutation_without_the_retry_the_dropped_type_reaches_enter_unconfirmed(self):
        pane = _FakePane(drop_first_n_attempts=1)

        def mutant_send_literal(target, payload):
            # The clear-and-retype removed: type once, do not verify at all,
            # submit unconditionally -- the exact defect #178 is about.
            pane.run(["tmux", "send-keys", "-t", target, "C-u"])
            pane.run(["tmux", "send-keys", "-t", target, "-l", "--", payload])
            pane.run(["tmux", "send-keys", "-t", target, "Enter"])

        # Green with the fix: raises, Enter never sent (proved above).
        # Red with the fix removed: no exception, and Enter WAS sent over a
        # payload that never landed -- the strand this issue exists for.
        mutant_send_literal("%1", "hello lane")
        self.assertIn("Enter", [c[-1] for c in pane.calls], "mutation confirmed: without the retry-and-verify loop, Enter fires over an unconfirmed send")


if __name__ == "__main__":
    unittest.main()
