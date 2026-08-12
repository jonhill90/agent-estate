#!/bin/bash
# Bring every lane back after a tmux server loss. One command, ledger-driven.
#
# agent-dotfiles#237. The incident this exists for: the tmux server died with
# nine lanes mid-task. Something restored the SESSIONS -- names, window counts,
# layout -- and every pane came back a dead `zsh` carrying a window name from
# hours earlier. `lanes.sh` reported nine lanes whose names described finished
# work; an operator read those names as current and respawned the windows,
# killing the live agents that had by then been resumed by hand. The restore
# recovered the label and lost the thing the label described.
#
# So this script reads the LEDGER and never a window name. What it restores
# per lane -- which conversation, under which name, in which directory --
# comes from `cli.py restore-plan`. Window names are written, never read: they
# are a projection of the record, which is the repository's "tmux is not a
# database" rule (AGENTS.md) applied to identity rather than availability.
#
# THE FAILURE DIRECTION IS REFUSE, NEVER INVENT. A lane whose harness session
# id is missing, whose harness has no resume dialect, or whose transcript is
# gone from disk is REPORTED UNRECOVERABLE and left alone. Starting a fresh
# agent in its place is the worst outcome available: it looks fully recovered,
# wears the right window name, and has none of the context. #237 names that as
# the primary design constraint and this script's exit code carries it -- 2
# means "some lane could not be brought back", and that is not a crash.
#
# IT NEVER KILLS ANYTHING. No `respawn-window`, no `-k`, no `kill-window`. A
# window whose pane is running an agent is skipped as already live, which is
# also what makes a second run a no-op. A window whose pane is a shell is
# adopted: the resume command is typed into that shell, so the pane, its
# scrollback and its index survive.
#
# Usage: restore.sh [--session NAME] [--dry-run]
#
# Exit 0  every lane the ledger knows is live or was restored
#      1  usage / no plan could be read
#      2  at least one lane is unrecoverable (reported, not restarted)

set -uo pipefail

RESTORE_HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./harness-registry.sh
. "$RESTORE_HERE/harness-registry.sh"

SESSION_OVERRIDE=""
DRY_RUN=0
while [ $# -gt 0 ]; do
  case "$1" in
    --session) SESSION_OVERRIDE="${2:-}"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) sed -n '1,40p' "$0"; exit 0 ;;
    *) echo "restore: unknown argument '$1'" >&2; exit 1 ;;
  esac
done

PYTHON="${RESTORE_PYTHON:-python3}"
TMUX_BIN="${AGENT_TMUX_BIN:-tmux}"
# Same shell set `lanes.sh` calls `dead`. A pane running one of these is
# wreckage to be adopted; a pane running anything else is left strictly alone.
SHELLS="bash|zsh|sh|fish|login"
HOME_DIR="${HOME:-}"

# The plan, as TSV. `restore-plan` prints JSON because that is what every
# other `cli.py` read prints; this converts rather than adding a second output
# format to the ledger for one caller's convenience.
PLAN=$("$PYTHON" "$RESTORE_HERE/cli.py" restore-plan 2>&1) || {
  echo "restore: could not read the ledger plan" >&2
  sed 's/^/  /' <<<"$PLAN" >&2
  exit 1
}
# FS is the ASCII unit separator (\x1f), not a tab. Bash's `read` treats any
# run of pure-whitespace IFS characters (space, tab, newline) as ONE
# delimiter and drops empty fields next to it -- so a tab-joined row with an
# unresolved (empty) harness_session_id, exactly the case this script exists
# to refuse, silently shifts every field after it one column left. Measured:
# `IFS=$'\t' read -r a b c <<<"$(printf 'x\t\ty\n')"` yields a=x b=y c=
# (empty), not the empty middle field. \x1f is not in that whitespace class,
# so empty fields are preserved exactly.
FS=$'\x1f'
ROWS=$("$PYTHON" - "$PLAN" <<PY
import json, sys
FS = "\x1f"
for row in json.loads(sys.argv[1]):
    print(FS.join(str(row.get(field) or "") for field in
                    ("lane", "harness", "harness_session_id", "repo", "task")))
PY
) || { echo "restore: the ledger plan is not readable JSON" >&2; exit 1; }

if [ -z "$ROWS" ]; then
  echo "restore: the ledger knows no lanes -- nothing to restore"
  exit 0
fi

unrecoverable=0
restored=0
live=0

# Report and DO NOTHING. Every path that cannot positively identify what a
# lane was doing ends here, by design: see the header.
refuse() {
  printf '%-24s %-14s %s\n' "$1" "UNRECOVERABLE" "$2"
  unrecoverable=$((unrecoverable + 1))
}

while IFS="$FS" read -r lane harness session_id repo task; do
  [ -n "$lane" ] || continue
  # `lane` is `session:index`, the shape `lanes.sh --free` prints and
  # `dispatch.sh` records -- never a window name, which is not unique (the
  # live session has held two windows with the same name).
  target_session="${lane%%:*}"
  index="${lane##*:}"
  [ -n "$SESSION_OVERRIDE" ] && target_session="$SESSION_OVERRIDE"
  if [ "$target_session" = "$lane" ] || [ -z "$index" ]; then
    refuse "$lane" "lane id is not session:index -- cannot place it"
    continue
  fi

  # A lane with no open task has no conversation to lose. It is still a lane
  # the estate needs back, so it is restored FRESH -- and that is not
  # inventing anything, because the ledger says there is nothing to resume.
  # Named `free-N`, the convention `lane-free`'s backfill already reads.
  want_name="${task:-free-$index}"
  resume=""
  if [ -n "$task" ]; then
    if [ -z "$session_id" ]; then
      refuse "$lane" "no harness session id recorded for task '$task'"
      continue
    fi
    if ! hidx=$(harness_index_for_name "$harness"); then
      refuse "$lane" "no harness adapter named '$harness'"
      continue
    fi
    if [ -z "${H_RESUME_CMD[$hidx]}" ]; then
      refuse "$lane" "harness '$harness' has no resume dialect -- task '$task' cannot be resumed"
      continue
    fi
    # The transcript must actually be on disk. This is the check that catches
    # a session id that was corrupted, truncated, or whose file was deleted --
    # the #237 mutation case. Without it, `claude --resume <garbage>` starts
    # something, and what it starts wears this lane's name.
    if [ -z "$HOME_DIR" ] || ! compgen -G "$HOME_DIR/.claude/projects/*/$session_id.jsonl" >/dev/null 2>&1; then
      refuse "$lane" "no transcript on disk for session $session_id -- task '$task' cannot be resumed"
      continue
    fi
    # shellcheck disable=SC2059
    resume=$(printf "${H_RESUME_CMD[$hidx]}" "$session_id")
  else
    if hidx=$(harness_index_for_name "$harness"); then
      resume="${H_LAUNCH_CMD[$hidx]}"
    fi
    if [ -z "$resume" ]; then
      refuse "$lane" "harness '$harness' has no launch command -- cannot start this free lane"
      continue
    fi
  fi

  cwd="$repo"
  [ -d "$cwd" ] || cwd="$HOME_DIR"

  if [ "$DRY_RUN" = 1 ]; then
    printf '%-24s %-14s %s\n' "$target_session:$index" "WOULD-RESTORE" "$want_name <- $resume"
    restored=$((restored + 1))
    continue
  fi

  if ! "$TMUX_BIN" has-session -t "=$target_session" 2>/dev/null; then
    if ! "$TMUX_BIN" new-session -d -s "$target_session" -c "$cwd" 2>/dev/null; then
      refuse "$lane" "could not create session '$target_session'"
      continue
    fi
  fi

  # `display-message -t "=sess:$index"` does NOT fail when window $index does
  # not exist -- it silently answers for the session's *current* window
  # instead, so a phantom window read as a live shell. List the real windows
  # and match the index exactly before ever asking a target to describe
  # itself; that is the only way to tell "absent" from "present and a shell".
  pane_cmd=""
  if "$TMUX_BIN" list-windows -t "=$target_session" -F '#{window_index}' 2>/dev/null | grep -qx "$index"; then
    pane_cmd=$("$TMUX_BIN" display-message -p -t "=$target_session:$index" '#{pane_current_command}' 2>/dev/null)
  fi
  if [ -n "$pane_cmd" ] && ! [[ "$pane_cmd" =~ ^($SHELLS)$ ]]; then
    # Something is already running there. NEVER touched -- this is both the
    # idempotence property and the #237 harm (nine live agents killed by a
    # respawn) refusing to happen again.
    printf '%-24s %-14s %s\n' "$target_session:$index" "LIVE" "$pane_cmd is running -- left alone"
    live=$((live + 1))
    continue
  fi

  if [ -z "$pane_cmd" ]; then
    # No `=` prefix here, and that is not cosmetic: `=name` is tmux's
    # EXACT-MATCH-AN-EXISTING-target syntax, so `new-window -t "=sess:2"`
    # refuses to create the window it is being asked to create. Every other
    # call in this script keeps the `=` precisely because it must address a
    # window that already exists and must never create one by accident.
    if ! "$TMUX_BIN" new-window -d -t "$target_session:$index" -n "$want_name" -c "$cwd" 2>/dev/null; then
      refuse "$lane" "could not create window $index in '$target_session'"
      continue
    fi
  fi

  # The rename is a PROJECTION of the ledger, written after the fact and read
  # by nobody here.
  "$TMUX_BIN" rename-window -t "=$target_session:$index" "$want_name" 2>/dev/null
  if ! "$TMUX_BIN" send-keys -t "=$target_session:$index" "$resume" Enter 2>/dev/null; then
    refuse "$lane" "could not send the resume command to $target_session:$index"
    continue
  fi
  printf '%-24s %-14s %s\n' "$target_session:$index" "RESTORED" "$want_name <- $resume"
  restored=$((restored + 1))
done <<<"$ROWS"

echo
echo "restore: $restored restored, $live already live, $unrecoverable unrecoverable"
if [ "$unrecoverable" -gt 0 ]; then
  echo "restore: an unrecoverable lane is NOT restarted -- a fresh agent wearing its name would look recovered and have none of its context (agent-dotfiles#237)" >&2
  exit 2
fi
exit 0
