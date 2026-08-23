---
type: Research
description: Evidence for the per-agent memory storage-format decision (agent-tui#116) -- does not choose a format; that stays reserved to Jon.
generated:
  at: 2026-08-23T07:10:03-04:00
---

# agent-tui#116: evidence for the per-agent memory storage decision

**This document does not choose a storage format.** How per-agent memory is
stored — the same OKF format the shared vault uses, vectors, or "graphify" —
is an open question the operator has reserved for himself. This is evidence
to choose from, not a recommendation to adopt one.

Scope: storage format for the new per-agent tier only. The knowledge-graph
*view* (drag-around rendering) is agent-tui#61, already spiked in
`docs/memoryvariants/` — not revisited here.

## What "graphify" actually refers to

Confirmed against a primary source: `github.com/Graphify-Labs/graphify`,
checked directly via `gh api repos/Graphify-Labs/graphify` on 2026-08-23.

```
full_name:      Graphify-Labs/graphify
description:    Turn any codebase, with its docs, SQL schemas, configs, and
                 PDFs, into a queryable knowledge graph. A /graphify skill
                 for Claude Code, Cursor, Codex, and Gemini CLI: local
                 deterministic AST parsing, every edge explained, no
                 vector store.
license:        Apache-2.0
stargazers:      109,676
forks:           10,665
open_issues:     1,051
created_at:      2026-04-03
pushed_at:       2026-08-20   (3 days before this check — active)
archived:        false
```

It is a `/graphify` skill for coding-agent CLIs that parses a codebase (code,
docs, SQL schemas, configs, PDFs) with local, deterministic tree-sitter AST
parsing — no LLM, no vector store — into `graph.json` (tagged `EXTRACTED`/
`INFERRED` edges), `GRAPH_REPORT.md` (communities, "god nodes," surprising
connections via Leiden clustering), and a force-directed `graph.html`. It
optionally pushes the graph to Neo4j or FalkorDB, and can serve it over MCP.

**One thing worth flagging before it sits in a comparison table as a peer of
the other two:** in this estate's own record, "graphify" was named by Jon as
a *visual* comparison point for Hill90's shared-knowledge-graph renderer,
alongside Obsidian's graph view and a direct link to this same repo, in
prompts about the force-directed rendering of an *existing* graph
(corpus rows mp-0471024040322b94, mp-5f8f8f6261fb310a — paraphrased here
rather than quoted, since neither row has a cleaned form yet and the raw
form is not for publishing per this estate's own quoting convention).
Nothing in that record names it as a candidate for *how per-agent memory
gets stored*; #116 is the first place it appears as a storage option rather
than a look-and-feel reference. That doesn't disqualify it, but it changes
what "adopting graphify" would mean here: Graphify is an
extractor that turns *source material* (a codebase) into a graph via parsing,
not a facts store an agent writes discrete memories into turn by turn. Using
it for per-agent memory would mean treating each agent's memory notes as "the
codebase" and re-running its parse/reindex pipeline over them — a workable
but unusual fit for its actual design centre, and worth naming explicitly
rather than assuming the name implies the right tool.

## What actually exists today, so costs below are relative to something real

The shared vault at `$AGENT_MEMORY_VAULT` (`agent/memory-conventions.md`,
read 2026-08-23):

- **One fact per markdown note** in `agent/facts/`, semantic kebab-slug
  filenames (identity = concept; update in place, never duplicate).
  Frontmatter: `type`, `title`, `description`, `created`/`updated`, `source`.
- **69 fact files today.**
- `agent/index.md` (75 lines) is **the only file loaded at session start** —
  capped by design; individual facts load on demand when relevant, not
  eagerly.
- Every write also appends to `agent/log.md`.
- Cross-fact relationships already exist, informally: fact bodies end with
  `Related: [[other-fact]]` wikilinks (see `agent/facts/
  loop-mechanism-layered-design.md`, four outbound links). Nothing computes
  over these edges today — no traversal, no ranking, no clustering. They are
  read by a human or agent opening the file and following a link by eye.
- Access is direct file I/O; the vault is git-tracked and iCloud-synced.
  Auditable and hand-editable by construction.

This is the actual read/write pattern the three candidates below get
compared against — not generic properties of "markdown," "vectors," or
"graphs."

## The three candidates

### 1. Same OKF format the shared vault already uses

**Costs to write:** cheapest of the three — the convention already exists,
every agent already knows how to produce it (this session's own system
prompt carries the write instructions verbatim), and no new infrastructure
is needed. A per-agent tier in this format is markdown files in a
per-agent-scoped directory.

**What it makes cheap:** exactly what the shared vault already makes
cheap — direct lookup by slug, `grep`/`glob` text search, human review and
git history, hand-editing. Nothing more.

**What it forecloses:** any query shaped like "what relates to X, two hops
out" or "what are the dense clusters in this agent's memory" without
building a traversal layer on top of flat files and wikilinks that nothing
currently parses programmatically. It also inherits the shared vault's own
observed scaling behaviour: 69 facts already required capping `index.md` to
keep it loadable at session start (`CLAUDE.md`: "the index is capped; facts
load on demand"). A per-agent tier that accrues memory turn-by-turn, rather
than only on explicit "remember this" moments, will cross that cap sooner
than the shared vault did — the format's write discipline (one fact per
note, editorial judgement about what's worth keeping) is what has kept the
shared vault small enough to eagerly load; a higher-frequency writer would
need the same discipline enforced somehow, or the index stops being
cheap to load.

### 2. Vectors (pgvector / Qdrant)

**Costs to write:** heavier than a markdown file — an embedding step (a
model call, local or hosted) at write time, plus a running database service
(Postgres+pgvector, or a Qdrant instance) that the shared vault has no
equivalent of today. New infrastructure, not just a new directory.

**What it makes cheap:** semantic similarity search — "find memories similar
in meaning to X" — which is a query shape the current system has **zero**
of. Every read pattern observed above is exact (slug lookup) or relational
(follow a named wikilink), never fuzzy-semantic. This would be new
capability, not a faster version of an existing one.

**What it forecloses:** the properties that make the shared vault auditable
today — a fact is a diffable, git-trackable, human-readable text file; a
vector is none of those by itself (a note's text would still need to live
somewhere, with the vector as a derived index over it, not a replacement for
it). It also does not give relationship/traversal queries — similarity is a
different question than "connected to," and the wikilink graph the shared
vault already has informally would need to be re-derived or maintained
separately alongside the embeddings.

### 3. Graphify

**Costs to write:** heaviest of the three, and not sized for this workload.
Graphify's design centre is a periodic (full or incremental) reindex pass
over a codebase-shaped corpus via tree-sitter parsing — not a per-fact
append as memory accrues one small note at a time. Fitting it here means
either running an indexing pass over an agent's own accumulated markdown
memories (treating them as "source" to be re-parsed) or adopting its output
graph (`graph.json`, optionally Neo4j/FalkorDB) as the live store and
writing to *that* representation directly, which is not really "using
Graphify" so much as "adopting a graph database and Graphify's schema."

**What it makes cheap:** real structural graph queries that neither other
candidate offers today — tagged, explained edges (`EXTRACTED` vs.
`INFERRED`, so a query can distinguish an asserted relationship from an
inferred one), Leiden community detection, "god node" and
surprising-connection reports. This is the only candidate of the three that
gives graph analytics rather than a graph shape with no computation over it.

**What it forecloses:** the vault's plain-file portability — a live
persistence backend (Neo4j/FalkorDB) is optional in Graphify itself but
necessary here for anything beyond a regenerated-per-run JSON blob, which is
a different consistency model than the shared vault's git-tracked,
incrementally hand-edited files. It also forecloses treating memory writes
as fine-grained, judged, one-fact-at-a-time edits (invariant to the shared
vault's discipline) in favor of periodic reindex passes over accumulated
material.

## Comparison, side by side

| | Same OKF format | Vectors (pgvector/Qdrant) | Graphify |
|---|---|---|---|
| New infra required | None — reuses vault convention | Yes — embedding step + vector DB service | Yes — parser pipeline, optional Neo4j/FalkorDB for persistence |
| Write shape | One small note per fact, as today | Note + embedding at write time | Periodic reindex/parse pass, not per-fact append |
| Query it adds that nothing has today | None — same lookup/relational reads | Semantic similarity search | Structural graph analytics: communities, explained edges, hub detection |
| Human-readable / git-diffable | Yes, by construction | Only if the source text is stored separately; the vector itself is not | Only the JSON export; a Neo4j/FalkorDB-backed graph is not diffable as text |
| Fit to this estate's actual write pattern (turn-by-turn, judged, one fact at a time) | Native fit — it's what the pattern already is | Workable, but adds a step to every write | Poor fit — designed for reindexing existing material, not incremental fact capture |

## Can per-agent and shared memory share one format without contaminating the shared vault's discipline?

**Format, yes for OKF; not really a meaningful question for the other two,**
because vectors and Graphify don't write markdown facts at all — they would
sit alongside the vault as a derived index (built *from* vault-shaped text)
rather than as a shared format the vault itself adopts.

The concrete risk, if the OKF format is reused: the shared vault's
discipline is one *judged* fact per note — an agent (or Jon) deciding
something is worth a permanent entry, not an automatic capture of everything
observed. A per-agent tier that writes far more frequently (every session,
not every "remember this") and shares the *same* `agent/facts/` directory
and the *same* `index.md` that's loaded at session start would flood both:
the index would stop being cheap to load eagerly (see the capping point
above), and the shared vault's editorial signal — a human can currently
trust that every fact there was judged worth keeping — would be diluted by
higher-volume, lower-judgement per-agent entries mixed into the same
namespace.

**That risk is avoidable without abandoning the format**: a per-agent tier
in the *same OKF conventions* (frontmatter, one-fact-per-note, wikilinks) but
in its **own directory**, scoped per agent and never merged into
`agent/facts/` or read by the shared `index.md`'s session-start load, keeps
the shared vault's discipline untouched. That is not a migration — the
shared vault's own files and read path are unchanged.

## The migration flag this task specifically asks for

**Any design that requires per-agent facts to live inside the same
`agent/facts/` directory as the shared vault, or that changes what
`agent/index.md` loads at session start to accommodate per-agent scoping, is
a migration of the shared vault's conventions — not a detail of the
per-agent design — and must be proposed as one explicitly**, separately from
whichever storage format is chosen. Nothing in this document proposes that;
the OKF option above is written against a **separate** directory specifically
to avoid triggering this flag. If a future design *does* want per-agent and
shared memory merged into one namespace (e.g. so a single graph view can
render both), say so as a migration proposal against the shared vault's own
conventions, with its own review — not as a side effect of picking a
per-agent storage format.

## What this document is not

Not a recommendation. Not a design. No storage code is proposed or written
here or in this PR. The next step is Jon's, per #116 and per
`docs/memoryvariants/README.md`'s own precedent for how this estate treats
gated/undecided work: evidence lands, the decision does not get made for
him.
