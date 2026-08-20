# AGENTS.md — agent-tui

Repository policy for an agent arriving here. `CLAUDE.md` is a committed
symlink to this file — one source, two harness-visible names, no sync step.
Edit this file; never edit `CLAUDE.md` directly (it will edit the same bytes,
but say so in the commit message if you do, to avoid a reviewer thinking two
files drifted).

**Verified against `main` `b00db9b`, 2026-08-16 (agent-tui#38's shell PR,
#43).** Confirm the branch/SHA in `git log -1` still matches before trusting
counts below; they are measured, not estimated.

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
-i agent-tui | wc -l`), up from round 1's 438/72.

## What this repo is

This repo (Go module `github.com/jonhill90/keelson` — see the naming note
above) is one terminal application: a persistent left rail over
`agent-supervisor`'s lane/session state, with the task board, cost panel and
glyph gallery reachable as panes in the same process (`internal/shell`,
agent-tui#38). The name `agent-tui` describes the rendering technology (Go +
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
internal/board/      task board projection — GitHub issues/PRs + ledger tasks + live lanes
internal/chat/       ACP thread chat -- Source seam, FixtureSource, two viewport-scrollable layouts (agent-tui#20)
internal/cost/       per-harness spend/quota projection from ccusage
internal/flow/       live flow view — the same board.Snapshot re-projected as a moving pipeline (agent-tui#64)
internal/gallery/    glyph gallery — every lane state × every candidate glyph set
internal/lane/       lane/session decode, glyph sets (data, not code), state table
internal/mcp/        minimal MCP JSON-RPC client over a child process's stdio
internal/rail/       the left-anchored navigation rail — the one shipped anchor feature, now always visible
internal/session/    write path: attach/detach/add/remove, all via MCP, no os/exec
internal/shell/      the application shell -- owns the rail + board/cost/gallery/flow/chat as panes (agent-tui#38, #64, #20)
internal/theme/      look-and-feel as data — Role-keyed colours, persisted per-user config
scripts/             verify-lanes-unaffected.sh — the rail's non-interference proof
```

`internal/chat` is wired into the shell as `PaneChat` (`[f6]`, agent-tui#20) --
`[f5]` was already claimed by `internal/flow`'s `PaneFlow` (agent-tui#64) by
the time this rebased onto it.
It renders against `chat.Source`, an adapter seam the same shape as
`rail.Fetcher`; `chat.FixtureSource` is the only implementation shipped
today because no lane in this estate runs on a structured transport
(`acp`/`pi-rpc`) yet — see `internal/chat/fixture.go`'s own doc comment for
why a screen-scraped transcript was rejected instead, and what a real
`Source` needs.

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
| `chat.Source` | `internal/chat` | ACP `session/update` thread content -- `chat.FixtureSource` today, a real ACP/pi-rpc client once one exists |

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

All three verified green on `main` `b00db9b` (9 packages with tests,
`cmd/keelson` has none). CI (`.github/workflows/*.yml`) runs the same three
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

agent-tui#49 (open) records three, each confirmed live at `b00db9b`,
2026-08-16 by running the actual binary, not by reading the source:

1. **Bare launch exits 1.** `./keelson` with no flags and no
   `$AGENT_SUPERVISOR_REPO` prints `no supervisor to connect to: set
   -supervisor-repo, $AGENT_SUPERVISOR_REPO, or -mcp-cmd` and exits 1 instead
   of opening in a degraded state. Confirmed: `go build -o /tmp/keelson-check
   ./cmd/keelson && /tmp/keelson-check` exits 1 with that message.
2. **The board pane reports itself unavailable with no `-ledger`.**
   Reaching it via `[f2]` (rather than `-board`, which refuses to start
   first) renders `! unavailable` / `no -ledger (or $AGENT_TUI_LEDGER)
   configured -- point it at a COPY of the ledger to use the board`
   (`cmd/keelson/main.go`'s `boardUnavailable` string).
3. **The cost panel's quota line is unwired from the current quota
   source.** It renders `unknown (no quota source)`
   (`internal/cost/view.go`) whenever `ccusage` has no local blocks/limit
   concept for a harness, even though `scripts/supervisor/quota.sh` is
   the quota source now — confirmed by `grep -rn "quota.sh"
   --include='*.go' .` returning zero matches anywhere in this module.

Fix or documentation update for any of these is in scope; silently working
around one in a new feature is not.

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
  `internal/shell` calls them as of `b00db9b` (`grep -rn "\.Attach(\|\.Detach("
  --include='*.go' .`, outside test files: zero matches).
- Do not point `-ledger` at the live supervisor's `ledger.sqlite3`. It is
  always opened read-only, but the flag help and `internal/board/ledger.go`
  both document why a copy is still required.
