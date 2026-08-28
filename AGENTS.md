# agent-estate — agent orientation

*(`AGENTS.md` and `CLAUDE.md` are the same file — one is a symlink, so there is
no second copy to drift.)*

**This file is an index, not a document to read end to end.** Find your task
below, open the one or two files it names, and stop there. Every claim in it
(path, script, flag) was checked against the tree at the commit named at the
bottom — re-check anything you're about to rely on if it's been a while since
that commit, the same way this rewrite had to.

This repo is two halves merged under migration Step 2b/2c (#682, #744): the
daemon (`agent-supervisor`, everything at the root outside `tui/`) and the
TUI (`agent-tui`, everything under `tui/`). Each keeps its own orientation
section below rather than being blended into one narrative — the two had
separate framing before the merge and still do.

## The daemon

### Before you ask Jon anything — read this first

Jon has stated this more than twenty times. It is a **hard** parameter in his
corpus and it keeps being broken, so it goes at the top of the file rather than
somewhere polite.

**Exhaust the record before a question reaches him**, in this order:

1. **Query the corpus.** `~/.local/state/agent-dotfiles-supervisor/ledger.sqlite3`
   — 3,700+ of his own prompts, 900+ live hard constraints. Views:
   `live_parameters`, `open_questions`, `unacknowledged`, `possibility_count`.
   If you have not queried it this session, you have not earned the question.
2. **Read the docs and the code.** `agent-dotfiles/docs/` carries ~2,467 lines
   of spec — PRD, SPEC, loop-engineering, supervisor-disposition, loop-signals.
   A loop was once declared "never planned" because someone searched the wrong
   repository.
3. **Convene a council** (`ask-a-council`) when the failure modes are plural.
4. **`sanity-check` or `devils-advocate`** when it is one decision that needs
   attacking rather than several lenses.

**Only INTENT questions reach him.** His words, weight hard: *"the right move is
to ask questions that determine intent, not to ask him to make your decisions."*
Architecture, sequencing, which-PR-next, how-to-implement — those are yours.
Deciding them is the job.

**Why this fails, so you can catch yourself:** asking is safe. If he picks, you
cannot have picked wrong. It is the same instinct that ships a stub reading
"not built yet" instead of a populated view — the defensible option over the
useful one. Choose the useful one.

[`README.md`](README.md) explains what the system is; this file explains what
will bite you and where to look for a given task.

### Index — what to open for which task

`scripts/supervisor/` has 100+ tracked files (`git ls-files scripts/supervisor
| grep -cE '\.(sh|py)$'`, checked at write time). This groups them by the job
they do, one line each, saying **what each decides**, not what it is. It is
not a substitute for the file's own header comment — every file here has one,
and it is longer and more specific than this line.

**Dispatch & lane lifecycle** — hand work to a lane, or bring one back:
- `dispatch.sh` — pick a free lane, claim the issue, create its worktree, send
  the brief; supports `--adopt-pane <window-id>` to hand the brief to an
  already-running idle pane's own process instead of spawning a new one
  (#668/#677). Split from 2753 lines into an ~280-line composition root plus
  8 sourced-only siblings, grouped by step (#716, same shape as #713/#708):
  `dispatch-rehome.sh`, `dispatch-args.sh`, `dispatch-preflight.sh`,
  `dispatch-guards.sh`, `dispatch-lane-select.sh`, `dispatch-worktree.sh`,
  `dispatch-send.sh`, `dispatch-record.sh` — none is meant to run standalone,
  and sourcing order is execution order.
- `dispatch-claude-print.sh` / `dispatch-pi-rpc.sh` — `dispatch.sh`'s siblings
  for the `claude -p` and `pi --mode rpc` harness shapes, not replacements —
  and NOT part of the `dispatch-*.sh` split above; a mutation-test search
  across `dispatch*.sh` must exclude them explicitly or it double-matches
  their own, separately-written copies of the same bash-3.2-safe idioms
  (`tests/supervisor/_dispatch_mutate.py`'s own `SPLIT_FILES` list is exact
  for this reason, not a wildcard glob)
- `claim.sh` — claim an issue on GitHub before dispatch, so two dispatchers
  can't both hand out the same one
- `worktree.sh` — one git worktree per lane task
- `collision-check.sh` — refuse a dispatch whose files overlap an in-flight
  lane's (whole-file overlap only) — #291
- `host-pressure.sh` / `host_pressure.py` — refuse a NEW dispatch when the
  host can't safely take it; exit 2 (not 0/1) means "could not measure",
  refused rather than guessed
- `count-agents.sh` — counts real Claude agent SESSIONS by `ps`'s `comm`
  field, not `pgrep -f claude` substring matching (#663/#678/#681, the fixed
  pid-padding bug). **As of this commit its own header still says "nothing
  wires this in yet"** — it is not yet called from `host-pressure.sh`; check
  its header before assuming the host guard uses it.
- `quota.sh` — the quota floor gate; nothing else may call `codexbar` directly
- `lane-done.sh` — rename a lane back to `free-N` on worker completion
- `lane-retire.sh` — administrative retirement: unregister and restore the
  window's name, never kill it (#564)
- `register-lane-self.sh` — how a hand-attached lane registers ITSELF, from
  `$TMUX_PANE` and explicit `-t` reads only
- `lane-whoami.sh` — the one command a review/fix-pass brief should name to
  derive `Review-Lane:`: anchors on `$TMUX_PANE` when set, falls back to the
  Invariant 10 `worktree-lane --include-reviews` self-lookup for a pane-less
  (claude-print) lane; never a bare `tmux display-message` (#685)
- `restore.sh` — rebuild every lane after a tmux server loss, ledger-driven
- `preserve-dead-lanes.sh` — save a dead lane's uncommitted work before it's
  lost (#651)
- `bootstrap-session.sh` — create the tmux session and lane windows dispatch
  sends into

**Lane state / classification** — what is this pane doing right now:
- `lanes.sh` — classify every pane into one of the states in Invariant 6;
  a whitelist, not a guess
- `sessions.sh` — `lanes.sh --json` across every tmux session, not just one
- `adapter.py` / `harness/{claude,codex,copilot}.sh` — harness-neutral pane
  classification; harness-specific strings live only in `harness/*.sh`
  (Invariant 7)
- `harness-registry.sh` — loads every `harness/*.sh` adapter and answers
  "which adapter, if any, owns this pane?"
- `input-box.sh` — is a lane's input box empty, or holding unsent text?
- `live-pane-exceptions.sh` — the named exceptions to send-keys retirement
  (#284)
- `poller-window.sh` — recognise the Telegram poller's window by name

**Ledger, CLI, and reconciliation** — the durable record (Invariant 1):
- `cli.py` — command-line entry point over the ledger
- `core.py` — the SQLite ledger itself: `Ledger` class, transactional
  task/event model, supervisor-lease methods (see Guards below)
- `github_source.py` — GitHub-backed task records for the local spool
- `sensor.py` — selected Git/GitHub state sensor
- `supervisor_view.py` — read-only view over several backing sources, one
  interface (`WRITE_SOURCES` there is the whitelist for the five MCP writes)
- `mcp_server.py` — the MCP read surface, plus the guarded session-management
  writes `supervisor_view.py`'s `WRITE_SOURCES` names
- `session_guard.py` — the one place session removal is judged safe
- `recycle.py` — scheduled supervisor session recycling
- `reconcile_lane_completions.py` / `reconcile_sources.py` /
  `reconcile_worktree_paths.py` — sweeps that fix specific historical drift
  classes; read each one's own docstring before assuming it's still needed
- `backfill-harness-session-ids.py`, `repair_401_reconcile_stamps.py` —
  one-time repairs for specific past incidents, kept for their comments

**PR / review / merge guards** — see "The guards a lane will hit" below.

**Supervisor loop / Director** — the thing that ticks:
- `director-loop.sh` — drive the Director on a cadence (#321)
- `loop-tick.md` — the loop's own step-by-step tick prompt; the supervisor
  lease is taken here, keyed on `#{pane_pid}` (the tmux pane's own live
  process), never on `$$` — a Bash tool call's own subprocess pid dies before
  the next tool call, which made every lease look stale (#671/#674)
- `heartbeat.sh` — detect a stalled estate and nudge it once (#315)
- `watchdog.sh` — restart the supervisor loop when it dies with work left,
  found by pane COORDINATES, not name; sourced-only siblings
  `watchdog-harness.sh` / `watchdog-status.sh` / `watchdog-checks.sh` /
  `watchdog-advance.sh` split its 2126 lines by responsibility (#704), not
  replacements — none is meant to run standalone
- `watchdog_notify.py` — decide whether the watchdog's `escalate` state
  should reach a human
- `sleepcheck.py` — is the loop asleep with a wakeup pending, or actually down
- `state.sh` — the supervisor's current situation as a small hard-capped
  document, not a conversation replay
- `digest.sh` — one command answering "what is the state of the estate now"
- `contest-stop.sh` — auto-contest a STOP-CONCLUSION nobody chose (#19-ish
  history, see its own header)
- `advance-live.sh` — advance the LIVE watchdog worktree to `origin/main`
- `tooling-drift.sh` — tick-time drift detector for the loop's own tooling
  surface (#654)
- `weekly-watch.sh` / `quota-watch.sh` / `quota-watch-recover.sh` — tell Jon
  when the weekly cap or session quota is nearly gone, and self-correct the
  watcher if it hangs; `quota-watch.sh` resolves the live Director via the
  supervisor lease, not a name guess (#673)
- `launchd/render-plists.sh` plus `launchd/templates/*.plist.tmpl` — render
  the 4 live launchd jobs' plists with a versioned, rename-safe entry-point
  path instead of a hardcoded checkout path (#682/#699); a rendered plist
  cannot be proven to fire on this machine's current boot from the repo alone

**Estate loop** (`scripts/estate-loop/` — a separate build loop: no
supervisor, no ledger, no lease; versioned in-repo as of #695, previously
outside it entirely):
- `check.sh` — the loop contract driver (`agent-dotfiles/docs/loop-engineering.md`)
- `status.sh` — deterministic estate status, one mechanical call replacing
  what a tick used to gather by hand
- `tick-scan.sh` — the mechanical half of a tick: detection and gated action,
  no judgement

**Telegram / notify:**
- `notify.sh` — send Jon a short escalation message (Telegram first, iMessage
  fallback)
- `inbox.sh` / `inbox-poll.sh` / `inbox-route.sh` — read Jon's replies;
  `inbox-poll.sh` is the automatic inbound path and a service (Invariant 8),
  `inbox-route.sh` delivers one message to the lane it answers,
  `director-route.sh` always delivers to the Director specifically
- `director-inbox.sh` — out-of-band messages to the supervisor pane, without
  typing into it
- `closed-report.sh` — tell Jon on Telegram which issues closed, every 30 min
- `idea.sh` — capture an idea into the corpus fast, without derailing (#341)
- `poller-leak-cleanup.sh` / `poller-lib.sh` / `poller-recover.sh` — detect
  and clean up leaked or dead poller processes

**tmux safety** (see Invariant 4):
- `tmux-isolation.sh` — `assert_isolated_tmux`, required before any
  destructive tmux verb or session creation (#185)
- `tmux-guard.sh` — a `tmux` wrapper on PATH that refuses a bare destructive
  verb typed directly into a lane's own shell
- `tmux_verb_guard.py` — static guard: no test may create/destroy a tmux
  SESSION outside isolation
- `worktree-guard-audit.sh` — audits that the isolation guard is actually
  reachable from files that source it (#199)
- `send.sh` — the one verified-send primitive for typed text into a pane's
  input box (#178/#186)
- `session-defaults.sh` — shared tmux session name/defaults, centralized so
  a repo rename can't leave stragglers

**Viewers / UI** (a UI PR needs a captured frame — see Conventions):
- `laneview/` — four renderers (`text.sh`, `tui.sh`, `opensessions.sh`,
  `dock.sh`); none is required by another
- `laneview.sh` — drive one `laneview/` implementation from `lanes.sh`'s json
- `laneview-leak-cleanup.sh` — report-first cleanup for leaked `tui.sh`
  processes (#356)
- `look.py` — let an agent SEE a pane: capture / png / navigate / frames
- `termshot.py` — the ANSI-to-SVG rasteriser `look.py`'s `png` renders through
- `dim-strip.sh` — the one place that defines "is this span a Claude Code
  prompt suggestion, or real pane content" (#521)

**Transport (harness-neutral send/receive):**
- `transport.py` — small tmux transport, output stays inside the adapter
- `pi_transport.py` / `acp_transport.py` / `claude_print_transport.py` —
  siblings for `pi --mode rpc`, ACP, and `claude -p`, not replacements
- `harness_session.py` — resolve the agent's own harness conversation id

**Prompt corpus / mining:**
- `mine_prompts.py` / `mine_jon.py` — extract the operator's own turns from
  harness transcripts, nothing else
- `itemize_prompts.py` — turn `prompts` rows into `items` rows, one corpus step
- `backfill_prompt_gap.py` — recover prompts from a dead-capture window
  (source transcripts, not `mine_prompts.py --store`'s live path), and
  populate `prompts.project` from the transcript's own directory since
  neither `record_prompt` nor `mine_prompts.py` sets it (#696)
- `prompt_capture_hook.py` — the `UserPromptSubmit` hook that captures and
  classifies a prompt at submit time instead of relying on someone
  remembering to re-crawl; registered in `.claude/settings.json` (#687/#693)
- `prior-attempts.sh` — what did the last agent on this issue already find
- `acceptance.sh` — re-run a CLOSED issue's acceptance test, reopen if back

**Housekeeping:**
- `branch-sweep.sh` — delete local branches already merged into `origin/main`
- `audit-lanes.sh` — compare every ledger `lanes` row against live tmux, once
- `reap-verified.sh` — one verified reap primitive for tests with a real
  long-lived poller-shaped process
- `lane_identity.py` — is a lane's REGISTRATION still true of the live tmux
  server it names (verified / contradicted / unverifiable, never bare yes/no,
  #520)
- `would-revert.sh` — "would merging this branch revert X", by merging it,
  not by reading a diff
- `refresh_brief_resume.py` — regenerate a brief's `## Resume point` block

`tests/supervisor/` — 279 tracked files (`git ls-files tests/supervisor | wc
-l`, checked at write time; re-run, don't trust this number stale): the suite
is the contract. `python3 -m unittest discover -s tests/supervisor` has not
reliably finished inside one working session's time budget; run a targeted
test file, not a full discovery, when you only touched one thing. The four
former monolithic files (`test_dispatch.sh`, `test_verdict.py`, `test_cli.py`,
`test_core.py`) no longer exist — each was split by topic/`TestCase` class
(#683) into the `test_dispatch_*`, `test_verdict_*`, `test_cli_*` and
`test_core_*` families; look there, not for the single file.

### The guards a lane will actually hit

Each of these can refuse your dispatch, your merge, or your PR. When one
fires, this is where to look — one hop from the refusal message to the code
that produced it.

| Guard | Refuses | Implemented in |
|---|---|---|
| CI gate | merge, until every check is green at the live head SHA | `ci_gate.py`, called from `merge-pr.sh` |
| Authorship / independence | merge, when the reviewer lane is the author lane (Invariant 9) | `verdict-independence.sh` (`lane_relation`), called from `merge-pr.sh` |
| Collision check | dispatch, when files overlap an in-flight lane's | `collision-check.sh`, called from `dispatch.sh` step 3.2; override with `--force` |
| Host pressure | dispatch, when the host can't safely take another agent | `host-pressure.sh` / `host_pressure.py`; exit 2 = "couldn't measure", refused, not guessed |
| Quota floor | new work, when the subscription quota is below the floor | `quota.sh` (`MIN_REMAINING`), watched by `quota-watch.sh` |
| Supervisor lease | a second Director loop starting while one is alive | `core.py` (`Ledger.take_supervisor_lease` / `release_supervisor_lease` / `reap_stale_supervisor_lease`), exposed via `cli.py`'s `take-supervisor-lease` / `supervisor-lease` / `release-supervisor-lease` / `reap-supervisor-lease` subcommands; owner is keyed on `#{pane_pid}` from `loop-tick.md`, never `$$` (#671/#674) |

Other gates worth knowing exist, not listed above because they fire less
often: `completion-gate.sh` (won't advance a task group until every member
left evidence), `fixpass-evidence-gate.sh` / `fixpass_evidence_gate.py` (a fix
pass must paste proof, not a claim — #338), `ui-evidence-gate.sh` /
`ui-evidence-report.sh` (a UI PR needs a captured frame — #110/#468),
`gh-comment-gate.sh` (only `post-verdict.sh` may post a Verdict/Review-Lane
comment — #188), `mark-pr-external.sh` (gated: record a PR as authored
outside the lane system).

### Invariants — do not break these without an explicit decision

1. **The ledger is the record; tmux is the screen.** Anything *decided and
   remembered* (availability, ownership, which conversation belongs to a lane)
   belongs in `ledger.sqlite3`. Anything *observed right now* (busy, blocked,
   scrolled) is read from the pane. The test is authorship: did this system
   write the value, or did tmux produce it as a byproduct?

2. **Write the durable fact before the pretty label.** `lane-done.sh` releases
   the ledger, then renames the window. The reverse order strands a lane
   permanently on a crash; this order leaves only a stale label.

3. **Restore refuses rather than invents.** A lane that cannot be brought back
   with its own conversation is reported `UNRECOVERABLE` (exit 2) and left
   alone. A fresh agent wearing a recovered lane's name looks fully healthy and
   has none of the context — it is the worst available outcome.

4. **Never address the default tmux socket in a test.** `kill-server`,
   `kill-session`, `kill-window` and `respawn-*` must be scoped with
   `TMUX_TMPDIR` and gated by `assert_isolated_tmux` (`tmux-isolation.sh`). A
   bare `tmux kill-server` from a lane destroyed the entire live estate three
   times in one day, including unrelated sessions belonging to the operator.
   The rule is not "never call it" — one test harness calls it sixteen times
   safely. `Verified 2026-08-15` (#185): the guard was extended to session
   *creation* too, not just the destructive verbs above — an isolated test
   that creates a session without `TMUX_TMPDIR` set was the same class of
   leak with a different verb.

5. **Address windows by `window_id` (`@7`), never by index.** Killing window 4
   renumbers 5 into 4. A loop killing indices hits shifting targets; that
   destroyed the Telegram poller.

6. **`unknown` means "not offered", not "broken".** `lanes.sh` is a whitelist:
   only a recognised idle shape is offered as free. Handing work to a lane you
   cannot read is worse than leaving it idle. Do not "improve" this into a guess.

7. **`lanes.sh` holds no harness-specific string.** Harness knowledge lives in
   `harness/*.sh`. Widening a regex to cover another harness is the wrong fix —
   it lets one harness's shapes falsely match another's.

8. **The poller is a service, not a lane.** Never dispatch to it, never "restart"
   it as a lane. It consumes Telegram messages by acking the offset, so running
   the inbox by hand returns nothing — that is not evidence nobody wrote.

9. **Lane identity is the string `<session>:<index>`, e.g. `agent-supervisor:3`
   — not the task, not the worktree, not the window name.** A lane that
   finishes one task and is dispatched a second, in a different worktree, is
   still the *same lane*. This was used to justify treating a review as
   independent on 2026-08-15 and was wrong: same `<session>:<index>` across a
   rename is a self-review, and `verdict-independence.sh`'s `lane_relation`
   check exists specifically to catch it — see its own comment on "the same
   window, renamed session" (#184/#192/#196/#198). Compare lane ids, never
   task ids or window names, when deciding whether two pieces of work were
   done by the same agent.

10. **A lane identifies itself by matching the ledger's `worktree_path`
    against its own `cwd`, not by asking tmux who it is.** `tmux
    display-message` **without an explicit `-t`** answers for the session's
    *currently active* window, not the caller's own — a background or
    non-focused pane gets someone else's answer. That produced six
    mis-stamped `Review-Lane:` trailers in one day (#187) before the merge
    gate's independence check caught them as suspicious self-reviews (see
    invariant 9) rather than silently trusting them.

    A brief should never spell this out as a raw command — name
    `lane-whoami.sh` (index above, #685), which already picks the right
    branch (pane vs. pane-less) and never calls `display-message` with no
    `-t`. The self-lookup it wraps for the pane-less branch is `cli.py
    worktree-lane --path "$(pwd)" --include-reviews`
    (`Ledger.get_task_for_worktree(..., include_reviews=True)`). The
    `--include-reviews` flag matters: `worktree-lane` defaults
    to `False` because its real caller, `dispatch.sh --reviews-pr`, is
    asking a DIFFERENT question — "who could plausibly have AUTHORED this
    PR?" — and a review task can never be its own PR's author (#76), so the
    default filters review-shaped tasks out. A REVIEWING lane's own
    worktree is legitimately parked on a task that looks like a review, so
    that same filter answers `known:false` for a row the ledger actually
    has if the flag is left off — measured directly (#212), from the exact
    situation #187 was about, before this flag existed:

    ```
    $ cli.py worktree-lane --path "$(pwd)"
    {"known":false,"lane":null,"path":".../ad-211-rev212-14268","task":null}
    $ cli.py worktree-lane --path "$(pwd)" --include-reviews
    {"known":true,"lane":"agent-supervisor:6","path":"...","task":"as211-rev212"}
    ```

    Do not remove the default filter to "fix" this — that reintroduces #76
    (a review task answering as a PR's author). The flag exists so the two
    questions share one lookup without sharing one answer.

### The failure mode this codebase produces most

**An instrument that cannot see a thing looks exactly like the thing being
absent.** Before reporting "none", "empty", "never" or "not called":

- Check the whole tree, not one file. `grep`ing a single script and concluding
  "nothing calls this" was wrong when the callers were one file away.
- Test *tracking* (`git ls-files`), not directory existence. A gitignored
  `__pycache__` made a completed deletion look incomplete.
- Capture exit codes directly. `cmd | tail` gives you `tail`'s status.
- Verify a mutation applied before believing the result it produced.
- Cite **functions and behaviours, never line numbers.** A comment citing its own
  callers by line was already wrong in the diff that added it.

### Two more, learned expensively

**A tool that fails closed and that nothing calls is a documentation rule with a
binary attached.** After building anything protective, ask both *what calls it?*
and *is that caller something that survives the failure it guards against?* A
cleanup invoked from the crashing process is wired to the one thing that will not
be running. (`count-agents.sh`, in the index above, is a live instance of the
first question right now — it exists, it is tested, and as of this commit
nothing calls it.)

**An abstraction can be present and correctly avoided.** Routing around a seam
looks identical to nobody having wired it up. Check whether the avoidance is
documented before "fixing" it — the reason belongs next to the seam.

**A merged fix that never reaches the process running it looks identical to an
unfixed defect.** `agent-supervisor#308` was reported broken three times
(#331, #333, then this same report a third time) with the same live
`bash -x` transcript: `cli.py pr-task` reported as an "invalid choice". Every repair
attempt found `pr-task` already implemented and working on `origin/main` —
because it was: added by #321/`a2d8a80`, before either later PR. The
transcripts were real; the repo was never the thing they were measuring.
The Director runs `scripts/supervisor/` out of the shared checkout at
`/Users/jon/source/repos/Personal/agent-supervisor`, which had fallen 13
commits behind `origin/main` (stuck at `876edb1`, predating #321 entirely)
and was carrying its own uncommitted staged/unstaged changes. A code-level
drift guard — #333's `test_resolve_pr_contributors_subcommands.sh` — can
only check that the tree it runs in is internally consistent; it has no way
to see that a *different* checkout, never pulled forward, is the one
actually executing production traffic. Before re-diagnosing a "still
broken" report against `origin/main` as a code defect, check what checkout
the report was actually measured against and how stale it is — a green
guard and a red transcript can both be telling the truth at once.

### Conventions

- Branch with a type prefix; never commit to `main`.
- One independent review per PR, by someone who did not write it — including
  fixup commits. `dispatch.sh --reviews-pr` asks the ledger, not the branch
  name, and `dispatch.sh --pr` supports dispatching a review or a fix pass
  scoped to a PR directly rather than only to the issue it closes (#169,
  `Verified 2026-08-15`).
- This is enforced at merge, not just at dispatch: `merge-pr.sh` is the only
  path that should merge a PR here, and it refuses to merge one whose author
  lane and reviewer lane are the same (invariant 9) or whose verdict cannot be
  read (`Verified 2026-08-15`, #184/#196/#198). `gh pr merge` run directly
  bypasses this — it is convention, not a platform block, same as the CI gate
  above.
- One fix pass. If a PR fails a second review, close it and file what remains.
- Cheaper model tiers for workers and reviewers; reserve the expensive tier for
  judgement.
- Anything touching tmux behaviour runs against an isolated socket or on a
  throwaway host — never the machine you are working on.
- A UI PR (touching `scripts/supervisor/laneview/`) needs a captured frame,
  not a description, as evidence — `look.py capture`/`look.py frames` (#110).
  This is enforced, not just written down here: `.github/workflows/ui-evidence.yml`
  runs `ui-evidence-gate.sh` on every PR and fails one that touches that path
  without a `<!-- ui-evidence:v1 -->` marker in the body or a comment.

---
*Last checked against the tree at `b083ca4` (2026-08-27). If `git log
--oneline scripts/supervisor | head -1` names something newer than that and
this file hasn't moved, treat any specific claim above as unverified until
you re-check it — don't trust it just because it's written down.*

## The TUI

Arrival policy for the TUI half of this repo, everything under `tui/`.

**Verified against `main` `6942926`, 2026-08-23 (agent-tui#38's shell PR,
agent-tui#43, plus everything through agent-tui#104's chat-send wiring), except the "What this
repo is" paragraph and this file's `internal/nav`/`internal/rail`/
`internal/shell`/`internal/stub` layout lines, updated 2026-08-22 for
`docs/tui/SPEC-shell.md` S1-S3/S5.** The "Known defects" section below was
re-verified separately against `6942926` on 2026-08-23 (agent-tui#49 closed).
Confirm the branch/SHA in `git log -1` still matches before trusting counts
below; they are measured, not estimated.

**Re-verified against `main` `390c99a`, 2026-08-23 (estate-loop/b-docs-stale,
docs-stale-sweep worktree).** Re-checked in this pass: the Layout table below
(missing package rows added — see the table itself for what was added), the
`agent-tui` grep count in the naming note (re-measured, see below), and the
"Running the tests" section's no-tests claim (re-measured, see that section).
The Known defects section's `6942926` re-verification above still stands
unchanged; not re-walked in this pass.

**Naming: superseded, 2026-08-23. The product is the Estate**, binary
`estate`. Full history, kept rather than erased, because a naming decision
worth recording once is worth recording twice:

Round one (agent-tui#42, seven rounds, ~60 candidates checked): Jon
rejected `keelson` (real collision: `akapril/keelson`, a near-identical
local-first AI-session workbench) and said to keep looking for "an untaken
gem" before falling back to `loom` (which collides with three separate
agent orchestrators, 12–74 stars each). `steading` was that gem: `gh api
users/steading` and `gh api repos/jonhill90/steading` both 404 (free),
`npm view steading` 404s (free), and `gh search repos steading` returned
zero purpose collisions — verified 2026-08-20, the day it was applied.
`steading.com` and `steading.dev` were registered by then (a real cost, not
disqualifying — GitHub org, `jonhill90/<name>`, npm, and
search-purpose-collision are the signals that discriminate real conflict
from mere squatting). A steading is a farmstead and all its
outbuildings — the whole working holding, not a single machine — which
matched what this product is (rail, board, cost, gallery, memory, chat,
workflows) better than a renderer-technology name. That round was
deliberately a **naming decision, not a rename**: the Go module, `cmd/`
directory and binary stayed `keelson` — a leftover of agent-tui#38's
overnight rename pass — and the GitHub repo stayed `jonhill90/agent-tui`,
both on purpose, because mixing the naming call with the mechanical rename
would have made that PR unreviewable. A `TODO(rename)` was left for the
follow-on mechanical move, explicitly not done in that pass.

Round two, today: Jon retired `keelson` outright (he named it as something
he actively dislikes) and chose **the Estate** over bare "Estate" and over
"Agent Estate" — `gh search repos agent-estate` returns
`brightdata/real-estate-ai-agent`, `prolinkinfo/RealEstateCRM`,
`liberusoftware/real-estate-laravel`; "estate agent" reads as *realtor* in
British English to anyone outside this estate, which "Agent Estate" would
too. This is the mechanical rename round one's own `TODO(rename)` asked
for — module path, `cmd/` directory, and binary — done in one PR
(agent-tui#117's go-public checklist forced the timing: publishing pins the
module path, and renaming after the flip breaks any consumer import),
landing on `estate` rather than `steading`, because `steading` is retired
by the same decision that retires `keelson`. Prose in this repo's docs now
says "the Estate" (capital E, lowercase article) wherever it said
`steading`; code identifiers use `estate`. Issue references below keep the
`agent-tui#NN` form because that is the repo they point at. Measured cost: `git grep -o -i agent-tui | wc -l` on this
branch, 2026-08-20 — 489 occurrences across 81 tracked files (`git grep -l
-i agent-tui | wc -l`), up from round 1's 438/72. **Re-measured 2026-08-23
against `390c99a`: 696 occurrences across 154 tracked files** — the repo has
grown substantially since the 2026-08-20 count (new `internal/admin`,
`internal/agents`, `internal/connectors`, `internal/dashboard`,
`internal/knowledge`, `internal/library`, `internal/mcpservers`,
`internal/navwalk`, `internal/skills`, `internal/prverdict`,
`internal/secrets`, `internal/mergepr` packages and their `cmd/` entry
points, each carrying its own `agent-tui#NN` issue references), not a
retraction of the earlier count.

### What this repo is

This repo (Go module `github.com/jonhill90/agent-tui` — see the naming note
above) is one terminal application: a left nav sidebar modelled 1:1 on the
hill90 web app's own nav (`internal/nav`, `docs/tui/SPEC-shell.md`), with the
task board, cost panel, glyph gallery and the lane rail over
`agent-supervisor`'s lane/session state all reachable as routed panes in the
same process (`internal/shell`, agent-tui#38; the nav sidebar replacing the
rail as the fixed left column is `docs/tui/SPEC-shell.md`'s S3). The name
`agent-tui` describes the rendering technology (Go +
[Bubble Tea](https://github.com/charmbracelet/bubbletea)), not the product —
the product's name is the Estate (see the naming note above).
It is a **viewer with one write path** (session
attach/detach/add/remove, see below) — same discipline as
`agent-supervisor`'s own `scripts/supervisor/laneview/`. It never shells out
to `tmux` directly, never reads or writes the ledger except through the
adapters listed below, and never reimplements `ccusage`'s or `lanes.sh`'s
parsing.

Read `README.md` for what has shipped, `docs/tui/PRD.md` for what the product is
for, and `docs/tui/SPEC.md` for the technical design. This file is arrival
policy only.

### What belongs here vs. `agent-supervisor`

- **Here:** rendering, layout, glyph/theme data, keybindings, anything that
  turns supervisor state into pixels a human reads. The one exception is the
  session write path (`internal/session`), which is a thin MCP call wrapper
  with zero tmux knowledge of its own.
- **In `agent-supervisor`:** tmux orchestration, the ledger, dispatch, the
  MCP server itself (`scripts/supervisor/mcp_server.py`), and any logic that
  decides whether an operation is *safe* (e.g. `session_remove_check`'s
  refusal rules). If a change requires knowing tmux client identity, session
  guard logic, or ledger schema, it is a supervisor change with an agent-tui
  caller added after, not the reverse.
- **Never here:** a second reader of tmux, a second ledger, a fabricated
  metric (a cost or quota figure invented because the real source returned
  nothing — see `internal/cost.Figure`'s `Known` field for the pattern this
  repo uses everywhere data may be absent).

### Layout

```
cmd/estate/         one tea.NewProgram entry point, running internal/shell.Model (see docs/tui/SPEC.md)
internal/admin/      Admin section -- Services/Profiles/Users/Dependencies/Settings, read-only first (SPEC-shell.md S11)
internal/agents/     Agents view -- id, model, state, current task, cost, assembled from the same seams internal/rail already reads (SPEC-shell.md S6)
internal/apidocs/    Docs -> API Docs -- hill90-app's own OpenAPI document as an operation table
internal/board/      task board projection — GitHub issues/PRs + ledger tasks + live lanes
internal/chat/       ACP thread chat -- Source/Sender seams, ClaudeCodeSource + FallbackSource (agent-tui#99) with FixtureSource as last resort, two viewport-scrollable layouts (agent-tui#20)
internal/connectors/ Connect group -- provider connections and models, mirrors web Connect (SPEC-shell.md S10)
internal/cost/       per-harness spend/quota projection from ccusage
internal/dashboard/  estate-at-a-glance view -- re-projects figures already established by internal/agents/internal/cost/internal/knowledge plus a small gh read of its own
internal/external/   Docs -> Platform Docs -- how a nav.KindExternal destination behaves (names the URL, opens a browser)
internal/flow/       live flow view — the same board.Snapshot re-projected as a moving pipeline (agent-tui#64)
internal/gallery/    glyph gallery — every lane state × every candidate glyph set
internal/knowledge/  Jon's personal memory vault viewer -- reads $AGENT_MEMORY_VAULT's agent/index.md + agent/facts/<slug>.md, progressive disclosure (agent-tui#87)
internal/lane/       lane/session decode, glyph sets (data, not code), state table
internal/library/    shared prompt/decision corpus viewer -- agent-dotfiles-supervisor's ledger.sqlite3 live_parameters/open_questions/unacknowledged views (w5c.md)
internal/mcp/        minimal MCP JSON-RPC client over a child process's stdio
internal/mcpservers/ configured MCP servers -- name, scope (global/project), reachability (SPEC-shell.md S9)
internal/mergepr/    merge-time gate for this repo -- chains the CI gate and internal/prverdict's comment-verdict gate, fails closed, then calls gh pr merge (agent-tui#109)
internal/monitor/    host health (load/swap/process count) + agent state counts (w5f.md, Observe -> Monitoring)
internal/nav/        the 1:1-with-hill90 nav tree + sidebar component -- now the fixed left column (SPEC-shell.md S1-S3)
internal/navwalk/    one JSONL file per nav destination, replacing the single hand-merged tui/testdata/vhs/full-nav-walk-report.md (agent-b3.md)
internal/prverdict/  reads a PR's own comments and decides whether it carries an independent, current APPROVE -- Go port of skills#255's pr_verdict.py
internal/rail/       the lane rail -- content behind the sidebar's "Lanes" route (PaneLanes) since SPEC-shell.md S3/S4, no longer a fixed column
internal/secrets/    Connect -> Secrets -- levels 1-4 of agent-tui#101's exposure scale from hill90-app's secrets-schema.yaml, never level 5 (the value)
internal/session/    write path: attach/detach/add/remove/send, all via MCP, no os/exec
internal/shell/      the application shell -- owns the sidebar (internal/nav) + ~20 routed panes (agent-tui#38, #64, #20; SPEC-shell.md S3)
internal/skills/     skills view -- name, description, last eval result, invocation count, from ~/.claude/skills (SPEC-shell.md S8)
internal/sshserver/  serves shell.Model over SSH via charmbracelet/wish (agent-tui#67) -- one Model per connection
internal/stub/       honest "not built yet" placeholder for any nav route with no real pane wired (SPEC-shell.md S5)
internal/theme/      look-and-feel as data — Role-keyed colours, persisted per-user config
internal/workflows/  ledger dispatch history -- a task's own path through the estate (w5f.md, Build -> Workflows)
scripts/tui/         verify-lanes-unaffected.sh — the rail's non-interference proof (rail's own render/key logic is unchanged by SPEC-shell.md S3; only its screen position moved)
```

`cmd/` also now has `cmd/demo`, `cmd/fakemcp`, `cmd/mergepr`, `cmd/navwalk`
and `cmd/prverdict` alongside `cmd/estate` — the CLI entry points for
`internal/mergepr`, `internal/navwalk` and `internal/prverdict` above, plus
a demo harness and a fake MCP server used by tests. None of the five is a
second `tea.NewProgram` site (see "What NOT to do here" below); they are
plain CLI commands.

`internal/chat` is wired into the shell as `PaneChat` (`[f6]`, agent-tui#20) --
`[f5]` was already claimed by `internal/flow`'s `PaneFlow` (agent-tui#64) by
the time this rebased onto it.
It renders against `chat.Source`, an adapter seam the same shape as
`rail.Fetcher`; `chat.FixtureSource` is the only implementation shipped
today because no lane in this estate runs on a structured transport
(`acp`/`pi-rpc`) yet — see `internal/chat/fixture.go`'s own doc comment for
why a screen-scraped transcript was rejected instead, and what a real
`Source` needs.

**False as of `56513a2`, corrected 2026-08-23 (estate-loop/b-docs-stale
sweep, pass 2) — the Layout table row above this paragraph was already
fixed by the prior sweep pass (agent-tui#119); this specific paragraph was missed.**
`agent-tui#99` (commit `5997399`) shipped `internal/chat/claudecode.go`'s
`ClaudeCodeSource`, which reads real Claude Code CLI session transcripts,
and `internal/chat/fallback.go`'s `FallbackSource`, which `cmd/estate`
wires in: try `ClaudeCodeSource` first, fall back to `FixtureSource` only
when the real source reports itself genuinely unconfigured.
`FixtureSource` is the last-resort fallback now, not the only
implementation. Sending is also built (`agent-tui#104`, commit `6942926`)
and Chat is a multi-participant room with `@`-mention addressing
(`agent-tui#114`, commit `a0ad626`) — see `docs/tui/SPEC-shell.md`'s S7 for the
fuller history.

### Adapter discipline

Every package that touches the outside world is behind a function-typed or
interface-typed seam, supplied by `cmd/estate/main.go`:

| seam | package | what it hides |
|---|---|---|
| `rail.Fetcher`, `rail.SessionsFetcher` | `internal/rail` | the MCP `lanes`/`sessions` tool calls |
| `session.Interface` | `internal/session` | attach/detach/add/remove, each one `mcp.Client.CallTool` |
| `cost.Fetcher` (built in `cmd/estate/cost.go`) | `internal/cost` | shelling out to `ccusage` |
| `board.Fetcher`-shaped functions (`cmd/estate/board.go`) | `internal/board` | `gh` CLI calls and a read-only `sqlite3` ledger open |
| `theme.Theme` / `theme.Load` | `internal/theme` | every colour, border and chrome literal |
| `chat.Source` | `internal/chat` | ACP `session/update` thread content -- **false as of `56513a2`, corrected 2026-08-23 (pass 2): `chat.ClaudeCodeSource` + `chat.FallbackSource` are the real implementations shipped (agent-tui#99); `chat.FixtureSource` is now only the last-resort fallback, not "today's" source** |

**Why this matters practically:** every package's tests construct a fake
implementing the seam, not a real subprocess. If you add a feature that needs
new external data, add it as a new field on an existing seam or a new
function-typed seam — never an `os/exec.Command` inside `internal/*` directly.
`internal/mcp` is the only package that knows it is talking to a subprocess;
everything above it knows only Go types.

### Running the tests

```
go build ./...
go vet ./...
go test ./...
```

All three verified green on `main` `6942926` (29 packages with tests,
`cmd/estate`, `internal/sshserver`, and the `tools/` spikes have none).
**Stale as of `390c99a`, re-measured 2026-08-23 (`find . -name '*_test.go'`):**
`cmd/estate` now has five `_test.go` files (`ledger_copy_test.go`,
`cost_test.go`, `docs_test.go`, `secrets_test.go`, `supervisor_test.go`) and
`tools/memoryvariants/spike` has one (`main_test.go`); only
`internal/sshserver` still genuinely has none. CI
(`.github/workflows/*.yml`) runs the same three
commands on `ubuntu-latest`, Go 1.26, plus a fourth check gated on a live
`agent-supervisor` checkout: `internal/lane/states_lanessh_test.go`
cross-checks `lane.AllStates` against `lanes.sh`'s own `state=` assignments
when `$AGENT_SUPERVISOR_REPO` is set, and skips otherwise — this repo must
still build and test standalone with no supervisor checkout present.

To run the app against a real supervisor:

```
go build -o estate ./cmd/estate
AGENT_SUPERVISOR_REPO=/path/to/agent-supervisor ./estate
```

The board, cost and gallery screens are panes reached with `[f2]`/`[f3]`/
`[f4]` inside the one running process (`internal/shell`, agent-tui#38);
`-board`/`-cost`/`-gallery` now only choose which pane the app opens on.

**A binary that builds is not a feature that works.** `go test` exercises
`Model.Update` with synthetic key messages against fakes; it does not press a
key against a live tmux session. Before documenting a control as working,
either cite the test that drives it through `Update` (name it) or say
"not verified against a live session."

### Merging PRs you did not author

When more than one agent lane works this repository at once, every lane
pushes through the same shared GitHub login — `gh pr review --approve` is
refused as self-review regardless of who is actually asking, so a real
cross-lane review has to be recorded another way: a reviewing lane posts a
plain PR comment, not a GitHub review object, carrying

```
Verdict: APPROVE            (or REQUEST CHANGES, with specifics)
Review-Lane: <reviewing lane's own name>
Reviewed-SHA: <the exact head commit SHA reviewed>
```

and the PR's own body states which lane opened it:

```
Author-Lane: <authoring lane's own name>
```

**`cmd/mergepr` is THE way to merge a PR in this repo. Do not `gh pr merge`
directly, and do not run `cmd/prverdict` as a manual pre-check and then
merge by hand** — that is exactly the gap agent-tui#109 recorded: a tool
nobody is told to use is exactly how agent-tui#107 happened (a
comment-verdict gate merged by its own author, unreviewed, within
minutes — the second confirmed instance of that anti-pattern after
`jonhill90/skills#255`'s own). `cmd/mergepr` is modelled directly on
`agent-supervisor`'s own working pattern
(`scripts/supervisor/merge-pr.sh` + `ci_gate.py`): it chains a CI gate and
the comment-verdict gate itself, fails closed on either, and only then
calls `gh pr merge` — the same "cannot be skipped by habit" role
merge-pr.sh plays there.

```
go run ./cmd/mergepr -repo <owner/name> -number <N>
go run ./cmd/mergepr -repo <owner/name> -number <N> -- --squash --delete-branch
```

Exit `0` means it merged. Exit `1` means a gate refused (CI not green at
the current head, or `internal/prverdict`'s gate did not resolve to a
genuine cross-lane approval — the refusing gate's own reason is always
printed to stderr) or `gh pr merge` itself failed; nothing was merged
either way. Exit `2` is a usage error. See `internal/mergepr`'s own
doc comment for exactly what the two gates check, and
`internal/prverdict`'s doc comment for the comment-verdict gate
specifically — a Go port of `jonhill90/skills#255`'s `pr_verdict.py`,
itself ported from `jonhill90/agent-supervisor`'s
`verdict.py`/`verdict-independence.sh` (this repo is Go-only, AGENTS.md's
own "Go, not shell, for new code" convention, so the port is Go rather
than a second-language copy of skills#255's Python). `390c99a` (agent-tui#113)
fixed a blank-`Review-Lane:`-trailer self-approval bypass in this gate: a
same-lane author posting a comment with an empty `Review-Lane:` value and
a real head SHA on the next line was previously resolved as `approved`
because the post-colon regex's greedy whitespace consumed the newline and
captured the next line's text instead of an empty string; it now resolves
to `unknown` with an explicit "no Review-Lane: trailer" reason — see
`internal/prverdict`'s own `BlankReviewLaneSelfApprovalBypass` regression
test.

**Not wired into CI, deliberately.** This repository's own CI
(`.github/workflows/ci.yml`) builds, vets and tests every push and PR; it
never merges one — merging is always a separate command an operator or an
agent lane runs directly, outside any workflow. There is no merge-time CI
job to attach this gate to without inventing one that does not otherwise
exist; `cmd/mergepr` is the command that invocation must be, by convention
stated here, the same posture `jonhill90/skills` took for the same
structural reason. Nothing on GitHub's side stops a caller from running
`gh pr merge` directly instead and skipping both gates entirely — the same
residual `merge-pr.sh`'s own doc comment states for `agent-supervisor`,
stated here rather than left implicit.

### Conventions

- **Code comments cite functions and behaviours, never line numbers.** A
  comment naming a caller by line number is wrong the moment the file is
  next edited. Existing comments in this repo already follow this — match
  it.
- **Every seam is a `func` type or a small interface, not a concrete
  dependency.** See "Adapter discipline" above.
- **Absence is a typed value, never a bare zero.** `cost.Figure.Known`,
  `theme.Load`'s notice string, `session.Worktree.Clean *bool` (nil is a
  third state, not false) are the pattern: a caller must be able to tell "we
  looked and it's zero" from "we could not look." Follow it for any new data
  that might be unavailable rather than absent.
- **Dated claims.** Any doc comment or README line asserting something is
  true today, not merely intended, should be checkable against a commit SHA
  or a test name. This file and its siblings under `docs/` carry a `Verified
  <UTC>` stamp at the top; update it when you re-check the claims below it,
  don't just edit the prose.
- **Glyph sets and themes are data, not code** (`internal/lane/variants.go`,
  `internal/theme/registry.go`) — a new visual variant is a struct literal
  addition, never a new code path in a render function.

### Known defects — do not paper over these

agent-tui#49 is **closed** (2026-08-16). All three of the defects it
originally recorded are fixed as of `6942926`, 2026-08-23 — re-confirmed by
running the actual binary and by grep, not by memory of the issue text:

1. ~~**Bare launch exits 1.**~~ Fixed. `./estate` with no flags and no
   `$AGENT_SUPERVISOR_REPO` now opens in a degraded state on the Home pane
   instead of exiting (`cmd/estate/main.go`'s `supervisorRepoResolved`
   handling, commented "agent-tui#49 item 1: a bare `estate` must open,
   never exit 1" — comment text updated for the rename; the issue's own
   original wording said `keelson`). Confirmed by running the built binary
   under a real TTY (`script -q ... ./estate`): it renders the sidebar and
   Home pane rather than printing the old `no supervisor to connect to`
   message and exiting.
2. ~~**The board pane reports itself unavailable with no `-ledger`.**~~
   Fixed. `resolveLedgerSource` (`cmd/estate/board.go`) now auto-discovers
   and stages a copy of the live ledger when `-ledger`/`$AGENT_TUI_LEDGER`
   is unset (`defaultLedgerLivePath` + `newLedgerCopier`); the old hard
   `boardOK == false` refusal only fires now when discovery genuinely finds
   nothing, not merely because the flag was omitted.
3. ~~**The cost panel's quota line is unwired from the current quota
   source.**~~ Fixed. `internal/cost/quota.go` now shells `quota.sh` out via
   `QuotaRunner`/`ExecQuotaRunner`, wired from `cmd/estate/main.go`'s
   `resolvedQuotaBin` (`<supervisor-repo>/scripts/supervisor/quota.sh`).
   `renderQuota`'s `unknown (no quota source)` string (`internal/cost/
   view.go`) is now the honest fallback for a genuinely missing/failing
   `quota.sh`, not a structurally unwired source — confirmed by `grep -rn
   "quota.sh" --include='*.go' .`, which now returns matches throughout
   `internal/cost` and `cmd/estate`.

This section is now a clean bill of health for agent-tui#49, not an open
punch list — if a regression reopens any of the three, restore the numbered
form above with fresh confirmation evidence rather than editing this prose
in place.

### What NOT to do here

- Do not add a new `tea.NewProgram` call site. `internal/shell.Model` is the
  one program now (agent-tui#38); a new view is a pane added to the shell,
  never a second program selected by a launch flag — the mistake a fifth
  flag would have repeated, which `lane/20-chat-threads` explicitly declined
  to make.
- Do not call `os/exec` for tmux from any package under `internal/`. Every
  tmux-adjacent operation is a supervisor MCP tool call.
- Do not restore `[a]ttach`/`[d]etach` in the rail without the client-identity
  fix tracked at `agent-supervisor#189`. They were removed in `3137206`
  because MCP's stdio transport gives the supervisor no way to know which
  tmux client is asking, so `switch-client`/`detach-client` acts on an
  arbitrary attached client while reporting success. `session.Interface`
  still declares both methods; nothing in `internal/rail` or
  `internal/shell` calls them as of `6942926` (`grep -rn "\.Attach(\|\.Detach("
  --include='*.go' .`, outside test files: zero matches).

  **Status update, 2026-08-23 (estate-loop/b-docs-stale sweep, pass 2):**
  `agent-supervisor#189` is now **closed** (`stateReason: COMPLETED`,
  2026-08-16), fixed by `agent-supervisor#202` ("session_attach/
  session_detach name which tmux client acts, and refuse to guess",
  merged). The supervisor-side prerequisite this bullet names is resolved —
  the blocker is no longer "the fix does not exist." What's still true,
  re-confirmed live against `56513a2`: `grep -rn "\.Attach(\|\.Detach("
  --include='*.go' .` outside test files is still zero matches — agent-tui
  itself has not added a caller since `agent-supervisor#202` landed.
  Restoring the keys is now unblocked on the supervisor side but still not
  done here; do not read this note as permission to restore them without
  checking `agent-supervisor#202`'s own shape first.
- Do not point `-ledger` at the live supervisor's `ledger.sqlite3`. It is
  always opened read-only, but the flag help and `internal/board/ledger.go`
  both document why a copy is still required.
