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

echo
echo "state.sh: $pass ok, $fail failed"
[ "$fail" -eq 0 ]
