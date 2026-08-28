#!/bin/bash
# agent-supervisor#682, Track A, ranked item 5. Proves scripts/supervisor/
# launchd/render-plists.sh actually resolves the checkout path both
# directions (default matches the live plists byte-for-byte; overriding
# AGENT_SUPERVISOR_REPO changes every rendered ProgramArguments entry) and
# fails loudly, not with an empty --out-dir, when the resolved path isn't a
# real checkout. Never touches ~/Library/LaunchAgents or launchctl -- the
# script under test doesn't either.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RENDER="$HERE/../../scripts/supervisor/launchd/render-plists.sh"
pass=0; fail=0
say_ok()  { echo "  ok   $1"; pass=$((pass+1)); }
say_bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }

echo "render-plists.sh: the launchd entry-point path, both directions"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# --- default resolution renders all 4, valid plists -------------------------
OUT_DEFAULT="$WORK/default"
(unset AGENT_SUPERVISOR_REPO; bash "$RENDER" --out-dir "$OUT_DEFAULT" >"$WORK/default.log" 2>&1)
rc=$?
if [ "$rc" -eq 0 ]; then say_ok "default resolution exits 0"; else say_bad "default resolution exits 0" "rc=$rc, log: $(cat "$WORK/default.log")"; fi

count="$(find "$OUT_DEFAULT" -maxdepth 1 -name '*.plist' 2>/dev/null | wc -l | tr -d ' ')"
if [ "$count" = "4" ]; then say_ok "renders exactly 4 plists"; else say_bad "renders exactly 4 plists" "found $count"; fi

if command -v plutil >/dev/null 2>&1; then
  bad=0
  for f in "$OUT_DEFAULT"/*.plist; do
    plutil -lint "$f" >/dev/null 2>&1 || bad=$((bad+1))
  done
  if [ "$bad" -eq 0 ]; then say_ok "every rendered plist is valid XML (plutil -lint)"; else say_bad "every rendered plist is valid XML" "$bad invalid"; fi
else
  echo "  SKIP plutil not on PATH"
fi

# --- positive control: diff itself must be able to detect a real difference,
#     before its zero below is trusted as "matches production" ---------------
MUTATED="$WORK/mutated.plist"
cp "$OUT_DEFAULT/com.jonhill.director-loop.plist" "$MUTATED"
sed -i '' 's/900/901/' "$MUTATED"
diff -q "$OUT_DEFAULT/com.jonhill.director-loop.plist" "$MUTATED" >/dev/null 2>&1
diff_rc=$?
if [ "$diff_rc" -ne 0 ]; then say_ok "positive control: diff detects a real difference"; else say_bad "positive control: diff detects a real difference" "diff reported no difference on a deliberately mutated file"; fi

# --- default resolution matches the live plists byte-for-byte, IF this host
#     actually has them (this repo's tests may run off this machine) --------
LIVE="$HOME/Library/LaunchAgents"
if [ -d "$LIVE" ] && [ -f "$LIVE/com.jonhill.director-loop.plist" ]; then
  mismatches=0
  for name in director-loop quota-watch supervisor-heartbeat weekly-watch; do
    diff -q "$LIVE/com.jonhill.$name.plist" "$OUT_DEFAULT/com.jonhill.$name.plist" >/dev/null 2>&1 || mismatches=$((mismatches+1))
  done
  if [ "$mismatches" -eq 0 ]; then
    say_ok "default-resolved plists are byte-identical to the live 4 in ~/Library/LaunchAgents"
  else
    say_bad "default-resolved plists are byte-identical to the live 4" "$mismatches differ"
  fi
else
  echo "  SKIP no live plists on this host to diff against"
fi

# --- mutation direction 1: a different real checkout changes every rendered
#     ProgramArguments entry, not just some -----------------------------------
FAKE_REPO="$WORK/fake-agent-estate"
mkdir -p "$FAKE_REPO"
OUT_RENAMED="$WORK/renamed"
AGENT_SUPERVISOR_REPO="$FAKE_REPO" bash "$RENDER" --out-dir "$OUT_RENAMED" >"$WORK/renamed.log" 2>&1
rc=$?
if [ "$rc" -eq 0 ]; then say_ok "override resolution exits 0"; else say_bad "override resolution exits 0" "rc=$rc"; fi

hits="$(grep -l "$FAKE_REPO" "$OUT_RENAMED"/*.plist 2>/dev/null | wc -l | tr -d ' ')"
if [ "$hits" = "4" ]; then
  say_ok "overriding AGENT_SUPERVISOR_REPO changes all 4 rendered ProgramArguments paths"
else
  say_bad "overriding AGENT_SUPERVISOR_REPO changes all 4 rendered ProgramArguments paths" "only $hits/4 contain the override"
fi
stale="$(grep -l "/Users/jon/source/repos/Personal/agent-supervisor" "$OUT_RENAMED"/*.plist 2>/dev/null | wc -l | tr -d ' ')"
if [ "$stale" = "0" ]; then
  say_ok "no rendered file still names the pre-rename checkout path"
else
  say_bad "no rendered file still names the pre-rename checkout path" "$stale still do"
fi

# --- mutation direction 2: an unresolvable checkout fails LOUDLY, not with a
#     quiet empty --out-dir that reads as "nothing to do" --------------------
OUT_FATAL="$WORK/fatal"
FATAL_LOG="$WORK/fatal.log"
AGENT_SUPERVISOR_REPO="$WORK/does-not-exist-682" bash "$RENDER" --out-dir "$OUT_FATAL" >"$FATAL_LOG" 2>&1
rc=$?
if [ "$rc" -eq 1 ]; then say_ok "unresolvable checkout exits 1"; else say_bad "unresolvable checkout exits 1" "rc=$rc"; fi
if grep -q "^FATAL:" "$FATAL_LOG"; then say_ok "unresolvable checkout prints a FATAL line"; else say_bad "unresolvable checkout prints a FATAL line" "log: $(cat "$FATAL_LOG")"; fi
if [ ! -d "$OUT_FATAL" ]; then say_ok "no partial --out-dir left behind on FATAL"; else say_bad "no partial --out-dir left behind on FATAL" "$OUT_FATAL exists"; fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
