#!/bin/bash
# Preserve a dead lane's uncommitted work -- agent-supervisor#651.
#
# WHY. Three lanes died mid-work on 2026-08-24 holding uncommitted changes
# in a `/var/folders` worktree -- one 298 insertions across four files. Each
# was rescued by hand: commit, mark it clearly as a rescue, push. Nothing in
# the tooling did this automatically, and the worktree is temporary --
# `worktree.sh`'s own `gc` correctly REFUSES to remove a dirty tree, but
# refusing to delete is not the same as preserving; the work just sits there
# until the OS reaps the directory. This sweep is the automatic version of
# the manual rescue, nothing more: for every open task whose lane is
# PROVABLY DEAD and whose worktree is DIRTY, commit the changes to the
# lane's own branch and push it, marked as unverified.
#
# WHAT THIS DELIBERATELY DOES NOT DO: it never pushes to main, never opens a
# PR, never merges, and never deletes anything (that is still `worktree.sh
# gc`'s job, once the branch is safely pushed). A live lane's worktree being
# committed underneath it is its own bug, worse than the one this fixes --
# see the liveness section below.
#
# Usage:
#   preserve-dead-lanes.sh [--dry-run]
#
# --dry-run reports what would be committed and pushed; nothing is changed.
# Exit 0: ran cleanly (whether or not anything needed preserving). Exit 1:
# at least one push failed after a commit was made locally -- the work is
# safe (it is committed) but NOT yet durable (not on a remote); look at the
# stderr for which branch. Exit 2: the ledger itself could not be read.
#
# LIVENESS -- the part most likely to be got wrong, measured today rather
# than assumed:
#
#   - A claude-print lane puts its worktree path in the spawned process's
#     argv (`claude_print_transport.py` passes `cwd=` to `subprocess.run`,
#     and the path also appears in the wrapping shell's command line) --
#     `pgrep -f "$wt"` finds it. `pgrep` excludes its own PID by design, so
#     this is not the self-counting trap below.
#   - A tmux pane lane (a hand-attached or `harness/*.sh`-driven pane) has
#     the worktree as the pane's CWD, which never appears in argv at all --
#     `pgrep` reports 0 for a lane that is alive and working right now. Only
#     `tmux list-panes -a -F '#{pane_current_path}'` sees it. A live review
#     lane was nearly declared dead on exactly this while #651 was measured.
#   - A process can also be cd'd into the worktree with no path in its argv
#     at all (agent-supervisor#478 measured this for a bare `claude`
#     process) -- invisible to both checks above. `lsof -a -d cwd` reports
#     a process's ACTUAL cwd, from its file descriptor table, not its argv.
#
#   No ONE of these three sees every lane shape. A worktree is dead only
#   when ALL THREE say so -- ANDed the opposite way from `worktree.sh`'s own
#   `_gc_is_live` (which keeps if ANY says "maybe live"): that asymmetry is
#   intentional, not a copy error. `_gc_is_live` is biased toward refusing a
#   DELETE; this is biased toward refusing a COMMIT-AND-PUSH under a live
#   lane, which is the worse of the two mistakes here, so the same "any one
#   dissenting vote wins" logic is kept, just for a different destructive
#   verb. If a check cannot even be asked (no tmux server, no lsof binary),
#   that counts as "maybe live" too, never as "clear".
#
#   These three checks intentionally duplicate `worktree.sh`'s
#   `_gc_tmux_occupies` / `_gc_process_refs` rather than sourcing them:
#   `worktree.sh` runs a `case "$CMD" in ... esac` dispatch unconditionally
#   at the bottom of the file, so `source`-ing it here would execute THAT
#   dispatch against this script's own "$@" and likely hit `usage`'s `exit
#   2`, killing this script rather than lending it two functions. Small,
#   read-only, and worth keeping in sync by comment cross-reference rather
#   than by a sourcing trick that breaks the moment either script's
#   arguments change.
#
# TWO TRAPS THAT PRODUCED CONFIDENT WRONG ANSWERS WHILE THIS WAS BUILT, so
# the next reader does not re-derive them the hard way:
#
#   - `ps -eo command | grep -cF "$wt"` counts its own pipeline: the grep's
#     OWN argv contains "$wt", so a dead lane's worktree reports "alive"
#     with a count of (at least) 1 no matter what. Measured: naive count
#     said 3 where `pgrep` (which excludes itself) said 0. Never grep
#     process output for a literal path with your own command's argv in
#     scope; `pgrep -f` is built to exclude itself, `ps | grep` is not.
#   - `find "$wt" -newermt '10 minutes ago'` prints nothing on this host --
#     `find` here is `bfs`, which rejects `-newermt` and produces empty
#     output, indistinguishable from "nothing changed recently" unless the
#     exit status is checked. Walking the tree and comparing `stat`'s mtime
#     by hand (see `_pdl_newest_mtime`) avoids depending on a `find` flag
#     that silently does not exist on every host this might run on.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# CLI_PYTHON runs cli.py against the real ledger -- stubbable via
# AGENT_PYTHON_BIN, same as every other caller in this estate, so a test can
# swap in a fake ledger response. JSON_PYTHON only parses the JSON this
# script already has in hand (no ledger access of its own) -- fixed to a
# real interpreter so a stubbed AGENT_PYTHON_BIN in a test does not also
# have to reimplement JSON parsing.
CLI_PYTHON="${AGENT_PYTHON_BIN:-python3}"
JSON_PYTHON="python3"
CLI="$HERE/cli.py"
TMUX_BIN="${TMUX_BIN:-tmux}"

DRY=""
if [ "${1:-}" = "--dry-run" ]; then DRY=1; shift; fi

# ---------------------------------------------------------------------------
# Liveness checks
# ---------------------------------------------------------------------------

# 0 = a process's argv contains $wt (claude-print shape); 1 = pgrep answered
# and found nothing; 2 = pgrep itself could not be asked.
_pdl_pgrep_alive() {
  local wt="$1"
  command -v pgrep >/dev/null 2>&1 || return 2
  pgrep -f -- "$wt" >/dev/null 2>&1
  local rc=$?
  case $rc in
    0) return 0 ;;
    1) return 1 ;;
    *) return 2 ;;
  esac
}

# 0 = some tmux pane's #{pane_current_path} is inside $wt; 1 = tmux answered
# and none matched; 2 = tmux itself could not be asked (no server, no
# binary). Mirrors worktree.sh's `_gc_tmux_occupies` -- same signal, same
# reasoning (agent-supervisor#478), duplicated rather than sourced (see the
# header comment above for why).
_pdl_tmux_alive() {
  local target_real="$1" panes pane pane_real
  command -v "$TMUX_BIN" >/dev/null 2>&1 || return 2
  panes=$("$TMUX_BIN" list-panes -a -F '#{pane_current_path}' 2>/dev/null) || return 2
  while IFS= read -r pane; do
    [ -n "$pane" ] || continue
    pane_real=$(cd "$pane" 2>/dev/null && pwd -P) || continue
    case "$pane_real" in
      "$target_real"|"$target_real"/*) return 0 ;;
    esac
  done <<<"$panes"
  return 1
}

_pdl_lsof_bin() {
  if command -v lsof >/dev/null 2>&1; then printf '%s' lsof; return 0; fi
  if [ -x /usr/sbin/lsof ]; then printf '%s' /usr/sbin/lsof; return 0; fi
  return 1
}

# 0 = some process's ACTUAL cwd (via lsof, not argv) is inside $wt; 1 = lsof
# answered and none is; 2 = lsof itself could not be asked. Mirrors
# worktree.sh's `_gc_process_refs` (agent-supervisor#478) -- catches a
# process cd'd into the worktree with no path in its own argv, which
# `_pdl_pgrep_alive` above is blind to.
_pdl_lsof_alive() {
  local target_real="$1" lsof_bin out line path
  lsof_bin=$(_pdl_lsof_bin) || return 2
  out=$("$lsof_bin" -a -d cwd -Fn 2>/dev/null) || return 2
  [ -n "$out" ] || return 2
  while IFS= read -r line; do
    case "$line" in
      n*)
        path="${line#n}"
        case "$path" in
          "$target_real"|"$target_real"/*) return 0 ;;
        esac
        ;;
    esac
  done <<<"$out"
  return 1
}

# Prints a one-line human reason to stdout and returns 0 if $1 must be
# treated as LIVE (any one signal says "maybe"), 1 if all three signals
# independently said "no process, no pane, no cwd reference anywhere".
_pdl_is_live() {
  local wt="$1" real rc
  real=$(cd "$wt" 2>/dev/null && pwd -P) || real="$wt"

  _pdl_pgrep_alive "$wt"; rc=$?
  case $rc in
    0) echo "a process's argv references the worktree path (pgrep -f)"; return 0 ;;
    2) echo "pgrep could not be asked -- refusing to guess"; return 0 ;;
  esac

  _pdl_tmux_alive "$real"; rc=$?
  case $rc in
    0) echo "a tmux pane's cwd is inside the worktree"; return 0 ;;
    2) echo "tmux could not be asked -- refusing to guess"; return 0 ;;
  esac

  _pdl_lsof_alive "$real"; rc=$?
  case $rc in
    0) echo "a process's actual cwd (lsof) is inside the worktree"; return 0 ;;
    2) echo "lsof could not be asked -- refusing to guess"; return 0 ;;
  esac

  return 1
}

# ---------------------------------------------------------------------------
# Staleness -- reported alongside liveness, never a substitute for it. A
# lane whose newest file mtime is old and whose process (if `_pdl_is_live`
# found one) is at ~0% CPU is finished-or-stuck, but a process that exists
# still fails the hard "both checks must be negative" rule above and is
# left alone regardless of how stale it looks -- this is forensic context
# for the report, not a fourth vote toward "dead".
# ---------------------------------------------------------------------------

_pdl_mtime() { stat -f %m "$1" 2>/dev/null || stat -c %Y "$1" 2>/dev/null; }

# Newest mtime among files git itself reports changed (tracked-modified +
# untracked) -- walked and stat'd by hand, not `find -newermt` (see header:
# this host's `find` is `bfs` and silently no-ops on that flag).
_pdl_newest_mtime() {
  local wt="$1" newest=0 line path full m
  while IFS= read -r -d '' line; do
    path="${line:3}"
    full="$wt/$path"
    [ -e "$full" ] || continue
    m=$(_pdl_mtime "$full") || continue
    [ -n "$m" ] || continue
    [ "$m" -gt "$newest" ] && newest="$m"
  done < <(git -C "$wt" status --porcelain -z 2>/dev/null)
  printf '%s' "$newest"
}

# ---------------------------------------------------------------------------
# The sweep
# ---------------------------------------------------------------------------

TASKS_JSON=$("$CLI_PYTHON" "$CLI" open-worktrees 2>&1)
if [ $? -ne 0 ]; then
  echo "preserve-dead-lanes: the ledger is unreadable -- cannot sweep what it does not have" >&2
  sed 's/^/  /' <<<"$TASKS_JSON" >&2
  exit 2
fi

# One TSV line per in-flight task: id, lane, worktree_path (tab-separated;
# `summary` is a whole brief and is fetched per-task below only when a
# commit message is actually being built, not pulled through this list).
mapfile -t ROWS < <("$JSON_PYTHON" - "$TASKS_JSON" <<'PYEOF'
import json, sys
data = json.loads(sys.argv[1])
for t in data.get("tasks", []):
    wt = t.get("worktree_path") or ""
    if not wt:
        continue
    print("\t".join([t.get("id", ""), t.get("lane", ""), wt]))
PYEOF
)

SELF_REAL=$(cd "$HERE/../.." 2>/dev/null && pwd -P) || SELF_REAL=""

examined=0
dirty=0
dead=0
preserved=0
push_failed=0

for row in "${ROWS[@]}"; do
  [ -n "$row" ] || continue
  IFS=$'\t' read -r TASK_ID LANE WT <<<"$row"
  examined=$((examined + 1))

  if [ ! -d "$WT" ]; then
    echo "preserve-dead-lanes: skip $LANE ($TASK_ID) -- worktree $WT does not exist" >&2
    continue
  fi

  STATUS=$(git -C "$WT" status --porcelain 2>&1)
  if [ -z "$STATUS" ]; then
    continue
  fi
  dirty=$((dirty + 1))

  WT_REAL=$(cd "$WT" 2>/dev/null && pwd -P) || WT_REAL="$WT"
  if [ -n "$SELF_REAL" ] && [ "$WT_REAL" = "$SELF_REAL" ]; then
    echo "preserve-dead-lanes: skip $LANE ($TASK_ID) -- this is the worktree the sweep itself is running from" >&2
    continue
  fi

  reason=$(_pdl_is_live "$WT")
  if [ $? -eq 0 ]; then
    echo "preserve-dead-lanes: skip $LANE ($TASK_ID) -- LIVE ($reason), dirty tree left alone" >&2
    continue
  fi
  dead=$((dead + 1))

  BRANCH=$(git -C "$WT" symbolic-ref -q --short HEAD 2>/dev/null)
  if [ -z "$BRANCH" ]; then
    echo "preserve-dead-lanes: skip $LANE ($TASK_ID) -- detached HEAD, refusing to guess a branch to push" >&2
    continue
  fi
  case "$BRANCH" in
    main|master)
      echo "preserve-dead-lanes: skip $LANE ($TASK_ID) -- branch is '$BRANCH', never committing to main" >&2
      continue
      ;;
  esac

  newest=$(_pdl_newest_mtime "$WT")
  age_desc="unknown age"
  if [ -n "$newest" ] && [ "$newest" != "0" ]; then
    now=$(date +%s)
    age=$(((now - newest) / 60))
    age_desc="newest changed file is ~${age}m old"
  fi

  ISSUE=""
  case "$LANE" in
    as[0-9]*)
      ISSUE="${LANE#as}"
      ISSUE="${ISSUE%%-*}"
      ;;
  esac
  if [[ "$ISSUE" =~ ^[0-9]+$ ]]; then
    SUBJECT_REF=" (agent-supervisor#$ISSUE)"
  else
    SUBJECT_REF=""
    ISSUE=""
  fi

  FILES=$(git -C "$WT" status --porcelain | awk '{print $2}')
  AREA=$(awk -F/ '{print $2; exit}' <<<"$FILES")
  [ -n "$AREA" ] || AREA="misc"
  FILE_COUNT=$(wc -l <<<"$FILES" | tr -d ' ')

  SUBJECT="wip($AREA): preserve uncommitted work from $LANE$SUBJECT_REF"
  BODY="Preservation commit made by the estate loop, not by the lane. The lane
was dispatched as $LANE, its worktree at $WT is dirty ($FILE_COUNT file(s)
changed), and it is provably dead: $reason. $age_desc.

Incomplete and unverified: tests not run, nothing mutation-checked, the
change has not been reviewed by anyone, human or agent. A lane must finish
this before it goes anywhere near a merge."

  if [ -n "$DRY" ]; then
    echo "preserve-dead-lanes: would preserve $LANE ($TASK_ID) -- branch $BRANCH, $FILE_COUNT file(s):" >&2
    echo "$FILES" | sed 's/^/    /' >&2
    echo "  --- would commit ---" >&2
    echo "  $SUBJECT" >&2
    echo "$BODY" | sed 's/^/  /' >&2
    preserved=$((preserved + 1))
    continue
  fi

  if ! git -C "$WT" add -A; then
    echo "preserve-dead-lanes: ERROR -- git add -A failed for $LANE ($TASK_ID) at $WT" >&2
    continue
  fi
  if ! git -C "$WT" commit -q -m "$SUBJECT" -m "$BODY"; then
    echo "preserve-dead-lanes: ERROR -- commit failed for $LANE ($TASK_ID) at $WT" >&2
    continue
  fi
  echo "preserve-dead-lanes: preserved $LANE ($TASK_ID) -- committed $FILE_COUNT file(s) on $BRANCH" >&2

  UPSTREAM=$(git -C "$WT" rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>/dev/null)
  PUSH_OUT=""
  if [ -n "$UPSTREAM" ]; then
    PUSH_OUT=$(git -C "$WT" push 2>&1)
  else
    PUSH_OUT=$(git -C "$WT" push -u origin "$BRANCH" 2>&1)
  fi
  if [ $? -ne 0 ]; then
    echo "preserve-dead-lanes: ERROR -- push failed for $LANE ($TASK_ID), branch $BRANCH -- the commit is safe LOCALLY at $WT but NOT yet durable:" >&2
    echo "$PUSH_OUT" | sed 's/^/  /' >&2
    push_failed=$((push_failed + 1))
    continue
  fi
  echo "preserve-dead-lanes: pushed $BRANCH" >&2
  preserved=$((preserved + 1))
done

if [ -n "$DRY" ]; then
  echo "preserve-dead-lanes: dry run done -- examined $examined open worktree(s), $dirty dirty, $dead provably dead, would preserve $preserved" >&2
else
  echo "preserve-dead-lanes: done -- examined $examined open worktree(s), $dirty dirty, $dead provably dead, preserved $preserved, $push_failed push failure(s)" >&2
fi

[ "$push_failed" -eq 0 ] || exit 1
exit 0
