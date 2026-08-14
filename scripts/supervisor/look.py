"""`look` -- let an agent see the pane it is building, instead of guessing.

#110: agents building the TUI have been describing it from a PR body, not
from running it -- the first pass was called "a curses TUI, selectable,
enter-to-jump" and Jon's answer was "I dont see a TUI. its looks like a shell
script." That is structural blindness, not carelessness: nothing in this
estate let an agent look at the frame it had just produced.

Two things are already measured, not assumed:

  - `tmux capture-pane -p -e` returns the FULL rendered frame, truecolor
    escapes included. That is not a downgrade from a screenshot -- for
    diagnosing rendering it is *better*, because it yields exact byte values
    instead of pixels. #109's reverse-video selection defect was found and
    fixed this way, with no visual at all:
        $ tmux capture-pane -p -e -t director:qa-lanebar-go | grep -cF $'\\e[7m'
        1        # before the fix
        0        # after the fix
    `annotate()` below generalizes that one grep into a structured per-run
    colour breakdown, so a lane does not have to hand-craft a new grep for
    every defect shape.

  - `screencapture` returns nothing here: these agents run with no attached
    display. Do not reach for it, and do not add a silent fallback that
    produces an empty image -- an instrument that cannot see a thing must
    say so, not stand in for the thing being absent (CLAUDE.md, "the failure
    mode this codebase produces most").

A first pass here reasoned "no PNG renderer is installed, and pinning one
(`ansisvg`, `termshot`, `agg`) would add a dependency this estate has not
measured" and stopped at annotated text. That reasoning was wrong, not
cautious -- it never checked, and the answer was already sitting one look
away: `capture-pane -e` **is** the DOM (the Playwright analogy is exact, not
a metaphor), headless Chrome is already installed, and a ~60-line ANSI-to-SVG
walk is all the "renderer" a screenshot needs. `termshot.py` (posted on #110,
copied in here rather than rewritten) does that walk; `png` below carries
its SVG the rest of the way to a PNG via `chrome --headless --screenshot`.
Annotated text (below) stays as the zero-dependency fallback for a target
with no Chrome on PATH -- degrade to a stated absence, never a guess.

Four capabilities, matching #110's asks:

  capture   read-only.  Print a pane's frame, plain or with its escapes,
                         or its per-run colour annotation.
  png       read-only.  Render a pane to an actual screenshot -- what Jon
                         sees, not a description of it.
  navigate  MUTATES.     Send keys, settle, then capture -- so a lane can
                          drive the UI it is testing, not just observe one
                          frame of it.
  frames    read-only.  Capture the same pane N times and diff the frames.
                          A static frame where motion was claimed is a defect
                          the agent can catch itself -- `--assert-motion`
                          turns that from an assertion into an exit code.

`capture`, `png` and `frames` never send input; only `navigate` does, and its
own name says so.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time

import termshot

DEFAULT_TIMEOUT = 10.0
PNG_RENDER_TIMEOUT = 30.0

# Probed in order; the first that resolves wins. Bare names are looked up on
# PATH (the VPS/Linux shape -- ubuntu-latest's own runners ship one of
# these); the absolute path is macOS's app-bundle layout, which is never on
# PATH. LOOK_CHROME_BIN overrides both, for a host that has one installed
# somewhere else entirely.
_CHROME_CANDIDATES = (
    "google-chrome",
    "google-chrome-stable",
    "chromium",
    "chromium-browser",
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
)

# One line of a captured frame carries only SGR sequences (ESC [ ... m) --
# capture-pane's escaped output emits colour, not cursor movement or other
# CSI finals. Recognising the general CSI shape and only ACTING on 'm'
# means an unexpected final byte is dropped from the text (it painted
# nothing visible) rather than corrupting the run it sits next to.
_CSI_RE = re.compile(r"(\x1b\[[0-9;]*[A-Za-z])")
_SGR_RE = re.compile(r"\x1b\[([0-9;]*)m")


def _default_state():
    return {"bold": False, "reverse": False, "fg": None, "bg": None}


def _basic_color(n, bright=False):
    names = ["black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"]
    name = names[n] if 0 <= n < len(names) else str(n)
    return f"bright-{name}" if bright else name


def _apply_sgr(state, params):
    state = dict(state)
    i, n = 0, len(params)
    while i < n:
        code = params[i]
        if code == 0:
            state = _default_state()
        elif code == 1:
            state["bold"] = True
        elif code == 22:
            state["bold"] = False
        elif code == 7:
            state["reverse"] = True
        elif code == 27:
            state["reverse"] = False
        elif 30 <= code <= 37:
            state["fg"] = _basic_color(code - 30)
        elif code == 38:
            if i + 2 < n and params[i + 1] == 5:
                state["fg"] = f"256:{params[i + 2]}"
                i += 2
            elif i + 4 < n and params[i + 1] == 2:
                r, g, b = params[i + 2], params[i + 3], params[i + 4]
                state["fg"] = f"{r},{g},{b}"
                i += 4
        elif code == 39:
            state["fg"] = None
        elif 40 <= code <= 47:
            state["bg"] = _basic_color(code - 40)
        elif code == 48:
            if i + 2 < n and params[i + 1] == 5:
                state["bg"] = f"256:{params[i + 2]}"
                i += 2
            elif i + 4 < n and params[i + 1] == 2:
                r, g, b = params[i + 2], params[i + 3], params[i + 4]
                state["bg"] = f"{r},{g},{b}"
                i += 4
        elif code == 49:
            state["bg"] = None
        elif 90 <= code <= 97:
            state["fg"] = _basic_color(code - 90, bright=True)
        elif 100 <= code <= 107:
            state["bg"] = _basic_color(code - 100, bright=True)
        # anything else (underline, blink, ...) is not tracked: nothing in
        # this estate's UI defects so far has hinged on it, and tracking
        # attributes nobody reads is exactly the "instrument nobody calls"
        # shape CLAUDE.md warns about.
        i += 1
    return state


def _attrs_summary(state):
    parts = []
    if state["reverse"]:
        parts.append("reverse")
    if state["bold"]:
        parts.append("bold")
    if state["fg"]:
        parts.append(f"fg={state['fg']}")
    if state["bg"]:
        parts.append(f"bg={state['bg']}")
    return ",".join(parts) if parts else "plain"


def parse_line_runs(line):
    """Split one escaped capture-pane line into (text, state) runs.

    Only non-empty text runs are returned -- a colour change with no text
    between it and the next change paints nothing, so it is not a run.
    """
    state = _default_state()
    runs = []
    current = []
    for token in _CSI_RE.split(line):
        if not token:
            continue
        m = _SGR_RE.fullmatch(token)
        if m:
            if current:
                runs.append(("".join(current), state))
                current = []
            params_str = m.group(1)
            params = [int(p) if p else 0 for p in params_str.split(";")] if params_str else [0]
            state = _apply_sgr(state, params)
            continue
        if token.startswith("\x1b["):
            # a CSI sequence with a final byte other than 'm' -- not colour,
            # dropped rather than treated as text (see _CSI_RE comment).
            continue
        current.append(token)
    if current:
        runs.append(("".join(current), state))
    return runs


def annotate(escaped_text):
    """Decode an escaped capture-pane frame into a per-run colour report.

    This is the "text + per-cell colour" reading of a frame #110 asks for.
    `png` (below) is the actual-screenshot reading; this stays useful as a
    zero-dependency fallback when no headless Chrome is on PATH.
    """
    out_lines = []
    for row, line in enumerate(escaped_text.splitlines()):
        runs = parse_line_runs(line)
        if not runs:
            out_lines.append(f"row {row:>3}: (blank)")
            continue
        rendered = " | ".join(f"{text!r} [{_attrs_summary(state)}]" for text, state in runs)
        out_lines.append(f"row {row:>3}: {rendered}")
    return "\n".join(out_lines) + "\n"


def _tmux(args, runner=None, timeout=DEFAULT_TIMEOUT):
    run = runner or subprocess.run
    proc = run(["tmux"] + args, capture_output=True, text=True, timeout=timeout)
    if proc.returncode != 0:
        raise RuntimeError(f"tmux {' '.join(args)} failed: {proc.stderr.strip()}")
    return proc.stdout


def capture_pane(target, escapes=False, runner=None, timeout=DEFAULT_TIMEOUT):
    """Read-only. Never sends input -- capture-pane is a read, and this
    stays one (#110's constraint: "capturing must not disturb the pane")."""
    args = ["capture-pane", "-p"]
    if escapes:
        args.append("-e")
    args += ["-t", target]
    return _tmux(args, runner=runner, timeout=timeout)


def send_keys(target, keys, runner=None, timeout=DEFAULT_TIMEOUT):
    """MUTATES the pane. Only caller: navigate()."""
    _tmux(["send-keys", "-t", target, *keys], runner=runner, timeout=timeout)


def find_chrome_binary(env=None):
    """Return a usable headless-Chrome binary path, or None.

    None is a real answer, not a failure to check -- a caller that gets it
    must say "no Chrome found" and fall back to annotate(), not silently
    produce an empty image (the `screencapture` mistake this file's
    docstring warns about, one abstraction level up).
    """
    env = env if env is not None else os.environ
    override = env.get("LOOK_CHROME_BIN")
    if override:
        return override
    for candidate in _CHROME_CANDIDATES:
        if os.sep in candidate:
            if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
                return candidate
        else:
            found = shutil.which(candidate)
            if found:
                return found
    return None


def render_png(target, out_path, chrome_bin=None, runner=None, timeout=PNG_RENDER_TIMEOUT):
    """Read-only. Render a pane to an actual PNG screenshot.

    `termshot.render()` does the capture-pane read and the ANSI-to-SVG walk
    (posted on #110, copied in verbatim); this carries its SVG the rest of
    the way through headless Chrome. Raises RuntimeError with a clear reason
    -- no Chrome found, or Chrome itself failed -- rather than writing a
    partial or empty PNG.
    """
    chrome_bin = chrome_bin or find_chrome_binary()
    if not chrome_bin:
        raise RuntimeError(
            "look.py png: no headless Chrome found on PATH or at the macOS "
            "app-bundle path -- set LOOK_CHROME_BIN, or use `capture --annotate` instead"
        )
    tmp_dir = tempfile.mkdtemp(prefix="look-svg-")
    try:
        svg_path = os.path.join(tmp_dir, "frame.svg")
        _, rows, cols = termshot.render(target, svg_path)
        run = runner or subprocess.run
        proc = run(
            [
                chrome_bin,
                "--headless=new",
                "--disable-gpu",
                f"--screenshot={out_path}",
                f"--window-size={max(int(cols * 9), 200)},{max(int(rows * 18), 100)}",
                f"file://{os.path.abspath(svg_path)}",
            ],
            capture_output=True,
            text=True,
            timeout=timeout,
        )
        if proc.returncode != 0:
            raise RuntimeError(f"look.py png: headless Chrome failed: {proc.stderr.strip()}")
        return out_path
    finally:
        shutil.rmtree(tmp_dir, ignore_errors=True)


def navigate(target, keys, settle=0.3, escapes=False, runner=None, sleeper=None, timeout=DEFAULT_TIMEOUT):
    """Drive the pane, then capture what it looks like afterward.

    Without this, a lane can only observe a frame someone else drove into
    place. `settle` gives the pane time to redraw before the capture --
    too short and the frame is mid-repaint, which reads as a bug that is not
    one.
    """
    sleep = sleeper or time.sleep
    send_keys(target, keys, runner=runner, timeout=timeout)
    sleep(settle)
    return capture_pane(target, escapes=escapes, runner=runner, timeout=timeout)


def capture_frames(target, count, interval=0.5, escapes=False, runner=None, sleeper=None, timeout=DEFAULT_TIMEOUT):
    """Read-only. Capture the same pane `count` times, `interval` apart."""
    sleep = sleeper or time.sleep
    frames = []
    for i in range(count):
        frames.append(capture_pane(target, escapes=escapes, runner=runner, timeout=timeout))
        if i < count - 1:
            sleep(interval)
    return frames


def diff_frames(frames):
    """Pairwise line diff across captured frames.

    `motion` is the whole point: False means every frame was byte-identical
    -- the "static frame where motion was claimed" #110 asks to be able to
    catch, turned from an assertion into a measurement.
    """
    pairs = []
    for i in range(1, len(frames)):
        prev_lines = frames[i - 1].splitlines()
        cur_lines = frames[i].splitlines()
        changed = [
            ln
            for ln in range(max(len(prev_lines), len(cur_lines)))
            if (prev_lines[ln] if ln < len(prev_lines) else None)
            != (cur_lines[ln] if ln < len(cur_lines) else None)
        ]
        pairs.append({"from": i - 1, "to": i, "changed_lines": changed})
    return {"pairs": pairs, "motion": any(p["changed_lines"] for p in pairs)}


def render_frames_report(diff):
    lines = [f"captured {len(diff['pairs']) + 1} frame(s)"]
    for pair in diff["pairs"]:
        n = len(pair["changed_lines"])
        if n:
            rows = ", ".join(str(r) for r in pair["changed_lines"])
            lines.append(f"  frame {pair['from']} -> {pair['to']}: {n} row(s) changed (rows {rows})")
        else:
            lines.append(f"  frame {pair['from']} -> {pair['to']}: no change")
    if diff["motion"]:
        lines.append("verdict: MOTION DETECTED")
    else:
        lines.append("verdict: NO CHANGE across every frame -- static, not animated")
    return "\n".join(lines) + "\n"


def _split_keys(argv_keys):
    # argparse hands us one string per shell word; tmux send-keys already
    # treats each arg this way (literal text or a key name like Enter,
    # C-c) so no further splitting is needed here.
    return list(argv_keys)


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="look.py",
        description="Let an agent see the tmux pane it is building (#110).",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    p_cap = sub.add_parser("capture", help="read-only: print one frame")
    p_cap.add_argument("-t", "--target", required=True, help="tmux target, e.g. session:window.pane")
    p_cap.add_argument("--escapes", action="store_true", help="include truecolor/SGR escapes (capture-pane -e)")
    p_cap.add_argument("--annotate", action="store_true", help="decode escapes into a per-run colour report (implies --escapes)")

    p_png = sub.add_parser("png", help="read-only: render a pane to an actual PNG screenshot")
    p_png.add_argument("-t", "--target", required=True)
    p_png.add_argument("-o", "--out", required=True, help="output PNG path")
    p_png.add_argument("--chrome", default=None, help="headless Chrome binary (default: probe PATH, then LOOK_CHROME_BIN)")

    p_nav = sub.add_parser("navigate", help="MUTATES: send keys, then print the resulting frame")
    p_nav.add_argument("-t", "--target", required=True)
    p_nav.add_argument("keys", nargs="+", help="tmux send-keys arguments, e.g. j Enter or 'C-c'")
    p_nav.add_argument("--settle", type=float, default=0.3, help="seconds to wait before capturing (default 0.3)")
    p_nav.add_argument("--escapes", action="store_true")

    p_fr = sub.add_parser("frames", help="read-only: capture N frames and diff them (animation proof)")
    p_fr.add_argument("-t", "--target", required=True)
    p_fr.add_argument("--count", type=int, default=3)
    p_fr.add_argument("--interval", type=float, default=0.5, help="seconds between captures (default 0.5)")
    p_fr.add_argument("--escapes", action="store_true")
    p_fr.add_argument("--assert-motion", action="store_true", help="exit 1 if every frame is identical")
    p_fr.add_argument("--json", action="store_true")

    args = parser.parse_args(argv)

    if args.command == "capture":
        text = capture_pane(args.target, escapes=(args.escapes or args.annotate))
        sys.stdout.write(annotate(text) if args.annotate else text)
        return 0

    if args.command == "png":
        try:
            out = render_png(args.target, args.out, chrome_bin=args.chrome)
        except RuntimeError as exc:
            print(exc, file=sys.stderr)
            return 1
        print(f"look.py: wrote {out}")
        return 0

    if args.command == "navigate":
        text = navigate(args.target, _split_keys(args.keys), settle=args.settle, escapes=args.escapes)
        sys.stdout.write(text)
        return 0

    if args.command == "frames":
        if args.count < 2:
            print("look.py frames: --count must be >= 2 to diff anything", file=sys.stderr)
            return 2
        frames = capture_frames(args.target, args.count, interval=args.interval, escapes=args.escapes)
        diff = diff_frames(frames)
        if args.json:
            print(json.dumps(diff))
        else:
            sys.stdout.write(render_frames_report(diff))
        if args.assert_motion and not diff["motion"]:
            print(
                f"look.py: NO CHANGE across {len(frames)} frame(s) -- "
                "animation was claimed but not observed",
                file=sys.stderr,
            )
            return 1
        return 0

    return 2  # argparse's `required=True` makes this unreachable


if __name__ == "__main__":
    sys.exit(main())
