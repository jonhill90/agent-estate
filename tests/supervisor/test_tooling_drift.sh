#!/bin/bash
# tooling-drift.sh must report, per file, in sync / behind by N commits /
# diverged -- and must never report drift where there is none, or silence
# where there is. agent-supervisor#654 Part 1.
#
# WHY: #654 measured a real four-file gap (dispatch.sh, cli.py, core.py,
# itemize_prompts.py drifted; merge-pr.sh, collision-check.sh,
# branch-sweep.sh, verdict.py did not, in the same checkout) found only by a
# human diffing by hand. This suite mutation-checks the detector both
# directions -- it must go from silent to reporting drift when origin/main
# advances, and back to silent once the clone catches up -- so "this reports
# drift" is proven, not merely "this runs without erroring".
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DRIFT="$HERE/../../scripts/supervisor/tooling-drift.sh"
pass=0; fail=0

ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

echo "tooling-drift.sh"

D=$(mktemp -d)
trap 'rm -rf "$D"' EXIT

git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/src" >/dev/null 2>&1
git -C "$D/src" config user.email test@example.com
git -C "$D/src" config user.name "Test"
git -C "$D/src" checkout -q -b main
mkdir -p "$D/src/scripts/supervisor"
echo "a v1" >"$D/src/scripts/supervisor/a.sh"
echo "b v1" >"$D/src/scripts/supervisor/b.sh"
git -C "$D/src" add -A
git -C "$D/src" commit -q -m "init"
git -C "$D/src" push -q origin main

git clone -q "$D/origin.git" "$D/clone" >/dev/null 2>&1
git -C "$D/clone" config user.email test@example.com
git -C "$D/clone" config user.name "Test"
git -C "$D/clone" checkout -q main

# --- baseline: freshly cloned, must report every file in sync -------------
out=$(TOOLING_DRIFT_NO_FETCH=1 "$DRIFT" "$D/clone" 2>&1)
rc=$?
if [ "$rc" -eq 0 ] && grep -q "scripts/supervisor/a.sh.*in sync" <<<"$out" \
   && grep -q "scripts/supervisor/b.sh.*in sync" <<<"$out"; then
  ok "fresh clone: both files in sync, exit 0"
else
  bad "fresh clone: both files in sync, exit 0" "exit=$rc
$out"
fi

# --- mutation, direction 1: advance origin, confirm drift is REPORTED -----
echo "a v2" >"$D/src/scripts/supervisor/a.sh"
git -C "$D/src" commit -q -am "change a only"
git -C "$D/src" push -q origin main
git -C "$D/clone" fetch -q origin

out=$(TOOLING_DRIFT_NO_FETCH=1 "$DRIFT" "$D/clone" 2>&1)
rc=$?
if [ "$rc" -eq 1 ] && grep -q "scripts/supervisor/a.sh.*behind by 1 commit" <<<"$out"; then
  ok "mutation: a.sh reports behind by 1 commit after origin advances, exit 1"
else
  bad "mutation: a.sh reports behind by 1 commit after origin advances, exit 1" "exit=$rc
$out"
fi
if grep -q "scripts/supervisor/b.sh.*in sync" <<<"$out"; then
  ok "mutation: b.sh (untouched by the commit) still reports in sync -- per-file, not per-directory"
else
  bad "mutation: b.sh (untouched by the commit) still reports in sync -- per-file, not per-directory" "$out"
fi

# --- mutation, direction 2: catch the clone up, confirm silence returns ---
git -C "$D/clone" merge -q --ff-only origin/main
out=$(TOOLING_DRIFT_NO_FETCH=1 "$DRIFT" "$D/clone" 2>&1)
rc=$?
if [ "$rc" -eq 0 ] && grep -q "scripts/supervisor/a.sh.*in sync" <<<"$out"; then
  ok "reverse mutation: fast-forwarding the clone restores in-sync, exit 0"
else
  bad "reverse mutation: fast-forwarding the clone restores in-sync, exit 0" "exit=$rc
$out"
fi

# --- diverged: a local commit the remote does not have --------------------
echo "b local-only" >"$D/clone/scripts/supervisor/b.sh"
git -C "$D/clone" commit -q -am "local-only edit to b, never pushed"
out=$(TOOLING_DRIFT_NO_FETCH=1 "$DRIFT" "$D/clone" 2>&1)
rc=$?
if [ "$rc" -eq 1 ] && grep -q "scripts/supervisor/b.sh.*diverged" <<<"$out" \
   && ! grep -q "scripts/supervisor/b.sh.*behind" <<<"$out"; then
  ok "diverged: a local-only commit is reported diverged, not behind"
else
  bad "diverged: a local-only commit is reported diverged, not behind" "exit=$rc
$out"
fi

# --- uncommitted working-tree edit is also surfaced ------------------------
git -C "$D/clone" reset -q --hard origin/main
echo "a uncommitted" >"$D/clone/scripts/supervisor/a.sh"
out=$(TOOLING_DRIFT_NO_FETCH=1 "$DRIFT" "$D/clone" 2>&1)
rc=$?
if [ "$rc" -eq 1 ] && grep -q "scripts/supervisor/a.sh.*diverged.*uncommitted local edit" <<<"$out"; then
  ok "uncommitted working-tree edit reported diverged (uncommitted local edit)"
else
  bad "uncommitted working-tree edit reported diverged (uncommitted local edit)" "exit=$rc
$out"
fi
git -C "$D/clone" checkout -q -- "scripts/supervisor/a.sh"

# --- explicit file list narrows the check ----------------------------------
out=$(TOOLING_DRIFT_NO_FETCH=1 "$DRIFT" "$D/clone" "scripts/supervisor/b.sh" 2>&1)
if grep -q "b.sh" <<<"$out" && ! grep -q "a.sh" <<<"$out"; then
  ok "explicit file list narrows the check to only the named file"
else
  bad "explicit file list narrows the check to only the named file" "$out"
fi

# --- not a git repo at all: fails closed, exit 2 ----------------------------
out=$("$DRIFT" "$D/not-a-repo" 2>&1)
rc=$?
if [ "$rc" -eq 2 ]; then
  ok "non-repo directory fails closed with exit 2"
else
  bad "non-repo directory fails closed with exit 2" "exit=$rc
$out"
fi

echo
echo "tooling-drift.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
