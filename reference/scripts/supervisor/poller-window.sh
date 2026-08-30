#!/bin/bash
# Shared poller window recognition. The poller is the tmux window whose name
# equals LANES_POLLER_WINDOW (default: inbox-poll), measured from tmux at the
# moment a caller needs to address or classify that terminal.

POLLER_WINDOW_NAME="${LANES_POLLER_WINDOW:-inbox-poll}"

# agent-supervisor#28: this used to build a tab-separated `#{window_id}\t
# #{window_name}` line and split it with `awk -F"\t"`. Under the watchdog
# LaunchAgent's environment there is no LANG/LC_ALL, and tmux's format
# engine sanitises the literal tab it emits into `_` when no locale is set
# -- measured with `od -c` under `env -i HOME=... PATH=...`: `@225_inbox-
# poll` instead of `@225<TAB>inbox-poll`. `awk -F"\t"` then never splits,
# `$2` is never the window name, and the function returned EMPTY WITH RC=0 --
# indistinguishable from "no poller window exists". agent-supervisor#31:
# moving the comparison into tmux's `-f` format string fixed that locale bug
# but made literal `}` / `#{` in LANES_POLLER_WINDOW part of tmux format
# parsing. This keeps tmux output to a space-separated id/name record; space
# survives the stripped environment, and the shell compares the configured
# window name as plain text.
poller_window_ids() { # poller_window_ids <session>
  # stdout: matching window ids, one per line. rc 0 = the tmux query ran
  # (0, 1, or many matches -- an empty result IS a fact here). rc 2 = the
  # query itself could not be trusted (tmux missing, or list-windows
  # failed) -- callers must refuse rather than treat that the same as
  # "confirmed zero windows".
  local session="${1:?}" out rc
  if ! command -v tmux >/dev/null 2>&1; then
    echo "poller_window_ids: tmux not found on PATH" >&2
    return 2
  fi
  out=$(tmux list-windows -t "$session" -F "#{window_id} #{window_name}" 2>&1)
  rc=$?
  if [ "$rc" -ne 0 ]; then
    echo "poller_window_ids: tmux list-windows -t '$session' failed (rc=$rc): $out" >&2
    return 2
  fi
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    id="${line%% *}"
    name="${line#* }"
    [ "$name" = "$POLLER_WINDOW_NAME" ] && printf '%s\n' "$id"
  done <<<"$out"
  return 0
}

poller_window_target() { # poller_window_target <session>
  # rc 0 = exactly one poller window (printed as session:id). rc 1 =
  # confirmed zero. rc 2 = confirmed multiple, refusing to guess. rc 3 =
  # could not determine (the underlying query failed) -- distinct from 1
  # so a parse/query failure is never mistaken for "no poller alive".
  local session="${1:?}" id count=0 first="" ids_out ids_rc
  ids_out=$(poller_window_ids "$session")
  ids_rc=$?
  if [ "$ids_rc" -ne 0 ]; then
    return 3
  fi

  while IFS= read -r id; do
    [ -n "$id" ] || continue
    count=$((count + 1))
    [ -n "$first" ] || first="$id"
  done <<<"$ids_out"

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
