# Lane B — source library and retrieval

Read `run/execution-plan.md` first; it holds ownership, sequence,
review/merge protocol, and constraints. You are Lane B. Your worktree:
`~/source/repos/Personal/agent-estate-lanes/lane-b`, branch
`knowledge/lane-b`. You do NOT write the vault, ever, this run — generated
views go to `run/views-staging/`.

## Mission

Turn the computed catalogue (`src/estate/internal/catalogue/` — today a
health report over two seeded conversation sources and two seed PDFs) into a
persistent registration service, without breaking its existing invocation.

## Deliverables

1. **Persistent private register** under
   `~/.local/state/agent-estate/catalogue/` (create; never in the vault,
   never Git-tracked). Entries: stable identity, source kind + locator,
   authority, scope, access policy, owner, observed revision/hash,
   freshness, why-indexed, links to reviewed derivatives. Field names align
   with `run/contract.md` once Lane A lands it (~30 min); scaffold before,
   freeze after.
2. **`register` / `list` / `show` / `refresh`** on `cmd/sourcecatalogue`
   (its `main.go` is yours; `src/estate/main.go` is NOT — expose everything
   as exported package functions so Lane C can wire `estate` subcommands).
   Existing no-arg health invocation must keep working unchanged.
3. **Source kinds proven this run**: PDF (`pdftotext` is installed),
   conversation (the existing seeded sources), and one of repo/docs or
   skill/tool. Other kinds may register with extraction marked unavailable —
   visible honesty ("could not extract: <reason>"), never silent success.
4. **Registration is idempotent**: re-registering the same source creates
   zero duplicate identities; interrupted view generation repairs on rerun.
5. **Drift detection**: `refresh` re-observes revision/hash; a changed
   source flips to a needs-review state, never silently updates meaning.
6. **Generated Markdown source views** (one per registered source, OKF 0.2
   frontmatter per contract) written to `run/views-staging/01 - Sources/`.
   Header-marked as generated; the register is the only update path.
7. **Retrieval integration**: catalogue-backed detail reachable from
   `internal/knowledge` query/get with an explicit `--private` working
   index. Do NOT regenerate the protected shared index — the write guard
   exists; if you hit it, that is the guard working, not a bug. Never weaken
   a guard to get green.
8. **`run/source-api.md`**: the exported Go API Lane C codes against —
   freeze it early, version any change loudly in that file.

## Tests and evidence

Focused `go test`/`go vet` on your packages; a mutation check for the
duplicate-registration guard (make the identity comparison wrong, show the
test fail, restore). Extraction caches are private and revision-bound;
none of their content enters Git or PR bodies (structural/synthetic
examples only). Every "works" claim carries real command output. PR body:
`Author-Lane:` per the plan. You review Lane A's PR (verdict comment per
protocol). Write `run/handoff-lane-b.md` before 19:15 EDT.
