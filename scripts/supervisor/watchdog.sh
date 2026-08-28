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
# shellcheck source=./send.sh
. "$HERE/send.sh"
# shellcheck source=./poller-lib.sh
. "$HERE/poller-lib.sh"
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
read -r -a REPOS <<<"${SUPERVISOR_REPOS:-agent-dotfiles $AGENT_SUPERVISOR_DEFAULT_REPO skills skills-private agent-evals}"

# shellcheck source=./watchdog-harness.sh
. "$HERE/watchdog-harness.sh"


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

# --- agent-supervisor#276: quota-watch heartbeat staleness ------------------
# quota-watch.sh is the estate's ONLY path back to work once a quota window
# closes (see that file's own header) -- and on 2026-08-16 it hung inside an
# unbounded `quota.sh check` call for three hours while `pgrep` kept
# reporting it alive on every tick, because "the process exists" was the
# only thing anything checked. quota-watch.sh now writes a `checked:`/
# `state:` heartbeat to $QUOTA_WATCH_STATUS_PATH after each iteration's work
# (never before), the same shape #163 already gave inbox-poll.status --
# reused here via the SAME generic `--mode heartbeat` in watchdog_notify.py,
# not a new parser. A live pid with a stale heartbeat is exactly the case
# that went undetected for three hours; this check reports it as unhealthy
# regardless of what the process table says, because the process table is
# not what this check reads.
QUOTA_WATCH_STATUS_PATH="${SUPERVISOR_QUOTA_WATCH_STATUS:-$STATE/.quota-watch.state}"
QUOTA_WATCH_HEARTBEAT_EPISODE="${SUPERVISOR_QUOTA_WATCH_HEARTBEAT_EPISODE:-$STATE/.watchdog-quota-watch-heartbeat-episode.json}"
# 2x quota-watch.sh's own default poll interval (300s) -- same "under ~2x
# the loop interval" rule the brief states outright, so one missed tick from
# scheduling jitter alone never pages, but two in a row does.
QUOTA_WATCH_HEARTBEAT_STALE_AFTER="${QUOTA_WATCH_HEARTBEAT_STALE_AFTER:-600}"

# --- agent-supervisor#341: weekly-watch heartbeat staleness -----------------
# weekly-watch.sh (from #328/#327) shipped with no liveness instrumentation
# at all -- no heartbeat on any code path, and no check here. A hung
# process, a permanently broken meter, or a launchd job that silently
# stopped firing (launchd reports a job "loaded" even if it has not run
# since it was loaded -- the specific trap this check exists to avoid) all
# produced the identical observable state as "everything's fine, below
# threshold": no file, only a `weekly-watch.log` line nothing scans. Same
# generic `--mode heartbeat` reuse as check_quota_watch_heartbeat above,
# against weekly-watch.sh's own `.weekly-watch.state` stamp and a separate
# episode -- a live pid (or a freshly-loaded launchd job) with a stale
# heartbeat must read as unhealthy regardless of what launchd's own
# "loaded" status or the process table says, because neither is what this
# check reads.
WEEKLY_WATCH_STATUS_PATH="${SUPERVISOR_WEEKLY_WATCH_STATUS:-$STATE/.weekly-watch.state}"
WEEKLY_WATCH_HEARTBEAT_EPISODE="${SUPERVISOR_WEEKLY_WATCH_HEARTBEAT_EPISODE:-$STATE/.watchdog-weekly-watch-heartbeat-episode.json}"
# weekly-watch.sh has no loop of its own -- launchd ticks it every 1800s
# (StartInterval). 2x that interval, same "under ~2x the loop interval"
# rule QUOTA_WATCH_HEARTBEAT_STALE_AFTER above uses, so one missed launchd
# tick from scheduling jitter alone never pages, but two in a row does.
WEEKLY_WATCH_HEARTBEAT_STALE_AFTER="${WEEKLY_WATCH_HEARTBEAT_STALE_AFTER:-3600}"
# agent-supervisor#41: watchdog.status is rebuilt from scratch every tick
# (report(), below), so a fact that must survive across ticks -- when
# poller-recover.sh last actually confirmed the poller alive, and how many
# consecutive attempts have failed since -- cannot live only in that file.
# These two small files are that memory. agent-supervisor#28: recovery had
# failed 37 consecutive times while nothing durable recorded either number.
POLLER_RECOVERY_LAST_SUCCESS="${SUPERVISOR_POLLER_RECOVERY_LAST_SUCCESS:-$STATE/.poller-recovery-last-success}"
POLLER_RECOVERY_FAIL_STREAK="${SUPERVISOR_POLLER_RECOVERY_FAIL_STREAK:-$STATE/.poller-recovery-fail-streak}"
# POLLER_SERVICE_RE is defined by poller-lib.sh (sourced above).
# agent-supervisor#112: which never-busy lane names were last notified about,
# so a lane that is STILL stuck on the next tick does not re-page -- the same
# episode discipline watchdog_notify.py's escalate/heartbeat modes already
# use, kept as a plain file here because this check reads WORKER lanes
# (lanes.sh), a different subsystem from either of those two, and does not
# share their status-file shape. Content, not a boolean: a SECOND, different
# lane going never-busy while the first is still stuck must still page, so
# the dedup key is the actual set of stuck names, not "has this fired before".
NEVER_BUSY_EPISODE="${SUPERVISOR_NEVER_BUSY_EPISODE:-$STATE/.watchdog-never-busy-episode}"
# agent-supervisor#163: the never-busy check ITSELF failing to run is a
# different fact from it running and finding a stuck lane -- #163 measured
# nine straight ticks of "NEVER-BUSY-CHECK FAILED: could not parse lanes.sh
# --json output" in watchdog.log with nobody paged, because a failed check
# only ever logged and wrote never_busy_note; nothing counted consecutive
# failures or escalated them. The detection this guards (#112's four-hour
# outage) is exactly the kind of thing that goes quiet for the same reason
# twice, so a safety check that cannot run is itself an alarm condition, the
# same posture poller-recover.sh's own fail streak already takes below. A
# plain file, not a counter kept only in memory: this function returns on
# EVERY exit path (report() cannot see it), so nothing here survives across
# ticks except what is written to disk.
NEVER_BUSY_CHECK_FAIL_STREAK="${SUPERVISOR_NEVER_BUSY_CHECK_FAIL_STREAK:-$STATE/.watchdog-never-busy-check-fail-streak}"
# Escalate every Nth consecutive failure, not only the Nth: a check still
# broken 20 ticks later must page again, not fall silent after the one page
# at streak 3 -- the same noisy-vs-silent tradeoff #112's own lane dedup
# makes in the other direction (page once per distinct STUCK SET, not once
# per tick). Modulo, not equality, is what keeps paging without re-paging
# every single tick once broken.
NEVER_BUSY_CHECK_FAIL_ESCALATE_AFTER="${SUPERVISOR_NEVER_BUSY_CHECK_FAIL_ESCALATE_AFTER:-3}"
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

# --- as#151: director-inbox staleness, from OUTSIDE the loop ----------------
# `director-route.sh` already pages Jon when a queued message crosses
# DIRECTOR_INBOX_STALE_SECONDS (as#34/#42), but that check only runs inside
# `inbox-poll.sh`'s own flush loop -- once per Telegram long-poll iteration,
# while that loop is alive and actually reaching it. Measured on this issue:
# `notify.log`'s entire history has no record of that page ever firing, and
# if the poller itself is down nothing calls `--flush` at all, so the queue
# can go stale with nothing watching. This is the same external-observer fix
# #163 already applied to the poller's own heartbeat, aimed at a different
# fact: `director-inbox.sh stats`' `oldest_age_s`, read from this watchdog's
# own unattended tick, independent of the pane, the poller, or the loop.
# Same threshold variable name director-route.sh already uses, so a deployer
# setting one sets both.
DIRECTOR_INBOX_BIN="${SUPERVISOR_DIRECTOR_INBOX_BIN:-$HERE/director-inbox.sh}"
DIRECTOR_INBOX_STALE_SECONDS="${DIRECTOR_INBOX_STALE_SECONDS:-1800}"
DIRECTOR_INBOX_EPISODE="${SUPERVISOR_DIRECTOR_INBOX_EPISODE:-$STATE/.watchdog-director-inbox-episode.json}"

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

# agent-supervisor#155: a sibling throttle for the lane-completion sweep.
# Unlike the source-task sweep this costs no external API call -- `lanes.sh
# --json` is a handful of local tmux reads -- so it runs far more often; the
# floor on how SOON a stranded lane can be reclaimed is this interval plus
# LANE_SWEEP_IDLE_AFTER (below), not an hour.
LANE_SWEEP_STAMP="${SUPERVISOR_LANE_SWEEP_STAMP:-$STATE/.lane-completion-sweep-last}"
LANE_SWEEP_INTERVAL="${SUPERVISOR_LANE_SWEEP_INTERVAL:-120}"
# Matches digest.sh's DIGEST_RECONCILE_IDLE_AFTER default (300s) -- the two
# already agree on how long a `free` pane must hold before a still-`delivered`
# task is trusted to mean "finished, never signalled" rather than "between
# tool calls" (lane-done.sh's own header names this exact danger, #102).
LANE_SWEEP_IDLE_AFTER="${SUPERVISOR_LANE_SWEEP_IDLE_AFTER:-300}"
# agent-supervisor#374: a claude-print/pi-rpc lane has no pane at all for
# LANE_SWEEP_IDLE_AFTER to observe -- see reconcile_lane_completions.py's
# DEFAULT_STALE_AFTER_SECONDS comment. Separate, longer default: this gates
# an ABSENCE of any signal, not a positive "pane read free" observation.
LANE_SWEEP_STALE_AFTER="${SUPERVISOR_LANE_SWEEP_STALE_AFTER:-3600}"

# agent-supervisor#199/#205: worktree-guard-audit.sh (this repo) existed with
# nothing calling it -- the PR that shipped it was blocked on exactly that:
# "a tool that fails closed and that nothing calls is a documentation rule
# with a binary attached" (CLAUDE.md). CI was rejected as the home because
# the leak this guards against is a LIVE-WORKTREE phenomenon on this machine,
# not a repo-state one -- a stale worktree that CI never sees is precisely
# what leaked in #199. This watchdog already runs unattended against the
# live estate every ${TICK_INTERVAL}s outside the loop (this file's own
# header), the same reason #112's never-busy check and #133's source-task
# sweep both live here rather than as bespoke cron entries -- one unattended
# entry point with PATH/credential/staleness handling already solved, not a
# second one built from scratch. Read-only, git-plumbing-only (see that
# script's own header) -- safe to run from here.
#
# Throttled like the other sweeps above: 2669 file@worktree pairs measured
# against the live estate (#205's PR body) is cheap but not free, and a gap
# does not appear or vanish between one 180s tick and the next -- a worktree
# only ADVANCES past the guard's commit when something fetches and rebases
# it, which happens on the order of dispatches, not seconds. Default matches
# neither sweep exactly: cheaper than the hourly GitHub-bound source sweep
# (no external API call here), but coarser than the 120s lane sweep (this one
# walks every worktree's git objects, the lane sweep reads a handful of tmux
# panes).
GUARD_AUDIT_STAMP="${SUPERVISOR_GUARD_AUDIT_STAMP:-$STATE/.worktree-guard-audit-last}"
GUARD_AUDIT_INTERVAL="${SUPERVISOR_GUARD_AUDIT_INTERVAL:-1800}"
# Dedup key for paging: the actual set of GAP lines the audit reported, not a
# boolean -- same discipline #112's NEVER_BUSY_EPISODE uses, so a DIFFERENT
# gap appearing while an earlier one is still open still pages, and the same
# unchanged gap does not re-page every 30 minutes once a human has been told.
GUARD_AUDIT_EPISODE="${SUPERVISOR_GUARD_AUDIT_EPISODE:-$STATE/.watchdog-guard-audit-episode}"
# The check ITSELF failing to run (script missing/not executable, git
# unreadable) is a different fact from it running and finding gaps -- #163's
# lesson, applied here the same way #112's NEVER_BUSY_CHECK_FAIL_STREAK
# already applies it: a safety check that cannot run is itself an alarm
# condition, escalated every Nth consecutive failure so it cannot go quiet
# forever after one page.
GUARD_AUDIT_FAIL_STREAK="${SUPERVISOR_GUARD_AUDIT_FAIL_STREAK:-$STATE/.watchdog-guard-audit-fail-streak}"
GUARD_AUDIT_FAIL_ESCALATE_AFTER="${SUPERVISOR_GUARD_AUDIT_FAIL_ESCALATE_AFTER:-3}"
# agent-supervisor#205 review (skills:2): the call into worktree-guard-audit.sh
# below had no bound at all -- #267's shape, and the exact thing f810a64/#276
# was written to catch a day before this PR's CI ran. worktree-guard-audit.sh
# itself now bounds each individual `git show`, but this is the outer,
# whole-invocation backstop: it must never depend on that inner bound being
# bug-free, because this call runs on EVERY exit path, ahead of
# advance_on_exit (this file's own comment on check_worktree_guard_audit
# below), and a hang here would silently wedge the one thing that must not
# depend on which check fired this tick. 120s default: ~58s measured for the
# real worktree farm (#205's PR body) plus headroom, well under
# GUARD_AUDIT_INTERVAL's 1800s cadence.
GUARD_AUDIT_TIMEOUT="${SUPERVISOR_GUARD_AUDIT_TIMEOUT:-120}"

# agent-supervisor#526: `worktree.sh gc` (agent-dotfiles#165/#169,
# agent-supervisor#478) is a tool that fails closed and that nothing calls --
# the exact shape that file's own header warns about, and worktree.sh says so
# explicitly: "deliberately NOT wired into the dispatch/lane-done pair or the
# Director tick... a separate decision for whoever owns the Director tick".
# 64 worktrees measured accumulated across the estate's repos with nothing
# ever sweeping them. This is that separate decision, made the same way
# #199/#205 wired worktree-guard-audit.sh in above: this watchdog already
# runs unattended every ${TICK_INTERVAL}s outside the loop, so it is the one
# entry point that survives the failure mode (a dead or wedged loop) that lets
# stale worktrees accumulate the longest, costs zero model tokens, and reuses
# the PATH/credential/staleness handling already solved here rather than a
# second bespoke cron entry.
#
# DRY-RUN BY DEFAULT, on purpose, unlike the source/lane sweeps above. `gc`
# actually deletes a worktree once both its content-containment check and its
# three liveness checks pass (see worktree.sh's own header) -- and this same
# class of automatic sweep already destroyed ~20 minutes of live work once
# (agent-supervisor#478, PR #489), before those liveness checks existed. They
# exist now and this PR's own test fixtures prove the content check itself is
# sound (see test_watchdog_worktree_gc_sweep.sh), but running unattended and
# destructively across the whole estate on day one, with no human having
# watched what it would actually decide, is a second decision this PR does
# NOT make. SUPERVISOR_GC_LIVE=1 flips it from reporting what it would remove
# to actually removing -- a deliberate, later, reviewable flip once the
# dry-run verdicts have been read, not a default this change ships with.
GC_SWEEP_STAMP="${SUPERVISOR_GC_STAMP:-$STATE/.worktree-gc-sweep-last}"
# 3600s: sweeping more often buys nothing -- `gc`'s own liveness floor
# (GC_MIN_AGE_SECONDS in worktree.sh, also 3600s by default) means a worktree
# cannot newly qualify faster than once an hour regardless of tick cadence.
GC_SWEEP_INTERVAL="${SUPERVISOR_GC_INTERVAL:-3600}"
GC_SWEEP_BASE="${SUPERVISOR_GC_BASE:-origin/main}"
# Reused rather than reinvented: cli.py's own DEFAULT_REPOSITORIES table
# (agent-supervisor#179 §3) is already the estate's one canonical
# name/path/github list, honoring SUPERVISOR_REPOSITORIES itself. A second,
# separately-maintained path list here would be exactly the kind of drift
# CLAUDE.md warns about. SUPERVISOR_GC_REPOS overrides with a colon-separated
# list of raw local paths -- same override shape as SUPERVISOR_GUARD_AUDIT_REPO
# above, plural because this sweep is meant to reach every repo in the farm,
# not one. This is how a test points the real check_worktree_gc_sweep at a
# disposable throwaway repo instead of the real estate.
GC_SWEEP_LIVE="${SUPERVISOR_GC_LIVE:-}"

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

# agent-supervisor#704: split out of this file for length -- log/report and
# the atomic watchdog.status note-writer helpers.
# shellcheck source=./watchdog-status.sh
. "$HERE/watchdog-status.sh"
# The on-exit checks (heartbeats, sweeps, never-busy, guard-audit, gc).
# shellcheck source=./watchdog-checks.sh
. "$HERE/watchdog-checks.sh"
# advance_on_exit/on_exit and the EXIT trap itself (armed by this source).
# shellcheck source=./watchdog-advance.sh
. "$HERE/watchdog-advance.sh"

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
#
# agent-supervisor#144: REST core (`gh api repos/.../issues`,
# `.../pulls`), not GraphQL (`gh issue list`/`gh pr list`) -- this loop runs
# on every watchdog tick, for every repo. REST's `/issues` endpoint answers
# for PRs too (they carry a `pull_request` key), filtered out here to match
# `gh issue list`'s issues-only count.
work=0; degraded=""
for r in "${REPOS[@]}"; do
  if n=$(gh api "repos/jonhill90/$r/issues?state=open&per_page=100" \
           --jq '[.[]|select(has("pull_request")|not)|select([.labels[].name]|any(.=="parked" or .=="question")|not)]|length' 2>/dev/null) \
     && [[ "$n" =~ ^[0-9]+$ ]]; then
    work=$((work + n))
  else
    degraded="${degraded}${r} "
  fi
  if p=$(gh api "repos/jonhill90/$r/pulls?state=open&per_page=100" --jq 'length' 2>/dev/null) \
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
# input line, so "the line looks non-empty" proves nothing, and a plain
# `capture-pane -p` capture cannot tell the two apart -- the discriminating
# information (SGR dim around the placeholder) lives in the attributes, which
# `-p` throws away (agent-supervisor#365). `input_box_state` (input-box.sh,
# sourced via send.sh above) reads `-pe` and answers `text` / `empty` /
# `unknown` from that attribute alone, with no need to touch the pane.
box_state=$(tmux capture-pane -pe -t "$PANE" 2>/dev/null | input_box_state)
if [ "$box_state" = text ]; then
  report human_typing "un-submitted text in the pane — left alone"
  log "real un-submitted text in pane — not touching it"
  exit 0
fi

# `unknown` -- no box could be identified on screen -- is the one case a
# passive read genuinely cannot resolve. Fall back to the active probe: append
# a character and see whether it lands on top of existing text or replaces
# the ghost:
#
#   real text  ->  "❯ do the thing"  becomes  "❯ do the thingX"
#   ghost text ->  "❯ do the thing"  becomes  "❯ X"
#
# Real iff the new line is the old line with X appended. The first version
# compared the wrong direction and called every ghost line real, which would
# have meant the watchdog never restarted anything.
if [ "$box_state" = unknown ]; then
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

# agent-supervisor#178: centralised via send.sh's blind_send, which
# preserves this exact sequence (C-u, settle, literal type, settle, Enter)
# byte for byte -- see that function's own header for why this is NOT
# upgraded to verified_type/verified_submit here. This pane's stub models
# busy/idle chrome, not an editable input buffer, so there is nothing yet
# for a landed/submitted check to read; closing that gap for real is
# follow-up work (growing the watchdog stub an input-buffer model, the way
# tmux-dispatch already has one), not a silent claim of verification this
# call cannot back.
blind_send "$PANE" \
  "/loop Supervisor tick. Follow $TICK exactly. Dispatch to idle worker lanes rather than implementing yourself. Never call stop, always re-arm." \
  --preclear-settle 1 --type-settle 2 --literal

echo "$now" >"$STAMP"
echo "$now" >>"$HISTORY"
recent=$((recent + 1))
report restarted "was idle with ${work} item(s)${degraded:+ (GitHub degraded: ${degraded% })}"
log "RESTARTED loop — idle with ${work} actionable item(s)${degraded:+; DEGRADED: ${degraded% }}"
