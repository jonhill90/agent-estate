#!/bin/bash
# agent-estate#735 (decided on #721): corpus-stage.sh is the loop-tick
# STAGING step -- extract-and-stage only, never judge. This exercises it
# directly, against real `Ledger` fixtures (never a mock of the ledger
# itself), covering the properties the brief called out as never having
# been demonstrated:
#
#   - a query returning 0 must be verifiable BY HAND, not trusted on faith
#     (positive control: seed a known count, assert that exact count)
#   - nothing falls out of the queue: a staged-but-unjudged prompt resurfaces
#     on every subsequent tick
#   - the loud-absence threshold fires in BOTH directions (age and count),
#     mutation-checked so a broken comparison cannot pass by accident
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$HERE/../../scripts/supervisor/corpus-stage.sh"
SUPERVISOR_DIR="$HERE/../../scripts/supervisor"
pass=0; fail=0

ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

echo "corpus-stage.sh -- extract-and-stage, loud-absence threshold (#735)"

D=$(mktemp -d)
trap 'rm -rf "$D"' EXIT

seed() { # seed <state-dir> <python-heredoc-on-stdin>
  python3 - "$1" <<PY
import sys
sys.path.insert(0, "$SUPERVISOR_DIR")
from core import Ledger
ledger = Ledger(sys.argv[1])
$(cat)
PY
}

run_stage() { # run_stage <state-dir>  -- prints stdout, sets $rc
  out=$(SUPERVISOR_STATE="$1" "$SCRIPT" 2>"$1/stage.stderr")
  rc=$?
}

# --- POSITIVE CONTROL: a count that is 0 because it is wrong looks exactly
# like an empty backlog -- prove a non-zero, hand-verifiable count works ----
STATE=$(mktemp -d "$D/state.XXXXXX")
seed "$STATE" <<'PY'
import time
now = int(time.time())
for i in range(5):
    ledger.record_prompt(f"p-recent-{i}", at=now - 60 * i, text_raw=f"recent prompt {i}", context="ctx")
PY
run_stage "$STATE"
if [ "$rc" = "0" ] && grep -q "^count=5 " <<<"$out"; then
  ok "positive control: 5 seeded prompts staged, count=5 reported exactly (not a suspicious zero)"
else
  bad "positive control: 5 seeded prompts staged, count=5 reported exactly" "rc=$rc out=$out"
fi
if [ "$(python3 -c "import json; print(len(json.load(open('$STATE/corpus-stage.json'))))")" = "5" ]; then
  ok "the staged JSON file itself also carries exactly 5 rows"
else
  bad "the staged JSON file itself also carries exactly 5 rows" "$(cat "$STATE/corpus-stage.json")"
fi

# --- a genuinely empty ledger is a real 0, not a broken query -------------
STATE=$(mktemp -d "$D/state.XXXXXX")
seed "$STATE" <<'PY'
pass
PY
run_stage "$STATE"
if [ "$rc" = "0" ] && grep -q "^count=0 " <<<"$out"; then
  ok "a genuinely empty ledger reports count=0, rc=0"
else
  bad "a genuinely empty ledger reports count=0, rc=0" "rc=$rc out=$out"
fi

# --- NOTHING FALLS OUT OF THE QUEUE: a staged prompt resurfaces every tick
# the judging lane never runs ------------------------------------------------
STATE=$(mktemp -d "$D/state.XXXXXX")
seed "$STATE" <<'PY'
ledger.record_prompt("p1", at=2_000_000, text_raw="never judged", context="ctx")
PY
run_stage "$STATE"
first_batch=$(cat "$STATE/corpus-stage.json")
run_stage "$STATE"   # a second tick; the judging lane still never ran
second_batch=$(cat "$STATE/corpus-stage.json")
if grep -q '"p1"' <<<"$first_batch" && grep -q '"p1"' <<<"$second_batch"; then
  ok "an unjudged prompt is staged again on the very next tick -- it never silently drops out"
else
  bad "an unjudged prompt is staged again on the very next tick" "first=$first_batch second=$second_batch"
fi

# --- THRESHOLD, direction 1: the exact incident (4-day-old hard directive)
# crosses the AGE line and escalates (rc=1) -------------------------------
STATE=$(mktemp -d "$D/state.XXXXXX")
seed "$STATE" <<'PY'
import time
now = int(time.time())
ledger.record_prompt("p-old", at=now - 4 * 24 * 3600, text_raw="weight=hard directive", context="ctx")
PY
run_stage "$STATE"
if [ "$rc" = "1" ]; then
  ok "GREEN: a 4-day-old unjudged prompt (the exact #721 incident) crosses the age threshold and escalates"
else
  bad "GREEN: a 4-day-old unjudged prompt crosses the age threshold and escalates" "rc=$rc out=$out"
fi

# --- THRESHOLD, direction 2: the SAME prompt, one hour old, stays quiet ----
STATE=$(mktemp -d "$D/state.XXXXXX")
seed "$STATE" <<'PY'
import time
now = int(time.time())
ledger.record_prompt("p-recent", at=now - 3600, text_raw="weight=hard directive", context="ctx")
PY
run_stage "$STATE"
if [ "$rc" = "0" ]; then
  ok "GREEN: the same shape of prompt, one hour old, stays under the age threshold (rc=0)"
else
  bad "GREEN: the same shape of prompt, one hour old, stays under the age threshold" "rc=$rc out=$out"
fi

# --- THRESHOLD, direction 3: COUNT alone crosses the line even when every
# prompt is fresh (a case age-only would miss) -----------------------------
STATE=$(mktemp -d "$D/state.XXXXXX")
seed "$STATE" <<'PY'
import time
now = int(time.time())
for i in range(200):
    ledger.record_prompt(f"p-fresh-{i}", at=now - i, text_raw=f"fresh {i}", context="ctx")
PY
run_stage "$STATE"
if [ "$rc" = "1" ] && grep -q '^count=200 ' <<<"$out"; then
  ok "GREEN: 200 fresh prompts (above the derived count threshold of 168) escalate on count alone"
else
  bad "GREEN: 200 fresh prompts escalate on count alone" "rc=$rc out=$out"
fi

# --- THRESHOLD, direction 4: 100 fresh prompts (below 168) stay quiet -----
STATE=$(mktemp -d "$D/state.XXXXXX")
seed "$STATE" <<'PY'
import time
now = int(time.time())
for i in range(100):
    ledger.record_prompt(f"p-fresh-{i}", at=now - i, text_raw=f"fresh {i}", context="ctx")
PY
run_stage "$STATE"
if [ "$rc" = "0" ]; then
  ok "GREEN: 100 fresh prompts (below the count threshold) do not escalate"
else
  bad "GREEN: 100 fresh prompts (below the count threshold) do not escalate" "rc=$rc out=$out"
fi

# --- RED: mutate the threshold comparison so it can never fire, prove the
# GREEN case above actually depends on that line -----------------------------
BROKEN="$D/corpus-stage-broken.sh"
sed 's/if \[ "\$count" -gt "\$CORPUS_STAGE_COUNT_THRESHOLD" \] || \[ "\$oldest_age_seconds" -gt "\$age_threshold_seconds" \]; then/if false; then/' \
  "$SCRIPT" > "$BROKEN"
if grep -qF 'if false; then' "$BROKEN"; then
  ok "constructed a mutated copy whose threshold comparison can never be true"
else
  bad "constructed a mutated copy whose threshold comparison can never be true" "sed did not match -- check corpus-stage.sh's exact source line"
fi
chmod +x "$BROKEN"

STATE=$(mktemp -d "$D/state.XXXXXX")
seed "$STATE" <<'PY'
import time
now = int(time.time())
ledger.record_prompt("p-old", at=now - 4 * 24 * 3600, text_raw="weight=hard directive", context="ctx")
PY
broken_out=$(SUPERVISOR_STATE="$STATE" ITEMIZE_PROMPTS="$SUPERVISOR_DIR/itemize_prompts.py" "$BROKEN" 2>"$STATE/broken.stderr"); broken_rc=$?
if [ "$broken_rc" = "0" ]; then
  ok "RED: with the comparison mutated to 'if false', the same 4-day-old prompt no longer escalates -- proves the GREEN case depends on the real comparison"
else
  bad "RED: with the comparison mutated to 'if false', the same 4-day-old prompt no longer escalates" "rc=$broken_rc out=$broken_out"
fi

# --- extract failure is an INSTRUMENT failure (rc=2), never a quiet 0 -----
STATE=$(mktemp -d "$D/state.XXXXXX")
FAILING_ITEMIZE="$D/itemize-fails.py"
cat > "$FAILING_ITEMIZE" <<'PY'
import sys
print("simulated extract failure", file=sys.stderr)
sys.exit(9)
PY
run_out=$(SUPERVISOR_STATE="$STATE" ITEMIZE_PROMPTS="$FAILING_ITEMIZE" "$SCRIPT" 2>"$STATE/fail.stderr")
run_rc=$?
if [ "$run_rc" = "2" ]; then
  ok "a failed --extract is reported as rc=2 (instrument failure), not rc=0"
else
  bad "a failed --extract is reported as rc=2 (instrument failure)" "rc=$run_rc out=$run_out $(cat "$STATE/fail.stderr" 2>/dev/null)"
fi
if grep -q "simulated extract failure" "$STATE/fail.stderr"; then
  ok "the underlying failure's own diagnostic output is not discarded"
else
  bad "the underlying failure's own diagnostic output is not discarded" "$(cat "$STATE/fail.stderr" 2>/dev/null)"
fi

echo
echo "corpus-stage.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
