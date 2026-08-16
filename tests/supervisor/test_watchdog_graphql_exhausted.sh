#!/bin/bash
# agent-supervisor#144 MUTATION CHECK, both directions -- watchdog.sh's idle
# work-count loop, which runs on every tick for every repo, is REST core now
# (`gh api repos/.../issues`, `.../pulls`), not GraphQL (`gh issue list`,
# `gh pr list`). Direction 1: GraphQL exhausted, REST alive -- the loop must
# still count real work, not fall back to the "GitHub unreachable" degraded
# path. Direction 2: REST also unreachable -- the degraded path must still
# fire (a failed query is NOT zero, finding 1 in the file's own history).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCHDOG="$HERE/../../scripts/supervisor/watchdog.sh"
# agent-supervisor#199/#205: this file hands watchdog.sh a fresh
# SUPERVISOR_STATE per case, so check_worktree_guard_audit's own throttle
# (a stamp file under that state dir) never has a prior run to find and
# would run the real worktree-guard-audit.sh -- against whatever repo this
# worktree happens to be checked out in -- on every tick. That check has
# its own dedicated test (test_watchdog_worktree_guard_audit.sh); this file
# is about something else, so disable it here the same way that test
# disables the checks it isn't about.
export SUPERVISOR_GUARD_AUDIT_INTERVAL=99999999999
STUBS="$HERE/stubs"
pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

run() { # run <bindir> <workdir>
  rm -rf "$2"; mkdir -p "$2" "$2/transcripts"
  SUPERVISOR_PATH="$1:$STUBS:/usr/bin:/bin" STUB_PANE_STATE=idle STUB_SENT="$2/sent" \
  SUPERVISOR_STATE="$2" SUPERVISOR_STATUS="$2/st" SUPERVISOR_LOG="$2/lg" \
  SUPERVISOR_STAMP="$2/stamp" SUPERVISOR_HISTORY="$2/hist" NOTIFY_ENV="$2/none.env" \
  SLEEPCHECK_DIR="$2/transcripts" SUPERVISOR_REPOS=one-repo \
  bash "$WATCHDOG" >/dev/null 2>"$2/err"
}

echo "watchdog.sh -- REST-vs-GraphQL mutation check (#144)"

# --- direction 1: GraphQL exhausted, REST alive -----------------------------
B1=$(mktemp -d); cp "$STUBS/gh-watchdog-graphql-exhausted" "$B1/gh"
D1=$(mktemp -d); run "$B1" "$D1"
if grep -q "state:    restarted" "$D1/st"; then
  ok "a real (non-degraded) REST work count still restarts an idle pane"
else
  bad "restart on a genuine REST work count" "$(cat "$D1/st" 2>/dev/null)"
fi
if grep -q "DEGRADED" "$D1/lg" 2>/dev/null; then
  bad "GraphQL exhausted should not fall back to the degraded path (REST answered)" "$(cat "$D1/lg")"
else
  ok "no DEGRADED fallback -- REST answered on its own budget"
fi

# --- direction 2: REST also unreachable -------------------------------------
B2=$(mktemp -d)
printf '#!/bin/bash\nexit 1\n' > "$B2/gh"; chmod +x "$B2/gh"
D2=$(mktemp -d); run "$B2" "$D2"
if grep -q "DEGRADED" "$D2/lg" 2>/dev/null; then
  ok "REST also unreachable -- the degraded (work-present) path still fires"
else
  bad "degraded path fires when REST is also unreachable" "$(cat "$D2/lg" 2>/dev/null)"
fi
if grep -q "state:    restarted" "$D2/st"; then
  ok "degraded is treated as work-present, not as zero -- still restarts"
else
  bad "degraded treated as work-present" "$(cat "$D2/st" 2>/dev/null)"
fi

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
