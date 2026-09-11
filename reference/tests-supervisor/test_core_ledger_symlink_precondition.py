"""agent-estate#1364, fix-pass finding from PR #1370's independent review:
the guard `_assert_ledger_corpus_symlink_intact` adds to `Ledger.__init__`
had no committed test -- deleting the call site entirely still passed every
test the PR's own "Verify" section cited (confirmed by the reviewer: 100/100
passed with the guard removed). This is that test, exercising `Ledger(root)`
itself -- not the private helper function in isolation -- so a future
refactor that drops, reorders, or otherwise stops calling the guard from
`__init__` fails here, not silently.

Covers both directions the review explicitly asked for: a guard that only
tests the refusal case would pass against a guard that refuses everything,
which blocks every legitimate corpus write and is worse than no guard.

Every fixture lives under a `tempfile.TemporaryDirectory()`. Never touches
the real dead database or `~/corpus/`."""
import os
import shutil
import sys
import tempfile
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parents[1] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger, LedgerSymlinkPreconditionError  # noqa: E402


def _seed_schema(root: Path) -> None:
    """Build a real ledger.sqlite3 (schema + prompts table) at root by
    constructing an ordinary Ledger there once -- root has no corpus.sqlite3
    yet, so this construction itself is unaffected by the guard."""
    Ledger(str(root))


class LedgerSymlinkPreconditionTests(unittest.TestCase):
    """Four cases, exercised through the public `Ledger(root)` constructor:
    two that must be ACCEPTED (a guard that refuses either blocks a
    legitimate corpus write), two that must be REFUSED (the #1364 shapes)."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)

    # --- ACCEPT: the correct, live shape --------------------------------

    def test_accepts_correct_symlink(self):
        # A real corpus.sqlite3 with the actual compat symlink pointing at
        # it -- Ledger(root) must construct normally and write through to
        # corpus.sqlite3, exactly as every real corpus write does today.
        seed_dir = self.root / "seed"
        seed_dir.mkdir()
        _seed_schema(seed_dir)
        shutil.copyfile(seed_dir / "ledger.sqlite3", self.root / "corpus.sqlite3")
        (self.root / "ledger.sqlite3").symlink_to("corpus.sqlite3")

        ledger = Ledger(str(self.root))  # must not raise
        ledger.record_prompt("hp-accept-symlink", at=1_000, text_raw="row", context="ctx")
        self.assertIsNotNone(ledger.get_prompt("hp-accept-symlink"))

    def test_accepts_fresh_directory_with_neither_file(self):
        # No corpus.sqlite3 at all -- not a corpus root (the dead state dir,
        # any other lane's state directory, an ordinary test fixture). The
        # guard must be a complete no-op here; Ledger(root) creates its own
        # ledger.sqlite3 exactly as it always has.
        ledger = Ledger(str(self.root))  # must not raise
        self.assertTrue((self.root / "ledger.sqlite3").exists())
        self.assertFalse((self.root / "corpus.sqlite3").exists())

    # --- REFUSE: the #1364 shapes ----------------------------------------

    def test_refuses_when_symlink_is_simply_missing(self):
        # corpus.sqlite3 exists, ledger.sqlite3 does not -- the exact
        # reproduced #1364 bug shape: without the guard, this is precisely
        # where sqlite would silently create a new, empty ledger.sqlite3.
        seed_dir = self.root / "seed"
        seed_dir.mkdir()
        _seed_schema(seed_dir)
        shutil.copyfile(seed_dir / "ledger.sqlite3", self.root / "corpus.sqlite3")

        with self.assertRaises(LedgerSymlinkPreconditionError):
            Ledger(str(self.root))
        self.assertFalse(
            (self.root / "ledger.sqlite3").exists(),
            "the guard must refuse BEFORE _initialize() ever creates ledger.sqlite3",
        )

    def test_refuses_when_both_exist_as_separate_files(self):
        # Both names present, but as two genuinely different files -- a
        # plain copy instead of a symlink, or a symlink pointing elsewhere.
        # Writing to either would silently diverge from the other.
        seed_dir = self.root / "seed"
        seed_dir.mkdir()
        _seed_schema(seed_dir)
        shutil.copyfile(seed_dir / "ledger.sqlite3", self.root / "corpus.sqlite3")
        shutil.copyfile(seed_dir / "ledger.sqlite3", self.root / "ledger.sqlite3")  # plain copy, not a symlink

        with self.assertRaises(LedgerSymlinkPreconditionError):
            Ledger(str(self.root))


if __name__ == "__main__":
    unittest.main()
