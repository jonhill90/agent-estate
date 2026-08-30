#!/bin/bash
# agent-supervisor#480: `claim.sh stale` reported EVERY claude-print
# dispatch (a plain `claude -p` subprocess, dispatch-claude-print.sh -- no
# tmux window at all by design) as stale from the moment it started until a
# PR opened, because the old liveness check only knew two signals: a tmux
# window naming the issue, and an open PR fixing it. A claude-print lane can
# never satisfy the first, and naturally does not satisfy the second while
# still in progress.
#
# Reproduced live, twice, same tick: issue #473 and #470 were both reported
# stale by `claim.sh stale <repo>` while genuinely `accepted`/`delivered` in
# the ledger (confirmed via `cli.py task-lane --task <id>` and a live `ps`
# process) -- see the issue for the full transcript.
#
# The fix adds a THIRD liveness signal: the ledger itself, via
# `cli.py issue-lane`, exposing the most recent dispatch's `status` and
# `completed_at`. `claim.sh`'s own `ledger_claims_live` treats
# `status` in (`delivered`,`accepted`) with `completed_at` still unset as a
# live claim, tmux window or not.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAIM="$HERE/../../scripts/supervisor/claim.sh"
CLI="$HERE/../../scripts/supervisor/cli.py"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

D=$(mktemp -d); mkdir -p "$D/bin"
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cp "$HERE/stubs/tmux-lanes" "$D/bin/tmux"
cp "$HERE/stubs/ps-lanes" "$D/bin/ps"

# Two claimed issues, no PR open for either -- the shape every claude-print
# dispatch is in from the moment it starts.
cat > "$D/issues" <<'FIX'
473|jonhill90|accepted, claude-print, no window|OPEN
470|jonhill90|delivered, claude-print, no window|OPEN
466|jonhill90|complete, no window -- genuinely stale|OPEN
FIX
: > "$D/prs"

# No window names any of the three issues above -- lanes.sh sees only an
# unrelated lane, the exact shape a claude-print dispatch produces (no
# window at all).
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
FIX

S="$D/state"; mkdir -p "$S"

run() { PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
        LANES_FIXTURE="$D/lanes" LANES_SESSION=t AGENT_SUPERVISOR_STATE_DIR="$S" \
        bash "$CLAIM" "$@" 2>&1; }
cli() { PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
        python3 "$CLI" --state-dir "$S" "$@" 2>&1; }

# --- #470: record-dispatch alone leaves a task `delivered` (cli.py's own
#     documented default, "Omitted... leaves the task `delivered`, exactly
#     today's behaviour") -- no accept/complete needed to reproduce this
#     status. ------------------------------------------------------------
out=$(cli record-dispatch --lane t:1 --task as470-print --summary "#470 claude-print" \
  --pane-id %1 --pane-path "$D/repo" --command claude --server-id srv --session-id sess \
  --issue 470 --github acme/repo --harness claude)
if grep -q '"id":"as470-print"' <<<"$out"; then ok "fixture: #470's task is recorded"; else bad "fixture: #470's task is recorded" "$out"; fi

# --- #473: move the same shape of task to `accepted` -- `cli.py accept`
#     wants to verify the caller against the lane's own pane, which a test
#     fixture has no live pane for; the ledger row itself is what claim.sh
#     reads, so setting it directly is the narrower, sufficient fixture. --
out=$(cli record-dispatch --lane t:2 --task as473-print --summary "#473 claude-print" \
  --pane-id %2 --pane-path "$D/repo" --command claude --server-id srv2 --session-id sess2 \
  --issue 473 --github acme/repo --harness claude)
if grep -q '"id":"as473-print"' <<<"$out"; then ok "fixture: #473's task is recorded"; else bad "fixture: #473's task is recorded" "$out"; fi
sqlite3 "$S/ledger.sqlite3" \
  "UPDATE tasks SET status='accepted', accepted_at=1700000000 WHERE id='as473-print';"
accepted_status=$(sqlite3 "$S/ledger.sqlite3" "SELECT status||'|'||coalesce(completed_at,'NULL') FROM tasks WHERE id='as473-print';")
if [ "$accepted_status" = "accepted|NULL" ]; then
  ok "fixture: #473's task is accepted with no completed_at"
else
  bad "fixture: #473's task is accepted with no completed_at" "$accepted_status"
fi

# --- #466: a task that genuinely finished -- must NOT be protected by this
#     fix. Proves the ledger check narrows to open work, it does not
#     blanket-suppress staleness for anything the ledger has ever seen. ---
out=$(cli record-dispatch --lane t:3 --task as466-print --summary "#466 claude-print, done" \
  --pane-id %3 --pane-path "$D/repo" --command claude --server-id srv3 --session-id sess3 \
  --issue 466 --github acme/repo --harness claude)
if grep -q '"id":"as466-print"' <<<"$out"; then ok "fixture: #466's task is recorded"; else bad "fixture: #466's task is recorded" "$out"; fi
sqlite3 "$S/ledger.sqlite3" \
  "UPDATE tasks SET status='complete', completed_at=1700000000 WHERE id='as466-print';"

echo "claim.sh stale, claude-print lanes (#480)"

out=$(run stale acme/repo)

if grep -qx '470' <<<"$out"; then
  bad "a delivered claude-print task with no window is not reported stale" "$out"
else
  ok "a delivered claude-print task with no window is not reported stale"
fi

if grep -qx '473' <<<"$out"; then
  bad "an accepted claude-print task with no window is not reported stale" "$out"
else
  ok "an accepted claude-print task with no window is not reported stale"
fi

if grep -qx '466' <<<"$out"; then
  ok "a genuinely complete task with no window is still reported stale"
else
  bad "a genuinely complete task with no window is still reported stale" "$out"
fi

# --- audit/reap must agree: #470/#473 are not swept, #466 is -------------
audit_out=$(run audit acme/repo); audit_rc=$?
if grep -q '#466 is claimed with no live lane' <<<"$audit_out"; then
  ok "audit reports #466 as stale"
else
  bad "audit reports #466 as stale" "$audit_out"
fi
if grep -q '#470 is claimed with no live lane' <<<"$audit_out"; then
  bad "audit does not report the delivered claude-print task #470" "$audit_out"
else
  ok "audit does not report the delivered claude-print task #470"
fi
if grep -q '#473 is claimed with no live lane' <<<"$audit_out"; then
  bad "audit does not report the accepted claude-print task #473" "$audit_out"
else
  ok "audit does not report the accepted claude-print task #473"
fi
if [ "$audit_rc" -ne 0 ]; then ok "audit exits non-zero while #466 is genuinely stale"; else bad "audit exits non-zero while #466 is genuinely stale" "rc=$audit_rc"; fi

reap_out=$(run reap acme/repo)
if grep -q 'released #466' <<<"$reap_out"; then ok "reap releases the genuinely stale #466"; else bad "reap releases the genuinely stale #466" "$reap_out"; fi
if grep -q 'released #470' <<<"$reap_out"; then bad "reap does not release the live #470 claim" "$reap_out"; else ok "reap does not release the live #470 claim"; fi
if grep -q 'released #473' <<<"$reap_out"; then bad "reap does not release the live #473 claim" "$reap_out"; else ok "reap does not release the live #473 claim"; fi

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
