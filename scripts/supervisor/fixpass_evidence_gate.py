"""A fix pass must paste proof, not a claim (agent-supervisor#338).

Measured over a 7-hour overnight window: 25 lane-turns produced 5 merges --
5 turns per merge, and re-review was most of that cost. The standard path is
review -> finds defects -> fix pass -> re-review -> merge; the re-review is a
FULL FRESH REVIEW today because the fix pass leaves behind a claim ("fixed
it") rather than evidence. `as308-rereview-pr331` spent 21.1 minutes
re-deriving what a pasted before/after would have made a 30-second check.

THE MECHANISM, not the prose. #338's own brief is explicit: "prefer the
mechanism that cannot be forgotten -- a rule stated only in prose is the
shape this estate keeps re-learning." This module is that mechanism, wired
the same way `ui-evidence-gate.sh` (#110) already is: a required CI check
(`.github/workflows/fixpass-evidence.yml`) that `ci_gate.py` already refuses
to merge past when red (agent-supervisor#13) -- no new logic needed in
`merge-pr.sh` itself, because the enforcement point already exists and
already fails closed on any red check.

WHAT TRIGGERS THE REQUIREMENT -- "required" vs "none required", never
guessed. A PR requires fixpass evidence only when it carries POSITIVE
evidence a reviewer found something to fix: a native GitHub review with
state `CHANGES_REQUESTED`, or a comment with a `Verdict:` line that
`verdict.py`'s own (already-tested) line-scanner classifies as `rejected`.
Reusing `verdict.py`'s `_scan_verdict_lines`/`_classify_decision_text`
instead of re-deriving a second regex is deliberate: this estate's own
history (#53, #192, #198, #232) is a long list of that classifier getting
subtler than it looks, and a second, slightly different copy would drift.
A PR with neither signal has nothing to gate -- "no rejection found" is a
looked-up, explicit `False`, not an absence read as an all-clear (the same
positive-evidence-vs-lookup-miss distinction #308 turned on).

WHAT COUNTS AS EVIDENCE, once required -- structure, not content. This
module cannot judge whether a pasted reproduction actually demonstrates the
defect is gone; that judgement is the re-reviewer's job, deliberately left
to them (#338: "does this pasted before/after actually demonstrate the
finding is closed... rather than let me work out what was wrong"). What
THIS module can and does check is that something real was pasted, not an
empty or stubbed marker:

  <!-- fixpass-evidence:v1 -->
  $ <the reviewer's exact reproduction command>
  <its output>
  exit code: <N>

A qualifying block needs a `$ ...` command line, an `exit code: <N>` line
carrying an actual resolved digit, and at least one more non-blank line of
content -- so a bare marker, or a marker with only a command and no exit
line (or vice versa), refuses. The digit requirement doubles as the `$?`
guard the brief asks for: pasting the literal, unexpanded `$?` (never
`$? after a pipe` -- the brief's own words) fails the digit match, because
an actually-run command's output never contains the string "$?" -- only a
placeholder does.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from verdict import _scan_verdict_lines  # noqa: E402 -- reuse the tested Verdict: line classifier


EVIDENCE_MARKER = "<!-- fixpass-evidence:v1 -->"

# Widened per PR #340's reviewer (#338 fix pass): a literal `$ ` prompt and a
# literal `exit code:` label are how the brief's example is written, not how
# reviewers actually paste already-run evidence. A terminal that doesn't echo
# its prompt yields a bare command line with no `$`; a status is as often
# reported `rc=0`, `status: 0`, or `exit status: 0` as `exit code: 0`. The
# gate is generous about phrasing on purpose -- what it measures is whether
# real evidence was pasted, not whether it matches one invented format.
_EXIT_CODE_RE = re.compile(r"(?im)^\s*(?:exit\s*code|exit\s*status|status|rc)\s*[:=]\s*(-?[0-9]+)\s*$")
_COMMAND_RE = re.compile(r"(?m)^\s*\$\s+\S.*$")


def subprocess_runner(command):
    return subprocess.run(command, check=True, capture_output=True, text=True, timeout=30).stdout


def _any_rejected_verdict(bodies):
    """True when at least one body has a `Verdict:` line that classifies as
    `rejected` -- the comment-posted path (#53), same classifier `verdict.py`
    uses for the estate's real merge decisions."""
    for body in bodies:
        for decision, _ in _scan_verdict_lines(body or ""):
            if decision == "rejected":
                return True
    return False


def _has_changes_requested_review(reviews):
    """True when any native GitHub review (`gh pr review --request-changes`)
    is on record -- independent of whether it is still the CURRENT verdict;
    a rejection that was later superseded by an approved fix pass is still
    the reason a fix pass evidence trail is expected to exist."""
    return any(
        isinstance(review, dict) and (review.get("state") or "").upper() == "CHANGES_REQUESTED" for review in reviews
    )


def _is_rejected_verdict_text(text):
    """Single-document version of `_any_rejected_verdict`, used to tag one
    document (a body, a review, a comment) as a rejection round on its own,
    so it can be matched against its own timestamp."""
    for decision, _ in _scan_verdict_lines(text or ""):
        if decision == "rejected":
            return True
    return False


def _gather_documents(*, body, body_timestamp, reviews, comments):
    """Every place evidence -- or a rejection -- could be posted, each tagged
    with the timestamp of that specific posting. This is what lets evidence
    be bound to the rejection round it answers (agent-supervisor#340 finding
    2): `evaluate()` no longer asks "is there a rejection anywhere" and "is
    there evidence anywhere" as two independent questions over pooled text,
    it asks whether evidence exists AFTER the most recent rejection."""
    documents = [{"text": body, "timestamp": body_timestamp, "is_rejection": _is_rejected_verdict_text(body)}]
    for review in reviews:
        if not isinstance(review, dict):
            continue
        text = review.get("body") or ""
        is_rejection = (review.get("state") or "").upper() == "CHANGES_REQUESTED"
        documents.append({"text": text, "timestamp": review.get("submittedAt"), "is_rejection": is_rejection})
    for comment in comments:
        if not isinstance(comment, dict):
            continue
        text = comment.get("body") or ""
        documents.append(
            {"text": text, "timestamp": comment.get("createdAt"), "is_rejection": _is_rejected_verdict_text(text)}
        )
    return documents


def _evidence_blocks(text):
    """Every span of `text` from one `EVIDENCE_MARKER` occurrence up to the
    next occurrence (or end of text) -- so several findings, each with its
    own marker, are checked independently rather than pooling the whole PR's
    text into one match."""
    blocks = []
    idx = 0
    while True:
        start = text.find(EVIDENCE_MARKER, idx)
        if start == -1:
            break
        next_start = text.find(EVIDENCE_MARKER, start + len(EVIDENCE_MARKER))
        end = next_start if next_start != -1 else len(text)
        blocks.append(text[start:end])
        idx = end
    return blocks


def _has_command_line(block):
    """A `$ <cmd>` prompt line, OR -- widened per #340's reviewer -- any
    other non-blank content line that isn't the marker or the exit-code
    line. A terminal that doesn't echo its prompt pastes the bare command
    with no `$`; the gate cannot tell a bare command line from a bare output
    line, and per the brief is not supposed to try -- it is generous about
    shape and leaves judging the content to the re-reviewer."""
    if _COMMAND_RE.search(block):
        return True
    for line in block.splitlines():
        stripped = line.strip()
        if not stripped or stripped == EVIDENCE_MARKER:
            continue
        if _EXIT_CODE_RE.match(stripped):
            continue
        return True
    return False


def _block_is_populated(block):
    """A command line, a resolved-digit exit code line, and at least one
    more non-blank line beyond those two -- the minimum shape that cannot be
    satisfied by an empty or stubbed paste (#338's own constraint)."""
    if not _has_command_line(block):
        return False
    if not _EXIT_CODE_RE.search(block):
        return False
    body_after_marker = block[len(EVIDENCE_MARKER) :]
    meaningful_lines = [line for line in body_after_marker.splitlines() if line.strip()]
    return len(meaningful_lines) >= 3


class FixpassEvidenceGate:
    def __init__(self, runner=None):
        self.runner = runner or subprocess_runner

    def evaluate(self, *, repo, number):
        try:
            raw = self.runner(
                ["gh", "pr", "view", str(number), "--repo", repo, "--json", "reviews,body,comments,createdAt"]
            )
            payload = json.loads(raw)
            reviews = payload.get("reviews", [])
            body = payload.get("body") or ""
            comments = payload.get("comments", [])
            if not isinstance(reviews, list):
                raise ValueError("reviews is not a list")
            if not isinstance(comments, list):
                raise ValueError("comments is not a list")
            comment_bodies = [c.get("body") or "" for c in comments if isinstance(c, dict)]
        except Exception as error:
            return {"decision": "refuse", "reason": f"could not read PR #{number}: {error}"}

        required = _has_changes_requested_review(reviews) or _any_rejected_verdict([body] + comment_bodies)
        if not required:
            return {
                "decision": "allow",
                "reason": f"no REQUEST CHANGES review or rejected Verdict: line found on #{number} -- nothing to gate",
            }

        documents = _gather_documents(
            body=body, body_timestamp=payload.get("createdAt"), reviews=reviews, comments=comments
        )

        # The round this evidence must answer: the LATEST qualifying
        # rejection's timestamp. A rejection with no recorded timestamp
        # cannot anchor a cutoff -- it is dropped rather than treated as
        # "now", which would stale-out every prior evidence block.
        rejection_timestamps = [d["timestamp"] for d in documents if d["is_rejection"] and d["timestamp"]]
        cutoff = max(rejection_timestamps) if rejection_timestamps else None

        qualifying = []
        stale = []
        any_blocks = False
        for doc in documents:
            for block in _evidence_blocks(doc["text"]):
                any_blocks = True
                if not _block_is_populated(block):
                    continue
                timestamp = doc["timestamp"]
                # A populated block counts for the CURRENT round unless we
                # can prove it predates the latest rejection -- both
                # timestamps present and the block's is strictly earlier.
                # Missing timestamps default to counting (agent-supervisor
                # #340 finding 2 asks for round-binding "at minimum" by
                # timestamp; it must not regress evidence that predates this
                # gate learning to read `createdAt`/`submittedAt`).
                if cutoff is not None and timestamp is not None and timestamp < cutoff:
                    stale.append(block)
                else:
                    qualifying.append(block)

        if qualifying:
            return {
                "decision": "allow",
                "reason": f"{EVIDENCE_MARKER} present with {len(qualifying)} populated block(s) -- evidence present",
            }
        if stale:
            return {
                "decision": "refuse",
                "reason": (
                    f"PR #{number} carries {len(stale)} populated {EVIDENCE_MARKER} block(s), but all of them "
                    f"predate the most recent rejection (at {cutoff}) -- that round's rejection has no fresh "
                    "evidence posted after it yet"
                ),
            }
        if any_blocks:
            return {
                "decision": "refuse",
                "reason": (
                    f"PR #{number} carries {EVIDENCE_MARKER} but no block has a command line ('$ ...'), "
                    "a resolved 'exit code: N' line, and output -- a stub marker is not evidence"
                ),
            }
        return {
            "decision": "refuse",
            "reason": (
                f"PR #{number} had a REQUEST CHANGES review or a rejected Verdict: line, but carries no "
                f"{EVIDENCE_MARKER} marker. Re-run the reviewer's own reproduction command after the fix "
                "and paste, prefixed with the marker:\n\n"
                f"  {EVIDENCE_MARKER}\n"
                "  $ <reviewer's exact reproduction command>\n"
                "  <its output>\n"
                "  exit code: <captured directly -- never $? after a pipe>"
            ),
        }


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", required=True, help="owner/name")
    parser.add_argument("--number", type=int, required=True)
    args = parser.parse_args(argv)

    gate = FixpassEvidenceGate()
    result = gate.evaluate(repo=args.repo, number=args.number)
    print(result["reason"], file=sys.stdout if result["decision"] == "allow" else sys.stderr)
    return 0 if result["decision"] == "allow" else 1


if __name__ == "__main__":
    sys.exit(main())
