# Local semantic retrieval — #1255

Implementation plan, 2026-09-10. Base `67bc462`, branch
`feat/semantic-retrieval`, as required by the operator's implementation brief.
This continues the measurement work in #1338/#1344 and the earlier
`feat/retrieval-ranking` checkpoint. No fresh-worker acceptance claim follows
from a query ranking measurement.

## Goal / signal

Make the measured semantic ranking available through `estate knowledge query`
while retaining a working BM25 answer without a local model. Reproduce the
claimed seven MISS→HIT flips and rb-14 HIT→MISS before implementation, then
report the production path's actual 26-case result and every regression.

## Scope

One optional local cosine reranker over the full eligible BM25 candidate
population, a removable function seam, a separate embedding cache, CLI wiring,
and disclosure of active ranking and fallback. No model installation, remote
embedding requests, shared index regeneration, source writes, baseline fixture
edits, admission-policy changes, standing-law edits, or merges.

## Test matrix

| Requirement | Check |
|---|---|
| Reproduce measurement | Existing `TestSemanticCeilingExperiment`, uncached frozen-index run |
| Semantic ordering reaches below top ten | Failing synthetic Query seam reproduction, then injected cosine ranks |
| Exact BM25 fallback | Nil/error/timeout/malformed-rank tests compare result order and counts |
| Privacy, tags, revision admission | Injected ranker observes only eligible candidates; original filters retained |
| Local-only transport | Loopback validation, redirect refusal, proxy bypass, bounded timeout HTTP tests |
| Cache correctness | Model/input/dimension/version validation; missing/stale/corrupt cache fallback; atomic private writes |
| Ranking disclosure | JSON and prose show active method; scores remain explicitly identified |
| Real deployment path | Warm private cache, run CLI baseline and all existing strata, disable/server-absent comparison |

## Implementation steps

1. Reproduce #1344 against the fixed 7,726-item snapshot and verify protected
   hashes. Inspect current `main`, the `ReadQuota`/`CountWorktrees` seams,
   query policy, and the live-test skip convention.
2. Commit/push this plan. Obtain an independent review of the design's cache,
   privacy, fallback, and score-semantics failure modes.
3. Write and run failing tests before the corresponding behavior change.
4. Add a function seam to Query with a nil BM25 default. Apply optional
   reranking only after existing admission/privacy filtering and before the
   display limit. Reject incomplete or invalid ranker results as a whole.
5. Add an isolated local embedding adapter and versioned sidecar cache.
   `knowledge embeddings` prepares it explicitly; queries never embed the
   document corpus or start a server. Main supplies the adapter; a BM25
   switch removes it. Preserve the existing knowledge index schema.
6. Measure the real CLI on identical private inputs before/after, including
   every HIT→MISS and all existing strata. Verify fallback with the model
   endpoint unavailable. Run required checks, push, open a PR, report checks.

## Verification matrix

| Command | Expected result |
|---|---|
| `ESTATE_RANKING_EXPERIMENT_INDEX=<private-index> go test ./src/estate/internal/knowledge -run '^TestSemanticCeilingExperiment$' -count=1 -v` | Actual measured ceiling and both flip sets |
| `go test ./src/estate/internal/knowledge ./src/estate/internal/embedding` | Synthetic seam/cache/transport tests pass after captured failures |
| `ESTATE_KNOWLEDGE_INDEX=<private-index> estate knowledge embeddings` | Explicit sidecar creation; original index hash unchanged |
| `ESTATE_KNOWLEDGE_INDEX=<private-index> go run ./src/estate/cmd/goldenquery -baseline -v -bin <binary>` | Report before/after and regressions |
| Same CLI without `-baseline` | Compare all existing strata, no silent ratchet changes |
| `go build ./src/estate/...` / `go vet ./src/estate/...` / `go test ./src/estate/...` | Exit 0; report any failure verbatim |
| Existing docs-lint tests and `python3 scripts/docs-lint/lint_docs.py` | Exit 0 |
| `git diff --check`, protected hashes, `gh pr checks <n>` | Actual output retained in final report |

## CI / drift gates

CI tests the deterministic seam, transport, validation and fallback without
LM Studio. Real embedding checks explicitly skip with “this run checks
nothing” when their private prerequisites are absent. The baseline remains a
measurement rather than a new ratchet; no existing threshold is loosened.

## Risks / mitigations

- Cosine can regress lexical hits: name every regression, including rb-14;
  compare all strata, rather than claiming a net gain is uniformly better.
- Model/cache changes: validate model identity, input format, each candidate's
  text hash, and vector shape; fail the entire optional rerank back to BM25.
- Latency: explicit preparation takes the bulk embedding work off queries;
  query requests have a short deadline and never start or install a model.
- Leakage: fixed local-only transport, no proxies/redirects, private cache
  permissions, no raw source/error-body dumps in logs or reports.
- Reversibility: main supplies one optional function; nil/disabled uses BM25;
  original index/readers and authoritative sources are untouched.

## Definition of done

- [ ] #1344's numbers independently reproduced, limitations named.
- [ ] Failing behavior tests captured, then passing and retained.
- [ ] Local production adapter, cache preparation, fallback, disclosure wired.
- [ ] Before/after, all regression cases, and fallback measured via real CLI.
- [ ] Build/tests/CI checked; protected inputs unchanged.
- [ ] Branch pushed and PR opened for a different lane; no merge performed.

## Stop conditions

Install nothing. Stop and report if the existing model cannot support the
direction. Do not weaken disclosure or input protections to obtain a better
number. Do not hide a negative measurement. The durability judgement request
is independent work on `feat/durability-judgement`, with no shared code edits.
