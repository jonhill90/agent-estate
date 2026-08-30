#!/usr/bin/env bash
# What did the LAST agent to work this issue already find out?
#
# WHY THIS EXISTS, measured 2026-08-20 against the live results directory:
#
#     817 result files
#     184 issues worked MORE THAN ONCE
#     #308 worked 13 times · #362 11 · #284 10 · #260 9
#
# Every one of those re-dispatches started from zero. The results were written,
# stored, and never read back -- so the tenth agent on an issue rediscovers what
# the second one already posted, or repeats the mistake that stopped the fifth.
#
# The specimen that prompted this: #284 was dispatched for the TENTH time and
# concluded "investigation-only pass, no code changes, worktree diff is empty",
# because the shared checkout was on a branch missing a merged commit. An
# attempt three days earlier had already found a real defect in the same area,
# posted a reject verdict with a mutation check, and named the same class of
# problem. Nothing carried that forward. The dispatcher did not know to look,
# and the brief did not say.
#
# This is the estate's own proverb applied to its own bookkeeping: a record
# written and never consumed is indistinguishable from a record nobody kept.
# `lane-logs/` (130 files) and `watchdog.log` (18,900 lines) have the same
# shape; this is the one where the cost is measurable in re-dispatches.
#
# WHAT IT DOES NOT DO. It does not judge whether a prior attempt was right, or
# summarise it. Summarising is where a paraphrase quietly becomes the record --
# this estate has a corpus that is 30-65% agent-written for exactly that reason.
# It prints WHAT EXISTS, WHEN, and HOW BIG, and points at the files. The agent
# reads them.
#
# Exit codes -- three, never two:
#     0  prior attempts found and listed
#     1  no prior attempt for this issue (a genuinely fresh dispatch)
#     3  could not measure -- results dir missing/unreadable; NOT the same as 1
#
# The distinction matters more than it looks: "no prior attempts" and "I could
# not look" produce identical silence, and this estate has repeatedly read
# blindness as cleanliness.
set -uo pipefail

# GNU `stat -c` first, BSD `stat -f` fallback -- same order quota.sh's
# cache_age uses and for the same reason (PR #267's CI run): GNU stat's `-f`
# means "filesystem status", not "FORMAT", and does not error on the BSD
# token -- it silently prints its own multi-line filesystem report instead
# of failing over. Trying `-c` first means the BSD branch only ever runs on
# a real, correctly non-zero failure of `-c`.
_pa_mtime() { stat -c %Y "$1" 2>/dev/null || stat -f %m "$1" 2>/dev/null || echo 0; }
_pa_mdate() {
  local ep
  ep="$(_pa_mtime "$1")"
  date -d "@$ep" +'%Y-%m-%d %H:%M' 2>/dev/null || date -r "$ep" +'%Y-%m-%d %H:%M' 2>/dev/null
}

STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
RESULTS="${PRIOR_ATTEMPTS_RESULTS:-$STATE/results}"
MAX="${PRIOR_ATTEMPTS_MAX:-4}"
PREFIX=""
ISSUE=""
FORMAT="text"

usage() {
  cat >&2 <<'USAGE'
usage: prior-attempts.sh --issue <N> [--prefix as|ad|sk|at] [--max N] [--brief]

  --issue   issue number, bare (284) or prefixed (as284)
  --prefix  repo prefix when the issue is bare; default inferred as 'as'
  --max     newest N attempts to list (default 4)
  --brief   emit a markdown section suitable for appending to a dispatch brief

exit: 0 prior attempts found | 1 none | 2 usage | 3 could not measure
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --issue)  ISSUE="${2:-}"; shift 2 ;;
    --prefix) PREFIX="${2:-}"; shift 2 ;;
    --max)    MAX="${2:-}"; shift 2 ;;
    --brief)  FORMAT="brief"; shift ;;
    -h|--help) usage; exit 2 ;;
    *) echo "prior-attempts.sh: unknown argument: $1" >&2; usage; exit 2 ;;
  esac
done

[ -n "$ISSUE" ] || { usage; exit 2; }
case "$MAX" in ''|*[!0-9]*) echo "prior-attempts.sh: --max must be an integer" >&2; exit 2 ;; esac

# Accept either form. A bare number defaults to the agent-supervisor prefix
# because that is what the ledger's own task ids use.
if printf '%s' "$ISSUE" | grep -qE '^[a-z]+[0-9]+$'; then
  KEY="$ISSUE"
elif printf '%s' "$ISSUE" | grep -qE '^[0-9]+$'; then
  KEY="${PREFIX:-as}$ISSUE"
else
  echo "prior-attempts.sh: --issue must be 284 or as284, got '$ISSUE'" >&2
  exit 2
fi

if [ ! -d "$RESULTS" ]; then
  echo "prior-attempts.sh: COULD NOT MEASURE -- results dir does not exist: $RESULTS" >&2
  echo "  This is exit 3. 'No prior attempts' and 'I could not look' are different." >&2
  exit 3
fi

# POSITIVE CONTROL. An empty results directory means the scan is blind, not that
# the estate has never completed a task. Without this the common failure --
# pointing at the wrong path -- reports every issue as fresh.
total=$(find "$RESULTS" -maxdepth 1 -type f -name '*.md' 2>/dev/null | wc -l | tr -d ' ')
case "$total" in ''|*[!0-9]*) total=0 ;; esac
if [ "$total" -eq 0 ]; then
  echo "prior-attempts.sh: COULD NOT MEASURE -- zero result files under $RESULTS" >&2
  echo "  The estate has completed tasks; zero means this scan cannot see them." >&2
  exit 3
fi

matches=$(find "$RESULTS" -maxdepth 1 -type f -name "${KEY}-*.md" -o -maxdepth 1 -type f -name "${KEY}.md" 2>/dev/null | sort)
count=$(printf '%s\n' "$matches" | grep -c . || true)
case "$count" in ''|*[!0-9]*) count=0 ;; esac

if [ "$count" -eq 0 ]; then
  [ "$FORMAT" = "brief" ] || echo "no prior attempts on $KEY (scanned $total result files)"
  exit 1
fi

# Newest first: the most recent attempt is the one whose blockers are most
# likely still true.
ordered=$(printf '%s\n' "$matches" | while IFS= read -r f; do
  [ -z "$f" ] && continue
  printf '%s\t%s\n' "$(_pa_mtime "$f")" "$f"
done | sort -rn | cut -f2- | head -"$MAX")

if [ "$FORMAT" = "brief" ]; then
  cat <<EOF

---

## PRIOR ATTEMPTS ON THIS ISSUE — READ THESE FIRST

This issue has been worked **$count time(s) before**. Those agents left results
and nobody read them back; 184 issues in this estate have been re-worked, one of
them 13 times, each dispatch starting from zero.

**Before you begin**, read the files below and state in your own result which
prior findings you confirmed, contradicted, or superseded. If a previous attempt
was blocked, re-measure the blocker rather than inheriting it — blockers expire,
and two on 2026-08-19 had already expired when they were reported.

EOF
  printf '%s\n' "$ordered" | while IFS= read -r f; do
    [ -z "$f" ] && continue
    note=""
    # A REAPER STAMP IS NOT A RESULT. `reconcile-lane-completions` overwrites a
    # lane's own report with "failed, not completed" when the lane went quiet --
    # measured 2026-08-20: 133 of 817 result files are stamps, 101 of those have
    # a lane-log, and 31 of THOSE name a pull request. One specimen, ad275-fix275,
    # is stamped "failed, not completed" while its lane-log names PR #283, which
    # is MERGED. Feeding that to the next agent would teach it the work failed
    # when the work shipped. Flag it and point at the surviving evidence.
    if grep -q 'failed, not completed\|never signalled completion' "$f" 2>/dev/null; then
      t="$(basename "$f" .md)"
      lg="$STATE/lane-logs/$t.log"
      inc="$STATE/incoming/$t.md"
      note=" — **REAPER STAMP, not the lane's own report.** The lane went quiet and this file was overwritten; it does NOT mean the work failed."
      if [ -f "$lg" ]; then
        pr="$(grep -oE 'https://github\.com/[^ ",]+/pull/[0-9]+' "$lg" 2>/dev/null | head -1)"
        [ -n "$pr" ] && note="$note Its lane-log names $pr — check that before believing the stamp."
        note="$note Real output: \`$lg\`"
      fi
      [ -f "$inc" ] && note="$note and \`$inc\`"
    fi
    printf -- '- `%s` (%s, %s bytes)%s\n' "$f" \
      "$(_pa_mdate "$f")" \
      "$(wc -c < "$f" 2>/dev/null | tr -d ' ')" "$note"
  done
  if [ "$count" -gt "$MAX" ]; then
    printf '\n(%s older attempts not listed; `ls %s/%s-*` for the rest.)\n' \
      "$((count - MAX))" "$RESULTS" "$KEY"
  fi
  printf '\nDo not summarise these into your brief second-hand — read the files.\n'
else
  echo "$count prior attempt(s) on $KEY (of $total result files):"
  printf '%s\n' "$ordered" | while IFS= read -r f; do
    [ -z "$f" ] && continue
    printf '  %s  %6s B  %s\n' \
      "$(_pa_mdate "$f")" \
      "$(wc -c < "$f" 2>/dev/null | tr -d ' ')" "$f"
  done
fi
exit 0
