# Replay artifacts for vocabulary bridging #1318

These are inert research artifacts, not application changes. Apply/copy
them only into separate disposable worktrees at commit
`2fbce3b069163a3ae21718a3f908d995db84ca8e`. Never apply the two instrumentation
patches to the same checkout. The main report defines the experiment and
its limitations; these files preserve the exact mechanisms and safe result
tables so a later reviewer need not reconstruct code from prose.

The private index and vectors are intentionally not included. Exact replay
requires the authorized frozen index/cache with the report's hashes at
`/tmp/vocabulary-1318-evidence/index.json` and
`/tmp/vocabulary-1318-evidence/index.json.embeddings.json`, plus the existing
local embedding model. New source snapshots produce a new measurement.
Never replace the shared index or write source files to reproduce this.

## Production controls

In an unmodified worktree at the pinned commit, build estate and goldenquery
into `/tmp/vocabulary-1318-evidence/bin/`. Set `ESTATE_KNOWLEDGE_INDEX` to the
private index. Copy `semantic-adapter.go.txt` to a scratch `.go` filename and
build it. Set `ESTATE_REAL_BIN` to the unmodified estate binary, then run
goldenquery with `-bin` naming either estate or that adapter, once with
`-baseline -v` and once with `-v` alone. The adapter inserts only `--semantic`.

Copy `capture.go.txt` to `src/estate/cmd/vocabularycapture/main.go` in that
disposable checkout and run `go run ./src/estate/cmd/vocabularycapture` with
the same two environment variables. It writes the private 26-case CLI JSON
controls needed by the ranking probe under the index's `raw/` sibling.
Those outputs contain private material and must remain outside source control.

## Full ranks, fusion and dense admission

Copy `ranking-probe.go.txt` to
`src/estate/cmd/vocabularyprobe/main.go`, and `dense-probe.go.txt` to
`src/estate/cmd/denseprobe/main.go`, in a disposable checkout. Run each with
`go run ./src/estate/cmd/<name>` and the private index environment variable.
The ranking probe compares its cosine order against every saved production
semantic top-ten list. The dense probe retains policy filtering through
production tag-only Query calls and intentionally adds no absence threshold.
It writes case IDs/ranks/timings, not source bodies. Its output directory is
`/tmp/vocabulary-1318-dense`.

For the real-CLI fusion ratchets, use a separate disposable checkout, apply
`rrf-main.patch`, and copy `rrf-wrapper.go.txt` to
`src/estate/vocabulary_experiment.go`. Build estate into a new scratch
binary. Point `ESTATE_REAL_BIN` at it and run unchanged goldenquery through
the semantic adapter with `ESTATE_EXPERIMENT_RRF_K=10`, then `=60`.
Without that environment variable the wrapper preserves semantic order.

## Lexical normalization

In another disposable checkout at the same commit:

```sh
git apply /absolute/path/to/lexical.patch
export ESTATE_LEXICAL_INDEX=/tmp/vocabulary-1318-evidence/index.json
export ESTATE_KNOWLEDGE_INDEX="$ESTATE_LEXICAL_INDEX"
go build -o /tmp/vocabulary-1318-projected-estate ./src/estate
go test ./src/estate/internal/knowledge -run '^TestLexicalProjectionShape$' -count=1 -v
go test ./src/estate/internal/knowledge -run '^TestLexicalExperiment(AffectedDocuments|AffectedQueries|AlternatingConstruction)$' -count=1 -v
go test ./src/estate/internal/knowledge -run '^$' -bench '^BenchmarkLexicalScorerConstruction$' -benchtime=5x -count=5 -benchmem
go test ./src/estate/internal/knowledge -run '^$' -bench '^BenchmarkLexicalProjectionOnly$' -benchtime=10x -count=5 -benchmem
```

The scratch JSON instrumentation accepts `ESTATE_LEXICAL_EXPERIMENT_TARGET`
as an item ID and reports full lexical/final rank without enlarging the
production display limit. Obtain that ID from the private index by the
unchanged fixture's source and expected permalink suffix. For the token
probe, also set `ESTATE_LEXICAL_VAULT02_TARGET` and run
`TestLexicalExperimentVault02Token`. The patch includes all scratch tests.
Run the same goldenquery commands through the projected binary, including
semantic mode; set `ESTATE_LEXICAL_EXPERIMENT_LEGACY=1` for its lexical
control. The four retained full-golden runs also set
`ESTATE_LEXICAL_EXPERIMENT_FROZEN_INDEX=1` equally in both variants. This
disables only post-query freshness folding and stale withheld-evidence
refusal, so these runs measure production ranking over a frozen index,
not live trust-state enforcement. The index had aged past live source
mtimes; source writes and frozen-input regeneration were prohibited.
Direct 26-case ranks, vault-02 probes, benchmarks and tests did not use
that bypass. The source text and original embedding cache stay unchanged.

## Included output

The TSV files contain case IDs, ranks, cutoff transitions and counters only.
Benchmark logs contain numeric timing/allocation output. They are the
publishable evidence; they are not private query result dumps. Query-time
measurements depend on host load. Read the main report before comparing
resident-cache scan timings with end-to-end CLI latency.

The local Git attributes preserve exact patch context and empty final TSV
fields, which otherwise look like trailing-whitespace errors. In the
current-main CLI table, rank 0 means outside the displayed top ten; the
separate `production-ranks.tsv` preserves the controlled full ranks.
