import hashlib
import sys
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from tests.supervisor.test_core_helpers import LedgerTestBase  # noqa: E402


class CompletionTest(LedgerTestBase):
    def test_completion_requires_the_owning_pane_incarnation(self):
        """Red: complete() takes no pane_nonce at all -- any caller who knows
        a task_id can complete it, regardless of which pane incarnation
        actually owns the lane the task is bound to."""
        self.assign()
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")
        self.ledger.accept("review-870", pane_nonce="nonce-22-a")

        # A different (impersonating) incarnation must not be able to complete
        # a task it does not own.
        with self.assertRaisesRegex(ValueError, "pane incarnation"):
            self.ledger.complete("review-870", b"# Result\n\nDone.\n", pane_nonce="some-other-nonce")

        completed = self.ledger.complete("review-870", b"# Result\n\nDone.\n", pane_nonce="nonce-22-a")
        self.assertEqual("complete", completed["status"])

    def test_completion_is_immutable_idempotent_and_event_is_exactly_once(self):
        self.assign()
        result = b"# Result\n\nNo findings.\n"
        completed = self.ledger.complete("review-870", result, pane_nonce="nonce-22-a")
        self.assertEqual("complete", completed["status"])
        self.assertEqual(hashlib.sha256(result).hexdigest(), completed["result_sha256"])

        repeated = self.ledger.complete("review-870", result, pane_nonce="nonce-22-a")
        self.assertEqual(completed["result_sha256"], repeated["result_sha256"])
        events = self.ledger.list_events(task_id="review-870", event_type="completion")
        self.assertEqual(["completion:review-870"], [event["key"] for event in events])
        with self.assertRaisesRegex(ValueError, "immutable result"):
            self.ledger.complete("review-870", b"different result\n", pane_nonce="nonce-22-a")

    def test_completion_adopts_an_orphaned_result_file_with_no_recorded_hash(self):
        """agent-supervisor#623: a prior `complete()` wrote the result file
        and then crashed before the row update that follows a write ever
        ran (the exact `after_result` failpoint `test_completion_
        reconciles_each_injected_crash_point` exercises below) -- the row
        is left with `result_path`/`result_sha256` both NULL while the
        lane's genuine result already sits on disk. A later completion
        attempt, even with DIFFERENT bytes (a fresh note text, not the
        original content -- record-completion's `--note` will not match
        word for word), must ADOPT the file already there rather than
        refuse it as an immutability conflict: the row has never recorded
        a hash of its own for the new bytes to conflict with."""
        self.assign()
        orphaned = b"# Result\n\nThe lane's real, already-written report.\n"
        (self.ledger.results_dir / "review-870.md").write_bytes(orphaned)

        completed = self.ledger.complete(
            "review-870", b"a completely different note text\n", pane_nonce="nonce-22-a"
        )

        self.assertEqual("complete", completed["status"])
        self.assertEqual(hashlib.sha256(orphaned).hexdigest(), completed["result_sha256"])
        self.assertEqual(str(self.ledger.results_dir / "review-870.md"), completed["result_path"])
        # The orphaned file itself was never touched -- proof this adopted
        # it rather than silently overwriting it.
        self.assertEqual(orphaned, Path(completed["result_path"]).read_bytes())

    def test_completion_still_refuses_a_genuine_overwrite_once_a_hash_is_recorded(self):
        """The direction the immutability guard exists for must survive
        #623's fix: once a row has genuinely recorded a result, a later
        call with different content is still refused outright -- adopting
        an ORPHANED file must never widen into tolerating an OVERWRITE of a
        result the ledger already knows about."""
        self.assign()
        first = self.ledger.complete("review-870", b"# Result\n\nFirst.\n", pane_nonce="nonce-22-a")
        self.assertEqual("complete", first["status"])

        with self.assertRaisesRegex(ValueError, "immutable result"):
            self.ledger.complete("review-870", b"# Result\n\nDifferent.\n", pane_nonce="nonce-22-a")

        # And the originally recorded result is untouched.
        reloaded = self.ledger.get_task("review-870")
        self.assertEqual(first["result_sha256"], reloaded["result_sha256"])

    def test_complete_refuses_to_restamp_a_task_already_failed_or_cancelled(self):
        """agent-supervisor#627: port of `daemon/internal/ledger/ledger.go`'s
        `Finish` refusal (`#488`, `TestFinishRefusesToRestampTerminal`) into
        this ledger's own write path. A terminal stamp must be backed by an
        observed outcome and, once terminal, a task never gets restamped --
        `complete()`'s own status check (`row["status"] in ("failed",
        "cancelled")`) is that guard on this side; this pins it down the same
        way the Go test does: reach terminal one way, then try to reach it
        the OTHER way, and confirm both the refusal and that the original
        terminal status survives untouched."""
        self.assign("review-870")
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")
        failed = self.ledger.fail_unaccepted("review-870", b"never accepted", pane_nonce="nonce-22-a")
        self.assertEqual("failed", failed["status"])

        with self.assertRaisesRegex(ValueError, "cannot complete failed task"):
            self.ledger.complete("review-870", b"late completion", pane_nonce="nonce-22-a")

        # The refused restamp must not have touched the terminal row at all.
        survived = self.ledger.get_task("review-870")
        self.assertEqual("failed", survived["status"])
        self.assertEqual(failed["result_sha256"], survived["result_sha256"])

        # Same refusal the other direction: a `cancelled` task must not be
        # restampable to `complete` either.
        self.assign("review-871")
        cancelled = self.ledger.cancel_open_task("app-review", abandoned=True)
        self.assertEqual("cancelled", cancelled["status"])

        with self.assertRaisesRegex(ValueError, "cannot complete cancelled task"):
            self.ledger.complete("review-871", b"late completion", pane_nonce="nonce-22-a")
        self.assertEqual("cancelled", self.ledger.get_task("review-871")["status"])

    def test_completion_reconciles_each_injected_crash_point(self):
        result = b"# Evidence\n\nchecks passed\n"
        for failpoint in ("after_result", "after_task", "after_event"):
            with self.subTest(failpoint=failpoint):
                task_id = f"task-{failpoint}"
                if failpoint != "after_result":
                    self.ledger.cancel_open_task("app-review", abandoned=True)
                self.assign(task_id)
                with self.assertRaisesRegex(RuntimeError, failpoint):
                    self.ledger.complete(task_id, result, pane_nonce="nonce-22-a", failpoint=failpoint)
                recovered = self.ledger.complete(task_id, result, pane_nonce="nonce-22-a")
                self.assertEqual("complete", recovered["status"])
                events = self.ledger.list_events(task_id=task_id, event_type="completion")
                self.assertEqual(1, len(events))
