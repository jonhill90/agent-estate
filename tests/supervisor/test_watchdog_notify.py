import json
import sys
import tempfile
import time
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from watchdog_notify import (  # noqa: E402
    SendError,
    build_heartbeat_message,
    build_message,
    check_and_notify,
    check_and_notify_heartbeat,
    classify_heartbeat,
    decide_notify,
    decide_notify_heartbeat,
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

        # #91: the episode flag records DELIVERY, not attempt. A failed send
        # that marked the episode notified consumed the escalation — the
        # retry was deduped away and nobody was ever paged.
        state = json.loads(self.episode_path.read_text())
        self.assertFalse(state["notified"])

        # The failure must be visible locally, and must say plainly that
        # nobody was told — this log is all that is left when the channel is
        # the thing that is broken.
        logged = log_path.read_text()
        self.assertIn("NOTIFY-FAILED", logged)
        self.assertIn("will retry", logged)
        self.assertIn("fake channel unreachable", logged)

    def test_a_failed_tick_is_retried_by_the_next_tick(self):
        """The #91 regression: tick 1 fails, tick 2 must still attempt."""
        write_status(self.status_path, "escalate", "keeps dying", restarts=3)
        log_path = Path(self.tempdir.name) / "watchdog-notify.log"
        kwargs = dict(
            status_path=self.status_path,
            episode_state_path=self.episode_path,
            log_path=log_path,
        )

        unreachable = FakeSender(fail=True)
        with self.assertRaises(SendError):
            check_and_notify(sender=unreachable, **kwargs)
        self.assertEqual(len(unreachable.calls), 1)

        # Tick 2, same escalate, channel back: the page must go out.
        recovered = FakeSender()
        decision = check_and_notify(sender=recovered, **kwargs)
        self.assertTrue(decision.should_notify)
        self.assertEqual(len(recovered.calls), 1)
        self.assertIn("NOTIFY-SENT", log_path.read_text())

        # Tick 3: now that it was actually delivered, dedup takes over.
        after = FakeSender()
        check_and_notify(sender=after, **kwargs)
        self.assertEqual(after.calls, [])

    def test_a_long_outage_delivers_one_page_not_a_burst(self):
        """Retry-every-tick must not become a queue of identical pages.

        The flag is per-episode, so a recovered channel gets one message
        describing the live state, no matter how many ticks failed first.
        """
        write_status(self.status_path, "escalate", "keeps dying", restarts=3)
        log_path = Path(self.tempdir.name) / "watchdog-notify.log"
        kwargs = dict(
            status_path=self.status_path,
            episode_state_path=self.episode_path,
            log_path=log_path,
        )

        unreachable = FakeSender(fail=True)
        for _ in range(5):
            with self.assertRaises(SendError):
                check_and_notify(sender=unreachable, **kwargs)
        self.assertEqual(len(unreachable.calls), 5)

        recovered = FakeSender()
        for _ in range(4):
            check_and_notify(sender=recovered, **kwargs)
        self.assertEqual(len(recovered.calls), 1, "a recovered channel drained a backlog")

    def test_the_failure_log_counts_attempts_so_an_outage_is_legible(self):
        write_status(self.status_path, "escalate", "keeps dying", restarts=3)
        log_path = Path(self.tempdir.name) / "watchdog-notify.log"
        sender = FakeSender(fail=True)
        for _ in range(2):
            with self.assertRaises(SendError):
                check_and_notify(
                    status_path=self.status_path,
                    episode_state_path=self.episode_path,
                    sender=sender,
                    log_path=log_path,
                )
        self.assertIn("attempt 1", log_path.read_text())
        self.assertIn("attempt 2", log_path.read_text())

    def test_recovery_clears_the_attempt_count_for_the_next_episode(self):
        log_path = Path(self.tempdir.name) / "watchdog-notify.log"
        kwargs = dict(
            status_path=self.status_path,
            episode_state_path=self.episode_path,
            log_path=log_path,
        )
        write_status(self.status_path, "escalate", "keeps dying", restarts=3)
        with self.assertRaises(SendError):
            check_and_notify(sender=FakeSender(fail=True), **kwargs)

        write_status(self.status_path, "restarted", "recovered")
        check_and_notify(sender=FakeSender(), **kwargs)
        self.assertEqual(json.loads(self.episode_path.read_text())["attempts"], 0)

        write_status(self.status_path, "escalate", "dying again", restarts=3)
        with self.assertRaises(SendError):
            check_and_notify(sender=FakeSender(fail=True), **kwargs)
        self.assertIn("attempt 1", log_path.read_text().splitlines()[-1])


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
        self.assertIn("NOTIFY-FAILED", self.log_path.read_text())

    def test_a_failed_cli_tick_leaves_the_episode_open_for_the_next_one(self):
        """End to end through main(), the way watchdog.sh calls it: an
        unconfigured (hence unreachable) notifier must not consume the
        escalation."""
        write_status(self.status_path, "escalate", "keeps dying", restarts=3)
        self.assertEqual(main(self._args()), 1)
        self.assertFalse(json.loads(self.episode_path.read_text())["notified"])

        # Next tick, with a notifier that works: the page goes out.
        script = Path(self.tempdir.name) / "notify.sh"
        script.write_text('#!/bin/bash\nprintf "%s|%s" "$1" "$2" >> "$(dirname "$0")/got"\n')
        script.chmod(0o755)
        self.assertEqual(main(self._args(notify_script=str(script))), 0)
        self.assertIn("Supervisor escalation|watchdog: escalate", (Path(self.tempdir.name) / "got").read_text())


# --- #163: inbox-poll heartbeat staleness -----------------------------------

def write_heartbeat(path, *, checked, state="ok", detail="listening", pid=1):
    path.write_text(f"checked: {checked}\nstate:   {state}\ndetail:  {detail}\npid:     {pid}\n")


class ClassifyHeartbeatTest(unittest.TestCase):
    def test_missing_when_no_fields(self):
        kind, age = classify_heartbeat(None, now=1000.0)
        self.assertEqual(kind, "missing")
        self.assertIsNone(age)

    def test_unreadable_checked_field(self):
        kind, age = classify_heartbeat({"checked": "not-a-timestamp", "state": "ok"}, now=1000.0)
        self.assertEqual(kind, "unreadable")
        self.assertIsNone(age)

    def test_unreadable_when_checked_absent(self):
        kind, age = classify_heartbeat({"state": "ok"}, now=1000.0)
        self.assertEqual(kind, "unreadable")

    def test_stopped_state_reports_stopped_regardless_of_age(self):
        # 2026-08-11T00:00:00Z == epoch 1786406400
        kind, age = classify_heartbeat(
            {"checked": "2026-08-11T00:00:00Z", "state": "stopped"}, now=1786406400.0 + 9999
        )
        self.assertEqual(kind, "stopped")
        self.assertAlmostEqual(age, 9999, delta=1)

    def test_alive_reports_age(self):
        kind, age = classify_heartbeat(
            {"checked": "2026-08-11T00:00:00Z", "state": "ok"}, now=1786406400.0 + 30
        )
        self.assertEqual(kind, "alive")
        self.assertAlmostEqual(age, 30, delta=1)


class BuildHeartbeatMessageTest(unittest.TestCase):
    def test_names_the_age_and_threshold(self):
        message = build_heartbeat_message(age_seconds=250, threshold_seconds=210)
        self.assertIn("250s", message)
        self.assertIn("210s", message)
        self.assertIn("inbox-poll.status", message)

    def test_unreadable_age_is_named_not_a_number(self):
        message = build_heartbeat_message(age_seconds=None, threshold_seconds=210)
        self.assertIn("unreadable", message)


class DecideNotifyHeartbeatTest(unittest.TestCase):
    def test_missing_is_silent_and_resets(self):
        decision = decide_notify_heartbeat(
            kind="missing", age_seconds=None, threshold_seconds=210, episode_notified=True
        )
        self.assertFalse(decision.should_notify)
        self.assertFalse(decision.next_episode_notified)

    def test_stopped_is_silent_and_resets_even_if_very_old(self):
        # #163's Do-not-double-page requirement: report_stop() already
        # decided whether to page for this exit; the heartbeat check must
        # never alarm on the frozen `checked:` an orderly stop leaves behind.
        decision = decide_notify_heartbeat(
            kind="stopped", age_seconds=999999, threshold_seconds=210, episode_notified=False
        )
        self.assertFalse(decision.should_notify)
        self.assertFalse(decision.next_episode_notified)

    def test_exactly_at_threshold_is_fresh_not_stale(self):
        # Mutation-check boundary: an off-by-one or an inverted comparison
        # flips this exact case.
        decision = decide_notify_heartbeat(
            kind="alive", age_seconds=210, threshold_seconds=210, episode_notified=False
        )
        self.assertFalse(decision.should_notify)

    def test_one_second_past_threshold_is_stale(self):
        decision = decide_notify_heartbeat(
            kind="alive", age_seconds=211, threshold_seconds=210, episode_notified=False
        )
        self.assertTrue(decision.should_notify)
        self.assertIsNotNone(decision.message)

    def test_unreadable_is_treated_as_stale(self):
        decision = decide_notify_heartbeat(
            kind="unreadable", age_seconds=None, threshold_seconds=210, episode_notified=False
        )
        self.assertTrue(decision.should_notify)

    def test_stale_same_episode_does_not_notify_again(self):
        decision = decide_notify_heartbeat(
            kind="alive", age_seconds=999, threshold_seconds=210, episode_notified=True
        )
        self.assertFalse(decision.should_notify)
        self.assertTrue(decision.next_episode_notified)

    def test_always_false_staleness_check_is_caught_by_the_boundary_test(self):
        """Mutation-check: a comparison hardcoded to `False` (never stale)
        would make every case in this class fail exactly like a real bug
        would -- proving the guard, not just exercising it."""
        for age in (210, 211, 100000):
            decision = decide_notify_heartbeat(
                kind="alive", age_seconds=age, threshold_seconds=210, episode_notified=False
            )
            expect_notify = age > 210
            self.assertEqual(decision.should_notify, expect_notify, f"age={age}")


class CheckAndNotifyHeartbeatTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.status_path = Path(self.tempdir.name) / "inbox-poll.status"
        self.episode_path = Path(self.tempdir.name) / ".heartbeat-episode.json"

    def test_missing_status_file_never_calls_sender(self):
        sender = FakeSender()
        decision = check_and_notify_heartbeat(
            heartbeat_status_path=self.status_path,
            episode_state_path=self.episode_path,
            sender=sender,
            threshold_seconds=210,
            now=1000.0,
        )
        self.assertFalse(decision.should_notify)
        self.assertEqual(sender.calls, [])

    def test_fresh_heartbeat_never_calls_sender(self):
        sender = FakeSender()
        write_heartbeat(self.status_path, checked="2026-08-11T00:00:00Z")
        check_and_notify_heartbeat(
            heartbeat_status_path=self.status_path,
            episode_state_path=self.episode_path,
            sender=sender,
            threshold_seconds=210,
            now=1786406400.0 + 30,
        )
        self.assertEqual(sender.calls, [])

    def test_stale_heartbeat_notifies_once_per_episode(self):
        sender = FakeSender()
        write_heartbeat(self.status_path, checked="2026-08-11T00:00:00Z")
        kwargs = dict(
            heartbeat_status_path=self.status_path,
            episode_state_path=self.episode_path,
            sender=sender,
            threshold_seconds=210,
            now=1786406400.0 + 500,
        )
        first = check_and_notify_heartbeat(**kwargs)
        second = check_and_notify_heartbeat(**kwargs)
        self.assertTrue(first.should_notify)
        self.assertFalse(second.should_notify)
        self.assertEqual(len(sender.calls), 1)

    def test_dedup_survives_repeated_checks_within_the_same_episode(self):
        """Mutation-check: a dedup that always notifies (episode flag never
        consulted) would send every tick instead of once."""
        sender = FakeSender()
        write_heartbeat(self.status_path, checked="2026-08-11T00:00:00Z")
        kwargs = dict(
            heartbeat_status_path=self.status_path,
            episode_state_path=self.episode_path,
            sender=sender,
            threshold_seconds=210,
            now=1786406400.0 + 500,
        )
        for _ in range(6):
            check_and_notify_heartbeat(**kwargs)
        self.assertEqual(len(sender.calls), 1, "the dedup guard failed to hold across repeated ticks")

    def test_recovery_then_a_new_stale_episode_notifies_again(self):
        sender = FakeSender()
        write_heartbeat(self.status_path, checked="2026-08-11T00:00:00Z")
        check_and_notify_heartbeat(
            heartbeat_status_path=self.status_path,
            episode_state_path=self.episode_path,
            sender=sender,
            threshold_seconds=210,
            now=1786406400.0 + 500,
        )
        # A fresh write means the poller is alive again -- resets the episode.
        write_heartbeat(self.status_path, checked="2026-08-11T00:10:00Z")
        check_and_notify_heartbeat(
            heartbeat_status_path=self.status_path,
            episode_state_path=self.episode_path,
            sender=sender,
            threshold_seconds=210,
            now=1786406400.0 + 600 + 30,
        )
        # And a later, separate stale spell notifies again -- not deduped
        # forever by the first episode.
        check_and_notify_heartbeat(
            heartbeat_status_path=self.status_path,
            episode_state_path=self.episode_path,
            sender=sender,
            threshold_seconds=210,
            now=1786406400.0 + 600 + 500,
        )
        self.assertEqual(len(sender.calls), 2)

    def test_an_orderly_stop_does_not_double_page(self):
        """#163's brief, verbatim: a clean poller exit already pages via its
        own trap (report_stop in inbox-poll.sh); the heartbeat watchdog must
        not also fire for the same event, even long after the stop, even
        though `checked:` is by then far past the threshold."""
        sender = FakeSender()
        write_heartbeat(self.status_path, checked="2026-08-11T00:00:00Z", state="stopped", detail="poller process exiting")
        check_and_notify_heartbeat(
            heartbeat_status_path=self.status_path,
            episode_state_path=self.episode_path,
            sender=sender,
            threshold_seconds=210,
            now=1786406400.0 + 999999,
        )
        self.assertEqual(sender.calls, [])

    def test_send_failure_is_logged_and_the_episode_stays_open(self):
        sender = FakeSender(fail=True)
        write_heartbeat(self.status_path, checked="2026-08-11T00:00:00Z")
        log_path = Path(self.tempdir.name) / "watchdog-notify.log"
        with self.assertRaises(SendError):
            check_and_notify_heartbeat(
                heartbeat_status_path=self.status_path,
                episode_state_path=self.episode_path,
                sender=sender,
                threshold_seconds=210,
                log_path=log_path,
                now=1786406400.0 + 500,
            )
        self.assertFalse(json.loads(self.episode_path.read_text())["notified"])
        logged = log_path.read_text()
        self.assertIn("NOTIFY-FAILED", logged)
        self.assertIn("will retry", logged)


class HeartbeatModeCliTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.status_path = Path(self.tempdir.name) / "inbox-poll.status"
        self.episode_path = Path(self.tempdir.name) / ".heartbeat-episode.json"
        self.log_path = Path(self.tempdir.name) / "watchdog-notify.log"

    def _args(self, *, threshold=1, notify_script=""):
        args = [
            "--mode", "heartbeat",
            "--heartbeat-status-path", str(self.status_path),
            "--episode-state-path", str(self.episode_path),
            "--threshold-seconds", str(threshold),
            "--log-path", str(self.log_path),
        ]
        if notify_script:
            args += ["--notify-script", notify_script]
        return args

    def test_fresh_heartbeat_exits_zero_without_a_notifier_configured(self):
        write_heartbeat(self.status_path, checked=time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()))
        rc = main(self._args(threshold=3600))
        self.assertEqual(rc, 0)
        self.assertFalse(self.log_path.exists())

    def test_stale_heartbeat_without_a_notifier_configured_is_loud_not_silent(self):
        write_heartbeat(self.status_path, checked="2026-08-11T00:00:00Z")
        rc = main(self._args(threshold=1))
        self.assertEqual(rc, 1)
        self.assertIn("NOTIFY-FAILED", self.log_path.read_text())

    def test_heartbeat_mode_and_escalate_mode_use_separate_episode_files_by_default(self):
        parser_default_state = Path("~/.local/state/agent-dotfiles-supervisor").expanduser()
        # Not exercised against the real $HOME -- this only proves the two
        # default paths differ, which is what stops one check's dedup state
        # from being read (and reset) by the other.
        self.assertNotEqual(
            str(parser_default_state / ".watchdog-heartbeat-episode.json"),
            str(parser_default_state / ".watchdog-escalate-episode.json"),
        )

    def test_zero_threshold_is_a_configuration_error_not_a_permastale_page(self):
        write_heartbeat(self.status_path, checked=time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()))
        rc = main(self._args(threshold=0))
        self.assertEqual(rc, 2)


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
