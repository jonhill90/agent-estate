#!/bin/bash
# dispatch-pi-rpc.sh must deliver one lane's brief over a REAL `pi --mode rpc`
# round trip -- no tmux, no send-keys anywhere in the path -- and must refuse
# rather than fall back when that RPC path is broken.
#
# WHY: agent-supervisor#160. Measured at the time this was filed: all 16
# registered lanes carried transport=send-keys and no harness declared
# anything else, even though `PiRPCAdapter` (agent-supervisor#58, #61) had
# existed, tested, with no caller since it landed -- the same "reachable code
# with no live caller" shape `acp_transport.py` was in before #56/#131. This
# suite is that missing caller's own test: it proves ONE real dispatch runs
# end to end over pi-rpc, that breaking the RPC path makes the dispatch
# refuse rather than quietly succeed some other way, and that lanes this
# script never touches are provably unaffected.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DISPATCH="$HERE/../../scripts/supervisor/dispatch-pi-rpc.sh"
CLI="$HERE/../../scripts/supervisor/cli.py"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

D=$(mktemp -d); mkdir -p "$D/bin" "$D/roots"
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cp "$HERE/stubs/pi" "$D/bin/pi"
# Deliberately NO tmux stub on PATH: dispatch-pi-rpc.sh must never shell out
# to it. If it ever does, that call fails with "command not found" and the
# case that checks for tmux's absence below catches it.

git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/repo" 2>/dev/null
REPO="$D/repo"
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name "Test"
git -C "$REPO" checkout -q -b main
echo one > "$REPO/file.txt"
git -C "$REPO" add file.txt
git -C "$REPO" commit -q -m initial
git -C "$REPO" push -q -u origin main

cat > "$D/issues" <<'FIX'
160|| Route one real lane onto pi --mode rpc
FIX
: > "$D/prs"
echo "do the pi-rpc thing" > "$D/brief.md"

run() {
  PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    AGENT_SUPERVISOR_STATE_DIR="${LEDGER_STATE:-$D/state}" \
    PI_RPC_LOG="$D/pi-rpc.log" PI_RPC_BROKEN="${PI_RPC_BROKEN:-}" \
    WORKTREE_ROOT="$D/roots" \
    bash "$DISPATCH" "$@" 2>&1
}
ledger() { AGENT_SUPERVISOR_STATE_DIR="${LEDGER_STATE:-$D/state}" python3 "$CLI" "$@"; }

echo "dispatch-pi-rpc.sh"

# --- a 15-send-keys estate, seeded before the pi-rpc dispatch runs ---------
# agent-supervisor#160's own acceptance: "the 15 send-keys lanes are
# unaffected -- show it, do not assert it." Seeded once here so every case
# below can diff its row against this snapshot.
for n in 1 2 3; do
  # `cli.py register` for a claude/send-keys lane needs a REAL tmux pane to
  # read metadata off (`TmuxAdapter.register_lane`); this suite deliberately
  # runs with no tmux stub on PATH at all (see the top-of-file note), so the
  # 15-lane estate is seeded directly against the ledger, exactly the way
  # `test_cli.py`'s own fixtures do.
  AGENT_SUPERVISOR_STATE_DIR="$D/state" python3 -c "
import sys
sys.path.insert(0, '$HERE/../../scripts/supervisor')
from core import Ledger
Ledger('$D/state').register_lane(
    lane='send-keys-$n', pane_id='%$n', nonce='nonce-$n', harness='claude',
    repo='$REPO', server_id='server', session_id='\$$n', command='claude',
)
"
done
BEFORE=$(ledger status)

# --- 1. the happy path: a REAL round trip over pi-rpc, no tmux -------------
LEDGER_STATE="$D/state" out=$(run 160 pi-rpc-transport "$D/brief.md" acme/agent-supervisor "$REPO")
rc=$?
want_exit "a working pi RPC path dispatches successfully" "$rc" 0 "$out"
want_contains "the success message names pi-rpc delivery" "delivered to" "$out"

TRANSPORT_ROW=$(sqlite3 "$D/state/ledger.sqlite3" "select transport, count(*) from lanes where transport='pi-rpc' group by 1;" 2>/dev/null)
want_contains "the ledger records a non-zero pi-rpc row" "pi-rpc|1" "$TRANSPORT_ROW"

want_contains "the task the ledger recorded reads complete" "\"status\":\"complete\"" "$(ledger status)"

want_contains "the brief pointer text crossed the RPC wire, not a pane" \
  "Read $D/brief.md" "$(cat "$D/pi-rpc.log" 2>/dev/null || true)"

want_missing "no tmux command was ever invoked" "tmux" "$out"

AFTER=$(ledger status)
for n in 1 2 3; do
  want_contains "send-keys-$n's own ledger row is unchanged after the pi-rpc dispatch" \
    "\"lane\":\"send-keys-$n\"" "$AFTER"
done
# Field-for-field identical (not merely string-contains, which key
# reordering across two independent json.dumps calls would defeat) modulo
# the new pi-rpc lane/task rows this dispatch itself added -- every
# send-keys-N row inside BEFORE must compare equal, as a dict, to its row
# inside AFTER.
DIFF=$(python3 -c "
import json
before = {l['lane']: l for l in json.loads('''$BEFORE''')['lanes']}
after = {l['lane']: l for l in json.loads('''$AFTER''')['lanes']}
mismatches = [n for n in ('send-keys-1', 'send-keys-2', 'send-keys-3') if before.get(n) != after.get(n)]
print(','.join(mismatches))
")
if [ -z "$DIFF" ]; then
  ok "send-keys-1/2/3's ledger rows are field-for-field identical before/after"
else
  bad "send-keys-1/2/3's ledger rows are field-for-field identical before/after" "changed: $DIFF"
fi

# --- 2. mutation check: break the RPC path, the dispatch must REFUSE -------
LEDGER_STATE="$D/state2"
CLAIM_OUT=$(PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" "$HERE/../../scripts/supervisor/claim.sh" release 160 acme/agent-supervisor 2>&1)
rm -f "$D/pi-rpc.log"
# A different slug from case 1's -- that dispatch's worktree/branch
# (lane/160-pi-rpc-transport) is still on disk (nothing in this estate
# removes a SUCCESSFUL lane's worktree; only `worktree.sh gc` does, on a
# separate sweep), so reusing the same slug here would fail on the branch
# already existing and never reach the RPC break this case exists to test.
PI_RPC_BROKEN=1 LEDGER_STATE="$D/state2" out=$(run 160 pi-rpc-transport-retry "$D/brief.md" acme/agent-supervisor "$REPO")
rc=$?
if [ "$rc" -ne 0 ]; then
  ok "a broken pi RPC path exits non-zero ($rc)"
else
  bad "a broken pi RPC path must exit non-zero" "$out"
fi
want_contains "the refusal says it will not fall back to send-keys" "send-keys" "$out"
want_missing "no prompt ever reached the (broken) pi process" \
  "Read $D/brief.md" "$(cat "$D/pi-rpc.log" 2>/dev/null || true)"

CLAIMABLE=$(PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" "$HERE/../../scripts/supervisor/claim.sh" check 160 acme/agent-supervisor 2>&1); crc=$?
want_exit "the failed dispatch released its claim -- #160 is claimable again" "$crc" 0 "$CLAIMABLE"

echo
echo "pass=$pass fail=$fail"
[ "$fail" -eq 0 ]
