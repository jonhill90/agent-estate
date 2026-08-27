import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import prompt_capture_hook  # noqa: E402
from core import Ledger  # noqa: E402
from mine_prompts import CONTEXT_UNDETERMINED  # noqa: E402


class CaptureUnitTests(unittest.TestCase):
    """agent-supervisor#687: `capture()` is the part of the hook that talks
    to the ledger, split out from `main()`'s stdin/exit-code plumbing so
    these tests can call it directly rather than shell out for every case."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.ledger = Ledger(self.tempdir.name)

    def test_real_prompt_is_captured_and_left_unitemised(self):
        status = prompt_capture_hook.capture(
            {"session_id": "s1", "prompt": "make render mode LIVE"}, self.ledger
        )
        self.assertIn("left unitemised for judging", status)
        rows = [p for p in self._all_prompts()]
        self.assertEqual(1, len(rows))
        self.assertEqual("make render mode LIVE", rows[0]["text_raw"])
        self.assertEqual(CONTEXT_UNDETERMINED, rows[0]["context"])
        # No transcript_path given -> nothing to itemise from, and no model
        # ran -- confirmed by there being no items row at all yet.
        self.assertEqual([], self.ledger.list_open_items())
        unitemised = self.ledger.list_unitemised_prompts()
        self.assertEqual(1, len(unitemised))
        self.assertEqual(rows[0]["id"], unitemised[0]["id"])

    def test_dispatch_brief_boilerplate_is_dropped_mechanically_not_deleted(self):
        text = "Read /tmp/brief.md. That file is your complete brief. Do the work."
        status = prompt_capture_hook.capture({"session_id": "s1", "prompt": text}, self.ledger)
        self.assertIn("dropped as noise", status)
        prompts = self._all_prompts()
        self.assertEqual(1, len(prompts), "the prompt row itself is never deleted, only its item")
        self.assertEqual(text, prompts[0]["text_raw"])
        self.assertEqual([], self.ledger.list_unitemised_prompts(),
                          "a dropped prompt must leave the itemisation queue, or it resurfaces forever")

    def test_idempotent_rerun_writes_zero(self):
        payload = {"session_id": "s1", "prompt": "keep the CLI narrow", "transcript_path": ""}
        first = prompt_capture_hook.capture(payload, self.ledger)
        self.assertIn("written", first)
        before = len(self._all_prompts())
        second = prompt_capture_hook.capture(payload, self.ledger)
        self.assertIn("already present", second)
        after = len(self._all_prompts())
        self.assertEqual(before, after, "a re-run over the identical submission must write zero new rows")

    def test_context_comes_from_transcript_last_assistant_turn(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        transcript = Path(tmp.name) / "session.jsonl"
        transcript.write_text(
            json.dumps({"message": {"role": "assistant", "content": "deciding LIVE vs PREVIEW"}}) + "\n"
        )
        prompt_capture_hook.capture(
            {"session_id": "s2", "prompt": "make it LIVE", "transcript_path": str(transcript)},
            self.ledger,
        )
        rows = self._all_prompts()
        self.assertEqual("deciding LIVE vs PREVIEW", rows[0]["context"])

    def test_empty_prompt_writes_nothing(self):
        status = prompt_capture_hook.capture({"session_id": "s1", "prompt": "   "}, self.ledger)
        self.assertIn("nothing to capture", status)
        self.assertEqual([], self._all_prompts())

    def _all_prompts(self):
        import sqlite3
        connection = sqlite3.connect(Path(self.tempdir.name) / "ledger.sqlite3")
        connection.row_factory = sqlite3.Row
        try:
            return [dict(row) for row in connection.execute("SELECT * FROM prompts ORDER BY at").fetchall()]
        finally:
            connection.close()


class CaptureHealthViewTests(unittest.TestCase):
    """agent-supervisor#687: the staleness signal itself."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.ledger = Ledger(self.tempdir.name)

    def test_empty_corpus_reports_null_not_zero(self):
        rows = self.ledger.read_prompt_view("capture_health")
        self.assertEqual(1, len(rows))
        self.assertIsNone(rows[0]["newest_prompt_at"])
        self.assertIsNone(rows[0]["seconds_since_capture"])

    def test_seconds_since_capture_reflects_the_newest_row(self):
        import time
        now = int(time.time())
        self.ledger.record_prompt(
            "hp-oldrow", at=now - 400000, text_raw="old", context="ctx"
        )
        self.ledger.record_prompt(
            "hp-newrow", at=now - 5, text_raw="new", context="ctx"
        )
        rows = self.ledger.read_prompt_view("capture_health")
        self.assertEqual(now - 5, rows[0]["newest_prompt_at"])
        self.assertGreaterEqual(rows[0]["seconds_since_capture"], 5)
        self.assertLess(rows[0]["seconds_since_capture"], 60)  # generous slack for test runtime


class MainEndToEndTests(unittest.TestCase):
    """Exercises the real subprocess entry point Claude Code actually
    invokes -- stdin in, exit code and stdout/stderr behaviour verified
    directly rather than assumed from the unit-level `capture()` tests."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)

    def _run(self, payload):
        return subprocess.run(
            [sys.executable, str(SUPERVISOR_DIR / "prompt_capture_hook.py")],
            input=json.dumps(payload),
            capture_output=True,
            text=True,
            env={**__import__("os").environ, "AGENT_SUPERVISOR_STATE_DIR": self.tempdir.name},
            timeout=30,
        )

    def test_exit_code_is_zero_and_stdout_is_empty(self):
        result = self._run({"session_id": "s1", "prompt": "a real directive"})
        self.assertEqual(0, result.returncode, msg=result.stderr)
        self.assertEqual("", result.stdout, "stdout is injected into model context -- must stay empty")

    def test_malformed_stdin_still_exits_zero(self):
        result = subprocess.run(
            [sys.executable, str(SUPERVISOR_DIR / "prompt_capture_hook.py")],
            input="not json{{{",
            capture_output=True,
            text=True,
            env={**__import__("os").environ, "AGENT_SUPERVISOR_STATE_DIR": self.tempdir.name},
            timeout=30,
        )
        self.assertEqual(0, result.returncode)
        self.assertEqual("", result.stdout)

    def test_prompt_is_actually_written_via_the_real_entry_point(self):
        self._run({"session_id": "s1", "prompt": "verify end to end capture"})
        ledger = Ledger(self.tempdir.name)
        unitemised = ledger.list_unitemised_prompts()
        self.assertEqual(1, len(unitemised))
        self.assertEqual("verify end to end capture", unitemised[0]["text_raw"])


if __name__ == "__main__":
    unittest.main()
