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

run() { HOST_PRESSURE_TEST_OS=Darwin PATH="$D/bin:$PATH" "$@" "$GATE"; }

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
OUT=$(HOST_PRESSURE_TEST_OS=Darwin PATH="$D2/bin:/usr/bin:/bin" "$GATE" 2>&1); RC=$?
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
OUT=$(SUPERVISOR_MAX_LOAD_PER_CORE=0 SUPERVISOR_MIN_FREE_MEM_GB=0 HOST_PRESSURE_TEST_OS=Darwin PATH="$D/bin:$PATH" "$GATE"); RC=$?
want_exit "0/0 disables both checks even under real pressure" "$RC" 0 "$OUT"

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
  MUT_OUT=$(HOST_PRESSURE_TEST_OS=Darwin PATH="$D/bin:$PATH" "$MUT"); MUT_RC=$?
  if [ "$MUT_RC" -eq 0 ]; then
    ok "mutation confirmed: inverting the load comparison lets 5.00 load/core through (the REFUSE case's GREEN assertion would now be RED)"
  else
    bad "mutation confirmed: inverting the load comparison lets 5.00 load/core through" "expected exit 0 on the mutant, got $MUT_RC: $MUT_OUT"
  fi
fi

echo
echo "host-pressure.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
