#!/bin/bash
# count-agents.sh's own classification (agent-supervisor#663) -- the estate
# tick's STEP 0 host guard's `pgrep -f claude | wc -l` over-counts (matches
# any process whose argv mentions a `.claude` path, and counts its own
# measuring pipeline) and under-reports nothing, so a fixed `ps` fixture
# below reproduces the issue's own measured breakdown (17 agent sessions,
# 22 `pgrep -f claude` hits) and every case here is deterministic regardless
# of what this machine's real process table happens to hold at test time --
# same reasoning as test_host_pressure.sh's own sysctl/vm_stat fakes, this
# file fakes `ps` via PATH the same way, needing no seam in the script
# itself.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$HERE/../../scripts/supervisor/count-agents.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_eq()       { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "want '$3', got '$2'"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_not_contains() { if grep -qF -- "$2" <<<"$3"; then bad "$1" "did NOT want '$2' in: $3"; else ok "$1"; fi }

echo "count-agents.sh"

D=$(mktemp -d); mkdir -p "$D/bin"

# Reproduces #663's own measured breakdown: 3 real agent sessions + 1
# claude.exe (Windows-shaped harness name), 2 daemon helpers (bg-pty-host,
# bg-spare), 2 transient snapshot shells, 1 tail -f, 1 sed (the measuring
# pipeline itself), 1 unrelated bash. A naive `grep -c claude` over the
# COMMAND fixture reads 4 (agents) + 2 (daemon) + 2 (shells) + 1 (tail) + 1
# (sed) = 10; the real agent-session count is 4.
write_fixture() {
  cat > "$D/bin/ps" <<'EOF'
#!/bin/bash
case "$*" in
  *comm=*)
    cat <<'FIXTURE'
100 claude
101 claude
102 claude
103 claude.exe
200 claude bg-pty-host
201 claude bg-spare
300 /bin/zsh
301 /bin/zsh
400 tail
500 sed
999 bash
FIXTURE
    ;;
  *command=*)
    cat <<'FIXTURE'
100 claude --model sonnet --dangerously-skip-permissions --strict-mcp-config
101 claude --model sonnet --dangerously-skip-permissions --strict-mcp-config
102 claude --model sonnet --dangerously-skip-permissions --strict-mcp-config
103 claude.exe --model sonnet --dangerously-skip-permissions --strict-mcp-config
200 claude bg-pty-host --bg-pty-host /tmp/cc-daemon-501/spare/x.pty.sock
201 claude bg-spare --bg-spare /tmp/cc-daemon-501/spare/x.claim.sock
300 /bin/zsh -c source /Users/jon/.claude/shell-snapshots/snapshot-zsh-1.sh
301 /bin/zsh -c source /Users/jon/.claude/shell-snapshots/snapshot-zsh-2.sh
400 tail -f /tmp/claude-501/some.log
500 sed -n /claude/p
999 bash -c "echo unrelated noise, never mentions the word"
FIXTURE
    ;;
esac
EOF
  chmod +x "$D/bin/ps"
}

run()          { PATH="$D/bin:$PATH" "$SCRIPT" "$@"; }
run_verbose()  { PATH="$D/bin:$PATH" "$SCRIPT" --verbose; }

# --- Basic count: matches comm exactly, not argv --------------------------
write_fixture
OUT=$(run); RC=$?
want_exit "fixture (17-shaped breakdown): exits 0" "$RC" "0" "$OUT"
want_eq "counts exactly the 4 comm=claude/claude.exe processes, not the 10 a substring match would" "$OUT" "4"

# --- Positive control: the script CAN report non-zero ---------------------
# (the assertion above already proves this, stated explicitly so a later
# reader does not have to infer it: a fixture with agent-shaped comm values
# produces a non-zero count, so a real "0" from this script is not just its
# only reachable output.)
if [ "$OUT" != "0" ]; then ok "positive control: non-zero fixture returns non-zero, not a blind '0'"
else bad "positive control" "expected non-zero, got 0"; fi

# --- Old instrument over-counts on the SAME fixture, ours does not --------
# Mutation direction A: a naive substring match (what pgrep -f claude does)
# against this fixture's full command lines reads 10 -- every category
# named in #663 (agents, daemon helpers, transient shells, tail, sed) all
# mention "claude" somewhere in argv. Printed side by side, not asserted in
# prose only.
NAIVE=$(PATH="$D/bin:$PATH" ps -Ao pid=,command= | grep -c -i claude)
echo "  (naive substring count on this fixture: $NAIVE, count-agents.sh: $OUT)"
if [ "$NAIVE" -gt "$OUT" ]; then
  ok "mutation A: naive substring match over-counts ($NAIVE) where count-agents.sh does not ($OUT)"
else
  bad "mutation A" "expected naive ($NAIVE) > count-agents.sh ($OUT)"
fi

# --- Mutation direction B: adding a real agent increments the count -------
cat >> "$D/bin/ps" <<'DONE'
DONE
# Rewrite the fixture with one more comm=claude line (pid 104) -- reverse of
# the mutation above: a REAL agent session appearing must move the count up,
# not just noise failing to move it.
cat > "$D/bin/ps" <<'EOF'
#!/bin/bash
case "$*" in
  *comm=*)
    cat <<'FIXTURE'
100 claude
101 claude
102 claude
103 claude.exe
104 claude
200 claude bg-pty-host
201 claude bg-spare
300 /bin/zsh
301 /bin/zsh
400 tail
500 sed
999 bash
FIXTURE
    ;;
  *command=*)
    cat <<'FIXTURE'
104 claude --model sonnet --dangerously-skip-permissions --strict-mcp-config
FIXTURE
    ;;
esac
EOF
chmod +x "$D/bin/ps"
OUT2=$(run); RC2=$?
want_exit "one more real agent added: still exits 0" "$RC2" "0" "$OUT2"
want_eq "mutation B: count increments from 4 to 5 when a real agent session appears" "$OUT2" "5"

# --- Daemon helpers, transient shells, tail, sed excluded by name in --verbose, not miscounted
write_fixture
VERR=$(run_verbose 2>&1 1>/dev/null)
want_contains "--verbose: counted section lists the 4 real agent pids" "  100  claude" "$VERR"
want_contains "--verbose: daemon helper labeled, not silently dropped" "daemon helper (cc-daemon pty host)" "$VERR"
want_contains "--verbose: transient shell labeled" "transient shell" "$VERR"
want_contains "--verbose: tail -f labeled" "log follower" "$VERR"
want_contains "--verbose: the measuring pipeline's own sed labeled" "measuring pipeline itself" "$VERR"
want_not_contains "--verbose: unrelated bash (never mentions claude) is not dumped into the breakdown" "999" "$VERR"

# --- Fails closed, never silently reports 0, when ps itself is unreadable -
# `PATH="$D2/bin:$PATH"` keeps bash/awk/etc. resolvable (the shebang's own
# `env bash` lookup needs a real PATH) while still putting a broken `ps`
# first -- exercises the actual failure shape (ps present but useless),
# which is the realistic case; ps being entirely absent from every PATH
# component is not a state this host, or CI's, can be forced into safely.
D2=$(mktemp -d); mkdir -p "$D2/bin"
cat > "$D2/bin/ps" <<'EOF'
#!/bin/bash
exit 1
EOF
chmod +x "$D2/bin/ps"
OUT3=$(PATH="$D2/bin:$PATH" "$SCRIPT" 2>&1); RC3=$?
want_exit "ps exits non-zero: refuses (exit 2), never guesses 0" "$RC3" "2" "$OUT3"

# --- A ps that runs cleanly but prints nothing is also refused, not read as "0 agents"
cat > "$D2/bin/ps" <<'EOF'
#!/bin/bash
exit 0
EOF
chmod +x "$D2/bin/ps"
OUT4=$(PATH="$D2/bin:$PATH" "$SCRIPT" 2>&1); RC4=$?
want_exit "ps runs but prints nothing: refuses rather than reporting 0" "$RC4" "2" "$OUT4"

echo
echo "count-agents.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
