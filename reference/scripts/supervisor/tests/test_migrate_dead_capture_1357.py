"""agent-estate#1364: migrate_dead_capture_1357.py had no automated test --
its idempotency, its never-overwrite guarantee, and the NULL `project`
column were established only by manual runs against production data and a
throwaway, uncommitted fixture during review. This is that test, committed.

Every fixture here is synthetic, built via `core.Ledger` against tempdirs --
never the real ~/corpus/ or the real dead database. `migrate_dead_capture_1357`
is imported and called directly (in-process), never shelled out to, so these
tests exercise the exact same `migrate()` function production runs used."""
import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
import migrate_dead_capture_1357 as migrate_mod  # noqa: E402


class MigrateDeadCapture1357Tests(unittest.TestCase):
    """Builds two synthetic Ledger-backed sqlite databases -- a `dead` one
    (the shape reference/scripts/supervisor/core_ledger_core.py's schema
    produces, no `project` column, matching the real dead database) and a
    `corpus` one (the same schema, plus a `project` column bolted on via a
    direct `ALTER TABLE` -- the one difference from the dead schema the
    module docstring names, and the real corpus genuinely has, that no
    `Ledger`-driven migration chain currently creates)."""

    def setUp(self):
        self.dead_dir = tempfile.TemporaryDirectory()
        self.corpus_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dead_dir.cleanup)
        self.addCleanup(self.corpus_dir.cleanup)

        self.dead_ledger = Ledger(self.dead_dir.name, clock=lambda: 1_000)

        # A corpus fixture, shaped like the real one: `corpus.sqlite3` is
        # the actual file (what migrate_dead_capture_1357.py's own
        # read_corpus_ids/main.go's internal/corpus.Path() both name
        # directly), with `ledger.sqlite3 -> corpus.sqlite3` the compat
        # symlink (agent-estate#P6) that makes `Ledger(corpus_dir)` -- and
        # this PR's own #1364 precondition check -- resolve to the SAME
        # file rather than refusing or silently diverging.
        corpus_root = Path(self.corpus_dir.name)
        seed = Ledger(str(corpus_root), clock=lambda: 1_000)  # creates corpus_root/ledger.sqlite3 with the real schema
        (corpus_root / "ledger.sqlite3").rename(corpus_root / "corpus.sqlite3")
        (corpus_root / "ledger.sqlite3").symlink_to("corpus.sqlite3")
        # The one real schema difference migrate_dead_capture_1357.py's own
        # module docstring names: corpus.prompts has `project`, the dead
        # schema does not. Added directly here, exactly like #1364's own
        # investigation found it exists on the real corpus (not produced by
        # any Ledger migration step) -- see core_ledger_schema.py, which has
        # no `project` column anywhere in its own migration chain.
        with sqlite3.connect(corpus_root / "corpus.sqlite3") as conn:  # noqa: SQL001 -- test fixture, not the live ledger-write-guard-protected path
            conn.execute("ALTER TABLE prompts ADD COLUMN project TEXT")
        self.corpus_ledger = Ledger(str(corpus_root), clock=lambda: 1_000)

        self.dead_db_path = str(Path(self.dead_dir.name) / "ledger.sqlite3")

    def _corpus_row(self, prompt_id):
        with sqlite3.connect(f"file:{Path(self.corpus_dir.name) / 'corpus.sqlite3'}?mode=ro", uri=True) as conn:
            conn.row_factory = sqlite3.Row
            row = conn.execute("SELECT * FROM prompts WHERE id=?", (prompt_id,)).fetchone()
            return dict(row) if row else None

    def test_second_run_writes_nothing_new(self):
        self.dead_ledger.record_prompt("hp-a", at=1_000, text_raw="row a", context="ctx-a")
        self.dead_ledger.record_prompt("hp-b", at=1_001, text_raw="row b", context="ctx-b")

        migrate_mod.migrate(self.dead_db_path, self.corpus_dir.name)
        after_first = {self._corpus_row("hp-a")["at"], self._corpus_row("hp-b")["at"]}
        self.assertEqual({1_000, 1_001}, after_first)

        # Second run: nothing in the dead db changed, so nothing should
        # write. Assert via the actual corpus row count, not a printed
        # count -- migrate() itself only prints, it does not return a value.
        with sqlite3.connect(f"file:{Path(self.corpus_dir.name) / 'corpus.sqlite3'}?mode=ro", uri=True) as conn:
            before_count = conn.execute("SELECT COUNT(*) FROM prompts").fetchone()[0]
        migrate_mod.migrate(self.dead_db_path, self.corpus_dir.name)
        with sqlite3.connect(f"file:{Path(self.corpus_dir.name) / 'corpus.sqlite3'}?mode=ro", uri=True) as conn:
            after_count = conn.execute("SELECT COUNT(*) FROM prompts").fetchone()[0]
        self.assertEqual(before_count, after_count, "re-running migrate() must change nothing the second time")

    def test_id_present_in_both_databases_keeps_the_corpus_side_text_clean(self):
        # The corpus's own copy may have been cleaned since the dead
        # database's row was written -- migrate() must never regress that
        # by overwriting text_clean (or any other column) with the dead
        # database's stale, uncleaned version.
        shared_id = "hp-shared-id"
        self.dead_ledger.record_prompt(
            shared_id, at=1_000, text_raw="raw dead-side text", context="ctx",
            text_clean="DEAD-SIDE-TEXT-CLEAN-MUST-NOT-WIN",
        )
        self.corpus_ledger.record_prompt(
            shared_id, at=1_000, text_raw="raw dead-side text", context="ctx",
        )
        self.corpus_ledger.update_text_clean(shared_id, "CORPUS-SIDE-TEXT-CLEAN-ALREADY-JUDGED")

        migrate_mod.migrate(self.dead_db_path, self.corpus_dir.name)

        row = self._corpus_row(shared_id)
        self.assertEqual(
            "CORPUS-SIDE-TEXT-CLEAN-ALREADY-JUDGED", row["text_clean"],
            "an id already present in the corpus must never have its text_clean "
            "overwritten by the dead database's copy",
        )

    def test_project_is_null_not_invented(self):
        self.dead_ledger.record_prompt("hp-project-test", at=1_000, text_raw="row", context="ctx")
        migrate_mod.migrate(self.dead_db_path, self.corpus_dir.name)
        row = self._corpus_row("hp-project-test")
        self.assertIsNotNone(row, "the row must actually have migrated")
        self.assertIsNone(
            row["project"],
            "project must be NULL (record_prompt's own INSERT never names that column) -- "
            "an invented value here is exactly what agent-estate#1357's brief ruled out",
        )

    def test_dry_run_writes_nothing(self):
        self.dead_ledger.record_prompt("hp-dry-run", at=1_000, text_raw="row", context="ctx")
        migrate_mod.migrate(self.dead_db_path, self.corpus_dir.name, dry_run=True)
        self.assertIsNone(self._corpus_row("hp-dry-run"), "--dry-run must never write")


if __name__ == "__main__":
    unittest.main()
