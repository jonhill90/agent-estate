import json
import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from ci_gate import CiGate  # noqa: E402


class FakeRunner:
    """Answers exactly the three `gh` calls `CiGate` makes, each keyed off
    the SHA in the command itself -- so a test can hand back data for a
    DIFFERENT sha than the one requested to simulate a stale or wrong
    response, the shape agent-supervisor#13's "green for an older SHA" bug
    takes."""

    def __init__(self, *, head_sha, check_runs, statuses=None):
        self.head_sha = head_sha
        self.check_runs = check_runs
        self.statuses = statuses if statuses is not None else []
        self.calls = []

    def __call__(self, command, *, timeout=None):
        self.calls.append(command)
        if command[:3] == ["gh", "pr", "view"]:
            return json.dumps({"headRefOid": self.head_sha})
        if command[1] == "api" and command[2].endswith("/check-runs"):
            return json.dumps(self.check_runs)
        if command[1] == "api" and command[2].endswith("/status"):
            return json.dumps({"statuses": self.statuses})
        raise AssertionError(f"unexpected command: {command}")


def run(check_sha, name="test", conclusion="success", status="completed"):
    return [{"name": name, "head_sha": check_sha, "status": status, "conclusion": conclusion}]


def run_at(check_sha, name, *, conclusion, started_at, completed_at=None, status="completed"):
    entry = {
        "name": name,
        "head_sha": check_sha,
        "status": status,
        "conclusion": conclusion,
        "started_at": started_at,
    }
    if completed_at is not None:
        entry["completed_at"] = completed_at
    return entry


class CiGateTest(unittest.TestCase):
    def test_head_sha_failing_check_refuses_and_names_sha_and_job(self):
        runner = FakeRunner(head_sha="deadbeef", check_runs=run("deadbeef", name="test", conclusion="failure"))
        result = CiGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])
        self.assertEqual("deadbeef", result["sha"])
        self.assertIn("deadbeef", result["reason"])
        self.assertIn("test", result["reason"])

    def test_check_green_for_an_older_sha_than_head_refuses(self):
        # The check-runs endpoint hands back a run stamped with an OLDER
        # commit than the head `CiGate` just fetched -- exactly the shape a
        # cache, proxy, or stale snapshot produces. This must refuse even
        # though the run itself says SUCCESS.
        runner = FakeRunner(head_sha="new-sha", check_runs=run("old-sha", conclusion="success"))
        result = CiGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])
        self.assertEqual("new-sha", result["sha"])
        self.assertIn("old-sha", result["reason"])
        self.assertIn("new-sha", result["reason"])

    def test_green_for_the_exact_head_sha_proceeds(self):
        runner = FakeRunner(head_sha="good-sha", check_runs=run("good-sha", conclusion="success"))
        result = CiGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("allow", result["decision"])
        self.assertEqual("good-sha", result["sha"])

    def test_absent_checks_refuse_rather_than_pass_as_pending(self):
        runner = FakeRunner(head_sha="sha", check_runs=[], statuses=[])
        result = CiGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])
        self.assertIn("no checks reported", result["reason"])

    def test_in_progress_check_refuses_not_just_failure(self):
        runner = FakeRunner(head_sha="sha", check_runs=run("sha", status="in_progress", conclusion=None))
        result = CiGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])

    def test_cancelled_check_reason_distinguishes_from_in_progress(self):
        # agent-supervisor#380: both used to read as plain "not green". A
        # cancelled run will never go green on its own (needs a re-run); an
        # in-progress run still might. The reason string must say which.
        cancelled = FakeRunner(head_sha="sha", check_runs=run("sha", status="completed", conclusion="cancelled"))
        cancelled_result = CiGate(cancelled).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", cancelled_result["decision"])
        self.assertIn("cancelled", cancelled_result["reason"])
        self.assertIn("will not go green", cancelled_result["reason"])

        in_progress = FakeRunner(head_sha="sha", check_runs=run("sha", status="in_progress", conclusion=None))
        in_progress_result = CiGate(in_progress).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", in_progress_result["decision"])
        self.assertIn("in_progress", in_progress_result["reason"])
        self.assertIn("may still go green", in_progress_result["reason"])

    def test_failing_legacy_commit_status_refuses(self):
        runner = FakeRunner(
            head_sha="sha",
            check_runs=run("sha", conclusion="success"),
            statuses=[{"context": "ci/legacy", "state": "failure"}],
        )
        result = CiGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])
        self.assertIn("ci/legacy", result["reason"])

    def test_gh_failure_refuses_rather_than_raising(self):
        def boom(command, *, timeout=None):
            raise RuntimeError("network unavailable")

        result = CiGate(boom).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])
        self.assertIn("network unavailable", result["reason"])

    def test_two_runs_of_one_check_newest_green_allows(self):
        # An old failing run of `test` plus a newer passing re-run of `test`,
        # both stamped with the current head SHA. Only the newest matters.
        runs = [
            run_at("sha", "test", conclusion="failure",
                   started_at="2026-08-16T07:46:02Z", completed_at="2026-08-16T08:01:11Z"),
            run_at("sha", "test", conclusion="success",
                   started_at="2026-08-16T15:36:24Z", completed_at="2026-08-16T15:46:00Z"),
            run_at("sha", "gate", conclusion="success",
                   started_at="2026-08-16T15:36:25Z", completed_at="2026-08-16T15:36:32Z"),
        ]
        runner = FakeRunner(head_sha="sha", check_runs=runs)
        result = CiGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("allow", result["decision"])

    def test_two_runs_of_one_check_newest_red_refuses_even_with_older_green(self):
        # The important case: the newest run of `test` FAILED and an older
        # run of the same check PASSED. A careless "any success counts"
        # implementation would rubber-stamp this. It must refuse.
        runs = [
            run_at("sha", "test", conclusion="success",
                   started_at="2026-08-16T07:46:02Z", completed_at="2026-08-16T08:01:11Z"),
            run_at("sha", "test", conclusion="failure",
                   started_at="2026-08-16T15:36:24Z", completed_at="2026-08-16T15:46:00Z"),
        ]
        runner = FakeRunner(head_sha="sha", check_runs=runs)
        result = CiGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])
        self.assertIn("test", result["reason"])

    def test_only_an_old_failure_refuses(self):
        runs = [
            run_at("sha", "test", conclusion="failure",
                   started_at="2026-08-16T07:46:02Z", completed_at="2026-08-16T08:01:11Z"),
        ]
        runner = FakeRunner(head_sha="sha", check_runs=runs)
        result = CiGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])

    def test_queued_newest_run_refuses_even_with_older_green(self):
        # The newest run of `test` has no completed_at at all (still queued
        # or in progress) -- it must not be skipped over to reach the older
        # success.
        runs = [
            run_at("sha", "test", conclusion="success",
                   started_at="2026-08-16T07:46:02Z", completed_at="2026-08-16T08:01:11Z"),
            run_at("sha", "test", conclusion=None, status="queued",
                   started_at="2026-08-16T15:36:24Z"),
        ]
        runner = FakeRunner(head_sha="sha", check_runs=runs)
        result = CiGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])

    def test_stale_gate_failure_superseded_by_later_green_ui_evidence_allows(self):
        # agent-supervisor#518 / #516: `ui-evidence.yml`'s `gate` job runs
        # only on `push` and never re-triggers when a PR satisfies UI
        # evidence via a follow-up comment (the #468 pattern). The
        # separately-named, `issue_comment`-triggered `ui-evidence`
        # check-run goes green afterward, at the same head SHA, and is the
        # one that actually reflects reality. Measured on #516
        # (e9ceb89d4dda6a19c7e934f6d0abeae328b95f34): `gate` FAILURE
        # completed 07:33:24Z, `ui-evidence` SUCCESS completed 07:46:04Z.
        runs = [
            run_at("sha", "gate", conclusion="failure",
                   started_at="2026-08-23T07:33:17Z", completed_at="2026-08-23T07:33:24Z"),
            run_at("sha", "ui-evidence", conclusion="success",
                   started_at="2026-08-23T07:46:04Z", completed_at="2026-08-23T07:46:04Z"),
        ]
        runner = FakeRunner(head_sha="sha", check_runs=runs)
        result = CiGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("allow", result["decision"])

    def test_stale_gate_failure_without_any_ui_evidence_run_still_refuses(self):
        # No superseding check at all -- the OLD-code behaviour, and it must
        # stay the behaviour here too: a `gate` failure with nothing to
        # supersede it is a genuine failure.
        runs = [
            run_at("sha", "gate", conclusion="failure",
                   started_at="2026-08-23T07:33:17Z", completed_at="2026-08-23T07:33:24Z"),
        ]
        runner = FakeRunner(head_sha="sha", check_runs=runs)
        result = CiGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])
        self.assertIn("gate", result["reason"])

    def test_gate_failure_with_also_failing_ui_evidence_still_refuses(self):
        # The superseding check exists but is itself red -- a failing
        # "supersede" clears nothing.
        runs = [
            run_at("sha", "gate", conclusion="failure",
                   started_at="2026-08-23T07:33:17Z", completed_at="2026-08-23T07:33:24Z"),
            run_at("sha", "ui-evidence", conclusion="failure",
                   started_at="2026-08-23T07:46:04Z", completed_at="2026-08-23T07:46:04Z"),
        ]
        runner = FakeRunner(head_sha="sha", check_runs=runs)
        result = CiGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])

    def test_gate_failure_after_an_earlier_ui_evidence_success_still_refuses(self):
        # The `ui-evidence` success happened FIRST and the `gate` failure
        # came later -- that is a real, later failure, not a stale earlier
        # one being superseded by a fresher green run. Must still refuse.
        runs = [
            run_at("sha", "ui-evidence", conclusion="success",
                   started_at="2026-08-23T07:20:00Z", completed_at="2026-08-23T07:20:00Z"),
            run_at("sha", "gate", conclusion="failure",
                   started_at="2026-08-23T07:33:17Z", completed_at="2026-08-23T07:33:24Z"),
        ]
        runner = FakeRunner(head_sha="sha", check_runs=runs)
        result = CiGate(runner).evaluate(repo="o/r", number=1)
        self.assertEqual("refuse", result["decision"])

    def test_evaluate_fetches_head_itself_every_call(self):
        # evaluate() takes only repo/number -- there is no head-sha
        # parameter a caller could pass to substitute a cached snapshot for
        # a live fetch. Calling it twice re-fetches both times.
        runner = FakeRunner(head_sha="sha", check_runs=run("sha", conclusion="success"))
        gate = CiGate(runner)
        gate.evaluate(repo="o/r", number=1)
        gate.evaluate(repo="o/r", number=1)
        view_calls = [c for c in runner.calls if c[:3] == ["gh", "pr", "view"]]
        self.assertEqual(2, len(view_calls))


if __name__ == "__main__":
    unittest.main()
