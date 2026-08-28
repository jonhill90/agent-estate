import sys
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from tests.supervisor.test_core_helpers import LedgerTestBase  # noqa: E402


class PrContributorsTest(LedgerTestBase):
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
