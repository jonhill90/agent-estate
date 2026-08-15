#!/bin/bash
# Report `lanes.sh --json` for every tmux session, not just one.
#
# WHY: agent-tui#13. `lanes.sh` (see its own header) takes exactly one
# session -- that is correct for what it does, but it left every consumer
# single-session BY CONSTRUCTION: the MCP `lanes` tool wraps it 1:1, and the
# Go rail that wraps the MCP tool could show only the session it happened to
# be pointed at. Six tmux sessions exist on the live box; the rail showed
# one, and the missing one was `director` -- the session Jon actually talks
# to. That is the regression this file exists to close.
#
# This wraps `lanes.sh`, it does not replace it. Every session's lane rows
# come from an unmodified `lanes.sh --json <session>` subprocess call, so
# `lanes.sh`'s own byte-identical-output guarantee (#84's acceptance) is
# untouched -- nothing about that script's behavior, arguments, or single-
# session output changes here.
#
# ## The `supervised` field is NOT agent-supervisor#153
#
# #153 is building the estate's real answer to "is this session mine to act
# on" -- a marker set at session creation, unknown defaulting to
# unsupervised. It had not landed as of this file's first version (its
# branch sat at the same commit as `main`). Shipping a multi-session view
# with no marker at all reopens exactly the incident #153 exists to prevent:
# Jon's own `Hill90` session would appear indistinguishable from one the
# estate dispatches into, and his sessions have been destroyed three times
# by that exact confusion.
#
# So this file computes a NARROWER, evidence-based stand-in: a session is
# `supervised` here only if the ledger (`cli.py status`) has ever registered
# a lane whose identity is `<that session>:<window>` -- i.e. `dispatch.sh`
# has actually claimed a window in it. That is real evidence the estate
# manages the session, not a guess, but it answers a narrower question than
# #153 will ("has the estate dispatched here" vs. #153's fuller intent) and
# every session outside that set reads unsupervised, full stop -- including
# the supervisor's own well-known sessions if the ledger cannot be read.
# Fail-closed: a ledger read failure counts every session unsupervised
# rather than trusting a name.
#
# When #153 lands its own marker, THAT is the authority and this heuristic
# should be replaced, not merged with -- see that issue.
#
# Usage: sessions.sh --json
#
# Exit 0 if at least one tmux session could be listed (per-session lane
# reads that fail are reported inline, not by failing the whole call).
# Exit 1 if tmux has no sessions at all, or is not reachable -- the same
# "blind, not quiet" posture `lanes.sh` and `digest.sh` already take.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Overridable for tests only -- production always resolves both next to this
# script, same as every other file in this directory.
LANES_SH="${SESSIONS_LANES_SH:-$HERE/lanes.sh}"
CLI="${SESSIONS_CLI:-$HERE/cli.py}"
PY="${SESSIONS_PYTHON:-python3}"
STATE_DIR="${AGENT_SUPERVISOR_STATE_DIR:-$HOME/.local/state/agent-dotfiles-supervisor}"

MODE="${1:-}"
if [ "$MODE" != "--json" ]; then
  echo "usage: sessions.sh --json" >&2
  exit 2
fi

declare -a SESSIONS
while IFS= read -r s; do
  [ -n "$s" ] || continue
  SESSIONS+=("$s")
done < <(tmux list-sessions -F '#{session_name}' 2>/dev/null)
if [ "${#SESSIONS[@]}" -eq 0 ]; then
  echo "sessions.sh: no tmux sessions (or tmux is not reachable)" >&2
  exit 1
fi

# Sessions the ledger has ever registered a lane under -- see the module
# comment above for exactly what this does and does not prove. A failed or
# unparseable read leaves this empty, which fails every session CLOSED to
# unsupervised rather than open to supervised.
#
# SESSIONS_LEDGER_CMD is a test-only override for the whole ledger read (not
# just the interpreter or the script path, since a stub replacing `cli.py`
# is not itself a Python program) -- production always runs the real
# `<python> <cli.py> --state-dir <dir> status` below.
if [ -n "${SESSIONS_LEDGER_CMD:-}" ]; then
  # shellcheck disable=SC2086 # deliberately word-split: a test-only command line
  LEDGER_RAW=$(eval "$SESSIONS_LEDGER_CMD" 2>/dev/null)
else
  LEDGER_RAW=$("$PY" "$CLI" --state-dir "$STATE_DIR" status 2>/dev/null)
fi
KNOWN_JSON=$(printf '%s' "$LEDGER_RAW" | "$PY" -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    d = {}
lanes = d.get("lanes") or []
sessions = sorted({
    lane["lane"].split(":", 1)[0]
    for lane in lanes
    if isinstance(lane.get("lane"), str) and ":" in lane["lane"]
})
print(json.dumps(sessions))
' 2>/dev/null)
[ -n "$KNOWN_JSON" ] || KNOWN_JSON='[]'
export SESSIONS_KNOWN_JSON="$KNOWN_JSON"

# Fields are separated by ASCII RS (0x1e) rather than a newline or tab so a
# session name or a lane's own JSON (which may contain either) cannot be
# mistaken for a record boundary.
{
  for s in "${SESSIONS[@]}"; do
    printf '%s\x1e' "$s"
    if lanes_json=$("$LANES_SH" --json "$s" 2>/dev/null); then
      printf '%s\x1e' "$lanes_json"
    else
      printf 'null\x1e'
    fi
  done
} | "$PY" -c '
import json, os, sys

known = set(json.loads(os.environ.get("SESSIONS_KNOWN_JSON") or "[]"))
parts = sys.stdin.read().split("\x1e")

out = []
i = 0
while i + 1 < len(parts):
    session, lanes_raw = parts[i], parts[i + 1]
    i += 2
    if session == "":
        continue
    entry = {"session": session, "supervised": session in known}
    if lanes_raw == "null":
        entry["lanes"] = None
        entry["error"] = "lanes.sh --json failed for this session (it may have closed mid-read)"
    else:
        try:
            entry["lanes"] = json.loads(lanes_raw)
        except json.JSONDecodeError:
            entry["lanes"] = None
            entry["error"] = "lanes.sh --json did not return JSON"
    out.append(entry)

print(json.dumps(out))
'
