#!/bin/bash
# resolve-pr-contributors.sh and mark-pr-external.sh must only ever invoke
# `cli.py` subcommands that actually exist.
#
# WHY: agent-supervisor#308's fourth requirement. #331 (resolve-pr-
# contributors.sh's exhaustive chain) was proven correct entirely against
# fixtures and a stubbed `gh` -- every reviewer, including the re-review,
# ran the logic read-only rather than the real script end to end. That left
# a call site invoking a `cli.py` subcommand with the wrong name completely
# invisible: every fixture test that reaches the failing lookup treats the
# non-zero exit exactly like a genuine "cannot resolve" refusal, so the
# suite goes green whether the subcommand exists or was typo'd. Only running
# the real `cli.py --help` against the real call sites catches the gap.
#
# This is a STRING-EXTRACTION test, not a mock: it greps the actual call
# sites out of the actual shell source, so a future edit that adds a sixth
# resolution path (or renames a subcommand) is covered automatically,
# without anyone remembering to update a second, hand-maintained list here.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUPERVISOR="$HERE/../../scripts/supervisor"
CLI="$SUPERVISOR/cli.py"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

echo "resolve-pr-contributors.sh / mark-pr-external.sh: every invoked cli.py subcommand must exist"

# Every `"$ledger_cli" <subcommand>` / `"$LEDGER_CLI" <subcommand>` call site
# in both files, subcommand token only.
subcommands=$(
  grep -hoE '"\$(ledger_cli|LEDGER_CLI)" [a-zA-Z][a-zA-Z0-9-]*' \
    "$SUPERVISOR/resolve-pr-contributors.sh" "$SUPERVISOR/mark-pr-external.sh" \
    | awk '{print $2}' | sort -u
)

if [ -z "$subcommands" ]; then
  bad "found at least one cli.py call site to check" "grep matched nothing -- the extraction pattern itself is broken"
else
  ok "found call sites: $(tr '\n' ' ' <<<"$subcommands")"
fi

for sub in $subcommands; do
  err=$(python3 "$CLI" "$sub" --help 2>&1 >/dev/null)
  rc=$?
  # argparse exits 2 with "invalid choice" on an unknown subcommand; exit 0
  # (or a subcommand-specific arg error, never "invalid choice") means the
  # subcommand itself is real.
  if [ "$rc" -eq 2 ] && grep -qi "invalid choice" <<<"$err"; then
    bad "cli.py recognises subcommand '$sub'" "$err"
  else
    ok "cli.py recognises subcommand '$sub'"
  fi
done

echo "resolve-pr-contributors.sh / mark-pr-external.sh subcommand audit: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
