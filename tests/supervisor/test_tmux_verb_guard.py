"""agent-supervisor#180: extend #247/#258's destructive-tmux-verb guard to
session CREATION, and wire it into the suite CI already runs.

test_shell_suites.py established the pattern this follows: a check with no
caller is a documentation rule with a binary attached (CLAUDE.md's own
lesson, learned from acp_transport.py). tmux_verb_guard.py is the same
shape -- so it is exercised here, inside `python -m unittest discover -s
tests`, not left as a script nobody runs.

Two things are asserted:

  1. The guard's OWN logic is correct against small fixtures crafted to
     hit each edge (isolated / unisolated, technique A / technique B,
     verb in a comment, undecodable file).
  2. The REAL test suite passes it -- zero findings across every
     tests/supervisor/test_*.sh file that touches a create/destroy verb.
     This is #2 is the mutation check's other half: break isolation in a
     real test file and this assertion is what goes red.
"""

import sys
import tempfile
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
TESTS_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SUPERVISOR_DIR))

from tmux_verb_guard import scan, scan_file  # noqa: E402


class GuardLogicTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)

    def _write(self, name, text):
        p = Path(self.tmp.name) / name
        p.write_text(text)
        return p

    def test_unisolated_new_session_is_flagged(self):
        # The exact shape #180 found: creation with no isolation setup at
        # all anywhere in the file.
        p = self._write("test_x.sh", 'tmux new-session -d -s leaky\n')
        findings = scan_file(p)
        self.assertEqual(1, len(findings))
        self.assertEqual("new-session", findings[0].verb)

    def test_unisolated_destructive_verb_is_flagged(self):
        p = self._write("test_x.sh", 'tmux kill-server\n')
        findings = scan_file(p)
        self.assertEqual(1, len(findings))
        self.assertEqual("kill-server", findings[0].verb)

    def test_technique_a_tmux_tmpdir_clears_it(self):
        p = self._write(
            "test_x.sh",
            'source "$HERE/../../scripts/supervisor/tmux-isolation.sh"\n'
            'export TMUX_TMPDIR="$RT"\n'
            'assert_isolated_tmux || exit 1\n'
            'tmux new-session -d -s ok\n',
        )
        self.assertEqual([], scan_file(p))

    def test_technique_b_path_shim_socket_clears_it(self):
        p = self._write(
            "test_x.sh",
            'REAL_TMUX=$(command -v tmux)\n'
            'SOCKET="ad-test-$$"\n'
            'tmux new-session -d -s ok  # via PATH-shim to -L "$SOCKET"\n'
            'echo \'exec "$REAL_TMUX" -L "$SOCKET" "$@"\' > "$D/bin/tmux"\n',
        )
        self.assertEqual([], scan_file(p))

    def test_partial_technique_a_markers_still_flags(self):
        # TMUX_TMPDIR mentioned, but assert_isolated_tmux never called --
        # the exact incomplete-isolation shape a careless copy/paste takes.
        p = self._write(
            "test_x.sh",
            'export TMUX_TMPDIR="$RT"\n'
            'tmux new-session -d -s leaky\n',
        )
        findings = scan_file(p)
        self.assertEqual(1, len(findings))

    def test_verb_before_isolation_setup_is_flagged_even_if_the_file_later_isolates(self):
        # A bash export takes effect for lines AFTER it, not before. A file
        # that eventually sets up isolation is not isolated for a call that
        # runs ahead of that setup -- whole-file presence would miss this;
        # this is the shape that must go red.
        p = self._write(
            "test_x.sh",
            'tmux new-session -d -s leaky   # runs before isolation exists\n'
            'source "$HERE/../../scripts/supervisor/tmux-isolation.sh"\n'
            'export TMUX_TMPDIR="$RT"\n'
            'assert_isolated_tmux || exit 1\n',
        )
        findings = scan_file(p)
        self.assertEqual(1, len(findings))
        self.assertEqual("new-session", findings[0].verb)

    def test_cleanup_function_defined_before_isolation_but_trapped_after_is_clear(self):
        # This suite's own idiom (test_inbox_poll_service.sh,
        # test_watchdog_launchd_relaunch.sh): a multi-line cleanup()
        # function is DEFINED (and so textually contains a destructive
        # verb) before isolation is set up, but only EXECUTES later, via
        # `trap cleanup EXIT`, by which point isolation is established.
        # Naive line position would flag this; it must not.
        p = self._write(
            "test_x.sh",
            'cleanup() {\n'
            '  tmux kill-server >/dev/null 2>&1\n'
            '}\n'
            'source "$HERE/../../scripts/supervisor/tmux-isolation.sh"\n'
            'export TMUX_TMPDIR="$RT"\n'
            'assert_isolated_tmux || exit 1\n'
            'trap cleanup EXIT\n',
        )
        self.assertEqual([], scan_file(p))

    def test_cleanup_function_never_trapped_falls_back_to_definition_and_flags(self):
        # Same shape, but nothing ever calls or traps `cleanup` -- there is
        # no later point to defer judgment to, so this must still flag
        # rather than silently trust an unreferenced function.
        p = self._write(
            "test_x.sh",
            'cleanup() {\n'
            '  tmux kill-server >/dev/null 2>&1\n'
            '}\n'
            'source "$HERE/../../scripts/supervisor/tmux-isolation.sh"\n'
            'export TMUX_TMPDIR="$RT"\n'
            'assert_isolated_tmux || exit 1\n',
        )
        findings = scan_file(p)
        self.assertEqual(1, len(findings))

    def test_verb_after_isolation_setup_is_clear_even_appended_far_below(self):
        # The mirror case: isolation established early stays in effect for
        # every later line, including one appended well below it.
        p = self._write(
            "test_x.sh",
            'source "$HERE/../../scripts/supervisor/tmux-isolation.sh"\n'
            'export TMUX_TMPDIR="$RT"\n'
            'assert_isolated_tmux || exit 1\n'
            '\n' * 5 +
            'tmux new-session -d -s ok\n',
        )
        self.assertEqual([], scan_file(p))

    def test_verb_named_only_in_a_comment_is_not_flagged(self):
        p = self._write("test_x.sh", "# never call tmux new-session bare\n")
        self.assertEqual([], scan_file(p))

    def test_file_with_no_guarded_verb_needs_no_isolation_evidence(self):
        p = self._write("test_x.sh", "tmux list-windows -t foo\n")
        self.assertEqual([], scan_file(p))

    def test_undecodable_file_fails_closed_as_a_finding(self):
        p = Path(self.tmp.name) / "test_x.sh"
        p.write_bytes(b"tmux new-session -d -s \xff\xfe\n")
        findings = scan_file(p)
        self.assertEqual(1, len(findings))
        self.assertEqual("decode-error", findings[0].verb)

    def test_scan_aggregates_across_multiple_files(self):
        a = self._write("test_a.sh", "tmux kill-server\n")
        b = self._write("test_b.sh", "tmux list-windows -t foo\n")
        findings = scan([a, b])
        self.assertEqual(1, len(findings))
        self.assertEqual(str(a), findings[0].file)


class GuardMarkerRemapClosesGapTests(unittest.TestCase):
    """agent-supervisor#185's REQUEST CHANGES: a marker's text presence in
    the prefix used to be trusted regardless of whether the code path
    holding it had run by the verb -- including a named helper that is
    DEFINED above the verb but not CALLED until after it. This is now
    remapped through the same `_function_spans` machinery verb lines
    already use, and should flip from evading to caught."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)

    def _write(self, name, text):
        p = Path(self.tmp.name) / name
        p.write_text(text)
        return p

    def test_helper_defined_above_verb_but_called_after_it_is_now_flagged(self):
        # #185's evading fixture: setup_isolation() is textually above the
        # verb, so a whole-prefix presence check reads it as isolation
        # already in effect -- but it is not CALLED until after the verb,
        # so the verb genuinely runs unisolated.
        p = self._write(
            "test_x.sh",
            'setup_isolation() {\n'
            '  source "$HERE/../../scripts/supervisor/tmux-isolation.sh"\n'
            '  export TMUX_TMPDIR="$RT"\n'
            '  assert_isolated_tmux || exit 1\n'
            '}\n'
            'tmux new-session -d -s leaky\n'
            'setup_isolation\n',
        )
        findings = scan_file(p)
        self.assertEqual(1, len(findings))
        self.assertEqual("new-session", findings[0].verb)

    def test_helper_defined_and_called_above_the_verb_still_clears_it(self):
        # The mirror case: same helper shape, but called BEFORE the verb --
        # genuinely isolated, and must stay clear.
        p = self._write(
            "test_x.sh",
            'setup_isolation() {\n'
            '  source "$HERE/../../scripts/supervisor/tmux-isolation.sh"\n'
            '  export TMUX_TMPDIR="$RT"\n'
            '  assert_isolated_tmux || exit 1\n'
            '}\n'
            'setup_isolation\n'
            'tmux new-session -d -s ok\n',
        )
        self.assertEqual([], scan_file(p))

    def test_marker_reached_only_through_nested_call_still_flags(self):
        # #185's follow-up finding: `inner` is never referenced directly --
        # only `outer` is, and `outer` is called AFTER the verb.
        # `_function_spans` cannot resolve `inner`'s reachability (it only
        # looks for a direct textual reference to `inner`, and the one
        # inside `outer`'s body sits before `inner`'s own close), so this
        # used to fall back to "judge at inner's own end" -- which reads as
        # isolation already in effect and wrongly clears the verb. The
        # verb genuinely runs unisolated; this must flag.
        p = self._write(
            "test_x.sh",
            'outer() {\n'
            '  inner\n'
            '}\n'
            'inner() {\n'
            '  source "$HERE/../../scripts/supervisor/tmux-isolation.sh"\n'
            '  export TMUX_TMPDIR="$RT"\n'
            '  assert_isolated_tmux || exit 1\n'
            '}\n'
            'tmux new-session -d -s leaky\n'
            'outer\n',
        )
        findings = scan_file(p)
        self.assertEqual(1, len(findings))
        self.assertEqual("new-session", findings[0].verb)

    def test_marker_in_helper_never_called_at_all_still_flags(self):
        # The "never called" variant of the same shape: neither `outer`
        # nor `inner` is referenced anywhere. Must flag for the same
        # reason a top-level unreferenced helper already did before this
        # fix -- unresolved reachability is not isolation in effect.
        p = self._write(
            "test_x.sh",
            'outer() {\n'
            '  inner\n'
            '}\n'
            'inner() {\n'
            '  source "$HERE/../../scripts/supervisor/tmux-isolation.sh"\n'
            '  export TMUX_TMPDIR="$RT"\n'
            '  assert_isolated_tmux || exit 1\n'
            '}\n'
            'tmux new-session -d -s leaky\n',
        )
        findings = scan_file(p)
        self.assertEqual(1, len(findings))
        self.assertEqual("new-session", findings[0].verb)

    def test_single_level_helper_still_clears_when_called_before_verb(self):
        # Mirror-case regression check for the fail-closed marker default:
        # a single-level helper (no nesting) directly called before the
        # verb must still resolve normally and stay clear -- the
        # fail-closed default only bites when reachability genuinely
        # cannot be established, not every helper call.
        p = self._write(
            "test_x.sh",
            'setup_isolation() {\n'
            '  source "$HERE/../../scripts/supervisor/tmux-isolation.sh"\n'
            '  export TMUX_TMPDIR="$RT"\n'
            '  assert_isolated_tmux || exit 1\n'
            '}\n'
            'setup_isolation\n'
            'tmux kill-server\n',
        )
        self.assertEqual([], scan_file(p))


class GuardKnownGapsTests(unittest.TestCase):
    """Pins agent-supervisor#185's remaining evading fixtures as documented,
    tracked gaps -- not fixed in this pass (see tmux_verb_guard.py's "Known
    gaps" docstring section). Each assertion here is the CURRENT (wrong)
    behavior, on purpose: if one of these ever starts asserting a Finding
    where it currently asserts none, that gap has been closed and this
    test (and the docstring) should be updated together, not left silently
    stale."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)

    def _write(self, name, text):
        p = Path(self.tmp.name) / name
        p.write_text(text)
        return p

    def test_variable_holding_tmux_evades_entirely(self):
        p = self._write(
            "test_x.sh",
            'TMUX_BIN="tmux"\n'
            '"$TMUX_BIN" new-session -d -s leaky\n',
        )
        self.assertEqual([], scan_file(p))

    def test_which_tmux_captured_to_a_variable_evades_entirely(self):
        p = self._write(
            "test_x.sh",
            'TMUX_BIN=$(which tmux)\n'
            '"$TMUX_BIN" new-session -d -s leaky\n',
        )
        self.assertEqual([], scan_file(p))

    def test_alias_indirection_evades_entirely(self):
        p = self._write(
            "test_x.sh",
            'alias tx=tmux\n'
            'tx new-session -d -s leaky\n',
        )
        self.assertEqual([], scan_file(p))

    def test_line_continuation_evades_entirely(self):
        p = self._write(
            "test_x.sh",
            'tmux \\\n'
            '  new-session -d -s leaky\n',
        )
        self.assertEqual([], scan_file(p))

    def test_isolation_markers_inside_dead_conditional_still_clear_it(self):
        # `if false` never runs, so the markers below never actually
        # establish isolation -- but the guard is a regex, not an
        # interpreter, and cannot tell a dead branch from a live one.
        p = self._write(
            "test_x.sh",
            'if false; then\n'
            '  source "$HERE/../../scripts/supervisor/tmux-isolation.sh"\n'
            '  export TMUX_TMPDIR="$RT"\n'
            '  assert_isolated_tmux || exit 1\n'
            'fi\n'
            'tmux new-session -d -s leaky\n',
        )
        self.assertEqual([], scan_file(p))

    def test_nested_helper_called_before_verb_is_a_false_positive_now(self):
        # Accepted trade-off from closing the nested-call evasion: `inner`
        # is only reachable through `outer`, one level of indirection this
        # scanner does not walk. It cannot tell this genuinely-isolated
        # case (outer called BEFORE the verb) apart from the evading one
        # (outer called after), so both now flag -- a false positive here,
        # on purpose, in exchange for no longer being fail-open on the
        # evading case. If this ever starts asserting `[]`, the scanner
        # learned to walk nested calls and this pin (and the "Known gaps"
        # docstring entry) should be updated together.
        p = self._write(
            "test_x.sh",
            'outer() {\n'
            '  inner\n'
            '}\n'
            'inner() {\n'
            '  source "$HERE/../../scripts/supervisor/tmux-isolation.sh"\n'
            '  export TMUX_TMPDIR="$RT"\n'
            '  assert_isolated_tmux || exit 1\n'
            '}\n'
            'outer\n'
            'tmux new-session -d -s ok\n',
        )
        findings = scan_file(p)
        self.assertEqual(1, len(findings))


class RealSuiteIsIsolatedTests(unittest.TestCase):
    def test_no_unisolated_create_or_destroy_verb_in_any_shell_test(self):
        suites = sorted(TESTS_DIR.glob("test_*.sh"))
        self.assertTrue(suites, f"no test_*.sh found under {TESTS_DIR}")
        findings = scan(suites)
        self.assertEqual(
            [],
            findings,
            "unisolated tmux create/destroy verb(s):\n" + "\n".join(str(f) for f in findings),
        )


if __name__ == "__main__":
    unittest.main()
