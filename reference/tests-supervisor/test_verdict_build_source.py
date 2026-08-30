import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import (  # noqa: E402
    GithubReviewVerdictSource,
    LedgerVerdictSource,
    build_source,
)

from tests.supervisor.test_verdict_helpers import REPO  # noqa: E402


class BuildSourceTests(unittest.TestCase):
    def test_unknown_name_raises(self):
        with self.assertRaises(ValueError):
            build_source("not-a-real-source", state_dir="unused")

    def test_ledger_name_resolves_to_a_working_ledger_source(self):
        with tempfile.TemporaryDirectory() as tmp:
            source = build_source("ledger", state_dir=tmp)
            self.assertIsInstance(source, LedgerVerdictSource)
            self.assertEqual(source.verdict(repo=REPO, number=1)["verdict"], "none")

    def test_github_name_resolves_to_a_github_source(self):
        source = build_source("github", state_dir="unused")
        self.assertIsInstance(source, GithubReviewVerdictSource)
