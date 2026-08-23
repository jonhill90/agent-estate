---
type: Decision
description: Decision record: restore reports UNRECOVERABLE rather than starting a fresh agent under a lost lane's name.
generated:
  at: 2026-08-20T07:40:40-04:00
---

# 0004 — Restore refuses an unrecoverable lane; it never invents one

`agent-dotfiles#237`, carried forward into `restore.sh` in this repo,
`Verified 2026-08-20`.

## Decision

After a tmux server loss, `restore.sh` resumes a lane's agent only when
every one of its recorded facts checks out: a `harness_session_id`, a
harness with a resume dialect, a transcript that still exists on disk, and
a `harness_project_dir` that still exists (agent-supervisor#172 — see
below). If any is missing, the lane is reported `UNRECOVERABLE` and the
script exits `2` — not a crash code, a "some lane could not be brought
back" code. It never starts a fresh agent under the old lane's name in
place of a real resume.

## Why

`restore.sh`'s header names the incident directly (`agent-dotfiles#237`):
a tmux server died with nine lanes mid-task. Something — not this
script — restored the *sessions*: names, window counts, layout. Every pane
came back a dead `zsh` still carrying a window name from hours earlier.
`lanes.sh` reported nine lanes whose names described finished work; an
operator read those names as current and respawned the windows —
killing live agents that had, by then, already been resumed by hand. The
restore recovered the *label* and lost the thing the label was supposed to
describe.

That is the general failure this repo's "tmux is not a database" rule
(`AGENTS.md` invariant 1) names, applied specifically to *identity* rather
than *availability*: a window name is a projection of the ledger, written
never read, and a restore path that reads window names instead of the
ledger inherits exactly the same failure the "one idea that matters"
section of `README.md` describes for availability. `restore.sh` reads only
the ledger (`cli.py restore-plan`) and never a window's current name.

The header states the design constraint plainly: starting a fresh agent
in an unrecoverable lane's place is *the worst outcome available* — it
looks fully recovered, wears the right window name, and has none of the
context that made the lane worth resuming in the first place. A refusal
that says "I can't" is strictly better than a success that lies.

## A second, narrower refusal: the directory a session was launched in

agent-supervisor#172 sharpened the same rule once the ledger already had
`harness_session_id`: a session id alone is not enough to resume with,
because a harness's resume dialect is scoped to the directory its process
was actually *launched* in — not the lane's current working directory
(`repo`, a worktree that gets rewritten on every dispatch). The two
happened to coincide for almost every lane, which is exactly why resuming
from `repo` looked correct for a long time; they stop coinciding for any
lane whose process predates a project-directory migration. The fix is the
same shape as the first refusal, not a new mechanism: `harness_project_dir`
is recorded at the same moment as the session id, and a lane missing it is
refused exactly like a lane missing the id — never resumed against a
guessed directory.

## What this rules out

- Restoring from anything tmux produced as a byproduct of the server
  restarting (window name, layout, pane count).
- Treating a session id without a matching, still-live transcript file as
  good enough to resume from.
- Ever calling `respawn-window` or `kill-window` on a pane that might
  still be a live agent — `restore.sh` never kills anything; a live pane
  is skipped as already recovered, which is also what makes a second run
  of the script a no-op.

## Verified

`2026-08-20`: `restore.sh`'s header still cites `agent-dotfiles#237` and
`agent-supervisor#172`, still states "THE FAILURE DIRECTION IS REFUSE,
NEVER INVENT", and exit code `2` is still documented as "some lane could
not be brought back", not a crash.
