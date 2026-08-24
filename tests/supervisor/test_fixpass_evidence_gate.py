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
        # A real posted verdict (#53's comment-posted path) carries both
        # trailers `post-verdict.sh` requires before it will post one --
        # agent-supervisor#484.
        runner = FakeRunner(
            reviews=[],
            body="",
            comments=["Verdict: REQUEST CHANGES\nReview-Lane: agent-supervisor:3\n\nthe bug is still there"],
        )
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
            comments=["Verdict: REQUEST CHANGES\nReview-Lane: agent-supervisor:3\nstill broken", GOOD_BLOCK],
        )
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("allow", result["decision"])
        self.assertIn("evidence present", result["reason"])

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
                {
                    "body": "Verdict: REQUEST CHANGES\nReview-Lane: agent-supervisor:3\nround 1: old_bug is broken",
                    "createdAt": "2026-08-01T00:00:00Z",
                },
                {"body": GOOD_BLOCK, "createdAt": "2026-08-01T01:00:00Z"},
                {
                    "body": (
                        "Verdict: REQUEST CHANGES\nReview-Lane: agent-supervisor:3\n"
                        "round 2: unrelated new_bug is broken, nothing pasted yet"
                    ),
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
                {
                    "body": "Verdict: REQUEST CHANGES\nReview-Lane: agent-supervisor:3\nround 1: old_bug is broken",
                    "createdAt": "2026-08-01T00:00:00Z",
                },
                {"body": GOOD_BLOCK, "createdAt": "2026-08-01T01:00:00Z"},
                {
                    "body": "Verdict: REQUEST CHANGES\nReview-Lane: agent-supervisor:3\nround 2: new_bug is broken",
                    "createdAt": "2026-08-10T00:00:00Z",
                },
                {"body": GOOD_BLOCK, "createdAt": "2026-08-10T01:00:00Z"},
            ],
        )
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("allow", result["decision"])

    # --- self-referential false positive (agent-supervisor#484) ------------
    def test_body_quoting_example_verdict_text_does_not_trip_the_gate(self):
        # PR #481's real (pre-reword) body text: documenting a bug in the
        # verdict parser by quoting the exact phrase it misclassified. The
        # PR itself carried no review and no comment from anyone -- the
        # body merely discussed the phrase, and used to read as a genuine
        # rejection of the PR carrying it.
        body = (
            "The original wording literally contained\n"
            "`Verdict: REQUEST CHANGES` as an example, which read as a real "
            "rejection\non this PR itself."
        )
        runner = FakeRunner(reviews=[], body=body, comments=[])
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=481)
        self.assertEqual("allow", result["decision"])
        self.assertIn("nothing to gate", result["reason"])

    def test_comment_quoting_verdict_text_as_prose_does_not_trip_the_gate(self):
        # The other half of #484: a REVIEWER'S OWN comment explaining this
        # exact false positive by quoting the offending phrase, with no
        # Review-Lane: trailer -- prose about verdict text, not a posted
        # verdict.
        comment = (
            "Heads up -- this PR's body used to contain the literal phrase "
            "`Verdict: REQUEST CHANGES` as an example, which the gate's "
            "classifier misread as a real rejection of this PR."
        )
        runner = FakeRunner(reviews=[], body="", comments=[comment])
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=481)
        self.assertEqual("allow", result["decision"])
        self.assertIn("nothing to gate", result["reason"])

    def test_body_with_verdict_text_still_ignored_even_when_review_requires_evidence(self):
        # The body must never count as a rejection source even when a real
        # review DOES require evidence -- otherwise the body's prose could
        # still be misread as a second, independent rejection round.
        body = "Discussing `Verdict: REQUEST CHANGES` as example text here."
        runner = FakeRunner(reviews=[{"state": "CHANGES_REQUESTED"}], body=body, comments=[])
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=481)
        self.assertEqual("refuse", result["decision"])
        self.assertIn(EVIDENCE_MARKER, result["reason"])
        self.assertNotIn("predate", result["reason"])

    def test_missing_timestamps_still_allow_evidence_as_before(self):
        # Backward compatibility: when timestamps aren't available (older
        # `gh` payloads, or a rejection/evidence pair with no recorded
        # time), the gate must not regress to refusing evidence it used to
        # accept -- only PROVEN-stale evidence (an earlier timestamp than
        # the latest rejection) is disqualified.
        runner = FakeRunner(reviews=[{"state": "CHANGES_REQUESTED"}], body=GOOD_BLOCK, comments=[])
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("allow", result["decision"])

    # --- agent-supervisor#571: second shape of #484/#540 ---------------------
    def test_547_diagnosis_comment_alone_is_not_a_rejection(self):
        # The real #547 comment (`2026-08-24T02:20:57Z`, verbatim) diagnoses
        # the failing `gate` check by quoting its own assertion sentence --
        # "a REQUEST CHANGES review or a rejected Verdict: line" -- inside a
        # fenced code block, plus two more `Verdict:`/`Review-Lane:` mentions
        # each single-backtick-quoted elsewhere in the comment. None of these
        # is a real posted verdict: no trailer block, no `Review-Lane:` line
        # at all. This is THIS module's own `_is_rejected_verdict_comment`,
        # not just `verdict.py`'s (both reuse `_scan_verdict_lines`, per this
        # module's own docstring) -- with nothing else on the PR, the gate
        # must find nothing to require evidence for.
        diagnosis_comment = (
            "## Diagnosed the failing `gate` check — it's Fixpass evidence, not UI evidence (`#565`)\n"
            "\n"
            "`gh pr checks 547` shows two checks both named `gate` — exactly the `#565`\n"
            "ambiguity. Resolved which is which by checking each run's own workflow\n"
            "name, not the display label:\n"
            "\n"
            "```\n"
            'run 32681427761  conclusion=failure  name="Fixpass evidence"   headSha=becbbaa4\n'
            'run 32681427793  conclusion=success  name="UI evidence"        headSha=becbbaa4\n'
            "```\n"
            "\n"
            "**The failing one is `Fixpass evidence`.** Its job log (`Check fixpass\n"
            "evidence` step) prints the actual assertion:\n"
            "\n"
            "```\n"
            "PR #547 had a REQUEST CHANGES review or a rejected Verdict: line, but\n"
            "carries no <!-- fixpass-evidence:v1 --> marker.\n"
            "```\n"
            "\n"
            "The `01:57:52Z` evidence block is real, but it's dated *before* the\n"
            "`02:01:28Z` `Verdict: REQUEST CHANGES` / `Review-Lane: estate:4` comment\n"
            "— which is itself a re-post. The gate has no way to know that\n"
            "re-post's substance was already addressed before it was even reposted —\n"
            "it only sees a `Verdict:`/`Review-Lane:` pair with a later timestamp than\n"
            "any existing evidence block, and refuses on that basis.\n"
        )
        runner = FakeRunner(reviews=[], body="", comments=[diagnosis_comment])
        result = FixpassEvidenceGate(runner).evaluate(repo="o/r", number=547)
        self.assertEqual("allow", result["decision"])
        self.assertIn("nothing to gate", result["reason"])


if __name__ == "__main__":
    unittest.main()
