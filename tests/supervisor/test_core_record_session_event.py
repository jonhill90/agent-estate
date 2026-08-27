import json
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
from tests.supervisor.test_core_helpers import MutableClock  # noqa: E402


class RecordSessionEventTest(unittest.TestCase):
    """agent-tui#14: `session_remove`'s audit trail -- logging every removal
    to the ledger with what was running at the time, via the same `events`
    table `complete`/`observe_attention`/`record_snapshot` already write."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.clock = MutableClock()
        self.ledger = Ledger(Path(self.tempdir.name), clock=self.clock)

    def test_writes_a_durable_event_carrying_the_detail(self):
        detail = {"session": "work", "safe_to_remove": True, "refusals": []}
        row = self.ledger.record_session_event("work", event="removed", detail=detail)
        self.assertEqual("session", row["type"])
        self.assertIsNone(row["task_id"])
        self.assertTrue(row["key"].startswith("session:removed:work:"))

    def test_the_event_is_readable_back_through_list_events(self):
        self.ledger.record_session_event("work", event="removed", detail={"a": 1})
        events = self.ledger.list_events()
        self.assertEqual(1, len(events))
        self.assertEqual("session", events[0]["type"])

    def test_the_full_detail_payload_survives_on_disk(self):
        detail = {"session": "work", "worktrees": [{"path": "/wt", "clean": True}]}
        row = self.ledger.record_session_event("work", event="removed", detail=detail)
        payload = json.loads(Path(row["payload_path"]).read_text())
        self.assertEqual(detail, payload)

    def test_two_removals_of_the_same_session_get_distinct_rows(self):
        self.clock.value = 1000
        first = self.ledger.record_session_event("work", event="removed", detail={"n": 1})
        self.clock.value = 2000
        second = self.ledger.record_session_event("work", event="removed", detail={"n": 2})
        self.assertNotEqual(first["key"], second["key"])
        self.assertEqual(2, len(self.ledger.list_events()))

    def test_session_is_required(self):
        with self.assertRaises(ValueError):
            self.ledger.record_session_event("", event="removed", detail={})

    def test_event_is_required(self):
        with self.assertRaises(ValueError):
            self.ledger.record_session_event("work", event="", detail={})


