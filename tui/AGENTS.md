# AGENTS.md — agent-tui

Repository policy for an agent arriving here. `CLAUDE.md` is a committed
symlink to this file — one source, two harness-visible names, no sync step.
Edit this file; never edit `CLAUDE.md` directly (it will edit the same bytes,
but say so in the commit message if you do, to avoid a reviewer thinking two
files drifted).

**Verified against `main` `6942926`, 2026-08-23 (agent-tui#38's shell PR,
#43, plus everything through #104's chat-send wiring), except the "What this
repo is" paragraph and this file's `internal/nav`/`internal/rail`/
`internal/shell`/`internal/stub` layout lines, updated 2026-08-22 for
`docs/SPEC-shell.md` S1-S3/S5.** The "Known defects" section below was
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

**Naming: decided. The product is `steading`** (agent-tui#42, seven rounds,
~60 candidates checked). Jon rejected `keelson` (real collision:
`akapril/keelson`, a near-identical local-first AI-session workbench) and
said to keep looking for "an untaken gem" before falling back to `loom`
(which collides with three separate agent orchestrators, 12–74 stars each).
`steading` is that gem: `gh api users/steading` and
`gh api repos/jonhill90/steading` both 404 (free), `npm view steading`
404s (free), and `gh search repos steading` returns zero purpose
collisions — re-verified 2026-08-20, the day this was applied. `steading.com`
and `steading.dev` are now registered (checked the same day; both were
reported free as of the 2026-08-16 round-4 check, so this changed in the
four days between), which is a real cost but not disqualifying — GitHub org,
`jonhill90/<name>`, npm, and search-purpose-collision are the signals that
discriminate real conflict from mere squatting, per every round's own
methodology, and `steading` clears all four. A steading is a farmstead and
all its outbuildings — the whole working holding, not a single machine —
which matches what this product actually is (rail, board, cost, gallery,
memory, chat, workflows) better than a renderer-technology name ever could.
**This is a naming decision, not a rename.** The Go module, `cmd/`
directory and binary stay `keelson` — a leftover of agent-tui#38's overnight
rename pass — and the GitHub repo stays `jonhill90/agent-tui`, both
deliberately, because mixing the naming call with the mechanical rename
would make this PR unreviewable (agent-tui#42's own brief). Prose in this
repo's docs should now say `steading` where the earlier text said "TODO" or
"unsettled"; code identifiers are unchanged and issue references below keep
the `agent-tui#NN` form because that is the repo they point at. TODO(rename):
a follow-on change should move the module path, `cmd/` directory, binary
name, and GitHub repo to `steading` in one pass — not done here, not
blocking here. Measured cost: `git grep -o -i agent-tui | wc -l` on this
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

## What this repo is

This repo (Go module `github.com/jonhill90/keelson` — see the naming note
above) is one terminal application: a left nav sidebar modelled 1:1 on the
hill90 web app's own nav (`internal/nav`, `docs/SPEC-shell.md`), with the
task board, cost panel, glyph gallery and the lane rail over
`agent-supervisor`'s lane/session state all reachable as routed panes in the
same process (`internal/shell`, agent-tui#38; the nav sidebar replacing the
rail as the fixed left column is `docs/SPEC-shell.md`'s S3). The name
`agent-tui` describes the rendering technology (Go +
[Bubble Tea](https://github.com/charmbracelet/bubbletea)), not the product —
the product's name is `steading` (agent-tui#42; see the naming note above).
It is a **viewer with one write path** (session
attach/detach/add/remove, see below) — same discipline as
`agent-supervisor`'s own `scripts/supervisor/laneview/`. It never shells out
to `tmux` directly, never reads or writes the ledger except through the
adapters listed below, and never reimplements `ccusage`'s or `lanes.sh`'s
parsing.

Read `README.md` for what has shipped, `docs/PRD.md` for what the product is
for, and `docs/SPEC.md` for the technical design. This file is arrival
policy only.

## What belongs here vs. `agent-supervisor`

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

## Layout

```
cmd/keelson/         one tea.NewProgram entry point, running internal/shell.Model (see docs/SPEC.md)
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
internal/navwalk/    one JSONL file per nav destination, replacing the single hand-merged testdata/vhs/full-nav-walk-report.md (agent-b3.md)
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
scripts/             verify-lanes-unaffected.sh — the rail's non-interference proof (rail's own render/key logic is unchanged by SPEC-shell.md S3; only its screen position moved)
```

`cmd/` also now has `cmd/demo`, `cmd/fakemcp`, `cmd/mergepr`, `cmd/navwalk`
and `cmd/prverdict` alongside `cmd/keelson` — the CLI entry points for
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
fixed by the prior sweep pass (#119); this specific paragraph was missed.**
`agent-tui#99` (commit `5997399`) shipped `internal/chat/claudecode.go`'s
`ClaudeCodeSource`, which reads real Claude Code CLI session transcripts,
and `internal/chat/fallback.go`'s `FallbackSource`, which `cmd/keelson`
wires in: try `ClaudeCodeSource` first, fall back to `FixtureSource` only
when the real source reports itself genuinely unconfigured.
`FixtureSource` is the last-resort fallback now, not the only
implementation. Sending is also built (`agent-tui#104`, commit `6942926`)
and Chat is a multi-participant room with `@`-mention addressing
(`agent-tui#114`, commit `a0ad626`) — see `docs/SPEC-shell.md`'s S7 for the
fuller history.

## Adapter discipline

Every package that touches the outside world is behind a function-typed or
interface-typed seam, supplied by `cmd/keelson/main.go`:

| seam | package | what it hides |
|---|---|---|
| `rail.Fetcher`, `rail.SessionsFetcher` | `internal/rail` | the MCP `lanes`/`sessions` tool calls |
| `session.Interface` | `internal/session` | attach/detach/add/remove, each one `mcp.Client.CallTool` |
| `cost.Fetcher` (built in `cmd/keelson/cost.go`) | `internal/cost` | shelling out to `ccusage` |
| `board.Fetcher`-shaped functions (`cmd/keelson/board.go`) | `internal/board` | `gh` CLI calls and a read-only `sqlite3` ledger open |
| `theme.Theme` / `theme.Load` | `internal/theme` | every colour, border and chrome literal |
| `chat.Source` | `internal/chat` | ACP `session/update` thread content -- **false as of `56513a2`, corrected 2026-08-23 (pass 2): `chat.ClaudeCodeSource` + `chat.FallbackSource` are the real implementations shipped (agent-tui#99); `chat.FixtureSource` is now only the last-resort fallback, not "today's" source** |

**Why this matters practically:** every package's tests construct a fake
implementing the seam, not a real subprocess. If you add a feature that needs
new external data, add it as a new field on an existing seam or a new
function-typed seam — never an `os/exec.Command` inside `internal/*` directly.
`internal/mcp` is the only package that knows it is talking to a subprocess;
everything above it knows only Go types.

## Running the tests

```
go build ./...
go vet ./...
go test ./...
```

All three verified green on `main` `6942926` (29 packages with tests,
`cmd/keelson`, `internal/sshserver`, and the `tools/` spikes have none).
**Stale as of `390c99a`, re-measured 2026-08-23 (`find . -name '*_test.go'`):**
`cmd/keelson` now has five `_test.go` files (`ledger_copy_test.go`,
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
go build -o keelson ./cmd/keelson
AGENT_SUPERVISOR_REPO=/path/to/agent-supervisor ./keelson
```

The board, cost and gallery screens are panes reached with `[f2]`/`[f3]`/
`[f4]` inside the one running process (`internal/shell`, agent-tui#38);
`-board`/`-cost`/`-gallery` now only choose which pane the app opens on.

**A binary that builds is not a feature that works.** `go test` exercises
`Model.Update` with synthetic key messages against fakes; it does not press a
key against a live tmux session. Before documenting a control as working,
either cite the test that drives it through `Update` (name it) or say
"not verified against a live session."

## Merging PRs you did not author

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
than a second-language copy of skills#255's Python). `390c99a` (#113)
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

## Conventions

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

## Known defects — do not paper over these

agent-tui#49 is **closed** (2026-08-16). All three of the defects it
originally recorded are fixed as of `6942926`, 2026-08-23 — re-confirmed by
running the actual binary and by grep, not by memory of the issue text:

1. ~~**Bare launch exits 1.**~~ Fixed. `./keelson` with no flags and no
   `$AGENT_SUPERVISOR_REPO` now opens in a degraded state on the Home pane
   instead of exiting (`cmd/keelson/main.go`'s `supervisorRepoResolved`
   handling, commented "agent-tui#49 item 1: a bare `keelson` must open,
   never exit 1"). Confirmed by running the built binary under a real TTY
   (`script -q ... ./keelson`): it renders the sidebar and Home pane rather
   than printing the old `no supervisor to connect to` message and exiting.
2. ~~**The board pane reports itself unavailable with no `-ledger`.**~~
   Fixed. `resolveLedgerSource` (`cmd/keelson/board.go`) now auto-discovers
   and stages a copy of the live ledger when `-ledger`/`$AGENT_TUI_LEDGER`
   is unset (`defaultLedgerLivePath` + `newLedgerCopier`); the old hard
   `boardOK == false` refusal only fires now when discovery genuinely finds
   nothing, not merely because the flag was omitted.
3. ~~**The cost panel's quota line is unwired from the current quota
   source.**~~ Fixed. `internal/cost/quota.go` now shells `quota.sh` out via
   `QuotaRunner`/`ExecQuotaRunner`, wired from `cmd/keelson/main.go`'s
   `resolvedQuotaBin` (`<supervisor-repo>/scripts/supervisor/quota.sh`).
   `renderQuota`'s `unknown (no quota source)` string (`internal/cost/
   view.go`) is now the honest fallback for a genuinely missing/failing
   `quota.sh`, not a structurally unwired source — confirmed by `grep -rn
   "quota.sh" --include='*.go' .`, which now returns matches throughout
   `internal/cost` and `cmd/keelson`.

This section is now a clean bill of health for agent-tui#49, not an open
punch list — if a regression reopens any of the three, restore the numbered
form above with fresh confirmation evidence rather than editing this prose
in place.

## What NOT to do here

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
  itself has not added a caller since #202 landed. Restoring the keys is
  now unblocked on the supervisor side but still not done here; do not
  read this note as permission to restore them without checking #202's own
  shape first.
- Do not point `-ledger` at the live supervisor's `ledger.sqlite3`. It is
  always opened read-only, but the flag help and `internal/board/ledger.go`
  both document why a copy is still required.
