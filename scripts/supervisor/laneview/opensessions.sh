#!/bin/bash
# laneview implementation: push lane state into an OpenSessions tmux
# sidebar (github.com/Ataraxy-Labs/opensessions), via its documented
# HTTP API.
#
# This is the "together" implementation (agent-dotfiles#178) -- the
# tmux-plugin path. It is a productionized version of the bridge script
# #173 wrote and measured live on remote.hill90.com
# (`~/exp173/lanebridge.sh`); the mapping and mechanism are unchanged, only
# cleaned up to fit the laneview.sh contract (README.md in this
# directory): read-only, exits nonzero rather than showing stale state if
# the daemon is unreachable, and never invoked from a headless supervisor
# path.
#
# Requires: OpenSessions installed and its server already running (TPM
# manages this; this script does not start or stop the daemon -- starting
# a daemon is a decision this viewer does not get to make).
#
# Usage: opensessions.sh <session> <lanes.sh --json output>
#   OS_PORT   OpenSessions server port (default 39104, its documented
#             default derivation for a single-socket tmux server)

set -uo pipefail

SESSION="$1"
JSON="$2"
URL="http://127.0.0.1:${OS_PORT:-39104}"

# Returns nonzero; never exits. Review of #231 measured why this matters:
# the caller below used to be the right-hand side of a pipeline, so an
# `exit 1` here killed only the subshell -- a failed post silently skipped
# the remaining lanes, left the sidebar showing whatever it had, and the
# script still printed a success line and exited 0. That is precisely the
# staleness README.md rule 2 forbids. The loop now runs in this shell and
# decides what a failure means; this function only reports.
post() {
  local path="$1" body="$2" code
  code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 2 \
    -X POST "$URL$path" -H 'content-type: application/json' -d "$body" </dev/null) || {
    echo "laneview/opensessions.sh: cannot reach $URL -- is the OpenSessions daemon running? not rendering stale state." >&2
    return 1
  }
  if [ "$code" != "204" ] && [ "$code" != "200" ]; then
    echo "laneview/opensessions.sh: $path -> HTTP $code" >&2
    return 1
  fi
}

# lanes.sh's states -> OpenSessions' fixed AgentStatus vocabulary
# (idle/running/stale/waiting/error). Every state lanes.sh ships gets an
# arm: `validate_laneview_state_maps` in scripts/validate_repository.py
# reads these arms and errors if lanes.sh grows a state this case does not
# name, so the next state cannot land here as a silent default.
#
# The mapping is lossy (#173): menu-blocked and text-blocked both collapse
# to waiting, and unknown/service both read as idle. That loss is
# OpenSessions' fixed vocabulary, not this script's -- see
# laneview/README.md. `scrolled` was missing entirely until review of #231:
# it fell to the old `*) idle` default and rendered a lane lanes.sh will
# not dispatch to as a healthy green tick. It is now `waiting`, the same as
# the other blocked-pending-a-human states.
#
# The fallback is no longer `idle`. A state this script has never heard of
# is not evidence that a lane is available, and saying so would be the same
# defect again; it renders `stale` and says so on stderr.
map_status() {
  case "$1" in
    free) echo idle ;;
    busy) echo running ;;
    hung) echo stale ;;
    menu-blocked|text-blocked|unsent|scrolled) echo waiting ;;
    # `stale` (agent-dotfiles#237) is a narrowing of `dead`: a shell whose
    # window name still claims a task the ledger has already closed. It is
    # error, same as dead, because there is no agent running -- the
    # narrowing is diagnostic (a lying name vs. no name), not a different
    # severity, and OpenSessions has no vocabulary slot for "dead and lying".
    dead|stale) echo error ;;
    service|supervisor|unknown) echo idle ;;
    *)
      echo "laneview/opensessions.sh: no mapping for lanes.sh state '$1' -- rendering it stale rather than idle" >&2
      echo stale
      ;;
  esac
}

events=$(python3 - "$SESSION" "$JSON" <<'PY'
import json, sys
session, raw = sys.argv[1], sys.argv[2]
rows = json.loads(raw)
out = []
for r in rows:
    if r["state"] == "supervisor":
        continue
    out.append({
        "agent": "lanes", "tmuxSession": session,
        "status": None,  # filled in by the shell mapping below
        "threadId": r["name"],
        "threadName": f"{r['name']} — {r['state']}",
        "lastUserPrompt": f"lanes.sh says {r['state']}",
        "_state": r["state"],
    })
print(json.dumps(out))
PY
)

summary=$(python3 - "$JSON" <<'PY'
import json, sys, collections
rows = json.loads(sys.argv[1])
c = collections.Counter(r["state"] for r in rows if r["state"] != "supervisor")
print(" · ".join(f"{n} {s}" for s, n in sorted(c.items())))
PY
)

status_body() {
  python3 -c '
import json, sys
print(json.dumps({"session": sys.argv[1], "text": sys.argv[2]}))
' "$SESSION" "$1"
}

# Fed by process substitution on fd 3, not by a pipeline: this loop must run
# in *this* shell so `pushed`/`failed` survive it and a failure is the
# script's failure, not a subshell's (review of #231). fd 3 rather than
# stdin so nothing the loop body runs can eat the remaining events.
pushed=0
failed=0
while IFS= read -r ev <&3; do
  state=$(python3 -c 'import json,sys; print(json.loads(sys.argv[1])["_state"])' "$ev")
  status=$(map_status "$state")
  ev_out=$(python3 -c '
import json, sys
e = json.loads(sys.argv[1]); e.pop("_state"); e["status"] = sys.argv[2]
print(json.dumps(e))
' "$ev" "$status")
  if post /api/agent-event "$ev_out"; then
    pushed=$((pushed + 1))
  else
    failed=$((failed + 1))
  fi
done 3< <(python3 -c '
import json, sys
for e in json.loads(sys.argv[1]):
    print(json.dumps(e))
' "$events")

# `pushed` counts posts the daemon accepted, not events this script
# generated. The old success line counted the latter and printed it as the
# former -- an inferred figure reported as a measured one (AGENTS.md,
# "Recording Figures").
if [ "$failed" -gt 0 ]; then
  # Some lanes are current and some are not, and from the sidebar the two
  # are indistinguishable. Rule 2 is "degrade to absence, not to
  # staleness", so overwrite the status line with the fact rather than
  # leaving the previous summary standing as if it described this run.
  post /set-status "$(status_body "lanes: view incomplete -- $failed of $((pushed + failed)) lane(s) failed to push; NOT current")" || true
  echo "laneview/opensessions.sh: $failed of $((pushed + failed)) lane push(es) failed -- the sidebar is not a current view of $SESSION." >&2
  exit 1
fi

post /set-status "$(status_body "$summary")" || exit 1

echo "laneview opensessions -- pushed $pushed lane(s) for $SESSION to $URL"
