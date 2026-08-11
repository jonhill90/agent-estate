#!/bin/bash
# bootstrap-session.sh creates the session lanes.sh/dispatch.sh operate on.
# Nothing created it before this script; the estate's session existed only
# because it had been built by hand, which is why a fresh clone had a working
# supervisor and nowhere to dispatch.
#
# These tests drive REAL tmux, not the fixture stub the lanes tests use: the
# thing under test IS session and window creation, and a stub that pretended
# to create windows would prove nothing about it.
#
# Every test runs against a throwaway session named for this PID. The live
# estate session must never be a test subject -- refusing to modify a running
# session is the script's central safety property, and a test that got that
# wrong would destroy the lanes it was meant to protect.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BOOT="$HERE/../../scripts/supervisor/bootstrap-session.sh"
S="bootstrap-test-$$"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1 — $2"; fail=$((fail+1)); }
check(){ # check <name> <expected> <actual>
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "want '$2', got '$3'"; fi
}
cleanup() { tmux kill-session -t "$S" 2>/dev/null; }
trap cleanup EXIT INT TERM

if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP no tmux on PATH"; exit 0
fi

windows() { tmux list-windows -t "$S" -F '#{window_index} #{window_name}' 2>/dev/null; }

# 1. Refuses to touch a session that already exists. This is the one that
#    protects live work: the estate's session is always holding lanes.
tmux new-session -d -s "$S" -n existing 2>/dev/null
# Capture the layout rather than hardcoding "1 existing". tmux's base-index is
# a user setting: this session is created by raw tmux, not by the script, so it
# lands at whatever index the local .tmux.conf dictates -- 1 here, 0 on the CI
# runner. The property under test is "unchanged", not "equal to a literal", and
# hardcoding the index tested the runner's config instead of the script.
before_existing="$(windows)"
bash "$BOOT" --session "$S" --lanes 4 --agent bash >/dev/null 2>&1
check "refuses an existing session" "1" "$?"
check "existing session left untouched" "$before_existing" "$(windows)"
cleanup

# 2. Dry run changes nothing at all.
bash "$BOOT" --session "$S" --lanes 3 --agent bash --dry-run >/dev/null 2>&1
check "dry-run exits 0" "0" "$?"
tmux has-session -t "$S" 2>/dev/null
check "dry-run created no session" "1" "$?"

# 3. An agent command that is not on PATH is refused BEFORE any window exists.
#    Otherwise the session comes up with every lane falling straight back to a
#    shell, which lanes.sh reports as `dead` with no indication why.
bash "$BOOT" --session "$S" --agent definitely-not-a-harness >/dev/null 2>&1
check "refuses an agent not on PATH" "1" "$?"
tmux has-session -t "$S" 2>/dev/null
check "no session left behind by that refusal" "1" "$?"

# 4. One window is a supervisor with nowhere to dispatch -- refuse rather than
#    produce something that looks bootstrapped and cannot work.
bash "$BOOT" --session "$S" --lanes 1 --agent bash >/dev/null 2>&1
check "refuses --lanes 1" "1" "$?"

# 5. Creation produces the supervisor at the index lanes.sh reserves, plus
#    free-N lanes named the way dispatch.sh renames them back to.
bash "$BOOT" --session "$S" --lanes 4 --agent bash >/dev/null 2>&1
check "creates cleanly" "0" "$?"
check "supervisor + 3 lanes, named to convention" \
  "1 architecture
2 free-2
3 free-3
4 free-4" "$(windows)"

# 6. THE IMPORTANT ONE. --add-lanes tops a session up without disturbing a
#    window that is holding work. A bootstrap that renamed, renumbered or
#    restarted a busy lane would be the #73 checkout clobber in a new costume.
tmux rename-window -t "$S:2" IN-USE
bash "$BOOT" --session "$S" --lanes 6 --agent bash --add-lanes >/dev/null 2>&1
check "--add-lanes exits 0" "0" "$?"
check "adds only missing windows, busy lane untouched" \
  "1 architecture
2 IN-USE
3 free-3
4 free-4
5 free-5
6 free-6" "$(windows)"

# 7. --add-lanes on an already-full session is a no-op, not an error.
before="$(windows)"
bash "$BOOT" --session "$S" --lanes 6 --agent bash --add-lanes >/dev/null 2>&1
check "--add-lanes when already full exits 0" "0" "$?"
check "--add-lanes when already full changes nothing" "$before" "$(windows)"
cleanup

# 8. --lanes must be a number; a typo must not be read as 0 lanes.
bash "$BOOT" --session "$S" --lanes ten --agent bash >/dev/null 2>&1
check "refuses non-numeric --lanes" "1" "$?"

# 9. A --cwd that does not exist is refused up front rather than producing
#    windows that all start in the wrong directory.
bash "$BOOT" --session "$S" --lanes 3 --agent bash --cwd /no/such/dir >/dev/null 2>&1
check "refuses a missing --cwd" "1" "$?"

# --- Findings from the independent review of #137 ---

# 10. THE BLOCKING ONE. tmux resolves an unambiguous session-name PREFIX as a
#     match when no exact match exists, so `has-session -t foo` returns true
#     when only `foo-2` exists. With --add-lanes the script then added four
#     windows to `foo-2`: a live session it was never told about. That
#     falsified the central safety claim, so this test encodes it directly.
NEIGHBOUR="${S}-2"
tmux new-session -d -s "$NEIGHBOUR" -n important-lane 2>/dev/null
before_neighbour="$(tmux list-windows -t "=$NEIGHBOUR" -F '#{window_index} #{window_name}' 2>/dev/null)"
bash "$BOOT" --session "$S" --lanes 5 --agent bash --add-lanes >/dev/null 2>&1
check "prefix neighbour is NOT modified by --add-lanes" \
  "$before_neighbour" "$(tmux list-windows -t "=$NEIGHBOUR" -F '#{window_index} #{window_name}' 2>/dev/null)"
# and the intended session must actually have been created, not skipped
tmux has-session -t "=$S" 2>/dev/null
check "the named session itself was created" "0" "$?"
cleanup
tmux kill-session -t "=$NEIGHBOUR" 2>/dev/null

# 11. `command -v` succeeds for shell builtins, which have no PATH binary and
#     are not harnesses. `--agent cd` produced exactly the all-`dead` session
#     the PATH guard exists to prevent.
bash "$BOOT" --session "$S" --lanes 3 --agent cd >/dev/null 2>&1
check "refuses a shell builtin as --agent" "1" "$?"
tmux has-session -t "=$S" 2>/dev/null
check "no session left behind by the builtin refusal" "1" "$?"

# 12. tmux rewrites `:` and `.` in session names, then targets built from the
#     original string miss -- previously leaving a stray half-built session
#     that the failure report never mentioned.
sessions_before="$(tmux list-sessions -F '#{session_name}' 2>/dev/null | sort)"
bash "$BOOT" --session "bs:evil-$$" --lanes 3 --agent bash >/dev/null 2>&1
check "refuses a session name containing ':'" "1" "$?"
check "that refusal leaves no stray session" \
  "$sessions_before" "$(tmux list-sessions -F '#{session_name}' 2>/dev/null | sort)"
bash "$BOOT" --session "bs.evil-$$" --lanes 3 --agent bash >/dev/null 2>&1
check "refuses a session name containing '.'" "1" "$?"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
