#!/bin/bash
# S12's actual blocker (docs/SPEC-shell.md, docs/SPEC-agentbox-execution-
# mode.md): nothing in lanes.sh --json could ever POSITIVELY identify a
# container-wrapped lane, so agent-tui's internal/agents.modeFor could only
# ever emit `local` or `unknown`, never `container`. This proves the new
# `execution_mode` column (8th, appended after `model` -- see lanes.sh's own
# execmode_for) reads the `@hill90_lane_execution_mode` tmux pane option
# `bootstrap-session.sh` now RECORDS, exactly the same "written by whatever
# created the lane, never inferred from the pane's command" discipline
# `@hill90_lane_harness` already established for harness identity
# (agent-dotfiles#216).
#
# Three-way mutation check, deliberately -- a test that only proved the
# happy path (a lane marked `local` reads `local`) would have passed on the
# OLD hardcoded-ExecutionLocal shape too, which is the exact defect S12
# exists to remove. All three directions:
#   1. the option says `container`               -> execution_mode=container
#   2. the option says `local`                    -> execution_mode=local
#   3. the option is unset (real tmux's own empty- -> execution_mode=unknown
#      string expansion for an option nobody set)
#
# COULD NOT MEASURE, stated plainly rather than hidden: no real
# container-wrapped lane exists anywhere in this estate to test against --
# agent-tui's docs/SPEC-agentbox-execution-mode.md confirms no dispatch path
# into AgentBox exists today. Arm 1 above is therefore SYNTHETIC: it proves
# the read mechanism (a tmux user option flows through list-panes, into
# lanes.sh, into the JSON) works for the value `container` would have, not
# that a real container lane produces it. Arms 2 and 3 are real: arm 2 is
# exactly what bootstrap-session.sh does today for every lane it creates,
# and arm 3 is exactly what every lane bootstrapped before this change (or
# hand-created outside bootstrap-session.sh) looks like right now.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LANES="$HERE/../../scripts/supervisor/lanes.sh"
pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

D=$(mktemp -d); mkdir -p "$D/bin"
cp "$HERE/stubs/tmux-lanes" "$D/bin/tmux"
cp "$HERE/stubs/ps-lanes" "$D/bin/ps"

# Fixture: index|name|command|pane-text|age|in-mode|box|argv|live-child|cwd|execution_mode
# Columns 7-10 left empty (every pre-existing fixture in this suite already
# does this) -- only the 11th, new column is what this file exercises.
cat > "$D/fixture" <<'FIX'
1|w-container|claude.exe|❯ ready|1|0||||/repo|container
2|w-local|claude.exe|❯ ready|1|0||||/repo|local
3|w-unset|claude.exe|❯ ready|1|0||||/repo|
4|w-garbage|claude.exe|❯ ready|1|0||||/repo|docker
FIX

json=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" LANES_SUPERVISOR_WINDOW=99 bash "$LANES" --json 2>&1)

execmode_for() { python3 -c "
import json,sys
rows=json.load(sys.stdin)
row=next((r for r in rows if r['name']=='$1'), None)
print(row['execution_mode'] if row else 'MISSING-ROW')
" <<<"$json"; }

echo "lanes.sh: execution_mode signal (S12 container signal)"

want_execmode() { # want_execmode <label> <lane-name> <expected>
  local got
  got=$(execmode_for "$2")
  if [ "$got" = "$3" ]; then ok "$1"; else bad "$1" "want execution_mode='$3', got '$got' in: $json"; fi
}

# --- arm 1, SYNTHETIC (stated above): no real container lane exists yet ----
want_execmode "SYNTHETIC: an option positively recorded 'container' reads container" \
  w-container container
# --- arm 2, REAL: exactly what bootstrap-session.sh writes today -----------
want_execmode "REAL: an option positively recorded 'local' (bootstrap-session.sh's own write) reads local" \
  w-local local
# --- arm 3, REAL: exactly what an unmarked lane looks like today -----------
want_execmode "REAL: no option ever set (real tmux's own empty-string expansion) reads unknown, never a guessed default" \
  w-unset unknown
want_execmode "a value neither side has agreed to means unknown, not a silent pass-through" \
  w-garbage unknown

# --- every row carries the field, never omitted -----------------------------
if python3 -c 'import json,sys; rows=json.load(sys.stdin); sys.exit(0 if rows and all("execution_mode" in r for r in rows) else 1)' <<<"$json"; then
  ok "every --json row carries an execution_mode field, never omitted"
else
  bad "every --json row carries an execution_mode field" "$json"
fi

# --- mutation check: prove the assertions above are anchored on the real ---
# option read, not passing for an unrelated reason. Break the stub's own
# pass-through of the 11th fixture column and confirm w-container goes red.
MUT_TMUX="$D/bin/tmux-mut"
sed 's/"\${em:-}"/"BROKEN"/' "$HERE/stubs/tmux-lanes" > "$MUT_TMUX"
chmod +x "$MUT_TMUX"
if ! grep -q '"BROKEN"' "$MUT_TMUX"; then
  bad "mutation setup" "sed did not find the expected line in stubs/tmux-lanes -- mutation target moved"
else
  cp "$MUT_TMUX" "$D/bin/tmux"
  mut_json=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" LANES_SUPERVISOR_WINDOW=99 bash "$LANES" --json 2>&1)
  mut_execmode=$(python3 -c "
import json,sys
rows=json.load(sys.stdin)
row=next((r for r in rows if r['name']=='w-container'), None)
print(row['execution_mode'] if row else 'MISSING-ROW')
" <<<"$mut_json")
  if [ "$mut_execmode" = "unknown" ]; then
    ok "mutation confirmed: breaking the option pass-through flips w-container from container to unknown (the arm-1 assertion above would be red without the real read)"
  else
    bad "mutation should have broken execution_mode detection" "got '$mut_execmode' in: $mut_json"
  fi
  cp "$HERE/stubs/tmux-lanes" "$D/bin/tmux"
fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
