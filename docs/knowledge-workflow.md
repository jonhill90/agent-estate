# Knowledge workflow — register, inspect, propose, review, publish, retrieve, refresh

## INMAPS write path (push 2)

Agent Memory uses `00 - Inbox`, `01 - Notes`, `02 - MOCs`, `03 - Agents`,
`04 - Projects`, `05 - Sources`, and `99 - Meta`. Earlier layout/path examples
below describe push 1. Existing legacy vaults remain supported, but an INMAPS
vault writes through Inbox to permanent IDs, never back to slug filenames.

From this repository's root, with explicit isolated corpus/vault paths:

```sh
go run ./src/estate candidates memory -db "$CORPUS_COPY" -vault "$FIXTURE_VAULT" -id "$CANDIDATE_ID" -action propose -proposal "$PROPOSAL_JSON" -apply
go run ./src/estate candidates memory -db "$CORPUS_COPY" -vault "$FIXTURE_VAULT" -id "$CANDIDATE_ID" -action accept -apply
go run ./src/estate candidates memory -db "$CORPUS_COPY" -vault "$FIXTURE_VAULT" -id "$CANDIDATE_ID" -action reject -apply
go run ./src/estate candidates memory -vault "$FIXTURE_VAULT" -action moc-propose -apply
go run ./src/estate candidates memory -vault "$FIXTURE_VAULT" -action moc-accept -id moc-kind-decision.md -reviewer process:reviewer -apply
go run ./src/estate candidates memory -vault "$FIXTURE_VAULT" -action refresh -apply
```

Proposals include type, title, description, learning, governed `tags`, attributed
operator/assistant context, reviewer, destination kind/path and reason. Use
Fact/Thought/Question/Parameter/Research/MOC/Project/Source types. Legacy type
names normalize to Fact only for the new writer. `supersedes` must name the exact
published revision when revising; propose and accept again. The old note retains
its ID, becomes deprecated and points to the replacement. Rejection preserves
history while excluding it from current retrieval. Approval alone is not publication.
`existing_fact_hash` permits explicit adoption of an inspected migrated note.

Notes use YYYYMMDD plus a four-digit daily sequence; source view IDs use
SRC-YYYY-MM-DD-NNN. Existing catalogue hash IDs remain stable citation identities,
with a persisted ViewID for the new filename. Source refresh optionally accepts
`-vault` to mark dependent notes needs_review; acknowledging a source never
silently reaccepts a dependent learning. Use a private working knowledge index.

MOC proposals require eight stable notes sharing a tag without a covering hub.
Acceptance is explicit. Refresh replaces only the generated link section and
preserves curated overview prose. Backups precede canonical file changes.

The live fact migration has separate evidence in the local run directory.
Obsidian aliases alone did not resolve three old bare wikilinks in the measured
pilot; use explicit ID-path Markdown links. Standing-law loading still assumes
legacy paths and is outside this push's authorized standing-law changes. Do not
claim the migration proves compatibility of every existing consumer.


Short canonical usage guide for turning something worth knowing into a
retrievable, cited fact — and back out again when you need to find it.
This is the loop, not the mechanism: for what `estate knowledge` actually
indexes and how it ranks, see `docs/knowledge-system.md`; for exactly where
each candidate/fact lives at each stage, see the vault's own
`agent/LIFECYCLE.md`.

Every command below was run against a private, read-only copy of the
corpus (`sqlite3 -readonly ~/corpus/ledger.sqlite3 ".backup <scratch>"`)
and a scratch vault (`-vault <scratch>`) on 2026-09-06 — never against the
live corpus or the live vault. `<db>` below is `~/corpus/ledger.sqlite3` in
real use (the flag defaults to `internal/corpus.Path()`, so `-db` can
usually be omitted); `-vault` defaults to `$AGENT_MEMORY_VAULT`.

## 1. Register (populate the candidate queue from ingested history)

```
estate candidates -apply
```

Note: there is **no separate `derive` verb** — this is the bare
`candidates` subcommand itself (`estate candidates derive -apply`, seen in
one vault doc before this PR, does not work — `derive` is rejected as an
unrecognised argument). Dry run (omit `-apply`) reports counts without
writing:

```
$ go run ./src/estate candidates -db <scratch-db>
dry run: 4360 codex_provenance rows; knowledge_candidates does not exist yet with 0 rows; would insert 4360 new (pass -apply to write)
```

Idempotent (`UNIQUE(prompt_id)`) — inserts one row per `codex_provenance`
row that resolves to a real prompt, status `candidate`, kind
`unclassified`. Never promotes, never classifies, never removes an
existing row.

**Registering a source** (a repo, a document, a URL — not a
corpus-derived candidate) is Lane B's catalogue work
(`src/estate/internal/catalogue/`), not yet merged as of this PR's first
half. This section will be updated once that command exists; until then,
a source worth indexing gets a source record under the vault's
`01 - Sources/` per `run/contract.md`'s frontmatter contract.

## 2. Inspect

```
estate candidates list [-db <db>]
estate candidates show [-db <db>] <candidate-id>
```

```
$ go run ./src/estate candidates list -db <scratch-db>
showing 50 of 4360 candidates matching this filter (ingestion order)

c3feed7c6622727d35c7f9ae305fb7c3d67db672d1bc97c2c0a0e60e8b255816  [undecided]  /Users/jon/.codex/sessions/2025/11/11/rollout-...jsonl (record 0)
...
```

`show` prints one candidate's cited prompt metadata (harness, session,
source file, content hash) — never the raw prompt text itself; that stays
in the corpus, disclosed only through `estate knowledge get`'s
`disclosure` field (see `docs/knowledge-system.md`).

## 3. Review (decide: promote or discard the candidate itself)

```
estate candidates decide [-db <db>] [-apply] <candidate-id> promote|discard
```

**The candidate id and the verb are positional arguments — not
`-id`/`-action` flags.** (A vault doc had this wrong before this PR.)

```
$ go run ./src/estate candidates decide -db <scratch-db> <id> discard
dry run: would record <id> -> discarded (pass -apply to write; this marks reviewed-and-discarded only, it does not delete or move anything)
```

This is a *review record*, not a publication — it only sets `decision`/
`decided_at` on the existing `knowledge_candidates` row.

## 4. Propose (draft a paraphrased fact against a promoted candidate)

```
estate candidates memory -id <candidate-id> -action propose -proposal <proposal.json> [-apply]
```

`memory` is the one `candidates` subcommand that **does** take `-id`/
`-action` flags. `proposal.json` carries `slug`, `type`
(`user|feedback|project|reference`), `title`, `description`, `learning`,
`operator_context`, `assistant_context`, `reviewer`, `supersedes` — all
required non-empty except `supersedes`; the CLI refuses a proposal missing
attributed operator/assistant context rather than accepting an
unattributed paraphrase:

```
$ go run ./src/estate candidates memory -db <scratch-db> -vault <scratch-vault> \
    -id <id> -action propose -proposal proposal.json -apply
{"proposal":{...},"state":"proposed","revision":"e65f6ead...","citation":"candidate=<id>; prompt=...; harness=codex; ...","changed":true}
```

`revision` is the proposal's own content hash — carried forward into the
published fact so a later `accept` can verify the source evidence hasn't
changed underneath it.

## 5. Publish / link (accept into the vault, or reject)

```
estate candidates memory -id <candidate-id> -action accept|reject [-apply]
```

```
$ go run ./src/estate candidates memory -db <scratch-db> -vault <scratch-vault> -id <id> -action accept -apply
{"proposal":{...},"state":"promoted","revision":"e65f6ead...","published_revision":"e65f6ead...","vault":"<scratch-vault>","citation":"...","changed":true,"file_hash":"8a9e7d0..."}
```

writes `agent/facts/<slug>.md` in the vault, carrying `candidate_id`,
`memory_status: promoted`, `memory_revision`, `supersedes`, and `reviewer`
in its frontmatter, and adds one bullet to `agent/index.md` if the fact
earns index space (see the vault's own `agent/INDEX-CONTRACT.md` for the
160-entry cap and when a bullet is pruned instead). **This is the only
"publish" step** — nothing else copies a fact body anywhere else. Other
surfaces (a root vault page, a repo doc) *link* to `facts/<slug>.md`; they
never restate its body. `-action accept` refuses if the source evidence
(prompt/provenance/content hash) has changed since the proposal was
recorded — review a new proposal in that case, per the tool's own error.

## 6. Retrieve

```
estate knowledge query [--private] [--json] <question>
estate knowledge get   [--private] [--json] <id>
```

```
$ go run ./src/estate knowledge query "how does dispatch work"
index built 8s ago (2026-09-06T20:12:28Z)
21 match(es) for "how does dispatch work" (showing 10, 11 not returned, 52 withheld as private)

[it-d8b0f7e085a8daf0] repo-docs (score 10: how, dispatch, work)
  How does knowledge retrieval work in this repo? — How do I scope a query to one source? (source:<name> scoping)
  docs/knowledge-system.md#how-does-knowledge-retrieval-work-in-this-repo--...
```

`query` returns small, ranked, cited pointers (never full bodies);
`get <id>` is the second half of progressive disclosure — the one item's
full Tier1/Tier2/Tier3 body. Publishable-only by default; `--private`
lifts that filter for anything entitled to see private material (never
paste `--private` output anywhere public). Full state/coverage/
contradiction semantics: `docs/knowledge-system.md`.

## 7. Refresh (regenerate the compiled index)

```
estate knowledge
```

Regenerates `estate knowledge`'s compiled index over GitHub stars, the
vault, the corpus, and Loops-Research — derived and regenerable, never
authoritative. Point `ESTATE_KNOWLEDGE_INDEX` at a private path first if
you're checking a doc change before it ships:

```
export ESTATE_KNOWLEDGE_INDEX=/tmp/my-index.json
go run ./src/estate knowledge
go run ./src/estate knowledge query "<question>"
```

**Refreshing `agent/parameters/*.md` is a separate, currently-broken
path.** Each file under the vault's `agent/parameters/` carries a banner
saying "regenerate with `estate vault-view`" — that command does not exist
anywhere in this repo (verified 2026-09-06: absent from `go run
./src/estate`'s own subcommand list and from a repo-wide grep) or in
`agent-dotfiles`. There is no current command that regenerates those
files; do not invent one and do not hand-edit them in the meantime — see
the vault's own `agent/ROUTING.md` for the same note kept next to the
table row it corrects.
