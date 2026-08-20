#!/usr/bin/env bash
# Refuse to advance a group of tasks until every one of them has left evidence.
#
# WHY THIS EXISTS. This estate's defining failure is not that work goes wrong --
# it is that work leaves no trace anything else reads back. `events` holds 703
# rows and 703 were never notified. `watchdog.log` has 18,900 lines and zero
# readers in the estate. A reaper gate was measured returning PASS against a
# script that was `exit 0` and nothing else. In every case the mechanism ran and
# nothing consumed the result.
#
# The one measured counterexample is Jon's own PRP execution run: 13 tasks, 13
# completion reports, 100% coverage, and it shipped. The difference was not the
# plan. It was that every task had to WRITE A FILE, and the orchestrator READ
# THAT FILE BACK and refused to start the next group when one was missing.
#
# So this is deliberately small. It does not plan, dispatch, schedule, or reason.
# It answers one question about a set of task ids:
#
#     Did each of them leave proof, and does that proof correspond to real work?
#
# WHAT COUNTS AS PROOF, and why each rule is here:
#   1. The report file EXISTS.                A missing file is unambiguous.
#   2. It is NON-TRIVIAL (>= MIN_BYTES).      An empty file is the cheapest
#                                             possible lie and costs one `touch`.
#   3. It names a path that CHANGED IN GIT.   This is the load-bearing rule. A
#                                             report is prose, and prose can
#                                             describe work that never happened.
#                                             A diff cannot.
#
# THREE EXIT CODES, NEVER TWO. A gate with only pass/fail cannot say "I could not
# see" -- and this estate has repeatedly read blindness as cleanliness:
#   0  every task proved  |  1  at least one did not  |  3  could not measure
#
# BLINDNESS IS NOT CLEANLINESS. `[ "$n" -gt 0 ]` with an empty $n prints an error
# to stderr and the `if` swallows it, so the gate reports GREEN -- measured, in
# this repo, three hours before this file was written. Every count below is
# validated as an integer before it is compared, and an unreadable input is a 3,
# never a 0.
#
# NOT A HOOK. It has no opinion on what to do about a failure; the caller decides.
# Wiring it is the caller's job -- and an unwired checker is the exact defect this
# estate has shipped five times (acp_transport.py: 302 lines, tested, zero
# importers). See test_completion_gate.sh, which asserts this file has a caller.
#
# House style is `set -uo pipefail`, not `set -euo`: `set -e` would abort on the
# first grep that legitimately matches nothing.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${COMPLETION_GATE_REPO:-$(cd "$HERE/../.." && pwd)}"
REPORT_DIR="${COMPLETION_GATE_DIR:-}"
MIN_BYTES="${COMPLETION_GATE_MIN_BYTES:-100}"
REQUIRE_DIFF="${COMPLETION_GATE_REQUIRE_DIFF:-1}"
BASE_REF="${COMPLETION_GATE_BASE:-origin/main}"
TASKS=()

usage() {
  cat >&2 <<'USAGE'
usage: completion-gate.sh --dir <report-dir> --tasks T1,T2,T3 [--base <ref>]
                          [--no-diff-check] [--min-bytes N]

  --dir           directory holding TASK<N>_COMPLETION.md files
  --tasks         comma-separated task ids (bare numbers, or T-prefixed)
  --base          git ref to diff against for the changed-files check (default origin/main)
  --no-diff-check report existence+size only; DO NOT use in CI, prose is not proof
  --min-bytes     minimum report size in bytes (default 100)

exit: 0 all proved | 1 one or more unproved | 2 usage | 3 could not measure
USAGE
}

log() { printf '%s completion-gate: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --dir)           REPORT_DIR="${2:-}"; shift 2 ;;
    --tasks)         IFS=',' read -r -a TASKS <<< "${2:-}"; shift 2 ;;
    --base)          BASE_REF="${2:-}"; shift 2 ;;
    --min-bytes)     MIN_BYTES="${2:-}"; shift 2 ;;
    --no-diff-check) REQUIRE_DIFF=0; shift ;;
    -h|--help)       usage; exit 2 ;;
    *)               log "unknown argument: $1"; usage; exit 2 ;;
  esac
done

[ -n "$REPORT_DIR" ]     || { log "REFUSED: --dir is required";   usage; exit 2; }
[ "${#TASKS[@]}" -gt 0 ] || { log "REFUSED: --tasks is required"; usage; exit 2; }

case "$MIN_BYTES" in ''|*[!0-9]*) log "REFUSED: --min-bytes must be an integer, got '$MIN_BYTES'"; exit 2 ;; esac

# --- could-not-measure checks come FIRST -------------------------------------
# A missing directory must never read as "no missing reports".
if [ ! -d "$REPORT_DIR" ]; then
  log "COULD NOT MEASURE: report directory does not exist: $REPORT_DIR"
  log "  This is exit 3, not 0. An absent directory is blindness, not cleanliness."
  exit 3
fi

if [ "$REQUIRE_DIFF" = "1" ]; then
  if ! git -C "$REPO_ROOT" rev-parse --git-dir >/dev/null 2>&1; then
    log "COULD NOT MEASURE: $REPO_ROOT is not a git repository; cannot verify reports against a diff"
    exit 3
  fi
  if ! git -C "$REPO_ROOT" rev-parse --verify --quiet "$BASE_REF" >/dev/null 2>&1; then
    log "COULD NOT MEASURE: base ref '$BASE_REF' does not resolve in $REPO_ROOT"
    exit 3
  fi
  # Four sources, and the fourth is the one that bites: `git diff` does NOT list
  # untracked files, so a task whose whole job was to CREATE a file would show no
  # diff and be judged as having done nothing. Found by this file's own test suite
  # before it ever ran for real -- most tasks in the plan it will gate create new
  # scripts rather than edit existing ones, so this would have failed nearly all
  # of them for a reason that was ours.
  CHANGED="$(
    git -C "$REPO_ROOT" diff --name-only "$BASE_REF"...HEAD 2>/dev/null
    git -C "$REPO_ROOT" diff --name-only 2>/dev/null
    git -C "$REPO_ROOT" diff --name-only --cached 2>/dev/null
    git -C "$REPO_ROOT" ls-files --others --exclude-standard 2>/dev/null
  )"
  # POSITIVE CONTROL: if the diff is empty, we cannot distinguish "no task changed
  # anything" from "the diff command is broken". Say so rather than failing every task
  # for a reason that might be ours.
  if [ -z "${CHANGED// }" ]; then
    log "COULD NOT MEASURE: no changed files at all against $BASE_REF."
    log "  Either nothing was implemented, or the diff is misconfigured. Refusing to"
    log "  report per-task failures when the instrument itself may be blind."
    exit 3
  fi
fi

# --- the actual check --------------------------------------------------------
missing=(); thin=(); undiffed=(); proved=()

for t in "${TASKS[@]}"; do
  t="${t//[[:space:]]/}"
  [ -z "$t" ] && continue
  n="${t#T}"; n="${n#t}"
  f="$REPORT_DIR/TASK${n}_COMPLETION.md"

  if [ ! -f "$f" ]; then missing+=("T${n}: no report at $f"); continue; fi

  size=$(wc -c < "$f" 2>/dev/null | tr -d ' ')
  case "$size" in ''|*[!0-9]*) missing+=("T${n}: report unreadable at $f"); continue ;; esac
  if [ "$size" -lt "$MIN_BYTES" ]; then thin+=("T${n}: report is ${size}B, minimum ${MIN_BYTES}B"); continue; fi

  if [ "$REQUIRE_DIFF" = "1" ]; then
    hit=0
    while IFS= read -r cf; do
      [ -z "$cf" ] && continue
      if grep -qF -- "$cf" "$f" 2>/dev/null; then hit=1; break; fi
      if grep -qF -- "$(basename "$cf")" "$f" 2>/dev/null; then hit=1; break; fi
    done <<< "$CHANGED"
    if [ "$hit" -eq 0 ]; then
      undiffed+=("T${n}: report names no file that actually changed vs $BASE_REF")
      continue
    fi
  fi
  proved+=("T${n}")
done

total="${#TASKS[@]}"
n_proved="${#proved[@]}"
n_bad=$(( ${#missing[@]} + ${#thin[@]} + ${#undiffed[@]} ))

if [ "$n_bad" -gt 0 ]; then
  log "HALT: ${n_proved}/${total} tasks proved. ${n_bad} did not."
  for m in "${missing[@]}";  do log "  MISSING  $m"; done
  for m in "${thin[@]}";     do log "  THIN     $m"; done
  for m in "${undiffed[@]}"; do log "  NO-DIFF  $m"; done
  log "Do NOT start the next group. A task without evidence is a task that did not happen."
  exit 1
fi

log "OK: ${n_proved}/${total} tasks proved (report exists, >=${MIN_BYTES}B, names a changed file)."
exit 0
