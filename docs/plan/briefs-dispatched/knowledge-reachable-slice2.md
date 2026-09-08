# Brief — Recovery slice 2: make current knowledge reachable

**Owner (implementation):** worker lane in tmux window `agent-estate:3` (lane-c).
**Independent reviewer:** assigned by the Director when the PR lands — not you.
**Issued by:** Director (run manager), 2026-09-07.
**Base:** `origin/main` = `1b581de4b01a507bb860475d9eab6905c8743988`.
**Timebox:** 30–45 minutes. Deliver the verified part and name the remainder.

Your session is fresh and shares no memory with earlier work. This brief is
self-contained. Do not infer context from your window name.

Read `run/astra-recovery-20260907/EXECUTION-PLAN.md` §2 and `SPEC.md` first.

## Runs in parallel with slice 1 — respect the boundary

Lane-b (`agent-estate:2`) is concurrently repairing the projection producer
in `internal/vaultview` / `internal/candidates` / `internal/notemeta`.
**Do not touch those packages.** If your work needs a change there, stop and
tell me rather than editing across the boundary.

## What is wrong

Installed and session-level references still point at paths the INMAPS
relayout removed — `agent/index.md`, `agent/facts`, and the old lifecycle
paths. The `agent/` directory no longer exists. Anything still routing
through it silently reaches nothing, which looks identical to "there is
nothing there."

The current knowledge guide also still claims in places that the catalogue
has not landed. It has.

## The work

1. **Correct the stale references** to the current INMAPS routes: root
   `index.md` and the real note locations. Find them by grep across estate,
   dotfiles and the vault — list every occurrence you find and every one you
   changed, including any you deliberately left and why.
2. **Rewrite the knowledge-guide sections** that describe the catalogue as
   not-yet-landed. Historical descriptions move behind a historical link
   rather than being deleted — the record of what was once true stays
   reachable.
3. **Build and select a private session index** from current main. Reuse the
   existing `ESTATE_KNOWLEDGE_INDEX` override and the registered repo
   locators. **Verify retrieval from both an estate worktree and a dotfiles
   worktree** — a user must never need to know which index happens to be
   stale.
4. **Unavailable sources are reported explicitly.** Never silently fall back
   to an older answer that looks current. That silent fallback is the exact
   failure this slice exists to remove.

## Sequencing note — be honest about what you cannot yet run

Step 3's verification ideally exercises slice 1's repaired projections. If
slice 1 has not landed when you reach it, run what you can against current
main and **report the remainder as not-yet-runnable**, naming precisely what
is deferred. Do not simulate the result, and do not wait idle — steps 1 and 2
are fully independent and are the bulk of this slice.

## Hard constraints

- **Never regenerate the shared knowledge index** at
  `~/.local/state/agent-estate/knowledge/index.json`. Private index under
  `ESTATE_KNOWLEDGE_INDEX` only.
- **Do not reorganize docs.** #1285 (merged) owns the docs classification;
  a parallel reorganization would fight landed work.
- **Do not touch `docs/knowledge-workflow.md`** — a later slice owns it.
- **Do not touch `AGENTS.md` / `CLAUDE.md`** in either repo: they are one
  file via symlink, and two tests fail the build if the file names an estate
  subcommand or corpus path that does not exist. If your reference-fixing
  genuinely needs a change there, stop and tell me.
- **agent-estate#1286:** the declared standing-law member is
  `01 - Notes/01f - Facts/202609060005.md`, hash-pinned at `ecf40670309d`. If
  anything you do writes vault bytes, exclude that file or re-pin in the same
  PR. Changing it makes **every dispatch on the estate** exit 1.
- The frozen checkout at `~/source/repos/Personal/agent-dotfiles` is not
  yours to tidy: unpushed commit, uncommitted guard/lock edits, untracked
  files. Use a fresh clone or worktree under `/tmp`.

## Deliverable

One PR against `origin/main` from `1b581de`. Body carries: what it does, the
full list of references found/changed/left, real command output for the
two-worktree retrieval verification, what it deliberately did not do, what a
reviewer should attack first, and anything deferred pending slice 1.

**Push once, then HOLD.** Reply `DONE` with the PR number and SHA. The
Director merges; you never self-merge.
