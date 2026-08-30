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

RE-RUNS (#269) -- `commits/{sha}/check-runs` returns every run of every
check for that SHA, not just the newest: a re-run of `test` after a flaky
failure leaves both the old failing run and the new passing one in the
list. `gh pr checks` and GitHub's PR UI evaluate only the latest run per
check name; `_latest_per_name` does the same here, keyed on `completed_at`
falling back to `started_at` for a run still in flight. This is
latest-per-name, never best-per-name -- if the newest run of a check
failed and an older run of the same check passed, that is red. Only
narrowing what counts as "the check", never widening what counts as
green.

STALE `gate` SUPERSEDED BY `ui-evidence` (#518, surfaced by #516) --
`_latest_per_name` collapses re-runs of the SAME name, but `ui-evidence.yml`'s
`gate` job (push-triggered) and the separately-named `ui-evidence`
check-run (issue_comment-triggered, satisfying a follow-up-comment #468
evidence pattern) are two DIFFERENT names for what is really the same
requirement -- `_latest_per_name` has no relationship between them to
collapse, so a `gate` job that only ever runs on push stays a permanent
FAILURE even after the PR is genuinely green.
`_gate_superseded_by_ui_evidence` is the one narrow exception to "only
narrowing what counts as green" above: a FAILURE `gate` run is excluded
from the failing list ONLY when a same-SHA `ui-evidence` run is itself
green and completed strictly later than the `gate` failure. This is named
explicitly for this one relationship, not a general "an unrelated green
check clears an unrelated red one" rule.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys


def subprocess_runner(command):
    return subprocess.run(command, check=True, capture_output=True, text=True, timeout=30).stdout


GREEN_CONCLUSIONS = ("success", "neutral", "skipped")


def _run_sort_key(run):
    # `completed_at` is missing while a run is still queued/in_progress --
    # fall back to `started_at` so an in-progress run still sorts by when it
    # started, rather than being treated as older than every finished run
    # (which would let a stale success win the "latest" slot). Both are
    # ISO-8601 UTC timestamps ("...Z"), so lexical order matches chronological
    # order without parsing them.
    return run.get("completed_at") or run.get("started_at") or ""


# agent-supervisor#380: a merge gate that only ever prints "not green" makes
# `cancelled` (will never go green without a re-run) read identically to
# `in_progress`/`conclusion: null` (may still go green on its own). Whoever
# reads this reason -- a human or a lane deciding whether to wait or
# re-dispatch -- needs that distinction spelled out, even though the
# decision itself (refuse) is the same either way.
def _describe_failing_run(run):
    name = run.get("name", "?")
    if run.get("status") == "completed" and run.get("conclusion") == "cancelled":
        return f"{name} (cancelled -- will not go green without a re-run)"
    if run.get("status") != "completed":
        return f"{name} ({run.get('status', 'unknown')} -- may still go green)"
    return f"{name} ({run.get('conclusion', 'unknown')})"


def _latest_per_name(check_runs):
    """Re-runs of the same check (e.g. `test`) land as separate entries in
    `check_runs` -- GitHub's own PR UI and `gh pr checks` both evaluate only
    the newest one per name. Do the same here: a stale failing run must not
    keep refusing once a newer run of the same check has succeeded, and
    conversely a fresh failure must not be shadowed by an older success."""
    latest_by_name = {}
    for run in check_runs:
        name = run.get("name", "?")
        current = latest_by_name.get(name)
        if current is None or _run_sort_key(run) >= _run_sort_key(current):
            latest_by_name[name] = run
    return list(latest_by_name.values())


def _gate_superseded_by_ui_evidence(latest_runs):
    """agent-supervisor#518 / #516: `ui-evidence.yml`'s `gate` job runs only
    on `pull_request` (push) and is never re-triggered when a PR satisfies
    UI evidence via a follow-up comment -- the #468 pattern this repo's own
    docs describe as valid. That leaves a permanently stale FAILURE `gate`
    run sitting at the head SHA even after the PR is genuinely green,
    because the separately-named, `issue_comment`-triggered `ui-evidence`
    check-run (workflow "Validate") that actually reflects reality lands
    under a different name and `_latest_per_name` has no relationship
    between the two names to collapse.

    This is narrowly the one stale/superseding relationship named in
    agent-supervisor#518 -- a FAILURE `gate` run superseded ONLY by a
    same-SHA `ui-evidence` run that is itself green AND completed strictly
    later than the stale failure. It does not become a general "ignore old
    failures" rule:
      - no same-SHA `ui-evidence` run at all -> not superseded, still refuse
      - the `ui-evidence` run exists but is not itself green -> not
        superseded, still refuse (a failing superseding check clears nothing)
      - the `ui-evidence` run completed BEFORE the `gate` failure (i.e. the
        `gate` failure is a real, later failure, not a stale earlier one)
        -> not superseded, still refuse
    """
    gate = next((run for run in latest_runs if run.get("name") == "gate"), None)
    if gate is None:
        return False
    if not (gate.get("status") == "completed" and gate.get("conclusion") == "failure"):
        return False

    ui_evidence = next((run for run in latest_runs if run.get("name") == "ui-evidence"), None)
    if ui_evidence is None:
        return False
    if not (ui_evidence.get("status") == "completed" and ui_evidence.get("conclusion") in GREEN_CONCLUSIONS):
        return False

    return _run_sort_key(ui_evidence) > _run_sort_key(gate)


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

        latest_runs = _latest_per_name(check_runs)
        gate_superseded = _gate_superseded_by_ui_evidence(latest_runs)
        failing_runs = [
            _describe_failing_run(run)
            for run in latest_runs
            if not (run.get("status") == "completed" and run.get("conclusion") in GREEN_CONCLUSIONS)
            and not (gate_superseded and run.get("name") == "gate")
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
