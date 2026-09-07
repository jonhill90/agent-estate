#!/usr/bin/env python3
"""Tests for lint_docs.py, mirroring scripts/knowledge/vault/
test_validate_index.py's unittest-based, tmp-tree-per-test shape."""
import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path

SPEC = importlib.util.spec_from_file_location(
    "lint_docs", Path(__file__).resolve().parent / "lint_docs.py"
)
lint_docs = importlib.util.module_from_spec(SPEC)
sys.modules["lint_docs"] = lint_docs
SPEC.loader.exec_module(lint_docs)


def write(path, text):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


class TestUnclassifiedRootFiles(unittest.TestCase):
    def test_clean_tree_no_root_files(self):
        with tempfile.TemporaryDirectory() as d:
            docs = Path(d) / "docs"
            write(docs / "canonical" / "a.md", "# a\n")
            self.assertEqual(lint_docs.check_unclassified_root_files(docs), [])

    def test_loose_root_file_is_a_violation(self):
        with tempfile.TemporaryDirectory() as d:
            docs = Path(d) / "docs"
            write(docs / "stray.md", "# stray\n\nno classification, no tombstone marker.\n")
            v = lint_docs.check_unclassified_root_files(docs)
            self.assertEqual(len(v), 1)
            self.assertIn("stray.md", v[0])

    def test_tombstone_is_exempt(self):
        with tempfile.TemporaryDirectory() as d:
            docs = Path(d) / "docs"
            write(
                docs / "moved.md",
                "Moved to `docs/canonical/moved.md` (P12 Phase 2, run/p12-execution-plan.md's\n"
                "docs-classification sort). Consumers: update the link, do not restore this file.\n",
            )
            self.assertEqual(lint_docs.check_unclassified_root_files(docs), [])

    def test_historical_tombstone_is_exempt(self):
        with tempfile.TemporaryDirectory() as d:
            docs = Path(d) / "docs"
            write(
                docs / "moved.md",
                "Moved to `docs/historical/moved.md` (P12 Phase 2, run/p12-execution-plan.md's\n"
                "docs-classification sort). Consumers: update the link, do not restore this file.\n",
            )
            self.assertEqual(lint_docs.check_unclassified_root_files(docs), [])

    def test_allowlisted_root_file_is_exempt(self):
        with tempfile.TemporaryDirectory() as d:
            docs = Path(d) / "docs"
            write(docs / "knowledge-workflow.md", "# real content, Phase 4 owned\n")
            self.assertEqual(lint_docs.check_unclassified_root_files(docs), [])

    def test_prose_mentioning_moved_to_without_the_real_prefix_still_violates(self):
        # A file that happens to start its first line with "Moved to" but
        # not in the exact tombstone shape must NOT be waved through --
        # the marker is matched literally, not sniffed for the substring.
        with tempfile.TemporaryDirectory() as d:
            docs = Path(d) / "docs"
            write(docs / "decoy.md", "Moved to a new house last year, unrelated content.\n")
            v = lint_docs.check_unclassified_root_files(docs)
            self.assertEqual(len(v), 1)
            self.assertIn("decoy.md", v[0])


class TestNoStateFiles(unittest.TestCase):
    def test_clean_tree_no_state_files(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            docs = root / "docs"
            write(docs / "canonical" / "a.md", "# a\n")
            self.assertEqual(lint_docs.check_no_state_files(docs, root), [])

    def test_new_jsonl_is_a_violation(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            docs = root / "docs"
            write(docs / "some-log.jsonl", '{"at":"now"}\n')
            v = lint_docs.check_no_state_files(docs, root)
            self.assertEqual(len(v), 1)
            self.assertIn("some-log.jsonl", v[0])

    def test_json_is_also_a_violation(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            docs = root / "docs"
            write(docs / "state.json", "{}")
            v = lint_docs.check_no_state_files(docs, root)
            self.assertEqual(len(v), 1)
            self.assertIn("state.json", v[0])

    def test_named_exemptions_do_not_violate(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            docs = root / "docs"
            write(docs / "tick-log.jsonl", '{"at":"now"}\n')
            write(docs / "tick-escalations.jsonl", '{"at":"now"}\n')
            self.assertEqual(lint_docs.check_no_state_files(docs, root), [])

    def test_state_file_in_a_subdirectory_still_violates(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            docs = root / "docs"
            write(docs / "canonical" / "hidden.jsonl", '{"at":"now"}\n')
            v = lint_docs.check_no_state_files(docs, root)
            self.assertEqual(len(v), 1)
            self.assertIn("hidden.jsonl", v[0])


class TestNoDuplicates(unittest.TestCase):
    def test_clean_tree_no_duplicates(self):
        with tempfile.TemporaryDirectory() as d:
            docs = Path(d) / "docs"
            write(docs / "a.md", "# a\nunique body one\n")
            write(docs / "b.md", "# b\nunique body two\n")
            self.assertEqual(lint_docs.check_no_duplicates(docs), [])

    def test_byte_identical_files_are_a_violation(self):
        with tempfile.TemporaryDirectory() as d:
            docs = Path(d) / "docs"
            body = "# same\nidentical content in both files\n"
            write(docs / "a.md", body)
            write(docs / "b.md", body)
            v = lint_docs.check_no_duplicates(docs)
            self.assertEqual(len(v), 1)
            self.assertIn(str(docs / "a.md"), v[0])
            self.assertIn(str(docs / "b.md"), v[0])

    def test_near_identical_but_not_byte_identical_is_not_a_violation(self):
        with tempfile.TemporaryDirectory() as d:
            docs = Path(d) / "docs"
            write(docs / "a.md", "# same\nidentical content in both files\n")
            write(docs / "b.md", "# same\nidentical content in both files -- extra.\n")
            self.assertEqual(lint_docs.check_no_duplicates(docs), [])


class TestCheckComposition(unittest.TestCase):
    def test_clean_tree_yields_no_violations_from_any_check(self):
        # main() is a thin composition of the three check_* functions over
        # repo_root/docs, derived from __file__ -- exercised via the real
        # invocation in the PR body's red/green proof instead of mocked
        # here, since faking __file__-relative resolution would test the
        # mock, not the lint. This exercises exactly what main() composes.
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            write(root / "docs" / "canonical" / "a.md", "# a\nunique\n")
            write(root / "docs" / "tick-log.jsonl", '{"at":"now"}\n')
            violations = (
                lint_docs.check_unclassified_root_files(root / "docs")
                + lint_docs.check_no_state_files(root / "docs", root)
                + lint_docs.check_no_duplicates(root / "docs")
            )
            self.assertEqual(violations, [])

    def test_violations_are_all_surfaced_together(self):
        # Exercise the three check_* functions the way main() composes them,
        # without needing to fake __file__-relative path resolution.
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            docs = root / "docs"
            write(docs / "stray.md", "# stray\nunclassified\n")
            write(docs / "leak.json", "{}")
            body = "# dup\nsame\n"
            write(docs / "canonical" / "a.md", body)
            write(docs / "canonical" / "b.md", body)

            violations = (
                lint_docs.check_unclassified_root_files(docs)
                + lint_docs.check_no_state_files(docs, root)
                + lint_docs.check_no_duplicates(docs)
            )
            self.assertEqual(len(violations), 3)


if __name__ == "__main__":
    unittest.main()
