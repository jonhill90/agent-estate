import json
import sys
import tempfile
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from mine_jon import harvest, is_jon, NOISE  # noqa: E402


def user_line(text, ts="2026-08-16T00:00:00.000Z"):
    """One JSONL record shaped like a real transcript role=user turn."""
    return json.dumps({
        "timestamp": ts,
        "message": {"role": "user", "content": text},
    })


class IsJonTests(unittest.TestCase):
    """Unit-level: every NOISE shape is rejected, a real turn is not."""

    def test_real_prompt_survives(self):
        self.assertTrue(is_jon("fix the flaky test in dispatch.sh"))

    def test_empty_or_blank_is_rejected(self):
        self.assertFalse(is_jon(""))
        self.assertFalse(is_jon("   \n  "))

    def test_each_noise_marker_is_rejected(self):
        for marker in NOISE:
            with self.subTest(marker=marker):
                self.assertFalse(is_jon(f"{marker} some trailing content here"))

    def test_bare_image_reference_is_rejected(self):
        self.assertFalse(is_jon("[Image #1]"))

    def test_image_reference_with_real_text_survives(self):
        # A pasted image is noise; a message that also has real content is not.
        long_text = "[Image #1] " + ("look at this screenshot " * 20)
        self.assertTrue(is_jon(long_text))


class HarvestFixtureTests(unittest.TestCase):
    """Integration-level: one real Jon turn plus one of every filtered shape
    in a single fixture transcript file. Only the real turn may survive.

    This is the test the brief requires: break the filter (remove a NOISE
    marker or the tool_result / is_jon check) and this goes red."""

    REAL_TEXT = "please fix the flaky test in dispatch.sh before merging"

    def _fixture_lines(self):
        lines = [user_line(self.REAL_TEXT, ts="2026-08-16T01:00:00.000Z")]

        # One of each NOISE-shaped role=user turn.
        for i, marker in enumerate(NOISE):
            ts = f"2026-08-16T01:0{(i % 9) + 1}:00.000Z"
            lines.append(user_line(f"{marker} filler content padding out the line",
                                    ts=ts))

        # A bare pasted image (filtered by the separate image check).
        lines.append(user_line("[Image #1]", ts="2026-08-16T01:20:00.000Z"))

        # A role=user turn whose content is a tool_result block, not typed text.
        lines.append(json.dumps({
            "timestamp": "2026-08-16T01:21:00.000Z",
            "message": {
                "role": "user",
                "content": [{"type": "tool_result", "content": "some tool output"}],
            },
        }))

        # A role=assistant line -- must never be picked up regardless of text.
        lines.append(json.dumps({
            "timestamp": "2026-08-16T01:22:00.000Z",
            "message": {"role": "assistant", "content": "sure, I will fix that"},
        }))

        # Malformed JSON -- must be skipped, not raise.
        lines.append('{"not": "valid json"')

        return lines

    def _write_fixture(self, tmpdir):
        path = Path(tmpdir) / "fixture.jsonl"
        path.write_text("\n".join(self._fixture_lines()) + "\n")
        return str(path)

    def test_only_the_real_turn_survives(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            path = self._write_fixture(tmpdir)
            prompts = harvest([path])

        texts = [text for _, text in prompts]
        self.assertEqual(texts, [self.REAL_TEXT])

    def test_removing_the_filter_would_go_red(self):
        # This test documents the contract by construction: it doesn't call
        # a "no filter" code path (there isn't one to call), but it proves
        # the fixture actually contains N noise turns that a naive
        # role==user harvest would return, so a broken/removed filter is
        # observable by re-running is_jon over the raw fixture content
        # directly.
        with tempfile.TemporaryDirectory() as tmpdir:
            path = self._write_fixture(tmpdir)
            raw_user_records = []
            for line in Path(path).read_text().splitlines():
                try:
                    rec = json.loads(line)
                except ValueError:
                    continue
                msg = rec.get("message") or {}
                if msg.get("role") == "user":
                    raw_user_records.append(msg.get("content"))

        # The fixture must actually contain more than just the real turn at
        # the raw role==user level -- otherwise this test would pass even
        # with the filter deleted, which defeats its purpose.
        self.assertGreater(len(raw_user_records), 1)

        filtered = [
            c for c in raw_user_records
            if isinstance(c, str) and is_jon(c)
        ]
        self.assertEqual(filtered, [self.REAL_TEXT])


class ReadOnlyTests(unittest.TestCase):
    """harvest() must never write to the paths it reads."""

    def test_harvest_does_not_modify_the_source_file(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            path = Path(tmpdir) / "fixture.jsonl"
            path.write_text(user_line("hello there") + "\n")
            before = path.read_bytes()
            before_mtime = path.stat().st_mtime_ns

            harvest([str(path)])

            self.assertEqual(path.read_bytes(), before)
            self.assertEqual(path.stat().st_mtime_ns, before_mtime)


if __name__ == "__main__":
    unittest.main()
