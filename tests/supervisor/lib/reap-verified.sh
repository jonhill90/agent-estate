#!/bin/bash
# agent-supervisor#382: the verified-reap primitive this file used to define
# directly moved to scripts/supervisor/reap-verified.sh so a production tool
# (poller-leak-cleanup.sh) can reuse it too, not only tests. This file stays
# so every existing `source "$HERE/lib/reap-verified.sh"` in this suite keeps
# working unchanged -- it just forwards to the one real copy.
HERE_REAP_SHIM="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../../../scripts/supervisor/reap-verified.sh
. "$HERE_REAP_SHIM/../../../scripts/supervisor/reap-verified.sh"

# Preserve standalone invocation through the shim too: `bash
# tests/supervisor/lib/reap-verified.sh <pid> <sandbox> [grace]`.
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  reap_pid_verified "$@"
  exit $?
fi
