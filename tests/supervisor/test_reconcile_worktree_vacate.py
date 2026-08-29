"""agent-estate#825: proves `reconcile_lane_completions.py`'s own
`_vacate_pane_before_reap` step actually frees a finished review lane's
worktree end to end -- against a REAL tmux pane and a REAL git worktree,
never a stand-in for either.

The bug this fixes, traced live on #825: a review lane's shell `cd`s into
its worktree exactly once, at dispatch (`dispatch-send.sh`'s own
`respawn-pane -k -c "$WORKTREE"`), and never leaves it again on its own --
posting a verdict and going idle does not move the pane. So the instant
`reconcile_lane_completions.LaneCompletionReconciler` auto-completed a
review lane's task and asked `LaneWorktreeReaper` to reap its worktree,
`worktree.sh reap`'s own liveness guard (`_gc_is_live`, agent-supervisor#478)
found that lane's own pane still pointing inside it and refused -- correctly,
since the pane really was still there. Measured live: eleven idle panes
across every session held a finished task's worktree open exactly this way.

This suite drives the REAL `sweep()` -- real tmux, a real merged git
worktree, a real `Ledger` and a real `LaneWorktreeReaper` calling the real
`worktree.sh reap` -- and proves both directions:

1. With `_vacate_pane_before_reap` wired in (the shipped behaviour): the
   pane is moved out and the worktree is actually reaped.
2. With that one step disabled (simulating the pre-#825 code): the exact
   same fixture is refused and the worktree survives -- proving this test
   would have failed before the fix, not merely that it passes after it.
"""

import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
from lane_worktree_reap import LaneWorktreeReaper, default_runner  # noqa: E402
from reconcile_lane_completions import LaneCompletionReconciler  # noqa: E402

WORKTREE_SH = str(SUPERVISOR_DIR / "worktree.sh")

# Same override `test_lane_worktree_reap.py` uses and explains at length:
# `_gc_is_live`'s age floor defaults to 3600s so a lane between tool calls
# is never mistaken for abandoned; every fixture here is seconds old by
# construction, so exercising the pane-liveness check specifically (rather
# than every case surviving for the unrelated reason of being "too young")
# needs this override.
os.environ["WORKTREE_GC_MIN_AGE_SECONDS"] = "0"


def _no_github_runner(argv):
    """Same wrapper `test_lane_worktree_reap.py` uses: force `--no-github`
    on every `worktree.sh reap` call, since none of this suite's fixtures
    have a real GitHub remote and the content-diff predicate alone already
    decides every case here."""
    if len(argv) >= 3 and argv[1] == WORKTREE_SH and argv[2] == "reap":
        argv = [argv[0], argv[1], argv[2], "--no-github"] + argv[3:]
    return default_runner(argv)


class RepoFixture:
    """A bare origin + a clone -- the same minimal shape
    `test_lane_worktree_reap.py`'s own `RepoFixture` builds, kept local to
    this file rather than imported cross-module so this suite's tmux/ledger
    setup stays self-contained."""

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
        # `new` (agent-supervisor#562) installs a pre-push hook refusing a
        # push unless the ledger has a dispatch record for that worktree's
        # own path -- irrelevant to this suite, stubbed the same way
        # `test_lane_worktree_reap.py` stubs it.
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


TMUX_AVAILABLE = shutil.which("tmux") is not None


def _isolated_env(tmux_tmpdir):
    """Same isolation discipline every real-tmux test in this suite already
    holds (CLAUDE.md invariant 4): a private TMUX_TMPDIR, TMUX unset, never
    the operator's own default socket."""
    env = dict(os.environ)
    env["TMUX_TMPDIR"] = tmux_tmpdir
    env.pop("TMUX", None)
    return env


class SessionRunner:
    """`lanes.sh --json <session>` answers from an in-memory row (this
    suite needs exactly one lane, read `free`, since that is the only shape
    `LaneCompletionReconciler` will auto-complete from observed pane state);
    every `tmux` and `git` call runs FOR REAL, against the isolated server
    and real worktree this test built -- so the pane this reconciler moves,
    and the worktree it reaps, are the genuine article, not a simulation of
    either."""

    def __init__(self, session, window, env):
        self.session = session
        self.window = window
        self.env = env
        self.calls = []

    def __call__(self, command):
        self.calls.append(command)
        if command[0] in ("tmux", "git"):
            return subprocess.run(
                command, env=self.env, check=True, capture_output=True, text=True, timeout=10
            ).stdout
        session = command[-1]
        if session != self.session:
            raise RuntimeError(f"lanes.sh unavailable for session {session}")
        return f'[{{"window":{self.window},"window_id":"@1","name":"n","command":"claude","state":"free","idle_seconds":400,"model":"sonnet","execution_mode":"tmux"}}]'


@unittest.skipUnless(TMUX_AVAILABLE, "tmux is not installed")
class VacatePaneBeforeReapTest(unittest.TestCase):
    """agent-estate#825: the real `sweep()` -> real `_vacate_pane_before_reap`
    -> real `worktree.sh reap` path, proven both directions."""

    def setUp(self):
        self.fixture = RepoFixture()
        self.addCleanup(self.fixture.cleanup)
        self.tmpdir = tempfile.mkdtemp(prefix="ae825-tmux-")
        self.env = _isolated_env(self.tmpdir)
        self.addCleanup(self._kill_tmux)
        self.state_tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.state_tempdir.cleanup)
        self.ledger = Ledger(Path(self.state_tempdir.name), clock=lambda: 1_000)

    def _kill_tmux(self):
        subprocess.run(["tmux", "kill-server"], env=self.env, capture_output=True, timeout=10)
        shutil.rmtree(self.tmpdir, ignore_errors=True)

    def _build_lane(self, slug, task_id):
        """A real merged, clean worktree; a real tmux pane whose OS-level
        cwd is `cd`'d into it (mirroring `dispatch-send.sh`'s own
        `respawn-pane -k -c "$WORKTREE"`); a real dispatched, terminal-
        eligible task naming that lane and worktree."""
        worktree = self.fixture.new_worktree(slug)
        (worktree / "file.txt").write_text("merged review-lane work\n", encoding="utf-8")
        self.fixture.merge_worktree(worktree, slug, message="merged work")

        session = f"s-{slug}"
        subprocess.run(
            ["tmux", "-f", "/dev/null", "new-session", "-d", "-s", session, "-c", str(worktree)],
            env=self.env, check=True, capture_output=True, text=True,
        )
        panes = subprocess.run(
            ["tmux", "list-panes", "-t", session, "-F", "#{window_index}\t#{pane_id}"],
            env=self.env, check=True, capture_output=True, text=True,
        ).stdout.strip()
        window_index, pane_id = panes.split("\t")
        # Confirm the pane's OS-level cwd is really inside the worktree --
        # the exact precondition #825 traced, not assumed.
        observed = subprocess.run(
            ["tmux", "display-message", "-t", session, "-p", "#{pane_current_path}"],
            env=self.env, check=True, capture_output=True, text=True,
        ).stdout.strip()
        self.assertEqual(str(worktree.resolve()), str(Path(observed).resolve()))

        lane = f"{session}:{window_index}"
        self.ledger.record_dispatch(
            lane=lane, pane_id=pane_id, nonce="nonce-825", harness="claude",
            repo=str(self.fixture.repo), server_id="server-a", session_id="$825", command="claude",
            task_id=task_id, source_kind="pull",
            source_url="https://github.com/jonhill90/agent-estate/pull/825",
            source_ref="825", summary="review of #825", source_state="OPEN",
            evidence=[f"claimed by dispatch.sh for lane {lane}", "issues: 825"],
            status_marker=None, worktree_path=str(worktree), accepted=True,
        )
        return worktree, session, window_index

    def test_direction_1_pane_is_vacated_and_worktree_is_actually_reaped(self):
        """The shipped behaviour: `sweep()` completes the task, moves the
        pane out first, and the worktree it left behind is genuinely gone
        from disk -- not merely reported reaped."""
        worktree, session, window_index = self._build_lane("825-vacate", "ae825-vacate")
        runner = SessionRunner(session, int(window_index), self.env)
        reaper = LaneWorktreeReaper(self.ledger, runner=_no_github_runner, worktree_bin=WORKTREE_SH)

        report = LaneCompletionReconciler(
            self.ledger, runner=runner, idle_after=300, worktree_reaper=reaper,
        ).sweep()

        self.assertEqual(["ae825-vacate"], report["completed"])
        self.assertEqual(1, len(report["worktrees"]))
        self.assertEqual("reaped", report["worktrees"][0]["outcome"], report["worktrees"][0])
        self.assertFalse(worktree.is_dir(), "the worktree must actually be gone, not merely reported reaped")

        pane_path = subprocess.run(
            ["tmux", "display-message", "-t", session, "-p", "#{pane_current_path}"],
            env=self.env, check=True, capture_output=True, text=True,
        ).stdout.strip()
        self.assertNotEqual(
            str(worktree.resolve()), str(Path(pane_path).resolve()),
            "the pane must have been moved out of the worktree before it was reaped",
        )

    def test_direction_2_mutation_without_the_vacate_step_the_refusal_reproduces(self):
        """Mutation check: the SAME fixture, with `_vacate_pane_before_reap`
        disabled -- reproducing exactly the pre-#825 code path. The worktree
        must be REFUSED and survive, proving this suite's own direction-1
        assertions are not vacuously true (they would fail against the old
        behaviour, not just pass against the new one)."""
        worktree, session, window_index = self._build_lane("825-nomove", "ae825-nomove")
        runner = SessionRunner(session, int(window_index), self.env)
        reaper = LaneWorktreeReaper(self.ledger, runner=_no_github_runner, worktree_bin=WORKTREE_SH)

        reconciler = LaneCompletionReconciler(
            self.ledger, runner=runner, idle_after=300, worktree_reaper=reaper,
        )
        reconciler._vacate_pane_before_reap = lambda task: None  # simulate pre-#825 code

        report = reconciler.sweep()

        self.assertEqual(["ae825-nomove"], report["completed"])
        self.assertEqual(1, len(report["worktrees"]))
        self.assertEqual("refused", report["worktrees"][0]["outcome"])
        # Either #478 guard is an acceptable specimen here (tmux pane cwd or
        # the pane's own shell process cwd) -- both are the SAME live tmux
        # session this test built and left un-vacated; which one `_gc_is_
        # live` reports first is `worktree.sh`'s own internal ordering, not
        # something this test asserts on.
        self.assertIn("cwd is inside it (#478)", report["worktrees"][0]["reason"])
        self.assertTrue(worktree.is_dir(), "without the vacate step, the worktree must survive exactly as before #825")


if __name__ == "__main__":
    unittest.main()
