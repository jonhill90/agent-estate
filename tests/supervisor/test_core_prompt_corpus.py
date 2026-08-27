import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402


class PromptCorpusTest(unittest.TestCase):
    """agent-supervisor#280. Synthetic fixtures only -- no transcript
    content belongs in this suite or in the diff that carries it."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.root = Path(self.tempdir.name)
        self.ledger = Ledger(self.root, clock=lambda: 1_000)

    def _raw(self, sql, params=()):
        connection = sqlite3.connect(self.root / "ledger.sqlite3")
        connection.row_factory = sqlite3.Row
        try:
            return [dict(row) for row in connection.execute(sql, params).fetchall()]
        finally:
            connection.close()

    def test_schema_creation_is_idempotent(self):
        """Opening the same ledger twice must not raise -- CREATE TABLE/VIEW
        IF NOT EXISTS is the whole migration for these three tables."""
        Ledger(self.root, clock=lambda: 1_000)  # second open, same directory
        tables = {
            row["name"]
            for row in self._raw(
                "SELECT name FROM sqlite_master WHERE type='table'"
            )
        }
        self.assertTrue({"prompts", "items", "links"}.issubset(tables))
        views = {
            row["name"]
            for row in self._raw("SELECT name FROM sqlite_master WHERE type='view'")
        }
        self.assertEqual(
            {
                "unacknowledged", "live_parameters", "conflicts", "open_questions",
                "possibility_count", "needs_review",
            },
            views,
        )

    def test_existing_tables_are_untouched_by_this_migration(self):
        """Additive only: every pre-existing table survives, unmodified, in
        the same ledger that now also carries the prompt corpus."""
        pre_existing = {
            "lanes", "tasks", "source_tasks", "events", "components",
            "pr_verdicts", "sessions",
        }
        tables = {
            row["name"]
            for row in self._raw("SELECT name FROM sqlite_master WHERE type='table'")
        }
        self.assertTrue(pre_existing.issubset(tables))

    def test_text_raw_survives_a_text_clean_update_unchanged(self):
        raw = "synthetic raw prompt fixture, unedited"
        self.ledger.record_prompt(
            "p1", at=1_000, text_raw=raw, context="deciding fixture behaviour",
        )
        updated = self.ledger.update_text_clean("p1", "synthetic cleaned prompt fixture")
        self.assertEqual(raw, updated["text_raw"])
        self.assertEqual("synthetic cleaned prompt fixture", updated["text_clean"])
        reread = self._raw("SELECT text_raw, text_clean FROM prompts WHERE id='p1'")[0]
        self.assertEqual(raw, reread["text_raw"])

    def test_context_is_required_on_a_prompt(self):
        with self.assertRaises(ValueError):
            self.ledger.record_prompt("p1", at=1_000, text_raw="x", context="")

    def _seed_prompt(self, prompt_id):
        self.ledger.record_prompt(
            prompt_id, at=1_000, text_raw=f"raw-{prompt_id}", context=f"context-{prompt_id}",
        )

    def test_unacknowledged_view_is_open_items_only(self):
        self._seed_prompt("p1")
        self.ledger.add_item("i-open", prompt_id="p1", kind="directive", body="b", weight="hard", status="open")
        self.ledger.add_item("i-acked", prompt_id="p1", kind="directive", body="b", weight="hard", status="acknowledged")
        ids = {row["id"] for row in self._raw("SELECT id FROM unacknowledged")}
        self.assertEqual({"i-open"}, ids)

    def test_live_parameters_excludes_retracted_and_non_parameters(self):
        self._seed_prompt("p1")
        self.ledger.add_item("i-live", prompt_id="p1", kind="parameter", body="render=LIVE", weight="hard")
        self.ledger.add_item("i-retracted", prompt_id="p1", kind="parameter", body="render=OLD", weight="retracted")
        self.ledger.add_item("i-question", prompt_id="p1", kind="question", body="q", weight="hard")
        ids = {row["id"] for row in self._raw("SELECT id FROM live_parameters")}
        self.assertEqual({"i-live"}, ids)

    def test_open_questions_view_is_open_questions_only(self):
        self._seed_prompt("p1")
        self.ledger.add_item("i-open-q", prompt_id="p1", kind="question", body="q1", weight="preference", status="open")
        self.ledger.add_item("i-resolved-q", prompt_id="p1", kind="question", body="q2", weight="preference", status="resolved")
        self.ledger.add_item("i-directive", prompt_id="p1", kind="directive", body="d", weight="hard", status="open")
        ids = {row["id"] for row in self._raw("SELECT id FROM open_questions")}
        self.assertEqual({"i-open-q"}, ids)

    def test_conflicts_view_reports_linked_pairs_only(self):
        self._seed_prompt("p1")
        self.ledger.add_item("i-a", prompt_id="p1", kind="parameter", body="render=LIVE", weight="hard")
        self.ledger.add_item("i-b", prompt_id="p1", kind="parameter", body="render=PREVIEW", weight="hard")
        self.ledger.add_item("i-c", prompt_id="p1", kind="parameter", body="unrelated", weight="preference")
        self.ledger.link_items("i-a", "i-b", "conflicts_with")
        self.ledger.link_items("i-a", "i-c", "supersedes")
        rows = self._raw("SELECT item_id, other_item_id FROM conflicts")
        self.assertEqual([{"item_id": "i-a", "other_item_id": "i-b"}], rows)

    def test_possibility_count_counts_only_live_hard_parameters(self):
        self._seed_prompt("p1")
        self.ledger.add_item("i-hard-live", prompt_id="p1", kind="parameter", body="render=LIVE", weight="hard")
        self.ledger.add_item("i-hard-retracted", prompt_id="p1", kind="parameter", body="render=OLD", weight="retracted")
        self.ledger.add_item("i-preference", prompt_id="p1", kind="parameter", body="theme=dark", weight="preference")
        count = self._raw("SELECT count FROM possibility_count")[0]["count"]
        self.assertEqual(1, count)

    def test_possibility_count_is_zero_on_an_empty_corpus(self):
        count = self._raw("SELECT count FROM possibility_count")[0]["count"]
        self.assertEqual(0, count)

    def test_dropped_status_requires_a_reason(self):
        self._seed_prompt("p1")
        with self.assertRaises(ValueError):
            self.ledger.add_item("i1", prompt_id="p1", kind="directive", body="b", weight="hard", status="dropped")
        row = self.ledger.add_item(
            "i2", prompt_id="p1", kind="directive", body="b", weight="hard",
            status="dropped", status_reason="superseded by a later prompt",
        )
        self.assertEqual("dropped", row["status"])
        self.assertEqual("superseded by a later prompt", row["status_reason"])

    def test_get_prompt_returns_none_for_unknown_id(self):
        self.assertIsNone(self.ledger.get_prompt("nope"))

    def test_get_prompt_round_trips_a_written_row(self):
        self._seed_prompt("p1")
        row = self.ledger.get_prompt("p1")
        self.assertEqual("raw-p1", row["text_raw"])
        self.assertEqual("context-p1", row["context"])

    def test_read_prompt_view_rejects_an_unknown_view(self):
        with self.assertRaises(ValueError):
            self.ledger.read_prompt_view("items")  # a table, not a view -- must not be reachable

    def test_get_item_returns_none_for_unknown_id(self):
        self.assertIsNone(self.ledger.get_item("nope"))

    def test_get_item_round_trips_a_written_row(self):
        self._seed_prompt("p1")
        self.ledger.add_item("i1", prompt_id="p1", kind="directive", body="do it", weight="hard")
        row = self.ledger.get_item("i1")
        self.assertEqual("do it", row["body"])

    def test_unitemised_prompts_excludes_prompts_with_any_item(self):
        self._seed_prompt("p1")
        self._seed_prompt("p2")
        self.ledger.add_item("i1", prompt_id="p1", kind="directive", body="do it", weight="hard")
        ids = [row["id"] for row in self.ledger.list_unitemised_prompts()]
        self.assertEqual(["p2"], ids)

    def test_unitemised_prompts_respects_limit(self):
        self._seed_prompt("p1")
        self._seed_prompt("p2")
        rows = self.ledger.list_unitemised_prompts(limit=1)
        self.assertEqual(1, len(rows))

    def test_read_prompt_view_reads_each_by_name(self):
        self._seed_prompt("p1")
        self.ledger.add_item("i-open", prompt_id="p1", kind="directive", body="b", weight="hard", status="open")
        for view in Ledger.PROMPT_VIEWS:
            rows = self.ledger.read_prompt_view(view)
            self.assertIsInstance(rows, list)
        unacked = self.ledger.read_prompt_view("unacknowledged")
        self.assertEqual(["i-open"], [row["id"] for row in unacked])

    def test_drop_item_requires_a_reason(self):
        self._seed_prompt("p1")
        self.ledger.add_item("i1", prompt_id="p1", kind="directive", body="b", weight="hard")
        with self.assertRaises(ValueError):
            self.ledger.drop_item("i1", "")

    def test_drop_item_rejects_an_unknown_id(self):
        with self.assertRaises(ValueError):
            self.ledger.drop_item("nope", "reason")

    def test_drop_item_corrects_status_in_place_without_touching_judgement_fields(self):
        """agent-supervisor#583: reclassification must change status/status_reason
        only -- kind, body, weight and the item's id are the original judgement
        and stay put, so the record remains reviewable, not rewritten."""
        self._seed_prompt("p1")
        self.ledger.add_item("i1", prompt_id="p1", kind="directive", body="do it", weight="hard")
        row = self.ledger.drop_item("i1", "synthetic eval fixture")
        self.assertEqual("dropped", row["status"])
        self.assertEqual("synthetic eval fixture", row["status_reason"])
        self.assertEqual("directive", row["kind"])
        self.assertEqual("do it", row["body"])
        self.assertEqual("hard", row["weight"])
        self.assertEqual([], self.ledger.read_prompt_view("unacknowledged"))

    def test_list_open_items_excludes_dropped_and_carries_prompt_context(self):
        self._seed_prompt("p1")
        self._seed_prompt("p2")
        self.ledger.add_item("i-open", prompt_id="p1", kind="directive", body="b", weight="hard", status="open")
        self.ledger.add_item(
            "i-dropped", prompt_id="p2", kind="directive", body="b", weight="hard",
            status="dropped", status_reason="already excluded",
        )
        rows = self.ledger.list_open_items()
        self.assertEqual(["i-open"], [row["id"] for row in rows])
        self.assertEqual("context-p1", rows[0]["prompt_context"])

    def test_list_open_items_respects_limit(self):
        self._seed_prompt("p1")
        self._seed_prompt("p2")
        self.ledger.add_item("i1", prompt_id="p1", kind="directive", body="b", weight="hard")
        self.ledger.add_item("i2", prompt_id="p2", kind="directive", body="b", weight="hard")
        rows = self.ledger.list_open_items(limit=1)
        self.assertEqual(1, len(rows))

    def test_needs_review_status_requires_a_reason(self):
        """agent-supervisor#652: same contract as 'dropped' -- a status that
        excludes an item from `unacknowledged` on an unconfirmed judgement
        must always carry the reason a later reviewer needs."""
        self._seed_prompt("p1")
        with self.assertRaises(ValueError):
            self.ledger.add_item(
                "i1", prompt_id="p1", kind="directive", body="b", weight="hard", status="needs_review",
            )
        row = self.ledger.add_item(
            "i2", prompt_id="p1", kind="directive", body="b", weight="hard",
            status="needs_review", status_reason="candidate synthetic fixture, unconfirmed",
        )
        self.assertEqual("needs_review", row["status"])

    def test_needs_review_view_is_needs_review_items_only(self):
        self._seed_prompt("p1")
        self.ledger.add_item("i-open", prompt_id="p1", kind="directive", body="b", weight="hard", status="open")
        self.ledger.add_item(
            "i-review", prompt_id="p1", kind="directive", body="b", weight="hard",
            status="needs_review", status_reason="candidate, unconfirmed",
        )
        self.ledger.add_item(
            "i-dropped", prompt_id="p1", kind="directive", body="b", weight="hard",
            status="dropped", status_reason="excluded",
        )
        ids = {row["id"] for row in self._raw("SELECT id FROM needs_review")}
        self.assertEqual({"i-review"}, ids)

    def test_needs_review_item_excluded_from_unacknowledged(self):
        self._seed_prompt("p1")
        self.ledger.add_item(
            "i-review", prompt_id="p1", kind="directive", body="b", weight="hard",
            status="needs_review", status_reason="candidate, unconfirmed",
        )
        self.assertEqual([], self.ledger.read_prompt_view("unacknowledged"))

    def test_flag_needs_review_requires_a_reason(self):
        self._seed_prompt("p1")
        self.ledger.add_item("i1", prompt_id="p1", kind="directive", body="b", weight="hard")
        with self.assertRaises(ValueError):
            self.ledger.flag_needs_review("i1", "")

    def test_flag_needs_review_rejects_an_unknown_id(self):
        with self.assertRaises(ValueError):
            self.ledger.flag_needs_review("nope", "reason")

    def test_flag_needs_review_corrects_status_in_place_without_touching_judgement_fields(self):
        """agent-supervisor#652: same "reviewable and reversible" contract
        `drop_item` already honours -- kind/body/weight/id are untouched,
        only status/status_reason move."""
        self._seed_prompt("p1")
        self.ledger.add_item("i1", prompt_id="p1", kind="directive", body="do it", weight="hard")
        row = self.ledger.flag_needs_review("i1", "candidate synthetic fixture, unconfirmed")
        self.assertEqual("needs_review", row["status"])
        self.assertEqual("candidate synthetic fixture, unconfirmed", row["status_reason"])
        self.assertEqual("directive", row["kind"])
        self.assertEqual("do it", row["body"])
        self.assertEqual("hard", row["weight"])
        self.assertEqual([], self.ledger.read_prompt_view("unacknowledged"))


