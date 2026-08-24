---
type: Archive
description: Full PR and issue metadata dump from agent-tui, captured before the repo merge/deletion so review threads (which do not survive a repo deletion) stay resolvable by number.
generated:
  at: 2026-08-23T17:41:00-04:00
---

# agent-tui PR/issue archive

Captured 2026-08-23, ahead of the `agent-tui` → `agent-estate` merge and the
planned deletion of the standalone `agent-tui` repo
(`director-EXECUTE-repo-merge-CORRECTION.md`). This is preflight work, done
before quiesce — see `director-preflight-nonblocking.md` job 1.

**Why this exists.** A GitHub repository deletion takes its pull-request
review-comment threads with it. Commits survive via the history merge into
`agent-estate`; open issues survive via `gh issue transfer`; **PR review
discussions do not transfer or merge — they are simply gone.** Several docs
across this estate cite `agent-tui` PRs and issues by bare number (`#79`,
`#101`, `#136`, `#139`, and others) — after deletion those citations become
unresolvable. This archive is the mitigation: a static record a future reader
(or agent) can grep by number to recover what a citation meant, even though
the live thread is gone.

## What's here

- `agent-tui-pr-archive.jsonl.gz` — one JSON object per line, every PR in
  `agent-tui` (92 total, numbers 1–142 minus the issue numbers interleaved
  in that same sequence). Fields: `number, title, body, state, mergedAt,
  mergeCommit, headRefName, baseRefName, author, createdAt, closedAt,
  comments, reviews`. `comments` is issue-style PR comments; `reviews` is
  each formal review's state and body (not individual inline diff comments —
  `gh pr view --json` does not expose those separately from the review body
  each is attached to).
- `agent-tui-issue-archive.jsonl.gz` — one JSON object per line, every issue
  (50 total). Fields: `number, title, body, state, createdAt, closedAt,
  author, comments, stateReason`. Issues were archived too, not just PRs,
  because two of the four numbers this job was asked to verify (`#101`,
  `#139`) turned out to be issues, not PRs — a PR-only dump would not have
  resolved them.

Both captured via `gh pr view`/`gh issue view --json ...`, one call per
item, 30s timeout each, zero failures on either pass (92/92 PRs, 50/50
issues). Exact commands, reproducible verbatim:

```
$ gh pr list --repo jonhill90/agent-tui --state all --limit 500 --json number -q '.[].number' | sort -n > numbers.txt
$ while read -r n; do
    timeout 30 gh pr view "$n" --repo jonhill90/agent-tui --json \
      number,title,body,state,mergedAt,mergeCommit,headRefName,baseRefName,author,createdAt,closedAt,comments,reviews \
      >> agent-tui-pr-archive.jsonl
  done < numbers.txt

$ gh issue list --repo jonhill90/agent-tui --state all --limit 500 --json number -q '.[].number' | sort -n > issue-numbers.txt
$ while read -r n; do
    timeout 30 gh issue view "$n" --repo jonhill90/agent-tui --json \
      number,title,body,state,createdAt,closedAt,author,comments,stateReason \
      >> agent-tui-issue-archive.jsonl
  done < issue-numbers.txt
```

## Verified, not assumed

The four numbers this job named as cited elsewhere all resolve:

| # | Kind | Title | State |
|---|---|---|---|
| 79 | PR | S12 — execution mode interface | MERGED |
| 101 | issue | Decision needed: what may agent-tui show for Storage and Secrets | CLOSED |
| 136 | PR | OKF bundle pilot for docs/ | MERGED |
| 139 | issue | Cost pane stampedes ccusage | OPEN (has a fix PR, #140, in flight as of this capture) |

```
$ gzip -dc agent-tui-pr-archive.jsonl.gz | jq -c 'select(.number==79) | {number,title,state}'
$ gzip -dc agent-tui-issue-archive.jsonl.gz | jq -c 'select(.number==101) | {number,title,state}'
```

(`gzip -dc`, not `zcat` — BSD/macOS `zcat` expects a `.Z`-suffixed file and
errors on a plain `.gz`; `gzip -dc` decompresses to stdout on both GNU and
BSD gzip and is the portable form.)

## What this does not capture

- Inline diff review comments as distinct records (see above — they live
  inside each review's own body in the `reviews` field, not separately).
- Anything that lands on `agent-tui` after this capture (2026-08-23
  ~17:41 ET) — notably `#140` (the ccusage fix, in review as of this
  capture) and `#141`/`#142` (captured, but were freshly opened/merged
  same day and may still change). Re-run the capture command immediately
  before the actual deletion step, not from this snapshot alone, if time
  has passed.
