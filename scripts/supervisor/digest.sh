#!/usr/bin/env bash
# One command that answers "what is the state of the estate right now".
#
# WHY: the Director reconstructs the same picture every tick from ~26 separate
# subprocess round-trips -- watchdog.status, pgrep, the poller status, lanes.sh,
# then five `gh` calls per open PR. That reasoning is identical every time and
# it is paid for in the most expensive tier in the estate.
#
# Every judgement moved out of a model and into a script is a permanent saving,
# and this is the largest remaining one: the Director's per-tick state read.
#
# The estate's own research (docs/hierarchy-naming-57.md) prices an LLM-backed
# coordination tier at 30-50% extra tokens; the answer here is not to remove the
# tier but to stop making it re-derive facts a script can hand it.
#
# Usage:
#   digest.sh              human-readable summary
#   digest.sh --json       one JSON object
#
# REQUIRES: jq. This is the first script in scripts/supervisor/ to need a
# binary the rest of the estate does not already assume -- every other script
# here is plain bash, and the `gh ... --jq` calls in watchdog.sh and
# loop-tick.md use gh's own bundled implementation, which is present with
# standalone jq absent. #139/#175 are actively looking at running this estate
# on other machines, so the dependency is stated here rather than discovered
# by a reader on a machine where it is missing. A missing jq is refused up
# front (below), named, and exits 1 -- it never degrades to a silent digest.
#
# Exit 0 when the digest was produced. Exit 1 only when it could not be built at
# all -- a partial digest is still emitted, with the failures NAMED. This matters
# more here than usual: this file is what a reader trusts instead of looking, so
# a section that could not be read must say so rather than appear empty. An
# empty `prs` list and an unreachable GitHub must not look the same.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./session-defaults.sh
. "$HERE/session-defaults.sh"
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
SESSION="$(lanes_session_or_default)"
REPOS="${DIGEST_REPOS:-agent-dotfiles agent-supervisor skills skills-private agent-evals}"
OWNER="${DIGEST_OWNER:-jonhill90}"
SINCE="${DIGEST_SINCE:-}"
MODE="${1:-}"
# The verdict source is an adapter (agent-dotfiles#203, scripts/supervisor/
# verdict.py) -- this script holds no knowledge of where the answer comes
# from, only which source name(s) to ask and a python3 binary to ask them
# with. DIGEST_VERDICT_BIN lets a test replace the whole call with a stub
# that takes --repo/--number and prints {"verdict":...} on stdout, the same
# override shape as DIGEST_LANES_BIN above.
#
# Default is "github", not "ledger" (agent-dotfiles#214): LedgerVerdictSource
# is a table nothing writes -- record_pr_verdict has no caller anywhere in
# this estate outside its own tests -- so "ledger" as a default always reads
# "none". GithubReviewVerdictSource works today with zero further wiring;
# under one GitHub identity the only verdict it can see is a formal
# `--request-changes` (#203), so it reports real rejections truthfully and
# "none" for everything else, which is honest about what it cannot see.
VERDICT_SOURCE="${DIGEST_VERDICT_SOURCE:-github}"
VERDICT_PYTHON="${DIGEST_VERDICT_PYTHON:-python3}"
VERDICT_BIN="${DIGEST_VERDICT_BIN:-}"
LEDGER_PYTHON="${DIGEST_LEDGER_PYTHON:-python3}"
LEDGER_CLI="${DIGEST_LEDGER_CLI:-$HERE/cli.py}"
RECONCILE_IDLE_AFTER="${DIGEST_RECONCILE_IDLE_AFTER:-300}"

# agent-supervisor#89: the `advance:` line watchdog.status carries is only
# rewritten when watchdog.sh itself ticks (advance_on_exit runs on every
# watchdog exit path, same tick that writes `checked:` with the same $iso).
# Between ticks the line sits unchanged, so a reader with no age on it reads
# a replay as a fresh observation -- exactly the shape #24 (watchdog silently
# unloaded for 91 minutes) already burned this estate on once. These knobs
# name the SAME tick cadence advance-live.sh's own watchdog_stale_check
# already uses (ADVANCE_TICK_INTERVAL / ADVANCE_WATCHDOG_STALE_MULTIPLE), not
# a second independent threshold that could drift from it.
ADVANCE_TICK_INTERVAL="${ADVANCE_TICK_INTERVAL:-180}"
ADVANCE_WATCHDOG_STALE_MULTIPLE="${ADVANCE_WATCHDOG_STALE_MULTIPLE:-3}"
ADVANCE_STALE_AFTER=$((ADVANCE_TICK_INTERVAL * ADVANCE_WATCHDOG_STALE_MULTIPLE))

# jq is the only dependency this script does not already share with the rest
# of the estate: watchdog.sh and loop-tick.md both use `gh ... --jq`, which is
# gh's own bundled implementation and works with standalone jq absent. This is
# the first script here to shell out to jq directly, so it is guarded like
# every other precondition -- unguarded, `--json` printed a zero-byte payload
# (8 lines of "jq: command not found" on stderr, nothing on stdout), which
# directly violates the "a partial digest is still emitted, with the failures
# named" contract above.
if ! command -v jq >/dev/null 2>&1; then
  if [ "$MODE" = "--json" ]; then
    printf '{"errors":["jq is required but not installed"],"ok":false}\n'
  else
    echo "ERRORS (this digest is INCOMPLETE):"
    echo "  ! jq is required but not installed"
  fi
  exit 1
fi

ERRORS=()
note_error() { ERRORS+=("$1"); }

# agent-supervisor#251: every `gh` call below used to run unbounded. Measured
# live against this estate: `state.sh` (which calls this script) hung past
# 120s inside one of these with zero output, even to stderr, and had to be
# `kill -9`ed. This is the same shape #267 fixed for quota.sh's `codexbar`
# calls -- `gh api`/`gh run list` are network round-trips against a rate-
# limited API, and a hang here is not "slow", it stalls every caller that
# shells out to this digest simultaneously.
#
# with_timeout SECONDS OUTFILE CMD... -- same self-contained `kill -0` poll
# loop as quota.sh's (#267) and advance-live.sh's (#51) fetch bound, not a
# `timeout`/`gtimeout` wrapper: the watchdog pins PATH deliberately
# (SUPERVISOR_PATH) and this repo's own suite runs against a PATH of only
# /usr/bin:/bin to prove that pin holds -- neither ships GNU coreutils on
# macOS. Prints nothing; CMD's stdout lands in OUTFILE so the caller reads it
# once the exit code is known, matching quota.sh's own convention exactly.
# Returns 124 on timeout, else CMD's own exit status.
#
# agent-supervisor#251 (second fix pass): killing only `$pid` does not take
# down anything CMD itself forked (e.g. verdict.py shelling out to `gh`/`git`
# for #226's rebase-content check) -- a killed parent's already-running
# children are reparented, not killed, and keep going to their own
# completion. That is what left `test_digest.sh` unable to confirm its
# process group dead after SIGTERM/SIGKILL. Fix: `set -m` (restored after)
# so the backgrounded job gets its OWN process group instead of inheriting
# this script's, then signal the negative pid -- a process-group signal
# reaches CMD and everything it spawned, group leader included.
#
# agent-supervisor#251 (third fix pass -- this PR's own CI failure):
# the poll interval itself, not any single hang, is what blew the 300s
# budget. Measured directly: a standalone probe wrapping 17 calls to a
# no-op `echo` through this exact function, with the poll `sleep` at a fixed
# one second, took 17s wall-clock -- 1.0s per call, whether the wrapped
# command takes 2ms or 2s, because the FIRST `kill -0` check races the fork
# and almost always finds the child still alive, so the loop always paid at
# least one full `sleep 1` before it could observe the child had exited.
# `digest.sh`'s own PR loop makes four with_timeout-wrapped calls per PR
# (gh_call run, gh_call mergeable, verdict_for, author_lane_for) against
# `gh`/`verdict.py` calls that, once stubbed or answered from cache, return
# in well under a second -- so this floor, not a genuine hang, is what
# turned a 17-PR test fixture, exercised three-plus times in
# `test_digest.sh`, into a run past 300s.
#
# A first attempt fixed this by racing a background "watchdog" subshell
# against `wait "$pid"` instead of polling at all. Discarded: that shape
# doubles the number of backgrounded jobs per call (target + watchdog), and
# under this suite's real load -- run alongside several other lanes'
# concurrent `test_digest.sh` runs on the same shared box, the environment
# this estate actually runs in -- it reproduced sporadic empty `gh`/
# `verdict.py` output for individual PRs in an otherwise-successful run,
# not a hang. That is a job-control race, not a timing win worth the risk.
# Kept instead: the exact same poll loop, proven correct across two prior
# fix passes, with the poll interval alone cut from 1s to 0.05s (`sleep`
# accepts fractional seconds on both this box's `/bin/sleep` and GNU
# coreutils). `waited`/`secs` move to centiseconds so the comparison stays
# integer-only. Same probe, same shape, this fix: 17 calls in 0.9s instead
# of 17s -- the fixed-second floor is gone, timeout behavior is untouched.
with_timeout() {
  local secs="$1" outfile="$2"; shift 2
  local prev_monitor=0
  case $- in *m*) prev_monitor=1 ;; esac
  set -m
  "$@" >"$outfile" 2>/dev/null &
  local pid=$!
  [ "$prev_monitor" -eq 1 ] || set +m 2>/dev/null
  local waited_cs=0 max_cs=$((secs * 100)) timed_out=0
  while kill -0 "$pid" 2>/dev/null; do
    if [ "$waited_cs" -ge "$max_cs" ]; then
      timed_out=1
      kill -TERM -- -"$pid" 2>/dev/null
      sleep 1
      kill -KILL -- -"$pid" 2>/dev/null
      break
    fi
    sleep 0.05
    waited_cs=$((waited_cs + 5))
  done
  wait "$pid" 2>/dev/null
  local rc=$?
  [ "$timed_out" -eq 1 ] && return 124
  return "$rc"
}
GH_TIMEOUT_SECONDS="${DIGEST_GH_TIMEOUT_SECONDS:-20}"

# `INBOX_BIN`, `LANES_BIN` and the ledger's `delivered-open` below are local
# calls (no network round-trip) left unwrapped deliberately: no CI evidence
# has ever shown one of these hang, and wrapping every one of them in a
# background-job-plus-poll -- even a cheap one -- adds real overhead
# multiplied across every call site. Measured live: doing exactly that to
# `verdict-independence.sh`'s local calls (agent-supervisor#251's own
# investigation) pushed this SAME suite's total wall-clock past the 300s
# ceiling on GitHub's runner, turning a fixed hang into a new timeout. Bound
# what is proven to hang (`gh` calls, `verdict.py get` -- see
# verdict-independence.sh); measure before bounding the rest.

# gh_call OUTVAR NOTE ARGS... -- runs `gh "${ARGS[@]}"` bounded by
# GH_TIMEOUT_SECONDS, sets OUTVAR to its stdout (empty on any failure), and
# returns 0 only when the call both finished in time and exited 0. A timeout
# is note_error'd with NOTE plus "(timed out after Ns)" so it reads
# differently from a plain API failure -- the two are not the same defect.
gh_call() {
  local __outvar="$1" note="$2"; shift 2
  local outfile
  outfile=$(mktemp "${TMPDIR:-/tmp}/digest-gh.XXXXXX") || { note_error "$note -- could not create a scratch file"; printf -v "$__outvar" '%s' ""; return 1; }
  with_timeout "$GH_TIMEOUT_SECONDS" "$outfile" gh "$@"
  local rc=$?
  local out
  out=$(cat "$outfile" 2>/dev/null)
  rm -f "$outfile"
  if [ "$rc" -eq 124 ]; then
    note_error "$note (timed out after ${GH_TIMEOUT_SECONDS}s)"
    printf -v "$__outvar" '%s' ""
    return 1
  fi
  if [ "$rc" -ne 0 ]; then
    note_error "$note"
    printf -v "$__outvar" '%s' ""
    return 1
  fi
  printf -v "$__outvar" '%s' "$out"
  return 0
}

# Splits only the LABEL prefix off a "label: value" status-file line, not
# every colon in it -- a plain `awk -F': *'` field split truncates any value
# containing its own colon. Reproduced live against watchdog.status:
# `checked:  2026-08-12T03:10:31Z` read back as `2026-08-12T03`.
status_field() {
  local file="$1" label="$2"
  awk -v label="$label" '
    $0 ~ "^" label ":" { sub("^" label ": *", ""); print; exit }
  ' "$file"
}

# Same UTC-ISO8601 -> epoch parse advance-live.sh's watchdog_age() uses --
# one parsing convention for this timestamp shape, not two that could drift.
# BSD `date -j` first (macOS ships no GNU date), GNU `date -d` as the
# fallback. Echoes nothing and returns 1 on an unparseable/empty input.
iso_to_epoch() {
  local line="$1" epoch
  [ -n "$line" ] || return 1
  epoch=$(date -u -j -f '%Y-%m-%dT%H:%M:%SZ' "$line" +%s 2>/dev/null \
        || date -u -d "$line" +%s 2>/dev/null)
  [ -n "$epoch" ] || return 1
  printf '%s\n' "$epoch"
}

# "68m ago" / "3h12m ago" -- coarse on purpose, this is a staleness read at a
# glance, not a stopwatch.
age_human() {
  local secs="$1"
  if [ "$secs" -lt 60 ]; then
    printf '%ss ago' "$secs"
  elif [ "$secs" -lt 3600 ]; then
    printf '%sm ago' "$((secs / 60))"
  else
    printf '%sh%02dm ago' "$((secs / 3600))" "$(((secs % 3600) / 60))"
  fi
}

# --- watchdog -------------------------------------------------------------
WD_FILE="$STATE/watchdog.status"
if [ -r "$WD_FILE" ]; then
  wd_state=$(status_field "$WD_FILE" state)
  wd_checked=$(status_field "$WD_FILE" checked)
  wd_restarts=$(status_field "$WD_FILE" restarts)
  wd_heartbeat=$(status_field "$WD_FILE" heartbeat)
  wd_recovery=$(status_field "$WD_FILE" recovery)
  wd_advance=$(status_field "$WD_FILE" advance)
else
  wd_state="UNREADABLE"; wd_checked=""; wd_restarts=""; wd_heartbeat=""
  wd_recovery=""; wd_advance=""
  note_error "watchdog.status unreadable at $WD_FILE"
fi

# agent-supervisor#89: `advance:` and `checked:` are written by the same
# tick (watchdog.sh's advance_on_exit runs on every exit path and reuses
# that tick's own $iso) -- so checked:'s age IS advance:'s age, without a
# second timestamp advance_note would have to start writing. wd_advance_disp
# is what gets printed/JSON-ed; empty wd_advance stays empty (no line is
# printed for it either way).
wd_advance_disp="$wd_advance"
wd_advance_age_s=""
wd_advance_stale="false"
if [ -n "$wd_advance" ]; then
  if wd_checked_epoch=$(iso_to_epoch "$wd_checked"); then
    wd_advance_age_s=$(( $(date -u +%s) - wd_checked_epoch ))
    [ "$wd_advance_age_s" -ge 0 ] || wd_advance_age_s=0
    wd_checked_time="${wd_checked#*T}"
    if [ "$wd_advance_age_s" -gt "$ADVANCE_STALE_AFTER" ]; then
      wd_advance_stale="true"
      wd_advance_disp="$wd_advance [STALE - as of $wd_checked_time, $(age_human "$wd_advance_age_s"), older than ${ADVANCE_STALE_AFTER}s]"
    else
      wd_advance_disp="$wd_advance (as of $wd_checked_time, $(age_human "$wd_advance_age_s"))"
    fi
  else
    # checked: unreadable/unparseable but advance: is present -- say so
    # rather than silently printing the bare (potentially stale) line as if
    # age were known to be fine. This is the failure #89 was filed over: an
    # instrument that cannot see a thing must not look like the thing is
    # absent (or, here, current).
    wd_advance_disp="$wd_advance [age unknown - checked: unreadable in $WD_FILE]"
  fi
fi

# agent-supervisor#41 (agent-supervisor#28): watchdog.status's `recovery:`
# line is poller-recover.sh's own OUTCOME, not the poller process being up --
# `pgrep inbox-poll.sh` and `state: ok` below both stayed green through 37
# straight recovery failures because nothing here read this line at all. A
# recovery attempt that found nothing to fix stays silent (recovery_note in
# watchdog.sh never writes an "ok" line for that case); only a recorded
# failure is degraded, so a healthy estate still reads healthy.
if [[ "$wd_recovery" == failed* ]]; then
  note_error "poller recovery: $wd_recovery"
fi

# agent-supervisor#41 (agent-supervisor#57): watchdog.status's `advance:` line
# can read "current" or "advanced" -- both trivially successful outcomes of
# the git-advance step -- while a poller-restart-request folded into that
# SAME tick failed underneath it (advance-live.sh's maybe_restart_poller).
# "the tick ran and reported success" must not stand in for "everything the
# tick attempted succeeded" (measured 2026-08-13: this exact text repeated on
# every watchdog run for hours with nothing surfacing it). Matched on the raw
# text so it fires no matter which top-level advance outcome carries it.
if [[ "$wd_advance" == *"could not be started"* ]]; then
  note_error "watchdog advance: $wd_advance"
fi

# --- poller ---------------------------------------------------------------
# agent-supervisor#154: the poller is a launchd/systemd-hosted service now,
# not a tmux window (see README.md) -- scoping liveness to a window's pane
# pid, as this used to, does not go merely dead once nothing runs a window,
# it goes ACTIVELY WRONG: it would read "not alive" forever, a permanent
# false alarm layered on top of the 278 duplicate-window alarms #154 was
# filed over. inbox-poll.status's own `checked:`/`state:` heartbeat is the
# host-agnostic answer used everywhere else in this estate (watchdog.sh's
# check_inbox_heartbeat, watchdog_notify.py) -- reused here instead of a
# second, tmux-only one. A fresh heartbeat means the process is up AND
# making progress (it is written once per completed poll iteration, never
# merely on process start); a stale or missing one means not alive, whether
# that is a crash, a hang, or a poller that was never started.
INBOX_HEARTBEAT_STALE_AFTER_DEFAULT=$(( 2 * (25 + 80) ))
POLLER_HEARTBEAT_STALE_AFTER="${INBOX_HEARTBEAT_STALE_AFTER:-$INBOX_HEARTBEAT_STALE_AFTER_DEFAULT}"
PL_FILE="$STATE/inbox-poll.status"
poller_alive=false
if [ -r "$PL_FILE" ]; then
  poller_state=$(status_field "$PL_FILE" state)
  poller_checked=$(status_field "$PL_FILE" checked)
  if poller_checked_epoch=$(iso_to_epoch "$poller_checked"); then
    poller_age_s=$(( $(date -u +%s) - poller_checked_epoch ))
    [ "$poller_age_s" -ge 0 ] || poller_age_s=0
    if [ "$poller_age_s" -le "$POLLER_HEARTBEAT_STALE_AFTER" ] && [ "$poller_state" != "stopped" ]; then
      poller_alive=true
    fi
  fi
else
  poller_state="UNREADABLE"; poller_checked=""
fi

# --- director inbox --------------------------------------------------------
# agent-supervisor#34: the Director only ever saw a queued Telegram message
# by running a tick, and a tick is exactly what a busy pane can go hours
# without -- 9 messages, 12 hours, nothing downstream able to tell "queued"
# from "lost". This section is delivered-by-construction, the same shape as
# every other field here: it costs a subprocess call regardless of whether
# the Director's pane has ever gone idle, so reading THIS digest is itself a
# delivery of the pending count and the oldest message's age, independent of
# `director-route.sh`'s nudge succeeding.
INBOX_BIN="${DIGEST_INBOX_BIN:-$HERE/director-inbox.sh}"
INBOX_STALE_SECONDS="${DIGEST_INBOX_STALE_SECONDS:-1800}"
inbox_json=$("$INBOX_BIN" stats 2>/dev/null)
if ! jq -e . >/dev/null 2>&1 <<<"$inbox_json"; then
  inbox_json='{"pending":null,"oldest_at":null,"oldest_age_s":null}'
  note_error "director-inbox.sh stats produced no readable output"
fi
inbox_pending=$(jq -r '.pending // "unknown"' <<<"$inbox_json")
inbox_oldest_age=$(jq -r '.oldest_age_s // empty' <<<"$inbox_json")
inbox_stale=false
if [ -n "$inbox_oldest_age" ] && [ "$inbox_oldest_age" -ge "$INBOX_STALE_SECONDS" ] 2>/dev/null; then
  inbox_stale=true
  note_error "director inbox: oldest pending message is ${inbox_oldest_age}s old (>= ${INBOX_STALE_SECONDS}s) -- not yet delivered to the Director"
fi

# --- lanes ----------------------------------------------------------------
# Overridable so a test can exercise a lanes.sh shape (e.g. header row, no
# data rows) without needing a real tmux session.
LANES_BIN="${DIGEST_LANES_BIN:-$HERE/lanes.sh}"
LANES_OUT=$("$LANES_BIN" "$SESSION" 2>/dev/null)
lanes_rc=$?
# Two distinct failure shapes, both invisible to a bare `[ -z ]` check:
# lanes.sh exiting non-zero (session genuinely gone, caught below already by
# emptiness in practice, but not guaranteed by contract), and lanes.sh exiting
# 0 with ONLY its header row -- a real, if narrow, tmux hiccup shape. Either
# one used to render as "the estate is idle in every category", indistinguishable
# from a genuinely idle estate.
lane_rows=$(printf '%s\n' "$LANES_OUT" | awk 'NR>1 && NF' | wc -l | tr -d ' ')
if [ "$lanes_rc" -ne 0 ] || [ -z "$LANES_OUT" ]; then
  note_error "lanes.sh returned nothing for session '$SESSION'"
elif [ "$lane_rows" -eq 0 ]; then
  note_error "lanes.sh returned no lane rows for session '$SESSION' (header only)"
fi
lane_line() { awk -v s="$1" 'NR>1 && $NF==s {print $2}' <<<"$LANES_OUT" | paste -sd, - ; }

# --- delivered-vs-pane reconciliation ------------------------------------
# agent-supervisor#36. `dispatch.sh` reads the ledger and `lanes.sh` reads the
# pane; when a worker finishes but never signals, both can be truthful and
# still disagree forever. This section surfaces only the cheap, observable
# disagreement: a task still `delivered` with no `completed_at`, whose lane's
# pane is now `free` and whose tmux-derived `idle_seconds` exceeds the
# threshold. It deliberately does NOT complete anything; the result note/path
# belongs in `record-completion` after a human reads the pane.
DELIVERED_OPEN_JSON="[]"
RECONCILIATION_JSON="[]"
if delivered_out=$("$LEDGER_PYTHON" "$LEDGER_CLI" --state-dir "$STATE" delivered-open 2>/dev/null); then
  if jq -e . >/dev/null 2>&1 <<<"$delivered_out"; then
    DELIVERED_OPEN_JSON=$(jq -c '.tasks // []' <<<"$delivered_out")
  else
    note_error "delivered-open produced unreadable JSON"
  fi
else
  note_error "delivered-open failed for ledger at $STATE"
fi
LANES_JSON_OUT=$("$LANES_BIN" --json "$SESSION" 2>/dev/null)
lanes_json_rc=$?
if [ "$lanes_json_rc" -ne 0 ] || ! jq -e . >/dev/null 2>&1 <<<"$LANES_JSON_OUT"; then
  note_error "lanes.sh --json failed for session '$SESSION' -- delivered/idle reconciliation omitted"
  LANES_JSON_OUT="[]"
fi
RECONCILIATION_JSON=$(jq -nc \
  --arg session "$SESSION" \
  --arg ledger_python "$LEDGER_PYTHON" \
  --arg ledger_cli "$LEDGER_CLI" \
  --arg state "$STATE" \
  --argjson threshold "$RECONCILE_IDLE_AFTER" \
  --argjson tasks "$DELIVERED_OPEN_JSON" \
  --argjson lanes "$LANES_JSON_OUT" '
  [
    $tasks[] as $task
    | ($lanes[] | select(($session + ":" + (.window|tostring)) == $task.lane)) as $pane
    | select($pane.state == "free")
    | select(($pane.idle_seconds // 0) >= $threshold)
    | {
        task: $task.id,
        lane: $task.lane,
        idle_seconds: ($pane.idle_seconds // 0),
        threshold_seconds: $threshold,
        window: $pane.window,
        window_id: $pane.window_id,
        name: $pane.name,
        recovery: ($ledger_python + " " + $ledger_cli + " --state-dir " + $state + " record-completion --task " + $task.id + " --note <note>")
      }
  ]')

# --- lane models ------------------------------------------------------------
# agent-supervisor#115: both worker lanes silently ran Opus instead of
# Sonnet -- measured 2026-08-12, Opus was $470 of a $552 day against $167 for
# comparable Sonnet volume -- and nothing caught it until a human read the
# pane footers by hand. `lanes.sh --json` (LANES_JSON_OUT, already fetched
# above for the delivered/idle reconciliation) now carries each lane's own
# `model`, read from the harness's self-report on its VISIBLE screen -- see
# harness/claude.sh for what that self-report actually is (a startup-splash
# line, gone once the conversation scrolls it away) and why `unknown` is
# consequently the common answer, not a bug. This section does NOT re-derive
# that read; it only reports what lanes.sh already measured, and flags a
# worker lane whose model is KNOWN and does not match what this estate's
# harness/claude.sh launches lanes with by default.
#
# Report, not enforce (#115's own brief): a mismatch is surfaced as a normal
# digest ERROR, the same severity `poller recovery: failed` gets above --
# nothing here kills or restarts a lane. That is a separate decision with
# its own blast radius.
#
# The Director's own lane is deliberately exempt, not flagged: Phase 2's
# constraint is workers and reviewers on Sonnet, Director on Opus, and
# `lanes.sh` already names the Director's own window `supervisor` -- a
# state no worker lane can be classified into (see lanes.sh's
# `SUPERVISOR_WINDOW` branch). Excluding that one state is enough; it is not
# a second guess about which window is the Director.
#
# `unknown` is a real state and is never flagged as a mismatch -- flagging it
# would make this section fire on every lane mid-conversation (the ordinary
# case for Claude, per harness/claude.sh) rather than only on a model this
# probe could actually read and found wrong. It is still carried in
# MODEL_JSON below, on every lane, so a reader auditing the JSON directly
# never sees a lane silently missing its model field -- #115's own "what
# would make this wrong" #1.
EXPECTED_WORKER_MODEL="${DIGEST_EXPECTED_MODEL:-sonnet}"
MODEL_JSON=$(jq -nc --argjson lanes "$LANES_JSON_OUT" --arg expected "$EXPECTED_WORKER_MODEL" '
  [ $lanes[] | (.model // "unknown") as $m | {
      lane: (.window|tostring), window: .window, window_id: .window_id,
      name: .name, state: .state, model: $m,
      expected: (if .state == "supervisor" then null else $expected end),
      flagged: (.state != "supervisor" and $m != "unknown" and $m != $expected)
    }
  ]')
MODEL_FLAGGED_COUNT=$(jq '[.[] | select(.flagged)] | length' <<<"$MODEL_JSON")
if [ "$MODEL_FLAGGED_COUNT" -gt 0 ] 2>/dev/null; then
  while IFS= read -r line; do
    note_error "lane model: $line"
  done < <(jq -r --arg s "$SESSION" '.[] | select(.flagged) | "\($s):\(.window) (\(.name)) is running \(.model), expected \(.expected)"' <<<"$MODEL_JSON")
fi

# agent-supervisor#179: `verdict_for`, `author_lane_for`, `lane_relation` and
# `repo_task_prefix` used to be defined here, purely for this digest's
# REPORTING of verdict independence. `merge-pr.sh` needed the identical
# computation for ENFORCEMENT and, rather than grow a second hand-rolled copy
# (the exact drift agent-supervisor#108 already paid for once), both now
# source it from one place. See verdict-independence.sh's own header for why.
# shellcheck source=./verdict-independence.sh
. "$HERE/verdict-independence.sh"

# --- pull requests --------------------------------------------------------
# One `gh` call per repo for the PR list, then one `gh run list` per PR,
# scoped to that PR's own branch.
#
# It used to be one `gh run list --limit 40` per REPO, matched against every
# PR by branch name. That call is not scoped to a branch -- it's the 40 most
# recent runs across every branch in the repo. Measured against this repo
# 2026-08-12: ~1 run every 11 minutes sustained, which exhausts a 40-slot
# window in about 7 hours -- less than a PR commonly sits open under review.
# Once a PR's run ages out of the window, `$r` was null and `run_conclusion`
# read "NO RUN" -- indistinguishable from #149's real conflicted-branch case,
# which this field exists to name. `--branch` costs one more call per PR but
# reads the actual latest run for that branch, not a stale slice of a shared
# window.
#
# agent-supervisor#144: the PR list is REST core (`gh api .../pulls`), not
# GraphQL (`gh pr list`) -- this is the call that runs every tick, for every
# repo, and the issue's own reproduction is exactly this failing. `gh run
# list` above was already REST (Actions has no GraphQL surface); unaffected.
# REST's list endpoint has no equivalent of GraphQL's `mergeStateStatus`
# (that field is genuinely GraphQL-only on the list shape), so it costs one
# more REST call per PR -- `mergeable_state` off the single-PR endpoint,
# same enum, lowercase -- which is a REST-core call spent to save a GraphQL
# one, exactly the trade #144 asks for.
PR_JSON="[]"
for repo in $REPOS; do
  gh_call list "gh pr list failed for $OWNER/$repo -- its PRs are NOT in this digest" \
    api "repos/$OWNER/$repo/pulls?state=open&per_page=100" || continue
  [ -z "$list" ] && list="[]"
  if ! jq -e . >/dev/null 2>&1 <<<"$list"; then
    note_error "gh pr list failed for $OWNER/$repo -- its PRs are NOT in this digest"
    continue
  fi
  list=$(jq -c '[.[] | {number, title, headRefOid: .head.sha, headRefName: .head.ref}]' <<<"$list")
  pr_count=$(jq 'length' <<<"$list")
  for ((i = 0; i < pr_count; i++)); do
    p=$(jq -c ".[$i]" <<<"$list")
    branch=$(jq -r '.headRefName' <<<"$p")
    num=$(jq -r '.number' <<<"$p")
    head_oid=$(jq -r '.headRefOid' <<<"$p")
    gh_call run "gh run list failed for $OWNER/$repo#$num -- CI status omitted" \
      run list -R "$OWNER/$repo" --branch "$branch" --limit 1 --json headSha,conclusion || run="[]"
    [ -z "$run" ] && run="[]"
    r=$(jq -c '.[0] // {}' <<<"$run")
    # REST's `mergeable_state` is null until GitHub finishes computing it (a
    # known API lag right after a push) -- reads UNKNOWN then, same as any
    # other unresolved answer here, not a guessed CLEAN.
    gh_call mergeable "gh pr view failed for $OWNER/$repo#$num -- merge_state omitted" \
      api "repos/$OWNER/$repo/pulls/$num" || mergeable=""
    [ -z "$mergeable" ] && mergeable="{}"
    merge_state=$(jq -r '(.mergeable_state // "unknown") | ascii_upcase' <<<"$mergeable" 2>/dev/null)
    [ -z "$merge_state" ] && merge_state="UNKNOWN"
    # Verdict comes from the adapter, not from this jq -- see verdict_for()
    # above and scripts/supervisor/verdict.py. It used to regex-match the
    # last PR comment's prose here, which read "I cannot approve this, it is
    # unsafe." as an APPROVE (agent-dotfiles#203).
    v=$(verdict_for "$OWNER/$repo" "$num" "$head_oid")
    author_lane=$(author_lane_for "$OWNER/$repo" "$num")
    # #108: computed HERE, in the shell, because it is a ledger question --
    # jq can only compare the two strings, which is exactly what was wrong.
    reviewer_lane_id=$(jq -r '.reviewer_lane // ""' <<<"$v")
    lane_rel='{"overall":"unknown","matched_lane":null,"matched_task":null}'
    if [ -n "$reviewer_lane_id" ] && jq -e '.known == true and (.external != true) and ((.contributors // []) | length) > 0' >/dev/null 2>&1 <<<"$author_lane"; then
      # agent-supervisor#332: resolve_lane_relation(), the SAME call
      # merge-pr.sh's enforcement now makes -- this report must not say
      # "independent" for a pairing the enforcement gate would refuse to
      # merge, or the two drift on the one comparison #179 built this file
      # to keep them sharing.
      # agent-supervisor#200: contributor_lane_relation(), the SAME
      # widened-contributor-set aggregation merge-pr.sh's enforcement now
      # makes -- see that script's own comment.
      lane_rel=$(contributor_lane_relation "$author_lane" "$reviewer_lane_id")
    fi
    # #179: the independence decision itself moved to verdict-independence.sh
    # (`independence_verdict`) so merge-pr.sh's ENFORCEMENT of it and this
    # digest's REPORTING of it are the same jq, computed once.
    ind=$(independence_verdict "$v" "$author_lane" "$lane_rel")
    entry=$(jq -n --argjson p "$p" --argjson r "$r" --arg repo "$repo" --argjson v "$v" --argjson ind "$ind" --arg merge_state "$merge_state" '
      {
        repo: $repo, number: $p.number, title: $p.title,
        head: $p.headRefOid[0:8],
        run_sha: ($r.headSha // "" | .[0:8]),
        run_conclusion: ($r.conclusion // "NO RUN"),
        # the check is stale unless the run was for THIS head -- the field the
        # UI does not distinguish, and a conflicted branch produces no run at all
        ci_is_current: (($r.headSha // "") == $p.headRefOid),
        merge_state: $merge_state,
        verdict: ($v.verdict // "unknown"),
        verdict_independent: $ind.value,
        verdict_detail: (
          ($v.detail // "") +
          (if (($v.detail // "") | length) > 0 and ($ind.detail | length) > 0 then "; " else "" end) +
          $ind.detail
        )
      }') || { note_error "jq failed assembling $OWNER/$repo#$num"; continue; }
    PR_JSON=$(jq -nc --argjson acc "$PR_JSON" --argjson e "$entry" '$acc + [$e]')
  done
done

# --- acceptance regressions ------------------------------------------------
# An issue closes on a CLAIM and the claim decays
# silently: PR #294 "lane identity for author exclusion" merged 2026-08-16 and
# the symptom was still excluding every free lane on 2026-08-18, with a second
# task having already completed on the same bug two days earlier. PHASES.md
# records the general form -- "seven issues closed while their symptom
# continued". Verification ran once, inside the lane, before the merge, and
# nothing ever re-ran it.
#
# This re-runs the ```acceptance block of CLOSED issues. A failure there is not
# a new bug, it is the ORIGINAL symptom still present after closure.
# REPORT-ONLY here: reopening is acceptance.sh --reopen, run deliberately, not
# a side effect of reading a digest.
ACCEPTANCE_BIN="${DIGEST_ACCEPTANCE_BIN:-$HERE/acceptance.sh}"
acceptance_line="not run"
if [ -x "$ACCEPTANCE_BIN" ]; then
  acceptance_out=$("$ACCEPTANCE_BIN" --limit "${DIGEST_ACCEPTANCE_LIMIT:-15}" 2>&1)
  acceptance_rc=$?
  acceptance_line=$(printf '%s' "$acceptance_out" | grep -E 'pass=|REGRESSED:' | tr '\n' ' ')
  if [ "$acceptance_rc" -eq 1 ]; then
    note_error "acceptance: a CLOSED issue's own test is failing again -- $(printf '%s' "$acceptance_out" | grep 'REGRESSED:' | tail -1)"
  fi
else
  note_error "acceptance.sh missing at $ACCEPTANCE_BIN -- cannot tell a healed issue from a rotted one"
fi

# --- merges since ---------------------------------------------------------
# agent-supervisor#144: REST core again. There is no `state=merged` on the
# REST list endpoint (only open/closed/all) -- `state=closed` includes PRs
# closed WITHOUT merging, filtered out here by `merged_at != null`, the same
# distinction `gh pr list --state merged` drew for free.
MERGED_JSON="[]"
if [ -n "$SINCE" ]; then
  for repo in $REPOS; do
    gh_call m "merged-list failed for $repo" \
      api "repos/$OWNER/$repo/pulls?state=closed&sort=updated&direction=desc&per_page=50" || continue
    if ! jq -e . >/dev/null 2>&1 <<<"${m:-[]}"; then
      note_error "merged-list failed for $repo"; continue
    fi
    m=$(jq -c '[.[] | select(.merged_at != null) | {number, title, mergedAt: .merged_at}]' <<<"${m:-[]}")
    MERGED_JSON=$(jq -n --argjson acc "$MERGED_JSON" --argjson m "${m:-[]}" \
      --arg repo "$repo" --arg since "$SINCE" '
      $acc + [ $m[] | select(.mergedAt > $since) | {repo:$repo, number:.number, title:.title} ]')
  done
fi

ERR_JSON=$(printf '%s\n' "${ERRORS[@]+"${ERRORS[@]}"}" | jq -R . | jq -s 'map(select(. != ""))')

DIGEST=$(jq -n \
  --arg checked "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg wd_state "$wd_state" --arg wd_checked "$wd_checked" \
  --arg wd_restarts "$wd_restarts" --arg wd_heartbeat "$wd_heartbeat" \
  --arg wd_recovery "$wd_recovery" --arg wd_advance "$wd_advance_disp" \
  --arg wd_advance_age_s "$wd_advance_age_s" --argjson wd_advance_stale "$wd_advance_stale" \
  --argjson poller_alive "$poller_alive" --arg poller_state "$poller_state" \
  --arg poller_checked "$poller_checked" \
  --argjson inbox "$inbox_json" --argjson inbox_stale "$inbox_stale" \
  --arg free "$(lane_line free)" --arg busy "$(lane_line busy)" \
  --arg blocked "$(lane_line blocked)" --arg menu "$(lane_line menu-blocked)" \
  --arg dead "$(lane_line dead)" --arg service "$(lane_line service)" \
  --arg stale "$(lane_line stale)" \
  --arg unknown "$(lane_line unknown)" \
  --argjson reconciliation "$RECONCILIATION_JSON" \
  --argjson models "$MODEL_JSON" --arg expected_model "$EXPECTED_WORKER_MODEL" \
  --argjson prs "$PR_JSON" --argjson merged "$MERGED_JSON" --argjson errors "$ERR_JSON" '
  {checked: $checked,
   watchdog: {state:$wd_state, checked:$wd_checked, restarts:$wd_restarts, heartbeat:$wd_heartbeat,
              recovery:$wd_recovery, advance:$wd_advance,
              advance_age_s:($wd_advance_age_s | if length > 0 then tonumber else null end),
              advance_stale:$wd_advance_stale},
   poller: {alive:$poller_alive, state:$poller_state, checked:$poller_checked},
   director_inbox: ($inbox + {stale: $inbox_stale}),
   lanes: {free:$free, busy:$busy, blocked:$blocked, menu_blocked:$menu,
           dead:$dead, stale:$stale, service:$service, unknown:$unknown},
   reconciliation: {delivered_idle: $reconciliation},
   lane_models: {expected: $expected_model, lanes: $models},
   prs: $prs, merged_since: $merged, errors: $errors,
   ok: ($errors | length == 0)}')

if [ "$MODE" = "--json" ]; then
  printf '%s\n' "$DIGEST"
else
  jq -r '
    "watchdog: \(.watchdog.state)  restarts=\(.watchdog.restarts)  \(.watchdog.heartbeat)",
    # Printed only when there is an outcome to report -- a recovery attempt
    # that found nothing to fix, or a tick with no poller-restart-request in
    # it, writes no line here, same as .watchdog.heartbeat above. Present
    # means an OUTCOME was recorded, not merely that the process ticked.
    (if (.watchdog.recovery|length) > 0 then "          recovery: \(.watchdog.recovery)" else empty end),
    (if (.watchdog.advance|length) > 0 then "          advance:  \(.watchdog.advance)" else empty end),
    "poller:   alive=\(.poller.alive) state=\(.poller.state)",
    # Printed every time, not only when stale -- this line IS the delivery
    # (agent-supervisor#34): a reader who checks this digest has seen the
    # pending count and the oldest message age regardless of whether the
    # Director pane was ever caught idle to nudge.
    "inbox:    pending=\(.director_inbox.pending // 0) oldest_age_s=\(.director_inbox.oldest_age_s // "n/a")\(if .director_inbox.stale then " [STALE - undelivered >= " + (.director_inbox.oldest_age_s|tostring) + "s]" else "" end)",
    "lanes:    free=[\(.lanes.free)] busy=[\(.lanes.busy)]",
    "          blocked=[\(.lanes.blocked)] menu=[\(.lanes.menu_blocked)] dead=[\(.lanes.dead)]",
    # #237: a stale lane is a dead one whose window name still claims a task.
    # Printed on its own line rather than folded into `dead` because the
    # action differs -- restore.sh, and do not believe the name.
    "          stale=[\(.lanes.stale)]",
    # #115: every lane model, always -- `unknown` prints in place rather
    # than the lane being dropped from this line, the same "never omit, say
    # unknown" rule the JSON side carries in lane_models. Only a worker lane
    # (not the Directors own `supervisor` state) with a KNOWN model that
    # does not match `.lane_models.expected` gets the `!=` marker; the
    # matching ERROR line (if any) is what actually surfaces it as something
    # to act on -- this line is the per-lane detail behind that count.
    "models:   expected=\(.lane_models.expected)",
    (.lane_models.lanes[] | "  \(.window) (\(.name)) model=\(.model)\(if .flagged then " [!= \(.expected)]" else "" end)"),
    (if (.reconciliation.delivered_idle|length) > 0 then "reconcile:" else empty end),
    (.reconciliation.delivered_idle[] | "  delivered-open \(.task) lane=\(.lane) idle=\(.idle_seconds)s; inspect pane, then record-completion --task \(.task)"),
    (if (.prs|length) == 0 then "prs:      none open" else "prs:" end),
    # Three distinct CI states, not two: no run at all, a run that failed, and
    # a run that passed but is not for this head. Collapsing "no run" and
    # "stale" cost a real investigation (#149) -- the STALE annotation names a
    # run to distrust, so it is only printed when a run actually exists.
    # A verdict of "unknown" caused by a stale review/ledger record
    # (agent-dotfiles#218) is otherwise indistinguishable here from any other
    # unreadable-source "unknown" -- the detail names which commit the
    # verdict was actually filed against, traceable against \(.head) above.
    # The detail prints for EVERY verdict that carries one, not only for
    # "unknown" (agent-dotfiles#229). It used to be gated on `.verdict ==
    # "unknown"`, which silently dropped the basis of an approved/rejected
    # verdict -- including one #226 promoted across a rebase, so a
    # rebase-promoted approval rendered identically to a review filed at the
    # literal current head. A rule that shows the basis only sometimes is a
    # rule the next reader has to learn, and its absence then reads as
    # "nothing to say" -- which is the failure #226 and #229 are both about.
    (.prs[] | "  \(.repo)#\(.number) ci=\(.run_conclusion)\(if (.ci_is_current or .run_conclusion == "NO RUN") then "" else " [STALE - run is for \(.run_sha), head is \(.head)]" end) \(.merge_state) verdict=\(.verdict)\(if (.verdict_detail|length) > 0 then " (\(.verdict_detail))" else "" end)"),
    (if (.merged_since|length) > 0 then "merged:" else empty end),
    (.merged_since[] | "  \(.repo)#\(.number) \(.title[0:52])"),
    (if (.errors|length) > 0 then "ERRORS (this digest is INCOMPLETE):" else empty end),
    (.errors[] | "  ! \(.)")
  ' <<<"$DIGEST"
fi

[ "${#ERRORS[@]}" -eq 0 ]
