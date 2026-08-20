#!/usr/bin/env bash
# Tests for prior-attempts.sh.
#
# The load-bearing cases are the two that look identical from outside and mean
# opposite things: "this issue has never been worked" (exit 1) and "I cannot see
# the results at all" (exit 3). This estate has repeatedly read the second as
# the first, so those assertions come before the happy path.
#
# Discovered automatically by tests/supervisor/test_shell_suites.py, which globs
# test_*.sh. No registration needed.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PA="$HERE/../../scripts/supervisor/prior-attempts.sh"
pass=0; fail=0
ok()  { printf '  ok   %s\n' "$*"; pass=$((pass+1)); }
bad() { printf '  FAIL %s\n' "$*"; fail=$((fail+1)); }
check() { if [ "$2" = "$3" ]; then ok "$1 (rc=$3)"; else bad "$1 (want $2, got $3)"; fi; }

[ -x "$PA" ] || { echo "FATAL: not executable: $PA"; exit 1; }

S="$(mktemp -d)"; trap 'rm -rf "$S"' EXIT
R="$S/results"; mkdir -p "$R"
mk() { printf '# result %s\n\nsome finding worth reading back.\n' "$1" > "$R/$1.md"; }
mk as284-first; sleep 1; mk as284-second; mk as999-only; mk ad100-other

run() { PRIOR_ATTEMPTS_RESULTS="$R" bash "$PA" "$@" >/dev/null 2>&1; echo $?; }

echo "== BLINDNESS IS NOT ABSENCE (the reason this file exists) =="
check "missing results dir -> 3" 3 "$(PRIOR_ATTEMPTS_RESULTS=/definitely/not/here bash "$PA" --issue 284 >/dev/null 2>&1; echo $?)"
EMPTY="$S/empty"; mkdir -p "$EMPTY"
check "empty results dir -> 3, not 1" 3 "$(PRIOR_ATTEMPTS_RESULTS="$EMPTY" bash "$PA" --issue 284 >/dev/null 2>&1; echo $?)"

echo "== found vs genuinely fresh =="
check "issue with 2 prior attempts -> 0" 0 "$(run --issue 284)"
check "issue never worked -> 1"          1 "$(run --issue 77777)"
check "prefixed form accepted"           0 "$(run --issue as284)"
check "other-repo prefix honoured"       0 "$(run --issue 100 --prefix ad)"

echo "== it must not match a DIFFERENT issue that shares a numeric prefix =="
# as28 must not match as284's files: a substring match here would silently
# attach the wrong history to a brief, which is worse than attaching none.
check "as28 does not match as284" 1 "$(run --issue 28)"

echo "== content: newest first, and it names the files rather than summarising =="
out="$(PRIOR_ATTEMPTS_RESULTS="$R" bash "$PA" --issue 284 2>/dev/null)"
first_listed="$(printf '%s' "$out" | sed -n '2p')"
case "$first_listed" in
  *as284-second*) ok "newest attempt listed first" ;;
  *) bad "newest-first ordering wrong: $first_listed" ;;
esac
case "$out" in
  *"$R/as284-first.md"*) ok "names the file path so the agent can read it" ;;
  *) bad "did not name the prior result path" ;;
esac

echo "== --brief emits an appendable section =="
b="$(PRIOR_ATTEMPTS_RESULTS="$R" bash "$PA" --issue 284 --brief 2>/dev/null)"
case "$b" in
  *"PRIOR ATTEMPTS ON THIS ISSUE"*) ok "brief section has its heading" ;;
  *) bad "brief section missing heading" ;;
esac
case "$b" in
  *"re-measure the blocker"*) ok "brief tells the lane blockers expire" ;;
  *) bad "brief does not warn about inherited blockers" ;;
esac
# A fresh issue must emit NOTHING appendable -- an empty section in a brief
# reads as "no history exists", which is a claim, not an absence.
b2="$(PRIOR_ATTEMPTS_RESULTS="$R" bash "$PA" --issue 77777 --brief 2>/dev/null)"
[ -z "${b2// }" ] && ok "fresh issue emits no brief section" || bad "fresh issue emitted text"

echo "== A REAPER STAMP MUST NOT READ AS A FAILED ATTEMPT =="
# reconcile-lane-completions overwrites a lane's report with "failed, not
# completed" when the lane goes quiet. Measured 2026-08-20: 133 of 817 results
# are stamps; 31 of those name a PR in their lane-log. Feeding one to the next
# agent unflagged teaches it the work failed when the work shipped.
mkdir -p "$S/lane-logs"
printf 'reconcile-lane-completions: as555-x has no observable pane ... failed, not completed\n' > "$R/as555-x.md"
printf -- '--- dispatched ---\nopened https://github.com/jonhill90/agent-supervisor/pull/999\n' > "$S/lane-logs/as555-x.log"
stamped="$(SUPERVISOR_STATE="$S" PRIOR_ATTEMPTS_RESULTS="$R" bash "$PA" --issue 555 --brief 2>/dev/null)"
case "$stamped" in
  *"REAPER STAMP"*) ok "stamped result is flagged, not presented as a real attempt" ;;
  *) bad "stamped result presented as if it were the lane's own report" ;;
esac
case "$stamped" in
  *"pull/999"*) ok "surfaces the PR the stamp buried" ;;
  *) bad "did not surface the PR named in the lane-log" ;;
esac
# and a genuine report must NOT be flagged
case "$(SUPERVISOR_STATE="$S" PRIOR_ATTEMPTS_RESULTS="$R" bash "$PA" --issue 999 --brief 2>/dev/null)" in
  *"REAPER STAMP"*) bad "flagged a genuine report as a stamp (false positive)" ;;
  *) ok "genuine report not flagged" ;;
esac

echo "== usage errors are 2, distinct from every real verdict =="
check "no --issue"      2 "$(PRIOR_ATTEMPTS_RESULTS="$R" bash "$PA" >/dev/null 2>&1; echo $?)"
check "non-numeric"     2 "$(run --issue 'not-an-issue')"
check "bad --max"       2 "$(run --issue 284 --max abc)"

echo "== ANTI-ORPHAN: a mechanism ships with its caller =="
callers="$(grep -rl 'prior-attempts.sh' "$HERE/../../scripts" 2>/dev/null | grep -v 'prior-attempts.sh$' | wc -l | tr -d ' ')"
if [ "$callers" -ge 1 ]; then
  ok "prior-attempts.sh has $callers caller(s) outside itself"
else
  bad "ZERO callers -- the acp_transport.py shape this estate has shipped five times"
fi

echo
echo "passed=$pass failed=$fail"
[ "$fail" -eq 0 ]
