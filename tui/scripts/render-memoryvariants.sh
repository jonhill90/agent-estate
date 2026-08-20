#!/usr/bin/env bash
# Renders every tools/memoryvariants static-frame variant to a PNG under
# docs/memoryvariants/, using Charm's own `freeze` (--execute captures a
# real terminal, ANSI colour and all) -- agent-tui#61. See
# tools/memoryvariants/main.go's package doc comment for why this is real
# Bubble Tea/lipgloss output over hardcoded fake data, not an HTML mock,
# same device as scripts/render-uivariants.sh (agent-tui#62/#63).
#
# The live mouse-drag spike (tools/memoryvariants/spike) is NOT rendered
# here -- it needs a real interactive terminal, not a single frozen frame.
# See docs/memoryvariants/README.md's "spike" section for how it was
# actually verified.
#
# Requires: go, freeze (https://github.com/charmbracelet/freeze) on PATH.
set -euo pipefail
cd "$(dirname "$0")/.."

bin="$(mktemp -d)/memoryvariants"
go build -o "$bin" ./tools/memoryvariants

outdir="docs/memoryvariants"
mkdir -p "$outdir"

variants=(
  orbit
  outline
  grid
)

for v in "${variants[@]}"; do
  echo "rendering $v..."
  freeze --execute "$bin $v" \
    --border.radius 8 --border.width 1 \
    --padding 20 --margin 10 \
    -o "$outdir/$v.png"
done

echo "wrote ${#variants[@]} images to $outdir/"
