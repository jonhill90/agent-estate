# Astra one-shot — Push 4.5: the associative layer (tags + links)

ONE-SHOT BUILD: branches + PRs only, never merge. Smallest reading on
ambiguity, noted in PR body. Read first: (1) this file; (2) the MEASURED
BASELINE in `next-plan-inputs.md` §"Push 4.5 baseline" (Part B gate
result: 0/119 facts tagged, 5 distinct tag values vault-wide, zero
topical tags, `vault.go` has no tag handling); (3) `inmaps-spec.md` §3
tag standard + §4 MOC-birth rule; (4) `master-execution-plan.md`
§Push 4.5. Law weights: hard = binding, preference = honour (same rule
as push 4's brief).

FIRST ACTION: verify state vs origin/main (estate + vault note counts +
tag census: `grep -rh '^tags:' <vault>/01\ -\ Notes -R | sort | uniq -c`)
and record in your report.

Repo scope: agent-estate + the vault (through the tool). NOT dotfiles,
NOT skills (post-#304 state is frozen for this push), no Hill90, no
Second Brain. VAULT EXCLUSIVITY: the Director guarantees no lane writes
the vault during your window (it controls dispatch); at your start,
record `find "$AGENT_MEMORY_VAULT" -mmin -10 -name '*.md'` output as
the exclusivity snapshot — if it shows active writes, STOP vault work,
do code-only, and report.

## C1 — Wire tags into retrieval FIRST (the substrate)

`internal/knowledge/vault.go` must parse note `tags:` frontmatter and
carry them into the index (search/filter/boost); `estate knowledge query`
gains tag filtering (e.g. `--tag azure` or tag-aware ranking — smallest
coherent design, documented). Acceptance is END-TO-END, not fixture-only (the original gate failed
precisely while fixtures would have passed): tag one REAL vault note
through the tool, rebuild a PRIVATE index, retrieve it BY TAG via
`estate knowledge query`. Plus fixture tests + mutation check (break
the tag parse, watch tests fail). THIS LANDS BEFORE C2 —
tagging before wiring would repeat the decorative-tags failure Jon
measured in his Second Brain.

## C2 — Associative tagging pass (the 2,757 notes)

- Vocabulary first: extend `99 - Meta` tag vocabulary with the topical
  set the corpus actually needs — derive candidates from the notes
  themselves (frequent subjects: azure, estate, dispatch, tmux, memory,
  skills, corpus, obsidian, testing, review...), keep it SMALL (target
  ~30-60 values), governed file updated in the same change. Flat
  lowercase only (spec §3); kind stays frontmatter, never a tag.
- Tag EVERY note under `01p - Parameters/` and `01f - Facts/` (count
  verified at YOUR start — 2,757 as of this writing, will drift) through the
  TOOL (schema/vocabulary validated), associatively per Jon's golden
  rule ("what comes to mind", not "where it belongs") — 2-5 topical tags
  per note, batched W1-style (backups, validator per batch, halt on
  fail). Jon's rule: routine tagging needs NO approval (hard parameter).
- Regenerability — A CODE CHANGE YOU MUST BUILD, not a property to
  test: `internal/candidates/inmaps.go`'s `noteBytes()` REBUILDS the
  whole frontmatter from the proposal struct (measured — no
  read-merge-write exists), so today regeneration DESTROYS on-disk tags.
  Change `noteBytes` (and any sibling writer) to read-merge-write:
  tool-owned fields regenerate, associative fields (topical tags,
  relations section) are preserved. This lands WITH OR BEFORE the
  tagging pass or the entire pass evaporates on next regeneration. Test:
  tag a note, regenerate, tags survive; mutation: revert the merge,
  watch it fail.

## C3 — Relation proposer (links)

Corpus twin: #1095 (relation model, zero rows). Build the proposer as
estate tooling: candidate relations from (a) shared sources, (b) shared
rare tags, (c) explicit textual reference (one note's body naming
another's title/id), (d) contradiction candidates (same subject,
opposing statements) — the ONE judgement-based input: a contradiction
proposal must carry BOTH statements verbatim (clean text only, never
text_raw) so the reviewer sees the actual claim, and contradicts is
NEVER auto-accepted, not even in the starter set. Output: PROPOSALS
through the existing review pipeline (same accept/reject flow as candidates — never auto-committed
en masse). Accepted relations written by the tool as typed markdown
links in a `## Relations` body section (Jon's INMPARA relation
vocabulary: relates_to, requires, enables, supersedes, contradicts...).
Seed run: propose for the 119 facts + top-cited parameters; accepting a
bounded starter set (~50-100 relations) is in scope — SELECTION ORDER:
highest mechanical confidence first (shared source, then explicit
textual reference, then shared rare tag); contradicts excluded from
auto-ordering entirely; the full 2,638
corpus is NOT (propose-at-scale without review is the anti-pattern).

## C4 — MOC follow-through

With real topical tag density, the ≥8-unhubbed-notes threshold fires:
run the proposer, accept the obviously-earned topic MOCs (azure, estate,
memory...), let them generate into `02 - MOCs` beside the area hubs.
Gate check: "show me the azure parameters" answers in BOTH Obsidian
(tag pane, reads the vault directly) and `estate knowledge query` run
against a PRIVATE index build (ESTATE_KNOWLEDGE_INDEX override) — the
protected shared index is NEVER regenerated (hard rule); demonstrating
via private index is the sanctioned path, say so in the report.

## Constraints

Wind down cleanly at ~20% quota (push, PR, honest report with UNRUN).
Tool-only vault writes; single vault writer at a time; missing metadata
never invented; every claim carries command output; W1 batch discipline
for all bulk vault operations; the protected shared index is not
regenerated.

## Definition of done

Estate PR(s) with green checks (C1 wiring, C3 proposer, generator
merge-tags change); vault operations delivered as
`run/astra-push45-report.md` with per-batch evidence, tag census
before/after, relation proposals+acceptances listed, and the C4 gate
question's actual output from both surfaces.
