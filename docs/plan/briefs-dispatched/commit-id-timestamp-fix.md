# Task — land an already-prepared worktree as a PR

Your session is fresh. This brief is self-contained.

## What exists

A git worktree at **`/tmp/estate-fix`** on branch
**`fix/vaultview-drop-duplicate-index`**, based on `origin/main`. The code
changes are already made and already staged.

The Director cannot run this: the `main-branch-guard` hook inspects the
session's own working directory, which sits on `main`, so it refuses
regardless of which worktree the command targets. The guard is correct and
must not be bypassed. Your shell can work inside `/tmp/estate-fix`, which is
the sanctioned path.

## Verify first

```
cd /tmp/estate-fix
git rev-parse --abbrev-ref HEAD     # fix/vaultview-drop-duplicate-index
git status --short                  # 7 staged files
go build ./src/estate/... && go vet ./src/estate/... && go test ./src/estate/...
```

The full suite passed for the Director. Confirm it yourself. **If anything
fails, stop and report — do not fix it.**

## Land it

The commit message is written at **`/tmp/cmsg.txt`** — use it verbatim:

```
git commit -F /tmp/cmsg.txt
git push -u origin fix/vaultview-drop-duplicate-index
```

Open a PR against `jonhill90/agent-estate` titled:

`fix(estate): note ids are real timestamps, and stop writing a duplicate index`

For the body, reuse the commit message's own explanation of the three
defects, then add the sections below, then end with the trailer line.

**Live vault.** Renamed to match the new scheme: 2,867 notes, references
rewritten across 140 files, validator reports `Contract holds: no hard
violations`, checksummed backup taken first. That moved the declared
standing-law member from `202609060005.md` to `20260906071524.md` and changed
its bytes, so `HashPrefix` is re-pinned `ecf40670309d` to `26d4a45ea2a7`.
Without it `corpus.StandingLaw` refuses and **every dispatch exits 1** —
agent-estate#1286, whose stated remedy is exactly this: re-pin in the same
change. Verified: `estate dispatch` runs.

**Evidence.** `go build`, `go vet`, `go test ./src/estate/...` all pass.

**Attack first:** whether the widened note-id pattern
(`^\d{12}(\d{2})?\.md$`) lets through anything that is not a note.

End the PR body with exactly this line:

    🤖 Generated with [Claude Code](https://claude.com/claude-code)

Reply `DONE` with the PR number and head SHA. **Do not merge.**
