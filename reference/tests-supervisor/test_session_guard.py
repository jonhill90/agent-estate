import subprocess
import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from session_guard import remove_guard  # noqa: E402
from supervisor_view import SupervisorUnavailable  # noqa: E402


def completed(returncode=0, stdout="", stderr=""):
    return subprocess.CompletedProcess([], returncode, stdout=stdout, stderr=stderr)


class FakeTransport:
    def __init__(self, *, exists=True, panes=None):
        self._exists = exists
        self._panes = panes if panes is not None else []

    def session_exists(self, session):
        return self._exists

    def list_panes(self, session):
        return self._panes


def scripted_runner(responses):
    """`responses` maps a command's first two tokens (script/verb) to a
    CompletedProcess, matched by substring against the joined command --
    good enough for these tests' small, distinct command shapes."""

    def runner(command, *, timeout):
        joined = " ".join(str(part) for part in command)
        for needle, result in responses:
            if needle in joined:
                return result
        raise AssertionError(f"no scripted response for: {joined}")

    return runner


SUPERVISED_STATE = completed(stdout='{"session": "work", "state": "supervised"}')
NO_BUSY_LANES = completed(stdout="[]")


class RemoveGuardTest(unittest.TestCase):
    def test_supervised_idle_clean_is_safe(self):
        runner = scripted_runner(
            [
                ("session-state", SUPERVISED_STATE),
                ("lanes.sh", NO_BUSY_LANES),
                ("git -C /wt status", completed(stdout="")),
                ("rev-parse", completed(stdout="origin/main")),
                ("rev-list", completed(stdout="0")),
            ]
        )
        transport = FakeTransport(panes=[{"window_name": "free-1", "path": "/wt"}])
        result = remove_guard("work", transport=transport, runner=runner)
        self.assertTrue(result["safe_to_remove"])
        self.assertEqual([], result["refusals"])

    def test_unsupervised_session_is_refused_and_named(self):
        runner = scripted_runner(
            [
                ("session-state", completed(stdout='{"session": "work", "state": "unknown"}')),
                ("lanes.sh", NO_BUSY_LANES),
            ]
        )
        transport = FakeTransport(panes=[])
        result = remove_guard("work", transport=transport, runner=runner)
        self.assertFalse(result["safe_to_remove"])
        self.assertTrue(any("not supervised" in r for r in result["refusals"]), result["refusals"])

    def test_busy_lane_is_refused_and_named(self):
        runner = scripted_runner(
            [
                ("session-state", SUPERVISED_STATE),
                ("lanes.sh", completed(stdout='[{"name": "free-1", "state": "busy"}]')),
            ]
        )
        transport = FakeTransport(panes=[])
        result = remove_guard("work", transport=transport, runner=runner)
        self.assertFalse(result["safe_to_remove"])
        self.assertEqual(["free-1"], result["busy_lanes"])
        self.assertTrue(any("free-1" in r and "busy" in r for r in result["refusals"]), result["refusals"])

    def test_dirty_worktree_is_refused_and_names_the_path(self):
        runner = scripted_runner(
            [
                ("session-state", SUPERVISED_STATE),
                ("lanes.sh", NO_BUSY_LANES),
                ("git -C /wt status", completed(stdout=" M dirty.py\n")),
            ]
        )
        transport = FakeTransport(panes=[{"window_name": "free-1", "path": "/wt"}])
        result = remove_guard("work", transport=transport, runner=runner)
        self.assertFalse(result["safe_to_remove"])
        self.assertFalse(result["worktrees"][0]["clean"])
        self.assertTrue(any("/wt" in r for r in result["refusals"]), result["refusals"])

    def test_unpushed_worktree_is_refused_and_names_the_path(self):
        runner = scripted_runner(
            [
                ("session-state", SUPERVISED_STATE),
                ("lanes.sh", NO_BUSY_LANES),
                ("git -C /wt status", completed(stdout="")),
                ("rev-parse", completed(stdout="origin/main")),
                ("rev-list", completed(stdout="3")),
            ]
        )
        transport = FakeTransport(panes=[{"window_name": "free-1", "path": "/wt"}])
        result = remove_guard("work", transport=transport, runner=runner)
        self.assertFalse(result["safe_to_remove"])
        self.assertTrue(result["worktrees"][0]["unpushed"])
        self.assertTrue(any("/wt" in r for r in result["refusals"]), result["refusals"])

    def test_undeterminable_git_state_fails_closed_not_clean(self):
        """A `git status` that itself fails (git missing, path gone, a
        timeout) must never be silently treated as clean."""
        runner = scripted_runner(
            [
                ("session-state", SUPERVISED_STATE),
                ("lanes.sh", NO_BUSY_LANES),
                ("git -C /wt status", completed(1, stderr="fatal: not a git repository")),
            ]
        )
        transport = FakeTransport(panes=[{"window_name": "free-1", "path": "/wt"}])
        result = remove_guard("work", transport=transport, runner=runner)
        self.assertFalse(result["safe_to_remove"])
        self.assertIsNone(result["worktrees"][0]["clean"])
        self.assertTrue(
            any("undeterminable" in r for r in result["refusals"]), result["refusals"]
        )

    def test_multiple_failures_are_all_named_not_just_the_first(self):
        runner = scripted_runner(
            [
                ("session-state", completed(stdout='{"session": "work", "state": "unknown"}')),
                ("lanes.sh", completed(stdout='[{"name": "free-1", "state": "busy"}]')),
                ("git -C /wt status", completed(stdout=" M dirty.py\n")),
            ]
        )
        transport = FakeTransport(panes=[{"window_name": "free-1", "path": "/wt"}])
        result = remove_guard("work", transport=transport, runner=runner)
        self.assertFalse(result["safe_to_remove"])
        self.assertEqual(3, len(result["refusals"]), result["refusals"])
        joined = " | ".join(result["refusals"])
        self.assertIn("not supervised", joined)
        self.assertIn("busy", joined)
        self.assertIn("/wt", joined)

    def test_unreadable_lanes_sh_for_an_existing_session_raises(self):
        """This is the one channel that IS a raise: the guard could not even
        read the evidence for a session tmux says exists."""
        runner = scripted_runner(
            [
                ("session-state", SUPERVISED_STATE),
                ("lanes.sh", completed(1, stderr="lanes.sh: unexpected failure")),
            ]
        )
        transport = FakeTransport(exists=True, panes=[])
        with self.assertRaises(SupervisorUnavailable):
            remove_guard("work", transport=transport, runner=runner)

    def test_worktrees_are_deduped_by_path(self):
        runner = scripted_runner(
            [
                ("session-state", SUPERVISED_STATE),
                ("lanes.sh", NO_BUSY_LANES),
                ("git -C /wt status", completed(stdout="")),
                ("rev-parse", completed(stdout="origin/main")),
                ("rev-list", completed(stdout="0")),
            ]
        )
        transport = FakeTransport(
            panes=[{"window_name": "free-1", "path": "/wt"}, {"window_name": "free-2", "path": "/wt"}]
        )
        result = remove_guard("work", transport=transport, runner=runner)
        self.assertEqual(1, len(result["worktrees"]))


if __name__ == "__main__":
    unittest.main()
