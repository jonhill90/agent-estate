#!/bin/bash
# Restarts the architecture supervisor loop when it has died with work left.
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
# A fixed PATH so a LaunchAgent (which inherits almost nothing) finds tmux,
# gh and python3. Overridable ONLY so tests can inject stub binaries -- three
# separate bugs shipped in this file because a hardcoded PATH made it
# impossible to test without a live tmux server and a live GitHub.
PATH="${SUPERVISOR_PATH:-/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:$HOME/.local/bin}"
export PATH

# Overridable so the script is testable and so a second lane can
# reuse it. Hardcoding the target was raised in review as both a
# portability and an untestability problem.
PANE="${SUPERVISOR_PANE:-agent-dotfiles:1.1}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
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
read -r -a REPOS <<<"${SUPERVISOR_REPOS:-agent-dotfiles skills skills-private agent-evals}"

COOLDOWN=600        # no more than one restart per 10 minutes
MAX_RESTARTS=3      # ...and no more than this many
ESCALATE_WINDOW=3600 # ...within this window, or stop and escalate

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

# How far behind main this copy is. The LaunchAgent runs the watchdog from a
# PINNED detached worktree that nothing in this repository updates (#99), so a
# merged fix to watchdog.sh, sleepcheck.py, watchdog_notify.py or loop-tick.md
# can sit green on main indefinitely while the live copy keeps running the old
# one. `code: detached @ 9cddafb` reads exactly as healthy as a current sha
# unless the reader already knows what main is.
#
# Reported, never acted on: code that guards the loop must not deploy itself.
# A broken watchdog would reinstall itself every 180s and nothing would be left
# to notice.
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
    printf 'code:     %s @ %s%s\n' "$branch" "$sha" "$code_note"
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

trap 'rm -f "$STATUS.$$" 2>/dev/null' EXIT

# How many restarts inside the escalation window?
recent=0
if [ -f "$HISTORY" ]; then
  recent=$(awk -v now="$now" -v w="$ESCALATE_WINDOW" '$1 > now - w' "$HISTORY" 2>/dev/null | wc -l | tr -d ' ')
  awk -v now="$now" -v w="$ESCALATE_WINDOW" '$1 > now - w' "$HISTORY" >"$HISTORY.tmp" 2>/dev/null \
    && mv -f "$HISTORY.tmp" "$HISTORY"
fi

if ! tmux has-session -t agent-dotfiles 2>/dev/null; then
  report no_session "tmux session 'agent-dotfiles' does not exist"
  log "no agent-dotfiles session"; exit 0
fi

pane=$(tmux capture-pane -p -t "$PANE" -S -6 2>/dev/null) || {
  report pane_unreadable "cannot capture $PANE"
  log "pane $PANE unreadable"; exit 0
}

# Busy. The harness prints this only while a turn is running. This is a
# Claude Code string — see the portability note in the repo copy; a lane
# running another harness needs its own probe.
if grep -q 'esc to interrupt' <<<"$pane"; then
  report working "supervisor turn in progress"
  exit 0
fi

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
if tmux capture-pane -p -t "$PANE" -S -6 2>/dev/null | grep -q 'esc to interrupt'; then
  report working "became busy during the pre-send probe; not sending a /loop into a busy pane"
  log "SKIPPED restart — pane became busy mid-probe (a queued /loop is inert)"
  exit 0
fi

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
