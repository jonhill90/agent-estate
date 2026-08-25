"""host_pressure.py's own logic, in isolation -- load/free-memory readers
are injected so every case is deterministic regardless of what this
machine's real load happens to be at test time. See test_host_pressure.sh
(this same directory) for host-pressure.sh's bash sibling, and
test_dispatch_claude_print_host_pressure.sh /
test_dispatch_pi_rpc_host_pressure.sh for this module's own CLI wired into
the two dispatch entry points that had no gate at all before agent-supervisor#643.
"""

import sys
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from host_pressure import (  # noqa: E402
    Limits,
    PressureReadError,
    check,
    main,
)


def _load_ok():
    return 1.0


def _load_over():
    return 50.0


def _free_ok():
    return 10.0


def _free_under():
    return 0.01


def _cpu_ok():
    return 10


def _unreadable():
    raise PressureReadError("could not read load average: boom")


class CheckTest(unittest.TestCase):
    # --- property this repo cares about most: an unreadable metric must
    # never report clean, in either direction (load or memory). -----------

    def test_unreadable_load_reports_not_ok_not_a_false_clean_pass(self):
        result = check(Limits(), load1=_unreadable, free_mem_gb=_free_ok, cpu_count=_cpu_ok)
        self.assertFalse(result.ok)
        self.assertIn("could not read", result.reason)

    def test_unreadable_free_mem_reports_not_ok_not_a_false_clean_pass(self):
        def unreadable_mem():
            raise PressureReadError("could not read free memory: boom")

        result = check(Limits(), load1=_load_ok, free_mem_gb=unreadable_mem, cpu_count=_cpu_ok)
        self.assertFalse(result.ok)
        self.assertIn("could not read", result.reason)

    def test_unreadable_core_count_reports_not_ok(self):
        result = check(Limits(), load1=_load_ok, free_mem_gb=_free_ok, cpu_count=lambda: None)
        self.assertFalse(result.ok)
        self.assertIn("could not read core count", result.reason)

    # --- within limits reports OK -------------------------------------------

    def test_within_both_limits_reports_ok(self):
        result = check(Limits(), load1=_load_ok, free_mem_gb=_free_ok, cpu_count=_cpu_ok)
        self.assertTrue(result.ok)
        self.assertEqual(result.reason, "within limits")

    # --- exceeding either threshold reports NOT ok, naming which one -------

    def test_load_over_threshold_reports_not_ok_and_names_load(self):
        result = check(Limits(), load1=_load_over, free_mem_gb=_free_ok, cpu_count=_cpu_ok)
        self.assertFalse(result.ok)
        self.assertIn("load/core", result.reason)
        self.assertIn("5.00", result.reason)  # 50.0 / 10 cores

    def test_free_mem_under_threshold_reports_not_ok_and_names_memory(self):
        result = check(Limits(), load1=_load_ok, free_mem_gb=_free_under, cpu_count=_cpu_ok)
        self.assertFalse(result.ok)
        self.assertIn("free memory", result.reason)
        self.assertIn("0.01", result.reason)

    def test_load_checked_before_memory_when_both_fail(self):
        # Same short-circuit order pressure.go's Check() uses.
        result = check(Limits(), load1=_load_over, free_mem_gb=_free_under, cpu_count=_cpu_ok)
        self.assertFalse(result.ok)
        self.assertIn("load/core", result.reason)

    # --- 0 disables that one check, matching Limits' own "0 disables" doc --

    def test_zero_thresholds_disable_both_checks_even_under_pressure(self):
        result = check(
            Limits(max_load_per_core=0, min_free_mem_gb=0),
            load1=_load_over,
            free_mem_gb=_free_under,
            cpu_count=_cpu_ok,
        )
        self.assertTrue(result.ok)

    def test_default_limits_match_the_incident_motivated_values(self):
        limits = Limits()
        self.assertEqual(limits.max_load_per_core, 3.0)
        self.assertEqual(limits.min_free_mem_gb, 1.5)


class MainCliTest(unittest.TestCase):
    """The CLI contract host-pressure.sh's callers can already rely on:
    0 = proceed, 1 = refused (over a threshold), 2 = refused (unreadable)."""

    def test_within_limits_exits_zero(self):
        import host_pressure

        original = host_pressure.check
        host_pressure.check = lambda limits: original(
            limits, load1=_load_ok, free_mem_gb=_free_ok, cpu_count=_cpu_ok
        )
        try:
            self.assertEqual(main([]), 0)
        finally:
            host_pressure.check = original

    def test_over_threshold_exits_one(self):
        import host_pressure

        original = host_pressure.check
        host_pressure.check = lambda limits: original(
            limits, load1=_load_over, free_mem_gb=_free_ok, cpu_count=_cpu_ok
        )
        try:
            self.assertEqual(main([]), 1)
        finally:
            host_pressure.check = original

    def test_unreadable_exits_two_not_zero(self):
        import host_pressure

        original = host_pressure.check
        host_pressure.check = lambda limits: original(
            limits, load1=_unreadable, free_mem_gb=_free_ok, cpu_count=_cpu_ok
        )
        try:
            self.assertEqual(main([]), 2)
        finally:
            host_pressure.check = original


if __name__ == "__main__":
    unittest.main()
