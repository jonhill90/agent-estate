---
type: Guard
description: What internal/vhscheck checks, why it exists (agent-tui#132/agent-tui#133), how it tells a live go build from a comment mentioning a path, and how to run it.
generated:
  at: 2026-08-23T16:06:08-04:00
---

# The tape-build guard (`internal/vhscheck`)

A `.tape` file (`testdata/vhs/*.tape`) drives `vhs` through a real
built binary to capture a screenshot — visual QA. That capture depends
on a `go build ... ./cmd/<name>` line inside the tape succeeding first.
If the referenced `cmd/<name>` directory doesn't exist, `go build`
fails, `vhs` produces no screenshot, and nothing reports an error
anyone reads. The tape looks exactly like a tape that ran and passed.
`internal/vhscheck` is the mechanical check that makes that failure
loud: it walks every `.tape` file, extracts each real `go build`
reference, and fails if the directory it names doesn't exist.

## Why this exists — three tapes, one never built at all

Found while auditing `docs/SPEC-shell.md`'s own build-status claims
(agent-tui#130): `testdata/vhs/agents-mode.tape` had referenced
`go build -o /tmp/agentsdemo ./cmd/agentsdemo` since the tape was added
in agent-tui#91, but `cmd/agentsdemo` had never been committed — the tape was
unrunnable from day one, silently, until agent-tui#130 restored the binary.

Checking the scope of that finding (agent-tui#132) found two more:
`testdata/vhs/knowledge.tape` and `testdata/vhs/knowledge-route.tape`
both referenced `cmd/knowledgedemo`. `git log --all --
'cmd/knowledgedemo*'` returns **zero commits across every ref** — this
one wasn't a regression from a deletion, the directory had *never*
existed at all, since `knowledge.tape` first landed in agent-tui#87. Three tapes,
found only because a human swept every `.tape` file by hand after the
first one turned up — exactly the kind of check that shouldn't depend
on a human remembering to look.

agent-tui#133 fixed both knowledge tapes (`knowledge-route.tape`
deleted — its coverage was already superseded by
`testdata/vhs/full-nav-walk-report.md`'s own nav-walk sweep;
`knowledge.tape` repointed at `cmd/keelson`, the shell binary that
already reaches that route) and, as the more durable half of that PR,
added this package so a fourth broken tape fails CI the day it's
introduced instead of waiting for the next by-hand sweep.

## What it checks, precisely — and what it deliberately does not

`ScanTapes` walks every `.tape` file under `testdata/vhs/` (including
subdirectories) and extracts every line containing a real
`go build ... ./cmd/<name>` reference. `MissingCmdDirs` then checks
that `cmd/<name>` actually exists as a directory on disk. That's the
whole check: **does the referenced directory exist.**

It deliberately does **not** check that the tape still runs correctly
end to end — that's what actually running `vhs` by hand proves, a
larger and flakier surface (`vhs`'s own capture pipeline has a known
flake, documented inline in `agents-mode.tape`'s own header). A missing
`cmd/` directory is a mechanical fact `go build` would hit every single
time; that's the class of failure worth gating CI on. Whether the
resulting screenshot looks *right* is a human's job, not this guard's.

## How it tells a live build line from a comment merely mentioning a path

This is the precision `knowledge-route.tape` itself forced: its header
comment described `knowledge.tape`'s own build target in prose —
"against `cmd/knowledgedemo`" — with no `go build` on that line, and no
real dependency on that path being buildable. A naive scan for the bare
substring `cmd/knowledgedemo` anywhere in the file would have flagged
that prose forever, including after the sentence it was explaining had
long since stopped being true.

The fix: `cmdRefPattern` only matches a `cmd/<name>` reference on the
**same line** as the literal text `go build`. Two real shapes in this
repo both still get caught correctly:

- A live `Type "go build -o /tmp/x ./cmd/estate && clear" Enter` line —
  the ordinary case, most tapes.
- A `#`-prefixed comment documenting a prerequisite command the
  operator runs by hand before `vhs` starts (`chat-send.tape`'s own
  `# go build -o /tmp/x ./cmd/fakemcp` pattern) — still a genuine
  dependency the tape cannot run without, so it's still caught if the
  target ever goes missing, even though it's never executed as a live
  `Type` line.

What does **not** get caught, by design: a comment that merely
*mentions* a `cmd/` path in prose, with no `go build` on that line —
the exact `knowledge-route.tape` shape that motivated this precision in
the first place.

## How to run it

```
go test ./internal/vhscheck/...
```

Already gated on every PR — `.github/workflows/ci.yml`'s `go test ./...`
step runs it along with every other package's tests, so a broken
reference fails CI the same day it's introduced, not on the next by-hand
sweep.

Five tests, each exercising one distinct claim above:

- `TestMissingCmdDirsFlagsATapePointingAtAMissingBinary` /
  `TestMissingCmdDirsPassesATapePointingAtARealBinary` — the mandatory
  pair, mutation-checked both directions on throwaway fixture repos.
- `TestACommentMentioningAPathWithNoGoBuildOnTheSameLineIsNotAReference` —
  the `knowledge-route.tape` false-positive shape, reproduced as a
  fixture, confirmed not flagged.
- `TestACommentedGoBuildLineIsStillAReference` — the `chat-send.tape`
  documented-prerequisite shape, confirmed still caught.
- `TestNoTapeReferencesAMissingCmdDirectory` — the real guard, run
  against this actual repository's own `testdata/vhs/`, not a fixture.
  This is the one CI actually depends on; the other four prove the
  scanning logic behind it is correct.

Run for real for this doc, not assumed:

```
$ go test ./internal/vhscheck/... -v
--- PASS: TestMissingCmdDirsFlagsATapePointingAtAMissingBinary (0.00s)
--- PASS: TestMissingCmdDirsPassesATapePointingAtARealBinary (0.00s)
--- PASS: TestACommentMentioningAPathWithNoGoBuildOnTheSameLineIsNotAReference (0.00s)
--- PASS: TestACommentedGoBuildLineIsStillAReference (0.00s)
--- PASS: TestNoTapeReferencesAMissingCmdDirectory (0.00s)
PASS
ok  	github.com/jonhill90/agent-tui/internal/vhscheck	0.272s
```

## What this does not do

Does not run `vhs` itself, does not check a tape's captured output for
correctness, does not check anything about a tape that doesn't build a
`cmd/` binary at all. A tape that builds and runs but captures a wrong
or stale frame — the flake documented in `agents-mode.tape`'s own
header — is outside this guard's scope entirely; only a human
re-running `vhs` by hand catches that.
