"""agent-supervisor#338: a fix pass must paste the reviewer's reproduction
output, not just claim the defect is gone. This pins
`fixpass_evidence_gate.FixpassEvidenceGate`'s outcomes against a stubbed
`gh` response so the check itself is verified without touching the network
-- the same shape `test_ci_gate.py` already uses for `ci_gate.CiGate`.
`.github/workflows/fixpass-evidence.yml` is what wires this to actually run
on every PR; this suite is what proves the gate's own logic before that CI
wiring is trusted.
"""

import json
import sys
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from fixpass_evidence_gate import EVIDENCE_MARKER, FixpassEvidenceGate  # noqa: E402


class FakeRunner:
    """Answers the one `gh pr view` call the gate makes."""

    def __init__(self, *, reviews=None, body="", comments=None):
        self.reviews = reviews or []
        self.body = body
        self.comments = comments or []
        self.calls = []

    def __call__(self, command, *, timeout=None):
        self.calls.append(command)
        if command[:3] == ["gh", "pr", "view"]:
            return json.dumps(
                {
                    "reviews": self.reviews,
                    "body": self.body,
                    "comments": [{"body": c} for c in self.comments],
                }
            )
        raise AssertionError(f"unexpected command: {command}")


GOOD_BLOCK = (
    f"{EVIDENCE_MARKER}\n"
    "$ python3 scripts/supervisor/cli.py worktree-lane --path bogus\n"
    '{"known": false, "lane": null}\n'
    "exit code: 0\n"
)


class FixpassEvidenceGateTest(unittest.TestCase):
    # --- "none required" case: no rejection anywhere -----------------------
    def test_no_rejection_anywhere_passes_without_evidence(self):
        runner = FakeRunner(reviews=[], body="Looks fine, thanks!", comments=["LGTM"])
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("allow", result["decision"])
        self.assertIn("nothing to gate", result["reason"])

    def test_approved_review_alone_passes_without_evidence(self):
        runner = FakeRunner(reviews=[{"state": "APPROVED"}], body="", comments=[])
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("allow", result["decision"])

    # --- "required and missing" case ---------------------------------------
    def test_native_request_changes_with_no_marker_refuses(self):
        runner = FakeRunner(reviews=[{"state": "CHANGES_REQUESTED"}], body="fixed it", comments=[])
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])
        self.assertIn(EVIDENCE_MARKER, result["reason"])

    def test_rejected_verdict_comment_with_no_marker_refuses(self):
        runner = FakeRunner(reviews=[], body="", comments=["Verdict: REQUEST CHANGES\n\nthe bug is still there"])
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])

    def test_marker_present_but_empty_stub_refuses(self):
        runner = FakeRunner(
            reviews=[{"state": "CHANGES_REQUESTED"}],
            body=f"Fixed!\n\n{EVIDENCE_MARKER}\n",
            comments=[],
        )
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])
        self.assertIn("stub", result["reason"])

    def test_marker_with_command_but_no_exit_code_refuses(self):
        runner = FakeRunner(
            reviews=[{"state": "CHANGES_REQUESTED"}],
            body=f"{EVIDENCE_MARKER}\n$ some-command\nsome output\n",
            comments=[],
        )
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])

    def test_marker_with_literal_dollar_question_refuses(self):
        # The brief's own rule: "never $? after a pipe" -- an unresolved,
        # literal `$?` in the pasted text must not satisfy the exit-code
        # requirement, only an actually-resolved digit does.
        runner = FakeRunner(
            reviews=[{"state": "CHANGES_REQUESTED"}],
            body=f"{EVIDENCE_MARKER}\n$ some-command\nsome output\nexit code: $?\n",
            comments=[],
        )
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])

    # --- "required and satisfied" case --------------------------------------
    def test_populated_block_in_body_passes(self):
        runner = FakeRunner(reviews=[{"state": "CHANGES_REQUESTED"}], body=GOOD_BLOCK, comments=[])
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("allow", result["decision"])
        self.assertIn("evidence present", result["reason"])

    def test_populated_block_in_a_followup_comment_passes(self):
        # #338's whole point: evidence posted as a follow-up after the fix
        # pass, not necessarily present when the PR was first opened.
        runner = FakeRunner(
            reviews=[],
            body="",
            comments=["Verdict: REQUEST CHANGES\nstill broken", GOOD_BLOCK],
        )
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("allow", result["decision"])

    def test_gh_read_failure_refuses_rather_than_allows(self):
        def broken_runner(command, *, timeout=None):
            raise RuntimeError("network is down")

        result = FixpassEvidenceGate(broken_runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])
        self.assertIn("network is down", result["reason"])


if __name__ == "__main__":
    unittest.main()
