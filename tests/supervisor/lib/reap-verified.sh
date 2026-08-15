#!/bin/bash
# agent-supervisor#104: one verified reap primitive for tests that start a
# real, long-lived poller-shaped process (a tmux-hosted stand-in or the real
# inbox-poll.sh under a throwaway LaunchAgent). Every such test in this suite
# hand-rolled its own kill line -- a bare `pkill -KILL -f "$STAND_IN"`,
# fire-and-forget, that never confirms the process is actually gone and never
# reports if it is not. #104 measured two of these processes outliving their
# test by hours: both survived SIGTERM and needed SIGKILL, and both were
# reaped by hand because nothing automated ever tried the escalation.
#
# reap_pid_verified reaps EXACTLY ONE recorded pid, by pid, never by name or
# pattern (as#104's brief calls out: "killing by name... will eventually kill
# the real poller"). The safety property that matters more than the leak
# itself: it REFUSES to signal a pid whose own command line does not contain
# the caller's sandbox path. A pid recorded by mistake, or a stale pid number
# that got recycled by an unrelated live process, is never touched -- it is
# reported as refused, not silently skipped and not silently killed.
#
# Usage: reap_pid_verified <pid> <sandbox-substring> [grace-seconds]
#   pid                a single process id, already believed to belong to
#                       this test (read from a PID_HISTORY/PID_FILE this
#                       test itself wrote at spawn time).
#   sandbox-substring   a path fragment that must appear in the process's own
#                       command line (its script path, almost always somewhere
#                       under this test's own mktemp dir) before this function
#                       will send it any signal at all.
#   grace-seconds       seconds to wait for TERM to take effect before
#                       escalating to KILL (default 5), and again after KILL
#                       before giving up and reporting failure (default 3,
#                       floored at 1).
#
# Exit status:
#   0  pid was already gone, or is gone now (TERM alone or TERM+KILL worked)
#   1  REFUSED -- the pid's command line did not contain the sandbox
#      substring; never signaled; still alive; reported to stderr
#   2  could not confirm death even after SIGKILL; reported to stderr
#
# Prints one of, to stderr, on every non-trivial outcome (never silent):
#   "reap: <pid> already gone"
#   "reap: <pid> exited on SIGTERM"
#   "reap: <pid> ignored SIGTERM, exited on SIGKILL"
#   "reap: REFUSING to signal <pid> -- ..."
#   "reap: COULD NOT REAP <pid> even after SIGKILL -- ..."
reap_pid_verified() {
  local pid="$1" sandbox="$2" grace="${3:-5}"
  [ -n "$pid" ] || return 0
  kill -0 "$pid" 2>/dev/null || { echo "reap: $pid already gone" >&2; return 0; }

  local cmdline
  cmdline=$(ps -o command= -p "$pid" 2>/dev/null)
  if [ -z "$cmdline" ]; then
    # The process answered kill -0 an instant ago but ps cannot see it now --
    # a race with it exiting on its own. Re-check kill -0 once more before
    # deciding; if it is still alive with unreadable command output, that is
    # itself grounds to refuse (no evidence it is ours).
    kill -0 "$pid" 2>/dev/null || { echo "reap: $pid already gone" >&2; return 0; }
    echo "reap: REFUSING to signal $pid -- its command line could not be read, so it cannot be confirmed as belonging to sandbox '$sandbox'" >&2
    return 1
  fi
  case "$cmdline" in
    *"$sandbox"*) ;;
    *)
      echo "reap: REFUSING to signal $pid -- command '$cmdline' does not run under sandbox '$sandbox'; this is exactly the case that must never be killed (agent-supervisor#104)" >&2
      return 1
      ;;
  esac

  kill -TERM "$pid" 2>/dev/null
  local waited=0 step=0.2
  while kill -0 "$pid" 2>/dev/null; do
    sleep "$step"
    waited=$(awk -v w="$waited" -v s="$step" 'BEGIN{print w+s}')
    awk -v w="$waited" -v g="$grace" 'BEGIN{exit !(w>=g)}' && break
  done
  if ! kill -0 "$pid" 2>/dev/null; then
    echo "reap: $pid exited on SIGTERM" >&2
    return 0
  fi

  kill -KILL "$pid" 2>/dev/null
  local kgrace="$grace"
  awk -v g="$kgrace" 'BEGIN{exit !(g<1)}' && kgrace=1
  waited=0
  while kill -0 "$pid" 2>/dev/null; do
    sleep "$step"
    waited=$(awk -v w="$waited" -v s="$step" 'BEGIN{print w+s}')
    awk -v w="$waited" -v g="$kgrace" 'BEGIN{exit !(w>=g)}' && break
  done
  if ! kill -0 "$pid" 2>/dev/null; then
    echo "reap: $pid ignored SIGTERM, exited on SIGKILL" >&2
    return 0
  fi
  echo "reap: COULD NOT REAP $pid even after SIGKILL -- it is still alive; this must be investigated by hand, never assumed clean (agent-supervisor#104)" >&2
  return 2
}

# reap_pid_history_verified <pid-history-file> <sandbox-substring> [grace]
# Reaps every pid recorded in a PID_HISTORY file (one pid per line, the
# convention the #57/#75/#154 suites already use), de-duplicated, via
# reap_pid_verified. Returns 0 only if every pid is confirmed gone or was
# rightly refused as out-of-scope and independently still not this test's to
# kill; returns the worst (highest) individual exit status otherwise so a
# caller can distinguish "nothing left to do" from "a refusal happened" from
# "a kill could not be confirmed".
reap_pid_history_verified() {
  local file="$1" sandbox="$2" grace="${3:-5}"
  [ -f "$file" ] || return 0
  local worst=0 pid rc
  while IFS= read -r pid; do
    [ -n "$pid" ] || continue
    reap_pid_verified "$pid" "$sandbox" "$grace"
    rc=$?
    [ "$rc" -gt "$worst" ] && worst=$rc
  done < <(sort -u "$file")
  return "$worst"
}

# Allow standalone invocation too: `bash reap-verified.sh <pid> <sandbox> [grace]`
# -- used by callers (including the Python test harness) that want the exact
# same verified-kill behaviour without sourcing bash into another language's
# process.
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  reap_pid_verified "$@"
  exit $?
fi
