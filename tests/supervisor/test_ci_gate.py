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
