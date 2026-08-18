#!/usr/bin/env bash
# Renders every tools/uivariants variant to a PNG under docs/uivariants/,
# using Charm's own `freeze` (--execute captures a real terminal, ANSI
# colour and all) -- agent-tui#62. See tools/uivariants/main.go's package
# doc comment for why this is real Bubble Tea/lipgloss output over
# hardcoded fake state, not an HTML mock.
#
# Requires: go, freeze (https://github.com/charmbracelet/freeze) on PATH.
set -euo pipefail
cd "$(dirname "$0")/.."

bin="$(mktemp -d)/uivariants"
go build -o "$bin" ./tools/uivariants

outdir="docs/uivariants"
mkdir -p "$outdir"

variants=(
  rail-thin
  rail-roomy
  rail-collapsed
  board-boxed-rules
  board-card-boxes
  board-chip
)

for v in "${variants[@]}"; do
  echo "rendering $v..."
  freeze --execute "$bin $v" \
    --border.radius 8 --border.width 1 \
    --padding 20 --margin 10 \
    -o "$outdir/$v.png"
done

echo "wrote ${#variants[@]} images to $outdir/"
