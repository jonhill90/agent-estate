# How does knowledge retrieval work in this repo?

This describes `estate knowledge`'s compiled index as it exists **on
`0140ec1`** (2026-09-04) — the mechanism, not a decision. Every number below
carries the commit it was measured on; treat an unstamped figure elsewhere as
a lead, not a fact. The one exception is the scoping section below, which
deliberately stopped stamping stratum hit rates as of this commit — see that
section for why.

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
and tags weighted 3x Tier2 as a BM25 field weight (tier1FieldWeight=3,
tier2FieldWeight=1, agent-estate#1043's measured ratio). For repo-docs
items only, a section's own leaf heading is scored as Tier1 (the 3x weight)
while its ancestor heading path is a separate field weighted 1x
(ancestorFieldWeight=1, agent-estate#1113) — the full heading path no
longer counts at the leaf's 3x weight, and every other source has no
ancestor field to split out. There is no minimum-match floor: one rare term
matching once can outrank several common terms matching by coincidence, so
an item needs only one weighted term match (score > 0) to be returned at all
(agent-estate#1054).

**The score printed to a caller is BM25's own figure rounded to the nearest
integer for display, applied after ranking.** Sorting itself runs on the
unrounded float, breaking ties only on genuine float equality — so two
printed results sharing the same displayed score are not necessarily a tie;
they may simply have rounded together. Measured on `81034df`: most of an
ordinary ten-result page shares its printed score with at least one other
result, purely from rounding.

## How do I scope a query to one source? (source:<name> scoping)

Prepend a `source:<name>` token to the question — e.g.
`source:repo-docs how does dispatch work`. This is an exact tag filter,
applied **before** term scoring: it narrows the candidate set to items
carrying that exact structural tag, then ranks only within that narrowed set
(agent-estate#1024). It is orthogonal to term search, not a substitute for
it — combine it with real question words, not instead of them. A bare word
with no colon (`repo-docs` alone, with no `source:` prefix) is scored as an
ordinary search term, not treated as a filter.

**Scoping is a real lever in `--private` mode and structurally inert in
public mode for repo questions — know which one applies before recommending
it to a caller.** This section used to carry a four-arm hit-rate table
stamped to a commit. It doesn't anymore, on purpose (agent-estate#1165):
that table's denominator was the checked-in natural-language stratum's case
count, and agent-estate#1172 grew that count (12 → 21) two hours after the
table was last re-measured and re-stamped, which made the table wrong again
before the ink dried. This is the second time this section carried a
fixture-derived figure that stopped reproducing — the first was
agent-estate#1081's "scoping doubles retrieval" claim, removed by
agent-estate#1167 — and re-stamping does not fix a doc that has no way to
know when the fixture it quotes has moved. Run the measurement instead of
reading it:

```
go run ./src/estate/cmd/goldenquery
```

prints, always freshly, both public-mode arms (unscoped and
`source:repo-docs`-scoped) and the `--private` scoped arm's own hit rates
against the current fixture and the current index, labelled with the case
count they were measured against. (It does not currently print the
`--private`, unscoped arm as a fourth line of its own — that combination can
be reproduced by hand: run the same natural-language questions through
`knowledge query --private` with no `source:` prefix.)

**Why public-mode scoping is inert, not just currently measured at zero
gain.** Only 2 of the index's 9 sources are publishable — `repo-docs` and
`github-stars` — so in public mode `source:repo-docs` can only ever remove
`github-stars` items. On every repo-oriented natural-language question
measured so far, across two different case counts (12 on agent-estate#1162,
21 after agent-estate#1172), `github-stars` never outranked `repo-docs`, so
the public unscoped and public scoped arms have always come back identical,
case for case, at every ordinal rank (agent-estate#1162, agent-estate#1165).
Scoping a public-mode repo question does nothing here, not because scoping
is weak, but because there is nothing else in the reachable set for it to
remove — this is a structural argument, not a number that could drift with
the fixture, which is why it survives here as prose while the hit-rate table
did not.

**Why `--private` scoping is worth doing.** In `--private` mode all 9
sources compete, and `source:repo-docs` has real work to do: on both case
counts measured so far, scoping never made the natural-language stratum's
hit rate worse and recovered cases that missed the unscoped top-10 entirely.
**If you know which of the nine sources should answer a question — and for
anything about this repo's own rules or mechanism, that source is
`repo-docs` — scope the query, but expect the gain only when the call itself
is `--private`** (or when other private sources are in play as
competitors). An unknown `source:` value is refused honestly as `no_match`
(exit 1), never silently ignored and never silently falling through to an
unscoped search over the whole index (agent-estate#1024).

**Do not scope on a guess.** `source:` is a filter, not a re-ranking hint: a
wrong guess deletes the answer outright rather than demoting it. Measured
harmful (agent-estate#1059): a wrong `source:` value took cases that would
otherwise land at rank 6, 8, and 5 down to no match at all (`6->None`,
`8->None`, `5->None`, across 24 cases in `cases.json`, a fixture
agent-estate#1172 did not touch). Scope only when you can name which of the
nine sources should hold the answer; scoping to be safe is the opposite of
safe.

**A table once stood here claiming `2df4d3f` measured 4/12 → 8/12 top-3 and
10/12 → 11/12 top-10, both arms in public mode, calling scoping "the single
largest quality lever available to a caller."** That pairing never
reproduced on any commit measured since, including `2df4d3f` itself — every
public-mode pair measured so far has come back identical (see above). The
number reached this doc via agent-estate#1081 and is recorded on
agent-estate#1162 as **unverified, not refuted**. The most likely
explanation is that the original pairing compared top-3 against top-10
rather than unscoped against scoped, but that is a suspicion, not a proven
origin, and is stated here as one.

## What happens when nothing (or the wrong thing) answers a question?

Four distinct absences, on purpose never collapsed into one "nothing here"
shape, each with its own exit code from `estate knowledge query`:

| exit | state | means |
|---|---|---|
| 0 | `matched` (or `matched_withheld_majority`) | at least one publishable item scored above zero and was returned |
| 1 | `no_match` | the index was read fine; nothing in it scored above zero against this question — a real, empty answer, not a failure |
| 2 | `index_missing` / `index_unreadable` | the compiled index itself could not be read at all — run `estate knowledge` first |
| 3 | `withheld_private` | something answers this question, but every matching item is private and this call did not ask for private material |

**In practice, exit 3 fires when the public sources themselves were absent or
unreadable when the index was built** — a checkout with no `AGENTS.md`
reachable above it, `gh` off `PATH` — not as a routine outcome of a
private-heavy question against a normally-built index. Measured on `81034df`:
against the live index, with every source readable, even a deliberately
private-heavy question still scrapes at least one public match and lands on
`matched_withheld_majority` (exit 0) instead; two independent attempts to
reach `withheld_private` that way both failed for this reason.

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

**A fifth situation hides inside `no_match` itself: an empty index.** An
index that is present, valid, readable and fresh but carries zero `items` — a
truncated or partial write, a full disk, an interrupted regeneration (#1123
narrowed how this happens but did not close it) — reads exactly like
`no_match` against a real, populated index that genuinely has nothing
relevant, unless something says otherwise. Both are honestly `no_match` (the
index *was* read fine, which is what that exit code already means) — this is
not a sixth state or a new exit code — but the two remedies are opposite:
rephrase the question, or regenerate a broken index. Every `QueryResult`
therefore carries `index_item_count` (the compiled index's own `len(items)`,
set on every state a real index was read for), and prose mode always prints
`index contains N item(s)` on a `no_match` result. Only the empty-index case
also gets a `reason` naming the difference explicitly — the ordinary
"nothing scored" shape against a genuinely non-empty index is unchanged by
this and still prints no reason, on purpose (agent-estate#1124's own issue
scoped the fix to the empty case, not to every `no_match`):

```
no item matches "zzqxwv qrtplm bnkdfz"
the compiled index at /path/to/index.json contains 0 items -- it was read
successfully but has nothing to answer with; this is a build defect
(truncated write, full disk, interrupted regeneration), not a phrasing
problem -- regenerate it with `estate knowledge`, do not just rephrase
the question
index contains 0 item(s)
```

against a real index's genuine miss:

```
no item matches "zzqxwv qrtplm bnkdfz"
index contains 3959 item(s)
```

(agent-estate#1124)

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
| `not_applicable` | there was no compiled index to have a coverage opinion about (`state: index_missing` or `index_unreadable`) | fix `reason` on the result, not `coverage` -- nothing to regenerate here yet |

`degraded` and `limited` are deliberately different words for a reason:
withholding private material in default mode is the boundary working as
intended, not a malfunction; a source failing to read at build time *is* a
malfunction. Conflating them would train a caller to ignore a real failure.
`coverage.reasons` always names which source (if any) and why, whenever
`coverage.state` is not `complete`.

## What does the `contradictions` field mean, and what should I do about it?

`QueryResult` carries a `contradictions` field **beside** `coverage`, never
inside it (`src/estate/internal/knowledge/query.go`,
`internal/knowledge/contradiction.go`, agent-estate#1051/#1104). The two
answer different questions: `coverage` asks *"was this search complete?"* —
every one of its states (`complete`, `limited`, `degraded`, `stale`,
`unknown`, `mixed`, `not_applicable`) is something the caller can act on by
rerunning, regenerating, trusting the answer as-is, or (for
`not_applicable`) reading `reason` on the result instead. A contradiction's
own remedy is
none of those — it is *"a human must decide which of these two is true"* —
which fits none of `coverage`'s states, so it is not one of them.

A contradiction fires when a `corpus-question` item (something the corpus
itself classified as asked-but-unresolved) and a `vault-fact` or
`corpus-directive` item (something asserting a settled position) both land
in the same result set, on the same shared terms, close enough in rank and
score to plausibly be about the same subject. All three thresholds — shared
terms, rank, score ratio — are fixed and content-blind; see
`contradiction.go`'s own doc comment for the exact values and the
measurements that picked them.

**It surfaces; it never adjudicates.** `Contradiction` names only the two
item ids, their sources, and the shared terms — there is no verdict field,
no re-ranking of either match, and no suppression of either one from the
result list. Nothing in this package decides which side is right; that
decision is the reader's, not the index's. Printed prose renders this as a
`note:` line above the match list, naming both ids and telling the reader to
read both before acting — never as a resolved fact folded into either
match's own tier text.

Absence of a contradiction is not evidence of agreement — it only means this
package's narrow, deterministic check found no pair meeting all three
thresholds in this particular result set; a real disagreement sitting
further down the ranking, or scored too far apart, will not fire.

## How do I tell which binary built the index?

Every compiled index carries `generated_by` (`src/estate/internal/knowledge/
knowledge.go`, agent-estate#1082): the commit of the checkout that ran
`estate knowledge` to build it, plus when. `QueryResult` carries the same
value forward, unchanged, as `index_generated_by`
(`internal/knowledge/query.go`) — a caller reading a query result, not just
the raw index file, can still see what built it.

**Why this exists.** Nothing before this compared the *index* against the
*binary reading it* — #1047/#1080's own staleness check is source-against-
index (has a source changed since the index was built?), which says nothing
about whether the code doing the reading is the code that built it. A stale
binary serving a query against a newer (or older) index was, before this,
undetectable from the output alone. Four incidents motivated adding it
(#1082, including a fourth recorded in its comments).

`generated_by.commit` is `"unknown"` — never a guessed value — whenever it
could not be positively determined: no git checkout resolved, `git` itself
unavailable or erroring, or **a dirty working tree**, deliberately, since a
dirty tree has no single commit that actually describes what produced the
index (`build_commit.go`'s `ResolveBuildCommit`). `unknown` here is a
correct, honest answer, not a failure to record — the same "report, never
guess" rule this doc's `coverage.state: unknown` already follows for a
different unmeasurable source (github-stars' freshness).

At query time, `estate knowledge query` resolves the **currently running**
checkout's own commit the same way and folds a real, positively-resolved
mismatch into `coverage` as `binary_mismatch`, naming both commits — printed
as a `note:` line in prose, machine-readable in `--json`. Either side being
`unknown` is never treated as a match or a mismatch; only two positively
resolved, differing commits fire it. This is detection, never prevention: an
index built by a different commit is often fine (a doc-only commit landed
since), so `binary_mismatch` is something to know, not something a query
refuses on.

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

## How do I know if a `prompt_id` can be opened?

`get`'s own `prompt_id:` line has printed a bare id for every corpus-sourced
item since agent-estate#1031, and a bare id reads as "provenance is
available" whether or not it actually is. Measured against the real corpus
(2026-09-04): only about 12% of hard items' prompts carry a non-empty
`text_clean` — the only field the operator's rules permit quoting at all
(`text_raw` is never disclosed, under any flag). Following a prompt_id leads
to nothing quotable most of the time, and a bare id cannot tell a caller
which case it is in.

`knowledge get` resolves this explicitly as a `disclosure` field
(`src/estate/internal/knowledge/disclosure.go`, agent-estate#1061/#1106) —
distinct from `Publishable`/`--private`, which gate the *item* as a whole:
`disclosure` is about whether this one item's own `prompt_id` leads
somewhere inspectable, one of four states:

| `disclosure.state` | means |
|---|---|
| `available_clean` | the corpus's own row for this `prompt_id` has a non-empty `text_clean`, and this item is visible in this call's scope — a caller MAY open it |
| `unavailable` | the row exists but `text_clean` is empty — the common case; the source was never cleaned for quoting, which is not a failure |
| `restricted` | the row (and possibly its `text_clean`) exists, but this item is private and this call did not pass `--private` |
| `source_missing` | this item's `prompt_id` does not resolve to any row in the corpus's prompts table at all — broken lineage, not merely uncleaned |

The point is that a caller can tell **"has a prompt id"** (the bare
`prompt_id:` line, true for every corpus item) from **"has inspectable
evidence"** (one of these four states, resolved by a live corpus read at
`get` time). This distinction did not exist before agent-estate#1061; the
`prompt_id:` line alone could not make it.

**This reports whether the text exists — it never prints it.**
`ResolveDisclosure` has no code path that returns `text_clean` or
`text_raw`; `disclosure.detail` names the evidence behind the state (e.g.
"text_clean exists for this item's prompt — not printed by this call"),
never the content itself. `--private` unlocks `restricted` into whichever of
the other three states actually applies; it does not change what
`ResolveDisclosure` is capable of returning. `text_raw` never leaves the
corpus through this field, under any flag.

A check that could not be performed at all — no corpus path configured, the
corpus unreadable — is reported as `provenance: could not check -- <reason>`
(prose) or `disclosure_error` (`--json`), never silently folded into one of
the four real states.

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
