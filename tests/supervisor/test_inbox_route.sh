#!/bin/bash
# inbox-route.sh must deliver Jon's Telegram reply to the one lane that is
# actually waiting on it, and must never guess when that is ambiguous.
#
# agent-dotfiles#142. Routing is built on lanes.sh's real `blocked` state
# (#123/#124), not a separate table, so this drives the REAL lanes.sh and
# notify.sh through the same tmux/curl stubs the rest of the suite uses --
# nothing about routing itself is reimplemented or mocked away here.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROUTE="$HERE/../../scripts/supervisor/inbox-route.sh"
pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

echo "inbox-route.sh"

D=$(mktemp -d); mkdir -p "$D/bin" "$D/state/.local/state/agent-dotfiles-supervisor"
cp "$HERE/stubs/tmux-dispatch" "$D/bin/tmux"

# Mutants below have to live next to the real scripts, not in $D: inbox-route.sh
# resolves lanes.sh/input-box.sh/notify.sh relative to its OWN path
# (`$HERE/lanes.sh` etc. inside the script, unrelated to this test's $HERE), so
# a mutated copy dropped in a bare tmp dir fails to source its siblings and the
# test would be asserting on a script that never ran, not on the mutation.
ROUTE_DIR="$(cd "$(dirname "$ROUTE")" && pwd)"
trap 'rm -f "$ROUTE_DIR"/.inbox-route-mutant-*.sh' EXIT

# notify.sh's real caller gate + Telegram send, with curl stubbed so no test
# touches the network -- same technique as test_notify.sh.
cat > "$D/bin/curl" <<'EOF'
#!/bin/bash
echo "curl $*" >> "${CURL_LOG:-/dev/null}"
exit 0
EOF
chmod +x "$D/bin/curl"
cat > "$D/notify.env" <<'EOF'
AGENT_NOTIFY_TELEGRAM_TOKEN=fake-token
AGENT_NOTIFY_TELEGRAM_CHAT_ID=fake-chat
EOF

run() {  # run <lanes-fixture-file> <message...>
  local fixture="$1"; shift
  : > "$D/tmux.log"; rm -rf "$D/panes"; mkdir -p "$D/panes"
  : > "$D/curl.log"
  PATH="$D/bin:$PATH" LANES_FIXTURE="$fixture" LANES_SESSION=t \
    TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" \
    HOME="$D/state" NOTIFY_ENV="$D/notify.env" CURL_LOG="$D/curl.log" \
    bash "$ROUTE" "$@" t
}

# --- exactly one blocked lane, TEXT-blocked: unambiguous, deliver there ----
# agent-dotfiles#159: this fixture used to model a MENU (a bash-permission
# approval, `Do you want to proceed? / 1. Yes / 2. No`) and still asserted
# that a free-text reply was delivered into it -- which is the bug #159 is
# about. It is retexted here to the estate's modelled text-blocked shape
# (lanes.sh's w-text-blocked fixture) so the tests below it -- which are
# about DELIVERY MECHANICS (literal keys, Enter-as-its-own-call, evidenced
# `delivered`) -- keep exercising the code path where delivery is supposed
# to happen. The menu-refusal path gets its own fixture and tests below.
cat > "$D/one-blocked" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
2|ad99-thing|claude.exe|Which environment should I target? Type the name, or press Esc to cancel|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
out=$(run "$D/one-blocked" "yes")
rc=$?
[ "$rc" -eq 0 ] && ok "exactly one blocked lane: exits 0" || bad "exited $rc" "$out"
# DELIVERY -- the reply reaching the lane -- which is the feature. Not "send-
# keys was called", which exits 0 whether or not the lane ever saw the text.
#
# Read from <pane>.submitted, not from the input box. Enter SUBMITS (#141): it
# empties the box, so after a successful delivery the box is EMPTY and the
# submitted text is what the lane received. A test that looks for the reply
# still sitting in the box is asserting the failure shape #141 is about --
# text typed, Enter sent, nothing run. Both halves are asserted below, because
# after #141 they are different claims.
#
# Diagnostics are dumped on failure rather than left bare: this assertion
# failed on CI reading only `cat $D/panes/2`, which printed an empty line and
# said nothing about why.
pane_diag() {
  echo "--- route output ---"; echo "$out"
  echo "--- input box (\$D/panes/2) ---"; cat "$D/panes/2" 2>&1
  echo "--- submitted (\$D/panes/2.submitted) ---"; cat "$D/panes/2.submitted" 2>&1
  echo "--- keys the pane acted on (\$D/panes/2.keys) ---"; cat "$D/panes/2.keys" 2>&1
  echo "--- files in \$D/panes ---"; ls -la "$D/panes" 2>&1
  echo "--- what was sent (\$D/tmux.log) ---"; cat "$D/tmux.log" 2>&1
}
grep -qx 'yes' "$D/panes/2.submitted" 2>/dev/null \
  && ok "the reply reaches the blocked lane's pane and is submitted" \
  || bad "the reply was never submitted to pane 2" "$(pane_diag)"
[ -s "$D/panes/2" ] \
  && bad "the reply is still sitting unsent in pane 2's input box" "$(pane_diag)" \
  || ok "the input box emptied -- the reply was submitted, not left unsent"
[ -s "$D/curl.log" ] && bad "notify.sh was called even though delivery succeeded" "$(cat "$D/curl.log")" \
  || ok "no Telegram notification sent when delivery succeeds"

# --- exactly one blocked lane, MENU-blocked: refuse, tell Jon the menu -----
# agent-dotfiles#159. This is the reproduction from the issue, verbatim: a
# lane sitting on a bash tool-permission approval (real capture, v2.1.220)
# is the commonest blocking event this estate hits, and it is a MENU. Routing
# a free-text reply into it used to be "delivered" -- the text was consumed
# as navigation keys and the trailing Enter committed whatever option was
# already highlighted. The fix must never call send-keys with the reply at
# all here, not just avoid claiming success afterward.
cat > "$D/menu-blocked" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
2|ad42-approve|claude.exe|Do you want to proceed?\n❯ 1. Yes\n  2. No\n Esc to cancel · Tab to amend · ctrl+e to explain|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
out=$(run "$D/menu-blocked" "yes")
rc=$?
[ "$rc" -eq 0 ] && ok "menu-blocked lane: exits 0 (Jon was told)" || bad "exited $rc" "$out"
[ ! -s "$D/panes/2.submitted" ] && ok "the reply was never submitted to the menu-blocked lane" \
  || bad "the reply landed in the menu-blocked lane's pane" "$(cat "$D/panes/2.submitted")"
[ ! -s "$D/panes/2" ] && ok "nothing was even typed into the menu-blocked lane's input box" \
  || bad "characters were typed into the menu-blocked lane's input box" "$(cat "$D/panes/2")"
grep -q '^send-keys .*t:2' "$D/tmux.log" 2>/dev/null \
  && bad "send-keys was called against the menu-blocked lane at all" "$(cat "$D/tmux.log")" \
  || ok "inbox-route.sh never called send-keys against the menu-blocked lane"
[ -s "$D/curl.log" ] && ok "Jon is notified instead of the reply being delivered" \
  || bad "no notify.sh call for the menu-blocked lane" "$out"
grep -q 'Do you want to proceed' "$D/curl.log" 2>/dev/null \
  && ok "Jon is told what the menu says, not just that it exists" \
  || bad "the menu's own text was not relayed to Jon" "$(cat "$D/curl.log")"

# --- #159 mutation-check 3: the stub must tell a menu apart from a text
# buffer, or the safety above is unfalsifiable ------------------------------
# Bypasses inbox-route.sh and drives the stub directly, simulating exactly
# what the OLD, buggy code did: send-keys -l "yes" then Enter, at a window
# the fixture models as a menu (stubs/tmux-dispatch's STUB_MENU_PANES). This
# is the load-bearing test change #159 calls for -- a stub that models every
# pane as an append-only text buffer cannot express this bug, so an
# assertion built on it would pass whether or not the real code is safe.
: > "$D/tmux.log"; rm -rf "$D/panes"; mkdir -p "$D/panes"
PATH="$D/bin:$PATH" LANES_FIXTURE="$D/menu-blocked" TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" \
  STUB_MENU_PANES="2" tmux send-keys -l -t t:2 "yes" >/dev/null 2>&1
PATH="$D/bin:$PATH" LANES_FIXTURE="$D/menu-blocked" TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" \
  STUB_MENU_PANES="2" tmux send-keys -t t:2 Enter >/dev/null 2>&1
[ ! -s "$D/panes/2.submitted" ] && ok "the stub models a menu distinctly: a naive send never lands as submitted text" \
  || bad "the stub let a naive send land as submitted text even in menu mode" "$(cat "$D/panes/2.submitted")"
grep -qx '1' "$D/panes/2.selected" 2>/dev/null && ok "Enter committed the highlighted option, not the typed reply" \
  || bad "the menu's committed selection was not recorded as expected" "$(cat "$D/panes/2.selected" 2>&1)"

# Mutation-check 3, verbatim: revert the stub's menu-modeling and repeat the
# SAME naive send. If the assertions above still read the same way with the
# distinction turned off, the stub was never actually distinguishing
# anything -- confirm they flip.
: > "$D/tmux.log"; rm -rf "$D/panes"; mkdir -p "$D/panes"
PATH="$D/bin:$PATH" LANES_FIXTURE="$D/menu-blocked" TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" \
  STUB_MENU_PANES="2" STUB_MENU_AS_TEXT=1 tmux send-keys -l -t t:2 "yes" >/dev/null 2>&1
PATH="$D/bin:$PATH" LANES_FIXTURE="$D/menu-blocked" TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" \
  STUB_MENU_PANES="2" STUB_MENU_AS_TEXT=1 tmux send-keys -t t:2 Enter >/dev/null 2>&1
if grep -qx 'yes' "$D/panes/2.submitted" 2>/dev/null; then
  ok "mutation confirmed: with the stub reverted to a plain text buffer, the same naive send silently looks like a normal delivery (the assertions above would be red)"
else
  bad "mutation confirmed: reverting the stub to a plain text buffer" \
    "expected 'yes' in panes/2.submitted once the menu-distinction is off, got: $(cat "$D/panes/2.submitted" 2>&1)"
fi

# --- zero blocked lanes: ask Jon rather than dropping it -------------------
cat > "$D/zero-blocked" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
2|free-2|claude.exe|❯ ready|1|0
3|ad50-review|claude.exe|esc to interrupt 3s|1|0
FIX
out=$(run "$D/zero-blocked" "yes")
rc=$?
[ "$rc" -eq 0 ] && ok "zero blocked lanes: still exits 0 (Jon was told)" || bad "exited $rc" "$out"
[ -s "$D/curl.log" ] && ok "zero blocked lanes notifies Jon through notify.sh" \
  || bad "no notify.sh call for zero blocked lanes"
[ -z "$(ls "$D/panes" 2>/dev/null)" ] && ok "nothing was sent to any pane" \
  || bad "a pane received the message despite zero blocked lanes" "$(ls "$D/panes")"

# --- several blocked lanes: ask which, rather than guess --------------------
cat > "$D/two-blocked" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
2|ad10-a|claude.exe|Do you want to proceed?\n❯ 1. Yes\n  2. No\n Esc to cancel|1|0
3|ad20-b|claude.exe|Enter to select · Esc to cancel|1|0
FIX
out=$(run "$D/two-blocked" "yes")
rc=$?
[ "$rc" -eq 0 ] && ok "several blocked lanes: exits 0 (Jon was asked)" || bad "exited $rc" "$out"
[ -s "$D/curl.log" ] && ok "several blocked lanes notifies Jon to disambiguate" \
  || bad "no notify.sh call for several blocked lanes"
[ -z "$(ls "$D/panes" 2>/dev/null)" ] && ok "nothing was guessed into any pane" \
  || bad "a pane received the message despite ambiguity" "$(ls "$D/panes")"

# --- a message that IS a tmux key name is sent literally, not as a key -----
# agent-dotfiles#152. Without `-l`, `tmux send-keys` interprets an argument
# that matches a real key name as that key, not as text -- a reply of
# literally `C-c` fires SIGINT at the lane instead of typing "C-c", and
# `C-u` would silently wipe whatever the lane had already typed. The stub
# (tests/supervisor/stubs/tmux-dispatch) now models this: without `-l` a
# recognised key name never reaches the pane buffer at all, it is logged to
# <pane>.keys instead -- so this assertion can actually distinguish a
# literal-safe send from an unsafe one, which the old stub could not.
for key in "C-c" "Escape" "C-u"; do
  out=$(run "$D/one-blocked" "$key")
  rc=$?
  [ "$rc" -eq 0 ] && ok "key-name reply ($key): exits 0" || bad "exited $rc" "$out"
  pane="$D/panes/2"
  # Read the SUBMITTED text, not the box: the Enter that follows the message
  # empties the box (#141), so by the time this looks the box is correctly
  # blank and the submitted record is where the typed text now lives.
  if [ "$key" = "C-u" ]; then
    # C-u's real action is "clear the buffer" -- sent literally it must
    # instead APPEAR in the buffer as the two characters C and u (well,
    # the text "C-u"), not clear it.
    grep -qF "$key" "$pane.submitted" 2>/dev/null && ok "C-u reply lands as literal text, not the clear action" \
      || bad "pane 2 never submitted the literal text \"$key\"" "$(pane_diag)"
  else
    grep -qF "$key" "$pane.submitted" 2>/dev/null && ok "$key reply lands as literal text in the pane" \
      || bad "pane 2 never submitted the literal text \"$key\"" "$(pane_diag)"
  fi
  [ -s "$pane.keys" ] && grep -q "^$key\$" "$pane.keys" 2>/dev/null \
    && bad "$key fired as a real key action instead of being typed literally" "$(cat "$pane.keys")" \
    || ok "$key was never interpreted as a key action"
done

# --- Enter is sent as its own key, not merely present in the buffer --------
# agent-dotfiles#152 finding 3: the old suite never checked Enter was
# actually sent -- deleting the Enter send-keys call still passed 9/9. The
# stub now logs every key it receives (Enter included, via `.keys` files
# for the literal case above and the same `.keys` log in general below)
# to <pane>.keys; assert on that rather than only on the buffer contents.
out=$(run "$D/one-blocked" "yes")
rc=$?
[ "$rc" -eq 0 ] && ok "plain reply: exits 0" || bad "exited $rc" "$out"
grep -qx 'yes' "$D/panes/2.submitted" 2>/dev/null && ok "the plain reply lands in the blocked lane's pane" \
  || bad "pane 2 never submitted the reply" "$(pane_diag)"
grep -q 'send-keys -t t:2 Enter$' "$D/tmux.log" 2>/dev/null && ok "Enter was sent as its own key after the message" \
  || bad "no separate Enter send-keys call in the log" "$(cat "$D/tmux.log" 2>/dev/null)"
grep -qx 'Enter' "$D/panes/2.keys" 2>/dev/null && ok "the pane acted on the Enter, it was not merely sent at it" \
  || bad "pane 2 never acted on an Enter" "$(pane_diag)"

# --- prove the delivery assertions can go red ------------------------------
# The assertion that broke on CI is the one that matters: `delivered` must mean
# the reply reached the lane, not that send-keys returned 0. A delivery check
# that cannot be turned red by breaking delivery is not checking delivery, so
# multiple ways of breaking it are exercised here rather than assumed.

# (a) delivery removed outright: the literal send never happens.
MUTANT="$ROUTE_DIR/.inbox-route-mutant-nosend.sh"
patch_rc=0
python3 - "$ROUTE" "$MUTANT" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''    if ! tmux send-keys -l -t "$LANE" "$MESSAGE" 2>/dev/null; then'''
assert marker in text, "literal send-keys call not found -- inbox-route.sh shape changed"
assert text.count(marker) == 1, "literal send-keys call not unique -- inbox-route.sh shape changed"
open(dst, "w").write(text.replace(marker, '''    if ! true; then''', 1))
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a delivery-free copy of inbox-route.sh" \
    "could not patch $ROUTE (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a delivery-free copy of inbox-route.sh"
  : > "$D/tmux.log"; rm -rf "$D/panes"; mkdir -p "$D/panes"; : > "$D/curl.log"
  out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/one-blocked" LANES_SESSION=t \
    TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" \
    HOME="$D/state" NOTIFY_ENV="$D/notify.env" CURL_LOG="$D/curl.log" \
    bash "$MUTANT" "yes" t 2>&1)
  if [ ! -s "$D/panes/2.submitted" ]; then
    ok "mutation confirmed: with delivery removed nothing is submitted (the arrival assertions above would be red)"
  else
    bad "mutation confirmed: with delivery removed nothing is submitted" \
      "something was still submitted: $(cat "$D/panes/2.submitted")"
  fi
  # #159 test 4: even under this mutation, the fix's OWN pane-evidence check
  # (input_box_state reading something other than `text` here, since nothing
  # was typed) refuses to claim delivered -- this is not a skip, it is the
  # requirement working as designed.
  ! grep -q '^inbox-route: delivered' <<<"$out" \
    && ok "inbox-route.sh does not claim delivered when nothing was sent" \
    || bad "inbox-route.sh claimed delivered with nothing sent" "$out"
fi

# (b) the #141 failure shape: the message is typed but the Enter that follows
#     is swallowed by a repainting harness. The reply sits in the box,
#     nothing runs, and -- with the REAL, unmutated script -- delivery must
#     not be claimed.
: > "$D/tmux.log"; rm -rf "$D/panes"; mkdir -p "$D/panes"; : > "$D/curl.log"
out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/one-blocked" LANES_SESSION=t \
  TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" DISPATCH_SWALLOW_ENTER=1 \
  HOME="$D/state" NOTIFY_ENV="$D/notify.env" CURL_LOG="$D/curl.log" \
  bash "$ROUTE" "yes" t 2>&1)
if [ ! -s "$D/panes/2.submitted" ] && grep -qx 'yes' "$D/panes/2" 2>/dev/null; then
  ok "a swallowed Enter leaves the reply unsent in the box"
else
  bad "a swallowed Enter leaves the reply unsent in the box" \
    "box='$(cat "$D/panes/2" 2>/dev/null)' submitted='$(cat "$D/panes/2.submitted" 2>/dev/null)'"
fi
! grep -q '^inbox-route: delivered' <<<"$out" \
  && ok "inbox-route.sh does not claim delivered when Enter was swallowed" \
  || bad "inbox-route.sh claimed delivered with a swallowed Enter" "$out"
[ -s "$D/curl.log" ] && ok "Jon is notified when a swallowed Enter leaves the reply unconfirmed" \
  || bad "no notify.sh call when a swallowed Enter leaves the reply unconfirmed" "$out"

# (c) #159 mutation-check 4: patch OUT the pane-evidence verification itself
# (both input_box_state checks) while leaving the real send-keys calls in
# place, and confirm that with DISPATCH_SWALLOW_ENTER=1 -- a reply that never
# actually submitted -- this reverted script DOES claim delivered. That is
# the exact bug requirement 4 is about, reproduced on demand, and it proves
# the verification above is what stands between the estate and it, not
# incidental to it.
MUTANT2="$ROUTE_DIR/.inbox-route-mutant-noverify.sh"
patch_rc=0
python3 - "$ROUTE" "$MUTANT2" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''    if [ "$(tmux capture-pane -pe -t "$LANE" 2>/dev/null | input_box_state)" != text ]; then
      echo "inbox-route: $LANE never showed the reply after typing it -- not sending Enter" >&2
      notify_jon "Telegram reply could not be confirmed" \\
        "$LANE was blocked and waiting but the reply never appeared in its input box -- reply was: $MESSAGE"
      exit 1
    fi
    if ! tmux send-keys -t "$LANE" Enter 2>/dev/null; then
      echo "inbox-route: sending Enter to $LANE failed" >&2
      notify_jon "Telegram reply could not be delivered" \\
        "$LANE was blocked and waiting but sending Enter failed -- reply was: $MESSAGE"
      exit 1
    fi
    if [ "$(tmux capture-pane -pe -t "$LANE" 2>/dev/null | input_box_state)" = empty ]; then
      echo "inbox-route: delivered to $LANE"
      exit 0
    fi'''
assert marker in text, "verification block not found -- inbox-route.sh shape changed"
assert text.count(marker) == 1, "verification block not unique -- inbox-route.sh shape changed"
replacement = '''    tmux send-keys -t "$LANE" Enter 2>/dev/null
    echo "inbox-route: delivered to $LANE"
    exit 0'''
open(dst, "w").write(text.replace(marker, replacement, 1))
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a verification-free copy of inbox-route.sh" \
    "could not patch $ROUTE (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a verification-free copy of inbox-route.sh"
  : > "$D/tmux.log"; rm -rf "$D/panes"; mkdir -p "$D/panes"; : > "$D/curl.log"
  out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/one-blocked" LANES_SESSION=t \
    TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" DISPATCH_SWALLOW_ENTER=1 \
    HOME="$D/state" NOTIFY_ENV="$D/notify.env" CURL_LOG="$D/curl.log" \
    bash "$MUTANT2" "yes" t 2>&1)
  if grep -q '^inbox-route: delivered' <<<"$out" && [ -s "$D/panes/2" ]; then
    ok "mutation confirmed: without the pane-evidence check, a swallowed Enter is wrongly reported delivered (test 4 would be red)"
  else
    bad "mutation confirmed: without the pane-evidence check, delivered is wrongly claimed" \
      "out='$out' box='$(cat "$D/panes/2" 2>/dev/null)'"
  fi
fi

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
