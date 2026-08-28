import hashlib
import json
import subprocess
import sys
import tempfile
import time
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


class FakeReviewerGhRunner:
    """agent-supervisor#774 half B: answers `lanes.sh --json <session>`
    (always raising -- the stuck shape this test class exercises is a
    session that no longer exists at all, e.g. the pre-#739
    `agent-supervisor:N` session) and `gh pr view <number> ...` calls from
    an in-memory per-PR-number map, so `_reviewer_task_merge_evidence`'s two
    real `gh` reads (the verdict source's reviews/comments read, and this
    sweep's own merge-state read) can be tested without a live GitHub call.
    """

    def __init__(self, prs):
        # prs: {number: {"reviews": [...], "comments": [...], "commits": [...], "state": "MERGED"|"OPEN"}}
        self.prs = prs
        self.calls = []

    def __call__(self, command):
        self.calls.append(command)
        if command[0] != "gh":
            raise RuntimeError(f"lanes.sh unavailable for session {command[-1]}")
        number = command[3]
        pr = self.prs.get(number)
        if pr is None:
            raise RuntimeError(f"no such PR {number}")
        json_fields = command[-1]
        if json_fields == "reviews,comments,author,commits":
            return json.dumps(
                {
                    "reviews": pr.get("reviews", []),
                    "comments": pr.get("comments", []),
                    "author": pr.get("author", {}),
                    "commits": pr.get("commits", []),
                }
            )
        if json_fields == "state,url":
            return json.dumps(
                {
                    "state": pr.get("state", "OPEN"),
                    "url": pr.get("url", f"https://github.com/jonhill90/agent-estate/pull/{number}"),
                }
            )
        raise RuntimeError(f"unexpected gh --json fields: {json_fields}")


class LaneCompletionReconcilerTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.ledger = Ledger(Path(self.tempdir.name), clock=lambda: 1_000)

    def dispatch(self, task_id, *, lane="agent-supervisor:2", accepted=True):
        """Records one dispatch the way `cli.py record_dispatch` does. Ends
        with `tasks.status == 'delivered'` and no `completed_at` -- exactly
        the shape #155 measured: a worker finished and never signalled.

        `accepted=True` by default (agent-supervisor#193): in the real,
        fixed pipeline `record_dispatch` sets `accepted_at` itself the
        moment its own send is confirmed to have landed (see that method's
        docstring) -- every test in this file except the #193 regression
        below is exercising the sweep's OTHER behaviour (pane state, dwell,
        batching) and should look exactly like a normal, accepted dispatch.
        `accepted=False` reproduces the #155 scenario as it was measured
        BEFORE this fix -- see `test_never_accepted_...` below, which is the
        one place that matters."""
        return self.ledger.record_dispatch(
            lane=lane, pane_id="%2", nonce="nonce-2", harness="claude",
            repo="/repo/agent-supervisor", server_id="server-a", session_id="$2", command="claude",
            task_id=task_id, source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/155",
            source_ref="155", summary="issue #155", source_state="OPEN",
            evidence=[f"claimed by dispatch.sh for lane {lane}", "issues: 155"],
            status_marker=None,
            accepted=accepted,
        )

    def dispatch_claude_print(self, task_id, *, lane):
        """Records one dispatch the way `ClaudePrintAdapter.assign_task`
        actually does -- `register_lane`, `reconstruct_task`, `assign`,
        `mark_delivery_pending`, `mark_delivered` as four independent calls,
        never `record_dispatch` (agent-supervisor#488: `record_dispatch`
        never forwards its own `transport` kwarg to `_register_lane_tx`, so
        routing a claude-print scenario through it silently reverts the
        lane's `transport` column back to `send-keys` -- harmless before
        this fix, since nothing read that column, but it would now defeat
        the very gate these tests exist to exercise. `record_dispatch` is
        only ever reachable from `cli.py record-dispatch`, which has no
        `--transport` flag at all -- this path is never how a real
        claude-print lane is dispatched, so faithfully reproducing THAT
        flow, not patching `record_dispatch`, is the right fix here)."""
        nonce = f"nonce-{lane}"
        self.ledger.register_lane(
            lane=lane, pane_id=f"claude-print:{lane}", nonce=nonce, harness="claude",
            repo="/repo/x", server_id="claude-print", session_id=f"sess-{lane}",
            command="claude", transport="claude-print",
        )
        self.ledger.reconstruct_task(
            task_id=task_id, source_kind="issue",
            source_url=f"https://github.com/jonhill90/agent-supervisor/issues/{task_id}",
            source_ref=task_id, summary=f"issue {task_id}", source_state="OPEN",
            status="created", evidence=[f"claimed by dispatch.sh for lane {lane}"], status_marker=None,
        )
        self.ledger.assign(task_id=task_id, lane=lane, pane_nonce=nonce, summary=f"issue {task_id}")
        self.ledger.mark_delivery_pending(task_id, pane_nonce=nonce)
        return self.ledger.mark_delivered(task_id, pane_nonce=nonce)

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

    def test_sweep_adopts_an_orphaned_result_file_with_no_recorded_hash(self):
        """agent-supervisor#623: this sweep exists precisely to rescue a
        lane that finished without signalling (#155), but it could not
        rescue THIS shape of that same failure -- a lane whose real result
        file already sits on disk (a prior `complete()` wrote the file and
        crashed before the row update that follows a write) with no row
        that ever recorded a hash for it. `_complete_observed` writes its
        own fabricated note text, which will not match the orphaned file's
        bytes -- it must adopt the file already there rather than die on
        the immutability guard, the exact `as531-rev620b` specimen the
        issue measured live."""
        self.dispatch("as623-orphaned")
        orphaned = b"# Result\n\nThe lane's real, already-written report.\n"
        (self.ledger.results_dir / "as623-orphaned.md").write_bytes(orphaned)
        runner = FakeLanesRunner(
            {"agent-supervisor": [{"window": 2, "state": "free", "idle_seconds": 400}]}
        )

        report = LaneCompletionReconciler(self.ledger, runner=runner, idle_after=300).sweep()

        self.assertEqual(["as623-orphaned"], report["completed"])
        self.assertEqual([], report["errors"])
        task = self.ledger.get_task("as623-orphaned")
        self.assertEqual("complete", task["status"])
        self.assertEqual(hashlib.sha256(orphaned).hexdigest(), task["result_sha256"])
        # The orphaned file itself was never touched.
        self.assertEqual(orphaned, Path(task["result_path"]).read_bytes())

    def test_sweep_still_refuses_a_genuine_overwrite_once_a_hash_is_recorded(self):
        """The direction the immutability guard exists for must survive
        through THIS caller too, not only `record-completion` -- both hit
        `Ledger.complete` and both must keep refusing a real conflict.
        Calls `_complete_observed` directly (mirroring `test_mutation_
        ignoring_accepted_at_...` below) so the second, genuinely
        conflicting write is forced rather than short-circuited by the
        row already reading `complete`."""
        self.dispatch("as623-recorded")
        task = self.ledger.get_task("as623-recorded")
        reconciler = LaneCompletionReconciler(self.ledger, runner=FakeLanesRunner({}), idle_after=300)
        report = {"completed": [], "errors": [], "unresolved": []}
        reconciler._complete_observed(task, session="agent-supervisor", idle_seconds=400, now=1_400, report=report)
        self.assertEqual(["as623-recorded"], report["completed"])
        recorded = self.ledger.get_task("as623-recorded")

        # A later, genuinely different note -- the same row, re-read fresh --
        # must still be refused, not silently adopted over the recorded hash.
        report2 = {"completed": [], "errors": [], "unresolved": []}
        reconciler._complete_observed(
            recorded, session="agent-supervisor", idle_seconds=999, now=9_999, report=report2
        )
        self.assertEqual([], report2["completed"])
        self.assertEqual(1, len(report2["errors"]))
        self.assertIn("immutable result", report2["errors"][0]["error"])
        reloaded = self.ledger.get_task("as623-recorded")
        self.assertEqual(recorded["result_sha256"], reloaded["result_sha256"])

    def test_never_accepted_task_is_failed_not_completed(self):
        """agent-supervisor#193's own regression: `at25-rev33` was dispatched
        to `agent-tui:3`, its `/clear` Enter never submitted, the retyped
        brief landed glued onto it as noise the harness discarded as an
        unknown command, and the lane went quiet exactly like a finished
        one. `accepted_at` was never set -- nothing about that pane ever
        confirmed the brief began. The sweep must not certify it `complete`
        just because the pane reads free past the dwell; the ledger must
        record what is actually known instead: `failed`."""
        self.dispatch("as193-never-accepted", accepted=False)
        runner = FakeLanesRunner(
            {"agent-supervisor": [{"window": 2, "state": "free", "idle_seconds": 400}]}
        )

        report = LaneCompletionReconciler(self.ledger, runner=runner, idle_after=300).sweep()

        task = self.ledger.get_task("as193-never-accepted")
        self.assertEqual("failed", task["status"])
        self.assertNotEqual("complete", task["status"])
        self.assertEqual([], report["completed"])
        self.assertEqual(["as193-never-accepted"], report["failed_unaccepted"])
        self.assertEqual([], report["unresolved"])
        self.assertEqual([], report["errors"])
        # And it is loud about WHY, not just terminal (#118's lesson) --
        # readable from the ledger's own result, the same evidence
        # `_complete_observed`'s note gives a normal completion.
        result_bytes = Path(task["result_path"]).read_bytes()
        self.assertIn(b"accepted_at", result_bytes)

    def test_never_accepted_frees_the_lane_for_a_fresh_dispatch(self):
        """`failed` is terminal (`one_open_task_per_lane` excludes it) --
        unlike leaving the task stuck `delivered` forever, a lane whose
        dispatch was never accepted must be dispatchable again."""
        self.dispatch("as193-never-accepted", accepted=False)
        runner = FakeLanesRunner(
            {"agent-supervisor": [{"window": 2, "state": "free", "idle_seconds": 400}]}
        )

        LaneCompletionReconciler(self.ledger, runner=runner, idle_after=300).sweep()

        # A second record_dispatch to the SAME lane must not collide with
        # the `one_open_task_per_lane` unique index -- it would if the
        # failed task were still open.
        self.dispatch("as193-redispatch", accepted=False)
        task = self.ledger.get_task("as193-redispatch")
        self.assertEqual("delivered", task["status"])

    def test_mutation_ignoring_accepted_at_completes_the_never_accepted_task(self):
        """Mutation check: if the sweep's `accepted_at is None` gate is
        removed, THIS test's own scenario (identical to
        `test_never_accepted_task_is_failed_not_completed`) goes back to
        `complete` -- proving that gate, not something else, is what makes
        the test above pass. Exercised here by calling the pre-#193 code
        path directly (`_complete_observed`), the same way the real sweep
        used to for every free-and-idle-enough task regardless of
        acceptance."""
        self.dispatch("as193-mutant", accepted=False)
        reconciler = LaneCompletionReconciler(
            self.ledger,
            runner=FakeLanesRunner({"agent-supervisor": [{"window": 2, "state": "free", "idle_seconds": 400}]}),
            idle_after=300,
        )
        task = self.ledger.get_task("as193-mutant")
        report = {"completed": [], "failed_unaccepted": [], "unresolved": [], "errors": []}
        reconciler._complete_observed(task, session="agent-supervisor", idle_seconds=400, now=1_400, report=report)
        task = self.ledger.get_task("as193-mutant")
        self.assertEqual(
            "complete", task["status"],
            "MUTATION CONFIRMED (red): without the accepted_at gate, "
            "_complete_observed alone still completes a never-accepted task",
        )

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

    def dispatch_review(self, task_id, *, lane, pr_number, repo="jonhill90/agent-estate"):
        """Records one REVIEW dispatch (`dispatch.sh --reviews-pr`) the way
        `cli_dispatch_record.py` actually shapes it: `source_kind='pull'`,
        `is_review=1`, `source_url` naming the PR being reviewed -- the
        exact `ae680-ae680-rev765`/`ae767-ae767-rev769`/`ae761-ae761-rev770`
        shape (agent-supervisor#774 half B), never the issue-scoped shape
        `dispatch()` above uses."""
        return self.ledger.record_dispatch(
            lane=lane, pane_id="%9", nonce=f"nonce-{task_id}", harness="claude",
            repo="/repo/agent-estate", server_id="server-a", session_id="$9", command="claude",
            task_id=task_id, source_kind="pull",
            source_url=f"https://github.com/{repo}/pull/{pr_number}",
            source_ref=str(pr_number), summary=f"reviews PR #{pr_number}", source_state="OPEN",
            evidence=[f"claimed by dispatch.sh for lane {lane}", f"pr: {pr_number}"],
            status_marker=None, is_review=1,
        )

    def test_stuck_reviewer_task_with_merged_pr_and_matching_verdict_completes(self):
        """agent-supervisor#774 half B: the exact live shape --
        `ae680-ae680-rev765`/`ae767-ae767-rev769`/`ae761-ae761-rev770` --
        a review task whose lane names a tmux session gone after #739's
        rename, so `lanes.sh --json` for it raises outright. Real evidence
        (this lane's own posted Verdict: APPROVE, and the PR it names
        confirmed MERGED) must complete the task instead of leaving it
        `unresolved` forever."""
        self.dispatch_review("ae680-ae680-rev765", lane="agent-supervisor:2", pr_number=765)
        verdict_comment = {
            "body": "Verdict: APPROVE\nReview-Lane: agent-supervisor:2\nReviewed-SHA: " + "a" * 40,
            "author": {"login": "jonhill90"},
            "createdAt": "2026-08-28T15:00:00Z",
        }
        runner = FakeReviewerGhRunner(
            {"765": {"comments": [verdict_comment], "state": "MERGED", "url": "https://github.com/jonhill90/agent-estate/pull/765"}}
        )

        report = LaneCompletionReconciler(self.ledger, runner=runner, idle_after=300).sweep()

        task = self.ledger.get_task("ae680-ae680-rev765")
        self.assertEqual("complete", task["status"])
        self.assertEqual(["ae680-ae680-rev765"], report["completed"])
        self.assertEqual(["ae680-ae680-rev765"], report["completed_from_evidence"])
        self.assertEqual([], report["unresolved"])
        result_bytes = Path(task["result_path"]).read_bytes()
        self.assertIn(b"pull/765", result_bytes)
        self.assertIn(b"agent-supervisor:2", result_bytes)

    def test_reviewer_task_with_no_verdict_comment_and_open_pr_stays_unresolved(self):
        """Mutation, direction 1: the PR this task reviewed is still OPEN
        and carries no verdict comment at all -- the synthetic
        no-merge-evidence specimen the brief requires. Must NOT be
        completed; must fall through to the ordinary session-error
        `unresolved` outcome, same as before this fix existed."""
        self.dispatch_review("ae999-ae999-rev999", lane="agent-supervisor:5", pr_number=999)
        runner = FakeReviewerGhRunner({"999": {"comments": [], "state": "OPEN"}})

        report = LaneCompletionReconciler(self.ledger, runner=runner, idle_after=300).sweep()

        task = self.ledger.get_task("ae999-ae999-rev999")
        self.assertEqual("delivered", task["status"])
        self.assertEqual([], report["completed"])
        self.assertIn("ae999-ae999-rev999", report["unresolved"])
        self.assertEqual(1, len(report["errors"]))

    def test_reviewer_task_with_approved_verdict_but_pr_still_open_stays_unresolved(self):
        """Mutation, direction 2: a real, matching Verdict: APPROVE exists,
        but the PR it names has not actually merged yet -- an APPROVE is
        not the same fact as a merge, and this path must check both
        independently rather than inferring one from the other."""
        self.dispatch_review("ae998-ae998-rev998", lane="agent-supervisor:6", pr_number=998)
        verdict_comment = {
            "body": "Verdict: APPROVE\nReview-Lane: agent-supervisor:6\nReviewed-SHA: " + "b" * 40,
            "author": {"login": "jonhill90"},
            "createdAt": "2026-08-28T15:00:00Z",
        }
        runner = FakeReviewerGhRunner({"998": {"comments": [verdict_comment], "state": "OPEN"}})

        report = LaneCompletionReconciler(self.ledger, runner=runner, idle_after=300).sweep()

        task = self.ledger.get_task("ae998-ae998-rev998")
        self.assertEqual("delivered", task["status"])
        self.assertEqual([], report["completed"])
        self.assertIn("ae998-ae998-rev998", report["unresolved"])

    def test_reviewer_task_with_verdict_from_a_different_lane_stays_unresolved(self):
        """Mutation, direction 3: the PR is merged and carries a real
        Verdict: APPROVE, but it was posted under a DIFFERENT lane's
        Review-Lane: trailer -- someone else reviewed it, or reviewed it
        under a name this task does not own. Must not be read as evidence
        this TASK's own work happened."""
        self.dispatch_review("ae997-ae997-rev997", lane="agent-supervisor:7", pr_number=997)
        verdict_comment = {
            "body": "Verdict: APPROVE\nReview-Lane: agent-supervisor:9\nReviewed-SHA: " + "c" * 40,
            "author": {"login": "jonhill90"},
            "createdAt": "2026-08-28T15:00:00Z",
        }
        runner = FakeReviewerGhRunner(
            {"997": {"comments": [verdict_comment], "state": "MERGED", "url": "https://github.com/jonhill90/agent-estate/pull/997"}}
        )

        report = LaneCompletionReconciler(self.ledger, runner=runner, idle_after=300).sweep()

        task = self.ledger.get_task("ae997-ae997-rev997")
        self.assertEqual("delivered", task["status"])
        self.assertEqual([], report["completed"])
        self.assertIn("ae997-ae997-rev997", report["unresolved"])

    def test_issue_scoped_task_with_dead_session_is_never_treated_as_a_reviewer_task(self):
        """An ordinary AUTHOR (issue-scoped) task stuck behind the same
        dead-session shape must not be routed through the reviewer-evidence
        path at all -- `source_kind='issue'` (or no `is_review`) short-
        circuits `_reviewer_task_merge_evidence` to `None` before any `gh`
        call is even attempted."""
        self.dispatch("as155-observed-completion", lane="agent-supervisor:8")
        runner = FakeReviewerGhRunner({})

        report = LaneCompletionReconciler(self.ledger, runner=runner, idle_after=300).sweep()

        task = self.ledger.get_task("as155-observed-completion")
        self.assertEqual("delivered", task["status"])
        self.assertIn("as155-observed-completion", report["unresolved"])
        # one failed `lanes.sh --json` attempt, and nothing else -- no `gh`
        # call was ever made for a non-review task.
        gh_calls = [call for call in runner.calls if call[0] == "gh"]
        self.assertEqual(0, len(gh_calls))

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

    def test_nonobservable_lane_past_stale_after_is_failed(self):
        """agent-supervisor#374: a claude-print/pi-rpc lane id never parses
        as `<session>:<index>` -- there is no pane for `lanes.sh` to ever
        answer for. Past `stale_after` this must resolve to `failed`
        (never `complete` -- there is no positive observation backing
        success), and `lanes.sh` must never even be invoked for it."""
        self.ledger.register_lane(
            lane="ad374-headless", pane_id="claude-print:ad374-headless", nonce="nonce-h",
            harness="claude", repo="/repo/x", server_id="claude-print", session_id="sess-h",
            command="claude", transport="claude-print",
        )
        self.dispatch("ad374-headless-task", lane="ad374-headless", accepted=False)
        runner = FakeLanesRunner({})

        report = LaneCompletionReconciler(
            self.ledger, runner=runner, stale_after=300, clock=lambda: 2_000
        ).sweep()

        task = self.ledger.get_task("ad374-headless-task")
        self.assertEqual("failed", task["status"])
        self.assertEqual(["ad374-headless-task"], report["failed_stale_delivery"])
        self.assertEqual(0, len(runner.calls))

    def test_nonobservable_lane_before_stale_after_is_left_unresolved(self):
        """A headless lane younger than `stale_after` may still be the one
        live turn about to call `complete` itself -- must not be touched."""
        self.ledger.register_lane(
            lane="ad374-fresh", pane_id="claude-print:ad374-fresh", nonce="nonce-f",
            harness="claude", repo="/repo/x", server_id="claude-print", session_id="sess-f",
            command="claude", transport="claude-print",
        )
        self.dispatch("ad374-fresh-task", lane="ad374-fresh")
        runner = FakeLanesRunner({})

        report = LaneCompletionReconciler(
            self.ledger, runner=runner, stale_after=300, clock=lambda: 1_100
        ).sweep()

        task = self.ledger.get_task("ad374-fresh-task")
        self.assertEqual("delivered", task["status"])
        self.assertIn("ad374-fresh-task", report["unresolved"])

    def test_nonobservable_lane_with_accepted_at_still_fails_not_completes(self):
        """Unlike `fail_unaccepted`, `accepted_at` being set does not divert
        this path to `complete()` -- there is still no positive observation
        of success for a transport with no pane, only silence."""
        self.ledger.register_lane(
            lane="ad374-accepted", pane_id="claude-print:ad374-accepted", nonce="nonce-a",
            harness="claude", repo="/repo/x", server_id="claude-print", session_id="sess-a",
            command="claude", transport="claude-print",
        )
        self.dispatch("ad374-accepted-task", lane="ad374-accepted", accepted=True)
        runner = FakeLanesRunner({})

        report = LaneCompletionReconciler(
            self.ledger, runner=runner, stale_after=300, clock=lambda: 2_000
        ).sweep()

        task = self.ledger.get_task("ad374-accepted-task")
        self.assertEqual("failed", task["status"])
        self.assertEqual(["ad374-accepted-task"], report["failed_stale_delivery"])

    def test_accepted_nonobservable_lane_past_stale_after_is_failed(self):
        """agent-supervisor#414: a claude-print/pi-rpc worker that calls
        `cli.py accept` moves its own row to status='accepted' -- outside
        `list_delivered_open_tasks`, which #374's fix (above) is built on.
        Reproduces the fix's target directly through `Ledger.accept`, the
        only real caller, exactly as `cli.py accept` invokes it -- not
        through `record_dispatch(accepted=True)`, which never changes
        `status` at all (see the #374 tests above) and so cannot exercise
        this gap."""
        self.ledger.register_lane(
            lane="ad414-headless", pane_id="claude-print:ad414-headless", nonce="nonce-414",
            harness="claude", repo="/repo/x", server_id="claude-print", session_id="sess-414",
            command="claude", transport="claude-print",
        )
        self.dispatch("ad414-headless-task", lane="ad414-headless", accepted=False)
        self.ledger.accept("ad414-headless-task", pane_nonce="nonce-2")
        self.assertEqual("accepted", self.ledger.get_task("ad414-headless-task")["status"])
        runner = FakeLanesRunner({})

        report = LaneCompletionReconciler(
            self.ledger, runner=runner, stale_after=300, clock=lambda: 2_000
        ).sweep()

        task = self.ledger.get_task("ad414-headless-task")
        self.assertEqual("failed", task["status"])
        self.assertEqual(["ad414-headless-task"], report["failed_stale_acceptance"])
        self.assertEqual(0, len(runner.calls))

    def test_accepted_nonobservable_lane_with_pr_evidence_completes_instead_of_failing(self):
        """agent-supervisor#401, found late (review of the #401 fix itself):
        `_sweep_nonobservable_accepted` had nothing but silence to go on,
        same as `_sweep_nonobservable`, but never checked the lane-log
        before this fix -- a claude-print/pi-rpc lane that reached
        `accept()`, shipped a PR that MERGED (named in its own lane-log),
        then went quiet, was stamped "failed, not completed" with zero
        evidence check. Must complete from that evidence instead, exactly
        like the pre-acceptance path already does."""
        self.ledger.register_lane(
            lane="ad401-accepted-headless", pane_id="claude-print:ad401-accepted-headless",
            nonce="nonce-414e", harness="claude", repo="/repo/x", server_id="claude-print",
            session_id="sess-414e", command="claude", transport="claude-print",
        )
        self.dispatch("ad401-accepted-headless-task", lane="ad401-accepted-headless", accepted=False)
        self.ledger.accept("ad401-accepted-headless-task", pane_nonce="nonce-2")
        self._write_lane_log(
            "ad401-accepted-headless-task",
            "opened https://github.com/jonhill90/agent-supervisor/pull/9999 -- MERGED\n",
        )
        runner = FakeLanesRunner({})

        report = LaneCompletionReconciler(
            self.ledger, runner=runner, stale_after=300, clock=lambda: 2_000
        ).sweep()

        task = self.ledger.get_task("ad401-accepted-headless-task")
        self.assertEqual("complete", task["status"])
        self.assertEqual([], report["failed_stale_acceptance"])
        self.assertEqual(["ad401-accepted-headless-task"], report["completed"])
        self.assertEqual(["ad401-accepted-headless-task"], report["completed_from_evidence"])
        result_bytes = Path(task["result_path"]).read_bytes()
        self.assertIn(b"pull/9999", result_bytes)
        self.assertEqual(
            self.ledger.results_dir / "ad401-accepted-headless-task.md", Path(task["result_path"])
        )

    def test_accepted_nonobservable_lane_without_evidence_still_fails(self):
        """A fix that stops this path stamping anything at all is a
        regression that looks exactly like success -- a genuinely dead
        accepted lane (agent-supervisor#488: its lane-log names a pid that
        has actually exited, the same evidence `pid_is_alive` checks) with
        no PR evidence must still be terminated `failed`, and the note must
        not claim the canonical `<task_id>.md` slot a late genuine report
        might still need."""
        self.dispatch_claude_print("ad401-accepted-dead-task", lane="ad401-accepted-dead")
        self.ledger.accept("ad401-accepted-dead-task", pane_nonce="nonce-ad401-accepted-dead")
        self._write_dispatched_pid_log("ad401-accepted-dead-task", self._dead_pid(), lane="ad401-accepted-dead")
        runner = FakeLanesRunner({})

        report = LaneCompletionReconciler(
            self.ledger, runner=runner, stale_after=300, clock=lambda: 2_000
        ).sweep()

        task = self.ledger.get_task("ad401-accepted-dead-task")
        self.assertEqual("failed", task["status"])
        self.assertEqual(["ad401-accepted-dead-task"], report["failed_stale_acceptance"])
        result_path = Path(task["result_path"])
        self.assertEqual("ad401-accepted-dead-task.reconcile.md", result_path.name)
        self.assertFalse((self.ledger.results_dir / "ad401-accepted-dead-task.md").exists())
        note = result_path.read_bytes()
        self.assertNotIn(b"failed, not completed", note)
        # agent-supervisor#692: a confirmed-dead pid now fails IMMEDIATELY
        # via the died_without_completing fast path -- see
        # test_accepted_dead_pid_fails_immediately_via_died_without_completing
        # for that behaviour directly; this test's own job is unchanged (a
        # dead accepted lane with no PR evidence still fails).
        self.assertIn(b"died_without_completing", note)

    def test_accepted_nonobservable_lane_before_stale_after_is_left_unresolved(self):
        """An accepted headless lane younger than `stale_after` may still be
        the one live turn about to call `complete` itself -- must not be
        touched, same posture as the pre-acceptance case."""
        self.ledger.register_lane(
            lane="ad414-fresh", pane_id="claude-print:ad414-fresh", nonce="nonce-414f",
            harness="claude", repo="/repo/x", server_id="claude-print", session_id="sess-414f",
            command="claude", transport="claude-print",
        )
        self.dispatch("ad414-fresh-task", lane="ad414-fresh", accepted=False)
        self.ledger.accept("ad414-fresh-task", pane_nonce="nonce-2")
        runner = FakeLanesRunner({})

        report = LaneCompletionReconciler(
            self.ledger, runner=runner, stale_after=300, clock=lambda: 1_100
        ).sweep()

        task = self.ledger.get_task("ad414-fresh-task")
        self.assertEqual("accepted", task["status"])
        self.assertIn("ad414-fresh-task", report["unresolved"])

    def test_pre_fix_query_never_sees_the_accepted_row(self):
        """Mutation check, in the other direction from #374's own: proves
        the defect this fix targets was real by exercising the OLD query
        directly. If `list_delivered_open_tasks` -- what every sweep before
        this fix relied on exclusively -- ever starts returning an accepted
        row, this goes red and something has changed underneath the new
        query's own reasoning."""
        self.ledger.register_lane(
            lane="ad414-mutant", pane_id="claude-print:ad414-mutant", nonce="nonce-414m",
            harness="claude", repo="/repo/x", server_id="claude-print", session_id="sess-414m",
            command="claude", transport="claude-print",
        )
        self.dispatch("ad414-mutant-task", lane="ad414-mutant", accepted=False)
        self.ledger.accept("ad414-mutant-task", pane_nonce="nonce-2")

        delivered_ids = [t["id"] for t in self.ledger.list_delivered_open_tasks()]
        accepted_ids = [t["id"] for t in self.ledger.list_accepted_open_tasks()]
        self.assertNotIn("ad414-mutant-task", delivered_ids)
        self.assertIn("ad414-mutant-task", accepted_ids)

    def _write_lane_log(self, task_id, text):
        log_dir = self.ledger.root / "lane-logs"
        log_dir.mkdir(exist_ok=True)
        (log_dir / f"{task_id}.log").write_text(text)

    def test_nonobservable_lane_with_pr_evidence_completes_instead_of_failing(self):
        """agent-supervisor#401's own specimen (ad275-fix275): a claude-print
        lane never signals, but its lane-log already names the PR it opened.
        That is cheap evidence available at stamp time -- the sweep must
        complete the task from it rather than stamping the false verdict
        the issue measured 31 times."""
        self.ledger.register_lane(
            lane="ad401-headless", pane_id="claude-print:ad401-headless", nonce="nonce-h",
            harness="claude", repo="/repo/x", server_id="claude-print", session_id="sess-h",
            command="claude", transport="claude-print",
        )
        self.dispatch("ad401-headless-task", lane="ad401-headless", accepted=False)
        self._write_lane_log(
            "ad401-headless-task",
            'opened https://github.com/jonhill90/agent-dotfiles/pull/283 "done"\n',
        )
        runner = FakeLanesRunner({})

        report = LaneCompletionReconciler(
            self.ledger, runner=runner, stale_after=300, clock=lambda: 2_000
        ).sweep()

        task = self.ledger.get_task("ad401-headless-task")
        self.assertEqual("complete", task["status"])
        self.assertEqual([], report["failed_stale_delivery"])
        self.assertEqual(["ad401-headless-task"], report["completed"])
        self.assertEqual(["ad401-headless-task"], report["completed_from_evidence"])
        result_bytes = Path(task["result_path"]).read_bytes()
        self.assertIn(b"pull/283", result_bytes)
        # The canonical slot -- reserved for the lane's own report -- is
        # exactly where this evidence-backed completion lands.
        self.assertEqual(self.ledger.results_dir / "ad401-headless-task.md", Path(task["result_path"]))

    def test_nonobservable_lane_with_dead_pid_and_no_pr_still_fails(self):
        """agent-supervisor#488: a genuinely dead lane-logged pid, with no
        PR evidence either, must still be terminated `failed` -- the sweep
        stopping to stamp anything at all whenever it cannot prove aliveness
        would be exactly as wrong as stamping a live one, and this is the
        codepath that proves the fix does not overcorrect into that."""
        self.dispatch_claude_print("ad401-silent-task", lane="ad401-silent")
        self._write_dispatched_pid_log("ad401-silent-task", self._dead_pid(), lane="ad401-silent")
        runner = FakeLanesRunner({})

        report = LaneCompletionReconciler(
            self.ledger, runner=runner, stale_after=300, clock=lambda: 2_000
        ).sweep()

        task = self.ledger.get_task("ad401-silent-task")
        self.assertEqual("failed", task["status"])
        self.assertEqual(["ad401-silent-task"], report["failed_stale_delivery"])
        result_path = Path(task["result_path"])
        self.assertEqual("ad401-silent-task.reconcile.md", result_path.name)
        self.assertFalse((self.ledger.results_dir / "ad401-silent-task.md").exists())
        note = result_path.read_bytes()
        self.assertNotIn(b"failed, not completed", note)
        # agent-supervisor#692: a confirmed-dead pid now fails IMMEDIATELY
        # via the died_without_completing fast path rather than waiting out
        # stale_after -- see test_dead_pid_fails_immediately_without_waiting_
        # for_stale_after and its accepted-path sibling for that behaviour
        # directly; this test's own job is unchanged (a dead pid with no PR
        # evidence still fails, never completes).
        self.assertIn(b"died_without_completing", note)

    def test_nonobservable_lane_with_no_pid_evidence_is_indeterminate_not_failed(self):
        """agent-supervisor#488: no lane-log at all means this sweep has no
        way to check liveness, not that the work is confirmed gone -- the
        dangerous direction #488 measured (a LIVE `as473-as473x` stamped
        `failed`) was reached through exactly this kind of blind spot.
        Indeterminate liveness must leave the row alone AND say so in the
        report, distinctly from an ordinary too-young `unresolved`."""
        self.dispatch_claude_print("ad488-no-log-task", lane="ad488-no-log")
        runner = FakeLanesRunner({})

        report = LaneCompletionReconciler(
            self.ledger, runner=runner, stale_after=300, clock=lambda: 2_000
        ).sweep()

        task = self.ledger.get_task("ad488-no-log-task")
        self.assertEqual("delivered", task["status"])
        self.assertIsNone(task["completed_at"])
        self.assertEqual([], report["failed_stale_delivery"])
        self.assertIn("ad488-no-log-task", report["unresolved"])
        self.assertEqual(["ad488-no-log-task"], report["liveness_indeterminate"])

    def test_accepted_nonobservable_lane_with_no_pid_evidence_is_indeterminate_not_failed(self):
        """Same indeterminate-liveness gate as the delivered-path test above,
        exercised through `_sweep_nonobservable_accepted` -- the exact
        method the measured `as473-as473x` specimen went through."""
        self.dispatch_claude_print("ad488-accepted-no-log-task", lane="ad488-accepted-no-log")
        self.ledger.accept("ad488-accepted-no-log-task", pane_nonce="nonce-ad488-accepted-no-log")
        runner = FakeLanesRunner({})

        report = LaneCompletionReconciler(
            self.ledger, runner=runner, stale_after=300, clock=lambda: 2_000
        ).sweep()

        task = self.ledger.get_task("ad488-accepted-no-log-task")
        self.assertEqual("accepted", task["status"])
        self.assertEqual([], report["failed_stale_acceptance"])
        self.assertIn("ad488-accepted-no-log-task", report["unresolved"])
        self.assertEqual(["ad488-accepted-no-log-task"], report["liveness_indeterminate"])

    def test_accepted_nonobservable_lane_with_live_process_is_not_failed(self):
        """agent-supervisor#488's measured specimen, reproduced directly:
        `as473-as473x` read `status=accepted` (per #480's own measurement)
        and its lane-log named a pid that was still alive an hour past the
        sweep's `stale_after` threshold -- the sweep must not stamp it
        `failed`. Uses a real, still-running subprocess (VERIFY THE
        INSTRUMENT: this liveness check must actually see a live process,
        not just fail to see a dead one)."""
        self.dispatch_claude_print("ad488-accepted-live-task", lane="ad488-accepted-live")
        self.ledger.accept("ad488-accepted-live-task", pane_nonce="nonce-ad488-accepted-live")
        proc = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"])
        self.addCleanup(proc.kill)
        self.addCleanup(proc.wait)
        try:
            self._write_dispatched_pid_log(
                "ad488-accepted-live-task", proc.pid, lane="ad488-accepted-live"
            )
            runner = FakeLanesRunner({})

            report = LaneCompletionReconciler(
                self.ledger, runner=runner, stale_after=300, clock=lambda: 2_000
            ).sweep()

            task = self.ledger.get_task("ad488-accepted-live-task")
            self.assertEqual("accepted", task["status"])
            self.assertEqual([], report["failed_stale_acceptance"])
            self.assertIn("ad488-accepted-live-task", report["unresolved"])
            self.assertEqual(["ad488-accepted-live-task"], report["liveness_alive"])
        finally:
            proc.kill()
            proc.wait()

    def _dead_pid(self):
        """A real, definitely-finished process -- not a number picked for
        being unlikely to exist (same convention `test_core.py`'s
        `test_pid_liveness_reports_a_dead_child` uses)."""
        proc = subprocess.Popen([sys.executable, "-c", "pass"])
        proc.wait()
        return proc.pid

    def _write_dispatched_pid_log(self, task_id, pid, *, lane):
        """Mirrors the exact line `ClaudePrintAdapter.assign_task` appends
        to `lane-logs/<task_id>.log` at dispatch time."""
        self._write_lane_log(task_id, f"\n--- dispatched detached: task={task_id} lane={lane} pid={pid} ---\n")

    def test_dead_pid_fails_immediately_without_waiting_for_stale_after(self):
        """agent-supervisor#692: as679-lease-anchor/as680-lane-completion/
        at167-tui-leak all ended their own claude -p turn to wait on a
        callback nothing sends, then sat `delivered`/`accepted` for
        110-227 minutes before anything noticed. A confirmed-dead pid is a
        positive fact available the very first sweep tick after the
        process exits -- this must fail the task well before `stale_after`
        (here: seconds, not the default hour), and name it distinctly in
        `died_without_completing`."""
        self.dispatch_claude_print("ad692-dead-fast-task", lane="ad692-dead-fast")
        self._write_dispatched_pid_log("ad692-dead-fast-task", self._dead_pid(), lane="ad692-dead-fast")
        runner = FakeLanesRunner({})

        # clock is only 5s past dispatch -- nowhere near stale_after=3600
        # (the default), yet the pid is already confirmed dead.
        report = LaneCompletionReconciler(self.ledger, runner=runner, clock=lambda: 1_005).sweep()

        task = self.ledger.get_task("ad692-dead-fast-task")
        self.assertEqual("failed", task["status"])
        self.assertEqual(["ad692-dead-fast-task"], report["failed_stale_delivery"])
        self.assertEqual(["ad692-dead-fast-task"], report["died_without_completing"])
        result_path = Path(task["result_path"])
        note = result_path.read_bytes()
        self.assertIn(b"died_without_completing", note)

    def test_dead_pid_fast_path_still_prefers_pr_evidence(self):
        """A confirmed-dead pid does not skip the cheap-evidence check --
        if the lane's own log already names a PR it opened before dying,
        that still completes the task instead of failing it."""
        self.dispatch_claude_print("ad692-dead-with-pr-task", lane="ad692-dead-with-pr")
        self._write_dispatched_pid_log(
            "ad692-dead-with-pr-task", self._dead_pid(), lane="ad692-dead-with-pr"
        )
        log_path = self.ledger.root / "lane-logs" / "ad692-dead-with-pr-task.log"
        with open(log_path, "a") as handle:
            handle.write("opened https://github.com/jonhill90/agent-supervisor/pull/9002\n")
        runner = FakeLanesRunner({})

        report = LaneCompletionReconciler(self.ledger, runner=runner, clock=lambda: 1_005).sweep()

        task = self.ledger.get_task("ad692-dead-with-pr-task")
        self.assertEqual("complete", task["status"])
        self.assertEqual([], report["died_without_completing"])
        self.assertEqual(["ad692-dead-with-pr-task"], report["completed_from_evidence"])

    def test_live_pid_still_waits_out_stale_after_not_failed_early(self):
        """A live process must never be fast-failed just because this fast
        path exists -- mutation check in the other direction from the dead-
        pid test above: only a CONFIRMED dead pid takes the fast path."""
        self.dispatch_claude_print("ad692-live-fast-task", lane="ad692-live-fast")
        proc = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"])
        self.addCleanup(proc.kill)
        self.addCleanup(proc.wait)
        try:
            self._write_dispatched_pid_log("ad692-live-fast-task", proc.pid, lane="ad692-live-fast")
            runner = FakeLanesRunner({})

            report = LaneCompletionReconciler(self.ledger, runner=runner, clock=lambda: 1_005).sweep()

            task = self.ledger.get_task("ad692-live-fast-task")
            self.assertEqual("delivered", task["status"])
            self.assertEqual([], report["died_without_completing"])
            self.assertIn("ad692-live-fast-task", report["unresolved"])
        finally:
            proc.kill()
            proc.wait()

    def test_accepted_dead_pid_fails_immediately_via_died_without_completing(self):
        """Same fast path, exercised through `_sweep_nonobservable_accepted`
        -- the exact method `as679-lease-anchor`/`as680-lane-completion`
        (both reached `accepted` before dying) would route through."""
        self.dispatch_claude_print("ad692-accepted-dead-fast-task", lane="ad692-accepted-dead-fast")
        self.ledger.accept("ad692-accepted-dead-fast-task", pane_nonce="nonce-ad692-accepted-dead-fast")
        self._write_dispatched_pid_log(
            "ad692-accepted-dead-fast-task", self._dead_pid(), lane="ad692-accepted-dead-fast"
        )
        runner = FakeLanesRunner({})

        report = LaneCompletionReconciler(self.ledger, runner=runner, clock=lambda: 1_005).sweep()

        task = self.ledger.get_task("ad692-accepted-dead-fast-task")
        self.assertEqual("failed", task["status"])
        self.assertEqual(["ad692-accepted-dead-fast-task"], report["failed_stale_acceptance"])
        self.assertEqual(["ad692-accepted-dead-fast-task"], report["died_without_completing"])

    def test_never_accepted_lane_with_pr_evidence_completes_instead_of_failing(self):
        """Same cheap-evidence check on the tmux `_fail_unaccepted` path: a
        lane observed free with no `accepted_at` is not normally evidence of
        success, but a PR named in its lane-log is a stronger, independent
        fact and settles it regardless."""
        self.dispatch("as193-with-pr", accepted=False)
        self._write_lane_log(
            "as193-with-pr", "https://github.com/jonhill90/agent-supervisor/pull/9001\n"
        )
        runner = FakeLanesRunner(
            {"agent-supervisor": [{"window": 2, "state": "free", "idle_seconds": 400}]}
        )

        report = LaneCompletionReconciler(self.ledger, runner=runner, idle_after=300).sweep()

        task = self.ledger.get_task("as193-with-pr")
        self.assertEqual("complete", task["status"])
        self.assertEqual([], report["failed_unaccepted"])
        self.assertEqual(["as193-with-pr"], report["completed_from_evidence"])

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
