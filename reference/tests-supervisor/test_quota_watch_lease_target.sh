#!/bin/bash
# agent-supervisor#662: quota-watch.sh's target resolution must not trust a
# same-named window inside whatever session the CONFIGURED target happens to
# name. On 2026-08-27 the director had moved WHOLESALE to a different
# session (`estate`, not `agent-supervisor`), and the old by-name fallback --
# correctly scoped to the configured session -- found a LEFTOVER window
# there that still happened to be named `supervisor`. It was not the
# director; it was an unrelated pane from a retired layout. The fallback did
# exactly what it was written to do and still delivered to nobody.
#
# THE FIX: prefer the ledger's own record of who holds the supervisor role
# (`cli.py take-supervisor-lease`, taken by the director's own tick loop
# with its own pid -- loop-tick.md) and locate THAT process's real tmux pane
# by walking the kernel's process tree, before ever falling back to a name
# guess. A pid is not confined to any one session, so it finds wherever the
# real director actually lives now -- session included -- which the by-name
# fallback structurally cannot.
#
# REAL tmux, not the stub the rest of this suite uses: the mechanism under
# test resolves a REAL process's pid to its REAL pane via the kernel's own
# process tree (`ps -o ppid=`), which a text-based stub cannot model. Two
# isolated sessions reproduce the exact incident shape: one holds a stale
# leftover window named `supervisor` (what the old fallback picks); the
# other holds the real lease-holder's pane, in a completely different
# session (what the fix must pick instead).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCH="$HERE/../../scripts/supervisor/quota-watch.sh"
CLI="$HERE/../../scripts/supervisor/cli.py"
source "$HERE/../../scripts/supervisor/tmux-isolation.sh"

S1="qw662-stale-$$"
S2="qw662-live-$$"
RT="$(mktemp -d "${TMPDIR:-/tmp}/qw662-tmux.XXXXXX")"
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$RT"
assert_isolated_tmux || exit 1

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

STATE=""
MUTANT_DIR=""
cleanup() {
  unset TMUX TMUX_PANE
  export TMUX_TMPDIR="$RT"
  tmux kill-session -t "$S1" 2>/dev/null
  tmux kill-session -t "$S2" 2>/dev/null
}
cleanup_all() { cleanup; rm -rf "$RT" "$STATE" "$MUTANT_DIR" 2>/dev/null; }
trap cleanup_all EXIT INT TERM

if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP no tmux on PATH"; exit 0
fi

echo "quota-watch.sh -- target resolution prefers the supervisor lease over a name guess (#662)"

# --- fixture: the exact incident shape ------------------------------------
# S1 ("stale"): a leftover session whose window 1 is named 'supervisor' but
#               is NOT the live director -- exactly what the old by-name
#               fallback would find and trust.
# S2 ("live"):  the session the director actually lives in now. Its window 1
#               runs a real process tree: a bash parent that forks a
#               long-lived `sleep` in the background and prints
#               "esc to interrupt" once before blocking in `wait` -- the
#               `sleep`'s pid is what gets registered as the supervisor
#               lease owner, so pane_for_pid must walk UP from a CHILD of
#               the pane's own first process to find it, not merely match
#               pane_pid directly.
# `-f` on the FIRST command that starts the server, not `set-option`
# afterward: base-index is a server-wide option, and `new-session` creates
# its first window using whatever base-index the server already has at that
# instant, so setting it after window 1 already exists is too late for that
# window itself (agent-supervisor#459 measured this directly). Jon's
# personal tmux.conf happens to set base-index 1, which is why this suite
# passed locally 11/11 while failing here: a bare server -- every GitHub
# Actions runner, and this isolated one without this line -- defaults to
# base-index 0, so `-n supervisor` lands S1's only window on index 0 and
# every literal "=$S1:1"/"=$S2:1" target below then fails outright
# ("can't find window: 1") because nothing was ever created there. Both S1
# and S2 share this isolated server (same $TMUX_TMPDIR, default socket), so
# setting it once on the call that starts the server is enough for both.
QW_CONF="$(mktemp "${TMPDIR:-/tmp}/qw662-tmux-conf.XXXXXX")"
printf 'set -g base-index 1\n' > "$QW_CONF"
tmux -f "$QW_CONF" new-session -d -s "$S1" -n supervisor -c /tmp
rm -f "$QW_CONF"
tmux new-session -d -s "$S2" -n director -c /tmp
# `-l` (literal): plain `send-keys` lets the shell's own line editor interpret
# some characters -- an interactive zsh's default history-expansion treats a
# bare `!` specially even inside a typed (not history-recalled) line, which
# silently ate `$!` below without this. Disable it explicitly first, the same
# defensive step `send_message` itself does not need only because its own
# WINDDOWN_MSG/RESUME_MSG never contain `!`.
tmux send-keys -t "=$S2:1" -l 'unsetopt histexpand 2>/dev/null; set +H 2>/dev/null'
tmux send-keys -t "=$S2:1" Enter
sleep 1
tmux send-keys -t "=$S2:1" -l 'sleep 600 & echo "LEASE_PID=$!"; echo "esc to interrupt"; wait'
tmux send-keys -t "=$S2:1" Enter
sleep 1
LEASE_PID="$(tmux capture-pane -p -t "=$S2:1" 2>/dev/null | sed -n 's/^LEASE_PID=//p' | tail -1)"
if [ -z "$LEASE_PID" ]; then
  echo "  FAIL setup: could not read the sleep pid from S2's pane"
  exit 1
fi
ok "setup: S2's real background process (pid $LEASE_PID) is a grandchild of its pane, not the pane_pid itself"

STATE="$(mktemp -d "${TMPDIR:-/tmp}/qw662-state.XXXXXX")"
cp "$HERE/stubs/notify-quota-watch" "$STATE/notify.sh"; chmod +x "$STATE/notify.sh"
cat > "$STATE/gate-winddown" <<'EOF'
#!/bin/bash
exit 1
EOF
chmod +x "$STATE/gate-winddown"

python3 "$CLI" --state-dir "$STATE" take-supervisor-lease --owner-pid "$LEASE_PID" >/dev/null
LEASE_CHECK="$(python3 "$CLI" --state-dir "$STATE" supervisor-lease)"
if grep -qF '"held":true' <<<"$LEASE_CHECK"; then
  ok "setup: the supervisor lease is recorded for pid $LEASE_PID"
else
  echo "  FAIL setup: lease was not recorded: $LEASE_CHECK"
  exit 1
fi

# The configured target names a window id that will never exist on this
# server (simulating the post-restart @id going stale), in the STALE
# session -- exactly #662's own shape: the CONFIGURED SESSION itself is the
# wrong one, not just the window index within it.
GONE_TARGET="$S1:@999"

tick() {  # tick <script> <notify-log>
  SUPERVISOR_STATE="$STATE" QUOTA_GATE="$STATE/gate-winddown" \
    QUOTA_WATCH_TARGET="$GONE_TARGET" QUOTA_WATCH_NOTIFY_SCRIPT="$STATE/notify.sh" \
    NOTIFY_LOG="$2" \
    bash "$1" --once >>"$STATE/quota-watch.out" 2>&1
}

# --- case 1: THE FIX -- resolves via the lease, not the stale session's --
#             name-alike leftover -----------------------------------------
NLOG1="$(mktemp "${TMPDIR:-/tmp}/qw662-notify1.XXXXXX")"
tick "$WATCH" "$NLOG1"

s2_capture="$(tmux capture-pane -p -S - -t "=$S2:1" 2>/dev/null)"
s1_capture="$(tmux capture-pane -p -S - -t "=$S1:1" 2>/dev/null)"

if grep -qF "QUOTA IS LOW" <<<"$s2_capture"; then
  ok "the wind-down landed on S2's real pane (the lease-holder), not a name guess"
else
  bad "the wind-down landed on S2's real pane (the lease-holder), not a name guess" "$s2_capture"
fi
if grep -qF "QUOTA IS LOW" <<<"$s1_capture"; then
  bad "the stale S1 leftover (matching name, wrong session) received NOTHING" "$s1_capture"
else
  ok "the stale S1 leftover (matching name, wrong session) received NOTHING"
fi
if grep -q "resolved to $S2:1 via the supervisor lease" "$STATE/quota-watch.out"; then
  ok "the log says it resolved via the lease, not a guess"
else
  bad "the log says it resolved via the lease, not a guess" "$(cat "$STATE/quota-watch.out")"
fi
if grep -qF "wind-down delivered to $S2:1 (resolution: lease)" "$STATE/quota-watch.out"; then
  ok "the delivery log itself is tagged with HOW the target was found"
else
  bad "the delivery log itself is tagged with HOW the target was found" "$(cat "$STATE/quota-watch.out")"
fi
if [ ! -s "$NLOG1" ]; then
  ok "a confident lease-based resolution pages nobody"
else
  bad "a confident lease-based resolution pages nobody" "$(cat "$NLOG1")"
fi

# --- case 2: MUTATION CHECK -- with the lease consultation reverted, the --
#             SAME fixture reproduces #662's own incident: the stale ------
#             same-named leftover wins ------------------------------------
MUTANT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/qw662-mutant.XXXXXX")"
MUTATED="$MUTANT_DIR/quota-watch.sh"
cp -R "$HERE/../../scripts/supervisor/." "$MUTANT_DIR/"
rm -rf "$MUTANT_DIR/__pycache__"
chmod +x "$MUTANT_DIR"/*.sh
patch_rc=0
python3 - "$MUTATED" <<'PY' || patch_rc=$?
import sys
target = sys.argv[1]
text = open(target).read()
marker = '''  local lease_json lease_pid resolved
  if lease_json=$("$LEDGER_PYTHON" "$LEDGER_CLI" --state-dir "$STATE" supervisor-lease 2>/dev/null) \\
    && grep -qF '"held":true' <<<"$lease_json"; then
    lease_pid=$(sed -n 's/.*"owner":"[^:"]*:\\([0-9]*\\)".*/\\1/p' <<<"$lease_json" | head -1)
    if [ -n "$lease_pid" ] && resolved=$(pane_for_pid "$lease_pid"); then
      log "configured target $TARGET is gone (tmux renumbered across a restart); resolved to $resolved via the supervisor lease (pid $lease_pid) -- not a name guess"
      TARGET="$resolved"
      RESOLUTION_KIND="lease"
      return 0
    fi
  fi

'''
assert text.count(marker) == 1, "lease-consultation block not found or not unique -- script shape changed"
text = text.replace(marker, "  local session_unused resolved\n", 1)
open(target, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a mutant copy of quota-watch.sh with lease consultation reverted" \
    "could not patch $MUTATED (exit $patch_rc)"
else
  ok "setup: patched a mutant copy of quota-watch.sh with lease consultation reverted"
  bash -n "$MUTATED" || { bad "mutant quota-watch.sh is still valid bash" "syntax error"; }

  # Fresh state, same fixture, same lease -- only the SCRIPT under test
  # changed, to isolate the mutation to exactly the reverted mechanism.
  STATE2="$(mktemp -d "${TMPDIR:-/tmp}/qw662-state2.XXXXXX")"
  cp "$HERE/stubs/notify-quota-watch" "$STATE2/notify.sh"; chmod +x "$STATE2/notify.sh"
  cp "$STATE/gate-winddown" "$STATE2/gate-winddown"
  python3 "$CLI" --state-dir "$STATE2" take-supervisor-lease --owner-pid "$LEASE_PID" >/dev/null

  # Panes are REUSED from case 1 (S2's already carries case 1's own
  # delivery) -- a fresh process tree per case is not worth the setup cost
  # here, so this compares COUNTS before/after rather than presence/absence,
  # which stays correct regardless of what case 1 already left on screen.
  s1_before=$(grep -cF "QUOTA IS LOW" <<<"$(tmux capture-pane -p -S - -t "=$S1:1" 2>/dev/null)")
  s2_before=$(grep -cF "QUOTA IS LOW" <<<"$(tmux capture-pane -p -S - -t "=$S2:1" 2>/dev/null)")

  NLOG2="$(mktemp "${TMPDIR:-/tmp}/qw662-notify2.XXXXXX")"
  SUPERVISOR_STATE="$STATE2" QUOTA_GATE="$STATE2/gate-winddown" \
    QUOTA_WATCH_TARGET="$GONE_TARGET" QUOTA_WATCH_NOTIFY_SCRIPT="$STATE2/notify.sh" \
    NOTIFY_LOG="$NLOG2" \
    bash "$MUTATED" --once >>"$STATE2/quota-watch.out" 2>&1

  s1_after=$(grep -cF "QUOTA IS LOW" <<<"$(tmux capture-pane -p -S - -t "=$S1:1" 2>/dev/null)")
  s2_after=$(grep -cF "QUOTA IS LOW" <<<"$(tmux capture-pane -p -S - -t "=$S2:1" 2>/dev/null)")

  if [ "$s1_after" -gt "$s1_before" ]; then
    ok "mutation confirmed: with the lease consultation reverted, the wind-down goes to the STALE leftover again (the assertion above would now be red)"
  else
    bad "mutation confirmed: with the lease consultation reverted, the wind-down goes to the STALE leftover again" \
      "S1 'QUOTA IS LOW' count before=$s1_before after=$s1_after (expected an increase)"
  fi
  if [ "$s2_after" -eq "$s2_before" ]; then
    ok "mutation confirmed: the real lease-holder's pane receives NO NEW delivery under the reverted code"
  else
    bad "mutation confirmed: the real lease-holder's pane receives NO NEW delivery under the reverted code" \
      "S2 'QUOTA IS LOW' count before=$s2_before after=$s2_after (expected unchanged)"
  fi
  # agent-estate#789: the guess path now searches every live session, not
  # only the stale-configured one -- the log line no longer names $S1
  # specifically, but still says it guessed by name (never the lease).
  if grep -q "GUESSING by window name 'supervisor' across every live session" "$STATE2/quota-watch.out"; then
    ok "mutation confirmed: the log itself says it GUESSED by name, exactly #662's own log line"
  else
    bad "mutation confirmed: the log itself says it GUESSED by name" "$(cat "$STATE2/quota-watch.out")"
  fi
fi

echo
echo "quota-watch lease target: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
