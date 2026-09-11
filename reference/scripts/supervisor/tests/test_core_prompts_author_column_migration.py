import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[4]
SUPERVISOR_DIR = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402


def _seed_pre_author_column_database(root: Path):
    """Build a ledger.sqlite3 with a `prompts` table in the exact
    pre-#1395 shape (no `author` column), one row seeded, so
    `Ledger.__init__`'s own `_migrate_prompts_author_column` has something
    real to migrate. Same "build real, then narrow just the one table this
    migration touches" approach `test_core_prompts_pane_columns_migration.py`'s
    `_seed_pre_pane_columns_database` already uses."""
    root.mkdir(parents=True, exist_ok=True)
    Ledger(root, clock=lambda: 1_000)  # creates every table at its CURRENT shape

    connection = sqlite3.connect(root / "ledger.sqlite3")
    try:
        connection.execute("DROP TABLE prompts")
        connection.execute(
            """
            CREATE TABLE prompts (
                id TEXT PRIMARY KEY,
                at INTEGER NOT NULL,
                text_raw TEXT NOT NULL,
                text_clean TEXT,
                context TEXT NOT NULL,
                session TEXT,
                source_file TEXT,
                tmux_pane TEXT,
                tmux_pane_target TEXT
            )
            """
        )
        connection.execute(
            "INSERT INTO prompts (id, at, text_raw, context, session, source_file) VALUES (?, ?, ?, ?, ?, ?)",
            ("hp-pre-migration", 1_000, "the mistake was mine: I dispatched h#748", "ctx", "s1", None),
        )
        connection.commit()
    finally:
        connection.close()


class PromptsAuthorColumnMigrationTest(unittest.TestCase):
    """agent-estate#1395/#1394 (label-prompt-author-1395): `prompts.author`
    is added with `ALTER TABLE ... ADD COLUMN ... NOT NULL DEFAULT
    'unknown' CHECK (...)`, verified directly against a scratch sqlite3
    file (never the live corpus) to accept a NOT NULL default this way --
    see `_migrate_prompts_author_column`'s own docstring."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.root = Path(self.tempdir.name)
        _seed_pre_author_column_database(self.root)

    def _table_info(self, root=None):
        connection = sqlite3.connect((root or self.root) / "ledger.sqlite3")
        connection.row_factory = sqlite3.Row
        try:
            return {row["name"]: row for row in connection.execute("PRAGMA table_info(prompts)").fetchall()}
        finally:
            connection.close()

    def test_column_is_absent_before_migration_runs(self):
        self.assertNotIn("author", self._table_info())

    def test_opening_migrates_the_column_to_unknown_and_preserves_every_row(self):
        ledger = Ledger(self.root, clock=lambda: 2_000)

        self.assertIn("author", self._table_info())

        row = ledger.get_prompt("hp-pre-migration")
        self.assertIsNotNone(row)
        self.assertEqual("the mistake was mine: I dispatched h#748", row["text_raw"])
        # The core finding this whole change exists for: a pre-existing row
        # (captured before authorship was tracked at all) must read as
        # 'unknown', never silently 'jon' -- 'unknown' means "not offered",
        # not "broken" (this repo's invariant 6).
        self.assertEqual("unknown", row["author"])

    def test_migration_failure_rolls_back_leaving_column_absent(self):
        for failpoint in ("before_add_author_column",):
            with self.subTest(failpoint=failpoint):
                root = Path(tempfile.mkdtemp())
                self.addCleanup(lambda r=root: __import__("shutil").rmtree(r, ignore_errors=True))
                _seed_pre_author_column_database(root)

                with self.assertRaisesRegex(RuntimeError, failpoint):
                    Ledger(root, clock=lambda: 2_000, _migration_failpoint=failpoint)

                self.assertNotIn("author", self._table_info(root))

                connection = sqlite3.connect(root / "ledger.sqlite3")
                try:
                    self.assertEqual(1, connection.execute("SELECT COUNT(*) FROM prompts").fetchone()[0])
                finally:
                    connection.close()

                recovered = Ledger(root, clock=lambda: 3_000)
                self.assertIn("author", self._table_info(root))
                self.assertEqual("unknown", recovered.get_prompt("hp-pre-migration")["author"])

    def test_fresh_ledger_already_carries_the_column(self):
        fresh_root = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: __import__("shutil").rmtree(fresh_root, ignore_errors=True))
        Ledger(fresh_root, clock=lambda: 1_000)
        self.assertIn("author", self._table_info(fresh_root))

    def test_record_prompt_accepts_an_explicit_author(self):
        ledger = Ledger(self.root, clock=lambda: 2_000)
        ledger.record_prompt("hp-jon", at=2_000, text_raw="ship it", context="ctx", author="jon")
        self.assertEqual("jon", ledger.get_prompt("hp-jon")["author"])

    def test_record_prompt_defaults_author_to_unknown(self):
        ledger = Ledger(self.root, clock=lambda: 2_000)
        ledger.record_prompt("hp-default", at=2_000, text_raw="a plain turn", context="ctx")
        self.assertEqual("unknown", ledger.get_prompt("hp-default")["author"])

    def test_record_prompt_rejects_an_invalid_author(self):
        ledger = Ledger(self.root, clock=lambda: 2_000)
        with self.assertRaises(sqlite3.IntegrityError):
            ledger.record_prompt("hp-bad", at=2_000, text_raw="x", context="ctx", author="the-agent")


class PendingPromptAuthorTest(unittest.TestCase):
    """`register_pending_author`/`consume_pending_author`: the durable,
    hash-keyed, proven (not guessed) handoff between an injecting caller
    and `prompt_capture_hook.py`. No injection call site is wired to this
    yet -- see the PR body -- these tests exercise the primitive directly,
    the same way `test_core_prompt_corpus.py` exercises `record_prompt`
    without a real hook in the loop."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.ledger = Ledger(Path(self.tempdir.name), clock=lambda: 1_000)

    def test_registered_text_is_consumed_with_its_author(self):
        self.ledger.register_pending_author("go dispatch the fix", author="director")
        self.assertEqual("director", self.ledger.consume_pending_author("go dispatch the fix"))

    def test_consuming_deletes_the_row_so_a_later_identical_text_does_not_reuse_it(self):
        self.ledger.register_pending_author("status: green", author="supervisor")
        self.assertEqual("supervisor", self.ledger.consume_pending_author("status: green"))
        # Second capture of the SAME literal text (e.g. Jon later types the
        # identical words by coincidence) must not inherit the spent
        # registration -- this is the false-attribution defect #1395
        # measured, and consuming-not-reading is what prevents it here.
        self.assertIsNone(self.ledger.consume_pending_author("status: green"))

    def test_unregistered_text_is_not_matched(self):
        self.assertIsNone(self.ledger.consume_pending_author("Jon's own real words, never registered"))

    def test_register_rejects_jon_or_unknown(self):
        with self.assertRaises(ValueError):
            self.ledger.register_pending_author("x", author="jon")
        with self.assertRaises(ValueError):
            self.ledger.register_pending_author("x", author="unknown")

    def test_re_registering_identical_text_replaces_rather_than_errors(self):
        self.ledger.register_pending_author("retry text", author="supervisor")
        self.ledger.register_pending_author("retry text", author="supervisor")  # a real retry, not a bug
        self.assertEqual("supervisor", self.ledger.consume_pending_author("retry text"))
        self.assertIsNone(self.ledger.consume_pending_author("retry text"))  # consumed once, not twice

    def test_stale_registration_is_purged_by_ttl(self):
        clock = {"t": 1_000}
        ledger = Ledger(Path(tempfile.mkdtemp()), clock=lambda: clock["t"])
        ledger.register_pending_author("abandoned send", author="supervisor")
        clock["t"] += ledger.PENDING_PROMPT_AUTHOR_TTL_SECONDS + 1
        # A later, unrelated registration is what triggers the opportunistic
        # purge (see register_pending_author's own comment) -- registering
        # anything else is enough to sweep the stale row.
        ledger.register_pending_author("a different, later send", author="director")
        self.assertIsNone(ledger.consume_pending_author("abandoned send"))


if __name__ == "__main__":
    unittest.main()
