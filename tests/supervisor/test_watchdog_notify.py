import json
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from watchdog_notify import (  # noqa: E402
    SendError,
    build_message,
    check_and_notify,
    decide_notify,
    main,
    parse_restarts,
    parse_status,
    send_via_notify_skill,
)


class FakeSender:
    """Records calls instead of touching Messages.app or the network."""

    def __init__(self, fail=False):
        self.fail = fail
        self.calls = []

    def __call__(self, message):
        self.calls.append(message)
        if self.fail:
            raise SendError("fake channel unreachable")


def write_status(path, state, detail="", restarts=0, window=3600):
    path.write_text(
        "checked:  2026-08-11T05:00:00Z\n"
        f"state:    {state}\n"
        f"detail:   {detail}\n"
        "pane:     agent-dotfiles:1.1\n"
        f"restarts: {restarts} in the last {window}s\n"
    )


class ParseStatusTest(unittest.TestCase):
    def test_parses_fields(self):
        text = (
            "checked:  2026-08-11T05:00:00Z\n"
            "state:    escalate\n"
            "detail:   restarted 3 times\n"
            "pane:     agent-dotfiles:1.1\n"
            "restarts: 3 in the last 3600s\n"
        )
        fields = parse_status(text)
        self.assertEqual(fields["state"], "escalate")
        self.assertEqual(fields["detail"], "restarted 3 times")

    def test_parse_restarts(self):
        self.assertEqual(parse_restarts("3 in the last 3600s"), (3, 3600))


class BuildMessageTest(unittest.TestCase):
    def test_short_and_actionable(self):
        message = build_message(detail="restarted 3 times and it keeps dying", restarts=3, window_seconds=3600)
        self.assertLess(len(message), 200)
        self.assertIn("3", message)
        self.assertIn("watchdog.status", message)


class DecideNotifyTest(unittest.TestCase):
    def test_escalate_first_time_notifies(self):
        decision = decide_notify(
            state="escalate", detail="d", restarts=3, window_seconds=3600, episode_notified=False
        )
        self.assertTrue(decision.should_notify)
        self.assertTrue(decision.next_episode_notified)
        self.assertIsNotNone(decision.message)

    def test_escalate_same_episode_does_not_notify(self):
        decision = decide_notify(
            state="escalate", detail="d", restarts=3, window_seconds=3600, episode_notified=True
        )
        self.assertFalse(decision.should_notify)
        self.assertTrue(decision.next_episode_notified)

    def test_non_escalate_states_are_silent_and_reset_episode(self):
        for state in ("working", "waiting_on_jon", "cooling_down", "restarted"):
            with self.subTest(state=state):
                decision = decide_notify(
                    state=state, detail="d", restarts=0, window_seconds=3600, episode_notified=True
                )
                self.assertFalse(decision.should_notify)
                self.assertFalse(decision.next_episode_notified)

    def test_new_episode_after_reset_notifies_again(self):
        # escalate -> notified
        first = decide_notify(state="escalate", detail="d", restarts=3, window_seconds=3600, episode_notified=False)
        self.assertTrue(first.should_notify)
        # loop recovers -> resets
        reset = decide_notify(
            state="restarted", detail="d", restarts=0, window_seconds=3600, episode_notified=first.next_episode_notified
        )
        self.assertFalse(reset.next_episode_notified)
        # escalates again -> new episode, notifies again
        second = decide_notify(
            state="escalate", detail="d", restarts=1, window_seconds=3600, episode_notified=reset.next_episode_notified
        )
        self.assertTrue(second.should_notify)


class CheckAndNotifyTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.status_path = Path(self.tempdir.name) / "watchdog.status"
        self.episode_path = Path(self.tempdir.name) / ".escalate-episode.json"

    def test_escalate_sends_once_across_ticks(self):
        sender = FakeSender()
        write_status(self.status_path, "escalate", "keeps dying", restarts=3)

        first = check_and_notify(status_path=self.status_path, episode_state_path=self.episode_path, sender=sender)
        second = check_and_notify(status_path=self.status_path, episode_state_path=self.episode_path, sender=sender)

        self.assertTrue(first.should_notify)
        self.assertFalse(second.should_notify)
        self.assertEqual(len(sender.calls), 1)

    def test_silent_states_never_call_sender(self):
        sender = FakeSender()
        for state in ("working", "waiting_on_jon", "cooling_down", "restarted"):
            write_status(self.status_path, state)
            check_and_notify(status_path=self.status_path, episode_state_path=self.episode_path, sender=sender)
        self.assertEqual(sender.calls, [])

    def test_new_episode_after_recovery_sends_again(self):
        sender = FakeSender()
        write_status(self.status_path, "escalate", "keeps dying", restarts=3)
        check_and_notify(status_path=self.status_path, episode_state_path=self.episode_path, sender=sender)

        write_status(self.status_path, "restarted", "recovered")
        check_and_notify(status_path=self.status_path, episode_state_path=self.episode_path, sender=sender)

        write_status(self.status_path, "escalate", "dying again", restarts=1)
        check_and_notify(status_path=self.status_path, episode_state_path=self.episode_path, sender=sender)

        self.assertEqual(len(sender.calls), 2)

    def test_sender_failure_is_logged_and_raised_not_silent(self):
        sender = FakeSender(fail=True)
        write_status(self.status_path, "escalate", "keeps dying", restarts=3)
        log_path = Path(self.tempdir.name) / "watchdog-notify.log"

        with self.assertRaises(SendError):
            check_and_notify(
                status_path=self.status_path,
                episode_state_path=self.episode_path,
                sender=sender,
                log_path=log_path,
            )

        # The episode must still be marked notified — a failed send attempt
        # is still an attempt; retrying every tick after a failed send would
        # be a message storm the moment the channel comes back.
        state = json.loads(self.episode_path.read_text())
        self.assertTrue(state["notified"])

        # The failure must be visible locally, not swallowed.
        self.assertIn("SEND-FAILED", log_path.read_text())
        self.assertIn("fake channel unreachable", log_path.read_text())


class MainCliTest(unittest.TestCase):
    """Exercises main() as watchdog.sh will actually call it: as a
    subprocess-style entry point, never touching a real channel."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.status_path = Path(self.tempdir.name) / "watchdog.status"
        self.episode_path = Path(self.tempdir.name) / ".escalate-episode.json"
        self.log_path = Path(self.tempdir.name) / "watchdog-notify.log"

    def _args(self, notify_script=""):
        args = [
            "--status-path", str(self.status_path),
            "--episode-state-path", str(self.episode_path),
            "--log-path", str(self.log_path),
        ]
        if notify_script:
            args += ["--notify-script", notify_script]
        return args

    def test_non_escalate_tick_exits_zero_without_notify_script_configured(self):
        write_status(self.status_path, "working")
        rc = main(self._args())
        self.assertEqual(rc, 0)
        self.assertFalse(self.log_path.exists())

    def test_escalate_without_notify_script_configured_is_loud_not_silent(self):
        write_status(self.status_path, "escalate", "keeps dying", restarts=3)
        rc = main(self._args())
        self.assertEqual(rc, 1)
        self.assertIn("SEND-FAILED", self.log_path.read_text())


if __name__ == "__main__":
    unittest.main()


class SenderInvocationTests(unittest.TestCase):
    """Which notifier gets called, and how.

    Two notifiers exist: the `notify` skill (iMessage only) and
    `scripts/supervisor/notify.sh` (Telegram, the only one proven to reach
    Jon). Routing an escalation to the wrong one means an escalation that
    reaches nobody, which is the failure this whole path exists to prevent.
    """

    def test_a_shell_notifier_is_called_with_subject_and_body(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            script = Path(tmp) / "notify.sh"
            script.write_text('#!/bin/bash\nprintf "%s|%s" "$1" "$2" > "$(dirname "$0")/got"\n')
            script.chmod(0o755)
            send_via_notify_skill("loop is down", notify_script=str(script))
            self.assertEqual((Path(tmp) / "got").read_text(), "Supervisor escalation|loop is down")

    def test_a_python_notifier_keeps_the_flag_form(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            script = Path(tmp) / "notify.py"
            script.write_text(
                "import sys,pathlib\n"
                "pathlib.Path(__file__).with_name('got').write_text(' '.join(sys.argv[1:]))\n"
            )
            send_via_notify_skill("loop is down", notify_script=str(script))
            self.assertEqual(
                (Path(tmp) / "got").read_text(), "--message loop is down --send"
            )

    def test_a_failing_notifier_raises_rather_than_silently_succeeding(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            script = Path(tmp) / "notify.sh"
            script.write_text('#!/bin/bash\necho "no channel" >&2\nexit 1\n')
            script.chmod(0o755)
            with self.assertRaises(SendError):
                send_via_notify_skill("x", notify_script=str(script))
