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
want_contains "--verbose: daemon helper labeled, not silently dropped" "daemon helper (cc-daemon pty host, --bg-pty-host)" "$VERR"
want_contains "--verbose: transient shell labeled" "transient shell" "$VERR"
want_contains "--verbose: tail -f labeled" "log follower" "$VERR"
want_contains "--verbose: the measuring pipeline's own sed labeled" "measuring pipeline itself" "$VERR"
want_not_contains "--verbose: unrelated bash (never mentions claude) is not dumped into the breakdown" "999" "$VERR"

# --- agent-supervisor#678: a real ps pads pid= to the width of the WIDEST
# pid currently on the host, with LEADING spaces -- not to each line's own
# width. A naive `${line%% *}` / `${line#* }` split treats that leading
# space as if it were the pid/comm separator, so a genuine agent session
# whose pid happens to be narrower than the host's current max is dropped
# silently: not counted, not excluded either (#678's own report -- 16
# counted + 2 excluded = 18 of 19, the 19th nowhere). This fixture
# reproduces that shape directly: pid 8454 is 4 digits, pid 99999 elsewhere
# on the "host" is 5, so a real `ps` right-justifies 8454 to "  8454" -- the
# same padding the estate's own capture showed live.
cat > "$D/bin/ps" <<'EOF'
#!/bin/bash
case "$*" in
  *comm=*)
    cat <<'FIXTURE'
    1 launchd
 8454 claude
99999 zsh
16962 claude bg-pty-host
16980 claude bg-spare
30625 zsh
40100 tail
40200 sed
FIXTURE
    ;;
  *command=*)
    cat <<'FIXTURE'
    1 /sbin/launchd
 8454 claude --model sonnet --dangerously-skip-permissions --strict-mcp-config
99999 /bin/zsh -c source /Users/jon/.claude/shell-snapshots/snapshot-zsh-1.sh
16962 claude bg-pty-host --bg-pty-host /tmp/cc-daemon-501/spare/x.pty.sock
16980 claude bg-spare --bg-spare /tmp/cc-daemon-501/spare/x.claim.sock
30625 /bin/zsh -c source /Users/jon/.claude/shell-snapshots/snapshot-zsh-2.sh
40100 tail -f /tmp/claude-501/some.log
40200 sed -n /claude/p
FIXTURE
    ;;
esac
EOF
chmod +x "$D/bin/ps"
OUT5=$(run); RC5=$?
want_exit "#678 padded-pid fixture: exits 0" "$RC5" "0" "$OUT5"
want_eq "#678: the one genuine session (pid 8454, pid narrower than the host max) is still counted" "$OUT5" "1"
VERR5=$(run_verbose 2>&1 1>/dev/null)
want_contains "#678: pid 8454 is counted with its true pid, not merged with its own digits" "  8454  claude" "$VERR5"
want_contains "#678: daemon helper still excluded correctly despite padding elsewhere in the table" "daemon helper (cc-daemon pty host, --bg-pty-host)" "$VERR5"

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

# --- agent-estate#871 review: fault-inject the SECOND `ps` call specifically
# (the `command=` one DAEMON_ARGV_RE is checked against), distinct from the
# two cases above which both break the FIRST, fatal `comm=` call. The
# script's own comment says this one FAILS OPEN: if it can't be read,
# CMD_OUT stays empty, every argv lookup finds no line for a given pid, and
# a daemon process that can't be positively identified via argv is COUNTED
# rather than excluded (never default to excluding something you can't
# positively identify as daemon infrastructure). The reviewer read this in
# the diff but did not fault-inject it; do that here. A `ps` that answers
# `comm=` but fails `command=` reproduces the exact failure this comment
# describes -- verify the process is counted, not silently dropped.
D5=$(mktemp -d); mkdir -p "$D5/bin"
cat > "$D5/bin/ps" <<'EOF'
#!/bin/bash
case "$*" in
  *comm=*)
    cat <<'FIXTURE'
920 claude
FIXTURE
    ;;
  *command=*)
    exit 1
    ;;
esac
EOF
chmod +x "$D5/bin/ps"
run5() { PATH="$D5/bin:$PATH" "$SCRIPT" "$@"; }
OUT9=$(run5); RC9=$?
want_exit "fail-open: second (command=) ps call failing still exits 0" "$RC9" "0" "$OUT9"
want_eq "fail-open: a process that can't be argv-identified is COUNTED, not excluded, when the second ps call fails" "$OUT9" "1"

# --- agent-estate#613 sub-decision 1: daemon exclusion by argv, not by an
# accident of what `comm` resolves to -----------------------------------
# Reproduces the Director's live finding verbatim: on a host where a
# node-launched `claude.exe` resolves through `comm` as the FULL
# interpreter path (never the bare `claude.exe` AGENT_COMMAND_RE expects),
# `claude.exe daemon run ...` and the `--session-id ... --fork-session
# --resume ...` processes cc-daemon forks from its pooled pty host were
# ALREADY excluded from the count -- but only because comm happened not to
# match, not because anything recognized them. This fixture pins comm to
# that exact full-path shape and asserts they are still excluded (mutation
# direction A) AND now labeled BY NAME in --verbose (mutation direction B,
# the actual fix: the old comm-prefix-matching reason case
# ("claude bg-pty-host"*) could never have matched this comm shape, so the
# same processes fell into the "other, comm did not match" bucket pre-fix
# -- proving the label is deliberate, not merely that the count is right).
D3=$(mktemp -d); mkdir -p "$D3/bin"
cat > "$D3/bin/ps" <<'EOF'
#!/bin/bash
case "$*" in
  *comm=*)
    cat <<'FIXTURE'
600 claude
601 /opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe
602 /opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe
603 /opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe
FIXTURE
    ;;
  *command=*)
    cat <<'FIXTURE'
600 claude --model sonnet --dangerously-skip-permissions --strict-mcp-config
601 /opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe daemon run --origin transient --spawned-by {"label":"claude","cwd":"/tmp","pid":1}
602 /opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe --bg-pty-host /tmp/cc-daemon-501/x/pty/y.sock 254 64 -- /opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe --session-id y --fork-session --resume /Users/jon/.claude/projects/x/y.jsonl --model claude-sonnet-5
603 /opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe --session-id y --fork-session --resume /Users/jon/.claude/projects/x/y.jsonl --model claude-sonnet-5
FIXTURE
    ;;
esac
EOF
chmod +x "$D3/bin/ps"
run3()          { PATH="$D3/bin:$PATH" "$SCRIPT" "$@"; }
OUT6=$(run3); RC6=$?
want_exit "#613: full-interpreter-path daemon fixture exits 0" "$RC6" "0" "$OUT6"
want_eq "#613: the 1 real agent session counts, the 3 daemon-shaped claude.exe processes do not" "$OUT6" "1"
VERR6=$(PATH="$D3/bin:$PATH" "$SCRIPT" --verbose 2>&1 1>/dev/null)
want_contains "#613: 'daemon run' subcommand excluded by name, not by comm-path accident" "daemon helper (cc-daemon supervisor, daemon run)" "$VERR6"
want_contains "#613: --bg-pty-host excluded by name even though comm is the full interpreter path" "daemon helper (cc-daemon pty host, --bg-pty-host)" "$VERR6"
want_contains "#613: --fork-session (cc-daemon's pooled-session fork) excluded by name" "daemon helper (cc-daemon forked session, --fork-session)" "$VERR6"

# --- Mutation: a process whose argv merely CONTAINS the substring "claude"
# near "daemon"/"run" but is not actually a claude(.exe)-prefixed "daemon
# run" invocation must NOT be excluded -- DAEMON_ARGV_RE's daemon-run
# alternative is anchored to "claude(.exe) daemon run" as a token
# sequence, not a bare "daemon" + "run" substring anywhere in argv. This is
# the fail-closed direction: an agent process whose own prose happens to
# mention "daemon run" (e.g. working on this very issue) must still count.
cat > "$D3/bin/ps" <<'EOF'
#!/bin/bash
case "$*" in
  *comm=*)
    cat <<'FIXTURE'
700 claude
FIXTURE
    ;;
  *command=*)
    cat <<'FIXTURE'
700 claude --model sonnet --dangerously-skip-permissions --strict-mcp-config -p "describe what claude.exe daemon run does in count-agents.sh"
FIXTURE
    ;;
esac
EOF
chmod +x "$D3/bin/ps"
OUT7=$(run3); RC7=$?
want_exit "#613: comm=claude with 'daemon run' only in unrelated prose: exits 0" "$RC7" "0" "$OUT7"
want_eq "#613: fail-closed -- comm already identifies this as a real agent session, so it still counts" "$OUT7" "1"

# --- agent-estate#871 review (ae871-rev871, REQUEST_CHANGES on 28adf24) §3:
# a dispatched claude -p lane's own task PROMPT is a positional argv element
# (claude_print_transport.py: `command.append(prompt)`, always the LAST
# token). Before this fix, three of DAEMON_ARGV_RE's four alternatives were
# bare substring matches against the WHOLE argv string, so a lane merely
# asked to discuss/review these flags by name had its own genuine session
# excluded -- the false-exclusion direction #613 says is categorically
# worse than the false-inclusion this whole check exists to fix (an
# under-count leaves the host-pressure guard blind to real load). This
# fixture reproduces the reviewer's exact construction: two live-shaped
# comm=claude sessions whose prompts each name one of the unanchored flags.
D4=$(mktemp -d); mkdir -p "$D4/bin"
cat > "$D4/bin/ps" <<'EOF'
#!/bin/bash
case "$*" in
  *comm=*)
    cat <<'FIXTURE'
910 claude
911 claude
FIXTURE
    ;;
  *command=*)
    cat <<'FIXTURE'
910 claude -p --output-format json --model sonnet --dangerously-skip-permissions --strict-mcp-config --resume abc-123 Review agent-estate#871 and check whether the daemon --fork-session flag ever leaks into a real dispatched lanes argv
911 claude -p --output-format json --model sonnet --dangerously-skip-permissions --strict-mcp-config --resume def-456 Investigate the --bg-pty-host socket path bug reported by the Director
FIXTURE
    ;;
esac
EOF
chmod +x "$D4/bin/ps"
run4() { PATH="$D4/bin:$PATH" "$SCRIPT" "$@"; }
OUT8=$(run4); RC8=$?
want_exit "#871 §3: two dispatched lanes whose prompts name daemon flags: exits 0" "$RC8" "0" "$OUT8"
want_eq "#871 §3: both genuine lanes still count (2), not excluded because their PROMPT text mentions a daemon flag" "$OUT8" "2"

# --- agent-estate#871 review §4 Direction A: the mutation gap the reviewer
# found. Every #613 fixture above pairs a daemon argv shape with a comm
# that ALSO independently fails AGENT_COMMAND_RE (a two-word title like
# "claude bg-pty-host", or a full interpreter path) -- so on THIS host,
# blanking DAEMON_ARGV_RE to match nothing does not turn any test red: the
# pre-existing comm mismatch already excludes those pids regardless of
# whether DAEMON_ARGV_RE does anything at all. Verified live as part of
# this fix (see PR comment): with DAEMON_ARGV_RE set to match nothing,
# `count-agents.sh: 29 passed, 0 failed` -- the suite passed with the
# mechanism this whole issue is about entirely disabled. The one pairing
# where DAEMON_ARGV_RE is actually load-bearing is comm resolving to the
# BARE `claude`/`claude.exe` string (matches AGENT_COMMAND_RE on its own)
# while argv is still daemon-shaped -- the exact scenario the header
# comment warns could happen after an install-path change. This fixture
# pins THAT pairing, so a mutation blanking DAEMON_ARGV_RE fails HERE.
D6=$(mktemp -d); mkdir -p "$D6/bin"
cat > "$D6/bin/ps" <<'EOF'
#!/bin/bash
case "$*" in
  *comm=*)
    cat <<'FIXTURE'
930 claude.exe
931 claude
FIXTURE
    ;;
  *command=*)
    cat <<'FIXTURE'
930 claude.exe --bg-pty-host /tmp/cc-daemon-501/spare/x.pty.sock
931 claude --model sonnet --dangerously-skip-permissions --strict-mcp-config
FIXTURE
    ;;
esac
EOF
chmod +x "$D6/bin/ps"
run6() { PATH="$D6/bin:$PATH" "$SCRIPT" "$@"; }
OUT10=$(run6); RC10=$?
want_exit "#871 §4A: bare comm=claude.exe daemon pairing exits 0" "$RC10" "0" "$OUT10"
want_eq "#871 §4A: DAEMON_ARGV_RE is load-bearing here -- excludes the bare-comm daemon pid, counts only the 1 real session" "$OUT10" "1"

# --- agent-estate#871 re-review (ae871-rerev871, REQUEST_CHANGES on
# c34b6c3) §2: round-1's fix anchored DAEMON_ARGV_RE with `(^|/)claude...`,
# intending "the daemon's own launch line starts here" -- but `(^|/)` binds
# to ANY slash anywhere in the string, not just the string's own start. A
# dispatched lane whose prompt merely QUOTES the daemon's own
# path-qualified launch line (exactly what this PR's own diff, description,
# and prior review comments do, verbatim) still has a `/`-preceded
# `claude(.exe)` token sitting in argv, deep inside the free-text prompt --
# reopening the identical false-exclusion one layer down. This fixture is
# the re-reviewer's own construction: three live-shaped comm=claude
# sessions whose prompts each quote the real daemon's full interpreter-path
# launch line for one of the three previously-unanchored alternatives.
D7=$(mktemp -d); mkdir -p "$D7/bin"
cat > "$D7/bin/ps" <<'EOF'
#!/bin/bash
case "$*" in
  *comm=*)
    cat <<'FIXTURE'
940 claude
941 claude
942 claude
FIXTURE
    ;;
  *command=*)
    cat <<'FIXTURE'
940 claude -p --output-format json --model sonnet --dangerously-skip-permissions --strict-mcp-config --session-id abc123 Investigate report that /opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe --bg-pty-host is leaking into the count-agents.sh regex, see agent-estate#871
941 claude -p --output-format json --model sonnet --dangerously-skip-permissions --strict-mcp-config --session-id def456 Please confirm /opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe daemon run never appears in a real dispatched lane, per agent-estate#871
942 claude -p --output-format json --model sonnet --dangerously-skip-permissions --strict-mcp-config --session-id ghi789 Quote the review verbatim: /opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe --session-id y --fork-session --resume /Users/jon/.claude/projects/x/y.jsonl
FIXTURE
    ;;
esac
EOF
chmod +x "$D7/bin/ps"
run7() { PATH="$D7/bin:$PATH" "$SCRIPT" "$@"; }
OUT11=$(run7); RC11=$?
want_exit "#871 §2: three dispatched lanes whose prompts QUOTE the daemon's path-qualified launch line: exits 0" "$RC11" "0" "$OUT11"
want_eq "#871 §2: all three genuine lanes still count (3), not excluded because their PROMPT text quotes the daemon's own launch line" "$OUT11" "3"

echo
echo "count-agents.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
