# agent-supervisor — SPEC

Historical record, not a live spec. It gathers the contracts that already
exist as headers, docstrings, and schema comments scattered across
`scripts/supervisor/` into one place — it does not restate them from
scratch, and it does not invent behavior the code does not have. If a cited
file has moved past what is written here, the code wins; that is why every
section below is dated.

`Verified 2026-08-20` on every claim below means: read from the cited file
in this repository on that date. Where a claim depends on a count that
drifts easily (a number of states, a number of tables), the source is named
so it can be re-measured rather than trusted.

See `docs/product/PRD.md` for what this project is for and refuses to do.
This document is the contracts underneath that.

## 1. The ledger schema (`ledger.sqlite3`)

`scripts/supervisor/core.py`'s `_initialize` is the schema's source of
truth — every table below is `CREATE TABLE IF NOT EXISTS`, so a running
ledger only ever gains columns via the migration functions next to it,
never loses one silently. `Verified 2026-08-20` against `core.py:320-620`.

| Table | Primary key | Holds |
|---|---|---|
| `lanes` | `lane` | One row per lane identity (`<session>:<index>`): which pane it lives in, which harness drives it, which transport delivers work to it, and (`harness_session_id`, `harness_project_dir`) the two facts `restore.sh` needs to resume the agent's own conversation after a tmux loss. |
| `tasks` | `id` | One row per dispatch: which lane it went to, its lifecycle status (`created` → `delivery_pending` → `delivered` → `accepted` → `running` → `complete`/`failed`/`cancelled`), and the worktree it ran in. `one_open_task_per_lane` is a unique index, not application logic — a lane physically cannot hold two open tasks in the ledger. |
| `source_tasks` | `id` | One row per GitHub issue or PR the system has dispatched against (`source_kind`: `issue` or `pull`), so a second dispatch against the same source is a lookup, not a guess from a branch name. |
| `events` | `key` | Outbound notifications (Telegram, etc.) queued against a task, with their own `pending`/`notified`/`acked` lifecycle — decoupled from task status so a delivery failure doesn't corrupt the task record. |
| `components` | `name` | Last-known health of a named subsystem (poller, watchdog, ...), for `digest.sh` to report without re-probing everything live. |
| `pr_verdicts` | `(repo, number)` | The review verdict (`approved`/`rejected`) recorded against a PR's head SHA, read by `verdict.py` and consumed by the merge gate. |
| `pr_authorship` | `(repo, pr_number)` | Which task's work opened this PR — recorded best-effort, after the fact, by `lane-done.sh` (agent-supervisor#308), because an issue-keyed dispatch has no PR number yet when it starts. |
| `pr_external_authorship` | `(repo, pr_number)` | A PR explicitly marked as authored outside the lane system (`mark-pr-external.sh`), so the independence gate does not refuse a PR it was never meant to evaluate. |
| `sessions` | `session` | Which tmux sessions are under supervision, and since when. |
| `prompts`, `items`, `links` | — | Jon's prompt/decision tracking system (agent-supervisor#280); unrelated to lane dispatch. |
| `supervisor_lease` | `id` (singleton, `CHECK (id = 'supervisor')`) | Not in this table originally — added here 2026-08-23. A single-row lease (`owner`, `taken_at`, `updated_at`) for the supervisor process itself, same INSERT-or-refuse-under-lock/pid-liveness-reap pattern as a lane claim, deliberately kept out of `lanes` because the supervisor has no pane for a lane row to describe (`core.py:404-422`). |

`harness` is constrained to `codex`, `claude`, `copilot`, `copilot-acp`,
`pi`. `transport` is constrained to `send-keys`, `acp`, `pi-rpc`,
`claude-print` — see [`docs/decisions/`](../decisions/) for why
`claude-print` exists alongside `send-keys` rather than replacing it.

All ledger access goes through `core.py`'s locking: a transaction wrapped
in `fcntl.flock` on a `.lock` file, `BEGIN IMMEDIATE`, full `synchronous`,
WAL journaling. There is no network dependency and no server process —
concurrent readers/writers (dispatch, watchdog, a worker script) all open
the same SQLite file directly.

## 2. Lane states (`lanes.sh`)

`lanes.sh` classifies every pane in a session into exactly one state; it is
a whitelist, not a heuristic (`AGENTS.md` invariant 6 — `unknown` means "not
offered", never "probably fine"). `Verified 2026-08-20` by grepping every
`state=<word>` assignment in `scripts/supervisor/lanes.sh`, fourteen
distinct states:

| State | Means |
|---|---|
| `free` | Idle, matches the harness's ready shape — available to dispatch. |
| `busy` | Mid-turn, matches the harness's busy marker. |
| `hung` | Looked busy, but tmux has seen no output for `HUNG_AFTER` seconds (default 180s) and there is no live child process — a busy shape can lie once the process under it has died. |
| `never-busy` | Shape is unrecognized by any harness adapter, and has been idle since launch for `NEVER_BUSY_AFTER` seconds (default 1200s) — distinguished from plain `unknown` because "unrecognized and it's been 20 minutes" is worth a human's attention differently than "unrecognized and it just started." |
| `menu-blocked` | Waiting on an interactive menu prompt (`HARNESS_OPTION_ROW_RE` / `HARNESS_MENU_ENTER_RE`). |
| `text-blocked` | Waiting on a text-entry prompt (`HARNESS_TEXT_PROMPT_RE`). |
| `unsent` | A brief was typed into the input box but never submitted. |
| `dead` | No agent process — just a bare shell (`SHELLS` regex). |
| `stale` | A `dead` shell whose window name still names a task — the ledger's record and the window's label disagree. |
| `scrolled` | Pane is in tmux copy mode; keys sent are eaten by copy-mode bindings rather than reaching the agent. |
| `broken` | The pane's working directory no longer exists on disk. |
| `service` | Running a supervisor service (e.g. `inbox-poll.sh`), not a worker — never offered, never dispatched to (see [`docs/decisions/`](../decisions/) and `AGENTS.md` invariant 8, "the poller is a service, not a lane"). |
| `supervisor` | The window's own `#{window_id}` matches the supervisor's, resolved by `session-defaults.sh`'s `supervisor_window_id` (agent-dotfiles#239) — falls back to comparing `#{window_index}` against `LANES_SUPERVISOR_WINDOW` (default window 1) only when no id resolves. The supervisor's own pane. |
| `unknown` | Shape matches no adapter and no other rule above — left alone, reported, never guessed at. |

Every renderer under `laneview/` is required to name every one of these
states explicitly in its own map (`validate_laneview_state_maps` in
`scripts/validate_repository.py` errors if `lanes.sh` grows a state a
renderer's map doesn't name) — see
`scripts/supervisor/laneview/README.md`, item 4, for the incident
(#231) that made silent fallthrough the thing being guarded against: a
`scrolled` lane fell through a renderer's `*) echo idle` and drew as a
healthy green tick.

## 3. Dispatch (`dispatch.sh`)

Given an issue number (or a `--pr` number), a slug, a brief file, and a
repo, `dispatch.sh`:

1. Reaps stale lane claims and checks the quota gate.
2. Picks a free lane from `lanes.sh --free`; for a `--reviews-pr <PR>`
   dispatch, refuses any lane that authored the PR (union of
   `issue-lane`, `author-issue-lane`, `contributor-issue-lanes`, and
   `worktree-lane` — every way a lane could plausibly be that PR's
   author).
3. For a fresh, single-issue, non-review, non-`--pr`-scoped `claude`
   dispatch, routes to a `claude-print` lane instead of a live tmux pane
   by default (agent-supervisor#171) — `--live-pane` opts back into a
   watchable terminal.
4. Claims every issue in the brief (`claim.sh take`), not just the first
   (agent-supervisor#116).
5. Creates a fresh git worktree (`worktree.sh new`) and refuses on a file
   collision with another in-flight lane's worktree unless `--force`.
6. Registers the lane and records the dispatch in the ledger — lane,
   task, worktree path, and (for tmux lanes) the harness's own
   conversation id, all in the one write.
7. Delivers the brief through `send.sh`'s verified type/submit primitive,
   never bare `send-keys`.

An unreadable ledger is a refusal, never an assumption of availability
(`AGENTS.md`, "fail closed"; `dispatch.sh`'s own step-0 guard). Exit 0 =
brief delivered; exit 1 = refusal (no free lane, already claimed, worktree
failure, authorship guard); exit 2 = usage error.

## 4. Completion (`lane-done.sh`)

Fixed order, and the order is the point (`README.md`, "Order of
operations"):

1. Block on a bare `tmux wait-for` channel until the worker signals
   `wait-for -S` — the bare form blocks until signaled; `-L` would lock
   and return immediately on a channel nobody has raised yet.
2. Verify the window name still matches the expected task name.
3. Record completion in the ledger (best-effort; a failure here is
   logged, not fatal to the rest of the sequence).
4. Best-effort: record PR authorship from the worktree's metadata.
5. Rename the window to `free-<index>` — read fresh, not from the
   original dispatch argument — as the last step, because it is
   cosmetic. A crash before step 3 leaves the ledger open (lane is
   retried, safe); a crash after step 5 but with step 3 done leaves only
   a stale label.

## 5. The merge gate (`merge-pr.sh`)

The only path in this repo meant to merge a PR — `gh pr merge` run
directly bypasses it; that is convention, not a platform block, because
GitHub branch protection and rulesets are both unavailable on these
private repos without GitHub Pro (`gh api .../branches/main/protection` →
403, measured in `merge-pr.sh`'s own header, agent-supervisor#13). It
chains **three** gates (originally documented as two here; corrected
2026-08-23 — see item 2 below), all fail-closed — refusing when a gate
returns false *or* when a gate cannot be evaluated at all:

1. **`ci_gate.py`** — re-fetches the PR's head SHA fresh (never a cached
   one), checks every check-run and legacy status at that exact SHA, and
   refuses if any is outside `GREEN_CONCLUSIONS` (`success`, `neutral`,
   `skipped`) or if the SHA has zero check results (absent is not
   passing).
2. **The rejected-verdict check** (`merge-pr.sh:123-135`, added by
   agent-supervisor#486/PR #487, commit `cb54804`, 2026-08-21 — not in this
   document before this pass) — refuses outright when the recorded verdict
   itself is `rejected`, checked before independence. Added because
   `independence_verdict()` answers "was this reviewed independently", not
   "was it approved": both `approved` and `rejected` satisfy its own
   `IN("approved","rejected")` branch, so a rejected-but-independent review
   used to pass the old `value == true` gate the same as an approved one —
   reproduced live on PR #485 before the fix.
3. **`verdict-independence.sh`** — reads the PR's verdict
   (`verdict.py`, ledger or GitHub, never inverts an unreadable result
   into a pass) and the lane that authored it, resolves whether reviewer
   and author are the *same lane* via `resolve_lane_relation` (comparing
   lane identity, not task ids or window names — `AGENTS.md` invariant
   9), and refuses an unreviewed, self-reviewed, or unresolvable pairing.

Exit 0 = all three gates passed and `gh pr merge` ran; exit 1 = a gate
refused (reason printed); exit 2 = usage error.

## 6. Harness adapters (`adapter.py`, `harness/*.sh`)

Adding a harness is a new file under `scripts/supervisor/harness/`;
`lanes.sh` itself carries no harness-specific string (`AGENTS.md`
invariant 7). Each `harness/<name>.sh` supplies, at minimum:

- `HARNESS_NAME`, `HARNESS_COMMAND_RE` — identity and how to recognize
  the pane's running process.
- `HARNESS_LAUNCH_CMD`, `HARNESS_RESUME_CMD` — how to start fresh and how
  to resume a specific conversation id (a harness with no resume dialect,
  e.g. `codex` today, is exactly the case `restore.sh` reports
  `UNRECOVERABLE` rather than guessing at).
- `HARNESS_READY_RE`, `HARNESS_BUSY_RE`, `HARNESS_BUSY_TAIL` — the idle
  and busy shapes `lanes.sh` matches against, and how many trailing lines
  to read doing it (Codex's busy marker sits above a static footer, so it
  needs more than the one line Claude and Copilot need).
- `HARNESS_OPTION_ROW_RE` / `HARNESS_MENU_ENTER_RE` /
  `HARNESS_TEXT_PROMPT_RE` — the blocked-on-a-prompt shapes.
- `HARNESS_SEND_LITERAL` — whether delivering text to this harness needs
  `send-keys -l`.

`adapter.py` picks an adapter class by `transport`, not by `harness`
alone: `TmuxAdapter` for `send-keys` lanes (register, verify the pane
still matches what was registered, assign a task, observe for attention
needed); `ACPAdapter` for `copilot-acp` (spawns a transport per call,
blocks for a structured response); `PiRPCAdapter` for `pi`;
`ClaudePrintAdapter` for `claude-print` (dispatch-and-collect, off-pane,
no input box to strand text in).

## 7. The `laneview/` renderer contract

Documented directly in `scripts/supervisor/laneview/README.md`; gathered
here rather than restated. A renderer is `laneview/<name>.sh`, invoked as
`<name>.sh <session> <lanes.sh --json output>`, and must:

1. **Read, never write.** The `lanes.sh --json` payload it was handed is
   the only ground truth available to it. A renderer must never decide "this
   lane is free" and act on that (dispatch, claim, rename) — that stays
   `dispatch.sh` and `claim.sh`'s job.
2. **Degrade to absence, not staleness.** An unreachable backend must
   say so and exit nonzero, never show a stale render as current.
3. **Cost nothing when unused.** No renderer may be sourced by, or run
   as a dependency of, any headless script (`dispatch.sh`, `watchdog.sh`,
   `notify.sh`, ...). `laneview.sh` is only ever invoked by a human-facing
   process.
4. **Name every state.** See §2 above.

**Four renderers exist today** (originally documented as two here;
corrected 2026-08-23, `ls scripts/supervisor/laneview/`): `text.sh` (a
plain stdout table — works over SSH or cron, no tmux client needed),
`opensessions.sh` (a tmux-plugin bridge), `dock.sh` (a docked vertical
tmux-pane sidebar refreshing on a timer, zero extra dependency — built on
tmux primitives already in this estate rather than a plugin, per its own
header), and `tui.sh` (a curses TUI Jon owns directly — select a lane,
enter jumps to it — distinct from `opensessions.sh`'s third-party daemon,
per its own header). None is required by any other, or by `lanes.sh`.

## 8. The MCP read surface (`mcp_server.py`)

**Eleven tools total today, not the nine originally documented here**
(corrected 2026-08-23; counted directly from `supervisor_view.py`'s
`READ_SOURCES`/`WRITE_SOURCES` dicts, `scripts/supervisor/supervisor_view.py:512-515,1010-1019`).
Six read tools: `lanes`, `sessions`, `digest`, `ledger`, `events`, and
`session_remove_check` (not documented here before this pass — a pure
read, evaluates every refusal `session_remove` would, so a caller can
check repeatedly before ever writing), so a harness other than the one
running the supervisor can still consume its state. Five guarded,
explicitly-scoped session-management writes (agent-tui#14 for the
original four; agent-supervisor#508/PR #509, commit `b30b70e`, for the
fifth): `session_attach`, `session_detach`, `session_add`,
`session_remove`, and **`session_send`** — each takes an exact session
name, logs to the ledger before mutating, and re-checks its guard at call
time. `session_send` closes what its own docstring calls "the capability
agent-tui's SPEC-shell.md S7 is blocked on": sending one ad-hoc message to
an *existing* agent session, via `supervisord send -session-id ID -message
TEXT` (`daemon/cmd/supervisord`) — before this, nothing in the MCP surface
could resume an existing session; any earlier claim that no such write
path exists is now false. `dispatch` and `merge` are still deliberately
excluded: `supervisor_view.py`'s `WRITE_SOURCES` docstring reasons that
dispatch requires an atomic lane claim under a race, and merge carries the
independence requirement in §5 above — neither reduces to a scoped,
logged, single-session mutation the way session add/remove/send does.

## 9. Worktrees (`worktree.sh`)

One git worktree per lane, branch named `lane/<slug>`. `worktree.sh new`
creates it; `worktree.sh done <path>` removes it but refuses a dirty tree,
a detached HEAD with no containing branch, or the live worktree
(agent-supervisor#367). `worktree.sh gc` removes every *clean, merged*
worktree in bulk — a deliberate, separate operation, not something
`lane-done.sh` calls automatically, so a finished lane's branch survives
for review or follow-up until GC is run on purpose.

## 10. Restore after a tmux loss (`restore.sh`)

Per lane in the ledger: a lane with no open task restores fresh
(`free-<index>`, nothing to lose). A lane with an open task is resumed
only if all of the following hold — `harness_session_id` is recorded, the
harness has a `HARNESS_RESUME_CMD`, the transcript file it names still
exists on disk, and `harness_project_dir` is recorded and still exists.
Any one missing is reported `UNRECOVERABLE` (exit 2) and left alone —
never a fresh agent started under the old lane's name, which
`restore.sh`'s own header calls the worst available outcome: it wears the
right window name, looks fully recovered, and has none of the context.
Restore never kills a live pane and is idempotent — a second run against
an already-restored lane is a no-op.

## 11. Resolving a lane's own conversation id (`harness_session.py`)

Claude-only today (Codex has no resolver; Copilot and `pi` are not
implemented). Reads `~/.claude/projects/*/<session-id>.jsonl` on disk —
never the pane's process environment (`/clear` invalidates the launch-time
id) and never `lsof` (the file isn't open during a pane capture). A
candidate is accepted only if all three hold: its first timestamped entry
is within `BEGAN_SLACK_SECONDS` (5s) of the dispatch's own `since` time,
its transcript carries that dispatch's worktree-path marker, and its
`sessionId` field agrees with its own filename (a valid UUID). Zero
candidates, more than one candidate, or a candidate that fails any check
all refuse rather than guess — the same refuse-don't-invent instrument
`restore.sh` depends on downstream.
