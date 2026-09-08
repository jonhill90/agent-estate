# Review brief — agent-dotfiles PR #348

**Assigned to:** the worker lane in tmux window `agent-estate:1` (lane-a).
**Issued by:** Director (run manager). **Date:** 2026-09-07.

This brief is self-contained. Your session is fresh and shares no memory
with earlier work today. Do not infer context from your window name.

## The PR

- **Repo:** `jonhill90/agent-dotfiles` — NOT agent-estate. Pass
  `--repo jonhill90/agent-dotfiles` on every `gh` command.
- **PR:** #348, `ci: ship docs-lint gate for the docs/ classification standard (P12)`
- **Head to review:** `f912993c91f6a56e80f989cf435ba25c0897c5ba`
- **State at assignment** (verified by me): OPEN, MERGEABLE, the single
  `repository` check passing, zero verdicts posted.
- **Files, all three:** `.github/workflows/validate.yml`,
  `scripts/docs_lint.py`, `tests/test_docs_lint.py`

## Authorship — you are a valid independent reviewer

#348 was authored by the lane in window `agent-estate:3` (lane-c), in a
pre-restart session. You are `agent-estate:1`. A fresh session does not erase
prior authorship, and window names do not carry identity across a restart —
this is recorded in a comment on the PR itself. **If you find evidence you
wrote this, stop and tell me.**

## Checkout rule — hard

Do **not** work in `~/source/repos/Personal/agent-dotfiles`. That checkout is
frozen: unpushed commit `a3f6e09`, uncommitted modifications to
`hooks/ledger-write-guard.sh` (the guard protecting Jon's corpus) and
`apm.lock.yaml`, plus untracked files and a nested worktree carrying two
unpushed commits. Do not commit, stash, clean, restore, checkout, push or
tidy any of it.

Make a fresh clone or worktree under `/tmp` from the PR ref, and work only
there.

## What this PR is, in full

P12 is a documentation-standard programme across three repos. Its rule, in
Jon's words: *"Classification is judgment; enforcement is a gate. Third
cleanup = last manual cleanup."*

The classification sort for agent-dotfiles was **already done** by a
separately merged PR, **#345** — flat root docs are now 5-line tombstones,
real content lives under `docs/canonical/` and `docs/historical/`, and
`docs/index.md` is the taxonomy signpost. So #348 deliberately sorts nothing.
It ships only the missing enforcement gate.

Read `run/p12-dotfiles-345-verification.md` in the agent-estate-lanes
worktree first — lane-c's companion finding on whether #345 actually
satisfied the standard.

## Attack in this order

### 1. The red/green problem — the central issue here

Because #345 already sorted the tree, running the lint on it is **green on
the first try and proves nothing**. A lint whose first run is on an
already-clean tree is indistinguishable from a lint that cannot fail.

So RED must come from **mutation**. For each of the three rules
independently — no unclassified root files; no state files in `docs/`; zero
full-text duplicates by checksum — introduce a real violation into your
scratch clone, run the lint, confirm it **fails naming the specific offending
file**, then revert and confirm clean.

**Run all three mutations yourself.** Do not accept the PR body's pasted
output. If any rule cannot be made to fail, it is decoration — that is
BLOCKING.

### 2. Honesty of the PR body

It must state plainly that the tree was already sorted by #345 and that red
comes from mutation. It must not imply this pass did the sorting.

### 3. The allowlist

Rule 1 needs exemptions for genuine entry points such as `docs/index.md`.
Check every allowlisted path is named with a stated reason, and that the
allowlist is not so broad it swallows the rule. An exemption nobody justified
is how a gate quietly stops gating.

### 4. CI wiring

Read `.github/workflows/validate.yml`. Confirm the lint actually **runs on
PRs** and that a failure **fails the job**. A lint wired as non-blocking,
`continue-on-error`, or sitting in a job nothing requires is decoration.
Verify the exit code propagates.

### 5. Tests

Run `tests/test_docs_lint.py` yourself. Report pass/fail counts.

### 6. Scope

Three files only. No docs moved, nothing deleted, `AGENTS.md`/`CLAUDE.md`
untouched (they are one file via symlink in that repo), and
`docs/knowledge-workflow.md` untouched.

## How to post the verdict

Post a **plain PR comment** (not `gh pr review`) on
`jonhill90/agent-dotfiles#348`. Verdict at **line start, outside any code
fence**, exactly these three lines plus your findings:

```
Verdict: APPROVE            <- or: Verdict: REQUEST CHANGES
Review-Lane: agent-estate:1
Reviewed-SHA: f912993c91f6a56e80f989cf435ba25c0897c5ba
```

Use the exact token `APPROVE` — not PASS, not LGTM. The merge gate is checked
mechanically and any other token blocks the merge.

**Do not merge. I merge.** Reply `DONE` when the comment is posted.
