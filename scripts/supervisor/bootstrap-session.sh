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
#                   agent-supervisor. Must match what lanes.sh/dispatch.sh read
#                   for the target lane -- since agent-supervisor#111,
#                   dispatch.sh derives that per repo (a lane working in
#                   jonhill90/X runs in session X), so bootstrapping a
#                   session for a repo that has none means passing --session
#                   with that repo's name explicitly, not relying on this
#                   default (which stays the shared fallback for non-repo
#                   callers, e.g. the poller).
#   --lanes N       total windows including the supervisor; default 10.
#   --agent CMD     command started in each lane; default $LANES_AGENT_CMD, or
#                   (agent-supervisor#135) the harness registry's `claude`
#                   launch command -- the same one dispatch.sh resolves, so
#                   this script carries no launch literal of its own. Use
#                   `copilot` / `codex` at a site without Claude.
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
#   --unattended    replace --agent (or its default) with the target
#                   harness's OWN recorded unattended launch command --
#                   agent-dotfiles#256. Opt-in only, never inferred: maps a
#                   BARE harness name (claude/codex/copilot, from --agent or
#                   from its no-agent default) to that harness's measured
#                   no-human-prompt launch line. Refuses, naming what it does
#                   not know, when --agent is a full command line (its
#                   unattended posture cannot be inferred) or when the target
#                   harness has no such command recorded (harness/*.sh's
#                   HARNESS_UNATTENDED_CMD is unset) -- a wrong guess here
#                   disables a safety prompt, which is worse than a lane that
#                   still asks.
#   --dry-run       print what would run, change nothing.
#
# Exit 0 when the session exists with at least --lanes windows and every lane
# was started by this run or already present. Exit 1 on any refusal.

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./session-defaults.sh
. "$HERE/session-defaults.sh"
SESSION="$(lanes_session_or_default)"
LANES=10
# agent-supervisor#135: no bare 'claude' literal here. `dispatch.sh` was fixed
# by #122 to type the harness registry's H_LAUNCH_CMD (harness/claude.sh's
# `claude --model ${CLAUDE_LANE_MODEL:-sonnet} ...`); this script carried its
# own literal default and was not, so any session bootstrapped with no
# --agent started every lane on the account default (Opus) -- the #135 leak.
# LANES_AGENT_CMD stays a real override (it existed before #135 as the
# documented way to hand this script a whole command, e.g. at a site running
# codex/copilot); when it is unset AND --agent is not passed, AGENT_CMD is
# left EMPTY here and resolved after option parsing from the harness
# registry -- the same H_LAUNCH_CMD dispatch.sh resolves -- so there is one
# definition of how a Claude lane starts, not two that can drift apart again.
AGENT_CMD="${LANES_AGENT_CMD:-}"
HARNESS=""
WORKDIR="$PWD"
ADD_LANES=0
DRY_RUN=0
UNATTENDED=0
# Keep in step with lanes.sh's own default; a supervisor window in a different
# slot would be offered as a dispatch target, which lanes.sh exists to prevent.
SUPERVISOR_WINDOW="${LANES_SUPERVISOR_WINDOW:-1}"
# as#132: "architecture" was a leftover name from an earlier era that misled
# every new reader. "supervisor" is the default going forward; an estate
# already running under LANES_SUPERVISOR_NAME=architecture is unaffected --
# this only changes the default for a FRESH bootstrap, never an existing
# session (see the SAFETY note above).
SUPERVISOR_NAME="${LANES_SUPERVISOR_NAME:-supervisor}"

usage() { sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; }

while [ $# -gt 0 ]; do
  case "$1" in
    --session)   SESSION="${2:?--session needs a value}"; shift 2 ;;
    --lanes)     LANES="${2:?--lanes needs a value}"; shift 2 ;;
    --agent)     AGENT_CMD="${2:?--agent needs a value}"; shift 2 ;;
    --harness)   HARNESS="${2:?--harness needs a value}"; shift 2 ;;
    --cwd)       WORKDIR="${2:?--cwd needs a value}"; shift 2 ;;
    --add-lanes) ADD_LANES=1; shift ;;
    --unattended) UNATTENDED=1; shift ;;
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

# agent-dotfiles#256: --unattended resolves BEFORE the no-agent default below,
# on the RAW value of --agent/LANES_AGENT_CMD -- once the default-resolution
# block runs, AGENT_CMD is already a full command line (claude's registry
# default), and bare-name detection here would never fire. Refuses rather
# than guesses: a wrong flag produces a lane that will not start, which #256
# calls out as worse than one that still prompts.
if [ "$UNATTENDED" -eq 1 ]; then
  UNATTENDED_TARGET=""
  case "$HARNESS" in
    claude|codex|copilot) UNATTENDED_TARGET="$HARNESS" ;;
  esac
  if [ -n "$AGENT_CMD" ]; then
    case "$AGENT_CMD" in
      claude|codex|copilot) UNATTENDED_TARGET="$AGENT_CMD" ;;
      *)
        echo "bootstrap-session: --unattended requires --agent to be a bare harness name (claude, codex, copilot), got '$AGENT_CMD'" >&2
        echo "  a full command line's unattended posture cannot be inferred -- pass --agent as the bare harness name, or drop --unattended" >&2
        exit 1
        ;;
    esac
  fi
  # No --agent and no --harness: match this script's OWN no-agent default
  # (claude), so --unattended alone behaves the same as the plain no-agent
  # path, just resolved through H_UNATTENDED_CMD instead of H_LAUNCH_CMD.
  [ -n "$UNATTENDED_TARGET" ] || UNATTENDED_TARGET=claude

  # shellcheck source=./harness-registry.sh
  . "$HERE/harness-registry.sh"
  _unattended_hidx="$(harness_index_for_name "$UNATTENDED_TARGET" || true)"
  if [ -z "$_unattended_hidx" ] || [ -z "${H_UNATTENDED_CMD[$_unattended_hidx]:-}" ]; then
    echo "bootstrap-session: no unattended launch command recorded for harness '$UNATTENDED_TARGET'" >&2
    echo "  check harness/$UNATTENDED_TARGET.sh -- either nobody has measured this harness unattended yet, or HARNESS_UNATTENDED_CMD needs setting there" >&2
    exit 1
  fi
  AGENT_CMD="${H_UNATTENDED_CMD[$_unattended_hidx]}"
  HARNESS="$UNATTENDED_TARGET"
  unset _unattended_hidx UNATTENDED_TARGET
fi

# agent-supervisor#135: neither LANES_AGENT_CMD nor --agent supplied a
# command, so ask the harness registry for the one dispatch.sh already
# resolves rather than falling back to a literal here. `HARNESS_REGISTRY_DIR`
# is honoured (not hardcoded to $HERE/harness) so a test can point this at a
# mutated adapter set the same way lanes.sh's/dispatch.sh's own tests do.
if [ -z "$AGENT_CMD" ]; then
  # shellcheck source=./harness-registry.sh
  . "$HERE/harness-registry.sh"
  _default_hidx="$(harness_index_for_name claude || true)"
  if [ -z "$_default_hidx" ] || [ -z "${H_LAUNCH_CMD[$_default_hidx]:-}" ]; then
    echo "bootstrap-session: no launch command recorded for harness 'claude' in $HARNESS_DIR" >&2
    echo "  pass --agent explicitly, or fix harness/claude.sh's HARNESS_LAUNCH_CMD" >&2
    exit 1
  fi
  AGENT_CMD="${H_LAUNCH_CMD[$_default_hidx]}"
  unset _default_hidx
fi

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
#
# agent-supervisor#521: harness/claude.sh's HARNESS_LAUNCH_CMD/
# HARNESS_RESUME_CMD now lead with an inline `VAR=value` env assignment
# (CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false) ahead of the real binary --
# a bare `${AGENT_CMD%% *}` took THAT token for AGENT_BIN, so every no-
# --agent bootstrap started failing this guard with "agent command not on
# PATH: CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false" (measured live, #522).
# Skip any number of leading `NAME=value` assignment words first, the same
# way a shell resolves the command of a simple command line with a
# temporary environment prefix, so this guard checks the actual binary
# regardless of how many env vars a harness's launch command carries.
_agent_bin_search="$AGENT_CMD"
while [[ "$_agent_bin_search" =~ ^[A-Za-z_][A-Za-z0-9_]*=[^[:space:]]*([[:space:]]+.*)?$ ]]; do
  _agent_bin_search="${_agent_bin_search#*[[:space:]]}"
done
AGENT_BIN="${_agent_bin_search%% *}"
unset _agent_bin_search
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

# agent-supervisor#166: every lane's agent process gets a `tmux` wrapper
# ahead of the real binary on its PATH, so a bare `tmux kill-server` (or an
# untargeted kill-session/kill-window) TYPED into the lane's own shell is
# refused -- not just a call that reaches a script that calls
# assert_isolated_tmux (tmux-isolation.sh cannot see a command an agent
# decides to type). See tmux-guard.sh for the isolation rule and why. Best
# effort: a guard install failure degrades to launching without the guard
# (the pre-#166 behaviour) rather than failing the whole bootstrap.
# shellcheck source=./tmux-guard.sh
. "$HERE/tmux-guard.sh"
GUARD_BIN=""
if [ "$DRY_RUN" -eq 0 ]; then
  GUARD_BIN="$(install_tmux_guard 2>&2)" || GUARD_BIN=""
fi
LAUNCH_CMD="$AGENT_CMD"
if [ -n "$GUARD_BIN" ]; then
  LAUNCH_CMD="PATH=\"$GUARD_BIN:\$PATH\" $AGENT_CMD"
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
  run send-keys -t "=$SESSION:$SUPERVISOR_WINDOW" "$LAUNCH_CMD" Enter
  # Not a dispatch target (lanes.sh never offers the supervisor window), but
  # recorded anyway for consistency -- nothing downstream should have to
  # special-case "the one window with no harness option".
  [ -z "$HARNESS" ] || run set-option -p -t "=$SESSION:$SUPERVISOR_WINDOW" @hill90_lane_harness "$HARNESS"
  # agent-supervisor#153: this is the ONLY place a fresh clone's ledger ever
  # gets a `sessions` row -- the write side of "supervised vs Jon's own".
  # Only on the branch that actually creates a session, never on
  # --add-lanes against one that already exists: --add-lanes has no opinion
  # on supervision, and a pre-existing session (Jon's own, or one this
  # estate adopted some other way) must not be silently re-decided just
  # because someone topped up its lane count.
  if [ "$DRY_RUN" -eq 0 ]; then
    if ! "${SUPERVISOR_PYTHON:-python3}" "$HERE/cli.py" adopt-session --session "$SESSION" --source "bootstrap-session.sh" >/dev/null; then
      echo "bootstrap-session: WARNING: session '$SESSION' was created but could not be recorded as supervised -- it will read as unknown/unsupervised until adopted by hand ('cli.py adopt-session --session $SESSION')" >&2
    fi
  else
    echo "  would run: ${SUPERVISOR_PYTHON:-python3} $HERE/cli.py adopt-session --session $SESSION --source bootstrap-session.sh"
  fi
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
  run send-keys -t "=$SESSION:$idx" "$LAUNCH_CMD" Enter
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
