#!/bin/bash
# laneview.sh must stay a pure viewer over lanes.sh --json: same fixture
# mechanism as test_lanes.sh (agent-dotfiles#178), reused here so a renderer
# bug can't be confused with a classification bug -- lanes.sh's own tests
# already cover that.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LANEVIEW="$HERE/../../scripts/supervisor/laneview.sh"
TEXT_IMPL="$HERE/../../scripts/supervisor/laneview/text.sh"
pass=0; fail=0
ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1 — $2"; fail=$((fail+1)); }

echo "laneview.sh"

if [ ! -f "$LANEVIEW" ]; then
  echo "  SKIP no laneview.sh -- viewer adapter not installed"; exit 0
fi

# An unknown implementation name must fail loudly and name what IS available,
# never silently fall back to one.
out=$(bash "$LANEVIEW" no-such-impl fixture 2>&1); rc=$?
if [ "$rc" -ne 0 ] && grep -q 'no renderer' <<<"$out"; then
  ok "an unknown renderer name fails, not falls back"
else
  bad "an unknown renderer name fails, not falls back" "rc=$rc out=$out"
fi

# End-to-end against the same stub tmux and fixture mechanism test_lanes.sh
# uses, so this exercises the real lanes.sh --json -> laneview.sh -> text.sh
# path, not a hand-built json blob.
D=$(mktemp -d); mkdir -p "$D/bin"
cp "$HERE/stubs/tmux-lanes" "$D/bin/tmux"
cp "$HERE/stubs/ps-lanes" "$D/bin/ps"
cat > "$D/fixture" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
15|w-real-free|claude.exe|⏵⏵ bypass permissions on (shift+tab to cycle) · ← 1 agent|1|0
2|w-busy|claude.exe|esc to interrupt 3s|1|0
FIX

out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" bash "$LANEVIEW" text fixture 2>&1)
if grep -qE 'w-real-free +free' <<<"$out"; then
  ok "a free lane renders as free through the full laneview.sh path"
else
  bad "a free lane renders as free through the full laneview.sh path" "$out"
fi
if grep -qE 'w-busy +busy' <<<"$out"; then
  ok "a busy lane renders as busy through the full laneview.sh path"
else
  bad "a busy lane renders as busy through the full laneview.sh path" "$out"
fi
if grep -qE 'arch +supervisor' <<<"$out"; then
  ok "the supervisor window is never rendered as a lane"
else
  bad "the supervisor window is never rendered as a lane" "$out"
fi

# text.sh itself must never touch tmux or the network -- it is the "apart"
# implementation (#178), so calling it directly with canned json and no PATH
# to a real tmux, no daemon, must still work.
out=$(PATH=/usr/bin:/bin bash "$TEXT_IMPL" demo-session \
  '[{"window":1,"name":"free-2","command":"claude.exe","state":"free"}]' 2>&1)
if grep -qE '^\s*- free-2\s+free$' <<<"$out"; then
  ok "text.sh renders with no tmux and no daemon reachable"
else
  bad "text.sh renders with no tmux and no daemon reachable" "$out"
fi

# `scrolled` (lanes.sh's copy-mode branch) reached review of #231 in neither
# renderer's map: text.sh drew it as `?`, indistinguishable from unknown, and
# opensessions.sh fell through `*) echo idle` and drew a lane dispatch.sh
# will not use as a healthy, available one. Both directions are checked here
# because the sidebar's is the dangerous one and the check is cheap.
SCROLLED_JSON='[{"window":3,"name":"w-scroll","command":"claude.exe","state":"scrolled"}]'
out=$(PATH=/usr/bin:/bin bash "$TEXT_IMPL" demo-session "$SCROLLED_JSON" 2>&1)
if grep -qE '^\s*\^ w-scroll\s+scrolled$' <<<"$out"; then
  ok "text.sh gives scrolled a glyph of its own, not unknown's"
else
  bad "text.sh gives scrolled a glyph of its own, not unknown's" "$out"
fi

OS_IMPL="$HERE/../../scripts/supervisor/laneview/opensessions.sh"
S=$(mktemp -d); mkdir -p "$S/bin"
cp "$HERE/stubs/curl-opensessions" "$S/bin/curl"
chmod +x "$S/bin/curl"

out=$(PATH="$S/bin:$PATH" CURL_STUB_LOG="$S/log1" CURL_STUB_COUNT="$S/n1" \
  bash "$OS_IMPL" demo-session "$SCROLLED_JSON" 2>&1)
if grep -q '"status": "waiting"' "$S/log1"; then
  ok "opensessions.sh maps scrolled to waiting, not idle"
else
  bad "opensessions.sh maps scrolled to waiting, not idle" "$(cat "$S/log1") out=$out"
fi

# The #231 review's blocking finding: post() used to `exit 1` from the
# right-hand side of a pipeline, killing only the subshell. One failed post
# silently skipped every remaining lane, left the sidebar showing whatever
# it had, and the script still printed a success line and exited 0.
THREE_JSON='[{"window":1,"name":"l-a","command":"claude.exe","state":"free"},
{"window":2,"name":"l-b","command":"claude.exe","state":"busy"},
{"window":3,"name":"l-c","command":"claude.exe","state":"free"}]'
out=$(PATH="$S/bin:$PATH" CURL_STUB_LOG="$S/log2" CURL_STUB_COUNT="$S/n2" \
  CURL_STUB_FAIL=2 bash "$OS_IMPL" demo-session "$THREE_JSON" 2>&1); rc=$?

if [ "$rc" -ne 0 ]; then
  ok "a failed lane post makes the script exit nonzero, not the subshell"
else
  bad "a failed lane post makes the script exit nonzero, not the subshell" "rc=$rc out=$out"
fi
if ! grep -q 'pushed 3 lane' <<<"$out"; then
  ok "a partial push never reports the lane count it generated as pushed"
else
  bad "a partial push never reports the lane count it generated as pushed" "$out"
fi
if [ "$(grep -c '/api/agent-event' "$S/log2")" = "3" ]; then
  ok "a failed post does not silently skip the remaining lanes"
else
  bad "a failed post does not silently skip the remaining lanes" "$(cat "$S/log2")"
fi
if grep -q 'NOT current' "$S/log2"; then
  ok "a partial push overwrites the status line rather than leaving it stale"
else
  bad "a partial push overwrites the status line rather than leaving it stale" "$(cat "$S/log2")"
fi

# Control: with every post accepted, the count is of posts the daemon took.
out=$(PATH="$S/bin:$PATH" CURL_STUB_LOG="$S/log3" CURL_STUB_COUNT="$S/n3" \
  bash "$OS_IMPL" demo-session "$THREE_JSON" 2>&1); rc=$?
if [ "$rc" -eq 0 ] && grep -q 'pushed 3 lane(s)' <<<"$out"; then
  ok "a fully accepted push reports the count and exits zero"
else
  bad "a fully accepted push reports the count and exits zero" "rc=$rc out=$out"
fi

# tui.sh -- jonhill90/agent-supervisor#7's "TUI Jon owns". curses needs a
# real tty, which no test harness has, so what is exercised here is exactly
# what any headless caller (this suite, a CI runner, `laneview.sh tui
# session > file`) actually gets: the static-frame fallback. It must still
# obey the same contract text.sh does -- no tmux, no daemon, name every
# state.
TUI_IMPL="$HERE/../../scripts/supervisor/laneview/tui.sh"

out=$(PATH=/usr/bin:/bin bash "$TUI_IMPL" demo-session \
  '[{"window":1,"name":"free-2","command":"claude.exe","state":"free"}]' </dev/null 2>&1)
if grep -qE '^\s*- free-2\s+free$' <<<"$out"; then
  ok "tui.sh renders with no tmux, no daemon, and no tty reachable"
else
  bad "tui.sh renders with no tmux, no daemon, and no tty reachable" "$out"
fi
if grep -qE '^\s*digest: ' <<<"$out"; then
  ok "tui.sh's static frame carries a digest header line"
else
  bad "tui.sh's static frame carries a digest header line" "$out"
fi

out=$(PATH=/usr/bin:/bin bash "$TUI_IMPL" demo-session "$SCROLLED_JSON" </dev/null 2>&1)
if grep -qE '^\s*\^ w-scroll\s+scrolled$' <<<"$out"; then
  ok "tui.sh gives scrolled a glyph of its own, not unknown's"
else
  bad "tui.sh gives scrolled a glyph of its own, not unknown's" "$out"
fi

# agent-dotfiles#67: the header comes from digest.sh --json, the same
# adapter boundary as lanes.sh -- structured output in, render out, never
# digest.sh's human-readable text form. A digest.sh that cannot even be run
# degrades the header to "unavailable", not a crash, and never takes over
# the lane table the way an unreachable lanes.sh does (rule 2 distinguishes
# the two: only the table's own backend gets the full-screen banner).
out=$(PATH=/usr/bin:/bin LANEVIEW_TUI_DIGEST_SH=/no/such/digest.sh \
  bash "$TUI_IMPL" demo-session \
  '[{"window":1,"name":"free-2","command":"claude.exe","state":"free"}]' </dev/null 2>&1)
if grep -qE '^\s*digest: unavailable --' <<<"$out" && grep -qE '^\s*- free-2\s+free$' <<<"$out"; then
  ok "an unreachable digest.sh degrades only the header, not the lane table"
else
  bad "an unreachable digest.sh degrades only the header, not the lane table" "$out"
fi

# A digest.sh that runs and produces a real (even error-carrying) JSON body
# is read through the JSON, not gated on its own exit code -- digest.sh
# exits nonzero on ANY noted error (e.g. an unreadable watchdog.status),
# which is not the same claim as "the header could not be built at all".
D_STUB="$D/bin/digest-stub.sh"
cat > "$D_STUB" <<'STUB'
#!/bin/bash
printf '%s\n' '{"prs":[{"verdict":"approved"},{"verdict":"none"}],"watchdog":{"state":"ok"},"poller":{"alive":true,"state":"ok"},"errors":["watchdog.status unreadable"],"ok":false}'
exit 1
STUB
chmod +x "$D_STUB"
out=$(PATH=/usr/bin:/bin LANEVIEW_TUI_DIGEST_SH="$D_STUB" \
  bash "$TUI_IMPL" demo-session \
  '[{"window":1,"name":"free-2","command":"claude.exe","state":"free"}]' </dev/null 2>&1)
if grep -qE '^\s*digest: prs=2 \(approved=1 none=1\)\s+watchdog=ok\s+poller=alive:ok\s+\[1 digest error\(s\)\]$' <<<"$out"; then
  ok "a digest.sh that exits nonzero but emits valid JSON still renders its data, not unavailable"
else
  bad "a digest.sh that exits nonzero but emits valid JSON still renders its data, not unavailable" "$out"
fi

# Through the full laneview.sh path, same fixture test_lanes.sh and the
# text.sh cases above use.
out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" bash "$LANEVIEW" tui fixture </dev/null 2>&1)
if grep -qE 'w-real-free +free' <<<"$out"; then
  ok "a free lane renders as free through the full laneview.sh tui path"
else
  bad "a free lane renders as free through the full laneview.sh tui path" "$out"
fi
if grep -qE 'arch +supervisor' <<<"$out"; then
  ok "the supervisor window is never rendered as a lane by tui.sh"
else
  bad "the supervisor window is never rendered as a lane by tui.sh" "$out"
fi

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
