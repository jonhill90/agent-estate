#!/bin/bash
# laneview implementation: a curses TUI Jon owns -- no third-party program,
# no daemon, nothing running when it is not on screen.
#
# agent-dotfiles#178 / jonhill90/agent-supervisor#7: Jon asked for "a TUI he
# owns" as one of the two viewers, distinct from opensessions.sh (which
# drives someone else's program). This is that renderer.
#
# Usage: tui.sh <session> <lanes.sh --json output>   (called by laneview.sh)
#
# CONTRACT (laneview/README.md):
#   1. Read, never write. Every screen this renders comes from the json it
#      was handed, or from re-asking lanes.sh --json for a fresh copy on the
#      next tick -- lanes.sh remains the one reader of tmux's measurements
#      and the ledger. This script never runs a tmux query lanes.sh does not
#      already run, and its one write to tmux (Enter, "jump to this lane")
#      is `select-window` / `switch-client` -- navigation, the same act a
#      human typing the command themselves would take. It never renames a
#      window, never claims a lane, never calls dispatch.sh or claim.sh.
#      The header line (agent-dotfiles#67) is read the same way: this
#      script shells out to `digest.sh --json` -- the SAME adapter boundary
#      as lanes.sh, structured output in, render out -- and never parses
#      digest.sh's human-readable text form or reads any of digest.sh's own
#      sources (watchdog.status, the ledger, gh) directly.
#   2. Degrade to absence, not staleness. If a refresh tick's `lanes.sh
#      --json` fails or comes back empty, the table is replaced by an
#      UNREACHABLE banner -- not left showing the last good frame -- and the
#      exit code the eventual quit returns is nonzero if the session was
#      unreachable when the program exited. `digest.sh` backs only the
#      header, not the table: a header that cannot be built says
#      "unavailable" and names why, in place of the header line, without
#      taking over the whole screen the way a lanes.sh failure does --
#      but it still counts toward the exit code the same way, because it
#      is still a backend of this renderer per rule 2's own text.
#   3. Cost nothing when unused. This is a plain script; nothing spawns it
#      but a human running `laneview.sh tui` or the tmux plugin's popup.
#   4. Name every state (README rule 4) -- STATE_GLYPH below is the same
#      12-entry map text.sh carries, so a state lanes.sh has never emitted
#      to this renderer still gets its own marker ("#"), never silently
#      reading as free/idle.
#
# When stdout is not a terminal (piped output, a test harness, `laneview.sh
# tui session > file`), curses cannot run at all -- it needs a real tty to
# draw into. Rather than fail, this renders ONE static frame in the same
# shape text.sh uses and exits, so the contract ("a renderer must degrade,
# never crash into a traceback") holds in a headless context too, and so
# this script is testable the same way text.sh is: call it directly with
# canned json and no tty.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LANES_SH="$HERE/../lanes.sh"
# Overridable so a test can pin the header to a known digest.sh shape (or an
# unreachable one) without depending on this machine's jq/gh/network state --
# real digest.sh output is legitimately environment-dependent, which is fine
# for a human running this live and wrong for a deterministic test.
DIGEST_SH="${LANEVIEW_TUI_DIGEST_SH:-$HERE/../digest.sh}"

SESSION="$1"
JSON="$2"

REFRESH_SECS="${LANEVIEW_TUI_REFRESH:-2}"

# Reviewed: `python3 -` reads the script itself from stdin, so a heredoc
# feeding it here also consumes stdin -- curses then has no real terminal
# to read keypresses from (every getch() came back -1, confirmed live: the
# static frame rendered fine, but j/k/enter never registered a single
# keystroke). The script goes to a temp file instead so stdin stays the
# actual tty all the way into curses.
PY_SRC="$(mktemp "${TMPDIR:-/tmp}/laneview-tui.XXXXXX.py")"
trap 'rm -f "$PY_SRC"' EXIT
cat > "$PY_SRC" <<'PY'
import json, os, subprocess, sys, time

session, raw, lanes_sh, refresh_secs, digest_sh = (
    sys.argv[1], sys.argv[2], sys.argv[3], float(sys.argv[4]), sys.argv[5],
)

# Same 12 states lanes.sh ships today (lanes.sh's header comment enumerates
# them); kept identical to text.sh's map on purpose -- two renderers naming
# the same state differently would be a second classifier by another name.
STATE_GLYPH = {
    "free": "-", "busy": "*", "hung": "!", "dead": "x",
    "menu-blocked": "?", "text-blocked": "?", "unsent": "~",
    "service": ".", "supervisor": ".", "unknown": "?",
    "scrolled": "^", "stale": "X",
}


def fetch_digest():
    """Ask digest.sh --json for the estate-level summary. Returns (digest, error).

    digest.sh's own exit code tracks whether ANY error was noted (agent-dotfiles#67
    read digest.sh directly to confirm this: `[ "${#ERRORS[@]}" -eq 0 ]` is its last
    line) -- a normal estate with one stale inbox message or an unreachable watchdog
    file still exits 1 while emitting a complete, valid JSON body with its own
    `errors` array naming what it could not read. Gating on returncode here would
    turn "digest.sh told you what it couldn't read" into "the header is unavailable"
    for cases that are not actually unavailable, so this only treats digest.sh as
    unreachable when it produced no parseable JSON at all -- the same distinction
    digest.sh itself draws in its `note_error` fields vs. a hard failure.
    """
    try:
        out = subprocess.run(
            ["bash", digest_sh, "--json"],
            capture_output=True, text=True, timeout=10,
            env={**os.environ, "LANES_SESSION": session},
        )
    except Exception as exc:  # noqa: BLE001 -- surfaced inline, not a crash
        return None, str(exc)
    if not out.stdout.strip():
        return None, (out.stderr.strip() or "digest.sh --json produced no output")
    try:
        return json.loads(out.stdout), None
    except json.JSONDecodeError as exc:
        return None, f"digest.sh --json produced unparseable output: {exc}"


def format_digest_line(digest, error):
    if error is not None:
        return f"digest: unavailable -- {error}"
    prs = digest.get("prs") or []
    verdicts = {}
    for p in prs:
        v = p.get("verdict", "unknown")
        verdicts[v] = verdicts.get(v, 0) + 1
    verdict_str = " ".join(f"{k}={v}" for k, v in sorted(verdicts.items())) or "none"
    wd = digest.get("watchdog", {}) or {}
    poller = digest.get("poller", {}) or {}
    errs = digest.get("errors") or []
    line = (
        f"digest: prs={len(prs)} ({verdict_str})"
        f"  watchdog={wd.get('state', 'unknown')}"
        f"  poller={'alive' if poller.get('alive') else 'down'}:{poller.get('state', 'unknown')}"
    )
    if errs:
        line += f"  [{len(errs)} digest error(s)]"
    return line


def render_static(rows, digest_line):
    lines = [f"laneview tui -- {session} (no tty; static frame)", f"  {digest_line}"]
    for r in rows:
        g = STATE_GLYPH.get(r["state"], "#")
        lines.append(f"  {g} {r['name']:<24} {r['state']}")
    print("\n".join(lines))


def fetch():
    """Re-ask lanes.sh --json for a fresh snapshot. Returns (rows, error)."""
    try:
        out = subprocess.run(
            ["bash", lanes_sh, "--json", session],
            capture_output=True, text=True, timeout=10,
        )
    except Exception as exc:  # noqa: BLE001 -- surfaced as an UNREACHABLE frame, not a crash
        return None, str(exc)
    if out.returncode != 0 or not out.stdout.strip():
        return None, (out.stderr.strip() or "lanes.sh --json produced no output")
    try:
        return json.loads(out.stdout), None
    except json.JSONDecodeError as exc:
        return None, f"lanes.sh --json produced unparseable output: {exc}"


def run_curses(stdscr, rows0, digest_line0):
    import curses

    curses.curs_set(0)
    stdscr.nodelay(True)
    # Reviewed: a sub-1s LANEVIEW_TUI_REFRESH truncated to a 0ms curses
    # timeout, which turns getch() non-blocking and busy-loops fetch()
    # below with no throttle at all. 250ms is fast enough to feel
    # immediate without spinning.
    stdscr.timeout(max(250, int(refresh_secs * 1000)))
    curses.start_color()
    curses.use_default_colors()
    curses.init_pair(1, curses.COLOR_GREEN, -1)   # free
    curses.init_pair(2, curses.COLOR_YELLOW, -1)  # busy
    curses.init_pair(3, curses.COLOR_RED, -1)     # hung / dead / stale
    curses.init_pair(4, curses.COLOR_MAGENTA, -1)  # blocked / unsent
    curses.init_pair(5, curses.COLOR_CYAN, -1)    # scrolled
    curses.init_pair(6, curses.COLOR_WHITE, -1)   # unknown / service / supervisor

    def color_for(state):
        if state == "free":
            return curses.color_pair(1)
        if state == "busy":
            return curses.color_pair(2)
        if state in ("hung", "dead", "stale"):
            return curses.color_pair(3) | curses.A_BOLD
        if state in ("menu-blocked", "text-blocked", "unsent"):
            return curses.color_pair(4)
        if state == "scrolled":
            return curses.color_pair(5)
        return curses.color_pair(6)

    rows = rows0
    digest_line = digest_line0
    selected = 0
    last_ok = time.monotonic()
    unreachable_reason = None
    digest_ok = True
    exit_code = 0

    while True:
        stdscr.erase()
        h, w = stdscr.getmaxyx()
        stdscr.addstr(0, 0, f"laneview tui — {session}"[: w - 1], curses.A_BOLD)
        # The header is a second, independent line: digest.sh backs only
        # this line, never the table, so a digest failure narrows to "the
        # header says unavailable" instead of taking over the whole screen
        # the way an unreachable lanes.sh does below.
        stdscr.addstr(1, 0, digest_line[: w - 1], curses.A_DIM)

        if unreachable_reason is not None:
            # Rule 2: never show the last good frame as if it were current.
            # The whole table is replaced by the banner, not merely
            # annotated, so a scan of the screen cannot mistake this for a
            # live view.
            msg = f"UNREACHABLE — {unreachable_reason}"
            stdscr.addstr(3, 0, msg[: w - 1], curses.A_REVERSE | curses.A_BOLD)
            stdscr.addstr(5, 0, "press q to quit, any other key to retry"[: w - 1])
        else:
            # Reviewed: the interactive path used to drop `supervisor` rows
            # entirely, while render_static (the headless path) shows every
            # row -- a real divergence between the two code paths for the
            # same contract. Both now show every row text.sh does; only
            # lanes (non-supervisor rows) are selectable/jumpable.
            lanes = [r for r in rows if r["state"] != "supervisor"]
            selected = max(0, min(selected, len(lanes) - 1)) if lanes else 0
            lane_i = 0
            for y, r in enumerate(rows, start=3):
                if y >= h - 2:
                    break
                is_lane = r["state"] != "supervisor"
                g = STATE_GLYPH.get(r["state"], "#")
                line = f"{g} {r['name']:<24} {r['state']}"
                attr = color_for(r["state"])
                if is_lane:
                    if lane_i == selected:
                        attr |= curses.A_REVERSE
                    lane_i += 1
                else:
                    attr |= curses.A_DIM
                stdscr.addstr(y, 0, line[: w - 1], attr)
            footer_y = h - 1
            stdscr.addstr(
                footer_y, 0,
                "↑/↓ or j/k select · enter jump to lane · q quit"[: w - 1],
                curses.A_DIM,
            )

        stdscr.refresh()

        ch = stdscr.getch()
        now = time.monotonic()

        # Reviewed: fetch() used to run unconditionally at the bottom of
        # every iteration, so every single j/k/enter keypress triggered a
        # fresh `lanes.sh --json` subprocess (up to a 10s stall per press
        # if lanes.sh was slow) instead of only the refresh timer. A
        # navigation keypress now redraws from the rows already in hand
        # and loops back without touching the network/tmux at all; fetch()
        # runs only on the timer tick (ch == -1) or an explicit retry
        # keypress while UNREACHABLE.
        if ch in (ord("q"), 27):  # q or Esc
            break
        elif unreachable_reason is None and ch in (curses.KEY_DOWN, ord("j")):
            selected = min(selected + 1, max(0, len(lanes) - 1))
            continue
        elif unreachable_reason is None and ch in (curses.KEY_UP, ord("k")):
            selected = max(selected - 1, 0)
            continue
        elif unreachable_reason is None and ch in (10, 13, curses.KEY_ENTER):
            if lanes and 0 <= selected < len(lanes):
                target = f"{session}:{lanes[selected]['window']}"
                # Navigation only -- the same act typing the command by hand
                # would take. Never a decision this viewer makes on the
                # estate's behalf (no claim, no dispatch, no rename).
                subprocess.run(["tmux", "select-window", "-t", target])
                subprocess.run(["tmux", "switch-client", "-t", session],
                                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            continue
        elif ch != -1 and unreachable_reason is None:
            continue  # unrecognized key while live: ignore, no fetch

        # Reached only on the refresh timer (ch == -1) or any keypress
        # while UNREACHABLE (an explicit retry).
        new_rows, err = fetch()
        if err is not None:
            unreachable_reason = err
        else:
            rows = new_rows
            unreachable_reason = None
            last_ok = now

        new_digest, digest_err = fetch_digest()
        digest_line = format_digest_line(new_digest, digest_err)
        digest_ok = digest_err is None

        # Rule 2 applies to digest.sh too, since the header is a backend of
        # this renderer same as lanes.sh is for the table (README rule 2) --
        # the exit code names "something this screen depends on could not
        # be read" even though only lanes.sh unreachability takes over the
        # whole screen visually.
        exit_code = 0 if (unreachable_reason is None and digest_ok) else 1

    return exit_code


rows0 = json.loads(raw)
digest0, digest_err0 = fetch_digest()
digest_line0 = format_digest_line(digest0, digest_err0)

if not sys.stdout.isatty():
    render_static(rows0, digest_line0)
    sys.exit(0)

import curses  # noqa: E402 -- only imported once we know we have a tty
rc = curses.wrapper(run_curses, rows0, digest_line0)
sys.exit(rc)
PY

python3 "$PY_SRC" "$SESSION" "$JSON" "$LANES_SH" "$REFRESH_SECS" "$DIGEST_SH"
