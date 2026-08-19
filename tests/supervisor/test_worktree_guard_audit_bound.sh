#!/bin/bash
# agent-supervisor#205 review (skills:2): the prior tests for #199/#205 --
# test_worktree_guard_audit.sh and test_watchdog_worktree_guard_audit.sh --
# only ever exercise a dependency that exits non-zero IMMEDIATELY (a missing
# file, an unguarded fixture). Neither ever proves a HANGING dependency is
# caught within a bound; that gap is exactly what the review called blocking,
# the same shape #267 (quota.sh/codexbar) and main's own #276 (quota-watch
# liveness) were both rejected for.
#
# This file drives a real, hanging `git show` through both of the two
# bounds #205's fix pass added, using a `git` shim on PATH that hangs on
# `show` and delegates everything else (worktree list, rev-parse) to the
# real binary -- never a stub of worktree-guard-audit.sh or watchdog.sh
# themselves, so this proves the actual shipped code, not a fixture standing
# in for it.
#
# 1. worktree-guard-audit.sh's own per-`git show` bound
#    (WORKTREE_GUARD_FILE_TIMEOUT_SECONDS): a hung `show` is reported as its
#    own UNKNOWN line and does not stop the rest of the walk or the process.
# 2. watchdog.sh's outer, whole-invocation bound
#    (SUPERVISOR_GUARD_AUDIT_TIMEOUT): set BELOW the per-file bound, proving
#    the outer bound does not depend on the inner one firing first -- it is
#    a genuine backstop, not a dead second layer.
#
# Neither bound must ever report a hang as a clean audit -- an
# observed-absence check that goes quiet on the one input it cannot finish
# reading is exactly the false negative it exists to prevent.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
AUDIT="$SUP/worktree-guard-audit.sh"
WATCHDOG="$SUP/watchdog.sh"
pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }

D="$(mktemp -d "${TMPDIR:-/tmp}/wga-bound-test.XXXXXX")"
trap 'rm -rf "$D"' EXIT INT TERM

REAL_GIT="$(command -v git)" || { echo "no git on PATH"; exit 1; }

# --- a real fixture repo + worktree, never the live estate's own -----------
REPO="$D/repo"
mkdir -p "$REPO/tests/supervisor"
git -C "$REPO" -c init.defaultBranch=main init -q
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name test

# Assembled at heredoc-BUILD time, never adjacent in this file's own source,
# same reason test_worktree_guard_audit.sh does this -- keeps the written-out
# fixture literal (what tmux_verb_guard.py's static scanner would otherwise
# mistake for a live unisolated call here) without this SOURCE file itself
# containing one.
T1="tm"; T2="ux"; V1="new-sess"; V2="ion"
{
  echo '#!/bin/bash'
  printf '%s%s %s%s -d -s "leak-test-$$"\n' "$T1" "$T2" "$V1" "$V2"
} > "$REPO/tests/supervisor/test_fixture.sh"
git -C "$REPO" add -A && git -C "$REPO" commit -q -m "verb, no guard"
SHA="$(git -C "$REPO" rev-parse HEAD)"
git -C "$REPO" worktree add -q --detach "$D/wt" "$SHA"

# --- a `git` shim that hangs on `show`, real otherwise ----------------------
# Records its own pid (exec replaces the image in place, so the pid recorded
# here is the pid of the sleep that must not survive the caller's kill)
# before hanging, so orphan-detection below can check pids by identity
# instead of matching "sleep 300" against a shared host's unrelated
# processes, which is not unique enough on a machine running other suites.
HANG_PIDS="$D/hang-pids"
BIN="$D/bin"; mkdir -p "$BIN"
cat > "$BIN/git" <<EOF
#!/bin/bash
for a in "\$@"; do
  if [ "\$a" = "show" ]; then
    # Hangs until killed -- proves the caller's bound, not this shim's.
    echo "\$\$" >> "$HANG_PIDS"
    exec sleep 300
  fi
done
exec "$REAL_GIT" "\$@"
EOF
chmod +x "$BIN/git"

export WORKTREE_GUARD_FILES="tests/supervisor/test_fixture.sh"

echo "worktree-guard-audit.sh / watchdog.sh -- #205 hanging-dependency bound"

# --- 1. worktree-guard-audit.sh's own per-file bound ------------------------
start=$SECONDS
out_a="$(PATH="$BIN:$PATH" WORKTREE_GUARD_FILE_TIMEOUT_SECONDS=2 "$AUDIT" "$REPO" 2>&1)"
rc_a=$?
elapsed_a=$((SECONDS - start))

if [ "$elapsed_a" -le 15 ]; then
  ok "1a. a hung 'git show' does not hang worktree-guard-audit.sh past its own bound (${elapsed_a}s, bound 2s)"
else
  bad "1a. a hung 'git show' does not hang worktree-guard-audit.sh past its own bound" "took ${elapsed_a}s"
fi

if [ "$rc_a" != "0" ]; then
  ok "1b. a timed-out probe makes the audit exit non-zero, never a clean pass"
else
  bad "1b. a timed-out probe makes the audit exit non-zero, never a clean pass" "rc=0 out=$out_a"
fi

if grep -q "UNKNOWN" <<<"$out_a" && grep -q "wt" <<<"$out_a" && grep -q "test_fixture.sh" <<<"$out_a"; then
  ok "1c. the timeout is reported by name -- UNKNOWN, not folded into a gap or a clean summary"
else
  bad "1c. the timeout is reported by name" "out=$out_a"
fi

if grep -qE "^worktree-guard-audit:.*, [1-9][0-9]* unknown\(s\)" <<<"$out_a"; then
  ok "1d. the summary line's own unknown-count is non-zero"
else
  bad "1d. the summary line's own unknown-count is non-zero" "out=$out_a"
fi

# --- 2. watchdog.sh's outer, whole-invocation bound -------------------------
# Deliberately set BELOW the per-file bound above: if this only ever passed
# because the inner bound happened to fire first, that would prove nothing
# about the outer wrapper existing at all.
STATE="$D/state"; mkdir -p "$STATE" "$STATE/transcripts"
cat > "$D/bin_notify.sh" <<'NOTIFY'
#!/bin/bash
printf 'CALLER=%s SUBJECT=%s BODY=%s\n' "${AGENT_NOTIFY_CALLER:-}" "$1" "$2" >> "${NOTIFY_CALLS:?}"
exit 0
NOTIFY
chmod +x "$D/bin_notify.sh"

start=$SECONDS
WORKTREE_GUARD_FILES="tests/supervisor/test_fixture.sh" \
SUPERVISOR_GUARD_AUDIT_REPO="$REPO" \
SUPERVISOR_GUARD_AUDIT_INTERVAL=0 \
SUPERVISOR_GUARD_AUDIT_TIMEOUT=2 \
WORKTREE_GUARD_FILE_TIMEOUT_SECONDS=30 \
SUPERVISOR_GUARD_AUDIT_STAMP="$D/guard-audit-stamp" \
SUPERVISOR_GUARD_AUDIT_EPISODE="$D/guard-audit-episode" \
SUPERVISOR_GUARD_AUDIT_FAIL_STREAK="$D/guard-audit-fail-streak" \
NOTIFY_CALLS="$D/notify-calls" \
SUPERVISOR_PATH="$BIN:/usr/bin:/bin" NOTIFY_SCRIPT="$D/bin_notify.sh" \
STUB_PANE_STATE=busy \
SUPERVISOR_STATE="$STATE" SUPERVISOR_STATUS="$D/st" SUPERVISOR_LOG="$D/lg" \
SUPERVISOR_STAMP="$D/stamp" SUPERVISOR_HISTORY="$D/hist" NOTIFY_ENV="$D/none.env" \
SLEEPCHECK_DIR="$STATE/transcripts" \
  bash "$WATCHDOG" >/dev/null 2>"$D/err"
elapsed_b=$((SECONDS - start))

if [ "$elapsed_b" -le 20 ]; then
  ok "2a. watchdog.sh's own tick does not hang past SUPERVISOR_GUARD_AUDIT_TIMEOUT (${elapsed_b}s, bound 2s, per-file bound 30s)"
else
  bad "2a. watchdog.sh's own tick does not hang past SUPERVISOR_GUARD_AUDIT_TIMEOUT" "took ${elapsed_b}s; err=$(cat "$D/err" 2>/dev/null)"
fi

if grep -qE '^guard-audit: unknown' "$D/st" 2>/dev/null; then
  ok "2b. watchdog.status reports the timeout as unknown, never as a clean audit"
else
  bad "2b. watchdog.status reports the timeout as unknown, never as a clean audit" "st=$(cat "$D/st" 2>/dev/null)"
fi

if grep -qi "did not finish within" "$D/lg" 2>/dev/null; then
  ok "2c. the log names the timeout explicitly"
else
  bad "2c. the log names the timeout explicitly" "lg=$(cat "$D/lg" 2>/dev/null)"
fi

# None of the shim's recorded hang pids (the stand-in for the hung git
# process, in either test 1 or test 2 above) may still be alive -- the outer
# bound must actually kill what it bounds, not just stop waiting on it.
# Checked by recorded pid identity, not by matching "sleep 300" against the
# process table, which is not unique enough on a host running other suites
# concurrently.
sleep 1
orphans=""
if [ -s "$HANG_PIDS" ]; then
  while IFS= read -r p; do
    [ -n "$p" ] && kill -0 "$p" 2>/dev/null && orphans="$orphans $p"
  done < "$HANG_PIDS"
fi
if [ -z "$orphans" ]; then
  ok "2d. the killed hang leaves no orphaned process behind"
else
  bad "2d. the killed hang leaves no orphaned process behind" "still alive:$orphans"
fi

git -C "$REPO" worktree remove -f "$D/wt" >/dev/null 2>&1

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
