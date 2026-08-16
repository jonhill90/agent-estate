"""Verdict-source adapter: is a PR reviewed, and if so, approved or rejected?

`digest.sh` used to answer this by regex-matching the prose of a PR's last
comment (agent-dotfiles#203). That read "I cannot approve this, it is
unsafe." as an approval, and a genuine `--request-changes` review as nothing
at all -- an instrument that inverts its answer is worse than one that has
none. This module replaces it with a real seam: `digest.sh` calls one CLI
command and prints whatever comes back, with no knowledge of where the
answer came from.

Two sources ship today, per Jon's "adapters, not choices" directive:

- `GithubReviewVerdictSource` reads GitHub's own review state
  (`gh pr view --json reviews`). Self-approval is refused by GitHub for a
  single-identity estate, but self `--request-changes` is not -- so this
  source already reports real rejections today and starts reporting real
  approvals the moment a second identity reviews. No code change needed
  here for that; only a distinct reviewer login.
- `LedgerVerdictSource` reads a verdict a reviewing lane recorded directly
  in the supervisor ledger (`Ledger.record_pr_verdict`) -- the estate's own
  record of what it decided, independent of whether GitHub can represent
  it ("tmux is not a database": an authored fact belongs in the ledger).

Adding a third source is a new class plus one `SOURCES` entry. Removing one
is deleting its class and entry -- `digest.sh` and the CLI contract are
unaffected either way.

Every source fails CLOSED: a source that cannot read its backing store
returns "unknown", never "approved" or "none". `main()` wraps the whole
resolution in its own try/except for the same reason -- a source that raises
must still produce a well-formed "unknown" verdict, not a crashed process a
caller might mistake for "no verdict recorded".

A moved head is not always a content change (agent-dotfiles#226). #218 made
every source refuse to answer for a head it never saw -- correct, but it
cannot tell a content-preserving rebase (every SHA on the branch changes by
construction) from a push that actually changed something, and this estate
rebases constantly. `_content_unchanged_since()` narrows that.

WHAT IT COMPARES, exactly -- a figure states its instrument, and the first
attempt at this got the instrument wrong in a way that only showed up
against the real API. For each of the two heads it takes the commits that
head introduces over `merge-base(<the PR's base branch>, head)`, runs each
commit's own diff through `git patch-id --stable`, and compares the two
SETS of patch-ids. Anchoring both sides on the PR's base branch is the
whole point: `merge-base(old, new)` is symmetric, so it resolves to the
PRE-rebase base for both sides, and the "new" side then carries every
commit `main` gained in between as if the branch had authored them. That
made the comparison answer "did `main` stand still?" rather than "did the
reviewed content change?" -- measured False on #226's own commits, the
exact case it was written for. A per-commit patch-id set over each side's
own merge-base is immune: a commit's patch does not change when `main`
moves underneath it.

PROMOTION RULE: the verdict is promoted when every patch-id now on the
branch was among the patch-ids reviewed (current set is a subset of the
reviewed set) -- i.e. nothing unreviewed has entered. Equal sets are the
pure-rebase case. A PROPER subset also promotes, and this is a deliberate
widening, named here because it is the one place this is looser than "any
content change stays unknown": a rebase can drop a commit whose work
`main` superseded, which is precisely what happened on #226's own example
(`0538cc6` -> `69784bd` dropped a `known_references.json` refresh because
upstream #210 replaced that file with `.txt`). The count of dropped
patch-ids is stated in `detail`, so the reader sees it. Adding a commit or
amending one is never promoted -- either puts a patch-id on the branch
that no review covers.

A `True` promotes the verdict and says so in `detail`; anything else
(`False`, or `None` when the comparison itself could not be computed --
network, an unreachable commit, a base branch that could not be read)
leaves the verdict `unknown`, same as before this existed. It never
auto-carries an approval past a change it cannot rule out.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from core import Ledger  # noqa: E402


VERDICT_VALUES = ("none", "approved", "rejected", "unknown")


class VerdictSource:
    """One way of answering "what did we decide about this PR?".

    `verdict()` must never raise -- catch everything internally and return
    `{"verdict": "unknown", "detail": "..."}` instead, so a caller iterating
    several sources never has one blow up the rest. `head_sha`, when given,
    is the PR's CURRENT head (`headRefOid`) -- a source that recorded its
    answer against an older commit must not answer for this one
    (agent-dotfiles#218). `head_sha=None` means the caller has no head to
    check against; a source then answers as it always has, unable to tell
    a current verdict from a stale one.
    """

    def verdict(self, *, repo, number, head_sha=None):
        raise NotImplementedError


def _subprocess_runner(command):
    return subprocess.run(command, check=True, capture_output=True, text=True, timeout=30).stdout


def _default_patch_id(diff_text):
    """Runs `diff_text` through the real `git patch-id --stable` and returns
    its id, or None if no id could be computed (empty diff, git failure) --
    None must read as "comparison inconclusive", never as "matches"."""
    if not diff_text or not diff_text.strip():
        return None
    try:
        result = subprocess.run(
            ["git", "patch-id", "--stable"],
            input=diff_text,
            capture_output=True,
            text=True,
            timeout=30,
            check=True,
        )
    except Exception:
        return None
    line = result.stdout.strip()
    if not line:
        return None
    return line.split()[0]


# A branch longer than this is not compared at all -- GitHub's compare and
# commit-list endpoints paginate, and a silently truncated commit list would
# understate what is on the branch, which is the one error direction that
# could promote an unreviewed patch. Over the cap, fail closed.
_MAX_BRANCH_COMMITS = 100


def _fetch_base_ref(runner, repo, number):
    """The PR's base branch (`main` here). Needed because the comparison must
    anchor BOTH heads on their own merge-base with the base branch;
    `merge-base(old, new)` is symmetric and therefore resolves to the
    pre-rebase base for both, which is what made the first attempt at this
    measure `main`'s movement instead of the branch's content
    (agent-dotfiles#226). Returns None (never raises) so a caller fails
    closed."""
    try:
        payload = json.loads(runner(["gh", "pr", "view", str(number), "--repo", repo, "--json", "baseRefName"]))
    except Exception:
        return None
    base_ref = payload.get("baseRefName") if isinstance(payload, dict) else None
    return base_ref if isinstance(base_ref, str) and base_ref else None


def _fetch_branch_commits(runner, repo, base_ref, head_sha):
    """The SHAs `head_sha` introduces over merge-base(base_ref, head_sha), via
    GitHub's own three-dot compare. Because the base is the PR's base branch
    and not the other head, what `main` gained underneath a rebase is
    excluded rather than counted as the branch's own work. Returns None
    (never raises) when the answer cannot be trusted -- fetch failure, an
    empty range, a range over the cap, or a list that does not actually end
    at `head_sha` (the shape a truncated page takes)."""
    try:
        raw = runner(["gh", "api", f"repos/{repo}/compare/{base_ref}...{head_sha}", "--jq", ".commits[].sha"])
    except Exception:
        return None
    shas = [line.strip() for line in (raw or "").splitlines() if line.strip()]
    if not shas or len(shas) > _MAX_BRANCH_COMMITS:
        return None
    tail = shas[-1]
    if not (tail.startswith(head_sha) or head_sha.startswith(tail)):
        return None
    return shas


def _fetch_patch_ids(runner, patch_id_fn, repo, shas):
    """The set of `git patch-id --stable` ids for `shas`, each commit's own
    diff fetched on its own. None if any one of them could not be reduced to
    an id -- a partial set would understate the branch."""
    ids = set()
    for sha in shas:
        try:
            diff = runner(["gh", "api", "-H", "Accept: application/vnd.github.v3.diff", f"repos/{repo}/commits/{sha}"])
        except Exception:
            return None
        patch_id = patch_id_fn(diff)
        if patch_id is None:
            return None
        ids.add(patch_id)
    return ids or None


def _content_unchanged_since(*, runner, patch_id_fn, repo, number, old_sha, new_sha):
    """Did anything the reviewer did not see get onto this branch?

    Returns `(unchanged, basis)`. `unchanged` is True when every patch-id the
    current head introduces was also introduced by the reviewed head, False
    when at least one was not, and None when the comparison could not be
    computed at all -- which the caller MUST treat as "cannot confirm", never
    as a match (agent-dotfiles#226). `basis` names the instrument and is
    empty unless `unchanged` is True. See this module's docstring for what is
    compared and why the subset rule, not equality, is the promotion test."""
    if old_sha == new_sha:
        return True, "same head"
    base_ref = _fetch_base_ref(runner, repo, number)
    if base_ref is None:
        return None, ""
    reviewed_shas = _fetch_branch_commits(runner, repo, base_ref, old_sha)
    current_shas = _fetch_branch_commits(runner, repo, base_ref, new_sha)
    if reviewed_shas is None or current_shas is None:
        return None, ""
    reviewed_ids = _fetch_patch_ids(runner, patch_id_fn, repo, reviewed_shas)
    current_ids = _fetch_patch_ids(runner, patch_id_fn, repo, current_shas)
    if reviewed_ids is None or current_ids is None:
        return None, ""
    instrument = f"git patch-id --stable per commit over merge-base({base_ref}, head)..head"
    if current_ids - reviewed_ids:
        return False, ""
    dropped = reviewed_ids - current_ids
    if dropped:
        basis = (
            f"every commit now on the branch was reviewed; {len(dropped)} of {len(reviewed_ids)} "
            f"reviewed commit patch(es) no longer present, none added ({instrument})"
        )
    else:
        basis = f"reviewed content unchanged, identical set of {len(current_ids)} commit patch(es) ({instrument})"
    return True, basis


# agent-supervisor#192: a verdict line is recognised on its CONTENT, not its
# emphasis. `**Verdict:` alone missed every reviewer who wrote plain
# `Verdict: APPROVE` or the heading form `## Verdict: APPROVE` -- three
# approved, CI-green PRs (#169, #176, #191) sat blocked because the bold
# form happened to be the only one #53 anchored on. This matches, per LINE
# (not "body starts with"; a verdict line often follows prose or a rationale
# -- #169's REQUEST CHANGES sits after other text), an optional `#`..`######`
# heading marker, an optional `**`/`*` opening emphasis, the literal word
# "Verdict" in any case, then `:`. Liberal in what counts as the LABEL;
# strict about the DECISION text after it -- see `_classify_decision_text`
# for what "strict" means (agent-supervisor#198).
_VERDICT_LINE_RE = re.compile(r"^#{0,6}\s*\*{0,2}verdict:\**\s*(.*)$", re.IGNORECASE)


# agent-supervisor#198: the decision text used to be matched with `in` --
# a substring test -- which read `Verdict: NOT APPROVED` and `Verdict:
# DISAPPROVE` as `approved`, because both strings contain "APPROVE". Every
# other ambiguity in this module refuses; this one instead picked the
# single most dangerous answer, and only in that direction: `REQUEST
# CHANGES` was checked first so a body naming both phrases correctly
# refused, but any rejection phrased WITHOUT those two exact words that
# happened to contain "APPROVE" as a substring landed on approve. The
# failure was one-directional and it always favoured merging.
#
# The fix is a whole-token match against a deliberately short, explicit
# list, decided here rather than grown ad hoc:
#   approved         -- APPROVE, APPROVED
#   rejected         -- REQUEST CHANGES, REQUEST-CHANGES, REJECTED
# Anything else -- including a recognised word wrapped in more words, e.g.
# "APPROVED WITH CHANGES" (a real thing reviewers write, and it is neither
# an approval nor a rejection) -- is unrecognised. A negation anywhere in
# the text (`NOT`, `DIS`, `NO`, `N'T`) is also unrecognised outright, ahead
# of the token match, even though whole-token matching alone already
# defeats the two measured cases (`NOT APPROVED` and `DISAPPROVE` are each
# a single token that is not in either list) -- named explicitly here
# because this module does not attempt sentiment analysis on a negation
# and must not be "improved" into trying to.
_NEGATION_MARKERS = ("NOT", "DIS", "NO", "N'T")
_APPROVED_TOKENS = frozenset({"APPROVE", "APPROVED"})
_REJECTED_TOKENS = frozenset({"REQUEST CHANGES", "REQUEST-CHANGES", "REJECTED"})


def _classify_decision_text(decision_text):
    """`decision_text` is already normalised (trailing markup/punctuation
    stripped, whitespace collapsed, upper-cased) by the caller. Returns
    "approved", "rejected", or None -- None for anything not an exact match
    to the lists above, including a negated one; a decision text this
    module cannot classify must not be guessed at, the same fail-closed
    stance the rest of this module takes."""
    if any(marker in decision_text for marker in _NEGATION_MARKERS):
        return None
    if decision_text in _APPROVED_TOKENS:
        return "approved"
    if decision_text in _REJECTED_TOKENS:
        return "rejected"
    return None


def _scan_verdict_lines(body):
    """The list of `(decision, decision_text)` for every LINE of `body` that
    matches `_VERDICT_LINE_RE` -- agent-supervisor#53's guard 4 (the word
    "verdict" merely mentioned mid-sentence, e.g. "I think the verdict here
    should be ...") still does not match, because the regex is anchored to
    the start of the line, not a substring search. Two more exclusions #192
    adds, because the line-scan (rather than "body starts with") makes a
    verdict line reachable from inside quoted material:

    - a line inside a fenced code block (```...```) is never consulted --
      a verdict quoted as an EXAMPLE must not be read as a real one;
    - a line that is a markdown blockquote (starts with `>`, GitHub's own
      "quote reply" shape) is never consulted -- a reply quoting an EARLIER
      comment's verdict must not be read as this comment restating it.

    `decision_text` is normalised (markup/punctuation stripped, whitespace
    collapsed, upper-cased) before being classified by
    `_classify_decision_text`, and is returned RAW-normalised alongside the
    classification either way -- callers that need to name an unrecognised
    decision in a refusal reason (agent-supervisor#198) get the text to
    name without re-deriving it. `decision` is None for a line whose text
    `_classify_decision_text` does not recognise; the caller decides what
    an unrecognised or absent line means, this function only reports what
    is on each qualifying line.

    Shared by `_parse_verdict_comment` (one comment, may reference several
    lines) and `GithubReviewVerdictSource._comment_verdict` (several
    comments, needs the raw text of an unrecognised LAST verdict line)."""
    in_fence = False
    results = []
    for raw_line in (body or "").splitlines():
        line = raw_line.strip()
        if line.startswith("```"):
            in_fence = not in_fence
            continue
        if in_fence or line.startswith(">"):
            continue
        match = _VERDICT_LINE_RE.match(line)
        if not match:
            continue
        rest = match.group(1)
        end = rest.find("**")
        if end != -1:
            rest = rest[:end]
        decision_text = re.sub(r"[*_`]+$", "", rest.strip()).strip()
        decision_text = decision_text.rstrip(".:;,!").strip()
        decision_text = re.sub(r"\s+", " ", decision_text).upper()
        results.append((_classify_decision_text(decision_text), decision_text))
    return results


def _parse_verdict_comment(body):
    """A comment counts as a verdict when `_scan_verdict_lines` finds at
    least one qualifying line with a RECOGNISED decision (see
    `_classify_decision_text`).

    #196: every qualifying line in the comment is consulted, not just the
    first, so a SINGLE comment can carry more than one verdict line -- a
    reviewer who drafts `Verdict: APPROVE`, reconsiders, and writes
    `Verdict: REQUEST CHANGES` further down. Returning on the first match
    would let that earlier, superseded APPROVE silently win, which is
    exactly the "REQUEST CHANGES read as approved" failure this module
    exists to prevent -- so this collects every qualifying line's decision
    instead of stopping at the first:

    - a line whose decision text is not recognised is SKIPPED, not treated
      as a terminal failure -- a later line in the same comment still gets
      a chance to supply a valid decision;
    - two or more qualifying lines that agree (same decision, repeated) are
      fine -- restating a verdict is not a conflict, and the comment
      resolves to that decision;
    - two or more qualifying lines with DIFFERING decisions make the whole
      comment ambiguous -- this refuses with None rather than picking
      either one, first or last: `unknown`/ambiguous must refuse at the
      merge gate, never resolve to a decision.

    Returns "approved", "rejected", or None -- None when no line qualifies,
    when every qualifying line's decision text is unrecognised, or when
    qualifying lines disagree; a comment this module cannot classify
    cleanly must not be guessed at, the same fail-closed stance every
    other source in this module takes. Callers that need to know WHY --
    unrecognised text vs. no verdict line at all vs. a genuine conflict --
    use `_scan_verdict_lines` directly (agent-supervisor#198); this
    function only answers the yes/no/refuse question."""
    decisions = {decision for decision, _ in _scan_verdict_lines(body) if decision is not None}
    if len(decisions) == 1:
        return decisions.pop()
    return None


_REVIEW_LANE_RE = re.compile(r"(?im)^\s*Review-Lane:\s*([A-Za-z0-9_.:@/-]+)\s*$")


def _parse_review_lane(body):
    """Lane stamp for comment verdicts.

    GitHub login cannot identify a lane in this estate because every agent
    posts through the same account. A comment verdict can only be compared for
    independence when the reviewing lane states its lane id explicitly.
    """
    match = _REVIEW_LANE_RE.search(body or "")
    return match.group(1) if match else None


# agent-supervisor#213: the SHA a `**Verdict:` comment applies to, beside
# `Review-Lane:`. Every OTHER verdict source in this module already refuses
# to answer for a head it never saw (#218's review-object check, extended
# by #226/#229 to tell a content-preserving rebase from a real change) --
# the comment path, which is the estate's PRIMARY posting path (the codex
# lane posts `gh pr comment`, never `gh pr review` -- see this class's own
# docstring), had NO such check at all. Two PRs (#204, #207) merged on an
# APPROVE comment written 6-9 minutes before the commit actually merged,
# one across a rebase, because `_comment_verdict` never compared a SHA.
_REVIEWED_SHA_RE = re.compile(r"(?im)^\s*Reviewed-SHA:\s*([A-Za-z0-9_.:@/-]+)\s*$")


def _parse_reviewed_sha(body):
    """The SHA a `Reviewed-SHA:` trailer states a comment's verdict covers.
    None when absent -- a caller must treat that as "unstated", never as
    "matches"; #213's issue explicitly requires ambiguity to refuse, not to
    read as fresh. Same permissive token class as `_REVIEW_LANE_RE` --
    comparison is exact-string equality against `head_sha` either way, so
    this need not itself validate git SHA shape."""
    match = _REVIEWED_SHA_RE.search(body or "")
    return match.group(1) if match else None


def _newest_commit_date(commits):
    """The latest commit timestamp on the PR, from the `commits` field of
    `gh pr view --json ...,commits` (agent-supervisor#213's timestamp
    backstop -- proposal 2). Prefers `committedDate` (when the commit
    actually landed) over `authoredDate` (which a rebase or amend can carry
    forward unchanged) per commit. Returns None -- never a guess -- unless
    `commits` is a non-empty list of dicts each carrying a usable date; a
    caller must fail closed on None, the same discipline every other
    freshness comparison in this module already takes."""
    if not isinstance(commits, list) or not commits:
        return None
    dates = []
    for commit in commits:
        if not isinstance(commit, dict):
            return None
        date = commit.get("committedDate") or commit.get("authoredDate")
        if not isinstance(date, str) or not date:
            return None
        dates.append(date)
    return max(dates)


class GithubReviewVerdictSource(VerdictSource):
    """Reads GitHub's own review state, and -- agent-supervisor#53 -- issue
    comments with a `Verdict:` line (any heading/emphasis form -- #192).
    Never comment PROSE: a comment only counts through
    `_parse_verdict_comment`'s line-anchored match, the same fail-closed
    discipline #203 established for reviews.

    The codex lane posts its verdicts as `gh pr comment`, not `gh pr
    review` -- a review object alone missed exactly the reviews that
    matter most (agent-supervisor#53). A review object still wins outright
    when it is decisive at the current head: comments are consulted only
    when the review side has nothing decisive to say (`none`, or `unknown`
    from a stale/superseded review), so PRs with a real review object are
    unaffected by this addition."""

    def __init__(self, runner=None, patch_id=None):
        self.runner = runner or _subprocess_runner
        self.patch_id = patch_id or _default_patch_id

    def verdict(self, *, repo, number, head_sha=None):
        try:
            raw = self.runner(
                ["gh", "pr", "view", str(number), "--repo", repo, "--json", "reviews,comments,author,commits"]
            )
            payload = json.loads(raw)
            reviews = payload.get("reviews", [])
            comments = payload.get("comments", [])
            commits = payload.get("commits", [])
            if not isinstance(reviews, list):
                raise ValueError("reviews is not a list")
            if not isinstance(comments, list):
                raise ValueError("comments is not a list")
        except Exception as error:
            return {"verdict": "unknown", "detail": f"github review read failed: {error}"}
        review_result = self._review_verdict(reviews, repo=repo, number=number, head_sha=head_sha)
        if review_result["verdict"] in ("approved", "rejected"):
            return review_result
        comment_result = self._comment_verdict(comments, repo=repo, number=number, head_sha=head_sha, commits=commits)
        if comment_result is not None:
            return comment_result
        return review_result

    def _comment_verdict(self, comments, *, repo=None, number=None, head_sha=None, commits=None):
        """The LAST comment (chronological, as `gh` returns them) that has
        at least one `Verdict:` line -- a later re-review supersedes an
        earlier one, the same "current state" reading the review side
        already gives a fresh review over a stale one. Returns None only
        when NO comment has a qualifying line at all, so the caller falls
        back to `review_result` unchanged.

        agent-supervisor#198: this used to track only the last comment with
        a RECOGNISED decision, silently skipping past one whose decision
        text was unrecognised to an earlier, valid one underneath it -- a
        rejection phrased in words this module could not classify would
        have resolved to the reviewer's own earlier, since-superseded
        approval, the same "stale answer silently wins" failure #196 fixed
        for two lines inside one comment. So the LAST verdict-bearing
        comment is authoritative even when its decision cannot be
        classified: an unrecognised or internally-ambiguous last comment
        returns a decisive `unknown` result naming the unrecognised text,
        never a silent fall-through to an older answer or to `none` --
        `none` must mean "nothing was ever posted", not "something was
        posted that this module could not read" (#192's "never refuses
        without a reason" property, extended to this path).

        agent-supervisor#213: a decisive comment verdict is also checked
        for FRESHNESS against `head_sha`, via `_comment_freshness` -- the
        same guard `_review_verdict` has had since #218, missing here until
        now even though the comment path is this estate's primary posting
        path. `repo`/`number`/`commits` are only used by that check."""
        last = None
        for comment in comments:
            if not isinstance(comment, dict):
                continue
            scan = _scan_verdict_lines(comment.get("body"))
            if not scan:
                continue
            last = (comment, scan)
        if last is None:
            return None
        comment, scan = last
        body = comment.get("body")
        author = (comment.get("author") or {}).get("login")
        reviewer_lane = _parse_review_lane(body)
        created_at = comment.get("createdAt") or ""
        who = f"@{author}" if author else "an unknown author"
        when = f" at {created_at}" if created_at else ""
        lane = f", review lane {reviewer_lane}" if reviewer_lane else ""

        decisions = {decision for decision, _ in scan if decision is not None}
        if len(decisions) == 1:
            verdict_value = decisions.pop()
            detail = f"PR comment verdict ({verdict_value}) by {who}{lane}{when}"
            if head_sha is not None:
                fresh, note, refusal = self._comment_freshness(
                    body=body, created_at=created_at, head_sha=head_sha, commits=commits,
                    repo=repo, number=number,
                )
                if not fresh:
                    return {"verdict": "unknown", "detail": f"{detail} -- {refusal}"}
                if note:
                    detail = f"{detail}, {note}"
            result = {"verdict": verdict_value, "detail": detail, "verdict_kind": "comment"}
            if reviewer_lane:
                result["reviewer_lane"] = reviewer_lane
            return result

        unrecognised = [text for decision, text in scan if decision is None]
        if unrecognised:
            named = "; ".join(f'"{text}"' for text in unrecognised)
            reason = f"decision text not recognised: {named}"
        else:
            reason = "conflicting Verdict: lines in one comment"
        detail = f"PR comment verdict unresolved ({reason}) by {who}{lane}{when}"
        return {"verdict": "unknown", "detail": detail}

    def _comment_freshness(self, *, body, created_at, head_sha, commits, repo, number):
        """Is a `**Verdict:` comment still current at `head_sha`? Two
        mechanisms (agent-supervisor#213), honest one first:

        1. `Reviewed-SHA:` trailer -- the reviewer states the SHA their
           verdict covers. Matching `head_sha`: fresh. Differing: fall back
           to the SAME content-unchanged-since-rebase comparison #218/#226
           already give the review-object path (`_content_unchanged_since`),
           so a pure rebase still promotes; anything else refuses, naming
           both SHAs.
        2. No trailer -- every verdict comment posted before this existed,
           and any reviewer who has not adopted the trailer yet -- the
           timestamp backstop: refuse if the newest commit on the PR is
           younger than this comment. Weaker (a force-push can rewrite
           dates; #213's issue says so explicitly) but it is the only
           signal available for a verdict already on record, and refusing
           beats trusting a comment that cannot be tied to a SHA at all.

        Returns `(fresh, note, refusal)`. `fresh` False means the caller
        MUST return `unknown` with `refusal` appended to its own detail --
        never the decisive verdict. `note`, when non-empty, is a suffix
        for the caller's own detail (e.g. the rebase basis)."""
        reviewed_sha = _parse_reviewed_sha(body)
        if reviewed_sha is not None:
            if reviewed_sha == head_sha:
                return True, "", ""
            unchanged, basis = _content_unchanged_since(
                runner=self.runner, patch_id_fn=self.patch_id,
                repo=repo, number=number, old_sha=reviewed_sha, new_sha=head_sha,
            )
            if unchanged:
                return True, f"head moved {reviewed_sha} -> {head_sha}, {basis}", ""
            return False, "", f"covers Reviewed-SHA {reviewed_sha}, not current head {head_sha}"

        newest = _newest_commit_date(commits)
        if newest is None:
            return False, "", (
                f"no Reviewed-SHA trailer and PR commit timestamps could not be read -- "
                f"cannot tell whether it covers current head {head_sha}"
            )
        if not created_at:
            return False, "", (
                f"no Reviewed-SHA trailer and no timestamp of its own -- "
                f"cannot tell whether it covers current head {head_sha}"
            )
        if newest > created_at:
            return False, "", (
                f"no Reviewed-SHA trailer, and a commit as recent as {newest} exists -- "
                f"cannot confirm it covers current head {head_sha}"
            )
        return True, f"no Reviewed-SHA trailer, timestamp backstop: newest commit {newest} <= verdict {created_at}", ""

    def _review_verdict(self, reviews, *, repo, number, head_sha=None):
        decisive = [r for r in reviews if isinstance(r, dict) and r.get("state") in ("CHANGES_REQUESTED", "APPROVED")]
        if head_sha is None:
            # No head to check freshness against -- answer as before #218,
            # unable to tell a current review from a stale one.
            states = [r.get("state") for r in decisive]
            if "CHANGES_REQUESTED" in states:
                return {"verdict": "rejected", "detail": "GitHub review state CHANGES_REQUESTED", "verdict_kind": "github_review"}
            if "APPROVED" in states:
                return {"verdict": "approved", "detail": "GitHub review state APPROVED", "verdict_kind": "github_review"}
            return {"verdict": "none", "detail": ""}
        current = [r for r in decisive if (r.get("commit") or {}).get("oid") == head_sha]
        rebase_basis = None
        if not current and decisive:
            stale_oids = sorted({(r.get("commit") or {}).get("oid") for r in decisive if (r.get("commit") or {}).get("oid")})
            for oid in stale_oids:
                unchanged, basis = _content_unchanged_since(
                    runner=self.runner,
                    patch_id_fn=self.patch_id,
                    repo=repo,
                    number=number,
                    old_sha=oid,
                    new_sha=head_sha,
                )
                if unchanged:
                    current = [r for r in decisive if (r.get("commit") or {}).get("oid") == oid]
                    rebase_basis = f"head moved {oid} -> {head_sha}, {basis}"
                    break
        current_states = [r.get("state") for r in current]
        if "CHANGES_REQUESTED" in current_states:
            return {
                "verdict": "rejected",
                "detail": rebase_basis or f"GitHub review state CHANGES_REQUESTED at {head_sha}",
                "verdict_kind": "github_review",
            }
        if "APPROVED" in current_states:
            return {
                "verdict": "approved",
                "detail": rebase_basis or f"GitHub review state APPROVED at {head_sha}",
                "verdict_kind": "github_review",
            }
        if decisive:
            stale_shas = sorted({(r.get("commit") or {}).get("oid") or "unknown-sha" for r in decisive})
            return {
                "verdict": "unknown",
                "detail": f"review(s) filed against {', '.join(stale_shas)}, not current head {head_sha}",
            }
        return {"verdict": "none", "detail": ""}


class LedgerVerdictSource(VerdictSource):
    """Reads a verdict a reviewing lane recorded in the supervisor ledger."""

    def __init__(self, ledger, runner=None, patch_id=None):
        self.ledger = ledger
        self.runner = runner or _subprocess_runner
        self.patch_id = patch_id or _default_patch_id

    def verdict(self, *, repo, number, head_sha=None):
        try:
            row = self.ledger.get_pr_verdict(repo=repo, number=number)
        except Exception as error:
            return {"verdict": "unknown", "detail": f"ledger read failed: {error}"}
        if row is None:
            return {"verdict": "none", "detail": ""}
        if row.get("verdict") not in ("approved", "rejected"):
            return {"verdict": "unknown", "detail": "ledger row has an unrecognised verdict value"}
        recorded_sha = row.get("head_sha")
        if head_sha is not None and recorded_sha != head_sha:
            unchanged, basis = _content_unchanged_since(
                runner=self.runner,
                patch_id_fn=self.patch_id,
                repo=repo,
                number=number,
                old_sha=recorded_sha,
                new_sha=head_sha,
            )
            if unchanged:
                return {
                    "verdict": row["verdict"],
                    "detail": (
                        f"ledger: {row['reviewer']} recorded at {row['updated_at']} for {recorded_sha}, "
                        f"head moved {recorded_sha} -> {head_sha}, {basis}"
                    ),
                    "verdict_kind": "ledger",
                    "reviewer_lane": row["reviewer"],
                }
            return {
                "verdict": "unknown",
                "detail": f"ledger verdict recorded at {recorded_sha}, not current head {head_sha}",
            }
        return {
            "verdict": row["verdict"],
            "detail": f"ledger: {row['reviewer']} recorded at {row['updated_at']} for {recorded_sha}",
            "verdict_kind": "ledger",
            "reviewer_lane": row["reviewer"],
        }


SOURCES = {
    "github": GithubReviewVerdictSource,
    "ledger": LedgerVerdictSource,
}


def build_source(name, *, state_dir):
    if name not in SOURCES:
        raise ValueError(f"unknown verdict source: {name!r} (known: {', '.join(sorted(SOURCES))})")
    if name == "ledger":
        return LedgerVerdictSource(Ledger(state_dir))
    return SOURCES[name]()


def resolve(names, *, state_dir, repo, number, head_sha=None):
    """Try each named source in order. A decisive verdict (approved/rejected)
    from an earlier source wins outright. If none is decisive, a source
    error ("unknown") wins over a later source's plain "none" -- an error
    must never be silently masked by "nothing to report" from elsewhere in
    the chain. Only when every source is reachable and none has anything on
    record does this return "none". `head_sha`, when given, is the PR's
    current head -- a source is passed it so it can refuse to answer for a
    review or ledger record filed against an older commit (#218)."""
    first_unknown = None
    for name in names:
        try:
            source = build_source(name, state_dir=state_dir)
            result = source.verdict(repo=repo, number=number, head_sha=head_sha)
        except Exception as error:
            result = {"verdict": "unknown", "detail": f"{name}: {error}"}
        if result.get("verdict") not in VERDICT_VALUES:
            result = {"verdict": "unknown", "detail": f"{name}: returned an unrecognised verdict"}
        if result["verdict"] in ("approved", "rejected"):
            return result
        if result["verdict"] == "unknown" and first_unknown is None:
            first_unknown = result
    return first_unknown or {"verdict": "none", "detail": ""}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state-dir", required=True)
    sub = parser.add_subparsers(dest="command", required=True)

    get = sub.add_parser("get", help="resolve the verdict for one PR")
    get.add_argument("--repo", required=True, help="owner/name")
    get.add_argument("--number", type=int, required=True)
    get.add_argument(
        "--source",
        default="github",
        help="comma-separated source names to try in order (default: github; "
        "'ledger' has no caller that writes to it yet -- agent-dotfiles#214)",
    )
    get.add_argument(
        "--head-sha",
        default=None,
        help="the PR's current headRefOid; a verdict filed against a different "
        "commit resolves to unknown rather than answering for a head it never "
        "saw (agent-dotfiles#218). Omit only when the caller has no head to "
        "check against.",
    )

    record = sub.add_parser("record", help="record a verdict in the ledger source")
    record.add_argument("--repo", required=True, help="owner/name")
    record.add_argument("--number", type=int, required=True)
    record.add_argument("--verdict", choices=("approved", "rejected"), required=True)
    record.add_argument("--head-sha", required=True)
    record.add_argument("--reviewer", required=True)
    record.add_argument("--note")

    args = parser.parse_args(argv)

    if args.command == "record":
        ledger = Ledger(args.state_dir)
        row = ledger.record_pr_verdict(
            repo=args.repo,
            number=args.number,
            verdict=args.verdict,
            head_sha=args.head_sha,
            reviewer=args.reviewer,
            note=args.note,
        )
        print(json.dumps(row))
        return 0

    try:
        names = [n.strip() for n in args.source.split(",") if n.strip()]
        if not names:
            raise ValueError("no verdict source named")
        result = resolve(
            names, state_dir=args.state_dir, repo=args.repo, number=args.number, head_sha=args.head_sha
        )
    except Exception as error:
        result = {"verdict": "unknown", "detail": f"verdict resolution failed: {error}"}
    print(json.dumps(result))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
