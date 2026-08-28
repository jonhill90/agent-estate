import contextlib
import sys
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from tests.supervisor.test_core_helpers import LedgerTestBase  # noqa: E402


class IssueResolutionTest(LedgerTestBase):
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
