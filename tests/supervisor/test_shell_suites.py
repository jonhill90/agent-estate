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

import subprocess
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
SUITES = sorted(HERE.glob("test_*.sh"))


class ShellSuites(unittest.TestCase):
    def test_suites_are_discovered(self):
        # A glob that silently matches nothing would make every assertion below
        # vacuous, and the suite would go green by running no tests at all.
        self.assertTrue(SUITES, f"no test_*.sh found under {HERE}")

    def test_shell_suites_pass(self):
        for suite in SUITES:
            with self.subTest(suite=suite.name):
                proc = subprocess.run(
                    ["bash", str(suite)],
                    capture_output=True,
                    text=True,
                    timeout=300,
                )
                self.assertEqual(
                    proc.returncode,
                    0,
                    f"{suite.name} failed:\n{proc.stdout}\n{proc.stderr}",
                )


if __name__ == "__main__":
    unittest.main()
