# 0001 — The ledger is SQLite, not tmux state

`2026-08-10` (the ledger's move into this codebase), reasoning current as
of `Verified 2026-08-20`.

## Decision

Anything the system *decides and must remember* — which lane is free, who
owns a task, which conversation belongs to a lane — lives in a single
SQLite file, `ledger.sqlite3`, guarded by `fcntl.flock` and a `BEGIN
IMMEDIATE` transaction per write. Nothing that qualifies as a durable fact
is inferred from tmux state (a window name, a pane's live content) at read
time.

## Why

The ledger core (`core.py`, `cli.py`, `adapter.py`) was not designed from
scratch in this repository — it was moved in whole from Hill90
(`bb1412d`, "Move portable supervisor core from Hill90 into
agent-dotfiles", part of #21) specifically because it was already the
durable, harness-agnostic piece worth keeping separate from Hill90's
launchd-specific adapter. The design predates agent-supervisor; this repo
inherited it because the alternative — tmux window names and pane content
as the record — had already failed once, elsewhere in this same estate.

`README.md`'s "one idea that matters" names the failure mode directly:
availability once lived in the tmux *window name*, and it failed exactly
as expected — a lane that finished but was never renamed became invisible
forever, and the repair was to hand-edit a database by way of `tmux
rename-window`. A window name is a display label; a SQLite row is a
record with a schema, a primary key, and a transaction boundary. Using the
label as the record made every crash between "finish the work" and
"update the label" ambiguous. SQLite does not remove that race, but it
gives the system a single place to make an explicit ordering choice about
it — see `lane-done.sh`'s release-then-rename order, and
`docs/decisions/0004-restore-refuses.md`.

SQLite specifically (not a server-backed database) because there is no
other process this system needs at rest: the supervisor, workers, and
watchdog all open the same file directly, and a `.lock` file plus WAL
journaling is enough concurrency control for a handful of local processes
reading and writing one machine's own state. Nothing here is shared
across machines or needs network access to function.

## What this rules out

- Deriving "is this lane free" from a pane's current shape. `lanes.sh`
  reads shape to answer *is this pane busy right now* (a screen question);
  it never uses shape to answer *is this lane available for dispatch* (a
  record question) — see `docs/product/SPEC.md` §2.
- A renderer or viewer writing to the ledger. `laneview/README.md`'s
  "read, never write" rule exists because a renderer that could write
  would be a second place decisions get made, defeating the point of
  having one record.

## Verified

`2026-08-20`: `core.py`'s `_initialize` still creates `ledger.sqlite3`
with the tables described in `docs/product/SPEC.md` §1; the file's own
provenance comment (`bb1412d`) still names Hill90 as the source.
