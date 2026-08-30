#!/bin/bash
# #110's "make it the standard": a UI change asserted without a captured
# frame is not reviewable. That sentence is only a rule if something checks
# it -- CLAUDE.md's own lesson is that a rule nothing enforces gets
# remembered right up until the night it doesn't. This is the check: given a
# PR, if its diff touches a UI path, it must carry a `<!-- ui-evidence:v1
# -->` marker (in the PR body or a comment) followed by a captured frame --
# the output of `look.py capture` / `look.py frames` (see look.py, #110).
#
# Read-only against GitHub: this never edits the PR. It is meant to run as
# a required CI check (see .github/workflows/ui-evidence.yml) so the gate is
# live at review time, not a convention an agent has to remember to apply
# to itself.
#
# Usage: ui-evidence-gate.sh <pr-number>
#   UI_EVIDENCE_PATH_RE   required. grep -E pattern for "this changed file is
#                         UI" (.github/workflows/ui-evidence.yml sets this to
#                         the viewer directory -- deliberately not defaulted
#                         here: laneview/README.md rule 3 is "no headless
#                         supervisor script names the viewer outside a
#                         comment," and test_laneview_isolation.sh enforces
#                         it by grepping *.sh code for the word. Which paths
#                         count as UI is configuration, not this script's
#                         business to hardcode -- and keeping it out of code
#                         is what keeps this script honestly generic instead
#                         of secretly coupled to one viewer.)
#   UI_EVIDENCE_MARKER    the literal marker to look for
#                         (default: <!-- ui-evidence:v1 -->)
set -uo pipefail

PR="${1:-}"
if [ -z "$PR" ]; then
  echo "usage: ui-evidence-gate.sh <pr-number>" >&2
  exit 2
fi
if [ -z "${UI_EVIDENCE_PATH_RE:-}" ]; then
  echo "ui-evidence-gate: UI_EVIDENCE_PATH_RE is required (see this script's header)" >&2
  exit 2
fi
if ! command -v gh >/dev/null 2>&1; then
  echo "ui-evidence-gate: gh is required on PATH" >&2
  exit 2
fi

PATTERN="$UI_EVIDENCE_PATH_RE"
MARKER="${UI_EVIDENCE_MARKER:-<!-- ui-evidence:v1 -->}"

# `gh pr diff --name-only` goes through the diff endpoint, which GitHub
# refuses outright ("HTTP 406: diff exceeded the maximum number of files
# (300)") on a PR wide enough to carry a full tree migration -- #745
# measured 392 changed paths and the diff endpoint would not return a file
# list at all, not even a truncated one. The paginated files endpoint
# (`GET /pulls/{n}/files`) is a different, unbounded-by-diff-size route
# to the same filename list; `gh api ... --paginate` walks all its pages.
# Verified directly: it returned all 392 paths for #745 where the diff
# endpoint returned nothing, and matched the diff endpoint file-for-file
# on a small control PR (#737, 1 file) -- so this is not a route that
# happens to work large, it is the same list both ways when both can be
# gotten.
changed="$(gh api "repos/{owner}/{repo}/pulls/$PR/files" --paginate -q '.[].filename' 2>&1)" || {
  echo "ui-evidence-gate: gh api pulls/$PR/files failed: $changed" >&2
  exit 2
}

ui_changed="$(grep -E "$PATTERN" <<<"$changed" || true)"
if [ -z "$ui_changed" ]; then
  echo "ui-evidence-gate: no UI paths changed (pattern: $PATTERN) -- nothing to gate"
  exit 0
fi

body="$(gh pr view "$PR" --json body -q .body 2>&1)" || {
  echo "ui-evidence-gate: gh pr view (body) failed: $body" >&2
  exit 2
}
comments="$(gh pr view "$PR" --json comments -q '.comments[].body' 2>&1)" || {
  echo "ui-evidence-gate: gh pr view (comments) failed: $comments" >&2
  exit 2
}

if grep -qF "$MARKER" <<<"$body"$'\n'"$comments"; then
  echo "ui-evidence-gate: UI paths changed and $MARKER found -- evidence present"
  exit 0
fi

cat >&2 <<EOF
ui-evidence-gate: PR #$PR touches a UI path but carries no $MARKER marker.

UI paths changed:
$ui_changed

Capture the pane with look.py (scripts/supervisor/look.py) and post the
result in the PR body or a comment, prefixed with the marker:

  $MARKER
  \$ python3 scripts/supervisor/look.py png -t <target> -o frame.png
  ...attach frame.png...

No headless Chrome on the host? \`look.py capture -t <target> --annotate\`
is the zero-dependency fallback -- text plus per-cell colour instead of a
screenshot.

For a claimed animation, use \`look.py frames\` instead -- a frame sequence,
not a single capture -- so the diff itself is the evidence.
EOF
exit 1
