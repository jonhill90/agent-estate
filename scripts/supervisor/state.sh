#!/usr/bin/env bash
# The supervisor's CURRENT SITUATION, as a small hard-capped document -- a
# replacement for replaying conversation history, not an addition to it.
#
# WHY (agent-supervisor#248): one supervisor pane carried 570k tokens of
# history and re-read all of it on every one of 253 turns -- 49% of a day's
# token spend. That is not a context-window problem; it is that the
# supervisor's working state was represented as *conversation history*, so
# the only way to know the current situation was to re-read everything that
# ever happened. History grows without bound; the situation does not.
#
# `sfw/loom` (src/loom/state/task_state.py) keeps a YAML state object
# explicitly capped at roughly 500-1500 tokens, reinjected on every prompt as
# *current truth*, transcript never replayed. This is that object for this
# estate. It is a PROJECTION of the ledger (and the live measurements
# `digest.sh` already knows how to take) -- exactly as a window name is a
# projection of the ledger and never the record (CLAUDE.md's own rule). If
# you are tempted to hand-edit the state this script prints, edit the ledger
# or the source document (loop-tick.md's Boundaries section) instead: a state
# file someone edits by hand is a second record, and this estate already
# knows what a second copy costs.
#
# THE CAP IS THE DESIGN, NOT AN OPTIMISATION (the issue's own words). It is
# enforced here, in code, not left aspirational:
#   - the token estimate is always printed (chars/4, named as the method --
#     see est_tokens below; it is deliberately crude, and named as such
#     rather than dressed up as exact)
#   - when the full document would exceed the cap, sections are dropped or
#     summarised in a fixed, documented order (see REDUCE ladder below) --
#     never silently, and never by raising the ceiling
#   - if the cap still cannot be met after every reduction, this exits 2 and
#     prints CAP EXCEEDED rather than emitting an over-budget document
#
# WHAT THIS DOES NOT DO: replace the judgement digest.sh's human-readable
# mode exists for. This calls `digest.sh --json` for the live measurements
# (watchdog/poller health, lane table, PR verdicts, CI gate reads, the
# delivered-vs-pane reconciliation that is this estate's existing #235-shaped
# stale-row caveat) and DISTILS that plus the ledger's own dispatch detail
# into the capped document. It is not a second implementation of any of
# digest.sh's `gh`/tmux reads.
#
# "Say what it cannot know" (requirement 4): a row this script cannot read
# is never omitted -- it is marked `unknown`, with a reason, same discipline
# `digest.sh` and `lanes.sh` already hold themselves to. Two rows are marked
# `unknown` by construction, not by a live probe, because nothing in this
# estate instruments them yet:
#   - `quota` -- no usage/rate-limit tracker exists anywhere in this repo
#     (grepped for budget/spend/usage-limit at the time this was written;
#     nothing matched). Reporting a number here would be inventing one.
#   - `gate.last_verdict` per PR reduces to digest's own CI-gate read
#     (`run_conclusion`/`merge_state`), which is a LIVE read, not a persisted
#     "last verdict" -- ci_gate.py computes nothing durable to look back at,
#     it re-fetches on every call. That is stated here rather than implied.
#
# Usage:
#   state.sh              human-readable capped document
#   state.sh --json       the same facts, uncapped, as one JSON object --
#                          for tooling/tests, NOT what gets reinjected as
#                          prompt state (that is the default, capped, mode)
#
# Exit 0: the document was produced (possibly reduced to fit the cap).
# Exit 1: a dependency (jq, digest.sh, the ledger) could not be read at all.
# Exit 2: even the most reduced document still exceeds STATE_TOKEN_CAP --
#         printed to stderr with the estimate; the fix is to drop a section
#         in code, never to raise the cap silently.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
# No session resolution here: digest.sh (called below) already resolves
# LANES_SESSION/its own default the same way every other caller does, and it
# inherits this process's environment -- a second resolution here would just
# be a second copy that could drift from digest.sh's.
MODE="${1:-}"

DIGEST_BIN="${STATE_DIGEST_BIN:-$HERE/digest.sh}"
LEDGER_PYTHON="${STATE_LEDGER_PYTHON:-python3}"
LEDGER_CLI="${STATE_LEDGER_CLI:-$HERE/cli.py}"
LOOP_TICK="${STATE_LOOP_TICK:-$HERE/loop-tick.md}"
# loom's own cap is "roughly 500-1500 tokens" (the issue's phrasing); 1500 is
# the upper edge of that range, not a number picked fresh here.
TOKEN_CAP="${STATE_TOKEN_CAP:-1500}"

# agent-supervisor#251: this script used to shell out to `digest.sh --json`
# and `cli.py status` with no timeout wrapper anywhere -- the same shape
# #267 fixed for quota.sh's `codexbar` calls. Reproduced live, twice: this
# script hung past 120s inside the `digest.sh --json` subshell with ZERO
# output, even to stderr, and had to be `kill -9`ed; `cli.py task-lane`
# against the live ledger separately blew past 120s. A document meant to be
# reinjected as cheap per-turn state cannot hang on the very reads it exists
# to replace.
#
# with_timeout SECONDS OUTFILE CMD... -- the same self-contained `kill -0`
# poll loop quota.sh (#267) and advance-live.sh (#51) already use, not a
# `timeout`/`gtimeout` wrapper: the watchdog pins PATH deliberately
# (SUPERVISOR_PATH) and this repo's own suite runs scripts against a PATH of
# only /usr/bin:/bin to prove that pin holds -- neither ships GNU coreutils
# on macOS. Prints nothing; CMD's stdout lands in OUTFILE so the caller can
# read it once the exit code is known. Returns 124 on timeout, else CMD's
# own exit status.
#
# agent-supervisor#251 (second fix pass, propagated here for consistency --
# this file's copy has the same latent defect even though it was not the one
# CI caught): killing only `$pid` does not take down anything CMD itself
# forked -- a killed parent's already-running children are reparented, not
# killed, and keep going to their own completion. `set -m` (restored after)
# puts the backgrounded job in its OWN process group instead of inheriting
# this script's, so the timeout path can signal the negative pid -- a
# process-group signal that reaches CMD and everything it spawned.
with_timeout() {
  local secs="$1" outfile="$2"; shift 2
  local prev_monitor=0
  case $- in *m*) prev_monitor=1 ;; esac
  set -m
  "$@" >"$outfile" 2>/dev/null &
  local pid=$!
  [ "$prev_monitor" -eq 1 ] || set +m 2>/dev/null
  local waited=0 timed_out=0
  while kill -0 "$pid" 2>/dev/null; do
    if [ "$waited" -ge "$secs" ]; then
      timed_out=1
      kill -TERM -- -"$pid" 2>/dev/null
      sleep 1
      kill -KILL -- -"$pid" 2>/dev/null
      break
    fi
    sleep 1
    waited=$((waited + 1))
  done
  wait "$pid" 2>/dev/null
  local rc=$?
  [ "$timed_out" -eq 1 ] && return 124
  return "$rc"
}
DIGEST_TIMEOUT_SECONDS="${STATE_DIGEST_TIMEOUT_SECONDS:-45}"
LEDGER_TIMEOUT_SECONDS="${STATE_LEDGER_TIMEOUT_SECONDS:-20}"

if ! command -v jq >/dev/null 2>&1; then
  echo "state.sh: jq is required but not installed" >&2
  exit 1
fi

# --- pull the live facts from digest.sh, once ------------------------------
# One subprocess, reused for every section below -- this script holds no
# knowledge of how to read tmux or gh, only how to distil what digest.sh
# already read. DIGEST_JSON on empty/malformed output, OR a timeout,
# degrades every section below to "unknown", same discipline digest.sh
# itself uses for a single unreadable source.
digest_outfile=$(mktemp "${TMPDIR:-/tmp}/state-digest.XXXXXX") || { echo "state.sh: could not create a scratch file for digest.sh's output" >&2; exit 1; }
with_timeout "$DIGEST_TIMEOUT_SECONDS" "$digest_outfile" "$DIGEST_BIN" --json
digest_rc=$?
DIGEST_JSON=$(cat "$digest_outfile" 2>/dev/null)
rm -f "$digest_outfile"
if [ "$digest_rc" -eq 124 ]; then
  echo "state.sh: digest.sh --json timed out after ${DIGEST_TIMEOUT_SECONDS}s -- state will be mostly 'unknown'" >&2
  DIGEST_JSON='{"ok":false,"errors":["digest.sh timed out after '"$DIGEST_TIMEOUT_SECONDS"'s"],"lanes":{},"prs":[],"reconciliation":{"delivered_idle":[]},"watchdog":{},"poller":{}}'
elif ! jq -e . >/dev/null 2>&1 <<<"$DIGEST_JSON"; then
  echo "state.sh: digest.sh --json produced no readable output -- state will be mostly 'unknown'" >&2
  DIGEST_JSON='{"ok":false,"errors":["digest.sh unreadable"],"lanes":{},"prs":[],"reconciliation":{"delivered_idle":[]},"watchdog":{},"poller":{}}'
fi

# --- ledger: what is dispatched, and to whom --------------------------------
# The ledger's `tasks` table is the durable record of occupancy (CLAUDE.md
# invariant 1) -- digest.sh's lane line only ever prints tmux-derived
# state/window names, never which issue or task owns a busy lane. This is
# the one thing `digest.sh` does not already carry that this document needs.
#
# DISPATCHED_UNKNOWN distinguishes "the read failed" from "the ledger
# genuinely has nothing open" -- both used to collapse to the same empty
# `[]`, which rendered as "dispatched: none", indistinguishable from a
# clean, idle ledger (agent-supervisor#251, requirement 4: a row is never
# silently dropped for being unknown).
DISPATCHED_JSON="[]"
DISPATCHED_UNKNOWN="false"
DISPATCHED_REASON=""
ledger_outfile=$(mktemp "${TMPDIR:-/tmp}/state-ledger.XXXXXX") || { echo "state.sh: could not create a scratch file for the ledger's output" >&2; exit 1; }
with_timeout "$LEDGER_TIMEOUT_SECONDS" "$ledger_outfile" "$LEDGER_PYTHON" "$LEDGER_CLI" --state-dir "$STATE" status
ledger_rc=$?
status_out=$(cat "$ledger_outfile" 2>/dev/null)
rm -f "$ledger_outfile"
if [ "$ledger_rc" -eq 124 ]; then
  DISPATCHED_UNKNOWN="true"
  DISPATCHED_REASON="ledger status timed out after ${LEDGER_TIMEOUT_SECONDS}s at $STATE"
  echo "state.sh: $DISPATCHED_REASON -- dispatched section will read unknown" >&2
elif [ "$ledger_rc" -eq 0 ] && jq -e . >/dev/null 2>&1 <<<"$status_out"; then
  DISPATCHED_JSON=$(jq -c '
    [ .tasks[] | select(.status | IN("complete","failed","cancelled") | not) |
      {lane, task: .id, status, summary: (.summary // "" | .[0:80])}
    ]' <<<"$status_out")
else
  DISPATCHED_UNKNOWN="true"
  DISPATCHED_REASON="ledger status unreadable at $STATE"
  echo "state.sh: $DISPATCHED_REASON -- dispatched section will read unknown" >&2
fi

# --- standing constraints: derived from loop-tick.md, never hand-maintained -
# Extracted from the document that already carries them (requirement 3) --
# this is a projection of that section, not a second copy of its wording. A
# reader who wants the reasoning behind a constraint follows the pointer;
# this only carries the bullet text so it survives a cap.
#
# CONSTRAINTS_UNKNOWN, same distinction as DISPATCHED_UNKNOWN above: an
# unreadable loop-tick.md used to render identically to a document with no
# standing rules at all -- "constraints:" with nothing under it.
# --- quota -------------------------------------------------------------------
# This document used to hardcode `no quota/usage instrumentation exists in this
# estate`. That was true when written and is now false: quota.sh, quota-watch.sh
# and quota-watch-recover.sh all exist, quota-watch runs under launchd, and
# PHASES.md records the work as DONE. A state document whose whole job is to be
# current truth was asserting a stale claim about the estate's most expensive
# constraint -- on the same night an exhausted weekly went unnoticed until a
# human asked.
#
# It reads the PERSISTED verdict rather than calling quota.sh, deliberately:
# quota.sh samples codexbar three times and can take 45s+, and this document is
# re-read every turn. quota-watch.sh already pays that cost on its own schedule.
#
# THE `state` FIELD IS REPORTED, NOT `confirmed`. Those differ, and the
# difference is a live defect: the file right now reads `state: UNKNOWN` with
# `confirmed: SAFE`, because the fallback retains the last good reading. Retaining
# last-good is the one direction a quota meter cannot fail safe in -- a blind
# meter and a healthy one render identically, which is how $80 of usage credits
# went to $8 on 2026-08-15. `unknown_streak` is surfaced for the same reason: one
# unknown is a blip, a streak is blindness.
QUOTA_STATE="unknown"
QUOTA_REASON="quota-watch has not written a state file yet"
QUOTA_FILE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}/.quota-watch.state"
if [ -r "$QUOTA_FILE" ]; then
  # sed, not `awk -F': *'`: the value IS a timestamp and contains colons, so a
  # colon field-split truncates 2026-08-20T03:04:27Z to 2026-08-20T03 -- which
  # then fails to parse as a date, so the staleness check silently never fires.
  # Caught by reading the rendered output rather than trusting the code.
  q_state=$(sed -n 's/^state: *//p'          "$QUOTA_FILE" | head -1)
  q_conf=$(sed -n 's/^confirmed: *//p'       "$QUOTA_FILE" | head -1)
  q_streak=$(sed -n 's/^unknown_streak: *//p' "$QUOTA_FILE" | head -1)
  q_checked=$(sed -n 's/^checked: *//p'      "$QUOTA_FILE" | head -1)
  case "$q_streak" in ''|*[!0-9]*) q_streak=0 ;; esac
  q_age=""
  if [ -n "$q_checked" ]; then
    # -u matters: without it BSD `date -j -f` parses a UTC stamp as LOCAL time,
    # so the age came out NEGATIVE by exactly the tz offset (-14248s on EDT) and
    # the staleness branch could never fire. A negative age is not a small error,
    # it is a check that cannot fail -- which is the defect class this estate is
    # remediating. Found by reading the rendered number, not the code.
    #
    # BSD `date -j -f` first (macOS ships no GNU date), GNU `date -d` as the
    # fallback -- the same UTC-ISO8601 -> epoch parse digest.sh's
    # iso_to_epoch() uses, not a second fallback that could drift from it.
    # `-j` exits 1 under GNU date rather than parsing, so on ubuntu-latest
    # (CI) this fell through to an empty epoch and the staleness branch never
    # fired -- reproduced live in CI (#397).
    q_epoch=$(date -u -j -f '%Y-%m-%dT%H:%M:%SZ' "$q_checked" '+%s' 2>/dev/null \
            || date -u -d "$q_checked" '+%s' 2>/dev/null || echo "")
    case "$q_epoch" in ''|*[!0-9]*) : ;; *) q_age=$(( $(date -u +%s) - q_epoch )) ;; esac
  fi
  if [ -n "${q_state// }" ]; then
    QUOTA_STATE="$q_state"
    q_age_txt=""
    [ -n "$q_age" ] && q_age_txt=", ${q_age}s ago"
    QUOTA_REASON="checked ${q_checked:-unknown}${q_age_txt}; last confirmed ${q_conf:-none}; unknown_streak ${q_streak}"
    # A stale state file is blindness wearing a verdict. 30 min is six
    # quota-watch intervals; past that the number on the page is not current.
    if [ -n "$q_age" ] && [ "$q_age" -gt 1800 ]; then
      QUOTA_STATE="unknown"
      QUOTA_REASON="STALE: quota-watch last wrote ${q_age}s ago (>1800s); refusing to report '${q_state}' as current"
    fi
  fi
else
  QUOTA_REASON="no readable quota state at $QUOTA_FILE -- quota-watch may not be running"
fi

CONSTRAINTS_JSON="[]"
CONSTRAINTS_UNKNOWN="false"
CONSTRAINTS_REASON=""
GATED_LINE=""
if [ -r "$LOOP_TICK" ]; then
  CONSTRAINTS_JSON=$(awk '/^## Boundaries/{f=1;next}/^## /{f=0}f && /^- /{print}' "$LOOP_TICK" \
    | sed 's/^- //' | jq -R . | jq -s .)
  GATED_LINE=$(awk '/^Currently gated:/{print;exit}' "$LOOP_TICK")
else
  CONSTRAINTS_UNKNOWN="true"
  CONSTRAINTS_REASON="$LOOP_TICK unreadable"
  echo "state.sh: $CONSTRAINTS_REASON -- constraints section will read unknown" >&2
fi

# --- assemble the full (uncapped) fact set ----------------------------------
FULL=$(jq -n \
  --arg checked "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --argjson digest "$DIGEST_JSON" \
  --argjson dispatched "$DISPATCHED_JSON" \
  --argjson dispatched_unknown "$DISPATCHED_UNKNOWN" \
  --arg dispatched_reason "$DISPATCHED_REASON" \
  --argjson constraints "$CONSTRAINTS_JSON" \
  --argjson constraints_unknown "$CONSTRAINTS_UNKNOWN" \
  --arg constraints_reason "$CONSTRAINTS_REASON" \
  --arg quota_state "$QUOTA_STATE" \
  --arg quota_reason "$QUOTA_REASON" \
  --arg gated_line "$GATED_LINE" '
  {
    checked: $checked,
    # requirement 4: the health gate is digest.ok/errors -- a LIVE re-read,
    # not a persisted verdict. quota IS instrumented (quota.sh + quota-watch.sh
    # under launchd); this reads the persisted quota-watch state rather than
    # paying its 45s+ sampling cost on every turn.
    gate: {
      result: (if $digest.ok then "PASS" else "FAIL" end),
      reasons: ($digest.errors // [])
    },
    quota: {state: $quota_state, reason: $quota_reason},
    lanes: ($digest.lanes // {}),
    dispatched: $dispatched,
    dispatched_unknown: $dispatched_unknown,
    dispatched_reason: $dispatched_reason,
    unreconciled: ($digest.reconciliation.delivered_idle // []),
    prs: ($digest.prs // []),
    lane_models: ($digest.lane_models.lanes // []),
    constraints: $constraints,
    constraints_unknown: $constraints_unknown,
    constraints_reason: $constraints_reason,
    constraints_gated: $gated_line
  }')

# --- render + cap ------------------------------------------------------------
# tokens are estimated as chars/4 -- crude, named as such rather than dressed
# up as exact (requirement 1: "print the token estimate"). It is a stated,
# reproducible instrument, not a claim of precision.
est_tokens() {
  local chars=${#1}
  echo $(( (chars + 3) / 4 ))
}

# Renders the document at one of three detail levels. Each level is a
# strictly smaller document than the one before it -- nothing is ever
# reordered, only summarised or dropped, and every drop says so in-line
# rather than silently shrinking a list (requirement 4: never omit a row,
# say what could not fit).
render() {
  local level="$1" full="$2"
  jq -r --argjson level "$level" '
    def cap(n; arr): if (arr|length) > n then arr[0:n] + ["... +\((arr|length) - n) more, omitted to fit the cap"] else arr end;

    "checked: \(.checked)",
    "gate: \(.gate.result)" + (if (.gate.reasons|length) > 0 then " (" + (.gate.reasons | join("; ")) + ")" else "" end),
    "quota: \(.quota.state) -- \(.quota.reason)",
    "",
    "lanes:",
    "  free: [\(.lanes.free // "")]",
    "  busy: [\(.lanes.busy // "")]",
    "  blocked: [\(.lanes.blocked // "")]",
    "  dead: [\(.lanes.dead // "")]",
    "  stale: [\(.lanes.stale // "")]",
    "  unknown: [\(.lanes.unknown // "")]",
    "",
    (if .dispatched_unknown then "dispatched: unknown -- \(.dispatched_reason)"
     elif (.dispatched|length) == 0 then "dispatched: none"
     else "dispatched:",
       (.dispatched
         | (if $level >= 2 then cap(6; .) else . end)[]
         | if type == "string" then "  " + .
           else "  \(.lane) task=\(.task) status=\(.status)" +
                (if $level == 0 then " -- \(.summary)" else "" end)
           end)
     end),
    "",
    (if (.unreconciled|length) == 0 then "unreconciled: none (no delivered task sitting idle past the reconcile threshold)"
     else "unreconciled (delivered but lane free past threshold -- ledger and pane disagree, #235-shaped):",
       (.unreconciled[] | "  task=\(.task) lane=\(.lane) idle=\(.idle_seconds)s -- \(.recovery)")
     end),
    "",
    (if (.prs|length) == 0 then "open_prs: none"
     else "open_prs:",
       (.prs
         | (if $level >= 1 then cap(10; .) else . end)[]
         | if type == "string" then "  " + .
           else "  \(.repo)#\(.number) ci=\(.run_conclusion) merge=\(.merge_state) verdict=\(.verdict)" +
                (if $level == 0 and (.verdict_detail|length) > 0 then " (\(.verdict_detail))" else "" end)
           end)
     end),
    "",
    (if .constraints_unknown then "constraints: unknown -- \(.constraints_reason)"
     elif $level >= 1 then
       "constraints: see loop-tick.md#Boundaries (\(.constraints|length) standing rules); \(.constraints_gated)"
     else
       "constraints:",
       (.constraints[] | "  - " + .),
       (if (.constraints_gated|length) > 0 then "  " + .constraints_gated else empty end)
     end)
  ' <<<"$full"
}

DOC=""
CHOSEN_LEVEL=""
for level in 0 1 2; do
  DOC="$(render "$level" "$FULL")"
  est=$(est_tokens "$DOC")
  if [ "$est" -le "$TOKEN_CAP" ]; then
    CHOSEN_LEVEL="$level"
    break
  fi
done

FINAL_EST=$(est_tokens "$DOC")

if [ "$MODE" = "--json" ]; then
  jq -c --argjson est "$FINAL_EST" --argjson cap "$TOKEN_CAP" '. + {token_estimate: $est, token_cap: $cap}' <<<"$FULL"
  exit 0
fi

if [ -z "$CHOSEN_LEVEL" ]; then
  # Every reduction level was tried and none fit -- fail loudly on stderr,
  # WITHOUT printing the over-budget document to stdout, so nothing that
  # consumes stdout unconditionally can mistake this for a valid capped
  # state. Never silently raise STATE_TOKEN_CAP to make this pass instead.
  echo "state.sh: CAP EXCEEDED -- ${FINAL_EST} tokens (est., chars/4) > cap ${TOKEN_CAP} even at the most reduced level" >&2
  echo "state.sh: fix is to drop or further summarise a section in code, never to raise STATE_TOKEN_CAP silently" >&2
  exit 2
fi

echo "$DOC"
echo ""
echo "# token_estimate=${FINAL_EST} (chars/4, cap=${TOKEN_CAP}, detail_level=${CHOSEN_LEVEL})"
