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
# ## The `supervised` field IS agent-supervisor#153, now
#
# #153 landed (PR #157): `bootstrap-session.sh` writes a `sessions` row via
# `cli.py adopt-session` at the moment it CREATES a session, independent of
# whether any lane has since been dispatched into it. `cli.py status` already
# returns that table verbatim as its `"sessions"` key
# (`ledger.list_sessions()`). This file used to derive `supervised` by
# scanning `status`'s `lanes` list for any `<session>:<window>` identity --
# real ledger evidence, but a re-derivation of a DIFFERENT, narrower question
# ("has dispatch.sh ever claimed a window here") than #153 actually answers
# ("did the estate decide to adopt this session"), and a session adopted at
# creation but never yet dispatched into read wrongly unsupervised under that
# scheme. `KNOWN_JSON` below now reads `sessions[].session` directly instead
# -- #153's own real signal, not a stand-in for it.
#
# This is still a SIMPLIFIED reading of #153's tri-state `session_state()`
# (`cli.py`, supervised/unsupervised/unknown), not a duplication of its
# logic: `session_state()` also checks whether tmux still has the session at
# all (`transport.session_exists`), collapsing to `unknown` when it does not.
# That half is moot here by construction -- this script only ever lists
# sessions `tmux list-sessions` currently returns, so every row already
# passes the "tmux has it" test before this field is even computed. Only the
# ledger-adoption half applies, and it is read straight from the table #153
# writes, not re-derived.
#
# Fail-closed, unchanged: a ledger read failure (or unparseable JSON) leaves
# `KNOWN_JSON` empty, so every session reads unsupervised rather than
# trusting a name.
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

# `=()`, not `declare -a` alone: an array that is declared but never
# assigned reads as UNBOUND under `set -u` in some bash builds when the loop
# below appends nothing (e.g. tmux has no sessions at all) -- measured
# directly against both `/bin/bash` (system, 3.2.57) and
# `/opt/homebrew/bin/bash` (5.3.3): `declare -a SESSIONS` alone raises
# `unbound variable` on the newer Homebrew 5.3 build; the older system 3.2
# build tolerates it. The empty-literal assignment marks the variable set
# even with zero elements, which `${#SESSIONS[@]}` below then reads as 0,
# not an error, on both.
SESSIONS=()
while IFS= read -r s; do
  [ -n "$s" ] || continue
  SESSIONS+=("$s")
done < <(tmux list-sessions -F '#{session_name}' 2>/dev/null)
if [ "${#SESSIONS[@]}" -eq 0 ]; then
  echo "sessions.sh: no tmux sessions (or tmux is not reachable)" >&2
  exit 1
fi

# Sessions the ledger has ADOPTED (agent-supervisor#153's own marker) -- see
# the module comment above for exactly what this does and does not prove. A
# failed or unparseable read leaves this empty, which fails every session
# CLOSED to unsupervised rather than open to supervised.
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
sessions_rows = d.get("sessions") or []
sessions = sorted({
    row["session"]
    for row in sessions_rows
    if isinstance(row, dict) and isinstance(row.get("session"), str) and row["session"]
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
