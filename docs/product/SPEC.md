---
type: Spec
description: The contracts the Go estate enforces in code today. Verified against the tree 2026-09-08, correcting nine days of drift from the 2026-08-30 rewrite.
verified: 2026-09-08
---

# agent-estate — SPEC

What is **actually built** today. Intent lives in `PRD.md`. Every path below
exists in the tree as of the verification date; if a path here is missing, this
file is wrong and the tree wins.

## Layout

```
src/estate      the supervisor: pressure gate, ledger, dispatch, merge gate,
                worktree sweep, knowledge index, corpus projection
src/notify      sends a message to the operator's Telegram
src/issuemine   distils closed issues into the rules worth carrying forward
src/progress    reports how much of the estate is app vs. tooling/scaffolding
src/tui         the terminal UI (see docs/tui/)
scripts/        tooling, not app: docs-lint (a CI gate), evidence, knowledge
reference/      the deleted shell and Python supervisor, read-only
```

There is no `src/langguard`. It does not exist in the tree and no CI job
enforces the app-is-Go rule today (see "Not yet built") — the name survives
only as a stale path string inside `src/progress`'s own scaffolding count,
unrelated to any enforcement.

## The ledger — `src/estate/internal/ledger`

Append-only JSON lines at `$ESTATE_LEDGER`, defaulting to
`~/.local/state/estate/ledger.jsonl`.

- A task is one `Record` appended per state change. **The current state of a
  task is its last record**; history is never rewritten.
- Append-only exists so authorship cannot be destroyed. The old supervisor
  discarded a task row on cancel, lost who wrote a PR, and approved a lane to
  review its own work.
- States: `dispatched`, `complete`, `failed`, `unknown`.
- **`Terminal()` is true only for `complete` and `failed`.** `unknown` is
  deliberately non-terminal — a turn nobody observed may still be running, and
  freeing its slot is how a cap fails open.
- `Current()` returns the latest record per id. **A malformed line is an error,
  never a short list** — a truncated read would report less work in flight,
  which fails open. A missing ledger file reads as empty, not an error,
  provided its own directory exists; a missing directory refuses instead of
  reporting zero tasks from a path that cannot be right.

## The pressure gate — `src/estate/internal/pressure`

Six independent limits, all of which must pass:

| limit | default | source |
|---|---|---|
| load per core | < 3.0 | `sysctl -n vm.loadavg` / `runtime.NumCPU()` |
| free memory | >= 512 MB | `vm_stat` (free + inactive + speculative pages) |
| active paging | < 1 swapout / 2s sample | `vm_stat` Swapouts, delta over the sample |
| worktree count | <= 40 | `git worktree list --porcelain` |
| weekly token budget | not exhausted, reading fresh | `codexbar usage --provider claude --json` |
| lanes in flight | < 6 | ledger, non-terminal records |

**Every one fails closed.** A measurement that errors sets `OK=false` with the
failure named. Free memory stopped reading `sysctl -n vm.swapusage` on
2026-09-02: on a swap-disabled host that read 0.00 MB forever and refused
100% of dispatches on a healthy machine while reporting it as host pressure —
fail-closed in the wrong direction is still a defect. Paging and worktree
count were added the same week: a memory reading that counts reclaimable
pages as free cannot see a host actively fighting over them, and 176
uncleaned dispatch worktrees were the visible symptom of a cleanup loop that
had stopped running, not a memory problem at all.

## Dispatch — `src/estate` (`estate dispatch <issue> <brief-file>`)

1. Read the brief. Refuse if unreadable.
2. Check pressure. **Refuse before creating anything** if the host is loaded —
   the old dispatcher created a branch and worktree first and leaked both on
   every refusal.
3. Append `dispatched`.
4. Run the selected harness (`--harness=`, default `claude`) as a subprocess
   with the brief on stdin — for the default harness, `claude -p
   --output-format json`, under a 45-minute context timeout.
5. Append the outcome:
   - context deadline hit → `unknown` ("unknown is not failed"), slot stays held
   - non-zero exit → `failed`
   - exit 0 with parseable JSON → `complete`, result recorded
   - exit 0 with unparseable output → `unknown`, not a clean completion

Exit status is 0 only for `complete`.

## Merge gate — `src/estate` (`estate merge <repo> <pr> <reviewer-lane>`)

`estate merge` **decides**; it never runs `gh pr merge` or any other mutating
call. It evaluates one PR against its head SHA: checks green at that exact
head, and the named reviewer lane's own ledger record carries a completed,
independent review whose `Reviewed-SHA` trailer matches the PR's current
head — a stale review (an earlier head, since superseded) does not count, and
neither does a reviewer whose own author-record HeadSHA matches the PR
(self-review). Prints its reasons either way; the operator or the Director
merges.

## Worktree cleanup — `src/estate` (`estate sweep-worktrees [--apply]`)

Report mode by default; only `--apply` removes anything. Every ledger record
is classified into one of seven categories (never a worktree, outside this
checkout's own dispatch root, already gone, kept by policy, bound-reached,
refused, removed) and counted separately — a merged "N left in place" number
used to add unrelated situations together and read as clean when it was not.
Refuses anything outside this checkout's own dispatch root before looking at
disk, and offers only terminal-state or positively-reclaimed corpses to the
same `Worktree.Remove` refusals dispatch itself would apply — never more
removals than `Remove` would allow on its own, only fewer.

## Knowledge — `src/estate/internal/{corpus,candidates,knowledge}`, `estate knowledge`

Three layers: the corpus (`~/corpus/corpus.sqlite3`) is the immutable record
of what was said; the memory vault (`$AGENT_MEMORY_VAULT`) holds the rules
distilled from it, each one verbatim from a corpus item, never composed;
`estate knowledge` compiles a regenerable index over both for retrieval.
`estate candidates memory` is the one sanctioned write path into the vault.
See [`docs/canonical/knowledge.md`](../canonical/knowledge.md) for the full
design and [`docs/canonical/knowledge-workflow.md`](../canonical/knowledge-workflow.md)
for the register/propose/review/publish/retrieve lifecycle — not restated
here.

## Tests

`go test ./...` under `src/estate`. The tests assert the failure directions
specifically: unknown is not terminal, in-flight counts unknown and dispatched,
a corrupt ledger errors rather than truncating, an unreadable ledger refuses
dispatch, and each pressure limit refuses at its own threshold while allowing
below it.

## Not yet built

Named so nobody reads this as complete: no lane view in `src/estate` itself
(the TUI's own lane surfaces are documented separately, `docs/tui/`), no
mechanical CI enforcement of the Go-only rule (the deleted `src/langguard`
was never rebuilt), and no relation proposer for the corpus — the `links`
table records nothing as superseding or contradicting anything yet, so that
judgement stays entirely human. The rules a future `src/langguard` needs are
recoverable from `reference/` and from the closed issues `src/issuemine`
identifies as carrying durable ones.
