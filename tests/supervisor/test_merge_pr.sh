#!/bin/bash
# merge-pr.sh must refuse to call `gh pr merge` when ci_gate.py refuses, and
# must call it when the gate allows -- the whole point of agent-supervisor#13
# is that the refusal is a script a caller cannot skip by habit, not a
# paragraph someone has to remember to consult.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MERGE_PR="$HERE/../../scripts/supervisor/merge-pr.sh"
pass=0; fail=0

ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

echo "merge-pr.sh"

D=$(mktemp -d)
BIN="$D/bin"
mkdir -p "$BIN"
MARKER="$D/merged"

# A fake `gh` that answers exactly the calls ci_gate.py and merge-pr.sh make.
# HEAD_SHA / CHECK_CONCLUSION / CHECK_SHA are read from env at call time so
# each scenario below can flip them without rewriting the script.
cat > "$BIN/gh" <<'FAKE'
#!/bin/bash
set -uo pipefail
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  echo "{\"headRefOid\": \"$HEAD_SHA\"}"
  exit 0
fi
if [ "$1" = "pr" ] && [ "$2" = "merge" ]; then
  echo "merged" > "$MARKER"
  exit 0
fi
if [ "$1" = "api" ]; then
  case "$2" in
    */check-runs)
      echo "[{\"name\": \"test\", \"head_sha\": \"$CHECK_SHA\", \"status\": \"completed\", \"conclusion\": \"$CHECK_CONCLUSION\"}]"
      ;;
    */status)
      echo '{"statuses": []}'
      ;;
    *) echo "fake gh: unexpected api path: $2" >&2; exit 1 ;;
  esac
  exit 0
fi
echo "fake gh: unexpected command: $*" >&2
exit 1
FAKE
chmod +x "$BIN/gh"
export PATH="$BIN:$PATH"
export MARKER

# --- failing check: refuses, never calls `gh pr merge` --------------------
rm -f "$MARKER"
export HEAD_SHA="sha-1" CHECK_SHA="sha-1" CHECK_CONCLUSION="failure"
out=$("$MERGE_PR" o/r 42 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "failing check exits 1"; else bad "failing check exits 1" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "failing check never merges"; else bad "failing check never merges" "$out"; fi
echo "$out" | grep -q "sha-1" && ok "refusal names the sha" || bad "refusal names the sha" "$out"

# --- green check for the exact head sha: merges ----------------------------
rm -f "$MARKER"
export HEAD_SHA="sha-2" CHECK_SHA="sha-2" CHECK_CONCLUSION="success"
out=$("$MERGE_PR" o/r 42 2>&1)
rc=$?
if [ "$rc" -eq 0 ]; then ok "green check exits 0"; else bad "green check exits 0" "got rc=$rc: $out"; fi
if [ -f "$MARKER" ]; then ok "green check merges"; else bad "green check merges" "$out"; fi

# --- green check for an OLDER sha than head: refuses, never merges --------
rm -f "$MARKER"
export HEAD_SHA="sha-4-new" CHECK_SHA="sha-3-old" CHECK_CONCLUSION="success"
out=$("$MERGE_PR" o/r 42 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "stale-sha check exits 1"; else bad "stale-sha check exits 1" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "stale-sha check never merges"; else bad "stale-sha check never merges" "$out"; fi

rm -rf "$D"

echo "  -> $pass ok, $fail failed"
[ "$fail" -eq 0 ]
