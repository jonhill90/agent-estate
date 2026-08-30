#!/bin/bash
# host-pressure.sh's own arithmetic (agent-supervisor#500 / corpus directive
# it-ef548e51e71daebe) -- refuse a NEW dispatch when load/core or free
# memory crosses a threshold, and refuse (never silently proceed) when a
# metric cannot be read at all. See test_dispatch_host_pressure.sh (this
# same directory) for dispatch.sh's own INTEGRATION with this gate; this
# file is the gate's own logic, in isolation, with sysctl/vm_stat faked so
# every case is deterministic regardless of what this machine's real load
# happens to be at test time.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATE="$HERE/../../scripts/supervisor/host-pressure.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }

echo "host-pressure.sh"

D=$(mktemp -d); mkdir -p "$D/bin"

# Fakes report Darwin-shaped output (sysctl/vm_stat) -- this suite runs on
# whatever CI host it's given (ubuntu-latest per .github/workflows/*.yml),
# so `uname` inside host-pressure.sh would pick the Linux branch there and
# never call these fakes at all. Forced to the Darwin branch here via
# HOST_PRESSURE_TEST_OS, the one seam this test needs that production code
# never reads -- see the gate script's own _OS resolution.
fake_darwin() {
  cat > "$D/bin/sysctl" <<EOF
#!/bin/bash
case "\$*" in
  "-n vm.loadavg") echo "{ $1 x x }" ;;
  "-n hw.ncpu") echo "$2" ;;
  "-n hw.pagesize") echo "16384" ;;
esac
EOF
  cat > "$D/bin/vm_stat" <<EOF
#!/bin/bash
echo "Pages free:                $3."
echo "Pages inactive:            0."
echo "Pages speculative:         0."
EOF
  chmod +x "$D/bin/sysctl" "$D/bin/vm_stat"
}

# SUPERVISOR_MAX_AGENT_SESSIONS=0 by default here: every test above this
# point (load/mem) predates the third gate and asserts only on those two
# metrics, on whatever real agent-session count this host happens to have
# at test time -- disabled so those assertions stay deterministic. The
# THIRD GATE section below re-enables it explicitly per case.
run() { HOST_PRESSURE_TEST_OS=Darwin SUPERVISOR_MAX_AGENT_SESSIONS=0 PATH="$D/bin:$PATH" "$@" "$GATE"; }

# --- REFUSE: load/core over threshold --------------------------------------
# load=50, cores=10 -> 5.00/core, default threshold 3.0 -- comfortably over,
# free memory made irrelevant (a huge free-page count) so this case isolates
# the LOAD branch specifically.
fake_darwin 50.00 10 100000000
OUT=$(run); RC=$?
want_exit "load/core over the default threshold refuses" "$RC" 1 "$OUT"
want_contains "...names the actual comparison" "load/core 5.00 >= 3.0" "$OUT"

# --- REFUSE: free memory under threshold -----------------------------------
# load=0.10, cores=10 -> 0.01/core, comfortably under -- isolates the MEMORY
# branch. 100 pages * 16384 bytes = ~0.0015GB, far under the 1.5GB default.
fake_darwin 0.10 10 100
OUT=$(run); RC=$?
want_exit "free memory under the default threshold refuses" "$RC" 1 "$OUT"
want_contains "...names the actual comparison" "< 1.5GB" "$OUT"

# --- ALLOW: both comfortably within limits ---------------------------------
fake_darwin 1.00 10 10000000
OUT=$(run); RC=$?
want_exit "both metrics within limits allows" "$RC" 0 "$OUT"
want_contains "...says why" "within limits" "$OUT"

# --- FAIL CLOSED: sysctl itself unreadable ----------------------------------
# An empty bin dir -- no sysctl, no vm_stat on PATH at all (PATH still
# carries /usr/bin etc., but this test forces the Darwin branch via
# HOST_PRESSURE_TEST_OS while giving it none of ITS OWN sysctl/vm_stat, the
# actual "instrument cannot see" case, not a contrived one).
D2=$(mktemp -d); mkdir -p "$D2/bin"
OUT=$(HOST_PRESSURE_TEST_OS=Darwin SUPERVISOR_MAX_AGENT_SESSIONS=0 PATH="$D2/bin:/usr/bin:/bin" "$GATE" 2>&1); RC=$?
# Real macOS DOES have sysctl at /usr/bin/sysctl, so this only proves
# fail-closed for real if this test runs somewhere sysctl is genuinely
# absent (Linux CI, forced into the Darwin branch) -- exactly the case that
# matters, since CI is where this actually gets exercised without a real Mac.
if command -v sysctl >/dev/null 2>&1; then
  echo "  SKIP fail-closed-on-unreadable-sysctl -- this host has a real sysctl on PATH, cannot construct genuine unreadability without root tricks this test won't use"
else
  want_exit "an unreadable load average fails closed (refuses), never reads as healthy" "$RC" 2 "$OUT"
  want_contains "...says it could not measure, not that it's fine" "could not read load average" "$OUT"
fi

# --- Threshold overrides: 0 disables that one check ------------------------
fake_darwin 50.00 10 100
OUT=$(SUPERVISOR_MAX_LOAD_PER_CORE=0 SUPERVISOR_MIN_FREE_MEM_GB=0 SUPERVISOR_MAX_AGENT_SESSIONS=0 HOST_PRESSURE_TEST_OS=Darwin PATH="$D/bin:$PATH" "$GATE"); RC=$?
want_exit "0/0 disables both checks even under real pressure" "$RC" 0 "$OUT"

# --- THIRD GATE: work in flight, not agent session count (#663, re-scoped
# by #826) --------------------------------------------------------------
# Isolates the gate by disabling load/mem (fake_darwin gives them harmless
# values anyway, but 0/0 removes any doubt) and fakes the two inputs
# count-work-in-flight.sh itself reads (sessions.sh and cli.py, via that
# script's own WORK_IN_FLIGHT_SESSIONS_SH/WORK_IN_FLIGHT_CLI env seams --
# see test_count_work_in_flight.sh for that instrument's own coverage in
# isolation). This section only has to prove host-pressure.sh's GATE reads
# and compares the number correctly, not re-derive the aggregation.
fake_work_in_flight() {
  # $1 = number of busy/hung tmux lanes to fabricate (one session, that
  # many lane rows, all "busy" -- this gate does not care which of the two
  # states, only that neither is "free").
  local n="$1" i lanes="[]"
  local rows=""
  for ((i = 1; i <= n; i++)); do
    [ -n "$rows" ] && rows="$rows,"
    rows="$rows{\"name\": \"lane-$i\", \"state\": \"busy\"}"
  done
  cat > "$D/fake-sessions.sh" <<EOF
#!/bin/bash
cat <<'JSON'
[{"session": "agent-supervisor", "lanes": [$rows]}]
JSON
EOF
  chmod +x "$D/fake-sessions.sh"
  cat > "$D/fake-cli.py" <<'EOF'
#!/usr/bin/env python3
print('{"lanes": [], "tasks": []}')
EOF
  export WORK_IN_FLIGHT_SESSIONS_SH="$D/fake-sessions.sh"
  export WORK_IN_FLIGHT_CLI="$D/fake-cli.py"
}

fake_darwin 1.00 10 10000000
fake_work_in_flight 17
OUT=$(SUPERVISOR_MAX_LOAD_PER_CORE=0 SUPERVISOR_MIN_FREE_MEM_GB=0 HOST_PRESSURE_TEST_OS=Darwin PATH="$D/bin:$PATH" "$GATE"); RC=$?
want_exit "17 lanes with work in flight stays under the ceiling of 20, allows" "$RC" 0 "$OUT"
want_contains "...says why" "within limits" "$OUT"

fake_work_in_flight 22
OUT=$(SUPERVISOR_MAX_LOAD_PER_CORE=0 SUPERVISOR_MIN_FREE_MEM_GB=0 HOST_PRESSURE_TEST_OS=Darwin PATH="$D/bin:$PATH" "$GATE"); RC=$?
want_exit "22 lanes with work in flight >= the default ceiling of 20 refuses" "$RC" 1 "$OUT"
want_contains "...names the actual comparison" "work in flight 22 >= 20" "$OUT"

OUT=$(SUPERVISOR_MAX_LOAD_PER_CORE=0 SUPERVISOR_MIN_FREE_MEM_GB=0 SUPERVISOR_MAX_AGENT_SESSIONS=0 HOST_PRESSURE_TEST_OS=Darwin PATH="$D/bin:$PATH" "$GATE"); RC=$?
want_exit "SUPERVISOR_MAX_AGENT_SESSIONS=0 disables the gate even at 22 lanes with work in flight" "$RC" 0 "$OUT"

# #826's own load-bearing case: 20 tmux panes exist (18 real agent
# processes, by count-agents.sh's own still-correct classification) but
# only a handful are actually busy/hung -- the gate must allow a new
# dispatch here, where the OLD raw-pane-count gate refused it.
fake_work_in_flight 4
OUT=$(SUPERVISOR_MAX_LOAD_PER_CORE=0 SUPERVISOR_MIN_FREE_MEM_GB=0 HOST_PRESSURE_TEST_OS=Darwin PATH="$D/bin:$PATH" "$GATE"); RC=$?
want_exit "#826: 4 lanes actually busy, well under 20 tmux panes existing, allows -- the whole point of this fix" "$RC" 0 "$OUT"
want_contains "...says why" "within limits" "$OUT"

unset WORK_IN_FLIGHT_SESSIONS_SH WORK_IN_FLIGHT_CLI

# Fail closed when count-work-in-flight.sh itself cannot be found next to a
# copy of host-pressure.sh (the same "instrument cannot see" shape as the
# load/mem gates' own fail-closed cases above).
D3=$(mktemp -d)
cp "$GATE" "$D3/host-pressure.sh"
chmod +x "$D3/host-pressure.sh"
OUT=$(SUPERVISOR_MAX_LOAD_PER_CORE=0 SUPERVISOR_MIN_FREE_MEM_GB=0 HOST_PRESSURE_TEST_OS=Darwin PATH="$D/bin:$PATH" "$D3/host-pressure.sh"); RC=$?
want_exit "count-work-in-flight.sh missing next to host-pressure.sh fails closed (refuses), never reads as healthy" "$RC" 2 "$OUT"
want_contains "...says it could not measure, not that it's fine" "could not read work-in-flight count" "$OUT"

# --- MUTATION: invert the work-in-flight comparison -> the REFUSE case must go RED
MUT_SESS="$D/host-pressure-mutant-invert-sessions.sh"
mut_sess_rc=0
python3 - "$GATE" "$MUT_SESS" <<'PY' || mut_sess_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '[ "$inflight" -ge "$MAX_AGENT_SESSIONS" ]'
assert marker in text, "work-in-flight comparison not found -- script shape changed"
assert text.count(marker) == 1, "work-in-flight comparison not unique -- script shape changed"
open(dst, "w").write(text.replace(marker, '[ "$inflight" -lt "$MAX_AGENT_SESSIONS" ]', 1))
PY
if [ "$mut_sess_rc" -ne 0 ]; then
  bad "setup: patched a copy of host-pressure.sh with the work-in-flight comparison inverted" "could not patch $GATE (exit $mut_sess_rc)"
else
  chmod +x "$MUT_SESS"
  cp "$HERE/../../scripts/supervisor/count-work-in-flight.sh" "$D/count-work-in-flight.sh"
  fake_work_in_flight 22
  MUT_SESS_OUT=$(SUPERVISOR_MAX_LOAD_PER_CORE=0 SUPERVISOR_MIN_FREE_MEM_GB=0 HOST_PRESSURE_TEST_OS=Darwin PATH="$D/bin:$PATH" "$MUT_SESS"); MUT_SESS_RC=$?
  unset WORK_IN_FLIGHT_SESSIONS_SH WORK_IN_FLIGHT_CLI
  if [ "$MUT_SESS_RC" -eq 0 ]; then
    ok "mutation confirmed: inverting the work-in-flight comparison lets 22 in through (the REFUSE case's GREEN assertion would now be RED)"
  else
    bad "mutation confirmed: inverting the work-in-flight comparison lets 22 in through" "expected exit 0 on the mutant, got $MUT_SESS_RC: $MUT_SESS_OUT"
  fi
fi

# --- MUTATION: invert the load comparison -> the REFUSE case must go RED --
# Proves the first assertion is pinned to the actual >= comparison, not to
# something else in the script already exiting nonzero.
MUT="$D/host-pressure-mutant-invert.sh"
mut_rc=0
python3 - "$GATE" "$MUT" <<'PY' || mut_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = "exit !(l >= m)"
assert marker in text, "load comparison not found -- script shape changed"
assert text.count(marker) == 1, "load comparison not unique -- script shape changed"
open(dst, "w").write(text.replace(marker, "exit !(l < m)", 1))
PY
if [ "$mut_rc" -ne 0 ]; then
  bad "setup: patched a copy of host-pressure.sh with the load comparison inverted" "could not patch $GATE (exit $mut_rc)"
else
  chmod +x "$MUT"
  fake_darwin 50.00 10 100000000
  MUT_OUT=$(HOST_PRESSURE_TEST_OS=Darwin SUPERVISOR_MAX_AGENT_SESSIONS=0 PATH="$D/bin:$PATH" "$MUT"); MUT_RC=$?
  if [ "$MUT_RC" -eq 0 ]; then
    ok "mutation confirmed: inverting the load comparison lets 5.00 load/core through (the REFUSE case's GREEN assertion would now be RED)"
  else
    bad "mutation confirmed: inverting the load comparison lets 5.00 load/core through" "expected exit 0 on the mutant, got $MUT_RC: $MUT_OUT"
  fi
fi

echo
echo "host-pressure.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
