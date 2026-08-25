#!/bin/bash
# advance-live.sh must refuse rather than force when $LIVE cannot fast-forward
# cleanly to origin/main -- agent-supervisor#654 Part 2's own bar: "If the
# dedicated clone can't fast-forward cleanly against origin/main (a
# conflict, local changes that shouldn't exist there in the first place),
# refuse and report loudly -- never force, never silently diverge."
#
# WHY A SEPARATE FILE FROM test_advance_live.sh: that suite's dirty-guard
# coverage is for an uncommitted working-tree edit (`git status --porcelain`
# catches it). This guard is for the case that leaves a CLEAN working tree
# but an unmergeable HEAD -- a local commit in $LIVE that origin/main's
# history does not contain. `git status --porcelain` reports nothing for
# that; only `git merge-base --is-ancestor` does. Mutation-checked both
# directions per the issue's own verification bar: introduce the divergence,
# confirm refusal and a clear report; remove it, confirm the fast-forward
# succeeds.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ADVANCE="$HERE/../../scripts/supervisor/advance-live.sh"
pass=0; fail=0

ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

echo "advance-live.sh: agent-supervisor#654 -- fast-forward-only guard"

D=$(mktemp -d)
trap 'rm -rf "$D"' EXIT

git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/src"
SRC="$D/src"
git -C "$SRC" config user.email test@example.com
git -C "$SRC" config user.name "Test"
git -C "$SRC" checkout -q -b main
mkdir -p "$SRC/scripts/supervisor"
cat >"$SRC/scripts/supervisor/watchdog.sh" <<'EOF'
#!/bin/bash
set -uo pipefail
STATUS="${SUPERVISOR_STATUS:?}"
mkdir -p "$(dirname "$STATUS")"
{
  printf 'checked:  %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'state:    pane_unreadable\n'
} >"$STATUS"
exit 0
EOF
chmod +x "$SRC/scripts/supervisor/watchdog.sh"
git -C "$SRC" add -A
git -C "$SRC" commit -q -m "good watchdog.sh"
git -C "$SRC" push -q -u origin main

LIVE="$D/live"
git -C "$SRC" worktree add -q --detach "$LIVE" origin/main
BASE_SHA=$(git -C "$LIVE" rev-parse HEAD)

fresh_status() { # fresh_status <state-dir>
  mkdir -p "$1"
  printf 'checked:  %s\nstate:    working\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$1/watchdog.status"
}
run() { # run <state-dir>
  SUPERVISOR_STATE="$1" bash "$ADVANCE" "$LIVE"
}

# --- baseline: ordinary advance still works (no divergence introduced) ----
echo "# ordinary commit" >"$SRC/README.md"
git -C "$SRC" add -A
git -C "$SRC" commit -q -m "ordinary upstream change"
git -C "$SRC" push -q origin main

S=$(mktemp -d)
fresh_status "$S"
out=$(run "$S" 2>&1); rc=$?
if [ "$rc" -eq 0 ] && grep -q '^advance-live: advanced' <<<"$out"; then
  ok "ordinary fast-forward (no divergence): advances cleanly"
else
  bad "ordinary fast-forward (no divergence): advances cleanly" "exit=$rc
$out"
fi
newsha=$(git -C "$LIVE" rev-parse HEAD)
[ "$newsha" = "$(git -C "$SRC" rev-parse main)" ] && ok "live HEAD matches origin/main after the ordinary advance" \
  || bad "live HEAD matches origin/main after the ordinary advance" "live=$newsha origin=$(git -C "$SRC" rev-parse main)"

# --- mutation, direction 1: introduce a divergence, confirm refusal -------
# A commit made DIRECTLY in $LIVE, never pushed anywhere -- exactly the
# "something wrote to a clone that should have exactly one writer" case the
# guard exists to catch. The working tree is clean (git status --porcelain
# reports nothing), so only the ancestry check can catch this.
git -C "$LIVE" config user.email test@example.com
git -C "$LIVE" config user.name "Test"
echo "rogue" >"$LIVE/rogue.txt"
git -C "$LIVE" add -A
git -C "$LIVE" commit -q -m "a commit that should never exist in a loop-owned clone"
DIVERGED_SHA=$(git -C "$LIVE" rev-parse HEAD)

# Advance origin again, so there is genuinely something to advance to.
echo "# second ordinary commit" >>"$SRC/README.md"
git -C "$SRC" add -A
git -C "$SRC" commit -q -m "second ordinary upstream change"
git -C "$SRC" push -q origin main

S2=$(mktemp -d)
fresh_status "$S2"
out=$(run "$S2" 2>&1); rc=$?
if [ "$rc" -ne 0 ] && grep -qi "not an ancestor" <<<"$out" && grep -qi "cannot fast-forward" <<<"$out"; then
  ok "mutation: a rogue local commit refuses the advance, names the cause clearly"
else
  bad "mutation: a rogue local commit refuses the advance, names the cause clearly" "exit=$rc
$out"
fi
stillsha=$(git -C "$LIVE" rev-parse HEAD)
if [ "$stillsha" = "$DIVERGED_SHA" ]; then
  ok "refused advance leaves live worktree exactly where it was (no force, no partial checkout)"
else
  bad "refused advance leaves live worktree exactly where it was (no force, no partial checkout)" "expected $DIVERGED_SHA, got $stillsha"
fi

# --- mutation, direction 2: remove the divergence, confirm success again --
# Reset live back onto origin/main's ancestry (what an operator would do by
# hand to recover -- discard the rogue commit, land back on a real ancestor)
# and confirm the SAME advance now succeeds.
git -C "$LIVE" reset -q --hard "$BASE_SHA"
S3=$(mktemp -d)
fresh_status "$S3"
out=$(run "$S3" 2>&1); rc=$?
if [ "$rc" -eq 0 ] && grep -q '^advance-live: advanced' <<<"$out"; then
  ok "reverse mutation: once live is back on origin/main's ancestry, the advance succeeds again"
else
  bad "reverse mutation: once live is back on origin/main's ancestry, the advance succeeds again" "exit=$rc
$out"
fi
finalsha=$(git -C "$LIVE" rev-parse HEAD)
[ "$finalsha" = "$(git -C "$SRC" rev-parse main)" ] && ok "live HEAD matches origin/main after recovery" \
  || bad "live HEAD matches origin/main after recovery" "live=$finalsha origin=$(git -C "$SRC" rev-parse main)"

echo
echo "advance-live.sh ff-only guard: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
