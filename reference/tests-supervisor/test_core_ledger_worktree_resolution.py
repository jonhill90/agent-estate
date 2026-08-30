import os
import sys
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from tests.supervisor.test_core_helpers import LedgerTestBase  # noqa: E402


class WorktreeResolutionTest(LedgerTestBase):
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
