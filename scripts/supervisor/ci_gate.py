"""CI as a gate, not a badge (agent-supervisor#13).

Branch protection and rulesets are both unavailable on this private repo
without GitHub Pro -- measured directly, not inferred from docs:

    $ gh api repos/jonhill90/agent-supervisor/branches/main/protection
    403 "Upgrade to GitHub Pro or make this repository public..."
    $ gh api repos/jonhill90/agent-supervisor/rulesets
    403 (same)
    $ gh api repos/jonhill90/agent-supervisor/rulesets -X POST -f name=x ...
    403 (same -- creating one is refused too, not just listing)

So GitHub enforces nothing here: a red run does not block `gh pr merge`,
it only informs whoever is looking, and "CI gates this repo" is a
sentence about convention, not a mechanism. This module is the mechanism
-- a hard refusal a merge wrapper (`merge-pr.sh`) must consult, in the
actor rather than the platform. It is still weaker than real branch
protection: nothing stops a caller from running `gh pr merge` directly
and skipping this module entirely. That gap is not closeable without
Pro or a public repo, and is stated here rather than left implicit.

THE SHA THIS CHECKS, exactly -- `evaluate()` takes only `repo` and
`number`, never a caller-supplied head SHA. It fetches `headRefOid`
itself, immediately before reading checks, and reads checks for that
exact SHA via `commits/{sha}/check-runs` and `commits/{sha}/status`
(SHA-scoped endpoints, not `gh pr view`'s `statusCheckRollup`, which
carries no SHA a caller can compare). A caller that fetched a PR
snapshot earlier -- the supervisor's sensor.py does this every tick --
and passed that snapshot's SHA in would reintroduce exactly the bug this
exists to close: check green for an OLDER commit than head, because the
push that invalidated it happened after the snapshot was taken and
before the merge. Refusing the parameter closes that path structurally
instead of relying on every caller to remember to re-fetch.

Even the SHA-scoped `check-runs` response is not trusted blindly: each
run's own `head_sha` field is compared against the SHA just fetched
(`_refuse_on_sha_mismatch`). GitHub's API should never return a run for
the wrong commit given a SHA-scoped URL -- this defends against a proxy,
a cache, or a future bug doing exactly that, and is the one comparison
the "mutate and confirm it goes red" step in the brief targets.

Absent is not pending (#149's distinction, inherited from verdict.py's
GithubReviewVerdictSource / LedgerVerdictSource). A SHA with zero check
runs and zero legacy statuses is refused, not treated as "nothing to
block on" -- a push that raced past CI, or a workflow that never
triggered, must not read the same as a green run.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys


def subprocess_runner(command):
    return subprocess.run(command, check=True, capture_output=True, text=True, timeout=30).stdout


GREEN_CONCLUSIONS = ("success", "neutral", "skipped")


class CiGate:
    def __init__(self, runner=None):
        self.runner = runner or subprocess_runner

    def _head_sha(self, repo, number):
        raw = self.runner(["gh", "pr", "view", str(number), "--repo", repo, "--json", "headRefOid"])
        payload = json.loads(raw)
        sha = payload.get("headRefOid") if isinstance(payload, dict) else None
        if not isinstance(sha, str) or not sha:
            raise ValueError("gh did not return a head SHA for this PR")
        return sha

    def _check_runs(self, repo, sha):
        raw = self.runner(["gh", "api", f"repos/{repo}/commits/{sha}/check-runs", "--jq", ".check_runs"])
        runs = json.loads(raw)
        if not isinstance(runs, list):
            raise ValueError("gh returned malformed check-runs for this SHA")
        return runs

    def _combined_status(self, repo, sha):
        raw = self.runner(["gh", "api", f"repos/{repo}/commits/{sha}/status", "--jq", "{statuses: .statuses}"])
        payload = json.loads(raw)
        statuses = payload.get("statuses") if isinstance(payload, dict) else None
        if not isinstance(statuses, list):
            raise ValueError("gh returned malformed commit status for this SHA")
        return statuses

    def evaluate(self, *, repo, number):
        try:
            sha = self._head_sha(repo, number)
        except Exception as error:
            return {"decision": "refuse", "sha": None, "reason": f"could not read PR head: {error}"}

        try:
            check_runs = self._check_runs(repo, sha)
            statuses = self._combined_status(repo, sha)
        except Exception as error:
            return {"decision": "refuse", "sha": sha, "reason": f"could not read checks for {sha}: {error}"}

        for run in check_runs:
            run_sha = run.get("head_sha") if isinstance(run, dict) else None
            if run_sha != sha:
                return {
                    "decision": "refuse",
                    "sha": sha,
                    "reason": (
                        f"check run {run.get('name', '?') if isinstance(run, dict) else '?'} "
                        f"reports head_sha {run_sha!r}, not PR head {sha}"
                    ),
                }

        if not check_runs and not statuses:
            return {"decision": "refuse", "sha": sha, "reason": f"no checks reported for {sha}"}

        failing_runs = [
            run.get("name", "?")
            for run in check_runs
            if not (run.get("status") == "completed" and run.get("conclusion") in GREEN_CONCLUSIONS)
        ]
        failing_statuses = [s.get("context", "?") for s in statuses if s.get("state") != "success"]
        failing = failing_runs + failing_statuses
        if failing:
            return {
                "decision": "refuse",
                "sha": sha,
                "reason": f"check(s) not green at {sha}: {', '.join(failing)}",
            }

        return {"decision": "allow", "sha": sha, "reason": f"all checks green at {sha}"}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", required=True, help="owner/name")
    parser.add_argument("--number", type=int, required=True)
    args = parser.parse_args(argv)

    gate = CiGate()
    result = gate.evaluate(repo=args.repo, number=args.number)
    print(json.dumps(result))
    return 0 if result["decision"] == "allow" else 1


if __name__ == "__main__":
    sys.exit(main())
