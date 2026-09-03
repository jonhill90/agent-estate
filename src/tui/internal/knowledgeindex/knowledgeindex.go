// Package knowledgeindex renders `estate knowledge`'s own compiled index
// -- a derived, regenerable read over four sources (GitHub stars, the
// Agent Memory vault, the prompt/parameter corpus, Loops-Research) that
// `estate knowledge` (src/estate/internal/knowledge) already produced
// and wrote to disk. This package is a READER of that one JSON file,
// nothing more: it never generates the index itself, never talks to any
// of the four sources directly, and never writes anything.
//
// That split follows this repo's own module boundary (AGENTS.md's "What
// belongs here vs. the daemon"): compiling the index is daemon
// work (a different Go module, src/estate), reachable here only through
// the file it wrote -- the same shape internal/cost reads ccusage's own
// output and internal/board reads a copy of the ledger, never a second
// implementation of either.
//
// Item and Result below are an independent, JSON-tag-compatible mirror
// of src/estate/internal/knowledge's own types, not an import of that
// module -- src/tui has never imported src/estate (AGENTS.md's "Never
// here: a second reader of tmux, a second ledger" is the same discipline
// applied to a different daemon boundary), and this package's own doc
// comment above is what states that boundary rather than a build-time
// dependency enforcing it.
package knowledgeindex

import "time"

// Item mirrors src/estate/internal/knowledge.Item field-for-field via
// its JSON tags.
type Item struct {
	ID             string   `json:"id"`
	Source         string   `json:"source"`
	Permalink      string   `json:"permalink"`
	StructuralTags []string `json:"structural_tags,omitempty"`
	SynapticTags   []string `json:"synaptic_tags,omitempty"`
	Tier1          string   `json:"tier1"`
	Tier2          string   `json:"tier2,omitempty"`
	Tier3          string   `json:"tier3,omitempty"`
}

// SourceResult mirrors src/estate/internal/knowledge.SourceResult -- a
// source's own honest outcome, OK=false carrying a real Reason rather
// than a silently absent line.
type SourceResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
	Count  int    `json:"count"`
}

// Result mirrors src/estate/internal/knowledge.Result -- the whole file
// `estate knowledge` writes.
type Result struct {
	GeneratedAt   time.Time      `json:"generated_at"`
	StalenessRule string         `json:"staleness_rule"`
	Note          string         `json:"note"`
	Sources       []SourceResult `json:"sources"`
	Items         []Item         `json:"items"`
}
