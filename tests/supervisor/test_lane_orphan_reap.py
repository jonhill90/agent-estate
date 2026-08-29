"""agent-estate#800: a lane's background descendants outlive its own turn,
reparented to `init`, with nobody left to signal them. This suite proves
`lane_orphan_reap.LaneOrphanReaper` in both directions the brief demanded:

1. A descendant of a COMPLETED lane, reparented to init -> reaped.
2. A descendant of a LIVE lane, identically shaped -> untouched.

Both mutation cases spawn a REAL process and confirm reparenting the same
way agent-estate#800's own investigation did (`ps -o ppid=`), rather than
asserting it. Every kill is confirmed by re-checking the pid afterward
(`kill -0`) -- never trusting a return code alone, the exact lesson #800's
own corrected comment states: "Signal 15 was ignored by these processes; 9
was needed. Whatever you send, verify the process is gone rather than
trusting the signal."

`TmuxServerExclusionTest` at the bottom proves the agent-estate#802 fix
pass: a tmux SERVER process for a live session -- reparented to `init` as
part of normal daemonization, its own argv naming a terminal task's
worktree path -- must never be a reap candidate, even though it looks
identical to a genuine orphan by the argv+ppid signal alone. Mirrors the
live-lane mutation test's own rigor: a real `tmux new-session -d` server,
not a fake, confirmed still alive (via `tmux list-sessions`) afterward.
"""

import os
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
from lane_orphan_reap import (  # noqa: E402
    LaneOrphanReaper,
    cwd_matches_worktree,
    find_candidate_pids,
    is_tmux_server_process,
    parse_ps_lines,
)


def _pid_alive(pid):
    try:
        subprocess.run(["kill", "-0", str(pid)], check=True, capture_output=True)
        return True
    except subprocess.CalledProcessError:
        return False


def _spawn_orphan(worktree_dir):
    """Spawns a real, long-lived process whose own argv names `worktree_dir`
    and whose cwd IS `worktree_dir`, then reparents it to `init` the same
    way a lane's background descendant does: the immediate parent (a
    throwaway subshell) exits on its own the instant the child is
    backgrounded, leaving the child orphaned with nobody watching it.

    Uses `yes <worktree_dir>` rather than a bare `sleep` because a bare
    `sleep`'s own argv never names the path -- this needs a process that
    LOOKS like the real orphans #800 found (path visible in `ps ... args`),
    not merely one that outlives its parent.
    """
    subprocess.run(
        ["bash", "-c", 'cd "$0" && ( yes "$0" >/dev/null & )', worktree_dir],
        check=True,
    )
    for _ in range(20):
        result = subprocess.run(
            ["pgrep", "-f", f"yes {worktree_dir}"], capture_output=True, text=True
        )
        pids = [int(p) for p in result.stdout.split()]
        if pids:
            return pids[0]
        time.sleep(0.1)
    raise RuntimeError("orphan process never appeared")


class PureFunctionTest(unittest.TestCase):
    def test_parse_ps_lines_skips_unparseable(self):
        output = "  PID  PPID ARGS\n123 1 sleep 45\nnot-a-line\n456 999 yes /tmp/x\n"
        rows = parse_ps_lines(output)
        self.assertEqual(rows, [{"pid": 123, "ppid": 1, "args": "sleep 45"}, {"pid": 456, "ppid": 999, "args": "yes /tmp/x"}])

    def test_find_candidate_pids_requires_ppid_1_and_path(self):
        rows = [
            {"pid": 1, "ppid": 1, "args": "yes /tmp/ad-live-1"},
            {"pid": 2, "ppid": 500, "args": "yes /tmp/ad-live-1"},  # live parent, not orphaned
            {"pid": 3, "ppid": 1, "args": "yes /tmp/other-worktree"},  # different path
        ]
        self.assertEqual(find_candidate_pids(rows, "/tmp/ad-live-1"), [1])

    def test_find_candidate_pids_blank_path_matches_nothing(self):
        rows = [{"pid": 1, "ppid": 1, "args": "anything at all"}]
        self.assertEqual(find_candidate_pids(rows, ""), [])

    def test_find_candidate_pids_excludes_tmux_server_process(self):
        """agent-estate#802: shaped exactly like the PR's own live-host
        sample (`tmux new-session ... -c <worktree_path>`, ppid=1) -- the
        review's own reproduction, as a synthetic row. A genuinely orphaned
        non-tmux descendant of the SAME worktree must still be caught."""
        rows = [
            {"pid": 9273, "ppid": 1, "args": "tmux new-session -d -s revtest -c /tmp/ad-649-finish649-80092"},
            {"pid": 9274, "ppid": 1, "args": "yes /tmp/ad-649-finish649-80092"},
        ]
        self.assertEqual(find_candidate_pids(rows, "/tmp/ad-649-finish649-80092"), [9274])

    def test_is_tmux_server_process_checks_argv0_not_a_substring(self):
        self.assertTrue(is_tmux_server_process("tmux new-session -d -s x -c /tmp/y"))
        self.assertTrue(is_tmux_server_process("/opt/homebrew/bin/tmux new-session -d -s x -c /tmp/y"))
        # A legitimate orphaned descendant that merely MENTIONS tmux later
        # in its own command line must still read as reapable -- this
        # excludes the process that IS a tmux server, not every process
        # that talks about one.
        self.assertFalse(is_tmux_server_process('bash -c "echo tmux new-session" /tmp/y'))
        self.assertFalse(is_tmux_server_process(""))


class RealOrphanProcessTest(unittest.TestCase):
    """Exercises `cwd_matches_worktree` and the reaping mechanics against a
    real, reparented OS process -- not a fake."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.worktree = self.tempdir.name
        subprocess.run(["git", "init", "-q", self.worktree], check=True)
        self.pid = _spawn_orphan(self.worktree)
        self.addCleanup(self._force_cleanup)

    def _force_cleanup(self):
        subprocess.run(["kill", "-9", str(self.pid)], capture_output=True)

    def test_spawned_process_is_confirmed_reparented(self):
        result = subprocess.run(["ps", "-o", "ppid=", "-p", str(self.pid)], capture_output=True, text=True)
        self.assertEqual(result.stdout.strip(), "1", "the fixture must actually reparent to init, or this suite proves nothing")

    def test_cwd_matches_worktree_true_for_the_real_orphan(self):
        self.assertTrue(cwd_matches_worktree(self.pid, self.worktree))

    def test_cwd_matches_worktree_false_for_a_different_path(self):
        with tempfile.TemporaryDirectory() as other:
            self.assertFalse(cwd_matches_worktree(self.pid, other))


class FakeLedger:
    """Answers `get_open_task_for_lane` from an in-memory map -- the only
    `Ledger` method `LaneOrphanReaper` calls that this suite needs to control
    independently of a task's own `status` field (a real `Ledger` is used
    for that instead, in `LaneOrphanReaperIntegrationTest` below, since it
    is cheap and exercises the real schema)."""

    def __init__(self, open_lanes=()):
        self.open_lanes = set(open_lanes)

    def get_open_task_for_lane(self, lane):
        return {"lane": lane} if lane in self.open_lanes else None


class LaneOrphanReaperTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.worktree = self.tempdir.name
        subprocess.run(["git", "init", "-q", self.worktree], check=True)

    def _reap(self, task, ledger):
        reaper = LaneOrphanReaper(ledger)
        return reaper.reap_task_orphans(task)

    def test_mutation_1_completed_lane_orphan_is_reaped(self):
        """A descendant of a COMPLETED lane, reparented to init -> reaped."""
        pid = _spawn_orphan(self.worktree)
        self.addCleanup(lambda: subprocess.run(["kill", "-9", str(pid)], capture_output=True))
        task = {"id": "ae800-t1", "lane": "agent-supervisor:9", "status": "complete", "worktree_path": self.worktree}
        report = self._reap(task, FakeLedger(open_lanes=()))
        self.assertEqual(report["outcome"], "reaped")
        self.assertEqual(report["reaped"], [pid])
        self.assertEqual(report["refused"], [])
        self.assertEqual(report["failed"], [])
        # Verify by count/liveness, not by trusting the report -- #800's own
        # lesson ("I reported success anyway because I did not check the
        # count afterwards").
        self.assertFalse(_pid_alive(pid), "the orphan must actually be gone, not merely reported reaped")

    def test_mutation_2_live_lane_orphan_is_untouched(self):
        """A descendant of a LIVE lane, identically shaped -> untouched.
        This is the case that matters (brief's own words): looks IDENTICAL
        to the reaped case (same ppid=1, same argv/cwd shape) but the lane
        occupying it is still live."""
        pid = _spawn_orphan(self.worktree)
        self.addCleanup(lambda: subprocess.run(["kill", "-9", str(pid)], capture_output=True))
        task = {"id": "ae800-t2", "lane": "agent-supervisor:9", "status": "complete", "worktree_path": self.worktree}
        report = self._reap(task, FakeLedger(open_lanes=("agent-supervisor:9",)))
        self.assertEqual(report["outcome"], "lane_live")
        self.assertEqual(report["reaped"], [])
        self.assertTrue(_pid_alive(pid), "a live lane's descendant must never be touched")

    def test_non_terminal_task_is_never_reaped(self):
        pid = _spawn_orphan(self.worktree)
        self.addCleanup(lambda: subprocess.run(["kill", "-9", str(pid)], capture_output=True))
        task = {"id": "ae800-t3", "lane": "agent-supervisor:9", "status": "delivered", "worktree_path": self.worktree}
        report = self._reap(task, FakeLedger())
        self.assertEqual(report["outcome"], "not_terminal")
        self.assertTrue(_pid_alive(pid))

    def test_dirty_worktree_is_never_reaped(self):
        (Path(self.worktree) / "dirty.txt").write_text("uncommitted\n")
        pid = _spawn_orphan(self.worktree)
        self.addCleanup(lambda: subprocess.run(["kill", "-9", str(pid)], capture_output=True))
        task = {"id": "ae800-t4", "lane": "agent-supervisor:9", "status": "complete", "worktree_path": self.worktree}
        report = self._reap(task, FakeLedger())
        self.assertEqual(report["outcome"], "worktree_dirty")
        self.assertTrue(_pid_alive(pid))

    def test_blank_worktree_path_is_never_reaped(self):
        task = {"id": "ae800-t5", "lane": "agent-supervisor:9", "status": "complete", "worktree_path": ""}
        report = self._reap(task, FakeLedger())
        self.assertEqual(report["outcome"], "no_worktree_path")

    def test_unrelated_process_mentioning_the_path_is_refused_not_reaped(self):
        """A process whose argv happens to name the worktree path but whose
        cwd is genuinely elsewhere is the exact over-match agent-estate#800
        warned about -- confirmed refused, not reaped."""
        elsewhere = tempfile.mkdtemp()
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", elsewhere]))
        proc = subprocess.Popen(
            ["bash", "-c", f'cd "{elsewhere}" && ( yes "{self.worktree}" >/dev/null & )']
        )
        proc.wait()
        pid = None
        for _ in range(20):
            result = subprocess.run(["pgrep", "-f", f"yes {self.worktree}"], capture_output=True, text=True)
            pids = [int(p) for p in result.stdout.split()]
            if pids:
                pid = pids[0]
                break
            time.sleep(0.1)
        self.assertIsNotNone(pid, "fixture process never appeared")
        self.addCleanup(lambda: subprocess.run(["kill", "-9", str(pid)], capture_output=True))
        task = {"id": "ae800-t6", "lane": "agent-supervisor:9", "status": "complete", "worktree_path": self.worktree}
        report = self._reap(task, FakeLedger())
        self.assertEqual(report["outcome"], "refused")
        self.assertEqual(report["reaped"], [])
        self.assertIn(pid, report["refused"])
        self.assertTrue(_pid_alive(pid), "a confirmed cwd mismatch must never be reaped")


class LaneOrphanReaperLedgerIntegrationTest(unittest.TestCase):
    """Same mutation pair, but against a real `Ledger` (real sqlite schema,
    real `get_open_task_for_lane`) instead of the fake above -- proves the
    integration point `reconcile_lane_completions.py` will actually call
    behaves the same way end to end."""

    def setUp(self):
        self.state_tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.state_tempdir.cleanup)
        self.ledger = Ledger(Path(self.state_tempdir.name), clock=lambda: 1_000)
        self.worktree_tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.worktree_tempdir.cleanup)
        self.worktree = self.worktree_tempdir.name
        subprocess.run(["git", "init", "-q", self.worktree], check=True)

    def dispatch(self, task_id, *, lane, worktree_path):
        return self.ledger.record_dispatch(
            lane=lane, pane_id="%9", nonce="nonce-9", harness="claude",
            repo="/repo/agent-estate", server_id="server-a", session_id="$9", command="claude",
            task_id=task_id, source_kind="issue",
            source_url="https://github.com/jonhill90/agent-estate/issues/800",
            source_ref="800", summary="issue #800", source_state="OPEN",
            evidence=[f"claimed by dispatch.sh for lane {lane}", "issues: 800"],
            status_marker=None, worktree_path=worktree_path, accepted=True,
        )

    def test_completed_task_orphan_reaped(self):
        pid = _spawn_orphan(self.worktree)
        self.addCleanup(lambda: subprocess.run(["kill", "-9", str(pid)], capture_output=True))
        task_id = "ae800-real1"
        self.dispatch(task_id, lane="agent-supervisor:8", worktree_path=self.worktree)
        task = self.ledger.get_task(task_id)
        self.ledger.complete(task_id, b"done", pane_nonce=task["pane_nonce"])
        completed_task = self.ledger.get_task(task_id)
        reaper = LaneOrphanReaper(self.ledger)
        report = reaper.reap_task_orphans(completed_task)
        self.assertEqual(report["outcome"], "reaped")
        self.assertFalse(_pid_alive(pid))

    def test_lane_redispatched_before_sweep_is_untouched(self):
        """The lane finished task 1's worktree, but has ALREADY been
        redispatched a second, still-open task by the time this sweep runs
        -- `get_open_task_for_lane` must report it live, and the first
        task's own leftover orphan must be left alone."""
        pid = _spawn_orphan(self.worktree)
        self.addCleanup(lambda: subprocess.run(["kill", "-9", str(pid)], capture_output=True))
        first_id, second_id = "ae800-real2", "ae800-real3"
        self.dispatch(first_id, lane="agent-supervisor:8", worktree_path=self.worktree)
        first_task = self.ledger.get_task(first_id)
        self.ledger.complete(first_id, b"done", pane_nonce=first_task["pane_nonce"])
        completed_first = self.ledger.get_task(first_id)
        # Same lane, redispatched to a second (still-open) task before the
        # orphan sweep gets to it.
        self.dispatch(second_id, lane="agent-supervisor:8", worktree_path="/tmp/does-not-matter")
        reaper = LaneOrphanReaper(self.ledger)
        report = reaper.reap_task_orphans(completed_first)
        self.assertEqual(report["outcome"], "lane_live")
        self.assertTrue(_pid_alive(pid))


def _isolated_tmux_env(tmux_tmpdir):
    """Same isolation discipline as test_reconcile_lane_completions.py's own
    `_isolated_env` helper: a private TMUX_TMPDIR, TMUX unset so nothing
    here can ever address the ambient/default socket (CLAUDE.md invariant
    4). Duplicated locally rather than imported -- this file and that one
    are independent test modules and neither imports test helpers from the
    other."""
    env = dict(os.environ)
    env["TMUX_TMPDIR"] = tmux_tmpdir
    env.pop("TMUX", None)
    return env


@unittest.skipUnless(shutil.which("tmux") is not None, "tmux is not installed")
class TmuxServerExclusionTest(unittest.TestCase):
    """agent-estate#802's review reproduced this exact shape against a live
    host and it is not a corner case: a `tmux new-session -d -c
    <worktree_path>` server is reparented to `init` the instant it
    daemonizes -- as part of normal, correct operation -- with its own
    argv naming the worktree path a lane was dispatched into. That is
    indistinguishable from a genuine orphan by ppid+argv alone once the
    task recorded against that worktree goes terminal, which says nothing
    about whether the tmux session itself has been torn down. This proves
    the exclusion holds against a REAL server process, isolated on its own
    socket (never the default one -- CLAUDE.md invariant 4), not a fake.
    """

    def setUp(self):
        self.tmux_tmpdir = tempfile.mkdtemp(prefix="ae802-tmux-")
        self.env = _isolated_tmux_env(self.tmux_tmpdir)
        self.addCleanup(self._cleanup)
        self.worktree = tempfile.mkdtemp(prefix="ae802-worktree-")
        self.addCleanup(lambda: shutil.rmtree(self.worktree, ignore_errors=True))
        subprocess.run(["git", "init", "-q", self.worktree], check=True)
        self.session = "ae802-revtest"
        subprocess.run(
            ["tmux", "new-session", "-d", "-s", self.session, "-c", self.worktree],
            env=self.env, check=True, capture_output=True, timeout=10,
        )
        self.server_pid = self._find_server_pid()
        self.assertIsNotNone(
            self.server_pid,
            "the fixture's own tmux server pid was not found by ps -- this test proves nothing without it",
        )

    def _cleanup(self):
        subprocess.run(["tmux", "kill-server"], env=self.env, capture_output=True, timeout=10)
        shutil.rmtree(self.tmux_tmpdir, ignore_errors=True)

    def _find_server_pid(self):
        ps_out = subprocess.run(["ps", "-axo", "pid,ppid,args"], capture_output=True, text=True, check=True).stdout
        for row in parse_ps_lines(ps_out):
            if row["ppid"] == 1 and is_tmux_server_process(row["args"]) and self.worktree in row["args"]:
                return row["pid"]
        return None

    def test_server_is_reparented_to_init_by_normal_daemonization(self):
        """Confirms the premise rather than merely asserting it: a `tmux
        new-session -d` server really is ppid==1 immediately, as ordinary
        daemonization -- not a crash or a manual reparenting trick like
        this suite's other fixtures use to simulate an actual orphan."""
        result = subprocess.run(["ps", "-o", "ppid=", "-p", str(self.server_pid)], capture_output=True, text=True)
        self.assertEqual(result.stdout.strip(), "1")

    def test_tmux_server_process_is_excluded_from_candidates(self):
        ps_out = subprocess.run(["ps", "-axo", "pid,ppid,args"], capture_output=True, text=True, check=True).stdout
        candidates = find_candidate_pids(parse_ps_lines(ps_out), self.worktree)
        self.assertNotIn(
            self.server_pid, candidates,
            "a live tmux server for a recognized session must never be a reap candidate",
        )

    def test_reap_task_orphans_leaves_the_live_session_untouched(self):
        """End to end, mirroring `test_mutation_2_live_lane_orphan_is_untouched`'s
        rigor: task terminal, lane not live, worktree clean -- every OTHER
        gate says reap, and this must still refuse because the only
        candidate found IS the tmux server itself. A fix that simply
        stopped reaping everything would pass this alone; paired with
        `test_mutation_1_completed_lane_orphan_is_reaped` above (a
        genuinely orphaned non-tmux process is still reaped), it proves
        the exclusion is specific, not a blanket retreat."""
        task = {"id": "ae802-t1", "lane": "agent-supervisor:9", "status": "complete", "worktree_path": self.worktree}
        reaper = LaneOrphanReaper(FakeLedger())
        report = reaper.reap_task_orphans(task)
        self.assertNotEqual(report["outcome"], "reaped")
        self.assertNotIn(self.server_pid, report["reaped"])
        # Confirmed live via a real tmux query against the isolated
        # server, not by trusting the report dict alone.
        list_result = subprocess.run(
            ["tmux", "list-sessions"], env=self.env, capture_output=True, text=True, timeout=10
        )
        self.assertIn(self.session, list_result.stdout)


if __name__ == "__main__":
    unittest.main()
