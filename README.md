# agent-supervisor

**A long-lived agent reads GitHub issues, hands each one to a disposable agent in
its own terminal and its own git worktree, and records who is doing what in
SQLite — because the terminal is a screen, not a record.**

Harness-agnostic: lanes run Claude Code, Codex or Copilot behind per-harness
adapters. Extracted from
[`agent-dotfiles`](https://github.com/jonhill90/agent-dotfiles) (#179) once it
became clear this is an application, not configuration.

---

## Quick start

```bash
git clone https://github.com/jonhill90/agent-supervisor.git
cd agent-supervisor

bash scripts/supervisor/digest.sh          # everything at a glance
bash scripts/supervisor/lanes.sh           # just the lanes
python3 -m unittest discover -s tests/supervisor   # 349 tests, as of 2026-08-13 --
                                                    # not re-run to completion in this sweep
                                                    # (it did not finish inside this
                                                    # environment's time budget); 92 tracked
                                                    # files under tests/supervisor/ today
                                                    # (`Verified 2026-08-16T01:32Z`, git
                                                    # ls-files), so the count is likely
                                                    # stale and should be re-measured, not
                                                    # assumed
```

Nothing needs configuring to run the suite. Every setting has a working default;
copy `.env.example` to `.env` (gitignored) only when you want to change one.

```bash
bash scripts/supervisor/bootstrap-session.sh --session work --lanes 4 --agent claude
bash scripts/supervisor/dispatch.sh 123 fix-thing brief.md owner/repo
bash scripts/supervisor/restore.sh         # rebuild every lane after a tmux loss
```

## The three roles

| Role | Lives in | What it decides |
|---|---|---|
| **Director** | its own tmux session | What matters next, and whether a PR is good |
| **Supervisor** | window 1 | Mechanical upkeep: is everything alive, is the ledger consistent |
| **Workers** (lanes) | windows 2..N | One issue each. Write code, open a PR, then get discarded |

Judgement is centralised and expensive; labour is parallel and cheap. Workers are
cattle.

## The one idea that matters

There are two places state can live, and confusing them causes nearly every
serious failure this system has had.

**tmux is a screen.** It answers *what is this pane doing right now* — painting
text, showing a menu, sitting idle. Read those from the pane.

**The ledger is the record.** `ledger.sqlite3` holds anything the system
*decided* and must *remember*: which lane is available, who owns a task, which
conversation belongs to which lane. Six tables — `lanes`, `tasks`,
`source_tasks`, `pr_verdicts`, `events`, `components`.

The test is authorship: **did this system write the value, or did tmux produce it
as a byproduct?**

Availability once lived in the tmux *window name* — a display label doing a
database's job. It failed exactly as you'd expect: a lane that finished but was
never renamed became invisible forever, and the repair was to edit the database
with `tmux rename-window`. Today the window name is a **projection** of the
ledger. A stale label is now cosmetic instead of corrupting.

## Order of operations, and why

`lane-done.sh` records completion **before** renaming the window. Take a crash
between the two:

- **Release then rename** (today): ledger says free, window shows a stale name.
  The lane is offered again. Harmless — the name is not consulted.
- **Rename then release** (the old order): window says `free-3`, ledger still
  holds the task open. The lane is **never offered again**, and renaming it by
  hand no longer helps, because the name is not what is consulted.

One order fails cosmetically. The other fails silently and permanently. Prefer
the order whose crash leaves a wrong *label* rather than a wrong *record*.

## Recovery

A tmux server death does not lose work. `dispatch.sh` records each lane's
**harness session id** — the agent's own conversation, which lives on disk
outside tmux — and `restore.sh` rebuilds every lane from the ledger.

**It refuses rather than inventing.** A lane whose session id is missing, whose
harness has no resume dialect, or whose transcript is gone is reported
`UNRECOVERABLE` and left alone; the exit code is `2`. Starting a fresh agent in
its place is the worst outcome available — it wears the right window name, looks
fully recovered, and has none of the context.

Proven by killing a real tmux server with two live lanes: both returned and
recalled a phrase they were told before the kill.

## Harness adapters

`scripts/supervisor/harness/{claude,codex,copilot}.sh`. Each supplies its launch
command, its idle shape, its busy shape, its menu shape, and how far back to read
(`HARNESS_BUSY_TAIL` — one line for Claude and Copilot, wider for Codex whose
busy marker sits above a static footer).

`lanes.sh` contains no harness-specific string. Adding a harness is a new file;
removing one is a deletion.

## Also here

- **MCP server** (`mcp_server.py`) — exposes lanes, digest and ledger over MCP so
  any harness can consume the supervisor, not just the one running it. Also
  exposes four guarded session-management writes (attach/detach/add/remove;
  agent-tui#14) — `dispatch`/`merge` remain excluded, see `supervisor_view.py`'s
  `WRITE_SOURCES` docstring for why these four are different.
- **Two lane viewers** (`laneview/text.sh`, `laneview/opensessions.sh`) — neither
  required by the other, or by `lanes.sh`.
- **Watchdog** (`watchdog.sh`) — runs outside the loop from a LaunchAgent, so it
  survives the loop dying. It escalates rather than restart-looping forever.
- **The merge gate** (`merge-pr.sh`) — the only path that should merge a PR in
  this repo. It chains `ci_gate.py` (every check green at the PR's live head
  SHA — GitHub branch protection is unavailable on these private repos
  without GitHub Pro, so nothing else enforces this) and
  `verdict-independence.sh` (the reviewing lane really is not the author —
  see `AGENTS.md` invariant 9 on lane identity). `Verified 2026-08-15`
  (#184/#196/#198): both gates fail closed — an unreadable verdict or
  unresolved authorship refuses the merge, never proceeds.
- **The verified-send primitive** (`send.sh`) — `verified_type`/
  `verified_submit` type text into a pane's input box and confirm it actually
  landed before submitting, rather than trusting `send-keys`'s own exit code.
  `dispatch.sh`, `inbox-route.sh` and `director-route.sh` route the brief/
  message text they send an agent through it (`Verified 2026-08-15`, #186).
  It does not replace every `tmux send-keys` call in this repo — launching a
  harness process and short control sequences (`Enter`, `C-u`) still call
  `tmux` directly, deliberately; see `AGENTS.md`'s note on an abstraction
  correctly avoided outside its actual risk.

## Conventions

Read [`AGENTS.md`](AGENTS.md) before changing anything — it is short, and it is
where the non-obvious rules live. `CLAUDE.md` is a symlink to it.

## Status

Extracted 2026-08-13 with full history. The suite passes standalone. Interfaces
are still moving; treat the scripts as the contract, not any given internal.
