#!/usr/bin/env bash
# Refresh internal/reposcan/testdata/naming_debt_baseline.json from the
# current tree.
#
# Maintenance tool, not part of CI -- internal/reposcan's naming-debt guard
# (TestNamingDebtDoesNotIncreaseOnMain, agent-estate#768 item 4) checks
# live per-file reference counts against a manifest checked into the
# repository, same offline-first shape as
# scripts/tui/refresh-known-references.sh. Run this only after a
# legitimate sweep actually REMOVES hill90-supervisor / hill90-codex-supervisor
# / unqualified agent-supervisor references (agent-estate#768 items 1-2),
# to lower the baseline behind them. Never run it to silence a guard
# failure caused by a reference THIS change just introduced -- that is the
# debt the guard exists to stop; use the inline `naming-guard:historical`
# marker instead for a genuine new historical citation.
#
#   scripts/tui/refresh-naming-debt-baseline.sh
set -euo pipefail

# This script lives at scripts/tui/, same as its sibling
# refresh-known-references.sh; the Go module it reads moved to src/tui/
# (#865), two levels up plus src/.
tui_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../src/tui" && pwd)"
out="${tui_root}/internal/reposcan/testdata/naming_debt_baseline.json"

( cd "${tui_root}" && go run ./cmd/namingdebtbaseline . ) > "${out}"

echo "wrote naming-debt baseline to ${out}"
