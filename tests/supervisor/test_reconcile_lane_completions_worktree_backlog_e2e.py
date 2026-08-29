"""agent-estate#834 end-to-end proof: a task written terminal OUTSIDE any
sweep (mirroring `cli.py record-completion`, `dispatch.sh`'s own suggested
recovery for a lane that finished without signalling) has its worktree
retired by the very next `reconcile-lane-completions` sweep, driven through
the REAL `LaneCompletionReconciler`, the REAL `LaneWorktreeReaper` and the
REAL `worktree.sh reap` guard chain against a real git worktree on disk --
no fakes, no stubs standing in for the removal decision itself.

`test_reconcile_lane_completions.py`'s own `LeakedWorktreeBacklogTest`
proves the WIRING (which tasks reach which reaper, in what order, with a
fake reaper recording calls). This module is that suite's real-worktree
counterpart, the same relationship `test_lane_worktree_reap.py` already
holds to `WorktreeReaperWiringTest` -- reusing that module's own
`RepoFixture`/`_no_github_runner`/isolated-tmux harness rather than
duplicating it, since the guard chain under test here is the identical one
that module already proves in every other direction.
"""

import os
import sys
import tempfile
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))
sys.path.insert(0, str(Path(__file__).resolve().parent))

from core import Ledger  # noqa: E402
from lane_orphan_reap import LaneOrphanReaper  # noqa: E402
from lane_worktree_reap import LaneWorktreeReaper  # noqa: E402
from reconcile_lane_completions import LaneCompletionReconciler  # noqa: E402

# Reuse test_lane_worktree_reap.py's own fixture and tmux harness rather
# than a second copy -- same reasoning that module's own docstring gives
# for building it in the first place.
from test_lane_worktree_reap import (  # noqa: E402
    RepoFixture,
    _no_github_runner,
    setUpModule as _worktree_reap_setUpModule,
    tearDownModule as _worktree_reap_tearDownModule,
)

setUpModule = _worktree_reap_setUpModule
tearDownModule = _worktree_reap_tearDownModule


class RecordCompletionLeakRetiredEndToEndTest(unittest.TestCase):
    """The exact shape `#834` demonstrated live against `ad341-fix341` /
    `ad-341-fix341-7071`: `record_completion` writes `status='complete'`
    directly, never through a sweep -- so before this fix, NOTHING ever
    revisited the worktree it left on disk. Mirrored here with a real git
    worktree and a real ledger, then run through the real production
    reconciler wiring (`orphan_reaper=LaneOrphanReaper(ledger)`,
    `worktree_reaper=LaneWorktreeReaper(ledger, ...)`, the identical
    construction `cli.py reconcile-lane-completions` itself uses)."""

    def setUp(self):
        self.fixture = RepoFixture()
        self.addCleanup(self.fixture.cleanup)
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.ledger = Ledger(Path(self.tempdir.name), clock=lambda: 1_000)

    def _record_completion_style_task(self, task_id, *, lane, worktree_path):
        """Mirrors `cli.py record-completion`: dispatch, then
        `ledger.complete(...)` called directly, never through
        `LaneCompletionReconciler.sweep()`."""
        self.ledger.record_dispatch(
            lane=lane, pane_id="%9", nonce=f"nonce-{task_id}", harness="claude",
            repo=str(self.fixture.repo), server_id="server-a", session_id="$9", command="claude",
            task_id=task_id, source_kind="issue",
            source_url="https://github.com/jonhill90/agent-estate/issues/834",
            source_ref="834", summary="issue #834", source_state="OPEN",
            evidence=[f"claimed by dispatch.sh for lane {lane}", "issues: 834"],
            status_marker=None, accepted=True, worktree_path=str(worktree_path),
        )
        self.ledger.complete(task_id, b"finished without signalling", pane_nonce=f"nonce-{task_id}")

    def test_leaked_worktree_from_record_completion_is_retired_by_the_next_sweep(self):
        """BEFORE: worktree on disk, task already `complete`, nothing has
        ever reaped it (the exact `ad-341-fix341-7071` state). AFTER: one
        real `reconcile-lane-completions`-shaped sweep, and the directory
        is gone."""
        worktree = self.fixture.new_worktree("834-leak")
        (worktree / "file.txt").write_text("merged change\n", encoding="utf-8")
        self.fixture.merge_worktree(worktree, "834-leak", message="merged before record-completion")

        self._record_completion_style_task("ae834-e2e-leak", lane="leak-e2e-session:1", worktree_path=worktree)

        # BEFORE state, as real command output:
        before_status = self.ledger.get_task("ae834-e2e-leak")["status"]
        before_exists = worktree.is_dir()
        self.assertEqual("complete", before_status)
        self.assertTrue(before_exists, "worktree must still be on disk before the fix runs -- the leak itself")

        report = LaneCompletionReconciler(
            self.ledger,
            runner=lambda command: (_ for _ in ()).throw(RuntimeError("no delivered tasks -- lanes.sh must not be called")),
            idle_after=300,
            orphan_reaper=LaneOrphanReaper(self.ledger),
            worktree_reaper=LaneWorktreeReaper(self.ledger, runner=_no_github_runner),
        ).sweep()

        after_exists = worktree.is_dir()
        entries = [e for e in report["worktrees"] if e["task"] == "ae834-e2e-leak"]
        self.assertEqual(1, len(entries))
        self.assertEqual("reaped", entries[0]["outcome"])
        self.assertFalse(after_exists, "the leaked worktree must actually be gone after the sweep, not merely reported reaped")

    def test_leaked_worktree_is_left_alone_while_its_lane_is_genuinely_busy(self):
        """The hard constraint, proven end to end: if the SAME lane has
        since been redispatched (a fresh OPEN task now occupies it -- the
        real `ad341-fix341` specimen's CURRENT state, `agent-dotfiles:2`
        now working `ad342-fix342b`), the leaked worktree from the OLDER
        terminal task must survive untouched."""
        worktree = self.fixture.new_worktree("834-busy")
        (worktree / "file.txt").write_text("merged change\n", encoding="utf-8")
        self.fixture.merge_worktree(worktree, "834-busy", message="merged before record-completion")

        lane = "leak-e2e-session:2"
        self._record_completion_style_task("ae834-e2e-busy", lane=lane, worktree_path=worktree)
        # Redispatch the same lane -- a new OPEN task now occupies it.
        self.ledger.record_dispatch(
            lane=lane, pane_id="%9", nonce="nonce-busy-2", harness="claude",
            repo=str(self.fixture.repo), server_id="server-a", session_id="$9", command="claude",
            task_id="ae834-e2e-busy-2", source_kind="issue",
            source_url="https://github.com/jonhill90/agent-estate/issues/900",
            source_ref="900", summary="issue #900", source_state="OPEN",
            evidence=[f"claimed by dispatch.sh for lane {lane}", "issues: 900"],
            status_marker=None, accepted=True,
        )

        report = LaneCompletionReconciler(
            self.ledger,
            runner=lambda command: (_ for _ in ()).throw(RuntimeError("no delivered tasks -- lanes.sh must not be called")),
            idle_after=300,
            orphan_reaper=LaneOrphanReaper(self.ledger),
            worktree_reaper=LaneWorktreeReaper(self.ledger, runner=_no_github_runner),
        ).sweep()

        entries = [e for e in report["worktrees"] if e["task"] == "ae834-e2e-busy"]
        self.assertEqual(1, len(entries))
        self.assertEqual("lane_live", entries[0]["outcome"])
        self.assertTrue(worktree.is_dir(), "a lane genuinely busy with newer work must never lose its old worktree")


if __name__ == "__main__":
    unittest.main()
