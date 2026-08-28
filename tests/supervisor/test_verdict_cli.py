import json
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import main  # noqa: E402

from tests.supervisor.test_verdict_helpers import REPO  # noqa: E402


class CliTests(unittest.TestCase):
    def test_record_then_get_roundtrips_through_the_ledger(self):
        with tempfile.TemporaryDirectory() as tmp:
            rc = main(
                [
                    "--state-dir",
                    tmp,
                    "record",
                    "--repo",
                    REPO,
                    "--number",
                    "9",
                    "--verdict",
                    "rejected",
                    "--head-sha",
                    "a" * 40,
                    "--reviewer",
                    "lane-3",
                ]
            )
            self.assertEqual(rc, 0)

            import io
            import contextlib
            import json

            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                rc = main(
                    ["--state-dir", tmp, "get", "--repo", REPO, "--number", "9", "--source", "ledger"]
                )
            self.assertEqual(rc, 0)
            self.assertEqual(json.loads(out.getvalue())["verdict"], "rejected")

    def test_get_with_head_sha_detects_a_stale_ledger_record(self):
        """agent-dotfiles#218 end-to-end through the CLI: record at SHA A,
        `get` with a DIFFERENT --head-sha, confirm the CLI itself (not just
        the class under test) resolves to unknown rather than approved."""
        with tempfile.TemporaryDirectory() as tmp:
            rc = main(
                [
                    "--state-dir", tmp, "record", "--repo", REPO, "--number", "9",
                    "--verdict", "approved", "--head-sha", "a" * 40, "--reviewer", "lane-3",
                ]
            )
            self.assertEqual(rc, 0)

            import io
            import contextlib
            import json

            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                rc = main(
                    [
                        "--state-dir", tmp, "get", "--repo", REPO, "--number", "9",
                        "--source", "ledger", "--head-sha", "b" * 40,
                    ]
                )
            self.assertEqual(rc, 0)
            self.assertEqual(json.loads(out.getvalue())["verdict"], "unknown")

    def test_get_never_raises_even_for_a_broken_state_dir(self):
        """main() must always produce a well-formed unknown verdict, never a
        traceback a caller could mistake for a hung or crashed process."""
        import io
        import contextlib
        import json

        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            rc = main(
                [
                    "--state-dir",
                    "/nonexistent/definitely/not/writable/anywhere",
                    "get",
                    "--repo",
                    REPO,
                    "--number",
                    "1",
                    "--source",
                    "ledger",
                ]
            )
        self.assertEqual(rc, 0)
        self.assertEqual(json.loads(out.getvalue())["verdict"], "unknown")
