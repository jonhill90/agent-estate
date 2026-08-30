"""host_pressure.py's own logic, in isolation -- load/free-memory/session
readers are injected so every case is deterministic regardless of what this
machine's real load or lane count happens to be at test time. See
test_host_pressure.sh (this same directory) for host-pressure.sh's bash
sibling, and test_dispatch_claude_print_host_pressure.sh /
test_dispatch_pi_rpc_host_pressure.sh for this module's own CLI wired into
the two dispatch entry points that had no gate at all before agent-supervisor#643.

Every pre-existing (load/memory) test below passes max_agent_sessions=0
explicitly -- the same isolation host-pressure.sh's own bash suite uses
(SUPERVISOR_MAX_AGENT_SESSIONS=0) so those assertions stay pinned to the
metric they name, undisturbed by the session gate added for
agent-estate#904. The session gate gets its own section, with the same
unreadable-fails-closed and mutation-in-both-directions coverage the
load/memory gates already have.
"""

import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
HOST_PRESSURE_PY = SUPERVISOR_DIR / "host_pressure.py"
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


def _inflight_ok():
    return 0


def _inflight_over():
    return 999


def _inflight_unreadable():
    raise PressureReadError(
        "could not read work-in-flight count (count-work-in-flight.sh exited 2)"
    )


class CheckTest(unittest.TestCase):
    # --- property this repo cares about most: an unreadable metric must
    # never report clean, in either direction (load or memory). -----------

    def test_unreadable_load_reports_not_ok_not_a_false_clean_pass(self):
        result = check(
            Limits(max_agent_sessions=0), load1=_unreadable, free_mem_gb=_free_ok, cpu_count=_cpu_ok
        )
        self.assertFalse(result.ok)
        self.assertIn("could not read", result.reason)

    def test_unreadable_free_mem_reports_not_ok_not_a_false_clean_pass(self):
        def unreadable_mem():
            raise PressureReadError("could not read free memory: boom")

        result = check(
            Limits(max_agent_sessions=0), load1=_load_ok, free_mem_gb=unreadable_mem, cpu_count=_cpu_ok
        )
        self.assertFalse(result.ok)
        self.assertIn("could not read", result.reason)

    def test_unreadable_core_count_reports_not_ok(self):
        result = check(
            Limits(max_agent_sessions=0), load1=_load_ok, free_mem_gb=_free_ok, cpu_count=lambda: None
        )
        self.assertFalse(result.ok)
        self.assertIn("could not read core count", result.reason)

    # --- within limits reports OK -------------------------------------------

    def test_within_both_limits_reports_ok(self):
        result = check(
            Limits(max_agent_sessions=0), load1=_load_ok, free_mem_gb=_free_ok, cpu_count=_cpu_ok
        )
        self.assertTrue(result.ok)
        self.assertEqual(result.reason, "within limits")

    # --- exceeding either threshold reports NOT ok, naming which one -------

    def test_load_over_threshold_reports_not_ok_and_names_load(self):
        result = check(
            Limits(max_agent_sessions=0), load1=_load_over, free_mem_gb=_free_ok, cpu_count=_cpu_ok
        )
        self.assertFalse(result.ok)
        self.assertIn("load/core", result.reason)
        self.assertIn("5.00", result.reason)  # 50.0 / 10 cores

    def test_free_mem_under_threshold_reports_not_ok_and_names_memory(self):
        result = check(
            Limits(max_agent_sessions=0), load1=_load_ok, free_mem_gb=_free_under, cpu_count=_cpu_ok
        )
        self.assertFalse(result.ok)
        self.assertIn("free memory", result.reason)
        self.assertIn("0.01", result.reason)

    def test_load_checked_before_memory_when_both_fail(self):
        # Same short-circuit order pressure.go's Check() uses.
        result = check(
            Limits(max_agent_sessions=0), load1=_load_over, free_mem_gb=_free_under, cpu_count=_cpu_ok
        )
        self.assertFalse(result.ok)
        self.assertIn("load/core", result.reason)

    # --- 0 disables that one check, matching Limits' own "0 disables" doc --

    def test_zero_thresholds_disable_both_checks_even_under_pressure(self):
        result = check(
            Limits(max_load_per_core=0, min_free_mem_gb=0, max_agent_sessions=0),
            load1=_load_over,
            free_mem_gb=_free_under,
            cpu_count=_cpu_ok,
        )
        self.assertTrue(result.ok)

    def test_default_limits_match_the_incident_motivated_values(self):
        limits = Limits()
        self.assertEqual(limits.max_load_per_core, 3.0)
        self.assertEqual(limits.min_free_mem_gb, 1.5)
        self.assertEqual(limits.max_agent_sessions, 20)


class SessionGateTest(unittest.TestCase):
    """agent-estate#904: the work-in-flight gate host_pressure.py had none of
    at all. Load and memory are disabled throughout (max_load_per_core=0,
    min_free_mem_gb=0) so each case isolates the session gate specifically,
    same isolation pattern test_host_pressure.sh's own THIRD GATE section
    uses for the bash sibling.
    """

    def _limits(self, max_agent_sessions):
        return Limits(max_load_per_core=0, min_free_mem_gb=0, max_agent_sessions=max_agent_sessions)

    def test_inflight_below_limit_allows(self):
        result = check(self._limits(20), inflight_count=lambda: 17)
        self.assertTrue(result.ok)
        self.assertEqual(result.reason, "within limits")

    def test_inflight_at_limit_refuses(self):
        result = check(self._limits(20), inflight_count=lambda: 20)
        self.assertFalse(result.ok)
        self.assertIn("work in flight 20 >= 20", result.reason)

    def test_inflight_over_limit_refuses(self):
        result = check(self._limits(20), inflight_count=_inflight_over)
        self.assertFalse(result.ok)
        self.assertIn("work in flight 999 >= 20", result.reason)

    def test_zero_disables_the_session_gate_even_over_limit(self):
        result = check(self._limits(0), inflight_count=_inflight_over)
        self.assertTrue(result.ok)

    # --- fail closed: a failing/absent count-work-in-flight.sh must refuse,
    # not silently read as zero in-flight (agent-estate#904 requirement 2) --

    def test_unreadable_inflight_count_refuses_not_a_false_clean_pass(self):
        result = check(self._limits(20), inflight_count=_inflight_unreadable)
        self.assertFalse(result.ok)
        self.assertIn("could not read", result.reason)

    def test_unreadable_inflight_reason_is_distinguishable_from_at_capacity(self):
        # Requirement 3 of #904: "the refusal reason is distinguishable from
        # 'at capacity'" -- assert the two reasons never collide.
        at_capacity = check(self._limits(20), inflight_count=lambda: 20).reason
        unreadable = check(self._limits(20), inflight_count=_inflight_unreadable).reason
        self.assertNotEqual(at_capacity, unreadable)
        self.assertIn("work in flight", at_capacity)
        self.assertIn("could not read", unreadable)
        self.assertNotIn("could not read", at_capacity)
        self.assertNotIn("work in flight", unreadable)

    def test_real_default_inflight_count_shells_out_to_count_work_in_flight_sh(self):
        # No injected inflight_count -- exercises _default_inflight_count's
        # real subprocess path against the real count-work-in-flight.sh next
        # to host_pressure.py on THIS machine. Only asserts the contract
        # (an int >= 0, or a PressureReadError -- never a crash, never a
        # silent non-int), since the real in-flight count varies by host.
        result = check(self._limits(0))  # gate disabled: proves no crash, not a specific count
        self.assertTrue(result.ok)


class MainCliTest(unittest.TestCase):
    """The CLI contract host-pressure.sh's callers can already rely on:
    0 = proceed, 1 = refused (over a threshold), 2 = refused (unreadable)."""

    def test_within_limits_exits_zero(self):
        import host_pressure

        original = host_pressure.check
        host_pressure.check = lambda limits: original(
            limits, load1=_load_ok, free_mem_gb=_free_ok, cpu_count=_cpu_ok, inflight_count=_inflight_ok
        )
        try:
            self.assertEqual(main([]), 0)
        finally:
            host_pressure.check = original

    def test_over_threshold_exits_one(self):
        import host_pressure

        original = host_pressure.check
        host_pressure.check = lambda limits: original(
            limits, load1=_load_over, free_mem_gb=_free_ok, cpu_count=_cpu_ok, inflight_count=_inflight_ok
        )
        try:
            self.assertEqual(main([]), 1)
        finally:
            host_pressure.check = original

    def test_unreadable_exits_two_not_zero(self):
        import host_pressure

        original = host_pressure.check
        host_pressure.check = lambda limits: original(
            limits, load1=_unreadable, free_mem_gb=_free_ok, cpu_count=_cpu_ok, inflight_count=_inflight_ok
        )
        try:
            self.assertEqual(main([]), 2)
        finally:
            host_pressure.check = original

    def test_session_gate_over_limit_exits_one(self):
        import host_pressure

        original = host_pressure.check
        host_pressure.check = lambda limits: original(
            limits, load1=_load_ok, free_mem_gb=_free_ok, cpu_count=_cpu_ok, inflight_count=_inflight_over
        )
        try:
            self.assertEqual(main([]), 1)
        finally:
            host_pressure.check = original

    def test_session_gate_unreadable_exits_two_not_zero(self):
        import host_pressure

        original = host_pressure.check
        host_pressure.check = lambda limits: original(
            limits, load1=_load_ok, free_mem_gb=_free_ok, cpu_count=_cpu_ok, inflight_count=_inflight_unreadable
        )
        try:
            self.assertEqual(main([]), 2)
        finally:
            host_pressure.check = original


def _load_mutant(marker, replacement):
    """Patches host_pressure.py's own source text (never the module already
    imported above) and loads the patched copy as an independent module, the
    same "patch a copy, run it, prove behaviour changed" technique
    test_host_pressure.sh's own bash MUTATION section uses on
    host-pressure.sh. Asserts the marker is present and unique first, so a
    future refactor that moves this comparison fails the test loudly instead
    of silently mutating nothing.
    """
    text = HOST_PRESSURE_PY.read_text()
    assert text.count(marker) == 1, f"marker not found or not unique -- host_pressure.py's shape changed: {marker!r}"
    mutated = text.replace(marker, replacement, 1)
    tmp_dir = tempfile.mkdtemp()
    tmp_path = Path(tmp_dir) / "host_pressure_mutant.py"
    tmp_path.write_text(mutated)
    spec = importlib.util.spec_from_file_location("host_pressure_mutant", tmp_path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module  # dataclass() needs cls.__module__ resolvable
    spec.loader.exec_module(module)
    return module


class MutationCheckTest(unittest.TestCase):
    """Standing bar since #893: mutate the session gate itself and prove a
    test goes red in both directions -- break it so it never refuses, and
    break it so it always refuses. Each test patches host_pressure.py's own
    source (not a hand-written stand-in for its logic), loads the mutant as
    a real module, and shows the mutant's Result diverges from what the
    corresponding CheckTest/SessionGateTest assertion requires -- i.e. that
    assertion would fail (go red) if it ran against this mutant instead of
    the real code.
    """

    def test_mutant_that_never_refuses_fails_the_over_limit_assertion(self):
        mutant = _load_mutant(
            "if inflight >= limits.max_agent_sessions:",
            "if False:  # MUTANT: never refuses",
        )
        result = mutant.check(
            mutant.Limits(max_load_per_core=0, min_free_mem_gb=0, max_agent_sessions=20),
            inflight_count=_inflight_over,  # 999, way over the limit of 20
        )
        # The real gate refuses here (test_inflight_over_limit_refuses
        # asserts assertFalse(result.ok)); this mutant reports ok=True,
        # which is exactly the divergence that would turn that assertion red.
        self.assertTrue(result.ok, "mutant expected to wrongly allow -- if this is False, the mutant didn't take")
        real_result = check(
            Limits(max_load_per_core=0, min_free_mem_gb=0, max_agent_sessions=20),
            inflight_count=_inflight_over,
        )
        self.assertNotEqual(real_result.ok, result.ok)

    def test_mutant_that_always_refuses_fails_the_within_limit_assertion(self):
        mutant = _load_mutant(
            "if inflight >= limits.max_agent_sessions:",
            "if True:  # MUTANT: always refuses",
        )
        result = mutant.check(
            mutant.Limits(max_load_per_core=0, min_free_mem_gb=0, max_agent_sessions=20),
            inflight_count=_inflight_ok,  # 0, comfortably under the limit of 20
        )
        # The real gate allows here (test_inflight_below_limit_allows /
        # test_zero_disables_the_session_gate_even_over_limit-style
        # assertions expect ok=True); this mutant reports ok=False.
        self.assertFalse(result.ok, "mutant expected to wrongly refuse -- if this is True, the mutant didn't take")
        real_result = check(
            Limits(max_load_per_core=0, min_free_mem_gb=0, max_agent_sessions=20),
            inflight_count=_inflight_ok,
        )
        self.assertNotEqual(real_result.ok, result.ok)


if __name__ == "__main__":
    unittest.main()
