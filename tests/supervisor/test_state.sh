#!/bin/bash
# state.sh emits the supervisor's current situation as a small, hard-capped
# document meant to REPLACE conversation-history replay, not add to it
# (agent-supervisor#248). Its failure modes matter more than its happy path:
# a cap that is aspirational is not a cap, and a row it cannot read must
# read "unknown", never be silently dropped.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE_SH="$HERE/../../scripts/supervisor/state.sh"
pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }
chk() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "want '$2', got '$3'"; fi; }

command -v jq >/dev/null 2>&1 || { echo "  SKIP no jq"; exit 0; }

D=$(mktemp -d); mkdir -p "$D/bin"
trap 'rm -rf "$D"' EXIT INT TERM

# Isolate the quota reader from ambient machine state: state.sh reads
# $SUPERVISOR_STATE/.quota-watch.state directly (not through a stubbable
# binary like digest.sh/cli.py), so without this every quota assertion below
# depends on whether *this machine* happens to have a live quota-watch
# heartbeat -- exactly the nondeterminism the reviewer caught (25 ok/1 failed
# on a box with a real heartbeat, vs. 26 ok/0 failed on a CI runner with
# none). Exported so it applies to every invocation of state.sh in this
# file, not just calls through run().
QSTATE_DIR="$D/qstate"; mkdir -p "$QSTATE_DIR"
export SUPERVISOR_STATE="$QSTATE_DIR"

healthy_digest() {
  cat > "$D/bin/digest.sh" <<'EOF'
#!/bin/bash
cat <<'J'
{"ok":true,"errors":[],
 "lanes":{"free":"agent-supervisor:3","busy":"agent-supervisor:5","blocked":"","dead":"","stale":"","service":"","unknown":""},
 "reconciliation":{"delivered_idle":[{"task":"t1","lane":"agent-supervisor:2","idle_seconds":620,"recovery":"cli.py record-completion --task t1"}]},
 "lane_models":{"lanes":[]},
 "prs":[{"repo":"agent-supervisor","number":249,"run_conclusion":"success","merge_state":"clean","verdict":"approved","verdict_detail":""}]}
J
EOF
  chmod +x "$D/bin/digest.sh"
}

fake_cli() {
  cat > "$D/bin/fake_cli.py" <<'EOF'
import json
print(json.dumps({"tasks":[{"id":"t248","lane":"agent-supervisor:5","status":"delivered","summary":"build state.sh for #248"}]}))
EOF
}

run() {
  STATE_DIGEST_BIN="$D/bin/digest.sh" STATE_LEDGER_PYTHON=python3 STATE_LEDGER_CLI="$D/bin/fake_cli.py" \
    bash "$STATE_SH" "$@" 2>/dev/null
}

healthy_digest; fake_cli

# 1. Happy path: a document is produced, carries a token estimate, and PASSes.
out=$(run)
grep -q "^gate: PASS" <<<"$out" && ok "healthy digest reads gate: PASS" || bad "gate PASS" "$out"
grep -q "^# token_estimate=" <<<"$out" && ok "the token estimate is always printed" || bad "token estimate printed" "$out"

# 2. Never invents a quota number -- no instrumentation exists, so it is
# unknown-by-construction, not a guessed figure.
grep -q "^quota: unknown" <<<"$out" && ok "quota reads unknown, not invented" || bad "quota unknown" "$out"

# 3. Dispatched section carries the ledger's own occupancy, not just digest's
# pane-derived busy/free counts.
grep -q "task=t248" <<<"$out" && ok "dispatched section carries the ledger's open task" || bad "dispatched task shown" "$out"

# 4. The stale-row / unreconciled caveat is a named row, never a silent drop.
grep -q "unreconciled (delivered but lane free" <<<"$out" && ok "unreconciled section is named, not omitted" \
  || bad "unreconciled named" "$out"
grep -q "task=t1 lane=agent-supervisor:2" <<<"$out" && ok "the specific unreconciled row is present" \
  || bad "unreconciled row present" "$out"

# 5. --json is valid JSON and carries the same token estimate machinery.
j=$(run --json)
jq -e . >/dev/null 2>&1 <<<"$j" && ok "--json is valid JSON" || bad "--json valid" "$j"
[ "$(jq -r '.token_estimate' <<<"$j")" -gt 0 ] 2>/dev/null && ok "--json carries a numeric token_estimate" \
  || bad "--json token_estimate numeric" "$j"

# 6. THE CAP IS ENFORCED, NOT ASPIRATIONAL: a cap too small to ever satisfy
# fails loudly (exit 2) and says so, rather than emit an over-budget doc or
# silently widen the ceiling.
out=$(STATE_DIGEST_BIN="$D/bin/digest.sh" STATE_LEDGER_PYTHON=python3 STATE_LEDGER_CLI="$D/bin/fake_cli.py" \
  STATE_TOKEN_CAP=5 bash "$STATE_SH" 2>&1)
rc=$?
chk "an unsatisfiable cap exits 2" "2" "$rc"
grep -q "CAP EXCEEDED" <<<"$out" && ok "cap violation is announced loudly" || bad "CAP EXCEEDED announced" "$out"
grep -qi "never to raise" <<<"$out" && ok "the failure message forbids silently raising the cap" \
  || bad "message forbids silent raise" "$out"

# 7. A generous cap is met at full detail (detail_level=0) -- the reduction
# ladder must not fire when it is not needed.
out=$(STATE_DIGEST_BIN="$D/bin/digest.sh" STATE_LEDGER_PYTHON=python3 STATE_LEDGER_CLI="$D/bin/fake_cli.py" \
  STATE_TOKEN_CAP=1500 bash "$STATE_SH" 2>/dev/null)
grep -q "detail_level=0" <<<"$out" && ok "a generous cap keeps full detail" || bad "full detail kept" "$out"

# 8. digest.sh unreadable degrades every derived section to unknown/FAIL,
# never to a clean-looking empty document (the "instrument that cannot see a
# thing must not look like the thing is absent" rule).
out=$(STATE_DIGEST_BIN="$D/bin/nope-does-not-exist.sh" STATE_LEDGER_CLI="$D/bin/fake_cli.py" bash "$STATE_SH" 2>&1)
grep -q "gate: FAIL (digest.sh unreadable)" <<<"$out" && ok "an unreadable digest.sh reads gate: FAIL, not PASS" \
  || bad "unreadable digest reads FAIL" "$out"

# 9. Missing jq is refused loudly, not silently empty (same discipline
# digest.sh itself holds).
BASH_BIN="$(command -v bash)"
NOJQ_BIN="$D/nojq"; mkdir -p "$NOJQ_BIN"
for t in dirname date awk sed cat mktemp rm; do
  p="$(command -v "$t")" && ln -sf "$p" "$NOJQ_BIN/$t"
done
out=$(PATH="$NOJQ_BIN" STATE_DIGEST_BIN="$D/bin/digest.sh" "$BASH_BIN" "$STATE_SH" 2>&1)
rc=$?
grep -q "jq is required" <<<"$out" && ok "missing jq is named, not silent" || bad "missing jq named" "$out"
chk "missing jq exits 1" "1" "$rc"

# 10. Standing constraints are DERIVED from loop-tick.md, not hand-authored
# in the script -- a change to that document's Boundaries section changes
# this output without touching state.sh.
ALT_LOOP_TICK="$D/loop-tick.md"
cat > "$ALT_LOOP_TICK" <<'EOF'
## Boundaries

- A brand new rule that did not exist when state.sh was written.
EOF
out=$(STATE_DIGEST_BIN="$D/bin/digest.sh" STATE_LEDGER_CLI="$D/bin/fake_cli.py" STATE_LOOP_TICK="$ALT_LOOP_TICK" \
  bash "$STATE_SH" 2>/dev/null)
grep -q "A brand new rule that did not exist" <<<"$out" && ok "constraints reflect loop-tick.md, not a hardcoded copy" \
  || bad "constraints derived from loop-tick.md" "$out"

# 11. agent-supervisor#251: a HANGING digest.sh must not hang state.sh --
# reproduced live against this estate (digest.sh --json hung past 120s with
# zero output, even to stderr). A short STATE_DIGEST_TIMEOUT_SECONDS proves
# the bound is real, not just documented: the stub sleeps far longer than
# the timeout, and state.sh must still return within it, with gate reading
# FAIL and a reason that says "timed out", not a hang or a silent PASS.
cat > "$D/bin/hanging_digest.sh" <<'EOF'
#!/bin/bash
sleep 30
echo '{"ok":true,"errors":[]}'
EOF
chmod +x "$D/bin/hanging_digest.sh"
start=$(date +%s)
out=$(STATE_DIGEST_BIN="$D/bin/hanging_digest.sh" STATE_LEDGER_PYTHON=python3 STATE_LEDGER_CLI="$D/bin/fake_cli.py" \
  STATE_DIGEST_TIMEOUT_SECONDS=2 timeout 20 bash "$STATE_SH" 2>&1)
elapsed=$(( $(date +%s) - start ))
[ "$elapsed" -lt 15 ] && ok "a hanging digest.sh does not hang state.sh (returned in ${elapsed}s)" \
  || bad "hanging digest.sh bounded" "took ${elapsed}s, expected well under the 20s hard test timeout"
grep -q "gate: FAIL (digest.sh timed out after 2s)" <<<"$out" && ok "a hanging digest.sh reads gate: FAIL with a timeout reason" \
  || bad "hanging digest gate reason" "$out"

# 12. Same shape for the ledger: a hanging `cli.py status` must not hang
# state.sh either, and must read "unknown" (not the CAP-crossing failure
# mode of a stalled document, and not a false "none").
cat > "$D/bin/hanging_cli.py" <<'EOF'
import time
time.sleep(30)
EOF
start=$(date +%s)
out=$(STATE_DIGEST_BIN="$D/bin/digest.sh" STATE_LEDGER_PYTHON=python3 STATE_LEDGER_CLI="$D/bin/hanging_cli.py" \
  STATE_LEDGER_TIMEOUT_SECONDS=2 timeout 20 bash "$STATE_SH" 2>&1)
elapsed=$(( $(date +%s) - start ))
[ "$elapsed" -lt 15 ] && ok "a hanging ledger cli.py does not hang state.sh (returned in ${elapsed}s)" \
  || bad "hanging ledger bounded" "took ${elapsed}s, expected well under the 20s hard test timeout"
grep -q "dispatched: unknown -- ledger status timed out after 2s" <<<"$out" && \
  ok "a hanging ledger reads dispatched: unknown with a timeout reason, not 'none'" \
  || bad "hanging ledger dispatched unknown" "$out"

# 13. agent-supervisor#251 (gap 2): an UNREADABLE (not hanging) ledger must
# also read dispatched: unknown, distinguishable from a genuinely empty
# ledger -- both used to collapse to the same "dispatched: none".
out=$(STATE_DIGEST_BIN="$D/bin/digest.sh" STATE_LEDGER_CLI="$D/bin/nope-does-not-exist.py" \
  bash "$STATE_SH" 2>/dev/null)
grep -q "^dispatched: unknown -- ledger status unreadable" <<<"$out" && \
  ok "an unreadable ledger reads dispatched: unknown, not 'none'" \
  || bad "unreadable ledger dispatched unknown" "$out"

# 14. Same discipline for constraints: a missing loop-tick.md must read
# constraints: unknown, not an empty "constraints:" section that looks like
# a document with no standing rules at all.
out=$(STATE_DIGEST_BIN="$D/bin/digest.sh" STATE_LEDGER_CLI="$D/bin/fake_cli.py" \
  STATE_LOOP_TICK="$D/no-such-loop-tick.md" bash "$STATE_SH" 2>/dev/null)
grep -q "^constraints: unknown -- .*no-such-loop-tick.md unreadable" <<<"$out" && \
  ok "a missing loop-tick.md reads constraints: unknown, not an empty section" \
  || bad "missing loop-tick constraints unknown" "$out"

# 15. agent-supervisor#251 review, "on the feature itself": the two existing
# cap tests prove the cap is enforced at its EDGES (unsatisfiable -> exit 2;
# generous -> full detail) but neither proves the MIDDLE of the ladder --
# that a cap satisfiable only via reduction actually reduces the least
# important thing first, not just whatever happens to shrink the string.
# Fixture: 15 open PRs (each with a verbose verdict_detail) and 10 long
# standing constraints, but only 3 dispatched tasks -- dispatched is the
# ledger's own occupancy and the thing state.sh exists to keep truthful, so
# it must survive intact at the first reduction level while the less
# essential PR/constraint detail is what gets trimmed.
python3 - "$D" <<'PYEOF'
import json, sys
D = sys.argv[1]
prs = []
for i in range(15):
    prs.append({
        "repo": "agent-supervisor", "number": 300 + i,
        "run_conclusion": "success", "merge_state": "clean",
        "verdict": "approved",
        "verdict_detail": (
            f"ledger: lane-{i} recorded a fairly long detail sentence "
            f"explaining the basis for PR {300 + i}'s verdict so this line is not tiny"
        ),
    })
doc = {
    "ok": True, "errors": [],
    "lanes": {"free": "agent-supervisor:3", "busy": "agent-supervisor:5",
              "blocked": "", "dead": "", "stale": "", "service": "", "unknown": ""},
    "reconciliation": {"delivered_idle": []},
    "lane_models": {"lanes": []},
    "prs": prs,
}
with open(f"{D}/bin/heavy_digest.sh", "w") as f:
    f.write("#!/bin/bash\ncat <<'J'\n" + json.dumps(doc) + "\nJ\n")
PYEOF
chmod +x "$D/bin/heavy_digest.sh"
cat > "$D/bin/heavy_cli.py" <<'EOF'
import json
print(json.dumps({"tasks": [
  {"id": "t1", "lane": "agent-supervisor:2", "status": "delivered", "summary": "a short summary one"},
  {"id": "t2", "lane": "agent-supervisor:3", "status": "in_progress", "summary": "a short summary two"},
  {"id": "t3", "lane": "agent-supervisor:4", "status": "delivered", "summary": "a short summary three"},
]}))
EOF
HEAVY_LOOP_TICK="$D/heavy-loop-tick.md"
{
  echo "## Boundaries"
  echo
  for n in one two three four five six seven eight nine ten; do
    echo "- Constraint line number $n, moderately long so it adds up across many lines."
  done
} > "$HEAVY_LOOP_TICK"
out=$(STATE_DIGEST_BIN="$D/bin/heavy_digest.sh" STATE_LEDGER_PYTHON=python3 STATE_LEDGER_CLI="$D/bin/heavy_cli.py" \
  STATE_LOOP_TICK="$HEAVY_LOOP_TICK" STATE_TOKEN_CAP=1000 bash "$STATE_SH" 2>/dev/null)
grep -q "detail_level=1" <<<"$out" && ok "a cap satisfiable only via reduction fires the ladder's first rung" \
  || bad "middle-of-ladder cap picks level 1" "$out"
grep -q "task=t1 status=delivered" <<<"$out" && grep -q "task=t2 status=in_progress" <<<"$out" \
  && grep -q "task=t3 status=delivered" <<<"$out" \
  && ok "dispatched (the ledger's own occupancy) survives the first reduction intact" \
  || bad "dispatched intact at level 1" "$out"
grep -q "+5 more, omitted to fit the cap" <<<"$out" && ok "open_prs is what gets trimmed first, and says how much" \
  || bad "open_prs trimmed with a stated count" "$out"
grep -q "^constraints: see loop-tick.md#Boundaries" <<<"$out" && ok "constraints collapses to a pointer, not silently dropped" \
  || bad "constraints collapsed to pointer" "$out"

# 16. Quota reader: the four branches of the .quota-watch.state parse that
# had no coverage at all -- only manual runs pasted into the PR body. Each
# writes a fixture state file directly into the isolated $QSTATE_DIR (never
# the real $HOME state dir) and reads state.sh's rendered quota line, the
# same discipline the reviewer used to catch the isolation bug in the first
# place.
QUOTA_FILE="$QSTATE_DIR/.quota-watch.state"
write_quota_state() {
  # $1 state  $2 confirmed  $3 unknown_streak  $4 checked
  cat > "$QUOTA_FILE" <<EOF
state: $1
confirmed: $2
unknown_streak: $3
checked: $4
EOF
}
rm_quota_state() { rm -f "$QUOTA_FILE"; }

# 16a. Missing state file: no .quota-watch.state at all reads unknown with a
# reason naming the file, distinguishable from a parse failure.
rm_quota_state
out=$(run)
grep -q "^quota: unknown -- no readable quota state at" <<<"$out" && \
  ok "a missing quota state file reads unknown, reason names the path" \
  || bad "missing quota state file" "$out"

# 16b. Fresh file, well inside the 1800s staleness window: the raw state is
# reported as current, with an age in seconds.
fresh_checked=$(date -u +%Y-%m-%dT%H:%M:%SZ)
write_quota_state "SAFE" "SAFE" "0" "$fresh_checked"
out=$(run)
grep -q "^quota: SAFE -- checked ${fresh_checked}, .*s ago; last confirmed SAFE; unknown_streak 0" <<<"$out" && \
  ok "a fresh quota state reads the raw state as current, with age" \
  || bad "fresh quota state current" "$out"

# 16c. Stale-vs-fresh boundary: a checked timestamp older than 1800s must
# degrade to unknown even though the file parses cleanly and the raw state
# says SAFE -- retaining a stale "last known good" reading is the exact
# failure class (#80->#8 on 2026-08-15) this reader exists to prevent.
stale_epoch=$(( $(date -u +%s) - 200000 ))
stale_checked=$(date -u -r "$stale_epoch" +%Y-%m-%dT%H:%M:%SZ)
write_quota_state "SAFE" "SAFE" "0" "$stale_checked"
out=$(run)
grep -q "^quota: unknown -- STALE: quota-watch last wrote .*s ago (>1800s); refusing to report 'SAFE' as current" <<<"$out" && \
  ok "a stale checked timestamp degrades quota to unknown, not a retained SAFE" \
  || bad "stale quota degrades to unknown" "$out"

# 16d. A malformed unknown_streak (non-numeric) must not crash the shell
# arithmetic later in the script -- it is coerced to 0 rather than treated
# as a fatal parse error.
write_quota_state "SAFE" "SAFE" "not-a-number" "$fresh_checked"
out=$(run)
grep -q "unknown_streak 0" <<<"$out" && ok "a malformed unknown_streak is coerced to 0, not fatal" \
  || bad "malformed unknown_streak coerced" "$out"

# 16e. state != confirmed: the RAW state field must be what's reported, not
# the retained "confirmed" value -- a genuine non-SAFE raw reading (e.g.
# WINDDOWN) must never be masked behind a last-good confirmed: SAFE. This is
# the specific masking bug the review verified was fixed; it had no
# regression test.
write_quota_state "WINDDOWN" "SAFE" "1" "$fresh_checked"
out=$(run)
grep -q "^quota: WINDDOWN --" <<<"$out" && ok "the raw state (WINDDOWN) is reported, not the retained confirmed (SAFE)" \
  || bad "raw state not masked by confirmed" "$out"
grep -q "last confirmed SAFE" <<<"$out" && ok "the last confirmed value is still surfaced in the reason text" \
  || bad "confirmed surfaced in reason" "$out"

rm_quota_state

echo
echo "state.sh: $pass ok, $fail failed"
[ "$fail" -eq 0 ]
