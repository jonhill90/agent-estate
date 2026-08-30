#!/bin/bash
# agent-estate#868: mutation tests for land-check.sh's reverse-apply "did
# this land under squash-merge" instrument. Four cases, matching the
# issue's own list:
#   1. squash-merged content -> landed (clean reverse-apply)
#   2. genuinely unmerged, unique commits -> not-landed
#   3. a conflicting reverse-apply -- content partially overlaps but a
#      LATER commit on base touched the same lines -- must fail loudly
#      (git apply --check -R exits non-zero) and be read as "not-landed",
#      never as permission to say "landed"
#   4. an unresolvable branch/base -> unknown (exit 2), never a guess
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LC="$HERE/../../scripts/supervisor/land-check.sh"
pass=0; fail=0

ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_prefix() {
  # $1=label $2=actual-first-line $3=expected-prefix $4=full-output
  case "$2" in
    "$3"*) ok "$1" ;;
    *) bad "$1" "expected output to start with '$3', got '$2': ${4:-}" ;;
  esac
}

echo "land-check.sh"

D=$(mktemp -d)
REPO="$D/repo"
git init -q "$REPO"
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name "Test"
git -C "$REPO" checkout -q -b main
echo one > "$REPO/file.txt"
echo two > "$REPO/other.txt"
git -C "$REPO" add file.txt other.txt
git -C "$REPO" commit -q -m "initial"
# land-check.sh defaults BASE to origin/main -- give it a real one so the
# default codepath (not just the explicit-base override) is exercised at
# least once below.
git init -q --bare "$D/origin.git"
git -C "$REPO" remote add origin "$D/origin.git"
git -C "$REPO" push -q -u origin main

# --- Case 1: squash-merged -- landed ----------------------------------------
# A branch with several real commits, squash-merged into main (one new
# commit on main carrying the branch's net diff, no ancestry relationship
# to the branch's own commits -- this is the exact shape `git merge-base
# --is-ancestor` cannot see, which is why this instrument exists).
git -C "$REPO" checkout -q -b squash-landed
echo "squash change one" >> "$REPO/file.txt"
git -C "$REPO" commit -q -am "squash work part 1"
echo "squash change two" >> "$REPO/file.txt"
git -C "$REPO" commit -q -am "squash work part 2"
git -C "$REPO" checkout -q main
git -C "$REPO" merge -q --squash squash-landed >/dev/null
git -C "$REPO" commit -q -m "squash-merge squash-landed"
# land-check.sh's default base is origin/main -- push, so the fixture
# actually exercises that default rather than a local-only main the tool
# would never see the same way a real sweep candidate does.
git -C "$REPO" push -q origin main

out=$(bash "$LC" "$REPO" squash-landed origin/main 2>&1); rc=$?
want_exit "case 1: squash-merged branch -- exit code is 0 (landed)" "$rc" 0 "$out"
want_prefix "case 1: squash-merged branch -- output says landed" "$(head -1 <<<"$out")" "landed:" "$out"
if git -C "$REPO" merge-base --is-ancestor squash-landed main 2>/dev/null; then
  bad "sanity: squash-landed is NOT an ancestor of main (if this fails, the fixture is not reproducing squash-merge, and case 1 is not testing the reverse-apply path at all)"
else
  ok "sanity: squash-landed is genuinely not an ancestor of main -- git merge-base --is-ancestor cannot see this landing, only reverse-apply can"
fi

# --- Case 2: genuinely unmerged -- not-landed -------------------------------
git -C "$REPO" checkout -q main
git -C "$REPO" checkout -q -b never-landed
echo "unique, unmerged content" >> "$REPO/other.txt"
git -C "$REPO" commit -q -am "work that never merged anywhere"

out=$(bash "$LC" "$REPO" never-landed origin/main 2>&1); rc=$?
want_exit "case 2: genuinely unmerged branch -- exit code is 1 (not-landed)" "$rc" 1 "$out"
want_prefix "case 2: genuinely unmerged branch -- output says not-landed" "$(head -1 <<<"$out")" "not-landed:" "$out"

# --- Case 3: conflicting reverse-apply (merged, then superseded) -----------
# The branch's change lands on main first; then a LATER commit on main
# touches the exact same lines. The branch's content did reach main once,
# but a byte-for-byte reverse-apply against main's CURRENT tree can no
# longer succeed -- this must fail loudly, not silently report "landed".
git -C "$REPO" checkout -q main
git -C "$REPO" checkout -q -b superseded
echo "superseded change" >> "$REPO/file.txt"
git -C "$REPO" commit -q -am "content that will land, then be edited again"
git -C "$REPO" checkout -q main
git -C "$REPO" merge -q --squash superseded >/dev/null
git -C "$REPO" commit -q -m "squash-merge superseded"
# A later, unrelated commit on main touches the same line the branch touched.
sed -i.bak 's/superseded change/superseded change, edited again/' "$REPO/file.txt" && rm -f "$REPO/file.txt.bak"
git -C "$REPO" commit -q -am "main moves on, touching the same line 'superseded' touched"
git -C "$REPO" push -q origin main

out=$(bash "$LC" "$REPO" superseded origin/main 2>&1); rc=$?
want_exit "case 3: merged-then-superseded -- exit code is 1 (not-landed), never 0" "$rc" 1 "$out"
want_prefix "case 3: merged-then-superseded -- output says not-landed, not landed" "$(head -1 <<<"$out")" "not-landed:" "$out"
if grep -q "git apply --check -R exit" <<<"$out"; then
  ok "case 3: the failure is the loud git-apply conflict, not a silent guess"
else
  bad "case 3: expected the reverse-apply's own git-apply failure to be named in the output" "$out"
fi

# --- Case 4: unresolvable branch -- unknown, never a guess ------------------
out=$(bash "$LC" "$REPO" this-branch-does-not-exist origin/main 2>&1); rc=$?
want_exit "case 4: branch that does not exist -- exit code is 2 (unknown)" "$rc" 2 "$out"
want_prefix "case 4: branch that does not exist -- output says unknown" "$(head -1 <<<"$out")" "unknown:" "$out"

# --- Case 4b: unresolvable base -- unknown, never a guess -------------------
out=$(bash "$LC" "$REPO" never-landed refs/heads/this-base-does-not-exist 2>&1); rc=$?
want_exit "case 4b: base that does not resolve -- exit code is 2 (unknown)" "$rc" 2 "$out"
want_prefix "case 4b: base that does not resolve -- output says unknown" "$(head -1 <<<"$out")" "unknown:" "$out"

# --- Case 5: an arbitrary non-refs/heads ref-ish (e.g. refs/rescued/*) -----
# resolves too, not just refs/heads/<name> -- exercised because agent-
# estate#868 asks this instrument be pointed at refs/rescued/* directly.
# A fresh file, squash-merged fresh off the CURRENT main tip -- cases 1 and
# 3 above already mutated file.txt repeatedly, so reusing that history here
# would make this case's own reverse-apply context collide with THEIR
# edits, not the thing case 5 is actually testing (ref resolution).
git -C "$REPO" checkout -q main
git -C "$REPO" checkout -q -b rescued-source
echo "rescued content" > "$REPO/case5.txt"
git -C "$REPO" add case5.txt
git -C "$REPO" commit -q -m "case 5 source work"
git -C "$REPO" update-ref refs/rescued/case5 rescued-source
git -C "$REPO" checkout -q main
git -C "$REPO" merge -q --squash rescued-source >/dev/null
git -C "$REPO" commit -q -m "squash-merge rescued-source"
git -C "$REPO" push -q origin main

out=$(bash "$LC" "$REPO" refs/rescued/case5 origin/main 2>&1); rc=$?
want_exit "case 5: a non-refs/heads ref-ish (refs/rescued/*) resolves -- exit 0" "$rc" 0 "$out"
want_prefix "case 5: refs/rescued/* -- output says landed" "$(head -1 <<<"$out")" "landed:" "$out"

# --- Never touches the repo's own real index/working tree ------------------
# land-check.sh must be safe to run against a dirty tree without disturbing
# it -- it works entirely through a scratch GIT_INDEX_FILE.
echo "operator's own uncommitted work" >> "$REPO/file.txt"
before_status=$(git -C "$REPO" status --porcelain)
bash "$LC" "$REPO" squash-landed origin/main >/dev/null 2>&1
after_status=$(git -C "$REPO" status --porcelain)
if [ "$before_status" = "$after_status" ]; then
  ok "land-check.sh never touches the caller's own working tree or index"
else
  bad "land-check.sh never touches the caller's own working tree or index" "status before: '$before_status' status after: '$after_status'"
fi

rm -rf "$D"

echo
echo "land-check.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
