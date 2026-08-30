import re
import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import (  # noqa: E402
    GithubReviewVerdictSource,
    _classify_decision_text,
    _normalise_decision_text,
    _scan_verdict_lines,
)

from tests.supervisor.test_verdict_helpers import (  # noqa: E402
    REPO,
    _comment_runner,
)


class Issue571DiagnosisCommentShadowingTests(unittest.TestCase):
    """agent-supervisor#571: a SECOND shape of #540's shadowing bug, live on
    `agent-supervisor#547` -- a comment DIAGNOSING the failing `gate` check
    quotes the gate's own failure sentence, which mentions "REQUEST CHANGES"
    and "Verdict:" in the same breath, but carries no trailer block at all
    (no `Review-Lane:`, no `Reviewed-SHA:`). This is the exact comment
    (`gh pr view 547 --json comments`, posted `2026-08-24T02:20:57Z` by
    `@jonhill90`), verbatim -- not a paraphrase.

    #540/#544 already close THIS specific text: both real `Verdict:`
    mentions inside it sit either inside a fenced code block (the literal
    quote of the gate's own assertion) or inside a single-backtick span
    (`` `Verdict: REQUEST CHANGES` ``, `` `Verdict:` ``) -- exactly the two
    shapes `_scan_verdict_lines`/`_label_inside_inline_code` already
    exclude. `test_mutation_reverting_540s_guard_turns_this_second_shape_red`
    below proves that anchoring, not a coincidence of this text, is what
    keeps it from shadowing: reverting to the pre-#540 regex (no
    inline-code exclusion) DOES misclassify line 44 of this comment as a
    decisive `REQUEST CHANGES` -- the second shape #571 names. This class
    locks the current, correct behaviour in as a regression test built from
    the real fixture, per #571's own brief ("use the exact #547 comment...
    verify both arms")."""

    DIAGNOSIS_COMMENT = (
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
        "carries no <!-- fixpass-evidence:v1 --> marker. Re-run the reviewer's own\n"
        "reproduction command after the fix and paste, prefixed with the marker:\n"
        "\n"
        "  <!-- fixpass-evidence:v1 -->\n"
        "  $ <reviewer's exact reproduction command>\n"
        "  <its output>\n"
        "  exit code: <captured directly -- never $? after a pipe>\n"
        "```\n"
        "\n"
        "That's the same cause that blocked `#553` earlier — a REQUEST CHANGES\n"
        "verdict on record with no qualifying evidence block. **One nuance worth\n"
        "naming so the fix lands right the first time:** that CI run executed at\n"
        "`01:57:14Z`, 14 seconds after `becbbaa4` was pushed — 38 seconds *before*\n"
        "a `<!-- fixpass-evidence:v1 -->` block was posted at `01:57:52Z`. So the\n"
        'job log\'s specific wording ("carries no marker") is a snapshot of that\n'
        "instant, not the current state. Re-running the gate live just now gives a\n"
        "more precise, current reason:\n"
        "\n"
        "```\n"
        "$ python3 scripts/supervisor/fixpass_evidence_gate.py --repo jonhill90/agent-supervisor --number 547\n"
        "PR #547 carries 1 populated <!-- fixpass-evidence:v1 --> block(s), but\n"
        "all of them predate the most recent rejection (at 2026-08-24T02:01:28Z)\n"
        "-- that round's rejection has no fresh evidence posted after it yet\n"
        "exit: 1\n"
        "```\n"
        "\n"
        "The `01:57:52Z` evidence block is real, but it's dated *before* the\n"
        "`02:01:28Z` `Verdict: REQUEST CHANGES` / `Review-Lane: estate:4` comment\n"
        "— which is itself a re-post (identity-correction only, `register-lane-\n"
        "self.sh`) of an *earlier* finding whose substance `becbbaa4` (pushed\n"
        "`01:57:00Z`) already fixes: \"this round's own count is eight blocked\n"
        'PRs... not the seven `0008` lists." The gate has no way to know that\n'
        "re-post's substance was already addressed before it was even reposted —\n"
        "it only sees a `Verdict:`/`Review-Lane:` pair with a later timestamp than\n"
        "any existing evidence block, and refuses on that basis. Correct behavior\n"
        "for what it can see, even though the underlying finding is stale.\n"
        "\n"
        "**What this means for closing it, per this repo's own rule (`#553`'s\n"
        "same cause):** the fix is **not** a bare marker to satisfy the parser,\n"
        "and **not** a no-op commit to force a re-run. Post a real\n"
        "`<!-- fixpass-evidence:v1 -->` block — a command, its actual output, and\n"
        "a resolved exit code — demonstrating the residual-count fix `becbbaa4`\n"
        "already made, timestamped after `02:01:28Z` so the gate has something to\n"
        "resolve the standing rejection against. (For reference, something in the\n"
        'shape of `grep -c "eight blocked PRs" docs/decisions/0008-...` against\n'
        "the current head would demonstrate it directly, but the exact command is\n"
        "the author's call, not mine to prescribe.)\n"
        "\n"
        "Not touched: `#553`'s own remaining failure (`shell-suites (shard 4)`) is\n"
        "out of scope here — flagged to me as already attributed elsewhere\n"
        "(`#567`, `test_inbox_poll.sh`'s SIGTERM escalation, distinct from `#548`)\n"
        "with its own owner; not re-derived or acted on in this comment.\n"
    )

    # --- unit level: the instrument itself -------------------------------

    def test_diagnosis_comment_yields_no_scan_line_at_all(self):
        """The real #547 diagnosis comment must never be a scan candidate --
        not merely unrecognised. Both real `Verdict:` mentions inside it are
        excluded: one by the fenced-code-block check (the literal quote of
        the gate's own assertion), the other two by `_label_inside_inline_code`
        (`` `Verdict: REQUEST CHANGES` `` and `` `Verdict:` ``, both
        single-backtick-quoted)."""
        self.assertEqual(_scan_verdict_lines(self.DIAGNOSIS_COMMENT), [])

    def test_mutation_reverting_540s_guard_turns_this_second_shape_red(self):
        """Proves the exclusion, not a coincidence of this text, is what
        keeps the diagnosis comment from shadowing: the pre-#540 regex (no
        label-capturing group, so `_label_inside_inline_code` cannot run)
        DOES match the `` `Verdict: REQUEST CHANGES` `` line inside this
        comment and classifies it as a decisive `REQUEST CHANGES` --
        exactly the false rejection #571 describes. Fence exclusion alone
        (independent of #540) is not enough for this second shape, because
        this particular line sits OUTSIDE any fenced block."""
        pre_540_re = re.compile(r"^#{0,6}\s*.*?\*{0,2}verdict:\**\s*(.*)$", re.IGNORECASE)
        offending_line = "`02:01:28Z` `Verdict: REQUEST CHANGES` / `Review-Lane: estate:4` comment"
        self.assertIn(offending_line, self.DIAGNOSIS_COMMENT)
        match = pre_540_re.match(offending_line)
        self.assertIsNotNone(match, "the pre-fix regex must still match this second shape (that IS the gap)")
        self.assertEqual(_classify_decision_text(_normalise_decision_text(match.group(1))), "rejected")

    # --- end-to-end: the actual refusal path ------------------------------

    def test_diagnosis_comment_does_not_shadow_the_real_approval_on_547(self):
        """Reproduces #547's live shape end to end: a properly trailered
        `REQUEST CHANGES`, superseded by a properly trailered `APPROVE` at
        the current head, followed by the diagnosis comment as the NEWEST
        comment on the PR. The diagnosis comment carries no trailer at all
        and must never become the operative verdict -- the genuine, fresher
        `APPROVE` must still be what resolves, exactly as it does on the
        real PR today."""
        head_sha = "b" * 40
        comments = [
            {
                "author": {"login": "jonhill90"},
                "body": "Verdict: REQUEST CHANGES\nReview-Lane: estate:4\nReviewed-SHA: " + "a" * 40,
                "createdAt": "2026-08-24T02:01:28Z",
            },
            {
                "author": {"login": "jonhill90"},
                "body": "Verdict: APPROVE\nReview-Lane: estate:5\nReviewed-SHA: " + head_sha,
                "createdAt": "2026-08-24T02:05:44Z",
            },
            {
                "author": {"login": "jonhill90"},
                "body": self.DIAGNOSIS_COMMENT,
                "createdAt": "2026-08-24T02:20:57Z",
            },
        ]
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=comments))
        result = source.verdict(repo=REPO, number=547, head_sha=head_sha)
        self.assertEqual(result["verdict"], "approved")
        self.assertNotIn("not recognised", result.get("detail", ""))

    def test_a_properly_trailered_rejection_still_blocks_with_the_diagnosis_comment_newest(self):
        """The other arm #571 asks for explicitly: don't regress #540's own
        fix. A properly trailered `REQUEST CHANGES` at the current head must
        still be selected and still block, even with the untrailered
        diagnosis comment posted after it as the newest comment on the PR."""
        head_sha = "c" * 40
        comments = [
            {
                "author": {"login": "jonhill90"},
                "body": "Verdict: REQUEST CHANGES\nReview-Lane: estate:4\nReviewed-SHA: " + head_sha,
                "createdAt": "2026-08-24T02:01:28Z",
            },
            {
                "author": {"login": "jonhill90"},
                "body": self.DIAGNOSIS_COMMENT,
                "createdAt": "2026-08-24T02:20:57Z",
            },
        ]
        source = GithubReviewVerdictSource(runner=_comment_runner(comments=comments))
        result = source.verdict(repo=REPO, number=547, head_sha=head_sha)
        self.assertEqual(result["verdict"], "rejected")
