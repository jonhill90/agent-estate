#!/bin/bash
# sessions.sh: lanes.sh --json, aggregated across every tmux session.
#
# agent-tui#13: `lanes.sh` (and everything behind it) is single-session BY
# CONSTRUCTION -- see that script's own header. This wraps it rather than
# changing it, so test_lanes.sh's coverage of per-lane state classification
# is untouched and this file only has to prove AGGREGATION: every session
# shows up, each keeps its own lane rows, and the interim `supervised`
# signal (real ledger evidence, not agent-supervisor#153's own marker, which
# had not landed when this was written -- see sessions.sh's module comment)
# reads correctly and fails CLOSED.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SESSIONS_SH="$HERE/../../scripts/supervisor/sessions.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1 -- $2"; fail=$((fail+1)); }

D=$(mktemp -d); mkdir -p "$D/bin"
cp "$HERE/stubs/tmux-sessions" "$D/bin/tmux"
cp "$HERE/stubs/ps-sessions" "$D/bin/ps"
chmod +x "$D/bin/tmux" "$D/bin/ps"
export PATH="$D/bin:$PATH"

# --- fixture: three sessions, only one known to the ledger ------------------
# columns: session|index|name|command|pane-text|activity-age-seconds|in-mode
# Window 1 is lanes.sh's SUPERVISOR_WINDOW default in every session, on
# purpose -- it proves each session is read independently rather than one
# global window-1 rule leaking across sessions.
cat > "$D/fixture" <<'FIX'
agent-supervisor|1|architecture|claude.exe|❯ ready|1|0
agent-supervisor|2|free-1|claude.exe|❯ ready|1|0
Hill90|1|scratch|claude.exe|❯ ready|1|0
director|1|director|claude.exe|❯ ready|1|0
FIX
export LANES_FIXTURE="$D/fixture"

run_json() { # run_json <known-sessions...>
  CLI_SESSIONS_KNOWN="$*" \
  SESSIONS_LEDGER_CMD="$HERE/stubs/cli-sessions" \
  "$SESSIONS_SH" --json
}

out=$(run_json agent-supervisor)
rc=$?
if [ "$rc" -eq 0 ]; then ok "exits 0 with sessions present"; else bad "exit code" "got $rc"; fi

python3 - "$out" <<'PY' || fail=$((fail+1))
import json, sys
data = json.loads(sys.argv[1])
by_session = {row["session"]: row for row in data}

assert set(by_session) == {"agent-supervisor", "Hill90", "director"}, by_session.keys()
print("  ok   all three sessions present, none dropped")

assert by_session["agent-supervisor"]["supervised"] is True, by_session["agent-supervisor"]
print("  ok   agent-supervisor (ledger-registered) reads supervised")

assert by_session["Hill90"]["supervised"] is False, by_session["Hill90"]
print("  ok   Hill90 (Jon's own, no ledger lane) reads UNsupervised -- the load-bearing case")

assert by_session["director"]["supervised"] is False, by_session["director"]
print("  ok   director reads unsupervised too -- no ledger evidence, so no claim is made either way")

sup_lanes = by_session["agent-supervisor"]["lanes"]
assert isinstance(sup_lanes, list) and len(sup_lanes) == 2, sup_lanes
states = {row["name"]: row["state"] for row in sup_lanes}
assert states["architecture"] == "supervisor", states
assert states["free-1"] == "free", states
print("  ok   agent-supervisor's own lane rows are real lanes.sh --json output, unmodified")

hill_lanes = by_session["Hill90"]["lanes"]
assert isinstance(hill_lanes, list) and len(hill_lanes) == 1
print("  ok   Hill90 gets its own lane rows too, independent of agent-supervisor's")
PY
[ $? -eq 0 ] && pass=$((pass+7)) || true

# --- nothing known: everything unsupervised, fail-closed --------------------
out=$(run_json)
python3 - "$out" <<'PY'
import json, sys
data = json.loads(sys.argv[1])
assert all(row["supervised"] is False for row in data), data
print("  ok   empty ledger -> every session unsupervised (fails closed, not open)")
PY
[ $? -eq 0 ] && pass=$((pass+1)) || fail=$((fail+1))

# --- a session lanes.sh cannot read is reported, not dropped -----------------
FAKE_LANES="$D/fake-lanes.sh"
cat > "$FAKE_LANES" <<'FAKE'
#!/bin/bash
if [ "$2" = "broken" ]; then
  echo "fake lanes: session 'broken' does not exist" >&2
  exit 1
fi
echo '[{"window":1,"window_id":"@1","name":"x","command":"claude.exe","state":"free","idle_seconds":0}]'
FAKE
chmod +x "$FAKE_LANES"

BROKEN_TMUX="$D/bin2"
mkdir -p "$BROKEN_TMUX"
cat > "$BROKEN_TMUX/tmux" <<'TMUX'
#!/bin/bash
case "$1" in
  list-sessions) printf 'ok-session\nbroken\n' ;;
  *) exit 0 ;;
esac
TMUX
chmod +x "$BROKEN_TMUX/tmux"

out=$(PATH="$BROKEN_TMUX:$PATH" \
  SESSIONS_LANES_SH="$FAKE_LANES" \
  SESSIONS_LEDGER_CMD="$HERE/stubs/cli-sessions" \
  CLI_SESSIONS_KNOWN="" \
  "$SESSIONS_SH" --json)
rc=$?
if [ "$rc" -eq 0 ]; then ok "aggregate call still exits 0 when one session's lanes.sh fails"
else bad "exit code with one broken session" "got $rc"; fi

python3 - "$out" <<'PY'
import json, sys
data = json.loads(sys.argv[1])
by_session = {row["session"]: row for row in data}
assert by_session["ok-session"]["lanes"], by_session["ok-session"]
assert by_session["ok-session"].get("error") is None
print("  ok   the healthy session's lanes are unaffected")
broken = by_session["broken"]
assert broken["lanes"] is None, broken
assert broken.get("error"), broken
print("  ok   the unreadable session is REPORTED (null lanes + error), not silently dropped")
PY
[ $? -eq 0 ] && pass=$((pass+2)) || fail=$((fail+1))

# --- refuses anything but --json ---------------------------------------------
if SESSIONS_LEDGER_CMD="$HERE/stubs/cli-sessions" "$SESSIONS_SH" >/dev/null 2>&1; then
  bad "no-arg call" "expected non-zero exit"
else
  ok "no-arg call is refused (only --json is supported)"
fi

# --- no tmux sessions at all: exit 1, not an empty success -------------------
EMPTY_TMUX="$D/bin3"; mkdir -p "$EMPTY_TMUX"
cat > "$EMPTY_TMUX/tmux" <<'TMUX'
#!/bin/bash
exit 0
TMUX
chmod +x "$EMPTY_TMUX/tmux"
if PATH="$EMPTY_TMUX:$PATH" SESSIONS_LEDGER_CMD="$HERE/stubs/cli-sessions" "$SESSIONS_SH" --json >/tmp/sessions-empty-out 2>&1; then
  bad "no sessions" "expected non-zero exit, got 0: $(cat /tmp/sessions-empty-out)"
else
  ok "no tmux sessions -> exit 1 (blind, not quietly empty)"
fi
rm -f /tmp/sessions-empty-out

rm -rf "$D"
echo "sessions.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
