import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parents[1] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import mine_prompts  # noqa: E402
from core import Ledger  # noqa: E402


def _line(role, text, at="2026-08-16T10:00:00Z"):
    return json.dumps({"timestamp": at, "message": {"role": role, "content": text}}) + "\n"


class StateDirDefaultTests(unittest.TestCase):
    """agent-estate#1357 (part 1's sibling fix): `--state-dir`'s default,
    with no env override, resolves to the corpus, not the dead
    ~/.local/state/agent-dotfiles-supervisor directory -- the same defect
    class prompt_capture_hook.py had, in the script #1357's own text names
    as responsible for "September 2026 had 994 prompts on disk and 386 in
    the corpus". Runs the real CLI end to end (`--store` against one
    synthetic transcript) with a fake $HOME, so this never risks touching
    the real ~/corpus, and checks WHICH directory the ledger actually got
    created under -- not merely what a re-derived default string would be."""

    def _run_store(self, env):
        with tempfile.TemporaryDirectory() as root:
            # harvest() globs root/*/​*.jsonl -- one level of session
            # subdirectory between --root and the transcript file itself.
            session_dir = Path(root) / "fake-session"
            session_dir.mkdir()
            (session_dir / "session.jsonl").write_text(
                _line("user", "a real directive for the state-dir default test")
            )
            result = subprocess.run(
                [sys.executable, str(SUPERVISOR_DIR / "mine_prompts.py"),
                 "--root", root, "--store"],
                capture_output=True,
                text=True,
                env=env,
                timeout=30,
            )
            return result

    def test_default_state_dir_is_the_corpus_not_the_dead_state_dir(self):
        with tempfile.TemporaryDirectory() as fake_home:
            result = self._run_store({"PATH": os.environ.get("PATH", ""), "HOME": fake_home})
            self.assertEqual(0, result.returncode, msg=result.stderr)
            self.assertIn("stored: 1 written", result.stdout)

            corpus_db = Path(fake_home) / "corpus" / "ledger.sqlite3"
            dead_db = Path(fake_home) / ".local" / "state" / "agent-dotfiles-supervisor" / "ledger.sqlite3"
            self.assertTrue(corpus_db.exists(), f"expected the ledger under the corpus dir, at {corpus_db}")
            self.assertFalse(
                dead_db.exists(),
                "mine_prompts.py must never create/touch a ledger under the dead supervisor "
                "state dir by default (agent-estate#942/#1357)",
            )

    def test_agent_corpus_dir_env_var_overrides_the_default(self):
        with tempfile.TemporaryDirectory() as fake_home, tempfile.TemporaryDirectory() as override_dir:
            result = self._run_store({
                "PATH": os.environ.get("PATH", ""),
                "HOME": fake_home,
                "AGENT_CORPUS_DIR": override_dir,
            })
            self.assertEqual(0, result.returncode, msg=result.stderr)
            self.assertTrue((Path(override_dir) / "ledger.sqlite3").exists())
            self.assertFalse((Path(fake_home) / "corpus" / "ledger.sqlite3").exists())


class HarvestContextTests(unittest.TestCase):
    """`context` must come from transcript shape alone -- the preceding
    assistant turn in the same file -- and say so honestly when there is
    none, never invent one (agent-supervisor#303)."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name) / "session.jsonl"

    def _harvest(self):
        return mine_prompts.harvest([str(self.path)], excludes=())

    def test_context_is_the_preceding_assistant_turn(self):
        self.path.write_text(
            _line("assistant", "deciding whether render mode is LIVE or PREVIEW")
            + _line("user", "make it LIVE", at="2026-08-16T10:00:05Z")
        )
        rows = self._harvest()
        self.assertEqual(1, len(rows))
        self.assertEqual(
            "deciding whether render mode is LIVE or PREVIEW", rows[0]["context"]
        )

    def test_context_is_undetermined_when_no_prior_assistant_turn(self):
        self.path.write_text(_line("user", "first thing said in this file"))
        rows = self._harvest()
        self.assertEqual(1, len(rows))
        self.assertEqual(mine_prompts.CONTEXT_UNDETERMINED, rows[0]["context"])

    def test_context_tracks_the_nearest_preceding_assistant_turn(self):
        self.path.write_text(
            _line("assistant", "first topic", at="2026-08-16T10:00:00Z")
            + _line("user", "about first topic", at="2026-08-16T10:00:01Z")
            + _line("assistant", "second topic", at="2026-08-16T10:00:02Z")
            + _line("user", "about second topic", at="2026-08-16T10:00:03Z")
        )
        rows = self._harvest()
        self.assertEqual(["first topic", "second topic"], [r["context"] for r in rows])


class StoreRowsTests(unittest.TestCase):
    """--store: idempotent, text_raw written once, context never invented."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.ledger = Ledger(self.tmp.name, clock=lambda: 1_000)

    def _rows(self):
        return [
            {
                "at": "2026-08-16T10:00:05Z",
                "text": "make it LIVE",
                "typed": True,
                "source": "session.jsonl",
                "context": "deciding render mode",
            }
        ]

    def test_first_store_writes_the_row(self):
        written, skipped, no_time = mine_prompts.store_rows(self._rows(), self.ledger)
        self.assertEqual((1, 0, 0), (written, skipped, no_time))
        prompt_id = mine_prompts._prompt_id(self._rows()[0])
        stored = self.ledger.get_prompt(prompt_id)
        self.assertEqual("make it LIVE", stored["text_raw"])
        self.assertEqual("deciding render mode", stored["context"])

    def test_second_store_over_the_same_rows_is_a_no_op(self):
        mine_prompts.store_rows(self._rows(), self.ledger)
        written, skipped, no_time = mine_prompts.store_rows(self._rows(), self.ledger)
        self.assertEqual((0, 1, 0), (written, skipped, no_time))
        prompt_id = mine_prompts._prompt_id(self._rows()[0])
        stored = self.ledger.get_prompt(prompt_id)
        self.assertEqual("make it LIVE", stored["text_raw"])  # unchanged, not rewritten

    def test_row_with_unparseable_timestamp_is_skipped_not_fabricated(self):
        rows = self._rows()
        rows[0]["at"] = "not-a-timestamp"
        written, skipped, no_time = mine_prompts.store_rows(rows, self.ledger)
        self.assertEqual((0, 0, 1), (written, skipped, no_time))


if __name__ == "__main__":
    unittest.main()
