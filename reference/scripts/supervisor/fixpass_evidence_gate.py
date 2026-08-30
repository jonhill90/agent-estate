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
state `CHANGES_REQUESTED`, or a COMMENT carrying a real posted verdict --
a `Verdict:` line that `verdict.py`'s own (already-tested) line-scanner
classifies as `rejected`, paired with a `Review-Lane:` trailer identifying
the reviewing lane. Reusing `verdict.py`'s `_scan_verdict_lines`/
`_classify_decision_text` instead of re-deriving a second regex is
deliberate: this estate's own history (#53, #192, #198, #232) is a long
list of that classifier getting subtler than it looks, and a second,
slightly different copy would drift. A PR with neither signal has nothing
to gate -- "no rejection found" is a looked-up, explicit `False`, not an
absence read as an all-clear (the same positive-evidence-vs-lookup-miss
distinction #308 turned on).

NEITHER SIGNAL IS THE PR'S OWN BODY (agent-supervisor#484). `_gather_documents`
still scans the body for EVIDENCE (a fix pass may paste its proof there),
but the body is never a rejection source, and a comment counts as a
rejection only when it carries the `Review-Lane:`/`Verdict:` trailer pair
`post-verdict.sh` requires before it will post one at all -- see
`_is_rejected_verdict_comment`. Measured twice on PR #481: a PR body (and,
separately, a reviewer's own comment) that merely QUOTED or DISCUSSED
example verdict text -- documenting this exact bug -- was misread by the
same classifier as a genuine rejection of the PR carrying it, demanding a
fixpass-evidence marker nobody had any real rejection to answer.

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

from verdict import (  # noqa: E402 -- reuse the tested classifiers
    _parse_review_lane,
    _parse_reviewed_sha,
    _review_lane_line,
    _scan_verdict_lines,
)


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


def _any_rejected_verdict(comment_bodies):
    """True when at least one COMMENT carries a real posted verdict --
    agent-supervisor#484: a `Verdict:` line that classifies as `rejected`
    is not enough on its own, because prose that merely quotes or discusses
    verdict-shaped text (a bug report about the parser, a PR body
    describing this exact false positive) matches the same classifier a
    real reviewer's comment does. `post-verdict.sh` never posts a
    `Verdict:` line without a paired `Review-Lane:` trailer identifying the
    reviewing lane (#187/#188) -- that pairing is what makes a comment a
    real posted verdict rather than text that happens to contain the
    phrase, so both must be present. This is the comment-posted path
    (#53), still using the same tested `_scan_verdict_lines` classifier
    `verdict.py` uses for the estate's real merge decisions -- just no
    longer trusting a bare, unattributed match."""
    for body in comment_bodies:
        if _is_rejected_verdict_comment(body):
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


def _is_rejected_verdict_comment(text):
    """Single-document version of `_any_rejected_verdict`, used to tag one
    COMMENT as a rejection round on its own, so it can be matched against
    its own timestamp. Never applied to the PR body (#484: the body is
    never a rejection source -- see `_gather_documents`); a comment counts
    only when it classifies as `rejected` AND carries a `Review-Lane:`
    trailer, the shape `post-verdict.sh` requires before it will post a
    `Verdict:` line at all. Requiring the raw trailer LINE (not that it
    resolves to a registered lane) is deliberate: this module has no
    ledger to resolve against, and the pairing itself -- not the lane's
    validity -- is what tells a real posted verdict apart from prose."""
    if _review_lane_line(text) is None:
        return False
    for decision, _ in _scan_verdict_lines(text or ""):
        if decision == "rejected":
            return True
    return False


def _rejection_key(text):
    """`(Review-Lane, Reviewed-SHA)` for a rejection COMMENT, or `None` when
    either half cannot be parsed -- agent-supervisor#566. A rejection with
    no key cannot be matched against a repost of itself and is left to
    count on its own raw timestamp, exactly as before this existed (see
    `_rejection_effective_timestamps`). Reuses `verdict.py`'s own trailer
    parsers rather than a second implementation of either shape; `Verdict:`
    and `Review-Lane:` are already required before a comment counts as a
    rejection at all (`_is_rejected_verdict_comment`), so only
    `Reviewed-SHA:` -- optional, and absent from any verdict posted before
    agent-supervisor#213 added the trailer -- can be missing here."""
    lane = _parse_review_lane(text)
    sha = _parse_reviewed_sha(text)
    if not lane or not sha:
        return None
    return (lane, sha)


_TRAILER_LINE_RE = re.compile(r"(?i)^\s*(?:verdict|review-lane|reviewed-sha)\s*:")


def _rejection_content_signature(text):
    """Normalized rejection content, protocol trailers stripped --
    agent-supervisor#569's reviewer finding on top of #566's own fix: a
    `(Review-Lane, Reviewed-SHA)` match alone is not proof of a REPOST.
    Two rejections from the same lane at the same SHA can be substantively
    DIFFERENT findings -- a second look that turns up a distinct, still-
    unaddressed bug without a new commit landing in between. Only a
    rejection whose CONTENT also matches is the byte-identical repost
    `_rejection_key` was built to catch (#547's re-registration shape);
    two rejections sharing a key with different content must never
    collapse into one round, or evidence answering the first silently
    satisfies the gate for the second too.

    `Verdict:`/`Review-Lane:`/`Reviewed-SHA:` lines are `post-verdict.sh`'s
    own protocol scaffolding, present and identical on every rejection
    regardless of what was actually found -- they're stripped before
    comparing, or every rejection at the same key would trivially "match"
    on the trailers alone even when the finding underneath differs.
    Whitespace is collapsed so a repost that differs only by incidental
    formatting (a trailing space, a re-wrapped line) still counts as the
    same content."""
    lines = []
    for line in (text or "").splitlines():
        stripped = line.strip()
        if not stripped or _TRAILER_LINE_RE.match(stripped):
            continue
        lines.append(" ".join(stripped.split()))
    return "\n".join(lines)


def _rejection_effective_timestamps(documents):
    """The timestamp each rejection document counts for when computing the
    operative cutoff -- agent-supervisor#566: live on `agent-supervisor#547`,
    a lane that lost its registered identity RE-POSTED an unchanged
    REQUEST-CHANGES verdict (same Review-Lane, same Reviewed-SHA) under its
    newly re-registered identity. The repost carries a LATER wall-clock
    timestamp than the original -- it is a re-registration event, not a new
    review -- and the gate's prior "latest rejection by raw timestamp" logic
    let that later timestamp stale-out evidence that had already answered
    the ORIGINAL round.

    When a rejection carries a parseable `(Review-Lane, Reviewed-SHA)`
    pair (`_rejection_key`) AND its content matches another rejection
    sharing that exact pair (`_rejection_content_signature`), its
    EFFECTIVE timestamp is the EARLIEST timestamp seen among every
    rejection sharing both the pair and the content -- so a repost can
    never push the round's cutoff past its own original. A rejection with
    no key (no `Reviewed-SHA:` trailer) cannot be deduped this way and
    keeps its own raw timestamp, the same behaviour this gate had before
    repost-dedup existed. A genuinely NEW rejection -- same lane, but a
    DIFFERENT Reviewed-SHA because a fresh look happened at a later head,
    OR the identical pair with DIFFERENT content because a second,
    distinct finding turned up without a new commit landing in between
    (agent-supervisor#569) -- is never merged into an earlier round by
    this: the (key, content) pair only matches when every part is
    identical."""
    earliest_by_group = {}
    for doc in documents:
        if not doc["is_rejection"] or doc["timestamp"] is None:
            continue
        key = doc.get("rejection_key")
        if key is None:
            continue
        group = (key, doc.get("rejection_signature"))
        if group not in earliest_by_group or doc["timestamp"] < earliest_by_group[group]:
            earliest_by_group[group] = doc["timestamp"]

    effective = []
    for doc in documents:
        if not doc["is_rejection"] or doc["timestamp"] is None:
            continue
        key = doc.get("rejection_key")
        if key is None:
            effective.append(doc["timestamp"])
        else:
            group = (key, doc.get("rejection_signature"))
            effective.append(earliest_by_group[group])
    return effective


def _gather_documents(*, body, body_timestamp, reviews, comments):
    """Every place evidence -- or a rejection -- could be posted, each tagged
    with the timestamp of that specific posting. This is what lets evidence
    be bound to the rejection round it answers (agent-supervisor#340 finding
    2): `evaluate()` no longer asks "is there a rejection anywhere" and "is
    there evidence anywhere" as two independent questions over pooled text,
    it asks whether evidence exists AFTER the most recent rejection.

    The PR body is still scanned for EVIDENCE (a fix pass may paste its
    proof directly into the body) but is never a rejection source
    (agent-supervisor#484) -- an author writes the body, so a body that
    quotes or discusses verdict-shaped text can never be a genuine
    third-party rejection the way a comment carrying a real
    `Review-Lane:`/`Verdict:` trailer pair can."""
    documents = [{"text": body, "timestamp": body_timestamp, "is_rejection": False}]
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
        is_rejection = _is_rejected_verdict_comment(text)
        documents.append(
            {
                "text": text,
                "timestamp": comment.get("createdAt"),
                "is_rejection": is_rejection,
                # agent-supervisor#566/#569: both only meaningful when this
                # document is itself a rejection -- see `_rejection_key`
                # and `_rejection_content_signature`. A rejection is only
                # deduped against another sharing BOTH.
                "rejection_key": _rejection_key(text) if is_rejection else None,
                "rejection_signature": _rejection_content_signature(text) if is_rejection else None,
            }
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

        required = _has_changes_requested_review(reviews) or _any_rejected_verdict(comment_bodies)
        if not required:
            return {
                "decision": "allow",
                "reason": f"no REQUEST CHANGES review or rejected Verdict: line found on #{number} -- nothing to gate",
            }

        documents = _gather_documents(
            body=body, body_timestamp=payload.get("createdAt"), reviews=reviews, comments=comments
        )

        # The round this evidence must answer: the LATEST qualifying
        # rejection's EFFECTIVE timestamp. A rejection with no recorded
        # timestamp cannot anchor a cutoff -- it is dropped rather than
        # treated as "now", which would stale-out every prior evidence
        # block. agent-supervisor#566: "effective" (not raw) so a REPOST of
        # an earlier rejection -- same Review-Lane, same Reviewed-SHA, a
        # later wall-clock timestamp only because of a re-registration, not
        # a new review -- cannot push the cutoff past the original round it
        # is a repost of; see `_rejection_effective_timestamps`.
        rejection_timestamps = _rejection_effective_timestamps(documents)
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
