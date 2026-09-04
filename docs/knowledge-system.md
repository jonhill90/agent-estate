# How does knowledge retrieval work in this repo?

This describes `estate knowledge`'s compiled index as it exists **on
`2df4d3f`** (2026-09-04) — the mechanism, not a decision. Every number below
carries the commit it was measured on; treat an unstamped figure elsewhere as
a lead, not a fact.

**What this is not.** This is not the design for how the operator's knowledge
should ultimately be stored — Markdown, SQLite and DuckDB remain open
(agent-estate#1019, the operator's own decision). This describes only the
current, compiled, regenerable index `estate knowledge` (no subcommand)
already builds today. It carries no corpus text, no vault text, and no
paraphrase of any private directive — mechanism only, never knowledge.

## What does `estate knowledge` actually index?

Five sources, none of them owned or written by this package
(`src/estate/internal/knowledge/knowledge.go`):

- GitHub stars (`gh api user/starred`)
- the Agent Memory vault (`$AGENT_MEMORY_VAULT/agent/facts/*.md`)
- the operator's prompt/parameter corpus (`~/corpus/ledger.sqlite3`'s
  `live_parameters` view only — never the raw `prompts` table)
- `~/source/repos/Personal/Loops-Research`, numbered markdown files
- this repository's own rules: `AGENTS.md`/`CLAUDE.md` and every
  `docs/**/*.md` file, split by heading at `##`/`###` — which is why this very
  file's headings are phrased as questions rather than nouns: each heading is
  its own retrievable unit.

`estate knowledge` (no further arguments) regenerates the compiled index and
writes it to `$ESTATE_KNOWLEDGE_INDEX` (a path you can override — see
"How do I check my own doc changes before shipping them?" below). The index
is **derived and regenerable, never authoritative**: it is safe to delete and
rebuild at any time, and never a second source of truth for any of its five
sources.

## How do I query it?

```
estate knowledge query [--private] [--json] <question>
estate knowledge get   [--private] [--json] <id>
```

`query` returns a small, ranked, cited set of pointers — id, source,
permalink, one-line Tier1 summary — never full bodies. `get <id>` is the
second step of progressive disclosure: it returns that one item's full
Tier1/Tier2/Tier3 body. A caller is expected to `query` first, then `get`
only the ids it actually needs — never the reverse.

Ranking is Okapi BM25 (k1=1.2, b=0.75) over stemmed, stop-word-filtered
question terms against an item's Tier1, Tier2, and both tag classes — Tier1
and tags weighted 3x Tier2 as a BM25 field weight (agent-estate#1043's
measured ratio). There is no minimum-match floor: one rare term matching
once can outrank several common terms matching by coincidence, so an item
needs only one weighted term match (score > 0) to be returned at all
(agent-estate#1054).

## How do I scope a query to one source? (source:<name> scoping)

Prepend a `source:<name>` token to the question — e.g.
`source:repo-docs how does dispatch work`. This is an exact tag filter,
applied **before** term scoring: it narrows the candidate set to items
carrying that exact structural tag, then ranks only within that narrowed set
(agent-estate#1024). It is orthogonal to term search, not a substitute for
it — combine it with real question words, not instead of them. A bare word
with no colon (`repo-docs` alone, with no `source:` prefix) is scored as an
ordinary search term, not treated as a filter.

**This is the single largest quality lever available to a caller, and it is
easy to skip.** Measured on `2df4d3f` (`go run ./cmd/goldenquery`, the
checked-in twelve-question natural-language stratum, agent-estate#1073/#1077):

| | top-3 hit rate | top-10 hit rate |
|---|---|---|
| unscoped (question exactly as written) | 4/12 | 10/12 |
| scoped (`source:repo-docs ` prepended) | 8/12 | 11/12 |

Doubling the top-3 hit rate is the difference between a caller reading the
right section first and reading four wrong ones before it. **If you know
which of the five sources should answer a question — and for anything about
this repo's own rules or mechanism, that source is `repo-docs` — scope the
query.** An unknown `source:` value is refused honestly as `no_match`
(exit 1), never silently ignored and never silently falling through to an
unscoped search over the whole index (agent-estate#1024).

## What happens when nothing (or the wrong thing) answers a question?

Four distinct absences, on purpose never collapsed into one "nothing here"
shape, each with its own exit code from `estate knowledge query`:

| exit | state | means |
|---|---|---|
| 0 | `matched` (or `matched_withheld_majority`) | at least one publishable item scored above zero and was returned |
| 1 | `no_match` | the index was read fine; nothing in it scored above zero against this question — a real, empty answer, not a failure |
| 2 | `index_missing` / `index_unreadable` | the compiled index itself could not be read at all — run `estate knowledge` first |
| 3 | `withheld_private` | something answers this question, but every matching item is private and this call did not ask for private material |

`matched_withheld_majority` deliberately shares exit 0 with `matched`: it
still returned a real, citable public answer, just one where more matching
items were private than were shown. Reading only `$?` cannot tell `matched`
apart from `matched_withheld_majority` — read the printed state word (or the
JSON `state` field) if that distinction matters to your caller
(agent-estate#1052).

Collapsing `no_match` and `withheld_private` into the same code was tried and
rejected: "there is nothing" and "there is something you may not see" are
different answers a script branching on `$?` needs to tell apart
(agent-estate#1037).

## What does the `coverage` field mean, and what should I do about each value?

Every successfully-read `QueryResult` — including a `no_match` one — carries
a `coverage` field: the machine-readable trustworthiness signal for whether
this particular answer can be treated as complete (agent-estate#1058/#1065).
It is a different question from `state`: `state` says whether *this
question* scored anything; `coverage` says whether the *index itself* is
trustworthy right now.

| `coverage.state` | means | what to do |
|---|---|---|
| `complete` | every source read cleanly at build time, nothing withheld by policy | trust the answer |
| `limited` | the query itself withheld eligible material by policy (private items, default mode) | rerun with `--private` if you're entitled to see them |
| `degraded` | a source the index depends on could not be read when it was built | fix the source, regenerate the index |
| `stale` | a source has been *observed* to have changed since the index was built | regenerate the index |
| `unknown` | a source's freshness could not be determined at all — e.g. GitHub stars are read live with no local file to stat against | no fix; a caveat, not an actionable finding |
| `mixed` | more than one of the above applied to the same result | read `coverage.reasons` for each contributing cause |

`degraded` and `limited` are deliberately different words for a reason:
withholding private material in default mode is the boundary working as
intended, not a malfunction; a source failing to read at build time *is* a
malfunction. Conflating them would train a caller to ignore a real failure.
`coverage.reasons` always names which source (if any) and why, whenever
`coverage.state` is not `complete`.

## What does `--json` give me that the prose output doesn't?

`--json` on either `query` or `get` emits the full result as JSON on stdout
instead of prose — transport only, never a second computation
(agent-estate#1068). For `query` that is the complete `QueryResult`
(`state`, `matches`, `total_matched`, `not_returned`, `withheld_private`,
`coverage`, `ranking_basis`, everything the prose form derives its lines
from). For `get` it is `{ok, reason, item}`, with `item` omitted (not a bare
zero value) when `ok` is false. Prose is always derived from this same
structure — never a parallel code path with its own logic. An unrecognised
flag (anything starting with `--` that isn't `--private` or `--json`) is
refused outright, never silently folded into the question text as a search
term.

## What are the two disclosure rules, and how do I ask for private material?

**Publishable-only by default.** A caller that asks for nothing special gets
nothing private, full stop (agent-estate#1033). Every item in the index
carries a `Publishable` bit set once at compile time; the unclassified
default is `false` — an item this package cannot positively classify as
public is private by construction, not by a filter someone remembered to add
at query time (agent-estate#1028).

**`--private` lifts that filter, and says so in the output.** Both `query`
and `get` accept `--private`; when set, the result carries `private_included:
true` and the prose output prints a `*** PRIVATE MODE ***` banner — visibly
marked in the result itself, not only inferable from which flag the caller
happened to pass (agent-estate#1028). `get <id> --private` is required even
for an id you already have written down: a private item's stable id can be
re-fetched later, so filtering it out of `query`'s own matches is not enough
on its own — `get` refuses the direct lookup too, or the filter would be
only cosmetic.

Never paste `--private` output into anything public.

## How do I check my own doc changes before shipping them?

Point `ESTATE_KNOWLEDGE_INDEX` at a private path, regenerate, and query
against that copy — never against `~/.local/state/estate/...`'s live index
until you're ready for it to move:

```
export ESTATE_KNOWLEDGE_INDEX=/tmp/my-index.json
go run ./src/estate knowledge                       # regenerate
go run ./src/estate knowledge query "<question>"     # try it
go run ./src/estate knowledge query --json "<question>"
```

`go run ./src/estate/cmd/goldenquery [-bin estate] [-v]` runs the checked-in
golden set and the natural-language stratum (both unscoped and
`source:repo-docs`-scoped) against a real, already-compiled index via the
real CLI — never a reimplementation of ranking, never an LLM judge. It always
exits 0 once the measurement itself completes; a low hit rate is a
measurement result, not a runner failure. Six lines print at the end,
covering three independently-measured, never-averaged properties: the
natural-language stratum (top-3/top-10, unscoped and scoped), and
`cases.json`'s retrieval score in `--private` mode versus its
publishable-only score in default mode — a low publishable-only number is
expected whenever most of the golden set's answers are private, not a
ranking regression.

Regenerating `estate knowledge`'s index yourself, from your own worktree,
before trusting a doc change against it, is the only way to know whether a
new heading actually retrieves — see this file's own PR for a worked
before/after example.
