#!/bin/bash
# agent-supervisor#199: the isolation guard (`assert_isolated_tmux`,
# scripts/supervisor/tmux-isolation.sh, b489ebc/#258) lives INSIDE the files
# that source it. A lane's own worktree is a full checkout pinned to
# whatever commit it last fetched -- if that commit predates the guard, the
# copy of e.g. test_bootstrap_session.sh sitting in that worktree has no
# `unset TMUX; export TMUX_TMPDIR=...; assert_isolated_tmux || exit 1` at
# all, and running it from inside a tmux pane (a lane's own condition)
# creates its throwaway `bootstrap-test-$$` session on whatever socket
# $TMUX already points at -- the live estate's default socket. That is the
# exact shape #177, #180 and #199 all measured.
#
# A guard shipped to `main` only protects a worktree once that worktree has
# advanced past the guard's commit. This script closes the observability
# gap between "the guard is on main" and "every worktree that can run these
# tests actually has it": it walks every worktree `git worktree list` knows
# about and asks, of each one's PINNED commit (not `main`'s), whether the
# guard marker is present in every test file this repo treats as touching
# real tmux create/destroy verbs.
#
# This is read-only against every worktree -- `git show <sha>:<path>`, never
# a checkout, a switch, or a tmux call of any kind. It cannot itself leak a
# session, because it never runs one.
#
# A worktree missing the marker is not proof it will leak (it may never run
# that suite again before being pruned), but it is exactly the condition
# that produced #199's leak, and unlike a leaked session on the live socket,
# this is safe to check for continuously.
#
# A file that has never yet learned to call a real create/destroy tmux verb
# cannot leak regardless of the marker's presence -- an early run of this
# audit against test_digest.sh's own history proved that the naive
# marker-only check is a false-positive generator: many worktrees are pinned
# to a commit where the file has NEITHER the verb NOR the guard, because
# both were added together in the same commit (#100, #48). Flagging those
# would report a gap where nothing was ever unguarded. So a file only counts
# as a gap if its PINNED content contains a real verb (VERB_MARKER) without
# also containing the guard (WORKTREE_GUARD_MARKER).
#
# agent-supervisor#205 review (skills:2): every `git show` below is bounded.
# #267 and main's own #276 already paid for the unbounded-shell-out shape
# once each -- a hang here (a promisor/partial-clone worktree, a git gc/index
# lock held by a concurrent lane, a worktree on a stalled network mount)
# would previously have blocked this loop with no bound and no report. Same
# background+poll pattern advance-live.sh#51 uses, for the same reason named
# in that file's own comment: not a `timeout`/`gtimeout` wrapper, because
# this repo's own suite runs scripts like this one against a PATH of only
# /usr/bin:/bin (SUPERVISOR_PATH, watchdog.sh) and neither ships GNU
# coreutils on macOS -- a script that *required* an external timeout binary
# would fail closed on every real production tick. `&`/`kill -0`/`wait` are
# bash builtins, not an optional external dependency, so there is nothing to
# fail over to here: the bound always applies.
#
# A `git show` that times out is neither a gap (the file might be guarded;
# nothing was actually read) nor "clean" (the file was never checked) -- it
# is reported as its own UNKNOWN line and makes the whole run exit non-zero,
# same as a real gap. Reporting it as clean would be the exact false
# negative this observed-absence audit exists to prevent.
#
# Usage: worktree-guard-audit.sh [<repo-path>]
#   <repo-path>   defaults to the current directory. Must be a worktree of
#                 the repo whose OTHER worktrees you want audited --
#                 `git worktree list` is shared across all of them.
#
#   WORKTREE_GUARD_FILES   newline- or space-separated list of repo-relative
#                           paths to check (default: the tmux-touching test
#                           files enumerated for #199 below).
#   WORKTREE_GUARD_MARKER  grep -E pattern that counts as "guarded"
#                           (default: assert_isolated_tmux). test_restore.sh
#                           is deliberately excluded from the default list:
#                           it isolates itself with its own private-socket
#                           PATH wrapper (`tmux -L "$SOCKET"`), a different
#                           and independently safe technique documented in
#                           its own header, not a gap this script should flag.
#   WORKTREE_GUARD_FILE_TIMEOUT_SECONDS
#                           per-`git show` bound in seconds (default: 5).
#                           A single hung read is reported as UNKNOWN and
#                           does not stop the rest of the walk.
#   WORKTREE_GUARD_POLL_INTERVAL_SECONDS
#                           how often bounded_show's poll loop wakes to
#                           check whether the backgrounded `git show` has
#                           finished (default: 0.05, agent-estate#800).
#                           bounded_show measures its OWN elapsed wall-clock
#                           time against FILE_TIMEOUT on every wake rather
#                           than counting ticks and multiplying by this
#                           interval -- a fix-pass on #800 (skills:2) found
#                           the tick-counting version overran its bound by
#                           ~50% at the default interval (measured 7.7s
#                           against a 6s ceiling: 5s FILE_TIMEOUT + 1s TERM
#                           grace) because each tick costs more than exactly
#                           POLL_INTERVAL -- there is a roughly constant
#                           per-iteration overhead (fork+exec for `sleep`
#                           plus loop bookkeeping, ~11.5ms measured on this
#                           host) that a naive `elapsed = ticks * interval`
#                           model does not see, and that overhead is paid
#                           once per tick, so a small interval (many ticks
#                           for the same bound) pays it many more times.
#                           Checking real elapsed time instead means a slow
#                           tick can only ever overrun the bound by about one
#                           tick's own cost, never by the accumulated cost of
#                           every tick that ran before it.
#   WORKTREE_GUARD_MAX_WORKTREES
#                           agent-estate#808: cap the walk to the FIRST N
#                           entries `git worktree list` returns (default: 0,
#                           meaning unlimited -- every real production tick
#                           leaves this unset and keeps auditing every live
#                           worktree, unchanged). `git worktree list`'s order
#                           is the repo's own worktree administration order
#                           (main worktree first, then linked worktrees in
#                           the order they were added) -- stable across runs
#                           against the same repo state, so "first N" is
#                           deterministic and reproducible, unlike a
#                           time-budget cutoff that would vary with host
#                           load. This exists ONLY because
#                           advance-live.sh's smoke test re-audits the SAME
#                           production worktree farm the real watchdog does
#                           (its scratch worktree is a linked worktree of the
#                           live repo, and `git worktree list` output is
#                           shared across every worktree of one repo) --
#                           #808 measured that smoke-test audit at 123.4s of
#                           a 138.4s smoke run against a 150s window, 11.6s
#                           of slack. The audit's job inside a smoke test is
#                           to prove the CANDIDATE watchdog.sh writes a
#                           well-formed status, not to re-benchmark full-farm
#                           performance on every promotion -- a structural
#                           bug in the audit logic shows up on a bounded
#                           subset just as reliably as on the full farm. When
#                           set, the summary line below reports both the
#                           worktrees actually walked and the total the repo
#                           reports, so a bounded run is never mistaken for a
#                           full one.
set -uo pipefail

REPO="${1:-.}"
if ! git -C "$REPO" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "worktree-guard-audit: $REPO is not a git worktree" >&2
  exit 2
fi

FILE_TIMEOUT="${WORKTREE_GUARD_FILE_TIMEOUT_SECONDS:-5}"
POLL_INTERVAL="${WORKTREE_GUARD_POLL_INTERVAL_SECONDS:-0.05}"
# agent-estate#800: bounded_show's poll loop used to sleep a full second per
# iteration, so a 9ms `git show` still cost ~1s -- the loop's first `kill -0`
# runs microseconds after backgrounding, finds the child still alive, and
# the body always sleeps once. Measured: 19 worktrees x 13 files = 247
# checks took 253s (1.02s/check) against a 9ms `git show`. A sub-second poll
# fixes that.
#
# #800 fix-pass (skills:2): the first cut at this re-derived a tick COUNT
# (FILE_TIMEOUT / POLL_INTERVAL) and compared the loop's own iteration
# counter against it, on the assumption that one iteration costs exactly
# POLL_INTERVAL. It does not -- `sleep`'s fork+exec plus loop bookkeeping
# adds a roughly constant per-iteration overhead, paid once per tick, so a
# small POLL_INTERVAL (many ticks for the same bound) overran by that
# overhead many times over: measured 7.7s against a 6s ceiling (5s
# FILE_TIMEOUT + 1s TERM grace) at the default 0.05s interval, a genuine
# ~50% overrun, not noise. See `now_seconds` and bounded_show below for the
# wall-clock replacement -- it checks elapsed time directly instead of
# inferring it from a tick count, so it cannot drift with iteration cost.

# `date +%s.%N` gives fractional-second wall-clock time; GNU coreutils and
# the BSD/macOS system `date` both support `%N` on every host this has been
# checked against (verified directly on this host's system `date`, not
# assumed). One fork per call -- called at most twice per bounded_show tick,
# same order of cost as the `sleep` every tick already pays.
now_seconds() { date +%s.%N; }

# agent-supervisor#205: watchdog.sh's own outer bound on this whole script
# (SUPERVISOR_GUARD_AUDIT_TIMEOUT) can TERM/KILL this process mid-file-check,
# between bounded_show's own poll iterations. Without a way to reap it, the
# `git show` child bounded_show had just backgrounded would outlive this
# script entirely -- an orphaned process is a worse leak than the one #199
# filed this audit to catch, and this is exactly the failure the two-bound
# design must not introduce.
#
# CUR_SHOW_PID names whichever `git show` bounded_show currently has in
# flight, if any -- but ONLY works as a plain variable because bounded_show
# below is called PLAIN, never wrapped in `$(bounded_show ...)`. A command
# substitution forks a subshell, and bash only runs a pending trap between
# top-level commands in the process actually holding it -- a parent blocked
# on `wait()` for a command-substitution subshell defers the trap until
# that subshell itself finishes, which is exactly the hang this exists to
# prevent (measured directly while building this fix: a trap set exactly
# this way never fired at all -- not late, not eventually, not once --
# while the call site captured bounded_show's own poll loop through `$()`).
# bounded_show writes its result to SHOW_CONTENT/SHOW_RC as globals instead,
# so its poll loop runs directly in THIS process and a trap sent to this
# pid is handled between its `sleep 1` iterations, same as it would be for
# any other foreground loop.
CUR_SHOW_PID=""
cleanup_show() {
  [ -n "$CUR_SHOW_PID" ] || return 0
  # No grace sleep here, deliberately: this script's OWN caller (watchdog.sh's
  # outer bound) is running the same TERM-then-sleep-1-then-KILL sequence
  # against THIS process concurrently. A grace sleep in both places races --
  # the caller's KILL can land before this trap reaches its own escalation,
  # leaving the child to outlive the parent that was supposed to reap it.
  # TERM then an immediate KILL is safe to send back-to-back: KILL always
  # finishes the job regardless of whether TERM's default handler already did.
  kill -TERM "$CUR_SHOW_PID" 2>/dev/null
  kill -KILL "$CUR_SHOW_PID" 2>/dev/null
  CUR_SHOW_PID=""
}
# agent-estate#800: a TERM/INT handler that only reaps the CURRENT child and
# returns, without terminating the script itself, does not stop the walk --
# bash resumes the interrupted loop right after the handler returns. That
# leaves a window between "current hang reaped" and "outer KILL lands" in
# which the walk can advance to the NEXT worktree/file and background a
# SECOND `git show`, one the already-fired TERM will never reap and the
# outer KILL (unblockable, cannot be trapped) cannot reap either -- an
# orphan despite the two-bound design. Finer polling (this file's own
# agent-estate#800 fix) makes this window worse, not better: catching TERM
# sooner leaves MORE of the caller's fixed TERM-then-KILL grace period free
# for the walk to reach that next iteration, not less. So on TERM/INT this
# handler reaps the in-flight child, then re-raises the same signal against
# itself with its own trap removed -- the shell's default disposition then
# actually terminates the process, matching the property the two-bound
# design already assumed was true. sig_cleanup_show is a thin per-signal
# wrapper (not `cleanup_show TERM` from `trap ... TERM INT` directly)
# because that shared-form invocation would not tell cleanup_show which
# signal name to re-raise.
sig_cleanup_show() {
  local sig="$1"
  cleanup_show
  trap - "$sig"
  kill -s "$sig" "$$"
}
trap 'sig_cleanup_show TERM' TERM
trap 'sig_cleanup_show INT' INT
trap cleanup_show EXIT

# Bounded `git show <sha>:<path>`. Never called via `$(...)` -- see the
# CUR_SHOW_PID comment above for why that would defeat the trap this relies
# on. Sets SHOW_CONTENT and returns 0 on success; SHOW_CONTENT is unset and
# the return is 1 if the file is absent at that commit (the existing,
# already harmless case -- "nothing to leak"), 2 on a real timeout (the
# caller must treat this as unknown, never as absent or clean).
bounded_show() {
  local sha="$1" path="$2" out_file rc timed_out start_ts now_ts overrun
  SHOW_CONTENT=""
  out_file="$(mktemp "${TMPDIR:-/tmp}/wga-show.XXXXXX")" || return 2
  git -C "$REPO" show "${sha}:${path}" >"$out_file" 2>/dev/null &
  CUR_SHOW_PID=$!
  start_ts="$(now_seconds)"
  timed_out=0
  while kill -0 "$CUR_SHOW_PID" 2>/dev/null; do
    now_ts="$(now_seconds)"
    # Elapsed wall-clock time against FILE_TIMEOUT directly -- never a tick
    # count times POLL_INTERVAL. See the header comment above FILE_TIMEOUT
    # for why: a tick count assumes every iteration costs exactly
    # POLL_INTERVAL, which is false by a roughly constant per-iteration
    # overhead, and that overhead compounds with tick count instead of
    # staying fixed. `awk` does the float compare -- bash arithmetic is
    # integer-only and $start_ts/$now_ts carry fractional seconds.
    overrun="$(awk -v s="$start_ts" -v n="$now_ts" -v t="$FILE_TIMEOUT" \
      'BEGIN { print (n - s >= t) ? 1 : 0 }')"
    if [ "$overrun" -eq 1 ]; then
      timed_out=1
      kill -TERM "$CUR_SHOW_PID" 2>/dev/null
      sleep 1
      kill -KILL "$CUR_SHOW_PID" 2>/dev/null
      break
    fi
    sleep "$POLL_INTERVAL"
  done
  wait "$CUR_SHOW_PID" 2>/dev/null
  rc=$?
  CUR_SHOW_PID=""  # reaped -- cleanup_show must not try to kill it again
  if [ "$timed_out" -eq 1 ]; then
    rm -f "$out_file"
    return 2
  fi
  if [ "$rc" -ne 0 ]; then
    rm -f "$out_file"
    return 1
  fi
  SHOW_CONTENT="$(cat "$out_file")"
  rm -f "$out_file"
  return 0
}

DEFAULT_FILES="tests/supervisor/test_bootstrap_session.sh
tests/supervisor/test_advance_live.sh
tests/supervisor/test_digest.sh
tests/supervisor/test_inbox_poll_service.sh
tests/supervisor/test_lane_done.sh
tests/supervisor/test_lanes_env_parity.sh
tests/supervisor/test_laneview_tmux_plugin.sh
tests/supervisor/test_laneview_tui_interactive.sh
tests/supervisor/test_look.sh
tests/supervisor/test_poller_recover.sh
tests/supervisor/test_poller_window.sh
tests/supervisor/test_watchdog_launchd_relaunch.sh
tests/supervisor/test_watchdog_poller_copy.sh"

FILES="${WORKTREE_GUARD_FILES:-$DEFAULT_FILES}"
MARKER="${WORKTREE_GUARD_MARKER:-assert_isolated_tmux}"
VERB_MARKER="${WORKTREE_GUARD_VERB_MARKER:-tmux (new-session|kill-session|kill-server|kill-window|respawn-(pane|window))}"
MAX_WORKTREES="${WORKTREE_GUARD_MAX_WORKTREES:-0}"

WORKTREES="$(git -C "$REPO" worktree list | awk '{print $1, $2}')"
total_worktrees=0
# `wc -l` pads its count with leading spaces on macOS/BSD; awk's own NR
# never does, so use that instead of `wc -l | tr -d ' '`.
[ -n "$WORKTREES" ] && total_worktrees=$(awk 'END{print NR}' <<<"$WORKTREES")

gaps=0
unknowns=0
checked=0
walked=0
while IFS= read -r line; do
  [ -n "$line" ] || continue
  # agent-estate#808: a bounded smoke-test run stops walking worktrees here,
  # never mid-worktree -- each worktree's own file loop still runs to
  # completion, so a bound never reports a half-checked worktree as fully
  # audited. Unset/0 (every real production tick) never enters this branch.
  if [ "$MAX_WORKTREES" -gt 0 ] && [ "$walked" -ge "$MAX_WORKTREES" ]; then
    break
  fi
  walked=$((walked + 1))
  wt_path="$(awk '{print $1}' <<<"$line")"
  wt_sha="$(awk '{print $2}' <<<"$line")"
  [ -n "$wt_sha" ] || continue
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    # Called plain, never as `content="$(bounded_show ...)"` -- see
    # bounded_show's own header for why a command-substitution subshell
    # here would defeat the TERM/INT trap this whole bound depends on.
    bounded_show "$wt_sha" "$f"
    rc=$?
    if [ "$rc" -eq 2 ]; then
      # Timed out: neither "absent" nor "clean" -- report it as its own
      # unknown rather than silently skipping, same reasoning the header
      # gives for why this must never read as a clean result.
      echo "UNKNOWN  $wt_path  ($wt_sha)  $f  -- git show did not finish within ${FILE_TIMEOUT}s" >&2
      unknowns=$((unknowns + 1))
      continue
    fi
    [ "$rc" -eq 0 ] || continue  # file absent at this commit: nothing to leak
    checked=$((checked + 1))
    grep -qE "$VERB_MARKER" <<<"$SHOW_CONTENT" || continue  # no real create/destroy verb at this commit: nothing to leak
    if ! grep -qE "$MARKER" <<<"$SHOW_CONTENT"; then
      echo "GAP  $wt_path  ($wt_sha)  $f  -- calls a real tmux verb without '$MARKER'" >&2
      gaps=$((gaps + 1))
    fi
  done <<<"$FILES"
done <<<"$WORKTREES"

# agent-estate#808: when bounded, say so in the summary line itself -- a
# bounded smoke-test run and a full production run must never be
# indistinguishable from their own output, or a genuinely narrowed audit
# could be misread as having covered the whole farm.
if [ "$MAX_WORKTREES" -gt 0 ] && [ "$walked" -lt "$total_worktrees" ]; then
  echo "worktree-guard-audit: $checked file@worktree pairs checked, $gaps gap(s), $unknowns unknown(s) (bounded: $walked of $total_worktrees worktree(s) walked)"
else
  echo "worktree-guard-audit: $checked file@worktree pairs checked, $gaps gap(s), $unknowns unknown(s)"
fi
[ "$gaps" -eq 0 ] && [ "$unknowns" -eq 0 ]
