import re
import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import (  # noqa: E402
    _author_lane_line,
    _parse_author_lane,
)


class Issue513AuthorLaneTrailerTests(unittest.TestCase):
    """agent-supervisor#513: `_parse_author_lane` is the PR-body complement
    to `_parse_review_lane` -- measured against agent-dotfiles#308, which
    stamped `Author-Lane: estate:4` in a PR body that `git grep -rn
    'Author-Lane' -- scripts/` proved nothing ever read. Mirrors that
    function's own regex shape line for line (`_AUTHOR_LANE_LINE_RE`, no
    `\\s*` between the colon and the capture group) rather than
    re-deriving it, for the exact reason #232's own comment on
    `_REVIEW_LANE_LINE_RE` gives."""

    def test_lane_shaped_token_parses_despite_trailing_prose(self):
        """Same parse #232 fixed for Review-Lane: the first lane-shaped
        token on the line, ignoring trailing prose after it."""
        body = "Some PR body text.\n\nAuthor-Lane: estate:4 (opened this from my own dispatch)\n"
        self.assertEqual(_parse_author_lane(body), "estate:4")

    def test_absent_trailer_is_none_not_a_guess(self):
        self.assertIsNone(_parse_author_lane("no trailer of any kind here"))
        self.assertIsNone(_parse_author_lane(""))
        self.assertIsNone(_parse_author_lane(None))

    def test_unparseable_line_with_no_lane_token_and_no_ledger_is_none(self):
        body = "Author-Lane: nonsense with no lane token"
        self.assertIsNone(_parse_author_lane(body))

    def test_off_pane_lane_id_recognised_via_ledger_fallback(self):
        """agent-supervisor#292's own reasoning applies here too: a
        claude-print/pi-rpc lane id has no `<session>:<index>` shape for
        `_LANE_SHAPE_RE` to find, so a registered off-pane lane must still
        resolve through the ledger fallback, exactly like
        `_parse_review_lane`."""

        class _FakeLedger:
            def get_lane(self, lane_id):
                return {"lane": lane_id} if lane_id == "ad182-review-186" else None

        body = "Author-Lane: ad182-review-186\n"
        self.assertEqual(_parse_author_lane(body, ledger=_FakeLedger()), "ad182-review-186")
        self.assertIsNone(_parse_author_lane(body, ledger=None))

    def test_blank_author_lane_does_not_swallow_the_next_line(self):
        """The deliverable this issue names explicitly: `^\\s*Author-Lane:
        \\s*(.*)$` (a stray `\\s*` between the colon and the capture group)
        lets `\\s*` walk across the newline a blank trailer leaves and
        capture the FOLLOWING line's text as if it were the Author-Lane
        value -- the exact defect fixed for Review-Lane in skills#260,
        agent-tui#113 and agent-dotfiles#305, reproduced here for its
        Author-Lane sibling. `_AUTHOR_LANE_LINE_RE` has no such `\\s*`; a
        blank `Author-Lane:` line followed immediately by a line that
        itself contains a lane-shaped token (`Review-Lane: t:9`) must
        parse as no claim at all, never as `t:9`."""
        body = "Author-Lane:\nReview-Lane: t:9\n"
        # The raw matched line is the blank trailer alone, not the next line.
        self.assertEqual(_author_lane_line(body), "Author-Lane:")
        self.assertIsNone(_parse_author_lane(body))

    def test_mutation_reintroducing_the_swallow_regex_would_fail_the_above(self):
        """Proves the test above is real evidence, not a check that cannot
        fail (this repo's own CLAUDE.md requirement): re-derive the SAME
        parse with the historically-buggy pattern (`\\s*` reinstated
        between the colon and the capture group) and confirm it DOES
        swallow the next line -- i.e. that the fixed regex in
        `verdict.py` is doing real work, not passing by construction."""
        buggy_re = re.compile(r"(?im)^\s*Author-Lane:\s*(.*)$")
        body = "Author-Lane:\nReview-Lane: t:9\n"
        match = buggy_re.search(body)
        self.assertIsNotNone(match, "buggy regex should still match something")
        self.assertIn("t:9", match.group(1), "the buggy regex swallows the next line's content")
