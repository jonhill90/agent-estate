#!/bin/bash
# agent-supervisor#107 hard-acceptance item 2: "lanes.sh output BYTE-IDENTICAL
# with the app running and not running." This is that proof, not a
# screenshot. It runs against an ISOLATED tmux server (never the machine's
# real one -- agent-supervisor's own AGENTS.md rule 4, and #173's fixture is
# exactly what a fixed-width injected pane broke) and diffs lanes.sh --json
# for a set of decoy windows before and after estate runs in its OWN
# window of the same session.
#
# Usage: verify-lanes-unaffected.sh <path-to-agent-supervisor-checkout> [estate-binary]
#
# Exit 0 and prints PASS when the pre-existing windows classify identically
# with the app running; exit 1 and prints the diff otherwise.
set -euo pipefail

SUPERVISOR_REPO="${1:?usage: verify-lanes-unaffected.sh <agent-supervisor-repo> [estate-binary]}"
BINARY="${2:-estate}"
LANES_SH="$SUPERVISOR_REPO/scripts/supervisor/lanes.sh"
MCP_SERVER="$SUPERVISOR_REPO/scripts/supervisor/mcp_server.py"

if [ ! -x "$LANES_SH" ]; then
  echo "verify-lanes-unaffected: $LANES_SH not found or not executable" >&2
  exit 1
fi
if [ ! -f "$MCP_SERVER" ]; then
  echo "verify-lanes-unaffected: $MCP_SERVER not found" >&2
  exit 1
fi
if ! command -v "$BINARY" >/dev/null 2>&1 && [ ! -x "$BINARY" ]; then
  echo "verify-lanes-unaffected: estate binary not found: $BINARY (build it first: go build -o estate ./cmd/estate)" >&2
  exit 1
fi

# --- isolation: never the attached server, never the real estate ----------
if [ -n "${TMUX:-}" ]; then
  echo "verify-lanes-unaffected: refusing to run while attached to a tmux session" >&2
  exit 1
fi

# Isolation follows agent-supervisor's own tmux-isolation.sh contract: no
# -L socket trick, just a fresh TMUX_TMPDIR so plain `tmux` (what lanes.sh
# itself invokes, unparameterized) resolves to a server nothing else on this
# machine can see, and any destructive verb here is scoped to it alone.
export TMUX_TMPDIR
TMUX_TMPDIR="$(mktemp -d)"
trap 'tmux kill-server >/dev/null 2>&1 || true; rm -rf "$TMUX_TMPDIR"' EXIT

SESSION="rail-verify-$$"
tmux new-session -d -s "$SESSION" -n decoy1 -x 80 -y 24
tmux new-window -d -t "$SESSION" -n decoy2
tmux new-window -d -t "$SESSION" -n decoy3

# Give the decoy windows a moment so lanes.sh's two-sample idle check has
# something stable to read.
sleep 1

BEFORE_RAW="$("$LANES_SH" --json "$SESSION")"

# Only the windows that existed before estate ran are part of the claim --
# estate's own window is expected to appear as a new row (it is a real
# window, own-window by design), not to be invisible. What must not change
# is everything that was already there.
BEFORE_IDS="$(echo "$BEFORE_RAW" | python3 -c 'import json,sys; print("\n".join(r["window_id"] for r in json.load(sys.stdin)))')"

filter_to_before() {
  # A heredoc here would steal stdin from python3 -- the piped lanes.sh
  # output has to reach it instead, so the script is passed via -c.
  #
  # idle_seconds is dropped from the comparison, deliberately: it is wall
  # clock time since the pane last drew, and it advances whether or not
  # estate exists -- the wait between BEFORE and AFTER moves it on its
  # own. What the acceptance item is actually about is CLASSIFICATION
  # (state, and the other fields lanes.sh derives from the pane), which
  # must not move; a monotonically increasing counter is not evidence of
  # that either way, so comparing it would make the check flaky in both
  # directions -- a false pass on a lucky tick, a false fail on a slow one.
  python3 -c '
import json, sys
before_ids = set(sys.argv[1].split())
rows = json.load(sys.stdin)
kept = [{k: v for k, v in r.items() if k != "idle_seconds"} for r in rows if r["window_id"] in before_ids]
kept.sort(key=lambda r: r["window_id"])
print(json.dumps(kept, sort_keys=True, indent=2))
' "$BEFORE_IDS"
}

BEFORE="$(echo "$BEFORE_RAW" | filter_to_before)"

# estate in ITS OWN window of the same isolated session -- never a pane
# injected into decoy1/2/3. AGENT_SUPERVISOR_REPO and TMUX_TMPDIR both need
# to reach the binary's mcp_server.py subprocess, which is why they are
# exported rather than passed as flags only.
export AGENT_SUPERVISOR_REPO="$SUPERVISOR_REPO"
tmux new-window -d -t "$SESSION" -n estate-under-test \
  "$BINARY -supervisor-repo '$SUPERVISOR_REPO' -session '$SESSION'; sleep 30"

# Let it connect, fetch once, and render.
sleep 3

AFTER_RAW="$(TMUX_TMPDIR="$TMUX_TMPDIR" "$LANES_SH" --json "$SESSION")"
AFTER="$(echo "$AFTER_RAW" | filter_to_before)"

if [ "$BEFORE" = "$AFTER" ]; then
  echo "PASS: lanes.sh --json is byte-identical for pre-existing windows with estate running"
  exit 0
fi

echo "FAIL: lanes.sh --json changed for a pre-existing window while estate ran" >&2
diff <(echo "$BEFORE") <(echo "$AFTER") >&2 || true
exit 1
