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

The daemon is Go, in `src/estate`: one binary, one append-only ledger, and one
narrowly-scoped tmux caller (`internal/mirror`, added by #1003 — it opens a
read-only viewer window on a turn's transcript, and tmux is never the
transport). Run `go run ./src/estate` with no arguments for the current
subcommand list — it grows, and a list written in prose goes stale between
commits.

**The shell and Python supervisor this section used to index is deleted.**
`git ls-files scripts/supervisor tests/supervisor` returns **0**. Those files
are kept, unmaintained, under `reference/` so a rule can be read in the form
it was once encoded, and git history has the rest. Nothing there is run,
tested, or fixed; recovering a rule from it means reimplementing that rule in
Go. The rules retired along with the CI workflows that ran them, and the
status of each, are in [`docs/ci-rules-retired.md`](docs/ci-rules-retired.md)
— do not cite any of them as enforced.

What follows is the part that still binds: how to treat the corpus and when a
question may reach Jon, the guards that actually refuse something, and the
invariants. Where an invariant's original mechanism was one of the deleted
scripts, that is said plainly rather than re-pointed at something that does
not do the same job.

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

### The guards that actually run

Two, both in Go, both failing closed.

| Guard | Refuses | Implemented in |
|---|---|---|
| Host pressure | a new dispatch when the host cannot safely take one — and equally when it cannot measure the host at all, because blindness is not capacity. The token-budget floor is one of its limits, not a separate gate: `internal/quota` refuses at roughly 10% remaining, and a reading older than 20 minutes counts as no reading | `estate pressure`, `src/estate/internal/pressure`, `src/estate/internal/quota` |
| Merge gate | to *allow* a merge unless the PR is open with every required check green at its live head SHA, a dispatched turn is joined to it by BOTH its `dispatch/<id>` head ref and a head SHA the estate itself recorded, the reviewer is a different dispatch from the author, and a parsable APPROVE was actually posted. It decides and prints; the merge itself is still someone else's `gh pr merge` — see the Conventions section and **#980** | `estate merge`, `src/estate/internal/gate` |

`estate dispatch` refuses to start a turn at all when the named harness is
unknown or its binary is not on PATH, when the operator's hard parameters
cannot be read from the corpus, when host pressure refuses, or when the turn
cannot be given a git worktree of its own (`src/estate/internal/isolate` — a
worktree, not a sandbox; read its doc comment for what that does and does not
bound).

Read `internal/gate`'s own package comment before relying on any summary of
it, including this one: it states what the authorship join does and does not
establish, at more length than a table can.

**Nothing else refuses anything.** The collision check, the supervisor lease,
the completion gate, the fix-pass evidence gate and the UI evidence gate were
all mechanisms of the deleted supervisor, and none was reimplemented. What each
of those five rules actually *said* is recorded in
[`docs/ci-rules-retired.md`](docs/ci-rules-retired.md), along with
`gh-comment-gate.sh` and `mark-pr-external.sh` — read it there rather than
recovering a rule from `reference/`'s source.

### Invariants — do not break these without an explicit decision

These are rules, not a map of live code. Several were once enforced by scripts
that no longer exist; each one says so where it applies.

1. **The ledger is the record; the live system is the screen.** Anything
   *decided and remembered* belongs in the ledger; anything *observed right
   now* is read from the running thing. The test is authorship: did this
   system write the value, or did something else produce it as a byproduct?
   The ledger today is `src/estate/internal/ledger` — append-only JSON lines
   at `$ESTATE_LEDGER`, defaulting to `~/.local/state/estate/ledger.jsonl`,
   not the SQLite `ledger.sqlite3` the old supervisor used. The decision
   record this invariant used to cite (`docs/decisions/0001-sqlite-ledger.md`)
   is **not in this tree**.

2. **Write the durable fact before the pretty label.** When one operation
   writes both a durable record and a lighter label pointing at it, write the
   record first. The reverse order strands the work permanently on a crash;
   this order leaves only a stale label. The script that demonstrated it
   (`lane-done.sh`) is gone and **no equivalent exists in the Go tree** — the
   rule binds anything new that writes two places.

3. **Restore refuses rather than invents.** Work that cannot be brought back
   with its own context is reported unrecoverable and left alone — a fresh
   agent wearing a recovered identity's name looks fully healthy and has none
   of the context. `restore.sh` is gone. The live expression of the same
   disposition is `ledger.State`: a turn that could not be observed is
   recorded `unknown`, `unknown` is deliberately **not** terminal, and
   `estate reclaim` frees a slot only for a turn positively observed dead.

4. **Never address the default tmux socket in a test.** `kill-server`,
   `kill-session`, `kill-window` and `respawn-*` must be scoped with
   `TMUX_TMPDIR` and gated on an isolation assertion — a bare `tmux
   kill-server` from a lane destroyed the entire live estate three times in
   one day. The guard that enforced this (`tmux-isolation.sh`) is gone, and
   **no general guard replaced it** — this still binds any script or test you
   write outside the app. What does now exist is scoped to the one package
   that calls tmux: `internal/mirror`'s `tmuxCmd` routes every invocation
   through a six-verb allowlist (`allowedVerbs`), so `kill-server`,
   `kill-session` and `respawn-*` cannot run from it at all, and
   `Config.TmuxTmpdir` scopes each call to a private socket directory with
   `$TMUX` unset. Read that as covering `internal/mirror`, not the tree.
   `internal/isolate` is about git worktrees, not tmux — do not read it as
   this guard.

5. **Address windows by `window_id` (`@7`), never by index.** Killing window
   4 renumbers 5 into 4. A loop killing indices hits shifting targets; that
   destroyed the Telegram poller. `internal/mirror` is the live caller and it
   honours this: `kill-window` is its only destructive verb, and every id it
   passes came out of a `list-windows` call in the same pair, never an index
   and never a name. Its package comment also reconciles this against the
   operator parameter `lane_addressing=session_index_not_raw_window_id` —
   that rule governs *delivering* to a lane across a server restart, which
   this package never does.

6. **`unknown` means "not offered", not "broken".** A classifier over live
   state should be a whitelist: only a recognised shape is offered as free.
   Handing work to something you cannot read is worse than leaving it idle —
   do not "improve" this into a guess. The classifier it was written about
   (`lanes.sh`) is gone; the same disposition is live in `ledger.State` (see
   invariant 3) — unobserved is not finished.

7. **Harness-specific strings live in one place.** In the Go tree that place
   is `src/estate/internal/harness`, which `estate dispatch` asks by name and
   which refuses an unknown or uninstalled harness rather than defaulting.
   Widening a general classifier's pattern to cover another harness is the
   wrong fix — it lets one harness's shapes falsely match another's.

8. **A service is not a lane.** The Telegram poller was the instance: never
   dispatch to it, never "restart" it as a lane, and remember it consumed its
   own inbound queue by acking the offset, so running the inbox by hand
   returned nothing — which is not evidence nobody wrote. **There is no
   poller under `src/`** (`git grep -il poller -- src` finds nothing); the
   rule is kept for the class, not for a live process.

9. **Identity is what the estate minted, never a name someone chose.** For
   the old supervisor that string was `<session>:<index>`. Today it is the
   dispatch id, carried as the `dispatch/<id>` head ref and cross-checked
   against a head SHA the estate itself recorded when that turn exited
   (`internal/gate`, `internal/dispatchid`). Compare those, never issue
   numbers, task titles or branch names, when deciding whether two pieces of
   work were done by the same agent. This one **is** enforced: `estate merge`
   refuses a PR whose head ref it cannot join to a dispatch record.

10. **A dispatched turn does not derive its own identity — the estate states
    it.** `estate dispatch` appends the turn's own branch to an author's
    brief and its own dispatch id to a reviewer's, so neither has to work out
    or invent one (`roleGrounding` in `src/estate/main.go`). Do not restate
    those values in a hand-written brief, and do not let a turn re-derive
    them from what it sees around it. `lane-whoami.sh`, the old fallback, is
    gone, and the decision record this invariant used to cite
    (`docs/decisions/0012-invariant-evidence.md`) is **not in this tree**.

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
guards against?* `count-agents.sh` was the instance: it existed, it was
tested, and for a time nothing called it, until `host-pressure.sh` was wired
to call it directly. Both scripts are now in `reference/`; the lesson is not.

**An abstraction can be present and correctly avoided.** Routing around a
seam looks identical to nobody having wired it up. Check whether the
avoidance is documented before "fixing" it — the reason belongs next to the
seam.

**A merged fix that never reaches the process running it looks identical to
an unfixed defect.** `agent-supervisor#308` was diagnosed as a live code bug
three times before anyone checked which checkout was actually running —
before re-diagnosing a "still broken" report, check what checkout it was
measured against. (The runbook this used to cite,
`docs/runbooks/stale-checkout-diagnosis.md`, is **not in this tree**.)

### Conventions

- Your branch is `dispatch/<id>`, created by the estate, and you push it
  as-is. Never commit to `main`, and never open a PR from a hand-named branch
  — the merge gate joins authorship through that head ref and refuses
  anything else structurally (invariant 9).
- One independent review per PR, by someone who did not write it — including
  fixup commits. A review turn is dispatched with `estate dispatch review`,
  which records `role=reviewer` at dispatch time, so the gate never has to
  infer the role later from what a lane or a PR comment claims about itself.
- This is checked at merge, not just at dispatch — but read the command's name
  as a question, not an action. **`estate merge <repo> <pr> <reviewer-lane>`
  evaluates and exits; it does not merge anything.** It decides whether the PR
  may merge — open, every required check green at the live head SHA, author and
  reviewer different dispatches, and an independent parsable APPROVE posted at
  that same head — then prints its verdict. Exit 0 prints `may merge: …` and
  **you still have to run `gh pr merge` yourself**; exit 1 prints each refusal
  reason to stderr. `internal/gate` shells out only to `gh pr view`; it has no
  `gh pr merge` call anywhere. That the name promises an action it never
  performs is **agent-estate#980, open** — so the gate is advisory in the
  literal sense that skipping the evaluation, not running `gh pr merge`, is
  what bypasses it.
- The gate refuses any head ref that is not a `dispatch/<id>` branch:
  authorship here is established structurally, and it cannot be established at
  all for an operator-authored branch. The refusal is correct — and it means
  **an operator-authored PR has no gated merge path today.** (`agent-estate#940`
  is closed; it is the change that built this join, and the refusal message
  names it.)
- One fix pass. If a PR fails a second review, close it and file what
  remains. A fix pass continues the PR's own branch (`estate dispatch fix`),
  never a fresh one.
- Cheaper model tiers for workers and reviewers; reserve the expensive tier for
  judgement.
- Anything touching tmux behaviour runs against an isolated socket or on a
  throwaway host — never the machine you are working on (invariant 4).
- **Credential store — read-only, no exceptions.** Never write, reset, or
  probe the macOS Keychain; a failed read is a report, not a repair. See
  `agent-dotfiles/AGENTS.md` for the canonical rule and incident rationale
  (agent-estate#665).
- **Nothing hand-authored or pane-written merges**, until the per-instance
  re-dispatch cost starts to dominate — at which point revisit. A change must
  go through a dispatched turn with a ledger-resolvable author; the merge
  gate is what makes this structural rather than advisory.
- A UI PR needs a captured frame, not a description, as evidence. **This is a
  convention now, not a gate** — `.github/workflows/ui-evidence.yml` was
  retired on 2026-09-02 (see
  [`docs/ci-rules-retired.md`](docs/ci-rules-retired.md)) and nothing fails a
  PR that omits the frame. The capture helper is `src/tui/cmd/vhscapture`,
  run from `src/tui`; `src/tui/testdata/vhs/README.md` explains its colour
  floor and what is and is not measured. Local only — it is not wired into
  CI. Reimplementing the gate in Go is open work.

---
*The claims in this section were checked against this branch's own tree as
rebased onto `ef06010` (2026-09-03) — every path, command and count above was
re-run, and
what could not be found is named as absent rather than described. Re-check
before relying on any of it: `src/estate/agents_md_test.go` is the only
automated check on this file, and it validates **subcommand names only** — it
would pass a section that described every one of them doing the wrong thing.
A review caught exactly that here: this section once said `estate merge`
merges. It does not merge; it decides. Read a verb against the code, never
against the command's name.*

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

