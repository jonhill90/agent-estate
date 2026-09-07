# The TUI

*Relocated verbatim from the repo root `AGENTS.md` by the progressive-disclosure split (only heading levels and relative link paths were adjusted for the new location). `AGENTS.md` is the index that routes here; this file is the detail.*

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
in [decisions/0006](../decisions/0006-agent-tui-merges-into-agent-supervisor.md).

## What this repo is

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
internal/knowledge/  Jon's personal memory vault viewer -- reads $AGENT_MEMORY_VAULT's index.md + 01 - Notes/<12-digit-id>.md, progressive disclosure
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

## Adapter discipline

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

## Running the tests

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

## Merging PRs you did not author

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
[decisions/0013](../decisions/0013-tui-merge-gate.md) for why this
command exists and the self-approval bypass it had to close.

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
  <UTC>` stamp at the top; update it — replace it, don't stack a new one on
  top — when you re-check the claims below it.
- **Glyph sets and themes are data, not code** (`internal/lane/variants.go`,
  `internal/theme/registry.go`) — a new visual variant is a struct literal
  addition, never a new code path in a render function.

## Known defects — do not paper over these

agent-tui#49 is **closed** (2026-08-16); all three defects it recorded (bare
launch exiting 1, the board pane refusing with no `-ledger`, the cost
panel's quota line being unwired) are fixed — see
[tui/known-defects-49](../tui/known-defects-49.md) for what each was and
the fix evidence. If a regression reopens any of the three, restore the
numbered form there with fresh confirmation evidence rather than treating
this as closed by assumption.

## What NOT to do here

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
