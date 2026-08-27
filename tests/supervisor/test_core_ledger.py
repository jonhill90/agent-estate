import contextlib
import hashlib
import concurrent.futures
import os
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
from tests.supervisor.test_core_helpers import MutableClock  # noqa: E402


class LedgerTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.clock = MutableClock()
        self.ledger = Ledger(Path(self.tempdir.name), clock=self.clock)
        self.ledger.register_lane(
            lane="app-review",
            pane_id="%22",
            nonce="nonce-22-a",
            harness="codex",
            repo="/repo/app",
            server_id="server-a",
            session_id="$4",
            command="codex",
        )

    def seed_source(self, task_id, summary="Review PR 870 without editing", source_state="OPEN"):
        self._source_number = getattr(self, "_source_number", 869) + 1
        self.ledger.reconstruct_task(
            task_id=task_id,
            source_kind="issue",
            source_url=f"https://github.com/jonhill90/Hill90/issues/{self._source_number}",
            source_ref="a" * 40,
            summary=summary,
            source_state=source_state,
            status="created",
            evidence=[],
            status_marker=None,
        )

    def assign(self, task_id="review-870"):
        self.seed_source(task_id)
        return self.ledger.assign(
            task_id=task_id,
            lane="app-review",
            pane_nonce="nonce-22-a",
            summary="Review PR 870 without editing",
        )

    def test_reconstruct_task_allows_a_second_dispatch_of_the_same_issue_under_a_different_task_id(self):
        """agent-dotfiles#144 finding 1. `source_url` used to be declared
        `NOT NULL UNIQUE`, and is derived from the issue number alone
        (`cli.py`'s `record_dispatch`) -- while `reconstruct_task` upserts
        `ON CONFLICT(id)`, a DIFFERENT key. A second dispatch of the same
        issue under a different task id (a lane failing and being
        re-briefed, a follow-up review -- this estate does this constantly)
        raised `UNIQUE constraint failed: source_tasks.source_url`,
        permanently, for that issue.

        The modelling decision (argued in full on `_migrate_source_tasks_table`):
        each `source_tasks` row is one recorded DISPATCH ATTEMPT, several of
        which legitimately share a URL, so the column is no longer unique at
        all -- not scoped to "one open attempt", because this table's status
        is never advanced past 'created' by this recording path and a scoped
        constraint would immediately wedge on exactly the case it exists to
        protect.
        """
        url = "https://github.com/jonhill90/agent-dotfiles/issues/999"
        first = self.ledger.reconstruct_task(
            task_id="ad999-first", source_kind="issue", source_url=url, source_ref="999",
            summary="#999 first attempt", source_state="OPEN", status="created",
            evidence=["claimed by dispatch.sh for lane free-3"], status_marker=None,
        )
        self.assertEqual(url, first["source_url"])

        # Not exotic: re-dispatch under a different task id, same URL.
        second = self.ledger.reconstruct_task(
            task_id="ad999-rereview", source_kind="issue", source_url=url, source_ref="999",
            summary="#999 rereview", source_state="OPEN", status="created",
            evidence=["claimed by dispatch.sh for lane free-4"], status_marker=None,
        )
        self.assertEqual(url, second["source_url"])

        # Both rows survive independently -- this is not an upsert collapsing
        # the two attempts into one.
        self.assertEqual("#999 first attempt", self.ledger.get_source_task("ad999-first")["summary"])
        self.assertEqual("#999 rereview", self.ledger.get_source_task("ad999-rereview")["summary"])
        urls = {row["source_url"] for row in self.ledger.list_source_tasks()}
        self.assertEqual({url}, urls)

    def test_assignment_requires_a_reconstructed_open_github_source(self):
        """Red: current assign() writes free-text summaries with no GitHub
        source check at all -- nothing gates assignment on the GitHub-side
        state actually permitting it."""
        with self.assertRaisesRegex(ValueError, "reconstructed GitHub source"):
            self.ledger.assign(
                task_id="no-source-task", lane="app-review", pane_nonce="nonce-22-a", summary="No source"
            )

        self.seed_source("closed-task", source_state="CLOSED")
        with self.assertRaisesRegex(ValueError, "source is not open"):
            self.ledger.assign(
                task_id="closed-task", lane="app-review", pane_nonce="nonce-22-a", summary="Closed source"
            )

        task = self.assign("open-task")
        self.assertEqual("created", task["status"])

    def test_update_source_task_state_writes_only_the_two_derived_columns(self):
        """agent-supervisor#127: the writer `source_tasks` never had. It must
        touch `source_state`/`status` and nothing this row was dispatched
        with -- `source_url`, `summary`, `evidence` all survive unchanged."""
        self.seed_source("as127-a", summary="Fix the reconciler")
        before = self.ledger.get_source_task("as127-a")

        self.clock.value += 10
        after = self.ledger.update_source_task_state("as127-a", source_state="CLOSED", status="complete")

        self.assertEqual("CLOSED", after["source_state"])
        self.assertEqual("complete", after["status"])
        self.assertEqual(before["source_url"], after["source_url"])
        self.assertEqual(before["summary"], after["summary"])
        self.assertEqual(before["evidence"], after["evidence"])

    def test_update_source_task_state_one_field_leaves_the_other_untouched(self):
        """A caller that could only resolve `source_state` (GitHub answered,
        but no local `tasks` row) or only `status` (GitHub was unreachable)
        must not clobber the column it has no fresh fact for."""
        self.seed_source("as127-b")

        only_state = self.ledger.update_source_task_state("as127-b", source_state="CLOSED")
        self.assertEqual("CLOSED", only_state["source_state"])
        self.assertEqual("created", only_state["status"])

        only_status = self.ledger.update_source_task_state("as127-b", status="complete")
        self.assertEqual("CLOSED", only_status["source_state"])
        self.assertEqual("complete", only_status["status"])

    def test_update_source_task_state_is_idempotent(self):
        """Same values, run twice: the second call must not move `updated_at`
        -- this is what lets a scheduled sweep report zero writes when
        nothing changed."""
        self.seed_source("as127-c")
        self.ledger.update_source_task_state("as127-c", source_state="CLOSED", status="complete")
        with contextlib.closing(self.ledger._connect()) as connection:
            first_updated_at = connection.execute(
                "SELECT updated_at FROM source_tasks WHERE id = ?", ("as127-c",)
            ).fetchone()["updated_at"]

        self.clock.value += 100
        self.ledger.update_source_task_state("as127-c", source_state="CLOSED", status="complete")
        with contextlib.closing(self.ledger._connect()) as connection:
            second_updated_at = connection.execute(
                "SELECT updated_at FROM source_tasks WHERE id = ?", ("as127-c",)
            ).fetchone()["updated_at"]

        self.assertEqual(first_updated_at, second_updated_at)

    def test_update_source_task_state_rejects_an_unsupported_status(self):
        self.seed_source("as127-d")
        with self.assertRaisesRegex(ValueError, "unsupported source task status"):
            self.ledger.update_source_task_state("as127-d", status="delivery_pending")

    def test_update_source_task_state_requires_unknown_task(self):
        with self.assertRaisesRegex(ValueError, "unknown source task"):
            self.ledger.update_source_task_state("no-such-task", source_state="CLOSED")

    def test_one_nonterminal_task_per_lane_and_bound_acceptance(self):
        task = self.assign()
        self.assertEqual("created", task["status"])
        with self.assertRaisesRegex(ValueError, "outstanding task"):
            self.assign("review-871")

        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")
        accepted = self.ledger.accept("review-870", pane_nonce="nonce-22-a")
        self.assertEqual("accepted", accepted["status"])
        with self.assertRaisesRegex(ValueError, "pane incarnation"):
            self.ledger.accept("review-870", pane_nonce="reused-pane")

    def test_lane_available_is_tristate_unknown_free_or_occupied(self):
        """agent-dotfiles#174: `dispatch.sh` now trusts this instead of the
        tmux window name, and the three-way split matters -- an unregistered
        lane is a different claim from a registered-but-busy one, and the
        caller (the CLI's first-sight backfill) needs to tell them apart.
        """
        self.assertIsNone(self.ledger.lane_available("never-registered"))

        self.assertTrue(self.ledger.lane_available("app-review"))

        task = self.assign()
        self.ledger.mark_delivery_pending(task["id"], pane_nonce="nonce-22-a")
        self.ledger.mark_delivered(task["id"], pane_nonce="nonce-22-a")
        self.assertFalse(self.ledger.lane_available("app-review"))

        self.ledger.complete(task["id"], b"done", pane_nonce="nonce-22-a")
        self.assertTrue(self.ledger.lane_available("app-review"))

    def test_delivery_pending_persists_before_send_and_blocks_direct_delivered(self):
        self.assign()
        with self.assertRaisesRegex(ValueError, "cannot transition"):
            self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")

        pending = self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.assertEqual("delivery_pending", pending["status"])
        # Once ambiguous, it stays ambiguous until a transition names an outcome;
        # a second delivery attempt cannot be represented as a fresh "created" task.
        with self.assertRaisesRegex(ValueError, "outstanding task"):
            self.assign("review-871")

        delivered = self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")
        self.assertEqual("delivered", delivered["status"])

    def test_reconcile_delivery_confirms_or_retires_an_ambiguous_task(self):
        self.assign()
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        with self.assertRaisesRegex(ValueError, "outcome"):
            self.ledger.reconcile_delivery("review-870", pane_nonce="nonce-22-a", outcome="bogus")

        confirmed = self.ledger.reconcile_delivery("review-870", pane_nonce="nonce-22-a", outcome="delivered")
        self.assertEqual("delivered", confirmed["status"])

        self.ledger.cancel_open_task("app-review", abandoned=True)
        self.assign("review-871")
        self.ledger.mark_delivery_pending("review-871", pane_nonce="nonce-22-a")
        retired = self.ledger.reconcile_delivery("review-871", pane_nonce="nonce-22-a", outcome="failed")
        self.assertEqual("failed", retired["status"])
        # A retired ambiguous task frees the lane for a genuinely new assignment.
        self.assign("review-872")

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

    def test_fail_unaccepted_terminates_a_delivered_task_with_no_accepted_at(self):
        """agent-supervisor#193's own primitive: a `delivered` task with no
        `accepted_at` gets terminated `failed`, not `complete` -- see
        `fail_unaccepted`'s own docstring for why. Mirrors `complete()`'s
        shape (immutable result, pane-nonce check, idempotent) on purpose."""
        self.assign()
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")

        with self.assertRaisesRegex(ValueError, "pane incarnation"):
            self.ledger.fail_unaccepted("review-870", b"never accepted", pane_nonce="some-other-nonce")

        failed = self.ledger.fail_unaccepted("review-870", b"never accepted", pane_nonce="nonce-22-a")
        self.assertEqual("failed", failed["status"])
        self.assertIsNotNone(failed["completed_at"])
        # Idempotent: a second call with the SAME result is a no-op, not an error.
        again = self.ledger.fail_unaccepted("review-870", b"never accepted", pane_nonce="nonce-22-a")
        self.assertEqual("failed", again["status"])
        # `failed` is terminal -- the lane is free for a fresh dispatch.
        self.assertTrue(self.ledger.lane_available("app-review"))

    def test_fail_unaccepted_refuses_a_task_that_was_actually_accepted(self):
        """The one thing this must never do: erase a GENUINE acceptance.
        `accepted_at` already set is the caller's own evidence being stale or
        wrong -- refuse rather than silently overwrite the record."""
        self.assign()
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")
        self.ledger.accept("review-870", pane_nonce="nonce-22-a")

        with self.assertRaisesRegex(ValueError, "accepted"):
            self.ledger.fail_unaccepted("review-870", b"never accepted", pane_nonce="nonce-22-a")

    def test_fail_unaccepted_refuses_a_task_that_is_not_delivered(self):
        """Only `delivered` is eligible -- a `created` (unsent) or `complete`
        task has no business going through this path at all."""
        task = self.assign()
        with self.assertRaisesRegex(ValueError, "only 'delivered' is eligible"):
            self.ledger.fail_unaccepted(task["id"], b"x", pane_nonce="nonce-22-a")

    def test_fail_stale_delivery_terminates_a_delivered_task_with_accepted_at_set(self):
        """agent-supervisor#374's own primitive: unlike `fail_unaccepted`,
        this never refuses on `accepted_at` -- a claude-print/pi-rpc lane has
        no pane to observe, so `accepted_at` set or not, silence proves
        nothing about success. `accepted=True` here is dispatch's own
        evidence (see `record_dispatch`'s docstring) -- it stamps
        `accepted_at` WITHOUT moving `status` off `delivered`, exactly the
        shape a claude-print lane's row has. Mirrors `complete()`/
        `fail_unaccepted()`'s shape otherwise (immutable result, pane-nonce
        check, idempotent)."""
        self.ledger.record_dispatch(
            lane="app-review", pane_id="%22", nonce="nonce-22-a", harness="codex",
            repo="/repo/app", server_id="server-a", session_id="$2", command="codex",
            task_id="review-870", source_kind="issue",
            source_url="https://github.com/acme/app/issues/870",
            source_ref="870", summary="issue #870", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane app-review", "issues: 870"],
            accepted=True,
        )
        self.assertEqual("delivered", self.ledger.get_task("review-870")["status"])
        self.assertIsNotNone(self.ledger.get_task("review-870")["accepted_at"])

        with self.assertRaisesRegex(ValueError, "pane incarnation"):
            self.ledger.fail_stale_delivery("review-870", b"no pane to observe", pane_nonce="some-other-nonce")

        failed = self.ledger.fail_stale_delivery("review-870", b"no pane to observe", pane_nonce="nonce-22-a")
        self.assertEqual("failed", failed["status"])
        self.assertIsNotNone(failed["completed_at"])
        # Idempotent: a second call with the SAME result is a no-op, not an error.
        again = self.ledger.fail_stale_delivery("review-870", b"no pane to observe", pane_nonce="nonce-22-a")
        self.assertEqual("failed", again["status"])
        # `failed` is terminal -- the lane is free for a fresh dispatch.
        self.assertTrue(self.ledger.lane_available("app-review"))

    def test_fail_stale_delivery_refuses_a_task_that_is_not_delivered(self):
        """Only `delivered` is eligible -- the same discipline `fail_unaccepted` holds."""
        task = self.assign()
        with self.assertRaisesRegex(ValueError, "only 'delivered' is eligible"):
            self.ledger.fail_stale_delivery(task["id"], b"x", pane_nonce="nonce-22-a")

    def test_fail_stale_delivery_refuses_a_completed_or_cancelled_task(self):
        self.assign()
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")
        self.ledger.complete("review-870", b"done", pane_nonce="nonce-22-a")
        # Same content as the completion above -- an intentionally DIFFERENT
        # result here would trip `_write_result`'s immutable-content check
        # before the status check this test is exercising ever runs.
        with self.assertRaisesRegex(ValueError, "cannot fail-stale-delivery"):
            self.ledger.fail_stale_delivery("review-870", b"done", pane_nonce="nonce-22-a")

    def test_record_dispatch_accepted_flag_sets_accepted_at_but_leaves_status_delivered(self):
        """agent-supervisor#193: `accepted=True` is dispatch's OWN evidence
        (see `record_dispatch`'s docstring), stamped as `accepted_at` WITHOUT
        moving `status` to `accepted` -- that status is the self-report
        path's (`Ledger.accept`), and `list_delivered_open_tasks` (the
        completion reconciler's whole candidate set) selects on
        `status='delivered'`. `accepted=False` (the default) must leave
        `accepted_at` null, exactly as every caller before this flag
        existed."""
        unaccepted = self.ledger.record_dispatch(
            lane="app-review", pane_id="%22", nonce="nonce-22-a", harness="codex",
            repo="/repo/app", server_id="server-a", session_id="$2", command="codex",
            task_id="review-900", source_kind="issue",
            source_url="https://github.com/acme/app/issues/900",
            source_ref="900", summary="issue #900", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane app-review", "issues: 900"],
        )["task"]
        self.assertEqual("delivered", unaccepted["status"])
        self.assertIsNone(unaccepted["accepted_at"])

        self.ledger.cancel_open_task("app-review", abandoned=True)
        accepted = self.ledger.record_dispatch(
            lane="app-review", pane_id="%22", nonce="nonce-22-b", harness="codex",
            repo="/repo/app", server_id="server-a", session_id="$3", command="codex",
            task_id="review-901", source_kind="issue",
            source_url="https://github.com/acme/app/issues/901",
            source_ref="901", summary="issue #901", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane app-review", "issues: 901"],
            accepted=True,
        )["task"]
        self.assertEqual("delivered", accepted["status"])
        self.assertIsNotNone(accepted["accepted_at"])
        # Directly observable candidate-set effect: still selected by the
        # exact query the completion reconciler runs.
        delivered_ids = {t["id"] for t in self.ledger.list_delivered_open_tasks()}
        self.assertIn("review-901", delivered_ids)

    def test_lane_reregistration_cancels_an_outstanding_non_pending_task_instead_of_rebinding_it(self):
        """register_lane does an unconditional INSERT ... ON CONFLICT UPDATE,
        so re-registering with a CHANGED identity would silently rebind a
        live task's identity out from under it if nothing intervened.
        `delivery_pending` is exempted: it has its own reconciliation escape
        valve keyed off the task's own pane_nonce (see `_reconcile_transition`),
        which #871 relies on to survive a dead pane being re-registered.

        agent-dotfiles#144 finding 3: for every OTHER outstanding status this
        used to raise here, permanently -- the only way out was `lane-done.sh`
        completing that exact task by renaming the window it owned, so a lane
        freed any other way (renamed by hand, a worker that died mid-turn)
        wedged every subsequent register_lane call for that lane forever. A
        changed identity is the evidence that the old task can never complete
        through this pane again, so this now CANCELS the stale task and
        proceeds with the re-registration, rather than wedging."""
        task = self.assign()
        self.assertEqual("created", task["status"])
        registered = self.ledger.register_lane(
            lane="app-review",
            pane_id="%23",
            nonce="nonce-23-b",
            harness="codex",
            repo="/repo/app",
            server_id="server-a",
            session_id="$4",
            command="codex",
        )
        self.assertEqual("nonce-23-b", registered["nonce"])
        self.assertEqual("nonce-23-b", self.ledger.get_lane("app-review")["nonce"])
        stale = self.ledger.get_task("review-870")
        self.assertEqual("cancelled", stale["status"])
        # And the lane is genuinely free again: a new task can be assigned to
        # it under the new incarnation without hitting the one-open-task guard.
        self.seed_source("review-871")
        reassigned = self.ledger.assign(
            task_id="review-871", lane="app-review", pane_nonce="nonce-23-b", summary="Second task"
        )
        self.assertEqual("created", reassigned["status"])

    def test_lane_reregistration_with_the_same_identity_does_not_cancel_the_outstanding_task(self):
        """The cancel-on-reregistration self-heal (#144 finding 3) is scoped
        to a CHANGED identity. Re-registering the exact same pane_id/nonce/
        harness/repo/server_id/session_id/command -- the ordinary no-op path,
        e.g. a duplicate register call -- must leave a genuinely still-running
        task alone."""
        task = self.assign()
        self.assertEqual("created", task["status"])
        self.ledger.register_lane(
            lane="app-review",
            pane_id="%22",
            nonce="nonce-22-a",
            harness="codex",
            repo="/repo/app",
            server_id="server-a",
            session_id="$4",
            command="codex",
        )
        self.assertEqual("created", self.ledger.get_task("review-870")["status"])

    def test_lane_reregistration_does_not_cancel_a_delivery_pending_task(self):
        """#871: a `delivery_pending` task survives re-registration untouched
        -- it has its own reconciliation path keyed off the task's own
        pane_nonce, and must not be silently cancelled by a re-registration
        racing an in-flight send."""
        self.assign()
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.register_lane(
            lane="app-review",
            pane_id="%23",
            nonce="nonce-23-b",
            harness="codex",
            repo="/repo/app",
            server_id="server-a",
            session_id="$4",
            command="codex",
        )
        self.assertEqual("delivery_pending", self.ledger.get_task("review-870")["status"])

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

    def test_cancel_open_task_requires_a_result_or_abandoned(self):
        """agent-supervisor#649: no default -- a caller must say which,
        rather than a bare cancel silently landing as an abandonment. This is
        the change that stops the next 951."""
        self.assign()
        with self.assertRaisesRegex(ValueError, "result or abandoned=True"):
            self.ledger.cancel_open_task("app-review")

    def test_cancel_open_task_refuses_both_a_result_and_abandoned(self):
        self.assign()
        with self.assertRaisesRegex(ValueError, "not both"):
            self.ledger.cancel_open_task("app-review", result=b"shipped", abandoned=True)

    def test_cancel_open_task_with_a_result_persists_it_with_a_hash(self):
        """The mutation-check in the direction the issue is actually about:
        a cancel carrying a result must not be indistinguishable from an
        abandonment. `result_sha256` is written alongside `result_path`, the
        way `record_completion` (`Ledger.complete`) already does."""
        self.assign()
        cancelled = self.ledger.cancel_open_task("app-review", result=b"# Delivered\n\nPR merged; pane was gone.\n")
        self.assertEqual("cancelled", cancelled["status"])
        self.assertIsNotNone(cancelled["result_path"])
        self.assertIsNotNone(cancelled["result_sha256"])
        self.assertEqual(
            hashlib.sha256(b"# Delivered\n\nPR merged; pane was gone.\n").hexdigest(),
            cancelled["result_sha256"],
        )
        self.assertEqual(
            b"# Delivered\n\nPR merged; pane was gone.\n", Path(cancelled["result_path"]).read_bytes()
        )

    def test_cancel_open_task_abandoned_persists_no_result(self):
        """The other direction of the same mutation-check: an explicit
        abandonment must still be recordable with no result, and must not
        pick up a stray result_path/sha256 from anywhere."""
        self.assign()
        cancelled = self.ledger.cancel_open_task("app-review", abandoned=True)
        self.assertEqual("cancelled", cancelled["status"])
        self.assertIsNone(cancelled["result_path"])
        self.assertIsNone(cancelled["result_sha256"])

    def test_cancel_open_task_result_and_abandoned_produce_different_rows(self):
        """A change where every cancel looks the same as before has done
        nothing -- assert the two are actually distinguishable, not just
        independently correct."""
        self.assign("review-870")
        with_result = self.ledger.cancel_open_task("app-review", result=b"# shipped\n")
        self.assign("review-871")
        abandoned = self.ledger.cancel_open_task("app-review", abandoned=True)
        self.assertIsNotNone(with_result["result_path"])
        self.assertIsNone(abandoned["result_path"])

    def test_list_terminal_tasks_missing_result_finds_an_abandoned_cancel_not_a_result_bearing_one(self):
        """agent-supervisor#649: the discoverability half -- a terminal row
        with no result must be queryable without knowing the schema, and the
        cheap regression the issue names: a PR-scoped task whose PR merged
        (recorded here as a cancel carrying a result) must never show up in
        this list the way `cancelled` used to for all 951 rows."""
        self.ledger.reconstruct_task(
            task_id="as649-shipped", source_kind="pull", source_url="https://example/pull/649",
            source_ref="649", summary="PR merged before the pane died", source_state="OPEN",
            status="created", evidence=[], status_marker=None,
        )
        self.ledger.assign(
            task_id="as649-shipped", lane="app-review", pane_nonce="nonce-22-a", summary="shipped"
        )
        self.ledger.cancel_open_task("app-review", result=b"# PR #649 merged\n")

        self.assign("review-870")
        self.ledger.cancel_open_task("app-review", abandoned=True)

        missing_ids = {row["id"] for row in self.ledger.list_terminal_tasks_missing_result()}
        self.assertIn("review-870", missing_ids)
        self.assertNotIn("as649-shipped", missing_ids)

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

    def test_record_dispatch_is_atomic_across_every_injected_crash_point(self):
        """agent-dotfiles#144 finding 2. `record_dispatch` used to make five
        independent `Ledger` calls -- register_lane, reconstruct_task, assign,
        mark_delivery_pending, mark_delivered -- each its own lock and
        transaction. A crash between any two left whatever had already
        committed: the review reproduced this as an orphan `lanes` row (the
        lane registered, nothing else recorded) claiming a lane occupied for a
        dispatch nothing else records. That is exactly the state this layer
        exists to prevent, so this parametrises over EVERY step, not just the
        one the reviewer happened to hit.

        A failure at ANY step must leave NO lane row, NO task row and NO
        source_tasks row for that attempt -- not a partial write, not an
        orphan claiming the lane is busy. A clean retry afterwards must then
        succeed exactly as if the failed attempt never happened.
        """
        failpoints = (
            "after_register_lane",
            "after_reconstruct_task",
            "after_assign",
            "after_mark_delivery_pending",
            "after_mark_delivered",
        )
        for failpoint in failpoints:
            with self.subTest(failpoint=failpoint):
                lane = f"lane-{failpoint}"
                task_id = f"task-{failpoint}"
                url = f"https://github.com/jonhill90/agent-dotfiles/issues/{hash(failpoint) % 10_000}"
                kwargs = dict(
                    lane=lane,
                    pane_id="%9",
                    nonce="nonce-9",
                    harness="codex",
                    repo="/repo/app",
                    server_id="server-a",
                    session_id="$9",
                    command="codex",
                    task_id=task_id,
                    source_kind="issue",
                    source_url=url,
                    source_ref="999",
                    summary="Atomicity check",
                    source_state="OPEN",
                    evidence=["seeded"],
                    status_marker=None,
                )
                with self.assertRaisesRegex(RuntimeError, failpoint):
                    self.ledger.record_dispatch(**kwargs, failpoint=failpoint)

                # THE assertion: no partial write survives the crash.
                self.assertIsNone(self.ledger.get_lane(lane), f"orphan lane row survived {failpoint}")
                self.assertIsNone(self.ledger.get_task(task_id), f"orphan task row survived {failpoint}")
                self.assertIsNone(
                    self.ledger.get_source_task(task_id), f"orphan source_tasks row survived {failpoint}"
                )

                # And a clean retry afterwards succeeds normally -- the crash
                # left nothing behind to conflict with it.
                result = self.ledger.record_dispatch(**kwargs)
                self.assertEqual("nonce-9", result["lane"]["nonce"])
                self.assertEqual("delivered", result["task"]["status"])

    def test_record_dispatch_succeeds_end_to_end_and_matches_the_five_call_sequence(self):
        """Not just atomic -- still does the same work the five-call sequence
        used to, in the same order, so a caller (`cli.py`'s `record_dispatch`)
        that switches to this one call sees identical results."""
        result = self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-3",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="ad999-first",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-dotfiles/issues/999",
            source_ref="999",
            summary="#999 first",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3", "issues: 999"],
            status_marker=None,
        )
        self.assertEqual("nonce-3", result["lane"]["nonce"])
        self.assertEqual("claude", result["lane"]["harness"])
        self.assertEqual("delivered", result["task"]["status"])
        self.assertIsNotNone(result["task"]["delivered_at"])
        source = self.ledger.get_source_task("ad999-first")
        self.assertEqual("https://github.com/jonhill90/agent-dotfiles/issues/999", source["source_url"])

    def test_record_dispatch_records_the_originating_project_dir_alongside_the_session_id(self):
        """agent-supervisor#172. `repo` is the lane's WORKING directory (a
        worktree); `harness_project_dir` is the directory the harness process
        was actually LAUNCHED in, which `restore.sh` needs because
        `claude --resume` is scoped to it, not to `repo`. This is the
        red-before-the-fix case: a lane whose two directories genuinely
        differ, which the pre-#172 code had nowhere to record at all."""
        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-3",
            harness="claude",
            repo="/repo/free-3-worktree",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="ad999-first",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-dotfiles/issues/999",
            source_ref="999",
            summary="#999 first",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3", "issues: 999"],
            status_marker=None,
            harness_session_id="7cef6d59-0000-4000-8000-000000000000",
            harness_project_dir="/repo/free-3-originating",
        )
        lane = self.ledger.get_lane("free-3")
        self.assertEqual("/repo/free-3-worktree", lane["repo"])
        self.assertEqual("/repo/free-3-originating", lane["harness_project_dir"])
        self.assertNotEqual(lane["repo"], lane["harness_project_dir"])

        plan = {row["lane"]: row for row in self.ledger.restore_plan()}
        self.assertEqual("/repo/free-3-originating", plan["free-3"]["harness_project_dir"])
        self.assertEqual("/repo/free-3-worktree", plan["free-3"]["repo"])

    def test_a_changed_pane_identity_clears_harness_project_dir_with_the_session_id(self):
        """The pairing rule: `harness_project_dir` must never survive a
        re-registration that clears `harness_session_id`, or a stale
        directory would sit next to a session id that no longer names it --
        exactly the mismatch #172 exists to prevent, just introduced from the
        other direction."""
        self.ledger.register_lane(
            lane="free-3", pane_id="%3", nonce="nonce-3", harness="claude",
            repo="/repo/free-3", server_id="server-a", session_id="$3", command="claude.exe",
            harness_session_id="7cef6d59-0000-4000-8000-000000000000",
            harness_project_dir="/repo/free-3-originating",
        )
        before = self.ledger.get_lane("free-3")
        self.assertEqual("/repo/free-3-originating", before["harness_project_dir"])

        # A different pane_id is a new incarnation (`_register_lane_tx`'s own
        # `changed_identity` test) -- the old conversation's directory must
        # not be carried forward onto a process that was never launched in it.
        self.ledger.register_lane(
            lane="free-3", pane_id="%99", nonce="nonce-99", harness="claude",
            repo="/repo/free-3", server_id="server-a", session_id="$3", command="claude.exe",
        )
        after = self.ledger.get_lane("free-3")
        self.assertEqual("", after["harness_session_id"])
        self.assertEqual("", after["harness_project_dir"])

    def test_get_task_for_issue_resolves_by_issue_never_by_branch(self):
        """agent-supervisor#35: `dispatch.sh`'s `--reviews-pr` guard used to
        determine a PR's author by regexing its head branch. The ledger
        already carries the fact this needs -- `source_tasks.source_ref` is
        the issue number `record_dispatch` was called FOR (see that
        function's own docstring) -- so this reads it back keyed on the
        issue, with no branch name involved anywhere in this test."""
        self.assertIsNone(self.ledger.get_task_for_issue("501"))

        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-3",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="ad501-scrub-secrets",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-dotfiles/issues/501",
            source_ref="501",
            summary="#501 scrub secrets",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3"],
            status_marker=None,
        )
        found = self.ledger.get_task_for_issue("501")
        self.assertEqual("free-3", found["lane"])
        self.assertEqual("ad501-scrub-secrets", found["id"])

        # An int and its string spelling are the same issue -- callers pass
        # both (dispatch.sh always passes a string; cli.py's own argparse
        # default for --issue is a str too, but this must not be brittle
        # about the caller's type).
        self.assertEqual("ad501-scrub-secrets", self.ledger.get_task_for_issue(501)["id"])

        # A DIFFERENT issue number, even one that shares a prefix, is not a
        # match -- this is not a substring or LIKE lookup.
        self.assertIsNone(self.ledger.get_task_for_issue("5011"))
        self.assertIsNone(self.ledger.get_task_for_issue("50"))

    def test_get_task_for_issue_prefers_the_most_recent_dispatch(self):
        """An issue re-dispatched after a prior task finished (recycled, or
        handed to a second lane) has more than one `source_tasks` row for
        the same issue -- the most recent one is the lane that actually
        holds it now, not the first lane that ever touched it."""
        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-3",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="ad502-first-attempt",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-dotfiles/issues/502",
            source_ref="502",
            summary="#502 first attempt",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3"],
            status_marker=None,
        )
        self.ledger.cancel_open_task("free-3", abandoned=True)
        self.clock.value += 1

        self.ledger.record_dispatch(
            lane="free-4",
            pane_id="%4",
            nonce="nonce-4",
            harness="claude",
            repo="/repo/free-4",
            server_id="server-a",
            session_id="$4",
            command="claude.exe",
            task_id="ad502-second-attempt",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-dotfiles/issues/502",
            source_ref="502",
            summary="#502 second attempt",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-4"],
            status_marker=None,
        )

        found = self.ledger.get_task_for_issue("502")
        self.assertEqual("free-4", found["lane"])
        self.assertEqual("ad502-second-attempt", found["id"])

    def test_get_open_task_for_pr_resolves_by_pr_never_by_issue(self):
        """agent-supervisor#159: a PR-scoped dispatch (a review, or a fix
        pass, on PR N while the issue it closes stays claimed by the
        in-flight work that opened it) is recorded `source_kind='pull'`,
        keyed by the PR number -- never the issue. This is the read side,
        the one `dispatch.sh` asks BEFORE picking a lane so a second
        dispatcher can see the PR is already spoken for instead of minting a
        second task for it (the measured "...b" suffix duplication)."""
        self.assertIsNone(self.ledger.get_open_task_for_pr("149"))

        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-3",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="as159-rev149",
            source_kind="pull",
            source_url="https://github.com/jonhill90/agent-supervisor/pull/149",
            source_ref="149",
            summary="review PR #149",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3", "pr: 149"],
            status_marker=None,
        )
        found = self.ledger.get_open_task_for_pr("149")
        self.assertEqual("free-3", found["lane"])
        self.assertEqual("as159-rev149", found["id"])

        # An int and its string spelling are the same PR, the same as
        # `get_task_for_issue` above.
        self.assertEqual("as159-rev149", self.ledger.get_open_task_for_pr(149)["id"])

        # This task's issue (say, #112, the review's own tracking issue) was
        # never claimed as its SOURCE -- `issue-lane` for it must not answer
        # with this task.
        self.assertIsNone(self.ledger.get_task_for_issue("112"))

    def test_get_open_task_for_pr_ignores_completed_or_cancelled_tasks(self):
        """UNLIKE `get_task_for_issue` (whose only caller today is a
        diagnostic query with no live effect), this is asked by
        `dispatch.sh` step 0.6 as a REFUSAL gate before a lane is even
        picked -- so it must not go on refusing every future dispatch of the
        same PR forever just because one prior review of it finished or was
        cancelled."""
        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-3",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="as159-rev150-first",
            source_kind="pull",
            source_url="https://github.com/jonhill90/agent-supervisor/pull/150",
            source_ref="150",
            summary="review PR #150",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3", "pr: 150"],
            status_marker=None,
        )
        row = self.ledger.get_task("as159-rev150-first")
        self.ledger.complete("as159-rev150-first", b"approved", pane_nonce=row["pane_nonce"])

        self.assertIsNone(self.ledger.get_open_task_for_pr("150"))

    def test_record_dispatch_refuses_a_second_open_dispatch_of_the_same_pr(self):
        """agent-supervisor#169: `dispatch.sh` step 0.6's `pr-lane` read is a
        TOCTOU by itself -- two dispatchers can both read "not yet claimed"
        before either one's `record_dispatch` write lands, seconds later.
        This is the write-side gate that closes it: the SECOND writer's
        INSERT into `source_tasks` must fail atomically, not merely be
        caught by an earlier read that a race can outrun."""
        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-a",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="as169-race-a",
            source_kind="pull",
            source_url="https://github.com/jonhill90/agent-supervisor/pull/960",
            source_ref="960",
            summary="fix pass on PR #960",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3", "pr: 960"],
            status_marker=None,
        )
        with self.assertRaises(sqlite3.IntegrityError) as exc:
            self.ledger.record_dispatch(
                lane="free-4",
                pane_id="%4",
                nonce="nonce-b",
                harness="claude",
                repo="/repo/free-4",
                server_id="server-a",
                session_id="$4",
                command="claude.exe",
                task_id="as169-race-b",
                source_kind="pull",
                source_url="https://github.com/jonhill90/agent-supervisor/pull/960",
                source_ref="960",
                summary="a second dispatcher also tries PR #960",
                source_state="OPEN",
                evidence=["claimed by dispatch.sh for lane free-4", "pr: 960"],
                status_marker=None,
            )
        self.assertIn("source_tasks.source_ref", str(exc.exception))

        # The whole five-write transaction rolled back -- the losing
        # dispatcher's task was never created, not left half-written.
        self.assertIsNone(self.ledger.get_task("as169-race-b"))
        # And the winner is exactly, and only, the first writer.
        self.assertEqual("free-3", self.ledger.get_open_task_for_pr("960")["lane"])

        # A CLOSED prior dispatch of the same PR never blocks a fresh one --
        # the index is scoped to open rows, same as `get_open_task_for_pr`'s
        # own filter.
        row = self.ledger.get_task("as169-race-a")
        self.ledger.complete("as169-race-a", b"done", pane_nonce=row["pane_nonce"])
        self.ledger.record_dispatch(
            lane="free-4",
            pane_id="%4",
            nonce="nonce-c",
            harness="claude",
            repo="/repo/free-4",
            server_id="server-a",
            session_id="$4",
            command="claude.exe",
            task_id="as169-race-c",
            source_kind="pull",
            source_url="https://github.com/jonhill90/agent-supervisor/pull/960",
            source_ref="960",
            summary="a fresh dispatch, after the first one closed",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-4", "pr: 960"],
            status_marker=None,
        )
        self.assertEqual("free-4", self.ledger.get_open_task_for_pr("960")["lane"])

    def test_get_author_task_for_issue_does_not_drift_to_later_reviews(self):
        """agent-supervisor#76: review tasks for the same issue must never
        become the author. The bug is drift, so this asserts after each new
        review dispatch instead of checking only the final ledger shape."""

        def dispatch(lane, task_id, summary):
            self.ledger.record_dispatch(
                lane=lane,
                pane_id=f"%{lane.rsplit('-', 1)[-1]}",
                nonce=f"nonce-{task_id}",
                harness="claude",
                repo=f"/repo/{lane}",
                server_id="server-a",
                session_id="$3",
                command="claude.exe",
                task_id=task_id,
                source_kind="issue",
                source_url="https://github.com/jonhill90/agent-supervisor/issues/76",
                source_ref="76",
                summary=summary,
                source_state="OPEN",
                evidence=[f"claimed by dispatch.sh for lane {lane}"],
                status_marker=None,
            )
            self.ledger.complete(task_id, b"# Result\n\nDone.\n", pane_nonce=f"nonce-{task_id}")
            self.clock.value += 1

        dispatch("free-3", "as76-author-lane-drift", "#76 fix author resolver")
        self.assertEqual("free-3", self.ledger.get_author_task_for_issue("76")["lane"])

        for lane, task_id in [
            ("free-4", "as76-review-as73"),
            ("free-5", "as76-rev73b"),
            ("free-6", "as76-review-as73c"),
            ("free-7", "as76-review-as73d"),
        ]:
            dispatch(lane, task_id, "#76 review PR #73")
            author = self.ledger.get_author_task_for_issue("76")
            self.assertEqual("free-3", author["lane"])
            self.assertEqual("as76-author-lane-drift", author["id"])

    def test_get_author_task_for_issue_resolves_by_head_ref_after_redispatch(self):
        """agent-supervisor#77: the reviewer's own reproduction -- a first
        attempt abandoned, then a second lane re-dispatched for the same
        issue actually produces the PR. Resolving to "the first non-review
        task" (the old rule) picks the stale, abandoned lane; this must
        resolve to whichever lane the PR's head branch names instead."""
        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-first",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="as13-as13-ci-as-a-gate",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/13",
            source_ref="13",
            summary="#13 first attempt",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3"],
            status_marker=None,
        )
        self.ledger.complete("as13-as13-ci-as-a-gate", b"# Result\n\nAbandoned.\n", pane_nonce="nonce-first")
        self.clock.value += 1

        self.ledger.record_dispatch(
            lane="free-5",
            pane_id="%5",
            nonce="nonce-second",
            harness="claude",
            repo="/repo/free-5",
            server_id="server-a",
            session_id="$5",
            command="claude.exe",
            task_id="as13-as13-reopen",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/13",
            source_ref="13",
            summary="#13 reopened attempt",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-5"],
            status_marker=None,
        )
        self.ledger.complete("as13-as13-reopen", b"# Result\n\nDone.\n", pane_nonce="nonce-second")

        # No head ref, two non-review candidates: genuinely ambiguous.
        self.assertIsNone(self.ledger.get_author_task_for_issue("13"))

        # The old "first" rule would answer free-3 here -- exactly the wrong
        # lane the reviewer caught. The head ref names the lane that actually
        # produced the PR.
        author = self.ledger.get_author_task_for_issue("13", head_ref="lane/13-as13-reopen")
        self.assertEqual("free-5", author["lane"])
        self.assertEqual("as13-as13-reopen", author["id"])

    def test_get_author_task_for_issue_unknown_when_only_reviews_exist(self):
        self.ledger.record_dispatch(
            lane="free-4",
            pane_id="%4",
            nonce="nonce-review-only",
            harness="claude",
            repo="/repo/free-4",
            server_id="server-a",
            session_id="$4",
            command="claude.exe",
            task_id="as76-review-as73",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/76",
            source_ref="76",
            summary="#76 review PR #73",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-4"],
            status_marker=None,
        )
        self.assertIsNone(self.ledger.get_author_task_for_issue("76"))

    def test_get_author_task_for_issue_survives_completion(self):
        """agent-supervisor#90: the guard added by #70 (and generalised by
        #76/#77) refused a review dispatched back to its author's lane, but
        only while that lane's task read as still open -- `record-completion`
        runs on every lane as routine reconciliation, and closing the task
        made the same dispatch land on the author.

        This query has no status filter at all -- unlike `lane_available` /
        `get_open_task_for_lane` (which exist specifically to answer "is this
        lane busy right now") -- so a task moving to `complete` must not
        change the answer here, the same way #79/#80 already proved
        `cancelled` does not. The mutation below is `dispatch.sh`'s own
        author-lookup query with a status filter spliced in -- exactly the
        defect this test exists to catch -- and confirms this test would
        have gone red against it."""
        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-90",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="as90-authorship-outlives-task",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/90",
            source_ref="90",
            summary="#90 authorship outlives task",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3"],
            status_marker=None,
        )
        row = self.ledger.get_task("as90-authorship-outlives-task")
        self.ledger.complete("as90-authorship-outlives-task", b"# Result\n\ndone\n", pane_nonce=row["pane_nonce"])

        author = self.ledger.get_author_task_for_issue("90")
        self.assertIsNotNone(author, "authorship must not go unknown just because the task completed")
        self.assertEqual("free-3", author["lane"])
        self.assertEqual("as90-authorship-outlives-task", author["id"])

        # RED CHECK: an "only open tasks" mutation of the same query -- the
        # bug #90 reported -- must lose this exact answer. If it did not,
        # the assertions above would not be exercising the status-independence
        # they claim to.
        with contextlib.closing(self.ledger._connect()) as connection:
            mutant_row = connection.execute(
                """
                SELECT tasks.* FROM tasks
                JOIN source_tasks ON source_tasks.id = tasks.id
                WHERE source_tasks.source_kind = 'issue' AND source_tasks.source_ref = ?
                  AND tasks.status NOT IN ('complete', 'failed', 'cancelled')
                ORDER BY tasks.created_at ASC, tasks.id ASC
                """,
                ("90",),
            ).fetchone()
        self.assertIsNone(
            mutant_row,
            "mutation sanity check failed: the 'only open tasks' variant should find nothing "
            "for a completed author task -- if it found one, this test cannot tell the fix from the bug",
        )

    def test_get_contributor_tasks_for_issue_includes_every_non_review_task(self):
        """agent-supervisor#190: `get_author_task_for_issue` narrows multiple
        non-review candidates for the same issue down to one (or `None`
        when it cannot). `get_contributor_tasks_for_issue` must NOT narrow --
        a fix-pass task dispatched against the same issue as an original
        author (both non-review) belongs in the CONTRIBUTOR SET dispatch.sh
        excludes a review from, even though `get_author_task_for_issue`
        itself would refuse to pick between them."""

        def dispatch(lane, task_id, summary):
            self.ledger.record_dispatch(
                lane=lane,
                pane_id=f"%{lane.rsplit('-', 1)[-1]}",
                nonce=f"nonce-{task_id}",
                harness="claude",
                repo=f"/repo/{lane}",
                server_id="server-a",
                session_id="$3",
                command="claude.exe",
                task_id=task_id,
                source_kind="issue",
                source_url="https://github.com/jonhill90/agent-supervisor/issues/190",
                source_ref="190",
                summary=summary,
                source_state="OPEN",
                evidence=[f"claimed by dispatch.sh for lane {lane}"],
                status_marker=None,
            )
            self.clock.value += 1

        dispatch("free-3", "as190-original-author", "#190 original fix")
        dispatch("free-4", "as190-fix-pass", "#190 fix pass after review findings")
        dispatch("free-5", "as190-review-only", "#190 review PR #460")

        contributors = self.ledger.get_contributor_tasks_for_issue("190")
        lanes = {row["lane"] for row in contributors}
        self.assertEqual({"free-3", "free-4"}, lanes, "both non-review tasks are contributors")
        task_ids = {row["id"] for row in contributors}
        self.assertNotIn("as190-review-only", task_ids, "a review task is never a contributor")

        # Unlike get_contributor_tasks_for_issue, the single-author lookup
        # for the SAME issue is genuinely ambiguous here (two non-review
        # candidates, no head_ref to disambiguate) and correctly answers
        # "don't know" -- proving the two methods answer different
        # questions rather than one silently reusing the other's narrowing.
        self.assertIsNone(self.ledger.get_author_task_for_issue("190"))

    def test_get_contributor_tasks_for_issue_unknown_issue_is_empty(self):
        self.assertEqual([], self.ledger.get_contributor_tasks_for_issue("no-such-issue"))

    def test_get_author_task_for_issue_cross_repo_collision(self):
        """agent-supervisor#146: the exact measured defect. Issue #181
        exists in both `jonhill90/agent-dotfiles` and `jonhill90/skills` --
        two entirely different tasks, dispatched to two different lanes,
        both under `source_ref='181'`. An unscoped lookup silently answered
        for the wrong repo (`agent-dotfiles:6` when the caller meant
        `skills`'s PR, authored by `skills:2`); this asserts a `repo`-scoped
        lookup gets the right one, and the unscoped, ambiguous case fails
        closed rather than picking either."""
        self.ledger.record_dispatch(
            lane="agent-dotfiles-6",
            pane_id="%6",
            nonce="nonce-ad181",
            harness="claude",
            repo="/repo/agent-dotfiles-6",
            server_id="server-a",
            session_id="$6",
            command="claude.exe",
            task_id="ad181-roster-the-orphaned-skills",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-dotfiles/issues/181",
            source_ref="181",
            summary="#181 roster the orphaned skills",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane agent-dotfiles-6"],
            status_marker=None,
        )
        self.clock.value += 1
        self.ledger.record_dispatch(
            lane="skills-2",
            pane_id="%2",
            nonce="nonce-skills181",
            harness="claude",
            repo="/repo/skills-2",
            server_id="server-b",
            session_id="$2",
            command="claude.exe",
            task_id="skills181-declare-fix-conflict",
            source_kind="issue",
            source_url="https://github.com/jonhill90/skills/issues/181",
            source_ref="181",
            summary="#181 declare fix conflict",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane skills-2"],
            status_marker=None,
        )

        # Repo-scoped: each repo's own #181 resolves to its own lane.
        skills_author = self.ledger.get_author_task_for_issue("181", repo="jonhill90/skills")
        self.assertEqual("skills-2", skills_author["lane"])
        self.assertEqual("skills181-declare-fix-conflict", skills_author["id"])

        dotfiles_author = self.ledger.get_author_task_for_issue("181", repo="jonhill90/agent-dotfiles")
        self.assertEqual("agent-dotfiles-6", dotfiles_author["lane"])

        # The exact defect: with the repo scope removed again, this must be
        # reproducible as ambiguous -- not silently answer for one repo's
        # row the way the pre-fix resolver did.
        self.assertIsNone(
            self.ledger.get_author_task_for_issue("181"),
            "an unscoped lookup across a genuine cross-repo issue-number collision "
            "must fail closed, not guess",
        )

        # Same fail-closed posture for issue-lane's single-answer lookup.
        self.assertEqual(
            "skills-2", self.ledger.get_task_for_issue("181", repo="jonhill90/skills")["lane"]
        )
        self.assertIsNone(self.ledger.get_task_for_issue("181"))

        # contributor-issue-lanes is deliberately over-inclusive when unscoped
        # (the safe direction, per its own docstring) but still narrows
        # correctly when a repo is given.
        scoped_contributors = self.ledger.get_contributor_tasks_for_issue("181", repo="jonhill90/skills")
        self.assertEqual(["skills-2"], [row["lane"] for row in scoped_contributors])
        unscoped_lanes = {row["lane"] for row in self.ledger.get_contributor_tasks_for_issue("181")}
        self.assertEqual({"agent-dotfiles-6", "skills-2"}, unscoped_lanes)

    def test_get_task_for_worktree_resolves_when_the_branch_slug_differs_from_the_task_slug(self):
        """agent-supervisor#117: the actual, measured bug. Task
        `as101-pr-inference-fix` produced a PR whose head branch was
        `fix/101-not-a-review-escape` -- a slug sharing no text with the
        dispatch's own -- so nothing reconstructed from that branch name
        could ever find this task. The worktree path is not renamed the way
        the branch is, so recording it at dispatch time and looking it back
        up here resolves this regardless of what the branch was later
        called."""
        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-117",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="as101-pr-inference-fix",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/101",
            source_ref="101",
            summary="#101 pr-inference-fix; worktree=/tmp/ad-101-pr-inference-fix-42",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3"],
            status_marker=None,
            worktree_path="/tmp/ad-101-pr-inference-fix-42",
        )

        found = self.ledger.get_task_for_worktree("/tmp/ad-101-pr-inference-fix-42")
        self.assertIsNotNone(found)
        self.assertEqual("free-3", found["lane"])
        self.assertEqual("as101-pr-inference-fix", found["id"])

    def test_get_task_for_worktree_unknown_for_an_unrecorded_path(self):
        self.assertIsNone(self.ledger.get_task_for_worktree("/tmp/never-recorded"))

    def test_get_task_for_worktree_unknown_for_a_blank_path(self):
        """A pre-#117 row's `worktree_path` reads '' (see
        `_migrate_tasks_table`), and matching '' against another blank path
        would wrongly declare every such row the same worktree."""
        self.assertIsNone(self.ledger.get_task_for_worktree(""))

    def test_get_task_for_worktree_matches_across_var_and_private_var_spellings(self):
        """agent-supervisor#624: the measured bug. On macOS `/var` is a
        symlink to `/private/var`, but the two live writers of
        `worktree_path` (`dispatch.sh`'s resolved `pwd -P` write and
        `reconcile_worktree_paths.py`'s unresolved-text write, pre-#624 fix)
        do not agree which spelling lands in the column. A correctly-
        dispatched lane must be found no matter which of the four
        (`/var` vs `/private/var`) x (doubled vs single separator)
        combinations either side holds."""
        self.ledger.record_dispatch(
            lane="free-624",
            pane_id="%624",
            nonce="nonce-624",
            harness="claude",
            repo="/repo/free-624",
            server_id="server-a",
            session_id="$624",
            command="claude.exe",
            task_id="as624-fix",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/624",
            source_ref="624",
            summary="#624 fix; worktree=/var/folders/xx/T//ad-624-fix-99",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-624"],
            status_marker=None,
            # the ledger row holds the RESOLVED, single-separator spelling --
            # what dispatch.sh's own `pwd -P` write produces.
            worktree_path="/private/var/folders/xx/T/ad-624-fix-99",
        )

        for queried_path, label in (
            ("/private/var/folders/xx/T/ad-624-fix-99", "resolved, single separator (matches the row as-is)"),
            ("/private/var/folders/xx/T//ad-624-fix-99", "resolved, doubled separator"),
            ("/var/folders/xx/T/ad-624-fix-99", "unresolved, single separator"),
            ("/var/folders/xx/T//ad-624-fix-99", "unresolved, doubled separator (the hook's own old literal shape)"),
        ):
            found = self.ledger.get_task_for_worktree(queried_path)
            self.assertIsNotNone(found, f"expected a match for {label}: {queried_path!r}")
            self.assertEqual("as624-fix", found["id"], f"wrong task for {label}: {queried_path!r}")

    def test_get_task_for_worktree_still_refuses_an_undispatched_path_after_normalizing(self):
        """agent-supervisor#624's own verification bar, second direction:
        normalizing the comparison must not turn into a guess. A path that
        merely resolves to the same shape as a real row, but is not
        actually that row, must still read as unknown -- this is #562's
        whole purpose, and an overly-aggressive normalization would defeat
        it more thoroughly than the bug being fixed."""
        self.ledger.record_dispatch(
            lane="free-624b",
            pane_id="%624b",
            nonce="nonce-624b",
            harness="claude",
            repo="/repo/free-624b",
            server_id="server-a",
            session_id="$624b",
            command="claude.exe",
            task_id="as624-real",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/624",
            source_ref="624",
            summary="#624 real dispatch",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-624b"],
            status_marker=None,
            worktree_path="/private/var/folders/xx/T/ad-624-real-1",
        )
        # Same directory, both spellings resolve the same, but a DIFFERENT
        # worktree that was never dispatched.
        self.assertIsNone(
            self.ledger.get_task_for_worktree("/var/folders/xx/T//ad-624-never-dispatched-2")
        )

    def test_get_task_for_worktree_still_refuses_a_null_worktree_path_row(self):
        """agent-supervisor#624's third verification case: a task row that
        exists but never had its worktree_path recorded (still '', same as
        the pre-#117 rows `test_get_task_for_worktree_unknown_for_a_blank_
        path` covers) must stay refused after normalization -- an empty
        path must never come out matching whatever this process's own
        cwd happens to be, so `normalize_worktree_path` special-cases
        blank input explicitly rather than running it through the
        general prefix-rewrite (#632's fix-pass moved this off
        `os.path.realpath`, whose own blank-input behavior -- resolving
        to the current directory -- was the original hazard this same
        case existed to catch)."""
        self.ledger.record_dispatch(
            lane="free-624c",
            pane_id="%624c",
            nonce="nonce-624c",
            harness="claude",
            repo="/repo/free-624c",
            server_id="server-a",
            session_id="$624c",
            command="claude.exe",
            task_id="as624-no-worktree",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/624",
            source_ref="624",
            summary="#624 dispatched with no worktree column recorded",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-624c"],
            status_marker=None,
            worktree_path="",
        )
        self.assertIsNone(self.ledger.get_task_for_worktree(""))
        self.assertIsNone(self.ledger.get_task_for_worktree(os.getcwd()))

    def test_get_task_for_worktree_never_returns_a_review_task(self):
        """agent-supervisor#76's rule holds here too: a review task must
        never stand in as the author, even if (implausibly) it shared a
        worktree path with the real one."""
        self.ledger.record_dispatch(
            lane="free-4",
            pane_id="%4",
            nonce="nonce-review",
            harness="claude",
            repo="/repo/free-4",
            server_id="server-a",
            session_id="$4",
            command="claude.exe",
            task_id="as101-review-as114",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/101",
            source_ref="101",
            summary="#101 review PR #114",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-4"],
            status_marker=None,
            worktree_path="/tmp/ad-101-review-as114-7",
        )
        self.assertIsNone(self.ledger.get_task_for_worktree("/tmp/ad-101-review-as114-7"))

    def test_get_contributor_tasks_for_pr_includes_every_non_review_pull_scoped_task(self):
        """agent-supervisor#308 item 2, resolution path five: a task
        dispatched DIRECTLY against a PR number (`--pr <N>`/`--reviews-pr
        <N>`, `source_kind='pull'`) must be found by a later review of the
        SAME PR even when it shares no issue and never had its own worktree
        checked out on the PR's real head branch -- the #302 shape, where
        two `--pr 302` fix-pass tasks sat unconsulted in exactly this table
        while #302's review refused for six hours."""
        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-3",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="as302-fix1",
            source_kind="pull",
            source_url="https://github.com/jonhill90/agent-supervisor/pull/302",
            source_ref="302",
            summary="fix pass on PR #302",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3", "pr: 302"],
            status_marker=None,
        )
        # `source_tasks` allows only one OPEN pull-kind row per PR at a time
        # (agent-supervisor#169) -- complete the fix-pass first, the same
        # sequence #302 actually went through, before dispatching its review.
        self.ledger.complete("as302-fix1", b"done", pane_nonce="nonce-3")
        self.ledger.record_dispatch(
            lane="free-4",
            pane_id="%4",
            nonce="nonce-4",
            harness="claude",
            repo="/repo/free-4",
            server_id="server-a",
            session_id="$4",
            command="claude.exe",
            task_id="as302-review149",
            source_kind="pull",
            source_url="https://github.com/jonhill90/agent-supervisor/pull/302",
            source_ref="302",
            summary="review PR #302",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-4", "pr: 302"],
            status_marker=None,
        )

        contributors = self.ledger.get_contributor_tasks_for_pr("302")
        lanes = {row["lane"] for row in contributors}
        self.assertEqual({"free-3"}, lanes, "only the non-review pull-scoped task is a contributor")
        task_ids = {row["id"] for row in contributors}
        self.assertNotIn("as302-review149", task_ids, "a review task is never a contributor")

        # An int and its string spelling are the same PR.
        self.assertEqual({"free-3"}, {row["lane"] for row in self.ledger.get_contributor_tasks_for_pr(302)})

    def test_get_contributor_tasks_for_pr_unknown_pr_is_empty(self):
        self.assertEqual([], self.ledger.get_contributor_tasks_for_pr("no-such-pr"))

    def _dispatch_pull_scoped(self, *, lane, pane_id, nonce, session_id, task_id, summary, is_review, pr="640"):
        self.ledger.record_dispatch(
            lane=lane,
            pane_id=pane_id,
            nonce=nonce,
            harness="claude",
            repo=f"/repo/{lane}",
            server_id="server-a",
            session_id=session_id,
            command="claude.exe",
            task_id=task_id,
            source_kind="pull",
            source_url=f"https://github.com/jonhill90/agent-supervisor/pull/{pr}",
            source_ref=pr,
            summary=summary,
            source_state="OPEN",
            evidence=[f"claimed by dispatch.sh for lane {lane}", f"pr: {pr}"],
            status_marker=None,
            is_review=is_review,
        )
        # `one_open_pull_per_source_ref` (agent-supervisor#169) allows only
        # ONE open pull-kind row per PR at a time, and every case in this
        # class dispatches several tasks against the same `pr` in a row --
        # closing each one immediately mirrors #302's own real sequence
        # (`test_get_contributor_tasks_for_pr_includes_every_non_review_
        # pull_scoped_task`, above) and `get_contributor_tasks_for_pr` is
        # documented as "unfiltered by status", so completing here does not
        # weaken what any of these tests actually check.
        self.ledger.complete(task_id, b"done", pane_nonce=nonce)

    def test_get_contributor_tasks_for_pr_excludes_a_recorded_review_whatever_it_is_named(self):
        """agent-supervisor#640: once `is_review=1` is RECORDED (what
        `dispatch.sh --reviews-pr` now does), exclusion no longer depends on
        `_task_looks_like_review`'s regex at all -- so it holds even for the
        exact names that regex silently failed on (`rerev...`,
        `re-review...`, `rereview...`) and for a name with no review-ish
        substring whatsoever."""
        for index, task_id in enumerate(
            ["as637-rerev636", "as1-re-review-640", "as2-rereview640", "as3-totally-unrelated-name"]
        ):
            with self.subTest(task_id=task_id):
                lane = f"free-r{index}"
                self._dispatch_pull_scoped(
                    lane=lane, pane_id=f"%{index}", nonce=f"nonce-r{index}", session_id=f"$r{index}",
                    task_id=task_id, summary="dispatch summary", is_review=1,
                )
                lanes = {row["lane"] for row in self.ledger.get_contributor_tasks_for_pr("640")}
                self.assertNotIn(lane, lanes, f"{task_id} must be excluded once is_review=1 is recorded")

    def test_get_contributor_tasks_for_pr_includes_a_recorded_fix_pass_even_named_like_a_review(self):
        """The overshoot direction agent-supervisor#640's own verification
        bar names explicitly: a `--pr` fix-pass recorded `is_review=0` must
        stay a contributor even when its name would trip the regex fallback
        (`revamp-parser`, `reverse-index` both match `_task_looks_like_review`
        directly -- see that method's own docstring)."""
        for index, task_id in enumerate(["as1-revamp-parser", "as2-reverse-index"]):
            with self.subTest(task_id=task_id):
                lane = f"free-f{index}"
                self._dispatch_pull_scoped(
                    lane=lane, pane_id=f"%f{index}", nonce=f"nonce-f{index}", session_id=f"$f{index}",
                    task_id=task_id, summary="dispatch summary", is_review=0,
                )
                lanes = {row["lane"] for row in self.ledger.get_contributor_tasks_for_pr("640")}
                self.assertIn(lane, lanes, f"{task_id} must stay a contributor once is_review=0 is recorded")

    def test_get_contributor_tasks_for_pr_falls_back_to_the_regex_when_is_review_was_never_recorded(self):
        """A row written before `is_review` existed reads `NULL` --
        `get_contributor_tasks_for_pr` must resolve it exactly as it did
        before this column existed (agent-supervisor#640's verification bar
        3), including the regex's own known blind spot: `rerev284` is NOT
        excluded here, because nothing ever recorded the fact and the
        fallback regex misses it -- this test pins that (unfixed, on
        purpose) behaviour rather than asserting it is correct."""
        self._dispatch_pull_scoped(
            lane="free-legacy", pane_id="%legacy", nonce="nonce-legacy", session_id="$legacy",
            task_id="Skills266-rerev284", summary="dispatch summary", is_review=None,
        )
        lanes = {row["lane"] for row in self.ledger.get_contributor_tasks_for_pr("640")}
        self.assertIn("free-legacy", lanes, "unrecorded is_review still falls back to the (imperfect) regex")

    def test_record_pr_for_task_round_trips_through_get_task_for_pr_number(self):
        """agent-supervisor#308 item 1: the explicit "task X's own work
        opened PR N" record, written after the fact for an issue-keyed
        dispatch that had no PR number yet when it started."""
        self.ledger.record_dispatch(
            lane="free-3",
            pane_id="%3",
            nonce="nonce-3",
            harness="claude",
            repo="/repo/free-3",
            server_id="server-a",
            session_id="$3",
            command="claude.exe",
            task_id="as308-original",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/308",
            source_ref="308",
            summary="#308 original fix",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3"],
            status_marker=None,
        )
        self.assertIsNone(self.ledger.get_task_for_pr_number(repo="acme/agent-supervisor", pr_number="316"))

        self.ledger.record_pr_for_task(task_id="as308-original", repo="acme/agent-supervisor", pr_number="316")
        found = self.ledger.get_task_for_pr_number(repo="acme/agent-supervisor", pr_number="316")
        self.assertIsNotNone(found)
        self.assertEqual("free-3", found["lane"])
        self.assertEqual("as308-original", found["id"])

        # An int and its string spelling are the same PR.
        self.assertEqual(
            "as308-original",
            self.ledger.get_task_for_pr_number(repo="acme/agent-supervisor", pr_number=316)["id"],
        )

        # A second call for the same (repo, pr_number) CORRECTS, not
        # duplicates -- an operator re-running lane-done.sh's best-effort
        # write must not raise a primary-key conflict.
        self.ledger.record_pr_for_task(task_id="as308-original", repo="acme/agent-supervisor", pr_number="316")

    def test_record_pr_for_task_refuses_an_unknown_task(self):
        with self.assertRaises(ValueError):
            self.ledger.record_pr_for_task(task_id="no-such-task", repo="acme/agent-supervisor", pr_number="316")

    def test_mark_pr_external_round_trips_through_get_pr_external(self):
        """agent-supervisor#308 item 3: "no lane contributed" as a
        first-class, recordable fact distinct from "unresolvable" -- the
        #316/#301/#300 shape, a PR authored outside the lane system
        entirely."""
        self.assertIsNone(self.ledger.get_pr_external(repo="acme/agent-supervisor", pr_number="316"))

        self.ledger.mark_pr_external(
            repo="acme/agent-supervisor", pr_number="316", note="authored directly, no lane ever dispatched",
            chain_verified=True,
        )
        found = self.ledger.get_pr_external(repo="acme/agent-supervisor", pr_number="316")
        self.assertIsNotNone(found)
        self.assertEqual("authored directly, no lane ever dispatched", found["note"])

        # A second marking corrects (idempotent), not duplicates.
        self.ledger.mark_pr_external(
            repo="acme/agent-supervisor", pr_number="316", note="updated note", chain_verified=True
        )
        found = self.ledger.get_pr_external(repo="acme/agent-supervisor", pr_number="316")
        self.assertEqual("updated note", found["note"])

        # A different, never-marked PR stays unknown -- marking is per-PR,
        # not a global switch.
        self.assertIsNone(self.ledger.get_pr_external(repo="acme/agent-supervisor", pr_number="301"))

    def test_get_task_for_worktree_include_reviews_answers_a_reviewing_lanes_own_worktree(self):
        """agent-supervisor#212: a REVIEWING lane confirming its own
        identity (AGENTS.md invariant 10, before stamping `Review-Lane:`)
        asks a different question than `dispatch.sh --reviews-pr` --
        "which task is THIS worktree" rather than "who authored this PR".
        The default (exercised by the test just above) correctly refuses to
        answer for a review task; `include_reviews=True` must answer for
        the exact same row when the question is self-identification, not
        authorship -- #212's review measured `known:false` for a row the
        ledger actually had, because no caller asked this version of the
        question yet."""
        self.ledger.record_dispatch(
            lane="free-6",
            pane_id="%6",
            nonce="nonce-rev212",
            harness="claude",
            repo="/repo/free-6",
            server_id="server-a",
            session_id="$6",
            command="claude.exe",
            task_id="as211-rev212",
            source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/211",
            source_ref="211",
            summary="#211 review PR #212",
            source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-6"],
            status_marker=None,
            worktree_path="/tmp/ad-211-rev212-9",
        )

        # Unchanged default: still refuses, same as the test above.
        self.assertIsNone(self.ledger.get_task_for_worktree("/tmp/ad-211-rev212-9"))

        found = self.ledger.get_task_for_worktree("/tmp/ad-211-rev212-9", include_reviews=True)

        self.assertIsNotNone(found)
        self.assertEqual("free-6", found["lane"])
        self.assertEqual("as211-rev212", found["id"])

    def test_mark_pr_external_refuses_a_pr_with_an_explicit_authorship_record(self):
        """agent-supervisor#308 item 3 / #321's own review, item 5: the
        laundering gate. `mark_pr_external` must not accept a caller's word
        alone -- it independently refuses when the ledger already records
        `record_pr_for_task`'s explicit "task X opened PR N" fact, the most
        direct evidence this method can check with no external process
        (`gh`/`git`, which only the shell wrapper `mark-pr-external.sh`
        reaches). Otherwise the contributing lane itself could call this and
        erase its own record, then have any lane -- including itself --
        review the PR it just laundered."""
        self.ledger.record_dispatch(
            lane="free-3", pane_id="%3", nonce="nonce-3", harness="claude", repo="/repo/free-3",
            server_id="server-a", session_id="$3", command="claude.exe",
            task_id="as308-original", source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/308",
            source_ref="308", summary="#308 original fix", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3"], status_marker=None,
        )
        self.ledger.record_pr_for_task(task_id="as308-original", repo="acme/agent-supervisor", pr_number="316")

        with self.assertRaises(ValueError) as caught:
            self.ledger.mark_pr_external(
                repo="acme/agent-supervisor", pr_number="316", note="trying to launder my own PR",
                chain_verified=True,
            )
        self.assertIn("as308-original", str(caught.exception))
        self.assertIn("free-3", str(caught.exception))
        self.assertIsNone(
            self.ledger.get_pr_external(repo="acme/agent-supervisor", pr_number="316"),
            "the refused call must not have written the row",
        )

    def test_mark_pr_external_refuses_a_pr_with_a_pull_scoped_contributor(self):
        """Same gate, the other ledger-only source it can check without
        `gh`/`git`: a task dispatched DIRECTLY against this PR
        (`source_kind='pull'`, `get_contributor_tasks_for_pr`) -- the #302
        shape. A review or fix-pass task dispatched with `--pr <N>` is a
        real, structured contributor record; marking that PR external must
        not be allowed to erase it."""
        self.ledger.record_dispatch(
            lane="free-3", pane_id="%3", nonce="nonce-3", harness="claude", repo="/repo/free-3",
            server_id="server-a", session_id="$3", command="claude.exe",
            task_id="as302-fix1", source_kind="pull",
            source_url="https://github.com/jonhill90/agent-supervisor/pull/302",
            source_ref="302", summary="fix pass on PR #302", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3", "pr: 302"], status_marker=None,
        )

        with self.assertRaises(ValueError) as caught:
            self.ledger.mark_pr_external(
                repo="acme/agent-supervisor", pr_number="302", note="trying to launder my own PR",
                chain_verified=True,
            )
        self.assertIn("as302-fix1", str(caught.exception))
        self.assertIsNone(self.ledger.get_pr_external(repo="acme/agent-supervisor", pr_number="302"))

    def test_mark_pr_external_bypass_via_cli_direct_call_is_refused(self):
        """PR #331 review, finding 2: `mark_pr_external`'s own backstop only
        checks two of the five resolution paths (`get_task_for_pr_number`,
        `get_contributor_tasks_for_pr`) -- it never consults issue-linkage,
        which needs `gh` and so cannot live here. `record_pr_for_task` is
        only written by `lane-done.sh` at completion, so an ORDINARY,
        still-in-progress, issue-scoped task (the most common dispatch
        shape) has no PR-keyed row yet and no pull-scoped `source_tasks` row
        either -- neither backstop check can see it. Before this fix, a lane
        calling `python3 cli.py mark-pr-external` directly (bypassing
        `mark-pr-external.sh` and its `gh`-based issue-linkage check
        entirely) sailed straight through for exactly this shape, reproduced
        in the #331 review. `chain_verified` now gates this regardless of
        contributor shape -- a direct call that never went through the
        exhaustive chain has no way to claim it did.
        """
        self.ledger.record_dispatch(
            lane="t:2", pane_id="%2", nonce="nonce-t2", harness="claude", repo="/repo/t2",
            server_id="server-a", session_id="$2", command="claude.exe",
            task_id="ad40-fix", source_kind="issue",
            source_url="https://github.com/acme/agent-dotfiles/issues/40",
            source_ref="40", summary="genuine fix for #40", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane t:2"], status_marker=None,
        )
        # Neither backstop path can see this contributor: no record_pr_for_task
        # row (only lane-done.sh writes that, at completion) and no
        # source_kind='pull' row (this task was dispatched by issue, not by
        # --pr). The bug this test guards against is proven by the fact that
        # BOTH checks below pass -- the gap is real, not a setup mistake.
        self.assertIsNone(self.ledger.get_task_for_pr_number(repo="acme/agent-dotfiles", pr_number="500"))
        self.assertEqual([], self.ledger.get_contributor_tasks_for_pr("500"))

        proc = subprocess.run(
            [
                sys.executable, str(SUPERVISOR_DIR / "cli.py"),
                "--state-dir", self.tempdir.name,
                "mark-pr-external", "--repo", "acme/agent-dotfiles", "--pr", "500",
                "--note", "bypass attempt -- direct cli.py call, no chain run",
            ],
            capture_output=True, text=True, timeout=30,
        )
        self.assertNotEqual(0, proc.returncode, proc.stdout + proc.stderr)
        self.assertIn("chain_verified", proc.stdout + proc.stderr)
        self.assertIsNone(
            self.ledger.get_pr_external(repo="acme/agent-dotfiles", pr_number="500"),
            "the bypass attempt must not have written the row",
        )

    def test_mark_lane_held_makes_a_free_lane_read_occupied(self):
        """agent-dotfiles#188 finding 1: this is what closes the window a
        rolled-back `record_dispatch` used to leave open -- a lane the ledger
        already had registered free must not go on reading free just because
        the write that was supposed to claim it failed."""
        self.assertTrue(self.ledger.lane_available("app-review"))
        held = self.ledger.mark_lane_held("app-review", note="record_dispatch failed for task review-870")
        self.assertEqual("created", held["status"])
        self.assertEqual("app-review", held["lane"])
        self.assertFalse(self.ledger.lane_available("app-review"))

    def test_mark_lane_held_on_an_unknown_lane_is_a_no_op(self):
        """Nothing to close: an unregistered lane already reads `None`
        (unknown), and unknown is not free -- `dispatch.sh`'s `lane-free`
        never offers it. Writing a placeholder task here would need a
        `lanes` row that does not exist and violate the FK."""
        self.assertIsNone(self.ledger.lane_available("never-registered"))
        self.assertIsNone(self.ledger.mark_lane_held("never-registered", note="unused"))
        self.assertIsNone(self.ledger.lane_available("never-registered"))

    def test_mark_lane_held_on_an_already_occupied_lane_is_a_no_op(self):
        """The lane is already exactly as unavailable as this method would
        make it -- the one_open_task_per_lane index refuses the second
        placeholder, and that refusal is fine: it means something else
        already guarantees the property this method exists for."""
        self.assign()
        self.assertFalse(self.ledger.lane_available("app-review"))
        self.assertIsNone(self.ledger.mark_lane_held("app-review", note="unused"))
        self.assertFalse(self.ledger.lane_available("app-review"))

    def test_record_dispatch_failure_leaves_the_lane_held_not_free(self):
        """The mutation this guards against, end to end: seed a lane already
        free, force `record_dispatch` to fail deep inside its transaction
        (the same `assign` collision #144's own atomicity test uses), and
        confirm the failure does not leave the pre-existing free row intact.
        Without `mark_lane_held` wired into the caller, this goes red -- the
        rollback restores exactly the free row asserted at the top."""
        self.assertTrue(self.ledger.lane_available("app-review"))
        self.seed_source("collide-870")
        self.ledger.assign(
            task_id="collide-870", lane="app-review", pane_nonce="nonce-22-a", summary="already here"
        )
        self.ledger.cancel_open_task("app-review", abandoned=True)
        self.assertTrue(self.ledger.lane_available("app-review"))
        # A second dispatch racing the first tries to reuse "collide-870" for
        # a DIFFERENT lane -- `_assign_tx` refuses it, `record_dispatch`
        # rolls back every write it had made for "app-review" so far, and
        # the pre-existing free row for "app-review" survives the rollback
        # untouched, exactly the #188 finding 1 scenario.
        with self.assertRaisesRegex(ValueError, "already exists with different assignment"):
            self.ledger.record_dispatch(
                lane="app-review",
                pane_id="%22",
                nonce="nonce-22-b",
                harness="codex",
                repo="/repo/app",
                server_id="server-a",
                session_id="$4",
                command="codex",
                task_id="collide-870",
                source_kind="issue",
                source_url="https://github.com/jonhill90/agent-dotfiles/issues/870",
                source_ref="870",
                summary="a different task body entirely",
                source_state="OPEN",
                evidence=["claimed by dispatch.sh for lane app-review"],
                status_marker=None,
            )
        self.assertTrue(self.ledger.lane_available("app-review"))  # the bug, absent the fix
        self.ledger.mark_lane_held("app-review", note="record_dispatch failed for task collide-870")
        self.assertFalse(self.ledger.lane_available("app-review"))  # closed

    def test_idle_attention_is_level_triggered_until_task_disposition(self):
        self.assign()
        self.ledger.mark_delivery_pending("review-870", pane_nonce="nonce-22-a")
        self.ledger.mark_delivered("review-870", pane_nonce="nonce-22-a")
        self.ledger.accept("review-870", pane_nonce="nonce-22-a")
        event = self.ledger.observe_idle("app-review", pane_nonce="nonce-22-a")
        self.assertEqual("attention:review-870", event["key"])
        self.ledger.mark_notified([event["key"]], retry_after=60)
        self.assertEqual([], self.ledger.events_due(now=1_059))
        self.assertEqual([event["key"]], [item["key"] for item in self.ledger.events_due(now=1_060)])
        with self.assertRaisesRegex(ValueError, "task disposition"):
            self.ledger.ack([event["key"]])

        self.ledger.complete("review-870", b"# Result\n\nDone.\n", pane_nonce="nonce-22-a")
        self.ledger.ack([event["key"]])
        self.assertEqual("acked", self.ledger.get_event(event["key"])["status"])

    def test_failed_component_collection_does_not_advance_baseline(self):
        first = self.ledger.record_component("github", snapshot=b"head-a\n", healthy=True)
        self.assertEqual(hashlib.sha256(b"head-a\n").hexdigest(), first["snapshot_sha256"])
        failed = self.ledger.record_component("github", healthy=False, error="timeout")
        self.assertEqual(first["snapshot_sha256"], failed["snapshot_sha256"])
        recovered = self.ledger.record_component("github", snapshot=b"head-b\n", healthy=True)
        self.assertNotEqual(first["snapshot_sha256"], recovered["snapshot_sha256"])

    def test_snapshot_changes_emit_one_bounded_diff_event(self):
        self.assertIsNone(self.ledger.record_snapshot("github", b"pr=870 pending\n"))
        self.assertIsNone(self.ledger.record_snapshot("github", b"pr=870 pending\n"))
        event = self.ledger.record_snapshot("github", b"pr=870 success\n")
        self.assertEqual("sensor", event["type"])
        self.assertTrue(event["key"].startswith("sensor:github:"))
        payload = Path(event["payload_path"]).read_text()
        self.assertIn("-pr=870 pending", payload)
        self.assertIn("+pr=870 success", payload)
        repeated = self.ledger.record_snapshot("github", b"pr=870 success\n")
        self.assertIsNone(repeated)
        self.assertEqual(1, len(self.ledger.list_events(event_type="sensor")))

        large = b"x" * (80 * 1024)
        truncated = self.ledger.record_snapshot("github", large)
        bounded = Path(truncated["payload_path"]).read_bytes()
        self.assertLessEqual(len(bounded), 64 * 1024)
        self.assertIn(b"[DIFF TRUNCATED]", bounded)

    def test_codex_and_claude_lanes_share_schema_but_keep_adapter_identity(self):
        self.ledger.register_lane(
            lane="infra-claude",
            pane_id="%8",
            nonce="nonce-8-a",
            harness="claude",
            repo="/repo/hill90",
            server_id="server-a",
            session_id="$4",
            command="claude.exe",
        )
        codex_lane = self.ledger.get_lane("app-review")
        claude_lane = self.ledger.get_lane("infra-claude")
        self.assertEqual(set(codex_lane), set(claude_lane))
        self.assertEqual("codex", codex_lane["harness"])
        self.assertEqual("claude", claude_lane["harness"])
        self.assertNotEqual(codex_lane["nonce"], claude_lane["nonce"])

    def test_copilot_acp_is_a_registerable_harness(self):
        """Red: register_lane's Python-level check and the lanes table's CHECK
        constraint both still hard-code ('codex', 'claude') -- a copilot-acp
        lane cannot be registered at all, so ACPTransport has no lane to
        dispatch through."""
        lane = self.ledger.register_lane(
            lane="copilot-worker",
            pane_id="session-1",
            nonce="nonce-acp",
            harness="copilot-acp",
            repo="/repo/app",
            server_id="acp",
            session_id="session-1",
            command="copilot",
        )
        self.assertEqual("copilot-acp", lane["harness"])
        self.assertEqual("copilot-acp", self.ledger.get_lane("copilot-worker")["harness"])

    def test_concurrent_assignments_leave_exactly_one_open_task(self):
        self.seed_source("race-a", summary="Task race-a")
        self.seed_source("race-b", summary="Task race-b")

        def assign(task_id):
            ledger = Ledger(Path(self.tempdir.name), clock=self.clock)
            try:
                ledger.assign(
                    task_id=task_id,
                    lane="app-review",
                    pane_nonce="nonce-22-a",
                    summary=f"Task {task_id}",
                )
                return "created"
            except ValueError as error:
                return str(error)

        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            outcomes = list(pool.map(assign, ("race-a", "race-b")))
        self.assertEqual(1, outcomes.count("created"))
        self.assertEqual(1, sum("outstanding task" in outcome for outcome in outcomes))
        open_tasks = [task for task in self.ledger.list_tasks() if task["status"] not in ("complete", "failed", "cancelled")]
        self.assertEqual(1, len(open_tasks))


