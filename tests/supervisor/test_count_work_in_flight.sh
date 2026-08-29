#!/bin/bash
# count-work-in-flight.sh's own aggregation (agent-supervisor#826) -- busy/
# hung tmux lanes across every session, plus pane-less claude-print/pi-rpc
# dispatches the ledger still has an open task for. See test_host_pressure.sh
# (this same directory) for the GATE that consumes this number; this file is
# the instrument's own logic, in isolation, with sessions.sh and cli.py
# faked via this script's own env seams so every case is deterministic.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$HERE/../../scripts/supervisor/count-work-in-flight.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_eq()       { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "want '$3', got '$2'"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }

echo "count-work-in-flight.sh"

D=$(mktemp -d)

# fake_sessions <json> -- WORK_IN_FLIGHT_SESSIONS_SH stand-in: a script that
# just prints canned `sessions.sh --json` output, ignoring its own args.
fake_sessions() {
  cat > "$D/fake-sessions.sh" <<EOF
#!/bin/bash
cat <<'JSON'
$1
JSON
EOF
  chmod +x "$D/fake-sessions.sh"
}

# fake_ledger <json> -- WORK_IN_FLIGHT_CLI stand-in: a python script that
# prints canned \`cli.py status\` output, ignoring the --state-dir/status
# args count-work-in-flight.sh passes it.
fake_ledger() {
  cat > "$D/fake-cli.py" <<EOF
#!/usr/bin/env python3
print('''$1''')
EOF
}

run() {
  WORK_IN_FLIGHT_SESSIONS_SH="$D/fake-sessions.sh" \
  WORK_IN_FLIGHT_CLI="$D/fake-cli.py" \
  "$SCRIPT"
}

# --- busy/hung lanes across two sessions, no pane-less lanes registered ----
fake_sessions '[
  {"session": "agent-supervisor", "lanes": [
    {"name": "arch", "state": "busy"},
    {"name": "free-1", "state": "free"},
    {"name": "free-2", "state": "hung"}
  ]},
  {"session": "director", "lanes": [
    {"name": "director", "state": "busy"}
  ]}
]'
fake_ledger '{"lanes": [], "tasks": []}'
OUT=$(run); RC=$?
want_exit "reads exit 0" "$RC" 0 "$OUT"
want_eq "counts busy+hung across BOTH sessions, ignores free" "$OUT" "3"

# --- free/dead/stale/blocked/unsent/unknown/scrolled/service never count --
fake_sessions '[
  {"session": "s", "lanes": [
    {"name": "a", "state": "free"},
    {"name": "b", "state": "dead"},
    {"name": "c", "state": "stale"},
    {"name": "d", "state": "menu-blocked"},
    {"name": "e", "state": "unsent"},
    {"name": "f", "state": "unknown"},
    {"name": "g", "state": "scrolled"},
    {"name": "h", "state": "service"}
  ]}
]'
fake_ledger '{"lanes": [], "tasks": []}'
OUT=$(run); RC=$?
want_exit "reads exit 0" "$RC" 0 "$OUT"
want_eq "none of the non-busy/hung states count -- the #826 load-bearing case" "$OUT" "0"

# --- a pane-less claude-print lane with an open task counts once ----------
fake_sessions '[{"session": "s", "lanes": [{"name": "a", "state": "free"}]}]'
fake_ledger '{
  "lanes": [
    {"lane": "claude-print-42", "transport": "claude-print"},
    {"lane": "pi-rpc-7", "transport": "pi-rpc"},
    {"lane": "agent-supervisor:3", "transport": "send-keys"}
  ],
  "tasks": [
    {"lane": "claude-print-42", "status": "accepted"},
    {"lane": "pi-rpc-7", "status": "complete"}
  ]
}'
OUT=$(run); RC=$?
want_exit "reads exit 0" "$RC" 0 "$OUT"
want_eq "one open claude-print task counts, one COMPLETE pi-rpc task does not" "$OUT" "1"

# --- pane-less lane with a terminal-status task does not count ------------
fake_sessions '[{"session": "s", "lanes": []}]'
fake_ledger '{
  "lanes": [{"lane": "claude-print-1", "transport": "claude-print"}],
  "tasks": [
    {"lane": "claude-print-1", "status": "failed"},
    {"lane": "claude-print-1", "status": "cancelled"}
  ]
}'
OUT=$(run); RC=$?
want_exit "reads exit 0" "$RC" 0 "$OUT"
want_eq "failed/cancelled pane-less tasks never count as work in flight" "$OUT" "0"

# --- a send-keys lane's own open task is NOT double-counted through the ---
# --- pane-less path -- it is only ever counted via its tmux state above.
fake_sessions '[{"session": "s", "lanes": [{"name": "a", "state": "free"}]}]'
fake_ledger '{
  "lanes": [{"lane": "agent-supervisor:1", "transport": "send-keys"}],
  "tasks": [{"lane": "agent-supervisor:1", "status": "running"}]
}'
OUT=$(run); RC=$?
want_exit "reads exit 0" "$RC" 0 "$OUT"
want_eq "an ordinary send-keys lane's task never counts through the pane-less path" "$OUT" "0"

# --- FAIL CLOSED: sessions.sh unreadable ------------------------------------
FAIL_SESSIONS="$D/fail-sessions.sh"
cat > "$FAIL_SESSIONS" <<'EOF'
#!/bin/bash
exit 1
EOF
chmod +x "$FAIL_SESSIONS"
fake_ledger '{"lanes": [], "tasks": []}'
OUT=$(WORK_IN_FLIGHT_SESSIONS_SH="$FAIL_SESSIONS" WORK_IN_FLIGHT_CLI="$D/fake-cli.py" "$SCRIPT" 2>&1); RC=$?
want_exit "sessions.sh failing fails closed (refuses), never reads as zero" "$RC" 2 "$OUT"
want_contains "...says it could not measure, not that it's empty" "could not read tmux lane state" "$OUT"

# --- FAIL CLOSED: cli.py/ledger unreadable ----------------------------------
fake_sessions '[{"session": "s", "lanes": []}]'
FAIL_CLI="$D/fail-cli.py"
cat > "$FAIL_CLI" <<'EOF'
#!/usr/bin/env python3
import sys
sys.exit(1)
EOF
OUT=$(WORK_IN_FLIGHT_SESSIONS_SH="$D/fake-sessions.sh" WORK_IN_FLIGHT_CLI="$FAIL_CLI" "$SCRIPT" 2>&1); RC=$?
want_exit "a failed ledger read fails closed (refuses), never reads as zero" "$RC" 2 "$OUT"
want_contains "...says it could not measure, not that it's empty" "could not read the ledger" "$OUT"

# --- FAIL CLOSED: sessions.sh missing next to count-work-in-flight.sh ------
D3=$(mktemp -d)
cp "$SCRIPT" "$D3/count-work-in-flight.sh"
chmod +x "$D3/count-work-in-flight.sh"
OUT=$("$D3/count-work-in-flight.sh" 2>&1); RC=$?
want_exit "sessions.sh missing next to a copy of this script fails closed" "$RC" 2 "$OUT"
want_contains "...says it could not measure, not that it's fine" "sessions.sh missing" "$OUT"
rm -rf "$D3"

rm -rf "$D"
echo
echo "count-work-in-flight.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
