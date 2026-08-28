#!/bin/bash
# agent-supervisor#682, Track A, ranked item 5. The 4 live launchd jobs
# (director-loop, quota-watch, supervisor-heartbeat, weekly-watch) each
# hardcode the checkout path in ProgramArguments. Unlike every other path
# these scripts touch, that one CANNOT self-resolve at run time the way
# director-loop.sh/quota-watch.sh/heartbeat.sh/weekly-watch.sh already
# resolve their OWN internal paths via `dirname "${BASH_SOURCE[0]}"`:
# launchd execs ProgramArguments directly, with no shell in between, so
# there is no `$VAR` expansion to lean on inside the plist itself.
#
# What this script versions instead, matching #695's shape (a single
# rename-safe resolution point, `$AGENT_SUPERVISOR_REPO` with a loud-fail
# guard, not a second mechanism): a template per plist with the checkout
# path factored out, and one script that resolves $AGENT_SUPERVISOR_REPO
# and renders all 4. A rename becomes "re-run this with the new env var",
# not "hand-edit 4 XML files" -- the runbook's existing `sed -i` (see
# docs/runbooks/agent-estate-migration.md Step 4a #3) still works too; this
# doesn't replace it, it gives the same fix a checked-in, provable form.
#
# This script only RENDERS to --out-dir. It never writes into
# ~/Library/LaunchAgents and never calls launchctl -- installing a rendered
# plist over a live one is a separate, deliberate step (backup the live
# file, diff, copy, unload/load), not something this script does silently.
#
# Usage: render-plists.sh [--out-dir DIR]
#   Prints the resolved checkout path, then the rendered file path for
#   each of the 4 templates in templates/.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATES="$HERE/templates"

OUT_DIR=""
while [ $# -gt 0 ]; do
  case "$1" in
    --out-dir) OUT_DIR="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

# agent-supervisor#682: same convention as scripts/estate-loop/*.sh -- a
# default that keeps today's (pre-rename) behavior byte-identical, an env
# var that wins when a caller sets it, and a loud FATAL (not a quiet
# fallback to "no work") when neither resolves to a real checkout.
AGENT_SUPERVISOR_REPO="${AGENT_SUPERVISOR_REPO:-/Users/jon/source/repos/Personal/agent-supervisor}"
if [ ! -d "$AGENT_SUPERVISOR_REPO" ]; then
  echo "FATAL: AGENT_SUPERVISOR_REPO does not resolve to a real checkout: $AGENT_SUPERVISOR_REPO" >&2
  exit 1
fi

if [ -z "$OUT_DIR" ]; then
  OUT_DIR="$(mktemp -d)"
fi
mkdir -p "$OUT_DIR"

echo "resolved AGENT_SUPERVISOR_REPO: $AGENT_SUPERVISOR_REPO"
for tmpl in "$TEMPLATES"/*.plist.tmpl; do
  name="$(basename "$tmpl" .tmpl)"
  out="$OUT_DIR/$name"
  sed "s#@@AGENT_SUPERVISOR_REPO@@#$AGENT_SUPERVISOR_REPO#g" "$tmpl" > "$out"
  echo "rendered: $out"
done
