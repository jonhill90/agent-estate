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
| `ui-evidence.yml` | a PR touching the viewer needs a captured frame, not a description | **Not reimplemented, and this one is live policy.** `CLAUDE.md` still states it as enforced; it is not. `src/tui/internal/shell/frame_capture_test.go` makes the frame reproducible, so a Go gate has something to check against. |

One thing worth keeping from `completion-gate.yml` specifically: it ran a
**mutation of its own gate** in CI — it replaced the gate with `exit 0` and
failed the build if the test suite still passed, on the reasoning that a
suite which passes against a gate that always succeeds is decoration. That
idea should survive whatever replaces it.

## What still runs

- `estate-ci.yml` — `go build`, `go vet`, `go test` over `src/estate`.
- `tui-ci.yml` — the same over `src/tui`.

Both are real and both must stay green.
