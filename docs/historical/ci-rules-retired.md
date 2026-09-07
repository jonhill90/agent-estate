# CI rules retired with the shell supervisor

`Written 2026-09-02.`

Four workflows were deleted because **every script and test they invoked no
longer exists**. They were CI for the shell and Python supervisor that was
moved to `reference/`. They did not enforce anything: they failed on every
pull request regardless of its contents, which is not a gate — it is noise
that trains a reader to ignore red. Between them they were the reason no PR
could merge.

This file exists so the *rules* are not lost with the *mechanisms*. Per
`CLAUDE.md`, recovering a rule means reimplementing it in Go, not restoring
the script.

## What was verified before deleting

Every path referenced by these four workflows, checked against the tree:

```
GONE  scripts/ci/plan_shell_shards.py
GONE  scripts/supervisor/completion-gate.sh
GONE  scripts/supervisor/fixpass_evidence_gate.py
GONE  scripts/supervisor/fixpass-comment-relevant.sh
GONE  scripts/supervisor/fixpass-evidence-gate.sh
GONE  scripts/supervisor/ui-evidence-gate.sh
GONE  scripts/supervisor/ui-evidence-report.sh
GONE  tests/supervisor/test_completion_gate.sh
GONE  tests/supervisor/test_fixpass_evidence_gate.py
GONE  tests/supervisor/test_ui_evidence_gate.sh
```

`git ls-files tests | wc -l` and `git ls-files scripts/supervisor | wc -l`
both returned **0**.

## The rules themselves, and their status

| retired workflow | the rule it encoded | status |
|---|---|---|
| `validate.yml` | the Python/shell supervisor suite passes, sharded | **Obsolete.** The suite it ran does not exist. `estate-ci.yml` and `tui-ci.yml` cover the Go tree. |
| `completion-gate.yml` | a task group does not advance until every member left evidence | **Not reimplemented.** Worth reviving in Go if task groups return. |
| `fixpass-evidence.yml` | a fix pass must paste proof, not claim a fix | **Not reimplemented.** The underlying discipline is live in how briefs are written, but nothing enforces it. |
| `ui-evidence.yml` | a PR touching the viewer needs a captured frame, not a description | **Not reimplemented.** It remains a convention that nothing enforces. `AGENTS.md` said it was enforced; that was corrected in the same pull request that retired the workflow, so the two agree as of this file's date. |

One thing worth keeping from `completion-gate.yml` specifically: it ran a
**mutation of its own gate** in CI — it replaced the gate with `exit 0` and
failed the build if the test suite still passed, on the reasoning that a
suite which passes against a gate that always succeeds is decoration. That
idea should survive whatever replaces it.

## Rules retired with their scripts, not with a workflow

`Added 2026-09-03.` The four below were never wired to one of the deleted
workflows — they were guards their own callers invoked — so the table above
never covered them. `AGENTS.md` names all four in its "Nothing else refuses
anything" sentence but does not say what any of them *was*; this section is
where that is written down. Same status for every one: **not reimplemented in
Go, nothing enforces it today.** Source is in `reference/scripts/supervisor/`.

| retired mechanism | the rule it encoded |
|---|---|
| `collision-check.sh` | Refuse a dispatch whose files overlap an in-flight lane's files (`agent-supervisor#291`). Overlap is **whole-file, nothing finer** — deliberately, because line-range overlap requires predicting where an unwritten change will land. "In flight" is every ledger task not recorded complete/failed/cancelled that has a recorded worktree path. Measured failure it prevented: `as#263`/`as#266`, two lanes independently writing the same fix to the same file. |
| the supervisor lease (`core_ledger_claims.py`, `take_supervisor_lease`) | Only one supervisor loop runs at a time. Taken atomically as a singleton row — the second writer's INSERT raises and is **refused, never merged** — and always owned by a `host:pid` token, because an unowned lease cannot be told apart from a dead one and so could never be safely reaped (`agent-dotfiles#238`). |
| `gh-comment-gate.sh` | One path posts a `Verdict:`/`Review-Lane:` comment. A static check over committed `*.sh` sources refusing a direct `gh pr comment`/`gh issue comment` in code outside `post-verdict.sh`, with pre-existing exceptions grandfathered **by exact line, not by file**, so editing that line has to re-earn the exemption (`agent-supervisor#188`). Hardening one posting path means nothing if callers can reach around it. |
| `mark-pr-external.sh` | Marking a PR "authored outside the lane system" must be a positive, evidenced claim, never an operator's assertion. It refused unless a full contributor-resolution chain **ran to completion and found nobody** — refusing equally when the PR could not be read at all, since "I could not check" is not "I checked and found nothing" (`agent-supervisor#308`, `#321`). Without the gate, a lane could launder its own PR as unauthored and then review its own work. |

The estate's live merge gate (`src/estate/internal/gate`) now covers part of
what the last two protected — it establishes authorship structurally from the
head ref and a recorded head SHA rather than from anything a lane asserts. It
does not cover the first two at all.

## What still runs

- `estate-ci.yml` — `go build`, `go vet`, `go test` over `src/estate`.
- `tui-ci.yml` — the same over `src/tui`.

Both are real and both must stay green.
