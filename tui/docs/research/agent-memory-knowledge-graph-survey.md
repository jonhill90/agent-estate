# Survey: prior art on agent memory, knowledge graphs, and OKF+RAG (agent-tui#116)

`2026-08-23`. Answers Jon's question — are there more logic-gems out there in
this space he hasn't shared — scoped tightly to what #116 actually asked for:
memory formats/stores, knowledge graphs for agents, OKF-adjacent tooling, and
the OKF+RAG router pattern. **This is a survey, not a build. Nothing here is
a recommendation to adopt anything, and no logic-gem skill was built** — Jon
named that as a possible future thing and said explicitly not to let it
distract; it is out of scope here on purpose.

## Method

Every candidate below was checked against a primary source, not a summary:
`gh api repos/<owner>/<repo>` for license SPDX id, archived status, star
count, and last push date (all timestamps below are live pulls, not
remembered); the project's own README/docs fetched directly for the
mechanism claims. **License is reported first for every candidate, before
maintenance or the idea, per the brief.** A candidate with no discoverable
open-source license is study-only — not weighed against the licensed ones,
not scored, not included in the shortlist. None of the candidates below hit
that case; where a look-alike did (see `graphify.net` below), it's named and
set aside rather than silently dropped.

**Reading a repo has no supply-chain risk; importing it has all of it** —
every "worth taking" verdict below is judged twice: once for the idea in
isolation, once for whether it's worth the actual dependency. Most of what's
worth stealing here is a *pattern*, not a package.

## Already known, not re-derived here

Two things Jon already found stand as-is:

- **`microsoft/hve-core`** (cloned at `/Users/jon/source/repos/skills-research/hve-core`,
  MIT). Its `evals/baseline-equivalence` suite is a working two-arm ablation
  harness — identical stimuli run against an empty baseline and a
  materialized-agent environment, compared by an LLM judge constrained to a
  curated allow-list of permitted divergences, with signed mean score / CI /
  win-rate as the pass criterion. Directly reusable *pattern* (not code) for
  a future "does adding per-agent memory change behavior beyond what it's
  supposed to" eval, whenever memory work reaches that stage. The repo's own
  README calls itself "a source of patterns and learning, not a stable
  platform, foundation, or production dependency" — take the shape, not a
  dependency on it.
- **The OKF+RAG router argument** (vault fact `rag-is-coming-design-for-it-now`,
  sourced from Qasimali's Medium piece, opinion/architecture, not measured
  evidence): OKF as the curated spine, RAG as the uncurated reach, a thin
  router deciding per query, both behind one interface. Not re-argued here —
  see "corroboration from a working system," below, for independent evidence
  the pattern shows up in real code, not just the essay it was first read
  from.

## Answering #116's own first ask: what does "graphify" actually refer to

Two different things share the name, and #116 explicitly asked not to let an
unidentified option sit in a comparison looking like a peer of the real
ones:

- **`safishamsi/graphify`** — Apache-2.0, **109,754 stars**, 10,671 forks,
  created 2026-04-03, actively developed (commits same week as this survey,
  detailed changelog, real issue-numbered PRs). "Turn any codebase, with its
  docs, SQL schemas, configs, and PDFs, into a queryable knowledge graph" —
  a `/graphify` skill for Claude Code, Cursor, Codex, Gemini CLI. Local
  deterministic AST parsing, **no vector store at all** — every edge in the
  graph is explained by a parse rule, not a similarity score. This is the
  real candidate; the star count is unusually high but the commit history,
  issue numbering, and change detail are consistent with genuine, fast
  organic growth, not a star-farming artifact — checked, not assumed.
- **`graphify.net`** — a commercial product site ("Knowledge Graph Skill for
  AI Coding Assistants"). Could not confirm a license or an open-source
  repository for it (WebFetch returned 403; no GitHub link surfaced). **No
  license found → study-only, full stop** — it is not scored, not compared,
  not part of the shortlist, per the brief's own instruction. Flagged only
  so it is not confused with the real `safishamsi/graphify` above if the
  name resurfaces.

So "graphify," as a real, evaluable candidate for #116's storage decision, is
**a codebase/notes-to-graph EXTRACTION tool, not a memory STORE** — it turns
existing files into a graph on demand; it does not itself hold a per-agent
agent's ongoing memory the way OKF or a vector store would. That's a
category difference #116's own three-way framing doesn't surface, and it
matters for the decision: "graphify" answers "how do we get a graph out of
what we already have," not "where do we write what an agent learns."

## Candidates, license first

| Candidate | License | Maintained | The idea | Worth without the dependency? |
|---|---|---|---|---|
| `getzep/graphiti` | **Apache-2.0** | Yes — 951 commits, active this week, standalone from Zep's cloud pivot | Bi-temporal fact validity: every fact has a `[valid_from, valid_until)` window; a new fact **invalidates** the old one programmatically (not an LLM judgment call) while keeping the superseded fact queryable as history | **Yes, the concept, not the library.** A vault fact that gets superseded today is hand-edited or silently overwritten; recording *when* a fact stopped being true, not just what replaced it, is a real, cheap-to-borrow idea for `memory-conventions` without pulling in a graph-DB dependency. |
| `mem0ai/mem0` | **Apache-2.0** | Yes — 63,880 stars, pushed same day as this survey | Multi-signal retrieval (semantic + BM25 keyword + entity linking) over a single-pass, ADD-only extraction (April 2026 redesign: one LLM call, no UPDATE/DELETE loop) | **Partially.** The ADD-only-then-consolidate-separately split is a real simplification worth noting for a future per-agent write path; the multi-signal retrieval blend is standard practice elsewhere too, not distinctive enough on its own to justify the dependency. |
| `topoteretes/cognee` | **Apache-2.0** | Yes — 30,199 stars, pushed same day | "Auto-routing" — one `remember`/query call picks graph vs. vector automatically per query, rather than the caller choosing. Fully local by default (SQLite + LanceDB + Kuzu, no cloud service required) | **Yes, as corroboration, not as code.** Independent evidence — from a different codebase than the Medium essay — that the OKF+RAG router idea is a real, shipped pattern, not a one-author opinion. The router *shape* is worth stealing; the multi-backend hybrid engine underneath it is a large dependency for what #116 is actually asking (a per-agent storage format decision, not a retrieval engine). |
| `letta-ai/letta` | **Apache-2.0** | Yes — 24,365 stars, pushed same day | Memory-first agent design: the agent manages its *own* context window as addressable memory blocks it can edit, rather than memory being an external system bolted on | Read-only interest for now — this is closer to "how should the agent's own working context be organized" than "where does per-agent memory live at rest," which is the actual #116 question. Worth a second look if per-agent memory design ever needs to answer "can the agent edit its own memory mid-task," not now. |
| `basicmachines-co/basic-memory` | **AGPL-3.0** | Yes — 3,730 stars, active | Markdown + YAML frontmatter + wikilinks as the knowledge base, served to any MCP client, with a graph view on top | **No — already tried and retired.** `docs/memory.md` in agent-dotfiles already says so directly: *"Basic Memory is retired because its MCP-only failure mode removed access with no fallback."* Listed here for completeness and because its shape (markdown+wikilinks+MCP+graph-view) is the closest published analog to what #116 is asking for — but the AGPL-3.0 license alone would need a real review before any dependency, and the estate's own prior experience with it already answers "should we adopt this" without needing to re-run the evaluation. |
| `fellowgeek/mcp-memory` | **MIT** | Yes — pushed within the week, small (193 stars, 1 open issue) | **Dual-write, not a router**: every write lands in an OKF v0.2 markdown file (canonical, auditable, git-diffable) *and* a SQLite FTS5 index (sub-20ms lookups), atomically, so reads default to the fast index while the source of truth stays the markdown | **Yes, the pattern.** This is a genuinely different mechanism from the router idea (which picks OKF-or-RAG per query) — here, every fact is indexed in *both* places on write, all the time. Directly relevant if per-agent memory ends up OKF-shaped: an FTS index alongside the markdown, kept in lockstep on write, costs little and answers "fast keyword search over a growing per-agent OKF bundle" without needing a vector store or a router decision at all. Small project, low complexity to re-derive without the dependency. |
| `zycaskevin/Vault-Agent-Memory` | **Apache-2.0** | Yes — pushed same day, small (47 stars) | **Candidate-first import**: an incoming OKF bundle from elsewhere is never trusted directly — it sits in a review queue and passes privacy/duplicate/metadata/quality gates before promotion into the local vault. OKF is treated as an *exchange* format, not the local system of record. | **Yes, directly, no dependency needed.** This is a policy/workflow idea, not code — cheap to reimplement as our own gate. Relevant the moment per-agent memories (or any external OKF bundle) are ever merged into the shared vault: the shared vault's one-fact-per-note discipline is exactly the kind of thing an untrusted import could quietly violate, and this pattern is the guard against that. |
| `EliaszDev/hermes-okf` | **MIT** | Thin — 31 stars, last push 2026-06-25, 1 open issue | An early, minimal OKF-based memory system for a specific agent ecosystem (Hermes) — mostly interesting as evidence OKF is being independently adopted outside Google's own repo, not for a novel mechanism | **No** — smallest, least-maintained candidate found; nothing here isn't already better demonstrated by `mcp-memory` or `Vault-Agent-Memory` above. |
| `getzep/zep` (Community Edition) | Apache-2.0 (unmaintained) | **No — deprecated April 2025**, moved to a `legacy/` folder, "no further updates or active support"; Zep's own open-source effort now concentrates entirely on Graphiti | N/A — named only to correct the record: several 2026 blog posts still describe "Zep" as if the self-hosted server were current. If it resurfaces as a candidate, the real thing to evaluate today is Graphiti, not this. | N/A |
| `GoogleCloudPlatform/knowledge-catalog` (the `okf/` subtree) | **Apache-2.0** | Yes — pushed same day; this is the OKF spec itself, already cloned locally at `google-knowledge-catalog/okf` | Not a new find — this is the specification the shared vault already implements. Included only to confirm, directly from source (`okf/SPEC.md`), that the license under which OKF itself is published is Apache-2.0 and imposes no constraint on the vault's own use of the format. | Already adopted; nothing to weigh. |

## Corroboration from a working system, not just an essay

The Medium piece behind `rag-is-coming-design-for-it-now` is opinion, not
measured evidence, and the vault fact already says so. Cognee's own
"auto-routing" (pick graph vs. vector per query, expose one interface)
independently reproduces the same shape in shipped, Apache-2.0 code, from a
team with no connection to that essay. That doesn't make the router idea
correct — it makes it **a pattern two independent parties converged on**,
which is stronger evidence than one author's argument alone, still short of
this estate's own measurement.

## Shortlist — ideas worth stealing, evidence beside each

Ranked by directness to #116's actual question, not by project popularity:

1. **Candidate-first import for any OKF bundle merged into the shared vault**
   (`Vault-Agent-Memory`, Apache-2.0, but the idea needs none of its code).
   Cheapest to adopt, most directly protects something the estate already
   depends on (the vault's one-fact-per-note discipline), and is a workflow
   change, not a storage decision — doesn't wait on #116 at all.
2. **Dual-write OKF-plus-FTS-index, not a router** (`fellowgeek/mcp-memory`,
   MIT). If per-agent memory ends up OKF-shaped (one of the three named
   candidates), this is the cheapest way to get fast search over it without
   a vector store or a routing decision — small enough to reimplement
   directly rather than depend on.
3. **Bi-temporal fact validity** (`getzep/graphiti`, Apache-2.0, concept
   only). A superseded vault fact today just gets overwritten; recording
   *when* it stopped being true, not only what replaced it, is a real gap
   worth closing independent of whatever #116 decides.
4. **The router pattern itself, now corroborated twice** (Cognee,
   independently, plus the original essay). Not actionable until the RAG
   half of "our own things plus OKF plus RAG" actually gets built (explicitly
   not now, per `rag-is-coming-design-for-it-now`) — but worth keeping as
   the leading design once it is.
5. **"graphify" is settled as a category, not a peer candidate** — it is an
   extraction tool (turns files into a graph on demand), not a memory store,
   so #116's three-way comparison should treat it as answering a different
   question than "where does per-agent memory live."

## What this does not do

- Does not recommend adopting anything. Every "worth taking" verdict above
  is about the idea, explicitly separated from the dependency.
- Does not decide #116's storage format. That remains Jon's.
- Does not build a logic-gem skill. Named as a possible future thing, told
  explicitly not to be a distraction — not started here.
- Does not re-run or re-argue the OKF+RAG router case; that stands as
  recorded in `rag-is-coming-design-for-it-now`.
