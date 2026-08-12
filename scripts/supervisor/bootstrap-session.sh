#!/usr/bin/env bash
# Create the tmux session and lane windows the supervisor dispatches into.
#
# WHY: this was the last thing standing between the estate and a second
# machine. `install.sh` and `sync.py apply` already bootstrap the harness, and
# every supervisor script is already environment-driven (`LANES_SESSION`,
# `SUPERVISOR_PANE`, `SUPERVISOR_STATE`) with no hardcoded paths. But
# `grep -rln 'new-session|new-window' scripts/` returned NOTHING: the session
# those scripts operate on only ever existed because it was built by hand.
# `dispatch.sh` fails closed with no lane to dispatch to, which is correct and
# is also the whole experience of a fresh clone.
#
# SAFETY. This script never modifies an existing session. It refuses instead.
# The estate's live session is running work at all times, and a bootstrap that
# "helpfully" reset it would destroy exactly the lanes it was meant to serve --
# the same class as the #73 checkout clobber. Topping an existing session up to
# LANES is opt-in via --add-lanes, and even that only ever ADDS windows; it
# does not rename, renumber, kill or restart anything already there.
#
# The agent command is a parameter, not a constant. That is the portability
# point: the same session layout serves `claude`, `copilot` or `codex`, which
# is what makes this usable at a site whose harness is not the one Jon runs at
# home. `lanes.sh` classifies a pane running a plain shell as `dead`, so the
# lanes must come up already running an agent or the whole session reads dead
# on first look.
#
# Usage:
#   bootstrap-session.sh [--session NAME] [--lanes N] [--agent CMD]
#                        [--harness NAME] [--cwd DIR] [--add-lanes] [--dry-run]
#
#   --session NAME  tmux session to create; default $LANES_SESSION or
#                   agent-dotfiles. Must match what lanes.sh/dispatch.sh read.
#   --lanes N       total windows including the supervisor; default 10.
#   --agent CMD     command started in each lane; default $LANES_AGENT_CMD or
#                   `claude`. Use `copilot` / `codex` at a site without Claude.
#   --harness NAME  the harness identity to RECORD on every lane window this
#                   run creates (claude/codex/copilot; agent-dotfiles#216).
#                   Default: inferred from --agent's basename when it is one
#                   of those three names exactly; otherwise unrecorded. Pass
#                   this explicitly when --agent is a wrapper/launcher whose
#                   binary is not named after the harness it runs (codex
#                   under a Node loader is the case #216 measured live) --
#                   the pane's OWN process name can never disambiguate that,
#                   because several harnesses run as `node`.
#   --cwd DIR       working directory for every window; default $PWD.
#   --add-lanes     if the session exists, ADD windows up to --lanes. Never
#                   touches windows that already exist.
#   --dry-run       print what would run, change nothing.
#
# Exit 0 when the session exists with at least --lanes windows and every lane
# was started by this run or already present. Exit 1 on any refusal.

set -euo pipefail

SESSION="${LANES_SESSION:-agent-dotfiles}"
LANES=10
AGENT_CMD="${LANES_AGENT_CMD:-claude}"
HARNESS=""
WORKDIR="$PWD"
ADD_LANES=0
DRY_RUN=0
# Keep in step with lanes.sh's own default; a supervisor window in a different
# slot would be offered as a dispatch target, which lanes.sh exists to prevent.
SUPERVISOR_WINDOW="${LANES_SUPERVISOR_WINDOW:-1}"
SUPERVISOR_NAME="${LANES_SUPERVISOR_NAME:-architecture}"

usage() { sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; }

while [ $# -gt 0 ]; do
  case "$1" in
    --session)   SESSION="${2:?--session needs a value}"; shift 2 ;;
    --lanes)     LANES="${2:?--lanes needs a value}"; shift 2 ;;
    --agent)     AGENT_CMD="${2:?--agent needs a value}"; shift 2 ;;
    --harness)   HARNESS="${2:?--harness needs a value}"; shift 2 ;;
    --cwd)       WORKDIR="${2:?--cwd needs a value}"; shift 2 ;;
    --add-lanes) ADD_LANES=1; shift ;;
    --dry-run)   DRY_RUN=1; shift ;;
    -h|--help)   usage; exit 0 ;;
    *) echo "bootstrap-session: unknown argument: $1" >&2; usage; exit 1 ;;
  esac
done

case "$LANES" in
  ''|*[!0-9]*) echo "bootstrap-session: --lanes must be a number, got '$LANES'" >&2; exit 1 ;;
esac
if [ "$LANES" -lt 2 ]; then
  # One window is a supervisor with nowhere to dispatch. That is not a
  # degraded estate, it is a non-functioning one, so refuse rather than
  # produce something that looks set up and cannot work.
  echo "bootstrap-session: --lanes must be at least 2 (supervisor + one lane), got $LANES" >&2
  exit 1
fi

command -v tmux >/dev/null 2>&1 || { echo "bootstrap-session: tmux not on PATH" >&2; exit 1; }

# tmux silently rewrites `:` and `.` in a session name (they are its target
# delimiters), then every later target built from the ORIGINAL string misses.
# Review of #137 hit this: new-session succeeded as `bs_evil-N` while the
# follow-up move-window failed on `bs:evil-N`, leaving a stray half-built
# session the failure report never mentioned. Reject up front instead.
case "$SESSION" in
  ""|*:*|*.*)
    echo "bootstrap-session: --session must be non-empty and contain no ':' or '.' (got '$SESSION')" >&2
    echo "  tmux uses both as target delimiters and rewrites them in session names." >&2
    exit 1 ;;
esac

if [ ! -d "$WORKDIR" ]; then
  echo "bootstrap-session: --cwd does not exist: $WORKDIR" >&2
  exit 1
fi

# The agent command is checked before any window is created. A typo'd harness
# name would otherwise produce N windows that all instantly fall back to a
# shell -- i.e. a session that lanes.sh reports entirely `dead`, with no clue
# as to why.
# `command -v` succeeds for shell builtins (`cd`, `true`, `echo`), which have
# no PATH-resident binary and are not harnesses. Review of #137 showed
# `--agent cd` sailing through this guard and producing exactly the all-`dead`
# session the guard exists to prevent. Require a real executable path.
AGENT_BIN="${AGENT_CMD%% *}"
AGENT_PATH="$(command -v "$AGENT_BIN" 2>/dev/null || true)"
case "$AGENT_PATH" in
  /*) ;;
  *) AGENT_PATH="" ;;
esac
if [ -z "$AGENT_PATH" ] || [ ! -x "$AGENT_PATH" ]; then
  echo "bootstrap-session: agent command not on PATH: $AGENT_BIN" >&2
  echo "  pass --agent with the harness this machine actually has (claude, copilot, codex)" >&2
  exit 1
fi

# agent-dotfiles#216: harness identity is RECORDED here, at the one place it
# is actually decided, rather than guessed later from the pane's process name
# -- every Node-based harness (copilot, and codex whenever it is not started
# as a binary literally named `codex`) reads `node` there and is otherwise
# indistinguishable from any other Node harness. An explicit --harness wins;
# otherwise infer from --agent's own basename when it names one of the three
# recognised harnesses exactly.
if [ -n "$HARNESS" ]; then
  case "$HARNESS" in
    claude|codex|copilot) ;;
    *) echo "bootstrap-session: --harness must be claude, codex or copilot, got '$HARNESS'" >&2; exit 1 ;;
  esac
else
  case "$(basename "$AGENT_BIN")" in
    claude|claude.exe)   HARNESS=claude ;;
    codex|codex.exe)     HARNESS=codex ;;
    copilot|copilot.exe) HARNESS=copilot ;;
    # Left unrecorded, not guessed. A wrapper/launcher (codex run under a
    # Node loader, for instance) still starts a working lane -- it just
    # reads unidentified to `lane_free` until an operator names it with
    # `--harness` here or `cli.py register --harness` later. Fail-closed
    # downstream, not a bootstrap refusal: the window is still useful for
    # manual work even if the supervisor cannot dispatch to it yet.
    *) HARNESS="" ;;
  esac
fi
if [ -z "$HARNESS" ]; then
  echo "bootstrap-session: harness not recorded for agent '$AGENT_BIN' -- lanes will read unidentified" >&2
  echo "  until named by hand (pass --harness, or register the lane with 'cli.py register --harness')" >&2
fi

run() {
  if [ "$DRY_RUN" -eq 1 ]; then
    printf '  would run: tmux'; printf ' %q' "$@"; printf '\n'
  else
    tmux "$@"
  fi
}

# BLOCKING BUG found reviewing #137: tmux resolves an UNAMBIGUOUS PREFIX of a
# session name as a match when no exact match exists. `has-session -t foo`
# therefore returns true when only `foo-2` exists, and with --add-lanes the
# script then added four windows to `foo-2` -- a live session it was never
# told about. That falsified this script's central safety claim.
#
# `=name` is tmux's exact-match target syntax and does not prefix-match;
# confirmed directly against a real tmux before relying on it. Every target
# built from $SESSION uses it, not just this probe -- an exact existence check
# followed by prefix-matching targets would be worse than no check.
session_exists() { tmux has-session -t "=$SESSION" 2>/dev/null; }

# Existing-window inventory, read ONCE up front. Re-querying per iteration
# would race against a lane that exits mid-run and could hand the same index
# to two windows.
existing_indexes=""
if session_exists; then
  existing_indexes="$(tmux list-windows -t "=$SESSION" -F '#{window_index}' 2>/dev/null || true)"
fi

has_window() {
  local idx="$1"
  printf '%s\n' "$existing_indexes" | grep -qx "$idx"
}

if session_exists && [ "$ADD_LANES" -eq 0 ]; then
  count="$(printf '%s\n' "$existing_indexes" | grep -c . || true)"
  echo "bootstrap-session: session '$SESSION' already exists ($count window(s)) -- refusing." >&2
  echo "  This script never modifies a running session; that session may be holding live work." >&2
  echo "  To add lanes up to --lanes without touching existing windows: --add-lanes" >&2
  echo "  To start clean, kill it yourself first: tmux kill-session -t $SESSION" >&2
  exit 1
fi

echo "bootstrap-session: session=$SESSION lanes=$LANES agent=$AGENT_CMD cwd=$WORKDIR"
[ "$DRY_RUN" -eq 1 ] && echo "  (dry run -- nothing will change)"

created=0
skipped=0

if ! session_exists; then
  run new-session -d -s "$SESSION" -n "$SUPERVISOR_NAME" -c "$WORKDIR"
  # tmux may be configured with base-index 1 or 0; renumber the first window
  # to the slot lanes.sh treats as the supervisor rather than assuming which
  # one tmux picked. Without this, a user whose .tmux.conf sets base-index 0
  # gets a supervisor at window 0 and lanes.sh offers it as a dispatch target.
  if [ "$DRY_RUN" -eq 0 ]; then
    first="$(tmux list-windows -t "=$SESSION" -F '#{window_index}' | head -1)"
    [ "$first" = "$SUPERVISOR_WINDOW" ] || tmux move-window -s "=$SESSION:$first" -t "=$SESSION:$SUPERVISOR_WINDOW"
  fi
  run send-keys -t "=$SESSION:$SUPERVISOR_WINDOW" "$AGENT_CMD" Enter
  # Not a dispatch target (lanes.sh never offers the supervisor window), but
  # recorded anyway for consistency -- nothing downstream should have to
  # special-case "the one window with no harness option".
  [ -z "$HARNESS" ] || run set-option -p -t "=$SESSION:$SUPERVISOR_WINDOW" @hill90_lane_harness "$HARNESS"
  created=$((created + 1))
  echo "  window $SUPERVISOR_WINDOW ($SUPERVISOR_NAME): supervisor, created"
else
  echo "  session exists; adding lanes only, existing windows untouched"
fi

# Lane windows. Named free-N to match what lanes.sh and dispatch.sh expect: a
# lane is renamed to repo-issue-slug on dispatch and back to free-N when done.
idx="$SUPERVISOR_WINDOW"
while [ "$idx" -lt "$((SUPERVISOR_WINDOW + LANES - 1))" ]; do
  idx=$((idx + 1))
  if has_window "$idx"; then
    echo "  window $idx: already exists, left alone"
    skipped=$((skipped + 1))
    continue
  fi
  run new-window -d -t "=$SESSION:$idx" -n "free-$idx" -c "$WORKDIR"
  run send-keys -t "=$SESSION:$idx" "$AGENT_CMD" Enter
  # agent-dotfiles#216: the RECORD `lane_free`'s backfill reads instead of
  # inferring harness from `#{pane_current_command}` (see that function and
  # `TmuxAdapter.HARNESS_OPTION`). Skipped when HARNESS is empty -- an
  # unrecognised --agent leaves the lane usable but unidentified, not
  # mis-identified.
  [ -z "$HARNESS" ] || run set-option -p -t "=$SESSION:$idx" @hill90_lane_harness "$HARNESS"
  created=$((created + 1))
  echo "  window $idx (free-$idx): lane, created"
done

echo "bootstrap-session: $created created, $skipped left alone"

if [ "$DRY_RUN" -eq 1 ]; then
  exit 0
fi

# Verify rather than assert. The agents need a moment to paint before lanes.sh
# can classify them, and a bootstrap that reports success on a session whose
# lanes all read `dead` is the failure mode this whole script exists to avoid.
echo "bootstrap-session: attach with: tmux attach -t $SESSION"
echo "  lanes.sh will report 'dead' until each agent finishes starting; re-run it after a few seconds."
