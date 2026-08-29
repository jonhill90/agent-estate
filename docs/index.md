---
okf_version: "0.2"
---

# supervisor/

The dispatcher, ledger, and lane-supervision system.

## Decisions

* [0001 — The ledger is SQLite, not tmux state](decisions/0001-sqlite-ledger.md) - Why availability/ownership state lives in `ledger.sqlite3`, not derived from tmux.
* [0002 — `claude-print` dispatch exists alongside tmux lanes](decisions/0002-claude-print-alongside-tmux.md) - Print-mode dispatch supplements tmux lanes, does not replace them.
* [0003 — Independent review is enforced at merge, not just at dispatch](decisions/0003-independent-review-required.md) - `merge-pr.sh` refuses an author reviewing its own PR; convention alone was not enough.
* [0004 — Restore refuses an unrecoverable lane; it never invents one](decisions/0004-restore-refuses-never-invents.md) - After a tmux server loss, an unrecoverable lane is reported `UNRECOVERABLE`, never silently replaced.
* [0005 — Codex lanes are resumable, the same as Claude's](decisions/0005-codex-session-resume.md) - `harness_session.py` resolves a codex lane's own session id from its on-disk rollout files.
* [0006 — agent-tui merges into agent-supervisor, in one step with going public](decisions/0006-agent-tui-merges-into-agent-supervisor.md) - The repo-merge question Jon floated, decided: merge, direction matches the measured dependency, sequenced with (not before) the go-public flip.
* [0008 — An estate-lane's PR authorship: self-run cross-check now, an independent poller as the real fix, on an explicit trigger](decisions/0008-estate-lane-pr-authorship-evidence.md) - Should a lane self-assert its own PR authorship, or must it only ever be recorded by whatever dispatched the work -- decided, with the losing argument answered.
* [0010 — Estate-lane PR authorship, after three failures: redesign, bound, and say plainly what is not proven](decisions/0010-estate-authorship-three-failures.md) - Three attempts at ledger-proven PR authorship have each failed differently. Decided -- what changes, what's bounded, what's documented as a limit rather than solved.
* [0011 — `dispatch.sh` proven; the standing rule, and the backlog's real path, established not assumed](decisions/0011-dispatch-sh-standing-rule-and-backlog-path.md) - dispatch.sh works end to end -- the standing rule for all future dispatch, what retires, and the established (not assumed) legitimate path for the fifteen already-open PRs.
* [0012 — Invariant evidence](decisions/0012-invariant-evidence.md) - Extended evidence for AGENTS.md's Invariants 4, 9, and 10 -- the incidents that produced each rule, moved out of the index.
* [0013 — The tui/ merge gate](decisions/0013-tui-merge-gate.md) - Why `cmd/mergepr` exists and the blank-`Review-Lane:` self-approval bypass it had to close, behind AGENTS.md's "Merging PRs you did not author" section.

## Product

* [PRD](product/PRD.md) - Historical record of the boundaries this project chose and why, not a live spec.
* [SPEC](product/SPEC.md) - Historical record gathering the contracts that already exist across the codebase.

## Runbooks

* [Restore after a tmux server loss](runbooks/restore-after-tmux-loss.md) - Verified steps for bringing every lane back after the tmux server dies.
* [Retiring the `send-keys` lanes (agent-supervisor#284)](runbooks/send-keys-retirement-284.md) - Enumeration of the 29 `send-keys` rows measured for #284's retirement.
* [agent-tui + agent-supervisor → agent-estate migration](runbooks/agent-estate-migration.md) - Executable command sequence for merging agent-tui into agent-supervisor as agent-estate, with a verification and a rollback after every step.
* [A merged fix can look identical to an unfixed defect](runbooks/stale-checkout-diagnosis.md) - `agent-supervisor#308`'s stale-checkout misdiagnosis, behind AGENTS.md's failure-modes section.

## Diagrams

* [The dispatch path](diagrams/dispatch-path.md) - First of three diagrams tracing how an issue becomes a dispatched lane.

## Archive

* [agent-tui PR/issue archive](archive/agent-tui/README.md) - Full PR and issue metadata dump from agent-tui, captured before the repo merge/deletion so review threads (which do not survive a repo deletion) stay resolvable by number.

## Other

* [Merge impact inventory — agent-tui + agent-supervisor → agent-estate](merge-impact-inventory-agent-estate.md) - Pre-flight list of everything the agent-tui/agent-supervisor repo merge will break, with a file/line locator per entry -- has no `description` frontmatter key of its own to copy from.

## Measurements

* [OKF index measurement (2026-08-24)](okf-index-measurement-2026-08-24.md) - Does `docs/index.md` hold up as a complete, accurate map of `docs/`? Redoes agent-supervisor#531 through the dispatch path.

# tui/

The terminal UI client, merged in under `tui/` by migration Step 2b/2c (#682, #744).

## Measurement

* [OKF bundle pilot](tui/okf-bundle-pilot-2026-08-23.md) - Measures whether an OKF-style docs/ bundle is worth doing across four repos -- a pilot on agent-tui, not an adoption.

## Design

* [PRD](tui/PRD.md) - What agent-tui is for, in Jon's own framing -- not the technical design.
* [Spec — technical design](tui/SPEC.md) - The technical design of what is actually built in agent-tui today.
* [Spec — shell build order](tui/SPEC-shell.md) - The TUI shell build order, 1:1 with the hill90 web nav -- one independently shippable item per PR.
* [Spec — AgentBox execution mode](tui/SPEC-agentbox-execution-mode.md) - Design brief for the AgentBox container execution driver, verified against the sibling AgentBox repo.

## Research

* [Per-agent memory storage evidence](tui/RESEARCH-per-agent-memory-storage.md) - Evidence for the per-agent memory storage-format decision (agent-tui#116) -- does not choose a format; that stays reserved to Jon.
* [Agent memory / knowledge graph / OKF+RAG survey](tui/research/agent-memory-knowledge-graph-survey.md) - Prior-art survey on agent memory, knowledge graphs, and the OKF+RAG router pattern (agent-tui#116) -- a survey, not an adoption recommendation.

## UI variants and spikes

* [Memory variants (knowledge graph view)](tui/memoryvariants/README.md) - Knowledge-graph view of agent memory -- variants and a spike, gated BACKLOG behind agent-tui#61's own do-not-start condition.
* [UI variants (rail + board)](tui/uivariants/README.md) - Six real rendered rail+board UI variants over hardcoded fake state, for Jon to pick from (agent-tui#62).
* [Lanechat variant comparison](tui/lanechat-variant-comparison.md) - Three real, working screens for the combined Lanes+Chat surface, side by side for Jon to pick from (agent-tui#122).

## Test evidence

* [Nav-walk docs captures](tui/navwalk-docs/README.md) - Nav-walk frame captures proving the Docs section's two destinations stopped being stubs (2026-08-22).

## CI / Guards

* [Tape-build guard](tui/vhscheck-guard.md) - What `internal/vhscheck` checks, why it exists (agent-tui#132/agent-tui#133), how it tells a live `go build` from a comment mentioning a path, and how to run it.

## Reference

* [agent-tui#49 — closed, three defects, all fixed](tui/known-defects-49.md) - Full detail behind AGENTS.md's "Known defects" clean bill of health.
