#!/usr/bin/env bash
# count-work-in-flight.sh -- print how many lanes are actually DOING work
# right now, across every tmux session plus every pane-less dispatch. This
# is the number host-pressure.sh's session gate caps against, not a raw
# pane count.
#
# WHY THIS EXISTS (agent-supervisor#826): host-pressure.sh's third gate used
# to compare SUPERVISOR_MAX_AGENT_SESSIONS against count-agents.sh's raw
# process census -- every tmux pane running a real `claude` process,
# whether it was mid-turn or sitting `free` waiting for a dispatch.
# Measured live: 18 tmux panes existed, the host was idle (~0.4 load/core,
# swap flat), and 4+ of those panes were `free` -- correctly idle, some for
# 4-7 days -- while the gate refused a new dispatch anyway. `count-agents.sh`
# (#663) is not wrong about what it counts (a real Claude PROCESS, as
# opposed to `pgrep` noise) -- the gate was comparing the wrong QUESTION
# against the ceiling: "how many agent processes exist" instead of "how
# much work is in flight". This script answers the second question.
# `count-agents.sh` is untouched and still answers the first -- see its own
# header; nothing here re-derives or replaces its classification.
#
# WHAT COUNTS AS WORK IN FLIGHT:
#   - a lane in lanes.sh's `busy` or `hung` state, across EVERY tmux
#     session (via sessions.sh, not a single-session lanes.sh call --
#     host-pressure.sh guards the whole host, not one session). Both
#     `busy` and `hung` are real, in-flight work by lanes.sh's own
#     kernel-process-tree-based distinction between "correctly idle" and
#     "quietly still working" (#83) -- `free`, `dead`, `stale`, `broken`,
#     `blocked`, `unsent`, `unknown`, `never-busy`, `scrolled` and
#     `service` are all NOT counted: none of them represents an agent
#     turn actually running.
#   - PLUS every pane-less claude-print/pi-rpc dispatch the ledger still
#     has an open (non-terminal) task for. Those lanes have no tmux pane
#     at all for lanes.sh to see (dispatch-claude-print.sh's and
#     dispatch-pi-rpc.sh's own headers), so they would otherwise vanish
#     from this count entirely even while genuinely running.
#
# Usage: count-work-in-flight.sh
#   Prints one line: the integer count.
# Exit 0 on a successful count (including zero -- an idle estate is a real,
# reportable state). Exit 2 if either input could not be read at all --
# same "absence is a typed value" rule as count-agents.sh and
# host-pressure.sh: a census that saw nothing must not look identical to a
# census that was never taken.
#
# Overridable for tests only, same shape as sessions.sh's own seams:
#   WORK_IN_FLIGHT_SESSIONS_SH   (default: sessions.sh next to this script)
#   WORK_IN_FLIGHT_CLI           (default: cli.py next to this script)
#   WORK_IN_FLIGHT_PYTHON        (default: python3)
#   AGENT_SUPERVISOR_STATE_DIR   (ledger location -- same default every
#                                 other script here resolves)
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SESSIONS_SH="${WORK_IN_FLIGHT_SESSIONS_SH:-$HERE/sessions.sh}"
CLI="${WORK_IN_FLIGHT_CLI:-$HERE/cli.py}"
PY="${WORK_IN_FLIGHT_PYTHON:-python3}"
STATE_DIR="${AGENT_SUPERVISOR_STATE_DIR:-$HOME/.local/state/agent-dotfiles-supervisor}"

if [ ! -x "$SESSIONS_SH" ]; then
  echo "count-work-in-flight: sessions.sh missing or not executable at $SESSIONS_SH -- refusing to guess" >&2
  exit 2
fi

SESSIONS_JSON=$("$SESSIONS_SH" --json 2>/dev/null)
rc=$?
if [ "$rc" -ne 0 ] || [ -z "$SESSIONS_JSON" ]; then
  echo "count-work-in-flight: could not read tmux lane state (sessions.sh exited $rc) -- refusing to guess" >&2
  exit 2
fi

LEDGER_JSON=$("$PY" "$CLI" --state-dir "$STATE_DIR" status 2>/dev/null)
rc=$?
if [ "$rc" -ne 0 ] || [ -z "$LEDGER_JSON" ]; then
  echo "count-work-in-flight: could not read the ledger (cli.py status exited $rc) -- refusing to guess" >&2
  exit 2
fi

# Both blobs go over STDIN, RS (0x1e) delimited -- not argv. A live ledger's
# `status` dump (every task and lane ever recorded) is well past ARG_MAX on
# this host (measured: `Argument list too long` passing it as $2), the same
# ARG_MAX risk sessions.sh's own session/lanes-json plumbing already avoids
# by piping rather than by argv.
printf '%s\x1e%s' "$SESSIONS_JSON" "$LEDGER_JSON" | "$PY" -c '
import json, sys

sessions_raw, ledger_raw = sys.stdin.read().split("\x1e", 1)

try:
    sessions = json.loads(sessions_raw)
except Exception:
    print("count-work-in-flight: sessions.sh --json did not return JSON", file=sys.stderr)
    sys.exit(2)

busy_hung = 0
for entry in sessions if isinstance(sessions, list) else []:
    if not isinstance(entry, dict):
        continue
    for lane in entry.get("lanes") or []:
        if isinstance(lane, dict) and lane.get("state") in ("busy", "hung"):
            busy_hung += 1

try:
    ledger = json.loads(ledger_raw)
except Exception:
    print("count-work-in-flight: cli.py status did not return JSON", file=sys.stderr)
    sys.exit(2)

PANELESS_TRANSPORTS = {"claude-print", "pi-rpc"}
TERMINAL_STATUSES = {"complete", "failed", "cancelled"}

paneless_lanes = {
    row["lane"]
    for row in (ledger.get("lanes") or [])
    if isinstance(row, dict) and row.get("transport") in PANELESS_TRANSPORTS
}
inflight_paneless = {
    row["lane"]
    for row in (ledger.get("tasks") or [])
    if isinstance(row, dict)
    and row.get("lane") in paneless_lanes
    and row.get("status") not in TERMINAL_STATUSES
}

print(busy_hung + len(inflight_paneless))
'
exit $?
