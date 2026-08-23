#!/bin/bash
# Register the lane THIS PROCESS IS RUNNING IN, from facts it can observe
# about itself -- nothing typed, nothing guessed, nothing backfilled.
#
# WHY THIS EXISTS (agent-supervisor#520). `dispatch.sh` is the only thing that
# ever called `cli.py register`, so a lane attached by hand -- a pane the
# operator or a tick loop drove with `send.sh` directly, which is the shape
# this estate actually runs -- has no `lanes` row at all. Two gates refuse
# outright on that: `post-verdict.sh` will not post a `Review-Lane:` trailer
# naming an unregistered lane, and `verdict-independence.sh` cannot answer
# `different` for a lane whose `pane_id` the ledger has never recorded, so
# `merge-pr.sh` refuses the merge. Both refusals are correct -- fail-closed is
# the whole posture -- but they left genuinely independent reviews with no way
# to count, which is how a repo ends up merging without one (#513).
#
# WHY NOT JUST RUN `cli.py register` BY HAND. Because that is the failure
# already measured. On 2026-08-23 all four of `estate:2`..`estate:5` carried
# rows registered out of band whose `pane_id` (`%38`, `%39`, `%51`, `%52`)
# matched no pane on the running server (`%1`..`%5`), whose `repo` named a
# different checkout than the panes were actually in, and one of whose nonces
# was a hand-written string rather than anything minted. Every one of them
# satisfied `post-verdict.sh`'s "is this lane registered" check and supplied
# `merge-pr.sh` with a `pane_id` it treated as an identity. A registration
# nobody measured is worse than no registration: it turns a refusal into a
# pass. That is invariant 10's self-attestation hazard, moved from lookup time
# to registration time, and it is what this script is shaped to prevent.
#
# THE ONE SELF-FACT A PANE'S OWN PROCESS HAS. `$TMUX_PANE` is exported by tmux
# into the environment of every process it starts in a pane, and it names THAT
# pane -- not the session's currently-active one. That distinction is exactly
# invariant 10: `tmux display-message` with no `-t` answers for whichever
# window happens to be focused, which mis-stamped six `Review-Lane:` trailers
# in one day (#187). So `$TMUX_PANE` is the anchor here, and every other value
# is read back off tmux with an explicit `-t "$TMUX_PANE"`:
#
#   lane id   #{session_name}:#{window_index}   (invariant 9's identity)
#   repo      #{pane_current_path}              (what cli.py register verifies)
#   command   #{pane_current_command}           (the harness plausibility check)
#
# There is no flag to override any of them. A caller who could pass a lane id
# in could pass the wrong one in, and then this script would be the thing
# laundering the claim.
#
# WHAT THE NONCE IS, since #520's brief asks for it explicitly. It is NOT a
# secret held by the running agent that this script must extract without
# inventing. `adapter.TmuxAdapter.register_lane` MINTS a nonce at registration
# and STAMPS it onto the pane as the `@hill90_lane_nonce` tmux option; later
# checks (`_verified_lane`) re-read the option and compare it to the ledger.
# It is an incarnation token minted going forward, not a historical fact, so
# minting a fresh one here fabricates nothing -- `cli.py register` writes it to
# both places in one call, exactly as a dispatch does. What WOULD be
# fabrication is the identity fields around it, and those are all measured
# above rather than accepted from a caller.
#
# WHAT THIS DELIBERATELY DOES NOT DO: write any task or dispatch row. It
# registers an identity, not a history. A lane that was never dispatched
# through `dispatch.sh` genuinely has no dispatch history, and inventing one
# would misrepresent the record invariant 1 exists to keep honest. Everything
# downstream that needs a real dispatch (`lane_available`, `restore.sh`'s
# ledger-driven plan, the author-resolution chain) keeps reading exactly the
# absence that is true.
#
# Registration is idempotent: `core.Ledger.register_lane` upserts, so re-running
# this after a tmux restart refreshes the row onto the live incarnation, which
# is the intended remedy for a `contradicted` result from `lane_identity.py`.
#
# Usage:
#   register-lane-self.sh [--harness NAME] [--expect-lane <session>:<index>]
#
# --harness       override the harness inferred from the pane's own command.
#                 `cli.py register` still refuses a value the live pane
#                 contradicts (`adapter.HARNESS_COMMANDS`), so this can only
#                 disambiguate a command the table cannot place on its own
#                 (every Node-based harness reads "node"), never assert a
#                 harness the pane is visibly not running.
# --expect-lane   assert the lane id this pane resolves to. Refuses on a
#                 mismatch instead of registering. This is a CHECK, not an
#                 override -- the registered id is always the measured one.
#
# Exit 0   registered, and the registration was read back and confirmed
#          against the live server by `lane_identity.py`.
# Exit 1   refused, or the registration could not be confirmed. Nothing that
#          exits non-zero here should be treated as a usable lane identity.
# Exit 2   usage error.
set -uo pipefail

usage() { sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; exit 2; }

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PYTHON="${AGENT_PYTHON_BIN:-python3}"
TMUX_BIN="${AGENT_TMUX_BIN:-tmux}"
STATE="${AGENT_SUPERVISOR_STATE_DIR:-${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}}"
SUPERVISOR_WINDOW="${LANES_SUPERVISOR_WINDOW:-1}"

HARNESS=""
EXPECT_LANE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --harness) HARNESS="${2:-}"; [ -n "$HARNESS" ] || usage; shift 2 ;;
    --expect-lane) EXPECT_LANE="${2:-}"; [ -n "$EXPECT_LANE" ] || usage; shift 2 ;;
    *) echo "register-lane-self.sh: unrecognised argument '$1'" >&2; usage ;;
  esac
done

# --- the anchor: this process's OWN pane ----------------------------------
if [ -z "${TMUX_PANE:-}" ]; then
  echo "register-lane-self.sh: refusing -- \$TMUX_PANE is not set, so this process cannot observe which pane it is in" >&2
  echo "register-lane-self.sh: run this FROM the lane's own pane. There is deliberately no flag to name a lane from outside -- see this script's header on agent-supervisor#520 and invariant 10." >&2
  exit 1
fi
PANE="$TMUX_PANE"

# Every read below is explicitly `-t "$PANE"`. A bare `display-message`
# answers for the session's ACTIVE window, which is the #187 defect.
META=$("$TMUX_BIN" display-message -p -t "$PANE" \
  '#{pane_id}|#{session_name}:#{window_index}|#{pane_current_path}|#{pane_current_command}|#{window_index}' 2>&1)
if [ $? -ne 0 ] || [ -z "$META" ]; then
  echo "register-lane-self.sh: refusing -- could not read pane $PANE off tmux: $META" >&2
  exit 1
fi
IFS='|' read -r LIVE_PANE LANE REPO COMMAND WINDOW_INDEX <<<"$META"

if [ "$LIVE_PANE" != "$PANE" ]; then
  echo "register-lane-self.sh: refusing -- asked tmux about $PANE and it answered for $LIVE_PANE" >&2
  exit 1
fi
if [ -z "$LANE" ] || [ -z "$REPO" ]; then
  echo "register-lane-self.sh: refusing -- tmux returned an incomplete description of pane $PANE: '$META'" >&2
  exit 1
fi

# The supervisor's own window never reviews and must never be registered as a
# worker lane -- the same convention `lanes.sh` and `post-verdict.sh` already
# hold, checked here so the bad row is never written rather than caught later.
if [ "$WINDOW_INDEX" = "$SUPERVISOR_WINDOW" ]; then
  echo "register-lane-self.sh: refusing -- pane $PANE is window index $WINDOW_INDEX, the supervisor's own window (LANES_SUPERVISOR_WINDOW), which is not a lane" >&2
  exit 1
fi

if [ -n "$EXPECT_LANE" ] && [ "$EXPECT_LANE" != "$LANE" ]; then
  echo "register-lane-self.sh: refusing -- --expect-lane says '$EXPECT_LANE' but this pane ($PANE) is really '$LANE'" >&2
  echo "register-lane-self.sh: the measured id wins; nothing was registered. If '$EXPECT_LANE' is what you meant, run this from THAT pane." >&2
  exit 1
fi

# --- harness: inferred from the pane's own command, unless told -----------
if [ -z "$HARNESS" ]; then
  HARNESS=$("$PYTHON" -c '
import sys
sys.path.insert(0, sys.argv[1])
from adapter import HARNESS_COMMANDS
command = sys.argv[2]
# Only an UNAMBIGUOUS command infers a harness. "node" appears under both
# codex and copilot (adapter.HARNESS_COMMANDS own comment), so it names
# nothing on its own and the caller must say which -- the same posture
# `cli.lane_free` takes for exactly this reason (agent-dotfiles#216).
hits = [h for h, commands in HARNESS_COMMANDS.items() if command in commands]
print(hits[0] if len(hits) == 1 else "")
' "$HERE" "$COMMAND")
fi
if [ -z "$HARNESS" ]; then
  echo "register-lane-self.sh: refusing -- cannot tell which harness pane command '$COMMAND' is; pass --harness" >&2
  exit 1
fi

# --- mint the incarnation nonce (see the header on what this is) ----------
NONCE=$("$PYTHON" -c 'import secrets; print(secrets.token_hex(16))')
if [ -z "$NONCE" ]; then
  echo "register-lane-self.sh: refusing -- could not mint a nonce" >&2
  exit 1
fi

# `cli.py register` re-reads the pane itself through `adapter.TmuxAdapter.
# register_lane` and refuses if `--repo` is not the pane's real cwd or if the
# harness contradicts the live command -- so the values measured above are
# checked a second time by the code that writes the row, not merely trusted.
REG_OUT=$("$PYTHON" "$HERE/cli.py" --state-dir "$STATE" register \
  --lane "$LANE" --target "$PANE" --harness "$HARNESS" --repo "$REPO" \
  --nonce "$NONCE" --transport send-keys 2>&1)
REG_RC=$?
if [ "$REG_RC" -ne 0 ]; then
  echo "register-lane-self.sh: refusing -- cli.py register failed (exit $REG_RC): $REG_OUT" >&2
  exit 1
fi

# --- read it back: "the write returned 0" is not evidence -----------------
# Same discipline as post-verdict.sh's read-back (#170): confirm the row this
# just wrote is actually corroborated by the live server before reporting a
# usable identity.
ID_OUT=$("$PYTHON" "$HERE/lane_identity.py" --lane "$LANE" --state-dir "$STATE" --tmux-bin "$TMUX_BIN" 2>&1)
ID_RC=$?
if [ "$ID_RC" -ne 0 ]; then
  echo "register-lane-self.sh: registered $LANE but could NOT confirm it against the live server -- treat this lane as unregistered: $ID_OUT" >&2
  exit 1
fi

echo "register-lane-self.sh: registered and confirmed $LANE (pane $PANE, harness $HARNESS, repo $REPO)"
printf '%s\n' "$ID_OUT"
exit 0
