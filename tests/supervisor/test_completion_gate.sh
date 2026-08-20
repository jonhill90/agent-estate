#!/usr/bin/env bash
# Tests for completion-gate.sh.
#
# The point of this suite is NOT that the gate passes when it should. It is that
# the gate FAILS when it should -- every prior gate in this estate passed and
# could not fail, which is why the estate reported itself healthy while dead.
# So the mutation cases below are the load-bearing ones, not the happy path.
#
# Discovered automatically by tests/supervisor/test_shell_suites.py, which globs
# test_*.sh and runs each as a subprocess. No registration needed.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATE="$HERE/../../scripts/supervisor/completion-gate.sh"
pass=0; fail=0

ok()  { printf '  ok   %s\n' "$*"; pass=$((pass+1)); }
bad() { printf '  FAIL %s\n' "$*"; fail=$((fail+1)); }
check() { # check <label> <expected-rc> <actual-rc>
  if [ "$2" = "$3" ]; then ok "$1 (rc=$3)"; else bad "$1 (want rc=$2, got rc=$3)"; fi
}

[ -x "$GATE" ] || { echo "FATAL: gate not executable at $GATE"; exit 1; }

# A throwaway git repo, so the diff check has something real to read.
SCRATCH="$(mktemp -d)"
trap 'rm -rf "$SCRATCH"' EXIT
REPO="$SCRATCH/repo"; mkdir -p "$REPO"
git -C "$REPO" init -q
git -C "$REPO" config user.email t@t; git -C "$REPO" config user.name t
echo base > "$REPO/base.txt"
git -C "$REPO" add -A && git -C "$REPO" commit -qm base
git -C "$REPO" branch -f fakemain
# The task "changed" this file. mkdir FIRST -- an earlier version of this test
# redirected into a non-existent directory, the write failed, the fixture had no
# changed file, and the gate correctly returned 3 for every case. Four "failures"
# that were the harness, not the gate. That is exactly the instrument error this
# whole suite exists to catch, so it is fixed here rather than worked around.
mkdir -p "$REPO/src"
echo changed > "$REPO/src/thing.py"

REPORTS="$SCRATCH/reports"; mkdir -p "$REPORTS"
mkreport() { # mkreport <n> <body>
  printf '# Task %s completion\n\n%s\n\n%s\n' "$1" "$2" "$(head -c 120 /dev/zero | tr '\0' 'x')" \
    > "$REPORTS/TASK$1_COMPLETION.md"
}

run() { COMPLETION_GATE_REPO="$REPO" bash "$GATE" --base fakemain "$@" >/dev/null 2>&1; echo $?; }

echo "== positive control: the harness itself can see a real change =="
if git -C "$REPO" diff --name-only | grep -q 'src/thing.py' || [ -f "$REPO/src/thing.py" ]; then
  ok "fixture has a changed file the gate can find"
else
  bad "fixture is broken -- every result below is meaningless"; exit 1
fi

echo "== happy path =="
mkreport 1 "Modified src/thing.py to do the thing."
check "all proved" 0 "$(run --dir "$REPORTS" --tasks 1)"

echo "== THE CASE THAT MATTERS: a task left no report =="
check "missing report halts" 1 "$(run --dir "$REPORTS" --tasks 1,2)"

echo "== an empty report is not proof =="
: > "$REPORTS/TASK3_COMPLETION.md"
check "empty report halts" 1 "$(run --dir "$REPORTS" --tasks 3)"

echo "== a report that describes work nobody did =="
mkreport 4 "I carefully refactored everything and it is excellent."
check "report naming no changed file halts" 1 "$(run --dir "$REPORTS" --tasks 4)"

echo "== blindness must NOT read as cleanliness =="
check "absent report dir -> 3, not 0" 3 "$(run --dir "$SCRATCH/nope" --tasks 1)"
check "unresolvable base ref -> 3"    3 "$(COMPLETION_GATE_REPO="$REPO" bash "$GATE" --dir "$REPORTS" --tasks 1 --base no/such/ref >/dev/null 2>&1; echo $?)"
NOGIT="$SCRATCH/nogit"; mkdir -p "$NOGIT"
check "non-git repo -> 3" 3 "$(COMPLETION_GATE_REPO="$NOGIT" bash "$GATE" --dir "$REPORTS" --tasks 1 >/dev/null 2>&1; echo $?)"

echo "== a clean tree cannot fail every task for our own blindness =="
CLEAN="$SCRATCH/clean"; mkdir -p "$CLEAN"; git -C "$CLEAN" init -q
git -C "$CLEAN" config user.email t@t; git -C "$CLEAN" config user.name t
echo x > "$CLEAN/x"; git -C "$CLEAN" add -A; git -C "$CLEAN" commit -qm x; git -C "$CLEAN" branch -f fakemain
check "no diff at all -> 3, not 1" 3 "$(COMPLETION_GATE_REPO="$CLEAN" bash "$GATE" --dir "$REPORTS" --tasks 1 --base fakemain >/dev/null 2>&1; echo $?)"

echo "== usage errors are 2, distinct from a real failure =="
check "no --dir"   2 "$(COMPLETION_GATE_REPO="$REPO" bash "$GATE" --tasks 1 >/dev/null 2>&1; echo $?)"
check "no --tasks" 2 "$(COMPLETION_GATE_REPO="$REPO" bash "$GATE" --dir "$REPORTS" >/dev/null 2>&1; echo $?)"

echo "== ANTI-ORPHAN: this estate has shipped tested code with zero callers 5 times =="
CALLERS="$(grep -rl 'completion-gate.sh' "$HERE/../../scripts" "$HERE/../../.github" 2>/dev/null | grep -v 'completion-gate.sh$' | wc -l | tr -d ' ')"
if [ "$CALLERS" -ge 1 ]; then
  ok "completion-gate.sh has $CALLERS caller(s) outside itself"
else
  bad "completion-gate.sh has ZERO callers -- this is the acp_transport.py shape (302 lines, tested, never wired)"
fi

echo
echo "passed=$pass failed=$fail"
[ "$fail" -eq 0 ]
