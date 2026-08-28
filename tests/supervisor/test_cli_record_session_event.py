import contextlib
import io
import json
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import cli  # noqa: E402


class RecordSessionEventCliTest(unittest.TestCase):
    """agent-tui#14: the CLI wiring `session_remove`'s write path uses to log
    a removal to the ledger before it acts -- see `Ledger.record_session_event`."""

    def test_records_the_detail_and_it_is_readable_back(self):
        with tempfile.TemporaryDirectory() as root:
            detail = {"session": "work", "safe_to_remove": True, "refusals": []}
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                rc = cli.main(
                    [
                        "--state-dir", root, "record-session-event",
                        "--session", "work", "--event", "removed",
                        "--detail", json.dumps(detail),
                    ]
                )
            self.assertEqual(0, rc, output.getvalue())
            result = json.loads(output.getvalue())
            self.assertEqual("session", result["type"])

            events_output = io.StringIO()
            with contextlib.redirect_stdout(events_output):
                cli.main(["--state-dir", root, "events"])
            events = json.loads(events_output.getvalue())
            self.assertEqual(1, len(events))
            self.assertEqual("session", events[0]["type"])
