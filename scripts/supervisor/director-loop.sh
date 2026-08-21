#!/usr/bin/env bash
# Drive the Director on a cadence. agent-supervisor#321.
#
# WHY THIS EXISTS. The watchdog's own prompt has said for weeks that the
# Director "runs in tmux director:1, self-driving on a 15-min loop". Checked
# on 2026-08-17 against reality: there is NO crontab entry, NO launchd job,
# and NO self-scheduled wakeup anywhere in its pane. It has never been
# self-driving. Every action it took in an eighteen-hour period was triggered
# by an external nudge -- the hourly watchdog tick, or heartbeat.sh's stall
# detector -- so the shape was: nudge, do exactly one thing, stop, silence.
# That is why "nothing is moving" was the recurring question.
#
# The description was intent, and it was read as fact for eighteen hours. It
# is the same failure this estate keeps hitting: a claim nobody measured.
#
# DIVISION OF LABOUR WITH heartbeat.sh -- read this before changing either.
# Two things poking one pane is a real hazard (a stranded prompt once sat in
# the input box of the lane that AUTHORED the PR it was told to merge), so
# the two have different jobs and different trigger conditions:
#
#   director-loop.sh  NORMAL CADENCE. Fires every LOOP_INTERVAL. Sends a tick
#                     ONLY when the pane is idle. This is what should keep the
#                     estate moving, and if it works the heartbeat never fires.
#   heartbeat.sh      BACKSTOP. Fires only on a real stall -- no ledger write
#                     for 1800s AND nothing in flight -- at most once an hour.
#
# The cadence (900s) is deliberately shorter than the stall threshold (1800s):
# in a healthy estate the loop always gets there first, and a heartbeat nudge
# means the LOOP failed, which is itself a signal worth having.
#
# NEVER SENDS TO A BUSY PANE. Text sent mid-turn sits in the input box until
# the turn ends -- that is the stranded-prompt defect, and a loop firing every
# 15 minutes would manufacture it at scale.
#
# launchd IS the loop. `--once` does one pass and exits, so a wedged tmux
# cannot leave a long-lived process that pgrep still calls alive.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
TARGET="${DIRECTOR_LOOP_TARGET:-director:@3}"
QUOTA_GATE="${QUOTA_GATE:-$HERE/quota.sh}"
# The interactive codexbar call measures ~12.5s and the gate's own default
# per-sample timeout is 15s, so samples trip over it intermittently and a
# SLOW instrument gets reported as a BROKEN one -- which halted the estate for
# two hours on 2026-08-17. Give it room; a real answer beats a fast UNKNOWN.
export QUOTA_GUARD_TIMEOUT_SECONDS="${QUOTA_GUARD_TIMEOUT_SECONDS:-45}"
export QUOTA_USAGE_TIMEOUT_SECONDS="${QUOTA_USAGE_TIMEOUT_SECONDS:-45}"

log() { printf '%s director-loop: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*"; }

# Overridable so tests can point this at a recording stub instead of the
# real notify.sh, same convention as quota-watch.sh's QUOTA_WATCH_NOTIFY_SCRIPT
# (agent-supervisor#273).
NOTIFY_SCRIPT="${DIRECTOR_LOOP_NOTIFY_SCRIPT:-$HERE/notify.sh}"
# UNLIKE heartbeat.sh, this script ticks every LOOP_INTERVAL (900s) with no
# built-in cooldown of its own -- a stall that persists across ticks would
# otherwise page every 15 minutes, which is exactly the "a repeated alarm
# becomes a log, and a log becomes ignored" failure #273 exists to prevent.
# ALARM_STAMP records the last time ANY alarm fired from this script;
# ALARM_COOLDOWN (default one hour, matching heartbeat.sh's NUDGE_COOLDOWN)
# suppresses a repeat page for the same standing incident while still
# paging again once real time has passed.
ALARM_STAMP="$STATE/.director-loop-alarm.state"
ALARM_COOLDOWN="${DIRECTOR_LOOP_ALARM_COOLDOWN:-3600}"

# agent-supervisor#466: a fail-closed refusal that only ever writes a log
# line is indistinguishable from a healthy loop to anything that does not
# grep the log -- that is how a correct refusal produced 17 hours of silence.
# STALE_STREAK persists the count of CONSECUTIVE ticks that have refused to
# resolve the target window, across process invocations (this script runs
# once and exits every tick, so nothing in memory survives between ticks).
# It resets the moment resolution succeeds -- this counts a standing
# incident, not a lifetime total.
#
# STALE_ESCALATE_AFTER=3: at the 900s cadence that is ~45 minutes. One tick
# is not enough -- a tmux restart resolved by name on the very next tick is
# the expected, self-healing case and should not page. Three consecutive
# refusals means the SAME ambiguity survived three independent samples, so
# it is a standing incident, not a blip; 45 minutes of silence is a
# containable regression compared to the 17 hours this issue measured.
STALE_STREAK_FILE="$STATE/.director-loop-stale-streak.state"
STALE_ESCALATE_AFTER="${DIRECTOR_LOOP_STALE_ESCALATE_AFTER:-3}"

read_stale_streak() {
  local n=0
  [ -f "$STALE_STREAK_FILE" ] && n=$(cat "$STALE_STREAK_FILE" 2>/dev/null)
  case "$n" in ''|*[!0-9]*) n=0 ;; esac
  printf '%s' "$n"
}

reset_stale_streak() {
  printf '0' > "$STALE_STREAK_FILE" 2>/dev/null || true
}

bump_stale_streak() {
  local n
  n=$(($(read_stale_streak) + 1))
  printf '%s' "$n" > "$STALE_STREAK_FILE" 2>/dev/null || true
  printf '%s' "$n"
}

# agent-supervisor#474: the other half of #466's fix. The quota gate below
# was already fail-closed (correct -- do not weaken it), but a refusal only
# ever wrote one `log` line while `"$QUOTA_GATE" check`'s own diagnostic
# output (which of the 3 samples timed out vs answered "unknown", how many
# seconds, a stale-cache age) went to /dev/null. That is the exact #273/#466
# shape: a correct refusal that is indistinguishable from a healthy tick to
# anything not grepping this log by hand -- measured live as the Director's
# loop going quiet for ~19 hours while its log filled with
# "quota UNKNOWN (rc=2) -- never treated as safe, not ticking" and nothing
# else, which was first misread as a mistuned guard rather than a genuine
# instrument failure worth paging on (issue #474). WIND DOWN (qrc=1) is
# deliberately NOT escalated here -- it is the gate doing its job, the same
# reason resolve_director_target's stale streak only tracks refusals, not
# every non-proceeding outcome. QUOTA_STREAK_FILE is independent of
# STALE_STREAK_FILE so an ambiguous-window incident and a quota-instrument
# incident escalate on their own timelines rather than one silently
# resetting the other's count.
QUOTA_STREAK_FILE="$STATE/.director-loop-quota-streak.state"
QUOTA_ESCALATE_AFTER="${DIRECTOR_LOOP_QUOTA_ESCALATE_AFTER:-3}"

read_quota_streak() {
  local n=0
  [ -f "$QUOTA_STREAK_FILE" ] && n=$(cat "$QUOTA_STREAK_FILE" 2>/dev/null)
  case "$n" in ''|*[!0-9]*) n=0 ;; esac
  printf '%s' "$n"
}

reset_quota_streak() {
  printf '0' > "$QUOTA_STREAK_FILE" 2>/dev/null || true
}

bump_quota_streak() {
  local n
  n=$(($(read_quota_streak) + 1))
  printf '%s' "$n" > "$QUOTA_STREAK_FILE" 2>/dev/null || true
  printf '%s' "$n"
}

# agent-supervisor#470: the remaining half of #466's pattern, applied to the
# other silent-skip gate #470 named -- not the quota gate (#474/#476 already
# fixed that half), but advance-live.sh's recovery attempt below. Its own
# `git fetch` has a hard ADVANCE_FETCH_TIMEOUT_SECONDS bound (20s default) and
# can time out under load exactly like a slow codexbar sample can; before this,
# a failed recovery attempt paged on the very FIRST occurrence, which meant a
# single flaky fetch escalated immediately -- as noisy in the other direction
# as the quota gate's old silence was. Same streak-and-cooldown shape as
# QUOTA_STREAK_FILE, kept in its own file so a standing live/-recovery
# incident and a standing quota-instrument incident escalate on independent
# timelines rather than one resetting the other's count.
ADVANCE_STREAK_FILE="$STATE/.director-loop-advance-streak.state"
ADVANCE_ESCALATE_AFTER="${DIRECTOR_LOOP_ADVANCE_ESCALATE_AFTER:-3}"

read_advance_streak() {
  local n=0
  [ -f "$ADVANCE_STREAK_FILE" ] && n=$(cat "$ADVANCE_STREAK_FILE" 2>/dev/null)
  case "$n" in ''|*[!0-9]*) n=0 ;; esac
  printf '%s' "$n"
}

reset_advance_streak() {
  printf '0' > "$ADVANCE_STREAK_FILE" 2>/dev/null || true
}

bump_advance_streak() {
  local n
  n=$(($(read_advance_streak) + 1))
  printf '%s' "$n" > "$ADVANCE_STREAK_FILE" 2>/dev/null || true
  printf '%s' "$n"
}

# send_takeover_alarm SUBJECT BODY -- page a human when this script hits a
# state it cannot recover from itself (agent-supervisor#273: a correct "a
# human should look" refusal used to go only to a logfile, which produced
# the same outward silence as no check at all). Reuses notify.sh, the
# estate's one human-notification path, rather than inventing a second one.
# Exits 0 only when a channel actually accepted the page AND the cooldown
# stamp was updated -- a suppressed page (still inside cooldown) is not an
# error, so callers log the outcome rather than branching on this return.
send_takeover_alarm() {
  local subject="$1" body="$2"
  local now_ts last=0
  now_ts=$(date +%s)
  [ -f "$ALARM_STAMP" ] && last=$(cat "$ALARM_STAMP" 2>/dev/null)
  case "$last" in ''|*[!0-9]*) last=0 ;; esac
  if [ $(( now_ts - last )) -lt "$ALARM_COOLDOWN" ]; then
    log "alarm suppressed (cooldown, last sent $(( now_ts - last ))s ago): $subject"
    return 0
  fi
  if AGENT_NOTIFY_CALLER=director bash "$NOTIFY_SCRIPT" "$subject" "$body"; then
    printf '%s' "$now_ts" > "$ALARM_STAMP"
    log "escalation sent: $subject"
  else
    log "escalation did NOT send -- still unverified, see notify.log: $subject"
  fi
}

# --- quota gate. Fail closed on anything that is not a definite SAFE.
# agent-supervisor#474: capture the gate's own diagnostic output instead of
# discarding it -- it names which samples timed out vs answered "unknown"
# and how many seconds, the exact detail a human needs to tell a genuine
# instrument failure from a real WIND DOWN without re-running the gate by
# hand.
quota_out=$("$QUOTA_GATE" check 2>&1)
qrc=$?
case "$qrc" in
  0)
    reset_quota_streak
    ;;
  1)
    reset_quota_streak
    log "quota WIND DOWN -- not ticking the Director"
    while IFS= read -r line; do log "  $line"; done <<<"$quota_out"
    exit 0
    ;;
  *)
    qstreak=$(bump_quota_streak)
    log "quota UNKNOWN (rc=$qrc) -- never treated as safe, not ticking (consecutive: $qstreak)"
    while IFS= read -r line; do log "  $line"; done <<<"$quota_out"
    if [ "$qstreak" -ge "$QUOTA_ESCALATE_AFTER" ]; then
      send_takeover_alarm "director-loop: quota gate UNKNOWN, $qstreak consecutive ticks (#474)" \
        "$QUOTA_GATE check has returned rc=$qrc (not SAFE, not WIND DOWN) for $qstreak consecutive ticks (~$(( qstreak * 15 )) minutes) -- the Director has been silently not-ticking. This is an instrument failure, not a real quota stop. Latest sample output:
$quota_out"
    else
      log "not yet escalating (need $QUOTA_ESCALATE_AFTER consecutive UNKNOWN ticks to page, have $qstreak)"
    fi
    exit 0
    ;;
esac

# --- agent-supervisor#367: live/ vanishing must be noticed BY SOMETHING ---
# --- that keeps running when it does, not discovered by a later command's
# --- ENOENT. On 2026-08-19 live/ was deleted -- not merely pruned, its own
# --- `git worktree list` registration was gone too -- and nothing noticed
# --- until claim.sh/lanes.sh/dispatch* each failed independently. The
# --- watchdog LaunchAgent runs `watchdog.sh` FROM live/ itself (README:
# --- "the watchdog LaunchAgent runs watchdog.sh from one pinned worktree"),
# --- so it cannot be the thing that reports live/'s own absence -- a
# --- process cd'd into a directory that no longer exists never starts.
# --- THIS script is the one already on an independent, live/-agnostic
# --- cadence: its own LaunchAgent invokes it from $HERE, the shared
# --- checkout (launchd/com.jonhill.director-loop.plist), every 900s,
# --- regardless of whether live/ exists at all -- exactly the "does the
# --- caller survive the failure it guards against" property this codebase
# --- has learned to check for (CLAUDE.md). loop-tick.md's own step 0 already
# --- tells a human/agent to run advance-live.sh "before anything else"; this
# --- is that same step, code-invoked instead of remembered, using the
# --- self-heal advance-live.sh now has (agent-supervisor#367) rather than a
# --- second detector duplicating its existence check.
LIVE_PATH="${SUPERVISOR_LIVE:-$STATE/live}"
# Registration is checked against SUPERVISOR_REPO (the same shared checkout
# advance-live.sh recreates from), not unconditionally against $HERE: in
# production the two are the same directory (this script's own LaunchAgent
# runs it from the shared checkout), but they need not be everywhere this
# script is invoked from, and hardcoding $HERE here would silently check the
# wrong repo's `git worktree list` the moment that assumption did not hold.
REPO_FOR_CHECK="${SUPERVISOR_REPO:-$HERE}"
if [ ! -d "$LIVE_PATH" ] || ! git -C "$LIVE_PATH" rev-parse --git-dir >/dev/null 2>&1 \
   || { live_real=$(cd "$LIVE_PATH" 2>/dev/null && pwd -P); \
        [ -n "$live_real" ] && ! git -C "$REPO_FOR_CHECK" worktree list --porcelain 2>/dev/null | grep -qx "worktree $live_real"; }; then
  log "LIVE MISSING -- $LIVE_PATH is absent, not a git worktree, or unregistered in 'git worktree list'; attempting recovery via advance-live.sh"
  if [ -x "$HERE/advance-live.sh" ]; then
    recover_out=$("$HERE/advance-live.sh" "$LIVE_PATH" 2>&1)
    recover_rc=$?
    log "advance-live.sh recovery attempt: rc=$recover_rc $recover_out"
    if [ "$recover_rc" -ne 0 ] || [ ! -d "$LIVE_PATH" ] || ! git -C "$LIVE_PATH" rev-parse --git-dir >/dev/null 2>&1; then
      astreak=$(bump_advance_streak)
      log "LIVE STILL MISSING after recovery attempt -- a human should look (consecutive: $astreak)"
      if [ "$astreak" -ge "$ADVANCE_ESCALATE_AFTER" ]; then
        send_takeover_alarm "director-loop: live/ MISSING and self-heal failed, $astreak consecutive ticks (#470)" \
          "advance-live.sh ran but $LIVE_PATH is still absent, not a git worktree, or unregistered for $astreak consecutive ticks (~$(( astreak * 15 )) minutes) -- the Director cannot tick without it. Latest recovery output: $recover_out"
      else
        log "not yet escalating (need $ADVANCE_ESCALATE_AFTER consecutive recovery failures to page, have $astreak)"
      fi
      exit 6
    fi
    reset_advance_streak
    log "LIVE RECOVERED at $LIVE_PATH -- continuing this tick"
  else
    log "advance-live.sh not found beside this script ($HERE) -- cannot recover live/ automatically; a human should look"
    send_takeover_alarm "director-loop: live/ MISSING, no advance-live.sh (#273)" \
      "$LIVE_PATH is absent, not a git worktree, or unregistered, and advance-live.sh is not present beside $HERE to self-heal it -- the Director cannot tick. Restore live/ by hand."
    exit 6
  fi
fi

SESSION="${TARGET%%:*}"
if ! tmux has-session -t "$SESSION" 2>/dev/null; then
  log "target session $SESSION does not exist -- a human should look"
  send_takeover_alarm "director-loop: target session missing (#273)" \
    "Target session '$SESSION' does not exist at all -- there is nowhere to tick the Director. Check by hand: tmux ls"
  exit 3
fi

# RESOLVE THE WINDOW, do not trust the id in the default.
#
# tmux window ids (@N) are unique within a server but are NOT stable across a
# server restart. This target defaulted to `director:@3`; the estate's tmux was
# restarted on 2026-08-18 and the Director's window came back as @35. The loop
# then failed to capture @3 on every single tick and logged "not ticking" --
# for nearly two hours, silently, while looking perfectly healthy in
# `launchctl list`. The same restart broke quota-watch (@1) and restore.sh the
# same way. A hardcoded @id is a defect, not a configuration.
#
# So: if the configured window is not there, re-resolve by STABLE IDENTITY --
# the window NAME, never the index (agent-supervisor#466: index addressing
# has misidentified panes here before, and `renumber-windows` is on in this
# estate). bootstrap-session.sh names the supervisor/Director's own window
# `supervisor` (LANES_SUPERVISOR_NAME) -- the same marker quota-watch.sh
# already resolves its own equivalent target by; reused here rather than
# invented a second time. Falls back to "exactly one window in the session"
# only when NO window carries that name at all (e.g. a session that predates
# the naming convention), same last-resort shape as before this fix. Either
# way, more than one plausible candidate must still refuse -- never guess.
resolve_director_target() {
  tmux capture-pane -p -t "=$TARGET" >/dev/null 2>&1 && return 0

  local name="${LANES_SUPERVISOR_NAME:-supervisor}"
  local by_name name_count
  by_name=$(tmux list-windows -t "$SESSION" -F '#{window_id}	#{window_name}' 2>/dev/null \
    | awk -F'\t' -v n="$name" '$2==n{print $1}')
  name_count=$(printf '%s\n' "$by_name" | grep -c .)
  if [ "$name_count" -eq 1 ]; then
    local resolved="$SESSION:$(printf '%s' "$by_name" | tr -d '[:space:]')"
    log "configured target $TARGET is gone (tmux renumbered across a restart); resolved to $resolved by window name '$name'"
    TARGET="$resolved"
    return 0
  fi
  if [ "$name_count" -gt 1 ]; then
    log "configured target $TARGET is gone and $name_count windows in $SESSION are named '$name' -- refusing to guess which is the Director; a human should look"
    return 1
  fi

  # No window is named '$name' at all -- last-resort fallback to the
  # session's only window, same rule this script used before #466.
  local wins wcount
  wins=$(tmux list-windows -t "$SESSION" -F '#{window_id}' 2>/dev/null)
  wcount=$(printf '%s\n' "$wins" | grep -c .)
  if [ "$wcount" -eq 1 ]; then
    local resolved="$SESSION:$(printf '%s' "$wins" | tr -d '[:space:]')"
    log "configured target $TARGET is gone, no window named '$name', but $SESSION has exactly one window; resolved to $resolved"
    TARGET="$resolved"
    return 0
  fi
  log "configured target $TARGET is gone, no window named '$name', and $SESSION has $wcount windows -- refusing to guess which is the Director; a human should look"
  return 1
}

if ! resolve_director_target; then
  streak=$(bump_stale_streak)
  log "consecutive stale-target refusals: $streak"
  if [ "$streak" -ge "$STALE_ESCALATE_AFTER" ]; then
    send_takeover_alarm "director-loop: ambiguous Director window, $streak consecutive refusals (#466)" \
      "Configured target $TARGET is gone and $SESSION's windows are still ambiguous after $streak consecutive ticks (~$(( streak * 15 )) minutes) -- the loop has been silently refusing to guess which one is the Director. Check by hand: tmux list-windows -t $SESSION"
  else
    log "not yet escalating (need $STALE_ESCALATE_AFTER consecutive refusals to page, have $streak)"
  fi
  exit 3
fi
reset_stale_streak

# Busy check is the LAST NON-BLANK LINE only, never a scrollback sweep. A
# whole-pane grep matches the phrase `esc to interrupt` wherever it appears --
# including inside a previous nudge's own text, which is exactly how
# heartbeat.sh silently disabled itself for four readings on 2026-08-17.
pane=$(tmux capture-pane -p -t "=$TARGET" 2>/dev/null | grep -v '^[[:space:]]*$')
if [ -z "$pane" ]; then
  log "could not capture $TARGET -- not ticking"
  exit 3
fi
if tail -1 <<<"$pane" | grep -q 'esc to interrupt'; then
  log "Director is working -- not interrupting"
  exit 0
fi

# A non-empty input box means a previous send is sitting unsubmitted. Do NOT
# add to it: retyping over stranded text is how a half-message gets sent.
box=$(tail -1 <<<"$pane")
case "$box" in
  *'❯'*)
    if [ "$(printf '%s' "$box" | sed 's/.*❯ *//' | tr -d '[:space:]')" != "" ]; then
      log "input box is NOT empty -- refusing to type over a stranded prompt; a human should look"
      send_takeover_alarm "director-loop: stranded prompt in input box (#273)" \
        "The Director's input box at $TARGET is not empty -- a previous send is sitting unsubmitted. Retyping over it risks sending a half-message. Check by hand: tmux attach -t ${TARGET%%:*}"
      exit 4
    fi
    ;;
esac

TICK='Director tick. SHORT MEANS SHORT PROSE, NOT FEW ACTIONS. Take as many actions as the work needs; write almost nothing about them.

The previous wording said "act, report one line, stop" twice and "fill the free lanes" once, so every tick resolved toward stopping. Measured 2026-08-19: 29 free lanes, 124 open issues, 49 percent of a quota window in reserve, and TWO dispatches in an hour. The estate was single-threaded by instruction, not by any cap.

1. QUOTA GATE FIRST, with the timeouts the instrument actually needs:
   QUOTA_GUARD_TIMEOUT_SECONDS=45 QUOTA_USAGE_TIMEOUT_SECONDS=45 bash scripts/supervisor/quota.sh check
   0 proceed, 1 wind down and go quiet, 2/3 UNKNOWN -- never treat UNKNOWN as safe.

2. Read the standing brief rather than re-deriving it: ~/.local/state/agent-dotfiles-supervisor/PHASES.md and NOTEBOOK-jon-directives.md.

3. DISPATCH UNTIL YOU RUN OUT OF WORK OR LANES. Not one. Not two. Count the free lanes, count the dispatchable issues, and fire until one of them hits zero. Ending a tick with idle lanes and open work is the failure this instruction exists to prevent.

   Run dispatches in PARALLEL, backgrounded -- do not serialise them. Each pays a quota gate and REST reads; serialising ten costs ten times what firing ten at once does, and that cost is exactly why previous ticks stopped at two.

   Gate quota ONCE at the top of the turn, not once per dispatch.

   Use subagents for verification you would otherwise do in sequence. Three lanes needing CI checked is one turn with three subagents, not three turns.

FILL THE FREE LANES. If lanes are idle and there is independent work, dispatch to all of them in the same turn. Reviews of different PRs touch different files and do not block each other; there is no coded concurrency cap and none is wanted. An idle lane is waste (lane_default=keep_working_not_idle).

The old instruction here said \"one PR per turn, do not fan out -- the WEEKLY quota is the binding constraint\". That was written when weekly was at 94% and it is now the throttle rather than the protection: the premise expired when the window reset and the sentence outlived it. Judge from the CURRENT reading, never from a remembered one.

Serialise only where there is a REAL dependency. Right now #301, #300 and #205 genuinely queue behind #235, because until lane identity resolves to a stable id the author guard excludes every lane and is right to.

Priority order, unless GitHub state says otherwise -- re-derive from live state, do not trust this list blindly:
  #320 (non-blocking dispatch -- fixes the 900s defect that has been destroying lanes all day)
  #316 (lane classifier -- without it every idle lane reads unknown and capacity silently hits ZERO)
  #317, #308, then 251, 205.

Standing traps, all measured, all cost real time today:
  - A merged fix is NOT a deployed fix. Check live/ (#310).
  - A dispatch timeout may be hidden SUCCESS -- check the ledger and GitHub, never the exit code (#278).
  - gh pr review --approve CANNOT work here; GitHub rejects it because every lane authenticates as jonhill90. Post evidence as a comment and merge.
  - Run checks synchronously. A lane that backgrounds its work gets recorded complete having delivered nothing.

BEFORE concluding "nothing", you must have checked ALL of: unclaimed issues in every repo (agent-supervisor, agent-tui, skills, agent-dotfiles), open PRs needing review, and `cli.py prompts unacknowledged` for work Jon asked for that nobody picked up. "Nothing" is valid ONLY with that list checked and named.

Twice on 2026-08-19 a "no surface" conclusion was wrong on inspection: agent-tui#52 was open the whole time, and a poller leak was actionable for three ticks before anyone filed it.

Report as a TABLE -- what you dispatched, to which lane, what you concluded. No narration of reasoning.'

tmux send-keys -t "=$TARGET" C-u 2>/dev/null
sleep 1
tmux send-keys -t "=$TARGET" -l "$TICK" 2>/dev/null
sleep 2
tmux send-keys -t "=$TARGET" Enter 2>/dev/null

# Verify it ARRIVED, not merely that it was sent. "Delivered" is a claim about
# someone else's state; if you did not ask them, do not say it.
#
# POLL, do not sleep-once. This was `sleep 6` followed by a single capture, and
# it produced a FALSE NEGATIVE on 2026-08-20: the Director had genuinely
# accepted the tick and was mid-`quota.sh check`, but had taken ~11s to show
# `esc to interrupt`, so the loop logged "the pane did NOT start working -- a
# human should look" over a pane that was working fine.
#
# That failure mode is worse than it sounds. The escalation path exists for a
# Director that is genuinely wedged; firing it on a slow start trains the
# reader to discount it, and this estate has already measured what happens when
# a real page is indistinguishable from a routine one. A model that stops to
# think before its first tool call is normal, not a fault.
#
# So: same total budget as before at minimum, but spent watching rather than
# waiting. Returns the moment the pane is observably working.
TICK_ARRIVE_TIMEOUT="${DIRECTOR_TICK_ARRIVE_TIMEOUT:-30}"
tick_arrived=0
waited=0
while [ "$waited" -lt "$TICK_ARRIVE_TIMEOUT" ]; do
  sleep 2
  waited=$((waited + 2))
  if tmux capture-pane -p -t "=$TARGET" 2>/dev/null | grep -q 'esc to interrupt'; then
    tick_arrived=1
    log "tick arrived after ${waited}s"
    break
  fi
done

if [ "$tick_arrived" -eq 1 ]; then
  # CONTEST A STOP-CONCLUSION, mechanically, without any agent choosing to.
  #
  # Jon, 2026-08-19: "I should not be the one pointing this out ... we need sanity
  # checks outside of this." Every failure he caught that day was a conclusion
  # that STOPPED work, reached alone and contested by nobody.
  # agent-supervisor#150 filed the same complaint on 2026-08-15 and the council
  # skills were never wired to anything.
  #
  # This fires when the previous tick concluded "nothing" -- rate-limited to once
  # per ~50 minutes by contest-stop.sh itself, and cheap because stop-conclusions
  # are rare. A wrong "keep working" costs one lane's turn; a wrong "stop" costs
  # the whole window and looks identical to a finished estate.
  if grep -qiE 'concluding "?nothing|nothing to dispatch|no .*(surface|work) (left|remains|available)' \
       <<<"$(tail -40 <<<"$pane")" 2>/dev/null; then
    claim=$(grep -iE 'concluding "?nothing|nothing to dispatch|no .*(surface|work)' <<<"$pane" | tail -1)
    "$HERE/contest-stop.sh" --claim "${claim:-the previous tick concluded there was nothing to do}" \
      --evidence "Read from the Director's own pane at $(date -u '+%Y-%m-%dT%H:%M:%SZ')." \
      >>"$STATE/contest-stop.log" 2>&1 &
  fi

  log "ticked $TARGET -- pane is now working"
  exit 0
fi
log "ticked $TARGET but the pane did NOT start working within ${TICK_ARRIVE_TIMEOUT}s -- a human should look"
send_takeover_alarm "director-loop: tick did NOT take (#273)" \
  "Ticked $TARGET but the pane never confirmed (no 'esc to interrupt' after send) within ${TICK_ARRIVE_TIMEOUT}s. The pane may be stranded or unresponsive -- check by hand: tmux attach -t ${TARGET%%:*}"
exit 1
