# agent-supervisor — agent orientation

*(`AGENTS.md` and `CLAUDE.md` are the same file — one is a symlink, so there is
no second copy to drift.)*

**This file is an index, not a document to read end to end.** Find your task
below, open the one or two files it names, and stop there. Every claim in it
(path, script, flag) was checked against the tree at the commit named at the
bottom — re-check anything you're about to rely on if it's been a while since
that commit, the same way this rewrite had to.

## Before you ask Jon anything — read this first

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

## Index — what to open for which task

`scripts/supervisor/` has 100+ tracked files (`git ls-files scripts/supervisor
| grep -cE '\.(sh|py)$'`, checked at write time). This groups them by the job
they do, one line each, saying **what each decides**, not what it is. It is
not a substitute for the file's own header comment — every file here has one,
and it is longer and more specific than this line.

**Dispatch & lane lifecycle** — hand work to a lane, or bring one back:
- `dispatch.sh` — pick a free lane, claim the issue, create its worktree, send
  the brief; supports `--adopt-pane <window-id>` to hand the brief to an
  already-running idle pane's own process instead of spawning a new one
  (#668/#677)
- `dispatch-claude-print.sh` / `dispatch-pi-rpc.sh` — `dispatch.sh`'s siblings
  for the `claude -p` and `pi --mode rpc` harness shapes, not replacements
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

`tests/supervisor/` — 199 tracked files (`git ls-files tests/supervisor | wc
-l`, checked at write time; re-run, don't trust this number stale): the suite
is the contract. `python3 -m unittest discover -s tests/supervisor` has not
reliably finished inside one working session's time budget; run a targeted
test file, not a full discovery, when you only touched one thing.

## The guards a lane will actually hit

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

## Invariants — do not break these without an explicit decision

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

## The failure mode this codebase produces most

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

## Two more, learned expensively

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

## Conventions

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
*Last checked against the tree at `1e2d4ae` (2026-08-27). If `git log
--oneline scripts/supervisor | head -1` names something newer than that and
this file hasn't moved, treat any specific claim above as unverified until
you re-check it — don't trust it just because it's written down.*
