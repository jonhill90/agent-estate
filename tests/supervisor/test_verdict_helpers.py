import json
import sys
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import (  # noqa: E402
    _VERDICT_LINE_RE,
    _classify_decision_text,
    _label_inside_inline_code,
    _normalise_decision_text,
)


REPO = "jonhill90/agent-dotfiles"


# A patch that is on the branch in BOTH the reviewed and the current head,
# at two different hunk offsets -- what a rebase does to a commit it does not
# touch. `git patch-id --stable` must hash these equal.
def _patch(marker, offset=10, replacement="new line"):
    return (
        f"diff --git a/{marker}.txt b/{marker}.txt\n"
        "index 1111111..2222222 100644\n"
        f"--- a/{marker}.txt\n"
        f"+++ b/{marker}.txt\n"
        f"@@ -{offset},3 +{offset},3 @@\n"
        " line before\n"
        "-old line\n"
        f"+{replacement}\n"
        " line after\n"
    )


BASE_REF = "main"


def _api_runner(*, reviews="{}", branches=None, patches=None, base_ref=BASE_REF, seen=None):
    """A `runner` over the four calls the #226 comparison makes.

    `branches` maps a head SHA to the commit SHAs that head introduces over
    the base branch; `patches` maps a commit SHA to its diff. The stub REFUSES
    any compare whose base is not `base_ref` -- that is the regression guard
    for agent-dotfiles#229's blocking finding: the first implementation asked
    `compare/old...new` and `compare/new...old`, which resolve to the same
    (pre-rebase) merge base and so measured whether `main` had moved. Anchor
    on the PR's base branch or the comparison cannot be computed at all."""
    branches = branches or {}
    patches = patches or {}

    def runner(cmd):
        if seen is not None:
            seen.append(cmd)
        if cmd[:3] == ["gh", "pr", "view"]:
            if cmd[-1] == "baseRefName":
                return json.dumps({"baseRefName": base_ref})
            return reviews
        joined = " ".join(cmd)
        if "/compare/" in joined:
            spec = joined.split("/compare/", 1)[1].split(" ", 1)[0]
            base, _, head = spec.partition("...")
            if base != base_ref:
                raise AssertionError(
                    "compare must be anchored on the PR's base branch, not on the other "
                    f"head (agent-dotfiles#226/#229) -- asked for compare/{spec}"
                )
            return "".join(f"{sha}\n" for sha in branches[head])
        if "/commits/" in joined:
            sha = joined.split("/commits/", 1)[1].split(" ", 1)[0]
            return patches[sha]
        raise RuntimeError(f"no stub for command: {cmd!r}")

    return runner


def _raising_runner(cmd):
    """A `runner` that always raises -- used where a test's fake SHAs are not
    real git objects, so the #226 rebase-content comparison must fail closed
    ("cannot compute") rather than reach a real `gh`/network."""
    raise RuntimeError(f"no stub for command: {cmd!r}")


def _comment_runner(*, reviews=None, comments=None, author=None, commits=None):
    """A `runner` for `gh pr view ... --json reviews,comments,author,commits`
    -- agent-supervisor#53, `commits` added by #213 so the comment-verdict
    freshness backstop has PR commit timestamps to compare against. Raises
    for anything else, the same discipline `_reviews_runner` uses, so a
    test that does not opt into the rebase comparison gets a fail-closed
    "cannot compute" rather than a stub silently answering for a call it
    was never given."""
    payload = json.dumps(
        {"reviews": reviews or [], "comments": comments or [], "author": author or {}, "commits": commits or []}
    )

    def runner(cmd):
        if cmd[:3] == ["gh", "pr", "view"]:
            return payload
        raise RuntimeError(f"no stub for command: {cmd!r}")

    return runner


def _pre_595_scan(body):
    """Reimplementation of `_scan_verdict_lines` AS IT WAS before
    agent-supervisor#595/#609: fenced code blocks and blockquotes excluded,
    the inline-code label guard (#540) applied, but NO three-line block
    requirement -- a bare `Verdict:`-shaped label alone was enough. Used
    only by this class's mutation tests, to prove each poisoning fixture
    below actually fooled the OLD code (not merely that it fails to fool
    the new code, which could be true for an unrelated reason)."""
    lines = []
    in_fence = False
    for raw_line in (body or "").splitlines():
        line = raw_line.strip()
        if line.startswith("```"):
            in_fence = not in_fence
            continue
        if in_fence or line.startswith(">"):
            continue
        lines.append(line)

    results = []
    for line in lines:
        match = _VERDICT_LINE_RE.match(line)
        if not match:
            continue
        if _label_inside_inline_code(line, match.start(1), match.end(1)):
            continue
        decision_text = _normalise_decision_text(match.group(2))
        results.append((_classify_decision_text(decision_text), decision_text))
    return results
