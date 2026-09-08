# Knowledge-structure research synthesis — 2026-09-06

Four parallel researchers, primary sources only: (1) PKM traditions (PARA,
Zettelkasten, LYT/MOCs, Johnny Decimal, Matuschak — live fetches of
fortelabs.com, luhmann.surge.sh, zettelkasten.de, linkingyourthinking.com,
johnnydecimal.com, notes.andymatuschak.org); (2) agent-memory practice
(Anthropic memory tool + Claude Code memory docs, basic-memory, Letta, mem0,
pi-llm-wiki, Karpathy's gist fetched raw, llms.txt, agents.md, Anthropic
context-engineering); (3) local prior art (okf-skill, pi-llm-wiki templates,
coleam00 second-brain-audit, Microsoft-Agent-Skills, hve-core, Jon's inmpara
repo + his hand-written Second Brain Claude guide); (4) OKF v0.2 SPEC.md
deep-read with section citations. Full agent reports are in this session's
task outputs; this file is the decision-ready distillation.

## Q1 — Root layout

- Traditions split on what folders ENCODE: purpose/actionability (PARA:
  Projects/Areas/Resources/Archives; LYT-ACE: Atlas/Calendar/Efforts),
  nothing (Zettelkasten, Matuschak — links only), or a full pre-designed
  taxonomy (Johnny Decimal). Every tradition that allows folders caps them
  hard (PARA ~2 levels, ACE 3 roots, JD exactly 3 levels).
- Agent systems that keep folders keep them FEW, FLAT, ROLE-based
  (Karpathy/llm-wiki: sources/entities/concepts/analyses; Claude Code:
  flat memory dir + index) — never deep topic trees. Half drop folders
  entirely and navigate by search/relations (basic-memory, Letta, mem0).
- OKF is explicitly silent on folder taxonomy (§3) — producer's choice.
- INMPARA is orthodox PARA×LYT (purpose folders + link-structured notes).
  Verdict: the numbered INMPARA spine is defensible against every source;
  the binding rule extracted is **folders encode purpose, never topic** —
  topics live in tags/links/MOCs. No topic folders, ever.

## Q2 — Note subtypes (parameters, thoughts, questions, directives)

- Every agent system puts TYPE in metadata, not folders: Claude Code
  (`type: user|feedback|project|reference`), basic-memory (open observation
  categories `[decision] [fact] [preference] [question] [risk] [idea]`),
  llm-wiki (8 types incl. the skill/case pair: generalization vs one run).
- Jon's own INMPARA standards already define observation categories
  (`[technical-finding] [insight] [decision] [requirement] [issue] [idea]`)
  and typed relations (`relates_to, enables, requires, implements…`).
- Jon's corpus taxonomy (parameter/question/thought/directive) is the same
  idea, already in production in the corpus ledger.
- Verdict: type lives in frontmatter (+ typed observations inside notes);
  letter-prefix subfolders (01f - Facts) are optional Obsidian *browsing
  views*, added only when a type hits critical mass ("structure must be
  earned" — Milo; "never force structure prematurely" — Jon's own README).

## Q3 — Pure-ID filenames

- Luhmann's rationale, primary source: the ID's only job is an address
  that NEVER changes so links never break; zettelkasten.de names the
  timestamp (`202006110955`) as the modern form and calls title-named
  files the one NOT-recommended scheme.
- Agent systems consistently decouple stable machine identity from display
  title (basic-memory permalink; OKF §2: concept ID = file path, "display
  title never determines identity"; llm-wiki `SRC-` IDs "keep provenance
  stable even if titles change"). Under OKF, RENAMING A FILE CHANGES ITS
  IDENTITY — so a never-renamed pure-ID filename is the strongest possible
  OKF conformance.
- The Matuschak counter (title-as-API) is served by: `title`+`description`
  in frontmatter, the index's one-line-per-entry hook, and markdown link
  labels (OKF links carry text anyway). With tool-only writes (Jon's rule),
  the tool always renders `id — title`.
- Jon's own Second Brain already practices the reconciliation: Notes =
  pure timestamp IDs; MOCs/Projects = title-named (they ARE their titles,
  and are unique). Verdict: pure `yyyymmddhhmmss` IDs for notes; title
  names for MOCs and Projects; `SRC-`-style dated IDs already in use for
  catalogue sources. Jon's instinct is vindicated by both camps' sources.

## Q4 — Frontmatter

- Field inventory synthesis: core = `type` (OKF's only required key),
  `title`, `description` (LOAD-BEARING: becomes the index hook line and
  the pre-open preview — llm-wiki + Claude Code + llms.txt all converge),
  `tags`, `created`/`updated` (ISO 8601 WITH UTC offset — OKF tightened
  this 2026-08-20), `id`/permalink. Trust/provenance from OKF: `sources`
  (with `author`/`usage_count`/`last_modified` credibility signals),
  `generated {by, at}`, `verified [{by, at}]` (human:jon ⇒ human-reviewed
  trust tier, derived free), `status: draft|stable|deprecated`
  (`draft` = inbox semantic), `stale_after`. Worth stealing from llm-wiki:
  `aliases`, `recall_triggers`. From hve-core: JSON-Schema-enforced
  frontmatter (`additionalProperties` controlled) — pairs exactly with
  Jon's tool-only-writes rule.
- Two hard rules found: never invent missing metadata (llm-wiki: "missing
  metadata does not become invented metadata"); auto-stamp `updated` on
  every tool write (Claude Code practice).
- Verdict: tiered schema — small required core, OKF trust fields added by
  the tool when known, schema-validated at write time.

## Q5 — MOCs: do agents use them?

- Yes — universally, but transformed: every agent system has an
  entry-point page with one-line-per-item pointers (Karpathy's index.md,
  llms.txt, Claude Code MEMORY.md, llm-wiki meta/index.md). The agent
  versions differ from human MOCs in three ways: CAPPED (MEMORY.md 200
  lines, Letta block limits, llms.txt token economics), GENERATED or
  gate-enforced rather than hand-curated (llm-wiki regenerates
  deterministically from an event log; Claude Code errors on overflow),
  and carrying an explicit READ PROTOCOL ("view memory before doing
  anything else" — Anthropic's own injected prompt).
- No controlled study isolating MOC-present vs MOC-absent for agent
  performance exists (honestly searched, not found).
- Emergence: every human tradition except JD says a MOC is born AFTER a
  cluster exists; Milo's trigger is the felt "mental squeeze point."
  Agents can't feel squeeze — so the emergence trigger must be mechanized:
  e.g. "N notes share a tag with no hub linking them → PROPOSE a MOC"
  through the existing review pipeline. Jon's hand-MOC staleness (frozen
  14 months, measured) is exactly what generation fixes.
- Verdict: MOCs yes, as generated/threshold-born projections with optional
  curated overview prose preserved across regenerations.

## Cross-cutting

- The convergent shape across ALL sources — capped index of one-line
  pointers + one-concept-per-file + typed frontmatter + append-only log —
  is exactly what `agent/index.md` + `facts/` + `log.md` already are. The
  vault's bones are right; the contested part was only naming and the top
  layer.
- The read-protocol finding bears directly on the #1 open gap
  (consultation reliability, 2-of-3): Anthropic's memory tool solves it by
  INJECTING "always view memory first" into every session — an enforced
  entry point, not a hoped-for habit. Same pattern the estate's dispatch
  grounding already uses; extend it.
