# SPEC: the TUI shell — 1:1 with the hill90 web nav

Build order is the item order. **Each item is independently shippable as one
PR.** An agent picks the lowest-numbered item that is specified here and not
yet built, builds exactly that, opens a PR, stops.

## The target

The requirement: a real TUI with a left nav bar modelled on the hill90 web app —
including an admin section, agent threads, skills, MCP servers and connectors.
Build it 1:1 with the existing web nav first; rearranging comes after.

`ui_fidelity=1:1` is a **hard** parameter in the corpus. The source of truth is
`hill90-app/services/ui/src/components/nav-items.ts`, read as admin — Jon is
`hill90-ui:admin,user`, so every `adminOnly` entry is visible.

## What exists today (measured, do not rebuild)

- `internal/rail` (1369 LOC) — the lane rail. Keep. No web equivalent.
- `internal/board` (2391 LOC) — task board. Becomes **Tasks**.
- `internal/cost` (1170 LOC) — spend panel. Becomes **Usage**.
- `internal/gallery`, `internal/lane`, `internal/mcp`.
- The three screens are selected by mutually-exclusive process flags
  (`-board`, `-cost`), so only one is reachable per launch and nothing on
  screen says the others exist. **That is the bug this spec fixes.**

---

## S1 — Nav model (no rendering)

`internal/nav`: `Item{ID, Label, Icon, Kind}`, `Group{ID, Label, Children}`,
and `Tree()` returning the full structure below. Pure data + a `Flatten()` for
keyboard traversal. Table test asserting the tree matches this list exactly.

Top level: `Home` · `Dashboard` · `Agents` · `Chat` · `Tasks` · `Knowledge` · `Library`

Groups:
- **Build** — Skills · Workflows · MCP Servers
- **Connect** — Connections · Models · Storage · Discord · Secrets
- **Observe** — Usage · Monitoring
- **Docs** — API Docs · Platform Docs
- **Admin** — Services · Profiles · Users · Dependencies · Settings

Plus **Lanes** at top level — the one item with no web equivalent, because it
is what the web app cannot do.

**Deviation, w5f.md (post-S1):** Connect's own `Models` entry above was
REMOVED from `internal/nav`'s actual tree, not merely left unwired.
`internal/connectors.View` (S10) already renders a `-- models --` section
inside `Connections`, the exact same `AvailableModel` catalog data a
dedicated `Models` route would show -- confirmed by reading that view
directly. A nav entry that will never carry content of its own is worse
than no entry. This list is kept as written above for historical fidelity
to `ui_fidelity=1:1`'s own source (`nav-items.ts`); `internal/nav/tree.go`'s
own doc comment on that Group's `Children` is the current, accurate state.

## S2 — Sidebar component

Renders S1's tree in a left column. Collapsible groups; the group containing
the active route auto-expands (matches web `Sidebar.tsx`). Active item
highlighted. Fixed width, collapsible to icons-only with `[b]`.

## S3 — Shell + routing

An app shell owning `activeRoute`. `↑/↓` move, `Enter`/`→` selects, `←`
collapses, `Tab` moves focus sidebar↔content. Content pane renders the view
for the active route. **`-board` and `-cost` keep working** and simply preset
the route — nothing that scripts them may break.

## S4 — Wire the three existing screens

`Tasks` → `internal/board`. `Usage` → `internal/cost`. `Lanes` → `internal/rail`.
No behaviour change; they become routes instead of process flags.

## S5 — Honest stubs for everything else

Every remaining destination renders a named placeholder: the title, one line
saying what it will show, and `not built yet`. **A visible stub beats a hidden
screen** — the current failure is not knowing the board exists.

## S6 — Agents view

List agents from the supervisor daemon: id, model, state, current task, cost.
`[n]` creates a thread against an agent. Read-only until S7.

## S7 — Threads

A thread is a conversation with an agent. List pane + transcript pane +
composer. Sends go through the daemon (subprocess transport), **never tmux
send-keys**.

**Status, 2026-08-23 (estate-loop/agent-b3.md).** Read-only half done:
`internal/chat` has a real `Source` (agent-tui#99) backed by Claude Code
CLI's own local session transcripts; the nav walk records Chat as RENDERS.
Sending is **blocked, not built** — investigated, not skipped: neither
`agent-supervisor`'s MCP tool surface (`lanes`/`sessions`/`digest`/`ledger`/
`events`/`session_remove_check` reads, `session_attach`/`session_detach`/
`session_add`/`session_remove` writes — ten sources total, verified against
`supervisor_view.py`'s own `READ_SOURCES`/`WRITE_SOURCES`) nor
`daemon/cmd/supervisord run` (requires `-task ID -brief FILE`, shaped for
fresh ledger dispatch, not "continue this thread") nor any RPC/HTTP/socket
surface (`grep -rl "net/http\|http.ListenAndServe\|net.Listen" daemon/` is
empty) currently exposes a way to send an ad-hoc message to an existing
thread through the daemon's own subprocess-transport contract
(`daemon/internal/agent/claude.go`: a nil error means delivered, as a fact,
never a third state). Building a workaround (an agent-tui-side `os/exec`
reimplementing that transport, or shelling out to `supervisord run`) would
duplicate the transport a second time or route around the MCP boundary —
exactly what S7's own hard constraint exists to prevent, so neither was
done. Proposed shape of the missing daemon capability filed as
`agent-supervisor#508`. `internal/chat.Sender` (the write half of the
`Source`/`Sender` seam) stays declared and `nil` by default until that
capability exists to implement it against.

## S8 — Skills view

List skills from `~/.claude/skills` and the `skills` repo: name, description,
last eval result, invocation count. `[e]` runs its eval.

**Design note, 2026-08-22.** Skills are built dynamically and then evaluated;
each one is improved, renamed, or dropped on the strength of its evals. Skills
that are not yet in use are therefore expected — the missing piece is the eval
loop, not the skills themselves. Do not treat an uninvoked skill as dead code.

## S9 — MCP servers view

List configured MCP servers, scope (global/project), and reachability. Note
lanes launch `--strict-mcp-config` (#494), so this is about what a lane *may*
be given, not what it inherits.

## S10 — Connectors view

Provider connections and models. Mirrors web **Connect**.

## S11 — Admin section

Services, Profiles, Users, Dependencies, Settings. Read-only first.

## S12 — Execution mode (AgentBox)

The AgentBox container sandbox should be selectable as an execution mode
rather than being a separate product.

A per-agent `ExecutionMode`: `local` (subprocess in a worktree, today's
behaviour) or `container` (AgentBox docker sandbox). Surface the mode in the
Agents view and make it selectable at creation. **Spec the interface in this
item; implementing the container driver is its own later item.**

**2026-08-22 update (interface + LOCAL depth):** the interface
(`internal/session/execution_mode.go`, `AddWithMode`) and the Agents view's
MODE column both shipped in #79. The column's own initial cut hardcoded
every row to `ExecutionLocal` regardless of that row's own data — a
fabricated value, not a read — and was replaced with `internal/agents.modeFor`,
which reads real per-row evidence (`Command`, `State`) and renders `unknown`
when that evidence does not support an answer (never a guessed default). No
signal exists yet, anywhere in agent-supervisor's `lanes.sh --json` payload,
that can positively identify a container-wrapped process, so `modeFor` can
only ever emit `local` or `unknown` today — never `container` from
inference. Container-side design (what AgentBox needs before a driver can
exist at all) is `docs/SPEC-agentbox-execution-mode.md`, spec only, no code
— still its own later item, not started here.

---

## Rules for every item

- **Go only.** No shell scripts. The current TUI is 7,010 LOC Go and 110 LOC
  shell; keep that ratio.
- **`go build ./... && go test ./... && go vet ./...` must pass.**
- **Drive what you build** before claiming it works — launch it in a pty and
  press the keys. Screenshots of the pane in the PR body.
- **No tmux send-keys anywhere.**
- One item per PR. Do not start the next item.
- If an item is already built, say so and move to the next — do not rebuild it.
