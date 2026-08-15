import json
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
from reconcile_lane_completions import LaneCompletionReconciler  # noqa: E402


class FakeLanesRunner:
    """Records every `lanes.sh --json <session>` invocation and answers from
    an in-memory session map: `{"session": [{"window": 2, "state": "free",
    "idle_seconds": 400}, ...]}`. A session absent from `windows` raises,
    simulating `lanes.sh` itself failing (missing session, tmux unreachable).
    """

    def __init__(self, windows):
        self.windows = windows
        self.calls = []

    def __call__(self, command):
        self.calls.append(command)
        session = command[-1]
        rows = self.windows.get(session)
        if rows is None:
            raise RuntimeError(f"lanes.sh unavailable for session {session}")
        return json.dumps(rows)


class LaneCompletionReconcilerTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.ledger = Ledger(Path(self.tempdir.name), clock=lambda: 1_000)

    def dispatch(self, task_id, *, lane="agent-supervisor:2"):
        """Records one dispatch the way `cli.py record_dispatch` does. Ends
        with `tasks.status == 'delivered'` and no `completed_at` -- exactly
        the shape #155 measured: a worker finished and never signalled."""
        return self.ledger.record_dispatch(
            lane=lane, pane_id="%2", nonce="nonce-2", harness="claude",
            repo="/repo/agent-supervisor", server_id="server-a", session_id="$2", command="claude",
            task_id=task_id, source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/155",
            source_ref="155", summary="issue #155", source_state="OPEN",
            evidence=[f"claimed by dispatch.sh for lane {lane}", "issues: 155"],
            status_marker=None,
        )

    def test_lane_free_past_the_dwell_is_completed_from_observed_state(self):
        """The #155 acceptance case: a lane never ran `wait-for -S`. The pane
        reads `free` and has for longer than the dwell -- the sweep must
        complete the task from that observed fact alone, freeing the lane,
        with no announcement from the worker at all."""
        self.dispatch("as155-observed-completion")
        runner = FakeLanesRunner(
            {"agent-supervisor": [{"window": 2, "state": "free", "idle_seconds": 400}]}
        )

        report = LaneCompletionReconciler(self.ledger, runner=runner, idle_after=300).sweep()

        task = self.ledger.get_task("as155-observed-completion")
        self.assertEqual("complete", task["status"])
        self.assertEqual(["as155-observed-completion"], report["completed"])
        self.assertEqual([], report["unresolved"])
        self.assertEqual([], report["errors"])
        self.assertEqual(1, len(runner.calls))  # one call per SESSION, not per task

    def test_idempotent_second_sweep_completes_nothing_new(self):
        self.dispatch("as155-observed-completion")
        runner = FakeLanesRunner(
            {"agent-supervisor": [{"window": 2, "state": "free", "idle_seconds": 400}]}
        )
        reconciler = LaneCompletionReconciler(self.ledger, runner=runner, idle_after=300)

        first = reconciler.sweep()
        second = reconciler.sweep()

        self.assertEqual(["as155-observed-completion"], first["completed"])
        self.assertEqual([], second["completed"])
        self.assertEqual([], second["unresolved"])  # task is complete, no longer "delivered open" at all

    def test_pane_busy_is_left_untouched(self):
        """A lane still mid-turn must not be completed just because it was
        once dispatched -- `lanes.sh` says busy, this sweep must agree."""
        self.dispatch("as155-observed-completion")
        runner = FakeLanesRunner(
            {"agent-supervisor": [{"window": 2, "state": "busy", "idle_seconds": 0}]}
        )

        report = LaneCompletionReconciler(self.ledger, runner=runner, idle_after=300).sweep()

        task = self.ledger.get_task("as155-observed-completion")
        self.assertEqual("delivered", task["status"])
        self.assertEqual([], report["completed"])
        self.assertIn("as155-observed-completion", report["unresolved"])

    def test_idle_seconds_exactly_at_the_threshold_is_completed(self):
        """Boundary case, matching digest.sh's own `>= $threshold` (not `>`):
        a pane free for EXACTLY idle_after seconds counts."""
        self.dispatch("as155-observed-completion")
        runner = FakeLanesRunner(
            {"agent-supervisor": [{"window": 2, "state": "free", "idle_seconds": 300}]}
        )

        report = LaneCompletionReconciler(self.ledger, runner=runner, idle_after=300).sweep()

        task = self.ledger.get_task("as155-observed-completion")
        self.assertEqual("complete", task["status"])
        self.assertEqual(["as155-observed-completion"], report["completed"])

    def test_pane_free_but_not_yet_past_the_dwell_is_left_untouched(self):
        """lane-done.sh's own header names this exact danger: idle also means
        "between tool calls". A pane that has only just gone free must not be
        completed -- only one that has stayed free past the dwell."""
        self.dispatch("as155-observed-completion")
        runner = FakeLanesRunner(
            {"agent-supervisor": [{"window": 2, "state": "free", "idle_seconds": 5}]}
        )

        report = LaneCompletionReconciler(self.ledger, runner=runner, idle_after=300).sweep()

        task = self.ledger.get_task("as155-observed-completion")
        self.assertEqual("delivered", task["status"])
        self.assertIn("as155-observed-completion", report["unresolved"])

    def test_blocked_pane_holding_an_unposted_verdict_is_left_untouched(self):
        """The exact case #102 nearly destroyed: a lane blocked on an
        approval prompt reads idle in a bare capture but is NOT `free` in
        `lanes.sh`'s classification -- must never be swept."""
        self.dispatch("as155-observed-completion")
        runner = FakeLanesRunner(
            {"agent-supervisor": [{"window": 2, "state": "menu-blocked", "idle_seconds": 900}]}
        )

        report = LaneCompletionReconciler(self.ledger, runner=runner, idle_after=300).sweep()

        task = self.ledger.get_task("as155-observed-completion")
        self.assertEqual("delivered", task["status"])
        self.assertIn("as155-observed-completion", report["unresolved"])

    def test_lanes_sh_failure_for_one_session_leaves_its_rows_untouched(self):
        """A transient `lanes.sh`/tmux failure must not be read as evidence
        of anything -- reported as an error, task left exactly as it was."""
        self.dispatch("as155-observed-completion")
        runner = FakeLanesRunner({})  # every session call raises

        report = LaneCompletionReconciler(self.ledger, runner=runner, idle_after=300).sweep()

        task = self.ledger.get_task("as155-observed-completion")
        self.assertEqual("delivered", task["status"])
        self.assertIn("as155-observed-completion", report["unresolved"])
        self.assertEqual(1, len(report["errors"]))
        self.assertEqual("agent-supervisor", report["errors"][0]["session"])

    def test_lane_missing_from_the_session_answer_is_left_unresolved(self):
        """The window this task's lane names is not in `lanes.sh`'s answer at
        all (closed, renamed, never existed) -- unknown, not guessed free."""
        self.dispatch("as155-observed-completion")
        runner = FakeLanesRunner({"agent-supervisor": []})

        report = LaneCompletionReconciler(self.ledger, runner=runner, idle_after=300).sweep()

        task = self.ledger.get_task("as155-observed-completion")
        self.assertEqual("delivered", task["status"])
        self.assertIn("as155-observed-completion", report["unresolved"])

    def test_unparseable_lane_id_is_left_unresolved_not_guessed(self):
        """A lane id that was never minted as `<session>:<index>` (a stamp
        typed by hand, a pre-migration shape) must not be resolved to a
        session at all -- fail-closed, same posture
        `reconcile_sources.py` takes for an unparseable `source_url`."""
        self.ledger.register_lane(
            lane="Review-Lane", pane_id="%9", nonce="nonce-9", harness="claude",
            repo="/repo/x", server_id="server-a", session_id="$9", command="claude",
        )
        self.dispatch("as155-mystery-lane", lane="Review-Lane")
        runner = FakeLanesRunner({})

        report = LaneCompletionReconciler(self.ledger, runner=runner, idle_after=300).sweep()

        task = self.ledger.get_task("as155-mystery-lane")
        self.assertEqual("delivered", task["status"])
        self.assertIn("as155-mystery-lane", report["unresolved"])
        self.assertEqual(0, len(runner.calls))  # never even tried to call lanes.sh for it

    def test_batches_one_call_per_session_not_per_task(self):
        """Two delivered tasks in the same session must cost ONE `lanes.sh
        --json` call, not two -- the same batching argument
        `reconcile_sources.py` makes for `gh`."""
        self.ledger.register_lane(
            lane="agent-supervisor:3", pane_id="%3", nonce="nonce-3", harness="claude",
            repo="/repo/agent-supervisor", server_id="server-a", session_id="$3", command="claude",
        )
        self.dispatch("as155-task-a", lane="agent-supervisor:2")
        self.dispatch("as155-task-b", lane="agent-supervisor:3")
        runner = FakeLanesRunner(
            {
                "agent-supervisor": [
                    {"window": 2, "state": "free", "idle_seconds": 400},
                    {"window": 3, "state": "free", "idle_seconds": 400},
                ]
            }
        )

        report = LaneCompletionReconciler(self.ledger, runner=runner, idle_after=300).sweep()

        self.assertEqual(sorted(["as155-task-a", "as155-task-b"]), sorted(report["completed"]))
        self.assertEqual(1, len(runner.calls))


if __name__ == "__main__":
    unittest.main()
