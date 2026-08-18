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

_EXIT_CODE_RE = re.compile(r"(?im)^\s*exit code:\s*(-?[0-9]+)\s*$")
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


def _block_is_populated(block):
    """A command line, a resolved-digit exit code line, and at least one
    more non-blank line beyond those two -- the minimum shape that cannot be
    satisfied by an empty or stubbed paste (#338's own constraint)."""
    if not _COMMAND_RE.search(block):
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
            raw = self.runner(["gh", "pr", "view", str(number), "--repo", repo, "--json", "reviews,body,comments"])
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

        all_text = "\n".join([body] + comment_bodies)
        blocks = _evidence_blocks(all_text)
        populated = [b for b in blocks if _block_is_populated(b)]
        if populated:
            return {
                "decision": "allow",
                "reason": f"{EVIDENCE_MARKER} present with {len(populated)} populated block(s) -- evidence present",
            }
        if blocks:
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
