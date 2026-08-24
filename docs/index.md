---
okf_version: "0.2"
---

<!-- Cap: 60 lines / 6 KB. One line per .md under docs/, nothing else. -->

# Measurement

* [OKF bundle replication](okf-bundle-replication-2026-08-23.md) - Replicates agent-tui#136's index-versus-reading measurement on a second repo -- a replication, not an adoption.
* [Merge impact inventory — agent-estate](merge-impact-inventory-agent-estate.md) - Pre-flight list of everything the agent-tui/agent-supervisor merge will break, gathered before the merge rather than one breakage at a time.

# Product

* [PRD](product/PRD.md) - What agent-supervisor is for and what it refuses to do -- a historical record of the boundaries it was designed against, not a live spec.
* [Spec](product/SPEC.md) - The eleven contracts agent-supervisor enforces in code -- ledger schema, lane states, dispatch, completion, merge gate, harness adapters, renderer, MCP surface, worktrees, restore, session ids.

# Decisions

* [0001 — SQLite ledger](decisions/0001-sqlite-ledger.md) - Decision record: the ledger is SQLite, not tmux state -- what is decided and remembered versus what is merely observed on screen.
* [0002 — claude-print alongside tmux](decisions/0002-claude-print-alongside-tmux.md) - Decision record: `claude-print` dispatch exists alongside the standing tmux lanes rather than replacing them.
* [0003 — Independent review required](decisions/0003-independent-review-required.md) - Decision record: independent review is enforced at merge by `merge-pr.sh`, not only at dispatch -- an author lane cannot merge its own PR.
* [0004 — Restore refuses, never invents](decisions/0004-restore-refuses-never-invents.md) - Decision record: restore reports UNRECOVERABLE rather than starting a fresh agent under a lost lane's name.
* [0005 — Codex session resume](decisions/0005-codex-session-resume.md) - Decision record: Codex lanes are resumable after a tmux server loss, the same way Claude lanes already were.
* [0006 — agent-tui merges into agent-supervisor, in one step with going public](decisions/0006-agent-tui-merges-into-agent-supervisor.md) - The repo-merge question Jon floated, decided: merge, direction matches the measured dependency, sequenced with (not before) the go-public flip.
* [0008 — Estate-lane PR authorship evidence](decisions/0008-estate-lane-pr-authorship-evidence.md) - Decision record: whether a lane may self-assert its own PR authorship -- its decision is SUPERSEDED by 0010 and kept for the record.
* [0010 — Three authorship attempts, three failures](decisions/0010-estate-authorship-three-failures.md) - Decision record: what changes after three ledger-authorship attempts failed differently, and what is documented as a limit rather than solved -- 0011 downgrades its primary decision (`#557`'s redesigned dispatch-time mechanism) from urgent to "unnecessary for the authorship problem specifically."
* [0011 — `dispatch.sh` is the standing rule](decisions/0011-dispatch-sh-standing-rule-and-backlog-path.md) - Decision record: dispatch.sh proven end to end, what it retires, and the established path for the already-open PRs.

# Diagrams

* [The dispatch path](diagrams/dispatch-path.md) - Annotated walk of the dispatch path end to end -- where each guard sits, and four places the happy path diverges.

# Runbooks

* [Restore after a tmux server loss](runbooks/restore-after-tmux-loss.md) - Step-by-step recovery after a tmux server loss, including how to tell a real loss from a lane that only looks wrong, and what UNRECOVERABLE means.
* [Retiring the send-keys lanes (#284)](runbooks/send-keys-retirement-284.md) - Measured enumeration of the 29 `send-keys` ledger rows retired under agent-supervisor#284, with method and before/after evidence.
* [The agent-estate migration](runbooks/agent-estate-migration.md) - Command sequence for merging agent-tui into agent-supervisor as agent-estate, a verification and rollback after every step -- written, NOT yet executed.

# Archive

* [agent-tui PR and issue metadata](archive/agent-tui/README.md) - Full PR and issue metadata captured before agent-tui's deletion, so review threads that do not survive a repo deletion stay resolvable by number.

# Not covered by this index

Eight tracked `.md` files live outside `docs/` and are deliberately out of
this index's scope (see the replication report for why): `AGENTS.md`
(`CLAUDE.md` is a symlink to it), `README.md`, `daemon/README.md`,
`scripts/supervisor/README.md`, `scripts/supervisor/loop-tick.md`,
`scripts/supervisor/laneview/README.md`,
`scripts/supervisor/laneview-plugin-tmux/README.md`.
