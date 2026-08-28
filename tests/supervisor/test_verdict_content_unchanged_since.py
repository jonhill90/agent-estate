import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import (  # noqa: E402
    _content_unchanged_since,
    _default_patch_id,
)

from tests.supervisor.test_verdict_helpers import (  # noqa: E402
    BASE_REF,
    REPO,
    _api_runner,
    _patch,
    _raising_runner,
)


REBASE_DIFF_OLD_OFFSET = (
    "diff --git a/foo.txt b/foo.txt\n"
    "index 1111111..2222222 100644\n"
    "--- a/foo.txt\n"
    "+++ b/foo.txt\n"
    "@@ -10,3 +10,3 @@\n"
    " line before\n"
    "-old line\n"
    "+new line\n"
    " line after\n"
)


REBASE_DIFF_NEW_OFFSET = (
    "diff --git a/foo.txt b/foo.txt\n"
    "index 3333333..4444444 100644\n"
    "--- a/foo.txt\n"
    "+++ b/foo.txt\n"
    "@@ -25,3 +25,3 @@\n"
    " line before\n"
    "-old line\n"
    "+new line\n"
    " line after\n"
)


CONTENT_CHANGED_DIFF = (
    "diff --git a/foo.txt b/foo.txt\n"
    "index 3333333..5555555 100644\n"
    "--- a/foo.txt\n"
    "+++ b/foo.txt\n"
    "@@ -25,3 +25,3 @@\n"
    " line before\n"
    "-old line\n"
    "+a genuinely different line\n"
    " line after\n"
)


class ContentUnchangedSinceTests(unittest.TestCase):
    """Direct tests of the #226 comparison primitive, independent of which
    verdict source calls it."""

    OLD, NEW = "a" * 40, "c" * 40

    def _compare(self, **kwargs):
        return _content_unchanged_since(
            patch_id_fn=_default_patch_id, repo=REPO, number=1, old_sha=self.OLD, new_sha=self.NEW, **kwargs
        )

    def test_identical_shas_are_trivially_unchanged(self):
        unchanged, _ = _content_unchanged_since(
            runner=_raising_runner,
            patch_id_fn=lambda d: "x",
            repo=REPO,
            number=1,
            old_sha="a" * 40,
            new_sha="a" * 40,
        )
        self.assertTrue(unchanged)

    def test_matching_patch_ids_read_unchanged(self):
        unchanged, basis = self._compare(
            runner=_api_runner(
                branches={self.OLD: [self.OLD], self.NEW: [self.NEW]},
                patches={self.OLD: _patch("only", offset=10), self.NEW: _patch("only", offset=99)},
            )
        )
        self.assertTrue(unchanged)
        self.assertIn("patch-id", basis)
        self.assertIn(BASE_REF, basis)

    def test_differing_patch_ids_read_changed_not_unknown(self):
        unchanged, _ = self._compare(
            runner=_api_runner(
                branches={self.OLD: [self.OLD], self.NEW: [self.NEW]},
                patches={
                    self.OLD: _patch("only", offset=10),
                    self.NEW: _patch("only", offset=99, replacement="a genuinely different line"),
                },
            )
        )
        self.assertIs(unchanged, False)

    def test_229_a_rebase_onto_a_moved_main_is_still_unchanged(self):
        """The case the first implementation got wrong, in its own shape:
        `main` gained commits under the branch. Those commits belong to
        neither side's list, because each side is measured from its own
        merge-base with the base branch -- so the branch's two patches
        still match and the verdict is promoted."""
        unchanged, basis = self._compare(
            runner=_api_runner(
                branches={self.OLD: ["o1", self.OLD], self.NEW: ["n1", self.NEW]},
                patches={
                    "o1": _patch("first", offset=10),
                    self.OLD: _patch("second", offset=20),
                    "n1": _patch("first", offset=310),
                    self.NEW: _patch("second", offset=420),
                },
            )
        )
        self.assertTrue(unchanged)
        self.assertIn("identical set of 2", basis)

    def test_229_a_rebase_that_drops_a_superseded_commit_is_unchanged_and_says_so(self):
        """agent-dotfiles#226's OWN example has this shape, measured
        2026-08-12: `0538cc6` -> `69784bd` dropped a `known_references.json`
        refresh because upstream #210 replaced that file with `.txt`. Nothing
        unreviewed entered, so the verdict is promoted -- and the count of
        dropped patches is stated, because a reader must be able to see that
        the branch is not byte-for-byte what was approved."""
        unchanged, basis = self._compare(
            runner=_api_runner(
                branches={self.OLD: ["o1", "o2", self.OLD], self.NEW: ["n1", self.NEW]},
                patches={
                    "o1": _patch("first", offset=10),
                    "o2": _patch("superseded", offset=10),
                    self.OLD: _patch("second", offset=20),
                    "n1": _patch("first", offset=310),
                    self.NEW: _patch("second", offset=420),
                },
            )
        )
        self.assertTrue(unchanged)
        self.assertIn("1 of 3", basis)

    def test_229_an_extra_commit_is_changed_not_unchanged(self):
        unchanged, _ = self._compare(
            runner=_api_runner(
                branches={self.OLD: [self.OLD], self.NEW: [self.OLD, self.NEW]},
                patches={self.OLD: _patch("first"), self.NEW: _patch("added")},
            )
        )
        self.assertIs(unchanged, False)

    def test_a_diff_fetch_failure_reads_none_not_unchanged(self):
        unchanged, _ = self._compare(runner=_raising_runner)
        self.assertIsNone(unchanged)

    def test_an_unreadable_base_branch_reads_none_not_unchanged(self):
        """No base branch means no anchor, and an unanchored compare is the
        defect this fixes -- refuse to answer rather than fall back to it."""

        def runner(cmd):
            if cmd[:3] == ["gh", "pr", "view"]:
                return "{}"
            raise AssertionError("must not reach a compare with no base branch")

        unchanged, _ = self._compare(runner=runner)
        self.assertIsNone(unchanged)

    def test_a_commit_list_that_does_not_end_at_the_head_reads_none(self):
        """A truncated page understates the branch, which is the one error
        direction that could promote an unreviewed patch. Fail closed."""
        unchanged, _ = self._compare(
            runner=_api_runner(
                branches={self.OLD: [self.OLD], self.NEW: ["n1"]},
                patches={self.OLD: _patch("only"), "n1": _patch("only")},
            )
        )
        self.assertIsNone(unchanged)

    def test_a_commit_whose_patch_cannot_be_read_reads_none(self):
        unchanged, _ = self._compare(
            runner=_api_runner(
                branches={self.OLD: [self.OLD], self.NEW: [self.NEW]},
                patches={self.OLD: _patch("only"), self.NEW: "   \n"},
            )
        )
        self.assertIsNone(unchanged)

    def test_real_git_patch_id_normalises_hunk_offsets(self):
        """No mocking of patch-id itself -- proves the actual instrument
        (`git patch-id --stable`) does what #226 relies on: the same change
        at two different hunk offsets hashes equal, a genuinely different
        change does not."""
        id_old = _default_patch_id(REBASE_DIFF_OLD_OFFSET)
        id_new = _default_patch_id(REBASE_DIFF_NEW_OFFSET)
        id_changed = _default_patch_id(CONTENT_CHANGED_DIFF)
        self.assertIsNotNone(id_old)
        self.assertEqual(id_old, id_new)
        self.assertNotEqual(id_old, id_changed)

    def test_empty_diff_reads_none(self):
        self.assertIsNone(_default_patch_id(""))
        self.assertIsNone(_default_patch_id("   \n"))
