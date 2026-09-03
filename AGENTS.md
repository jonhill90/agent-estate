# agent-estate — agent orientation

*(`AGENTS.md` and `CLAUDE.md` are the same file — one is a symlink, so there is
no second copy to drift.)*

**This file is an index, not a document to read end to end.** Find your task
below, open the one or two files it names, and stop there. Every claim in it
(path, script, flag) was checked against the tree at the commit named at the
bottom — re-check anything you're about to rely on if it's been a while since
that commit. Longer rationale, history, and evidence than a "which file"
index needs lives under `docs/` and in each file's own header comment, linked
from here rather than inlined — this file drifts less if it says less.

This repo is two halves merged under migration Step 2b/2c (#682, #744): the
daemon (`agent-supervisor`, everything at the root outside `src/tui/`) and
the TUI (`agent-tui`, everything under `src/tui/`, moved there from `tui/`
by #865). Each keeps its own orientation section below rather than being
blended into one narrative — the two had separate framing before the merge
and still do.

## The daemon

> **Read this section as history, not as a map.** Almost everything it names
> under `scripts/supervisor/` and `tests/supervisor/` was deleted to
> `reference/`; `git ls-files scripts/supervisor` and `git ls-files
> tests/supervisor` both return **0**. The daemon today is Go, in
> `src/estate`, and is a much smaller surface. Run `go run ./src/estate` with
> no arguments for the current list rather than trusting one written here —
> it is growing, and a list in prose goes stale between commits.
>
> The RULES below are still what this estate believes, and that is why the
> section survives — but **check any specific file, flag or script against
> the tree before relying on it**, and expect most of them to be gone.
> Rewriting this section properly is open work, named in
> `docs/director-brief.md` §9. What follows the next heading — how to treat
> the corpus and when a question may reach Jon — is current and binding.

### Before you ask Jon anything — read this first

Jon has stated this more than twenty times. It is a **hard** parameter in his
corpus and it keeps being broken, so it goes at the top of the file rather than
somewhere polite.

**Exhaust the record before a question reaches him**, in this order:

1. **Query the corpus.** `~/corpus/ledger.sqlite3` — the same path
   `internal/corpus.Path()` resolves for every dispatch's own grounding, so
   the two cannot drift apart unnoticed
   (`src/estate/internal/corpus/agents_md_test.go` fails the build if they
   do). Measured read-only 2026-09-03: 5,403 prompts, 1,104 live hard
   constraints in `live_parameters` (re-run the count yourself before citing
   it further — it grows). Views: `live_parameters`, `open_questions`,
   `unacknowledged`, `possibility_count`.
   `~/.local/state/agent-dotfiles-supervisor/ledger.sqlite3` is a **different,
   nearly-empty database** (0 live parameters measured the same day) — do not
   query it for this rule (agent-estate#942).
   If you have not queried the corpus this session, you have not earned the
   question.
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

`scripts/supervisor/` has 147 tracked `.sh`/`.py` files (`git ls-files scripts/supervisor
| grep -cE '\.(sh|py)$'`, checked at write time). This groups them by the job
they do, one line each, saying **what each decides**, not what it is. It is
not a substitute for the file's own header comment — every file here has one,
carries the "why" (issue numbers, incidents, dates), and is longer and more
specific than this line.

**Dispatch & lane lifecycle** — hand work to a lane, or bring one back:
- `dispatch.sh` — pick a free lane, claim the issue, create its worktree, send
  the brief; `--adopt-pane <window-id>` hands the brief to an already-running
  idle pane's own process instead of spawning a new one. Split into an
  ~280-line composition root plus 8 sourced-only siblings, grouped by step:
  `dispatch-rehome.sh`, `dispatch-args.sh`, `dispatch-preflight.sh`,
  `dispatch-guards.sh`, `dispatch-lane-select.sh`, `dispatch-worktree.sh`,
  `dispatch-send.sh`, `dispatch-record.sh` — none is meant to run standalone,
  and sourcing order is execution order.
- `dispatch-claude-print.sh` / `dispatch-pi-rpc.sh` — `dispatch.sh`'s siblings
  for the `claude -p` and `pi --mode rpc` harness shapes, not replacements,
  and not part of the `dispatch-*.sh` split above — excluded by name from
  `tests/supervisor/_dispatch_mutate.py`'s `SPLIT_FILES` list since they
  carry their own copies of the same bash-3.2-safe idioms.
- `claim.sh` — claim an issue on GitHub before dispatch, so two dispatchers
  can't both hand out the same one
- `worktree.sh` — one git worktree per lane task
- `collision-check.sh` — refuse a dispatch whose files overlap an in-flight
  lane's (whole-file overlap only)
- `host-pressure.sh` / `host_pressure.py` — refuse a NEW dispatch when the
  host can't safely take it; exit 2 (not 0/1) means "could not measure",
  refused rather than guessed
- `count-agents.sh` — counts real Claude agent SESSIONS by `ps`'s `comm`
  field, not `pgrep -f claude` substring matching. Wired into
  `host-pressure.sh`'s session gate (`grep -n count-agents
  scripts/supervisor/host-pressure.sh`, line 189 calls it directly).
- `quota.sh` — the quota floor gate; nothing else may call `codexbar` directly
- `lane-done.sh` — rename a lane back to `free-N` on worker completion
- `lane-retire.sh` — administrative retirement: unregister and restore the
  window's name, never kill it
- `register-lane-self.sh` — how a hand-attached lane registers ITSELF, from
  `$TMUX_PANE` and explicit `-t` reads only
- `lane-whoami.sh` — the fallback command a hand-written brief should name to
  derive `Review-Lane:` when nothing has already stated it (never a bare
  `tmux display-message` — see Invariant 10). Superseded as the PRIMARY path:
  `dispatch.sh`/`dispatch-claude-print.sh`/`dispatch-pi-rpc.sh` now state the
  lane's own id directly in every brief; this script is for a brief written
  outside that contract.
- `restore.sh` — rebuild every lane after a tmux server loss, ledger-driven
- `preserve-dead-lanes.sh` — save a dead lane's uncommitted work before it's
  lost
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
- `director-loop.sh` — drive the Director on a cadence
- `loop-tick.md` — the loop's own step-by-step tick prompt; the supervisor
  lease is taken here, keyed on `#{pane_pid}` (the tmux pane's own live
  process), never on `$$` — a Bash tool call's own subprocess pid dies before
  the next tool call, which made every lease look stale
- `heartbeat.sh` — detect a stalled estate and nudge it once
- `watchdog.sh` — restart the supervisor loop when it dies with work left,
  found by pane COORDINATES, not name; sourced-only siblings
  `watchdog-harness.sh` / `watchdog-status.sh` / `watchdog-checks.sh` /
  `watchdog-advance.sh` split its responsibility — none is meant to run
  standalone
- `watchdog_notify.py` — decide whether the watchdog's `escalate` state
  should reach a human
- `sleepcheck.py` — is the loop asleep with a wakeup pending, or actually down
- `state.sh` — the supervisor's current situation as a small hard-capped
  document, not a conversation replay
- `digest.sh` — one command answering "what is the state of the estate now"
- `contest-stop.sh` — auto-contest a STOP-CONCLUSION nobody chose
- `advance-live.sh` — advance the LIVE watchdog worktree to `origin/main`
- `tooling-drift.sh` — tick-time drift detector for the loop's own tooling
  surface
- `weekly-watch.sh` / `quota-watch.sh` / `quota-watch-recover.sh` — tell Jon
  when the weekly cap or session quota is nearly gone, and self-correct the
  watcher if it hangs; `quota-watch.sh` resolves the live Director via the
  supervisor lease, not a name guess
- `launchd/render-plists.sh` plus `launchd/templates/*.plist.tmpl` — render
  the 4 live launchd jobs' plists with a versioned, rename-safe entry-point
  path instead of a hardcoded checkout path; a rendered plist cannot be
  proven to fire on this machine's current boot from the repo alone

**Estate loop** (`scripts/estate-loop/` — a separate build loop: no
supervisor, no ledger, no lease; versioned in-repo, previously outside it
entirely):
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
- `idea.sh` — capture an idea into the corpus fast, without derailing
- `poller-leak-cleanup.sh` / `poller-lib.sh` / `poller-recover.sh` — detect
  and clean up leaked or dead poller processes

**tmux safety** (see Invariant 4):
- `tmux-isolation.sh` — `assert_isolated_tmux`, required before any
  destructive tmux verb or session creation
- `tmux-guard.sh` — a `tmux` wrapper on PATH that refuses a bare destructive
  verb typed directly into a lane's own shell
- `tmux_verb_guard.py` — static guard: no test may create/destroy a tmux
  SESSION outside isolation
- `worktree-guard-audit.sh` — audits that the isolation guard is actually
  reachable from files that source it
- `send.sh` — the one verified-send primitive for typed text into a pane's
  input box
- `session-defaults.sh` — shared tmux session name/defaults, centralized so
  a repo rename can't leave stragglers

**Viewers / UI** (a UI PR needs a captured frame — see Conventions):
- `laneview/` — four renderers (`text.sh`, `tui.sh`, `opensessions.sh`,
  `dock.sh`); none is required by another
- `laneview.sh` — drive one `laneview/` implementation from `lanes.sh`'s json
- `laneview-leak-cleanup.sh` — report-first cleanup for leaked `tui.sh`
  processes
- `look.py` — let an agent SEE a pane: capture / png / navigate / frames
- `termshot.py` — the ANSI-to-SVG rasteriser `look.py`'s `png` renders through
- `dim-strip.sh` — the one place that defines "is this span a Claude Code
  prompt suggestion, or real pane content"

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
  neither `record_prompt` nor `mine_prompts.py` sets it
- `prompt_capture_hook.py` — the `UserPromptSubmit` hook that captures and
  classifies a prompt at submit time instead of relying on someone
  remembering to re-crawl; registered in `.claude/settings.json`
- `prior-attempts.sh` — what did the last agent on this issue already find
- `acceptance.sh` — re-run a CLOSED issue's acceptance test, reopen if back

**Housekeeping:**
- `branch-sweep.sh` — delete local branches already merged into `origin/main`
- `audit-lanes.sh` — compare every ledger `lanes` row against live tmux, once
- `reap-verified.sh` — one verified reap primitive for tests with a real
  long-lived poller-shaped process
- `lane_identity.py` — is a lane's REGISTRATION still true of the live tmux
  server it names (verified / contradicted / unverifiable, never bare yes/no)
- `would-revert.sh` — "would merging this branch revert X", by merging it,
  not by reading a diff
- `refresh_brief_resume.py` — regenerate a brief's `## Resume point` block

`tests/supervisor/` — 308 tracked files (`git ls-files tests/supervisor | wc
-l`, checked at write time; re-run, don't trust this number stale): the suite
is the contract. `python3 -m unittest discover -s tests/supervisor` has not
reliably finished inside one working session's time budget; run a targeted
test file, not a full discovery, when you only touched one thing. The four
former monolithic files (`test_dispatch.sh`, `test_verdict.py`, `test_cli.py`,
`test_core.py`) no longer exist — each was split by topic/`TestCase` class
into the `test_dispatch_*`, `test_verdict_*`, `test_cli_*` and `test_core_*`
families; look there, not for the single file.

### The guards a lane will actually hit

> **None of the guards in the table below still exists.** Every file in its
> "Implemented in" column resolves only to `reference/` — check any of them
> with `git ls-files '*<name>'` and see. `git ls-files scripts/supervisor`
> returns 0. The table is kept because the RULES are still what this estate
> believes; it is a record of intent, not a map of live code, and nothing in
> it will refuse anything.
>
> The guards that do run today are in Go, in `src/estate`: `estate pressure`
> (host capacity, fails closed when it cannot measure) and `estate merge`
> (checks green at head, plus reviewer ≠ author read from the ledger).
> Reimplementing any row below means writing it in Go, not restoring the
> script. `src/estate/agents_md_test.go` fails the build if this file names
> an `estate` subcommand that does not exist — added because three review
> rounds on one pull request each caught a claim about a command that was
> real in the author's working tree and absent from the branch.

Each of these *once* refused your dispatch, your merge, or your PR, and the
"Implemented in" column says where the rule was encoded so it can be read
before being rebuilt.

| Guard | Refuses | Implemented in |
|---|---|---|
| CI gate | merge, until every check is green at the live head SHA | `ci_gate.py`, called from `merge-pr.sh` |
| Authorship / independence | merge, when the reviewer lane is the author lane (Invariant 9) | `verdict-independence.sh` (`lane_relation`), called from `merge-pr.sh` |
| Collision check | dispatch, when files overlap an in-flight lane's | `collision-check.sh`, called from `dispatch.sh` step 3.2; override with `--force` |
| Host pressure | dispatch, when the host can't safely take another agent | `host-pressure.sh` / `host_pressure.py`; exit 2 = "couldn't measure", refused, not guessed |
| Quota floor | new work, when the subscription quota is below the floor | `quota.sh` (`MIN_REMAINING`), watched by `quota-watch.sh` |
| Supervisor lease | a second Director loop starting while one is alive | `core.py` (`Ledger.take_supervisor_lease` / `release_supervisor_lease` / `reap_stale_supervisor_lease`), exposed via `cli.py`'s `take-supervisor-lease` / `supervisor-lease` / `release-supervisor-lease` / `reap-supervisor-lease` subcommands; owner is keyed on `#{pane_pid}` from `loop-tick.md`, never `$$` |

**These gates no longer exist.** `completion-gate.sh` (wouldn't advance a task
group until every member left evidence), `fixpass-evidence-gate.sh` /
`fixpass_evidence_gate.py` (a fix pass must paste proof, not a claim),
`ui-evidence-gate.sh` / `ui-evidence-report.sh` (a UI PR needs a captured
frame), `gh-comment-gate.sh` and `mark-pr-external.sh` all went to
`reference/` with the shell supervisor; `git ls-files scripts/supervisor`
returns 0. The workflows that ran them were retired on 2026-09-02 because
they failed on every PR regardless of its contents, which is not enforcement.

The **rules** are worth keeping and are recorded, with their status, in
[`docs/ci-rules-retired.md`](docs/ci-rules-retired.md). Recovering one means
reimplementing it in Go. Do not cite any of them as enforced.

### Invariants — do not break these without an explicit decision

1. **The ledger is the record; tmux is the screen.** Anything *decided and
   remembered* belongs in `ledger.sqlite3`; anything *observed right now* is
   read from the pane. The test is authorship: did this system write the
   value, or did tmux produce it as a byproduct? See
   [decisions/0001](docs/decisions/0001-sqlite-ledger.md).

2. **Write the durable fact before the pretty label.** `lane-done.sh`
   releases the ledger, then renames the window. The reverse order strands a
   lane permanently on a crash; this order leaves only a stale label.

3. **Restore refuses rather than invents.** A lane that cannot be brought
   back with its own conversation is reported `UNRECOVERABLE` (exit 2) and
   left alone — a fresh agent wearing a recovered lane's name looks fully
   healthy and has none of the context. See
   [decisions/0004](docs/decisions/0004-restore-refuses-never-invents.md).

4. **Never address the default tmux socket in a test.** `kill-server`,
   `kill-session`, `kill-window` and `respawn-*` must be scoped with
   `TMUX_TMPDIR` and gated by `assert_isolated_tmux` (`tmux-isolation.sh`) —
   a bare `tmux kill-server` from a lane destroyed the entire live estate
   three times in one day. See
   [decisions/0012](docs/decisions/0012-invariant-evidence.md#invariant-4--never-address-the-default-tmux-socket-in-a-test).

5. **Address windows by `window_id` (`@7`), never by index.** Killing window
   4 renumbers 5 into 4. A loop killing indices hits shifting targets; that
   destroyed the Telegram poller.

6. **`unknown` means "not offered", not "broken".** `lanes.sh` is a
   whitelist: only a recognised idle shape is offered as free. Handing work
   to a lane you cannot read is worse than leaving it idle — do not
   "improve" this into a guess.

7. **`lanes.sh` holds no harness-specific string.** Harness knowledge lives
   in `harness/*.sh`. Widening a regex to cover another harness is the wrong
   fix — it lets one harness's shapes falsely match another's.

8. **The poller is a service, not a lane.** Never dispatch to it, never
   "restart" it as a lane. It consumes Telegram messages by acking the
   offset, so running the inbox by hand returns nothing — that is not
   evidence nobody wrote.

9. **Lane identity is the string `<session>:<index>`** (e.g.
   `agent-supervisor:3`) — not the task, not the worktree, not the window
   name. Compare lane ids, never task ids or window names, when deciding
   whether two pieces of work were done by the same agent. See
   [decisions/0012](docs/decisions/0012-invariant-evidence.md#invariant-9--lane-identity-is-the-string-sessionindex).

10. **A lane identifies itself by matching the ledger's `worktree_path`
    against its own `cwd`, not by asking tmux who it is.** A brief should
    never spell this out as a raw command — name `lane-whoami.sh` (index
    above), which already picks the right branch (pane vs. pane-less) and
    never calls `tmux display-message` with no `-t`. See
    [decisions/0012](docs/decisions/0012-invariant-evidence.md#invariant-10--a-lane-identifies-itself-by-matching-worktree_path-to-its-own-cwd)
    for the `--include-reviews` flag's own trap and why it must stay.

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

**A tool that fails closed and that nothing calls is a documentation rule
with a binary attached.** After building anything protective, ask both
*what calls it?* and *is that caller something that survives the failure it
guards against?* `count-agents.sh` (index above) was a live instance: it
existed, it was tested, and for a time nothing called it, until
`host-pressure.sh` was wired to call it directly — check whether a case
like this is still unresolved before citing it as a fresh example.

**An abstraction can be present and correctly avoided.** Routing around a
seam looks identical to nobody having wired it up. Check whether the
avoidance is documented before "fixing" it — the reason belongs next to the
seam.

**A merged fix that never reaches the process running it looks identical to
an unfixed defect.** `agent-supervisor#308` was diagnosed as a live code bug
three times before anyone checked which checkout was actually running —
before re-diagnosing a "still broken" report, check what checkout it was
measured against. See
[runbooks/stale-checkout-diagnosis](docs/runbooks/stale-checkout-diagnosis.md).

### Conventions

- Branch with a type prefix; never commit to `main`.
- One independent review per PR, by someone who did not write it — including
  fixup commits. `dispatch.sh --reviews-pr` asks the ledger, not the branch
  name, and `dispatch.sh --pr` supports dispatching a review or a fix pass
  scoped to a PR directly rather than only to the issue it closes.
- This is enforced at merge, not just at dispatch: `merge-pr.sh` is the only
  path that should merge a PR here, and it refuses to merge one whose author
  lane and reviewer lane are the same (Invariant 9) or whose verdict cannot be
  read. `gh pr merge` run directly bypasses this — it is convention, not a
  platform block, same as the CI gate above.
- One fix pass. If a PR fails a second review, close it and file what remains.
- Cheaper model tiers for workers and reviewers; reserve the expensive tier for
  judgement.
- Anything touching tmux behaviour runs against an isolated socket or on a
  throwaway host — never the machine you are working on.
- **Credential store — read-only, no exceptions.** Never write, reset, or
  probe the macOS Keychain; a failed read is a report, not a repair. See
  `agent-dotfiles/AGENTS.md` for the canonical rule and incident rationale
  (agent-estate#665).
- **Nothing hand-authored or pane-written merges**, until the per-instance
  re-dispatch cost starts to dominate — at which point revisit. A change
  must go through a dispatched lane with a ledger-resolvable author, no
  exception path today, the same posture the CI gate and author-exclusion
  guard above already take.
- A UI PR needs a captured frame, not a description, as evidence. **This is a
  convention now, not a gate.** `.github/workflows/ui-evidence.yml` and
  `ui-evidence-gate.sh` were retired on 2026-09-02 (see
  [`docs/ci-rules-retired.md`](docs/ci-rules-retired.md)); nothing fails a PR
  that omits the frame. The path it named, `scripts/supervisor/laneview/`, no
  longer exists either — the viewer is `src/tui`. **The capture helper is
  `src/tui/cmd/vhscapture`** (`go run ./cmd/vhscapture -tape
  testdata/vhs/<name>.tape`, run from `src/tui`) — retries a whole `vhs` run
  until every `Screenshot` target clears its colour floor, since a bare `vhs`
  run silently writes a stale/blank frame some fraction of the time
  (agent-estate#947). Local only, not wired into CI (`vhs`, `ttyd` and
  `ffmpeg` aren't installed on the `ubuntu-latest` runner `tui-ci.yml` uses —
  confirmed by reading the workflow, agent-estate#976); see
  `src/tui/testdata/vhs/README.md`. Only 4 of 27 tapes have a measured
  `.mincolors` floor (agent-estate#976) — the rest silently fall back to
  `-min-colors`'s own unmeasured default (1000), which agent-estate#960's own
  sweep found rejects over a third of tapes outright, so the floor is neither
  fully permissive nor fully closed for the other 23. Reimplementing the gate
  in Go is open work.

---
*Last checked against the tree at `2e810dc` (2026-08-29). If `git log
--oneline scripts/supervisor | head -1` names something newer than that and
this file hasn't moved, treat any specific claim above as unverified until
you re-check it — don't trust it just because it's written down.*

## The TUI

Arrival policy for the TUI half of this repo, everything under `src/tui/`
(moved there from `tui/` by #865). `src/` is a deliberate one-member
convention introduced for that move, not an incomplete migration — nothing
else (e.g. `scripts/`) moves under it without its own decision, since that
blast radius is real and unmeasured (#875). **Verified against `main` `2e810dc`,
2026-08-29, before #865's move** — path references below reflect the
post-move `src/tui/` location; re-check counts against the current tree
before trusting them. Earlier verification stamps for this section (through
`390c99a`, 2026-08-23) are superseded by this one; git history has them if
you need the trail.

**Naming: the product is the Estate**, binary `estate`, Go module
`github.com/jonhill90/agent-estate/src/tui` (renamed off
`github.com/jonhill90/agent-estate/tui` by #865, itself renamed off
`github.com/jonhill90/agent-tui` by #747, once `jonhill90/agent-tui` itself
was decommissioned — the module path stayed `agent-tui` for a while after the
product rename on purpose, since publishing it had pinned the import path and
renaming while the repo was still live would have broken any consumer
import; that constraint lapsed once the repo it named was retired). Prose in
this repo's docs says "the Estate"
(capital E, lowercase article); code identifiers use `estate`. Issue
references below keep the `agent-tui#NN` form because that is the repo they
point at. Full naming history — `keelson` and `steading` both considered and
retired, the collision checks behind each, and the mechanical rename PR — is
in [decisions/0006](docs/decisions/0006-agent-tui-merges-into-agent-supervisor.md).

### What this repo is

This repo (Go module `github.com/jonhill90/agent-estate/src/tui` — see the
naming note above) is one terminal application: a left nav sidebar modelled 1:1 on the
hill90 web app's own nav (`internal/nav`, `docs/tui/SPEC-shell.md`), with the
task board, cost panel, glyph gallery and the lane rail over
`agent-supervisor`'s lane/session state all reachable as routed panes in the
same process (`internal/shell`; the nav sidebar replacing the rail as the
fixed left column is `docs/tui/SPEC-shell.md`'s S3). The name `agent-tui`
describes the rendering technology (Go +
[Bubble Tea](https://github.com/charmbracelet/bubbletea)), not the
product — the product's name is the Estate (see the naming note above).
It is a **viewer with one write path** (session attach/detach/add/remove,
see below) — same discipline as `agent-supervisor`'s own
`scripts/supervisor/laneview/`. It never shells out to `tmux` directly,
never reads or writes the ledger except through the adapters listed below,
and never reimplements `ccusage`'s or `lanes.sh`'s parsing.

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
internal/chat/       ACP thread chat -- Source/Sender seams, ClaudeCodeSource + FallbackSource with FixtureSource as last resort, two viewport-scrollable layouts
internal/connectors/ Connect group -- provider connections and models, mirrors web Connect (SPEC-shell.md S10)
internal/cost/       per-harness spend/quota projection from ccusage
internal/dashboard/  estate-at-a-glance view -- re-projects figures already established by internal/agents/internal/cost/internal/knowledge plus a small gh read of its own
internal/external/   Docs -> Platform Docs -- how a nav.KindExternal destination behaves (names the URL, opens a browser)
internal/flow/       live flow view — the same board.Snapshot re-projected as a moving pipeline
internal/gallery/    glyph gallery — every lane state × every candidate glyph set
internal/knowledge/  Jon's personal memory vault viewer -- reads $AGENT_MEMORY_VAULT's agent/index.md + agent/facts/<slug>.md, progressive disclosure
internal/lane/       lane/session decode, glyph sets (data, not code), state table
internal/library/    shared prompt/decision corpus viewer -- agent-dotfiles-supervisor's ledger.sqlite3 live_parameters/open_questions/unacknowledged views
internal/mcp/        minimal MCP JSON-RPC client over a child process's stdio
internal/mcpservers/ configured MCP servers -- name, scope (global/project), reachability (SPEC-shell.md S9)
internal/mergepr/    merge-time gate for this repo -- chains the CI gate and internal/prverdict's comment-verdict gate, fails closed, then calls gh pr merge
internal/monitor/    host health (load/swap/process count) + agent state counts (Observe -> Monitoring)
internal/nav/        the 1:1-with-hill90 nav tree + sidebar component -- the fixed left column (SPEC-shell.md S1-S3)
internal/navwalk/    one JSONL file per nav destination, replacing the single hand-merged src/tui/testdata/vhs/full-nav-walk-report.md
internal/prverdict/  reads a PR's own comments and decides whether it carries an independent, current APPROVE -- Go port of skills#255's pr_verdict.py
internal/rail/       the lane rail -- content behind the sidebar's "Lanes" route (PaneLanes) since SPEC-shell.md S3/S4, no longer a fixed column
internal/secrets/    Connect -> Secrets -- levels 1-4 of an exposure scale from hill90-app's secrets-schema.yaml, never level 5 (the value)
internal/session/    write path: attach/detach/add/remove/send, all via MCP, no os/exec
internal/shell/      the application shell -- owns the sidebar (internal/nav) + ~20 routed panes (SPEC-shell.md S3)
internal/skills/     skills view -- name, description, last eval result, invocation count, from ~/.claude/skills (SPEC-shell.md S8)
internal/sshserver/  serves shell.Model over SSH via charmbracelet/wish -- one Model per connection
internal/stub/       honest "not built yet" placeholder for any nav route with no real pane wired (SPEC-shell.md S5)
internal/theme/      look-and-feel as data — Role-keyed colours, persisted per-user config
internal/workflows/  ledger dispatch history -- a task's own path through the estate (Build -> Workflows)
scripts/tui/         verify-lanes-unaffected.sh — the rail's non-interference proof (rail's own render/key logic is unchanged by SPEC-shell.md S3; only its screen position moved)
```

`cmd/` also now has `cmd/demo`, `cmd/fakemcp`, `cmd/mergepr`, `cmd/navwalk`
and `cmd/prverdict` alongside `cmd/estate` — the CLI entry points for
`internal/mergepr`, `internal/navwalk` and `internal/prverdict` above, plus
a demo harness and a fake MCP server used by tests. None of the five is a
second `tea.NewProgram` site (see "What NOT to do here" below); they are
plain CLI commands.

`internal/chat` is wired into the shell as `PaneChat` (`[f6]`) — `[f5]` is
`internal/flow`'s `PaneFlow`. It renders against `chat.Source`, an adapter
seam the same shape as `rail.Fetcher`: `ClaudeCodeSource` (reads real Claude
Code CLI session transcripts) is the real implementation, `FallbackSource`
drops to `FixtureSource` only when `ClaudeCodeSource` reports itself
genuinely unconfigured. Sending is built, and Chat is a multi-participant
room with `@`-mention addressing — see `docs/tui/SPEC-shell.md`'s S7 for the
fuller build history, including why a screen-scraped transcript was rejected
as the read source.

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
| `chat.Source` | `internal/chat` | ACP `session/update` thread content — `ClaudeCodeSource` + `FallbackSource` are the real implementations shipped; `FixtureSource` is only the last-resort fallback |

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

`cmd/estate` has seven `_test.go` files (`chat_test.go`, `cost_test.go`,
`docs_test.go`, `ledger_copy_test.go`, `secrets_test.go`, `skills_test.go`,
`supervisor_test.go`) and `tools/memoryvariants/spike` has one
(`main_test.go`); `internal/sshserver` still has none (`git ls-files
'src/tui/**/*_test.go'`, checked at write time — re-run, don't trust this
list stale). CI (`.github/workflows/*.yml`) runs the same three commands on
`ubuntu-latest`, Go 1.26, plus a fourth check gated on a live
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
`[f4]` inside the one running process (`internal/shell`);
`-board`/`-cost`/`-gallery` now only choose which pane the app opens on.

**A binary that builds is not a feature that works.** `go test` exercises
`Model.Update` with synthetic key messages against fakes; it does not press a
key against a live tmux session. Before documenting a control as working,
either cite the test that drives it through `Update` (name it) or say
"not verified against a live session."

### Merging PRs you did not author

Every agent lane pushes through the same shared GitHub login, so `gh pr
review --approve` is refused as self-review regardless of who is actually
asking — a real cross-lane review is recorded as a plain PR comment instead
of a GitHub review object, carrying:

```
Verdict: APPROVE            (or REQUEST CHANGES, with specifics)
Review-Lane: <reviewing lane's own name>
Reviewed-SHA: <the exact head commit SHA reviewed>
```

and the PR's own body states which lane opened it: `Author-Lane: <authoring
lane's own name>`.

**`cmd/mergepr` is THE way to merge a PR in this repo. Do not `gh pr merge`
directly, and do not run `cmd/prverdict` as a manual pre-check and then merge
by hand.**

```
go run ./cmd/mergepr -repo <owner/name> -number <N>
go run ./cmd/mergepr -repo <owner/name> -number <N> -- --squash --delete-branch
```

Exit `0` means it merged. Exit `1` means a gate refused (CI not green at the
current head, or `internal/prverdict`'s gate did not resolve to a genuine
cross-lane approval — the refusing gate's own reason is always printed to
stderr) or `gh pr merge` itself failed; nothing was merged either way. Exit
`2` is a usage error. See `internal/mergepr`'s own doc comment for exactly
what the two gates check, `internal/prverdict`'s doc comment for the
comment-verdict gate specifically, and
[decisions/0013](docs/decisions/0013-tui-merge-gate.md) for why this
command exists and the self-approval bypass it had to close.

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
  <UTC>` stamp at the top; update it — replace it, don't stack a new one on
  top — when you re-check the claims below it.
- **Glyph sets and themes are data, not code** (`internal/lane/variants.go`,
  `internal/theme/registry.go`) — a new visual variant is a struct literal
  addition, never a new code path in a render function.

### Known defects — do not paper over these

agent-tui#49 is **closed** (2026-08-16); all three defects it recorded (bare
launch exiting 1, the board pane refusing with no `-ledger`, the cost
panel's quota line being unwired) are fixed — see
[tui/known-defects-49](docs/tui/known-defects-49.md) for what each was and
the fix evidence. If a regression reopens any of the three, restore the
numbered form there with fresh confirmation evidence rather than treating
this as closed by assumption.

### What NOT to do here

- Do not add a new `tea.NewProgram` call site. `internal/shell.Model` is the
  one program (a new view is a pane added to the shell, never a second
  program selected by a launch flag).
- Do not call `os/exec` for tmux from any package under `internal/`. Every
  tmux-adjacent operation is a supervisor MCP tool call.
- Do not restore `[a]ttach`/`[d]etach` in the rail without checking
  `agent-supervisor#202`'s shape first. They were removed because MCP's
  stdio transport gave the supervisor no way to know which tmux client was
  asking, so `switch-client`/`detach-client` acted on an arbitrary attached
  client while reporting success. `agent-supervisor#202` ("session_attach/
  session_detach name which tmux client acts, and refuse to guess") fixed
  the supervisor-side blocker, but `session.Interface`'s `Attach`/`Detach`
  still have zero callers here (`grep -rn "\.Attach(\|\.Detach("
  --include='*.go' .`, outside test files) — the fix landing upstream is not
  the same as this repo having wired a caller to it.
- Do not point `-ledger` at the live supervisor's `ledger.sqlite3`. It is
  always opened read-only, but the flag help and `internal/board/ledger.go`
  both document why a copy is still required.


## The implementation language is Go. This is checked, not trusted.

**The app is Go.** Shell and Python are not an implementation option here, at
any size, for any reason, including "just this one script" and "only for
delivery".

`reference/` holds the deleted shell and Python supervisor, kept so an agent can
read how a rule was once encoded. It is **reference material, not a codebase**:
nothing there is maintained, run, tested, or fixed. Recovering a rule from it
means reimplementing that rule in Go, not calling the script.

This is guidance, not a gate. A CI blocker on new shell or Python was tried and
removed on 2026-09-02: it was an over-extreme reading of the operator's intent,
and a hard block can wedge an agent that legitimately needs a script for
tooling, a sandbox, or an experiment. The intent is narrow and stands — the
APP is not built out of shell and Python. Scripts elsewhere are unremarked.

**Why this is a guard and not a paragraph.** The directive that the supervisor
is Go was recorded on 2026-08-22. Its named target was later archived, the rule
was left pointing at nothing, and it silently stopped binding — a month of work
went into growing the layer it ruled out. A rule nothing checks is a preference.

**Before starting any task**, check it against the standing directives. If the
task extends something ruled out, stop and say so rather than doing it well.
Never open an issue against a layer scheduled for deletion. "Merged" is not
"delivered" — report what a human can now do that they could not before.

