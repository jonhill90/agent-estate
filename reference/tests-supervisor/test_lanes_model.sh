#!/bin/bash
# agent-supervisor#115: both worker lanes silently ran Opus instead of
# Sonnet and nothing caught it -- a human found it by reading pane footers.
# lanes.sh --json now carries each lane's own `model`, read from the
# harness's self-report on its VISIBLE screen. See harness/claude.sh's own
# HARNESS_MODEL_RE comment for the live evidence this is built from:
# Claude Code's ordinary footer never names the model at all, and the
# issue's own "obvious" read (a bare substring grep for opus/sonnet/haiku
# anywhere on the pane) is UNSAFE -- it fires on prose, not just the
# harness's own self-report (measured live: a window's own conversation-
# title bar reading "Setup Opus orchestrator for..." was the only match
# across nine real panes, and it was a false one). The one genuine
# self-report is the startup-splash line, which is what HARNESS_MODEL_RE
# anchors on; everywhere else reads `unknown`, on purpose.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LANES="$HERE/../../scripts/supervisor/lanes.sh"
pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

D=$(mktemp -d); mkdir -p "$D/bin"
cp "$HERE/stubs/tmux-lanes" "$D/bin/tmux"
cp "$HERE/stubs/ps-lanes" "$D/bin/ps"

# Same fixture shape as test_lanes.sh: index|name|command|pane-text|age|in-mode
# `\n` in the pane-text column becomes a real newline (the splash screen is
# several lines tall), the same mechanism test_lanes.sh already uses for
# w-mentions to put text in scrollback-vs-status-line.
cat > "$D/fixture" <<'FIX'
1|w-splash-sonnet|claude.exe|Welcome back Jon!\n     Sonnet 5 with medium effort · Claude Max ·     \n❯ |1|0
2|w-splash-opus|claude.exe|Welcome back Jon!\n     Opus 5 with high effort · Claude Max ·     \n❯ |1|0
3|w-splash-haiku|claude.exe|Welcome back Jon!\n     Haiku 5 · Claude Max ·     \n❯ |1|0
4|w-busy-no-splash|claude.exe|esc to interrupt 3s|1|0
5|w-title-decoy|claude.exe|─── Setup Opus orchestrator for agent-dotfiles architecture ───\n❯ ready|1|0
6|w-dead|zsh|❯ |1|0
7|w-codex|codex|  gpt-5.5 medium · /repo/path|1|0
FIX

# LANES_SUPERVISOR_WINDOW defaults to 1 -- moved off window 1 here so the
# splash fixtures above are classified as ordinary worker lanes (`free`),
# not folded into the `supervisor` exemption, which is a distinct concern
# tested separately below.
json=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" LANES_SUPERVISOR_WINDOW=99 bash "$LANES" --json 2>&1)

model_for() { python3 -c "
import json,sys
rows=json.load(sys.stdin)
row=next((r for r in rows if r['name']=='$1'), None)
print(row['model'] if row else 'MISSING-ROW')
" <<<"$json"; }

echo "lanes.sh: model detection (#115)"

want_model() { # want_model <label> <lane-name> <expected-model>
  local got
  got=$(model_for "$2")
  if [ "$got" = "$3" ]; then ok "$1"; else bad "$1" "want model='$3', got '$got' in: $json"; fi
}

# --- RED-FIRST: these are the assertions #115's own brief asks for --------
want_model "a lane freshly launched on Sonnet reports sonnet"      w-splash-sonnet  sonnet
want_model "the regression #115 is about: a lane on Opus reports opus" w-splash-opus opus
want_model "a lane on Haiku reports haiku (no launch-effort clause needed)" w-splash-haiku haiku

# --- fail closed, never guess -----------------------------------------------
want_model "a lane mid-conversation with no splash on screen reads unknown, not a guessed default" \
  w-busy-no-splash unknown
# The exact false-positive #115's own naive read (grep for the family name
# anywhere on the pane) would produce: a window whose TITLE happens to say
# "Opus" because that is the TASK's name, not the model. A bare substring
# match would report this "opus"; the real regex must not.
want_model "a title bar merely mentioning a model family is not read as that model" \
  w-title-decoy unknown
want_model "a lane with no agent at all (dead) reads unknown, not a stale guess" \
  w-dead unknown
# codex.sh deliberately leaves HARNESS_MODEL_RE unset (documented gap, not
# wired for #115) -- unknown is the correct, honest default until a real
# capture backs a codex-specific regex, same fail-closed posture as above.
want_model "a codex lane reads unknown -- HARNESS_MODEL_RE is not yet wired for that harness" \
  w-codex unknown

# --- every row carries the field, never omitted -----------------------------
if python3 -c 'import json,sys; rows=json.load(sys.stdin); sys.exit(0 if rows and all("model" in r for r in rows) else 1)' <<<"$json"; then
  ok "every --json row carries a model field, never omitted"
else
  bad "every --json row carries a model field" "$json"
fi

# --- mutation check: prove the assertions above are anchored on the real ---
# regex, not passing for an unrelated reason. Widen HARNESS_MODEL_RE to
# match nothing real (an empty-alternation-proof impossible pattern) and
# confirm the splash-Opus assertion goes red.
MUT_HARNESS_DIR="$D/harness-mut"; mkdir -p "$MUT_HARNESS_DIR"
cp "$HERE/../../scripts/supervisor/harness/"*.sh "$MUT_HARNESS_DIR/"
python3 - "$MUT_HARNESS_DIR/claude.sh" <<'PY'
import re, sys
path = sys.argv[1]
text = open(path).read()
pattern = re.compile(r"^HARNESS_MODEL_RE=.*$", re.M)
found = pattern.findall(text)
assert len(found) == 1, "expected exactly one HARNESS_MODEL_RE= assignment, found %d" % len(found)
# A pattern that cannot match anything real: requires literal text no real
# capture will ever contain.
open(path, "w").write(pattern.sub("HARNESS_MODEL_RE='THIS-PATTERN-MATCHES-NOTHING-REAL-xyz123'", text, count=1))
PY
mut_json=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" LANES_SUPERVISOR_WINDOW=99 \
  LANES_HARNESS_DIR="$MUT_HARNESS_DIR" bash "$LANES" --json 2>&1)
mut_model=$(python3 -c "
import json,sys
rows=json.load(sys.stdin)
row=next((r for r in rows if r['name']=='w-splash-opus'), None)
print(row['model'] if row else 'MISSING-ROW')
" <<<"$mut_json")
if [ "$mut_model" = "unknown" ]; then
  ok "mutation confirmed: breaking HARNESS_MODEL_RE flips w-splash-opus from opus to unknown (the assertion above would be red without the real regex)"
else
  bad "mutation should have broken model detection" "got '$mut_model' in: $mut_json"
fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
