# SPEC: container execution mode (AgentBox)

Verified against the sibling `AgentBox` repo (`github.com/jonhill90/AgentBox`)
at its current `main` tip, 2026-08-22, by reading its own docs/architecture/overview.md,
docker-compose.yml, src/jumpbox_tools.py and src/policy.py directly, not by
recalling what the project is expected to do. This is a **spec, not code** —
`docs/SPEC-shell.md` S12 named implementing the container driver as its own
later item; this document is that item's design brief, and none of it should
be read as already true of this repo or of agent-supervisor.

**2026-08-23 update (agent-tui#105):** re-verified against AgentBox's
current `main` tip (`f71a9f0`, no commits since 2026-08-22) and this repo's
own current tree (`cff10f9`) — see "Settled / adequate already" and
"#105's three genuinely open questions" below for what changed (one new
AgentBox file, `src/git_credentials.py`) and what did not.

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

## Settled / adequate already

Not one of #105's three open questions — the interface and the eventual
TUI-side signal are both already scoped correctly, and neither should be
re-litigated:

- **agent-tui's own interface is done.** `ExecutionMode`/`AddWithMode` (this
  file) and `internal/agents.modeFor` (`internal/agents/row.go`) already read
  real per-row evidence and answer `unknown` rather than guessing — see the
  package's own doc comment for the history of the bug this fixed.
- **What `lanes.sh` would need to add, once there is a signal to attach, is
  scoped.** Re-read directly against the current tree, 2026-08-23
  (`cff10f9`): the `--json` payload is still exactly seven fields —
  `window`, `window_id`, `name`, `command`, `state`, `idle_seconds`, `model`
  (`scripts/supervisor/lanes.sh`'s `--json` case) — nothing added since
  2026-08-22, and `grep -rn "docker\|agentbox\|execution_mode\|container"
  scripts/supervisor/*.sh scripts/supervisor/*.py` returns zero matches. The
  measured constraint in this doc's own prior revision **still holds**: no
  signal exists anywhere in that payload that could distinguish a
  container-wrapped process from a native one, so `modeFor` can only ever
  emit `local` or `unknown` today. The proposed fix, once questions 1/2 below
  are answered, is a new appended column (e.g. `execution_mode`, written by
  whatever dispatch mechanism created the lane) — the same pattern
  agent-supervisor#115 used adding `model` as a 7th column without breaking
  positional consumers. `modeFor` would read that field directly, the same
  way it already treats `Model` as self-reported rather than inferred.
  **Explicitly not the answer:** guessing from `Command` alone — a container
  entrypoint could re-exec into a harness whose `pane_current_command` looks
  identical to a local one, so the two are distinguishable only if whatever
  created the lane says so.

## #105's three genuinely open questions

Each answered below with what is true today and, per #105, what would have
to become true for the answer to change — none of the three are agent-tui's
to resolve unilaterally.

### 1. How does an agent in a Docker sandbox get the repo?

**Still not solved, and the evidence for why has partially shifted since
this doc's prior revision.** Re-checked against AgentBox's current `main`
tip, 2026-08-23 (`f71a9f0`, unchanged since 2026-08-22 — no new commits):
`GIT_ACTIONS` is still exactly `("init", "status", "add", "commit", "diff",
"log", "reset")` — no `clone`, no `fetch`, no `push` — and
`docker-compose.yml`'s top-of-file invariant is still literally "No Docker
socket, no external network."

What changed: `src/git_credentials.py` now exists (SPEC §16, wired in via
`mcp_server.py`'s `git_credentials.configure()` at startup) — a real,
carefully-scoped **push**-credential mechanism (file-backed, never in the
environment, read-only credential helper so nothing in the container can
overwrite it). This narrows part of the original objection ("adding push
means real credential-leak risk") but does not answer question 1: it
handles authenticating an already-open git operation, not getting a repo
into `/workspace` in the first place, and it is not wired into
`GIT_ACTIONS` at all — an MCP-tool-driven agent still cannot invoke
`clone`, `fetch`, or `push` through `execute_git`. The two paths this doc
already named are otherwise unchanged: bind-mount a host worktree
(cheapest, but defeats `PathPolicy`'s `/workspace`-scoping guarantee), or
add a reviewed `clone`/`fetch` action plus network egress (a real loosening
of the stated no-network invariant, now somewhat less alarming for the
push side specifically given `git_credentials.py`'s design, but still
unaddressed for read access).

**What would have to become true for this to change:** an AgentBox
`docs/SPEC.md` decision — Jon's, per AgentBox's own "spec first" rule and
because it touches a stated invariant — choosing bind-mount or
clone-plus-egress (or a third option not yet named), followed by the
corresponding code change in AgentBox itself. Nothing on the agent-tui or
agent-supervisor side can produce this signal without that decision
happening first.

### 2. How does its output reach the daemon?

**Still not solved; nothing new to build on.** Re-grepped AgentBox's `src/`
for `webhook`, `callback`, `report_status` on 2026-08-23: zero matches,
same as 2026-08-22 — no push/callback surface has been added. agent-
supervisor's ledger, dispatch, and `lanes.sh` still assume a tmux pane it
can `capture-pane`, poll, and classify; AgentBox still has no tmux-facing
surface for a driven agent (its bundled tmux/zsh backs only its own
human-facing `/ui` terminal tab) and its MCP tools are still pull-only —
Claude Code or the `/ui` viewer calls them, they never push status
anywhere.

**What would have to become true for this to change:** two things, neither
built:

- AgentBox-side, a written convention for a status path an agent inside the
  container writes to (inside `/workspace`, the one location its tools can
  already reach).
- agent-supervisor-side, a new poller (`dispatch.sh` or a sibling script)
  that reads that path via AgentBox's own MCP tools on a cadence, the same
  role `lanes.sh` already plays for tmux panes — per this repo's own
  `AGENTS.md`, this is squarely a supervisor-side change with an agent-tui
  caller added after, not something this repo builds first.

### 3. Concurrency / threat-model gap

**Not evaluated; the evidence still points the same direction it did before
this question was split out as its own item.** AgentBox's own docs still
state the posture plainly: `CLAUDE.md` — "Local-only, single operator,
Phase 1" (line 11, re-read 2026-08-23); `docs/architecture/security.md` —
"Phase 1 is local Docker only, single operator" (line 9), and that same
document names a concrete single-shared-resource failure mode directly:
"Concurrent `navigate` calls to the one shared page make Chromium abort"
(line 224) — one `Page` per container, dispatched into from one background
asyncio loop (this doc's "What AgentBox is today" section above).
`docker-compose.yml` defines exactly one service, `agentbox`, with no
scaling or multi-instance pattern. None of this has changed since the prior
revision of this doc; it has also never been evaluated for what
agent-supervisor's real dispatch load looks like — N concurrent supervised
agents, each wanting its own container.

**What would have to become true for this to change:** a written evaluation
in AgentBox's own docs (per its own "spec first" convention) of whether its
current one-container/one-Page model holds under N concurrent instances —
e.g. N separate `agentbox` containers each with their own Page and volume,
versus something that shares state across containers today and would need
to change. That evaluation has not been done and is not this document's or
agent-tui's to perform; AgentBox's own threat model has to say so, since it
is the component being evaluated.

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
- AgentBox is explicitly single-operator, Phase 1, local-only — see
  "#105's three genuinely open questions" → "3. Concurrency / threat-model
  gap" above for the current evidence and what would have to become true
  before this is a viable dispatch target.

## What this document explicitly does not do

No code changes anywhere (neither this repo nor AgentBox nor agent-supervisor).
No decision is made here about which of question 1's two paths (bind mount
vs. `clone`/`fetch` + network egress) is correct — that is an AgentBox
`docs/SPEC.md`-owned decision, per its own "spec first" rule, and probably a
conversation with Jon given it touches AgentBox's stated "no Docker socket,
no external network" invariant. This document exists so that conversation
starts from what is actually true today, not from an assumption about what
AgentBox already does.
