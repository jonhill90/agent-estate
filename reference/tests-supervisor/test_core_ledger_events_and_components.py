import hashlib
import sys
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from tests.supervisor.test_core_helpers import LedgerTestBase  # noqa: E402


class EventsAndComponentsTest(LedgerTestBase):
    def test_idle_attention_is_level_triggered_until_task_disposition(self):
        self.assign()
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")
        self.ledger.accept("review-870", pane_nonce="nonce-22-a")
        event = self.ledger.observe_idle("app-review", pane_nonce="nonce-22-a")
        self.assertEqual("attention:review-870", event["key"])
        self.ledger.mark_notified([event["key"]], retry_after=60)
        self.assertEqual([], self.ledger.events_due(now=1_059))
        self.assertEqual([event["key"]], [item["key"] for item in self.ledger.events_due(now=1_060)])
        with self.assertRaisesRegex(ValueError, "task disposition"):
            self.ledger.ack([event["key"]])

        self.ledger.complete("review-870", b"# Result\n\nDone.\n", pane_nonce="nonce-22-a")
        self.ledger.ack([event["key"]])
        self.assertEqual("acked", self.ledger.get_event(event["key"])["status"])

    def test_failed_component_collection_does_not_advance_baseline(self):
        first = self.ledger.record_component("github", snapshot=b"head-a\n", healthy=True)
        self.assertEqual(hashlib.sha256(b"head-a\n").hexdigest(), first["snapshot_sha256"])
        failed = self.ledger.record_component("github", healthy=False, error="timeout")
        self.assertEqual(first["snapshot_sha256"], failed["snapshot_sha256"])
        recovered = self.ledger.record_component("github", snapshot=b"head-b\n", healthy=True)
        self.assertNotEqual(first["snapshot_sha256"], recovered["snapshot_sha256"])

    def test_snapshot_changes_emit_one_bounded_diff_event(self):
        self.assertIsNone(self.ledger.record_snapshot("github", b"pr=870 pending\n"))
        self.assertIsNone(self.ledger.record_snapshot("github", b"pr=870 pending\n"))
        event = self.ledger.record_snapshot("github", b"pr=870 success\n")
        self.assertEqual("sensor", event["type"])
        self.assertTrue(event["key"].startswith("sensor:github:"))
        payload = Path(event["payload_path"]).read_text()
        self.assertIn("-pr=870 pending", payload)
        self.assertIn("+pr=870 success", payload)
        repeated = self.ledger.record_snapshot("github", b"pr=870 success\n")
        self.assertIsNone(repeated)
        self.assertEqual(1, len(self.ledger.list_events(event_type="sensor")))

        large = b"x" * (80 * 1024)
        truncated = self.ledger.record_snapshot("github", large)
        bounded = Path(truncated["payload_path"]).read_bytes()
        self.assertLessEqual(len(bounded), 64 * 1024)
        self.assertIn(b"[DIFF TRUNCATED]", bounded)
