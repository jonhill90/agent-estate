"""Unit tests for scripts/supervisor/look.py (#110).

Real-tmux behaviour (capture actually reflecting a pane, navigate actually
landing a key, frames actually proving motion) is covered end-to-end by
tests/supervisor/test_look.sh, which drives a real isolated tmux server --
that is the only place capture-pane's exact escape byte shapes can be
trusted. These tests cover the pure logic (SGR decoding, diffing, the CLI's
wiring) with fake tmux calls, so a regression there is caught in
milliseconds instead of needing a tmux server at all.
"""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "scripts" / "supervisor"))

import look  # noqa: E402


# Bytes actually observed from a real tmux server (verified live while
# building this): `printf '\033[7mSELECTED\033[0m plain \033[38;2;51;71;91mcolored\033[0m'`
# captured with `tmux capture-pane -p -e`. Pinning the fixture to a real
# capture, not a hand-written guess, is what keeps this test honest about
# the shape look.py actually has to parse.
REAL_FRAME_LINE = "\x1b[7mSELECTED\x1b[0m plain \x1b[38;2;51;71;91mcolored\x1b[0m"


class FakeRunner:
    """Stands in for subprocess.run: records every tmux invocation and
    returns a scripted stdout for it."""

    def __init__(self, stdout=""):
        self.calls = []
        self.stdout = stdout

    def __call__(self, cmd, **kwargs):
        self.calls.append(cmd)
        return _Proc(self.stdout)


class _Proc:
    def __init__(self, stdout, returncode=0, stderr=""):
        self.stdout = stdout
        self.returncode = returncode
        self.stderr = stderr


class AnnotateTests(unittest.TestCase):
    def test_reverse_and_truecolor_runs_are_decoded(self):
        runs = look.parse_line_runs(REAL_FRAME_LINE)
        self.assertEqual(
            runs,
            [
                ("SELECTED", {"bold": False, "reverse": True, "fg": None, "bg": None}),
                (" plain ", {"bold": False, "reverse": False, "fg": None, "bg": None}),
                ("colored", {"bold": False, "reverse": False, "fg": "51,71,91", "bg": None}),
            ],
        )

    def test_annotate_reports_reverse_video_the_way_109_was_diagnosed(self):
        # #109's whole diagnosis was `grep -cF $'\e[7m'`. annotate() must
        # make that same fact legible without a hand grep.
        report = look.annotate(REAL_FRAME_LINE)
        self.assertIn("reverse", report)
        self.assertIn("fg=51,71,91", report)

    def test_blank_line_is_reported_not_dropped(self):
        report = look.annotate("line one\n\nline three")
        self.assertIn("row   1: (blank)", report)

    def test_reset_code_clears_prior_state(self):
        runs = look.parse_line_runs("\x1b[7mA\x1b[0mB")
        self.assertEqual(runs[1], ("B", look._default_state()))

    def test_256_color_is_decoded(self):
        runs = look.parse_line_runs("\x1b[38;5;76mX")
        self.assertEqual(runs[0][1]["fg"], "256:76")

    def test_non_sgr_csi_is_dropped_not_left_as_text(self):
        # A cursor-movement CSI (not 'm'-terminated) paints nothing and
        # must not leak into a run's text.
        runs = look.parse_line_runs("\x1b[2Kcleared")
        self.assertEqual(runs, [("cleared", look._default_state())])


class CapturePaneTests(unittest.TestCase):
    def test_plain_capture_omits_e_flag(self):
        runner = FakeRunner(stdout="frame\n")
        out = look.capture_pane("sess:0", escapes=False, runner=runner)
        self.assertEqual(out, "frame\n")
        self.assertEqual(runner.calls[0], ["tmux", "capture-pane", "-p", "-t", "sess:0"])

    def test_escaped_capture_includes_e_flag(self):
        runner = FakeRunner(stdout="frame\n")
        look.capture_pane("sess:0", escapes=True, runner=runner)
        self.assertEqual(runner.calls[0], ["tmux", "capture-pane", "-p", "-e", "-t", "sess:0"])

    def test_nonzero_exit_raises(self):
        def failing_runner(cmd, **kwargs):
            return _Proc("", returncode=1, stderr="no such pane")
        with self.assertRaises(RuntimeError):
            look.capture_pane("sess:9", runner=failing_runner)


class NavigateTests(unittest.TestCase):
    def test_navigate_sends_then_settles_then_captures_in_order(self):
        events = []

        def runner(cmd, **kwargs):
            events.append(("tmux", cmd))
            return _Proc("after\n")

        def sleeper(seconds):
            events.append(("sleep", seconds))

        out = look.navigate("sess:0", ["j", "Enter"], settle=0.7, runner=runner, sleeper=sleeper)

        self.assertEqual(out, "after\n")
        kinds = [e[0] for e in events]
        self.assertEqual(kinds, ["tmux", "sleep", "tmux"])
        self.assertEqual(events[0][1], ["tmux", "send-keys", "-t", "sess:0", "j", "Enter"])
        self.assertEqual(events[1][1], 0.7)
        self.assertEqual(events[2][1], ["tmux", "capture-pane", "-p", "-t", "sess:0"])


class FramesTests(unittest.TestCase):
    def test_capture_frames_sleeps_between_but_not_after_the_last(self):
        stdouts = iter(["f1", "f2", "f3"])
        sleeps = []

        def runner(cmd, **kwargs):
            return _Proc(next(stdouts))

        frames = look.capture_frames("sess:0", 3, interval=0.25, runner=runner, sleeper=sleeps.append)

        self.assertEqual(frames, ["f1", "f2", "f3"])
        self.assertEqual(sleeps, [0.25, 0.25])

    def test_diff_frames_true_when_content_changes(self):
        diff = look.diff_frames(["a\nb", "a\nc", "a\nc"])
        self.assertTrue(diff["motion"])
        self.assertEqual(diff["pairs"][0]["changed_lines"], [1])
        self.assertEqual(diff["pairs"][1]["changed_lines"], [])

    def test_diff_frames_false_for_identical_frames(self):
        # The exact defect #110 wants catchable: motion claimed, none seen.
        diff = look.diff_frames(["same", "same", "same"])
        self.assertFalse(diff["motion"])
        for pair in diff["pairs"]:
            self.assertEqual(pair["changed_lines"], [])

    def test_diff_frames_handles_frames_of_different_line_counts(self):
        diff = look.diff_frames(["a\nb", "a"])
        self.assertTrue(diff["motion"])
        self.assertEqual(diff["pairs"][0]["changed_lines"], [1])

    def test_render_report_names_the_static_verdict_explicitly(self):
        report = look.render_frames_report(look.diff_frames(["x", "x"]))
        self.assertIn("NO CHANGE", report)
        self.assertIn("static, not animated", report)

    def test_render_report_names_the_motion_verdict_explicitly(self):
        report = look.render_frames_report(look.diff_frames(["x", "y"]))
        self.assertIn("MOTION DETECTED", report)


class FindChromeBinaryTests(unittest.TestCase):
    def test_env_override_wins_even_over_a_real_binary(self):
        self.assertEqual(
            look.find_chrome_binary(env={"LOOK_CHROME_BIN": "/custom/chrome"}),
            "/custom/chrome",
        )

    def test_none_when_nothing_is_found(self):
        real_which = look.shutil.which
        real_isfile = look.os.path.isfile
        look.shutil.which = lambda name: None
        look.os.path.isfile = lambda path: False
        try:
            self.assertIsNone(look.find_chrome_binary(env={}))
        finally:
            look.shutil.which, look.os.path.isfile = real_which, real_isfile


class RenderPngTests(unittest.TestCase):
    def test_raises_a_clear_error_when_no_chrome_is_found(self):
        # chrome_bin=None means "go probe for one" -- on a machine that
        # genuinely has Chrome installed (this one does), the probe would
        # find it and mask the case this test exists for. Stub the probe
        # itself so the case holds regardless of what's actually on disk.
        real_find = look.find_chrome_binary
        look.find_chrome_binary = lambda: None
        try:
            with self.assertRaisesRegex(RuntimeError, "no headless Chrome found"):
                look.render_png("sess:0", "/tmp/out.png", chrome_bin=None, runner=lambda *a, **k: None)
        finally:
            look.find_chrome_binary = real_find

    def test_wires_termshot_render_into_a_chrome_screenshot_call(self):
        calls = []

        def fake_render(target, out):
            calls.append(("render", target, out))
            with open(out, "w") as f:
                f.write("<svg/>")
            return out, 5, 20  # rows, cols

        def fake_runner(cmd, **kwargs):
            calls.append(("chrome", cmd))
            return _Proc("", returncode=0)

        real_render = look.termshot.render
        look.termshot.render = fake_render
        try:
            out = look.render_png("sess:0", "/tmp/out.png", chrome_bin="/opt/chrome", runner=fake_runner)
        finally:
            look.termshot.render = real_render

        self.assertEqual(out, "/tmp/out.png")
        self.assertEqual(calls[0][0], "render")
        self.assertEqual(calls[0][1], "sess:0")
        chrome_cmd = calls[1][1]
        self.assertEqual(chrome_cmd[0], "/opt/chrome")
        self.assertIn("--screenshot=/tmp/out.png", chrome_cmd)
        self.assertTrue(any(a.startswith("file://") for a in chrome_cmd))

    def test_nonzero_chrome_exit_raises_with_stderr(self):
        def fake_render(target, out):
            with open(out, "w") as f:
                f.write("<svg/>")
            return out, 5, 20

        def failing_runner(cmd, **kwargs):
            return _Proc("", returncode=1, stderr="chrome exploded")

        real_render = look.termshot.render
        look.termshot.render = fake_render
        try:
            with self.assertRaisesRegex(RuntimeError, "chrome exploded"):
                look.render_png("sess:0", "/tmp/out.png", chrome_bin="/opt/chrome", runner=failing_runner)
        finally:
            look.termshot.render = real_render


class CliTests(unittest.TestCase):
    def test_frames_assert_motion_exits_nonzero_on_a_static_pane(self):
        # main() wires the CLI straight to subprocess.run / time.sleep (no
        # runner/sleeper plumbed through argparse) -- patch those directly
        # to keep this hermetic, same as the rest of the suite requires
        # (no real tmux server, no real waiting).
        real_run, real_sleep = look.subprocess.run, look.time.sleep
        look.subprocess.run = lambda cmd, **kwargs: _Proc("same frame\n")
        look.time.sleep = lambda s: None
        try:
            rc = look.main(["frames", "-t", "sess:0", "--count", "2", "--assert-motion"])
        finally:
            look.subprocess.run, look.time.sleep = real_run, real_sleep
        self.assertEqual(rc, 1)

    def test_frames_rejects_count_below_two(self):
        rc = look.main(["frames", "-t", "sess:0", "--count", "1"])
        self.assertEqual(rc, 2)


if __name__ == "__main__":
    unittest.main()
