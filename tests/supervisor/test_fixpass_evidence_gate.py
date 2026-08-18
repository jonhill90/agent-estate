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
    """Answers the one `gh pr view` call the gate makes. Comments may be
    plain strings (no timestamp) or `{"body": ..., "createdAt": ...}` dicts
    when a test needs to bind evidence to a specific round."""

    def __init__(self, *, reviews=None, body="", body_timestamp=None, comments=None):
        self.reviews = reviews or []
        self.body = body
        self.body_timestamp = body_timestamp
        self.comments = comments or []
        self.calls = []

    def __call__(self, command, *, timeout=None):
        self.calls.append(command)
        if command[:3] == ["gh", "pr", "view"]:
            comments_payload = [c if isinstance(c, dict) else {"body": c} for c in self.comments]
            return json.dumps(
                {
                    "reviews": self.reviews,
                    "body": self.body,
                    "createdAt": self.body_timestamp,
                    "comments": comments_payload,
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

    # --- widened evidence shape (agent-supervisor#340 finding 1) -----------
    # PR #340's reviewer reproduced this: the original `_COMMAND_RE`/
    # `_EXIT_CODE_RE` required a literal `$ ` prompt and a literal
    # `exit code:` label, refusing real, already-run evidence pasted in the
    # (very common) shapes below -- the #339 failure mode reproduced inside
    # #338's own fix.
    def test_real_command_and_output_without_dollar_prefix_is_allowed(self):
        runner = FakeRunner(
            reviews=[{"state": "CHANGES_REQUESTED"}],
            body=(
                f"{EVIDENCE_MARKER}\n"
                "python3 -m unittest tests.supervisor.test_fixpass_evidence_gate -v\n"
                "Ran 10 tests in 0.003s\n"
                "OK\n"
                "exit code: 0\n"
            ),
            comments=[],
        )
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("allow", result["decision"])

    def test_real_exit_status_reported_as_rc_equals_is_allowed(self):
        runner = FakeRunner(
            reviews=[{"state": "CHANGES_REQUESTED"}],
            body=(f"{EVIDENCE_MARKER}\n$ some-command\nsome real output\nrc=0\n"),
            comments=[],
        )
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("allow", result["decision"])

    def test_exit_status_colon_phrasing_is_allowed(self):
        runner = FakeRunner(
            reviews=[{"state": "CHANGES_REQUESTED"}],
            body=(f"{EVIDENCE_MARKER}\n$ some-command\nsome real output\nexit status: 0\n"),
            comments=[],
        )
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("allow", result["decision"])

    def test_status_colon_phrasing_is_allowed(self):
        runner = FakeRunner(
            reviews=[{"state": "CHANGES_REQUESTED"}],
            body=(f"{EVIDENCE_MARKER}\n$ some-command\nsome real output\nstatus: 0\n"),
            comments=[],
        )
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("allow", result["decision"])

    def test_widened_shapes_still_refuse_a_bare_stub(self):
        # Widening the regexes must not turn "nothing pasted" into a pass:
        # a marker with no command-ish line and no resolved status at all
        # still refuses.
        runner = FakeRunner(
            reviews=[{"state": "CHANGES_REQUESTED"}],
            body=f"Fixed it, trust me.\n\n{EVIDENCE_MARKER}\n",
            comments=[],
        )
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])

    def test_no_evidence_pasted_at_all_still_refuses(self):
        # The gate's whole purpose, unaffected by the shape-widening: a fix
        # pass that pastes nothing at all is still refused.
        runner = FakeRunner(reviews=[{"state": "CHANGES_REQUESTED"}], body="I fixed it.", comments=["done!"])
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])

    # --- round-binding (agent-supervisor#340 finding 2) ---------------------
    def test_evidence_from_a_resolved_earlier_round_should_not_cover_a_new_unrelated_rejection(self):
        runner = FakeRunner(
            reviews=[],
            comments=[
                {"body": "Verdict: REQUEST CHANGES\nround 1: old_bug is broken", "createdAt": "2026-08-01T00:00:00Z"},
                {"body": GOOD_BLOCK, "createdAt": "2026-08-01T01:00:00Z"},
                {
                    "body": "Verdict: REQUEST CHANGES\nround 2: unrelated new_bug is broken, nothing pasted yet",
                    "createdAt": "2026-08-10T00:00:00Z",
                },
            ],
        )
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])

    def test_fresh_evidence_posted_after_the_latest_rejection_is_allowed(self):
        runner = FakeRunner(
            reviews=[],
            comments=[
                {"body": "Verdict: REQUEST CHANGES\nround 1: old_bug is broken", "createdAt": "2026-08-01T00:00:00Z"},
                {"body": GOOD_BLOCK, "createdAt": "2026-08-01T01:00:00Z"},
                {"body": "Verdict: REQUEST CHANGES\nround 2: new_bug is broken", "createdAt": "2026-08-10T00:00:00Z"},
                {"body": GOOD_BLOCK, "createdAt": "2026-08-10T01:00:00Z"},
            ],
        )
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("allow", result["decision"])

    def test_missing_timestamps_still_allow_evidence_as_before(self):
        # Backward compatibility: when timestamps aren't available (older
        # `gh` payloads, or a rejection/evidence pair with no recorded
        # time), the gate must not regress to refusing evidence it used to
        # accept -- only PROVEN-stale evidence (an earlier timestamp than
        # the latest rejection) is disqualified.
        runner = FakeRunner(reviews=[{"state": "CHANGES_REQUESTED"}], body=GOOD_BLOCK, comments=[])
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("allow", result["decision"])


if __name__ == "__main__":
    unittest.main()
