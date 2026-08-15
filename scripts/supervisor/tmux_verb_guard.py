"""Static guard: no test may create or destroy a tmux SESSION outside an
isolated socket.

agent-dotfiles#247 / agent-supervisor#258 shipped `assert_isolated_tmux()`
(tmux-isolation.sh) -- a RUNTIME check a test calls before a destructive
verb. agent-supervisor#180 found the gap that check cannot close by
itself: it only refuses a call that actually reaches it. A test that
reaches for `tmux new-session` directly, on a line that never calls
assert_isolated_tmux or otherwise sets up isolation, leaks on the DEFAULT
socket -- exactly the shape that put `bootstrap-test-52078` there, four
`free-N` bash windows dispatch.sh's own backfill logic would have offered
as capacity.

#247's guard covered the DESTRUCTIVE verbs (`kill-server`, `kill-session`,
`kill-window`). #180 is "creation was never covered": `new-session` puts a
foreign session in the live namespace just as surely as a destructive verb
misfires there, and nothing before this module checked for it.

This is the STATIC half, meant to run as part of the ordinary test suite
(see test_tmux_verb_guard.py) so an unisolated verb in a NEW test fails
`python -m unittest discover` the same way any other regression does --
no separate gate to remember to run.

Scope: `tests/supervisor/test_*.sh` only. Production scripts
(bootstrap-session.sh, lane-done.sh, restore.sh, ...) call these same verbs
against the REAL estate on purpose -- that is the supervisor's job -- and
are not in scope for a check about test isolation.

## What counts as isolated

Presence-based, like test_laneview_isolation.sh's own rule, but positional
rather than whole-file: a bash export takes effect for every line AFTER
it, not before, so a guarded verb is isolated only if the isolation
markers appear ABOVE it in the file, not merely somewhere in it. A file
that sets TMUX_TMPDIR at the top and then calls `tmux new-session` at the
bottom is genuinely isolated (the export is still in effect); a file that
calls it BEFORE isolation is ever established is not, even though the
markers appear later in the same file. Whole-file presence would miss
exactly that case -- a bad call landing ahead of an otherwise-correct
setup block -- so this checks the prefix up to and including each
candidate line instead of the file as a whole.

  A. TMUX_TMPDIR isolation (tmux-isolation.sh): by the verb's line, the
     file has sourced tmux-isolation.sh, set TMUX_TMPDIR, and called
     assert_isolated_tmux at least once.
  B. PATH-shim / explicit-socket isolation (test_restore.sh's shape): by
     the verb's line, the file has resolved the REAL tmux binary
     (`command -v tmux` / `which tmux`) and targeted it with an explicit
     `-L $SOCKET` at least once (directly or via a PATH-shim wrapper).

A verb line with neither technique satisfied above it is flagged -- one
Finding per occurrence, fail closed. A file the guard cannot decode is
also a Finding rather than a silent skip (#258's own "fail closed on
files it cannot decode" fix, restated here for this scanner).

## The one exception position alone gets wrong: cleanup functions

`cleanup() { ...; tmux kill-server; }` followed later by `trap cleanup
EXIT` is this suite's own idiom (test_inbox_poll_service.sh,
test_watchdog_launchd_relaunch.sh, ...). A function body's verb lines do
not execute where they are DEFINED -- they execute wherever the function
is later CALLED (directly, or via `trap NAME EXIT/INT/TERM`), which in
this idiom is always after isolation is established. Judging those lines
by their definition's line position produces exactly the false positive a
naive positional check would: flagging a cleanup trap that in fact never
runs before assert_isolated_tmux. Verb lines inside a `name() { ... }`
block are therefore judged as of that name's nearest CALL site instead of
their own line -- falling back to the definition's own end if the
function is never referenced (dead code some other check should catch,
not this one).
"""

import re
from dataclasses import dataclass
from pathlib import Path

CREATE_VERBS = ("new-session",)
DESTRUCTIVE_VERBS = ("kill-server", "kill-session", "kill-window")
GUARDED_VERBS = CREATE_VERBS + DESTRUCTIVE_VERBS

VERB_RE = re.compile(r"\btmux\b[^\n]*\b(" + "|".join(GUARDED_VERBS) + r")\b")
COMMENT_RE = re.compile(r"^\s*#")
FUNC_OPEN_RE = re.compile(r"^\s*(\w+)\s*\(\)\s*\{\s*$")

TECHNIQUE_A_MARKERS = (
    re.compile(r"tmux-isolation\.sh"),
    re.compile(r"\bTMUX_TMPDIR\b"),
    re.compile(r"\bassert_isolated_tmux\b"),
)
TECHNIQUE_B_MARKERS = (
    re.compile(r"command -v tmux|which tmux"),
    re.compile(r'-L\s+"?\$\{?SOCKET\}?"?'),
)


@dataclass
class Finding:
    file: str
    line: int
    verb: str
    snippet: str

    def __str__(self):
        return f"{self.file}:{self.line}: unisolated `tmux {self.verb}` — {self.snippet}"


def _is_isolated(text: str) -> bool:
    technique_a = all(m.search(text) for m in TECHNIQUE_A_MARKERS)
    technique_b = all(m.search(text) for m in TECHNIQUE_B_MARKERS)
    return technique_a or technique_b


def _function_spans(lines):
    """Map each 1-indexed line to the 1-indexed line its containing
    `name() { ... }` block should be judged at instead: the nearest later
    reference to that function's name (a `trap NAME ...` or a bare call),
    or the block's own closing line if the function is never referenced.
    Lines outside any function body are absent from the returned dict."""
    # Depth-based, not "first `}` wins": a cleanup() body in this suite
    # routinely nests its own `cond && { ...; }` block (parameter
    # expansions like "${VAR:-}" are brace-balanced within one line and so
    # net zero against this), and the first inner `}` is not the function's
    # own close.
    spans = []  # (name, start, end) with start/end 1-indexed, inclusive
    open_name = None
    open_at = None
    depth = 0
    for lineno, line in enumerate(lines, start=1):
        if open_name is None:
            m = FUNC_OPEN_RE.match(line)
            if m:
                open_name, open_at = m.group(1), lineno
                depth = line.count("{") - line.count("}")
            continue
        depth += line.count("{") - line.count("}")
        if depth <= 0:
            spans.append((open_name, open_at, lineno))
            open_name = open_at = None

    remap = {}
    for name, start, end in spans:
        call_re = re.compile(r"\b" + re.escape(name) + r"\b")
        trap_re = re.compile(r"\btrap\b[^\n]*\b" + re.escape(name) + r"\b[^\n]*\b(EXIT|INT|TERM)\b")
        call_at = end
        for lineno, line in enumerate(lines, start=1):
            if lineno <= end:
                continue
            if trap_re.search(line):
                # An EXIT/INT/TERM trap fires last, after everything else
                # in the script -- including isolation setup that textually
                # follows the `trap` line itself, which is this suite's own
                # idiom (assert_isolated_tmux called right after `trap
                # cleanup EXIT`, not before it). Defer to end of file.
                call_at = len(lines)
                break
            if call_re.search(line):
                call_at = lineno
                break
        for lineno in range(start, end + 1):
            remap[lineno] = call_at
    return remap


def scan_file(path: Path) -> list:
    try:
        text = path.read_text(encoding="utf-8")
    except (UnicodeDecodeError, OSError) as exc:
        return [Finding(str(path), 0, "decode-error", str(exc))]

    lines = text.splitlines()
    remap = _function_spans(lines)
    findings = []
    for lineno, line in enumerate(lines, start=1):
        if COMMENT_RE.match(line):
            continue
        m = VERB_RE.search(line)
        if not m:
            continue
        effective_line = remap.get(lineno, lineno)
        prefix = "\n".join(lines[:effective_line])  # up to and including the effective line
        if _is_isolated(prefix):
            continue
        findings.append(Finding(str(path), lineno, m.group(1), line.strip()))
    return findings


def scan(paths) -> list:
    findings = []
    for p in paths:
        findings.extend(scan_file(Path(p)))
    return findings


if __name__ == "__main__":
    import sys

    root = Path(__file__).resolve().parents[2] / "tests" / "supervisor"
    results = scan(sorted(root.glob("test_*.sh")))
    for f in results:
        print(f)
    sys.exit(1 if results else 0)
