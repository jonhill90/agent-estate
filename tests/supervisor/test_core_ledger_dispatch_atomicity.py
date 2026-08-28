import sys
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from tests.supervisor.test_core_helpers import LedgerTestBase  # noqa: E402


class DispatchAtomicityTest(LedgerTestBase):
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
