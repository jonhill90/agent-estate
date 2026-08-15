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
