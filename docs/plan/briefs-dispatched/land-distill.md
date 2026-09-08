# Task — land a prepared worktree as a PR

Your session is fresh. This brief is self-contained.

## What exists

A git worktree at **`/tmp/estate-distill`** on branch **`feat/distill`**,
based on `origin/main`. Three new files are already staged:

- `src/estate/internal/distill/distill.go`
- `src/estate/internal/distill/distill_test.go`
- `src/estate/cmd/distillprobe/main.go`

The Director cannot run this: the `main-branch-guard` hook inspects the
session's own working directory, which sits on `main`, so it refuses
regardless of which worktree the command targets. The guard is correct and
must not be bypassed. Your shell can work inside the worktree, which is the
sanctioned path.

## Verify first

```
cd /tmp/estate-distill
git rev-parse --abbrev-ref HEAD     # feat/distill
git status --short                  # 3 staged files
go build ./src/estate/... && go vet ./src/estate/... && go test ./src/estate/...
```

Also run the probe once and confirm it reports roughly 25 groups over 2,872
items — it reads the corpus read-only and writes nothing:

```
go run ./src/estate/cmd/distillprobe -show 3
```

**If anything fails, stop and report — do not fix it.**

## Land it

The message is written at **`/tmp/cmsg-distill.txt`**. Use it verbatim with
`git commit -F /tmp/cmsg-distill.txt`, then
`git push -u origin feat/distill`.

Open a PR against `jonhill90/agent-estate` titled:

`feat(estate/distill): group restated rules into one fact, without inventing wording`

For the body, reuse the message's own explanation, and add:

**What a reviewer should attack first:** whether `canonical()` can ever return
a body that is not one of the group's members — that is the property that
stops this putting words in Jon's mouth, and there is a test for it. Second:
whether the `corpus`/`vault` stop words are the right call, given they are
the subject of most items.

**Known limit, stated in the PR, not hidden:** recall is 3%. A lexical pass
catches rules restated in similar words and misses rules restated in
different words. That gap needs the model step and is not claimed here.

End the PR body with exactly this line:

    🤖 Generated with [Claude Code](https://claude.com/claude-code)

Reply `DONE` with the PR number and head SHA. **Do not merge.**
