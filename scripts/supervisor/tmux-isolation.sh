#!/bin/bash

assert_isolated_tmux() {
  if [ -n "${TMUX:-}" ]; then
    echo "tmux isolation: TMUX is set; refusing to target an attached server" >&2
    return 1
  fi
  if [ -z "${TMUX_TMPDIR:-}" ]; then
    echo "tmux isolation: TMUX_TMPDIR is required for destructive tmux verbs" >&2
    return 1
  fi
  if [ ! -d "$TMUX_TMPDIR" ]; then
    echo "tmux isolation: TMUX_TMPDIR does not exist: $TMUX_TMPDIR" >&2
    return 1
  fi
}
