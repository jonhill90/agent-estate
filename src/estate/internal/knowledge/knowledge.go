// Package knowledge builds `estate knowledge`'s compiled index -- a
// derived, regenerable read over five sources that already exist, none
// of them owned or written by this package:
//
//   - GitHub stars (gh api user/starred --paginate)
//   - the Agent Memory vault ($AGENT_MEMORY_VAULT/agent/facts/*.md)
//   - the operator's prompt/parameter corpus (~/corpus/ledger.sqlite3,
//     the live_parameters view -- see internal/corpus's own doc comment
//     for why that path, not the one CLAUDE.md documents, is correct;
//     agent-estate#942)
//   - ~/source/repos/Personal/Loops-Research, a plain directory of
//     numbered markdown files
//   - this repository's own written rules: AGENTS.md (CLAUDE.md is the
//     same file, a symlink) and every docs/**/*.md file -- agent-estate
//     #1034, added because the rules every dispatched lane in this repo
//     is required to obey were, until this source existed, not a
//     knowledge source themselves
//
// NEVER AUTHORITATIVE. This package never writes to any of its five
// sources, never migrates or rewrites anything, and never chooses a
// storage format for the operator's own knowledge base -- that is his
// open decision, not this package's to settle. What Generate produces is
// a derived artifact: safe to delete and regenerate at any time, never
// safe to treat as a second source of truth for anything it reads.
//
// NEVER RAW PROMPTS. The corpus source reads only the live_parameters
// view (already-derived parameter statements), never the prompts table.
// This package has no code path that can read a raw prompt's own text --
// see corpusSource in corpus.go.
//
// HONEST ABSENCE. A source that cannot be read is reported as a
// SourceResult with OK=false and a Reason, never silently dropped and
// never rendered as an empty-but-present section -- the same typed-
// absence discipline internal/knowledge (src/tui) and cost.Figure.Known
// already use elsewhere in this repo.
package knowledge

import "time"

// Item is one entry in the compiled index, regardless of which source
// produced it. The three Tier fields are progressive disclosure
// (docs_structure=progressive_disclosure, a hard operator parameter): a
// reader who stops at Tier1 alone still has something true and useful;
// Tier2 adds context; Tier3 points at (never duplicates, for the corpus
// source never quotes raw text out of) the source itself.
type Item struct {
	// ID is derived from Permalink alone (see id.go's itemID) -- an
	// "it-<16 hex chars>" value, stable across two Generate calls over
	// the same sources, because it is a function of the item's own
	// permalink rather than the wall clock at compile time
	// (agent-estate#1026: the clock-based scheme this replaced changed
	// on every regenerate, breaking `get <id>` the moment an id was
	// written into anything durable).
	ID string `json:"id"`

	// Source names which of the five readers produced this item --
	// "github-stars", "vault-fact", "corpus-parameter", "loops-research"
	// or "repo-docs".
	Source string `json:"source"`

	// Permalink is a URL or filesystem path a reader can actually open
	// to reach this item's own source, one per item as the operator's
	// conventions require.
	Permalink string `json:"permalink"`

	// StructuralTags are bare, organisational -- lifted mechanically
	// from a field the source already carries (a fact's own `type:`,
	// a corpus parameter's own `weight`/`status`), never invented by
	// this package.
	StructuralTags []string `json:"structural_tags,omitempty"`

	// SynapticTags are #hashtag, associative -- lifted mechanically from
	// a source's own tagging (GitHub's own `topics` array is the only
	// source here that carries any), kept as a visibly distinct class
	// from StructuralTags rather than merged with it.
	SynapticTags []string `json:"synaptic_tags,omitempty"`

	// Tier1 is one line, true and useful standing alone.
	Tier1 string `json:"tier1"`
	// Tier2 is a short paragraph of additional context, for three of
	// the four sources. The vault-fact source is the exception
	// (agent-estate#1027): its Tier2 carries the fact's own full body,
	// not a summary of it, because that body was ~86% of the fact and
	// entirely unsearchable otherwise -- query.go's searchableText
	// already reads Tier1+Tier2, so this is what made vault fact bodies
	// searchable without any change to query.go itself. The disclosure
	// boundary is unmoved: Match (query.go) carries Tier1 only, so this
	// content is searchable but still not returned until Get is called.
	Tier2 string `json:"tier2,omitempty"`
	// Tier3 is where the full material lives -- a pointer (Permalink
	// repeated, a file path, a "read the fact itself" instruction),
	// never the raw material inlined for the corpus source.
	Tier3 string `json:"tier3,omitempty"`

	// Publishable states whether this item may leave the operator's own
	// machine -- pasted into a public artifact, shown to a caller with
	// no explicit request for private material. Set once, here in the
	// compile step (see classify in classify.go), and carried verbatim
	// into index.json; a caller reads it, it never recomputes it.
	//
	// UNCLASSIFIED MEANS PRIVATE (agent-estate#1028). The zero value of
	// this field is false, and classify's own default branch returns
	// false for any source it does not positively know to be public --
	// so an item this package cannot classify, or a new source added
	// tomorrow that nobody has updated classify for, is private by
	// construction, not by a filter someone remembered to add at query
	// time. See classify.go's own doc comment for what this default-deny
	// rule does and does not catch.
	Publishable bool `json:"publishable"`

	// PublishBasis states, in one short phrase, why Publishable has the
	// value it does -- so an audit of the compiled index never has to
	// guess which rule produced a given item's classification. Always
	// set alongside Publishable, by the same classify call.
	PublishBasis string `json:"publish_basis"`

	// PromptID is the corpus's own prompts.id this item's prompt_id
	// column names -- the trace agent-estate#1031 asks this package to
	// carry, so a query result can be joined back to the operator's own
	// words instead of stopping at a distillation. Set only for
	// Source == "corpus-parameter"; empty for the other three sources,
	// each of whose own Permalink already IS its trace to origin (a
	// vault fact's own file path, a star's own html_url, a
	// Loops-Research file's own path) -- corpus-parameter is the one
	// source whose Permalink (corpus:item:<id>) names the distillation
	// and stops there, per #1031's own framing.
	//
	// THE ID ONLY, NEVER THE TEXT. This carries prompts.id, the same
	// bare identifier corpus.go's own query already reads -- never
	// prompts.text_raw or prompts.text_clean. See corpus.go's own doc
	// comment for why no query in this package reaches either text
	// column, and Item's own "UNCLASSIFIED MEANS PRIVATE" note above: a
	// traceable pointer to private material is still private material,
	// so this id is exactly as sensitive as the item it sits on, not a
	// side channel around Publishable.
	PromptID string `json:"prompt_id,omitempty"`
}

// SourceResult is what one of the five readers actually managed --
// Count and Items only meaningful when OK. A source that failed still
// gets an entry here, with Reason, so failure is a visible line in the
// output rather than a silently smaller Items slice.
type SourceResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
	Count  int    `json:"count"`
}

// Result is Generate's full output -- the whole of what gets written to
// disk (see Write in write.go) and what a caller (estate's own CLI, the
// TUI's compiled-index pane) reads.
type Result struct {
	// GeneratedAt is when this Result was built, UTC. Every generated
	// file carries this per the operator's own convention.
	GeneratedAt time.Time `json:"generated_at"`
	// StalenessRule states, in one sentence, when a reader should no
	// longer trust this Result without regenerating it.
	StalenessRule string `json:"staleness_rule"`
	// Note is the "derived, never authoritative" statement itself,
	// repeated here (not just in this package's doc comment) so it
	// travels with the artifact.
	Note    string         `json:"note"`
	Sources []SourceResult `json:"sources"`
	Items   []Item         `json:"items"`
}

const stalenessRule = "stale the moment any of its five sources changes; " +
	"this Result carries no freshness check of its own beyond generated_at " +
	"-- regenerate with `estate knowledge` before trusting a count here " +
	"over a live read of the source"

const derivedNote = "GENERATED by `estate knowledge`. This is a derived, " +
	"regenerable index over sources it only ever reads -- it is not, and " +
	"must never be treated as, the authoritative home for any of them. " +
	"Do not hand-edit this file; regenerate it instead."

// Config is every input Generate needs, each one overridable so tests
// never touch a real vault, corpus or filesystem tree.
type Config struct {
	VaultDir      string // $AGENT_MEMORY_VAULT
	CorpusDBPath  string // ~/corpus/ledger.sqlite3 by default
	LoopsResearch string // ~/source/repos/Personal/Loops-Research by default
	// RepoRoot is this agent-estate checkout's own root -- the directory
	// containing AGENTS.md and docs/. See write.go's findRepoRoot for how
	// DefaultConfig resolves it; empty means repoDocsSource reports
	// itself unresolved rather than guessing a path (docs.go).
	RepoRoot string
	// RunGH executes a gh CLI invocation and returns its stdout --
	// the seam stars.go reads through, so a test never shells out to
	// the real gh binary or a real network. nil means "use the real
	// gh binary" (see stars.go's defaultGHRunner).
	RunGH func(args ...string) ([]byte, error)
}
