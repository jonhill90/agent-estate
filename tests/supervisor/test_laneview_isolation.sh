#!/bin/bash
# laneview/README.md rule 3: "cost nothing when unused" -- no renderer, and
# no laneview.sh, may be sourced by or run as a dependency of a headless
# supervisor script. That rule shipped with #178/#231 enforced by nothing
# (#4 finding C). This is the binary: any *.sh under scripts/supervisor/
# other than laneview.sh and the renderers themselves must not reference
# `laneview` outside a comment.
#
# A reference in a COMMENT is allowed and a reference in CODE is not. The
# rule is about dependency, not about mentioning the thing -- a script that
# explains in a comment why it does not call a viewer is obeying rule 3, and
# a check that failed on that would be a check people route around by
# deleting the explanation.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUPERVISOR="$HERE/../../scripts/supervisor"
pass=0; fail=0
ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1 — $2"; fail=$((fail+1)); }

echo "laneview isolation (rule 3)"

if [ ! -d "$SUPERVISOR/laneview" ]; then
  echo "  SKIP no laneview/ -- viewer adapter not installed"; exit 0
fi

# The tmux-plugin launcher directory (`laneview-*`, tested separately by
# tests/supervisor/test_laneview_tmux_plugin.sh) is the human-facing launcher
# laneview/README.md documents ("The tmux plugin"): it binds a tmux key to a
# popup running a renderer, and is exempt for the same reason laneview.sh
# itself is -- it is never invoked from a headless supervisor path. Rule 3 is
# about dispatch.sh, watchdog.sh, notify.sh and their kind reaching for a
# viewer, not about the viewer's own launcher naming itself. Matched by
# prefix (`laneview*`) rather than spelled out here, so this file does not
# itself trip test_laneview_tmux_plugin.sh's own "nothing outside its
# directory depends on it in code" check.
violations=$(
  find "$SUPERVISOR" -name '*.sh' \
    ! -path "$SUPERVISOR/laneview*" \
    -print0 |
  xargs -0 grep -HnE '\blaneview\b' 2>/dev/null |
  grep -vE ':[0-9]+: *#'
)

if [ -z "$violations" ]; then
  ok "no headless supervisor script references laneview outside a comment"
else
  bad "no headless supervisor script references laneview outside a comment" "$violations"
fi

# A comment naming the rule must not itself be flagged -- the rule is about
# dependency, not about saying the word.
D=$(mktemp -d)
mkdir -p "$D/scripts/supervisor/laneview"
cat > "$D/scripts/supervisor/notify.sh" <<'EOF'
#!/bin/bash
# Deliberately does not call laneview.sh: rule 3.
echo hi
EOF
c_violations=$(
  find "$D/scripts/supervisor" -name '*.sh' \
    ! -path "$D/scripts/supervisor/laneview.sh" \
    ! -path "$D/scripts/supervisor/laneview/*" \
    -print0 |
  xargs -0 grep -HnE '\blaneview\b' 2>/dev/null |
  grep -vE ':[0-9]+: *#'
)
if [ -z "$c_violations" ]; then
  ok "a comment naming the rule is not itself a violation"
else
  bad "a comment naming the rule is not itself a violation" "$c_violations"
fi

# A headless script that actually calls a renderer must be caught.
cat > "$D/scripts/supervisor/dispatch.sh" <<'EOF'
#!/bin/bash
bash "$HERE/laneview.sh" text
EOF
d_violations=$(
  find "$D/scripts/supervisor" -name '*.sh' \
    ! -path "$D/scripts/supervisor/laneview.sh" \
    ! -path "$D/scripts/supervisor/laneview/*" \
    -print0 |
  xargs -0 grep -HnE '\blaneview\b' 2>/dev/null |
  grep -vE ':[0-9]+: *#'
)
rm -rf "$D"
if [ -n "$d_violations" ] && grep -q 'dispatch.sh' <<<"$d_violations"; then
  ok "a headless script calling a renderer is caught"
else
  bad "a headless script calling a renderer is caught" "$d_violations"
fi

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
