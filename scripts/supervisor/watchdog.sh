#!/bin/bash
# Restarts the supervisor loop when it has died with work left.
#
# as#132: this file finds the pane by COORDINATES (SUPERVISOR_PANE, below),
# never by window name -- `grep -nE 'window_name|#W|list-windows'` over this
# file matches nothing. Renaming the window (as#132) does not affect revival;
# what IS load-bearing is the window's position (window 1), not its label.
#
# It is a WATCHDOG, not the loop. The loop is event-driven; this only catches
# the case where it stopped and nobody noticed. Never make this the mechanism —
# a 3-minute timer driving real work is what exhausted the weekly limit twice
# (see loop-throughput-comes-from-lanes-not-cadence in the memory vault).
#
# Rewritten 2026-08-11 after a four-reviewer council (Codex/gpt-5.6-sol,
# Claude, Copilot, Pi/gpt-5.5), each given a different lens. Three findings
# were confirmed with evidence and are fixed here:
#
#   1. GitHub failure was counted as zero work.  `n=$(gh ...) || n=0` turned an
#      unreachable API into "nothing actionable" — indistinguishable from a
#      genuinely empty queue. This matters specifically because `gh` keeps its
#      token in the macOS keyring, which a cron job frequently cannot read.
#      Evidenced: the 04:18:02Z tick logged "nothing actionable" while eleven
#      actionable items were open. Now a query failure marks the tick DEGRADED
#      and fails TOWARD restarting, because a stalled loop costs more than a
#      redundant restart.
#   2. Silent on the healthy path.  Healthy-and-busy, dead-cron, and
#      nothing-to-do all looked identical: no new log line. Now every tick
#      writes STATUS atomically, so one `cat` answers "where are we" and a
#      stale timestamp proves the cron itself has stopped.
#   3. No escalation.  Restarting forever hides the bug it is papering over.
#      After MAX_RESTARTS in ESCALATE_WINDOW it stops restarting, says so in
#      STATUS, and leaves the loop down for a human.
#
# Cost when healthy: one tmux read, one status write, zero model tokens.

set -uo pipefail
# A fixed fallback PATH so a LaunchAgent (which inherits almost nothing) finds
# tmux, gh and python3. An inherited PATH is kept first so env -i tests can
# inject stubs without relying on SUPERVISOR_PATH -- three separate bugs shipped
# in this file because the production environment shape was not testable.
#
# /usr/sbin is included for lsof (agent-supervisor#25): this PATH is
# inherited by every child this script execs, including poller-recover.sh,
# whose orphan check needs lsof to tell "no live poller" from "cannot tell".
# poller-recover.sh also resolves lsof by absolute path on its own now, so
# this is defense in depth, not the only fix -- see that script for the
# primary one.
PATH="${SUPERVISOR_PATH:-${PATH:+$PATH:}/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:$HOME/.local/bin}"
export PATH

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./session-defaults.sh
. "$HERE/session-defaults.sh"
# Overridable so the script is testable and so a second lane can reuse it.
PANE="${SUPERVISOR_PANE:-$(lanes_session_or_default):1.1}"
# Runtime state (logs, status, briefs) stays outside the repo; the CODE
# lives here and is versioned and tested. Splitting them was the point of
# moving this file in: an untracked shell script that the whole loop
# depends on is not reproducible for anyone else.
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
LOG="${SUPERVISOR_LOG:-$STATE/watchdog.log}"
STATUS="${SUPERVISOR_STATUS:-$STATE/watchdog.status}"
TICK="${SUPERVISOR_TICK:-$HERE/loop-tick.md}"
STAMP="${SUPERVISOR_STAMP:-$STATE/.last-restart}"
HISTORY="${SUPERVISOR_HISTORY:-$STATE/.restart-history}"
# Where the pinned copy of this repository lives. Used ONLY to decide whether
# the copy now running IS that pinned copy, and so whether it may advance
# itself -- see the advance section at the bottom of this file.
LIVE="${SUPERVISOR_LIVE:-$STATE/live}"
read -r -a REPOS <<<"${SUPERVISOR_REPOS:-agent-dotfiles agent-supervisor skills skills-private agent-evals}"

# --- #215: which harness is in the pane, and is it busy? --------------------
#
# This script used to answer "is it busy?" with one Claude Code literal --
# `grep -q 'esc to interrupt'` -- in two places, with no harness parameter and
# no third outcome. On a pane that does not paint that exact string the check
# fell through to NOT BUSY, so a mid-turn lane read as a dead one and this
# script's whole purpose inverted: the watchdog acts on idle, so a false idle
# is an intervention against a working lane. Copilot's own busy footer is
# `◎ Working esc interrupt` -- `esc interrupt`, no "to" -- which is exactly the
# near miss a shared literal does not survive. Latent today (the pane watched
# by default is the Claude supervisor loop), live the moment the supervisor
# itself runs on another harness, which is the stated goal.
#
# WHY NOT adapter.py's `classify_capture`, which #215 points at. Because the
# test for an adapter is "is the implementation fit to be called?", not "is
# there a caller?" (agent-dotfiles#223). `classify_capture` matches quoted
# phrases anywhere in a 25-line window -- the wide-window anti-pattern #65
# fixed lanes.sh to avoid, where a lane merely QUOTING "Should I proceed?"
# reads back as blocked -- its own header says not to wire a new caller
# through it before it is rebuilt to lanes.sh's standard, and
# docs/supervisor-disposition.md §5 lists that rebuild as outstanding work.
# It also models only claude and codex; a copilot pane would make it RAISE,
# and a raising classifier inside a bash `if` is a non-zero exit, which is
# "not busy" again. Wiring it in here would have imported the defect.
#
# So: the same harness/*.sh adapters lanes.sh uses, via harness-registry.sh --
# already the estate's fit, tested, per-harness seam -- and a probe with THREE
# outcomes rather than two. `unknown` is the important one: an unrecognised
# harness, an unreadable capture, a harness whose adapter declines to model a
# busy shape, or no adapters at all all resolve to "cannot tell", and every
# caller below treats cannot-tell as busy. A false busy costs one skipped
# tick; a false idle costs a working lane its turn.
HARNESS_REGISTRY_OK=0
if [ -r "$HERE/harness-registry.sh" ]; then
  # shellcheck source=./harness-registry.sh
  . "$HERE/harness-registry.sh" && HARNESS_REGISTRY_OK=1
fi

# Which adapter owns $PANE. SUPERVISOR_HARNESS names it outright; otherwise it
# is inferred from the pane's own command, the same signal lanes.sh keys on.
# That inference is imperfect and the imperfection is filed: `pane_current_
# command` is `node` for EVERY Node-based harness (agent-dotfiles#216), so a
# second Node CLI in the supervisor pane would be handed copilot's chrome.
# It fails toward `unknown` rather than toward a false idle -- another
# harness's markers do not match this one's paint -- so #216 is a precision
# bug here, not a safety one, and is left to #216 rather than fixed in passing.
harness_slot() {
  local cmd
  [ "$HARNESS_REGISTRY_OK" = 1 ] || return 1
  if [ -n "${SUPERVISOR_HARNESS:-}" ]; then
    harness_index_for_name "$SUPERVISOR_HARNESS"
    return $?
  fi
  cmd=$(tmux display-message -p -t "$PANE" '#{pane_current_command}' 2>/dev/null) || return 1
  [ -n "$cmd" ] || return 1
  harness_index_for_command "$cmd"
}

# pane_busy_state <capture> -> prints busy | idle | unknown
#
# Reads only the harness's OWN bounded window (HARNESS_BUSY_TAIL non-empty
# lines: 1 for Claude and Copilot, whose busy marker IS the last line; 4 for
# Codex, whose marker sits above a footer that never changes) rather than
# sweeping the whole capture. That is the #65 discipline: the old probe
# grepped six scrollback lines plus the visible pane, so a pane that had
# merely PRINTED the phrase -- reviewing this very file -- read busy.
pane_busy_state() {
  local capture="$1" hidx lines busy_tail
  hidx=$(harness_slot) || { printf 'unknown\n'; return 0; }
  # An adapter that does not model a busy shape cannot answer this question.
  # Silence is not evidence of idleness -- see harness/copilot.sh's unset
  # blocked markers for the same posture applied to a different probe.
  [ -n "${H_BUSY_RE[$hidx]}" ] || { printf 'unknown\n'; return 0; }
  lines=$(grep -v '^[[:space:]]*$' <<<"$capture")
  # A capture that succeeded and returned nothing: a repainting pane, a pane
  # whose harness has printed nothing yet. The old probe scored this as "the
  # string is absent", i.e. idle, which is the fail-open direction.
  [ -n "$lines" ] || { printf 'unknown\n'; return 0; }
  busy_tail=$(tail -n "${H_BUSY_TAIL[$hidx]}" <<<"$lines")
  if grep -qE "${H_BUSY_RE[$hidx]}" <<<"$busy_tail"; then printf 'busy\n'; else printf 'idle\n'; fi
}

# Why the probe said what it said, for the status file's detail line -- so a
# `harness_unknown` tick names WHICH cannot-tell it hit rather than leaving a
# human to re-derive it.
harness_note() {
  local hidx
  [ "$HARNESS_REGISTRY_OK" = 1 ] || { printf 'no harness-registry.sh beside this watchdog'; return 0; }
  if ! hidx=$(harness_slot); then
    if [ -n "${SUPERVISOR_HARNESS:-}" ]; then
      printf 'no adapter under harness/ is named %s' "$SUPERVISOR_HARNESS"
    else
      printf 'no adapter under harness/ claims pane command %s' \
        "$(tmux display-message -p -t "$PANE" '#{pane_current_command}' 2>/dev/null || echo '<unreadable>')"
    fi
    return 0
  fi
  if [ -z "${H_BUSY_RE[$hidx]}" ]; then
    printf 'harness %s has no busy shape modelled' "${HARNESS_IDS[$hidx]}"
  else
    printf 'harness %s captured nothing to read' "${HARNESS_IDS[$hidx]}"
  fi
}

COOLDOWN=600        # no more than one restart per 10 minutes
MAX_RESTARTS=3      # ...and no more than this many
ESCALATE_WINDOW=3600 # ...within this window, or stop and escalate

# --- #163: inbox-poll heartbeat staleness -----------------------------------
# `inbox-poll.sh` writes `inbox-poll.status` every iteration and pages Jon
# itself from `trap report_stop EXIT` -- but a SIGKILL, an OOM kill, or a hard
# power loss runs no trap at all, so `checked:` simply stops moving. This
# watchdog is the natural external observer: it already runs unattended every
# 180s and already reads-a-status-file-and-decides-if-something-is-overdue for
# the supervisor loop itself (see report()/the escalate branch below).
#
# What watches THIS watcher: nothing does, to the same degree (#170). Every
# tick writes its own `checked:` line to watchdog.status via report() above,
# but no code reads THAT line for staleness -- watchdog_notify.py's
# `--mode heartbeat` reads inbox-poll.status, not watchdog.status, and
# `--mode escalate` (the default, called from report() below) only fires on
# `state: escalate`; it never compares `checked:` against a threshold. The
# LaunchAgent, ~/Library/LaunchAgents/com.jonhill.supervisor-watchdog.plist,
# has `StartInterval` 180 and `RunAtLoad`, and no `KeepAlive` key. If it stops
# firing -- unloaded, launchd killed, the machine off -- nothing pages
# anyone; the estate is silent until Jon notices. That is the same gap #163
# closed for inbox-poll.sh, left open here.
INBOX_POLL_STATUS_PATH="${SUPERVISOR_INBOX_POLL_STATUS:-$STATE/inbox-poll.status}"
INBOX_HEARTBEAT_EPISODE="${SUPERVISOR_HEARTBEAT_EPISODE:-$STATE/.watchdog-heartbeat-episode.json}"
# agent-supervisor#41: watchdog.status is rebuilt from scratch every tick
# (report(), below), so a fact that must survive across ticks -- when
# poller-recover.sh last actually confirmed the poller alive, and how many
# consecutive attempts have failed since -- cannot live only in that file.
# These two small files are that memory. agent-supervisor#28: recovery had
# failed 37 consecutive times while nothing durable recorded either number.
POLLER_RECOVERY_LAST_SUCCESS="${SUPERVISOR_POLLER_RECOVERY_LAST_SUCCESS:-$STATE/.poller-recovery-last-success}"
POLLER_RECOVERY_FAIL_STREAK="${SUPERVISOR_POLLER_RECOVERY_FAIL_STREAK:-$STATE/.poller-recovery-fail-streak}"
POLLER_SERVICE_RE="${LANES_SERVICE_RE:-(^|/)inbox-poll\.sh( |$)}"
# Threshold derived from inbox-poll.sh's own worst-case gap between heartbeat
# writes while the process is genuinely alive -- not a round number (#163):
#   - one iteration can block up to POLL_TIMEOUT+20s before giving up
#     (inbox.sh's CURL_MAX_TIME = timeout+20, so curl always outlives
#     Telegram's own long-poll timeout);
#   - a FAILING iteration then backs off before retrying, capped at 60s
#     (inbox-poll.sh: fail_count<12 ? fail_count*BACKOFF_BASE : 60).
# POLL_TIMEOUT+80 is that worst single-iteration span; doubled for scheduling
# jitter and because this watchdog itself only samples every ~180s, so the
# threshold does not need to be tight to still catch a dead poller within a
# couple of ticks. Default POLL_TIMEOUT=25 -> 2*(25+80) = 210s (3.5min).
INBOX_POLL_TIMEOUT_ASSUMED="${INBOX_POLL_TIMEOUT:-25}"
INBOX_HEARTBEAT_STALE_AFTER="${INBOX_HEARTBEAT_STALE_AFTER:-$(( 2 * (INBOX_POLL_TIMEOUT_ASSUMED + 80) ))}"

# agent-supervisor#133: `source_tasks` is written once at dispatch
# (`record_dispatch`) and never advanced again unless something calls
# `cli.py reconcile-source-tasks` -- #130 shipped that sweep, nothing called
# it, and the row count kept climbing (314 -> 315 -> 322 -> 326) instead of
# draining. This watchdog is the owner, not `loop-tick.md`'s supervisor loop
# and not a separate timer:
#   - it already runs unattended every ${TICK_INTERVAL}s OUTSIDE the loop
#     (this file's own header), so reconciliation survives exactly the
#     failure mode -- a dead or wedged loop -- that lets `source_tasks` rot
#     the longest;
#   - it costs zero model tokens (a bash subprocess, not an agent turn), so
#     it cannot repeat the mistake `loop-tick.md` warns about ("a 3-minute
#     timer driving real work is what exhausted the weekly limit twice");
#   - a bare cron/LaunchAgent timer would be a second unattended entry point
#     with its own PATH, credentials and staleness detection to build and
#     test from scratch -- this file already solved all three for the same
#     kind of duty (see check_poller_window/check_inbox_heartbeat above).
# Throttled independently of the ${TICK_INTERVAL}s tick cadence via a stamp
# file, NOT run on every tick: the sweep is two batched
# `gh ... list --state all --limit 1000` calls per repo (SUPERVISOR_REPOS,
# 5 today = 10 calls/sweep) -- cheap against GitHub's 5000/hr authenticated
# rate limit even at 180s cadence (up to 200 calls/hr), but pointless at that
# cadence: a dispatched issue/PR closes or merges on the order of hours, not
# minutes, so nothing but rate-limit budget is bought by sweeping every tick.
# Default once per hour. A stamp file, not the fixed 180s clock, survives the
# watchdog itself restarting mid-hour without resetting the throttle.
SOURCE_SWEEP_STAMP="${SUPERVISOR_SOURCE_SWEEP_STAMP:-$STATE/.source-task-sweep-last}"
SOURCE_SWEEP_INTERVAL="${SUPERVISOR_SOURCE_SWEEP_INTERVAL:-3600}"

# Credentials + NOTIFY_SCRIPT for the escalate path. Sourced here so the
# LaunchAgent needs no secrets inlined in its plist.
ENVFILE="${NOTIFY_ENV:-$STATE/notify.env}"
# shellcheck source=/dev/null
if [ -r "$ENVFILE" ]; then set -a; . "$ENVFILE"; set +a; fi

now=$(date +%s)
iso=$(date -u +%Y-%m-%dT%H:%M:%SZ)
branch=$(git -C "$HERE" rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)
# A detached worktree -- how the live copy is pinned -- reports "HEAD", which
# tells a reader nothing. Name a ref that actually contains this commit.
if [ "$branch" = "HEAD" ]; then
  # Prefer main when several refs point at the same commit. A worker branching
  # from main creates a second ref at that sha, and picking arbitrarily made
  # the status file report 'code: feat/claim-before-dispatch' while the live
  # copy was faithfully running main's commit. The sha was right and the name
  # was misleading, which is worse than saying nothing in a line whose whole
  # job is telling a human what is running.
  refs=$(git -C "$HERE" for-each-ref --format='%(refname:short)' --points-at HEAD 2>/dev/null)
  branch=$(grep -m1 -x 'main' <<<"$refs" \
        || grep -m1 -x 'origin/main' <<<"$refs" \
        || head -1 <<<"$refs")
  branch="${branch:-detached}"
fi
sha=$(git -C "$HERE" rev-parse --short HEAD 2>/dev/null || echo unknown)

# Which REPOSITORY $sha belongs to (agent-supervisor#1). `code: branch @ sha`
# alone was ambiguous about that the moment this tree stopped being the only
# repo the estate runs code from: the live worktree pinned at `d6312b3` kept
# reporting a plausible-looking branch/sha pair with no way to tell, from the
# status file alone, that it was agent-dotfiles's `d6312b3` and not this
# repo's. Read from `origin`, not hardcoded, so a clone under a second
# machine layout or a fork still reports its own identity.
#
# SUPERVISOR_REPO_NAME overrides for tests and for a layout with no `origin`
# remote at all -- same override shape as SUPERVISOR_STATE/SUPERVISOR_REPOS.
repo=$(git -C "$HERE" remote get-url origin 2>/dev/null | sed 's/\.git$//' \
     | awk -F'[:/]' 'NF>=2{print $(NF-1)"/"$NF}')
if [ -z "$repo" ]; then
  root=$(git -C "$HERE" rev-parse --show-toplevel 2>/dev/null)
  [ -n "$root" ] && repo=$(basename "$root")
fi
repo="${SUPERVISOR_REPO_NAME:-${repo:-unknown}}"

# How far behind main this copy is. The LaunchAgent runs the watchdog from a
# PINNED detached worktree that nothing in this repository updates (#99), so a
# merged fix to watchdog.sh, sleepcheck.py, watchdog_notify.py or loop-tick.md
# can sit green on main indefinitely while the live copy keeps running the old
# one. `code: agent-supervisor@detached @ 9cddafb` reads exactly as healthy as
# a current sha unless the reader already knows what main is.
#
# Reported AND acted on: the `advance:` line at the bottom of this file says
# what this tick did about the drift this line reports. Reporting alone was the
# original design and it was wrong for a reason worth keeping written down --
# see the advance section for the argument.
#
# NO `git fetch` -- this runs every 180s. The comparison is against the LOCAL
# origin/main ref, so a nonzero count is trustworthy and a zero is not proof of
# freshness.
#
# The live copy is a git WORKTREE of this repository, not a standalone clone, so
# it shares one object store and one set of refs with the main checkout and with
# every lane worktree on the machine. origin/main's freshness is therefore an
# emergent property of whatever unrelated git activity has happened recently --
# it may be seconds old because a lane just fetched, or hours old because none
# did. This check cannot tell which. The wording claims only what it knows: that
# IT did not refetch. Do not read it as an assertion that the ref is stale.
behind=$(git -C "$HERE" rev-list --count HEAD..origin/main 2>/dev/null || echo "")
# THREE outcomes, not two. Lumping the unreadable case in with zero is the same
# defect this line exists to fix: if origin/main is missing or git fails, an
# empty result printed as no-note reads exactly like "up to date". Caught in
# review before merge -- the first version had `''|0) code_note=""`.
case "$behind" in
  0)            code_note="" ;;
  ''|*[!0-9]*)  code_note=" (cannot compare — origin/main ref unreadable)" ;;
  *)            code_note=" (${behind} behind origin/main, not refetched by this check)" ;;
esac

log() { printf '%s %s\n' "$iso" "$*" >>"$LOG"; }

# Heartbeat. Written on EVERY exit path, including the healthy one — that is
# the whole point of finding 2. Atomic so a reader never sees a half file.
report() {                       # report <state> <detail> [notify-line]
  local tmp="$STATUS.$$"
  # Remembered for the advance step, which runs on the way out of EVERY exit
  # path and has to know which one it was. Reading it back off the status file
  # would work today and break the first time a path exits without reporting.
  last_state="$1"
  # A missing state directory used to make every write fail silently: the
  # script still exited 0 while watchdog.status quietly stopped updating,
  # which is indistinguishable from a dead cron -- the exact failure this
  # tool exists to detect, occurring inside the tool itself.
  mkdir -p "$(dirname "$STATUS")" 2>/dev/null
  {
    printf 'checked:  %s\n' "$iso"
    printf 'state:    %s\n' "$1"
    printf 'detail:   %s\n' "${2:-}"
    printf 'pane:     %s\n' "$PANE"
    printf 'restarts: %s in the last %ss\n' "${recent:-0}" "$ESCALATE_WINDOW"
    # Which code is actually running. The LaunchAgent executes this file from
    # the repo WORKING TREE, so whichever branch happens to be checked out is
    # what guards the loop. On 2026-08-11 the live watchdog spent a stretch
    # running from a test branch purely because that was the last checkout --
    # it worked, but by luck. An unexpected branch here is a real finding.
    printf 'code:     %s@%s @ %s%s\n' "$repo" "$branch" "$sha" "$code_note"
    # Present only when a send was attempted and failed (#91). "escalate with
    # no notify: line" is therefore "a human was reached"; this line is the
    # difference between that and "the loop is down and NOBODY KNOWS". Written
    # on the second pass below, because the notifier has to read this file to
    # decide before there is any outcome to report.
    #
    # An `if`, not `[ ... ] && printf`: a false test as the LAST command in
    # this group makes the group exit non-zero, the `&& mv` below never runs,
    # and every tick reports CANNOT WRITE STATUS instead of writing one.
    if [ -n "${3:-}" ]; then printf 'notify:   %s\n' "$3"; fi
  } >"$tmp" 2>/dev/null && mv -f "$tmp" "$STATUS" 2>/dev/null \
    || printf '%s WATCHDOG CANNOT WRITE STATUS to %s\n' "$iso" "$STATUS" >&2

  # A recursive call carrying the outcome; it must not run the notifier again.
  if [ -n "${3:-}" ]; then return 0; fi

  # escalate is the only state a human needs told about; every other state
  # (working/waiting_on_jon/cooling_down/restarted/...) stays silent, and
  # dedup is one message per escalation episode, not one per tick. That
  # decision and its dedup state live in tracked, tested code — this line
  # is the whole hookup. See scripts/supervisor/watchdog_notify.py in
  # agent-dotfiles (#50). Resolved from $HERE, not from a guessed clone
  # path: the notifier ships beside this script, so the copy that runs is
  # always the copy that was reviewed alongside the watchdog invoking it --
  # and a test running this file from a worktree exercises that worktree's
  # notifier rather than whatever happens to be in the shared checkout.
  local notify_out notify_rc
  notify_out=$(python3 "$HERE/watchdog_notify.py" \
    --status-path "$STATUS" \
    --episode-state-path "$STATE/.watchdog-escalate-episode.json" \
    --log-path "$STATE/watchdog-notify.log" \
    --notify-script "${NOTIFY_SCRIPT:-}" 2>&1)
  notify_rc=$?
  if [ "$notify_rc" -ne 0 ]; then
    log "NOTIFY-CHECK rc=$notify_rc: $notify_out"
    # Say so in the one file a human `cat`s. The log is append-only and easy
    # to scroll past; watchdog.status is the answer to "where are we", and
    # "escalate" alone reads as "Jon has been told" when the truth may be
    # that nothing got out. Newlines collapsed so the field stays one line.
    report "$1" "${2:-}" "FAILED — escalation did NOT reach a human, retrying next tick: $(printf '%s' "$notify_out" | tr '\n' ' ')"
  fi
}

# --- advancing the code this watchdog itself runs from ---------------------
#
# The LaunchAgent runs this file out of a PINNED detached worktree
# ($SUPERVISOR_LIVE). Nothing about a merge updates that worktree, so the
# `code:` line above exists to say how far behind it is. For a while that was
# the whole story: report the drift, and let the supervisor loop run
# advance-live.sh once per tick to act on it.
#
# That put the fixer in the component that goes down. The loop is exactly what
# stops running when something is wrong -- and during a deliberate escalation
# it is down BY DESIGN -- so the live worktree stopped advancing precisely
# during the incidents it most needed to be current for. The guard ran its
# stalest code in the one situation it exists to handle. Observed 2026-08-11:
# `1 behind origin/main` held across an escalation while the loop slept; the
# missed commit was benign, and would not have been if it had been a fix to
# this file.
#
# The objection this design was originally rejected over is real and is
# answered by the gate, not by refusing to deploy: "a broken watchdog would
# reinstall itself every 180s and nothing would be left to notice."
# advance-live.sh does not move the pin until the candidate commit's OWN
# watchdog.sh has been run from a throwaway worktree and has written a
# well-formed status. A candidate that cannot run cannot be installed, so the
# copy that would be left unable to notice anything never becomes live. That
# check is not reimplemented here; this calls the one script that owns it.
#
# WHY ON THE WAY OUT, AND NOT AT THE TOP:
#   1. Every duty of this tick is already done. An advance can therefore not
#      cost the tick its status write, its restart, or its page.
#   2. advance-live.sh only advances in the window just after a watchdog tick,
#      read from watchdog.status's own `checked:` line. Called at the top of a
#      tick it would read the PREVIOUS tick's timestamp -- ~180s old, outside
#      the window -- and skip forever. Called here it reads the line this tick
#      just wrote.
#   3. Bash reads a script incrementally, by file offset. Checking out over
#      the file a running bash is still reading is how a script executes
#      garbage. An EXIT trap is the one place where nothing further will be
#      read from this file: the trap body is already in memory and the shell
#      exits when it returns. For the same reason advance-live.sh is run from
#      a COPY -- it lives beside this file and would otherwise be overwritten
#      mid-run by its own checkout.
#   4. Most ticks exit early (working / asleep / waiting_on_jon). A call
#      placed inline at the bottom would only ever run on the restart path.
#
# AND ONLY FROM THE PINNED COPY. The identity check below is what keeps this
# from checking out over a developer's own checkout during a test run, and
# what stops the smoke run advance-live.sh performs -- which executes a
# candidate watchdog.sh, this code included -- from recursing into a second
# advance.
#
# DURING AN ESCALATION, IT HOLDS. This is a deliberate choice against the
# opposite one, so the argument belongs here rather than the conclusion alone:
#
#   For advancing: escalation is when stale guard code is most dangerous, and
#   if the escalation is caused by a watchdog bug, the fix sitting on main is
#   the thing that ends it.
#
#   Against, and decisive: escalation is the ONE state in which a human has
#   already been paged. Staleness is then bounded by a person who is on their
#   way and who can run advance-live.sh by hand; that is how the live worktree
#   was advanced before any of this was automated. Set against that bound, the
#   cost of advancing is unbounded: the sha in the status file a human was
#   paged with must still be the sha they find when they look, or they are
#   debugging a system that is rewriting itself underneath the diagnosis. A
#   merge is also a leading suspect for whatever made the loop die three times
#   in an hour, and pulling further changes into a live incident is the way to
#   turn one unknown into several. Freezing is trivially reversible by hand;
#   redeploying mid-diagnosis is not reversible at all in the sense that
#   matters, because the confusion has already happened.
#
#   Residual risk, named rather than papered over: if the page never got out
#   (`notify:` says FAILED) nobody is coming, and the freeze outlasts the
#   incident. That is bounded by this watchdog retrying the page every tick.
#   Gating the hold on delivery was considered and rejected: it makes whether
#   the estate deploys itself depend on whether a phone had signal.
last_state=""

# Physical path, so a symlink anywhere in $HOME cannot stop the live copy from
# recognising itself and quietly disable the whole mechanism.
real_path() { (cd "$1" 2>/dev/null && pwd -P) || printf '%s' "$1"; }

# Add or replace the `advance:` line in the status file. Additive by
# construction: the report written above is the contract, this is one more
# line on the end of it. Written with the same tmp+rename the report uses, so
# a reader never sees a half file.
advance_note() {                 # advance_note <line>
  local tmp="$STATUS.adv.$$"
  [ -f "$STATUS" ] || return 0
  { grep -v '^advance:' "$STATUS"; printf 'advance:  %s\n' "$1"; } >"$tmp" 2>/dev/null
  # Only rename a file that has content. A truncated write reaching $STATUS
  # would erase the tick's whole report to add one line to it.
  if [ -s "$tmp" ]; then mv -f "$tmp" "$STATUS" 2>/dev/null; fi
  rm -f "$tmp" 2>/dev/null
  return 0
}

# Same tmp+rename append advance_note uses, for a second, unrelated line: the
# result of #163's inbox-poll heartbeat check. Kept as its own function rather
# than generalizing advance_note, so a change to one line's format cannot
# silently reach the other.
heartbeat_note() {               # heartbeat_note <line>
  local tmp="$STATUS.hb.$$"
  [ -f "$STATUS" ] || return 0
  { grep -v '^heartbeat:' "$STATUS"; printf 'heartbeat: %s\n' "$1"; } >"$tmp" 2>/dev/null
  if [ -s "$tmp" ]; then mv -f "$tmp" "$STATUS" 2>/dev/null; fi
  rm -f "$tmp" 2>/dev/null
  return 0
}

# Same tmp+rename append shape again, for the process-table measurement of
# inbox-poll.sh. This line is intentionally absent in the healthy one-poller
# case: the detector should be silent when the process table is exactly right.
poller_note() {                  # poller_note <line>
  local tmp="$STATUS.poller.$$"
  [ -f "$STATUS" ] || return 0
  { grep -v '^poller:' "$STATUS"; printf 'poller:    %s\n' "$1"; } >"$tmp" 2>/dev/null
  if [ -s "$tmp" ]; then mv -f "$tmp" "$STATUS" 2>/dev/null; fi
  rm -f "$tmp" 2>/dev/null
  return 0
}

# Same tmp+rename append shape again, for the recovery mechanism's own
# availability. Absent is different from present-but-not-runnable: the first is
# a partial install, the second is a broken install with a concrete chmod fix.
recovery_note() {                # recovery_note <line>
  local tmp="$STATUS.recovery.$$"
  [ -f "$STATUS" ] || return 0
  { grep -v '^recovery:' "$STATUS"; printf 'recovery: %s\n' "$1"; } >"$tmp" 2>/dev/null
  if [ -s "$tmp" ]; then mv -f "$tmp" "$STATUS" 2>/dev/null; fi
  rm -f "$tmp" 2>/dev/null
  return 0
}

# Same tmp+rename append shape again, for the source_tasks sweep (#133).
# Absent (no line at all) means "not due this tick", the ordinary case --
# only a tick that actually ran the sweep, or found it could not, writes one.
sweep_note() {                   # sweep_note <line>
  local tmp="$STATUS.sweep.$$"
  [ -f "$STATUS" ] || return 0
  { grep -v '^sweep:' "$STATUS"; printf 'sweep:    %s\n' "$1"; } >"$tmp" 2>/dev/null
  if [ -s "$tmp" ]; then mv -f "$tmp" "$STATUS" 2>/dev/null; fi
  rm -f "$tmp" 2>/dev/null
  return 0
}

# Runs on EVERY exit path, regardless of which early `exit 0` above fired --
# that is the whole reason this lives in the trap rather than in the main
# body: the supervisor-loop checks below (busy/idle/asleep/...) all return
# early, but the inbox-poll heartbeat is a different subsystem entirely and
# must be read every tick no matter which of those branches this one took.
# Mirrors the escalate notifier's call shape (report(), above): read the
# episode-gated decision from watchdog_notify.py, log it, and surface it in
# watchdog.status even when the page itself could not be delivered (#163's
# "failure must be visible locally" constraint) -- notify.log and
# watchdog.status are what remain when the channel is the thing that broke.
check_inbox_heartbeat() {
  local notify_out notify_rc
  notify_out=$(python3 "$HERE/watchdog_notify.py" \
    --mode heartbeat \
    --heartbeat-status-path "$INBOX_POLL_STATUS_PATH" \
    --threshold-seconds "$INBOX_HEARTBEAT_STALE_AFTER" \
    --episode-state-path "$INBOX_HEARTBEAT_EPISODE" \
    --log-path "$STATE/watchdog-notify.log" \
    --notify-script "${NOTIFY_SCRIPT:-}" 2>&1)
  notify_rc=$?
  heartbeat_note "$(printf '%s' "$notify_out" | tr '\n' ' ')"
  if [ "$notify_rc" -ne 0 ]; then
    log "HEARTBEAT-CHECK FAILED rc=$notify_rc: $notify_out"
  else
    log "HEARTBEAT-CHECK: $notify_out"
  fi
}

# agent-supervisor#18: inbox-poll.status is authored by the poller itself, and
# four duplicate pollers still reported one healthy pid because the last writer
# won. The duplicate detector must therefore ask the kernel, not the status
# file. It reports zero distinctly from more-than-one, and it never reaps: a
# wrong reap bounces the live inbound channel.
poller_process_rows() {
  command -v pgrep >/dev/null 2>&1 || return 2
  command -v ps >/dev/null 2>&1 || return 2
  local pid cmd start ppid pgid records line parent_pid parent_ppid parent_pgid parent_start skip
  while IFS= read -r pid; do
    [ -n "$pid" ] || continue
    cmd=$(ps -o command= -p "$pid" 2>/dev/null) || continue
    [[ "$cmd" =~ $POLLER_SERVICE_RE ]] || continue
    ppid=$(ps -o ppid= -p "$pid" 2>/dev/null | sed 's/^[[:space:]]*//;s/[[:space:]]*$//') || ppid=""
    pgid=$(ps -o pgid= -p "$pid" 2>/dev/null | sed 's/^[[:space:]]*//;s/[[:space:]]*$//') || pgid=""
    start=$(ps -o lstart= -p "$pid" 2>/dev/null | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    [ -n "$start" ] || start=$(ps -o start= -p "$pid" 2>/dev/null | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    records="${records:-}${pid}	${ppid:-unknown}	${pgid:-unknown}	${start:-unknown}
"
  done < <(pgrep -f inbox-poll.sh 2>/dev/null || true)
  while IFS=$'\t' read -r pid ppid pgid start; do
    [ -n "$pid" ] || continue
    skip=0
    while IFS=$'\t' read -r parent_pid parent_ppid parent_pgid parent_start; do
      [ -n "$parent_pid" ] || continue
      if [ "$ppid" = "$parent_pid" ] && [ "$pgid" != "unknown" ] && [ "$pgid" = "$parent_pgid" ]; then
        skip=1
        break
      fi
    done <<<"${records:-}"
    [ "$skip" -eq 1 ] && continue
    printf '%s\t%s\n' "$pid" "${start:-unknown}"
  done <<<"${records:-}"
  return 0
}

check_poller_process_count() {
  local rows rows_rc count detail line pid start
  rows=$(poller_process_rows)
  rows_rc=$?
  if [ "$rows_rc" -ne 0 ]; then
    log "POLLER-CHECK FAILED rc=$rows_rc: could not measure live inbox-poll.sh processes from pgrep/ps"
    poller_note "unknown — could not measure live inbox-poll.sh processes from pgrep/ps"
    return 0
  fi
  count=$(grep -c . <<<"$rows" 2>/dev/null || true)
  if [ "$count" -eq 1 ]; then
    return 0
  fi
  if [ "$count" -eq 0 ]; then
    log "POLLER-CHECK: zero live inbox-poll.sh processes by pid — dead-poller recovery handles this"
    poller_note "dead — zero live inbox-poll.sh processes by pid; recovery handles this"
    return 0
  fi

  detail="${count} live inbox-poll.sh processes by pid"
  while IFS=$'\t' read -r pid start; do
    [ -n "$pid" ] || continue
    detail="$detail; pid $pid started ${start:-unknown}"
  done <<<"$rows"
  log "POLLER-DUPLICATE: $detail"
  poller_note "DUPLICATE — $detail"
  return 0
}

# agent-supervisor#10: a poller that exits takes its window with it, so
# nothing is left for the cooperative restart path to address. Runs on EVERY
# exit path, same reasoning as check_inbox_heartbeat above -- this is a
# different subsystem from the supervisor-loop checks (busy/idle/asleep/...)
# that return early throughout this file, and has to be read every tick
# regardless of which of those branches this one took. poller-recover.sh
# owns its own idempotency (a lock around window creation, and a respawn
# that can only ever land on the one pane it already found) -- nothing here
# needs to serialize it further.
check_poller_window() {
  if [ ! -e "$HERE/poller-recover.sh" ]; then
    log "POLLER-RECOVER-MISSING: poller-recover.sh is missing beside this watchdog; reinstall or advance the live worktree"
    recovery_note "missing — poller-recover.sh is missing beside this watchdog; reinstall or advance the live worktree"
    return 0
  fi
  if [ ! -x "$HERE/poller-recover.sh" ]; then
    log "POLLER-RECOVER-BROKEN: poller-recover.sh exists but is not executable; run chmod +x $HERE/poller-recover.sh or restore the committed 100755 mode"
    recovery_note "broken — poller-recover.sh exists but is not executable; run chmod +x $HERE/poller-recover.sh or restore the committed 100755 mode"
    return 0
  fi
  local out rc
  # The poller lives in the same session $PANE does -- derived from it
  # rather than a second independent default, so a deployment that points
  # SUPERVISOR_PANE at a non-default session does not leave poller-recover.sh
  # quietly acting on (or missing) the wrong one. LANES_SESSION, if the
  # caller already set it, still wins -- same override precedence lanes.sh
  # and advance-live.sh give it.
  out=$(SUPERVISOR_STATE="$STATE" SUPERVISOR_LIVE="$LIVE" \
        LANES_SESSION="${LANES_SESSION:-${PANE%%:*}}" \
        "$HERE/poller-recover.sh" 2>&1)
  rc=$?
  # agent-supervisor#41 (agent-supervisor#28): a poller-recover.sh that runs
  # and exits nonzero for a real reason (ambiguous windows, an orphan it
  # refuses to duplicate, tmux itself failing) used to reach only
  # watchdog.log -- FAILED rc=1 lines piled up there for 37 straight ticks
  # while digest.sh, which never reads watchdog.log, kept reporting
  # `poller: alive=true state=ok`. The recovery MECHANISM failing is a
  # different fact from the poller PROCESS being up, and only the log
  # captured it. recovery_note is the outcome field digest.sh reads; the
  # healthy path (rc=0) stays silent below, same as the missing/broken
  # checks above -- a recovery attempt that found nothing to fix is not
  # noise, only one that failed is.
  if [ "$rc" -ne 0 ]; then
    local streak=0
    [ -r "$POLLER_RECOVERY_FAIL_STREAK" ] && streak=$(cat "$POLLER_RECOVERY_FAIL_STREAK" 2>/dev/null)
    [[ "$streak" =~ ^[0-9]+$ ]] || streak=0
    streak=$((streak + 1))
    printf '%s' "$streak" >"$POLLER_RECOVERY_FAIL_STREAK" 2>/dev/null
    local last_success=""
    [ -r "$POLLER_RECOVERY_LAST_SUCCESS" ] && last_success=$(cat "$POLLER_RECOVERY_LAST_SUCCESS" 2>/dev/null)
    log "POLLER-RECOVER FAILED rc=$rc: $out"
    recovery_note "failed (attempt ${streak} in a row) — rc=$rc: $(printf '%s' "$out" | tr '\n' ' ') — last confirmed recovery: ${last_success:-never}"
  else
    printf '0' >"$POLLER_RECOVERY_FAIL_STREAK" 2>/dev/null
    printf '%s' "$iso" >"$POLLER_RECOVERY_LAST_SUCCESS" 2>/dev/null
    if [ -n "$out" ]; then
      log "POLLER-RECOVER: $out"
    fi
  fi
}

# agent-supervisor#133: runs on EVERY exit path, same reasoning as
# check_poller_window/check_inbox_heartbeat above -- this is a different
# subsystem from the supervisor-loop checks (busy/idle/asleep/...) that
# return early throughout this file, and the whole point of putting it here
# is that it must still run on the ticks where those checks short-circuit.
# Self-throttled against SOURCE_SWEEP_STAMP (see that var's definition for
# the cost/cadence reasoning); most ticks return in the first branch below
# having done nothing.
check_source_task_sweep() {
  local last=0
  if [ -r "$SOURCE_SWEEP_STAMP" ]; then
    last=$(cat "$SOURCE_SWEEP_STAMP" 2>/dev/null)
  fi
  [[ "$last" =~ ^[0-9]+$ ]] || last=0
  if [ $(( now - last )) -lt "$SOURCE_SWEEP_INTERVAL" ]; then
    return 0
  fi
  if [ ! -e "$HERE/cli.py" ]; then
    log "SOURCE-SWEEP-MISSING: cli.py is missing beside this watchdog; reinstall or advance the live worktree"
    sweep_note "missing — cli.py is missing beside this watchdog"
    return 0
  fi
  local out rc
  out=$("${SUPERVISOR_PYTHON:-python3}" "$HERE/cli.py" --state-dir "$STATE" reconcile-source-tasks 2>&1)
  rc=$?
  # The stamp is written whether the sweep succeeded or not: a repo-fetch
  # failure inside the sweep already leaves its own rows untouched and
  # reports itself in `errors` (reconcile_sources.py's own fail-closed
  # contract) -- retrying every 180s instead of waiting out the interval
  # would not recover a down `gh`/network any faster, only spend more of the
  # rate-limit budget finding out again.
  printf '%s' "$now" >"$SOURCE_SWEEP_STAMP" 2>/dev/null
  if [ "$rc" -ne 0 ]; then
    log "SOURCE-SWEEP FAILED rc=$rc: $out"
    sweep_note "failed — rc=$rc: $(printf '%s' "$out" | tr '\n' ' ')"
    return 0
  fi
  # Review fix (PR #142): this formatter used to build its f-strings with
  # backslash-escaped double quotes inside the `{...}` expression --
  # `f"updated={len(d.get(\"updated\", []))} "` -- which is a SyntaxError on
  # every CPython from 3.9 to 3.14, not a version-specific issue. `python3
  # -c` therefore failed before running a single line, on every invocation,
  # success or not. That would have been visible immediately except the next
  # line redirected the formatter's stderr to /dev/null -- so a SUCCESSFUL
  # sweep (the row really did flip OPEN -> CLOSED) still rendered "not
  # parseable" in watchdog.status forever, and nothing short of reading the
  # Python by eye would have caught it. Fixed two ways: the f-strings below
  # hold no quote characters at all (values are computed into plain
  # variables first, so there is nothing left to escape), and the
  # formatter's stderr is now captured, not discarded -- a crash in the
  # formatter itself surfaces as FORMATTER-CRASHED, distinct from the sweep's
  # own report genuinely being unparseable JSON (a real, different outcome
  # `reconcile_sources.py` can also produce).
  local summary py_rc py_err py_err_file
  py_err_file="$STATUS.sweep-fmt-err.$$"
  summary=$(printf '%s' "$out" | "${SUPERVISOR_PYTHON:-python3}" -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    print("unparseable report")
    sys.exit(0)
updated = len(d.get("updated", []))
unchanged = len(d.get("unchanged", []))
unresolved = len(d.get("unresolved", []))
errors = len(d.get("errors", []))
print(f"updated={updated} unchanged={unchanged} unresolved={unresolved} errors={errors}")
' 2>"$py_err_file")
  py_rc=$?
  py_err=$(cat "$py_err_file" 2>/dev/null)
  rm -f "$py_err_file" 2>/dev/null
  if [ "$py_rc" -ne 0 ]; then
    log "SOURCE-SWEEP-FORMATTER-CRASHED rc=$py_rc: $py_err"
    sweep_note "formatter crashed — rc=$py_rc: $(printf '%s' "$py_err" | tr '\n' ' ')"
    return 0
  fi
  [ -n "$summary" ] || summary="ran, output not parseable: $(printf '%s' "$out" | tr '\n' ' ')"
  log "SOURCE-SWEEP: $summary"
  sweep_note "$summary"
  return 0
}

# Runs on EVERY exit path. It must never change this tick's exit status and
# must never abort it: a refused advance is a report, not a crash -- the tick
# it rode out on had already succeeded, and failing it would turn "the code is
# one commit stale" into "the watchdog is down". Takes rc as an argument
# rather than reading $? itself: it is no longer the trap handler directly
# (on_exit, below, is, so it can also run check_inbox_heartbeat on every exit
# path) and $? inside a called function reflects THAT call, not the script's.
advance_on_exit() {
  local rc="$1"
  rm -f "$STATUS.$$" 2>/dev/null

  local root
  root=$(git -C "$HERE" rev-parse --show-toplevel 2>/dev/null) || return $rc
  [ -n "$root" ] || return $rc
  [ "$(real_path "$root")" = "$(real_path "$LIVE")" ] || return $rc
  [ -f "$HERE/advance-live.sh" ] || {
    log "ADVANCE-UNAVAILABLE: no advance-live.sh beside this watchdog"
    advance_note "unavailable — no advance-live.sh beside this watchdog"
    return $rc
  }

  if [ "$last_state" = escalate ]; then
    log "ADVANCE-HELD: escalation in effect — leaving the live copy at $sha for the human who was paged"
    advance_note "held — escalation in effect, live copy left at $sha for diagnosis; run advance-live.sh by hand to move it"
    return $rc
  fi

  # The copy: see point 3 above. Deleted whatever happens, including when the
  # advance it performed replaced the original underneath it.
  #
  # Every file advance-live.sh reaches for via $HERE must land beside the
  # copy too, or its checks silently no-op inside copy_dir (agent-supervisor
  # #57: poller-recover.sh was missing here, so the watchdog's every-tick
  # relaunch attempt gave up before starting, every time, with only a log
  # line to show for it). Keep this list in sync with `grep -n '\$HERE/'
  # advance-live.sh`.
  #
  # poller-window.sh is a HARD dependency (advance-live.sh sources it
  # unconditionally near the top): missing it aborts the copy entirely, the
  # same refuse-rather-than-run posture the rest of this function already
  # takes. poller-recover.sh is not: advance-live.sh already degrades
  # gracefully when it is missing (POLLER-PROMPT-RELAUNCH-SKIPPED, #48/#56),
  # falling back to the watchdog's own periodic backstop call below. Refusing
  # the WHOLE advance over a missing poller-recover.sh would trade a live-code
  # freeze for a poller-relaunch degradation that already reports itself
  # loudly through its own path -- worse than the bug this fixes. So this
  # copies it best-effort and logs loudly on failure, but never blocks the
  # advance on it.
  local copy_dir copy out arc
  copy_dir=$(mktemp -d "${TMPDIR:-/tmp}/watchdog-advance-live.XXXXXX" 2>/dev/null) || return $rc
  copy="$copy_dir/advance-live.sh"
  cp "$HERE/advance-live.sh" "$copy" 2>/dev/null || { rm -rf "$copy_dir" 2>/dev/null; return $rc; }
  cp "$HERE/poller-window.sh" "$copy_dir/poller-window.sh" 2>/dev/null || { rm -rf "$copy_dir" 2>/dev/null; return $rc; }
  cp "$HERE/session-defaults.sh" "$copy_dir/session-defaults.sh" 2>/dev/null || { rm -rf "$copy_dir" 2>/dev/null; return $rc; }
  # -p: poller-recover.sh is exec'd directly (not sourced via bash), so its
  # executable bit must survive the copy, not fall through the umask.
  if ! cp -p "$HERE/poller-recover.sh" "$copy_dir/poller-recover.sh" 2>/dev/null; then
    log "ADVANCE-COPY-INCOMPLETE: poller-recover.sh could not be copied beside advance-live.sh -- the prompt poller relaunch will no-op this tick, watchdog poller-recover.sh remains the backstop"
  fi
  out=$(SUPERVISOR_STATE="$STATE" SUPERVISOR_STATUS="$STATUS" bash "$copy" "$root" 2>&1)
  arc=$?
  # advance-live.sh's prompt_poller_relaunch backgrounds a waiter that execs
  # poller-recover.sh only once the OLD poller pid exits -- up to
  # INBOX_POLL_RELAUNCH_WAIT_SECONDS later -- and returns immediately itself,
  # so "bash $copy" above has already come back by the time that waiter is
  # still running. Deleting copy_dir synchronously here race-deletes
  # poller-recover.sh out from under it before it ever gets exec'd
  # (agent-supervisor#57: this reproduced as ENOENT even with poller-recover.sh
  # correctly copied in above). Defer the cleanup past that waiter's own
  # deadline instead of racing it.
  ( sleep "$(( ${INBOX_POLL_RELAUNCH_WAIT_SECONDS:-45} + 15 ))"; rm -rf "$copy_dir" ) >/dev/null 2>&1 &
  out=$(printf '%s' "$out" | tr '\n' ' ')

  if [ "$arc" -ne 0 ]; then
    log "ADVANCE-REFUSED rc=$arc: $out"
    advance_note "refused — $out"
  elif [[ "$out" == *"advance-live: advanced"* ]]; then
    log "ADVANCED: $out"
    advance_note "advanced — $out (the code: line above is what THIS tick ran)"
  elif [ -z "$out" ] || [[ "$out" == *"advance-live: current"* ]]; then
    # agent-supervisor#11: advance-live.sh now fetches before it can call
    # this "current" -- so the empty-output case (an older candidate that
    # predates that fetch) and the explicit "advance-live: current" report
    # both mean the same thing here: genuinely current, not merely silent.
    advance_note "current — ${out:-live copy already at origin/main (fetched fresh)}"
  else
    # A gate declining is the ordinary case, not a fault: outside the post-tick
    # window, no status to compare against yet. Says so without shouting.
    advance_note "not this tick — $out"
  fi
  return $rc
}

# The actual trap handler. Captures $? FIRST, exactly as advance_on_exit used
# to do directly -- everything after this line runs commands of its own that
# would otherwise clobber it.
on_exit() {
  local rc=$?
  check_inbox_heartbeat
  check_poller_process_count
  check_poller_window
  check_source_task_sweep
  advance_on_exit "$rc"
  return $rc
}
trap on_exit EXIT

# How many restarts inside the escalation window?
recent=0
if [ -f "$HISTORY" ]; then
  recent=$(awk -v now="$now" -v w="$ESCALATE_WINDOW" '$1 > now - w' "$HISTORY" 2>/dev/null | wc -l | tr -d ' ')
  awk -v now="$now" -v w="$ESCALATE_WINDOW" '$1 > now - w' "$HISTORY" >"$HISTORY.tmp" 2>/dev/null \
    && mv -f "$HISTORY.tmp" "$HISTORY"
fi

if ! tmux has-session -t "$(lanes_session_or_default)" 2>/dev/null; then
  report no_session "tmux session '$(lanes_session_or_default)' does not exist"
  log "no $(lanes_session_or_default) session"; exit 0
fi

pane=$(tmux capture-pane -p -t "$PANE" -S -6 2>/dev/null) || {
  report pane_unreadable "cannot capture $PANE"
  log "pane $PANE unreadable"; exit 0
}

# Busy? Asked of the pane's own harness adapter, with cannot-tell treated as
# busy (#215 — see pane_busy_state above for why that direction and not the
# other). Everything below this point is reasoning about an IDLE pane, so
# every non-idle answer has to stop here.
case "$(pane_busy_state "$pane")" in
  busy)
    report working "supervisor turn in progress"
    exit 0 ;;
  unknown)
    report harness_unknown "cannot tell whether $PANE is busy ($(harness_note)) — assuming busy, not restarting"
    log "HARNESS-UNKNOWN: $(harness_note) — assuming busy rather than concluding idle"
    exit 0 ;;
esac

# Idle is NOT evidence of death. A dynamic /loop sleeps by scheduling its own
# wakeup, so between ticks the pane looks exactly like a stopped loop. Measured
# 2026-08-11: the supervisor had scheduled delay=3600 on eight consecutive
# ticks ("maximum sleep keeps the loop alive") and had crashed zero times --
# while this watchdog was restarting it every cooldown because it counted nine
# open issues the supervisor had correctly judged gated on Jon. The watchdog
# was the thing interrupting the loop it existed to protect.
#
# sleepcheck.py reads the last ScheduleWakeup from the live transcript and
# says whether a wakeup is still pending. It observes rather than trusting a
# cooperating writer, which is the same reason completion is not inferred from
# echoed prompt text.
if python3 "$HERE/sleepcheck.py" >/dev/null 2>&1; then
  report asleep "loop has a pending wakeup — idle is correct, not dead"
  exit 0
fi

# Idle. Is there anything to do? A failed query is NOT zero (finding 1).
work=0; degraded=""
for r in "${REPOS[@]}"; do
  if n=$(gh issue list --repo "jonhill90/$r" --state open --limit 60 \
           --json number,labels \
           --jq '[.[]|select([.labels[].name]|any(.=="parked" or .=="question")|not)]|length' 2>/dev/null) \
     && [[ "$n" =~ ^[0-9]+$ ]]; then
    work=$((work + n))
  else
    degraded="${degraded}${r} "
  fi
  if p=$(gh pr list --repo "jonhill90/$r" --state open --json number --jq 'length' 2>/dev/null) \
     && [[ "$p" =~ ^[0-9]+$ ]]; then
    work=$((work + p))
  else
    degraded="${degraded}${r}:pr "
  fi
done

if [ -n "$degraded" ]; then
  log "DEGRADED: GitHub unreachable for: ${degraded% } — treating as work-present, not as zero"
fi

if [ -z "$degraded" ] && [ "$work" -eq 0 ]; then
  report waiting_on_jon "idle, queue empty or everything gated on Jon — correct to be still"
  exit 0
fi

# Idle with work (or unknown). The loop should not be stopped.
if [ "$recent" -ge "$MAX_RESTARTS" ]; then
  report escalate "restarted $recent times in ${ESCALATE_WINDOW}s and it keeps dying — NOT restarting again, needs a human"
  log "ESCALATE: $recent restarts in ${ESCALATE_WINDOW}s; leaving the loop down deliberately"
  # Reaching a human happens inside report() above -- ONLY on escalate, never
  # on working/waiting_on_jon/cooling_down/restarted, and deduplicated to one
  # delivered message per escalate episode by watchdog_notify.py, because a
  # watchdog that messages every tick gets muted and a muted channel is
  # indistinguishable from no channel.
  #
  # There used to be a SECOND, argument-less call to watchdog_notify.py right
  # here. It was redundant -- report() had already run the same check one line
  # earlier -- and actively harmful: with no arguments it fell back to the
  # default state paths under $HOME instead of this run's $STATE, so a test or
  # a second machine layout read and rewrote the LIVE episode file. A test
  # exercising this branch could mark the real escalation delivered and
  # suppress a real page.
  exit 0
fi

last=$(cat "$STAMP" 2>/dev/null || echo 0)
if [ $((now - last)) -lt "$COOLDOWN" ]; then
  report cooling_down "idle with ${work} item(s); last restart $((now - last))s ago"
  exit 0
fi

# Do not clobber text Jon has typed and not submitted.
#
# The harness renders the PREVIOUS prompt as ghost placeholder text on an empty
# input line, so "the line looks non-empty" proves nothing. Append a character
# and see whether it lands on top of existing text or replaces the ghost:
#
#   real text  ->  "❯ do the thing"  becomes  "❯ do the thingX"
#   ghost text ->  "❯ do the thing"  becomes  "❯ X"
#
# Real iff the new line is the old line with X appended. The first version
# compared the wrong direction and called every ghost line real, which would
# have meant the watchdog never restarted anything.
promptline() { tmux capture-pane -p -t "$PANE" -S -3 2>/dev/null | grep -m1 '^❯' || true; }
before=$(promptline)
tmux send-keys -t "$PANE" -l 'X' 2>/dev/null; sleep 1
after=$(promptline)
tmux send-keys -t "$PANE" BSpace 2>/dev/null; sleep 1
if [ -n "$before" ] && [ "$after" = "${before}X" ]; then
  report human_typing "un-submitted text in the pane — left alone"
  log "real un-submitted text in pane — not touching it"
  exit 0
fi

# Re-check busy IMMEDIATELY before sending. The earlier check is stale by
# several seconds: the ghost-text probe types a character, sleeps, backspaces
# and sleeps again, and the supervisor can start a turn inside that window.
#
# Losing this race is not harmless. A slash command delivered to a busy pane
# is QUEUED AS PLAIN TEXT and never parses as a command -- the pane shows
# "Press up to edit queued messages" -- so the /loop never re-arms the loop.
# Measured 2026-08-11: the supervisor transcript held 91 "/loop" messages and
# only 11 ScheduleWakeup calls, and had not re-armed since 02:31Z while the
# watchdog restarted it all night believing it had.
#
# Same three-outcome probe as the first check (#215), and the same direction:
# if this second look cannot tell, nothing is sent. The cost of holding is one
# tick; the cost of sending into a pane that is actually busy is a /loop that
# never re-arms the loop at all.
recheck=$(tmux capture-pane -p -t "$PANE" -S -6 2>/dev/null) || recheck=""
case "$(pane_busy_state "$recheck")" in
  busy)
    report working "became busy during the pre-send probe; not sending a /loop into a busy pane"
    log "SKIPPED restart — pane became busy mid-probe (a queued /loop is inert)"
    exit 0 ;;
  unknown)
    report harness_unknown "cannot tell whether $PANE is busy on the pre-send re-check ($(harness_note)) — not sending"
    log "SKIPPED restart — pre-send re-check could not tell: $(harness_note)"
    exit 0 ;;
esac

tmux send-keys -t "$PANE" C-u 2>/dev/null; sleep 1
tmux send-keys -t "$PANE" -l \
  "/loop Supervisor tick. Follow $TICK exactly. Dispatch to idle worker lanes rather than implementing yourself. Never call stop, always re-arm." 2>/dev/null
sleep 2
tmux send-keys -t "$PANE" Enter 2>/dev/null

echo "$now" >"$STAMP"
echo "$now" >>"$HISTORY"
recent=$((recent + 1))
report restarted "was idle with ${work} item(s)${degraded:+ (GitHub degraded: ${degraded% })}"
log "RESTARTED loop — idle with ${work} actionable item(s)${degraded:+; DEGRADED: ${degraded% }}"
