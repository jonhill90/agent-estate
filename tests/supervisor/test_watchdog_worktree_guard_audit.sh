#!/bin/bash
# agent-supervisor#199/#205: worktree-guard-audit.sh existed with nothing
# calling it -- the review that blocked #205 confirmed this by grepping the
# PR head for the script's own name outside its two new files and finding
# only its usage text and its own test. An unwired tool produces exactly one
# more one-time green result (the manual run pasted in a PR body); if a
# worktree drifts stale and unguarded next week, nothing notices until a
# human remembers to run the script by hand.
#
# Same discipline test_watchdog_never_busy_lanes.sh and
# test_watchdog_source_task_sweep.sh already use for #112/#133: this only
# ever runs watchdog.sh itself, exactly as the LaunchAgent would, against a
# disposable throwaway repo with a deliberately unguarded worktree -- never
# against the real agent-supervisor worktree farm, which would make this
# suite's result depend on Jon's live lane inventory instead of on the code.
# If the call from watchdog.sh into worktree-guard-audit.sh is ever removed
# while the audit script itself stays intact, this goes red.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCHDOG="$HERE/../../scripts/supervisor/watchdog.sh"
STUBS="$HERE/stubs"
pass=0; fail=0

D=$(mktemp -d); mkdir -p "$D/bin"
trap 'rm -rf "$D"' EXIT

# --- build the throwaway repo: one file that calls a real tmux verb with NO
# guard, exactly the shape #199 measured, pinned into its own worktree. -----
REPO="$D/repo"
mkdir -p "$REPO/tests/supervisor"
git -C "$REPO" -c init.defaultBranch=main init -q
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name test

# Assembled at heredoc-BUILD time, never adjacent in this file's own source,
# for the same reason test_worktree_guard_audit.sh does this: keeps the
# written-out fixture literal (what the audit's VERB_MARKER greps for)
# without this SOURCE file itself containing an unisolated tmux call that
# tmux_verb_guard.py's static scanner (#180) would flag.
T1="tm"; T2="ux"; V1="new-sess"; V2="ion"
{
  echo '#!/bin/bash'
  printf '%s%s %s%s -d -s "leak-test-$$"\n' "$T1" "$T2" "$V1" "$V2"
} > "$REPO/tests/supervisor/test_fixture.sh"
git -C "$REPO" add -A && git -C "$REPO" commit -q -m "vulnerable: verb, no guard"
SHA_VULN="$(git -C "$REPO" rev-parse HEAD)"
git -C "$REPO" worktree add -q --detach "$D/wt-vuln" "$SHA_VULN" >/dev/null 2>&1

# A `notify.sh` stub that records every call instead of paging anyone for
# real.
cat > "$D/bin/notify.sh" <<'NOTIFY'
#!/bin/bash
printf 'CALLER=%s SUBJECT=%s BODY=%s\n' "${AGENT_NOTIFY_CALLER:-}" "$1" "$2" >> "${NOTIFY_CALLS:?}"
exit 0
NOTIFY
chmod +x "$D/bin/notify.sh"

run() {
  WORKTREE_GUARD_FILES="tests/supervisor/test_fixture.sh" \
  SUPERVISOR_GUARD_AUDIT_REPO="$REPO" \
  SUPERVISOR_GUARD_AUDIT_INTERVAL=0 \
  SUPERVISOR_GUARD_AUDIT_STAMP="$D/guard-audit-stamp" \
  SUPERVISOR_GUARD_AUDIT_EPISODE="$D/guard-audit-episode" \
  SUPERVISOR_GUARD_AUDIT_FAIL_STREAK="$D/guard-audit-fail-streak" \
  NOTIFY_CALLS="$D/notify-calls" \
  SUPERVISOR_PATH="$D/bin:$STUBS:/usr/bin:/bin" NOTIFY_SCRIPT="$D/bin/notify.sh" \
  STUB_PANE_STATE=busy \
  SUPERVISOR_STATE="$D" SUPERVISOR_STATUS="$D/st" SUPERVISOR_LOG="$D/lg" \
  SUPERVISOR_STAMP="$D/stamp" SUPERVISOR_HISTORY="$D/hist" NOTIFY_ENV="$D/none.env" \
  SLEEPCHECK_DIR="$D/transcripts" \
  bash "$WATCHDOG" >/dev/null 2>"$D/err"
}
mkdir -p "$D/transcripts"

echo "watchdog.sh -- #199/#205 worktree-guard-audit wiring"

run
if grep -qE '^guard-audit: GAP' "$D/st" 2>/dev/null; then
  echo "  ok   watchdog.status carries the guard-audit GAP line"; pass=$((pass+1))
else
  echo "  FAIL no guard-audit GAP line in watchdog.status:"; sed 's/^/       /' "$D/st" 2>/dev/null; fail=$((fail+1))
fi

if [ -f "$D/notify-calls" ] && grep -qE 'CALLER=supervisor' "$D/notify-calls" \
  && grep -qE 'wt-vuln' "$D/notify-calls" && grep -qE 'test_fixture\.sh' "$D/notify-calls"; then
  echo "  ok   notify.sh was called, naming the unguarded worktree and file"; pass=$((pass+1))
else
  echo "  FAIL notify.sh was not called correctly:"; sed 's/^/       /' "$D/notify-calls" 2>/dev/null; fail=$((fail+1))
fi

calls_after_first=$([ -f "$D/notify-calls" ] && wc -l < "$D/notify-calls" | tr -d ' ' || echo 0)

# A second tick with the SAME unguarded worktree must not page a second time
# -- same dedup discipline #112's never-busy check applies, via the episode
# file storing the actual GAP lines rather than a bare boolean.
run
calls_after_second=$([ -f "$D/notify-calls" ] && wc -l < "$D/notify-calls" | tr -d ' ' || echo 0)
if [ "$calls_after_second" = "$calls_after_first" ]; then
  echo "  ok   a second tick with the same gap does not page again (dedup)"; pass=$((pass+1))
else
  echo "  FAIL the same gap paged a second time:"; sed 's/^/       /' "$D/notify-calls" 2>/dev/null; fail=$((fail+1))
fi

# Clear the gap (remove the unguarded worktree, add a guarded one in its
# place) and confirm the episode clears -- proven by a later, fresh gap
# paging again rather than reading as the same still-open episode.
git -C "$REPO" worktree remove -f "$D/wt-vuln" >/dev/null 2>&1
{
  echo '#!/bin/bash'
  echo 'unset TMUX'
  echo 'export TMUX_TMPDIR="$RT"'
  echo 'assert_isolated_tmux || exit 1'
  printf '%s%s %s%s -d -s "leak-test-$$"\n' "$T1" "$T2" "$V1" "$V2"
} > "$REPO/tests/supervisor/test_fixture.sh"
git -C "$REPO" add -A && git -C "$REPO" commit -q -m "guarded"
SHA_GUARDED="$(git -C "$REPO" rev-parse HEAD)"
git -C "$REPO" worktree add -q --detach "$D/wt-guarded" "$SHA_GUARDED" >/dev/null 2>&1

run
if grep -qE '^guard-audit: worktree-guard-audit:' "$D/st" 2>/dev/null; then
  echo "  ok   a clean audit reports its clean summary, not a stale GAP"; pass=$((pass+1))
else
  echo "  FAIL guard-audit line did not clear after the gap was fixed:"; sed 's/^/       /' "$D/st" 2>/dev/null; fail=$((fail+1))
fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
