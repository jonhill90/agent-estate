import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger, normalize_worktree_path  # noqa: E402
from reconcile_worktree_paths import WorktreePathReconciler  # noqa: E402


def _run_git(*args, cwd):
    return subprocess.run(
        ["git", *args], cwd=cwd, check=True, capture_output=True, text=True
    ).stdout.strip()


def make_git_worktree(root):
    """A REAL git repo on disk -- this sweep's own disk-corroboration check
    (`_worktree_exists`, `git reflog`, `git merge-base --is-ancestor`) is
    exactly what is under test, so a fake filesystem/fake git would test
    nothing but the fake."""
    _run_git("init", "-q", cwd=root)
    _run_git("config", "user.email", "test@example.com", cwd=root)
    _run_git("config", "user.name", "Test", cwd=root)
    (Path(root) / "README.md").write_text("hello\n")
    _run_git("add", "README.md", cwd=root)
    _run_git("commit", "-q", "-m", "initial", cwd=root)
    return _run_git("rev-parse", "HEAD", cwd=root)


class FakeGhRunner:
    """Answers `gh pr view` from an in-memory map and delegates every other
    command (the `git` calls this sweep also issues) to the real
    subprocess -- see `make_git_worktree`'s own docstring for why the git
    side is real rather than faked."""

    def __init__(self, pr_heads):
        self.pr_heads = pr_heads  # {(repo, pr_number): head_sha or Exception}
        self.calls = []

    def __call__(self, command):
        self.calls.append(command)
        if command[0] == "gh":
            # ["gh", "pr", "view", <pr_number>, "--repo", <repo>, "--json", "headRefOid"]
            pr_number, repo = command[3], command[5]
            key = (repo, pr_number)
            if key not in self.pr_heads:
                raise RuntimeError(f"gh pr view unavailable for {key}")
            answer = self.pr_heads[key]
            if isinstance(answer, Exception):
                raise answer
            return json.dumps({"headRefOid": answer})
        return subprocess.run(command, check=True, capture_output=True, text=True).stdout


class WorktreePathReconcilerTest(unittest.TestCase):
    def setUp(self):
        self.ledger_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.ledger_dir.cleanup)
        self.ledger = Ledger(Path(self.ledger_dir.name), clock=lambda: 1_000)
        self.ledger.register_lane(
            lane="cp-worker", pane_id="cp-1", nonce="nonce-1", harness="claude",
            repo="/repo/cp-worker", server_id="claude-print", session_id="sess-1", command="claude",
        )

    def seed_complete_task(self, task_id, *, summary, worktree_path=""):
        self.ledger.reconstruct_task(
            task_id=task_id, source_kind="issue",
            source_url=f"https://github.com/jonhill90/agent-supervisor/issues/1",
            source_ref="1", summary=summary, source_state="OPEN",
            status="created", evidence=[], status_marker=None,
        )
        self.ledger.assign(
            task_id=task_id, lane="cp-worker", pane_nonce="nonce-1",
            summary=summary, worktree_path=worktree_path,
        )
        self.ledger.mark_delivery_pending(task_id, pane_nonce="nonce-1")
        self.ledger.mark_delivered(task_id, pane_nonce="nonce-1")
        return self.ledger.complete(task_id, b"# Result\ndone\n", pane_nonce="nonce-1")

    def test_backfills_a_row_whose_worktree_and_pr_link_both_corroborate(self):
        worktree_dir = tempfile.TemporaryDirectory()
        self.addCleanup(worktree_dir.cleanup)
        head_sha = make_git_worktree(worktree_dir.name)

        summary = (
            f"Read /tmp/wt/brief-611.md and do exactly what it says. That file is your "
            f"complete brief. Do all of your work in the worktree at {worktree_dir.name} -- "
            "it is yours, already branched; never work in the shared checkout at /repo."
        )
        self.seed_complete_task("as611-fix", summary=summary)
        self.ledger.record_pr_for_task(task_id="as611-fix", repo="jonhill90/agent-supervisor", pr_number=611)

        runner = FakeGhRunner({("jonhill90/agent-supervisor", "611"): head_sha})
        report = WorktreePathReconciler(self.ledger, runner=runner).sweep()

        self.assertEqual(["as611-fix"], [item["task"] for item in report["backfilled"]])
        self.assertEqual([], report["unresolved"])
        self.assertEqual([], report["errors"])
        # agent-supervisor#624: the sweep now writes the CANONICAL spelling
        # (`normalize_worktree_path`), not the raw text `worktree_dir.name`
        # happened to carry -- on this host `tempfile.TemporaryDirectory`
        # mints paths under `/var/...`, itself a symlink into
        # `/private/var/...`, so this is a real resolve on this platform,
        # not a no-op assertion.
        self.assertEqual(
            normalize_worktree_path(worktree_dir.name),
            self.ledger.get_task("as611-fix")["worktree_path"],
        )

    def test_dry_run_reports_without_writing(self):
        worktree_dir = tempfile.TemporaryDirectory()
        self.addCleanup(worktree_dir.cleanup)
        head_sha = make_git_worktree(worktree_dir.name)
        summary = f"Do all of your work in the worktree at {worktree_dir.name} -- go."
        self.seed_complete_task("as611-dry", summary=summary)
        self.ledger.record_pr_for_task(task_id="as611-dry", repo="jonhill90/agent-supervisor", pr_number=612)

        runner = FakeGhRunner({("jonhill90/agent-supervisor", "612"): head_sha})
        report = WorktreePathReconciler(self.ledger, runner=runner).sweep(dry_run=True)

        self.assertEqual(["as611-dry"], [item["task"] for item in report["would_backfill"]])
        self.assertEqual([], report["backfilled"])
        self.assertEqual("", self.ledger.get_task("as611-dry")["worktree_path"])

    def test_second_sweep_after_a_real_backfill_writes_nothing(self):
        """Idempotence: `list_complete_tasks_missing_worktree_path` already
        excludes a row this sweep just fixed, so a re-run performs zero
        additional writes without this module needing its own dedup."""
        worktree_dir = tempfile.TemporaryDirectory()
        self.addCleanup(worktree_dir.cleanup)
        head_sha = make_git_worktree(worktree_dir.name)
        summary = f"Do all of your work in the worktree at {worktree_dir.name} -- go."
        self.seed_complete_task("as611-idem", summary=summary)
        self.ledger.record_pr_for_task(task_id="as611-idem", repo="jonhill90/agent-supervisor", pr_number=613)
        runner = FakeGhRunner({("jonhill90/agent-supervisor", "613"): head_sha})

        first = WorktreePathReconciler(self.ledger, runner=runner).sweep()
        self.assertEqual(1, len(first["backfilled"]))

        second = WorktreePathReconciler(self.ledger, runner=runner).sweep()
        self.assertEqual([], second["backfilled"])
        self.assertEqual([], second["would_backfill"])
        self.assertEqual([], second["unresolved"])

    def test_no_pr_link_leaves_the_row_untouched(self):
        """agent-supervisor#611's central refusal: real disk evidence (a
        worktree that genuinely exists) is not enough on its own -- without
        an independently recorded `record_pr_for_task` link there is nothing
        to corroborate the candidate path against, so this must NOT write,
        exactly the posture the issue took toward `as531-as531-fixpass`
        itself."""
        worktree_dir = tempfile.TemporaryDirectory()
        self.addCleanup(worktree_dir.cleanup)
        make_git_worktree(worktree_dir.name)
        summary = f"Do all of your work in the worktree at {worktree_dir.name} -- go."
        self.seed_complete_task("as531-as531-fixpass", summary=summary)
        # Deliberately no record_pr_for_task call.

        report = WorktreePathReconciler(self.ledger, runner=FakeGhRunner({})).sweep()

        self.assertEqual([], report["backfilled"])
        self.assertEqual(1, len(report["unresolved"]))
        self.assertIn("no record_pr_for_task link", report["unresolved"][0]["reason"])
        self.assertEqual("", self.ledger.get_task("as531-as531-fixpass")["worktree_path"])

    def test_worktree_missing_on_disk_leaves_the_row_untouched(self):
        summary = "Do all of your work in the worktree at /no/such/path -- go."
        self.seed_complete_task("as611-gone", summary=summary)
        self.ledger.record_pr_for_task(task_id="as611-gone", repo="jonhill90/agent-supervisor", pr_number=614)

        report = WorktreePathReconciler(self.ledger, runner=FakeGhRunner({})).sweep()

        self.assertEqual([], report["backfilled"])
        self.assertEqual(1, len(report["unresolved"]))
        self.assertIn("not a git worktree on disk", report["unresolved"][0]["reason"])

    def test_no_worktree_path_named_in_summary_leaves_the_row_untouched(self):
        self.seed_complete_task("as611-no-summary", summary="Review the attached artifact.")

        report = WorktreePathReconciler(self.ledger, runner=FakeGhRunner({})).sweep()

        self.assertEqual([], report["backfilled"])
        self.assertEqual(1, len(report["unresolved"]))
        self.assertIn("no worktree path found", report["unresolved"][0]["reason"])

    def test_reflog_with_no_commit_reachable_from_the_pr_leaves_the_row_untouched(self):
        """The corroboration check must actually check reachability, not
        just that a PR link exists at all -- a task's own recorded PR whose
        head shares no history with the candidate worktree must not pass."""
        worktree_dir = tempfile.TemporaryDirectory()
        self.addCleanup(worktree_dir.cleanup)
        make_git_worktree(worktree_dir.name)
        # A well-formed but entirely unrelated commit sha (not present in
        # this worktree's own object database at all) -- merge-base
        # --is-ancestor must fail closed, not raise past this check.
        unrelated_sha = "a" * 40
        summary = f"Do all of your work in the worktree at {worktree_dir.name} -- go."
        self.seed_complete_task("as611-unrelated", summary=summary)
        self.ledger.record_pr_for_task(task_id="as611-unrelated", repo="jonhill90/agent-supervisor", pr_number=615)

        runner = FakeGhRunner({("jonhill90/agent-supervisor", "615"): unrelated_sha})
        report = WorktreePathReconciler(self.ledger, runner=runner).sweep()

        self.assertEqual([], report["backfilled"])
        self.assertEqual(1, len(report["unresolved"]))
        self.assertIn("no commit in", report["unresolved"][0]["reason"])

    def test_a_failed_gh_call_is_reported_as_an_error_not_an_unresolved_guess(self):
        worktree_dir = tempfile.TemporaryDirectory()
        self.addCleanup(worktree_dir.cleanup)
        make_git_worktree(worktree_dir.name)
        summary = f"Do all of your work in the worktree at {worktree_dir.name} -- go."
        self.seed_complete_task("as611-gh-down", summary=summary)
        self.ledger.record_pr_for_task(task_id="as611-gh-down", repo="jonhill90/agent-supervisor", pr_number=616)

        runner = FakeGhRunner({})  # no answer configured -- simulates gh failing
        report = WorktreePathReconciler(self.ledger, runner=runner).sweep()

        self.assertEqual([], report["backfilled"])
        self.assertEqual([], report["unresolved"])
        self.assertEqual(1, len(report["errors"]))

    def test_only_reads_tasks_still_missing_a_worktree_path(self):
        """A task that already carries a `worktree_path` must never be a
        candidate at all -- invariant 1 (do not overwrite the historical
        record), and `Ledger.backfill_task_worktree_path` refuses it too if
        this ever regressed."""
        self.seed_complete_task("as611-already-set", summary="x", worktree_path="/already/set")

        report = WorktreePathReconciler(self.ledger, runner=FakeGhRunner({})).sweep()

        self.assertEqual([], report["backfilled"])
        self.assertEqual([], report["unresolved"])
        self.assertEqual([], report["errors"])


class BackfillTaskWorktreePathTest(unittest.TestCase):
    """`Ledger.backfill_task_worktree_path` directly -- the write primitive
    the sweep above calls, tested in isolation for its own refusals."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.ledger = Ledger(Path(self.tempdir.name), clock=lambda: 1_000)
        self.ledger.register_lane(
            lane="cp-worker", pane_id="cp-1", nonce="nonce-1", harness="claude",
            repo="/repo/cp-worker", server_id="claude-print", session_id="sess-1", command="claude",
        )
        self.ledger.reconstruct_task(
            task_id="t1", source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/1",
            source_ref="1", summary="x", source_state="OPEN",
            status="created", evidence=[], status_marker=None,
        )
        self.ledger.assign(task_id="t1", lane="cp-worker", pane_nonce="nonce-1", summary="x")
        self.ledger.mark_delivery_pending("t1", pane_nonce="nonce-1")
        self.ledger.mark_delivered("t1", pane_nonce="nonce-1")

    def test_refuses_a_task_that_is_not_complete(self):
        with self.assertRaisesRegex(ValueError, "not complete"):
            self.ledger.backfill_task_worktree_path("t1", "/repo/cp-worker")

    def test_refuses_to_overwrite_an_already_populated_worktree_path(self):
        self.ledger.complete("t1", b"done", pane_nonce="nonce-1")
        self.ledger.backfill_task_worktree_path("t1", "/repo/cp-worker")
        with self.assertRaisesRegex(ValueError, "already has a recorded worktree_path"):
            self.ledger.backfill_task_worktree_path("t1", "/repo/cp-worker-2")
        self.assertEqual("/repo/cp-worker", self.ledger.get_task("t1")["worktree_path"])

    def test_refuses_an_unknown_task(self):
        with self.assertRaisesRegex(ValueError, "unknown task"):
            self.ledger.backfill_task_worktree_path("no-such-task", "/repo/x")

    def test_refuses_an_empty_worktree_path(self):
        self.ledger.complete("t1", b"done", pane_nonce="nonce-1")
        with self.assertRaisesRegex(ValueError, "non-empty"):
            self.ledger.backfill_task_worktree_path("t1", "")


if __name__ == "__main__":
    unittest.main()
