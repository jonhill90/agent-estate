"""agent-estate#804: a lane's WORKTREE outlives its own terminal task --
`#802` already reaps the PROCESSES a lane leaves running once its task goes
terminal; nothing reaped the disposable checkout directory itself. This
suite proves `lane_worktree_reap.LaneWorktreeReaper` in every direction the
brief demanded:

1. Terminal task, clean, merged worktree -> reaped.
2. Terminal task, but DIRTY / UNPUSHED / a live process inside -> survives.
   Constructed as three separate cases, since each is a distinct guard.
3. Non-terminal task -> untouched.

Every case drives the REAL `worktree.sh reap` (no stub, no mock of the
shell layer) against a REAL git repo with a REAL bare origin, the same
"prove it against real code" discipline `test_lane_orphan_reap.py` and
`test_worktree.sh` already hold their own guard chains to. `--no-github` is
passed throughout: none of these fixtures have a real GitHub remote for
`gh pr list` to query, and the content-diff predicate alone is already
sufficient to prove every case below (agent-supervisor#682's own PR-record
fallback is `worktree.sh`'s own concern, already tested in
`test_worktree_reap.sh`, not this module's).
"""

import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
from lane_worktree_reap import LaneWorktreeReaper, default_runner  # noqa: E402

WORKTREE_SH = str(SUPERVISOR_DIR / "worktree.sh")

# `_gc_is_live`'s age floor (agent-supervisor#478) defaults to 3600s so a
# lane between tool calls is never mistaken for abandoned -- every fixture
# in this module is seconds old by construction, so exercising the rest of
# `_gc_is_live` (rather than every case "surviving" for the wrong reason,
# merely being too young) needs the same override `test_worktree_reap.sh`
# and `test_worktree.sh`'s own #478 fixtures already use. `0`, not `1`: a
# fixture built and reaped inside the same wall-clock second measures
# age=0, and `1 -lt 1` is false in bash the same as `0 -lt 1` is true --
# `age < GC_MIN_AGE_SECONDS` with a `1` floor flaked exactly on that
# boundary (measured directly, ~15% of runs) without a `sleep` this suite
# has no need for otherwise. Set at import time, inherited by every
# `worktree.sh reap` subprocess this module spawns.
os.environ["WORKTREE_GC_MIN_AGE_SECONDS"] = "0"


def _no_github_runner(argv):
    """Wraps `default_runner` to always pass `--no-github` to `worktree.sh
    reap` -- none of this suite's fixtures have a real GitHub remote, and
    every case here is already decided by the content-diff predicate alone
    (see this module's own docstring)."""
    if len(argv) >= 3 and argv[1] == WORKTREE_SH and argv[2] == "reap":
        argv = [argv[0], argv[1], argv[2], "--no-github"] + argv[3:]
    return default_runner(argv)


class FakeLedger:
    """Answers `get_open_task_for_lane` from an in-memory map -- the only
    `Ledger` method `LaneWorktreeReaper` calls that this suite needs to
    control independently of a task's own `status` field (a real `Ledger`
    is used for that instead, in `LedgerIntegrationTest` below)."""

    def __init__(self, open_lanes=()):
        self.open_lanes = set(open_lanes)

    def get_open_task_for_lane(self, lane):
        return {"lane": lane} if lane in self.open_lanes else None


class RepoFixture:
    """A bare origin + a clone, standing in for the real shared checkout --
    the same minimal shape `test_worktree.sh` builds by hand, factored into
    one helper since this suite needs it repeatedly."""

    def __init__(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.root = Path(self.tempdir.name)
        self.origin = self.root / "origin.git"
        self.repo = self.root / "repo"
        subprocess.run(["git", "init", "-q", "--bare", str(self.origin)], check=True)
        subprocess.run(["git", "clone", "-q", str(self.origin), str(self.repo)], check=True)
        self._git("config", "user.email", "test@example.com")
        self._git("config", "user.name", "Test")
        self._git("checkout", "-q", "-b", "main")
        (self.repo / "file.txt").write_text("one\n")
        self._git("add", "file.txt")
        self._git("commit", "-q", "-m", "initial")
        self._git("push", "-q", "-u", "origin", "main")
        # `new` (via `install_dispatch_origin_guard`, agent-supervisor#562)
        # installs a pre-push hook on every worktree it hands out, refusing
        # a push unless the ledger has a dispatch record for that
        # worktree's own path. Irrelevant to this suite's own concern (the
        # worktree reap guard chain) -- stubbed the same way
        # `test_worktree.sh` stubs it for its own unrelated fixtures.
        bin_dir = self.root / "bin"
        bin_dir.mkdir()
        stub = bin_dir / "allow-python3"
        stub.write_text('#!/bin/bash\necho \'{"known":true,"lane":"stub:0","path":"stub","task":"stub"}\'\n')
        stub.chmod(0o755)
        self._prior_agent_python_bin = os.environ.get("AGENT_PYTHON_BIN")
        os.environ["AGENT_PYTHON_BIN"] = str(stub)

    def cleanup(self):
        if self._prior_agent_python_bin is None:
            os.environ.pop("AGENT_PYTHON_BIN", None)
        else:
            os.environ["AGENT_PYTHON_BIN"] = self._prior_agent_python_bin
        self.tempdir.cleanup()

    def _git(self, *args, cwd=None):
        subprocess.run(["git", "-C", str(cwd or self.repo), *args], check=True, capture_output=True, text=True)

    def new_worktree(self, slug):
        out = subprocess.run(
            ["bash", WORKTREE_SH, "new", slug, str(self.repo), "origin/main"],
            check=True, capture_output=True, text=True,
        ).stdout.strip()
        return Path(out)

    def merge_worktree(self, worktree, slug, *, message):
        self._git("-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-q", "-am", message, cwd=worktree)
        self._git("push", "-q", "origin", f"lane/{slug}", cwd=worktree)
        self._git("fetch", "-q", "origin")
        self._git("merge", "-q", "--no-edit", f"origin/lane/{slug}")
        self._git("push", "-q", "origin", "main")
        self._git("fetch", "-q", "origin", cwd=worktree)


class LaneWorktreeReaperTest(unittest.TestCase):
    def setUp(self):
        self.fixture = RepoFixture()
        self.addCleanup(self.fixture.cleanup)
        self.reaper = LaneWorktreeReaper(FakeLedger(), runner=_no_github_runner, worktree_bin=WORKTREE_SH)

    def test_mutation_1_terminal_clean_merged_worktree_is_reaped(self):
        """Terminal task, clean, merged worktree -> reaped."""
        worktree = self.fixture.new_worktree("804-t1")
        (worktree / "file.txt").write_text("merged change\n", encoding="utf-8")
        self.fixture.merge_worktree(worktree, "804-t1", message="merged work")
        task = {"id": "ae804-t1", "lane": "agent-supervisor:1", "status": "complete", "worktree_path": str(worktree)}
        report = self.reaper.reap_task_worktree(task)
        self.assertEqual(report["outcome"], "reaped")
        self.assertFalse(worktree.is_dir(), "the worktree must actually be gone, not merely reported reaped")

    def test_mutation_2a_dirty_worktree_survives(self):
        """Terminal task, branch merged, but the tree is DIRTY -> survives."""
        worktree = self.fixture.new_worktree("804-t2a")
        (worktree / "file.txt").write_text("dirty-but-merged base\n", encoding="utf-8")
        self.fixture.merge_worktree(worktree, "804-t2a", message="dirty-but-merged base")
        with (worktree / "file.txt").open("a", encoding="utf-8") as handle:
            handle.write("uncommitted on top of a merged branch\n")
        task = {"id": "ae804-t2a", "lane": "agent-supervisor:1", "status": "complete", "worktree_path": str(worktree)}
        report = self.reaper.reap_task_worktree(task)
        self.assertEqual(report["outcome"], "refused")
        self.assertTrue(worktree.is_dir(), "a dirty worktree must survive even though its branch is merged")
        self.assertIn("uncommitted", (worktree / "file.txt").read_text(encoding="utf-8"))

    def test_mutation_2b_unpushed_unmerged_worktree_survives(self):
        """Terminal task, branch UNMERGED and never pushed -> survives."""
        worktree = self.fixture.new_worktree("804-t2b")
        with (worktree / "file.txt").open("a", encoding="utf-8") as handle:
            handle.write("unmerged, unpushed change\n")
        subprocess.run(
            ["git", "-C", str(worktree), "-c", "user.email=test@example.com", "-c", "user.name=Test",
             "commit", "-q", "-am", "unmerged, unpushed work"],
            check=True,
        )
        task = {"id": "ae804-t2b", "lane": "agent-supervisor:1", "status": "complete", "worktree_path": str(worktree)}
        report = self.reaper.reap_task_worktree(task)
        self.assertEqual(report["outcome"], "refused")
        self.assertTrue(worktree.is_dir(), "an unmerged, unpushed worktree must survive")
        remote_refs = subprocess.run(
            ["git", "-C", str(self.fixture.origin), "show-ref", "--verify", "--quiet", "refs/heads/lane/804-t2b"],
        )
        self.assertNotEqual(
            remote_refs.returncode, 0,
            "sanity: this branch must genuinely never have reached the remote, or this is not testing 'unpushed'",
        )

    def test_mutation_2c_worktree_with_a_live_process_inside_survives(self):
        """Terminal task, branch merged, tree clean, but a real process's
        cwd is inside it -> survives. Reuses `worktree.sh`'s own liveness
        guard (`_gc_is_live`'s process-cwd check) rather than reimplementing
        it here."""
        worktree = self.fixture.new_worktree("804-t2c")
        (worktree / "file.txt").write_text("live-but-merged work\n", encoding="utf-8")
        self.fixture.merge_worktree(worktree, "804-t2c", message="live-but-merged work")
        proc = subprocess.Popen(["sleep", "30"], cwd=str(worktree))
        self.addCleanup(proc.kill)
        try:
            reaper = LaneWorktreeReaper(
                FakeLedger(), runner=_no_github_runner, worktree_bin=WORKTREE_SH,
            )
            task = {
                "id": "ae804-t2c", "lane": "agent-supervisor:1",
                "status": "complete", "worktree_path": str(worktree),
            }
            report = reaper.reap_task_worktree(task)
        finally:
            proc.kill()
            proc.wait()
        self.assertEqual(report["outcome"], "refused")
        self.assertTrue(worktree.is_dir(), "a worktree with a live process inside must survive")

    def test_non_terminal_task_is_never_reaped(self):
        worktree = self.fixture.new_worktree("804-t3")
        task = {"id": "ae804-t3", "lane": "agent-supervisor:1", "status": "delivered", "worktree_path": str(worktree)}
        report = self.reaper.reap_task_worktree(task)
        self.assertEqual(report["outcome"], "not_terminal")
        self.assertTrue(worktree.is_dir())

    def test_blank_worktree_path_is_never_reaped(self):
        task = {"id": "ae804-t4", "lane": "agent-supervisor:1", "status": "complete", "worktree_path": ""}
        report = self.reaper.reap_task_worktree(task)
        self.assertEqual(report["outcome"], "no_worktree_path")

    def test_missing_worktree_directory_is_reported_not_erred(self):
        task = {
            "id": "ae804-t5", "lane": "agent-supervisor:1", "status": "complete",
            "worktree_path": str(self.fixture.root / "already-gone"),
        }
        report = self.reaper.reap_task_worktree(task)
        self.assertEqual(report["outcome"], "worktree_missing")

    def test_lane_redispatched_before_sweep_is_untouched(self):
        """The lane finished this task's worktree, but has ALREADY been
        redispatched a second, still-open task by the time this sweep runs
        -- the worktree must be left alone even though ITS OWN branch is
        merged and clean, same shape `LaneOrphanReaper`'s identical check
        protects for a lane's processes."""
        worktree = self.fixture.new_worktree("804-t6")
        (worktree / "file.txt").write_text("merged change\n", encoding="utf-8")
        self.fixture.merge_worktree(worktree, "804-t6", message="merged work")
        reaper = LaneWorktreeReaper(
            FakeLedger(open_lanes=("agent-supervisor:1",)), runner=_no_github_runner, worktree_bin=WORKTREE_SH,
        )
        task = {"id": "ae804-t6", "lane": "agent-supervisor:1", "status": "complete", "worktree_path": str(worktree)}
        report = reaper.reap_task_worktree(task)
        self.assertEqual(report["outcome"], "lane_live")
        self.assertTrue(worktree.is_dir(), "a lane already redispatched must never have its old worktree reaped")


class LedgerIntegrationTest(unittest.TestCase):
    """Same mutation pair, but against a real `Ledger` -- proves the
    integration point `reconcile_lane_completions.py` actually calls
    behaves the same way end to end, mirroring `lane_orphan_reap.py`'s own
    `LaneOrphanReaperLedgerIntegrationTest`."""

    def setUp(self):
        self.state_tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.state_tempdir.cleanup)
        self.ledger = Ledger(Path(self.state_tempdir.name), clock=lambda: 1_000)
        self.fixture = RepoFixture()
        self.addCleanup(self.fixture.cleanup)

    def dispatch(self, task_id, *, lane, worktree_path):
        return self.ledger.record_dispatch(
            lane=lane, pane_id="%9", nonce="nonce-9", harness="claude",
            repo=str(self.fixture.repo), server_id="server-a", session_id="$9", command="claude",
            task_id=task_id, source_kind="issue",
            source_url="https://github.com/jonhill90/agent-estate/issues/804",
            source_ref="804", summary="issue #804", source_state="OPEN",
            evidence=[f"claimed by dispatch.sh for lane {lane}", "issues: 804"],
            status_marker=None, worktree_path=worktree_path, accepted=True,
        )

    def test_completed_task_worktree_reaped(self):
        worktree = self.fixture.new_worktree("804-real1")
        (worktree / "file.txt").write_text("merged change\n", encoding="utf-8")
        self.fixture.merge_worktree(worktree, "804-real1", message="merged work")
        task_id = "ae804-real1"
        self.dispatch(task_id, lane="agent-supervisor:8", worktree_path=str(worktree))
        task = self.ledger.get_task(task_id)
        self.ledger.complete(task_id, b"done", pane_nonce=task["pane_nonce"])
        completed_task = self.ledger.get_task(task_id)
        reaper = LaneWorktreeReaper(self.ledger, runner=_no_github_runner, worktree_bin=WORKTREE_SH)
        report = reaper.reap_task_worktree(completed_task)
        self.assertEqual(report["outcome"], "reaped")
        self.assertFalse(worktree.is_dir())

    def test_lane_redispatched_before_sweep_is_untouched(self):
        worktree = self.fixture.new_worktree("804-real2")
        (worktree / "file.txt").write_text("merged change\n", encoding="utf-8")
        self.fixture.merge_worktree(worktree, "804-real2", message="merged work")
        first_id, second_id = "ae804-real2", "ae804-real3"
        self.dispatch(first_id, lane="agent-supervisor:8", worktree_path=str(worktree))
        first_task = self.ledger.get_task(first_id)
        self.ledger.complete(first_id, b"done", pane_nonce=first_task["pane_nonce"])
        completed_first = self.ledger.get_task(first_id)
        # Same lane, redispatched to a second (still-open) task before the
        # worktree sweep gets to it.
        self.dispatch(second_id, lane="agent-supervisor:8", worktree_path="/tmp/does-not-matter")
        reaper = LaneWorktreeReaper(self.ledger, runner=_no_github_runner, worktree_bin=WORKTREE_SH)
        report = reaper.reap_task_worktree(completed_first)
        self.assertEqual(report["outcome"], "lane_live")
        self.assertTrue(worktree.is_dir())


if __name__ == "__main__":
    unittest.main()
