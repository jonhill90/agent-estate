#!/bin/bash
# agent-supervisor#188: post-verdict.sh is meant to be the ONE path in this
# estate that posts a PR/issue comment carrying a Verdict:/Review-Lane: pair
# -- #176 hardened it (argument-shaped-body refusal, read-back
# verification) and #187 taught it to validate that pair against the
# ledger before posting. Neither defense means anything if a caller can
# still reach straight for `gh pr comment` / `gh issue comment` and walk
# around both -- #188's own finding was `git grep -n post-verdict` matching
# only the script and its own test: nothing called it.
#
# This is a static check, not a live-PR one (contrast
# ui-evidence-gate.sh, which reads a real PR's diff/comments): it greps the
# committed *.sh sources under scripts/supervisor/ for a direct `gh pr
# comment` / `gh issue comment` invocation in CODE (never a comment line),
# excluding post-verdict.sh itself.
#
# #188 offered two acceptable closes: wire post-verdict.sh into a real
# code seam, or -- if posting stays agent-driven for good reason -- make
# that explicit and add a lint/CI check that makes a raw `gh pr comment` /
# `gh issue comment` outside post-verdict.sh harder, with existing
# occurrences grandfathered explicitly. This is that check: nothing in this
# repo's OWN committed scripts posts a verdict via the raw path today
# (`acceptance.sh`'s reopen note is the one pre-existing exception, and it
# is not a Verdict:/Review-Lane: post at all -- grandfathered below by its
# exact line, not by file, so an edit to that line has to re-earn the
# exemption on purpose); brief/prompt generation for review and fix-pass
# dispatches lives outside this repo, in the operator's own state
# directory, and is out of this gate's reach.
#
# Usage: gh-comment-gate.sh [scripts-dir]  (default: this script's own dir)
set -uo pipefail

SUPERVISOR="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}"

# file|||exact matched line, trimmed of leading/trailing whitespace. Keyed
# on the LINE, not just the file: if this line ever changes, the grandfather
# stops silently covering whatever replaced it.
GRANDFATHERED=(
  "$SUPERVISOR/acceptance.sh|||&& gh issue comment \"\$num\" --repo \"\$REPO\" --body \"\$note\" >/dev/null 2>&1; then"
)

violations=$(
  find "$SUPERVISOR" -name '*.sh' ! -name 'post-verdict.sh' ! -name 'gh-comment-gate.sh' -print0 |
  xargs -0 grep -HnE '(\bgh\b|\$GH\b)[^#]*\b(pr|issue)[[:space:]]+comment\b' 2>/dev/null |
  grep -vE ':[0-9]+: *#'
)

filtered=""
while IFS= read -r line; do
  [ -z "$line" ] && continue
  file="${line%%:*}"
  rest="${line#*:}"
  text="${rest#*:}"
  trimmed="$(sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' <<<"$text")"
  skip=0
  for g in "${GRANDFATHERED[@]}"; do
    gfile="${g%%|||*}"
    gtext="${g#*|||}"
    if [ "$file" = "$gfile" ] && [ "$trimmed" = "$gtext" ]; then
      skip=1
      break
    fi
  done
  [ "$skip" -eq 1 ] || filtered="$filtered$line"$'\n'
done <<<"$violations"
filtered="${filtered%$'\n'}"

if [ -z "$filtered" ]; then
  echo "gh-comment-gate: no un-grandfathered gh pr/issue comment invocation outside post-verdict.sh"
  exit 0
fi

cat >&2 <<EOF
gh-comment-gate: found a direct gh pr/issue comment invocation outside post-verdict.sh (agent-supervisor#188):

$filtered

post-verdict.sh is the one hardened path for this class of write: it
refuses argument-shaped bodies, verifies via read-back, and validates a
Verdict:/Review-Lane: pairing against the ledger before posting (#170/
#187). Route this through it instead:

  printf '%s\n' "\$BODY" | scripts/supervisor/post-verdict.sh <repo> <number> [--issue]

If this occurrence is deliberate and reviewed, add its exact line to
GRANDFATHERED in this script, with a comment explaining why it stays
outside post-verdict.sh.
EOF
exit 1
