#!/bin/bash
# Sourced-only, from dispatch.sh, right after dispatch-worktree.sh. Part of
# the agent-supervisor#716 split (see dispatch-rehome.sh's own header for the
# shape and precedent). Never run standalone.
#
# Step 3.5 (put the lane IN its worktree at the OS level via `respawn-pane -c`,
# #15/#236), step 3.6 (dismiss a fresh harness's own startup menu, #255), step
# 4 (rename the window, append the deliverable contract to the brief), the
# `/clear`-then-type-then-submit sequence (#178/#193/#240), step 4.5 (the
# point of no return: `commit-lane-claim`, agent-dotfiles#209 round 2), step 5
# (confirm the brief actually started -- #141/#255), and step 5.5 (confirm the
# pane survived the send, not just that tmux accepted it -- agent-supervisor#456).
# Calls `abort_send` (dispatch-worktree.sh, sourced just before this file).
# --- 3.5. put the lane IN its worktree, at the OS level (#15) --------------
#
# Measured on a live codex lane (#15): `lsof -a -p "$(tmux list-panes ... pane_pid)" -d cwd`
# resolved to the SHARED checkout, never the worktree just created above,
# even though the brief typed into the pane a few lines below names the
# worktree and tells the lane to work only there. The harness process
# occupying the pane had its cwd fixed the moment it was execed -- at lane
# creation (`bootstrap-session.sh`'s `new-window -c "$WORKDIR"`) or its last
# relaunch -- and nothing after that point can change a running process's
# cwd from outside it. Typing `cd $WORKTREE` into the pane does not touch
# that cwd: past this point in a lane's life the pane is not a shell, it is
# the harness's own chat input box, and text sent there is a PROMPT, read and
# acted on by the agent, never executed by a shell. A Claude lane usually
# gets away with it because it reasons in absolute paths; a codex lane does
# not, which is exactly how #15 was caught.
#
# So the fix is not a stronger sentence in the brief (#15 already had one,
# and it still failed -- the shape #73/#81/#263 keep closing). It is the same
# mechanism `restore.sh` already uses to put a restored lane in the right
# directory: `-c <dir>` on the tmux call that creates the pane's process,
# which sets the REAL, OS-level starting directory before anything runs in
# it. `restore.sh` gets that for free because it always creates a fresh
# window. `dispatch.sh` reuses an existing lane's window, so the equivalent
# here is `respawn-pane -k -c "$WORKTREE"`: kill whatever the harness left
# running (the pool only ever offers this step a FREE lane -- ledger and
# lanes.sh both said so above -- so there is no live conversation to lose,
# same as `restore.sh`'s own "no open task -> restore fresh" branch) and
# start the harness AS the pane's new process, its adapter's own
# `HARNESS_LAUNCH_CMD` given to the same tmux call as its argv (#236) --
# a real shell command, run as the pane's process directly, which is the one
# place in this script's lifetime a `cd`-shaped instruction is actually
# obeyed by something other than the agent choosing to obey it.
#
# Fails closed: a harness this dispatch cannot identify, or one whose
# adapter records no launch command, is refused rather than dispatched with
# an unverifiable cwd -- the exact failure mode #15 is about, produced on
# purpose instead of by accident.
# agent-supervisor#668: --adopt-pane's entire reason to exist is skipping
# everything in this block -- the respawn IS "spawning a new claude process",
# the exact thing this mode was built to avoid. The candidate is already a
# live, idle harness process (lanes.sh/the ledger both said so before this
# point), so there is no cwd to re-verify by killing and restarting it and no
# fresh-process menu to dismiss; PROMPT_IN_LAUNCH stays 0 so step 4 below
# falls straight through to the ordinary typed-message send (verified_preclear
# + verified_type + verified_submit), exactly as it would for any other
# tmux-flow dispatch. See this flag's own usage comment at the top of this
# file for what an adopted pane's OS-level cwd is left at instead.
# agent-estate#446: resolved BEFORE the --adopt-pane branch below, not only
# inside the respawn branch -- an adopted pane still needs its harness's own
# input-box shape for the verified_preclear/verified_type/verified_submit
# calls further down (step 4), which run for BOTH branches. Left unresolved
# here, those calls would fall through to `${H_INPUT_BOX_PROMPT[$HARNESS_HIDX]}`
# with HARNESS_HIDX either unset (a `set -u` abort) or empty (which bash's
# arithmetic-subscript rule silently reads as INDEX 0 -- Claude's own entry,
# since harness/claude.sh sorts first -- reusing Claude's box shape for
# whatever harness the adopted pane actually runs, a wrong-answer failure
# mode strictly worse than the honest `unknown` this file is closing).
HARNESS_HIDX=""
if [ -n "$LANE_HARNESS" ]; then
  HARNESS_HIDX=$(harness_index_for_name "$LANE_HARNESS") || HARNESS_HIDX=""
fi
if [ -n "$ADOPT_PANE" ]; then
  PROMPT_IN_LAUNCH=0
  echo "dispatch: --adopt-pane $ADOPT_PANE -- adopting $LANE's existing process, not respawning it (agent-supervisor#668)" >&2
else
if [ -z "$HARNESS_HIDX" ] || [ -z "${H_LAUNCH_CMD[$HARNESS_HIDX]:-}" ]; then
  abort_send "no launch command recorded for harness '${LANE_HARNESS:-unknown}' in $LANE -- cannot relaunch it in the worktree, so its cwd cannot be verified correct (#15); #$ISSUE_ARG was NOT dispatched"
fi
LAUNCH_CMD="${H_LAUNCH_CMD[$HARNESS_HIDX]}"

# agent-dotfiles#255: a harness whose adapter sets H_LAUNCH_TAKES_PROMPT
# (codex, see harness/codex.sh) does not treat the first message TYPED into
# a live pane as a real turn -- so for this harness's fresh-lane path there
# is no typed message at all. The short "Read $BRIEF ..." pointer built
# above (MESSAGE) is folded into LAUNCH_CMD itself, as the harness's own
# documented launch-time PROMPT argument, and step 4 below skips typing
# entirely for it -- see PROMPT_IN_LAUNCH.
#
# `printf %q` quotes MESSAGE for re-parsing by the shell respawn-pane hands
# LAUNCH_CMD to (it contains no embedded newline -- MESSAGE is built as one
# line above -- so this is a plain single-token quoting job, not the
# newline-as-Enter hazard send.sh's verified_type refuses).
PROMPT_IN_LAUNCH=0
if [ "${H_LAUNCH_TAKES_PROMPT[$HARNESS_HIDX]:-0}" = 1 ]; then
  PROMPT_IN_LAUNCH=1
  LAUNCH_CMD="$LAUNCH_CMD $(printf '%q' "$MESSAGE")"
fi

# agent-supervisor#236: LAUNCH_CMD is handed to respawn-pane as its own
# argv, so it becomes the pane's PROCESS directly -- it is never typed into
# whatever the respawn produces. The prior shape here was `respawn-pane -k`,
# sleep one second, then a blind `send-keys "$LAUNCH_CMD" Enter`: exactly the
# mechanism #236 reports, a lane found blocked on a Claude Code menu offering
# to run a pasted, unpinned launch command (the same shape #120/#135 already
# refuse to let this file's OWN launch literal be -- see harness/claude.sh),
# because nothing checked what was listening for those keystrokes a second
# after the respawn. One tmux call now does what three did. There is no
# window in which the launch command exists as text for something else to
# receive, so DISPATCH_RESPAWN_SETTLE -- the sleep that used to buy that
# window time to become a shell before it was typed into -- has nothing left
# to settle and is no longer read on this path. H_SEND_LITERAL is likewise
# moot here: it governed how `send-keys` parsed `$LAUNCH_CMD` for tmux key
# names, which does not apply to a shell command handed to respawn-pane's
# own argv.
#
# This still only runs against a lane `lanes.sh` and the ledger have already
# said is FREE (checked above, same as before #236) -- `respawn-pane -k`
# kills whatever is in the pane, and that hazard is unchanged by this fix;
# it is guarded by staying on the free-lane path, not by anything new here.
#
# agent-supervisor#166/#421: this is the routine, everyday respawn -- every
# ordinary dispatch of a free lane onto its next issue goes through here far
# more often than a lane's first boot or a post-crash restore, so it gets the
# same tmux guard on PATH ahead of the real binary those two sites already
# install. Best effort: an install failure degrades to dispatching without
# the guard rather than refusing the dispatch outright.
GUARD_BIN=""
GUARD_BIN="$(install_tmux_guard 2>&2)" || GUARD_BIN=""
if [ -n "$GUARD_BIN" ]; then
  LAUNCH_CMD="PATH=\"$GUARD_BIN:\$PATH\" $LAUNCH_CMD"
fi
if ! tmux respawn-pane -k -t "$LANE_TARGET" -c "$WORKTREE" "$LAUNCH_CMD" 2>/dev/null; then
  abort_send "tmux respawn-pane failed for $LANE -- could not put it in its worktree; #$ISSUE_ARG was NOT dispatched"
fi

# Give the harness time to actually start before anything else is typed at
# it -- a cold process start is slower than the UI repaint `/clear` waits out
# below, so this gets its own, longer default. Still load-bearing after
# #236: the harness's startup wall-clock is unchanged by how it was started,
# and `verified_preclear` just below still sends `/clear` + Enter before it
# has read the pane back even once -- this sleep is what keeps that send
# from landing on a splash screen instead of a ready input box.
sleep "${DISPATCH_LAUNCH_SETTLE:-3}"

# --- 3.6 accept a fresh harness's own one-time menu, if it has one --------
# agent-dotfiles#255. A cold codex process launched into a directory it has
# never seen (every worktree this dispatch just created, every time) opens
# on its own directory-trust menu, not the ordinary chat box the next steps
# expect -- see `send.sh`'s `verified_dismiss_menu` for the live-measured
# mechanics and why this is #255's actual root cause: the pane's first
# Enter goes to that menu's default selection, not to `/clear` or a brief,
# which is the same "first Enter lands somewhere else" shape #255 reported
# as a whole brief consumed as a session TITLE.
#
# `H_OPTION_ROW_RE`/`H_MENU_TAIL` are the SAME per-harness adapter values
# `lanes.sh` already keys its own menu-blocked reading on -- nothing new is
# defined here, and a harness whose adapter names no such menu (Claude,
# Copilot today) gets an empty regex, which returns success on the first
# read without sending anything at all. Fails closed: a menu still showing
# after every retry aborts the dispatch rather than typing a brief onto it.
if ! verified_dismiss_menu "$LANE_TARGET" "${H_OPTION_ROW_RE[$HARNESS_HIDX]:-}" "${H_MENU_TAIL[$HARNESS_HIDX]:-6}" \
     --settle "${DISPATCH_MENU_SETTLE:-2}" --retries "${DISPATCH_MENU_RETRIES:-5}"; then
  abort_send "a startup menu never cleared in $LANE -- #$ISSUE_ARG was NOT dispatched (check the pane by hand)"
fi
fi

# agent-estate#446: this lane's own input-box marker/close-mode, resolved
# once here and threaded to every verified_preclear/verified_type/
# verified_submit call below via --box-prompt/--box-close -- ALWAYS passed
# explicitly (never an omitted flag) so this stays two plain scalar
# variables rather than a possibly-empty bash ARRAY: `"${arr[@]}"` on a
# zero-element indexed array aborts with "unbound variable" under `set -u`
# on bash 3.2 (macOS's own `/bin/bash`, confirmed live -- this script's own
# shebang), the same bash-3.2 constraint harness-registry.sh's own header
# documents for why it uses indexed rather than associative arrays.
#
# BOTH `:-`, deliberately (not the empty-means-"positively unmeasured"
# sentinel `input-box.sh`'s own header, section 4, documents for a caller
# that wants it): a harness with no HARNESS_INPUT_BOX_PROMPT recorded at all
# -- `harness/copilot.sh` today, and any lane whose LANE_HARNESS never
# resolved -- falls back to Claude's own marker/`rule`, the SAME read this
# whole file gave every harness before #446. That is a deliberate, narrow
# fix: only `codex` (the harness #446 is actually about, MEASURED live
# against a real pane -- see harness/codex.sh) gets its own shape here.
# Widening this to fail an unmeasured harness closed to `unknown` is a real
# improvement input-box.sh's own contract supports, but it is a SEPARATE,
# larger behavior change (copilot's box has never been measured either way,
# and this file's own dispatch already relies on the Claude-shaped fallback
# working for it today) that #446 did not ask for and this change does not
# make.
BOX_PROMPT="$INPUT_BOX_PROMPT"
BOX_CLOSE="rule"
if [ -n "$HARNESS_HIDX" ]; then
  BOX_PROMPT="${H_INPUT_BOX_PROMPT[$HARNESS_HIDX]:-$INPUT_BOX_PROMPT}"
  BOX_CLOSE="${H_INPUT_BOX_CLOSE[$HARNESS_HIDX]:-rule}"
fi

# --- 4. the lane is told what it is doing, then given the work ------------
if ! tmux rename-window -t "$LANE_TARGET" "$WINDOW_NAME" 2>/dev/null; then
  echo "dispatch: could not rename $LANE -- not dispatching #$ISSUE_ARG" >&2
  "$HERE/worktree.sh" done "$WORKTREE" >/dev/null 2>&1
  cleanup_dispatch_branch
  release_claim
  release_lane_claim
  exit 1
fi

# The standing deliverable contract (#117), written into the BRIEF rather than
# typed at the pane. A lane completed #112 correctly -- tests green,
# mutation-checked, committed -- and stopped, because the brief never said to
# push. It was right to be literal. From outside, a lane that finished without
# shipping is indistinguishable from one that did nothing: no PR, no comment,
# issue still claimed, and the work living only as an unpushed commit in a
# temporary worktree one cleanup away from being lost.
#
# Still structural, which is the whole point of #117: the DISPATCHER writes it
# on every dispatch, so it does not depend on whoever wrote the brief
# remembering -- the mechanism that failed in #114. It moved out of the typed
# message because that string has a hard length budget and this text does not
# fit in it; the brief file has no such limit and is the thing the lane is told
# to read.
#
# It also stops the message contradicting itself. Typed at the pane, the
# dispatcher said "that file is your COMPLETE brief" and then added an
# instruction that was not in it -- and for a read-only review brief, "push
# your branch and open a PR" contradicted the brief's own first line. In the
# file it sits with the rest of the instructions and defers to them.
# agent-estate#793: this dispatcher already knows exactly which lane $LANE
# is -- lane selection resolved it above, independent of tmux focus. A lane
# asked to name itself for a `Review-Lane:`/`Lane:` trailer used to be told
# to derive that answer itself with a bare `tmux display-message`, which
# reports the SESSION'S ACTIVE window, not the caller's own pane -- wrong
# for any lane dispatched into a window that is not currently focused, with
# no error (skills#289, skills#291: both stamped the supervisor's own
# window). Stating the value here removes the derivation step entirely: a
# lane that is TOLD its id cannot mis-derive it. `lane-whoami.sh` still
# exists as a fallback for a brief that predates this contract or is typed
# by hand outside dispatch.sh -- it is not removed, just no longer the
# primary path for anything this dispatcher itself sends.
CONTRACT_MARKER="<!-- dispatch:deliverable-contract -->"
if ! grep -qF "$CONTRACT_MARKER" "$BRIEF" 2>/dev/null; then
  cat >>"$BRIEF" <<EOF || abort_send "could not append the deliverable contract to $BRIEF -- #$ISSUE_ARG was NOT dispatched"

$CONTRACT_MARKER
## Delivering this work

Added by \`dispatch.sh\` on every dispatch, not by the brief's author.

**Your lane id is \`$LANE\`.** Use this exact value for any \`Review-Lane:\` or
\`Lane:\` trailer this brief asks you to write -- it is stated here, not
something to derive. Do not run a bare \`tmux display-message\` to find it;
that command answers for whichever window is currently focused, not
necessarily this one (agent-estate#793). If you need to double-check it,
\`scripts/supervisor/lane-whoami.sh\` is the one command that gets this right
regardless of focus.

Unless this brief says otherwise, when you are finished:
**push your branch and open a PR**.
If you produced no code -- a review, an investigation, an options paper --
**post your findings as a comment** on the issue or PR the brief names.

Do not stop with the work only in your worktree. From outside, a lane that
finished without shipping is indistinguishable from a lane that did nothing:
unshipped work looks exactly like no work, and the worktree is temporary.
EOF
fi

# agent-dotfiles#237: the instant BEFORE the `/clear` that starts this lane's
# new conversation. `harness_session.py` uses it to tell the transcript this
# dispatch created from every other transcript on the machine -- see that
# module for why "began after this moment" is one of the three tests it
# requires, and why nothing weaker resolves a lane in this estate.
DISPATCH_SEND_EPOCH=$(date +%s)

# `/clear` first: an author reviewing their own PR is not an independent
# reviewer, and a lane carrying the last task's context is not a fresh one.
#
# `C-u` immediately before it, agent-supervisor#178/#179: this is the FIRST
# thing dispatch.sh ever types into a lane it did not just create, and #179
# found exactly this box holding a leftover, unsubmitted prompt from
# something else entirely (`merge the PR`, sitting in the author lane's
# input box). Typing `/clear` on top of that appends to it rather than
# clearing anything -- `/clear` is a plain string to the pane, not a key
# tmux interprets -- and #178's diagnosis is that the Enter which follows
# then may not submit either.
#
# VERIFIED, not bare, as of agent-supervisor#193: `/clear`'s own Enter can be
# swallowed exactly the way #178 already found a brief's Enter can be, and
# that failure used to be invisible -- the retyped brief landed glued onto
# the unsubmitted "/clear", and the proof-token check below (an AND-of-
# substrings check with no notion of position) still read it as `landed`
# because its tokens were still true substrings of the corrupted line.
# `verified_preclear` (send.sh) confirms the screen actually came back
# BLANK -- the input box reads `empty`, not `text` or `unknown` -- before
# anything else is ever typed. This is still not `verified_type`/
# `verified_submit`: `/clear` blanks the whole screen, which the proof-token
# check was never built to read through (see that function's own header) --
# confirming the blank is the whole of what can be confirmed here.
#
# Defaults raised 2 -> 5 (settle) and 2 -> 6 (retries), agent-dotfiles#255,
# landed by #447: Jon reported three consecutive dispatches refused today
# with "`/clear` did not blank <lane>'s screen" at the old defaults, on real,
# loaded panes -- the guard firing correctly (#193's whole point), just
# against too small a budget to survive the load it was actually up against.
# Raising to these values, by hand, made all three succeed. The
# `Escape`-before-`C-u` fix just above (send.sh) targets the same failure by
# a different mechanism -- a real key instead of a bigger budget -- and may
# make some of this margin unnecessary; it is kept anyway because it costs
# latency only on the preclear path (2-5s x up to 6 retries, worst case, once
# per dispatch), and a lane wrongly refused here costs nothing a retry
# doesn't fix, while a `/clear` this guard incorrectly waved through is
# exactly #255's silent-success shape.
#
# agent-dotfiles#255: skipped entirely when PROMPT_IN_LAUNCH -- that harness's
# pane was just started fresh by respawn-pane above with the brief-pointer
# message already folded into its own argv, so there is no live conversation
# to clear and nothing typed yet for a corrupted `/clear` to glue onto.
if [ "$PROMPT_IN_LAUNCH" != 1 ]; then
  if ! verified_preclear "$LANE_TARGET" \
       --settle "${DISPATCH_SETTLE:-5}" --retries "${DISPATCH_PRECLEAR_RETRIES:-6}" \
       --box-prompt "$BOX_PROMPT" --box-close "$BOX_CLOSE"; then
    if [ "$SEND_STATUS" = send_failed ]; then
      abort_send "send-keys to $LANE failed -- #$ISSUE_ARG was not dispatched"
    fi
    abort_send "/clear did not blank $LANE's screen -- #$ISSUE_ARG was NOT dispatched (check the pane by hand)"
  fi
fi

# Type, verify, THEN submit -- send.sh's verified_type, extracted from what
# used to be this loop. The verification is why the Enter is a separate call
# (verified_submit, below the ledger commit): what the pane actually shows
# is the only evidence that the keys landed.
#
# Check BOTH ENDS of the message plus the worktree path -- the proof tokens
# below. The head is what a dropped prefix eats first (observed live,
# 2026-08-11), and it is also the first thing an over-long message hides by
# scrolling -- so checking the head alone conflates "arrived and is visible"
# with "fits". The tail is the part that stays visible under scrolling, so it
# is the half that still reports honestly when the box is full; checking only
# the tail would pass a dropped prefix, which is the failure this loop exists
# for. Both, or neither is evidence.
#
# The tail token is the closing phrase plus $REPO_PATH, not the path alone:
# the harness prints the working directory in its own header, so the bare
# path matches ordinary pane furniture and would pass on a blank pane.
# send.sh strips spaces and newlines from both sides before matching, because
# a real pane wraps a long path across lines and indents the continuation.
#
# The head token is `--proof-head`, not `--proof`, as of agent-supervisor#193:
# the ORIGINAL `--proof "Read $BRIEF"` only checked that the string appeared
# somewhere on the pane, and "somewhere" is exactly what let a corrupted
# `/clearRead $BRIEF...` -- `/clear`'s own Enter swallowed, the retyped brief
# glued onto the unsubmitted line -- read as `landed`: `Read $BRIEF` was
# still a true substring, just not the start of anything. `--proof-head`
# anchors it to the START of the input box's own content instead (see
# send.sh's `_send_head_matches`), so this exact corruption now fails the
# check and falls into the C-u-and-retype loop below rather than shipping.
#
# `--preclear`, as of agent-supervisor#240: `verified_preclear` above already
# confirmed this box empty, but that confirmation covers the instant it was
# taken, not the instant `send-keys` below actually runs -- six lanes were
# measured holding live unsent text that `lanes.sh` had classified `free`,
# `busy` or `broken`, never `unsent`, which is exactly the shape of text
# landing in a gap no earlier check could see. `--proof-head` already
# detects a glued brief and the retry above already recovers it, but that
# recovery costs a whole extra type-and-check round trip every time, and it
# is not the only thing that could go wrong in that gap. A classification --
# `verified_preclear`'s included -- can be wrong; one more `C-u`, sent
# immediately before the keys that matter, cannot be.
# agent-dotfiles#255: skipped when PROMPT_IN_LAUNCH, same reason as the
# preclear above -- the message is already this harness's launch argv, never
# a live pane's typed input, so there is nothing here to type or verify.
if [ "$PROMPT_IN_LAUNCH" != 1 ]; then
  if ! verified_type "$LANE_TARGET" "$MESSAGE" \
       --settle "${DISPATCH_SETTLE:-1}" --retries 2 --preclear \
       --proof-head "Read $BRIEF" \
       --proof "$WORKTREE" \
       --proof "never work in the shared checkout at $REPO_PATH." \
       --box-prompt "$BOX_PROMPT" --box-close "$BOX_CLOSE"; then
    if [ "$SEND_STATUS" = send_failed ]; then
      abort_send "send-keys to $LANE failed -- #$ISSUE_ARG was not dispatched"
    fi
    abort_send "the brief did not land intact in $LANE -- #$ISSUE_ARG was NOT dispatched (check the pane by hand)"
  fi
fi

# --- 4.5 THE POINT OF NO RETURN, AND IT IS THE SEND -----------------------
# agent-dotfiles#209 round 2. `CLAIM_COMMITTED` used to be set ~70 lines below
# this, after step 5's confirmation loop -- and step 5 costs up to
# DISPATCH_CONFIRM_TRIES x DISPATCH_SETTLE (10s by default) of wall clock. So
# for that whole window the lane was renamed and the brief was ABOUT to be
# live, while both cleanup paths still believed the dispatch was unwindable. A
# SIGTERM landing there ran the trap and deleted the claim, and
# `lane_available` answered True for a lane that was actively working -- #102's
# shape produced BY the cleanup, which step 6's own comment below says in its
# own words must not happen. Reproduced against the stubs, both directions, in
# tests/supervisor/test_dispatch.sh.
#
# So the commit happens HERE, before the Enter rather than after the
# confirmation, because "the brief is live" starts at the submit and not at
# the moment the bookkeeping finishes noticing.
#
# IT IS WRITTEN TO THE LEDGER, not just to this shell. `CLAIM_COMMITTED` below
# is only a fast path for the trap; it dies with this process, and the SIGKILL
# case is exactly the one where this process stops existing while the pane
# keeps working. `commit-lane-claim` moves the placeholder to a status both
# `release_lane_claim` and `reap-lane-claims` refuse to touch, so the
# protection survives a kill that no shell can trap. That ordering also means
# a signal arriving BETWEEN the ledger write and the assignment below is
# already safe: the trap's release matches no row.
#
# WHAT THE REORDERING COSTS, stated rather than discovered later. From here
# every failure leaves the lane HELD -- including a send that fails outright,
# and step 5 concluding the brief never left the input box. Those used to free
# the lane. That is deliberate and it is the fail-closed direction: a lane
# wrongly held costs capacity and is recovered by the documented command the
# "no free lane" refusal prints; a lane wrongly freed costs a running lane's
# work and is recovered by nothing. `lanes.sh` still shows such a lane
# `unsent`, so the cost is visible rather than silent.
#
# FATAL IF IT FAILS, and that is the same argument pointing the other way:
# nothing has gone into the pane yet, so refusing is still free, and sending a
# brief we could not first mark as live would leave the exact window this
# block exists to close.
COMMIT_OUT=$("$LEDGER_PYTHON" "$LEDGER_CLI" commit-lane-claim --lane "$LANE" --token "$CLAIM_TOKEN" 2>&1) \
  || COMMIT_OUT="${COMMIT_OUT:-commit-lane-claim failed to run}"
if ! grep -qF '"committed":true' <<<"$COMMIT_OUT"; then
  sed 's/^/  /' <<<"$COMMIT_OUT" >&2
  abort_send "could not mark $LANE's claim live before sending -- #$ISSUE_ARG was NOT dispatched (nothing was submitted)"
fi
CLAIM_COMMITTED=1

# --- 5. AND THE BRIEF ACTUALLY STARTED ------------------------------------
# #141. Everything above proves the brief was TYPED. Nothing proved it was
# SUBMITTED, and on 2026-08-11 two lanes sat for 40 minutes each holding a
# full brief in the input box because the Enter arrived while `/clear` was
# still repainting and was swallowed. The dispatcher printed
# `dispatch: #N -> lane` and walked away. This is the #81 and #130 shape
# again: the dispatcher's success message is not evidence of dispatch.
#
# What "started" means is measured, not assumed. The obvious check -- wait for
# the footer to show a running shape -- is racy: driving a real Claude Code
# pane through a short turn, `esc to interrupt` was gone from the footer
# within six seconds, so a fast first turn looks exactly like a brief that
# never ran. The input box emptying is the durable signal: it is true while
# the turn runs AND after it finishes, and it is false in precisely the
# failure this exists for.
#
# LATENCY: this loop adds ~DISPATCH_SETTLE (default 1s) to every dispatch,
# even one that lands instantly, because the first sleep runs before the
# first check -- and up to DISPATCH_CONFIRM_TRIES x DISPATCH_SETTLE (10s by
# default) to a slow-confirming one. That is the price of #141: it is what
# turns "the dispatcher printed success" into "the box actually went empty",
# so do not tune DISPATCH_CONFIRM_TRIES down to make dispatch feel faster
# without understanding that the loop is what makes an unsent brief
# detectable instead of silent.
# verified_submit sends the Enter itself -- this used to be a separate
# `tmux send-keys ... Enter` call right above step 5's comment block; moving
# it into send.sh changed nothing about WHEN it fires (still immediately
# after the ledger commit above, still fatal if the send-keys call itself
# errors), only where the code that fires it lives.
# agent-dotfiles#255: PROMPT_IN_LAUNCH has no Enter to send and no box to
# poll for empty -- the message left as this harness's launch argv, so
# `verified_submit`'s box-state check has nothing to read. `verified_launch_prompt`
# is its replacement for this path: it polls for that harness's OWN recorded
# failure signature (H_LAUNCH_PROMPT_FAILURE_RE -- codex's `Session renamed
# to`, see harness/codex.sh) instead of a box state, because there is no box
# state here for a title-eating quirk to leave behind -- only that
# signature, painted by the harness itself.
# `--blocked-re`/`--option-row-re` below, agent-dotfiles#255 round 2: LIVE
# reproduction, real codex, never a mock -- `respawn-pane -c $WORKTREE`
# always starts the harness in a worktree it has never seen, so codex's own
# directory-trust menu ("Press enter to continue") came up on THIS dispatch
# before the folded prompt was ever read, and nothing above would have
# caught it: the menu contains no `Session renamed to`, so an unanswered
# codex lane would have reported SEND_STATUS=submitted -- silent success,
# #255's exact shape, out of the very fix meant to close it. Wired from the
# same H_BLOCKED_MARKERS/H_OPTION_ROW_RE fields `lanes.sh` already reads to
# classify a lane `menu-blocked`.
if [ "$PROMPT_IN_LAUNCH" = 1 ]; then
  if ! verified_launch_prompt "$LANE_TARGET" \
       --tries "${DISPATCH_CONFIRM_TRIES:-10}" \
       --settle "${DISPATCH_SETTLE:-1}" \
       --failure-re "${H_LAUNCH_PROMPT_FAILURE_RE[$HARNESS_HIDX]:-}" \
       --blocked-re "${H_BLOCKED_MARKERS[$HARNESS_HIDX]:-}" \
       --option-row-re "${H_OPTION_ROW_RE[$HARNESS_HIDX]:-}"; then
    case "$SEND_STATUS" in
      stranded)
        abort_send "$LANE's harness did not accept the folded launch prompt as a turn (${H_LAUNCH_PROMPT_FAILURE_RE[$HARNESS_HIDX]:-} matched) -- #$ISSUE_ARG was NOT dispatched (check the pane by hand)" ;;
      blocked)
        abort_send "$LANE is still stuck on a menu/prompt after the folded launch (e.g. a first-sight directory-trust gate) -- the brief may be queued behind it, unconfirmed either way; #$ISSUE_ARG was NOT dispatched (answer the prompt by hand, then re-dispatch)" ;;
      unknown)
        echo "dispatch: WARNING -- $LANE's harness has H_LAUNCH_TAKES_PROMPT set but no H_LAUNCH_PROMPT_FAILURE_RE, so the folded launch prompt could not be confirmed either way" >&2
        echo "dispatch: #$ISSUE_ARG is claimed and the worktree exists; CHECK THE PANE BY HAND." >&2
        ;;
    esac
  fi
elif ! verified_submit "$LANE_TARGET" \
     --confirm-tries "${DISPATCH_CONFIRM_TRIES:-10}" \
     --confirm-settle "${DISPATCH_SETTLE:-1}" \
     --box-prompt "$BOX_PROMPT" --box-close "$BOX_CLOSE"; then
  case "$SEND_STATUS" in
    send_failed)
      abort_send "could not submit the brief in $LANE -- #$ISSUE_ARG was not dispatched" ;;
    stranded)
      # Confirmed failure: the message is still sitting in the box. Unwind,
      # so the issue goes back to the pool rather than looking
      # claimed-and-running.
      #
      # The text is deliberately NOT cleared on the way out. C-u does not
      # reliably empty a multi-row box on a real pane, so "cleared" would be
      # another unverified claim -- and a lane left holding it is now
      # visible: `lanes.sh` reports it `unsent` with a count line, which is
      # the state #141 added for exactly this.
      abort_send "the brief was typed into $LANE but never submitted -- #$ISSUE_ARG was NOT dispatched (lanes.sh will show that lane 'unsent')" ;;
    unknown)
      # The box could not be identified at all -- another harness, or a pane
      # too short to show it. The brief may well be running, so unwinding
      # would release a claim out from under a working lane, which is its
      # own failure. Say so loudly instead of printing a clean success line.
      echo "dispatch: WARNING -- could not confirm the brief started in $LANE" >&2
      echo "dispatch: the input box was not readable (input_box_state: ${SEND_BOX_STATE:-none})." >&2
      echo "dispatch: #$ISSUE_ARG is claimed and the worktree exists; CHECK THE PANE BY HAND." >&2
      ;;
  esac
fi

# --- 5.5 THE PANE ACTUALLY SURVIVED, NOT JUST "TMUX ACCEPTED THE SEND" ----
# agent-supervisor#456, mined from Gastown's `VerifySurvived` (`internal/
# session/lifecycle.go`, `StartSession` step 12) via #453. Step 5 above
# proves the input box went EMPTY; it does not prove the process behind it
# is still THERE. Both read identically to `verified_submit`: a turn that
# just started empties the box, and so does a process that crashed and took
# the whole tmux window down with it -- codex eating a whole brief as a
# session title (agent-dotfiles#255) and a lane running for an hour inside a
# worktree that no longer existed are both this same shape, "success" was
# read off the send, not off the agent. Only checked when SEND_STATUS is
# still `submitted` -- true whichever of the two branches just above set it:
# `verified_submit`'s ordinary box-empty read, or `verified_launch_prompt`'s
# own no-failure-signature read for a `PROMPT_IN_LAUNCH` harness
# (agent-dotfiles#255's codex fold). Every other value either branch can
# leave behind (`stranded`/`send_failed`/`blocked` already aborted above,
# `unknown` already means "could not observe") is left alone here -- this
# step cannot improve on "could not observe" and must not run at all on a
# path that already aborted.
#
# Deliberately does NOT release the claim -- CLAIM_COMMITTED was set at step
# 4.5, before this pane was ever typed into, and step 4.5's own comment is
# unchanged by this: a lane wrongly freed here costs a running lane's work
# and is recovered by nothing, while a lane wrongly left HELD costs only
# capacity and is recovered by the command printed below. What a dead pane
# never gets is ACCEPTED (see `DISPATCH_CONFIRM_LANDED_ARGS` immediately
# below, which reads `DISPATCH_DIED`) -- and, in practice, no fresh
# task row at all: `LANE_META` (step 6, further down) reads the pane's own
# identity fields, which read BLANK for a target that no longer exists
# (measured, see `verified_survived`'s own header), and `record_dispatch`'s
# existing, pre-#456 guard already refuses to register a lane from blank
# fields -- falling into the existing `ledger_record_failed`/`mark_lane_held`
# path rather than a new one. Either way, dispatch.sh's own exit code goes
# non-zero (see `DISPATCH_DIED`, checked at the very end) so a caller does
# not read a clean "dispatch: #N -> lane" success line over a lane that is
# not running anything.
# `--settle` defaults to `$DISPATCH_SETTLE` itself (1s in production, the
# same knob step 5 above already tunes), not a fresh number -- measured
# 2026-08-21 against a real, throwaway isolated tmux socket: `kill-pane`
# takes a killed window out of `list-windows` synchronously (no propagation
# lag to wait out; see `verified_survived`'s own header in send.sh for the
# measurement), so this settle only has to cover a crash landing a beat
# AFTER the box drained, not tmux's own latency. Reusing `DISPATCH_SETTLE`
# also means `tests/supervisor/test_dispatch.sh`'s existing `DISPATCH_SETTLE=0`
# already keeps this step instant in the suite, the same way it already
# keeps steps 3-5 instant, without a second env var every test call site
# would otherwise need to remember to zero.
#
# ADDED LATENCY UNDER LOAD (agent-dotfiles#255, same live session that raised
# verified_preclear's own default 2s/2 -> 5s/6, and that Jon reports needed
# `DISPATCH_SETTLE=12-14`/`DISPATCH_PRECLEAR_RETRIES=14-16` by hand to clear
# reliably that day): this loop sleeps ONE `--settle` period before its FIRST
# check and returns the instant that check finds the pane alive -- it only
# burns a second retry's worth of sleep for a pane that is ACTUALLY not there
# yet. So a heavier `DISPATCH_SETTLE`, however high an operator has to raise
# it for `verified_preclear`'s box-CONTENT reads to stop false-refusing under
# load, costs this step only that same single settle period on the ordinary,
# healthy path -- not `settle x retries`. And because this check is
# existence-only (no content read at all, see the header above), a harness
# that is merely SLOW to render under load is not what this loop is
# watching for in the first place: the window still exists the whole time a
# slow-but-fine harness is starting, so raising DISPATCH_SETTLE for
# verified_preclear's sake does not make this step more likely to
# false-positive the way it would a content-matching check.
# Saved BEFORE calling verified_survived, which overwrites SEND_STATUS with
# its own vocabulary (survived/died) on return -- `DISPATCH_CONFIRM_LANDED_ARGS`
# just below still needs to know verified_submit's own answer, not
# verified_survived's, and reading `$SEND_STATUS` there unsaved would silently
# read the WRONG check's status by the time it runs.
DISPATCH_SUBMIT_STATUS="$SEND_STATUS"
if [ "$SEND_STATUS" = submitted ]; then
  if ! verified_survived "$LANE_TARGET" \
       --settle "${DISPATCH_SURVIVE_SETTLE:-${DISPATCH_SETTLE:-1}}" --retries "${DISPATCH_SURVIVE_RETRIES:-2}"; then
    DISPATCH_DIED=1
    echo "dispatch: WARNING -- $LANE's pane is GONE after the brief was submitted (#$ISSUE_ARG)" >&2
    echo "dispatch: the brief looked delivered (the input box went empty) but nothing survived to run it -- the process or its window died during startup." >&2
    echo "dispatch: $LANE STAYS HELD; this dispatch is NOT recorded as accepted (step 6 below may not even find pane identity left to record). CHECK THE PANE BY HAND." >&2
    echo "dispatch:   $LEDGER_PYTHON $LEDGER_CLI record-completion --lane $LANE --note <note>   # once you know what actually happened" >&2
  fi
fi
