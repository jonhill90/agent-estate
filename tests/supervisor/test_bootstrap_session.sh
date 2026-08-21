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
source "$HERE/../../scripts/supervisor/tmux-isolation.sh"
S="bootstrap-test-$$"
RT="$(mktemp -d "${TMPDIR:-/tmp}/bootstrap-tmux.XXXXXX")"
unset TMUX
export TMUX_TMPDIR="$RT"
assert_isolated_tmux || exit 1
# agent-supervisor#153: bootstrap-session.sh now writes a ledger row
# (`cli.py adopt-session`) on every session it creates. Without this, every
# run of this suite would write into Jon's REAL ledger at
# ~/.local/state/agent-dotfiles-supervisor -- exactly the "do not mutate the
# live ledger" rule #153's own brief calls out. A scratch state dir, same as
# `--state-dir`/`AGENT_SUPERVISOR_STATE_DIR` is designed for.
LEDGER_DIR="$(mktemp -d "${TMPDIR:-/tmp}/bootstrap-ledger.XXXXXX")"
export AGENT_SUPERVISOR_STATE_DIR="$LEDGER_DIR"
CLI="$HERE/../../scripts/supervisor/cli.py"
session_state() { python3 "$CLI" --state-dir "$LEDGER_DIR" session-state --session "$1" 2>/dev/null | sed -n 's/.*"state":"\([a-z]*\)".*/\1/p'; }
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1 — $2"; fail=$((fail+1)); }
check(){ # check <name> <expected> <actual>
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "want '$2', got '$3'"; fi
}
# agent-supervisor#111: `bootstrap-test-48488` proved this trap was
# incomplete, not absent. Case 10 below stands up a SECOND session
# ($NEIGHBOUR) to reproduce #137's prefix-match bug, and used to kill it only
# with an inline `tmux kill-session` right after the case ran (still present
# below, for the common path -- it frees the session as soon as it is no
# longer needed rather than making every run wait for the trap). That inline
# call is not the EXIT/INT/TERM trap, so a kill, a crash, or this script's own
# `set -e`-free early exit between creating $NEIGHBOUR and reaching it left an
# orphan exactly the leaked-session shape #111 measured live. NEIGHBOUR is
# declared here (empty) so `cleanup` can reference it unconditionally from the
# first line of the script onward -- it only ever actually kills something
# once case 10 has set it.
NEIGHBOUR=""
cleanup() {
  unset TMUX; export TMUX_TMPDIR="$RT"; assert_isolated_tmux
  tmux kill-session -t "$S" 2>/dev/null
  [ -z "$NEIGHBOUR" ] || tmux kill-session -t "=$NEIGHBOUR" 2>/dev/null
}
cleanup_all() { cleanup; rm -rf "$RT" "$LEDGER_DIR"; }
trap cleanup_all EXIT INT TERM

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
  "1 supervisor
2 free-2
3 free-3
4 free-4" "$(windows)"
# agent-supervisor#153 acceptance 2: a session bootstrap-session.sh creates
# classifies as supervised.
check "153: the session bootstrap-session.sh just created is supervised" \
  "supervised" "$(session_state "$S")"

# 6. THE IMPORTANT ONE. --add-lanes tops a session up without disturbing a
#    window that is holding work. A bootstrap that renamed, renumbered or
#    restarted a busy lane would be the #73 checkout clobber in a new costume.
tmux rename-window -t "$S:2" IN-USE
bash "$BOOT" --session "$S" --lanes 6 --agent bash --add-lanes >/dev/null 2>&1
check "--add-lanes exits 0" "0" "$?"
check "adds only missing windows, busy lane untouched" \
  "1 supervisor
2 IN-USE
3 free-3
4 free-4
5 free-5
6 free-6" "$(windows)"
check "153: --add-lanes on an already-supervised session leaves it supervised" \
  "supervised" "$(session_state "$S")"

# 7. --add-lanes on an already-full session is a no-op, not an error.
before="$(windows)"
bash "$BOOT" --session "$S" --lanes 6 --agent bash --add-lanes >/dev/null 2>&1
check "--add-lanes when already full exits 0" "0" "$?"
check "--add-lanes when already full changes nothing" "$before" "$(windows)"
cleanup

# --- agent-supervisor#153: mark a session SUPERVISED vs Jon's own ----------

# 153a/153b use a session name distinct from $S -- $S already picked up a
# ledger row in case 5 above, and a ledger row survives `cleanup` killing
# the tmux session (that survival IS 153c's drift scenario, below). Reusing
# $S here would test "does an old row for this name stick around" rather
# than "does a name with no history at all read unknown", so a fresh name
# with no ledger history keeps these two cases isolated from that one.
# Reuses $NEIGHBOUR (declared at the top, killed by `cleanup` on any exit
# path including a crash mid-case) rather than a plain local var, for the
# same leak-safety case 10 below relies on it for.
NEIGHBOUR="${S}-human"

# 153a. A session tmux knows about that bootstrap-session.sh never touched --
# the `Hill90` shape, a human's own session -- carries no marker at all and
# must classify as unknown, which every caller treats as unsupervised. This
# is acceptance 1 and 3 from as153-brief.md: no marker -> unknown ->
# hands-off, without anyone having to configure anything.
tmux new-session -d -s "$NEIGHBOUR" -n human-window 2>/dev/null
check "153: a session bootstrap-session.sh never created is unknown, not supervised" \
  "unknown" "$(session_state "$NEIGHBOUR")"
tmux kill-session -t "=$NEIGHBOUR" 2>/dev/null

# 153b. --add-lanes against a PRE-EXISTING, never-adopted session (the
# Hill90 shape, topped up by hand or by another tool) must NOT silently
# adopt it. Supervision is a decision bootstrap-session.sh only makes at
# the moment it creates a session -- never as a side effect of adding
# windows to one it did not create.
tmux new-session -d -s "$NEIGHBOUR" -n human-window 2>/dev/null
bash "$BOOT" --session "$NEIGHBOUR" --lanes 3 --agent bash --add-lanes >/dev/null 2>&1
check "153: --add-lanes exits 0 against a pre-existing unadopted session" "0" "$?"
check "153: --add-lanes never adopts a pre-existing session it did not create" \
  "unknown" "$(session_state "$NEIGHBOUR")"
tmux kill-session -t "=$NEIGHBOUR" 2>/dev/null
NEIGHBOUR=""

# 153c. #153's own measured drift: a ledger row can outlive the session it
# names. Adopt a session, kill it, and confirm the STALE row does not read
# as supervised -- there is nothing left to act on, so it must not be
# reported as anything actionable (acceptance 4).
bash "$BOOT" --session "$S" --lanes 2 --agent bash >/dev/null 2>&1
check "153: setup for the stale-row case creates cleanly" "0" "$?"
check "153: setup: freshly created session reads supervised" "supervised" "$(session_state "$S")"
tmux kill-session -t "=$S" 2>/dev/null
check "153: a stale ledger row for a session that no longer exists is unknown, not supervised" \
  "unknown" "$(session_state "$S")"

# 153d. THE ACCEPTANCE TEST (as153-brief.md item 3, verbatim): break the
# marker read and confirm it degrades to unsupervised, never to supervised.
# Recreate the session (so tmux genuinely has it again, isolating this case
# from 153c's drift scenario above) and adopt it normally first, so the ONLY
# thing broken below is the read, not "there was never a row to find" --
# 153a/153b already cover that shape.
bash "$BOOT" --session "$S" --lanes 2 --agent bash >/dev/null 2>&1
check "153: setup for the mutation case creates cleanly" "0" "$?"
check "153: setup: freshly adopted session reads supervised before the mutation" \
  "supervised" "$(session_state "$S")"
# Corrupt the `sessions` table's SCHEMA, not the whole ledger file: renaming
# its primary-key column means `Ledger.__init__` still constructs cleanly
# (`CREATE TABLE IF NOT EXISTS` never touches a table that already exists),
# so this exercises exactly one thing -- `session_marked_supervised`'s own
# `SELECT ... WHERE session = ?` now raises `sqlite3.OperationalError: no
# such column: session` -- the real shape an old ledger predating a future
# schema change, or a hand-edited one, would take. A whole-file corruption
# (garbage bytes, wrong permissions) would ALSO degrade safely, but would
# fail earlier, in `Ledger.__init__` itself, and prove less about this
# specific read.
sqlite3 "$LEDGER_DIR/ledger.sqlite3" \
  "ALTER TABLE sessions RENAME COLUMN session TO sess;" 2>/dev/null
mutant_state="$(session_state "$S")"
check "153: mutation confirmed -- the session genuinely still exists in tmux" \
  "0" "$(tmux has-session -t "=$S" 2>/dev/null; echo $?)"
if [ "$mutant_state" = "supervised" ]; then
  bad "153: a broken marker read must never degrade to supervised" "got 'supervised' with a corrupted sessions table"
else
  ok "153: a broken marker read degrades to unsupervised, never to supervised (got '${mutant_state:-<blank/error>}')"
fi
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
# `cleanup` now kills $NEIGHBOUR too (see its definition above) -- one call
# instead of the separate inline kill this line used to duplicate.
cleanup

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

# --- agent-dotfiles#216: harness identity is RECORDED at bootstrap ---------

# 13. --agent named exactly after a recognised harness gets the option
#     inferred and set on every lane window it creates.
bash "$BOOT" --session "$S" --lanes 3 --agent bash --harness codex >/dev/null 2>&1
check "creates cleanly with an explicit --harness" "0" "$?"
opt() { tmux show-options -p -t "=$S:$1" -v @hill90_lane_harness 2>/dev/null; }
check "explicit --harness recorded on the supervisor window" "codex" "$(opt 1)"
check "explicit --harness recorded on lane window 2" "codex" "$(opt 2)"
check "explicit --harness recorded on lane window 3" "codex" "$(opt 3)"
cleanup

# 14. --agent whose OWN basename names a recognised harness gets it inferred
#     with no --harness flag at all -- the common case, matching what a real
#     `claude`/`codex`/`copilot` launch on PATH looks like.
FAKE_BIN="$(mktemp -d)"
cp "$(command -v bash)" "$FAKE_BIN/claude"
chmod +x "$FAKE_BIN/claude"
PATH="$FAKE_BIN:$PATH" bash "$BOOT" --session "$S" --lanes 2 --agent claude >/dev/null 2>&1
check "creates cleanly inferring harness from --agent's basename" "0" "$?"
check "inferred harness (claude) recorded on the lane" "claude" "$(opt 2)"
cleanup
rm -rf "$FAKE_BIN"

# 15. An --agent this script cannot map to a harness (the pre-existing `bash`
#     fixture used throughout this suite) leaves the option UNSET rather than
#     guessing -- fail closed, not fail permissive (agent-dotfiles#216).
bash "$BOOT" --session "$S" --lanes 2 --agent bash >/dev/null 2>&1
check "creates cleanly with an unmappable --agent" "0" "$?"
check "no harness recorded for an unrecognised agent" "" "$(opt 2)"
cleanup

# 16. An invalid --harness value is refused before any window is created.
bash "$BOOT" --session "$S" --lanes 2 --agent bash --harness gemini >/dev/null 2>&1
check "refuses an unrecognised --harness value" "1" "$?"
tmux has-session -t "=$S" 2>/dev/null
check "no session left behind by an invalid --harness" "1" "$?"

# --- agent-supervisor#135: no --agent means ASK THE REGISTRY, not a literal
#     -- the same H_LAUNCH_CMD dispatch.sh resolves, so there is one
#     definition of how a Claude lane starts. --dry-run is enough here: it
#     prints the resolved AGENT_CMD without needing a real tmux window, and
#     env -u LANES_AGENT_CMD strips the one thing (besides --agent) that is
#     ALLOWED to override the default, so what is left on the "agent=" line
#     is purely what bootstrap-session.sh itself derived.
FAKE_BIN="$(mktemp -d)"
cp "$(command -v bash)" "$FAKE_BIN/claude"
chmod +x "$FAKE_BIN/claude"

out17="$(PATH="$FAKE_BIN:$PATH" env -u LANES_AGENT_CMD -u CLAUDE_LANE_MODEL \
  bash "$BOOT" --session "$S" --lanes 2 --dry-run 2>&1)"
agent_line="$(printf '%s\n' "$out17" | grep -o 'agent=.* cwd=' | sed 's/ cwd=$//')"
check "17. with no --agent/LANES_AGENT_CMD, the default is registry-derived, not a bare literal" \
  "agent=claude --model sonnet --dangerously-skip-permissions" "$agent_line"

# 18. CLAUDE_LANE_MODEL is honoured through the SAME path dispatch.sh reads
#     -- if only dispatch.sh's harness/claude.sh honoured it, bootstrap would
#     still be a second, independent definition even with #135 "fixed".
out18="$(PATH="$FAKE_BIN:$PATH" env -u LANES_AGENT_CMD CLAUDE_LANE_MODEL=haiku \
  bash "$BOOT" --session "$S" --lanes 2 --dry-run 2>&1)"
agent_line18="$(printf '%s\n' "$out18" | grep -o 'agent=.* cwd=' | sed 's/ cwd=$//')"
check "18. CLAUDE_LANE_MODEL overrides the derived default (bootstrap path)" \
  "agent=claude --model haiku --dangerously-skip-permissions" "$agent_line18"

# 19. --agent still wins outright over the registry default -- the override
#     this script documents as how a site without Claude runs codex/copilot.
PATH="$FAKE_BIN:$PATH" env -u LANES_AGENT_CMD bash "$BOOT" --session "$S" --lanes 2 --agent bash --dry-run >/dev/null 2>&1
check "19. explicit --agent still overrides the registry default" "0" "$?"
rm -rf "$FAKE_BIN"

# --- agent-dotfiles#256: --unattended maps a bare harness name to the
#     harness's OWN recorded unattended launch command, per-harness, opt-in
#     only. This is the flag whose absence let a codex lane come up as bare
#     `codex` (no flags) and stall on a sandbox prompt a human had to answer.
FAKE_BIN2="$(mktemp -d)"
for h in claude codex copilot; do
  cp "$(command -v bash)" "$FAKE_BIN2/$h"
  chmod +x "$FAKE_BIN2/$h"
done

# 20. --unattended --agent claude resolves to claude's registered unattended
#     command -- same string #135/#17 already prove is claude's registry
#     default, reached this time through the explicit flag, not the no-agent
#     fallback.
out20="$(PATH="$FAKE_BIN2:$PATH" env -u CLAUDE_LANE_MODEL \
  bash "$BOOT" --session "$S" --lanes 2 --agent claude --unattended --dry-run 2>&1)"
agent_line20="$(printf '%s\n' "$out20" | grep -o 'agent=.* cwd=' | sed 's/ cwd=$//')"
check "20. --unattended --agent claude resolves claude's bypass flag" \
  "agent=claude --model sonnet --dangerously-skip-permissions" "$agent_line20"

# 21. --unattended --agent codex resolves to codex's OWN bypass flag, not
#     claude's -- the exact defect #256 reports: the flag differs per harness
#     and must not be hardcoded to one of them.
out21="$(PATH="$FAKE_BIN2:$PATH" bash "$BOOT" --session "$S" --lanes 2 --agent codex --unattended --dry-run 2>&1)"
agent_line21="$(printf '%s\n' "$out21" | grep -o 'agent=.* cwd=' | sed 's/ cwd=$//')"
check "21. --unattended --agent codex resolves codex's own bypass flag" \
  "agent=codex -a never -s danger-full-access" "$agent_line21"

# 22. --unattended with no --agent at all defaults to the same harness
#     bootstrap-session.sh already defaults to with no --agent (claude).
out22="$(PATH="$FAKE_BIN2:$PATH" env -u LANES_AGENT_CMD -u CLAUDE_LANE_MODEL \
  bash "$BOOT" --session "$S" --lanes 2 --unattended --dry-run 2>&1)"
agent_line22="$(printf '%s\n' "$out22" | grep -o 'agent=.* cwd=' | sed 's/ cwd=$//')"
check "22. --unattended with no --agent defaults to claude's bypass flag" \
  "agent=claude --model sonnet --dangerously-skip-permissions" "$agent_line22"

# 23. --unattended against a harness with no MEASURED unattended command
#     (copilot's --allow-all has never been driven through a live pane, see
#     harness/copilot.sh) refuses and names the harness, rather than guessing.
PATH="$FAKE_BIN2:$PATH" bash "$BOOT" --session "$S" --lanes 2 --agent copilot --unattended --dry-run >/tmp/ad256-out23.$$ 2>&1
check "23. --unattended refuses a harness with no recorded unattended command" "1" "$?"
grep -q "copilot" /tmp/ad256-out23.$$
check "23. the refusal names the harness it does not know" "0" "$?"
rm -f /tmp/ad256-out23.$$

# 24. --unattended cannot be mapped onto a full command line -- inferring an
#     unattended posture for an arbitrary command is exactly the guess #256
#     says must not happen.
PATH="$FAKE_BIN2:$PATH" bash "$BOOT" --session "$S" --lanes 2 --agent "claude --model opus" --unattended --dry-run >/tmp/ad256-out24.$$ 2>&1
check "24. --unattended refuses a full --agent command line" "1" "$?"
grep -q "bare harness name" /tmp/ad256-out24.$$
check "24. the refusal explains a bare harness name is required" "0" "$?"
rm -f /tmp/ad256-out24.$$

rm -rf "$FAKE_BIN2"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
