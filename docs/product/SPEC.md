---
type: Spec
description: The contracts the Go estate enforces in code today. Rewritten 2026-08-30 against the tree after the shell and Python supervisor was deleted.
verified: 2026-08-30
---

# agent-estate — SPEC

What is **actually built** today. Intent lives in `PRD.md`. Every path below
exists in the tree as of the verification date; if a path here is missing, this
file is wrong and the tree wins.

## Layout

```
src/estate      the supervisor: pressure gate, ledger, dispatch
src/langguard   enforces Go as the implementation language (CI)
src/notify      sends a message to the operator's Telegram
src/issuemine   distils closed issues into the rules worth carrying forward
src/tui         the terminal UI (see docs/tui/)
reference/      the deleted shell and Python supervisor, read-only
```

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
  which fails open. A missing ledger is empty and not an error.

## The pressure gate — `src/estate/internal/pressure`

Three independent limits, all of which must pass:

| limit | default | source |
|---|---|---|
| load per core | < 3.0 | `sysctl -n vm.loadavg` / `runtime.NumCPU()` |
| free memory | >= 512 MB | `sysctl -n vm.swapusage` |
| lanes in flight | < 6 | ledger, non-terminal records |

**Every one fails closed.** A measurement that errors sets `OK=false` with the
failure named. This exists because the previous estate had three pressure checks
that did not consult each other and a session cap the dispatchers in use never
called; the host needed a hard restart twice.

## Dispatch — `src/estate` (`estate dispatch <issue> <brief-file>`)

1. Read the brief. Refuse if unreadable.
2. Check pressure. **Refuse before creating anything** if the host is loaded —
   the old dispatcher created a branch and worktree first and leaked both on
   every refusal.
3. Append `dispatched`.
4. Run `claude -p --output-format json` as a subprocess with the brief on stdin,
   under a 45-minute context timeout.
5. Append the outcome:
   - context deadline hit → `unknown` ("unknown is not failed"), slot stays held
   - non-zero exit → `failed`
   - exit 0 with parseable JSON → `complete`, result recorded
   - exit 0 with unparseable output → `unknown`, not a clean completion

Exit status is 0 only for `complete`.

## Language guard — `src/langguard`

Fails the build on any tracked `.sh` or `.py` outside `reference/`, `.github/`
and `.claude/`. Exits **2** when it cannot list files, so an unmeasurable tree
never reads as clean. Runs in `.github/workflows/langguard.yml` on every push
and pull request.

## Tests

`go test ./...` under `src/estate`. The tests assert the failure directions
specifically: unknown is not terminal, in-flight counts unknown and dispatched,
a corrupt ledger errors rather than truncating, an unreadable ledger refuses
dispatch, and the cap refuses at the limit while allowing below it.

## Not yet built

Named so nobody reads this as complete: no merge gate, no reviewer-vs-author
independence check, no worktree lifecycle, no lane view. The rules those need
are recoverable from `reference/` and from the 196 closed issues `src/issuemine`
identifies as carrying durable rules.
