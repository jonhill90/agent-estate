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


class GithubReviewVerdictSource(VerdictSource):
    """Reads GitHub's own review state, never comment prose."""

    def __init__(self, runner=None, patch_id=None):
        self.runner = runner or _subprocess_runner
        self.patch_id = patch_id or _default_patch_id

    def verdict(self, *, repo, number, head_sha=None):
        try:
            raw = self.runner(["gh", "pr", "view", str(number), "--repo", repo, "--json", "reviews"])
            payload = json.loads(raw)
            reviews = payload.get("reviews", [])
            if not isinstance(reviews, list):
                raise ValueError("reviews is not a list")
        except Exception as error:
            return {"verdict": "unknown", "detail": f"github review read failed: {error}"}
        decisive = [r for r in reviews if isinstance(r, dict) and r.get("state") in ("CHANGES_REQUESTED", "APPROVED")]
        if head_sha is None:
            # No head to check freshness against -- answer as before #218,
            # unable to tell a current review from a stale one.
            states = [r.get("state") for r in decisive]
            if "CHANGES_REQUESTED" in states:
                return {"verdict": "rejected", "detail": "GitHub review state CHANGES_REQUESTED"}
            if "APPROVED" in states:
                return {"verdict": "approved", "detail": "GitHub review state APPROVED"}
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
            return {"verdict": "rejected", "detail": rebase_basis or f"GitHub review state CHANGES_REQUESTED at {head_sha}"}
        if "APPROVED" in current_states:
            return {"verdict": "approved", "detail": rebase_basis or f"GitHub review state APPROVED at {head_sha}"}
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
                }
            return {
                "verdict": "unknown",
                "detail": f"ledger verdict recorded at {recorded_sha}, not current head {head_sha}",
            }
        return {
            "verdict": row["verdict"],
            "detail": f"ledger: {row['reviewer']} recorded at {row['updated_at']} for {recorded_sha}",
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
