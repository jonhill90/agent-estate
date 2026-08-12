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


class GithubReviewVerdictSource(VerdictSource):
    """Reads GitHub's own review state, never comment prose."""

    def __init__(self, runner=None):
        self.runner = runner or _subprocess_runner

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
        current_states = [r.get("state") for r in current]
        if "CHANGES_REQUESTED" in current_states:
            return {"verdict": "rejected", "detail": f"GitHub review state CHANGES_REQUESTED at {head_sha}"}
        if "APPROVED" in current_states:
            return {"verdict": "approved", "detail": f"GitHub review state APPROVED at {head_sha}"}
        if decisive:
            stale_shas = sorted({(r.get("commit") or {}).get("oid") or "unknown-sha" for r in decisive})
            return {
                "verdict": "unknown",
                "detail": f"review(s) filed against {', '.join(stale_shas)}, not current head {head_sha}",
            }
        return {"verdict": "none", "detail": ""}


class LedgerVerdictSource(VerdictSource):
    """Reads a verdict a reviewing lane recorded in the supervisor ledger."""

    def __init__(self, ledger):
        self.ledger = ledger

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
