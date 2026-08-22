# SPEC: container execution mode (AgentBox)

Verified against `/Users/jon/source/repos/Personal/AgentBox` at its
current `main` tip, 2026-08-22, by reading its own docs/architecture/overview.md,
docker-compose.yml, src/jumpbox_tools.py and src/policy.py directly, not by
recalling what the project is expected to do. This is a **spec, not code** —
`docs/SPEC-shell.md` S12 named implementing the container driver as its own
later item; this document is that item's design brief, and none of it should
be read as already true of this repo or of agent-supervisor.

## Where this picks up

`internal/session/execution_mode.go` already ships the interface: a per-agent
`ExecutionMode` (`local` | `container`), and `AddWithMode(..., ExecutionContainer)`
returns `ErrContainerNotImplemented` rather than silently creating a local
subprocess. `internal/agents`' Mode column (this PR) reads real per-row
evidence and renders `unknown` when it has none — it is not, and cannot yet
be, ExecutionContainer, because nothing anywhere in this estate can currently
produce that signal (see "What AgentBox is today" below). This document is
what has to become true before either of those can move.

## What AgentBox is today

Read directly, not assumed:

- **A standalone MCP server**, one Python process, one Docker container,
  `docker-compose.yml`'s own service named `agentbox`. It serves Streamable
  HTTP MCP (`/mcp`), a REST mirror for its own browser viewer (`/api/*`), the
  viewer itself (`/ui`), and a PTY-over-WebSocket terminal (`/terminal`) —
  `docs/architecture/overview.md`'s own request-path diagram.
- **Its core feature is a persistent Playwright browser page**, not agent
  orchestration: one `Page` lives for the life of the container, dispatched
  into from a background asyncio loop, so DOM/JS/cookie state survives across
  separate tool calls and separate MCP client connections.
- **Filesystem/git/HTTP tools exist, scoped to `/workspace`** behind a
  `PathPolicy` realpath check (`src/policy.py`), toggled by
  `AGENTBOX_ENABLE_JUMPBOX_TOOLS`. Git is **not** a general passthrough —
  `src/jumpbox_tools.py`'s own `GIT_ACTIONS = ("init", "status", "add",
  "commit", "diff", "log", "reset")`. There is no `clone`, no `push`
  in that fixed list (docker-compose.yml documents git-push credentials as a
  separate, unimplemented-here SPEC §16 concern) — confirmed by reading the
  tuple itself, not by inference from the surrounding comments.
- **`/workspace` is a named Docker volume** (`agentbox-workspace:/workspace`
  in `docker-compose.yml`), not a bind mount of any host path. It is durable
  across container restarts but starts **empty** on a fresh volume — nothing
  seeds it with any repo today.
- **Zero knowledge of agent-supervisor.** No ledger, no lane concept, no
  dispatch, no tmux (the container installs tmux/zsh only so its *own*
  `/terminal` viewer "looks like the operator's own" —
  `docs/architecture/overview.md`'s "Container" section — not to run a
  supervised agent inside it). No webhook, callback, or any other
  status-reporting mechanism back to a caller exists in `src/` at all
  (grepped `src/` for "webhook"/"callback"/"report_status", zero matches,
  2026-08-22).
- **Auth is off by default**; the terminal is off by default and requires a
  token even when enabled. Single operator, local-only, Phase 1 — its own
  `CLAUDE.md` says this explicitly.

## The three questions SPEC-shell.md S12 asked

### 1. How does an agent in a Docker sandbox get the repo?

**Not solved today.** AgentBox's own git tool set cannot clone or fetch
(`GIT_ACTIONS` above has no `clone`), and `/workspace` starts as an empty
named volume with no bind mount to any of this estate's `.worktrees/`
layout. Building this needs, at minimum:

- Either a bind mount of a real host worktree into the container (matching
  what a local lane already gets — a worktree agent-supervisor's
  `worktree.sh` created) — cheapest to implement, but ties a "container"
  agent back to a host filesystem path, which weakens the isolation
  AgentBox's own threat model (`docs/architecture/security.md`) is built
  around: the whole point of `PathPolicy` scoping every tool to
  `/workspace` is defeated if `/workspace` is secretly a live host worktree
  another process (agent-supervisor's own gc, `_gc_process_refs` —
  agent-supervisor#498, reviewed the same day this doc was written) can
  observe or remove out from under it.
- Or `execute_git` gaining a `clone`/`fetch` action (a reviewed addition to
  `GIT_ACTIONS`, per that file's own comment: "not a general passthrough...
  adding one is a reviewed change, not a parameter") plus network egress to
  reach `origin` — AgentBox's `docker-compose.yml` states "No Docker socket,
  no external network" as a top-of-file invariant today, so this is not a
  free addition; it is a real, reviewed loosening of AgentBox's own current
  posture, not a agent-tui-side decision to make unilaterally.

Either path is an AgentBox-repo decision (its own `CLAUDE.md`: "spec first,"
operator writes `docs/PRD.md`/`docs/SPEC.md` sections before work is built
against them) — not something this document can resolve by itself.

### 2. How does its output reach the daemon?

**Not solved today; nothing exists to build on.** agent-supervisor's ledger,
dispatch, and `lanes.sh` all assume a tmux pane it can `capture-pane`,
poll, and classify into a `state=`. AgentBox has no tmux-facing surface for
an agent's own turns (its bundled tmux/zsh is for the human-facing `/ui`
terminal tab, not for a driven agent) and no push/callback path of any kind.
A driver would need agent-supervisor's side to originate a genuinely new
signal — most plausibly, dispatch.sh (or a new sibling script) polling
AgentBox's own MCP tools directly (e.g. a `read_file` against a known
status path the agent writes to inside `/workspace`) on the same cadence
`lanes.sh` already polls tmux, since AgentBox exposes no event/webhook
mechanism to push through instead. This is squarely an agent-supervisor
change (per this repo's own `AGENTS.md`: "tmux orchestration, the ledger,
dispatch... is a supervisor change with an agent-tui caller added after"),
and by extension an AgentBox change too (whatever status file/convention the
poll reads has to be written from inside the container by something).

### 3. How does the TUI tell the two apart?

This is the one piece already staged correctly, and the only one this repo
should build: **`lanes.sh` (or its container-mode successor) has to emit a
real, per-row signal** — this document's read of the current `--json`
payload (`window`, `window_id`, `name`, `command`, `state`, `idle_seconds`,
`model` — confirmed by reading `lanes.sh`'s own `--json` emission directly,
agent-tui's own `internal/agents/row.go` `modeFor` doc comment cites the same
check) carries nothing that could distinguish a container-wrapped process
from a native one. Once agent-supervisor's dispatch path can create a
container-mode lane at all (questions 1/2 above), the cheapest correct
signal is a new column on that same payload — e.g. `execution_mode` written
by whatever dispatch mechanism created the lane, the same way `model` was
added as an appended 7th column by agent-supervisor#115 without breaking any
positional consumer. `internal/agents.modeFor` (this PR) would then read
that field directly instead of inferring from `Command`/`State`, the same
way it already treats `Model` as a self-reported field rather than something
to infer.

**Explicitly not the answer:** guessing from `Command` alone. A container
entrypoint could easily re-exec into a harness process whose reported
`pane_current_command` looks identical to a local one; the two are
distinguishable only if whatever created the lane says so, not by pattern-
matching the process name after the fact.

## What is reusable from AgentBox, and what is not

**Reusable:**

- The container base image discipline: Debian slim, non-root via an
  entrypoint that fixes volume ownership then drops privileges
  (`scripts/entrypoint.sh`, `setpriv --inh-caps=-all`) — the exact posture
  a container-mode agent-supervisor lane would want, and already proven
  running in this estate today (AgentBox is a live, working container).
- `PathPolicy`'s realpath-resolution scoping (`src/policy.py`) as the model
  for whatever confines a container-mode agent to its own workspace, if a
  future driver wants defense-in-depth beyond Docker's own filesystem
  isolation.
- The `_tool()`/`_api_route()` registration-time auth-guard pattern
  (`src/mcp_server.py`) if a container driver ever needs its own
  authenticated control surface distinct from the MCP tools an agent uses.

**Not reusable, or reusable only after a change:**

- The git tool set (`execute_git`) as it stands cannot get a repo into the
  workspace — no `clone`, no bind mount today. This is the actual blocker
  for question 1, not a detail to smooth over.
- The named-volume workspace model is wrong for this use case as-is: a
  supervised agent's container needs to start FROM a specific worktree's
  content (or a fresh clone of a specific branch), not an empty persistent
  volume that outlives the container regardless of which lane last used it.
- Nothing in AgentBox's own request surface reports status anywhere. Its
  MCP tools are pulled by a client (Claude Code, or the `/ui` viewer); they
  never push. A container-mode lane's liveness/output signal has to be
  built new, on the agent-supervisor side, using AgentBox's tools as the
  read target — not adapted from anything AgentBox already does, because
  AgentBox was never built to be watched by a second process the way a
  tmux pane already is.
- AgentBox is explicitly single-operator, Phase 1, local-only (its own
  `CLAUDE.md`) — nothing in its threat model has been evaluated for running
  N concurrent supervised agents' containers side by side, which is what
  agent-supervisor's actual dispatch load looks like. That evaluation has
  to happen before this is a dispatch target, not an assumption this
  document should paper over.

## What this document explicitly does not do

No code changes anywhere (neither this repo nor AgentBox nor agent-supervisor).
No decision is made here about which of question 1's two paths (bind mount
vs. `clone`/`fetch` + network egress) is correct — that is an AgentBox
`docs/SPEC.md`-owned decision, per its own "spec first" rule, and probably a
conversation with Jon given it touches AgentBox's stated "no Docker socket,
no external network" invariant. This document exists so that conversation
starts from what is actually true today, not from an assumption about what
AgentBox already does.
