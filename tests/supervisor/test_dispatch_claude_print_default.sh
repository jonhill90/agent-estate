#!/bin/bash
# dispatch.sh -- the NORMAL path, never dispatch-claude-print.sh by hand --
# must default a fresh, plain, single-issue `claude` dispatch onto
# `claude-print`, not the candidate lane's tmux pane (agent-supervisor#171).
#
# WHY: #171/#215/#255 measured that flipping `_TRANSPORTS_BY_HARNESS`'s
# tuple order alone would be a LIE -- `Ledger.record_dispatch` (the write
# dispatch.sh's tmux flow makes) never changes what physically delivers a
# brief, so relabelling its default transport without also changing the
# delivery mechanism would record `claude-print` for a lane a human is
# actually typing into. This suite proves the real thing: dispatch.sh,
# called exactly as any other caller calls it (no dispatch-claude-print.sh
# by hand), (1) leaves a freshly selected `claude` candidate's tmux pane
# completely untouched -- no rename-window, no send-keys, still free in the
# ledger afterward -- and (2) produces a SEPARATE, brand-new lane whose
# ledger row reads transport=claude-print and whose task reaches delivered,
# purely from `claude`'s own argv, never a keystroke. `delivered` -- not
# `complete` -- is deliberate (agent-supervisor#278/#320): dispatch.sh now
# returns once `run_detached` has handed the brief off, before the detached
# child does any work, so the ledger status observable at that point is the
# handoff, not the outcome. The stub `claude` this suite drives is a
# single-shot subprocess that prints one JSON result and exits -- it never
# calls `hill90-supervisor complete` the way a real agent session would, so
# there is no completion signal this suite could wait on even if it wanted
# to; `delivered` is the only status a plain dispatch can honestly assert
# here. It also proves the
# opt-out (--live-pane), that a harness with no claude-print transport at
# all (codex) is unaffected, that a multi-issue dispatch falls through to
# the pre-#171 tmux flow (a documented scope boundary, not a silent
# fallback), and that a broken `claude` refuses rather than silently
# falling back to send-keys.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DISPATCH="$HERE/../../scripts/supervisor/dispatch.sh"
export QUOTA_GATE="$HERE/stubs/quota-safe"
# agent-supervisor#500: same reason as this line above -- disable the
# host-pressure gate dispatch.sh now runs before the quota gate, so this
# suite is not sensitive to a CI runner's real load/free-memory.
export SUPERVISOR_MAX_LOAD_PER_CORE=0
export SUPERVISOR_MIN_FREE_MEM_GB=0
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

echo "dispatch.sh -- claude-print default (#171)"

D=$(mktemp -d); mkdir -p "$D/bin" "$D/roots"
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cp "$HERE/stubs/tmux-dispatch" "$D/bin/tmux"
cp "$HERE/stubs/claude" "$D/bin/claude"

git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/repo" 2>/dev/null
REPO="$D/repo"
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name "Test"
git -C "$REPO" checkout -q -b main
echo one > "$REPO/file.txt"
git -C "$REPO" add file.txt
git -C "$REPO" commit -q -m initial
git -C "$REPO" push -q -u origin main
git -C "$REPO" remote set-url origin "git@github.com:acme/agent-dotfiles.git"

: > "$D/prs"
echo "do the thing" > "$D/brief.md"

# Every case below gets its own single-candidate lanes fixture and its own
# issue number, so cases never contend over the same GH claim or the same
# tmux window -- each is a clean, independent dispatch.
one_claude_lane() {
  cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
}
one_codex_lane() {
  cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
4|free-4|codex|  gpt-5.5 medium · /repo/path|1|0
FIX
}

run() {
  : > "$D/tmux.log"
  rm -rf "$D/panes"; mkdir -p "$D/panes"
  PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
    TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 \
    DISPATCH_RESPAWN_SETTLE=0 DISPATCH_LAUNCH_SETTLE=0 \
    DISPATCH_CONFIRM_TRIES=2 DISPATCH_SESSION_TIMEOUT=0 \
    AGENT_SUPERVISOR_STATE_DIR="${LEDGER_STATE:-$(mktemp -d "$D/state.XXXXXX")}" \
    STUB_PANE_PATH="${STUB_PANE_PATH:-$REPO}" \
    CLAUDE_PRINT_BROKEN="${CLAUDE_PRINT_BROKEN:-}" \
    CLAUDE_PRINT_LOG="${CLAUDE_PRINT_LOG:-}" \
    WORKTREE_ROOT="$D/roots" bash "${DISPATCH_SCRIPT:-$DISPATCH}" "$@" 2>&1
}
lane_row() {  # lane_row <state-dir> <lane> -> transport, or '' if unregistered
  AGENT_SUPERVISOR_STATE_DIR="$1" python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
row = Ledger(sys.argv[2]).get_lane(sys.argv[3])
print(row["transport"] if row else "")
' "$HERE/../../scripts/supervisor" "$1" "$2" 2>&1
}
task_status() {  # task_status <state-dir> <task-id> -> status, or '' if none
  AGENT_SUPERVISOR_STATE_DIR="$1" python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
row = Ledger(sys.argv[2]).get_task(sys.argv[3])
print(row["status"] if row else "")
' "$HERE/../../scripts/supervisor" "$1" "$2" 2>&1
}
lane_available() {
  AGENT_SUPERVISOR_STATE_DIR="$1" python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
print(Ledger(sys.argv[2]).lane_available(sys.argv[3]))
' "$HERE/../../scripts/supervisor" "$1" "$2" 2>&1
}

# --- 1. the default: a plain, single-issue claude dispatch goes over
#        claude-print, and the candidate tmux pane is never touched --------
one_claude_lane
echo '171|| Migrate the default' > "$D/issues"
LEDGER_STATE="$D/state-171"
CLAUDE_PRINT_LOG="$D/print.log"
out=$(run 171 migrate-default "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a plain claude dispatch succeeds" "$rc" 0 "$out"
want_contains "dispatch says it routed over claude-print" "routing #171 over claude-print" "$out"

want_missing "the candidate lane's window was never renamed" "rename-window" "$(cat "$D/tmux.log")"
want_missing "the candidate lane never received send-keys" "send-keys" "$(cat "$D/tmux.log")"

# `lane-free`'s own first-sight backfill (unrelated to this dispatch's
# routing decision) registers a never-before-seen free pane as a KNOWN, IDLE
# lane the moment it is scanned -- so t:3 having a ledger row at all is
# expected. What #171 promises is that it was never DISPATCHED to: still
# free, no task, right after a dispatch that could have used it.
CAND_AVAIL=$(lane_available "$D/state-171" "t:3")
if [ "$CAND_AVAIL" = "True" ]; then
  ok "the candidate lane t:3 is still free -- never dispatched to"
else
  bad "the candidate lane t:3 is still free -- never dispatched to" "lane_available: $CAND_AVAIL"
fi

# The label dispatch-claude-print.sh mints: PREFIX is one letter per
# hyphen-separated word of the repo name ("agent-dotfiles" -> "ad"),
# followed by <issue><slug> -- see dispatch-claude-print.sh's own header.
NEW_LANE="ad171-migrate-default"
NEW_TRANSPORT=$(lane_row "$D/state-171" "$NEW_LANE")
if [ "$NEW_TRANSPORT" = "claude-print" ]; then
  ok "the NEW lane's ledger row reads transport=claude-print"
else
  bad "the NEW lane's ledger row reads transport=claude-print" "got '$NEW_TRANSPORT'"
fi

TASK_STATUS=$(task_status "$D/state-171" "$NEW_LANE")
if [ "$TASK_STATUS" = "delivered" ]; then
  ok "the claude-print task reached delivered"
else
  bad "the claude-print task reached delivered" "got status '$TASK_STATUS'"
fi

want_contains "the real brief pointer crossed claude's argv, never send-keys" \
  "$D/brief.md" "$(cat "$D/print.log" 2>/dev/null)"
CLAUDE_PRINT_LOG=""

# --- 2. --live-pane opts back into the pre-#171 tmux flow, unchanged ------
one_claude_lane
echo '173|| Keep this one on tmux' > "$D/issues"
LEDGER_STATE="$D/state-live"
out=$(run --live-pane 173 keep-live "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "--live-pane dispatches successfully" "$rc" 0 "$out"
want_contains "--live-pane sends the brief over tmux" "send-keys" "$(cat "$D/tmux.log")"
LIVE_TRANSPORT=$(lane_row "$D/state-live" "t:3")
if [ "$LIVE_TRANSPORT" = "send-keys" ]; then
  ok "--live-pane's lane is recorded transport=send-keys"
else
  bad "--live-pane's lane is recorded transport=send-keys" "got '$LIVE_TRANSPORT'"
fi

# --- 3. a codex candidate is unaffected -- codex has no claude-print
#        transport at all (_TRANSPORTS_BY_HARNESS), so this is the pre-#171
#        tmux flow with no opt-out needed -----------------------------------
one_codex_lane
echo '174|| A codex issue' > "$D/issues"
LEDGER_STATE="$D/state-codex"
out=$(run 174 codex-lane "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a codex candidate dispatches over tmux, unaffected" "$rc" 0 "$out"
CODEX_TRANSPORT=$(lane_row "$D/state-codex" "t:4")
if [ "$CODEX_TRANSPORT" = "send-keys" ]; then
  ok "the codex lane is recorded transport=send-keys"
else
  bad "the codex lane is recorded transport=send-keys" "got '$CODEX_TRANSPORT'"
fi

# --- 4. a multi-issue dispatch falls through to the tmux flow -- a
#        documented scope boundary (dispatch-claude-print.sh speaks one
#        issue), not a silent fallback: nothing has been claimed or built
#        yet at the point this decision is made ----------------------------
one_claude_lane
printf '175|| First of two\n176|| Second of two\n' > "$D/issues"
LEDGER_STATE="$D/state-multi"
out=$(run 175,176 two-issues "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
want_exit "a multi-issue dispatch still succeeds" "$rc" 0 "$out"
want_contains "a multi-issue dispatch is sent over tmux, not claude-print" "send-keys" "$(cat "$D/tmux.log")"
MULTI_TRANSPORT=$(lane_row "$D/state-multi" "t:3")
if [ "$MULTI_TRANSPORT" = "send-keys" ]; then
  ok "the multi-issue lane is recorded transport=send-keys"
else
  bad "the multi-issue lane is recorded transport=send-keys" "got '$MULTI_TRANSPORT'"
fi

# --- 5. fail closed and loudly: a broken claude refuses, never falls back -
one_claude_lane
echo '177|| Should never land anywhere' > "$D/issues"
LEDGER_STATE="$D/state-broken"
CLAUDE_PRINT_BROKEN=1
out=$(run 177 broken-claude "$D/brief.md" acme/agent-dotfiles "$REPO"); rc=$?
CLAUDE_PRINT_BROKEN=
want_exit "a broken claude refuses the dispatch" "$rc" 1 "$out"
want_missing "a broken claude never falls back to send-keys" "send-keys" "$(cat "$D/tmux.log")"
AVAIL=$(lane_available "$D/state-broken" "t:3")
if [ "$AVAIL" = "True" ]; then
  ok "the candidate lane t:3 is still free after the refusal"
else
  bad "the candidate lane t:3 is still free after the refusal" "lane_available: $AVAIL"
fi

# --- 6. agent-supervisor#576's fix-pass gap (closes #572 for real): the
#        first attempt's trap block landed AFTER `claim.sh take`, not
#        before it -- a signal in that window still stranded the GitHub
#        issue claim, the same shape #572 exists to close, just shrunk to a
#        few bash statements. Exercised directly here, not inferred from
#        reading the diff, and this must NOT depend on `claude` happening
#        to be on the test runner's PATH -- that exact environmental
#        difference (present on the reviewer's machine, absent on CI) is
#        what let the gap ship past the bundled suite the first time
#        (`test_dispatch.sh`'s own #572 assertions only reach this script's
#        internals when `command -v claude` succeeds).
#
# WHY A FULL MUTANT scripts/supervisor/ COPY, not a lone modified
# dispatch-claude-print.sh: dispatch.sh resolves dispatch-claude-print.sh
# from its OWN $HERE (dispatch.sh's own directory, from BASH_SOURCE), so
# reaching it at all through this file's own convention -- "never
# dispatch-claude-print.sh by hand" (see the header above) -- means a real,
# self-consistent scripts/supervisor/ tree, the same technique
# test_dispatch.sh's own #572 mutation checks use (make_mutant_scripts_dir).
make_mutant_scripts_dir() {
  local dir
  dir=$(mktemp -d "$D/mutant.XXXXXX")
  cp -R "$HERE/../../scripts/supervisor/." "$dir/"
  rm -rf "$dir/__pycache__"
  chmod +x "$dir"/*.sh
  printf '%s' "$dir"
}
assignees() { awk -F'|' -v n="$1" '$1==n{print $2}' "$D/issues"; }

# The kill is delivered the same way test_dispatch.sh's own #572 arm
# delivers it: a wrapped claim.sh that runs the real thing, then signals its
# own parent the instant `take` succeeds. Here that parent IS
# dispatch-claude-print.sh (claim.sh is its direct child, no subshell) --
# exactly the process whose trap ordering this test is about.
ICP_KILL_DIR=$(make_mutant_scripts_dir)
mv "$ICP_KILL_DIR/claim.sh" "$ICP_KILL_DIR/claim-real.sh"
cat > "$ICP_KILL_DIR/claim.sh" <<'WRAP'
#!/bin/bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
out=$("$HERE/claim-real.sh" "$@" 2>&1); rc=$?
printf '%s\n' "$out"
if [ "${1:-}" = take ] && [ "$rc" -eq 0 ]; then
  kill "-${DISPATCH_TEST_KILL_SIGNAL:-TERM}" "$PPID" 2>/dev/null
  sleep 1
fi
exit $rc
WRAP
chmod +x "$ICP_KILL_DIR/claim.sh" "$ICP_KILL_DIR/claim-real.sh"

one_claude_lane
echo '901|| a dispatcher signalled right after the ISSUE claim (claude-print)' > "$D/issues"
LEDGER_STATE="$D/state-icp-term"
icp_out=$(DISPATCH_SCRIPT="$ICP_KILL_DIR/dispatch.sh" \
  run 901 icp-term "$D/brief.md" acme/agent-dotfiles "$REPO"); icp_rc=$?
want_exit "a dispatch-claude-print.sh dispatcher SIGTERMed right after the ISSUE claim exits through its trap" \
  "$icp_rc" 143 "$icp_out"
if [ -z "$(assignees 901)" ]; then
  ok "...and the TRAP released the GitHub claim immediately -- no assignee left behind"
else
  bad "...and the TRAP released the GitHub claim immediately" "still assigned: $(assignees 901)"
fi

# --- MUTATION: put the trap block back AFTER claim.sh take -- #576's own
#     original, too-late ordering -- and confirm the claim strands again.
#     Only worth trusting the assertion above if deleting the fix actually
#     breaks it (same discipline test_dispatch.sh's own #572 mutation check
#     uses).
ICP_NOFIX_DIR=$(make_mutant_scripts_dir)
cp "$ICP_KILL_DIR/claim.sh" "$ICP_KILL_DIR/claim-real.sh" "$ICP_NOFIX_DIR/"
chmod +x "$ICP_NOFIX_DIR/claim.sh" "$ICP_NOFIX_DIR/claim-real.sh"
patch_rc=0
python3 - "$ICP_NOFIX_DIR/dispatch-claude-print.sh" <<'PY' || patch_rc=$?
import sys
path = sys.argv[1]
text = open(path).read()

trap_start = text.index('# agent-supervisor#572/#576: every INTENTIONAL failure below already')
take_start = text.index('# --- 1. claim the issue on GitHub, same tool dispatch.sh uses')
take_end_marker = 'fi\n\n'
take_end = text.index(take_end_marker, take_start) + len(take_end_marker)

assert trap_start < take_start, "trap block already after the claim call -- script shape changed"

trap_block = text[trap_start:take_start]
take_block = text[take_start:take_end]

# Reassemble with the claim call FIRST and the trap block installed only
# after it returns -- the exact too-late ordering #576's own diff shipped
# and this whole test exists to catch.
new_text = text[:trap_start] + take_block + trap_block + text[take_end:]
assert new_text != text, "mutation produced no change -- markers matched the same span"
open(path, "w").write(new_text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of dispatch-claude-print.sh with the trap moved back after claim.sh take" \
    "could not patch $ICP_NOFIX_DIR/dispatch-claude-print.sh (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of dispatch-claude-print.sh with the trap moved back after claim.sh take"
  bash -n "$ICP_NOFIX_DIR/dispatch-claude-print.sh" \
    || bad "setup: mutant dispatch-claude-print.sh is still valid bash" "bash -n failed"
  one_claude_lane
  echo '902|| a dispatcher signalled right after the ISSUE claim, trap reverted to too-late' > "$D/issues"
  NOFIX_STATE="$D/state-icp-nofix"
  nofix_out=$(DISPATCH_SCRIPT="$ICP_NOFIX_DIR/dispatch.sh" \
    LEDGER_STATE="$NOFIX_STATE" DISPATCH_TEST_KILL_SIGNAL=TERM \
    run 902 icp-nofix "$D/brief.md" acme/agent-dotfiles "$REPO"); nofix_rc=$?
  if [ -n "$(assignees 902)" ]; then
    ok "mutation confirmed: with the trap reverted to its too-late position, a SIGTERMed dispatcher strands the GitHub claim (the assertion above would now be red)"
  else
    bad "mutation confirmed: with the trap reverted to its too-late position, a SIGTERMed dispatcher strands the GitHub claim" \
      "expected #902 to still show an assignee, got none; rc=$nofix_rc out=$nofix_out"
  fi
fi

echo
echo "dispatch.sh claude-print default: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
