#!/bin/bash
# agent-estate#735: director-loop.sh wires the corpus-STAGING tick step
# (corpus-stage.sh) in and escalates through the SAME alarm path
# (`send_takeover_alarm` -> notify.sh) it already uses for its other
# tick-level incidents (quota/live/stale-target), rather than inventing a
# second escalation idiom -- and never on the quota gate's critical path:
# the step runs, and can escalate, even under a SAFE quota reading with no
# Director session present at all.
#
# agent-estate#771: a threshold crossing (rc=1) no longer just pages a human
# to dispatch a judging lane by hand -- it DISPATCHES one mechanically, via
# dispatch.sh, the same way any other bounded task is handed to a lane. This
# file's rc=1 coverage was rewritten for that: the alarm assertions below are
# gone (this branch no longer alarms on a plain threshold crossing) and
# replaced with dispatch-call coverage mirroring test_contest_stop.sh's own
# stub-dispatch.sh pattern. The rc=2 (instrument failure) coverage is
# unchanged -- that branch was never touched by #771.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOOP="$HERE/../../scripts/supervisor/director-loop.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_count() {
  local label="$1" needle="$2" file="$3" expect="$4"
  local got; got=$(grep -cF -- "$needle" "$file" 2>/dev/null || true)
  if [ "$got" = "$expect" ]; then ok "$label"; else bad "$label" "expected $expect of '$needle' in $file, got $got:
$(cat "$file" 2>/dev/null)"; fi
}

echo "director-loop.sh -- wires corpus-stage.sh into the tick and escalates through send_takeover_alarm (#735); dispatches a judging lane on threshold crossing (#771)"

D=$(mktemp -d)
trap 'rm -rf "$D"' EXIT

QUOTA_SAFE="$HERE/stubs/quota-safe"
cp "$HERE/stubs/notify-quota-watch" "$D/notify.sh"; chmod +x "$D/notify.sh"

mk_stub() { # mk_stub <path> <exit-code> <stdout-metrics-line>
  cat > "$1" <<STUB
#!/bin/bash
echo "$3"
exit $2
STUB
  chmod +x "$1"
}

STAGE_OK="$D/corpus-stage-ok"
mk_stub "$STAGE_OK" 0 "count=3 oldest_age_seconds=120 age_threshold_seconds=86400 count_threshold=168"

STAGE_LOUD="$D/corpus-stage-loud"
mk_stub "$STAGE_LOUD" 1 "count=4 oldest_age_seconds=345600 age_threshold_seconds=86400 count_threshold=168"

STAGE_FAILED="$D/corpus-stage-failed"
mk_stub "$STAGE_FAILED" 2 ""

# A stub dispatch.sh that records every call's arguments (one line per call,
# appended -- not overwritten -- so a two-tick test can see both calls) and
# exits with $DISPATCH_JUDGE_STUB_EXIT (default 0).
DISPATCH_STUB="$D/dispatch-judge-stub"
cat > "$DISPATCH_STUB" <<'STUB'
#!/bin/bash
printf '%s\n' "$*" >>"$DISPATCH_JUDGE_CALL_LOG"
exit "${DISPATCH_JUDGE_STUB_EXIT:-0}"
STUB
chmod +x "$DISPATCH_STUB"

tick() { # tick <corpus-stage-stub> <state-dir> <notify-log> [dispatch-stub]
  local dispatch_stub="${4:-$DISPATCH_STUB}"
  SUPERVISOR_STATE="$2" QUOTA_GATE="$QUOTA_SAFE" DIRECTOR_LOOP_TARGET="no-such-session-735:@1" \
    CORPUS_STAGE_SCRIPT="$1" DIRECTOR_LOOP_NOTIFY_SCRIPT="$D/notify.sh" NOTIFY_LOG="$3" \
    DIRECTOR_LOOP_ALARM_COOLDOWN=0 \
    CORPUS_JUDGE_DISPATCH_SCRIPT="$dispatch_stub" DISPATCH_JUDGE_CALL_LOG="$2/dispatch-call.log" \
    CORPUS_JUDGE_REPO="jonhill90/agent-estate" CORPUS_JUDGE_REPO_PATH="$D" \
    bash "$LOOP" >>"$2/out.log" 2>&1
}

# --- a clean tick under threshold never pages, never dispatches ------------
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.ok"; : > "$NLOG"
tick "$STAGE_OK" "$STATE" "$NLOG"
want_count "an under-threshold tick reports nothing to a human" "director-loop: prompt corpus backlog" "$NLOG" 0
if grep -q "count=3 oldest_age_seconds=120" "$STATE/out.log"; then
  ok "the tick's own log carries corpus-stage.sh's metrics line"
else
  bad "the tick's own log carries corpus-stage.sh's metrics line" "$(cat "$STATE/out.log")"
fi
if [ -f "$STATE/dispatch-call.log" ]; then
  bad "an under-threshold tick never dispatches a judging lane" "$(cat "$STATE/dispatch-call.log")"
else
  ok "an under-threshold tick never dispatches a judging lane"
fi

# --- a threshold-crossing tick DISPATCHES a judging lane against #771 ------
# (agent-estate#771: replaces the old "pages a human" behaviour for this case)
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.loud"; : > "$NLOG"
tick "$STAGE_LOUD" "$STATE" "$NLOG"
want_count "a threshold crossing never pages a human directly (dispatch handles it now)" \
  "director-loop: prompt corpus backlog crossed loud-absence threshold" "$NLOG" 0
call=$(cat "$STATE/dispatch-call.log" 2>/dev/null || echo "<no call recorded>")
if grep -qF "771 judge-corpus" <<<"$call"; then
  ok "dispatches issue 771 with the judge-corpus slug"
else
  bad "dispatches issue 771 with the judge-corpus slug" "$call"
fi
if grep -qF "jonhill90/agent-estate" <<<"$call"; then
  ok "dispatches into the configured repo"
else
  bad "dispatches into the configured repo" "$call"
fi
if grep -qF -- "--not-a-review" <<<"$call"; then
  ok "passes --not-a-review (a judging pass produces a comment, never a PR under review)"
else
  bad "passes --not-a-review" "$call"
fi
# the brief-file argument (3rd positional word) must exist and reference the
# staged batch + the actual metrics, not a placeholder
brief_path=$(awk '{print $3}' <<<"$call")
if [ -f "$brief_path" ]; then
  bad "the brief file is cleaned up after dispatch (no leftover under \$STATE)" "$brief_path still exists"
else
  ok "the brief file is cleaned up after dispatch (no leftover under \$STATE)"
fi
if grep -q "corpus judging lane dispatched against #771" "$STATE/out.log"; then
  ok "the tick's own log records the successful dispatch"
else
  bad "the tick's own log records the successful dispatch" "$(cat "$STATE/out.log")"
fi

# --- the tick NEVER judges itself: it must not shell out to
#     itemize_prompts.py --load, and the brief content (captured via a stub
#     that copies the brief file before exiting) must say so -----------------
CAPTURE_DISPATCH="$D/dispatch-judge-capture"
cat > "$CAPTURE_DISPATCH" <<'STUB'
#!/bin/bash
printf '%s\n' "$*" >>"$DISPATCH_JUDGE_CALL_LOG"
cp "$3" "$DISPATCH_JUDGE_BRIEF_CAPTURE"
exit "${DISPATCH_JUDGE_STUB_EXIT:-0}"
STUB
chmod +x "$CAPTURE_DISPATCH"
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.capture"; : > "$NLOG"
BRIEF_CAPTURE="$D/captured-brief.md"
SUPERVISOR_STATE="$STATE" QUOTA_GATE="$QUOTA_SAFE" DIRECTOR_LOOP_TARGET="no-such-session-735:@1" \
  CORPUS_STAGE_SCRIPT="$STAGE_LOUD" DIRECTOR_LOOP_NOTIFY_SCRIPT="$D/notify.sh" NOTIFY_LOG="$NLOG" \
  DIRECTOR_LOOP_ALARM_COOLDOWN=0 \
  CORPUS_JUDGE_DISPATCH_SCRIPT="$CAPTURE_DISPATCH" DISPATCH_JUDGE_CALL_LOG="$STATE/dispatch-call.log" \
  DISPATCH_JUDGE_BRIEF_CAPTURE="$BRIEF_CAPTURE" \
  CORPUS_JUDGE_REPO="jonhill90/agent-estate" CORPUS_JUDGE_REPO_PATH="$D" \
  bash "$LOOP" >>"$STATE/out.log" 2>&1
if grep -qF "count=4 oldest_age_seconds=345600" "$BRIEF_CAPTURE" 2>/dev/null; then
  ok "the brief carries the real gate metrics, not a placeholder"
else
  bad "the brief carries the real gate metrics" "$(cat "$BRIEF_CAPTURE" 2>&1)"
fi
if grep -qF "No PR is expected" "$BRIEF_CAPTURE" 2>/dev/null; then
  ok "the brief states this is judging, not code (no PR expected)"
else
  bad "the brief states this is judging, not code" "$(cat "$BRIEF_CAPTURE" 2>&1)"
fi
# Strip the brief heredoc (its PROSE legitimately tells the dispatched lane
# to run --load) AND comment lines (prose describing the boundary, not code)
# before checking that director-loop.sh's own EXECUTABLE code never invokes
# --load itself -- the tick must dispatch judging, never perform it.
loop_code_no_heredoc=$(awk '/cat > "\$judge_brief" <<EOF/{skip=1; next} skip && /^EOF$/{skip=0; next} !skip' "$LOOP" \
  | grep -v '^[[:space:]]*#')
if grep -qi "itemize_prompts.py --load\|itemize_prompts\.py\".*--load\|ITEMIZE.*--load" <<<"$loop_code_no_heredoc"; then
  bad "director-loop.sh itself never calls itemize_prompts.py --load" \
    "$(grep -n 'load' <<<"$loop_code_no_heredoc")"
else
  ok "director-loop.sh itself never calls itemize_prompts.py --load (the tick never judges)"
fi

# --- exactly-once dedup: a second tick while the lane is still "in-flight"
#     (dispatch.sh's own claim.sh guard, stubbed here as a refusal) must NOT
#     be treated as a failure of THIS script -- it logs and moves on --------
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.dedup"; : > "$NLOG"
tick "$STAGE_LOUD" "$STATE" "$NLOG"   # first tick: dispatch succeeds (rc=0 stub)
first_calls=$(grep -c . "$STATE/dispatch-call.log" 2>/dev/null || echo 0)
DISPATCH_JUDGE_STUB_EXIT=1 tick "$STAGE_LOUD" "$STATE" "$NLOG"  # second tick: claim already held
second_calls=$(grep -c . "$STATE/dispatch-call.log" 2>/dev/null || echo 0)
if [ "$first_calls" = "1" ] && [ "$second_calls" = "2" ]; then
  ok "both ticks attempt a dispatch (the guard against a duplicate lives in dispatch.sh/claim.sh, not a second copy here)"
else
  bad "both ticks attempt a dispatch" "first_calls=$first_calls second_calls=$second_calls"
fi
if grep -q "corpus judging dispatch refused" "$STATE/out.log"; then
  ok "a refused second dispatch (already claimed) is logged, not silently dropped"
else
  bad "a refused second dispatch is logged" "$(cat "$STATE/out.log")"
fi
if grep -q "retrying next tick, never judging inline" "$STATE/out.log"; then
  ok "the refusal message states the correct behaviour: retry next tick, never judge inline"
else
  bad "the refusal message states retry-next-tick behaviour" "$(cat "$STATE/out.log")"
fi

# --- an extract failure (instrument failure) escalates differently, and is
# never mistaken for an empty backlog, and never triggers a dispatch --------
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.failed"; : > "$NLOG"
tick "$STAGE_FAILED" "$STATE" "$NLOG"
want_count "an extract failure pages as an instrument failure" "director-loop: corpus staging failed" "$NLOG" 1
want_count "...and is never reported as a threshold escalation" "director-loop: prompt corpus backlog crossed" "$NLOG" 0
if [ -f "$STATE/dispatch-call.log" ]; then
  bad "an extract failure never dispatches a judging lane" "$(cat "$STATE/dispatch-call.log")"
else
  ok "an extract failure never dispatches a judging lane"
fi

# --- a missing corpus-stage.sh is skipped, not fatal to the tick ----------
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.missing"; : > "$NLOG"
tick "$D/no-such-corpus-stage.sh" "$STATE" "$NLOG"
if grep -q "corpus-stage: .*not found or not executable -- skipping" "$STATE/out.log"; then
  ok "a missing corpus-stage.sh is logged and skipped rather than crashing the tick"
else
  bad "a missing corpus-stage.sh is logged and skipped rather than crashing the tick" "$(cat "$STATE/out.log")"
fi
want_count "a missing corpus-stage.sh does not page" "director-loop: prompt corpus backlog" "$NLOG" 0
want_count "...nor does it page as a staging failure" "director-loop: corpus staging failed" "$NLOG" 0

# --- a missing dispatch.sh (the judge-dispatch target) is skipped, not
#     fatal, and does not crash the tick -------------------------------------
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.nodispatch"; : > "$NLOG"
tick "$STAGE_LOUD" "$STATE" "$NLOG" "$D/no-such-dispatch.sh"
if grep -q "corpus-judge: .*not found or not executable -- skipping" "$STATE/out.log"; then
  ok "a missing judge-dispatch target is logged and skipped rather than crashing the tick"
else
  bad "a missing judge-dispatch target is logged and skipped rather than crashing the tick" "$(cat "$STATE/out.log")"
fi

# --- RED: mutate the exit-code dispatch so a threshold crossing (rc=1) is
# read the same as clean (rc=0) -- proves the GREEN case above actually
# depends on the case statement, not on incidental log text ----------------
BROKEN="$D/director-loop-broken-corpus.sh"
sed 's/^    1)$/    99)/' "$LOOP" > "$BROKEN"
if grep -qF '    99)' "$BROKEN"; then
  ok "constructed a mutated copy whose rc=1 case arm is unreachable"
else
  bad "constructed a mutated copy whose rc=1 case arm is unreachable" "sed did not match -- check director-loop.sh's exact source line"
fi
chmod +x "$BROKEN"
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.red"; : > "$NLOG"
SUPERVISOR_STATE="$STATE" QUOTA_GATE="$QUOTA_SAFE" DIRECTOR_LOOP_TARGET="no-such-session-735:@1" \
  CORPUS_STAGE_SCRIPT="$STAGE_LOUD" DIRECTOR_LOOP_NOTIFY_SCRIPT="$D/notify.sh" NOTIFY_LOG="$NLOG" \
  DIRECTOR_LOOP_ALARM_COOLDOWN=0 \
  CORPUS_JUDGE_DISPATCH_SCRIPT="$DISPATCH_STUB" DISPATCH_JUDGE_CALL_LOG="$STATE/dispatch-call.log" \
  CORPUS_JUDGE_REPO="jonhill90/agent-estate" CORPUS_JUDGE_REPO_PATH="$D" \
  bash "$BROKEN" >>"$STATE/out.log" 2>&1
if [ -f "$STATE/dispatch-call.log" ]; then
  bad "RED: with rc=1 mutated to fall into the rc=0 no-op branch, the loud stub no longer dispatches" \
    "$(cat "$STATE/dispatch-call.log")"
else
  ok "RED: with rc=1 mutated to fall into the rc=0 no-op branch, the loud stub no longer dispatches"
fi

echo
echo "director-loop corpus-stage wiring: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
