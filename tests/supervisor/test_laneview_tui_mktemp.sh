#!/bin/bash
# agent-supervisor#356: tui.sh built its scratch python file with
# `mktemp ".../laneview-tui.XXXXXX.py"` -- a ".py" suffix trailing the
# XXXXXX template. On BSD mktemp (macOS, what laneview-tui actually leaked
# on) that is not an error, it is a SILENT no-op: mktemp exits 0 and prints
# the literal, unexpanded path back, with "XXXXXX" never replaced. Every
# invocation of tui.sh then raced for the exact same path instead of a
# unique one -- the two orphaned processes #356 found were still holding
# that literal filename three days later.
#
# This test does not re-implement mktemp's substitution rules (platform-
# specific and not this suite's job to model) or hand-copy tui.sh's
# template into a second literal string that could silently drift from the
# real one. It extracts the ACTUAL template tui.sh passes to mktemp and
# feeds that exact string to the real `mktemp` binary, live -- the same
# call tui.sh itself makes -- so a regression in either direction (the
# fix regresses, or a future edit reintroduces a trailing suffix) is
# caught the same way #356 was found: by looking at the real path mktemp
# hands back, not by asserting the source reads a particular way.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TUI="$HERE/../../scripts/supervisor/laneview/tui.sh"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 -- $2"; fail=$((fail+1)); }

echo "laneview tui.sh: mktemp template must actually expand (agent-supervisor#356)"

if [ ! -f "$TUI" ]; then
  echo "  SKIP no tui.sh -- viewer implementation not installed"; exit 0
fi

# Pull the exact template string out of tui.sh's own mktemp call, e.g.
# "${TMPDIR:-/tmp}/laneview-tui.XXXXXX" -- never a second, hand-typed copy.
template_expr=$(grep -o 'mktemp "[^"]*"' "$TUI" | head -1 | sed 's/^mktemp "//; s/"$//')
if [ -z "$template_expr" ]; then
  bad "tui.sh's mktemp call could be located" "no 'mktemp \"...\"' line found in $TUI"
  echo "  $pass passed, $fail failed"
  exit 1
fi
ok "located tui.sh's own mktemp template: $template_expr"

RT="$(mktemp -d "${TMPDIR:-/tmp}/lv-tui-mktemp.XXXXXX")"
trap 'rm -rf "$RT"' EXIT
# TMPDIR governs tui.sh's own "${TMPDIR:-/tmp}" fallback -- point it at an
# isolated scratch dir so this test never touches the real /tmp population.
template=$(TMPDIR="$RT" bash -c "echo $template_expr")

created="$(mktemp "$template" 2>&1)"; rc=$?
if [ "$rc" -ne 0 ]; then
  bad "mktemp accepted tui.sh's template" "rc=$rc output=$created"
else
  case "$created" in
    *XXXXXX*)
      bad "mktemp actually substituted the XXXXXX placeholder" \
        "got literal unexpanded path back: $created"
      ;;
    *)
      ok "mktemp substituted a real random path, not the literal template: $created"
      ;;
  esac
fi
rm -f "$created" 2>/dev/null

# The consequence that made this a real leak, not just a naming nit: two
# concurrent invocations must not collide on the SAME path. Run the exact
# template twice, back to back, the way two concurrent tui.sh launches
# would (agent-supervisor#356 cites a live "mkstemp failed ... File
# exists" from exactly this race).
first="$(mktemp "$template" 2>&1)"; rc1=$?
second="$(mktemp "$template" 2>&1)"; rc2=$?
if [ "$rc1" -eq 0 ] && [ "$rc2" -eq 0 ] && [ "$first" != "$second" ]; then
  ok "two concurrent invocations get two distinct paths, no collision"
else
  bad "two concurrent invocations get two distinct paths, no collision" \
    "rc1=$rc1 rc2=$rc2 first=$first second=$second"
fi
rm -f "$first" "$second" 2>/dev/null

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
