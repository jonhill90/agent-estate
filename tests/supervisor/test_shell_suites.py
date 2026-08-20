"""Run the bash test suites under tests/supervisor/ as part of the Python suite.

The supervisor's shell tools -- ``lanes.sh``, ``watchdog.sh``, ``claim.sh`` --
are covered by stub-driven bash suites next to this file. Nothing ran them.
They were not in ``.github/workflows/validate.yml``, no Python test shelled out
to them, and ``scripts/supervisor/README.md`` claimed the repository-wide
``unittest discover`` picked up "this core's tests" -- which was true only of
the Python ones.

That is the ``acp_transport.py`` shape: a tested mechanism with no caller. A
regression in ``lanes.sh`` would have reached ``main`` green. This wires them
into the one command CI already runs, so the README's claim becomes true.
"""

import os
import signal
import subprocess
import sys
import time
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
_ALL_SUITES = sorted(HERE.glob("test_*.sh"))

# agent-supervisor#440: CI ran all 89 suites serially in this one test,
# owning ~99% of the 22-minute run. validate.yml now bin-packs suites into
# balanced parallel shards (scripts/ci/plan_shell_shards.py) and passes each
# shard's assignment here via SHELL_SUITE_ONLY, so the fast Python job (which
# sets SHELL_SUITE_SKIP=1) does not also run all 89 serially itself.
#
# Discovery (`_ALL_SUITES` above) is always the full glob -- never filtered
# implicitly -- so SHELL_SUITE_ONLY naming a suite that does not exist on
# disk is a loud, immediate error below, not a silently-empty shard.
SHELL_SUITE_SKIP = os.environ.get("SHELL_SUITE_SKIP") == "1"
_only_raw = os.environ.get("SHELL_SUITE_ONLY")
if _only_raw is None:
    SUITES = _ALL_SUITES
else:
    _only_names = {n for n in _only_raw.splitlines() if n.strip()}
    _all_names = {p.name for p in _ALL_SUITES}
    _missing = _only_names - _all_names
    if _missing:
        raise RuntimeError(
            f"SHELL_SUITE_ONLY names not found among discovered suites under {HERE}: "
            f"{sorted(_missing)}"
        )
    SUITES = [p for p in _ALL_SUITES if p.name in _only_names]

TIMEOUT = 300
# agent-supervisor#104: a suite that starts a real, long-lived poller-shaped
# process (a tmux-hosted stand-in, or the real inbox-poll.sh under a
# throwaway LaunchAgent) relies on its OWN trap to reap that process -- but
# the trap cannot run at all if this harness kills the suite outright.
# `subprocess.run(timeout=...)` does exactly that on TimeoutExpired: it calls
# `Popen.kill()`, which is SIGKILL, straight to the bash process. SIGKILL
# cannot be caught, so the suite's EXIT/TERM trap never fires and whatever it
# started (a tmux session on an isolated socket, a throwaway LaunchAgent job)
# keeps running for as long as the machine stays up -- this is exactly the
# shape #104 measured: two processes, hours old, both survived SIGTERM only
# because nothing ever SENT them one.
#
# Each suite now runs in its own process group (`start_new_session=True`,
# which calls `setsid()` before exec, so the bash process's own pid becomes
# its pgid). On timeout this harness signals that GROUP -- SIGTERM first,
# then waits GRACE_AFTER_TERM seconds -- giving the suite's own trap the real
# chance to run its verified reap (tests/supervisor/lib/reap-verified.sh) and
# tear down whatever it created, the same way a clean exit already does.
# Escalating straight to SIGKILL is what caused the leak in the first place;
# this only escalates if the group is still alive once that grace has passed,
# and it reports explicitly whether escalation was needed and whether the
# group could still not be confirmed dead afterward -- never silent.
GRACE_AFTER_TERM = 30


def _pgid_alive(pgid):
    try:
        os.killpg(pgid, 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        # A pgid we can signal-check-0 but not act on would be a surprise on
        # a process we just spawned ourselves -- treat as alive rather than
        # silently assuming it is gone.
        return True


class ShellSuites(unittest.TestCase):
    def test_suites_are_discovered(self):
        if SHELL_SUITE_SKIP:
            self.skipTest("shell suites run in a separate sharded job; see .github/workflows/validate.yml")
        # A glob that silently matches nothing would make every assertion below
        # vacuous, and the suite would go green by running no tests at all.
        # Also catches a shard whose SHELL_SUITE_ONLY filtered SUITES to empty --
        # a shard that silently ran zero suites must not read as a fast green one.
        self.assertTrue(SUITES, f"no suites selected under {HERE} (discovered={len(_ALL_SUITES)})")

    def test_shell_suites_pass(self):
        if SHELL_SUITE_SKIP:
            self.skipTest("shell suites run in a separate sharded job; see .github/workflows/validate.yml")
        for suite in SUITES:
            with self.subTest(suite=suite.name):
                started = time.monotonic()
                proc = subprocess.Popen(
                    ["bash", str(suite)],
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    text=True,
                    start_new_session=True,
                )
                pgid = os.getpgid(proc.pid)
                timed_out = False
                escalated = False
                could_not_confirm_dead = False
                stdout = stderr = ""
                try:
                    stdout, stderr = proc.communicate(timeout=TIMEOUT)
                except subprocess.TimeoutExpired:
                    timed_out = True
                    os.killpg(pgid, signal.SIGTERM)
                    deadline = time.monotonic() + GRACE_AFTER_TERM
                    while time.monotonic() < deadline and _pgid_alive(pgid):
                        time.sleep(0.5)
                    if _pgid_alive(pgid):
                        escalated = True
                        os.killpg(pgid, signal.SIGKILL)
                        deadline = time.monotonic() + 5
                        while time.monotonic() < deadline and _pgid_alive(pgid):
                            time.sleep(0.5)
                        could_not_confirm_dead = _pgid_alive(pgid)
                    try:
                        stdout, stderr = proc.communicate(timeout=5)
                    except subprocess.TimeoutExpired:
                        pass
                finally:
                    # #440: nothing recorded per-suite wall time, so CI's 22
                    # minutes had no ranked breakdown -- only a single 1330s
                    # gap around this entire test in the -v log. Printed
                    # unconditionally (finally, not just on the passing path)
                    # so a hang or failure still leaves a timing data point
                    # instead of silently dropping out of the ranking.
                    print(
                        f"[shell-suite-timing] {suite.name} {time.monotonic() - started:.1f}s",
                        file=sys.stderr,
                        flush=True,
                    )

                if timed_out:
                    detail = (
                        f"{suite.name} timed out after {TIMEOUT}s -- SIGTERM to its "
                        f"process group {'was escalated to SIGKILL' if escalated else 'was sufficient'}"
                    )
                    if could_not_confirm_dead:
                        detail += " and the group STILL could not be confirmed dead"
                    self.fail(f"{detail}:\n{stdout}\n{stderr}")

                self.assertEqual(
                    proc.returncode,
                    0,
                    f"{suite.name} failed:\n{stdout}\n{stderr}",
                )


if __name__ == "__main__":
    unittest.main()
