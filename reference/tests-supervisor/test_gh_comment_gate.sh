#!/bin/bash
# agent-supervisor#188: post-verdict.sh (#170/#176/#187) is meant to be the
# ONE path this estate uses to post a PR/issue comment carrying a
# Verdict:/Review-Lane: pair. This suite locks down the static gate
# (scripts/supervisor/gh-comment-gate.sh) that keeps a raw `gh pr comment`
# / `gh issue comment` invocation from creeping back in unnoticed.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATE="$HERE/../../scripts/supervisor/gh-comment-gate.sh"
pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 -- $2"; fail=$((fail+1)); }

echo "gh-comment-gate.sh"

# =========================================================================
# 1. GREEN: the real scripts/supervisor/ tree passes today -- the one real
#    invocation outside post-verdict.sh (acceptance.sh's reopen note) is
#    explicitly grandfathered by its exact line.
# =========================================================================
if out=$(bash "$GATE" "$HERE/../../scripts/supervisor" 2>&1); then
  ok "the real scripts/supervisor/ tree passes the gate"
else
  bad "the real scripts/supervisor/ tree passes the gate" "$out"
fi

D=$(mktemp -d)

# =========================================================================
# 2. RED: a fresh raw `gh pr comment` invocation outside post-verdict.sh is
#    caught.
# =========================================================================
mkdir -p "$D/new"
cat > "$D/new/some-script.sh" <<'EOF'
#!/bin/bash
gh pr comment "$1" --repo "$2" --body "$3"
EOF
rc=0
out=$(bash "$GATE" "$D/new" 2>&1) || rc=$?
if [ "$rc" -ne 0 ]; then ok "a fresh gh pr comment invocation is refused (got $rc)"; else bad "a fresh gh pr comment invocation was not caught" "$out"; fi
if grep -q "some-script.sh" <<<"$out"; then ok "the refusal names the offending file"; else bad "the refusal does not name the offending file" "$out"; fi

# =========================================================================
# 3. RED: `gh issue comment` is caught too, not just the PR form.
# =========================================================================
mkdir -p "$D/issuecomment"
cat > "$D/issuecomment/other-script.sh" <<'EOF'
#!/bin/bash
gh issue comment "$1" --repo "$2" --body "$3"
EOF
rc=0
out=$(bash "$GATE" "$D/issuecomment" 2>&1) || rc=$?
if [ "$rc" -ne 0 ]; then ok "a fresh gh issue comment invocation is refused (got $rc)"; else bad "a fresh gh issue comment invocation was not caught" "$out"; fi

# =========================================================================
# 4. GREEN: a COMMENT merely mentioning "gh pr comment" (prose, not code)
#    is not flagged -- the rule is about invocation, not the word.
# =========================================================================
mkdir -p "$D/comment-only"
cat > "$D/comment-only/prose.sh" <<'EOF'
#!/bin/bash
# This script deliberately does not call `gh pr comment` directly.
echo hi
EOF
if out=$(bash "$GATE" "$D/comment-only" 2>&1); then
  ok "a comment merely mentioning the shape is not flagged"
else
  bad "a comment merely mentioning the shape is not flagged" "$out"
fi

# =========================================================================
# 5. GREEN: post-verdict.sh itself is exempt (it IS the path).
# =========================================================================
mkdir -p "$D/self"
cp "$HERE/../../scripts/supervisor/post-verdict.sh" "$D/self/post-verdict.sh"
if out=$(bash "$GATE" "$D/self" 2>&1); then
  ok "post-verdict.sh itself is exempt from its own gate"
else
  bad "post-verdict.sh itself is exempt from its own gate" "$out"
fi

# =========================================================================
# 6. RED: an EDITED grandfathered line no longer matches, and must be
#    caught again -- the grandfather is keyed on exact text, not the file.
# =========================================================================
mkdir -p "$D/edited"
cat > "$D/edited/acceptance.sh" <<'EOF'
#!/bin/bash
gh issue comment "$num" --repo "$REPO" --body "a different note entirely" >/dev/null 2>&1
EOF
rc=0
out=$(bash "$GATE" "$D/edited" 2>&1) || rc=$?
if [ "$rc" -ne 0 ]; then ok "an edited grandfathered line is caught again"; else bad "an edited grandfathered line was wrongly still exempt" "$out"; fi

rm -rf "$D"

echo
echo "gh-comment-gate.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
