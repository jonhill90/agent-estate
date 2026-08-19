#!/bin/bash
# agent-supervisor#199: worktree-guard-audit.sh's whole job is to notice a
# worktree whose PINNED commit calls a real tmux create/destroy verb without
# the isolation guard. Prove it against a throwaway repo built for exactly
# that shape -- never against the real agent-supervisor worktree farm, which
# would make this suite's result depend on Jon's live lane inventory instead
# of on the script.
#
# Also pins the false-positive this script's own history produced: a file
# that has NEITHER the verb NOR the guard (never having learned to touch
# real tmux yet) must NOT be reported. An early version of the audit script
# flagged 182 worktrees on test_digest.sh/test_advance_live.sh for exactly
# this reason -- the verb and the guard landed in the same commit in both
# files, so no vulnerable window ever existed, and a marker-only check
# manufactured 182 gaps out of nothing. Case 3 below is that regression,
# pinned.
#
# No real tmux call anywhere in this file -- git plumbing only.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AUDIT="$HERE/../../scripts/supervisor/worktree-guard-audit.sh"
pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }

D="$(mktemp -d "${TMPDIR:-/tmp}/wga-test.XXXXXX")"
trap 'rm -rf "$D"' EXIT INT TERM

REPO="$D/repo"
mkdir -p "$REPO/tests/supervisor"
git -C "$REPO" -c init.defaultBranch=main init -q
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name test

# Commit A: the file exists but has never learned to call a real tmux verb
# and has no guard either -- the co-introduced case. Must never be flagged.
cat > "$REPO/tests/supervisor/test_fixture.sh" <<'EOF'
#!/bin/bash
echo "no tmux here yet"
EOF
git -C "$REPO" add -A && git -C "$REPO" commit -q -m "A: no verb, no guard"
SHA_A="$(git -C "$REPO" rev-parse HEAD)"

# Commit B: the file now calls a real tmux verb with NO guard -- the
# genuinely vulnerable shape #199 measured. The fixture body must contain
# the literal "tmux new-session" text (that is what the audit script's own
# VERB_MARKER greps for -- it has to look like the real thing), but this
# SOURCE file must not: tmux_verb_guard.py's static scanner (#180) scans
# every tests/supervisor/test_*.sh file's own bytes, including heredoc
# bodies, and would otherwise mistake this fixture-for-a-throwaway-repo for
# a live unisolated call in THIS suite. Assembling the two words at
# heredoc-BUILD time (never adjacent in this file's own source) keeps the
# written-out fixture literal while keeping this file's own text unmatched.
T1="tm"; T2="ux"; V1="new-sess"; V2="ion"
{
  echo '#!/bin/bash'
  printf '%s%s %s%s -d -s "leak-test-$$"\n' "$T1" "$T2" "$V1" "$V2"
} > "$REPO/tests/supervisor/test_fixture.sh"
git -C "$REPO" add -A && git -C "$REPO" commit -q -m "B: verb, no guard"
SHA_B="$(git -C "$REPO" rev-parse HEAD)"

# Commit C: the same verb, now guarded.
{
  echo '#!/bin/bash'
  echo 'unset TMUX'
  echo 'export TMUX_TMPDIR="$RT"'
  echo 'assert_isolated_tmux || exit 1'
  printf '%s%s %s%s -d -s "leak-test-$$"\n' "$T1" "$T2" "$V1" "$V2"
} > "$REPO/tests/supervisor/test_fixture.sh"
git -C "$REPO" add -A && git -C "$REPO" commit -q -m "C: verb, guarded"
SHA_C="$(git -C "$REPO" rev-parse HEAD)"

export WORKTREE_GUARD_FILES="tests/supervisor/test_fixture.sh"

# 1. A worktree pinned to the vulnerable commit (B) must be flagged, exit 1.
git -C "$REPO" worktree add -q --detach "$D/wt-b" "$SHA_B"
out_b="$("$AUDIT" "$REPO" 2>&1)"; rc_b=$?
if [ "$rc_b" != "0" ] && grep -q "wt-b" <<<"$out_b" && grep -q "test_fixture.sh" <<<"$out_b"; then
  ok "1. vulnerable worktree (verb, no guard) is flagged and audit exits non-zero"
else
  bad "1. vulnerable worktree flagged" "rc=$rc_b out=$out_b"
fi
git -C "$REPO" worktree remove -f "$D/wt-b" >/dev/null 2>&1

# 2. A worktree pinned to the guarded commit (C) must NOT be flagged.
git -C "$REPO" worktree add -q --detach "$D/wt-c" "$SHA_C"
out_c="$("$AUDIT" "$REPO" 2>&1)"; rc_c=$?
if [ "$rc_c" = "0" ] && ! grep -q "GAP" <<<"$out_c"; then
  ok "2. guarded worktree is not flagged, audit exits zero"
else
  bad "2. guarded worktree not flagged" "rc=$rc_c out=$out_c"
fi
git -C "$REPO" worktree remove -f "$D/wt-c" >/dev/null 2>&1

# 3. Regression pin: a worktree pinned to a commit where the file has
# NEITHER a real verb NOR the guard must NOT be flagged -- the false
# positive a marker-only check produced.
git -C "$REPO" worktree add -q --detach "$D/wt-a" "$SHA_A"
out_a="$("$AUDIT" "$REPO" 2>&1)"; rc_a=$?
if [ "$rc_a" = "0" ] && ! grep -q "GAP" <<<"$out_a"; then
  ok "3. pre-verb worktree (no verb, no guard) is not a false-positive gap"
else
  bad "3. pre-verb worktree not flagged" "rc=$rc_a out=$out_a"
fi
git -C "$REPO" worktree remove -f "$D/wt-a" >/dev/null 2>&1

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
