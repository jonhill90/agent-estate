"""The brief's resume block is generated, so the generator has to be safe.

Three failure modes matter, in this order:

1. It must never damage the prose outside the markers. That prose is judgement
   somebody wrote down; a stale heading is recoverable, a destroyed brief is not.
2. It must refuse rather than guess when the markers are absent.
3. A query it could not run must render as "could not read", never as "none".
   That conflation -- unreadable reported as absent -- is the defect this estate
   has hit most often (#59, #92, #95, #100), and a generated brief that says
   "issues: none" because `gh` was offline would put it in the one document a
   cold session trusts first.
"""

from __future__ import annotations

import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import refresh_brief_resume as r  # noqa: E402


BRIEF = f"""# Brief

Prose above that must survive untouched.

{r.BEGIN}
## Resume point — generated 1999-01-01 00:00 UTC
stale content
{r.END}

## A section below

More prose that must survive untouched.
"""


class SpliceTests(unittest.TestCase):
    def test_prose_outside_the_markers_is_untouched(self):
        out = r.splice(BRIEF, f"{r.BEGIN}\nNEW\n{r.END}")
        self.assertIn("Prose above that must survive untouched.", out)
        self.assertIn("More prose that must survive untouched.", out)
        self.assertIn("## A section below", out)
        self.assertIn("NEW", out)
        self.assertNotIn("stale content", out)

    def test_missing_markers_refuse_rather_than_guess(self):
        with self.assertRaises(SystemExit) as caught:
            r.splice("# Brief\n\nNo markers here.\n", f"{r.BEGIN}\nNEW\n{r.END}")
        self.assertIn("markers not found", str(caught.exception))

    def test_a_second_run_is_idempotent(self):
        once = r.splice(BRIEF, f"{r.BEGIN}\nNEW\n{r.END}")
        twice = r.splice(once, f"{r.BEGIN}\nNEW\n{r.END}")
        self.assertEqual(once, twice)


class UnreadableIsNotAbsentTests(unittest.TestCase):
    def test_a_failed_gh_query_is_not_rendered_as_none(self):
        with patch.object(r, "_run", return_value=None):
            self.assertIn("could not read", r.open_items("agent-dotfiles", "pr"))

    def test_malformed_json_is_not_rendered_as_none(self):
        with patch.object(r, "_run", return_value="not json at all"):
            self.assertIn("could not parse", r.open_items("agent-dotfiles", "issue"))

    def test_a_genuinely_empty_list_says_none(self):
        # The other half of the contract: "none" must still be reachable, or the
        # distinction above is worthless.
        with patch.object(r, "_run", return_value="[]"):
            self.assertEqual("none", r.open_items("agent-dotfiles", "pr"))

    def test_an_unreadable_sha_is_labelled_not_blank(self):
        with patch.object(r, "_run", return_value=None), \
             patch.object(Path, "is_dir", return_value=True):
            self.assertEqual("unreadable", r.repo_head("agent-dotfiles"))

    def test_a_missing_checkout_is_distinguished_from_an_unreadable_one(self):
        with patch.object(Path, "is_dir", return_value=False):
            self.assertEqual("no local checkout", r.repo_head("nope"))


class CheckModeTests(unittest.TestCase):
    def test_check_mode_writes_nothing(self):
        with tempfile.TemporaryDirectory() as d:
            brief = Path(d) / "brief.md"
            brief.write_text(BRIEF)
            before = brief.read_text()
            with patch.object(r, "build_block", return_value=f"{r.BEGIN}\nDIFFERENT\n{r.END}"):
                rc = r.main(["--brief", str(brief), "--check"])
            self.assertEqual(1, rc)
            self.assertEqual(before, brief.read_text())

    def test_check_mode_reports_current_when_unchanged(self):
        with tempfile.TemporaryDirectory() as d:
            brief = Path(d) / "brief.md"
            same = f"{r.BEGIN}\n## Resume point — generated 1999-01-01 00:00 UTC\nstale content\n{r.END}"
            brief.write_text(BRIEF)
            with patch.object(r, "build_block", return_value=same):
                self.assertEqual(0, r.main(["--brief", str(brief), "--check"]))

    def test_a_missing_brief_is_an_error_not_a_silent_success(self):
        with tempfile.TemporaryDirectory() as d:
            self.assertEqual(2, r.main(["--brief", str(Path(d) / "nope.md")]))


class TestCountHonestyTests(unittest.TestCase):
    def test_it_says_so_when_tests_were_not_run(self):
        with patch.object(r, "repo_head", return_value="abc1234"), \
             patch.object(r, "open_items", return_value="none"):
            self.assertIn("not run by this refresh", r.build_block(None))

    def test_it_reports_counts_it_was_given(self):
        with patch.object(r, "repo_head", return_value="abc1234"), \
             patch.object(r, "open_items", return_value="none"):
            self.assertIn("399 python", r.build_block("399 python, 0 failed"))


class CheckIgnoresTheGenerationStampTests(unittest.TestCase):
    """--check must be able to say "current". The block embeds
    "generated <when>", which changes every run, so a naive equality comparison
    reports STALE unconditionally -- a check that always fires carries exactly
    as much information as one that never does. Found by running the tool
    against the real brief, not by reading it.
    """

    def test_only_the_timestamp_changing_is_not_stale(self):
        with tempfile.TemporaryDirectory() as d:
            brief = Path(d) / "brief.md"
            body = "agent-dotfiles  main abc1234"
            brief.write_text(
                f"# B\n\n{r.BEGIN}\n## Resume point — generated 1999-01-01 00:00 UTC\n{body}\n{r.END}\n"
            )
            newer = f"{r.BEGIN}\n## Resume point — generated 2030-12-31 23:59 UTC\n{body}\n{r.END}"
            with patch.object(r, "build_block", return_value=newer):
                self.assertEqual(0, r.main(["--brief", str(brief), "--check"]))

    def test_real_content_change_is_still_stale(self):
        # The other direction, or the fix above would just disable the check.
        with tempfile.TemporaryDirectory() as d:
            brief = Path(d) / "brief.md"
            brief.write_text(
                f"# B\n\n{r.BEGIN}\n## Resume point — generated 1999-01-01 00:00 UTC\nmain abc1234\n{r.END}\n"
            )
            newer = f"{r.BEGIN}\n## Resume point — generated 1999-01-01 00:00 UTC\nmain DIFFERENT\n{r.END}"
            with patch.object(r, "build_block", return_value=newer):
                self.assertEqual(1, r.main(["--brief", str(brief), "--check"]))


class AdversarialMarkerTests(unittest.TestCase):
    """Every case here was constructed by an adversarial review of this tool and
    silently corrupted or silently no-op'd before the fix. Each must now REFUSE.

    The out-of-order case is the one that mattered most: the CLI printed
    "rewrote the resume block", exited 0, changed nothing, and --check then
    reported "current" -- a malformed brief certified as fresh, which is worse
    than the staleness this tool exists to remove.
    """

    def _refuses(self, text, expect):
        with self.assertRaises(SystemExit) as caught:
            r.splice(text, f"{r.BEGIN}\nNEW\n{r.END}")
        self.assertIn(expect, str(caught.exception))

    def test_two_marker_pairs_are_refused(self):
        self._refuses(
            f"a\n{r.BEGIN}\none\n{r.END}\nb\n{r.BEGIN}\ntwo\n{r.END}\nc\n",
            "exactly one marker pair",
        )

    def test_nested_markers_are_refused(self):
        self._refuses(
            f"a\n{r.BEGIN}\nouter\n{r.BEGIN}\ninner\n{r.END}\ntail\n{r.END}\n",
            "exactly one marker pair",
        )

    def test_markers_out_of_order_are_refused(self):
        self._refuses(f"a\n{r.END}\nb\n{r.BEGIN}\nc\n", "END appears before BEGIN")

    def test_a_marker_pasted_in_a_code_fence_is_refused(self):
        # This repo's own docstrings and PR bodies paste the marker syntax, so a
        # second pair landing in the brief as documentation is realistic.
        self._refuses(
            f"# B\n\n```\n{r.BEGIN}\nexample\n{r.END}\n```\n\n{r.BEGIN}\nreal\n{r.END}\n",
            "exactly one marker pair",
        )

    def test_a_begin_with_no_end_is_refused(self):
        self._refuses(f"a\n{r.BEGIN}\nb\n", "markers not found")

    def test_the_ordinary_single_pair_still_works(self):
        # Or every assertion above is satisfied by a tool that refuses always.
        out = r.splice(BRIEF, f"{r.BEGIN}\nNEW\n{r.END}")
        self.assertIn("NEW", out)
        self.assertIn("Prose above that must survive untouched.", out)


class AtomicWriteTests(unittest.TestCase):
    def test_a_failed_write_leaves_the_brief_intact(self):
        """A partial write truncates the cold-start document and nothing else
        holds a copy of its prose."""
        with tempfile.TemporaryDirectory() as d:
            brief = Path(d) / "brief.md"
            brief.write_text(BRIEF)
            original = brief.read_text()
            with patch.object(r, "build_block", return_value=f"{r.BEGIN}\nNEW\n{r.END}"), \
                 patch("os.replace", side_effect=OSError("disk full")):
                with self.assertRaises(OSError):
                    r.main(["--brief", str(brief)])
            self.assertEqual(original, brief.read_text())


class PermissionsAndTruncationTests(unittest.TestCase):
    """Both from re-review. Both are the same family this tool is otherwise
    careful about: something incomplete or altered, rendered as if it were not.
    """

    def test_the_write_preserves_the_briefs_mode(self):
        """os.replace carries the TEMP file's mode onto the target, so a brief
        at 0600 came back 0644 -- the write silently widened permissions on the
        file it maintains. #103 is hardening this state directory to 0700/0600.
        """
        with tempfile.TemporaryDirectory() as d:
            brief = Path(d) / "brief.md"
            brief.write_text(BRIEF)
            brief.chmod(0o600)
            with patch.object(r, "build_block", return_value=f"{r.BEGIN}\nNEW\n{r.END}"):
                self.assertEqual(0, r.main(["--brief", str(brief)]))
            self.assertEqual(0o600, brief.stat().st_mode & 0o777)

    def test_a_truncated_list_says_it_is_truncated(self):
        rows = json.dumps([{"number": n, "title": "x"} for n in range(1, 60)])
        with patch.object(r, "_run", return_value=rows):
            out = r.open_items("agent-dotfiles", "issue")
        self.assertIn("and more", out)
        self.assertIn("showing 40", out)

    def test_a_complete_list_does_not_claim_truncation(self):
        # The other direction, or the fix is just a label on everything.
        rows = json.dumps([{"number": n, "title": "x"} for n in range(1, 5)])
        with patch.object(r, "_run", return_value=rows):
            out = r.open_items("agent-dotfiles", "issue")
        self.assertNotIn("and more", out)
        self.assertIn("#4", out)


if __name__ == "__main__":
    unittest.main()
