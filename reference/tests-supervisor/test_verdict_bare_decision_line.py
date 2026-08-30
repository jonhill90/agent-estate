import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import (  # noqa: E402
    _VERDICT_LINE_RE,
    _bare_decision_line,
    _classify_decision_text,
    _normalise_decision_text,
    _parse_verdict_comment,
)


class BareDecisionLineTests(unittest.TestCase):
    """agent-supervisor#475 case 3 (agent-supervisor#472, round 2) used to be
    a FEATURE here: a lane wrote `Verdict:` on its own line at the top of a
    long comment and `APPROVE` as a standalone line at the very end, and
    `_bare_decision_line` scanned the rest of the comment for that standalone
    decision to adopt when the label's own text was empty.

    agent-supervisor#595 RETIRES that feature, not merely leaves it alone.
    `_scan_verdict_lines` now requires an operative `Verdict:` match to be
    the first line of an unbroken three-line `Verdict:`/`Review-Lane:`/
    `Reviewed-SHA:` block -- and the #475 case-3 shape structurally can
    never satisfy that: an empty `Verdict:` label is, by definition, not
    immediately followed by a `Review-Lane:` line (it is followed by prose,
    then eventually a bare decision word many lines later). Keeping the old
    fallback wired in would have meant a bare, untrailered label could still
    manufacture an operative verdict by scanning forward for a decision
    word -- exactly the kind of unanchored inference #595 closes off. See
    the comment at `_scan_verdict_lines`'s own (removed) call site in
    `scripts/supervisor/verdict.py` for the same reasoning stated next to
    the code.

    What used to be `POSITIVE_CASES` (shapes that used to resolve to a
    decision) now correctly resolve to `None` -- an empty `Verdict:` label
    is just another unrecognised/incomplete line now, the same fail-closed
    answer any other label with nothing genuine following it gets. The two
    tests exercising `_bare_decision_line` directly are kept: that function
    is still defined (only its WIRING into `_scan_verdict_lines` was
    removed), and its own pure-function behaviour is still real code worth
    covering."""

    RETIRED_CASES = [
        (
            "#475 case 3 itself: empty label at the top, bare APPROVE at the end -- now None",
            "Verdict:\n\nThe patch-id comparison looks right, tests pass, "
            "and the freshness check covers the rebase case correctly.\n\nAPPROVE",
        ),
        (
            "empty label, bare REQUEST CHANGES at the end -- now None",
            "Verdict:\n\nThe freshness check does not compare the base branch correctly.\n\nREQUEST CHANGES",
        ),
        (
            "empty label, bare decision immediately below it -- now None",
            "Verdict:\nAPPROVE",
        ),
    ]

    NEGATIVE_CASES = [
        (
            "no Verdict: label at all -- a bare APPROVE alone must not become a verdict",
            "Looks fine to me.\n\nAPPROVE",
        ),
        (
            "empty label, two conflicting bare lines -- refuses as ambiguous",
            "Verdict:\n\nAPPROVE\n\nOn reflection:\n\nREQUEST CHANGES",
        ),
        (
            "empty label, bare decision only inside a fenced code block -- not consulted",
            "Verdict:\n\n```\nAPPROVE\n```",
        ),
        (
            "empty label, bare decision only inside a blockquote -- not consulted",
            "Verdict:\n\n> APPROVE",
        ),
        (
            "empty label, no bare decision anywhere -- unchanged from before the fix",
            "Verdict:\n\nStill reviewing, no conclusion yet.",
        ),
    ]

    def test_every_retired_case_now_resolves_to_none(self):
        """agent-supervisor#595 supersedes #475 case 3: an empty `Verdict:`
        label with a bare decision word elsewhere in the comment is no
        longer promoted to a verdict -- it never had a complete trailer
        block, so it was never operative under the new rule."""
        for name, body in self.RETIRED_CASES:
            with self.subTest(name):
                self.assertIsNone(_parse_verdict_comment(body))

    def test_every_negative_case_reads_none(self):
        for name, body in self.NEGATIVE_CASES:
            with self.subTest(name):
                self.assertIsNone(_parse_verdict_comment(body))

    def test_mutation_reintroducing_the_bare_scan_wiring_would_turn_case_3_green_again(self):
        """The inverse of this file's usual mutation-test idiom: this class
        asserts a FEATURE was removed, so the "mutation" that would turn the
        RETIRED_CASES table's own outcome around is re-wiring the retired
        `_bare_decision_line` fallback back into a from-scratch scan --
        proving the retired case's `None` result actually depends on the
        wiring being gone, not on some unrelated accident."""

        def pre_595_scan_with_bare_fallback(body):
            in_fence = False
            lines = []
            for raw_line in (body or "").splitlines():
                line = raw_line.strip()
                if line.startswith("```"):
                    in_fence = not in_fence
                    continue
                if in_fence or line.startswith(">"):
                    continue
                lines.append(line)

            results = []
            has_empty_label = False
            for line in lines:
                match = _VERDICT_LINE_RE.match(line)
                if not match:
                    continue
                decision_text = _normalise_decision_text(match.group(2))
                if decision_text == "":
                    has_empty_label = True
                results.append((_classify_decision_text(decision_text), decision_text))

            if has_empty_label:
                bare = _bare_decision_line(lines)
                if bare is not None:
                    bare_decision, bare_text = bare
                    results = [
                        (bare_decision, bare_text) if decision is None and text == "" else (decision, text)
                        for decision, text in results
                    ]
            return results

        def pre_595_parse(body):
            decisions = {d for d, _ in pre_595_scan_with_bare_fallback(body) if d is not None}
            if len(decisions) == 1:
                return decisions.pop()
            return None

        case_3 = next(body for name, body in self.RETIRED_CASES if "case 3 itself" in name)
        self.assertIsNone(_parse_verdict_comment(case_3), "the real, current parser must NOT resolve this")
        self.assertEqual(
            pre_595_parse(case_3), "approved",
            "re-wiring the old fallback must still resolve this -- proving the retirement, not an accident, is why the real parser now refuses",
        )

    def test_bare_decision_line_ignores_lines_with_their_own_label(self):
        """A line that already carries its own `Verdict:` label is never a
        BARE candidate -- it is handled (and classified) by the label scan
        itself; treating it as bare too would double-count it. This tests
        `_bare_decision_line` directly, as a pure function -- it is no
        longer called from `_scan_verdict_lines` (agent-supervisor#595), but
        the function itself is still real code."""
        self.assertIsNone(_bare_decision_line(["Verdict: APPROVE"]))

    def test_bare_decision_line_empty_input_reads_none(self):
        self.assertIsNone(_bare_decision_line([]))
