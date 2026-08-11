"""Liveness decisions for the supervisor loop.

The bug these pin, measured on 2026-08-11: the shell watchdog treated an idle
pane as a dead loop and restarted a supervisor that had scheduled
`delaySeconds=3600` and was sleeping correctly. Idle is not evidence of death.
"""

from __future__ import annotations

import json
import sys
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "scripts" / "supervisor"))

import sleepcheck  # noqa: E402


def iso(seconds_ago: float) -> str:
    stamp = datetime.now(timezone.utc) - timedelta(seconds=seconds_ago)
    return stamp.strftime("%Y-%m-%dT%H:%M:%S.000Z")


class DecideLivenessTests(unittest.TestCase):
    def setUp(self) -> None:
        self.now = datetime.now(timezone.utc).timestamp()

    def decide(self, **kwargs):
        params = {
            "scheduled_at": iso(60),
            "delay_seconds": 3600,
            "stopped": False,
            "now": self.now,
        }
        params.update(kwargs)
        return sleepcheck.decide_liveness(**params)

    def test_pending_wakeup_means_asleep_not_dead(self) -> None:
        verdict = self.decide(scheduled_at=iso(60), delay_seconds=3600)
        self.assertTrue(verdict.asleep)
        self.assertIn("wakes in", verdict.reason)

    def test_overdue_wakeup_is_not_asleep(self) -> None:
        verdict = self.decide(scheduled_at=iso(7200), delay_seconds=3600)
        self.assertFalse(verdict.asleep)
        self.assertIn("overdue", verdict.reason)

    def test_a_loop_that_stopped_itself_is_never_asleep(self) -> None:
        # `stop: true` is terminal: no wakeup will ever fire, so the watchdog
        # must act however recent the call was.
        verdict = self.decide(scheduled_at=iso(1), delay_seconds=None, stopped=True)
        self.assertFalse(verdict.asleep)
        self.assertIn("stopped itself", verdict.reason)

    def test_slightly_overdue_is_tolerated_within_grace(self) -> None:
        # A tick fires seconds after a wakeup is due; without grace the
        # watchdog would race the loop it is watching.
        verdict = self.decide(scheduled_at=iso(3660), delay_seconds=3600, grace_seconds=300)
        self.assertTrue(verdict.asleep)
        self.assertIn("grace", verdict.reason)

    def test_no_wakeup_recorded_fails_toward_acting(self) -> None:
        verdict = self.decide(scheduled_at=None, delay_seconds=None)
        self.assertFalse(verdict.asleep)

    def test_missing_delay_fails_toward_acting(self) -> None:
        verdict = self.decide(delay_seconds=None)
        self.assertFalse(verdict.asleep)

    def test_unparseable_timestamp_fails_toward_acting(self) -> None:
        verdict = self.decide(scheduled_at="not-a-timestamp")
        self.assertFalse(verdict.asleep)


class LastWakeupTests(unittest.TestCase):
    def write(self, records: list[dict]) -> str:
        handle = tempfile.NamedTemporaryFile("w", suffix=".jsonl", delete=False)
        for record in records:
            handle.write(json.dumps(record) + "\n")
        handle.close()
        return handle.name

    @staticmethod
    def wakeup(timestamp: str, **params) -> dict:
        return {
            "timestamp": timestamp,
            "message": {
                "content": [
                    {"type": "tool_use", "name": "ScheduleWakeup", "input": params}
                ]
            },
        }

    def test_the_last_wakeup_wins_not_the_first(self) -> None:
        # The real transcript holds every tick's wakeup. Reading the first one
        # would report an hours-old sleep as current.
        path = self.write(
            [
                self.wakeup(iso(9000), delaySeconds=3600),
                self.wakeup(iso(120), delaySeconds=1800),
            ]
        )
        self.assertEqual(sleepcheck.last_wakeup(path), (iso(120), 1800, False))

    def test_unrelated_and_malformed_lines_are_skipped(self) -> None:
        path = self.write([{"type": "user", "message": {"content": "hello"}}])
        with open(path, "a") as handle:
            handle.write("{not json\n")
            handle.write(json.dumps(self.wakeup(iso(30), delaySeconds=600)) + "\n")
        self.assertEqual(sleepcheck.last_wakeup(path), (iso(30), 600, False))

    def test_stop_is_carried_through(self) -> None:
        path = self.write([self.wakeup(iso(30), stop=True)])
        self.assertEqual(sleepcheck.last_wakeup(path), (iso(30), None, True))

    def test_a_transcript_with_no_wakeups_reports_none(self) -> None:
        path = self.write([{"timestamp": iso(10), "message": {"content": []}}])
        self.assertEqual(sleepcheck.last_wakeup(path), (None, None, False))


if __name__ == "__main__":
    unittest.main()


class LatestWakeupAcrossTests(unittest.TestCase):
    """Which transcript the loop's state is read from.

    Every lane shares one Claude project directory, so the newest *file* is
    almost always a busy worker. Measured 2026-08-11: the three newest
    transcripts had zero ScheduleWakeup calls while the supervisor's older one
    held eleven. Reading the newest file reported "no wakeup found", the
    watchdog read that as a dead loop, restarted a healthy supervisor three
    times, hit its escalation cap and paged Jon for a problem that did not
    exist.
    """

    def write(self, directory: Path, name: str, records: list[dict]) -> None:
        with open(directory / name, "w") as handle:
            for record in records:
                handle.write(json.dumps(record) + "\n")

    @staticmethod
    def wakeup(timestamp: str, **params) -> dict:
        return {
            "timestamp": timestamp,
            "message": {
                "content": [
                    {"type": "tool_use", "name": "ScheduleWakeup", "input": params}
                ]
            },
        }

    def test_a_newer_worker_transcript_does_not_hide_the_loop(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self.write(root, "supervisor.jsonl", [self.wakeup(iso(60), delaySeconds=3600)])
            # A worker, written later, with no wakeups at all.
            self.write(root, "worker.jsonl", [{"timestamp": iso(1), "message": {"content": []}}])
            import os as _os
            _os.utime(root / "worker.jsonl", None)

            stamp, delay, stopped = sleepcheck.latest_wakeup_across(str(root))

        self.assertEqual((stamp, delay, stopped), (iso(60), 3600, False))

    def test_the_most_recent_wakeup_wins_across_files(self) -> None:
        # Names chosen so the STALE wakeup is visited last in glob order: a
        # "keep whatever I saw most recently" implementation picks the wrong
        # one and this test goes red. An earlier version used old/new, where
        # glob order happened to favour the right answer, so the mutation
        # check passed while the guard was broken.
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self.write(root, "a-current.jsonl", [self.wakeup(iso(120), delaySeconds=1800)])
            self.write(root, "z-stale.jsonl", [self.wakeup(iso(9000), delaySeconds=3600)])

            stamp, delay, _ = sleepcheck.latest_wakeup_across(str(root))

        self.assertEqual((stamp, delay), (iso(120), 1800))

    def test_ordering_holds_when_the_stale_file_sorts_first(self) -> None:
        # Mirror of the test above. Together they pin the comparison from both
        # directions: one fails a "keep the last one I saw" implementation,
        # this one fails "keep the first one I saw". Either alone passes by
        # luck, which is exactly what the mutation check caught.
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self.write(root, "a-stale.jsonl", [self.wakeup(iso(9000), delaySeconds=3600)])
            self.write(root, "z-current.jsonl", [self.wakeup(iso(120), delaySeconds=1800)])

            stamp, delay, _ = sleepcheck.latest_wakeup_across(str(root))

        self.assertEqual((stamp, delay), (iso(120), 1800))

    def test_no_wakeups_anywhere_reports_none(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self.write(root, "a.jsonl", [{"timestamp": iso(5), "message": {"content": []}}])

            self.assertEqual(sleepcheck.latest_wakeup_across(str(root)), (None, None, False))

    def test_an_empty_directory_reports_none(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            self.assertEqual(sleepcheck.latest_wakeup_across(tmp), (None, None, False))
