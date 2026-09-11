# agent-estate — agent orientation

*(`AGENTS.md` and `CLAUDE.md` are the same file — one is a symlink, so there is
no second copy to drift.)*

**This file is an index. Read only the part your task needs.** The detailed
orientation that used to live here was relocated verbatim to
`docs/orientation/` — find your task in the routing table below, open the one
file it names, and stop there. Only the rules on this page bind every task.

This repo is two halves merged under migration Step 2b/2c (#682, #744): the
daemon (`agent-supervisor`, Go, in `src/estate` — everything at the root
outside `src/tui/`) and the TUI (`agent-tui`, everything under `src/tui/`,
moved there from `tui/` by #865). Each keeps its own orientation file rather
than being blended into one narrative.

## Before you ask Jon anything — read this first

Jon has stated this more than twenty times. It is a **hard** parameter in his
corpus and it keeps being broken, so it goes at the top of the file rather than
somewhere polite.

**Exhaust the record before a question reaches him**, in this order:

1. **Query the corpus.** `~/corpus/corpus.sqlite3` — the same path
   `internal/corpus.Path()` resolves for every dispatch's own grounding, so
   the two cannot drift apart unnoticed
   (`src/estate/internal/corpus/agents_md_test.go` fails the build if they
   do). Measured read-only 2026-09-03: 5,403 prompts, 1,104 live hard
   constraints in `live_parameters` (re-run the count yourself before citing
   it further — it grows). Views: `live_parameters`, `open_questions`,
   `unacknowledged`, `possibility_count`.
   Renamed from `ledger.sqlite3` (agent-estate#P6): the old name is kept as
   a compat symlink to this file, but every consumer in this repo now
   names the file directly — see `99 - Meta`'s disambiguation note for
   which "ledger" is which.
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

## The hard rules that bind every task

- **The app is Go.** Shell and Python are not an implementation option for the
  app (`src/estate`, `src/tui`), at any size, for any reason. That is the
  whole rule; it does not extend to helper scripts elsewhere in the repo —
  Jon relaxed that part on 2026-09-11, saying scripts outside the app may now
  be written and used as needed (`hp-41cc3914d6d18816`, described rather than
  quoted — `text_clean` is NULL for that row; agent-estate#1412). A named
  17-file subset of
  `reference/scripts/supervisor/` — `core.py` and its ledger mixins, plus
  `itemize_prompts.py`, `mine_prompts.py`, `prompt_capture_hook.py` (a live
  registered hook) and two migration scripts — is live infrastructure the app
  and that hook call; do not delete it. Everything else under `reference/` is
  retired, and deleting it is authorised, not a risk. Full statement,
  including what is still retired and what changed:
  [docs/orientation/go-only.md](docs/orientation/go-only.md).
- **Credential store — read-only, no exceptions.** Never write, reset, or
  probe the macOS Keychain; a failed read is a report, not a repair
  (agent-estate#665).
- **Your branch is `dispatch/<id>`, created by the estate.** Never commit to
  `main`; nothing hand-authored merges. One independent review per PR; one
  fix pass. Details: [docs/orientation/conventions.md](docs/orientation/conventions.md).
- **Two guards actually refuse things**, both Go, both failing closed:
  `estate pressure` and `estate merge` (which *decides*, it does not merge —
  agent-estate#980). Everything else retired with the shell supervisor.
  Details: [docs/orientation/daemon.md](docs/orientation/daemon.md).

## Invariants — one line each; the full text with evidence is [docs/orientation/invariants.md](docs/orientation/invariants.md)

1. The ledger is the record; the live system is the screen.
2. Write the durable fact before the pretty label.
3. Restore refuses rather than invents.
4. Never address the default tmux socket in a test.
5. Address windows by `window_id` (`@7`), never by index.
6. `unknown` means "not offered", not "broken".
7. Harness-specific strings live in one place (`src/estate/internal/harness`).
8. A service is not a lane.
9. Identity is what the estate minted, never a name someone chose.
10. A dispatched turn does not derive its own identity — the estate states it.

Do not break these without an explicit decision. Also read, before reporting
"none"/"empty"/"never": the failure-mode section in the same file — an
instrument that cannot see a thing looks exactly like the thing being absent.

## Before you take a brief: read the plan

**[docs/plan/PLAN.md](docs/plan/PLAN.md)** says which plan governs, which are
its children, and which are superseded. Read it before acting on a brief a
lane or a director handed you.

This exists because on 2026-09-07 ten plan-shaped documents described this
work, two of them were real plans, and neither referenced the other — so
agents followed whichever brief was nearest and a weekend of carefully
executed work never connected to the recorded goal. **A brief is an
instruction to one worker at one moment. It does not override the plan.**

## Task routing — open only what your task touches

| Touching… | Read |
|---|---|
| What the plan is, what is done, what governs | [docs/plan/PLAN.md](docs/plan/PLAN.md) |
| The daemon (`src/estate`): dispatch, guards, what refuses what | [docs/orientation/daemon.md](docs/orientation/daemon.md) |
| Anything stateful, destructive, tmux-adjacent, or identity-bearing | [docs/orientation/invariants.md](docs/orientation/invariants.md) |
| Branching, reviews, verdicts, merging, PR evidence | [docs/orientation/conventions.md](docs/orientation/conventions.md) |
| The TUI (`src/tui`): panes, seams, layout, its merge path | [docs/orientation/tui-arrival.md](docs/orientation/tui-arrival.md) |
| Adding any script, or tempted by shell/Python | [docs/orientation/go-only.md](docs/orientation/go-only.md) |
| A rule that used to be a CI gate | [docs/ci-rules-retired.md](docs/historical/ci-rules-retired.md) |
| `estate knowledge`'s index: what it queries, how it ranks, `coverage`/`contradictions`, `--private` | [docs/knowledge-system.md](docs/canonical/knowledge-system.md) |
| Turning something worth knowing into a cited fact, or finding one already recorded: register/inspect/propose/review/publish/retrieve/refresh | [docs/knowledge-workflow.md](docs/canonical/knowledge-workflow.md) |
| Running as, or reasoning about, the Director's cron loop (`estate tick check/record/escalate`) | [docs/director-brief.md](docs/canonical/director-brief.md) and [docs/director-loop.md](docs/canonical/director-loop.md) |
| What phase the meta-harness roadmap is in, or what to build next | [docs/phase-plan.md](docs/canonical/phase-plan.md) |
| Whether the one-independent-review convention is worth its cost | [docs/reviewer-value.md](docs/canonical/reviewer-value.md) |
| What a dispatched turn actually costs, per harness, and how `estate spend` reads it | [docs/spend-observation.md](docs/canonical/spend-observation.md) |

## Three PRD/SPEC pairs — different scopes, not competing authorities

Read all three headers before assuming a conflict; none has been found. Each
governs a different scope:

| Pair | Governs |
|---|---|
| [`docs/product/PRD.md`](docs/product/PRD.md) / [`SPEC.md`](docs/product/SPEC.md) | The supervisor/daemon product — dispatch, guards, pressure. Routed to from `README.md`. Says nothing about the knowledge system (checked: zero mentions of knowledge/vault/corpus in either file). |
| [`docs/tui/PRD.md`](docs/tui/PRD.md) / [`SPEC.md`](docs/tui/SPEC.md) | The TUI product (`src/tui`). Routed to from `docs/orientation/tui-arrival.md`. |
| [`docs/plan/pushes/astra-recovery-20260907/PRD.md`](docs/plan/pushes/astra-recovery-20260907/PRD.md) / [`SPEC.md`](docs/plan/pushes/astra-recovery-20260907/SPEC.md) | Proposed knowledge-system requirements for ONE in-flight push (`docs/plan/PLAN.md`'s Pushes table, row "Recovery"). Its own opening line: "not a claim of delivery... does not replace the estate's orchestration/TUI requirements." Not a third product authority. |

Not re-listed as a fourth path elsewhere. `docs/tui-arrival.md`'s own citations
(`src/tui/testdata/vhs/README.md`, `full-nav-walk-report.md`) are likewise
already reachable from there.

Run `go run ./src/estate` with no arguments for the current subcommand list —
it grows, and a list written in prose goes stale between commits.

Two tests bind this file's content: `src/estate/agents_md_test.go` (every
`estate <subcommand>` named here must exist in `main.go`'s own switch) and
`src/estate/internal/corpus/agents_md_test.go` (the corpus path above must
match `internal/corpus.Path()`). They check names and paths only — they would
pass prose that described every command doing the wrong thing. Read a verb
against the code, never against a command's name.
