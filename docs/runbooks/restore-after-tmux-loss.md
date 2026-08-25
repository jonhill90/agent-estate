---
type: Runbook
description: Step-by-step recovery after a tmux server loss -- how to tell a real loss from a lane that only looks wrong, and what UNRECOVERABLE means -- with each step marked verified or unverified per how it was actually proven.
generated:
  at: 2026-08-24T21:18:37-04:00
---

# Runbook: restore after a tmux server loss

`Verified 2026-08-15` unless a step says otherwise. Verified means: the
command below was actually run — either against `tests/supervisor/test_restore.sh`
(the repository's own hermetic demonstration, private socket, stub harness),
or by hand against a throwaway isolated tmux server built for this runbook.
Neither touches the live `agent-supervisor` session, `hill90`, or any real
lane. If a step could only be proven by killing a real server, it is marked
**unverified** and says why, per this project's "do not document what has not
been run" rule (`AGENTS.md`, agent-supervisor#98).

## When to reach for this

The tmux server that hosts the estate died, was restarted, or was replaced
(machine reboot, `tmux kill-server` run outside this system, a crashed
process manager). Not every wrong-looking lane means this — see the next
section before running anything.

## Step 1 — tell a real loss from a lane that only looks wrong

Run `lanes.sh` (or `digest.sh`, which wraps it) first. It classifies every
pane into one of several states (`AGENTS.md`/`CLAUDE.md`, repo-layout
section). [Corrected 2026-08-23: this used to say the header comment (lines
7–20) only enumerated ten and omitted `scrolled`, with agent-supervisor#131
tracking the fix. That fix has since landed — the current header
(`scripts/supervisor/lanes.sh` lines 7–29) explicitly lists `scrolled` and
even documents its own prior omission ("#65, #131: this state was already
emitted below but missing from this list"); the assignment itself is now at
`lanes.sh:487`, not line 328. The header today names twelve states (free,
busy, hung, blocked, unsent, dead, stale, scrolled, broken, service, unknown,
never-busy); the running code also emits `menu-blocked`/`text-blocked` as a
finer split of `blocked` and an internal `supervisor` marker for the
supervisor's own window, none of which the header enumerates separately —
so "eleven" was already imprecise before #131 and a single fixed number is
harder to keep current than the header itself; read `lanes.sh`'s header
directly rather than trusting a count here.] Two of these are the ones this
runbook cares about, and they mean different things:

- **`dead`** — no agent process in the pane, and the window name carries no
  task claim (`free-N`, or something restore.sh itself would ever have
  written). This is an ordinary "start an agent here" case — dispatch
  normally handles it, no restore needed.
- **`stale`** — no agent process in the pane, **and the window name still
  claims a task**. `lanes.sh`'s own comment names this exactly: "no agent,
  and the window name still claims a task -> restore.sh, and do not believe
  the name (#237)." The window name is not evidence of what the lane was
  doing — that answer only lives in the ledger (`AGENTS.md`, item 1) — the
  name is only evidence that the *pane* is empty while something still
  *claims* it.

A tmux server that died and came back (or was manually recreated) tends to
produce many `stale` lanes at once — every window that was mid-task loses its
process but keeps whatever name it last had. One or two `stale` lanes is
routine churn; most or all of them going `stale` together is the server-loss
case this runbook is for.

**Do not** read `dead` or `stale` from the window name alone, and do not
respawn a `stale` window by hand — that is the exact operator action that
caused agent-dotfiles#237 (`restore.sh` header, and `README.md`'s "Recovery"
section): nine live, already-resumed agents were killed because their window
names described finished work and an operator trusted the names.

## Step 2 — what `restore.sh` does, and what it refuses

`scripts/supervisor/restore.sh` reads `cli.py restore-plan` — a ledger query,
never a window name (`core.py`'s `restore_plan`, docstring: "this is the
whole read side of the restore path, and it is a LEDGER query on purpose").
Per lane it gets: harness, harness session id, the directory that session id
was resolved in (`harness_project_dir`, agent-supervisor#172 — distinct from
`repo` below; see the note under Step 4), repo path (the lane's WORKING
directory, a worktree), and the open task (if any) that owns it. Then, per
lane:

- **No open task** → the lane is started fresh, under the convention
  `free-N`. This is not "inventing" a conversation — the ledger itself says
  there is nothing to resume.
- **Open task, but no `harness_session_id` recorded, no resume dialect for
  that harness, no transcript file on disk for that session id, or no
  `harness_project_dir` recorded** → the lane is reported `UNRECOVERABLE` and
  left completely alone. Nothing is started in its place.
- **Open task, session id present, transcript present on disk,
  `harness_project_dir` present** → the harness's resume command is typed
  into the pane, from that recorded directory — not `repo`
  (`H_RESUME_CMD` in `harness-registry.sh`, e.g. `claude --resume <id>`),
  and the window is renamed to the task name **after** the resume command is
  queued — the rename is a projection, written last, read by nobody in this
  script.

It never kills anything. A pane already running something other than a bare
shell is reported `LIVE` and is never touched — this is also what makes a
second run of `restore.sh` a no-op for anything already recovered.

**Verified** — ran the repository's own hermetic suite:

```
$ bash tests/supervisor/test_restore.sh
...
37 passed, 0 failed
```

That suite (private tmux socket `ad237-test-$$`, private `$HOME`, a stub
harness whose `claude` records its argv and execs `sleep`) demonstrates, on a
real killed tmux server: two live lanes come back RESTORED with the exact
harness session ids the ledger held; a corrupted or missing session id comes
back UNRECOVERABLE and starts nothing; a second run of `restore.sh` reports
an already-restored lane `LIVE` and does not touch it.

**Verified** — also reproduced by hand, independently of that suite, against
a disposable isolated server (`env -u TMUX TMUX_TMPDIR=<throwaway dir> tmux -f
/dev/null ...`, never the default socket):

```
$ bash restore.sh --dry-run --session rverify
rverify:2                WOULD-RESTORE  ad901-first <- claude --dangerously-skip-permissions --resume 33333333-3333-4333-8333-333333333333

restore: 1 restored, 0 already live, 0 unrecoverable

$ bash restore.sh --session rverify        # after killing the isolated server
rverify:2                RESTORED       ad901-first <- claude --dangerously-skip-permissions --resume 33333333-3333-4333-8333-333333333333

restore: 1 restored, 0 already live, 0 unrecoverable

$ cat launched.log                         # what the stub harness actually received
--dangerously-skip-permissions --resume 33333333-3333-4333-8333-333333333333
```

— the session id in the resume command matches the `harness_session_id`
recorded by `cli.py record-dispatch` exactly; nothing was invented.

A second lane in the same run, whose `harness_session_id` had no matching
`~/.claude/projects/*/<id>.jsonl` on disk:

```
rverify:3                UNRECOVERABLE  no transcript on disk for session 44444444-4444-4444-8444-444444444444 -- task 'ad902-second' cannot be resumed

restore: 1 restored, 0 already live, 1 unrecoverable
restore: an unrecoverable lane is NOT restarted -- a fresh agent wearing its name would look recovered and have none of its context (agent-dotfiles#237)
$ echo $?
2
```

Running `restore.sh` again with no state change in between reports the
already-restored lane `LIVE` (not re-restored) and leaves the unrecoverable
lane exactly as it was:

```
rverify:2                LIVE           sleep is running -- left alone
rverify:3                UNRECOVERABLE  no transcript on disk for session 44444444-4444-4444-8444-444444444444 -- task 'ad902-second' cannot be resumed

restore: 0 restored, 1 already live, 1 unrecoverable
```

**Verified 2026-08-15** (agent-supervisor#172) — the manual reproduction
above (`Verified 2026-08-14`) predates `harness_project_dir` and does not
exercise it: that demo's lane happened to have `repo` and the originating
directory coincide, which is exactly the common case that let this defect
ship unnoticed. Re-verified instead against a **copy** of the live estate's
own ledger (`cp ~/.local/state/agent-dotfiles-supervisor/ledger.sqlite3
<scratch>/`; the live file itself is never written by this script or by
this check) — opening the copy runs the real migration, and `restore.sh
--dry-run` against it, read-only, reports:

```
agent-supervisor:4       UNRECOVERABLE  no originating project directory recorded for task 'as169-fix-as169' -- refusing to guess between '/.../ad-169-fix-as169-37192' and elsewhere (pre-agent-supervisor#172 lane)
agent-supervisor:5       UNRECOVERABLE  no originating project directory recorded for task 'as170-rerev176' -- refusing to guess between '/.../ad-170-rerev176-54616' and elsewhere (pre-agent-supervisor#172 lane)
agent-supervisor:6       UNRECOVERABLE  no originating project directory recorded for task 'as172-restore-project-dir' -- refusing to guess between '/.../ad-172-restore-project-dir-86320' and elsewhere (pre-agent-supervisor#172 lane)
```

Three real, currently-live lanes — dispatched before this field existed,
including the very lane this fix was written in — refuse cleanly, name the
working directory they refuse to guess with, and read distinctly from a "no
session id" refusal. Nothing was resumed, nothing was guessed, and the live
ledger was never touched (`AGENTS.md` item 4's spirit applied to a database
instead of a tmux socket). The hermetic suite (`test_restore.sh`, `PROJECT
DIR`/`NO PROJECT DIR` sections) covers the positive case — a lane whose two
directories genuinely differ resumes from the *originating* one, not `repo` —
with a real, stub-harness process whose actual launch `$PWD` is captured and
checked, something a read-only ledger copy cannot demonstrate.

**Unverified**: the behavior of a *real* Claude Code `--resume` against a
real, non-stub transcript — proving the resumed process itself recalls prior
turns — is not re-demonstrated here. It is demonstrated in `README.md`'s
"Recovery" section ("Proven by killing a real tmux server with two live
lanes: both returned and recalled a phrase they were told before the kill"),
which this runbook did not independently re-run; the stub-harness proof above
only shows that `restore.sh` hands the *correct* session id to the *correct*
lane, which is what a script external to the harness can actually check.

## Step 3 — running it for real

```bash
bash scripts/supervisor/restore.sh                  # default session
bash scripts/supervisor/restore.sh --session work    # a named session
bash scripts/supervisor/restore.sh --dry-run         # print the plan, touch nothing
```

Always run `--dry-run` first against a real estate — it prints exactly what
each lane would do (`WOULD-RESTORE <name> <- <command>`) without typing
anything into any pane. Read the plan before running it for real; an
unexpected `UNRECOVERABLE` line is worth investigating before you commit to
the run, not after.

Exit codes (`restore.sh` header, confirmed against the runs above): `0`
everything the ledger knew is live or was restored; `1` usage error or the
ledger plan could not be read at all; `2` at least one lane came back
`UNRECOVERABLE` — this is a report, not a crash, and is the expected exit
code any time even one lane cannot be brought back.

## Step 4 — what `UNRECOVERABLE` means and what to do about it

It means `restore.sh` positively could not identify what that lane was
doing, for one of four reasons the printed message names directly:

1. **No `harness_session_id` recorded** — the ledger never resolved one for
   this task (see `harness_session.py`). There may be no on-disk transcript
   to recover at all.
2. **No resume dialect for the harness** — the harness adapter in
   `harness/*.sh` (via `harness-registry.sh`) has no `H_RESUME_CMD` entry.
   This harness cannot be resumed by this script, full stop, regardless of
   what's on disk.
3. **No transcript file on disk for the recorded session id** — the id was
   real once, but the file it should point to
   (`~/.claude/projects/*/<session_id>.jsonl` for Claude) is gone, truncated,
   or the id itself is corrupted. This is the exact agent-dotfiles#237
   mutation case.
4. **No `harness_project_dir` recorded, or the recorded one no longer exists
   on disk** (agent-supervisor#172) — a session id was resolved, but the
   directory it was resolved IN was not (every lane dispatched before this
   field existed), or that directory has since been removed. `claude
   --resume` is scoped to that directory, not to `repo` (a worktree,
   rewritten on every dispatch) — the two coincide for most lanes, which is
   why resuming from `repo` looked correct for a long time and is wrong for
   any lane whose harness process predates a project-directory migration
   (the Phase 1.5 split, `agent-dotfiles` → `agent-supervisor`, is the real
   one this estate hit). The refusal message names `repo` explicitly, so it
   reads as "a session exists, but I refuse to guess where," never as "no
   conversation exists" (reason 1) — those are different facts and the
   message says which one applies.

**Do not** hand-start a fresh agent into that window under the old name — the
whole point of refusing is that a fresh agent wearing the old name is
indistinguishable from a real recovery from the outside (`restore.sh`
header; `AGENTS.md` item 3). Options, in order of preference:

- If the work is recoverable some other way (a PR already opened, a branch
  already pushed), close the task in the ledger and start a fresh lane under
  a **new** task, so nothing pretends to be the old conversation.
- If it's genuinely lost, say so — file what's missing as a new issue rather
  than silently re-launching, per this project's "one fix pass" convention
  (`AGENTS.md`, Conventions) extended to recovery: don't paper over a loss
  with a lane that looks like nothing happened.

## Step 5 — verify a restored lane actually carries its own conversation

A `RESTORED` line alone is `restore.sh`'s own claim; verifying that the
resumed process actually is the ledger's session, not a script's opinion of
it, takes one more look:

- **Cross-check the resume argument against the ledger.** The command typed
  into the pane embeds the harness session id
  (`H_RESUME_CMD` filled from `harness_session_id`, e.g.
  `claude --resume <id>`). Compare that id against
  `python3 scripts/supervisor/cli.py restore-plan` (or `lane-status`) for the
  same lane — they must be the same string. This is exactly what the manual
  reproduction above did (`launched.log` vs. the ledger's
  `harness_session_id`) and is externally checkable without trusting the
  agent's own account of itself.
- **Ask the pane something only the prior conversation would know** — a
  phrase, a file path, a decision made earlier in the task — the same check
  `README.md`'s "Recovery" section describes as the real-server proof. This
  is the only check that verifies the *harness*, not just `restore.sh`, did
  its job; it requires interacting with a live agent and cannot be scripted
  here, so treat it as a manual spot-check on anything you actually intend
  to keep working, not something to skip because the ledger id matched.

## Known limits (state them, don't paper over them)

- `restore.sh` can only place a window at the `index` the ledger recorded
  for `session:index`; if that session no longer exists at all, it creates
  it fresh, but any *other* windows that session used to hold are not
  reconstructed — only lanes the ledger has rows for come back.
- Killing a live real tmux server to re-verify this end to end is explicitly
  out of scope for this runbook (`AGENTS.md`, item 4: never address the
  default tmux socket in a test or by hand) — every verification above ran
  on an isolated, throwaway socket, never the machine's real session.
