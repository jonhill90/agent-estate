# INMAPS — Agent Memory layout and flow spec

Decided by Jon with Fable, 2026-09-06 evening, after four-researcher study
(`run/knowledge-structure-research.md` — primary-source basis for every rule
here). This supersedes the run's earlier 00-05 naming (Sources/Projects/
Shared Memory/Skills and Tools/System). Status: Jon-approved direction;
Astra formalizes into the next execution plan; Lane A may begin the vault
relayout under this spec.

## 1. Layout — the tree spells INMAPS

```
Agent Memory/
├── Start Here.md            entry point; READ PROTOCOL first, then one line per area
├── 00 - Inbox/              intake airlock; everything arrives as status: draft
├── 01 - Notes/              atomic knowledge; pure-ID filenames (202609061745.md)
├── 02 - MOCs/               generated hubs, title-named (Estate.md, Azure.md)
├── 03 - Agents/index.md     ONE pointer file → agent roster in agent-dotfiles
├── 04 - Projects/           knowledge hubs per project, title-named, planned/active/done
├── 05 - Sources/            source records: SRC-YYYY-MM-DD-NNN.md, provenance/freshness
├── 99 - Meta/               tag vocabulary, frontmatter schema, templates, log.md
└── agent/                   TRANSITIONAL ONLY — this directory GOES AWAY (Jon,
                             2026-09-06 ~20:30). Everything in it dissolves into
                             the numbered structure; nothing may live outside it.
```

Rules that produced it (each traces to research or Jon's record):
- Folders encode PURPOSE, never topic. No topic folders, ever. Topics live
  in tags, links, MOCs.
- Numbers = a-z order = the acronym. 99 - Meta last by design (Jon's own
  convention: always at the end).
- No Areas (measured unused 5 years), no Archive (unused; `status:
  deprecated` does that job), no Skills folder, no System folder.
- Inventory-vs-memory test: an index of git artifacts lives IN git (skills
  index → skills repo; agents roster → agent-dotfiles); the vault indexes
  only vault-native content. `03 - Agents/index.md` is a single pointer,
  not a store. `04 - Projects` survives because project pages are memory
  (status, decisions, connections), not inventory.
- Repos instruct, the vault remembers. AGENTS.md/CLAUDE.md content never
  migrates here; accumulated facts in instruction files belong here.

## 2. Naming

- Notes: pure timestamp ID `YYYYMMDDHHMM(SS).md`, NEVER renamed (Luhmann
  stable-address rationale; OKF concept-ID = file path, so a never-renamed
  ID is strongest conformance). Title lives in frontmatter + H1.
- MOCs and Projects: title-named (`Azure.md`, `agent-estate.md`) — unique
  things that ARE their titles. (Jon's own Second Brain split, kept.)
- Sources: `SRC-YYYY-MM-DD-NNN.md` (llm-wiki pattern: provenance stable
  even if titles change).
- Subtype folders (e.g. `01f - Facts/`): created only when a type earns a
  browsing view. Type lives in frontmatter regardless.

## 3. Frontmatter (tool-written, schema-validated — see §5)

Required core: `type`, `title`, `description` (LOAD-BEARING — it becomes
the index line), `tags` (from the governed vocabulary), `id`,
`created`/`updated` (ISO 8601 WITH UTC offset, auto-stamped on write).

Type vocabulary (open, OKF-conformant, ours to extend):
`Fact | Thought | Question | Parameter | Research | MOC | Project | Source`
— promotes Jon's existing corpus item taxonomy.

Trust/provenance (OKF 0.2, added when known, NEVER invented):
- `sources: [{id, resource, title?, author?, usage_count?, last_modified?}]`
- `generated: {by: <actor>, at}` — actor convention `claude/sonnet-5`,
  `human:jon`, `process:<id>`
- `verified: [{by, at}]` — `human:jon` ⇒ human-reviewed trust tier, derived
  free; agent-only ⇒ machine-confirmed; none ⇒ unverified
- `status: draft | stable | deprecated` (draft = Inbox state; deprecated =
  superseded, file and ID never deleted)
- `stale_after: <absolute ISO instant>` where knowledge expires
Optional, stolen deliberately: `aliases`, `recall_triggers` (llm-wiki).

**Tag standard (Jon, 2026-09-06 ~20:15, from his own Second Brain 99 - Meta
templates — the binding prior art):** tags are FLAT lowercase-kebab words,
never namespaced (`kind/doc` is wrong; `doc` is wrong too if it duplicates
frontmatter). Three kinds, per his guide: structural (`note`, `review`,
`clippings`), time (`MM-YYYY`), synaptic/domain (`azure`, `ai`,
`estate` — "tag associatively, not categorically"). A record's KIND is
frontmatter (`type:`/`format:`), NEVER a tag — the tag pane is for
association, not schema. No placeholder text may ever reach a tag or any
frontmatter value (live vault currently shows `N`/`NNN` as tags — a
generator/template leak; find and fix the source). The `99 - Meta`
vocabulary file encodes this.

Hard rules: missing metadata is never fabricated; `updated` auto-stamps on
every tool write; body observations may use Jon's bracketed categories
(`[decision] [insight] [requirement] …`) — same idea as basic-memory's
observation model, already in Jon's INMPARA standards.

## 4. Flows — Jon talks and judges; agents do ALL bookkeeping

- **Intake**: Jon drops a link or says something durable in chat → the
  AGENT registers the source (`05 - Sources`) and/or writes a draft
  candidate into `00 - Inbox` with conversation provenance (the existing
  prompt-capture → corpus → `estate candidates` pipeline). Agents also
  propose things they encounter during tasks — always with source + reason.
  Nothing writes directly to `01 - Notes`; the Inbox is the airlock.
- **Review**: accept/reject/supersede through the existing candidates
  workflow. Jon's "yeah that's right" in chat ⇒ `verified: human:jon`.
  Unreviewed accepted facts carry machine-confirmed tier — usable, honest.
- **MOC birth is mechanized emergence**: when ≥N notes (default 8) share a
  tag with no hub linking them, the tool PROPOSES a MOC through review.
  MOC link sections regenerate on refresh (never stale — fixes the measured
  14-month freeze); a curated overview paragraph survives regeneration.
- **Drift**: source refresh re-hashes originals; a changed source flips
  dependent notes to needs-review. Never silent rewrites.
- **Supersede**: new note replaces old; old keeps ID, gains
  `status: deprecated` + pointer; links never break.
- **Read protocol**: Start Here first, index lines decide what opens next,
  canonical truth is one link further (progressive disclosure tiers:
  entry point → area index/MOC → note → canonical repo/source). Enforced
  by injection into dispatch grounding, not by hope (next plan, item #1).

## 5. Tooling boundary (Jon's rule 17 — no hand-written memory)

All vault writes go through the tool (estate CLI / MCP): schema-validated
frontmatter, vocabulary-checked tags, auto-stamped timestamps, correct
lifecycle transitions. An agent CANNOT hand-write a note file. Links in new
content are standard markdown links (OKF edges; Obsidian renders fine);
existing wikilinks stay readable, not rewritten. Helper scripts in shell or
Python are permitted (Jon 2026-09-06); the estate APP stays Go.

## 6. Migration from today's state

Lane A's merged navigation (Start Here + 00-05 with old names) relayouts to
INMAPS: rename dirs, update links, checksummed backups first, validator
after. `agent/` stays canonical during transition; records migrate to
`01 - Notes` via tool, one at a time, never bulk (per Jon's standing rule).
The two 2026-09-05 seed PDFs and existing catalogue sources become
`05 - Sources` records. **§8 supersedes this section's "one at a time"
for the fact migration specifically — Jon authorized the batch migration
2026-09-06.**

## 7. Deferred, explicitly

Per-agent memory format (reserved to Jon; personas/domain experts later —
`03 - Agents` pointer is the parking spot), knowledge graph views (derived
only, never source of truth), vectors/RAG (design stays compatible: stable
IDs + typed frontmatter chunk cleanly), Second Brain (reference only,
NEVER edited), harness-native memory forcing, bulk historical migration
**of corpus/transcript history — the 119-fact vault migration is NOT
deferred; it is authorized and specified in §8.**

## 7b. agent/ dissolution map (Jon, 2026-09-06 ~20:30 — binding)

The whole `agent/` directory is retired. Disposition of each member:
- `facts/` → DONE (W1, into `01 - Notes`).
- `parameters/` (6 generated view files) → RETIRED. Each corpus item
  (parameter/correction/directive/question/thought — 1,104 live) becomes an
  INDIVIDUAL note in `01 - Notes` with `type:` per kind, corpus item id +
  prompt_id as provenance, `generated: process:vault-view` so regeneration
  can update-in-place as the corpus evolves. At this volume the
  earned-subtype rule triggers immediately: a letter subdir (e.g.
  `01p - Parameters/`, Jon's 01nf pattern) is JUSTIFIED for this type —
  pick the letter scheme at migration time. Corpus stays the raw source of
  truth; the notes are its vault representation, tool-maintained.
- `sources/` → `05 - Sources` records (the karpathy transcript etc.).
- `corpus/` (read-only extraction record) → private state outside the
  vault, or `05 - Sources` if it must stay visible — decide at migration.
- `intent/`, `tools/` → `99 - Meta` (or the repo if they are code).
- `index.md`/`INDEX-CONTRACT.md` → merge into `Start Here.md` + a
  `99 - Meta` contract doc; the cap discipline survives, the file moves.
- `ROUTING.md`/`LIFECYCLE.md`/`log.md`/`pending-links.md` → `99 - Meta`.
- Standing-law loader, validator, `estate vault-view`, knowledge index
  sources and every other consumer must be repointed as part of the same
  work — the P0 lesson generalized: never move content ahead of its
  readers again; each migration batch names its consumers and updates
  them in the same change.

## 8. Fact-migration ID rule (Jon, 2026-09-06 ~18:10 EDT)

Migrate `agent/facts/<slug>.md` → `01 - Notes/<ID>.md` where
**ID = YYYYMMDD from `created:` + 4-digit same-day sequence** (ordered by
full timestamp where present, then alphabetically by slug) — uniform 12
digits, collision-free (67/119 facts are date-only; 15 share 2026-08-23).
Each migrated note gains `aliases: [<old-slug>]` so every existing
`[[slug]]` wikilink still resolves (112 files carry them — none rewritten).
`title`/H1 unchanged; `id:` field added; markdown links in `agent/index.md`
and ROUTING/LIFECYCLE updated to the new paths. Batch-wise with validator
after each batch, never one giant untested move. Old path is MOVED not
copied (no duplicates). Instruction surfaces that name `agent/facts/`
(memory-conventions, global CLAUDE.md) update in the Astra plan.
