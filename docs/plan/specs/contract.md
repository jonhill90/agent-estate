# Source-record contract (Lane A, Deliverable 2)

Unblocks Lane B (catalogue/source-reference API) and Lane C (candidates,
`main.go` wiring, integration tests). This is a contract, not an
implementation — it names the fields a source record carries, the tag
vocabulary it draws from, and the rule for where a piece of durable content
canonically lives. It does not decide storage format (markdown vs
sqlite/duckdb — open per agent-estate#1019) and does not restructure
anything already built under `agent/facts/`, `agent/corpus/`,
`agent/parameters/`, or `agent/sources/` in the vault.

## 1. Source-record frontmatter fields

A **source record** points at an original; it never copies the original's
body. Every source record — whether it lives under the vault's
`01 - Sources/` (Lane A's area) or in Lane B's catalogue store
(`src/estate/internal/catalogue/`) — carries these fields. Names are the
contract; each store may serialize them as YAML frontmatter, a struct
field, or a column, as fits its own format.

| Field | Required? | Meaning |
|---|---|---|
| `id` | yes | Stable identifier for this source record. Never reused after a record is deleted; a new record for the "same" real-world source after a gap gets a new id, not a resurrected old one (per [[refuse-invented-identity]] — recoverable identity requires positive confirmation, and a source record has no positive-confirmation mechanism today). |
| `locator` | yes | Where the original actually is — a URL, a repo path, a file path, a GitHub `owner/repo`. Must be dereferenceable by something outside this record; a locator that only this record itself resolves is not a locator. |
| `kind` | yes | One value from the `kind` axis in `05 - System/tags.md` (`kind/repo`, `kind/doc`, `kind/transcript`, `kind/decision`, `kind/skill`, `kind/tool`). |
| `provenance` | yes | Who or what produced the *record* (not the original) — an agent id, a harness, a human name. Distinct from `attribution` below. |
| `attribution` | yes | Who or what authored the *original* the record points at. A GitHub repo's `owner`; a document's byline; `unknown` if genuinely unrecoverable — `unknown` means "not offered," never "broken," per this repo's own invariant 6. |
| `authority` | yes | How much weight this source's content should carry against a conflicting claim — free text today (e.g. "canonical", "reference-only", "study-only, not adopted"), not yet a closed enumeration. Mirrors the distinction `docs/orientation` and `agent-dotfiles`'s OKF study already draw between a canonical spec and a frozen/unmaintained mirror of it. |
| `access` | yes | Public, private, or scoped — governs whether this record (or content quoting it) may appear in a public PR body or public docs. Private-content originals (transcripts, extractions) must carry `access: private` and this contract's own standing constraint (never enters Git or PR bodies) binds regardless of what any one record says. |
| `revision` / `hash` | yes | The specific version of the original this record was observed against — a commit SHA, a document hash, a page ETag. Lets a later reader tell whether the original has moved since this record was written. |
| `observed-at` | yes | ISO 8601 timestamp with an explicit UTC offset (per OKF 0.2 §13.1/§13.2 — see `agent-dotfiles/docs/okf-0.2-study-2026-08-23.md`) of when this record was created or last confirmed against the original. |
| `freshness` | recommended | A judgement, distinct from `observed-at`: is this source still expected to be current, or has enough time/change passed that it should be re-checked before being cited as ground truth? Free text or a `stale_after` timestamp (OKF 0.2 additive field) — either is acceptable; absence means "not assessed," not "confirmed fresh." |
| `review-state` | yes | One value from the `lifecycle` axis in `05 - System/tags.md` (`lifecycle/candidate`, `lifecycle/current`, `lifecycle/superseded`, `lifecycle/rejected`). Mirrors `agent/LIFECYCLE.md`'s candidate → accepted → superseded/rejected states, generalized from vault facts to any source record. |
| `derivative-links` | recommended | Pointers *out* from this source record to anything distilled from it (a vault fact, a catalogue entry, a doc section) — never the reverse copy. Keeps the "originals stay authoritative" constraint checkable: a reader can follow a derivative back to what it was drawn from. |

`type` remains the one OKF-required key for any vault-side file carrying
this frontmatter (per `agent/facts/memory-conventions.md` and OKF v0.2 §11);
the fields above are additive to that, not a replacement for it.

## 2. Tag vocabulary reference

Governed at [`05 - System/tags.md`](../../../Library/Mobile%20Documents/iCloud~md~obsidian/Documents/Agent%20Memory/05%20-%20System/tags.md)
in the vault (Agent Memory, `$AGENT_MEMORY_VAULT`) — not duplicated here.
Four axes: `kind`, `lifecycle`, `project`, `topic`. `kind` and `lifecycle`
are closed enumerations (extending either needs a reviewed change, per that
file's own extension rule); `project` and `topic` are open. Lane B and
Lane C: use these axis values verbatim in catalogue/candidate metadata
rather than inventing parallel vocabulary — if a value you need doesn't
exist yet, that is a gap to raise, not a license to add a fifth axis
unilaterally.

## 3. Canonical-destination rule

Where a piece of durable content lives, by what it *is*, not by which tool
happened to produce it:

| This is... | Lives in |
|---|---|
| Repo behavior — how a command works, what a package does, what a test guards | Beside the code: repo docs (`docs/`, `AGENTS.md`/`CLAUDE.md`, package-level comments), never the vault. |
| A project decision — architecture, sequencing, scope for one project | In that project: its repo's `docs/decisions/`, issues, or PRs. The vault's `02 - Projects/<project>.md` page links to it; it does not restate it. |
| A procedure — a repeatable multi-step workflow a human or agent follows | A guide or skill: a `docs/*-workflow.md` doc plus a Claude Code skill that *references* the guide (never copies it) — e.g. this run's `docs/knowledge-workflow.md` + `.claude/skills/knowledge-session/SKILL.md`. |
| A cross-project preference, standing constraint, or fact about the operator that holds regardless of which project is active | Agent Memory (`agent/facts/<slug>.md` in the vault), indexed from `agent/index.md` if it has earned index space. |
| Anything else — a source worth indexing, a candidate not yet reviewed, a per-harness transcript | The relevant surface **links** to it; nothing here re-hosts a copy. A source record (§1) is itself a pointer, never a body. |

The rule of thumb underneath the table: pick the destination a stranger
would look in first for that *kind* of fact, not the destination that was
easiest to write to at the time. Two stores holding independently-editable
copies of the same fact is exactly the drift this contract exists to
prevent — see `docs/knowledge-system.md`'s existing rationale for keeping
the vault, the corpus, and repo docs as separate stores owned by separate
processes, generalized here to cover Lane B's catalogue and Lane C's
candidate store as two more such processes.

## 4. What this does not decide

- Storage backend for source records (markdown/sqlite/duckdb) — open per
  agent-estate#1019 and `agent-tui#116`; both are already flagged not to be
  foreclosed by today's decisions (see `agent/facts/*`'s own
  `[[rag-is-coming-design-for-it-now]]`).
- Lane B's exact catalogue schema — this contract names the fields a source
  record must carry semantically; Lane B's `run/source-api.md` decides how
  its package represents them in Go.
- Lane C's candidate schema beyond the `review-state` values it must map
  to/from `knowledge_candidates.decision` — that mapping is Lane C's to
  specify against this contract, not this contract's to dictate.

## 5. Verification

No code changes in this deliverable; nothing to run beyond confirming the
referenced vault file exists and confirming the tag axes named above match
what `05 - System/tags.md` actually defines (checked by hand while writing
both files in this same session — same author, same sitting).
