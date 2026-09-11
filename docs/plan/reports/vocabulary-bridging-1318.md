# Vocabulary bridging after shipped semantic reranking — #1318

Measured 2026-09-10 EDT against merged `origin/main`
`2fbce3b069163a3ae21718a3f908d995db84ca8e`, with a final production refresh
at `d2b3f6d`. This PR contains the report and inert replay artifacts;
no bridge, fixture, threshold, source, or production-code changes.

## Answer and decision

**Generic vocabulary bridging is not the right next fix.** The shipped
reranker raises the operator-words baseline from **9/26 to 13/26 top-10
hits**, but fails three existing ratchets that BM25 passes on the same
index. Resolve those regressions and the benchmark/source-representation
defects below before choosing a new recall mechanism. There is a real,
smaller admission gap worth preserving as evidence; it does not justify
replacing the current ranker or broadening admission indiscriminately.

The earlier 65% headline should become: **BM25 still misses 17/26 exact
targets; opt-in semantic ranking misses 13/26 (50%), comprising one stale
fixture identifier, five present targets excluded by lexical admission, and
seven admitted targets ranked below ten.** These are exact-target results,
not a measured 50% rate of wrong answers. Some returned alternatives are
the corresponding distilled rule, and some indexed identifiers lack their
answer text.

The governing plan remains [PLAN.md](../PLAN.md) and its master plan. This
measurement supports reachable knowledge inside the existing memory work;
it starts no new architectural push. It follows the
[semantic-retrieval plan](semantic-retrieval-1255.md) and the
[implementation comparison on #1255](https://github.com/jonhill90/agent-estate/issues/1255).
The contradiction-order fix (#1368/#1373) and hermetic display-limit test
(#1369/#1374), including their integration repair #1377, are in the measured
base. The superseded Astra implementation was not resumed or modified.

## Method and frozen input

The first experiment was the real production path, before candidate work:
build the merged CLI in a clean detached worktree, create a new private
index, prepare its local embedding cache, then run every baseline question
with and without `--semantic`. Every semantic result disclosed
`ranking_method=semantic`, with no fallback; every result returned ten
matches. A separate full-candidate probe reproduced all 26 semantic CLI
top-ten lists exactly before being used for counterfactuals.

| Source | Items |
|---|---:|
| GitHub stars | 412 |
| Vault facts | 3,203 |
| Corpus items | 4,058 |
| Loops research | 28 |
| Repo docs | 158 |
| Catalogue | 30 |
| **Total; all six source reads OK** | **7,889** |

The fixture file remains unchanged. Its current 26 cases include repo-docs,
vault-fact, and corpus-parameter targets; the older description of it as
only the vault/corpus layer is incomplete.

Private evidence root: `/tmp/vocabulary-1318-evidence`; index:
`/tmp/vocabulary-1318-evidence/index.json`. Sources were read-only, including
mtimes. No `--allow-shared-write`, shared-index regeneration, source repair,
model installation, or off-machine embedding request was used. The existing
local `text-embedding-nomic-embed-text-v1.5` model served
`http://localhost:1234/v1` through the production loopback-only client.

Frozen SHA-256 identities:

```text
index   bf6e66c280cfd4e14d302a0edeb46b8c4d7f7ce86b85ebf91e8adc5c39d154ec
cache   7e6da4a5296f5cf99a08930c79fcec5df3d271308bb7bdc7f844c1d6547cb643
fixture e060e8f835078d363a03900223efd96e0ae2f88ff20add09b07c0ce13416226d
```

Production-source content and mtime manifests compare equal before/after:
corpus, catalogue, vault notes and Loops. Excluded Obsidian UI state changed
externally and is disclosed in the raw manifests. A before-hash of the
shared index was not captured; no unchanged-hash claim is made about it.

### Preparation and query costs

| Measurement | Result |
|---|---:|
| Private index preparation | 6.74 s |
| Index size | 17,555,629 bytes |
| Local embedding preparation, 7,889 items | 154.90 s |
| Embedding sidecar size | 75,873,040 bytes |
| BM25, 26 real CLI queries | 10.281 s total; 0.395 s mean |
| Semantic, same 26 queries | 31.302 s total; 1.204 s mean |
| Semantic incremental mean | 0.809 s; approximately 3.05× total latency |

These are one-host measurements with a warm prepared cache, not latency
SLOs. The semantic path still reads/parses its sidecar and builds BM25
statistics for each CLI invocation. Index-time normalization is therefore
not automatically free at query time: [NewBM25Scorer](../../../src/estate/internal/knowledge/bm25.go) tokenizes all items on
every query. Parallel candidate runs are not comparable latency samples.

### Publication-time check against newer main

While the experiments ran, main advanced to
`d2b3f6d054765d35516cc684c1978d2bb694da59`. A second clean build and new
private index at `/tmp/vocabulary-1318-current-evidence/index.json`
repeated the production 26-case comparison and both full goldenquery runs.
The index has **7,890 items**, adding one skills-registry row; the six
counts above and all four fixture files are unchanged. Every 26-case
production hit/rank result and all **112 case-status rows per ranking
mode** match the original run. BM25 again passes all seven checks;
semantic again fails the same three. No freshness bypass was used.

```text
current index SHA-256 d32a9baacf354f4915c4518e73ea7b7f89810b02bf266256d70d8e98e31dd323
current cache SHA-256 c0be8953ad9b816556b7b5e83966d66a6fba4720f40eba12c880715f0662c9d1
```

New preparation: index 9.79 s / 17,557,369 bytes; cache 152.43 s /
75,882,708 bytes. The 26 real queries took 10.546 s BM25 and 31.776 s
semantic; full goldenquery took 51.73 s and 146.38 s, respectively.
Source content and mtime manifests compare equal before/after, and the
original frozen index/cache hashes remain unchanged. Candidate experiments
below retain the original 7,889-item input; this refresh checks whether
their production starting point moved, rather than silently mixing inputs.

## Production result, case by case

Ranks below are full eligible-population ranks, not zero substituted for
everything below ten. `—` means present but not lexically admitted; `absent`
means the expected identifier is not in this index. Success remains rank
1–10, using the unchanged fixture's expected source and permalink.

| Case | BM25 | Semantic | What changed |
|---|---:|---:|---|
| rb-01 | 3 | 1 | Hit retained |
| rb-02 | 145 | 323 | Miss retained |
| rb-03 | 75 | 143 | Miss retained |
| rb-04 | — | — | Admission gap |
| rb-05 | 123 | 84 | Miss retained |
| rb-06 | — | — | Admission gap; answer text truncated |
| rb-07 | — | — | Admission gap; answer text truncated |
| rb-08 | absent | absent | Stale fixture anchor |
| rb-09 | 1 | 1 | Hit retained |
| rb-10 | 10 | 2 | Hit retained |
| rb-11 | — | — | Admission gap |
| rb-12 | 1 | 1 | Hit retained |
| rb-13 | 42 | 420 | Miss retained |
| rb-14 | 6 | 77 | **Regression** |
| rb-15 | 4 | 1 | Hit retained |
| rb-16 | 6 | 1 | Hit retained |
| rb-17 | 17 | 6 | **Fixed** |
| rb-18 | 27 | 1 | **Fixed** |
| rb-19 | 53 | 12 | Miss retained |
| rb-20 | 944 | 5 | **Fixed** |
| rb-21 | 138 | 1 | **Fixed** |
| rb-22 | 12 | 2 | **Fixed** |
| rb-23 | — | — | Admission gap |
| rb-24 | 29 | 32 | Miss retained |
| rb-25 | 3 | 1 | Hit retained |
| rb-26 | 2 | 1 | Hit retained |

Top-three hits increase from 5/26 to 11/26. Top-ten gains are five fixes
minus one regression, net **+4**, not the old ceiling's +6. The
[merged implementation's own report](https://github.com/jonhill90/agent-estate/pull/1361)
already measured a different ceiling on its 7,755-item snapshot. Corpus
growth is enough reason to state each run's actual result, not copy an
older numerator.

### Why “present in the index” overstates coverage

1. **rb-08 is a stale identifier.** Commit `04aee5d` added
   `[--harness=NAME]` to the SPEC's dispatch heading. Its indexed anchor
   changed; the fixture still names the old anchor. The current item,
   `it-c946e54d6cbfd513`, contains the timeout rule but remains outside both
   top-ten lists. Correcting the pointer would reclassify the miss, not
   manufacture a hit. No fixture was changed for this report.
2. **rb-06/rb-07 lack their answer text in searchable fields.**
   [repoDocsSource](../../../src/estate/internal/knowledge/docs.go) writes
   `Tier2: truncate(body, 800)`. The PRD Parameters
   section's first item now consumes that prefix; items 2 and 6 contain
   these answers in the source but lie beyond it. The section identifier
   exists while the relevant statement does not survive indexing. A
   better synonym for the question cannot restore omitted text.
3. **Exact-target hits differ from answerability.** rb-11, rb-19, and
   rb-23 each have a distilled rule with primary `corpus_item:` provenance
   linking to the expected corpus item. The unchanged fixture does not
   accept that rule's permalink. rb-19's rule is BM25 rank 1; rb-23's rule
   is BM25 rank 1 and semantic rank 5. Those remain fixture misses here,
   but must not be called wrong documents solely for having another ID.

The two PRD findings were checked against the frozen item's searchable
fields and the source file at the measured commit. They are content
coverage defects, not evidence that all five admission failures share one
morphological cause.

## Displacement and known-absence costs

Read directly from `git show origin/main:src/estate/cmd/goldenquery/main.go`:

```text
nlTop3MaxMisses    = 12
retrievalMaxMisses = 5
reachableMaxMisses = 0
starTop3MaxMisses  = 1
starTop10MaxMisses = 1
noneMaxMisses     = 0
```

There are six constants and seven printed checks: the natural-language
constant governs both unscoped and scoped top-three results. Denominators
are current, not copied from stale comments beside the constants.

| Check | Required hits | BM25 | Shipped semantic |
|---|---:|---:|---:|
| Natural language top-3, public unscoped | 18/30 | 18/30 | **15/30 FAIL** |
| Natural language top-3, private repo-docs scoped | 18/30 | 18/30 | **16/30 FAIL** |
| Retrieval top-3, private | 17/22 | 17/22 | 18/22 |
| Publishable-reachable top-3 | 5/5 | 5/5 | **4/5 FAIL** |
| Stars top-3 | 7/8 | 7/8 | 7/8 |
| Stars top-10 | 7/8 | 7/8 | 7/8 |
| none-01 returns no_match | 1/1 | 1/1 | 1/1 |

Unratcheted natural-language top-ten results: public unscoped 20/30 →
20/30; private scoped 22/30 → 19/30. BM25 runner exit **0**; semantic
runner exit **1**. No freshness refusal explains these differences.

Every production HIT→MISS at its applicable window:

- Baseline top-10: `rb-14`.
- Private cases top-3: `loops-01`, `loops-02`; gains are `vault-01`,
  `corpus-03`, `camelcase-03`.
- Publishable cases top-3: `docs-01`.
- Public natural top-3: `nl-05`, `nl-14`, `nl-24`, `nl-30`; gain `nl-06`.
- Scoped natural top-3: `nl-05`, `nl-15`, `nl-17`, `nl-24`; gains
  `nl-06`, `nl-22`.
- Public natural top-10: `nl-07`, `nl-14`; gains `nl-03`, `nl-22`.
- Scoped natural top-10: `nl-07`, `nl-23`, `nl-24`; no compensating gain.

These ratchets measure displacement of preselected answers and preserve one
verified absence. They do not label every other returned item irrelevant,
so “nine other results” cannot honestly be reported as nine false
positives. The distilled twins above demonstrate why. The report keeps
this precision-label gap explicit rather than converting exact-target
recall into an invented precision percentage.

## Candidate design and measured boundaries

### Existing aliases or question forms

A bounded implementation would read already-authored `aliases` or
`recall_triggers` on distilled rules into a separate searchable field or
lookup map. The current index contains 233 eligible `Fact` + `#rule`
items: **zero** have aliases, recall triggers, or question-form fields.
Across all 3,203 vault items, 118 have legacy-slug aliases; none has a
recall trigger or question form. The vault parser ignores these as typed
searchable metadata; raw frontmatter survives only in unscored Tier3.

The remaining 13 expected targets include no eligible distilled rule:
eight repo-docs, three corpus parameters, and two feedback facts. Hence an
existing-metadata-only bridge on those rules has an **exact-target ceiling
of 0/13**, consistent with [#1327](https://github.com/jonhill90/agent-estate/pull/1327)'s
earlier 0/17 result. This is a structural bound, not a reranking run and
not proof that newly authored forms could never help equivalent answers.

For this empty eligible input there are no added postings, serialized bytes
or matches to measure. A populated design would require authoring and reviewing
new metadata, then measure its bytes, query costs, precision and provenance
coverage on held-out questions. Those costs are **unmeasured**; claiming a
benefit now would be recommending an unbuilt training set. Appending forms
to existing BM25 fields would also change lengths and document frequencies
on every query. An exact lookup map has a different cost profile and no
implementation today. Neither cost is an excuse to ingest raw prompts or
invent target-specific synonyms.

### Morphology and identifier splitting

The camelCase experiment is symmetric document/query tokenization: preserve
each original token, add components before lowercasing, and preserve negation.
It operates on lexical input, leaving source text and semantic embeddings
unchanged. This isolates lexical admission/ordering from semantic text
changes. It affects **1,385/7,889 items** and adds **243 candidate/question
pairs across 26 questions**, at most 54 on one question. These are extra
candidates, not independently judged false positives.

BM25 improves **9/26 → 10/26**, solely rb-22 (rank 12 → 7), which shipped
semantic already answers. Semantic stays **13/26** with no new baseline
hit or lost hit. It reaches **none of the 13 current semantic misses**.
The five present excluded targets remain excluded.

All full goldenquery aggregates and hit/status classifications are identical
between the lexical control and projection, in both ranking modes. Thus the
projection repairs none of semantic mode's three failing checks and adds
none. These four scratch CLI runs disabled freshness folding and stale
withheld-evidence refusal, equally in control and candidate, to isolate the
frozen index. They test ranking behavior, not live freshness enforcement.
Their control results equal the separate unmodified production runs above;
the bypass was not used for those production measurements or RRF. The
26-case ranks, #1150 probes and component benchmarks also used no bypass.

The projection stores no new index or embedding bytes: its document work
runs inside per-query scorer construction. Across three alternating
12-pair runs, median scorer construction costs an extra **20.507–35.175 ms
(11.6–17.1%)**. A separate five-sample benchmark gives medians
151.642 → 180.470 ms, with independently computed median allocations
128.074 → 163.256 MB/op (**+35.182 MB**). Tokenization alone over all
scored fields costs 39.947 → 62.408 ms (**+22.461 ms**) and adds
34.826 MB/op. These are component costs, not end-to-end CLI speed claims.
Moving the projection to a persistent precomputed structure would be a
different design with unmeasured build time, storage and invalidation costs.

Full Porter steps 2–5 are not reimplemented here. [#1327](https://github.com/jonhill90/agent-estate/pull/1327)
already measured 9/26 → 8/26, regressing rb-10; its exploratory source was
reverted rather than preserved as a reusable probe. A newly improvised
stemmer would repeat a rejected direction without a trustworthy controlled
implementation. This historical result is not relabeled as a fresh run.

### Semantic admission beyond the lexical pool

The counterfactual keeps current-revision, privacy and source policies but
ranks every policy-eligible item's cached vector. The unscoped private
population is all 7,889 items. It introduces no new model or text, and no
similarity threshold selected after seeing the cases.

It reaches **rb-11 at rank 5 and rb-23 at rank 1**, both excluded by the
current lexical floor. It also loses rb-10 (rank 15) and rb-20 (rank 21):
baseline hits remain **13/26, net zero against shipped semantic**. The
other excluded targets land at ranks 1,443 (rb-04), 5,059 (rb-06), and
6,976 (rb-07). Broadening admission cannot fix an already-admitted target
by itself when the same cosine ordering is retained: it can only add
competitors ahead of that target. It fails **five of seven printed
ratchets**, including the absence check: with no new rejection threshold,
the query whose correct answer is none receives a nonempty candidate list.

With the cache already resident, the measured vector lookup/cosine/sort
step has median **0.882 ms**, mean **4.32 ms**, and p95 **15.706 ms** across
190 arm/case evaluations; question embedding averages **13.173 ms**.
Those exclude production index/cache loading and BM25 preparation and are
not an end-to-end CLI latency claim. Index/cache preparation and serialized
bytes do not change; the existing cache already embeds every item. A
production bridge would still need a measured absence policy and accurate
disclosure for evidence admitted without lexical support. Neither is
smuggled into this ceiling experiment.

### Combining lexical and semantic ranks

A separate ranking counterfactual uses equal-weight reciprocal-rank fusion:
`1/(k + lexical_rank) + 1/(k + semantic_rank)`, ties retaining lexical
order. Two fixed alternatives, `k=10` and `k=60`, were run; no search for a
per-question setting, learned weight, special term or fixture exception.
It reorders the same admitted candidates and uses the original embeddings.

Both achieve **14/26**. `k=10` repairs rb-14 while retaining every shipped
semantic baseline hit. `k=60` repairs rb-14 and rb-19 but loses rb-20. This
is only a one-hit net gain; it cannot reach the five admission gaps. The
full ratchet result, not this selected stratum, determines its disposition:
**both still fail publishable-reachable retrieval, 4/5 instead of 5/5**.
Neither is ready to ship. Both natural top-three checks recover to passing
levels, which makes this a bounded ranking lead rather than a reason to
build a vocabulary layer.

The extra rank-list sort measured approximately **1.06 ms mean for k=10**
and **1.03 ms for k=60** over the 26 baseline cases plus vault-02. It adds
no index construction, document embeddings, model calls or persistent
bytes; these sort-only timings exclude rank-map creation and the existing
semantic path. The two full CLI runs used the unchanged goldenquery
runner against scratch binaries with only this rerank wrapper changed.

### Comparative ratchet table

The two RRF columns have identical aggregate counts, but different baseline
case effects, so they are not averaged. Dense is an API-level
counterfactual; the other rank experiments use CLI goldenquery.

| Check | Floor | Shipped semantic | RRF-10 | RRF-60 | Global dense |
|---|---:|---:|---:|---:|---:|
| Natural public top-3 | 18/30 | 15 | 19 | 19 | 14 |
| Natural scoped top-3 | 18/30 | 16 | 18 | 18 | 13 |
| Private retrieval top-3 | 17/22 | 18 | 18 | 18 | 14 |
| Publishable-reachable top-3 | 5/5 | 4 | 4 | 4 | 4 |
| Stars top-3 | 7/8 | 7 | 7 | 7 | 7 |
| Stars top-10 | 7/8 | 7 | 7 | 7 | 8 |
| none-01 no_match | 1/1 | 1 | 1 | 1 | 0 |
| **Failed checks** | **0 allowed** | **3** | **1** | **1** | **5** |

RRF unratcheted natural top-ten counts are 21/30 public and 20/30 scoped,
for both k values. Full per-case candidate outcomes, including regressions
hidden by unchanged aggregates, remain part of the evidence.

At the original BM25 cutoffs, both RRF variants still lose private
`loops-02`, public `docs-01`, scoped top-3 `nl-05`, scoped top-10
`nl-07`/`nl-23`, and public top-10 `nl-07`. Relative to shipped semantic,
RRF-10 newly loses private `camelcase-03`, while RRF-60 newly loses private
`vault-01`; both lose scoped top-3 `nl-06`. The equal private aggregate
therefore conceals a different lost case for each k.

Dense's additional losses versus shipped semantic are private top-3
`vault-01`, `corpus-02`, `docs-02` and the private `none-01` case; public
natural top-3 `nl-06`; scoped top-3 `nl-06`/`nl-14`/`nl-22`; and stars
top-3 `stars-nl-05`. It also loses public natural top-ten
`nl-05`/`nl-22`/`nl-24` and scoped top-ten `nl-05`/`nl-17`. Public absence
fails too. Stars top-ten improves to 8/8; that gain does not compensate for
any failed check.

### Which current misses each candidate reaches

| Candidate | Current semantic misses reaching top-10 | Baseline hit lost versus semantic |
|---|---|---|
| Existing rule aliases/forms | None; structural bound | No result change |
| CamelCase projection | None | None |
| Global dense admission | rb-11, rb-23 | rb-10, rb-20 |
| RRF-10 | rb-14 | None |
| RRF-60 | rb-14, rb-19 | rb-20 |

**None of these tested candidates reaches rb-02, rb-03, rb-04, rb-05,
rb-06, rb-07, rb-08, rb-13 or rb-24.** This is the interesting residual,
not a claim that every possible bridge has been disproved. One identifier
is stale, two answers were truncated, one other target is excluded by
lexical admission but ranks 1,443 under unrestricted cosine, and five are
already admitted yet survive both ranking alternatives below ten. There
is no single measured morphological remedy for that set.

## #1150 and #1063: what the claim actually bounds

The tokenizer defect in [#1150](https://github.com/jonhill90/agent-estate/issues/1150)
is real: unsplit `loggedIn` does not contribute query stem `log`. That
does **not** imply its document is unreachable by a ranking-side change.
The document is already admitted through other terms: on this snapshot
`vault-02` ranks **26 in BM25, 17 in shipped semantic, 16 with RRF-10,
and 4 with RRF-60**. The top-three fixture still fails all four; the
camelCase projection contributes `log` (count 0 → 1) but moves the target
only to **BM25 rank 14**, and semantic rank **18**. It still fails top
three. The older rank-5 → rank-2 success therefore does not reproduce on
this corpus. No field weights were tuned for this report.

The claim needs two separate dispositions: **weight tuning cannot create
the missing token match**, but **missing that token is not a hard
ranking-side reachability bound**, since this target is already admitted.
The old finite tuning sweeps and every ranking candidate measured here
fail the top-three criterion; this report does not claim otherwise. It
also does not turn those negative experiments into a proof that every
ranking-side fix must fail, or that indexing-time splitting alone now
suffices. #1150 remains a real tokenizer example with a changed measured
outcome, not a universal bound on ranking.

[#1063](https://github.com/jonhill90/agent-estate/issues/1063) correctly warns
against conflating `requested` with `unrequested`. Neither the camelCase
projection nor the ranking experiments strip negation prefixes. Recovering
an answer by erasing the term that determines its meaning is not a recall
fix. The old substring behavior is not reinstated.

## Next work and acceptance criteria

1. Repair the measurement contract separately: restore rb-08's current
   source pointer; audit answer-bearing text coverage; explicitly decide
   how primary-provenance rule twins should be scored. Keep the existing
   results beside any corrected measurement, so fixture repair cannot
   masquerade as retrieval improvement.
2. Resolve or explicitly dispose of the shipped semantic mode's three
   failing ratchets before broadening admission. A proposal must show all
   strata, every lost case and fallback behavior, not just a positive net
   score on the operator-words baseline.
3. Preserve rb-11/rb-23 as genuine semantic-admission examples, and
   rb-06/rb-07 as omitted-content examples. A future bridge needs held-out
   positive and absent-answer cases and a visibility policy for evidence
   with no lexical match. No new alias list, morphology rule, threshold
   or candidate-union implementation is authorized by this report.

Stop when a candidate has no material gain or fails the unchanged budgets;
do not tune toward whichever one case made it look promising. A future
source-representation change needs separate cost measurement: this report
diagnoses the 800-character loss, but does not claim that removing the cap
or splitting lists has already been shown safe.

## Reproduction and handoff

Production commands, from clean commit `2fbce3b`:

```sh
go build ./src/estate/...
go build -o /tmp/vocabulary-1318-evidence/bin/estate ./src/estate
export ESTATE_KNOWLEDGE_INDEX=/tmp/vocabulary-1318-evidence/index.json
/tmp/vocabulary-1318-evidence/bin/estate knowledge
/tmp/vocabulary-1318-evidence/bin/estate knowledge embeddings
go run ./src/estate/cmd/goldenquery -baseline -v -bin /tmp/vocabulary-1318-evidence/bin/estate
go run ./src/estate/cmd/goldenquery -v -bin /tmp/vocabulary-1318-evidence/bin/estate
```

For semantic runs, the scratch Go `semantic-adapter.go` inserts only
`--semantic` after `knowledge query` and `exec`s `ESTATE_REAL_BIN`.
The unchanged goldenquery runner still observes the real CLI's stdout and
exit status. Production evidence is in `raw/` and `summaries/` beneath the
private evidence root; no private query result or source text is committed.
The full-rank comparison is `summaries/retrieval-26-full-ranks.tsv`, and all
production case transitions are `summaries/goldenquery-status-flips.tsv`.
The [replay artifacts](vocabulary-bridging-1318-evidence/README.md) preserve
the exact candidate mechanisms, safe per-case tables and benchmark outputs.
They are inert files under this report, not compiled application code.
Private inputs and query-result bodies stay outside source control. New
index builds may produce different numbers as sources grow; retain the
frozen index for exact reproduction.

### Validation

`go build ./src/estate/...` on the clean measured checkout: exit 0, no
stdout/stderr. Both archived patches pass `git apply --check` at the pinned
base (exit 0, no output); the capture helper builds and returns:

```text
private production controls captured; existing files preserved
```

`git diff --cached --check`: exit 0, no output. The evidence directory's
local Git attributes preserve meaningful patch context and empty final TSV
columns. `python3 scripts/docs-lint/lint_docs.py` returns:

```text
docs-lint: clean -- no unclassified root files, no state files in docs/, zero full-text duplicates.
```

`python3 -m unittest discover -s scripts/docs-lint -p 'test_*.py'` returns:

```text
................
----------------------------------------------------------------------
Ran 16 tests in 0.009s

OK
```

The scratch lexical suite also completed `go test ./src/estate/...` with
all packages passing; its output includes:

```text
ok  	github.com/jonhill90/agent-estate/estate	76.486s
ok  	github.com/jonhill90/agent-estate/estate/cmd/goldenquery	138.226s
ok  	github.com/jonhill90/agent-estate/estate/internal/knowledge	2.243s
```

The real semantic goldenquery run intentionally remains a **failed**
validation result (exit 1), with its three failing checks shown above.
Neither benchmark changes nor a passing Go build erase those failures.
The runner's status lines, with explanatory suffixes omitted:

```text
  [FAIL] natural-language stratum top-3, unscoped: 15/30 (floor 18)
  [FAIL] natural-language stratum top-3, private scoped source:repo-docs: 16/30 (floor 18)
  [OK] retrieval score (private): 18/22 (floor 17)
  [FAIL] publishable-reachable score: 4/5 (floor 5)
  [OK] github-stars stratum top-3: 7/8 (floor 7)
  [OK] github-stars stratum top-10: 7/8 (floor 7)
  [OK] none-01 (absence must report no_match): 1/1 (floor 1)
```

An independent read-only review recomputed the baseline, compared both
snapshots, checked patch replay and inspected the publishable artifacts.
It led to durable replay files, explicit lexical freshness-bypass scope and
the displacement/absence wording above. Its final result was:

```text
No critical or warning-level report defects remain.
Verdict: PASS for publication
```

The review conditioned publication on excluding scratch application files;
only this report and its inert evidence directory are staged. Its separate
interrupted broad test attempt is incomplete and is not cited as validation.
This research review is not a merge-gate verdict or authorization to merge.

Refs jonhill90/agent-estate#1318
