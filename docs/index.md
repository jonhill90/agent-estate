---
okf_version: "0.2"
---

# Decisions

* [0001 — The ledger is SQLite, not tmux state](decisions/0001-sqlite-ledger.md) - Why availability/ownership state lives in `ledger.sqlite3`, not derived from tmux.
* [0002 — `claude-print` dispatch exists alongside tmux lanes](decisions/0002-claude-print-alongside-tmux.md) - Print-mode dispatch supplements tmux lanes, does not replace them.
* [0003 — Independent review is enforced at merge, not just at dispatch](decisions/0003-independent-review-required.md) - `merge-pr.sh` refuses an author reviewing its own PR; convention alone was not enough.
* [0004 — Restore refuses an unrecoverable lane; it never invents one](decisions/0004-restore-refuses-never-invents.md) - After a tmux server loss, an unrecoverable lane is reported `UNRECOVERABLE`, never silently replaced.
* [0005 — Codex lanes are resumable, the same as Claude's](decisions/0005-codex-session-resume.md) - `harness_session.py` resolves a codex lane's own session id from its on-disk rollout files.
* [0006 — agent-tui merges into agent-supervisor, in one step with going public](decisions/0006-agent-tui-merges-into-agent-supervisor.md) - The repo-merge question Jon floated, decided: merge, direction matches the measured dependency, sequenced with (not before) the go-public flip.

# Product

* [PRD](product/PRD.md) - Historical record of the boundaries this project chose and why, not a live spec.
* [SPEC](product/SPEC.md) - Historical record gathering the contracts that already exist across the codebase.

# Runbooks

* [Restore after a tmux server loss](runbooks/restore-after-tmux-loss.md) - Verified steps for bringing every lane back after the tmux server dies.
* [Retiring the `send-keys` lanes (agent-supervisor#284)](runbooks/send-keys-retirement-284.md) - Enumeration of the 29 `send-keys` rows measured for #284's retirement.

# Diagrams

* [The dispatch path](diagrams/dispatch-path.md) - First of three diagrams tracing how an issue becomes a dispatched lane.
