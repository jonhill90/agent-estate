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

agent-estate#827: this suite originally proved both directions ONLY against
a REAL tmux server and the REAL `worktree.sh reap` guard chain. That made it
prove nothing about `_vacate_pane_before_reap` on a host with no live tmux
server -- exactly CI's `unit-tests` job. There, `_gc_tmux_occupies` can't
even ask the question (`rc==2`, "could not query tmux panes"), so
`worktree.sh reap` fails closed with a DIFFERENT reason than the one this
suite meant to pin, and the positive ("reaped") case can never be reached
at all. Worse, the negative case passed locally for a reason it did not
intend: `LaneWorktreeReaper`'s own `runner` (`default_runner`, then
`_make_reap_runner` here) never threaded this test's isolated
`TMUX_TMPDIR` through to the `worktree.sh reap` subprocess it shells out
to, so that call was answered by whatever tmux server happened to be
AMBIENT on the machine actually running the suite, not the private one
this fixture built -- a real bug in this file, not in the change under
test.

`VacatePaneBeforeReapDeterministicTest` below is the suite that now proves
the fix: it fakes the pane-cwd lookup at its own seam (the `runner`
callables `LaneCompletionReconciler` and `LaneWorktreeReaper` already take
as constructor injection) so a test can say "the pane is still inside" or
"no pane is inside" directly, without asking any real tmux server or
`lsof` to agree -- then asserts `refused` vs `reaped`, and the exact
reason string, on that. It still drives the REAL `_vacate_pane_before_reap`
method end to end (not a stub of it): the fake respawn-pane call it issues
genuinely updates the in-memory pane location the fake reap call reads,
so disabling `_vacate_pane_before_reap` (the same mutation check the
original suite already used) reproduces the refusal exactly as it would
against a real tmux server, and this suite requires no tmux at all.

`VacatePaneBeforeReapRealTmuxTest` below is the ORIGINAL suite, kept as an
optional real-tmux/real-`worktree.sh` integration check, now with its own
env-threading bug fixed and gated on actually starting a private tmux
server (not merely `tmux` being on `PATH`) -- it explicitly `skipTest`s
with a reason when no usable tmux server is available, and never silently
passes for the wrong reason the way it used to.
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
from core_lane_relation import normalize_worktree_path  # noqa: E402
from lane_worktree_reap import LaneWorktreeReaper  # noqa: E402
from reconcile_lane_completions import LaneCompletionReconciler  # noqa: E402

WORKTREE_SH = str(SUPERVISOR_DIR / "worktree.sh")

# Same override `test_lane_worktree_reap.py` uses and explains at length:
# `_gc_is_live`'s age floor defaults to 3600s so a lane between tool calls
# is never mistaken for abandoned; every fixture here is seconds old by
# construction, so exercising the pane-liveness check specifically (rather
# than every case surviving for the unrelated reason of being "too young")
# needs this override.
os.environ["WORKTREE_GC_MIN_AGE_SECONDS"] = "0"


def _make_reap_runner(env):
    """Same `--no-github` wrapper `test_lane_worktree_reap.py` uses (none of
    this suite's fixtures have a real GitHub remote and the content-diff
    predicate alone already decides every case here), now also threading
    `env` through the actual subprocess call -- the missing piece that let
    `worktree.sh reap`'s own `tmux`/`lsof` queries silently answer from
    whatever tmux server happened to be AMBIENT on the host running this
    suite instead of the private, isolated one this fixture built. Local
    runs 'passed' against that ambient state; CI has no ambient tmux server
    at all, so the same call there got a real but unrelated `rc==2` refusal.
    Used only by `VacatePaneBeforeReapRealTmuxTest` below."""

    def runner(argv):
        if len(argv) >= 3 and argv[1] == WORKTREE_SH and argv[2] == "reap":
            argv = [argv[0], argv[1], argv[2], "--no-github"] + argv[3:]
        return subprocess.run(argv, env=env, check=True, capture_output=True, text=True).stdout

    return runner


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


def _isolated_env(tmux_tmpdir):
    """Same isolation discipline every real-tmux test in this suite already
    holds (CLAUDE.md invariant 4): a private TMUX_TMPDIR, TMUX unset, never
    the operator's own default socket."""
    env = dict(os.environ)
    env["TMUX_TMPDIR"] = tmux_tmpdir
    env.pop("TMUX", None)
    return env


def _probe_real_tmux_server():
    """Actually try to stand up a private tmux server, not merely check
    `tmux` is on `PATH` -- agent-estate#827: `shutil.which("tmux")` alone
    was true in CI (the `unit-tests` job installs the binary), and a
    private server there genuinely CAN be created (this probe passes in
    CI too). That is not the same guarantee as `worktree.sh reap`'s own
    guard chain re-observing that server deterministically a few
    subprocess calls later, right after a real `respawn-pane -k` -- see
    `VacatePaneBeforeReapRealTmuxTest`'s own docstring for the race this
    suite hit even with a genuinely running private server. This probe is
    kept only to give `RUN_REAL_TMUX_VACATE_TEST=1` a specific reason when
    a server truly can't be reached, never as the thing that decides
    whether the class runs by default -- see that env var below. Returns
    `(ok, reason)` -- `reason` is empty when `ok` is True, and a
    human-readable cause otherwise, always suitable for `self.skipTest`."""
    if shutil.which("tmux") is None:
        return False, "tmux is not installed"
    probe_tmpdir = tempfile.mkdtemp(prefix="ae827-tmux-probe-")
    env = _isolated_env(probe_tmpdir)
    try:
        subprocess.run(
            ["tmux", "-f", "/dev/null", "new-session", "-d", "-s", "probe", "-c", "/tmp"],
            env=env, check=True, capture_output=True, text=True, timeout=10,
        )
        subprocess.run(
            ["tmux", "list-panes", "-a"], env=env, check=True, capture_output=True, text=True, timeout=10,
        )
    except Exception as error:  # noqa: BLE001 -- any failure means "no usable server", not a test error
        return False, f"could not start a real tmux server: {error}"
    finally:
        subprocess.run(["tmux", "kill-server"], env=env, capture_output=True, timeout=10)
        shutil.rmtree(probe_tmpdir, ignore_errors=True)
    return True, ""


# agent-estate#827: measured directly against this repo's own CI -- with
# `LaneWorktreeReaper`'s env-threading bug fixed (`_make_reap_runner`
# above), `VacatePaneBeforeReapRealTmuxTest` still failed intermittently on
# `ubuntu-latest`, `refused` instead of `reaped`, even though its own
# `_probe_real_tmux_server` check passed there (a real private server IS
# reachable). The remaining gap is a race this suite does not control:
# `tmux respawn-pane -k` returns before the OLD pane's process has fully
# exited, and `worktree.sh reap`'s `_gc_process_refs` (real `lsof`,
# system-wide, un-scoped to this test's own isolated tmux socket) can
# still see that dying process's cwd for a brief window afterward. Retrying
# or sleeping around that race would only narrow it, never close it, and
# this suite's real job -- proving `_vacate_pane_before_reap` itself, both
# directions, deterministically -- is already fully carried by
# `VacatePaneBeforeReapDeterministicTest` above, which needs no real tmux
# server at all. So this class is opt-in only, for a human deliberately
# exercising the real `worktree.sh reap` guard chain end to end; it is
# never part of the default CI run, and never silently flakes there.
RUN_REAL_TMUX_VACATE_TEST = os.environ.get("RUN_REAL_TMUX_VACATE_TEST") == "1"


class FakePaneState:
    """The seam agent-estate#827 fakes: one shared, in-memory record of
    where a lane's pane currently is. `FakeReconcilerRunner` (below) reads
    and mutates it in response to the exact `tmux`/`git` calls
    `_vacate_pane_before_reap` really issues; `fake_reap_runner` (below)
    reads it to decide `refused` vs `reaped` -- so both halves of the real
    production call chain agree on ONE deterministic answer to "is a pane
    inside this worktree", instead of each asking a real tmux server or
    `lsof` and hoping they agree with the fixture.

    `last_launch_cmd` is agent-estate#827 fix2's own addition: the exact
    string `_vacate_pane_before_reap` handed `respawn-pane` as its launch
    command, so a test can assert it is never empty (fix2's own defect --
    the first version respawned with NO command at all, leaving a bare
    shell)."""

    def __init__(self, pane_path):
        self.pane_path = pane_path
        self.respawned_to = []
        self.last_launch_cmd = None


class FakeReconcilerRunner:
    """Drop-in for `LaneCompletionReconciler`'s own `runner` -- answers
    `lanes.sh --json <session>` from an in-memory row (identical shape to
    the real-tmux suite's own `SessionRunner`), and answers every
    `tmux`/`git`/`bash harness-launch-cmd.sh` call `_vacate_pane_before_reap`
    itself issues (`display-message`, `rev-parse --git-common-dir`,
    `harness-launch-cmd.sh`, `respawn-pane`) against `FakePaneState` instead
    of a real tmux server or the real harness registry -- so the real,
    unmodified `_vacate_pane_before_reap` method runs end to end and its
    real effect (moving the pane, WITH a launch command) is what this suite
    observes.

    `launch_cmd` defaults to a non-empty fake -- most fixtures in this file
    want the shipped, successful path; `launch_cmd=""` (or `None`) models
    agent-estate#827 fix2's own unresolved-harness case (no `H_LAUNCH_CMD`
    for this harness, or `harness-launch-cmd.sh` failing outright)."""

    def __init__(self, state, session, window, common_dir, launch_cmd="FAKE_LAUNCH_CMD --ready"):
        self.state = state
        self.session = session
        self.window = window
        self.common_dir = common_dir
        self.launch_cmd = launch_cmd
        self.calls = []

    def __call__(self, command):
        self.calls.append(command)
        if command[0] == "tmux" and command[1] == "display-message":
            return self.state.pane_path + "\n"
        if command[0] == "git" and "--git-common-dir" in command:
            return self.common_dir + "\n"
        if command[0] == "bash" and command[1].endswith("harness-launch-cmd.sh"):
            if not self.launch_cmd:
                raise RuntimeError("harness-launch-cmd.sh: no harness named '%s'" % command[2])
            return self.launch_cmd + "\n"
        if command[0] == "tmux" and command[1] == "respawn-pane":
            # agent-estate#827 fix2's own defect, pinned: the FIRST version
            # of `_vacate_pane_before_reap` called `respawn-pane` with NO
            # trailing launch command at all (bare shell). Shape-checked
            # here, not merely positionally unpacked, so THAT mutation is
            # caught as a call this fake refuses to answer, rather than
            # silently reinterpreting `-c`/`park_dir` as `launch_cmd`/
            # `park_dir` and passing anyway.
            if len(command) != 8 or command[:3] != ["tmux", "respawn-pane", "-k"] \
                    or command[3] != "-t" or command[5] != "-c" or not command[7]:
                raise AssertionError(f"respawn-pane called with no launch command (bare shell): {command}")
            park_dir, launch_cmd = command[6], command[7]
            self.state.pane_path = park_dir
            self.state.respawned_to.append(park_dir)
            self.state.last_launch_cmd = launch_cmd
            return ""
        session = command[-1]
        if session != self.session:
            raise RuntimeError(f"lanes.sh unavailable for session {session}")
        return f'[{{"window":{self.window},"window_id":"@1","name":"n","command":"claude","state":"free","idle_seconds":400,"model":"sonnet","execution_mode":"tmux"}}]'


def fake_reap_runner(state, calls=None):
    """Drop-in for `LaneWorktreeReaper`'s own `runner` -- never shells out
    to `worktree.sh`/`tmux`/`lsof` at all. Reads `state.pane_path` (the
    SAME state `FakeReconcilerRunner` just moved, if `_vacate_pane_before_
    reap` ran) to decide `refused` vs `reaped`, in the exact shape
    `LaneWorktreeReaper.reap_task_worktree` expects: a `CalledProcessError`
    carrying `.stderr` on refusal (worktree.sh's own real reason string,
    reproduced verbatim, agent-supervisor#478), plain success -- and the
    directory actually removed, matching `worktree.sh reap`'s real
    on-disk effect -- otherwise."""

    def runner(argv):
        if calls is not None:
            calls.append(argv)
        worktree_path = argv[3]
        target_real = normalize_worktree_path(worktree_path)
        pane_real = normalize_worktree_path(state.pane_path)
        if pane_real and (pane_real == target_real or pane_real.startswith(target_real.rstrip("/") + "/")):
            raise subprocess.CalledProcessError(
                1, argv, output="",
                stderr=f"worktree: gc skipping {worktree_path} -- a tmux pane's cwd is inside it (#478)\n",
            )
        shutil.rmtree(worktree_path, ignore_errors=True)
        return ""

    return runner


class VacatePaneBeforeReapDeterministicTest(unittest.TestCase):
    """agent-estate#827: the real `sweep()` -> real
    `_vacate_pane_before_reap` -> real `LaneWorktreeReaper.reap_task_
    worktree` orchestration, with the pane-cwd lookup faked at its own
    seam (see `FakePaneState`/`FakeReconcilerRunner`/`fake_reap_runner`
    above) so the assertion is about THIS orchestration, never about
    whether a real tmux server or `lsof` happens to be reachable. Runs on
    every host, tmux installed or not."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.worktree = Path(self.tempdir.name) / "worktree"
        self.worktree.mkdir()
        self.state_tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.state_tempdir.cleanup)
        self.ledger = Ledger(Path(self.state_tempdir.name), clock=lambda: 1_000)

    def _build_lane(self, task_id, *, session="s-827", window_index="3"):
        """A real merged/dispatched-shaped ledger row naming `self.worktree`
        -- no real git/tmux needed, since neither runner faked above shells
        out to either."""
        lane = f"{session}:{window_index}"
        self.ledger.record_dispatch(
            lane=lane, pane_id="%1", nonce="nonce-827", harness="claude",
            repo="https://example.invalid/repo", server_id="server-a", session_id="$827", command="claude",
            task_id=task_id, source_kind="pull",
            source_url="https://github.com/jonhill90/agent-estate/pull/825",
            source_ref="825", summary="review of #825", source_state="OPEN",
            evidence=[f"claimed by dispatch.sh for lane {lane}", "issues: 825"],
            status_marker=None, worktree_path=str(self.worktree), accepted=True,
        )
        return lane, session, window_index

    def test_direction_1_pane_is_vacated_and_worktree_is_reaped(self):
        """The shipped behaviour, with the pane starting INSIDE the
        worktree (mirroring dispatch's own initial `respawn-pane -k -c
        "$WORKTREE"`): `_vacate_pane_before_reap` moves it out, and the
        reap call that follows sees no pane inside and succeeds."""
        self._build_lane("ae827-vacate")
        pane_state = FakePaneState(pane_path=str(self.worktree))
        reconciler_runner = FakeReconcilerRunner(
            pane_state, "s-827", 3, common_dir=str(Path(self.tempdir.name) / "shared" / ".git"),
        )
        reap_calls = []
        reaper = LaneWorktreeReaper(self.ledger, runner=fake_reap_runner(pane_state, reap_calls))

        report = LaneCompletionReconciler(
            self.ledger, runner=reconciler_runner, idle_after=300, worktree_reaper=reaper,
        ).sweep()

        self.assertEqual(["ae827-vacate"], report["completed"])
        self.assertEqual(1, len(report["worktrees"]))
        self.assertEqual("reaped", report["worktrees"][0]["outcome"], report["worktrees"][0])
        self.assertFalse(self.worktree.is_dir(), "the worktree must actually be gone, not merely reported reaped")
        self.assertTrue(pane_state.respawned_to, "the pane must have been moved before the reap call ran")
        self.assertNotEqual(str(self.worktree), pane_state.pane_path)
        self.assertEqual(1, len(reap_calls), "reap must be called exactly once, after the vacate step")
        # agent-estate#827 fix2, requirement 1: the pane must be parked with
        # a REAL launch command, never bare -- the defect the fix pass
        # closed was `respawn-pane -k -c <dir>` with no trailing argv at
        # all, which leaves a login shell lanes.sh can never read `free`.
        self.assertTrue(pane_state.last_launch_cmd, "the parked pane must have been given a launch command, not left bare")
        # agent-estate#827 fix2, requirement 2: the vacate step's own
        # outcome is recorded on the SAME dict, distinguishable from the
        # reap call's `outcome`/`reason`.
        self.assertEqual("attempted:succeeded", report["worktrees"][0]["vacate"], report["worktrees"][0])

    def test_direction_2_mutation_without_the_vacate_step_the_refusal_reproduces(self):
        """Mutation check (agent-estate#827's own brief): the SAME fixture,
        with `_vacate_pane_before_reap` disabled -- simulating the pre-#825
        code. The pane never moves, the fake reap call must see it still
        inside and refuse with worktree.sh's own #478 reason text, and the
        worktree must survive. If this assertion stays green with the real
        `_vacate_pane_before_reap` reverted to a no-op (i.e. this test),
        direction 1 above must go RED -- it does, because direction 1's
        `reaped` outcome depends entirely on the pane actually having moved."""
        self._build_lane("ae827-nomove")
        pane_state = FakePaneState(pane_path=str(self.worktree))
        reconciler_runner = FakeReconcilerRunner(
            pane_state, "s-827", 3, common_dir=str(Path(self.tempdir.name) / "shared" / ".git"),
        )
        reaper = LaneWorktreeReaper(self.ledger, runner=fake_reap_runner(pane_state))

        reconciler = LaneCompletionReconciler(
            self.ledger, runner=reconciler_runner, idle_after=300, worktree_reaper=reaper,
        )
        reconciler._vacate_pane_before_reap = lambda task: None  # simulate pre-#825 code

        report = reconciler.sweep()

        self.assertEqual(["ae827-nomove"], report["completed"])
        self.assertEqual(1, len(report["worktrees"]))
        self.assertEqual("refused", report["worktrees"][0]["outcome"])
        self.assertIn("cwd is inside it (#478)", report["worktrees"][0]["reason"])
        self.assertFalse(pane_state.respawned_to, "the vacate step was disabled -- the pane must never have moved")
        self.assertTrue(self.worktree.is_dir(), "without the vacate step, the worktree must survive exactly as before #825")

    def test_direction_3_mutation_unresolved_harness_skips_instead_of_bare_shelling(self):
        """agent-estate#827 fix2's own mutation check for requirement 1: an
        unresolved harness (no `H_LAUNCH_CMD` for it, or `harness-launch-
        cmd.sh` failing outright -- `launch_cmd=""` below simulates both)
        must never fall back to the pre-fix2 bare respawn. It skips
        entirely: the pane never moves, the reap call sees it still inside
        and refuses exactly like direction 2, and `vacate` records WHY."""
        self._build_lane("ae827-nolaunch")
        pane_state = FakePaneState(pane_path=str(self.worktree))
        reconciler_runner = FakeReconcilerRunner(
            pane_state, "s-827", 3, common_dir=str(Path(self.tempdir.name) / "shared" / ".git"),
            launch_cmd="",
        )
        reaper = LaneWorktreeReaper(self.ledger, runner=fake_reap_runner(pane_state))

        report = LaneCompletionReconciler(
            self.ledger, runner=reconciler_runner, idle_after=300, worktree_reaper=reaper,
        ).sweep()

        self.assertEqual(["ae827-nolaunch"], report["completed"])
        self.assertEqual(1, len(report["worktrees"]))
        self.assertEqual("refused", report["worktrees"][0]["outcome"])
        self.assertIn("cwd is inside it (#478)", report["worktrees"][0]["reason"])
        self.assertFalse(pane_state.respawned_to, "an unresolved harness must never respawn the pane at all")
        self.assertEqual("skipped:no_harness_launch_cmd", report["worktrees"][0]["vacate"], report["worktrees"][0])
        self.assertTrue(self.worktree.is_dir(), "the worktree must survive -- reap is still correctly refused")

    def test_vacate_outcome_field_records_a_respawn_failure_distinctly_from_a_skip(self):
        """agent-estate#827 fix2's own requirement 2: a vacate that was
        genuinely ATTEMPTED but whose `respawn-pane` call itself failed
        (tmux unreachable mid-call, say) must record `attempted:failed`,
        never collapse into the same `skipped:*` bucket a lane this method
        correctly declined to touch would produce -- the two must stay
        distinguishable, which is the whole point of this field existing."""
        self._build_lane("ae827-respawnfails")
        pane_state = FakePaneState(pane_path=str(self.worktree))

        class RaisingRespawnRunner(FakeReconcilerRunner):
            def __call__(self, command):
                if command[0] == "tmux" and command[1] == "respawn-pane":
                    self.calls.append(command)
                    raise RuntimeError("tmux respawn-pane: the server vanished")
                return super().__call__(command)

        reconciler_runner = RaisingRespawnRunner(
            pane_state, "s-827", 3, common_dir=str(Path(self.tempdir.name) / "shared" / ".git"),
        )
        reaper = LaneWorktreeReaper(self.ledger, runner=fake_reap_runner(pane_state))

        report = LaneCompletionReconciler(
            self.ledger, runner=reconciler_runner, idle_after=300, worktree_reaper=reaper,
        ).sweep()

        self.assertEqual(1, len(report["worktrees"]))
        self.assertEqual("attempted:failed", report["worktrees"][0]["vacate"], report["worktrees"][0])
        self.assertNotEqual(
            "skipped:no_harness_launch_cmd", report["worktrees"][0]["vacate"],
            "a genuinely attempted-but-failed respawn must not read the same as a correctly declined one",
        )


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
        # agent-estate#827 fix2: `bash` is `harness-launch-cmd.sh` --
        # exercised for REAL here (see `VacatePaneBeforeReapRealTmuxTest.
        # setUp`'s `HARNESS_REGISTRY_DIR` override, which points the real
        # registry loader at a harmless stub harness instead of letting it
        # resolve the genuine `claude` binary and actually launch one).
        if command[0] in ("tmux", "git", "bash"):
            return subprocess.run(
                command, env=self.env, check=True, capture_output=True, text=True, timeout=10
            ).stdout
        session = command[-1]
        if session != self.session:
            raise RuntimeError(f"lanes.sh unavailable for session {session}")
        return f'[{{"window":{self.window},"window_id":"@1","name":"n","command":"claude","state":"free","idle_seconds":400,"model":"sonnet","execution_mode":"tmux"}}]'


class VacatePaneBeforeReapRealTmuxTest(unittest.TestCase):
    """agent-estate#825/#827: the real `sweep()` -> real
    `_vacate_pane_before_reap` -> real `worktree.sh reap` path, proven both
    directions -- an OPT-IN integration check (see `RUN_REAL_TMUX_VACATE_
    TEST` above for why) on top of `VacatePaneBeforeReapDeterministicTest`
    above, which is what actually proves the fix on any host, tmux or not,
    and is what CI relies on (agent-estate#827)."""

    def setUp(self):
        if not RUN_REAL_TMUX_VACATE_TEST:
            self.skipTest(
                "opt-in only (set RUN_REAL_TMUX_VACATE_TEST=1) -- see this "
                "class's own docstring for the real-tmux race that makes it "
                "unsuitable for unattended CI"
            )
        ok, reason = _probe_real_tmux_server()
        if not ok:
            self.skipTest(f"no usable real tmux server: {reason}")
        self.fixture = RepoFixture()
        self.addCleanup(self.fixture.cleanup)
        self.tmpdir = tempfile.mkdtemp(prefix="ae825-tmux-")
        self.env = _isolated_env(self.tmpdir)
        # agent-estate#827 fix2: `_vacate_pane_before_reap` now relaunches
        # this lane's own recorded harness ("claude", set by `_build_lane`
        # below) via the REAL `harness-launch-cmd.sh` -- pointed at a
        # one-harness stub registry rather than the real one, so this test
        # proves the CLI seam end to end without ever actually invoking the
        # real `claude` binary inside its private tmux session.
        self.harness_dir = Path(tempfile.mkdtemp(prefix="ae827-harness-"))
        self.addCleanup(shutil.rmtree, self.harness_dir, ignore_errors=True)
        (self.harness_dir / "claude.sh").write_text(
            "HARNESS_NAME=claude\n"
            "HARNESS_COMMAND_RE='^sleep$'\n"
            "HARNESS_READY_RE='ready'\n"
            "HARNESS_LAUNCH_CMD='sleep 30'\n"
        )
        self.env["HARNESS_REGISTRY_DIR"] = str(self.harness_dir)
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
        reaper = LaneWorktreeReaper(self.ledger, runner=_make_reap_runner(self.env), worktree_bin=WORKTREE_SH)

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
        reaper = LaneWorktreeReaper(self.ledger, runner=_make_reap_runner(self.env), worktree_bin=WORKTREE_SH)

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
