#!/bin/bash
# Shared poller window recognition. The poller is the tmux window whose name
# equals LANES_POLLER_WINDOW (default: inbox-poll), measured from tmux at the
# moment a caller needs to address or classify that terminal.

POLLER_WINDOW_NAME="${LANES_POLLER_WINDOW:-inbox-poll}"

poller_window_ids() { # poller_window_ids <session>
  local session="${1:?}" tab
  tab=$'\t'
  command -v tmux >/dev/null 2>&1 || return 1
  tmux list-windows -t "$session" -F "#{window_id}${tab}#{window_name}" 2>/dev/null \
    | awk -F"$tab" -v name="$POLLER_WINDOW_NAME" '$2 == name { print $1 }'
}

poller_window_target() { # poller_window_target <session>
  local session="${1:?}" id count=0 first=""
  while IFS= read -r id; do
    [ -n "$id" ] || continue
    count=$((count + 1))
    [ -n "$first" ] || first="$id"
  done < <(poller_window_ids "$session")

  if [ "$count" -eq 1 ]; then
    printf '%s:%s\n' "$session" "$first"
    return 0
  fi
  [ "$count" -eq 0 ] && return 1
  return 2
}

is_poller_window_name() { # is_poller_window_name <window-name>
  [ "${1:-}" = "$POLLER_WINDOW_NAME" ]
}
