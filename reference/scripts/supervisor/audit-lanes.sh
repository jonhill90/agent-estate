#!/bin/bash
# agent-supervisor#235 item 4: "Build the list -- do not quote a count for a
# set nobody has enumerated." This is that list. It compares every row in the
# ledger's `lanes` table against live tmux, once, and reports every mismatch
# by name -- never a summary count standing in for a set nobody looked at.
#
# A ledger row and a live pane can diverge two distinct ways, and this keeps
# them apart rather than folding both into one "mismatch" bucket:
#
#   GONE   -- the row's `pane_id` does not exist anywhere in live tmux. The
#             window it named is closed; nothing to reconcile it against.
#   MOVED  -- the row's `pane_id` DOES exist live, but not at the index the
#             row's own `lane` string claims (`<session>:<index>`). This is
#             #235's own measured case: `renumber-windows on` shifted the
#             window to a different index, and the ledger's index-keyed PK
#             never heard about it.
#
# A row with neither problem -- pane_id live, at the index the lane string
# names -- is not printed; this is a report of what needs attention, not a
# census. Exit 0 means the audit ran and found nothing to report; exit 1
# means it ran and found mismatches (see the summary line); exit 2 means the
# audit itself could not be trusted (ledger unreadable, tmux unreachable) --
# never conflated with "found nothing", the same fail-closed posture the rest
# of this estate takes for an unreadable ledger (dispatch.sh step 0).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PYTHON="${DISPATCH_PYTHON:-python3}"
CLI="$HERE/cli.py"
TMUX_BIN="${TMUX_BIN:-tmux}"

# `cli.py` resolves its own state dir from $AGENT_SUPERVISOR_STATE_DIR (or
# its built-in default) exactly as every other caller in this estate does --
# nothing here re-derives or overrides it.
STATUS_JSON=$("$PYTHON" "$CLI" status 2>&1)
if [ $? -ne 0 ]; then
  echo "audit-lanes: the ledger is unreadable -- cannot audit what it does not have" >&2
  sed 's/^/  /' <<<"$STATUS_JSON" >&2
  exit 2
fi

# Live tmux, once: every pane, its session:index and its window id, keyed by
# pane_id (%N) so the ledger's own pane_id column is the join key -- the
# stable half of a lane's identity, same reasoning as `lane_relation_from_rows`
# in core.py.
if ! LIVE=$("$TMUX_BIN" list-panes -a -F '#{pane_id} #{session_name}:#{window_index} #{session_name}:@#{window_id}' 2>&1); then
  echo "audit-lanes: could not read live tmux panes -- cannot audit against it" >&2
  sed 's/^/  /' <<<"$LIVE" >&2
  exit 2
fi

# `lanes` rows, one `lane\tpane_id` pair per line -- parsed out of the
# ledger's own JSON rather than re-querying, so this reads the SAME snapshot
# `status` already took.
LANES=$("$PYTHON" -c '
import json, sys
data = json.load(sys.stdin)
for row in data["lanes"]:
    print(row["lane"] + "\t" + row["pane_id"])
' <<<"$STATUS_JSON")

gone=0
moved=0
checked=0

echo "audit-lanes: ledger \`lanes\` rows vs live tmux"
while IFS=$'\t' read -r lane pane_id; do
  [ -n "$lane" ] || continue
  checked=$((checked + 1))
  live_line=$(grep -m1 "^${pane_id} " <<<"$LIVE" || true)
  if [ -z "$live_line" ]; then
    echo "  GONE   $lane -- pane_id $pane_id no longer exists in live tmux"
    gone=$((gone + 1))
    continue
  fi
  live_index_target=$(awk '{print $2}' <<<"$live_line")
  if [ "$live_index_target" != "$lane" ]; then
    live_window_target=$(awk '{print $3}' <<<"$live_line")
    echo "  MOVED  $lane -- pane_id $pane_id is now $live_index_target ($live_window_target), not $lane"
    moved=$((moved + 1))
  fi
done <<<"$LANES"

echo
echo "audit-lanes: $checked row(s) checked, $gone gone, $moved moved"
[ "$gone" -eq 0 ] && [ "$moved" -eq 0 ]
